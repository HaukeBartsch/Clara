package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/db"
)

// Sequence H and the self-service change (GD-22/GD-23): invite, reset,
// completion uniformity against probing, the lockout interplay, and the
// pre-auth rate limit. The mail relay is replaced by the SendMail hook —
// tests assert on what would have been sent, never sending anything.

var tokenLinkRE = regexp.MustCompile(`token=([0-9a-f]{64})`)

// captureMail installs a fake relay that records the last message and hands
// back the extracted setup token.
func (e *env) captureMail() func() string {
	e.t.Helper()
	var body string
	e.Handler.SendMail = func(to, subject, msg string) error {
		body = to + "\n" + subject + "\n" + msg
		return nil
	}
	return func() string {
		m := tokenLinkRE.FindStringSubmatch(body)
		if m == nil {
			e.t.Fatalf("captured email carries no setup link: %q", body)
		}
		return m[1]
	}
}

// passwordHash reads the stored hash of an account.
func (e *env) passwordHash(userID int64) string {
	e.t.Helper()
	u, err := e.Store.GetUser(context.Background(), userID)
	if err != nil || u == nil {
		e.t.Fatalf("GetUser(%d): %v", userID, err)
	}
	return u.PasswordHash.String
}

func (e *env) mustLocalUser(email, password string) *db.User {
	e.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		e.t.Fatalf("bcrypt: %v", err)
	}
	u := &db.User{Email: email, DisplayName: "User", Enabled: true,
		PasswordHash: sql.NullString{String: string(hash), Valid: true}}
	id, err := e.Store.CreateUser(context.Background(), u)
	if err != nil {
		e.t.Fatalf("CreateUser: %v", err)
	}
	u.ID = id
	return u
}

func decodeOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if !body["ok"] {
		t.Fatalf("body = %s, want ok:true", rec.Body.String())
	}
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body.Error
}

func hasAudit(types []string, want string) bool {
	for _, s := range types {
		if s == want {
			return true
		}
	}
	return false
}

// --- invitation ---

func TestInvitePreconditions(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("invited@example.org")

	rec := e.do(http.MethodPost, "/api/v1/users/1/invite", nil, target)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin invite = %d, want 403", rec.Code)
	}
	rec = e.do(http.MethodPost, "/api/v1/users/9999/invite", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown target = %d, want 404", rec.Code)
	}
	// No relay configured: the admin sets the password directly instead.
	rec = e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	if code := decodeError(t, rec); rec.Code != http.StatusConflict || code != "smtp_not_configured" {
		t.Errorf("invite without SMTP = %d %s, want 409 smtp_not_configured", rec.Code, rec.Body.String())
	}
	// An identity-provider account has no local password to set.
	e.Handler.SendMail = func(string, string, string) error { return nil }
	idp := &db.User{Email: "idp@example.org", DisplayName: "IdP", Enabled: true, AuthSource: "oauth2"}
	id, err := e.Store.CreateUser(context.Background(), idp)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	idp.ID = id
	rec = e.do(http.MethodPost, "/api/v1/users/"+itoa(idp.ID)+"/invite", nil, admin)
	if rec.Code != http.StatusConflict {
		t.Errorf("invite to oauth2 account = %d, want 409", rec.Code)
	}
}

