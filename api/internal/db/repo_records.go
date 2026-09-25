package db

import "context"

// ListRecordIDs returns the distinct record ids that hold at least one
// data value or a record entity (union of both tables) — the collision
// set generateNextRecordName must avoid (REQ-API-023).
func (s *Store) ListRecordIDs(ctx context.Context, projectID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT record_id FROM data WHERE project_id = ?
		 UNION
		 SELECT record_id FROM record_entities WHERE project_id = ?
		 ORDER BY 1`,
		projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
