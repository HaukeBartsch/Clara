package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// The projects row carries identity and ethics metadata only (REQ-DB-006,
// GD-17); option flags and the like are instrument data, not project
// attributes (REQ-DB-032). These tests cover the round trip of that row, its
// nullable columns, and the two fields the repository owns: creation time and
// mode.

// ns builds a valid sql.NullString; the empty string means unset, matching how
// the repositories treat an absent optional column.
func ns(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}

// checkField compares one loaded column against its expected value, naming the
// column on failure — plain columns and nullable ones alike.
func checkField[T comparable](t *testing.T, column string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", column, got, want)
	}
}

func TestProjectRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	want := &Project{
		ProjectName:      "PROJ-" + t.Name(),
		Organization:     "HSN",
		PIName:           "Pat Investigator",
		PIEmail:          "pi@example.org",
		DMName:           ns("Data Manager"),
		DMEmail:          ns("dm@example.org"),
		RekNumber:        ns("REK 2026/1234"),
		RekStartDate:     ns("2026-01-01"),
		RekEndDate:       ns("2027-01-01"),
		StartDate:        ns("2026-02-01"),
		EndDate:          ns("2027-02-01"),
		ParticipantNames: "8DISC[0-9][0-9][0-9]",
	}
	id, err := s.CreateProject(ctx, want)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if id == 0 {
		t.Fatalf("CreateProject returned id 0")
	}

	got, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got == nil {
		t.Fatalf("GetProject returned nil for an existing project")
	}
	checkField(t, "project_name", got.ProjectName, want.ProjectName)
	checkField(t, "organization", got.Organization, want.Organization)
	checkField(t, "pi_name", got.PIName, want.PIName)
	checkField(t, "pi_email", got.PIEmail, want.PIEmail)
	checkField(t, "dm_name", got.DMName, want.DMName)
	checkField(t, "dm_email", got.DMEmail, want.DMEmail)
	checkField(t, "rek_number", got.RekNumber, want.RekNumber)
	checkField(t, "rek_start_date", got.RekStartDate, want.RekStartDate)
	checkField(t, "rek_end_date", got.RekEndDate, want.RekEndDate)
	checkField(t, "start_date", got.StartDate, want.StartDate)
	checkField(t, "end_date", got.EndDate, want.EndDate)
	checkField(t, "participant_names", got.ParticipantNames, want.ParticipantNames)

	// Creation time defaults to server UTC (REQ-DB-005) and every project
	// starts in development mode (REQ-DB-034).
	if _, err := time.Parse(datetimeLayout, got.CreationTime); err != nil {
		t.Errorf("creation_time = %q is not a DATETIME default: %v", got.CreationTime, err)
	}
	if got.Mode != ModeDevelopment {
		t.Errorf("mode = %q, want %q for a new project", got.Mode, ModeDevelopment)
	}
}

