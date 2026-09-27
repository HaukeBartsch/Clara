package db

import (
	"context"
	"database/sql"
)

// Project modes and staging (GD-20, REQ-DB-034/035). The mode is stored as a
// string with the allowlist below enforced by the API; a project holds exactly
// one, and only the mode endpoint writes it.
const (
	ModeDevelopment = "development" // setup and data entry as usual (default)
	ModeProduction  = "production"  // setup changes go through a staging set
	ModeAnalysis    = "analysis"    // data entry disabled, reads and exports open
)

// ValidMode reports whether mode is one of the three allowed values. An
// unknown value never reaches the column (API_Endpoints_Design.md §4.21).
func ValidMode(mode string) bool {
	switch mode {
	case ModeDevelopment, ModeProduction, ModeAnalysis:
		return true
	}
	return false
}

// SetProjectModeTx writes the mode inside the caller's transaction, so the
// transition and its audit entry commit together (REQ-AUD-003). Callers
// validate the value and the transition; this is storage only.
func (s *Store) SetProjectModeTx(ctx context.Context, tx *sql.Tx, id int64, mode string) error {
	_, err := tx.ExecContext(ctx, `UPDATE projects SET mode = ? WHERE id = ?`, mode, id)
	return err
}

// PurgeProjectDataTx deletes the project's record data with the end-provision
// delete scope of Data_Export_Anonymization_Design.md §7.3 — every data-plane
// table keyed by project_id (EAV rows, record entities, survey links,
// anonymization offsets). Project metadata, structure, roles, memberships,
// tokens and the audit trail are kept, so the project stays administrable and
// the deletion itself auditable (used by development → production with
// keep_data = false, REQ-API-105). It returns the number of records removed.
func (s *Store) PurgeProjectDataTx(ctx context.Context, tx *sql.Tx, projectID int64) (int64, error) {
	var records int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM record_entities WHERE project_id = ?`, projectID).Scan(&records); err != nil {
		return 0, err
	}
	// survey_links reference records; data and anon_offsets are keyed by
	// record_id without a foreign key to record_entities, so clear the
	// dependants first and the entities last.
	for _, stmt := range []string{
		`DELETE FROM survey_links WHERE project_id = ?`,
		`DELETE FROM anon_offsets WHERE project_id = ?`,
		`DELETE FROM data WHERE project_id = ?`,
		`DELETE FROM record_entities WHERE project_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, projectID); err != nil {
			return 0, err
		}
	}
	return records, nil
}

// StagingSet is one open staging set (REQ-DB-035): the JSON snapshot of the
// staged design plus who opened it and when. The row exists only while a set
// is open, so its presence is the "staging open" answer.
type StagingSet struct {
	ProjectID int64
	Design    string // staged-design snapshot (JSON)
	OpenedBy  sql.NullInt64
	OpenedAt  string // DATETIME UTC "YYYY-MM-DD HH:MM:SS"
}

// StagingOpen reports whether the project has an open staging set.
func (s *Store) StagingOpen(ctx context.Context, projectID int64) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM project_staging WHERE project_id = ?`, projectID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetStaging returns the open staging set, or nil when none is open.
func (s *Store) GetStaging(ctx context.Context, projectID int64) (*StagingSet, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT project_id, design, opened_by, opened_at FROM project_staging WHERE project_id = ?`,
		projectID)
	var st StagingSet
	var openedAt any
	err := row.Scan(&st.ProjectID, &st.Design, &st.OpenedBy, &openedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if v, ok := datetimeString(openedAt); ok {
		st.OpenedAt = v
	}
	return &st, nil
}

// OpenStaging inserts the first snapshot of a staging set. A set that is
// already open is rejected by the caller (409, REQ-API-106); the primary key
// makes a race fail loudly rather than silently replace an open set.
func (s *Store) OpenStaging(ctx context.Context, projectID int64, design string, openedBy int64) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO project_staging (project_id, design, opened_by, opened_at)
		 VALUES (?, ?, ?, ?)`,
		projectID, design, nullInt64(sql.NullInt64{Int64: openedBy, Valid: openedBy > 0}), nowUTC())
	return err
}

// UpdateStagingDesign replaces the staged snapshot after a structure change
// applied to it (REQ-API-107). Reports whether a set was open to write to.
func (s *Store) UpdateStagingDesign(ctx context.Context, projectID int64, design string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE project_staging SET design = ? WHERE project_id = ?`, design, projectID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteStaging closes the open set without applying it (discard) or after a
// commit has applied the snapshot. Reports whether a row was there.
func (s *Store) DeleteStaging(ctx context.Context, projectID int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM project_staging WHERE project_id = ?`, projectID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
