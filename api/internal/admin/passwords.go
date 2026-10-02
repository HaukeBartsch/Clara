package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// registerPasswords mounts the password lifecycle of GD-22/GD-23: the three
// pre-authentication Sequence H endpoints (API_Endpoints_Design.md §4.3),
// the administrator invitation, and the self-service change (§4.4). The
// first three ride the service-token-only boundary exemption
// (Authentication_Authorization_Design.md §2.8, DEV-API-16); they run with
// no acting user in the context.

func (h *Handler) registerPasswords(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/password-reset/request", h.passwordResetRequest)
	mux.HandleFunc("POST /api/v1/auth/password-reset/complete", h.passwordResetComplete)
	mux.HandleFunc("POST /api/v1/auth/invite/complete", h.inviteComplete)
	mux.HandleFunc("POST /api/v1/users/{id}/invite", h.inviteUser)
	mux.HandleFunc("PUT /api/v1/users/me/password", h.changeMyPassword)
}

// --- password policy (security finding F12) ---

const (
	passwordMinLen   = 12 // characters
	passwordMaxBytes = 72 // bcrypt's input limit — beyond it hashing fails with a 500
)

// passwordPolicyError returns the rejection message when pw violates the
// policy, "" when it satisfies it. Every point where a password is created
// applies it: the setup completions, the self-service change, the
// administrator-set passwords in users.go, and ADMIN_BOOTSTRAP_PASSWORD at
// startup (via ValidatePasswordPolicy). Stored hashes are never re-checked —
// a legacy account keeps working until its next change.
func passwordPolicyError(pw string) string {
	if utf8.RuneCountInString(pw) < passwordMinLen {
		return fmt.Sprintf("password must be at least %d characters", passwordMinLen)
	}
	if len(pw) > passwordMaxBytes {
		return fmt.Sprintf("password must be at most %d bytes", passwordMaxBytes)
	}
	return ""
}

// ValidatePasswordPolicy is the exported form of the policy check, used by
// cmd/server to fail fast on a non-compliant ADMIN_BOOTSTRAP_PASSWORD.
func ValidatePasswordPolicy(pw string) error {
	if msg := passwordPolicyError(pw); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// --- shared token mechanics (Authentication_Authorization_Design.md §2.8) ---

// newSetupToken returns a fresh 256-bit token value and its SHA-256 hash:
// the value goes into the email once, only the hash is stored (REQ-DB-039).
func newSetupToken() (value, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	value = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(value))
	return value, hex.EncodeToString(sum[:]), nil
}

// setupLink renders the set-password URL the email carries; the token rides
// as a query parameter to the PHP route (the logging rule of REQ-AUTH-049
// extended by analogy keeps it out of access logs).
func (h *Handler) setupLink(token string) string {
	return h.Cfg.WebPublicURL + "/set-password?token=" + token
}

func (h *Handler) setupExpiry(now time.Time) string {
	return now.UTC().AddDate(0, 0, h.Cfg.AuthPasswordTokenTTLDays).Format("2006-01-02 15:04:05")
}

// mailConfigured reports whether setup emails can be delivered at all; the
// SendMail test hook counts as a relay.
func (h *Handler) mailConfigured() bool { return h.Mail.Enabled() || h.SendMail != nil }

func (h *Handler) mail(to, subject, body string) error {
	if h.SendMail != nil {
		return h.SendMail(to, subject, body)
	}
	return h.Mail.Send(to, subject, body)
}

