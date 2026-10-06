package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// seedRecord inserts one record with one stored value, the smallest shape the
// keep_data = false purge has to remove (REQ-API-105).
func (e *env) seedRecord(projectID int64, record string) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.Store.DB.ExecContext(ctx,
		`INSERT INTO record_entities (project_id, record_id, created_at) VALUES (?, ?, '2026-01-01 00:00:00')`,
		projectID, record); err != nil {
		e.t.Fatalf("record_entities: %v", err)
	}
	if _, err := e.Store.DB.ExecContext(ctx,
		`INSERT INTO data (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name, value)
		 VALUES (?, ?, 'baseline_arm_1', '', 1, 'age', '42')`, projectID, record); err != nil {
		e.t.Fatalf("data: %v", err)
	}
}

func (e *env) countRows(table string, projectID int64) int {
	e.t.Helper()
	var n int
	err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, projectID).Scan(&n)
	if err != nil {
		e.t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestModeDefaultsToDevelopment covers the read side of REQ-API-105 and the
// default mode of a new project (REQ-DB-034).
func TestModeDefaultsToDevelopment(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	pid := e.mustProject("Modes Default")

	rec := e.do("GET", "/api/v1/projects/"+itoa(pid)+"/mode", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mode status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body modeObject
	e.decode(rec, &body)
	if body.Mode != db.ModeDevelopment {
		t.Errorf("mode = %q, want %q", body.Mode, db.ModeDevelopment)
	}
	if body.StagingOpen {
		t.Error("staging_open = true for a project with no staging row")
	}
}

// TestModeReadGating: an outsider is rejected without disclosing existence, and
// a member needs data access ≥ read_only (REQ-API-105).
func TestModeReadGating(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin2@example.org")
	outsider := e.mustUser("outsider@example.org")
	observer := e.mustUser("observer@example.org")
	pid := e.mustProject("Modes Gating")

	if rec := e.do("GET", "/api/v1/projects/"+itoa(pid)+"/mode", nil, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-member status = %d, want 403", rec.Code)
	}
	roleID, err := e.Store.CreateRole(ctx, &db.Role{ProjectID: pid, RoleName: "viewer"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_none"}}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := e.Store.AddAssignment(ctx, &db.Assignment{
		UserID: observer.ID, ProjectID: pid, RoleID: nullInt64(roleID),
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	if rec := e.do("GET", "/api/v1/projects/"+itoa(pid)+"/mode", nil, observer); rec.Code != http.StatusOK {
		t.Errorf("read_only member status = %d, want 200", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/9999/mode", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rec.Code)
	}
}

// TestModeChangeRequiresIsAdmin: the project's own project_admin cannot change
// the mode — only an installation administrator can (GD-20, 2026-09-27).
func TestModeChangeRequiresIsAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pid := e.mustProject("Modes Permission")

	// A role-less member is project_admin (REQ-AUTH-022) but not is_admin.
	projectAdmin := e.mustUser("padmin@example.org")
	if _, err := e.Store.AddAssignment(ctx, &db.Assignment{
		UserID: projectAdmin.ID, ProjectID: pid,
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
		map[string]any{"mode": "production", "keep_data": true}, projectAdmin)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_admin PUT mode status = %d, want 403", rec.Code)
	}
	// and the project stayed in development.
	if p, err := e.Store.GetProject(ctx, pid); err != nil || p.Mode != db.ModeDevelopment {
		t.Errorf("project after rejected PUT: mode=%q err=%v", p.Mode, err)
	}
}

// TestModeTransitionToProduction covers both keep_data branches of
// development → production (REQ-API-105).
func TestModeTransitionToProduction(t *testing.T) {
	t.Run("keep_data missing is 400", func(t *testing.T) {
		e := newEnv(t)
		admin := e.mustAdmin("a1@example.org")
		pid := e.mustProject("Keep Missing")
		rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
			map[string]any{"mode": "production"}, admin)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("keep_data true keeps the data", func(t *testing.T) {
		e := newEnv(t)
		admin := e.mustAdmin("a2@example.org")
		pid := e.mustProject("Keep True")
		e.seedRecord(pid, "R001")

		rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
			map[string]any{"mode": "production", "keep_data": true}, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		var body modeUpdateObject
		e.decode(rec, &body)
		if body.Mode != db.ModeProduction || body.RecordsDeleted != 0 {
			t.Errorf("body = %+v, want production / 0 deleted", body)
		}
		if got := e.countRows("record_entities", pid); got != 1 {
			t.Errorf("records after keep_data=true = %d, want 1", got)
		}
	})

	t.Run("keep_data false purges the data plane only", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		admin := e.mustAdmin("a3@example.org")
		pid := e.mustProject("Keep False")
		// Structure the purge must survive (the endpoint-created project has
		// arm 1; mustProject writes the row directly, so add it here).
		if _, err := e.Store.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: 1}); err != nil {
			t.Fatalf("AddArm: %v", err)
		}
		e.seedRecord(pid, "R001")
		e.seedRecord(pid, "R002")
		if _, err := e.Store.DB.ExecContext(ctx,
			`INSERT INTO anon_offsets (project_id, record_id, offset_days) VALUES (?, 'R001', 7)`,
			pid); err != nil {
			t.Fatalf("anon_offsets: %v", err)
		}

		rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
			map[string]any{"mode": "production", "keep_data": false}, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		var body modeUpdateObject
		e.decode(rec, &body)
		if body.RecordsDeleted != 2 {
			t.Errorf("records_deleted = %d, want 2", body.RecordsDeleted)
		}
		for _, table := range []string{"data", "record_entities", "anon_offsets", "survey_links"} {
			if got := e.countRows(table, pid); got != 0 {
				t.Errorf("%s rows after purge = %d, want 0", table, got)
			}
		}
		// Metadata and structure survive: the project stays administrable (§7.3).
		p, err := e.Store.GetProject(ctx, pid)
		if err != nil || p == nil {
			t.Fatalf("project gone after purge: %v", err)
		}
		if p.Mode != db.ModeProduction {
			t.Errorf("mode = %q, want production", p.Mode)
		}
		if arms, err := e.Store.ListArms(ctx, pid); err != nil || len(arms) == 0 {
			t.Errorf("arms after purge = %d (err %v), want the structure kept", len(arms), err)
		}
	})
}

// TestModeTransitionTable checks the allowed pairs and the rejections,
// including analysis → development (added 2026-09-27) and the missing
// development → analysis.
func TestModeTransitionTable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("t@example.org")
	pid := e.mustProject("Transitions")

	doPut := func(body map[string]any) int {
		rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode", body, admin)
		return rec.Code
	}

	// development → analysis is not offered.
	if code := doPut(map[string]any{"mode": "analysis"}); code != http.StatusConflict {
		t.Errorf("development → analysis status = %d, want 409", code)
	}
	// production → analysis, then back out to development directly.
	if code := doPut(map[string]any{"mode": "production", "keep_data": true}); code != http.StatusOK {
		t.Fatalf("development → production status = %d", code)
	}
	if code := doPut(map[string]any{"mode": "analysis"}); code != http.StatusOK {
		t.Errorf("production → analysis status = %d, want 200", code)
	}
	if p, _ := e.Store.GetProject(ctx, pid); p.Mode != db.ModeAnalysis {
		t.Errorf("mode = %q, want analysis", p.Mode)
	}
	if code := doPut(map[string]any{"mode": "development"}); code != http.StatusOK {
		t.Errorf("analysis → development status = %d, want 200", code)
	}
	if p, _ := e.Store.GetProject(ctx, pid); p.Mode != db.ModeDevelopment {
		t.Errorf("mode = %q, want development", p.Mode)
	}
	// An unknown mode is a bad request, not a conflict.
	if code := doPut(map[string]any{"mode": "staging"}); code != http.StatusBadRequest {
		t.Errorf("unknown mode status = %d, want 400", code)
	}
}

// TestModeIdempotent: naming the current mode succeeds with no write and no
// audit entry (REQ-API-042).
func TestModeIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("idem@example.org")
	pid := e.mustProject("Idempotent")

	if rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
		map[string]any{"mode": "development"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("same-mode PUT status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := e.auditCount(audit.ProjectModeChanged, pid); got != 0 {
		t.Errorf("mode-change audit rows = %d, want 0 for a no-op", got)
	}
	if p, _ := e.Store.GetProject(ctx, pid); p.Mode != db.ModeDevelopment {
		t.Errorf("mode = %q, want development", p.Mode)
	}
}

// TestModeBlockedByOpenStaging: no transition while a staging set is open, for
// any target mode (REQ-API-105, GD-20 2026-09-27).
func TestModeBlockedByOpenStaging(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("staging@example.org")
	pid := e.mustProject("Staging Block")

	// Production with an open set: neither way out is accepted.
	if rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
		map[string]any{"mode": "production", "keep_data": true}, admin); rec.Code != http.StatusOK {
		t.Fatalf("to production status = %d", rec.Code)
	}
	if err := e.Store.OpenStaging(context.Background(), pid, `{"instruments":[]}`, admin.ID); err != nil {
		t.Fatalf("OpenStaging: %v", err)
	}
	rec := e.do("GET", "/api/v1/projects/"+itoa(pid)+"/mode", nil, admin)
	var body modeObject
	e.decode(rec, &body)
	if !body.StagingOpen {
		t.Errorf("staging_open = false with a staging row present")
	}
	for _, target := range []string{"development", "analysis"} {
		rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
			map[string]any{"mode": target}, admin)
		if rec.Code != http.StatusConflict {
			t.Errorf("production → %s with open staging = %d, want 409 (%s)",
				target, rec.Code, rec.Body.String())
		}
	}
}

