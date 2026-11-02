package admin

// Project deletion (§4.20, REQ-API-187, REQ-EXP-044,
// Data_Export_Anonymization_Design §7.5): everything that belongs to the
// project goes, in any mode and with a staging set open; the audit trail stays
// and gains project_deleted; other projects and the user accounts are
// untouched; is_admin only; one transaction; and the id is not handed out again.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// seedEverything gives the fixture's project a row in every project-owned table
// the export fixture leaves empty: a project_admin role with an arm default and a
// pair grant, a
// member holding it with a token and a group membership, a completion mark, a
// survey link, an anonymization offset — and puts the project in production
// with a staging set open, the state deletion must not be blocked by.
func seedEverything(t *testing.T, f *exportFixture, opener *db.User) (member *db.User) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	roleID, err := f.e.Store.CreateRole(ctx, &db.Role{ProjectID: f.pid, RoleName: "owners", ProjectAdmin: true},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_full"}},
		[]db.RoleGrant{{EventID: f.evBaseline, InstrumentID: f.demoID, DataAccessLevel: "read_only", ExportLevel: "export_de_identified"}})
	must(err)
	member = f.member(t, "owner@example.org", roleID)
	a, err := f.e.Store.GetAssignment(ctx, member.ID, f.pid)
	must(err)
	_, err = f.e.Store.AddDAGMembership(ctx, &db.DagMembership{AssignmentID: a.ID, GroupID: f.groupA, IsActive: true})
	must(err)

	tx, err := f.e.Store.DB.BeginTx(ctx, nil)
	must(err)
	_, err = f.e.Store.SetInstrumentCompletionTx(ctx, tx, f.pid, "REC-001", f.evBaseline, f.demoID, 1, "finished", opener.ID)
	must(err)
	must(f.e.Store.SetProjectModeTx(ctx, tx, f.pid, "production"))
	must(tx.Commit())

	_, err = f.e.Store.CreateSurveyLink(ctx, &db.SurveyLink{ProjectID: f.pid, RecordID: "REC-001",
		InstrumentID: f.demoID, EventID: f.evBaseline, Token: "survey-token-1"})
	must(err)
	must(f.e.Store.CreateAnonOffset(ctx, &db.AnonOffset{ProjectID: f.pid, RecordID: "REC-001", OffsetDays: 9}))
	must(f.e.Store.OpenStaging(ctx, f.pid, `{}`, opener.ID))
	return member
}

// projectTables lists every base table with a project_id column, the audit
// tables aside — read from the schema catalog, so a table added later is
// covered too.
func projectTables(t *testing.T, store *db.Store) []string {
	t.Helper()
	q := `SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) c
		WHERE m.type = 'table' AND m.name NOT LIKE 'audit%' AND c.name = 'project_id'`
	if store.Dialect == db.DialectMariaDB {
		q = `SELECT c.table_name FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
			WHERE c.table_schema = DATABASE() AND c.column_name = 'project_id'
			  AND t.table_type = 'BASE TABLE' AND c.table_name NOT LIKE 'audit%'`
	}
	rows, err := store.DB.Query(q)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, name)
	}
	if len(out) < 10 {
		t.Fatalf("found %d project tables (%v) — the catalog query is off", len(out), out)
	}
	return out
}

