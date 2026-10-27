package admin

// Sequence J — provider assertion verification (REQ-AUTH-076…082, DEV-AUTH-18,
// Authentication_Authorization_Design.md §2.10).
//
// This is the file that makes an OAuth2 login testable inside the API instead of
// something the web layer asserts. PHP still owns the authorization-code round trip
// end to end; what it now also does is hand over the ID token the issuer signed for
// it, and everything below satisfies the API itself that the token is genuine before
// a single user row is read (finding F8). Verification uses
// github.com/coreos/go-oidc/v3 — admitted for this purpose by
// Design/Technology_Stack_Design.md §3 under GD-25/REQ-TECH-026 — rather than
// hand-written JOSE, because this is the one place in the system where a parsing
// mistake opens a session.
//
// What the library does NOT do, and this file therefore does: the nonce comparison
// (go-oidc documents it as the caller's responsibility), the bounded clock skew, the
// type-strict `email_verified` rule, the acr/amr assurance gate, and the JWKS reuse
// bound OAUTH2_JWKS_TTL asks for. Those are CLARA policy, and they stay visible here.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"csms/api/internal/config"
)

// assertionClockSkew is the bounded allowance Sequence J step 5 fixes at 120
// seconds: a five-minute ID-token lifetime makes an uncorrected clock look like an
// attack, and a refusal that says "invalid" teaches nobody to check their NTP.
//
// go-oidc has no leeway field — it compares against Config.Now — so the allowance is
// expressed by presenting the verifier with a clock that runs skew late. That widens
// `exp` by the allowance and `nbf` by the same amount, which is what a ±skew window
// means; it does not widen anything else, because this function is the only clock an
// assertion check sees. It is deliberately not configurable: an operator raising it
// would be quietly extending how long a stolen token stays usable.
const assertionClockSkew = 120 * time.Second

// verifiedAssertion is what Sequence J establishes about one provider login. Only
// the fields the audit trail and account resolution need are carried forward, and no
// token material is among them (REQ-AUTH-036, REQ-AUTH-084).
type verifiedAssertion struct {
	Issuer        string
	Subject       string // the configured subject claim, stored and compared verbatim (REQ-AUTH-080)
	Email         string // the configured email claim, lower-cased as accounts are keyed
	EmailVerified bool   // whether `email_verified` was the JSON boolean true
	Acr           string // observed assurance claims — recorded, never interpreted again
	Amr           []string
	NonceChecked  bool // a nonce claim was present and matched (REQ-AUTH-079)
}

// assertionFailure is a refusal from Sequence J: the reason goes to the audit trail,
// the status and message to the caller. Reasons are stable identifiers because
// REQ-AUTH-084 requires an assertion refusal to be distinguishable in the audit log
// from one the provider itself produced.
type assertionFailure struct {
	reason  string
	status  int
	message string
	// observed carries whatever Sequence J had established when it refused, so a
	// login_failure audit entry can name the issuer, subject and assurance claims it
	// turned away (REQ-AUTH-084) without inventing any it never parsed. A refusal at
	// the algorithm pin has nothing to carry, and says so by leaving this nil.
	observed *verifiedAssertion
}

// newFailure builds a refusal. It is a constructor rather than a composite literal so
// that the call sites stay positional and readable while `observed` stays an optional
// attachment (see withObserved).
func newFailure(reason string, status int, message string) *assertionFailure {
	return &assertionFailure{reason: reason, status: status, message: message}
}

// withObserved attaches what had been established at the point of refusal, for the
// audit trail (REQ-AUTH-084).
func (f *assertionFailure) withObserved(a *verifiedAssertion) *assertionFailure {
	f.observed = a
	return f
}

// auditDetails is the REQ-AUTH-084 detail set: issuer, provider subject, the
// assurance claims observed, and whether the address was verified — never a token, a
// signature, or any other claim material (REQ-AUTH-036). Nil-safe, because a refusal
// that came before any claim could be read has none of it.
func (a *verifiedAssertion) auditDetails() map[string]any {
	if a == nil {
		return nil
	}
	out := map[string]any{
		"provider_issuer":  a.Issuer,
		"provider_subject": a.Subject,
		"email_verified":   a.EmailVerified,
	}
	if a.Acr != "" {
		out["acr"] = a.Acr
	}
	if len(a.Amr) > 0 {
		out["amr"] = a.Amr
	}
	return out
}

