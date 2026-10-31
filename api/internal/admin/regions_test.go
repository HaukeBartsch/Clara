package admin

// The preferred region (REQ-API-175…179, REQ-AUTH-088/089, REQ-AUD-036): the user's own
// choice, the provider role mapping an administrator keeps, the region a sign-in derives
// from the roles claim, the placeholder an assignment sets, and the per-actor
// in_preferred_region flag of the project list.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// reload reads a user back the way the boundary middleware does on every request, so a
// region set by one call is what the next call's actor carries.
func (e *env) reload(u *db.User) *db.User {
	e.t.Helper()
	got, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil || got == nil {
		e.t.Fatalf("GetUser %d: %v", u.ID, err)
	}
	return got
}

// countType counts the audit events of one type written so far.
func (e *env) countType(eventType string) int {
	e.t.Helper()
	n := 0
	for _, t := range e.auditTypes() {
		if t == eventType {
			n++
		}
	}
	return n
}

// mustRoleMapping enters one reviewed mapping row directly, the way an administrator's
// decision leaves it.
func (e *env) mustRoleMapping(issuer, value, region string) {
	e.t.Helper()
	ctx := context.Background()
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		e.t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	if _, err := e.Store.CreateProviderRoleRegionTx(ctx, tx, &db.ProviderRoleRegion{
		Issuer: issuer, RoleValue: value, Region: sql.NullString{String: region, Valid: region != ""},
	}); err != nil {
		e.t.Fatalf("CreateProviderRoleRegionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		e.t.Fatalf("Commit: %v", err)
	}
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestNormalizeRoleValues(t *testing.T) {
	long := strings.Repeat("x", db.MaxRoleValueLength+1)
	widest := strings.Repeat("ø", db.MaxRoleValueLength) // 255 characters, 510 bytes: kept
	many := make([]string, 0, maxRoleValues+10)
	for i := range maxRoleValues + 10 {
		many = append(many, "role-"+itoa(int64(i)))
	}
	manyRaw, _ := json.Marshal(many)

	cases := []struct {
		name      string
		raw       string
		want      []string
		tooLong   int
		overLimit int
	}{
		{"single string", `" HSO-Ahus "`, []string{"HSO-Ahus"}, 0, 0},
		{"array keeps strings only", `["a", 7, null, {"b": 1}, "c"]`, []string{"a", "c"}, 0, 0},
		{"trimmed duplicates collapse", `["a", " a", "", "  "]`, []string{"a"}, 0, 0},
		{"object is nothing", `{"a": "OUS"}`, nil, 0, 0},
		{"number is nothing", `42`, nil, 0, 0},
		{"too long is skipped, not truncated", `["` + long + `", "` + widest + `"]`, []string{widest}, 1, 0},
		{"bounded to fifty", string(manyRaw), many[:maxRoleValues], 0, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, tooLong, overLimit := normalizeRoleValues(json.RawMessage(c.raw))
			if !slices.Equal(got, c.want) || tooLong != c.tooLong || overLimit != c.overLimit {
				t.Errorf("got %d values %v (too long %d, over limit %d), want %v (%d, %d)",
					len(got), got, tooLong, overLimit, c.want, c.tooLong, c.overLimit)
			}
		})
	}
}

func TestDeriveRegionTakesEnumerationOrder(t *testing.T) {
	mapped := map[string]string{"sun": "SUN", "ahus": "Ahus", "gone": "WITHDRAWN", "other": "OTHER"}
	cases := []struct {
		values []string
		want   string
	}{
		{[]string{"sun", "ahus"}, "Ahus"}, // Ahus precedes SUN in REQ-DB-006, whatever the claim order
		{[]string{"other", "gone"}, "OTHER"},
		{[]string{"gone"}, "WITHDRAWN"}, // a withdrawn code still wins when nothing current maps
		{[]string{"unmapped"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := deriveRegion(c.values, mapped); got != c.want {
			t.Errorf("deriveRegion(%v) = %q, want %q", c.values, got, c.want)
		}
	}
}

// TestMyRegion covers GET/PUT /api/v1/users/me/region (REQ-API-175): a code sets the
// user's own choice, null returns to the automatic region, anything else is 400, and only
// a change is audited (REQ-AUD-036).
func TestMyRegion(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("me@example.org")
	ctx := context.Background()
	if err := e.Store.SetProviderRegion(ctx, u.ID, "ST"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}

	read := func(method string, body any) regionState {
		t.Helper()
		rec := e.do(method, "/api/v1/users/me/region", body, e.reload(u))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", method, rec.Code, rec.Body.String())
		}
		var s regionState
		e.decode(rec, &s)
		return s
	}

	got := read("GET", nil)
	if strOrEmpty(got.Region) != "ST" || got.PreferredRegion != nil || strOrEmpty(got.AutomaticRegionSource) != "provider" {
		t.Errorf("initial state = %+v, want the automatic ST", got)
	}

	got = read("PUT", map[string]any{"region": "SØ"})
	if strOrEmpty(got.Region) != "SØ" || strOrEmpty(got.PreferredRegion) != "SØ" || strOrEmpty(got.AutomaticRegion) != "ST" {
		t.Errorf("after choice = %+v, want SØ chosen over the kept ST", got)
	}
	if n := e.countType("preferred_region_changed"); n != 1 {
		t.Fatalf("preferred_region_changed entries = %d, want 1", n)
	}
	details := lastAuditDetails(t, e, "preferred_region_changed")
	if details["old"] != nil || details["new"] != "SØ" {
		t.Errorf("audit details = %v, want old null, new SØ", details)
	}

	read("PUT", map[string]any{"region": "SØ"})
	if n := e.countType("preferred_region_changed"); n != 1 {
		t.Errorf("an unchanged value wrote an entry: %d entries, want 1", n)
	}

	got = read("PUT", map[string]any{"region": nil})
	if strOrEmpty(got.Region) != "ST" || got.PreferredRegion != nil {
		t.Errorf("after Automatic = %+v, want the automatic ST back", got)
	}

	for _, body := range []any{
		map[string]any{"region": "Oslo"},
		map[string]any{"region": "ous"},
		map[string]any{"region": 3},
		map[string]any{},
		map[string]any{"region": "OUS", "theme": "dark"},
		"not json",
	} {
		rec := e.do("PUT", "/api/v1/users/me/region", body, e.reload(u))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %v status = %d, want 400", body, rec.Code)
		}
	}
	if n := e.countType("preferred_region_changed"); n != 2 {
		t.Errorf("preferred_region_changed entries = %d, want 2 (choice and Automatic)", n)
	}
}

// TestProviderRolesRequireAdmin: the mapping is administrator-only (REQ-API-177/178).
func TestProviderRolesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("user@example.org")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/provider-roles"},
		{"POST", "/api/v1/provider-roles"},
		{"PUT", "/api/v1/provider-roles/1"},
		{"DELETE", "/api/v1/provider-roles/1"},
	} {
		rec := e.do(c.method, c.path, map[string]any{"region": "OUS"}, u)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", c.method, c.path, rec.Code)
		}
	}
}

