package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- §4.18 data access groups (REQ-API-086…091) ---

func dagPath(projectID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/data-access-groups"
}

// memberDAGPath is §4.18's member-group route: the user comes before the
// collection, not under it.
func memberDAGPath(projectID, userID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/users/" + itoa(userID) + "/data-access-groups"
}

// dagAuditRows returns the group-management audit trail with its details, so a
// test can assert both that an entry exists and that a rejected call wrote
// none (REQ-AUD-004).
func dagAuditRows(t *testing.T, e *env) []map[string]any {
	t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT event_type, target_record, details FROM audit_events
		 WHERE event_type IN (?, ?, ?, ?, ?) ORDER BY id`,
		audit.DagCreated, audit.DagDeleted, audit.DagMembershipChanged,
		audit.DagActiveSwitched, audit.DagRecordAssigned)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var (
			eventType, details string
			target             sql.NullString
		)
		if err := rows.Scan(&eventType, &target, &details); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		row := map[string]any{"event_type": eventType}
		if target.Valid {
			row["target_record"] = target.String
		}
		if details != "" {
			var d map[string]any
			if err := json.Unmarshal([]byte(details), &d); err != nil {
				t.Fatalf("audit details %q: %v", details, err)
			}
			for k, v := range d {
				row[k] = v
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

// dagFixture is a one-arm project with two records — one in group A, one
// unassigned — and a read_only member.
type dagFixture struct {
	projectID int64
	groupA    int64
	groupB    int64
	reader    *db.User
	editor    *db.User
}

func newDAGFixture(t *testing.T, e *env) dagFixture {
	t.Helper()
	ctx := context.Background()
	f := dagFixture{}
	f.projectID = e.mustProject("DAG Study")
	armID := e.mustArm(f.projectID, 1)
	v1 := e.mustEvent(f.projectID, armID, "Visit 1", "v1_arm_1")
	instr := e.mustInstrument(f.projectID, "intake")
	e.mustMap(f.projectID, armID, db.InstrumentEvent{InstrumentID: instr, EventID: v1})

	var err error
	f.groupA, err = e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup A: %v", err)
	}
	f.groupB, err = e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center B"})
	if err != nil {
		t.Fatalf("CreateDAGGroup B: %v", err)
	}
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: f.projectID, RecordID: "R1", DagGroupID: nullInt64(f.groupA),
	}); err != nil {
		t.Fatalf("CreateRecordEntity R1: %v", err)
	}
	e.mustRecord(f.projectID, "R2") // unassigned

	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	f.reader = e.mustUser("reader@example.org")
	e.mustMemberWithRole(f.projectID, f.reader, readRole)

	// A role-less member is this project's admin (REQ-AUTH-022) without being an
	// installation administrator — exactly the distinction §4.18 turns on.
	f.editor = e.mustUser("padmin@example.org")
	e.mustMemberWithRole(f.projectID, f.editor, 0)
	return f
}

// TestDAGListAndCreate covers the listing (empty and populated), the read gate,
// creation with its audit entry, and the duplicate-name conflict.
func TestDAGListAndCreate(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Empty Groups Study")

	rec := e.do("GET", dagPath(projectID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d %s", rec.Code, rec.Body.String())
	}
	var groups []dagObject
	e.decode(rec, &groups)
	if len(groups) != 0 {
		t.Fatalf("new project has groups: %+v", groups)
	}

	rec = e.do("POST", dagPath(projectID), map[string]string{"name": "Center A"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s", rec.Code, rec.Body.String())
	}
	var created dagObject
	e.decode(rec, &created)
	if created.ID == 0 || created.Name != "Center A" {
		t.Errorf("created = %+v", created)
	}

	rec = e.do("GET", dagPath(projectID), nil, admin)
	e.decode(rec, &groups)
	if len(groups) != 1 || groups[0].Name != "Center A" {
		t.Fatalf("list after create: %+v", groups)
	}

	// Duplicate name within the project → 409 (REQ-AUTH-043).
	if rec := e.do("POST", dagPath(projectID), map[string]string{"name": "Center A"}, admin); rec.Code != http.StatusConflict {
		t.Errorf("duplicate name: got %d %s, want 409", rec.Code, rec.Body.String())
	}
	// Missing name → 400.
	if rec := e.do("POST", dagPath(projectID), map[string]string{}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name: got %d, want 400", rec.Code)
	}

	rows := dagAuditRows(t, e)
	if len(rows) != 1 || rows[0]["event_type"] != audit.DagCreated || rows[0]["name"] != "Center A" {
		t.Errorf("audit = %+v, want one dag_created naming the group", rows)
	}
}

// TestDAGPermissions pins who may change groups: reads need read_only, writes
// need project_admin, and the member-set endpoint is reserved to is_admin
// (REQ-AUTH-044).
func TestDAGPermissions(t *testing.T) {
	e := newEnv(t)
	f := newDAGFixture(t, e)
	outsider := e.mustUser("outsider@example.org")

	if rec := e.do("GET", dagPath(f.projectID), nil, f.reader); rec.Code != http.StatusOK {
		t.Errorf("read_only listing: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := e.do("GET", dagPath(f.projectID), nil, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-member listing: got %d, want 403", rec.Code)
	}

	// A role-less member is project_admin (REQ-AUTH-022) and may create groups;
	// a read_only member may not.
	if rec := e.do("POST", dagPath(f.projectID), map[string]string{"name": "Center C"}, f.reader); rec.Code != http.StatusForbidden {
		t.Errorf("read_only create: got %d, want 403", rec.Code)
	}
	// The member group set is is_admin only — project_admin is not enough.
	memberSet := memberDAGPath(f.projectID, f.reader.ID)
	body := map[string]any{"groups": []int64{f.groupA}, "active_group_id": f.groupA}
	if rec := e.do("PUT", memberSet, body, f.editor); rec.Code != http.StatusForbidden {
		t.Errorf("project_admin setting member groups: got %d, want 403 (is_admin only)", rec.Code)
	}
	if rec := e.do("PUT", memberSet, body, e.mustAdmin("admin@example.org")); rec.Code != http.StatusOK {
		t.Errorf("is_admin setting member groups: got %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestDeleteDAG covers the delete happy path and the ASM-AUTH-4 guard: a group
// with records still assigned cannot go, and the rejected call leaves no audit
// entry (REQ-AUD-004).
func TestDeleteDAG(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newDAGFixture(t, e)

	// groupA holds R1 → blocked.
	before := dagAuditRows(t, e)
	rec := e.do("DELETE", dagPath(f.projectID)+"/"+itoa(f.groupA), nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete group with records: got %d %s, want 409", rec.Code, rec.Body.String())
	}
	if after := dagAuditRows(t, e); len(after) != len(before) {
		t.Errorf("rejected delete wrote audit rows: %+v", after[len(before):])
	}

	// groupB is empty → 204, and the row is gone.
	rec = e.do("DELETE", dagPath(f.projectID)+"/"+itoa(f.groupB), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete empty group: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	rows := dagAuditRows(t, e)
	if len(rows) == 0 || rows[len(rows)-1]["event_type"] != audit.DagDeleted {
		t.Fatalf("audit = %+v, want a trailing dag_deleted", rows)
	}
	var n int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM dag_groups WHERE id = ?`, f.groupB).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("group %d survived its delete", f.groupB)
	}

	// Unknown group, and a group of another project, are both 404.
	if rec := e.do("DELETE", dagPath(f.projectID)+"/999999", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown group: got %d, want 404", rec.Code)
	}
	other := e.mustProject("Other Study")
	if rec := e.do("DELETE", dagPath(other)+"/"+itoa(f.groupA), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("group of another project: got %d, want 404", rec.Code)
	}
}

