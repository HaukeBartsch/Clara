# Database Schema — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Database_Schema_Requirements.md`
**Date:** 2026-09-19

## 1. Purpose and Conventions

This document contains the **normative** logical schema and DDL. MariaDB is the reference dialect (production); the SQLite variant differs only where §12 (dialect exceptions) says so (GD-6, REQ-DB-002). Conventions:

- One logical schema, two dialect renderings; the DDL below is written in the common subset (REQ-DB-002)
- All primary keys `id INTEGER` auto-generated (REQ-DB-004); SQLite: `INTEGER PRIMARY KEY` (rowid alias); MariaDB: append `AUTO_INCREMENT`
- Booleans: `INTEGER` 0/1 on both dialects (ASM-DB-2)
- All timestamps UTC (REQ-DB-005, GD-7): MariaDB `DATETIME`, SQLite `TEXT` (`YYYY-MM-DD HH:MM:SS`, UTC) — see §2
- Foreign keys declared everywhere (REQ-DB-004); MariaDB `ENGINE=InnoDB`, SQLite `PRAGMA foreign_keys=ON` per connection
- Identifiers: `snake_case`; string identifiers `VARCHAR(255)`; email `VARCHAR(254)`; UUIDs `CHAR(36)`
- **Canonical value formats** (resolves ASM-VAL-1; GD-16, REQ-VAL-041): dates `YYYY-MM-DD±HH:MM`; date-times `YYYY-MM-DD HH:MM±HH:MM` — no seconds, **with the timezone of collection** (`±HH:MM`, UTC = `+00:00`); values are stored as collected, never converted to UTC (clinical data, not system timestamps — system timestamps remain UTC per REQ-DB-005/GD-7); choice lists use the REDCap encoding `code$label##code$label`; free-form text has **no application length cap** beyond the storage type (REQ-VAL-021), `LONGTEXT`/`TEXT`
- **Migrations** (REQ-DB-003): a `schema_version` table records applied versions; migrations are numbered `NNN_name.up.sql`, applied in order, idempotent (`CREATE TABLE IF NOT EXISTS`, guarded `ALTER`); the API applies them at startup (the project modes of GD-20 arrive as one such migration: `projects.mode` + `project_staging`)

## 2. Logical Type Map

| Logical type | SQLite | MariaDB | Notes |
|---|---|---|---|
| id / counts / flags | `INTEGER` | `INTEGER` | PKs auto-generated |
| short string (≤255) | `TEXT` | `VARCHAR(255)` | indexed/unique columns |
| email | `TEXT` | `VARCHAR(254)` | RFC 5321 limit |
| uuid | `TEXT` | `CHAR(36)` | tokens |
| medium text | `TEXT` | `TEXT` | labels, notes, choices, expressions |
| value text | `TEXT` | `LONGTEXT` | EAV values, translations |
| date | `TEXT` | `DATE` | canonical `YYYY-MM-DD` |
| timestamp | `TEXT` | `DATETIME` | UTC, `YYYY-MM-DD HH:MM:SS` |

## 3. Schema Version

```sql
CREATE TABLE IF NOT EXISTS schema_version (
    version     INTEGER PRIMARY KEY,   -- 1, 2, 3, ...
    applied_at  DATETIME NOT NULL
);
```

## 4. Projects, Users, Roles, Assignments

