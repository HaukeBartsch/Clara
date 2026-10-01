# Authentication & Authorization — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/Authentication_Authorization_Requirements.md`\
**Date:** 2026-09-21

## 1. Trust Model

### 1.1 Actors and surfaces

| Actor | Surface | Credential | Grants |
|---|---|---|---|
| browser (member) | PHP routes only (REQ-AUTH-010) | session cookie (§3) | the member's effective permissions |
| PHP web application | `/api/v1/*` (internal path only) | `X-Internal-Service-Token` + `X-Internal-User-Id` (REQ-AUTH-011) | exactly the acting user's permissions — PHP adds none |
| external data-API caller (Fiona/RIS) | `POST /api/` (public) | project token as `token` parameter (REQ-AUTH-031) | the token holder's per-arm levels (REQ-AUTH-033) |
| survey respondent | PHP route `/s/{link}` (public, no session — GD-9) | opaque link token | fill-only on exactly one (record, survey instrument) (REQ-AUTH-039) |
| IdP / LDAP | outbound from PHP | OAuth2 client secret / LDAP bind DN | identity assertion (email) |
| local (table-based) account | PHP login form → API | email + password (verified against `users.password_hash`), plus the configured second factor when enrolled (GD-21, §2.7) | the account's normal permissions (GD-18, REQ-AUTH-050) |
| invited / resetting person (pre-authentication) | PHP set-password page via the emailed link (public route, no session — GD-22/GD-23) | single-use hashed token (`password_tokens`, REQ-DB-039) | exactly one thing: set the password of the one addressed account (§2.8) — nothing else is reachable |

