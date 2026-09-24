-- CLARA data store — SQLite rendering (Database_Schema_Design.md, GD-6).
-- Dialect: SQLite. TEXT for all strings/dates/times; INTEGER for ids/flags.
-- All primary keys are rowid aliases (INTEGER PRIMARY KEY).

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS projects (
    id                  INTEGER PRIMARY KEY,
    project_name        TEXT NOT NULL UNIQUE,
    organization        TEXT NOT NULL,
    pi_name             TEXT NOT NULL,
    pi_email            TEXT NOT NULL,
    dm_name             TEXT,
    dm_email            TEXT,
    rek_number          TEXT,
    rek_start_date      TEXT,
    rek_end_date        TEXT,
    start_date          TEXT,
    end_date            TEXT,
    participant_names   TEXT NOT NULL,
    creation_time       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    auth_source   TEXT NOT NULL,
    password_hash TEXT,
    valid_until   TEXT,
    last_login_at TEXT,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    ui_language   TEXT NOT NULL DEFAULT 'en',
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS roles (
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role_name     TEXT NOT NULL,
    project_admin INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, role_name)
);

CREATE TABLE IF NOT EXISTS role_arms (
    id                INTEGER PRIMARY KEY,
    role_id           INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    arm_num           INTEGER NOT NULL,
    data_access_level TEXT NOT NULL,
    export_level      TEXT NOT NULL,
    UNIQUE (role_id, arm_num)
);

CREATE TABLE IF NOT EXISTS user_projects (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role_id    INTEGER REFERENCES roles(id) ON DELETE SET NULL,
    token      TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    UNIQUE (user_id, project_id)
);
CREATE INDEX IF NOT EXISTS idx_user_projects_user ON user_projects (user_id);

CREATE TABLE IF NOT EXISTS arms (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    arm_num    INTEGER NOT NULL,
    name       TEXT,
    position   INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, arm_num)
);

CREATE TABLE IF NOT EXISTS events (
    id                INTEGER PRIMARY KEY,
    project_id        INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    arm_id            INTEGER NOT NULL REFERENCES arms(id) ON DELETE CASCADE,
    event_name        TEXT NOT NULL,
    unique_event_name TEXT NOT NULL,
    period            INTEGER,
    safe_region_start INTEGER,
    safe_region_end   INTEGER,
    position          INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, unique_event_name),
    UNIQUE (project_id, arm_id, event_name)
);

CREATE TABLE IF NOT EXISTS instruments (
    id              INTEGER PRIMARY KEY,
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 1,
    is_survey       INTEGER NOT NULL DEFAULT 0,
    branching_logic TEXT,
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS instrument_events (
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (instrument_id, event_id)
);

CREATE TABLE IF NOT EXISTS fields (
    id                   INTEGER PRIMARY KEY,
    project_id           INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    instrument_id        INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    field_name           TEXT NOT NULL,
    field_label          TEXT,
    field_type           TEXT NOT NULL,
    section_header       TEXT,
    choices              TEXT,
    field_note           TEXT,
    validation_type      TEXT,
    validation_format    TEXT,
    validation_min       TEXT,
    validation_max       TEXT,
    required             INTEGER NOT NULL DEFAULT 0,
    branching_logic      TEXT,
    calculation          TEXT,
    matrix_group         TEXT,
    personal_information INTEGER NOT NULL DEFAULT 0,
    export_approved      INTEGER NOT NULL DEFAULT 0,
    position             INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, field_name),
    UNIQUE (project_id, instrument_id, position)
);

CREATE TABLE IF NOT EXISTS calculated_dependencies (
    project_id            INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    calculated_field_id   INTEGER NOT NULL REFERENCES fields(id) ON DELETE CASCADE,
    ref_unique_event_name TEXT NOT NULL,
    ref_field_name        TEXT NOT NULL,
    PRIMARY KEY (project_id, calculated_field_id, ref_unique_event_name, ref_field_name)
);

CREATE TABLE IF NOT EXISTS data (
    project_id                INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id                 TEXT NOT NULL,
    unique_event_name         TEXT NOT NULL,
    repeating_instrument      TEXT NOT NULL,
    repeating_instance_number INTEGER NOT NULL DEFAULT 1,
    field_name                TEXT NOT NULL,
    value                     TEXT NOT NULL,
    UNIQUE (project_id, record_id, unique_event_name,
            repeating_instrument, repeating_instance_number, field_name)
);
CREATE INDEX IF NOT EXISTS idx_data_project ON data (project_id);
CREATE INDEX IF NOT EXISTS idx_data_record  ON data (record_id);

CREATE TABLE IF NOT EXISTS dag_groups (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS record_entities (
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id    TEXT NOT NULL,
    dag_group_id INTEGER REFERENCES dag_groups(id) ON DELETE SET NULL,
    created_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (project_id, record_id)
);
CREATE INDEX IF NOT EXISTS idx_record_entities_dag ON record_entities (project_id, dag_group_id);

CREATE TABLE IF NOT EXISTS dag_memberships (
    id            INTEGER PRIMARY KEY,
    assignment_id INTEGER NOT NULL REFERENCES user_projects(id) ON DELETE CASCADE,
    group_id      INTEGER NOT NULL REFERENCES dag_groups(id) ON DELETE CASCADE,
    is_active     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (assignment_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_dag_memberships_assignment ON dag_memberships (assignment_id);

CREATE TABLE IF NOT EXISTS anon_offsets (
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id   TEXT NOT NULL,
    offset_days INTEGER NOT NULL,
    PRIMARY KEY (project_id, record_id)
);

CREATE TABLE IF NOT EXISTS survey_links (
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id     TEXT NOT NULL,
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    token         TEXT NOT NULL UNIQUE,
    revoked       INTEGER NOT NULL DEFAULT 0,
    created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (project_id, record_id, instrument_id)
);

CREATE TABLE IF NOT EXISTS languages (
    id           INTEGER PRIMARY KEY,
    code         TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS i18n_strings (
    id          INTEGER PRIMARY KEY,
    language_id INTEGER NOT NULL REFERENCES languages(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    text        TEXT NOT NULL,
    UNIQUE (language_id, key)
);

-- Audit: portable append-only tables (REQ-DB-021/024). Per-year partitioning
-- (MariaDB) / per-year tables + views (SQLite) is a scale optimization applied
-- by the rollover check; the logical schema and append-only contract are the
-- same on both dialects.
CREATE TABLE IF NOT EXISTS audit_events (
    id            INTEGER PRIMARY KEY,
    event_type    TEXT NOT NULL,
    source        TEXT NOT NULL,
    user_id       INTEGER,
    email         TEXT,
    token         TEXT,
    project_id    INTEGER,
    arm_num       INTEGER,
    role          TEXT,
    target_record TEXT,
    details       TEXT,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_project ON audit_events (project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_user    ON audit_events (user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_type    ON audit_events (project_id, event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_record  ON audit_events (project_id, target_record, created_at);

CREATE TABLE IF NOT EXISTS audit_record_views (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER,
    email       TEXT,
    token       TEXT NOT NULL,
    project_id  INTEGER NOT NULL,
    record_ids  TEXT NOT NULL,
    instruments TEXT,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_views_project ON audit_record_views (project_id, created_at);