```sql
CREATE TABLE IF NOT EXISTS projects (            -- REQ-DB-006 (simplified, GD-17)
    id                          INTEGER PRIMARY KEY,
    project_name                VARCHAR(255) NOT NULL UNIQUE,
    organization                VARCHAR(16)  NOT NULL,   -- main supporting institution: OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU
    pi_name                     VARCHAR(255) NOT NULL,
    pi_email                    VARCHAR(254) NOT NULL,
    dm_name                     VARCHAR(255),
    dm_email                    VARCHAR(254),
    rek_number                  VARCHAR(255),
    rek_start_date              DATE,
    rek_end_date                DATE,
    start_date                  DATE,
    end_date                    DATE,
    participant_names           VARCHAR(255) NOT NULL,   -- naming pattern (REQ-DB-007)
    mode                        VARCHAR(16) NOT NULL DEFAULT 'development',  -- development | production | analysis (GD-20, REQ-DB-034); API allowlist
    creation_time               DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS users (               -- REQ-DB-008 (GD-18/GD-19)
    id            INTEGER PRIMARY KEY,
    email         VARCHAR(254) NOT NULL UNIQUE,
    display_name  VARCHAR(255) NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    auth_source   VARCHAR(8)  NOT NULL,          -- oauth2 | ldap | local
    password_hash VARCHAR(255),                  -- nullable; bcrypt; table-based auth (GD-18); never plaintext, never logged
    valid_until   DATE,                          -- nullable; NULL = indefinite (validity days 0); GD-19
    last_login_at DATETIME,                      -- nullable; UTC; set on every successful login; GD-19
    is_admin      INTEGER NOT NULL DEFAULT 0,    -- GD-4
    ui_language   VARCHAR(8)  NOT NULL DEFAULT 'en',  -- GD-12
    created_at    DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS roles (               -- REQ-DB-009 (GD-2)
    id             INTEGER PRIMARY KEY,
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role_name      VARCHAR(255) NOT NULL,
    project_admin  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, role_name)
);

CREATE TABLE IF NOT EXISTS role_arms (           -- per-arm levels of a role (REQ-DB-009)
    id               INTEGER PRIMARY KEY,
    role_id          INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    arm_num          INTEGER NOT NULL,
    data_access_level VARCHAR(32) NOT NULL,  -- no_access | read_only | view_edit | delete | edit_survey_responses
    export_level     VARCHAR(32) NOT NULL,  -- export_none | export_de_identified | export_no_identifiers | export_full
    UNIQUE (role_id, arm_num)
);

CREATE TABLE IF NOT EXISTS user_projects (       -- REQ-DB-010
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role_id     INTEGER REFERENCES roles(id) ON DELETE SET NULL,  -- NULL = full permissions
    token       CHAR(36) NOT NULL UNIQUE,       -- project token, unique across the DB
    created_at  DATETIME NOT NULL,
    UNIQUE (user_id, project_id)
);
CREATE INDEX IF NOT EXISTS idx_user_projects_user ON user_projects (user_id);
```

Notes: `organization` and the level enums are enforced by the API's allowlists (SQLite `CHECK` is not portable); `ON DELETE SET NULL` on `role_id` means deleting a role leaves the assignment role-less (full permissions) — the API's role-deletion rule (REQ-API-059) is the authority on when deletion is allowed.

## 5. Project Structure

