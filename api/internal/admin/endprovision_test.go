package admin

// End-of-project provision (§4.20, BR-009, Data_Export_Anonymization_Design
// §7): the two branches and their counts, one-shot enforcement through the
// project_ended audit event, is_admin gating, and same-transaction atomicity.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
)

func TestEndProvisionDelete(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")

	rec := f.e.do(http.MethodPost, "/api/v1/projects/"+itoa(f.pid)+"/end-provision",
		map[string]string{"provision": "delete"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var out map[string]any
	f.e.decode(rec, &out)
	if out["provision"] != "delete" || out["records_affected"] != float64(2) || out["values_affected"] != float64(14) {
		t.Fatalf("body = %s, want provision delete with 2 records and 14 values", rec.Body.String())
	}

	ctx := context.Background()
	for _, q := range []string{
		`SELECT COUNT(*) FROM data`, `SELECT COUNT(*) FROM record_entities`,
		`SELECT COUNT(*) FROM survey_links`, `SELECT COUNT(*) FROM anon_offsets`,
	} {
		var n int
		if err := f.e.Store.DB.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s = %d (err %v), want 0 — the data plane is emptied", q, n, err)
		}
	}
	// Metadata and structure survive: the project stays administrable (§7.3).
	if p, err := f.e.Store.GetProject(ctx, f.pid); err != nil || p == nil {
		t.Errorf("project kept = %v (%v), want it intact", p, err)
	}
	if arms, err := f.e.Store.ListArms(ctx, f.pid); err != nil || len(arms) != 2 {
		t.Errorf("arms = %v (%v), want both kept", arms, err)
	}

	_, d := f.lastAudit(t, audit.ProjectEnded)
	if d["provision"] != "delete" || d["records_affected"] != float64(2) {
		t.Errorf("audit details = %v, want the provision and the counts", d)
	}

	// One-shot: the audit event is the idempotency state (§7.2).
	rec = f.e.do(http.MethodPost, "/api/v1/projects/"+itoa(f.pid)+"/end-provision",
		map[string]string{"provision": "anonymize"}, admin)
	if rec.Code != http.StatusConflict || decodeError(t, rec) != "conflict" {
		t.Errorf("second execution = %d %s, want 409 conflict", rec.Code, rec.Body.String())
	}
	var n int
	f.e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, audit.ProjectEnded).Scan(&n)
	if n != 1 {
		t.Errorf("project_ended rows = %d, want exactly 1", n)
	}
}

func TestEndProvisionAnonymize(t *testing.T) {
	f := newExportFixture(t)
	admin := f.e.mustAdmin("admin@example.org")

	rec := f.e.do(http.MethodPost, "/api/v1/projects/"+itoa(f.pid)+"/end-provision",
		map[string]string{"provision": "anonymize"}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymize = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var out map[string]any
	f.e.decode(rec, &out)
	// Six values change per record: identifier and email cleared, secret
	// hashed, the unapproved free-text fields (age, notes) cleared,
	// visit_date shifted.
	if out["provision"] != "anonymize" || out["records_affected"] != float64(2) || out["values_affected"] != float64(12) {
		t.Fatalf("body = %s, want 2 records and 10 values affected", rec.Body.String())
	}

	want := map[string]string{} // field -> value for REC-001 after the rewrite
	rows, err := f.e.Store.DB.Query(
		`SELECT field_name, value FROM data WHERE record_id = 'REC-001'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		want[k] = v
	}
	if want["record_id"] != "" || want["contact_email"] != "" {
		t.Errorf("direct identifiers = %q/%q, want both cleared (§7.4)", want["record_id"], want["contact_email"])
	}
	if want["notes"] != "" || want["age"] != "" {
		t.Errorf("free text = %q/%q, want unapproved free text cleared", want["notes"], want["age"])
	}
	if !isHash(want["secret"]) {
		t.Errorf("secret = %q, want the §5.1 hash", want["secret"])
	}
	if v := want["visit_date"]; v == "" || v == "2026-05-04" || len(v) != 10 {
		t.Errorf("visit_date = %q, want the shifted date", v)
	}

	// Every later read returns the anonymized form (§7.4): an export_full
	// by the admin shows the rewritten values, not the originals.
	rec = f.e.do(http.MethodGet, f.path(""), nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export after anonymize = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "very private") || strings.Contains(body, "participant@example.org") {
		t.Errorf("export still carries pre-anonymization values: %s", body)
	}

	_, d := f.lastAudit(t, audit.ProjectEnded)
	if d["provision"] != "anonymize" || d["values_affected"] != float64(12) {
		t.Errorf("audit details = %v, want the anonymize provision and counts", d)
	}

	// One-shot in both directions.
	rec = f.e.do(http.MethodPost, "/api/v1/projects/"+itoa(f.pid)+"/end-provision",
		map[string]string{"provision": "delete"}, admin)
	if rec.Code != http.StatusConflict {
		t.Errorf("second execution = %d, want 409", rec.Code)
	}
}

func TestEndProvisionGating(t *testing.T) {
	f := newExportFixture(t)
	member := f.member(t, "member@example.org", 0) // role-less member, not is_admin
	admin := f.e.mustAdmin("admin@example.org")
	path := "/api/v1/projects/" + itoa(f.pid) + "/end-provision"

	rec := f.e.do(http.MethodPost, path, map[string]string{"provision": "delete"}, member)
	if rec.Code != http.StatusForbidden || decodeError(t, rec) != "forbidden" {
		t.Errorf("non-admin = %d %s, want 403 forbidden", rec.Code, rec.Body.String())
	}
	rec = f.e.do(http.MethodPost, path, map[string]string{"provision": "purge"}, admin)
	if rec.Code != http.StatusBadRequest || decodeError(t, rec) != "invalid_request" {
		t.Errorf("bad provision = %d %s, want 400 invalid_request", rec.Code, rec.Body.String())
	}
	rec = f.e.do(http.MethodPost, "/api/v1/projects/9999/end-provision",
		map[string]string{"provision": "delete"}, admin)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	// Nothing executed: no audit event, data intact.
	var n int
	f.e.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, audit.ProjectEnded).Scan(&n)
	if n != 0 {
		t.Errorf("project_ended rows = %d, want 0", n)
	}
	if err := f.e.Store.DB.QueryRow(`SELECT COUNT(*) FROM data`).Scan(&n); err != nil || n == 0 {
		t.Errorf("data rows = %d (err %v), want the project untouched", n, err)
	}
}
