package admin

// The UI-surface export of §4.14 (REQ-API-075/076): arm scope resolution and the
// per-(instrument, event) sensitivity of §4.3 that replaces the old lowest-wins
// rule, arm validation, DAG scoping, the label axes, and the audit event with
// surface "ui". The rendering pipeline itself is covered by the data API tests;
// these exercise the administration boundary around it.

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
	// The two events' ids: pair grants address (event, instrument) (REQ-DB-040).
	evBaseline int64
	evVisit    int64
	demoID     int64 // re-stated when addInstrument rewrites the arm's mapping
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

	return &exportFixture{e: e, pid: pid, groupA: groupA, groupB: groupB,
		evBaseline: ev1, evVisit: ev2, demoID: demoID}
}

// addInstrument maps a second instrument onto baseline_arm_1 with one free-text
// field and one personal-information field, and returns its id. The mapping call
// replaces the arm's whole list, so demo's own mapping is re-stated with it.
func (f *exportFixture) addInstrument(t *testing.T, name string, position int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := f.e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: f.pid, Name: name, Position: position})
	if err != nil {
		t.Fatalf("AddInstrument %s: %v", name, err)
	}
	for _, fld := range []db.Field{
		{FieldName: "b_note", FieldType: "text"}, // free text, not export-approved
		{FieldName: "b_secret", FieldType: "text", PersonalInformation: true},
	} {
		fld.ProjectID, fld.InstrumentID = f.pid, id
		if _, err := f.e.Store.AddField(ctx, &fld); err != nil {
			t.Fatalf("AddField %s: %v", fld.FieldName, err)
		}
	}
	if err := f.e.Store.SetInstrumentEventsForArm(ctx, f.pid, f.armOf(t, 1), []db.InstrumentEvent{
		{InstrumentID: f.demoID, EventID: f.evBaseline},
		{InstrumentID: id, EventID: f.evBaseline},
	}); err != nil {
		t.Fatalf("map %s: %v", name, err)
	}
	return id
}

// armOf reads the arm id behind an arm number.
func (f *exportFixture) armOf(t *testing.T, armNum int) int64 {
	t.Helper()
	var id int64
	if err := f.e.Store.DB.QueryRow(
		`SELECT id FROM arms WHERE project_id = ? AND arm_num = ?`, f.pid, armNum).Scan(&id); err != nil {
		t.Fatalf("arm %d: %v", armNum, err)
	}
	return id
}

// seedValues stores values on an existing record at one event — the per-instrument
// fields a pair-grant test needs beside the fixture's own seeded row.
func (f *exportFixture) seedValues(t *testing.T, recordID, event string, values map[string]string) {
	t.Helper()
	ctx := context.Background()
	for field, value := range values {
		if err := f.e.Store.UpsertDataValue(ctx, &db.DataValue{
			ProjectID: f.pid, RecordID: recordID, UniqueEventName: event,
			FieldName: field, Value: value,
		}); err != nil {
			t.Fatalf("seed %s.%s: %v", event, field, err)
		}
	}
}

