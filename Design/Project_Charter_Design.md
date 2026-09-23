# Project Charter — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Project_Charter_Requirements.md`
**Date:** 2026-09-23

## 1. Purpose

Fixes the top-level system design: the normative component decomposition, the boundaries between components, and how the area design documents compose into one deployable system. It maps each business requirement (BR-001…BR-012) and global decision (GD-1…GD-19) of the charter to the area design that realizes it, and states how the charter's success criteria (§7) are verifiable.

The area design documents remain normative within their own areas; this document is normative for the **cross-area system view** — the component set, the inter-component boundaries, and the composition. Adding a component, moving a responsibility across a boundary, or changing how the areas compose is a change to this document.

## 2. System Architecture

### 2.1 Component decomposition (normative)

The system is exactly these four components and no others. There is no scheduler, message broker, or separate storage service (charter scope; ASM-TECH-1).

| Component | Responsibility | Technology | Normative source |
|---|---|---|---|
| Go API (single process, static binary) | All data access, validation, authorization, and audit writes; both API surfaces (REDCap data API + administration API); serves the OpenAPI document | Go 1.26 | `Technology_Stack_Design.md` §3–§5 |
| PHP web application | Page rendering, session ownership, the OAuth2/LDAP/local login flow; the **sole client** of the administration API | PHP 8.4, no DB driver | `Technology_Stack_Design.md` §4–§5 |
| Database | Single store: structure, EAV record data, and the two audit tables; SQLite (development) / MariaDB (production) | — | `Database_Schema_Design.md` |
| Reverse proxy | TLS termination; publicly routes `POST/GET /api/` and the PHP routes, and only those; strips the internal headers | nginx + PHP-FPM | `Technology_Stack_Design.md` §5 |

### 2.2 Boundaries and interfaces (normative)

- **Browser ↔ PHP:** the browser talks only to the PHP web application, authenticated by the PHP-native session cookie. It never calls `/api/v1/*` and never holds a project token (GD-1, `Authentication_Authorization_Design.md` §3). The browser's vanilla ES2020 client MAY bind data regions by pulling JSON from PHP endpoints that proxy the API (REQ-TECH-025, REQ-UI-032); it never writes and never reaches the API directly.
- **PHP ↔ Go API:** server-side, presenting the shared service secret plus the authenticated user id in `X-Internal-Service-Token` / `X-Internal-User-Id` (GD-1, `API_Endpoints_Design.md` §4.1, `Authentication_Authorization_Design.md` §6). The proxy strips both headers from every externally-originated request.
- **External caller ↔ Go API:** the REDCap data API (`POST/GET /api/`), the token passed as a body parameter, not a header (GD-5, `API_Endpoints_Design.md` §3.1).
- **Single writer:** the Go API is the only component that opens a database connection. PHP MUST NOT open one (REQ-TECH-006, BR-006); no other code path writes to the store (success criterion 6).
- **Exposure:** the administration API and the documentation endpoints are NOT publicly routable; only the REDCap data API and the PHP pages are (REQ-TECH-018, `Authentication_Authorization_Design.md` §1.2).

### 2.3 Cross-area flows

| Flow | Path through the components | Normative sources |
|---|---|---|
| Login | browser → PHP (local first, then OAuth2/LDAP) → Go API (account check, bootstrap promotion) → session | `Authentication_Authorization_Design.md` §2 |
| Data entry | UI form → PHP → Go API import → validation → EAV upsert + same-transaction audit row | `API_Endpoints_Design.md` §3.7, `Data_Validation_Design.md` §2, `Audit_Logging_Design.md` §3 |
| External pull (Fiona) | `POST /api/` → token/level check → export pipeline (level + anonymization) → streamed CSV/JSON + audit + record-view row | `Data_Export_Anonymization_Design.md` §2–§6, `API_Endpoints_Design.md` §3.6 |
| Project end | operator → `POST /api/v1/projects/{id}/end-provision` (`is_admin`) → delete / in-place anonymize + `project_ended` audit | `Data_Export_Anonymization_Design.md` §7 |

## 3. Realization of Business Requirements

