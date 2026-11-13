package admin

// Sequence L — single sign-out with the provider (REQ-AUTH-097…099, REQ-API-189/190,
// REQ-AUD-039, REQ-DB-057). The provider is the miniature OIDC server of
// assertion_test.go; logout tokens are signed by hand with its key, like the ID tokens
// there, so a forged case is forged here and not by a library shortcut.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// useSingleSignOut points the handler at a test provider with OIDC_SINGLE_SIGN_OUT=1.
func (e *env) useSingleSignOut(t *testing.T) (*testIdP, config.OAuth2Provider) {
	t.Helper()
	idp := newTestIdP(t)
	provider := idp.withProvider(func(p *config.OAuth2Provider) {
		p.PostLogoutRedirectURI = "https://csms.example.org/auth/logged-out"
	})
	e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
	e.Cfg.OAuthJWKSTTLSec = 3600
	e.Cfg.OIDCSingleSignOut = true
	return idp, provider
}

// mintLogout signs a logout token with the provider's key. The claims start as a valid
// back-channel logout token for subject-42; a case overrides members, and a nil value
// removes one. header overrides the JOSE header the same way.
func (p *testIdP) mintLogout(header, claims map[string]any) string {
	p.t.Helper()
	base := map[string]any{
		"iss":    p.issuer(),
		"aud":    "clara-test-client",
		"sub":    "subject-42",
		"iat":    time.Now().Unix(),
		"jti":    "jti-" + randomHex(p.t),
		"events": map[string]any{backchannelLogoutEvent: map[string]any{}},
	}
	for k, v := range claims {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	h := map[string]any{"alg": "RS256", "typ": "logout+jwt", "kid": p.kid}
	for k, v := range header {
		if v == nil {
			delete(h, k)
			continue
		}
		h[k] = v
	}
	rawHeader, _ := json.Marshal(h)
	rawClaims, _ := json.Marshal(base)
	signingInput := b64(rawHeader) + "." + b64(rawClaims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		p.t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + b64(sig)
}

func randomHex(t *testing.T) string {
	t.Helper()
	v, err := randomOAuthValue(12)
	if err != nil {
		t.Fatalf("random: %v", err)
	}
	return v
}

func (e *env) backchannel(provider int, token string) map[string]any {
	e.t.Helper()
	rec := e.do("POST", "/api/v1/auth/oauth/backchannel-logout",
		map[string]any{"provider": provider, "logout_token": token}, nil)
	out := map[string]any{"_status": float64(rec.Code)}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	out["_status"] = float64(rec.Code)
	return out
}

func (e *env) epochOf(id int64) int64 {
	e.t.Helper()
	epoch, err := e.Store.SessionEpoch(context.Background(), id)
	if err != nil {
		e.t.Fatalf("SessionEpoch: %v", err)
	}
	return epoch
}

// --- L1: the provider sign-out address (REQ-API-189) ---------------------------------

func TestEndSessionBuildsTheProviderSignOutAddress(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useSingleSignOut(t)
	idp.endSession = idp.issuer() + "/idp/startSLO.ping?realm=clinic"
	u := e.mustUser("member@example.org")

	rec := e.do("POST", "/api/v1/auth/oauth/end-session", map[string]any{"provider": 1}, u)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		URL   string `json:"end_session_url"`
		State string `json:"state"`
	}
	e.decode(rec, &body)
	if len(body.State) != 43 {
		t.Errorf("state = %q, want 32 random bytes in base64url (43 characters)", body.State)
	}
	parsed, err := url.Parse(body.URL)
	if err != nil || !strings.HasPrefix(body.URL, idp.issuer()+"/idp/startSLO.ping?") {
		t.Fatalf("end_session_url = %q, want the advertised endpoint", body.URL)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"realm":                    "clinic", // the endpoint's own query is kept
		"client_id":                provider.ClientID,
		"post_logout_redirect_uri": provider.PostLogoutRedirectURI,
		"state":                    body.State,
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if q.Has("id_token_hint") {
		t.Error("CLARA keeps no ID token and must not send id_token_hint (DEV-AUTH-23)")
	}

	// It stores nothing and writes no audit entry: Sequence D's logout records the sign-out.
	var n int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows = %d (%v), want none", n, err)
	}
}

func TestEndSessionRefusals(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useSingleSignOut(t)
	u := e.mustUser("member@example.org")

	// The provider advertises no end_session_endpoint.
	rec := e.do("POST", "/api/v1/auth/oauth/end-session", map[string]any{"provider": 1}, u)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "end_session_unsupported") {
		t.Errorf("no endpoint: %d %s, want 502 end_session_unsupported", rec.Code, rec.Body.String())
	}

	idp.endSession = idp.issuer() + "/logout"
	e.Handler.assertions.forget(idp.issuer())
	rec = e.do("POST", "/api/v1/auth/oauth/end-session", map[string]any{"provider": 9}, u)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "unknown_provider") {
		t.Errorf("unknown provider: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/auth/oauth/end-session", map[string]any{"provider": 1}, nil); rec.Code != 403 {
		t.Errorf("no acting user: %d, want 403", rec.Code)
	}

	e.Cfg.OIDCSingleSignOut = false
	rec = e.do("POST", "/api/v1/auth/oauth/end-session", map[string]any{"provider": 1}, u)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "single_sign_out_disabled") {
		t.Errorf("switch off: %d %s, want 409 single_sign_out_disabled", rec.Code, rec.Body.String())
	}
}

