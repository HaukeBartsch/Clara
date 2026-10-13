package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

func moveURL(projectID, instrumentID, fieldID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(instrumentID) +
		"/fields/" + itoa(fieldID) + "/move"
}

// fieldOrder reads an instrument's field names in position order, through the
// listing (the staged design while a set is open).
func (e *env) fieldOrder(t *testing.T, projectID, instrumentID int64, actor *db.User) string {
	t.Helper()
	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(instrumentID)+"/fields", nil, actor)
	if rec.Code != http.StatusOK {
		t.Fatalf("list fields: %d %s", rec.Code, rec.Body.String())
	}
	var fields []fieldObject
	e.decode(rec, &fields)
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.FieldName)
	}
	return strings.Join(names, ",")
}

// storedValue reads one stored value at its slot; "" with found false when none.
func (e *env) storedValue(t *testing.T, projectID int64, record, event, repeating string, instance int, field string) (string, bool) {
	t.Helper()
	var v string
	err := e.Store.DB.QueryRow(
		`SELECT value FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ?
		 AND repeating_instrument = ? AND repeating_instance_number = ? AND field_name = ?`,
		projectID, record, event, repeating, instance, field).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// TestMoveField covers POST …/fields/{fid}/move on the live design
// (REQ-API-157): across instruments before a field and to the end, within an
// instrument, the no-op, the refusals, the audit entry, and that the stored
// values stay where they are.
func TestMoveField(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Move Study")
	f := e.seedDesign(projectID) // intake: record_id, age, age_plus · labs: hb
	age := e.mustFieldID(projectID, "age")
	hb := e.mustFieldID(projectID, "hb")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "age", "41")

	// age → labs, before hb.
	rec := e.do("POST", moveURL(projectID, f.instrumentA, age), map[string]any{"instrument_id": f.instrumentB, "before_field_id": hb}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("move across: %d %s", rec.Code, rec.Body.String())
	}
	var obj fieldObject
	e.decode(rec, &obj)
	if obj.ID != age || obj.FieldName != "age" || obj.Position != 1 {
		t.Fatalf("moved field object: %+v", obj)
	}
	if got := e.fieldOrder(t, projectID, f.instrumentB, admin); got != "age,hb" {
		t.Fatalf("labs after the move: %s", got)
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus" {
		t.Fatalf("intake after the move: %s", got)
	}
	e.expectDenseFieldPositions(t, projectID)
	if v, ok := e.storedValue(t, projectID, "R1", "baseline_arm_1", "", 1, "age"); !ok || v != "41" {
		t.Fatalf("stored value after the move: %q %v", v, ok)
	}
	details := e.auditDetails(audit.FieldMoved, projectID)
	if len(details) != 1 || details[0]["field"] != "age" || details[0]["from_instrument"] != "intake" ||
		details[0]["to_instrument"] != "labs" || details[0]["before"] != "hb" || details[0]["position"] != float64(1) {
		t.Fatalf("field_moved audit: %v", details)
	}

	// Back to intake, last (no before_field_id).
	rec = e.do("POST", moveURL(projectID, f.instrumentB, age), map[string]any{"instrument_id": f.instrumentA}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("move to the end: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus,age" {
		t.Fatalf("intake after moving back: %s", got)
	}
	if got := e.fieldOrder(t, projectID, f.instrumentB, admin); got != "hb" {
		t.Fatalf("labs after moving back: %s", got)
	}

	// Within its own instrument; then the same request again changes nothing.
	agePlus := e.mustFieldID(projectID, "age_plus")
	within := map[string]any{"instrument_id": f.instrumentA, "before_field_id": agePlus}
	if rec := e.do("POST", moveURL(projectID, f.instrumentA, age), within, admin); rec.Code != http.StatusOK {
		t.Fatalf("move within: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age,age_plus" {
		t.Fatalf("intake after moving within: %s", got)
	}
	if rec := e.do("POST", moveURL(projectID, f.instrumentA, age), within, admin); rec.Code != http.StatusOK {
		t.Fatalf("repeat: %d %s", rec.Code, rec.Body.String())
	}
	if n := len(e.auditDetails(audit.FieldMoved, projectID)); n != 3 {
		t.Fatalf("field_moved entries = %d, want 3 (the repeat writes nothing)", n)
	}

	// Refusals.
	for _, c := range []struct {
		name string
		inst int64
		body map[string]any
		code int
	}{
		{"no target", f.instrumentA, map[string]any{}, http.StatusBadRequest},
		{"unknown attribute", f.instrumentA, map[string]any{"instrument_id": f.instrumentB, "position": 2}, http.StatusBadRequest},
		{"unknown target", f.instrumentA, map[string]any{"instrument_id": 99999}, http.StatusBadRequest},
		{"before a field of another instrument", f.instrumentA, map[string]any{"instrument_id": f.instrumentB, "before_field_id": agePlus}, http.StatusBadRequest},
		{"before itself", f.instrumentA, map[string]any{"instrument_id": f.instrumentA, "before_field_id": age}, http.StatusBadRequest},
		{"field not in the path's instrument", f.instrumentB, map[string]any{"instrument_id": f.instrumentA}, http.StatusNotFound},
	} {
		if rec := e.do("POST", moveURL(projectID, c.inst, age), c.body, admin); rec.Code != c.code {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, rec.Body.String(), c.code)
		}
	}
	member := e.mustUser("member@example.org")
	e.mustMemberWithRole(projectID, member, mustRole(t, e, projectID, "data-entry"))
	if rec := e.do("POST", moveURL(projectID, f.instrumentA, age), map[string]any{"instrument_id": f.instrumentB}, member); rec.Code != http.StatusForbidden {
		t.Errorf("member without project_admin: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age,age_plus" {
		t.Fatalf("a refused move changed intake: %s", got)
	}
}

// TestMoveFieldKeepsIdentifier: while records exist the record identifier —
// the first field of the first instrument — stays where it is (GD-8); a
// project without records may move it.
func TestMoveFieldKeepsIdentifier(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Identifier Study")
	f := e.seedDesign(projectID)
	recordID := e.mustFieldID(projectID, "record_id")
	hb := e.mustFieldID(projectID, "hb")
	e.mustRecord(projectID, "R1")

	if rec := e.do("POST", moveURL(projectID, f.instrumentA, recordID), map[string]any{"instrument_id": f.instrumentB}, admin); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "GD-8") {
		t.Fatalf("moving the identifier away: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", moveURL(projectID, f.instrumentB, hb), map[string]any{"instrument_id": f.instrumentA, "before_field_id": recordID}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("moving a field in front of the identifier: %d %s", rec.Code, rec.Body.String())
	}
	// Behind it is fine.
	age := e.mustFieldID(projectID, "age")
	if rec := e.do("POST", moveURL(projectID, f.instrumentB, hb), map[string]any{"instrument_id": f.instrumentA, "before_field_id": age}, admin); rec.Code != http.StatusOK {
		t.Fatalf("moving a field behind the identifier: %d %s", rec.Code, rec.Body.String())
	}

	empty := e.mustProject("Identifier Study, no records")
	g := e.seedDesign(empty)
	if rec := e.do("POST", moveURL(empty, g.instrumentA, e.mustFieldID(empty, "record_id")), map[string]any{"instrument_id": g.instrumentB}, admin); rec.Code != http.StatusOK {
		t.Fatalf("no records yet: %d %s", rec.Code, rec.Body.String())
	}
}

// TestMoveFieldStaged: in production the move lands on the open set, the diff
// names it once as a non-breaking field_moved (the fields it shifts are not
// "reordered"), and the commit applies it with the values in place.
func TestMoveFieldStaged(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Move")
	f := e.seedDesign(projectID)
	age := e.mustFieldID(projectID, "age")
	hb := e.mustFieldID(projectID, "hb")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "age", "41")
	e.mustProduction(projectID, admin)
	base := "/api/v1/projects/" + itoa(projectID)
	if code := e.do("POST", base+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	if rec := e.do("POST", moveURL(projectID, f.instrumentA, age), map[string]any{"instrument_id": f.instrumentB, "before_field_id": hb}, admin); rec.Code != http.StatusOK {
		t.Fatalf("staged move: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentB, admin); got != "age,hb" {
		t.Fatalf("staged labs: %s", got)
	}
	var instrumentID int64
	if err := e.Store.DB.QueryRow(`SELECT instrument_id FROM fields WHERE id = ?`, age).Scan(&instrumentID); err != nil || instrumentID != f.instrumentA {
		t.Fatalf("the live row moved before commit: %d %v", instrumentID, err)
	}

	rec := e.do("GET", base+"/staging", nil, admin)
	var state stagingState
	e.decode(rec, &state)
	if len(state.Changes) != 1 || state.Changes[0].Kind != "field_moved" || state.Changes[0].Breaking ||
		state.Changes[0].Object != "intake.age → labs.age" {
		t.Fatalf("staged diff: %+v", state.Changes)
	}

	if rec := e.do("POST", base+"/staging/commit", nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentB, admin); got != "age,hb" {
		t.Fatalf("live labs after commit: %s", got)
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus" {
		t.Fatalf("live intake after commit: %s", got)
	}
	e.expectDenseFieldPositions(t, projectID)
	if v, ok := e.storedValue(t, projectID, "R1", "baseline_arm_1", "", 1, "age"); !ok || v != "41" {
		t.Fatalf("stored value after commit: %q %v", v, ok)
	}
}

// TestMoveFieldStagedIntoNewOutOfDeleted: one set creates an instrument, moves
// fields into it — one out of an instrument the set then deletes, one renamed
// on the way — and the commit applies all of it: the deleted instrument goes
// only after its field has left it, taking the further instances stored under
// its name (REQ-DB-049), which makes its deletion breaking.
func TestMoveFieldStagedIntoNewOutOfDeleted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Restructure")
	f := e.seedDesign(projectID)
	base := "/api/v1/projects/" + itoa(projectID)
	age := e.mustFieldID(projectID, "age")
	hb := e.mustFieldID(projectID, "hb")
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"followup_arm_2"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("labs repeating: %d %s", rec.Code, rec.Body.String())
	}
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "age", "41")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "hb", "9.1")
	e.mustInstanceValue(projectID, "R1", "followup_arm_2", "labs", 2, "hb", "8.7")
	// age_plus reads hb; point it at age so labs can go without an expression naming it.
	if _, err := e.Store.DB.ExecContext(ctx, `UPDATE fields SET calculation = '[baseline_arm_1][age] + 1' WHERE id = ?`, f.calcField); err != nil {
		t.Fatal(err)
	}
	e.mustProduction(projectID, admin)
	if code := e.do("POST", base+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	rec := e.do("POST", base+"/instruments", map[string]any{"name": "visits"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create visits: %d %s", rec.Code, rec.Body.String())
	}
	var visits instrumentObject
	e.decode(rec, &visits)
	if visits.ID >= 0 {
		t.Fatalf("a staged instrument should carry a provisional id: %+v", visits)
	}
	if rec := e.do("POST", moveURL(projectID, f.instrumentB, hb), map[string]any{"instrument_id": visits.ID}, admin); rec.Code != http.StatusOK {
		t.Fatalf("hb → visits: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("DELETE", base+"/instruments/"+itoa(f.instrumentB), nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete labs: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", moveURL(projectID, f.instrumentA, age), map[string]any{"instrument_id": visits.ID, "before_field_id": hb}, admin); rec.Code != http.StatusOK {
		t.Fatalf("age → visits: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("PUT", base+"/instruments/"+itoa(visits.ID)+"/fields/"+itoa(age), map[string]any{"field_name": "age_years"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename age: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do("GET", base+"/staging", nil, admin)
	var state stagingState
	e.decode(rec, &state)
	kinds := map[string]bool{}
	for _, c := range state.Changes {
		kinds[c.Kind] = true
		if c.Kind == "instrument_deleted" && !c.Breaking {
			t.Errorf("deleting labs drops its further instances, so it is breaking: %+v", c)
		}
		if c.Kind == "reordered" {
			t.Errorf("a move shifts positions without reordering anything: %+v", c)
		}
	}
	for _, want := range []string{"instrument_added", "instrument_deleted", "field_moved", "field_renamed"} {
		if !kinds[want] {
			t.Errorf("diff lacks %s: %+v", want, state.Changes)
		}
	}

	if rec := e.do("POST", base+"/staging/commit", nil, admin); rec.Code != http.StatusConflict {
		t.Fatalf("commit without acknowledgement: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", base+"/staging/commit", map[string]any{"acknowledge_breaking": true}, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	instruments, err := e.Store.ListInstruments(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var visitsID int64
	for _, i := range instruments {
		names = append(names, i.Name)
		if i.Name == "visits" {
			visitsID = i.ID
		}
	}
	if strings.Join(names, ",") != "intake,visits" {
		t.Fatalf("live instruments after commit: %v", names)
	}
	if got := e.fieldOrder(t, projectID, visitsID, admin); got != "age_years,hb" {
		t.Fatalf("live visits: %s", got)
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus" {
		t.Fatalf("live intake: %s", got)
	}
	e.expectDenseFieldPositions(t, projectID)
	if v, ok := e.storedValue(t, projectID, "R1", "baseline_arm_1", "", 1, "age_years"); !ok || v != "41" {
		t.Errorf("renamed value: %q %v", v, ok)
	}
	if v, ok := e.storedValue(t, projectID, "R1", "baseline_arm_1", "", 1, "hb"); !ok || v != "9.1" {
		t.Errorf("first instance of hb: %q %v", v, ok)
	}
	if _, ok := e.storedValue(t, projectID, "R1", "followup_arm_2", "labs", 2, "hb"); ok {
		t.Error("the further instance stored under the deleted labs survived")
	}
}

// TestDeleteInstrumentRemovesItsInstances: the further instances stored under
// an instrument's name go with it — those of a field moved away since included
// (REQ-DB-049) — and count among the removed values.
func TestDeleteInstrumentRemovesItsInstances(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Instance Residue")
	f := e.seedDesign(projectID)
	hb := e.mustFieldID(projectID, "hb")
	e.mustField(projectID, f.instrumentB, "glucose", "text", 2)
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"followup_arm_2"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("labs repeating: %d %s", rec.Code, rec.Body.String())
	}
	e.mustInstanceValue(projectID, "R1", "followup_arm_2", "labs", 2, "hb", "8.7")
	if rec := e.do("POST", moveURL(projectID, f.instrumentB, hb), map[string]any{"instrument_id": f.instrumentA}, admin); rec.Code != http.StatusOK {
		t.Fatalf("hb → intake: %d %s", rec.Code, rec.Body.String())
	}
	// Kept while labs exists: moving hb back would show it again.
	if _, ok := e.storedValue(t, projectID, "R1", "followup_arm_2", "labs", 2, "hb"); !ok {
		t.Fatal("the move dropped the further instance")
	}
	if rec := e.do("DELETE", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(f.instrumentB), nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete labs: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := e.storedValue(t, projectID, "R1", "followup_arm_2", "labs", 2, "hb"); ok {
		t.Error("the further instance outlived its instrument")
	}
	details := e.auditDetails(audit.InstrumentDeleted, projectID)
	if len(details) != 1 || details[0]["values_removed"] != float64(1) {
		t.Fatalf("instrument_deleted: %v", details)
	}
}

// TestMoveFieldRecomputesInstances: a field moved into a repeating instrument
// is read per instance by that instrument's calculations (REQ-VAL-045), so the
// move recomputes them — and a further-instance value kept under the target's
// name from an earlier stay there shows again.
func TestMoveFieldRecomputesInstances(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Move Recompute")
	f := e.seedDesign(projectID)
	weight := e.mustField(projectID, f.instrumentA, "weight", "text", 4)
	e.mustCalcField(projectID, f.instrumentB, "dose", "[baseline_arm_1][weight] * 2", 2)
	if rec := e.do("PUT", repeatingURL(projectID, f.instrumentB), map[string]any{"events": []string{"baseline_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("labs repeating: %d %s", rec.Code, rec.Body.String())
	}
	e.mustRecord(projectID, "R1")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "weight", "10")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "", 1, "dose", "20")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 2, "hb", "8.7")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 2, "dose", "20")
	e.mustInstanceValue(projectID, "R1", "baseline_arm_1", "labs", 2, "weight", "7") // hidden while weight is on intake

	if rec := e.do("POST", moveURL(projectID, f.instrumentA, weight), map[string]any{"instrument_id": f.instrumentB}, admin); rec.Code != http.StatusOK {
		t.Fatalf("weight → labs: %d %s", rec.Code, rec.Body.String())
	}
	if v, _ := e.storedValue(t, projectID, "R1", "baseline_arm_1", "", 1, "dose"); v != "20" {
		t.Errorf("first instance: dose = %q, want 20", v)
	}
	if v, _ := e.storedValue(t, projectID, "R1", "baseline_arm_1", "labs", 2, "dose"); v != "14" {
		t.Errorf("instance 2: dose = %q, want 14 (its own weight)", v)
	}
	if len(e.auditDetails(audit.CalculatedRecomputed, projectID)) != 1 {
		t.Errorf("calculated_recomputed: %v", e.auditDetails(audit.CalculatedRecomputed, projectID))
	}
}

// TestStagedAddAfterDelete: a field added to an instrument after one of its
// fields was deleted in the same set takes the position after the highest —
// not after the count, which collided with the last field at commit.
func TestStagedAddAfterDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Delete Add")
	f := e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	base := "/api/v1/projects/" + itoa(projectID)
	if code := e.do("POST", base+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}
	fields := base + "/instruments/" + itoa(f.instrumentA) + "/fields"
	if rec := e.do("DELETE", fields+"/"+itoa(e.mustFieldID(projectID, "age")), nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", fields, map[string]any{"field_name": "weight", "field_type": "text"}, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus,weight" {
		t.Fatalf("staged intake: %s", got)
	}
	if rec := e.do("POST", base+"/staging/commit", map[string]any{"acknowledge_breaking": true}, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.fieldOrder(t, projectID, f.instrumentA, admin); got != "record_id,age_plus,weight" {
		t.Fatalf("live intake: %s", got)
	}
}
