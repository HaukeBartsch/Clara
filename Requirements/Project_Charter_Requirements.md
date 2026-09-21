# Project Charter — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Project_Charter.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

This document consolidates the business and product requirements for a research electronic data capture (EDC) system for clinical studies. It is the top-level requirements baseline; the area requirements documents in this folder refine it. The system replaces REDCap for this deployment: it provides a web-based platform to collect project data, and integrates with the research information system (RIS, "Fiona") through a REDCap-compatible API.

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
|---|---|
| BR-001 | The system must be a secure, token-based access system mapped to user accounts (OAuth2, LDAP fallback). |
| BR-002 | The system must enforce granular, per-arm and project-scoped permissions per user role. |
| BR-003 | The system must support flexible project structures: study arms, events, instruments, and instrument-by-event mappings. |
| BR-004 | The system must guarantee data integrity through server-side field validation before any value is stored. |
| BR-005 | The system must expose a REDCap-compatible API (Go, OpenAPI/Swagger) so existing callers (Fiona) keep working unchanged. |
| BR-006 | The system must provide a web UI (admin interface + data entry) that accesses the backend exclusively through the API. |
| BR-007 | The system must log all backend data access in two audit tables: one for change/create/delete events, one exclusively for record views. |
| BR-008 | The system must support exporting project data as CSV and JSON, with full, anonymized, and non-sensitive export levels. |
| BR-009 | The system must handle project end per the project's end provision (delete or anonymize the stored data at the REK end date). |
| BR-010 | A project must only be visible to a user who is an administrator or a member of the project. |
| BR-011 | Records MUST be isolated between data access groups: a member with an active group accesses only the records of that group; a member without a group sees all records (GD-10). |

## 4. Scope — Phase 1

