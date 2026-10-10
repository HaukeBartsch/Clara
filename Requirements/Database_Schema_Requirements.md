# Database Schema — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source plan:** `Plan/Database_Schema.md`, `VISION_AND_REQUIREMENTS.md` (master spec)\
**Status:** Approved for development handoff\
**Date:** 2026-09-18

## 1. Purpose

Defines the persistent data model requirements. `Design/Database_Schema_Design.md` contains the normative DDL for both dialects.

## 2. Structural Requirements

| ID | Requirement |
|---|------|
| REQ-DB-001 | The schema MUST be created by SQL statements that create or alter an existing database (no ORM-managed private schema). |
| REQ-DB-002 | The schema MUST be expressible on both SQLite and MariaDB using only SQL features common to both, with documented dialect exceptions (GD-6). |
| REQ-DB-003 | A schema version table MUST record the applied schema version; migrations MUST be applied in order and MUST be idempotent. |
| REQ-DB-004 | All primary keys MUST be integer, auto-generated. All foreign key relationships MUST be declared. |
| REQ-DB-005 | **System** timestamps MUST be stored in UTC (GD-7, as scoped 2026-09-22). Clinical date/date-time values in the data table carry their collection timezone instead (GD-16, REQ-VAL-041). |

### 2.1 Projects

| ID | Requirement |
|---|------|
| REQ-DB-006 | Store the project's identity and ethics metadata: project name (unique), organization (the main supporting institution; enumerated: OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU), PI name/email, data manager name/email, REK number, REK start/end dates, start/end dates, participant naming pattern, creation time (GD-17). The option flags (radiology, pathology, pathology type, redcap-only, data collection from home), the end-user-contract flag, the end provision, and the initial `event_names` list are **not stored** — see REQ-DB-032. |
| REQ-DB-007 | `participant_names` MUST support two pattern styles for `generateNextRecordName`: REDCap-style digit placeholders (`8DISC[0-9][0-9][0-9]`) and a numeric counter prefix (`0001_01` → next `0002_01`, width preserved). |

### 2.2 Users and Administration

| ID | Requirement |
|---|------|
| REQ-DB-008 | Store user accounts: email (unique), display name, enabled flag, authentication source (`oauth2`|`ldap`|`local`), the `is_admin` flag (GD-4), the UI language setting (default `en`, GD-12), the UI theme override `ui_theme` (nullable; one of the installed theme identifiers, or `NULL` = follow the installation default `UI_THEME` — GD-26, REQ-TECH-027, REQ-CFG-031), the local password hash (nullable; bcrypt; table-based authentication — GD-18), the account validity end date `valid_until` (nullable date; `NULL` = indefinite; set as days with `0` = indefinite — GD-19), and the last successful login `last_login_at` (nullable UTC timestamp; updated on every successful login — GD-19). |
| REQ-DB-009 | Store project roles: role name (unique per project), project-scoped, with the **per-arm default** permission assignment — a data access level and an export level for each arm (GD-2) — plus the project-level `project_admin` flag; an arm not listed in a role defaults to `no_access` / `export_none`. The ordering that once ranked `delete` and `edit_survey_responses` above `view_edit` no longer holds (REQ-AUTH-070). The example presets `data-manager`, `data-entry`, `controller` MAY be seeded per project; a project MAY have zero roles (REQ-AUTH-020). |
| REQ-DB-040 | Below the arm default, the store MUST hold the role's **per-(instrument, event) grant**: for each pair of the arm, its data access level, export level, and the two rights **delete instrument values** and **edit collected surveys** (REQ-AUTH-069). The pair MUST be addressed by stable identifiers with the grant removed by cascade when the instrument or the event goes away, so a re-created name inherits nothing. A pair with no stored grant resolves to the arm default at evaluation time — the absence of a row is not a permission (REQ-AUTH-019). |
| REQ-DB-010 | Store user-project assignments: unique per (user, project), nullable role (role-less = full permissions for that project), and the project token (UUID) unique across the database. |

### 2.3 Project Structure

