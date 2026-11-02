package admin

import (
	"fmt"
	"net/http"
	"testing"

	"csms/api/internal/db"
)

// REQ-API-186: the user search behind the Control Panel's user list and the project
// members page's account search.

type searchAnswer struct {
	Users  []UserSearchRow `json:"users"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func (e *env) searchUsers(t *testing.T, admin *db.User, query string) searchAnswer {
	t.Helper()
	rec := e.do("GET", "/api/v1/users/search"+query, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", query, rec.Code, rec.Body.String())
	}
	var a searchAnswer
	e.decode(rec, &a)
	return a
}

func emails(rows []UserSearchRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Email)
	}
	return out
}

func (e *env) member(userID, projectID int64) {
	e.exec(`INSERT INTO user_projects (user_id, project_id, token, created_at) VALUES (?, ?, ?, '2026-11-01 00:00:00')`,
		userID, projectID, fmt.Sprintf("tok-%d-%d", userID, projectID))
}

func TestUserSearchFilters(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@admin.test")
	kari := e.mustUser("kari@hospital.example.org")
	ola := e.mustUser("ola@hospital.example.org")
	e.mustUser("per@clinic.example.org")
	e.exec(`UPDATE users SET display_name = 'Kari Nordmann', auth_source = 'oauth2' WHERE id = ?`, kari.ID)
	e.exec(`UPDATE users SET auth_source = 'ldap', enabled = 0 WHERE id = ?`, ola.ID)
	p := e.mustProject("Heart")
	e.member(kari.ID, p)

	if got := emails(e.searchUsers(t, admin, "?q=NORDMANN").Users); len(got) != 1 || got[0] != "kari@hospital.example.org" {
		t.Errorf("q by name = %v", got)
	}
	if got := emails(e.searchUsers(t, admin, "?q=hospital").Users); len(got) != 2 {
		t.Errorf("q by address = %v, want kari and ola", got)
	}
	if got := emails(e.searchUsers(t, admin, "?source=ldap").Users); len(got) != 1 || got[0] != "ola@hospital.example.org" {
		t.Errorf("source=ldap = %v", got)
	}
	none := e.searchUsers(t, admin, "?projects=none&q=example.org")
	for _, r := range none.Users {
		if r.Email == "kari@hospital.example.org" || r.ProjectCount != 0 {
			t.Errorf("projects=none returned %s with %d projects", r.Email, r.ProjectCount)
		}
	}
	// The member search: enabled accounts that are not members of the project yet.
	got := emails(e.searchUsers(t, admin, "?q=example.org&enabled=1&not_in_project="+fmt.Sprint(p)).Users)
	for _, email := range got {
		if email == "kari@hospital.example.org" || email == "ola@hospital.example.org" {
			t.Errorf("member search returned %s (a member, or disabled): %v", email, got)
		}
	}
	all := e.searchUsers(t, admin, "?q=kari")
	if len(all.Users) != 1 || all.Users[0].ProjectCount != 1 || all.Users[0].CreatedAt == "" || all.Users[0].AuthSource != "oauth2" {
		t.Errorf("row = %+v", all.Users)
	}
}

// LIKE wildcards in the query are literal characters, not patterns.
func TestUserSearchQuotesWildcards(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@admin.test")
	e.mustUser("a_b@example.org")
	e.mustUser("axb@example.org")
	if got := emails(e.searchUsers(t, admin, "?q=a_b").Users); len(got) != 1 || got[0] != "a_b@example.org" {
		t.Errorf("q=a_b = %v, want only the literal match", got)
	}
	if got := emails(e.searchUsers(t, admin, "?q=%25").Users); len(got) != 0 {
		t.Errorf("q=%% = %v, want none", got)
	}
}

func TestUserSearchPagesAndSorts(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@admin.test")
	for i := 1; i <= 5; i++ {
		u := e.mustUser(fmt.Sprintf("user%d@example.org", i))
		e.exec(`UPDATE users SET created_at = ? WHERE id = ?`, fmt.Sprintf("2026-10-0%d 08:00:00", i), u.ID)
	}

	first := e.searchUsers(t, admin, "?q=user&limit=2")
	second := e.searchUsers(t, admin, "?q=user&limit=2&offset=2")
	if first.Total != 5 || first.Limit != 2 || second.Offset != 2 {
		t.Errorf("first = %+v, second offset %d", first, second.Offset)
	}
	if got := emails(first.Users); len(got) != 2 || got[0] != "user1@example.org" || got[1] != "user2@example.org" {
		t.Errorf("page 1 = %v", got)
	}
	if got := emails(second.Users); len(got) != 2 || got[0] != "user3@example.org" {
		t.Errorf("page 2 = %v", got)
	}
	if got := emails(e.searchUsers(t, admin, "?q=user&sort=created&limit=1").Users); len(got) != 1 || got[0] != "user5@example.org" {
		t.Errorf("newest first = %v", got)
	}
}

func TestUserSearchRefusals(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")
	if rec := e.do("GET", "/api/v1/users/search", nil, member); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", rec.Code)
	}
	admin := e.mustAdmin("admin@example.org")
	for _, q := range []string{"?source=saml", "?projects=some", "?sort=name", "?enabled=0", "?not_in_project=x", "?offset=-1"} {
		if rec := e.do("GET", "/api/v1/users/search"+q, nil, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}
}
