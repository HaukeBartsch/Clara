package admin

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// mustMember adds a role-less member (full permissions, REQ-AUTH-022).
func mustMember(t *testing.T, e *env, userID, projectID int64) {
	t.Helper()
	_, err := e.Store.AddAssignment(context.Background(),
		&db.Assignment{UserID: userID, ProjectID: projectID})
	if err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
}

// nullInt64 wraps a role id for Assignment.RoleID.
func nullInt64(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }

// contains is strings.Contains under a name that reads well in assertions.
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestProjectsCreateSingleArmAndAudit: creation is 201 with the project
// object, creates arm 1 only (REQ-DB-011) together with its first event
// "baseline" (REQ-API-050), and audits project_created.
func TestProjectsCreateSingleArmAndAudit(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")

	rec := e.do("POST", "/api/v1/projects", map[string]any{
		"project_name": "8DISC", "organization": "NAT EU",
		"pi_name": "Ansgar Espeland", "pi_email": "ansgar@example.org",
		"rek_start_date":    "2026-01-01",
		"participant_names": "8DISC[0-9][0-9][0-9]",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var p projectObject
	e.decode(rec, &p)
	if p.ProjectName != "8DISC" || p.Organization != "NAT EU" ||
		p.PIEmail != "ansgar@example.org" || p.CreationTime == "" {
		t.Errorf("project = %+v", p)
	}
	if p.DMName != nil {
		t.Errorf("dm_name = %q, want null when unset", *p.DMName)
	}

	arm, err := e.Store.GetArmByNum(context.Background(), p.ID, 1)
	if err != nil || arm == nil {
		t.Fatalf("arm 1 missing after creation (REQ-DB-011): %v", err)
	}
	// The first event "baseline" (timepoint day 0) is created with the arm —
	// every project holds at least one event from creation on.
	events, err := e.Store.ListEventsByArm(context.Background(), p.ID, arm.ID)
	if err != nil {
		t.Fatalf("ListEventsByArm: %v", err)
	}
	if len(events) != 1 || events[0].EventName != "baseline" ||
		events[0].UniqueEventName != "baseline_arm_1" ||
		!events[0].Period.Valid || events[0].Period.Int64 != 0 {
		t.Errorf("events after creation = %+v, want the single baseline event (period 0)", events)
	}
	if !hasType(e.auditTypes(), "project_created") {
		t.Errorf("audit types = %v, want project_created", e.auditTypes())
	}
}

// TestProjectsCreateValidation: duplicate name is 409; the GD-17 removals
// are rejected as unknown attributes (REQ-API-052); malformed dates and a
// missing name are 400; non-admins get 403.
func TestProjectsCreateValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	plain := e.mustUser("plain@example.org")

	body := map[string]any{"project_name": "8DISC", "participant_names": "REC[0-9]+"}
	if rec := e.do("POST", "/api/v1/projects", body, admin); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/projects", body, admin); rec.Code != http.StatusConflict {
		t.Errorf("duplicate status = %d, want 409", rec.Code)
	}

	for _, removed := range []string{"end_provision", "event_names",
		"agreed_to_end_user_contract", "option_auto_assign"} {
		b := map[string]any{"project_name": "P" + removed, removed: true}
		if rec := e.do("POST", "/api/v1/projects", b, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400 (REQ-API-052)", removed, rec.Code)
		}
	}

	bad := map[string]any{"project_name": "Dates", "start_date": "01/2026/01"}
	if rec := e.do("POST", "/api/v1/projects", bad, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad date status = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/projects", map[string]any{}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name status = %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/projects", body, plain); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", rec.Code)
	}
}

// TestProjectsListVisibility: is_admin sees every project with dashboard
// counts; a member sees only their own; everyone else sees nothing
// (REQ-AUTH-026, REQ-API-049/007).
func TestProjectsListVisibility(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustUser("member@example.org")
	outsider := e.mustUser("outsider@example.org")

	ctx := context.Background()
	p1 := e.mustProject("Alpha")
	p2 := e.mustProject("Beta")
	mustMember(t, e, member.ID, p1)
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{ProjectID: p1, RecordID: "REC001"}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	iid, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: p1, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	if _, err := e.Store.AddField(ctx, &db.Field{
		ProjectID: p1, InstrumentID: iid, FieldName: "age", FieldType: "text",
	}); err != nil {
		t.Fatalf("AddField: %v", err)
	}

	rec := e.do("GET", "/api/v1/projects", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list status = %d", rec.Code)
	}
	var all []projectSummary
	e.decode(rec, &all)
	if len(all) != 2 || all[0].ProjectName != "Alpha" {
		t.Fatalf("admin listing = %+v, want both projects by name", all)
	}
	if all[0].RecordCount != 1 || all[0].InstrumentCount != 1 || all[0].FieldCount != 1 {
		t.Errorf("Alpha counts = %+v, want 1/1/1", all[0])
	}
	if all[1].ID != p2 || all[1].RecordCount != 0 {
		t.Errorf("Beta summary = %+v", all[1])
	}

	rec = e.do("GET", "/api/v1/projects", nil, member)
	var mine []projectSummary
	e.decode(rec, &mine)
	if len(mine) != 1 || mine[0].ID != p1 {
		t.Errorf("member listing = %+v, want only Alpha", mine)
	}

	rec = e.do("GET", "/api/v1/projects", nil, outsider)
	var none []projectSummary
	e.decode(rec, &none)
	if len(none) != 0 {
		t.Errorf("outsider listing = %+v, want empty (REQ-API-007)", none)
	}
}