```sql
CREATE TABLE IF NOT EXISTS arms (                -- REQ-DB-011
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    arm_num     INTEGER NOT NULL,                -- 1-based
    name        VARCHAR(255),
    position    INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, arm_num)
);

CREATE TABLE IF NOT EXISTS events (              -- REQ-DB-011 (GD-15)
    id                 INTEGER PRIMARY KEY,
    project_id         INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    arm_id             INTEGER NOT NULL REFERENCES arms(id) ON DELETE CASCADE,
    event_name         VARCHAR(255) NOT NULL,    -- label, e.g. baseline
    unique_event_name  VARCHAR(255) NOT NULL,    -- <label>_arm_<n>
    period             INTEGER,                  -- timepoint: days after baseline; NULL = no timepoint (GD-15)
    safe_region_start  INTEGER,                            -- days before (e.g. -2)
    safe_region_end    INTEGER,                            -- days after  (e.g. +3)
    position           INTEGER NOT NULL DEFAULT 1,         -- user order for no-timepoint events (and period ties)
    UNIQUE (project_id, unique_event_name),
    UNIQUE (project_id, arm_id, event_name)
);

CREATE TABLE IF NOT EXISTS instruments (         -- REQ-DB-011
    id               INTEGER PRIMARY KEY,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name             VARCHAR(255) NOT NULL,
    position         INTEGER NOT NULL DEFAULT 1,
    is_survey        INTEGER NOT NULL DEFAULT 0,        -- GD-9
    branching_logic  TEXT,                             -- GD-13, REQ-VAL-040
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS instrument_events (   -- REQ-DB-012
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (instrument_id, event_id),
    UNIQUE (instrument_id, event_id)
);

CREATE TABLE IF NOT EXISTS fields (              -- REQ-DB-013, REQ-DB-014
    id                  INTEGER PRIMARY KEY,
    project_id          INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    instrument_id       INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    field_name          VARCHAR(255) NOT NULL,   -- lower-case alnum + underscore; unique per project
    field_label         VARCHAR(255),
    field_type          VARCHAR(16) NOT NULL,    -- text | dropdown | radio | matrix | description | header | calculated
    section_header      VARCHAR(255),
    choices             TEXT,                    -- code$label##code$label (dropdown/radio/matrix)
    field_note          TEXT,
    validation_type     VARCHAR(32),             -- empty | built-in (integer | floating point | date | datetime) | validation_types.name (REQ-VAL-010/042)
    validation_format   VARCHAR(32),             -- accepted input format for date/datetime (e.g. Y-m-d, m-d-Y, Y-m-d H:i); see Data_Validation_Design.md
    validation_min      VARCHAR(255),
    validation_max      VARCHAR(255),
    required            INTEGER NOT NULL DEFAULT 0,
    branching_logic     TEXT,                    -- GD-13, REQ-VAL-029
    calculation         TEXT,                    -- GD-11, REQ-VAL-033 (type = calculated only)
    matrix_group        VARCHAR(255),            -- matrix rows share this (REQ-DB-014)
    personal_information INTEGER NOT NULL DEFAULT 0,
    direct_identifier   INTEGER NOT NULL DEFAULT 0,   -- user-set on any field; preset by the API for email/MRN/phone types (REQ-EXP-020, DEV-EXP-5)
    export_approved     INTEGER NOT NULL DEFAULT 0,   -- DEV-DB-2 (free-text export approval)
    position            INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, field_name),
    UNIQUE (project_id, instrument_id, position)
);

-- Maintained by the API whenever a calculated field's expression changes (REQ-VAL-037);
-- lets the recomputation find affected fields without re-parsing every expression.
CREATE TABLE IF NOT EXISTS calculated_dependencies (   -- GD-11 support
    project_id            INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    calculated_field_id   INTEGER NOT NULL REFERENCES fields(id) ON DELETE CASCADE,
    ref_unique_event_name VARCHAR(255) NOT NULL,
    ref_field_name        VARCHAR(255) NOT NULL,
    PRIMARY KEY (project_id, calculated_field_id, ref_unique_event_name, ref_field_name)
);

-- System-wide (not per-project): extensible field validation (REQ-DB-033, REQ-VAL-042/043).
-- Adding a validation type is an INSERT here, not a schema change (mirrors `languages`, REQ-DB-031).
CREATE TABLE IF NOT EXISTS validation_types (
    name      VARCHAR(64) PRIMARY KEY,   -- shown to the designer; referenced by fields.validation_type
    regex     TEXT NOT NULL,             -- Go RE2 pattern, full-value match (§4.2); served to the designer for advisory hints (REQ-API-104)
    builtin   INTEGER NOT NULL DEFAULT 0 -- seeded entries; cannot be removed while referenced
);

-- Seed (idempotent, same style as the languages seed): grammars are normative in Data_Validation_Design.md §4.
INSERT INTO validation_types (name, regex, builtin) SELECT 'email',               '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$', 1 WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'email');
INSERT INTO validation_types (name, regex, builtin) SELECT 'MRN',                 '^[0-9]{11}$',                                          1 WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'MRN');
INSERT INTO validation_types (name, regex, builtin) SELECT 'international phone', '^\+[1-9][0-9 ]{7,14}$',                                1 WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'international phone');
INSERT INTO validation_types (name, regex, builtin) SELECT 'national phone',      '^[0-9]{8}$',                                           1 WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'national phone');

-- One OPEN staging set per project at most (primary key); the row exists only while
-- staging is open in production mode (GD-20, REQ-DB-035). Commit applies the snapshot
-- to the live structure tables in one transaction and deletes the row; discard only
-- deletes the row. Data collection never reads this table (REQ-API-107).
CREATE TABLE IF NOT EXISTS project_staging (
    project_id  INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    design      TEXT NOT NULL,       -- staged-design snapshot (JSON): {instruments:[{name,position,is_survey,branching_logic,fields:[…]}], arms:[{arm_num,name,events:[…]}], mapping:{arm_num:{instrument:[unique_event_name]}}}
    opened_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    opened_at   DATETIME NOT NULL
);
```

