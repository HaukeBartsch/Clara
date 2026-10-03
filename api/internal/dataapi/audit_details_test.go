package dataapi

import (
	"encoding/json"
	"testing"
)

// TestImportAuditUsesTheDataChangeShape pins the payloads an import writes to
// Audit_Logging_Design.md §3.2 — the shape the record history (§4.16) parses
// and filters: action, record, instrument, event, and per field old → new with
// null on the side that holds no value. A recomputed calculated result gets its
// own calculated_recomputed entry naming where it is stored and the change
// that set it off (REQ-AUD-023).
func TestImportAuditUsesTheDataChangeShape(t *testing.T) {
	f := newRecordFixture(t)

	res := f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "51", "status": "1",
	}))
	if res[0].ImportRecordID != 1 {
		t.Fatalf("create = %+v", res)
	}
	created := dataChangeEntries(t, f, "record_created", "8DISC010")
	if len(created) != 1 {
		t.Fatalf("record_created entries = %+v", created)
	}
	c := created[0]
	if c.Action != "create" || c.RecordID != "8DISC010" || c.Instrument != "demo" || c.Event != "baseline_arm_1" {
		t.Fatalf("record_created = %+v", c)
	}
	assertChanges(t, c.Fields, []wantChange{
		{"record_id", nil, ptr("8DISC010")}, {"age", nil, ptr("51")}, {"status", nil, ptr("1")},
	})
	calc := recomputedEntries(t, f, "8DISC010")
	if len(calc) != 1 {
		t.Fatalf("calculated_recomputed entries = %+v", calc)
	}
	assertRecomputed(t, calc[0], calculatedDetails{RecordID: "8DISC010", Field: "total", Instrument: "calc",
		Event: "baseline_arm_1", Old: "", New: "102", TriggerField: ptr("age"), TriggerEvent: ptr("baseline_arm_1")})

	// An update that changes one value and clears another: old → new, and the
	// cleared side null (REQ-VAL-024 — empty is no value).
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1",
		"age": "52", "status": "",
	}))
	updated := dataChangeEntries(t, f, "record_updated", "8DISC010")
	if len(updated) != 1 || updated[0].Action != "update" || updated[0].Instrument != "demo" {
		t.Fatalf("record_updated = %+v", updated)
	}
	assertChanges(t, updated[0].Fields, []wantChange{{"age", ptr("51"), ptr("52")}, {"status", ptr("1"), nil}})
	calc = recomputedEntries(t, f, "8DISC010")
	if len(calc) != 2 {
		t.Fatalf("calculated_recomputed after update = %+v", calc)
	}
	assertRecomputed(t, calc[1], calculatedDetails{RecordID: "8DISC010", Field: "total", Instrument: "calc",
		Event: "baseline_arm_1", Old: "102", New: "104", TriggerField: ptr("age"), TriggerEvent: ptr("baseline_arm_1")})

	// A change at another event moves no baseline-referencing result: no trigger
	// is invented and no recomputation entry written.
	f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC010", "form_name": "demo", "event_name": "followup_arm_1", "age": "60",
	}))
	if got := recomputedEntries(t, f, "8DISC010"); len(got) != 2 {
		t.Fatalf("an unrelated change wrote a recomputation: %+v", got)
	}
	followup := dataChangeEntries(t, f, "record_updated", "8DISC010")
	if last := followup[len(followup)-1]; last.Event != "followup_arm_1" {
		t.Fatalf("follow-up entry = %+v", last)
	}
}

func TestImportTriggerFollowsTheCalculation(t *testing.T) {
	changes := map[string]map[string]string{"b": {"old": "", "new": "1"}, "a": {"old": "1", "new": "2"}}

	field, event := importTrigger("[e1][a] + [e1][b]", "e1", changes)
	if field == nil || *field != "a" || event == nil || *event != "e1" {
		t.Fatalf("trigger = %v/%v, want the first changed reference a@e1", field, event)
	}
	if field, _ := importTrigger("[e2][a] * 2", "e1", changes); field != nil {
		t.Fatalf("a change at e1 cannot trigger a reference to e2: %v", *field)
	}
	if field, _ := importTrigger("not ( an expression", "e1", changes); field != nil {
		t.Fatal("an unparseable expression names no trigger")
	}
}

type wantChange struct {
	field    string
	old, new *string
}

func ptr(s string) *string { return &s }

func assertChanges(t *testing.T, got []fieldChange, want []wantChange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fields = %s, want %d", mustJSON(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Field != w.field || !sameValue(g.Old, w.old) || !sameValue(g.New, w.new) {
			t.Fatalf("field %d = %s, want %s %v→%v", i, mustJSON(g), w.field, deref(w.old), deref(w.new))
		}
	}
}

func assertRecomputed(t *testing.T, got, want calculatedDetails) {
	t.Helper()
	if mustJSON(got) != mustJSON(want) {
		t.Fatalf("calculated_recomputed = %s, want %s", mustJSON(got), mustJSON(want))
	}
}

func recomputedEntries(t *testing.T, f *recordFixture, recordID string) []calculatedDetails {
	t.Helper()
	rows, err := f.s.DB.Query(`SELECT details FROM audit_events
		WHERE event_type = 'calculated_recomputed' AND target_record = ? ORDER BY id`, recordID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []calculatedDetails
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var c calculatedDetails
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("calculated_recomputed %s: %v", raw, err)
		}
		out = append(out, c)
	}
	return out
}

func sameValue(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func deref(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
