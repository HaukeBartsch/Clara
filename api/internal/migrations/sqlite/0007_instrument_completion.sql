-- User-assigned instrument completion (REQ-DB-036, DEV-DB-10): the third
-- state of the record-status dashboard. Sparse on purpose — a row exists only
-- while the user has marked a (record, event, instrument) finished, so
-- absence means "not finished" and the no_data/some_data split stays derived
-- from `data`. A stored flag can therefore never contradict the values
-- (REQ-API-074). Survey-marked instruments never get a row (GD-9).
--
-- The primary key doubles as the record-status read path (project_id,
-- record_id prefix, REQ-DB-025); the cascades drop a project's assignments
-- with its records, instruments and events.

CREATE TABLE IF NOT EXISTS instrument_completion (
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    record_id      VARCHAR(255) NOT NULL,
    event_id       INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    instrument_id  INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    completed_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    completed_at   DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id, event_id, instrument_id)
);
