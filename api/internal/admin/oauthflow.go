package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"csms/api/internal/config"
)

type oauthStartRequest struct {
	Provider int `json:"provider"`
}

type oauthCodeRequest struct {
	Provider     int    `json:"provider"`
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	Nonce        string `json:"nonce"`
}

func (h *Handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	var body oauthStartRequest
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	provider, ok := h.providerByNumber(body.Provider)
	if !ok {
		APIError(w, http.StatusBadRequest, "unknown_provider", "this authentication provider is not configured")
		return
	}

	runtime, err := h.oauthRuntime(r, provider)
	if err != nil {
		failure := discoveryFailure(err)
		APIError(w, failure.status, failure.reason, failure.message)
		return
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		errInternal(w)
		return
	}
	nonce, err := randomOAuthValue(32)
	if err != nil {
		errInternal(w)
		return
	}
	verifier := oauth2.GenerateVerifier()
	authorizeURL := runtime.oauth.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	writeJSON(w, http.StatusOK, map[string]string{
		"authorize_url": authorizeURL,
		"state":         state,
		"code_verifier": verifier,
		"nonce":         nonce,
	})
}

func (h *Handler) oauthRuntime(r *http.Request, provider config.OAuth2Provider) (*providerRuntime, error) {
	reuse := time.Duration(h.Cfg.OAuthJWKSTTLSec) * time.Second
	if reuse <= 0 {
		reuse = time.Hour
	}
	return h.assertions.runtimeFor(r.Context(), provider, reuse)
}

func (h *Handler) providerByNumber(number int) (config.OAuth2Provider, bool) {
	for _, provider := range h.Cfg.OAuth2 {
		if provider.N == number {
			return provider, true
		}
	}
	return config.OAuth2Provider{}, false
}

func randomOAuthValue(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// idTokenOf pulls the ID token out of a token response. A provider answering an `openid`
// request without one proves nothing Sequence J can check, so the exchange refuses it rather
// than letting an empty assertion reach the verifier.
func idTokenOf(token *oauth2.Token) string {
	if token == nil {
		return ""
	}
	id, _ := token.Extra("id_token").(string)
	return strings.TrimSpace(id)
}

// exchangeOAuthCode redeems one authorization code with its PKCE verifier and the
// configured client credentials, returning the token response Sequence J works from: the ID
// token it verifies, and the access token the userinfo fallback reads when that ID token
// carries no address (REQ-AUTH-078, REQ-AUTH-086).
func (h *Handler) exchangeOAuthCode(r *http.Request, issuer, code, verifier string) (*oauth2.Token, *assertionFailure) {
	provider, ok := h.providerByIssuer(issuer)
	if !ok {
		return nil, newFailure(reasonUnknownProvider, http.StatusUnauthorized,
			"this authentication provider is not configured")
	}
	if strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" {
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the authorization response is incomplete")
	}

	runtime, err := h.oauthRuntime(r, provider)
	if err != nil {
		return nil, discoveryFailure(err)
	}
	token, err := runtime.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, newFailure(reasonProviderUnavail, http.StatusBadGateway,
			"the authentication provider could not complete sign-in")
	}
	if idTokenOf(token) == "" {
		return nil, newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the provider did not return an ID token")
	}
	return token, nil
}

