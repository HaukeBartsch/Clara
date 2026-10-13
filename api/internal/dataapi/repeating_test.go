package dataapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// withRepeatingVisits extends the record fixture with a third instrument,
// "visits", mapped to both events and repeating at baseline (REQ-DB-047): a free
// text, an integer, a calculated field reading the integer at the same event,
// and a checkbox.
func withRepeatingVisits(t *testing.T, f *recordFixture) int64 {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	visits, err := f.s.AddInstrument(ctx, &db.Instrument{ProjectID: f.pid, Name: "visits", Position: 3})
	must(err)
	for _, fl := range []db.Field{
		{FieldName: "visit_note", FieldType: "text", Position: 1},
		{FieldName: "visit_score", FieldType: "text", ValidationType: sql.NullString{String: "integer", Valid: true}, Position: 2},
		{FieldName: "visit_double", FieldType: "calculated", Calculation: sql.NullString{String: "[baseline_arm_1][visit_score] * 2", Valid: true}, Position: 3},
		{FieldName: "symptoms", FieldType: "checkbox", Choices: sql.NullString{String: "1$Cough##2$Fever", Valid: true}, Position: 4},
	} {
		fl.ProjectID, fl.InstrumentID = f.pid, visits
		_, err := f.s.AddField(ctx, &fl)
		must(err)
	}
	arms, err := f.s.ListArms(ctx, f.pid)
	must(err)
	pairs, err := f.s.ListInstrumentEvents(ctx, f.pid)
	must(err)
	base, err := f.s.GetEventByUniqueName(ctx, f.pid, "baseline_arm_1")
	must(err)
	fol, err := f.s.GetEventByUniqueName(ctx, f.pid, "followup_arm_1")
	must(err)
	pairs = append(pairs, db.InstrumentEvent{InstrumentID: visits, EventID: base.ID}, db.InstrumentEvent{InstrumentID: visits, EventID: fol.ID})
	must(f.s.SetInstrumentEventsForArm(ctx, f.pid, arms[0].ID, pairs))
	tx, err := f.s.DB.BeginTx(ctx, nil)
	must(err)
	must(f.s.SetRepeatingEventsTx(ctx, tx, visits, []int64{base.ID}))
	must(tx.Commit())
	f.s.BumpStructureCache()
	return visits
}

// slotValue reads one stored value at its exact storage slot (REQ-DB-048).
func slotValue(t *testing.T, f *recordFixture, record, event, repeating string, instance int, field string) (string, bool) {
	t.Helper()
	var v string
	err := f.s.DB.QueryRow(`SELECT value FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		AND repeating_instrument = ? AND repeating_instance_number = ? AND field_name = ?`,
		f.pid, record, event, repeating, instance, field).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return v, true
}

