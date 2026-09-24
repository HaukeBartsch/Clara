package db

import (
	"context"
	"database/sql"
	"fmt"
)

// nextPosition returns the next available position value for rows within
// the filter clause; the caller passes a pointer to receive the value.
// It is used by Add* methods to default position to the end of the list.
func (s *Store) nextPosition(ctx context.Context, table, where string, posPtr any, args ...any) error {
	var maxPos sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), 0) FROM `+table+` WHERE `+where, args...).Scan(&maxPos)
	if err != nil {
		return fmt.Errorf("nextPosition %s: %w", table, err)
	}
	if !maxPos.Valid {
		return fmt.Errorf("nextPosition %s: no value", table)
	}
	switch p := posPtr.(type) {
	case *int:
		*p = int(maxPos.Int64) + 1
	default:
		return fmt.Errorf("nextPosition: unexpected pointer type %T", posPtr)
	}
	return nil
}

// --- arms (REQ-DB-011) ---

const armColumns = `id, project_id, arm_num, name, position`

func scanArm(row interface{ Scan(dest ...any) error }) (Arm, error) {
	var (
		a    Arm
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
		if err := s.nextPosition(ctx, `arms`, `project_id = ?`, &a.Position, a.ProjectID); err != nil {
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
		e               Event
		period          any
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
		if err := s.nextPosition(ctx, `events`, `project_id = ? AND arm_id = ?`, &e.Position, e.ProjectID, e.ArmID); err != nil {
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
		i         Instrument
		isSurvey  int
		branching any
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
		if err := s.nextPosition(ctx, `instruments`, `project_id = ?`, &i.Position, i.ProjectID); err != nil {
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
		return nil, err
	}
	return &i, nil
}

func (s *Store) ListInstruments(ctx context.Context, projectID int64) ([]Instrument, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+instrumentColumns+` FROM instruments WHERE project_id = ? ORDER BY position, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instrument
	for rows.Next() {
		i, err := scanInstrument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// UpdateInstrument rewrites the instrument's mutable attributes — the name,
// the is_survey flag (GD-9), the branching logic (GD-13, REQ-VAL-040), and
// the position (REQ-API-101, REQ-API-066); the id and project are fixed.
func (s *Store) UpdateInstrument(ctx context.Context, i *Instrument) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE instruments SET name = ?, position = ?, is_survey = ?, branching_logic = ?
		 WHERE id = ?`,
		i.Name, i.Position, boolToInt(i.IsSurvey), nullStr(i.BranchingLogic), i.ID)
	return err
}

// SetInstrumentPosition reorders a single instrument (REQ-API-066).
func (s *Store) SetInstrumentPosition(ctx context.Context, id int64, position int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE instruments SET position = ? WHERE id = ?`, position, id)
	return err
}

