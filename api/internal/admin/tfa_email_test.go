package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// The email second factor (REQ-AUTH-057): enrollment by a delivered code, the
// login challenge that sends one, single use, expiry, the per-account send cap
// and a failing relay. Mail goes through the SendMail hook, so nothing is sent.

var emailCodeRE = regexp.MustCompile(`login code is (\d{6})\.`)

// mailbox records every message the handler hands to the relay.
type mailbox struct {
	t    *testing.T
	sent []string // "to\nsubject\nbody"
}

func (e *env) emailFactorMailbox() *mailbox {
	e.t.Helper()
	e.Cfg.TotpIssuer = "CLARA"
	e.Cfg.TFAEmailCodeTTL = 600
	m := &mailbox{t: e.t}
	e.Handler.SendMail = func(to, subject, body string) error {
		m.sent = append(m.sent, to+"\n"+subject+"\n"+body)
		return nil
	}
	return m
}

// lastCode returns the code of the newest message and checks it went to want.
func (m *mailbox) lastCode(want string) string {
	m.t.Helper()
	if len(m.sent) == 0 {
		m.t.Fatalf("no code was mailed")
	}
	msg := m.sent[len(m.sent)-1]
	if to := strings.SplitN(msg, "\n", 2)[0]; to != want {
		m.t.Fatalf("code mailed to %q, want %q", to, want)
	}
	if !strings.Contains(msg, "Your CLARA login code") {
		m.t.Errorf("subject does not name the issuer: %q", msg)
	}
	c := emailCodeRE.FindStringSubmatch(msg)
	if c == nil {
		m.t.Fatalf("mailed message carries no code: %q", msg)
	}
	return c[1]
}

func (e *env) startEmailFactor(u *db.User) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/users/me/tfa/email/start", nil, u)
}

func (e *env) confirmEmailFactor(u *db.User, code string) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/users/me/tfa/email/confirm", map[string]any{"code": code}, u)
}

func (e *env) tfaMethod(u *db.User) string {
	e.t.Helper()
	var status struct {
		Method string `json:"method"`
	}
	e.decode(e.do("GET", "/api/v1/users/me/tfa", nil, u), &status)
	return status.Method
}

func expectAPIError(t *testing.T, e *env, rec *httptest.ResponseRecorder, code int, errName, what string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("%s = %d %s, want %d %s", what, rec.Code, rec.Body.String(), code, errName)
	}
	if errName == "" {
		return
	}
	var body ErrorBody
	e.decode(rec, &body)
	if body.Error != errName {
		t.Errorf("%s error = %q, want %q", what, body.Error, errName)
	}
}

