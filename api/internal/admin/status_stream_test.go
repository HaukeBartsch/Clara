package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// --- §4.13 Streaming: the dashboard's reads chunk by chunk (REQ-API-172) ---

// getAccept is e.do for a GET that names what it accepts.
func (e *env) getAccept(path, accept string, actor *db.User) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req = req.WithContext(authz.WithActor(req.Context(), actor))
	rec := httptest.NewRecorder()
	e.Handler.Handler.ServeHTTP(rec, req)
	return rec
}

// withStatusChunk sets the chunk size for the rest of the test.
func withStatusChunk(t *testing.T, n int) {
	t.Helper()
	before := statusChunk
	statusChunk = n
	t.Cleanup(func() { statusChunk = before })
}

func TestAcceptsNDJSON(t *testing.T) {
	for accept, want := range map[string]bool{
		"":                                       false,
		"*/*":                                    false,
		"application/json":                       false,
		"application/x-ndjsonx":                  false,
		"application/x-ndjson":                   true,
		"Application/X-NDJSON":                   true,
		"application/json, application/x-ndjson": true,
		"application/x-ndjson; q=0.5":            true,
		"application/x-ndjson;q=0":               false,
		"application/x-ndjson;q=abc":             false,
	} {
		if got := acceptsNDJSON(accept); got != want {
			t.Errorf("acceptsNDJSON(%q) = %v, want %v", accept, got, want)
		}
	}
}

