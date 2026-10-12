package dataapi

// Tests for the checkbox field type on the data API (REQ-DB-042, REQ-EXP-053):
// values are written and stored per choice column `<field>___<code>`, export
// writes one column per choice, and the dictionary contents keep the single
// parent row.

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// newCheckboxFixture is the record fixture plus a checkbox `habits` at the end
// of the demo instrument. It is flagged personal information, which a choice
// column inherits without ever being hashed.
func newCheckboxFixture(t *testing.T) *recordFixture {
	t.Helper()
	f := newRecordFixture(t)
	ctx := context.Background()
	demo, err := f.s.GetInstrumentByName(ctx, f.pid, "demo")
	if err != nil || demo == nil {
		t.Fatalf("demo instrument: %v", err)
	}
	if _, err := f.s.AddField(ctx, &db.Field{ProjectID: f.pid, InstrumentID: demo.ID,
		FieldName: "habits", FieldLabel: sql.NullString{String: "Habits", Valid: true}, FieldType: "checkbox",
		Choices:             sql.NullString{String: "1$Smoking##2$Drinking##3$Sports", Valid: true},
		PersonalInformation: true, Position: 8}); err != nil {
		t.Fatalf("seed habits: %v", err)
	}
	return f
}

func TestCheckboxImportStoresCheckedBoxesOnly(t *testing.T) {
	f := newCheckboxFixture(t)

	res := f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"habits___1": "1", "habits___2": "0", "habits___3": "",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importAdded {
		t.Fatalf("import result = %+v", res)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "habits___1"); got != "1" {
		t.Errorf("checked box stored %q, want 1", got)
	}
	var rows int
	if err := f.s.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE project_id = ? AND record_id = ? AND field_name LIKE 'habits%'`,
		f.pid, "8DISC010").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("stored checkbox rows = %d, want 1 — an unchecked box stores nothing", rows)
	}
	created := dataChangeEntries(t, f, "record_created", "8DISC010")
	if len(created) != 1 || len(created[0].Fields) != 2 || created[0].Fields[1].Field != "habits___1" {
		t.Errorf("create audit = %+v, want record_id then habits___1", created)
	}

	// 0 clears a checked box exactly like an empty value (REQ-DB-042).
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"habits___1": "0", "habits___2": "1",
	}))
	if res[0].ImportRecordID != importUpdated {
		t.Fatalf("update result = %+v", res)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "habits___1"); got != "" {
		t.Errorf("box 1 still stored as %q after 0", got)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "habits___2"); got != "1" {
		t.Errorf("box 2 = %q, want 1", got)
	}

	// The history groups the boxes with their checkbox's instrument (§3.2).
	entries := dataChangeEntries(t, f, "record_updated", "8DISC010")
	if len(entries) != 1 || entries[0].Instrument != "demo" {
		t.Fatalf("update audit entries = %+v", entries)
	}
	changed := map[string]bool{}
	for _, c := range entries[0].Fields {
		changed[c.Field] = true
	}
	if !changed["habits___1"] || !changed["habits___2"] || len(changed) != 2 {
		t.Errorf("audited fields = %v, want habits___1 and habits___2", changed)
	}
}

func TestCheckboxImportRejectsWhatNoBoxMeans(t *testing.T) {
	f := newCheckboxFixture(t)
	for _, tc := range []struct {
		row  map[string]string
		want string
	}{
		{map[string]string{"habits___1": "2"}, "habits___1: CHOICE_INVALID"},
		{map[string]string{"habits___1": "yes"}, "must be 0 or 1"},
		{map[string]string{"habits": "1"}, "habits: NON_VALUE_FIELD"},
		{map[string]string{"habits___9": "1"}, "habits___9: UNKNOWN_FIELD"},
	} {
		row := map[string]string{"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1"}
		for k, v := range tc.row {
			row[k] = v
		}
		res := f.importRows(t, importForm("tok-edit", row))
		if res[0].ImportRecordID != importInvalid || !strings.Contains(res[0].ImportFormName, tc.want) {
			t.Errorf("%v: result = %+v, want a rejection naming %q", tc.row, res[0], tc.want)
		}
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "habits___1"); got != "" {
		t.Errorf("a rejected row stored %q", got)
	}
}

func TestCheckboxExportColumns(t *testing.T) {
	f := newCheckboxFixture(t)
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1",
		"habits___1": "1", "habits___3": "1",
	}))

	// Raw: one column per choice right after the field's position, 1 checked, 0 not.
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"},
		"records": {"8DISC001"}, "events": {"baseline_arm_1"}})
	mustStatus(t, code, 200, body)
	lines := strings.Split(strings.TrimRight(body, "\r\n"), "\n")
	if !strings.Contains(lines[0], ",secret,habits___1,habits___2,habits___3,demo_complete,") {
		t.Errorf("header = %q, want the choice columns after secret", lines[0])
	}
	if strings.Contains(lines[0], ",habits,") {
		t.Errorf("the checkbox itself must not be a column: %q", lines[0])
	}
	rows := f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC001"}})
	if rows[0]["habits___1"] != "1" || rows[0]["habits___2"] != "0" || rows[0]["habits___3"] != "1" {
		t.Errorf("baseline raw boxes = %v", rows[0])
	}
	if rows[1]["redcap_event_name"] != "followup_arm_1" || rows[1]["habits___1"] != "0" {
		t.Errorf("an unchecked box at a mapped event exports 0: %v", rows[1])
	}

	// Labels: the choice's text when checked, nothing otherwise; label headers
	// read "<Field Label>, <Choice Label>".
	code, body = f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"},
		"records": {"8DISC001"}, "events": {"baseline_arm_1"}, "rawOrLabel": {"label"}, "rawOrLabelHeaders": {"label"}})
	mustStatus(t, code, 200, body)
	lines = strings.Split(strings.TrimRight(body, "\r\n"), "\n")
	if !strings.Contains(lines[0], `"Habits, Smoking","Habits, Drinking","Habits, Sports"`) {
		t.Errorf("label header = %q", lines[0])
	}
	if !strings.Contains(lines[1], ",Smoking,,Sports,") {
		t.Errorf("label row = %q", lines[1])
	}
	rows = f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC001"}, "rawOrLabelHeaders": {"both"}})
	if _, ok := rows[0]["Habits, Sports (habits___3)"]; !ok {
		t.Errorf("both-header key missing: %v", rows[0])
	}

	// fields[] matches the checkbox (every column) or one choice column.
	rows = f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC001"}, "fields": {"habits"}})
	if len(rows[0]) != 3+3 { // record_id-less: event + two repeat columns, three boxes
		t.Errorf("fields[]=habits row = %v", rows[0])
	}
	rows = f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC001"}, "fields": {"habits___3"}})
	if _, ok := rows[0]["habits___1"]; ok || rows[0]["habits___3"] != "1" {
		t.Errorf("fields[]=habits___3 row = %v", rows[0])
	}

	// De-identified: personal information on the checkbox, yet the boxes travel
	// unhashed — 1/0 carries nothing to hide (REQ-EXP-053).
	rows = f.exportJSON(t, "tok-de", url.Values{"records": {"8DISC001"}, "events": {"baseline_arm_1"}})
	if rows[0]["habits___1"] != "1" || rows[0]["habits___2"] != "0" {
		t.Errorf("de-identified boxes = %v", rows[0])
	}
}

func TestCheckboxDictionaryKeepsTheParentRow(t *testing.T) {
	f := newCheckboxFixture(t)
	var meta []map[string]string
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"metadata"}, "returnFormat": {"json"}})
	mustStatus(t, code, 200, body)
	if err := decodeInto(body, &meta); err != nil {
		t.Fatal(err)
	}
	var habits []map[string]string
	for _, m := range meta {
		if strings.HasPrefix(m["field_name"], "habits") {
			habits = append(habits, m)
		}
	}
	if len(habits) != 1 || habits[0]["field_type"] != "checkbox" ||
		habits[0]["select_choices_or_calculations"] != "1, Smoking | 2, Drinking | 3, Sports" {
		t.Errorf("metadata rows = %v", habits)
	}
	var names []map[string]string
	code, body = f.call(t, url.Values{"token": {"tok-admin"}, "content": {"exportFieldNames"}, "returnFormat": {"json"}})
	mustStatus(t, code, 200, body)
	if err := decodeInto(body, &names); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range names {
		if strings.HasPrefix(m["field_name"], "habits") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("exportFieldNames lists %d habits rows, want the parent only", n)
	}
}

// A calculation over boxes counts the checked ones: an unchecked box reads as
// 0, never as a missing operand that empties the result (§6.3, REQ-DB-042).
func TestCheckboxCalculationReadsUncheckedAsZero(t *testing.T) {
	f := newCheckboxFixture(t)
	ctx := context.Background()
	calc, err := f.s.GetInstrumentByName(ctx, f.pid, "calc")
	if err != nil || calc == nil {
		t.Fatalf("calc instrument: %v", err)
	}
	if _, err := f.s.AddField(ctx, &db.Field{ProjectID: f.pid, InstrumentID: calc.ID,
		FieldName: "vices", FieldType: "calculated", Position: 2,
		Calculation: sql.NullString{String: "[baseline_arm_1][habits___1] + [baseline_arm_1][habits___2]", Valid: true}}); err != nil {
		t.Fatalf("seed vices: %v", err)
	}
	row := func(values map[string]string) {
		t.Helper()
		values["record_id"], values["form_name"], values["event_name"] = "8DISC010", "demo", "baseline_arm_1"
		if res := f.importRows(t, importForm("tok-edit", values)); len(res) != 1 {
			t.Fatalf("import = %+v", res)
		}
	}

	row(map[string]string{"age": "40"})
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "vices"); got != "0" {
		t.Errorf("no box checked: vices = %q, want 0", got)
	}
	row(map[string]string{"habits___2": "1"})
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "vices"); got != "1" {
		t.Errorf("box 2 checked: vices = %q, want 1", got)
	}
	row(map[string]string{"habits___1": "1"})
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "vices"); got != "2" {
		t.Errorf("both boxes checked: vices = %q, want 2", got)
	}
	row(map[string]string{"habits___1": "0", "habits___2": ""})
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "vices"); got != "0" {
		t.Errorf("both boxes cleared: vices = %q, want 0", got)
	}
}

func TestCheckboxDeleteByFieldTakesItsBoxes(t *testing.T) {
	f := newCheckboxFixture(t)
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC001", "form_name": "demo", "event_name": "baseline_arm_1",
		"habits___1": "1", "habits___2": "1",
	}))
	code, body := f.call(t, url.Values{
		"token": {"tok-del"}, "content": {"record"}, "action": {"delete"}, "returnFormat": {"json"},
		"records[0]": {"8DISC001"}, "fields[0]": {"habits"},
	})
	if res := deleteResults(t, code, body); res[0].Deleted != 1 {
		t.Fatalf("delete result = %+v", res)
	}
	for _, box := range []string{"habits___1", "habits___2"} {
		if got := storedValue(t, f, "8DISC001", "baseline_arm_1", box); got != "" {
			t.Errorf("%s survived a delete of habits: %q", box, got)
		}
	}
	if storedValue(t, f, "8DISC001", "baseline_arm_1", "age") != "42" {
		t.Error("a delete scoped to habits removed another field")
	}
}
