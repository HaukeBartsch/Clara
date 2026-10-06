-- Role permissions move from per-arm levels to an arm default with a
-- per-(instrument, event) override (GD-2 revised 2026-10-03, REQ-DB-040,
-- REQ-AUTH-069/070), and survey links gain the event dimension they were
-- missing: an instrument mapped to three events now has three distinct links
-- (REQ-DB-041, REQ-AUTH-039).
--
-- Two consequences worth naming:
--   * The data-access ladder ends at view_edit. The rights it used to carry —
--     delete and edit collected surveys — become the two boolean columns of
--     role_grants and are implied by no level (REQ-AUTH-070), so the legacy
--     arm levels below are normalized rather than ranked: `delete` and
--     `edit_survey_responses` keep the editing half of what they granted and
--     lose the rest, which an administrator then grants explicitly.
--   * A survey link is keyed by a (record, instrument, event) triple and the
--     event is required: a project without events holds no links and no pair
--     rights, since both address a pair that does not exist there (DEV-DB-14).
--     Existing links are therefore re-keyed onto the event their instrument is
--     mapped to, and one whose instrument maps to nothing is dropped — it has no
--     key, and a link carries no data: GET …/survey-link issues a fresh token.

-- --- per-pair grants (REQ-DB-040) ------------------------------------------

CREATE TABLE IF NOT EXISTS role_grants (
    id                INTEGER PRIMARY KEY,
    role_id           INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    event_id          INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
                                                           -- an absent row is the arm default (REQ-AUTH-069)
    instrument_id     INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    data_access_level VARCHAR(32) NOT NULL,  -- no_access | read_only | view_edit
    export_level      VARCHAR(32) NOT NULL,  -- export_none | … | export_full
    delete_values     INTEGER NOT NULL DEFAULT 0,  -- right (REQ-AUTH-018)
    edit_surveys      INTEGER NOT NULL DEFAULT 0,  -- right (REQ-AUTH-071)
    UNIQUE (role_id, event_id, instrument_id)
);

-- An absent row is the arm default, never a permission (REQ-AUTH-069), so
-- nothing is materialized here: new pairs are materialized by the design
-- change that creates them.

-- --- legacy data levels (REQ-DB-009 as revised) ----------------------------

UPDATE role_arms
   SET data_access_level = 'view_edit'
 WHERE data_access_level IN ('delete', 'edit_survey_responses');

-- --- survey links per event (REQ-DB-027/041) -------------------------------

-- The uniqueness of a link changes, which SQLite can only do by rebuilding the
-- table. collected_at starts NULL for every migrated row: "collected" is this
-- column and not a probe of the stored values (REQ-DB-041), so a survey
-- submitted before this migration reads as not yet collected until its next
-- save stamps it.
CREATE TABLE survey_links_new (
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id     TEXT NOT NULL,
    instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
                                                           -- the event is required: no events, no links (DEV-DB-14)
    token         TEXT NOT NULL UNIQUE,
    revoked       INTEGER NOT NULL DEFAULT 0,
    collected_at  TEXT,                               -- first save through the link
    created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    TEXT NOT NULL,
    UNIQUE (project_id, record_id, instrument_id, event_id)
);

-- An existing link takes the event its instrument is actually mapped to — the
-- lowest-positioned one, so the choice is deterministic. A link with no mapping
-- to follow cannot be given a key and goes with it (DEV-DB-14).
INSERT INTO survey_links_new
    (id, project_id, record_id, instrument_id, event_id, token, revoked,
     collected_at, created_by, created_at)
SELECT sl.id, sl.project_id, sl.record_id, sl.instrument_id,
       (SELECT ie.event_id
          FROM instrument_events ie JOIN events me ON me.id = ie.event_id
         WHERE ie.instrument_id = sl.instrument_id
         ORDER BY me.position, me.id LIMIT 1),
       sl.token, sl.revoked, NULL, sl.created_by, sl.created_at
  FROM survey_links sl
 WHERE EXISTS (SELECT 1 FROM instrument_events ie2
                WHERE ie2.instrument_id = sl.instrument_id);

DROP TABLE survey_links;
ALTER TABLE survey_links_new RENAME TO survey_links;