func (s *Store) DeleteInstrument(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM instruments WHERE id = ?`, id)
	return err
}

// --- instrument_events (REQ-DB-012, REQ-API-072/073) ---

// ListInstrumentEvents returns the checked (instrument, event) pairs of a
// project (REQ-DB-012), ordered by instrument position and then event
// position — the shape the per-arm instrument × event matrix is rendered
// from (REQ-API-072).
func (s *Store) ListInstrumentEvents(ctx context.Context, projectID int64) ([]InstrumentEvent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT ie.instrument_id, ie.event_id
		 FROM instrument_events ie
		 JOIN events e ON e.id = ie.event_id
		 JOIN instruments i ON i.id = ie.instrument_id
		 WHERE e.project_id = ?
		 ORDER BY i.position, e.position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstrumentEvent
	for rows.Next() {
		var p InstrumentEvent
		if err := rows.Scan(&p.InstrumentID, &p.EventID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetInstrumentEventsForArm replaces the mapping of one arm from the full
// checked matrix (REQ-DB-012, REQ-API-073); pairs is the complete set for
// the arm after the change.
func (s *Store) SetInstrumentEventsForArm(ctx context.Context, projectID, armID int64, pairs []InstrumentEvent) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM instrument_events
		 WHERE event_id IN (SELECT id FROM events WHERE project_id = ? AND arm_id = ?)`,
		projectID, armID); err != nil {
		return err
	}
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO instrument_events (instrument_id, event_id) VALUES (?, ?)`,
			p.InstrumentID, p.EventID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- fields (REQ-DB-013/014) ---

const fieldColumns = `id, project_id, instrument_id, field_name, field_label,
	field_type, section_header, choices, field_note,
	validation_type, validation_format, validation_min, validation_max,
	required, branching_logic, calculation, matrix_group,
	personal_information, export_approved, position`

func scanField(row interface{ Scan(dest ...any) error }) (Field, error) {
	var (
		f              Field
		fieldLabel     any
		sectionHeader  any
		choices        any
		fieldNote      any
		validationType any
		validationFmt  any
		validationMin  any
		validationMax  any
		required       int
		branchingLogic any
		calculation    any
		matrixGroup    any
		personalInfo   int
		exportApproved int
	)
	err := row.Scan(&f.ID, &f.ProjectID, &f.InstrumentID, &f.FieldName, &fieldLabel,
		&f.FieldType, &sectionHeader, &choices, &fieldNote,
		&validationType, &validationFmt, &validationMin, &validationMax,
		&required, &branchingLogic, &calculation, &matrixGroup,
		&personalInfo, &exportApproved, &f.Position)
	if err != nil {
		return f, err
	}
	f.Required = required != 0
	f.PersonalInformation = personalInfo != 0
	f.ExportApproved = exportApproved != 0
	if v, ok := nullAnyString(fieldLabel); ok {
		f.FieldLabel = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(sectionHeader); ok {
		f.SectionHeader = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(choices); ok {
		f.Choices = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(fieldNote); ok {
		f.FieldNote = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(validationType); ok {
		f.ValidationType = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(validationFmt); ok {
		f.ValidationFormat = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(validationMin); ok {
		f.ValidationMin = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(validationMax); ok {
		f.ValidationMax = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(branchingLogic); ok {
		f.BranchingLogic = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(calculation); ok {
		f.Calculation = sql.NullString{String: v, Valid: true}
	}
	if v, ok := nullAnyString(matrixGroup); ok {
		f.MatrixGroup = sql.NullString{String: v, Valid: true}
	}
	return f, nil
}

// AddField inserts a field; position defaults to the end of its instrument's
// field list.
func (s *Store) AddField(ctx context.Context, f *Field) (int64, error) {
	if f.Position == 0 {
		if err := s.nextPosition(ctx, `fields`, `project_id = ? AND instrument_id = ?`,
			&f.Position, f.ProjectID, f.InstrumentID); err != nil {
			return 0, err
		}
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO fields (project_id, instrument_id, field_name, field_label,
			field_type, section_header, choices, field_note,
			validation_type, validation_format, validation_min, validation_max,
			required, branching_logic, calculation, matrix_group,
			personal_information, export_approved, position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ProjectID, f.InstrumentID, f.FieldName, nullStr(f.FieldLabel),
		f.FieldType, nullStr(f.SectionHeader), nullStr(f.Choices), nullStr(f.FieldNote),
		nullStr(f.ValidationType), nullStr(f.ValidationFormat), nullStr(f.ValidationMin), nullStr(f.ValidationMax),
		boolToInt(f.Required), nullStr(f.BranchingLogic), nullStr(f.Calculation), nullStr(f.MatrixGroup),
		boolToInt(f.PersonalInformation), boolToInt(f.ExportApproved), f.Position)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetField(ctx context.Context, id int64) (*Field, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+fieldColumns+` FROM fields WHERE id = ?`, id)
	f, err := scanField(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// GetFieldByName resolves the unique field name within a project
// (REQ-DB-013). nil when no such field exists.
func (s *Store) GetFieldByName(ctx context.Context, projectID int64, name string) (*Field, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+fieldColumns+` FROM fields WHERE project_id = ? AND field_name = ?`,
		projectID, name)
	f, err := scanField(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// ListFieldsByInstrument returns the instrument's fields in position order
// — the designer's field table (REQ-API-067, REQ-DB-013).
func (s *Store) ListFieldsByInstrument(ctx context.Context, projectID, instrumentID int64) ([]Field, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+fieldColumns+`
		 FROM fields WHERE project_id = ? AND instrument_id = ? ORDER BY position`,
		projectID, instrumentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Field
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListFields returns every field of a project in instrument order, then
// field position — the order the data dictionary is rendered
// (REQ-API-019, REQ-DB-013).
func (s *Store) ListFields(ctx context.Context, projectID int64) ([]Field, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+fieldColumns+`
		 FROM fields f
		 JOIN instruments i ON i.id = f.instrument_id
		 WHERE f.project_id = ?
		 ORDER BY i.position, f.position`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Field
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- calculated_dependencies (GD-11, REQ-VAL-037) ---

func (s *Store) AddCalculatedDependency(ctx context.Context, cd *CalculatedDependency) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO calculated_dependencies
		 (project_id, calculated_field_id, ref_unique_event_name, ref_field_name)
		 VALUES (?, ?, ?, ?)`,
		cd.ProjectID, cd.CalculatedFieldID, cd.RefUniqueEventName, cd.RefFieldName)
	return err
}

