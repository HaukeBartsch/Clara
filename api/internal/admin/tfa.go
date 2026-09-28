package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/totp"
)

// registerTfa mounts the two-factor endpoints of API_Endpoints_Design.md §4.3:
// self-service enrollment and disable under /users/me/tfa, plus the admin
// reset (REQ-API-115). The login-time challenge itself lives in auth.go —
// there is no separate challenge endpoint; all state sits in user_two_factor
// so the flow stays stateless (DEV-API-15, GD-1).
func (h *Handler) registerTfa(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/users/me/tfa", h.getMyTFA)
	mux.HandleFunc("POST /api/v1/users/me/tfa/totp/enroll", h.enrollTOTP)
	mux.HandleFunc("POST /api/v1/users/me/tfa/totp/confirm", h.confirmTOTP)
	mux.HandleFunc("POST /api/v1/users/me/tfa/email/start", h.startEmailFactor)
	mux.HandleFunc("POST /api/v1/users/me/tfa/email/confirm", h.confirmEmailFactor)
	mux.HandleFunc("POST /api/v1/users/me/tfa/disable", h.disableTFA)
	mux.HandleFunc("POST /api/v1/users/{id}/tfa/reset", h.resetUserTFA)
}

// --- self-service status and enrollment ---

// getMyTFA reports the acting user's second factor: method, enrollment time
// and how many recovery codes remain (REQ-API-115). No secret is ever
// returned here — only totp/enroll shows one, exactly once.
func (h *Handler) getMyTFA(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	tfa, err := h.Store.GetTFA(r.Context(), u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	out := map[string]any{"method": "off", "enrolled_at": nil, "recovery_codes_remaining": 0}
	if tfa != nil && tfa.Method != "off" {
		out["method"] = tfa.Method
		out["enrolled_at"] = NullStrPtr(tfa.EnrolledAt)
		remaining := 0
		for _, c := range parseRecoveryCodes(tfa.RecoveryCodes) {
			if !c.Used {
				remaining++
			}
		}
		out["recovery_codes_remaining"] = remaining
	}
	writeJSON(w, http.StatusOK, out)
}

// enrollTOTP starts a TOTP enrollment: a fresh shared secret and its
// otpauth:// URI are returned once for the QR code (REQ-AUTH-054). The method
// stays "off" until confirm proves possession.
func (h *Handler) enrollTOTP(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	tfa, err := h.Store.GetTFA(r.Context(), u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if tfa != nil && tfa.Method != "off" {
		errConflict(w, "two-factor is already enabled — disable it first")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.SetTFAPendingSecret(r.Context(), u.ID, secret); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_uri": totp.OTPAuthURL(h.Cfg.TotpIssuer, u.Email, secret),
	})
}

// confirmTOTP activates the pending TOTP enrollment after one correct code
// and issues the one-time recovery codes (REQ-AUTH-054/059).
func (h *Handler) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ctx := r.Context()
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if tfa == nil || !tfa.TotpSecret.Valid || tfa.Method != "off" {
		errConflict(w, "no pending TOTP enrollment")
		return
	}
	step, ok := totp.Verify(tfa.TotpSecret.String, body.Code, time.Now())
	if !ok {
		APIError(w, http.StatusUnauthorized, "bad_mfa_code", "the code is not valid right now")
		return
	}
	recovery, plaintext, err := newRecoveryCodes()
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.ActivateTFA(ctx, u.ID, "totp", recovery); err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.SetTFALastStep(ctx, u.ID, step); err != nil {
		errInternal(w)
		return
	}
	h.auditTFA(ctx, audit.TFAEnrolled, u, map[string]any{"method": "totp"})
	writeJSON(w, http.StatusOK, map[string]any{"method": "totp", "recovery_codes": plaintext})
}