// --- L2: the provider's back-channel logout (REQ-API-190) ----------------------------

func TestBackchannelLogoutEndsEverySessionAndGrantOfTheAccount(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useSingleSignOut(t)
	ctx := context.Background()
	now := time.Now()
	u := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")
	if err := e.Store.EnsureProviderLink(ctx, provider.Issuer, "subject-42", u.ID, now); err != nil {
		t.Fatalf("EnsureProviderLink: %v", err)
	}
	// Two sign-ins of the account gave codes to clients, and another account holds one too.
	for i, g := range []db.SSOGrant{
		{ClientID: "fiona", UserID: u.ID, SIDHash: sha256Hex("sid-a")},
		{ClientID: "lims", UserID: u.ID, SIDHash: sha256Hex("sid-b")},
		{ClientID: "fiona", UserID: other.ID, SIDHash: sha256Hex("sid-c")},
	} {
		g.CodeHash = sha256Hex("code-" + string(rune('a'+i)))
		g.CodeChallenge, g.RedirectURI = "challenge", "https://fiona.example.org/cb"
		g.CodeExpiresAt = now.Add(time.Minute).UTC().Format("2006-01-02 15:04:05")
		g.ExpiresAt = now.Add(time.Hour).UTC().Format("2006-01-02 15:04:05")
		if _, err := e.Store.InsertSSOCode(ctx, &g, now); err != nil {
			t.Fatalf("InsertSSOCode: %v", err)
		}
	}
	before, otherBefore := e.epochOf(u.ID), e.epochOf(other.ID)

	token := idp.mintLogout(nil, nil)
	out := e.backchannel(1, token)
	if out["_status"] != float64(200) || out["matched"] != true || out["user_id"] != float64(u.ID) {
		t.Fatalf("answer = %v, want 200 matched for user %d", out, u.ID)
	}
	if got := out["clients"]; len(got.([]any)) != 2 || got.([]any)[0] != "fiona" || got.([]any)[1] != "lims" {
		t.Errorf("clients = %v, want [fiona lims]", got)
	}
	if e.epochOf(u.ID) != before+1 {
		t.Error("the account's session epoch was not raised: its CLARA sessions would survive")
	}
	if e.epochOf(other.ID) != otherBefore {
		t.Error("another account's sessions were ended")
	}
	var live int
	_ = e.Store.DB.QueryRow(`SELECT COUNT(*) FROM sso_grants WHERE ended_at IS NULL AND user_id = ?`, u.ID).Scan(&live)
	if live != 0 {
		t.Errorf("%d client grants of the account still live", live)
	}
	_ = e.Store.DB.QueryRow(`SELECT COUNT(*) FROM sso_grants WHERE ended_at IS NULL AND user_id = ?`, other.ID).Scan(&live)
	if live != 1 {
		t.Error("another account's client grant was ended")
	}

	details := lastAuditDetails(t, e, "provider_logout")
	if details["issuer"] != provider.Issuer || details["provider_subject"] != "subject-42" || details["matched"] != true {
		t.Errorf("provider_logout details = %v", details)
	}
	raw, _ := json.Marshal(details)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "jti") {
		t.Errorf("the audit entry carries token material: %s", raw)
	}

	// The same token again is a replay: refused, and nothing changes a second time.
	again := e.backchannel(1, token)
	if again["_status"] != float64(400) || again["error"] != "invalid_logout_token" {
		t.Errorf("replay answer = %v, want 400 invalid_logout_token", again)
	}
	if lastAuditDetails(t, e, "provider_logout_rejected")["reason"] != "replayed" {
		t.Error("the replay was not audited as replayed")
	}
	if e.epochOf(u.ID) != before+1 {
		t.Error("a replayed token raised the epoch again")
	}
}

