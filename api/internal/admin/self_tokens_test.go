package admin

import (
	"net/http"
	"strings"
	"testing"
)

// TestMyMembershipsListsOwnProjectsOnly: the account page's list is the acting
// user's memberships — with the role name, never a token value — and not the
// projects an administrator merely sees (REQ-API-161).
func TestMyMembershipsListsOwnProjectsOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	first := e.mustProject("8DISC")
	e.mustProject("EMIT-23")
	third := e.mustProject("NAT EU")
	mustRole(t, e, third, "data-entry")

	if rec := e.do("PUT", "/api/v1/projects/"+itoa(first)+"/users/"+itoa(u.ID), map[string]any{}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var added memberWithToken
	rec := e.do("PUT", "/api/v1/projects/"+itoa(third)+"/users/"+itoa(u.ID), map[string]any{"role": "data-entry"}, admin)
	e.decode(rec, &added)

	rec = e.do("GET", "/api/v1/users/me/projects", nil, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []membershipObject
	e.decode(rec, &list)
	if len(list) != 2 || list[0].ProjectID != first || list[0].ProjectName != "8DISC" || list[0].Role != nil ||
		list[1].ProjectID != third || list[1].Role == nil || *list[1].Role != "data-entry" {
		t.Errorf("memberships = %+v, want 8DISC (no role) and NAT EU (data-entry)", list)
	}
	assertNoTokenInBody(t, rec.Body.String(), added.Token)

	// An administrator who is a member of nothing has no memberships, though they see
	// every project.
	rec = e.do("GET", "/api/v1/users/me/projects", nil, admin)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("admin memberships = %d %q, want 200 []", rec.Code, rec.Body.String())
	}
}

// TestRevealOwnToken: a member's reveal answers their own token and writes one
// token_viewed entry naming themselves per call (REQ-API-162, REQ-AUD-033); a
// project they are not a member of is the uniform 403 and writes nothing.
func TestRevealOwnToken(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	pid := e.mustProject("8DISC")

	rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/users/"+itoa(u.ID), map[string]any{}, admin)
	var added memberWithToken
	e.decode(rec, &added)
	path := "/api/v1/users/me/projects/" + itoa(pid) + "/token-reveal"

	for i := 0; i < 2; i++ {
		rec := e.do("POST", path, nil, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("reveal %d status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
		var tok struct {
			Token string `json:"token"`
		}
		e.decode(rec, &tok)
		if tok.Token != added.Token {
			t.Errorf("reveal %d token = %q, want the member's own", i, tok.Token)
		}
	}
	details := e.auditDetails("token_viewed", pid)
	if len(details) != 2 || details[0]["member_email"] != "member@example.org" || len(details[0]) != 1 {
		t.Errorf("token_viewed details = %v, want two {member_email: member@example.org}", details)
	}
	assertNoTokenInAudit(t, e, added.Token)

	for _, c := range []struct {
		name string
		path string
		body map[string]any
		want int
	}{
		{"not a member", path, nil, http.StatusForbidden},
		{"unknown project", "/api/v1/users/me/projects/999/token-reveal", nil, http.StatusForbidden},
		{"invalid id", "/api/v1/users/me/projects/x/token-reveal", nil, http.StatusForbidden},
	} {
		if rec := e.do("POST", c.path, c.body, other); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
	}
	if rec := e.do("POST", path, map[string]any{"user_id": other.ID}, u); rec.Code != http.StatusBadRequest {
		t.Errorf("body attribute: status = %d, want 400", rec.Code)
	}
	if n := countType(e.auditTypes(), "token_viewed"); n != 2 {
		t.Errorf("token_viewed count = %d, want 2 (refused calls write nothing)", n)
	}
}

// assertNoTokenInBody fails when a response that must not disclose the value does.
func assertNoTokenInBody(t *testing.T, body, token string) {
	t.Helper()
	if token != "" && strings.Contains(body, token) {
		t.Errorf("response body discloses the token value")
	}
}
