package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- arms (§4.8) ---

func TestStructureArms(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Arm Study")

	// arm_num is the next 1-based number; empty name renders "".
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create arm: %d %s", rec.Code, rec.Body.String())
	}
	var arm armObject
	e.decode(rec, &arm)
	if arm.ArmNum != 1 || arm.Name != "" {
		t.Fatalf("created arm: %+v", arm)
	}
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "B"}, admin)
	var arm2 armObject
	e.decode(rec, &arm2)
	if rec.Code != http.StatusCreated || arm2.ArmNum != 2 {
		t.Fatalf("second arm: %d %+v", rec.Code, arm2)
	}

	// An event nests under its arm without repeating arm_num (§4.8).
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "visit",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: %d %s", rec.Code, rec.Body.String())
	}
	var ev EventObject
	e.decode(rec, &ev)
	if ev.UniqueEventName != "visit_arm_1" || ev.ArmNum != 1 {
		t.Fatalf("created event: %+v", ev)
	}

	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/arms", nil, admin)
	var arms []armObject
	e.decode(rec, &arms)
	if len(arms) != 2 || len(arms[0].Events) != 1 || arms[0].Events[0].UniqueEventName != "visit_arm_1" {
		t.Fatalf("arms listing: %s", rec.Body.String())
	}

	// Duplicate label within the arm → 409; unknown arm number → 400.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "visit",
	}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup label: %d, want 409", rec.Code)
	}
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 9, "event_name": "x",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown arm: %d, want 400", rec.Code)
	}

	// Non-members get the uniform 403; a member without read access too.
	regular := e.mustUser("user@example.org")
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/arms", nil, regular)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member listing: %d, want 403", rec.Code)
	}
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{}, regular)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: %d, want 403", rec.Code)
	}
}

func TestDeleteArm(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Arm Study")
	e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{}, admin)

	// An arm with an event cannot be deleted (DEV-API-6) — as long as it is
	// not the last one, which TestLastArmRename covers.
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "visit",
	}, admin)
	var ev EventObject
	e.decode(rec, &ev)
	armRow, err := e.Store.GetArm(context.Background(), armIDOfEvent(t, e, ev.ID))
	if err != nil || armRow == nil {
		t.Fatalf("load arm: %v", err)
	}
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "B"}, admin)
	var arm2 armObject
	e.decode(rec, &arm2)
	rec = e.do("DELETE", "/api/v1/arms/"+itoa(armRow.ID), nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete busy arm: %d, want 409", rec.Code)
	}

	// The second (empty) arm deletes cleanly with an audit entry.
	rec = e.do("DELETE", "/api/v1/arms/"+itoa(arm2.ID), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete empty arm: %d %s", rec.Code, rec.Body.String())
	}
	var found bool
	for _, typ := range e.auditTypes() {
		if typ == "arm_deleted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing arm_deleted audit: %v", e.auditTypes())
	}
}

