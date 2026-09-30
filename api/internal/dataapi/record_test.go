package dataapi

// Tests for content=record&action=export|import|delete (§3.6–§3.8): the
// sensitivity pipeline, filterLogic, the import response codes and
// validation, delete scoping, DAG visibility, and the audit side effects.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

type recordFixture struct {
	h   *Handler
	s   *db.Store
	cfg *config.Config
	pid int64
}

// newRecordFixture seeds a two-event project whose "demo" instrument carries
// one field of every sensitivity category plus a calculated field on a second
// instrument, and hands out one token per permission profile.
func newRecordFixture(t *testing.T) *recordFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		AppEnv:           "development",
		DBConnection:     "sqlite",
		DBDatabase:       filepath.Join(dir, "test.sqlite"),
		AnonSalt:         "test-salt",
		AnonDateShiftMin: 1,
		AnonDateShiftMax: 365,
		AppTimezone:      "UTC",
	}
	s, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	pid, err := s.CreateProject(ctx, &db.Project{ProjectName: "rec-" + t.Name(), ParticipantNames: "8DISC[0-9][0-9][0-9]"})
	must(err)
	armID, err := s.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	must(err)
	evBase, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1"})
	must(err)
	evFol, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "followup", UniqueEventName: "followup_arm_1"})
	must(err)

	demoID, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "demo", Position: 1})
	must(err)
	calcID, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "calc", Position: 2})
	must(err)

	add := func(instrID int64, f db.Field) {
		t.Helper()
		f.ProjectID, f.InstrumentID = pid, instrID
		if _, err := s.AddField(ctx, &f); err != nil {
			t.Fatalf("seed AddField %s: %v", f.FieldName, err)
		}
	}
	add(demoID, db.Field{FieldName: "record_id", FieldType: "text", Position: 1})
	add(demoID, db.Field{FieldName: "age", FieldType: "text", ValidationType: sql.NullString{String: "integer", Valid: true}, Position: 2})
	add(demoID, db.Field{FieldName: "status", FieldType: "dropdown", Choices: sql.NullString{String: "1$Pending##2$Done", Valid: true}, Position: 3})
	add(demoID, db.Field{FieldName: "notes", FieldType: "text", Position: 4}) // free text, not export-approved
	add(demoID, db.Field{FieldName: "contact_email", FieldType: "text", ValidationType: sql.NullString{String: "email", Valid: true}, Position: 5})
	add(demoID, db.Field{FieldName: "visit_date", FieldType: "text", ValidationType: sql.NullString{String: "date", Valid: true}, ExportApproved: true, Position: 6})
	add(demoID, db.Field{FieldName: "secret", FieldType: "text", PersonalInformation: true, Position: 7})
	add(calcID, db.Field{FieldName: "total", FieldType: "calculated", Calculation: sql.NullString{String: "[baseline_arm_1][age] * 2", Valid: true}, Position: 1})

	must(s.SetInstrumentEventsForArm(ctx, pid, armID, []db.InstrumentEvent{
		{InstrumentID: demoID, EventID: evBase},
		{InstrumentID: demoID, EventID: evFol},
		{InstrumentID: calcID, EventID: evBase},
	}))

	mkUser := func(email string, admin bool) int64 {
		id, err := s.CreateUser(ctx, &db.User{Email: email, DisplayName: email, Enabled: true, AuthSource: "local", IsAdmin: admin})
		if err != nil {
			t.Fatalf("seed CreateUser %s: %v", email, err)
		}
		return id
	}
	mkToken := func(userID int64, token string, roleID int64) int64 {
		a := &db.Assignment{UserID: userID, ProjectID: pid, Token: token}
		if roleID > 0 {
			a.RoleID = sql.NullInt64{Int64: roleID, Valid: true}
		}
		id, err := s.AddAssignment(ctx, a)
		if err != nil {
			t.Fatalf("seed AddAssignment %s: %v", token, err)
		}
		return id
	}
	mkRole := func(name, data, export string) int64 {
		id, err := s.CreateRole(ctx, &db.Role{ProjectID: pid, RoleName: name},
			[]db.RoleArm{{ArmNum: 1, DataAccessLevel: data, ExportLevel: export}})
		if err != nil {
			t.Fatalf("seed CreateRole %s: %v", name, err)
		}
		return id
	}

	mkToken(mkUser("admin@example.org", true), "tok-admin", 0)
	mkToken(mkUser("de@example.org", false), "tok-de", mkRole("reader-de", "read_only", "export_de_identified"))
	mkToken(mkUser("none@example.org", false), "tok-none", mkRole("reader-none", "read_only", "export_none"))
	editorAssignment := mkToken(mkUser("edit@example.org", false), "tok-edit", mkRole("editor", "view_edit", "export_full"))
	mkToken(mkUser("del@example.org", false), "tok-del", mkRole("deleter", "delete", "export_full"))
	_ = editorAssignment

	dv := func(record, event, field, value string) {
		must(s.AddDataValue(ctx, &db.DataValue{ProjectID: pid, RecordID: record,
			UniqueEventName: event, FieldName: field, Value: value}))
	}
	dv("8DISC001", "baseline_arm_1", "record_id", "8DISC001")
	dv("8DISC001", "baseline_arm_1", "age", "42")
	dv("8DISC001", "baseline_arm_1", "status", "2")
	dv("8DISC001", "baseline_arm_1", "notes", "call me")
	dv("8DISC001", "baseline_arm_1", "contact_email", "participant@example.org")
	dv("8DISC001", "baseline_arm_1", "visit_date", "2026-03-01+01:00")
	dv("8DISC001", "baseline_arm_1", "secret", "top secret")
	dv("8DISC001", "followup_arm_1", "record_id", "8DISC001")
	dv("8DISC001", "followup_arm_1", "age", "43")
	dv("8DISC002", "baseline_arm_1", "record_id", "8DISC002")
	dv("8DISC002", "baseline_arm_1", "age", "30")

	aw := audit.NewWriter(s.DB, string(s.Dialect))
	must(aw.EnsureYear(ctx)) // audit tables exist before the first query in tests

	return &recordFixture{
		h: &Handler{Store: s, Cfg: cfg, Audit: aw},
		s: s, cfg: cfg, pid: pid,
	}
}