func (s *Store) DeleteCalculatedDependenciesByField(ctx context.Context, projectID, fieldID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM calculated_dependencies
		 WHERE project_id = ? AND calculated_field_id = ?`, projectID, fieldID)
	return err
}

func (s *Store) ListCalculatedDependencies(ctx context.Context, projectID int64) ([]CalculatedDependency, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id, calculated_field_id, ref_unique_event_name, ref_field_name
		 FROM calculated_dependencies WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalculatedDependency
	for rows.Next() {
		var cd CalculatedDependency
		if err := rows.Scan(&cd.ProjectID, &cd.CalculatedFieldID,
			&cd.RefUniqueEventName, &cd.RefFieldName); err != nil {
			return nil, err
		}
		out = append(out, cd)
	}
	return out, rows.Err()
}

// --- data (EAV) (REQ-DB-015…020) ---

// AddDataValue inserts a single EAV row (REQ-DB-015).
func (s *Store) AddDataValue(ctx context.Context, dv *DataValue) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		dv.ProjectID, dv.RecordID, dv.UniqueEventName, dv.RepeatingInstrument,
		dv.RepeatingInstanceNumber, dv.FieldName, dv.Value)
	return err
}

// DeleteDataValueByID deletes a single EAV row by its unique key (REQ-DB-016).
func (s *Store) DeleteDataValueByID(ctx context.Context, dv *DataValue) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM data
		 WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		 AND repeating_instrument = ? AND repeating_instance_number = ? AND field_name = ?`,
		dv.ProjectID, dv.RecordID, dv.UniqueEventName, dv.RepeatingInstrument,
		dv.RepeatingInstanceNumber, dv.FieldName)
	return err
}

// UpsertDataValue inserts or replaces an EAV row (REQ-DB-017). The upsert
// clause is dialect-specific: SQLite uses ON CONFLICT, MariaDB uses ON
// DUPLICATE KEY UPDATE (the data table's UNIQUE key drives both).
func (s *Store) UpsertDataValue(ctx context.Context, dv *DataValue) error {
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
	_, err := s.DB.ExecContext(ctx, q,
		dv.ProjectID, dv.RecordID, dv.UniqueEventName, dv.RepeatingInstrument,
		dv.RepeatingInstanceNumber, dv.FieldName, dv.Value, dv.Value)
	return err
}