| ID | Requirement |
|---|------|
| REQ-DB-011 | Store arms (1-based `arm_num`, name), events (label, unique name `<label>_arm_<n>`, **timepoint** `period` in days after baseline — nullable, `NULL` = no timepoint, GD-15), safe region start/end in days, position), and instruments (name unique per project, position for ordering, `is_survey` flag: the instrument can be filled out via a public record link, GD-9, and an optional branching logic expression, GD-13). **Canonical per-arm event order (GD-15):** events with a timepoint first, sorted by `period` ascending (ties broken by `position`); then events without a timepoint, sorted by `position` (user-orderable via the events-order endpoint, REQ-API-103). The order applies to the UI event table, the record-status dashboard, `content=event`, and `content=formEventMapping`. |
| REQ-DB-012 | Store the instrument-by-event mapping as unique (instrument, event) pairs; an instrument becomes active for data entry only while mapped to at least one event. |
| REQ-DB-013 | Store the data dictionary per field: name (unique per project, lower-case alphanumeric + underscore), label, type (`text`, `dropdown`, `radio`, `matrix`, `description`, `header`, `calculated`), calculation expression (when type is `calculated`, GD-11), section header, choices (code/label pairs), field note, validation type + min/max (a built-in structured type or a name in the validation-type registry, REQ-DB-033), required flag, branching logic expression (optional, GD-13), matrix group, personal-information flag, direct-identifier flag (user-set on any field; preset for `email`/`MRN`/phone validation types — REQ-EXP-020), export-approval flag (for free text, see `Data_Export_Anonymization_Requirements.md`), position. |
| REQ-DB-014 | Matrix rows MUST be stored as individual field rows sharing the same `matrix_group`, choices, and validation, differing in `field_name` and `field_label` (REDCap-compatible metadata expansion). |

### 2.4 Data (Records)

| ID | Requirement |
|---|------|
| REQ-DB-015 | Store record values in an entity-attribute-value table keyed by (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value). |
| REQ-DB-016 | The unique constraint on that combination MUST make it impossible to store a second value for the same key; imports MUST upsert (update) rather than fail. |
| REQ-DB-017 | Indices MUST provide fast lookup of all values for one project and all values for one record_id. |
| REQ-DB-018 | Adding a new field or a new project MUST NOT change the data table layout (no per-project or per-field columns). |
| REQ-DB-019 | `value` MUST hold very long text; `repeating_instance_number` starts at 1 and is 1 for non-repeating entries. |
| REQ-DB-020 | `record_id` is the value of the record identifier field (GD-8); the API MUST enforce identifier-field rules on import (see `Data_Validation_Requirements.md`). |
| REQ-DB-030 | Store the materialized value of a calculated field as a regular EAV row, unique per (project, record_id, event, arm, repeating instrument, repeating instance) (REQ-DB-015): one row at each position where the field is active, i.e. where the field's instrument is mapped to the event; where the instrument is unmapped, no value is stored (inactive, REQ-DB-012). The rows are rewritten by recomputation (REQ-VAL-037). |

### 2.5 Audit and Anonymization Support

| ID | Requirement |
|---|------|
| REQ-DB-021 | Two audit tables MUST exist: `audit_events` (create/update/delete, structure changes, exports, authentication, administration) and `audit_record_views` (record pulls via the API only). Both are append-only (see `Audit_Logging_Requirements.md`). |
| REQ-DB-022 | Audit tables MUST support yearly rollover: MariaDB partitioned by year; SQLite per-year tables behind a stable view name. |
| REQ-DB-023 | A table MUST persist per-record anonymization date offsets (record_id → offset days) so anonymized exports are consistent across time (see `Data_Export_Anonymization_Requirements.md`). |
| REQ-DB-024 | Audit tables MUST NOT be reachable for UPDATE/DELETE by the application's database account (privilege-level immutability on MariaDB; the API simply provides no such operation). |

### 2.6 Survey Links

| ID | Requirement |
|---|------|
| REQ-DB-027 | Store survey link tokens: opaque unique token, project, record, survey instrument, **event**, created by, created at, and a revoked flag; the row is keyed by (project, record, instrument, event) and its token is live until it is submitted, revoked, or replaced by a re-issue (REQ-API-145/146), so an instrument mapped to several events holds one distinct link each (GD-9, REQ-API-082/085, REQ-AUTH-039). |
| REQ-DB-041 | A survey link MUST also carry the **collection date** — the UTC timestamp of the response saved through it, `NULL` while nothing has been collected. The API MUST stamp it on that save and MUST NOT overwrite it, so "collected" (REQ-AUTH-071) is a stored fact rather than an inference from the presence of values, and clearing every value leaves the response collected. Because a link admits exactly one submission (REQ-API-145), a non-null collection date **is also what marks the link spent**: no use counter, expiry column or history table is needed for that, and `revoked` keeps meaning "closed by a person" rather than "used". The stamp returns to `NULL` only when a fresh link is issued (REQ-API-146). |

