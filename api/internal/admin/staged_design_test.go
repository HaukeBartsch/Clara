package admin

import (
	"context"
	"database/sql"
	"testing"

	"csms/api/internal/db"
)

// mustField inserts one field of an instrument (the snapshot tests need a
// dictionary, and the structure fixtures stop at arms, events and instruments).
func (e *env) mustField(projectID, instrumentID int64, name, fieldType string, position int) int64 {
	e.t.Helper()
	id, err := e.Store.AddField(context.Background(), &db.Field{
		ProjectID: projectID, InstrumentID: instrumentID, FieldName: name,
		FieldType: fieldType, Position: position,
	})
	if err != nil {
		e.t.Fatalf("AddField: %v", err)
	}
	return id
}

// mustCalcField inserts a calculated field with its expression, the shape the
// §6.2 reference rules are checked against.
func (e *env) mustCalcField(projectID, instrumentID int64, name, expr string, position int) int64 {
	e.t.Helper()
	id := e.mustField(projectID, instrumentID, name, "calculated", position)
	if _, err := e.Store.DB.ExecContext(context.Background(),
		`UPDATE fields SET calculation = ? WHERE id = ?`, expr, id); err != nil {
		e.t.Fatalf("calculation: %v", err)
	}
	return id
}

// seedDesign builds the smallest design that exercises every part of the
// snapshot: two arms, an event in each, two instruments with fields, and one
// instrument mapped into both arms.
func (e *env) seedDesign(projectID int64) designFixture {
	e.t.Helper()
	ctx := context.Background()
	arm1 := e.mustArm(projectID, 1)
	arm2 := e.mustArm(projectID, 2)
	ev1 := e.mustEvent(projectID, arm1, "Baseline", "baseline_arm_1")
	ev2 := e.mustEvent(projectID, arm2, "Followup", "followup_arm_2")

	instA := e.mustInstrument(projectID, "intake")
	e.mustField(projectID, instA, "record_id", "text", 1)
	e.mustField(projectID, instA, "age", "text", 2)
	instB := e.mustInstrument(projectID, "labs")
	e.mustField(projectID, instB, "hb", "text", 1)

	// labs.hb feeds a calculated field on intake (a dependency the context has
	// to re-derive from the stored expression).
	calc := e.mustCalcField(projectID, instA, "age_plus", "[baseline_arm_1][hb] + 1", 3)
	if err := e.Store.AddCalculatedDependency(ctx, &db.CalculatedDependency{
		ProjectID: projectID, CalculatedFieldID: calc,
		RefUniqueEventName: "baseline_arm_1", RefFieldName: "hb",
	}); err != nil {
		e.t.Fatalf("AddCalculatedDependency: %v", err)
	}

	e.mustMap(projectID, arm1,
		db.InstrumentEvent{InstrumentID: instA, EventID: ev1},
		db.InstrumentEvent{InstrumentID: instB, EventID: ev1})
	e.mustMap(projectID, arm2, db.InstrumentEvent{InstrumentID: instB, EventID: ev2})

	return designFixture{
		arm1: arm1, arm2: arm2, event1: ev1, event2: ev2,
		instrumentA: instA, instrumentB: instB, calcField: calc,
	}
}

type designFixture struct {
	arm1, arm2               int64
	event1, event2           int64
	instrumentA, instrumentB int64
	calcField                int64
}