// TestBranchingBareFieldReference: design-time branching validation resolves
// a bare [field] reference against the project's first event in canonical
// order (§3.6.3) — a field active there validates; a field that is not does
// not, even when it exists at another event.
func TestBranchingBareFieldReference(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	projectID := e.mustProject("Bare Ref Study")
	armID, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: projectID, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	baseID, err := e.Store.AddEvent(ctx, &db.Event{
		ProjectID: projectID, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1",
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	folID, err := e.Store.AddEvent(ctx, &db.Event{
		ProjectID: projectID, ArmID: armID, EventName: "followup", UniqueEventName: "followup_arm_1",
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	intakeID, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	laterID, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "later"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	if err := e.Store.SetInstrumentEventsForArm(ctx, projectID, armID, []db.InstrumentEvent{
		{InstrumentID: intakeID, EventID: baseID},
		{InstrumentID: laterID, EventID: folID},
	}); err != nil {
		t.Fatalf("SetInstrumentEventsForArm: %v", err)
	}
	addField := func(instrID int64, name string) {
		t.Helper()
		if _, err := e.Store.AddField(ctx, &db.Field{
			ProjectID: projectID, InstrumentID: instrID, FieldName: name, FieldType: "text",
		}); err != nil {
			t.Fatalf("AddField %s: %v", name, err)
		}
	}
	addField(intakeID, "age")       // active at the first event
	addField(laterID, "later_only") // active only at followup

	// Bare [field] resolves against baseline_arm_1 — age is active there.
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(intakeID)+"/fields",
		map[string]any{"field_name": "flag", "field_type": "text",
			"branching_logic": `[age] = "x"`}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bare ref at first event: %d %s, want 201", rec.Code, rec.Body.String())
	}
	// later_only is not active at the first event → rejected with the reason.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(intakeID)+"/fields",
		map[string]any{"field_name": "bad", "field_type": "text",
			"branching_logic": `[later_only] = "x"`}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bare ref elsewhere: %d %s, want 400", rec.Code, rec.Body.String())
	}
	// The event-qualified form keeps working unchanged.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(intakeID)+"/fields",
		map[string]any{"field_name": "ok_qualified", "field_type": "text",
			"branching_logic": `[followup_arm_1][later_only] = "x"`}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("qualified ref: %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// TestDeleteEvent: DELETE /api/v1/events/{id} removes one event with its
// mapping pairs (204 + audit). The last remaining event of the project cannot
// go — the call instead resets it to the plain baseline state (renamed,
// offset day 0, safe region cleared) and answers with the object (REQ-API-126).
func TestDeleteEvent(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	projectID := e.mustProject("Event Study")
	armID, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: projectID, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	baseID, err := e.Store.AddEvent(ctx, &db.Event{
		ProjectID: projectID, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1",
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}

	// The last remaining event resets to baseline instead of going (200 +
	// object). It already is the plain baseline, so nothing changes but the
	// answer shape.
	rec := e.do("DELETE", "/api/v1/events/"+itoa(baseID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset last event: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var resetEv EventObject
	e.decode(rec, &resetEv)
	if resetEv.ID != baseID || resetEv.EventName != "baseline" ||
		resetEv.UniqueEventName != "baseline_arm_1" ||
		resetEv.Period == nil || *resetEv.Period != 0 {
		t.Fatalf("reset last event object = %+v", resetEv)
	}

	// With a second event the deletion goes through and takes the event's
	// mapping pairs with it.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "follow_up",
	}, admin)
	var ev2 EventObject
	e.decode(rec, &ev2)
	iid, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/instrument-event-mapping",
		map[string]any{"arm_num": 1, "mapping": map[string][]string{
			"intake": {"baseline_arm_1", "follow_up_arm_1"},
		}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("put mapping: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("DELETE", "/api/v1/events/"+itoa(ev2.ID), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete event: %d %s", rec.Code, rec.Body.String())
	}
	events, err := e.Store.ListEvents(ctx, projectID)
	if err != nil || len(events) != 1 || events[0].ID != baseID {
		t.Fatalf("events after delete = %+v (%v), want only baseline", events, err)
	}
	pairs, err := e.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil || len(pairs) != 1 || pairs[0].InstrumentID != iid {
		t.Fatalf("mapping pairs after delete = %+v (%v), want only the baseline pair", pairs, err)
	}
	var audited bool
	for _, typ := range e.auditTypes() {
		if typ == "event_deleted" {
			audited = true
		}
	}
	if !audited {
		t.Errorf("audit types = %v, want event_deleted", e.auditTypes())
	}

	// project_admin only; an unknown id is 404.
	regular := e.mustUser("user@example.org")
	rec = e.do("DELETE", "/api/v1/events/"+itoa(baseID), nil, regular)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin delete: %d, want 403", rec.Code)
	}
	rec = e.do("DELETE", "/api/v1/events/999999", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown event: %d, want 404", rec.Code)
	}
}

// TestLastEventResetMigratesData: deleting the last remaining event renames
// it to "baseline", resets the offset day to 0 and clears the safe region;
// stored values follow the new unique name (REQ-API-126, ASM-API-4).
func TestLastEventResetMigratesData(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	projectID := e.mustProject("Reset Study")
	armID, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: projectID, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	evID, err := e.Store.AddEvent(ctx, &db.Event{
		ProjectID: projectID, ArmID: armID, EventName: "visit", UniqueEventName: "visit_arm_1",
		Period:          sql.NullInt64{Int64: 5, Valid: true},
		SafeRegionStart: sql.NullInt64{Int64: 1, Valid: true},
		SafeRegionEnd:   sql.NullInt64{Int64: 10, Valid: true},
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'visit_arm_1', '', 1, 'age', '42')`, projectID); err != nil {
		t.Fatalf("seed data: %v", err)
	}

	rec := e.do("DELETE", "/api/v1/events/"+itoa(evID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset last event: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var out EventObject
	e.decode(rec, &out)
	if out.ID != evID || out.EventName != "baseline" || out.UniqueEventName != "baseline_arm_1" ||
		out.Period == nil || *out.Period != 0 ||
		out.SafeRegionStart != nil || out.SafeRegionEnd != nil {
		t.Fatalf("reset object = %+v, want plain baseline (period 0, no safe region)", out)
	}
	var n int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND unique_event_name = 'baseline_arm_1'`,
		projectID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("data followed the rename: %d rows (%v)", n, err)
	}
	var saw bool
	rows, err := e.Store.DB.Query(
		`SELECT details FROM audit_events WHERE event_type = 'event_updated'`)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && contains(d, `"last_event_reset"`) {
			saw = true
		}
	}
	if !saw {
		t.Errorf("audit missing event_updated with last_event_reset")
	}
}

// TestDeleteInstrument: DELETE /api/v1/projects/{id}/instruments/{iid}
// removes a non-last instrument with its fields, values and mapping pairs
// (204 + `instrument_deleted`); the last instrument of the project only loses
// its fields and is renamed to "instrument" (200 + object, REQ-API-127). An
// expression that survives the change may not name a doomed field.
func TestDeleteInstrument(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	projectID := e.mustProject("Instrument Study")
	armID := e.mustArm(projectID, 1)
	baseID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")

	aID, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	bID, err := e.Store.AddInstrument(ctx, &db.Instrument{ProjectID: projectID, Name: "later"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	addField := func(instrID int64, name string) {
		t.Helper()
		if _, err := e.Store.AddField(ctx, &db.Field{
			ProjectID: projectID, InstrumentID: instrID, FieldName: name, FieldType: "text",
		}); err != nil {
			t.Fatalf("AddField %s: %v", name, err)
		}
	}
	addField(aID, "age")
	addField(bID, "note")
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'baseline_arm_1', '', 1, 'note', 'x')`, projectID); err != nil {
		t.Fatalf("seed data: %v", err)
	}
	if err := e.Store.SetInstrumentEventsForArm(ctx, projectID, armID, []db.InstrumentEvent{
		{InstrumentID: aID, EventID: baseID},
		{InstrumentID: bID, EventID: baseID},
	}); err != nil {
		t.Fatalf("SetInstrumentEventsForArm: %v", err)
	}

	// An expression in the surviving instrument naming a doomed field blocks
	// the delete (same rule as the single-field delete).
	refID, err := e.Store.AddField(ctx, &db.Field{
		ProjectID: projectID, InstrumentID: aID, FieldName: "flag", FieldType: "text",
		BranchingLogic: sql.NullString{String: `[note] = "x"`, Valid: true},
	})
	if err != nil {
		t.Fatalf("AddField flag: %v", err)
	}
	rec := e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(bID), nil, admin)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "referenced by an active expression") {
		t.Fatalf("delete referenced instrument: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if _, err := e.Store.DB.Exec(`DELETE FROM fields WHERE id = ?`, refID); err != nil {
		t.Fatalf("remove ref field: %v", err)
	}

	// The non-last instrument goes with its fields, values and mapping pairs.
	rec = e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(bID), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete instrument: %d %s", rec.Code, rec.Body.String())
	}
	instruments, err := e.Store.ListInstruments(ctx, projectID)
	if err != nil || len(instruments) != 1 || instruments[0].ID != aID {
		t.Fatalf("instruments after delete = %+v (%v)", instruments, err)
	}
	fields, err := e.Store.ListFieldsByInstrument(ctx, projectID, bID)
	if err != nil || len(fields) != 0 {
		t.Fatalf("fields of deleted instrument = %+v (%v)", fields, err)
	}
	var n int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = 'note'`, projectID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("values of deleted instrument: %d rows (%v)", n, err)
	}
	pairs, err := e.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil || len(pairs) != 1 || pairs[0].InstrumentID != aID {
		t.Fatalf("mapping pairs after delete = %+v (%v)", pairs, err)
	}
	if !hasType(e.auditTypes(), "instrument_deleted") {
		t.Errorf("audit types = %v, want instrument_deleted", e.auditTypes())
	}

	// The last instrument only loses its fields and comes back as
	// "instrument" — id, position and mapping pair stay.
	rec = e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(aID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset last instrument: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var obj instrumentObject
	e.decode(rec, &obj)
	if obj.ID != aID || obj.Name != "instrument" || obj.FieldCount != 0 {
		t.Fatalf("reset object = %+v, want id %d named \"instrument\" with no fields", obj, aID)
	}
	fields, err = e.Store.ListFieldsByInstrument(ctx, projectID, aID)
	if err != nil || len(fields) != 0 {
		t.Fatalf("fields after reset = %+v (%v)", fields, err)
	}
	pairs, err = e.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil || len(pairs) != 1 || pairs[0].InstrumentID != aID {
		t.Fatalf("mapping pairs after reset = %+v (%v), want the pair kept", pairs, err)
	}
	var saw bool
	rows, err := e.Store.DB.Query(
		`SELECT details FROM audit_events WHERE event_type = 'instrument_updated'`)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && contains(d, `"last_instrument_reset"`) {
			saw = true
		}
	}
	if !saw {
		t.Errorf("audit missing instrument_updated with last_instrument_reset")
	}

	// project_admin only; an unknown id is 404.
	regular := e.mustUser("user@example.org")
	rec = e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(aID), nil, regular)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin delete: %d, want 403", rec.Code)
	}
	rec = e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/999999", nil, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown instrument: %d, want 404", rec.Code)
	}
}

