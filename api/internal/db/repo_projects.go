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
	start_date, end_date, participant_names, creation_time, mode, record_naming`

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
		&startDate, &endDate, &p.ParticipantNames, &creation, &p.Mode, &p.RecordNaming)
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

// The two record naming modes (REQ-DB-045): auto names new participants with
// plain numeric ids (REQ-API-151), manual keeps the typed record_id — the
// default every existing project keeps.
const (
	RecordNamingAuto   = "auto"
	RecordNamingManual = "manual"
)

// CreateProject inserts a project row and returns the new id. Creation time
// defaults to now when unset (REQ-DB-006), the mode to development, the
// starting mode of every new project (REQ-DB-034), and the naming mode to
// manual (REQ-DB-045).
// CreateProjectTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) CreateProjectTx(ctx context.Context, tx *sql.Tx, p *Project) (int64, error) {
	if p.CreationTime == "" {
		p.CreationTime = nowUTC()
	}
	if p.Mode == "" {
		p.Mode = ModeDevelopment
	}
	if p.RecordNaming == "" {
		p.RecordNaming = RecordNamingManual
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO projects (project_name, organization, pi_name, pi_email,
			dm_name, dm_email, rek_number, rek_start_date, rek_end_date,
			start_date, end_date, participant_names, creation_time, mode, record_naming)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectName, p.Organization, p.PIName, p.PIEmail,
		nullStr(p.DMName), nullStr(p.DMEmail), nullStr(p.RekNumber),
		nullStr(p.RekStartDate), nullStr(p.RekEndDate),
		nullStr(p.StartDate), nullStr(p.EndDate), p.ParticipantNames, p.CreationTime,
		p.Mode, p.RecordNaming)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateProject inserts a project row and returns the new id. Creation time
// defaults to now when unset (REQ-DB-006), the mode to development, the
// starting mode of every new project (REQ-DB-034), and the naming mode to
// manual (REQ-DB-045).
func (s *Store) CreateProject(ctx context.Context, p *Project) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.CreateProjectTx(ctx, tx, p)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// GetProject resolves a project by id; nil when there is no such row. Code
// holding an open transaction uses GetProjectTx instead (see §10 of
// Design/Database_Schema_Design.md).
func (s *Store) GetProject(ctx context.Context, id int64) (*Project, error) {
	return projectOrNil(s.DB.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
}

// GetProjectTx is GetProject read inside tx. A transaction must not read
// through the pool: a second connection cannot see this transaction's own
// writes and answers from its own snapshot, so the value can disagree with what
// the transaction is building — and on SQLite before WAL it could wait behind
// the locks that same transaction holds.
func (s *Store) GetProjectTx(ctx context.Context, tx *sql.Tx, id int64) (*Project, error) {
	return projectOrNil(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
}

// GetProjectByName resolves the unique project name (REQ-DB-006). nil when
// no such project exists.
func (s *Store) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	return projectOrNil(s.DB.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE project_name = ?`, name))
}

// GetProjectByNameTx is GetProjectByName read inside tx. A uniqueness check
// that will be settled by an insert in this transaction has to be made against
// the transaction's own snapshot, or another request can take the name between
// the check and the write it guards.
func (s *Store) GetProjectByNameTx(ctx context.Context, tx *sql.Tx, name string) (*Project, error) {
	return projectOrNil(tx.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE project_name = ?`, name))
}

// projectOrNil renders a one-row project query: no rows means "no such
// project", not an error (REQ-DB-006).
func projectOrNil(row interface{ Scan(dest ...any) error }) (*Project, error) {
	p, err := scanProject(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
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
// check uniqueness first) and the record naming mode (REQ-DB-045); the
// creation time is fixed at creation and the mode is deliberately absent — it
// changes only through the mode endpoint (REQ-DB-034, SetProjectMode).
// UpdateProjectTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) UpdateProjectTx(ctx context.Context, tx *sql.Tx, p *Project) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE projects SET
			project_name = ?, organization = ?, pi_name = ?, pi_email = ?,
			dm_name = ?, dm_email = ?, rek_number = ?,
			rek_start_date = ?, rek_end_date = ?, start_date = ?, end_date = ?,
			participant_names = ?, record_naming = ?
		 WHERE id = ?`,
		p.ProjectName, p.Organization, p.PIName, p.PIEmail,
		nullStr(p.DMName), nullStr(p.DMEmail), nullStr(p.RekNumber),
		nullStr(p.RekStartDate), nullStr(p.RekEndDate),
		nullStr(p.StartDate), nullStr(p.EndDate), p.ParticipantNames, p.RecordNaming, p.ID)
	return err
}

// UpdateProject rewrites the mutable identity/ethics fields including the
// name (PUT accepts project_name per API_Endpoints_Design.md §4.5 — callers
// check uniqueness first) and the record naming mode (REQ-DB-045); the
// creation time is fixed at creation and the mode is deliberately absent — it
// changes only through the mode endpoint (REQ-DB-034, SetProjectMode).
func (s *Store) UpdateProject(ctx context.Context, p *Project) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.UpdateProjectTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProject removes the project; the schema cascades its structure and
// data (arms, events, instruments, fields, EAV values, links) per the
// ON DELETE CASCADE rules in the migration.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}
