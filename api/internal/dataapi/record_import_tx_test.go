package dataapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