### 2.7 Data Access Groups

| ID | Requirement |
|---|------|
| REQ-DB-028 | Store data access groups: id, project, name (unique per project), creation time; store member assignments as (user-project assignment, group) pairs with exactly one active per assignment when one or more are present (GD-10, REQ-AUTH-044). |
| REQ-DB-029 | Store a record entity per (project, record_id): nullable data access group, creator, creation time — one group per record (GD-10, REQ-AUTH-047); the EAV value table is unchanged (REQ-DB-018). |

### 2.8 UI Translations

| ID | Requirement |
|---|------|
| REQ-DB-031 | Store UI translations (GD-12): a **languages** table (code unique, e.g. `en`/`nb`/`nn`, display name, enabled flag) and a **strings mapping** table (language code, stable key, translated text), unique per (language, key). English strings are the source of truth in the application and are NOT stored in this table; a missing translation MUST fall back to English, never to a blank or a raw key (REQ-UI-008). Keys are stable identifiers, not English text, so rewording English MUST NOT invalidate existing translations. Adding a language is an insert into these tables, not a schema change. Norwegian Bokmål and Nynorsk are the first targets; the tables admit further languages. |

### 2.9 Project Data (simplified `projects`, GD-17)

| ID | Requirement |
|---|------|
| REQ-DB-032 | The attributes removed from `projects` by GD-17 (option flags, end-user-contract flag, end provision, initial event names) are no longer project metadata and MUST NOT be stored in `projects`. The owner MAY hold them as **data** in an ordinary instrument of the project (master spec: a `DataTransferProjects` instrument): created, mapped, and filled through the ordinary designer/mapping/data-entry endpoints. The system MUST give such an instrument **no special handling** — no reserved name, no auto-creation, no special validation or export behavior. |

### 2.10 Validation-Type Registry (extensible field validation)

| ID | Requirement |
|---|------|
| REQ-DB-033 | Store the **validation-type registry**: a system-wide table of named regular expressions (name unique, pattern, built-in flag). Migrations MUST seed it with `email`, `MRN`, `international phone` and `national phone` (REQ-VAL-042/043); adding a further validation type is an insert into this table, not a schema change (mirrors the languages rule, REQ-DB-031). The four built-in structured types (`integer`, `floating point`, `date`, `datetime`) are validated by dedicated code and MUST NOT be shadowed by registry rows of the same name. A `fields.validation_type` value that names a registry entry resolves to that entry's pattern at validation time (REQ-VAL-010). |

### 2.11 Project Modes and Staging (GD-20)

| ID | Requirement |
|---|------|
| REQ-DB-034 | `projects` MUST store the project's mode — `development`, `production`, or `analysis` — defaulting to `development` for new projects. Exactly one mode per project; changes flow only through the mode endpoint (REQ-API-105). |
| REQ-DB-035 | A staging table MUST hold **at most one open staging set per project**: a snapshot of the staged design (instruments, fields, events, and the instrument-event mapping, as a JSON document), who opened it, and when. Closing the set — commit or discard — removes the row; commit applies the snapshot to the live structure tables in one transaction (REQ-API-106/107). |

### 2.12 Instrument Completion State

| ID | Requirement |
|---|------|
| REQ-DB-036 | The store MUST persist the user-assigned completion of a data-collection instrument per **(record, event, instrument)**, with who set it and when, and MUST enforce at most one such row per triple (REQ-API-110, DEV-DB-10). The stored state is only the `finished` assignment: whether an instrument has no values or some values stays **derived** from the stored data, so a persisted flag can never contradict what the record actually holds. Survey instruments hold no completion state (GD-9). Rows MUST be removed with the record (cascade) and MUST NOT be created for an (event, instrument) pair that is not mapped in the project's active design. |

### 2.13 System Settings

