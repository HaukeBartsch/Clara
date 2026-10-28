-- The lookup indexes of `data` (REQ-DB-017): (project_id, field_name) and
-- (project_id, unique_event_name) replace the single-column ones.
--
-- Order matters on MariaDB. The unique key of `data` spans four VARCHAR(255)
-- utf8mb4 columns, past InnoDB's 3072-byte key limit, so MariaDB keeps it as a
-- HASH index, which cannot back fk_data_project. idx_data_project is the
-- foreign key's index until another index starts with project_id: the new
-- indexes are created first, and only then is it dropped — the other way round
-- MariaDB refuses the drop (errno 150).
--
-- MariaDB commits each DDL statement on its own, so a run that stops halfway is
-- not rolled back; IF [NOT] EXISTS lets the next start finish it.
CREATE INDEX IF NOT EXISTS idx_data_project_field ON data (project_id, field_name);
CREATE INDEX IF NOT EXISTS idx_data_project_event ON data (project_id, unique_event_name);

DROP INDEX IF EXISTS idx_data_project ON data;
DROP INDEX IF EXISTS idx_data_record ON data;
