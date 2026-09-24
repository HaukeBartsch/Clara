package db

import (
	"context"
	"fmt"

	"csms/api/internal/migrations"
)

// Migrate applies any unapplied migrations for the store's dialect
// (REQ-DB-003) and is idempotent. Applied versions are recorded in the
// schema_version table.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	files, err := migrations.For(string(s.Dialect))
	if err != nil {
		return err
	}

	for i, name := range files {
		version := i + 1
		if applied[version] {
			continue
		}
		sqlBytes, err := migrations.Read(string(s.Dialect), name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(ctx, version, string(sqlBytes), name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// applyMigration runs a migration file in a transaction and records its
// version on success. A failure aborts the transaction and leaves the
// version unrecorded (REQ-DB-003 idempotency).
func (s *Store) applyMigration(ctx context.Context, version int, sqlText, name string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply migration %s (v%d): %w", name, version, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, version, nowUTC()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %s (v%d): %w", name, version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s (v%d): %w", name, version, err)
	}
	return nil
}

// SchemaVersion returns the highest applied schema version (0 if none).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}