func TestBackchannelLogoutForAnUnknownSubjectIsAnsweredAndAudited(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useSingleSignOut(t)

	out := e.backchannel(1, idp.mintLogout(nil, map[string]any{"sub": "nobody-here"}))
	if out["_status"] != float64(200) || out["matched"] != false || out["user_id"] != nil {
		t.Fatalf("answer = %v, want 200 matched false user_id null", out)
	}
	if c, ok := out["clients"].([]any); !ok || len(c) != 0 {
		t.Errorf("clients = %v, want []", out["clients"])
	}
	if lastAuditDetails(t, e, "provider_logout")["matched"] != false {
		t.Error("the unmatched token was not audited")
	}
}

// TestBackchannelLogoutRefusals walks §2.14 L2 step 3: each case breaks one rule, and the
// audit reason (REQ-AUD-039) says which. The answer is the same 400 for every one of them.
func TestBackchannelLogoutRefusals(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useSingleSignOut(t)
	u := e.mustUser("member@example.org")
	if err := e.Store.EnsureProviderLink(context.Background(), provider.Issuer, "subject-42", u.ID, time.Now()); err != nil {
		t.Fatalf("EnsureProviderLink: %v", err)
	}
	before := e.epochOf(u.ID)
	now := time.Now()

	tampered := idp.mintLogout(nil, nil)
	parts := strings.Split(tampered, ".")
	forged, _ := json.Marshal(map[string]any{"iss": idp.issuer(), "aud": "clara-test-client", "sub": "someone-else",
		"iat": now.Unix(), "jti": "x", "events": map[string]any{backchannelLogoutEvent: map[string]any{}}})
	tampered = parts[0] + "." + b64(forged) + "." + parts[2]

	cases := []struct {
		name, token, reason string
	}{
		{"not a JWS", "not-a-token", "malformed"},
		{"payload not JSON", b64([]byte(`{"alg":"RS256"}`)) + "." + b64([]byte("nope")) + ".sig", "malformed"},
		{"another algorithm", idp.mintLogout(map[string]any{"alg": "RS384"}, nil), "algorithm"},
		{"unsigned", b64([]byte(`{"alg":"none"}`)) + "." + b64([]byte(`{"sub":"subject-42"}`)) + ".", "algorithm"},
		{"an access token's typ", idp.mintLogout(map[string]any{"typ": "at+jwt"}, nil), "algorithm"},
		{"a payload changed after signing", tampered, "signature"},
		{"a key the provider never published", idp.mintLogout(map[string]any{"kid": "rotated-away"}, nil), "unknown_key"},
		{"another issuer", idp.mintLogout(nil, map[string]any{"iss": "https://other.example.org"}), "claims"},
		{"another audience", idp.mintLogout(nil, map[string]any{"aud": []string{"someone-else"}}), "claims"},
		{"no iat", idp.mintLogout(nil, map[string]any{"iat": nil}), "claims"},
		{"iat older than 10 minutes", idp.mintLogout(nil, map[string]any{"iat": now.Add(-11 * time.Minute).Unix()}), "claims"},
		{"iat in the future", idp.mintLogout(nil, map[string]any{"iat": now.Add(5 * time.Minute).Unix()}), "claims"},
		{"expired", idp.mintLogout(nil, map[string]any{"exp": now.Add(-5 * time.Minute).Unix()}), "claims"},
		{"no events", idp.mintLogout(nil, map[string]any{"events": nil}), "events"},
		{"another event", idp.mintLogout(nil, map[string]any{"events": map[string]any{"urn:other": map[string]any{}}}), "events"},
		{"event not an object", idp.mintLogout(nil, map[string]any{"events": map[string]any{backchannelLogoutEvent: "yes"}}), "events"},
		{"a nonce", idp.mintLogout(nil, map[string]any{"nonce": "n"}), "nonce_present"},
		{"an ID token", idp.mintLogout(map[string]any{"typ": "JWT"}, map[string]any{"events": nil, "nonce": "n", "email": "member@example.org"}), "events"},
		{"sid without sub", idp.mintLogout(nil, map[string]any{"sub": nil, "sid": "s-1"}), "subject_missing"},
		{"no jti", idp.mintLogout(nil, map[string]any{"jti": nil}), "jti_missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := e.backchannel(1, c.token)
			if out["_status"] != float64(400) || out["error"] != "invalid_logout_token" {
				t.Fatalf("answer = %v, want 400 invalid_logout_token", out)
			}
			if got := lastAuditDetails(t, e, "provider_logout_rejected")["reason"]; got != c.reason {
				t.Errorf("audit reason = %v, want %s", got, c.reason)
			}
		})
	}
	if e.epochOf(u.ID) != before {
		t.Error("a refused token ended the account's sessions")
	}

	// typ is optional, and both registered spellings pass (DEV-AUTH-23).
	for _, header := range []map[string]any{{"typ": nil}, {"typ": "JWT"}, {"typ": "application/logout+JWT"}} {
		if out := e.backchannel(1, idp.mintLogout(header, nil)); out["_status"] != float64(200) {
			t.Errorf("header %v: answer = %v, want 200", header, out)
		}
	}
}

