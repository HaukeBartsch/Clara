package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math/big"
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

// RecordExists reports whether a record id is present in the project: an
// identity row or at least one stored value (REQ-DB-029 — a record is
// persisted on first import, REQ-API-033). Same two sources as MaxRecordID,
// without loading either id list.
func (s *Store) RecordExists(ctx context.Context, projectID int64, recordID string) (bool, error) {
	var found int
	err := s.DB.QueryRowContext(ctx,
		`SELECT CASE WHEN EXISTS (SELECT 1 FROM data WHERE project_id = ? AND record_id = ?)
		              OR EXISTS (SELECT 1 FROM record_entities WHERE project_id = ? AND record_id = ?)
		            THEN 1 ELSE 0 END`,
		projectID, recordID, projectID, recordID).Scan(&found)
	if err != nil {
		return false, err
	}
	return found == 1, nil
}

// --- record data path (content=record, API_Endpoints_Design.md §3.6–§3.8) ---

// ListRecordIDs returns the project's record ids in ascending order: every
// id that holds a value or a record entity. When dagGroupID is non-nil the
// result is restricted to records assigned to that data access group — the
// holder-with-a-group side of the data-access-group rule (REQ-AUTH-045).
func (s *Store) ListRecordIDs(ctx context.Context, projectID int64, dagGroupID *int64) ([]string, error) {
	q := `SELECT DISTINCT record_id FROM (
		 SELECT d.record_id FROM data d WHERE d.project_id = ?
		 UNION
		 SELECT e.record_id FROM record_entities e WHERE e.project_id = ?
	 )`
	args := []any{projectID, projectID}
	if dagGroupID != nil {
		q = `SELECT DISTINCT d.record_id FROM data d
		 JOIN record_entities e ON e.project_id = d.project_id AND e.record_id = d.record_id
		 WHERE d.project_id = ? AND e.dag_group_id = ?
		 UNION
		 SELECT e.record_id FROM record_entities e
		 WHERE e.project_id = ? AND e.dag_group_id = ?`
		args = []any{projectID, *dagGroupID, projectID, *dagGroupID}
	}
	q += ` ORDER BY 1`
	rows, err := s.DB.QueryContext(ctx, q, args...)
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

// GetRecordEntityTx reads one record entity inside tx (nil when absent).
func (s *Store) GetRecordEntityTx(ctx context.Context, tx *sql.Tx, projectID int64, recordID string) (*RecordEntity, error) {
	var re RecordEntity
	err := tx.QueryRowContext(ctx,
		`SELECT project_id, record_id, dag_group_id, created_by, created_at
		 FROM record_entities WHERE project_id = ? AND record_id = ?`,
		projectID, recordID).
		Scan(&re.ProjectID, &re.RecordID, &re.DagGroupID, &re.CreatedBy, &re.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &re, nil
}

// RecordExists reports whether the project holds a record entity with this
// id — the existence check for endpoints that act on one record outside a
// transaction.
func (s *Store) RecordExists(ctx context.Context, projectID int64, recordID string) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM record_entities WHERE project_id = ? AND record_id = ?`,
		projectID, recordID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateRecordEntityTx inserts a record identity row inside tx; the DAG
// assignment comes from the importing holder's active group (REQ-API-093).
func (s *Store) CreateRecordEntityTx(ctx context.Context, tx *sql.Tx, re *RecordEntity) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO record_entities (project_id, record_id, dag_group_id, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		re.ProjectID, re.RecordID, nullInt64(re.DagGroupID), nullInt64(re.CreatedBy), re.CreatedAt)
	return err
}

// UpsertDataValueTx inserts or replaces an EAV row inside tx (REQ-DB-017).
func (s *Store) UpsertDataValueTx(ctx context.Context, tx *sql.Tx, dv *DataValue) error {
	q := `INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`
	if s.Dialect == DialectMariaDB {
		q += ` ON DUPLICATE KEY UPDATE value = ?`
	} else {
		q += ` ON CONFLICT (project_id, record_id, unique_event_name,
				repeating_instrument, repeating_instance_number, field_name)
			 DO UPDATE SET value = ?`
	}
	_, err := tx.ExecContext(ctx, q,
		dv.ProjectID, dv.RecordID, dv.UniqueEventName, dv.RepeatingInstrument,
		dv.RepeatingInstanceNumber, dv.FieldName, dv.Value, dv.Value)
	return err
}

// DeleteDataValueTx removes one EAV row inside tx — the intentional clear
// of an empty import value (REQ-VAL-024).
func (s *Store) DeleteDataValueTx(ctx context.Context, tx *sql.Tx, dv *DataValue) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM data
		 WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		 AND repeating_instrument = ? AND repeating_instance_number = ? AND field_name = ?`,
		dv.ProjectID, dv.RecordID, dv.UniqueEventName, dv.RepeatingInstrument,
		dv.RepeatingInstanceNumber, dv.FieldName)
	return err
}