// TestRepeatingImport covers REDCap's repeat columns on import (REQ-API-155):
// instance 1 lands in the first-instance slot, a further instance under the
// instrument's name, each computes its own calculated field, and a row naming a
// non-repeating pair or no instance number is refused.
func TestRepeatingImport(t *testing.T) {
	f := newRecordFixture(t)
	withRepeatingVisits(t, f)

	res := f.importRows(t, importForm("tok-admin",
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "1", "visit_score": "3", "visit_note": "first"},
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "2", "visit_score": "5", "symptoms___2": "1"},
	))
	for i, r := range res {
		if r.ImportRecordID != importUpdated {
			t.Fatalf("row %d = %+v", i, r)
		}
	}
	if v, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "", 1, "visit_score"); !ok || v != "3" {
		t.Fatalf("instance 1 score = %q %v", v, ok)
	}
	if v, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 2, "visit_score"); !ok || v != "5" {
		t.Fatalf("instance 2 score = %q %v", v, ok)
	}
	// Calculated per instance (REQ-VAL-045): each reads its own instance's score.
	if v, _ := slotValue(t, f, "8DISC001", "baseline_arm_1", "", 1, "visit_double"); v != "6" {
		t.Fatalf("instance 1 double = %q, want 6", v)
	}
	if v, _ := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 2, "visit_double"); v != "10" {
		t.Fatalf("instance 2 double = %q, want 10", v)
	}
	// The identifier is not copied into a further instance.
	if _, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 2, "record_id"); ok {
		t.Fatal("record_id stored at instance 2")
	}
	// Every first instance is instance 1, never the zero value (REQ-DB-019).
	var zero int
	if err := f.s.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE repeating_instance_number < 1`).Scan(&zero); err != nil || zero != 0 {
		t.Fatalf("rows at instance 0: %d (%v)", zero, err)
	}
	// The trail names the instance of the further one only.
	var details string
	if err := f.s.DB.QueryRow(`SELECT details FROM audit_events WHERE event_type = 'record_updated' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"instance":2`) {
		t.Fatalf("audit of instance 2: %s", details)
	}

	for _, tc := range []struct {
		row  map[string]string
		want string
	}{
		{map[string]string{"redcap_event_name": "followup_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": "2", "visit_note": "x"}, "is not repeating at event"},
		{map[string]string{"redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": "", "visit_note": "x"}, "is not an instance number"},
		{map[string]string{"redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": "two", "visit_note": "x"}, "is not an instance number"},
		{map[string]string{"redcap_event_name": "baseline_arm_1", "redcap_repeat_instance": "2", "visit_note": "x"}, "repeating events are not supported"},
		{map[string]string{"redcap_event_name": "baseline_arm_1", "form_name": "demo", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": "2"}, "conflicts with form_name"},
		{map[string]string{"redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": "3", "age": "7"}, "belongs to instrument 'demo'"},
	} {
		tc.row["record_id"] = "8DISC001"
		res := f.importRows(t, importForm("tok-admin", tc.row))
		if res[0].ImportRecordID != importInvalid || !strings.Contains(res[0].ImportFormName, tc.want) {
			t.Errorf("row %v = %+v, want %q", tc.row, res[0], tc.want)
		}
	}
	// "1" without an instrument is the first instance, as before.
	res = f.importRows(t, importForm("tok-admin", map[string]string{"record_id": "8DISC001",
		"redcap_event_name": "followup_arm_1", "redcap_repeat_instance": "1", "visit_note": "fu"}))
	if res[0].ImportRecordID != importUpdated {
		t.Fatalf("instance 1 without instrument: %+v", res[0])
	}
}

// TestRepeatingExportRoundTrip: the flat export writes REDCap's layout — the
// event's own row without the repeating instrument, then one row per instance —
// and importing it back changes nothing; an empty repeating column on a flat row
// is no clear (REQ-API-155).
func TestRepeatingExportRoundTrip(t *testing.T) {
	f := newRecordFixture(t)
	withRepeatingVisits(t, f)
	f.importRows(t, importForm("tok-admin",
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "1", "visit_score": "3"},
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "2", "visit_score": "5", "symptoms___1": "1"},
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "followup_arm_1", "form_name": "visits", "visit_note": "plain"},
	))

	rows := f.exportJSON(t, "tok-admin", url.Values{"records[0]": {"8DISC001"}})
	type key struct{ event, instrument, instance string }
	byKey := map[key]map[string]string{}
	for _, r := range rows {
		byKey[key{r["redcap_event_name"], r["redcap_repeat_instrument"], r["redcap_repeat_instance"]}] = r
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (two event rows, two instance rows): %v", len(rows), rows)
	}
	own := byKey[key{"baseline_arm_1", "", ""}]
	if own == nil || own["age"] != "42" || own["visit_score"] != "" || own["visits_complete"] != "" || own["symptoms___1"] != "" {
		t.Fatalf("baseline's own row: %v", own)
	}
	first := byKey[key{"baseline_arm_1", "visits", "1"}]
	if first == nil || first["visit_score"] != "3" || first["visit_double"] != "6" || first["age"] != "" ||
		first["record_id"] != "8DISC001" || first["visits_complete"] != "1" || first["symptoms___1"] != "0" {
		t.Fatalf("instance 1 row: %v", first)
	}
	second := byKey[key{"baseline_arm_1", "visits", "2"}]
	if second == nil || second["visit_score"] != "5" || second["visit_double"] != "10" || second["symptoms___1"] != "1" ||
		second["record_id"] != "8DISC001" || second["demo_complete"] != "" {
		t.Fatalf("instance 2 row: %v", second)
	}
	// visits does not repeat at follow-up: its values stay on the event's row.
	if fu := byKey[key{"followup_arm_1", "", ""}]; fu == nil || fu["visit_note"] != "plain" {
		t.Fatalf("follow-up row: %v", fu)
	}
	// JSON writes the instance number as a number, like REDCap; an event row
	// keeps the empty string.
	_, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"}, "returnFormat": {"json"},
		"records[0]": {"8DISC001"}, "fields[0]": {"visit_score"}})
	if !strings.Contains(body, `"redcap_repeat_instance":2`) || !strings.Contains(body, `"redcap_repeat_instance":""`) {
		t.Fatalf("redcap_repeat_instance must be a JSON number on instance rows: %s", body)
	}

	// Round trip: every exported row imports back without a change. Calculated
	// values and the _complete columns are export-only, as for any project.
	before := auditCount(t, f.s, "record_updated")
	for _, r := range rows {
		for k := range r {
			if strings.HasSuffix(k, "_complete") || k == "total" || k == "visit_double" {
				delete(r, k)
			}
		}
	}
	payload, _ := json.Marshal(rows)
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"}, "format": {"json"},
		"returnFormat": {"json"}, "data": {string(payload)}})
	for _, r := range importResults(t, code, body) {
		if r.ImportRecordID != importUpdated {
			t.Fatalf("round trip row: %+v", r)
		}
	}
	if after := auditCount(t, f.s, "record_updated"); after != before {
		t.Fatalf("round trip changed data: %d → %d record_updated entries", before, after)
	}
	if v, _ := slotValue(t, f, "8DISC001", "baseline_arm_1", "", 1, "visit_score"); v != "3" {
		t.Fatalf("instance 1 after round trip = %q", v)
	}
}

// TestRepeatingWideExport: the wide layout adds a block per further instance,
// suffixed __<n> (REQ-API-155).
func TestRepeatingWideExport(t *testing.T) {
	f := newRecordFixture(t)
	withRepeatingVisits(t, f)
	f.importRows(t, importForm("tok-admin",
		map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "3", "visit_score": "4"},
	))
	rows := f.exportJSON(t, "tok-admin", url.Values{"type": {"wide"}})
	var row1 map[string]string
	for _, r := range rows {
		if r["record_id"] == "8DISC001" { // the wide row opens with its record (REQ-API-028)
			row1 = r
		}
	}
	if row1 == nil || row1["age_baseline_arm_1"] != "42" {
		t.Fatalf("wide row of 8DISC001: %v", rows)
	}
	for _, col := range []string{"visit_score_baseline_arm_1__2", "visit_score_baseline_arm_1__3", "visits_complete_baseline_arm_1__3"} {
		if _, ok := row1[col]; !ok {
			t.Fatalf("wide column %s missing: %v", col, row1)
		}
	}
	if row1["visit_score_baseline_arm_1__3"] != "4" || row1["visit_double_baseline_arm_1__3"] != "8" ||
		row1["visits_complete_baseline_arm_1__3"] != "1" || row1["visit_score_baseline_arm_1__2"] != "" {
		t.Fatalf("wide instance values: %v", row1)
	}
	if _, ok := row1["visit_score_followup_arm_1__2"]; ok {
		t.Fatal("a non-repeating event got instance columns")
	}
}

// TestRepeatingDeleteAndHidden: repeat_instance narrows a delete to one
// instance; a pair that stops repeating hides its further instances from export
// and refuses new ones, and they come back when it repeats again (REQ-DB-048).
func TestRepeatingDeleteAndHidden(t *testing.T) {
	f := newRecordFixture(t)
	visits := withRepeatingVisits(t, f)
	ctx := context.Background()
	for _, n := range []string{"2", "3"} {
		f.importRows(t, importForm("tok-admin", map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
			"redcap_repeat_instrument": "visits", "redcap_repeat_instance": n, "visit_note": "n" + n}))
	}
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"}, "action": {"delete"}, "returnFormat": {"json"},
		"records[0]": {"8DISC001"}, "events[0]": {"baseline_arm_1"}, "fields[0]": {"visit_note"}, "repeat_instance": {"2"}})
	if res := deleteResults(t, code, body); res[0].Deleted != 1 {
		t.Fatalf("delete = %v", res)
	}
	if _, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 2, "visit_note"); ok {
		t.Fatal("instance 2 survived its delete")
	}
	if _, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 3, "visit_note"); !ok {
		t.Fatal("instance 3 went with instance 2")
	}
	if code, _ := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"record"}, "action": {"delete"},
		"records[0]": {"8DISC001"}, "repeat_instance": {"x"}}); code != 400 {
		t.Fatalf("bad repeat_instance: %d", code)
	}

	// Off: instance 3 stays stored but is neither exported nor writable.
	tx, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetRepeatingEventsTx(ctx, tx, visits, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.s.BumpStructureCache()
	for _, r := range f.exportJSON(t, "tok-admin", nil) {
		if r["redcap_repeat_instrument"] != "" {
			t.Fatalf("hidden instance exported: %v", r)
		}
	}
	res := f.importRows(t, importForm("tok-admin", map[string]string{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1",
		"redcap_repeat_instrument": "visits", "redcap_repeat_instance": "3", "visit_note": "x"}))
	if res[0].ImportRecordID != importInvalid {
		t.Fatalf("write to a hidden instance: %+v", res[0])
	}
	if v, ok := slotValue(t, f, "8DISC001", "baseline_arm_1", "visits", 3, "visit_note"); !ok || v != "n3" {
		t.Fatalf("hidden instance changed: %q %v", v, ok)
	}
}

// TestRepeatingFormsEventsContent covers REDCap's read of the repeating pairs
// and the project flag (REQ-API-155).
func TestRepeatingFormsEventsContent(t *testing.T) {
	f := newRecordFixture(t)
	project := func() map[string]string {
		code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"project"}, "returnFormat": {"json"}})
		mustStatus(t, code, 200, body)
		var out []map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"flag": out[0]["has_repeating_instruments_or_events"].(string)}
	}
	if got := project()["flag"]; got != "" {
		t.Fatalf("flag without repeating = %q", got)
	}
	withRepeatingVisits(t, f)
	if got := project()["flag"]; got != "1" {
		t.Fatalf("flag with repeating = %q", got)
	}
	code, body := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"repeatingFormsEvents"}, "returnFormat": {"json"}})
	mustStatus(t, code, 200, body)
	if strings.TrimSpace(body) != `[{"event_name":"baseline_arm_1","form_name":"visits","custom_form_label":""}]` {
		t.Fatalf("repeatingFormsEvents = %s", body)
	}
	if code, _ := f.call(t, url.Values{"token": {"tok-admin"}, "content": {"repeatingFormsEvents"}, "data": {"[]"}}); code != 400 {
		t.Fatalf("repeatingFormsEvents import: %d", code)
	}
}
