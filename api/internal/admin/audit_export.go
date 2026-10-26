package admin

// GET /api/v1/audit/export (§4.23, REQ-API-158, REQ-AUD-031/032): the audit
// trail as CSV inside a streamed ZIP — every entry the §4.15 filters match,
// pagination never bounding an export. The ZIP is written straight to the
// response (archive/zip over non-seekable output uses data descriptors) and
// one row is in hand at a time: the log may be multi-GB and is never
// materialized (REQ-TECH-011). estimate=1 answers {rows, estimated_bytes}
// instead of the file, the bounded figure behind the > 1 GiB confirmation
// (REQ-AUD-032). Every export that ends writes one audit_export entry — a read
// of the trail itself, filters and counts, no values (§3.5).

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"database/sql"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

func (h *Handler) exportAudit(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	f, bad := parseAuditFilter(q)
	if bad != "" {
		errBadRequest(w, bad)
		return
	}
	// Same stable-name latch as the listing (audit design §6).
	if err := h.Audit.EnsureYear(ctx); err != nil {
		errInternal(w)
		return
	}
	if q.Get("estimate") == "1" {
		h.auditEstimate(w, ctx, f)
		return
	}
	h.streamAuditZip(w, r, u, f)
}

// auditColumns names the export's header row and SELECT per table (§4.23):
// events carry the full fixed-column set plus the payload, views their §4
// shape — wider than the listing's JSON on purpose, the file is the archive.
func auditColumns(table string) (header []string, selectSQL string) {
	if table == "views" {
		return []string{"id", "created_at", "user_id", "email", "token", "project_id", "record_ids", "instruments"},
			`SELECT id, created_at, user_id, email, token, project_id, record_ids, instruments FROM audit_record_views`
	}
	return []string{"id", "created_at", "source", "user_id", "email", "token", "project_id", "arm_num", "role", "target_record", "event_type", "details"},
		`SELECT id, created_at, source, user_id, email, token, project_id, arm_num, role, target_record, event_type, details FROM audit_events`
}

// scanAuditCells renders one row into its CSV cells. Every cell passes formula
// neutralization (REQ-VAL-032) as record exports do; unset columns are empty
// cells; `details` goes out as the stored JSON text.
func scanAuditCells(scan func(dest ...any) error, table string) ([]string, error) {
	if table == "views" {
		var (
			id       int64
			created  any
			userID   sql.NullInt64
			email    sql.NullString
			token    sql.NullString
			project  int64
			recordID sql.NullString
			instrs   sql.NullString
		)
		if err := scan(&id, &created, &userID, &email, &token, &project, &recordID, &instrs); err != nil {
			return nil, err
		}
		cells := []string{strconv.FormatInt(id, 10), normDatetime(created), nullIntCell(userID),
			email.String, token.String, strconv.FormatInt(project, 10), recordID.String, instrs.String}
		return neutralizeRow(cells), nil
	}
	var (
		id       int64
		created  any
		source   string
		userID   sql.NullInt64
		email    sql.NullString
		token    sql.NullString
		project  sql.NullInt64
		armNum   sql.NullInt64
		role     sql.NullString
		target   sql.NullString
		eventTyp string
		details  sql.NullString
	)
	if err := scan(&id, &created, &source, &userID, &email, &token, &project,
		&armNum, &role, &target, &eventTyp, &details); err != nil {
		return nil, err
	}
	cells := []string{strconv.FormatInt(id, 10), normDatetime(created), source, nullIntCell(userID),
		email.String, token.String, nullIntCell(project), nullIntCell(armNum), role.String,
		target.String, eventTyp, details.String}
	return neutralizeRow(cells), nil
}

func neutralizeRow(cells []string) []string {
	for i, c := range cells {
		cells[i] = validate.CSVCell(c)
	}
	return cells
}

func nullIntCell(ni sql.NullInt64) string {
	if !ni.Valid {
		return ""
	}
	return strconv.FormatInt(ni.Int64, 10)
}