// ListDataValuesByRecordTx returns all values for a record inside tx.
func (s *Store) ListDataValuesByRecordTx(ctx context.Context, tx *sql.Tx, projectID int64, recordID string) ([]DataValue, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT project_id, record_id, unique_event_name, repeating_instrument,
			repeating_instance_number, field_name, value
		 FROM data WHERE project_id = ? AND record_id = ?`, projectID, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataValue
	for rows.Next() {
		var dv DataValue
		if err := rows.Scan(&dv.ProjectID, &dv.RecordID, &dv.UniqueEventName,
			&dv.RepeatingInstrument, &dv.RepeatingInstanceNumber, &dv.FieldName, &dv.Value); err != nil {
			return nil, err
		}
		out = append(out, dv)
	}
	return out, rows.Err()
}

// RecordHasAnyDataTx reports whether the record still holds any value.
func (s *Store) RecordHasAnyDataTx(ctx context.Context, tx *sql.Tx, projectID int64, recordID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND record_id = ?`,
		projectID, recordID).Scan(&n)
	return n > 0, err
}

// DeleteRecordTx removes the record itself once its last value is gone
// (REQ-API-036): identity row, survey links and date-shift offsets.
func (s *Store) DeleteRecordTx(ctx context.Context, tx *sql.Tx, projectID int64, recordID string) error {
	for _, q := range []string{
		`DELETE FROM survey_links WHERE project_id = ? AND record_id = ?`,
		`DELETE FROM anon_offsets WHERE project_id = ? AND record_id = ?`,
		`DELETE FROM record_entities WHERE project_id = ? AND record_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, projectID, recordID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRecordValuesTx deletes the record's values scoped by events and
// field names (nil lists = unrestricted) inside tx, returning what was
// removed for the record_deleted audit payload (REQ-AUD-009).
func (s *Store) DeleteRecordValuesTx(ctx context.Context, tx *sql.Tx, projectID int64, recordID string, events, fields []string) ([]DataValue, error) {
	q := `SELECT project_id, record_id, unique_event_name, repeating_instrument,
		repeating_instance_number, field_name, value
	 FROM data WHERE project_id = ? AND record_id = ?`
	args := []any{projectID, recordID}
	if len(events) > 0 {
		q += ` AND unique_event_name IN (` + placeholders(len(events)) + `)`
		for _, e := range events {
			args = append(args, e)
		}
	}
	if len(fields) > 0 {
		q += ` AND field_name IN (` + placeholders(len(fields)) + `)`
		for _, f := range fields {
			args = append(args, f)
		}
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var doomed []DataValue
	for rows.Next() {
		var dv DataValue
		if err := rows.Scan(&dv.ProjectID, &dv.RecordID, &dv.UniqueEventName,
			&dv.RepeatingInstrument, &dv.RepeatingInstanceNumber, &dv.FieldName, &dv.Value); err != nil {
			rows.Close()
			return nil, err
		}
		doomed = append(doomed, dv)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, dv := range doomed {
		if err := s.DeleteDataValueTx(ctx, tx, &dv); err != nil {
			return nil, err
		}
	}
	return doomed, nil
}

// EnsureAnonOffset returns the record's persisted date-shift, computing and
// persisting it on first use (REQ-DB-023): offset = min +
// SHA-256(project_id ':' record_id ':' salt) mod (max − min + 1). The range
// comes from System_Configuration_Design.md §3.6; max < min degenerates to
// a zero shift rather than a panic.
func EnsureAnonOffset(ctx context.Context, s *Store, projectID int64, recordID, salt string, min, max int) (int, error) {
	ao, err := s.GetAnonOffset(ctx, projectID, recordID)
	if err != nil {
		return 0, err
	}
	if ao != nil {
		return ao.OffsetDays, nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", projectID, recordID, salt)))
	n := new(big.Int).SetBytes(sum[:])
	span := int64(max-min) + 1
	off := min
	if span > 0 {
		off = min + int(new(big.Int).Mod(n, big.NewInt(span)).Int64())
	}
	err = s.CreateAnonOffset(ctx, &AnonOffset{ProjectID: projectID, RecordID: recordID, OffsetDays: off})
	if err != nil {
		// Concurrent first export: the row exists by now — use it.
		ao, gerr := s.GetAnonOffset(ctx, projectID, recordID)
		if gerr == nil && ao != nil {
			return ao.OffsetDays, nil
		}
		return 0, err
	}
	return off, nil
}

func placeholders(n int) string {
	out := make([]byte, 0, n*2-1)
	for i := range n {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}
