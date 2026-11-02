package config

import (
	"fmt"
	"strings"
	"testing"
)

// clearSSOEnv unsets every SSO_CLIENT_n_* variable, the same real unset clearAuthEnv
// does, and sets the table-based bootstrap a development Load needs.
func clearSSOEnv(t *testing.T) {
	t.Helper()
	clearAuthEnv(t)
	for n := 1; n <= 3; n++ {
		for _, k := range []string{"ID", "NAME", "SECRET", "REDIRECT_URI", "LOGOUT_URI"} {
			unsetEnv(t, fmt.Sprintf("SSO_CLIENT_%d_%s", n, k))
		}
	}
	t.Setenv("ADMIN_BOOTSTRAP_EMAIL", "admin@example.org")
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "secret")
}

// TestLoadSSOClient covers REQ-CFG-038: a registered client parses with its
// name defaulting to its id, is found by id, and is masked like every secret.
func TestLoadSSOClient(t *testing.T) {
	clearSSOEnv(t)
	t.Setenv("SSO_CLIENT_1_ID", "fiona")
	t.Setenv("SSO_CLIENT_1_SECRET", "client-secret-value")
	t.Setenv("SSO_CLIENT_1_REDIRECT_URI", "http://localhost:8080/applications/User/login.php")
	t.Setenv("SSO_CLIENT_1_LOGOUT_URI", "http://localhost:8080/index.php")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if len(cfg.SSOClients) != 1 {
		t.Fatalf("SSOClients = %+v, want one client", cfg.SSOClients)
	}
	c := cfg.SSOClientByID("fiona")
	if c == nil || c.Name != "fiona" || c.Secret != "client-secret-value" ||
		c.RedirectURI != "http://localhost:8080/applications/User/login.php" ||
		c.LogoutURI != "http://localhost:8080/index.php" {
		t.Errorf("client = %+v", c)
	}
	if cfg.SSOClientByID("other") != nil || cfg.SSOClientByID("") != nil {
		t.Error("SSOClientByID found a client that is not registered")
	}
	if got := cfg.Masked().SSOClients[0].Secret; got != "***" {
		t.Errorf("masked secret = %q, want ***", got)
	}
	if cfg.SSOClients[0].Secret != "client-secret-value" {
		t.Error("Masked() changed the live configuration")
	}
	dump := strings.Join(cfg.Dump(), "\n")
	if strings.Contains(dump, "client-secret-value") || !strings.Contains(dump, "SSO_CLIENT_1_SECRET=***") {
		t.Errorf("dump leaks or omits the client secret:\n%s", dump)
	}
}

// TestLoadSSOClientValidation: a client without its secret or with an address that
// is not an absolute URL is refused, as are duplicate ids and an id CLARA could not
// write into an audit detail as it is.
func TestLoadSSOClientValidation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing secret", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_REDIRECT_URI": "https://fiona.example.org/cb",
		}, "SSO_CLIENT_1_SECRET"},
		{"missing redirect", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_SECRET": "s",
		}, "SSO_CLIENT_1_REDIRECT_URI"},
		{"relative redirect", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_SECRET": "s", "SSO_CLIENT_1_REDIRECT_URI": "/cb",
		}, "SSO_CLIENT_1_REDIRECT_URI"},
		{"redirect with fragment", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_SECRET": "s",
			"SSO_CLIENT_1_REDIRECT_URI": "https://fiona.example.org/cb#x",
		}, "SSO_CLIENT_1_REDIRECT_URI"},
		{"bad logout", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_SECRET": "s",
			"SSO_CLIENT_1_REDIRECT_URI": "https://fiona.example.org/cb", "SSO_CLIENT_1_LOGOUT_URI": "javascript:alert(1)",
		}, "SSO_CLIENT_1_LOGOUT_URI"},
		{"bad id", map[string]string{
			"SSO_CLIENT_1_ID": "Fiona Test", "SSO_CLIENT_1_SECRET": "s",
			"SSO_CLIENT_1_REDIRECT_URI": "https://fiona.example.org/cb",
		}, "SSO_CLIENT_1_ID"},
		{"duplicate id", map[string]string{
			"SSO_CLIENT_1_ID": "fiona", "SSO_CLIENT_1_SECRET": "s", "SSO_CLIENT_1_REDIRECT_URI": "https://a.example.org/cb",
			"SSO_CLIENT_2_ID": "fiona", "SSO_CLIENT_2_SECRET": "t", "SSO_CLIENT_2_REDIRECT_URI": "https://b.example.org/cb",
		}, "SSO_CLIENT_2_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSSOEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load() error = %v, want one naming %s", err, tc.want)
			}
		})
	}
}

// TestLoadSSOClientProduction: production refuses plain http for the addresses a
// code travels to and a secret shorter than the service-token minimum.
func TestLoadSSOClientProduction(t *testing.T) {
	clearSSOEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("WEB_PUBLIC_URL", "https://csms.example.org")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "prod-svc-secret-0123456789abcdefghij")
	t.Setenv("ANON_SALT", "prod-salt")
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_USERNAME", "csms")
	t.Setenv("DB_PASSWORD", "db-pass")
	t.Setenv("SSO_CLIENT_1_ID", "fiona")
	t.Setenv("SSO_CLIENT_1_SECRET", "short")
	t.Setenv("SSO_CLIENT_1_REDIRECT_URI", "http://fiona.example.org/cb")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want failure")
	}
	for _, want := range []string{"SSO_CLIENT_1_SECRET", "SSO_CLIENT_1_REDIRECT_URI"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s, got: %v", want, err)
		}
	}

	t.Setenv("SSO_CLIENT_1_SECRET", strings.Repeat("a", 64))
	t.Setenv("SSO_CLIENT_1_REDIRECT_URI", "https://fiona.example.org/cb")
	if _, err := Load(); err != nil {
		t.Errorf("Load() with an https address and a long secret: %v", err)
	}
}