// auditEstimate answers estimate=1: the exact count of matching entries and an
// advisory byte figure — rows × the average rendered row size sampled from at
// most the newest 1000 matches, so the sample is a bounded index-ordered scan
// whatever the table holds (REQ-API-158, REQ-AUD-032).
func (h *Handler) auditEstimate(w http.ResponseWriter, ctx context.Context, f *auditFilter) {
	table := "audit_events"
	if f.table == "views" {
		table = "audit_record_views"
	}
	where := " WHERE " + joinConditions(f.conds)
	var rows int64
	if err := h.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+table+where, f.args...).Scan(&rows); err != nil {
		errInternal(w)
		return
	}
	_, sel := auditColumns(f.table)
	sample, err := h.Store.DB.QueryContext(ctx,
		sel+where+` ORDER BY created_at DESC, id DESC LIMIT 1000`, f.args...)
	if err != nil {
		errInternal(w)
		return
	}
	defer sample.Close()
	var total, sampled int64
	for sample.Next() {
		cells, err := scanAuditCells(sample.Scan, f.table)
		if err != nil {
			errInternal(w)
			return
		}
		for _, c := range cells {
			total += int64(len(c)) + 1 // the separator or line break behind it
		}
		sampled++
	}
	if err := sample.Err(); err != nil {
		errInternal(w)
		return
	}
	est := int64(0)
	if sampled > 0 {
		est = int64(math.Round(float64(total) / float64(sampled) * float64(rows)))
	}
	writeJSON(w, http.StatusOK, struct {
		Rows           int64 `json:"rows"`
		EstimatedBytes int64 `json:"estimated_bytes"`
	}{rows, est})
}

// streamAuditZip streams the whole filtered trail as one CSV member of a ZIP
// written straight to the response, chronologically (an archive reads front to
// back, unlike the reverse-chronological listing), and records the read as one
// audit_export entry when it ends — including a cut-short one, which carries
// truncated: true (§3.5).
func (h *Handler) streamAuditZip(w http.ResponseWriter, r *http.Request, u *db.User, f *auditFilter) {
	ctx := r.Context()
	_, sel := auditColumns(f.table)
	rows, err := h.Store.DB.QueryContext(ctx,
		sel+" WHERE "+joinConditions(f.conds)+" ORDER BY created_at ASC, id ASC", f.args...)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	member := "audit_events.csv"
	if f.table == "views" {
		member = "audit_record_views.csv"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="audit_`+f.table+`_`+now.Format("2006-01-02_1504")+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	header, _ := auditColumns(f.table)
	zw := zip.NewWriter(w)
	truncated := false
	var delivered int64
	memberw, err := zw.CreateHeader(&zip.FileHeader{Name: member, Method: zip.Deflate, Modified: now})
	if err != nil {
		truncated = true
	} else {
		cw := csv.NewWriter(memberw)
		if err := cw.Write(header); err != nil {
			truncated = true
		}
		for !truncated && rows.Next() {
			cells, err := scanAuditCells(rows.Scan, f.table)
			if err != nil {
				truncated = true
				break
			}
			if err := cw.Write(cells); err != nil {
				truncated = true // the client went away mid-stream
				break
			}
			delivered++
			if delivered%1000 == 0 {
				cw.Flush() // hand the chunk downstream instead of holding it
			}
		}
		if err := rows.Err(); err != nil {
			truncated = true
		}
		cw.Flush()
	}
	if err := zw.Close(); err != nil {
		truncated = true // the trail records what did not arrive
	}

	// The download itself may have ended with the client's context — the entry
	// is still owed (REQ-AUD-031), so it writes uncanceled.
	_ = h.Audit.Insert(context.WithoutCancel(ctx), audit.Entry{
		EventType: audit.AuditExport,
		Source:    audit.SourceUI,
		UserID:    u.ID,
		Email:     u.Email,
		Details:   auditExportDetailsOf(f.table, delivered, truncated, r.URL.Query()),
	})
}

// auditExportDetailsOf is the §3.5 payload: the filters as applied (normalized
// where the filter was), the rows delivered, and truncation only when it
// happened. No entry values ever enter it — this names a read, not data.
func auditExportDetailsOf(table string, rows int64, truncated bool, q url.Values) any {
	details := struct {
		Type      string          `json:"type"`
		Rows      int64           `json:"rows"`
		Truncated bool            `json:"truncated,omitempty"`
		Filters   auditExportFilt `json:"filters"`
	}{Type: table, Rows: rows, Truncated: truncated}
	if v := q.Get("project"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			details.Filters.Project = &n
		}
	}
	if v := q.Get("user"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			details.Filters.User = &n
		}
	}
	if v := q.Get("event_type"); v != "" && table == "events" {
		details.Filters.EventType = &v
	}
	if v := q.Get("from"); v != "" {
		if norm, ok := parseRangeBound(v, false); ok {
			details.Filters.From = &norm
		}
	}
	if v := q.Get("to"); v != "" {
		if norm, ok := parseRangeBound(v, true); ok {
			details.Filters.To = &norm
		}
	}
	return details
}

type auditExportFilt struct {
	Project   *int64  `json:"project"`
	User      *int64  `json:"user"`
	EventType *string `json:"event_type"`
	From      *string `json:"from"`
	To        *string `json:"to"`
}
