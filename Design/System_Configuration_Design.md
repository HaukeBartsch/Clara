# System Configuration — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/System_Configuration_Requirements.md`\
**Date:** 2026-09-21

## 1. Purpose

Defines the complete configuration variable inventory, the defaults, and the startup validation rules for both components. §3 is the **canonical inventory** that REQ-CFG-003 points to: the Go API and the PHP app read exactly these variable names, so a single `.env` file configures the whole system (ASM-CFG-1).

## 2. Mechanism (REQ-CFG-001…006)

- Environment variables exclusively; optionally loaded from a `.env` file in the working directory of each component. `.env` is git-ignored; the repository ships `.env.example` (REQ-CFG-001).
- Precedence: process environment **overrides** `.env` values (REQ-CFG-002).
- Configuration is read once at process start and is immutable at runtime; there is no reload endpoint — a restart applies changes (REQ-CFG-006, ASM-CFG-2).
- Secrets are never written to logs; the startup dump masks them (§4.4, REQ-CFG-021/022).

## 3. Variable Inventory (canonical)

`Required` = startup fails without it (REQ-CFG-004/005). `—` = no default; required only where stated. `dev` = development defaults, `prod` = production defaults (REQ-CFG-007).

### 3.1 Core

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `APP_ENV` | both | `development`\|`production` | `development` | — | environment switch; selects the defaults marked `dev`/`prod` (REQ-CFG-007) |
| `API_ADDR` | api | host:port | `127.0.0.1:8080` | — | listen address (internal only, §5 of `Technology_Stack_Design.md`) |
| `API_ALLOW_REMOTE` | api | `0`\|`1` | `0` | — | names the exception required before `API_ADDR` may bind an address other hosts can reach: production refuses a non-loopback bind without it (§4.1, REQ-CFG-033), because `INTERNAL_SERVICE_TOKEN` plus the actor header is the whole credential for every account (§3.3). A loopback bind never needs it; `development` ignores it |
| `WEB_PUBLIC_URL` | web (api: docs links) | absolute URL | — | prod | public base URL; used for the OAuth2 redirect URI, survey link URLs, and documentation links (REQ-CFG-018) |
| `LOG_LEVEL_API` | api | `debug`\|`info`\|`warn`\|`error` | dev: `debug`, prod: `info` | — | (REQ-CFG-019) |
| `LOG_LEVEL_WEB` | web | `debug`\|`info`\|`warn`\|`error` | dev: `debug`, prod: `info` | — | (REQ-CFG-019) |

### 3.2 Database

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `DB_CONNECTION` | api | `sqlite`\|`mariadb` | dev: `sqlite`, prod: `mariadb` | — | engine selector (REQ-CFG-008) |
| `DB_DATABASE` | api | path (sqlite) / name (mariadb) | dev: `./data/app.sqlite` | — | for `sqlite`: file path, may point at a per-developer or per-test file (REQ-CFG-009); for `mariadb`: database name |
| `DB_HOST` | api | host | `127.0.0.1` | mariadb | (REQ-CFG-008) |
| `DB_PORT` | api | port | `3306` | mariadb | |
| `DB_USERNAME` | api | string | — | mariadb | |
| `DB_PASSWORD` | api | string (secret) | — | mariadb | |

### 3.3 Service boundary and bootstrap

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `INTERNAL_SERVICE_TOKEN` | both (web sends, api verifies) | string (secret) | dev: `dev-internal-token` (accepted **only** when `APP_ENV=development`) | prod | shared secret for `X-Internal-Service-Token` (REQ-CFG-013, REQ-AUTH-011); no production default |
| `ADMIN_BOOTSTRAP_EMAIL` | api | email | — | — | bootstrap administrator (GD-4, REQ-CFG-014); promoted on first login (REQ-AUTH-007) |
| `ADMIN_BOOTSTRAP_PASSWORD` | api | string (secret) | — (no default) | if no OAuth2 provider **and** no LDAP server (§4.1) | local password of the bootstrap administrator (GD-18, REQ-CFG-025, REQ-AUTH-051); stored only as an argon2id hash in `users.password_hash`; never logged (§4.4) |
| `LOCAL_LOGIN_NAMES` | web | comma-separated display names | unset → implicit default set | — | names the table-based (local) source is selected by on the login page — the local kind exists at most once but may carry several names (master spec "Authentication order", REQ-CFG-032, REQ-AUTH-064; `Authentication_Authorization_Design.md` §2.9) |

