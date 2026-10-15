package db

// Transparent database/sql instrumentation (REQ-API-163,
// Design/API_Endpoints_Design.md §4.25): a connector wrapper around the real
// driver records per-statement verb/duration/error/rows and per-connection
// dial times into the metrics registry — no call site changes, because every
// query in the API flows through store.DB.
//
// PHI rule (REQ-API-005/006): the statement text is read only to classify its
// leading word onto the closed verb enum; it is never retained, counted per
// value, or logged, and parameters and row values are never observed.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	sqlite "modernc.org/sqlite"

	"csms/api/internal/metrics"
)

// openDB returns the instrumented *sql.DB when reg != nil, else exactly today's
// sql.Open path. The wrapped connector comes from the drivers' own exported
// constructors (the pattern modernc.org/sqlite documents for instrumentation):
// no process-global sql.Register, so repeated db.Open in tests cannot collide,
// and context-aware dialing is preserved. DSN handling — including the SQLite
// ?_pragma= query — is the drivers' own.
func openDB(d Dialect, driverName, dsn string, reg *metrics.Registry) (*sql.DB, error) {
	if reg == nil {
		return sql.Open(driverName, dsn)
	}
	var (
		base driver.Connector
		err  error
	)
	switch d {
	case DialectSQLite:
		base, err = sqlite.NewConnector(dsn)
	case DialectMariaDB:
		var mcfg *mysql.Config
		if mcfg, err = mysql.ParseDSN(dsn); err == nil {
			base, err = mysql.NewConnector(mcfg)
		}
	default:
		return nil, errors.New("openDB: unknown dialect")
	}
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(insConnector{base: base, reg: reg}), nil
}

// sqlVerb classifies a statement by its leading word onto the closed enum of
// metrics.dbVerbs. PRAGMA, BEGIN issued raw and DDL are OTHER; COMMIT and
// ROLLBACK as statements count like any other (transaction commits through
// BeginTx/Commit are timed by insTx below).
func sqlVerb(query string) string {
	q := strings.TrimLeft(query, " \t\n\r(")
	word := q
	if i := strings.IndexAny(q, " \t\n\r("); i >= 0 {
		word = q[:i]
	}
	switch up := strings.ToUpper(word); up {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "COMMIT", "ROLLBACK":
		return up
	default:
		return "OTHER"
	}
}

type insConnector struct {
	base driver.Connector
	reg  *metrics.Registry
}

func (c insConnector) Connect(ctx context.Context) (driver.Conn, error) {
	t := time.Now()
	dc, err := c.base.Connect(ctx)
	c.reg.ObserveDial(time.Since(t), err != nil)
	if err != nil {
		return nil, err
	}
	return &insConn{base: dc, reg: c.reg}, nil
}

func (c insConnector) Driver() driver.Driver { return c.base.Driver() }

// insConn wraps a driver.Conn. Every optional interface is implemented with
// the delegate-or-ErrSkip pattern; the legacy Queryer/Execer fallbacks matter
// because modernc.org/sqlite implements those and not the Context variants —
// skipping them would silently drop every fast-path observation into the
// generic prepare path (which this wrapper measures too, via insStmt).
type insConn struct {
	base driver.Conn
	reg  *metrics.Registry
}

func (c *insConn) Close() error { return c.base.Close() }

func (c *insConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.base.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &insStmt{Stmt: st, reg: c.reg, verb: sqlVerb(query)}, nil
}

func (c *insConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.base.(driver.ConnPrepareContext); ok {
		st, err := pc.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &insStmt{Stmt: st, reg: c.reg, verb: sqlVerb(query)}, nil
	}
	return nil, driver.ErrSkip // database/sql falls back to Prepare above
}

func (c *insConn) Begin() (driver.Tx, error) {
	tx, err := c.base.Begin()
	if err != nil {
		return nil, err
	}
	return &insTx{base: tx, reg: c.reg}, nil
}

func (c *insConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if bt, ok := c.base.(driver.ConnBeginTx); ok {
		tx, err := bt.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &insTx{base: tx, reg: c.reg}, nil
	}
	return nil, driver.ErrSkip // database/sql falls back to Begin above
}

func (c *insConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	verb := sqlVerb(query)
	t := time.Now()
	if q, ok := c.base.(driver.ExecerContext); ok {
		res, err := q.ExecContext(ctx, query, args)
		c.reg.ObserveDB(verb, time.Since(t), rowsAffected(res), err != nil)
		return res, err
	}
	if q, ok := c.base.(driver.Execer); ok {
		if vals, ok := positional(args); ok {
			res, err := q.Exec(query, vals)
			c.reg.ObserveDB(verb, time.Since(t), rowsAffected(res), err != nil)
			return res, err
		}
	}
	return nil, driver.ErrSkip // generic prepare path; insStmt measures it
}

