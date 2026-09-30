package admin

// Applying a staging set (REQ-API-106/108, API_Endpoints_Design.md §4.21).
//
// Commit turns the staged snapshot back into the live structure tables inside
// one transaction, so the whole set activates at once and data collection never
// sees half a design. Objects are matched by id: a staged row with a live id is
// an update (a changed name is a rename, whose stored values follow it), a
// negative id is an insert, and a live row missing from the snapshot is a
// delete. The counts returned are the objects touched — inserted, updated or
// removed — plus the mapping pairs added or dropped.
//
// Three uniqueness constraints shape the order of writes
// (api/internal/migrations/*/0001_schema.sql): UNIQUE (project_id, field_name)
// and UNIQUE (project_id, name) on instruments mean a rename cycle has to go
// through a provisional name, and UNIQUE (project_id, instrument_id, position)
// means field positions park negative first — the same two-phase write the
// order endpoints use. Renames therefore run in four passes: park the old names
// and their stored values, remove what goes, insert what is new, then settle
// every row on its final name, position and attributes.

import (
	"context"
	"database/sql"
	"strconv"

	"csms/api/internal/validate"
)

// stagedRenamePrefix marks the provisional names a rename parks behind. It is
// not a legal name a caller can choose (field and instrument names are
// identifiers), so it cannot collide with real data.
const stagedRenamePrefix = "__clara_staged_"

// applyStagedDesignTx applies the staged design to the live tables in the
// caller's transaction and reports what changed (REQ-API-106).
func (h *Handler) applyStagedDesignTx(
	ctx context.Context, tx *sql.Tx, projectID int64, active, staged *stagedDesign,
) (appliedCounts, error) {
	a := &applyState{
		ctx: ctx, tx: tx, projectID: projectID,
		armID:   map[int64]int64{},
		eventID: map[int64]int64{},
		instID:  map[int64]int64{},
	}
	for _, x := range active.Arms {
		a.armID[x.ID] = x.ID
	}
	for _, x := range active.Instruments {
		a.instID[x.ID] = x.ID
	}

	if err := a.arms(active, staged); err != nil {
		return a.applied, err
	}
	if err := a.events(active, staged); err != nil {
		return a.applied, err
	}
	if err := a.instrumentsAndFields(active, staged); err != nil {
		return a.applied, err
	}
	if err := a.mapping(active, staged); err != nil {
		return a.applied, err
	}
	return a.applied, nil
}

// applyState carries one commit's transaction, the id translation from snapshot
// ids to live ids, and the running counts.
type applyState struct {
	ctx       context.Context
	tx        *sql.Tx
	projectID int64
	applied   appliedCounts
	armID     map[int64]int64
	eventID   map[int64]int64
	instID    map[int64]int64
}

// --- arms ---

func (a *applyState) arms(active, staged *stagedDesign) error {
	// New arms.
	for _, sa := range staged.Arms {
		if sa.ID >= 0 {
			continue
		}
		if err := a.requireFree(ctxRow{a.ctx, a.tx}, "arms", "arm_num = ?", sa.ArmNum,
			"arm number "+strconv.Itoa(sa.ArmNum)+" already exists"); err != nil {
			return err
		}
		res, err := a.tx.ExecContext(a.ctx,
			`INSERT INTO arms (project_id, arm_num, name, position) VALUES (?, ?, ?, ?)`,
			a.projectID, sa.ArmNum, nullStr(sa.Name), max(sa.Position, 1))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		a.armID[sa.ID] = id
	}
	// Renamed or repositioned arms.
	for _, sa := range staged.Arms {
		old, ok := lookupArm(active, sa.ID)
		if !ok || sa.ID < 0 {
			continue
		}
		if old.Name == sa.Name && old.Position == sa.Position {
			continue
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`UPDATE arms SET name = ?, position = ? WHERE id = ?`,
			nullStr(sa.Name), sa.Position, sa.ID); err != nil {
			return err
		}
		a.armID[sa.ID] = sa.ID
	}
	// Removed arms: an arm that still holds recorded data cannot go (DEV-API-6)
	// — acknowledging a breaking change does not lift that guard, it only warns.
	for _, xa := range active.Arms {
		if _, ok := lookupArm(staged, xa.ID); ok {
			continue
		}
		if held, err := a.holdsData(ctxRow{a.ctx, a.tx}, "unique_event_name", uniqueNames(xa.Events)); err != nil {
			return err
		} else if held {
			return conflictf("arm %d still has recorded data and cannot be deleted", xa.ArmNum)
		}
		if err := a.deleteArmCascade(xa); err != nil {
			return err
		}
	}
	return nil
}

