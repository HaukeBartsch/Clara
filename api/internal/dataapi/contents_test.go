package dataapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// nextRecordName is pure logic (REQ-DB-007, REQ-API-023), so it is tested
// without a store: both pattern styles, collision avoidance, gap filling,
// width preservation, and extension past the pattern's digit width.
func TestNextRecordName(t *testing.T) {
	cases := []struct {
		name     string
		pattern  string
		existing []string
		want     string
	}{
		{"digit placeholder, none used", "8DISC[0-9][0-9][0-9]", nil, "8DISC001"},
		{"digit placeholder, next after run", "8DISC[0-9][0-9][0-9]", []string{"8DISC001", "8DISC002", "8DISC041"}, "8DISC003"},
		{"digit placeholder, fills gap", "8DISC[0-9][0-9][0-9]", []string{"8DISC001", "8DISC003"}, "8DISC002"},
		{
			"digit placeholder, extends past width", "8DISC[0-9][0-9][0-9]",
			[]string{"8DISC001", "8DISC002", "8DISC003", "8DISC004", "8DISC005", "8DISC006", "8DISC007", "8DISC008", "8DISC009"},
			"8DISC010",
		},
		{"digit placeholder, ignores unrelated ids", "8DISC[0-9][0-9][0-9]", []string{"foo", "8DISC999"}, "8DISC001"},
		{"counter prefix, none used", "0001_01", nil, "0001_01"},
		{"counter prefix, next after run", "0001_01", []string{"0001_01", "0002_01"}, "0003_01"},
		{"counter prefix, width preserved", "00001_02", []string{"00001_02", "00002_02"}, "00003_02"},
		{
			"counter prefix, extends past width", "0001_01",
			[]string{"0001_01", "0002_01", "0003_01", "0004_01", "0005_01"},
			"0006_01",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := make(map[string]bool, len(tc.existing))
			for _, e := range tc.existing {
				ex[e] = true
			}
			if got := nextRecordName(tc.pattern, ex); got != tc.want {
				t.Fatalf("nextRecordName(%q, %v) = %q, want %q", tc.pattern, tc.existing, got, tc.want)
			}
		})
	}
}

// splitChoices expands the stored code$label##code$label encoding (§3.4).
func TestSplitChoices(t *testing.T) {
	codes, labels := splitChoices("1$Pending##2$Done##3$Cancelled")
	if codes != "1,2,3" {
		t.Errorf("codes = %q, want %q", codes, "1,2,3")
	}
	if labels != "Pending,Done,Cancelled" {
		t.Errorf("labels = %q, want %q", labels, "Pending,Done,Cancelled")
	}
	// A code with no label falls back to the code as its own label.
	if c, l := splitChoices("9"); c != "9" || l != "9" {
		t.Errorf("splitChoices(%q) = %q,%q, want 9,9", "9", c, l)
	}
	if c, l := splitChoices(""); c != "" || l != "" {
		t.Errorf("splitChoices(%q) = %q,%q, want empty", "", c, l)
	}
}

// --- end-to-end against a seeded store ---