// TestLastArmRename: deleting the last remaining arm renames it to "arm_1"
// and answers with the object — id, arm_num and events stay (REQ-API-128).
func TestLastArmRename(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Arm Study")

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "Only"}, admin)
	var arm armObject
	e.decode(rec, &arm)
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "visit",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do("DELETE", "/api/v1/arms/"+itoa(arm.ID), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete last arm: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var renamed armObject
	e.decode(rec, &renamed)
	if renamed.ID != arm.ID || renamed.ArmNum != 1 || renamed.Name != "arm_1" || len(renamed.Events) != 1 {
		t.Fatalf("renamed arm object = %+v, want id %d arm_1 with its event", renamed, arm.ID)
	}
	if !hasType(e.auditTypes(), "arm_updated") {
		t.Errorf("audit types = %v, want arm_updated", e.auditTypes())
	}
}

// TestOrderArms: PUT /api/v1/projects/{id}/arms/order writes the full order;
// ids and arm numbers never change (REQ-API-129).
func TestOrderArms(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Arm Order Study")
	var a1, a2 armObject
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "A"}, admin)
	e.decode(rec, &a1)
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "B"}, admin)
	e.decode(rec, &a2)

	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/arms/order", map[string]any{
		"order": []int64{a1.ID},
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("partial order: %d, want 400", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/arms/order", map[string]any{
		"order": []int64{a2.ID, a1.ID},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder arms: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/arms", nil, admin)
	var arms []armObject
	e.decode(rec, &arms)
	if len(arms) != 2 || arms[0].ID != a2.ID || arms[1].ID != a1.ID {
		t.Fatalf("arms after reorder = %+v", arms)
	}
	if !hasType(e.auditTypes(), "arm_reordered") {
		t.Errorf("audit types = %v, want arm_reordered", e.auditTypes())
	}
}

// armIDOfEvent looks up the arm id of an event row directly.
func armIDOfEvent(t *testing.T, e *env, eventID int64) int64 {
	t.Helper()
	var armID int64
	err := e.Store.DB.QueryRow(`SELECT arm_id FROM events WHERE id = ?`, eventID).Scan(&armID)
	if err != nil {
		t.Fatalf("event arm: %v", err)
	}
	return armID
}

// --- events (§4.9) ---

