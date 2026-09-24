// Package config implements the Go API's configuration: the canonical
// variable inventory, the defaults, and the startup validation matrix of
// System_Configuration_Design.md §2–§4 (REQ-CFG-001…026).
//
// Normative rules implemented here:
//   - environment variables exclusively; optionally loaded from a .env file
//     in the component's working directory (REQ-CFG-001)
//   - process environment overrides .env values (REQ-CFG-002)
//   - read once at process start, immutable at runtime (REQ-CFG-006)
//   - startup failure names every missing/invalid variable (REQ-CFG-004)
//   - secrets never written to logs; the startup dump masks them (REQ-CFG-021/022)
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// OAuth2Provider is one configured provider (System_Configuration_Design.md §3.4).
type OAuth2Provider struct {
	N            int
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	EmailAttr    string
}

// LDAPServer is one configured server, in evaluation order (§3.5).
type LDAPServer struct {
	N            int
	URL          string
	BindDN       string
	BindPassword string
	SearchBase   string
	UIDAttr      string
	EmailAttr    string
	NameAttr     string
}

// Config is the complete, immutable configuration snapshot (REQ-CFG-006).
type Config struct {
	// §3.1 core
	AppEnv       string
	APIAddr      string
	WebPublicURL string
	LogLevelAPI  string
	LogLevelWeb  string

	// §3.2 database
	DBConnection string
	DBDatabase   string
	DBHost       string
	DBPort       string
	DBUsername   string
	DBPassword   string

	// §3.3 service boundary and bootstrap
	InternalServiceToken   string
	AdminBootstrapEmail    string
	AdminBootstrapPassword string

	// §3.4/§3.5 authentication
	OAuth2 []OAuth2Provider
	LDAP   []LDAPServer

	// §3.6 anonymization
	AnonSalt         string
	AnonDateShiftMin int
	AnonDateShiftMax int

	// §3.7 data validation (0 = unset, no application cap)
	MaxValueBytes int

	// §3.8 sessions and CSRF (read here for the startup matrix; owned by the web app)
	SessionDir          string
	SessionCookieName   string
	SessionLifetime     int
	SessionCookieSecure bool

	// §3.9 rate limiting
	RateLimitEnabled bool
	RateLimitRPM     int

	// §3.10 account policy and time
	AuthInactivityLimitDays int
	AppTimezone             string
	appLocation             *time.Location // resolved APP_TIMEZONE, for value offsets
}

// AppLocation returns the resolved timezone location for the default
// collection offset (REQ-CFG-026, REQ-VAL-041).
func (c *Config) AppLocation() *time.Location {
	if c.appLocation != nil {
		return c.appLocation
	}
	return time.UTC
}

