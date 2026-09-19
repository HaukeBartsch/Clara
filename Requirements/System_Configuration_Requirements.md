# System Configuration — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/System_Configuration.md`
**Status:** Approved for development handoff
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
| REQ-CFG-010 | Configuration MUST support up to 3 LDAP servers (`LDAP_SERVER_1..3`), evaluated in order, with servers 2 and 3 optional. |

### 2.3 Authentication Configuration

| ID | Requirement |
|---|---|
| REQ-CFG-011 | OAuth2 provider settings MUST support the authorization-code flow: issuer/provider URL, client id, client secret, redirect URI, and the attribute(s) used to map the provider's user to the system user (email). Multiple providers MAY be configured; at least one provider or at least one LDAP server is required. |
| REQ-CFG-012 | LDAP settings MUST include, per server: URL, bind DN and bind password (or simple anonymous search), search base, and the attribute names for uid/email/display name. |
| REQ-CFG-013 | The service secret shared between PHP and the Go API (`INTERNAL_SERVICE_TOKEN`) MUST be configurable and MUST NOT have a built-in default that works in production (production MUST fail to start without it). |
| REQ-CFG-014 | The bootstrap administrator (`ADMIN_BOOTSTRAP_EMAIL`) MUST be configurable (decision GD-4). |

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
| REQ-CFG-020 | Optional rate limiting for the REDCap API MUST be configurable (enabled/disabled, requests-per-minute per token) with a default of disabled (see `API_Endpoints_Requirements.md`). |

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

None. The plan's example configuration is extended (not contradicted) with the variables required by GD-1, GD-2, GD-3, GD-4 and the authentication design.
