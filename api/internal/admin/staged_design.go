package admin

// Staged-design snapshot (GD-20, REQ-DB-035, API_Endpoints_Design.md §4.21).
//
// A staging set holds the whole project design as one JSON document in
// project_staging.design: instruments with their fields, arms with their
// events, and the instrument–event mapping keyed arm_num → instrument name →
// unique event names (the shape sketched in Database_Schema_Design.md §5). The
// live structure tables stay untouched while a set is open, which is what lets
// data collection keep running on the active design (REQ-API-107).
//
// Every object carries the id it has in the live tables. An object created
// while a set is open has no live row yet and takes a provisional negative id
// from next_id, which is what makes the commit diff unambiguous: a staged
// field with a live id and a different field_name is a rename (its stored
// values follow in the same transaction, REQ-VAL-014), never a delete plus an
// add. Provisional ids are visible in responses until the set commits and the
// client refetches.
//
// The document is internal to the API — no endpoint echoes it verbatim; the
// structure endpoints render their usual objects from it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// stagedField is one fields row (db.Field without the project id, which the
// document itself supplies). The sql.Null* column types marshal to a value or
// JSON null, so the snapshot round-trips every column faithfully.
type stagedField struct {
	ID                  int64          `json:"id"`
	InstrumentID        int64          `json:"instrument_id"`
	FieldName           string         `json:"field_name"`
	FieldLabel          sql.NullString `json:"field_label"`
	FieldType           string         `json:"field_type"`
	SectionHeader       sql.NullString `json:"section_header"`
	Choices             sql.NullString `json:"choices"`
	FieldNote           sql.NullString `json:"field_note"`
	ValidationType      sql.NullString `json:"validation_type"`
	ValidationFormat    sql.NullString `json:"validation_format"`
	ValidationMin       sql.NullString `json:"validation_min"`
	ValidationMax       sql.NullString `json:"validation_max"`
	Required            bool           `json:"required"`
	BranchingLogic      sql.NullString `json:"branching_logic"`
	Calculation         sql.NullString `json:"calculation"`
	MatrixGroup         sql.NullString `json:"matrix_group"`
	PersonalInformation bool           `json:"personal_information"`
	DirectIdentifier    bool           `json:"direct_identifier"`
	ExportApproved      bool           `json:"export_approved"`
	Position            int            `json:"position"`
}

// stagedInstrument is one instruments row with its fields nested (the §5 shape).
type stagedInstrument struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Position       int            `json:"position"`
	IsSurvey       bool           `json:"is_survey"`
	BranchingLogic sql.NullString `json:"branching_logic"`
	Fields         []stagedField  `json:"fields"`
}

// stagedEvent is one events row; ArmID is implied by the enclosing arm.
type stagedEvent struct {
	ID              int64         `json:"id"`
	EventName       string        `json:"event_name"`
	UniqueEventName string        `json:"unique_event_name"`
	Period          sql.NullInt64 `json:"period"`
	SafeRegionStart sql.NullInt64 `json:"safe_region_start"`
	SafeRegionEnd   sql.NullInt64 `json:"safe_region_end"`
	Position        int           `json:"position"`
}

// stagedArm is one arms row with its events nested.
type stagedArm struct {
	ID       int64          `json:"id"`
	ArmNum   int            `json:"arm_num"`
	Name     sql.NullString `json:"name"`
	Position int            `json:"position"`
	Events   []stagedEvent  `json:"events"`
}

// stagedDesign is the document: instruments, arms with events, and the mapping
// (arm_num → instrument name → unique event names). NextID allocates the
// provisional ids of objects created while the set is open (descending from
// -1, so they can never collide with a live id).
type stagedDesign struct {
	Instruments []stagedInstrument          `json:"instruments"`
	Arms        []stagedArm                 `json:"arms"`
	Mapping     map[int]map[string][]string `json:"mapping"`
	NextID      int64                       `json:"next_id"`
}

// --- conversion to and from the storage rows ---

func stageField(f db.Field) stagedField {
	return stagedField{
		ID: f.ID, InstrumentID: f.InstrumentID, FieldName: f.FieldName,
		FieldLabel: f.FieldLabel, FieldType: f.FieldType, SectionHeader: f.SectionHeader,
		Choices: f.Choices, FieldNote: f.FieldNote,
		ValidationType: f.ValidationType, ValidationFormat: f.ValidationFormat,
		ValidationMin: f.ValidationMin, ValidationMax: f.ValidationMax,
		Required: f.Required, BranchingLogic: f.BranchingLogic, Calculation: f.Calculation,
		MatrixGroup: f.MatrixGroup, PersonalInformation: f.PersonalInformation,
		DirectIdentifier: f.DirectIdentifier, ExportApproved: f.ExportApproved,
		Position: f.Position,
	}
}

