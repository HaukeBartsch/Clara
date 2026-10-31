# Project Charter — Requirements Analysis

**Project:** CLARA — Clinical Logbook for Automated Research Assistance (REDCap API Replacement)\
**Source:** `VISION_AND_REQUIREMENTS.md` (master spec); seeded from the area plan `Plan/Project_Charter.md`, retired 2026-10-28 and kept in history (`git show 2b0c371:Plan/Project_Charter.md`)\
**Status:** Approved for development handoff\
**Created:** 2026-09-18

## 1. Purpose

This document consolidates the business and product requirements for a research electronic data capture (EDC) system for clinical studies. It is the top-level requirements baseline; the area requirements documents in this folder refine it. The system replaces REDCap for this deployment: it provides a web-based platform to collect project data, and integrates with the research information system (RIS, "Fiona") through a REDCap-compatible API.

> **Key constraints** — a summary for quick orientation; the cited IDs and sections are normative.
>
> - **The global decisions (§5) bind every area document.**
> - **PHP never opens a database connection.** All data access goes through the Go API (BR-006, success criterion 6).
> - **FIONA's calls must work unchanged** (BR-005, success criterion 1).
> - **One binary for SQLite and MariaDB**, switched by configuration only (success criterion 5).

## 2. Stakeholders and Actors

| Actor | Description | Key needs |
|---|---|---|
| Administrator | Authenticated user with `is_admin` flag; manages accounts, projects, roles, assignments | Full visibility of all projects; user/project administration |
| Data Manager | Project member with role `data-manager` | Manage project structure (arms, events, instruments, fields, mapping); enter and change records |
| Data Entry | Project member with role `data-entry` | Enter and change records only |
| Controller | Project member with role `controller` | View project structure and data; no record changes |
| Custom-role member | Project member with a project-defined role | Exactly the permissions of that role |
| Role-less member | Project member without a project role | Full permissions for that project only |
| External system (Fiona / RIS) | Automated caller of the REDCap-compatible API | Unchanged call pattern (token in request body), stable responses |
| System operator | Deploys and operates the system | Environment separation (dev/production), configuration via environment variables |

## 3. Business Requirements

| ID | Requirement |
|---|------|
| BR-001 | The system must be a secure, token-based access system mapped to user accounts (OAuth2, LDAP fallback, and table-based local-password authentication — GD-18). |
| BR-002 | The system must enforce granular, per-arm and project-scoped permissions per user role. |
| BR-003 | The system must support flexible project structures: study arms, events, instruments, and instrument-by-event mappings. |
| BR-004 | The system must guarantee data integrity through server-side field validation before any value is stored. |
| BR-005 | The system must expose a REDCap-compatible API (Go, OpenAPI/Swagger) so existing callers (Fiona) keep working unchanged. |
| BR-006 | The system must provide a web UI (admin interface + data entry) that accesses the backend exclusively through the API. |
| BR-007 | The system must log all backend data access in two audit tables: one for change/create/delete events, one exclusively for record views. |
| BR-008 | The system must support exporting project data as CSV and JSON at the four GD-2 export levels — `export_none`, `export_de_identified` (anonymized), `export_no_identifiers`, `export_full` — held per (instrument, event) pair under the arm's default (DEV-AUTH-5). |
| BR-009 | The system must handle project end per the project's end provision (delete or anonymize the stored data at the REK end date). The end provision is not a `projects`-table attribute (GD-17): it is held as project data (e.g. in a `DataTransferProjects` instrument) or outside the system; the REK end date remains project metadata. |
| BR-010 | A project must only be visible to a user who is an administrator or a member of the project. |
| BR-011 | Records MUST be isolated between data access groups: a member with an active group accesses only the records of that group; a member without a group sees all records (GD-10). |
| BR-012 | User accounts MUST support a validity period (days; `0` = indefinite) and MUST be auto-disabled after a configurable period of inactivity (default 180 days), re-enableable by an administrator (GD-19). |
| BR-013 | Every project MUST be in exactly one of three modes — development, production, analysis: staged setup changes with a breaking-change warning in production, disabled data entry in analysis with the same warning guarding immediate setup changes there, and transitions only an installation admin user may make, requiring any open staging set to be closed first (GD-20). |
| BR-014 | User accounts MUST support a **by-user configurable two-factor authentication** covering a phone-based key option (TOTP authenticator app) and an email option (one-time code by email); the second factor guards the CLARA-verified login paths, enrollment is self-service, and an administrator can mandate 2FA installation-wide and reset it per account (GD-21). |

