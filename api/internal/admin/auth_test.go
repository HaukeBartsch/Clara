package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/db"
)

// mustLocalUser inserts a user with a bcrypt password hash (GD-18).
func mustLocalUser(t *testing.T, e *env, email, password string) *db.User {
	t.Helper()
	u := e.mustUser(email)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := e.Store.SetUserPasswordHash(context.Background(), u.ID,
		sql.NullString{String: string(hash), Valid: true}); err != nil {
		t.Fatalf("SetUserPasswordHash: %v", err)
	}
	return u
}

func hasType(types []string, want string) bool {
	for _, s := range types {
		if s == want {
			return true
		}
	}
	return false
}

// TestAuthLocalLoginSuccess covers the GD-18 happy path: 200 user object,
// last_login_at stamped, login_success audited.
func TestAuthLocalLoginSuccess(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.ID != u.ID || obj.Email != "user@example.org" || obj.Status != "active" {
		t.Errorf("user object = %+v", obj)
	}
	if obj.AuthSource != "local" {
		t.Errorf("auth_source = %q, want local", obj.AuthSource)
	}
	if obj.LastLoginAt == nil {
		t.Error("last_login_at = nil, want stamped")
	}

	stored, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !stored.LastLoginAt.Valid {
		t.Error("stored last_login_at not set")
	}
	if !hasType(e.auditTypes(), "login_success") {
		t.Errorf("audit types = %v, want login_success", e.auditTypes())
	}
}

// TestAuthLocalLoginWrongPassword: mismatch is 401 bad_password (REQ-AUTH-050).
func TestAuthLocalLoginWrongPassword(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "wrong",
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "bad_password" {
		t.Errorf("error = %q, want bad_password", eb.Error)
	}
	types := e.auditTypes()
	if !hasType(types, "login_failure") {
		t.Errorf("audit types = %v, want login_failure", types)
	}
	if hasType(types, "login_success") {
		t.Errorf("unexpected login_success for failed attempt")
	}
}

// TestAuthLocalLoginMissingHash: no stored hash is indistinguishable from a
// wrong password (GD-18).
func TestAuthLocalLoginMissingHash(t *testing.T) {
	e := newEnv(t)
	e.mustUser("nohash@example.org") // created without a password

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "nohash@example.org", "source": "local", "password": "anything",
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "bad_password" {
		t.Errorf("error = %q, want bad_password", eb.Error)
	}
}

// TestAuthUnknownEmail: no row is 401 account_not_found + audit (REQ-AUTH-006).
func TestAuthUnknownEmail(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "ghost@example.org", "source": "oauth2",
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "account_not_found" {
		t.Errorf("error = %q, want account_not_found", eb.Error)
	}
	if !hasType(e.auditTypes(), "login_failure") {
		t.Errorf("audit types = %v, want login_failure", e.auditTypes())
	}
}

// TestAuthDisabledAccount: disabled is 403 account_disabled (REQ-AUTH-053).
func TestAuthDisabledAccount(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "gone@example.org", "pw")
	if err := e.Store.SetUserEnabled(context.Background(), u.ID, false); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "gone@example.org", "source": "local", "password": "pw",
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "account_disabled" {
		t.Errorf("error = %q, want account_disabled", eb.Error)
	}
	types := e.auditTypes()
	if !hasType(types, "login_failure") {
		t.Errorf("audit types = %v, want login_failure", types)
	}
}

// TestAuthExpiredAccount: a passed valid_until is 403 account_expired (GD-19).
func TestAuthExpiredAccount(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("old@example.org")
	if err := e.Store.SetUserValidUntil(context.Background(), u.ID,
		sql.NullString{String: "2020-01-01", Valid: true}); err != nil {
		t.Fatalf("SetUserValidUntil: %v", err)
	}

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "old@example.org", "source": "oauth2",
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "account_expired" {
		t.Errorf("error = %q, want account_expired", eb.Error)
	}
	if !hasType(e.auditTypes(), "login_failure") {
		t.Errorf("audit types = %v, want login_failure", e.auditTypes())
	}
}

// TestAuthOAuth2Login: provider login stamps auth_source and last_login_at
// on the row and audits login_success (REQ-AUTH-005).
func TestAuthOAuth2Login(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("sso@example.org")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "sso@example.org", "source": "oauth2", "provider": "https://idp.example.org",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.AuthSource != "oauth2" {
		t.Errorf("auth_source = %q, want oauth2", obj.AuthSource)
	}
	if obj.LastLoginAt == nil {
		t.Error("last_login_at = nil, want stamped")
	}

	stored, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.AuthSource != "oauth2" || !stored.LastLoginAt.Valid {
		t.Errorf("stored row = %+v, want auth_source oauth2 and last_login_at set", stored)
	}
	if !hasType(e.auditTypes(), "login_success") {
		t.Errorf("audit types = %v, want login_success", e.auditTypes())
	}
}

