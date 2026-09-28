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
    project_id     INTEGER NOT NULL,
    record_id      VARCHAR(255) NOT NULL,
    event_id       INTEGER NOT NULL,
    instrument_id  INTEGER NOT NULL,
    completed_by   INTEGER,
    completed_at   DATETIME NOT NULL,
    PRIMARY KEY (project_id, record_id, event_id, instrument_id),
    CONSTRAINT fk_ic_project   FOREIGN KEY (project_id)    REFERENCES projects(id)    ON DELETE CASCADE,
    CONSTRAINT fk_ic_event     FOREIGN KEY (event_id)      REFERENCES events(id)      ON DELETE CASCADE,
    CONSTRAINT fk_ic_instrument FOREIGN KEY (instrument_id) REFERENCES instruments(id) ON DELETE CASCADE,
    CONSTRAINT fk_ic_user      FOREIGN KEY (completed_by)  REFERENCES users(id)       ON DELETE SET NULL
) ENGINE=InnoDB;
