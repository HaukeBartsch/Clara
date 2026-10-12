-- The project's record naming mode (REQ-DB-045, REQ-API-151). 'auto' names
-- new participants with plain numeric ids and the Record Status Dashboard
-- emits only the auto-create action; 'manual' keeps the typed record_id as
-- the primary path. Existing rows become 'manual', which preserves every
-- project's naming behaviour as it was (the pattern generator REQ-DB-007 is
-- untouched). The value is an allowlist enforced in code, not by CHECK —
-- mirrors projects.mode.
ALTER TABLE projects ADD COLUMN record_naming VARCHAR(16) NOT NULL DEFAULT 'manual';