func TestInviteAndComplete(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("invited@example.org")
	tokenOf := e.captureMail()

	rec := e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite = %d %s, want 200", rec.Code, rec.Body.String())
	}
	decodeOK(t, rec)
	token := tokenOf()

	// Only the SHA-256 hash is stored — never the emailed value.
	var stored string
	if err := e.Store.DB.QueryRow(`SELECT token_hash FROM password_tokens WHERE user_id = ?`, target.ID).Scan(&stored); err != nil {
		t.Fatalf("token row: %v", err)
	}
	if stored == token || len(stored) != 64 {
		t.Errorf("stored hash = %q, want a 64-char digest distinct from the value", stored)
	}

	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": token, "password": "s3cret-choice"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite complete = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHash(target.ID)), []byte("s3cret-choice")); err != nil {
		t.Errorf("stored hash does not verify the chosen password: %v", err)
	}

	types := e.auditTypes()
	if !hasAudit(types, "user_invited") || !hasAudit(types, "invite_accepted") {
		t.Errorf("audit = %v, want user_invited and invite_accepted", types)
	}

	// Single use: the consumed token answers the generic rejection.
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": token, "password": "***"}, nil)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "invalid_setup_token" {
		t.Errorf("replay = %d %s, want 401 invalid_setup_token", rec.Code, rec.Body.String())
	}
}

func TestInviteReissueKillsOldLink(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("invited@example.org")
	tokenOf := e.captureMail()

	e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	oldToken := tokenOf()
	e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	newToken := tokenOf()

	rec := e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": oldToken, "password": "***"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("old invite link = %d, want 401 after re-invite", rec.Code)
	}
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": newToken, "password": "a-brand-new-pw"}, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("new invite link = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// --- reset request (no enumeration) ---

func TestPasswordResetRequestIsGeneric(t *testing.T) {
	e := newEnv(t)
	e.captureMail()

	unknown := e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": "ghost@example.org"}, nil)
	if unknown.Code != http.StatusAccepted {
		t.Fatalf("unknown address = %d, want 202", unknown.Code)
	}

	local := e.mustLocalUser("member@example.org", "old-password")
	got := e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": local.Email}, nil)
	if got.Code != http.StatusAccepted {
		t.Fatalf("local account = %d, want 202", got.Code)
	}
	if got.Body.String() != unknown.Body.String() {
		t.Errorf("bodies differ — enumeration oracle: %q vs %q", got.Body.String(), unknown.Body.String())
	}

	// The ghost produced no token; the member did.
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM password_tokens`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("password_tokens rows = %d, want exactly 1 (for the active local account)", n)
	}

	// Disabled accounts get nothing; the answer stays identical.
	disabled := e.mustUser("gone@example.org")
	if err := e.Store.SetUserEnabled(context.Background(), disabled.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec := e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": disabled.Email}, nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("disabled account = %d, want 202", rec.Code)
	}
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM password_tokens`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d (err %v), want still 1 — disabled accounts receive no link", n, err)
	}

	if types := e.auditTypes(); !hasAudit(types, "password_reset_requested") {
		t.Errorf("audit = %v, want password_reset_requested", types)
	}
}

func TestPasswordResetRateLimitPerAddress(t *testing.T) {
	e := newEnv(t)
	e.captureMail()
	for i := 0; i < resetsPerWindow; i++ {
		rec := e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
			map[string]string{"email": "dripper@example.org"}, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d = %d, want 202", i+1, rec.Code)
		}
	}
	rec := e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": "dripper@example.org"}, nil)
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec) != "rate_limited" {
		t.Errorf("4th request = %d %s, want 429 rate_limited", rec.Code, rec.Body.String())
	}
	// The cap is per address, not global.
	rec = e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": "other@example.org"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("different address = %d, want 202", rec.Code)
	}
}

// --- completion uniformity and cross-token invalidation ---

func TestSetupTokenFailuresAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("invited@example.org")
	tokenOf := e.captureMail()

	e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	inviteToken := tokenOf()

	cases := []struct {
		name  string
		path  string
		token string
	}{
		{"unknown", "/api/v1/auth/password-reset/complete", strings.Repeat("a", 64)},
		{"wrong purpose", "/api/v1/auth/password-reset/complete", inviteToken},
	}
	for _, tc := range cases {
		rec := e.do(http.MethodPost, tc.path,
			map[string]string{"token": tc.token, "password": "***"}, nil)
		if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "invalid_setup_token" {
			t.Errorf("%s: %d %s, want 401 invalid_setup_token", tc.name, rec.Code, rec.Body.String())
		}
	}

	// Expired invite token.
	if _, err := e.Store.DB.Exec(
		`UPDATE password_tokens SET expires_at = '2020-01-01 00:00:00' WHERE user_id = ?`, target.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	rec := e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": inviteToken, "password": "***"}, nil)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "invalid_setup_token" {
		t.Errorf("expired: %d %s, want 401 invalid_setup_token", rec.Code, rec.Body.String())
	}
}

