package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- §4.17 survey links (REQ-API-082…085) ---

func surveyLinkPath(projectID int64, record string, instrumentID int64) string {
	return "/api/v1/projects/" + itoa(projectID) + "/records/" + record +
		"/instruments/" + itoa(instrumentID) + "/survey-link"
}

// surveyFixture is a one-arm project with a survey instrument and a plain one,
// both mapped to v1_arm_1, plus record R1.
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
	f.plain = e.mustInstrument(f.projectID, "intake")
	e.mustMap(f.projectID, armID,
		db.InstrumentEvent{InstrumentID: surveyID, EventID: v1},
		db.InstrumentEvent{InstrumentID: f.plain, EventID: v1},
	)
	e.mustRecord(f.projectID, "R1")
	return f
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

// TestSurveyLinkLifecycle walks REQ-API-082/085: issue once and the URL is
// stable; revoke takes effect immediately; issuing again after a revocation
// mints a fresh token. Audit fires on the two real changes only.
func TestSurveyLinkLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)
	path := surveyLinkPath(f.projectID, "R1", f.survey)

	rec := e.do("GET", path, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue: got %d %s", rec.Code, rec.Body.String())
	}
	var first surveyLinkObject
	e.decode(rec, &first)
	if !strings.HasPrefix(first.URL, "https://csms.example.org/s/") || first.Revoked {
		t.Fatalf("url = %q, revoked = %v", first.URL, first.Revoked)
	}
	token := strings.TrimPrefix(first.URL, "https://csms.example.org/s/")
	if len(token) < 30 {
		t.Errorf("token %q is not a link token", token)
	}
	// The stored row carries the same token and belongs to the triple.
	stored, err := e.Store.GetSurveyLink(context.Background(), f.projectID, "R1", f.survey)
	if err != nil || stored == nil {
		t.Fatalf("GetSurveyLink: %v", err)
	}
	if stored.Token != token || stored.Revoked {
		t.Errorf("stored link = %+v, want the returned unrevoked token", stored)
	}

	// Idempotent: same URL, no second audit entry.
	rec = e.do("GET", path, nil, admin)
	var again surveyLinkObject
	e.decode(rec, &again)
	if again.URL != first.URL {
		t.Errorf("second GET issued %q, want the stable %q", again.URL, first.URL)
	}
	if got := surveyAudit(t, e); len(got) != 1 || got[0] != audit.SurveyLinkIssued {
		t.Fatalf("audit = %v, want one survey_link_issued", got)
	}

	// Revoke: 204, the row flips, and the token is dead on lookup.
	if rec := e.do("DELETE", path, nil, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: got %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = e.Store.GetSurveyLink(context.Background(), f.projectID, "R1", f.survey)
	if stored != nil && !stored.Revoked {
		t.Errorf("link still live after revoke")
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
	rec = e.do("GET", path, nil, admin)
	var reissued surveyLinkObject
	e.decode(rec, &reissued)
	if reissued.URL == first.URL || reissued.Revoked {
		t.Errorf("reissued = %+v, want a fresh working URL", reissued)
	}
}

// TestSurveyLinkRejections covers the gates of §4.17: the survey marking, the
// view_edit level, record visibility, and the unknown-resource cases.
func TestSurveyLinkRejections(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	f := newSurveyFixture(t, e)

	// A data-collection instrument takes no survey link → 400 invalid_request.
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", f.plain), nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("non-survey instrument: got %d %s, want 400", rec.Code, rec.Body.String())
	}
	// Unknown record / instrument → 404; another project's instrument → 404.
	if rec := e.do("GET", surveyLinkPath(f.projectID, "NOPE", f.survey), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown record: got %d, want 404", rec.Code)
	}
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", 999999), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown instrument: got %d, want 404", rec.Code)
	}
	// Revoking a link that was never issued → 404.
	if rec := e.do("DELETE", surveyLinkPath(f.projectID, "R1", f.survey), nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("revoke with no link: got %d, want 404", rec.Code)
	}

	// Permission: read_only may not issue; view_edit may.
	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("reader@example.org")
	e.mustMemberWithRole(f.projectID, reader, readRole)
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", f.survey), nil, reader); rec.Code != http.StatusForbidden {
		t.Errorf("read_only issuing a link: got %d, want 403", rec.Code)
	}
	editRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "editor"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_none"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	editor := e.mustUser("editor@example.org")
	e.mustMemberWithRole(f.projectID, editor, editRole)
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", f.survey), nil, editor); rec.Code != http.StatusOK {
		t.Errorf("view_edit issuing a link: got %d %s, want 200", rec.Code, rec.Body.String())
	}

	// A grouped member cannot issue for a record outside their group (403, and
	// it says nothing about whether the record exists).
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
	if rec := e.do("GET", surveyLinkPath(f.projectID, "R1", f.survey), nil, editor); rec.Code != http.StatusForbidden {
		t.Errorf("grouped member, foreign record: got %d, want 403", rec.Code)
	}
}