Notes: matrix rows are ordinary `fields` rows sharing `matrix_group`/`choices`/validation (REQ-DB-014); the record identifier is the first field of the first instrument by `position` (GD-8 — enforced by the API, not the schema). `validation_type` resolves against `validation_types` at validation time; the four built-in structured types are validated by code and cannot be shadowed by a registry row (§4.2). The API presets `direct_identifier = 1` when a field's validation type is `email`, `MRN`, `international phone` or `national phone`; the user may set or clear it on any field (REQ-EXP-020). **Event order (GD-15, REQ-DB-011):** within an arm, events with a `period` sort by it ascending (ties by `position`); events with `period = NULL` sort by `position` after them. `position` is set by the API at creation (end of list) and by the events-order endpoint (REQ-API-103); the canonical order is computed by the API (the schema stores the inputs only) and applies to the UI event table, the record-status dashboard, `content=event`, and `content=formEventMapping`.

## 6. Data (EAV) and Record Entities

```sql
CREATE TABLE IF NOT EXISTS data (                -- REQ-DB-015…020
    project_id                 INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id                  VARCHAR(255) NOT NULL,
    unique_event_name          VARCHAR(255) NOT NULL,
    repeating_instrument       VARCHAR(255) NOT NULL,   -- '' for non-repeating
    repeating_instance_number  INTEGER NOT NULL DEFAULT 1,  -- starts at 1; 1 = non-repeating (REQ-DB-019)
    field_name                 VARCHAR(255) NOT NULL,
    value                      TEXT NOT NULL,           -- LONGTEXT on MariaDB
    UNIQUE (project_id, record_id, unique_event_name,
            repeating_instrument, repeating_instance_number, field_name)   -- REQ-DB-016
);
CREATE INDEX IF NOT EXISTS idx_data_project ON data (project_id);          -- REQ-DB-017
CREATE INDEX IF NOT EXISTS idx_data_record  ON data (record_id);           -- REQ-DB-017

CREATE TABLE IF NOT EXISTS record_entities (     -- REQ-DB-029 (GD-10)
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id   VARCHAR(255) NOT NULL,
    dag_group_id INTEGER REFERENCES dag_groups(id) ON DELETE SET NULL,  -- one group per record
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id)
);
```

Notes: upsert is the unique constraint + `INSERT … ON CONFLICT` (SQLite) / `INSERT … ON DUPLICATE KEY UPDATE` (MariaDB) inside one transaction (REQ-DB-016, REQ-DB-026); calculated-field values live here as ordinary rows at each active position (REQ-DB-030); `repeating_instrument` is `''` for non-repeating entries so the unique key stays total.

## 7. Audit

```sql
CREATE TABLE IF NOT EXISTS audit_events (        -- REQ-DB-021 (see Audit_Logging_Requirements.md)
    id          INTEGER PRIMARY KEY,
    event_type  VARCHAR(64)  NOT NULL,           -- taxonomy in the audit requirements
    source      VARCHAR(8)   NOT NULL,           -- api | ui | system (DEV-DB-4)
    user_id     INTEGER,
    email       VARCHAR(254),
    token       CHAR(36),                        -- project token when relevant (never logged elsewhere, REQ-API-005)
    project_id  INTEGER,
    arm_num     INTEGER,
    role        VARCHAR(255),
    details     TEXT,                            -- JSON payload per event type (audit design doc)
    created_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_project ON audit_events (project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_user    ON audit_events (user_id, created_at);

CREATE TABLE IF NOT EXISTS audit_record_views (  -- REQ-DB-021: record pulls via the API only
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER,
    email       VARCHAR(254),
    token       CHAR(36) NOT NULL,
    project_id  INTEGER NOT NULL,
    record_ids  TEXT NOT NULL,                   -- JSON array (DEV-DB-4)
    instruments TEXT,                            -- JSON array
    created_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_views_project ON audit_record_views (project_id, created_at);
```