The second factor (GD-21) guards the two CLARA-verified rows above — local and LDAP form logins — after first-factor success; the OAuth2 row is exempt (MFA there is the provider's responsibility, DEV-AUTH-10), as are survey links and data-API tokens (§4.3).

### 1.2 Network topology (normative rules)

```
public internet
   │  TLS termination
   ▼
reverse proxy (nginx)
   ├─ PHP routes            ──► PHP-FPM ──► Go API  (/api/v1/*, loopback)
   ├─ POST /api/            ─────────────► Go API  (data API, REDCap protocol)
   └─ /api/v1/*, /docs      ── NOT routable externally (REQ-TECH-018, REQ-AUTH-014)
Go API ──► SQLite / MariaDB
```

- `/api/v1/*` is reachable only from the trusted internal path (REQ-AUTH-014, REQ-API-040); the proxy strips `X-Internal-Service-Token` and `X-Internal-User-Id` from every externally-originated request (rules fixed in `Technology_Stack_Design.md` §5).
- Inter-component traffic is loopback or TLS (REQ-AUTH-034).
- The browser never calls `/api/v1/*` directly (GD-1, REQ-AUTH-010, REQ-UI-002).
- The proxy MUST NOT log the query string of `/api/` requests, or must redact `token=…` (the bearer rides in the query/body per REQ-API-010; REQ-AUTH-049).

## 2. Authentication Sequences

Every login starts with the user selecting an authentication source **by name** on the login page (Sequence I, §2.9 — REQ-AUTH-063); the sequences below describe what happens inside and after that selection.

### 2.1 Sequence A — OAuth2 authorization code (REQ-AUTH-001/002/066)

An OAuth2 source joins login only through a browser round-trip, never the credential race (§2.9): selecting a name mapped to exactly one OAuth2 source starts this sequence immediately; with several providers under the name, the user picks which one to complete (REQ-AUTH-066).

1. Browser opens `/login`, where PHP lists the distinct configured source names (§2.9); the user selects a name whose set contains the OAuth2 provider. PHP generates `state` (16-byte random hex) and a PKCE `code_verifier` (random, challenge method S256), stores both in the session together with the selected name (`auth_source_name`, §3), and redirects to the provider's `authorize` endpoint with `response_type=code`, `client_id`, `redirect_uri = WEB_PUBLIC_URL + /auth/callback`, `state`, `code_challenge`.
2. The user authenticates at the IdP; the provider redirects the browser to `/auth/callback?code&state`.
3. PHP verifies `state` against the session value (**single use**; mismatch → abort, no session, `login_failure` with reason `state_mismatch`).
4. PHP exchanges `code` + `code_verifier` + `client_secret` for tokens at the `token` endpoint — server-side, over TLS (REQ-AUTH-001).
5. PHP resolves the identity: `userinfo` response (or ID token) → email via `OAUTH2_N_EMAIL_ATTR` (REQ-AUTH-004).
6. PHP calls the API's login endpoint (Sequence C) with `source:"oauth2"`.
7. On success PHP establishes the session (§3) and redirects to `/`.

Any failure at steps 3–5 does **not** silently move on to other sources: it is audit-logged (`login_failure`, `Audit_Logging_Design.md` §3.1) and the user returns to the login page (where the same name's credential form remains available if that name's set also contains credential sources — REQ-AUTH-066).

### 2.2 Sequence B — LDAP attempt (sources under the selected name, REQ-AUTH-003/065)

The PHP login form carries email + password for the **local and LDAP** paths; on the LDAP path the password is sent to the directory and never stored or logged (REQ-AUTH-036). All LDAP sources registered under the selected name are attempted **in parallel** with the local check of Sequence F — not sequentially, and not after it has failed (DEV-AUTH-14). For every LDAP source `N` in the selected set, concurrently:

1. **Search** (using `LDAP_SERVER_N_BIND_DN`/`BIND_PASSWORD`, or anonymous): find the entry in `SEARCH_BASE` whose `UID_ATTR` matches the login name; read `EMAIL_ATTR` and `NAME_ATTR` (REQ-CFG-012).
2. **Bind-as-user** (ASM-AUTH-2): simple bind with the entry DN and the supplied password. Success → identity is the entry's email (REQ-AUTH-004); a bind failure or per-source connect timeout settles that attempt as failed — other attempts continue unaffected.
3. On the **first** successful server (first "login ok" wins, REQ-AUTH-065): PHP calls the API's login endpoint (Sequence C) with `source:"ldap"`, `provider:"ldap-N"`, and the identity; any later success from another source is discarded (§2.9 step 4).
4. All attempts in the set settle as failed → `login_failure` (reason `bad_credentials` when an entry was found, `provider_unavailable` when unreachable) per §2.9 step 5.

### 2.3 Sequence C — API login call (REQ-AUTH-016, REQ-API-044)

`POST /api/v1/auth/login` — one of the five administration endpoints exempt from `X-Internal-User-Id` (it establishes the user context; the other four are the pre-authentication password endpoints of Sequence H, §2.8, and the side-effect-free `POST /api/v1/auth/verify-password` of Sequence I, §2.9 — DEV-API-16); it still requires a valid `X-Internal-Service-Token` and is internal-only per §1.2. `provider` names the concrete winning source (`"ldap-N"`, the provider issuer URL, or the local source id) and PHP passes the user-selected name through in the audit details (REQ-AUTH-067). Request body:

```json
{ "email": "user@example.org", "source": "oauth2", "provider": "https://idp.example.org", "source_name": "Hospital 1" }
```

`source_name` (optional, REQ-AUTH-067) is the name the user selected on the login page; the API records it in the `login_success` / `login_failure` audit details. It never affects authorization or identity resolution.

```json
{ "email": "user@example.org", "source": "local", "password": "***" }
```

```json
{ "email": "user@example.org", "source": "local", "password": "***", "mfa_code": "492817" }
```

API processing (in order):
0. **Local verification** (only when `source = "local"`, GD-18, REQ-AUTH-050/051): the user row exists — otherwise 401 `account_not_found` + audit `login_failure`; its `password_hash` is present and the password matches it — constant-time comparison (bcrypt) — otherwise 401 `bad_password` + audit `login_failure` (the two cases are not distinguished to the caller, and the password value is never logged, REQ-AUTH-036).
1. User row is **active** (REQ-AUTH-006, §4.4): `enabled = 1`, not expired (`valid_until`), not inactive — otherwise reject (403 `account_disabled` / 403 `account_expired`) and audit `login_failure` (the inactivity case first performs the auto-disable of §4.4, REQ-AUTH-053).
1.5. **Two-factor gate** (GD-21, REQ-AUTH-055/058, only for `source = "local"` or `"ldap"`): if the account's method is not `off`, a call without `mfa_code` → 401 `mfa_required` (body names the method); with an invalid, expired, or already-used code → audit `login_failure` (`bad_mfa_code`) + 401 `bad_mfa_code`; valid codes (§2.7) proceed and are recorded against replay. If `AUTH_REQUIRE_2FA` is on and the method is `off` → 401 `tfa_enrollment_required` (no audit failure — the first factor succeeded; enrollment follows, §2.7). Until this gate passes, no user object is returned and steps 3–5 do not run.
2. **Bootstrap promotion** (REQ-AUTH-007, GD-4): if the email equals `ADMIN_BOOTSTRAP_EMAIL`, ensure the row exists, is enabled, and has `is_admin = 1` (idempotent); on a first installation without an IdP, the row's `password_hash` is provisioned from `ADMIN_BOOTSTRAP_PASSWORD` (REQ-AUTH-051).
3. Update the row's `auth_source` to the call's `source` (REQ-AUTH-005) and set `last_login_at` to now (UTC, REQ-AUTH-053).
4. Record `login_success` (audit) — `source=ui`, acting user set, no token; when the gate of step 1.5 applied, its details name the factor used (`totp`, `email`, or `recovery`, REQ-AUD-028).
5. Return the user object.

Response (200):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "is_admin": true, "auth_source": "oauth2", "ui_language": "en", "ui_theme": null }
```

The API is stateless with respect to sessions: it MUST NOT create or store a session (GD-1, REQ-API-044).

### 2.4 Sequence D — logout (REQ-AUTH-015, REQ-API-045)

1. Browser `POST /logout` (CSRF token, REQ-AUTH-037).
2. PHP reads the acting user id from the session.
3. PHP → API `POST /api/v1/auth/logout` (service token + `X-Internal-User-Id`) → the API records `logout` (audit) and returns 200.
4. PHP destroys the session (cookie invalidated).
5. `302` to `/login`.

Normative ordering: the audit call **precedes** session destruction (REQ-AUTH-015); REQ-API-045's assignment of destruction to the PHP layer is satisfied by step 4.

Session inactivity timeout: `SESSION_LIFETIME` (default 8 h, REQ-AUTH-015, `System_Configuration_Design.md` §3.8); an expired session redirects to `/login` (REQ-UI-007).

### 2.5 Sequence E — brute-force protection (REQ-AUTH-035)

- Store: per host, in memory (`email → {failures, window_start, locked_until}`); lost on restart (allowed by REQ-AUTH-035's "in memory or session store").
- Rule: ≥ 5 failed logins for the same email within a rolling 10-minute window → further attempts for that email rejected for 15 minutes (429 on the login form; `login_failure` with reason `rate_limited`).
- The check runs **before** contacting the IdP/LDAP — and before the local hash check of Sequence F (fail fast; no credential probing against the directory or the hash column).
- A successful login clears the counter for that email.

### 2.6 Sequence F — local (table-based) attempt (GD-18, REQ-AUTH-050/051/065)

The email+password form of the login page (`User_Interface_Design.md` §2.2) initiates the credential race for the selected name (Sequence I, §2.9). When a local source is registered under that name, every submission includes this attempt:

1. Brute-force check (Sequence E) — once per submission, before any source is contacted (§2.5).
2. **Verify** — PHP → API `POST /api/v1/auth/verify-password` with `{ "email": "…", "password": "***" }` (service token; the password travels only on the trusted internal path, REQ-AUTH-034, and is never logged — REQ-AUTH-036). The API runs Sequence C steps 0–1 **without side effects** — no `last_login_at`, no `auth_source` write, no audit event (that is what makes it safe to fire alongside the LDAP attempts of Sequence B) — and returns one of `ok` / `bad_password` (also for an unknown account) / `account_disabled` / `account_expired`.
3. On `ok`, PHP **finalizes** with the single Sequence C call `POST /api/v1/auth/login` `{ "email": "…", "source": "local", "password": "***", "mfa_code"?: "…", "source_name": "Hospital 1" }`: the API re-verifies the hash (idempotent) and runs steps 1–5. Sequence C remains the only place login side effects happen, so a losing parallel attempt can never produce a second session or duplicate audit success (REQ-AUTH-065).
4. If every attempt of the name settles as failed: any `account_disabled` / `account_expired` outcome is surfaced as the specific reason (the account state — not the credential — is the problem); otherwise the login page shows the generic translated failure line, and exactly one `login_failure` is recorded for the submission (§2.9 step 5).

On success PHP establishes the session (§3) exactly as for the other paths; `auth_source` is `local`.

### 2.7 Sequence G — two-factor challenge and enrollment (GD-21, REQ-AUTH-054…059)

**Methods.** `off` (default), `totp` — RFC 6238 time-based one-time codes (SHA-1, 30 s step, 6 digits, ±1 step skew window) from a phone authenticator app; works with no network access on the user's side. `email` — a cryptographically random 6-digit code delivered to the account's address via the configured SMTP relay (`System_Configuration_Design.md` §3.11), valid for `TFA_EMAIL_CODE_TTL` (default 600 s), single-use, send rate-limited per account. Both methods issue 10 single-use **recovery codes** at activation (random, stored as one-way hashes, shown exactly once).

**Statelessness.** All second-factor state lives in the `user_two_factor` row (`Database_Schema_Design.md` §4): secret, last accepted TOTP step (a code is accepted only for a step strictly greater than the stored one — replay-proof), pending email code hash + expiry, recovery code hashes. The API keeps no challenge state of its own (GD-1).

**Challenge flow.** Sequence F/B receive 401 `mfa_required` from Sequence C step 1.5. PHP records the first-factor success in a **pre-authentication** session (`tfa_pending = {email, source, verified_at}` plus, on the local path, the **first-factor handle** verify-password issued — REQ-API-131; TTL 5 min) and renders the code form (no other page is reachable). Submitting the code re-invokes `POST /api/v1/auth/login` with `mfa_code` and, on the local path, that handle in place of the password: the user types one field, and no layer holds their password between the two calls (REQ-AUTH-036 — a PHP session holding it would be plaintext persistence). On success PHP promotes the pending session to a full session (§3) — `session_regenerate_id(true)` as always. Failed attempts count toward the Sequence E lockout; while locked, no verification runs and no email code is sent (REQ-AUTH-058). A handle that has expired or been invalidated answers 401 `first_factor_expired`, which is not a credential failure and does not extend the lockout — PHP clears the pending state and returns the user to the credential form.

**Mandated enrollment.** 401 `tfa_enrollment_required` (first factor OK, method `off`, `AUTH_REQUIRE_2FA` on) leads PHP to the enrollment wizard inside the pending session. The self-service endpoints (REQ-API-115) accept `X-Internal-User-Id` for the pending identity — the same service-token trust boundary as every other admin call; PHP only supplies an id whose first factor it just verified. That 401 carries the pending identity's `user_id`, which is what makes the wizard drivable pre-session (web-readiness amendment, 2026-09-29): PHP stores it in `tfa_pending` and presents it as `X-Internal-User-Id` on every wizard call. Wizard: TOTP enroll → secret + QR shown once → confirm with one current code; or email start/confirm (409 when SMTP is not configured). Activation writes the method and returns the recovery codes once; PHP then resumes the login call and completes normally.

**Self-service management.** A signed-in user reaches the same wizard from the account page (`User_Interface_Design.md`): change method, view status, disable — disable requires a valid current code or recovery code, so a hijacked session cannot silently drop the factor (REQ-API-115). Audit: `tfa_enrolled`, `tfa_disabled`, `tfa_reset` (REQ-AUD-028).

**Administrator reset.** `POST /api/v1/users/{id}/tfa/reset` (`is_admin`) deletes the row back to `off` and clears codes — for a user who lost their phone or email access; under a mandate the user re-enrolls at the next login (REQ-AUTH-059). The user overview shows each account's method (REQ-UI-011).

### 2.8 Sequence H — out-of-band password setup: invite and reset (GD-22/GD-23, REQ-AUTH-060/062)

**Shared token mechanics.** Invite and reset tokens are one mechanism with two purposes. Generation: 32 random bytes from `crypto/rand` (256 bits); the value is emailed, never stored — only its SHA-256 hash goes into `password_tokens` (`purpose` = `invite` | `reset`, expiry = now + `AUTH_PASSWORD_TOKEN_TTL_DAYS` days, default 7; REQ-CFG-030, REQ-DB-039). A new invite replaces the account's outstanding invite row (re-invite invalidates the old link, REQ-AUTH-060); a new reset request replaces the outstanding reset row. Completion verifies the hash in constant time and enforces purpose, expiry, and single use; **every failure answers the same 401 `invalid_setup_token`** — unknown, expired, consumed, or wrong purpose are not distinguished (REQ-API-120). The link URL carries the token as a query parameter to the PHP route `/set-password?token=…`; the proxy's `/api/` query-string logging rule is extended to the reset/invite email routes' URLs in PHP access logs (REQ-AUTH-049 by analogy — token values never land in logs).

**Invite (admin-initiated).** `POST /api/v1/users/{id}/invite` (`is_admin`, REQ-API-117): requires the account to be on the local path (or intended for it) and SMTP configured (else 409 `smtp_not_configured` — with no relay the admin sets the password directly, REQ-AUTH-060). The email (translated, per §5.7 of the UI design) links to `/set-password?token=…`; audit `user_invited`. Completion (`POST /api/v1/auth/invite/complete`, purpose `invite`) stores the new bcrypt hash, consumes the token, and audits `invite_accepted` — the person then logs in through Sequence F normally; the second-factor gate is **not** bypassed (REQ-AUTH-060).

**Reset request.** `POST /api/v1/auth/password-reset/request` with `{ "email": "…" }`: the response is a fixed 202 body regardless of outcome (REQ-AUTH-062 — no enumeration oracle). Internally: an **active local account** (`enabled = 1`, not expired, per §4.4) matching the address receives the emailed link; anything else sends nothing yet answers identically. Rate limit: default 3 requests / 15 minutes per address (plus the general IP limiter, `System_Configuration_Design.md` §3.9); audit `password_reset_requested` with the submitted address and source IP — never a token.

**Reset completion.** `POST /api/v1/auth/password-reset/complete` with `{ "token": "…", "password": "***" }`: verifies per the shared mechanics (purpose `reset`), stores the new bcrypt hash, consumes the token, **invalidates all outstanding tokens of the account**, and audits `password_reset_completed`. No temporary password exists at any point (REQ-AUTH-062). Existing PHP sessions are not revoked — DEV-AUTH-13; they end via logout or timeout (REQ-AUTH-015).

**Pre-authentication boundary.** The three Sequence H endpoints ride the same internal trust path as `POST /api/v1/auth/login`: service token required, no `X-Internal-User-Id` (the REQ-API-041 exception set — DEV-API-16), unreachable from outside (§1.2). PHP's public routes (`/password-reset`, `/set-password`) are the only browser-facing entry points and carry CSRF protection like every other form (REQ-AUTH-037); the token itself is the credential, so no session or identity is implied before completion.

**Self-service change (signed-in).** Not part of Sequence H's pre-auth path: `PUT /api/v1/users/me/password` verifies `current_password` against the hash first (failure → 401 `bad_password`, audit-logged, counted toward the Sequence E lockout), then stores the new hash and audits `password_changed` (REQ-AUTH-061, REQ-API-118).

### 2.9 Sequence I — named source selection and the parallel credential race (master spec "Authentication order", REQ-AUTH-063…067, DEV-AUTH-14)

**Source registry.** Every configured authentication path is a **source**: kind `local` | `ldap` | `oauth2`, carrying one or more display **names**; the relation between names and sources is many-to-many (REQ-AUTH-064). Names extend the existing per-index configuration (`OAUTH2_N_*`, REQ-CFG-011; `LDAP_SERVER_N_*`, REQ-CFG-012) with a name list per source — the variable set is owned by `System_Configuration_Design.md` §3 (follow-up registration). The local kind exists at most once (one `users` table, GD-18) but may carry several names.

**Login page.** `GET /login` renders the distinct set of names across all configured sources ("Hospital 1", "Hospital 2", … — REQ-AUTH-063). With exactly one distinct name the picker is skipped and that name applies implicitly; sources configured without a name form one implicit default set (REQ-AUTH-067). The selected name is stored in the session (`auth_source_name`, §3) before any credential exchange.

**Dispatch on submit.** For the selected name, PHP dispatches concurrently:

1. every **local** source under the name → the verify step of Sequence F (2.6);
2. every **LDAP** source under the name → Sequence B (search + bind-as-user), each with its own connect timeout so one unreachable directory cannot stall the race;
3. **OAuth2** sources under the name do **not** join the credential race — they are interactive: exactly one provider redirects immediately (Sequence A), several are offered for individual selection (REQ-AUTH-066).

The submitted email+password is sent **only** to the sources under the selected name — never to a source registered under a different name (REQ-AUTH-063; this replaces the old chain, which forwarded a failed local password onward to every configured LDAP server).

**First "login ok" wins.** The first successful attempt finalizes through Sequence C (with `source`, `provider` of the winner and `source_name`). A later success from another attempt is discarded at PHP before finalization — since non-winning attempts are side-effect-free (Sequence F step 2, Sequence B binds), no second session, duplicate audit success, or `last_login_at` / `auth_source` write can occur (REQ-AUTH-065). If a winner arrives while another attempt is still in flight, the in-flight result is simply ignored on arrival.

**All failed.** When every attempt settles as failed: any `account_disabled` / `account_expired` outcome is surfaced as the specific reason; otherwise the generic credential failure line is shown. Exactly one `login_failure` audit event is recorded for the submission (reason `bad_credentials`, or `provider_unavailable` when no source could be reached), with the selected name and the per-source outcomes in `details` (`Audit_Logging_Design.md` §3.1).

**New pre-auth endpoint.** `POST /api/v1/auth/verify-password` joins the `X-Internal-User-Id` exemption set (§2.3, §6 — DEV-API-16): service token required, internal-only (§1.2), rate-limit and lockout rules as every login path (Sequence E runs in PHP before dispatch). It performs Sequence C steps 0–1 only and returns `ok` / `bad_password` / `account_disabled` / `account_expired` without touching any row or writing audit events; the password is never logged (REQ-AUTH-036).

**Interaction with the second factor.** The MFA gate stays in Sequence C step 1.5 on the winning source: the race answers *who you are*, the second factor then confirms it before any session exists (§2.7, REQ-AUTH-055) — losing attempts never reach the gate.

## 3. Session Schema (GD-1)

The session is owned by the PHP web application: PHP-native session, file storage in `SESSION_DIR` (MUST NOT be the application database, REQ-CFG-017). The API never sees the session (GD-1, REQ-API-044).

**Cookie** (REQ-TECH-019): name `SESSION_COOKIE_NAME`; `HttpOnly`, `Secure` in production (`SESSION_COOKIE_SECURE`), `SameSite=Lax`. On login, `session_regenerate_id(true)` is performed (session-fixation defense).

| Key | Set | Value | Cleared |
|---|---|---|---|
| `user_id` | login | integer | logout / timeout |
| `email` | login | string | |
| `display_name` | login | string | |
| `is_admin` | login | `0`/`1` | |
| `auth_source` | login | `oauth2`/`ldap`/`local` | |
| `auth_source_name` | `/login` selection (§2.9) | the user-selected source name (REQ-AUTH-067); updated at each login | |
| `issued_at` | login | unix timestamp | |
| `csrf_token` | session start | 32-byte random hex | session regeneration |
| `oauth2_state` | `/login` | 16-byte random hex | callback (single use) |
| `oauth2_pkce_verifier` | `/login` | random (S256) | callback (single use) |
| `tfa_pending` | login 401 `mfa_required` / `tfa_enrollment_required` (§2.7) | `{email, source, verified_at}` plus `user_id` on `tfa_enrollment_required` (drives the wizard, §2.7), plus `first_factor` on the local path — the handle of REQ-API-131 that stands in for the password when the code is submitted, so the credential is never persisted here — **pre-authentication**: not a session; every page guard rejects it | second factor verified or enrollment completed (promoted via `session_regenerate_id(true)`), or TTL 5 min / abort |

The identity keys above are written **only after the two-factor gate has passed** for accounts with a method other than `off` (GD-21, §2.7); while only `tfa_pending` exists, no authenticated page is reachable.

All page reads go through a single `require_login()` helper; a missing session redirects to `/login`. The session stores **identity only** — no permissions, no project tokens, no data: permissions are re-derived from the API on every request (REQ-AUTH-033), and project tokens live in `user_projects.token`, never in the session.

The reference application's `AC.php` (master spec, "Details"; `assets/table_based_authentication_plus_user_management/AC.php`) is the model for this PHP session-establishment flow — a per-page access-control include that checks the session and redirects to login when it is absent, resolving the identity by name **or** institutional email. Only that *session pattern* is adopted (REQ-UI-032, REQ-TECH-025); the authentication mechanism itself (§2), the credential handling, and the storage (the database, not the reference's JSON file) follow this document and the fixed requirements (REQ-AUTH-036, REQ-TECH-020/024).

## 4. Authorization Evaluation (ASM-AUTH-5)

A single explicit function in the Go API — no external policy engine. Inputs: subject, `is_admin`, role, arm, data access level, export level, active data access group (ASM-AUTH-5). Every decision is explicit and auditable (REQ-AUTH-019).

### 4.1 Effective levels per (user, project)

```
effective_levels(user, project):
  if user.is_admin:
      every arm:  data = edit_survey_responses, export = export_full
      project_admin = true                                        (REQ-AUTH-023)
  else if the assignment's role is set:
      for each arm a in arms(project):
          data_level   = role.arms[a].data    or no_access        (REQ-AUTH-019 — no implicit access)
          export_level = role.arms[a].export  or export_none
      project_admin = role.project_admin
  else:   # member without a role
      every arm:  data = edit_survey_responses, export = export_full
      project_admin = true                                        (REQ-AUTH-022)
