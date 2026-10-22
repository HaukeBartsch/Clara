package audit

import (
	"context"
	"strings"
	"testing"
	"time"

	"csms/api/internal/testdb"
)

// These tests cross a calendar-year boundary by pinning the writer's clock
// (Writer.Now) and moving it, so §6's rollover is exercised rather than argued
// about. The boundary case that matters is a process that was started last year:
// its latch covers only that year, and everything below asks what happens to the
// first write of the new one.

const (
	oldYear = 2026
	newYear = 2027
)

var newYearsEve = time.Date(oldYear, 12, 31, 23, 59, 50, 0, time.UTC)
var newYearDay = time.Date(newYear, 1, 1, 0, 0, 5, 0, time.UTC)

// countThroughStableName reads the way every reader does: by the stable name.
func countThroughStableName(t *testing.T, w *Writer, eventType string) int {
	t.Helper()
	var n int
	if err := w.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE event_type = ?`, eventType).Scan(&n); err != nil {
		t.Fatalf("read through stable name: %v", err)
	}
	return n
}

// TestStandaloneWriteAcrossTheBoundary covers §6.1's trigger on the path that is
// free to act on it: a write with no transaction around it may run the rollover,
// and must. The latch is the mechanism — on MariaDB it is the only thing that
// provisions the year for a non-HTTP caller — so it is what this asserts first;
// the visible row is the consequence.
func TestStandaloneWriteAcrossTheBoundary(t *testing.T) {
	testdb.SQLiteOnly(t) // inspects the per-year objects and view of §6.2
	ctx := context.Background()
	s := openStore(t)

	now := newYearsEve
	w := NewWriter(s.DB, string(s.Dialect))
	w.Now = func() time.Time { return now }
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	if err := w.Insert(ctx, Entry{EventType: LoginSuccess, Source: SourceUI}); err != nil {
		t.Fatalf("Insert (old year): %v", err)
	}

	now = newYearDay // the process keeps running; nothing restarts it
	if w.covers(newYear) {
		t.Fatalf("the latch already covers %d before anything ran for it", newYear)
	}

	if err := w.Insert(ctx, Entry{EventType: Logout, Source: SourceUI}); err != nil {
		t.Fatalf("Insert (new year): %v", err)
	}
	if !w.covers(newYear) {
		t.Errorf("the first write of %d did not run the rollover (§6.1): on MariaDB this "+
			"is where the year's partition would be added", newYear)
	}
	if masterKind(t, s, physical("audit_events", newYear)) != "table" {
		t.Errorf("%s was not created by the write of the new year", physical("audit_events", newYear))
	}
	// The row has to be readable where readers look. §6.2 makes creating the
	// year object and rebuilding the view one check, not two.
	if got := countThroughStableName(t, w, Logout); got != 1 {
		t.Errorf("new-year row visible through audit_events = %d, want 1", got)
	}
	if got := countThroughStableName(t, w, LoginSuccess); got != 1 {
		t.Errorf("old-year row still visible = %d, want 1", got)
	}
}

// TestTransactionWriteAcrossTheBoundary is the same crossing on the path an
// audited change takes. The writer must not run the rollover here — on MariaDB
// that would commit the caller's transaction (REQ-AUD-003) — while SQLite, whose
// DDL is transactional, still has to land the row somewhere a read can find it.
func TestTransactionWriteAcrossTheBoundary(t *testing.T) {
	testdb.SQLiteOnly(t)
	ctx := context.Background()
	s := openStore(t)

	now := newYearsEve
	w := NewWriter(s.DB, string(s.Dialect))
	w.Now = func() time.Time { return now }
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	if err := w.Insert(ctx, Entry{EventType: LoginSuccess, Source: SourceUI}); err != nil {
		t.Fatalf("Insert (old year): %v", err)
	}

	now = newYearDay

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := w.InsertTx(ctx, tx, Entry{EventType: RecordCreated, Source: SourceAPI, ProjectID: 3}); err != nil {
		tx.Rollback()
		t.Fatalf("InsertTx (new year): %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if got := countThroughStableName(t, w, RecordCreated); got != 1 {
		t.Errorf("new-year entry visible through audit_events = %d, want 1 — "+
			"the year object was created without rebuilding the view", got)
	}
	if got := countThroughStableName(t, w, LoginSuccess); got != 1 {
		t.Errorf("old-year row still visible = %d, want 1", got)
	}
	// One view over both years, in year order.
	var years []string
	rows, err := s.DB.Query(`SELECT DISTINCT substr(created_at, 1, 4) FROM audit_events ORDER BY 1`)
	if err != nil {
		t.Fatalf("distinct years: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var y string
		if err := rows.Scan(&y); err != nil {
			t.Fatalf("scan year: %v", err)
		}
		years = append(years, y)
	}
	if len(years) != 2 || years[0] != "2026" || years[1] != "2027" {
		t.Errorf("years visible through the stable name = %v, want [2026 2027]", years)
	}
}

// TestRolledBackYearCreationLeavesTheYearWritable covers the cache that makes the
// write path cheap: w.ensured says a year's objects exist, and a rollback of the
// transaction that created them takes them back without saying so. The next write
// has to notice instead of failing for every caller until the process restarts.
// Both writes go through InsertTx on purpose — the standalone path checks its
// latch first and would paper over the staleness.
func TestRolledBackYearCreationLeavesTheYearWritable(t *testing.T) {
	testdb.SQLiteOnly(t)
	ctx := context.Background()
	s := openStore(t)

	w := NewWriter(s.DB, string(s.Dialect))
	w.Now = func() time.Time { return newYearDay } // never EnsureYear'd: latch is stale

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := w.InsertTx(ctx, tx, Entry{EventType: RecordCreated, Source: SourceAPI}); err != nil {
		tx.Rollback()
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	// The rolled-back operation left nothing behind — including its year object.
	if got := countThroughStableName(t, w, RecordCreated); got != 0 {
		t.Fatalf("rolled-back entry survived (REQ-AUD-003): %d rows", got)
	}
	if masterKind(t, s, physical("audit_events", newYear)) == "table" {
		t.Errorf("%s outlived the transaction that created it", physical("audit_events", newYear))
	}

	// And the year is still writable: the stale cache entry is re-run, not trusted.
	tx2, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx (second): %v", err)
	}
	if err := w.InsertTx(ctx, tx2, Entry{EventType: RecordUpdated, Source: SourceAPI}); err != nil {
		tx2.Rollback()
		t.Fatalf("InsertTx after a rolled-back year creation: %v — every later write of %d "+
			"would fail the same way", err, newYear)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := countThroughStableName(t, w, RecordUpdated); got != 1 {
		t.Errorf("entry after the stale cache = %d visible, want 1", got)
	}
}

// TestMariaDBWritePathRunsNoDDL is the REQ-AUD-003 half: on MariaDB the writer
// must never attempt DDL inside a caller's transaction, because DDL commits it.
// The store here is SQLite with the writer told to speak MariaDB, which lets the
// absence of DDL be observed directly — the missing table stays missing.
func TestMariaDBWritePathRunsNoDDL(t *testing.T) {
	testdb.SQLiteOnly(t) // a SQLite file stands in for the engine; only its dialect name matters
	ctx := context.Background()
	s := openStore(t)

	// MariaDB writes the stable name (§6.1); drop it so any INSERT must fail,
	// and so any DDL that tried to repair it would leave a trace.
	if _, err := s.DB.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	w := NewWriter(s.DB, "mariadb")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	err = w.InsertTx(ctx, tx, Entry{EventType: RecordUpdated, Source: SourceAPI})
	if err == nil {
		t.Fatalf("insert into a dropped table unexpectedly succeeded")
	}
	if masterKind(t, s, "audit_events") != "" {
		t.Errorf("the MariaDB write path created audit_events inside the caller's transaction — "+
			"DDL would have committed it (REQ-AUD-003): %v", err)
	}
	// The failure names the rollover, because "no partition for value" on its own
	// sends someone looking at the wrong thing.
	if !strings.Contains(err.Error(), "no rollover has run") {
		t.Errorf("error = %v, want it to name the missing rollover", err)
	}

	// Once the year is covered, an unrelated failure keeps its own error.
	w.latched.Store(int64(w.now().Year()))
	err = w.InsertTx(ctx, tx, Entry{EventType: RecordUpdated, Source: SourceAPI})
	if err == nil {
		t.Fatalf("insert into a dropped table unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "no rollover has run") {
		t.Errorf("error blames the rollover although this year is latched: %v", err)
	}
}
