package db

import (
	"context"
	"database/sql"
)

// MaxRecordID returns the lexicographically greatest record id that holds at
// least one data value or a record entity, or ("", false) when the project
// has none. The data API derives the next generated name from it
// (REQ-API-023) without loading every id into memory.
func (s *Store) MaxRecordID(ctx context.Context, projectID int64) (string, bool, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT max(record_id) FROM (
			 SELECT record_id FROM data WHERE project_id = ?
			 UNION
			 SELECT record_id FROM record_entities WHERE project_id = ?
		 )`, projectID, projectID).Scan(&v)
	if err != nil {
		return "", false, err
	}
	if !v.Valid {
		return "", false, nil
	}
	return v.String, true, nil
}
