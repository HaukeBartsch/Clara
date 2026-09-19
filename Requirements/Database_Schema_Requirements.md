# Database Schema — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Database_Schema.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

Defines the persistent data model requirements. `Design/Database_Schema_Design.md` contains the normative DDL for both dialects.

## 2. Structural Requirements

| ID | Requirement |
|---|---|
| REQ-DB-001 | The schema MUST be created by SQL statements that create or alter an existing database (no ORM-managed private schema). |
| REQ-DB-002 | The schema MUST be expressible on both SQLite and MariaDB using only SQL features common to both, with documented dialect exceptions (GD-6). |
| REQ-DB-003 | A schema version table MUST record the applied schema version; migrations MUST be applied in order and MUST be idempotent. |
| REQ-DB-004 | All primary keys MUST be integer, auto-generated. All foreign key relationships MUST be declared. |
| REQ-DB-005 | Timestamps MUST be stored in UTC (GD-7). |

### 2.1 Projects

| ID | Requirement |
|---|---|
| REQ-DB-006 | Store all project creation fields from the master spec: project name (unique), organization (enumerated: OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU), PI name/email, data manager name/email, REK number, REK start/end dates, start/end dates, end provision (`delete`|`anonymize`), option flags (radiology, pathology, pathology type, redcap-only, data collection from home, agreed to end-user contract), participant naming pattern, initial event names (comma-separated), creation time. |
| REQ-DB-007 | `participant_names` MUST support two pattern styles for `generateNextRecordName`: REDCap-style digit placeholders (`8DISC[0-9][0-9][0-9]`) and a numeric counter prefix (`0001_01` → next `0002_01`, width preserved). |

### 2.2 Users and Administration

| ID | Requirement |
|---|---|
| REQ-DB-008 | Store user accounts: email (unique), display name, enabled flag, authentication source (`oauth2`|`ldap`), and the `is_admin` flag (GD-4). |
| REQ-DB-009 | Store project roles: role name (unique per project), project-scoped, with an explicit permission set drawn from the seven permissions (GD-2). Built-in roles `data-manager`, `data-entry`, `controller` MUST be seedable per project. |
| REQ-DB-010 | Store user-project assignments: unique per (user, project), nullable role (role-less = full permissions for that project), and the project token (UUID) unique across the database. |

### 2.3 Project Structure

| ID | Requirement |
|---|---|
| REQ-DB-011 | Store arms (1-based `arm_num`, name), events (label, unique name `<label>_arm_<n>`, period in days, safe region start/end in days, position), and instruments (name unique per project, position for ordering). |
| REQ-DB-012 | Store the instrument-by-event mapping as unique (instrument, event) pairs; an instrument becomes active for data entry only while mapped to at least one event. |
| REQ-DB-013 | Store the data dictionary per field: name (unique per project, lower-case alphanumeric + underscore), label, type (`text`, `dropdown`, `radio`, `matrix`, `description`, `header`), section header, choices (code/label pairs), field note, validation type + min/max, required flag, branching logic, matrix group, personal-information flag, export-approval flag (for free text, see `Data_Export_Anonymization_Requirements.md`), position. |
| REQ-DB-014 | Matrix rows MUST be stored as individual field rows sharing the same `matrix_group`, choices, and validation, differing in `field_name` and `field_label` (REDCap-compatible metadata expansion). |

### 2.4 Data (Records)

| ID | Requirement |
|---|---|
| REQ-DB-015 | Store record values in an entity-attribute-value table keyed by (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value). |
| REQ-DB-016 | The unique constraint on that combination MUST make it impossible to store a second value for the same key; imports MUST upsert (update) rather than fail. |
| REQ-DB-017 | Indices MUST provide fast lookup of all values for one project and all values for one record_id. |
| REQ-DB-018 | Adding a new field or a new project MUST NOT change the data table layout (no per-project or per-field columns). |
| REQ-DB-019 | `value` MUST hold very long text; `repeating_instance_number` starts at 1 and is 1 for non-repeating entries. |
| REQ-DB-020 | `record_id` is the value of the record identifier field (GD-8); the API MUST enforce identifier-field rules on import (see `Data_Validation_Requirements.md`). |

### 2.5 Audit and Anonymization Support

| ID | Requirement |
|---|---|
| REQ-DB-021 | Two audit tables MUST exist: `audit_events` (create/update/delete, structure changes, exports, authentication, administration) and `audit_record_views` (record pulls via the API only). Both are append-only (see `Audit_Logging_Requirements.md`). |
| REQ-DB-022 | Audit tables MUST support yearly rollover: MariaDB partitioned by year; SQLite per-year tables behind a stable view name. |
| REQ-DB-023 | A table MUST persist per-record anonymization date offsets (record_id → offset days) so anonymized exports are consistent across time (see `Data_Export_Anonymization_Requirements.md`). |
| REQ-DB-024 | Audit tables MUST NOT be reachable for UPDATE/DELETE by the application's database account (privilege-level immutability on MariaDB; the API simply provides no such operation). |

## 3. Capacity and Performance

| ID | Requirement |
|---|---|
| REQ-DB-025 | Reference scale: 100 projects, 10,000 records, 200 fields, 10 events (≈20M data rows) MUST remain responsive for single-record reads (< 500 ms) and full-project exports under the limits in `Technology_Stack_Requirements.md`. |
| REQ-DB-026 | The application MUST use one connection per request with an explicit transaction for multi-row writes (imports, structure changes). |

## 4. Assumptions

| ID | Assumption |
|---|---|
| ASM-DB-1 | A single database per environment; no sharding. |
| ASM-DB-2 | Booleans are stored as integers (0/1) on both dialects; text as `TEXT` (SQLite) / `LONGTEXT` for values (MariaDB); dialect-specific DDL is generated from one source of truth (see design). |

## 5. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-DB-1 | Added `users.is_admin` | Decision GD-4 (admin status mechanism). |
| DEV-DB-2 | Added `fields.export_approved` | `Data_Export_Anonymization` plan: free text excluded "unless explicitly approved for export" — approval must be storable. |
| DEV-DB-3 | Added `anon_offsets` table | Anonymization plan: date shift "consistent for each patient" — offset must persist. |
| DEV-DB-4 | Added `audit_events.source`, `audit_record_views.record_ids` | Audit plan: record views must capture which records/instruments and which token; source distinguishes API vs UI origin. |
