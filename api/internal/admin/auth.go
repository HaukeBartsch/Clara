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

// registerAuth mounts the session endpoints of §4.3: login, the
// side-effect-free verify step of the named-source credential race, and
// logout. These run without an acting user — together with the three
// Sequence H pre-authentication endpoints in passwords.go they form the
// REQ-API-041 exception set (the exemption list lives in httpapi;
// DEV-API-16/17). The API is stateless: no session is created or stored (GD-1).
func (h *Handler) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/verify-password", h.verifyPassword)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
}

// loginRequest is the §4.3 body: provider only for oauth2/ldap, password
// only for source "local" (GD-18). mfa_code is supplied on the second call
// of a two-factor challenge (REQ-API-114); codes are never logged or audited
// (REQ-AUTH-036, REQ-AUTH-059). source_name is the authentication-source
// name the user selected on the login page — recorded in the audit details
// and nothing else (REQ-AUTH-067). attempts carries the per-source outcomes
// of a failed named-source credential race so the single finalizing call
// yields exactly one login_failure with the losing attempts' detail
// (Audit_Logging_Design.md §3.1, DEV-AUD-5); it never contains credentials.
// first_factor replaces password on the second call of a two-factor challenge:
// the signed proof verify-password issued for an account a second factor still
// guards, so the challenge completes without the password being typed twice or
// stored anywhere (REQ-API-131, firstfactor.go).
type loginRequest struct {
	Email       string            `json:"email"`
	Source      string            `json:"source"`
	Provider    string            `json:"provider"`
	Password    string            `json:"password"`
	MFACode     string            `json:"mfa_code"`
	SourceName  string            `json:"source_name"`
	Attempts    map[string]string `json:"attempts"`
	FirstFactor string            `json:"first_factor"`
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

	// Sequence E (REQ-AUTH-035): a locked address is rejected before any
	// credential probing — no IdP/LDAP contact, no hash comparison.
	if h.lockouts.locked(body.Email, time.Now()) {
		h.loginFailure(ctx, &body, "rate_limited")
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed attempts for this address — try again later")
		return
	}

	u, err := h.Store.GetUserByEmail(ctx, body.Email)
	if err != nil {
		errInternal(w)
		return
	}

	// Step 0 (GD-18, REQ-AUTH-050): local hash check. A missing hash and a
	// mismatch are indistinguishable to the caller; bcrypt comparison is
	// constant-time per hash. A first_factor handle from verify-password stands
	// in for the password only where a second factor still guards the account,
	// so it can never complete a login by itself (REQ-API-131).
	if body.Source == "local" {
		if u == nil {
			h.loginFailure(ctx, &body, "account_not_found")
			APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this email")
			return
		}
		switch {
		case body.FirstFactor != "":
			guarded, err := h.secondFactorGuards(ctx, u)
			if err != nil {
				errInternal(w)
				return
			}
			if !guarded || !h.acceptFirstFactor(body.FirstFactor, u, time.Now()) {
				// Unknown format, wrong signature, wrong account, expired, or
				// presented where no second factor guards the account: one
				// answer for all of them — sign in again.
				h.loginFailure(ctx, &body, "first_factor_expired")
				APIError(w, http.StatusUnauthorized, "first_factor_expired",
					"the first-factor proof is no longer valid — sign in again")
				return
			}
		case !u.PasswordHash.Valid ||
			bcrypt.CompareHashAndPassword([]byte(u.PasswordHash.String), []byte(body.Password)) != nil:
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

	// Step 1.5 (Sequence G, GD-21): the second factor guards local and LDAP
	// logins; the OAuth2 path is exempt because the identity provider owns
	// its own second factor.
	mfaFactor := ""
	if body.Source != "oauth2" {
		var proceed bool
		_, mfaFactor, proceed = h.secondFactor(w, r, &body, u)
		if !proceed {
			return
		}
	}

	// Steps 3–5: stamp auth_source + last_login_at (REQ-AUTH-005), audit
	// login_success with the source (§3.1), respond with the user object.
	h.lockouts.success(body.Email) // a success clears the counter (§2.5)
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
	if body.SourceName != "" {
		details["source_name"] = body.SourceName // REQ-AUTH-067
	}
	if mfaFactor != "" {
		details["mfa"] = mfaFactor // totp | email | recovery (Audit_Logging_Design.md §3.1)
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginSuccess, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: details,
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.userObject(ctx, u, now)) // tfa_method per REQ-API-116
}

// isBootstrapEmail reports whether the email equals the configured
// ADMIN_BOOTSTRAP_EMAIL (REQ-AUTH-007); unset config never matches.
func (h *Handler) isBootstrapEmail(email string) bool {
	return h.Cfg.AdminBootstrapEmail != "" && email == h.Cfg.AdminBootstrapEmail
}

// verifyPassword implements POST /api/v1/auth/verify-password (REQ-API-123,
// DEV-API-17): the side-effect-free verify step of the named-source
// credential race (Authentication_Authorization_Design.md §2.9). It runs
// Sequence C steps 0–1 only — the bcrypt hash check and the account-active
// rule — and answers ok / bad_password / account_disabled / account_expired.
// No last_login_at or auth_source write, no audit event, no user object:
// that is what makes it safe to fire alongside the LDAP binds of a race,
// with login remaining the single place side effects happen (REQ-AUTH-065).
// An unknown account and a wrong password are not distinguished (§2.6).
// The brute-force check (Sequence E) runs in PHP before dispatch; the API's
// shared IP limiter already covers this path. The password is never logged
// (REQ-AUTH-036). For an account a second factor still guards the ok answer also
// carries a first-factor handle — the proof the challenge's second login call
// presents in place of the password, so no layer has to hold one (REQ-API-131).
func (h *Handler) verifyPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
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
	if u == nil || !u.PasswordHash.Valid ||
		bcrypt.CompareHashAndPassword([]byte(u.PasswordHash.String), []byte(body.Password)) != nil {
		APIError(w, http.StatusUnauthorized, "bad_password", "invalid email or password")
		return
	}

	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, time.Now())
	if err != nil {
		errInternal(w)
		return
	}
	switch state {
	case authz.AccountExpired:
		APIError(w, http.StatusForbidden, "account_expired", "this account has expired")
		return
	case authz.AccountDisabled:
		APIError(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}

	resp := map[string]any{"status": "ok"}

	// An account a second factor still guards gets back the proof that lets the
	// challenge's second call carry no password (REQ-API-131). None is issued
	// otherwise: there is no challenge to complete, and login would refuse it —
	// an unguarded account authenticates with its password alone.
	guarded, err := h.secondFactorGuards(ctx, u)
	if err != nil {
		errInternal(w)
		return
	}
	if guarded {
		if handle := h.issueFirstFactor(u, time.Now()); handle != "" {
			resp["first_factor"] = handle
			resp["expires_in"] = int(firstFactorTTL.Seconds())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// loginFailure writes the login_failure entry (§3.1): source, email and a
// stable reason — never the password (REQ-AUTH-036). The rejection response
// is written by the caller; an audit write failure does not mask it. For a
// failed named-source credential race the finalizing call carries the
// per-source outcomes in attempts: they ride along in details, and a plain
// credential rejection settles as bad_credentials — or provider_unavailable
// when no source could be reached (Audit_Logging_Design.md §3.1, DEV-AUD-5).
// Account-state rejections keep their specific reason (REQ-AUTH-065).
func (h *Handler) loginFailure(ctx context.Context, body *loginRequest, reason string) {
	// The lockout rejection must not extend itself, and neither does a stale
	// first-factor handle: taking a minute over a code is not a credential
	// attempt, and it must not spend the address's lockout budget (REQ-AUTH-035).
	if reason != "rate_limited" && reason != "first_factor_expired" {
		h.lockouts.failure(body.Email, time.Now())
	}
	if len(body.Attempts) > 0 {
		switch reason {
		case "bad_password", "account_not_found":
			allUnreachable := true
			for _, outcome := range body.Attempts {
				if outcome != "unreachable" {
					allUnreachable = false
					break
				}
			}
			reason = "bad_credentials"
			if allUnreachable {
				reason = "provider_unavailable"
			}
		}
	}
	details := map[string]any{"source": body.Source, "email": body.Email, "reason": reason}
	if body.SourceName != "" {
		details["source_name"] = body.SourceName // REQ-AUTH-067
	}
	if len(body.Attempts) > 0 {
		details["attempts"] = body.Attempts
	}
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginFailure, Source: audit.SourceUI,
		Email:   body.Email,
		Details: details,
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
