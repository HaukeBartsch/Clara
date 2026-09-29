# System Configuration — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source plan:** `Plan/System_Configuration.md`\
**Status:** Approved for development handoff\
**Date:** 2026-09-18

## 1. Purpose

Defines how the system is configured across environments. The design document `Design/System_Configuration_Design.md` contains the complete variable inventory, defaults, and validation rules.

## 2. Requirements

### 2.1 Configuration Mechanism

| ID | Requirement |
|---|---|
| REQ-CFG-001 | Configuration MUST be supplied exclusively via environment variables, optionally loaded from a `.env` file. `.env` files MUST NOT be tracked in version control (`.gitignore` entry provided). |
| REQ-CFG-002 | Process environment variables MUST take precedence over `.env` values. |
| REQ-CFG-003 | Both components (Go API, PHP app) MUST read the same variable names from `System_Configuration_Design.md §3` so a single `.env` configures the whole system. |
| REQ-CFG-004 | The API MUST validate required configuration at startup and refuse to start (exit non-zero with a clear message naming the missing variable) when a required value is absent. |
| REQ-CFG-005 | The PHP app MUST validate its required configuration at boot of the entry point and fail with an operator-readable error page (no stack trace in production). |
| REQ-CFG-006 | Configuration MUST be immutable at runtime; changing configuration requires a process restart. |

### 2.2 Environments

| ID | Requirement |
|---|---|
| REQ-CFG-007 | `APP_ENV` MUST distinguish `development` from `production`. Development defaults: SQLite, permissive logging, debug error detail. Production defaults: MariaDB, no debug detail, `Secure` cookie flag. |
| REQ-CFG-008 | `DB_CONNECTION` MUST select the database engine (`sqlite` or `mariadb`); all other `DB_*` variables apply to the selected engine. |
| REQ-CFG-009 | A SQLite configuration MUST accept a file path (`DB_DATABASE`) that can point at a per-developer or per-test file. |
| REQ-CFG-010 | Configuration MUST support up to 3 LDAP servers (`LDAP_SERVER_1..3`), tried **in parallel within the authentication-source name selected by the user** (REQ-AUTH-065, DEV-AUTH-14 — the earlier sequential evaluation is superseded), with servers 2 and 3 optional. |

### 2.3 Authentication Configuration

| ID | Requirement |
|---|---|
| REQ-CFG-011 | OAuth2 provider settings MUST support the authorization-code flow: issuer/provider URL, client id, client secret, redirect URI, and the attribute(s) used to map the provider's user to the system user (email). Multiple providers MAY be configured, each presented to the user by display name (REQ-CFG-032). Providers and LDAP servers are **optional** — table-based (local) authentication is always available (GD-18); the startup rule for first installations is in `System_Configuration_Design.md` §4.1 (REQ-CFG-025). |
| REQ-CFG-012 | LDAP settings MUST include, per server: URL, bind DN and bind password (or simple anonymous search), search base, and the attribute names for uid/email/display name. |
| REQ-CFG-032 | **Authentication-source display names** (master spec "Authentication order"). Every authentication source MUST support one or more configurable display names presented on the login page — `OAUTH2_N_NAMES`, `LDAP_SERVER_N_NAMES`, `LOCAL_LOGIN_NAMES` (comma-separated lists; REQ-AUTH-063/064). Sources MAY share a name and one source MAY carry several; an entry empty after trimming MUST be rejected at startup. When no names are configured anywhere, all sources form one implicit default set and the login picker is skipped (REQ-AUTH-067). |
| REQ-CFG-013 | The service secret shared between PHP and the Go API (`INTERNAL_SERVICE_TOKEN`) MUST be configurable and MUST NOT have a built-in default that works in production (production MUST fail to start without it). |
| REQ-CFG-014 | The bootstrap administrator (`ADMIN_BOOTSTRAP_EMAIL`) MUST be configurable (decision GD-4). |
| REQ-CFG-025 | The bootstrap administrator's local password (`ADMIN_BOOTSTRAP_PASSWORD`) MUST be configurable (GD-18, REQ-AUTH-051): on a first installation without an OAuth2 provider and without an LDAP server it is **required** (startup fails without it — `System_Configuration_Design.md` §4.1), and it is hashed (bcrypt) into the bootstrap account's `password_hash`; it MUST NOT have a built-in default and MUST NOT be logged (REQ-CFG-021). |
| REQ-CFG-024 | The inactivity auto-disable limit (`AUTH_INACTIVITY_LIMIT_DAYS`) MUST be configurable as an integer number of days ≥ 0 (GD-19, REQ-AUTH-053): accounts whose last successful login is older than this are auto-disabled at their next authentication check; the default is **180**; `0` disables the rule. |

