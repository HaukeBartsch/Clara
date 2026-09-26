package admin

import (
	"context"
	"database/sql"
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
