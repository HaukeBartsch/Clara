package admin

// Cross-project transfers on the administration API (§4.23, REQ-API-165/166):
// design-time validation of a transfer field's copy instructions (REQ-DB-050),
// execution with runtime target authorization and the audit trail on both
// projects (REQ-AUD-034), idempotent re-clicks, and the availability probe.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// xferConfig is the reference instruction text of these tests: it names the
// target project twice with its spaces, resolves one copy by short event name
// and one by unique_event_name, and carries a comment.
const xferConfig = "create_record [Target Study][pid] record_name # the new record's name\n" +
	"copy age  [Target Study][visit][age_in]\n" +
	"copy city [Target Study][visit_arm_1][city_in]"

type transferFixture struct {
	src, tgt     int64
	srcInstrID   int64 // intake on the source project
	tgtInstrID   int64 // enroll on the target project
	visitEventID int64 // the target's visit event row (pair grants address it)
	mem          *db.User
	admin        *db.User
}

func newTransferFixture(t *testing.T) (*env, transferFixture) {
	t.Helper()
	e := newEnv(t)
	f := transferFixture{admin: e.mustAdmin("xfer-admin@example.org")}

	f.src = e.mustProject("Source Study")
	arm := e.mustArm(f.src, 1)
	v1 := e.mustEvent(f.src, arm, "baseline", "baseline_arm_1")
	f.srcInstrID = e.mustInstrument(f.src, "intake")
	for i, name := range []string{"record_name", "age", "city"} {
		e.mustField(f.src, f.srcInstrID, name, "text", i+1)
	}
	hist := e.mustInstrument(f.src, "history") // faraway lives here — cross-instrument source
	e.mustField(f.src, hist, "faraway", "text", 1)
	e.mustMap(f.src, arm, db.InstrumentEvent{InstrumentID: f.srcInstrID, EventID: v1})

	f.tgt = e.mustProject("Target Study")
	tarm := e.mustArm(f.tgt, 1)
	visit := e.mustEvent(f.tgt, tarm, "visit", "visit_arm_1")
	f.visitEventID = visit
	f.tgtInstrID = e.mustInstrument(f.tgt, "enroll")
	for i, name := range []string{"pid", "age_in", "city_in"} { // pid: first field = identifier (GD-8)
		e.mustField(f.tgt, f.tgtInstrID, name, "text", i+1)
	}
	e.mustMap(f.tgt, tarm, db.InstrumentEvent{InstrumentID: f.tgtInstrID, EventID: visit})

	f.mem = e.mustUser("mem@example.org") // role-less member = full rights on each project
	e.mustMemberWithRole(f.src, f.mem, 0)
	e.mustMemberWithRole(f.tgt, f.mem, 0)

	e.mustRecord(f.src, "R1")
	e.mustValue(f.src, "R1", "baseline_arm_1", "", "record_name", "P-001")
	e.mustValue(f.src, "R1", "baseline_arm_1", "", "age", "42")
	e.mustValue(f.src, "R1", "baseline_arm_1", "", "city", "Oslo")
	return e, f
}

func (f transferFixture) fieldsPath(pid, instID int64) string {
	return "/api/v1/projects/" + itoa(pid) + "/instruments/" + itoa(instID) + "/fields"
}

func (f transferFixture) execPath(record string) string {
	return "/api/v1/projects/" + itoa(f.src) + "/records/" + record + "/transfers"
}

// mustCreateTransferField saves a transfer field through the API and returns
// the created object.
func (e *env) mustCreateTransferField(t *testing.T, f transferFixture, name, config string) fieldObject {
	t.Helper()
	rec := e.do("POST", f.fieldsPath(f.src, f.srcInstrID), map[string]any{
		"field_name": name, "field_type": "transfer", "choices": config,
	}, f.admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transfer field: %d %s", rec.Code, rec.Body.String())
	}
	var fo fieldObject
	e.decode(rec, &fo)
	return fo
}

// seedTransferField inserts a transfer field directly (bypassing design-time
// validation) so a test can execute or probe against a config the API would
// never have accepted.
func (e *env) seedTransferField(t *testing.T, f transferFixture, name, config string) {
	t.Helper()
	id := e.mustField(f.src, f.srcInstrID, name, "transfer", 90)
	if _, err := e.Store.DB.Exec(`UPDATE fields SET choices = ? WHERE id = ?`, config, id); err != nil {
		t.Fatalf("seed transfer field: %v", err)
	}
}