### 3.4 OAuth2 (providers 1–3; at least one provider or one LDAP server required)

Per provider `N` (1…3):

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `OAUTH2_N_ISSUER` | web | absolute URL | — | if provider N used | provider root (authorize/token/userinfo endpoints derived, ASM-AUTH-1) |
| `OAUTH2_N_CLIENT_ID` | web | string | — | if provider N used | (REQ-CFG-011) |
| `OAUTH2_N_CLIENT_SECRET` | web | string (secret) | — | if provider N used | (REQ-CFG-011) |
| `OAUTH2_N_REDIRECT_URI` | web | absolute URL | `WEB_PUBLIC_URL + /auth/callback` | — | (REQ-CFG-011) |
| `OAUTH2_N_EMAIL_ATTR` | web | string | `email` | — | claim/attribute mapped to the system user (REQ-CFG-011, REQ-AUTH-004) |
| `OAUTH2_N_NAMES` | web | comma-separated display names | unset → implicit default set | — | names this provider is selected by on the login page (master spec "Authentication order", REQ-CFG-032, REQ-AUTH-063/064); entries empty after trimming rejected at startup (§4.1) |

### 3.5 LDAP (servers 1–3, tried in parallel within the selected source name; 2 and 3 optional)

Per server `N` (1…3):

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `LDAP_SERVER_N_URL` | web | `ldap://…`\|`ldaps://…` | — | if server N used | (REQ-CFG-010/012) |
| `LDAP_SERVER_N_BIND_DN` | web | string | `""` (anonymous search) | — | directory-read bind DN (REQ-CFG-012, ASM-AUTH-2) |
| `LDAP_SERVER_N_BIND_PASSWORD` | web | string (secret) | `""` | — | (REQ-CFG-012) |
| `LDAP_SERVER_N_SEARCH_BASE` | web | DN | — | if server N used | (REQ-CFG-012) |
| `LDAP_SERVER_N_UID_ATTR` | web | attribute | `uid` | — | (REQ-CFG-012) |
| `LDAP_SERVER_N_EMAIL_ATTR` | web | attribute | `mail` | — | identity mapping (REQ-AUTH-004) |
| `LDAP_SERVER_N_NAME_ATTR` | web | attribute | `cn` | — | display name (REQ-CFG-012) |
| `LDAP_SERVER_N_NAMES` | web | comma-separated display names | unset → implicit default set | — | names this server is selected by on the login page (REQ-CFG-032, REQ-AUTH-063/064); all LDAP servers under the selected name are tried in parallel (`Authentication_Authorization_Design.md` §2.9) |

### 3.6 Anonymization (api)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `ANON_SALT` | api | string (secret) | dev: `dev-anon-salt` (accepted **only** in development) | prod | salt for the date-shift algorithm (REQ-CFG-015; `Database_Schema_Design.md` §8); no production default |
| `ANON_DATE_SHIFT_MIN` | api | integer ≥ 0 | `0` | — | (REQ-CFG-016) |
| `ANON_DATE_SHIFT_MAX` | api | integer ≥ MIN | `364` | — | (REQ-CFG-016); `offset_days = MIN + SHA-256(project_id‖':'‖record_id‖':'‖ANON_SALT) mod (MAX − MIN + 1)` — with the defaults this is exactly the `mod 365` of `Database_Schema_Design.md` §8 |

### 3.7 Data validation (api)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `MAX_VALUE_BYTES` | api | integer ≥ 1 | unset → no application cap | — | optional finite cap for the content policy step 3 (`Data_Validation_Design.md` §5.1; REQ-VAL-021/ASM-VAL-2) |

