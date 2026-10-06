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
--
-- MariaDB commits DDL implicitly, so unlike the SQLite file this one is not
-- atomic; it is written to run in order and to leave each step valid for the
-- next (the documented exception of Technology_Stack_Design.md §4).

-- --- per-pair grants (REQ-DB-040) ------------------------------------------

CREATE TABLE IF NOT EXISTS role_grants (
    id                INTEGER PRIMARY KEY AUTO_INCREMENT,
    role_id           INTEGER NOT NULL,
    event_id          INTEGER NOT NULL,         -- an absent grant row is the arm default (REQ-AUTH-069)
    instrument_id     INTEGER NOT NULL,
    data_access_level VARCHAR(32) NOT NULL,  -- no_access | read_only | view_edit
    export_level      VARCHAR(32) NOT NULL,  -- export_none | … | export_full
    delete_values     INTEGER NOT NULL DEFAULT 0,  -- right (REQ-AUTH-018)
    edit_surveys      INTEGER NOT NULL DEFAULT 0,  -- right (REQ-AUTH-071)
    UNIQUE KEY uq_role_grants (role_id, event_id, instrument_id),
    CONSTRAINT fk_rg_role       FOREIGN KEY (role_id)       REFERENCES roles(id)       ON DELETE CASCADE,
    CONSTRAINT fk_rg_event      FOREIGN KEY (event_id)      REFERENCES events(id)      ON DELETE CASCADE,
    CONSTRAINT fk_rg_instrument FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE
) ENGINE=InnoDB;

-- An absent row is the arm default, never a permission (REQ-AUTH-069), so
-- nothing is materialized here: new pairs are materialized by the design
-- change that creates them.

-- --- legacy data levels (REQ-DB-009 as revised) ----------------------------

UPDATE role_arms
   SET data_access_level = 'view_edit'
 WHERE data_access_level IN ('delete', 'edit_survey_responses');

-- --- survey links per event (REQ-DB-027/041) -------------------------------

-- collected_at starts NULL for every migrated row: "collected" is this column
-- and not a probe of the stored values (REQ-DB-041), so a survey submitted
-- before this migration reads as not yet collected until its next save stamps
-- it. event_id stays nullable only until the backfill has run; it becomes NOT
-- NULL below, because a link with no event has no key (DEV-DB-14).
ALTER TABLE survey_links
    ADD COLUMN event_id     INTEGER  NULL AFTER instrument_id,
    ADD COLUMN collected_at DATETIME NULL AFTER revoked;

-- An existing link takes the event its instrument is actually mapped to — the
-- lowest-positioned one, so the choice is deterministic. A link with no mapping
-- to follow cannot be given a key and goes with it (DEV-DB-14): it carries no
-- data, and GET …/survey-link issues a fresh token on demand.
UPDATE survey_links sl
   SET sl.event_id = (SELECT ie.event_id
                        FROM instrument_events ie JOIN events me ON me.id = ie.event_id
                       WHERE ie.instrument_id = sl.instrument_id
                       ORDER BY me.position, me.id LIMIT 1)
 WHERE sl.event_id IS NULL;

DELETE FROM survey_links WHERE event_id IS NULL;

ALTER TABLE survey_links
    MODIFY COLUMN event_id INTEGER NOT NULL,
    ADD CONSTRAINT fk_sl_event FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE;

-- The new key is added before the old one goes: the unnamed UNIQUE of 0001 is
-- indexed as `project_id` and carries the project_id foreign key, and the
-- quadruple's leftmost prefix covers it.
ALTER TABLE survey_links
    ADD UNIQUE KEY uq_survey_links_quad (project_id, record_id, instrument_id, event_id);

ALTER TABLE survey_links DROP INDEX project_id;
