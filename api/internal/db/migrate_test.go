package db

import (
	"context"
	"path/filepath"
	"testing"

	"csms/api/internal/config"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		AppEnv:               "development",
		DBConnection:         "sqlite",
		DBDatabase:           filepath.Join(dir, "test.sqlite"),
		AnonSalt:             "test-salt",
		InternalServiceToken: "test-token",
	}
	s, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateCreatesSchema(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", v)
	}

	// Every core table must exist.
	want := []string{
		"projects", "users", "roles", "role_arms", "user_projects", "arms",
		"events", "instruments", "instrument_events", "fields",
		"calculated_dependencies", "data", "record_entities", "dag_groups",
		"dag_memberships", "anon_offsets", "survey_links", "languages",
		"i18n_strings", "audit_events", "audit_record_views",
	}
	for _, table := range want {
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}
}

func TestMigrateSeedsLanguages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT code, display_name, enabled FROM languages ORDER BY code`)
	if err != nil {
		t.Fatalf("query languages: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var code, name string
		var enabled int
		if err := rows.Scan(&code, &name, &enabled); err != nil {
			t.Fatal(err)
		}
		seen[code] = true
		if enabled != 1 {
			t.Errorf("language %q not enabled", code)
		}
	}
	for _, code := range []string{"en", "nb", "nn"} {
		if !seen[code] {
			t.Errorf("language %q not seeded", code)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// Second run must be a no-op and not error.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	v, _ := s.SchemaVersion(ctx)
	if v != 2 {
		t.Fatalf("SchemaVersion = %d after re-migrate, want 2", v)
	}
	// Languages must not be duplicated by the re-seed.
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM languages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("languages count = %d after re-migrate, want 3", n)
	}
}
