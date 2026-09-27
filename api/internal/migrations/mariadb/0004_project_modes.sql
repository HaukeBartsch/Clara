-- Project modes and staged setup changes (GD-20, REQ-DB-034/035). A project
-- holds exactly one mode; the value is an API allowlist
-- (development | production | analysis) enforced in code, not by CHECK, so
-- the enum can grow without a schema change. Existing rows become
-- development — the mode that imposes no new behaviour. MODE is a
-- non-reserved keyword in MariaDB and needs no quoting.
ALTER TABLE projects ADD COLUMN mode VARCHAR(16) NOT NULL DEFAULT 'development';

-- At most one OPEN staging set per project (primary key on project_id): the
-- row exists only while staging is open in production mode (GD-20, REQ-DB-035).
-- Commit applies the snapshot to the live structure tables in one transaction
-- and deletes the row; discard only deletes the row. Data collection never
-- reads this table (REQ-API-107), so its presence costs a lookup on the setup
-- paths only.
CREATE TABLE IF NOT EXISTS project_staging (
    project_id  INTEGER PRIMARY KEY,
    design      LONGTEXT NOT NULL,   -- staged-design snapshot (JSON): instruments, arms/events, mapping
    opened_by   INTEGER,
    opened_at   DATETIME NOT NULL,
    CONSTRAINT fk_staging_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_staging_user    FOREIGN KEY (opened_by)  REFERENCES users(id)    ON DELETE SET NULL
) ENGINE=InnoDB;