// TestMemberDAGAssignments walks the member group set: assignment drives what
// the member sees immediately (REQ-AUTH-045), the active-group rule is enforced,
// and an empty list clears.
func TestMemberDAGAssignments(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newDAGFixture(t, e)
	memberSet := memberDAGPath(f.projectID, f.reader.ID)
	statusPath := "/api/v1/projects/" + itoa(f.projectID) + "/record-status"

	visible := func() []string {
		rec := e.do("GET", statusPath, nil, f.reader)
		if rec.Code != http.StatusOK {
			t.Fatalf("record-status: got %d %s", rec.Code, rec.Body.String())
		}
		var rows []recordStatusRow
		e.decode(rec, &rows)
		var ids []string
		for _, row := range rows {
			ids = append(ids, row.RecordID)
		}
		return ids
	}
	// Ungrouped: both records.
	if got := visible(); len(got) != 2 {
		t.Fatalf("ungrouped member sees %v, want both records", got)
	}

	rec := e.do("PUT", memberSet, map[string]any{
		"groups": []int64{f.groupA}, "active_group_id": f.groupA}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign group: got %d %s", rec.Code, rec.Body.String())
	}
	// Grouped in A: only R1 — the unassigned R2 is not visible to a grouped
	// member (§4.2).
	if got := visible(); len(got) != 1 || got[0] != "R1" {
		t.Fatalf("grouped member sees %v, want [R1]", got)
	}

	// Validation: no active group with a non-empty set; unknown group; a bad
	// active id outside the set.
	if rec := e.do("PUT", memberSet, map[string]any{"groups": []int64{f.groupA}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("no active group: got %d %s, want 400", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", memberSet, map[string]any{"groups": []int64{999999}, "active_group_id": 999999}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown group: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", memberSet, map[string]any{"groups": []int64{f.groupA}, "active_group_id": f.groupB}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("active outside the set: got %d, want 400", rec.Code)
	}

	// Clearing: empty list, no active group → back to seeing everything.
	rec = e.do("PUT", memberSet, map[string]any{"groups": []int64{}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear groups: got %d %s", rec.Code, rec.Body.String())
	}
	if got := visible(); len(got) != 2 {
		t.Fatalf("after clearing, member sees %v, want both records", got)
	}

	rows := dagAuditRows(t, e)
	var changed int
	for _, row := range rows {
		if row["event_type"] == audit.DagMembershipChanged {
			changed++
			if row["member_email"] != "reader@example.org" {
				t.Errorf("membership_changed details = %+v, want the target member's email", row)
			}
		}
	}
	if changed != 2 { // the assignment and the clear; the three rejected calls wrote none
		t.Errorf("dag_membership_changed rows = %d, want 2 (audit = %+v)", changed, rows)
	}
}

// TestActiveDAGSelfService covers REQ-API-090/046: a member switches their own
// active group, only among the groups they hold, and the effect is immediate.
func TestActiveDAGSelfService(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newDAGFixture(t, e)
	activePath := "/api/v1/projects/" + itoa(f.projectID) + "/active-data-access-group"
	statusPath := "/api/v1/projects/" + itoa(f.projectID) + "/record-status"

	// Put the member in both groups with A active; add an R3 record in B so the
	// switch is observable.
	ctx := context.Background()
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: f.projectID, RecordID: "R3", DagGroupID: nullInt64(f.groupB),
	}); err != nil {
		t.Fatalf("CreateRecordEntity R3: %v", err)
	}
	rec := e.do("PUT", memberDAGPath(f.projectID, f.reader.ID),
		map[string]any{"groups": []int64{f.groupA, f.groupB}, "active_group_id": f.groupA}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign groups: got %d %s", rec.Code, rec.Body.String())
	}
	see := func() []string {
		rec := e.do("GET", statusPath, nil, f.reader)
		var rows []recordStatusRow
		e.decode(rec, &rows)
		var ids []string
		for _, row := range rows {
			ids = append(ids, row.RecordID)
		}
		return ids
	}
	if got := see(); len(got) != 1 || got[0] != "R1" {
		t.Fatalf("with A active the member sees %v, want [R1]", got)
	}

	rec = e.do("PUT", activePath, map[string]any{"group_id": f.groupB}, f.reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch active: got %d %s", rec.Code, rec.Body.String())
	}
	if got := see(); len(got) != 1 || got[0] != "R3" {
		t.Fatalf("after switching, member sees %v, want [R3] (REQ-AUTH-046: immediate)", got)
	}

	// A group the member does not hold is 400 (REQ-API-090).
	otherGroup, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center Z"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	if rec := e.do("PUT", activePath, map[string]any{"group_id": otherGroup}, f.reader); rec.Code != http.StatusBadRequest {
		t.Errorf("switch to an unheld group: got %d %s, want 400", rec.Code, rec.Body.String())
	}
	if rec := e.do("PUT", activePath, map[string]any{}, f.reader); rec.Code != http.StatusBadRequest {
		t.Errorf("missing group_id: got %d, want 400", rec.Code)
	}

	rows := dagAuditRows(t, e)
	last := rows[len(rows)-1]
	if last["event_type"] != audit.DagActiveSwitched || last["member_email"] != "reader@example.org" {
		t.Errorf("audit tail = %+v, want dag_active_switched naming the member", last)
	}
}