// field restamps a staged row as a db.Field for the shared validation and
// response-shaping code, which all work on storage types.
func (s stagedField) field(projectID int64) db.Field {
	return db.Field{
		ID: s.ID, ProjectID: projectID, InstrumentID: s.InstrumentID, FieldName: s.FieldName,
		FieldLabel: s.FieldLabel, FieldType: s.FieldType, SectionHeader: s.SectionHeader,
		Choices: s.Choices, FieldNote: s.FieldNote,
		ValidationType: s.ValidationType, ValidationFormat: s.ValidationFormat,
		ValidationMin: s.ValidationMin, ValidationMax: s.ValidationMax,
		Required: s.Required, BranchingLogic: s.BranchingLogic, Calculation: s.Calculation,
		MatrixGroup: s.MatrixGroup, PersonalInformation: s.PersonalInformation,
		DirectIdentifier: s.DirectIdentifier, ExportApproved: s.ExportApproved,
		Position: s.Position,
	}
}

func stageInstrument(i db.Instrument) stagedInstrument {
	return stagedInstrument{
		ID: i.ID, Name: i.Name, Position: i.Position,
		IsSurvey: i.IsSurvey, BranchingLogic: i.BranchingLogic,
		Fields: []stagedField{},
	}
}

func (s stagedInstrument) instrument(projectID int64) db.Instrument {
	return db.Instrument{
		ID: s.ID, ProjectID: projectID, Name: s.Name, Position: s.Position,
		IsSurvey: s.IsSurvey, BranchingLogic: s.BranchingLogic,
	}
}

func stageEvent(e db.Event) stagedEvent {
	return stagedEvent{
		ID: e.ID, EventName: e.EventName, UniqueEventName: e.UniqueEventName,
		Period: e.Period, SafeRegionStart: e.SafeRegionStart, SafeRegionEnd: e.SafeRegionEnd,
		Position: e.Position,
	}
}

func (s stagedEvent) event(projectID, armID int64) db.Event {
	return db.Event{
		ID: s.ID, ProjectID: projectID, ArmID: armID, EventName: s.EventName,
		UniqueEventName: s.UniqueEventName, Period: s.Period,
		SafeRegionStart: s.SafeRegionStart, SafeRegionEnd: s.SafeRegionEnd,
		Position: s.Position,
	}
}

func stageArm(a db.Arm) stagedArm {
	return stagedArm{ID: a.ID, ArmNum: a.ArmNum, Name: a.Name, Position: a.Position, Events: []stagedEvent{}}
}

func (s stagedArm) arm(projectID int64) db.Arm {
	return db.Arm{ID: s.ID, ProjectID: projectID, ArmNum: s.ArmNum, Name: s.Name, Position: s.Position}
}

// --- load, parse, encode ---

// stagedDesignOf snapshots a project's live design (POST …/staging, and the
// active side of every diff). Instruments come back by position with their
// fields by position, arms by arm_num — the order the structure listings use,
// so a read answered from the snapshot matches one answered from the tables.
func (h *Handler) stagedDesignOf(ctx context.Context, projectID int64) (*stagedDesign, error) {
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		return nil, err
	}
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		return nil, err
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}

	d := &stagedDesign{
		Instruments: make([]stagedInstrument, 0, len(instruments)),
		Arms:        make([]stagedArm, 0, len(arms)),
		Mapping:     map[int]map[string][]string{},
		NextID:      -1,
	}
	byInstrument := map[int64][]stagedField{}
	for _, f := range fields {
		byInstrument[f.InstrumentID] = append(byInstrument[f.InstrumentID], stageField(f))
	}
	for _, i := range instruments {
		si := stageInstrument(i)
		si.Fields = append(si.Fields, sortedFields(byInstrument[i.ID])...)
		d.Instruments = append(d.Instruments, si)
	}
	byArm := map[int64][]stagedEvent{}
	for _, e := range events {
		byArm[e.ArmID] = append(byArm[e.ArmID], stageEvent(e))
	}
	armNumByID := map[int64]int{}
	for _, a := range arms {
		sa := stageArm(a)
		armNumByID[a.ID] = a.ArmNum
		sa.Events = append(sa.Events, byArm[a.ID]...)
		d.Arms = append(d.Arms, sa)
	}
	// Mapping as instrument name → unique event names per arm (§4.12 shape).
	eventUnique := map[int64]string{}
	armIDByEvent := map[int64]int64{}
	for _, e := range events {
		eventUnique[e.ID] = e.UniqueEventName
		armIDByEvent[e.ID] = e.ArmID
	}
	instName := map[int64]string{}
	for _, i := range instruments {
		instName[i.ID] = i.Name
	}
	for _, p := range pairs {
		armNum, ok := armNumByID[armIDByEvent[p.EventID]]
		if !ok {
			continue
		}
		name, ok := instName[p.InstrumentID]
		if !ok {
			continue
		}
		unique, ok := eventUnique[p.EventID]
		if !ok {
			continue
		}
		if d.Mapping[armNum] == nil {
			d.Mapping[armNum] = map[string][]string{}
		}
		d.Mapping[armNum][name] = append(d.Mapping[armNum][name], unique)
	}
	for armNum := range d.Mapping {
		for name := range d.Mapping[armNum] {
			d.Mapping[armNum][name] = sortedUniqueNames(d.Mapping[armNum][name], eventsOfArm(d.Arms, armNum))
		}
	}
	return d, nil
}

