package db

import (
	"context"
	"path/filepath"
	"testing"

	"csms/api/internal/config"
	"csms/api/internal/migrations"
)

// wantSchemaVersion is the version a fully migrated store reports: Migrate
// numbers files by their position in the sorted list, so it is one per
// migration file rather than the highest number in a file name (five files
// apply as versions 1–5). Derived from the embedded list so adding a migration
// does not break this test. Keep file-name numbers unique and append-only: a
// gap or a renumbered prefix would make an existing database re-run or skip a
// migration.
func wantSchemaVersion(t *testing.T) int {
	t.Helper()
	files, err := migrations.For(string(DialectSQLite))
	if err != nil {
		t.Fatalf("migrations.For: %v", err)
	}
	return len(files)
}

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
	if want := wantSchemaVersion(t); v != want {
		t.Fatalf("SchemaVersion = %d, want %d (every migration file applied)", v, want)
	}

	// Every core table must exist.
	want := []string{
		"projects", "users", "roles", "role_arms", "user_projects", "arms",
		"events", "instruments", "instrument_events", "fields",
		"calculated_dependencies", "data", "record_entities", "dag_groups",
		"dag_memberships", "anon_offsets", "survey_links", "languages",
		"i18n_strings", "audit_events", "audit_record_views", "validation_types",
		"project_staging",
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

	// The mode column added by the GD-20 migration, NOT NULL with the starting
	// mode as its default so existing rows land in development (REQ-DB-034).
	var notNull, hasDefault int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT "notnull", dflt_value IS NOT NULL
		 FROM pragma_table_info('projects') WHERE name = 'mode'`).
		Scan(&notNull, &hasDefault); err != nil {
		t.Fatalf("projects.mode column: %v", err)
	}
	if notNull != 1 || hasDefault != 1 {
		t.Errorf("projects.mode notnull=%d hasDefault=%d, want both 1", notNull, hasDefault)
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
	if want := wantSchemaVersion(t); v != want {
		t.Fatalf("SchemaVersion = %d after re-migrate, want %d", v, want)
	}
	// Languages must not be duplicated by the re-seed.
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM languages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("languages count = %d after re-migrate, want 3", n)
	}
	// Neither must the validation types (REQ-VAL-043).
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM validation_types`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("validation_types count = %d after re-migrate, want 4", n)
	}
	// Neither must the system settings (REQ-DB-037).
	settings, err := s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if len(settings) != 2 {
		t.Errorf("system_settings count = %d after re-migrate, want 2", len(settings))
	}
	if settings["rate_limit_enabled"] != "false" || settings["rate_limit_rpm"] != "600" {
		t.Errorf("system_settings seed = %v, want rate_limit_enabled=false, rate_limit_rpm=600", settings)
	}
}

func TestMigrateSeedsValidationTypes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	types, err := s.ListValidationTypes(ctx)
	if err != nil {
		t.Fatalf("ListValidationTypes: %v", err)
	}
	seen := map[string]ValidationType{}
	for _, vt := range types {
		seen[vt.Name] = vt
	}
	// The seeded set and grammars of REQ-VAL-043 / Data_Validation_Design.md §4.
	for name, wantRegex := range map[string]string{
		"email":               `^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`,
		"MRN":                 `^[0-9]{11}$`,
		"international phone": `^\+[1-9][0-9 ]{7,14}$`,
		"national phone":      `^[0-9]{8}$`,
	} {
		vt, ok := seen[name]
		if !ok {
			t.Errorf("validation type %q not seeded", name)
			continue
		}
		if vt.Regex != wantRegex {
			t.Errorf("validation type %q regex = %q, want %q", name, vt.Regex, wantRegex)
		}
		if !vt.Builtin {
			t.Errorf("seeded validation type %q not marked builtin", name)
		}
	}
}
