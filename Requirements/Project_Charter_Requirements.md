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
| BR-002 | The system must enforce granular, project-scoped permissions per user role. |
| BR-003 | The system must support flexible project structures: study arms, events, instruments, and instrument-by-event mappings. |
| BR-004 | The system must guarantee data integrity through server-side field validation before any value is stored. |
| BR-005 | The system must expose a REDCap-compatible API (Go, OpenAPI/Swagger) so existing callers (Fiona) keep working unchanged. |
| BR-006 | The system must provide a web UI (admin interface + data entry) that accesses the backend exclusively through the API. |
| BR-007 | The system must log all backend data access in two audit tables: one for change/create/delete events, one exclusively for record views. |
| BR-008 | The system must support exporting project data as CSV and JSON, with full, anonymized, and non-sensitive export levels. |
| BR-009 | The system must handle project end per the project's end provision (delete or anonymize the stored data at the REK end date). |
| BR-010 | A project must only be visible to a user who is an administrator or a member of the project. |

## 4. Scope — Phase 1

**In scope:**
- User account management (enable/disable, admin flag).
- Project creation and metadata management.
- Project structure: single-arm first (multi-arm supported in the model), events, instruments, fields (data dictionary), instrument-by-event mapping.
- Instrument designer and instrument/field reordering.
- Data entry (create/update of record values), record deletion, and record status dashboard.
- REDCap-compatible data API (`POST/GET /api/`) with content: `project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, `record` (export/import/delete).
- Administration API (`/api/v1/...`) used exclusively by the PHP web application.
- RBAC with seven permissions, built-in roles, and custom roles.
- CSV/JSON export with three sensitivity levels; end-provision execution.
- Audit logging (events + record views), immutable, yearly rollover.
- SQLite (development) and MariaDB (production) backends.

**Out of scope (phase 1):**
- Survey functionality, randomization, DDP, external modules (REDCap features never used by the planned callers).
- Per-instrument access levels (see `Data_Export_Anonymization_Requirements.md`, deviation DEV-1).
- Multi-language UI.
- File/document upload storage (radiology/pathology options are recorded as project metadata only; DICOM handling is a separate system).

## 5. Global Decisions and Conventions

These decisions were made explicit with the project owner and are binding for all area documents:

| ID | Decision |
|---|---|
| GD-1 | **Administration API authentication:** the PHP web application owns the session (PHP-native session cookie). The browser never calls `/api/v1/*` directly. PHP invokes the Go API server-side, presenting a shared service secret plus the authenticated user's ID in headers (`X-Internal-Service-Token`, `X-Internal-User-Id`). The API trusts PHP as the sole admin-API client. |
| GD-2 | **Permission set (seven):** `view`, `change`, `add`, `export_all`, `export_anonymized`, `export_non_sensitive`, `project_admin`. `export_non_sensitive` exports data with sensitive fields excluded entirely (third export level). |
| GD-3 | **Record deletion is in scope:** the REDCap-compatible API supports `content=record&action=delete`, the data entry UI offers a confirmed delete action, and deletions (including deleted values) are audit-logged. |
| GD-4 | **Admin status:** a boolean `is_admin` on the users table. The bootstrap admin is defined by `ADMIN_BOOTSTRAP_EMAIL` in configuration; the API promotes a matching login on first occurrence. Admins are managed through the users API. |
| GD-5 | **API tokens** are UUIDs mapped to (user, project) assignments, passed as the `token` form parameter in the REDCap API request body. |
| GD-6 | **Database:** SQL statements target the feature set common to current MariaDB and SQLite; dialect-specific constructs (audit table partitioning) are documented exceptions. |
| GD-7 | **Time:** all timestamps are stored and compared in UTC. |
| GD-8 | **Record identifier field:** the field at position 1 of the instrument at position 1 of the project is the record identifier; its value is the record name (`record_id`). |

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
4. An export with the `export_anonymized` permission returns no direct identifiers and hashed personal fields; `export_non_sensitive` returns the same data with sensitive fields removed.
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
