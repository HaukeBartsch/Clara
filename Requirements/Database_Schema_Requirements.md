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
| REQ-DB-005 | **System** timestamps MUST be stored in UTC (GD-7, as scoped 2026-09-22). Clinical date/date-time values in the data table carry their collection timezone instead (GD-16, REQ-VAL-041). |

### 2.1 Projects

| ID | Requirement |
|---|---|
| REQ-DB-006 | Store the project's identity and ethics metadata: project name (unique), organization (the main supporting institution; enumerated: OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU), PI name/email, data manager name/email, REK number, REK start/end dates, start/end dates, participant naming pattern, creation time (GD-17). The option flags (radiology, pathology, pathology type, redcap-only, data collection from home), the end-user-contract flag, the end provision, and the initial `event_names` list are **not stored** — see REQ-DB-032. |
| REQ-DB-007 | `participant_names` MUST support two pattern styles for `generateNextRecordName`: REDCap-style digit placeholders (`8DISC[0-9][0-9][0-9]`) and a numeric counter prefix (`0001_01` → next `0002_01`, width preserved). |

### 2.2 Users and Administration

| ID | Requirement |
|---|---|
| REQ-DB-008 | Store user accounts: email (unique), display name, enabled flag, authentication source (`oauth2`|`ldap`|`local`), the `is_admin` flag (GD-4), the UI language setting (default `en`, GD-12), the local password hash (nullable; bcrypt; table-based authentication — GD-18), the account validity end date `valid_until` (nullable date; `NULL` = indefinite; set as days with `0` = indefinite — GD-19), and the last successful login `last_login_at` (nullable UTC timestamp; updated on every successful login — GD-19). |
| REQ-DB-009 | Store project roles: role name (unique per project), project-scoped, with per-arm permission assignments — a data access level and an export level for each arm (GD-2) — plus the project-level `project_admin` flag; an arm not listed in a role defaults to `no_access` / `export_none`. The example presets `data-manager`, `data-entry`, `controller` MAY be seeded per project; a project MAY have zero roles (REQ-AUTH-020). |
| REQ-DB-010 | Store user-project assignments: unique per (user, project), nullable role (role-less = full permissions for that project), and the project token (UUID) unique across the database. |

### 2.3 Project Structure

| ID | Requirement |
|---|---|
| REQ-DB-011 | Store arms (1-based `arm_num`, name), events (label, unique name `<label>_arm_<n>`, **timepoint** `period` in days after baseline — nullable, `NULL` = no timepoint, GD-15), safe region start/end in days, position), and instruments (name unique per project, position for ordering, `is_survey` flag: the instrument can be filled out via a public record link, GD-9, and an optional branching logic expression, GD-13). **Canonical per-arm event order (GD-15):** events with a timepoint first, sorted by `period` ascending (ties broken by `position`); then events without a timepoint, sorted by `position` (user-orderable via the events-order endpoint, REQ-API-103). The order applies to the UI event table, the record-status dashboard, `content=event`, and `content=formEventMapping`. |
| REQ-DB-012 | Store the instrument-by-event mapping as unique (instrument, event) pairs; an instrument becomes active for data entry only while mapped to at least one event. |
| REQ-DB-013 | Store the data dictionary per field: name (unique per project, lower-case alphanumeric + underscore), label, type (`text`, `dropdown`, `radio`, `matrix`, `description`, `header`, `calculated`), calculation expression (when type is `calculated`, GD-11), section header, choices (code/label pairs), field note, validation type + min/max (a built-in structured type or a name in the validation-type registry, REQ-DB-033), required flag, branching logic expression (optional, GD-13), matrix group, personal-information flag, direct-identifier flag (user-set on any field; preset for `email`/`MRN`/phone validation types — REQ-EXP-020), export-approval flag (for free text, see `Data_Export_Anonymization_Requirements.md`), position. |
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
| REQ-DB-030 | Store the materialized value of a calculated field as a regular EAV row, unique per (project, record_id, event, arm, repeating instrument, repeating instance) (REQ-DB-015): one row at each position where the field is active, i.e. where the field's instrument is mapped to the event; where the instrument is unmapped, no value is stored (inactive, REQ-DB-012). The rows are rewritten by recomputation (REQ-VAL-037). |

### 2.5 Audit and Anonymization Support

| ID | Requirement |
|---|---|
| REQ-DB-021 | Two audit tables MUST exist: `audit_events` (create/update/delete, structure changes, exports, authentication, administration) and `audit_record_views` (record pulls via the API only). Both are append-only (see `Audit_Logging_Requirements.md`). |
| REQ-DB-022 | Audit tables MUST support yearly rollover: MariaDB partitioned by year; SQLite per-year tables behind a stable view name. |
| REQ-DB-023 | A table MUST persist per-record anonymization date offsets (record_id → offset days) so anonymized exports are consistent across time (see `Data_Export_Anonymization_Requirements.md`). |
| REQ-DB-024 | Audit tables MUST NOT be reachable for UPDATE/DELETE by the application's database account (privilege-level immutability on MariaDB; the API simply provides no such operation). |