// parseStagedDesign decodes a stored snapshot. A document missing its mapping
// or object lists is treated as empty rather than rejected: the row is written
// by this package only, so a short read is an old or truncated value, and
// failing loudly on commit would be worse than treating it as no change.
func parseStagedDesign(doc string) (*stagedDesign, error) {
	var d stagedDesign
	if err := json.Unmarshal([]byte(doc), &d); err != nil {
		return nil, err
	}
	if d.Mapping == nil {
		d.Mapping = map[int]map[string][]string{}
	}
	if d.NextID == 0 {
		d.NextID = -1
	}
	return &d, nil
}

// encode renders the snapshot for project_staging.design.
func (d *stagedDesign) encode() (string, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// clone copies the document. The analysis-mode guard needs a second design to
// apply one pending edit to (classify the result, then throw it away unless the
// change is accepted), and the mutators work through pointers into slices, so a
// shallow copy would not do.
func (d *stagedDesign) clone() (*stagedDesign, error) {
	doc, err := d.encode()
	if err != nil {
		return nil, err
	}
	return parseStagedDesign(doc)
}

// newID allocates a provisional id for an object created while the set is open.
func (d *stagedDesign) newID() int64 {
	id := d.NextID
	d.NextID--
	return id
}

// --- accessors ---

// instrument returns the nested instrument with this id.
func (d *stagedDesign) instrument(id int64) (*stagedInstrument, bool) {
	for i := range d.Instruments {
		if d.Instruments[i].ID == id {
			return &d.Instruments[i], true
		}
	}
	return nil, false
}

// instrumentByName resolves an instrument by its unique name (REQ-DB-011).
func (d *stagedDesign) instrumentByName(name string) (*stagedInstrument, bool) {
	for i := range d.Instruments {
		if d.Instruments[i].Name == name {
			return &d.Instruments[i], true
		}
	}
	return nil, false
}

// field returns the field with this id, wherever it sits.
func (d *stagedDesign) field(id int64) (*stagedField, bool) {
	for i := range d.Instruments {
		f := d.Instruments[i].Fields
		for j := range f {
			if f[j].ID == id {
				return &f[j], true
			}
		}
	}
	return nil, false
}

// fieldByName resolves a field by its project-unique name (UNIQUE
// (project_id, field_name)).
func (d *stagedDesign) fieldByName(name string) (*stagedField, bool) {
	for i := range d.Instruments {
		f := d.Instruments[i].Fields
		for j := range f {
			if f[j].FieldName == name {
				return &f[j], true
			}
		}
	}
	return nil, false
}

// arm returns the arm with this number.
func (d *stagedDesign) arm(armNum int) (*stagedArm, bool) {
	for i := range d.Arms {
		if d.Arms[i].ArmNum == armNum {
			return &d.Arms[i], true
		}
	}
	return nil, false
}

// armByID returns the arm with this id (the id is what events carry).
func (d *stagedDesign) armByID(id int64) (*stagedArm, bool) {
	for i := range d.Arms {
		if d.Arms[i].ID == id {
			return &d.Arms[i], true
		}
	}
	return nil, false
}

// event returns the event with this id.
func (d *stagedDesign) event(id int64) (*stagedEvent, *stagedArm, bool) {
	for i := range d.Arms {
		ev := d.Arms[i].Events
		for j := range ev {
			if ev[j].ID == id {
				return &ev[j], &d.Arms[i], true
			}
		}
	}
	return nil, nil, false
}

// eventByUniqueName resolves an event by its project-unique name.
func (d *stagedDesign) eventByUniqueName(unique string) (*stagedEvent, *stagedArm, bool) {
	for i := range d.Arms {
		ev := d.Arms[i].Events
		for j := range ev {
			if ev[j].UniqueEventName == unique {
				return &ev[j], &d.Arms[i], true
			}
		}
	}
	return nil, nil, false
}

// nextArmNum is the 1-based arm number a new arm takes (the live path uses the
// same rule: max + 1).
func (d *stagedDesign) nextArmNum() int {
	next := 1
	for _, a := range d.Arms {
		if a.ArmNum >= next {
			next = a.ArmNum + 1
		}
	}
	return next
}

// uniqueEventName derives <label>_arm_<n> (REQ-DB-011).
func uniqueEventName(label string, armNum int) string {
	return label + "_arm_" + strconv.Itoa(armNum)
}

// mapped reports whether an instrument is checked for one event of an arm.
func (d *stagedDesign) mapped(armNum int, instrument string, uniqueEvent string) bool {
	for _, u := range d.Mapping[armNum][instrument] {
		if u == uniqueEvent {
			return true
		}
	}
	return false
}

// identifierFieldID is the record identifier: the first field of the first
// instrument by position (GD-8). 0 when the design has no fields yet.
func (d *stagedDesign) identifierFieldID() int64 {
	for _, i := range sortedInstruments(d.Instruments) {
		fields := sortedFields(i.Fields)
		if len(fields) > 0 {
			return fields[0].ID
		}
	}
	return 0
}

// --- flattened views in the listings' own order ---

// dbInstruments returns the instruments ordered by position, as ListInstruments does.
func (d *stagedDesign) dbInstruments(projectID int64) []db.Instrument {
	out := make([]db.Instrument, 0, len(d.Instruments))
	for _, i := range sortedInstruments(d.Instruments) {
		out = append(out, i.instrument(projectID))
	}
	return out
}

// dbFields returns every field, instruments by position and fields by position
// within them — the order ListFields produces.
func (d *stagedDesign) dbFields(projectID int64) []db.Field {
	var out []db.Field
	for _, i := range sortedInstruments(d.Instruments) {
		for _, f := range sortedFields(i.Fields) {
			out = append(out, f.field(projectID))
		}
	}
	return out
}

// dbArms returns the arms by arm_num, as ListArms does.
func (d *stagedDesign) dbArms(projectID int64) []db.Arm {
	out := make([]db.Arm, 0, len(d.Arms))
	arms := make([]stagedArm, len(d.Arms))
	copy(arms, d.Arms)
	sort.SliceStable(arms, func(i, j int) bool { return arms[i].ArmNum < arms[j].ArmNum })
	for _, a := range arms {
		out = append(out, a.arm(projectID))
	}
	return out
}

// dbEvents returns every event, by arm number then position.
func (d *stagedDesign) dbEvents(projectID int64) []db.Event {
	var out []db.Event
	for _, a := range d.Arms {
		for _, e := range sortedEvents(a.Events) {
			out = append(out, e.event(projectID, a.ID))
		}
	}
	return out
}

// dbPairs resolves the mapping back to (instrument id, event id) pairs. An
// entry naming an instrument or event that no longer exists is dropped: the
// snapshot stays internally consistent because every mutator prunes it, and a
// stale name from a hand-edited document must not invent a pair.
func (d *stagedDesign) dbPairs(projectID int64) []db.InstrumentEvent {
	var out []db.InstrumentEvent
	for _, i := range sortedInstruments(d.Instruments) {
		var pairs []db.InstrumentEvent
		for _, a := range d.Arms {
			for _, u := range d.Mapping[a.ArmNum][i.Name] {
				if e, ok := eventInArm(a, u); ok {
					pairs = append(pairs, db.InstrumentEvent{InstrumentID: i.ID, EventID: e.ID})
				}
			}
		}
		out = append(out, pairs...)
	}
	return out
}

// --- the shared validation context ---

// designContext builds the data-dictionary view the §6.2/§7.2 reference rules
// are checked against, from the snapshot instead of the tables. The result is
// the same designCtx the live path loads, so validateCalc, resolves and
// checkAcyclic apply identically to a staged edit.
func (d *stagedDesign) designContext(projectID int64) *designCtx {
	fields := d.dbFields(projectID)
	events := d.dbEvents(projectID)
	ctx := &designCtx{
		fields:   make(map[string]db.Field, len(fields)),
		events:   make(map[string]db.Event, len(events)),
		activeAt: map[string]map[int64]bool{},
	}
	for _, f := range fields {
		ctx.fields[f.FieldName] = f
	}
	for _, e := range events {
		ctx.events[e.UniqueEventName] = e
	}
	// First event in canonical order (GD-15) — the fallback target of a bare
	// [field] reference; arms ascending, within an arm the GD-15 order.
	snapshotArms := make([]stagedArm, len(d.Arms))
	copy(snapshotArms, d.Arms)
	sort.SliceStable(snapshotArms, func(i, j int) bool { return snapshotArms[i].ArmNum < snapshotArms[j].ArmNum })
	for _, a := range snapshotArms {
		if len(a.Events) == 0 {
			continue
		}
		evs := make([]db.Event, 0, len(a.Events))
		for _, e := range a.Events {
			evs = append(evs, e.event(projectID, a.ID))
		}
		SortEventsCanonical(evs)
		ctx.firstEvent = evs[0].UniqueEventName
		break
	}
	for _, p := range d.dbPairs(projectID) {
		var unique string
		for _, e := range events {
			if e.ID == p.EventID {
				unique = e.UniqueEventName
				break
			}
		}
		if unique == "" {
			continue
		}
		if ctx.activeAt[unique] == nil {
			ctx.activeAt[unique] = map[int64]bool{}
		}
		ctx.activeAt[unique][p.InstrumentID] = true
	}
	// Dependencies are re-derived from the stored expressions rather than
	// snapshotted: the snapshot carries the expressions, so a staged edit can
	// never leave the two out of step.
	for _, f := range fields {
		if f.FieldType != "calculated" || !f.Calculation.Valid || f.Calculation.String == "" {
			continue
		}
		refs, err := validate.ParseCalcExpression(f.Calculation.String)
		if err != nil {
			continue // stored expressions parse; a broken one names nothing
		}
		for _, ref := range refs {
			ctx.deps = append(ctx.deps, db.CalculatedDependency{
				ProjectID: projectID, CalculatedFieldID: f.ID,
				RefUniqueEventName: ref.Event, RefFieldName: ref.Field,
			})
		}
	}
	return ctx
}

// --- rendering the §4.8–§4.12 objects from a snapshot ---
//
// While a staging set is open the structure endpoints answer from it (decision:
// one design per project for every reader), so each listing needs a snapshot
// twin that produces byte-identical objects — same order, same fields — to the
// live query.

// armObjects renders §4.8: each arm with its events in the canonical GD-15 order.
func (d *stagedDesign) armObjects() []armObject {
	out := make([]armObject, 0, len(d.Arms))
	for _, a := range d.armsByNum() {
		events := canonicalStagedEvents(a.Events)
		objs := make([]armEventObject, 0, len(events))
		for _, e := range events {
			o := stagedEventObject(e, a.ArmNum)
			objs = append(objs, armEventObject{
				ID: o.ID, EventName: o.EventName, UniqueEventName: o.UniqueEventName,
				Period: o.Period, SafeRegionStart: o.SafeRegionStart,
				SafeRegionEnd: o.SafeRegionEnd, Position: o.Position,
			})
		}
		out = append(out, armObject{ID: a.ID, ArmNum: a.ArmNum, Name: a.Name.String, Events: objs})
	}
	return out
}

// eventObjects renders §4.9: every event in canonical per-arm order, stamped
// with its arm number.
func (d *stagedDesign) eventObjects() []EventObject {
	out := make([]EventObject, 0)
	for _, a := range d.armsByNum() {
		for _, e := range canonicalStagedEvents(a.Events) {
			out = append(out, stagedEventObject(e, a.ArmNum))
		}
	}
	return out
}

// instrumentObjects renders §4.10 in position order with field counts.
func (d *stagedDesign) instrumentObjects() []instrumentObject {
	out := make([]instrumentObject, 0, len(d.Instruments))
	for _, i := range sortedInstruments(d.Instruments) {
		out = append(out, instrumentObject{
			ID: i.ID, Name: i.Name, Position: i.Position, FieldCount: len(i.Fields),
			IsSurvey: i.IsSurvey, BranchingLogic: i.BranchingLogic.String,
		})
	}
	return out
}

// instrumentObjectFor renders one §4.10 instrument with its staged field count —
// what create and update answer with, in place of the live path's fieldCounts
// lookup, so a staged response reports the set's field count rather than the
// active design's.
func (d *stagedDesign) instrumentObjectFor(instID int64) (instrumentObject, bool) {
	si, ok := d.instrument(instID)
	if !ok {
		return instrumentObject{}, false
	}
	return instrumentObject{
		ID: si.ID, Name: si.Name, Position: si.Position, FieldCount: len(si.Fields),
		IsSurvey: si.IsSurvey, BranchingLogic: si.BranchingLogic.String,
	}, true
}

// isFirstInstrument reports whether instID is the instrument at position 1 —
// the one whose first field is the record identifier (GD-8).
func (d *stagedDesign) isFirstInstrument(instID int64) bool {
	sorted := sortedInstruments(d.Instruments)
	return len(sorted) > 0 && sorted[0].ID == instID
}

// fieldObjects renders §4.11 for one instrument, in position order. The objects
// come from the same newFieldObject the live listing uses, so a staged read is
// indistinguishable from an unstaged one.
func (d *stagedDesign) fieldObjects(projectID, instID int64) []fieldObject {
	out := make([]fieldObject, 0)
	inst, ok := d.instrument(instID)
	if !ok {
		return out
	}
	for _, f := range sortedFields(inst.Fields) {
		out = append(out, newFieldObject(f.field(projectID)))
	}
	return out
}

// mappingObjects renders the §4.12 instrument × event matrix per arm.
func (d *stagedDesign) mappingObjects() []armMappingObject {
	out := make([]armMappingObject, 0, len(d.Arms))
	for _, a := range d.armsByNum() {
		order := make([]string, 0, len(a.Events))
		for _, e := range canonicalStagedEvents(a.Events) {
			order = append(order, e.UniqueEventName)
		}
		mapping := make(map[string][]string, len(d.Instruments))
		for _, i := range sortedInstruments(d.Instruments) {
			checked := []string{}
			for _, u := range order {
				if d.mapped(a.ArmNum, i.Name, u) {
					checked = append(checked, u)
				}
			}
			mapping[i.Name] = checked
		}
		out = append(out, armMappingObject{ArmNum: a.ArmNum, Mapping: mapping})
	}
	return out
}

// armsByNum returns the arms by arm number, as ListArms orders them.
func (d *stagedDesign) armsByNum() []stagedArm {
	out := make([]stagedArm, len(d.Arms))
	copy(out, d.Arms)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ArmNum < out[j].ArmNum })
	return out
}