// call posts a data-API form and returns status + body.
func (f *recordFixture) call(t *testing.T, form url.Values) (int, string) {
	t.Helper()
	return do(t, f.h, form)
}

func (f *recordFixture) exportJSON(t *testing.T, token string, extra url.Values) []map[string]string {
	t.Helper()
	form := url.Values{"token": {token}, "content": {"record"}, "returnFormat": {"json"}}
	for k, vs := range extra {
		form[k] = vs
	}
	code, body := f.call(t, form)
	mustStatus(t, code, 200, body)
	var out []map[string]string
	if err := decodeInto(body, &out); err != nil {
		t.Fatalf("bad export json: %v / %s", err, body)
	}
	return out
}

func auditCount(t *testing.T, s *db.Store, eventType string) int {
	t.Helper()
	table := "audit_events_" + strconv.Itoa(time.Now().UTC().Year())
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE event_type = ?`, eventType).Scan(&n)
	if err != nil {
		t.Fatalf("count %s: %v", eventType, err)
	}
	return n
}

func viewCount(t *testing.T, s *db.Store) int {
	t.Helper()
	table := "audit_record_views_" + strconv.Itoa(time.Now().UTC().Year())
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
	if err != nil {
		t.Fatalf("count record views: %v", err)
	}
	return n
}

func TestRecordExportFlatFull(t *testing.T) {
	f := newRecordFixture(t)
	rows := f.exportJSON(t, "tok-admin", nil)
	if len(rows) != 3 { // 8DISC001 × two events, 8DISC002 × one
		t.Fatalf("rows = %d, want 3: %v", len(rows), rows)
	}
	base := rows[0]
	if base["record_id"] != "8DISC001" || base["redcap_event_name"] != "baseline_arm_1" {
		t.Errorf("first row = %v", base)
	}
	if base["age"] != "42" || base["status"] != "2" || base["notes"] != "call me" {
		t.Errorf("full export lost values: %v", base)
	}
	if base["contact_email"] != "participant@example.org" || base["secret"] != "top secret" {
		t.Errorf("full export must keep identifiers at export_full: %v", base)
	}
	if base["visit_date"] != "2026-03-01+01:00" {
		t.Errorf("full export must not shift dates: %v", base)
	}
	if rows[1]["redcap_event_name"] != "followup_arm_1" || rows[1]["age"] != "43" {
		t.Errorf("followup row = %v", rows[1])
	}
	if rows[2]["record_id"] != "8DISC002" {
		t.Errorf("second record row = %v", rows[2])
	}
}

func TestRecordExportCSVHeaderAndLabels(t *testing.T) {
	f := newRecordFixture(t)
	code, body := f.call(t, url.Values{
		"token": {"tok-admin"}, "content": {"record"},
		"rawOrLabel": {"label"}, "records": {"8DISC001"}, "events": {"baseline_arm_1"},
	})
	mustStatus(t, code, 200, body)
	lines := strings.Split(strings.TrimRight(body, "\r\n"), "\n")
	wantHeader := "record_id,redcap_event_name,redcap_repeat_instrument,redcap_repeat_instance," +
		"age,status,notes,contact_email,visit_date,secret,total"
	if lines[0] != wantHeader {
		t.Errorf("header =\n%q\nwant\n%q", lines[0], wantHeader)
	}
	if len(lines) != 2 {
		t.Fatalf("csv rows = %d, want header + 1 data row: %q", len(lines), body)
	}
	if !strings.Contains(lines[1], ",Done,") {
		t.Errorf("rawOrLabel=label must map status 2 to Done: %q", lines[1])
	}
}

func TestRecordExportWide(t *testing.T) {
	f := newRecordFixture(t)
	code, body := f.call(t, url.Values{
		"token": {"tok-admin"}, "content": {"record"}, "returnFormat": {"json"},
		"type": {"wide"}, "records": {"8DISC001"},
	})
	mustStatus(t, code, 200, body)
	var rows []map[string]string
	if err := decodeInto(body, &rows); err != nil {
		t.Fatalf("bad wide json: %v / %s", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("wide export is one row per record, got %v", rows)
	}
	r := rows[0]
	// demo maps to both events → age repeats; calc maps to baseline only → bare name.
	if r["age_baseline_arm_1"] != "42" || r["age_followup_arm_1"] != "43" {
		t.Errorf("wide event-suffixed slots = %v", r)
	}
	if _, ok := r["age"]; ok {
		t.Errorf("multi-event field must not also appear bare: %v", r)
	}
	if r["total"] != "" && r["total"] != "84" {
		t.Errorf("single-event slot = %q, want empty or recomputed value", r["total"])
	}
}

func TestRecordExportDeIdentified(t *testing.T) {
	f := newRecordFixture(t)
	rows := f.exportJSON(t, "tok-de", url.Values{"records": {"8DISC001"}, "events": {"baseline_arm_1"}})
	if len(rows) == 0 {
		t.Fatal("de-identified export returned no rows")
	}
	r := rows[0]
	// record_id and contact_email are direct identifiers; notes is unapproved
	// free text; age is text too (D-3: free text = field_type = text, no
	// matter its validation type) — all absent at export_de_identified.
	for _, gone := range []string{"record_id", "contact_email", "notes", "age"} {
		if _, ok := r[gone]; ok {
			t.Errorf("%s must be absent at export_de_identified: %v", gone, r)
		}
	}
	wantHash := fieldHash(f.cfg.AnonSalt, f.pid, "secret", "top secret")
	if r["secret"] != wantHash {
		t.Errorf("secret = %q, want hash %q", r["secret"], wantHash)
	}
	if m, _ := regexp.MatchString(`^[0-9A-F]{64}$`, r["secret"]); !m {
		t.Errorf("personal hash is not 64 uppercase hex: %q", r["secret"])
	}
	if r["visit_date"] == "2026-03-01+01:00" || r["visit_date"] == "" {
		t.Errorf("approved date must be shifted, got %q", r["visit_date"])
	}
	if m, _ := regexp.MatchString(`^[0-9]{4}-[0-9]{2}-[0-9]{2}\+01:00$`, r["visit_date"]); !m {
		t.Errorf("shifted date lost its canonical form/offset: %q", r["visit_date"])
	}
	if r["status"] != "2" {
		t.Errorf("structured values must pass through: %v", r)
	}
	// The shift offset is persisted for reuse (§5.2).
	ao, err := f.s.GetAnonOffset(context.Background(), f.pid, "8DISC001")
	if err != nil || ao == nil {
		t.Fatalf("GetAnonOffset: %v / %+v", err, ao)
	}
	if ao.OffsetDays < 1 || ao.OffsetDays > 365 {
		t.Errorf("offset days = %d, want within [1,365]", ao.OffsetDays)
	}
}

func TestRecordExportNoneForbidden(t *testing.T) {
	f := newRecordFixture(t)
	before := auditCount(t, f.s, audit.Export)
	code, body := f.call(t, url.Values{"token": {"tok-none"}, "content": {"record"}})
	mustStatus(t, code, 403, body)
	if !strings.Contains(body, "Permission denied") {
		t.Errorf("body = %s", body)
	}
	// A rejected export writes no audit row (REQ-AUD-004).
	if got := auditCount(t, f.s, audit.Export); got != before {
		t.Errorf("forbidden export wrote %d audit events", got-before)
	}
}

func TestRecordExportFilterLogic(t *testing.T) {
	f := newRecordFixture(t)
	rows := f.exportJSON(t, "tok-admin", url.Values{"filterLogic": {"[baseline_arm_1][age] < 40"}})
	if len(rows) != 1 || rows[0]["record_id"] != "8DISC002" {
		t.Fatalf("filterLogic rows = %v, want only 8DISC002", rows)
	}
	rows = f.exportJSON(t, "tok-admin", url.Values{"filterLogic": {"is_blank([followup_arm_1][age])"}})
	if len(rows) != 1 || rows[0]["record_id"] != "8DISC002" {
		t.Fatalf("is_blank filter rows = %v, want only 8DISC002", rows)
	}
	// A bare [field] reference falls back to the project's first event
	// (baseline_arm_1): 8DISC001 matches on its baseline age of 42 — never
	// on the follow-up value of 43. Whole records are kept, so 8DISC001
	// contributes both of its event rows.
	rows = f.exportJSON(t, "tok-admin", url.Values{"filterLogic": {`[age] = "42"`}})
	if len(rows) != 2 {
		t.Fatalf("bare [field] filter rows = %v, want the two rows of 8DISC001", rows)
	}
	for _, row := range rows {
		if row["record_id"] != "8DISC001" {
			t.Errorf("bare [field] filter row = %v, want only 8DISC001", row)
		}
	}
	rows = f.exportJSON(t, "tok-admin", url.Values{"filterLogic": {`[age] = "43"`}})
	if len(rows) != 0 {
		t.Fatalf("bare [field] must resolve at the first event only, got %v", rows)
	}
	// A malformed expression is a request error before any output.
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"}, "filterLogic": {"[age"}})
	mustStatus(t, code, 400, body)
	if !strings.Contains(body, "Invalid request") {
		t.Errorf("body = %s", body)
	}
}

func TestRecordExportAudit(t *testing.T) {
	f := newRecordFixture(t)
	f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC001"}})
	if got := auditCount(t, f.s, audit.Export); got != 1 {
		t.Errorf("export events = %d, want 1", got)
	}
	if got := viewCount(t, f.s); got != 1 {
		t.Errorf("record-view rows = %d, want 1", got)
	}
	// Zero returned records: the export event is still written, no view row.
	f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC999"}})
	if got := auditCount(t, f.s, audit.Export); got != 2 {
		t.Errorf("export events after empty export = %d, want 2", got)
	}
	if got := viewCount(t, f.s); got != 1 {
		t.Errorf("record-view rows after empty export = %d, want still 1", got)
	}
}

// importForm builds one data[i][…] parameter set.
func importForm(token string, rows ...map[string]string) url.Values {
	form := url.Values{"token": {token}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"}}
	for i, row := range rows {
		for k, v := range row {
			form[fmt.Sprintf("data[%d][%s]", i, k)] = []string{v}
		}
	}
	return form
}

func (f *recordFixture) importRows(t *testing.T, form url.Values) []importRow {
	code, body := f.call(t, form)
	return importResults(t, code, body)
}

func importResults(t *testing.T, code int, body string) []importRow {
	t.Helper()
	mustStatus(t, code, 200, body)
	var out []importRow
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad import json: %v / %s", err, body)
	}
	return out
}

func deleteResults(t *testing.T, code int, body string) []deleteRow {
	t.Helper()
	mustStatus(t, code, 200, body)
	var out []deleteRow
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad delete json: %v / %s", err, body)
	}
	return out
}

func storedValue(t *testing.T, f *recordFixture, record, event, field string) string {
	t.Helper()
	var v string
	err := f.s.DB.QueryRow(`SELECT value FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ? AND field_name = ?`,
		f.pid, record, event, field).Scan(&v)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("query %s/%s/%s: %v", record, event, field, err)
	}
	return v
}

func TestRecordImportAddUpdateInvalidate(t *testing.T) {
	f := newRecordFixture(t)

	// Add: a brand-new record answers 1 and stores the values.
	res := f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "51", "status": "1",
	}))
	if len(res) != 1 || res[0].ImportRecordID != 1 {
		t.Fatalf("add result = %v, want import_record_id 1", res)
	}
	if storedValue(t, f, "8DISC010", "baseline_arm_1", "age") != "51" {
		t.Error("imported age not stored")
	}
	if storedValue(t, f, "8DISC010", "baseline_arm_1", "record_id") != "8DISC010" {
		t.Error("record_id must be stored as the identifier value")
	}
	// The calculated field recomputes in the same call (REQ-VAL-037).
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "total"); got != "102" {
		t.Errorf("calculated total = %q, want 102", got)
	}
	if auditCount(t, f.s, audit.RecordCreated) != 1 {
		t.Error("record_created audit row missing")
	}

	// Update: an existing record answers 2.
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1", "age": "52",
	}))
	if res[0].ImportRecordID != 2 {
		t.Fatalf("update result = %v, want 2", res)
	}

	// Validation error: answer 0 with the codes, and nothing is stored.
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "not-a-number", "status": "9",
	}))
	if res[0].ImportRecordID != 0 {
		t.Fatalf("invalid row = %v, want 0", res)
	}
	for _, want := range []string{"TYPE_INVALID", "CHOICE_INVALID"} {
		if !strings.Contains(res[0].ImportFormName, want) {
			t.Errorf("error message missing %s: %q", want, res[0].ImportFormName)
		}
	}
	if storedValue(t, f, "8DISC010", "baseline_arm_1", "age") != "52" {
		t.Error("failed row must not touch stored values")
	}

	// Unknown field names are reported the same way.
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1", "bogus": "x",
	}))
	if res[0].ImportRecordID != 0 || !strings.Contains(res[0].ImportFormName, "UNKNOWN_FIELD") {
		t.Fatalf("unknown field row = %v", res)
	}

	// An empty value clears (REQ-VAL-024).
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1", "age": "",
	}))
	if res[0].ImportRecordID != 2 {
		t.Fatalf("clear row = %v, want 2", res)
	}
	if storedValue(t, f, "8DISC010", "baseline_arm_1", "age") != "" {
		t.Error("empty value must remove the stored value")
	}
}

func TestRecordImportDateCanonicalization(t *testing.T) {
	f := newRecordFixture(t)
	f.importRows(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"},
		"tz":                  {"+02:00"},
		"data[0][record_id]":  {"8DISC011"},
		"data[0][form_name]":  {"demo"},
		"data[0][event_name]": {"baseline_arm_1"},
		"data[0][visit_date]": {"2026-03-01"},
	})
	if got := storedValue(t, f, "8DISC011", "baseline_arm_1", "visit_date"); got != "2026-03-01+02:00" {
		t.Errorf("stored date = %q, want 2026-03-01+02:00 (tz parameter)", got)
	}
	// A value already carrying an offset keeps it.
	f.importRows(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"},
		"tz":                  {"+02:00"},
		"data[0][record_id]":  {"8DISC011"},
		"data[0][form_name]":  {"demo"},
		"data[0][event_name]": {"baseline_arm_1"},
		"data[0][visit_date]": {"2026-04-01-05:00"},
	})
	if got := storedValue(t, f, "8DISC011", "baseline_arm_1", "visit_date"); got != "2026-04-01-05:00" {
		t.Errorf("stored date = %q, want the supplied offset kept", got)
	}
	// An impossible calendar date is a validation error.
	res := f.importRows(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "action": {"import"}, "returnFormat": {"json"},
		"data[0][record_id]":  {"8DISC011"},
		"data[0][form_name]":  {"demo"},
		"data[0][event_name]": {"baseline_arm_1"},
		"data[0][visit_date]": {"2026-02-30"},
	})
	if res[0].ImportRecordID != 0 {
		t.Fatalf("impossible date row = %v, want 0", res)
	}
}

func TestRecordImportAnalysisMode(t *testing.T) {
	f := newRecordFixture(t)
	ctx := context.Background()
	tx, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetProjectModeTx(ctx, tx, f.pid, "analysis"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	code, body := f.call(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC012", "form_name": "demo", "event_name": "baseline_arm_1", "age": "1",
	}))
	mustStatus(t, code, 403, body)
	if !strings.Contains(body, "analysis mode") {
		t.Errorf("body = %s", body)
	}
	// Delete is rejected too; export still works (GD-20).
	code, body = f.call(t, url.Values{"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}, "records[0]": {"8DISC001"}})
	mustStatus(t, code, 403, body)
	if rows := f.exportJSON(t, "tok-admin", nil); len(rows) == 0 {
		t.Error("analysis mode must not block export")
	}
}

func TestRecordDelete(t *testing.T) {
	f := newRecordFixture(t)
	ctx := context.Background()

	// Whole record: values and identity row go together (REQ-API-036).
	code, body := f.call(t, url.Values{
		"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}, "returnFormat": {"json"},
		"records[0]": {"8DISC002"},
	})
	res := deleteResults(t, code, body)
	if res[0].Deleted != 1 {
		t.Fatalf("delete result = %v", res)
	}
	if dvs, err := f.s.ListDataValuesByRecord(ctx, f.pid, "8DISC002"); err != nil || len(dvs) > 0 {
		t.Errorf("values remain after delete: %v / %v", dvs, err)
	}
	if re, err := f.s.GetRecordEntity(ctx, f.pid, "8DISC002"); err != nil || re != nil {
		t.Errorf("record entity remains after delete: %+v / %v", re, err)
	}

	// Scoped delete: only the named field goes; the record survives.
	code, body = f.call(t, url.Values{
		"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}, "returnFormat": {"json"},
		"records[0]": {"8DISC001"}, "fields[0]": {"age"},
	})
	res = deleteResults(t, code, body)
	if res[0].Deleted != 1 {
		t.Fatalf("scoped delete result = %v", res)
	}
	if storedValue(t, f, "8DISC001", "baseline_arm_1", "age") != "" {
		t.Error("named field must be removed")
	}
	if storedValue(t, f, "8DISC001", "baseline_arm_1", "status") != "2" {
		t.Error("unrelated field must survive a scoped delete")
	}

	// The record_deleted audit entry carries the deleted values (REQ-AUD-009).
	var details string
	err := f.s.DB.QueryRow(`SELECT details FROM audit_events_` + strconv.Itoa(time.Now().UTC().Year()) +
		` WHERE event_type = 'record_deleted' ORDER BY id DESC LIMIT 1`).Scan(&details)
	if err != nil {
		t.Fatalf("read record_deleted: %v", err)
	}
	if !strings.Contains(details, "age") || !strings.Contains(details, "42") {
		t.Errorf("audit details lack the deleted values: %s", details)
	}

	// A read-only holder cannot delete.
	code, body = f.call(t, url.Values{
		"token": {"tok-de"}, "content": {"record"}, "action": {"delete"}, "records[0]": {"8DISC001"},
	})
	mustStatus(t, code, 403, body)

	// An unscoped delete is rejected; an unknown record reports deleted: 0.
	code, body = f.call(t, url.Values{"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}})
	mustStatus(t, code, 400, body)
	code, body = f.call(t, url.Values{
		"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}, "returnFormat": {"json"},
		"records[0]": {"8DISC999"},
	})
	res = deleteResults(t, code, body)
	if res[0].Deleted != 0 {
		t.Errorf("unknown record = %v, want deleted 0", res)
	}
}

func TestRecordDAGScope(t *testing.T) {
	f := newRecordFixture(t)
	ctx := context.Background()

	groupID, err := f.s.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.pid, Name: "site-a"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	// The editor assignment (token tok-edit) joins the group actively.
	var assignmentID int64
	if err := f.s.DB.QueryRow(`SELECT id FROM user_projects WHERE token = 'tok-edit'`).Scan(&assignmentID); err != nil {
		t.Fatalf("find assignment: %v", err)
	}
	if _, err := f.s.AddDAGMembership(ctx, &db.DagMembership{AssignmentID: assignmentID, GroupID: groupID, IsActive: true}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	// The seeded records have no identity row yet (values were inserted
	// directly); create 8DISC001's so it can join a group.
	tx, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.CreateRecordEntityTx(ctx, tx, &db.RecordEntity{ProjectID: f.pid, RecordID: "8DISC001"}); err != nil {
		t.Fatalf("CreateRecordEntityTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetRecordDAG(ctx, f.pid, "8DISC001", sql.NullInt64{Int64: groupID, Valid: true}); err != nil {
		t.Fatalf("SetRecordDAG: %v", err)
	}

	// The group holder sees only their group's records (REQ-AUTH-045); the
	// admin still sees everything.
	rows := f.exportJSON(t, "tok-edit", nil)
	for _, r := range rows {
		if r["record_id"] != "8DISC001" {
			t.Fatalf("DAG holder saw %v", r)
		}
	}
	if len(rows) == 0 {
		t.Fatal("DAG holder must see their group's record")
	}

	// A new import joins the holder's active group (REQ-API-093).
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC020", "form_name": "demo", "event_name": "baseline_arm_1", "age": "44",
	}))
	re, err := f.s.GetRecordEntity(ctx, f.pid, "8DISC020")
	if err != nil || re == nil {
		t.Fatalf("GetRecordEntity: %v / %+v", err, re)
	}
	if !re.DagGroupID.Valid || re.DagGroupID.Int64 != groupID {
		t.Errorf("new record group = %+v, want %d", re.DagGroupID, groupID)
	}
}

// decodeInto decodes a record-response JSON array (all values are strings
// by contract) into out.
func decodeInto(body string, out any) error {
	return json.Unmarshal([]byte(body), out)
}