### 2.6 Survey Links

| ID | Requirement |
|---|---|
| REQ-DB-027 | Store survey link tokens: opaque unique token, project, record, survey instrument, created by, created at, and a revoked flag; the token is stable per (project, record, instrument) until revoked (GD-9, REQ-API-082/085). |

### 2.7 Data Access Groups

| ID | Requirement |
|---|---|
| REQ-DB-028 | Store data access groups: id, project, name (unique per project), creation time; store member assignments as (user-project assignment, group) pairs with exactly one active per assignment when one or more are present (GD-10, REQ-AUTH-044). |
| REQ-DB-029 | Store a record entity per (project, record_id): nullable data access group, creator, creation time — one group per record (GD-10, REQ-AUTH-047); the EAV value table is unchanged (REQ-DB-018). |

### 2.8 UI Translations

| ID | Requirement |
|---|---|
| REQ-DB-031 | Store UI translations (GD-12): a **languages** table (code unique, e.g. `en`/`nb`/`nn`, display name, enabled flag) and a **strings mapping** table (language code, stable key, translated text), unique per (language, key). English strings are the source of truth in the application and are NOT stored in this table; a missing translation MUST fall back to English, never to a blank or a raw key (REQ-UI-008). Keys are stable identifiers, not English text, so rewording English MUST NOT invalidate existing translations. Adding a language is an insert into these tables, not a schema change. Norwegian Bokmål and Nynorsk are the first targets; the tables admit further languages. |

### 2.9 Project Data (simplified `projects`, GD-17)

| ID | Requirement |
|---|---|
| REQ-DB-032 | The attributes removed from `projects` by GD-17 (option flags, end-user-contract flag, end provision, initial event names) are no longer project metadata and MUST NOT be stored in `projects`. The owner MAY hold them as **data** in an ordinary instrument of the project (master spec: a `DataTransferProjects` instrument): created, mapped, and filled through the ordinary designer/mapping/data-entry endpoints. The system MUST give such an instrument **no special handling** — no reserved name, no auto-creation, no special validation or export behavior. |

### 2.10 Validation-Type Registry (extensible field validation)

| ID | Requirement |
|---|---|
| REQ-DB-033 | Store the **validation-type registry**: a system-wide table of named regular expressions (name unique, pattern, built-in flag). Migrations MUST seed it with `email`, `MRN`, `international phone` and `national phone` (REQ-VAL-042/043); adding a further validation type is an insert into this table, not a schema change (mirrors the languages rule, REQ-DB-031). The four built-in structured types (`integer`, `floating point`, `date`, `datetime`) are validated by dedicated code and MUST NOT be shadowed by registry rows of the same name. A `fields.validation_type` value that names a registry entry resolves to that entry's pattern at validation time (REQ-VAL-010). |

## 3. Capacity and Performance

| ID | Requirement |
|---|---|
| REQ-DB-025 | Reference scale: 600 projects, 100,000 records, 2000 fields, 10 events (≈2000M data rows) MUST remain responsive for single-record reads (< 500 ms) and full-project exports under the limits in `Technology_Stack_Requirements.md`. |
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
| DEV-DB-5 | `projects` simplified: option flags, end-user-contract flag, end provision, and `event_names` removed from the table | Owner decision (2026-09-22, GD-17; master spec "Details"): keep PI + REK + main supporting institution; removed attributes MAY live as data in a `DataTransferProjects` instrument (REQ-DB-032). |
| DEV-DB-6 | `users` gains `password_hash`, `valid_until`, `last_login_at`; `auth_source` gains `local` | Owner decisions (2026-09-22, GD-18/GD-19; master spec "Details"): table-based authentication; account validity (days, 0 = indefinite); inactivity auto-disable (180 days) with admin re-enable. |
| DEV-DB-7 | `events.period` becomes nullable (`NULL` = no timepoint) and gains the canonical per-arm ordering rule | Owner decision (2026-09-22, GD-15; master spec "Details" event ordering): timepoint events sorted by timepoint, non-timepoint events user-reorderable. |
| DEV-DB-8 | Added `validation_types` table (seeded regex registry) and `fields.direct_identifier` flag | Owner decision (2026-09-25, master spec "Field validation"): extensible named-regex validation types; identifier classification becomes a user-set choice on any field instead of being derived from the validation type (REQ-DB-033, REQ-EXP-020, DEV-VAL-11). |
