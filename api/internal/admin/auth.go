package admin

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
	"csms/api/internal/passhash"
)

// registerAuth mounts the session endpoints of §4.3: login, the
// side-effect-free verify step of the named-source credential race, and
// logout. These run without an acting user — together with the three
// Sequence H pre-authentication endpoints in passwords.go they form the
// REQ-API-041 exception set (the exemption list lives in httpapi;
// DEV-API-16/17). The API is stateless: no session is created or stored (GD-1).
func (h *Handler) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/verify-password", h.verifyPassword)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("POST /api/v1/auth/session-replay", h.sessionReplay)
	mux.HandleFunc("POST /api/v1/auth/oauth/start", h.oauthStart)
	mux.HandleFunc("POST /api/v1/auth/oauth/test", h.oauthTest)
}

// loginRequest is the §4.3 body: provider only for oauth2/ldap, password
// only for source "local" (GD-18). mfa_code is supplied on the second call
// of a two-factor challenge (REQ-API-114); codes are never logged or audited
// (REQ-AUTH-036, REQ-AUTH-059). source_name is the authentication-source
// name the user selected on the login page — recorded in the audit details
// and nothing else (REQ-AUTH-067). attempts carries the per-source outcomes
// of a failed named-source credential race so the single finalizing call
// yields exactly one login_failure with the losing attempts' detail
// (Audit_Logging_Design.md §3.1, DEV-AUD-5); it never contains credentials.
// first_factor replaces password on the second call of a two-factor challenge:
// the signed proof verify-password issued for an account a second factor still
// guards, so the challenge completes without the password being typed twice or
// stored anywhere (REQ-API-131, firstfactor.go).
type loginRequest struct {
	Email       string            `json:"email"`
	Source      string            `json:"source"`
	Provider    string            `json:"provider"`
	Password    string            `json:"password"`
	MFACode     string            `json:"mfa_code"`
	SourceName  string            `json:"source_name"`
	Attempts    map[string]string `json:"attempts"`
	FirstFactor string            `json:"first_factor"`
	// AssertionNonce is the nonce PHP stored with the state and PKCE verifier.
	// The API exchanges the code and verifies the returned ID token itself.
	AssertionNonce    string `json:"assertion_nonce"`
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

// resolveLoginIdentifier canonicalizes what the credential form was given. An
// address is the identity already and passes through untouched; a login name —
// the part before "@", which the sign-in page accepts beside an address
// (REQ-AUTH-087) — is expanded to the single stored address it belongs to.
//
// Canonicalizing here, before the lockout check and the audit entry, is the
// point: keyed on the raw string a caller typed, `hbar` would be a second
// budget for someone already locked out of `hbar@institution.edu`, and the
// guessing trail would name a spelling instead of an account.
//
// An unknown or ambiguous name resolves to itself, which matches no row, so it
// is refused exactly as an unknown address is — the shorthand adds no way to
// ask whether an account exists (§2.6).
func (h *Handler) resolveLoginIdentifier(ctx context.Context, raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" || strings.Contains(id, "@") {
		return raw
	}
	u, err := h.Store.GetUserByLocalPart(ctx, id)
	if err != nil || u == nil {
		return id
	}
	return u.Email
}

