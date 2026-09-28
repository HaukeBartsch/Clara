package db

import (
	"context"
	"database/sql"
)

// InstrumentCompletion is one user-assigned "finished" assignment of a
// data-collection instrument at a (record, event) — the green state of the
// record-status dashboard (REQ-DB-036, DEV-DB-10). The table is sparse: a row
// exists only while the assignment stands, so clearing it is a delete and
// "no_data vs. some_data" stays derived from `data` (REQ-API-074). Survey
// instruments never get a row (GD-9); the endpoint enforces that.
type InstrumentCompletion struct {
	ProjectID    int64
	RecordID     string
	EventID      int64
	InstrumentID int64
	CompletedBy  sql.NullInt64
	CompletedAt  string // DATETIME UTC "YYYY-MM-DD HH:MM:SS"
}

// SetInstrumentCompletionTx records the finished assignment inside tx, so it
// commits with its audit entry (REQ-AUD-003). Idempotent (REQ-API-042): an
// existing row is left untouched — its completed_by and completed_at keep
// naming the user who first set it — and the return reports false so the
// caller can skip a second audit entry for a no-op.
func (s *Store) SetInstrumentCompletionTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, eventID, instrumentID, completedBy int64) (bool, error) {
	q := `INSERT INTO instrument_completion
			(project_id, record_id, event_id, instrument_id, completed_by, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`
	if s.Dialect == DialectMariaDB {
		// Assigning a column to itself changes nothing, so MariaDB reports 0
		// rows affected for a row that was already finished.
		q += ` ON DUPLICATE KEY UPDATE record_id = record_id`
	} else {
		q += ` ON CONFLICT (project_id, record_id, event_id, instrument_id) DO NOTHING`
	}
	res, err := tx.ExecContext(ctx, q, projectID, recordID, eventID, instrumentID,
		nullInt64(sql.NullInt64{Int64: completedBy, Valid: completedBy > 0}), nowUTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ClearInstrumentCompletionTx removes the finished assignment inside tx,
// returning the cell to its derived state (REQ-API-110). Reports whether a row
// was there — false is the idempotent no-op of clearing an unfinished cell.
func (s *Store) ClearInstrumentCompletionTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, eventID, instrumentID int64) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM instrument_completion
		 WHERE project_id = ? AND record_id = ? AND event_id = ? AND instrument_id = ?`,
		projectID, recordID, eventID, instrumentID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListInstrumentCompletions returns every finished assignment of a project —
// the whole sparse set, which the record-status dashboard joins in memory per
// (record, event, instrument) (REQ-API-074). Served by the primary key's
// project_id prefix (REQ-DB-025).
func (s *Store) ListInstrumentCompletions(ctx context.Context, projectID int64) ([]InstrumentCompletion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id, record_id, event_id, instrument_id, completed_by, completed_at
		 FROM instrument_completion WHERE project_id = ? ORDER BY record_id, event_id, instrument_id`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstrumentCompletion
	for rows.Next() {
		var (
			ic      InstrumentCompletion
			created any
		)
		if err := rows.Scan(&ic.ProjectID, &ic.RecordID, &ic.EventID, &ic.InstrumentID,
			&ic.CompletedBy, &created); err != nil {
			return nil, err
		}
		if v, ok := datetimeString(created); ok {
			ic.CompletedAt = v
		}
		out = append(out, ic)
	}
	return out, rows.Err()
}
