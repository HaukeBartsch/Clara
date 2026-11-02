package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

// Sequence K — sign-in for registered clients (Authentication_Authorization_Design.md
// §2.13, REQ-AUTH-092…095). CLARA is the sign-in page of another application of
// the installation (FIONA): that application sends the browser to CLARA's
// /sso/authorize, CLARA signs the user in by any of its own paths — local, LDAP,
// OAuth2, with the second factor — and returns the browser with a single-use code.
// The client's server redeems the code at /sso/token for the identity and a grant
// handle, and asks /sso/check whether the grant still stands. The browser never
// carries the identity, and the client never sees a password or a CLARA session.
//
// These are the API halves. The web layer owns the browser side and the public
// routes, and reaches this API the way it always does (GD-1): /codes and /end with
// the acting user of the CLARA session, /token and /check before any user context,
// with the client's own credentials in the body (DEV-API-44).
func (h *Handler) registerSSO(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/sso/codes", h.ssoIssueCode)
	mux.HandleFunc("POST /api/v1/sso/token", h.ssoToken)
	mux.HandleFunc("POST /api/v1/sso/check", h.ssoCheck)
	mux.HandleFunc("POST /api/v1/sso/end", h.ssoEnd)
}

// ssoCodeTTL is how long an authorization code may wait for its redemption. The
// client's server redeems it in the request the browser's return arrives on, so a
// minute is generous; anything longer only widens the window for a leaked code.
const ssoCodeTTL = 60 * time.Second

// ssoDefaultLifetime stands in for SESSION_LIFETIME when the configuration carries
// none (a handler built in a test); config.Load always sets a positive value.
const ssoDefaultLifetime = 8 * time.Hour

