-- The unverified completion state (REQ-DB-036, DEV-DB-19): a user-assigned
-- state beside finished, as REDCap's Unverified sits between Incomplete and
-- Complete. instrument_completion stays sparse — a row exists only while the
-- user has assigned one of the two states, and no_data/some_data stay derived
-- from `data` (REQ-API-074). Every existing row is a finished assignment. The
-- API writes only 'finished' or 'unverified'; there is no CHECK.
ALTER TABLE instrument_completion ADD COLUMN IF NOT EXISTS state VARCHAR(16) NOT NULL DEFAULT 'finished' AFTER repeat_instance;
