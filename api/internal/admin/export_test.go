package admin

// The UI-surface export of §4.14 (REQ-API-075/076): candidate-arm resolution
// and the lowest-wins sensitivity (D-4), arm validation, DAG scoping, the
// label axes, and the audit event with surface "ui". The rendering pipeline
// itself is covered by the data API tests; these exercise the administration
// boundary around it.

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// exportFixture seeds a two-arm project: arm 1 has event baseline_arm_1 with
// record REC-001, arm 2 has visit_arm_2 with record REC-002. The demo
// instrument carries one field of each sensitivity category.
type exportFixture struct {
	e      *env
	pid    int64
	groupA int64 // data access groups for the DAG scoping test
	groupB int64
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	e := newEnv(t)
	e.Cfg.AnonDateShiftMin, e.Cfg.AnonDateShiftMax = 1, 365
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	pid := e.mustProject("export")
	arm1, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	must(err)
	arm2, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 2})
	must(err)
	ev1, err := e.Store.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: arm1,
		EventName: "baseline", UniqueEventName: "baseline_arm_1"})
	must(err)
	ev2, err := e.Store.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: arm2,
		EventName: "visit", UniqueEventName: "visit_arm_2"})
	must(err)

	demoID, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "demo", Position: 1})
	must(err)
	add := func(f db.Field, pos int) {
		t.Helper()
		f.ProjectID, f.InstrumentID, f.Position = pid, demoID, pos
		if _, err := e.Store.AddField(ctx, &f); err != nil {
			t.Fatalf("seed AddField %s: %v", f.FieldName, err)
		}
	}
	add(db.Field{FieldName: "record_id", FieldType: "text"}, 1)
	add(db.Field{FieldName: "age", FieldType: "text",
		FieldLabel:     sql.NullString{String: "Age", Valid: true},
		ValidationType: sql.NullString{String: "integer", Valid: true}}, 2)
	add(db.Field{FieldName: "status", FieldType: "dropdown",
		Choices: sql.NullString{String: "1$Pending##2$Done", Valid: true}}, 3)
	add(db.Field{FieldName: "notes", FieldType: "text"}, 4) // free text, not export-approved
	add(db.Field{FieldName: "contact_email", FieldType: "text",
		ValidationType: sql.NullString{String: "email", Valid: true}}, 5)
	add(db.Field{FieldName: "secret", FieldType: "text", PersonalInformation: true}, 6)
	add(db.Field{FieldName: "visit_date", FieldType: "text",
		ValidationType: sql.NullString{String: "date", Valid: true}, ExportApproved: true}, 7)

	must(e.Store.SetInstrumentEventsForArm(ctx, pid, arm1, []db.InstrumentEvent{
		{InstrumentID: demoID, EventID: ev1}}))
	must(e.Store.SetInstrumentEventsForArm(ctx, pid, arm2, []db.InstrumentEvent{
		{InstrumentID: demoID, EventID: ev2}}))

	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: pid, Name: "Center A"})
	must(err)
	groupB, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: pid, Name: "Center B"})
	must(err)

	seed := func(recordID, event string, group int64) {
		t.Helper()
		must(e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
			ProjectID: pid, RecordID: recordID,
			DagGroupID: sql.NullInt64{Int64: group, Valid: true}}))
		for _, v := range []struct{ field, value string }{
			{"record_id", recordID},
			{"age", "41"},
			{"status", "2"},
			{"notes", "=SUM(1)"}, // formula-neutralised in CSV (REQ-VAL-032)
			{"contact_email", "participant@example.org"},
			{"secret", "very private"},
			{"visit_date", "2026-05-04"},
		} {
			must(e.Store.UpsertDataValue(ctx, &db.DataValue{
				ProjectID: pid, RecordID: recordID, UniqueEventName: event,
				FieldName: v.field, Value: v.value}))
		}
	}
	seed("REC-001", "baseline_arm_1", groupA)
	seed("REC-002", "visit_arm_2", groupB)

	return &exportFixture{e: e, pid: pid, groupA: groupA, groupB: groupB}
}

// member inserts a user with an assignment on the fixture project.
func (f *exportFixture) member(t *testing.T, email string, roleID int64) *db.User {
	t.Helper()
	u := f.e.mustUser(email)
	a := &db.Assignment{UserID: u.ID, ProjectID: f.pid, Token: "tok-" + email}
	if roleID > 0 {
		a.RoleID = sql.NullInt64{Int64: roleID, Valid: true}
	}
	if _, err := f.e.Store.AddAssignment(context.Background(), a); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	return u
}

// role creates a role with per-arm export levels.
func (f *exportFixture) role(t *testing.T, name string, arms ...db.RoleArm) int64 {
	t.Helper()
	id, err := f.e.Store.CreateRole(context.Background(),
		&db.Role{ProjectID: f.pid, RoleName: name}, arms, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	return id
}

func (f *exportFixture) path(query string) string {
	p := "/api/v1/projects/" + itoa(f.pid) + "/export"
	if query != "" {
		p += "?" + query
	}
	return p
}

func csvRows(t *testing.T, body string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV %q: %v", body, err)
	}
	return rows
}

