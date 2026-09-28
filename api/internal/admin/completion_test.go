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

// --- §4.13 completion write endpoint (REQ-API-110, REQ-AUD-026) ---

// completionPath builds the cell address of §4.13: the event is named by its
// unique_event_name (what record-status returns and the dashboard binds to),
// the instrument by id like every other instrument route.
func completionPath(projectID int64, record, event string, instrumentID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/records/" + record +
		"/events/" + event + "/instruments/" + itoa(instrumentID) + "/completion"
}

// completionFixture is a one-arm project with two mapped instruments at
// v1_arm_1 (intake, scores) and one survey instrument mapped alongside them,
// plus record R1 holding a value in intake only.
type completionFixture struct {
	projectID int64
	intake    int64
	scores    int64
	survey    int64
}

func newCompletionFixture(t *testing.T, e *env) completionFixture {
	t.Helper()
	ctx := context.Background()
	f := completionFixture{}
	f.projectID = e.mustProject("Completion Study")
	armID := e.mustArm(f.projectID, 1)
	v1 := e.mustEvent(f.projectID, armID, "Visit 1", "v1_arm_1")
	f.intake = e.mustInstrument(f.projectID, "intake")
	f.scores = e.mustInstrument(f.projectID, "scores")
	surveyID, err := e.Store.AddInstrument(ctx, &db.Instrument{
		ProjectID: f.projectID, Name: "feedback", Position: 3, IsSurvey: true,
	})
	if err != nil {
		t.Fatalf("AddInstrument(survey): %v", err)
	}
	f.survey = surveyID
	e.mustMap(f.projectID, armID,
		db.InstrumentEvent{InstrumentID: f.intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: f.scores, EventID: v1},
		db.InstrumentEvent{InstrumentID: surveyID, EventID: v1},
	)
	e.mustRecord(f.projectID, "R1")
	e.mustValue(f.projectID, "R1", "v1_arm_1", "intake", "age", "42") // some_data
	return f
}

// stateOf reads one cell back through the dashboard endpoint.
func stateOf(t *testing.T, e *env, actor *db.User, f completionFixture, event, instrument string) string {
	t.Helper()
	rec := e.do("GET", "/api/v1/projects/"+itoa(f.projectID)+"/record-status", nil, actor)
	if rec.Code != http.StatusOK {
		t.Fatalf("record-status: got %d %s", rec.Code, rec.Body.String())
	}
	var rows []recordStatusRow
	e.decode(rec, &rows)
	for _, row := range rows {
		if row.RecordID != "R1" {
			continue
		}
		for _, ev := range row.Events {
			if ev.UniqueEventName != event {
				continue
			}
			for _, in := range ev.Instruments {
				if in.Name == instrument {
					return in.State
				}
			}
		}
	}
	t.Fatalf("cell (%s, %s) absent from %v", event, instrument, rows)
	return ""
}

// auditRow is one completion audit entry with the columns that carry its
// meaning: the code, the record it points at, and the details payload.
type auditRow struct {
	EventType    string
	Source       string
	TargetRecord string
	ArmNum       int
	Details      completionDetails
}