// TestDashboardReadsStreamInChunks: read in chunks of two records, both reads
// answer byte for byte what one chunk answers — the marks of a completion, of
// a further instance and of branching on a record's own values land in the
// right row whichever chunk it falls in. The NDJSON answer is the same rows, a
// line each, and the end line with their count.
func TestDashboardReadsStreamInChunks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Streamed Dashboard")
	armID := e.mustArm(projectID, 1)
	v1 := e.mustEvent(projectID, armID, "Visit 1", "v1_arm_1")
	v2 := e.mustEvent(projectID, armID, "Visit 2", "v2_arm_1")
	intake := e.mustInstrument(projectID, "intake")
	visits := e.mustInstrument(projectID, "visits")
	field := func(instrument int64, pos int, name, typ, choices, branching string) {
		f := db.Field{ProjectID: projectID, InstrumentID: instrument, FieldName: name, FieldType: typ, Position: pos}
		if choices != "" {
			f.Choices = logic(choices)
		}
		if branching != "" {
			f.BranchingLogic = logic(branching)
		}
		e.mustDesignField(f)
	}
	field(intake, 1, "record_id", "text", "", "")
	field(intake, 2, "sex", "radio", "1$Male##2$Female", "")
	field(intake, 3, "age", "text", "", `[sex] = '2'`)
	field(visits, 1, "kind", "radio", "1$Phone##2$Clinic", "")
	field(visits, 2, "room", "text", "", `[kind] = '2'`)
	e.mustMap(projectID, armID,
		db.InstrumentEvent{InstrumentID: intake, EventID: v1},
		db.InstrumentEvent{InstrumentID: visits, EventID: v1},
		db.InstrumentEvent{InstrumentID: intake, EventID: v2},
	)
	if rec := e.do("PUT", repeatingURL(projectID, visits), map[string]any{"events": []string{"v1_arm_1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("designate: %d %s", rec.Code, rec.Body.String())
	}
	for _, r := range []string{"R1", "R2", "R3", "R4", "R5"} {
		e.mustRecord(projectID, r)
	}
	e.mustValue(projectID, "R1", "v1_arm_1", "", "sex", "1")
	e.mustValue(projectID, "R3", "v1_arm_1", "", "sex", "2")
	e.mustValue(projectID, "R3", "v1_arm_1", "", "age", "30")
	e.mustValue(projectID, "R3", "v2_arm_1", "", "age", "31")
	e.mustInstanceValue(projectID, "R4", "v1_arm_1", "visits", 1, "kind", "1")
	e.mustInstanceValue(projectID, "R4", "v1_arm_1", "visits", 2, "kind", "2")
	e.mustInstanceValue(projectID, "R4", "v1_arm_1", "visits", 2, "room", "A1")
	// R5 sits alone in the last chunk of two.
	e.mustValue(projectID, "R5", "v1_arm_1", "", "sex", "2")
	e.mustInstanceValue(projectID, "R5", "v1_arm_1", "visits", 3, "kind", "2")
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := e.Store.SetInstrumentCompletionTx(ctx, tx, projectID, "R5", v2, intake, 1, admin.ID); err != nil {
		t.Fatalf("SetInstrumentCompletionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	statusURL := "/api/v1/projects/" + itoa(projectID) + "/record-status"
	for _, path := range []string{statusURL, completenessURL(projectID)} {
		withStatusChunk(t, 1000)
		whole := e.getAccept(path, "", admin)
		if whole.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, whole.Code, whole.Body.String())
		}
		withStatusChunk(t, 2)
		chunked := e.getAccept(path, "application/json", admin)
		if ct := chunked.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q, want application/json", path, ct)
		}
		if chunked.Body.String() != whole.Body.String() {
			t.Fatalf("%s in chunks of 2\n got %s\nwant %s", path, chunked.Body.String(), whole.Body.String())
		}

		var rows []json.RawMessage
		if err := json.Unmarshal(whole.Body.Bytes(), &rows); err != nil || len(rows) != 5 {
			t.Fatalf("%s: %d rows, err %v", path, len(rows), err)
		}
		var want strings.Builder
		for _, row := range rows {
			want.Write(row)
			want.WriteByte('\n')
		}
		want.WriteString(`{"end":true,"records":5}` + "\n")
		lines := e.getAccept(path, "application/x-ndjson", admin)
		if lines.Code != http.StatusOK || lines.Header().Get("Content-Type") != ndjsonType {
			t.Fatalf("%s as NDJSON: %d %q", path, lines.Code, lines.Header().Get("Content-Type"))
		}
		if lines.Body.String() != want.String() {
			t.Fatalf("%s as NDJSON\n got %s\nwant %s", path, lines.Body.String(), want.String())
		}
	}

	// The last chunk carries R5's own marks: its completion, its further
	// instance, and the branching its own values decide.
	var status []recordStatusRow
	if err := json.Unmarshal(e.getAccept(statusURL, "", admin).Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	r5 := asJSON(status[4])
	for _, part := range []string{
		`{"name":"visits","state":"no_data","instances":[{"instance":3,"state":"some_data"}]}`,
		`{"unique_event_name":"v2_arm_1","instruments":[{"name":"intake","state":"finished"}]}`,
	} {
		if !strings.Contains(r5, part) {
			t.Errorf("R5 status %s lacks %s", r5, part)
		}
	}
	r5 = asJSON(e.completenessOf(projectID, admin)[4])
	// sex = 2 shows age (empty); kind = 2 shows room (empty) on instance 3.
	for _, part := range []string{
		`{"name":"intake","filled":1,"visible":2}`,
		`{"instance":3,"filled":1,"visible":2}`,
	} {
		if !strings.Contains(r5, part) {
			t.Errorf("R5 completeness %s lacks %s", r5, part)
		}
	}
}

// TestDashboardReadsStreamEmpty: a project without visible records is the
// empty array, or the end line alone.
func TestDashboardReadsStreamEmpty(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.mustProject("Empty Dashboard")
	for _, path := range []string{"/api/v1/projects/" + itoa(projectID) + "/record-status", completenessURL(projectID)} {
		if got := e.getAccept(path, "", admin).Body.String(); got != "[]\n" {
			t.Errorf("%s = %q, want []", path, got)
		}
		if got := e.getAccept(path, ndjsonType, admin).Body.String(); got != `{"end":true,"records":0}`+"\n" {
			t.Errorf("%s as NDJSON = %q, want the end line alone", path, got)
		}
	}
}

// TestStreamStatusFailure: a store failure on the first chunk is the usual
// 500; one after the first byte ends the answer where it stands — no closing
// bracket, no end line — so a cut stream cannot pass for a whole project.
func TestStreamStatusFailure(t *testing.T) {
	withStatusChunk(t, 2)
	visible := []db.RecordEntity{{RecordID: "A"}, {RecordID: "B"}, {RecordID: "C"}}
	type row struct {
		ID string `json:"id"`
	}
	failing := func(at int) func(context.Context, []db.RecordEntity) ([]row, error) {
		calls := 0
		return func(_ context.Context, chunk []db.RecordEntity) ([]row, error) {
			calls++
			if calls == at {
				return nil, errors.New("store gone")
			}
			out := make([]row, len(chunk))
			for i, re := range chunk {
				out[i] = row{ID: re.RecordID}
			}
			return out, nil
		}
	}
	serve := func(accept string, visible []db.RecordEntity, failAt int) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/stream", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		streamStatus(rec, req, visible, failing(failAt))
		return rec
	}

	for _, tc := range []struct {
		name, accept string
		failAt, code int
		body         string
	}{
		{"whole", "", 0, 200, `[{"id":"A"},{"id":"B"},{"id":"C"}]` + "\n"},
		{"whole ndjson", ndjsonType, 0, 200, "{\"id\":\"A\"}\n{\"id\":\"B\"}\n{\"id\":\"C\"}\n{\"end\":true,\"records\":3}\n"},
		{"cut", "", 2, 200, `[{"id":"A"},{"id":"B"}`},
		{"cut ndjson", ndjsonType, 2, 200, "{\"id\":\"A\"}\n{\"id\":\"B\"}\n"},
	} {
		rec := serve(tc.accept, visible, tc.failAt)
		if rec.Code != tc.code || rec.Body.String() != tc.body {
			t.Errorf("%s: %d %q, want %d %q", tc.name, rec.Code, rec.Body.String(), tc.code, tc.body)
		}
	}
	rec := serve(ndjsonType, visible, 1)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), `"end"`) {
		t.Errorf("first chunk failing: %d %q, want the 500", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct == ndjsonType {
		t.Errorf("first chunk failing answers as %s", ct)
	}
}
