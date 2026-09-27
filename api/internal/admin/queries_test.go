package admin

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- fixtures shared by the query tests ---

func (e *env) mustArm(projectID int64, armNum int) int64 {
	e.t.Helper()
	id, err := e.Store.AddArm(context.Background(), &db.Arm{ProjectID: projectID, ArmNum: armNum})
	if err != nil {
		e.t.Fatalf("AddArm: %v", err)
	}
	return id
}

func (e *env) mustEvent(projectID, armID int64, name, unique string) int64 {
	e.t.Helper()
	id, err := e.Store.AddEvent(context.Background(), &db.Event{
		ProjectID: projectID, ArmID: armID, EventName: name, UniqueEventName: unique,
	})
	if err != nil {
		e.t.Fatalf("AddEvent: %v", err)
	}
	return id
}

func (e *env) mustInstrument(projectID int64, name string) int64 {
	e.t.Helper()
	id, err := e.Store.AddInstrument(context.Background(), &db.Instrument{
		ProjectID: projectID, Name: name,
	})
	if err != nil {
		e.t.Fatalf("AddInstrument: %v", err)
	}
	return id
}

func (e *env) mustMap(projectID, armID int64, pairs ...db.InstrumentEvent) {
	e.t.Helper()
	if err := e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID, pairs); err != nil {
		e.t.Fatalf("SetInstrumentEventsForArm: %v", err)
	}
}

func (e *env) mustRecord(projectID int64, recordID string) {
	e.t.Helper()
	if err := e.Store.CreateRecordEntity(context.Background(),
		&db.RecordEntity{ProjectID: projectID, RecordID: recordID}); err != nil {
		e.t.Fatalf("CreateRecordEntity: %v", err)
	}
}

func (e *env) mustValue(projectID int64, recordID, event, instrument, field, value string) {
	e.t.Helper()
	if err := e.Store.AddDataValue(context.Background(), &db.DataValue{
		ProjectID: projectID, RecordID: recordID, UniqueEventName: event,
		RepeatingInstrument: instrument, FieldName: field, Value: value,
	}); err != nil {
		e.t.Fatalf("AddDataValue: %v", err)
	}
}

// mustMemberWithRole adds a member; roleID 0 means role-less (full rights).
func (e *env) mustMemberWithRole(projectID int64, u *db.User, roleID int64) {
	e.t.Helper()
	a := &db.Assignment{UserID: u.ID, ProjectID: projectID}
	if roleID > 0 {
		a.RoleID = sql.NullInt64{Int64: roleID, Valid: true}
	}
	if _, err := e.Store.AddAssignment(context.Background(), a); err != nil {
		e.t.Fatalf("AddAssignment: %v", err)
	}
}

// --- §4.13 record status ---

// TestRecordStatus covers the completion matrix (any value vs. none), the
// instrument order per event, the non-disclosure of values and membership,
// and the no-access rejection.
func TestRecordStatus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Status Study")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	v2 := e.mustEvent(projectID, armID, "Visit 2", "v2_arm_1")
	intake := e.mustInstrument(projectID, "intake") // position 1
	scores := e.mustInstrument(projectID, "scores") // position 2
	e.mustMap(projectID, armID,
		db.InstrumentEvent{InstrumentID: intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: scores, EventID: v1},
		db.InstrumentEvent{InstrumentID: intake, EventID: v2},
	)
	e.mustRecord(projectID, "R1")
	e.mustRecord(projectID, "R2")
	e.mustValue(projectID, "R1", "v1_arm_1", "intake", "age", "42") // complete
	e.mustValue(projectID, "R1", "v2_arm_1", "intake", "age", "")   // empty ≠ value

	rec := e.do("GET", "/api/v1/projects/999999/record-status", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: got %d, want 404", rec.Code)
	}

	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin record-status: got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "42") {
		t.Fatalf("response leaks field values: %s", rec.Body.String())
	}
	var rows []recordStatusRow
	e.decode(rec, &rows)
	if len(rows) != 2 || rows[0].RecordID != "R1" || rows[1].RecordID != "R2" {
		t.Fatalf("rows: %+v", rows)
	}
	if len(rows[0].Events) != 2 {
		t.Fatalf("R1 events: %+v", rows[0].Events)
	}
	e1 := rows[0].Events[0] // v1_arm_1 — intake, scores in instrument order
	if e1.UniqueEventName != "v1_arm_1" || len(e1.Instruments) != 2 ||
		e1.Instruments[0].Name != "intake" || !e1.Instruments[0].Complete ||
		e1.Instruments[1].Name != "scores" || e1.Instruments[1].Complete {
		t.Fatalf("R1 v1 state: %+v", e1)
	}
	e2 := rows[0].Events[1] // v2_arm_1 — intake only; empty value = incomplete
	if e2.UniqueEventName != "v2_arm_1" || len(e2.Instruments) != 1 ||
		e2.Instruments[0].Complete {
		t.Fatalf("R1 v2 state: %+v", e2)
	}
	for _, ev := range rows[1].Events { // R2 has no values at all
		for _, in := range ev.Instruments {
			if in.Complete {
				t.Fatalf("R2 unexpectedly complete: %+v", ev)
			}
		}
	}

	// A record-status read writes no audit row (ASM-AUD-2).
	before := len(e.auditTypes())
	e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, admin)
	if after := len(e.auditTypes()); after != before {
		t.Fatalf("record-status wrote audit entries: %v", e.auditTypes())
	}

	// Non-member: uniform 403 (REQ-API-007).
	outsider := e.mustUser("out@example.org")
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, outsider)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member: got %d, want 403", rec.Code)
	}

	// Member whose role grants no data access on any arm: 403 (GD-2).
	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: projectID, RoleName: "watcher"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "no_access", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	watcher := e.mustUser("watch@example.org")
	e.mustMemberWithRole(projectID, watcher, roleID)
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, watcher)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no-access member: got %d, want 403", rec.Code)
	}

	// A read_only member sees the dashboard.
	reader := e.mustUser("read@example.org")
	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	e.mustMemberWithRole(projectID, reader, readRole)
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("read_only member: got %d %s", rec.Code, rec.Body.String())
	}
}

