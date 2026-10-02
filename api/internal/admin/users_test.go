package admin

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"csms/api/internal/db"
)

func TestListUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	e.mustUser("user@example.org")

	rec := e.do("GET", "/api/v1/users", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var out []UserObject
	e.decode(rec, &out)
	if len(out) != 2 {
		t.Fatalf("users: %s", rec.Body.String())
	}
	for _, o := range out {
		if o.Status != "active" {
			t.Fatalf("derived status: %+v", o)
		}
	}

	rec = e.do("GET", "/api/v1/users", nil, e.mustUser("other@example.org"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", rec.Code)
	}
}

func TestCreateAndReEnableUser(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")

	rec := e.do("POST", "/api/v1/users", map[string]any{
		"email": "new@example.org", "display_name": "New User",
		"valid_days": 90, "password": "a-long-enough-pw",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created UserObject
	e.decode(rec, &created)
	if created.Email != "new@example.org" || !created.Enabled || created.ValidUntil == nil {
		t.Fatalf("created user: %+v", created)
	}
	wantUntil := time.Now().UTC().AddDate(0, 0, 90).Format("2006-01-02")
	if *created.ValidUntil != wantUntil {
		t.Fatalf("valid_until = %s, want %s", *created.ValidUntil, wantUntil)
	}

	// The password is stored only as a bcrypt hash.
	u, err := e.Store.GetUserByEmail(context.Background(), "new@example.org")
	if err != nil || u == nil || !u.PasswordHash.Valid {
		t.Fatalf("password hash: %v", err)
	}
	if u.PasswordHash.String == "s3cret" || len(u.PasswordHash.String) < 20 {
		t.Fatalf("password not hashed: %q", u.PasswordHash.String)
	}

	// A live account with the email conflicts.
	rec = e.do("POST", "/api/v1/users", map[string]any{"email": "new@example.org"}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d, want 409", rec.Code)
	}

	// valid_days must not be negative.
	rec = e.do("POST", "/api/v1/users", map[string]any{"email": "x@example.org", "valid_days": -1}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative days: %d, want 400", rec.Code)
	}

	// A disabled account with the same email is re-enabled → 200.
	if err := e.Store.SetUserEnabled(context.Background(), created.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = e.do("POST", "/api/v1/users", map[string]any{
		"email": "new@example.org", "display_name": "New User",
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", rec.Code, rec.Body.String())
	}
	var again UserObject
	e.decode(rec, &again)
	if !again.Enabled || again.Status != "active" {
		t.Fatalf("re-enabled user: %+v", again)
	}

	// The audit trail marks the re-enable and never carries a password.
	var sawReEnabled bool
	rows, _ := e.Store.DB.Query(`SELECT details FROM audit_events WHERE event_type = 'user_created'`)
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && contains(d, `"re_enabled":true`) {
			sawReEnabled = true
		}
	}
	if !sawReEnabled {
		t.Fatalf("missing re_enabled audit flag")
	}
}

func TestUpdateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	target := e.mustUser("target@example.org")
	// A prior login distinguishes an admin disable from inactivity
	// auto-disable in the derived status (REQ-AUTH-053).
	if _, err := e.Store.DB.Exec(`UPDATE users SET last_login_at = '2026-09-01 10:00:00' WHERE id = ?`, target.ID); err != nil {
		t.Fatalf("seed login: %v", err)
	}

	// Disable → derived status disabled; audit records old/new as 0/1.
	rec := e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"enabled": false}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	var out UserObject
	e.decode(rec, &out)
	if out.Enabled || out.Status != "disabled" {
		t.Fatalf("disabled user: %+v", out)
	}

	// valid_days 0 → indefinite (valid_until null); 30 sets a date.
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"enabled": true, "valid_days": 30}, admin)
	e.decode(rec, &out)
	if out.ValidUntil == nil {
		t.Fatalf("valid_days 30: %+v", out)
	}
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"valid_days": 0}, admin)
	e.decode(rec, &out)
	if out.ValidUntil != nil {
		t.Fatalf("indefinite: %+v", out)
	}

	// Password set then cleared; the trail says password_changed only.
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"password": "hunter2-set-now"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("set password: %d", rec.Code)
	}
	u, _ := e.Store.GetUserByEmail(context.Background(), "target@example.org")
	if !u.PasswordHash.Valid {
		t.Fatalf("hash not stored")
	}
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"password": ""}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear password: %d", rec.Code)
	}
	u, _ = e.Store.GetUserByEmail(context.Background(), "target@example.org")
	if u.PasswordHash.Valid {
		t.Fatalf("hash not cleared")
	}

	var sawChanged, leaked bool
	rows, _ := e.Store.DB.Query(`SELECT details FROM audit_events WHERE event_type = 'user_updated'`)
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil {
			if contains(d, `"password_changed":true`) {
				sawChanged = true
			}
			if contains(d, "hunter2") {
				leaked = true
			}
		}
	}
	if !sawChanged || leaked {
		t.Fatalf("user_updated audit: changed=%v leaked=%v", sawChanged, leaked)
	}

	// Unknown attributes and unknown users are rejected.
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"role": "admin"}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown attr: %d, want 400", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/users/999999", map[string]any{"enabled": false}, admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d, want 404", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"enabled": false}, e.mustUser("nope@example.org"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", rec.Code)
	}
}

