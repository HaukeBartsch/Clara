-- Repeating instruments (REQ-DB-047, REQ-DB-048, DEV-DB-16).
--
-- 1. A mapped (instrument, event) pair may be designated repeating: the flag
--    lives on the pair itself, so unmapping the pair drops the designation with
--    it and no designation can name a pair that does not exist.
ALTER TABLE instrument_events ADD COLUMN is_repeating INTEGER NOT NULL DEFAULT 0;

-- 2. The first instance of every form is (repeating_instrument '', instance 1)
--    (REQ-DB-019). The data API stored its values at instance 0 until now while
--    the design-time recomputation wrote instance 1, so one calculated field could
--    hold a row at each. The instance-0 row is the one the data API wrote last and
--    the one every read resolved, so it is kept; the instance-1 twin goes.
DELETE FROM data
 WHERE repeating_instrument = '' AND repeating_instance_number = 1
   AND EXISTS (SELECT 1 FROM data d0
                WHERE d0.project_id = data.project_id
                  AND d0.record_id = data.record_id
                  AND d0.unique_event_name = data.unique_event_name
                  AND d0.repeating_instrument = ''
                  AND d0.repeating_instance_number = 0
                  AND d0.field_name = data.field_name);
UPDATE data SET repeating_instance_number = 1
 WHERE repeating_instrument = '' AND repeating_instance_number = 0;

-- 3. The finished assignment is held per instance (REQ-DB-036): a further
--    instance of a repeating instrument is finished on its own. SQLite cannot
--    widen a primary key in place, so the table is rebuilt; every existing row
--    is an assignment of instance 1.
CREATE TABLE instrument_completion_next (
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id       VARCHAR(255) NOT NULL,
    event_id        INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    instrument_id   INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    repeat_instance INTEGER NOT NULL DEFAULT 1,
    completed_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    completed_at    DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id, event_id, instrument_id, repeat_instance)
);
INSERT INTO instrument_completion_next
    (project_id, record_id, event_id, instrument_id, repeat_instance, completed_by, completed_at)
SELECT project_id, record_id, event_id, instrument_id, 1, completed_by, completed_at
  FROM instrument_completion;
DROP TABLE instrument_completion;
ALTER TABLE instrument_completion_next RENAME TO instrument_completion;