func completionAuditRows(t *testing.T, e *env) []auditRow {
	t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT event_type, source, target_record, arm_num, details
		 FROM audit_events WHERE event_type IN (?, ?) ORDER BY id`,
		audit.InstrumentCompleted, audit.InstrumentUncompleted)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var (
			a       auditRow
			target  sql.NullString
			armNum  sql.NullInt64
			details sql.NullString
		)
		if err := rows.Scan(&a.EventType, &a.Source, &target, &armNum, &details); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		a.TargetRecord = target.String
		a.ArmNum = int(armNum.Int64)
		if details.Valid {
			if err := json.Unmarshal([]byte(details.String), &a.Details); err != nil {
				t.Fatalf("audit details %q: %v", details.String, err)
			}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

// TestPutCompletion walks the assignment lifecycle: set → the stored third
// state; clear → back to the derived state; and one audit entry per actual
// change, with no field value written anywhere (REQ-API-110).
func TestPutCompletion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	f := newCompletionFixture(t, e)
	path := completionPath(f.projectID, "R1", "v1_arm_1", f.intake)

	if got := stateOf(t, e, admin, f, "v1_arm_1", "intake"); got != StateSomeData {
		t.Fatalf("initial state = %q, want %s", got, StateSomeData)
	}

	rec := e.do("PUT", path, map[string]string{"state": StateFinished}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("set finished: got %d %s", rec.Code, rec.Body.String())
	}
	var body completionObject
	e.decode(rec, &body)
	if body.State != StateFinished {
		t.Errorf("response = %+v, want state finished", body)
	}
	if got := stateOf(t, e, admin, f, "v1_arm_1", "intake"); got != StateFinished {
		t.Errorf("after set: state = %q, want %s", got, StateFinished)
	}
	// finished overrides the derived state of a cell that holds values, and it
	// says nothing about its neighbours in the same event.
	if got := stateOf(t, e, admin, f, "v1_arm_1", "scores"); got != StateNoData {
		t.Errorf("scores = %q, want %s (assignment is per cell)", got, StateNoData)
	}

	// One audit entry, shaped per Audit_Logging_Design.md §3.2, source ui.
	rows := completionAuditRows(t, e)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1: %+v", len(rows), rows)
	}
	want := auditRow{
		EventType: audit.InstrumentCompleted, Source: audit.SourceUI,
		TargetRecord: "R1", ArmNum: 1,
		Details: completionDetails{RecordID: "R1", Event: "v1_arm_1", Instrument: "intake"},
	}
	if rows[0].EventType != want.EventType || rows[0].Source != want.Source ||
		rows[0].TargetRecord != want.TargetRecord || rows[0].ArmNum != want.ArmNum ||
		rows[0].Details != want.Details {
		t.Errorf("audit row = %+v, want %+v", rows[0], want)
	}

	// Idempotent: the same PUT answers 200 again and writes no second entry
	// (REQ-API-042 — as the mode endpoint treats a no-op).
	rec = e.do("PUT", path, map[string]string{"state": StateFinished}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat set: got %d %s", rec.Code, rec.Body.String())
	}
	if rows := completionAuditRows(t, e); len(rows) != 1 {
		t.Errorf("repeat set wrote audit rows: %+v", rows)
	}

	// Clearing returns the cell to its derived state — some_data here, because
	// R1 holds a value in intake (REQ-API-074).
	rec = e.do("PUT", path, map[string]string{"state": StateUnfinished}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: got %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &body)
	if body.State != StateUnfinished {
		t.Errorf("response = %+v, want state unfinished", body)
	}
	if got := stateOf(t, e, admin, f, "v1_arm_1", "intake"); got != StateSomeData {
		t.Errorf("after clear: state = %q, want the derived %s", got, StateSomeData)
	}
	rows = completionAuditRows(t, e)
	if len(rows) != 2 || rows[1].EventType != audit.InstrumentUncompleted {
		t.Fatalf("audit rows after clear = %+v, want a second instrument_uncompleted", rows)
	}
	// Clearing an unfinished cell changes nothing and audits nothing.
	e.do("PUT", path, map[string]string{"state": StateUnfinished}, admin)
	if rows := completionAuditRows(t, e); len(rows) != 2 {
		t.Errorf("repeat clear wrote audit rows: %+v", rows)
	}

	// No field value was written by any of this (REQ-API-110): the project's
	// data table still holds exactly the one seeded value.
	var values int
	if err := e.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM data WHERE project_id = ?`, f.projectID).Scan(&values); err != nil {
		t.Fatalf("count data: %v", err)
	}
	if values != 1 {
		t.Errorf("data rows = %d, want the single seeded value (the call writes none)", values)
	}
}

// TestPutCompletionPermissions covers the level and visibility gates: view_edit
// on the event's arm is required, read_only is not enough, and a record outside
// the caller's data-access-group scope answers the uniform 403.
func TestPutCompletionPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := newCompletionFixture(t, e)
	path := completionPath(f.projectID, "R1", "v1_arm_1", f.intake)

	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("reader@example.org")
	e.mustMemberWithRole(f.projectID, reader, readRole)
	if rec := e.do("PUT", path, map[string]string{"state": StateFinished}, reader); rec.Code != http.StatusForbidden {
		t.Errorf("read_only member: got %d %s, want 403", rec.Code, rec.Body.String())
	}

	editRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "editor"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	editor := e.mustUser("editor@example.org")
	e.mustMemberWithRole(f.projectID, editor, editRole)
	if rec := e.do("PUT", path, map[string]string{"state": StateFinished}, editor); rec.Code != http.StatusOK {
		t.Errorf("view_edit member: got %d %s, want 200", rec.Code, rec.Body.String())
	}

	// A non-member gets the same uniform 403 as any other outsider.
	outsider := e.mustUser("outsider@example.org")
	if rec := e.do("PUT", path, map[string]string{"state": StateFinished}, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-member: got %d, want 403", rec.Code)
	}

	// DAG scope (REQ-AUTH-045): a grouped member cannot annotate a record that
	// belongs to another group.
	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: mustAssignment(t, e, editor.ID, f.projectID).ID, GroupID: groupA, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	if rec := e.do("PUT", path, map[string]string{"state": StateFinished}, editor); rec.Code != http.StatusForbidden {
		t.Errorf("grouped member, unassigned record: got %d, want 403", rec.Code)
	}
	// An unknown record id for a grouped member is the same 403 — it never
	// discloses whether the record exists (REQ-API-007).
	unknown := completionPath(f.projectID, "NOPE", "v1_arm_1", f.intake)
	if rec := e.do("PUT", unknown, map[string]string{"state": StateFinished}, editor); rec.Code != http.StatusForbidden {
		t.Errorf("grouped member, unknown record: got %d, want 403 (not a crash)", rec.Code)
	}
}

func mustAssignment(t *testing.T, e *env, userID, projectID int64) *db.Assignment {
	t.Helper()
	a, err := e.Store.GetAssignment(context.Background(), userID, projectID)
	if err != nil || a == nil {
		t.Fatalf("assignment for user %d: %v", userID, err)
	}
	return a
}