// TestAuthBootstrapPromotion: logging in with ADMIN_BOOTSTRAP_EMAIL creates
// the row enabled and is_admin (REQ-AUTH-007). env.Cfg is shared with the
// handler, so mutating it before the request configures promotion.
func TestAuthBootstrapPromotion(t *testing.T) {
	e := newEnv(t)
	e.Cfg.AdminBootstrapEmail = "root@example.org"

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "root@example.org", "source": "oauth2",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if !obj.IsAdmin || !obj.Enabled || obj.Status != "active" {
		t.Errorf("user object = %+v, want enabled admin", obj)
	}

	stored, err := e.Store.GetUserByEmail(context.Background(), "root@example.org")
	if err != nil || stored == nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !stored.IsAdmin || !stored.Enabled {
		t.Errorf("stored row = %+v, want enabled admin", stored)
	}
	if !hasType(e.auditTypes(), "login_success") {
		t.Errorf("audit types = %v, want login_success", e.auditTypes())
	}
}

// TestAuthLoginInvalidRequest: malformed JSON and an unknown source are 400
// invalid_request (§4.2).
func TestAuthLoginInvalidRequest(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/api/v1/auth/login", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", eb.Error)
	}

	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "carrier_pigeon",
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestAuthLogout: logout audits the event (REQ-AUTH-008) and returns ok;
// without an acting user it is forbidden.
func TestAuthLogout(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("out@example.org")

	rec := e.do("POST", "/api/v1/auth/logout", nil, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	e.decode(rec, &resp)
	if resp["status"] != "ok" {
		t.Errorf("response = %v, want status ok", resp)
	}
	if !hasType(e.auditTypes(), "logout") {
		t.Errorf("audit types = %v, want logout", e.auditTypes())
	}

	rec = e.do("POST", "/api/v1/auth/logout", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d without actor, want 403", rec.Code)
	}
}

// --- POST /api/v1/auth/verify-password (REQ-API-123, Sequence I §2.9) ---

// lastAuditDetails returns the details JSON of the newest event of a type.
func lastAuditDetails(t *testing.T, e *env, eventType string) map[string]any {
	t.Helper()
	var raw string
	err := e.Store.DB.QueryRow(
		`SELECT details FROM audit_events WHERE event_type = ? ORDER BY id DESC LIMIT 1`,
		eventType).Scan(&raw)
	if err != nil {
		t.Fatalf("query %s details: %v", eventType, err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("decode details %q: %v", raw, err)
	}
	return d
}

// TestVerifyPasswordOK: a correct password answers ok without any side
// effect — no audit event, no last_login_at or auth_source write (REQ-AUTH-065).
func TestVerifyPasswordOK(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	before, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil || before == nil {
		t.Fatalf("GetUser: %v", err)
	}

	rec := e.do("POST", "/api/v1/auth/verify-password", map[string]any{
		"email": "user@example.org", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	e.decode(rec, &resp)
	if resp["status"] != "ok" {
		t.Errorf("response = %v, want status ok", resp)
	}
	if types := e.auditTypes(); len(types) != 0 {
		t.Errorf("audit types = %v, want none — verify is side-effect-free", types)
	}
	stored, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.LastLoginAt != before.LastLoginAt || stored.AuthSource != before.AuthSource {
		t.Errorf("stored row mutated: last_login_at %v→%v, auth_source %q→%q",
			before.LastLoginAt, stored.LastLoginAt, before.AuthSource, stored.AuthSource)
	}
}

// TestVerifyPasswordBadCredentials: a wrong password and an unknown account
// answer identically with 401 bad_password (§2.6 step 2). Rejections are no
// longer silent — each one leaves a login_failure entry (security finding F1),
// identically for both cases so the answer stays indistinguishable.
func TestVerifyPasswordBadCredentials(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	for _, email := range []string{"user@example.org", "ghost@example.org"} {
		rec := e.do("POST", "/api/v1/auth/verify-password", map[string]any{
			"email": email, "password": "wrong",
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", email, rec.Code)
		}
		var eb ErrorBody
		e.decode(rec, &eb)
		if eb.Error != "bad_password" {
			t.Errorf("%s: error = %q, want bad_password (not distinguishable)", email, eb.Error)
		}
	}
	types := e.auditTypes()
	if len(types) != 2 {
		t.Fatalf("audit types = %v, want one login_failure per rejection", types)
	}
	for _, ty := range types {
		if ty != "login_failure" {
			t.Errorf("audit type = %q, want login_failure", ty)
		}
	}
}

// TestVerifyPasswordAccountState: the account-active rule is surfaced as the
// specific reason (REQ-AUTH-065) — disabled and expired local accounts pass
// the hash check first.
func TestVerifyPasswordAccountState(t *testing.T) {
	e := newEnv(t)

	disabled := mustLocalUser(t, e, "off@example.org", "hunter2")
	if _, err := e.Store.DB.Exec(`UPDATE users SET enabled = 0 WHERE id = ?`, disabled.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec := e.do("POST", "/api/v1/auth/verify-password", map[string]any{
		"email": "off@example.org", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var eb ErrorBody
	e.decode(rec, &eb)
	if eb.Error != "account_disabled" {
		t.Errorf("error = %q, want account_disabled", eb.Error)
	}

	expired := mustLocalUser(t, e, "old@example.org", "hunter2")
	if err := e.Store.SetUserValidUntil(context.Background(), expired.ID,
		sql.NullString{String: "2020-01-01", Valid: true}); err != nil {
		t.Fatalf("SetUserValidUntil: %v", err)
	}
	rec = e.do("POST", "/api/v1/auth/verify-password", map[string]any{
		"email": "old@example.org", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expired: status = %d, want 403", rec.Code)
	}
	e.decode(rec, &eb)
	if eb.Error != "account_expired" {
		t.Errorf("error = %q, want account_expired", eb.Error)
	}
}

// TestLoginSourceNameAudited: the name the user selected on the login page is
// recorded in the login_success and login_failure details and nothing else
// (REQ-AUTH-067).
func TestLoginSourceNameAudited(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local",
		"password": "hunter2", "source_name": "Hospital 1",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := lastAuditDetails(t, e, "login_success")["source_name"]; got != "Hospital 1" {
		t.Errorf("login_success source_name = %v, want Hospital 1", got)
	}

	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local",
		"password": "wrong", "source_name": "Hospital 2",
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := lastAuditDetails(t, e, "login_failure")["source_name"]; got != "Hospital 2" {
		t.Errorf("login_failure source_name = %v, want Hospital 2", got)
	}
}

// TestLoginRaceFailureAudited: the finalizing call of a failed credential
// race yields exactly one login_failure carrying the per-source outcomes in
// attempts with reason bad_credentials — or provider_unavailable when no
// source was reachable (DEV-AUD-5, Audit_Logging_Design.md §3.1).
func TestLoginRaceFailureAudited(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "wrong",
		"source_name": "Hospital 1",
		"attempts":    map[string]string{"local": "bad_password", "ldap-2": "unreachable"},
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	types := e.auditTypes()
	if n := countType(types, "login_failure"); n != 1 {
		t.Errorf("%d login_failure rows, want exactly one per submission", n)
	}
	d := lastAuditDetails(t, e, "login_failure")
	if d["reason"] != "bad_credentials" {
		t.Errorf("reason = %v, want bad_credentials", d["reason"])
	}
	attempts, ok := d["attempts"].(map[string]any)
	if !ok || attempts["local"] != "bad_password" || attempts["ldap-2"] != "unreachable" {
		t.Errorf("attempts = %v, want per-source outcomes", d["attempts"])
	}

	// All sources unreachable → provider_unavailable.
	e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "ghost@example.org", "source": "ldap",
		"source_name": "Hospital 2",
		"attempts":    map[string]string{"ldap-1": "unreachable", "ldap-2": "unreachable"},
	}, nil)
	if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "provider_unavailable" {
		t.Errorf("reason = %v, want provider_unavailable", got)
	}

	// An account-state rejection keeps its specific reason (REQ-AUTH-065).
	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "wrong",
		"attempts": map[string]string{"local": "bad_password"},
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "bad_credentials" {
		t.Errorf("reason = %v, want bad_credentials", got)
	}
}

// TestVerifyPasswordLockoutAndAudit (security finding F1): failed
// verify-password attempts count toward the shared Sequence E lockout and
// leave the login_failure trail — the endpoint can no longer be hammered to
// guess passwords without either consequence. The same budget covers login,
// so splitting the flow across the two endpoints does not double it.
func TestVerifyPasswordLockoutAndAudit(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	for i := 0; i < lockoutThreshold; i++ {
		rec := e.do("POST", "/api/v1/auth/verify-password", map[string]any{
			"email": "user@example.org", "password": "wrong",
		}, nil)
		if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "bad_password" {
			t.Fatalf("attempt %d = %d %s, want 401 bad_password", i+1, rec.Code, rec.Body.String())
		}
	}

	// Locked: even the correct credential is rejected before any probing.
	rec := e.do("POST", "/api/v1/auth/verify-password", map[string]any{
		"email": "user@example.org", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec) != "rate_limited" {
		t.Errorf("locked verify = %d %s, want 429 rate_limited", rec.Code, rec.Body.String())
	}

	// The same lock guards login for the address.
	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("locked login = %d, want 429", rec.Code)
	}

	types := e.auditTypes()
	if !hasType(types, "login_failure") {
		t.Errorf("audit = %v, want the guessing trail as login_failure", types)
	}
	if hasType(types, "login_success") {
		t.Error("unexpected login_success for rejected attempts")
	}
}