func TestBackchannelLogoutRequestShape(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useSingleSignOut(t)

	if out := e.backchannel(2, idp.mintLogout(nil, nil)); out["_status"] != float64(400) || out["error"] != "unknown_provider" {
		t.Errorf("unknown provider: %v", out)
	}
	rec := e.do("POST", "/api/v1/auth/oauth/backchannel-logout", map[string]any{"provider": 1}, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("no token: %d %s", rec.Code, rec.Body.String())
	}
	e.Cfg.OIDCSingleSignOut = false
	if out := e.backchannel(1, idp.mintLogout(nil, nil)); out["_status"] != float64(409) {
		t.Errorf("switch off: %v, want 409", out)
	}
}

// TestLogoutReplayWindowRolls covers REQ-DB-057: a jti is refused while its row stands, and
// the window prunes rows past their expiry on the next insert, with no job.
func TestLogoutReplayWindowRolls(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	record := func(key string, expires, at time.Time) bool {
		t.Helper()
		tx, err := e.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ok, err := e.Store.RecordLogoutJTI(ctx, tx, key, expires, at)
		if err != nil {
			t.Fatalf("RecordLogoutJTI: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	key := sha256Hex("https://idp.example.org\x00jti-1")
	if !record(key, now.Add(12*time.Minute), now) {
		t.Fatal("first insert refused")
	}
	if record(key, now.Add(12*time.Minute), now.Add(time.Minute)) {
		t.Error("the same key inside the window was accepted")
	}
	if !record(sha256Hex("https://other.example.org\x00jti-1"), now.Add(12*time.Minute), now) {
		t.Error("another issuer's equal jti was refused")
	}
	if !record(key, now.Add(30*time.Minute), now.Add(13*time.Minute)) {
		t.Error("the row was not pruned once its window had passed")
	}
}
