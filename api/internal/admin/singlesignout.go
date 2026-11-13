package admin

// Sequence L — single sign-out with the provider (REQ-AUTH-083, REQ-AUTH-097…099,
// DEV-AUTH-23, Authentication_Authorization_Design.md §2.14).
//
// Two directions that share nothing at run time:
//
//   - L1, CLARA tells the provider: POST /api/v1/auth/oauth/end-session builds the address of
//     the provider's end_session_endpoint for the browser to visit after CLARA's own sign-out
//     (REQ-API-189). It stores nothing and audits nothing — Sequence D's logout call does.
//   - L2, the provider tells CLARA: POST /api/v1/auth/oauth/backchannel-logout receives the
//     provider's logout token, relayed unchanged by the web layer (REQ-API-190, DEV-API-46).
//     The token is the credential, so it is verified here to the standard of an ID token's
//     signature (REQ-AUTH-077) with its own claim rules, and then applied in one transaction.
//
// Both answer 409 single_sign_out_disabled while OIDC_SINGLE_SIGN_OUT is off (REQ-CFG-036).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

const (
	// logoutTokenMaxAge is how old a logout token's `iat` may be (REQ-AUTH-098). It also
	// sizes the replay window: a token older than this is refused by age, so its jti row is
	// no longer needed once iat + this + the skew allowance has passed (REQ-DB-057).
	logoutTokenMaxAge = 10 * time.Minute
	// logoutTokenMaxBytes bounds the token itself. The web layer refuses a larger request
	// body before it calls; this is the same bound for a caller that is not the web layer.
	logoutTokenMaxBytes = 16 << 10
	// backchannelLogoutEvent is the member the `events` claim must carry (OpenID Connect
	// Back-Channel Logout 1.0 §2.4).
	backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"
	// subjectMaxChars bounds `sub` and `jti` as REQ-AUTH-098 states; provider_subject_links
	// stores subjects up to the same length.
	subjectMaxChars = 255
)

// The refusal reasons of a logout token (REQ-AUD-039). They go to the audit entry and the
// API log only: every refusal answers the same 400 invalid_logout_token, because which
// check failed is of use to someone forging tokens and of none to the provider.
const (
	logoutMalformed      = "malformed"
	logoutAlgorithm      = "algorithm"
	logoutUnknownKey     = "unknown_key"
	logoutSignature      = "signature"
	logoutBadClaims      = "claims"
	logoutEvents         = "events"
	logoutNoncePresent   = "nonce_present"
	logoutSubjectMissing = "subject_missing"
	logoutJTIMissing     = "jti_missing"
	logoutReplayed       = "replayed"
)

// errSingleSignOutDisabled is the 409 both endpoints answer with the switch off: neither
// exists then (System_Configuration_Design.md §3.4).
func errSingleSignOutDisabled(w http.ResponseWriter) {
	APIError(w, http.StatusConflict, "single_sign_out_disabled",
		"single sign-out with the provider is not enabled (OIDC_SINGLE_SIGN_OUT)")
}

// oauthEndSession implements POST /api/v1/auth/oauth/end-session (REQ-API-189, Sequence L1
// step 1). The caller is the signed-in user, with the session epoch; the provider is the one
// the session signed in through, which the web layer remembers as `oauth2_provider`.
func (h *Handler) oauthEndSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := actor(r); !ok {
		errForbidden(w)
		return
	}
	if !h.Cfg.OIDCSingleSignOut {
		errSingleSignOutDisabled(w)
		return
	}
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
		h.assertions.forget(provider)
		failure := discoveryFailure(err)
		APIError(w, failure.status, failure.reason, failure.message)
		return
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		errInternal(w)
		return
	}
	address, ok := endSessionURL(runtime.endSession, provider, state)
	if !ok {
		// A document without end_session_endpoint is a provider that cannot take part in
		// RP-initiated logout (PingFederate before 12.0, ASM-AUTH-6). The web layer finishes
		// CLARA's sign-out and tells the user the provider sign-in may still be active.
		slog.Warn("provider names no usable end_session_endpoint", "issuer", provider.Issuer)
		APIError(w, http.StatusBadGateway, "end_session_unsupported",
			"the provider's discovery document names no end_session_endpoint")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"end_session_url": address, "state": state})
}

