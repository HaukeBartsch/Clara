-- A project's organization is unbounded text (DEV-DB-20). The list of REQ-DB-006 is
-- what the interface offers, not what the API accepts: a value outside it is stored as
-- given and stays valid. VARCHAR(16) made that true on SQLite only — MariaDB's default
-- strict mode refused a longer value with an internal error where SQLite stored it, so
-- the same request created a project in development and failed in production.
ALTER TABLE projects MODIFY organization TEXT NOT NULL;
