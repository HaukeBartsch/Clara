# Technology Stack — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source plan:** `Plan/Technology_Stack.md`\
**Status:** Approved for development handoff\
**Date:** 2026-09-18

## 1. Purpose

Defines the mandatory technology constraints and the non-functional requirements that follow from the chosen stack. The design document `Design/Technology_Stack_Design.md` fixes concrete versions, libraries, and repository layout.

## 2. Mandatory Technology Constraints

| ID | Requirement |
|---|------|
| REQ-TECH-001 | **Frontend:** plain HTML and JavaScript only. No frontend framework, no build step. Bootstrap is the only UI library (responsive layout, forms, buttons, modals). |
| REQ-TECH-002 | **Web application layer:** PHP. Responsible for page rendering, sessions, and the OAuth2/LDAP login flow. |
| REQ-TECH-003 | **API layer:** Go (Golang). Responsible for all data access, validation, authorization, and audit writes. Exposes the REDCap-compatible protocol and the administration API. |
| REQ-TECH-004 | **API documentation:** OpenAPI (Swagger) specification maintained for both API surfaces, available at a stable URL. |
| REQ-TECH-005 | **Database:** SQLite for development, MariaDB for production, selectable purely by configuration. |
| REQ-TECH-006 | **Layering:** the PHP layer MUST NOT open a database connection or write data directly. All backend data access is delegated to the Go API (business requirement BR-006). |
| REQ-TECH-007 | **SQL portability:** application SQL targets the feature set common to current SQLite and MariaDB. Dialect-specific constructs are allowed only where documented (e.g. audit table partitioning on MariaDB; per-year tables on SQLite). |
| REQ-TECH-025 | **PHP ↔ client interfacing style (reference application).** The PHP web application follows the interfacing style of the historic FIONA reference in `assets/table_based_authentication_plus_user_management/` (master spec, "Details"): PHP renders each page's shell and structure server-side and exposes **JSON endpoints** that proxy the Go API, presenting the service secret and the acting user id (GD-1, REQ-TECH-006) — those endpoints are the UI's own page routes, content-negotiated on `Accept`, not a separate route family (REQ-UI-044); the browser's **vanilla ES2020** client **fetches that JSON and populates rendering targets** (list rows, table bodies, `<select>` options) on the client (REQ-UI-032). The client does **no** page switching and uses **no** framework or jQuery (REQ-TECH-001). Reads that populate data regions are `GET`s to PHP routes; **all** state-changing requests remain CSRF-protected `POST`s to PHP routes (REQ-AUTH-037, REQ-TECH-020). The browser never reaches `/api/v1/*` or the data API directly (REQ-TECH-018, REQ-UI-002), and the JSON endpoints delegate to the API and never open a database connection (REQ-TECH-006). |

## 3. Non-Functional Requirements

### 3.1 Portability and Environments

| ID | Requirement |
|---|------|
| REQ-TECH-008 | The system MUST start and pass its test suite with zero code changes when only the environment configuration changes from SQLite to MariaDB and back. |
| REQ-TECH-009 | Development MUST be possible on a workstation without MariaDB, LDAP, or an OAuth2 provider (local substitutes or mocks acceptable). |

### 3.2 Performance

| ID | Requirement |
|---|------|
| REQ-TECH-010 | Typical admin API responses (project metadata, field lists) < 300 ms p95 on reference hardware (see design document) with a project of 1,000 records × 200 fields × 10 events. |
| REQ-TECH-011 | A full project export (same size as REQ-TECH-010, CSV, flat) MUST complete in under 10 s and MUST stream to the client (no full materialization in memory beyond one record page). |
| REQ-TECH-012 | The data table layout MUST allow adding fields or projects without schema migration of the data table (EAV layout; BR from master spec). |

### 3.3 Reliability and Operations

| ID | Requirement |
|---|------|
| REQ-TECH-013 | The Go API MUST be deployable as a single static binary with no runtime language dependency beyond the OS. |
| REQ-TECH-014 | The API MUST fail fast at startup when required configuration is missing or the database is unreachable. |
| REQ-TECH-015 | The system MUST be operable behind a single reverse proxy / TLS termination point (see `Authentication_Authorization_Requirements.md` for the network trust model). |
| REQ-TECH-016 | All components MUST emit structured logs (timestamp, level, component, request/user context) to stdout or a configured sink. |

### 3.4 Security

| ID | Requirement |
|---|------|
| REQ-TECH-017 | No secrets (DB credentials, service secret, OAuth client secrets, anonymization salt) MAY appear in source code or version control. |
| REQ-TECH-018 | The administration API MUST be unreachable from the public network; only the REDCap-compatible `/api/` endpoint is exposed externally (see `Authentication_Authorization_Requirements.md`). |
| REQ-TECH-019 | Session cookies MUST be `HttpOnly`, `Secure` (production), `SameSite=Lax`. |
| REQ-TECH-020 | All user-supplied content rendered in the UI MUST be escaped server-side; a Content-Security-Policy header MUST be sent by the web application. |
| REQ-TECH-024 | All queries built from user-supplied data MUST use parameterization or allowlists: bound SQL values, escaped LDAP filters, validated redirect URIs; user-supplied values MUST NOT be concatenated into SQL, LDAP filters, or similar constructs. Names used as SQL identifiers MUST first be validated against the data dictionary and identifier grammar (REQ-VAL-011). |