// endSessionURL appends client_id, post_logout_redirect_uri and state to the provider's
// end_session_endpoint, keeping any query the endpoint already carries (§2.14 L1 step 1).
// There is no id_token_hint: CLARA keeps no ID token (REQ-AUTH-036, DEV-AUTH-23). An empty or
// non-absolute endpoint reports false.
func endSessionURL(endpoint string, provider config.OAuth2Provider, state string) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", false
	}
	q := u.Query()
	q.Set("client_id", provider.ClientID)
	q.Set("post_logout_redirect_uri", provider.PostLogoutRedirectURI)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), true
}

type backchannelLogoutRequest struct {
	Provider    *int   `json:"provider"`
	LogoutToken string `json:"logout_token"`
}

// logoutClaims is what a verified logout token establishes: the account it names and the
// values the replay window needs. No other claim is carried forward (REQ-AUTH-036).
type logoutClaims struct {
	Issuer   string
	Subject  string
	JTI      string
	IssuedAt time.Time
}

// logoutRefusal is a verification outcome other than success: a reason for the audit trail,
// or — when unavailable is set — a provider that could not be asked, which is an outage
// rather than a bad token and answers 502 so the provider retries.
type logoutRefusal struct {
	reason      string
	issuer      string // the payload's `iss`, when the payload could be read at all
	unavailable bool
}

// oauthBackchannelLogout implements POST /api/v1/auth/oauth/backchannel-logout (REQ-API-190,
// Sequence L2 steps 3–5). Pre-authentication: the web layer relays the provider's request
// with the service token and names no user (DEV-API-46).
func (h *Handler) oauthBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	if !h.Cfg.OIDCSingleSignOut {
		errSingleSignOutDisabled(w)
		return
	}
	var body backchannelLogoutRequest
	if err := decodeBody(r, &body); err != nil || body.Provider == nil || body.LogoutToken == "" {
		errBadRequest(w, "provider and logout_token are required")
		return
	}
	provider, ok := h.providerByNumber(*body.Provider)
	if !ok {
		APIError(w, http.StatusBadRequest, "unknown_provider", "this authentication provider is not configured")
		return
	}
	ctx := r.Context()

	claims, refusal := h.verifyLogoutToken(ctx, provider, body.LogoutToken)
	if refusal != nil {
		if refusal.unavailable {
			APIError(w, http.StatusBadGateway, reasonProviderUnavail, "the authentication provider could not be reached")
			return
		}
		h.rejectLogoutToken(ctx, w, provider, refusal.reason, refusal.issuer)
		return
	}

	result, replayed, err := h.applyProviderLogout(ctx, provider, claims, time.Now())
	switch {
	case err != nil:
		// Everything rolled back, the jti included: the provider's retry is accepted.
		slog.Error("provider logout not applied", "provider", provider.N, "error", err)
		errInternal(w)
	case replayed:
		h.rejectLogoutToken(ctx, w, provider, logoutReplayed, claims.Issuer)
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// rejectLogoutToken answers 400 invalid_logout_token and records why (REQ-AUD-039): the
// provider index, the reason and — when the payload could be read — its issuer. Never the
// token, its signature or another claim.
func (h *Handler) rejectLogoutToken(ctx context.Context, w http.ResponseWriter, provider config.OAuth2Provider, reason, issuer string) {
	slog.Warn("provider logout token refused", "provider", provider.N, "reason", reason)
	details := map[string]any{"provider": provider.N, "reason": reason}
	if issuer != "" {
		details["issuer"] = truncateDiagnostic(issuer, 500)
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.ProviderLogoutRejected, Source: audit.SourceSystem, Details: details,
	}); err != nil {
		slog.Error("audit provider_logout_rejected", "error", err)
	}
	APIError(w, http.StatusBadRequest, "invalid_logout_token", "the logout token was not accepted")
}

