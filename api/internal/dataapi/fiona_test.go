package dataapi

// Fiona compatibility fixtures (REQ-API-037, REQ-TECH-022): every call
// example in VISION_AND_REQUIREMENTS.md ("Endpoints used by Fiona") encoded
// as a table-driven fixture — form-encoded request → expected status + body —
// run against one seeded project. This is charter success criterion 1 as an
// executable regression test: the research information system keeps working
// unchanged against this API. The CI convention (ci/run.sh) runs every test
// named TestFiona*.
//
// The seeded project mirrors the shape of the FIONA examples — a longitudinal
// DataTransferProjects project with the record names the cURL examples ask
// for (1462-0004, 1490-0004) plus 8DISC-named records for the naming-pattern
// calls. Call parameters are sent verbatim from the document; unknown
// parameters (exportCheckboxLabel, exportSurveyFields, …) must be accepted
// and dropped (REQ-API-017), so they stay in the fixtures on purpose.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
	"csms/api/internal/testdb"
)

// fionaToken is the single project token every fixture call carries. The
// holder is a plain project member without a role — full permissions
// (VISION: members not assigned to a role have full access).
const fionaToken = "tok-fiona"

type fionaFixture struct {
	h   *Handler
	s   *db.Store
	pid int64
}

func newFionaFixture(t *testing.T) *fionaFixture {
	t.Helper()
	cfg := &config.Config{
		AppEnv:           "development",
		DBConnection:     "sqlite",
		DBDatabase:       filepath.Join(t.TempDir(), "fiona.sqlite"),
		AnonSalt:         "test-salt",
		AnonDateShiftMin: 1,
		AnonDateShiftMax: 365,
		AppTimezone:      "UTC",
	}
	testdb.Use(t, cfg)
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

	pid, err := s.CreateProject(ctx, &db.Project{
		ProjectName:      "DataTransferProjects",
		Organization:     "Helse Vest",
		PIName:           "Ansgar Espeland",
		PIEmail:          "ansgar@example.org",
		RekNumber:        sql.NullString{String: "2019/36045", Valid: true},
		StartDate:        sql.NullString{String: "2019-04-11", Valid: true},
		ParticipantNames: "8DISC[0-9][0-9][0-9]",
	})
	must(err)
	armID, err := s.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	must(err)

	// Events as in the "Export Events" example response (trimmed to three).
	evEvent1, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "Event 1", UniqueEventName: "event_1_arm_1"})
	must(err)
	evBaseline, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1"})
	must(err)
	// M5Y1 exists but no instrument is mapped to it — formEventMapping must
	// then leave it out.
	_, err = s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "M5Y1", UniqueEventName: "m5y1_arm_1"})
	must(err)

	// Instruments from the record-export examples. The first field of the
	// first instrument is the record identifier (GD-8).
	inProjects, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "projects", Position: 1})
	must(err)
	inLeaveAlone, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "leave_alone_do_not_touch", Position: 2})
	must(err)
	inTransfers, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "transfers", Position: 3})
	must(err)

	add := func(instrID int64, names ...string) {
		t.Helper()
		for i, name := range names {
			f := db.Field{ProjectID: pid, InstrumentID: instrID,
				FieldName: name, FieldType: "text", Position: i + 1}
			if _, err := s.AddField(ctx, &f); err != nil {
				t.Fatalf("seed AddField %s: %v", name, err)
			}
		}
	}
	add(inProjects, "record_id", "project_name", "project_id", "project_active")
	add(inLeaveAlone, "leave_alone_notes", "leave_alone_current_rek",
		"leave_alone_mrn", "leave_alone_do_not_touch_complete")
	add(inTransfers, "transfer_project_name", "transfer_mapped_uid")

	must(s.SetInstrumentEventsForArm(ctx, pid, armID, []db.InstrumentEvent{
		{InstrumentID: inProjects, EventID: evEvent1},
		{InstrumentID: inLeaveAlone, EventID: evBaseline},
		{InstrumentID: inTransfers, EventID: evEvent1},
	}))

	userID, err := s.CreateUser(ctx, &db.User{Email: "fiona@example.org",
		DisplayName: "FIONA", Enabled: true, AuthSource: "local"})
	must(err)
	_, err = s.AddAssignment(ctx, &db.Assignment{UserID: userID, ProjectID: pid, Token: fionaToken})
	must(err)

	dv := func(record, event, field, value string) {
		t.Helper()
		must(s.AddDataValue(ctx, &db.DataValue{ProjectID: pid, RecordID: record,
			UniqueEventName: event, FieldName: field, Value: value}))
	}
	// A projects-type record (the first PHP example asks for its project_id).
	dv("8DISC001", "event_1_arm_1", "record_id", "8DISC001")
	dv("8DISC001", "event_1_arm_1", "project_name", "8DISC001")
	dv("8DISC001", "event_1_arm_1", "project_id", "33")
	dv("8DISC001", "event_1_arm_1", "project_active", "1")
	// Transfer records for the filterLogic example (matching UID on 8DISC002).
	dv("8DISC002", "event_1_arm_1", "record_id", "8DISC002")
	dv("8DISC002", "event_1_arm_1", "transfer_project_name", "StudyB")
	dv("8DISC002", "event_1_arm_1", "transfer_mapped_uid", "1.2.3.4")
	dv("8DISC003", "event_1_arm_1", "record_id", "8DISC003")
	dv("8DISC003", "event_1_arm_1", "transfer_mapped_uid", "9.9.9.9")
	// The two records the second cURL example names, with leave_alone values.
	dv("1462-0004", "baseline_arm_1", "record_id", "1462-0004")
	dv("1462-0004", "baseline_arm_1", "leave_alone_notes", "do not merge")
	dv("1462-0004", "baseline_arm_1", "leave_alone_current_rek", "2019/36045")
	dv("1462-0004", "baseline_arm_1", "leave_alone_mrn", "MRN-77")
	dv("1490-0004", "baseline_arm_1", "record_id", "1490-0004")
	dv("1490-0004", "baseline_arm_1", "leave_alone_notes", "second review")

	aw := audit.NewWriter(s.DB, string(s.Dialect))
	must(aw.EnsureYear(ctx)) // record exports write audit rows

	return &fionaFixture{h: &Handler{Store: s, Cfg: cfg, Audit: aw}, s: s, pid: pid}
}