// login implements POST /api/v1/auth/login with the processing order of
// Authentication_Authorization_Design.md §2.3 (Sequence C, step 0 for local).
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	switch body.Source {
	case "oauth2", "ldap", "local":
	default:
		errBadRequest(w, "source must be one of oauth2, ldap, local")
		return
	}
	if body.Source != "oauth2" && strings.TrimSpace(body.Email) == "" {
		errBadRequest(w, "email is required")
		return
	}
	ctx := r.Context()

	// The credential form accepts a login name in place of an address
	// (REQ-AUTH-087). Expand it before anything keys on it — the lockout below,
	// the audit entry, the account lookup — so the shorthand is one identity and
	// not a spare guessing budget.
	if body.Source != "oauth2" && body.Email != "" {
		body.Email = h.resolveLoginIdentifier(ctx, body.Email)
	}

	// A browser login knows its email before credential verification on local and
	// LDAP paths. For OIDC, only the verified ID token supplies the identity, so its
	// address lockout check follows provider verification rather than trusting a
	// caller-supplied email.
	if body.Source != "oauth2" && body.Email != "" && h.lockouts.locked(body.Email, time.Now()) {
		h.loginFailure(ctx, &body, "rate_limited")
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed attempts for this address — try again later")
		return
	}

	// Sequence J (REQ-AUTH-076, DEV-AUTH-18): on the oauth2 leg the provider's own
	// signature is satisfied first. The order is the point — no user row is read, no
	// active check runs and no session material is issued on an unproven assertion,
	// which is what "PHP vouches for it" used to mean (finding F8).
	var assertion *verifiedAssertion
	if body.Source == "oauth2" {
		token, failure := h.exchangeOAuthCode(r, body.Provider, body.AuthorizationCode, body.CodeVerifier)
		if failure != nil {
			h.loginFailureExtra(ctx, &body, failure.reason, nil)
			APIError(w, failure.status, failure.reason, failure.message)
			return
		}
		a, af := h.verifyAssertion(ctx, body.Provider, idTokenOf(token), body.AssertionNonce, token.AccessToken)
		if af != nil {
			h.loginFailureExtra(ctx, &body, af.reason, af.observed.auditDetails())
			APIError(w, af.status, af.reason, af.message)
			return
		}
		assertion = a
		body.Email = assertion.Email
	}
	if body.Email != "" && h.lockouts.locked(body.Email, time.Now()) {
		h.loginFailure(ctx, &body, "rate_limited")
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed attempts for this address — try again later")
		return
	}

	// The address a provider login is judged by is the one its assertion verified, not
	// the string that arrived beside it. DEV-AUTH-15 narrowed bootstrap promotion
	// precisely because an IdP-controlled mail attribute could name the administrator,
	// and re-opening that through a body field would be a poor way to spend a signature.
	bootstrapEmail := body.Email
	if assertion != nil {
		bootstrapEmail = assertion.Email
	}

	// Step 8 (REQ-AUTH-080): the account is the one this provider subject is bound to;
	// only when no such binding exists does the address get consulted. Preferring the
	// binding over the address is what stops a mail attribute edited or reassigned at
	// the identity provider from moving an account into different hands.
	var (
		u             *db.User
		resolvedBySub bool
	)
	if assertion != nil {
		link, err := h.Store.GetProviderLinkBySubject(ctx, assertion.Issuer, assertion.Subject)
		if err != nil {
			errInternal(w)
			return
		}
		if link != nil {
			u, err = h.Store.GetUser(ctx, link.UserID)
			if err != nil {
				errInternal(w)
				return
			}
			if u == nil {
				// The binding names an account that no longer exists. Falling back to the
				// address would give this provider identity to whoever holds the mailbox
				// now, so it fails closed and stays an administrator's decision.
				h.loginFailureExtra(ctx, &body, "account_not_found", assertion.auditDetails())
				APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this identity")
				return
			}
			resolvedBySub = true
		}
	}
	if !resolvedBySub {
		var err error
		u, err = h.Store.GetUserByEmail(ctx, body.Email)
		if err != nil {
			errInternal(w)
			return
		}
	}

	// A call carrying attempts is the record of a *failed* named-source
	// credential race (Authentication_Authorization_Design.md §2.9): it exists to
	// write the submission's one login_failure and must never authenticate
	// (REQ-API-135). Falling through would be worst exactly where attempts
	// matter — for source "ldap" the API trusts PHP's word, so a race that
	// reached no directory and bound nobody would stamp last_login_at and return
	// a user object on an unverified first factor, or open a two-factor
	// challenge nobody earned at step 1.5.
	if len(body.Attempts) > 0 {
		h.failedRace(ctx, w, &body, u)
		return
	}

	// Step 0 (GD-18, REQ-AUTH-050): local hash check. A missing hash and a
	// mismatch are indistinguishable to the caller; the argon2id comparison is
	// constant-time per hash. A first_factor handle from verify-password stands
	// in for the password only where a second factor still guards the account,
	// so it can never complete a login by itself (REQ-API-131).
	if body.Source == "local" {
		if u == nil {
			h.loginFailure(ctx, &body, "account_not_found")
			APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this email")
			return
		}
		switch {
		case body.FirstFactor != "":
			guarded, err := h.secondFactorGuards(ctx, u)
			if err != nil {
				errInternal(w)
				return
			}
			if !guarded || !h.acceptFirstFactor(body.FirstFactor, u, time.Now()) {
				// Unknown format, wrong signature, wrong account, expired, or
				// presented where no second factor guards the account: one
				// answer for all of them — sign in again.
				h.loginFailure(ctx, &body, "first_factor_expired")
				APIError(w, http.StatusUnauthorized, "first_factor_expired",
					"the first-factor proof is no longer valid — sign in again")
				return
			}
		case !u.PasswordHash.Valid || !passhash.Verify(u.PasswordHash.String, body.Password):
			h.loginFailure(ctx, &body, "bad_credentials")
			APIError(w, http.StatusUnauthorized, "bad_password", "invalid email or password")
			return
		}
	} else if u == nil && !h.isBootstrapEmail(bootstrapEmail) {
		h.loginFailure(ctx, &body, "account_not_found")
		APIError(w, http.StatusUnauthorized, "account_not_found", "no account for this email")
		return
	}

	// A binding for a different subject on the account the address names is two
	// identities claiming one account. REQ-AUTH-080 refuses to guess which one a
	// directory edit meant: not a silent rebind, and not a silent lockout of whoever
	// was there first — an audit entry an administrator resolves.
	if assertion != nil && !resolvedBySub && u != nil {
		links, err := h.Store.ListProviderLinksByUser(ctx, u.ID)
		if err != nil {
			errInternal(w)
			return
		}
		for _, l := range links {
			if l.Issuer == assertion.Issuer && l.Subject != assertion.Subject {
				h.loginFailureExtra(ctx, &body, "subject_conflict", assertion.auditDetails())
				APIError(w, http.StatusForbidden, "subject_conflict",
					"this account is already bound to a different identity at that provider")
				return
			}
		}
	}

	// Step 2 (REQ-AUTH-007, DEV-AUTH-15): bootstrap promotion, narrowed — a
	// login creates the row only in the first-installation case, never
	// re-enables a disabled account, and promotes an existing enabled row only
	// for a local login or while no other enabled administrator exists. The
	// startup ensure (cmd/server) stays the operator's setup path. A provider
	// login whose subject is already bound to an account is not a first
	// installation for that person, whatever address it names (REQ-AUTH-080).
	if h.isBootstrapEmail(bootstrapEmail) && !resolvedBySub {
		enabledAdmins, err := h.Store.CountEnabledAdmins(ctx)
		if err != nil {
			errInternal(w)
			return
		}
		promote := body.Source == "local" || enabledAdmins == 0
		if _, err := h.Store.EnsureBootstrapForLogin(ctx, bootstrapEmail, "Administrator", promote); err != nil {
			errInternal(w)
			return
		}
		if u, err = h.Store.GetUserByEmail(ctx, bootstrapEmail); err != nil || u == nil {
			errInternal(w)
			return
		}
	}

	// Step 1 (Authentication_Authorization_Design.md §4.4): account-active
	// rule; the inactivity case has already written account_auto_disabled
	// inside CheckActive — do not double-write it here (REQ-AUTH-053).
	now := time.Now().UTC()
	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, now)
	if err != nil {
		errInternal(w)
		return
	}
	switch state {
	case authz.AccountExpired:
		h.loginFailure(ctx, &body, "account_expired")
		APIError(w, http.StatusForbidden, "account_expired", "this account has expired")
		return
	case authz.AccountDisabled:
		h.loginFailure(ctx, &body, "account_disabled")
		APIError(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}

	// Step 1.5 (Sequence G, GD-21): the second factor guards local and LDAP
	// logins; the OAuth2 path is exempt because the identity provider owns
	// its own second factor.
	mfaFactor := ""
	if body.Source != "oauth2" {
		var proceed bool
		_, mfaFactor, proceed = h.secondFactor(w, r, &body, u)
		if !proceed {
			return
		}
	}

	// Steps 3–5: stamp auth_source + last_login_at (REQ-AUTH-005), audit
	// login_success with the source (§3.1), respond with the user object.
	h.lockouts.success(body.Email) // a success clears the counter (§2.5)
	if err := h.Store.TouchLastLogin(ctx, u.ID, body.Source); err != nil {
		errInternal(w)
		return
	}
	if assertion != nil {
		// First verified login for this (iss, sub): record the binding, so the next one
		// resolves by subject and an address edited at the provider can no longer move
		// the account (REQ-AUTH-080). It is idempotent — two concurrent logins of one
		// person write one row. A failure here does not take back a login the assertion
		// already satisfied; the binding is retried on the next sign-in, so it is logged
		// and the request goes through.
		if err := h.Store.EnsureProviderLink(ctx, assertion.Issuer, assertion.Subject, u.ID, now); err != nil {
			slog.Error("provider subject link not recorded",
				"issuer", assertion.Issuer, "user_id", u.ID, "err", err)
		}
		// Step 3.5 (REQ-AUTH-088): the role values this sign-in carried, and the automatic
		// region they map to. Never a reason to refuse the login (§2.12).
		h.applyProviderRegion(ctx, assertion, u, now)
	}
	u.LastLoginAt = sql.NullString{String: now.Format("2006-01-02 15:04:05"), Valid: true}
	u.AuthSource = body.Source
	details := map[string]any{"source": body.Source, "display_name": u.DisplayName}
	if body.Provider != "" {
		details["provider"] = body.Provider
	}
	if body.SourceName != "" {
		details["source_name"] = body.SourceName // REQ-AUTH-067
	}
	if mfaFactor != "" {
		details["mfa"] = mfaFactor // totp | email | recovery (Audit_Logging_Design.md §3.1)
	}
	if assertion != nil {
		// REQ-AUTH-084: the issuer, the provider subject, the assurance claims seen and
		// whether the address was verified — and no token, signature or other claim
		// material beside it (REQ-AUTH-036).
		for k, v := range assertion.auditDetails() {
			details[k] = v
		}
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginSuccess, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: details,
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.userObject(ctx, u, now)) // tfa_method per REQ-API-116
}

