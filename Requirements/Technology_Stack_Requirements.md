# Technology Stack — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Technology_Stack.md`
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

Defines the mandatory technology constraints and the non-functional requirements that follow from the chosen stack. The design document `Design/Technology_Stack_Design.md` fixes concrete versions, libraries, and repository layout.

## 2. Mandatory Technology Constraints

| ID | Requirement |
|---|---|
| REQ-TECH-001 | **Frontend:** plain HTML and JavaScript only. No frontend framework, no build step. Bootstrap is the only UI library (responsive layout, forms, buttons, modals). |
| REQ-TECH-002 | **Web application layer:** PHP. Responsible for page rendering, sessions, and the OAuth2/LDAP login flow. |
| REQ-TECH-003 | **API layer:** Go (Golang). Responsible for all data access, validation, authorization, and audit writes. Exposes the REDCap-compatible protocol and the administration API. |
| REQ-TECH-004 | **API documentation:** OpenAPI (Swagger) specification maintained for both API surfaces, available at a stable URL. |
| REQ-TECH-005 | **Database:** SQLite for development, MariaDB for production, selectable purely by configuration. |
| REQ-TECH-006 | **Layering:** the PHP layer MUST NOT open a database connection or write data directly. All backend data access is delegated to the Go API (business requirement BR-006). |
| REQ-TECH-007 | **SQL portability:** application SQL targets the feature set common to current SQLite and MariaDB. Dialect-specific constructs are allowed only where documented (e.g. audit table partitioning on MariaDB; per-year tables on SQLite). |

## 3. Non-Functional Requirements

### 3.1 Portability and Environments

| ID | Requirement |
|---|---|
| REQ-TECH-008 | The system MUST start and pass its test suite with zero code changes when only the environment configuration changes from SQLite to MariaDB and back. |
| REQ-TECH-009 | Development MUST be possible on a workstation without MariaDB, LDAP, or an OAuth2 provider (local substitutes or mocks acceptable). |

### 3.2 Performance

| ID | Requirement |
|---|---|
| REQ-TECH-010 | Typical admin API responses (project metadata, field lists) < 300 ms p95 on reference hardware (see design document) with a project of 1,000 records × 200 fields × 10 events. |
| REQ-TECH-011 | A full project export (same size as REQ-TECH-010, CSV, flat) MUST complete in under 10 s and MUST stream to the client (no full materialization in memory beyond one record page). |
| REQ-TECH-012 | The data table layout MUST allow adding fields or projects without schema migration of the data table (EAV layout; BR from master spec). |

### 3.3 Reliability and Operations

| ID | Requirement |
|---|---|
| REQ-TECH-013 | The Go API MUST be deployable as a single static binary with no runtime language dependency beyond the OS. |
| REQ-TECH-014 | The API MUST fail fast at startup when required configuration is missing or the database is unreachable. |
| REQ-TECH-015 | The system MUST be operable behind a single reverse proxy / TLS termination point (see `Authentication_Authorization_Requirements.md` for the network trust model). |
| REQ-TECH-016 | All components MUST emit structured logs (timestamp, level, component, request/user context) to stdout or a configured sink. |

### 3.4 Security

| ID | Requirement |
|---|---|
| REQ-TECH-017 | No secrets (DB credentials, service secret, OAuth client secrets, anonymization salt) MAY appear in source code or version control. |
| REQ-TECH-018 | The administration API MUST be unreachable from the public network; only the REDCap-compatible `/api/` endpoint is exposed externally (see `Authentication_Authorization_Requirements.md`). |
| REQ-TECH-019 | Session cookies MUST be `HttpOnly`, `Secure` (production), `SameSite=Lax`. |
| REQ-TECH-020 | All user-supplied content rendered in the UI MUST be escaped server-side; a Content-Security-Policy header MUST be sent by the web application. |

### 3.5 Maintainability

| ID | Requirement |
|---|---|
| REQ-TECH-021 | The Go API MUST be covered by automated tests (unit + integration against SQLite) that run in CI without external services. |
| REQ-TECH-022 | The REDCap compatibility contract (Fiona call examples) MUST be encoded as executable regression tests. |
| REQ-TECH-023 | Dependency footprint: the Go module MUST avoid non-standard-library dependencies except database drivers, an OpenAPI/Swagger UI static bundle, and at most two other production libraries. PHP MUST use the standard distribution (no Composer framework dependencies). |

## 4. Assumptions

| ID | Assumption |
|---|---|
| ASM-TECH-1 | A standard LAMP-style host (PHP-FPM + web server) is available for the web application; no container orchestration is required in phase 1. |
| ASM-TECH-2 | Bootstrap and any small JS utility (none beyond vanilla ES2020) are vendored locally; no CDN dependency in production. |
| ASM-TECH-3 | The Go API and PHP app run on the same host or a trusted internal network segment. |

## 5. Open Items

None blocking. If the operator prefers containerized deployment later, the constraints above remain satisfied (single binary + static PHP app).
