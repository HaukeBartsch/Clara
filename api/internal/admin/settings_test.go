package admin

import (
	"context"
	"net/http"
	"testing"

	"csms/api/internal/db"
)

// GET returns the seeded defaults (REQ-API-112): disabled, 600 rpm, a
// ten-minute blockout of an over-budget source IP (REQ-API-113).
func TestSettingsGetDefaults(t *testing.T) {
	e := newEnv(t)
	adminUser := e.mustAdmin("settings-admin@example.org")

	rec := e.do("GET", "/api/v1/settings", nil, adminUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var s SettingsObject
	e.decode(rec, &s)
	if s.RateLimitEnabled || s.RateLimitRPM != 600 || s.RateLimitBlockMinutes != 10 {
		t.Errorf("settings = %+v, want {false 600 10}", s)
	}
}

// Non-admins and anonymous callers are rejected (REQ-API-112).
func TestSettingsRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	user := e.mustUser("settings-user@example.org")

	for _, actor := range []*db.User{nil, user} {
		rec := e.do("GET", "/api/v1/settings", nil, actor)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET as %v: status = %d, want 403", actor, rec.Code)
		}
		rec = e.do("PUT", "/api/v1/settings", map[string]any{"rate_limit_rpm": 300}, actor)
		if rec.Code != http.StatusForbidden {
			t.Errorf("PUT as %v: status = %d, want 403", actor, rec.Code)
		}
	}
}

// A PUT applies the supplied subset idempotently, is readable afterwards,
// and audit-logs old and new values (REQ-API-112, REQ-AUD-027).
func TestSettingsPutAppliesAndAudits(t *testing.T) {
	e := newEnv(t)
	adminUser := e.mustAdmin("settings-write@example.org")

	rec := e.do("PUT", "/api/v1/settings", map[string]any{
		"rate_limit_enabled": true, "rate_limit_rpm": 300, "rate_limit_block_minutes": 5,
	}, adminUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var s SettingsObject
	e.decode(rec, &s)
	if !s.RateLimitEnabled || s.RateLimitRPM != 300 || s.RateLimitBlockMinutes != 5 {
		t.Errorf("response = %+v, want {true 300 5}", s)
	}

	stored, err := e.Store.SystemSettings(context.Background())
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if stored["rate_limit_enabled"] != "true" || stored["rate_limit_rpm"] != "300" ||
		stored["rate_limit_block_minutes"] != "5" {
		t.Errorf("stored = %v, want enabled=true rpm=300 block=5", stored)
	}

	types := e.auditTypes()
	if len(types) != 1 || types[0] != "settings_updated" {
		t.Fatalf("audit types = %v, want [settings_updated]", types)
	}

	// The same PUT again changes nothing and writes no entry (REQ-AUD-004).
	rec = e.do("PUT", "/api/v1/settings", map[string]any{
		"rate_limit_enabled": true, "rate_limit_rpm": 300, "rate_limit_block_minutes": 5,
	}, adminUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent PUT: status = %d, want 200", rec.Code)
	}
	if again := e.auditTypes(); len(again) != 1 {
		t.Errorf("audit types after no-op PUT = %v, want one entry only", again)
	}
}

// Out-of-range values and unknown attributes are 400 and change nothing
// (REQ-API-112).
func TestSettingsPutValidation(t *testing.T) {
	e := newEnv(t)
	adminUser := e.mustAdmin("settings-invalid@example.org")

	for _, body := range []map[string]any{
		{"rate_limit_rpm": 0},
		{"rate_limit_rpm": -1},
		{"rate_limit_rpm": "300"},
		{"rate_limit_block_minutes": 0},
		{"rate_limit_block_minutes": -1},
		{"rate_limit_block_minutes": 1441}, // beyond the 24 h ceiling (REQ-API-113)
		{"rate_limit_block_minutes": "10"}, // strings are not integers here
		{"rate_limit_enabled": "yes"},
		{"unknown_key": true},
	} {
		rec := e.do("PUT", "/api/v1/settings", body, adminUser)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400 (body %s)", body, rec.Code, rec.Body.String())
		}
	}

	stored, err := e.Store.SystemSettings(context.Background())
	if err != nil {
		t.Fatalf("SystemSettings: %v", err)
	}
	if stored["rate_limit_rpm"] != "600" || stored["rate_limit_enabled"] != "false" ||
		stored["rate_limit_block_minutes"] != "10" {
		t.Errorf("rejected PUTs changed the store: %v", stored)
	}
	if types := e.auditTypes(); len(types) != 0 {
		t.Errorf("audit types = %v, want none (no applied change)", types)
	}
}