// startEmailFactor sends the confirmation code for an email second factor.
// Without a configured SMTP relay this fails with a clear 409 instead of a
// silently undeliverable enrollment (REQ-AUTH-057).
func (h *Handler) startEmailFactor(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	if !h.Mail.Enabled() {
		APIError(w, http.StatusConflict, "smtp_not_configured",
			"email delivery is not configured on this installation")
		return
	}
	ctx := r.Context()
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if tfa != nil && tfa.Method != "off" {
		errConflict(w, "two-factor is already enabled — disable it first")
		return
	}
	if !h.tfaSends.allow(u.ID, time.Now()) {
		h.tooManySends(w)
		return
	}
	if err := h.sendLoginCode(ctx, u); err != nil {
		APIError(w, http.StatusBadGateway, "smtp_send_failed",
			"the code could not be sent — try again or contact an administrator")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// confirmEmailFactor activates the email second factor once the delivered
// code is returned correctly; the code is single-use (REQ-AUTH-057).
func (h *Handler) confirmEmailFactor(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ctx := r.Context()
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if tfa == nil || !tfa.EmailCodeHash.Valid || tfa.Method != "off" {
		errConflict(w, "no pending email enrollment — start one first")
		return
	}
	ok, err = h.Store.ConsumeTFAEmailCode(ctx, u.ID, totp.HashCode(body.Code))
	if err != nil {
		errInternal(w)
		return
	}
	if !ok {
		APIError(w, http.StatusUnauthorized, "bad_mfa_code", "the code is wrong or has expired")
		return
	}
	recovery, plaintext, err := newRecoveryCodes()
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.ActivateTFA(ctx, u.ID, "email", recovery); err != nil {
		errInternal(w)
		return
	}
	h.auditTFA(ctx, audit.TFAEnrolled, u, map[string]any{"method": "email"})
	writeJSON(w, http.StatusOK, map[string]any{"method": "email", "recovery_codes": plaintext})
}

// disableTFA turns the second factor off after re-authentication with a valid
// current code or recovery code (REQ-AUTH-056).
func (h *Handler) disableTFA(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ctx := r.Context()
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if tfa == nil || tfa.Method == "off" {
		errConflict(w, "two-factor is not enabled")
		return
	}
	if h.verifySecondFactor(ctx, tfa, body.Code) == "" {
		APIError(w, http.StatusUnauthorized, "bad_mfa_code",
			"a current code or recovery code is required")
		return
	}
	method := tfa.Method
	if err := h.Store.ResetTFA(ctx, u.ID); err != nil {
		errInternal(w)
		return
	}
	h.auditTFA(ctx, audit.TFADisabled, u, map[string]any{"method": method})
	writeJSON(w, http.StatusOK, map[string]string{"method": "off"})
}

// --- admin reset (REQ-API-115) ---

// resetUserTFA clears any user's second factor — the lockout escape hatch.
// is_admin only; audited as tfa_reset with the target's email.
func (h *Handler) resetUserTFA(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Store.ResetTFA(ctx, userID); err != nil {
		errInternal(w)
		return
	}
	h.auditTFA(ctx, audit.TFAReset, adminUser, map[string]any{"target_email": target.Email})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- login-time challenge (Sequence G; called from auth.go) ---

// secondFactor is the two-factor gate between the account-active check and
// the success steps of login (Authentication_Authorization_Design.md §2.7).
// It applies to local and LDAP logins only — the OAuth2 path is exempt
// (GD-21). Returns the account's configured method, the verifying factor
// ("totp", "email" or "recovery"; "" when none applied) and whether login
// may proceed; a rejection response is written when it returns false.
func (h *Handler) secondFactor(w http.ResponseWriter, r *http.Request, body *loginRequest, u *db.User) (method, factor string, proceed bool) {
	ctx := r.Context()
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		errInternal(w)
		return "", "", false
	}
	method = "off"
	if tfa != nil {
		method = tfa.Method
	}
	if method == "off" {
		if h.Cfg.AuthRequire2FA { // installation-wide mandate (REQ-AUTH-055)
			APIError(w, http.StatusUnauthorized, "tfa_enrollment_required",
				"this installation requires two-factor authentication — set it up to sign in")
			return method, "", false
		}
		return method, "", true
	}
	if body.MFACode == "" {
		if method == "email" && h.Mail.Enabled() && h.tfaSends.allow(u.ID, time.Now()) {
			// Best effort: the challenge panel announces the send; a recovery
			// code stays accepted even when delivery fails (REQ-AUTH-057).
			_ = h.sendLoginCode(ctx, u)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":   "mfa_required",
			"method":  method,
			"message": "enter the second-factor code for this account",
		})
		return method, "", false
	}
	factor = h.verifySecondFactor(ctx, tfa, body.MFACode)
	if factor == "" {
		h.loginFailure(ctx, body, "bad_mfa_code")
		APIError(w, http.StatusUnauthorized, "bad_mfa_code", "the code is wrong or has expired")
		return method, "", false
	}
	return method, factor, true
}

// verifySecondFactor checks a submitted code against the account's record and
// returns the verifying factor: "totp", "email" or "recovery"; "" rejects.
// Replay prevention: an accepted TOTP step must be strictly newer than the
// stored watermark; email codes are consumed atomically; recovery codes are
// single-use (REQ-AUTH-054/057/059).
func (h *Handler) verifySecondFactor(ctx context.Context, tfa *db.UserTFA, code string) string {
	now := time.Now()
	switch tfa.Method {
	case "totp":
		if tfa.TotpSecret.Valid {
			step, ok := totp.Verify(tfa.TotpSecret.String, code, now)
			if ok && (!tfa.TotpLastStep.Valid || step > tfa.TotpLastStep.Int64) {
				if err := h.Store.SetTFALastStep(ctx, tfa.UserID, step); err == nil {
					return "totp"
				}
			}
		}
	case "email":
		if tfa.EmailCodeHash.Valid {
			ok, err := h.Store.ConsumeTFAEmailCode(ctx, tfa.UserID, totp.HashCode(code))
			if err == nil && ok {
				return "email"
			}
		}
	}
	// Recovery codes verify in place of either method (REQ-AUTH-059).
	if h.consumeRecoveryCode(ctx, tfa, code) {
		return "recovery"
	}
	return ""
}

// consumeRecoveryCode marks a matching unused recovery code as spent. Codes
// are unique by construction, so at most one entry can match; the scan runs
// to completion regardless, keeping timing independent of position.
func (h *Handler) consumeRecoveryCode(ctx context.Context, tfa *db.UserTFA, code string) bool {
	list := parseRecoveryCodes(tfa.RecoveryCodes)
	if len(list) == 0 {
		return false
	}
	want := totp.HashCode(code)
	idx := -1
	for i := range list {
		if list[i].H == want && !list[i].Used {
			idx = i
		}
	}
	if idx < 0 {
		return false // no match, or the only match was already spent
	}
	list[idx].Used = true
	raw, err := json.Marshal(list)
	if err != nil {
		return false
	}
	return h.Store.SetTFARecoveryCodes(ctx, tfa.UserID, string(raw)) == nil
}

// --- emailed codes ---

// sendLoginCode generates a fresh email OTP, stores only its hash with the
// configured expiry, and delivers it (REQ-AUTH-057). The plaintext code never
// touches a log or an audit detail.
func (h *Handler) sendLoginCode(ctx context.Context, u *db.User) error {
	code, err := totp.RandomDigits(totp.Digits)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(time.Duration(h.Cfg.TFAEmailCodeTTL) * time.Second).
		Format("2006-01-02 15:04:05")
	if err := h.Store.SetTFAEmailCode(ctx, u.ID, totp.HashCode(code), expires); err != nil {
		return err
	}
	subject := fmt.Sprintf("Your %s login code", h.Cfg.TotpIssuer)
	message := fmt.Sprintf(
		"Your %s login code is %s.\n\n"+
			"It expires in %d minutes and can be used once. If you did not try to\n"+
			"sign in, change your password and contact an administrator.\n",
		h.Cfg.TotpIssuer, code, h.Cfg.TFAEmailCodeTTL/60)
	return h.Mail.Send(u.Email, subject, message)
}

// tooManySends answers the per-account send cap (REQ-AUTH-057).
func (h *Handler) tooManySends(w http.ResponseWriter) {
	APIError(w, http.StatusTooManyRequests, "code_send_limited",
		"too many codes were requested recently — use a recovery code or try again later")
}

// sendLimiter caps emailed code requests per account. In-memory is enough:
// the cap defends mailbox spam and cost, not a security boundary that must
// survive restarts (the login_failure audit covers attempts).
type sendLimiter struct {
	mu sync.Mutex
	at map[int64][]time.Time
}

const (
	sendsPerWindow = 3
	sendWindow     = 5 * time.Minute
)

func newSendLimiter() *sendLimiter { return &sendLimiter{at: map[int64][]time.Time{}} }

// allow records a send for the account and reports whether it fits the cap.
func (l *sendLimiter) allow(userID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.at[userID][:0]
	for _, t := range l.at[userID] {
		if now.Sub(t) < sendWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= sendsPerWindow {
		l.at[userID] = kept
		return false
	}
	l.at[userID] = append(kept, now)
	return true
}

// --- recovery code storage helpers ---

// recoveryCode is one entry of the user_two_factor.recovery_codes JSON array:
// the SHA-256 hash and whether it has been spent (REQ-DB-038).
type recoveryCode struct {
	H    string `json:"h"`
	Used bool   `json:"used"`
}

// newRecoveryCodes returns the stored JSON and the plaintext list — the
// plaintext is shown exactly once, here (REQ-AUTH-059).
func newRecoveryCodes() (stored string, plaintext []string, err error) {
	plaintext, err = totp.GenerateRecoveryCodes()
	if err != nil {
		return "", nil, err
	}
	list := make([]recoveryCode, len(plaintext))
	for i, c := range plaintext {
		list[i] = recoveryCode{H: totp.HashCode(c)}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "", nil, err
	}
	return string(raw), plaintext, nil
}

// parseRecoveryCodes decodes the stored array; corrupt or empty values yield
// no codes rather than an error.
func parseRecoveryCodes(s string) []recoveryCode {
	if s == "" {
		return nil
	}
	var list []recoveryCode
	if json.Unmarshal([]byte(s), &list) != nil {
		return nil
	}
	return list
}

// userObject maps a user to the §4.3 response shape and stamps the current
// second factor (REQ-API-116). A lookup failure degrades to "off" — the
// field is informational, the enforcement path reads the row directly.
func (h *Handler) userObject(ctx context.Context, u *db.User, now time.Time) UserObject {
	obj := NewUserObject(u, now)
	if tfa, err := h.Store.GetTFA(ctx, u.ID); err == nil && tfa != nil && tfa.Method != "off" {
		obj.TFAMethod = tfa.Method
	}
	return obj
}

// auditTFA writes one of the tfa_* events (REQ-AUD-028). Details never carry
// codes or secrets — method names and target emails only.
func (h *Handler) auditTFA(ctx context.Context, event string, u *db.User, details map[string]any) {
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: event, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: details,
	})
}
