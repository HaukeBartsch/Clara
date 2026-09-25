-- Extensible field validation (REQ-DB-033, REQ-VAL-042/043) and the user-set
-- direct-identifier flag (REQ-EXP-020, DEV-EXP-5). The seed grammars are
-- normative in Data_Validation_Design.md §4.

CREATE TABLE IF NOT EXISTS validation_types (
    name      VARCHAR(64) PRIMARY KEY,   -- designer name; referenced by fields.validation_type
    regex     TEXT NOT NULL,             -- Go RE2 pattern, full-value match (§4.2)
    builtin   INTEGER NOT NULL DEFAULT 0 -- seeded entry; not removable while referenced
);

INSERT INTO validation_types (name, regex, builtin)
SELECT 'email', '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$', 1
WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'email');

INSERT INTO validation_types (name, regex, builtin)
SELECT 'MRN', '^[0-9]{11}$', 1
WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'MRN');

INSERT INTO validation_types (name, regex, builtin)
SELECT 'international phone', '^\+[1-9][0-9 ]{7,14}$', 1
WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'international phone');

INSERT INTO validation_types (name, regex, builtin)
SELECT 'national phone', '^[0-9]{8}$', 1
WHERE NOT EXISTS (SELECT 1 FROM validation_types WHERE name = 'national phone');

-- User-set on any field; the API presets it for email/MRN/phone types
-- (REQ-EXP-020). Versioned migration: applied exactly once.
ALTER TABLE fields ADD COLUMN direct_identifier INTEGER NOT NULL DEFAULT 0;
