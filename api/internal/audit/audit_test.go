package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// openStore returns a migrated sqlite store; the plain migration-created
// audit tables are still in place (rollover has not run yet).
func openStore(t *testing.T) *db.Store {
	t.Helper()
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "audit-test.sqlite"),
	}
	s, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func masterKind(t *testing.T, s *db.Store, name string) string {
	t.Helper()
	var kind string
	err := s.DB.QueryRow(`SELECT type FROM sqlite_master WHERE name = ?`, name).Scan(&kind)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("sqlite_master %s: %v", name, err)
	}
	return kind
}

func TestEnsureYearRolloverSQLite(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	year := time.Now().UTC().Year()

	if masterKind(t, s, "audit_events") != "table" {
		t.Fatalf("expected plain audit_events table before rollover")
	}
	// A row stored in the plain table must survive the rename.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_events (event_type, source, created_at) VALUES ('login_success','ui',?)`,
		time.Now().UTC().Format(datetimeLayout)); err != nil {
		t.Fatalf("seed plain table: %v", err)
	}

	w := NewWriter(s.DB, "sqlite")
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	phys := physical("audit_events", year)
	if masterKind(t, s, phys) != "table" {
		t.Fatalf("expected physical table %s after rollover", phys)
	}
	if masterKind(t, s, "audit_events") != "view" {
		t.Fatalf("expected stable name audit_events to be a view after rollover")
	}

	var survived int
	if err := s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE event_type='login_success'`).Scan(&survived); err != nil {
		t.Fatalf("read through view: %v", err)
	}
	if survived != 1 {
		t.Fatalf("pre-rollover row lost: %d rows visible through the view", survived)
	}

	// Idempotent: a second call is a latch hit and must not disturb anything.
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear (second call): %v", err)
	}
	if masterKind(t, s, "audit_events") != "view" {
		t.Fatalf("stable name changed on the second EnsureYear")
	}
}

func TestInsertThroughStableName(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w := NewWriter(s.DB, "sqlite")
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}

	err := w.Insert(ctx, Entry{
		EventType:    RecordUpdated,
		Source:       SourceAPI,
		UserID:       7,
		Email:        "researcher@example.org",
		Token:        "tok-123",
		ProjectID:    42,
		ArmNum:       2,
		Role:         "data_manager",
		TargetRecord: "8DISC001",
		Details:      map[string]any{"instrument": "demographics"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var (
		eventType, source, email, token, role, target, details, createdAt string
		userID, projectID                                                 sql.NullInt64
		armNum                                                            sql.NullInt64
	)
	err = s.DB.QueryRowContext(ctx,
		`SELECT event_type, source, user_id, email, token, project_id, arm_num, role,
		        target_record, details, created_at
		 FROM audit_events ORDER BY id DESC LIMIT 1`).
		Scan(&eventType, &source, &userID, &email, &token, &projectID, &armNum,
			&role, &target, &details, &createdAt)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if eventType != RecordUpdated || source != SourceAPI {
		t.Errorf("event_type/source = %q/%q", eventType, source)
	}
	if userID.Int64 != 7 || projectID.Int64 != 42 || armNum.Int64 != 2 {
		t.Errorf("fixed id columns = %v/%v/%v", userID, projectID, armNum)
	}
	if email != "researcher@example.org" || token != "tok-123" || role != "data_manager" || target != "8DISC001" {
		t.Errorf("actor/target columns = %q/%q/%q/%q", email, token, role, target)
	}
	var dm map[string]any
	if err := json.Unmarshal([]byte(details), &dm); err != nil {
		t.Fatalf("details not JSON: %v", err)
	}
	if dm["instrument"] != "demographics" {
		t.Errorf("details = %v", dm)
	}
	if _, err := time.Parse(datetimeLayout, createdAt); err != nil {
		t.Errorf("created_at %q not in canonical layout: %v", createdAt, err)
	}

	// Unset optional columns render as NULL (0 / "" mean unset).
	if err := w.Insert(ctx, Entry{EventType: Logout, Source: SourceUI}); err != nil {
		t.Fatalf("Insert minimal: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_events WHERE event_type='logout'
		 AND user_id IS NULL AND project_id IS NULL AND arm_num IS NULL
		 AND email IS NULL AND token IS NULL AND details IS NULL`).Scan(&n); err != nil {
		t.Fatalf("minimal row check: %v", err)
	}
	if n != 1 {
		t.Fatalf("minimal entry did not render unset columns as NULL")
	}
}

func TestInsertTxRollback(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w := NewWriter(s.DB, "sqlite")
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := w.InsertTx(ctx, tx, Entry{EventType: ProjectCreated, Source: SourceUI, UserID: 1}); err != nil {
		tx.Rollback()
		t.Fatalf("InsertTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_events WHERE event_type='project_created'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("rolled-back operation left its audit entry behind (REQ-AUD-003)")
	}
}

func TestInsertViewTx(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w := NewWriter(s.DB, "sqlite")
	if err := w.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	err = w.InsertViewTx(ctx, tx, RecordView{
		UserID:      3,
		Email:       "reader@example.org",
		Token:       "tok-view",
		ProjectID:   9,
		RecordIDs:   []string{"8DISC001", "8DISC002"},
		Instruments: []string{"demographics"},
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("InsertViewTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var (
		rids, instrs string
		projectID    int64
		token        string
	)
	err = s.DB.QueryRowContext(ctx,
		`SELECT token, project_id, record_ids, instruments FROM audit_record_views ORDER BY id DESC LIMIT 1`).
		Scan(&token, &projectID, &rids, &instrs)
	if err != nil {
		t.Fatalf("read view row: %v", err)
	}
	if token != "tok-view" || projectID != 9 {
		t.Errorf("token/project = %q/%d", token, projectID)
	}
	var got []string
	if err := json.Unmarshal([]byte(rids), &got); err != nil || len(got) != 2 || got[0] != "8DISC001" {
		t.Errorf("record_ids = %q (parsed %v, err %v)", rids, got, err)
	}
	if err := json.Unmarshal([]byte(instrs), &got); err != nil || len(got) != 1 || got[0] != "demographics" {
		t.Errorf("instruments = %q (parsed %v, err %v)", instrs, got, err)
	}
}