func (h *Handler) oauthTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	var body oauthCodeRequest
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	provider, ok := h.providerByNumber(body.Provider)
	if !ok {
		APIError(w, http.StatusBadRequest, "unknown_provider", "this authentication provider is not configured")
		return
	}
	runtime, err := h.oauthRuntime(r, provider)
	if err != nil {
		failure := discoveryFailure(err)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "stage": "discovery", "issuer": provider.Issuer,
			"discovery_status": 0, "token_status": 0, "userinfo_status": 0,
			"userinfo_claims": map[string]any{}, "id_token_claims": map[string]any{},
			"error": failure.reason,
		})
		return
	}

	report := map[string]any{
		"ok": true, "issuer": provider.Issuer,
		"endpoints": map[string]string{
			"authorize": runtime.oauth.Endpoint.AuthURL,
			"token":     runtime.oauth.Endpoint.TokenURL,
			"userinfo":  runtime.userinfo,
		},
		"discovery_status": 200, "token_status": 0, "userinfo_status": 0,
		"userinfo_claims": map[string]any{}, "id_token_claims": map[string]any{},
	}
	token, failure := h.exchangeOAuthCode(r, provider.Issuer, body.Code, body.CodeVerifier)
	if failure != nil {
		report["ok"] = false
		report["stage"] = "token_exchange"
		report["error"] = failure.reason
		report["token_status"] = failure.status
		writeJSON(w, http.StatusOK, report)
		return
	}
	report["token_status"] = 200

	// The diagnostic reads the userinfo endpoint whether or not Sequence J needs it. An
	// administrator debugging a provider that releases `email` to userinfo and not to the ID
	// token has to see what that endpoint answered — an empty column with no reason is what
	// made a split claim release invisible (REQ-AUTH-078, REQ-AUTH-085).
	if claims, status, _ := h.userInfo(r.Context(), runtime.userinfo, token.AccessToken); status > 0 {
		report["userinfo_status"] = status
		report["userinfo_claims"] = safeDiagnosticClaims(claims)
	}

	assertion, failure := h.verifyAssertion(r.Context(), provider.Issuer, idTokenOf(token), body.Nonce, token.AccessToken)
	if failure != nil {
		report["ok"] = false
		report["stage"] = "assertion"
		report["error"] = failure.reason
		writeJSON(w, http.StatusOK, report)
		return
	}
	report["id_token_claims"] = assertion.auditDetails()
	writeJSON(w, http.StatusOK, report)
}

// diagnosticClaimAllowlist is what the OAuth diagnostic may show an administrator: identity,
// assurance and directory-membership claims, and nothing that could be mistaken for a
// credential (REQ-AUTH-085, REQ-AUTH-036).
var diagnosticClaimAllowlist = []string{
	"iss", "sub", "aud", "exp", "iat", "auth_time", "email", "email_verified",
	"name", "given_name", "family_name", "preferred_username", "groups", "roles",
	"memberOf", "acr", "amr",
}

// diagnosticGroupClaim admits the membership claims a directory names however it likes,
// which is the one pattern an allowlist cannot enumerate (REQ-AUTH-085).
var diagnosticGroupClaim = regexp.MustCompile(`(?i)groups?$|roles?$|memberof`)

const (
	diagnosticValueLimit = 500
	diagnosticListLimit  = 30
	diagnosticNameLimit  = 200
)

// safeDiagnosticClaims projects a provider's claim set onto the allowlist, bounding every
// name and value. The claims come from a server this installation trusts to authenticate
// people but has no reason to trust for rendering, and the report is rendered (REQ-AUTH-085,
// REQ-UI-008).
func safeDiagnosticClaims(claims map[string]json.RawMessage) map[string]any {
	out := map[string]any{}
	for name, raw := range claims {
		if !slices.Contains(diagnosticClaimAllowlist, name) && !diagnosticGroupClaim.MatchString(name) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		key := truncateDiagnostic(name, diagnosticNameLimit)
		switch typed := value.(type) {
		case string:
			out[key] = truncateDiagnostic(typed, diagnosticValueLimit)
		case bool, float64:
			out[key] = typed
		case []any:
			items := make([]string, 0, diagnosticListLimit)
			for _, entry := range typed {
				switch item := entry.(type) {
				case string:
					items = append(items, truncateDiagnostic(item, diagnosticValueLimit))
				case bool:
					items = append(items, strconv.FormatBool(item))
				case float64:
					items = append(items, strconv.FormatFloat(item, 'f', -1, 64))
				}
				if len(items) == diagnosticListLimit {
					break
				}
			}
			out[key] = items
		default:
			// Nested objects carry nothing the allowlist admits; dropping them keeps the
			// report flat and bounded.
		}
	}
	return out
}

// truncateDiagnostic cuts a provider-supplied string to a rune budget, never mid-rune.
func truncateDiagnostic(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