// ListDataValuesByRecord returns all values for a record in a project
// (REQ-DB-018).
func (s *Store) ListDataValuesByRecord(ctx context.Context, projectID int64, recordID string) ([]DataValue, error) {
	rows, err := s.DB.QueryContext(ctx,
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

// ListDataValuesByEvent returns all values for a record in a specific event
// (REQ-DB-019).
func (s *Store) ListDataValuesByEvent(ctx context.Context, projectID int64, recordID, uniqueEventName string) ([]DataValue, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id, record_id, unique_event_name, repeating_instrument,
			repeating_instance_number, field_name, value
		 FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ?`,
		projectID, recordID, uniqueEventName)
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

// ListDataValuesByInstrument returns all values for a record in a specific
// instrument (REQ-DB-020). The data table stores the instrument by name
// (repeating_instrument), not by id, so this filters directly on the name.
func (s *Store) ListDataValuesByInstrument(ctx context.Context, projectID int64, recordID, instrumentName string) ([]DataValue, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT project_id, record_id, unique_event_name, repeating_instrument,
			repeating_instance_number, field_name, value
		 FROM data
		 WHERE project_id = ? AND record_id = ? AND repeating_instrument = ?`,
		projectID, recordID, instrumentName)
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

// --- record_entities (REQ-DB-029, GD-10) ---

const recordEntityColumns = `project_id, record_id, dag_group_id, created_by, created_at`

func scanRecordEntity(row interface{ Scan(dest ...any) error }) (RecordEntity, error) {
	var (
		re      RecordEntity
		created any
	)
	err := row.Scan(&re.ProjectID, &re.RecordID, &re.DagGroupID, &re.CreatedBy, &created)
	if err != nil {
		return re, err
	}
	if v, ok := datetimeString(created); ok {
		re.CreatedAt = v
	}
	return re, nil
}

// CreateRecordEntity inserts a record identity row (REQ-DB-029).
func (s *Store) CreateRecordEntity(ctx context.Context, re *RecordEntity) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO record_entities (project_id, record_id, dag_group_id, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		re.ProjectID, re.RecordID, nullInt64(re.DagGroupID), nullInt64(re.CreatedBy), nowUTC())
	return err
}

// GetRecordEntity returns the record identity row (REQ-DB-029).
func (s *Store) GetRecordEntity(ctx context.Context, projectID int64, recordID string) (*RecordEntity, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+recordEntityColumns+` FROM record_entities WHERE project_id = ? AND record_id = ?`,
		projectID, recordID)
	re, err := scanRecordEntity(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &re, nil
}

// ListRecordEntities returns all records in a project (REQ-DB-029).
func (s *Store) ListRecordEntities(ctx context.Context, projectID int64) ([]RecordEntity, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+recordEntityColumns+` FROM record_entities WHERE project_id = ? ORDER BY record_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordEntity
	for rows.Next() {
		re, err := scanRecordEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, rows.Err()
}

// SetRecordDAG assigns a data access group to a record (REQ-DB-029).
func (s *Store) SetRecordDAG(ctx context.Context, projectID int64, recordID string, dagGroupID sql.NullInt64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE record_entities SET dag_group_id = ? WHERE project_id = ? AND record_id = ?`,
		nullInt64(dagGroupID), projectID, recordID)
	return err
}

// SetRecordCreatedBy assigns the user who created a record (REQ-DB-029).
func (s *Store) SetRecordCreatedBy(ctx context.Context, projectID int64, recordID string, userID sql.NullInt64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE record_entities SET created_by = ? WHERE project_id = ? AND record_id = ?`,
		nullInt64(userID), projectID, recordID)
	return err
}

// DeleteRecordEntity removes a record identity row (REQ-DB-029).
func (s *Store) DeleteRecordEntity(ctx context.Context, projectID int64, recordID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM record_entities WHERE project_id = ? AND record_id = ?`, projectID, recordID)
	return err
}

// --- anon_offsets (REQ-DB-023) ---

// CreateAnonOffset inserts a deterministic date-shift (REQ-DB-023).
func (s *Store) CreateAnonOffset(ctx context.Context, ao *AnonOffset) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO anon_offsets (project_id, record_id, offset_days)
		 VALUES (?, ?, ?)`, ao.ProjectID, ao.RecordID, ao.OffsetDays)
	return err
}

// GetAnonOffset returns the date-shift for a record (REQ-DB-023).
func (s *Store) GetAnonOffset(ctx context.Context, projectID int64, recordID string) (*AnonOffset, error) {
	var ao AnonOffset
	err := s.DB.QueryRowContext(ctx,
		`SELECT project_id, record_id, offset_days
		 FROM anon_offsets WHERE project_id = ? AND record_id = ?`, projectID, recordID).
		Scan(&ao.ProjectID, &ao.RecordID, &ao.OffsetDays)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ao, nil
}

// DeleteAnonOffset removes a date-shift (REQ-DB-023).
func (s *Store) DeleteAnonOffset(ctx context.Context, projectID int64, recordID string) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM anon_offsets WHERE project_id = ? AND record_id = ?`, projectID, recordID)
	return err
}

