package dataapi

// Replays of the recorded transport logs of the system being replaced
// (assets/transfer_samples_input_output/, DEV-API-24). These callers send
// imports as plain content=record with a JSON-array `data` parameter and no
// action, and parse {"count": N} — read as action-dispatched they would have
// been served an export instead of importing.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
	"csms/api/internal/testdb"
)

// newClassicFixture seeds a project without events — the shape of the
// updateREDCap log's target: one instrument of plain text fields plus an
// integer field, and one admin token.
func newClassicFixture(t *testing.T, fields ...db.Field) *recordFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		AppEnv:           "development",
		DBConnection:     "sqlite",
		DBDatabase:       filepath.Join(dir, "test.sqlite"),
		AnonSalt:         "test-salt",
		AnonDateShiftMin: 1,
		AnonDateShiftMax: 365,
		AppTimezone:      "UTC",
	}
	testdb.Use(t, cfg)
	s, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	pid, err := s.CreateProject(ctx, &db.Project{ProjectName: "classic-" + t.Name()})
	must(err)
	instrID, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: "dicom", Position: 1})
	must(err)
	for i, f := range fields {
		f.ProjectID, f.InstrumentID, f.Position = pid, instrID, i+1
		if _, err := s.AddField(ctx, &f); err != nil {
			t.Fatalf("seed AddField %s: %v", f.FieldName, err)
		}
	}
	uid, err := s.CreateUser(ctx, &db.User{Email: "admin@example.org", DisplayName: "admin",
		Enabled: true, AuthSource: "local", IsAdmin: true})
	must(err)
	_, err = s.AddAssignment(ctx, &db.Assignment{UserID: uid, ProjectID: pid, Token: "tok-admin"})
	must(err)

	aw := audit.NewWriter(s.DB, string(s.Dialect))
	must(aw.EnsureYear(ctx))
	return &recordFixture{h: &Handler{Store: s, Cfg: cfg, Audit: aw}, s: s, cfg: cfg, pid: pid}
}

// TestTransportLogUpdateREDCapReplay replays the updateREDCap call — the
// field set, order and encodings verbatim (three records instead of 86) —
// against a project without events: it imports and answers {"count": N}
// (REQ-API-012, REQ-API-031, REQ-API-142).
func TestTransportLogUpdateREDCapReplay(t *testing.T) {
	f := newClassicFixture(t,
		db.Field{FieldName: "record_id", FieldType: "text"},
		db.Field{FieldName: "ids7_patient_name", FieldType: "text"},
		db.Field{FieldName: "ids7_number_of_series", FieldType: "text",
			ValidationType: sql.NullString{String: "integer", Valid: true}},
		db.Field{FieldName: "ids7_study_date", FieldType: "text"},
	)

	data := `[{"record_id": "1.3.6.1.4.1.45037.411364340918945153977062676307243216048197020",` +
		` "ids7_patient_name": "ENDO_MONT_049", "ids7_number_of_series": 18, "ids7_study_date": "20221202"},` +
		`{"record_id": "1.3.6.1.4.1.45037.812211684545058520454967140210000694401102621",` +
		` "ids7_patient_name": "ENDO_MONT_006", "ids7_number_of_series": 18, "ids7_study_date": "20231121"},` +
		`{"record_id": "1.3.6.1.4.1.45037.891082805190481452434114712403623544814245436",` +
		` "ids7_patient_name": "ENDO_MONT_081", "ids7_number_of_series": 64, "ids7_study_date": "20220607"}]`

	code, body := f.call(t, map[string][]string{
		"token":             {"tok-admin"},
		"content":           {"record"},
		"format":            {"json"},
		"type":              {"flat"},
		"overwriteBehavior": {"overwrite"},
		"forceAutoNumber":   {"false"},
		"returnContent":     {"count"},
		"returnFormat":      {"json"},
		"data":              {data},
	})
	mustStatus(t, code, 200, body)
	if strings.TrimSpace(body) != `{"count": 3}` && strings.TrimSpace(body) != `{"count":3}` {
		t.Fatalf("response = %s, want the count of the three imported records", body)
	}

	// The values are stored, numbers coerced to their literal spelling.
	if got := storedValue(t, f, "1.3.6.1.4.1.45037.411364340918945153977062676307243216048197020",
		"", "ids7_patient_name"); got != "ENDO_MONT_049" {
		t.Errorf("stored ids7_patient_name = %q", got)
	}
	if got := storedValue(t, f, "1.3.6.1.4.1.45037.411364340918945153977062676307243216048197020",
		"", "ids7_number_of_series"); got != "18" {
		t.Errorf("stored ids7_number_of_series = %q, want the JSON number coerced to \"18\"", got)
	}

	// Replaying the same call overwrites (the upsert of REQ-API-033 that
	// overwriteBehavior=overwrite asks for) and counts the rows as updates.
	code, body = f.call(t, map[string][]string{
		"token": {"tok-admin"}, "content": {"record"}, "format": {"json"}, "type": {"flat"},
		"returnContent": {"count"}, "returnFormat": {"json"}, "data": {data},
	})
	mustStatus(t, code, 200, body)
	if !strings.Contains(body, "3") {
		t.Fatalf("replay response = %s, want the count of three updated records", body)
	}
}
