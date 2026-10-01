package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// The named-source credential race finalizes failed attempts through the login
// endpoint carrying their per-source outcomes in `attempts`
// (Authentication_Authorization_Design.md §2.9). For source "ldap" the API trusts
// PHP's word completely — step 0 verifies a hash only for source "local" — so an
// attempts-carrying call that fell through would authenticate a first factor
// nobody verified, or open a two-factor challenge on a submission whose first
// factor failed. REQ-API-135 is what makes that impossible.

// TestLoginAttemptsNeverAuthenticate: a failed race answers 401 whatever the
// account's state — no user object, and no second-factor challenge either
// (REQ-API-135, REQ-AUTH-055).
func TestLoginAttemptsNeverAuthenticate(t *testing.T) {
	e := newEnv(t)

	mustLocalUser(t, e, "plain@example.org", "hunter2")
	guarded := mustLocalUser(t, e, "guarded@example.org", "hunter2")
	enrollTOTPComplete(t, e, guarded)

	cases := []struct {
		name  string
		email string
	}{
		{"method off: not a session", "plain@example.org"},
		{"method totp: not a challenge", "guarded@example.org"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do("POST", "/api/v1/auth/login", map[string]any{
				"email": tc.email, "source": "ldap", "provider": "ldap-1",
				"source_name": "Hospital 2",
				"attempts":    map[string]string{"ldap-1": "bad_credentials", "ldap-2": "unreachable"},
			}, nil)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 — body %s", rec.Code, rec.Body.String())
			}

			var fields map[string]any
			e.decode(rec, &fields)
			if _, ok := fields["id"]; ok {
				t.Errorf("response carries a user object: %v", fields)
			}
			if code, _ := fields["error"].(string); code == "mfa_required" || code == "tfa_enrollment_required" {
				t.Errorf("a failed first factor opened the second factor: error = %q", code)
			}
		})
	}
}

// TestLoginAttemptsWriteNoLoginSideEffects: the race's failure record touches no
// row — auth_source and last_login_at stay as they were, and no login_success is
// written (REQ-AUTH-065 keeps login the only place side effects happen).
func TestLoginAttemptsWriteNoLoginSideEffects(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "ldap", "provider": "ldap-1",
		"attempts": map[string]string{"ldap-1": "bad_credentials"},
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	got, err := e.Store.GetUserByEmail(context.Background(), u.Email)
	if err != nil || got == nil {
		t.Fatalf("re-read: %v", err)
	}
	if got.LastLoginAt.Valid {
		t.Errorf("last_login_at = %v, want untouched", got.LastLoginAt.String)
	}
	if got.AuthSource != u.AuthSource {
		t.Errorf("auth_source = %q, want the stored %q", got.AuthSource, u.AuthSource)
	}
	if hasType(e.auditTypes(), "login_success") {
		t.Errorf("unexpected login_success for a failed race")
	}
}

// TestLoginAttemptsReasonMapping: one login_failure per submission, with the
// reason the outcome of the race decides — credential failure, outage, or the
// account's own state, which outranks both (REQ-AUTH-065, DEV-AUD-5).
func TestLoginAttemptsReasonMapping(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	disabled, err := e.Store.GetUserByEmail(context.Background(), "user@example.org")
	if err != nil || disabled == nil {
		t.Fatalf("re-read: %v", err)
	}

	t.Run("mixed outcomes settle as bad_credentials", func(t *testing.T) {
		rec := e.do("POST", "/api/v1/auth/login", map[string]any{
			"email": "user@example.org", "source": "local",
			"attempts": map[string]string{"local": "bad_password", "ldap-2": "unreachable"},
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		var eb ErrorBody
		e.decode(rec, &eb)
		if eb.Error != "bad_password" {
			t.Errorf("error = %q, want bad_password", eb.Error)
		}
		if n := countType(e.auditTypes(), "login_failure"); n != 1 {
			t.Errorf("%d login_failure rows, want exactly one per submission", n)
		}
		if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "bad_credentials" {
			t.Errorf("reason = %v, want bad_credentials", got)
		}
	})

	t.Run("nothing reachable settles as provider_unavailable", func(t *testing.T) {
		rec := e.do("POST", "/api/v1/auth/login", map[string]any{
			"email": "user@example.org", "source": "ldap",
			"attempts": map[string]string{"ldap-1": "unreachable", "ldap-2": "unreachable"},
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		// The page distinguishes an outage from a wrong password, so the response
		// code says so too and not only the audit detail.
		var eb ErrorBody
		e.decode(rec, &eb)
		if eb.Error != "provider_unavailable" {
			t.Errorf("error = %q, want provider_unavailable", eb.Error)
		}
		if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "provider_unavailable" {
			t.Errorf("reason = %v, want provider_unavailable", got)
		}
	})

	t.Run("account state keeps its specific reason", func(t *testing.T) {
		if err := e.Store.SetUserEnabled(context.Background(), disabled.ID, false); err != nil {
			t.Fatalf("SetUserEnabled: %v", err)
		}
		rec := e.do("POST", "/api/v1/auth/login", map[string]any{
			"email": "user@example.org", "source": "ldap",
			"attempts": map[string]string{"ldap-1": "bad_credentials"},
		}, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var eb ErrorBody
		e.decode(rec, &eb)
		if eb.Error != "account_disabled" {
			t.Errorf("error = %q, want account_disabled", eb.Error)
		}
		if got := lastAuditDetails(t, e, "login_failure")["reason"]; got != "account_disabled" {
			t.Errorf("reason = %v, want account_disabled", got)
		}
	})
}

// TestLoginWithoutAttemptsStillAuthenticates: the guard keys on attempts alone —
// an ordinary login for the same account is unaffected (REQ-API-044).
func TestLoginWithoutAttemptsStillAuthenticates(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "password": "hunter2",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body %s", rec.Code, rec.Body.String())
	}
	var obj struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	if obj.ID == 0 || obj.Email != "user@example.org" {
		t.Errorf("user object = %+v, want the signed-in row", obj)
	}
}
