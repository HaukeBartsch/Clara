package dataapi

// Tests for the yesno and notes field types on the data API (REQ-DB-043/044):
// a yesno field takes 1 or 0 and exports Yes/No as labels; a notes field
// stores multi-line text and is free text for the de-identified export.

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// newFieldTypesFixture is the record fixture plus, on the demo instrument, a
// yesno `consent` (stored with its fixed coding, as the API writes it) and a
// notes field `remarks`.
func newFieldTypesFixture(t *testing.T) *recordFixture {
	t.Helper()
	f := newRecordFixture(t)
	ctx := context.Background()
	demo, err := f.s.GetInstrumentByName(ctx, f.pid, "demo")
	if err != nil || demo == nil {
		t.Fatalf("demo instrument: %v", err)
	}
	for _, field := range []db.Field{
		{FieldName: "consent", FieldType: "yesno", Position: 8,
			FieldLabel: sql.NullString{String: "Consent", Valid: true},
			Choices:    sql.NullString{String: validate.YesNoChoices, Valid: true}},
		{FieldName: "remarks", FieldType: "notes", Position: 9,
			FieldLabel: sql.NullString{String: "Remarks", Valid: true}},
	} {
		field.ProjectID, field.InstrumentID = f.pid, demo.ID
		if _, err := f.s.AddField(ctx, &field); err != nil {
			t.Fatalf("seed %s: %v", field.FieldName, err)
		}
	}
	return f
}

func TestYesNoAndNotesImportAndExport(t *testing.T) {
	f := newFieldTypesFixture(t)
	row := func(values map[string]string) []importRow {
		values["record_id"], values["form_name"], values["event_name"] = "8DISC010", "demo", "baseline_arm_1"
		return f.importRows(t, importForm("tok-edit", values))
	}

	// Only the two codes exist; "Yes" is a label, not a value (REQ-VAL-022).
	for _, bad := range []string{"2", "Yes"} {
		if res := row(map[string]string{"consent": bad}); res[0].ImportRecordID != importInvalid ||
			!strings.Contains(res[0].ImportFormName, "consent: CHOICE_INVALID") {
			t.Errorf("consent=%q: result = %+v, want CHOICE_INVALID", bad, res[0])
		}
	}
	remarks := "First line\nsecond line, with a comma"
	if res := row(map[string]string{"consent": "0", "remarks": remarks}); res[0].ImportRecordID != importAdded {
		t.Fatalf("import = %+v", res)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "consent"); got != "0" {
		t.Errorf("consent stored %q, want 0 — No is an answer, not a blank", got)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "remarks"); got != remarks {
		t.Errorf("remarks stored %q, want the multi-line text unchanged", got)
	}

	rows := f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC010"}, "events": {"baseline_arm_1"}})
	if rows[0]["consent"] != "0" || rows[0]["remarks"] != remarks {
		t.Errorf("raw export = %v", rows[0])
	}
	rows = f.exportJSON(t, "tok-admin", url.Values{"records": {"8DISC010"}, "events": {"baseline_arm_1"}, "rawOrLabel": {"label"}})
	if rows[0]["consent"] != "No" {
		t.Errorf("label export consent = %q, want No", rows[0]["consent"])
	}

	// De-identified: an unapproved notes field is free text and stays home
	// (REQ-EXP-023); the yes/no answer is structured and travels.
	rows = f.exportJSON(t, "tok-de", url.Values{"records": {"8DISC010"}, "events": {"baseline_arm_1"}})
	if _, ok := rows[0]["remarks"]; ok {
		t.Errorf("unapproved notes in a de-identified export: %v", rows[0])
	}
	if rows[0]["consent"] != "0" {
		t.Errorf("de-identified consent = %q, want 0", rows[0]["consent"])
	}
}

// A yes/no answer is numeric, so a calculation can count it (§6.3).
func TestYesNoInCalculation(t *testing.T) {
	f := newFieldTypesFixture(t)
	ctx := context.Background()
	calc, err := f.s.GetInstrumentByName(ctx, f.pid, "calc")
	if err != nil || calc == nil {
		t.Fatalf("calc instrument: %v", err)
	}
	if _, err := f.s.AddField(ctx, &db.Field{ProjectID: f.pid, InstrumentID: calc.ID,
		FieldName: "consent_x10", FieldType: "calculated", Position: 2,
		Calculation: sql.NullString{String: "[baseline_arm_1][consent] * 10", Valid: true}}); err != nil {
		t.Fatalf("seed calc: %v", err)
	}
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1", "consent": "1",
	}))
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "consent_x10"); got != "10" {
		t.Errorf("consent_x10 = %q, want 10", got)
	}
}