// TestProviderRolesCRUD covers the mapping endpoints (REQ-API-177/178): entering a value
// ahead of any sign-in, deciding on a value a sign-in recorded, removing one, and the
// region_mapping_changed entry each change writes (REQ-AUD-036).
func TestProviderRolesCRUD(t *testing.T) {
	e := newEnv(t)
	const issuer = "https://idp.example.org/idp"
	e.Cfg.OAuth2 = []config.OAuth2Provider{providerAtIssuer(issuer)}
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	if err := e.Store.RecordRoleSightings(ctx, issuer, []string{"HSO-OUS-Ansatt"}, time.Now()); err != nil {
		t.Fatalf("RecordRoleSightings: %v", err)
	}

	rec := e.do("POST", "/api/v1/provider-roles",
		map[string]any{"issuer": issuer, "role_value": " HSO-Ahus-Ansatt ", "region": "Ahus"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created providerRoleObject
	e.decode(rec, &created)
	if created.RoleValue != "HSO-Ahus-Ansatt" || strOrEmpty(created.Region) != "Ahus" || !created.Reviewed ||
		created.SeenCount != 0 || created.FirstSeenAt != nil {
		t.Errorf("created = %+v, want a trimmed, reviewed, never-seen row", created)
	}
	details := lastAuditDetails(t, e, "region_mapping_changed")
	if details["action"] != "add" || details["role_value"] != "HSO-Ahus-Ansatt" || details["issuer"] != issuer {
		t.Errorf("add details = %v", details)
	}

	for _, c := range []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"duplicate", map[string]any{"issuer": issuer, "role_value": "HSO-Ahus-Ansatt", "region": "OUS"}, http.StatusConflict},
		{"unconfigured issuer", map[string]any{"issuer": "https://elsewhere.example.org", "role_value": "x", "region": "OUS"}, http.StatusBadRequest},
		{"empty value", map[string]any{"issuer": issuer, "role_value": "  ", "region": "OUS"}, http.StatusBadRequest},
		{"long value", map[string]any{"issuer": issuer, "role_value": strings.Repeat("x", 256), "region": "OUS"}, http.StatusBadRequest},
		{"missing region", map[string]any{"issuer": issuer, "role_value": "x"}, http.StatusBadRequest},
		{"unknown region", map[string]any{"issuer": issuer, "role_value": "x", "region": "Oslo"}, http.StatusBadRequest},
		{"unknown attribute", map[string]any{"issuer": issuer, "role_value": "x", "region": "OUS", "user_id": 1}, http.StatusBadRequest},
	} {
		if rec := e.do("POST", "/api/v1/provider-roles", c.body, admin); rec.Code != c.status {
			t.Errorf("create %s status = %d, want %d (body %s)", c.name, rec.Code, c.status, rec.Body.String())
		}
	}

	rec = e.do("GET", "/api/v1/provider-roles", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []providerRoleObject
	e.decode(rec, &list)
	if len(list) != 2 || list[0].RoleValue != "HSO-OUS-Ansatt" || list[0].Reviewed || list[0].SeenCount != 1 {
		t.Fatalf("list = %+v, want the undecided sighting first", list)
	}
	seen := list[0].ID

	entries := e.countType("region_mapping_changed")
	rec = e.do("PUT", "/api/v1/provider-roles/"+itoa(seen), map[string]any{"region": nil}, admin)
	var decided providerRoleObject
	e.decode(rec, &decided)
	if rec.Code != http.StatusOK || !decided.Reviewed || decided.Region != nil {
		t.Errorf("names-no-region status = %d, row = %+v", rec.Code, decided)
	}
	if e.countType("region_mapping_changed") != entries+1 {
		t.Error("deciding an undecided value wrote no entry")
	}
	e.do("PUT", "/api/v1/provider-roles/"+itoa(seen), map[string]any{"region": nil}, admin)
	if e.countType("region_mapping_changed") != entries+1 {
		t.Error("repeating a decision wrote an entry")
	}
	rec = e.do("PUT", "/api/v1/provider-roles/"+itoa(seen), map[string]any{"region": "OUS"}, admin)
	e.decode(rec, &decided)
	if rec.Code != http.StatusOK || strOrEmpty(decided.Region) != "OUS" {
		t.Errorf("map status = %d, row = %+v", rec.Code, decided)
	}
	details = lastAuditDetails(t, e, "region_mapping_changed")
	if r, _ := details["region"].(map[string]any); details["action"] != "update" || r["old"] != nil || r["new"] != "OUS" {
		t.Errorf("update details = %v", details)
	}

	if rec := e.do("PUT", "/api/v1/provider-roles/999", map[string]any{"region": "OUS"}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("update unknown id status = %d, want 404", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/provider-roles/"+itoa(seen), map[string]any{"region": "Oslo"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("update unknown region status = %d, want 400", rec.Code)
	}

	if rec := e.do("DELETE", "/api/v1/provider-roles/"+itoa(seen), nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if details := lastAuditDetails(t, e, "region_mapping_changed"); details["action"] != "remove" {
		t.Errorf("remove details = %v", details)
	}
	if rec := e.do("DELETE", "/api/v1/provider-roles/"+itoa(seen), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", rec.Code)
	}
}

// useRolesIdP configures the test provider to read its roles from claim "groups".
func (e *env) useRolesIdP(t *testing.T) (*testIdP, config.OAuth2Provider) {
	t.Helper()
	idp, _ := e.useIdP(t)
	provider := idp.withProvider(func(p *config.OAuth2Provider) { p.RolesAttr = "groups" })
	e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
	return idp, provider
}

// rolesLogin signs email in through the test provider with these extra ID-token claims.
func rolesLogin(t *testing.T, e *env, idp *testIdP, issuer, email string, claims map[string]any) UserObject {
	t.Helper()
	all := map[string]any{"email": email, "email_verified": true}
	for k, v := range claims {
		all[k] = v
	}
	body := loginArgs(email, idp.mint(all), "")
	body["provider"] = issuer
	rec := e.do("POST", "/api/v1/auth/login", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	return obj
}

// TestSignInSetsRegionFromProviderRoles is Sequence C step 3.5 (REQ-AUTH-088): every value
// is recorded, the first mapped region in enumeration order becomes the automatic region,
// the login response carries it, and no role value reaches the audit trail.
func TestSignInSetsRegionFromProviderRoles(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useRolesIdP(t)
	u := e.mustUser("sso@example.org")
	e.mustRoleMapping(provider.Issuer, "HSO-SUN", "SUN")
	e.mustRoleMapping(provider.Issuer, "HSO-Ahus", "Ahus")

	obj := rolesLogin(t, e, idp, provider.Issuer, u.Email,
		map[string]any{"groups": []string{"HSO-SUN", "HSO-Ahus", "Brand-New-Group"}})
	if strOrEmpty(obj.Region) != "Ahus" || strOrEmpty(obj.AutomaticRegionSource) != "provider" {
		t.Errorf("login region = %v from %v, want Ahus from provider", strOrEmpty(obj.Region), strOrEmpty(obj.AutomaticRegionSource))
	}
	if stored := e.reload(u); stored.AutomaticRegion.String != "Ahus" {
		t.Errorf("stored automatic region = %q, want Ahus", stored.AutomaticRegion.String)
	}

	fresh, err := e.Store.GetProviderRoleRegionByValue(context.Background(), provider.Issuer, "Brand-New-Group")
	if err != nil || fresh == nil || fresh.Reviewed || fresh.SeenCount != 1 {
		t.Errorf("new value row = %+v (err %v), want recorded undecided once", fresh, err)
	}

	var raw string
	if err := e.Store.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type = 'login_success'`).Scan(&raw); err != nil {
		t.Fatalf("login_success details: %v", err)
	}
	if strings.Contains(raw, "HSO-") || strings.Contains(raw, "Brand-New-Group") {
		t.Errorf("login_success details carry role values: %s", raw)
	}

	// Mapping changes reach the account at its next sign-in.
	e.mustRoleMapping(provider.Issuer, "HSO-OUS", "OUS")
	obj = rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": "HSO-OUS"})
	if strOrEmpty(obj.Region) != "OUS" {
		t.Errorf("second login region = %q, want OUS", strOrEmpty(obj.Region))
	}
}

// TestSignInWithoutMappedRolesKeepsRegion: a sign-in whose values map nothing leaves the
// region as it was, and the user's own choice outranks any provider region.
func TestSignInWithoutMappedRolesKeepsRegion(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useRolesIdP(t)
	u := e.mustUser("sso@example.org")
	ctx := context.Background()
	if err := e.Store.SetProviderRegion(ctx, u.ID, "SI"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}

	obj := rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": []string{"unmapped"}})
	if strOrEmpty(obj.Region) != "SI" {
		t.Errorf("region = %q, want SI kept when nothing maps", strOrEmpty(obj.Region))
	}
	obj = rolesLogin(t, e, idp, provider.Issuer, u.Email, nil) // no claim at all
	if strOrEmpty(obj.Region) != "SI" {
		t.Errorf("region = %q, want SI kept without a claim", strOrEmpty(obj.Region))
	}

	rec := e.do("PUT", "/api/v1/users/me/region", map[string]any{"region": "VVHF"}, e.reload(u))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}
	e.mustRoleMapping(provider.Issuer, "HSO-OUS", "OUS")
	obj = rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": []string{"HSO-OUS"}})
	if strOrEmpty(obj.Region) != "VVHF" || strOrEmpty(obj.AutomaticRegion) != "OUS" {
		t.Errorf("region = %q (automatic %q), want the own VVHF over the provider's OUS",
			strOrEmpty(obj.Region), strOrEmpty(obj.AutomaticRegion))
	}
}

// TestSignInNeverRefusedForRoles: a malformed claim, an oversized one and a mapping store
// that cannot be written all leave the login as it would be without roles (REQ-AUTH-088).
func TestSignInNeverRefusedForRoles(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useRolesIdP(t)
	u := e.mustUser("sso@example.org")

	rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": map[string]any{"OUS": true}})

	many := make([]string, 0, 80)
	for i := range 80 {
		many = append(many, "group-"+itoa(int64(i)))
	}
	many = append(many, strings.Repeat("y", 300))
	rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": many})
	rows, err := e.Store.ListProviderRoleRegions(context.Background())
	if err != nil {
		t.Fatalf("ListProviderRoleRegions: %v", err)
	}
	if len(rows) != maxRoleValues {
		t.Errorf("recorded %d values, want the first %d", len(rows), maxRoleValues)
	}

	if _, err := e.Store.DB.Exec(`DROP TABLE provider_role_regions`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	obj := rolesLogin(t, e, idp, provider.Issuer, u.Email, map[string]any{"groups": []string{"HSO-OUS"}})
	if obj.ID != u.ID || obj.Region != nil {
		t.Errorf("login = %+v, want the account signed in without a region", obj)
	}
}

// TestSignInReadsRolesFromUserinfo: a provider that keeps the roles out of the ID token is
// read through userinfo — but only a response about the ID token's own subject counts.
func TestSignInReadsRolesFromUserinfo(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useRolesIdP(t)
	u := e.mustUser("sso@example.org")
	e.mustRoleMapping(provider.Issuer, "HSO-SSHF", "SSHF")

	idp.userInfoClaims = map[string]any{"sub": "someone-else", "groups": []string{"HSO-SSHF"}}
	obj := rolesLogin(t, e, idp, provider.Issuer, u.Email, nil)
	if obj.Region != nil {
		t.Errorf("region = %q from another subject's userinfo, want none", strOrEmpty(obj.Region))
	}

	idp.userInfoClaims = map[string]any{"sub": "subject-42", "groups": []string{"HSO-SSHF"}}
	obj = rolesLogin(t, e, idp, provider.Issuer, u.Email, nil)
	if strOrEmpty(obj.Region) != "SSHF" {
		t.Errorf("region = %q, want SSHF from the subject's userinfo", strOrEmpty(obj.Region))
	}
}

// TestOAuthDiagnosticPreviewsRegion: the Authentication Test shows what the mapping says
// about each value and the region a sign-in would set, and records nothing (§2.11).
func TestOAuthDiagnosticPreviewsRegion(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useRolesIdP(t)
	e.mustRoleMapping(provider.Issuer, "HSO-SiV", "SiV")
	flow := beginOAuthTransaction(t, e, idp)
	idp.tokenClaims = map[string]any{"nonce": flow["nonce"], "email": "sso@example.org", "email_verified": true,
		"groups": []string{"HSO-SiV", "Unknown"}}

	_, body := oauthTestWithAdmin(t, e, flow)
	var report struct {
		RegionPreview struct {
			Claim        string           `json:"claim"`
			ClaimPresent bool             `json:"claim_present"`
			Values       []map[string]any `json:"values"`
			Region       *string          `json:"region"`
		} `json:"region_preview"`
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := report.RegionPreview
	if p.Claim != "groups" || !p.ClaimPresent || strOrEmpty(p.Region) != "SiV" || len(p.Values) != 2 {
		t.Fatalf("preview = %+v", p)
	}
	if p.Values[1]["value"] != "Unknown" || p.Values[1]["listed"] != false {
		t.Errorf("unknown value = %v, want listed false", p.Values[1])
	}
	if row, _ := e.Store.GetProviderRoleRegionByValue(context.Background(), provider.Issuer, "Unknown"); row != nil {
		t.Errorf("preview recorded %+v, want nothing written", row)
	}
}

// TestMemberAddSetsRegionPlaceholder covers REQ-API-179: the assigning administrator's
// region becomes the placeholder of an account without one, and nothing else changes.
func TestMemberAddSetsRegionPlaceholder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	if err := e.Store.SetProviderRegion(ctx, admin.ID, "OUS"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}
	admin = e.reload(admin)
	e.mustProject("Alpha")
	e.mustProject("Beta")

	fresh := e.mustUser("fresh@example.org")
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(fresh.ID), map[string]any{}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := e.reload(fresh); got.AutomaticRegion.String != "OUS" || got.AutomaticRegionSource.String != db.RegionSourceAssignment {
		t.Errorf("placeholder = %+v, want OUS from assignment", got)
	}
	if details := lastAuditDetails(t, e, "membership_changed"); details["region_placeholder"] != "OUS" {
		t.Errorf("membership_changed details = %v, want region_placeholder OUS", details)
	}

	own := e.mustUser("own@example.org")
	if rec := e.do("PUT", "/api/v1/users/me/region", map[string]any{"region": "SUN"}, own); rec.Code != http.StatusOK {
		t.Fatalf("PUT region status = %d", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(own.ID), map[string]any{}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d", rec.Code)
	}
	if got := e.reload(own); got.AutomaticRegion.Valid || got.EffectiveRegion() != "SUN" {
		t.Errorf("own-region account = %+v, want no placeholder", got)
	}
	if _, has := lastAuditDetails(t, e, "membership_changed")["region_placeholder"]; has {
		t.Error("membership_changed names a placeholder that was not set")
	}

	// An administrator without a region has none to pass on.
	plain := e.mustAdmin("plain@example.org")
	other := e.mustUser("other@example.org")
	if rec := e.do("PUT", "/api/v1/projects/2/users/"+itoa(other.ID), map[string]any{}, plain); rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d", rec.Code)
	}
	if got := e.reload(other); got.AutomaticRegion.Valid {
		t.Errorf("account = %+v, want no region from an administrator without one", got)
	}

	// A later sign-in's provider region replaces the placeholder.
	if err := e.Store.SetProviderRegion(ctx, fresh.ID, "SI"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}
	if got := e.reload(fresh); got.EffectiveRegion() != "SI" || got.AutomaticRegionSource.String != db.RegionSourceProvider {
		t.Errorf("after sign-in = %+v, want SI from provider", got)
	}
}

// TestProjectsListMarksPreferredRegion: in_preferred_region follows the acting user's
// effective region, per call, and leaves the list order alone (REQ-API-049).
func TestProjectsListMarksPreferredRegion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	for _, p := range []struct{ name, org string }{{"Alpha", "OUS"}, {"Beta", "Ahus"}, {"Gamma", "OUS"}} {
		id, err := e.Store.CreateProject(ctx, &db.Project{ProjectName: p.name, Organization: p.org,
			ParticipantNames: "REC[0-9][0-9][0-9]"})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		mustMember(t, e, u.ID, id)
		mustMember(t, e, other.ID, id)
	}
	if err := e.Store.SetProviderRegion(ctx, other.ID, "Ahus"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}

	list := func(actor *db.User) (names []string, flagged []string) {
		t.Helper()
		rec := e.do("GET", "/api/v1/projects", nil, e.reload(actor))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var out []projectSummary
		e.decode(rec, &out)
		for _, s := range out {
			names = append(names, s.ProjectName)
			if s.InPreferredRegion {
				flagged = append(flagged, s.ProjectName)
			}
		}
		return names, flagged
	}

	names, flagged := list(u)
	if len(flagged) != 0 {
		t.Errorf("flagged = %v for a user without a region, want none", flagged)
	}
	order := names
	e.do("PUT", "/api/v1/users/me/region", map[string]any{"region": "OUS"}, e.reload(u))
	names, flagged = list(u)
	if !slices.Equal(flagged, []string{"Alpha", "Gamma"}) || !slices.Equal(names, order) {
		t.Errorf("names %v flagged %v, want order %v with Alpha and Gamma flagged", names, flagged, order)
	}
	if _, flagged := list(other); !slices.Equal(flagged, []string{"Beta"}) {
		t.Errorf("other's flagged = %v, want their own Beta", flagged)
	}
}
