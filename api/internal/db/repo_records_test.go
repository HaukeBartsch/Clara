package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// MaxRecordID drives the next generated record name (REQ-API-023): the data
// API asks for the greatest id in the project rather than loading every one,
// so the answer must cover both places a record can exist — EAV rows and the
// record entity — and stay inside the project.

func TestMaxRecordIDEmptyProject(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	got, found, err := s.MaxRecordID(ctx, pid)
	if err != nil {
		t.Fatalf("MaxRecordID: %v", err)
	}
	if found || got != "" {
		t.Errorf("MaxRecordID on an empty project = %q,%v, want \"\",false", got, found)
	}
}

// A record with data but no entity row still counts — the EAV table is where
// imported and API-written records land first.
func TestMaxRecordIDFromData(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	for _, id := range []string{"8DISC002", "8DISC001", "8DISC010"} {
		dv := &DataValue{ProjectID: pid, RecordID: id, FieldName: "record_id", Value: id}
		if err := s.AddDataValue(ctx, dv); err != nil {
			t.Fatalf("AddDataValue %s: %v", id, err)
		}
	}

	got, found, err := s.MaxRecordID(ctx, pid)
	if err != nil {
		t.Fatalf("MaxRecordID: %v", err)
	}
	// Record ids are TEXT, so the comparison is lexicographic — which is why
	// the generated-name patterns are zero-padded (REQ-API-023).
	if !found || got != "8DISC010" {
		t.Errorf("MaxRecordID = %q,%v, want 8DISC010,true", got, found)
	}
}

// A record entity with no values yet (created, then abandoned before data
// entry) must still reserve its id.
func TestMaxRecordIDFromRecordEntity(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: "8DISC042"}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}

	got, found, err := s.MaxRecordID(ctx, pid)
	if err != nil {
		t.Fatalf("MaxRecordID: %v", err)
	}
	if !found || got != "8DISC042" {
		t.Errorf("MaxRecordID = %q,%v, want 8DISC042,true", got, found)
	}
}

// The answer is scoped to the project (REQ-DB-001): a neighbouring project with
// a greater id must not push generation forward here.
func TestMaxRecordIDIsScopedToProject(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	small := seedProject(t, s, ctx)
	large := seedProject(t, s, ctx)

	if err := s.AddDataValue(ctx, &DataValue{
		ProjectID: small, RecordID: "R001", FieldName: "record_id", Value: "R001",
	}); err != nil {
		t.Fatalf("AddDataValue small: %v", err)
	}
	if err := s.AddDataValue(ctx, &DataValue{
		ProjectID: large, RecordID: "Z999", FieldName: "record_id", Value: "Z999",
	}); err != nil {
		t.Fatalf("AddDataValue large: %v", err)
	}

	got, _, err := s.MaxRecordID(ctx, small)
	if err != nil {
		t.Fatalf("MaxRecordID small: %v", err)
	}
	if got != "R001" {
		t.Errorf("MaxRecordID(small) = %q, want R001 (other projects must not leak in)", got)
	}
}

// MaxNumericRecordID drives the auto naming mode's next id (REQ-DB-045,
// REQ-API-151): only all-digit ids count and they compare numerically, so a
// wide numeric id wins over a lexicographically greater non-digit one.
func TestMaxNumericRecordID(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	for _, id := range []string{"8DISC002", "9", "42", "09"} {
		if err := s.AddDataValue(ctx, &DataValue{ProjectID: pid, RecordID: id, FieldName: "record_id", Value: id}); err != nil {
			t.Fatalf("AddDataValue %s: %v", id, err)
		}
	}
	got, err := s.MaxNumericRecordID(ctx, pid)
	if err != nil {
		t.Fatalf("MaxNumericRecordID: %v", err)
	}
	// 42 > 9 numerically; "8DISC002" is not all digits and "09" is a form the
	// counter never emits, so neither advances the value.
	if got != 42 {
		t.Errorf("MaxNumericRecordID = %d, want 42", got)
	}
}