// Reason codes for the refusals below. `provider_unavailable` is deliberately not
// folded into `assertion_invalid`: an IdP that cannot be reached is an outage to fix,
// while an invalid assertion is either a misconfiguration or an attempt, and an
// administrator reading one audit trail has to be able to tell them apart.
const (
	reasonAssertionInvalid = "assertion_invalid"
	reasonEmailUnverified  = "email_unverified"
	reasonAssuranceMissing = "assurance_insufficient"
	reasonNonceMismatch    = "nonce_mismatch"
	reasonProviderUnavail  = "provider_unavailable"
	reasonIssuerMismatch   = "provider_issuer_mismatch"
	reasonUnknownProvider  = "unknown_provider"
)

// providerVerifiers caches one verifier per issuer for the life of the process.
//
// Caching is required, not merely fast: go-oidc's remote key set fetches and caches
// keys precisely so that it is long lived ("reuse a single remote key set rather than
// creating a new one for each verification"), and a fresh one per login would put a
// round trip to the identity provider in front of every sign-in. A cached entry is
// dropped when its provider turns out to be unreachable, so a document that gets
// fixed — or a key set that starts answering again — is picked up on the next
// attempt without a restart (REQ-AUTH-082).
type providerVerifiers struct {
	mu       sync.Mutex
	byIssuer map[string]*providerRuntime
}

type providerRuntime struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	jwksURI  string
	userinfo string
}

func newProviderVerifiers() *providerVerifiers {
	return &providerVerifiers{byIssuer: map[string]*providerRuntime{}}
}

// runtimeFor resolves discovery once and shares the resulting endpoints, key source,
// and verifier between authorization-code exchange and assertion verification.
func (c *providerVerifiers) runtimeFor(ctx context.Context, p config.OAuth2Provider, jwksReuse time.Duration) (*providerRuntime, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if runtime, ok := c.byIssuer[p.Issuer]; ok {
		return runtime, nil
	}

	provider, err := oidc.NewProvider(ctx, p.Issuer)
	if err != nil {
		return nil, err
	}

	// REQ-AUTH-082: the discovery document must name a jwks_uri. go-oidc would
	// otherwise build a key set with an empty URL and fail later as a verification
	// error that reads like a bad token; failing here names the provider instead,
	// which is exactly what withdrawing the conventional-path fallback was for.
	var discovered struct {
		JWKSURI     string `json:"jwks_uri"`
		UserInfoURL string `json:"userinfo_endpoint"`
	}
	if err := provider.Claims(&discovered); err != nil || strings.TrimSpace(discovered.JWKSURI) == "" {
		return nil, fmt.Errorf("discovery document for %s names no jwks_uri (REQ-AUTH-082)", p.Issuer)
	}
	endpoint := provider.Endpoint()
	if strings.TrimSpace(endpoint.AuthURL) == "" || strings.TrimSpace(endpoint.TokenURL) == "" {
		return nil, fmt.Errorf("discovery document for %s names no authorization or token endpoint", p.Issuer)
	}

	cfg := &oidc.Config{
		ClientID: p.ClientID,
		// Pinned from configuration on purpose. Built any other way from a Provider,
		// go-oidc defaults this to the set the provider advertises — and providers
		// advertise `none` and the HMAC family, which is the failure REQ-AUTH-077
		// exists to prevent (an algorithm an attacker can sign themselves).
		SupportedSigningAlgs: []string{p.IDTokenAlg},
		Now:                  func() time.Time { return time.Now().Add(assertionClockSkew) },
	}

	// go-oidc's own key set caches until it sees an unknown kid and never on a timer,
	// so OAUTH2_JWKS_TTL is honoured by rebuilding it when its reuse window closes.
	keySet := newReuseBoundedKeySet(ctx, discovered.JWKSURI, jwksReuse)
	runtime := &providerRuntime{
		provider: provider,
		verifier: oidc.NewVerifier(p.Issuer, keySet, cfg),
		oauth: &oauth2.Config{
			ClientID:     p.ClientID,
			ClientSecret: p.ClientSecret,
			Endpoint:     endpoint,
			RedirectURL:  p.RedirectURI,
			Scopes:       []string{"openid", "email", "profile"},
		},
		jwksURI:  discovered.JWKSURI,
		userinfo: discovered.UserInfoURL,
	}
	c.byIssuer[p.Issuer] = runtime
	return runtime, nil
}

