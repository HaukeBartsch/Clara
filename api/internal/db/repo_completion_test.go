package db

import (
	"context"
	"testing"
)

// Instrument completion storage (REQ-DB-036, DEV-DB-10). The table is sparse —
// a row exists only while the user's "finished" assignment stands — so what
// these tests pin down is that a clear really deletes, that re-setting changes
// nothing (the idempotence REQ-API-110 promises), and that the cascades take
// the assignments with the event, the instrument and the project.

// seedCompletionCell returns a project with one event, one instrument and one
// record — the smallest thing a completion row can point at.
func seedCompletionCell(t *testing.T, s *Store, ctx context.Context) (int64, int64, int64, int64, string) {
	t.Helper()
	projectID := seedProject(t, s, ctx)
	userID := seedUser(t, s, ctx, "completer@"+t.Name()+".example.org")

	armID, err := s.AddArm(ctx, &Arm{ProjectID: projectID, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm: %v", err)
	}
	eventID, err := s.AddEvent(ctx, &Event{
		ProjectID: projectID, ArmID: armID, EventName: "Visit 1", UniqueEventName: "v1_arm_1",
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	instrumentID, err := s.AddInstrument(ctx, &Instrument{
		ProjectID: projectID, Name: "intake", Position: 1,
	})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}
	recordID := "R1"
	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: projectID, RecordID: recordID}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	return projectID, userID, eventID, instrumentID, recordID
}

// setCommitted writes one assignment in its own committed transaction — the
// preamble every test here needs before it can observe or undo the state.
func setCommitted(t *testing.T, s *Store, ctx context.Context, projectID int64,
	recordID string, eventID, instrumentID, userID int64) {
	t.Helper()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := s.SetInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID, userID); err != nil {
		t.Fatalf("SetInstrumentCompletionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func completionCount(t *testing.T, s *Store, ctx context.Context, projectID int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM instrument_completion WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestSetInstrumentCompletionTx(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	projectID, userID, eventID, instrumentID, recordID := seedCompletionCell(t, s, ctx)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	created, err := s.SetInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID, userID)
	if err != nil {
		t.Fatalf("SetInstrumentCompletionTx: %v", err)
	}
	if !created {
		t.Fatalf("first assignment reported no row created")
	}
	// Repeating the call is a no-op — at most one row per triple (REQ-DB-036),
	// and the caller learns nothing changed so it writes no second audit entry.
	again, err := s.SetInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID, userID)
	if err != nil {
		t.Fatalf("SetInstrumentCompletionTx repeat: %v", err)
	}
	if again {
		t.Fatalf("repeat assignment reported a second row")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	list, err := s.ListInstrumentCompletions(ctx, projectID)
	if err != nil {
		t.Fatalf("ListInstrumentCompletions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("stored assignments = %d, want exactly 1 (sparse, one row per triple)", len(list))
	}
	got := list[0]
	if got.RecordID != recordID || got.EventID != eventID || got.InstrumentID != instrumentID {
		t.Errorf("cell = %+v, want (%s, %d, %d)", got, recordID, eventID, instrumentID)
	}
	if !got.CompletedBy.Valid || got.CompletedBy.Int64 != userID {
		t.Errorf("completed_by = %v, want %d (REQ-DB-036 stores who set it)", got.CompletedBy, userID)
	}
	if got.CompletedAt == "" {
		t.Errorf("completed_at is empty — REQ-DB-036 stores when")
	}

	// Another project never sees it.
	other := seedProject(t, s, ctx)
	if n := completionCount(t, s, ctx, other); n != 0 {
		t.Errorf("another project sees %d assignments, want 0", n)
	}
}

func TestSetInstrumentCompletionTxRollback(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	projectID, userID, eventID, instrumentID, recordID := seedCompletionCell(t, s, ctx)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := s.SetInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID, userID); err != nil {
		t.Fatalf("SetInstrumentCompletionTx: %v", err)
	}
	// Rolling back drops the assignment exactly as it drops the audit entry
	// written in the same transaction (REQ-AUD-003).
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n := completionCount(t, s, ctx, projectID); n != 0 {
		t.Fatalf("rolled-back assignment survived: %d row(s)", n)
	}
}

func TestClearInstrumentCompletionTx(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	projectID, userID, eventID, instrumentID, recordID := seedCompletionCell(t, s, ctx)
	setCommitted(t, s, ctx, projectID, recordID, eventID, instrumentID, userID)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	cleared, err := s.ClearInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID)
	if err != nil {
		t.Fatalf("ClearInstrumentCompletionTx: %v", err)
	}
	if !cleared {
		t.Fatalf("clear reported no row removed")
	}
	// Clearing an unfinished cell is the idempotent no-op (REQ-API-042).
	again, err := s.ClearInstrumentCompletionTx(ctx, tx, projectID, recordID, eventID, instrumentID)
	if err != nil {
		t.Fatalf("ClearInstrumentCompletionTx repeat: %v", err)
	}
	if again {
		t.Fatalf("repeat clear reported a second row removed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	list, err := s.ListInstrumentCompletions(ctx, projectID)
	if err != nil {
		t.Fatalf("ListInstrumentCompletions: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("assignment survived the clear: %+v (absence must mean not finished)", list)
	}
}

// TestInstrumentCompletionCascades covers the REQ-DB-036 cascade: dropping the
// event, the instrument or the whole project takes the assignment with it, so
// no row can outlive the cell it annotates.
func TestInstrumentCompletionCascades(t *testing.T) {
	for _, tc := range []struct {
		name string
		drop func(t *testing.T, s *Store, ctx context.Context, projectID, eventID, instrumentID int64)
	}{
		{"event", func(t *testing.T, s *Store, ctx context.Context, _, eventID, _ int64) {
			if err := s.DeleteEvent(ctx, eventID); err != nil {
				t.Fatalf("DeleteEvent: %v", err)
			}
		}},
		{"instrument", func(t *testing.T, s *Store, ctx context.Context, _, _, instrumentID int64) {
			if err := s.DeleteInstrument(ctx, instrumentID); err != nil {
				t.Fatalf("DeleteInstrument: %v", err)
			}
		}},
		{"project", func(t *testing.T, s *Store, ctx context.Context, projectID, _, _ int64) {
			if _, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID); err != nil {
				t.Fatalf("delete project: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := migrateTestStore(t)
			ctx := context.Background()
			projectID, userID, eventID, instrumentID, recordID := seedCompletionCell(t, s, ctx)
			setCommitted(t, s, ctx, projectID, recordID, eventID, instrumentID, userID)

			tc.drop(t, s, ctx, projectID, eventID, instrumentID)

			if n := completionCount(t, s, ctx, projectID); n != 0 {
				t.Fatalf("%d assignment(s) survived dropping the %s", n, tc.name)
			}
		})
	}
}
