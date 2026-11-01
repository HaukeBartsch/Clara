package dataapi

// FIONA's processing scripts and Assign pages against a project shaped like
// FIONA's *Incoming* (REQ-API-037, REQ-TECH-022): the record identifier
// study_instance_uid, the instruments "Incoming Studies", "Series" and
// "Transfers" with the last two repeating, one event (this system's default
// `baseline_arm_1`), date-time fields and the transfer_error checkbox — the
// structure of assets/Incoming_odm_2026-10-25.xml. Every call below is the one
// the named FIONA file sends, parameters verbatim (unknown ones included, which
// REQ-API-017 drops), so these tests fail when the API stops answering FIONA
// unchanged. They cover the owner decisions DEV-API-35 (no event names the
// first), DEV-API-36 (`<instrument>_complete` and REDCap instrument spelling on
// import), DEV-API-37 (fields[] ∪ forms[]) and DEV-VAL-14 (dates without the
// offset on the data API), and the change window of REQ-API-181
// (`dateRangeBegin`). Named TestFiona* so ci/run.sh runs them.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
	"csms/api/internal/testdb"
)

const (
	incomingToken = "tok-incoming"
	incomingStudy = "1.2.826.0.1.3680043.8.498.20261031.1001"
)

type incomingFixture struct {
	h   *Handler
	s   *db.Store
	pid int64
	// instrument ids by stored name
	instr map[string]int64
	event int64
}

func newIncomingFixture(t *testing.T) *incomingFixture {
	t.Helper()
	cfg := &config.Config{
		AppEnv:           "development",
		DBConnection:     "sqlite",
		DBDatabase:       filepath.Join(t.TempDir(), "incoming.sqlite"),
		AnonSalt:         "test-salt",
		AnonDateShiftMin: 1,
		AnonDateShiftMax: 365,
		AppTimezone:      "UTC",
	}
	testdb.Use(t, cfg)
	s, err := db.Open(cfg, nil)
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
	pid, err := s.CreateProject(ctx, &db.Project{ProjectName: "Incoming", Organization: "Helse Vest",
		PIName: "FIONA", PIEmail: "fiona@example.org"})
	must(err)
	armID, err := s.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	must(err)
	ev, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1"})
	must(err)

	f := &incomingFixture{s: s, pid: pid, instr: map[string]int64{}, event: ev}
	addInstrument := func(name string, pos int, survey bool) int64 {
		id, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: name, Position: pos, IsSurvey: survey})
		must(err)
		f.instr[name] = id
		return id
	}
	inStudies := addInstrument("Incoming Studies", 1, false)
	inSeries := addInstrument("Series", 2, false)
	inTransfers := addInstrument("Transfers", 3, false)
	inFeedback := addInstrument("Feedback", 4, true) // survey-marked (GD-9)

	pos := 0
	field := func(instrID int64, name, vtype, choices string) {
		t.Helper()
		pos++
		fld := db.Field{ProjectID: pid, InstrumentID: instrID, FieldName: name, FieldType: "text", Position: pos,
			ExportApproved: true}
		if vtype != "" {
			fld.ValidationType = sql.NullString{String: vtype, Valid: true}
		}
		if choices != "" {
			fld.FieldType = "checkbox"
			fld.Choices = sql.NullString{String: choices, Valid: true}
		}
		if _, err := s.AddField(ctx, &fld); err != nil {
			t.Fatalf("seed AddField %s: %v", name, err)
		}
	}
	field(inStudies, "study_instance_uid", "", "")
	field(inStudies, "study_description", "", "")
	field(inStudies, "study_date", "", "") // DICOM YYYYMMDD, text in the ODM file
	field(inStudies, "study_arrival_date", "datetime", "")
	field(inStudies, "study_patient_id", "", "")
	// The AutoID run names it in fields[] (populateAutoID.py getIncomingData), and
	// an unknown fields[] entry is a 400 (DEV-API-41), as the real project has it.
	field(inStudies, "study_aetitle_addressed", "", "")
	field(inSeries, "series_instance_uid", "", "")
	field(inSeries, "series_description", "", "")
	field(inSeries, "series_num_files", "", "")
	field(inTransfers, "transfer_requested_date", "datetime", "")
	field(inTransfers, "transfer_project_name", "", "")
	field(inTransfers, "transfer_name", "", "")
	field(inTransfers, "transfer_event_name", "", "")
	field(inTransfers, "transfer_date", "datetime", "")
	field(inTransfers, "transfer_mapped_uid", "", "")
	field(inTransfers, "transfer_error_message", "", "")
	field(inTransfers, "transfer_active_proc_time", "", "")
	field(inTransfers, "transfer_error", "", "1$Anonymization error##2$Send error##3$Masking applied")
	field(inFeedback, "feedback_note", "", "")

	must(s.SetInstrumentEventsForArm(ctx, pid, armID, []db.InstrumentEvent{
		{InstrumentID: inStudies, EventID: ev},
		{InstrumentID: inSeries, EventID: ev},
		{InstrumentID: inTransfers, EventID: ev},
		{InstrumentID: inFeedback, EventID: ev},
	}))
	tx, err := s.DB.BeginTx(ctx, nil)
	must(err)
	must(s.SetRepeatingEventsTx(ctx, tx, inSeries, []int64{ev}))
	must(s.SetRepeatingEventsTx(ctx, tx, inTransfers, []int64{ev}))
	must(tx.Commit())
	s.BumpStructureCache()

	userID, err := s.CreateUser(ctx, &db.User{Email: "fiona@example.org", DisplayName: "FIONA", Enabled: true, AuthSource: "local"})
	must(err)
	_, err = s.AddAssignment(ctx, &db.Assignment{UserID: userID, ProjectID: pid, Token: incomingToken})
	must(err)

	aw := audit.NewWriter(s.DB, string(s.Dialect))
	must(aw.EnsureYear(ctx))
	f.h = &Handler{Store: s, Cfg: cfg, Audit: aw}
	return f
}

