package admin

import (
	"context"
	"net/http"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// The uniform 403s of REQ-API-007 that no other test reached: a member without
// data access on any arm, a record outside the caller's active data access
// group (REQ-AUTH-045), and an installation administrator who holds no
// assignment of their own. Each denial is paired with the same call from a
// caller who may make it, so the 403 is the permission and not the request.

// denialFixture extends the DAG fixture: R3 sits in group B, the reader and the
// role-less project admin both work in group A, and "blind" is a member whose
// role grants no data access on the project's only arm.
type denialFixture struct {
	dagFixture
	admin *db.User
	blind *db.User
	calc  int64 // a calculated field on the intake instrument
}

func newDenialFixture(t *testing.T, e *env) denialFixture {
	t.Helper()
	ctx := context.Background()
	f := denialFixture{dagFixture: newDAGFixture(t, e)}
	f.admin = e.mustAdmin("admin@example.org")

	if err := e.Store.CreateRecordEntity(ctx, &db.RecordEntity{
		ProjectID: f.projectID, RecordID: "R3", DagGroupID: nullInt64(f.groupB),
	}); err != nil {
		t.Fatalf("CreateRecordEntity R3: %v", err)
	}
	for _, u := range []*db.User{f.reader, f.editor} {
		rec := e.do("PUT", memberDAGPath(f.projectID, u.ID),
			map[string]any{"groups": []int64{f.groupA}, "active_group_id": f.groupA}, f.admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("assign %s to group A: %d %s", u.Email, rec.Code, rec.Body.String())
		}
	}

	blindRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "blind"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "no_access", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole blind: %v", err)
	}
	f.blind = e.mustUser("blind@example.org")
	e.mustMemberWithRole(f.projectID, f.blind, blindRole)

	instruments, err := e.Store.ListInstruments(ctx, f.projectID)
	if err != nil || len(instruments) != 1 {
		t.Fatalf("ListInstruments: %v (%d)", err, len(instruments))
	}
	f.calc = e.mustCalcField(f.projectID, instruments[0].ID, "score", "1 + 1", 1)
	return f
}

func (f denialFixture) path(suffix string) string {
	return "/api/v1/projects/" + itoa(f.projectID) + suffix
}

// expectCodes runs one request per caller and compares each status code.
func expectCodes(t *testing.T, e *env, method, path string, body any, want map[*db.User]int) {
	t.Helper()
	for u, code := range want {
		if rec := e.do(method, path, body, u); rec.Code != code {
			t.Errorf("%s %s as %s = %d %s, want %d", method, path, u.Email, rec.Code, rec.Body.String(), code)
		}
	}
}

// TestStructureListingNeedsDataAccess: a member whose role grants no data
// access on any arm is refused the structure listings (§4.8, requireRead).
func TestStructureListingNeedsDataAccess(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	expectCodes(t, e, "GET", f.path("/arms"), nil, map[*db.User]int{
		f.blind:  http.StatusForbidden,
		f.reader: http.StatusOK,
	})
}

// TestProjectModeNeedsDataAccess: the mode badge is for members with data
// access ≥ read_only (§4.21); membership alone is not enough.
func TestProjectModeNeedsDataAccess(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	expectCodes(t, e, "GET", f.path("/mode"), nil, map[*db.User]int{
		f.blind:  http.StatusForbidden,
		f.reader: http.StatusOK,
	})
}

// TestRecordHistoryNeedsDataAccess: a member without data access on any arm
// cannot read a record's history, even one inside its scope (GD-2).
func TestRecordHistoryNeedsDataAccess(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	expectCodes(t, e, "GET", e.historyPath(f.projectID, "R2"), nil, map[*db.User]int{
		f.blind: http.StatusForbidden,
		f.admin: http.StatusOK,
	})
}

// TestTransfersHideRecordsOutsideTheActiveGroup: the transfer probe answers the
// uniform 403 for a record in another data access group and never discloses it
// (REQ-API-007, REQ-AUTH-045).
func TestTransfersHideRecordsOutsideTheActiveGroup(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	expectCodes(t, e, "GET", f.path("/records/R3/transfers?instrument=intake"), nil, map[*db.User]int{
		f.reader: http.StatusForbidden,
		f.admin:  http.StatusOK,
	})
	expectCodes(t, e, "GET", f.path("/records/R1/transfers?instrument=intake"), nil, map[*db.User]int{
		f.reader: http.StatusOK,
	})
	// A record that does not exist reads exactly like one in another group.
	expectCodes(t, e, "GET", f.path("/records/NOPE/transfers?instrument=intake"), nil, map[*db.User]int{
		f.reader: http.StatusForbidden,
	})
}

// TestTestFieldHidesRecordsOutsideTheActiveGroup: even a project admin cannot
// evaluate a calculation against a record outside its active group (§4.11,
// REQ-AUTH-045).
func TestTestFieldHidesRecordsOutsideTheActiveGroup(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	testURL := func(record string) string {
		return f.path("/records/" + record + "/fields/" + itoa(f.calc) + "/test")
	}
	expectCodes(t, e, "POST", testURL("R3"), map[string]any{}, map[*db.User]int{
		f.editor: http.StatusForbidden,
		f.admin:  http.StatusOK,
	})
	expectCodes(t, e, "POST", testURL("R1"), map[string]any{}, map[*db.User]int{
		f.editor: http.StatusOK,
	})
}

// TestActiveGroupNeedsAnAssignment: an installation administrator counts as a
// member (REQ-AUTH-023) but holds no assignment, so there are no groups to
// switch between; the refusal writes no audit entry (REQ-AUD-004).
func TestActiveGroupNeedsAnAssignment(t *testing.T) {
	e := newEnv(t)
	f := newDenialFixture(t, e)
	before := e.auditCount(audit.DagActiveSwitched, f.projectID)
	expectCodes(t, e, "PUT", f.path("/active-data-access-group"), map[string]any{"group_id": f.groupA},
		map[*db.User]int{f.admin: http.StatusForbidden})
	if got := e.auditCount(audit.DagActiveSwitched, f.projectID); got != before {
		t.Errorf("a refused switch wrote %d audit rows", got-before)
	}
	expectCodes(t, e, "PUT", f.path("/active-data-access-group"), map[string]any{"group_id": f.groupA},
		map[*db.User]int{f.reader: http.StatusOK})
}