// --- survey_links (REQ-DB-027, GD-9) ---

const surveyLinkColumns = `id, project_id, record_id, instrument_id, token, revoked, created_by, created_at`

func scanSurveyLink(row interface{ Scan(dest ...any) error }) (SurveyLink, error) {
	var (
		sl      SurveyLink
		revoked int
		created any
	)
	err := row.Scan(&sl.ID, &sl.ProjectID, &sl.RecordID, &sl.InstrumentID, &sl.Token,
		&revoked, &sl.CreatedBy, &created)
	if err != nil {
		return sl, err
	}
	sl.Revoked = revoked != 0
	if v, ok := datetimeString(created); ok {
		sl.CreatedAt = v
	}
	return sl, nil
}

// CreateSurveyLink inserts a stable public fill token (REQ-DB-027, GD-9).
func (s *Store) CreateSurveyLink(ctx context.Context, sl *SurveyLink) (int64, error) {
	sl.Token = newToken()
	sl.CreatedAt = nowUTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO survey_links (project_id, record_id, instrument_id, token, revoked, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sl.ProjectID, sl.RecordID, sl.InstrumentID, sl.Token, boolToInt(sl.Revoked),
		nullInt64(sl.CreatedBy), sl.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	sl.ID = id
	return id, nil
}

// GetSurveyLinkByToken resolves a survey link by its token (REQ-DB-027).
func (s *Store) GetSurveyLinkByToken(ctx context.Context, token string) (*SurveyLink, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+surveyLinkColumns+` FROM survey_links WHERE token = ?`, token)
	sl, err := scanSurveyLink(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &sl, nil
}

// GetSurveyLink returns the survey link for a (project, record, instrument) tuple.
func (s *Store) GetSurveyLink(ctx context.Context, projectID int64, recordID string, instrumentID int64) (*SurveyLink, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+surveyLinkColumns+` FROM survey_links WHERE project_id = ? AND record_id = ? AND instrument_id = ?`,
		projectID, recordID, instrumentID)
	sl, err := scanSurveyLink(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &sl, nil
}

// ListSurveyLinks returns all survey links for a project (REQ-DB-027).
func (s *Store) ListSurveyLinks(ctx context.Context, projectID int64) ([]SurveyLink, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+surveyLinkColumns+` FROM survey_links WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SurveyLink
	for rows.Next() {
		sl, err := scanSurveyLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// RevokeSurveyLink invalidates a survey link (REQ-DB-027).
func (s *Store) RevokeSurveyLink(ctx context.Context, projectID int64, recordID string, instrumentID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE survey_links SET revoked = 1
		 WHERE project_id = ? AND record_id = ? AND instrument_id = ?`,
		projectID, recordID, instrumentID)
	return err
}

// DeleteSurveyLink removes a survey link entirely (REQ-DB-027).
func (s *Store) DeleteSurveyLink(ctx context.Context, projectID int64, recordID string, instrumentID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM survey_links WHERE project_id = ? AND record_id = ? AND instrument_id = ?`,
		projectID, recordID, instrumentID)
	return err
}

// --- dag_groups (REQ-DB-028, GD-10) ---

const dagGroupColumns = `id, project_id, name, created_at`

func scanDagGroup(row interface{ Scan(dest ...any) error }) (DagGroup, error) {
	var (
		dg      DagGroup
		created any
	)
	err := row.Scan(&dg.ID, &dg.ProjectID, &dg.Name, &created)
	if err != nil {
		return dg, err
	}
	if v, ok := datetimeString(created); ok {
		dg.CreatedAt = v
	}
	return dg, nil
}

// CreateDAGGroup inserts a data access group (REQ-DB-028, GD-10).
func (s *Store) CreateDAGGroup(ctx context.Context, dg *DagGroup) (int64, error) {
	dg.CreatedAt = nowUTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO dag_groups (project_id, name, created_at) VALUES (?, ?, ?)`,
		dg.ProjectID, dg.Name, dg.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	dg.ID = id
	return id, nil
}