// deleteArmCascade removes an arm with its events and their mapping pairs. The
// events go uncounted here when the whole arm disappears — the set is applied as
// one change either way.
func (a *applyState) deleteArmCascade(xa stagedArm) error {
	names := uniqueNames(xa.Events)
	if len(names) > 0 {
		if _, err := a.tx.ExecContext(a.ctx,
			`DELETE FROM instrument_events WHERE event_id IN
			 (SELECT id FROM events WHERE project_id = ? AND unique_event_name IN (`+placeholders(len(names))+`))`,
			append([]any{a.projectID}, toArgs(names)...)...); err != nil {
			return err
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`DELETE FROM calculated_dependencies WHERE project_id = ? AND ref_unique_event_name IN (`+placeholders(len(names))+`)`,
			append([]any{a.projectID}, toArgs(names)...)...); err != nil {
			return err
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`DELETE FROM events WHERE project_id = ? AND unique_event_name IN (`+placeholders(len(names))+`)`,
			append([]any{a.projectID}, toArgs(names)...)...); err != nil {
			return err
		}
	}
	_, err := a.tx.ExecContext(a.ctx, `DELETE FROM arms WHERE id = ?`, xa.ID)
	return err
}

// --- events ---

func (a *applyState) events(active, staged *stagedDesign) error {
	// Renames park first: UNIQUE (project_id, unique_event_name) makes a swap of
	// two names collide unless both step aside, and the stored values follow the
	// same two-phase route (ASM-API-4).
	type rename struct {
		id       int64
		from, to string
	}
	var renames []rename
	for _, sa := range staged.Arms {
		for _, se := range sa.Events {
			if se.ID < 0 {
				continue
			}
			oldArm, ok := lookupArm(active, sa.ID)
			if !ok {
				continue
			}
			xe, ok := lookupEvent(oldArm, se.ID)
			if !ok || xe.UniqueEventName == se.UniqueEventName {
				continue
			}
			renames = append(renames, rename{id: se.ID, from: xe.UniqueEventName, to: se.UniqueEventName})
		}
	}
	for _, r := range renames {
		temp := stagedRenamePrefix + strconv.FormatInt(r.id, 10)
		if err := a.renameEventNames(r.id, r.from, temp, false); err != nil {
			return err
		}
	}

	// New events.
	for _, sa := range staged.Arms {
		armID, ok := a.armID[sa.ID]
		if !ok {
			continue // an arm that was deleted is not being extended
		}
		for _, se := range sa.Events {
			if se.ID >= 0 {
				continue
			}
			if err := a.requireFree(ctxRow{a.ctx, a.tx}, "events", "unique_event_name = ?", se.UniqueEventName,
				"unique event name '"+se.UniqueEventName+"' already exists"); err != nil {
				return err
			}
			res, err := a.tx.ExecContext(a.ctx,
				`INSERT INTO events (project_id, arm_id, event_name, unique_event_name,
					period, safe_region_start, safe_region_end, position)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				a.projectID, armID, se.EventName, se.UniqueEventName,
				nullInt64Param(se.Period), nullInt64Param(se.SafeRegionStart),
				nullInt64Param(se.SafeRegionEnd), max(se.Position, 1))
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			a.eventID[se.ID] = id
			a.applied.Events++
		}
	}

	// Existing events: settle the parked renames and any other attribute change.
	for _, sa := range staged.Arms {
		oldArm, ok := lookupArm(active, sa.ID)
		if !ok {
			continue
		}
		for _, se := range sa.Events {
			xe, ok := lookupEvent(oldArm, se.ID)
			if !ok || se.ID < 0 {
				continue
			}
			a.eventID[se.ID] = se.ID
			renamed := xe.UniqueEventName != se.UniqueEventName
			if renamed {
				temp := stagedRenamePrefix + strconv.FormatInt(se.ID, 10)
				if err := a.renameEventNames(se.ID, temp, se.UniqueEventName, true); err != nil {
					return err
				}
			}
			if !renamed && xe.EventName == se.EventName && xe.Period == se.Period &&
				xe.SafeRegionStart == se.SafeRegionStart && xe.SafeRegionEnd == se.SafeRegionEnd &&
				xe.Position == se.Position {
				continue
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`UPDATE events SET event_name = ?, unique_event_name = ?,
					period = ?, safe_region_start = ?, safe_region_end = ?, position = ?
				 WHERE id = ?`,
				se.EventName, se.UniqueEventName, nullInt64Param(se.Period),
				nullInt64Param(se.SafeRegionStart), nullInt64Param(se.SafeRegionEnd),
				se.Position, se.ID); err != nil {
				return err
			}
			a.applied.Events++
		}
	}

	// Removed events (their arm survives; the arm itself is handled above).
	for _, xa := range active.Arms {
		sa, armStill := lookupArm(staged, xa.ID)
		if !armStill {
			continue // deleteArmCascade already took its events
		}
		for _, xe := range xa.Events {
			if _, ok := lookupEvent(sa, xe.ID); ok {
				continue
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`DELETE FROM instrument_events WHERE event_id = ?`, xe.ID); err != nil {
				return err
			}
			if _, err := a.tx.ExecContext(a.ctx, `DELETE FROM events WHERE id = ?`, xe.ID); err != nil {
				return err
			}
			a.applied.Events++
		}
	}
	return nil
}

// renameEventNames moves an event's unique name and, on the second pass, the
// stored values and dependency rows that key on it.
func (a *applyState) renameEventNames(eventID int64, from, to string, final bool) error {
	if _, err := a.tx.ExecContext(a.ctx,
		`UPDATE events SET unique_event_name = ? WHERE id = ?`, to, eventID); err != nil {
		return err
	}
	if !final {
		// Park the values too, so a swap of two names cannot overwrite either.
		_, err := a.tx.ExecContext(a.ctx,
			`UPDATE data SET unique_event_name = ? WHERE project_id = ? AND unique_event_name = ?`,
			to, a.projectID, from)
		return err
	}
	if _, err := a.tx.ExecContext(a.ctx,
		`UPDATE data SET unique_event_name = ? WHERE project_id = ? AND unique_event_name = ?`,
		to, a.projectID, from); err != nil {
		return err
	}
	_, err := a.tx.ExecContext(a.ctx,
		`UPDATE calculated_dependencies SET ref_unique_event_name = ?
		 WHERE project_id = ? AND ref_unique_event_name = ?`,
		to, a.projectID, from)
	return err
}

// --- instruments and fields ---

func (a *applyState) instrumentsAndFields(active, staged *stagedDesign) error {
	// Pass 1 — park the renames of both instruments and fields, with the stored
	// values that key on a renamed field.
	for _, si := range staged.Instruments {
		if si.ID < 0 {
			continue
		}
		old, ok := lookupInstrument(active, si.ID)
		if !ok || old.Name == si.Name {
			continue
		}
		temp := stagedRenamePrefix + strconv.FormatInt(si.ID, 10)
		if _, err := a.tx.ExecContext(a.ctx,
			`UPDATE instruments SET name = ? WHERE id = ?`, temp, si.ID); err != nil {
			return err
		}
	}
	type fieldRename struct {
		id       int64
		from, to string
	}
	var fieldRenames []fieldRename
	for _, si := range staged.Instruments {
		oldInst, ok := lookupInstrument(active, si.ID)
		if !ok {
			continue
		}
		for _, sf := range si.Fields {
			if sf.ID < 0 {
				continue
			}
			xf, ok := lookupField(oldInst, sf.ID)
			if !ok || xf.FieldName == sf.FieldName {
				continue
			}
			fieldRenames = append(fieldRenames, fieldRename{id: sf.ID, from: xf.FieldName, to: sf.FieldName})
			temp := stagedRenamePrefix + strconv.FormatInt(sf.ID, 10)
			if _, err := a.tx.ExecContext(a.ctx,
				`UPDATE fields SET field_name = ? WHERE id = ?`, temp, sf.ID); err != nil {
				return err
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`UPDATE data SET field_name = ? WHERE project_id = ? AND field_name = ?`,
				temp, a.projectID, xf.FieldName); err != nil {
				return err
			}
		}
	}

	// Pass 2 — remove what the staged design dropped. Deleting before the new
	// names are applied frees both the name and the (instrument, position) slot.
	if err := a.deleteFieldsAndInstruments(active, staged); err != nil {
		return err
	}

	// Pass 3 — insert new instruments and fields.
	for _, si := range staged.Instruments {
		if si.ID >= 0 {
			continue
		}
		if err := a.requireFree(ctxRow{a.ctx, a.tx}, "instruments", "name = ?", si.Name,
			"instrument '"+si.Name+"' already exists"); err != nil {
			return err
		}
		res, err := a.tx.ExecContext(a.ctx,
			`INSERT INTO instruments (project_id, name, position, is_survey, branching_logic)
			 VALUES (?, ?, ?, ?, ?)`,
			a.projectID, si.Name, max(si.Position, 1), boolToIntParam(si.IsSurvey), nullStr(si.BranchingLogic))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		a.instID[si.ID] = id
		a.applied.Instruments++
	}
	for _, si := range staged.Instruments {
		instID, ok := a.instID[si.ID]
		if !ok {
			continue // a deleted instrument is not being extended
		}
		for _, sf := range si.Fields {
			if sf.ID >= 0 {
				continue
			}
			if err := a.insertField(instID, sf); err != nil {
				return err
			}
			a.applied.Fields++
		}
	}

	// Pass 4 — settle existing rows: parked names and positions to their final
	// values, then the attribute updates. Positions park negative first for
	// UNIQUE (project_id, instrument_id, position).
	if err := a.parkFieldPositions(active, staged); err != nil {
		return err
	}
	for _, si := range staged.Instruments {
		oldInst, ok := lookupInstrument(active, si.ID)
		if !ok || si.ID < 0 {
			continue // new instruments were inserted with their final state
		}
		// Instrument attributes (the name was parked in pass 1).
		if oldInst.Name != si.Name {
			if err := a.requireFreeExcluding(ctxRow{a.ctx, a.tx}, "instruments", "name", si.Name, si.ID,
				"instrument '"+si.Name+"' already exists"); err != nil {
				return err
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`UPDATE instruments SET name = ? WHERE id = ?`, si.Name, si.ID); err != nil {
				return err
			}
			a.applied.Instruments++
		}
		if oldInst.Position != si.Position || oldInst.IsSurvey != si.IsSurvey ||
			oldInst.BranchingLogic != si.BranchingLogic {
			if _, err := a.tx.ExecContext(a.ctx,
				`UPDATE instruments SET name = ?, position = ?, is_survey = ?, branching_logic = ?
				 WHERE id = ?`,
				si.Name, si.Position, boolToIntParam(si.IsSurvey), nullStr(si.BranchingLogic), si.ID); err != nil {
				return err
			}
			a.applied.Instruments++
		}
		a.instID[si.ID] = si.ID

		for _, sf := range si.Fields {
			xf, ok := lookupField(oldInst, sf.ID)
			if !ok || sf.ID < 0 {
				continue
			}
			if err := a.settleField(instIDof(a, si.ID), xf, sf); err != nil {
				return err
			}
		}
	}

	// Stored values follow the field renames that settled in pass 4.
	for _, r := range fieldRenames {
		temp := stagedRenamePrefix + strconv.FormatInt(r.id, 10)
		if _, err := a.tx.ExecContext(a.ctx,
			`UPDATE data SET field_name = ? WHERE project_id = ? AND field_name = ?`,
			r.to, a.projectID, temp); err != nil {
			return err
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`UPDATE calculated_dependencies SET ref_field_name = ?
			 WHERE project_id = ? AND ref_field_name = ?`,
			r.to, a.projectID, temp); err != nil {
			return err
		}
		a.applied.Fields++
	}
	return nil
}

func instIDof(a *applyState, id int64) int64 {
	if live, ok := a.instID[id]; ok {
		return live
	}
	return id
}

// insertField writes one new field of an instrument that already exists live.
func (a *applyState) insertField(instID int64, sf stagedField) error {
	if err := a.requireFree(ctxRow{a.ctx, a.tx}, "fields", "field_name = ?", sf.FieldName,
		"field name '"+sf.FieldName+"' already exists"); err != nil {
		return err
	}
	res, err := a.tx.ExecContext(a.ctx,
		`INSERT INTO fields (project_id, instrument_id, field_name, field_label, field_type,
			section_header, choices, field_note, validation_type, validation_format,
			validation_min, validation_max, required, branching_logic, calculation,
			matrix_group, personal_information, direct_identifier, export_approved, position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.projectID, instID, sf.FieldName, nullStr(sf.FieldLabel), sf.FieldType,
		nullStr(sf.SectionHeader), nullStr(sf.Choices), nullStr(sf.FieldNote),
		nullStr(sf.ValidationType), nullStr(sf.ValidationFormat),
		nullStr(sf.ValidationMin), nullStr(sf.ValidationMax), boolToIntParam(sf.Required),
		nullStr(sf.BranchingLogic), nullStr(sf.Calculation), nullStr(sf.MatrixGroup),
		boolToIntParam(sf.PersonalInformation), boolToIntParam(sf.DirectIdentifier),
		boolToIntParam(sf.ExportApproved), max(sf.Position, 1))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	// A calculated field's dependency rows come from its expression (§6.2).
	if sf.FieldType == "calculated" && sf.Calculation.Valid && sf.Calculation.String != "" {
		refs, err := validate.ParseCalcExpression(sf.Calculation.String)
		if err == nil {
			if err := putCalcDepsTx(a.ctx, a.tx, a.projectID, id, refs); err != nil {
				return err
			}
		}
	}
	return nil
}

// settleField writes an existing field's final name, position and attributes.
func (a *applyState) settleField(instID int64, old, next stagedField) error {
	if old == next {
		return nil
	}
	calcChanged := next.FieldType == "calculated" && next.Calculation != old.Calculation
	if _, err := a.tx.ExecContext(a.ctx,
		`UPDATE fields SET field_name = ?, field_label = ?, field_type = ?,
			section_header = ?, choices = ?, field_note = ?,
			validation_type = ?, validation_format = ?, validation_min = ?, validation_max = ?,
			required = ?, branching_logic = ?, calculation = ?, matrix_group = ?,
			personal_information = ?, direct_identifier = ?, export_approved = ?, position = ?
		 WHERE id = ?`,
		next.FieldName, nullStr(next.FieldLabel), next.FieldType,
		nullStr(next.SectionHeader), nullStr(next.Choices), nullStr(next.FieldNote),
		nullStr(next.ValidationType), nullStr(next.ValidationFormat),
		nullStr(next.ValidationMin), nullStr(next.ValidationMax), boolToIntParam(next.Required),
		nullStr(next.BranchingLogic), nullStr(next.Calculation), nullStr(next.MatrixGroup),
		boolToIntParam(next.PersonalInformation), boolToIntParam(next.DirectIdentifier),
		boolToIntParam(next.ExportApproved), next.Position, next.ID); err != nil {
		return err
	}
	a.applied.Fields++
	if calcChanged {
		var refs []validate.Ref
		if next.Calculation.Valid && next.Calculation.String != "" {
			parsed, err := validate.ParseCalcExpression(next.Calculation.String)
			if err != nil {
				return conflictf("the staged expression for %s is not well-formed: %s", next.FieldName, err)
			}
			refs = parsed
		}
		if err := putCalcDepsTx(a.ctx, a.tx, a.projectID, next.ID, refs); err != nil {
			return err
		}
	}
	return nil
}

// parkFieldPositions moves every field of the touched instruments to a negative
// position, so the final ones can be written without colliding on
// UNIQUE (project_id, instrument_id, position).
func (a *applyState) parkFieldPositions(active, staged *stagedDesign) error {
	ids := map[int64]bool{}
	for _, si := range staged.Instruments {
		if _, ok := a.instID[si.ID]; !ok {
			continue // new instruments insert their fields at the final position
		}
		for _, sf := range si.Fields {
			if sf.ID >= 0 {
				ids[sf.ID] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	list := make([]any, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	_, err := a.tx.ExecContext(a.ctx,
		`UPDATE fields SET position = -ABS(position) - 1 WHERE project_id = ? AND id IN (`+placeholders(len(list))+`)`,
		append([]any{a.projectID}, list...)...)
	return err
}

// deleteFieldsAndInstruments removes the rows the staged design dropped. A field
// an expression in the staged design still names cannot go, exactly as on the
// live path; deleting a field takes its stored values with it (DEV-API-5).
func (a *applyState) deleteFieldsAndInstruments(active, staged *stagedDesign) error {
	remaining := flattenFields(staged)
	for _, si := range active.Instruments {
		_, instKept := lookupInstrument(staged, si.ID)
		for _, xf := range si.Fields {
			if _, keep := remaining[xf.ID]; keep && instKept {
				continue
			}
			if referenced, err := designReferencesField(staged, xf.FieldName); err != nil {
				return err
			} else if referenced {
				return conflictf("field %s is referenced by an active expression", xf.FieldName)
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`DELETE FROM data WHERE project_id = ? AND field_name = ?`,
				a.projectID, xf.FieldName); err != nil {
				return err
			}
			if _, err := a.tx.ExecContext(a.ctx,
				`DELETE FROM calculated_dependencies WHERE project_id = ? AND calculated_field_id = ?`,
				a.projectID, xf.ID); err != nil {
				return err
			}
			if _, err := a.tx.ExecContext(a.ctx, `DELETE FROM fields WHERE id = ?`, xf.ID); err != nil {
				return err
			}
			a.applied.Fields++
		}
	}
	// Instruments the staged design dropped (their fields are gone by now).
	for _, si := range active.Instruments {
		if _, ok := lookupInstrument(staged, si.ID); ok {
			continue
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`DELETE FROM instrument_events WHERE instrument_id = ?`, si.ID); err != nil {
			return err
		}
		if _, err := a.tx.ExecContext(a.ctx, `DELETE FROM instruments WHERE id = ?`, si.ID); err != nil {
			return err
		}
		a.applied.Instruments++
	}
	return nil
}

// --- instrument–event mapping ---

func (a *applyState) mapping(active, staged *stagedDesign) error {
	before := pairSet(active)
	after := pairSet(staged)
	added, removed := 0, 0
	for k := range after {
		if _, ok := before[k]; !ok {
			added++
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed++
		}
	}
	a.applied.MappingPairs = added + removed

	// Rebuild each surviving arm's pairs from the staged matrix, translating the
	// snapshot ids the set introduced into their live ids.
	for _, sa := range staged.Arms {
		armID, ok := a.armID[sa.ID]
		if !ok {
			continue
		}
		if _, err := a.tx.ExecContext(a.ctx,
			`DELETE FROM instrument_events
			 WHERE event_id IN (SELECT id FROM events WHERE project_id = ? AND arm_id = ?)`,
			a.projectID, armID); err != nil {
			return err
		}
		for _, si := range staged.Instruments {
			instID, ok := a.instID[si.ID]
			if !ok {
				continue
			}
			for _, unique := range staged.Mapping[sa.ArmNum][si.Name] {
				se, ok := lookupEventByName(sa, unique)
				if !ok {
					continue
				}
				eventID, ok := a.eventID[se.ID]
				if !ok {
					continue
				}
				if _, err := a.tx.ExecContext(a.ctx,
					`INSERT INTO instrument_events (instrument_id, event_id) VALUES (?, ?)`,
					instID, eventID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// --- guards and small helpers ---

// ctxRow bundles the context and transaction a guard runs in.
type ctxRow struct {
	ctx context.Context
	tx  *sql.Tx
}

// requireFree rejects a value that another row already carries (the UNIQUE
// constraints of §5 — reported as a conflict, not an internal error).
func (a *applyState) requireFree(q ctxRow, table, predicate string, value any, message string) error {
	var one int
	err := q.tx.QueryRowContext(q.ctx,
		`SELECT 1 FROM `+table+` WHERE project_id = ? AND `+predicate+` LIMIT 1`,
		a.projectID, value).Scan(&one)
	if err == nil {
		return conflictf("%s", message)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

// requireFreeExcluding is requireFree with one row allowed to hold the value.
func (a *applyState) requireFreeExcluding(q ctxRow, table, column string, value any, exceptID int64, message string) error {
	var one int
	err := q.tx.QueryRowContext(q.ctx,
		`SELECT 1 FROM `+table+` WHERE project_id = ? AND `+column+` = ? AND id <> ? LIMIT 1`,
		a.projectID, value, exceptID).Scan(&one)
	if err == nil {
		return conflictf("%s", message)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

// holdsData reports whether the data table carries a row for any of these names.
func (a *applyState) holdsData(q ctxRow, column string, names []string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	var one int
	err := q.tx.QueryRowContext(q.ctx,
		`SELECT 1 FROM data WHERE project_id = ? AND `+column+` IN (`+placeholders(len(names))+`) LIMIT 1`,
		append([]any{a.projectID}, toArgs(names)...)...).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// exprNamesField reports whether one stored expression (calculated field or
// branching logic) references the given field.
func exprNamesField(expr, fieldName string) bool {
	if refs, err := validate.ParseCalcExpression(expr); err == nil {
		for _, r := range refs {
			if r.Field == fieldName {
				return true
			}
		}
		return false
	}
	if refs, err := validate.ParseBranchingRefs(expr); err == nil {
		for _, r := range refs {
			if r.Field == fieldName {
				return true
			}
		}
	}
	return false
}

// designReferencesField reports whether any calculated or branching expression
// of the staged design names this field — the same check the live delete makes,
// evaluated against the design that is about to become active.
func designReferencesField(d *stagedDesign, fieldName string) (bool, error) {
	for _, si := range d.Instruments {
		if si.BranchingLogic.Valid && exprNamesField(si.BranchingLogic.String, fieldName) {
			return true, nil
		}
		for _, sf := range si.Fields {
			if sf.Calculation.Valid && exprNamesField(sf.Calculation.String, fieldName) {
				return true, nil
			}
			if sf.BranchingLogic.Valid && exprNamesField(sf.BranchingLogic.String, fieldName) {
				return true, nil
			}
		}
	}
	return false, nil
}

func lookupField(i stagedInstrument, id int64) (stagedField, bool) {
	for _, f := range i.Fields {
		if f.ID == id {
			return f, true
		}
	}
	return stagedField{}, false
}

func lookupEventByName(a stagedArm, unique string) (stagedEvent, bool) {
	for _, e := range a.Events {
		if e.UniqueEventName == unique {
			return e, true
		}
	}
	return stagedEvent{}, false
}

func placeholders(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += "?"
	}
	return out
}

func toArgs(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
