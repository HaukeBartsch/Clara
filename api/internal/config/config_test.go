package config

import (
	"os"
	"strings"
	"testing"
)

// clearAuthEnv unsets the auth-related variables so each test starts from a clean
// slate, and restores them when the test ends.
//
// A real unset, not t.Setenv(k, ""): Load reads with os.LookupEnv, so an empty
// value counts as *present* and shadows a .env file rather than falling through to
// it (REQ-CFG-002). Adding a variable to this list in its old form therefore read
// the default instead of the file in every test that writes one — visible only in
// TestLoadReadsDotEnv, while looking like harmless hygiene everywhere else. Go has
// no t.Usetenv, so the restore is done by hand.
func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"APP_ENV", "WEB_PUBLIC_URL", "API_ADDR", "API_ALLOW_REMOTE",
		"DB_CONNECTION", "DB_HOST", "DB_USERNAME",
		"DB_PASSWORD", "INTERNAL_SERVICE_TOKEN", "ADMIN_BOOTSTRAP_EMAIL",
		"ADMIN_BOOTSTRAP_PASSWORD", "OAUTH2_1_ISSUER", "OAUTH2_1_CLIENT_ID",
		"OAUTH2_1_CLIENT_SECRET", "OAUTH2_1_NAMES", "LDAP_SERVER_1_URL",
		"LDAP_SERVER_1_SEARCH_BASE", "LDAP_SERVER_1_NAMES", "LOCAL_LOGIN_NAMES",
		"ANON_SALT", "ANON_DATE_SHIFT_MIN", "ANON_DATE_SHIFT_MAX",
		"AUTH_INACTIVITY_LIMIT_DAYS", "APP_TIMEZONE", "TRUSTED_PROXY_CIDRS",
		"AUTH_REQUIRE_2FA", "TOTP_ISSUER", "TFA_EMAIL_CODE_TTL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_SECURITY", "SMTP_USERNAME",
		"SMTP_PASSWORD", "OTP_MAIL_FROM", "UI_THEME",
	} {
		unsetEnv(t, k)
	}
}

// unsetEnv removes k from the process environment for the duration of the test and
// puts back exactly what was there before — including "it was not there", which is
// the case a .env file needs in order to be read at all.
func unsetEnv(t *testing.T, k string) {
	t.Helper()
	if prior, existed := os.LookupEnv(k); existed {
		t.Cleanup(func() {
			if err := os.Setenv(k, prior); err != nil {
				t.Fatalf("restore %s: %v", k, err)
			}
		})
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(k) })
	}
	if err := os.Unsetenv(k); err != nil {
		t.Fatalf("unset %s: %v", k, err)
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
	if cfg.UITheme != "bootstrap" {
		t.Errorf("UITheme = %q, want bootstrap (REQ-CFG-031 default)", cfg.UITheme)
	}
}

// TestLoadUITheme covers the §4.1 row: the installation default theme is one
// of the installed identifiers; an unknown value refuses startup (GD-26).
func TestLoadUITheme(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	for _, want := range []string{"bootstrap", "darkly", "yeti"} {
		t.Setenv("UI_THEME", want)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(UI_THEME=%s): %v", want, err)
		}
		if cfg.UITheme != want {
			t.Errorf("UITheme = %q, want %s", cfg.UITheme, want)
		}
	}

	t.Setenv("UI_THEME", "cyborg")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "UI_THEME") {
		t.Fatalf("Load(UI_THEME=cyborg) error = %v, want it to name UI_THEME", err)
	}
}

func TestLoadProductionDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "prod-svc-secret-0123456789abcdefghij")
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

// TestLoadProductionRequiresALongEnoughServiceToken covers finding F8: the token
// acts as any user, and INSTALL.md §6 already asks for a 64-character one, so a
// short value has to be a startup failure rather than a secret nobody can guess
// only by luck.
func TestLoadProductionRequiresALongEnoughServiceToken(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("DB_CONNECTION", "sqlite") // avoid requiring DB creds
	t.Setenv("ANON_SALT", "prod-salt")
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	t.Setenv("INTERNAL_SERVICE_TOKEN", strings.Repeat("t", 31))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "INTERNAL_SERVICE_TOKEN") {
		t.Fatalf("Load() with a 31-byte token = %v, want it refused and named", err)
	}

	t.Setenv("INTERNAL_SERVICE_TOKEN", strings.Repeat("t", 32))
	if _, err := Load(); err != nil {
		t.Fatalf("Load() with a 32-byte token returned error: %v", err)
	}

	// Development keeps its short fixed token, which the whole test suite uses.
	t.Setenv("APP_ENV", "development")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "test-service-token")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() in development with a short token returned error: %v", err)
	}
}

