package db

import (
	"context"
	"time"
)

// Starred projects (REQ-DB-056, migration 0023): the projects a user marked on the
// project overview (REQ-UI-087). A star is presentation only — no authorization path
// reads it — so these functions take ids and leave visibility to the caller.

// SetProjectStar stars (on = true) or unstars a project for a user. Both directions are
// idempotent: starring a starred project keeps its first created_at, and unstarring a
// project without a star deletes nothing. Two tabs racing to star the same project both
// succeed, because the loser's row already says what it wanted.
func (s *Store) SetProjectStar(ctx context.Context, userID, projectID int64, on bool, now time.Time) error {
	if !on {
		_, err := s.DB.ExecContext(ctx,
			`DELETE FROM project_stars WHERE user_id = ? AND project_id = ?`, userID, projectID)
		return err
	}
	ts := now.UTC().Format(datetimeLayout)
	if s.Dialect == DialectMariaDB {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO project_stars (user_id, project_id, created_at)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE created_at = created_at`, userID, projectID, ts)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO project_stars (user_id, project_id, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT (user_id, project_id) DO NOTHING`, userID, projectID, ts)
	return err
}

// StarredProjectIDs returns the set of project ids the user starred, read once for a
// whole project list (REQ-API-049) rather than once per project.
func (s *Store) StarredProjectIDs(ctx context.Context, userID int64) (map[int64]bool, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id FROM project_stars WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
