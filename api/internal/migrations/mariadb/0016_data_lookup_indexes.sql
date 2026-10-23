DROP INDEX idx_data_project ON data;
DROP INDEX idx_data_record ON data;

CREATE INDEX idx_data_project_field ON data (project_id, field_name);
CREATE INDEX idx_data_project_event ON data (project_id, unique_event_name);