### 2.4 Anonymization Configuration

| ID | Requirement |
|---|---|
| REQ-CFG-015 | The anonymization salt MUST be configurable and MUST NOT have a default in production. |
| REQ-CFG-016 | The date-shift range for anonymized exports (min/max days) MUST be configurable with sane defaults (see design document). |

### 2.5 Session and Runtime

| ID | Requirement |
|---|---|
| REQ-CFG-017 | PHP session settings MUST be configurable: storage directory (MUST NOT be the application database), cookie name, session lifetime, secure flag. |
| REQ-CFG-018 | The public base URL of the application MUST be configurable (used for OAuth2 redirect URIs and API documentation links). |
| REQ-CFG-019 | Log level MUST be configurable per component (debug|info|warn|error). |
| REQ-CFG-020 | Optional rate limiting for the API MUST be configurable as **system-wide runtime settings stored in the database** — enabled/disabled (default disabled), requests-per-minute per source IP (default 600) and how long an over-budget source IP stays blocked (default 10 minutes, REQ-API-113) — editable through the administration interface and effective without a restart; it MUST NOT depend on environment variables (REQ-API-112, REQ-DB-037; master spec "Rate limitter"). Which proxy addresses may supply the caller IP stays deployment configuration (`TRUSTED_PROXY_CIDRS`, REQ-API-111). |
| REQ-CFG-026 | The deployment's default collection timezone (`APP_TIMEZONE`) MUST be configurable as an IANA timezone name (GD-16, REQ-VAL-041): it supplies the offset for date/date-time values imported without an explicit zone (no browser zone, no `tz` parameter); the default is `UTC`. |
| REQ-CFG-027 | The two-factor mandate (`AUTH_REQUIRE_2FA`) MUST be configurable as `0`|`1` with default **`0`** (GD-21, REQ-AUTH-059): when `1`, every account without an enrolled method is directed to enroll after first-factor success, before login completes. |
| REQ-CFG-028 | Delivery of the email two-factor codes MUST be configurable as an SMTP relay — host, port, transport security (`starttls` | `tls` | `none`), optional username/password, and the sender address (`SMTP_HOST`, `SMTP_PORT`, `SMTP_SECURITY`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `OTP_MAIL_FROM`) (GD-21, REQ-AUTH-057). The relay is **optional**: without it the `email` method is unavailable. `SMTP_PASSWORD` is a secret — environment-only, no built-in default, never logged (REQ-CFG-021); the code content never appears in any log. |
| REQ-CFG-029 | The TOTP issuer label shown in authenticator apps (`TOTP_ISSUER`, string, default `CLARA`) and the email-code lifetime (`TFA_EMAIL_CODE_TTL`, integer seconds > 0, default **600**) MUST be configurable (GD-21, REQ-AUTH-056/057). |
| REQ-CFG-030 | `AUTH_PASSWORD_TOKEN_TTL_DAYS` — validity in days of invite and password-reset tokens (REQ-AUTH-060/062); integer ≥ 1, default **7**. |
| REQ-CFG-031 | The installation-wide default UI theme (`UI_THEME`) MUST be configurable as one of the installed theme identifiers — `bootstrap` (the standard stylesheet), `darkly`, `yeti` — with default **`bootstrap`** (GD-26, REQ-TECH-027); an unknown value MUST fail startup validation (`System_Configuration_Design.md` §4.1). The setting is consumed by the web layer and applies to every user without a personal theme override (REQ-UI-040/041, REQ-DB-008). |

