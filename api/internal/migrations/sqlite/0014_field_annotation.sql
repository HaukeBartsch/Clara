-- A field's annotation (REQ-DB-046): REDCap's Field Annotation column — action
-- tags such as @READONLY plus free notes, stored as given. Labels and section
-- headers need no change here: SQLite does not enforce a VARCHAR length, so
-- the 255-character declaration of 0001 never cut a value (DEV-DB-15).
ALTER TABLE fields ADD COLUMN field_annotation TEXT;