### 3.8 Sessions and CSRF (web)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `SESSION_DIR` | web | directory path | OS temp dir | — | MUST be a directory, never the application database file (REQ-CFG-017) |
| `SESSION_COOKIE_NAME` | web | string | `csms_session` | — | letters, digits, `_` (REQ-CFG-017); sent as `__Host-<name>` whenever the cookie is `Secure` (REQ-AUTH-075) |
| `SESSION_LIFETIME` | web | seconds ≥ 60 | `28800` (8 h) | — | absolute lifetime from sign-in, however active the session (REQ-CFG-017, REQ-AUTH-015) |
| `SESSION_IDLE_TIMEOUT` | web | seconds: `0` or ≥ 60 | `1800` (30 min) | — | ends a session with no request for this long; `0` = off (REQ-CFG-017, REQ-AUTH-015) |
| `SESSION_ROTATE_INTERVAL` | web | seconds: `0` or ≥ 120 | `900` (15 min) | — | moves a signed-in session to a new id this often; the replaced id follows its successor for 60 s, after which it counts as a replay (REQ-AUTH-072/073); `0` = off, which also disables replay detection; the minimum is twice the grace window |
| `SESSION_COOKIE_SECURE` | web | `0`\|`1` | dev: `0`, prod: `1` | — | `Secure` flag; `0` is refused under `APP_ENV=production` (REQ-CFG-007, REQ-TECH-019, REQ-AUTH-075) |

### 3.9 Rate limiting (api, web)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `TRUSTED_PROXY_CIDRS` | api, web | comma-separated CIDR list | `127.0.0.0/8,::1` | — | address ranges whose `X-Real-IP` header may name the caller (REQ-API-125); loopback covers the same-host nginx + PHP-FPM deployment (`Technology_Stack_Design.md` §5) |

Both components read this one list and apply the same rule to their own direct peer: the API keys its limiter on a forwarded address only when the TCP peer sits inside the list (`dataapi.SourceIP`), and the web layer decides what it forwards by the same test (`Request::clientIp`, `Clara\TrustedProxies`). A header arriving from anywhere else is ignored in both, so a browser cannot choose the address it is limited by or written into an audit entry however nginx handles `fastcgi_param HTTP_X_REAL_IP` (finding F9). An unset value keeps the default; a malformed entry refuses startup like any other variable (§4.1).

The rate-limit **thresholds are not environment variables**: `rate_limit_enabled` (default off), `rate_limit_rpm` (default 600 requests per minute per source IP) and `rate_limit_block_minutes` (default 10 minutes an over-budget source IP stays blocked, REQ-API-113) are system settings stored in the database and edited in the administration interface, effective without a restart (REQ-CFG-020, REQ-API-112, REQ-DB-037, `API_Endpoints_Design.md` §4.22). The former keys `RATE_LIMIT_ENABLED`/`RATE_LIMIT_RPM` are removed (DEV-CFG-3).

### 3.10 Account policy and time (api)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `AUTH_INACTIVITY_LIMIT_DAYS` | api | integer ≥ 0 | `180` | — | inactivity auto-disable limit (GD-19, REQ-CFG-024, REQ-AUTH-053): `last_login_at` older than this → auto-disabled at the next authentication check; `0` = rule off |
| `AUTH_PASSWORD_TOKEN_TTL_DAYS` | api | integer ≥ 1 | `7` | — | validity in days of invite and password-reset tokens (GD-22/GD-23, REQ-CFG-030, REQ-AUTH-060/062; `password_tokens.expires_at`, `Authentication_Authorization_Design.md` §2.8) |
| `APP_TIMEZONE` | api | IANA timezone name | `UTC` | — | default collection timezone (GD-16, REQ-CFG-026, REQ-VAL-041): the offset used for date/date-time values imported without an explicit zone (no browser zone, no `tz` parameter); resolved to the `±HH:MM` offset at the value's date (DST-aware) |