// A record belongs to the project, not to an arm: the same record id written
// under two arms' events is one record (uniqueness per project, REQ-DB-029 —
// the primary key carries no arm).
func TestRecordIDsAreUniqueAcrossArms(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	arm1, err := s.AddArm(ctx, &Arm{ProjectID: pid, ArmNum: 1})
	if err != nil {
		t.Fatalf("AddArm 1: %v", err)
	}
	arm2, err := s.AddArm(ctx, &Arm{ProjectID: pid, ArmNum: 2})
	if err != nil {
		t.Fatalf("AddArm 2: %v", err)
	}
	for _, e := range []*Event{
		{ProjectID: pid, ArmID: arm1, EventName: "screening", UniqueEventName: "screening_arm_1"},
		{ProjectID: pid, ArmID: arm2, EventName: "screening", UniqueEventName: "screening_arm_2"},
	} {
		if _, err := s.AddEvent(ctx, e); err != nil {
			t.Fatalf("AddEvent %s: %v", e.UniqueEventName, err)
		}
	}
	for _, ev := range []string{"screening_arm_1", "screening_arm_2"} {
		if err := s.AddDataValue(ctx, &DataValue{ProjectID: pid, RecordID: "7", UniqueEventName: ev, FieldName: "record_id", Value: "7"}); err != nil {
			t.Fatalf("AddDataValue %s: %v", ev, err)
		}
	}

	ids, err := s.ListRecordIDs(ctx, pid, nil)
	if err != nil {
		t.Fatalf("ListRecordIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "7" {
		t.Errorf("ListRecordIDs = %v, want [7] — one record across both arms", ids)
	}

	// The identity table enforces the same guarantee at row level: one entity
	// per (project, record), whichever arm touches it first.
	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: "7"}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: "7"}); err == nil {
		t.Errorf("second CreateRecordEntity for the same record id succeeded, want a primary key violation")
	}
}

// TestForEachValueOfFields: the narrow read of the completeness count
// (REQ-API-171) returns the non-empty values of exactly the named columns,
// across records, and reads a name list longer than one IN chunk in several
// queries without losing any of it.
func TestForEachValueOfFields(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	projectID := seedProject(t, s, ctx)
	for _, r := range []string{"R1", "R2"} {
		if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: projectID, RecordID: r}); err != nil {
			t.Fatalf("CreateRecordEntity: %v", err)
		}
	}
	put := func(record, field, value string) {
		t.Helper()
		if err := s.AddDataValue(ctx, &DataValue{ProjectID: projectID, RecordID: record,
			UniqueEventName: "v1_arm_1", FieldName: field, Value: value}); err != nil {
			t.Fatalf("AddDataValue: %v", err)
		}
	}
	// Names in the first, second and third chunk, an empty value, and a
	// column nobody asked for.
	put("R1", "f0001", "a")
	put("R2", "f0700", "b")
	put("R1", "f1199", "c")
	put("R1", "f0002", "")
	put("R1", "other", "d")

	names := make([]string, 1200)
	for i := range names {
		names[i] = fmt.Sprintf("f%04d", i)
	}
	var got []string
	if err := s.ForEachValueOfFields(ctx, projectID, names, func(dv DataValue) error {
		got = append(got, dv.RecordID+"|"+dv.FieldName+"|"+dv.Value)
		return nil
	}); err != nil {
		t.Fatalf("ForEachValueOfFields: %v", err)
	}
	slices.Sort(got)
	if want := []string{"R1|f0001|a", "R1|f1199|c", "R2|f0700|b"}; !slices.Equal(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}

	// No names, no query and no values.
	calls := 0
	if err := s.ForEachValueOfFields(ctx, projectID, nil, func(DataValue) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("empty name list: %d calls, err %v", calls, err)
	}
}