// Load parses the environment and .env file, applies the defaults of §3,
// and runs the required-at-startup matrix of §4.1. The returned error
// names every missing or invalid variable (REQ-CFG-004).
func Load() (*Config, error) {
	file := loadDotEnv(".env")

	get := func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return file[key]
	}

	cfg := &Config{}
	var errs []string
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	// §3.1 core
	cfg.AppEnv = getOr(get, "APP_ENV", "development")
	switch cfg.AppEnv {
	case "development", "production":
	default:
		fail("APP_ENV: %q (want development or production)", cfg.AppEnv)
	}
	production := cfg.AppEnv == "production"
	cfg.APIAddr = getOr(get, "API_ADDR", "127.0.0.1:8080")
	cfg.WebPublicURL = strings.TrimRight(get("WEB_PUBLIC_URL"), "/")
	if production && cfg.WebPublicURL == "" {
		fail("WEB_PUBLIC_URL: missing (required in production — redirect URIs must be absolute)")
	}
	defaultLevel := "debug"
	if production {
		defaultLevel = "info"
	}
	cfg.LogLevelAPI = getOr(get, "LOG_LEVEL_API", defaultLevel)
	cfg.LogLevelWeb = getOr(get, "LOG_LEVEL_WEB", defaultLevel)
	for name, level := range map[string]string{"LOG_LEVEL_API": cfg.LogLevelAPI, "LOG_LEVEL_WEB": cfg.LogLevelWeb} {
		switch level {
		case "debug", "info", "warn", "error":
		default:
			fail("%s: %q (want debug, info, warn or error)", name, level)
		}
	}

	// §3.2 database
	defaultEngine := "sqlite"
	if production {
		defaultEngine = "mariadb"
	}
	cfg.DBConnection = getOr(get, "DB_CONNECTION", defaultEngine)
	switch cfg.DBConnection {
	case "sqlite", "mariadb":
	default:
		fail("DB_CONNECTION: %q (want sqlite or mariadb)", cfg.DBConnection)
	}
	cfg.DBDatabase = getOr(get, "DB_DATABASE", "./data/app.sqlite")
	cfg.DBHost = getOr(get, "DB_HOST", "127.0.0.1")
	cfg.DBPort = getOr(get, "DB_PORT", "3306")
	cfg.DBUsername = get("DB_USERNAME")
	cfg.DBPassword = get("DB_PASSWORD")
	if cfg.DBConnection == "mariadb" {
		for _, key := range []string{"DB_HOST", "DB_USERNAME", "DB_PASSWORD"} {
			if get(key) == "" {
				fail("%s: missing (required when DB_CONNECTION=mariadb)", key)
			}
		}
	}

	// §3.3 service boundary and bootstrap
	cfg.InternalServiceToken = getOr(get, "INTERNAL_SERVICE_TOKEN", "dev-internal-token")
	if production && cfg.InternalServiceToken == "" {
		fail("INTERNAL_SERVICE_TOKEN: missing (required in production — no default)")
	}
	if production && cfg.InternalServiceToken == "dev-internal-token" {
		fail("INTERNAL_SERVICE_TOKEN: the development default is accepted only when APP_ENV=development")
	}
	cfg.AdminBootstrapEmail = get("ADMIN_BOOTSTRAP_EMAIL")
	cfg.AdminBootstrapPassword = get("ADMIN_BOOTSTRAP_PASSWORD")

	// §3.4 OAuth2 providers 1–3
	for n := 1; n <= 3; n++ {
		issuer := get(fmt.Sprintf("OAUTH2_%d_ISSUER", n))
		if issuer == "" {
			continue
		}
		p := OAuth2Provider{N: n, Issuer: issuer, EmailAttr: getOr(get, fmt.Sprintf("OAUTH2_%d_EMAIL_ATTR", n), "email")}
		p.ClientID = get(fmt.Sprintf("OAUTH2_%d_CLIENT_ID", n))
		p.ClientSecret = get(fmt.Sprintf("OAUTH2_%d_CLIENT_SECRET", n))
		if p.ClientID == "" {
			fail("OAUTH2_%d_CLIENT_ID: missing (required for a provider with OAUTH2_%d_ISSUER)", n, n)
		}
		if p.ClientSecret == "" {
			fail("OAUTH2_%d_CLIENT_SECRET: missing (required for a provider with OAUTH2_%d_ISSUER)", n, n)
		}
		p.RedirectURI = getOr(get, fmt.Sprintf("OAUTH2_%d_REDIRECT_URI", n), cfg.WebPublicURL+"/auth/callback")
		cfg.OAuth2 = append(cfg.OAuth2, p)
	}

	// §3.5 LDAP servers 1–3
	for n := 1; n <= 3; n++ {
		url := get(fmt.Sprintf("LDAP_SERVER_%d_URL", n))
		if url == "" {
			continue
		}
		s := LDAPServer{
			N:            n,
			URL:          url,
			SearchBase:   get(fmt.Sprintf("LDAP_SERVER_%d_SEARCH_BASE", n)),
			UIDAttr:      getOr(get, fmt.Sprintf("LDAP_SERVER_%d_UID_ATTR", n), "uid"),
			EmailAttr:    getOr(get, fmt.Sprintf("LDAP_SERVER_%d_EMAIL_ATTR", n), "mail"),
			NameAttr:     getOr(get, fmt.Sprintf("LDAP_SERVER_%d_NAME_ATTR", n), "cn"),
			BindDN:       getOr(get, fmt.Sprintf("LDAP_SERVER_%d_BIND_DN", n), ""),
			BindPassword: get(fmt.Sprintf("LDAP_SERVER_%d_BIND_PASSWORD", n)),
		}
		if s.SearchBase == "" {
			fail("LDAP_SERVER_%d_SEARCH_BASE: missing (required when LDAP_SERVER_%d_URL is set)", n, n)
		}
		cfg.LDAP = append(cfg.LDAP, s)
	}

	// §4.1: an authentication path must exist — an IdP, an LDAP server, or
	// the table-based bootstrap (GD-18, REQ-CFG-025).
	if len(cfg.OAuth2) == 0 && len(cfg.LDAP) == 0 {
		if cfg.AdminBootstrapEmail == "" {
			fail("ADMIN_BOOTSTRAP_EMAIL: missing (required when no OAuth2 provider and no LDAP server are configured)")
		}
		if cfg.AdminBootstrapPassword == "" {
			fail("ADMIN_BOOTSTRAP_PASSWORD: missing (required when no OAuth2 provider and no LDAP server are configured)")
		}
	}

	// §3.6 anonymization
	cfg.AnonSalt = getOr(get, "ANON_SALT", "dev-anon-salt")
	if production && cfg.AnonSalt == "" {
		fail("ANON_SALT: missing (required in production — no default)")
	}
	if production && cfg.AnonSalt == "dev-anon-salt" {
		fail("ANON_SALT: the development default is accepted only when APP_ENV=development")
	}
	cfg.AnonDateShiftMin = getInt(get, "ANON_DATE_SHIFT_MIN", 0)
	cfg.AnonDateShiftMax = getInt(get, "ANON_DATE_SHIFT_MAX", 364)
	if cfg.AnonDateShiftMin < 0 {
		fail("ANON_DATE_SHIFT_MIN: %d (want >= 0)", cfg.AnonDateShiftMin)
	}
	if cfg.AnonDateShiftMax < cfg.AnonDateShiftMin {
		fail("ANON_DATE_SHIFT_MAX: %d (want >= ANON_DATE_SHIFT_MIN = %d)", cfg.AnonDateShiftMax, cfg.AnonDateShiftMin)
	}

	// §3.7 data validation
	cfg.MaxValueBytes = getInt(get, "MAX_VALUE_BYTES", 0)
	if cfg.MaxValueBytes < 0 {
		fail("MAX_VALUE_BYTES: %d (want >= 0; 0 = no cap)", cfg.MaxValueBytes)
	}

	// §3.8 sessions and CSRF (web-owned; validated here for the shared matrix)
	cfg.SessionDir = getOr(get, "SESSION_DIR", os.TempDir())
	cfg.SessionCookieName = getOr(get, "SESSION_COOKIE_NAME", "csms_session")
	cfg.SessionLifetime = getInt(get, "SESSION_LIFETIME", 28800)
	cfg.SessionCookieSecure = getBool(get, "SESSION_COOKIE_SECURE", production)
	if cfg.SessionLifetime <= 0 {
		fail("SESSION_LIFETIME: %d (want > 0 seconds)", cfg.SessionLifetime)
	}
	// REQ-CFG-017: SESSION_DIR must be a directory and never the application database file.
	if cfg.DBConnection == "sqlite" && filepath.Clean(cfg.SessionDir) == filepath.Clean(cfg.DBDatabase) {
		fail("SESSION_DIR: %q is the application database file (must be a separate directory)", cfg.SessionDir)
	}
	if fi, err := os.Stat(cfg.SessionDir); err == nil && !fi.IsDir() {
		fail("SESSION_DIR: %q is not a directory", cfg.SessionDir)
	}

	// §3.9 rate limiting
	cfg.RateLimitEnabled = getBool(get, "RATE_LIMIT_ENABLED", false)
	cfg.RateLimitRPM = getInt(get, "RATE_LIMIT_RPM", 600)
	if cfg.RateLimitRPM < 1 {
		fail("RATE_LIMIT_RPM: %d (want >= 1)", cfg.RateLimitRPM)
	}

	// §3.10 account policy and time
	cfg.AuthInactivityLimitDays = getInt(get, "AUTH_INACTIVITY_LIMIT_DAYS", 180)
	if cfg.AuthInactivityLimitDays < 0 {
		fail("AUTH_INACTIVITY_LIMIT_DAYS: %d (want >= 0; 0 = rule off)", cfg.AuthInactivityLimitDays)
	}
	cfg.AppTimezone = getOr(get, "APP_TIMEZONE", "UTC")
	if loc, err := time.LoadLocation(cfg.AppTimezone); err != nil {
		fail("APP_TIMEZONE: %q is not a resolvable IANA timezone (%v)", cfg.AppTimezone, err)
	} else {
		cfg.appLocation = loc
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("configuration invalid:\n  %s", strings.Join(errs, "\n  "))
	}
	return cfg, nil
}

