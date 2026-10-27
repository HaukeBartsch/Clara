package admin

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
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

func (h *Handler) exchangeOAuthCode(r *http.Request, issuer, code, verifier string) (string, *assertionFailure) {
	provider, ok := h.providerByIssuer(issuer)
	if !ok {
		return "", newFailure(reasonUnknownProvider, http.StatusUnauthorized,
			"this authentication provider is not configured")
	}
	if strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" {
		return "", newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the authorization response is incomplete")
	}

	runtime, err := h.oauthRuntime(r, provider)
	if err != nil {
		return "", discoveryFailure(err)
	}
	token, err := runtime.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", newFailure(reasonProviderUnavail, http.StatusBadGateway,
			"the authentication provider could not complete sign-in")
	}
	idToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(idToken) == "" {
		return "", newFailure(reasonAssertionInvalid, http.StatusUnauthorized,
			"the provider did not return an ID token")
	}
	return idToken, nil
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
	idToken, failure := h.exchangeOAuthCode(r, provider.Issuer, body.Code, body.CodeVerifier)
	if failure != nil {
		report["ok"] = false
		report["stage"] = "token_exchange"
		report["error"] = failure.reason
		report["token_status"] = failure.status
		writeJSON(w, http.StatusOK, report)
		return
	}
	assertion, failure := h.verifyAssertion(r.Context(), provider.Issuer, idToken, body.Nonce)
	if failure != nil {
		report["ok"] = false
		report["stage"] = "assertion"
		report["error"] = failure.reason
		report["token_status"] = 200
		writeJSON(w, http.StatusOK, report)
		return
	}
	report["token_status"] = 200
	report["id_token_claims"] = assertion.auditDetails()
	writeJSON(w, http.StatusOK, report)
}