// TestModeAuditPayload checks the §3.8 payload: old and new always, keep_data
// and records_deleted only on development → production.
func TestModeAuditPayload(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("audit@example.org")
	pid := e.mustProject("Audit Payload")

	if rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
		map[string]any{"mode": "production", "keep_data": false}, admin); rec.Code != http.StatusOK {
		t.Fatalf("to production status = %d", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/projects/"+itoa(pid)+"/mode",
		map[string]any{"mode": "analysis"}, admin); rec.Code != http.StatusOK {
		t.Fatalf("to analysis status = %d", rec.Code)
	}
	rows := e.auditDetails(audit.ProjectModeChanged, pid)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(rows))
	}
	first := rows[0]
	if first["old"] != "development" || first["new"] != "production" {
		t.Errorf("first entry old/new = %v/%v", first["old"], first["new"])
	}
	if kd, ok := first["keep_data"].(bool); !ok || kd {
		t.Errorf("first entry keep_data = %v, want false", first["keep_data"])
	}
	if n, ok := first["records_deleted"].(float64); !ok || n != 0 {
		t.Errorf("first entry records_deleted = %v, want 0", first["records_deleted"])
	}
	second := rows[1]
	if second["old"] != "production" || second["new"] != "analysis" {
		t.Errorf("second entry old/new = %v/%v", second["old"], second["new"])
	}
	if _, present := second["keep_data"]; present {
		t.Error("keep_data present on a keep-all transition")
	}
}

// auditCount returns the number of audit entries of one event type for a
// project.
func (e *env) auditCount(eventType string, projectID int64) int {
	e.t.Helper()
	var n int
	err := e.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE event_type = ? AND project_id = ?`,
		eventType, projectID).Scan(&n)
	if err != nil {
		e.t.Fatalf("count %s: %v", eventType, err)
	}
	return n
}

// auditDetails returns the details payload of every entry of one event type for
// a project, oldest first.
func (e *env) auditDetails(eventType string, projectID int64) []map[string]any {
	e.t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT details FROM audit_events WHERE event_type = ? AND project_id = ? ORDER BY id`,
		eventType, projectID)
	if err != nil {
		e.t.Fatalf("query %s: %v", eventType, err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw *string
		if err := rows.Scan(&raw); err != nil {
			e.t.Fatalf("scan %s: %v", eventType, err)
		}
		var m map[string]any
		if raw != nil && *raw != "" {
			if err := json.Unmarshal([]byte(*raw), &m); err != nil {
				e.t.Fatalf("decode %s details %q: %v", eventType, *raw, err)
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatalf("rows %s: %v", eventType, err)
	}
	return out
}