func TestResetCompletionInvalidatesOtherTokens(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustLocalUser("member@example.org", "old-password")
	tokenOf := e.captureMail()

	// An outstanding invite link…
	e.do(http.MethodPost, "/api/v1/users/"+itoa(member.ID)+"/invite", nil, admin)
	inviteToken := tokenOf()
	// …and a reset request that supersedes everything.
	e.do(http.MethodPost, "/api/v1/auth/password-reset/request",
		map[string]string{"email": member.Email}, nil)
	resetToken := tokenOf()

	rec := e.do(http.MethodPost, "/api/v1/auth/password-reset/complete",
		map[string]string{"token": resetToken, "password": "a-brand-new-pw"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset complete = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHash(member.ID)), []byte("a-brand-new-pw")); err != nil {
		t.Errorf("new password not stored: %v", err)
	}
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": inviteToken, "password": "***"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("invite link after reset = %d, want 401 — all outstanding tokens must die", rec.Code)
	}
	if types := e.auditTypes(); !hasAudit(types, "password_reset_completed") {
		t.Errorf("audit = %v, want password_reset_completed", types)
	}
}

// --- self-service change (REQ-API-118/061) ---

func TestChangeMyPassword(t *testing.T) {
	e := newEnv(t)
	oldPW, freshPW := "old-password", "fresh-water-91"
	member := e.mustLocalUser("member@example.org", oldPW)
	path := "/api/v1/users/me/password"

	// Accounts without a local credential answer 409.
	plain := e.mustUser("idp-only@example.org")
	rec := e.do(http.MethodPut, path,
		map[string]string{"current_password": "x", "new_password": "***"}, plain)
	if rec.Code != http.StatusConflict || decodeError(t, rec) != "no_local_credential" {
		t.Errorf("no credential = %d %s, want 409 no_local_credential", rec.Code, rec.Body.String())
	}

	// Wrong current password: 401 + audited.
	rec = e.do(http.MethodPut, path,
		map[string]string{"current_password": "wrong", "new_password": "***"}, member)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "bad_password" {
		t.Errorf("wrong current = %d %s, want 401 bad_password", rec.Code, rec.Body.String())
	}
	if types := e.auditTypes(); !hasAudit(types, "login_failure") {
		t.Errorf("audit = %v, want the failure audited", types)
	}

	// Correct proof: the new hash is stored and audited.
	rec = e.do(http.MethodPut, path,
		map[string]string{"current_password": oldPW, "new_password": freshPW}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("change = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHash(member.ID)), []byte(freshPW)); err != nil {
		t.Errorf("new hash does not verify: %v", err)
	}
	if types := e.auditTypes(); !hasAudit(types, "password_changed") {
		t.Errorf("audit = %v, want password_changed", types)
	}

	// Five wrong proofs lock the address (REQ-AUTH-061 counts toward §2.5).
	for i := 0; i < lockoutThreshold; i++ {
		e.do(http.MethodPut, path,
			map[string]string{"current_password": "wrong", "new_password": "***"}, member)
	}
	rec = e.do(http.MethodPut, path,
		map[string]string{"current_password": freshPW, "new_password": "***"}, member)
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec) != "rate_limited" {
		t.Errorf("after lockout = %d %s, want 429 rate_limited", rec.Code, rec.Body.String())
	}
}

// --- Sequence E on login (REQ-AUTH-035) ---

