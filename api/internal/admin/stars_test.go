package admin

// Starred projects (REQ-API-188, REQ-API-049, REQ-DB-056): a user stars and unstars the
// projects they can see, the project list carries the actor's own stars, nothing is
// audited, and a deleted project takes its stars with it.

import (
	"context"
	"net/http"
	"testing"

	"csms/api/internal/db"
)

// starredNames lists the actor's projects and returns the names flagged starred, in list
// order, together with the whole list's names.
func (e *env) starredNames(actor *db.User) (names, starred []string) {
	e.t.Helper()
	rec := e.do("GET", "/api/v1/projects", nil, actor)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET /api/v1/projects status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out []projectSummary
	e.decode(rec, &out)
	for _, s := range out {
		names = append(names, s.ProjectName)
		if s.Starred {
			starred = append(starred, s.ProjectName)
		}
	}
	return names, starred
}

func TestProjectStarMarksTheActorsList(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	alpha := e.mustProject("Alpha")
	beta := e.mustProject("Beta")
	for _, id := range []int64{alpha, beta} {
		mustMember(t, e, u.ID, id)
		mustMember(t, e, other.ID, id)
	}
	before := len(e.auditTypes())
	path := "/api/v1/users/me/starred-projects/" + itoa(beta)

	star := func(method string, want bool) {
		t.Helper()
		rec := e.do(method, path, nil, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", method, rec.Code, rec.Body.String())
		}
		var got projectStar
		e.decode(rec, &got)
		if got.ProjectID != beta || got.Starred != want {
			t.Errorf("%s answer = %+v, want project %d starred %v", method, got, beta, want)
		}
	}

	names, starred := e.starredNames(u)
	if len(starred) != 0 {
		t.Fatalf("starred = %v before any star, want none", starred)
	}
	order := names

	star("PUT", true)
	star("PUT", true) // idempotent
	names, starred = e.starredNames(u)
	if len(starred) != 1 || starred[0] != "Beta" {
		t.Errorf("starred = %v, want [Beta]", starred)
	}
	if len(names) != len(order) || names[0] != order[0] {
		t.Errorf("list order %v changed from %v: a star is presentation only", names, order)
	}
	if _, theirs := e.starredNames(other); len(theirs) != 0 {
		t.Errorf("another member sees stars %v, want none: a star is the actor's own", theirs)
	}

	star("DELETE", false)
	star("DELETE", false) // idempotent
	if _, starred := e.starredNames(u); len(starred) != 0 {
		t.Errorf("starred = %v after unstarring, want none", starred)
	}

	// Starring is neither a data change nor an administration event, and an entry would
	// make the project read "today" on the overview (REQ-UI-071).
	if got := e.auditTypes(); len(got) != before {
		t.Errorf("audit entries %v written by starring, want none", got[before:])
	}
}

func TestProjectStarVisibility(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := e.mustUser("member@example.org")
	mine := e.mustProject("Mine")
	theirs := e.mustProject("Theirs")
	mustMember(t, e, u.ID, mine)

	cases := []struct {
		label  string
		method string
		path   string
		actor  *db.User
		want   int
	}{
		{"member, own project", "PUT", "/api/v1/users/me/starred-projects/" + itoa(mine), u, http.StatusOK},
		{"member, another project", "PUT", "/api/v1/users/me/starred-projects/" + itoa(theirs), u, http.StatusForbidden},
		{"member, unstar another project", "DELETE", "/api/v1/users/me/starred-projects/" + itoa(theirs), u, http.StatusForbidden},
		{"member, no such project", "PUT", "/api/v1/users/me/starred-projects/999999", u, http.StatusForbidden},
		{"admin, any project", "PUT", "/api/v1/users/me/starred-projects/" + itoa(theirs), admin, http.StatusOK},
		{"admin, no such project", "PUT", "/api/v1/users/me/starred-projects/999999", admin, http.StatusNotFound},
		{"malformed id", "PUT", "/api/v1/users/me/starred-projects/abc", u, http.StatusBadRequest},
		{"zero id", "PUT", "/api/v1/users/me/starred-projects/0", u, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec := e.do(c.method, c.path, nil, c.actor); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (body %s)", c.label, rec.Code, c.want, rec.Body.String())
		}
	}

	stars, err := e.Store.StarredProjectIDs(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("StarredProjectIDs: %v", err)
	}
	if len(stars) != 1 || !stars[mine] {
		t.Errorf("member's stars = %v, want only %d", stars, mine)
	}
}

// A star never outlives its project (REQ-DB-056, REQ-EXP-044): the foreign key cascades.
func TestProjectStarGoesWithTheProject(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("member@example.org")
	keep := e.mustProject("Keep")
	gone := e.mustProject("Gone")
	ctx := context.Background()
	for _, id := range []int64{keep, gone} {
		mustMember(t, e, u.ID, id)
		if rec := e.do("PUT", "/api/v1/users/me/starred-projects/"+itoa(id), nil, u); rec.Code != http.StatusOK {
			t.Fatalf("star %d: status = %d", id, rec.Code)
		}
	}
	if err := e.Store.DeleteProject(ctx, gone); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	stars, err := e.Store.StarredProjectIDs(ctx, u.ID)
	if err != nil {
		t.Fatalf("StarredProjectIDs: %v", err)
	}
	if len(stars) != 1 || !stars[keep] {
		t.Errorf("stars after deleting %d = %v, want only %d", gone, stars, keep)
	}
}
