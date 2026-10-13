package dataapi

// Tests for the record identifier as an import key (GD-8, REQ-API-031): a row
// carries the record id under the name of the first field of the first
// instrument — whatever that name is — exactly as an export writes it.
// `record_id` is no framing key of its own.

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// renamedIdentifierFixture is the record fixture with its identifier field
// renamed from record_id to study_instance_uid, stored values included — the
// shape of a FIONA "Incoming" project.
func renamedIdentifierFixture(t *testing.T) *recordFixture {
	t.Helper()
	f := newRecordFixture(t)
	for _, q := range []string{
		`UPDATE fields SET field_name = 'study_instance_uid' WHERE project_id = ? AND field_name = 'record_id'`,
		`UPDATE data SET field_name = 'study_instance_uid' WHERE project_id = ? AND field_name = 'record_id'`,
	} {
		if _, err := f.s.DB.Exec(q, f.pid); err != nil {
			t.Fatalf("rename identifier: %v", err)
		}
	}
	return f
}

func TestImportKeysOnIdentifierFieldName(t *testing.T) {
	f := renamedIdentifierFixture(t)

	res := f.importRows(t, importForm("tok-edit", map[string]string{
		"study_instance_uid": "8DISC010", "form_name": "demo", "event_name": "baseline_arm_1", "age": "51",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importAdded || res[0].RecordID != "8DISC010" {
		t.Fatalf("add result = %v, want 8DISC010 added", res)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "study_instance_uid"); got != "8DISC010" {
		t.Errorf("identifier value = %q, want 8DISC010 stored under study_instance_uid", got)
	}
	if got := storedValue(t, f, "8DISC010", "baseline_arm_1", "age"); got != "51" {
		t.Errorf("age = %q, want 51", got)
	}

	// The literal record_id names no field of this project: the row has no
	// record id, and nothing is stored.
	res = f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC011", "form_name": "demo", "event_name": "baseline_arm_1", "age": "52",
	}))
	if len(res) != 1 || res[0].ImportRecordID != importInvalid ||
		!strings.Contains(res[0].ImportFormName, "study_instance_uid: CONTENT_INVALID — a record id is required") {
		t.Fatalf("record_id row = %v, want the identifier named as missing", res)
	}
	if got := storedValue(t, f, "8DISC011", "baseline_arm_1", "age"); got != "" {
		t.Errorf("age of 8DISC011 = %q, want nothing stored", got)
	}
}

// An export imports back unchanged: the identifier column it writes is the
// key the import reads (REQ-API-028/031).
func TestImportRoundTripsExportWithRenamedIdentifier(t *testing.T) {
	f := renamedIdentifierFixture(t)

	rows := f.exportJSON(t, "tok-edit", url.Values{"records[0]": {"8DISC002"},
		"fields[0]": {"study_instance_uid"}, "fields[1]": {"age"}})
	if len(rows) == 0 || rows[0]["study_instance_uid"] != "8DISC002" {
		t.Fatalf("export = %v, want 8DISC002 under study_instance_uid", rows)
	}
	if _, ok := rows[0]["record_id"]; ok {
		t.Fatalf("export row %v carries record_id, which names no field here", rows[0])
	}
	row := rows[0]
	row["age"] = "31"

	res := f.importRows(t, importForm("tok-edit", row))
	if len(res) != 1 || res[0].ImportRecordID != importUpdated {
		t.Fatalf("re-import = %v, want one updated row", res)
	}
	if got := storedValue(t, f, "8DISC002", "baseline_arm_1", "age"); got != "31" {
		t.Errorf("age = %q, want 31", got)
	}
}

// A survey submission names no record; the link supplies it and the import
// stores it under the identifier's own name.
func TestSurveyLinkStoresRenamedIdentifier(t *testing.T) {
	f := renamedIdentifierFixture(t)
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

	res := f.importRows(t, importForm(link.Token, map[string]string{"age": "51"}))
	if len(res) != 1 || res[0].ImportRecordID != importAdded {
		t.Fatalf("submission = %v, want one added row", res)
	}
	if got := storedValue(t, f, "8DISC077", "baseline_arm_1", "study_instance_uid"); got != "8DISC077" {
		t.Errorf("identifier value = %q, want 8DISC077 stored under study_instance_uid", got)
	}
	if got := storedValue(t, f, "8DISC077", "baseline_arm_1", "record_id"); got != "" {
		t.Errorf("a record_id value %q was stored for a field the project does not have", got)
	}
}
