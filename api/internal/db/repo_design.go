package db

import (
	"context"
	"database/sql"
)

// --- arms (REQ-DB-011) ---

const armColumns = `id, project_id, arm_num, name, position`

func scanArm(row interface{ Scan(dest ...any) error }) (Arm, error) {
	var (
		a   Arm
		name any
	)
	if err := row.Scan(&a.ID, &a.ProjectID, &a.ArmNum, &name, &a.Position); err != nil {
		return a, err
	}
	if v, ok := nullAnyString(name); ok {
		a.Name = sql.NullString{String: v, Valid: true}
	}
	return a, nil
}

// AddArm inserts an arm; position defaults to the end of the arm list.
func (s *Store) AddArm(ctx context.Context, a *Arm) (int64, error) {
	if a.Position == 0 {
		if err := s.nextPosition(ctx, `arms`, `project_id = ?`, a.ProjectID, &a.Position); err != nil {
			return 0, err
		}
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO arms (project_id, arm_num, name, position) VALUES (?, ?, ?, ?)`,
		a.ProjectID, a.ArmNum, nullStr(a.Name), a.Position)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetArm(ctx context.Context, id int64) (*Arm, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+armColumns+` FROM arms WHERE id = ?`, id)
	a, err := scanArm(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// GetArmByNum resolves the 1-based arm number within a project
// (REQ-DB-011). nil when no such arm exists.
func (s *Store) GetArmByNum(ctx context.Context, projectID int64, armNum int) (*Arm, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+armColumns+` FROM arms WHERE project_id = ? AND arm_num = ?`, projectID, armNum)
	a, err := scanArm(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListArms(ctx context.Context, projectID int64) ([]Arm, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+armColumns+` FROM arms WHERE project_id = ? ORDER BY position, arm_num`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Arm
	for rows.Next() {
		a, err := scanArm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateArm(ctx context.Context, a *Arm) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE arms SET name = ?, position = ? WHERE id = ?`,
		nullStr(a.Name), a.Position, a.ID)
	return err
}

func (s *Store) DeleteArm(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM arms WHERE id = ?`, id)
	return err
}

// --- events (REQ-DB-011, GD-15) ---

const eventColumns = `id, project_id, arm_id, event_name, unique_event_name,
	period, safe_region_start, safe_region_end, position`

func scanEvent(row interface{ Scan(dest ...any) error }) (Event, error) {
	var (
		e              Event
		period         any
		safeRegionStart any
		safeRegionEnd   any
	)
	err := row.Scan(&e.ID, &e.ProjectID, &e.ArmID, &e.EventName, &e.UniqueEventName,
		&period, &safeRegionStart, &safeRegionEnd, &e.Position)
	if err != nil {
		return e, err
	}
	if v, ok := nullInt64Value(period); ok {
		e.Period = sql.NullInt64{Int64: v, Valid: true}
	}
	if v, ok := nullInt64Value(safeRegionStart); ok {
		e.SafeRegionStart = sql.NullInt64{Int64: v, Valid: true}
	}
	if v, ok := nullInt64Value(safeRegionEnd); ok {
		e.SafeRegionEnd = sql.NullInt64{Int64: v, Valid: true}
	}
	return e, nil
}

// nullInt64Value lifts a scanned nullable INTEGER value into an int64.
func nullInt64Value(v any) (int64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case int64:
		return t, true
	case int:
		return int64(t), true
	default:
		return 0, false
	}
}

// AddEvent inserts an event; position defaults to the end of its arm's list.
// The API supplies unique_event_name (<label>_arm_<n>, REQ-DB-011).
func (s *Store) AddEvent(ctx context.Context, e *Event) (int64, error) {
	if e.Position == 0 {
		if err := s.nextPosition(ctx, `events`, `project_id = ? AND arm_id = ?`, e.ProjectID, e.ArmID, &e.Position); err != nil {
			return 0, err
		}
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO events (project_id, arm_id, event_name, unique_event_name,
			period, safe_region_start, safe_region_end, position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ProjectID, e.ArmID, e.EventName, e.UniqueEventName,
		nullInt64(e.Period), nullInt64(e.SafeRegionStart), nullInt64(e.SafeRegionEnd), e.Position)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetEvent(ctx context.Context, id int64) (*Event, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ?`, id)
	e, err := scanEvent(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// GetEventByUniqueName resolves the unique event name within a project
// (REQ-DB-011). nil when no such event exists.
func (s *Store) GetEventByUniqueName(ctx context.Context, projectID int64, uniqueName string) (*Event, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE project_id = ? AND unique_event_name = ?`,
		projectID, uniqueName)
	e, err := scanEvent(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// eventOrderClause is the canonical per-arm event order (GD-15, REQ-DB-011):
// events with a timepoint first, sorted by period ascending (ties by
// position); then events without a timepoint, sorted by position.
const eventOrderClause = `ORDER BY (period IS NULL), period, position`

func (s *Store) ListEvents(ctx context.Context, projectID int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE project_id = ? `+eventOrderClause, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListEventsByArm(ctx context.Context, projectID, armID int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE project_id = ? AND arm_id = ? `+eventOrderClause,
		projectID, armID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpdateEvent(ctx context.Context, e *Event) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE events SET
			event_name = ?, unique_event_name = ?,
			period = ?, safe_region_start = ?, safe_region_end = ?, position = ?
		 WHERE id = ?`,
		e.EventName, e.UniqueEventName,
		nullInt64(e.Period), nullInt64(e.SafeRegionStart), nullInt64(e.SafeRegionEnd), e.Position, e.ID)
	return err
}

// SetEventPosition reorders a single event within its arm (REQ-API-103).
func (s *Store) SetEventPosition(ctx context.Context, id int64, position int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET position = ? WHERE id = ?`, position, id)
	return err
}

func (s *Store) DeleteEvent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id)
	return err
}

// --- instruments (REQ-DB-011) ---

const instrumentColumns = `id, project_id, name, position, is_survey, branching_logic`

func scanInstrument(row interface{ Scan(dest ...any) error }) (Instrument, error) {
	var (
		i            Instrument
		isSurvey     int
		branching    any
	)
	err := row.Scan(&i.ID, &i.ProjectID, &i.Name, &i.Position, &isSurvey, &branching)
	if err != nil {
		return i, err
	}
	i.IsSurvey = isSurvey != 0
	if v, ok := nullAnyString(branching); ok {
		i.BranchingLogic = sql.NullString{String: v, Valid: true}
	}
	return i, nil
}

// AddInstrument inserts an instrument; position defaults to the end of the
// instrument list.
func (s *Store) AddInstrument(ctx context.Context, i *Instrument) (int64, error) {
	if i.Position == 0 {
		if err := s.nextPosition(ctx, `instruments`, `project_id = ?`, i.ProjectID, &i.Position); err != nil {
			return 0, err
		}
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO instruments (project_id, name, position, is_survey, branching_logic)
		 VALUES (?, ?, ?, ?, ?)`,
		i.ProjectID, i.Name, i.Position, boolToInt(i.IsSurvey), nullStr(i.BranchingLogic))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetInstrument(ctx context.Context, id int64) (*Instrument, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+instrumentColumns+` FROM instruments WHERE id = ?`, id)
	i, err := scanInstrument(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &i, nil
}

// GetInstrumentByName resolves the unique instrument name within a project
// (REQ-DB-011). nil when no such instrument exists.
func (s *Store) GetInstrumentByName(ctx context.Context, projectID int64, name string) (*Instrument, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+instrumentColumns+` FROM instruments WHERE project_id = ? AND name = ?`,
		projectID, name)
	i, err := scanInstrument(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}