### 3.11 Two-factor authentication (api)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `AUTH_REQUIRE_2FA` | api | `0`\|`1` | `0` | — | installation-wide mandate (GD-21, REQ-CFG-027, REQ-AUTH-059): accounts without an enrolled method are directed to enroll after first-factor success |
| `TOTP_ISSUER` | api | string | `CLARA` | — | issuer label in authenticator apps / `otpauth://` URIs (REQ-CFG-029) |
| `TFA_EMAIL_CODE_TTL` | api | integer seconds > 0 | `600` | — | email one-time-code lifetime (GD-21, REQ-CFG-029, REQ-AUTH-057) |
| `SMTP_HOST` | api | host | — | — | SMTP relay for email codes; **all SMTP keys unset → the `email` method is unavailable** (REQ-CFG-028) |
| `SMTP_PORT` | api | integer | `587` | — | relay port |
| `SMTP_SECURITY` | api | `starttls`\|`tls`\|`none` | `starttls` | — | transport security to the relay |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | api | string | — | — | optional relay credentials; `SMTP_PASSWORD` is a secret — never logged, redacted in the startup dump (REQ-CFG-021) |
| `OTP_MAIL_FROM` | api | address | — | — | sender of code emails (required when `SMTP_HOST` is set) |

### 3.12 Appearance (web)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `UI_THEME` | web | one of `bootstrap`, `darkly`, `yeti` | `bootstrap` | — | installation-wide default Bootstrap theme (GD-26, REQ-CFG-031, REQ-TECH-027): the stylesheet rendered for every user without a personal override (`users.ui_theme`, REQ-DB-008); an unknown value refuses startup (§4.1); the web layer resolves the effective theme at render time and links exactly one same-origin stylesheet (REQ-UI-040) |

## 4. Validation Rules and Startup Behavior

### 4.1 Required-at-startup matrix

| Condition | `development` | `production` |
|---|---|---|
| `INTERNAL_SERVICE_TOKEN` | default acceptable (§3.3) | **absent → refuse to start** (REQ-CFG-013) |
| `API_ADDR` reachable from the host only (loopback), or `API_ALLOW_REMOTE=1` naming the exception | any bind allowed | **non-loopback bind without `API_ALLOW_REMOTE=1` → refuse to start** (REQ-CFG-033) |
| `ANON_SALT` | default acceptable (§3.6) | **absent → refuse to start** (REQ-CFG-015) |
| `WEB_PUBLIC_URL` | optional | **absent → refuse to start** (redirect URIs must be absolute) |
| `DB_HOST`/`DB_USERNAME`/`DB_PASSWORD` (when `DB_CONNECTION=mariadb`) | required | required |
| an authentication path exists: at least one of `OAUTH2_N_ISSUER`, `LDAP_SERVER_N_URL` — **or** table-based bootstrap (GD-18, REQ-CFG-025): if **neither** an OAuth2 provider **nor** an LDAP server is configured, then `ADMIN_BOOTSTRAP_EMAIL` and `ADMIN_BOOTSTRAP_PASSWORD` | **required** (the system must be loggable-in on a first installation) | **required** |
| `OAUTH2_N_CLIENT_ID`/`CLIENT_SECRET` for every provider with a set `ISSUER` | required | required |
| `LDAP_SERVER_N_SEARCH_BASE` for every server with a set `URL` | required | required (REQ-CFG-012) |
| every entry of `OAUTH2_N_NAMES` / `LDAP_SERVER_N_NAMES` / `LOCAL_LOGIN_NAMES` non-empty after trimming; duplicate entries within one list ignored | required | required (REQ-CFG-032) |
| `ANON_DATE_SHIFT_MIN ≤ ANON_DATE_SHIFT_MAX` | required | required |
| `SESSION_DIR` is a directory path and not equal to `DB_DATABASE` | required (REQ-CFG-017) | required |
| `SESSION_IDLE_TIMEOUT` is `0` or an integer ≥ 60; `SESSION_ROTATE_INTERVAL` is `0` or an integer ≥ 120 | required (REQ-CFG-017) | required |
| `SESSION_COOKIE_SECURE=1` | — (`0` allowed) | required — `0` refuses startup (REQ-AUTH-075) |
| every entry of `TRUSTED_PROXY_CIDRS` is a CIDR range or a bare address (an unset value keeps the default, §3.9) | required | required (REQ-API-125) |
| `AUTH_INACTIVITY_LIMIT_DAYS` is an integer ≥ 0 (default 180; 0 = rule off) | required | required (REQ-CFG-024) |
| `APP_TIMEZONE` is a resolvable IANA timezone name (default `UTC`) | required | required (REQ-CFG-026) |
| `AUTH_REQUIRE_2FA` ∈ {`0`,`1`} (default `0`); `TFA_EMAIL_CODE_TTL` integer > 0; `SMTP_SECURITY` ∈ {`starttls`,`tls`,`none`} | required | required (GD-21, REQ-CFG-027/028/029) |
| when `SMTP_HOST` is set: `OTP_MAIL_FROM` set and parseable (email method active; unset SMTP → method unavailable, no startup failure) | required | required (REQ-CFG-028) |
| `UI_THEME` ∈ {`bootstrap`, `darkly`, `yeti`} (default `bootstrap`) | required | required (GD-26, REQ-CFG-031) |