```

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

The endpoint→permission mapping is the normative summary in `API_Endpoints_Requirements.md` §4.21. Access to a project or record the caller is not entitled to is rejected with a consistent 403 that does not disclose existence (REQ-API-007, REQ-AUTH-026).

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
     AUTO-DISABLE: set enabled = 0, last_login_at = NULL   (same transaction)
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
- Accepted **only** as the `token` request-body/query parameter of the REDCap protocol — never an `Authorization` header (REQ-AUTH-031, REQ-API-010).
- Validation is a single indexed lookup (REQ-AUTH-032); a missing/invalid token yields the same 401 `Invalid token` response whether or not the token exists (REQ-API-011).
- Rotation (REQ-AUTH-030): the new value replaces the old in the single row — the previous token is invalid immediately; audit `token_issued` / `token_rotated` / `token_revoked` (`Audit_Logging_Design.md` §3.4).
- Member removal invalidates the token immediately (REQ-API-055); disabling a user denies the effective permissions of that user's tokens at call time (REQ-API-048, §4.3).
- Token values MUST NOT appear in application logs (REQ-API-005); the audit trail MAY record them in the fixed `token` column (`Audit_Logging_Design.md` §2).

## 6. Service Token Rules (internal boundary)

- `X-Internal-Service-Token` is compared in constant time (`crypto/subtle.ConstantTimeCompare`) and MUST NOT be logged (REQ-AUTH-012); a missing/invalid token → 401 + audit `admin_rejected` — enforced regardless of any other configuration (REQ-CFG-023).
- `X-Internal-User-Id` is authoritative for authorization on `/api/v1/*` (REQ-AUTH-013); an unknown or disabled user → 403 + audit `admin_rejected`.
- The proxy strips both headers from every externally-originated request (REQ-AUTH-014; configuration in `Technology_Stack_Design.md` §5).
- `POST /api/v1/auth/login` (service token only, no user id — §2.3), the three pre-authentication password endpoints of Sequence H (§2.8), and `POST /api/v1/auth/verify-password` (§2.9) are the exceptions.

## 7. Non-Functional Security

- **Password handling** (REQ-AUTH-036, GD-18): the LDAP password exists only transiently — it goes over TLS to the directory and is never persisted or logged. Local (table-based) passwords are verified against a **bcrypt** hash (`cost ≥ 10`, constant-time compare — `golang.org/x/crypto/bcrypt` in the Go API) stored in `users.password_hash`; the plaintext exists only in flight (browser → PHP over TLS; PHP → API over the trusted internal path) and is never logged, never in audit `details`, and never returned by any endpoint. Setting/resetting a password stores only the new hash; clearing it sets the column to `NULL` (the account keeps its other paths). Out-of-band setup tokens (Sequence H) exist in plaintext only inside the generated email and the completion request — the store holds only the SHA-256 hash (`password_tokens`, REQ-DB-039), and token values never appear in logs or audit details (REQ-AUTH-060/062, REQ-AUD-029).
- **Two-factor material** (GD-21, REQ-AUTH-056/057/058): TOTP secrets are 160-bit `crypto/rand` values (base32 for the QR/`otpauth://` URI); RFC 6238 verification uses standard-library `crypto/hmac` + `crypto/sha1` — no new dependency. Email codes and recovery codes come from `crypto/rand`. Pending email codes and recovery codes are persisted **as one-way hashes only** (`user_two_factor`, REQ-DB-038); the TOTP secret is stored as issued, never returned by any endpoint after its single enrollment display, and never written to logs or audit details. Email delivery uses standard-library `net/smtp` from the API against the configured relay; message bodies (the code) are never logged.
- **CSRF** (REQ-AUTH-037, REQ-UI-005): every state-changing browser request carries the per-session `csrf_token` (form field or `X-CSRF-Token` header); the administration API is additionally protected by the service-token boundary (GD-1).
- **Trusted path** (REQ-AUTH-034): PHP→API traffic on loopback or TLS.
- **Log hygiene** (REQ-AUTH-049, REQ-API-005): the proxy does not log `/api/` query strings (or redacts `token=…`); neither component logs the service token, IdP secrets, or record values.

## 8. Resolved Deferred Items

| Deferred | Resolution here |
|---|------|
| session ownership and schema (GD-1) | PHP-native session, key table §3; the API is stateless |
| logout ordering (REQ-AUTH-015 vs REQ-API-045) | the audit call precedes session destruction (§2.4) |
| `X-Internal-User-Id` on `POST /api/v1/auth/login` (REQ-API-041) | the login endpoint is exempt — service token only; the externally authenticated identity arrives in the body (§2.3); the three Sequence H pre-auth endpoints share the exemption (§2.8, DEV-API-16) |
| brute-force store and windows (REQ-AUTH-035) | in-memory per host; 5 failures / 10 min → 15 min lockout (§2.5) |
| OAuth2 flow hardening | PKCE (S256) + single-use `state` (§2.1) |
| `login_failure` reason vocabulary | `provider_unavailable \| state_mismatch \| bad_credentials \| bad_password \| account_not_found \| account_disabled \| account_expired \| rate_limited \| bad_mfa_code` (`Audit_Logging_Design.md` §3.1) |
| table-based authentication (owner decision 2026-09-22, GD-18) | `users.password_hash` (bcrypt) + `auth_source = local`; Sequence F before the LDAP fallback (ordering superseded by Sequence I, DEV-AUTH-14); bootstrap password from `ADMIN_BOOTSTRAP_PASSWORD` (required at startup when no IdP is configured); `REQ-AUTH-036` revised to "no plaintext, hash only" (§2.2/§2.3/§2.6, §7) |
| account validity and inactivity (owner decision 2026-09-22, GD-19) | `users.valid_until` / `users.last_login_at`; the account-active rule of §4.4 evaluated at every authentication check; auto-disable + `account_auto_disabled` audit; admin re-enable resets the inactivity clock |
| two-factor authentication (owner decision 2026-09-28, GD-21) | `user_two_factor` (§2.7); Sequence C step 1.5 gate on local/LDAP logins only — OAuth2 MFA stays with the IdP (DEV-AUTH-10); TOTP (stdlib RFC 6238) + email codes (`net/smtp`); recovery codes; `tfa_pending` pre-auth session (§3); mandate `AUTH_REQUIRE_2FA`; admin reset |
| account onboarding and password lifecycle (owner decision 2026-09-28, GD-22/GD-23) | Sequence H (§2.8): `password_tokens` (REQ-DB-039) — invite and reset share single-use hashed expiring tokens (`AUTH_PASSWORD_TOKEN_TTL_DAYS`, REQ-CFG-030); the three pre-auth endpoints extend the REQ-API-041 exception set (DEV-API-16); generic reset response (no enumeration), no temp passwords, current-password proof for self-service change; sessions not revoked (DEV-AUTH-13) |
| authentication order (owner decision 2026-09-29, master spec "Authentication order") | Sequence I (§2.9): user selects a source **name** before login (many-to-many with sources); local+LDAP sources under the name race in parallel, first "login ok" wins via side-effect-free `verify-password` + single Sequence C finalization; OAuth2 stays interactive within the name; credentials confined to the selected set; supersedes the sequential LDAP fallback and local-first ordering (DEV-AUTH-14) |

## 9. Open Items

None.