| BR | Requirement (charter §3) | Realized by |
|---|---|---|
| BR-001 | Secure token-based access (OAuth2, LDAP fallback, table-based local) | `Authentication_Authorization_Design.md` §2 (sequences A/B/F); `System_Configuration_Design.md` §3.3–§3.5 |
| BR-002 | Granular per-arm and project-scoped permissions | `Authentication_Authorization_Design.md` §4 (single evaluation function); `API_Endpoints_Design.md` §5 (endpoint → permission) |
| BR-003 | Flexible project structure (arms, events, instruments, mapping) | `Database_Schema_Design.md` §5; `API_Endpoints_Design.md` §4.8–§4.12 |
| BR-004 | Data integrity via server-side field validation before storage | `Data_Validation_Design.md` §2 (pipeline runs in the Go API on every import) |
| BR-005 | REDCap-compatible API (Go, OpenAPI) so Fiona keeps working unchanged | `API_Endpoints_Design.md` §3; `Technology_Stack_Design.md` §3 (hand-maintained OpenAPI 3.1), §6 (Fiona fixtures) |
| BR-006 | Web UI that accesses the backend exclusively through the API | `User_Interface_Design.md` (all); the single-writer boundary (§2.2, REQ-TECH-006) |
| BR-007 | Two audit tables (change/create/delete; record views) | `Audit_Logging_Design.md` §3/§4; `Database_Schema_Design.md` §7 (append-only, yearly rollover) |
| BR-008 | CSV/JSON export with full / anonymized / non-sensitive levels | `Data_Export_Anonymization_Design.md` §2–§5 |
| BR-009 | Project end per the end provision (delete or anonymize at the REK end date) | `Data_Export_Anonymization_Design.md` §7 (one-shot `is_admin` action) |
| BR-010 | Project visible only to an administrator or a member | `Authentication_Authorization_Design.md` §4.3 (per-surface decision paths); `API_Endpoints_Design.md` (REQ-API-007) |
| BR-011 | Record isolation between data access groups | `Authentication_Authorization_Design.md` §4.2; `Database_Schema_Design.md` §6/§8 (`record_entities`, DAG tables) |
| BR-012 | Account validity period + inactivity auto-disable | `Authentication_Authorization_Design.md` §4.4 (account active rule); `System_Configuration_Design.md` §3.10 |

## 4. Realization of Global Decisions

| GD | Decision (charter §5) | Realized by |
|---|---|---|
| GD-1 | Administration API authentication: PHP owns the session and presents the service secret + user id; the browser never calls `/api/v1/*` | `API_Endpoints_Design.md` §4.1; `Authentication_Authorization_Design.md` §3/§6 |
| GD-2 | Per-arm permission model: ordered data-access levels, ordered export levels, `project_admin` | `Authentication_Authorization_Design.md` §4.1; `Data_Export_Anonymization_Design.md` §4 |
| GD-3 | Record deletion in scope: API `action=delete`, confirmed UI action, audit-logged | `API_Endpoints_Design.md` §3.8; `Audit_Logging_Design.md` §3 (deletion events) |
| GD-4 | `is_admin` flag; bootstrap admin from configuration; promoted on first login | `Authentication_Authorization_Design.md` §2.3; `API_Endpoints_Design.md` §4.4; `System_Configuration_Design.md` §3.3 |
| GD-5 | API tokens: UUIDs mapped to (user, project), passed as a body parameter | `Authentication_Authorization_Design.md` §5; `API_Endpoints_Design.md` §3.1 |
| GD-6 | SQL portability: common feature set; dialect-specific constructs as documented exceptions | `Database_Schema_Design.md` §10 |
| GD-7 | All system timestamps in UTC | `Audit_Logging_Design.md` (REQ-AUD-005); `Database_Schema_Design.md` §1 |
| GD-8 | Record identifier = field 1 of the instrument at position 1; its value is the `record_id` | `Database_Schema_Design.md` §5 (enforced by the API); `Data_Export_Anonymization_Design.md` §4.1 |
| GD-9 | Surveys: filled via stable, record-specific public links without login | `API_Endpoints_Design.md` §3.10/§4.17; `Database_Schema_Design.md` §8 (`survey_links`) |
| GD-10 | Data access groups: visibility scoping, active-group switching, record reassignment | `Authentication_Authorization_Design.md` §4.2; `Database_Schema_Design.md` §8; `API_Endpoints_Design.md` §4.18 |
| GD-11 | Calculated fields: expressions over other fields, automatic recomputation in the same transaction | `Data_Validation_Design.md` §6; `Database_Schema_Design.md` §5/§6 (`calculated_dependencies`, EAV rows) |
| GD-12 | Multilingual UI: English default/fallback, Bokmål/Nynorsk first; UI strings only | `User_Interface_Design.md` §9; `API_Endpoints_Design.md` §4.19; `Database_Schema_Design.md` §8 (`languages`/`i18n_strings`) |
| GD-13 | Branching logic: expression-based, designer-editable, display-only — never affects import, export, or audit | `Data_Validation_Design.md` §7; `Technology_Stack_Design.md` §4 (client-side evaluator, `web/assets/app.js`) |
| GD-14 | Data-entry submission policy: the UI submits exactly the fields with a value plus the fields the user cleared | `User_Interface_Design.md` §8 (data entry) |
| GD-15 | Event ordering: timepoint events by `period`, then no-timepoint events in user order (canonical per-arm order) | `Database_Schema_Design.md` §5; `API_Endpoints_Design.md` §4.9 (events-order endpoint) |
| GD-16 | Date/date-time values carry the collection timezone; stored as collected, never converted | `Data_Validation_Design.md` §4.1 (canonical storage); `Data_Export_Anonymization_Design.md` §5.2 (shift preserves the offset) |
| GD-17 | Simplified `projects` table; removed attributes are ordinary instrument data, no special handling | `Database_Schema_Design.md` §4 (REQ-DB-032); `API_Endpoints_Design.md` §4.5 (creation body) |
| GD-18 | Table-based authentication: local password path tried first; bootstrap password from configuration | `Authentication_Authorization_Design.md` §2.6 (sequence F); `System_Configuration_Design.md` §3.3 |
| GD-19 | Account validity period + inactivity auto-disable, re-enableable by an administrator | `Authentication_Authorization_Design.md` §4.4 (account active rule); `System_Configuration_Design.md` §3.10 |