func TestLoginLockoutSequenceE(t *testing.T) {
	e := newEnv(t)
	member := e.mustLocalUser("member@example.org", "right-password")
	login := func(password string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": member.Email, "source": "local", "password": password}, nil)
	}
	for i := 0; i < lockoutThreshold; i++ {
		if rec := login("nope"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := login("right-password") // correct credential, but the address is locked
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec) != "rate_limited" {
		t.Errorf("locked login = %d %s, want 429 rate_limited", rec.Code, rec.Body.String())
	}
	if types := e.auditTypes(); !hasAudit(types, "login_failure") {
		t.Errorf("audit = %v, want login_failure rows", types)
	}
}

// --- password policy (security finding F12) ---

func TestPasswordPolicyOnSetupCompletion(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("invited@example.org")
	tokenOf := e.captureMail()
	e.do(http.MethodPost, "/api/v1/users/"+itoa(target.ID)+"/invite", nil, admin)
	token := tokenOf()

	// Too short: a clear 400 — and the token survives for one more try.
	rec := e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": token, "password": "short"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password = %d %s, want 400", rec.Code, rec.Body.String())
	}
	// Too long (bcrypt would fail with a 500 beyond 72 bytes): also a 400.
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": token, "password": strings.Repeat("x", 73)}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("long password = %d %s, want 400", rec.Code, rec.Body.String())
	}
	// A policy-compliant choice still completes.
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": token, "password": "a-good-long-password"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compliant password = %d %s, want 200", rec.Code, rec.Body.String())
	}

	// An invalid token is still the one indistinguishable rejection — the
	// policy check must not become an oracle for token validity.
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": strings.Repeat("a", 64), "password": "short"}, nil)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "invalid_setup_token" {
		t.Errorf("invalid token + short password = %d %s, want 401 invalid_setup_token",
			rec.Code, rec.Body.String())
	}
}

func TestPasswordPolicyOnSelfServiceChange(t *testing.T) {
	e := newEnv(t)
	member := e.mustLocalUser("member@example.org", "old-password-long")
	path := "/api/v1/users/me/password"

	// Wrong current password keeps its answer even with a short new one.
	rec := e.do(http.MethodPut, path,
		map[string]string{"current_password": "wrong", "new_password": "short"}, member)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "bad_password" {
		t.Fatalf("wrong current = %d %s, want 401 bad_password", rec.Code, rec.Body.String())
	}

	// Right proof, non-compliant new password: 400 and the hash stands.
	rec = e.do(http.MethodPut, path,
		map[string]string{"current_password": "old-password-long", "new_password": "short"}, member)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short new password = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if err := bcrypt.CompareHashAndPassword([]byte(e.passwordHash(member.ID)), []byte("old-password-long")); err != nil {
		t.Errorf("rejected change must leave the stored hash intact: %v", err)
	}

	// Compliant new password succeeds.
	rec = e.do(http.MethodPut, path,
		map[string]string{"current_password": "old-password-long", "new_password": "a-good-long-password"}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("compliant change = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestValidatePasswordPolicy pins the exported rule cmd/server applies to
// ADMIN_BOOTSTRAP_PASSWORD at startup.
func TestValidatePasswordPolicy(t *testing.T) {
	if err := ValidatePasswordPolicy("twelve-char!"); err != nil {
		t.Errorf("12-char password rejected: %v", err)
	}
	if err := ValidatePasswordPolicy(strings.Repeat("x", 72)); err != nil {
		t.Errorf("72-byte password rejected: %v", err)
	}
	if err := ValidatePasswordPolicy(strings.Repeat("x", 73)); err == nil {
		t.Error("73-byte password accepted")
	}
	if err := ValidatePasswordPolicy("eleven-ch"); err == nil {
		t.Error("11-char password accepted")
	}
	// Length counts characters, not bytes.
	if err := ValidatePasswordPolicy("fünf-wörters"); err != nil { // 12 runes / 14 bytes
		t.Errorf("multi-byte password rejected: %v", err)
	}
}
