package admin

// Tests for GET /api/v1/audit/export (§4.23, REQ-API-158, REQ-AUD-031/032):
// the admin gate, filter honoring, pagination independence (every matching
// entry in one file), the ZIP+CSV shape read back with zip.NewReader +
// encoding/csv, the estimate mode, and the audit_export entry each export
// owes.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"csms/api/internal/audit"
)

// probeType is the event type these tests seed — narrow enough that fixture
// entries (project_created, login_success, …) never join an export.
const probeType = "export_probe"

var cdDispositionRE = regexp.MustCompile(`^attachment; filename="audit_events_[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{4}\.zip"$`)

// mustProbes seeds n audit_events entries of probeType for one project and
// returns their ids in insertion order (== chronological).
func mustProbes(t *testing.T, e *env, projectID int64, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		if err := e.Audit.Insert(ctx, audit.Entry{
			EventType: probeType, Source: audit.SourceUI,
			Email: "probe@example.org", ProjectID: projectID,
			Details: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("probe insert: %v", err)
		}
		var id int64
		if err := e.Store.DB.QueryRow(
			`SELECT MAX(id) FROM audit_events WHERE event_type = ?`, probeType).Scan(&id); err != nil {
			t.Fatalf("probe id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// exportCSV parses the streamed ZIP and returns (header, data rows) of its
// single CSV member.
func exportCSV(t *testing.T, body []byte, member string) ([]string, [][]string) {
	t.Helper()
	files := exportZip(t, body)
	if len(files) != 1 {
		t.Fatalf("zip members: %v", memberNames(files))
	}
	raw, ok := files[member]
	if !ok {
		t.Fatalf("member %q missing, got %v", member, memberNames(files))
	}
	recs, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(recs) < 1 {
		t.Fatal("CSV has no header")
	}
	return recs[0], recs[1:]
}

func memberNames(files map[string]string) []string {
	var out []string
	for name := range files {
		out = append(out, name)
	}
	return out
}

// auditExportEntries returns every audit_export details payload, oldest first,
// each with its source under "_source".
func auditExportEntries(t *testing.T, e *env) []map[string]any {
	t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT source, details FROM audit_events WHERE event_type = ? ORDER BY id`, audit.AuditExport)
	if err != nil {
		t.Fatalf("query audit_export: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var source string
		var raw *string
		if err := rows.Scan(&source, &raw); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if raw != nil && *raw != "" {
			if err := json.Unmarshal([]byte(*raw), &m); err != nil {
				t.Fatalf("audit_export details %q: %v", *raw, err)
			}
		}
		m["_source"] = source
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAuditExportRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	member := e.mustUser("member@example.org")
	e.mustMemberWithRole(p1, member, 0)

	for _, path := range []string{
		"/api/v1/audit/export",
		"/api/v1/audit/export?project=" + itoa(p1),
		"/api/v1/audit/export?estimate=1",
	} {
		rec := e.do("GET", path, nil, member)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as member: got %d, want 403", path, rec.Code)
		}
	}

	// Filters validate before anything streams (shared with the listing).
	rec := e.do("GET", "/api/v1/audit/export?project=abc", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad project: got %d, want 400", rec.Code)
	}
	rec = e.do("GET", "/api/v1/audit/export?type=bogus", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type: got %d, want 400", rec.Code)
	}
	rec = e.do("GET", "/api/v1/audit/export?to=notadate", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad to: got %d, want 400", rec.Code)
	}
}

func TestAuditExportCSV(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	p2 := e.mustProject("Study Two")
	ids1 := mustProbes(t, e, p1, 2)
	ids2 := mustProbes(t, e, p2, 1)

	rec := e.do("GET", "/api/v1/audit/export?event_type="+probeType, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: got %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type: %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !cdDispositionRE.MatchString(cd) {
		t.Errorf("Content-Disposition: %q", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control: %q", cc)
	}

	header, data := exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	want := []string{"id", "created_at", "source", "user_id", "email", "token",
		"project_id", "arm_num", "role", "target_record", "event_type", "details"}
	if strings.Join(header, ",") != strings.Join(want, ",") {
		t.Errorf("header: %v", header)
	}
	if len(data) != 3 {
		t.Fatalf("rows: %d, want 3 (every match, one file)", len(data))
	}
	// Chronological (an archive reads front to back), id column first.
	got := []string{data[0][0], data[1][0], data[2][0]}
	wantOrder := []string{itoa(ids1[0]), itoa(ids1[1]), itoa(ids2[0])}
	if strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("row order: %v, want chronological %v", got, wantOrder)
	}
	for _, row := range data {
		if row[10] != probeType {
			t.Errorf("event_type cell: %q", row[10])
		}
		if !strings.HasPrefix(row[11], "{") {
			t.Errorf("details cell: %q", row[11])
		}
	}

	// The filter applies to the export exactly as to the listing.
	rec = e.do("GET", "/api/v1/audit/export?event_type="+probeType+"&project="+itoa(p2), nil, admin)
	_, data = exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	if len(data) != 1 || data[0][0] != itoa(ids2[0]) {
		t.Fatalf("project filter: %v", data)
	}

	// from/to bound the stream inclusively (UTC dates).
	today := time.Now().UTC().Format("2006-01-02")
	rec = e.do("GET", "/api/v1/audit/export?event_type="+probeType+"&from="+today, nil, admin)
	_, data = exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	if len(data) != 3 {
		t.Errorf("from=today: %d rows, want 3", len(data))
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	rec = e.do("GET", "/api/v1/audit/export?event_type="+probeType+"&from="+tomorrow, nil, admin)
	_, data = exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	if len(data) != 0 {
		t.Errorf("future from: %d rows, want 0", len(data))
	}
}

func TestAuditExportNeutralizesFormulas(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	if err := e.Audit.Insert(context.Background(), audit.Entry{
		EventType: probeType, Source: audit.SourceUI,
		Email: "probe@example.org", ProjectID: p1, TargetRecord: "=SUM(A1)",
		Details: map[string]any{},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rec := e.do("GET", "/api/v1/audit/export?event_type="+probeType, nil, admin)
	_, data := exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	if len(data) != 1 {
		t.Fatalf("rows: %d", len(data))
	}
	if cell := data[0][9]; cell != "'=SUM(A1)" { // REQ-VAL-032
		t.Errorf("target_record cell: %q, want formula-neutralized", cell)
	}
}

// A trail longer than the listing page must still arrive complete in one ZIP
// — pagination never bounds an export (REQ-AUD-031).
func TestAuditExportIgnoresPagination(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	ids := mustProbes(t, e, p1, 60)

	var page struct {
		Entries    []struct{ ID int64 `json:"id"` } `json:"entries"`
		NextCursor *string                          `json:"next_cursor"`
	}
	rec := e.do("GET", "/api/v1/audit?event_type="+probeType, nil, admin)
	e.decode(rec, &page)
	if len(page.Entries) != 50 || page.NextCursor == nil {
		t.Fatalf("listing should page at its default limit: %d entries, cursor %v",
			len(page.Entries), page.NextCursor != nil)
	}

	rec = e.do("GET", "/api/v1/audit/export?event_type="+probeType, nil, admin)
	_, data := exportCSV(t, rec.Body.Bytes(), "audit_events.csv")
	if len(data) != 60 {
		t.Fatalf("export rows: %d, want all 60 regardless of pagination", len(data))
	}
	if data[0][0] != itoa(ids[0]) || data[59][0] != itoa(ids[59]) {
		t.Errorf("boundary rows: %v … %v", data[0][0], data[59][0])
	}
}

func TestAuditExportEstimate(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	p2 := e.mustProject("Study Two")
	mustProbes(t, e, p1, 3)
	mustProbes(t, e, p2, 1)

	rec := e.do("GET", "/api/v1/audit/export?estimate=1&event_type="+probeType, nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("estimate: got %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("estimate Content-Type: %q", ct)
	}
	var est struct {
		Rows           int64 `json:"rows"`
		EstimatedBytes int64 `json:"estimated_bytes"`
	}
	e.decode(rec, &est)
	if est.Rows != 4 {
		t.Fatalf("estimate rows: %d, want 4", est.Rows)
	}
	// The estimate is the exact count times a sampled average row size; it
	// must be a plausible byte figure, not zero and not orders off.
	if est.EstimatedBytes < est.Rows*20 || est.EstimatedBytes > est.Rows*100000 {
		t.Errorf("estimated_bytes: %d for %d rows", est.EstimatedBytes, est.Rows)
	}

	rec = e.do("GET", "/api/v1/audit/export?estimate=1&event_type="+probeType+"&project="+itoa(p2), nil, admin)
	e.decode(rec, &est)
	if est.Rows != 1 {
		t.Errorf("filtered estimate rows: %d, want 1", est.Rows)
	}

	rec = e.do("GET", "/api/v1/audit/export?estimate=1&event_type=no_such_event", nil, admin)
	e.decode(rec, &est)
	if est.Rows != 0 || est.EstimatedBytes != 0 {
		t.Errorf("empty estimate: %+v", est)
	}
}

func TestAuditExportViews(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Views Study")
	for _, rids := range [][]string{{"R1", "R2"}, {"R3"}} {
		tx, err := e.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := e.Audit.InsertViewTx(ctx, tx, audit.RecordView{
			UserID: admin.ID, Email: admin.Email, Token: "pull-token", ProjectID: p1,
			RecordIDs: rids, Instruments: []string{"intake"},
		}); err != nil {
			t.Fatalf("InsertViewTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	rec := e.do("GET", "/api/v1/audit/export?type=views", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("views export: got %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="audit_views_`) {
		t.Errorf("Content-Disposition: %q", cd)
	}
	header, data := exportCSV(t, rec.Body.Bytes(), "audit_record_views.csv")
	want := []string{"id", "created_at", "user_id", "email", "token", "project_id", "record_ids", "instruments"}
	if strings.Join(header, ",") != strings.Join(want, ",") {
		t.Errorf("views header: %v", header)
	}
	if len(data) != 2 || data[0][6] != `["R1","R2"]` || data[1][6] != `["R3"]` {
		t.Fatalf("views rows: %v", data)
	}

	// event_type is events-only; the views estimate counts all view rows.
	rec = e.do("GET", "/api/v1/audit/export?type=views&estimate=1", nil, admin)
	var est struct {
		Rows int64 `json:"rows"`
	}
	e.decode(rec, &est)
	if est.Rows != 2 {
		t.Errorf("views estimate: %d, want 2", est.Rows)
	}
}

// Every export that ends writes one audit_export entry naming the filters and
// the rows delivered — a read of the trail itself, no values (§3.5).
func TestAuditExportWritesAuditEntry(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	p1 := e.mustProject("Study One")
	mustProbes(t, e, p1, 2)

	rec := e.do("GET", "/api/v1/audit/export?event_type="+probeType+"&project="+itoa(p1), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: got %d", rec.Code)
	}

	entries := auditExportEntries(t, e)
	if len(entries) != 1 {
		t.Fatalf("audit_export entries: %d, want 1", len(entries))
	}
	d := entries[0]
	if d["_source"] != string(audit.SourceUI) {
		t.Errorf("source: %v", d["_source"])
	}
	if d["type"] != "events" {
		t.Errorf("type: %v", d["type"])
	}
	if d["rows"] != float64(2) {
		t.Errorf("rows: %v, want 2 delivered", d["rows"])
	}
	if _, present := d["truncated"]; present {
		t.Error("truncated present on a complete export")
	}
	filters, _ := d["filters"].(map[string]any)
	if filters == nil || filters["project"] != float64(p1) {
		t.Errorf("filters: %v", d["filters"])
	}
	if filters["event_type"] != probeType {
		t.Errorf("filters.event_type: %v", filters["event_type"])
	}
	for _, k := range []string{"user", "from", "to"} {
		if filters[k] != nil {
			t.Errorf("filters.%s: %v, want null", k, filters[k])
		}
	}

	// The estimate mode is a read of counts only — it owes no export entry.
	e.do("GET", "/api/v1/audit/export?estimate=1", nil, admin)
	if got := auditExportEntries(t, e); len(got) != 1 {
		t.Errorf("estimate wrote an audit_export entry: %d entries", len(got))
	}
}
