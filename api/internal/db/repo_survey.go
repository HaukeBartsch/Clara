package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Survey links (REQ-DB-027/041, GD-9) in the transactional form §4.17 needs.
// The table holds one row per (project, record, instrument, event), so an
// instrument mapped to three events has three links with three tokens
// (REQ-AUTH-039). A link is good for one submission (REQ-API-145): issuing again
// replaces that quadruple's token rather than adding a second live link, and the
// row's collected_at is both what makes its response "collected" (REQ-DB-041)
// and what marks the link spent.

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

// MarkSurveyLinkCollectedTx stamps the first save through a link inside tx
// (REQ-DB-041): "collected" is this column, so the write happens once and is
// never overwritten. Under one-submission links it is also what consumes the
// link (REQ-API-145), which makes its affected-row count the guard against two
// submissions of one link both storing — the loser reads false. It runs in the
// submission's transaction for the reason that transaction exists: a stored
// response the stamp did not survive would read as not yet collected, and the
// edit-collected-surveys right of REQ-AUTH-071 gates on this column.
// Reports whether this call was the one that stamped it.
func (s *Store) MarkSurveyLinkCollectedTx(ctx context.Context, tx *sql.Tx, linkID int64) (bool, error) {
	res, err := tx.ExecContext(ctx,
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

// InstrumentHasStoredValueTx reports whether the given fields of one instrument
// carry a value for that record at that event (REQ-API-146): a survey is issued
// onto an empty instrument, so re-issuing over collected answers is refused and
// the caller clears them first (REQ-API-036). The caller passes the field names
// with the GD-8 record identifier already excluded — that identifier is a
// stored value of every record, so counting it would refuse every re-issue. An
// empty value deletes its row rather than storing "" (REQ-VAL-024), and the
// `value <> ”` keeps a leftover empty row from blocking an issue.
func (s *Store) InstrumentHasStoredValueTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID, event string, fields []string) (bool, error) {
	if len(fields) == 0 {
		return false, nil
	}
	in := strings.Repeat("?, ", len(fields)-1) + "?"
	args := make([]any, 0, len(fields)+3)
	args = append(args, projectID, recordID, event)
	for _, f := range fields {
		args = append(args, f)
	}
	var one int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM data
		  WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		    AND value <> '' AND field_name IN (`+in+`) LIMIT 1`,
		args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