// canonicalStagedEvents applies the GD-15 ordering to a snapshot's events:
// timepoint events first by period (ties by position), then the rest by position.
func canonicalStagedEvents(in []stagedEvent) []stagedEvent {
	out := make([]stagedEvent, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Period.Valid != b.Period.Valid {
			return a.Period.Valid
		}
		if a.Period.Valid && a.Period.Int64 != b.Period.Int64 {
			return a.Period.Int64 < b.Period.Int64
		}
		return a.Position < b.Position
	})
	return out
}

// stagedEventObject renders one snapshot event as the §4.9 object, stamped with
// its arm number — the same shape NewEventObject produces from a live row.
func stagedEventObject(e stagedEvent, armNum int) EventObject {
	return EventObject{
		ID: e.ID, ArmNum: armNum, EventName: e.EventName, UniqueEventName: e.UniqueEventName,
		Period:          Int64Ptr(e.Period),
		SafeRegionStart: Int64Ptr(e.SafeRegionStart),
		SafeRegionEnd:   Int64Ptr(e.SafeRegionEnd),
		Position:        e.Position,
	}
}

// --- mutators ---
//
// Each mutator applies one structure change to the document and nothing else:
// the handler keeps owning decode, permission, design validation and audit,
// exactly as it does on the live path.