func countWhere(t *testing.T, store *db.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestDeleteProject(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")
	member := seedEverything(t, f, admin)
	ctx := context.Background()

	// Another project, which must come through untouched.
	other := f.e.mustProject("other")
	if _, err := f.e.Store.AddArm(ctx, &db.Arm{ProjectID: other, ArmNum: 1}); err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	if err := f.e.Store.UpsertDataValue(ctx, &db.DataValue{ProjectID: other, RecordID: "X-1",
		UniqueEventName: "baseline_arm_1", FieldName: "record_id", Value: "X-1"}); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	// An entry the project already has in its trail.
	rec := f.e.do(http.MethodPut, "/api/v1/projects/"+itoa(f.pid), map[string]any{"pi_name": "Dr. Before"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata update = %d %s", rec.Code, rec.Body.String())
	}
	tables := projectTables(t, f.e.Store)
	for _, table := range []string{"data", "record_entities", "instrument_completion", "survey_links",
		"anon_offsets", "user_projects", "roles", "arms", "events", "instruments", "fields", "dag_groups", "project_staging"} {
		if countWhere(t, f.e.Store, `SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, f.pid) == 0 {
			t.Fatalf("fixture leaves %s empty — the test would prove nothing for it", table)
		}
	}

	rec = f.e.do(http.MethodDelete, "/api/v1/projects/"+itoa(f.pid), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var out map[string]any
	f.e.decode(rec, &out)
	if out["project_id"] != float64(f.pid) || out["project_name"] != "export" ||
		out["records_deleted"] != float64(2) || out["values_deleted"] != float64(14) {
		t.Fatalf("body = %s, want the project's id and name with 2 records and 14 values", rec.Body.String())
	}

	// Nothing of the project is left in any table that names it …
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM projects WHERE id = ?`, f.pid); n != 0 {
		t.Errorf("projects row survived")
	}
	for _, table := range tables {
		if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, f.pid); n != 0 {
			t.Errorf("%s keeps %d rows of the deleted project", table, n)
		}
	}
	// … nor in the tables that reach it through a parent row.
	for _, table := range []string{"role_arms", "role_grants", "instrument_events", "dag_memberships"} {
		if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s keeps %d rows — only the deleted project had any", table, n)
		}
	}
	if f.e.Store.Dialect == db.DialectSQLite { // InnoDB refuses a dangling row outright
		if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM pragma_foreign_key_check`); n != 0 {
			t.Errorf("foreign_key_check reports %d dangling rows", n)
		}
	}

	// The token is gone with the membership; the account stays.
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM user_projects WHERE token = ?`, "tok-owner@example.org"); n != 0 {
		t.Errorf("the member's project token survived")
	}
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM users WHERE id = ?`, member.ID); n != 1 {
		t.Errorf("the member's account was removed with the project")
	}

	// The other project is untouched.
	if p, err := f.e.Store.GetProject(ctx, other); err != nil || p == nil {
		t.Errorf("other project = %v (%v), want it kept", p, err)
	}
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM data WHERE project_id = ?`, other); n != 1 {
		t.Errorf("other project's data rows = %d, want 1", n)
	}

	// The trail keeps the project's earlier entry and gains the deletion (REQ-AUD-038).
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND event_type = ?`,
		f.pid, audit.ProjectUpdated); n != 1 {
		t.Errorf("project_updated entries of the deleted project = %d, want the 1 written before", n)
	}
	var pid int64
	if err := f.e.Store.DB.QueryRow(`SELECT project_id FROM audit_events WHERE event_type = ?`,
		audit.ProjectDeleted).Scan(&pid); err != nil || pid != f.pid {
		t.Errorf("project_deleted project_id = %d (%v), want %d", pid, err, f.pid)
	}
	_, d := f.lastAudit(t, audit.ProjectDeleted)
	if d["project_name"] != "export" || d["mode"] != "production" ||
		d["records_deleted"] != float64(2) || d["values_deleted"] != float64(14) {
		t.Errorf("audit details = %v, want name, mode and both counts", d)
	}
	if strings.Contains(rec.Body.String(), "very private") {
		t.Errorf("the answer carries a stored value")
	}

	// The project is gone for every later call.
	if rec := f.e.do(http.MethodGet, "/api/v1/projects/"+itoa(f.pid), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", rec.Code)
	}
	if rec := f.e.do(http.MethodDelete, "/api/v1/projects/"+itoa(f.pid), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

func TestDeleteProjectGating(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")
	owners, err := f.e.Store.CreateRole(context.Background(),
		&db.Role{ProjectID: f.pid, RoleName: "owners", ProjectAdmin: true}, nil, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	projectAdmin := f.member(t, "owner@example.org", owners)
	stranger := f.e.mustUser("stranger@example.org")
	path := "/api/v1/projects/" + itoa(f.pid)

	for name, u := range map[string]*db.User{"project_admin": projectAdmin, "non-member": stranger} {
		rec := f.e.do(http.MethodDelete, path, nil, u)
		if rec.Code != http.StatusForbidden || decodeError(t, rec) != "forbidden" {
			t.Errorf("%s = %d %s, want 403 forbidden", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.e.do(http.MethodDelete, "/api/v1/projects/9999", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	if rec := f.e.do(http.MethodDelete, "/api/v1/projects/abc", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id = %d, want 404", rec.Code)
	}

	// Nothing happened: the project, its data, and no project_deleted entry.
	if p, err := f.e.Store.GetProject(context.Background(), f.pid); err != nil || p == nil {
		t.Errorf("project = %v (%v), want it intact", p, err)
	}
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM data WHERE project_id = ?`, f.pid); n == 0 {
		t.Errorf("data removed by a refused call")
	}
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, audit.ProjectDeleted); n != 0 {
		t.Errorf("project_deleted rows = %d, want 0", n)
	}
}

// TestDeleteProjectStaysInsideItsTransaction runs the deletion with the pool
// capped at one connection: every read it makes must be on its own transaction
// (Database_Schema_Design.md §10), as for the end provision.
func TestDeleteProjectStaysInsideItsTransaction(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")
	singleConnPool(t, f.e.Store)

	rec := mustComplete(t, "project delete", func() *httptest.ResponseRecorder {
		return f.e.do(http.MethodDelete, "/api/v1/projects/"+itoa(f.pid), nil, admin)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete with a one-connection pool = %d %s — the deletion reads outside its transaction",
			rec.Code, rec.Body.String())
	}
}

// TestDeleteProjectIDNotReused deletes the newest project — the one whose id
// SQLite's rowid alias would hand out next — and checks what reuse would break:
// the next project gets a new id, an empty trail, and its own end provision,
// although the deleted project had already executed one (REQ-DB-055).
func TestDeleteProjectIDNotReused(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")
	path := "/api/v1/projects/" + itoa(f.pid)

	if rec := f.e.do(http.MethodPost, path+"/end-provision", map[string]string{"provision": "delete"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("end provision = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.e.do(http.MethodDelete, path, nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}

	next := f.e.mustProject("next")
	if next <= f.pid {
		t.Fatalf("next project id = %d, want above the deleted %d", next, f.pid)
	}
	if n := countWhere(t, f.e.Store, `SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, next); n != 0 {
		t.Errorf("the new project starts with %d audit entries, want none", n)
	}
	rec := f.e.do(http.MethodPost, "/api/v1/projects/"+itoa(next)+"/end-provision",
		map[string]string{"provision": "delete"}, admin)
	if rec.Code != http.StatusOK {
		t.Errorf("end provision of the new project = %d %s, want 200 — not the deleted project's 409",
			rec.Code, rec.Body.String())
	}
}
