package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// Sequence K (Authentication_Authorization_Design.md §2.13): the API halves of
// signing a user in to a registered client — code, redemption, check, end.

const (
	ssoTestSecret   = "fiona-client-secret-0123456789abcdef"
	ssoTestRedirect = "https://fiona.example.org/applications/User/login.php"
	ssoTestVerifier = "verifier-0123456789-abcdefghijklmnopqrstuvwxyz-ABCDEFGH"
	ssoTestSID      = "sid-0123456789abcdef0123456789abcdef"
)

func ssoChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ssoEnv is an env with FIONA registered as client 1.
func ssoEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.Cfg.SessionLifetime = 28800
	e.Cfg.SSOClients = []config.SSOClient{
		{N: 1, ID: "fiona", Name: "FIONA", Secret: ssoTestSecret, RedirectURI: ssoTestRedirect},
		{N: 2, ID: "other", Name: "Other", Secret: "other-secret", RedirectURI: "https://other.example.org/cb"},
	}
	return e
}

func ssoCodeBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"client_id": "fiona", "redirect_uri": ssoTestRedirect,
		"code_challenge": ssoChallenge(ssoTestVerifier), "code_challenge_method": "S256",
		"sid": ssoTestSID, "session_issued_at": time.Now().Add(-time.Minute).Unix(),
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// issueSSOCode asks for a code as u and returns it.
func issueSSOCode(t *testing.T, e *env, u *db.User) string {
	t.Helper()
	rec := e.do("POST", "/api/v1/sso/codes", ssoCodeBody(nil), u)
	if rec.Code != http.StatusOK {
		t.Fatalf("codes: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
	}
	e.decode(rec, &out)
	if out.Code == "" || out.ExpiresIn != 60 {
		t.Fatalf("codes answer = %+v", out)
	}
	return out.Code
}

func ssoTokenBody(code string, overrides map[string]any) map[string]any {
	body := map[string]any{
		"client_id": "fiona", "client_secret": ssoTestSecret, "code": code,
		"code_verifier": ssoTestVerifier, "redirect_uri": ssoTestRedirect,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// redeemSSOCode redeems code and returns the grant handle.
func redeemSSOCode(t *testing.T, e *env, code string) string {
	t.Helper()
	rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Grant string `json:"grant"`
	}
	e.decode(rec, &out)
	if out.Grant == "" {
		t.Fatalf("token answer has no grant: %s", rec.Body.String())
	}
	return out.Grant
}

type ssoCheckAnswer struct {
	Active    bool    `json:"active"`
	Reason    string  `json:"reason"`
	ExpiresIn int     `json:"expires_in"`
	User      ssoUser `json:"user"`
}

func checkSSOGrant(t *testing.T, e *env, grant string) ssoCheckAnswer {
	t.Helper()
	rec := e.do("POST", "/api/v1/sso/check", map[string]any{
		"client_id": "fiona", "client_secret": ssoTestSecret, "grant": grant,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rec.Code, rec.Body.String())
	}
	var out ssoCheckAnswer
	e.decode(rec, &out)
	return out
}

func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.Store.DB.Exec(query, args...); err != nil {
		e.t.Fatalf("exec %q: %v", query, err)
	}
}

// TestSSOFullCycle: code → redemption → check → CLARA sign-out → check
// (REQ-AUTH-092…094, REQ-API-182…185), with the audit trail of REQ-AUD-037.
func TestSSOFullCycle(t *testing.T) {
	e := ssoEnv(t)
	u := e.mustUser("ola@example.org")

	code := issueSSOCode(t, e, u)
	rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Grant     string  `json:"grant"`
		ExpiresIn int     `json:"expires_in"`
		User      ssoUser `json:"user"`
	}
	e.decode(rec, &tok)
	if tok.User.ID != u.ID || tok.User.Email != "ola@example.org" || tok.User.DisplayName != "User" {
		t.Errorf("user = %+v", tok.User)
	}
	// The grant lives as long as the CLARA session could: sign-in + SESSION_LIFETIME.
	if tok.ExpiresIn < 28800-120 || tok.ExpiresIn > 28800 {
		t.Errorf("expires_in = %d, want about 8 h minus the minute since sign-in", tok.ExpiresIn)
	}
	if strings.Contains(rec.Body.String(), code) {
		t.Error("the token answer repeats the code")
	}
	if d := lastAuditDetails(t, e, "sso_login"); d["client_id"] != "fiona" {
		t.Errorf("sso_login details = %v", d)
	}

	if got := checkSSOGrant(t, e, tok.Grant); !got.Active || got.User.Email != u.Email {
		t.Fatalf("check after redemption = %+v, want active", got)
	}

	rec = e.do("POST", "/api/v1/sso/end", map[string]any{"sid": ssoTestSID}, u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fiona"`) {
		t.Fatalf("end: %d %s", rec.Code, rec.Body.String())
	}
	if d := lastAuditDetails(t, e, "sso_logout"); d["clients"] == nil {
		t.Errorf("sso_logout details = %v", d)
	}
	if got := checkSSOGrant(t, e, tok.Grant); got.Active || got.Reason != "ended" {
		t.Errorf("check after sign-out = %+v, want ended", got)
	}

	// Ending again is harmless and writes nothing.
	before := len(e.auditTypes())
	rec = e.do("POST", "/api/v1/sso/end", map[string]any{"sid": ssoTestSID}, u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"clients":[]`) {
		t.Errorf("second end: %d %s", rec.Code, rec.Body.String())
	}
	if len(e.auditTypes()) != before {
		t.Error("an end that ended nothing was audited")
	}

	// No hash column holds a value the client or the browser saw.
	var stored string
	if err := e.Store.DB.QueryRow(`SELECT code_hash || grant_hash || sid_hash FROM sso_grants`).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	for _, secret := range []string{code, tok.Grant, ssoTestSID} {
		if strings.Contains(stored, secret) {
			t.Errorf("sso_grants stores %q in the clear", secret)
		}
	}
}

// TestSSOCodeRefusals: the code endpoint refuses what is not this client's
// registered address and a challenge or session it cannot bind to (REQ-API-182).
func TestSSOCodeRefusals(t *testing.T) {
	e := ssoEnv(t)
	u := e.mustUser("ola@example.org")

	cases := []struct {
		name      string
		overrides map[string]any
		status    int
		code      string
	}{
		{"unknown client", map[string]any{"client_id": "nobody"}, 400, "invalid_client"},
		{"other address", map[string]any{"redirect_uri": ssoTestRedirect + "/../evil"}, 400, "invalid_redirect_uri"},
		{"prefix of the address", map[string]any{"redirect_uri": "https://fiona.example.org/"}, 400, "invalid_redirect_uri"},
		{"plain challenge", map[string]any{"code_challenge_method": "plain"}, 400, "invalid_request"},
		{"short challenge", map[string]any{"code_challenge": "abc"}, 400, "invalid_request"},
		{"short sid", map[string]any{"sid": "x"}, 400, "invalid_request"},
		{"session past its lifetime", map[string]any{"session_issued_at": time.Now().Add(-9 * time.Hour).Unix()}, 400, "invalid_request"},
		{"session from the future", map[string]any{"session_issued_at": time.Now().Add(time.Hour).Unix()}, 400, "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do("POST", "/api/v1/sso/codes", ssoCodeBody(tc.overrides), u)
			if rec.Code != tc.status || decodeError(t, rec) != tc.code {
				t.Errorf("= %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}

	if rec := e.do("POST", "/api/v1/sso/codes", ssoCodeBody(nil), nil); rec.Code != http.StatusForbidden {
		t.Errorf("without an acting user = %d, want 403", rec.Code)
	}
}

// TestSSOTokenRefusals: each failed redemption is refused before anything is
// issued, ends the code so it cannot be tried again, and is audited (REQ-AUTH-093).
func TestSSOTokenRefusals(t *testing.T) {
	t.Run("wrong client secret", func(t *testing.T) {
		e := ssoEnv(t)
		code := issueSSOCode(t, e, e.mustUser("ola@example.org"))
		rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, map[string]any{"client_secret": "guess"}), nil)
		if rec.Code != http.StatusUnauthorized || decodeError(t, rec) != "invalid_client" {
			t.Errorf("= %d %s, want 401 invalid_client", rec.Code, rec.Body.String())
		}
		if d := lastAuditDetails(t, e, "sso_rejected"); d["reason"] != "invalid_client" {
			t.Errorf("sso_rejected = %v", d)
		}
		// The code was never examined, so the real client can still redeem it.
		redeemSSOCode(t, e, code)
	})

	t.Run("another client's secret", func(t *testing.T) {
		e := ssoEnv(t)
		code := issueSSOCode(t, e, e.mustUser("ola@example.org"))
		rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, map[string]any{
			"client_id": "other", "client_secret": "other-secret", "redirect_uri": "https://other.example.org/cb",
		}), nil)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec) != "invalid_grant" {
			t.Errorf("= %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
		}
		if rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, nil), nil); rec.Code != http.StatusBadRequest {
			t.Errorf("a code shown to another client stays usable: %d", rec.Code)
		}
	})

	refusals := []struct {
		name   string
		setup  func(e *env, u *db.User)
		body   map[string]any
		status int
		code   string
		reason string
	}{
		{"wrong verifier", nil, map[string]any{"code_verifier": strings.Repeat("v", 43)}, 400, "invalid_grant", "pkce_mismatch"},
		{"other redirect", nil, map[string]any{"redirect_uri": "https://fiona.example.org/other"}, 400, "invalid_grant", "redirect_uri_mismatch"},
		{"expired code", func(e *env, _ *db.User) {
			e.exec(`UPDATE sso_grants SET code_expires_at = '2000-01-01 00:00:00'`)
		}, nil, 400, "invalid_grant", "code_expired"},
		{"disabled account", func(e *env, u *db.User) {
			e.exec(`UPDATE users SET enabled = 0 WHERE id = ?`, u.ID)
		}, nil, 403, "account_disabled", "account_disabled"},
		{"sessions ended since", func(e *env, u *db.User) {
			e.exec(`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, u.ID)
		}, nil, 400, "invalid_grant", "session_revoked"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			e := ssoEnv(t)
			u := e.mustUser("ola@example.org")
			code := issueSSOCode(t, e, u)
			if tc.setup != nil {
				tc.setup(e, u)
			}
			rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, tc.body), nil)
			if rec.Code != tc.status || decodeError(t, rec) != tc.code {
				t.Fatalf("= %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
			if d := lastAuditDetails(t, e, "sso_rejected"); d["reason"] != tc.reason {
				t.Errorf("sso_rejected reason = %v, want %s", d["reason"], tc.reason)
			}
			// The refusal ended the code: the right request no longer works either.
			if rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, nil), nil); rec.Code == http.StatusOK {
				t.Error("a refused code was redeemed on the second try")
			}
		})
	}

	t.Run("unknown code", func(t *testing.T) {
		e := ssoEnv(t)
		rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody("not-a-code", nil), nil)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec) != "invalid_grant" {
			t.Errorf("= %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
		}
	})

	t.Run("a second redemption ends the first grant", func(t *testing.T) {
		e := ssoEnv(t)
		code := issueSSOCode(t, e, e.mustUser("ola@example.org"))
		grant := redeemSSOCode(t, e, code)
		rec := e.do("POST", "/api/v1/sso/token", ssoTokenBody(code, nil), nil)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec) != "invalid_grant" {
			t.Fatalf("second redemption = %d %s, want 400 invalid_grant", rec.Code, rec.Body.String())
		}
		if got := checkSSOGrant(t, e, grant); got.Active || got.Reason != "ended" {
			t.Errorf("first grant after a replayed code = %+v, want ended", got)
		}
	})
}