// isBootstrapEmail reports whether the email equals the configured
// ADMIN_BOOTSTRAP_EMAIL (REQ-AUTH-007); unset config never matches.
func (h *Handler) isBootstrapEmail(email string) bool {
	return h.Cfg.AdminBootstrapEmail != "" && email == h.Cfg.AdminBootstrapEmail
}

// verifyPassword implements POST /api/v1/auth/verify-password (REQ-API-123,
// DEV-API-17): the side-effect-free verify step of the named-source
// credential race (Authentication_Authorization_Design.md §2.9). It runs
// Sequence C steps 0–1 only — the argon2id hash check and the account-active
// rule — and answers ok / bad_password / account_disabled / account_expired.
// No last_login_at or auth_source write and no user object: that is what
// makes it safe to fire alongside the LDAP binds of a race, with login
// remaining the single place login side effects happen (REQ-AUTH-065).
// An unknown account and a wrong password are not distinguished (§2.6).
// The brute-force rule (Sequence E) is enforced here, not delegated to PHP
// (security finding F1): a locked address is rejected before any credential
// probing, every failed attempt counts toward the shared lockout, and each
// rejection writes the login_failure entry the guessing trail needs — the
// same budget login spends, so splitting the flow across the two endpoints
// cannot double it. The password is never logged (REQ-AUTH-036).
// For an account a second factor still guards the ok answer also
// carries a first-factor handle — the proof the challenge's second login call
// presents in place of the password, so no layer has to hold one (REQ-API-131).
func (h *Handler) verifyPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if strings.TrimSpace(body.Email) == "" {
		errBadRequest(w, "email is required")
		return
	}
	ctx := r.Context()

	// Same canonicalization as login (REQ-AUTH-087): this endpoint carries the
	// lockout for the credential race, so a login name has to reach it already
	// expanded to the address it is short for.
	body.Email = h.resolveLoginIdentifier(ctx, body.Email)

	// Sequence E before any credential probing (security finding F1): the
	// web login fires this endpoint first, so without the check here an
	// attacker could guess indefinitely without ever reaching login's lock.
	if h.lockouts.locked(body.Email, time.Now()) {
		h.verifyFailure(ctx, body.Email, "rate_limited")
		APIError(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed attempts for this address — try again later")
		return
	}

	u, err := h.Store.GetUserByEmail(ctx, body.Email)
	if err != nil {
		errInternal(w)
		return
	}
	if u == nil || !u.PasswordHash.Valid || !passhash.Verify(u.PasswordHash.String, body.Password) {
		// Count toward the lock and leave the audit trail (security finding
		// F1); an unknown account counts identically, so neither outcome is
		// distinguishable (§2.6). A rate_limited rejection never counts —
		// a locked address must not extend its own lockout (REQ-AUTH-035).
		h.lockouts.failure(body.Email, time.Now())
		h.verifyFailure(ctx, body.Email, "bad_password")
		APIError(w, http.StatusUnauthorized, "bad_password", "invalid email or password")
		return
	}

	state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, time.Now())
	if err != nil {
		errInternal(w)
		return
	}
	switch state {
	case authz.AccountExpired:
		APIError(w, http.StatusForbidden, "account_expired", "this account has expired")
		return
	case authz.AccountDisabled:
		APIError(w, http.StatusForbidden, "account_disabled", "this account is disabled")
		return
	}

	resp := map[string]any{"status": "ok"}

	// An account a second factor still guards gets back the proof that lets the
	// challenge's second call carry no password (REQ-API-131). None is issued
	// otherwise: there is no challenge to complete, and login would refuse it —
	// an unguarded account authenticates with its password alone.
	guarded, err := h.secondFactorGuards(ctx, u)
	if err != nil {
		errInternal(w)
		return
	}
	if guarded {
		if handle := h.issueFirstFactor(u, time.Now()); handle != "" {
			resp["first_factor"] = handle
			resp["expires_in"] = int(firstFactorTTL.Seconds())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// verifyFailure writes the login_failure entry for a rejected
// verify-password attempt (security finding F1): the guessing trail lives in
// the audit log, so the verify leg of the race can no longer be hammered
// without a trace. It only audits — the lockout counter is recorded by the
// caller exactly where the attempt is a credential failure, never for a
// rate_limited rejection (REQ-AUTH-035). Reason vocabulary per §3.1; never
// the password (REQ-AUTH-036).
func (h *Handler) verifyFailure(ctx context.Context, email, reason string) {
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginFailure, Source: audit.SourceUI,
		Email:   email,
		Details: map[string]any{"source": "local", "email": email, "reason": reason},
	})
}