// postRaw posts a raw form body — the cURL examples' DATA strings verbatim,
// with only the token substituted (the fixture's own credential).
func postRaw(t *testing.T, h *Handler, raw string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// --- Export Project Info (cURL example, VISION §Endpoints used by Fiona) ---

// TestFionaExportProject encodes the shell example's DATA string. The
// document asks for "information about the project (name, description, PI,
// REK-number, start/end dates)": those keys must carry the seeded values,
// and every key a naive FIONA parser reads from the sample output must be
// present with a neutral value where nothing is stored (REQ-API-018).
func TestFionaExportProject(t *testing.T) {
	f := newFionaFixture(t)
	code, body := postRaw(t, f.h, "token="+fionaToken+"&content=project&format=json&returnFormat=json")
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 {
		t.Fatalf("rows = %d, want the single project object: %s", len(out), body)
	}
	row := out[0]
	for key, want := range map[string]string{
		"project_name":             "DataTransferProjects",
		"project_title":            "DataTransferProjects",
		"project_pi_name":          "Ansgar Espeland",
		"project_pi_email":         "ansgar@example.org",
		"project_rek_number":       "2019/36045",
		"project_start_date":       "2019-04-11",
		"is_longitudinal":          "1", // events exist (REQ-API-018)
		"project_language":         "English",
		"display_today_now_button": "1",
	} {
		if got := row[key]; got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
	// Keys of the sample output the system does not store: present, neutral.
	for _, key := range []string{"creation_time", "production_time", "in_production",
		"purpose", "purpose_other", "project_notes", "surveys_enabled",
		"record_autonumbering_enabled", "bypass_branching_erase_field_prompt"} {
		if _, ok := row[key]; !ok {
			t.Errorf("project response is missing key %q from the sample output", key)
		}
	}
}

// --- Export Events (PHP example and cURL example) ---

// TestFionaExportEvents encodes content=event&format=json&returnFormat=json.
// The response keys follow the sample output (event_name, arm_num,
// unique_event_name, event_id) in the canonical per-arm order (GD-15).
func TestFionaExportEvents(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{
		"token": {fionaToken}, "content": {"event"},
		"format": {"json"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	want := []struct{ name, unique string }{
		{"Event 1", "event_1_arm_1"},
		{"baseline", "baseline_arm_1"},
		{"M5Y1", "m5y1_arm_1"},
	}
	if len(out) != len(want) {
		t.Fatalf("rows = %d, want %d: %s", len(out), len(want), body)
	}
	for i, w := range want {
		row := out[i]
		if row["event_name"] != w.name || row["unique_event_name"] != w.unique {
			t.Errorf("row %d = %v, want %q / %q", i, row, w.name, w.unique)
		}
		if row["arm_num"] != float64(1) {
			t.Errorf("row %d arm_num = %v, want 1", i, row["arm_num"])
		}
		if _, ok := row["event_id"]; !ok {
			t.Errorf("row %d is missing event_id: %v", i, row)
		}
	}
}

// --- Export Metadata (PHP example) ---

// TestFionaExportMetadata encodes content=metadata&format=json&returnFormat=json:
// one row per field in dictionary order, the first field of the first
// instrument flagged as the record identifier (GD-8), each row naming its
// form.
func TestFionaExportMetadata(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{
		"token": {fionaToken}, "content": {"metadata"},
		"format": {"json"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 10 { // 4 + 4 + 2 seeded fields
		t.Fatalf("rows = %d, want 10: %s", len(out), body)
	}
	first := out[0]
	if first["field_name"] != "record_id" || first["form_name"] != "projects" {
		t.Errorf("first row = %v, want projects.record_id", first)
	}
	if first["record_identifier"] != "Y" {
		t.Errorf("first row record_identifier = %v, want Y", first["record_identifier"])
	}
	forms := map[string]string{} // field_name -> form_name
	for _, row := range out {
		name, _ := row["field_name"].(string)
		forms[name], _ = row["form_name"].(string)
	}
	for name, wantForm := range map[string]string{
		"transfer_mapped_uid": "transfers",
		"leave_alone_notes":   "leave_alone_do_not_touch",
		"project_id":          "projects",
	} {
		if got := forms[name]; got != wantForm {
			t.Errorf("field %s form_name = %q, want %q", name, got, wantForm)
		}
	}
}

// --- generateNextRecordName (PHP example) ---

// TestFionaGenerateNextRecordName encodes the bare call of the PHP example —
// token and content only. With no format or returnFormat the REDCap default
// encoding is csv, so the answer is the CSV rendering of next_record_name:
// one past the greatest seeded 8DISC record (REQ-API-023).
func TestFionaGenerateNextRecordName(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{"token": {fionaToken}, "content": {"generateNextRecordName"}})
	mustStatus(t, code, http.StatusOK, body)
	if want := "next_record_name\n8DISC004\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}

	// The same call with returnFormat=json (the JSON rendering).
	code, body = do(t, f.h, url.Values{
		"token": {fionaToken}, "content": {"generateNextRecordName"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 || out[0]["next_record_name"] != "8DISC004" {
		t.Errorf("rows = %s, want next_record_name 8DISC004", body)
	}
}

// --- formEventMapping (JS example) ---

// TestFionaFormEventMapping encodes content=formEventMapping&format=json&returnFormat=json:
// one row per mapped (form, event) pair, instruments in position order and
// events in the canonical per-arm order shared with content=event (GD-15).
func TestFionaFormEventMapping(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{
		"token": {fionaToken}, "content": {"formEventMapping"},
		"format": {"json"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	want := []struct{ form, unique string }{
		{"projects", "event_1_arm_1"},
		{"leave_alone_do_not_touch", "baseline_arm_1"},
		{"transfers", "event_1_arm_1"},
	}
	if len(out) != len(want) {
		t.Fatalf("rows = %d, want %d: %s", len(out), len(want), body)
	}
	for i, w := range want {
		row := out[i]
		if row["form_name"] != w.form || row["unique_event_name"] != w.unique {
			t.Errorf("row %d = %v, want %q / %q", i, row, w.form, w.unique)
		}
		if row["form_event_mapping"] != "1" {
			t.Errorf("row %d form_event_mapping = %v, want 1", i, row["form_event_mapping"])
		}
		if row["arm_num"] != float64(1) {
			t.Errorf("row %d arm_num = %v, want 1", i, row["arm_num"])
		}
	}
}

// --- exportFieldNames (PHP example) ---

// TestFionaExportFieldNames encodes content=exportFieldNames&format=json&returnFormat=json:
// every field of the project with its form name (REQ-API-022).
func TestFionaExportFieldNames(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{
		"token": {fionaToken}, "content": {"exportFieldNames"},
		"format": {"json"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 10 {
		t.Fatalf("rows = %d, want the 10 seeded fields: %s", len(out), body)
	}
	got := map[string]bool{}
	for _, row := range out {
		name, _ := row["field_name"].(string)
		form, _ := row["form_name"].(string)
		got[name+"/"+form] = true
	}
	for _, pair := range []string{
		"record_id/projects", "project_id/projects",
		"leave_alone_mrn/leave_alone_do_not_touch",
		"transfer_mapped_uid/transfers",
	} {
		if !got[pair] {
			t.Errorf("field/form %s missing from %s", pair, body)
		}
	}
}

// --- Export Records (cURL examples and PHP examples) ---

// TestFionaExportRecordsFlat encodes the "Export Records" DATA string
// verbatim: content=record&action=export&format=json&type=flat with all the
// export* flags. Unknown flags are accepted and dropped (REQ-API-017). Flat
// means one row per (record, event) that holds data (§3.6.2), records in id
// order, events in canonical order.
func TestFionaExportRecordsFlat(t *testing.T) {
	f := newFionaFixture(t)
	data := "token=" + fionaToken + "&content=record&action=export&format=json&type=flat" +
		"&csvDelimiter=&rawOrLabel=raw&rawOrLabelHeaders=raw&exportCheckboxLabel=false" +
		"&exportSurveyFields=false&exportDataAccessGroups=false&returnFormat=json"
	code, body := postRaw(t, f.h, data)
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)

	type wantRow struct{ record, event string }
	want := []wantRow{
		{"1462-0004", "baseline_arm_1"},
		{"1490-0004", "baseline_arm_1"},
		{"8DISC001", "event_1_arm_1"},
		{"8DISC002", "event_1_arm_1"},
		{"8DISC003", "event_1_arm_1"},
	}
	if len(out) != len(want) {
		t.Fatalf("rows = %d, want %d: %s", len(out), len(want), body)
	}
	for i, w := range want {
		row := out[i]
		if row["record_id"] != w.record || row["redcap_event_name"] != w.event {
			t.Errorf("row %d = %v/%v, want %s / %s",
				i, row["record_id"], row["redcap_event_name"], w.record, w.event)
		}
		// The flat redcap_* columns are present on every row (§3.2).
		if _, ok := row["redcap_repeat_instrument"]; !ok {
			t.Errorf("row %d is missing redcap_repeat_instrument: %v", i, row)
		}
		if _, ok := row["redcap_repeat_instance"]; !ok {
			t.Errorf("row %d is missing redcap_repeat_instance: %v", i, row)
		}
	}
	// Values land in their own rows; other events' fields are empty.
	if out[2]["project_id"] != "33" || out[2]["project_active"] != "1" {
		t.Errorf("8DISC001 row lost its projects values: %v", out[2])
	}
	if out[3]["transfer_mapped_uid"] != "1.2.3.4" || out[3]["leave_alone_notes"] != "" {
		t.Errorf("8DISC002 row = %v, want the transfers values and empty leave_alone", out[3])
	}
	if out[0]["leave_alone_mrn"] != "MRN-77" || out[0]["transfer_mapped_uid"] != "" {
		t.Errorf("1462-0004 row = %v, want the leave_alone values", out[0])
	}
}

// TestFionaExportRecordFieldProjection encodes the first PHP example: one
// record and one field (records[0]/fields[0] indexed syntax, REQ-API-015).
// The answer is that record's row carrying exactly the requested value.
func TestFionaExportRecordFieldProjection(t *testing.T) {
	f := newFionaFixture(t)
	code, body := do(t, f.h, url.Values{
		"token":                  {fionaToken},
		"content":                {"record"},
		"format":                 {"json"},
		"type":                   {"flat"},
		"csvDelimiter":           {""},
		"records[0]":             {"8DISC001"},
		"fields[0]":              {"project_id"},
		"rawOrLabel":             {"raw"},
		"rawOrLabelHeaders":      {"raw"},
		"exportCheckboxLabel":    {"false"},
		"exportSurveyFields":     {"false"},
		"exportDataAccessGroups": {"false"},
		"returnFormat":           {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 {
		t.Fatalf("rows = %d, want only 8DISC001: %s", len(out), body)
	}
	if out[0]["project_id"] != "33" {
		t.Errorf("project_id = %v, want 33", out[0]["project_id"])
	}
	if out[0]["redcap_event_name"] != "event_1_arm_1" {
		t.Errorf("redcap_event_name = %v, want event_1_arm_1", out[0]["redcap_event_name"])
	}
	// Fields outside the projection are absent, not empty (REQ-API-024).
	for _, absent := range []string{"project_active", "transfer_mapped_uid"} {
		if _, ok := out[0][absent]; ok {
			t.Errorf("field %s survived the fields[] filter: %v", absent, out[0])
		}
	}
}

// TestFionaExportRecordsFormsEvents encodes the second cURL example verbatim:
// records[], fields[], forms[] and events[] combined (filters intersect,
// REQ-API-024). rewritepixelexclusions names no instrument here — an unknown
// filter value is silently absent, as in REDCap. The two named records carry
// leave_alone values at baseline_arm_1 only.
func TestFionaExportRecordsFormsEvents(t *testing.T) {
	f := newFionaFixture(t)
	data := "token=" + fionaToken + "&content=record&action=export&format=json&type=flat" +
		"&csvDelimiter=&records[0]=1462-0004&records[1]=1490-0004" +
		"&fields[0]=leave_alone_notes&fields[1]=leave_alone_current_rek" +
		"&fields[2]=leave_alone_mrn&fields[3]=leave_alone_do_not_touch_complete" +
		"&forms[0]=leave_alone_do_not_touch&forms[1]=projects&forms[2]=rewritepixelexclusions" +
		"&events[0]=event_1_arm_1&events[1]=baseline_arm_1&events[2]=m5y1_arm_1" +
		"&rawOrLabel=raw&rawOrLabelHeaders=raw&exportCheckboxLabel=false" +
		"&exportSurveyFields=false&exportDataAccessGroups=false&returnFormat=json"
	code, body := postRaw(t, f.h, data)
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 2 { // both records hold data at baseline_arm_1 only
		t.Fatalf("rows = %d, want 2: %s", len(out), body)
	}
	if out[0]["leave_alone_notes"] != "do not merge" ||
		out[0]["leave_alone_current_rek"] != "2019/36045" ||
		out[0]["leave_alone_mrn"] != "MRN-77" {
		t.Errorf("1462-0004 row = %v", out[0])
	}
	if out[1]["leave_alone_notes"] != "second review" {
		t.Errorf("1490-0004 row = %v", out[1])
	}
	for i, row := range out {
		if row["redcap_event_name"] != "baseline_arm_1" {
			t.Errorf("row %d event = %v, want baseline_arm_1 (events[] filter)", i, row["redcap_event_name"])
		}
	}
}

// TestFionaFilterLogicTransferLookup encodes the second PHP example: the
// transfer lookup by SOP instance UID — forms[0]=transfers with
// filterLogic=[transfer_mapped_uid]="<uid>". The filter keeps whole records
// (REQ-API-025): only 8DISC002 carries the matching UID.
func TestFionaFilterLogicTransferLookup(t *testing.T) {
	f := newFionaFixture(t)
	call := func(uid string) (int, string) {
		return do(t, f.h, url.Values{
			"token":        {fionaToken},
			"content":      {"record"},
			"format":       {"json"},
			"type":         {"flat"},
			"csvDelimiter": {""},
			"forms[0]":     {"transfers"},
			"rawOrLabel":   {"raw"},
			"filterLogic":  {`[transfer_mapped_uid]="` + uid + `"`},
			"returnFormat": {"json"},
		})
	}

	code, body := call("1.2.3.4")
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 { // only 8DISC002 carries the UID; its data is at event_1_arm_1
		t.Fatalf("rows = %d, want the single row of the matching record: %s", len(out), body)
	}
	for i, row := range out {
		if row["transfer_mapped_uid"] != "1.2.3.4" || row["transfer_project_name"] != "StudyB" {
			t.Errorf("row %d = %v, want the matching transfer values", i, row)
		}
	}

	// A UID no record carries: the empty array, still 200 (REQ-API-025).
	code, body = call("5.5.5.5")
	mustStatus(t, code, http.StatusOK, body)
	if out := decodeJSON(t, body); len(out) != 0 {
		t.Errorf("rows = %s, want the empty array", body)
	}

	// A malformed expression is a client error caught before any output.
	code, body = call(`1.2.3.4" or [`)
	if code != http.StatusBadRequest {
		t.Errorf("malformed filterLogic: status = %d, want 400 (body %q)", code, body)
	}
}

// --- the uniform error contract on every example content ---

// TestFionaInvalidTokenIsUniform checks that every content of the call
// examples answers the same way to a bad token: 401 with "Invalid token"
// (REQ-API-011) — FIONA's callers key off exactly this body.
func TestFionaInvalidTokenIsUniform(t *testing.T) {
	f := newFionaFixture(t)
	contents := []string{
		"project", "event", "metadata", "formEventMapping",
		"exportFieldNames", "generateNextRecordName", "record",
	}
	for _, content := range contents {
		t.Run(content, func(t *testing.T) {
			code, body := do(t, f.h, url.Values{
				"token": {"not-a-real-token"}, "content": {content},
				"format": {"json"}, "returnFormat": {"json"},
			})
			if code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (body %q)", code, body)
			}
			if !strings.Contains(body, "Invalid token") {
				t.Errorf("body = %q, want the uniform \"Invalid token\" (REQ-API-011)", body)
			}
		})
	}
}
