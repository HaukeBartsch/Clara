package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"csms/api/internal/config"
)

// Open is the only place a connection is made (REQ-TECH-006), and it must fail
// fast on an unusable configuration rather than at the first query
// (REQ-TECH-014).

func TestOpenUnknownConnection(t *testing.T) {
	cfg := &config.Config{AppEnv: "development", DBConnection: "postgres", AnonSalt: "s"}
	s, err := Open(cfg)
	if err == nil {
		s.Close()
		t.Fatalf("Open with DB_CONNECTION=postgres succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "DB_CONNECTION") {
		t.Errorf("error %q does not name the offending variable", err)
	}
}

func TestOpenSQLiteSelectsDialect(t *testing.T) {
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "open.sqlite"),
		AnonSalt:     "test-salt",
	}
	s, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if s.Dialect != DialectSQLite {
		t.Errorf("Dialect = %q, want %q", s.Dialect, DialectSQLite)
	}
	if s.Cfg != cfg {
		t.Errorf("Store.Cfg is not the config Open was given")
	}
	// SQLite enforces foreign keys per connection and defaults to off; the
	// cascade rules the repositories rely on (REQ-DB-004) need it on.
	var on int
	if err := s.DB.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if on != 1 {
		t.Errorf("foreign_keys = %d, want 1", on)
	}
}

// An unreachable database surfaces at Open, not on the first request: a SQLite
// file in a directory that does not exist opens lazily and fails the ping.
func TestOpenUnreachableDatabaseFailsFast(t *testing.T) {
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "missing-dir", "test.sqlite"),
		AnonSalt:     "test-salt",
	}
	s, err := Open(cfg)
	if err == nil {
		s.Close()
		t.Fatalf("Open on an unwritable path succeeded, want a ping failure")
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Errorf("error %q does not report the connectivity check", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Shutdown paths can race (defer + explicit); a second Close must not panic.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := s.DB.PingContext(context.Background()); err == nil {
		t.Errorf("ping after Close succeeded, want an error")
	}
}

// nowUTC is the single source of DATETIME values (REQ-DB-005, GD-7): server
// time in UTC, in the canonical layout, so a row written here scans back
// through datetimeString unchanged.
func TestNowUTCLayout(t *testing.T) {
	before := time.Now().UTC().Add(-time.Minute)
	v := nowUTC()
	after := time.Now().UTC().Add(time.Minute)

	got, err := time.Parse(datetimeLayout, v)
	if err != nil {
		t.Fatalf("nowUTC() = %q does not parse as %q: %v", v, datetimeLayout, err)
	}
	if got.Before(before) || got.After(after) {
		t.Errorf("nowUTC() = %q is not the current time (between %s and %s)",
			v, before.Format(datetimeLayout), after.Format(datetimeLayout))
	}
	if again, ok := datetimeString(v); !ok || again != v {
		t.Errorf("nowUTC() output does not survive a datetimeString round trip: %q → %q", v, again)
	}
}
