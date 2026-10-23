package db

import (
	"context"
	"testing"
)

// The system settings registry (REQ-DB-037) holds runtime settings as raw JSON
// scalar text; interpretation happens at the API boundary (REQ-API-112), so
// the repository's whole job is to hand back exactly what was stored.

func TestSystemSettingsSeed(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	settings, err := s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	// The rate limiter ships disabled with a 600/min budget (REQ-CFG-020);
	// enabling it is an operator decision recorded here, not a restart.
	for key, want := range map[string]string{
		"rate_limit_enabled": "false",
		"rate_limit_rpm":     "600",
	} {
		if got := settings[key]; got != want {
			t.Errorf("system_settings[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestSystemSettingsCacheInvalidation(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	settings, err := s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings initial read: %v", err)
	}
	settings["rate_limit_rpm"] = "caller mutation"
	settings, err = s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings cached read: %v", err)
	}
	if settings["rate_limit_rpm"] != "600" {
		t.Fatalf("cached rate_limit_rpm = %q, want 600 (caller must not mutate cache)", settings["rate_limit_rpm"])
	}

	if _, err := s.DB.ExecContext(ctx,
		"UPDATE system_settings SET value = '120' WHERE `key` = 'rate_limit_rpm'"); err != nil {
		t.Fatalf("direct settings update: %v", err)
	}
	settings, err = s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings before invalidation: %v", err)
	}
	if settings["rate_limit_rpm"] != "600" {
		t.Fatalf("cached rate_limit_rpm = %q before invalidation, want 600", settings["rate_limit_rpm"])
	}

	s.BumpSystemSettingsCache()
	settings, err = s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings after invalidation: %v", err)
	}
	if settings["rate_limit_rpm"] != "120" {
		t.Fatalf("rate_limit_rpm after invalidation = %q, want 120", settings["rate_limit_rpm"])
	}

	if err := s.SetSystemSetting(ctx, "rate_limit_rpm", "300"); err != nil {
		t.Fatalf("SetSystemSetting: %v", err)
	}
	settings, err = s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings after SetSystemSetting: %v", err)
	}
	if settings["rate_limit_rpm"] != "300" {
		t.Fatalf("rate_limit_rpm after SetSystemSetting = %q, want 300", settings["rate_limit_rpm"])
	}
}

// SetSystemSetting upserts: a new key inserts, an existing one is replaced in
// place (REQ-API-112). The update path must not leave a second row behind.
func TestSetSystemSettingUpserts(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	// Update branch: a key the seed already carries.
	if err := s.SetSystemSetting(ctx, "rate_limit_rpm", "120"); err != nil {
		t.Fatalf("SetSystemSetting (update of a seeded key): %v", err)
	}
	settings, err := s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if settings["rate_limit_rpm"] != "120" {
		t.Errorf("rate_limit_rpm = %q after update, want 120", settings["rate_limit_rpm"])
	}

	// Insert branch: a brand-new setting name.
	if err := s.SetSystemSetting(ctx, "note_taking_enabled", "true"); err != nil {
		t.Fatalf("SetSystemSetting (insert): %v", err)
	}
	settings, err = s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if settings["note_taking_enabled"] != "true" {
		t.Errorf("note_taking_enabled = %q, want true", settings["note_taking_enabled"])
	}

	// One row per key — the update must not have inserted a duplicate.
	var n int
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM system_settings WHERE `key` = 'rate_limit_rpm'").Scan(&n); err != nil {
		t.Fatalf("count rate_limit_rpm: %v", err)
	}
	if n != 1 {
		t.Errorf("system_settings holds %d rows for rate_limit_rpm, want 1", n)
	}
}

// Values are stored verbatim: the registry does not interpret or reject a
// scalar, which is what keeps adding a setting an insert rather than a schema
// change (DEV-DB-11). Validation of a bad value belongs to the API.
func TestSetSystemSettingStoresRawText(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	for _, raw := range []string{"true", "600", `"a string"`, "null", ""} {
		if err := s.SetSystemSetting(ctx, "raw_probe", raw); err != nil {
			t.Fatalf("SetSystemSetting(%q): %v", raw, err)
		}
		settings, err := s.SystemSettings(ctx)
		if err != nil {
			t.Fatalf("SystemSettings: %v", err)
		}
		if got := settings["raw_probe"]; got != raw {
			t.Errorf("stored %q, read back %q", raw, got)
		}
	}
}

// A setting nobody set reads as absent rather than as a zero value, so the
// caller's documented fallback applies (REQ-API-112).
func TestSystemSettingsAbsentKey(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	settings, err := s.SystemSettings(ctx)
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if _, ok := settings["no_such_setting"]; ok {
		t.Errorf("unset setting present in the registry map")
	}
}