// roleWithGrants creates a role with arm defaults and per-(instrument, event)
// overrides — the shape REQ-AUTH-069 holds the permission at.
func (f *exportFixture) roleWithGrants(t *testing.T, name string, arms []db.RoleArm, grants []db.RoleGrant) int64 {
	t.Helper()
	id, err := f.e.Store.CreateRole(context.Background(), &db.Role{ProjectID: f.pid, RoleName: name}, arms, grants)
	if err != nil {
		t.Fatalf("CreateRole %s: %v", name, err)
	}
	return id
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
	// No repeating instrument, so no repeat columns (REQ-API-155).
	wantHeader := []string{"record_id", "redcap_event_name", "age", "status", "notes", "contact_email",
		"secret", "visit_date", "demo_complete"} // REQ-API-134: whole-instrument export ends the block with completion
	if len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("CSV = %v, want header %v and two data rows", rows, wantHeader)
	}
	if rows[1][8] != "1" { // values present, no finished assignment (REQ-API-074 derivation)
		t.Errorf("demo_complete = %q, want 1", rows[1][8])
	}
	if rows[1][0] != "REC-001" || rows[1][1] != "baseline_arm_1" {
		t.Errorf("row 1 = %v, want REC-001 at baseline_arm_1", rows[1])
	}
	if rows[2][0] != "REC-002" || rows[2][1] != "visit_arm_2" {
		t.Errorf("row 2 = %v, want REC-002 at visit_arm_2", rows[2])
	}
	if rows[1][4] != "'=SUM(1)" { // formula neutralisation, CSV only (REQ-VAL-032)
		t.Errorf("notes cell = %q, want the neutralised '=SUM(1)", rows[1][4])
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

// TestExportArmScopeCarriesPerPairLevels is the arm-scope half of the per-pair
// model (Data_Export_Design.md §4.3, D-4 revised): `arm=` selects scope, and each
// pair inside it renders at its own level. The union of columns appears — one file
// holding arm 1's untransformed free text beside arm 2's hashed value is the normal
// case — and the audit records the most protective level among the pairs delivered.
func TestExportArmScopeCarriesPerPairLevels(t *testing.T) {
	f := newExportFixture(t)
	roleID := f.role(t, "mixed",
		db.RoleArm{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_no_identifiers"},
		db.RoleArm{ArmNum: 2, DataAccessLevel: "read_only", ExportLevel: "export_de_identified"})
	member := f.member(t, "member@example.org", roleID)

	// No arm parameter: both arms are in scope. record_id belongs to no pair and
	// follows the floor (de_identified), so it is gone from the file; notes is
	// unapproved free text, held at de_identified strength on arm 2 only — so the
	// column stays (arm 1 delivers it) and arm 2's cell is empty.
	rec := f.e.do(http.MethodGet, f.path(""), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("default export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	if headerHas(rows[0], "record_id") {
		t.Errorf("header = %v, want record_id gone: the floor is de_identified", rows[0])
	}
	if !headerHas(rows[0], "notes") {
		t.Fatalf("header = %v, want notes present for the pair that permits it", rows[0])
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want both arms' records", rows)
	}
	at := func(header string) int {
		for i, h := range rows[0] {
			if h == header {
				return i
			}
		}
		t.Fatalf("no %q column in %v", header, rows[0])
		return -1
	}
	// Row 1 is REC-001 at baseline_arm_1 (no_identifiers), row 2 REC-002 at
	// visit_arm_2 (de_identified): one column, two treatments.
	if v := rows[1][at("secret")]; v != "very private" {
		t.Errorf("arm 1 secret = %q, want the raw value at no_identifiers", v)
	}
	if v := rows[2][at("secret")]; !isHash(v) {
		t.Errorf("arm 2 secret = %q, want the de-identified hash", v)
	}
	if v := rows[1][at("notes")]; v != "'=SUM(1)" {
		t.Errorf("arm 1 notes = %q, want the value its pair permits (neutralised for CSV)", v)
	}
	if v := rows[2][at("notes")]; v != "" {
		t.Errorf("arm 2 notes = %q, want an empty cell where the pair forbids the column", v)
	}
	if v := rows[1][at("visit_date")]; v != "2026-05-04" {
		t.Errorf("arm 1 visit_date = %q, want it unshifted at no_identifiers", v)
	}
	if v := rows[2][at("visit_date")]; v == "2026-05-04" || v == "" {
		t.Errorf("arm 2 visit_date = %q, want the shifted date", v)
	}
	if _, d := f.lastExportAudit(t); d["sensitivity"] != "export_de_identified" {
		t.Errorf("audit sensitivity = %v, want export_de_identified (the most protective delivered)", d["sensitivity"])
	}

	// arm=1 alone: one pair in scope, so the whole file reads at no_identifiers —
	// free text survives and no value is transformed.
	rec = f.e.do(http.MethodGet, f.path("arm=1"), nil, member)
	rows = csvRows(t, rec.Body.String())
	if headerHas(rows[0], "record_id") {
		t.Errorf("header = %v, want record_id removed at no_identifiers", rows[0])
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

// TestExportPairGrantsSideBySide is the headline case of §4.3: two instruments at
// one event, held at different levels by pair grants (REQ-AUTH-069), delivered in
// one file — each column transformed at its own pair's level.
func TestExportPairGrantsSideBySide(t *testing.T) {
	f := newExportFixture(t)
	otherID := f.addInstrument(t, "followup", 2)
	f.seedValues(t, "REC-001", "baseline_arm_1", map[string]string{
		"b_note":   "free text",
		"b_secret": "very private",
	})
	roleID := f.roleWithGrants(t, "pairwise",
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_full"}},
		[]db.RoleGrant{{EventID: f.evBaseline, InstrumentID: otherID,
			DataAccessLevel: "view_edit", ExportLevel: "export_de_identified"}})
	member := f.member(t, "member@example.org", roleID)

	rec := f.e.do(http.MethodGet, f.path("arm=1"), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want REC-001's one row", rows)
	}
	at := func(header string) int {
		for i, h := range rows[0] {
			if h == header {
				return i
			}
		}
		t.Fatalf("no %q column in %v", header, rows[0])
		return -1
	}
	// The instrument held in full: raw values, unapproved free text included.
	if v := rows[1][at("secret")]; v != "very private" {
		t.Errorf("demo.secret = %q, want it raw at export_full", v)
	}
	if v := rows[1][at("notes")]; v == "" {
		t.Error("demo.notes empty, want its free text at export_full")
	}
	// The de-identified instrument beside it: personal value hashed, unapproved
	// free text absent from the file — its only pair forbids it everywhere.
	if v := rows[1][at("b_secret")]; !isHash(v) {
		t.Errorf("followup.b_secret = %q, want the hash its own pair applies", v)
	}
	if headerHas(rows[0], "b_note") {
		t.Errorf("header = %v, want b_note absent: its only pair is de_identified", rows[0])
	}
	// And the record identifier, belonging to no pair, follows the floor — the
	// stricter of the two instruments in scope.
	if headerHas(rows[0], "record_id") {
		t.Errorf("header = %v, want record_id gone while any pair is below full", rows[0])
	}
	if _, d := f.lastExportAudit(t); d["sensitivity"] != "export_de_identified" {
		t.Errorf("audit sensitivity = %v, want export_de_identified (§4.3)", d["sensitivity"])
	}
}

// TestExportDroppedPairKeepsTheRest is the other half of §4.3: a pair at
// export_none contributes no columns and the request proceeds — it is counted in
// the audit event, and it lowers no level, because nothing came from it.
func TestExportDroppedPairKeepsTheRest(t *testing.T) {
	f := newExportFixture(t)
	otherID := f.addInstrument(t, "followup", 2)
	f.seedValues(t, "REC-001", "baseline_arm_1", map[string]string{"b_note": "free text"})
	roleID := f.roleWithGrants(t, "one-pair-none",
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_full"}},
		[]db.RoleGrant{{EventID: f.evBaseline, InstrumentID: otherID,
			DataAccessLevel: "view_edit", ExportLevel: "export_none"}})
	member := f.member(t, "member@example.org", roleID)

	rec := f.e.do(http.MethodGet, f.path("arm=1"), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200: a pair at export_none drops, it does not refuse",
			rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	for _, h := range rows[0] {
		if h == "b_note" || h == "b_secret" {
			t.Errorf("header carries %q, want the dropped pair absent", h)
		}
	}
	if !headerHas(rows[0], "record_id") {
		t.Errorf("header = %v, want record_id: every delivered pair is export_full", rows[0])
	}
	_, d := f.lastExportAudit(t)
	if d["sensitivity"] != "export_full" {
		t.Errorf("audit sensitivity = %v, want export_full — a dropped pair lowers no level", d["sensitivity"])
	}
	if n, _ := d["pairs_dropped"].(float64); n != 1 {
		t.Errorf("audit pairs_dropped = %v, want 1 (§4.3)", d["pairs_dropped"])
	}
}

// TestExportCompletionStateFollowsPairs reads REQ-API-134 through §4.3: the
// <instrument>_complete state belongs to the instrument's pair like its values do.
// A flat row that survives on a permitted pair at its event still leaves the refused
// pair's completion cell empty — whether that instrument was completed there is not
// information the refusal leaves in the file.
func TestExportCompletionStateFollowsPairs(t *testing.T) {
	f := newExportFixture(t)
	otherID := f.addInstrument(t, "followup", 2) // maps onto baseline beside demo
	// Map both instruments onto arm 2's visit event too, so one event can hold a
	// permitted and a refused pair at once.
	ctx := context.Background()
	if err := f.e.Store.SetInstrumentEventsForArm(ctx, f.pid, f.armOf(t, 2), []db.InstrumentEvent{
		{InstrumentID: f.demoID, EventID: f.evVisit},
		{InstrumentID: otherID, EventID: f.evVisit},
	}); err != nil {
		t.Fatalf("map onto visit: %v", err)
	}
	f.seedValues(t, "REC-002", "visit_arm_2", map[string]string{"b_note": "kept"})
	roleID := f.roleWithGrants(t, "demo-not-at-visit",
		[]db.RoleArm{
			{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_full"},
			{ArmNum: 2, DataAccessLevel: "read_only", ExportLevel: "export_full"},
		},
		[]db.RoleGrant{{EventID: f.evVisit, InstrumentID: f.demoID,
			DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	member := f.member(t, "member@example.org", roleID)

	rec := f.e.do(http.MethodGet, f.path(""), nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rows := csvRows(t, rec.Body.String())
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want both records: visit_arm_2 delivers through followup's pair", rows)
	}
	completion, seen := -1, 0
	for i, h := range rows[0] {
		if h == "demo_complete" {
			completion, seen = i, seen+1
		}
	}
	if seen != 1 {
		t.Fatalf("header %v carries %d demo_complete columns, want exactly one (flat)", rows[0], seen)
	}
	// REC-001 at baseline_arm_1: the pair permits, so the state is derived as ever —
	// values without a finished mark.
	if v := rows[1][completion]; v != "1" {
		t.Errorf("REC-001 demo_complete = %q, want 1", v)
	}
	// REC-002 at visit_arm_2: the row stands on followup's permitted pair; demo's
	// refused pair contributes nothing, its completion state included.
	if rows[2][0] != "REC-002" {
		t.Fatalf("row 2 = %v, want REC-002's", rows[2])
	}
	if v := rows[2][completion]; v != "" {
		t.Errorf("REC-002 demo_complete = %q, want empty: the refused pair discloses no completion state", v)
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
	// The CSV options take REDCap's value sets and nothing else (REQ-EXP-013/015):
	// a longer value is never cut down to its first character, and a double quote
	// cannot delimit a CSV field at all.
	for _, q := range []string{"csvDelimiter=ab", "csvDelimiter=x", "csvDelimiter=%22",
		"csvDelimiter=%0A", "decimalCharacter=x", "decimalCharacter=%2C%2C"} {
		rec = f.e.do(http.MethodGet, f.path(q), nil, member)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec) != "invalid_request" {
			t.Errorf("%s = %d %s, want 400 invalid_request", q, rec.Code, rec.Body.String())
		}
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

// exportNumbers adds the fields the CSV options act on to the fixture's demo
// instrument and fills them for REC-001: a floating-point and a calculated value
// (decimalCharacter rewrites both), a text value that only looks like a number
// (it does not), a personal floating-point value (hashed at de-identified
// strength from its stored form), and a label beginning with "=".
func (f *exportFixture) exportNumbers(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for i, fld := range []db.Field{
		{FieldName: "weight", FieldType: "text",
			ValidationType: sql.NullString{String: "floating point", Valid: true}},
		{FieldName: "bmi", FieldType: "calculated",
			Calculation: sql.NullString{String: "[weight]", Valid: true}},
		{FieldName: "remark", FieldType: "text", ExportApproved: true},
		{FieldName: "p_weight", FieldType: "text", PersonalInformation: true,
			ValidationType: sql.NullString{String: "floating point", Valid: true}},
		{FieldName: "trap", FieldType: "text", ExportApproved: true,
			FieldLabel: sql.NullString{String: "=HYPERLINK(1)", Valid: true}},
	} {
		fld.ProjectID, fld.InstrumentID, fld.Position = f.pid, f.demoID, 10+i
		if _, err := f.e.Store.AddField(ctx, &fld); err != nil {
			t.Fatalf("AddField %s: %v", fld.FieldName, err)
		}
	}
	f.seedValues(t, "REC-001", "baseline_arm_1", map[string]string{
		"weight": "-1.5", "bmi": "23.25", "remark": `1.5 says "hi", ok`, "p_weight": "70.5", "trap": "x",
	})
}

// cellOf reads one named column of the first data row.
func cellOf(t *testing.T, rows [][]string, col string) string {
	t.Helper()
	for i, h := range rows[0] {
		if h == col {
			return rows[1][i]
		}
	}
	t.Fatalf("header = %v, want a %s column", rows[0], col)
	return ""
}

// TestExportCSVOptions covers the spreadsheet options of REQ-EXP-013/015: REDCap's
// delimiter set (tab by name included), standard quoting under any delimiter, the
// decimal character on calculated and floating-point values in either encoding, a
// converted negative number left unprefixed, and neutralized header cells.
func TestExportCSVOptions(t *testing.T) {
	f := newExportFixture(t)
	f.exportNumbers(t)
	admin := f.e.mustAdmin("admin@example.org")

	get := func(query string) string {
		t.Helper()
		rec := f.e.do(http.MethodGet, f.path(query), nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s, want 200", query, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	parse := func(body string, comma rune) [][]string {
		t.Helper()
		r := csv.NewReader(strings.NewReader(body))
		r.Comma = comma
		rows, err := r.ReadAll()
		if err != nil {
			t.Fatalf("parse %q: %v", body, err)
		}
		return rows
	}

	// Semicolon and decimal comma — the nb/nn spreadsheet pair.
	body := get("arm=1&csvDelimiter=%3B&decimalCharacter=%2C")
	rows := parse(body, ';')
	for col, want := range map[string]string{
		"weight": "-1,5",  // converted, and still a number: no formula prefix (REQ-VAL-032)
		"bmi":    "23,25", // calculated values convert too
		"age":    "41",    // an integer has no separator
		"remark": `1.5 says "hi", ok`,
	} {
		if got := cellOf(t, rows, col); got != want {
			t.Errorf("%s = %q, want %q", col, got, want)
		}
	}
	// Quoting is the standard one under any delimiter: the semicolon file leaves a
	// comma unquoted but still quotes the double quotes, doubled.
	if !strings.Contains(body, `;-1,5;23,25;"1.5 says ""hi"", ok";`) {
		t.Errorf("body = %q, want the raw cells -1,5;23,25 and the quoted remark", body)
	}

	// The same conversion under a comma delimiter quotes the converted cell.
	body = get("arm=1&decimalCharacter=%2C")
	if !strings.Contains(body, `,"-1,5","23,25",`) {
		t.Errorf("body = %q, want the comma-decimal cells quoted", body)
	}
	if rows = parse(body, ','); cellOf(t, rows, "bmi") != "23,25" {
		t.Errorf("bmi = %q, want 23,25", cellOf(t, rows, "bmi"))
	}

	// A period, or no option at all, is the stored form.
	for _, q := range []string{"arm=1", "arm=1&decimalCharacter=.", "arm=1&decimalCharacter="} {
		if got := cellOf(t, parse(get(q), ','), "weight"); got != "-1.5" {
			t.Errorf("%s: weight = %q, want the stored -1.5", q, got)
		}
	}

	// Tab, by REDCap's name and as the character.
	for _, q := range []string{"arm=1&csvDelimiter=tab", "arm=1&csvDelimiter=%09"} {
		if got := cellOf(t, parse(get(q), '\t'), "bmi"); got != "23.25" {
			t.Errorf("%s: bmi = %q, want 23.25 in a tab-separated file", q, got)
		}
	}

	// The decimal character is not CSV-only (REDCap applies it to the values).
	var out []map[string]string
	if err := json.Unmarshal([]byte(get("arm=1&format=json&decimalCharacter=%2C")), &out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(out) != 1 || out[0]["bmi"] != "23,25" || out[0]["weight"] != "-1,5" {
		t.Errorf("JSON = %v, want bmi 23,25 and weight -1,5", out)
	}

	// Header cells are neutralized like values (REQ-EXP-013): a label header is
	// the designer's text.
	rows = parse(get("arm=1&rawOrLabelHeaders=label"), ',')
	if !headerHas(rows[0], "'=HYPERLINK(1)") || headerHas(rows[0], "=HYPERLINK(1)") {
		t.Errorf("header = %v, want the label neutralized to '=HYPERLINK(1)", rows[0])
	}
}

// The personal-field hash is computed from the stored value, so the decimal
// character never changes it (REQ-EXP-015, REQ-EXP-030): two exports of the same
// record link up whichever separator each one asked for.
func TestExportDecimalLeavesHashesAlone(t *testing.T) {
	f := newExportFixture(t)
	f.exportNumbers(t)
	roleID := f.role(t, "deid",
		db.RoleArm{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_de_identified"})
	member := f.member(t, "member@example.org", roleID)

	hashed := func(query string) string {
		t.Helper()
		rec := f.e.do(http.MethodGet, f.path(query), nil, member)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s, want 200", query, rec.Code, rec.Body.String())
		}
		return cellOf(t, csvRows(t, rec.Body.String()), "p_weight")
	}
	stored, comma := hashed("arm=1"), hashed("arm=1&decimalCharacter=%2C")
	if stored == "70.5" || stored == "" {
		t.Fatalf("p_weight = %q, want a hash at de-identified strength", stored)
	}
	if comma != stored {
		t.Errorf("p_weight hash = %q with a decimal comma, %q without: the option fed the hash", comma, stored)
	}
}
