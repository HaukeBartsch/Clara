package dataapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"csms/api/internal/db"
)

// TestRecordImportExistingRowReadsInsideTransaction guards the SQLITE_BUSY
// defect that aborts a bulk import with HTTP 500: the per-record write
// transaction read the holder's active data-access group through the pool
// instead of through itself. A second connection cannot see what the
// transaction has already written, and on SQLite's rollback journal it can wait
// behind locks the very same request holds until busy_timeout(5000) turns the
// wait into a 500 (record_import.go calls activeGroupIDTx for every record that
// already exists).
//
// Capping the pool at one connection makes any read that leaves the transaction
// impossible to serve: with the defect the request dies on its own deadline,
// without it the import answers normally.
func TestRecordImportExistingRowReadsInsideTransaction(t *testing.T) {
	f := newRecordFixture(t)

	// Create the record first, so the import below takes the existing-record
	// branch where the data-access-group check runs at all.
	if res := f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC050", "form_name": "demo", "event_name": "baseline_arm_1", "age": "43",
	})); len(res) != 1 || res[0].ImportRecordID != 1 {
		t.Fatalf("seeding import = %v, want one row with import_record_id 1", res)
	}

	f.s.DB.SetMaxOpenConns(1) // configurePool uses 10 for SQLite
	defer f.s.DB.SetMaxOpenConns(10)

	form := importForm("tok-edit", map[string]string{
		"record_id": "8DISC050", "form_name": "demo", "event_name": "baseline_arm_1", "age": "44",
	})
	req := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("import of an existing record answered %d %s — a read inside the per-record "+
			"transaction is not running on that transaction, and the single-connection pool "+
			"could not serve it", rec.Code, rec.Body.String())
	}
	res := importResults(t, rec.Code, rec.Body.String())
	if len(res) != 1 || res[0].ImportRecordID != 2 {
		t.Fatalf("import result = %v, want one row with import_record_id 2 (update)", res)
	}
	if got := storedValue(t, f, "8DISC050", "baseline_arm_1", "age"); got != "44" {
		t.Errorf("stored age = %q, want 44", got)
	}
}

// TestRecordImportCountBatchReadsInsideTransaction is the same guard for
// returnContent=count, whose rows share one transaction (REQ-API-142,
// DEV-API-40): with a single-connection pool every read of every row — an
// existing record's group check included — must run on that transaction, or
// the call cannot be served at all.
func TestRecordImportCountBatchReadsInsideTransaction(t *testing.T) {
	f := newRecordFixture(t)
	if res := f.importRows(t, importForm("tok-edit", map[string]string{
		"record_id": "8DISC050", "form_name": "demo", "event_name": "baseline_arm_1", "age": "43",
	})); len(res) != 1 || res[0].ImportRecordID != 1 {
		t.Fatalf("seeding import = %v, want one row with import_record_id 1", res)
	}

	f.s.DB.SetMaxOpenConns(1)
	defer f.s.DB.SetMaxOpenConns(10)

	for _, tc := range []struct {
		age1, age2 string
		status     int
	}{{"44", "45", http.StatusOK}, {"46", "abc", http.StatusBadRequest}} {
		form := importForm("tok-edit",
			map[string]string{"record_id": "8DISC050", "form_name": "demo", "event_name": "baseline_arm_1", "age": tc.age1},
			map[string]string{"record_id": "8DISC051", "form_name": "demo", "event_name": "baseline_arm_1", "age": tc.age2},
		)
		form.Set("returnContent", "count")
		req := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req.WithContext(ctx))
		cancel()
		if rec.Code != tc.status {
			t.Fatalf("count import with second age %q answered %d %s, want %d", tc.age2, rec.Code, rec.Body.String(), tc.status)
		}
	}
	// The refused second call rolled back its valid first row (46).
	if got := storedValue(t, f, "8DISC050", "baseline_arm_1", "age"); got != "44" {
		t.Errorf("stored age = %q, want 44 from the applied call", got)
	}
	if got := storedValue(t, f, "8DISC051", "baseline_arm_1", "age"); got != "45" {
		t.Errorf("stored age of 8DISC051 = %q, want 45", got)
	}
}

