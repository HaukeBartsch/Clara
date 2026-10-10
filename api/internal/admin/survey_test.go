package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- §4.17 survey links (REQ-API-082/085, REQ-API-145/146) ---

// surveyLinkPath builds the §4.17 path. The event is part of a link's identity,
// so it travels on every call: one instrument mapped to several events has one
// distinct link each (REQ-AUTH-039, REQ-API-082).
func surveyLinkPath(projectID int64, record string, instrumentID int64, eventName string) string {
	return "/api/v1/projects/" + itoa(projectID) + "/records/" + record +
		"/instruments/" + itoa(instrumentID) + "/survey-link?event=" + eventName
}

// surveyEventID resolves the fixture's event, which a stored link is keyed by.
func surveyEventID(t *testing.T, e *env, projectID int64) int64 {
	t.Helper()
	ev, err := e.Store.GetEventByUniqueName(context.Background(), projectID, "v1_arm_1")
	if err != nil || ev == nil {
		t.Fatalf("GetEventByUniqueName(v1_arm_1): %v", err)
	}
	return ev.ID
}

// surveyFixture is a one-arm project with a survey instrument and a plain one,
// both mapped to v1_arm_1, plus record R1. The survey sits first in the order and
// carries the GD-8 record identifier, so its second field is the first answer —
// the pair the REQ-API-146 value check distinguishes (a stored identifier must not
// refuse an issue).
type surveyFixture struct {
	projectID int64
	survey    int64
	plain     int64
}

func newSurveyFixture(t *testing.T, e *env) surveyFixture {
	t.Helper()
	ctx := context.Background()
	f := surveyFixture{}
	f.projectID = e.mustProject("Survey Study")
	armID := e.mustArm(f.projectID, 1)
	v1 := e.mustEvent(f.projectID, armID, "Visit 1", "v1_arm_1")
	surveyID, err := e.Store.AddInstrument(ctx, &db.Instrument{
		ProjectID: f.projectID, Name: "feedback", Position: 1, IsSurvey: true,
	})
	if err != nil {
		t.Fatalf("AddInstrument(survey): %v", err)
	}
	f.survey = surveyID
	e.mustField(f.projectID, surveyID, "participant", "text", 1)
	e.mustField(f.projectID, surveyID, "satisfaction", "text", 2)
	// The plain instrument sorts after the survey, so the identifier above really
	// is position 1 of position 1.
	if f.plain, err = e.Store.AddInstrument(ctx, &db.Instrument{
		ProjectID: f.projectID, Name: "intake", Position: 2,
	}); err != nil {
		t.Fatalf("AddInstrument(intake): %v", err)
	}
	e.mustMap(f.projectID, armID,
		db.InstrumentEvent{InstrumentID: surveyID, EventID: v1},
		db.InstrumentEvent{InstrumentID: f.plain, EventID: v1},
	)
	e.mustRecord(f.projectID, "R1")
	return f
}