// lastAudit returns (source, details) of the newest event of one type.
func (f *exportFixture) lastAudit(t *testing.T, eventType string) (string, map[string]any) {
	t.Helper()
	var source, details string
	err := f.e.Store.DB.QueryRow(
		`SELECT source, details FROM audit_events WHERE event_type = ? ORDER BY id DESC LIMIT 1`,
		eventType,
	).Scan(&source, &details)
	if err == sql.ErrNoRows {
		t.Fatalf("no %s audit row", eventType)
	}
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(details), &d); err != nil {
		t.Fatalf("decode details %q: %v", details, err)
	}
	return source, d
}

// lastExportAudit returns (source, details) of the newest export event.
func (f *exportFixture) lastExportAudit(t *testing.T) (string, map[string]any) {
	t.Helper()
	return f.lastAudit(t, "export")
}

func TestExportAdminFullCSVAndAudit(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")

	rec := f.e.do(http.MethodGet, f.path(""), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	rows := csvRows(t, rec.Body.String())
	wantHeader := []string{"record_id", "redcap_event_name", "redcap_repeat_instrument",
		"redcap_repeat_instance", "age", "status", "notes", "contact_email", "secret", "visit_date",
		"demo_complete"} // REQ-API-134: whole-instrument export ends the block with completion
	if len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("CSV = %v, want header %v and two data rows", rows, wantHeader)
	}
	if rows[1][10] != "1" { // values present, no finished assignment (REQ-API-074 derivation)
		t.Errorf("demo_complete = %q, want 1", rows[1][10])
	}
	if rows[1][0] != "REC-001" || rows[1][1] != "baseline_arm_1" {
		t.Errorf("row 1 = %v, want REC-001 at baseline_arm_1", rows[1])
	}
	if rows[2][0] != "REC-002" || rows[2][1] != "visit_arm_2" {
		t.Errorf("row 2 = %v, want REC-002 at visit_arm_2", rows[2])
	}
	if rows[1][6] != "'=SUM(1)" { // formula neutralisation, CSV only (REQ-VAL-032)
		t.Errorf("notes cell = %q, want the neutralised '=SUM(1)", rows[1][6])
	}

	source, d := f.lastExportAudit(t)
	if source != "ui" {
		t.Errorf("audit source = %q, want ui", source)
	}
	if d["surface"] != "ui" || d["sensitivity"] != "export_full" {
		t.Errorf("details = %v, want surface ui and sensitivity export_full", d)
	}
	filters, _ := d["filters"].(map[string]any)
	for _, key := range []string{"records", "fields", "forms", "events"} {
		if arr, ok := filters[key].([]any); !ok || len(arr) != 0 {
			t.Errorf("filters[%s] = %v, want an empty array (§3.5)", key, filters[key])
		}
	}
	// The record-view table keys on the presented token: UI exports carry
	// only the export event (§4).
	var n int
	if err := f.e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_record_views`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit_record_views rows = %d (err %v), want 0 for the UI surface", n, err)
	}
}

func TestExportJSONFormat(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")

	rec := f.e.do(http.MethodGet, f.path("format=JSON"), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var out []map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(out) != 2 || out[0]["record_id"] != "REC-001" || out[0]["redcap_event_name"] != "baseline_arm_1" {
		t.Fatalf("JSON = %s, want the two flat rows", rec.Body.String())
	}
	if out[0]["notes"] != "=SUM(1)" { // neutralisation is CSV-only (§3.3)
		t.Errorf("notes = %q, JSON keeps the raw value", out[0]["notes"])
	}
}

func TestExportArmLevelsLowestWins(t *testing.T) {
	f := newExportFixture(t)
	roleID := f.role(t, "mixed",
		db.RoleArm{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_no_identifiers"},
		db.RoleArm{ArmNum: 2, DataAccessLevel: "read_only", ExportLevel: "export_de_identified"})
	member := f.member(t, "member@example.org", roleID)

	// No arm parameter: every exportable arm is a candidate and the applied
	// level is the lowest among them (D-4) — de_identified. record_id is a
	// direct identifier (gone below full), notes is unapproved free text
	// (gone at de_identified), secret is hashed; both arms' records appear,
	// each transformed at the applied minimum.
	rec := f.e.do(http.MethodGet, f.path(""), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("default export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	if headerHas(rows[0], "record_id") || headerHas(rows[0], "notes") {
		t.Errorf("header = %v, want record_id and notes removed at de_identified", rows[0])
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want both arms' records at the applied minimum", rows)
	}
	secretIdx := 0
	for i, h := range rows[0] {
		if h == "secret" {
			secretIdx = i
		}
	}
	if v := rows[1][secretIdx]; v != "very private" && !isHash(v) {
		t.Errorf("secret = %q, want the de-identified hash", v)
	}
	if _, d := f.lastExportAudit(t); d["sensitivity"] != "export_de_identified" {
		t.Errorf("audit sensitivity = %v, want export_de_identified (the applied minimum)", d["sensitivity"])
	}

	// arm=1 alone: no_identifiers — free text survives, direct identifiers
	// still removed, values untransformed.
	rec = f.e.do(http.MethodGet, f.path("arm=1"), nil, member)
	rows = csvRows(t, rec.Body.String())
	if headerHas(rows[0], "record_id") {
		t.Errorf("header = %v, want record_id removed at no_identifiers", rows[0])
	}
	if !headerHas(rows[0], "notes") {
		t.Errorf("header = %v, want notes present at no_identifiers", rows[0])
	}
	if len(rows) != 2 || strings.Contains(rec.Body.String(), "visit_arm_2") {
		t.Fatalf("rows = %v, want only arm 1's record", rows)
	}
	for i, h := range rows[0] {
		if h == "secret" && rows[1][i] != "very private" {
			t.Errorf("secret = %q, want the raw value at no_identifiers", rows[1][i])
		}
	}
	if _, d := f.lastExportAudit(t); d["sensitivity"] != "export_no_identifiers" {
		t.Errorf("audit sensitivity = %v, want export_no_identifiers", d["sensitivity"])
	}
}

func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range strings.ToUpper(s) {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func TestExportArmValidation(t *testing.T) {
	f := newExportFixture(t)
	roleID := f.role(t, "arm1-only",
		db.RoleArm{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_full"})
	member := f.member(t, "member@example.org", roleID)
	outsider := f.e.mustUser("outsider@example.org")

	rec := f.e.do(http.MethodGet, f.path("arm=99"), nil, member)
	if rec.Code != http.StatusNotFound || decodeError(t, rec) != "not_found" {
		t.Errorf("unknown arm = %d %s, want 404 not_found", rec.Code, rec.Body.String())
	}
	rec = f.e.do(http.MethodGet, f.path("arm=2"), nil, member) // export_none on arm 2
	if rec.Code != http.StatusForbidden || decodeError(t, rec) != "forbidden" {
		t.Errorf("unexportable arm = %d %s, want 403 forbidden", rec.Code, rec.Body.String())
	}
	rec = f.e.do(http.MethodGet, f.path(""), nil, outsider) // not a member
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-member = %d, want 403", rec.Code)
	}
	// None of the rejections may leave an audit trail (REQ-AUD-004).
	var n int
	if err := f.e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = 'export'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("export audit rows = %d (err %v), want 0", n, err)
	}

	rec = f.e.do(http.MethodGet, f.path("arm=abc"), nil, member)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric arm = %d, want 400", rec.Code)
	}
	rec = f.e.do(http.MethodGet, f.path("format=xml"), nil, member)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad format = %d, want 400", rec.Code)
	}
	rec = f.e.do(http.MethodGet, f.path("csvDelimiter=ab"), nil, member)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("multi-char delimiter = %d, want 400", rec.Code)
	}
	// Unknown parameters are accepted and ignored (REQ-API-017).
	rec = f.e.do(http.MethodGet, f.path("bogus=1"), nil, member)
	if rec.Code != http.StatusOK {
		t.Errorf("unknown parameter = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestExportDAGScope(t *testing.T) {
	f := newExportFixture(t)
	member := f.e.mustUser("center-a@example.org")
	a := &db.Assignment{UserID: member.ID, ProjectID: f.pid, Token: "tok-dag"}
	asgID, err := f.e.Store.AddAssignment(context.Background(), a)
	if err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	ctx := context.Background()
	if _, err := f.e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: asgID, GroupID: f.groupA, IsActive: true}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}

	rec := f.e.do(http.MethodGet, f.path(""), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	if len(rows) != 2 || rows[1][0] != "REC-001" {
		t.Errorf("rows = %v, want only the active group's record (REQ-API-092)", rows)
	}
}

func TestExportLabelsAndHeaders(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")

	rec := f.e.do(http.MethodGet, f.path("rawOrLabel=label&rawOrLabelHeaders=both"), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	// rawOrLabelHeaders=both renders "<label> (field_name)" where a label
	// exists and the bare name otherwise; rawOrLabel=label renders the
	// choice label for code 2.
	if !headerHas(rows[0], "Age (age)") {
		t.Fatalf("header = %v, want the combined Age (age) column", rows[0])
	}
	if !headerHas(rows[0], "status") {
		t.Fatalf("header = %v, want the status column", rows[0])
	}
	statusIdx := -1
	for i, h := range rows[0] {
		if h == "status" {
			statusIdx = i
		}
	}
	if rows[1][statusIdx] != "Done" { // code 2 of "1$Pending##2$Done"
		t.Errorf("status = %q, want the label Done", rows[1][statusIdx])
	}
}

func headerHas(h []string, col string) bool {
	for _, x := range h {
		if x == col {
			return true
		}
	}
	return false
}