The API refuses to start with a non-zero exit and a message naming each missing/invalid variable (REQ-CFG-004); the PHP app fails at entry-point boot with an operator-readable page and no stack trace in production (REQ-CFG-005).

### 4.2 Engine-specific applicability (REQ-CFG-008)

When `DB_CONNECTION=sqlite`: `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD` are ignored (warn at startup). When `mariadb`: `DB_DATABASE` is a database name, not a path.

### 4.3 Invariants (REQ-CFG-006)

Configuration is snapshotted at startup; there is no runtime mutation path and no reload endpoint.

### 4.4 Startup dump and redaction (REQ-CFG-021/022)

At `info` level both components log the effective **non-secret** configuration (env, engine, listen address, log level, rate-limit state, provider/server *names*, session lifetime, account policy, collection timezone). Secret values — `INTERNAL_SERVICE_TOKEN`, `OAUTH2_N_CLIENT_SECRET`, `LDAP_SERVER_N_BIND_PASSWORD`, `DB_PASSWORD`, `ANON_SALT`, `ADMIN_BOOTSTRAP_PASSWORD` — are printed as `***` in any configuration dump (REQ-CFG-021) and never appear in logs otherwise (REQ-API-005).

## 5. `.env.example` (complete)

```dotenv
# core
APP_ENV=development
API_ADDR=127.0.0.1:8080
# production refuses a non-loopback bind unless this names the exception (REQ-CFG-033, §4.1);
# ignored when APP_ENV=development
#API_ALLOW_REMOTE=0
WEB_PUBLIC_URL=http://localhost:8000
LOG_LEVEL_API=debug
LOG_LEVEL_WEB=debug

# database (sqlite) — or DB_CONNECTION=mariadb with DB_HOST/DB_PORT/DB_USERNAME/DB_PASSWORD
DB_CONNECTION=sqlite
DB_DATABASE=./data/app.sqlite

# service boundary
INTERNAL_SERVICE_TOKEN=dev-internal-token
ADMIN_BOOTSTRAP_EMAIL=admin@example.org
# required at startup when NO OAuth2 provider and NO LDAP server are configured (GD-18)
ADMIN_BOOTSTRAP_PASSWORD=***

# OAuth2 provider 1 (optional — table-based authentication is always available, GD-18)
OAUTH2_1_ISSUER=https://idp.example.org
OAUTH2_1_CLIENT_ID=csms
OAUTH2_1_CLIENT_SECRET=***
OAUTH2_1_REDIRECT_URI=http://localhost:8000/auth/callback
OAUTH2_1_EMAIL_ATTR=email
# names shown on the login page; sources may share names, one source may carry several (REQ-CFG-032)
OAUTH2_1_NAMES=Hospital 1

# local (table-based) source names — optional; unset everywhere = one implicit default set
#LOCAL_LOGIN_NAMES=Clinic A

# LDAP server 1 (optional — tried in parallel with the other sources under its name, REQ-AUTH-065)
#LDAP_SERVER_1_NAMES=Hospital 2
#LDAP_SERVER_1_URL=ldap://dir.example.org:389
#LDAP_SERVER_1_SEARCH_BASE=ou=people,dc=example,dc=org
#LDAP_SERVER_1_UID_ATTR=uid
#LDAP_SERVER_1_EMAIL_ATTR=mail
#LDAP_SERVER_1_NAME_ATTR=cn

# anonymization
ANON_SALT=dev-a…n-salt
ANON_DATE_SHIFT_MIN=0
ANON_DATE_SHIFT_MAX=364

# data validation
#MAX_VALUE_BYTES=1048576

# sessions (web)
SESSION_DIR=/tmp/csms-sessions
SESSION_COOKIE_NAME=csms_session
SESSION_LIFETIME=28800
SESSION_IDLE_TIMEOUT=1800
SESSION_ROTATE_INTERVAL=900
SESSION_COOKIE_SECURE=0

# rate limiting (api, web) — thresholds live in system_settings, edited in the admin interface;
# both components take a forwarded X-Real-IP only from these addresses
TRUSTED_PROXY_CIDRS=127.0.0.0/8,::1

# account policy and time (api)
AUTH_INACTIVITY_LIMIT_DAYS=180
AUTH_PASSWORD_TOKEN_TTL_DAYS=7
APP_TIMEZONE=UTC

# appearance (web) — installation default theme: bootstrap | darkly | yeti (GD-26);
# users may override it personally; an unknown value refuses startup
UI_THEME=bootstrap

# two-factor authentication (api, GD-21) — email method unavailable without SMTP_HOST
AUTH_REQUIRE_2FA=0
TOTP_ISSUER=CLARA
TFA_EMAIL_CODE_TTL=600
SMTP_HOST=
SMTP_PORT=587
SMTP_SECURITY=starttls
SMTP_USERNAME=
SMTP_PASSWORD=
OTP_MAIL_FROM=
```