func (e *env) countSQL(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (e *env) valueSQL(t *testing.T, projectID int64, record, event, field string) (string, bool) {
	t.Helper()
	var v string
	err := e.Store.DB.QueryRow(
		`SELECT value FROM data WHERE project_id = ? AND record_id = ? AND unique_event_name = ? AND field_name = ?`,
		projectID, record, event, field).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// --- design-time validation (REQ-DB-050) ---

func TestTransferDesignValidation(t *testing.T) {
	e, f := newTransferFixture(t)

	// The instruction text is stored verbatim — the choices grammar is not run
	// over a transfer field (REQ-DB-050).
	created := e.mustCreateTransferField(t, f, "xfer", xferConfig)
	if created.Choices != xferConfig {
		t.Fatalf("choices = %q, want the config verbatim", created.Choices)
	}

	bad := func(name, config, want string) {
		t.Helper()
		rec := e.do("POST", f.fieldsPath(f.src, f.srcInstrID), map[string]any{
			"field_name": name, "field_type": "transfer", "choices": config,
		}, f.admin)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d %s, want 400", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: body %s does not mention %q", name, rec.Body.String(), want)
		}
	}

	bad("empty", "", "copy instructions")
	bad("no_create", "copy age [Target Study][visit][age_in]", "create")
	bad("two_creates", "create_record [Target Study][pid] record_name\ncreate_record [Target Study][pid] age", "only one create")
	// The design-time answer names an unknown project on purpose — the runtime
	// one never does (REQ-DB-050 vs REQ-API-165).
	bad("ghost_project", "create_record [Ghost Study][pid] record_name", "no project named 'Ghost Study'")
	bad("wrong_identifier", "create_record [Target Study][age_in] record_name", "record identifier field 'pid'")
	bad("cross_instrument_source", "create_record [Target Study][pid] faraway", "not a value-carrying field of this instrument")
	bad("create_arity", "create_record [Target Study][visit][pid] record_name", "not valid")

	// A create_project line names the new project by a field of this instrument,
	// and that mistake is answered in those words rather than in type vocabulary.
	// A template is not among the mistakes: one argument alone is a whole
	// instruction, tested in TestTransferCreateProjectEmpty.
	bad("project_name_must_be_a_field", "create_project [New Study] [Target Study]",
		"names the new project by the value of a field of this instrument")
	bad("project_wrong_arity", "create_project record_name template token id extra",
		"takes 1 to 4 arguments, not 5")

	// A checkbox stores nothing under its own name (REQ-DB-042).
	rec := e.do("POST", f.fieldsPath(f.src, f.srcInstrID), map[string]any{
		"field_name": "mood", "field_type": "checkbox", "choices": "1$ok##2$low",
	}, f.admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed checkbox: %d %s", rec.Code, rec.Body.String())
	}
	bad("checkbox_source", "create_record [Target Study][pid] mood\ncopy mood [Target Study][visit][age_in]", "choice columns")

	// A non-value target field cannot receive a transfer.
	e.mustField(f.tgt, f.tgtInstrID, "note_t", "description", 9)
	bad("nonvalue_target", "create_record [Target Study][pid] record_name\ncopy age [Target Study][visit][note_t]", "cannot receive")

	// A transfer field is not a value: it cannot be another transfer's source.
	e.mustCreateTransferField(t, f, "xfer2", xferConfig)
	bad("transfer_as_source", "create_record [Target Study][pid] record_name\ncopy xfer2 [Target Study][visit][age_in]", "value-carrying")
}

// --- execution (REQ-API-165) and idempotency ---