// loginFailure writes the login_failure entry (§3.1): source, email and a
// stable reason — never the password (REQ-AUTH-036). The rejection response
// is written by the caller; an audit write failure does not mask it. For a
// failed named-source credential race the finalizing call carries the
// per-source outcomes in attempts: they ride along in details, and a plain
// credential rejection settles as bad_credentials — or provider_unavailable
// when no source could be reached (Audit_Logging_Design.md §3.1, DEV-AUD-5).
// Account-state rejections keep their specific reason (REQ-AUTH-065).
func (h *Handler) loginFailure(ctx context.Context, body *loginRequest, reason string) {
	h.loginFailureExtra(ctx, body, reason, nil)
}

// providerSideReasons are refusals that say something about the installation or the
// identity provider rather than about the person trying to sign in: an unreachable
// provider, a discovery document whose issuer disagrees with the configured one, an
// issuer nobody configured. None of them spends the address's brute-force budget
// (REQ-AUTH-035) — an outage at the IdP must not lock every one of its users out of
// CLARA on top of locking them out of everything else.
var providerSideReasons = map[string]bool{
	"rate_limited":         true,
	"first_factor_expired": true,
	reasonProviderUnavail:  true,
	reasonIssuerMismatch:   true,
	reasonUnknownProvider:  true,
}

