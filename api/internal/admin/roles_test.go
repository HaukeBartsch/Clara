package admin

import (
	"net/http"
	"testing"
)

func TestListRoles(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Role Study")
	e.mustArm(projectID, 1)
	e.mustArm(projectID, 2)

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/roles", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty roles: got %d %s", rec.Code, rec.Body.String())
	}
	var out []roleObject
	e.decode(rec, &out)
	if len(out) != 0 {
		t.Fatalf("expected empty array: %s", rec.Body.String())
	}

	// is_admin only (REQ-API-056).
	regular := e.mustUser("user@example.org")
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/roles", nil, regular)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: got %d, want 403", rec.Code)
	}
	rec = e.do("GET", "/api/v1/projects/999999/roles", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: got %d, want 404", rec.Code)
	}
}

func TestCreateRole(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Role Study")
	e.mustArm(projectID, 1)
	e.mustArm(projectID, 2)

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles", map[string]any{
		"name": "data-entry", "project_admin": false,
		"arms": map[string]any{"1": map[string]string{"data": "view_edit", "export": "export_full"}},
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role: got %d %s", rec.Code, rec.Body.String())
	}
	var created roleObject
	e.decode(rec, &created)
	if created.ID == 0 || created.Name != "data-entry" || created.ProjectAdmin {
		t.Fatalf("created role: %+v", created)
	}
	// Every project arm appears; the ungranted one defaults to no access
	// (REQ-AUTH-019).
	if g := created.Arms["1"]; g.Data != "view_edit" || g.Export != "export_full" {
		t.Fatalf("arm 1 grants: %+v", created.Arms)
	}
	if g := created.Arms["2"]; g.Data != "no_access" || g.Export != "export_none" {
		t.Fatalf("arm 2 default: %+v", created.Arms)
	}

	// The listing shows the stored role with the same shape.
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/roles", nil, admin)
	var list []roleObject
	e.decode(rec, &list)
	if len(list) != 1 || list[0].Arms["2"].Data != "no_access" {
		t.Fatalf("listing: %s", rec.Body.String())
	}

	// Audit role_created (§3.4).
	found := false
	for _, typ := range e.auditTypes() {
		if typ == "role_created" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing role_created audit entry: %v", e.auditTypes())
	}

	// Duplicate name within the project → 409 (REQ-API-057).
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles", map[string]any{
		"name": "data-entry",
	}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name: got %d, want 409", rec.Code)
	}

	// Validation rejections → 400 invalid_request.
	bad := []any{
		map[string]any{}, // missing name
		map[string]any{"name": "x", "arms": map[string]any{
			"9": map[string]string{"data": "read_only", "export": "export_none"}}}, // unknown arm
		map[string]any{"name": "x", "arms": map[string]any{
			"1": map[string]string{"data": "superuser", "export": "export_none"}}}, // bad data level
		map[string]any{"name": "x", "arms": map[string]any{
			"1": map[string]string{"data": "read_only", "export": "export_everything"}}}, // bad export
		map[string]any{"name": "x", "description": "no such attribute"}, // unknown attribute
	}
	for i, body := range bad {
		rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles", body, admin)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad body %d: got %d, want 400 (%s)", i, rec.Code, rec.Body.String())
		}
	}

	// Non-admin cannot create.
	regular := e.mustUser("user@example.org")
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles", map[string]any{"name": "x"}, regular)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: got %d, want 403", rec.Code)
	}
}
