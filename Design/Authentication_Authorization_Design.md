# Authentication & Authorization — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Authentication_Authorization_Requirements.md`
**Date:** 2026-09-21

## 1. Trust Model

### 1.1 Actors and surfaces

| Actor | Surface | Credential | Grants |
|---|---|---|---|
| browser (member) | PHP routes only (REQ-AUTH-010) | session cookie (§3) | the member's effective permissions |
| PHP web application | `/api/v1/*` (internal path only) | `X-Internal-Service-Token` + `X-Internal-User-Id` (REQ-AUTH-011) | exactly the acting user's permissions — PHP adds none |
| external data-API caller (Fiona/RIS) | `POST /api/` (public) | project token as `token` parameter (REQ-AUTH-031) | the token holder's per-arm levels (REQ-AUTH-033) |
| survey respondent | PHP route `/survey/{link}` (public, no session — GD-9) | opaque link token | fill-only on exactly one (record, survey instrument) (REQ-AUTH-039) |
| IdP / LDAP | outbound from PHP | OAuth2 client secret / LDAP bind DN | identity assertion (email) |
| local (table-based) account | PHP login form → API | email + password (verified against `users.password_hash`) | the account's normal permissions (GD-18, REQ-AUTH-050) |

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

### 2.1 Sequence A — OAuth2 authorization code (primary, REQ-AUTH-001/002)

1. Browser opens `/login`. PHP generates `state` (16-byte random hex) and a PKCE `code_verifier` (random, challenge method S256), stores both in the session, and redirects to the provider's `authorize` endpoint with `response_type=code`, `client_id`, `redirect_uri = WEB_PUBLIC_URL + /auth/callback`, `state`, `code_challenge`.
2. The user authenticates at the IdP; the provider redirects the browser to `/auth/callback?code&state`.
3. PHP verifies `state` against the session value (**single use**; mismatch → abort, no session, `login_failure` with reason `state_mismatch`).
4. PHP exchanges `code` + `code_verifier` + `client_secret` for tokens at the `token` endpoint — server-side, over TLS (REQ-AUTH-001).
5. PHP resolves the identity: `userinfo` response (or ID token) → email via `OAUTH2_N_EMAIL_ATTR` (REQ-AUTH-004).
6. PHP calls the API's login endpoint (Sequence C) with `source:"oauth2"`.
7. On success PHP establishes the session (§3) and redirects to `/`.

Any failure at steps 3–5 falls through to the LDAP fallback (Sequence B); if no LDAP server is configured either, the login fails and is audit-logged (`login_failure`, `Audit_Logging_Design.md` §3.1).

### 2.2 Sequence B — LDAP fallback (up to 3 servers, REQ-AUTH-003)

The PHP login form carries email + password for the **local (Sequence F) and LDAP** paths; on the LDAP path the password is sent to the directory and never stored or logged (REQ-AUTH-036). Sequence B runs **after** the local attempt of Sequence F has failed (REQ-AUTH-051). For server `N` = 1, 2, 3 (in order; stop at the first success — REQ-AUTH-003):

1. **Search** (using `LDAP_SERVER_N_BIND_DN`/`BIND_PASSWORD`, or anonymous): find the entry in `SEARCH_BASE` whose `UID_ATTR` matches the login name; read `EMAIL_ATTR` and `NAME_ATTR` (REQ-CFG-012).
2. **Bind-as-user** (ASM-AUTH-2): simple bind with the entry DN and the supplied password. Success → identity is the entry's email (REQ-AUTH-004); bind failure → next server.
3. On the first successful server: PHP calls the API's login endpoint (Sequence C) with `source:"ldap"`, `provider:"ldap-N"`, and the identity.
4. All servers exhausted → `login_failure` (reason `bad_credentials` when an entry was found, `provider_unavailable` when unreachable).

### 2.3 Sequence C — API login call (REQ-AUTH-016, REQ-API-044)

`POST /api/v1/auth/login` — the **only** administration endpoint exempt from `X-Internal-User-Id` (it establishes the user context; it still requires a valid `X-Internal-Service-Token` and is internal-only per §1.2). Request body:

```json
{ "email": "user@example.org", "source": "oauth2", "provider": "https://idp.example.org" }
```

```json
{ "email": "user@example.org", "source": "local", "password": "***" }
```

API processing (in order):
0. **Local verification** (only when `source = "local"`, GD-18, REQ-AUTH-050/051): the user row exists — otherwise 401 `account_not_found` + audit `login_failure`; its `password_hash` is present and the password matches it — constant-time comparison (bcrypt) — otherwise 401 `bad_password` + audit `login_failure` (the two cases are not distinguished to the caller, and the password value is never logged, REQ-AUTH-036).
1. User row is **active** (REQ-AUTH-006, §4.4): `enabled = 1`, not expired (`valid_until`), not inactive — otherwise reject (403 `account_disabled` / 403 `account_expired`) and audit `login_failure` (the inactivity case first performs the auto-disable of §4.4, REQ-AUTH-053).
2. **Bootstrap promotion** (REQ-AUTH-007, GD-4): if the email equals `ADMIN_BOOTSTRAP_EMAIL`, ensure the row exists, is enabled, and has `is_admin = 1` (idempotent); on a first installation without an IdP, the row's `password_hash` is provisioned from `ADMIN_BOOTSTRAP_PASSWORD` (REQ-AUTH-051).
3. Update the row's `auth_source` to the call's `source` (REQ-AUTH-005) and set `last_login_at` to now (UTC, REQ-AUTH-053).
4. Record `login_success` (audit) — `source=ui`, acting user set, no token.
5. Return the user object.

