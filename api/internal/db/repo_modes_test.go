package db

import (
	"context"
	"testing"
)

// Project modes and the staging set (GD-20, REQ-DB-034/035). The repository is
// storage only — the allowlist and the legal transitions are enforced at the
// API boundary — but the transactional coupling to the audit entry
// (REQ-AUD-003) and the delete scope of the end-provision purge (§7.3) live
// here, so that is what these tests pin down.

func TestValidMode(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{ModeDevelopment, true},
		{ModeProduction, true},
		{ModeAnalysis, true},
		{"", false},
		{"Development", false}, // the column values are lowercase
		{"prod", false},
		{"staging", false}, // staging is a set, never a mode (REQ-DB-035)
		{"development ", false},
	}
	for _, tc := range cases {
		if got := ValidMode(tc.mode); got != tc.want {
			t.Errorf("ValidMode(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestSetProjectModeTx(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	setMode := func(mode string, commit bool) error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := s.SetProjectModeTx(ctx, tx, pid, mode); err != nil {
			tx.Rollback()
			return err
		}
		if commit {
			return tx.Commit()
		}
		return tx.Rollback()
	}

	// A committed transition lands.
	if err := setMode(ModeProduction, true); err != nil {
		t.Fatalf("SetProjectModeTx(production): %v", err)
	}
	p, err := s.GetProject(ctx, pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Mode != ModeProduction {
		t.Fatalf("mode = %q after commit, want production", p.Mode)
	}

	// A rolled-back transition does not — this is what lets the mode change and
	// its audit entry commit or fail as one unit (REQ-AUD-003).
	if err := setMode(ModeAnalysis, false); err != nil {
		t.Fatalf("SetProjectModeTx(analysis, rollback): %v", err)
	}
	p, err = s.GetProject(ctx, pid)
	if err != nil {
		t.Fatalf("GetProject after rollback: %v", err)
	}
	if p.Mode != ModeProduction {
		t.Errorf("mode = %q after rollback, want production (the write must be undone)", p.Mode)
	}
}

// seedRecordedProject builds a project with structure and record-plane rows in
// every table the purge is meant to clear: EAV data, a record entity, an
// anonymization offset and a survey link.
func seedRecordedProject(t *testing.T, s *Store, ctx context.Context, tag string) int64 {
	t.Helper()
	pid, err := s.CreateProject(ctx, &Project{
		ProjectName:      tag + "-" + t.Name(),
		ParticipantNames: "REC",
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
	for _, id := range []string{"R001", "R002"} {
		if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: id}); err != nil {
			t.Fatalf("CreateRecordEntity %s: %v", id, err)
		}
		if err := s.AddDataValue(ctx, &DataValue{
			ProjectID: pid, RecordID: id, FieldName: "age", Value: "42",
		}); err != nil {
			t.Fatalf("AddDataValue %s: %v", id, err)
		}
		if err := s.CreateAnonOffset(ctx, &AnonOffset{ProjectID: pid, RecordID: id, OffsetDays: 7}); err != nil {
			t.Fatalf("CreateAnonOffset %s: %v", id, err)
		}
	}
	link := &SurveyLink{ProjectID: pid, RecordID: "R001", InstrumentID: instrumentID}
	if _, err := s.CreateSurveyLink(ctx, link); err != nil {
		t.Fatalf("CreateSurveyLink: %v", err)
	}
	return pid
}

// countProject counts rows of a project-keyed table.
func countProject(t *testing.T, s *Store, ctx context.Context, table string, projectID int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// The end-provision purge with keep_data = false (REQ-API-105) deletes the
// data plane only: record data, record entities, survey links and
// anonymization offsets. Structure, roles and the audit trail stay so the
// project remains administrable and the deletion itself auditable
// (Data_Export_Anonymization_Design.md §7.3).
func TestPurgeProjectDataTxScope(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	victim := seedRecordedProject(t, s, ctx, "VICTIM")
	neighbour := seedRecordedProject(t, s, ctx, "NEIGHBOUR")

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	records, err := s.PurgeProjectDataTx(ctx, tx, victim)
	if err != nil {
		tx.Rollback()
		t.Fatalf("PurgeProjectDataTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The return value is the number of records removed (§7.3).
	if records != 2 {
		t.Errorf("PurgeProjectDataTx reported %d records, want 2", records)
	}

	for _, table := range []string{"data", "record_entities", "survey_links", "anon_offsets"} {
		if n := countProject(t, s, ctx, table, victim); n != 0 {
			t.Errorf("%s still holds %d rows after purge, want 0", table, n)
		}
	}
	// The design survives: re-entry to development mode is possible without
	// rebuilding the instrument layout.
	for _, table := range []string{"arms", "events", "instruments", "fields"} {
		if n := countProject(t, s, ctx, table, victim); n == 0 {
			t.Errorf("%s was emptied by the data purge, want the project design kept", table)
		}
	}
	if p, err := s.GetProject(ctx, victim); err != nil || p == nil {
		t.Fatalf("the purged project must remain administrable (GetProject: %v)", err)
	}

	// Another project's data is untouched.
	for _, table := range []string{"data", "record_entities", "anon_offsets"} {
		if n := countProject(t, s, ctx, table, neighbour); n == 0 {
			t.Errorf("purging one project also cleared %s of another", table)
		}
	}
}

// A rolled-back purge leaves the data in place — the mode transition, the
// purge and their audit entries commit together or not at all (REQ-AUD-003).
func TestPurgeProjectDataTxRollback(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedRecordedProject(t, s, ctx, "ROLLBACK")

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := s.PurgeProjectDataTx(ctx, tx, pid); err != nil {
		tx.Rollback()
		t.Fatalf("PurgeProjectDataTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	for _, table := range []string{"data", "record_entities", "survey_links", "anon_offsets"} {
		if n := countProject(t, s, ctx, table, pid); n == 0 {
			t.Errorf("%s is empty after a rolled-back purge, want the rows restored", table)
		}
	}
}

// A project with nothing to delete purges cleanly and reports zero records.
func TestPurgeProjectDataTxEmpty(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	records, err := s.PurgeProjectDataTx(ctx, tx, pid)
	if err != nil {
		tx.Rollback()
		t.Fatalf("PurgeProjectDataTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if records != 0 {
		t.Errorf("purged %d records from an empty project, want 0", records)
	}
}

// The staging set exists only while it is open, so the presence of its row is
// the "staging open" answer (REQ-DB-035).
func TestStagingLifecycle(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)
	userID := seedUser(t, s, ctx, "opener-"+t.Name()+"@example.org")

	open, err := s.StagingOpen(ctx, pid)
	if err != nil {
		t.Fatalf("StagingOpen before opening: %v", err)
	}
	if open {
		t.Errorf("a fresh project reports staging open")
	}
	if st, err := s.GetStaging(ctx, pid); err != nil || st != nil {
		t.Errorf("GetStaging before opening = %+v,%v, want nil,nil", st, err)
	}

	// Open the set with a design snapshot.
	if err := s.OpenStaging(ctx, pid, `{"instruments":["intake"]}`, userID); err != nil {
		t.Fatalf("OpenStaging: %v", err)
	}
	if open, err = s.StagingOpen(ctx, pid); err != nil || !open {
		t.Fatalf("StagingOpen after OpenStaging = %v,%v, want true,nil", open, err)
	}
	st, err := s.GetStaging(ctx, pid)
	if err != nil {
		t.Fatalf("GetStaging: %v", err)
	}
	if st == nil {
		t.Fatalf("GetStaging returned nil while a set is open")
	}
	if st.Design != `{"instruments":["intake"]}` {
		t.Errorf("staged design = %q, want the snapshot written", st.Design)
	}
	if !st.OpenedBy.Valid || st.OpenedBy.Int64 != userID {
		t.Errorf("opened_by = %v, want user %d", st.OpenedBy, userID)
	}
	if st.OpenedAt == "" {
		t.Errorf("opened_at is empty; the set records who opened it and when")
	}

	// A second open set fails loudly on the primary key rather than replacing
	// the first (the caller answers 409, REQ-API-106).
	if err := s.OpenStaging(ctx, pid, `{"instruments":["other"]}`, userID); err == nil {
		t.Errorf("a second OpenStaging succeeded, want a primary-key failure")
	}

	// A structure change applied to the set rewrites the snapshot (REQ-API-107).
	if ok, err := s.UpdateStagingDesign(ctx, pid, `{"instruments":["intake","visit"]}`); err != nil || !ok {
		t.Fatalf("UpdateStagingDesign = %v,%v, want true,nil", ok, err)
	}
	st, err = s.GetStaging(ctx, pid)
	if err != nil {
		t.Fatalf("GetStaging after update: %v", err)
	}
	if st.Design != `{"instruments":["intake","visit"]}` {
		t.Errorf("staged design = %q after update, want the new snapshot", st.Design)
	}

	// Closing the set (discard or commit) removes the row; afterwards there is
	// nothing left to update or delete.
	if ok, err := s.DeleteStaging(ctx, pid); err != nil || !ok {
		t.Fatalf("DeleteStaging = %v,%v, want true,nil", ok, err)
	}
	if open, err = s.StagingOpen(ctx, pid); err != nil || open {
		t.Errorf("StagingOpen after delete = %v,%v, want false,nil", open, err)
	}
	if ok, err := s.UpdateStagingDesign(ctx, pid, "{}"); err != nil || ok {
		t.Errorf("UpdateStagingDesign with no open set = %v,%v, want false,nil", ok, err)
	}
	if ok, err := s.DeleteStaging(ctx, pid); err != nil || ok {
		t.Errorf("DeleteStaging with no open set = %v,%v, want false,nil", ok, err)
	}
	if st, err := s.GetStaging(ctx, pid); err != nil || st != nil {
		t.Errorf("GetStaging after delete = %+v,%v, want nil,nil", st, err)
	}
}

// A staging set is dropped with its project (ON DELETE CASCADE), so deleting a
// project cannot leave an orphaned snapshot behind.
func TestStagingCascadesWithProject(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	if err := s.OpenStaging(ctx, pid, `{}`, 0); err != nil {
		t.Fatalf("OpenStaging: %v", err)
	}
	if err := s.DeleteProject(ctx, pid); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM project_staging WHERE project_id = ?`, pid).Scan(&n); err != nil {
		t.Fatalf("count project_staging: %v", err)
	}
	if n != 0 {
		t.Errorf("%d staging rows survived DeleteProject, want 0", n)
	}
}
