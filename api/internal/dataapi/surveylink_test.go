package dataapi

// Tests for survey-link tokens on the data API (API_Endpoints_Design.md
// §3.10): the two admitted calls and their scope, the uniform 403 for
// everything else, the revoked link refused on every call (REQ-AUTH-040),
// and the survey_submitted audit trail for accepted and rejected
// submissions (REQ-AUD-021).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// surveyLinkFixture is the record fixture plus a live link on
// (8DISC001, demo) — the pair its public survey page may render and fill.
func surveyLinkFixture(t *testing.T) (*recordFixture, *db.SurveyLink) {
	t.Helper()
	f := newRecordFixture(t)
	ctx := context.Background()
	ins, err := f.s.GetInstrumentByName(ctx, f.pid, "demo")
	if err != nil || ins == nil {
		t.Fatalf("GetInstrumentByName(demo): %v", err)
	}
	link := &db.SurveyLink{ProjectID: f.pid, RecordID: "8DISC001", InstrumentID: ins.ID}
	if _, err := f.s.CreateSurveyLink(ctx, link); err != nil {
		t.Fatalf("CreateSurveyLink: %v", err)
	}
	return f, link
}

// surveyAudit is one survey_submitted row as stored.
type surveyAudit struct {
	Token        string
	TargetRecord string
	UserID       sql.NullInt64
	Details      struct {
		Status     string `json:"status"`
		Reason     *string
		RecordID   string `json:"record_id"`
		Instrument string
		Fields     []struct {
			Field, Old, New string
		}
	}
}

// lastSurveyEntry returns the newest survey_submitted row, or nil when the
// call wrote none.
func lastSurveyEntry(t *testing.T, f *recordFixture) *surveyAudit {
	t.Helper()
	table := "audit_events" // stable name: SQLite view / MariaDB partitioned table
	var (
		e       surveyAudit
		details string
	)
	err := f.s.DB.QueryRow(`SELECT token, target_record, user_id, details FROM `+table+
		` WHERE event_type = 'survey_submitted' ORDER BY id DESC LIMIT 1`).
		Scan(&e.Token, &e.TargetRecord, &e.UserID, &details)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		t.Fatalf("read survey_submitted: %v", err)
	}
	if err := json.Unmarshal([]byte(details), &e.Details); err != nil {
		t.Fatalf("survey_submitted details %q: %v", details, err)
	}
	return &e
}

// content=metadata over a link renders the link's instrument and no other
// (REQ-API-083): forms[] may name it, never another.
func TestSurveyLinkMetadataScopedToInstrument(t *testing.T) {
	f, link := surveyLinkFixture(t)

	code, body := f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"}, "returnFormat": {"json"}})
	mustStatus(t, code, 200, body)
	rows := decodeJSON(t, body)
	if len(rows) != 7 { // demo's seven fields; calc's "total" stays unseen
		t.Fatalf("metadata rows = %d, want demo's 7 fields: %v", len(rows), rows)
	}
	for _, r := range rows {
		if r["form_name"] != "demo" {
			t.Errorf("metadata leaked form_name = %v for field %v", r["form_name"], r["field_name"])
		}
	}
	// The dictionary position of the identifier survives the filter (GD-8).
	if rows[0]["field_name"] != "record_id" || rows[0]["record_identifier"] != "Y" {
		t.Errorf("first row = %v, want the record identifier", rows[0])
	}

	code, body = f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"}, "forms": {"demo"}})
	mustStatus(t, code, 200, body)

	code, body = f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"}, "forms": {"calc"}})
	mustStatus(t, code, 403, body)
	if !strings.Contains(body, "Permission denied") {
		t.Errorf("body = %s, want Permission denied", body)
	}

	// A forms[] list that names the link's instrument among others still
	// reaches past it: the render is the link's instrument or nothing.
	code, _ = f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"},
		"forms[0]": {"demo"}, "forms[1]": {"calc"}})
	mustStatus(t, code, 403, "")
}

