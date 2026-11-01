package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// itoa renders a path parameter id.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// mustRole inserts a project role and returns its id.
func mustRole(t *testing.T, e *env, projectID int64, name string) int64 {
	t.Helper()
	id, err := e.Store.CreateRole(context.Background(), &db.Role{
		ProjectID: projectID, RoleName: name,
	}, nil, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	return id
}

// TestMembersAddReturnsTokenOnce: adding a member is 201 with the new token
// returned once (REQ-API-055); membership_changed + token_issued are
// audited and the listing reports token_present without the value.
func TestMembersAddReturnsTokenOnce(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var added memberWithToken
	e.decode(rec, &added)
	if added.UserID != u.ID || added.Email != "member@example.org" {
		t.Errorf("member = %+v", added)
	}
	if added.Token == "" {
		t.Error("token empty, want fresh token (REQ-API-055)")
	}
	if added.Role != nil {
		t.Errorf("role = %q, want null for role-less member", *added.Role)
	}

	rec = e.do("GET", "/api/v1/projects/1/users", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []memberObject
	e.decode(rec, &list)
	if len(list) != 1 || !list[0].TokenPresent || !list[0].Enabled {
		t.Errorf("listing = %+v", list)
	}

	types := e.auditTypes()
	if !hasType(types, "membership_changed") || !hasType(types, "token_issued") {
		t.Errorf("audit types = %v, want membership_changed + token_issued", types)
	}
	assertNoTokenInAudit(t, e, added.Token)
}

// TestMembersRoleChange: changing the role by name is 200 without a token;
// an unknown role name is 400 invalid_request.
func TestMembersRoleChange(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	pid := e.mustProject("8DISC")
	mustRole(t, e, pid, "data-entry")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"role": "data-entry"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"role": nil}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("role-change status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var changed memberObject
	e.decode(rec, &changed)
	if changed.Role != nil {
		t.Errorf("role = %q, want null after role-less change", *changed.Role)
	}

	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"role": "ghost"}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown role status = %d, want 400", rec.Code)
	}
}

// TestMembersRotateInvalidatesPrevious: rotation returns a new token and the
// old one stops resolving (REQ-AUTH-030); token_rotated is audited.
func TestMembersRotateInvalidatesPrevious(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)

	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"rotate_token": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rotated memberWithToken
	e.decode(rec, &rotated)
	if rotated.Token == "" || rotated.Token == added.Token {
		t.Errorf("rotated token = %q, want a fresh value", rotated.Token)
	}

	ctx := context.Background()
	if old, err := e.Store.GetAssignmentByToken(ctx, added.Token); err != nil || old != nil {
		t.Error("previous token still resolves after rotation")
	}
	if !hasType(e.auditTypes(), "token_rotated") {
		t.Errorf("audit types = %v, want token_rotated", e.auditTypes())
	}
	assertNoTokenInAudit(t, e, rotated.Token)

	// Rotation of a non-member is 404.
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(e.mustUser("other@example.org").ID),
		map[string]any{"rotate_token": true}, admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rotate non-member status = %d, want 404", rec.Code)
	}
}