Notes: append-only (REQ-DB-021/024): the API exposes no UPDATE/DELETE; on MariaDB the application account is granted `INSERT, SELECT` only. Yearly rollover (REQ-DB-022): MariaDB `PARTITION BY RANGE (YEAR(created_at))` on both tables, partitions pre-created per year; SQLite keeps per-year tables `audit_events_YYYY` behind a stable `VIEW audit_events AS SELECT * FROM audit_events_2026 UNION ALL …` (same pattern for views). `details` holds the per-event JSON shapes (audit design doc); the fixed columns carry the fields common to every event.

## 8. Anonymization, Survey Links, Data Access Groups, i18n

```sql
CREATE TABLE IF NOT EXISTS anon_offsets (        -- REQ-DB-023 (GD-6, DEV-DB-3)
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id   VARCHAR(255) NOT NULL,
    offset_days INTEGER NOT NULL,                -- deterministic per (project, record)
    PRIMARY KEY (project_id, record_id)
);

CREATE TABLE IF NOT EXISTS survey_links (        -- REQ-DB-027 (GD-9)
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id     VARCHAR(255) NOT NULL,
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    token         CHAR(36) NOT NULL UNIQUE,      -- opaque bearer, 128-bit random
    revoked       INTEGER NOT NULL DEFAULT 0,
    created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    DATETIME NOT NULL,
    UNIQUE (project_id, record_id, instrument_id)   -- stable per triple until revoked
);

CREATE TABLE IF NOT EXISTS dag_groups (          -- REQ-DB-028 (GD-10)
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    created_at  DATETIME NOT NULL,
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS dag_memberships (     -- REQ-DB-028 (REQ-AUTH-044)
    id            INTEGER PRIMARY KEY,
    assignment_id INTEGER NOT NULL REFERENCES user_projects(id) ON DELETE CASCADE,
    group_id      INTEGER NOT NULL REFERENCES dag_groups(id) ON DELETE CASCADE,
    is_active     INTEGER NOT NULL DEFAULT 0,    -- exactly one per assignment when any
    UNIQUE (assignment_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_dag_memberships_assignment ON dag_memberships (assignment_id);

CREATE TABLE IF NOT EXISTS languages (           -- REQ-DB-031 (GD-12)
    id           INTEGER PRIMARY KEY,
    code         VARCHAR(8) NOT NULL UNIQUE,     -- en, nb, nn, ...
    display_name VARCHAR(255) NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS i18n_strings (        -- REQ-DB-031
    id           INTEGER PRIMARY KEY,
    language_id  INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
    key          VARCHAR(255) NOT NULL,          -- stable identifier, never English text
    text         TEXT NOT NULL,                  -- LONGTEXT on MariaDB
    UNIQUE (language_id, key)
);
```

Notes:
- **Date-shift algorithm (REQ-DB-023):** on first anonymized export of a record, `offset_days = SHA-256(project_id || ':' || record_id || ':' || anon_salt) mod 365` (salt from configuration, `REQ-CFG-*`), stored in `anon_offsets`; every date/date-time value in that record's export is then shifted by exactly `offset_days` — consistent across time and across export levels (GD-6). The shift applies to the **date part** of the canonical value; the collection offset (`±HH:MM`, GD-16, REQ-VAL-041) is preserved
- `survey_links.token` is the public bearer (128-bit random, REQ-AUTH-039); revocation is the `revoked` flag (REQ-API-085)
- "Exactly one active group" per assignment is enforced in the API's transaction (portable partial unique indexes don't exist on MariaDB before 10.5); deleting a group with assigned records is rejected by the API (REQ-API-088)

## 9. Indexes and Capacity (REQ-DB-017, REQ-DB-025/026)

| Access pattern | Index |
|---|---|
| all values of one project (exports, REQ-API-024) | `idx_data_project` |
| all values of one record (data entry, history) | `idx_data_record` |
| upsert uniqueness (REQ-DB-016) | `data` unique key |
| token → assignment (every API call) | `user_projects.token` unique |
| record visibility under a DAG (REQ-AUTH-045) | `record_entities (project_id, dag_group_id)` — **add:** `CREATE INDEX idx_record_entities_dag ON record_entities (project_id, dag_group_id)` |
| audit browsing (REQ-API-077) | `idx_audit_events_project`, `idx_audit_views_project` |
| calculated-field recomputation (REQ-VAL-037) | `calculated_dependencies` primary key |
| survey link resolution (REQ-API-082) | `survey_links.token` unique |