## 5. Scope Boundaries (charter §4)

System-level consequences of the phase-1 scope:

- **No scheduler or queue component** — time-bound behavior is evaluated at defined checkpoints: account inactivity at the next authentication check (GD-19), and project end by an explicit operator action at the REK end date (BR-009, `Data_Export_Anonymization_Design.md` §7.1). Nothing runs automatically.
- **No file/document storage** — the option flags are not project metadata (GD-17); DICOM handling is a separate system (charter out-of-scope).
- **No randomization, DDP, or external modules; no survey invitations or scheduling** — only the survey-link filling is in scope (GD-9).
- **No per-instrument access levels** — per-arm export levels plus the `personal_information`/`export_approved` field flags are the control (charter out-of-scope; `Data_Export_Anonymization_Requirements.md` DEV-EXP-1).

## 6. Success Criteria → Verification (charter §7)

| # | Criterion | Verified by |
|---|---|---|
| 1 | Every Fiona call example in `Endpoints.md` succeeds without modification of the caller | table-driven fixtures in CI (REQ-TECH-022, `Technology_Stack_Design.md` §6) |
| 2 | A data-entry user can create, read, and update record values through the UI; all operations appear in the correct audit table | Go integration tests + PHP smoke tests (REQ-TECH-021, `Technology_Stack_Design.md` §6) |
| 3 | An administrator can run the full project lifecycle — structure, members, roles, tokens — entirely through the web UI | integration coverage of the administration-API flows (REQ-TECH-021, `Technology_Stack_Design.md` §6) |
| 4 | `export_de_identified` returns no direct identifiers and hashed personal fields; `export_no_identifiers` returns the same data with all identifier fields removed | pipeline tests of `Data_Export_Anonymization_Design.md` §4–§5 |
| 5 | The same application binary and code base run against SQLite (dev) and MariaDB (production) with only configuration changes | the same test suite against both engines (REQ-TECH-008, `Technology_Stack_Design.md` §5) |
| 6 | No application code path writes to the database outside the Go API; PHP never opens a database connection | the single-writer boundary (§2.2) + a PHP dependency set without a database driver (REQ-TECH-006/023) |

## 7. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| \"the web application … accesses the backend exclusively through the API\" (BR-006, master spec) | normative component set + single-writer boundary: exactly four components, PHP has no DB driver (§2.1/§2.2) |
| how the area designs compose into one deployable system (charter §1/§8) | component decomposition, boundaries, and cross-area flows (§2) |
| the success criteria as acceptance (charter §7) | mapped to verifiable artifacts (§6) |
| time-bound behavior without a scheduler (GD-19; BR-009) | checkpoint-based — account inactivity evaluated at the next authentication check, project end by an explicit `is_admin` action at the REK end date (§5; `Authentication_Authorization_Design.md` §4.4, `Data_Export_Anonymization_Design.md` §7.1); no scheduler or queue component (§2.1) |

## 8. Open Items

Single-area open items (endpoint encoding, audit `details` shapes, configuration keys, validator grammar, UI advisories) are tracked in their respective area design documents. The items open at this system level:

| Item | Owner |
|---|---|
| the per-project end-provision decision (delete vs. anonymize at the REK end date) — BR-009; spans export, storage, UI, and API | operations / project owner (rules fixed in `Data_Export_Anonymization_Design.md` §7) |
| exact nginx configuration file for the deployment target (reverse-proxy component, §2.1) | operations (rules fixed in `Technology_Stack_Design.md` §5) |
| additive scope candidates not in phase 1: 3-state record completion (binary today, REQ-API-074) and a most-recent-first record-history read (`API_Endpoints_Design.md` §4.16) | owner + requirements (tracked in `User_Interface_Design.md` §11) |