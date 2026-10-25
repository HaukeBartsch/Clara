-- Project recency for the project overview (REQ-UI-071, REQ-API-049): the overview asks for
-- the acting user's newest audit event per project. idx_audit_events_user is ordered by
-- created_at within a user, so grouping it by project reads that user's whole history; this
-- index is ordered by project first and answers
-- `SELECT project_id, MAX(created_at) … WHERE user_id = ? GROUP BY project_id` from itself —
-- as a local index, so each yearly RANGE partition of §6.1 contributes its own descent. The
-- audit writer provisions SQLite's per-year tables from the same list (createEventsObjects).
CREATE INDEX idx_audit_events_user_project ON audit_events (user_id, project_id, created_at);
