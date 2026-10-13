package db

import (
	"context"
	"testing"

	"csms/api/internal/migrations"
)

// TestMigrateRepeatingInstruments applies 0015 to a store holding the pre-0015
// state (REQ-DB-047/048, DEV-DB-16): values the data API stored at instance 0
// move to instance 1, an instance-1 twin of such a value goes, every pair is
// non-repeating, and a finished assignment becomes one of instance 1.
func TestMigrateRepeatingInstruments(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	files, err := migrations.For(string(s.Dialect))
	if err != nil {
		t.Fatal(err)
	}
	target := -1
	for i, name := range files {
		if name == "0015_repeating_instruments.sql" {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("0015_repeating_instruments.sql is not in the migration list")
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, name := range files[:target] {
		sqlBytes, err := migrations.Read(string(s.Dialect), name)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.applyMigration(ctx, i+1, string(sqlBytes), name); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	pid, err := s.CreateProject(ctx, &Project{ProjectName: "pre-0015"})
	if err != nil {
		t.Fatal(err)
	}
	armID, err := s.AddArm(ctx, &Arm{ProjectID: pid, ArmNum: 1})
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := s.AddEvent(ctx, &Event{ProjectID: pid, ArmID: armID, EventName: "baseline", UniqueEventName: "baseline_arm_1"})
	if err != nil {
		t.Fatal(err)
	}
	instID, err := s.AddInstrument(ctx, &Instrument{ProjectID: pid, Name: "intake", Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO instrument_events (instrument_id, event_id) VALUES (?, ?)`, []any{instID, eventID}},
		// The data API's write: instance 0.
		{`INSERT INTO data (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value)
		  VALUES (?, 'R1', 'baseline_arm_1', '', 0, 'age', '42')`, []any{pid}},
		{`INSERT INTO data (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value)
		  VALUES (?, 'R1', 'baseline_arm_1', '', 0, 'total', '84')`, []any{pid}},
		// The design-time recomputation's stale twin at instance 1.
		{`INSERT INTO data (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value)
		  VALUES (?, 'R1', 'baseline_arm_1', '', 1, 'total', '80')`, []any{pid}},
		// A value only the recomputation wrote stays where it is.
		{`INSERT INTO data (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value)
		  VALUES (?, 'R2', 'baseline_arm_1', '', 1, 'total', '10')`, []any{pid}},
		{`INSERT INTO instrument_completion (project_id, record_id, event_id, instrument_id, completed_by, completed_at)
		  VALUES (?, 'R1', ?, ?, NULL, '2026-10-01 10:00:00')`, []any{pid, eventID, instID}},
	} {
		if _, err := s.DB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	values, err := s.ListDataValuesByRecord(ctx, pid, "R1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, dv := range values {
		if dv.RepeatingInstanceNumber != 1 || dv.RepeatingInstrument != "" {
			t.Errorf("value %s at (%q, %d), want ('', 1)", dv.FieldName, dv.RepeatingInstrument, dv.RepeatingInstanceNumber)
		}
		got[dv.FieldName] = dv.Value
	}
	if len(values) != 2 || got["age"] != "42" || got["total"] != "84" {
		t.Fatalf("R1 after 0015 = %+v, want age 42 and the data API's total 84 only", values)
	}
	if values, err := s.ListDataValuesByRecord(ctx, pid, "R2"); err != nil || len(values) != 1 || values[0].Value != "10" {
		t.Fatalf("R2 after 0015 = %+v (%v)", values, err)
	}

	pairs, err := s.ListInstrumentEvents(ctx, pid)
	if err != nil || len(pairs) != 1 || pairs[0].IsRepeating {
		t.Fatalf("pairs after 0015 = %+v (%v)", pairs, err)
	}
	comps, err := s.ListInstrumentCompletions(ctx, pid)
	if err != nil || len(comps) != 1 || comps[0].Instance != 1 {
		t.Fatalf("completions after 0015 = %+v (%v)", comps, err)
	}
	// A further instance can now be finished beside the first.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetInstrumentCompletionTx(ctx, tx, pid, "R1", eventID, instID, 2, 0); err != nil || !ok {
		t.Fatalf("finish instance 2: %v %v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if comps, _ := s.ListInstrumentCompletions(ctx, pid); len(comps) != 2 {
		t.Fatalf("completions = %+v, want instances 1 and 2", comps)
	}
}
