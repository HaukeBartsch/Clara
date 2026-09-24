package config

import (
	"strings"
	"testing"
)

// clearAuthEnv unsets the auth-related variables so each test starts from a
// clean slate (t.Setenv restores them afterwards).
func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"APP_ENV", "WEB_PUBLIC_URL", "DB_CONNECTION", "DB_HOST", "DB_USERNAME",
		"DB_PASSWORD", "INTERNAL_SERVICE_TOKEN", "ADMIN_BOOTSTRAP_EMAIL",
		"ADMIN_BOOTSTRAP_PASSWORD", "OAUTH2_1_ISSUER", "OAUTH2_1_CLIENT_ID",
		"OAUTH2_1_CLIENT_SECRET", "LDAP_SERVER_1_URL", "LDAP_SERVER_1_SEARCH_BASE",
		"ANON_SALT", "ANON_DATE_SHIFT_MIN", "ANON_DATE_SHIFT_MAX",
		"AUTH_INACTIVITY_LIMIT_DAYS", "APP_TIMEZONE", "RATE_LIMIT_RPM",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, want development", cfg.AppEnv)
	}
	if cfg.DBConnection != "sqlite" {
		t.Errorf("DBConnection = %q, want sqlite (dev default)", cfg.DBConnection)
	}
	if cfg.AnonDateShiftMin != 0 || cfg.AnonDateShiftMax != 364 {
		t.Errorf("date shift = %d..%d, want 0..364", cfg.AnonDateShiftMin, cfg.AnonDateShiftMax)
	}
	if cfg.AuthInactivityLimitDays != 180 {
		t.Errorf("AuthInactivityLimitDays = %d, want 180", cfg.AuthInactivityLimitDays)
	}
	if cfg.AppLocation() == nil {
		t.Error("AppLocation() = nil, want non-nil")
	}
	if cfg.SessionCookieSecure {
		t.Error("SessionCookieSecure = true, want false (dev default)")
	}
}

func TestLoadProductionDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "prod-svc")
	t.Setenv("ANON_SALT", "prod-salt")
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_USERNAME", "csms")
	t.Setenv("DB_PASSWORD", "db-pass")
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.DBConnection != "mariadb" {
		t.Errorf("DBConnection = %q, want mariadb (prod default)", cfg.DBConnection)
	}
	if !cfg.SessionCookieSecure {
		t.Error("SessionCookieSecure = false, want true (prod default)")
	}
	if cfg.LogLevelAPI != "info" {
		t.Errorf("LogLevelAPI = %q, want info (prod default)", cfg.LogLevelAPI)
	}
}

func TestLoadProductionRejectsDevDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("DB_CONNECTION", "sqlite") // avoid requiring DB creds
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
	// INTERNAL_SERVICE_TOKEN and ANON_SALT are left at their dev defaults.

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want failure for dev-default secrets in production")
	}
	msg := err.Error()
	if !strings.Contains(msg, "INTERNAL_SERVICE_TOKEN") || !strings.Contains(msg, "ANON_SALT") {
		t.Errorf("error should name both dev-default secrets, got: %s", msg)
	}
}

func TestLoadNamesEveryInvalidVariable(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DB_CONNECTION", "sqlite")
	// No WEB_PUBLIC_URL, no auth path at all.

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want failure")
	}
	msg := err.Error()
	for _, want := range []string{"WEB_PUBLIC_URL", "ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_PASSWORD", "INTERNAL_SERVICE_TOKEN", "ANON_SALT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should name %q, got: %s", want, msg)
		}
	}
}

func TestLoadDateShiftRange(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
	t.Setenv("ANON_DATE_SHIFT_MIN", "10")
	t.Setenv("ANON_DATE_SHIFT_MAX", "5") // < MIN → invalid

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want failure for MAX < MIN")
	}
	if !strings.Contains(err.Error(), "ANON_DATE_SHIFT_MAX") {
		t.Errorf("error should name ANON_DATE_SHIFT_MAX, got: %s", err.Error())
	}
}

func TestMaskedRedactsSecrets(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "bootstrap-pass")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "svc-token")
	t.Setenv("ANON_SALT", "the-salt")
	t.Setenv("DB_PASSWORD", "db-pass")
	t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
	t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
	t.Setenv("OAUTH2_1_CLIENT_SECRET", "client-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	m := cfg.Masked()
	if m.InternalServiceToken != "***" || m.AdminBootstrapPassword != "***" || m.AnonSalt != "***" || m.DBPassword != "***" {
		t.Errorf("Masked() failed to redact core secrets: %+v", m)
	}
	if len(cfg.OAuth2) != 1 || cfg.OAuth2[0].ClientSecret != "client-secret" {
		t.Fatalf("original config should keep the real client secret; got %+v", cfg.OAuth2)
	}
	if m.OAuth2[0].ClientSecret != "***" {
		t.Errorf("Masked() failed to redact OAuth2 client secret")
	}
	// The original must be untouched (immutability of the snapshot).
	if cfg.InternalServiceToken != "svc-token" {
		t.Errorf("original config mutated by Masked(): %q", cfg.InternalServiceToken)
	}
}

func TestDumpDoesNotLeakSecrets(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "bootstrap-pass")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "svc-token")
	t.Setenv("ANON_SALT", "the-salt")
	t.Setenv("DB_PASSWORD", "db-pass")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	dump := strings.Join(cfg.Dump(), "\n")
	for _, secret := range []string{"bootstrap-pass", "svc-token", "the-salt", "db-pass"} {
		if strings.Contains(dump, secret) {
			t.Errorf("Dump() leaked secret %q", secret)
		}
	}
	if !strings.Contains(dump, "***") {
		t.Error("Dump() should contain the *** mask marker")
	}
}

func TestLoadDotEnvParsing(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	content := `
# a comment
APP_ENV=production
export WEB_PUBLIC_URL="https://csms.example.org"
OAUTH2_1_ISSUER='https://idp.example.org'
PLAIN=with=equals
`
	if err := writeFile(path, content); err != nil {
		t.Fatal(err)
	}
	m := loadDotEnv(path)
	if m["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q, want production", m["APP_ENV"])
	}
	if m["WEB_PUBLIC_URL"] != "https://csms.example.org" {
		t.Errorf("WEB_PUBLIC_URL = %q, want quoted value stripped", m["WEB_PUBLIC_URL"])
	}
	if m["OAUTH2_1_ISSUER"] != "https://idp.example.org" {
		t.Errorf("OAUTH2_1_ISSUER = %q, want single-quoted value stripped", m["OAUTH2_1_ISSUER"])
	}
	if m["PLAIN"] != "with=equals" {
		t.Errorf("PLAIN = %q, want split on first '=' only", m["PLAIN"])
	}
}

func TestLoadProcessEnvOverridesDotEnv(t *testing.T) {
	clearAuthEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := writeFile(".env", "API_ADDR=1.1.1.1:1111\n"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_ADDR", "2.2.2.2:2222") // process env must win over .env
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.APIAddr != "2.2.2.2:2222" {
		t.Errorf("APIAddr = %q, want 2.2.2.2:2222 (process env overrides .env)", cfg.APIAddr)
	}
}

func TestLoadReadsDotEnv(t *testing.T) {
	clearAuthEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := writeFile(".env", "API_ADDR=9.9.9.9:9999\n"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
	// API_ADDR is supplied solely by .env.

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.APIAddr != "9.9.9.9:9999" {
		t.Errorf("APIAddr = %q, want 9.9.9.9:9999 (read from .env)", cfg.APIAddr)
	}
}