// putInstrument inserts or replaces an instrument by id (a new instrument has
// no id yet and is given a provisional one). It returns the stored row.
func (d *stagedDesign) putInstrument(i db.Instrument) *stagedInstrument {
	for k := range d.Instruments {
		if d.Instruments[k].ID == i.ID {
			next := stageInstrument(i)
			next.Fields = d.Instruments[k].Fields
			d.Instruments[k] = next
			return &d.Instruments[k]
		}
	}
	if i.Position == 0 {
		i.Position = len(d.Instruments) + 1
	}
	if i.ID == 0 {
		i.ID = d.newID()
	}
	d.Instruments = append(d.Instruments, stageInstrument(i))
	return &d.Instruments[len(d.Instruments)-1]
}

// renameInstrument moves an instrument to a new name and carries its mapping
// entries with it, so the two never disagree (the caller has already rejected a
// name that collides, UNIQUE (project_id, name)).
func (d *stagedDesign) renameInstrument(instID int64, to string) {
	for k := range d.Instruments {
		if d.Instruments[k].ID != instID || d.Instruments[k].Name == to {
			continue
		}
		from := d.Instruments[k].Name
		d.Instruments[k].Name = to
		for armNum := range d.Mapping {
			if pairs, ok := d.Mapping[armNum][from]; ok {
				delete(d.Mapping[armNum], from)
				d.Mapping[armNum][to] = pairs
			}
		}
		return
	}
}