// testHandler seeds one project (arms/events/instruments/fields/mapping), a
// full-permission data user, a read-only user, and two existing records, then
// returns a ready Handler plus the two tokens.
func testHandler(t *testing.T) (h *Handler, full, readonly string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(dir, "test.sqlite"),
		AnonSalt:     "test-salt",
	}
	s, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	pid, err := s.CreateProject(ctx, &db.Project{ProjectName: "proj-" + t.Name(), ParticipantNames: "8DISC[0-9][0-9][0-9]"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	armID, err := s.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	evBase, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1", Period: sql.NullInt64{Int64: 0, Valid: true}})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	evFol, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "followup", UniqueEventName: "followup_arm_1", Period: sql.NullInt64{Int64: 90, Valid: true}})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	// No timepoint: exercises the trailing no-timepoint slot in the canonical
	// per-arm event order (GD-15). Its id is not needed by the mappings.
	if _, err := s.AddEvent(ctx, &db.Event{ProjectID: pid, ArmID: armID, EventName: "screening", UniqueEventName: "screening_arm_1"}); err != nil {
		t.Fatalf("AddEvent screening: %v", err)
	}
	intakeID, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	visitID, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "visit"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	if _, err := s.AddField(ctx, &db.Field{ProjectID: pid, InstrumentID: intakeID, FieldName: "record_id", FieldType: "text"}); err != nil {
		t.Fatalf("seed AddField record_id: %v", err)
	}
	if _, err := s.AddField(ctx, &db.Field{ProjectID: pid, InstrumentID: intakeID, FieldName: "age", FieldType: "text"}); err != nil {
		t.Fatalf("seed AddField age: %v", err)
	}
	if _, err := s.AddField(ctx, &db.Field{ProjectID: pid, InstrumentID: visitID, FieldName: "visit_flag", FieldType: "text"}); err != nil {
		t.Fatalf("seed AddField visit_flag: %v", err)
	}

	// Mappings: intake→{baseline,followup}, visit→{followup}.
	must(s.SetInstrumentEventsForArm(ctx, pid, armID, []db.InstrumentEvent{
		{InstrumentID: intakeID, EventID: evBase},
		{InstrumentID: intakeID, EventID: evFol},
		{InstrumentID: visitID, EventID: evFol},
	}))

	// Full-permission data user (admin), and a read-only user via a role.
	fullID, err := s.CreateUser(ctx, &db.User{Email: "full@example.org", DisplayName: "Full", Enabled: true, AuthSource: "local", IsAdmin: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	roID, err := s.CreateUser(ctx, &db.User{Email: "ro@example.org", DisplayName: "RO", Enabled: true, AuthSource: "local"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	roRole, err := s.CreateRole(ctx, &db.Role{ProjectID: pid, RoleName: "reader"}, []db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	full = "tok-full"
	if _, err := s.AddAssignment(ctx, &db.Assignment{UserID: fullID, ProjectID: pid, Token: full}); err != nil {
		t.Fatalf("seed AddAssignment full: %v", err)
	}
	readonly = "tok-ro"
	if _, err := s.AddAssignment(ctx, &db.Assignment{UserID: roID, ProjectID: pid, Token: readonly, RoleID: sql.NullInt64{Int64: roRole, Valid: true}}); err != nil {
		t.Fatalf("seed AddAssignment ro: %v", err)
	}

	// Two existing records so generateNextRecordName must return 8DISC003.
	must(s.AddDataValue(ctx, &db.DataValue{ProjectID: pid, RecordID: "8DISC001", UniqueEventName: "baseline_arm_1", FieldName: "record_id", Value: "8DISC001"}))
	must(s.AddDataValue(ctx, &db.DataValue{ProjectID: pid, RecordID: "8DISC002", UniqueEventName: "baseline_arm_1", FieldName: "record_id", Value: "8DISC002"}))

	return &Handler{Store: s, Cfg: cfg}, full, readonly
}

// do posts a form-encoded data-API call and returns the status and body.
func do(t *testing.T, h *Handler, form url.Values) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func mustStatus(t *testing.T, got, want int, body string) {
	t.Helper()
	if got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, body)
	}
}

// decodeJSON decodes a JSON content response into a slice of row maps.
func decodeJSON(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad json: %v / %s", err, body)
	}
	return out
}

func TestContentProject(t *testing.T) {
	h, full, _ := testHandler(t)
	code, body := do(t, h, url.Values{
		"token": {full}, "content": {"project"}, "format": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 {
		t.Fatalf("want 1 row, got %d", len(out))
	}
	row := out[0]
	// Neutral values are present rather than omitted (REQ-API-018): keys the
	// system does not store must still appear in the response.
	for _, key := range []string{
		"project_id", "project_name", "surveys_enabled", "randomization_enabled",
		"creation_time", "project_language", "project_pi_lastname",
		"is_longitudinal", "display_today_now_button",
	} {
		if _, ok := row[key]; !ok {
			t.Fatalf("project response is missing key %q", key)
		}
	}
	// Concrete values the seed establishes.
	wantName := "proj-" + t.Name()
	if row["project_name"] != wantName || row["project_title"] != wantName {
		t.Errorf("project_name/title = %v / %v, want %q", row["project_name"], row["project_title"], wantName)
	}
	if row["project_language"] != "English" {
		t.Errorf("project_language = %v, want English", row["project_language"])
	}
	if pid, _ := row["project_id"].(string); pid == "" {
		t.Errorf("project_id = %v, want a non-empty string", row["project_id"])
	}
	// Events exist, so the project is longitudinal (REQ-API-018).
	if row["is_longitudinal"] != "1" {
		t.Errorf("is_longitudinal = %v, want 1 (events exist)", row["is_longitudinal"])
	}
	// No surveys are seeded, so surveys_enabled is the neutral empty value.
	if row["surveys_enabled"] != "" {
		t.Errorf("surveys_enabled = %v, want empty (no surveys)", row["surveys_enabled"])
	}
}

func TestContentEvent(t *testing.T) {
	h, full, _ := testHandler(t)
	code, body := do(t, h, url.Values{"token": {full}, "content": {"event"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	// Canonical per-arm order (GD-15): timepoint events by period, then the
	// no-timepoint event.
	want := []string{"baseline_arm_1", "followup_arm_1", "screening_arm_1"}
	if len(out) != len(want) {
		t.Fatalf("want %d events, got %d", len(want), len(out))
	}
	for i, w := range want {
		if out[i]["unique_event_name"] != w {
			t.Errorf("event[%d].unique_event_name = %v, want %q", i, out[i]["unique_event_name"], w)
		}
		if out[i]["arm_num"] != float64(1) {
			t.Errorf("event[%d].arm_num = %v, want 1", i, out[i]["arm_num"])
		}
		if id, _ := out[i]["event_id"].(float64); id <= 0 {
			t.Errorf("event[%d].event_id = %v, want > 0", i, out[i]["event_id"])
		}
	}
}

func TestContentMetadata(t *testing.T) {
	h, full, _ := testHandler(t)
	code, body := do(t, h, url.Values{"token": {full}, "content": {"metadata"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 3 {
		t.Fatalf("want 3 field rows, got %d", len(out))
	}
	// Instrument order, then position: intake{record_id, age}, visit{visit_flag}.
	if out[0]["field_name"] != "record_id" || out[1]["field_name"] != "age" || out[2]["field_name"] != "visit_flag" {
		t.Fatalf("field order = %v %v %v, want record_id, age, visit_flag",
			out[0]["field_name"], out[1]["field_name"], out[2]["field_name"])
	}
	if out[0]["form_name"] != "intake" || out[2]["form_name"] != "visit" {
		t.Errorf("form_name = %v / %v, want intake / visit", out[0]["form_name"], out[2]["form_name"])
	}
	// GD-8: exactly the first field is the record identifier.
	if out[0]["record_identifier"] != "Y" {
		t.Errorf("first field record_identifier = %v, want Y", out[0]["record_identifier"])
	}
	if out[1]["record_identifier"] != "" || out[2]["record_identifier"] != "" {
		t.Errorf("non-first record_identifier = %v / %v, want empty",
			out[1]["record_identifier"], out[2]["record_identifier"])
	}
	for _, r := range out {
		if r["field_type"] != "text" {
			t.Errorf("field %v type = %v, want text", r["field_name"], r["field_type"])
		}
		if r["required_field"] != "" {
			t.Errorf("field %v required_field = %v, want empty (none required)", r["field_name"], r["required_field"])
		}
	}
}

func TestContentFormEventMapping(t *testing.T) {
	h, full, _ := testHandler(t)
	code, body := do(t, h, url.Values{"token": {full}, "content": {"formEventMapping"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	// Only the checked (form, event) pairs appear, in instrument order then
	// canonical event order; every row carries the mapping flag.
	want := []struct{ form, event string }{
		{"intake", "baseline_arm_1"},
		{"intake", "followup_arm_1"},
		{"visit", "followup_arm_1"},
	}
	if len(out) != len(want) {
		t.Fatalf("want %d mappings, got %d / %s", len(want), len(out), body)
	}
	for i, w := range want {
		if out[i]["form_name"] != w.form || out[i]["unique_event_name"] != w.event {
			t.Errorf("mapping[%d] = %v/%v, want %v/%v",
				i, out[i]["form_name"], out[i]["unique_event_name"], w.form, w.event)
		}
		if out[i]["form_event_mapping"] != "1" {
			t.Errorf("mapping[%d].form_event_mapping = %v, want 1", i, out[i]["form_event_mapping"])
		}
	}
}

func TestContentExportFieldNames(t *testing.T) {
	h, full, _ := testHandler(t)

	// No forms[] filter returns every field (REQ-API-022).
	code, body := do(t, h, url.Values{"token": {full}, "content": {"exportFieldNames"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	if all := decodeJSON(t, body); len(all) != 3 {
		t.Fatalf("want 3 fields, got %d / %s", len(all), body)
	}

	// A forms[] filter restricts to that form (REQ-API-022).
	code, body = do(t, h, url.Values{
		"token": {full}, "content": {"exportFieldNames"}, "format": {"json"},
		"forms": {"intake"},
	})
	mustStatus(t, code, http.StatusOK, body)
	intake := decodeJSON(t, body)
	if len(intake) != 2 {
		t.Fatalf("want 2 intake fields, got %d / %s", len(intake), body)
	}
	for _, r := range intake {
		if r["form_name"] != "intake" {
			t.Errorf("filtered form_name = %v, want intake", r["form_name"])
		}
	}
}

func TestGenerateNextRecordName(t *testing.T) {
	h, full, _ := testHandler(t)
	code, body := do(t, h, url.Values{"token": {full}, "content": {"generateNextRecordName"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	out := decodeJSON(t, body)
	if len(out) != 1 {
		t.Fatalf("want 1 row, got %d", len(out))
	}
	// 8DISC001 and 8DISC002 exist, so the next free name is 8DISC003 (REQ-API-023).
	if out[0]["next_record_name"] != "8DISC003" {
		t.Errorf("next_record_name = %v, want 8DISC003", out[0]["next_record_name"])
	}
}

// --- response format contracts (REQ-API-013, REQ-API-029) ---

func TestCSVOutput(t *testing.T) {
	h, full, _ := testHandler(t)

	// No format → the REDCap default csv (REQ-API-013).
	code, body := do(t, h, url.Values{"token": {full}, "content": {"event"}})
	mustStatus(t, code, http.StatusOK, body)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header + 3 rows, got %d lines: %s", len(lines), body)
	}
	if lines[0] != "event_name,arm_num,unique_event_name,event_id" {
		t.Errorf("csv header = %q", lines[0])
	}
	wantPrefix := []string{"baseline,1,baseline_arm_1,", "followup,1,followup_arm_1,", "screening,1,screening_arm_1,"}
	for i, p := range wantPrefix {
		if !strings.HasPrefix(lines[i+1], p) {
			t.Errorf("csv row %d = %q, want prefix %q", i+1, lines[i+1], p)
		}
	}

	// A single-character csvDelimiter is honored (REQ-API-029).
	code, body = do(t, h, url.Values{"token": {full}, "content": {"event"}, "csvDelimiter": {"|"}})
	mustStatus(t, code, http.StatusOK, body)
	if first := strings.SplitN(body, "\n", 2)[0]; first != "event_name|arm_num|unique_event_name|event_id" {
		t.Errorf("delimiter header = %q, want pipes", first)
	}
}

func TestReturnFormatWins(t *testing.T) {
	h, full, _ := testHandler(t)
	// format asks for csv, but a present returnFormat=json wins (REQ-API-013).
	code, body := do(t, h, url.Values{
		"token": {full}, "content": {"event"}, "format": {"csv"}, "returnFormat": {"json"},
	})
	mustStatus(t, code, http.StatusOK, body)
	var out []map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("returnFormat=json should yield json: %v\n%s", err, body)
	}
}

// --- error, permission, and rate-limit contracts ---

func TestInvalidToken(t *testing.T) {
	h, _, _ := testHandler(t)
	// A missing token and an unknown token both render the uniform 401
	// (REQ-API-011), so callers cannot probe for valid ones.
	code, body := do(t, h, url.Values{"content": {"project"}, "format": {"json"}})
	mustStatus(t, code, http.StatusUnauthorized, body)
	if !strings.Contains(body, "Invalid token") {
		t.Errorf("missing-token body %q, want \"Invalid token\"", body)
	}
	code, body = do(t, h, url.Values{"token": {"nope"}, "content": {"project"}, "format": {"json"}})
	mustStatus(t, code, http.StatusUnauthorized, body)
	if !strings.Contains(body, "Invalid token") {
		t.Errorf("unknown-token body %q, want \"Invalid token\"", body)
	}
}

func TestReadOnlyPermission(t *testing.T) {
	h, _, readonly := testHandler(t)
	// read_only reaches the read-only contents…
	code, body := do(t, h, url.Values{"token": {readonly}, "content": {"event"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	// …but generateNextRecordName needs view_edit → uniform 403 (REQ-API-007).
	code, body = do(t, h, url.Values{"token": {readonly}, "content": {"generateNextRecordName"}, "format": {"json"}})
	mustStatus(t, code, http.StatusForbidden, body)
	if !strings.Contains(body, "Permission denied") {
		t.Errorf("denied body %q, want \"Permission denied\"", body)
	}
}

func TestInvalidContent(t *testing.T) {
	h, full, _ := testHandler(t)
	// record is the next slice; until it lands it is a uniform 400.
	code, body := do(t, h, url.Values{"token": {full}, "content": {"record"}, "action": {"export"}, "format": {"json"}})
	mustStatus(t, code, http.StatusBadRequest, body)
	if !strings.Contains(body, "Invalid content") {
		t.Errorf("record body %q, want \"Invalid content\"", body)
	}
	// Unknown and missing content are 400 as well.
	code, body = do(t, h, url.Values{"token": {full}, "content": {"bogus"}, "format": {"json"}})
	mustStatus(t, code, http.StatusBadRequest, body)
	if !strings.Contains(body, "Invalid content") {
		t.Errorf("bogus body %q, want \"Invalid content\"", body)
	}
	code, body = do(t, h, url.Values{"token": {full}, "format": {"json"}})
	mustStatus(t, code, http.StatusBadRequest, body)
	if !strings.Contains(body, "Invalid content") {
		t.Errorf("missing-content body %q, want \"Invalid content\"", body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _, _ := testHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "http://test/api/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestRateLimit(t *testing.T) {
	h, full, _ := testHandler(t)
	limited := &Handler{Store: h.Store, Cfg: h.Cfg, Limiter: NewRateLimiter(1)}
	code, body := do(t, limited, url.Values{"token": {full}, "content": {"project"}, "format": {"json"}})
	mustStatus(t, code, http.StatusOK, body)
	// The same token is now over its 1-call-per-minute budget (REQ-API-038).
	code, body = do(t, limited, url.Values{"token": {full}, "content": {"project"}, "format": {"json"}})
	mustStatus(t, code, http.StatusTooManyRequests, body)
	if !strings.Contains(body, "Rate limit exceeded") {
		t.Errorf("body %q, want \"Rate limit exceeded\"", body)
	}
}