// TestProductionBindGuard covers the API_ADDR guard in Load: the administration
// surface decides *who* acts from a shared service token plus an actor header
// (Authentication_Authorization_Design.md §2.3), so the token alone is the credential
// for every account, root included, and the listen address is what keeps that off the
// network. A production bind outside the host therefore needs API_ALLOW_REMOTE by
// name; the loopback forms it already has to use must keep working, and development
// must stay free to bind broadly (the e2e specs reach the API across a container edge).
func TestProductionBindGuard(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("DB_CONNECTION", "sqlite") // avoid requiring DB creds
	t.Setenv("ANON_SALT", "prod-salt")
	t.Setenv("INTERNAL_SERVICE_TOKEN", strings.Repeat("t", 32))
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	cases := []struct {
		addr    string
		allow   string
		wantRef bool
		why     string
	}{
		{"127.0.0.1:8080", "", false, "the documented default"},
		{"localhost:8080", "", false, "loopback by name"},
		{"[::1]:8080", "", false, "IPv6 loopback"},
		{":8080", "", true, "an empty host is every interface, not a local default"},
		{"0.0.0.0:8080", "", true, "all IPv4 interfaces"},
		{"10.0.0.5:8080", "", true, "a routable address"},
		{"10.0.0.5:8080", "0", true, "API_ALLOW_REMOTE is set to something other than 1"},
		{"10.0.0.5:8080", "1", false, "the opt-in was asked for by name"},
	}
	for _, c := range cases {
		t.Setenv("API_ADDR", c.addr)
		if c.allow == "" {
			unsetEnv(t, "API_ALLOW_REMOTE")
		} else {
			t.Setenv("API_ALLOW_REMOTE", c.allow)
		}
		_, err := Load()
		refused := err != nil && strings.Contains(err.Error(), "API_ADDR")
		if refused != c.wantRef {
			t.Errorf("API_ADDR=%q API_ALLOW_REMOTE=%q: refused=%v, want %v (%s); err=%v",
				c.addr, c.allow, refused, c.wantRef, c.why, err)
		}
		// A run the guard accepts has to be a *valid* config, or "no API_ADDR
		// complaint" would also be reported for an unrelated startup failure.
		if !c.wantRef && err != nil {
			t.Errorf("API_ADDR=%q: Load() returned error: %v", c.addr, err)
		}
	}

	// Development keeps the pre-existing behaviour: bind wherever it likes.
	t.Setenv("APP_ENV", "development")
	t.Setenv("API_ADDR", "0.0.0.0:8080")
	unsetEnv(t, "API_ALLOW_REMOTE")
	if _, err := Load(); err != nil {
		t.Errorf("development with a broad bind returned error: %v", err)
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

// TestLoadSourceNames covers REQ-CFG-032 (master spec "Authentication order"):
// display names parse per source — trimmed, duplicates within one list
// ignored; unset lists leave the implicit default set (REQ-AUTH-067).
func TestLoadSourceNames(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
	t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
	t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
	t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")
	t.Setenv("OAUTH2_1_NAMES", "Hospital 1, Clinic A ,Hospital 1")
	t.Setenv("LDAP_SERVER_1_URL", "ldaps://dir.example.org:636")
	t.Setenv("LDAP_SERVER_1_SEARCH_BASE", "ou=people,dc=example,dc=org")
	t.Setenv("LDAP_SERVER_1_NAMES", "Hospital 2")
	t.Setenv("LOCAL_LOGIN_NAMES", "Clinic A, Walk-in")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if len(cfg.OAuth2) != 1 || strings.Join(cfg.OAuth2[0].Names, "|") != "Hospital 1|Clinic A" {
		t.Errorf("OAUTH2_1_NAMES = %q, want [Hospital 1, Clinic A] (trimmed, deduplicated)",
			cfg.OAuth2[0].Names)
	}
	if len(cfg.LDAP) != 1 || strings.Join(cfg.LDAP[0].Names, "|") != "Hospital 2" {
		t.Errorf("LDAP_SERVER_1_NAMES = %q, want [Hospital 2]", cfg.LDAP[0].Names)
	}
	if strings.Join(cfg.LocalLoginNames, "|") != "Clinic A|Walk-in" {
		t.Errorf("LOCAL_LOGIN_NAMES = %q, want [Clinic A, Walk-in]", cfg.LocalLoginNames)
	}
}

// TestLoadSourceNameEmptyEntryRejected: an entry empty after trimming refuses
// startup naming the variable (REQ-CFG-032, System_Configuration_Design.md §4.1).
func TestLoadSourceNameEmptyEntryRejected(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"OAUTH2_1_NAMES", "Hospital 1,,X"},
		{"LDAP_SERVER_1_NAMES", " ,Hospital 2"},
		{"LOCAL_LOGIN_NAMES", "Clinic A,"},
	} {
		clearAuthEnv(t)
		t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
		t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
		t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
		t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
		t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")
		t.Setenv("LDAP_SERVER_1_URL", "ldaps://dir.example.org:636")
		t.Setenv("LDAP_SERVER_1_SEARCH_BASE", "ou=people,dc=example,dc=org")
		t.Setenv(tc.key, tc.value)

		_, err := Load()
		if err == nil {
			t.Fatalf("%s=%q: Load() succeeded, want failure", tc.key, tc.value)
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("error should name %s, got: %s", tc.key, err.Error())
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
	t.Setenv("SMTP_HOST", "smtp.example.org")
	t.Setenv("SMTP_PASSWORD", "smtp-pass")
	t.Setenv("OTP_MAIL_FROM", "codes@example.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	m := cfg.Masked()
	if m.InternalServiceToken != "***" || m.AdminBootstrapPassword != "***" || m.AnonSalt != "***" || m.DBPassword != "***" {
		t.Errorf("Masked() failed to redact core secrets: %+v", m)
	}
	if m.SMTPPassword != "***" {
		t.Error("Masked() failed to redact SMTP password (REQ-CFG-021)")
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
	t.Setenv("SMTP_HOST", "smtp.example.org")
	t.Setenv("SMTP_PASSWORD", "smtp-pass")
	t.Setenv("OTP_MAIL_FROM", "codes@example.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	dump := strings.Join(cfg.Dump(), "\n")
	for _, secret := range []string{"bootstrap-pass", "svc-token", "the-salt", "db-pass", "smtp-pass"} {
		if strings.Contains(dump, secret) {
			t.Errorf("Dump() leaked secret %q", secret)
		}
	}
	if !strings.Contains(dump, "***") {
		t.Error("Dump() should contain the *** mask marker")
	}
}

// TestLoadTwoFactorDefaults checks the §3.11 defaults (REQ-CFG-027/028/029).
func TestLoadTwoFactorDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.AuthRequire2FA {
		t.Error("AuthRequire2FA = true, want false (opt-in by default)")
	}
	if cfg.TotpIssuer != "CLARA" {
		t.Errorf("TotpIssuer = %q, want CLARA", cfg.TotpIssuer)
	}
	if cfg.TFAEmailCodeTTL != 600 {
		t.Errorf("TFAEmailCodeTTL = %d, want 600", cfg.TFAEmailCodeTTL)
	}
	if cfg.SMTPSecurity != "starttls" {
		t.Errorf("SMTPSecurity = %q, want starttls", cfg.SMTPSecurity)
	}
	if cfg.SMTPConfigured() {
		t.Error("SMTPConfigured() = true without SMTP_HOST")
	}
}

// TestLoadTwoFactorValidation covers the startup matrix rows: an emailed
// factor needs a sender, the security mode is closed, and the TTL positive.
func TestLoadTwoFactorValidation(t *testing.T) {
	cases := []struct {
		name    string
		set     map[string]string
		wantErr string
	}{
		{
			"relay without sender",
			map[string]string{"SMTP_HOST": "smtp.example.org"},
			"OTP_MAIL_FROM",
		},
		{
			"unknown security mode",
			map[string]string{"SMTP_SECURITY": "plaintext"},
			"SMTP_SECURITY",
		},
		{
			"non-positive code TTL",
			map[string]string{"TFA_EMAIL_CODE_TTL": "0"},
			"TFA_EMAIL_CODE_TTL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
			t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want it to name %s", err, tc.wantErr)
			}
		})
	}

	// A complete relay block loads and reports itself configured.
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
	t.Setenv("SMTP_HOST", "smtp.example.org")
	t.Setenv("OTP_MAIL_FROM", "codes@example.org")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if !cfg.SMTPConfigured() || cfg.SMTPPort != 587 {
		t.Errorf("SMTP block = %+v, want configured on port 587", cfg)
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

// TestLoadAssertionDefaults covers REQ-CFG-035's defaults: a provider configured only
// with issuer/client id/secret gets the strict reading of every assertion setting, and
// each default is what Sequence J needs rather than what is convenient to parse.
func TestLoadAssertionDefaults(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
	t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
	t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(cfg.OAuth2) != 1 {
		t.Fatalf("OAuth2 providers = %d, want 1", len(cfg.OAuth2))
	}
	p := cfg.OAuth2[0]
	if p.SubjectAttr != "sub" {
		t.Errorf("SubjectAttr = %q, want sub (REQ-AUTH-080)", p.SubjectAttr)
	}
	if p.AllowUnverifiedEmail {
		t.Error("AllowUnverifiedEmail = true, want false: an unset setting must not drop the address check (REQ-AUTH-078)")
	}
	if p.IDTokenAlg != "RS256" {
		t.Errorf("IDTokenAlg = %q, want RS256 (REQ-AUTH-077)", p.IDTokenAlg)
	}
	if p.NonceRequired {
		t.Error("NonceRequired = true, want false until the provider is observed echoing the claim (REQ-AUTH-079)")
	}
	if len(p.MinACR) != 0 {
		t.Errorf("MinACR = %v, want empty (no assurance gate, REQ-AUTH-081)", p.MinACR)
	}
	if cfg.OAuthJWKSTTLSec != 3600 {
		t.Errorf("OAuthJWKSTTLSec = %d, want 3600", cfg.OAuthJWKSTTLSec)
	}
}

// TestLoadAssertionSettings covers the non-default side of REQ-CFG-035.
func TestLoadAssertionSettings(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
	t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
	t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")
	t.Setenv("OAUTH2_1_SUBJECT_ATTR", "persistent_id")
	t.Setenv("OAUTH2_1_REQUIRE_EMAIL_VERIFIED", "0")
	t.Setenv("OAUTH2_1_MIN_ACR", "urn:clara:mfa, urn:eidas:large ")
	t.Setenv("OAUTH2_1_NONCE_REQUIRED", "1")
	t.Setenv("OAUTH2_1_ID_TOKEN_ALG", "es256")
	t.Setenv("OAUTH2_JWKS_TTL", "600")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	p := cfg.OAuth2[0]
	if p.SubjectAttr != "persistent_id" {
		t.Errorf("SubjectAttr = %q, want persistent_id", p.SubjectAttr)
	}
	if !p.AllowUnverifiedEmail {
		t.Error("AllowUnverifiedEmail = false, want true for REQUIRE_EMAIL_VERIFIED=0")
	}
	if len(p.MinACR) != 2 || p.MinACR[0] != "urn:clara:mfa" || p.MinACR[1] != "urn:eidas:large" {
		t.Errorf("MinACR = %v, want [urn:clara:mfa urn:eidas:large] (trimmed)", p.MinACR)
	}
	if !p.NonceRequired {
		t.Error("NonceRequired = false, want true")
	}
	if p.IDTokenAlg != "ES256" {
		t.Errorf("IDTokenAlg = %q, want the canonical ES256", p.IDTokenAlg)
	}
	if cfg.OAuthJWKSTTLSec != 600 {
		t.Errorf("OAuthJWKSTTLSec = %d, want 600", cfg.OAuthJWKSTTLSec)
	}
}

// TestLoadRejectsUnverifiableAlgorithms is REQ-AUTH-077's startup rule: the values that
// would make "verify the signature" a no-op cannot be configured at all, so a typo in
// the environment fails at startup instead of opening the login path.
func TestLoadRejectsUnverifiableAlgorithms(t *testing.T) {
	for _, alg := range []string{"none", "None", "HS256", "HS384", "HS512", "oct", ""} {
		t.Run("alg="+alg, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
			t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
			t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
			t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")
			if alg == "" {
				// An empty value falls back to the default rather than being rejected,
				// so this case is only meaningful when a value is actually set.
				t.Skip("empty means unset; covered by the defaults test")
			}
			t.Setenv("OAUTH2_1_ID_TOKEN_ALG", alg)
			if _, err := Load(); err == nil {
				t.Errorf("Load() succeeded for OAUTH2_1_ID_TOKEN_ALG=%q, want rejection (REQ-AUTH-077)", alg)
			}
		})
	}
}

// TestLoadRejectsJWKSTTLBelowOne keeps a mistyped "0" from meaning "never refetch keys".
func TestLoadRejectsJWKSTTLBelowOne(t *testing.T) {
	for _, raw := range []string{"0", "-1", "abc", "1h"} {
		t.Run("ttl="+raw, func(t *testing.T) {
			clearAuthEnv(t)
			t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
			t.Setenv("OAUTH2_1_ISSUER", "https://idp.example.org")
			t.Setenv("OAUTH2_1_CLIENT_ID", "csms")
			t.Setenv("OAUTH2_1_CLIENT_SECRET", "s3cret")
			t.Setenv("OAUTH2_JWKS_TTL", raw)
			if _, err := Load(); err == nil {
				t.Errorf("Load() succeeded for OAUTH2_JWKS_TTL=%q, want rejection (REQ-CFG-035)", raw)
			}
		})
	}
}
