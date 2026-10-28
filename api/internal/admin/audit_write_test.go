package admin

import (
	"net/http"
	"testing"

	"csms/api/internal/testdb"
)

// REQ-AUD-003 / DEV-AUD-1: no data change commits without its audit entry.
// These handlers used to run the change in one transaction and Audit.Insert in
// the next, so a failed entry left the change applied and untraced. Blocking the
// audit write and asserting the row is absent is the only test that distinguishes
// the two shapes — a passing "the entry exists" test cannot.

// blockAuditWrites makes every insert into an audit table fail, the way a dead
// tablespace or a revoked INSERT grant would in production. A trigger expresses
// it: RAISE(ABORT) on SQLite's per-year tables, SIGNAL on MariaDB's single
// partitioned table.
func (e *env) blockAuditWrites(t *testing.T) {
	t.Helper()
	listSQL := `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'audit_events%'`
	blockBody := ` BEGIN SELECT RAISE(ABORT, 'test: audit write blocked'); END`
	if testdb.Engine() == "mariadb" {
		listSQL = `SELECT TABLE_NAME FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' AND TABLE_NAME LIKE 'audit_events%'`
		blockBody = ` FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test: audit write blocked'`
	}
	rows, err := e.Store.DB.Query(listSQL)
	if err != nil {
		t.Fatalf("list audit tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan audit table: %v", err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) == 0 {
		t.Fatal("no audit_events_* table to block — the fixture wrote no audit entry yet")
	}
	for _, tbl := range tables {
		if _, err := e.Store.DB.Exec(
			`CREATE TRIGGER ` + tbl + `_block BEFORE INSERT ON ` + tbl + blockBody); err != nil {
			t.Fatalf("block inserts on %s: %v", tbl, err)
		}
	}
}

func TestStructureChangeRollsBackWhenAuditWriteFails(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Atomicity Study") // writes an audit entry, so one exists to block

	armsBefore := e.armCount(t, projectID)
	e.blockAuditWrites(t)

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "Doomed"}, admin)
	if rec.Code == http.StatusCreated {
		t.Fatalf("arm was created although its audit entry could not be written (status %d)", rec.Code)
	}

	if after := e.armCount(t, projectID); after != armsBefore {
		t.Errorf("arms after a failed audit write: %d, want %d — the change committed without its entry", after, armsBefore)
	}
}

// armCount counts the arms of one project straight from the table, so the check
// does not depend on what a fixture happened to create.
func (e *env) armCount(t *testing.T, projectID int64) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM arms WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		t.Fatalf("count arms: %v", err)
	}
	return n
}

func TestUserCreateRollsBackWhenAuditWriteFails(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	e.mustProject("Atomicity Study") // ensures an audit table exists to block

	e.blockAuditWrites(t)

	rec := e.do("POST", "/api/v1/users", map[string]any{
		"email": "doomed@example.org", "display_name": "Doomed", "password": "Str0ngPassphrase!23",
	}, admin)
	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("user was created although its audit entry could not be written (status %d)", rec.Code)
	}

	var n int
	if err := e.Store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM users WHERE email = 'doomed@example.org'`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("users after a failed audit write: %d, want 0 — the account exists with no trace of who made it", n)
	}
}
