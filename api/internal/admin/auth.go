package admin

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
)

// registerAuth mounts the session endpoints of §4.3: login (the only
// endpoint reached without an acting user — REQ-API-041's sole exception)
// and logout. The API is stateless: no session is created or stored (GD-1).
func (h *Handler) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
}

// loginRequest is the §4.3 body: provider only for oauth2/ldap, password
// only for source "local" (GD-18). The password is never logged or audited
// (REQ-AUTH-036).
type loginRequest struct {
	Email    string `json:"email"`
	Source   string `json:"source"`
	Provider string `json:"provider"`
	Password string `json:"password"`
}

// login implements POST /api/v1/auth/login with the processing order of
// Authentication_Authorization_Design.md §2.3 (Sequence C, step 0 for local).
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	switch body.Source {
	case "oauth2", "ldap", "local":
	default:
		errBadRequest(w, "source must be one of oauth2, ldap, local")
		return
	}
	if strings.TrimSpace(body.Email) == "" {
		errBadRequest(w, "email is required")
		return
	}
	ctx := r.Context()

	u, err := h.Store.GetUserByEmail(ctx, body.Email)
	if err != nil {
		errInternal(w)
		return
	}

	// Step 0 (GD-18, REQ-AUTH-050): local hash check. A missing hash and a
	// mismatch are indistinguishable to the caller; bcrypt comparison is
	// constant-time per hash.
	if body.Source == "local" {
		if u == nil {
			h.loginFailure(ctx, &body, "account_not_found")
			APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this email")
			return
		}
		if !u.PasswordHash.Valid ||
			bcrypt.CompareHashAndPassword([]byte(u.PasswordHash.String), []byte(body.Password)) != nil {
			h.loginFailure(ctx, &body, "bad_credentials")
			APIError(w, http.StatusUnauthorized, "bad_password", "invalid email or password")
			return
		}
	} else if u == nil && !h.isBootstrapEmail(body.Email) {
		h.loginFailure(ctx, &body, "account_not_found")
		APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this email")
		return
	}

	// Step 2 (REQ-AUTH-007, GD-4): bootstrap promotion — idempotent ensure
	// that the row exists, is enabled, and has is_admin = 1. Runs before the
	// active check so a promoted account passes it.
	if h.isBootstrapEmail(body.Email) {
		if _, err := h.Store.UpsertBootstrap(ctx, body.Email, "Administrator", ""); err != nil {
			errInternal(w)
			return
		}
		if u, err = h.Store.GetUserByEmail(ctx, body.Email); err != nil || u == nil {
			errInternal(w)
			return
		}
	}

	// Step 1 (Authentication_Authorization_Design.md §4.4): account-active
	// rule; the inactivity case has already written account_auto_disabled
	// inside CheckActive — do not double-write it here (REQ-AUTH-053).
	now := time.Now().UTC()
	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, now)
	if err != nil {
		errInternal(w)
		return
	}
	switch state {
	case authz.AccountExpired:
		h.loginFailure(ctx, &body, "account_expired")
		APIError(w, http.StatusForbidden, "account_expired", "this account has expired")
		return
	case authz.AccountDisabled:
		h.loginFailure(ctx, &body, "account_disabled")
		APIError(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}

	// Steps 3–5: stamp auth_source + last_login_at (REQ-AUTH-005), audit
	// login_success with the source (§3.1), respond with the user object.
	if err := h.Store.TouchLastLogin(ctx, u.ID, body.Source); err != nil {
		errInternal(w)
		return
	}
	u.LastLoginAt = sql.NullString{String: now.Format("2006-01-02 15:04:05"), Valid: true}
	u.AuthSource = body.Source
	details := map[string]any{"source": body.Source, "display_name": u.DisplayName}
	if body.Provider != "" {
		details["provider"] = body.Provider
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginSuccess, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: details,
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, NewUserObject(u, now))
}

// isBootstrapEmail reports whether the email equals the configured
// ADMIN_BOOTSTRAP_EMAIL (REQ-AUTH-007); unset config never matches.
func (h *Handler) isBootstrapEmail(email string) bool {
	return h.Cfg.AdminBootstrapEmail != "" && email == h.Cfg.AdminBootstrapEmail
}

// loginFailure writes the login_failure entry (§3.1): source, email and a
// stable reason — never the password (REQ-AUTH-036). The rejection response
// is written by the caller; an audit write failure does not mask it.
func (h *Handler) loginFailure(ctx context.Context, body *loginRequest, reason string) {
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginFailure, Source: audit.SourceUI,
		Email: body.Email,
		Details: map[string]any{"source": body.Source, "email": body.Email, "reason": reason},
	})
}

// logout implements POST /api/v1/auth/logout (REQ-AUTH-008/015): it records
// the logout event — the PHP layer destroys the session after this call —
// and stores no state itself (GD-1).
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	if err := h.Audit.Insert(r.Context(), audit.Entry{
		EventType: audit.Logout, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: map[string]any{},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