// Every optional column may be absent; the scan helpers must report them unset
// rather than as empty strings, which the API renders differently (null vs "").
func TestProjectNullables(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	id, err := s.CreateProject(ctx, &Project{
		ProjectName:      "BARE-" + t.Name(),
		ParticipantNames: "REC",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	for name, ns := range map[string]sql.NullString{
		"dm_name":        got.DMName,
		"dm_email":       got.DMEmail,
		"rek_number":     got.RekNumber,
		"rek_start_date": got.RekStartDate,
		"rek_end_date":   got.RekEndDate,
		"start_date":     got.StartDate,
		"end_date":       got.EndDate,
	} {
		if ns.Valid {
			t.Errorf("%s = %q, want unset on a bare project", name, ns.String)
		}
	}
}

func TestGetProjectMissing(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	p, err := s.GetProject(ctx, 999999)
	if err != nil {
		t.Fatalf("GetProject on a missing id: %v", err)
	}
	if p != nil {
		t.Errorf("GetProject returned %+v for a missing id, want nil", p)
	}
}

// The project name is unique (REQ-DB-006) and the API resolves projects by it.
func TestGetProjectByName(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	name := "NAMED-" + t.Name()

	id, err := s.CreateProject(ctx, &Project{ProjectName: name, ParticipantNames: "REC"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := s.GetProjectByName(ctx, name)
	if err != nil {
		t.Fatalf("GetProjectByName: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("GetProjectByName(%q) = %+v, want the row with id %d", name, got, id)
	}

	missing, err := s.GetProjectByName(ctx, name+"-nope")
	if err != nil {
		t.Fatalf("GetProjectByName on a missing name: %v", err)
	}
	if missing != nil {
		t.Errorf("GetProjectByName returned %+v for an unknown name, want nil", missing)
	}
}

func TestListProjectsOrderedByName(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if _, err := s.CreateProject(ctx, &Project{
			ProjectName: name + "-" + t.Name(), ParticipantNames: "REC",
		}); err != nil {
			t.Fatalf("CreateProject %s: %v", name, err)
		}
	}

	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListProjects returned %d projects, want 3", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ProjectName > list[i].ProjectName {
			t.Errorf("ListProjects is not ordered by name: %q before %q",
				list[i-1].ProjectName, list[i].ProjectName)
		}
	}
}

// PUT rewrites the mutable fields including the name, but never the creation
// time (fixed at creation) and never the mode, which only the mode endpoint
// changes (REQ-DB-034).
func TestUpdateProjectLeavesCreationTimeAndMode(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	id, err := s.CreateProject(ctx, &Project{
		ProjectName: "OLD-" + t.Name(), Organization: "HSN", ParticipantNames: "REC",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	before, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}

	// Move the mode through its own endpoint (REQ-API-105).
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := s.SetProjectModeTx(ctx, tx, id, ModeProduction); err != nil {
		tx.Rollback()
		t.Fatalf("SetProjectModeTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	updated := *before
	updated.ProjectName = "NEW-" + t.Name()
	updated.Organization = "OsloMet"
	updated.PIName = "New Investigator"
	// A stale mode on the struct must not reach the column.
	updated.Mode = ModeAnalysis
	if err := s.UpdateProject(ctx, &updated); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}

	got, err := s.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject after update: %v", err)
	}
	if got.ProjectName != updated.ProjectName {
		t.Errorf("project_name = %q, want %q", got.ProjectName, updated.ProjectName)
	}
	if got.Organization != "OsloMet" || got.PIName != "New Investigator" {
		t.Errorf("identity fields not updated: %+v", got)
	}
	if got.Mode != ModeProduction {
		t.Errorf("mode = %q after UpdateProject, want production (the mode is not a mutable project field)", got.Mode)
	}
	if got.CreationTime != before.CreationTime {
		t.Errorf("creation_time = %q after update, want the original %q", got.CreationTime, before.CreationTime)
	}
}

// Deleting a project cascades its structure and data (REQ-DB-004) — what must
// survive is nothing of the project at all.
func TestDeleteProjectCascades(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	pid, err := s.CreateProject(ctx, &Project{
		ProjectName: "DOOM-" + t.Name(), ParticipantNames: "REC",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	armID, err := s.AddArm(ctx, &Arm{ProjectID: pid, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	if _, err := s.AddEvent(ctx, &Event{
		ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1",
	}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	instrumentID, err := s.AddInstrument(ctx, &Instrument{ProjectID: pid, Name: "intake"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	if _, err := s.AddField(ctx, &Field{
		ProjectID: pid, InstrumentID: instrumentID, FieldName: "age", FieldType: "text",
	}); err != nil {
		t.Fatalf("AddField: %v", err)
	}
	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: "R001"}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	if err := s.AddDataValue(ctx, &DataValue{
		ProjectID: pid, RecordID: "R001", FieldName: "age", Value: "42",
	}); err != nil {
		t.Fatalf("AddDataValue: %v", err)
	}

	if err := s.DeleteProject(ctx, pid); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	count := func(table string) int {
		t.Helper()
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, pid).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	for _, table := range []string{
		"arms", "events", "instruments", "fields", "data", "record_entities",
	} {
		if n := count(table); n != 0 {
			t.Errorf("%s still holds %d rows after DeleteProject, want 0", table, n)
		}
	}
	p, err := s.GetProject(ctx, pid)
	if err != nil {
		t.Fatalf("GetProject after delete: %v", err)
	}
	if p != nil {
		t.Errorf("project %d survived DeleteProject", pid)
	}
}