Reference scale (REQ-DB-025): 100 projects × 10,000 records × 200 fields × 10 events ≈ 20M EAV rows. Single-record read = `idx_data_record` lookup (< 500 ms target); full-project export streams via `idx_data_project` (REQ-TECH-011) — no full table scan at either. One connection per request, explicit transactions for multi-row writes (REQ-DB-026).

## 10. Dialect Exceptions (GD-6, REQ-DB-002)

| Concern | SQLite | MariaDB |
|---|---|---|
| auto-generated PKs | `INTEGER PRIMARY KEY` (rowid) | append `AUTO_INCREMENT` |
| `LONGTEXT` | `TEXT` (64-bit limit) | `LONGTEXT` |
| `DATE` | `TEXT` (`YYYY-MM-DD`) | `DATE` |
| `DATETIME` | `TEXT` (UTC) | `DATETIME` |
| `VARCHAR(n)` | `TEXT` (affinity check only) | `VARCHAR(n)` |
| `IF NOT EXISTS` on indexes | not supported — create in a temp check or `CREATE INDEX` guarded by migration state | supported |
| yearly audit rollover | per-year tables + `VIEW` (no partitions) | `PARTITION BY RANGE (YEAR(created_at))` |
| append-only enforcement | application-level (no such operation exposed) | grant `INSERT, SELECT` only to the app account |
| foreign keys | `PRAGMA foreign_keys=ON` per connection | enforced (InnoDB) |
| upsert | `INSERT … ON CONFLICT DO UPDATE` | `INSERT … ON DUPLICATE KEY UPDATE` |
| `ENUM`/`CHECK` as constraint | not portable — API allowlists | API allowlists (same) |

## 11. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| ASM-VAL-1 (canonical date forms) | dates `YYYY-MM-DD±HH:MM`; date-times `YYYY-MM-DD HH:MM±HH:MM` — with the timezone of collection (GD-16, REQ-VAL-041); stored as collected, never converted to UTC (§1) |
| REQ-VAL-030 (max length) | no application cap beyond storage — `LONGTEXT`/`TEXT` (§1, REQ-VAL-021 default) |
| choices encoding | REDCap-style `code$label##code$label` (§1) |
| REQ-DB-023 (date shift) | deterministic `offset_days` per (project, record) via salted SHA-256, persisted (§8); date part shifted, collection offset preserved |
| role level enums | `no_access`/`read_only`/`view_edit`/`delete`/`edit_survey_responses`; `export_none`/`export_de_identified`/`export_no_identifiers`/`export_full` (§4, REQ-AUTH-017) |
| simplified `projects` (master spec "Details", GD-17) | `projects` keeps name, organization, PI, DM, REK, dates, naming pattern (§4); removed attributes are ordinary instrument data if wanted (REQ-DB-032) |
| table-based authentication (master spec "Details", GD-18) | `users.password_hash` (nullable, bcrypt) + `auth_source = local` (§4); plaintext never stored |
| account validity and inactivity (master spec "Details", GD-19) | `users.valid_until` (NULL = indefinite) + `users.last_login_at` (§4); auto-disable rule in `Authentication_Authorization_Design.md` §4.4 |
| project modes (master spec "Project modes", GD-20) | `projects.mode` (`development` default, §4) + `project_staging` JSON-snapshot table (§5 — the live structure tables stay untouched while a set is open); transition/staging rules normative in `API_Endpoints_Design.md` §4.21 |

## 12. Open Items

| Item | Owner |
|---|---|
| `details` JSON shapes per audit event type | `Audit_Logging_Design.md` |
| `anon_salt` configuration key | `System_Configuration_Design.md` |
| exact validator grammar for `validation_type` values and the `validation_types` seed patterns | `Data_Validation_Design.md` §4/§4.2 |
