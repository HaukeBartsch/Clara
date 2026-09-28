package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"csms/api/internal/db"
	"csms/api/internal/totp"
)

// enrollTOTPComplete runs the self-service wizard for u and returns the
// shared secret plus the one-time recovery codes (REQ-AUTH-054/059).
func enrollTOTPComplete(t *testing.T, e *env, u *db.User) (string, []string) {
	t.Helper()
	rec := e.do("POST", "/api/v1/users/me/tfa/totp/enroll", nil, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body.String())
	}
	var enr struct {
		Secret     string `json:"secret"`
		OTPAuthURI string `json:"otpauth_uri"`
	}
	e.decode(rec, &enr)
	if enr.Secret == "" || enr.OTPAuthURI == "" {
		t.Fatalf("enroll response missing secret/uri: %+v", enr)
	}
	code, err := totp.Code(enr.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do("POST", "/api/v1/users/me/tfa/totp/confirm", map[string]any{"code": code}, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	var conf struct {
		Method        string   `json:"method"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	e.decode(rec, &conf)
	if conf.Method != "totp" || len(conf.RecoveryCodes) != 10 {
		t.Fatalf("confirm response = %+v", conf)
	}
	return enr.Secret, conf.RecoveryCodes
}

func loginBody(email, password, mfa string) map[string]any {
	b := map[string]any{"email": email, "source": "local", "password": password}
	if mfa != "" {
		b["mfa_code"] = mfa
	}
	return b
}

// TestTFAChallengeFlowEnrollAndLogin walks the whole Sequence G story:
// enrollment, challenge without a code, wrong code, right code, replay.
func TestTFAChallengeFlowEnrollAndLogin(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")

	// Before enrollment: login succeeds and reports tfa_method off (REQ-API-116).
	rec := e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-enroll login: %d %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.TFAMethod != "off" {
		t.Fatalf("tfa_method = %q, want off", obj.TFAMethod)
	}

	// GET status before enrollment.
	rec = e.do("GET", "/api/v1/users/me/tfa", nil, u)
	var status struct {
		Method    string `json:"method"`
		Remaining int    `json:"recovery_codes_remaining"`
	}
	e.decode(rec, &status)
	if status.Method != "off" || status.Remaining != 0 {
		t.Fatalf("GET me/tfa = %+v, want off/0", status)
	}

	secret, recovery := enrollTOTPComplete(t, e, u)
	if !hasType(e.auditTypes(), "tfa_enrolled") {
		t.Errorf("audit = %v, want tfa_enrolled (REQ-AUD-028)", e.auditTypes())
	}

	rec = e.do("GET", "/api/v1/users/me/tfa", nil, u)
	e.decode(rec, &status)
	if status.Method != "totp" || status.Remaining != 10 {
		t.Fatalf("GET me/tfa after enroll = %+v, want totp/10", status)
	}

	// Login without the second factor: 401 mfa_required naming the method.
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge status = %d, want 401", rec.Code)
	}
	var challenge map[string]any
	e.decode(rec, &challenge)
	if challenge["error"] != "mfa_required" || challenge["method"] != "totp" {
		t.Fatalf("challenge body = %v, want mfa_required/totp (REQ-API-114)", challenge)
	}

	// Wrong code: bad_mfa_code plus a login_failure audit entry.
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", "000000"), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d, want 401", rec.Code)
	}
	var errBody ErrorBody
	e.decode(rec, &errBody)
	if errBody.Error != "bad_mfa_code" {
		t.Errorf("error = %q, want bad_mfa_code", errBody.Error)
	}

	// Right code (next step — the confirm already spent the current one):
	// 200 with tfa_method and an mfa detail on login_success.
	code, err := totp.Code(secret, time.Now().Add(31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", code), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code login: %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &obj)
	if obj.TFAMethod != "totp" {
		t.Errorf("tfa_method = %q, want totp", obj.TFAMethod)
	}
	if details := loginSuccessDetails(t, e); details["mfa"] != "totp" {
		t.Errorf("login_success details = %v, want mfa=totp", details)
	}

	// Replay of the same code inside its window: rejected (watermark).
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", code), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code accepted: %d", rec.Code)
	}

	// A recovery code verifies in place of the app (REQ-AUTH-059)…
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", recovery[0]), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery login: %d %s", rec.Code, rec.Body.String())
	}
	if details := loginSuccessDetails(t, e); details["mfa"] != "recovery" {
		t.Errorf("login_success details = %v, want mfa=recovery", details)
	}
	// …exactly once.
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", recovery[0]), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed recovery code accepted")
	}
}

// TestTFADisableRequiresCode covers REQ-AUTH-056: turning the factor off
// needs a valid current or recovery code.
func TestTFADisableRequiresCode(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	_, recovery := enrollTOTPComplete(t, e, u)

	rec := e.do("POST", "/api/v1/users/me/tfa/disable", map[string]any{"code": "000000"}, u)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong code: %d, want 401", rec.Code)
	}

	rec = e.do("POST", "/api/v1/users/me/tfa/disable", map[string]any{"code": recovery[1]}, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable with recovery code: %d %s", rec.Code, rec.Body.String())
	}
	if !hasType(e.auditTypes(), "tfa_disabled") {
		t.Errorf("audit = %v, want tfa_disabled", e.auditTypes())
	}

	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after disable: %d %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.TFAMethod != "off" {
		t.Errorf("tfa_method = %q, want off", obj.TFAMethod)
	}
}

// TestTFAEnrollTwiceIsConflict: an active method must be disabled first.
func TestTFAEnrollTwiceIsConflict(t *testing.T) {
	e := newEnv(t)
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	enrollTOTPComplete(t, e, u)

	rec := e.do("POST", "/api/v1/users/me/tfa/totp/enroll", nil, u)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second enroll: %d, want 409", rec.Code)
	}
}

// TestTFAEnrollmentMandated covers REQ-AUTH-055: with AUTH_REQUIRE_2FA an
// account without a second factor cannot log in.
func TestTFAEnrollmentMandated(t *testing.T) {
	e := newEnv(t)
	mustLocalUser(t, e, "user@example.org", "hunter2")
	e.Cfg.AuthRequire2FA = true

	rec := e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mandated login: %d, want 401", rec.Code)
	}
	var errBody ErrorBody
	e.decode(rec, &errBody)
	if errBody.Error != "tfa_enrollment_required" {
		t.Errorf("error = %q, want tfa_enrollment_required", errBody.Error)
	}
}

// TestTFAOAuth2Exempt: the mandate and the challenge apply to local and LDAP
// form logins only (GD-21).
func TestTFAOAuth2Exempt(t *testing.T) {
	e := newEnv(t)
	e.mustUser("idp@example.org")
	e.Cfg.AuthRequire2FA = false

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"email": "idp@example.org", "source": "oauth2", "provider": "1",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("oauth2 login: %d %s", rec.Code, rec.Body.String())
	}
}

// TestTFAAdminReset covers REQ-API-115: is_admin clears any account's factor.
func TestTFAAdminReset(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	enrollTOTPComplete(t, e, u)

	// A non-admin cannot reset.
	rec := e.do("POST", "/api/v1/users/1/tfa/reset", nil, u)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reset: %d, want 403", rec.Code)
	}

	rec = e.do("POST", "/api/v1/users/"+strconv.FormatInt(u.ID, 10)+"/tfa/reset", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin reset: %d %s", rec.Code, rec.Body.String())
	}
	if !hasType(e.auditTypes(), "tfa_reset") {
		t.Errorf("audit = %v, want tfa_reset", e.auditTypes())
	}

	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after admin reset: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown target: 404.
	rec = e.do("POST", "/api/v1/users/99999/tfa/reset", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reset unknown user: %d, want 404", rec.Code)
	}
}

// TestTFAEmailStartWithoutSMTP: the enrollment step fails with a clear 409
// when no relay is configured (REQ-AUTH-057).
func TestTFAEmailStartWithoutSMTP(t *testing.T) {
	e := newEnv(t)
	u := e.mustUser("user@example.org")

	rec := e.do("POST", "/api/v1/users/me/tfa/email/start", nil, u)
	if rec.Code != http.StatusConflict {
		t.Fatalf("email start without SMTP: %d %s", rec.Code, rec.Body.String())
	}
	var errBody ErrorBody
	e.decode(rec, &errBody)
	if errBody.Error != "smtp_not_configured" {
		t.Errorf("error = %q, want smtp_not_configured", errBody.Error)
	}
}

// TestTFAUsersListingCarriesMethod: the admin listing stamps tfa_method in
// one batched lookup (REQ-API-116).
func TestTFAUsersListingCarriesMethod(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	enrollTOTPComplete(t, e, u)

	rec := e.do("GET", "/api/v1/users", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var users []UserObject
	e.decode(rec, &users)
	methods := map[int64]string{}
	for _, o := range users {
		methods[o.ID] = o.TFAMethod
	}
	if methods[u.ID] != "totp" {
		t.Errorf("tfa_method for enrolled user = %q, want totp", methods[u.ID])
	}
	if methods[admin.ID] != "off" {
		t.Errorf("tfa_method for admin = %q, want off", methods[admin.ID])
	}
}

// loginSuccessDetails returns the details of the most recent login_success.
func loginSuccessDetails(t *testing.T, e *env) map[string]any {
	t.Helper()
	var raw []byte
	err := e.Store.DB.QueryRow(
		`SELECT details FROM audit_events WHERE event_type = 'login_success' ORDER BY id DESC LIMIT 1`,
	).Scan(&raw)
	if err != nil {
		t.Fatalf("read login_success details: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decode login_success details: %v", err)
	}
	return d
}
