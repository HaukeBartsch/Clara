-- System-wide runtime settings registry (REQ-DB-037, DEV-DB-11; master
-- spec "Rate limitter"). Values are JSON scalars interpreted and validated
-- at the API boundary (REQ-API-112); adding a further runtime setting is an
-- insert here plus its validation, not a schema change (mirrors REQ-DB-031).

CREATE TABLE IF NOT EXISTS system_settings (
    key   VARCHAR(64) PRIMARY KEY,   -- dotted setting name
    value TEXT NOT NULL              -- JSON scalar
) ENGINE=InnoDB;

INSERT INTO system_settings (key, value)
SELECT 'rate_limit_enabled', 'false'
WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'rate_limit_enabled');

INSERT INTO system_settings (key, value)
SELECT 'rate_limit_rpm', '600'
WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'rate_limit_rpm');