// GetDAGGroup returns a data access group (REQ-DB-028).
func (s *Store) GetDAGGroup(ctx context.Context, id int64) (*DagGroup, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+dagGroupColumns+` FROM dag_groups WHERE id = ?`, id)
	dg, err := scanDagGroup(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &dg, nil
}

// ListDAGGroups returns all data access groups in a project (REQ-DB-028).
func (s *Store) ListDAGGroups(ctx context.Context, projectID int64) ([]DagGroup, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+dagGroupColumns+` FROM dag_groups WHERE project_id = ? ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DagGroup
	for rows.Next() {
		dg, err := scanDagGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dg)
	}
	return out, rows.Err()
}

// UpdateDAGGroup rewrites the mutable name of a DAG (REQ-DB-028).
func (s *Store) UpdateDAGGroup(ctx context.Context, dg *DagGroup) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE dag_groups SET name = ? WHERE id = ?`, dg.Name, dg.ID)
	return err
}

// DeleteDAGGroup removes a data access group (REQ-DB-028).
func (s *Store) DeleteDAGGroup(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM dag_groups WHERE id = ?`, id)
	return err
}

// --- dag_memberships (REQ-DB-028, REQ-AUTH-044) ---

const dagMembershipColumns = `id, assignment_id, group_id, is_active`

func scanDagMembership(row interface{ Scan(dest ...any) error }) (DagMembership, error) {
	var (
		dm      DagMembership
		isActive int
	)
	err := row.Scan(&dm.ID, &dm.AssignmentID, &dm.GroupID, &isActive)
	if err != nil {
		return dm, err
	}
	dm.IsActive = isActive != 0
	return dm, nil
}

// AddDAGMembership links an assignment to a DAG (REQ-DB-028, REQ-AUTH-044).
func (s *Store) AddDAGMembership(ctx context.Context, dm *DagMembership) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO dag_memberships (assignment_id, group_id, is_active) VALUES (?, ?, ?)`,
		dm.AssignmentID, dm.GroupID, boolToInt(dm.IsActive))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	dm.ID = id
	return id, nil
}

// GetDAGMembership returns one DAG membership (REQ-DB-028).
func (s *Store) GetDAGMembership(ctx context.Context, assignmentID, groupID int64) (*DagMembership, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+dagMembershipColumns+` FROM dag_memberships WHERE assignment_id = ? AND group_id = ?`,
		assignmentID, groupID)
	dm, err := scanDagMembership(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &dm, nil
}

// ListDAGMemberships returns all memberships in a DAG (REQ-DB-028).
func (s *Store) ListDAGMemberships(ctx context.Context, groupID int64) ([]DagMembership, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+dagMembershipColumns+` FROM dag_memberships WHERE group_id = ? ORDER BY assignment_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DagMembership
	for rows.Next() {
		dm, err := scanDagMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dm)
	}
	return out, rows.Err()
}

// ListDAGMembershipsByAssignment returns all DAG memberships for an assignment (REQ-DB-028).
func (s *Store) ListDAGMembershipsByAssignment(ctx context.Context, assignmentID int64) ([]DagMembership, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+dagMembershipColumns+` FROM dag_memberships WHERE assignment_id = ? ORDER BY group_id`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DagMembership
	for rows.Next() {
		dm, err := scanDagMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dm)
	}
	return out, rows.Err()
}

// SetDAGMembershipActive activates a DAG membership (REQ-DB-028, REQ-AUTH-044).
func (s *Store) SetDAGMembershipActive(ctx context.Context, assignmentID, groupID int64, active bool) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE dag_memberships SET is_active = ? WHERE assignment_id = ? AND group_id = ?`,
		boolToInt(active), assignmentID, groupID)
	return err
}