## 6. Resolved Deferred Items

| Deferred in | Resolution here |
|---|------|
| `anon_salt` configuration key (`Database_Schema_Design.md` §12) | `ANON_SALT` + range keys, §3.6 |
| finite value-length cap key (`Data_Validation_Design.md` §11) | `MAX_VALUE_BYTES`, §3.7 |
| date-shift "sane defaults" (REQ-CFG-016) | 0…364 days, §3.6 |
| rate-limit keys (REQ-CFG-020) | superseded 2026-09-27 (master spec "Rate limitter", DEV-CFG-3): enable flag and per-source-IP threshold moved to `system_settings` edited in the admin interface (`API_Endpoints_Design.md` §4.22); env keeps only `TRUSTED_PROXY_CIDRS`, §3.9 |
| session settings (REQ-CFG-017) | §3.8, 8 h default per REQ-AUTH-015; idle timeout (30 min) and rotation interval (15 min) added 2026-10-11 (DEV-AUTH-17) |
| bootstrap local password (GD-18, master spec "Details") | `ADMIN_BOOTSTRAP_PASSWORD`, §3.3; startup rule §4.1; redaction §4.4 |
| inactivity auto-disable rule key (GD-19, master spec "Details") | `AUTH_INACTIVITY_LIMIT_DAYS` (default 180, 0 = off), §3.10 |
| default collection timezone (GD-16, master spec "Details") | `APP_TIMEZONE` (IANA name, default `UTC`), §3.10 |
| two-factor mandate, code lifetime, and mail relay (GD-21, master spec "Details") | `AUTH_REQUIRE_2FA`, `TOTP_ISSUER`, `TFA_EMAIL_CODE_TTL`, SMTP keys, §3.11; posture keys stay environment-only (DEV-CFG-4) |
| authentication-source display names (master spec "Authentication order") | `OAUTH2_N_NAMES` (§3.4), `LDAP_SERVER_N_NAMES` (§3.5), `LOCAL_LOGIN_NAMES` (§3.3); validation §4.1; REQ-CFG-032, DEV-CFG-6 |

## 7. Open Items

None.
