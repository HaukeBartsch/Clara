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
- Foreign keys declared everywhere (REQ-DB-004); MariaDB `ENGINE=InnoDB`, SQLite `PRAGMA foreign_keys=ON` on **every** connection — set through the driver DSN, never with a one-off statement after opening the pool, which enables it only on whichever connection answered (§10)
- Identifiers: `snake_case`; string identifiers `VARCHAR(255)`; email `VARCHAR(254)`; UUIDs `CHAR(36)`
- **Canonical value formats** (resolves ASM-VAL-1; GD-16, REQ-VAL-041): dates `YYYY-MM-DD±HH:MM`; date-times `YYYY-MM-DD HH:MM±HH:MM` — no seconds, **with the timezone of collection** (`±HH:MM`, UTC = `+00:00`); values are stored as collected, never converted to UTC (clinical data, not system timestamps — system timestamps remain UTC per REQ-DB-005/GD-7); choice lists use the REDCap encoding `code$label##code$label`; free-form text has **no application length cap** beyond the storage type (REQ-VAL-021), `LONGTEXT`/`TEXT`
- **Migrations** (REQ-DB-003): a `schema_version` table records applied versions; migrations are numbered `NNNN_name.sql`, one file per dialect under `api/internal/migrations/{sqlite,mariadb}/`, applied in order, idempotent (`CREATE TABLE IF NOT EXISTS`, guarded `ALTER`); the API applies them at startup (the project modes of GD-20 arrive as one such migration: `projects.mode` + `project_staging`)

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
    organization                VARCHAR(16)  NOT NULL,   -- main supporting institution: OUS, Ahus, SØ, SiV, ST, SSHF, SI, VVHF, SUN, NAT EU, OTHER
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
    record_naming               VARCHAR(16) NOT NULL DEFAULT 'manual',  -- auto | manual — record naming mode (REQ-DB-045)
    mode                        VARCHAR(16) NOT NULL DEFAULT 'development',  -- development | production | analysis (GD-20, REQ-DB-034); API allowlist
    creation_time               DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS users (               -- REQ-DB-008 (GD-18/GD-19)
    id            INTEGER PRIMARY KEY,
    email         VARCHAR(254) NOT NULL UNIQUE,
    display_name  VARCHAR(255) NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    auth_source   VARCHAR(8)  NOT NULL,          -- oauth2 | ldap | local
    password_hash VARCHAR(255),                  -- nullable; argon2id PHC string (DEV-AUTH-16); table-based auth (GD-18); never plaintext, never logged
    valid_until   DATE,                          -- nullable; NULL = indefinite (validity days 0); GD-19
    last_login_at DATETIME,                      -- nullable; UTC; set on every successful login; GD-19
    is_admin      INTEGER NOT NULL DEFAULT 0,    -- GD-4
    ui_language   VARCHAR(8)  NOT NULL DEFAULT 'en',  -- GD-12
    ui_theme      VARCHAR(16),                       -- nullable; installed theme id (bootstrap|darkly|yeti); NULL = follow installation default UI_THEME (GD-26, REQ-DB-008)
    session_epoch INTEGER NOT NULL DEFAULT 0,    -- raised to end every PHP session of the account: password writes, disables, 2FA reset, replay (REQ-AUTH-074, migration 0012)
    created_at    DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS user_two_factor (     -- REQ-DB-038 (GD-21); absent row = method off
    user_id               INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    method                VARCHAR(5)  NOT NULL DEFAULT 'off',  -- off | totp | email
    totp_secret           VARCHAR(64),             -- base32; never returned after enrollment display, never logged
    totp_last_step        BIGINT,                  -- last accepted RFC 6238 step; replay prevention (REQ-AUTH-058)
    email_code_hash       VARCHAR(64),             -- SHA-256 hex of the pending email code; never the code itself
    email_code_expires_at DATETIME,                -- UTC; now + TFA_EMAIL_CODE_TTL (REQ-AUTH-057)
    recovery_codes        TEXT,                    -- JSON array of {hash, consumed}; one-way hashes only
    enrolled_at           DATETIME                 -- UTC; audit anchor for tfa_enrolled (REQ-AUD-028)
);

CREATE TABLE IF NOT EXISTS password_tokens (     -- REQ-DB-039 (GD-22/GD-23); Sequence H tokens
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   CHAR(64) NOT NULL UNIQUE,       -- SHA-256 hex of the emailed token; value never stored
    purpose      VARCHAR(6) NOT NULL,            -- invite | reset
    created_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,  -- inviting admin; NULL for self-service reset
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,              -- now + AUTH_PASSWORD_TOKEN_TTL_DAYS (REQ-CFG-030)
    consumed_at  DATETIME                        -- UTC on first successful completion; expiry enforced at check
);

CREATE TABLE IF NOT EXISTS roles (               -- REQ-DB-009 (GD-2)
    id             INTEGER PRIMARY KEY,
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role_name      VARCHAR(255) NOT NULL,
    project_admin  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, role_name)
);

