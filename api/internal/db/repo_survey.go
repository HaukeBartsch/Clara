package db

import (
	"context"
	"database/sql"
)

// Survey links (REQ-DB-027/041, GD-9) in the transactional form §4.17 needs.
// The table holds one row per (project, record, instrument, event), so an
// instrument mapped to three events has three links with three tokens
// (REQ-AUTH-039) and re-issuing after a revocation replaces that quadruple's
// token rather than adding a second live link — which is also what keeps
// "stable until revoked" true on both sides of a revoke.

// IssueSurveyLinkTokenTx writes a fresh, unrevoked token for the quadruple
// inside tx (REQ-DB-027). An existing row — revoked or not — is overwritten:
// only one link per (project, record, instrument, event) can exist, and a
// caller that asks for a link after a revocation must get a working one. The
// fresh link has collected nothing yet, so collected_at goes back to NULL with
// the old token (REQ-DB-041).
func (s *Store) IssueSurveyLinkTokenTx(ctx context.Context, tx *sql.Tx, sl *SurveyLink) error {
	sl.Token = newToken()
	sl.Revoked = false
	sl.CollectedAt = sql.NullString{}
	sl.CreatedAt = nowUTC()
	createdBy := nullInt64(sl.CreatedBy)

	var err error
	if s.Dialect == DialectMariaDB {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO survey_links (project_id, record_id, instrument_id, event_id, token, revoked, collected_at, created_by, created_at)
			 VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)
			 ON DUPLICATE KEY UPDATE token = VALUES(token), revoked = 0, collected_at = NULL,
				 created_by = VALUES(created_by), created_at = VALUES(created_at)`,
			sl.ProjectID, sl.RecordID, sl.InstrumentID, sl.EventID, sl.Token, createdBy, sl.CreatedAt)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO survey_links (project_id, record_id, instrument_id, event_id, token, revoked, collected_at, created_by, created_at)
			 VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)
			 ON CONFLICT (project_id, record_id, instrument_id, event_id)
			 DO UPDATE SET token = excluded.token, revoked = 0, collected_at = NULL,
				 created_by = excluded.created_by, created_at = excluded.created_at`,
			sl.ProjectID, sl.RecordID, sl.InstrumentID, sl.EventID, sl.Token, createdBy, sl.CreatedAt)
	}
	return err
}

// RevokeSurveyLinkTx marks the quadruple's link revoked inside tx and reports
// whether a live link was there to revoke — revoking an already-revoked link is
// the idempotent no-op of REQ-API-042 and writes no audit entry. Revoking one
// event's link leaves the same instrument's links at other events valid
// (REQ-API-085).
func (s *Store) RevokeSurveyLinkTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, instrumentID, eventID int64) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE survey_links SET revoked = 1
		 WHERE project_id = ? AND record_id = ? AND instrument_id = ? AND event_id = ? AND revoked = 0`,
		projectID, recordID, instrumentID, eventID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkSurveyLinkCollected stamps the first save through a link (REQ-DB-041):
// "collected" is this column, so the write happens once and is never
// overwritten — a later save through the same link changes nothing. Reports
// whether this call was the one that stamped it.
func (s *Store) MarkSurveyLinkCollected(ctx context.Context, linkID int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE survey_links SET collected_at = ? WHERE id = ? AND collected_at IS NULL`,
		nowUTC(), linkID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