### 2.6 Security

| ID | Requirement |
|---|---|
| REQ-CFG-021 | Secrets MUST be loaded only from the environment; they MUST NOT be written to logs (redacted in any startup config dump). |
| REQ-CFG-022 | On startup, the API MUST log the effective non-secret configuration (env, db engine, endpoints) at `info` level for operational traceability. |
| REQ-CFG-023 | The API MUST reject `/api/v1/*` requests that do not present a valid service token (see `Authentication_Authorization_Requirements.md`), regardless of any other configuration. |

## 3. Assumptions

| ID | Assumption |
|---|---|
| ASM-CFG-1 | A single `.env` file per host/environment is the deployment norm; no configuration database or remote config service. |
| ASM-CFG-2 | Restarting the process is an accepted operational procedure for configuration changes. |

## 4. Deviations from Plan

The plan's example configuration is extended (not contradicted) with the variables required by GD-1, GD-2, GD-3, GD-4 and the authentication design.

| ID | Deviation | Rationale |
|---|---|---|
| DEV-CFG-1 | "At least one OAuth2 provider or one LDAP server" is no longer a startup requirement; instead: when **neither** is configured, `ADMIN_BOOTSTRAP_PASSWORD` is required | Owner decision (2026-09-22, GD-18; master spec "Details"): first installation without an IdP must be usable — the table-based bootstrap admin is the guaranteed login path (REQ-CFG-025, `System_Configuration_Design.md` §4.1). |
| DEV-CFG-2 | New configuration keys `AUTH_INACTIVITY_LIMIT_DAYS`, `ADMIN_BOOTSTRAP_PASSWORD`, `APP_TIMEZONE` | Owner decisions (2026-09-22, GD-16/GD-18/GD-19; master spec "Details"): inactivity rule (180 days), bootstrap local password, default collection timezone (REQ-CFG-024/025/026). |
| DEV-CFG-3 | `RATE_LIMIT_ENABLED`/`RATE_LIMIT_RPM` removed; the rate-limit enable flag and threshold move to the `system_settings` table edited in the administration interface; new key `TRUSTED_PROXY_CIDRS` | Owner decision (2026-09-27; master spec "Rate limitter"): thresholds customizable in the administration interface, so they must live in the database, not the environment. The trusted-proxy range is deployment topology and a security boundary — it stays an environment variable (`System_Configuration_Design.md` §3.9; REQ-CFG-020, REQ-API-111/112). |
| DEV-CFG-4 | New keys `AUTH_REQUIRE_2FA`, `TOTP_ISSUER`, `TFA_EMAIL_CODE_TTL`, and the SMTP relay set (`SMTP_HOST`/`_PORT`/`_SECURITY`/`_USERNAME`/`_PASSWORD`, `OTP_MAIL_FROM`) | Owner decision (2026-09-28, GD-21; master spec "Details"): by-user two-factor authentication with an email option needs a mail relay; the mandate and code lifetime are deployment policy and set at installation, not runtime-edited — unlike the rate-limit settings they change security posture and stay environment-only (REQ-CFG-027/028/029). |
| DEV-CFG-5 | New key `UI_THEME` | Owner decision (2026-09-28, GD-26; "allow bootstrap themes like darkly and yeti in assets/"): installation-wide default Bootstrap theme for the web layer; per-user override lives on the user row, not the environment (REQ-CFG-031, REQ-TECH-027). |
| DEV-CFG-6 | New keys `OAUTH2_N_NAMES`, `LDAP_SERVER_N_NAMES`, `LOCAL_LOGIN_NAMES`; LDAP servers no longer "evaluated in order" | Owner decision (2026-09-29; master spec "Authentication order"): users select their authentication source by name before login ("Hospital 1", "Hospital 2"); names relate many-to-many to sources and credential sources under a name are tried in parallel (`Authentication_Authorization_*` DEV-AUTH-14, REQ-CFG-032). |