Response (200):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "is_admin": true, "auth_source": "oauth2", "ui_language": "en" }
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

### 2.6 Sequence F — local (table-based) login (GD-18, REQ-AUTH-050/051)

The email+password form of the login page (present when no OAuth2 provider is configured, or as the fallback form beside the provider buttons — `User_Interface_Design.md` §2.2) initiates, in order:

1. Brute-force check (Sequence E).
2. PHP → API `POST /api/v1/auth/login` with `{ "email": "…", "source": "local", "password": "***" }` (service token; the password travels only on the trusted internal path, REQ-AUTH-034, and is never logged — REQ-AUTH-036). The API performs Sequence C step 0 (hash verification) and steps 1–5.
3. On **401** (`account_not_found` or `bad_password`) **and** LDAP servers are configured: PHP runs Sequence B (LDAP); a successful LDAP bind logs the user in with `source: "ldap"`. On **403** (`account_disabled` / `account_expired`) PHP surfaces the specific reason and does **not** fall through (the account state — not the credential — is the problem).
4. Otherwise: `login_failure` is already audit-logged by the API; the login page shows the translated failure line.

On success PHP establishes the session (§3) exactly as for the other paths; `auth_source` is `local`.

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
| `issued_at` | login | unix timestamp | |
| `csrf_token` | session start | 32-byte random hex | session regeneration |
| `oauth2_state` | `/login` | 16-byte random hex | callback (single use) |
| `oauth2_pkce_verifier` | `/login` | random (S256) | callback (single use) |

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

The endpoint→permission mapping is the normative summary in `API_Endpoints_Requirements.md` §4.19. Access to a project or record the caller is not entitled to is rejected with a consistent 403 that does not disclose existence (REQ-API-007, REQ-AUTH-026).

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
- `POST /api/v1/auth/login` (service token only, no user id — §2.3) is the sole exception.

## 7. Non-Functional Security

- **Password handling** (REQ-AUTH-036, GD-18): the LDAP password exists only transiently — it goes over TLS to the directory and is never persisted or logged. Local (table-based) passwords are verified against a **bcrypt** hash (`cost ≥ 10`, constant-time compare — `golang.org/x/crypto/bcrypt` in the Go API) stored in `users.password_hash`; the plaintext exists only in flight (browser → PHP over TLS; PHP → API over the trusted internal path) and is never logged, never in audit `details`, and never returned by any endpoint. Setting/resetting a password stores only the new hash; clearing it sets the column to `NULL` (the account keeps its other paths).
- **CSRF** (REQ-AUTH-037, REQ-UI-005): every state-changing browser request carries the per-session `csrf_token` (form field or `X-CSRF-Token` header); the administration API is additionally protected by the service-token boundary (GD-1).
- **Trusted path** (REQ-AUTH-034): PHP→API traffic on loopback or TLS.
- **Log hygiene** (REQ-AUTH-049, REQ-API-005): the proxy does not log `/api/` query strings (or redacts `token=…`); neither component logs the service token, IdP secrets, or record values.

## 8. Resolved Deferred Items

| Deferred | Resolution here |
|---|---|
| session ownership and schema (GD-1) | PHP-native session, key table §3; the API is stateless |
| logout ordering (REQ-AUTH-015 vs REQ-API-045) | the audit call precedes session destruction (§2.4) |
| `X-Internal-User-Id` on `POST /api/v1/auth/login` (REQ-API-041) | the login endpoint is the sole exception — service token only; the externally authenticated identity arrives in the body (§2.3) |
| brute-force store and windows (REQ-AUTH-035) | in-memory per host; 5 failures / 10 min → 15 min lockout (§2.5) |
| OAuth2 flow hardening | PKCE (S256) + single-use `state` (§2.1) |
| `login_failure` reason vocabulary | `provider_unavailable \| state_mismatch \| bad_credentials \| bad_password \| account_not_found \| account_disabled \| account_expired \| rate_limited` (`Audit_Logging_Design.md` §3.1) |
| table-based authentication (owner decision 2026-09-22, GD-18) | `users.password_hash` (bcrypt) + `auth_source = local`; Sequence F before the LDAP fallback; bootstrap password from `ADMIN_BOOTSTRAP_PASSWORD` (required at startup when no IdP is configured); `REQ-AUTH-036` revised to "no plaintext, hash only" (§2.2/§2.3/§2.6, §7) |
| account validity and inactivity (owner decision 2026-09-22, GD-19) | `users.valid_until` / `users.last_login_at`; the account-active rule of §4.4 evaluated at every authentication check; auto-disable + `account_auto_disabled` audit; admin re-enable resets the inactivity clock |

## 9. Open Items

None.