// TestMembersAdoptREDCapToken: an administrator sets an existing REDCap token
// value for a member so a client with it hard-coded keeps working (REQ-AUTH-090,
// DEV-AUTH-20). The previous token dies, the value resolves, is not echoed back
// and is not audited; malformed and taken values are refused with nothing
// written, and re-adopting the member's own value is a silent no-op.
func TestMembersAdoptREDCapToken(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	e.mustProject("8DISC")
	ctx := context.Background()
	const adopted = "0123456789ABCDEF0123456789ABCDEF" // a made-up value in REDCap's form

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)
	e.do("PUT", "/api/v1/projects/1/users/"+itoa(other.ID), map[string]any{}, admin)

	// A non-member cannot be given a token: adoption is not a way to add one.
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(e.mustUser("x@example.org").ID),
		map[string]any{"adopt_token": adopted}, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("adopt for a non-member: status = %d, want 404", rec.Code)
	}

	before := len(e.auditTypes())
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"adopt_token": adopted}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), adopted) {
		t.Error("the adopted value was echoed back in the response")
	}
	var obj memberObject
	e.decode(rec, &obj)
	if obj.UserID != u.ID || !obj.TokenPresent || obj.Groups == nil {
		t.Errorf("member object = %+v", obj)
	}
	if a, err := e.Store.GetAssignmentByToken(ctx, adopted); err != nil || a == nil || a.UserID != u.ID {
		t.Fatalf("adopted token does not resolve to the member: %+v, %v", a, err)
	}
	if old, err := e.Store.GetAssignmentByToken(ctx, added.Token); err != nil || old != nil {
		t.Error("previous token still resolves after adoption")
	}
	types := e.auditTypes()
	if len(types) != before+1 || types[len(types)-1] != "token_rotated" {
		t.Errorf("audit types after adoption = %v, want one more token_rotated", types[before:])
	}
	var details string
	if err := e.Store.DB.QueryRow(`SELECT details FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"adopted":true`) || !strings.Contains(details, "member@example.org") {
		t.Errorf("token_rotated details = %s, want member_email and adopted:true", details)
	}
	assertNoTokenInAudit(t, e, adopted)

	// The member's own value again: 200, nothing changes, nothing is recorded.
	before = len(e.auditTypes())
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"adopt_token": adopted}, admin); rec.Code != http.StatusOK {
		t.Errorf("re-adopt status = %d, want 200", rec.Code)
	}
	if n := len(e.auditTypes()); n != before {
		t.Errorf("re-adopting the held value wrote %d audit entries, want 0", n-before)
	}

	// Held by another member: 409, and neither token moves.
	otherBefore, _ := e.Store.GetAssignment(ctx, other.ID, 1)
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(other.ID), map[string]any{"adopt_token": adopted}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("adopt a taken value: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if otherAfter, _ := e.Store.GetAssignment(ctx, other.ID, 1); otherAfter.Token != otherBefore.Token {
		t.Error("a refused adoption changed the other member's token")
	}
	if a, _ := e.Store.GetAssignmentByToken(ctx, adopted); a == nil || a.UserID != u.ID {
		t.Error("a refused adoption moved the value away from its holder")
	}

	// Only REDCap's form: 32 upper-case hex characters, and a string.
	for _, bad := range []any{
		"", "0123456789abcdef0123456789abcdef", "0123456789ABCDEF0123456789ABCDE",
		"0123456789ABCDEF0123456789ABCDEF0", "0123456789ABCDEF0123456789ABCDEG",
		" 0123456789ABCDEF0123456789ABCDE", "8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83", nil, 42, true,
	} {
		before = len(e.auditTypes())
		rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(other.ID), map[string]any{"adopt_token": bad}, admin)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("adopt_token %#v: status = %d, want 400", bad, rec.Code)
		}
		if n := len(e.auditTypes()); n != before {
			t.Errorf("adopt_token %#v: a refused value wrote %d audit entries", bad, n-before)
		}
	}
	if otherAfter, _ := e.Store.GetAssignment(ctx, other.ID, 1); otherAfter.Token != otherBefore.Token {
		t.Error("a malformed adoption changed the member's token")
	}

	// Mutually exclusive with the other mutations, and administrator-only.
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID),
		map[string]any{"adopt_token": adopted, "rotate_token": true}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("adopt + rotate: status = %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(other.ID),
		map[string]any{"adopt_token": "FEDCBA9876543210FEDCBA9876543210"}, u); rec.Code != http.StatusForbidden {
		t.Errorf("adopt by a non-administrator: status = %d, want 403", rec.Code)
	}

	// A later rotation replaces the adopted value with a CLARA-issued UUID.
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"rotate_token": true}, admin)
	var rotated memberWithToken
	e.decode(rec, &rotated)
	if len(rotated.Token) != 36 {
		t.Errorf("rotated token = %d characters, want a UUID", len(rotated.Token))
	}
	if a, _ := e.Store.GetAssignmentByToken(ctx, adopted); a != nil {
		t.Error("the adopted value still resolves after a rotation")
	}
}

// TestMembersRemoveRevokesToken: removal is 200, the token dies immediately,
// membership_changed (remove) + token_revoked are audited, and a second
// removal is an idempotent no-op (REQ-API-042).
func TestMembersRemoveRevokesToken(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)

	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"remove": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status = %d, body = %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	if a, err := e.Store.GetAssignmentByToken(ctx, added.Token); err != nil || a != nil {
		t.Error("token still resolves after removal")
	}
	types := e.auditTypes()
	if !hasType(types, "membership_changed") || !hasType(types, "token_revoked") {
		t.Errorf("audit types = %v, want membership_changed + token_revoked", types)
	}

	before := len(e.auditTypes())
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"remove": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("second remove status = %d, want 200", rec.Code)
	}
	if len(e.auditTypes()) != before {
		t.Error("idempotent removal wrote audit entries")
	}
}

// TestMembersRequireAdmin: the listing and the mutation are is_admin only;
// a non-admin gets the uniform 403 (REQ-API-053/054).
func TestMembersRequireAdmin(t *testing.T) {
	e := newEnv(t)
	plain := e.mustUser("plain@example.org")
	e.mustProject("8DISC")

	if rec := e.do("GET", "/api/v1/projects/1/users", nil, plain); rec.Code != http.StatusForbidden {
		t.Errorf("list status = %d, want 403", rec.Code)
	}
	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(plain.ID), map[string]any{}, plain)
	if rec.Code != http.StatusForbidden {
		t.Errorf("put status = %d, want 403", rec.Code)
	}
}

