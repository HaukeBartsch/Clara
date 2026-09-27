package admin

import (
	"context"
	"net/http"
	"testing"
	"time"
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
		"valid_days": 90, "password": "s3cret",
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
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"password": "hunter2"}, admin)
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
	rec = e.do("PUT", "/api/v1/users/"+itoa(target.ID), map[string]any{"is_admin": true}, admin)
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
