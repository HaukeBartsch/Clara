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
DELETE d1 FROM data d1
  JOIN data d0
    ON d0.project_id = d1.project_id
   AND d0.record_id = d1.record_id
   AND d0.unique_event_name = d1.unique_event_name
   AND d0.repeating_instrument = ''
   AND d0.repeating_instance_number = 0
   AND d0.field_name = d1.field_name
 WHERE d1.repeating_instrument = '' AND d1.repeating_instance_number = 1;
UPDATE data SET repeating_instance_number = 1
 WHERE repeating_instrument = '' AND repeating_instance_number = 0;

-- 3. The finished assignment is held per instance (REQ-DB-036): a further
--    instance of a repeating instrument is finished on its own. Every existing
--    row is an assignment of instance 1.
ALTER TABLE instrument_completion
    ADD COLUMN repeat_instance INTEGER NOT NULL DEFAULT 1 AFTER instrument_id,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (project_id, record_id, event_id, instrument_id, repeat_instance);