// deleteInstrument drops an instrument and its fields together with the
// mapping pairs that named it. The last instrument of a project is never
// dropped through here — its endpoint deletes only the fields and renames the
// shell to "instrument" (REQ-API-127).
func (d *stagedDesign) deleteInstrument(instID int64) {
	for k := range d.Instruments {
		if d.Instruments[k].ID != instID {
			continue
		}
		name := d.Instruments[k].Name
		d.Instruments = append(d.Instruments[:k], d.Instruments[k+1:]...)
		for armNum := range d.Mapping {
			delete(d.Mapping[armNum], name)
		}
		return
	}
}

// orderInstruments writes the full project instrument order; a partial or
// foreign list is refused as the live endpoint refuses it.
func (d *stagedDesign) orderInstruments(orderedIDs []int64) bool {
	if len(orderedIDs) != len(d.Instruments) {
		return false
	}
	byID := map[int64]stagedInstrument{}
	for _, i := range d.Instruments {
		byID[i.ID] = i
	}
	next := make([]stagedInstrument, 0, len(orderedIDs))
	for i, id := range orderedIDs {
		inst, ok := byID[id]
		if !ok {
			return false
		}
		inst.Position = i + 1
		delete(byID, id)
		next = append(next, inst)
	}
	d.Instruments = next
	return true
}

