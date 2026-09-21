# System Configuration — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/System_Configuration_Requirements.md`
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

### 3.4 OAuth2 (providers 1–3; at least one provider or one LDAP server required)

Per provider `N` (1…3):

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `OAUTH2_N_ISSUER` | web | absolute URL | — | if provider N used | provider root (authorize/token/userinfo endpoints derived, ASM-AUTH-1) |
| `OAUTH2_N_CLIENT_ID` | web | string | — | if provider N used | (REQ-CFG-011) |
| `OAUTH2_N_CLIENT_SECRET` | web | string (secret) | — | if provider N used | (REQ-CFG-011) |
| `OAUTH2_N_REDIRECT_URI` | web | absolute URL | `WEB_PUBLIC_URL + /auth/callback` | — | (REQ-CFG-011) |
| `OAUTH2_N_EMAIL_ATTR` | web | string | `email` | — | claim/attribute mapped to the system user (REQ-CFG-011, REQ-AUTH-004) |

### 3.5 LDAP (servers 1–3, evaluated in order; 2 and 3 optional)

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
| `SESSION_COOKIE_NAME` | web | string | `csms_session` | — | (REQ-CFG-017) |
| `SESSION_LIFETIME` | web | seconds | `28800` (8 h) | — | inactivity timeout (REQ-CFG-017, REQ-AUTH-015) |
| `SESSION_COOKIE_SECURE` | web | `0`\|`1` | dev: `0`, prod: `1` | — | (REQ-CFG-007, REQ-TECH-019) |

### 3.9 Rate limiting (api)

| Variable | Component | Type | Default | Required | Description |
|---|---|---|---|---|---|
| `RATE_LIMIT_ENABLED` | api | `0`\|`1` | `0` (disabled) | — | (REQ-CFG-020, REQ-API-038) |
| `RATE_LIMIT_RPM` | api | integer ≥ 1 | `600` | — | requests per minute per token (REQ-CFG-020) |

## 4. Validation Rules and Startup Behavior

### 4.1 Required-at-startup matrix

| Condition | `development` | `production` |
|---|---|---|
| `INTERNAL_SERVICE_TOKEN` | default acceptable (§3.3) | **absent → refuse to start** (REQ-CFG-013) |
| `ANON_SALT` | default acceptable (§3.6) | **absent → refuse to start** (REQ-CFG-015) |
| `WEB_PUBLIC_URL` | optional | **absent → refuse to start** (redirect URIs must be absolute) |
| `DB_HOST`/`DB_USERNAME`/`DB_PASSWORD` (when `DB_CONNECTION=mariadb`) | required | required |
| at least one of: `OAUTH2_1_ISSUER`, `LDAP_SERVER_1_URL` | **required** (some authentication path must exist) | **required** (REQ-CFG-011) |
| `OAUTH2_N_CLIENT_ID`/`CLIENT_SECRET` for every provider with a set `ISSUER` | required | required |
| `LDAP_SERVER_N_SEARCH_BASE` for every server with a set `URL` | required | required (REQ-CFG-012) |
| `ANON_DATE_SHIFT_MIN ≤ ANON_DATE_SHIFT_MAX` | required | required |
| `SESSION_DIR` is a directory path and not equal to `DB_DATABASE` | required (REQ-CFG-017) | required |

The API refuses to start with a non-zero exit and a message naming each missing/invalid variable (REQ-CFG-004); the PHP app fails at entry-point boot with an operator-readable page and no stack trace in production (REQ-CFG-005).

### 4.2 Engine-specific applicability (REQ-CFG-008)

When `DB_CONNECTION=sqlite`: `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD` are ignored (warn at startup). When `mariadb`: `DB_DATABASE` is a database name, not a path.

### 4.3 Invariants (REQ-CFG-006)

Configuration is snapshotted at startup; there is no runtime mutation path and no reload endpoint.

### 4.4 Startup dump and redaction (REQ-CFG-021/022)

At `info` level both components log the effective **non-secret** configuration (env, engine, listen address, log level, rate-limit state, provider/server *names*, session lifetime). Secret values — `INTERNAL_SERVICE_TOKEN`, `OAUTH2_N_CLIENT_SECRET`, `LDAP_SERVER_N_BIND_PASSWORD`, `DB_PASSWORD`, `ANON_SALT` — are printed as `***` in any configuration dump (REQ-CFG-021) and never appear in logs otherwise (REQ-API-005).

## 5. `.env.example` (complete)

```dotenv
# core
APP_ENV=development
API_ADDR=127.0.0.1:8080
WEB_PUBLIC_URL=http://localhost:8000
LOG_LEVEL_API=debug
LOG_LEVEL_WEB=debug

# database (sqlite) — or DB_CONNECTION=mariadb with DB_HOST/DB_PORT/DB_USERNAME/DB_PASSWORD
DB_CONNECTION=sqlite
DB_DATABASE=./data/app.sqlite

# service boundary
INTERNAL_SERVICE_TOKEN=dev-internal-token
ADMIN_BOOTSTRAP_EMAIL=admin@example.org

# OAuth2 provider 1 (or LDAP_SERVER_1_URL — at least one authentication path)
OAUTH2_1_ISSUER=https://idp.example.org
OAUTH2_1_CLIENT_ID=csms
OAUTH2_1_CLIENT_SECRET=***
OAUTH2_1_REDIRECT_URI=http://localhost:8000/auth/callback
OAUTH2_1_EMAIL_ATTR=email

# LDAP server 1 (optional fallback)
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
SESSION_COOKIE_SECURE=0

# rate limiting (api)
RATE_LIMIT_ENABLED=0
RATE_LIMIT_RPM=600
```

## 6. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| `anon_salt` configuration key (`Database_Schema_Design.md` §12) | `ANON_SALT` + range keys, §3.6 |
| finite value-length cap key (`Data_Validation_Design.md` §11) | `MAX_VALUE_BYTES`, §3.7 |
| date-shift "sane defaults" (REQ-CFG-016) | 0…364 days, §3.6 |
| rate-limit keys (REQ-CFG-020) | `RATE_LIMIT_ENABLED`/`RATE_LIMIT_RPM`, §3.9 |
| session settings (REQ-CFG-017) | §3.8, 8 h default per REQ-AUTH-015 |

## 7. Open Items

None.
