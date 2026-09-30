package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// mustProduction puts a project into production mode through the mode endpoint
// (an is_admin call — REQ-API-105), the precondition for staging.
func (e *env) mustProduction(projectID int64, admin *db.User) {
	e.t.Helper()
	rec := e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/mode",
		map[string]any{"mode": "production", "keep_data": true}, admin)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("enter production: %d (%s)", rec.Code, rec.Body.String())
	}
}

// stageEdit applies a mutation to the open set's snapshot and writes it back —
// what a structure endpoint in production mode does once its handler branch is
// wired, expressed directly so the lifecycle and commit can be tested alone.
func (e *env) stageEdit(t *testing.T, projectID int64, mutate func(*stagedDesign)) {
	t.Helper()
	ctx := context.Background()
	set, err := e.Store.GetStaging(ctx, projectID)
	if err != nil || set == nil {
		t.Fatalf("no staging set open: %v", err)
	}
	d, err := parseStagedDesign(set.Design)
	if err != nil {
		t.Fatalf("parse staged design: %v", err)
	}
	mutate(d)
	doc, err := d.encode()
	if err != nil {
		t.Fatalf("encode staged design: %v", err)
	}
	if _, err := e.Store.UpdateStagingDesign(ctx, projectID, doc); err != nil {
		t.Fatalf("UpdateStagingDesign: %v", err)
	}
}

// TestStagingLifecycleStartGetDiscard covers the set's life in production mode:
// it opens once, reads back its state and diff, and discards without touching
// the design (REQ-API-106).
func TestStagingLifecycleStartGetDiscard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Lifecycle")
	f := e.seedDesign(projectID)

	// Nothing open yet.
	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET staging = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var closed stagingState
	e.decode(rec, &closed)
	if closed.Open || len(closed.Changes) != 0 {
		t.Errorf("state before start = %+v, want closed with no changes", closed)
	}

	// Development mode refuses a set.
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusConflict {
		t.Errorf("start in development = %d, want 409", code)
	}

	e.mustProduction(projectID, admin)
	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	var started stagingObject
	e.decode(rec, &started)
	if started.OpenedAt == "" || started.OpenedBy != admin.ID {
		t.Errorf("start response = %+v, want opened_at and opened_by %d", started, admin.ID)
	}

	// A second set is refused.
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusConflict {
		t.Errorf("second start = %d, want 409", code)
	}

	// Stage a field and read the diff.
	e.stageEdit(t, projectID, func(d *stagedDesign) {
		if _, ok := d.putField(f.instrumentA, newTextField("blood_pressure")); !ok {
			t.Fatal("putField failed")
		}
	})
	rec = e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin)
	var state stagingState
	e.decode(rec, &state)
	if !state.Open || state.OpenedAt == nil {
		t.Fatalf("state after edit = %+v, want open with opened_at", state)
	}
	if len(state.Changes) != 1 || state.Changes[0].Kind != "field_added" || state.Changes[0].Breaking {
		t.Errorf("changes = %+v, want one non-breaking field_added", state.Changes)
	}

	// The live design is untouched while the set is open (REQ-API-107).
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields = %d while staged, want 4 (the snapshot's four)", got)
	}

	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/discard", nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("discard = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields after discard = %d, want 4", got)
	}
	open, err := e.Store.StagingOpen(ctx, projectID)
	if err != nil || open {
		t.Errorf("staging still open after discard: %v %v", open, err)
	}

	types := e.auditTypes()
	for _, want := range []string{audit.StagingStarted, audit.StagingDiscarded} {
		if !containsType(types, want) {
			t.Errorf("audit is missing %s (events: %v)", want, types)
		}
	}
}