**In scope:**
- User account management (enable/disable, admin flag).
- Project creation and metadata management.
- Project structure: single-arm first (multi-arm supported in the model), events, instruments, fields (data dictionary), instrument-by-event mapping.
- Instrument designer and instrument/field reordering.
- Data entry (create/update of record values), record deletion, and record status dashboard.
- REDCap-compatible data API (`POST/GET /api/`) with content: `project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, `record` (export/import/delete).
- Administration API (`/api/v1/...`) used exclusively by the PHP web application.
- RBAC with per-arm permission levels (data access and export) plus `project_admin`; roles are defined per project (example presets, custom roles, or none).
- Survey instruments filled via stable, record-specific public links without login (GD-9).
- Data access groups per project: record visibility scoping, active-group switching, and later record reassignment (GD-10).
- Calculated fields: expressions over other fields with automatic recomputation (GD-11).
- Multilingual UI — English default; Bokmål and Nynorsk first targets (GD-12).
- Branching logic on fields and instruments (expression-based, designer-editable, display-only) (GD-13).
- CSV/JSON export with three sensitivity levels; end-provision execution.
- Audit logging (events + record views), immutable, yearly rollover.
- SQLite (development) and MariaDB (production) backends.

**Out of scope (phase 1):**
- Survey invitations, scheduling, and survey administration (the survey link filling of GD-9 is in scope); randomization, DDP, external modules (REDCap features never used by the planned callers).
- Per-instrument access levels (see `Data_Export_Anonymization_Requirements.md`, deviation DEV-1).
- Multi-language UI.
- File/document upload storage (radiology/pathology options are recorded as project metadata only; DICOM handling is a separate system).

## 5. Global Decisions and Conventions

These decisions were made explicit with the project owner and are binding for all area documents:

| ID | Decision |
|---|---|
| GD-1 | **Administration API authentication:** the PHP web application owns the session (PHP-native session cookie). The browser never calls `/api/v1/*` directly. PHP invokes the Go API server-side, presenting a shared service secret plus the authenticated user's ID in headers (`X-Internal-Service-Token`, `X-Internal-User-Id`). The API trusts PHP as the sole admin-API client. |
| GD-2 | **Permission model (revised 2026-09-19):** permissions are assigned per arm. **Data access level** (ordered, higher includes lower): `no_access` (arm hidden), `read_only`, `view_edit` (enter and change values), `delete`, `edit_survey_responses` (additionally modify responses collected via survey links). **Export level** (ordered): `export_none`, `export_de_identified` (direct identifiers removed, personal fields hashed, dates shifted), `export_no_identifiers` (all identifier fields removed), `export_full` (full dataset). **Project level:** `project_admin` (structure and metadata). The former seven permissions (view/change/add/export_all/export_anonymized/export_non_sensitive) are superseded by these levels. |
| GD-3 | **Record deletion is in scope:** the REDCap-compatible API supports `content=record&action=delete`, the data entry UI offers a confirmed delete action, and deletions (including deleted values) are audit-logged. |
| GD-4 | **Admin status:** a boolean `is_admin` on the users table. The bootstrap admin is defined by `ADMIN_BOOTSTRAP_EMAIL` in configuration; the API promotes a matching login on first occurrence. Admins are managed through the users API. |
| GD-5 | **API tokens** are UUIDs mapped to (user, project) assignments, passed as the `token` form parameter in the REDCap API request body. |
| GD-6 | **Database:** SQL statements target the feature set common to current MariaDB and SQLite; dialect-specific constructs (audit table partitioning) are documented exceptions. |
| GD-7 | **Time:** all timestamps are stored and compared in UTC. |
| GD-8 | **Record identifier field:** the field at position 1 of the instrument at position 1 of the project is the record identifier; its value is the record name (`record_id`). |
| GD-9 | **Surveys (2026-09-19):** an instrument MAY be marked as a survey. Survey instruments can be filled out by a person without a login via a stable, record-specific public web link carrying an opaque link token; the link grants fill-only access to that (record, instrument). Only instruments marked as surveys can be filled this way. |
| GD-10 | **Data access groups (2026-09-19):** a project MAY have none, one, or several data access groups. A member MAY be assigned to none, one, or several groups; with one or more assigned, exactly one is active. A record belongs to exactly one group or none (record property, taken from the creating member's active group). A member with an active group sees only the records of that group; a member without a group sees all records of the project. The active group is switchable by the member; a record's group can be assigned or changed later. |
| GD-11 | **Calculated fields (2026-09-19):** a field MAY be of type `calculated`, holding an expression over other fields referenced as `[event][field]`, with the operators `+`, `-`, `*`, `/` and numeric constants; its value is stored at each (record, event, instrument, instance) position where the field is active, and automatically updated in the same transaction whenever a referenced value — or the expression itself — changes. |
| GD-12 | **Multilingual UI (2026-09-19):** the UI MUST be translatable; English is the default and the fallback language. Norwegian Bokmål and Nynorsk are the first target languages; the mapping tables admit further languages. The language setting applies to UI strings only — never to stored data, field labels, or choice values. |
| GD-13 | **Branching logic (2026-09-19):** a field or an instrument MAY carry a branching logic expression over other fields of the same record (references `[event][field]`, value comparisons with `=`, `!=`, `<`, `>`, `<=`, `>=`, the usual functions `text_contains`, `is_blank`, `is_not_blank`, logical AND/OR, parentheses); a radio/checkbox reference evaluates to `1` when checked/selected, else `0`; the field or instrument is displayed only while the expression evaluates to true (1), in surveys and in normal data entry; it is display-only and MUST NOT affect import, export, or audit. |

## 6. Dependencies

| Dependency | Impact |
|---|---|
| External OAuth2 identity provider(s) | Login flow; provider metadata (issuer, client id/secret) must be provisioned |
| Up to 3 LDAP servers (fallback) | Login fallback; directory attributes for name/email mapping |
| Fiona / RIS (external caller) | Defines the compatibility contract for `/api/`; call examples in `Endpoints.md` become acceptance tests |
| Existing REDCap deployments | Behavioral reference for response shapes (`project`, `metadata`, `event`, `formEventMapping`) |
| Network topology | PHP and Go API on a trusted internal path; public exposure of `/api/` (REDCap protocol) via TLS termination |

## 7. Success Criteria

1. Every Fiona call example in `Endpoints.md` (cURL and PHP) succeeds against the new system without modification of the caller.
2. A data entry user can create, read, and update record values through the UI; all operations appear in the correct audit table.
3. An administrator can create a project, build its structure (instruments, fields, events, mapping), assign users and roles, and issue project tokens — entirely through the web UI.
4. An export at the `export_de_identified` level returns no direct identifiers and hashed personal fields; at the `export_no_identifiers` level the same data is returned with all identifier fields removed.
5. The same application binary and code base run against SQLite (dev) and MariaDB (production) with only configuration changes.
6. No application code path writes to the database outside the Go API; the PHP layer never opens a database connection.

## 8. Traceability

| Area | Requirements document |
|---|---|
| API surface | `API_Endpoints_Requirements.md` |
| Security | `Authentication_Authorization_Requirements.md` |
| Audit | `Audit_Logging_Requirements.md` |
| Export/anonymization | `Data_Export_Anonymization_Requirements.md` |
| Validation | `Data_Validation_Requirements.md` |
| Data model | `Database_Schema_Requirements.md` |
| Configuration | `System_Configuration_Requirements.md` |
| Platform | `Technology_Stack_Requirements.md` |
| UI | `User_Interface_Requirements.md` |

Each area document carries requirement IDs prefixed with its area (`REQ-API-…`, `REQ-AUTH-…`, …) and references these business requirements where applicable.