// TestSSOCheckStates: a grant stops standing when its lifetime runs out, the
// account stops being active, or every CLARA session of the account is ended
// (REQ-AUTH-094); a grant is only answered for its own client.
func TestSSOCheckStates(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *env, u *db.User)
		reason string
	}{
		{"expired", func(e *env, _ *db.User) {
			e.exec(`UPDATE sso_grants SET expires_at = '2000-01-01 00:00:00'`)
		}, "expired"},
		{"disabled", func(e *env, u *db.User) {
			e.exec(`UPDATE users SET enabled = 0 WHERE id = ?`, u.ID)
		}, "account_disabled"},
		{"expired account", func(e *env, u *db.User) {
			e.exec(`UPDATE users SET valid_until = '2000-01-01' WHERE id = ?`, u.ID)
		}, "account_expired"},
		{"sessions ended", func(e *env, u *db.User) {
			e.exec(`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, u.ID)
		}, "session_revoked"},
		{"account deleted", func(e *env, u *db.User) {
			e.exec(`DELETE FROM users WHERE id = ?`, u.ID)
		}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ssoEnv(t)
			u := e.mustUser("ola@example.org")
			grant := redeemSSOCode(t, e, issueSSOCode(t, e, u))
			tc.setup(e, u)
			if got := checkSSOGrant(t, e, grant); got.Active || got.Reason != tc.reason {
				t.Errorf("check = %+v, want inactive %s", got, tc.reason)
			}
		})
	}

	t.Run("other client and bad credentials", func(t *testing.T) {
		e := ssoEnv(t)
		grant := redeemSSOCode(t, e, issueSSOCode(t, e, e.mustUser("ola@example.org")))
		rec := e.do("POST", "/api/v1/sso/check", map[string]any{
			"client_id": "other", "client_secret": "other-secret", "grant": grant,
		}, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reason":"unknown"`) {
			t.Errorf("another client's check = %d %s, want unknown", rec.Code, rec.Body.String())
		}
		rec = e.do("POST", "/api/v1/sso/check", map[string]any{
			"client_id": "fiona", "client_secret": "wrong", "grant": grant,
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("wrong secret = %d, want 401", rec.Code)
		}
		if got := checkSSOGrant(t, e, "made-up"); got.Active || got.Reason != "unknown" {
			t.Errorf("unknown grant = %+v", got)
		}
	})
}

// TestSSOEndOnlyItsOwnSession: a sign-out ends the grants of that CLARA session
// of that account, and no other.
func TestSSOEndOnlyItsOwnSession(t *testing.T) {
	e := ssoEnv(t)
	u := e.mustUser("ola@example.org")
	other := e.mustUser("kari@example.org")
	grant := redeemSSOCode(t, e, issueSSOCode(t, e, u))

	e.do("POST", "/api/v1/sso/end", map[string]any{"sid": "another-session-id-0123456789abcdef"}, u)
	e.do("POST", "/api/v1/sso/end", map[string]any{"sid": ssoTestSID}, other)
	if got := checkSSOGrant(t, e, grant); !got.Active {
		t.Errorf("check = %+v, want still active", got)
	}
	if rec := e.do("POST", "/api/v1/sso/end", map[string]any{}, u); rec.Code != http.StatusBadRequest {
		t.Errorf("end without sid = %d, want 400", rec.Code)
	}
}

// TestSSOPurgesDeadRows: issuing a code removes rows nobody can use any more.
func TestSSOPurgesDeadRows(t *testing.T) {
	e := ssoEnv(t)
	u := e.mustUser("ola@example.org")
	issueSSOCode(t, e, u)
	e.exec(`UPDATE sso_grants SET code_expires_at = '2000-01-01 00:00:00', expires_at = '2000-01-01 00:00:00'`)
	issueSSOCode(t, e, u)
	var n int
	if err := e.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sso_grants`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 (the dead row purged)", n)
	}
}