// TestStagedDesignRoundTrip asserts the snapshot carries the live design
// faithfully: encode, parse, and compare every flattened view against the
// store listing it was taken from (REQ-DB-035).
func TestStagedDesignRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Snapshot Study")
	e.seedDesign(projectID)

	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	doc, err := d.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := parseStagedDesign(doc)
	if err != nil {
		t.Fatalf("parseStagedDesign: %v (%s)", err, doc)
	}

	wantInstruments, err := e.Store.ListInstruments(ctx, projectID)
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	gotInstruments := back.dbInstruments(projectID)
	if len(gotInstruments) != len(wantInstruments) {
		t.Fatalf("instruments = %d, want %d", len(gotInstruments), len(wantInstruments))
	}
	for i := range wantInstruments {
		if gotInstruments[i].ID != wantInstruments[i].ID ||
			gotInstruments[i].Name != wantInstruments[i].Name ||
			gotInstruments[i].Position != wantInstruments[i].Position {
			t.Errorf("instrument %d = %+v, want %+v", i, gotInstruments[i], wantInstruments[i])
		}
	}

	wantFields, err := e.Store.ListFields(ctx, projectID)
	if err != nil {
		t.Fatalf("ListFields: %v", err)
	}
	gotFields := back.dbFields(projectID)
	if len(gotFields) != len(wantFields) {
		t.Fatalf("fields = %d, want %d", len(gotFields), len(wantFields))
	}
	// Same rows in the same order — listings answer from the snapshot, so a
	// reader cannot tell the two apart.
	for i := range wantFields {
		if gotFields[i] != wantFields[i] {
			t.Errorf("field %d = %+v, want %+v", i, gotFields[i], wantFields[i])
		}
	}

	wantEvents, err := e.Store.ListEvents(ctx, projectID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	gotEvents := back.dbEvents(projectID)
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("events = %d, want %d", len(gotEvents), len(wantEvents))
	}
	for i := range wantEvents {
		if gotEvents[i] != wantEvents[i] {
			t.Errorf("event %d = %+v, want %+v", i, gotEvents[i], wantEvents[i])
		}
	}

	wantPairs, err := e.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		t.Fatalf("ListInstrumentEvents: %v", err)
	}
	gotPairs := back.dbPairs(projectID)
	if len(gotPairs) != len(wantPairs) {
		t.Fatalf("mapping pairs = %d, want %d", len(gotPairs), len(wantPairs))
	}
	for i := range wantPairs {
		if gotPairs[i] != wantPairs[i] {
			t.Errorf("pair %d = %+v, want %+v", i, gotPairs[i], wantPairs[i])
		}
	}

	// The mapping is keyed arm_num → instrument name (§4.12 shape).
	if got := back.Mapping[2]["labs"]; len(got) != 1 || got[0] != "followup_arm_2" {
		t.Errorf("arm 2 labs mapping = %v, want [followup_arm_2]", got)
	}
	if !back.mapped(1, "intake", "baseline_arm_1") || back.mapped(2, "intake", "followup_arm_2") {
		t.Error("mapped() disagrees with the seeded matrix")
	}
}

// TestStagedDesignContextMatchesLive is the reuse claim: the reference rules of
// §6.2 must see the same dictionary whether they read the tables or the
// snapshot, so a staged edit cannot pass a check the live edit would fail.
func TestStagedDesignContextMatchesLive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Context Parity")
	e.seedDesign(projectID)

	live, err := e.Handler.loadDesign(ctx, projectID)
	if err != nil {
		t.Fatalf("loadDesign: %v", err)
	}
	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	staged := d.designContext(projectID)

	if len(staged.fields) != len(live.fields) {
		t.Errorf("context fields = %d, want %d", len(staged.fields), len(live.fields))
	}
	for name, want := range live.fields {
		got, ok := staged.fields[name]
		if !ok {
			t.Errorf("field %q missing from the staged context", name)
			continue
		}
		if got.ID != want.ID || got.FieldType != want.FieldType {
			t.Errorf("field %q = %+v, want %+v", name, got, want)
		}
	}
	if len(staged.events) != len(live.events) {
		t.Errorf("context events = %d, want %d", len(staged.events), len(live.events))
	}
	for unique, want := range live.events {
		got, ok := staged.events[unique]
		if !ok || got.ID != want.ID {
			t.Errorf("event %q missing or wrong in the staged context", unique)
		}
	}
	if len(staged.activeAt) != len(live.activeAt) {
		t.Errorf("activeAt arms = %d, want %d", len(staged.activeAt), len(live.activeAt))
	}
	for unique, want := range live.activeAt {
		got := staged.activeAt[unique]
		if len(got) != len(want) {
			t.Errorf("activeAt[%s] = %v, want %v", unique, got, want)
			continue
		}
		for instID := range want {
			if !got[instID] {
				t.Errorf("activeAt[%s][%d] missing in the staged context", unique, instID)
			}
		}
	}
	// Dependencies come back from the stored expression, not the table.
	if len(staged.deps) != 1 {
		t.Fatalf("deps = %d, want 1 (%+v)", len(staged.deps), staged.deps)
	}
	if staged.deps[0].CalculatedFieldID != live.deps[0].CalculatedFieldID ||
		staged.deps[0].RefFieldName != live.deps[0].RefFieldName {
		t.Errorf("deps = %+v, want %+v", staged.deps[0], live.deps[0])
	}
}

