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

// --- §4.28 audited record read (REQ-API-167, REQ-AUD-035) ---

// readPath builds the endpoint address with its optional query string.
func readPath(projectID int64, record, query string) string {
	return "/api/v1/projects/" + itoa(projectID) + "/records/" + record + "/read" + query
}

// lastRecordRead reads back the newest record_read entry from the trail: its
// source, actor, target record and decoded payload.
func lastRecordRead(t *testing.T, e *env) (source, email, target string, details map[string]any) {
	t.Helper()
	var raw sql.NullString
	err := e.Store.DB.QueryRow(
		`SELECT source, email, target_record, details FROM audit_events
		  WHERE event_type = ? ORDER BY created_at DESC, id DESC LIMIT 1`, audit.RecordRead).
		Scan(&source, &email, &target, &raw)
	if err != nil {
		t.Fatalf("no record_read entry: %v", err)
	}
	details = map[string]any{}
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &details); err != nil {
			t.Fatalf("record_read details %q: %v", raw.String, err)
		}
	}
	return source, email, target, details
}

// countRecordRead counts the trail's record_read entries — every denial must
// leave this where it was (REQ-AUD-004).
func countRecordRead(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, audit.RecordRead).Scan(&n); err != nil {
		t.Fatalf("counting record_read: %v", err)
	}
	return n
}