// Production reports whether APP_ENV is production.
func (c *Config) Production() bool { return c.AppEnv == "production" }

// Mask returns a copy of the config with every secret value replaced by
// "***" (REQ-CFG-021), safe to log or render.
func (c *Config) Masked() *Config {
	m := *c
	m.InternalServiceToken = mask
	m.AdminBootstrapPassword = mask
	m.DBPassword = mask
	m.AnonSalt = mask
	// Deep-copy the slices so masking them does not mutate the snapshot.
	if len(c.OAuth2) > 0 {
		m.OAuth2 = make([]OAuth2Provider, len(c.OAuth2))
		copy(m.OAuth2, c.OAuth2)
	}
	if len(c.LDAP) > 0 {
		m.LDAP = make([]LDAPServer, len(c.LDAP))
		copy(m.LDAP, c.LDAP)
	}
	for i := range m.OAuth2 {
		m.OAuth2[i].ClientSecret = mask
	}
	for i := range m.LDAP {
		m.LDAP[i].BindPassword = mask
	}
	return &m
}

// Dump returns the effective configuration as KEY=VALUE lines for the
// startup log (REQ-CFG-021/022). Secret values are masked as "***".
func (c *Config) Dump() []string {
	m := "***"
	none := func(s string) string {
		if s == "" {
			return "(unset)"
		}
		return s
	}
	out := []string{
		"APP_ENV=" + c.AppEnv,
		"API_ADDR=" + c.APIAddr,
		"WEB_PUBLIC_URL=" + none(c.WebPublicURL),
		"LOG_LEVEL_API=" + c.LogLevelAPI,
		"LOG_LEVEL_WEB=" + c.LogLevelWeb,
		"DB_CONNECTION=" + c.DBConnection,
		"DB_DATABASE=" + c.DBDatabase,
	}
	if c.DBConnection == "mariadb" {
		out = append(out,
			"DB_HOST="+c.DBHost,
			"DB_PORT="+c.DBPort,
			"DB_USERNAME="+none(c.DBUsername),
			"DB_PASSWORD="+m,
		)
	}
	out = append(out,
		"INTERNAL_SERVICE_TOKEN="+m,
		"ADMIN_BOOTSTRAP_EMAIL="+none(c.AdminBootstrapEmail),
		"ADMIN_BOOTSTRAP_PASSWORD="+m,
		"ANON_SALT="+m,
		"ANON_DATE_SHIFT_MIN="+itoa(c.AnonDateShiftMin),
		"ANON_DATE_SHIFT_MAX="+itoa(c.AnonDateShiftMax),
		"MAX_VALUE_BYTES="+itoa(c.MaxValueBytes)+" (0 = no cap)",
		"SESSION_DIR="+c.SessionDir,
		"SESSION_COOKIE_NAME="+c.SessionCookieName,
		"SESSION_LIFETIME="+itoa(c.SessionLifetime),
		"SESSION_COOKIE_SECURE="+boolStr(c.SessionCookieSecure),
		"RATE_LIMIT_ENABLED="+boolStr(c.RateLimitEnabled),
		"RATE_LIMIT_RPM="+itoa(c.RateLimitRPM),
		"AUTH_INACTIVITY_LIMIT_DAYS="+itoa(c.AuthInactivityLimitDays)+" (0 = off)",
		"APP_TIMEZONE="+c.AppTimezone,
	)
	for _, p := range c.OAuth2 {
		out = append(out,
			fmt.Sprintf("OAUTH2_%d_ISSUER=%s", p.N, p.Issuer),
			fmt.Sprintf("OAUTH2_%d_CLIENT_ID=%s", p.N, none(p.ClientID)),
			fmt.Sprintf("OAUTH2_%d_CLIENT_SECRET=%s", p.N, m),
			fmt.Sprintf("OAUTH2_%d_EMAIL_ATTR=%s", p.N, p.EmailAttr),
		)
	}
	for _, s := range c.LDAP {
		out = append(out,
			fmt.Sprintf("LDAP_SERVER_%d_URL=%s", s.N, s.URL),
			fmt.Sprintf("LDAP_SERVER_%d_SEARCH_BASE=%s", s.N, none(s.SearchBase)),
			fmt.Sprintf("LDAP_SERVER_%d_BIND_DN=%s", s.N, none(s.BindDN)),
			fmt.Sprintf("LDAP_SERVER_%d_BIND_PASSWORD=%s", s.N, m),
		)
	}
	return out
}

// --- helpers ---

const mask = "***"

// getOr returns the value of key, or def when it is unset/empty.
func getOr(get func(string) string, key, def string) string {
	if v := get(key); v != "" {
		return v
	}
	return def
}

// getInt returns key parsed as an integer, or def when unset/empty/unparseable.
func getInt(get func(string) string, key string, def int) int {
	if v := get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// getBool returns key parsed as a boolean, or def when unset/empty/unparseable.
func getBool(get func(string) string, key string, def bool) bool {
	if v := get(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func itoa(n int) string { return strconv.Itoa(n) }
func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// loadDotEnv reads a KEY=VALUE .env file (REQ-CFG-001). A missing or
// unreadable file yields an empty map — the .env file is optional. Blank
// lines and `#` comments are skipped; an optional `export ` prefix and
// surrounding quotes on the value are stripped; later keys win.
func loadDotEnv(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}
