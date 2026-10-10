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
	"strconv"
	"strings"
	"sync"
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
	// The link is keyed by (record, instrument, event): one instrument mapped to
	// several events has one distinct link each (REQ-AUTH-039).
	ev, err := f.s.GetEventByUniqueName(ctx, f.pid, "baseline_arm_1")
	if err != nil || ev == nil {
		t.Fatalf("GetEventByUniqueName(baseline_arm_1): %v", err)
	}
	link := &db.SurveyLink{ProjectID: f.pid, RecordID: "8DISC001", InstrumentID: ins.ID, EventID: ev.ID}
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
	if err := f.s.RevokeSurveyLink(ctx, f.pid, "8DISC001", link.InstrumentID, link.EventID); err != nil {
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
	ev, err := f.s.GetEventByUniqueName(ctx, f.pid, "baseline_arm_1")
	if err != nil || ev == nil {
		t.Fatalf("GetEventByUniqueName(baseline_arm_1): %v", err)
	}
	link := &db.SurveyLink{ProjectID: f.pid, RecordID: "8DISC077", InstrumentID: ins.ID, EventID: ev.ID}
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
// else. The refused row comes first here on purpose: a row the validator rejected
// stores nothing and does not consume the link, so the respondent corrects on the
// spot and sends again (REQ-API-145) — which is what lets one link carry both
// calls below.
func TestSurveyLinkSubmissionDecidedByData(t *testing.T) {
	f, link := surveyLinkFixture(t)

	// The pin holds without form_name: a field of another instrument is refused…
	code, body := f.call(t, url.Values{
		"token": {link.Token}, "content": {"record"}, "returnFormat": {"json"},
		"data": {`[{"record_id":"8DISC001","redcap_event_name":"baseline_arm_1","total":"9"}]`},
	})
	res := importResults(t, code, body)
	if len(res) != 1 || res[0].ImportRecordID != importInvalid ||
		!strings.Contains(res[0].ImportFormName, "belongs to instrument 'calc', not 'demo'") {
		t.Fatalf("cross-instrument flat submission = %v, want the pin's instrument message", res)
	}

	// …and left the link live, so the corrected row still gets in.
	code, body = f.call(t, url.Values{
		"token": {link.Token}, "content": {"record"}, "returnFormat": {"json"},
		"data": {`[{"record_id":"8DISC001","redcap_event_name":"baseline_arm_1","age":"45"}]`},
	})
	res = importResults(t, code, body)
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("flat link submission after a rejected row = %v, want one updated row", res)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "45" {
		t.Errorf("stored age = %q, want 45", got)
	}
}

// The public page names none of the triple: a row carrying only field values is
// addressed by the link itself, which is how a respondent submits without ever
// being told the study's record id (§3.10, REQ-API-083). The first save through
// the link stamps its collection date (REQ-DB-041) — and that stamp is what ends
// the link, so it is also the last thing that happens on it (REQ-API-145).
func TestSurveyLinkSubmissionResolvesItsTriple(t *testing.T) {
	f, link := surveyLinkFixture(t)

	res := f.importRows(t, importForm(link.Token, map[string]string{
		"age": "47", "notes": "sent from the survey page",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("unaddressed submission = %v, want one updated row on the link's triple", res)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "47" {
		t.Errorf("stored age = %q, want 47 at the link's record and event", got)
	}
	if got := storedValue(t, f, "8DISC001", "followup_arm_1", "age"); got != "43" {
		t.Errorf("age at followup_arm_1 = %q, want the fixture's 43 — the link's event is the only target", got)
	}

	e := lastSurveyEntry(t, f)
	if e == nil {
		t.Fatal("no survey_submitted audit row")
	}
	if e.TargetRecord != "8DISC001" || e.Details.RecordID != "8DISC001" {
		t.Errorf("audit identifies %q/%q, want the resolved 8DISC001 in both",
			e.TargetRecord, e.Details.RecordID)
	}

	collected := func() sql.NullString {
		t.Helper()
		var v sql.NullString
		if err := f.s.DB.QueryRow(`SELECT collected_at FROM survey_links WHERE id = ?`, link.ID).Scan(&v); err != nil {
			t.Fatalf("read collected_at: %v", err)
		}
		return v
	}
	first := collected()
	if !first.Valid {
		t.Fatal("the first save through the link must stamp collected_at (REQ-DB-041)")
	}
	// The stamp is also what spends the link (REQ-API-145), so nothing arrives
	// after it that could move it: both the stored value and the date stay as the
	// one submission left them.
	if code, body := f.call(t, importForm(link.Token, map[string]string{"age": "48"})); code != 410 {
		t.Errorf("second submission = %d (%s), want 410", code, body)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "47" {
		t.Errorf("stored age = %q after a submission on a spent link, want the untouched 47", got)
	}
	if again := collected(); again != first {
		t.Errorf("collected_at moved from %v to %v — later saves never change it", first, again)
	}
}

// One submission per link (REQ-API-145): the save that stored the answer spent
// the link, and every later call on its token answers 410 — the fill as much as
// the render — having written neither value nor audit entry. The 410 is the one
// state a link's holder is told: an unknown token keeps the uniform 401 of
// REQ-API-011, so a respondent reopening their own finished survey learns that
// their answer arrived without anyone being able to probe which links exist.
func TestSurveyLinkSecondSubmissionGone(t *testing.T) {
	f, link := surveyLinkFixture(t)

	res := f.importRows(t, importForm(link.Token, map[string]string{"age": "47"}))
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("first submission = %v, want one updated row", res)
	}

	calls := map[string]url.Values{
		"a second fill": importForm(link.Token, map[string]string{"age": "48"}),
		"the render":    {"token": {link.Token}, "content": {"metadata"}, "returnFormat": {"json"}},
	}
	for name, form := range calls {
		code, body := f.call(t, form)
		if code != 410 {
			t.Errorf("%s: status = %d, want 410 (%s)", name, code, body)
		}
		if !strings.Contains(body, "Survey already submitted") {
			t.Errorf("%s: body = %s, want the machine-readable 'Survey already submitted'", name, body)
		}
	}

	// Nothing of the refused call was stored — not a value, not a trail.
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "47" {
		t.Errorf("stored age = %q, want the 47 the accepted submission left", got)
	}
	if n := auditCount(t, f.s, "survey_submitted"); n != 1 {
		t.Errorf("%d survey_submitted entries, want the one its own submission wrote", n)
	}

	// A token that is nobody's stays in the single indistinguishable 401
	// (REQ-API-011, REQ-AUTH-032): only a link this respondent finished is told.
	code, body := f.call(t, url.Values{
		"token": {"00000000-0000-4000-8000-000000000000"}, "content": {"metadata"}, "returnFormat": {"json"},
	})
	if code != 401 || strings.Contains(body, "Survey") {
		t.Errorf("unknown token: status = %d body = %s, want the uniform 401", code, body)
	}
}

// A submission that does name a triple stays bound to the link's. Another event
// of the same instrument — which holds its own distinct link (REQ-AUTH-039) —
// is the uniform 403 rather than a silent retarget, and an unknown event name
// never resolves to the link's event either.
func TestSurveyLinkSubmissionCannotNameAnotherEvent(t *testing.T) {
	f, link := surveyLinkFixture(t)

	cases := map[string]map[string]string{
		"another event of the same instrument": {
			"event_name": "followup_arm_1", "age": "50",
		},
		"another event, alias spelling": {
			"redcap_event_name": "followup_arm_1", "age": "50",
		},
	}
	for name, fields := range cases {
		form := importForm(link.Token, fields)
		code, body := f.call(t, form)
		if code != 403 {
			t.Errorf("%s: status = %d, want 403 (%s)", name, code, body)
		}
	}
	// The event the link does not hold keeps its own value (the fixture's 43),
	// and so does the one it does (its 42): a rejected submission writes nothing.
	if got := storedValue(t, f, "8DISC001", "followup_arm_1", "age"); got != "43" {
		t.Errorf("age at followup_arm_1 = %q, want the untouched 43", got)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("age at the link's event = %q, want the untouched 42", got)
	}
}

// The record an unaddressed submission resolves to is the link's own, so a row
// that names a different record alongside its values stays the 403 of
// REQ-API-083 — resolution fills an omission, it does not overrule a claim.
func TestSurveyLinkResolutionDoesNotOverruleANamedRecord(t *testing.T) {
	f, link := surveyLinkFixture(t)

	code, body := f.call(t, importForm(link.Token, map[string]string{
		"record_id": "8DISC002", "age": "51",
	}))
	if code != 403 {
		t.Errorf("status = %d, want 403 (%s)", code, body)
	}
	if got := storedValue(t, f, "8DISC002", "baseline_arm_1", "age"); got != "30" {
		t.Errorf("8DISC002 age = %q, want the fixture's untouched 30", got)
	}
}

// Two submissions racing on one link store exactly once (REQ-API-145). The stamp's
// conditional write is the guard: the racer that stamps nothing rolls its values
// back with it and answers 410 like any later call on a spent link, so no answer
// is stored twice and the trail holds one submission.
func TestSurveyLinkConcurrentSubmissionStoresOnce(t *testing.T) {
	f, link := surveyLinkFixture(t)

	// Each racer sends a distinct age, so the stored value names the winner.
	const racers = 4
	codes := make([]int, racers)
	bodies := make([]string, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together: the race is the point
			rec := doRec(t, f.h, importForm(link.Token, map[string]string{"age": strconv.Itoa(50 + i)}))
			codes[i], bodies[i] = rec.Code, rec.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, code := range codes {
		switch code {
		case 200:
			if winner >= 0 {
				t.Fatalf("two submissions stored on one link: %v", codes)
			}
			winner = i
		case 410:
		default:
			t.Errorf("racer %d answered %d (%s), want 200 or 410", i, code, bodies[i])
		}
	}
	if winner < 0 {
		t.Fatalf("no submission got in: %v / %s", codes, bodies[0])
	}
	if want, got := strconv.Itoa(50+winner), storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != want {
		t.Errorf("stored age = %q, want racer %d's %s", got, winner, want)
	}
	if n := auditCount(t, f.s, "survey_submitted"); n != 1 {
		t.Errorf("%d survey_submitted entries, want the winner's alone", n)
	}
}