// completeSetup redeems a setup token: constant-time hash verification,
// purpose, expiry and single use — every failure answers the same 401
// invalid_setup_token so unknown, expired, consumed and wrong-purpose tokens
// are indistinguishable (REQ-API-120/121). On success it stores the new
// bcrypt hash, consumes the token, invalidates every other outstanding
// token of the account, and audits — all in one transaction.
func (h *Handler) completeSetup(w http.ResponseWriter, r *http.Request, purpose, event string) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.Token == "" || body.Password == "" {
		errBadRequest(w, "token and password are required")
		return
	}
	ctx := r.Context()

	sum := sha256.Sum256([]byte(body.Token))
	stored, err := h.Store.GetPasswordTokenByHash(ctx, hex.EncodeToString(sum[:]))
	if err != nil {
		errInternal(w)
		return
	}
	if !validSetupToken(stored, purpose, hex.EncodeToString(sum[:]), time.Now().UTC()) {
		invalidSetupToken(w)
		return
	}
	// The policy is checked only once the token has proven itself, so a
	// probing request keeps answering the one indistinguishable rejection
	// (REQ-API-120/121) instead of leaking whether the password is short.
	if msg := passwordPolicyError(body.Password); msg != "" {
		errBadRequest(w, msg)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		errInternal(w)
		return
	}
	target, err := h.Store.GetUser(ctx, stored.UserID)
	if err != nil || target == nil {
		errInternal(w)
		return
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), target.ID); err != nil {
		errInternal(w)
		return
	}
	// The guarded UPDATE is the single-use gate: a concurrent second
	// redemption of the same token loses the race and answers invalid.
	ok, err := h.Store.ConsumePasswordTokenTx(ctx, tx, stored.TokenHash, now)
	if err != nil {
		errInternal(w)
		return
	}
	if !ok {
		invalidSetupToken(w)
		return
	}
	if err := h.Store.InvalidatePasswordTokensTx(ctx, tx, target.ID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: event, Source: audit.SourceUI,
		UserID: target.ID, Email: target.Email,
		Details: map[string]any{"email": target.Email}, // never the token (REQ-AUD-029)
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validSetupToken checks purpose, single use and expiry; the hash comparison
// runs in constant time even though the row was found by its hash.
func validSetupToken(t *db.PasswordToken, purpose, hash string, now time.Time) bool {
	if t == nil {
		return false
	}
	matches := subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) == 1
	return matches && t.Purpose == purpose && !t.ConsumedAt.Valid &&
		t.ExpiresAt > now.UTC().Format("2006-01-02 15:04:05")
}

func invalidSetupToken(w http.ResponseWriter) {
	APIError(w, http.StatusUnauthorized, "invalid_setup_token",
		"the setup link is not valid — request a new one")
}

// --- reset request (REQ-API-119, REQ-AUTH-062) ---

// passwordResetRequest answers with the identical 202 body whether or not a
// matching account exists — no enumeration oracle. Internally only an active
// local account receives the emailed link; anything else sends nothing yet
// answers identically. Rate limit: 3 requests per 15 minutes per address,
// on top of the general IP limiter wrapping /api/v1/.
func (h *Handler) passwordResetRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	body.Email = strings.TrimSpace(body.Email)
	if body.Email == "" {
		errBadRequest(w, "email is required")
		return
	}
	ctx := r.Context()
	address := strings.ToLower(body.Email)

	if !h.resetSends.allow(address, time.Now()) {
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many requests for this address — try again later")
		return
	}

	u, err := h.Store.GetUserByEmail(ctx, body.Email)
	if err != nil {
		errInternal(w)
		return
	}
	if resetEligible(u, time.Now().UTC()) && h.mailConfigured() {
		value, hash, err := newSetupToken()
		if err != nil {
			errInternal(w)
			return
		}
		if err := h.Store.IssuePasswordToken(ctx, u.ID, hash, "reset", sql.NullInt64{}, h.setupExpiry(time.Now())); err != nil {
			errInternal(w)
			return
		}
		subject := fmt.Sprintf("Reset your %s password", h.Cfg.TotpIssuer)
		message := fmt.Sprintf(
			"Somebody asked to reset the password for the %s account %s.\n\n"+
				"Open this link to choose a new password:\n\n%s\n\n"+
				"The link works once and expires after %d days. If you did not ask\n"+
				"for a reset, no action is needed — your current password stays valid.\n",
			h.Cfg.TotpIssuer, u.Email, h.setupLink(value), h.Cfg.AuthPasswordTokenTTLDays)
		// A delivery failure never changes the response (REQ-AUTH-062); the
		// audit entry records the request either way.
		_ = h.mail(u.Email, subject, message)
	}

	// The 202 stands either way — a lost audit line must not disclose
	// anything about the account (REQ-AUTH-062).
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.PasswordResetRequested, Source: audit.SourceUI,
		Email:   body.Email,
		Details: map[string]any{"email": body.Email, "ip": dataapi.SourceIP(h.Cfg, r)},
	})
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// resetEligible reports whether the address may receive a reset link: an
// active (enabled, not expired — §4.4) account on the local path. OAuth2 and
// LDAP accounts have no local password to reset.
func resetEligible(u *db.User, now time.Time) bool {
	return u != nil && u.AuthSource == "local" && authz.UserStatus(u, now) == "active"
}

// --- completions (REQ-API-120/121) ---

func (h *Handler) passwordResetComplete(w http.ResponseWriter, r *http.Request) {
	h.completeSetup(w, r, "reset", audit.PasswordResetCompleted)
}

func (h *Handler) inviteComplete(w http.ResponseWriter, r *http.Request) {
	h.completeSetup(w, r, "invite", audit.InviteAccepted)
}

