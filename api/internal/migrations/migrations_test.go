package migrations

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"
)

// wantFiles is the migration set both dialects carry, in application order: the
// runner applies For's output by index (REQ-DB-003), so this list is the schema
// history. A new migration goes into api/internal/migrations/{sqlite,mariadb}/
// for both dialects (AGENTS.md) and into this list here.
var wantFiles = []string{
	"0001_schema.sql",
	"0002_seed.sql",
	"0003_validation_types.sql",
	"0004_project_modes.sql",
	"0005_system_settings.sql",
	"0006_rate_limit_block.sql",
}

var dialects = []string{"sqlite", "mariadb"}

func TestForReturnsMigrationsInOrder(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			got, err := For(dialect)
			if err != nil {
				t.Fatalf("For(%q): %v", dialect, err)
			}
			if len(got) == 0 {
				t.Fatalf("For(%q) returned no migrations", dialect)
			}
			// Order is load-bearing: version numbers come from the position in
			// the sorted list.
			if !sort.StringsAreSorted(got) {
				t.Errorf("For(%q) = %v, want names sorted ascending", dialect, got)
			}
			if len(got) != len(wantFiles) {
				t.Fatalf("For(%q) = %d files %v, want %d %v", dialect, len(got), got, len(wantFiles), wantFiles)
			}
			for i := range got {
				if got[i] != wantFiles[i] {
					t.Errorf("For(%q)[%d] = %q, want %q", dialect, i, got[i], wantFiles[i])
				}
			}
		})
	}
}

// TestForUnknownDialect covers the startup path where DB_CONNECTION names an
// engine we have no migrations for; the error must name the offending value
// (REQ-CFG-004 style diagnostics) rather than fail silently with no files.
func TestForUnknownDialect(t *testing.T) {
	cases := []struct {
		label   string
		dialect string
	}{
		{"unsupported engine", "postgres"},
		{"near miss", "sqlite3"},
		{"empty", ""},
		{"wrong case", "SQLITE"},
		{"relative path", "./sqlite"},
		{"a file, not a dialect directory", "sqlite/0001_schema.sql"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got, err := For(tc.dialect)
			if err == nil {
				t.Fatalf("For(%q) = %v, want an error", tc.dialect, got)
			}
			if want := fmt.Sprintf("%q", tc.dialect); !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the dialect %s", err, want)
			}
			if got != nil {
				t.Errorf("For(%q) = %v alongside an error, want nil", tc.dialect, got)
			}
		})
	}
}

// TestForReturnsOnlySQLFiles checks the filter: embedded directories and any
// stray non-SQL file must not reach the runner as migrations.
func TestForReturnsOnlySQLFiles(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			got, err := For(dialect)
			if err != nil {
				t.Fatalf("For(%q): %v", dialect, err)
			}
			for _, name := range got {
				if !strings.HasSuffix(name, ".sql") {
					t.Errorf("For(%q) entry %q is not a .sql file", dialect, name)
				}
				// Bare file names only — the dialect directory must not leak in.
				if name != path.Base(name) {
					t.Errorf("For(%q) entry %q carries a directory component", dialect, name)
				}
				if name == "sqlite" || name == "mariadb" {
					t.Errorf("For(%q) returned the directory %q as a migration", dialect, name)
				}
			}
		})
	}
}

func TestReadEveryMigration(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			names, err := For(dialect)
			if err != nil {
				t.Fatalf("For(%q): %v", dialect, err)
			}
			for _, name := range names {
				b, err := Read(dialect, name)
				if err != nil {
					t.Errorf("Read(%q, %q): %v", dialect, name, err)
					continue
				}
				if len(bytes.TrimSpace(b)) == 0 {
					t.Errorf("Read(%q, %q) returned no SQL", dialect, name)
				}
			}
		})
	}
}

func TestReadMissingMigration(t *testing.T) {
	const missing = "9999_not_a_migration.sql"
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			b, err := Read(dialect, missing)
			if err == nil {
				t.Fatalf("Read(%q, %q) = %d bytes, want an error", dialect, missing, len(b))
			}
			// The message names both halves of the path so a failed startup says
			// which migration file is unreadable.
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error %q does not name the migration %q", err, missing)
			}
			if !strings.Contains(err.Error(), dialect) {
				t.Errorf("error %q does not name the dialect %q", err, dialect)
			}
		})
	}
}

// TestDialectsStayInStep enforces AGENTS.md — "add a file for both dialects
// when a change is dialect-specific". A migration present for one dialect only
// means a deployment on the other silently skips a schema change, so drift here
// fails loudly rather than at the first query against the missing column.
func TestDialectsStayInStep(t *testing.T) {
	sqlite, err := For("sqlite")
	if err != nil {
		t.Fatalf("For(\"sqlite\"): %v", err)
	}
	mariadb, err := For("mariadb")
	if err != nil {
		t.Fatalf("For(\"mariadb\"): %v", err)
	}
	sqliteSet, mariadbSet := fileSet(sqlite), fileSet(mariadb)
	for _, name := range sqlite {
		if !mariadbSet[name] {
			t.Errorf("migration %s exists for sqlite but not mariadb (internal/migrations/mariadb/ is missing it)", name)
		}
	}
	for _, name := range mariadb {
		if !sqliteSet[name] {
			t.Errorf("migration %s exists for mariadb but not sqlite (internal/migrations/sqlite/ is missing it)", name)
		}
	}
	// Position matters as much as membership: the runner numbers migrations by
	// index, so the two dialects must order the shared history identically.
	for i := 0; i < min(len(sqlite), len(mariadb)); i++ {
		if sqlite[i] != mariadb[i] {
			t.Errorf("position %d: sqlite applies %q, mariadb applies %q — keep both directories in the same order",
				i, sqlite[i], mariadb[i])
		}
	}
}

func fileSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}