## 4. Scope — Phase 1

**In scope:**
- User account management (enable/disable, admin flag, account validity period, inactivity auto-disable with admin re-enable — GD-19).
- Table-based (local password) authentication: a third login path beside OAuth2 and LDAP, available from first installation (GD-18).
- Two-factor authentication on the CLARA-verified login paths (local + LDAP form logins): per-user opt-in with TOTP (authenticator app) or email one-time code, self-service enrollment, admin mandate and per-account reset (GD-21).
- Project creation and metadata management.
- Project structure: single-arm first (multi-arm supported in the model), events, instruments, fields (data dictionary), instrument-by-event mapping.
- Instrument designer and instrument/field reordering.
- Data entry (create/update of record values), record deletion, and record status dashboard.
- REDCap-compatible data API (`POST /api/`; `GET` is refused, REQ-API-009) with content: `project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, `record` (export/import/delete).
- Administration API (`/api/v1/...`) used exclusively by the PHP web application.
- RBAC with per-arm permission levels (data access and export) plus `project_admin`; roles are defined per project (example presets, custom roles, or none).
- Survey instruments filled via stable, record-specific public links without login (GD-9).
- Project modes (development / production / analysis): staged setup changes in production with a breaking-change warning before commit, disabled data entry in analysis, admin-only transitions (GD-20).
- Data access groups per project: record visibility scoping, active-group switching, and later record reassignment (GD-10).
- Calculated fields: expressions over other fields with automatic recomputation (GD-11).
- Multilingual UI — English default; Bokmål and Nynorsk first targets (GD-12).
- Branching logic on fields and instruments (expression-based, designer-editable, display-only) (GD-13).
- CSV/JSON export with three sensitivity levels; end-provision execution.
- Audit logging (events + record views), immutable, yearly rollover.
- SQLite (development) and MariaDB (production) backends.

**Out of scope (phase 1):**
- Survey invitations, scheduling, and survey administration (the survey link filling of GD-9 is in scope); randomization, DDP, external modules (REDCap features never used by the planned callers).
- Access control finer than the (instrument, event) grant of GD-2 — per-field or per-user lists, time-boxed access, approval workflows (DEV-EXP-1; master spec "Role adjustment").
- File/document upload storage (radiology/pathology options are not recorded as project metadata — GD-17: the owner MAY hold them as data, e.g. in a `DataTransferProjects` instrument; DICOM handling is a separate system).

## 5. Global Decisions and Conventions

These decisions were made explicit with the project owner and are binding for all area documents:

| ID | Decision |
|---|------|
| GD-1 | **Administration API authentication:** the PHP web application owns the session (PHP-native session cookie). The browser never calls `/api/v1/*` directly. PHP invokes the Go API server-side, presenting a shared service secret plus the authenticated user's ID in headers (`X-Internal-Service-Token`, `X-Internal-User-Id`). The API trusts PHP as the sole admin-API client. |
| GD-2 | **Permission model:** permissions are assigned per arm as the role's default and, below it, per (instrument, event) pair. **Data access level** (ordered, higher includes lower): `no_access` (hidden), `read_only`, `view_edit` (enter and change values). Two **rights** ride on the same scope and are implied by no level: **delete instrument values** (clear that instrument's values for a record) and **edit collected surveys**. **Export level** (ordered, held per pair): `export_none`, `export_de_identified` (direct identifiers removed, personal fields hashed, dates shifted), `export_no_identifiers` (all identifier fields removed), `export_full` (full dataset). **Project level:** `project_admin` (structure and metadata). |
| GD-3 | **Record deletion is in scope:** the REDCap-compatible API supports `content=record&action=delete`, the data entry UI offers a confirmed delete action, and deletions (including deleted values) are audit-logged. |
| GD-4 | **Admin status:** a boolean `is_admin` on the users table. The bootstrap admin is defined by `ADMIN_BOOTSTRAP_EMAIL` in configuration; the API promotes a matching login on first occurrence. Admins are managed through the users API. On a first install without an identity provider, the bootstrap admin's local password is set from `ADMIN_BOOTSTRAP_PASSWORD` (GD-18). |
| GD-5 | **API tokens** are UUIDs mapped to (user, project) assignments, passed as the `token` form parameter in the REDCap API request body. |
| GD-6 | **Database:** SQL statements target the feature set common to current MariaDB and SQLite; dialect-specific constructs (audit table partitioning) are documented exceptions. |
| GD-7 | **Time:** all **system** timestamps (audit entries, account and project metadata, API-level response timestamps) are stored and compared in UTC. Clinical date/date-time values are not system timestamps: they carry their collection timezone (GD-16). |
| GD-8 | **Record identifier field:** the first field, by position, in the earliest instrument by position that has fields is the record identifier; its value is the record name (`record_id`). An empty earlier instrument does not displace that field; a project with no fields has no identifier yet. |
| GD-9 | **Surveys:** an instrument MAY be marked as a survey. Survey instruments can be filled out by a person without a login via a stable, record-specific public web link carrying an opaque link token; the link grants fill-only access to that (record, instrument). Only instruments marked as surveys can be filled this way. |
| GD-10 | **Data access groups:** a project MAY have none, one, or several data access groups. A member MAY be assigned to none, one, or several groups; with one or more assigned, exactly one is active. A record belongs to exactly one group or none (record property, taken from the creating member's active group). A member with an active group sees only the records of that group; a member without a group sees all records of the project. The active group is switchable by the member; a record's group can be assigned or changed later. |
| GD-11 | **Calculated fields:** a field MAY be of type `calculated`, holding an expression over other fields referenced as `[event][field]`, with the operators `+`, `-`, `*`, `/` and numeric constants; its value is stored at each (record, event, instrument, instance) position where the field is active, and automatically updated in the same transaction whenever a referenced value — or the expression itself — changes. |
| GD-12 | **Multilingual UI:** the UI MUST be translatable; English is the default and the fallback language. Norwegian Bokmål and Nynorsk are the first target languages; the mapping tables admit further languages. The language setting applies to UI strings only — never to stored data, field labels, or choice values. |
| GD-13 | **Branching logic:** a field or an instrument MAY carry a branching logic expression over other fields of the same record (references `[event][field]`, value comparisons with `=`, `!=`, `<`, `>`, `<=`, `>=`, the usual functions `text_contains`, `is_blank`, `is_not_blank`, logical AND/OR, parentheses); a radio/checkbox reference evaluates to `1` when checked/selected, else `0`; the field or instrument is displayed only while the expression evaluates to true (1), in surveys and in normal data entry; it is display-only and MUST NOT affect import, export, or audit. |
| GD-14 | **Data-entry submission policy:** fields may have no value; an instrument that is partially filled and saved stores only the entered values. An empty value supplied to the API is a value — it intentionally clears the previously stored value — so the **web application MUST NOT submit empty values for fields whose previous value the user has not removed**: it submits exactly the fields that carry a value in the form plus the fields the user explicitly cleared. The API import semantics (empty = "no value"/clear, REQ-VAL-024) are unchanged; this is a UI-side decision. |
| GD-15 | **Event ordering:** within an arm, events **with** a timepoint value (`period`, days after baseline) are sorted by that timepoint (ascending; ties by user position); events **without** a timepoint value (`period` = `NULL`) are user-orderable through the events table (a new events-order endpoint). `events.period` is therefore nullable: `NULL` = no timepoint. The canonical per-arm order applies to the UI event table, the record-status dashboard, `content=event`, and `content=formEventMapping`. |
| GD-16 | **Timezones in the internal date/date-time format:** the canonical storage form of date and date-time values carries the timezone of collection: dates `YYYY-MM-DD±HH:MM`, date-times `YYYY-MM-DD HH:MM±HH:MM` (offset `±HH:MM`, UTC = `+00:00`). The offset for an imported value comes, in order, from (1) the zone supplied with the import — the browser timezone for UI data entry, an optional `tz` parameter for data-API imports — and (2) the deployment default `APP_TIMEZONE` (configuration; default `UTC`). Values are stored as collected (wall time + offset), never converted; GD-7 continues to govern system timestamps only. Anonymized-export date shifting applies to the date part and preserves the offset; chronological comparisons use absolute instants. |
| GD-17 | **Simplified `projects` table:** the `projects` table stores project name, organization (the main supporting institution), PI name/email, data-manager name/email, REK number, REK start/end dates, start/end dates, participant naming pattern, and creation time. The option flags (radiology, pathology, pathology type, redcap-only, data collection from home), the end-user-contract flag, the end provision, and the initial `event_names` list are **removed** from the table. If the owner wants them, they are stored as data in an ordinary instrument of the project (master spec: `DataTransferProjects`) — the system gives that instrument no special handling. Project creation creates the first arm with a `baseline` event (REQ-API-050); further events are added through the events endpoint. |
| GD-18 | **Table-based authentication:** the `users` table has a nullable `password_hash` column (argon2id — DEV-AUTH-16) and `auth_source` admits `local`. An email+password login path (PHP form → API verification of the stored hash) exists beside the OAuth2 and LDAP paths; under a selected source name it is tried in parallel with the LDAP servers of that name (master spec "Authentication order", `Authentication_Authorization_*` DEV-AUTH-14). On a first installation without an OAuth2 provider or LDAP server, the bootstrap admin (`ADMIN_BOOTSTRAP_EMAIL`) receives its local password from `ADMIN_BOOTSTRAP_PASSWORD` (required at startup in that case), so the system is usable and its authentication/authorization can be set up through the existing administration UI. Plaintext passwords are never stored or logged (REQ-AUTH-036). |
| GD-19 | **Account validity and inactivity:** the `users` table has `valid_until` (date; `NULL` = indefinite; set as days, `0` = indefinite) and `last_login_at` (UTC; updated on every successful login). An account is active only while `enabled`, not expired (`valid_until`), and not inactive: if the last successful login is older than `AUTH_INACTIVITY_LIMIT_DAYS` (configuration; default 180; `0` = rule off), the account is auto-disabled at the check, the inactivity clock is reset, the attempt is rejected, and the event is audit-logged (`account_auto_disabled`). An administrator re-enables such accounts (user overview shows last login, validity end, and status). |
| GD-20 | **Project modes:** every project is in exactly one of three modes — `development` (default for new projects), `production`, `analysis`. Only an installation admin user (`is_admin`) may change the mode — a project's own `project_admin` may not. **Development:** setup and data entry work as usual; all functionality available. **Production:** setup changes are staged and activated as a group — *start staging* opens a staging set (e.g., add or modify instruments); while staged, ongoing data collection continues to use the currently active design; *commit staged changes* activates the whole set at once. Before committing, warn about breaking changes — changes that would make existing project data inconsistent/inaccessible (e.g., deleting a field); non-breaking changes (adding a field, changing a field description, adding options to an existing dropdown) need no warning. **Analysis:** data entry is disabled; viewing and exporting remain available per each user's permissions; admin users can still interact with the project. **Transitions** (`is_admin`): development → production asks whether previously stored data should be kept or deleted; production → development, production → analysis, analysis → production and analysis → development keep all data — a project enters analysis only from production but returns to development directly; development → analysis is not offered. **No mode change while a staging set is open**: commit or discard the set first. **Analysis-mode setup edits** apply immediately without staging, but a breaking change warns and applies only on the admin's confirmation, using the production classification (REQ-API-105/111). |
| GD-21 | **Two-factor authentication:** accounts MAY carry a by-user configurable second factor with two methods — `totp` (RFC 6238 time-based one-time codes from a phone authenticator app; works offline, fitting the no-internet constraint) and `email` (a random one-time code sent to the account's email address via a configured SMTP relay). Method `off` is the default. The second factor applies **after a successful first factor on the CLARA-verified login paths** — the local (table-based) and LDAP form logins; on the OAuth2 path MFA remains the identity provider's responsibility (DEV-AUTH-10). Enrollment is self-service (TOTP QR/secret confirmed by one valid code, email confirmed by a delivered code; one-time recovery codes issued at activation); an administrator can **mandate** 2FA installation-wide (`AUTH_REQUIRE_2FA`, configuration) — accounts without a method are directed to enroll after their first-factor success — and MUST be able to reset a user's 2FA (lost device), which clears the method and the recovery codes. Secrets, codes, and recovery codes are never logged; the OAuth2/LDAP-external flow is otherwise unchanged (`Authentication_Authorization_Requirements.md` §2.3). |
| GD-22 | **Emailed invite for local accounts:** when an administrator creates a local account without a password, the system can send an **invitation** — an email carrying a cryptographically random single-use token link to the set-password page (public PHP route; stored only as a one-way hash in `password_tokens`; expires after `AUTH_PASSWORD_TOKEN_TTL_DAYS`, default 7; invalidated by a re-invite). Completing it stores the argon2id hash and the user logs in normally — the second factor is **not** bypassed. Invitations apply to local accounts only; without a configured SMTP relay the action is unavailable and the administrator sets the password directly (`Authentication_Authorization_Requirements.md` §2.4, REQ-AUTH-060; Sequence H). |
| GD-23 | **Self-service password lifecycle:** a signed-in local account changes its own password by proving the current one; the account's email address is not self-editable — it is the identity across all authentication paths and changes only by an administrator. "Forgot password" takes an email address, answers identically whether or not an account exists (no enumeration oracle — corrects the FIONA reference's `newPassword.php`), and emails a single-use token link that takes the **new password directly** — no temporary password is ever generated, transmitted, or stored. Requests are rate-limited per address; all events audit-log without token values (`Authentication_Authorization_Requirements.md` §2.4, REQ-AUTH-061/062). |
| GD-24 | **Access-application workflow stays out of scope:** the FIONA reference's `requestLogin.php` intake pipeline is not adopted — a request-intake workflow is a workflow product, not EDC scope. The adopted form remains REQ-AUTH-028's information page for users without project membership; its instructions are an admin-editable translation string (`User_Interface_Design.md` §5.7; DEV-AUTH-12). |
| GD-25 | **Vetted libraries for critical functions:** for the critical functions — authentication protocol clients (OAuth2, LDAP), two-factor and token cryptography adjacent code, session handling, and i18n — established, actively maintained libraries MAY be used in Go, PHP, and JavaScript, a bounded relaxation of the "no non-standard dependencies" rule (DEV-TECH-1: a vetted standard library is lower risk than hand-rolled protocol code). Admission is only by explicit amendment to the production allowlist in `Design/Technology_Stack_Design.md` — each candidate recorded with pinned version + integrity hash, passing a documented security review at adoption and a re-review on every version bump, with owner approval for each amendment. All admitted code is vendored locally (PHP as a committed `vendor/` tree plus `composer.lock`; JavaScript as vendored ES modules under `web/assets/vendor/`) — no build step, no CDN (REQ-TECH-023/026). |
| GD-26 | **Bootstrap theme files:** the UI MAY be rendered with a Bootstrap-compatible theme stylesheet — a full replacement for `bootstrap.min.css` (Bootswatch class, e.g. `darkly`, `yeti`). Each theme MUST be vendored locally under `web/assets/vendor/bootstrap/themes/<name>/` and served same-origin only; remote references inside a theme file (e.g. a Google Fonts `@import`) are stripped when vendoring, so a theme introduces no CDN dependency (REQ-TECH-001/027). The installation-wide default is set by `UI_THEME` (REQ-CFG-031); users MAY override it personally (stored on the user row), and public pages render with the installation default (`User_Interface_Design.md` §3.8; DEV-TECH-2). |

## 6. Dependencies

| Dependency | Impact |
|---|------|
| External OAuth2 identity provider(s) | Login flow (optional — table-based authentication is always available, GD-18); provider metadata (issuer, client id/secret) provisioned when used |
| Up to 3 LDAP servers (fallback) | Login fallback (optional, GD-18); directory attributes for name/email mapping |
| SMTP relay (internal mail server) | Delivery of the email two-factor codes (GD-21, optional — the `email` method is unavailable without it; TOTP needs no network service) |
| Fiona / RIS (external caller) | Defines the compatibility contract for `/api/`; call examples in `VISION_AND_REQUIREMENTS.md` become acceptance tests |
| Existing REDCap deployments | Behavioral reference for response shapes (`project`, `metadata`, `event`, `formEventMapping`) |
| Network topology | PHP and Go API on a trusted internal path; public exposure of `/api/` (REDCap protocol) via TLS termination |

## 7. Success Criteria

1. Every Fiona call example in `VISION_AND_REQUIREMENTS.md` (cURL and PHP) succeeds against the new system without modification of the caller.
2. A data entry user can create, read, and update record values through the UI; all operations appear in the correct audit table.
3. An administrator can create a project, build its structure (instruments, fields, events, mapping), assign users and roles, and issue project tokens — entirely through the web UI.
4. An export at the `export_de_identified` level returns no direct identifiers and hashed personal fields; at the `export_no_identifiers` level the same data is returned with all identifier fields removed.
5. The same application binary and code base run against SQLite (dev) and MariaDB (production) with only configuration changes.
6. No application code path writes to the database outside the Go API; the PHP layer never opens a database connection.

## 8. Traceability

| Area | Requirements document |
|---|------|
| AI assistant | `AI_Assistant_Requirements.md` |
| API surface | `API_Endpoints_Requirements.md` |
| Security | `Authentication_Authorization_Requirements.md` |
| Audit | `Audit_Logging_Requirements.md` |
| Export/anonymization | `Data_Export_Anonymization_Requirements.md` |
| Validation | `Data_Validation_Requirements.md` |
| Data model | `Database_Schema_Requirements.md` |
| Project interchange (CDISC ODM) | `ODM_Interchange_Requirements.md` |
| Configuration | `System_Configuration_Requirements.md` |
| Platform | `Technology_Stack_Requirements.md` |
| UI | `User_Interface_Requirements.md` |

Each area document carries requirement IDs prefixed with its area (`REQ-API-…`, `REQ-AUTH-…`, …) and references these business requirements where applicable.

## 9. Glossary

The terms and reference codes used across `Requirements/` and `Design/`. Each entry names where the term is defined normatively; the definition there wins over the summary here.

### 9.1 Reference codes

| Code | Meaning | Defined in |
|---|---|---|
| `REQ-<AREA>-nnn` | A requirement. IDs are stable: never renumbered, never reused. `<AREA>` names the document: `AI`, `API` (`API_Endpoints_Requirements.md`), `AUD` (Audit_Logging), `AUTH` (Authentication_Authorization), `CFG` (System_Configuration), `DB` (Database_Schema), `EXP` (Data_Export_Anonymization), `ODM` (ODM_Interchange), `TECH` (Technology_Stack), `UI` (User_Interface), `VAL` (Data_Validation). The same IDs appear in code comments and tests. | the area's Requirements document |
| `ASM-<AREA>-n` | An assumption the area's requirements rely on. If it stops holding, every requirement citing it needs review. | "Assumptions" section of the area's Requirements document |
| `DEV-<AREA>-n` | A decision record: a choice the area made where the master spec, another requirement, or a common default points elsewhere, with the reason. | "Decision Records" section of the area's Requirements document |
| `BR-nnn` | Business requirement. | §3 |
| `GD-n` | Global decision, binding on every area. | §5 |
| `D-n` | Local design decision of the export and anonymization pipeline. | `Design/Data_Export_Anonymization_Design.md` §10.1 |
| `F1`…`F20` | Finding of the security review, with severity; "Req-change" marks findings that changed a requirement. | `VISION_AND_REQUIREMENTS.md`, "Security relevant findings" |
| Sequence A…J | The authentication sequences: A OAuth2 authorization code, B LDAP attempt, C API login call, D logout, E brute-force protection, F local (table-based) attempt, G two-factor challenge and enrollment, H invite and password reset, I named source selection and credential race, J provider assertion verification. | `Design/Authentication_Authorization_Design.md` §2.1…§2.10 |
| `§n` | Section `n` of the document named just before it; with no document named, of the document it appears in. "charter §n" is this document. | — |
| master spec | `VISION_AND_REQUIREMENTS.md`, the origin document. A quoted heading after it (master spec "Authentication order") names its section. | — |
| plan | One of the per-area delivery plans retired on 2026-10-28. Citations quote them as of commit `2b0c371` (`git show 2b0c371:Plan/<Area>.md`). | `AGENTS.md` |

### 9.2 Domain terms

| Term | Meaning | Defined in |
|---|---|---|
| acting user | The account an administration-API call acts for, named by `X-Internal-User-Id`. Authoritative for every authorization decision; PHP adds no permissions of its own. | GD-1, REQ-AUTH-013 |
| administration API | The `/api/v1/*` surface. Called only by the PHP web application, server-side, over the trusted internal path; never by the browser or an external system. | GD-1, REQ-API-040 |
| arm | A study arm of a project, numbered from 1 (`arm_num`). Holds events; permissions default per arm. | BR-003, REQ-DB-011 |
| audit tables | `audit_events` (changes, exports, authentication, administration) and `audit_record_views` (record pulls through the data API). Both append-only, rolled over yearly. | BR-007, REQ-AUD-001 |
| authentication source | One configured login backend of one kind: the local (table-based) source, an LDAP server, or an OAuth2/OpenID Connect provider. | REQ-AUTH-063 |
| bootstrap administrator | The account named by `ADMIN_BOOTSTRAP_EMAIL`. Promoted to `is_admin` at login; on an installation without OAuth2 or LDAP it signs in with `ADMIN_BOOTSTRAP_PASSWORD`. | GD-4, GD-18, REQ-AUTH-007 |
| branching logic | An expression on a field or instrument that decides whether it is displayed. Display-only: it never affects import, export, or audit. | GD-13 |
| calculated field | Field type `calculated`. Its value is computed from other fields and recomputed in the same transaction as any change to an input or to the expression. | GD-11 |
| collection timezone | The UTC offset stored with every clinical date and date-time value (`2026-03-01+01:00`). Values are stored as collected, never converted. System timestamps are UTC instead. | GD-16, GD-7 |
| completion state | Per (record, event, instrument, instance): `no_data` and `some_data` are derived from the stored values; `finished` is assigned by a user. Survey instruments report `finished` automatically. | REQ-DB-036, REQ-API-074 |
| Control Panel | The administration page (`GET /admin`, `is_admin` only), one left-panel section per task: users, projects, audits, translations, settings, API documentation, configuration check, monitoring, authentication test. | `Design/User_Interface_Design.md` §5 |
| credential race | The parallel attempt of every local and LDAP source registered under the source name the user selected. The first successful answer wins; the rest are discarded. | REQ-AUTH-065, Sequence I |
| data access group (DAG) | A per-project grouping of records. A member with an active group sees only that group's records; a member without a group sees all records. | GD-10, BR-011 |
| data access level | `no_access` < `read_only` < `view_edit`. Held per arm as the role's default and per (instrument, event) pair below it. Two further rights — delete instrument values, edit collected surveys — are implied by no level. | GD-2 |
| data API | The REDCap-compatible `POST /api/` surface for external callers such as FIONA, authenticated by a project token in the request body. | REQ-API-009, REQ-API-010 |
| data dictionary | The field definitions of a project: name, type, label, choices, validation, and the identifier and personal-information flags. Imported and exported in the REDCap CSV layout. | REQ-DB-013 |
| direct identifier field | The record identifier field, or a field flagged `direct_identifier`. Its value never leaves the system untransformed below `export_full`. | REQ-EXP-020, REQ-EXP-021 |
| end-of-project provision | The one-shot action an administrator runs at the REK end date: delete or anonymize the project's stored data. No scheduler triggers it. | BR-009, D-7 |
| event | A time point within an arm. Unique name `<label>_arm_<n>` (the data API's `redcap_event_name`); optional timepoint `period` in days after baseline. Events with a timepoint sort by it; the others are ordered by hand. | REQ-DB-011, GD-15 |
| export level | `export_none` < `export_de_identified` (identifiers removed, personal fields hashed, dates shifted) < `export_no_identifiers` (identifier fields removed) < `export_full`. Held per (instrument, event) pair. | GD-2, BR-008 |
| FIONA | (1) The research information system (RIS) that calls the data API; its recorded calls are the compatibility contract. (2) Its historic user-management application in `assets/table_based_authentication_plus_user_management/`, the reference for the web UI's layout and session flow. Spelled "Fiona" in older text. | §1, REQ-UI-032 |
| first-factor handle | A signed statement, valid for 5 minutes, that the API verified an account's password. The second login call of a two-factor challenge presents it in place of the password. | REQ-API-131 |
| instance | One fill-out of a repeating instrument, numbered from 1. Every non-repeating form has instance 1. | REQ-DB-047, REQ-DB-048 |
| instrument | A form: an ordered set of fields. Collects data only while mapped to at least one event. | REQ-DB-011, REQ-DB-012 |
| instrument-event mapping | The set of unique (instrument, event) pairs. The pair is also the unit of permission grants, export levels, and the repeating designation. | REQ-DB-012, GD-2 |
| `is_admin` | The installation administrator flag on a user. Implies every permission on every project; only it may change a project's mode. | GD-4, GD-20, REQ-AUTH-023 |
| ODM | CDISC Operational Data Model XML (versions 1.3.2 and 2.0), used to export and import a project's structure and data. | `ODM_Interchange_Requirements.md` |
| personal field | A field flagged `personal_information` that is not a direct identifier. Hashed at `export_de_identified`, kept at the levels above. | REQ-EXP-022 |
| preferred region | The organization code (REQ-DB-006 enumeration) whose projects a user sees first on the project overview: the user's own choice, else a region set automatically from provider roles at sign-in or, failing that, from the administrator who assigned them to a project. Orders presentation only, never access. | REQ-DB-052, REQ-AUTH-088/089, REQ-UI-076 |
| project mode | `development` (everything allowed), `production` (setup changes go through a staging set; breaking changes warned before commit), or `analysis` (data entry disabled). Only `is_admin` changes it. | GD-20, BR-013 |
| project token | The UUID of one (user, project) assignment. A data-API caller sends it as the `token` parameter in the request body. | GD-5, REQ-API-010 |
| `project_admin` | A role flag: manage the structure and metadata of that one project. | GD-2 |
| provider role mapping | The administrator-maintained list that pairs a role value released by an OpenID Connect provider with a region, or records that it names none. Sign-ins add the values they carry; no account is linked to a value. | REQ-DB-053, REQ-API-177/178 |
| provider subject link | The stored (issuer, subject) pair binding an OpenID Connect identity to an account. Account resolution prefers it over the email address. | REQ-AUTH-080, REQ-DB-051 |
| record | One participant's data within a project, named by the value of the record identifier field. | GD-8 |
| record identifier field | The first field, by position, of the earliest instrument that has fields. Its value is the record name (`record_id`). | GD-8 |
| record naming mode | `auto` (the system assigns plain numeric ids) or `manual` (the user types the id). | REQ-DB-045 |
| REK | Norway's Regional Committees for Medical and Health Research Ethics. The REK number and approval dates are project metadata; the end-of-project provision is due at the REK end date. | GD-17, BR-009 |
| repeating instrument | A mapped (instrument, event) pair whose form can be filled more than once per record at that event. Never a survey instrument or the instrument holding the record identifier. | REQ-DB-047 |
| role | A project-scoped permission set: per-arm defaults, per-pair grants, and the `project_admin` flag. A member assigned no role has full permissions on that project. | GD-2, REQ-DB-009, REQ-DB-010 |
| service token | `INTERNAL_SERVICE_TOKEN`, the shared secret PHP presents as `X-Internal-Service-Token` on every administration-API call. | GD-1, REQ-AUTH-011 |
| session epoch | A per-account counter. PHP stores it in the session and presents it on every call; raising it ends every session of the account at its next call. | REQ-AUTH-074 |
| source name | The label ("Hospital 1") a user selects on the login page. One name can cover several authentication sources, and one source can carry several names. | REQ-AUTH-063, REQ-AUTH-064 |
| staging set | In production mode, the single open snapshot of pending setup changes of a project. Committed or discarded as a whole. | GD-20, REQ-DB-035 |
| survey link | A stable, record-specific public URL (`/s/{link}`) with an opaque token. It lets a person without a login fill exactly one survey-marked instrument of one record. | GD-9, REQ-AUTH-038 |
| transfer field | Field type `transfer`. Holds instructions (in its `choices` attribute) for copying values into another project; executed on request; stores no value of its own. | REQ-DB-050, REQ-API-165 |
| two-factor authentication | A TOTP or emailed one-time code after a successful local or LDAP first factor. OAuth2 logins rely on the identity provider's own factor. | GD-21, BR-014 |
