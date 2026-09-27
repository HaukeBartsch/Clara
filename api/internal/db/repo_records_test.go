package db

import (
	"context"
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
