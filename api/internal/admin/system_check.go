package admin

// Configuration check (API_Endpoints_Design.md §4.24, REQ-API-159): the
// facts about this installation that only the API can see — its own
// configuration, the host it runs on, and the database through its own
// connection — for the Control Panel's Configuration Check section
// (REQ-UI-061). The endpoint reports facts, never secrets: a weak secret is
// named by its variable, its value never leaves the process (REQ-CFG-021).
// Which fact is a problem is decided by the web layer against INSTALL.md.

import (
	"net"
	"net/http"
	"os"
	"strings"

	"csms/api/internal/db"
	"csms/api/internal/passhash"
)

// minSecretLength is the shortest shared secret the check accepts: INSTALL.md
// §6 generates 24 to 64 characters, so anything under 16 was typed by hand.
const minSecretLength = 16

// SystemCheck is the §4.24 response shape.
type SystemCheck struct {
	AppEnv                   string           `json:"app_env"`
	WebPublicURL             string           `json:"web_public_url"`
	APIAddr                  string           `json:"api_addr"`
	APIListenLoopback        bool             `json:"api_listen_loopback"`
	RunningAsRoot            *bool            `json:"running_as_root"`
	WeakSecrets              []string         `json:"weak_secrets"`
	Bootstrap                BootstrapFacts   `json:"bootstrap"`
	SMTPConfigured           bool             `json:"smtp_configured"`
	TrustedProxiesConfigured bool             `json:"trusted_proxies_configured"`
	Require2FA               bool             `json:"require_2fa"`
	AppTimezone              string           `json:"app_timezone"`
	Host                     HostFacts        `json:"host"`
	Database                 db.DatabaseFacts `json:"database"`
}

// BootstrapFacts describes ADMIN_BOOTSTRAP_EMAIL/PASSWORD (INSTALL.md §6):
// PasswordUnchanged is true while the bootstrap account still signs in with
// the password from .env — the first login's password change has not
// happened (INSTALL.md §13.2). Nil when the question does not apply.
type BootstrapFacts struct {
	PasswordConfigured bool  `json:"password_configured"`
	PasswordUnchanged  *bool `json:"password_unchanged"`
}

// HostFacts are read from the operating system; nil where this platform
// cannot answer (hostfacts_linux.go / hostfacts_other.go).
type HostFacts struct {
	ClockSynchronized *bool   `json:"clock_synchronized"`
	SELinux           *string `json:"selinux"`
}

func (h *Handler) registerSystemCheck(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/check", h.getSystemCheck)
}

// getSystemCheck answers the facts (is_admin, REQ-API-159). Read-only and
// not audited, like every other administrative read except record views.
func (h *Handler) getSystemCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	dbFacts, err := h.Store.DatabaseFacts(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	cfg := h.Cfg
	out := SystemCheck{
		AppEnv:                   cfg.AppEnv,
		WebPublicURL:             cfg.WebPublicURL,
		APIAddr:                  cfg.APIAddr,
		APIListenLoopback:        isLoopbackAddr(cfg.APIAddr),
		RunningAsRoot:            runningAsRoot(),
		WeakSecrets:              h.weakSecrets(),
		SMTPConfigured:           cfg.SMTPConfigured(),
		TrustedProxiesConfigured: len(cfg.TrustedProxyCIDRs) > 0,
		Require2FA:               cfg.AuthRequire2FA,
		AppTimezone:              cfg.AppTimezone,
		Host:                     hostFacts(),
		Database:                 dbFacts,
	}

	out.Bootstrap.PasswordConfigured = cfg.AdminBootstrapPassword != ""
	if cfg.AdminBootstrapEmail != "" && cfg.AdminBootstrapPassword != "" {
		u, err := h.Store.GetUserByEmail(r.Context(), cfg.AdminBootstrapEmail)
		if err != nil {
			errInternal(w)
			return
		}
		if u != nil {
			unchanged := u.PasswordHash.Valid && passhash.Verify(u.PasswordHash.String, cfg.AdminBootstrapPassword)
			out.Bootstrap.PasswordUnchanged = &unchanged
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// weakSecrets names the shared secrets that are empty, a development default,
// the INSTALL.md placeholder, or too short to be generated (INSTALL.md §6).
func (h *Handler) weakSecrets() []string {
	cfg := h.Cfg
	secrets := []struct{ name, value, devDefault string }{
		{"INTERNAL_SERVICE_TOKEN", cfg.InternalServiceToken, "dev-internal-token"},
		{"ANON_SALT", cfg.AnonSalt, "dev-anon-salt"},
	}
	if cfg.DBConnection == "mariadb" {
		secrets = append(secrets, struct{ name, value, devDefault string }{"DB_PASSWORD", cfg.DBPassword, ""})
	}
	weak := []string{}
	for _, s := range secrets {
		if s.value == "" || s.value == s.devDefault || len(s.value) < minSecretLength ||
			strings.Contains(strings.ToUpper(s.value), "CHANGE_ME") {
			weak = append(weak, s.name)
		}
	}
	return weak
}

// isLoopbackAddr reports whether a listen address binds loopback only — a
// wildcard or an external address would let the API answer the network
// directly, past nginx (INSTALL.md §1).
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runningAsRoot is nil where the platform has no effective user id.
func runningAsRoot() *bool {
	uid := os.Geteuid()
	if uid < 0 {
		return nil
	}
	root := uid == 0
	return &root
}