// TestMembersSelfServiceToken: a member fetches their own token; fetching
// someone else's — or without membership — is the uniform 403 (REQ-API-102,
// REQ-API-007), and no audit event is written for the fetch.
func TestMembersSelfServiceToken(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)

	rec = e.do("GET", "/api/v1/projects/1/users/"+itoa(u.ID)+"/token", nil, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("self fetch status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	e.decode(rec, &tok)
	if tok.Token != added.Token {
		t.Errorf("token = %q, want the member's token", tok.Token)
	}

	if rec := e.do("GET", "/api/v1/projects/1/users/"+itoa(u.ID)+"/token", nil, other); rec.Code != http.StatusForbidden {
		t.Errorf("fetch of another user's token status = %d, want 403", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/1/users/"+itoa(other.ID)+"/token", nil, other); rec.Code != http.StatusForbidden {
		t.Errorf("non-member fetch status = %d, want 403", rec.Code)
	}
	// the fetch itself writes no audit event (REQ-API-102) — the add above
	// stays the only token_issued entry.
	if n := countType(e.auditTypes(), "token_issued"); n != 1 {
		t.Errorf("token_issued count = %d, want 1 (fetch writes no audit)", n)
	}
}

// TestMembersTokenViewed: every unmasking of a just-issued token is its own
// token_viewed entry naming the member (REQ-AUD-033, REQ-API-160) — never the
// value. is_admin only; a non-member or a body attribute writes nothing.
func TestMembersTokenViewed(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	pid := e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)
	path := "/api/v1/projects/1/users/" + itoa(u.ID) + "/token-viewed"

	// Shown, hidden, shown again: two views, two entries.
	for i := 0; i < 2; i++ {
		if rec := e.do("POST", path, map[string]any{}, admin); rec.Code != http.StatusOK {
			t.Fatalf("view %d status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
	}
	details := e.auditDetails("token_viewed", pid)
	if len(details) != 2 || details[0]["member_email"] != "member@example.org" || len(details[0]) != 1 {
		t.Errorf("token_viewed details = %v, want two {member_email: member@example.org}", details)
	}
	// The view is an entry of its own: the add's two entries stay as they were.
	types := e.auditTypes()
	if countType(types, "token_issued") != 1 || countType(types, "membership_changed") != 1 {
		t.Errorf("audit types = %v, want one membership_changed + token_issued beside the views", types)
	}
	assertNoTokenInAudit(t, e, added.Token)

	for _, c := range []struct {
		name  string
		path  string
		body  map[string]any
		actor *db.User
		want  int
	}{
		{"non-admin", path, map[string]any{}, u, http.StatusForbidden},
		{"non-member", "/api/v1/projects/1/users/" + itoa(other.ID) + "/token-viewed", map[string]any{}, admin, http.StatusNotFound},
		{"unknown project", "/api/v1/projects/999/users/" + itoa(u.ID) + "/token-viewed", map[string]any{}, admin, http.StatusNotFound},
		{"unknown user", "/api/v1/projects/1/users/999/token-viewed", map[string]any{}, admin, http.StatusNotFound},
		{"body attribute", path, map[string]any{"token": added.Token}, admin, http.StatusBadRequest},
	} {
		if rec := e.do("POST", c.path, c.body, c.actor); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
	}
	if n := countType(e.auditTypes(), "token_viewed"); n != 2 {
		t.Errorf("token_viewed count = %d, want 2 (refused calls write nothing)", n)
	}
}

// TestMembersRejectUnknownAndAmbiguousBodies: unknown attributes are 400 and
// exactly one action attribute is required.
func TestMembersRejectUnknownAndAmbiguousBodies(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID), map[string]any{"token": "x"}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown attr status = %d, want 400", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/projects/1/users/"+itoa(u.ID),
		map[string]any{"role": nil, "remove": true}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("two actions status = %d, want 400", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/projects/999/users/"+itoa(u.ID), map[string]any{}, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/projects/1/users/999", map[string]any{}, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown user status = %d, want 404", rec.Code)
	}
}

// assertNoTokenInAudit checks the never-leak rule for token values
// (REQ-API-005): the value must not appear in any audit details payload.
func assertNoTokenInAudit(t *testing.T, e *env, token string) {
	t.Helper()
	rows, err := e.Store.DB.Query(`SELECT COALESCE(details, '') FROM audit_events`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		if len(token) > 0 && strings.Contains(d, token) {
			t.Fatal("token value leaked into audit details")
		}
	}
}

func countType(types []string, want string) int {
	n := 0
	for _, s := range types {
		if s == want {
			n++
		}
	}
	return n
}