func TestEventRenameMigratesData(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Event Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "visit", "visit_arm_1")
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'visit_arm_1', '', 1, 'age', '42')`, projectID); err != nil {
		t.Fatalf("seed data: %v", err)
	}

	rec := e.do("PUT", "/api/v1/events/"+itoa(eventID), map[string]any{
		"event_name": "followup", "period": 14,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	var out EventObject
	e.decode(rec, &out)
	if out.UniqueEventName != "followup_arm_1" || out.Period == nil || *out.Period != 14 {
		t.Fatalf("renamed event: %+v", out)
	}
	// Stored values follow the new unique name (ASM-API-4).
	var n int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND unique_event_name = 'followup_arm_1'`,
		projectID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("data migrated: %d rows (%v)", n, err)
	}

	// period: null clears it.
	rec = e.do("PUT", "/api/v1/events/"+itoa(eventID), map[string]any{"period": nil}, admin)
	e.decode(rec, &out)
	if out.Period != nil {
		t.Fatalf("clear period: %+v", out)
	}

	// Renaming onto an existing label in the arm → 409.
	otherID := e.mustEvent(projectID, armID, "screening", "screening_arm_1")
	rec = e.do("PUT", "/api/v1/events/"+itoa(otherID), map[string]any{"event_name": "followup"}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename collision: %d, want 409", rec.Code)
	}

	// Audit carries the label change with old/new.
	var saw bool
	rows, err := e.Store.DB.Query(`SELECT details FROM audit_events WHERE event_type = 'event_updated'`)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil &&
			contains(d, `"label"`) && contains(d, `"visit"`) && contains(d, `"followup"`) {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("event_updated audit missing label change")
	}
}

func TestOrderEvents(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Order Study")
	armID := e.mustArm(projectID, 1)
	e1 := e.mustEvent(projectID, armID, "a", "a_arm_1")
	e2 := e.mustEvent(projectID, armID, "b", "b_arm_1")

	// A partial or foreign order → 400 invalid_request.
	rec := e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/events/order", map[string]any{
		"arm_num": 1, "order": []int64{e1},
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("partial order: %d, want 400", rec.Code)
	}

	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/events/order", map[string]any{
		"arm_num": 1, "order": []int64{e2, e1},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	var out []EventObject
	e.decode(rec, &out)
	if len(out) != 2 || out[0].ID != e2 || out[1].ID != e1 {
		t.Fatalf("reordered listing: %+v", out)
	}
	var saw bool
	for _, typ := range e.auditTypes() {
		if typ == "event_reordered" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("missing event_reordered audit")
	}
}

// --- instruments (§4.10) ---

func TestInstrumentsAndBranching(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Inst Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments", map[string]any{"name": "intake"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create instrument: %d %s", rec.Code, rec.Body.String())
	}
	var inst instrumentObject
	e.decode(rec, &inst)
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments", map[string]any{"name": "intake"}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup instrument: %d, want 409", rec.Code)
	}

	// A field on the instrument so a branching reference can resolve.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(inst.ID)+"/fields", map[string]any{
		"field_name": "yn", "field_type": "text",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create field: %d %s", rec.Code, rec.Body.String())
	}
	if err := e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID,
		[]db.InstrumentEvent{{InstrumentID: inst.ID, EventID: eventID}}); err != nil {
		t.Fatalf("map instrument: %v", err)
	}

	// Invalid branching logic is rejected at design time (§7.2).
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(inst.ID), map[string]any{
		"branching_logic": "[baseline_arm_1][nope] = \"y\"",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad branching: %d, want 400", rec.Code)
	}
	rec = e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(inst.ID), map[string]any{
		"branching_logic": "[baseline_arm_1][yn] = \"y\"", "is_survey": true,
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("set branching: %d %s", rec.Code, rec.Body.String())
	}
	var updated instrumentObject
	e.decode(rec, &updated)
	if !updated.IsSurvey || updated.BranchingLogic != "[baseline_arm_1][yn] = \"y\"" {
		t.Fatalf("updated instrument: %+v", updated)
	}

	// Listing shows the field count.
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instruments", nil, admin)
	var list []instrumentObject
	e.decode(rec, &list)
	if len(list) != 1 || list[0].FieldCount != 1 {
		t.Fatalf("listing: %s", rec.Body.String())
	}
}

