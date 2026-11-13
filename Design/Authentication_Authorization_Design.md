# Authentication & Authorization — Design

**Project:** CLARA — Clinical Logbook for Automated Research Assistance (REDCap API Replacement)\
**Implements:** `Requirements/Authentication_Authorization_Requirements.md`\
**Created:** 2026-09-21

## 1. Trust Model

### 1.1 Actors and surfaces

| Actor | Surface | Credential | Grants |
|---|---|---|---|
| browser (member) | PHP routes only (REQ-AUTH-010) | session cookie (§3) | the member's effective permissions |
| PHP web application | `/api/v1/*` (internal path only) | `X-Internal-Service-Token` + `X-Internal-User-Id` (REQ-AUTH-011) | exactly the acting user's permissions — PHP adds none |
| external data-API caller (Fiona/RIS) | `POST /api/` (public) | project token as `token` parameter (REQ-AUTH-031) | the token holder's per-arm and per-pair levels (REQ-AUTH-033/069) |
| survey respondent | PHP route `/s/{link}` (public, no session — GD-9) | opaque link token | fill-only on exactly one (record, survey instrument) (REQ-AUTH-039) |
| OAuth2 / OIDC provider | outbound from the Go API (discovery, code exchange, userinfo, keys — REQ-AUTH-086) | OAuth2 client secret | signed ID token, verified by the API (Sequence J, §2.10) |
| LDAP server | outbound from PHP | LDAP bind DN / the user's password | identity (email) after a successful bind |
| local (table-based) account | PHP login form → API | email + password (verified against `users.password_hash`), plus the configured second factor when enrolled (GD-21, §2.7) | the account's normal permissions (GD-18, REQ-AUTH-050) |
| registered sign-in client (FIONA's server, REQ-AUTH-092) | PHP routes `POST /sso/token` and `POST /sso/check` (public, no session — DEV-TECH-3); its user's browser visits `/sso/authorize` and `/sso/logout` | client secret, plus a single-use code and its PKCE verifier; afterwards an opaque grant | who the user is (`id`, `email`, `display_name`) and whether the CLARA sign-in behind the grant still stands — no permission in CLARA (Sequence K, §2.13) |
| invited / resetting person (pre-authentication) | PHP set-password page via the emailed link (public route, no session — GD-22/GD-23) | single-use hashed token (`password_tokens`, REQ-DB-039) | exactly one thing: set the password of the one addressed account (§2.8) — nothing else is reachable |

The second factor (GD-21) guards the two CLARA-verified rows above — local and LDAP form logins — after first-factor success; the OAuth2 row is exempt (MFA there is the provider's responsibility, DEV-AUTH-10), as are survey links and data-API tokens (§4.3).

### 1.2 Network topology (normative rules)

```
public internet
   │  TLS termination
   ▼
reverse proxy (nginx / Apache)
   ├─ PHP routes            ──► PHP-FPM ──► Go API  (/api/v1/*, loopback)
   ├─ POST /api/            ─────────────► Go API  (data API, REDCap protocol)
   └─ /api/v1/*, /docs      ── NOT routable externally (REQ-TECH-018, REQ-AUTH-014)
Go API ──► SQLite / MariaDB
```

- `/api/v1/*` is reachable only from the trusted internal path (REQ-AUTH-014, REQ-API-040); the proxy strips `X-Internal-Service-Token`, `X-Internal-User-Id` and `X-Internal-Session-Epoch` from every externally-originated request (rules fixed in `Technology_Stack_Design.md` §5).
- Inter-component traffic is loopback or TLS (REQ-AUTH-034).
- The browser never calls `/api/v1/*` directly (GD-1, REQ-AUTH-010, REQ-UI-002).
- The reference proxy access logs omit query strings and referrers (INSTALL.md §11.2/§11.3, REQ-AUTH-049), so the OAuth authorization code returned to `/auth/callback` is not logged. The data API reads its token from the body only (REQ-API-010, DEV-API-30), and the invite/reset link `/set-password?token=…` remains a GET whose token the web server MUST NOT log.

> **Key constraints** — a summary for quick orientation; the cited IDs and sections are normative.
>
> - **PHP adds no permissions.** The API applies exactly the acting user's effective permissions on every call (§1.1, REQ-AUTH-013).
> - **Identity rests on different proofs per source.** OAuth2: the API verifies the provider's ID token itself (Sequence J). Local: the API checks the stored hash. LDAP: the API trusts PHP's word after a successful bind (§2.3).
> - **`/api/v1/*` is internal.** The proxy strips the `X-Internal-*` headers from external requests and never routes `/api/v1/*` publicly (§1.2).
> - **The API keeps no session state.** Sessions live in PHP; raising the account's session epoch is how the API ends them (§3, REQ-AUTH-074).

## 2. Authentication Sequences

Every login starts with the user selecting an authentication source **by name** on the login page (Sequence I, §2.9 — REQ-AUTH-063); the sequences below describe what happens inside and after that selection.

### 2.1 Sequence A — OAuth2 authorization code (REQ-AUTH-001/002/066)

An OAuth2 source joins login only through a browser round-trip, never the credential race (§2.9): selecting a name mapped to exactly one OAuth2 source starts this sequence immediately; with several providers under the name, the user picks which one to complete (REQ-AUTH-066).

1. Browser opens `/login`, where PHP lists the distinct configured source names (§2.9); the user selects a name whose set contains the OAuth2 provider. PHP asks the API to start the round trip (`POST /api/v1/auth/oauth/start` — service-token guarded, no acting user, `httpapi.preAuthPaths`). The API resolves the provider's discovery document (§2.10, REQ-AUTH-082) and returns an `authorize_url` carrying `response_type=code`, `client_id`, `redirect_uri = WEB_PUBLIC_URL + /auth/callback`, `state`, `code_challenge` (method S256) and a `nonce`, together with the `state`, `code_verifier` and `nonce` themselves. PHP stores all three in the session beside the provider index and the selected name (`auth_source_name`, §3) as one single-use transaction, then redirects to that URL. The nonce ties the assertion to this browser rather than merely to this session (REQ-AUTH-079); generating it and the challenge in Go is what makes the API the component that satisfies itself (REQ-AUTH-086), and the client secret never enters PHP at all (REQ-CFG-021).
2. The user authenticates at the IdP; the provider redirects the browser to `/auth/callback?code&state`. With `OAUTH2_N_RESPONSE_MODE=form_post` (REQ-CFG-041) the authorization request carried `response_mode=form_post` — step 1 refused the start with `response_mode_unsupported` when discovery does not list it — and the provider instead returns an auto-submitting form that POSTs `code` and `state` to `/auth/callback`.
3a. **form_post handoff.** That POST is cross-site, so the browser sends no `SameSite=Lax` session cookie with it and it carries no CSRF token. `POST /auth/callback` is therefore a `'session' => false` route without a CSRF check (DEV-TECH-4: `state` and PKCE bind the response, as on the GET), open only while some provider is configured for `form_post` (otherwise 405). It requires exactly one `state` and exactly one `code` or `error` in a body of at most 16 KiB, counted on the raw body because `$_POST` keeps the last of two equal names. It verifies nothing and calls nothing: it writes `{at, code, state, error}` to `SESSION_DIR/clara_handoff_<sha256(handle)>` (mode `0600`; expired records are swept on each write) under a fresh 32-byte random handle and answers `303` to `GET /auth/callback?handoff=<handle>` with `Referrer-Policy: no-referrer`. A malformed response gets the empty handle. That GET is a top-level navigation, which carries the session cookie; PHP redeems the handle once — an atomic rename claims the record before it is read — within 60 seconds, ignores any `code`/`state` in the query beside it, and continues at step 3 with the parked values. A handle that redeems nothing (unknown, used, expired, empty) is treated as a provider error callback. The code never appears in a URL; a handle alone is worth nothing without the browser session whose transaction issued its `state`. `GET /auth/callback?code&state` keeps working for every provider, whatever its response mode.
3. PHP verifies `state` against the session value (**single use**; mismatch → abort, no session, `login_failure` with reason `state_mismatch`). A provider error callback (`access_denied` and friends) consumes the transaction the same way and returns to the form with no login call at all.
4. PHP calls the API's login endpoint (Sequence C) with `source:"oauth2"`, the provider's issuer, and the transaction's values as `authorization_code`, `code_verifier` and `assertion_nonce`. It asserts no identity of its own, because it reads none: the address arrives out of the verified token, never out of this call (REQ-AUTH-076, DEV-AUTH-18).
5. The API redeems `code` + `code_verifier` with the configured client credentials at the discovery document's `token` endpoint — server-side, over TLS — and verifies the ID token that comes back by §2.10 **before resolving anyone**: signature under the pinned algorithm, `iss`/`aud`/`exp`, the nonce, the assurance gate, and the address attestation, with email read via `OAUTH2_N_EMAIL_ATTR` and subject via `OAUTH2_N_SUBJECT_ATTR` (REQ-AUTH-001/004/076/077).
6. The API resolves the account by its (`iss`, `sub`) link, falling back to the verified address on a first verified login (§2.10 step 8, REQ-AUTH-080), and answers Sequence C as for any other source. The address is judged by the response that supplies it: the ID token's own claims first, and when it names none for `OAUTH2_N_EMAIL_ATTR`, the `userinfo` endpoint this provider's discovery document named — read with the access token from this same exchange, since PingFederate configures the ID-token release and `/idp/userinfo.openid` separately and a client that releases `email` only to userinfo is a working deployment (REQ-AUTH-078). A userinfo answer about a subject other than the verified token's is refused outright; one that cannot be reached reports `provider_unavailable` and spends none of the address's lockout budget.
7. On success PHP establishes the session (§3) and redirects to `/`.

Any failure at steps 3–6 does **not** silently move on to other sources: it is audit-logged (`login_failure`, `Audit_Logging_Design.md` §3.1) and the user returns to the login page (where the same name's credential form remains available if that name's set also contains credential sources — REQ-AUTH-066). A failure at steps 5–6 names which check refused — `assertion_invalid` (including an `iss`/`aud`/`exp` failure), `nonce_mismatch`, `email_unverified`, `assurance_insufficient`, `subject_conflict` — or which provider fault stopped it — `provider_issuer_mismatch`, `provider_unavailable` — so an operator can tell a rejected proof from an unavailable provider (REQ-AUTH-084; status codes in `API_Endpoints_Design.md` §4.3). A failure at step 1 — the start itself, before the browser leaves — stores no transaction and clears the selected name (`auth_source_name`, §3), so a name mapped to one provider does not restart the failing start on every visit to `/login`; the page that reports it leads back to the picker (`User_Interface_Design.md` §2.2).

### 2.2 Sequence B — LDAP attempt (sources under the selected name, REQ-AUTH-003/065)

The PHP login form carries email + password for the **local and LDAP** paths; on the LDAP path the password is sent to the directory and never stored or logged (REQ-AUTH-036). All LDAP sources registered under the selected name are attempted **in parallel** with the local check of Sequence F — not sequentially, and not after it has failed (DEV-AUTH-14). For every LDAP source `N` in the selected set, concurrently:

1. **Search** (using `LDAP_SERVER_N_BIND_DN`/`BIND_PASSWORD`, or anonymous): find the entry in `SEARCH_BASE` whose `UID_ATTR` matches the login name; read `EMAIL_ATTR` and `NAME_ATTR` (REQ-CFG-012).
2. **Bind-as-user** (ASM-AUTH-2): simple bind with the entry DN and the supplied password. Success → identity is the entry's email (REQ-AUTH-004); a bind failure or per-source connect timeout settles that attempt as failed — other attempts continue unaffected.
3. On the **first** successful server (first "login ok" wins, REQ-AUTH-065): PHP calls the API's login endpoint (Sequence C) with `source:"ldap"`, `provider:"ldap-N"`, and the identity; any later success from another source is discarded (§2.9 step 4).
4. All attempts in the set settle as failed → `login_failure` (reason `bad_credentials` when an entry was found, `provider_unavailable` when unreachable) per §2.9 step 5.

### 2.3 Sequence C — API login call (REQ-AUTH-016, REQ-API-044)

`POST /api/v1/auth/login` — one of the eight administration endpoints exempt from `X-Internal-User-Id` (it establishes the user context; the other seven are the three pre-authentication password endpoints of Sequence H, §2.8, the pre-authentication `POST /api/v1/auth/verify-password` of Sequence I, §2.9, `POST /api/v1/auth/oauth/start` of Sequence A, §2.1, and `POST /api/v1/sso/token` and `POST /api/v1/sso/check` of Sequence K, §2.13 — DEV-API-16/17/32/44; the code's list is `httpapi.preAuthPaths`); it still requires a valid `X-Internal-Service-Token` and is internal-only per §1.2. `provider` names the concrete winning source (`"ldap-N"`, the provider issuer URL, or the local source id) and PHP passes the user-selected name through in the audit details (REQ-AUTH-067). Request body:

```json
{ "source": "oauth2", "provider": "https://idp.example.org", "source_name": "Hospital 1",
  "authorization_code": "…", "code_verifier": "…", "assertion_nonce": "…" }
```

For `source = "oauth2"` the call carries no identity and no credential the API could check against a stored secret. It carries the authorization code, which the API redeems itself; the ID token that comes back is what makes the login testable, and the API verifies it to §2.10 **before any account lookup**, so that no user row is read on an unproven assertion (REQ-AUTH-076, REQ-AUTH-086). A call naming `oauth2` without a redeemable code, or whose token fails verification, is refused — the web layer's word alone never opens a session. The other sources are unaffected: LDAP still rests on PHP's word, and local still carries a password.

`source_name` (optional, REQ-AUTH-067) is the name the user selected on the login page; the API records it in the `login_success` / `login_failure` audit details. It never affects authorization or identity resolution.

```json
{ "email": "user@example.org", "source": "local", "password": "***" }
```

```json
{ "email": "user@example.org", "source": "ldap", "provider": "ldap-1", "source_name": "Hospital 1", "display_name": "Kari Nordmann" }
```

`display_name` (optional, LDAP only) is the name the winning directory entry carries (`LDAP_SERVER_n_NAME_ATTR`). It is used only when step 0.5 creates the account. An OAuth2 call carries no name: step 0.5 takes it from the verified claims.

```json
{ "email": "user@example.org", "source": "local", "password": "***", "mfa_code": "492817" }
```

API processing (in order):
0. **Local verification** (only when `source = "local"`, GD-18, REQ-AUTH-050/051): the user row exists — otherwise 401 `account_not_found` + audit `login_failure`; its `password_hash` is present and the password matches it — constant-time comparison (argon2id) — otherwise 401 `bad_password` + audit `login_failure` (the two cases are not distinguished to the caller, and the password value is never logged, REQ-AUTH-036).
0.5. **First-sign-in account creation** (`source = "ldap"` with no `attempts`, or `source = "oauth2"` once Sequence J (§2.10) has verified the assertion and its step 8 found neither a subject link nor an exact address; REQ-AUTH-091, DEV-AUTH-21): when no row has the address exactly and it is not `ADMIN_BOOTSTRAP_EMAIL` (step 2 handles that one), the API first refuses a value that is not an address — not exactly one `@` with text on both sides, or containing whitespace — with 401 `account_not_found` + audit `login_failure`. It then looks the address up ignoring case. One match is the account. Two matches → 401 `account_not_found` + audit `login_failure` with reason `account_ambiguous`, and nothing is created. No match → one transaction inserts the row — enabled, `auth_source` the call's source, no `password_hash`, `is_admin = 0`, no `valid_until`, `display_name` from the body (LDAP) or from the verified claims (OAuth2: `name`, else `given_name` + " " + `family_name`; a claim that is not a string is ignored), else the address's local part (at most 255 bytes, cut on a character boundary) — and audits `user_created` with `{email, display_name, re_enabled: false, valid_until: null, automatic: true, source, provider}`. On the OAuth2 leg the address is the verified claim, never the body's; an account found ignoring case still meets the subject-conflict check of §2.10 step 8 (403 `subject_conflict`, nothing created), and the account found or created is bound to the verified (`iss`, `sub`) when the login completes, as on any first verified sign-in. An insert that loses a race to a concurrent sign-in of the same address reads the winner's row. The steps below then run on the account as on any other: a disabled or expired account found here is refused at step 1, never replaced.
1. User row is **active** (REQ-AUTH-006, §4.4): `enabled = 1`, not expired (`valid_until`), not inactive — otherwise reject (403 `account_disabled` / 403 `account_expired`) and audit `login_failure` (the inactivity case first performs the auto-disable of §4.4, REQ-AUTH-053).
1.5. **Two-factor gate** (GD-21, REQ-AUTH-055/058, only for `source = "local"` or `"ldap"`): if the account's method is not `off`, a call without `mfa_code` → 401 `mfa_required` (body names the method); with an invalid, expired, or already-used code → audit `login_failure` (`bad_mfa_code`) + 401 `bad_mfa_code`; valid codes (§2.7) proceed and are recorded against replay. If `AUTH_REQUIRE_2FA` is on and the method is `off` → 401 `tfa_enrollment_required` (no audit failure — the first factor succeeded; enrollment follows, §2.7). Until this gate passes, no user object is returned and steps 3–5 do not run.
2. **Bootstrap promotion** (REQ-AUTH-007, GD-4, DEV-AUTH-15): if the email equals `ADMIN_BOOTSTRAP_EMAIL` or `ADMIN_BOOTSTRAP_EMAIL_2` (REQ-CFG-042) and no row exists, create it as an enabled administrator — the first-installation setup case; on a first installation without an IdP, the row's `password_hash` is provisioned from `ADMIN_BOOTSTRAP_PASSWORD` at startup (REQ-AUTH-051). Login never re-enables a disabled account and sets `is_admin = 1` on an existing enabled row only when the source is `local` or no other enabled administrator exists — after setup, administration is assigned through `PUT /api/v1/users/{id}` (REQ-API-136), not by the bootstrap path; the invariant that at least one enabled administrator remains is REQ-AUTH-068.
3. Update the row's `auth_source` to the call's `source` (REQ-AUTH-005) and set `last_login_at` to now (UTC, REQ-AUTH-053).
3.5. **Region from provider roles** (only `source = "oauth2"` from a provider with `OAUTH2_N_ROLES_ATTR` set, REQ-AUTH-088): record the role values the verified assertion carries and, when one maps to a region, set the automatic region (§2.12). This step never refuses a login.
4. Record `login_success` (audit) — `source=ui`, acting user set, no token; when the gate of step 1.5 applied, its details name the factor used (`totp`, `email`, or `recovery`, REQ-AUD-028).
5. Return the user object.

Response (200):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "is_admin": true, "auth_source": "oauth2", "ui_language": "en", "ui_theme": null,
  "region": "OUS", "preferred_region": null, "automatic_region": "OUS", "automatic_region_source": "provider" }
```

The API is stateless with respect to sessions: it MUST NOT create or store a session (GD-1, REQ-API-044).

### 2.4 Sequence D — logout (REQ-AUTH-015, REQ-API-045)

1. Browser `POST /logout` (CSRF token, REQ-AUTH-037).
2. PHP reads the acting user id from the session.
2.5. If this session ever gave a client a code (it holds `sso_sid`, §2.13), PHP → API `POST /api/v1/sso/end` `{sid}`: every client grant issued under this sign-in ends now (REQ-AUTH-094). A failure is logged and does not stop the sign-out. `POST /sso/logout` (a client's sign-out link, §2.13) runs this same sequence and returns to the client instead of step 5.
2.7. If `OIDC_SINGLE_SIGN_OUT=1` and the session signed in through OAuth2 provider N (`oauth2_provider`, §3), PHP → API `POST /api/v1/auth/oauth/end-session` `{provider: N}` as the user, with the session epoch (REQ-API-189). PHP keeps the answer's `end_session_url` and `state` for step 4.5. A failure is logged, does not stop the sign-out, and selects the "provider may still be signed in" line of step 5 (REQ-UI-088). Local and LDAP sessions skip this step.
3. PHP → API `POST /api/v1/auth/logout` (service token + `X-Internal-User-Id`) → the API records `logout` (audit) and returns 200.
4. PHP destroys the session (cookie invalidated) and drops its id from the account's session index (§3).
4.5. With an `end_session_url` from step 2.7, PHP starts a fresh **pre-authentication** session that holds only `oidc_logout_state` (§3) and answers `302` to `end_session_url`. The provider ends its own session and returns the browser to `GET /auth/logged-out?state=…` (Sequence L, §2.14).
5. Otherwise `302` to `/login`, with the "provider may still be signed in" line when step 2.7 failed.

Normative ordering: the audit call **precedes** session destruction (REQ-AUTH-015); REQ-API-045's assignment of destruction to the PHP layer is satisfied by step 4.

Session bounds: `SESSION_IDLE_TIMEOUT` (default 30 min) and the absolute `SESSION_LIFETIME` (default 8 h) — REQ-AUTH-015, `System_Configuration_Design.md` §3.8; an ended session redirects to `/login` with a line saying why (REQ-UI-007). The whole lifecycle — bounds, id rotation, replay detection, revocation — is §3.1.

### 2.5 Sequence E — brute-force protection (REQ-AUTH-035)

- Store: per host, in memory (`email → {failures, window_start, locked_until}`); lost on restart (allowed by REQ-AUTH-035's "in memory or session store").
- Rule: ≥ 5 failed logins for the same email within a rolling 10-minute window → further attempts for that email rejected for 15 minutes (429 on the login form; `login_failure` with reason `rate_limited`).
- The check runs **before** contacting the IdP/LDAP — and before the local hash check of Sequence F (fail fast; no credential probing against the directory or the hash column).
- A submission naming an account by username rather than address (REQ-AUTH-087) is expanded to that address **before** this check, so `hbar` and `hbar@institution.edu` are one lockout key and not two; an unknown or ambiguous name keeps its own text as the key, matching no account.
- A successful login clears the counter for that email.

### 2.6 Sequence F — local (table-based) attempt (GD-18, REQ-AUTH-050/051/065)

The email+password form of the login page (`User_Interface_Design.md` §2.2) initiates the credential race for the selected name (Sequence I, §2.9). When a local source is registered under that name, every submission includes this attempt: The identifier is an address or, since REQ-AUTH-087, the username in front of one: the API expands a value with no `@` to the one stored address whose local part it is (`GetUserByLocalPart`) and passes it on as the email field, so every step below — lockout, lookup, audit, session — sees an address. No name matching zero or two accounts resolves, and no answer distinguishes it from an unknown address (§2.6 step 2).

1. Brute-force check (Sequence E) — once per submission, before any source is contacted (§2.5).
2. **Verify** — PHP → API `POST /api/v1/auth/verify-password` with `{ "email": "…", "password": "***" }` (service token; the password travels only on the trusted internal path, REQ-AUTH-034, and is never logged — REQ-AUTH-036). The API runs Sequence C steps 0–1 **without login side effects** — no `last_login_at`, no `auth_source` write, no session, no duplicate `login_success` (that is what makes it safe to fire alongside the LDAP attempts of Sequence B) — but a rejection is not free: the endpoint enforces the Sequence E lockout itself and records a `login_failure` like every other credential path (security finding F1). It returns one of `ok` / `bad_password` (also for an unknown account) / `account_disabled` / `account_expired`, or `rate_limited` while the address is locked.
3. On `ok`, PHP **finalizes** with the single Sequence C call `POST /api/v1/auth/login` `{ "email": "…", "source": "local", "password": "***", "mfa_code"?: "…", "source_name": "Hospital 1" }`: the API re-verifies the hash (idempotent) and runs steps 1–5. Sequence C remains the only place login side effects happen, so a losing parallel attempt can never produce a second session or duplicate audit success (REQ-AUTH-065).
4. If every attempt of the name settles as failed: any `account_disabled` / `account_expired` outcome is surfaced as the specific reason (the account state — not the credential — is the problem); otherwise the login page shows the generic translated failure line, and exactly one `login_failure` is recorded for the submission (§2.9 step 5).

On success PHP establishes the session (§3) exactly as for the other paths; `auth_source` is `local`.

### 2.7 Sequence G — two-factor challenge and enrollment (GD-21, REQ-AUTH-054…059)

**Methods.** `off` (default), `totp` — RFC 6238 time-based one-time codes (SHA-1, 30 s step, 6 digits, ±1 step skew window) from a phone authenticator app; works with no network access on the user's side. `email` — a cryptographically random 6-digit code delivered to the account's address via the configured SMTP relay (`System_Configuration_Design.md` §3.11), valid for `TFA_EMAIL_CODE_TTL` (default 600 s), single-use, send rate-limited per account. Both methods issue 10 single-use **recovery codes** at activation (random, stored as one-way hashes, shown exactly once).

**Statelessness.** All second-factor state lives in the `user_two_factor` row (`Database_Schema_Design.md` §4): secret, last accepted TOTP step (a code is accepted only for a step strictly greater than the stored one — replay-proof), pending email code hash + expiry, recovery code hashes. The API keeps no challenge state of its own (GD-1).

**Challenge flow.** Sequence F/B receive 401 `mfa_required` from Sequence C step 1.5. PHP records the first-factor success in a **pre-authentication** session (`tfa_pending = {email, source, verified_at}` plus, on the local path, the **first-factor handle** verify-password issued — REQ-API-131; TTL 5 min) and renders the code form (no other page is reachable). Submitting the code re-invokes `POST /api/v1/auth/login` with `mfa_code` and, on the local path, that handle in place of the password: the user types one field, and no layer holds their password between the two calls (REQ-AUTH-036 — a PHP session holding it would be plaintext persistence). On success PHP promotes the pending session to a full session (§3) — `session_regenerate_id(true)` as always. Failed attempts count toward the Sequence E lockout; while locked, no verification runs and no email code is sent (REQ-AUTH-058). A handle that has expired or been invalidated answers 401 `first_factor_expired`, which is not a credential failure and does not extend the lockout — PHP clears the pending state and returns the user to the credential form.

**Mandated enrollment.** 401 `tfa_enrollment_required` (first factor OK, method `off`, `AUTH_REQUIRE_2FA` on) leads PHP to the enrollment wizard inside the pending session. The self-service endpoints (REQ-API-115) accept `X-Internal-User-Id` for the pending identity — the same service-token trust boundary as every other admin call; PHP only supplies an id whose first factor it just verified. That 401 carries the pending identity's `user_id`, which is what makes the wizard drivable pre-session: PHP stores it in `tfa_pending` and presents it as `X-Internal-User-Id` on every wizard call — without `X-Internal-Session-Epoch`, since no session exists yet (§3.1). Wizard: TOTP enroll → secret + QR shown once → confirm with one current code; or email start/confirm (409 when SMTP is not configured). Activation writes the method and returns the recovery codes once; PHP then resumes the login call and completes normally.

**Self-service management.** A signed-in user reaches the same wizard from the account page (`User_Interface_Design.md`): change method, view status, disable — disable requires a valid current code or recovery code, so a hijacked session cannot silently drop the factor (REQ-API-115). Audit: `tfa_enrolled`, `tfa_disabled`, `tfa_reset` (REQ-AUD-028).

**Administrator reset.** `POST /api/v1/users/{id}/tfa/reset` (`is_admin`) deletes the row back to `off` and clears codes — for a user who lost their phone or email access; under a mandate the user re-enrolls at the next login (REQ-AUTH-059). The user overview shows each account's method (REQ-UI-011).

### 2.8 Sequence H — out-of-band password setup: invite and reset (GD-22/GD-23, REQ-AUTH-060/062)

**Shared token mechanics.** Invite and reset tokens are one mechanism with two purposes. Generation: 32 random bytes from `crypto/rand` (256 bits); the value is emailed, never stored — only its SHA-256 hash goes into `password_tokens` (`purpose` = `invite` | `reset`, expiry = now + `AUTH_PASSWORD_TOKEN_TTL_DAYS` days, default 7; REQ-CFG-030, REQ-DB-039). A new invite replaces the account's outstanding invite row (re-invite invalidates the old link, REQ-AUTH-060); a new reset request replaces the outstanding reset row. Completion verifies the hash in constant time and enforces purpose, expiry, and single use; **every failure answers the same 401 `invalid_setup_token`** — unknown, expired, consumed, or wrong purpose are not distinguished (REQ-API-120). The link URL carries the token as a query parameter to the PHP route `/set-password?token=…`; the proxy's `/api/` query-string logging rule is extended to the reset/invite email routes' URLs in PHP access logs (REQ-AUTH-049 by analogy — token values never land in logs).

**Invite (admin-initiated).** `POST /api/v1/users/{id}/invite` (`is_admin`, REQ-API-117): requires the account to be on the local path (or intended for it) and SMTP configured (else 409 `smtp_not_configured` — with no relay the admin sets the password directly, REQ-AUTH-060). The email (translated, per §5.7 of the UI design) links to `/set-password?token=…`; audit `user_invited`. Completion (`POST /api/v1/auth/invite/complete`, purpose `invite`) stores the new argon2id hash, consumes the token, and audits `invite_accepted` — the person then logs in through Sequence F normally; the second-factor gate is **not** bypassed (REQ-AUTH-060).

**Reset request.** `POST /api/v1/auth/password-reset/request` with `{ "email": "…" }`: the response is a fixed 202 body regardless of outcome (REQ-AUTH-062 — no enumeration oracle). Internally: an **active local account** (`enabled = 1`, not expired, per §4.4) matching the address receives the emailed link; anything else sends nothing yet answers identically. Rate limit: default 3 requests / 15 minutes per address (plus the general IP limiter, `System_Configuration_Design.md` §3.9); audit `password_reset_requested` with the submitted address and source IP — never a token.

**Reset completion.** `POST /api/v1/auth/password-reset/complete` with `{ "token": "…", "password": "***" }`: verifies per the shared mechanics (purpose `reset`), stores the new argon2id hash, consumes the token, **invalidates all outstanding tokens of the account**, and audits `password_reset_completed`. No temporary password exists at any point (REQ-AUTH-062). The same transaction raises the account's session epoch, so every existing PHP session of the account ends at its next API call (REQ-AUTH-074, §3.1); invite completion does the same.

**Pre-authentication boundary.** The three Sequence H endpoints ride the same internal trust path as `POST /api/v1/auth/login`: service token required, no `X-Internal-User-Id` (the REQ-API-041 exception set — DEV-API-16), unreachable from outside (§1.2). PHP's public routes (`/password-reset`, `/set-password`) are the only browser-facing entry points and carry CSRF protection like every other form (REQ-AUTH-037); the token itself is the credential, so no session or identity is implied before completion.

**Self-service change (signed-in).** Not part of Sequence H's pre-auth path: `PUT /api/v1/users/me/password` verifies `current_password` against the hash first (failure → 401 `bad_password`, audit-logged, counted toward the Sequence E lockout), then stores the new hash, raises the account's session epoch — ending its other sessions — and audits `password_changed`; the answer carries the new epoch, which the calling session adopts under a new id (§3.1) (REQ-AUTH-061, REQ-API-118).

### 2.9 Sequence I — named source selection and the parallel credential race (master spec "Authentication order", REQ-AUTH-063…067, DEV-AUTH-14)

**Source registry.** Every configured authentication path is a **source**: kind `local` | `ldap` | `oauth2`, carrying one or more display **names**; the relation between names and sources is many-to-many (REQ-AUTH-064). Names extend the existing per-index configuration (`OAUTH2_N_*`, REQ-CFG-011; `LDAP_SERVER_N_*`, REQ-CFG-012) with a name list per source — the variable set is owned by `System_Configuration_Design.md` §3 (follow-up registration). The local kind exists at most once (one `users` table, GD-18) but may carry several names.

**Login page.** `GET /login` renders the distinct set of names across all configured sources ("Hospital 1", "Hospital 2", … — REQ-AUTH-063), in name order with a name carried by the local source alone last (REQ-UI-042); a name the local source shares with a directory or provider keeps its place. With exactly one distinct name the picker is skipped and that name applies implicitly; sources configured without a name form one implicit default set (REQ-AUTH-067). The selected name is stored in the session (`auth_source_name`, §3) before any credential exchange.

**Dispatch on submit.** For the selected name, PHP dispatches concurrently:

1. every **local** source under the name → the verify step of Sequence F (2.6);
2. every **LDAP** source under the name → Sequence B (search + bind-as-user), each with its own connect timeout so one unreachable directory cannot stall the race;
3. **OAuth2** sources under the name do **not** join the credential race — they are interactive: exactly one provider redirects immediately (Sequence A), several are offered for individual selection (REQ-AUTH-066).

The submitted email+password is sent **only** to the sources under the selected name — never to a source registered under a different name (REQ-AUTH-063; this replaces the old chain, which forwarded a failed local password onward to every configured LDAP server).

**First "login ok" wins.** The first successful attempt finalizes through Sequence C (with `source`, `provider` of the winner and `source_name`). A later success from another attempt is discarded at PHP before finalization — since non-winning attempts are side-effect-free (Sequence F step 2, Sequence B binds), no second session, duplicate audit success, or `last_login_at` / `auth_source` write can occur (REQ-AUTH-065). If a winner arrives while another attempt is still in flight, the in-flight result is simply ignored on arrival.

**All failed.** When every attempt settles as failed: any `account_disabled` / `account_expired` outcome is surfaced as the specific reason; otherwise the generic credential failure line is shown. Exactly one `login_failure` audit event is recorded for the submission (reason `bad_credentials`, or `provider_unavailable` when no source could be reached), with the selected name and the per-source outcomes in `details` (`Audit_Logging_Design.md` §3.1). The call that carries those outcomes reports the failure and authenticates nothing (REQ-API-135): PHP calls login **only** for an attempt that succeeded, so a first factor nobody passed can neither reach step 1.5's challenge nor return a user object.

**New pre-auth endpoint.** `POST /api/v1/auth/verify-password` joins the `X-Internal-User-Id` exemption set (§2.3, §6 — DEV-API-16): service token required, internal-only (§1.2), rate-limit and lockout rules as every login path — Sequence E is enforced here in the API, not delegated to PHP (security finding F1). It performs Sequence C steps 0–1 only and returns `ok` / `bad_password` / `account_disabled` / `account_expired` without touching any user row; a failed attempt extends the lockout window and writes a `login_failure` entry as on every other credential path, so guessing through this endpoint is bounded exactly like guessing through login. The password is never logged (REQ-AUTH-036).

**Interaction with the second factor.** The MFA gate stays in Sequence C step 1.5 on the winning source: the race answers *who you are*, the second factor then confirms it before any session exists (§2.7, REQ-AUTH-055) — losing attempts never reach the gate.

### 2.10 Sequence J — provider assertion verification (REQ-AUTH-076…084, DEV-AUTH-18)

Runs in the API on the `source = "oauth2"` leg of Sequence C, **before any account lookup**, so no user row is read on an unproven assertion. It starts at the token exchange rather than at a token PHP hands over: PHP delivers the code, the PKCE verifier and the nonce from its single-use browser transaction, and the API obtains the ID token from the issuer itself (REQ-AUTH-086). Nothing below takes PHP's word for anything in that token, which is the point of the sequence (finding F8).

1. **Parse.** The compact JWS splits into header, payload and signature. Malformed input → 401 `assertion_invalid`.
2. **Pin the algorithm.** The header's `alg` MUST equal `OAUTH2_N_ID_TOKEN_ALG` (default `RS256`). Anything else is refused *before a key is consulted* — including `none` and every HMAC algorithm, both of which providers advertise in metadata and both of which turn "verify using the algorithm the token names" into no verification at all (REQ-AUTH-077).
3. **Get keys.** From the `jwks_uri` named in the provider's discovery document — never derived from the issuer path, whose convention differs between providers (`/pf/JWKS` on PingFederate). Cached per issuer for `OAUTH2_JWKS_TTL`, refreshed once on an unknown `kid`, with superseded keys retained because providers publish pending, active and retired sets together. A `kid` still unknown after a refresh → `assertion_invalid`. No `kid` or certificate is ever pinned in configuration.
4. **Verify the signature** over the exact header-payload bytes with the selected key. Failure → `assertion_invalid`; logged without the token, its payload, or any claim (REQ-AUTH-036).
5. **Check the audience of this request.** `iss` equals the configured issuer; `aud` equals the configured client id (a list containing it is acceptable); `exp` lies in the future and `nbf`, when present, does not — each within a bounded clock-skew allowance (120 s default), because a five-minute ID-token lifetime makes an uncorrected clock look like an attack. The `nonce`, if the payload carries one, equals the transaction value PHP forwards; with `OAUTH2_N_NONCE_REQUIRED=1` a missing nonce is itself a refusal (REQ-AUTH-079).
6. **Check the address attestation.** `email_verified` MUST be the JSON boolean `true` in the same claim set that supplies the configured email address; a string `"true"`, `false`, or an absent claim is rejected as `email_unverified` (REQ-AUTH-078). The check runs here, on the response that supplied the address: the verified ID token's claims, or — when it names none for `OAUTH2_N_EMAIL_ATTR` — the userinfo endpoint of §2.1 step 6, read with this exchange's access token and held to the same rule, with its `sub` required to equal the verified token's. PHP does not perform this check; nothing reaches account resolution on either response without it.
7. **Check assurance.** When `OAUTH2_N_MIN_ACR` is configured, `acr` must be one of its values or `amr` include a configured method; an assertion carrying no assurance claim is refused `assurance_insufficient`. This fails closed deliberately — an installation that asks for a gate gets one, and the OAuth2 exemption of REQ-AUTH-055 stops being an assumption on step 1.5 (REQ-AUTH-081).
8. **Resolve the account.** The (`iss`, `sub`) link is consulted first (REQ-AUTH-080): a known link resolves to its account whatever the assertion's email now says. An unknown link with a verified address matching exactly one account creates the link and proceeds — that is the backfill path for accounts predating this sequence. An unknown address goes to Sequence C step 0.5, which finds the account ignoring case or creates it (REQ-AUTH-091); only an address matching two accounts that way → `account_not_found`. A conflict (the link points at one account while the address points at another) is refused with an administrator-actionable audit entry; the API never guesses which of two people a directory edit meant.
9. **Audit.** Issuer, provider subject, the assurance claims seen, and whether the address was verified — and no token, signature, or other claim material (REQ-AUTH-084).

**Discovery posture.** The discovery document is required: it names `jwks_uri`, and its own `issuer` value MUST equal the configured issuer (REQ-AUTH-082). Deriving `authorize`/`token`/`userinfo` by appending conventional names to the issuer root is withdrawn — PingFederate serves `/as/authorization.oauth2`, `/as/token.oauth2` and `/idp/userinfo.openid`, so a derived path produces a misleading upstream error where a hard stop names the provider. INSTALL.md §14 gains "the provider publishes no discovery document" as its own cause. The 502 names only the reason; the API log carries the cause of every discovery failure — both issuer values for a mismatch, the parse error for an unusable document, and for an unreachable provider the discovery URL tried with the transport error (refused, DNS, TLS, timeout).

**Logout token (REQ-AUTH-083/098).** A provider's back-channel logout token is verified by this sequence's key machinery (steps 2–4: pinned algorithm, `jwks_uri` keys and cache, signature) with its own claim rules in place of steps 5–9. That sequence is Sequence L (§2.14).

### 2.11 Authentication connection diagnostics (REQ-AUTH-085)

The Control Panel's Authentication Test section is guarded by the normal `is_admin` route guard. It enumerates only the immutable sources parsed from environment configuration; it accepts no caller-supplied host, issuer, or endpoint.

- **LDAP:** a CSRF-protected POST names one configured server index and carries the test username/password. PHP launches the existing isolated LDAP child with that server's configured search credentials and a five-second timeout. The check reports search and user-bind outcomes, the configured identity attributes, and a bounded allowlist of returned `uid`, `mail`, `cn`, `memberOf`, `groups`, and `roles` values. No login API call or CLARA session is made. The entered password remains only in the request and child stdin.
- **OAuth/OIDC:** a CSRF-protected POST starts the configured authorization-code/PKCE flow and marks its single-use session transaction as `auth_test`. The existing callback verifies state, exchanges the code server-side, then stores a one-time report and redirects to the diagnostic section; it never calls the login API or establishes a CLARA session. The report includes discovered endpoint URLs, endpoint status, and only allowlisted non-secret claims (`iss`, `sub`, email/name claims, groups/roles, and assurance claims). Claims are not interpreted as CLARA roles. For a provider with `OAUTH2_N_ROLES_ATTR` set, the report also names that claim, includes it under the same bounds even when its name is outside the allowlist, and shows the region each value currently maps to and the region a sign-in would set (§2.12) — a preview for setting up the mapping. The test records nothing in the mapping and changes no account.
- **Addresses to register** (REQ-AUTH-100): each OAuth2 provider's card lists, before any test runs, a table of the three addresses its registration needs — redirect URI, post-logout redirect URI (Sequence L1) and back-channel logout URL (Sequence L2) — with the purpose and the setting each comes from. PHP builds them from its own configuration only: the redirect URI and post-logout URI as configured (both display-only in PHP), the back-channel URL as `WEB_PUBLIC_URL` + `OAUTH2_N_BACKCHANNEL_LOGOUT_PATH`. An unconfigured sign-out address is shown as the suggested value (`WEB_PUBLIC_URL` + `/auth/logged-out`, `WEB_PUBLIC_URL` + `/auth/backchannel-logout/idpN`) with a "suggested" badge, and a line says whether `OIDC_SINGLE_SIGN_OUT` is on. No API call is made for it.
- Both reports are escaped when rendered, bounded, and one-time session data. Passwords, bind credentials, client secrets, authorization codes, access tokens, and ID tokens are excluded from report data, HTML, and logs; the reference proxy formats omit query strings and referrers. Failed requests return generic translated outcomes and technical stage/status information only.

### 2.12 Preferred region from provider roles (REQ-AUTH-088/089, DEV-AUTH-19)

Sequence C runs this as step 3.5 on the `source = "oauth2"` leg, for a provider whose `OAUTH2_N_ROLES_ATTR` is set (REQ-CFG-037). By then Sequence J has verified the assertion and resolved the account (§2.10 step 8); the user object has not been returned yet. **It never refuses a login.** Every failure below degrades to "no region information", and the login completes as it would without the key.

1. **Read the claim** named by `OAUTH2_N_ROLES_ATTR` from the verified ID token's payload. When the token does not carry it, read it from the userinfo response held to the token's `sub` (§2.10 step 6), fetching that response for this purpose if step 6 did not need it. PingFederate makes this common: its ID-token claims and `/idp/userinfo.openid` are released independently, so an attribute contract may release roles to userinfo only. A userinfo failure at this point is logged with its status alone and treated as an absent claim.
2. **Normalize.** A JSON string is one value; an array contributes its string elements; any other type contributes nothing. Values are trimmed, empty ones dropped and duplicates collapsed. At most the first 50 distinct values are taken, and a value longer than 255 characters is skipped. Both limits are logged as counts, never as values.
3. **Record** each value against the provider's issuer in `provider_role_regions` (REQ-DB-053) with one dialect upsert per value, in the login's transaction (`Database_Schema_Design.md` §4): a new pair enters unreviewed, and a known pair is marked seen again.
4. **Derive.** Collect the regions of the reviewed rows among those values. If there is at least one, the first in REQ-DB-006 order (OUS, Ahus, SiV, SSHF, NAT EU, OTHER) becomes `users.automatic_region`, with `automatic_region_source = 'provider'`. This replaces an earlier provider value or an assignment placeholder (`API_Endpoints_Design.md` §4.6). If no value maps, the row is left as it is. `preferred_region` is never written here.
5. **Return** the user object with its region values (REQ-API-176), so the session starts with the derived region.

**What is kept.** Role values are kept per provider without any account (DEV-DB-18). The table tells an administrator which values exist and how common they are, never who holds one. As a result, a mapping changed in the Control Panel reaches an account at its next sign-in. Role values never enter the `login_success` details (REQ-AUTH-084) or the logs; the sign-in writes no audit event of its own for them (REQ-AUD-036).

**Why it cannot authorize** (REQ-AUTH-089). The region is read in exactly two places: the per-project `in_preferred_region` flag of `GET /api/v1/projects`, computed after the visibility filter (`API_Endpoints_Design.md` §4.5), and the account page's region state (§4.30 there). No authorization path (§4) reads the region columns or the mapping. A forged or mistaken role value therefore reorders one user's own list and nothing else.

**Other sources.** LDAP and local accounts take no region from a directory. They receive the assignment placeholder (REQ-API-179) or choose their own region (REQ-UI-075).

### 2.13 Sequence K — sign-in for registered applications (REQ-AUTH-092…096, DEV-AUTH-22)

CLARA is the sign-in provider for clients registered as `SSO_CLIENT_N_*` (REQ-CFG-038). The client is another web application with a server of its own; FIONA is the first. The protocol is OAuth2's authorization code with PKCE S256 and a confidential client. What the client receives is not an access token for CLARA's API but a **grant**: an opaque value that says who the user is and that the client can ask about.

```text
browser          client server            CLARA web (PHP)                    CLARA API
   |  open client page  |                        |                                 |
   |------------------->|  verifier, state       |                                 |
   |<-- 302 /sso/authorize?client_id&redirect_uri&code_challenge&state            |
   |------------------------------------------->| registered? exact redirect_uri?  |
   |              (not signed in: /login?next=… — every source, two-factor rules)  |
   |                                            |-- POST /api/v1/sso/codes ------->|
   |<-- 302 redirect_uri?code&state ------------|<-------- {code, 60 s} ----------|
   |------------------->|-- POST /sso/token (secret, code, verifier) ----------->|
   |                    |<----------- {grant, expires_in, user} -----------------|
   |   client session   |   every few minutes: POST /sso/check (secret, grant)    |
```

**1. Authorization request** (`GET /sso/authorize`, guard `public`). PHP looks the client up and compares `redirect_uri` with the registered address byte for byte. On a mismatch, or for an unknown client, it shows the refusal page (400, `User_Interface_Design.md` §2.1) and never redirects: the address in the request is not trusted until it is the registered one. After that every answer goes to the registered address. `state` must be 1–512 printable ASCII characters (otherwise `error=invalid_request` without a state). `response_type` must be `code` (`unsupported_response_type`). The challenge must be 43 base64url characters with method `S256` (`invalid_request`).

**2. Sign-in.** A visitor who is not signed in, or whose second factor is pending, goes to `/login?next=` with the request rebuilt from the checked values only. The ordinary login runs, whatever the source, and lands back on `next` (REQ-AUTH-096): the login forms post `next` as a field, and an OAuth2 round trip keeps it in the session (`_login_next`, single use). With `prompt=none` the visitor goes back to the client with `error=login_required` instead (REQ-AUTH-095), so a client can try CLARA silently first and fall back to its own sign-in.

**3. Code.** For a signed-in user PHP calls `POST /api/v1/sso/codes` as that user, with the session's epoch. It sends the client, the address, the challenge, the session's **sign-in id** `sid` (`sso_sid`: 32 random bytes in hex, made the first time this session asks, kept across id rotation, gone with the session) and the session's `issued_at`. The API stores a row in `sso_grants` (REQ-DB-054): the SHA-256 hashes of the code and of `sid`, the challenge, the address, the account's `session_epoch`, and two expiries. The code expires after 60 seconds. The grant will expire at `issued_at + SESSION_LIFETIME`, the end of the CLARA session's absolute bound. PHP redirects to `redirect_uri?code=…&state=…`. A `sid` value never leaves PHP and the API; the client never sees it.

**4. Redemption** (`POST /sso/token`, `'session' => false`, form-encoded, client credentials as fields or HTTP Basic). PHP relays to `POST /api/v1/sso/token`. The API checks, in this order: client credentials (constant time, 401 `invalid_client`); the code's row exists (400 `invalid_grant`); it belongs to this client, is not redeemed, not ended and not expired; the address equals the stored one; `BASE64URL(SHA-256(code_verifier))` equals the challenge; the account exists; it is active (§4.4 — 403 `account_disabled` / `account_expired`); its `session_epoch` still equals the stored one. Every refusal is audited as `sso_rejected` with the reason, and once the row is found it is ended too. A conditional update then marks the row redeemed and stores the hash of a new 256-bit grant, so two concurrent redemptions cannot both succeed. The answer is `{grant, expires_in, user: {id, email, display_name}}`, and `sso_login` is audited.

**5. Check** (`POST /sso/check`). The client asks with its secret and the grant; the answer is always 200 `{active, reason, expires_in, user}`. The grant stands while the row is not ended and not past `expires_at`, the account is active, and its `session_epoch` equals the stored one. So a password change, a disable or a two-factor reset (REQ-AUTH-074) ends client sign-ins with the CLARA session. A 503 `temporarily_unavailable` means CLARA could not answer, and the client keeps its user signed in until it can.

**6. Sign-out.** CLARA's own sign-out ends the grants issued under the session's `sid` (Sequence D step 2.5). A client's sign-out link goes to `GET /sso/logout?client_id=…`. That page only asks, because a GET that signed out could be fired by any page embedding it. Its POST, with the CSRF token, runs Sequence D and then redirects to the client's registered `SSO_CLIENT_N_LOGOUT_URI`, or to `/login`. A visitor who is not signed in is sent on at once. A client that only ends its own session leaves CLARA signed in, and the next authorization request gets a code without a prompt.

**What the client does** (FIONA's side, not part of CLARA). It keeps its own sign-in for installations without CLARA. With CLARA, its sign-in page sends the browser to `/sso/authorize`, keeping `state` and the verifier in its own session, and checks `state` on return. Its server redeems the code, maps `user.email` to its own account and roles, and stores the grant in its session. It checks the grant every few minutes and ends its session when `active` is false. Its sign-out link goes to `/sso/logout?client_id=…`.

**Not covered.** The idle timeout of the CLARA session does not end a grant: an idle CLARA tab must not sign the user out of a client being used, so each side applies its own idle rule. The grant gives no access to CLARA's API, so a client cannot read projects or data with it. A user with no CLARA account who signs in through LDAP or OAuth2 gets one in no project (REQ-AUTH-091): the client is told who they are, and CLARA grants them nothing until an administrator adds them to a project.

### 2.14 Sequence L — single sign-out with the provider (REQ-AUTH-083, REQ-AUTH-097…099, DEV-AUTH-23)

Active only with `OIDC_SINGLE_SIGN_OUT=1` (REQ-CFG-036), and only for OAuth2 providers. It has two directions, which share nothing at run time: CLARA telling the provider that the user signed out (L1), and the provider telling CLARA (L2). Startup refuses the switch unless every configured provider has both `OAUTH2_N_POST_LOGOUT_REDIRECT_URI` and `OAUTH2_N_BACKCHANNEL_LOGOUT_PATH`, and `OAUTH2_N_SUBJECT_ATTR` left at `sub` (`System_Configuration_Design.md` §4.1).

```
L1  Browser          PHP (web)                        Go API                       Provider
     | POST /logout     |                                |                             |
     |----------------->| Sequence D 2.5 sso/end         |                             |
     |                  | 2.7 POST auth/oauth/end-session|                             |
     |                  |------------------------------->| discovery: end_session_     |
     |                  |<-- {end_session_url, state} ---|   endpoint                  |
     |                  | 3 auth/logout · 4 destroy      |                             |
     |<-- 302 end_session_url (new pre-auth session: oidc_logout_state) ---------------|
     |-------------------------------------------------------------------------------->| ends its session
     |<-- 302 /auth/logged-out?state=… ------------------------------------------------|
     |----------------->| state check (single use) → 302 /login (or client LOGOUT_URI)|

L2  Provider (server)    PHP (web)                          Go API
     | POST /auth/backchannel-logout/<seg>  logout_token=…    |
     |--------------------->| POST auth/oauth/backchannel-logout {provider, logout_token}
     |                      |-------------------------------->| verify (L2.3) · one tx (L2.4)
     |                      |<-- {matched, user_id, clients} -|
     |                      | remove the account's session files (§3)
     |<-- 200 (empty, no-store) | 400 {"error":"invalid_request"} | 503 ------------------|
```

**L1 — CLARA-initiated sign-out** (REQ-AUTH-097). Runs inside Sequence D (§2.4 steps 2.7, 4.5).

1. **Address.** `POST /api/v1/auth/oauth/end-session` resolves provider N's discovery document through the same cache as Sequence A and reads `end_session_endpoint`. It appends `client_id`, `post_logout_redirect_uri` and a fresh 32-byte base64url `state` to it, keeping any query the endpoint already carries, and returns the address and the `state`. It sends no `id_token_hint` (DEV-AUTH-23) and stores nothing (GD-1).
2. **The browser leaves.** PHP finishes CLARA's sign-out first (grants, audit, session destruction), so a provider that never sends the browser back leaves nothing of the CLARA session behind. The fresh pre-authentication session holds only `oidc_logout_state = {state, at, return}`. `return` is the registered `SSO_CLIENT_N_LOGOUT_URI` when the sign-out came from a client's sign-out link (`POST /sso/logout`, §2.13 step 6); otherwise it is empty.
3. **The browser returns** to `GET /auth/logged-out?state=…` (guard `public`). PHP compares `state` with `oidc_logout_state.state` in constant time and clears the key whatever the outcome. A match within 10 minutes of `at` goes to `return`, or to `/login` with the "signed out of CLARA and of the provider" line. Anything else — no state, a wrong one, an expired one, no pre-authentication session — goes to `/login` with the plain "signed out of CLARA" line (REQ-UI-088). The return changes no session and no account: it only chooses a line of text, so a forged return gains nothing.

**L2 — provider-initiated sign-out** (REQ-AUTH-098/099).

1. **Receive.** The provider POSTs `application/x-www-form-urlencoded` with `logout_token` to `OAUTH2_N_BACKCHANNEL_LOGOUT_PATH` (one path per provider under `/auth/backchannel-logout/`). The route is `'session' => false` and takes no CSRF token, like `POST /sso/token`: the signed token is the credential (DEV-TECH-4). The configured path selects provider N; a path naming no provider is 404. A request without exactly one `logout_token`, or with a body above 16 KiB, is answered 400 `{"error":"invalid_request"}` without calling the API. The reverse proxy exposes this prefix to the provider's addresses and nothing else (REQ-CFG-036, INSTALL.md §11).
2. **Relay.** PHP → API `POST /api/v1/auth/oauth/backchannel-logout` `{provider: N, logout_token}` with the service token and no user header (DEV-API-46).
3. **Verify** (refusals are 400 `invalid_logout_token`; the audit reason of REQ-AUD-039 is in brackets):
   1. Compact JWS in three parts, header and payload valid JSON objects [`malformed`].
   2. Header `alg` equals `OAUTH2_N_ID_TOKEN_ALG`, checked before a key is consulted; `typ`, when present, is `logout+jwt` or `JWT` (case-insensitive, an `application/` prefix allowed); any other value is refused [`algorithm`]. An ID token cannot pass on its `typ` alone, because steps 5 and 6 refuse it (DEV-AUTH-23).
   3. Key from `jwks_uri`, using Sequence J step 3's cache and its single refetch on an unknown `kid` [`unknown_key`]; the signature verifies over the exact header-payload bytes [`signature`].
   4. `iss` equals provider N's issuer; `aud` equals or contains its client id; `iat` is present, not more than the 120 s skew in the future, and not older than 10 minutes; `exp`, when present, has not passed (with the same skew) [`claims`].
   5. `events` is an object with the member `http://schemas.openid.net/event/backchannel-logout`, whose value is an object [`events`].
   6. No `nonce` claim [`nonce_present`] — so an ID token can never pass as a logout token.
   7. `sub` is a non-empty string of at most 255 characters [`subject_missing`]. A token that carries only `sid` is refused (DEV-AUTH-23).
   8. `jti` is a non-empty string of at most 255 characters [`jti_missing`].
4. **Apply** in one write transaction (`IMMEDIATE`, `Database_Schema_Design.md` §10):
   1. Delete the `oidc_logout_jtis` rows whose `expires_at` has passed; insert `(SHA-256(issuer ‖ 0x00 ‖ jti), iat + 10 min + 120 s)`. A key conflict is a replay: roll back, audit `provider_logout_rejected` with `replayed`, and answer 400.
   2. Look up `provider_subject_links` by (`iss`, `sub`). No row → `matched: false`; skip to step 5.
   3. Raise the account's `session_epoch` (REQ-AUTH-074). Every CLARA session of the account, whatever its source, gets 401 `session_revoked` on its next API call (REQ-API-147).
   4. End every row of `sso_grants` with this `user_id` and `ended_at IS NULL`, collecting their distinct `client_id`s first. This covers codes and grants issued under every sign-in of the account, not only one `sid` as `POST /api/v1/sso/end` does. A client's next `POST /sso/check` answers `active: false` with `reason: ended`.
   5. Write `provider_logout` `{issuer, provider_subject, matched, clients}`, with the account as acting user, or none when unmatched (REQ-AUD-039).
   6. Commit. A failure anywhere rolls everything back, including the `jti` row, so the provider's retry of the same token is accepted rather than refused as a replay.
5. **Answer.** The API answers `{status: "ok", matched, user_id, clients}`. When `matched`, PHP deletes the session files listed in the account's session index (§3). The epoch raise already ends each session at its next API call; deleting the files also ends it on a request that would not have called the API. PHP answers the provider 200 with an empty body; a 400 from the API is relayed as 400 `{"error":"invalid_request"}`; an unreachable API is 503 `{"error":"temporarily_unavailable"}`, which the provider may retry. Every answer carries `Cache-Control: no-store`.

**What it does not do.** No front-channel logout and no session-management iframe (PingFederate offers neither the iframe nor `sid`, §2.10). L1 does not wait for the provider to confirm. A user who closes the tab on the provider's page is still signed out of CLARA. L2 resolves by subject only, so it ends every session of the person, not just the one browser the provider had in mind (DEV-AUTH-23).

## 3. Session Schema (GD-1)

The session is owned by the PHP web application: PHP-native session, file storage in `SESSION_DIR` (MUST NOT be the application database, REQ-CFG-017). The API never sees the session (GD-1, REQ-API-044).

**Cookie** (REQ-TECH-019, REQ-AUTH-075): name `SESSION_COOKIE_NAME`, sent as `__Host-<SESSION_COOKIE_NAME>` whenever it is `Secure`; `HttpOnly`, `Secure` in production (`SESSION_COOKIE_SECURE` — `0` refuses to start under `APP_ENV=production`), `SameSite=Lax`. On login, `session_regenerate_id(true)` is performed (session-fixation defense).

| Key | Set | Value | Cleared |
|---|---|---|---|
| `user_id` | login | integer | logout / timeout |
| `email` | login | string | |
| `display_name` | login | string | |
| `is_admin` | login | `0`/`1` | |
| `auth_source` | login | `oauth2`/`ldap`/`local` | |
| `auth_source_name` | `/login` selection (§2.9) | the user-selected source name (REQ-AUTH-067); updated at each login | |
| `issued_at` | login | unix timestamp — start of the absolute bound (§3.1) | |
| `last_seen_at` | login; every request except a page's background refresh (`User_Interface_Design.md` §3.7) | unix timestamp — start of the idle bound (§3.1) | |
| `rotated_at` | login; every id rotation | unix timestamp (§3.1) | |
| `session_epoch` | login (the user object's `session_epoch`) | integer (REQ-AUTH-074) — sent as `X-Internal-Session-Epoch` | replaced when the session adopts its own account's raised epoch (§3.1) |
| `csrf_token` | session start | 32-byte random hex | session regeneration |
| `oauth2_state` | `/login` | 16-byte random hex | callback (single use) |
| `oauth2_pkce_verifier` | `/login` | random (S256) | callback (single use) |
| `sso_sid` | the first client code of this sign-in (§2.13) | 32-byte random hex — the sign-in id whose hash binds client grants to this session; kept across id rotation, never sent to a client | logout (after `POST /api/v1/sso/end`), or any new sign-in |
| `oauth2_provider` | OAuth2 login | the `N` of the provider the session signed in through — selects the provider of Sequence L1 (§2.14) | logout / timeout |
| `oidc_logout_state` | Sequence D step 4.5, in a fresh **pre-authentication** session that holds nothing else | `{state, at, return}` (§2.14 L1) — never signed in, never rendered | `GET /auth/logged-out` (single use), or 10 minutes |
| `_login_next` | `/login` before an OAuth2 redirect (REQ-AUTH-096) | the same-site landing path the provider's callback cannot carry | the callback (single use) |
| `tombstone` | id rotation, on the **replaced** id only | `{successor, at, user_id}` — the only key a replaced id holds; never signed in, never rendered (§3.1) | replay detection, or session GC |
| `tfa_pending` | login 401 `mfa_required` / `tfa_enrollment_required` (§2.7) | `{email, source, verified_at}` plus `user_id` on `tfa_enrollment_required` (drives the wizard, §2.7), plus `first_factor` on the local path — the handle of REQ-API-131 that stands in for the password when the code is submitted, so the credential is never persisted here — **pre-authentication**: not a session; every page guard rejects it | second factor verified or enrollment completed (promoted via `session_regenerate_id(true)`), or TTL 5 min / abort |

The identity keys above are written **only after the two-factor gate has passed** for accounts with a method other than `off` (GD-21, §2.7); while only `tfa_pending` exists, no authenticated page is reachable.

**Session index** (REQ-AUTH-083/099). With `OIDC_SINGLE_SIGN_OUT=1`, PHP keeps one index file per account under `SESSION_DIR` (mode 0700, the same directory and permissions as the sessions). It lists the live session ids of that account. PHP adds the id at login and on every id rotation (§3.1), and removes it at logout, at the end of a session, and when it finds a listed id that no longer exists. Sequence L2 reads it to delete the files of a signed-out account. The index is keyed by user id, which is what the API returns, and not by provider subject: PHP never sees the subject. An index that is wrong costs only promptness, because the epoch raise ends every session regardless.

All page reads go through a single `require_login()` helper; a missing session redirects to `/login`. The session stores **identity only** — no permissions, no project tokens, no data: permissions are re-derived from the API on every request (REQ-AUTH-033), and project tokens live in `user_projects.token`, never in the session.

The reference application's `AC.php` (master spec, "Details"; `assets/table_based_authentication_plus_user_management/AC.php`) is the model for this PHP session-establishment flow — a per-page access-control include that checks the session and redirects to login when it is absent, resolving the identity by name **or** institutional email. Only that *session pattern* is adopted (REQ-UI-032, REQ-TECH-025); the authentication mechanism itself (§2), the credential handling, and the storage (the database, not the reference's JSON file) follow this document and the fixed requirements (REQ-AUTH-036, REQ-TECH-020/024).

### 3.1 Session lifecycle (REQ-AUTH-015, REQ-AUTH-072…075 — DEV-AUTH-17)

The session stays an opaque, server-side PHP session. What this section adds is how long one id lives, how a copied id is noticed, and how the API ends sessions it never stored. The router runs the lifecycle once per request — after the session starts, before the guard or anything else reads identity (`Session::lifecycle()`; the translator loads the user's language only on its first line, for the same reason).

**Bounds (REQ-AUTH-015).** `issued_at + SESSION_LIFETIME` (default 8 h) is absolute; `last_seen_at + SESSION_IDLE_TIMEOUT` (default 30 min, `0` = off) is the idle bound, and every request of a signed-in session moves `last_seen_at` — except a page's background refresh (`X-Clara-Refresh: 1` on a `GET` data read, `User_Interface_Design.md` §3.7, REQ-UI-073), which is the page's timer rather than the user and would otherwise hold an open page's session alive until `SESSION_LIFETIME`. Such a read still meets the bounds, so it is often the request that finds an idle session and ends it. A session past either bound ends: the identity is dropped, the id regenerated with its file removed, a fresh CSRF token set, the flash line `session.ended.expired` or `session.ended.idle` queued, and the guard sends the browser to `/login?next=…`. A signed-in session without `session_epoch` predates this design and ends the same way.

**Rotation (REQ-AUTH-072).** On the first request after `rotated_at + SESSION_ROTATE_INTERVAL` (default 15 min, `0` = off, at least 120 s):

1. create the successor id (`session_create_id()`);
2. overwrite the current session with only `tombstone = {successor, at, user_id}` and write it;
3. start the successor — strict mode steps aside for this one start, because the id has no file yet — restore the data into it and set `rotated_at`; the response carries the new cookie.

The CSRF token moves with the data, so forms rendered before the rotation still submit.

**Grace window and replay (REQ-AUTH-072/073).** A request that presents a replaced id:

- within 60 s of `tombstone.at` continues in the successor, and its response re-issues the successor's cookie. PHP's session-file lock serializes the requests of one session, so a request queued behind the rotating one reads the tombstone only after the successor exists;
- after that is a replay. The tombstone is removed (the id regenerated), the presenting browser gets an anonymous session and `session.ended.replayed`, and PHP calls `POST /api/v1/auth/session-replay` as `tombstone.user_id` — through the override, without an epoch. The API raises the epoch, which ends the session holding the successor at its next call, and records `session_replay_detected` with the presenting address (REQ-AUD-030). Whoever holds the successor — the user or the copy — loses it; the system cannot tell which one is legitimate, so neither keeps it.

The trade-off is deliberate. Inside the grace window a copied old id is followed like a legitimate one; it is caught at the next rotation instead. A response carrying a new cookie that never reaches the browser, followed by more than 60 s without a request, reads as a replay: that user signs in again, and the audit trail shows it.

**Session epoch (REQ-AUTH-074).** `users.session_epoch` (REQ-DB-008) is copied into the session at login (the user object's `session_epoch`; `0` from an API that predates it, which also ignores the header) and sent as `X-Internal-Session-Epoch` on every call PHP makes for the session — not on the enrollment wizard's override calls (§2.7) nor on the replay report. The API compares it after the account-active rule (§4.3): a mismatch answers 401 `session_revoked` and writes `admin_rejected` with reason `session_revoked`. PHP then resets the session, and the router sends the browser to `/login` with `session.ended.revoked` — or answers a data request 403 `forbidden`, like any unauthenticated one. A controller never sees the refusal as a line to show: it travels as `SessionRevoked`, which is not an `ApiException`.

| Raises the epoch | Endpoint | Atomic with |
|---|---|---|
| any write of the local password — self-service change, reset completion, invite completion, administrator set or clear, re-create of a disabled account with a password | `PUT /users/me/password`, `POST /auth/password-reset/complete`, `POST /auth/invite/complete`, `PUT /users/{id}`, `POST /users` | the password write |
| `enabled` → `0` | `PUT /users/{id}`; the inactivity auto-disable (§4.4) | the disable (and its audit entry) |
| an administrator's two-factor reset | `POST /users/{id}/tfa/reset` | — |
| a reported replay | `POST /auth/session-replay` | the `session_replay_detected` entry |

Re-enabling never lowers the epoch, so a session from before a disable stays dead. The session that changed its own account keeps going: a self-service password change, or an administrator's password set or two-factor reset on their own row, answers the raised epoch, and PHP adopts it with `session_regenerate_id(true)` — no tombstone, since a credential change must not leave an old id that still leads anywhere. An administrator who disables their own account does not adopt it.

**Cookie (REQ-AUTH-075).** With `SESSION_COOKIE_SECURE=1` the cookie is `__Host-<SESSION_COOKIE_NAME>` — `Path=/`, no `Domain`, `Secure`, `HttpOnly`, `SameSite=Lax` — which a browser accepts only from this exact host over HTTPS, so neither a sibling subdomain nor a plain-HTTP page can plant or overwrite it. `APP_ENV=production` with `SESSION_COOKIE_SECURE=0` refuses to start. `SameSite=Strict` is not used: the OAuth2 callback (§2.1) is a cross-site top-level navigation that must carry the session holding `state` and the PKCE verifier.

**Why not signed tokens.** The owner asked whether the PHP session should become stateless JWTs with rotating refresh tokens. That design serves clients that call resource servers directly. Here the browser talks only to PHP (REQ-AUTH-010), and the API re-reads the acting user on every call anyway (§6), so a self-contained token would save no lookup; refresh-token rotation with reuse detection is server-side state in any case; and the session holds things a readable token must not (the cached project token, the PKCE verifier). This section delivers the properties that recommendation is after — short-lived credentials, automatic rotation, theft detection, revocation — on the opaque session (DEV-AUTH-17). The data API's project tokens are a separate credential and unchanged (§5).

## 4. Authorization Evaluation (ASM-AUTH-5)

A single explicit function in the Go API — no external policy engine. Inputs: subject, `is_admin`, role, arm, (instrument, event) pair, data access level, export level, the two rights, active data access group (ASM-AUTH-5). Every decision is explicit and auditable (REQ-AUTH-019).

### 4.1 Effective levels per (user, project)

Levels resolve at **arm** granularity as a default and at **(instrument, event)** granularity where a grant exists (REQ-AUTH-069); the two rights ride on the same scope and are never implied by a level (REQ-AUTH-070).

```
effective_levels(user, project):
  if user.is_admin:
      every arm and every pair:
          data = view_edit, export = export_full,
          delete_values = true, edit_surveys = true                    (REQ-AUTH-023)
      project_admin = true
  else if the assignment's role is set:
      for each arm a in arms(project):
          default[a] = { data   = role.arms[a].data    or no_access,     (REQ-AUTH-019)
                         export = role.arms[a].export  or export_none,
                         delete_values = false, edit_surveys = false }
      for each mapped pair p of an arm the role grants on:
          g = role.grants[p] or default[arm_of(p)]                       (REQ-AUTH-069)
          if g.data == no_access:                                       (REQ-AUTH-070)
              g.delete_values = g.edit_surveys = false      # denial wins over a right
      project_admin = role.project_admin
  else:   # member without a role
      every arm and pair as for is_admin
      project_admin = true                                              (REQ-AUTH-022)
```

A design change that adds an instrument, an event, or a mapping materializes `role.grants[p]` for the new pairs of every role as `read_only` / `export_none` / both rights unset, so a new pair is never resolved by an implicit rule later (REQ-AUTH-069).

Roles are project-scoped (REQ-AUTH-024); exactly one role per member, no union (REQ-AUTH-025); levels are read **at call time**, so role changes and user disabling take effect immediately without token refresh (REQ-AUTH-033, REQ-API-048).

### 4.2 Data access group visibility (GD-10)

```
visible_records(user, project):
  if user.is_admin:   all records                                          (REQ-AUTH-045)
  g = active_group(user, project)
  if g is set:        records where dag_group_id = g
  else:               all records of the project
```

The rule is orthogonal to the levels: the level governs which actions are allowed, the group governs which records they apply to (REQ-AUTH-045, REQ-API-092). A record created by an import takes the creator's active group, or none (REQ-AUTH-047, REQ-API-093); an import never changes an existing record's group; reassignment requires `project_admin` (REQ-AUTH-048).

### 4.3 Per-surface decision paths

| Surface | Credential | Decision |
|---|---|---|
| data API (`/api/`) | project token | token → (user, project, role) single indexed lookup (REQ-AUTH-032); then `user.enabled = 1` (REQ-API-048); levels from §4.1; record scope from §4.2 |
| survey link | link token | link → (project, record, instrument) with `revoked = 0`; fill-only on that (record, instrument) — every other content, record, or instrument is 403 (REQ-AUTH-039, REQ-API-083); revocation takes effect immediately (REQ-AUTH-040) |
| administration API (`/api/v1/*`) | service token + user id | §4.1 + §4.2 for the acting user (REQ-AUTH-013) |

The endpoint→permission mapping is the normative summary in `API_Endpoints_Requirements.md` §4.22. Access to a project or record the caller is not entitled to is rejected with a consistent 403 that does not disclose existence (REQ-API-007, REQ-AUTH-026).

### 4.4 Account active rule (GD-19, REQ-AUTH-006/052/053)

Evaluated at **every** authentication check — login (Sequence C), administration-API calls (the `X-Internal-User-Id` check of §4.3), and data-API token checks (the `user.enabled` check, REQ-API-048):

```
active(user, now):
  if user.enabled = 0:                      reject account_disabled
  if user.valid_until is not null and user.valid_until < today(now, UTC):
                                           reject account_expired        (REQ-AUTH-052)
  if user.last_login_at is not null
     and AUTH_INACTIVITY_LIMIT_DAYS > 0
     and now - user.last_login_at > AUTH_INACTIVITY_LIMIT_DAYS days:
     AUTO-DISABLE: set enabled = 0, last_login_at = NULL,
                   session_epoch + 1 (§3.1)             (same transaction)
     audit account_auto_disabled (source = system)         (REQ-AUTH-053, REQ-AUD-024)
     reject account_disabled
  return active
```

Rules:

- `AUTH_INACTIVITY_LIMIT_DAYS` is configuration (`System_Configuration_Design.md` §3.10); default **180**; `0` turns the rule off (REQ-CFG-024).
- The auto-disable is the **only** path that sets `enabled = 0` without an administrator; it is idempotent (a second check on the same account just sees `enabled = 0`) and commits atomically with the audit entry (REQ-AUD-003).
- **Re-enable** by an administrator (`PUT /api/v1/users/{id}`, `enabled: true`, REQ-API-048) leaves `last_login_at = NULL`, so the inactivity clock starts at the account's **next successful login** — the account is usable immediately after re-enabling (REQ-AUTH-053).
- `valid_until` (date, UTC; `NULL` = indefinite) is set from a `valid_days` field (integer ≥ 0; `0` = indefinite → `NULL`) as `today(UTC) + valid_days` at account creation/update (REQ-API-047/048). An expired account stays `enabled = 1`; the administrator restores access by setting a new `valid_days` (REQ-AUTH-052).
- `last_login_at` is set to now (UTC) on **every** successful login, whatever the source (GD-19).
- The user overview displays `last_login_at`, `valid_until`, and a derived status (active / disabled / expired / auto-disabled) (REQ-UI-011, `User_Interface_Design.md` §5.1).

## 5. API Token Mechanics (GD-5)

- One token per (user, project) assignment: UUID v4 (128-bit, `crypto/rand`), stored in `user_projects.token` (`CHAR(36)`), globally unique (REQ-AUTH-029).
- **Adopted REDCap token** (REQ-AUTH-090, DEV-AUTH-20): an administrator may replace a member's token with an existing REDCap value, so a client that has it hard-coded (FIONA) keeps working. The value must match `^[0-9A-F]{32}$` — REDCap's form, 128 bits — and fits the same column; it is looked up by the same single indexed query, so the data API does not distinguish the two forms. It cannot collide with a survey-link token, which is a 36-character UUID. Uniqueness is checked before the write, and the `UNIQUE` index still decides a race; either case is a 409 and changes nothing. The adoption is audited as `token_rotated` with `"adopted": true`, never with the value, and the value is not returned. A later rotation replaces it with a CLARA-issued UUID.
- Accepted **only** as the `token` request-body/query parameter of the REDCap protocol — never an `Authorization` header (REQ-AUTH-031, REQ-API-010).
- Validation is a single indexed lookup (REQ-AUTH-032); a missing/invalid token yields the same 401 `Invalid token` response whether or not the token exists (REQ-API-011).
- Rotation (REQ-AUTH-030): the new value replaces the old in the single row — the previous token is invalid immediately; audit `token_issued` / `token_rotated` / `token_revoked` (`Audit_Logging_Design.md` §3.4).
- Member removal invalidates the token immediately (REQ-API-055); disabling a user denies the effective permissions of that user's tokens at call time (REQ-API-048, §4.3).
- Token values MUST NOT appear in application logs (REQ-API-005); the audit trail MAY record them in the fixed `token` column (`Audit_Logging_Design.md` §2).

## 6. Service Token Rules (internal boundary)

- `X-Internal-Service-Token` is compared in constant time (`crypto/subtle.ConstantTimeCompare`) and MUST NOT be logged (REQ-AUTH-012); a missing/invalid token → 401 + audit `admin_rejected` — enforced regardless of any other configuration (REQ-CFG-023).
- `X-Internal-User-Id` is authoritative for authorization on `/api/v1/*` (REQ-AUTH-013); an unknown or disabled user → 403 + audit `admin_rejected`.
- `X-Internal-Session-Epoch`, when present, MUST equal the acting account's `session_epoch`; otherwise 401 `session_revoked` + audit `admin_rejected` (REQ-API-147, §3.1). It is checked after the account-active rule, so a disabled account keeps its 403.
- The proxy strips all three headers from every externally-originated request (REQ-AUTH-014; configuration in `Technology_Stack_Design.md` §5).
- `POST /api/v1/auth/login` (service token only, no user id — §2.3), the three pre-authentication password endpoints of Sequence H (§2.8), `POST /api/v1/auth/verify-password` (§2.9), `POST /api/v1/auth/oauth/start` (§2.1), and `POST /api/v1/sso/token` and `POST /api/v1/sso/check` (§2.13, where the client secret takes the user header's place — DEV-API-44) are the exceptions. On the `source = "oauth2"` leg the service token is necessary but not sufficient: the call's `assertion` must verify first (§2.10, REQ-AUTH-076), which is what narrows finding F8 on that path without changing anything else here.

## 7. Non-Functional Security

- **Password handling** (REQ-AUTH-036, GD-18): the LDAP password exists only transiently — it goes over TLS to the directory and is never persisted or logged. Local (table-based) passwords are verified against an **argon2id** hash stored in `users.password_hash` (DEV-AUTH-16) — `golang.org/x/crypto/argon2` behind `api/internal/passhash`: a PHC string `$argon2id$v=19$m=…,t=…,p=…$salt$hash` with a 16-byte `crypto/rand` salt and a 32-byte key, made at RFC 9106's second recommended setting (64 MiB, three passes, four lanes) and verified with the parameters it carries, in constant time; nothing but argon2id verifies (no bcrypt fallback), and parameters outside sane bounds (> 1 GiB, > 64 passes) are refused unevaluated. At most one hash computation runs per CPU, so concurrent logins cannot multiply the 64 MiB unboundedly. The password policy (security finding F12) is at least 12 characters and at most 1,024 bytes; the plaintext exists only in flight (browser → PHP over TLS; PHP → API over the trusted internal path) and is never logged, never in audit `details`, and never returned by any endpoint. Setting/resetting a password stores only the new hash; clearing it sets the column to `NULL` (the account keeps its other paths). Out-of-band setup tokens (Sequence H) exist in plaintext only inside the generated email and the completion request — the store holds only the SHA-256 hash (`password_tokens`, REQ-DB-039), and token values never appear in logs or audit details (REQ-AUTH-060/062, REQ-AUD-029).
- **Two-factor material** (GD-21, REQ-AUTH-056/057/058): TOTP secrets are 160-bit `crypto/rand` values (base32 for the QR/`otpauth://` URI); RFC 6238 verification uses standard-library `crypto/hmac` + `crypto/sha1` — no new dependency. Email codes and recovery codes come from `crypto/rand`. Pending email codes and recovery codes are persisted **as one-way hashes only** (`user_two_factor`, REQ-DB-038); the TOTP secret is stored as issued, never returned by any endpoint after its single enrollment display, and never written to logs or audit details. Email delivery uses standard-library `net/smtp` from the API against the configured relay; message bodies (the code) are never logged.
- **CSRF** (REQ-AUTH-037, REQ-UI-005): every state-changing browser request carries the per-session `csrf_token` (form field or `X-CSRF-Token` header); the administration API is additionally protected by the service-token boundary (GD-1).
- **Trusted path** (REQ-AUTH-034): PHP→API traffic on loopback or TLS.
- **Log hygiene** (REQ-AUTH-049, REQ-API-005): no credential reaches a log line — the data API takes its token in the request body (REQ-API-010, DEV-API-30), and the invite/reset link's query string stays out of the web server's logs; neither component logs the service token, IdP secrets, or record values.

## 8. Resolved Deferred Items

| Deferred | Resolution here |
|---|------|
| session ownership and schema (GD-1) | PHP-native session, key table §3; the API is stateless |
| logout ordering (REQ-AUTH-015 vs REQ-API-045) | the audit call precedes session destruction (§2.4) |
| `X-Internal-User-Id` on `POST /api/v1/auth/login` (REQ-API-041) | the login endpoint is exempt — service token only; the externally authenticated identity arrives in the body (§2.3); the three Sequence H pre-auth endpoints, `verify-password` and `oauth/start` share the exemption (§2.8, §2.9, §2.1 — DEV-API-16/17/32) |
| brute-force store and windows (REQ-AUTH-035) | in-memory per host; 5 failures / 10 min → 15 min lockout (§2.5) |
| OAuth2 flow hardening | PKCE (S256) + single-use `state` (§2.1); signed-assertion verification added later as Sequence J (§2.10, DEV-AUTH-18) |
| `login_failure` reason vocabulary | `provider_unavailable \| state_mismatch \| bad_credentials \| bad_password \| account_not_found \| account_disabled \| account_expired \| rate_limited \| bad_mfa_code` (`Audit_Logging_Design.md` §3.1) |
| table-based authentication (GD-18) | `users.password_hash` (argon2id, DEV-AUTH-16) + `auth_source = local`; Sequence F, raced with the LDAP servers of the selected name by Sequence I (DEV-AUTH-14); bootstrap password from `ADMIN_BOOTSTRAP_PASSWORD` (required at startup when no IdP is configured); `REQ-AUTH-036`: no plaintext, hash only (§2.2/§2.3/§2.6, §7) |
| account validity and inactivity (GD-19) | `users.valid_until` / `users.last_login_at`; the account-active rule of §4.4 evaluated at every authentication check; auto-disable + `account_auto_disabled` audit; admin re-enable resets the inactivity clock |
| two-factor authentication (GD-21) | `user_two_factor` (§2.7); Sequence C step 1.5 gate on local/LDAP logins only — OAuth2 MFA stays with the IdP (DEV-AUTH-10); TOTP (stdlib RFC 6238) + email codes (`net/smtp`); recovery codes; `tfa_pending` pre-auth session (§3); mandate `AUTH_REQUIRE_2FA`; admin reset |
| account onboarding and password lifecycle (GD-22/GD-23) | Sequence H (§2.8): `password_tokens` (REQ-DB-039) — invite and reset share single-use hashed expiring tokens (`AUTH_PASSWORD_TOKEN_TTL_DAYS`, REQ-CFG-030); the three pre-auth endpoints extend the REQ-API-041 exception set (DEV-API-16); generic reset response (no enumeration), no temp passwords, current-password proof for self-service change; a password write ends the account's other sessions through the session epoch (§3.1) |
| authentication order (master spec "Authentication order", DEV-AUTH-14) | Sequence I (§2.9): user selects a source **name** before login (many-to-many with sources); local+LDAP sources under the name race in parallel, first "login ok" wins via side-effect-free `verify-password` + single Sequence C finalization; OAuth2 stays interactive within the name; credentials confined to the selected set; instead of a sequential fallback (DEV-AUTH-14) |
| session lifecycle hardening (DEV-AUTH-17; security finding F17) | §3.1: idle bound `SESSION_IDLE_TIMEOUT` (30 min) beside the absolute `SESSION_LIFETIME` (8 h); id rotation every `SESSION_ROTATE_INTERVAL` (15 min) with a 60 s grace window and a tombstone on the replaced id; replay → all sessions of the account end + `session_replay_detected`; `users.session_epoch` raised by password writes, disables, two-factor resets and replays, checked as `X-Internal-Session-Epoch`; `__Host-` cookie, production refuses a non-`Secure` one. Signed stateless tokens were considered and not adopted (§3.1, "Why not signed tokens") |

## 9. Open Items

- **Whether the provider echoes `nonce`** is an observed fact, not a documented one — PingFederate's OIDC policy settings expose no nonce option and its discovery sample shows none. `OAUTH2_N_NONCE_REQUIRED` stays at `0` until one live exchange has been inspected per provider (REQ-AUTH-079).
- **The JSON type of `email_verified`** under the mapping a given installation chooses, and whether that directory can attest verification at all. If it cannot, the honest configuration is to say so in the installation notes rather than map a constant true (REQ-AUTH-078).
- **What the provider's logout token and logout endpoint accept** (ASM-AUTH-7): that its back-channel token carries `sub`, and that its RP-initiated logout accepts `client_id` with the registered `post_logout_redirect_uri` without `id_token_hint`. Observe one live sign-out in each direction before enabling `OIDC_SINGLE_SIGN_OUT` (§2.14).
- **Provider version floor and entitlements.** Back-channel logout needs PingFederate 11.3 or newer and RP-initiated logout 12.0 or newer; nothing public gates these behind an entitlement, but licensing is by managed identities and enforcement can be by licence key, so both are confirmations to obtain rather than assumptions to design on (ASM-AUTH-6).
- **Which PingFederate attribute carries the region roles**, and whether the attribute contract releases it to the ID token or to userinfo only, is installation-specific. The installation records the claim name in `OAUTH2_N_ROLES_ATTR` and checks it with the Authentication Test (§2.11) before relying on sign-in regions. Reading LDAP `memberOf` for the same mapping is not designed.
- **A pairwise subject's sector identifier** must be recorded per installation and treated as immutable: changing it invalidates every stored `sub` and orphans the account links built with it (REQ-AUTH-080).

## 10. Rationale and Trade-offs

Why authentication and authorization are built this way, and what each choice costs. The decision records DEV-AUTH-1…19 (`Requirements/Authentication_Authorization_Requirements.md` §6) carry the requirement-level reasoning.

- **PHP owns the session; the API owns every decision** (GD-1, §3, §6). The browser talks only to PHP, and the API re-reads the acting user on every call (§4), so a permission change takes effect on the next request without re-authentication. The cost is one account read per call, and a service secret whose holder can act as any user — which is why the administration API must not be reachable from other hosts (§1.2, REQ-CFG-033).
- **The API verifies provider logins itself** (§2.10, DEV-AUTH-18). PHP keeps only the single-use browser transaction; the Go API redeems the code and verifies the ID token before any account lookup, so the web layer's word alone never opens a session. LDAP still rests on PHP's word, because the bind happens in PHP (§2.2, §6).
- **Named sources raced in parallel** (§2.9, DEV-AUTH-14). Credentials reach only the sources under the name the user picked, one unreachable directory cannot stall the others (per-source timeouts), and exactly one `login_failure` is written per submission. OAuth2 cannot be raced silently, so it stays an explicit choice within the name.
- **Opaque sessions, not signed tokens** (§3.1, DEV-AUTH-17). A self-contained token would save no lookup here, and refresh-token rotation needs server-side state anyway. Rotation, replay detection and an account epoch deliver the same properties on the existing session. The accepted cost: inside the 60-second grace window a copied old id is followed like the legitimate one; it is caught at the next rotation (§3.1).
- **CLARA's second factor guards only the logins CLARA verifies** (§2.7, DEV-AUTH-10). On the OAuth2 path the provider owns MFA; the assurance gate (`OAUTH2_N_MIN_ACR`, §2.10) is how an installation requires it.
- **The assurance and verification checks fail closed** (§2.10). A configured gate refuses a token with no assurance claim; an `email_verified` that is not the JSON boolean `true` is refused. An installation that asks for a check gets it, even when a provider's release is incomplete.
- **Brute-force state lives in memory** (§2.5). It is lost on restart, which REQ-AUTH-035 allows; the gain is no shared store and a check that runs before any directory or hash is touched.
- **argon2id with bounded work** (§7, DEV-AUTH-16). Memory-hard hashing costs 64 MiB per verification, so at most one hash computation runs per CPU, and parameters outside sane bounds are refused unevaluated.

## 11. Edge Cases and Failure Modes

| Situation | Behavior | Specified in |
|---|---|---|
| OAuth2 `state` does not match the session | Abort, no session; `login_failure` with `state_mismatch` | §2.1 |
| Provider returns an error callback (`access_denied`, …) | The transaction is consumed; the user returns to the login page; no other source is tried silently | §2.1 |
| The round trip cannot start (discovery fails, REQ-API-173) | No transaction is stored and the selected name is cleared, so `/login` does not restart the failing start; the page that says so leads back to the picker | §2.1, `User_Interface_Design.md` §2.2 |
| ID token signed with an algorithm other than the pinned one (including `none` or HMAC) | Refused before any key is consulted | §2.10, REQ-AUTH-077 |
| ID token with an unknown `kid` | One JWKS refetch; still unknown → refused | §2.10 |
| Server clock drifts | `exp`/`nbf` are checked within a bounded skew (120 s default) | §2.10 |
| `email_verified` absent, `false`, or the string `"true"` | Refused as `email_unverified` | §2.10, REQ-AUTH-078 |
| Userinfo answers about a different subject, or cannot be reached | Different subject → refused; unreachable → `provider_unavailable`, without spending the address's lockout | §2.1, §2.10 |
| Subject link and address point at different accounts | Refused, with an administrator-actionable audit entry; the API never guesses | §2.10, REQ-AUTH-080 |
| The roles claim is absent, not a string or list, or userinfo fails while reading it | No region information; the login completes, and the automatic region stays as it was | §2.12 |
| A sign-in's role values map to several regions | The first in REQ-DB-006 order wins; the user can choose another on the account page | §2.12 |
| A role value is mapped after its holders have signed in | Each holder gets the region at their next sign-in; a user who chose their own region keeps it | §2.12, DEV-DB-18 |
| An LDAP server is unreachable during a race | That attempt settles as failed after its own timeout; the others continue | §2.2, §2.9 |
| Every source under a name fails | One `login_failure`: `provider_unavailable` if all were unreachable, otherwise `bad_credentials`; a disabled or expired account shows its specific reason | §2.2, §2.9 |
| Five failed logins for one address within 10 minutes | Further attempts rejected for 15 minutes (429) before any directory or hash is contacted; a username is expanded to the address first | §2.5, REQ-AUTH-087 |
| Invite or reset token unknown, expired, consumed, or of the wrong purpose | One answer for all four: 401 `invalid_setup_token` | §2.8 |
| A first-factor handle expires before the code is entered | 401 `first_factor_expired`; not counted as a credential failure; the user returns to the credential form | §2.7 |
| A rotated-away session id is presented after the grace window | Replay: the account's sessions end, `session_replay_detected` is audited, and neither holder keeps the successor | §3.1 |
| A rotated cookie never reaches the browser, and the next request comes after more than 60 s | Reads as a replay: the user signs in again, and the audit trail shows it | §3.1 |
| A password change, disable, or 2FA reset elsewhere | The account epoch rises; every other session gets 401 `session_revoked` on its next API call | §3.1, §6 |
| Production with `SESSION_COOKIE_SECURE=0` | Startup is refused | §3, §3.1 |
| Provider sign-out address cannot be built (discovery fails, no `end_session_endpoint`) | CLARA's sign-out completes; the login page says the provider sign-in may still be active | §2.4, §2.14 L1 |
| The browser returns to `/auth/logged-out` with no or a wrong `state` | The plain "signed out of CLARA" line; no session or account changes | §2.14 L1 |
| A logout token is presented a second time | Refused as `replayed` while its `jti` is in the replay window; after the window it is refused by age | §2.14 L2, REQ-DB-057 |
| A logout token names a subject with no account link | 200 to the provider, `provider_logout` with `matched: false`; nothing ends | §2.14 L2 |
| The API fails while applying a logout token | Nothing is recorded, the provider gets 503 and may retry; the retry is not a replay | §2.14 L2 |
| An ID token is posted to the back-channel path | Refused: it carries `nonce` or lacks `events` | §2.14 L2 |
| Unknown or disabled acting user on `/api/v1/*` | 403, `admin_rejected` audited; a disabled account keeps its 403 even with a stale epoch | §6 |
