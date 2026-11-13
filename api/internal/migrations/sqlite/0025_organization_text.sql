-- A project's organization is unbounded text (DEV-DB-20). Nothing changes here:
-- 0001 already declares the column TEXT, and SQLite does not enforce a VARCHAR
-- length anyway. The file keeps the dialects in step; the MariaDB file widens
-- its VARCHAR(16) to the same unbounded text.
SELECT 1;
