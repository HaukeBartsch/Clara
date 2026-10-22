package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"csms/api/internal/testdb"
)

// TestChainProvisionsTheYearOfItsOwnAccord is New Year's Eve for a process that
// keeps running: its audit writer latched last year at startup, and a request in
// the new year has to find that year provisioned before the handler runs — ahead
// of any transaction an audited change would be written in (§6.1).
//
// The request is deliberately one whose call writes no audit entry: a data-API
// call with a bad token is refused before it touches the trail. Otherwise the
// boundary's own admin_rejected row would create the year objects through the
// writer's lazy path, and the test would pass without the chain doing anything.
func TestChainProvisionsTheYearOfItsOwnAccord(t *testing.T) {
	testdb.SQLiteOnly(t) // inspects the per-year objects and view of §6.2
	e := newEnv(t)       // newEnv runs EnsureYear, latching the writer at this year

	next := time.Date(time.Now().UTC().Year()+1, 1, 1, 0, 0, 5, 0, time.UTC)
	e.Audit.Now = func() time.Time { return next }

	mux := NewMux(e.Store, e.Cfg, e.Audit, nil)
	serve(t, mux, httptest.NewRequest(http.MethodPost, "/api/", nil))

	if n := auditRowCount(t, e); n != 0 {
		t.Fatalf("the refused data-API call wrote %d audit rows; this test needs a request "+
			"that writes none, or it cannot tell the guard's work from the writer's", n)
	}
	yearTable := "audit_events_" + strconv.Itoa(next.Year())
	if kind := masterKindOf(t, e, yearTable); kind != "table" {
		t.Errorf("%s is %q, want table — nothing in the request chain provisioned its year",
			yearTable, kind)
	}
	if kind := masterKindOf(t, e, "audit_events"); kind != "view" {
		t.Errorf("stable name audit_events is %q, want view over the year tables", kind)
	}
}

// TestGuardProvisionsBeforeCallingNext checks the ordering that the placement is
// all about: by the time the handler — and so any transaction it opens — runs,
// the year already exists.
func TestGuardProvisionsBeforeCallingNext(t *testing.T) {
	testdb.SQLiteOnly(t)
	e := newEnv(t)

	next := time.Date(time.Now().UTC().Year()+1, 1, 1, 0, 0, 5, 0, time.UTC)
	e.Audit.Now = func() time.Time { return next }
	yearTable := "audit_events_" + strconv.Itoa(next.Year())

	var kindInsideHandler string
	handlerRan := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerRan = true
		kindInsideHandler = masterKindOf(t, e, yearTable)
	})
	serve(t, rolloverGuard(e.Audit, handler), httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	if !handlerRan {
		t.Fatalf("a successful check must not stop the request")
	}
	if kindInsideHandler != "table" {
		t.Errorf("inside the handler %s was %q, want table — the guard ran too late, or not at all",
			yearTable, kindInsideHandler)
	}
}

// failingGuard stands in for a rollover that cannot complete — the grant is
// missing, the DDL timed out behind a long lock. A request must not pay for it.
type failingGuard struct{}

func (failingGuard) EnsureYear(context.Context) error {
	return errors.New("ALTER TABLE denied")
}

// TestGuardNeverBlocksARequestOnTheRollover: reads of earlier years work without
// the new year's objects, so a failed check is logged and the request is served.
// Only a write of the unprovisioned year can fail, and it reports itself at its
// INSERT (audit.Writer.writeError).
func TestGuardNeverBlocksARequestOnTheRollover(t *testing.T) {
	ran := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	rolloverGuard(failingGuard{}, next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	if !ran {
		t.Errorf("the handler was skipped after a failed rollover check")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the handler's own 418 — the guard answered instead: %s",
			rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("guard wrote a body: %q", rec.Body.String())
	}
}

// TestGuardPassesThroughWithoutAWriter keeps the chain intact when no writer is
// wired in (metrics.Middleware does the same for a nil registry).
func TestGuardPassesThroughWithoutAWriter(t *testing.T) {
	ran := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })
	rolloverGuard(nil, next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !ran {
		t.Errorf("a nil guard swallowed the request")
	}
}

func masterKindOf(t *testing.T, e *env, name string) string {
	t.Helper()
	var kind string
	err := e.Store.DB.QueryRowContext(context.Background(),
		`SELECT type FROM sqlite_master WHERE name = ?`, name).Scan(&kind)
	if err != nil {
		return ""
	}
	return kind
}

func auditRowCount(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// TestGuardReportsAContinuingFailureOnce keeps a stalled rollover from turning
// every request into a log line: one report per distinct failure, while every
// request still reaches its handler and still retries the check.
func TestGuardReportsAContinuingFailureOnce(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(prev)

	ran := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran++ })
	h := rolloverGuard(failingGuard{}, next)
	const requests = 25
	for i := 0; i < requests; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	}

	if ran != requests {
		t.Errorf("handler ran %d times, want %d — a failed check must not skip it", ran, requests)
	}
	if n := strings.Count(logged.String(), "audit year rollover failed"); n != 1 {
		t.Errorf("a continuing failure produced %d log lines, want 1 (retried per request, reported once)", n)
	}
}
