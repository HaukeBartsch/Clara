package db

import (
	"context"
	"database/sql"
	"fmt"
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
}

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
		dsn = cfg.DBDatabase
	case "mariadb":
		driver = "mysql"
		d = DialectMariaDB
		dsn = fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true",
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