CREATE TABLE IF NOT EXISTS role_arms (           -- per-arm DEFAULT of a role (REQ-DB-009)
    id               INTEGER PRIMARY KEY,
    role_id          INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    arm_num          INTEGER NOT NULL,
    data_access_level VARCHAR(32) NOT NULL,  -- no_access | read_only | view_edit
    export_level     VARCHAR(32) NOT NULL,  -- export_none | export_de_identified | export_no_identifiers | export_full
    UNIQUE (role_id, arm_num)
);

CREATE TABLE IF NOT EXISTS role_grants (         -- per-pair override of a role (REQ-DB-040)
    id                INTEGER PRIMARY KEY,
    role_id           INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    event_id          INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    instrument_id     INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    data_access_level VARCHAR(32) NOT NULL,  -- no_access | read_only | view_edit; absent row = the arm default
    export_level      VARCHAR(32) NOT NULL,
    delete_values     INTEGER NOT NULL DEFAULT 0,   -- right: clear this instrument's values (REQ-AUTH-018)
    edit_surveys      INTEGER NOT NULL DEFAULT 0,   -- right: modify collected survey responses (REQ-AUTH-071)
    UNIQUE (role_id, event_id, instrument_id)
);                                             -- the event implies the arm; rows die with the design object,
                                               -- so a re-created name inherits nothing (REQ-AUTH-069)

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
    is_repeating  INTEGER NOT NULL DEFAULT 0,     -- the pair is a repeating instrument (REQ-DB-047, migration 0015)
    PRIMARY KEY (instrument_id, event_id),
    UNIQUE (instrument_id, event_id)
);

Instrument names are limited to 255 ASCII letters, digits, spaces and underscores (REQ-DB-011). The API validates this contract on creation and rename; importers preserve valid names and normalize external names that do not satisfy it.