// TestInstrumentRename covers the name rename of PUT …/instruments/{iid}
// (REQ-API-130): a label change — unique within the project, mapping pairs and
// stored values untouched — audited with old and new name.
func TestInstrumentRename(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Rename Study")
	armID := e.mustArm(projectID, 1)
	e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	instID := e.mustInstrument(projectID, "intake")
	e.mustInstrument(projectID, "labs")

	base := "/api/v1/projects/" + itoa(projectID)
	instURL := base + "/instruments/" + itoa(instID)

	// intake holds a mapping pair the rename must carry along.
	rec := e.do("PUT", base+"/instrument-event-mapping", map[string]any{
		"arm_num": 1, "mapping": map[string][]string{"intake": {"baseline_arm_1"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("put mapping: %d %s", rec.Code, rec.Body.String())
	}

	countAudit := func(eventType string) int {
		var n int
		if err := e.Store.DB.QueryRow(
			`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, eventType).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Empty or non-string name → 400; a collision with another instrument → 409.
	if code := e.do("PUT", instURL, map[string]any{"name": ""}, admin).Code; code != http.StatusBadRequest {
		t.Fatalf("empty name: %d, want 400", code)
	}
	if code := e.do("PUT", instURL, map[string]any{"name": 7}, admin).Code; code != http.StatusBadRequest {
		t.Fatalf("non-string name: %d, want 400", code)
	}
	if code := e.do("PUT", instURL, map[string]any{"name": "labs"}, admin).Code; code != http.StatusConflict {
		t.Fatalf("colliding name: %d, want 409", code)
	}

	// Idempotence (REQ-API-042): the current name answers 200 and writes no entry.
	before := countAudit("instrument_updated")
	rec = e.do("PUT", instURL, map[string]any{"name": "intake"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-name rename: %d %s", rec.Code, rec.Body.String())
	}
	if n := countAudit("instrument_updated"); n != before {
		t.Fatalf("same-name rename wrote an audit entry (%d → %d)", before, n)
	}

	// Rename together with the survey flag in one PUT.
	rec = e.do("PUT", instURL, map[string]any{"name": "follow_up", "is_survey": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	var obj instrumentObject
	e.decode(rec, &obj)
	if obj.Name != "follow_up" || !obj.IsSurvey || obj.ID != instID {
		t.Fatalf("renamed object: %+v", obj)
	}

	// Audit carries the old and new name inside changes.
	var details string
	if err := e.Store.DB.QueryRow(
		`SELECT details FROM audit_events WHERE event_type = 'instrument_updated' ORDER BY id DESC LIMIT 1`,
	).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Name    string `json:"name"`
		Changes struct {
			Name struct {
				Old string `json:"old"`
				New string `json:"new"`
			} `json:"name"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(details), &entry); err != nil {
		t.Fatalf("audit details: %s (%v)", details, err)
	}
	if entry.Changes.Name.Old != "intake" || entry.Changes.Name.New != "follow_up" {
		t.Fatalf("audit name change: %s", details)
	}

	// The listing and the mapping answer under the new name; nothing else moved.
	rec = e.do("GET", base+"/instruments", nil, admin)
	var list []instrumentObject
	e.decode(rec, &list)
	if len(list) != 2 || list[0].Name != "follow_up" || list[0].Position != 1 {
		t.Fatalf("listing after rename: %s", rec.Body.String())
	}
	rec = e.do("GET", base+"/instrument-event-mapping", nil, admin)
	var mapping []armMappingObject
	e.decode(rec, &mapping)
	if len(mapping[0].Mapping["follow_up"]) != 1 || len(mapping[0].Mapping["intake"]) != 0 {
		t.Fatalf("mapping after rename: %s", rec.Body.String())
	}
}

// TestInstrumentRenameStaged renames through the endpoint while a staging set
// is open (production mode): the snapshot answers, the live tables wait for
// commit, and the change classifies as the non-breaking instrument_renamed
// (§4.21, REQ-API-130).
func TestInstrumentRenameStaged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Rename")
	f := e.seedDesign(projectID)

	e.mustProduction(projectID, admin)
	if rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin); rec.Code != http.StatusCreated {
		t.Fatalf("start staging: %d %s", rec.Code, rec.Body.String())
	}

	instURL := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(f.instrumentA)
	rec := e.do("PUT", instURL, map[string]any{"name": "checkin"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("staged rename: %d %s", rec.Code, rec.Body.String())
	}
	var obj instrumentObject
	e.decode(rec, &obj)
	if obj.Name != "checkin" {
		t.Fatalf("staged object: %+v", obj)
	}

	// The staging diff names the rename as non-breaking.
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin)
	if !strings.Contains(rec.Body.String(), "instrument_renamed") {
		t.Fatalf("staging diff: %s", rec.Body.String())
	}

	// The live tables keep the old name until commit.
	if live, err := e.Store.GetInstrument(ctx, f.instrumentA); err != nil || live.Name != "intake" {
		t.Fatalf("live instrument before commit: %+v (%v)", live, err)
	}

	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	live, err := e.Store.GetInstrument(ctx, f.instrumentA)
	if err != nil || live.Name != "checkin" {
		t.Fatalf("live instrument after commit: %+v (%v)", live, err)
	}

	// The mapping pairs followed the name through commit.
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instrument-event-mapping", nil, admin)
	var mapping []armMappingObject
	e.decode(rec, &mapping)
	if len(mapping[0].Mapping["checkin"]) != 1 {
		t.Fatalf("mapping after commit: %s", rec.Body.String())
	}
}

// --- fields (§4.11) ---

func TestFieldValidationRules(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Field Study")
	instID := e.mustInstrument(projectID, "intake")
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	post := func(body map[string]any) int {
		return e.do("POST", base, body, admin).Code
	}

	if code := post(map[string]any{"field_name": "Bad Name", "field_type": "text"}); code != http.StatusBadRequest {
		t.Fatalf("bad field_name: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "redcap_event_name", "field_type": "text"}); code != http.StatusBadRequest {
		t.Fatalf("reserved name: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "unknown"}); code != http.StatusBadRequest {
		t.Fatalf("bad type: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "text", "validation_type": "nope"}); code != http.StatusBadRequest {
		t.Fatalf("unknown validation_type: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "radio", "choices": "1$yes##2$"}); code != http.StatusBadRequest {
		t.Fatalf("empty choice label: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "radio", "choices": "1$yes##x$no"}); code != http.StatusBadRequest {
		t.Fatalf("non-numeric choice code: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "text", "validation_type": "integer", "validation_min": "1.5"}); code != http.StatusBadRequest {
		t.Fatalf("non-integer min: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "text", "validation_type": "integer", "validation_min": "10", "validation_max": "2"}); code != http.StatusBadRequest {
		t.Fatalf("min > max: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "a", "field_type": "text", "calculation": "[e][x]"}); code != http.StatusBadRequest {
		t.Fatalf("calculation on text: %d, want 400", code)
	}
	if code := post(map[string]any{"field_name": "total", "field_type": "calculated"}); code != http.StatusBadRequest {
		t.Fatalf("calculated without expression: %d, want 400", code)
	}

	// A valid field stores min/max as strings and round-trips.
	rec := e.do("POST", base, map[string]any{
		"field_name": "age", "field_type": "text", "validation_type": "integer",
		"validation_min": "0", "validation_max": "120", "required": true,
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid field: %d %s", rec.Code, rec.Body.String())
	}
	var f fieldObject
	e.decode(rec, &f)
	if f.ValidationMin == nil || *f.ValidationMin != "0" || !f.Required {
		t.Fatalf("stored field: %+v", f)
	}
	// Duplicate name within the project → 409.
	rec = e.do("POST", base, map[string]any{"field_name": "age", "field_type": "text"}, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup field: %d, want 409", rec.Code)
	}
}

// --- bulk fields (§4.11, REQ-API-132) ---

func TestCreateFieldsBulk(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Bulk Study")
	instID := e.mustInstrument(projectID, "intake")
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	count := func() int {
		fs, err := e.Store.ListFieldsByInstrument(context.Background(), projectID, instID)
		if err != nil {
			t.Fatalf("list fields: %v", err)
		}
		return len(fs)
	}

	rec := e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "a", "field_type": "text"},
		{"field_name": "b", "field_type": "text", "required": true},
		{"field_name": "c", "field_type": "dropdown", "choices": "1$yes##2$no"},
	}}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bulk create: %d %s", rec.Code, rec.Body.String())
	}
	var objs []fieldObject
	e.decode(rec, &objs)
	if len(objs) != 3 || objs[0].FieldName != "a" || objs[2].Position != 3 {
		t.Fatalf("bulk objects = %+v", objs)
	}
	fields, err := e.Store.ListFieldsByInstrument(context.Background(), projectID, instID)
	if err != nil || len(fields) != 3 || fields[0].FieldName != "a" || fields[2].Position != 3 {
		t.Fatalf("stored fields = %+v (%v)", fields, err)
	}

	// Every created field is audit-logged like the single-field endpoint.
	created := 0
	for _, ty := range e.auditTypes() {
		if ty == audit.FieldCreated {
			created++
		}
	}
	if created != 3 {
		t.Fatalf("field_created entries = %d, want 3 (%v)", created, e.auditTypes())
	}

	// One invalid entry rejects the whole batch — nothing is created.
	rec = e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "d", "field_type": "text"},
		{"field_name": "Bad E", "field_type": "text"},
	}}, admin)
	if rec.Code != http.StatusBadRequest || count() != 3 {
		t.Fatalf("invalid entry: %d, count %d — want 400 and no fields", rec.Code, count())
	}

	// A name used twice inside the batch → 409, nothing created.
	rec = e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "f1", "field_type": "text"},
		{"field_name": "f1", "field_type": "text"},
	}}, admin)
	if rec.Code != http.StatusConflict || count() != 3 {
		t.Fatalf("in-batch dup: %d, count %d — want 409 and no fields", rec.Code, count())
	}

	// A name already in the project → 409.
	rec = e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "a", "field_type": "text"},
	}}, admin)
	if rec.Code != http.StatusConflict || count() != 3 {
		t.Fatalf("existing dup: %d, count %d — want 409", rec.Code, count())
	}

	// Body shape: empty or missing array and unknown attributes → 400.
	for name, body := range map[string]any{
		"empty array":   map[string]any{"fields": []map[string]any{}},
		"missing array": map[string]any{},
		"unknown attr":  map[string]any{"fields": []map[string]any{{"field_name": "x", "field_type": "text"}}, "bogus": 1},
		"unknown in el": map[string]any{"fields": []map[string]any{{"field_name": "x", "field_type": "text", "bogus": 1}}},
		"missing name":  map[string]any{"fields": []map[string]any{{"field_type": "text"}}},
	} {
		rec = e.do("POST", base+"/bulk", body, admin)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bulk %s: %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if count() != 3 {
		t.Fatalf("fields after rejected bodies = %d, want 3", count())
	}
}

