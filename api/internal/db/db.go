package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"csms/api/internal/config"
	"csms/api/internal/metrics"
)

// Dialect identifies the storage engine in use.
type Dialect string

const (
	DialectSQLite  Dialect = "sqlite"
	DialectMariaDB Dialect = "mariadb"
)

// Store bundles the *sql.DB with its dialect and config. It is the single
// writer to the data store.
type Store struct {
	DB      *sql.DB
	Dialect Dialect
	Cfg     *config.Config

	// structGen invalidates cached views of project structure (arms, events,
	// instruments, fields, instrument-event mapping); see StructureGeneration.
	structGen atomic.Uint64

	settingsGen        atomic.Uint64
	settingsCacheMu    sync.Mutex
	settingsCache      map[string]string
	settingsCacheGen   uint64
	settingsCacheValid bool
}

// StructureGeneration returns the process-local structure generation: a
// counter advanced by BumpStructureCache on every write that can change any
// project's structure. Callers caching structure across requests tag each
// cached value with the generation observed BEFORE building it — a later
// mismatch proves a write happened at or after the read started, so the
// entry is dropped and rebuilt. The counter is in-memory: it is correct for
// this API process only (the deployment runs one instance behind nginx,
// Technology_Stack_Design.md §5); direct database edits bypass it like they
// bypass every other in-process state.
func (s *Store) StructureGeneration() uint64 { return s.structGen.Load() }

// BumpStructureCache advances the structure generation after a successful
// structure write. The db layer's own write methods call it; the
// administration API's raw-SQL design transactions are covered by the
// structureInvalidation middleware in httpapi. Extra bumps are harmless —
// they only cost one dictionary reload per cached project.
func (s *Store) BumpStructureCache() { s.structGen.Add(1) }

// BumpSystemSettingsCache invalidates the process-local system settings view.
// Call only after the transaction writing settings has committed.
func (s *Store) BumpSystemSettingsCache() { s.settingsGen.Add(1) }

// Open connects to the configured database (REQ-DB-002), sets sane pool
// limits, and verifies connectivity (REQ-TECH-014: fail fast on an
// unreachable database). When reg != nil the connection is instrumented
// transparently (REQ-API-163, driverwrap.go); callers that do not want
// metrics — tests included — pass nil and get the plain sql.Open path.
func Open(cfg *config.Config, reg *metrics.Registry) (*Store, error) {
	var (
		driver string
		dsn    string
		d      Dialect
	)
	switch cfg.DBConnection {
	case "sqlite":
		driver = "sqlite"
		d = DialectSQLite
		// Per-connection settings, applied by the driver to every connection it
		// opens (Design/Database_Schema_Design.md §10):
		//
		//   foreign_keys  enforcement, as on InnoDB. It used to be set with one
		//                 ExecContext after opening the pool, which enabled it on
		//                 whichever connection answered and left the rest of the
		//                 pool without it.
		//   journal_mode  WAL (decision 2026-10-25): SQLite is development and
		//                 test only (REQ-TECH-005), and in rollback-journal mode a
		//                 writer excludes readers entirely, so any concurrency the
		//                 pool allows surfaces as SQLITE_BUSY instead of behaving
		//                 like production. WAL makes readers and a single writer
		//                 coexist — it does NOT make reading on a second connection
		//                 inside a transaction correct: that still sees the state
		//                 from before the transaction began (see the regression
		//                 guard dataapi.TestRecordImportExistingRowReadsInsideTransaction).
		//   busy_timeout  SQLite serializes writers; without a timeout a write that
		//                 meets a concurrent reader fails immediately (transient
		//                 500s under load before WAL).
		dsn = cfg.DBDatabase + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	case "mariadb":
		// No parseTime: DATE/DATETIME then scan as text in the same canonical
		// layout SQLite returns ("2006-01-02 15:04:05", REQ-DB-005), instead of
		// time.Time rendered as RFC 3339 wherever a column lands in a string.
		driver = "mysql"
		d = DialectMariaDB
		dsn = fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true",
			cfg.DBUsername, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBDatabase,
		)
	default:
		return nil, fmt.Errorf("unknown DB_CONNECTION %q", cfg.DBConnection)
	}

	db, err := openDB(d, driver, dsn, reg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	configurePool(db, d)

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database (%s): %w", cfg.DBConnection, err)
	}

	if d == DialectSQLite {
		// Confirm the per-connection pragmas landed (§10). Foreign keys are a
		// correctness property, so a pool without them fails startup. WAL is not:
		// a filesystem with no shared memory (NFS and similar) silently keeps the
		// rollback journal, which deserves a warning rather than a refused start.
		var (
			fk   int
			mode string
		)
		if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			db.Close()
			return nil, fmt.Errorf("read foreign_keys pragma: %w", err)
		}
		if fk != 1 {
			db.Close()
			return nil, fmt.Errorf("foreign_keys = %d, want 1 — the DSN pragma was not applied", fk)
		}
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			db.Close()
			return nil, fmt.Errorf("read journal_mode pragma: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			slog.Warn("SQLite is not running in WAL mode; readers and a writer will exclude each other",
				"journal_mode", mode)
		}
	}

	return &Store{DB: db, Dialect: d, Cfg: cfg}, nil
}

func configurePool(db *sql.DB, d Dialect) {
	if d == DialectMariaDB {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(25)
		// Bound idle retention and connection age to limit stale reuse and
		// periodically refresh connections.
		db.SetConnMaxIdleTime(5 * time.Minute)
		db.SetConnMaxLifetime(30 * time.Minute)
		return
	}

	// SQLite serializes writes, so keep its pool modest.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(0)
}

// Close releases the pool.
func (s *Store) Close() error { return s.DB.Close() }

// dialTimeout bounds the startup connectivity check (REQ-TECH-014).
const dialTimeout = 10 * time.Second

// nowUTC returns the server UTC time (REQ-DB-005, GD-7) in the canonical
// DATETIME layout.
func nowUTC() string { return time.Now().UTC().Format(datetimeLayout) }