// TestStagingRequiresProjectAdmin: the lifecycle belongs to the project's admin;
// a stranger with no membership is refused (§5).
func TestStagingRequiresProjectAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Permission")
	e.mustProduction(projectID, admin)
	outsider := e.mustUser("outsider@example.org")

	for _, step := range []struct{ method, path string }{
		{"POST", "/api/v1/projects/" + itoa(projectID) + "/staging"},
		{"GET", "/api/v1/projects/" + itoa(projectID) + "/staging"},
		{"POST", "/api/v1/projects/" + itoa(projectID) + "/staging/discard"},
		{"POST", "/api/v1/projects/" + itoa(projectID) + "/staging/commit"},
	} {
		if code := e.do(step.method, step.path, nil, outsider).Code; code != http.StatusForbidden {
			t.Errorf("%s %s by an outsider = %d, want 403", step.method, step.path, code)
		}
	}
}

// TestStagingCommitAddsFields is the happy path: the staged set reaches the live
// tables in one commit, reports its counts, and closes (REQ-API-106/108).
func TestStagingCommitAddsFields(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Commit")
	f := e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	e.stageEdit(t, projectID, func(d *stagedDesign) {
		d.putField(f.instrumentA, newTextField("blood_pressure"))
		inst := d.putInstrument(newInstrument("visits"))
		d.putField(inst.ID, newTextField("visit_note"))
	})

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out commitResponse
	e.decode(rec, &out)
	if out.Applied.Fields != 2 || out.Applied.Instruments != 1 {
		t.Errorf("applied = %+v, want 2 fields and 1 instrument", out.Applied)
	}

	if got := e.countRows("fields", projectID); got != 6 {
		t.Errorf("live fields = %d after commit, want 6", got)
	}
	if got := e.countRows("instruments", projectID); got != 3 {
		t.Errorf("live instruments = %d after commit, want 3", got)
	}
	open, _ := e.Store.StagingOpen(ctx, projectID)
	if open {
		t.Error("the staging set stayed open after commit")
	}

	details := e.auditDetails(audit.StagingCommitted, projectID)
	if len(details) != 1 {
		t.Fatalf("staging_committed entries = %d, want 1", len(details))
	}
	applied, _ := details[0]["applied"].(map[string]any)
	if applied == nil || applied["fields"] == nil {
		t.Errorf("staging_committed details = %v, want an applied object", details[0])
	}
	if _, present := details[0]["breaking_acknowledged"]; present {
		t.Errorf("breaking_acknowledged present for a non-breaking commit: %v", details[0])
	}
}

// TestStagingCommitRequiresAcknowledgement covers the breaking-change gate: the
// first commit lists what breaks and applies nothing; the retry with the flag
// goes through and records what was acknowledged (REQ-API-108, REQ-AUD-025).
func TestStagingCommitRequiresAcknowledgement(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Ack")
	e.seedDesign(projectID)
	e.mustValue(projectID, "REC001", "baseline_arm_1", "", "hb", "9.1")
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	e.stageEdit(t, projectID, func(d *stagedDesign) {
		fld, _ := d.fieldByName("age") // unreferenced, unlike hb
		d.deleteField(fld.ID)
	})

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unacknowledged commit = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	var rejection map[string]any
	e.decode(rec, &rejection)
	listed, _ := rejection["breaking"].([]any)
	if len(listed) == 0 {
		t.Fatalf("rejection lists no breaking changes: %v", rejection)
	}
	first, _ := listed[0].(map[string]any)
	if first["kind"] != "field_deleted" {
		t.Errorf("listed breaking change = %v, want field_deleted", listed[0])
	}
	// Nothing applied, no audit entry (REQ-AUD-004).
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields = %d after the rejected commit, want 4", got)
	}
	if n := e.auditCount(audit.StagingCommitted, projectID); n != 0 {
		t.Errorf("staging_committed entries = %d after a rejected commit, want 0", n)
	}

	rec = e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit",
		map[string]any{"acknowledge_breaking": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledged commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := e.countRows("fields", projectID); got != 3 {
		t.Errorf("live fields = %d after the acknowledged commit, want 3", got)
	}
	details := e.auditDetails(audit.StagingCommitted, projectID)
	if len(details) != 1 {
		t.Fatalf("staging_committed entries = %d, want 1", len(details))
	}
	acknowledged, _ := details[0]["breaking_acknowledged"].([]any)
	if len(acknowledged) == 0 {
		t.Fatalf("details carry no breaking_acknowledged: %v", details[0])
	}
}