// verifierFor returns a verifier pinned to one provider's issuer, client id and
// signing algorithm.
func (c *providerVerifiers) verifierFor(ctx context.Context, p config.OAuth2Provider, jwksReuse time.Duration) (*oidc.IDTokenVerifier, error) {
	runtime, err := c.runtimeFor(ctx, p, jwksReuse)
	if err != nil {
		return nil, err
	}
	return runtime.verifier, nil
}

// forget drops a cached verifier so the next attempt rediscovers. Called when a
// provider could not be reached, so an outage does not fossilise into a permanent
// refusal (REQ-AUTH-082's "honest failure" is about being honest *and* recoverable).
func (c *providerVerifiers) forget(issuer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byIssuer, issuer)
}

// reuseBoundedKeySet wraps go-oidc's remote key set so fetched keys are reused for at
// most a configured interval (OAUTH2_JWKS_TTL, REQ-CFG-035). Inside the window every
// verification hits the in-process cache; an unknown kid still triggers the library's
// own immediate refetch, which is the rotation path that actually matters.
type reuseBoundedKeySet struct {
	jwksURL string
	ttl     time.Duration
	ctx     context.Context

	mu        sync.Mutex
	inner     *oidc.RemoteKeySet
	expiresAt time.Time
}

func newReuseBoundedKeySet(ctx context.Context, jwksURL string, ttl time.Duration) *reuseBoundedKeySet {
	return &reuseBoundedKeySet{jwksURL: jwksURL, ttl: ttl, ctx: ctx}
}

// VerifySignature implements oidc.KeySet for oidc.NewVerifier. A new inner key set is
// swapped in when the reuse window closes — a fresh one has an empty cache, so the
// next verification refetches and replaces what is held.
func (k *reuseBoundedKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	return k.current().VerifySignature(ctx, jwt)
}

func (k *reuseBoundedKeySet) current() *oidc.RemoteKeySet {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.inner == nil || time.Now().After(k.expiresAt) {
		k.inner = oidc.NewRemoteKeySet(k.ctx, k.jwksURL)
		k.expiresAt = time.Now().Add(k.ttl)
	}
	return k.inner
}

// providerByIssuer finds the configuration a named provider is checked against
// (Sequence C carries the issuer as `provider`, §2.3). Exact string equality: the
// issuer is what the token's own `iss` has to equal, so trimming or case-folding it
// here would invent a second notion of what identifies a provider.
func (h *Handler) providerByIssuer(issuer string) (config.OAuth2Provider, bool) {
	for _, p := range h.Cfg.OAuth2 {
		if p.Issuer == issuer {
			return p, true
		}
	}
	return config.OAuth2Provider{}, false
}

