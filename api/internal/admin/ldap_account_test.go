package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"csms/api/internal/testdb"
)

// REQ-AUTH-091 (DEV-AUTH-21): a directory that bound the user is the authority on
// who they are, so a first LDAP sign-in creates the account — not an administrator,
// in no project — instead of refusing it. Every other path keeps REQ-AUTH-006.

func countUsers(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

func ldapLogin(_ *env, email, displayName string) map[string]any {
	body := map[string]any{"email": email, "source": "ldap", "provider": "ldap-1", "source_name": "Helse Bergen"}
	if displayName != "" {
		body["display_name"] = displayName
	}
	return body
}

func TestLDAPFirstSignInCreatesTheAccount(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "new.person@hospital.example.org", "New Person"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.Email != "new.person@hospital.example.org" || obj.DisplayName != "New Person" ||
		obj.IsAdmin || !obj.Enabled || obj.AuthSource != "ldap" || obj.LastLoginAt == nil {
		t.Errorf("user object = %+v", obj)
	}

	stored, err := e.Store.GetUser(context.Background(), obj.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.PasswordHash.Valid {
		t.Error("a directory account got a local password")
	}
	d := lastAuditDetails(t, e, "user_created")
	if d["automatic"] != true || d["source"] != "ldap" || d["provider"] != "ldap-1" {
		t.Errorf("user_created details = %v", d)
	}
	if !hasType(e.auditTypes(), "login_success") {
		t.Errorf("audit types = %v, want login_success", e.auditTypes())
	}

	// No project knows the account: the dashboard shows the information page
	// (REQ-AUTH-028) until an administrator adds it to one.
	rec = e.do("GET", "/api/v1/users/me/projects", nil, stored)
	if rec.Code != http.StatusOK {
		t.Fatalf("my projects: %d %s", rec.Code, rec.Body.String())
	}
	var mine []map[string]any
	e.decode(rec, &mine)
	if len(mine) != 0 {
		t.Errorf("memberships = %v, want none", mine)
	}

	// The second sign-in finds the account and creates nothing.
	before := countUsers(t, e)
	if rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "new.person@hospital.example.org", "Renamed"), nil); rec.Code != http.StatusOK {
		t.Fatalf("second sign-in: %d %s", rec.Code, rec.Body.String())
	}
	if countUsers(t, e) != before {
		t.Error("the second sign-in created another account")
	}
}

func TestLDAPFirstSignInWithoutADisplayName(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "kari@hospital.example.org", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.DisplayName != "kari" {
		t.Errorf("display_name = %q, want the address's local part", obj.DisplayName)
	}
}

// A directory name longer than the column is cut on a character boundary, never inside
// a two-byte letter.
func TestLDAPFirstSignInCutsALongNameOnACharacter(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("ø", 200) // 400 bytes; byte 255 is the second byte of an ø
	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "long@hospital.example.org", long), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if !utf8.ValidString(obj.DisplayName) || len(obj.DisplayName) != 254 {
		t.Errorf("display_name = %d bytes, valid UTF-8 %v; want 254 bytes", len(obj.DisplayName), utf8.ValidString(obj.DisplayName))
	}
}

// An address spelled with other capitals is the same person's account; it is not
// duplicated, and a disable on it sticks.
func TestLDAPSignInMatchesAnAddressIgnoringCase(t *testing.T) {
	e := newEnv(t)
	existing := e.mustUser("Ola.Nordmann@Hospital.example.org")

	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "ola.nordmann@hospital.example.org", "Ola"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.ID != existing.ID || countUsers(t, e) != 1 {
		t.Errorf("signed in as %d with %d accounts, want the existing account %d", obj.ID, countUsers(t, e), existing.ID)
	}

	e.exec(`UPDATE users SET enabled = 0 WHERE id = ?`, existing.ID)
	rec = e.do("POST", "/api/v1/auth/login", ldapLogin(e, "ola.nordmann@hospital.example.org", "Ola"), nil)
	if rec.Code != http.StatusForbidden || decodeError(t, rec) != "account_disabled" {
		t.Errorf("disabled = %d %s, want 403 account_disabled", rec.Code, rec.Body.String())
	}
	if countUsers(t, e) != 1 {
		t.Error("a disabled account was replaced by a new one")
	}
}

// Two stored addresses differing only in case can only exist on SQLite: MariaDB's
// default collation already makes users.email unique without regard to case.
func TestLDAPSignInRefusesAmbiguousAddresses(t *testing.T) {
	if testdb.Engine() == "mariadb" {
		t.Skip("MariaDB's case-insensitive UNIQUE index cannot hold two such addresses")
	}
	e := newEnv(t)
	e.mustUser("Twin@hospital.example.org")
	e.mustUser("twin@Hospital.example.org")

	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "TWIN@hospital.example.org", ""), nil)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "account_not_found" {
		t.Errorf("ambiguous = %d %s, want 401 account_not_found", rec.Code, rec.Body.String())
	}
	if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "account_ambiguous" {
		t.Errorf("reason = %v, want account_ambiguous", got)
	}
	if countUsers(t, e) != 2 {
		t.Errorf("accounts = %d, want the two that existed", countUsers(t, e))
	}
}

func TestLDAPSignInRefusesMalformedAddresses(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"no-at-sign", "two@@example.org", "@example.org", "space in@example.org"} {
		rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, bad, ""), nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%q = %d, want 401", bad, rec.Code)
		}
	}
	if n := countUsers(t, e); n != 0 {
		t.Errorf("accounts = %d, want none", n)
	}
}

// A failed credential race and the other sources create nothing.
func TestOnlyASuccessfulLDAPSignInCreatesAnAccount(t *testing.T) {
	e := newEnv(t)

	e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "ghost@example.org", "source": "ldap", "provider": "ldap-1",
		"attempts": map[string]string{"ldap-1": "bad_credentials"},
	}, nil)
	e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "ghost@example.org", "source": "local", "password": "whatever",
	}, nil)
	if n := countUsers(t, e); n != 0 {
		t.Errorf("accounts = %d, want none", n)
	}
}

// With two-factor authentication mandatory, the new account goes to enrollment
// like every other account without a factor (REQ-AUTH-055).
func TestLDAPFirstSignInMeetsTheTwoFactorMandate(t *testing.T) {
	e := newEnv(t)
	e.Cfg.AuthRequire2FA = true

	rec := e.do("POST", "/api/v1/auth/login", ldapLogin(e, "new@hospital.example.org", ""), nil)
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "tfa_enrollment_required" {
		t.Fatalf("= %d %s, want 401 tfa_enrollment_required", rec.Code, rec.Body.String())
	}
	if countUsers(t, e) != 1 {
		t.Error("the account was not created before the enrollment step")
	}
}