// TestRecordImportUnmappedPairRefused: a value whose instrument is not mapped to
// the row's event has no pair to live at, so it would be stored and shown
// nowhere (REQ-DB-036). The import refuses it as REDCap does (REQ-API-033,
// DEV-API-42) — a result row of its own, the count mode's 400 — while an empty
// cell there is no value at all and keeps what the pair stored before.
func TestRecordImportUnmappedPairRefused(t *testing.T) {
	f := newRecordFixture(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	late, err := f.s.AddInstrument(ctx, &db.Instrument{ProjectID: f.pid, Name: "late", Position: 5})
	must(err)
	_, err = f.s.AddField(ctx, &db.Field{ProjectID: f.pid, InstrumentID: late, FieldName: "late_note", FieldType: "text", Position: 1})
	must(err)
	arms, err := f.s.ListArms(ctx, f.pid)
	must(err)
	pairs, err := f.s.ListInstrumentEvents(ctx, f.pid)
	must(err)
	fol, err := f.s.GetEventByUniqueName(ctx, f.pid, "followup_arm_1")
	must(err)
	must(f.s.SetInstrumentEventsForArm(ctx, f.pid, arms[0].ID, append(pairs, db.InstrumentEvent{InstrumentID: late, EventID: fol.ID})))
	f.s.BumpStructureCache()

	// Mapped at follow-up: stored.
	res := f.importRows(t, importForm("tok-admin", map[string]string{
		"record_id": "8DISC001", "redcap_event_name": "followup_arm_1", "late_note": "kept"}))
	if res[0].ImportRecordID != importUpdated {
		t.Fatalf("mapped pair: %+v", res[0])
	}
	// Not mapped at baseline: refused, flat row and form_name row alike, and
	// nothing else of the row is stored.
	for _, row := range []map[string]string{
		{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1", "age": "50", "late_note": "x"},
		{"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1", "form_name": "late", "late_note": "x"},
	} {
		res := f.importRows(t, importForm("tok-admin", row))
		if res[0].ImportRecordID != importInvalid ||
			!strings.Contains(res[0].ImportFormName, "late_note: CONTENT_INVALID — instrument 'late' is not mapped to event 'baseline_arm_1'") {
			t.Errorf("row %v = %+v, want the unmapped-pair refusal", row, res[0])
		}
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "age"); got != "42" {
		t.Errorf("age = %q after a refused row, want the old 42", got)
	}
	if got := storedValue(t, f, "8DISC001", "baseline_arm_1", "late_note"); got != "" {
		t.Errorf("late_note stored at baseline = %q", got)
	}
	// Count mode: the call's 400, which FIONA reads as the failed write.
	form := importForm("tok-admin", map[string]string{
		"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1", "late_note": "x"})
	form.Set("returnContent", "count")
	code, body := f.call(t, form)
	mustStatus(t, code, http.StatusBadRequest, body)
	if !strings.Contains(body, "is not mapped to event 'baseline_arm_1'") {
		t.Errorf("count-mode body = %s", body)
	}
	// An empty cell at the unmapped pair is no value: the row applies, and the
	// value the mapped pair holds stays.
	res = f.importRows(t, importForm("tok-admin", map[string]string{
		"record_id": "8DISC001", "redcap_event_name": "baseline_arm_1", "age": "51", "late_note": ""}))
	if res[0].ImportRecordID != importUpdated {
		t.Fatalf("empty unmapped cell: %+v", res[0])
	}
	if got := storedValue(t, f, "8DISC001", "followup_arm_1", "late_note"); got != "kept" {
		t.Errorf("follow-up late_note = %q, want kept", got)
	}
}