// The permitted submission fills its (record, instrument) and audits the
// old/new values with the link in the token column.
func TestSurveyLinkImportFillsItsRecord(t *testing.T) {
	f, link := surveyLinkFixture(t)

	res := f.importRows(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "44", "notes": "many thanks",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("import result = %v, want one updated row", res)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "44" {
		t.Errorf("stored age = %q, want 44", got)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "notes"); got != "many thanks" {
		t.Errorf("stored notes = %q", got)
	}

	e := lastSurveyEntry(t, f)
	if e == nil {
		t.Fatal("no survey_submitted audit row")
	}
	if e.Token != link.Token {
		t.Errorf("token column = %q, want the link token", e.Token)
	}
	if e.TargetRecord != "8DISC001" {
		t.Errorf("target_record = %q, want 8DISC001", e.TargetRecord)
	}
	if e.UserID.Valid {
		t.Errorf("user_id = %d for an anonymous submission, want NULL", e.UserID.Int64)
	}
	d := e.Details
	if d.Status != "success" {
		t.Errorf("status = %q, want success", d.Status)
	}
	if d.Reason != nil {
		t.Errorf("reason = %q for a success, want null", *d.Reason)
	}
	if d.RecordID != "8DISC001" || d.Instrument != "demo" {
		t.Errorf("details identify %s/%s, want 8DISC001/demo", d.RecordID, d.Instrument)
	}
	if strings.Contains(d.Instrument, link.Token) {
		t.Error("the link token must never appear in details (REQ-AUTH-041)")
	}
	want := [][3]string{{"age", "42", "44"}, {"notes", "call me", "many thanks"}}
	if len(d.Fields) != len(want) {
		t.Fatalf("fields = %v, want %v", d.Fields, want)
	}
	for i, w := range want {
		got := d.Fields[i]
		if got.Field != w[0] || got.Old != w[1] || got.New != w[2] {
			t.Errorf("fields[%d] = %+v, want %v", i, got, w)
		}
	}
}

// A rejected submission is audited too, with the reason and no field
// changes (REQ-AUD-021).
func TestSurveyLinkSubmissionFailureAudited(t *testing.T) {
	f, link := surveyLinkFixture(t)

	res := f.importRows(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "not-a-number",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importInvalid {
		t.Fatalf("import result = %v, want a validation-error row", res)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("stored age = %q, want the unchanged 42", got)
	}

	e := lastSurveyEntry(t, f)
	if e == nil {
		t.Fatal("a rejected submission must still write survey_submitted")
	}
	d := e.Details
	if d.Status != "failure" {
		t.Errorf("status = %q, want failure", d.Status)
	}
	if d.Reason == nil || !strings.Contains(*d.Reason, "age") {
		t.Errorf("reason = %v, want one naming the offending field", d.Reason)
	}
	if len(d.Fields) != 0 {
		t.Errorf("fields = %v for a rejected submission, want []", d.Fields)
	}
	if e.Token != link.Token || e.TargetRecord != "8DISC001" {
		t.Errorf("entry identifies %q/%q", e.Token, e.TargetRecord)
	}
}

// Another record or another instrument is a permission mismatch — 403 for
// the whole call, with nothing written (REQ-API-083).
func TestSurveyLinkImportOtherRecordOrFormForbidden(t *testing.T) {
	f, link := surveyLinkFixture(t)

	cases := map[string]map[string]string{
		"another record":     {"record_id": "8DISC002", "form_name": "demo", "event_name": "baseline_arm_1", "age": "9"},
		"another instrument": {"record_id": "8DISC001", "form_name": "calc", "event_name": "baseline_arm_1", "total": "9"},
	}
	for name, row := range cases {
		code, body := f.call(t, importForm(link.Token, row))
		if code != 403 || !strings.Contains(body, "Permission denied") {
			t.Errorf("%s: code = %d body = %s, want 403 Permission denied", name, code, body)
		}
	}
	if got := storedValue(t, f, "8DISC002", "baseline_arm_1", "age"); got != "30" {
		t.Errorf("8DISC002 age = %q, want the untouched 30", got)
	}
	if e := lastSurveyEntry(t, f); e != nil {
		t.Errorf("a forbidden call wrote %+v, want no survey_submitted entry", e)
	}
}

// Every other content — export and delete included — is the uniform 403.
func TestSurveyLinkEverythingElseForbidden(t *testing.T) {
	f, link := surveyLinkFixture(t)

	cases := []url.Values{
		{"content": {"record"}}, // no action means export
		{"content": {"record"}, "action": {"export"}},
		{"content": {"record"}, "action": {"delete"}},
		{"content": {"project"}},
		{"content": {"event"}},
		{"content": {"formEventMapping"}},
		{"content": {"exportFieldNames"}},
		{"content": {"generateNextRecordName"}},
		{"content": {"nonsense"}},
	}
	for _, extra := range cases {
		form := url.Values{"token": {link.Token}, "returnFormat": {"json"}}
		for k, vs := range extra {
			form[k] = vs
		}
		code, body := f.call(t, form)
		if code != 403 || !strings.Contains(body, "Permission denied") {
			t.Errorf("%v: code = %d body = %s, want 403 Permission denied", extra, code, body)
		}
	}

	// A request with no content at all is malformed, not forbidden.
	code, _ := f.call(t, url.Values{"token": {link.Token}})
	mustStatus(t, code, 400, "")
}

// A revoked link is rejected on every call — both permitted ones included
// (REQ-AUTH-040).
func TestSurveyLinkRevokedRejectedOnBothCalls(t *testing.T) {
	f, link := surveyLinkFixture(t)

	// Sanity: live before the revocation.
	if code, body := f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"}}); code != 200 {
		t.Fatalf("live link metadata: code = %d body = %s, want 200", code, body)
	}

	ctx := context.Background()
	if err := f.s.RevokeSurveyLink(ctx, f.pid, "8DISC001", link.InstrumentID); err != nil {
		t.Fatalf("RevokeSurveyLink: %v", err)
	}

	code, body := f.call(t, url.Values{"token": {link.Token}, "content": {"metadata"}, "returnFormat": {"json"}})
	mustStatus(t, code, 401, body)
	if !strings.Contains(body, "Invalid token") {
		t.Errorf("metadata body = %s, want Invalid token", body)
	}

	code, body = f.call(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1", "age": "45",
	}))
	mustStatus(t, code, 401, body)
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("a revoked link changed age to %q", got)
	}
	if e := lastSurveyEntry(t, f); e != nil {
		t.Errorf("a revoked link wrote %+v, want no survey_submitted entry", e)
	}
}

