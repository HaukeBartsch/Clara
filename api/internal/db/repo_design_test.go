package db

import (
	"context"
	"database/sql"
	"testing"
)

// These tests exercise the read paths whose scans were previously lossy
// (bool flags scanned into a throwaway int, and DATETIME columns scanned
// straight into string). They fail if any of those regress.

// mustNullInt64 wraps a non-zero id as a valid sql.NullInt64 for the tests.
func mustNullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		panic("mustNullInt64: zero id")
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func TestSurveyLinkRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	// survey_links.instrument_id references instruments(id): create one first.
	insID, err := s.AddInstrument(ctx, &Instrument{ProjectID: pid, Name: "pr2mask"})
	if err != nil {
		t.Fatalf("AddInstrument: %v", err)
	}

	// A survey link for (project, record, instrument) that is already revoked.
	link := &SurveyLink{ProjectID: pid, RecordID: "REC-01", InstrumentID: insID, Revoked: true}
	if _, err := s.CreateSurveyLink(ctx, link); err != nil {
		t.Fatalf("CreateSurveyLink: %v", err)
	}
	if link.Token == "" {
		t.Fatalf("CreateSurveyLink: empty token")
	}

	got, err := s.GetSurveyLinkByToken(ctx, link.Token)
	if err != nil {
		t.Fatalf("GetSurveyLinkByToken: %v", err)
	}
	if got == nil {
		t.Fatalf("GetSurveyLinkByToken: nil")
	}
	if !got.Revoked {
		t.Errorf("Revoked = false, want true (bool was dropped on scan)")
	}
	if got.CreatedAt == "" {
		t.Errorf("CreatedAt = empty, want a normalized timestamp")
	}

	byTuple, err := s.GetSurveyLink(ctx, pid, "REC-01", insID)
	if err != nil {
		t.Fatalf("GetSurveyLink: %v", err)
	}
	if byTuple == nil || !byTuple.Revoked {
		t.Errorf("GetSurveyLink revoked = %+v, want Revoked=true", byTuple)
	}

	list, err := s.ListSurveyLinks(ctx, pid)
	if err != nil {
		t.Fatalf("ListSurveyLinks: %v", err)
	}
	if len(list) != 1 || !list[0].Revoked {
		t.Errorf("ListSurveyLinks = %+v, want one revoked link", list)
	}
}

func TestDagGroupAndMembershipRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)
	uid := seedUser(t, s, ctx, "dag@test.org")

	// A DAG group: created_at must come back non-empty.
	gid, err := s.CreateDAGGroup(ctx, &DagGroup{ProjectID: pid, Name: "dag-a"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	g, err := s.GetDAGGroup(ctx, gid)
	if err != nil {
		t.Fatalf("GetDAGGroup: %v", err)
	}
	if g == nil {
		t.Fatalf("GetDAGGroup: nil")
	}
	if g.CreatedAt == "" {
		t.Errorf("DagGroup.CreatedAt = empty, want a normalized timestamp")
	}

	// An assignment so we can attach a DAG membership.
	aid, err := s.AddAssignment(ctx, &Assignment{UserID: uid, ProjectID: pid})
	if err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}

	// is_active must round-trip (was scanned into a throwaway int before).
	if _, err := s.AddDAGMembership(ctx, &DagMembership{AssignmentID: aid, GroupID: gid, IsActive: true}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}
	m, err := s.GetDAGMembership(ctx, aid, gid)
	if err != nil {
		t.Fatalf("GetDAGMembership: %v", err)
	}
	if m == nil || !m.IsActive {
		t.Errorf("DagMembership.IsActive = %+v, want true", m)
	}

	ml, err := s.ListDAGMembershipsByAssignment(ctx, aid)
	if err != nil {
		t.Fatalf("ListDAGMembershipsByAssignment: %v", err)
	}
	if len(ml) != 1 || !ml[0].IsActive {
		t.Errorf("ListDAGMembershipsByAssignment = %+v, want one active", ml)
	}
}

func TestLanguageEnabledRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	// 'en' is seeded enabled; the flag must survive the scan (it was dropped
	// before).
	got, err := s.GetLanguageByCode(ctx, "en")
	if err != nil {
		t.Fatalf("GetLanguageByCode: %v", err)
	}
	if got == nil {
		t.Fatalf("GetLanguageByCode: nil")
	}
	if !got.Enabled {
		t.Errorf("Language.Enabled = false, want true (bool was dropped on scan)")
	}

	all, err := s.ListLanguages(ctx)
	if err != nil {
		t.Fatalf("ListLanguages: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("ListLanguages = %d rows, want >= 3", len(all))
	}
	for _, l := range all {
		if !l.Enabled {
			t.Errorf("language %q Enabled = false, want true", l.Code)
		}
	}
}

func TestRecordEntityRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	if err := s.CreateRecordEntity(ctx, &RecordEntity{ProjectID: pid, RecordID: "REC-01"}); err != nil {
		t.Fatalf("CreateRecordEntity: %v", err)
	}
	got, err := s.GetRecordEntity(ctx, pid, "REC-01")
	if err != nil {
		t.Fatalf("GetRecordEntity: %v", err)
	}
	if got == nil {
		t.Fatalf("GetRecordEntity: nil")
	}
	if got.CreatedAt == "" {
		t.Errorf("RecordEntity.CreatedAt = empty, want a normalized timestamp")
	}

	// Assign a DAG group and confirm the nullable id round-trips.
	gid, err := s.CreateDAGGroup(ctx, &DagGroup{ProjectID: pid, Name: "re-dag"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	if err := s.SetRecordDAG(ctx, pid, "REC-01", mustNullInt64(gid)); err != nil {
		t.Fatalf("SetRecordDAG: %v", err)
	}
	got, _ = s.GetRecordEntity(ctx, pid, "REC-01")
	if !got.DagGroupID.Valid || got.DagGroupID.Int64 != gid {
		t.Errorf("RecordEntity.DagGroupID = %+v, want %d", got.DagGroupID, gid)
	}
}

func TestDataValuesRoundTrip(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	// ByRecord / ByEvent / ByInstrument all key off the name, not an id.
	dv := &DataValue{
		ProjectID: pid, RecordID: "REC-01", UniqueEventName: "baseline_arm_1",
		RepeatingInstrument: "pr2mask", RepeatingInstanceNumber: 1,
		FieldName: "physical_size", Value: "3.2",
	}
	if err := s.AddDataValue(ctx, dv); err != nil {
		t.Fatalf("AddDataValue: %v", err)
	}

	// Upsert must replace the value on both dialects' syntax.
	dv.Value = "5.0"
	if err := s.UpsertDataValue(ctx, dv); err != nil {
		t.Fatalf("UpsertDataValue: %v", err)
	}

	byRec, err := s.ListDataValuesByRecord(ctx, pid, "REC-01")
	if err != nil {
		t.Fatalf("ListDataValuesByRecord: %v", err)
	}
	if len(byRec) != 1 || byRec[0].Value != "5.0" {
		t.Errorf("ListDataValuesByRecord = %+v, want one value 5.0", byRec)
	}

	byEvt, err := s.ListDataValuesByEvent(ctx, pid, "REC-01", "baseline_arm_1")
	if err != nil {
		t.Fatalf("ListDataValuesByEvent: %v", err)
	}
	if len(byEvt) != 1 {
		t.Errorf("ListDataValuesByEvent = %d rows, want 1", len(byEvt))
	}

	byInst, err := s.ListDataValuesByInstrument(ctx, pid, "REC-01", "pr2mask")
	if err != nil {
		t.Fatalf("ListDataValuesByInstrument: %v", err)
	}
	if len(byInst) != 1 {
		t.Errorf("ListDataValuesByInstrument = %d rows, want 1", len(byInst))
	}

	// Delete by key.
	if err := s.DeleteDataValueByID(ctx, dv); err != nil {
		t.Fatalf("DeleteDataValueByID: %v", err)
	}
	byRec, _ = s.ListDataValuesByRecord(ctx, pid, "REC-01")
	if len(byRec) != 0 {
		t.Errorf("after delete, ListDataValuesByRecord = %d rows, want 0", len(byRec))
	}
}