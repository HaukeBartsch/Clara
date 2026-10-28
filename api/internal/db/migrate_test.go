package db

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"csms/api/internal/config"
	"csms/api/internal/migrations"
	"csms/api/internal/testdb"
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
	testdb.Use(t, cfg)
	s, err := Open(cfg, nil)
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
		"projects", "users", "roles", "role_arms", "role_grants", "user_projects", "arms",
		"events", "instruments", "instrument_events", "fields",
		"calculated_dependencies", "data", "record_entities", "dag_groups",
		"dag_memberships", "anon_offsets", "survey_links", "languages",
		"i18n_strings", "audit_events", "audit_record_views", "validation_types",
		"project_staging",
	}
	for _, table := range want {
		var n int
		q := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`
		if s.Dialect == DialectMariaDB {
			q = `SELECT COUNT(*) FROM information_schema.TABLES
			     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' AND TABLE_NAME = ?`
		}
		if err := s.DB.QueryRowContext(ctx, q, table).Scan(&n); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}

	// The mode column added by the GD-20 migration, NOT NULL with the starting
	// mode as its default so existing rows land in development (REQ-DB-034).
	var notNull, hasDefault int
	q := `SELECT "notnull", dflt_value IS NOT NULL
		 FROM pragma_table_info('projects') WHERE name = 'mode'`
	if s.Dialect == DialectMariaDB {
		q = `SELECT IS_NULLABLE = 'NO', COLUMN_DEFAULT IS NOT NULL
		     FROM information_schema.COLUMNS
		     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'projects' AND COLUMN_NAME = 'mode'`
	}
	if err := s.DB.QueryRowContext(ctx, q).Scan(&notNull, &hasDefault); err != nil {
		t.Fatalf("projects.mode column: %v", err)
	}
	if notNull != 1 || hasDefault != 1 {
		t.Errorf("projects.mode notnull=%d hasDefault=%d, want both 1", notNull, hasDefault)
	}
}

// TestMigrateDataLookupIndexes checks the index set migrations 0016 and 0019
// leave on `data` (REQ-DB-017) on either engine — on MariaDB the order of
// 0016's statements is what lets it apply at all, see the migration's own
// comment.
func TestMigrateDataLookupIndexes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Index name -> its columns in key order, and the indexes kept as a hash —
	// which answer their full key only, never a prefix of it.
	q := `SELECT m.name, ii.name, 'BTREE' FROM sqlite_master AS m JOIN pragma_index_info(m.name) AS ii
	      WHERE m.type = 'index' AND m.tbl_name = 'data' ORDER BY m.name, ii.seqno`
	if s.Dialect == DialectMariaDB {
		q = `SELECT INDEX_NAME, COLUMN_NAME, INDEX_TYPE FROM information_schema.STATISTICS
		     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'data' ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	}
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("query data indexes: %v", err)
	}
	indexes := make(map[string][]string)
	hashed := make(map[string]bool)
	for rows.Next() {
		var index, column, kind string
		if err := rows.Scan(&index, &column, &kind); err != nil {
			rows.Close()
			t.Fatalf("scan data index: %v", err)
		}
		indexes[index] = append(indexes[index], column)
		hashed[index] = kind == "HASH"
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate data indexes: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close data indexes: %v", err)
	}

	for _, name := range []string{"idx_data_project", "idx_data_record"} {
		if _, ok := indexes[name]; ok {
			t.Errorf("obsolete data index %q still exists", name)
		}
	}
	want := map[string][]string{
		"idx_data_project_field": {"project_id", "field_name"},
		"idx_data_project_event": {"project_id", "unique_event_name"},
	}
	if s.Dialect == DialectMariaDB {
		want["idx_data_project_record"] = []string{"project_id", "record_id"}
	}
	for index, wantColumns := range want {
		gotColumns, ok := indexes[index]
		if !ok {
			t.Errorf("data index %q is missing", index)
			continue
		}
		if !slices.Equal(gotColumns, wantColumns) {
			t.Errorf("columns for %s = %v, want %v", index, gotColumns, wantColumns)
		}
	}

	// A single-record read (`WHERE project_id = ? AND record_id = ?`) needs an
	// index led by those two columns that can answer a prefix: the unique key
	// on SQLite, idx_data_project_record on MariaDB, whose unique key is a hash.
	byRecord := false
	for index, columns := range indexes {
		if !hashed[index] && len(columns) >= 2 && slices.Equal(columns[:2], []string{"project_id", "record_id"}) {
			byRecord = true
		}
	}
	if !byRecord {
		t.Errorf("no prefix-capable data index starts with (project_id, record_id): %v (hashed %v)", indexes, hashed)
	}

	// MariaDB commits each DDL statement on its own, so a run of 0016 or 0019
	// that stopped halfway stays half done: every statement must apply again
	// over its own result for the next start to finish it.
	if s.Dialect == DialectMariaDB {
		for _, name := range []string{"0016_data_lookup_indexes.sql", "0019_data_record_index.sql"} {
			sqlBytes, err := migrations.Read(string(s.Dialect), name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if _, err := s.DB.ExecContext(ctx, string(sqlBytes)); err != nil {
				t.Errorf("%s does not apply again over its own result: %v", name, err)
			}
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
	if len(settings) != 3 {
		t.Errorf("system_settings count = %d after re-migrate, want 3", len(settings))
	}
	if settings["rate_limit_enabled"] != "false" || settings["rate_limit_rpm"] != "600" ||
		settings["rate_limit_block_minutes"] != "10" {
		t.Errorf("system_settings seed = %v, want rate_limit_enabled=false, rate_limit_rpm=600, rate_limit_block_minutes=10", settings)
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