// In an analysis-mode project the permitted import is rejected too — with
// the mode's own message (GD-20, REQ-API-109).
func TestSurveyLinkAnalysisModeRejectsImport(t *testing.T) {
	f, link := surveyLinkFixture(t)
	ctx := context.Background()

	tx, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := f.s.SetProjectModeTx(ctx, tx, f.pid, "analysis"); err != nil {
		t.Fatalf("SetProjectModeTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	code, body := f.call(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1", "age": "46",
	}))
	mustStatus(t, code, 403, body)
	if !strings.Contains(body, "Project in analysis mode") {
		t.Errorf("body = %s, want the analysis-mode message", body)
	}
}

// A submission that creates its record leaves it without an author and
// without a data access group: no account stands behind a link.
func TestSurveyLinkNewRecordHasNoAuthor(t *testing.T) {
	f := newRecordFixture(t)
	ctx := context.Background()
	ins, err := f.s.GetInstrumentByName(ctx, f.pid, "demo")
	if err != nil || ins == nil {
		t.Fatalf("GetInstrumentByName(demo): %v", err)
	}
	link := &db.SurveyLink{ProjectID: f.pid, RecordID: "8DISC077", InstrumentID: ins.ID}
	if _, err := f.s.CreateSurveyLink(ctx, link); err != nil {
		t.Fatalf("CreateSurveyLink: %v", err)
	}

	res := f.importRows(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC077", "form_name": "demo", "event_name": "baseline_arm_1", "age": "51",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importAdded {
		t.Fatalf("import result = %v, want one added row", res)
	}

	var createdBy, group sql.NullInt64
	err = f.s.DB.QueryRow(`SELECT created_by, dag_group_id FROM record_entities WHERE project_id = ? AND record_id = ?`,
		f.pid, "8DISC077").Scan(&createdBy, &group)
	if err != nil {
		t.Fatalf("read record entity: %v", err)
	}
	if createdBy.Valid {
		t.Errorf("created_by = %d, want NULL for an anonymous submission", createdBy.Int64)
	}
	if group.Valid {
		t.Errorf("dag_group_id = %d, want no group for a survey record", group.Int64)
	}
}

// A link row that smuggles a field of another instrument is rejected as a
// row-level failure (security finding F3): the (record, form) pin means this
// instrument only, and an anonymous respondent must not reach clinician data
// elsewhere in the record. The whole row writes nothing.
func TestSurveyLinkCannotWriteForeignInstrumentFields(t *testing.T) {
	f, link := surveyLinkFixture(t)

	res := f.importRows(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "44", "total": "999", // total belongs to the calc instrument
	}))
	if len(res) != 1 || res[0].ImportRecordID != importInvalid {
		t.Fatalf("import result = %v, want a validation-error row", res)
	}
	if !strings.Contains(res[0].ImportFormName, "total") {
		t.Errorf("error = %q, want one naming the foreign field", res[0].ImportFormName)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("age = %q — a rejected row must write nothing, not even its own fields", got)
	}
	if e := lastSurveyEntry(t, f); e == nil || e.Details.Status != "failure" {
		t.Errorf("want a failure survey_submitted entry, got %+v", e)
	}
}

// A submission is decided by `data` too (REQ-API-012): with neither action nor
// form_name the row still fills the link's (record, instrument) — and nothing
// else.
func TestSurveyLinkSubmissionDecidedByData(t *testing.T) {
	f, link := surveyLinkFixture(t)

	code, body := f.call(t, url.Values{
		"token": {link.Token}, "content": {"record"}, "returnFormat": {"json"},
		"data": {`[{"record_id":"8DISC001","redcap_event_name":"baseline_arm_1","age":"45"}]`},
	})
	res := importResults(t, code, body)
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("flat link submission = %v, want one updated row", res)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "45" {
		t.Errorf("stored age = %q, want 45", got)
	}

	// The pin still holds without form_name: a field of another instrument
	// is refused.
	code, body = f.call(t, url.Values{
		"token": {link.Token}, "content": {"record"}, "returnFormat": {"json"},
		"data": {`[{"record_id":"8DISC001","redcap_event_name":"baseline_arm_1","total":"9"}]`},
	})
	res = importResults(t, code, body)
	if len(res) != 1 || res[0].ImportRecordID != importInvalid ||
		!strings.Contains(res[0].ImportFormName, "belongs to instrument 'calc', not 'demo'") {
		t.Fatalf("cross-instrument flat submission = %v, want the pin's instrument message", res)
	}
}