// TestTFAEmailEnrollAndLogin walks the whole email factor: start, a wrong code,
// the right code, replay, the login challenge and a recovery code.
func TestTFAEmailEnrollAndLogin(t *testing.T) {
	e := newEnv(t)
	box := e.emailFactorMailbox()
	u := mustLocalUser(t, e, "user@example.org", "hunter2")

	rec := e.startEmailFactor(u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sent"`) {
		t.Fatalf("start = %d %s, want 200 sent", rec.Code, rec.Body.String())
	}
	code := box.lastCode("user@example.org")

	// Only the hash is stored, and nothing is active before the code returns.
	tfa, err := e.Store.GetTFA(context.Background(), u.ID)
	if err != nil || tfa == nil || !tfa.EmailCodeHash.Valid {
		t.Fatalf("pending enrollment row = %+v, %v", tfa, err)
	}
	if strings.Contains(tfa.EmailCodeHash.String, code) {
		t.Errorf("the stored code hash contains the plaintext code")
	}
	if m := e.tfaMethod(u); m != "off" {
		t.Errorf("method before confirmation = %q, want off", m)
	}

	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	expectAPIError(t, e, e.confirmEmailFactor(u, wrong), http.StatusUnauthorized, "bad_mfa_code", "confirm with a wrong code")
	if m := e.tfaMethod(u); m != "off" {
		t.Errorf("method after a wrong code = %q, want off", m)
	}

	// A wrong attempt does not spend the pending code.
	rec = e.confirmEmailFactor(u, code)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body.String())
	}
	var conf struct {
		Method        string   `json:"method"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	e.decode(rec, &conf)
	if conf.Method != "email" || len(conf.RecoveryCodes) != 10 {
		t.Fatalf("confirm response = %+v, want email with 10 recovery codes", conf)
	}
	if m := e.tfaMethod(u); m != "email" {
		t.Errorf("method after confirmation = %q, want email", m)
	}
	var enrolled []string
	rows, err := e.Store.DB.Query(`SELECT COALESCE(details, '') FROM audit_events WHERE event_type = ?`, audit.TFAEnrolled)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		enrolled = append(enrolled, d)
	}
	rows.Close()
	if len(enrolled) != 1 || !strings.Contains(enrolled[0], `"method":"email"`) {
		t.Errorf("tfa_enrolled audit = %v, want one entry with method email (REQ-AUD-028)", enrolled)
	}

	// Replaying the enrollment code, or starting again, finds nothing pending.
	expectAPIError(t, e, e.confirmEmailFactor(u, code), http.StatusConflict, "", "replayed confirmation")
	expectAPIError(t, e, e.startEmailFactor(u), http.StatusConflict, "", "start while enabled")

	// Login: the first factor alone answers mfa_required and mails a fresh code.
	sentBefore := len(box.sent)
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), `"mfa_required"`) || !strings.Contains(rec.Body.String(), `"email"`) {
		t.Fatalf("login without code = %d %s, want 401 mfa_required/email", rec.Code, rec.Body.String())
	}
	if len(box.sent) != sentBefore+1 {
		t.Fatalf("login challenge sent %d messages, want 1", len(box.sent)-sentBefore)
	}
	loginCode := box.lastCode("user@example.org")

	expectAPIError(t, e, e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", code), nil),
		http.StatusUnauthorized, "bad_mfa_code", "login with the spent enrollment code")
	rec = e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", loginCode), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with the mailed code = %d %s", rec.Code, rec.Body.String())
	}
	expectAPIError(t, e, e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", loginCode), nil),
		http.StatusUnauthorized, "bad_mfa_code", "replayed login code")

	// A recovery code stands in for the mailed one (REQ-AUTH-059).
	if rec := e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", conf.RecoveryCodes[0]), nil); rec.Code != http.StatusOK {
		t.Fatalf("login with a recovery code = %d %s", rec.Code, rec.Body.String())
	}

	// The plaintext codes never reach an audit detail.
	rows, err = e.Store.DB.Query(`SELECT COALESCE(details, '') FROM audit_events`)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		if strings.Contains(d, code) || strings.Contains(d, loginCode) {
			t.Errorf("an audit detail carries a mailed code: %s", d)
		}
	}
}

// TestTFAEmailConfirmNeedsPendingCode: confirming without a started enrollment
// is a conflict, not a code check.
func TestTFAEmailConfirmNeedsPendingCode(t *testing.T) {
	e := newEnv(t)
	e.emailFactorMailbox()
	u := e.mustUser("user@example.org")
	expectAPIError(t, e, e.confirmEmailFactor(u, "123456"), http.StatusConflict, "", "confirm without start")
	if m := e.tfaMethod(u); m != "off" {
		t.Errorf("method = %q, want off", m)
	}
}

// TestTFAEmailCodeExpires: a code past TFA_EMAIL_CODE_TTL is refused like a
// wrong one, and the factor stays off.
func TestTFAEmailCodeExpires(t *testing.T) {
	e := newEnv(t)
	box := e.emailFactorMailbox()
	e.Cfg.TFAEmailCodeTTL = -60 // issued already expired
	u := e.mustUser("user@example.org")
	if rec := e.startEmailFactor(u); rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	expectAPIError(t, e, e.confirmEmailFactor(u, box.lastCode("user@example.org")),
		http.StatusUnauthorized, "bad_mfa_code", "confirm with an expired code")
	if m := e.tfaMethod(u); m != "off" {
		t.Errorf("method = %q, want off", m)
	}
}

// TestTFAEmailNewestCodeWins: a new send replaces the pending code, so an
// earlier message is worthless.
func TestTFAEmailNewestCodeWins(t *testing.T) {
	e := newEnv(t)
	box := e.emailFactorMailbox()
	u := e.mustUser("user@example.org")
	e.startEmailFactor(u)
	first := box.lastCode("user@example.org")
	var second string
	for second = first; second == first; { // a repeated 6-digit draw would prove nothing
		if rec := e.startEmailFactor(u); rec.Code != http.StatusOK {
			t.Fatalf("restart = %d %s", rec.Code, rec.Body.String())
		}
		second = box.lastCode("user@example.org")
	}
	expectAPIError(t, e, e.confirmEmailFactor(u, first), http.StatusUnauthorized, "bad_mfa_code", "confirm with the replaced code")
	if rec := e.confirmEmailFactor(u, second); rec.Code != http.StatusOK {
		t.Fatalf("confirm with the newest code = %d %s", rec.Code, rec.Body.String())
	}
}

// TestTFAEmailSendCap: at most three codes per account in the window; the
// fourth request is 429 and sends nothing. The cap is per account, and the
// login challenge honours it too.
func TestTFAEmailSendCap(t *testing.T) {
	e := newEnv(t)
	box := e.emailFactorMailbox()
	u := mustLocalUser(t, e, "user@example.org", "hunter2")
	other := e.mustUser("other@example.org")

	for i := 0; i < sendsPerWindow; i++ {
		if rec := e.startEmailFactor(u); rec.Code != http.StatusOK {
			t.Fatalf("send %d = %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	expectAPIError(t, e, e.startEmailFactor(u), http.StatusTooManyRequests, "code_send_limited", "send over the cap")
	if len(box.sent) != sendsPerWindow {
		t.Fatalf("%d messages sent, want %d", len(box.sent), sendsPerWindow)
	}
	if rec := e.startEmailFactor(other); rec.Code != http.StatusOK {
		t.Fatalf("another account's send = %d %s, want 200 (the cap is per account)", rec.Code, rec.Body.String())
	}

	// The last code still enrolls; then a login challenge inside the same window
	// still answers mfa_required but mails nothing.
	if rec := e.confirmEmailFactor(u, codeTo(t, box, "user@example.org")); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body.String())
	}
	sent := len(box.sent)
	rec := e.do("POST", "/api/v1/auth/login", loginBody("user@example.org", "hunter2", ""), nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"mfa_required"`) {
		t.Fatalf("capped login challenge = %d %s, want 401 mfa_required", rec.Code, rec.Body.String())
	}
	if len(box.sent) != sent {
		t.Errorf("the capped login challenge sent %d messages, want none", len(box.sent)-sent)
	}
}

// codeTo returns the newest code mailed to one address.
func codeTo(t *testing.T, box *mailbox, to string) string {
	t.Helper()
	for i := len(box.sent) - 1; i >= 0; i-- {
		if strings.HasPrefix(box.sent[i], to+"\n") {
			if c := emailCodeRE.FindStringSubmatch(box.sent[i]); c != nil {
				return c[1]
			}
		}
	}
	t.Fatalf("no code was mailed to %s", to)
	return ""
}

// TestTFAEmailRelayFailure: a relay that refuses the message is a 502 the user
// can act on, and no enrollment is left half-done.
func TestTFAEmailRelayFailure(t *testing.T) {
	e := newEnv(t)
	e.emailFactorMailbox()
	e.Handler.SendMail = func(to, subject, body string) error { return errors.New("relay refused") }
	u := e.mustUser("user@example.org")
	expectAPIError(t, e, e.startEmailFactor(u), http.StatusBadGateway, "smtp_send_failed", "start with a failing relay")
	if m := e.tfaMethod(u); m != "off" {
		t.Errorf("method = %q, want off", m)
	}
}