// --- administrator invitation (REQ-API-117, REQ-AUTH-060) ---

// inviteUser sends a set-password invitation to a local account. Without an
// SMTP relay the admin sets the password directly instead — 409 names that
// clearly. A re-invite replaces the outstanding invite token, killing the
// old link. The token value never appears in the response.
func (h *Handler) inviteUser(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	userID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid user id")
		return
	}
	ctx := r.Context()
	target, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		errInternal(w)
		return
	}
	if target == nil {
		errNotFound(w)
		return
	}
	if !h.mailConfigured() {
		APIError(w, http.StatusConflict, "smtp_not_configured",
			"email delivery is not configured — set the password directly instead")
		return
	}
	if target.AuthSource != "local" {
		errConflict(w, "the account authenticates through an identity provider — it has no local password to set")
		return
	}

	value, hash, err := newSetupToken()
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.IssuePasswordToken(ctx, target.ID, hash, "invite",
		sql.NullInt64{Int64: adminUser.ID, Valid: true}, h.setupExpiry(time.Now())); err != nil {
		errInternal(w)
		return
	}
	subject := fmt.Sprintf("Set up your %s account", h.Cfg.TotpIssuer)
	message := fmt.Sprintf(
		"An administrator invited you to the %s system as %s.\n\n"+
			"Open this link to choose your password:\n\n%s\n\n"+
			"The link works once and expires after %d days.\n",
		h.Cfg.TotpIssuer, target.Email, h.setupLink(value), h.Cfg.AuthPasswordTokenTTLDays)
	if err := h.mail(target.Email, subject, message); err != nil {
		APIError(w, http.StatusBadGateway, "smtp_send_failed",
			"the invitation could not be sent — try again or set the password directly")
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.UserInvited, Source: audit.SourceUI,
		UserID: adminUser.ID, Email: adminUser.Email,
		Details: map[string]any{"target_email": target.Email}, // never the token (REQ-AUD-029)
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- self-service change (REQ-API-118, REQ-AUTH-061) ---

// changeMyPassword proves the current credential before accepting a new
// one: a wrong current password is 401 bad_password, audit-logged and
// counted toward the Sequence E lockout. Accounts without a local credential
// answer 409 no_local_credential. Sessions are not revoked (DEV-AUTH-13).
func (h *Handler) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.CurrentPassword == "" || body.NewPassword == "" {
		errBadRequest(w, "current_password and new_password are required")
		return
	}
	ctx := r.Context()

	fakeBody := &loginRequest{Email: u.Email, Source: "local"}
	if h.lockouts.locked(u.Email, time.Now()) {
		h.loginFailure(ctx, fakeBody, "rate_limited")
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed attempts — try again later")
		return
	}

	current, err := h.Store.GetUser(ctx, u.ID) // re-read: the boundary actor may predate a change
	if err != nil {
		errInternal(w)
		return
	}
	if current == nil || !current.PasswordHash.Valid {
		APIError(w, http.StatusConflict, "no_local_credential",
			"this account has no local password — sign in through its identity provider")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(current.PasswordHash.String), []byte(body.CurrentPassword)) != nil {
		h.loginFailure(ctx, fakeBody, "bad_password") // audited + counted toward the lockout
		APIError(w, http.StatusUnauthorized, "bad_password", "the current password is not correct")
		return
	}
	if msg := passwordPolicyError(body.NewPassword); msg != "" {
		errBadRequest(w, msg) // the proof stood; only the new password is rejected
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.SetUserPasswordHash(ctx, u.ID, sql.NullString{String: string(hash), Valid: true}); err != nil {
		errInternal(w)
		return
	}
	h.lockouts.success(u.Email)
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.PasswordChanged, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: map[string]any{}, // never the password (REQ-AUTH-036)
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- per-address reset limiter (Sequence H request cap) ---

// addressLimiter caps reset requests per email address: 3 within a rolling
// 15-minute window (REQ-API-119). In-memory like the Sequence E store — the
// general IP limiter covers restarts of this host's share.
type addressLimiter struct {
	mu sync.Mutex
	at map[string][]time.Time
}

const (
	resetsPerWindow = 3
	resetWindow     = 15 * time.Minute
)

func newAddressLimiter() *addressLimiter { return &addressLimiter{at: map[string][]time.Time{}} }

// allow records a request for the address and reports whether it fits.
func (l *addressLimiter) allow(address string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.at[address][:0]
	for _, t := range l.at[address] {
		if now.Sub(t) < resetWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= resetsPerWindow {
		l.at[address] = kept
		return false
	}
	l.at[address] = append(kept, now)
	return true
}