// TestPutCompletionRejections covers the 404 and 409 cases of §4.13 and the
// body validation: unknown event / instrument / record are 404; an unmapped
// (event, instrument) pair and a survey instrument are 409; anything but
// finished or unfinished in state is 400.
func TestPutCompletionRejections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	f := newCompletionFixture(t, e)

	// A second event with nothing mapped to it.
	const unmappedEvent = "v2_arm_2"
	arm2 := e.mustArm(f.projectID, 2)
	e.mustEvent(f.projectID, arm2, "Visit 2", unmappedEvent)

	otherProject := e.mustProject("Other Study")

	cases := []struct {
		name   string
		path   string
		body   any
		status int
	}{
		{"unknown event", completionPath(f.projectID, "R1", "nope_arm_1", f.intake),
			map[string]string{"state": StateFinished}, http.StatusNotFound},
		{"unknown instrument", completionPath(f.projectID, "R1", "v1_arm_1", 999999),
			map[string]string{"state": StateFinished}, http.StatusNotFound},
		{"unknown record", completionPath(f.projectID, "NOPE", "v1_arm_1", f.intake),
			map[string]string{"state": StateFinished}, http.StatusNotFound},
		{"instrument of another project", completionPath(otherProject, "R1", "v1_arm_1", f.intake),
			map[string]string{"state": StateFinished}, http.StatusNotFound},
		{"unmapped pair", completionPath(f.projectID, "R1", unmappedEvent, f.intake),
			map[string]string{"state": StateFinished}, http.StatusConflict},
		{"survey instrument", completionPath(f.projectID, "R1", "v1_arm_1", f.survey),
			map[string]string{"state": StateFinished}, http.StatusConflict},
		{"missing state", completionPath(f.projectID, "R1", "v1_arm_1", f.intake),
			map[string]string{}, http.StatusBadRequest},
		{"unknown state", completionPath(f.projectID, "R1", "v1_arm_1", f.intake),
			map[string]string{"state": "done"}, http.StatusBadRequest},
		{"state not a string", completionPath(f.projectID, "R1", "v1_arm_1", f.intake),
			map[string]any{"state": true}, http.StatusBadRequest},
		{"unknown attribute", completionPath(f.projectID, "R1", "v1_arm_1", f.intake),
			map[string]string{"state": StateFinished, "instrument": "intake"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := e.do("PUT", tc.path, tc.body, admin)
		if rec.Code != tc.status {
			t.Errorf("%s: got %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
		}
	}

	// An unknown project is 404 like everywhere else.
	unknownProject := "/api/v1/projects/999999/records/R1/events/v1_arm_1/instruments/1/completion"
	if rec := e.do("PUT", unknownProject, map[string]string{"state": StateFinished}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: got %d, want 404", rec.Code)
	}

	// None of the rejected calls stored anything or audited a change.
	if rows := completionAuditRows(t, e); len(rows) != 0 {
		t.Errorf("rejected calls wrote audit rows: %+v", rows)
	}
	var stored int
	if err := e.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM instrument_completion`).Scan(&stored); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 0 {
		t.Errorf("%d assignment(s) stored by rejected calls", stored)
	}
}

// TestPutCompletionInAnalysisMode pins the GD-20 scoping: analysis mode rejects
// data entry, but the completion assignment is a workflow annotation that
// writes no field value, so it stays available (REQ-API-110, §4.13).
func TestPutCompletionInAnalysisMode(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newCompletionFixture(t, e)

	// development → production → analysis, per the GD-20 transition table.
	for _, body := range []map[string]any{
		{"mode": "production", "keep_data": true},
		{"mode": "analysis"},
	} {
		rec := e.do("PUT", "/api/v1/projects/"+itoa(f.projectID)+"/mode", body, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("mode → %v: got %d %s", body["mode"], rec.Code, rec.Body.String())
		}
	}

	path := completionPath(f.projectID, "R1", "v1_arm_1", f.intake)
	if rec := e.do("PUT", path, map[string]string{"state": StateFinished}, admin); rec.Code != http.StatusOK {
		t.Errorf("completion in analysis mode: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := stateOf(t, e, admin, f, "v1_arm_1", "intake"); got != StateFinished {
		t.Errorf("state = %q, want %s", got, StateFinished)
	}
}

// TestPutCompletionSurveyNeverStored guards the read side too: even a row that
// should never exist cannot make a survey instrument report finished (GD-9).
func TestPutCompletionSurveyNeverStored(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	f := newCompletionFixture(t, e)

	events, err := e.Store.ListEvents(ctx, f.projectID)
	if err != nil || len(events) == 0 {
		t.Fatalf("events: %v", err)
	}
	// Insert the row behind the endpoint's back to prove the read guards it.
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := e.Store.SetInstrumentCompletionTx(ctx, tx, f.projectID, "R1",
		events[0].ID, f.survey, admin.ID); err != nil {
		t.Fatalf("SetInstrumentCompletionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if got := stateOf(t, e, admin, f, "v1_arm_1", "feedback"); got != StateNoData {
		t.Errorf("survey state = %q, want the derived %s", got, StateNoData)
	}
}