func TestTransferExecuteAndReClick(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "xfer", xferConfig)

	rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		TargetProject string   `json:"target_project"`
		TargetRecord  string   `json:"target_record"`
		Created       bool     `json:"created"`
		Copied        []string `json:"copied"`
		SkippedEmpty  []string `json:"skipped_empty"`
	}
	e.decode(rec, &res)
	if res.TargetProject != "Target Study" || res.TargetRecord != "P-001" || !res.Created {
		t.Fatalf("result = %+v, want Target Study/P-001 created", res)
	}
	if strings.Join(res.Copied, ",") != "age,city" || len(res.SkippedEmpty) != 0 {
		t.Fatalf("copied/skipped = %v / %v, want age,city / none", res.Copied, res.SkippedEmpty)
	}

	// The identifier row rides every event the copies name; the values land at
	// the target's first-instance slot.
	for field, want := range map[string]string{"pid": "P-001", "age_in": "42", "city_in": "Oslo"} {
		if got, ok := e.valueSQL(t, f.tgt, "P-001", "visit_arm_1", field); !ok || got != want {
			t.Errorf("target %s = %q (%v), want %q", field, got, ok, want)
		}
	}

	// Audit: one execution entry on the source (never values), and the target's
	// birth entry carries the transfer's origin (REQ-AUD-034).
	srcAudit := e.auditDetails("transfer_executed", f.src)
	if len(srcAudit) != 1 {
		t.Fatalf("source transfer_executed rows = %d, want 1", len(srcAudit))
	}
	d := srcAudit[0]
	if d["field"] != "xfer" || d["target_project"] != "Target Study" ||
		d["target_record"] != "P-001" || d["created"] != true {
		t.Errorf("transfer_executed details = %v", d)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "Oslo") || strings.Contains(string(raw), `"42"`) {
		t.Errorf("transfer_executed leaked values: %s", raw)
	}
	tgtCreated := e.auditDetails("record_created", f.tgt)
	if len(tgtCreated) != 1 {
		t.Fatalf("target record_created rows = %d, want 1", len(tgtCreated))
	}
	origin, _ := tgtCreated[0]["transfer"].(map[string]any)
	if origin == nil || origin["source_project"] != "Source Study" || origin["source_record"] != "R1" {
		t.Errorf("target record_created details = %v, want a transfer origin naming the source", tgtCreated[0])
	}

	// Re-click: the same name resolves the existing record — updated, not
	// duplicated; nothing changed, so no new data-change entry is written,
	// while the execution itself stays on the source trail (REQ-API-165).
	rec = e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-click: %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &res)
	if res.Created {
		t.Errorf("re-click created = true, want false")
	}
	if n := e.countSQL(t, `SELECT count(*) FROM audit_events WHERE project_id = ? AND event_type IN ('record_created','record_updated')`, f.tgt); n != 1 {
		t.Errorf("target data-change entries after re-click = %d, want 1", n)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM audit_events WHERE project_id = ? AND event_type = 'transfer_executed'`, f.src); n != 2 {
		t.Errorf("transfer_executed rows after re-click = %d, want 2", n)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM data WHERE project_id = ? AND record_id = 'P-001'`, f.tgt); n != 3 {
		t.Errorf("target data rows after re-click = %d, want 3", n)
	}
}

// --- authorization (REQ-API-165, REQ-AUTH-045): denials are uniform and write
// nothing at all, audit included ---