| ID | Requirement |
|---|------|
| REQ-DB-037 | The store MUST hold a system-wide `system_settings` table — a key/value registry of runtime settings changed through the administration interface without a restart (REQ-API-112). Migrations MUST seed it with `rate_limit_enabled` = `false`, `rate_limit_rpm` = `600` and `rate_limit_block_minutes` = `10` (the third key arriving in the insert migration that introduces it, per the same rule; master spec "Rate limitter"); adding a further runtime setting is an insert into this table plus its validation at the API boundary, not a schema change (mirrors the languages rule, REQ-DB-031). Values are JSON scalars interpreted and validated by the API; the table holds no secrets (REQ-CFG-021 — secrets remain environment-only). |
| REQ-DB-038 | The store MUST hold a **per-user two-factor record** (at most one row per user): the method (`off` \| `totp` \| `email`, default `off`), the TOTP shared secret (nullable; never returned by any endpoint, never logged — REQ-AUTH-056), the last accepted TOTP time step (replay prevention, REQ-AUTH-058), the pending email code **as a one-way hash only** with its expiry (REQ-AUTH-057), the recovery codes as one-way hashes with their consumed state (REQ-AUTH-056), and the enrollment timestamp (GD-21, REQ-AUTH-054…058). A row exists only while a user has engaged with 2FA; an administrator reset clears it back to `off` and drops secret, codes, and hashes (REQ-AUTH-059); rows MUST be removed with the user (cascade). |
| REQ-DB-039 | The store MUST hold **out-of-band password tokens** (`password_tokens`, GD-22/GD-23): user id, one-way token hash (unique), purpose (`invite` \| `reset`), created-by, expiry, consumed-at. One row per outstanding token; hashes only — the token value itself is never stored (REQ-AUTH-060/062). Rows MUST be removed with the user (cascade); a re-invite replaces the account's outstanding `invite` row. |

## 3. Capacity and Performance

| ID | Requirement |
|---|------|
| REQ-DB-025 | Reference scale: 600 projects, 100,000 records, 2000 fields, 10 events (≈2000M data rows) MUST remain responsive for single-record reads (< 500 ms) and full-project exports under the limits in `Technology_Stack_Requirements.md`. |
| REQ-DB-026 | The application MUST use one connection per request with an explicit transaction for multi-row writes (imports, structure changes). |

## 4. Assumptions

| ID | Assumption |
|---|------|
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
| DEV-DB-9 | `projects` gains `mode`; added `project_staging` table (JSON design snapshot) | Owner decision (2026-09-25, GD-20; master spec "Project modes"): three project modes with staged setup changes in production; the snapshot form keeps the EAV/structure tables untouched while a staging set is open (REQ-DB-034/035). |
| DEV-DB-10 | Added `instrument_completion` — one row per (record, event, instrument) marked finished, sparse (absence = not finished) | The plan's record-status dashboard derives filled/empty from the data and stores nothing; the master spec's third state ("finished", set by the user at the end of a data-collection instrument) is a fact about the user's act, not about the values, so it cannot be derived and must be stored. Sparse rather than one row per triple because the mapped cross is large (records × events × instruments) and almost none of it is ever marked (REQ-DB-036, REQ-API-110). |
| DEV-DB-11 | Added `system_settings` (key/value registry of runtime settings, seeded with the rate-limit flag and threshold) | Owner decision (2026-09-27; master spec "Rate limitter"): rate-limit thresholds are customizable in the administration interface, so they must persist in the database rather than the environment; a key/value registry admits further runtime settings without schema changes (REQ-DB-037, REQ-API-112, DEV-CFG-3). |
| DEV-DB-12 | Added `user_two_factor` — a separate one-to-one table instead of more columns on `users` | Owner decision (2026-09-28, GD-21; master spec "Details"): by-user configurable two-factor authentication. The 2FA state (secret, last step, pending email code hash + expiry, recovery code hashes) is a distinct, resettable credential bundle with several nullable fields — a separate table keeps it out of the frequently-read `users` row and makes the admin reset a single-row delete (REQ-DB-038). |
| DEV-DB-14 | `role_grants.event_id` and `survey_links.event_id` are `NOT NULL`: a project **without events** holds no survey link and expresses neither right | Owner decision (2026-10-05, GD-2 revised): both the grant and the link are keyed by event (REQ-DB-040, REQ-DB-027), so the event is a precondition of such a row rather than a nullable column carrying a sentinel for the event-less classic layout. A project without events keeps its arm defaults for reading and entering values, but it cannot hold a link and cannot express **delete instrument values** or **edit collected surveys**, which exist only on a pair (REQ-AUTH-070); migration 0011 therefore drops links whose instrument maps to no event instead of storing rows no request can resolve. Project admins are expected to create the role permission assignment before a project holds a survey (REQ-API-143, REQ-UI-014). |
| DEV-DB-13 | `users` gains `ui_theme` (nullable theme override) | Owner decision (2026-09-28, GD-26; "allow bootstrap themes like darkly and yeti in assets/"): per-user Bootstrap theme with an installation default — `NULL` follows `UI_THEME`, mirroring how `ui_language` stores the personal setting (REQ-DB-008, REQ-CFG-031, REQ-TECH-027). |
