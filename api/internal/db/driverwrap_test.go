package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"csms/api/internal/config"
	"csms/api/internal/metrics"
)

// openInstrumented opens a temp-file SQLite store with a fresh registry — the
// REQ-API-163 wrapper path exactly as cmd/server uses it.

func openInstrumented(t *testing.T) (*Store, *metrics.Registry) {
	t.Helper()
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "wrap.sqlite"),
		AnonSalt:     "test-salt",
	}
	reg := metrics.New()
	s, err := Open(cfg, reg)
	if err != nil {
		t.Fatalf("Open with registry: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, reg
}

func verbsOf(t *testing.T, reg *metrics.Registry) map[string]metrics.DBVerbStats {
	t.Helper()
	out := map[string]metrics.DBVerbStats{}
	for _, st := range reg.Snapshot().DB.Statements {
		out[st.Verb] = st
	}
	return out
}

func TestSQLVerbClassification(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                    "SELECT",
		"  \t\ninsert into t":         "INSERT",
		"(SELECT 1)":                  "SELECT",
		"UPDATE t SET a=1":            "UPDATE",
		"delete from t":               "DELETE",
		"COMMIT":                      "COMMIT",
		"rollback":                    "ROLLBACK",
		"PRAGMA foreign_keys = ON":    "OTHER",
		"BEGIN":                       "OTHER", // raw BEGIN is not a tx commit point
		"CREATE TABLE t (a INT)":      "OTHER",
		"":                            "OTHER",
		"SELECTIVE_STUFF":             "OTHER", // leading word, not prefix match
		"SELECT\t*\nFROM t":           "SELECT",
		"WITH x AS (SELECT 1) SELECT": "OTHER", // CTE: closed enum keeps it OTHER
	}
	for q, want := range cases {
		if got := sqlVerb(q); got != want {
			t.Errorf("sqlVerb(%q) = %s, want %s", q, got, want)
		}
	}
}

func TestDriverWrapCountsStatementsAndRows(t *testing.T) {
	s, reg := openInstrumented(t)
	ctx := context.Background()

	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE wrap (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO wrap (v) VALUES (?)`, "x"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, v FROM wrap`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close() // exercises the Close-after-exhaustion path (record is once-only)

	if _, err := s.DB.ExecContext(ctx, `UPDATE wrap SET v = ? WHERE id <= 2`, "y"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM wrap WHERE id > 4`); err != nil {
		t.Fatalf("delete: %v", err)
	}

	v := verbsOf(t, reg)
	if n != 5 {
		t.Fatalf("iterated %d rows, want 5", n)
	}
	if v["INSERT"].Count < 5 || v["INSERT"].RowsTotal < 5 {
		t.Errorf("INSERT = %+v, want count ≥ 5 and rows_total ≥ 5", v["INSERT"])
	}
	if v["SELECT"].Count < 1 || v["SELECT"].RowsTotal < 5 {
		t.Errorf("SELECT = %+v, want count ≥ 1 and rows_total ≥ 5 (streamed Next counts)", v["SELECT"])
	}
	if v["UPDATE"].RowsTotal != 2 {
		t.Errorf("UPDATE rows_total = %d, want 2 (RowsAffected)", v["UPDATE"].RowsTotal)
	}
	if v["DELETE"].Count < 1 || v["DELETE"].RowsTotal < 1 {
		t.Errorf("DELETE = %+v, want count ≥ 1 and rows_total ≥ 1", v["DELETE"])
	}
	// CREATE TABLE and the foreign_keys PRAGMA from Open are OTHER.
	if v["OTHER"].Count < 2 {
		t.Errorf("OTHER = %+v, want CREATE + PRAGMA counted there", v["OTHER"])
	}
	if reg.Snapshot().DB.ConnectionsDialed < 1 {
		t.Error("no dials observed")
	}
}

func TestDriverWrapTransactionCommitsAndRollbacks(t *testing.T) {
	s, reg := openInstrumented(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE tx (a INT)`); err != nil {
		t.Fatal(err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tx VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatal(err)
	}

	v := verbsOf(t, reg)
	if v["COMMIT"].Count != 1 {
		t.Errorf("COMMIT count = %d, want 1 (timed via driver.Tx)", v["COMMIT"].Count)
	}
	if v["ROLLBACK"].Count != 1 {
		t.Errorf("ROLLBACK count = %d, want 1", v["ROLLBACK"].Count)
	}
}

func TestDriverWrapPreparedStatements(t *testing.T) {
	s, reg := openInstrumented(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE p (a INT)`); err != nil {
		t.Fatal(err)
	}

	st, err := s.DB.PrepareContext(ctx, `INSERT INTO p VALUES (?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.ExecContext(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	q, err := s.DB.PrepareContext(ctx, `SELECT a FROM p`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := q.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
	}
	r.Close()
	q.Close()

	v := verbsOf(t, reg)
	if v["INSERT"].Count < 3 || v["INSERT"].RowsTotal < 3 {
		t.Errorf("prepared INSERT = %+v, want count ≥ 3 / rows ≥ 3 via insStmt", v["INSERT"])
	}
	if v["SELECT"].Count < 1 || v["SELECT"].RowsTotal < 3 {
		t.Errorf("prepared SELECT = %+v, want count ≥ 1 / rows ≥ 3", v["SELECT"])
	}
}

func TestDriverWrapErrorCounting(t *testing.T) {
	s, reg := openInstrumented(t)
	ctx := context.Background()

	// A query the driver rejects must observe as an error under its verb.
	if _, err := s.DB.QueryContext(ctx, `SELECT * FROM no_such_table`); err == nil {
		t.Fatal("expected failure on missing table")
	}
	v := verbsOf(t, reg)
	if v["SELECT"].Errors < 1 {
		t.Errorf("SELECT errors = %d, want ≥ 1", v["SELECT"].Errors)
	}
}

func TestDriverWrapSnapshotCarriesNoQueryText(t *testing.T) {
	s, reg := openInstrumented(t)
	ctx := context.Background()
	const marker = "secret_marker_table"
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE `+marker+` (patient_note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO `+marker+` VALUES ('phi')`); err != nil {
		t.Fatal(err)
	}

	out, err := json.Marshal(reg.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Verb labels (SELECT, INSERT, …) legitimately appear; table names, column
	// names and row values must never (REQ-API-163 PHI rule).
	for _, forbidden := range []string{marker, "patient_note", "phi"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("snapshot JSON leaks %q: %s", forbidden, out)
		}
	}
}

// The SQLite DSN carries ?_pragma=busy_timeout(5000); the connector path must
// apply it exactly like sql.Open did (writes serialize under concurrency).
func TestDriverWrapPragmaDSNStillWorks(t *testing.T) {
	s, _ := openInstrumented(t)
	var ms int
	if err := s.DB.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&ms); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if ms != 5000 {
		t.Errorf("busy_timeout = %d, want 5000 (DSN pragma applied through NewConnector)", ms)
	}
}