// DeleteDAGMembership removes a DAG membership (REQ-DB-028).
func (s *Store) DeleteDAGMembership(ctx context.Context, assignmentID, groupID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM dag_memberships WHERE assignment_id = ? AND group_id = ?`, assignmentID, groupID)
	return err
}

// --- languages (REQ-DB-031, GD-12) ---

const languageColumns = `id, code, display_name, enabled`

func scanLanguage(row interface{ Scan(dest ...any) error }) (Language, error) {
	var (
		lang    Language
		enabled int
	)
	err := row.Scan(&lang.ID, &lang.Code, &lang.DisplayName, &enabled)
	if err != nil {
		return lang, err
	}
	lang.Enabled = enabled != 0
	return lang, nil
}

// CreateLanguage inserts a language (REQ-DB-031, GD-12).
func (s *Store) CreateLanguage(ctx context.Context, lang *Language) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO languages (code, display_name, enabled) VALUES (?, ?, ?)`,
		lang.Code, lang.DisplayName, boolToInt(lang.Enabled))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	lang.ID = id
	return id, nil
}

// GetLanguage returns a language by id (REQ-DB-031).
func (s *Store) GetLanguage(ctx context.Context, id int64) (*Language, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+languageColumns+` FROM languages WHERE id = ?`, id)
	lang, err := scanLanguage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &lang, nil
}

// GetLanguageByCode returns a language by its code (REQ-DB-031).
func (s *Store) GetLanguageByCode(ctx context.Context, code string) (*Language, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+languageColumns+` FROM languages WHERE code = ?`, code)
	lang, err := scanLanguage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &lang, nil
}

// ListLanguages returns all languages (REQ-DB-031).
func (s *Store) ListLanguages(ctx context.Context) ([]Language, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+languageColumns+` FROM languages ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Language
	for rows.Next() {
		lang, err := scanLanguage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lang)
	}
	return out, rows.Err()
}

// UpdateLanguage rewrites a language's mutable attributes (REQ-DB-031).
func (s *Store) UpdateLanguage(ctx context.Context, lang *Language) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE languages SET display_name = ?, enabled = ? WHERE id = ?`,
		lang.DisplayName, boolToInt(lang.Enabled), lang.ID)
	return err
}

// DeleteLanguage removes a language (REQ-DB-031).
func (s *Store) DeleteLanguage(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM languages WHERE id = ?`, id)
	return err
}

// --- i18n_strings (REQ-DB-031, GD-12) ---

// CreateI18nString inserts an i18n string (REQ-DB-031, GD-12).
func (s *Store) CreateI18nString(ctx context.Context, i18n *I18nString) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO i18n_strings (language_id, key, text) VALUES (?, ?, ?)`,
		i18n.LanguageID, i18n.Key, i18n.Text)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	i18n.ID = id
	return id, nil
}

// GetI18nString returns one i18n string (REQ-DB-031).
func (s *Store) GetI18nString(ctx context.Context, languageID int64, key string) (*I18nString, error) {
	var i18n I18nString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, language_id, key, text FROM i18n_strings WHERE language_id = ? AND key = ?`,
		languageID, key).Scan(&i18n.ID, &i18n.LanguageID, &i18n.Key, &i18n.Text)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &i18n, nil
}

// ListI18nStrings returns all strings for a language (REQ-DB-031).
func (s *Store) ListI18nStrings(ctx context.Context, languageID int64) ([]I18nString, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, language_id, key, text FROM i18n_strings WHERE language_id = ? ORDER BY key`, languageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []I18nString
	for rows.Next() {
		var i18n I18nString
		if err := rows.Scan(&i18n.ID, &i18n.LanguageID, &i18n.Key, &i18n.Text); err != nil {
			return nil, err
		}
		out = append(out, i18n)
	}
	return out, rows.Err()
}

// UpdateI18nString rewrites the text of an i18n string (REQ-DB-031).
func (s *Store) UpdateI18nString(ctx context.Context, i18n *I18nString) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE i18n_strings SET text = ? WHERE id = ?`, i18n.Text, i18n.ID)
	return err
}

// DeleteI18nString removes an i18n string (REQ-DB-031).
func (s *Store) DeleteI18nString(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM i18n_strings WHERE id = ?`, id)
	return err
}