// importData posts one FIONA import: the `data` parameter as the JSON array the
// scripts send, with their framing parameters (populateIncoming.py updateREDCap).
func (f *incomingFixture) importData(t *testing.T, rows any, count bool) (int, string) {
	t.Helper()
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	form := url.Values{"token": {incomingToken}, "content": {"record"}, "format": {"json"}, "type": {"flat"},
		"overwriteBehavior": {"normal"}, "forceAutoNumber": {"false"}, "data": {string(payload)},
		"returnFormat": {"json"}, "record_id": {"da39a3ee5e6b4b0d"}}
	if count {
		form.Set("returnContent", "count")
	}
	return do(t, f.h, form)
}

func (f *incomingFixture) value(t *testing.T, repeating string, instance int, field string) string {
	t.Helper()
	slot, n := db.InstanceSlot(repeating, instance)
	var v string
	err := f.s.DB.QueryRow(`SELECT value FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		AND repeating_instrument = ? AND repeating_instance_number = ? AND field_name = ?`,
		f.pid, incomingStudy, "baseline_arm_1", slot, n, field).Scan(&v)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("query %s: %v", field, err)
	}
	return v
}

// completion returns the stored state of an instrument's instance ("" = none).
func (f *incomingFixture) completion(t *testing.T, instrument string, instance int) string {
	t.Helper()
	comps, err := f.s.ListInstrumentCompletions(context.Background(), f.pid)
	if err != nil {
		t.Fatalf("ListInstrumentCompletions: %v", err)
	}
	for _, c := range comps {
		if c.RecordID == incomingStudy && c.EventID == f.event && c.InstrumentID == f.instr[instrument] && c.Instance == instance {
			return c.State
		}
	}
	return ""
}