// TestProjectGetDetail: a role-less member gets full metadata plus arms with
// canonically ordered events and instruments with field counts (§4.5).
func TestProjectGetDetail(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")
	ctx := context.Background()
	pid := e.mustProject("8DISC")
	mustMember(t, e, member.ID, pid)
	// mustProject bypasses the API's single-arm creation — add arm 1 here.
	armID, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	if _, err := e.Store.AddEvent(ctx, &db.Event{
		ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1",
	}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	iid, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	if _, err := e.Store.AddField(ctx, &db.Field{
		ProjectID: pid, InstrumentID: iid, FieldName: "age", FieldType: "text",
	}); err != nil {
		t.Fatalf("AddField: %v", err)
	}

	rec := e.do("GET", "/api/v1/projects/"+itoa(pid), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var d projectDetail
	e.decode(rec, &d)
	if d.ProjectName != "8DISC" {
		t.Errorf("project_name = %q", d.ProjectName)
	}
	if len(d.Arms) != 1 || d.Arms[0].ArmNum != 1 || len(d.Arms[0].Events) != 1 {
		t.Fatalf("arms = %+v", d.Arms)
	}
	if ev := d.Arms[0].Events[0]; ev.UniqueEventName != "baseline_arm_1" || ev.ArmNum != 1 {
		t.Errorf("event = %+v", ev)
	}
	if len(d.Instruments) != 1 || d.Instruments[0].Name != "intake" ||
		d.Instruments[0].FieldCount != 1 {
		t.Errorf("instruments = %+v", d.Instruments)
	}
}

// TestProjectGetAccessRules: a non-member gets the uniform 403 that never
// discloses existence; an unknown id is 404; a member whose role grants no
// data access fails the read_only floor (§4.5).
func TestProjectGetAccessRules(t *testing.T) {
	e := newEnv(t)
	outsider := e.mustUser("outsider@example.org")
	locked := e.mustUser("locked@example.org")
	pid := e.mustProject("8DISC")

	if rec := e.do("GET", "/api/v1/projects/"+itoa(pid), nil, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-member status = %d, want 403 (REQ-API-007)", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/999", nil, outsider); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id status = %d, want 404", rec.Code)
	}

	ctx := context.Background()
	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: pid, RoleName: "observer"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "no_access", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := e.Store.AddAssignment(ctx, &db.Assignment{
		UserID: locked.ID, ProjectID: pid, RoleID: nullInt64(roleID),
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	if rec := e.do("GET", "/api/v1/projects/"+itoa(pid), nil, locked); rec.Code != http.StatusForbidden {
		t.Errorf("no-data member status = %d, want 403", rec.Code)
	}
}

// TestProjectUpdate: a role-less member (project_admin per REQ-AUTH-022)
// applies a partial update; the audit entry carries old and new values
// (§3.3); an idempotent repeat writes no second entry (REQ-API-042).
func TestProjectUpdate(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")
	pid := e.mustProject("8DISC")
	mustMember(t, e, member.ID, pid)

	rec := e.do("PUT", "/api/v1/projects/"+itoa(pid),
		map[string]any{"pi_name": "New PI", "dm_email": nil}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var d projectDetail
	e.decode(rec, &d)
	if d.PIName != "New PI" || d.ProjectName != "8DISC" {
		t.Errorf("updated = %+v", d.projectObject)
	}

	types := e.auditTypes()
	if !hasType(types, "project_updated") {
		t.Fatalf("audit types = %v, want project_updated", types)
	}
	var details string
	if err := e.Store.DB.QueryRow(
		`SELECT details FROM audit_events WHERE event_type = 'project_updated'`).
		Scan(&details); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	for _, want := range []string{`"pi_name"`, `"old":""`, `"new":"New PI"`} {
		if !contains(details, want) {
			t.Errorf("audit details = %s, want %s", details, want)
		}
	}

	before := len(e.auditTypes())
	rec = e.do("PUT", "/api/v1/projects/"+itoa(pid), map[string]any{"pi_name": "New PI"}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent PUT status = %d", rec.Code)
	}
	if len(e.auditTypes()) != before {
		t.Error("no-op PUT wrote audit entries")
	}
}

// TestProjectUpdateGuards: renaming onto an existing name is 409; a member
// without project_admin and a non-member are both 403.
func TestProjectUpdateGuards(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	plain := e.mustUser("plain@example.org")
	ctx := context.Background()
	p1 := e.mustProject("Alpha")
	e.mustProject("Beta")
	if _, err := e.Store.AddAssignment(ctx, &db.Assignment{UserID: plain.ID, ProjectID: p1}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}

	// role-less member is project_admin — the duplicate rename must 409.
	if rec := e.do("PUT", "/api/v1/projects/"+itoa(p1),
		map[string]any{"project_name": "Beta"}, plain); rec.Code != http.StatusConflict {
		t.Errorf("duplicate rename status = %d, want 409", rec.Code)
	}

	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: p1, RoleName: "worker"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	worker := e.mustUser("worker@example.org")
	if _, err := e.Store.AddAssignment(ctx, &db.Assignment{
		UserID: worker.ID, ProjectID: p1, RoleID: nullInt64(roleID),
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	if rec := e.do("PUT", "/api/v1/projects/"+itoa(p1),
		map[string]any{"pi_name": "X"}, worker); rec.Code != http.StatusForbidden {
		t.Errorf("non-project-admin status = %d, want 403", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/projects/"+itoa(p1), map[string]any{"pi_name": "X"},
		admin); rec.Code != http.StatusOK {
		t.Errorf("is_admin status = %d, want 200 (REQ-AUTH-023)", rec.Code)
	}
}

// TestProjectDetailCarriesEffectivePermissions: the project detail read returns
// the acting user's effective levels so the web layer can gate its rendering
// (REQ-API-126) — full levels for is_admin and for a role-less member
// (REQ-AUTH-023/022), otherwise the role's grants with no implicit access on an
// ungranted arm (REQ-AUTH-019) — one entry per project arm in every case.
func TestProjectDetailCarriesEffectivePermissions(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("8DISC")
	e.mustArm(projectID, 1)
	e.mustArm(projectID, 2)

	// check fetches the detail as actor and asserts its permissions block.
	check := func(actor *db.User, projectAdmin bool, want map[int][2]string) {
		t.Helper()
		rec := e.do("GET", "/api/v1/projects/"+itoa(projectID), nil, actor)
		if rec.Code != http.StatusOK {
			t.Fatalf("get as %s: %d %s", actor.Email, rec.Code, rec.Body.String())
		}
		var d projectDetail
		e.decode(rec, &d)
		p := d.Permissions
		if p.ProjectAdmin != projectAdmin {
			t.Errorf("%s: project_admin = %v, want %v", actor.Email, p.ProjectAdmin, projectAdmin)
		}
		if len(p.Arms) != len(want) {
			t.Fatalf("%s: arms = %+v, want one entry per project arm", actor.Email, p.Arms)
		}
		for _, a := range p.Arms {
			w, ok := want[a.ArmNum]
			if !ok || a.DataAccessLevel != w[0] || a.ExportLevel != w[1] {
				t.Errorf("%s: arm %d = %+v, want %v", actor.Email, a.ArmNum, a, w)
			}
		}
	}
	full := func() map[int][2]string {
		return map[int][2]string{
			1: {"edit_survey_responses", "export_full"},
			2: {"edit_survey_responses", "export_full"},
		}
	}

	// is_admin covers every arm (REQ-AUTH-023).
	check(admin, true, full())

	// A member on a role sees that role's grants; the arm the role does not
	// grant reports the no-access default instead of being absent.
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles", map[string]any{
		"name": "data-entry",
		"arms": map[string]any{"1": map[string]string{"data": "view_edit", "export": "export_de_identified"}},
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role: %d %s", rec.Code, rec.Body.String())
	}
	entry := e.mustUser("entry@example.org")
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/users/"+itoa(entry.ID),
		map[string]any{"role": "data-entry"}, admin)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("add member: %d %s", rec.Code, rec.Body.String())
	}
	check(entry, false, map[int][2]string{
		1: {"view_edit", "export_de_identified"},
		2: {"no_access", "export_none"},
	})

	// A role-less member holds full permissions including project_admin
	// (REQ-AUTH-022).
	open := e.mustUser("open@example.org")
	mustMember(t, e, open.ID, projectID)
	check(open, true, full())

	// PUT answers with the same shape, permissions included (REQ-API-126).
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID), map[string]any{"pi_name": "New PI"}, open)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	var updated projectDetail
	e.decode(rec, &updated)
	if !updated.Permissions.ProjectAdmin || len(updated.Permissions.Arms) != 2 {
		t.Errorf("PUT permissions = %+v, want the GET shape (REQ-API-126)", updated.Permissions)
	}

	// The block is disclosed to nobody the read itself is denied for: a member
	// whose role grants nothing keeps the uniform 403 of REQ-API-051/007 and
	// learns nothing from it.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/roles",
		map[string]any{"name": "viewer-none"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create empty role: %d %s", rec.Code, rec.Body.String())
	}
	none := e.mustUser("none@example.org")
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/users/"+itoa(none.ID),
		map[string]any{"role": "viewer-none"}, admin)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("add member with empty role: %d %s", rec.Code, rec.Body.String())
	}
	for _, actor := range []*db.User{none, e.mustUser("outsider@example.org")} {
		rec = e.do("GET", "/api/v1/projects/"+itoa(projectID), nil, actor)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", actor.Email, rec.Code)
		}
		if contains(rec.Body.String(), "permissions") {
			t.Errorf("%s: rejected read leaked permissions: %s", actor.Email, rec.Body.String())
		}
	}
}