var (
	// A PKCE S256 challenge is the unpadded base64url SHA-256 of the verifier:
	// always 43 characters (RFC 7636 §4.2).
	pkceChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	// A PKCE verifier is 43–128 unreserved characters (RFC 7636 §4.1).
	pkceVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

// ssoUser is the identity a client receives: the address is the identity on every
// path (REQ-AUTH-004), the id is stable, the name is for display.
type ssoUser struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

func ssoUserOf(u *db.User) ssoUser {
	return ssoUser{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// pkceMatches reports whether verifier hashes to challenge under S256.
func pkceMatches(verifier, challenge string) bool {
	if !pkceVerifierPattern.MatchString(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// authenticateClient returns the registered client whose id and secret match, or
// nil. The secret comparison is constant-time and runs for an unknown id too, so
// the answer time does not say which ids are registered.
func (h *Handler) authenticateClient(id, secret string) *config.SSOClient {
	c := h.Cfg.SSOClientByID(id)
	want := "\x00no client\x00"
	if c != nil {
		want = c.Secret
	}
	match := subtle.ConstantTimeCompare([]byte(secret), []byte(want)) == 1
	if c == nil || !match || secret == "" {
		return nil
	}
	return c
}

func (h *Handler) ssoLifetime() time.Duration {
	if h.Cfg.SessionLifetime > 0 {
		return time.Duration(h.Cfg.SessionLifetime) * time.Second
	}
	return ssoDefaultLifetime
}

// ssoIssueCode implements POST /api/v1/sso/codes (REQ-API-182): the web layer asks
// for a code for the signed-in user of a CLARA session, naming the client, the
// return address the client registered, the client's PKCE challenge, the session's
// sign-in id, and when that session signed in. The registered address is compared
// exactly — never as a prefix — so a code can only travel to where the operator
// said it may (REQ-AUTH-092).
func (h *Handler) ssoIssueCode(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		ClientID            string `json:"client_id"`
		RedirectURI         string `json:"redirect_uri"`
		CodeChallenge       string `json:"code_challenge"`
		CodeChallengeMethod string `json:"code_challenge_method"`
		SID                 string `json:"sid"`
		SessionIssuedAt     int64  `json:"session_issued_at"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	client := h.Cfg.SSOClientByID(body.ClientID)
	if client == nil {
		APIError(w, http.StatusBadRequest, "invalid_client", "no registered client has this client_id")
		return
	}
	if body.RedirectURI != client.RedirectURI {
		APIError(w, http.StatusBadRequest, "invalid_redirect_uri",
			"redirect_uri is not the address registered for this client")
		return
	}
	if body.CodeChallengeMethod != "S256" || !pkceChallengePattern.MatchString(body.CodeChallenge) {
		errBadRequest(w, "code_challenge must be a PKCE S256 challenge and code_challenge_method S256")
		return
	}
	if len(body.SID) < 32 || len(body.SID) > 256 {
		errBadRequest(w, "sid must be the session's sign-in id (32–256 characters)")
		return
	}
	now := time.Now().UTC()
	issued := time.Unix(body.SessionIssuedAt, 0).UTC()
	if body.SessionIssuedAt <= 0 || issued.After(now.Add(time.Minute)) {
		errBadRequest(w, "session_issued_at must be the session's sign-in time in Unix seconds")
		return
	}
	// The grant ends when the CLARA session it came from would have ended at the
	// latest (REQ-AUTH-094): sign-in + SESSION_LIFETIME.
	expires := issued.Add(h.ssoLifetime())
	if !expires.After(now) {
		errBadRequest(w, "the session's lifetime has ended")
		return
	}

	code, codeHash, err := newSetupToken()
	if err != nil {
		errInternal(w)
		return
	}
	if _, err := h.Store.InsertSSOCode(r.Context(), &db.SSOGrant{
		ClientID:      client.ID,
		UserID:        u.ID,
		SIDHash:       sha256Hex(body.SID),
		CodeHash:      codeHash,
		CodeChallenge: body.CodeChallenge,
		RedirectURI:   client.RedirectURI,
		SessionEpoch:  u.SessionEpoch,
		CodeExpiresAt: now.Add(ssoCodeTTL).Format("2006-01-02 15:04:05"),
		ExpiresAt:     expires.Format("2006-01-02 15:04:05"),
	}, now); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":         code,
		"expires_in":   int(ssoCodeTTL.Seconds()),
		"redirect_uri": client.RedirectURI,
	})
}

// ssoToken implements POST /api/v1/sso/token (REQ-API-183, REQ-AUTH-093): the
// client's server, through the web layer, redeems a code. Every check that can
// refuse runs before the redemption, and a refusal ends the row, so a code that
// failed once cannot be retried with a better guess. A code redeemed twice ends
// the grant the first redemption produced: one of the two holders is not the
// client (RFC 6749 §4.1.2).
func (h *Handler) ssoToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		RedirectURI  string `json:"redirect_uri"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()

	client := h.authenticateClient(body.ClientID, body.ClientSecret)
	if client == nil {
		h.ssoReject(ctx, body.ClientID, nil, "invalid_client")
		APIError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong client secret")
		return
	}
	if body.Code == "" || body.CodeVerifier == "" {
		errBadRequest(w, "code and code_verifier are required")
		return
	}

	g, err := h.Store.GetSSOGrantByCode(ctx, sha256Hex(body.Code))
	if err != nil {
		errInternal(w)
		return
	}
	refuse := func(reason string, u *db.User) {
		if g != nil {
			_ = h.Store.EndSSOGrant(ctx, g.ID, now)
		}
		h.ssoReject(ctx, client.ID, u, reason)
		APIError(w, http.StatusBadRequest, "invalid_grant", "the code is not valid — sign in again")
	}
	if g == nil {
		h.ssoReject(ctx, client.ID, nil, "unknown_code")
		APIError(w, http.StatusBadRequest, "invalid_grant", "the code is not valid — sign in again")
		return
	}
	u, err := h.Store.GetUser(ctx, g.UserID)
	if err != nil {
		errInternal(w)
		return
	}
	switch {
	case g.ClientID != client.ID:
		refuse("wrong_client", u)
		return
	case g.RedeemedAt.Valid:
		refuse("code_reused", u)
		return
	case g.EndedAt.Valid:
		refuse("ended", u)
		return
	case g.CodeExpiresAt < now.Format("2006-01-02 15:04:05"):
		refuse("code_expired", u)
		return
	case body.RedirectURI != g.RedirectURI:
		refuse("redirect_uri_mismatch", u)
		return
	case !pkceMatches(body.CodeVerifier, g.CodeChallenge):
		refuse("pkce_mismatch", u)
		return
	case u == nil:
		refuse("account_not_found", nil)
		return
	}
	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, now)
	if err != nil {
		errInternal(w)
		return
	}
	if state != authz.Active {
		_ = h.Store.EndSSOGrant(ctx, g.ID, now)
		h.ssoReject(ctx, client.ID, u, string(state))
		APIError(w, http.StatusForbidden, string(state), "this account cannot sign in")
		return
	}
	if u.SessionEpoch != g.SessionEpoch {
		// Every CLARA session of the account was ended after the code was issued
		// (REQ-AUTH-074); the code came from one of them.
		refuse("session_revoked", u)
		return
	}

	handle, handleHash, err := newSetupToken()
	if err != nil {
		errInternal(w)
		return
	}
	redeemed, err := h.Store.RedeemSSOCode(ctx, g.ID, handleHash, now)
	if err != nil {
		errInternal(w)
		return
	}
	if !redeemed {
		// A concurrent redemption won between the read and this write.
		refuse("code_reused", u)
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.SSOLogin, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email,
		Details: map[string]any{"client_id": client.ID},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"grant":      handle,
		"expires_in": secondsUntil(g.ExpiresAt, now),
		"user":       ssoUserOf(u),
	})
}

// ssoCheck implements POST /api/v1/sso/check (REQ-API-184, REQ-AUTH-094): whether a
// client's grant still stands. It answers 200 either way — "not active" is the
// answer, not an error — and names why, so the client can tell a sign-out from an
// account an administrator disabled. A grant stands while it was not ended by a
// CLARA sign-out, its CLARA session's lifetime has not run out, the account is
// active, and the account's session epoch is the one the grant was issued under.
func (h *Handler) ssoCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Grant        string `json:"grant"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()

	client := h.authenticateClient(body.ClientID, body.ClientSecret)
	if client == nil {
		h.ssoReject(ctx, body.ClientID, nil, "invalid_client")
		APIError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong client secret")
		return
	}
	inactive := func(reason string) {
		writeJSON(w, http.StatusOK, map[string]any{"active": false, "reason": reason})
	}
	if body.Grant == "" {
		inactive("unknown")
		return
	}
	g, err := h.Store.GetSSOGrantByHandle(ctx, sha256Hex(body.Grant))
	if err != nil {
		errInternal(w)
		return
	}
	switch {
	case g == nil || g.ClientID != client.ID:
		inactive("unknown")
		return
	case g.EndedAt.Valid:
		inactive("ended")
		return
	case g.ExpiresAt <= now.Format("2006-01-02 15:04:05"):
		inactive("expired")
		return
	}
	u, err := h.Store.GetUser(ctx, g.UserID)
	if err != nil {
		errInternal(w)
		return
	}
	if u == nil {
		inactive("unknown")
		return
	}
	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, now)
	if err != nil {
		errInternal(w)
		return
	}
	if state != authz.Active {
		inactive(string(state))
		return
	}
	if u.SessionEpoch != g.SessionEpoch {
		inactive("session_revoked")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active":     true,
		"expires_in": secondsUntil(g.ExpiresAt, now),
		"user":       ssoUserOf(u),
	})
}

// ssoEnd implements POST /api/v1/sso/end (REQ-API-185): the web layer, signing a
// CLARA session out, ends every client grant that session gave (REQ-AUTH-094). The
// clients find out at their next check. The call is idempotent; sso_logout is
// written only when something ended.
func (h *Handler) ssoEnd(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var body struct {
		SID string `json:"sid"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.SID == "" {
		errBadRequest(w, "sid is required")
		return
	}
	ctx := r.Context()
	clients, err := h.Store.EndSSOGrantsForSession(ctx, u.ID, sha256Hex(body.SID), time.Now())
	if err != nil {
		errInternal(w)
		return
	}
	if clients == nil {
		clients = []string{}
	}
	if len(clients) > 0 {
		if err := h.Audit.Insert(ctx, audit.Entry{
			EventType: audit.SSOLogout, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email,
			Details: map[string]any{"clients": clients},
		}); err != nil {
			errInternal(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "clients": clients})
}

// ssoReject writes the sso_rejected entry (REQ-AUD-037): the client id as
// presented, the account when the code named one, and a stable reason — never
// the code, the verifier, the grant handle or the secret (REQ-AUTH-036).
func (h *Handler) ssoReject(ctx context.Context, clientID string, u *db.User, reason string) {
	e := audit.Entry{
		EventType: audit.SSORejected, Source: audit.SourceUI,
		Details: map[string]any{"client_id": truncate(clientID, 64), "reason": reason},
	}
	if u != nil {
		e.UserID, e.Email = u.ID, u.Email
	}
	_ = h.Audit.Insert(ctx, e)
}

// secondsUntil answers the whole seconds from now to a stored UTC datetime, never
// below zero.
func secondsUntil(datetime string, now time.Time) int {
	t, ok := ParseUTCTime(datetime)
	if !ok {
		return 0
	}
	if d := t.Sub(now); d > 0 {
		return int(d.Seconds())
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
