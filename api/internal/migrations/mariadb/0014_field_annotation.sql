-- A field's annotation (REQ-DB-046): REDCap's Field Annotation column — action
-- tags such as @READONLY plus free notes, stored as given. Labels and section
-- headers become TEXT (DEV-DB-15): REDCap dictionaries carry labels longer than
-- 255 characters, and MariaDB's default strict mode refuses such a row where
-- SQLite stores it, so the same design would import in development and fail in
-- production.
ALTER TABLE fields
    MODIFY field_label TEXT,
    MODIFY section_header TEXT,
    ADD COLUMN field_annotation TEXT;