// surveyStampCollected plays the data API's own stamp (REQ-DB-041): the save that
// stores a submitted response marks the link collected, which is also what spends
// it (REQ-API-145). Returns the stamp, to compare against the report.
func surveyStampCollected(t *testing.T, e *env, projectID int64, record string, instrumentID, eventID int64) string {
	t.Helper()
	ctx := context.Background()
	link, err := e.Store.GetSurveyLink(ctx, projectID, record, instrumentID, eventID)
	if err != nil || link == nil {
		t.Fatalf("GetSurveyLink: %v", err)
	}
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	stamped, err := e.Store.MarkSurveyLinkCollectedTx(ctx, tx, link.ID)
	if err != nil || !stamped {
		t.Fatalf("MarkSurveyLinkCollectedTx: stamped=%v err=%v", stamped, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	stored, err := e.Store.GetSurveyLink(ctx, projectID, record, instrumentID, eventID)
	if err != nil || stored == nil {
		t.Fatalf("GetSurveyLink after stamp: %v", err)
	}
	return stored.CollectedAt.String
}

// surveyDeleteValues removes the answers of the fixture's survey at v1_arm_1 — what
// the member does through the delete action of §3.8 before re-issuing (REQ-API-036).
func surveyDeleteValues(t *testing.T, e *env, projectID int64, record, field string) {
	t.Helper()
	err := e.Store.DeleteDataValueByID(context.Background(), &db.DataValue{
		ProjectID: projectID, RecordID: record, UniqueEventName: "v1_arm_1", FieldName: field,
	})
	if err != nil {
		t.Fatalf("DeleteDataValueByID(%s): %v", field, err)
	}
}

func surveyAudit(t *testing.T, e *env) []string {
	t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT event_type FROM audit_events WHERE event_type IN (?, ?) ORDER BY id`,
		audit.SurveyLinkIssued, audit.SurveyLinkRevoked)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// TestSurveyLinkLifecycle walks §4.17 as revised: GET reports without minting, POST
// issues, the URL is stable while live, revoke takes effect immediately, and a fresh
// issue after a revocation mints a different token. Audit fires on issuance and
// revocation only — reporting writes nothing (REQ-API-043).
func TestSurveyLinkLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	path := surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1")
	eventID := surveyEventID(t, e, f.projectID)

	// Nothing issued yet: reported as such, with no URL and no audit entry. A GET
	// that minted would destroy a live link by rendering the record view.
	rec := e.do("GET", path, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("report: got %d %s", rec.Code, rec.Body.String())
	}
	var none surveyLinkObject
	e.decode(rec, &none)
	if none.State != surveyStateNone || none.URL != "" {
		t.Fatalf("report = %+v, want state none with no URL", none)
	}
	if got := surveyAudit(t, e); len(got) != 0 {
		t.Fatalf("reporting wrote audit: %v", got)
	}

	// Issue: 200 live, the URL carries a link token, and the stored row is the one
	// reported (REQ-API-082/146).
	rec = e.do("POST", path, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue: got %d %s", rec.Code, rec.Body.String())
	}
	var first surveyLinkObject
	e.decode(rec, &first)
	if first.State != surveyStateLive || !strings.HasPrefix(first.URL, "https://csms.example.org/s/") {
		t.Fatalf("issued = %+v, want state live with the public URL", first)
	}
	token := strings.TrimPrefix(first.URL, "https://csms.example.org/s/")
	if len(token) < 30 {
		t.Errorf("token %q is not a link token", token)
	}
	stored, err := e.Store.GetSurveyLink(context.Background(), f.projectID, "R1", f.survey, eventID)
	if err != nil || stored == nil {
		t.Fatalf("GetSurveyLink: %v", err)
	}
	if stored.Token != token || stored.Revoked {
		t.Errorf("stored link = %+v, want the returned unrevoked token", stored)
	}

	// Reporting again is free: same URL, still one issuance entry.
	rec = e.do("GET", path, nil, admin)
	var again surveyLinkObject
	e.decode(rec, &again)
	if again.URL != first.URL || again.State != surveyStateLive {
		t.Errorf("second GET reported %+v, want the stable %q live", again, first.URL)
	}
	if got := surveyAudit(t, e); len(got) != 1 || got[0] != audit.SurveyLinkIssued {
		t.Fatalf("audit = %v, want one survey_link_issued", got)
	}

	// Revoke: 204, the row flips, and the report says revoked with no URL to copy.
	if rec := e.do("DELETE", path, nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: got %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = e.Store.GetSurveyLink(context.Background(), f.projectID, "R1", f.survey, eventID)
	if stored != nil && !stored.Revoked {
		t.Errorf("link still live after revoke")
	}
	rec = e.do("GET", path, nil, admin)
	var revoked surveyLinkObject
	e.decode(rec, &revoked)
	if revoked.State != surveyStateRevoked || revoked.URL != "" {
		t.Errorf("after revoke = %+v, want state revoked with no URL", revoked)
	}
	if got := surveyAudit(t, e); len(got) != 2 || got[1] != audit.SurveyLinkRevoked {
		t.Fatalf("audit = %v, want a trailing survey_link_revoked", got)
	}

	// Revoking again is the idempotent no-op: 204, no third entry.
	if rec := e.do("DELETE", path, nil, admin); rec.Code != http.StatusNoContent {
		t.Errorf("second revoke: got %d, want 204", rec.Code)
	}
	if got := surveyAudit(t, e); len(got) != 2 {
		t.Errorf("repeat revoke wrote audit: %v", got)
	}

	// Issuing after a revocation mints a different token (the old one is dead).
	rec = e.do("POST", path, nil, admin)
	var reissued surveyLinkObject
	e.decode(rec, &reissued)
	if reissued.State != surveyStateLive || reissued.URL == first.URL {
		t.Errorf("reissued = %+v, want a fresh working URL", reissued)
	}
}

// TestSurveyLinkReplacement covers the other half of REQ-API-146: issuing over a
// live link replaces its token, so the URL handed out before admits nothing from
// its next call. Only one row exists for the pair either way (REQ-DB-027).
func TestSurveyLinkReplacement(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	path := surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1")

	if rec := e.do("POST", path, nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("first issue: got %d %s", rec.Code, rec.Body.String())
	}
	rec := e.do("POST", path, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("second issue: got %d %s", rec.Code, rec.Body.String())
	}
	var second surveyLinkObject
	e.decode(rec, &second)
	if second.State != surveyStateLive {
		t.Errorf("state = %q, want live", second.State)
	}
	stored, err := e.Store.GetSurveyLink(context.Background(), f.projectID, "R1", f.survey,
		surveyEventID(t, e, f.projectID))
	if err != nil || stored == nil {
		t.Fatalf("GetSurveyLink: %v", err)
	}
	if !strings.HasSuffix(second.URL, stored.Token) {
		t.Errorf("reported URL %q is not the stored token %q", second.URL, stored.Token)
	}
	links, err := e.Store.ListSurveyLinks(context.Background(), f.projectID)
	if err != nil {
		t.Fatalf("ListSurveyLinks: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("%d links for the pair, want the one row REQ-DB-027 allows", len(links))
	}
	// Two real issuances, so two entries: a replaced link is a fact worth the trail.
	if got := surveyAudit(t, e); len(got) != 2 {
		t.Errorf("audit = %v, want two survey_link_issued", got)
	}
}

// TestSurveyLinkSubmitted covers the state a spent link reports (REQ-API-145 seen
// from the member's side): submitted with its date and no URL, and re-issue refused
// until the answers that consumed it are gone (REQ-API-146).
func TestSurveyLinkSubmitted(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	path := surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1")
	eventID := surveyEventID(t, e, f.projectID)

	e.do("POST", path, nil, admin)
	// What a stored submission leaves behind: the answer, and the stamp.
	e.mustValue(f.projectID, "R1", "v1_arm_1", "", "satisfaction", "5")
	stamped := surveyStampCollected(t, e, f.projectID, "R1", f.survey, eventID)

	rec := e.do("GET", path, nil, admin)
	var got surveyLinkObject
	e.decode(rec, &got)
	if got.State != surveyStateSubmitted || got.URL != "" ||
		got.CollectedAt == nil || *got.CollectedAt != stamped {
		t.Fatalf("report = %+v, want submitted with no URL and collected_at %q", got, stamped)
	}

	// Re-issue over collected answers is refused, naming what has to happen first.
	rec = e.do("POST", path, nil, admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-issue over stored values: got %d %s, want 409", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	e.decode(rec, &body)
	if body.Error != "conflict" || !strings.Contains(body.Message, "delete") {
		t.Errorf("409 body = %+v, want a conflict naming the delete that clears the way", body)
	}
	// The refusal changed nothing: still the spent link, still one issuance.
	if got := surveyAudit(t, e); len(got) != 1 {
		t.Errorf("refused issue wrote audit: %v", got)
	}

	// Clear the answers and the pair is open again; issuing resets the stamp, so
	// the new link has collected nothing (REQ-DB-041).
	surveyDeleteValues(t, e, f.projectID, "R1", "satisfaction")
	rec = e.do("POST", path, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue after clearing values: got %d %s", rec.Code, rec.Body.String())
	}
	var fresh surveyLinkObject
	e.decode(rec, &fresh)
	if fresh.State != surveyStateLive || fresh.URL == "" || fresh.CollectedAt != nil {
		t.Errorf("fresh issue = %+v, want live with a URL and no collection date", fresh)
	}
}

// TestSurveyLinkIssueIgnoresRecordIdentifier is the GD-8 half of REQ-API-146: every
// record stores its identifier, so counting it would refuse every issue. A survey
// holding only that value is an empty survey.
func TestSurveyLinkIssueIgnoresRecordIdentifier(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	path := surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1")

	e.mustValue(f.projectID, "R1", "v1_arm_1", "", "participant", "REC001")
	if rec := e.do("POST", path, nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("issue with only the record identifier stored: got %d %s, want 200",
			rec.Code, rec.Body.String())
	}
	// An answer, and the same call refuses.
	e.mustValue(f.projectID, "R1", "v1_arm_1", "", "satisfaction", "4")
	if rec := e.do("POST", path, nil, admin); rec.Code != http.StatusConflict {
		t.Errorf("issue with a stored answer: got %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// TestSurveyLinkRejections covers the gates of §4.17: the survey marking, the
// view_edit level, record visibility, and the unknown-resource cases. Every verb
// shares them (REQ-API-082/085/146).
func TestSurveyLinkRejections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)

	// A data-collection instrument takes no survey link → 400 invalid_request.
	for _, method := range []string{"GET", "POST"} {
		if rec := e.do(method, surveyLinkPath(f.projectID, "R1", f.plain, "v1_arm_1"), nil, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("%s on a non-survey instrument: got %d %s, want 400", method, rec.Code, rec.Body.String())
		}
	}
	// Unknown record / instrument → 404; another project's instrument → 404.
	if rec := e.do("GET", surveyLinkPath(f.projectID, "NOPE", f.survey, "v1_arm_1"), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown record: got %d, want 404", rec.Code)
	}
	if rec := e.do("POST", surveyLinkPath(f.projectID, "R1", 999999, "v1_arm_1"), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown instrument: got %d, want 404", rec.Code)
	}
	// Revoking a link that was never issued → 404.
	if rec := e.do("DELETE", surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1"), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("revoke with no link: got %d, want 404", rec.Code)
	}

	// Permission: read_only may neither report nor issue; view_edit may both.
	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("reader@example.org")
	e.mustMemberWithRole(f.projectID, reader, readRole)
	for _, method := range []string{"GET", "POST"} {
		if rec := e.do(method, surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1"), nil, reader); rec.Code != http.StatusForbidden {
			t.Errorf("%s as read_only: got %d, want 403", method, rec.Code)
		}
	}
	editRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "editor"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	editor := e.mustUser("editor@example.org")
	e.mustMemberWithRole(f.projectID, editor, editRole)
	if rec := e.do("POST", surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1"), nil, editor); rec.Code != http.StatusOK {
		t.Errorf("view_edit issuing a link: got %d %s, want 200", rec.Code, rec.Body.String())
	}

	// A grouped member cannot reach a record outside their group (403, and it says
	// nothing about whether the record exists).
	group, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center B"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	asg, err := e.Store.GetAssignment(ctx, editor.ID, f.projectID)
	if err != nil || asg == nil {
		t.Fatalf("assignment: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: asg.ID, GroupID: group, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", f.survey, "v1_arm_1"), nil, editor); rec.Code != http.StatusForbidden {
		t.Errorf("grouped member, foreign record: got %d, want 403", rec.Code)
	}
}