// TestStagingCommitMovesRenamedValues asserts the rename semantics survive a
// commit: the field is renamed and its stored values follow it (REQ-VAL-014),
// while an unmapped pair keeps its values in place (§4.12).
func TestStagingCommitMovesRenamedValues(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Rename")
	e.seedDesign(projectID)
	e.mustValue(projectID, "REC001", "baseline_arm_1", "", "hb", "9.1")
	e.mustValue(projectID, "REC002", "followup_arm_2", "", "hb", "8.7")
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	e.stageEdit(t, projectID, func(d *stagedDesign) {
		fld, _ := d.fieldByName("hb")
		fld.FieldName = "hemoglobin"
		// Unmap labs from the follow-up event: values stay, nothing warns.
		d.setMappingForArm(2, map[string][]string{"labs": {}})
	})

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var renamed, old int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = 'hemoglobin'`, projectID).Scan(&renamed); err != nil {
		t.Fatalf("count renamed: %v", err)
	}
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = 'hb'`, projectID).Scan(&old); err != nil {
		t.Fatalf("count old: %v", err)
	}
	if renamed != 2 || old != 0 {
		t.Errorf("values under hemoglobin = %d, hb = %d; want 2 and 0", renamed, old)
	}

	// The unmapped pair is gone from the mapping; both values are still stored.
	var pairs int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM instrument_events ie JOIN events e ON e.id = ie.event_id
		 WHERE e.project_id = ?`, projectID).Scan(&pairs); err != nil {
		t.Fatalf("count mapping: %v", err)
	}
	if pairs != 2 {
		t.Errorf("mapping pairs = %d after commit, want 2 (labs × follow-up unmapped)", pairs)
	}
	var kept int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND unique_event_name = 'followup_arm_2'`, projectID).Scan(&kept); err != nil {
		t.Fatalf("count follow-up values: %v", err)
	}
	if kept != 1 {
		t.Errorf("values at the unmapped event = %d, want 1 (unmap keeps values)", kept)
	}
}

// TestStagingCommitAppliesAtomically checks that a commit which fails part way
// leaves the live design and the staging row exactly as they were: the whole set
// activates at once or not at all (GD-20).
func TestStagingCommitAppliesAtomically(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staging Atomic")
	f := e.seedDesign(projectID)
	// A field an expression names cannot be deleted (the live rule), so the
	// commit must fail — and undo everything else it had applied.
	e.mustSetCalcReference(projectID, f.instrumentA)
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start: %d", code)
	}

	e.stageEdit(t, projectID, func(d *stagedDesign) {
		d.putField(f.instrumentA, newTextField("harmless_first"))
		fld, _ := d.fieldByName("hb")
		d.deleteField(fld.ID)
	})

	rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging/commit", nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("commit = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// seedDesign's four fields plus the calculated hb_twice.
	if got := e.countRows("fields", projectID); got != 5 {
		t.Errorf("live fields = %d after the failed commit, want 5 (nothing applied)", got)
	}
	open, _ := e.Store.StagingOpen(context.Background(), projectID)
	if !open {
		t.Error("the staging set closed on a failed commit")
	}
	if n := e.auditCount(audit.StagingCommitted, projectID); n != 0 {
		t.Errorf("staging_committed entries = %d after a failed commit, want 0", n)
	}
}