// verifyLogoutToken runs §2.14 L2 step 3 over one logout token. The order matters where it
// says so: the algorithm is pinned before any key is consulted (REQ-AUTH-077), and the
// claims are read from the payload the signature check returned, never from the bytes parsed
// beforehand.
func (h *Handler) verifyLogoutToken(ctx context.Context, provider config.OAuth2Provider, raw string) (*logoutClaims, *logoutRefusal) {
	// 3.1 — a compact JWS whose header and payload are JSON objects.
	if len(raw) > logoutTokenMaxBytes {
		return nil, &logoutRefusal{reason: logoutMalformed}
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, &logoutRefusal{reason: logoutMalformed}
	}
	var header map[string]json.RawMessage
	if !decodeSegment(parts[0], &header) {
		return nil, &logoutRefusal{reason: logoutMalformed}
	}
	var unverified map[string]json.RawMessage
	if !decodeSegment(parts[1], &unverified) {
		return nil, &logoutRefusal{reason: logoutMalformed}
	}
	// The issuer the payload claims, for the audit entry of a refusal only — it is not yet
	// believed, and a refusal that names it lets an administrator see which provider sent it.
	issuer := stringClaim(unverified, "iss")

	// 3.2 — the pinned algorithm, before a key is looked at; `typ` when present is
	// logout+jwt or JWT (DEV-AUTH-23). An ID token is not stopped here but at 3.5/3.6.
	if stringClaim(header, "alg") != provider.IDTokenAlg {
		return nil, &logoutRefusal{reason: logoutAlgorithm, issuer: issuer}
	}
	if rawTyp, present := header["typ"]; present {
		var typ string
		if json.Unmarshal(rawTyp, &typ) != nil || !acceptedLogoutTyp(typ) {
			return nil, &logoutRefusal{reason: logoutAlgorithm, issuer: issuer}
		}
	}

	// 3.3 — the provider's keys, through Sequence J's cache and its single refetch on an
	// unknown kid.
	runtime, err := h.assertions.runtimeFor(ctx, provider, h.jwksReuse())
	if err != nil {
		h.assertions.forget(provider)
		slog.Warn("provider logout: discovery failed", "provider", provider.N, "error", err)
		return nil, &logoutRefusal{unavailable: true}
	}
	payload, err := runtime.keys.VerifySignature(ctx, raw)
	if err != nil {
		if isTransportFailure(err) {
			h.assertions.forget(provider)
			slog.Warn("provider logout: key set unreachable", "provider", provider.N, "error", err)
			return nil, &logoutRefusal{unavailable: true}
		}
		reason := logoutSignature
		if kid := stringClaim(header, "kid"); kid != "" && !h.keySetNames(ctx, runtime.jwksURI, kid) {
			reason = logoutUnknownKey
		}
		return nil, &logoutRefusal{reason: reason, issuer: issuer}
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil || claims == nil {
		return nil, &logoutRefusal{reason: logoutMalformed, issuer: issuer}
	}
	issuer = stringClaim(claims, "iss")

	// 3.4 — issuer, audience and time.
	now := time.Now()
	iat, iatOK := numericClaim(claims, "iat")
	switch {
	case issuer != provider.Issuer,
		!audienceContains(claims["aud"], provider.ClientID),
		!iatOK,
		iat.After(now.Add(assertionClockSkew)),
		iat.Before(now.Add(-logoutTokenMaxAge)):
		return nil, &logoutRefusal{reason: logoutBadClaims, issuer: issuer}
	}
	if _, present := claims["exp"]; present {
		exp, ok := numericClaim(claims, "exp")
		if !ok || !exp.Add(assertionClockSkew).After(now) {
			return nil, &logoutRefusal{reason: logoutBadClaims, issuer: issuer}
		}
	}

	// 3.5 — the back-channel logout event.
	var events map[string]json.RawMessage
	if rawEvents, present := claims["events"]; !present || json.Unmarshal(rawEvents, &events) != nil || events == nil {
		return nil, &logoutRefusal{reason: logoutEvents, issuer: issuer}
	}
	var event map[string]json.RawMessage
	if rawEvent, present := events[backchannelLogoutEvent]; !present || json.Unmarshal(rawEvent, &event) != nil || event == nil {
		return nil, &logoutRefusal{reason: logoutEvents, issuer: issuer}
	}

	// 3.6 — no nonce, so an ID token can never pass as a logout token.
	if _, present := claims["nonce"]; present {
		return nil, &logoutRefusal{reason: logoutNoncePresent, issuer: issuer}
	}

	// 3.7 and 3.8 — the subject (a sid alone is not enough, DEV-AUTH-23) and the jti.
	subject := stringClaim(claims, "sub")
	if subject == "" || utf8.RuneCountInString(subject) > subjectMaxChars {
		return nil, &logoutRefusal{reason: logoutSubjectMissing, issuer: issuer}
	}
	jti := stringClaim(claims, "jti")
	if jti == "" || utf8.RuneCountInString(jti) > subjectMaxChars {
		return nil, &logoutRefusal{reason: logoutJTIMissing, issuer: issuer}
	}

	return &logoutClaims{Issuer: issuer, Subject: subject, JTI: jti, IssuedAt: iat}, nil
}

// applyProviderLogout runs §2.14 L2 step 4 in one write transaction: the jti enters the
// replay window, and for a linked subject the account's session epoch rises (every CLARA
// session of it ends at its next API call), every client grant it holds ends, and
// provider_logout is written. replayed reports a jti already in the window; nothing is
// written then and the caller audits the refusal.
func (h *Handler) applyProviderLogout(ctx context.Context, provider config.OAuth2Provider, c *logoutClaims, now time.Time) (map[string]any, bool, error) {
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	keyHash := sha256Hex(c.Issuer + "\x00" + c.JTI)
	accepted, err := h.Store.RecordLogoutJTI(ctx, tx, keyHash, c.IssuedAt.Add(logoutTokenMaxAge+assertionClockSkew), now)
	if err != nil {
		return nil, false, err
	}
	if !accepted {
		return nil, true, nil
	}

	userID, err := db.ProviderLinkUserTx(ctx, tx, c.Issuer, c.Subject)
	if err != nil {
		return nil, false, err
	}
	clients := []string{}
	email := ""
	if userID != 0 {
		if err := db.BumpSessionEpoch(ctx, tx, userID); err != nil {
			return nil, false, err
		}
		if clients, err = db.EndSSOGrantsForUserTx(ctx, tx, userID, now); err != nil {
			return nil, false, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
			return nil, false, err
		}
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.ProviderLogout, Source: audit.SourceSystem,
		UserID: userID, Email: email,
		Details: map[string]any{
			"provider":         provider.N,
			"issuer":           c.Issuer,
			"provider_subject": c.Subject,
			"matched":          userID != 0,
			"clients":          clients,
		},
	}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	result := map[string]any{"status": "ok", "matched": userID != 0, "user_id": nil, "clients": clients}
	if userID != 0 {
		result["user_id"] = userID
	}
	slog.Info("provider logout applied", "provider", provider.N, "matched", userID != 0, "clients", len(clients))
	return result, false, nil
}

// decodeSegment reads one base64url JWS segment as a JSON object.
func decodeSegment(segment string, v *map[string]json.RawMessage) bool {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, v) == nil && *v != nil
}