// --- audit_events (REQ-DB-021/024) ---

const auditEventColumns = `id, event_type, source, user_id, email, token, project_id,
	arm_num, role, target_record, details, created_at`

func scanAuditEvent(row interface{ Scan(dest ...any) error }) (AuditEvent, error) {
	var (
		ae      AuditEvent
		created any
	)
	err := row.Scan(&ae.ID, &ae.EventType, &ae.Source, &ae.UserID, &ae.Email,
		&ae.Token, &ae.ProjectID, &ae.ArmNum, &ae.Role, &ae.TargetRecord,
		&ae.Details, &created)
	if err != nil {
		return ae, err
	}
	if v, ok := datetimeString(created); ok {
		ae.CreatedAt = v
	}
	return ae, nil
}

// CreateAuditEvent appends an audit log entry (REQ-DB-021, REQ-DB-024).
func (s *Store) CreateAuditEvent(ctx context.Context, ae *AuditEvent) (int64, error) {
	ae.CreatedAt = nowUTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_events (event_type, source, user_id, email, token,
			project_id, arm_num, role, target_record, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ae.EventType, ae.Source, nullInt64(ae.UserID), nullStr(ae.Email),
		nullStr(ae.Token), nullInt64(ae.ProjectID), nullInt64(ae.ArmNum),
		nullStr(ae.Role), nullStr(ae.TargetRecord), nullStr(ae.Details), ae.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	ae.ID = id
	return id, nil
}

// ListAuditEvents returns audit events for a project (REQ-DB-021).
func (s *Store) ListAuditEvents(ctx context.Context, projectID int64) ([]AuditEvent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+auditEventColumns+` FROM audit_events WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		ae, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ae)
	}
	return out, rows.Err()
}

// ListAuditEventsByUser returns audit events for a user (REQ-DB-021).
func (s *Store) ListAuditEventsByUser(ctx context.Context, userID int64) ([]AuditEvent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+auditEventColumns+` FROM audit_events WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		ae, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ae)
	}
	return out, rows.Err()
}

// ListAuditEventsByRecord returns audit events for a specific record (REQ-DB-021).
func (s *Store) ListAuditEventsByRecord(ctx context.Context, projectID int64, targetRecord string) ([]AuditEvent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+auditEventColumns+` FROM audit_events WHERE project_id = ? AND target_record = ? ORDER BY created_at DESC`,
		projectID, targetRecord)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		ae, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ae)
	}
	return out, rows.Err()
}

// --- audit_record_views (REQ-DB-021) ---

const auditRecordViewColumns = `id, user_id, email, token, project_id, record_ids, instruments, created_at`

func scanAuditRecordView(row interface{ Scan(dest ...any) error }) (AuditRecordView, error) {
	var (
		arv     AuditRecordView
		created any
	)
	err := row.Scan(&arv.ID, &arv.UserID, &arv.Email, &arv.Token, &arv.ProjectID,
		&arv.RecordIDs, &arv.Instruments, &created)
	if err != nil {
		return arv, err
	}
	if v, ok := datetimeString(created); ok {
		arv.CreatedAt = v
	}
	return arv, nil
}

// CreateAuditRecordView appends a record-pull audit entry (REQ-DB-021).
func (s *Store) CreateAuditRecordView(ctx context.Context, arv *AuditRecordView) (int64, error) {
	arv.CreatedAt = nowUTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_record_views (user_id, email, token, project_id, record_ids, instruments, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nullInt64(arv.UserID), nullStr(arv.Email), arv.Token, arv.ProjectID,
		arv.RecordIDs, nullStr(arv.Instruments), arv.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	arv.ID = id
	return id, nil
}

// ListAuditRecordViews returns record-pull audit entries for a project (REQ-DB-021).
func (s *Store) ListAuditRecordViews(ctx context.Context, projectID int64) ([]AuditRecordView, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+auditRecordViewColumns+` FROM audit_record_views WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRecordView
	for rows.Next() {
		arv, err := scanAuditRecordView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, arv)
	}
	return out, rows.Err()
}
