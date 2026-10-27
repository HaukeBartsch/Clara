package admin

// A miniature OpenID Connect provider for tests, and the Sequence J cases
// (REQ-AUTH-076…082, 084) that a deployment's login path is judged by.
//
// The JWTs are assembled with crypto/rsa rather than a JOSE library on purpose: the
// production allowlist (Design/Technology_Stack_Design.md §3) admits go-oidc and what
// it pulls in, and a test that imports a fourth module would quietly widen it. It also
// keeps the negative cases honest — an attack token is forged by hand here, not by
// misusing a library's own shortcuts.

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"csms/api/internal/config"
)

// testIdP is an OpenID Connect provider: one RSA key, a discovery document, a JWKS
// endpoint, and a mint that produces tokens with whatever claims a case asks for.
type testIdP struct {
	t                *testing.T
	srv              *httptest.Server
	key              *rsa.PrivateKey
	kid              string
	alg              string // signature algorithm the mint uses by default
	jwksReqs         int32
	tokenClaims      map[string]any
	expectedVerifier string
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	p := &testIdP{t: t, key: key, kid: "test-key-1", alg: "RS256"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"userinfo_endpoint":                     p.srv.URL + "/userinfo",
			"jwks_uri":                              p.srv.URL + "/certs",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/certs", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&p.jwksReqs, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{p.jwk()}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") == "" ||
			r.Form.Get("code_verifier") == "" ||
			(p.expectedVerifier != "" && r.Form.Get("code_verifier") != p.expectedVerifier) {
			p.t.Logf("unexpected token exchange: parse_error=%v code=%q verifier=%q expected=%q", err, r.Form.Get("code"), r.Form.Get("code_verifier"), p.expectedVerifier)
			http.Error(w, "invalid authorization code or PKCE verifier", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		idToken := p.mint(p.tokenClaims)
		if strings.Count(r.Form.Get("code"), ".") == 2 {
			idToken = r.Form.Get("code")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access-token", "token_type": "Bearer", "id_token": idToken})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *testIdP) issuer() string { return p.srv.URL }

// jwksRequests counts JWKS fetches, which is how the caching expectations are tested:
// one verifier per issuer means one fetch across many logins (REQ-AUTH-077).
func (p *testIdP) jwksRequests() int { return int(atomic.LoadInt32(&p.jwksReqs)) }

func (p *testIdP) jwk() map[string]any {
	pub := &p.key.PublicKey
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": p.kid,
		"n":   b64(pub.N.Bytes()),
		"e":   b64(exponentBytes(pub.E)),
	}
}

// provider is the configuration this IdP corresponds to, with the fields Sequence J
// reads left at their defaults unless a case changes them.
func (p *testIdP) provider() config.OAuth2Provider {
	return config.OAuth2Provider{
		N: 1, Issuer: p.issuer(), ClientID: "clara-test-client", ClientSecret: "secret",
		RedirectURI: "https://csms.example.org/auth/callback",
		EmailAttr:   "email",
		SubjectAttr: "sub",
		IDTokenAlg:  "RS256",
	}
}

func (p *testIdP) withProvider(mutate func(*config.OAuth2Provider)) config.OAuth2Provider {
	provider := p.provider()
	mutate(&provider)
	return provider
}

func TestOAuthStartBuildsOIDCAuthorizationRequest(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useIdP(t)
	rec := e.do("POST", "/api/v1/auth/oauth/start", map[string]any{"provider": 1}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	authorizeURL := got["authorize_url"]
	if !strings.HasPrefix(authorizeURL, idp.issuer()+"/authorize?") {
		t.Fatalf("authorization URL = %q, want discovered authorization endpoint", authorizeURL)
	}
	values, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := values.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != "clara-test-client" ||
		query.Get("redirect_uri") != "https://csms.example.org/auth/callback" ||
		query.Get("code_challenge_method") != "S256" || query.Get("nonce") != got["nonce"] {
		t.Fatalf("authorization parameters = %v", query)
	}
	if got["state"] == "" || got["nonce"] == "" || got["code_verifier"] == "" {
		t.Fatalf("missing transaction values: %#v", got)
	}
	if query.Get("code_challenge") == "" {
		t.Fatal("authorization request has no PKCE challenge")
	}
}

func TestOAuthLoginExchangesCodeWithPKCEAndVerifiesIDToken(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	user := e.mustUser("oauth@example.org")
	start := e.do("POST", "/api/v1/auth/oauth/start", map[string]any{"provider": 1}, nil)
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", start.Code, start.Body.String())
	}
	var flow map[string]string
	if err := json.Unmarshal(start.Body.Bytes(), &flow); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	idp.expectedVerifier = flow["code_verifier"]
	idp.tokenClaims = map[string]any{
		"email": "oauth@example.org", "email_verified": true, "nonce": flow["nonce"],
	}

	rec := e.do("POST", "/api/v1/auth/login", map[string]any{
		"source": "oauth2", "provider": provider.Issuer, "source_name": "Hospital 1",
		"authorization_code": "test-code", "code_verifier": flow["code_verifier"],
		"assertion_nonce": flow["nonce"],
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got UserObject
	e.decode(rec, &got)
	if got.ID != user.ID || got.Email != user.Email {
		t.Errorf("user = %+v, want id %d and email %s", got, user.ID, user.Email)
	}
	if !hasType(e.auditTypes(), "login_success") {
		t.Errorf("audit types = %v, want login_success", e.auditTypes())
	}
}

// mint signs one ID token. Claims given here override the defaults, so a case can set
// email_verified to a string, drop acr, or move the expiry into the past.
func (p *testIdP) mint(claims map[string]any) string {
	p.t.Helper()
	return p.mintSigned(p.alg, claims)
}

func (p *testIdP) mintSigned(alg string, claims map[string]any) string {
	p.t.Helper()
	now := time.Now().UTC()
	base := map[string]any{
		"iss": p.issuer(),
		"aud": "clara-test-client",
		"sub": "subject-42",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range claims {
		base[k] = v
	}
	payload, err := json.Marshal(base)
	if err != nil {
		p.t.Fatalf("marshal claims: %v", err)
	}
	header, err := json.Marshal(map[string]any{"alg": alg, "typ": "JWT", "kid": p.kid})
	if err != nil {
		p.t.Fatalf("marshal header: %v", err)
	}
	signingInput := b64(header) + "." + b64(payload)

	switch alg {
	case "none":
		// The unsigned form REQ-AUTH-077 refuses outright.
		return signingInput + "."
	case "HS256":
		// The classic confusion: an HMAC keyed with the issuer's *public* key, which is
		// public — so anyone can produce a token that "verifies" if the verifier agrees
		// to use the algorithm the header names.
		return signingInput + "." + b64(hmacSHA256(p.publicKeyPEM(), []byte(signingInput)))
	default:
		digest := sha256.Sum256([]byte(signingInput))
		sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
		if err != nil {
			p.t.Fatalf("sign: %v", err)
		}
		return signingInput + "." + b64(sig)
	}
}

func (p *testIdP) publicKeyPEM() []byte {
	p.t.Helper()
	// The HMAC key an attacker would use: the modulus, which is not secret.
	return p.key.PublicKey.N.Bytes()
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func exponentBytes(e int) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(e))
	i := 0
	for i < len(buf)-1 && buf[i] == 0 {
		i++
	}
	return buf[i:]
}

func hmacSHA256(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

// useIdP points the handler at a test provider and returns both.
func (e *env) useIdP(t *testing.T) (*testIdP, config.OAuth2Provider) {
	t.Helper()
	idp := newTestIdP(t)
	provider := idp.provider()
	e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
	if e.Cfg.OAuthJWKSTTLSec == 0 {
		e.Cfg.OAuthJWKSTTLSec = 3600
	}
	return idp, provider
}

func loginArgs(email, assertion, nonce string) map[string]any {
	return map[string]any{
		"email": email, "source": "oauth2", "provider": "", "authorization_code": assertion,
		"code_verifier": "test-code-verifier", "assertion_nonce": nonce,
	}
}

// oauthLoginBody builds a source=oauth2 login body carrying a valid assertion for
// email. Tests whose subject is something other than the assertion — account state,
// bootstrap promotion, the second-factor exemption — use it, so DEV-AUTH-18's addition
// to the request is stated once and each test still reads as what it tests.
func oauthLoginBody(t *testing.T, idp *testIdP, issuer, email string, extra map[string]any) map[string]any {
	t.Helper()
	body := loginArgs(email, idp.mint(map[string]any{"email": email, "email_verified": true}), "")
	body["provider"] = issuer
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// TestSequenceJAcceptsSignedAssertion is the REQ-AUTH-076 happy path: a token signed by
// the configured issuer, for the configured client, opens the session and records the
// provider identity in the audit entry (REQ-AUTH-084) without recording the token.
func TestSequenceJAcceptsSignedAssertion(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	ctx := context.Background()
	u := e.mustUser("subject@example.org")

	token := idp.mint(map[string]any{"email": "subject@example.org", "email_verified": true})
	body := loginArgs("subject@example.org", token, "")
	body["provider"] = provider.Issuer

	rec := e.do("POST", "/api/v1/auth/login", body, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	details := lastAuditDetails(t, e, "login_success")
	if details["provider_issuer"] != provider.Issuer {
		t.Errorf("audit issuer = %v, want %v", details["provider_issuer"], provider.Issuer)
	}
	if details["provider_subject"] != "subject-42" {
		t.Errorf("audit subject = %v, want subject-42", details["provider_subject"])
	}
	if details["email_verified"] != true {
		t.Errorf("audit email_verified = %v, want true", details["email_verified"])
	}
	// REQ-AUTH-084/036: the token itself must not appear anywhere in the entry.
	if raw, err := json.Marshal(details); err == nil && strings.Contains(string(raw), token) {
		t.Error("login_success audited the ID token")
	}

	// The (iss, sub) binding is written on first verified login (REQ-AUTH-080).
	link, err := e.Store.GetProviderLinkBySubject(ctx, provider.Issuer, "subject-42")
	if err != nil || link == nil {
		t.Fatalf("provider subject link = %+v, err %v; want the account bound", link, err)
	}
	if link.UserID != u.ID {
		t.Errorf("link user = %d, want %d", link.UserID, u.ID)
	}
}

// TestSequenceJRejectsUnverifiedAndForged covers the refusals that must never open a
// session: a token from another key, `none`, an HMAC forged under the public key, and
// each shape of an unverified address.
func TestSequenceJRejectsUnverifiedAndForged(t *testing.T) {
	cases := []struct {
		name       string
		token      func(idp *testIdP) string
		wantStatus int
		wantReason string
	}{
		{"wrong key", func(idp *testIdP) string {
			other, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			payload, _ := json.Marshal(map[string]any{
				"iss": idp.issuer(), "aud": "clara-test-client", "sub": "subject-42",
				"email": "subject@example.org", "email_verified": true,
				"exp": now.Add(time.Minute).Unix(),
			})
			header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": idp.kid})
			input := b64(header) + "." + b64(payload)
			digest := sha256.Sum256([]byte(input))
			sig, err := rsa.SignPKCS1v15(rand.Reader, other, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			return input + "." + b64(sig)
		}, http.StatusUnauthorized, reasonAssertionInvalid},
		{"alg none", func(idp *testIdP) string {
			return idp.mintSigned("none", map[string]any{"email": "subject@example.org", "email_verified": true})
		}, http.StatusUnauthorized, reasonAssertionInvalid},
		{"alg HS256 under the public key", func(idp *testIdP) string {
			return idp.mintSigned("HS256", map[string]any{"email": "subject@example.org", "email_verified": true})
		}, http.StatusUnauthorized, reasonAssertionInvalid},
		{"expired", func(idp *testIdP) string {
			return idp.mint(map[string]any{
				"email": "subject@example.org", "email_verified": true,
				"exp": time.Now().Add(-10 * time.Minute).Unix(),
			})
		}, http.StatusUnauthorized, reasonAssertionInvalid},
		{"wrong audience", func(idp *testIdP) string {
			return idp.mint(map[string]any{
				"aud": "someone-elses-client", "email": "subject@example.org", "email_verified": true,
			})
		}, http.StatusUnauthorized, reasonAssertionInvalid},
		{"email_verified as a string", func(idp *testIdP) string {
			return idp.mint(map[string]any{"email": "subject@example.org", "email_verified": "true"})
		}, http.StatusUnauthorized, reasonEmailUnverified},
		{"email_verified false", func(idp *testIdP) string {
			return idp.mint(map[string]any{"email": "subject@example.org", "email_verified": false})
		}, http.StatusUnauthorized, reasonEmailUnverified},
		{"email_verified absent", func(idp *testIdP) string {
			return idp.mint(map[string]any{"email": "subject@example.org"})
		}, http.StatusUnauthorized, reasonEmailUnverified},
		{"no address claim", func(idp *testIdP) string {
			return idp.mint(map[string]any{"email_verified": true})
		}, http.StatusUnauthorized, reasonEmailUnverified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			idp, provider := e.useIdP(t)
			e.mustUser("subject@example.org")
			token := tc.token(idp)

			body := loginArgs("subject@example.org", token, "")
			body["provider"] = provider.Issuer
			rec := e.do("POST", "/api/v1/auth/login", body, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got["error"] != tc.wantReason {
				t.Errorf("error = %v, want %s", got["error"], tc.wantReason)
			}
		})
	}
}

// TestSequenceJNonce covers REQ-AUTH-079 in both directions and the two readings of an
// absent claim: required is a refusal, unobserved is not.
func TestSequenceJNonce(t *testing.T) {
	base := map[string]any{"email": "subject@example.org", "email_verified": true}

	t.Run("matching nonce", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		e.mustUser("subject@example.org")
		token := idp.mint(withNonce(base, "n-123"))
		body := loginArgs("subject@example.org", token, "n-123")
		body["provider"] = provider.Issuer
		if rec := e.do("POST", "/api/v1/auth/login", body, nil); rec.Code != 200 {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("differing nonce is refused", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		e.mustUser("subject@example.org")
		token := idp.mint(withNonce(base, "n-123"))
		body := loginArgs("subject@example.org", token, "other-nonce")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), reasonNonceMismatch) {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("absent nonce with the setting on", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		provider.NonceRequired = true
		e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
		e.mustUser("subject@example.org")
		token := idp.mint(base)
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), reasonNonceMismatch) {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("absent nonce with the setting off is accepted", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		e.mustUser("subject@example.org")
		token := idp.mint(base)
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = provider.Issuer
		if rec := e.do("POST", "/api/v1/auth/login", body, nil); rec.Code != 200 {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestSequenceJAssuranceGate is REQ-AUTH-081's fail-closed rule: with a gate
// configured, an assertion carrying no assurance claim is refused even though every
// other check passed.
func TestSequenceJAssuranceGate(t *testing.T) {
	cases := []struct {
		name       string
		claims     map[string]any
		wantStatus int
	}{
		{"no assurance claims at all", map[string]any{}, http.StatusForbidden},
		{"acr not among the configured values", map[string]any{"acr": "silver"}, http.StatusForbidden},
		{"acr matches", map[string]any{"acr": "urn:clara:mfa"}, 200},
		{"amr contains a configured method", map[string]any{"amr": []string{"pwd", "urn:clara:mfa"}}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			idp, provider := e.useIdP(t)
			provider.MinACR = []string{"urn:clara:mfa"}
			e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
			e.mustUser("subject@example.org")

			claims := map[string]any{"email": "subject@example.org", "email_verified": true}
			for k, v := range tc.claims {
				claims[k] = v
			}
			body := loginArgs("subject@example.org", idp.mint(claims), "")
			body["provider"] = provider.Issuer
			rec := e.do("POST", "/api/v1/auth/login", body, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == 403 && !strings.Contains(rec.Body.String(), reasonAssuranceMissing) {
				t.Errorf("error body = %s, want %s", rec.Body.String(), reasonAssuranceMissing)
			}
		})
	}
}

// TestSequenceJSubjectBinding is REQ-AUTH-080: the binding wins over the address, an
// unknown subject on a known address links, and two subjects claiming one account is a
// refusal an administrator resolves rather than a guess.
func TestSequenceJSubjectBinding(t *testing.T) {
	ctx := context.Background()

	t.Run("a bound subject resolves its account whatever the address says", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		bound := e.mustUser("original@example.org")
		if err := e.Store.EnsureProviderLink(ctx, provider.Issuer, "subject-42", bound.ID, time.Now()); err != nil {
			t.Fatalf("EnsureProviderLink: %v", err)
		}

		// The assertion names a different mailbox; the binding decides. REQ-AUTH-080 is
		// explicit that an address changed at the provider must not change who you are.
		token := idp.mint(map[string]any{"email": "reassigned@example.org", "email_verified": true})
		body := loginArgs("reassigned@example.org", token, "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a second subject on the same account is refused", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		u := e.mustUser("subject@example.org")
		if err := e.Store.EnsureProviderLink(ctx, provider.Issuer, "other-subject", u.ID, time.Now()); err != nil {
			t.Fatalf("EnsureProviderLink: %v", err)
		}

		token := idp.mint(map[string]any{"email": "subject@example.org", "email_verified": true})
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "subject_conflict") {
			t.Fatalf("status = %d, body = %s; want 403 subject_conflict", rec.Code, rec.Body.String())
		}
		// The refused attempt must not have moved the binding.
		link, err := e.Store.GetProviderLinkBySubject(ctx, provider.Issuer, "subject-42")
		if err != nil || link != nil {
			t.Errorf("link = %+v, err %v; want no new binding", link, err)
		}
	})

	t.Run("an unknown address stays account_not_found", func(t *testing.T) {
		e := newEnv(t)
		idp, provider := e.useIdP(t)
		token := idp.mint(map[string]any{"email": "nobody@example.org", "email_verified": true})
		body := loginArgs("nobody@example.org", token, "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "account_not_found") {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestSequenceJProviderFaultsAreNotAssertionFailures keeps an outage distinguishable
// from a forgery (REQ-AUTH-082's named provider error) — and makes sure a provider that
// cannot be reached does not spend the address's lockout budget.
func TestSequenceJProviderFaultsAreNotAssertionFailures(t *testing.T) {
	t.Run("unreachable provider", func(t *testing.T) {
		e := newEnv(t)
		idp := newTestIdP(t)
		provider := idp.provider()
		e.Cfg.OAuth2 = []config.OAuth2Provider{provider}
		e.mustUser("subject@example.org")

		idp.srv.Close() // discovery and JWKS both fail now

		token := idp.mint(map[string]any{"email": "subject@example.org", "email_verified": true})
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), reasonProviderUnavail) {
			t.Fatalf("status = %d, body = %s; want 502 %s", rec.Code, rec.Body.String(), reasonProviderUnavail)
		}
	})

	t.Run("unconfigured provider", func(t *testing.T) {
		e := newEnv(t)
		idp, _ := e.useIdP(t)
		e.mustUser("subject@example.org")
		token := idp.mint(map[string]any{"email": "subject@example.org", "email_verified": true})
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = "https://unknown.example.net"
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), reasonUnknownProvider) {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing assertion", func(t *testing.T) {
		e := newEnv(t)
		_, provider := e.useIdP(t)
		e.mustUser("subject@example.org")
		body := loginArgs("subject@example.org", "", "")
		body["provider"] = provider.Issuer
		rec := e.do("POST", "/api/v1/auth/login", body, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), reasonAssertionInvalid) {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestSequenceJKeysAreCachedAcrossLogins is the caching half of REQ-AUTH-077: one key
// set per issuer, fetched once, not one fetch per sign-in.
func TestSequenceJKeysAreCachedAcrossLogins(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	e.mustUser("subject@example.org")

	for i := 0; i < 3; i++ {
		token := idp.mint(map[string]any{"email": "subject@example.org", "email_verified": true})
		body := loginArgs("subject@example.org", token, "")
		body["provider"] = provider.Issuer
		if rec := e.do("POST", "/api/v1/auth/login", body, nil); rec.Code != 200 {
			t.Fatalf("login %d: status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
	}
	if got := idp.jwksRequests(); got > 1 {
		t.Errorf("jwks fetched %d times across three logins, want at most 1", got)
	}
}

func withNonce(claims map[string]any, nonce string) map[string]any {
	out := map[string]any{}
	for k, v := range claims {
		out[k] = v
	}
	out["nonce"] = nonce
	return out
}