// putField inserts or replaces one field of an instrument.
func (d *stagedDesign) putField(instID int64, f db.Field) (*stagedField, bool) {
	inst, ok := d.instrument(instID)
	if !ok {
		return nil, false
	}
	f.InstrumentID = instID
	for k := range inst.Fields {
		if inst.Fields[k].ID == f.ID {
			inst.Fields[k] = stageField(f)
			return &inst.Fields[k], true
		}
	}
	if f.Position == 0 {
		f.Position = len(inst.Fields) + 1
	}
	if f.ID == 0 {
		f.ID = d.newID()
	}
	inst.Fields = append(inst.Fields, stageField(f))
	return &inst.Fields[len(inst.Fields)-1], true
}

// deleteField removes one field from its instrument.
func (d *stagedDesign) deleteField(fieldID int64) bool {
	for i := range d.Instruments {
		f := d.Instruments[i].Fields
		for k := range f {
			if f[k].ID == fieldID {
				d.Instruments[i].Fields = append(f[:k], f[k+1:]...)
				return true
			}
		}
	}
	return false
}

// orderFields writes the full field order of one instrument. A partial list is
// refused, as the live endpoint refuses it (400: "order must list every field of
// the instrument exactly once"). The staged design has no UNIQUE constraint to
// park positions against — that is a commit-time concern, handled by the
// two-phase write there.
func (d *stagedDesign) orderFields(instID int64, orderedIDs []int64) bool {
	inst, ok := d.instrument(instID)
	if !ok || len(orderedIDs) != len(inst.Fields) {
		return false
	}
	byID := map[int64]stagedField{}
	for _, f := range inst.Fields {
		byID[f.ID] = f
	}
	next := make([]stagedField, 0, len(orderedIDs))
	for i, id := range orderedIDs {
		f, ok := byID[id]
		if !ok {
			return false
		}
		f.Position = i + 1
		delete(byID, id)
		next = append(next, f)
	}
	inst.Fields = next
	return true
}

// putArm inserts or replaces an arm.
func (d *stagedDesign) putArm(a db.Arm) *stagedArm {
	for k := range d.Arms {
		if d.Arms[k].ID == a.ID {
			next := stageArm(a)
			next.Events = d.Arms[k].Events
			d.Arms[k] = next
			return &d.Arms[k]
		}
	}
	if a.ID == 0 {
		a.ID = d.newID()
	}
	if a.Position == 0 {
		a.Position = len(d.Arms) + 1
	}
	d.Arms = append(d.Arms, stageArm(a))
	return &d.Arms[len(d.Arms)-1]
}

// deleteArm removes an arm and its events. Callers reject a non-empty arm
// first (DEV-API-6), and never the last remaining one — that endpoint renames
// it to "arm_1" instead (REQ-API-128).
func (d *stagedDesign) deleteArm(armID int64) {
	for k := range d.Arms {
		if d.Arms[k].ID != armID {
			continue
		}
		armNum := d.Arms[k].ArmNum
		d.Arms = append(d.Arms[:k], d.Arms[k+1:]...)
		delete(d.Mapping, armNum)
		return
	}
}

// orderArms writes the full project arm order; a partial or foreign list is
// refused as the live endpoint refuses it. Reordering only moves positions —
// every arm keeps its id and arm_num (REQ-API-129).
func (d *stagedDesign) orderArms(orderedIDs []int64) bool {
	if len(orderedIDs) != len(d.Arms) {
		return false
	}
	byID := map[int64]stagedArm{}
	for _, a := range d.Arms {
		byID[a.ID] = a
	}
	next := make([]stagedArm, 0, len(orderedIDs))
	for i, id := range orderedIDs {
		a, ok := byID[id]
		if !ok {
			return false
		}
		a.Position = i + 1
		delete(byID, id)
		next = append(next, a)
	}
	d.Arms = next
	return true
}

