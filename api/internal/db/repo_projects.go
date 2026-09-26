package db

import (
	"context"
	"database/sql"
)

// --- projects (REQ-DB-006, REQ-DB-007, GD-17) ---
//
// The projects table holds only identity and ethics metadata. The option
// flags, contract flag, end provision, and initial event names are data in an
// ordinary instrument, not project attributes (REQ-DB-032).

const projectColumns = `id, project_name, organization, pi_name, pi_email,
	dm_name, dm_email, rek_number, rek_start_date, rek_end_date,
	start_date, end_date, participant_names, creation_time`

func scanProject(row interface{ Scan(dest ...any) error }) (Project, error) {
	var (
		p         Project
		dmName    any
		dmEmail   any
		rekNumber any
		rekStart  any
		rekEnd    any
		startDate any
		endDate   any
		creation  any
	)
	err := row.Scan(&p.ID, &p.ProjectName, &p.Organization, &p.PIName, &p.PIEmail,
		&dmName, &dmEmail, &rekNumber, &rekStart, &rekEnd,
		&startDate, &endDate, &p.ParticipantNames, &creation)
	if err != nil {
		return p, err
	}
	if v, ok := nullAnyString(dmName); ok {
		p.DMName = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(dmEmail); ok {
		p.DMEmail = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(rekNumber); ok {
		p.RekNumber = sql.NullString{String: v, Valid: true}
	}
	if v, ok := dateString(rekStart); ok {
		p.RekStartDate = sql.NullString{String: v, Valid: true}
	}
	if v, ok := dateString(rekEnd); ok {
		p.RekEndDate = sql.NullString{String: v, Valid: true}
	}
	if v, ok := dateString(startDate); ok {
		p.StartDate = sql.NullString{String: v, Valid: true}
	}
	if v, ok := dateString(endDate); ok {
		p.EndDate = sql.NullString{String: v, Valid: true}
	}
	if v, ok := datetimeString(creation); ok {
		p.CreationTime = v
	}
	return p, nil
}

// nullAnyString lifts a scanned nullable TEXT value (string, []byte, or
// NULL) into a plain string; ok is false for NULL/empty.
func nullAnyString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		if t == "" {
			return "", false
		}
		return t, true
	case []byte:
		if len(t) == 0 {
			return "", false
		}
		return string(t), true
	default:
		return "", false
	}
}

// CreateProject inserts a project row and returns the new id. Creation time
// defaults to now when unset (REQ-DB-006).
func (s *Store) CreateProject(ctx context.Context, p *Project) (int64, error) {
	if p.CreationTime == "" {
		p.CreationTime = nowUTC()
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO projects (project_name, organization, pi_name, pi_email,
			dm_name, dm_email, rek_number, rek_start_date, rek_end_date,
			start_date, end_date, participant_names, creation_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectName, p.Organization, p.PIName, p.PIEmail,
		nullStr(p.DMName), nullStr(p.DMEmail), nullStr(p.RekNumber),
		nullStr(p.RekStartDate), nullStr(p.RekEndDate),
		nullStr(p.StartDate), nullStr(p.EndDate), p.ParticipantNames, p.CreationTime)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetProject(ctx context.Context, id int64) (*Project, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
	p, err := scanProject(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// GetProjectByName resolves the unique project name (REQ-DB-006). nil when
// no such project exists.
func (s *Store) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE project_name = ?`, name)
	p, err := scanProject(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY project_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProject rewrites the mutable identity/ethics fields including the
// name (PUT accepts project_name per API_Endpoints_Design.md §4.5 — callers
// check uniqueness first); the creation time is fixed at creation.
func (s *Store) UpdateProject(ctx context.Context, p *Project) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE projects SET
			project_name = ?, organization = ?, pi_name = ?, pi_email = ?,
			dm_name = ?, dm_email = ?, rek_number = ?,
			rek_start_date = ?, rek_end_date = ?, start_date = ?, end_date = ?,
			participant_names = ?
		 WHERE id = ?`,
		p.ProjectName, p.Organization, p.PIName, p.PIEmail,
		nullStr(p.DMName), nullStr(p.DMEmail), nullStr(p.RekNumber),
		nullStr(p.RekStartDate), nullStr(p.RekEndDate),
		nullStr(p.StartDate), nullStr(p.EndDate), p.ParticipantNames, p.ID)
	return err
}

// DeleteProject removes the project; the schema cascades its structure and
// data (arms, events, instruments, fields, EAV values, links) per the
// ON DELETE CASCADE rules in the migration.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}
