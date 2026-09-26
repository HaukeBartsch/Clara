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
	}, nil)
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