// loginFailureExtra records a refused login with details beyond the standard set.
// `extra` carries what Sequence J established when it has established anything —
// issuer, subject, assurance claims observed (REQ-AUTH-084) — and never token
// material, which is why the assertion type does not hold any to begin with
// (REQ-AUTH-036).
func (h *Handler) loginFailureExtra(ctx context.Context, body *loginRequest, reason string, extra map[string]any) {
	// The lockout rejection must not extend itself, and neither does a stale
	// first-factor handle: taking a minute over a code is not a credential
	// attempt, and it must not spend the address's lockout budget (REQ-AUTH-035).
	if !providerSideReasons[reason] {
		h.lockouts.failure(body.Email, time.Now())
	}
	if len(body.Attempts) > 0 {
		switch reason {
		case "bad_password", "account_not_found":
			// A failed race is one credential failure, not one per losing
			// attempt — or an outage, when no source could be reached at all.
			reason = "bad_credentials"
			if allAttemptsUnreachable(body.Attempts) {
				reason = "provider_unavailable"
			}
		}
	}
	details := map[string]any{"source": body.Source, "email": body.Email, "reason": reason}
	if body.SourceName != "" {
		details["source_name"] = body.SourceName // REQ-AUTH-067
	}
	if len(body.Attempts) > 0 {
		details["attempts"] = body.Attempts
	}
	for k, v := range extra {
		details[k] = v
	}
	_ = h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.LoginFailure, Source: audit.SourceUI,
		Email:   body.Email,
		Details: details,
	})
}

