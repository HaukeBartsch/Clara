-- Blockout period for an over-budget source IP (REQ-API-113; master spec
-- "Rate limitter"): exceeding rate_limit_rpm blocks that IP for this many
-- minutes, after which its requests are accepted again. A further runtime
-- setting is an insert into system_settings plus its validation at the API
-- boundary, not a schema change (REQ-DB-037).

INSERT INTO system_settings (`key`, value)
SELECT 'rate_limit_block_minutes', '10'
WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE `key` = 'rate_limit_block_minutes');