### 3.5 Maintainability

| ID | Requirement |
|---|------|
| REQ-TECH-021 | The Go API MUST be covered by automated tests (unit + integration against SQLite) that run in CI without external services. |
| REQ-TECH-022 | The REDCap compatibility contract (Fiona call examples) MUST be encoded as executable regression tests. |
| REQ-TECH-023 | Dependency footprint: the Go module MUST avoid non-standard-library dependencies except database drivers, an OpenAPI/Swagger UI static bundle, and at most two other production libraries. PHP MUST use the standard distribution — **except** for critical functions under REQ-TECH-026, where allowlisted Composer packages MAY be used. |
| REQ-TECH-026 | **Vetted libraries for critical functions (GD-25).** For the *critical functions* — authentication protocol clients (OAuth2, LDAP), two-factor and token cryptography adjacent code, session handling, and i18n — a small set of established, actively maintained libraries MAY be used in Go, PHP, and JavaScript. Admission is by explicit allowlist amendment: each candidate MUST be recorded in the production allowlist of `Design/Technology_Stack_Design.md` with version pin + integrity hash, pass a documented security review at adoption, and be re-reviewed on every version bump; the owner approves each amendment. All admitted code MUST be vendored locally — PHP via a committed `vendor/` tree plus `composer.lock`; JavaScript as vendored ES modules loaded without a build step (REQ-TECH-001, ASM-TECH-2 unchanged: no runtime CDN). Outside the critical functions the stdlib-first rule of REQ-TECH-023 stands; UI strings remain translated server-side (REQ-UI-008), so an admitted JS i18n library covers client-side formatting only (e.g. `Intl` fallbacks), not string transport. |
| REQ-TECH-027 | **Bootstrap theme files (GD-26).** The UI MAY be rendered with a Bootstrap-compatible theme stylesheet — a full replacement for `bootstrap.min.css` (Bootswatch class, e.g. `darkly`, `yeti`) instead of the standard stylesheet. Each theme MUST be vendored locally under `web/assets/vendor/bootstrap/themes/<name>/` and served same-origin only; no CDN dependency is introduced by a theme (REQ-TECH-001, ASM-TECH-2). Theme files are derived from the copies in `assets/` (`darkly_theme_bootstrap.min.css`, `yeti_theme_bootstrap.min.css`); any remote reference inside a theme file (e.g. a Google Fonts `@import`) MUST be stripped when the theme is vendored — a theme MUST render completely without internet access, and fonts remain served locally. A theme MUST NOT require markup changes: every page renders identically with the standard stylesheet or any installed theme. The set of selectable themes is the set of installed theme files (REQ-UI-040/041); the installation default and the per-user override are configuration/user-setting concerns (`REQ-CFG-031`, REQ-DB-008). |
| REQ-TECH-028 | **Browser testing of the web layer (Playwright).** Essential interactive components of `web/` MUST be covered by automated browser tests written with Playwright (Microsoft), run in CI: at minimum the login and session flow, a record entry/edit form including client-side validation and branching feedback, a Tabulator-backed data table, and the client half of the page/data-region split (REQ-UI-032, REQ-UI-044, REQ-TECH-025). Playwright is development and CI tooling only — it MUST NOT be referenced by any shipped page or template, so it adds no production dependency under REQ-TECH-023/026 and no runtime CDN reference (ASM-TECH-2), and writing or running its tests MUST NOT introduce a build step for frontend code (REQ-TECH-001). Tests drive the real Go API against SQLite with local substitutes for LDAP and OAuth2 (REQ-TECH-009); they complement the PHP harness of `Design/Technology_Stack_Design.md` §6, and the normative coverage stays in the Go API (REQ-TECH-021). |

## 4. Assumptions


| ID | Assumption |
|---|------|
| ASM-TECH-1 | A standard LAMP-style host (PHP-FPM + web server) is available for the web application; no container orchestration is required in phase 1. |
| ASM-TECH-2 | Bootstrap and any small JS utility (none beyond vanilla ES2020, apart from critical-function libraries admitted under REQ-TECH-026) are vendored locally; no CDN dependency in production. |
| ASM-TECH-3 | The Go API and PHP app run on the same host or a trusted internal network segment. |

## 5. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-TECH-1 | REQ-TECH-023's blanket "no Composer dependencies" relaxed to an allowlisted exception for critical functions (GD-25) | Owner decision (2026-09-28): authentication and i18n are security- and correctness-critical; a vetted standard library is lower risk than hand-rolled protocol code. The exception is bounded by the allowlist, pinning, local vendoring, and re-review on bump — not open-ended. `AGENTS.md` "Stack facts" updated accordingly (REQ-TECH-026). |
| DEV-TECH-2 | Bootstrap theme files (Bootswatch `darkly` and `yeti`) admitted as selectable alternate stylesheets | Owner decision (2026-09-28, GD-26; "allow bootstrap themes like darkly and yeti in assets/"): themes are full-replacement vendored CSS with remote references stripped (REQ-TECH-027); installation-wide default via `UI_THEME` (REQ-CFG-031) plus a per-user override (REQ-UI-040/041, REQ-API-122). |

## 6. Open Items

None blocking. If the operator prefers containerized deployment later, the constraints above remain satisfied (single binary + static PHP app).
