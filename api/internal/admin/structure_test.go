package admin

import (
	"context"
	"net/http"
	"testing"

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

	// An arm with an event cannot be deleted (DEV-API-6).
	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/events", map[string]any{
		"arm_num": 1, "event_name": "visit",
	}, admin)
	var ev EventObject
	e.decode(rec, &ev)
	armRow, err := e.Store.GetArm(context.Background(), armIDOfEvent(t, e, ev.ID))
	if err != nil || armRow == nil {
		t.Fatalf("load arm: %v", err)
	}
	rec = e.do("DELETE", "/api/v1/arms/"+itoa(armRow.ID), nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete busy arm: %d, want 409", rec.Code)
	}

	// The second (empty) arm deletes cleanly with an audit entry.
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/arms", map[string]any{"name": "B"}, admin)
	var arm2 armObject
	e.decode(rec, &arm2)
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
// mapping pairs (204 + audit); the last remaining event of the project cannot
// be deleted (409 — rename and reorder stay available, REQ-API-125).
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

	// The last remaining event cannot be deleted.
	rec := e.do("DELETE", "/api/v1/events/"+itoa(baseID), nil, admin)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "at least one event") {
		t.Fatalf("delete last event: %d %s, want 409", rec.Code, rec.Body.String())
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