// verifyAssertion runs Sequence J steps 1–7 (parse, pin, keys, signature, audience
// and time, nonce, address attestation, assurance) over the ID token PHP delivered.
// Steps 8 and 9 — resolving the account from (`iss`, `sub`) and auditing — belong to
// the login handler, which owns the store and the audit writer.
//
// expectedNonce is the value PHP generated alongside `state` and the PKCE verifier;
// it arrives on the login call because only that browser session knows it. An empty
// expected value is not "no nonce required" — that question is what
// OAUTH2_N_NONCE_REQUIRED answers (REQ-AUTH-079).
func (h *Handler) verifyAssertion(ctx context.Context, issuer, rawAssertion, expectedNonce string) (*verifiedAssertion, *assertionFailure) {
	provider, ok := h.providerByIssuer(issuer)
	if !ok {
		// The login names a provider this installation did not configure. Nothing
		// about the token can be checked against an unknown issuer, and saying so is
		// more useful than pretending the signature was wrong.
		return nil, newFailure(reasonUnknownProvider, http.StatusUnauthorized,
			"this authentication provider is not configured")
	}
	if strings.TrimSpace(rawAssertion) == "" {
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"an OAuth2 login requires the provider's ID token")
	}

	reuse := time.Duration(h.Cfg.OAuthJWKSTTLSec) * time.Second
	if reuse <= 0 {
		reuse = time.Hour // config's own default; guards a zero-valued Config in tests
	}
	verifier, err := h.assertions.verifierFor(ctx, provider, reuse)
	if err != nil {
		h.assertions.forget(provider.Issuer)
		return nil, discoveryFailure(err)
	}

	idToken, err := verifier.Verify(ctx, rawAssertion)
	if err != nil {
		if isTransportFailure(err) {
			// Keys could not be fetched: the provider is unreachable, which is neither
			// this user's fault nor an invalid token. Drop the cached verifier so the
			// next attempt starts from discovery again.
			h.assertions.forget(provider.Issuer)
			return nil, newFailure(reasonProviderUnavail, http.StatusBadGateway,
				"the authentication provider could not be reached")
		}
		// Signature, algorithm, issuer, audience, expiry: one answer for all of them.
		// The differences tell an attacker which check to evade next, and none of them
		// is actionable by a user — the configuration check is where they surface.
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the provider's assertion could not be verified")
	}

	var claims map[string]json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the provider's assertion could not be read")
	}

	// found accumulates what has been established as the checks proceed. A refusal
	// further down still knows the issuer and subject it turned away, and REQ-AUTH-084
	// wants exactly that in the audit entry — no more of the claim set than that.
	found := &verifiedAssertion{Issuer: idToken.Issuer, Subject: idToken.Subject}

	// Step 5 (REQ-AUTH-079): a nonce the payload carries must be the one this browser
	// was sent. Absent is a policy question — an unset OAUTH2_N_NONCE_REQUIRED records
	// "not observed", and treating that as satisfied would make the claim optional in
	// exactly the deployments where it matters.
	nonceChecked := false
	if raw, present := claims["nonce"]; present {
		var tokenNonce string
		if err := json.Unmarshal(raw, &tokenNonce); err != nil {
			return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
				"the provider's assertion could not be verified").withObserved(found)
		}
		if subtle.ConstantTimeCompare([]byte(tokenNonce), []byte(expectedNonce)) != 1 {
			return nil, newFailure(reasonNonceMismatch, http.StatusUnauthorized,
				"the provider's assertion does not belong to this sign-in attempt").withObserved(found)
		}
		nonceChecked = true
	} else if provider.NonceRequired {
		return nil, newFailure(reasonNonceMismatch, http.StatusUnauthorized,
			"the provider's assertion carries no nonce, which this installation requires").withObserved(found)
	}

	// Step 6 (REQ-AUTH-078): the address, and the provider's own statement that it is
	// verified. The comparison is by type as well as value — a string "true" is not
	// true, and accepting one reopens finding F10.
	var email string
	if raw, present := claims[provider.EmailAttr]; present {
		_ = json.Unmarshal(raw, &email) // a non-string claim leaves email empty → refused below
	}
	email = strings.ToLower(strings.TrimSpace(email))
	found.Email = email
	if email == "" {
		return nil, newFailure(reasonEmailUnverified, http.StatusUnauthorized,
			"the provider's assertion names no address for this installation").withObserved(found)
	}
	emailVerified := false
	if raw, present := claims["email_verified"]; present {
		// Compared against the JSON literal `true`, so the check is by type as well as
		// value: `false`, `null`, `1` and the string `"true"` all fail it, which is what
		// REQ-AUTH-078 means by "a string \"true\" is not true". An absent claim fails
		// the same way — REQ-AUTH-078 calls that a provider-configuration fault rather
		// than an unverified login, and the diagnostic report (REQ-AUTH-085) is where an
		// administrator sees which of the two a deployment is living with.
		emailVerified = string(bytes.TrimSpace(raw)) == "true"
	}
	found.EmailVerified = emailVerified
	if !provider.AllowUnverifiedEmail && !emailVerified {
		return nil, newFailure(reasonEmailUnverified, http.StatusUnauthorized,
			"the provider did not verify this address").withObserved(found)
	}

	// Step 7 (REQ-AUTH-081): the assurance gate, failing closed. This is what makes
	// the OAuth2 exemption from CLARA's own second factor a checked fact rather than
	// an assumption — and an assertion with no assurance claim at all is refused, not
	// waved through as "nothing required".
	var acr string
	if raw, present := claims["acr"]; present {
		_ = json.Unmarshal(raw, &acr)
	}
	var amr []string
	if raw, present := claims["amr"]; present {
		_ = json.Unmarshal(raw, &amr)
	}
	found.Acr, found.Amr = acr, amr
	if provider.AssuranceGate() && !assuranceAccepted(acr, amr, provider.MinACR) {
		return nil, newFailure(reasonAssuranceMissing, http.StatusForbidden,
			"the provider's assertion does not meet the configured authentication context").withObserved(found)
	}

	// Step 8's input: the subject. Read from the configured claim so a provider that
	// names its stable identifier something other than `sub` still binds; stored and
	// compared verbatim, never parsed or derived (REQ-AUTH-080).
	subject := idToken.Subject
	if raw, present := claims[provider.SubjectAttr]; present {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			subject = s
		}
	}
	if strings.TrimSpace(subject) == "" {
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the provider's assertion names no subject to bind").withObserved(found)
	}
	found.Subject = subject
	found.NonceChecked = nonceChecked

	return found, nil
}