// putEvent inserts or replaces one event of an arm.
func (d *stagedDesign) putEvent(armID int64, e db.Event) (*stagedEvent, bool) {
	arm, ok := d.armByID(armID)
	if !ok {
		return nil, false
	}
	e.ArmID = armID
	for k := range arm.Events {
		if arm.Events[k].ID == e.ID {
			arm.Events[k] = stageEvent(e)
			return &arm.Events[k], true
		}
	}
	if e.Position == 0 {
		e.Position = len(arm.Events) + 1
	}
	if e.ID == 0 {
		e.ID = d.newID()
	}
	arm.Events = append(arm.Events, stageEvent(e))
	return &arm.Events[len(arm.Events)-1], true
}

// totalEvents counts every event of the snapshot, across all arms.
func (d *stagedDesign) totalEvents() int {
	n := 0
	for _, a := range d.Arms {
		n += len(a.Events)
	}
	return n
}

// deleteEvent removes one event and the mapping pairs that named it.
func (d *stagedDesign) deleteEvent(eventID int64) bool {
	for i := range d.Arms {
		a := &d.Arms[i]
		var unique string
		for k := range a.Events {
			if a.Events[k].ID != eventID {
				continue
			}
			unique = a.Events[k].UniqueEventName
			a.Events = append(a.Events[:k], a.Events[k+1:]...)
			break
		}
		if unique == "" {
			continue // the event lives in another arm
		}
		for name, uniques := range d.Mapping[a.ArmNum] {
			kept := make([]string, 0, len(uniques))
			for _, u := range uniques {
				if u != unique {
					kept = append(kept, u)
				}
			}
			d.Mapping[a.ArmNum][name] = kept
		}
		return true
	}
	return false
}

// orderEvents writes the full user order of one arm's events (REQ-API-103); a
// partial list is refused as the live endpoint refuses it.
func (d *stagedDesign) orderEvents(armID int64, orderedIDs []int64) bool {
	arm, ok := d.armByID(armID)
	if !ok || len(orderedIDs) != len(arm.Events) {
		return false
	}
	byID := map[int64]stagedEvent{}
	for _, e := range arm.Events {
		byID[e.ID] = e
	}
	next := make([]stagedEvent, 0, len(orderedIDs))
	for i, id := range orderedIDs {
		e, ok := byID[id]
		if !ok {
			return false
		}
		e.Position = i + 1
		delete(byID, id)
		next = append(next, e)
	}
	arm.Events = next
	return true
}

// setMappingForArm replaces one arm's mapping from the full checked matrix
// (REQ-API-073). Instrument names unknown to the design are skipped; callers
// reject them with 400 before getting here.
func (d *stagedDesign) setMappingForArm(armNum int, mapping map[string][]string) {
	clean := map[string][]string{}
	for name, uniques := range mapping {
		list := make([]string, 0, len(uniques))
		seen := map[string]bool{}
		for _, u := range uniques {
			if !seen[u] {
				seen[u] = true
				list = append(list, u)
			}
		}
		clean[name] = list
	}
	d.Mapping[armNum] = clean
}

// --- small ordering helpers shared by the flatteners ---

func sortedInstruments(in []stagedInstrument) []stagedInstrument {
	out := make([]stagedInstrument, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

func sortedFields(in []stagedField) []stagedField {
	out := make([]stagedField, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

func sortedEvents(in []stagedEvent) []stagedEvent {
	out := make([]stagedEvent, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// eventsOfArm returns one arm's events by position.
func eventsOfArm(arms []stagedArm, armNum int) []stagedEvent {
	for _, a := range arms {
		if a.ArmNum == armNum {
			return sortedEvents(a.Events)
		}
	}
	return nil
}

// eventInArm resolves a unique event name inside one arm.
func eventInArm(a stagedArm, unique string) (stagedEvent, bool) {
	for _, e := range a.Events {
		if e.UniqueEventName == unique {
			return e, true
		}
	}
	return stagedEvent{}, false
}

// sortedUniqueNames orders mapped event names the way the canonical listing
// does (GD-15), so a staged read matches an unstaged one.
func sortedUniqueNames(names []string, canonical []stagedEvent) []string {
	rank := map[string]int{}
	for i, e := range canonical {
		rank[e.UniqueEventName] = i
	}
	out := append([]string(nil), names...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i]] < rank[out[j]] })
	return out
}
