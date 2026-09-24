-- CLARA data store — MariaDB rendering (Database_Schema_Design.md, GD-6).
-- Dialect: MariaDB 11.x (InnoDB). AUTO_INCREMENT ids; VARCHAR/DATE/DATETIME;
-- LONGTEXT for EAV values and i18n text.
--
-- Note: this file is applied with multiStatements=true. Foreign keys are
-- declared inline; referenced tables are created before they are referenced.

CREATE TABLE IF NOT EXISTS projects (
    id                  INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_name        VARCHAR(255) NOT NULL UNIQUE,
    organization        VARCHAR(16)  NOT NULL,
    pi_name             VARCHAR(255) NOT NULL,
    pi_email            VARCHAR(254) NOT NULL,
    dm_name             VARCHAR(255),
    dm_email            VARCHAR(254),
    rek_number          VARCHAR(255),
    rek_start_date      DATE,
    rek_end_date        DATE,
    start_date          DATE,
    end_date            DATE,
    participant_names   VARCHAR(255) NOT NULL,
    creation_time       DATETIME NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    email         VARCHAR(254) NOT NULL UNIQUE,
    display_name  VARCHAR(255) NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    auth_source   VARCHAR(8)  NOT NULL,
    password_hash VARCHAR(255),
    valid_until   DATE,
    last_login_at DATETIME,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    ui_language   VARCHAR(8)  NOT NULL DEFAULT 'en',
    created_at    DATETIME NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS roles (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id    INTEGER NOT NULL,
    role_name     VARCHAR(255) NOT NULL,
    project_admin INTEGER NOT NULL DEFAULT 0,
    UNIQUE (project_id, role_name),
    CONSTRAINT fk_roles_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS role_arms (
    id                INTEGER PRIMARY KEY AUTO_INCREMENT,
    role_id           INTEGER NOT NULL,
    arm_num           INTEGER NOT NULL,
    data_access_level VARCHAR(32) NOT NULL,
    export_level      VARCHAR(32) NOT NULL,
    UNIQUE (role_id, arm_num),
    CONSTRAINT fk_role_arms_role FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS user_projects (
    id         INTEGER PRIMARY KEY AUTO_INCREMENT,
    user_id    INTEGER NOT NULL,
    project_id INTEGER NOT NULL,
    role_id    INTEGER,
    token      CHAR(36) NOT NULL UNIQUE,
    created_at DATETIME NOT NULL,
    UNIQUE (user_id, project_id),
    CONSTRAINT fk_up_user    FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE,
    CONSTRAINT fk_up_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_up_role    FOREIGN KEY (role_id)    REFERENCES roles(id)    ON DELETE SET NULL
) ENGINE=InnoDB;
CREATE INDEX idx_user_projects_user ON user_projects (user_id);

CREATE TABLE IF NOT EXISTS arms (
    id         INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id INTEGER NOT NULL,
    arm_num    INTEGER NOT NULL,
    name       VARCHAR(255),
    position   INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, arm_num),
    CONSTRAINT fk_arms_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS events (
    id                INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id        INTEGER NOT NULL,
    arm_id            INTEGER NOT NULL,
    event_name        VARCHAR(255) NOT NULL,
    unique_event_name VARCHAR(255) NOT NULL,
    period            INTEGER,
    safe_region_start INTEGER,
    safe_region_end   INTEGER,
    position          INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, unique_event_name),
    UNIQUE (project_id, arm_id, event_name),
    CONSTRAINT fk_events_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_events_arm     FOREIGN KEY (arm_id)     REFERENCES arms(id)     ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS instruments (
    id              INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id      INTEGER NOT NULL,
    name            VARCHAR(255) NOT NULL,
    position        INTEGER NOT NULL DEFAULT 1,
    is_survey       INTEGER NOT NULL DEFAULT 0,
    branching_logic TEXT,
    UNIQUE (project_id, name),
    CONSTRAINT fk_instruments_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS instrument_events (
    instrument_id INTEGER NOT NULL,
    event_id      INTEGER NOT NULL,
    PRIMARY KEY (instrument_id, event_id),
    CONSTRAINT fk_ie_instrument FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE,
    CONSTRAINT fk_ie_event      FOREIGN KEY (event_id)      REFERENCES events(id)      ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS fields (
    id                   INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id           INTEGER NOT NULL,
    instrument_id        INTEGER NOT NULL,
    field_name           VARCHAR(255) NOT NULL,
    field_label          VARCHAR(255),
    field_type           VARCHAR(16) NOT NULL,
    section_header       VARCHAR(255),
    choices              TEXT,
    field_note           TEXT,
    validation_type      VARCHAR(32),
    validation_format    VARCHAR(32),
    validation_min       VARCHAR(255),
    validation_max       VARCHAR(255),
    required             INTEGER NOT NULL DEFAULT 0,
    branching_logic      TEXT,
    calculation          TEXT,
    matrix_group         VARCHAR(255),
    personal_information INTEGER NOT NULL DEFAULT 0,
    export_approved      INTEGER NOT NULL DEFAULT 0,
    position             INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, field_name),
    UNIQUE (project_id, instrument_id, position),
    CONSTRAINT fk_fields_project    FOREIGN KEY (project_id)    REFERENCES projects(id)    ON DELETE CASCADE,
    CONSTRAINT fk_fields_instrument FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS calculated_dependencies (
    project_id            INTEGER NOT NULL,
    calculated_field_id   INTEGER NOT NULL,
    ref_unique_event_name VARCHAR(255) NOT NULL,
    ref_field_name        VARCHAR(255) NOT NULL,
    PRIMARY KEY (project_id, calculated_field_id, ref_unique_event_name, ref_field_name),
    CONSTRAINT fk_calcdep_project REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_calcdep_field  REFERENCES fields(id)    ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS data (
    project_id                INTEGER NOT NULL,
    record_id                 VARCHAR(255) NOT NULL,
    unique_event_name         VARCHAR(255) NOT NULL,
    repeating_instrument      VARCHAR(255) NOT NULL,
    repeating_instance_number INTEGER NOT NULL DEFAULT 1,
    field_name                VARCHAR(255) NOT NULL,
    value                     LONGTEXT NOT NULL,
    UNIQUE (project_id, record_id, unique_event_name,
            repeating_instrument, repeating_instance_number, field_name),
    CONSTRAINT fk_data_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE INDEX idx_data_project ON data (project_id);
CREATE INDEX idx_data_record  ON data (record_id);

CREATE TABLE IF NOT EXISTS dag_groups (
    id         INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id INTEGER NOT NULL,
    name       VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (project_id, name),
    CONSTRAINT fk_dag_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS record_entities (
    project_id   INTEGER NOT NULL,
    record_id    VARCHAR(255) NOT NULL,
    dag_group_id INTEGER,
    created_by   INTEGER,
    created_at   DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id),
    CONSTRAINT fk_re_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_re_dag     FOREIGN KEY (dag_group_id) REFERENCES dag_groups(id) ON DELETE SET NULL,
    CONSTRAINT fk_re_user    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB;
CREATE INDEX idx_record_entities_dag ON record_entities (project_id, dag_group_id);

CREATE TABLE IF NOT EXISTS dag_memberships (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    assignment_id INTEGER NOT NULL,
    group_id      INTEGER NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (assignment_id, group_id),
    CONSTRAINT fk_dm_assignment FOREIGN KEY (assignment_id) REFERENCES user_projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_dm_group      FOREIGN KEY (group_id)      REFERENCES dag_groups(id)    ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE INDEX idx_dag_memberships_assignment ON dag_memberships (assignment_id);

CREATE TABLE IF NOT EXISTS anon_offsets (
    project_id  INTEGER NOT NULL,
    record_id   VARCHAR(255) NOT NULL,
    offset_days INTEGER NOT NULL,
    PRIMARY KEY (project_id, record_id),
    CONSTRAINT fk_anon_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS survey_links (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    project_id    INTEGER NOT NULL,
    record_id     VARCHAR(255) NOT NULL,
    instrument_id INTEGER NOT NULL,
    token         CHAR(36) NOT NULL UNIQUE,
    revoked       INTEGER NOT NULL DEFAULT 0,
    created_by    INTEGER,
    created_at    DATETIME NOT NULL,
    UNIQUE (project_id, record_id, instrument_id),
    CONSTRAINT fk_sl_project    FOREIGN KEY (project_id)    REFERENCES projects(id)    ON DELETE CASCADE,
    CONSTRAINT fk_sl_instrument FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE,
    CONSTRAINT fk_sl_user       FOREIGN KEY (created_by)    REFERENCES users(id)       ON DELETE SET NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS languages (
    id           INTEGER PRIMARY KEY AUTO_INCREMENT,
    code         VARCHAR(8)  NOT NULL UNIQUE,
    display_name VARCHAR(255) NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS i18n_strings (
    id          INTEGER PRIMARY KEY AUTO_INCREMENT,
    language_id INTEGER NOT NULL,
    key         VARCHAR(255) NOT NULL,
    text        LONGTEXT NOT NULL,
    UNIQUE (language_id, key),
    CONSTRAINT fk_i18n_language FOREIGN KEY (language_id) REFERENCES languages(id) ON DELETE CASCADE
) ENGINE=InnoDB;

-- Audit: portable append-only tables (REQ-DB-021/024). The application account
-- holds INSERT, SELECT only (REQ-DB-024); per-year PARTITION BY RANGE is a
-- scale optimization applied by the rollover check.
CREATE TABLE IF NOT EXISTS audit_events (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    event_type    VARCHAR(64)  NOT NULL,
    source        VARCHAR(8)   NOT NULL,
    user_id       INTEGER,
    email         VARCHAR(254),
    token         CHAR(36),
    project_id    INTEGER,
    arm_num       INTEGER,
    role          VARCHAR(255),
    target_record VARCHAR(255),
    details       TEXT,
    created_at    DATETIME NOT NULL,
    CONSTRAINT fk_audit_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB;
CREATE INDEX idx_audit_events_project ON audit_events (project_id, created_at);
CREATE INDEX idx_audit_events_user    ON audit_events (user_id, created_at);
CREATE INDEX idx_audit_events_type    ON audit_events (project_id, event_type, created_at);
CREATE INDEX idx_audit_events_record  ON audit_events (project_id, target_record, created_at);

CREATE TABLE IF NOT EXISTS audit_record_views (
    id          INTEGER PRIMARY KEY AUTO_INCREMENT,
    user_id     INTEGER,
    email       VARCHAR(254),
    token       CHAR(36) NOT NULL,
    project_id  INTEGER NOT NULL,
    record_ids  TEXT NOT NULL,
    instruments TEXT,
    created_at  DATETIME NOT NULL,
    CONSTRAINT fk_views_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB;
CREATE INDEX idx_audit_views_project ON audit_record_views (project_id, created_at);