// mustSetCalcReference gives instrument intake a calculated field that names
// labs.hb, so deleting hb is refused by the expression guard.
func (e *env) mustSetCalcReference(projectID, instA int64) {
	e.t.Helper()
	calc := e.mustCalcField(projectID, instA, "hb_twice", "[baseline_arm_1][hb] * 2", 4)
	if err := e.Store.AddCalculatedDependency(context.Background(), &db.CalculatedDependency{
		ProjectID: projectID, CalculatedFieldID: calc,
		RefUniqueEventName: "baseline_arm_1", RefFieldName: "hb",
	}); err != nil {
		e.t.Fatalf("AddCalculatedDependency: %v", err)
	}
}

func containsType(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// TestProductionStructureWritesRequireOpenSet is the REQ-API-107 gate on the
// instrument, field and mapping endpoints: with no set open in production every
// one of them is refused, and none of them reaches the live tables.
func TestProductionStructureWritesRequireOpenSet(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Production Gate")
	f := e.seedDesign(projectID)
	hb := e.mustFieldID(projectID, "hb")
	e.mustProduction(projectID, admin)

	base := "/api/v1/projects/" + itoa(projectID)
	for _, step := range []struct {
		method, path string
		body         any
	}{
		{"POST", base + "/instruments", map[string]any{"name": "unsent_instrument"}},
		{"PUT", base + "/instruments/" + itoa(f.instrumentA), map[string]any{"is_survey": true}},
		{"POST", base + "/instruments/" + itoa(f.instrumentA) + "/fields",
			map[string]any{"field_name": "unsent_field", "field_type": "text"}},
		{"PUT", base + "/instruments/" + itoa(f.instrumentB) + "/fields/" + itoa(hb),
			map[string]any{"field_label": "Haemoglobin"}},
		{"DELETE", base + "/instruments/" + itoa(f.instrumentB) + "/fields/" + itoa(hb), nil},
		{"PUT", base + "/instruments/" + itoa(f.instrumentA) + "/fields/order", map[string]any{"order": []int64{3, 1, 2}}},
		{"PUT", base + "/instrument-event-mapping",
			map[string]any{"arm_num": 1, "mapping": map[string][]string{"intake": {}, "labs": {"baseline_arm_1"}}}},
	} {
		rec := e.do(step.method, step.path, step.body, admin)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s = %d, want 409 (%s)", step.method, step.path, rec.Code, rec.Body.String())
		}
	}

	// Nothing landed: the design is exactly what seedDesign built.
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields = %d, want 4 (no structure change applied)", got)
	}
	if got := e.countRows("instruments", projectID); got != 2 {
		t.Errorf("live instruments = %d, want 2", got)
	}
	var surveys int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM instruments WHERE project_id = ? AND is_survey = 1`, projectID).Scan(&surveys); err != nil {
		t.Fatalf("count surveys: %v", err)
	}
	if surveys != 0 {
		t.Errorf("survey instruments = %d, want 0 (the PUT was refused)", surveys)
	}
}

// mustFieldID looks a field up by its project-unique name.
func (e *env) mustFieldID(projectID int64, name string) int64 {
	e.t.Helper()
	f, err := e.Store.GetFieldByName(context.Background(), projectID, name)
	if err != nil || f == nil {
		e.t.Fatalf("GetFieldByName(%s): %v", name, err)
	}
	return f.ID
}

// TestStagedFieldEditsStayInSet covers the field endpoints on the staged path:
// each edit answers as it would live, carries a provisional id, and leaves the
// tables alone until commit (REQ-API-107).
func TestStagedFieldEditsStayInSet(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Fields")
	f := e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start staging: %d", code)
	}

	base := "/api/v1/projects/" + itoa(projectID)
	rec := e.do("POST", base+"/instruments/"+itoa(f.instrumentA)+"/fields",
		map[string]any{"field_name": "blood_pressure", "field_type": "text"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create staged field = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	var created fieldObject
	e.decode(rec, &created)
	if created.ID >= 0 {
		t.Errorf("staged field id = %d, want a provisional negative id", created.ID)
	}

	// The edit is visible to a structure read and invisible to the tables.
	rec = e.do("GET", base+"/instruments/"+itoa(f.instrumentA)+"/fields", nil, admin)
	var listed []fieldObject
	e.decode(rec, &listed)
	if len(listed) != 4 {
		t.Fatalf("staged fields = %d, want 4 (three staged plus the new one)", len(listed))
	}
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields = %d while staged, want 4", got)
	}

	// The provisional id addresses the field: rename it through the endpoint.
	rec = e.do("PUT", base+"/instruments/"+itoa(f.instrumentA)+"/fields/"+itoa(created.ID),
		map[string]any{"field_label": "Blood pressure"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename staged field = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var renamed fieldObject
	e.decode(rec, &renamed)
	if renamed.ID != created.ID || renamed.FieldLabel != "Blood pressure" {
		t.Errorf("renamed = %+v, want id %d with the new label", renamed, created.ID)
	}

	// And delete it: the set is back to the active design, so nothing is pending.
	rec = e.do("DELETE", base+"/instruments/"+itoa(f.instrumentA)+"/fields/"+itoa(created.ID), nil, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete staged field = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", base+"/staging", nil, admin)
	var state stagingState
	e.decode(rec, &state)
	if !state.Open {
		t.Fatal("the set closed on a staged delete")
	}
	if len(state.Changes) != 0 {
		t.Errorf("changes after add-then-remove = %+v, want none", state.Changes)
	}
}

// TestStagedFieldUniquenessIsTheSetsOwn asserts that the dictionary rules run
// against the snapshot: a name another staged field took is refused even though
// no live row carries it yet.
func TestStagedFieldUniquenessIsTheSetsOwn(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Uniqueness")
	f := e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start staging: %d", code)
	}

	base := "/api/v1/projects/" + itoa(projectID)
	for round, want := range []int{http.StatusCreated, http.StatusConflict} {
		rec := e.do("POST", base+"/instruments/"+itoa(f.instrumentB)+"/fields",
			map[string]any{"field_name": "weight", "field_type": "text"}, admin)
		if rec.Code != want {
			t.Errorf("create #%d = %d, want %d (%s)", round+1, rec.Code, want, rec.Body.String())
		}
	}
	// A name the live design already used is refused as well.
	if rec := e.do("POST", base+"/instruments/"+itoa(f.instrumentB)+"/fields",
		map[string]any{"field_name": "hb", "field_type": "text"}, admin); rec.Code != http.StatusConflict {
		t.Errorf("reuse of a live field name = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// TestStagedProvisionalInstrumentAcceptsFields is the round trip that provisional
// ids exist for: an instrument created in the set has no live row, and its id has
// to address it for the field endpoints.
func TestStagedProvisionalInstrumentAcceptsFields(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Staged Provisional")
	e.seedDesign(projectID)
	e.mustProduction(projectID, admin)
	if code := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/staging", nil, admin).Code; code != http.StatusCreated {
		t.Fatalf("start staging: %d", code)
	}

	base := "/api/v1/projects/" + itoa(projectID)
	rec := e.do("POST", base+"/instruments", map[string]any{"name": "visits"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create staged instrument = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	var inst instrumentObject
	e.decode(rec, &inst)
	if inst.ID >= 0 {
		t.Fatalf("staged instrument id = %d, want a provisional negative id", inst.ID)
	}

	rec = e.do("POST", base+"/instruments/"+itoa(inst.ID)+"/fields",
		map[string]any{"field_name": "visit_note", "field_type": "text"}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("field on the staged instrument = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", base+"/instruments/"+itoa(inst.ID)+"/fields", nil, admin)
	var listed []fieldObject
	e.decode(rec, &listed)
	if len(listed) != 1 || listed[0].FieldName != "visit_note" {
		t.Errorf("staged instrument fields = %+v, want just visit_note", listed)
	}

	// Commit brings both rows across together.
	rec = e.do("POST", base+"/staging/commit", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var out commitResponse
	e.decode(rec, &out)
	if out.Applied.Instruments != 1 || out.Applied.Fields != 1 {
		t.Errorf("applied = %+v, want 1 instrument and 1 field", out.Applied)
	}
	if got := e.countRows("fields", projectID); got != 5 {
		t.Errorf("live fields after commit = %d, want 5", got)
	}
}

// TestAnalysisModeBreakingDeleteNeedsAcknowledgement covers REQ-API-111 with the
// acknowledgement a DELETE carries in its body, and REQ-AUD-025's rule that the
// structure event records it: the first call names the change and applies
// nothing, the retry goes through and says so on the trail.
func TestAnalysisModeBreakingDeleteNeedsAcknowledgement(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Analysis Ack")
	f := e.seedDesign(projectID)
	// An unreferenced field with a stored value: deleting it is breaking, and no
	// expression blocks the way (unlike labs.hb, which the fixture's calculated
	// field names).
	weight := e.mustField(projectID, f.instrumentB, "weight", "text", 2)
	e.mustValue(projectID, "REC001", "baseline_arm_1", "", "weight", "70")
	e.mustProduction(projectID, admin)
	if code := e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/mode",
		map[string]any{"mode": "analysis"}, admin).Code; code != http.StatusOK {
		t.Fatalf("enter analysis: %d", code)
	}

	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/" + itoa(f.instrumentB) + "/fields/" + itoa(weight)
	rec := e.do("DELETE", path, nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unacknowledged delete = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acknowledge_breaking") {
		t.Errorf("rejection does not name the acknowledgement: %s", rec.Body.String())
	}
	if got := e.countRows("fields", projectID); got != 5 {
		t.Errorf("live fields = %d after the refused delete, want 5", got)
	}
	if n := e.auditCount(audit.FieldDeleted, projectID); n != 0 {
		t.Errorf("field_deleted entries = %d after a refused change, want 0 (REQ-AUD-004)", n)
	}

	rec = e.do("DELETE", path, map[string]any{"acknowledge_breaking": true}, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("acknowledged delete = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if got := e.countRows("fields", projectID); got != 4 {
		t.Errorf("live fields = %d after the acknowledged delete, want 4", got)
	}
	details := e.auditDetails(audit.FieldDeleted, projectID)
	if len(details) != 1 {
		t.Fatalf("field_deleted entries = %d, want 1", len(details))
	}
	if acknowledged, _ := details[0]["breaking_acknowledged"].(bool); !acknowledged {
		t.Errorf("field_deleted details carry no acknowledgement: %v", details[0])
	}
}

// TestAnalysisModeNonBreakingEditNeedsNoAcknowledgement is the other half of
// REQ-API-111: a non-breaking setup edit in analysis mode applies straight away,
// and its event claims no acknowledgement it never needed.
func TestAnalysisModeNonBreakingEditNeedsNoAcknowledgement(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Analysis Plain")
	f := e.seedDesign(projectID)
	hb := e.mustFieldID(projectID, "hb")
	e.mustProduction(projectID, admin)
	if code := e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/mode",
		map[string]any{"mode": "analysis"}, admin).Code; code != http.StatusOK {
		t.Fatalf("enter analysis: %d", code)
	}

	rec := e.do("PUT", "/api/v1/projects/"+itoa(projectID)+"/instruments/"+itoa(f.instrumentB)+"/fields/"+itoa(hb),
		map[string]any{"field_label": "Haemoglobin"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-breaking analysis edit = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	details := e.auditDetails(audit.FieldUpdated, projectID)
	if len(details) != 1 {
		t.Fatalf("field_updated entries = %d, want 1", len(details))
	}
	if _, present := details[0]["breaking_acknowledged"]; present {
		t.Errorf("a non-breaking change recorded an acknowledgement: %v", details[0])
	}
}
