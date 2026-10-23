DROP INDEX IF EXISTS idx_data_project;
DROP INDEX IF EXISTS idx_data_record;

CREATE INDEX idx_data_project_field ON data (project_id, field_name);
CREATE INDEX idx_data_project_event ON data (project_id, unique_event_name);
