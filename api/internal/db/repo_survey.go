package db

import (
	"context"
	"database/sql"
)

// Survey links (REQ-DB-027, GD-9) in the transactional form §4.17 needs. The
// table holds one row per (project, record, instrument), so re-issuing after a
// revocation replaces that row's token rather than adding a second live link —
// which is also what keeps "stable until revoked" true on both sides of a
// revoke.

// IssueSurveyLinkTokenTx writes a fresh, unrevoked token for the triple inside
// tx (REQ-DB-027). An existing row — revoked or not — is overwritten: only one
// link per (project, record, instrument) can exist, and a caller that asks for
// a link after a revocation must get a working one.
func (s *Store) IssueSurveyLinkTokenTx(ctx context.Context, tx *sql.Tx, sl *SurveyLink) error {
	sl.Token = newToken()
	sl.Revoked = false
	sl.CreatedAt = nowUTC()
	createdBy := nullInt64(sl.CreatedBy)
	var err error
	if s.Dialect == DialectMariaDB {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO survey_links (project_id, record_id, instrument_id, token, revoked, created_by, created_at)
			 VALUES (?, ?, ?, ?, 0, ?, ?)
			 ON DUPLICATE KEY UPDATE token = VALUES(token), revoked = 0,
				 created_by = VALUES(created_by), created_at = VALUES(created_at)`,
			sl.ProjectID, sl.RecordID, sl.InstrumentID, sl.Token, createdBy, sl.CreatedAt)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO survey_links (project_id, record_id, instrument_id, token, revoked, created_by, created_at)
			 VALUES (?, ?, ?, ?, 0, ?, ?)
			 ON CONFLICT (project_id, record_id, instrument_id)
			 DO UPDATE SET token = excluded.token, revoked = 0,
				 created_by = excluded.created_by, created_at = excluded.created_at`,
			sl.ProjectID, sl.RecordID, sl.InstrumentID, sl.Token, createdBy, sl.CreatedAt)
	}
	return err
}

// RevokeSurveyLinkTx marks the triple's link revoked inside tx and reports
// whether a live link was there to revoke — revoking an already-revoked link is
// the idempotent no-op of REQ-API-042 and writes no audit entry.
func (s *Store) RevokeSurveyLinkTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, instrumentID int64) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE survey_links SET revoked = 1
		 WHERE project_id = ? AND record_id = ? AND instrument_id = ? AND revoked = 0`,
		projectID, recordID, instrumentID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