// userUpdatedDetails returns the details JSON of every user_updated audit
// entry in insertion order.
func userUpdatedDetails(e *env) []string {
	e.t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT details FROM audit_events WHERE event_type = 'user_updated' ORDER BY id`)
	if err != nil {
		e.t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			e.t.Fatalf("audit scan: %v", err)
		}
		out = append(out, d)
	}
	return out
}

// TestUpdateUserAdminFlag covers the is_admin grant/revoke of REQ-API-136 and
// the last-enabled-admin invariant of REQ-AUTH-068.
func TestUpdateUserAdminFlag(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org") // the only enabled administrator
	target := e.mustUser("target@example.org")

	// Grant: 200, stored flag set, audit carries old/new.
	rec := e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"is_admin": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if !obj.IsAdmin {
		t.Fatalf("grant response: %+v, want is_admin true", obj)
	}
	u, _ := e.Store.GetUser(context.Background(), target.ID)
	if !u.IsAdmin {
		t.Fatalf("grant not stored")
	}
	details := userUpdatedDetails(e)
	if len(details) != 1 || !contains(details[0], `"is_admin"`) {
		t.Fatalf("grant audit: %v, want one entry naming is_admin", details)
	}

	// Idempotent grant: 200 with no new audit entry.
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"is_admin": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent grant: %d", rec.Code)
	}
	if len(userUpdatedDetails(e)) != 1 {
		t.Fatalf("idempotent grant wrote an audit entry")
	}

	// Revoke the second administrator while the first remains: allowed.
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"is_admin": false}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke with another admin: %d %s", rec.Code, rec.Body.String())
	}
	u, _ = e.Store.GetUser(context.Background(), target.ID)
	if u.IsAdmin {
		t.Fatalf("revocation not stored")
	}

	// The last enabled administrator cannot be revoked, disabled, or both.
	for _, body := range []map[string]any{
		{"is_admin": false},
		{"enabled": false},
		{"enabled": false, "is_admin": false},
	} {
		before := len(userUpdatedDetails(e))
		rec = e.do("PUT", "/api/v1/users/"+itoa(admin.ID), body, admin)
		if rec.Code != http.StatusConflict {
			t.Fatalf("last-admin guard %v: %d %s, want 409", body, rec.Code, rec.Body.String())
		}
		if len(userUpdatedDetails(e)) != before {
			t.Fatalf("last-admin guard %v wrote an audit entry", body)
		}
		u, _ = e.Store.GetUser(context.Background(), admin.ID)
		if !u.Enabled || !u.IsAdmin {
			t.Fatalf("last-admin guard %v changed the row: %+v", body, u)
		}
	}

	// Self-revoke is allowed once another enabled administrator exists.
	second := e.mustUser("second@example.org")
	rec = e.do("PUT", "/api/v1/users/"+itoa(second.ID), map[string]any{"is_admin": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant second: %d", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/users/"+itoa(admin.ID), map[string]any{"is_admin": false}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("self-revoke with another admin: %d %s", rec.Code, rec.Body.String())
	}

	// A disabled administrator can be revoked — the enabled set is untouched.
	disabled := &db.User{Email: "gone@example.org", DisplayName: "Gone", Enabled: false, IsAdmin: true}
	id, err := e.Store.CreateUser(context.Background(), disabled)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	disabled.ID = id
	second, _ = e.Store.GetUser(context.Background(), second.ID) // the grant lives on the row
	rec = e.do("PUT", "/api/v1/users/"+itoa(id), map[string]any{"is_admin": false}, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke disabled admin: %d %s", rec.Code, rec.Body.String())
	}

	// A grant to a disabled account is allowed (it joins the admin set only
	// when re-enabled).
	rec = e.do("PUT", "/api/v1/users/"+itoa(id), map[string]any{"is_admin": true}, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant to disabled account: %d %s", rec.Code, rec.Body.String())
	}

	// Non-admin actors are rejected (403).
	rec = e.do("PUT", "/api/v1/users/"+itoa(second.ID), map[string]any{"is_admin": true},
		e.mustUser("plain@example.org"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", rec.Code)
	}
}

// TestLastAdminRevocationRace drives concurrent revocations of the two (and
// last two) enabled administrators through one handler; whatever interleaving
// SQLite produces, at least one enabled administrator must remain
// (REQ-AUTH-068).
func TestLastAdminRevocationRace(t *testing.T) {
	e := newEnv(t)
	a := e.mustAdmin("a@example.org")
	b := e.mustAdmin("b@example.org")

	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, actor := range []*db.User{a, b} {
		wg.Add(1)
		go func(actor *db.User) {
			defer wg.Done()
			rec := e.do("PUT", "/api/v1/users/"+itoa(actor.ID),
				map[string]any{"is_admin": false}, actor)
			codes <- rec.Code
		}(actor)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK && code != http.StatusConflict &&
			code != http.StatusInternalServerError {
			t.Fatalf("race response %d, want 200/409/500", code)
		}
	}
	n, err := e.Store.CountEnabledAdmins(context.Background())
	if err != nil {
		t.Fatalf("CountEnabledAdmins: %v", err)
	}
	if n < 1 {
		t.Fatalf("enabled administrators = %d after concurrent revocations, want >= 1", n)
	}
}