// TestStagedDesignProvisionalIDs: objects created while a set is open take
// negative ids below zero and descending, so they can never be mistaken for a
// live row at commit.
func TestStagedDesignProvisionalIDs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Provisional IDs")
	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}

	inst := d.putInstrument(db.Instrument{ProjectID: projectID, Name: "new_form"})
	if inst.ID >= 0 {
		t.Errorf("instrument id = %d, want a negative provisional id", inst.ID)
	}
	f, ok := d.putField(inst.ID, db.Field{
		ProjectID: projectID, InstrumentID: inst.ID, FieldName: "answer", FieldType: "text",
	})
	if !ok {
		t.Fatalf("putField on the new instrument failed")
	}
	if f.ID >= 0 || f.ID == inst.ID {
		t.Errorf("field id = %d, want a fresh negative id below %d", f.ID, inst.ID)
	}
	if f.Position != 1 {
		t.Errorf("field position = %d, want 1 (end of an empty instrument)", f.Position)
	}
	arm := d.putArm(db.Arm{ProjectID: projectID, ArmNum: d.nextArmNum(), Name: sql.NullString{String: "Third", Valid: true}})
	if arm.ID >= 0 {
		t.Errorf("arm id = %d, want a negative provisional id", arm.ID)
	}
	if ev, ok := d.putEvent(arm.ID, db.Event{ProjectID: projectID, ArmID: arm.ID,
		EventName: "Visit", UniqueEventName: uniqueEventName("Visit", arm.ArmNum)}); !ok || ev.ID >= 0 {
		t.Errorf("event = %+v/%v, want a stored event with a negative id", ev, ok)
	}

	// Re-putting an existing row replaces it rather than appending.
	before := len(d.Instruments)
	f2, _ := d.putField(inst.ID, db.Field{ID: f.ID, ProjectID: projectID, InstrumentID: inst.ID,
		FieldName: "answer", FieldType: "text", FieldLabel: sql.NullString{String: "Answer", Valid: true},
		Position: f.Position})
	if len(d.Instruments) != before {
		t.Errorf("instruments = %d after re-put, want %d", len(d.Instruments), before)
	}
	if f2.ID != f.ID || !f2.FieldLabel.Valid {
		t.Errorf("re-put did not replace the field: %+v", f2)
	}
}

// TestStagedDesignRenameAndDeletePruneMapping keeps the mapping honest as the
// design moves under a staged edit: an instrument rename carries its entries to
// the new name, and a delete takes them away (a stale key must not resurrect a
// pair at commit).
func TestStagedDesignRenameAndDeletePruneMapping(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Rename Prune")
	f := e.seedDesign(projectID)

	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	d.renameInstrument(f.instrumentB, "bloods")
	if inst, ok := d.instrument(f.instrumentB); !ok || inst.Name != "bloods" {
		t.Errorf("instrument after rename = %+v/%v, want name bloods", inst, ok)
	}
	if _, stale := d.Mapping[2]["labs"]; stale {
		t.Error("mapping kept the old instrument name")
	}
	if got := d.Mapping[2]["bloods"]; len(got) != 1 || got[0] != "followup_arm_2" {
		t.Errorf("mapping after rename = %v, want [followup_arm_2]", got)
	}

	d.deleteInstrument(f.instrumentB)
	for armNum, byName := range d.Mapping {
		if _, ok := byName["bloods"]; ok {
			t.Errorf("arm %d still maps the deleted instrument", armNum)
		}
	}
	if len(d.dbPairs(projectID)) != 1 {
		t.Errorf("pairs after delete = %d, want 1 (intake × baseline)", len(d.dbPairs(projectID)))
	}
}

// TestStagedDesignOrdering covers the two reorder mutators: positions follow
// the given order and the flattened views agree.
func TestStagedDesignOrdering(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Ordering")
	f := e.seedDesign(projectID)

	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	instA, _ := d.instrument(f.instrumentA)
	ids := make([]int64, 0, len(instA.Fields))
	for i := len(instA.Fields) - 1; i >= 0; i-- { // reverse intake's three fields
		ids = append(ids, instA.Fields[i].ID)
	}
	if !d.orderFields(f.instrumentA, ids) {
		t.Fatalf("orderFields rejected a valid full order")
	}
	fields := d.dbFields(projectID)
	if fields[0].FieldName != "age_plus" || fields[2].FieldName != "record_id" {
		t.Errorf("field order after reorder = %s, %s, %s, want age_plus, age, record_id",
			fields[0].FieldName, fields[1].FieldName, fields[2].FieldName)
	}

	// A partial or foreign order is refused (the live endpoint answers 400).
	if d.orderFields(f.instrumentA, ids[:1]) {
		t.Error("orderFields accepted a partial order")
	}
	if d.orderFields(9999, ids) {
		t.Error("orderFields accepted an unknown instrument")
	}

	arm1, _ := d.arm(1)
	evIDs := make([]int64, 0, len(arm1.Events))
	for _, ev := range arm1.Events {
		evIDs = append(evIDs, ev.ID)
	}
	if !d.orderEvents(f.arm1, evIDs) {
		t.Error("orderEvents rejected a valid order")
	}
	if d.orderEvents(f.arm1, []int64{9998}) {
		t.Error("orderEvents accepted an unknown event")
	}
}