// assuranceAccepted reports whether an assertion's acr/amr satisfy a configured gate.
// A configured value matches either claim — OAUTH2_N_MIN_ACR lists context names, and
// providers put the same identifiers in `acr`, in `amr`, or in both depending on how
// they were built (REQ-AUTH-081).
func assuranceAccepted(acr string, amr []string, required []string) bool {
	if acr == "" && len(amr) == 0 {
		return false // fail closed on a missing claim, not open on an empty one
	}
	for _, want := range required {
		if acr != "" && acr == want {
			return true
		}
		for _, seen := range amr {
			if seen == want {
				return true
			}
		}
	}
	return false
}

// discoveryFailure turns a discovery error into a refusal with the shape REQ-AUTH-082
// asks for: an issuer that disagrees with the configured one is a named provider
// error, not a generic "could not connect". go-oidc has checked that equality since
// admitting IssuerMismatchError; the distinction survives here as the operator-facing
// message.
func discoveryFailure(err error) *assertionFailure {
	// An issuer that disagrees with the configured one is a configuration fault, not
	// an outage, and it gets its own reason so the audit trail says which. The
	// discovered value goes to the log rather than the response: REQ-AUTH-082 asks for
	// a named provider error, and naming it for the operator is the part that helps.
	var mismatch *oidc.IssuerMismatchError
	if errors.As(err, &mismatch) {
		slog.Warn("oauth discovery issuer mismatch",
			"configured", mismatch.Provided, "discovered", mismatch.Discovered) // not token material (REQ-AUTH-036)
		return newFailure(reasonIssuerMismatch, http.StatusBadGateway,
			"the provider's issuer does not match the one configured for it")
	}
	if isTransportFailure(err) {
		return newFailure(reasonProviderUnavail, http.StatusBadGateway,
			"the authentication provider could not be reached")
	}
	slog.Warn("oauth discovery document unusable", "error", err)
	return newFailure(reasonProviderUnavail, http.StatusBadGateway,
		"the authentication provider's configuration document is not usable")
}

// isTransportFailure reports whether an error came from failing to talk to the
// provider — as opposed to talking to it and being told the token is no good. The two
// deserve different answers: one is a 502 an administrator acts on, the other a 401
// that also spends the address's lockout budget (REQ-AUTH-035).
func isTransportFailure(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
