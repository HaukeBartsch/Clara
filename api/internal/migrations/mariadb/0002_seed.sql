-- Seed: enabled languages (GD-12, REQ-DB-031). English is the default/fallback;
-- Bokmål and Nynorsk are the first non-English languages.
INSERT INTO languages (code, display_name, enabled)
SELECT 'en', 'English', 1 FROM dual WHERE NOT EXISTS (SELECT 1 FROM languages WHERE code = 'en');

INSERT INTO languages (code, display_name, enabled)
SELECT 'nb', 'Norsk bokmål', 1 FROM dual WHERE NOT EXISTS (SELECT 1 FROM languages WHERE code = 'nb');

INSERT INTO languages (code, display_name, enabled)
SELECT 'nn', 'Norsk nynorsk', 1 FROM dual WHERE NOT EXISTS (SELECT 1 FROM languages WHERE code = 'nn');