// TestRecordDAGAssignment covers REQ-API-091: assign, unassign, and the two
// rejection paths that matter — an invisible record (uniform 403) and an unknown
// group.
func TestRecordDAGAssignment(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newDAGFixture(t, e)
	path := func(record string) string {
		return "/api/v1/projects/" + itoa(f.projectID) + "/records/" + record + "/data-access-group"
	}

	rec := e.do("PUT", path("R2"), map[string]any{"group_id": f.groupB}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: got %d %s", rec.Code, rec.Body.String())
	}
	re := mustRecordEntity(t, e, f.projectID, "R2")
	if !re.DagGroupID.Valid || re.DagGroupID.Int64 != f.groupB {
		t.Errorf("R2 group = %v, want %d", re.DagGroupID, f.groupB)
	}

	// Unassign with null.
	if rec := e.do("PUT", path("R2"), map[string]any{"group_id": nil}, admin); rec.Code != http.StatusOK {
		t.Fatalf("unassign: got %d %s", rec.Code, rec.Body.String())
	}
	re = mustRecordEntity(t, e, f.projectID, "R2")
	if re.DagGroupID.Valid {
		t.Errorf("R2 still assigned to %d after unassign", re.DagGroupID.Int64)
	}

	// Unknown group → 400; unknown record → 404; a record outside the caller's
	// group → the uniform 403.
	if rec := e.do("PUT", path("R2"), map[string]any{"group_id": 999999}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown group: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", path("NOPE"), map[string]any{"group_id": f.groupA}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown record: got %d, want 404", rec.Code)
	}
	if rec := e.do("PUT", path("R2"), map[string]any{}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("missing group_id: got %d, want 400", rec.Code)
	}

	grouped := e.mustUser("grouped@example.org")
	e.mustMemberWithRole(f.projectID, grouped, 0) // role-less → project_admin
	asg, err := e.Store.GetAssignment(context.Background(), grouped.ID, f.projectID)
	if err != nil || asg == nil {
		t.Fatalf("assignment: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(context.Background(), &db.DagMembership{
		AssignmentID: asg.ID, GroupID: f.groupB, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	// R1 sits in group A; this caller holds only B → invisible, and the answer
	// is the 403 that discloses nothing.
	if rec := e.do("PUT", path("R1"), map[string]any{"group_id": f.groupB}, grouped); rec.Code != http.StatusForbidden {
		t.Errorf("record outside the caller's group: got %d, want 403", rec.Code)
	}

	rows := dagAuditRows(t, e)
	var assigned []map[string]any
	for _, row := range rows {
		if row["event_type"] == audit.DagRecordAssigned {
			assigned = append(assigned, row)
		}
	}
	if len(assigned) != 2 { // the assign and the unassign; rejections wrote none
		t.Fatalf("dag_record_assigned rows = %d, want 2: %+v", len(assigned), assigned)
	}
	for _, row := range assigned {
		if row["target_record"] != "R2" {
			t.Errorf("audit row = %+v, want target_record R2", row)
		}
	}
	if assigned[0]["group_id"] == nil {
		t.Errorf("assign entry lost its group_id: %+v", assigned[0])
	}
	if assigned[1]["group_id"] != nil {
		t.Errorf("unassign entry should carry a null group_id: %+v", assigned[1])
	}
}

func mustRecordEntity(t *testing.T, e *env, projectID int64, recordID string) *db.RecordEntity {
	t.Helper()
	re, err := e.Store.GetRecordEntity(context.Background(), projectID, recordID)
	if err != nil {
		t.Fatalf("GetRecordEntity: %v", err)
	}
	if re == nil {
		t.Fatalf("record entity %s missing", recordID)
	}
	return re
}
