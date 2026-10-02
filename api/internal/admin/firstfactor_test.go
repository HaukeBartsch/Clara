package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestFirstFactorCompletesAChallengedLogin walks the local two-factor login as
// the web layer drives it: verify-password returns a handle, the challenge's
// second call carries the handle and the code and no password (REQ-API-131).
func TestFirstFactorCompletesAChallengedLogin(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	enrollTOTPComplete(t, e, u)

	rec := e.do("POST", "/api/v1/auth/verify-password",
		map[string]any{"email": "user@example.org", "password": "hunter2"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify-password: %d %s", rec.Code, rec.Body.String())
	}
	var verify struct {
		Status      string `json:"status"`
		FirstFactor string `json:"first_factor"`
		ExpiresIn   int    `json:"expires_in"`
	}
	e.decode(rec, &verify)
	if verify.Status != "ok" || verify.FirstFactor == "" {
		t.Fatalf("verify-password = %+v, want ok with a first-factor handle", verify)
	}
	if verify.ExpiresIn != 300 {
		t.Errorf("expires_in = %d, want 300 (the tfa_pending lifetime)", verify.ExpiresIn)
	}

	// Handle alone is not a login: the gate still closes behind it.
	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "first_factor": verify.FirstFactor,
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handle without a code: %d %s, want 401 mfa_required", rec.Code, rec.Body.String())
	}
	var challenge map[string]any
	e.decode(rec, &challenge)
	if challenge["error"] != "mfa_required" {
		t.Fatalf("challenge = %v, want mfa_required", challenge)
	}

	// Handle + code completes it, with no password anywhere in the call.
	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local",
		"first_factor": verify.FirstFactor, "mfa_code": nextStepCode(t, secretOf(t, e, u.ID)),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("handle + code: %d %s", rec.Code, rec.Body.String())
	}
	if details := loginSuccessDetails(t, e); details["mfa"] != "totp" {
		t.Errorf("login_success details = %v, want mfa=totp", details)
	}

	// The handle is a credential substitute: it belongs to neither the audit
	// trail nor any log line (REQ-AUTH-036 by analogy with the password).
	if auditContains(t, e, verify.FirstFactor) {
		t.Errorf("an audit detail carries the first-factor handle")
	}
}

// TestFirstFactorCannotReplaceAPasswordForAnUnguardedAccount is the property the
// issuance rule exists for: a handle never finishes a login that has no second
// factor in front of it, so it widens nothing beyond the challenge it serves.
func TestFirstFactorCannotReplaceAPasswordForAnUnguardedAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	enrollTOTPComplete(t, e, u)

	rec := e.do("POST", "/api/v1/auth/verify-password",
		map[string]any{"email": "user@example.org", "password": "hunter2"}, nil)
	var verify struct {
		FirstFactor string `json:"first_factor"`
	}
	e.decode(rec, &verify)
	if verify.FirstFactor == "" {
		t.Fatal("no handle issued for a guarded account")
	}

	// An administrator resets the factor (REQ-AUTH-059): the account is unguarded
	// again and the handle it left behind must stop working.
	rec = e.do("POST", "/api/v1/users/"+itoa(u.ID)+"/tfa/reset", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("tfa/reset: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "user@example.org", "source": "local", "first_factor": verify.FirstFactor,
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handle for an unguarded account: %d %s, want 401", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	e.decode(rec, &body)
	if body.Error != "first_factor_expired" {
		t.Errorf("error = %q, want first_factor_expired", body.Error)
	}

	// An unguarded account is never issued one in the first place. (A fresh
	// struct: decoding into the previous one would keep its stale handle.)
	rec = e.do("POST", "/api/v1/auth/verify-password",
		map[string]any{"email": "user@example.org", "password": "hunter2"}, nil)
	var afterReset struct {
		FirstFactor string `json:"first_factor"`
	}
	e.decode(rec, &afterReset)
	if afterReset.FirstFactor != "" {
		t.Errorf("a handle was issued for an account with no second factor")
	}

	// And the lockout budget is untouched: a stale proof is not a credential
	// attempt (REQ-AUTH-035). Five of them must not lock the address out.
	for i := 0; i < 5; i++ {
		rec = e.do("POST", "/api/v1/auth/login", map[string]any{
			"email": "user@example.org", "source": "local", "first_factor": verify.FirstFactor + "x",
		}, nil)
	}
	if rec.Code == http.StatusTooManyRequests {
		t.Errorf("stale handles spent the lockout budget: %d", rec.Code)
	}
}

// TestFirstFactorSignatureAndExpiryAreEnforced covers the two properties that
// make the handle a statement rather than a guess: it is this API's own, and it
// stops being true on its own.
func TestFirstFactorSignatureAndExpiryAreEnforced(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")

	issued := e.Handler.issueFirstFactor(u, time.Now())
	if !e.Handler.acceptFirstFactor(issued, u, time.Now()) {
		t.Fatal("a freshly issued handle was not accepted")
	}

	// A changed signature, payload or encoding all fail the same way.
	if e.Handler.acceptFirstFactor(tamper(issued), u, time.Now()) {
		t.Error("a tampered handle was accepted")
	}
	if e.Handler.acceptFirstFactor(strings.Split(issued, ".")[0], u, time.Now()) {
		t.Error("a handle with its signature removed was accepted")
	}
	if e.Handler.acceptFirstFactor("not-a-handle", u, time.Now()) {
		t.Error("a malformed handle was accepted")
	}

	// Bound to the account it names.
	other := e.mustUser("other@example.org")
	if e.Handler.acceptFirstFactor(issued, other, time.Now()) {
		t.Error("a handle was accepted for another account")
	}

	// Expired: accepted up to its lifetime, refused after it. The window is the
	// tfa_pending lifetime, so nothing outlives the session that holds it.
	if !e.Handler.acceptFirstFactor(issued, u, time.Now().Add(firstFactorTTL-time.Minute)) {
		t.Error("a handle inside its lifetime was refused")
	}
	if e.Handler.acceptFirstFactor(issued, u, time.Now().Add(firstFactorTTL+time.Minute)) {
		t.Error("an expired handle was accepted")
	}
}

// secretOf reads back the TOTP secret of an enrolled account — test access to
// the one value no endpoint returns after enrollment (REQ-AUTH-056).
func secretOf(t *testing.T, e *env, userID int64) string {
	t.Helper()
	var secret string
	if err := e.Store.DB.QueryRow(`SELECT totp_secret FROM user_two_factor WHERE user_id = ?`, userID).Scan(&secret); err != nil {
		t.Fatalf("read TOTP secret: %v", err)
	}

	return secret
}

// auditContains reports whether any audit row's details carry needle.
func auditContains(t *testing.T, e *env, needle string) bool {
	t.Helper()
	var count int
	// The pattern is built here: `'%' || ? || '%'` is a logical OR on MariaDB.
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE details LIKE ?`, "%"+needle+"%").Scan(&count); err != nil {
		t.Fatalf("query audit details: %v", err)
	}

	return count > 0
}

// tamper flips the last character of a handle's signature.
func tamper(handle string) string {
	if handle == "" {
		return "x"
	}
	last := handle[len(handle)-1]
	if last == 'A' {
		return handle[:len(handle)-1] + "B"
	}

	return handle[:len(handle)-1] + "A"
}