// A batch shares one dictionary view: a calculated entry may reference an
// earlier entry of the same call, and a bad reference rejects the whole batch.
func TestCreateFieldsBulkCalc(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Bulk Calc Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	instID := e.mustInstrument(projectID, "labs")
	if err := e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID,
		[]db.InstrumentEvent{{InstrumentID: instID, EventID: eventID}}); err != nil {
		t.Fatalf("map: %v", err)
	}
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	rec := e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "a", "field_type": "text"},
		{"field_name": "total", "field_type": "calculated", "calculation": "[baseline_arm_1][a] * 2"},
	}}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bulk with calc: %d %s", rec.Code, rec.Body.String())
	}
	var deps int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM calculated_dependencies WHERE project_id = ?`, projectID).Scan(&deps); err != nil || deps != 1 {
		t.Fatalf("calc dependencies = %d (%v), want 1", deps, err)
	}

	// A bad reference rejects the whole batch — "x" is not created either.
	rec = e.do("POST", base+"/bulk", map[string]any{"fields": []map[string]any{
		{"field_name": "x", "field_type": "text"},
		{"field_name": "bad", "field_type": "calculated", "calculation": "[baseline_arm_1][nope]"},
	}}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad calc reference: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	fields, err := e.Store.ListFieldsByInstrument(context.Background(), projectID, instID)
	if err != nil || len(fields) != 2 {
		t.Fatalf("fields after rejected batch = %+v (%v), want the two from before", fields, err)
	}
}

func TestFieldRenameRecomputeAndDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Calc Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	instID := e.mustInstrument(projectID, "labs")
	if err := e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID,
		[]db.InstrumentEvent{{InstrumentID: instID, EventID: eventID}}); err != nil {
		t.Fatalf("map: %v", err)
	}
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	rec := e.do("POST", base, map[string]any{"field_name": "a", "field_type": "text"}, admin)
	var a fieldObject
	e.decode(rec, &a)
	rec = e.do("POST", base, map[string]any{
		"field_name": "total", "field_type": "calculated", "calculation": "[baseline_arm_1][a] * 2",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("calc field: %d %s", rec.Code, rec.Body.String())
	}
	var total fieldObject
	e.decode(rec, &total)

	// A record with a = 5; changing the expression recomputes every record
	// in the same transaction (§6.3, ASM-VAL-4).
	if _, err := e.Store.DB.Exec(
		`INSERT INTO record_entities (project_id, record_id, created_at) VALUES (?, 'R001', '2026-01-01 00:00:00')`,
		projectID); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'baseline_arm_1', '', 1, 'a', '5')`, projectID); err != nil {
		t.Fatalf("data: %v", err)
	}
	rec = e.do("PUT", base+"/"+itoa(total.ID), map[string]any{
		"calculation": "[baseline_arm_1][a] + 100",
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("change expression: %d %s", rec.Code, rec.Body.String())
	}
	var stored string
	err := e.Store.DB.QueryRow(
		`SELECT value FROM data WHERE project_id = ? AND record_id = 'R001' AND field_name = 'total'`,
		projectID).Scan(&stored)
	if err != nil || stored != "105" {
		t.Fatalf("recomputed value = %q (%v), want 105", stored, err)
	}

	// Deleting a referenced field is rejected (§6.2 invariant).
	rec = e.do("DELETE", base+"/"+itoa(a.ID), nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete referenced: %d, want 409", rec.Code)
	}

	// Renaming the calculated field moves its stored values (REQ-VAL-014).
	rec = e.do("PUT", base+"/"+itoa(total.ID), map[string]any{"field_name": "sum_total"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	if err := e.Store.DB.QueryRow(
		`SELECT value FROM data WHERE project_id = ? AND record_id = 'R001' AND field_name = 'sum_total'`,
		projectID).Scan(&stored); err != nil || stored != "105" {
		t.Fatalf("renamed values: %q (%v)", stored, err)
	}

	// Deleting the calculated field removes its values and audits the count.
	rec = e.do("DELETE", base+"/"+itoa(total.ID), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete calc: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	e.Store.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = 'sum_total'`,
		projectID).Scan(&n)
	if n != 0 {
		t.Fatalf("values not removed: %d", n)
	}
	var saw bool
	rows, _ := e.Store.DB.Query(`SELECT details FROM audit_events WHERE event_type = 'field_deleted'`)
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && contains(d, `"values_removed":1`) {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("field_deleted audit missing values_removed")
	}
}

func TestCalcCycleRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Cycle Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	instID := e.mustInstrument(projectID, "labs")
	e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID,
		[]db.InstrumentEvent{{InstrumentID: instID, EventID: eventID}})
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	// y starts as a plain text field; x is calculated over it.
	rec := e.do("POST", base, map[string]any{"field_name": "y", "field_type": "text"}, admin)
	var y fieldObject
	e.decode(rec, &y)
	rec = e.do("POST", base, map[string]any{
		"field_name": "x", "field_type": "calculated", "calculation": "[baseline_arm_1][y] + 1",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create x: %d %s", rec.Code, rec.Body.String())
	}
	var x fieldObject
	e.decode(rec, &x)

	// Turning y into a calculated field over x closes the cycle (REQ-VAL-035).
	rec = e.do("PUT", base+"/"+itoa(y.ID), map[string]any{
		"field_type": "calculated", "calculation": "[baseline_arm_1][x] + 1",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cycle: %d, want 400", rec.Code)
	}
	// Self-reference is a cycle too.
	rec = e.do("PUT", base+"/"+itoa(x.ID), map[string]any{
		"calculation": "[baseline_arm_1][x] + 1",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-cycle: %d, want 400", rec.Code)
	}
	// A reference to a field that does not exist is rejected outright.
	rec = e.do("POST", base, map[string]any{
		"field_name": "z", "field_type": "calculated", "calculation": "[baseline_arm_1][ghost] + 1",
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dangling ref: %d, want 400", rec.Code)
	}
}

func TestGD8IdentifierInvariant(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("GD8 Study")
	instID := e.mustInstrument(projectID, "intake")
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	rec := e.do("POST", base, map[string]any{"field_name": "record_id_field", "field_type": "text"}, admin)
	var f1 fieldObject
	e.decode(rec, &f1)
	rec = e.do("POST", base, map[string]any{"field_name": "second", "field_type": "text"}, admin)
	var f2 fieldObject
	e.decode(rec, &f2)

	// Without records the identifier may still move.
	order := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields/order"
	rec = e.do("PUT", order, map[string]any{"order": []int64{f2.ID, f1.ID}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder empty project: %d %s", rec.Code, rec.Body.String())
	}
	// Back to f1 first; then with a record present the swap is rejected.
	e.do("PUT", order, map[string]any{"order": []int64{f1.ID, f2.ID}}, admin)
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'baseline_arm_1', '', 1, 'record_id_field', 'x')`, projectID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec = e.do("PUT", order, map[string]any{"order": []int64{f2.ID, f1.ID}}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("gd8 violation: %d, want 400", rec.Code)
	}
}

// --- validation types (§4.11) ---

func TestValidationTypesListing(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	rec := e.do("GET", "/api/v1/validationTypes", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("validationTypes: %d", rec.Code)
	}
	var out []validationTypeObject
	e.decode(rec, &out)
	byName := map[string]validationTypeObject{}
	for _, o := range out {
		byName[o.Name] = o
	}
	if got := byName["integer"]; !got.Builtin || got.Regex == "" {
		t.Fatalf("integer builtin: %+v", got)
	}
	if got := byName["date"]; !got.Builtin {
		t.Fatalf("date builtin: %+v", got)
	}
	// The seeded registry rows carry their patterns (they ship builtin in the
	// migration but must not shadow the four structured types).
	if got := byName["email"]; got.Regex == "" {
		t.Fatalf("registry email: %+v", got)
	}
	// Any authenticated user may read it; anonymous is rejected.
	rec = e.do("GET", "/api/v1/validationTypes", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous: %d, want 403", rec.Code)
	}
}

// --- design-time test action (§4.11) ---

func TestTestFieldEndpoint(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("TestExpr Study")
	armID := e.mustArm(projectID, 1)
	eventID := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	instID := e.mustInstrument(projectID, "labs")
	e.Store.SetInstrumentEventsForArm(context.Background(), projectID, armID,
		[]db.InstrumentEvent{{InstrumentID: instID, EventID: eventID}})
	base := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instID) + "/fields"

	rec := e.do("POST", base, map[string]any{"field_name": "a", "field_type": "text"}, admin)
	var a fieldObject
	e.decode(rec, &a)
	rec = e.do("POST", base, map[string]any{
		"field_name": "total", "field_type": "calculated", "calculation": "[baseline_arm_1][a] * 2",
	}, admin)
	var total fieldObject
	e.decode(rec, &total)

	if _, err := e.Store.DB.Exec(
		`INSERT INTO record_entities (project_id, record_id, created_at) VALUES (?, 'R001', '2026-01-01 00:00:00')`,
		projectID); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := e.Store.DB.Exec(
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, 'R001', 'baseline_arm_1', '', 1, 'a', '7')`, projectID); err != nil {
		t.Fatalf("data: %v", err)
	}

	testURL := "/api/v1/projects/" + itoa(projectID) + "/records/R001/fields/" + itoa(total.ID) + "/test"
	rec = e.do("POST", testURL, map[string]any{}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Value    string `json:"value"`
		Problems []struct {
			Operand string `json:"operand"`
			Problem string `json:"problem"`
		} `json:"problems"`
	}
	e.decode(rec, &out)
	if out.Value != "14" || len(out.Problems) != 0 {
		t.Fatalf("test result: %+v", out)
	}

	// A draft expression with a missing operand reports the problem and
	// stores nothing (§6.4).
	rec = e.do("POST", testURL, map[string]any{
		"expression": "[baseline_arm_1][a] / ([baseline_arm_1][a] - 7)",
	}, admin)
	e.decode(rec, &out)
	if out.Value != "" || len(out.Problems) != 1 || out.Problems[0].Problem != "division_by_zero" {
		t.Fatalf("draft problems: %s", rec.Body.String())
	}
	var n int
	e.Store.DB.QueryRow(`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = 'total'`,
		projectID).Scan(&n)
	if n != 0 {
		t.Fatalf("test action stored values: %d rows", n)
	}

	// A non-calculated field → 400.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/records/R001/fields/"+itoa(a.ID)+"/test", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-calculated test: %d, want 400", rec.Code)
	}
}

// --- instrument–event mapping (§4.12) ---

func TestInstrumentEventMapping(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Map Study")
	armID := e.mustArm(projectID, 1)
	e1 := e.mustEvent(projectID, armID, "baseline", "baseline_arm_1")
	e2 := e.mustEvent(projectID, armID, "followup", "followup_arm_1")
	e.mustInstrument(projectID, "intake")
	e.mustInstrument(projectID, "scores")

	url := "/api/v1/projects/" + itoa(projectID) + "/instrument-event-mapping"
	rec := e.do("GET", url, nil, admin)
	var listing []armMappingObject
	e.decode(rec, &listing)
	if len(listing) != 1 || len(listing[0].Mapping["intake"]) != 0 || len(listing[0].Mapping["scores"]) != 0 {
		t.Fatalf("empty mapping: %s", rec.Body.String())
	}

	rec = e.do("PUT", url, map[string]any{
		"arm_num": 1, "mapping": map[string][]string{"intake": {"baseline_arm_1", "followup_arm_1"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("put mapping: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", url, nil, admin)
	e.decode(rec, &listing)
	if len(listing[0].Mapping["intake"]) != 2 || len(listing[0].Mapping["scores"]) != 0 {
		t.Fatalf("after put: %s", rec.Body.String())
	}

	// Unknown names → 400 invalid_request.
	rec = e.do("PUT", url, map[string]any{
		"arm_num": 1, "mapping": map[string][]string{"nope": {}},
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown instrument: %d, want 400", rec.Code)
	}
	rec = e.do("PUT", url, map[string]any{
		"arm_num": 1, "mapping": map[string][]string{"intake": {"ghost_arm_9"}},
	}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown event: %d, want 400", rec.Code)
	}
	_ = e1
	_ = e2

	var saw bool
	rows, _ := e.Store.DB.Query(`SELECT details FROM audit_events WHERE event_type = 'mapping_updated'`)
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && contains(d, `"intake"`) && !contains(d, `"scores"`) {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("mapping_updated audit should list only the changed instrument")
	}
}