// TestRecordStatusDAG covers the data-access-group record scope (REQ-AUTH-045):
// a grouped member sees only their group's records; unassigned records are not
// visible to them; ungrouped members and administrators see all.
func TestRecordStatusDAG(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("DAG Study")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	instr := e.mustInstrument(projectID, "intake")
	e.mustMap(projectID, armID, db.InstrumentEvent{InstrumentID: instr, EventID: v1})

	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: projectID, Name: "Center A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: projectID, RecordID: "R1", DagGroupID: sql.NullInt64{Int64: groupA, Valid: true},
	}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	e.mustRecord(projectID, "R2") // unassigned

	grouped := e.mustUser("grouped@example.org")
	e.mustMemberWithRole(projectID, grouped, 0)
	asg, err := e.Store.GetAssignment(ctx, grouped.ID, projectID)
	if err != nil || asg == nil {
		t.Fatalf("assignment: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: asg.ID, GroupID: groupA, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, grouped)
	if rec.Code != http.StatusOK {
		t.Fatalf("grouped member: got %d %s", rec.Code, rec.Body.String())
	}
	var rows []recordStatusRow
	e.decode(rec, &rows)
	if len(rows) != 1 || rows[0].RecordID != "R1" {
		t.Fatalf("grouped visibility: %+v", rows)
	}

	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/record-status", nil, admin)
	e.decode(rec, &rows)
	if len(rows) != 2 {
		t.Fatalf("admin sees all records: %+v", rows)
	}
}

// --- §4.15 audit log ---

func TestListAudit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	p2 := e.mustProject("Study Two")
	member := e.mustUser("member@example.org")
	e.mustMemberWithRole(p1, member, 0)

	for _, pid := range []int64{p1, p2} {
		if err := e.Audit.Insert(ctx, audit.Entry{
			EventType: audit.ProjectUpdated, Source: audit.SourceUI,
			UserID: admin.ID, Email: admin.Email, ProjectID: pid,
			Details: map[string]any{"changes": map[string]any{}},
		}); err != nil {
			t.Fatalf("audit insert: %v", err)
		}
	}
	// A project-less entry (boundary event) — visible to admins only.
	if err := e.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginSuccess, Source: audit.SourceUI,
		UserID: admin.ID, Email: admin.Email, Details: map[string]any{},
	}); err != nil {
		t.Fatalf("audit insert: %v", err)
	}

	// Admin without filters: all three entries, reverse chronological.
	rec := e.do("GET", "/api/v1/audit", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit: got %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Entries    []auditEventEntry `json:"entries"`
		NextCursor *string           `json:"next_cursor"`
	}
	e.decode(rec, &page)
	if len(page.Entries) != 3 || page.NextCursor != nil {
		t.Fatalf("admin audit entries: %s", rec.Body.String())
	}
	for i := 1; i < len(page.Entries); i++ {
		if page.Entries[i-1].ID <= page.Entries[i].ID {
			t.Fatalf("not reverse chronological: %+v", page.Entries)
		}
	}

	// Non-admin: the project filter is required (REQ-API-078).
	rec = e.do("GET", "/api/v1/audit", nil, member)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing project filter: got %d, want 400", rec.Code)
	}
	// Non-member of the queried project: uniform 403.
	outsider := e.mustUser("out@example.org")
	rec = e.do("GET", "/api/v1/audit?project="+itoa(p2), nil, outsider)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member query: got %d, want 403", rec.Code)
	}
	// Member sees only their project's entries.
	rec = e.do("GET", "/api/v1/audit?project="+itoa(p1), nil, member)
	e.decode(rec, &page)
	if len(page.Entries) != 1 || page.Entries[0].EventType != audit.ProjectUpdated {
		t.Fatalf("member scope: %s", rec.Body.String())
	}

	// event_type and user filters (admin).
	rec = e.do("GET", "/api/v1/audit?event_type="+audit.LoginSuccess, nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 1 || page.Entries[0].EventType != audit.LoginSuccess {
		t.Fatalf("event_type filter: %s", rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/audit?user="+itoa(member.ID), nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("user filter: %s", rec.Body.String())
	}

	// from/to (UTC, inclusive).
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	rec = e.do("GET", "/api/v1/audit?from="+tomorrow, nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("future from: %s", rec.Body.String())
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	rec = e.do("GET", "/api/v1/audit?to="+yesterday, nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("past to: %s", rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/audit?from=2000-01-01&to=notadate", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad to: got %d, want 400", rec.Code)
	}

	// Pagination walk with limit=1: every entry exactly once, newest first.
	var seen []int64
	cursor := ""
	for i := 0; i < 10; i++ {
		path := "/api/v1/audit?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec = e.do("GET", path, nil, admin)
		e.decode(rec, &page)
		for _, en := range page.Entries {
			seen = append(seen, en.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 3 || seen[0] < seen[1] || seen[1] < seen[2] {
		t.Fatalf("cursor walk: %v", seen)
	}
}

func TestListAuditViews(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Views Study")

	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := e.Audit.InsertViewTx(ctx, tx, audit.RecordView{
		UserID: admin.ID, Email: admin.Email, Token: "pull-token", ProjectID: p1,
		RecordIDs: []string{"R1", "R2"}, Instruments: []string{"intake"},
	}); err != nil {
		t.Fatalf("InsertViewTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rec := e.do("GET", "/api/v1/audit?type=views&project="+itoa(p1), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("views: got %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Entries []struct {
			Token       string   `json:"token"`
			ProjectID   int64    `json:"project_id"`
			RecordIDs   []string `json:"record_ids"`
			Instruments []string `json:"instruments"`
		} `json:"entries"`
	}
	e.decode(rec, &page)
	if len(page.Entries) != 1 || page.Entries[0].Token != "pull-token" ||
		len(page.Entries[0].RecordIDs) != 2 || len(page.Entries[0].Instruments) != 1 {
		t.Fatalf("views entry: %s", rec.Body.String())
	}

	// event_type is meaningless for the views table.
	rec = e.do("GET", "/api/v1/audit?type=views&event_type=x", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("views with event_type: got %d, want 400", rec.Code)
	}
	rec = e.do("GET", "/api/v1/audit?type=nope", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type: got %d, want 400", rec.Code)
	}
}

// --- §4.16 record history ---

// insertHistory writes one data-change audit entry of the §3.2 shape through
// the real writer (target_record set, details payload as stored).
func (e *env) insertHistory(projectID int64, user *db.User, record, action, instrument, event string, fields []map[string]any) {
	e.t.Helper()
	if err := e.Audit.Insert(context.Background(), audit.Entry{
		EventType: map[string]string{
			"create": audit.RecordCreated, "update": audit.RecordUpdated,
			"delete": audit.RecordDeleted,
		}[action],
		Source: audit.SourceAPI, UserID: user.ID, Email: user.Email,
		ProjectID: projectID, TargetRecord: record,
		Details: map[string]any{
			"action": action, "record_id": record,
			"instrument": instrument, "event": event, "fields": fields,
		},
	}); err != nil {
		e.t.Fatalf("insert history entry: %v", err)
	}
}

func (e *env) historyPath(projectID int64, record string) string {
	return "/api/v1/projects/" + itoa(projectID) + "/records/" + record + "/history"
}

func TestRecordHistory(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	entry := e.mustUser("entry@example.org")
	projectID := e.mustProject("History Study")
	armID := e.mustArm(projectID, 1)
	e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	e.mustRecord(projectID, "R1")

	e.insertHistory(projectID, entry, "R1", "create", "intake", "v1_arm_1",
		[]map[string]any{{"field": "age", "old": nil, "new": "42"}})
	e.insertHistory(projectID, entry, "R1", "update", "scores", "v1_arm_1",
		[]map[string]any{{"field": "score", "old": "1", "new": "2"}})
	e.insertHistory(projectID, entry, "R1", "delete", "intake", "v1_arm_1",
		[]map[string]any{{"field": "age", "old": "42", "new": nil}})

	rec := e.do("GET", e.historyPath(projectID, "R1"), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: got %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Entries    []historyEntry `json:"entries"`
		NextCursor *string        `json:"next_cursor"`
	}
	e.decode(rec, &page)
	if len(page.Entries) != 3 || page.NextCursor != nil {
		t.Fatalf("history entries: %s", rec.Body.String())
	}
	want := []struct{ action, instrument string }{
		{"create", "intake"}, {"update", "scores"}, {"delete", "intake"},
	}
	for i, e0 := range page.Entries {
		if e0.Action != want[i].action || e0.Instrument != want[i].instrument ||
			e0.Event != "v1_arm_1" || e0.UserDisplayName == nil || *e0.UserDisplayName != "User" {
			t.Fatalf("entry %d: %+v", i, e0)
		}
	}
	if page.Entries[0].Fields[0].Old != nil || page.Entries[0].Fields[0].New == nil ||
		*page.Entries[0].Fields[0].New != "42" {
		t.Fatalf("create old/new: %+v", page.Entries[0].Fields)
	}
	if page.Entries[2].Fields[0].New != nil || page.Entries[2].Fields[0].Old == nil ||
		*page.Entries[2].Fields[0].Old != "42" {
		t.Fatalf("delete old/new (REQ-AUD-009): %+v", page.Entries[2].Fields)
	}

	// Filters (REQ-API-080).
	rec = e.do("GET", e.historyPath(projectID, "R1")+"?instrument=intake", nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 2 {
		t.Fatalf("instrument filter: %s", rec.Body.String())
	}
	rec = e.do("GET", e.historyPath(projectID, "R1")+"?field=score", nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 1 || page.Entries[0].Action != "update" {
		t.Fatalf("field filter: %s", rec.Body.String())
	}
	rec = e.do("GET", e.historyPath(projectID, "R1")+"?event=v9_arm_1", nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("event filter: %s", rec.Body.String())
	}

	// Cursor walk over the record page.
	var actions []string
	cursor := ""
	for i := 0; i < 10; i++ {
		path := e.historyPath(projectID, "R1") + "?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec = e.do("GET", path, nil, admin)
		e.decode(rec, &page)
		for _, en := range page.Entries {
			actions = append(actions, en.Action)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(actions) != 3 || actions[0] != "create" || actions[2] != "delete" {
		t.Fatalf("cursor walk: %v", actions)
	}

	// Unknown record and non-member.
	rec = e.do("GET", e.historyPath(projectID, "NOPE"), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown record: got %d, want 200 with empty history", rec.Code)
	}
	outsider := e.mustUser("out@example.org")
	rec = e.do("GET", e.historyPath(projectID, "R1"), nil, outsider)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member: got %d, want 403", rec.Code)
	}
}

// TestRecordHistoryVisibility covers the record-scoped gates: DAG visibility
// (REQ-AUTH-045) and per-arm read access (GD-2).
func TestRecordHistoryVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	entry := e.mustUser("entry@example.org")
	projectID := e.mustProject("Gated Study")
	arm1 := e.mustArm(projectID, 1)
	arm2 := e.mustArm(projectID, 2)
	e.mustEvent(projectID, arm1, "Visit 1", "v1_arm_1")
	e.mustEvent(projectID, arm2, "Visit 1", "v1_arm_2")

	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: projectID, Name: "A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	groupB, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: projectID, Name: "B"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: projectID, RecordID: "R1", DagGroupID: sql.NullInt64{Int64: groupA, Valid: true},
	}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	e.insertHistory(projectID, entry, "R1", "create", "intake", "v1_arm_1",
		[]map[string]any{{"field": "age", "old": nil, "new": "42"}})

	// Member whose active group is B: the record is not visible — 403.
	inGroupB := e.mustUser("b@example.org")
	e.mustMemberWithRole(projectID, inGroupB, 0)
	asg, err := e.Store.GetAssignment(ctx, inGroupB.ID, projectID)
	if err != nil || asg == nil {
		t.Fatalf("assignment: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: asg.ID, GroupID: groupB, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	rec := e.do("GET", e.historyPath(projectID, "R1"), nil, inGroupB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("record outside active group: got %d, want 403", rec.Code)
	}

	// Member with read_only on arm 2 only: visible and allowed (AnyData), but
	// the entries belong to arm 1 — omitted from the response (GD-2).
	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: projectID, RoleName: "arm2-reader"},
		[]db.RoleArm{{ArmNum: 2, DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	arm2Only := e.mustUser("arm2@example.org")
	e.mustMemberWithRole(projectID, arm2Only, roleID)
	rec = e.do("GET", e.historyPath(projectID, "R1"), nil, arm2Only)
	if rec.Code != http.StatusOK {
		t.Fatalf("arm-2 reader: got %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Entries []historyEntry `json:"entries"`
	}
	e.decode(rec, &page)
	if len(page.Entries) != 0 {
		t.Fatalf("arm-1 entries leaked to arm-2 reader: %s", rec.Body.String())
	}
}