func TestTransferAuthorization(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "xfer", xferConfig)

	denied := func(name string, u *db.User) {
		t.Helper()
		rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, u)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: got %d %s, want 403", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "Target Study") || strings.Contains(rec.Body.String(), "P-001") {
			t.Errorf("%s: 403 body discloses the target: %s", name, rec.Body.String())
		}
	}

	denied("outsider", e.mustUser("outsider@example.org")) // not even a source member

	srcOnly := e.mustUser("srconly@example.org")
	e.mustMemberWithRole(f.src, srcOnly, 0) // no membership on the target at all
	denied("target_stranger", srcOnly)

	readerRole, err := e.Store.CreateRole(context.Background(),
		&db.Role{ProjectID: f.tgt, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("tgt-reader@example.org")
	e.mustMemberWithRole(f.src, reader, 0)
	e.mustMemberWithRole(f.tgt, reader, readerRole)
	denied("target_read_only", reader)

	// Pair level: the arm default is edit, but the written pair carries a
	// read_only grant — all pairs or none (REQ-AUTH-070).
	pairRole, err := e.Store.CreateRole(context.Background(),
		&db.Role{ProjectID: f.tgt, RoleName: "pair-deny"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_all"}},
		[]db.RoleGrant{{EventID: f.visitEventID, InstrumentID: f.tgtInstrID, DataAccessLevel: "read_only"}})
	if err != nil {
		t.Fatalf("CreateRole pair-deny: %v", err)
	}
	pairDenied := e.mustUser("pair-denied@example.org")
	e.mustMemberWithRole(f.src, pairDenied, 0)
	e.mustMemberWithRole(f.tgt, pairDenied, pairRole)
	denied("pair_level", pairDenied)

	if n := e.countSQL(t, `SELECT count(*) FROM data WHERE project_id = ?`, f.tgt); n != 0 {
		t.Errorf("target rows after denials = %d, want 0", n)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM audit_events WHERE event_type IN ('transfer_executed','record_created','record_updated')`); n != 0 {
		t.Errorf("audit rows after denials = %d, want 0", n)
	}

	// A missing or unknown field/instrument is a configuration error, not a
	// silent success.
	rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=nope", nil, f.mem)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no transfer field") {
		t.Fatalf("unknown field: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTransferCreateProjectFromTemplate(t *testing.T) {
	e, f := newTransferFixture(t)
	for i, field := range []string{"project_token", "project_id"} {
		e.mustField(f.src, f.srcInstrID, field, "text", 10+i)
	}
	config := "create_project record_name [Target Study] project_token project_id"
	e.mustCreateTransferField(t, f, "create_project", config)
	if _, err := e.Store.DB.Exec(
		`UPDATE data SET value = ? WHERE project_id = ? AND record_id = ? AND unique_event_name = ? AND field_name = ?`,
		"New Study", f.src, "R1", "baseline_arm_1", "record_name"); err != nil {
		t.Fatalf("set project name: %v", err)
	}

	path := f.execPath("R1") + "?event=baseline_arm_1&instrument=intake&field=create_project"
	rec := e.do("POST", path, nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		TargetProject string   `json:"target_project"`
		TargetRecord  string   `json:"target_record"`
		Created       bool     `json:"created"`
		Copied        []string `json:"copied"`
		SkippedEmpty  []string `json:"skipped_empty"`
	}
	e.decode(rec, &result)
	if result.TargetProject != "New Study" || !result.Created || result.TargetRecord != "" ||
		result.Copied == nil || result.SkippedEmpty == nil {
		t.Fatalf("result = %+v, want created New Study", result)
	}

	project, err := e.Store.GetProjectByName(context.Background(), "New Study")
	if err != nil || project == nil {
		t.Fatalf("get new project: project=%v err=%v", project, err)
	}
	instruments, err := e.Store.ListInstruments(context.Background(), project.ID)
	if err != nil || len(instruments) != 1 || instruments[0].Name != "enroll" {
		t.Fatalf("cloned instruments = %+v, err=%v; want enroll", instruments, err)
	}
	fields, err := e.Store.ListFields(context.Background(), project.ID)
	if err != nil || len(fields) != 3 {
		t.Fatalf("cloned fields = %d, err=%v; want 3", len(fields), err)
	}
	events, err := e.Store.ListEventsByArm(context.Background(), project.ID, func() int64 {
		arms, armErr := e.Store.ListArms(context.Background(), project.ID)
		if armErr != nil || len(arms) != 1 {
			t.Fatalf("cloned arms = %+v, err=%v", arms, armErr)
		}
		return arms[0].ID
	}())
	if err != nil || len(events) != 1 || events[0].UniqueEventName != "visit_arm_1" {
		t.Fatalf("cloned events = %+v, err=%v; want visit_arm_1", events, err)
	}
	pairs, err := e.Store.ListInstrumentEvents(context.Background(), project.ID)
	if err != nil || len(pairs) != 1 || pairs[0].InstrumentID != instruments[0].ID ||
		pairs[0].EventID != events[0].ID {
		t.Fatalf("cloned mappings = %+v, err=%v; want enroll mapped at visit_arm_1", pairs, err)
	}

	assignment, err := e.Store.GetAssignment(context.Background(), f.mem.ID, project.ID)
	if err != nil || assignment == nil || assignment.RoleID.Valid || assignment.Token == "" {
		t.Fatalf("project assignment = %+v, err=%v; want role-less membership with token", assignment, err)
	}
	if got, _ := e.valueSQL(t, f.src, "R1", "baseline_arm_1", "project_token"); got != assignment.Token {
		t.Errorf("copied project token = %q, want membership token", got)
	}
	if got, _ := e.valueSQL(t, f.src, "R1", "baseline_arm_1", "project_id"); got != itoa(project.ID) {
		t.Errorf("copied project id = %q, want %d", got, project.ID)
	}

	// A second click is an exact no-op for an already-existing project.
	if _, err := e.Store.DB.Exec(`DELETE FROM data WHERE project_id = ? AND record_id = ?
		AND unique_event_name = ? AND field_name IN ('project_token', 'project_id')`,
		f.src, "R1", "baseline_arm_1"); err != nil {
		t.Fatalf("clear writeback fields: %v", err)
	}
	rec = e.do("POST", path, nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat create project: %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &result)
	if result.Created || result.TargetProject != "New Study" {
		t.Fatalf("repeat result = %+v, want existing project without creation", result)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM user_projects WHERE project_id = ?`, project.ID); n != 1 {
		t.Errorf("memberships = %d, want one after repeated click", n)
	}
	if _, exists := e.valueSQL(t, f.src, "R1", "baseline_arm_1", "project_token"); exists {
		t.Error("existing-project no-op wrote a project token")
	}
	if n := e.countSQL(t, `SELECT count(*) FROM audit_events WHERE project_id = ? AND event_type = 'transfer_executed'`, f.src); n != 2 {
		t.Errorf("transfer audit entries = %d, want both creation and no-op attempts", n)
	}
	var auditDetails string
	if err := e.Store.DB.QueryRow(`SELECT details FROM audit_events
		WHERE project_id = ? AND event_type = 'project_created'`, project.ID).Scan(&auditDetails); err != nil {
		t.Fatalf("read project audit: %v", err)
	}
	if strings.Contains(auditDetails, assignment.Token) {
		t.Error("project token leaked into audit details")
	}
}

// The project name a create_project line uses is a reference to a field of the
// current instrument, and `[fieldname]` is that reference — the same argument a
// bare `fieldname` is, carrying the field's stored value into the new project's
// name (REQ-VAL-047). The bracketed spelling is what VISION writes, so it is
// pinned here end to end: design-time acceptance and one executed click.
func TestTransferCreateProjectBracketedName(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "create_project", "create_project [record_name] [Target Study]")

	path := f.execPath("R1") + "?event=baseline_arm_1&instrument=intake&field=create_project"
	rec := e.do("POST", path, nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		TargetProject string `json:"target_project"`
		Created       bool   `json:"created"`
	}
	e.decode(rec, &result)
	if !result.Created || result.TargetProject != "P-001" { // R1's stored record_name value
		t.Fatalf("result = %+v, want created project P-001", result)
	}
	project, err := e.Store.GetProjectByName(context.Background(), "P-001")
	if err != nil || project == nil {
		t.Fatalf("new project = %v, err = %v; want one named from the field value", project, err)
	}
	instruments, err := e.Store.ListInstruments(context.Background(), project.ID)
	if err != nil || len(instruments) != 1 || instruments[0].Name != "enroll" {
		t.Fatalf("cloned instruments = %+v, err=%v; want enroll", instruments, err)
	}
}

// One argument is a whole create_project instruction: with no template the button
// raises an **empty** project — no arms, events, instruments or fields (REQ-VAL-047).
// Nothing on the projects row records who created it, so the acting user has to be
// findable two ways: the role-less membership that makes them its only project
// admin, and the project_created audit entry written against the new project
// without a template name (REQ-API-168).
func TestTransferCreateProjectEmpty(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "create_project", "create_project [record_name]")

	rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=create_project", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("create empty project: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		TargetProject string `json:"target_project"`
		Created       bool   `json:"created"`
	}
	e.decode(rec, &result)
	if !result.Created || result.TargetProject != "P-001" { // R1's stored record_name value
		t.Fatalf("result = %+v, want created project P-001", result)
	}
	project, err := e.Store.GetProjectByName(context.Background(), "P-001")
	if err != nil || project == nil {
		t.Fatalf("new project = %v, err = %v", project, err)
	}
	for _, structure := range []struct{ what, table string }{
		{"arms", "arms"}, {"events", "events"}, {"instruments", "instruments"}, {"fields", "fields"},
	} {
		if n := e.countSQL(t, `SELECT count(*) FROM `+structure.table+` WHERE project_id = ?`, project.ID); n != 0 {
			t.Errorf("%s of the empty project = %d, want 0", structure.what, n)
		}
	}
	// instrument_events hangs off the instrument and event rows, not the project.
	if n := e.countSQL(t, `SELECT count(*) FROM instrument_events ie
		JOIN instruments i ON i.id = ie.instrument_id WHERE i.project_id = ?`, project.ID); n != 0 {
		t.Errorf("instrument/event mappings of the empty project = %d, want 0", n)
	}

	assignment, err := e.Store.GetAssignment(context.Background(), f.mem.ID, project.ID)
	if err != nil || assignment == nil || assignment.RoleID.Valid || assignment.Token == "" {
		t.Fatalf("project assignment = %+v, err = %v; want role-less membership with token", assignment, err)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM user_projects WHERE project_id = ?`, project.ID); n != 1 {
		t.Errorf("members = %d, want the acting user alone", n)
	}

	var auditUser int64
	var auditDetails string
	if err := e.Store.DB.QueryRow(`SELECT user_id, details FROM audit_events
		WHERE project_id = ? AND event_type = 'project_created'`, project.ID).Scan(&auditUser, &auditDetails); err != nil {
		t.Fatalf("read project_created audit: %v", err)
	}
	if auditUser != f.mem.ID {
		t.Errorf("project_created written by user %d, want the acting user %d", auditUser, f.mem.ID)
	}
	if !strings.Contains(auditDetails, "P-001") || strings.Contains(auditDetails, "template_project") {
		t.Errorf("project_created details = %s, want the project name and no template", auditDetails)
	}

	// A second click on an empty project that already exists stays a no-op.
	rec = e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=create_project", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat create: %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &result)
	if result.Created {
		t.Error("repeat click created a second project")
	}
	if n := e.countSQL(t, `SELECT count(*) FROM user_projects WHERE project_id = ?`, project.ID); n != 1 {
		t.Errorf("members after the repeat = %d, want one", n)
	}
}

// --- the availability probe (REQ-API-166): enabled per actor, fail-closed ---

func TestTransferAvailabilityProbe(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "xfer", xferConfig)
	e.seedTransferField(t, f, "xfer_ghost", "create_record [Ghost Study][pid] record_name")

	probe := func(u *db.User) map[string]bool {
		t.Helper()
		rec := e.do("GET", f.execPath("R1")+"?instrument=intake", nil, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("probe: %d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Transfers []struct {
				FieldName string `json:"field_name"`
				Enabled   bool   `json:"enabled"`
			} `json:"transfers"`
		}
		e.decode(rec, &body)
		out := map[string]bool{}
		for _, s := range body.Transfers {
			out[s.FieldName] = s.Enabled
		}
		return out
	}

	got := probe(f.mem)
	if !got["xfer"] || got["xfer_ghost"] {
		t.Fatalf("probe as member = %v, want xfer enabled, xfer_ghost disabled", got)
	}

	readerRole, err := e.Store.CreateRole(context.Background(),
		&db.Role{ProjectID: f.tgt, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("probe-reader@example.org")
	e.mustMemberWithRole(f.src, reader, 0)
	e.mustMemberWithRole(f.tgt, reader, readerRole)
	if got := probe(reader); got["xfer"] {
		t.Errorf("probe as target read_only = %v, want xfer disabled (no reason disclosed)", got)
	}

	// A member of neither project never gets the map at all — the uniform 403.
	rec := e.do("GET", f.execPath("R1")+"?instrument=intake", nil, e.mustUser("probe-ghost@example.org"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("probe outsider: %d %s", rec.Code, rec.Body.String())
	}

	// The instrument parameter is required; fields of other instruments are
	// not listed.
	rec = e.do("GET", f.execPath("R1"), nil, f.mem)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("probe without instrument: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", f.execPath("R1")+"?instrument=history", nil, f.mem)
	var body struct {
		Transfers []json.RawMessage `json:"transfers"`
	}
	e.decode(rec, &body)
	if rec.Code != http.StatusOK || len(body.Transfers) != 0 {
		t.Fatalf("probe on history = %d %s, want empty list", rec.Code, rec.Body.String())
	}
}

// --- source and target edge semantics (REQ-API-165) ---

func TestTransferEmptySourceSkipsAndStaleTarget(t *testing.T) {
	e, f := newTransferFixture(t)
	e.mustCreateTransferField(t, f, "xfer", xferConfig)

	// R2 has no city; the target record already holds one. An empty source
	// value skips the operation — it does not clear the target (REQ-API-165,
	// a deliberate deviation from import's empty-clears).
	e.mustRecord(f.src, "R2")
	e.mustValue(f.src, "R2", "baseline_arm_1", "", "record_name", "P-002")
	e.mustValue(f.src, "R2", "baseline_arm_1", "", "age", "33")
	e.mustRecord(f.tgt, "P-002")
	e.mustValue(f.tgt, "P-002", "visit_arm_1", "", "city_in", "Bergen")

	rec := e.do("POST", f.execPath("R2")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute R2: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Created      bool     `json:"created"`
		Copied       []string `json:"copied"`
		SkippedEmpty []string `json:"skipped_empty"`
	}
	e.decode(rec, &res)
	if res.Created {
		t.Errorf("created = true, want false (P-002 exists)")
	}
	if strings.Join(res.Copied, ",") != "age" || strings.Join(res.SkippedEmpty, ",") != "city" {
		t.Fatalf("copied/skipped = %v / %v, want age / city", res.Copied, res.SkippedEmpty)
	}
	if got, _ := e.valueSQL(t, f.tgt, "P-002", "visit_arm_1", "city_in"); got != "Bergen" {
		t.Errorf("city_in = %q, want Bergen untouched by the skipped copy", got)
	}

	// An empty create source is a configuration-shaped failure: no name, no
	// record (400 with the field named).
	e.mustRecord(f.src, "R3")
	rec = e.do("POST", f.execPath("R3")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "record_name") {
		t.Fatalf("empty record name: %d %s", rec.Code, rec.Body.String())
	}

	// Designs rot: the target repurposes age_in after the field was saved —
	// a design change through the API, so the dictionary cache moves with it.
	// Execution re-resolves and fails with a clean per-field detail, writing
	// nothing (REQ-API-165).
	var ageFieldID int64
	if err := e.Store.DB.QueryRow(
		`SELECT id FROM fields WHERE project_id = ? AND field_name = 'age_in'`, f.tgt).Scan(&ageFieldID); err != nil {
		t.Fatalf("find target field: %v", err)
	}
	rec = e.do("PUT", "/api/v1/projects/"+itoa(f.tgt)+"/instruments/"+itoa(f.tgtInstrID)+"/fields/"+itoa(ageFieldID),
		map[string]any{"field_type": "description"}, f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("repurpose target field: %d %s", rec.Code, rec.Body.String())
	}
	// The httpapi middleware bumps the structure generation after every
	// successful design write (httpapi.go structureInvalidation); these tests
	// call the mux directly, so emulate it.
	e.Store.BumpStructureCache()
	rec = e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "age_in") {
		t.Fatalf("stale target: %d %s", rec.Code, rec.Body.String())
	}
	if n := e.countSQL(t, `SELECT count(*) FROM data WHERE project_id = ?`, f.tgt); n != 3 { // P-002: pid, age_in, untouched city_in
		t.Errorf("target rows after stale failure = %d, want only P-002's three", n)
	}
}

func TestTransferEventlessTarget(t *testing.T) {
	e, f := newTransferFixture(t)
	plain := e.mustProject("Plain Study") // never gets an event: values live at ""
	form := e.mustInstrument(plain, "form")
	e.mustField(plain, form, "pid", "text", 1)
	e.mustField(plain, form, "note_in", "text", 2)

	const plainConfig = "create_record [Plain Study][pid] record_name\ncopy age [Plain Study][note_in]"
	if rec := e.do("POST", f.fieldsPath(f.src, f.srcInstrID), map[string]any{
		"field_name": "xfer_plain", "field_type": "transfer", "choices": plainConfig,
	}, f.admin); rec.Code != http.StatusCreated {
		t.Fatalf("event-less target config: %d %s", rec.Code, rec.Body.String())
	}
	// An event token against an event-less target is rejected at design time.
	if rec := e.do("POST", f.fieldsPath(f.src, f.srcInstrID), map[string]any{
		"field_name": "xfer_plain_bad", "field_type": "transfer",
		"choices": "create_record [Plain Study][pid] record_name\ncopy age [Plain Study][visit_arm_1][note_in]",
	}, f.admin); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "has no events") {
		t.Fatalf("event token on event-less target: %d %s", rec.Code, rec.Body.String())
	}

	e.mustMemberWithRole(plain, f.mem, 0)
	rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer_plain", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute to event-less: %d %s", rec.Code, rec.Body.String())
	}
	if got, ok := e.valueSQL(t, plain, "P-001", "", "note_in"); !ok || got != "42" {
		t.Errorf("plain note_in = %q (%v), want 42 at the empty event", got, ok)
	}
}

func TestTransferTypeChanges(t *testing.T) {
	e, f := newTransferFixture(t)
	fo := e.mustCreateTransferField(t, f, "xfer", xferConfig)

	// Leaving transfer is an ordinary type change; the designer sends empty
	// choices for a type without a choice editor, and the instructions go.
	rec := e.do("PUT", f.fieldsPath(f.src, f.srcInstrID)+"/"+itoa(fo.ID),
		map[string]any{"field_type": "text", "choices": ""}, f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer → text: %d %s", rec.Code, rec.Body.String())
	}
	var got fieldObject
	e.decode(rec, &got)
	if got.FieldType != "text" || got.Choices != "" {
		t.Fatalf("after change = %+v, want text without choices", got)
	}

	// Back to transfer needs instructions again — the grammar gate runs on the
	// merged field, and an empty choices is refused (REQ-DB-050).
	if rec := e.do("PUT", f.fieldsPath(f.src, f.srcInstrID)+"/"+itoa(fo.ID),
		map[string]any{"field_type": "transfer"}, f.admin); rec.Code == http.StatusOK {
		t.Fatalf("transfer without instructions accepted: %s", rec.Body.String())
	}
	rec = e.do("PUT", f.fieldsPath(f.src, f.srcInstrID)+"/"+itoa(fo.ID),
		map[string]any{"field_type": "transfer", "choices": xferConfig}, f.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("back to transfer: %d %s", rec.Code, rec.Body.String())
	}
	e.decode(rec, &got)
	if got.Choices != xferConfig {
		t.Errorf("choices after return = %q, want the config verbatim", got.Choices)
	}
}

// --- data access groups (REQ-API-093 pattern on the target) ---

func TestTransferDAGGroups(t *testing.T) {
	e, f := newTransferFixture(t)
	ctx := context.Background()
	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.tgt, Name: "Center A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup A: %v", err)
	}
	groupB, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.tgt, Name: "Center B"})
	if err != nil {
		t.Fatalf("CreateDAGGroup B: %v", err)
	}
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	assignment, err := e.Store.GetAssignment(ctx, f.mem.ID, f.tgt)
	if err != nil || assignment == nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	if err := e.Store.SetDAGMembershipsTx(ctx, tx, assignment.ID, []int64{groupA}, groupA); err != nil {
		t.Fatalf("SetDAGMembershipsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit memberships: %v", err)
	}

	e.mustCreateTransferField(t, f, "xfer", xferConfig)
	rec := e.do("POST", f.execPath("R1")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute with group: %d %s", rec.Code, rec.Body.String())
	}
	var gid int64
	if err := e.Store.DB.QueryRow(
		`SELECT dag_group_id FROM record_entities WHERE project_id = ? AND record_id = 'P-001'`, f.tgt).Scan(&gid); err != nil {
		t.Fatalf("group of new record: %v", err)
	}
	if gid != groupA { // the new record joins the actor's active group (REQ-API-093)
		t.Errorf("new record group = %d, want A (%d)", gid, groupA)
	}

	// A target record of another group is invisible — the uniform 403 that can
	// not enumerate records.
	e.mustRecord(f.src, "R9")
	e.mustValue(f.src, "R9", "baseline_arm_1", "", "record_name", "P-009")
	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: f.tgt, RecordID: "P-009", DagGroupID: nullInt64(groupB),
	}); err != nil {
		t.Fatalf("seed foreign-group record: %v", err)
	}
	rec = e.do("POST", f.execPath("R9")+"?event=baseline_arm_1&instrument=intake&field=xfer", nil, f.mem)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign-group target: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if n := e.countSQL(t, `SELECT count(*) FROM data WHERE project_id = ? AND record_id = 'P-009'`, f.tgt); n != 0 {
		t.Errorf("foreign-group record was written: %d rows", n)
	}
	if n := e.countSQL(t, `SELECT count(*) FROM audit_events WHERE event_type = 'transfer_executed'`); n != 1 {
		t.Errorf("transfer_executed rows = %d, want only the successful one", n)
	}
}