// stringClaim reads a string member; anything else reads as "".
func stringClaim(m map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := m[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// numericClaim reads a NumericDate (seconds since the epoch, RFC 7519 §2).
func numericClaim(m map[string]json.RawMessage, key string) (time.Time, bool) {
	raw, ok := m[key]
	if !ok {
		return time.Time{}, false
	}
	var seconds float64
	if json.Unmarshal(raw, &seconds) != nil {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true
}

// audienceContains reports whether `aud` — a string or a list of strings — names clientID.
func audienceContains(raw json.RawMessage, clientID string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}

// acceptedLogoutTyp reports whether a `typ` header is logout+jwt or JWT, case-insensitive,
// with an optional application/ prefix (§2.14 L2.3.2, DEV-AUTH-23).
func acceptedLogoutTyp(typ string) bool {
	t := strings.ToLower(strings.TrimSpace(typ))
	t = strings.TrimPrefix(t, "application/")
	return t == "logout+jwt" || t == "jwt"
}

// keySetNames reports whether the provider's published key set lists kid. It runs only after
// a signature has failed, to tell an unknown key (a rotation the provider has not published,
// or a token from elsewhere) from a bad signature by a known one in the audit entry. An
// unreadable key set reads as "listed", so the refusal stays `signature`.
func (h *Handler) keySetNames(ctx context.Context, jwksURI, kid string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return true
	}
	req.Header.Set("Accept", "application/json")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, userInfoBodyLimit))
	if err != nil {
		return true
	}
	var set struct {
		Keys []struct {
			KID string `json:"kid"`
		} `json:"keys"`
	}
	if json.NewDecoder(bytes.NewReader(body)).Decode(&set) != nil {
		return true
	}
	for _, k := range set.Keys {
		if k.KID == kid {
			return true
		}
	}
	return false
}