CREATE TABLE IF NOT EXISTS fields (              -- REQ-DB-013, REQ-DB-014
    id                  INTEGER PRIMARY KEY,
    project_id          INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    instrument_id       INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    field_name          VARCHAR(255) NOT NULL,   -- lower-case alnum + underscore; unique per project
    field_label         TEXT,                    -- any length: REDCap labels exceed 255 characters (DEV-DB-15, migration 0014)
    field_type          VARCHAR(16) NOT NULL,    -- text | notes | dropdown | radio | checkbox | yesno | matrix | description | header | calculated | transfer
    section_header      TEXT,                    -- any length (DEV-DB-15, migration 0014)
    choices             TEXT,                    -- code$label##code$label (dropdown/radio/checkbox/matrix; yesno: fixed 1$Yes##0$No); code grammar Data_Validation_Design.md §9; a transfer field holds its copy instructions here instead, free text (REQ-DB-050)
    field_note          TEXT,
    validation_type     VARCHAR(32),             -- empty | built-in (integer | floating point | date | datetime) | validation_types.name (REQ-VAL-010/042)
    validation_format   VARCHAR(32),             -- accepted input format for date/datetime (e.g. Y-m-d, m-d-Y, Y-m-d H:i); see Data_Validation_Design.md
    validation_min      VARCHAR(255),
    validation_max      VARCHAR(255),
    required            INTEGER NOT NULL DEFAULT 0,
    branching_logic     TEXT,                    -- GD-13, REQ-VAL-029
    calculation         TEXT,                    -- GD-11, REQ-VAL-033 (type = calculated only)
    matrix_group        VARCHAR(255),            -- matrix rows share this (REQ-DB-014)
    field_annotation    TEXT,                    -- REDCap action tags + notes, stored as given; @READONLY (REQ-DB-046, migration 0014)
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

Notes: matrix rows are ordinary `fields` rows sharing `matrix_group`/`choices`/validation (REQ-DB-014); a **yesno field** is one row whose `choices` the API always sets to `1$Yes##0$No` (REQ-DB-043), and a **notes field** one row with neither choices nor validation type, its value multi-line free text (REQ-DB-044); **checkbox fields** hold one row with their choices and expand at API load into value columns `<field_name>___<choice_code>` — data rows are keyed by these derived names (checked stores `1`, unchecked stores nothing), the derived names are never stored in `fields`, they inherit the parent's instrument, permission pair and sensitivity flags, and no field name may collide with one (REQ-DB-042); **a `transfer` field** carries no value of its own — no data row is ever keyed by it and it never becomes a calculation or branching operand; its `choices` holds free-text copy instructions whose references are resolved against the live design when the field is saved (REQ-DB-050, REQ-VAL-046); the record identifier is the first field, by position, in the earliest instrument by `position` that has fields (GD-8 — enforced by the API, not the schema). `validation_type` resolves against `validation_types` at validation time; the four built-in structured types are validated by code and cannot be shadowed by a registry row (§4.2). The API presets `direct_identifier = 1` when a field's validation type is `email`, `MRN`, `international phone` or `national phone`; the user may set or clear it on any field (REQ-EXP-020). **Event order (GD-15, REQ-DB-011):** within an arm, events with a `period` sort by it ascending (ties by `position`); events with `period = NULL` sort by `position` after them. `position` is set by the API at creation (end of list) and by the events-order endpoint (REQ-API-103); the canonical order is computed by the API (the schema stores the inputs only) and applies to the UI event table, the record-status dashboard, `content=event`, and `content=formEventMapping`.

## 6. Data (EAV) and Record Entities

```sql
CREATE TABLE IF NOT EXISTS data (                -- REQ-DB-015…020
    project_id                 INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id                  VARCHAR(255) NOT NULL,
    unique_event_name          VARCHAR(255) NOT NULL,
    repeating_instrument       VARCHAR(255) NOT NULL,   -- '' for every form's first instance; the instrument's name for instance ≥ 2 (REQ-DB-048)
    repeating_instance_number  INTEGER NOT NULL DEFAULT 1,  -- starts at 1; 1 = a first instance (REQ-DB-019)
    field_name                 VARCHAR(255) NOT NULL,
    value                      TEXT NOT NULL,           -- LONGTEXT on MariaDB
    UNIQUE (project_id, record_id, unique_event_name,
            repeating_instrument, repeating_instance_number, field_name)   -- REQ-DB-016
);
CREATE INDEX IF NOT EXISTS idx_data_project_field  ON data (project_id, field_name);         -- REQ-DB-017 (migration 0016)
CREATE INDEX IF NOT EXISTS idx_data_project_event  ON data (project_id, unique_event_name);  -- REQ-DB-017 (migration 0016)
CREATE INDEX IF NOT EXISTS idx_data_project_record ON data (project_id, record_id);          -- REQ-DB-017, MariaDB only (migration 0019, §9)

CREATE TABLE IF NOT EXISTS record_entities (     -- REQ-DB-029 (GD-10)
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id   VARCHAR(255) NOT NULL,
    dag_group_id INTEGER REFERENCES dag_groups(id) ON DELETE SET NULL,  -- one group per record
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id)
);

CREATE TABLE IF NOT EXISTS instrument_completion (  -- REQ-DB-036 (DEV-DB-10) — sparse: a row exists only when the user marked it finished
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id      VARCHAR(255) NOT NULL,
    event_id       INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    instrument_id  INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    repeat_instance INTEGER NOT NULL DEFAULT 1,     -- the instance of a repeating instrument; 1 otherwise (REQ-DB-036/048, migration 0015)
    completed_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    completed_at   DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id, event_id, instrument_id, repeat_instance)
);
```

**Repeating instruments** (REQ-DB-047/048, DEV-DB-16). A pair of `instrument_events` with `is_repeating = 1` lets its form be filled out more than once per record at that event. The designation lives on the pair, so `SetInstrumentEventsForArm` — which replaces an arm's pairs — carries the flag over for every pair that survives and drops it with every pair it removes; a survey instrument, the first instrument, and the instrument holding the record identifier are refused (REQ-API-154). Every form's **first instance** is stored as before, at (`repeating_instrument = ''`, `repeating_instance_number = 1`), so turning repeating on or off never rewrites a value; instance n ≥ 2 is stored at (`<instrument name>`, n). The name is part of the key: a rename moves those rows in the same transaction — directly on the live path, parked and settled with the instrument's own name in a staging commit, so two swapped names cannot meet. Rows of a further instance at a pair that no longer repeats stay in `data` and are **hidden**: every reader keys them by (event, instrument) and admits them only while the pair repeats (REQ-DB-048). Deleting a field or an instrument removes every instance of its values, hidden ones included; deleting the last value of a record removes the record whatever instance it was.

**Moving a field** (REQ-DB-049, REQ-API-157) changes `fields.instrument_id` and the positions of the two instruments and nothing in `data`: values are keyed by field name and event, so a first instance shows wherever the target instrument is used. What the target does not show stays in `data`, hidden — values at an event the target is not mapped to (as for an unmapped pair), and further instances stored under the source instrument's name, which every reader admits only for a field of that instrument (`instancesIndex`, the record-status scan); moving the field back shows them again. An instrument's further instances can therefore outlive its own fields, so deleting the instrument deletes every row stored under its name (`repeating_instrument = <name>`) — directly on the live path, in a staging commit while the renamed instruments' rows are still parked — and a later instrument of the same name starts empty.

Migration 0015 (both dialects): adds `instrument_events.is_repeating`; moves values stored at `repeating_instance_number = 0` — the data API's import never set the number (DEV-DB-16) — to 1, first deleting the instance-1 twin the design-time recomputation could have written beside such a value (the instance-0 row is the one every read resolved); and widens `instrument_completion`'s key by `repeat_instance` (SQLite rebuilds the table, MariaDB alters the primary key; existing rows become instance 1). Verified on SQLite and MariaDB 11.4.

**`instrument_completion`** (REQ-DB-036, DEV-DB-10) stores **only** the user's "finished" assignment — there is no state column, because absence of the row already means "not finished" and the grey/amber split stays derived from `data` (REQ-API-074). Keeping it sparse avoids a row per (record × event × instrument) mapping in projects that never mark anything, and makes clearing the assignment a delete. It joins nothing into `data`, so it cannot make a stored value look complete. The primary key doubles as the record-status read path (`project_id, record_id` prefix, REQ-DB-025); the cascades drop a project's assignments with its records, instruments, and events (REQ-DB-036). Survey-marked instruments never get a row — their completion is filled in automatically by the read side, not stored (GD-9, REQ-API-074).

Notes: upsert is the unique constraint + `INSERT … ON CONFLICT` (SQLite) / `INSERT … ON DUPLICATE KEY UPDATE` (MariaDB) inside one transaction (REQ-DB-016, REQ-DB-026); calculated-field values live here as ordinary rows at each active position (REQ-DB-030) — per instance on a repeating instrument (REQ-DB-048); `repeating_instrument` is `''` for every first instance so the unique key stays total.

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
    target_record VARCHAR(255),                -- record name for record-scoped events (Audit_Logging_Design.md §6.4)
    details     TEXT,                            -- JSON payload per event type (audit design doc)
    created_at  DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_project ON audit_events (project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_user    ON audit_events (user_id, created_at);
-- the acting user's newest event per project, for the project overview's ordering
-- (REQ-UI-071, migration 0017; read shape and rationale in Audit_Logging_Design.md §6.5)
CREATE INDEX IF NOT EXISTS idx_audit_events_user_project ON audit_events (user_id, project_id, created_at);

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

Notes: append-only (REQ-DB-021/024): the API exposes no UPDATE/DELETE; on MariaDB the application account is granted `INSERT, SELECT` only. Yearly rollover (REQ-DB-022): MariaDB `PARTITION BY RANGE (YEAR(created_at))` on both tables, partitions pre-created per year — the partitioned table's key MUST be composite `(id, created_at)`, since MariaDB rejects a partitioned table whose primary key omits the partition column (error 1503; DEV-AUD-4); SQLite keeps per-year tables `audit_events_YYYY` behind a stable `VIEW audit_events AS SELECT * FROM audit_events_2026 UNION ALL …` (same pattern for views), and because a compound view is not insertable the writer targets the current-year physical table on SQLite while reads keep the stable name (DEV-AUD-3). The listing above is the **logical** shape; `Audit_Logging_Design.md` §6 is normative for the physical objects. `details` holds the per-event JSON shapes (audit design doc); the fixed columns carry the fields common to every event. A record opened in the web interface writes no row in `audit_record_views` — its `token NOT NULL` has nothing to hold, a browser read having no token — it appears as a `record_read` entry of `audit_events` instead (`Audit_Logging_Design.md` §3.9, REQ-AUD-035, DEV-AUD-6).

## 8. Anonymization, Survey Links, Data Access Groups, i18n, System Settings

```sql
CREATE TABLE IF NOT EXISTS anon_offsets (        -- REQ-DB-023 (GD-6, DEV-DB-3)
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id   VARCHAR(255) NOT NULL,
    offset_days INTEGER NOT NULL,                -- deterministic per (project, record)
    PRIMARY KEY (project_id, record_id)
);

CREATE TABLE IF NOT EXISTS survey_links (        -- REQ-DB-027/041 (GD-9)
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id     VARCHAR(255) NOT NULL,
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,  -- one link per event the
                                                           -- instrument is mapped to (REQ-AUTH-039)
    token         CHAR(36) NOT NULL UNIQUE,      -- opaque bearer, 128-bit random
    revoked       INTEGER NOT NULL DEFAULT 0,
    collected_at  DATETIME,                      -- first save through the link; never overwritten —
                                                           -- "collected" is this column, not a value probe (REQ-DB-041)
    created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    DATETIME NOT NULL,
    UNIQUE (project_id, record_id, instrument_id, event_id)   -- stable per quadruple until revoked
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

CREATE TABLE IF NOT EXISTS system_settings (     -- REQ-DB-037 (master spec "Rate limitter")
    key   VARCHAR(64) PRIMARY KEY,               -- dotted setting name
    value TEXT NOT NULL                          -- JSON scalar; interpreted and validated at the API boundary (REQ-API-112)
);
INSERT INTO system_settings (key, value) SELECT 'rate_limit_enabled', 'false' WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'rate_limit_enabled');
INSERT INTO system_settings (key, value) SELECT 'rate_limit_rpm',     '600'   WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'rate_limit_rpm');
-- seeded by the migration introducing the blockout period (REQ-API-113), same insert-not-schema rule
INSERT INTO system_settings (key, value) SELECT 'rate_limit_block_minutes', '10' WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'rate_limit_block_minutes');
```

Notes:
- **Date-shift algorithm (REQ-DB-023):** on first anonymized export of a record, `offset_days = SHA-256(project_id || ':' || record_id || ':' || anon_salt) mod 365` (salt from configuration, `REQ-CFG-*`), stored in `anon_offsets`; every date/date-time value in that record's export is then shifted by exactly `offset_days` — consistent across time and across export levels (GD-6). The shift applies to the **date part** of the canonical value; the collection offset (`±HH:MM`, GD-16, REQ-VAL-041) is preserved
- `survey_links.token` is the public bearer (128-bit random, REQ-AUTH-039); revocation is the `revoked` flag (REQ-API-085)
- **Events are a precondition (DEV-DB-14):** `role_grants` and `survey_links` key on `events(id)`, so a project without events has no pairs at all — it keeps its arm defaults for reading and entering values, holds no survey link, and expresses neither right (REQ-AUTH-070). Migration 0011 drops links whose instrument maps to no event rather than storing unkeyable rows; creating the role permission assignment precedes holding a survey (REQ-API-143)
- "Exactly one active group" per assignment is enforced in the API's transaction (portable partial unique indexes don't exist on MariaDB before 10.5); deleting a group with assigned records is rejected by the API (REQ-API-088)

## 9. Indexes and Capacity (REQ-DB-017, REQ-DB-025/026)

| Access pattern | Index |
|---|------|
| all values of one project (exports, REQ-API-024) | the `project_id` prefix of `idx_data_project_field` / `idx_data_project_event` |
| all values of one record (data entry, history, record rename and delete), and of one chunk of records (`record_id IN (…)`: the dashboard's streamed reads, REQ-API-172) | SQLite: the (`project_id`, `record_id`) prefix of the `data` unique key; MariaDB: `idx_data_project_record` (migration 0019) — the unique key is a hash there and answers no prefix (§10) |
| the `finished` assignments of one chunk of records (REQ-API-172) | the (`project_id`, `record_id`) prefix of the `instrument_completion` primary key |
| one field's or one event's values (designer renames, deletes, breaking-change checks) | `idx_data_project_field`, `idx_data_project_event` |
| upsert uniqueness (REQ-DB-016) | `data` unique key |
| token → assignment (every API call) | `user_projects.token` unique |
| record visibility under a DAG (REQ-AUTH-045) | `record_entities (project_id, dag_group_id)` — **add:** `CREATE INDEX idx_record_entities_dag ON record_entities (project_id, dag_group_id)` |
| audit browsing (REQ-API-077) | `idx_audit_events_project`, `idx_audit_views_project` |
| calculated-field recomputation (REQ-VAL-037) | `calculated_dependencies` primary key |
| survey link resolution (REQ-API-082) | `survey_links.token` unique |

Reference scale (REQ-DB-025): 100 projects × 10,000 records × 200 fields × 10 events ≈ 20M EAV rows. Single-record read = a (`project_id`, `record_id`) range over that record's rows only (< 500 ms target); full-project export streams over the `project_id` prefix (REQ-TECH-011) — neither walks another project's rows, though once one project holds most of the table the optimizer reads it with a table scan, the cheaper plan at that share. Checked with `EXPLAIN` on MariaDB 11.4 over 800,000 rows (2026-10-28): a single-record read and a record delete use `idx_data_project_record` on both columns; before migration 0019 they used `idx_data_project_field` on `project_id` alone and filtered the whole project. One connection per request, explicit transactions for multi-row writes (REQ-DB-026).

## 10. Dialect Exceptions (GD-6, REQ-DB-002)

| Concern | SQLite | MariaDB |
|---|---|---|
| auto-generated PKs | `INTEGER PRIMARY KEY` (rowid) | append `AUTO_INCREMENT` |
| `LONGTEXT` | `TEXT` (64-bit limit) | `LONGTEXT` |
| `DATE` | `TEXT` (`YYYY-MM-DD`) | `DATE` |
| `DATETIME` | `TEXT` (UTC) | `DATETIME` |
| `VARCHAR(n)` | `TEXT` (affinity check only) | `VARCHAR(n)` |
| `IF [NOT] EXISTS` on indexes | `CREATE INDEX IF NOT EXISTS`, `DROP INDEX IF EXISTS name` | `CREATE INDEX IF NOT EXISTS`, `DROP INDEX IF EXISTS name ON table` — required, not cosmetic: each DDL statement commits on its own, so an index migration stopped halfway is not rolled back, and the guards let the next start finish it (migration 0016) |
| unique key over 3072 bytes (`data`: four `VARCHAR(255)` utf8mb4 columns) | an ordinary B-tree; answers any leading prefix of the key | kept as a `HASH` "long unique" index: enforces uniqueness only — it answers no prefix lookup (hence `idx_data_project_record`, migration 0019) and cannot back a foreign key (hence an index led by `project_id` for `fk_data_project`, migration 0016) |
| yearly audit rollover | per-year tables + `VIEW` (no partitions) | `PARTITION BY RANGE (YEAR(created_at))` |
| append-only enforcement | application-level (no such operation exposed) | grant `INSERT, SELECT` only to the app account |
| foreign keys | `PRAGMA foreign_keys=ON` on every connection (DSN pragma, verified at startup — see the note below) | enforced (InnoDB) |
| upsert | `INSERT … ON CONFLICT DO UPDATE` | `INSERT … ON DUPLICATE KEY UPDATE` |
| `ENUM`/`CHECK` as constraint | not portable — API allowlists | API allowlists (same) |
| journal mode | **WAL** (decided 2026-10-25; SQLite is development and test only, REQ-TECH-005) — readers and one writer coexist, as on InnoDB | row-level locking with MVCC; nothing to set |

**Connection pragmas are per connection, so they belong in the DSN** (`api/internal/db/db.go`): `foreign_keys(1)`, `journal_mode(WAL)`, `busy_timeout(5000)`. Two defects came from ignoring that. A single `ExecContext("PRAGMA foreign_keys = ON")` after opening the pool enabled enforcement on whichever connection answered and left the other nine unenforced — the settings are now read back at startup, foreign keys fatally (correctness) and journal mode as a warning (a filesystem without shared memory silently keeps the rollback journal, which degrades speed, not correctness). And WAL-less SQLite excluded readers from a writer's window entirely, so any concurrency the pool allowed surfaced as `SQLITE_BUSY`; that is what turned one bad read into intermittent 500s under bulk import.

**Write transactions begin `IMMEDIATE`** (DSN `_txlock=immediate`). A deferred transaction that reads before its first write holds a WAL snapshot; once another connection commits, that write fails at once with `SQLITE_BUSY_SNAPSHOT` (517), which `busy_timeout` cannot wait out — an instrument delete under a concurrent stress-test load returned 500 this way (2026-10-30). Taking the write lock at `BEGIN` makes a transaction wait under `busy_timeout` like any other writer, as an InnoDB transaction waits on its row locks. A consequence: while a transaction is open, a write on a second pooled connection waits for it (and fails after the timeout), so such writes go through the transaction too, as reads already must. Transactions opened with `sql.TxOptions{ReadOnly: true}` still begin deferred. `db.TestSQLiteWriteTxSurvivesConcurrentCommit` guards it.

**WAL does not relax the transaction rule.** Reading on a second pooled connection while a transaction is open still sees the state from before that transaction began, whatever the journal mode — and production (MariaDB) never had WAL's forgiveness. Code inside a transaction uses the `*Tx` store methods (§10, and `dataapi.activeGroupIDTx` as the pattern); `dataapi.TestRecordImportExistingRowReadsInsideTransaction` enforces it by capping the pool at one connection, so a read that leaves the transaction cannot be served at all.

## 11. Resolved Deferred Items

| Deferred in | Resolution here |
|---|------|
| ASM-VAL-1 (canonical date forms) | dates `YYYY-MM-DD±HH:MM`; date-times `YYYY-MM-DD HH:MM±HH:MM` — with the timezone of collection (GD-16, REQ-VAL-041); stored as collected, never converted to UTC (§1) |
| REQ-VAL-030 (max length) | no application cap beyond storage — `LONGTEXT`/`TEXT` (§1, REQ-VAL-021 default) |
| choices encoding | REDCap-style `code$label##code$label` (§1) |
| REQ-DB-023 (date shift) | deterministic `offset_days` per (project, record) via salted SHA-256, persisted (§8); date part shifted, collection offset preserved |
| role level enums | `no_access`/`read_only`/`view_edit`; `export_none`/`export_de_identified`/`export_no_identifiers`/`export_full` (§4, REQ-AUTH-017) — `delete` and `edit_survey_responses` are no longer levels of the ladder but the two boolean rights of a grant (REQ-AUTH-070) |
| per-pair grants | `role_grants(role_id, event_id, instrument_id, …)` under `role_arms`; an absent row is the arm default, never a permission; a design change materializes new pairs as `read_only`/`export_none`/rights off (REQ-DB-040, REQ-AUTH-069) |
| simplified `projects` (master spec "Details", GD-17) | `projects` keeps name, organization, PI, DM, REK, dates, naming pattern (§4); removed attributes are ordinary instrument data if wanted (REQ-DB-032) |
| table-based authentication (master spec "Details", GD-18) | `users.password_hash` (nullable, argon2id) + `auth_source = local` (§4); plaintext never stored |
| session revocation (owner decision 2026-10-11, DEV-AUTH-17) | `users.session_epoch` (integer, default `0`, §4) — raised in the transaction of the change that ends the account's sessions; the API compares it with `X-Internal-Session-Epoch` (`Authentication_Authorization_Design.md` §3.1); no session table — the API stores no sessions (GD-1) |
| account validity and inactivity (master spec "Details", GD-19) | `users.valid_until` (NULL = indefinite) + `users.last_login_at` (§4); auto-disable rule in `Authentication_Authorization_Design.md` §4.4 |
| two-factor authentication (master spec "Details", GD-21) | `user_two_factor` — one row per user, method/secret/replay step/pending-code hash/recovery hashes (§4); verification and enrollment in `Authentication_Authorization_Design.md` §2.7 |
| account onboarding and password lifecycle (GD-22/GD-23) | `password_tokens` — single-use hashed invite/reset tokens (§4); issuance, email, and completion in `Authentication_Authorization_Design.md` §2.8 |
| project modes (master spec "Project modes", GD-20) | `projects.mode` (`development` default, §4) + `project_staging` JSON-snapshot table (§5 — the live structure tables stay untouched while a set is open); transition/staging rules normative in `API_Endpoints_Design.md` §4.21 |

## 12. Open Items

| Item | Owner |
|---|------|
| `details` JSON shapes per audit event type | `Audit_Logging_Design.md` |
| `anon_salt` configuration key | `System_Configuration_Design.md` |
| exact validator grammar for `validation_type` values and the `validation_types` seed patterns | `Data_Validation_Design.md` §4/§4.2 |
