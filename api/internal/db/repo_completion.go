package db

import (
	"context"
	"database/sql"
	"strings"
)

// The two user-assigned completion states a row can hold (REQ-DB-036,
// DEV-DB-19): finished — the green state of the record-status dashboard — and
// unverified, the yellow one between some data and finished (REDCap's
// Unverified).
const (
	CompletionFinished   = "finished"
	CompletionUnverified = "unverified"
)

// InstrumentCompletion is one user-assigned completion of a data-collection
// instrument at a (record, event, instance) — finished or unverified
// (REQ-DB-036, DEV-DB-10, DEV-DB-19). Instance is 1 for every non-repeating
// form and counts the instances of a repeating one (REQ-DB-048). The table is
// sparse: a row exists only while the assignment stands, so clearing it is a
// delete and "no_data vs. some_data" stays derived from `data` (REQ-API-074).
// Survey instruments never get a row (GD-9); the endpoint enforces that.
type InstrumentCompletion struct {
	ProjectID    int64
	RecordID     string
	EventID      int64
	InstrumentID int64
	Instance     int
	State        string // CompletionFinished or CompletionUnverified
	CompletedBy  sql.NullInt64
	CompletedAt  string // DATETIME UTC "YYYY-MM-DD HH:MM:SS"
}

// SetInstrumentCompletionTx records the assignment of state (finished or
// unverified) inside tx, so it commits with its audit entry (REQ-AUD-003).
// Idempotent (REQ-API-042): a row already holding state is left untouched —
// its completed_by and completed_at keep naming the user who first set it —
// and the return reports false so the caller can skip a second audit entry for
// a no-op. A row holding the other state takes the new one, naming the user
// and the time of the change.
func (s *Store) SetInstrumentCompletionTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, eventID, instrumentID int64, instance int, state string, completedBy int64) (bool, error) {
	q := `INSERT INTO instrument_completion
			(project_id, record_id, event_id, instrument_id, repeat_instance, state, completed_by, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if s.Dialect == DialectMariaDB {
		// MariaDB assigns left to right, so state changes last; assigning the
		// stored values back changes nothing, and MariaDB then reports 0 rows
		// affected for a row that already held state.
		q += ` ON DUPLICATE KEY UPDATE
			completed_by = IF(state = VALUES(state), completed_by, VALUES(completed_by)),
			completed_at = IF(state = VALUES(state), completed_at, VALUES(completed_at)),
			state = VALUES(state)`
	} else {
		q += ` ON CONFLICT (project_id, record_id, event_id, instrument_id, repeat_instance) DO UPDATE
			SET state = excluded.state, completed_by = excluded.completed_by, completed_at = excluded.completed_at
			WHERE instrument_completion.state <> excluded.state`
	}
	res, err := tx.ExecContext(ctx, q, projectID, recordID, eventID, instrumentID, max(instance, 1), state,
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

// ClearInstrumentCompletionTx removes the assignment inside tx,
// returning the cell to its derived state (REQ-API-110). Reports whether a row
// was there — false is the idempotent no-op of clearing an unassigned cell.
func (s *Store) ClearInstrumentCompletionTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, eventID, instrumentID int64, instance int) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM instrument_completion
		 WHERE project_id = ? AND record_id = ? AND event_id = ? AND instrument_id = ? AND repeat_instance = ?`,
		projectID, recordID, eventID, instrumentID, max(instance, 1))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListInstrumentCompletions returns every assignment of a project —
// the whole sparse set, which the record export joins in memory per
// (record, event, instrument) (REQ-API-134). Served by the primary key's
// project_id prefix (REQ-DB-025).
func (s *Store) ListInstrumentCompletions(ctx context.Context, projectID int64) ([]InstrumentCompletion, error) {
	return s.listInstrumentCompletions(ctx, `project_id = ?`, projectID)
}

// ListInstrumentCompletionsOf returns the assignments of the named
// records only — one chunk of the record-status dashboard's streamed read
// (REQ-API-172). Served by the primary key's (project_id, record_id) prefix;
// no records, no query.
func (s *Store) ListInstrumentCompletionsOf(ctx context.Context, projectID int64, recordIDs []string) ([]InstrumentCompletion, error) {
	if len(recordIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(recordIDs)+1)
	args = append(args, projectID)
	for _, id := range recordIDs {
		args = append(args, id)
	}
	return s.listInstrumentCompletions(ctx,
		`project_id = ? AND record_id IN (?`+strings.Repeat(", ?", len(recordIDs)-1)+`)`, args...)
}

func (s *Store) listInstrumentCompletions(ctx context.Context, where string, args ...any) ([]InstrumentCompletion, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id, record_id, event_id, instrument_id, repeat_instance, state, completed_by, completed_at
		 FROM instrument_completion WHERE `+where+`
		 ORDER BY record_id, event_id, instrument_id, repeat_instance`,
		args...)
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
			&ic.Instance, &ic.State, &ic.CompletedBy, &created); err != nil {
			return nil, err
		}
		if v, ok := datetimeString(created); ok {
			ic.CompletedAt = v
		}
		out = append(out, ic)
	}
	return out, rows.Err()
}