func countOf(t *testing.T, code int, body string) int {
	t.Helper()
	mustStatus(t, code, 200, body)
	var c struct {
		Count *int `json:"count"`
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.Count == nil {
		t.Fatalf("count response = %s", body)
	}
	return *c.Count
}

// populateIncoming.py lines 592-657: the study row and one row per series, no
// redcap_event_name, `series` in REDCap's spelling and the form-status columns
// incoming_studies_complete / series_complete — all stored at the first event.
func populateIncomingRows() []map[string]any {
	return []map[string]any{
		{"study_instance_uid": incomingStudy, "study_description": "MR Brain", "study_date": "20261031",
			"study_patient_id": "ab12", "study_arrival_date": "2026-10-31 10:40", "incoming_studies_complete": 2},
		{"study_instance_uid": incomingStudy, "series_instance_uid": incomingStudy + ".1", "series_description": "T1",
			"redcap_repeat_instrument": "series", "redcap_repeat_instance": 1, "series_num_files": 176, "series_complete": 2},
		{"study_instance_uid": incomingStudy, "series_instance_uid": incomingStudy + ".2", "series_description": "T2",
			"redcap_repeat_instrument": "series", "redcap_repeat_instance": 2, "series_num_files": 40, "series_complete": 2},
	}
}

func TestFionaIncomingPopulateImport(t *testing.T) {
	f := newIncomingFixture(t)
	if n := f.importCount(t, populateIncomingRows()); n != 3 {
		t.Fatalf("count = %d, want all 3 rows applied", n)
	}
	if v := f.value(t, "", 1, "study_description"); v != "MR Brain" {
		t.Errorf("study_description = %q", v)
	}
	if v := f.value(t, "", 1, "study_date"); v != "20261031" {
		t.Errorf("study_date = %q, the DICOM text kept", v)
	}
	// Stored as collected with the import's zone (APP_TIMEZONE UTC here).
	if v := f.value(t, "", 1, "study_arrival_date"); v != "2026-10-31 10:40+00:00" {
		t.Errorf("study_arrival_date stored = %q", v)
	}
	if v := f.value(t, "Series", 1, "series_description"); v != "T1" {
		t.Errorf("series instance 1 = %q", v)
	}
	if v := f.value(t, "Series", 2, "series_num_files"); v != "40" {
		t.Errorf("series instance 2 num_files = %q", v)
	}
	for _, c := range []struct {
		instr string
		n     int
	}{{"Incoming Studies", 1}, {"Series", 1}, {"Series", 2}} {
		if got := f.completion(t, c.instr, c.n); got != db.CompletionFinished {
			t.Errorf("%s instance %d completion = %q, want finished", c.instr, c.n, got)
		}
	}

	// The export names the completion as REDCap's code and the instance rows by
	// number; the instrument keeps its stored name (FIONA reads neither).
	rows := decodeJSON(t, f.okExport(t, url.Values{"token": {incomingToken}, "content": {"record"},
		"format": {"json"}, "type": {"flat"}, "records[0]": {incomingStudy}, "returnFormat": {"json"}}))
	if len(rows) != 3 || rows[0]["Incoming Studies_complete"] != "2" || rows[1]["Series_complete"] != "2" {
		t.Fatalf("export after populateIncoming = %v", rows)
	}

	// Running the script again sends the same rows: nothing changes, no new
	// completion entries (REQ-API-042).
	before := auditCount(t, f.s, audit.InstrumentCompleted)
	f.importCount(t, populateIncomingRows())
	if after := auditCount(t, f.s, audit.InstrumentCompleted); after != before {
		t.Errorf("repeated import wrote %d completion entries, want none", after-before)
	}
}

// Assign/php/createTransferRequest.php: one transfer request, no event, the
// instrument as `transfers`; then populateIncoming.py getTransfers (records[0],
// fields[0]=study_instance_uid, forms[0]=transfers) must see it to pick the next
// repeat instance — the fields[] ∪ forms[] reading (DEV-API-37).
func TestFionaIncomingTransferRequest(t *testing.T) {
	f := newIncomingFixture(t)
	f.importCount(t, populateIncomingRows())

	code, body := f.importData(t, []map[string]any{{
		"study_instance_uid": incomingStudy, "redcap_repeat_instance": 1, "redcap_repeat_instrument": "transfers",
		"transfer_project_name": "R_0001_TEST", "transfer_name": "R_0001_TEST_001", "transfer_event_name": "",
		"transfer_requested_date": "2026-11-01 10:40"}}, true)
	if n := countOf(t, code, body); n != 1 {
		t.Fatalf("createTransferRequest.php count = %d, want 1", n)
	}
	if v := f.value(t, "Transfers", 1, "transfer_project_name"); v != "R_0001_TEST" {
		t.Fatalf("transfer_project_name = %q", v)
	}

	code, body = do(t, f.h, url.Values{"token": {incomingToken}, "content": {"record"}, "format": {"json"},
		"type": {"flat"}, "rawOrLabel": {"raw"}, "records[0]": {incomingStudy}, "fields[0]": {"study_instance_uid"},
		"forms[0]": {"transfers"}, "rawOrLabelHeaders": {"raw"}, "exportCheckboxLabel": {"false"},
		"exportSurveyFields": {"false"}, "exportDataAccessGroups": {"false"}, "returnFormat": {"json"}})
	mustStatus(t, code, 200, body)
	rows := decodeJSON(t, body)
	if len(rows) != 2 {
		t.Fatalf("getTransfers rows = %d, want the event row and the transfer: %s", len(rows), body)
	}
	// Every row carries what the script indexes without a guard.
	for i, r := range rows {
		for _, k := range []string{"study_instance_uid", "transfer_project_name", "transfer_name",
			"transfer_event_name", "redcap_repeat_instance"} {
			if _, ok := r[k]; !ok {
				t.Errorf("row %d lacks %q: %v", i, k, r)
			}
		}
	}
	if rows[0]["study_instance_uid"] != incomingStudy || rows[1]["transfer_project_name"] != "R_0001_TEST" {
		t.Errorf("getTransfers = %v", rows)
	}
	if !strings.Contains(body, `"redcap_repeat_instance":1`) {
		t.Errorf("the transfer's instance must be a JSON number (the script compares it with 0): %s", body)
	}
	// forms[]=transfers keeps its completion column; the study's is not asked for.
	if _, ok := rows[1]["Transfers_complete"]; !ok {
		t.Errorf("Transfers_complete missing beside fields[]: %v", rows[1])
	}
	if _, ok := rows[0]["Incoming Studies_complete"]; ok {
		t.Errorf("an instrument reached through fields[] only carries no completion: %v", rows[0])
	}
}

// anonymizeAndSend.py lines 562-575 writes the result back with the instrument
// name the export gave it, the checkbox boxes and transfers_complete; then
// createTransferRequests.py and resendProject.py parse both dates with
// strptime('%Y-%m-%d %H:%M') — the data API writes them without the offset
// (DEV-VAL-14).
func TestFionaIncomingTransferDates(t *testing.T) {
	f := newIncomingFixture(t)
	f.importCount(t, populateIncomingRows())
	f.importCount(t, []map[string]any{{
		"study_instance_uid": incomingStudy, "redcap_repeat_instance": 1, "redcap_repeat_instrument": "transfers",
		"transfer_project_name": "R_0001_TEST", "transfer_name": "R_0001_TEST_001",
		"transfer_requested_date": "2026-11-01 10:40"}})

	// createTransferRequests.py getTransfers: the request as the script stores it.
	exportTransfers := func() map[string]any {
		t.Helper()
		rows := decodeJSON(t, f.okExport(t, url.Values{"token": {incomingToken}, "content": {"record"},
			"format": {"json"}, "type": {"flat"}, "rawOrLabel": {"raw"},
			"fields[0]": {"study_instance_uid"}, "fields[1]": {"transfer_project_name"}, "fields[2]": {"transfer_name"},
			"fields[3]": {"transfer_date"}, "fields[4]": {"transfer_requested_date"}, "fields[5]": {"transfer_event_name"},
			"rawOrLabelHeaders": {"raw"}, "exportCheckboxLabel": {"false"}, "exportSurveyFields": {"false"},
			"exportDataAccessGroups": {"false"}, "returnFormat": {"json"}}))
		for _, r := range rows {
			if r["redcap_repeat_instrument"] == "Transfers" {
				return r
			}
		}
		t.Fatalf("no transfer row in %v", rows)
		return nil
	}
	req := exportTransfers()

	code, body := f.importData(t, []map[string]any{{
		"study_instance_uid": incomingStudy, "redcap_repeat_instance": 1,
		"redcap_repeat_instrument": req["redcap_repeat_instrument"], // "Transfers", as exported
		"transfer_date":            "2026-11-01 11:05", "transfer_mapped_uid": "1.3.6.1.4.1.45037.99",
		"transfer_error___1": 0, "transfer_error___2": 0, "transfer_error___3": 1,
		"transfer_active_proc_time": 12, "transfer_error_message": "", "transfers_complete": 2}}, true)
	if n := countOf(t, code, body); n != 1 {
		t.Fatalf("anonymizeAndSend.py write-back count = %d, want 1", n)
	}
	if got := f.completion(t, "Transfers", 1); got != db.CompletionFinished {
		t.Errorf("Transfers completion = %q, want finished", got)
	}
	if v := f.value(t, "Transfers", 1, "transfer_error___3"); v != "1" {
		t.Errorf("transfer_error___3 = %q", v)
	}

	got := exportTransfers()
	for _, k := range []string{"transfer_date", "transfer_requested_date"} {
		if _, err := time.Parse("2006-01-02 15:04", fmt.Sprint(got[k])); err != nil {
			t.Errorf("%s = %q: FIONA's strptime('%%Y-%%m-%%d %%H:%%M') fails (%v)", k, got[k], err)
		}
	}
	if got["transfer_date"] != "2026-11-01 11:05" || got["transfer_requested_date"] != "2026-11-01 10:40" {
		t.Errorf("dates = %q / %q, want the wall times written", got["transfer_date"], got["transfer_requested_date"])
	}
	// Storage keeps the collection offset (REQ-VAL-041) …
	if v := f.value(t, "Transfers", 1, "transfer_date"); v != "2026-11-01 11:05+00:00" {
		t.Errorf("stored transfer_date = %q", v)
	}
	// … and the administration surface's canonical form is untouched by the flag.
	if withoutDateOffset("2026-11-01 11:05+02:00") != "2026-11-01 11:05" ||
		withoutDateOffset("2026-11-01-05:00") != "2026-11-01" || withoutDateOffset("20261101") != "20261101" {
		t.Error("withoutDateOffset must drop the suffix only")
	}
}

// A date sent back as the data API exported it — the wall time without offset
// — is no change: the value collected at +02:00 keeps its offset, and only a
// changed wall time takes the import's zone (REQ-VAL-041, DEV-VAL-14).
func TestFionaIncomingBareDateKeepsOffset(t *testing.T) {
	f := newIncomingFixture(t)
	f.importCount(t, []map[string]any{{"study_instance_uid": incomingStudy,
		"study_arrival_date": "2026-10-31 10:40+02:00"}})
	before := auditCount(t, f.s, audit.RecordUpdated)
	f.importCount(t, []map[string]any{{"study_instance_uid": incomingStudy,
		"study_arrival_date": "2026-10-31 10:40"}})
	if v := f.value(t, "", 1, "study_arrival_date"); v != "2026-10-31 10:40+02:00" {
		t.Errorf("unchanged wall time rewrote the offset: %q", v)
	}
	if after := auditCount(t, f.s, audit.RecordUpdated); after != before {
		t.Errorf("unchanged date wrote %d record_updated entries", after-before)
	}
	f.importCount(t, []map[string]any{{"study_instance_uid": incomingStudy,
		"study_arrival_date": "2026-10-31 11:00"}})
	if v := f.value(t, "", 1, "study_arrival_date"); v != "2026-10-31 11:00+00:00" {
		t.Errorf("changed wall time = %q, want the import's zone", v)
	}
}

// The rules of an imported `<instrument>_complete` (REQ-API-134, DEV-API-36).
func TestFionaIncomingCompleteKeys(t *testing.T) {
	f := newIncomingFixture(t)
	f.importCount(t, populateIncomingRows())
	results := func(code int, body string) []importRow {
		t.Helper()
		return importResults(t, code, body)
	}

	// 1 → unverified, in the stored spelling too.
	res := results(f.importData(t, []map[string]any{{"study_instance_uid": incomingStudy,
		"Incoming Studies_complete": "1"}}, false))
	if res[0].ImportRecordID != importUpdated || f.completion(t, "Incoming Studies", 1) != db.CompletionUnverified {
		t.Fatalf("stored spelling, 1: %+v, state %q", res, f.completion(t, "Incoming Studies", 1))
	}
	if auditCount(t, f.s, audit.InstrumentUnverified) != 1 {
		t.Error("unverified must write its audit entry")
	}
	// 0 → the assignment cleared.
	results(f.importData(t, []map[string]any{{"study_instance_uid": incomingStudy, "incoming_studies_complete": 0}}, false))
	if got := f.completion(t, "Incoming Studies", 1); got != "" {
		t.Errorf("0 must clear the state, got %q", got)
	}
	if auditCount(t, f.s, audit.InstrumentUncompleted) != 1 {
		t.Error("clearing must write its audit entry")
	}
	// Empty is not supplied: nothing changes.
	results(f.importData(t, []map[string]any{{"study_instance_uid": incomingStudy, "series_complete": "",
		"study_description": "MR Brain"}}, false))
	if got := f.completion(t, "Series", 1); got != db.CompletionFinished {
		t.Errorf("an empty series_complete changed the state to %q", got)
	}

	invalid := func(label string, row map[string]any, want string) {
		t.Helper()
		res := results(f.importData(t, []map[string]any{row}, false))
		if len(res) != 1 || res[0].ImportRecordID != importInvalid || !strings.Contains(res[0].ImportFormName, want) {
			t.Errorf("%s: %+v, want a validation error naming %q", label, res, want)
		}
	}
	invalid("code outside 0/1/2", map[string]any{"study_instance_uid": incomingStudy, "incoming_studies_complete": 3},
		"must be 0, 1 or 2")
	invalid("another instrument's state on an instance row", map[string]any{"study_instance_uid": incomingStudy,
		"redcap_repeat_instrument": "series", "redcap_repeat_instance": 1, "transfers_complete": 2},
		"belongs to instrument 'Transfers', not 'Series'")
	invalid("unknown instrument", map[string]any{"study_instance_uid": incomingStudy, "imaging_complete": 2},
		"unknown field 'imaging_complete'")

	// A survey instrument always exports 2 and takes no assignment (GD-9): a
	// round-tripped value is accepted and stores nothing.
	res = results(f.importData(t, []map[string]any{{"study_instance_uid": incomingStudy, "feedback_complete": 2}}, false))
	if res[0].ImportRecordID == importInvalid || f.completion(t, "Feedback", 1) != "" {
		t.Errorf("survey _complete: %+v, state %q", res, f.completion(t, "Feedback", 1))
	}
}

// populateIncoming.py getAllRoutingRules reads `content=metadata` with
// fields[0]=routing_dest_project and takes the first row as that field's
// choices: fields[] narrows the metadata, together with forms[] (DEV-API-37).
func TestFionaIncomingMetadataFields(t *testing.T) {
	f := newIncomingFixture(t)
	meta := func(extra url.Values) []map[string]any {
		t.Helper()
		form := url.Values{"token": {incomingToken}, "content": {"metadata"}, "format": {"json"}, "returnFormat": {"json"}}
		for k, v := range extra {
			form[k] = v
		}
		return decodeJSON(t, f.okExport(t, form))
	}
	rows := meta(url.Values{"fields[0]": {"transfer_project_name"}})
	if len(rows) != 1 || rows[0]["field_name"] != "transfer_project_name" {
		t.Fatalf("metadata fields[0] = %v, want that one field", rows)
	}
	rows = meta(url.Values{"forms[0]": {"series"}, "fields[0]": {"study_description"}})
	names := []string{}
	for _, r := range rows {
		names = append(names, fmt.Sprint(r["field_name"]))
	}
	if strings.Join(names, ",") != "study_description,series_instance_uid,series_description,series_num_files" {
		t.Errorf("metadata fields[] ∪ forms[] = %v", names)
	}
}

// importCount posts an import with returnContent=count, as every FIONA script
// does, and returns the applied count.
func (f *incomingFixture) importCount(t *testing.T, rows any) int {
	t.Helper()
	code, body := f.importData(t, rows, true)
	return countOf(t, code, body)
}

// okExport posts a read call and returns its 200 body.
func (f *incomingFixture) okExport(t *testing.T, form url.Values) string {
	t.Helper()
	code, body := do(t, f.h, form)
	mustStatus(t, code, 200, body)
	return body
}

// populateAutoID.py getIncomingData: the AutoID run asks only for studies
// changed in the last 14 days (`dateRangeBegin`, REQ-API-181). Without the
// window it read every study ever received and could open transfer requests for
// old ones. The window is the half-open [begin, end) over the audited data
// changes, completion states included, read in APP_TIMEZONE.
func TestFionaIncomingDateRange(t *testing.T) {
	f := newIncomingFixture(t)
	ctx := context.Background()
	clock := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	f.h.Audit.Now = func() time.Time { return clock }
	if err := f.h.Audit.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	// Record ids sort ascending in an export, so the old study comes first.
	const oldStudy, newStudy = "1.2.3.4.1001", "1.2.3.4.1002"
	study := func(uid string) []map[string]any {
		return []map[string]any{{"study_instance_uid": uid, "study_description": "MR Brain",
			"study_patient_id": "P-" + uid[len(uid)-4:]}}
	}
	f.importCount(t, study(oldStudy)) // arrives 2026-10-01 08:00 UTC
	clock = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	f.importCount(t, study(newStudy))

	// FIONA's call verbatim, its begin 14 days before "now" (2026-10-29 here).
	autoID := func(begin, end string) []map[string]any {
		t.Helper()
		form := url.Values{"token": {incomingToken}, "content": {"record"}, "format": {"json"}, "type": {"flat"},
			"fields[0]": {"study_aetitle_addressed"}, "fields[1]": {"study_instance_uid"},
			"fields[2]": {"transfer_project_name"}, "fields[3]": {"study_patient_id"},
			"rawOrLabel": {"raw"}, "rawOrLabelHeaders": {"raw"}, "exportCheckboxLabel": {"false"},
			"exportSurveyFields": {"false"}, "exportDataAccessGroups": {"false"}, "returnFormat": {"json"}}
		if begin != "" {
			form.Set("dateRangeBegin", begin)
		}
		if end != "" {
			form.Set("dateRangeEnd", end)
		}
		return decodeJSON(t, f.okExport(t, form))
	}
	uids := func(rows []map[string]any) string {
		seen := map[string]bool{}
		var out []string
		for _, r := range rows {
			if u := fmt.Sprint(r["study_instance_uid"]); !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
		return strings.Join(out, ",")
	}

	if got := uids(autoID("", "")); got != oldStudy+","+newStudy {
		t.Fatalf("no window: want both studies, got %q", got)
	}
	if got := uids(autoID("2026-10-15 08:00:00", "")); got != newStudy {
		t.Errorf("last 14 days: want only %s, got %q", newStudy, got)
	}

	// A completion state is a data change (REDCap logs its form status as data):
	// setting it on the old study brings that study into the window.
	clock = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	f.importCount(t, []map[string]any{{"study_instance_uid": oldStudy, "incoming_studies_complete": 2}})
	if got := uids(autoID("2026-10-15 08:00:00", "")); got != oldStudy+","+newStudy {
		t.Errorf("after the completion change: want both studies, got %q", got)
	}

	// Half-open: the begin is inclusive, the end exclusive.
	if got := uids(autoID("2026-10-20 08:00:00", "2026-10-25 10:00:00")); got != newStudy {
		t.Errorf("[new study's change, old study's change): want %s, got %q", newStudy, got)
	}
	if got := uids(autoID("2026-10-01", "2026-10-20 08:00")); got != oldStudy {
		t.Errorf("[2026-10-01, new study's change): want %s, got %q", oldStudy, got)
	}
	// A window holding no change is an empty export, not an error.
	if rows := autoID("2026-11-01 00:00:00", ""); len(rows) != 0 {
		t.Errorf("future window: want no rows, got %v", rows)
	}
	if rows := autoID("2026-10-25 10:00:00", "2026-10-01 00:00:00"); len(rows) != 0 {
		t.Errorf("begin after end: want no rows, got %v", rows)
	}

	// The bounds are the deployment's wall time, DST included: in Europe/Oslo
	// the new study's change (2026-10-20 08:00 UTC) happened at 10:00 summer
	// time, and the old study's completion (2026-10-25 10:00 UTC) at 11:00 —
	// summer time ended that morning.
	f.h.Cfg.AppTimezone = "Europe/Oslo"
	if got := uids(autoID("2026-10-20 10:00:00", "2026-10-25 11:00:00")); got != newStudy {
		t.Errorf("Oslo [10:00 on the 20th, 11:00 on the 25th): want %s, got %q", newStudy, got)
	}
	if got := uids(autoID("2026-10-20 10:00:01", "2026-10-25 11:00:00")); got != "" {
		t.Errorf("Oslo from 10:00:01: want nothing, got %q", got)
	}
	if got := uids(autoID("2026-10-25 11:00:00", "")); got != oldStudy {
		t.Errorf("Oslo from 11:00 on the 25th: want %s, got %q", oldStudy, got)
	}
	f.h.Cfg.AppTimezone = "UTC"

	// The export audit row records the window as sent, and only when sent.
	lastExportFilters := func() map[string]any {
		t.Helper()
		var details string
		if err := f.s.DB.QueryRowContext(ctx, `SELECT details FROM audit_events
			WHERE project_id = ? AND event_type = ? ORDER BY id DESC LIMIT 1`, f.pid, audit.Export).Scan(&details); err != nil {
			t.Fatalf("export audit row: %v", err)
		}
		var d struct {
			Filters map[string]any `json:"filters"`
		}
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			t.Fatalf("export audit details %q: %v", details, err)
		}
		return d.Filters
	}
	autoID("2026-10-15 08:00:00", "")
	if fl := lastExportFilters(); fl["date_range_begin"] != "2026-10-15 08:00:00" || fl["date_range_end"] != nil {
		t.Errorf("audit filters with a begin: %v", fl)
	}
	autoID("", "")
	if fl := lastExportFilters(); fl["date_range_begin"] != nil {
		t.Errorf("audit filters without a window carry date_range_begin: %v", fl)
	}

	// A malformed bound is refused before any record is read, and audits nothing.
	exports := func() int {
		t.Helper()
		var n int
		if err := f.s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND event_type = ?`,
			f.pid, audit.Export).Scan(&n); err != nil {
			t.Fatalf("count exports: %v", err)
		}
		return n
	}
	before := exports()
	for _, c := range []struct{ key, value, want string }{
		{"dateRangeBegin", "2026-13-01 00:00:00", "Invalid dateRangeBegin"},
		{"dateRangeBegin", "2026-02-30", "Invalid dateRangeBegin"},
		{"dateRangeBegin", "2026-10-15T08:00:00", "Invalid dateRangeBegin"},
		{"dateRangeEnd", "yesterday", "Invalid dateRangeEnd"},
	} {
		form := url.Values{"token": {incomingToken}, "content": {"record"}, "format": {"json"}, c.key: {c.value}}
		code, body := do(t, f.h, form)
		if code != 400 || !strings.Contains(body, c.want) {
			t.Errorf("%s=%q: want 400 %q, got %d %s", c.key, c.value, c.want, code, body)
		}
	}
	if after := exports(); after != before {
		t.Errorf("refused windows wrote %d export audit rows", after-before)
	}
}

// TestFionaResendKeepsMappedUID: anonymizeAndSend.py writes back with
// overwriteBehavior=normal, and a failed resend sends the transfer row with the
// cells it no longer holds left empty. REDCap's normal keeps the stored values
// under empty cells; reading them as clears erased the transfer's mapped UID in
// the FIONA test run (REQ-VAL-024, DEV-API-43). A box sent as 0 is a value and
// still unchecks. overwrite — FIONA's process_tiff_importREDCap.py — and an
// omitted parameter keep clearing.
func TestFionaResendKeepsMappedUID(t *testing.T) {
	f := newIncomingFixture(t)
	row := func(uid string, box3 any) []map[string]any {
		return []map[string]any{{"study_instance_uid": incomingStudy, "redcap_repeat_instance": 1,
			"redcap_repeat_instrument": "transfers", "transfer_project_name": "StudyB",
			"transfer_mapped_uid": uid, "transfer_error___3": box3, "transfer_error_message": ""}}
	}
	if code, body := f.importData(t, row("1.3.6.1.4.1.45037.99", 1), true); countOf(t, code, body) != 1 {
		t.Fatalf("seed: %d %s", code, body)
	}
	// The failed resend: empty UID, the box unchecked explicitly.
	if code, body := f.importData(t, row("", 0), true); countOf(t, code, body) != 1 {
		t.Fatalf("resend: %d %s", code, body)
	}
	if v := f.value(t, "Transfers", 1, "transfer_mapped_uid"); v != "1.3.6.1.4.1.45037.99" {
		t.Errorf("transfer_mapped_uid after a normal resend = %q, want it kept", v)
	}
	if v := f.value(t, "Transfers", 1, "transfer_error___3"); v != "" {
		t.Errorf("transfer_error___3 = %q after 0, want the box unchecked", v)
	}

	// overwrite clears, as asked; so does a call that names no behaviour.
	for _, behaviour := range []string{"overwrite", ""} {
		f.importData(t, row("1.3.6.1.4.1.45037.99", ""), true)
		payload, _ := json.Marshal(row("", ""))
		form := url.Values{"token": {incomingToken}, "content": {"record"}, "format": {"json"},
			"returnContent": {"count"}, "data": {string(payload)}}
		if behaviour != "" {
			form.Set("overwriteBehavior", behaviour)
		}
		code, body := do(t, f.h, form)
		if n := countOf(t, code, body); n != 1 {
			t.Fatalf("%q: count = %d", behaviour, n)
		}
		if v := f.value(t, "Transfers", 1, "transfer_mapped_uid"); v != "" {
			t.Errorf("overwriteBehavior %q: transfer_mapped_uid = %q, want cleared", behaviour, v)
		}
	}
}
