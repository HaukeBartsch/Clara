package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"csms/api/internal/config"
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

// Open connects to the configured database (REQ-DB-002), sets sane pool
// limits, and verifies connectivity (REQ-TECH-014: fail fast on an
// unreachable database).
func Open(cfg *config.Config) (*Store, error) {
	var (
		driver string
		dsn    string
		d      Dialect
	)
	switch cfg.DBConnection {
	case "sqlite":
		driver = "sqlite"
		d = DialectSQLite
		// busy_timeout on every pooled connection: SQLite serializes its
		// writers, and without a timeout a write that meets a concurrent
		// reader fails immediately with SQLITE_BUSY (observed as transient
		// 500s under load). The driver applies _pragma parameters per
		// connection it opens.
		dsn = cfg.DBDatabase + "?_pragma=busy_timeout(5000)"
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

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Single connection per request, explicit transactions (REQ-DB-026); a
	// modest pool is sufficient and keeps SQLite from over-subscribing.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(0)

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database (%s): %w", cfg.DBConnection, err)
	}

	if d == DialectSQLite {
		if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable foreign keys: %w", err)
		}
	}

	return &Store{DB: db, Dialect: d, Cfg: cfg}, nil
}

// Close releases the pool.
func (s *Store) Close() error { return s.DB.Close() }

// dialTimeout bounds the startup connectivity check (REQ-TECH-014).
const dialTimeout = 10 * time.Second

// nowUTC returns the server UTC time (REQ-DB-005, GD-7) in the canonical
// DATETIME layout.
func nowUTC() string { return time.Now().UTC().Format(datetimeLayout) }
