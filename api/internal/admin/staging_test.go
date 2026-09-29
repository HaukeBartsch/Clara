package admin

import (
	"context"
	"net/http"
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