// failedRace records the single login_failure of a named-source credential race
// whose attempts all settled as failed, and answers the rejection the login page
// shows (REQ-API-135, Authentication_Authorization_Design.md §2.9 "All failed").
// It authenticates nothing by construction: no hash check, no reading of the
// password attribute, no last_login_at or auth_source write, no bootstrap
// promotion, and no arrival at the two-factor gate — a first factor nobody
// verified must not open a challenge (REQ-AUTH-055).
//
// Two things still distinguish themselves from the generic credential line. An
// account that is disabled or expired says so, because there the account rather
// than the password is the problem (§2.6 step 4). And an attempt set in which
// every source was unreachable answers provider_unavailable, so the page reports
// an outage instead of implying the user mistyped something (§2.9 step 5).
func (h *Handler) failedRace(ctx context.Context, w http.ResponseWriter, body *loginRequest, u *db.User) {
	if u != nil {
		state, err := authz.CheckActive(ctx, h.Store, h.Audit, h.Cfg, u, time.Now().UTC())
		switch {
		case err != nil:
			errInternal(w)
			return
		case state == authz.AccountExpired:
			h.loginFailure(ctx, body, "account_expired")
			APIError(w, http.StatusForbidden, "account_expired", "this account has expired")
			return
		case state == authz.AccountDisabled:
			h.loginFailure(ctx, body, "account_disabled")
			APIError(w, http.StatusForbidden, "account_disabled", "this account is disabled")
			return
		}
	}

	if allAttemptsUnreachable(body.Attempts) {
		// The audit reason comes from the attempts map (DEV-AUD-5); the response
		// code is the one the web layer maps to "the authentication service is
		// unavailable".
		h.loginFailure(ctx, body, "bad_password")
		APIError(w, http.StatusUnauthorized, "provider_unavailable",
			"the authentication service could not be reached")
		return
	}

	h.loginFailure(ctx, body, "bad_password")
	APIError(w, http.StatusUnauthorized, "bad_password", "invalid email or password")
}

// allAttemptsUnreachable reports whether every attempt of a race settled as
// unreachable — nothing could be contacted, which is an outage rather than a
// wrong password (Audit_Logging_Design.md §3.1). An empty map answers false: that
// is a plain credential rejection, not a race that failed to run.
func allAttemptsUnreachable(attempts map[string]string) bool {
	if len(attempts) == 0 {
		return false
	}
	for _, outcome := range attempts {
		if outcome != "unreachable" {
			return false
		}
	}

	return true
}

// logout implements POST /api/v1/auth/logout (REQ-AUTH-008/015): it records
// the logout event — the PHP layer destroys the session after this call —
// and stores no state itself (GD-1).
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	if err := h.Audit.Insert(r.Context(), audit.Entry{
		EventType: audit.Logout, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, Details: map[string]any{},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sessionReplay implements POST /api/v1/auth/session-replay (REQ-API-148,
// REQ-AUTH-073). The web layer rotates a session's id every
// SESSION_ROTATE_INTERVAL and keeps the old id only as a pointer to its
// successor; when that old id comes back after the grace window, two parties
// held the same session and one of them should not have. Nothing tells the
// API which one, so it ends every session of the acting account by raising
// its epoch (REQ-AUTH-074) and records session_replay_detected with the
// address that presented the old id. Like logout it keeps no state of its own
// (GD-1): the epoch lives on the users row.
func (h *Handler) sessionReplay(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	ctx := r.Context()
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.BumpSessionEpoch(ctx, tx, u.ID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.SessionReplayDetected, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email,
		// Never the session id: the old one is dead, the new one is not ours to log.
		Details: map[string]any{"ip": dataapi.SourceIP(h.Cfg, r)},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