// TestRecordRead is the happy path of §4.28 and REQ-AUD-035: a read_only member
// opening a stored record writes exactly one entry, naming who, which record,
// and what was displayed — with no values, source=ui, and no token. A further
// instance is named; the first is not, as in §3.2.
func TestRecordRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := newCompletionFixture(t, e) // one arm, v1_arm_1, intake/scores mapped, record R1
	readRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "reader"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	reader := e.mustUser("read@example.org")
	e.mustMemberWithRole(f.projectID, reader, readRole)

	before := countRecordRead(t, e)
	rec := e.do("POST", readPath(f.projectID, "R1", "?event=v1_arm_1&instrument=intake"), nil, reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("record read: got %d %s", rec.Code, rec.Body.String())
	}
	var answer map[string]bool
	e.decode(rec, &answer)
	if !answer["ok"] {
		t.Fatalf("body = %s, want ok true", rec.Body.String())
	}
	if n := countRecordRead(t, e); n != before+1 {
		t.Fatalf("record_read entries = %d, want %d (one per page view)", n, before+1)
	}
	source, email, target, details := lastRecordRead(t, e)
	if source != audit.SourceUI {
		t.Errorf("source = %q, want %q (a browser read has no token, REQ-AUD-017/018)", source, audit.SourceUI)
	}
	if email != reader.Email {
		t.Errorf("email = %q, want the acting member", email)
	}
	if target != "R1" {
		t.Errorf("target_record = %q, want R1 (§6.4)", target)
	}
	if details["event"] != "v1_arm_1" || details["instrument"] != "intake" {
		t.Errorf("details = %v, want the displayed pair", details)
	}
	if _, seen := details["instance"]; seen {
		t.Errorf("details = %v, instance must be absent for the first instance (§3.2)", details)
	}
	for key := range details {
		if key != "event" && key != "instrument" && key != "instance" {
			t.Errorf("details carry %q beyond the displayed triple (REQ-AUD-014): %v", key, details)
		}
	}

	// A further instance names itself.
	rec = e.do("POST", readPath(f.projectID, "R1", "?event=v1_arm_1&instrument=intake&instance=3"), nil, reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance 3: got %d %s", rec.Code, rec.Body.String())
	}
	if _, _, _, details = lastRecordRead(t, e); details["instance"] != float64(3) {
		t.Errorf("details = %v, want instance 3", details)
	}

	// The record-view table stays the API pull trail (REQ-AUD-013, DEV-AUD-6).
	var views int
	if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_record_views`).Scan(&views); err != nil || views != 0 {
		t.Errorf("audit_record_views rows = %d (err %v), want 0 for a browser read", views, err)
	}

	// The entry reaches the §4.15 listing and its event_type filter.
	admin := e.mustAdmin("admin@example.org")
	rec = e.do("GET", "/api/v1/audit?type=events&event_type="+audit.RecordRead, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing: got %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Entries []struct {
			EventType string `json:"event_type"`
		} `json:"entries"`
	}
	e.decode(rec, &page)
	if len(page.Entries) < 2 {
		t.Fatalf("listing holds %d entries, want the two reads above", len(page.Entries))
	}
	for _, entry := range page.Entries {
		if entry.EventType != audit.RecordRead {
			t.Fatalf("listing leaked %q under the record_read filter", entry.EventType)
		}
	}
}

// TestRecordReadGates covers every path that must write nothing (REQ-AUD-004):
// a stranger, a role without data access, a record outside the member's active
// group, an unknown record, and a malformed call (REQ-API-167).
func TestRecordReadGates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := newCompletionFixture(t, e)

	// A member whose active group is Center A: R1 holds no group, so it is not
	// visible to them (REQ-AUTH-045).
	groupA, err := e.Store.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: f.projectID, Name: "Center A"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	grouped := e.mustUser("grouped@example.org")
	e.mustMemberWithRole(f.projectID, grouped, 0)
	a, err := e.Store.GetAssignment(ctx, grouped.ID, f.projectID)
	if err != nil || a == nil {
		t.Fatalf("assignment: %v", err)
	}
	if _, err := e.Store.AddDAGMembership(ctx, &db.DagMembership{
		AssignmentID: a.ID, GroupID: groupA, IsActive: true,
	}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}

	// A role-less member of the project: allowed to read R1 (full rights), so the
	// shape rejections below reach the write path before refusing it.
	full := e.mustUser("full@example.org")
	e.mustMemberWithRole(f.projectID, full, 0)

	outsider := e.mustUser("out@example.org")
	watcherRole, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: f.projectID, RoleName: "watcher"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "no_access", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	watcher := e.mustUser("watch@example.org")
	e.mustMemberWithRole(f.projectID, watcher, watcherRole)

	cases := []struct {
		name   string
		path   string
		actor  *db.User
		status int
	}{
		{"non-member", readPath(f.projectID, "R1", ""), outsider, http.StatusForbidden},
		{"no data access", readPath(f.projectID, "R1", ""), watcher, http.StatusForbidden},
		{"record outside active group", readPath(f.projectID, "R1", ""), grouped, http.StatusForbidden},
		{"unknown record", readPath(f.projectID, "NOPE", "?event=v1_arm_1"), full, http.StatusNotFound},
		{"unknown project", "/api/v1/projects/999999/records/R1/read", full, http.StatusNotFound},
		{"instance zero", readPath(f.projectID, "R1", "?instance=0"), full, http.StatusBadRequest},
		{"event too long", readPath(f.projectID, "R1", "?event="+strings.Repeat("e", 256)), full, http.StatusBadRequest},
	}
	for _, c := range cases {
		before := countRecordRead(t, e)
		rec := e.do("POST", c.path, nil, c.actor)
		if rec.Code != c.status {
			t.Errorf("%s: got %d %s, want %d", c.name, rec.Code, rec.Body.String(), c.status)
		}
		if n := countRecordRead(t, e); n != before {
			t.Errorf("%s wrote %d entries, want none", c.name, n-before)
		}
	}

	// A body carries nothing: any attribute is 400 (REQ-API-167).
	before := countRecordRead(t, e)
	rec := e.do("POST", readPath(f.projectID, "R1", ""), map[string]any{"event": "v1_arm_1"}, full)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("body attribute: got %d %s, want 400", rec.Code, rec.Body.String())
	}
	if n := countRecordRead(t, e); n != before {
		t.Errorf("refused body wrote %d entries, want none", n-before)
	}
	// An empty object is the accepted shape.
	rec = e.do("POST", readPath(f.projectID, "R1", "?event=v1_arm_1"), map[string]any{}, full)
	if rec.Code != http.StatusOK {
		t.Errorf("empty body: got %d %s, want 200", rec.Code, rec.Body.String())
	}
}