func (c *insConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	verb := sqlVerb(query)
	t := time.Now()
	if q, ok := c.base.(driver.QueryerContext); ok {
		rows, err := q.QueryContext(ctx, query, args)
		if err != nil {
			c.reg.ObserveDB(verb, time.Since(t), -1, true)
			return nil, err
		}
		return &insRows{Rows: rows, reg: c.reg, verb: verb, start: t}, nil
	}
	if q, ok := c.base.(driver.Queryer); ok {
		if vals, ok := positional(args); ok {
			rows, err := q.Query(query, vals)
			if err != nil {
				c.reg.ObserveDB(verb, time.Since(t), -1, true)
				return nil, err
			}
			return &insRows{Rows: rows, reg: c.reg, verb: verb, start: t}, nil
		}
	}
	return nil, driver.ErrSkip // generic prepare path; insStmt measures it
}

// ResetSession / IsValid / CheckNamedValue keep the pooled-session semantics of
// the wrapped driver intact.

func (c *insConn) ResetSession(ctx context.Context) error {
	if rs, ok := c.base.(driver.SessionResetter); ok {
		return rs.ResetSession(ctx)
	}
	return driver.ErrSkip
}

func (c *insConn) IsValid() bool {
	if v, ok := c.base.(driver.Validator); ok {
		return v.IsValid()
	}
	return true // drivers without a validator are assumed always valid
}

func (c *insConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ck, ok := c.base.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	return driver.ErrSkip // default conversion rules
}

// insStmt measures each execution of a prepared statement under the verb its
// query classified at prepare time.
type insStmt struct {
	driver.Stmt
	reg  *metrics.Registry
	verb string
}

func (s *insStmt) Exec(args []driver.Value) (driver.Result, error) {
	t := time.Now()
	res, err := s.Stmt.Exec(args)
	s.reg.ObserveDB(s.verb, time.Since(t), rowsAffected(res), err != nil)
	return res, err
}

func (s *insStmt) Query(args []driver.Value) (driver.Rows, error) {
	t := time.Now()
	rows, err := s.Stmt.Query(args)
	if err != nil {
		s.reg.ObserveDB(s.verb, time.Since(t), -1, true)
		return nil, err
	}
	return &insRows{Rows: rows, reg: s.reg, verb: s.verb, start: t}, nil
}

func (s *insStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if ec, ok := s.Stmt.(driver.StmtExecContext); ok {
		t := time.Now()
		res, err := ec.ExecContext(ctx, args)
		s.reg.ObserveDB(s.verb, time.Since(t), rowsAffected(res), err != nil)
		return res, err
	}
	return nil, driver.ErrSkip // database/sql falls back to the legacy Exec above
}

func (s *insStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if qc, ok := s.Stmt.(driver.StmtQueryContext); ok {
		t := time.Now()
		rows, err := qc.QueryContext(ctx, args)
		if err != nil {
			s.reg.ObserveDB(s.verb, time.Since(t), -1, true)
			return nil, err
		}
		return &insRows{Rows: rows, reg: s.reg, verb: s.verb, start: t}, nil
	}
	return nil, driver.ErrSkip // database/sql falls back to the legacy Query above
}

// insRows counts rows as they are consumed and records the observation once —
// at exhaustion (io.EOF), at an iteration error, or at Close (covers abandoned
// result sets, which database/sql closes). Duration therefore spans full
// result streaming, not just time-to-first-row.
type insRows struct {
	driver.Rows
	reg   *metrics.Registry
	verb  string
	start time.Time
	n     int64
	done  bool // single-threaded by database/sql contract
}

func (r *insRows) record(failed bool) {
	if r.done {
		return
	}
	r.done = true
	r.reg.ObserveDB(r.verb, time.Since(r.start), r.n, failed)
}

func (r *insRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	switch {
	case errors.Is(err, io.EOF):
		r.record(false)
	case err != nil:
		r.record(true)
	default:
		r.n++
	}
	return err
}

func (r *insRows) Close() error {
	r.record(false)
	return r.Rows.Close()
}

// insTx times COMMIT and ROLLBACK — this is where SQLite's fsync cost shows up.
type insTx struct {
	base driver.Tx
	reg  *metrics.Registry
}

func (t *insTx) Commit() error {
	start := time.Now()
	err := t.base.Commit()
	t.reg.ObserveDB("COMMIT", time.Since(start), 0, err != nil)
	return err
}

func (t *insTx) Rollback() error {
	start := time.Now()
	err := t.base.Rollback()
	t.reg.ObserveDB("ROLLBACK", time.Since(start), 0, err != nil)
	return err
}

// positional maps database/sql's named values onto the legacy []driver.Value
// slice; it reports false when the call uses true named parameters (which the
// legacy interfaces cannot express — the caller then takes ErrSkip).
func positional(args []driver.NamedValue) ([]driver.Value, bool) {
	if len(args) == 0 {
		return nil, true
	}
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, false
		}
		vals[i] = a.Value
	}
	return vals, true
}

// rowsAffected is the best-effort write row count; -1 means "the driver could
// not say" and contributes nothing to metrics.
func rowsAffected(res driver.Result) int64 {
	if res == nil {
		return -1
	}
	n, err := res.RowsAffected()
	if err != nil {
		return -1
	}
	return n
}
