package dataapi

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

// Handler serves the data API against one store.
type Handler struct {
	Store   *db.Store
	Cfg     *config.Config
	Limiter *RateLimiter  // nil = rate limiting off (default, REQ-CFG-020)
	Audit   *audit.Writer // nil = audit writes skipped (unit tests)

	// Structure caches shared across calls; see dictcache.go. Both are used
	// under their internal locks, so the zero values work in place.
	dicts dictCache
	regs  registryCache
}

// ServeHTTP dispatches one data-API call (API_Endpoints_Design.md §3).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
	default:
		writeError(w, "json", http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	p, err := ParseParams(r)
	if err != nil {
		writeError(w, "csv", http.StatusBadRequest, "Invalid content")
		return
	}
	enc := p.Encoding()

	// Rate limiting is per source IP, thresholds from the system settings
	// (REQ-API-038/111/112/113); checked before token work so an over-budget
	// caller cannot spend token lookups. Over the budget blocks the IP for
	// the configured period, announced as Retry-After.
	if h.Limiter != nil {
		if enabled, lim := RateLimitSettings(r.Context(), h.Store); enabled {
			ok, retryAfter := h.Limiter.Allow(SourceIP(h.Cfg, r), lim, time.Now())
			if !ok {
				SetRetryAfter(w, retryAfter)
				writeError(w, enc, http.StatusTooManyRequests, "Rate limit exceeded")
				return
			}
		}
	}

	sub, rerr := h.resolveToken(r.Context(), p.Token)
	if errors.Is(rerr, errInvalidToken) {
		writeError(w, enc, http.StatusUnauthorized, "Invalid token")
		return
	}
	if rerr != nil {
		writeError(w, enc, http.StatusInternalServerError, "Internal error")
		return
	}

	ctx := r.Context()
	// A survey-link caller is scoped to the two calls of §3.10 and never
	// reaches the permission table below (REQ-API-083).
	if sub.isLink() {
		h.serveSurveyLink(ctx, w, r, enc, sub, p)
		return
	}
	switch p.Content {
	case "":
		writeError(w, enc, http.StatusBadRequest, "Invalid content")
	case "project":
		// Any valid token for the project suffices (REQ-API-018).
		h.contentProject(ctx, w, enc, sub, p)
	case "event", "metadata", "formEventMapping", "exportFieldNames":
		h.requireData(w, enc, sub, lvlReadOnly, func() {
			switch p.Content {
			case "event":
				h.contentEvent(ctx, w, enc, sub, p)
			case "metadata":
				h.contentMetadata(ctx, w, enc, sub, p)
			case "formEventMapping":
				h.contentFormEventMapping(ctx, w, enc, sub, p)
			case "exportFieldNames":
				h.contentExportFieldNames(ctx, w, enc, sub, p)
			}
		})
	case "generateNextRecordName":
		h.requireData(w, enc, sub, lvlViewEdit, func() {
			h.contentGenerateNextRecordName(ctx, w, enc, sub, p)
		})
	case "record":
		h.contentRecord(w, r, enc, sub, p)
	default:
		writeError(w, enc, http.StatusBadRequest, "Invalid content")
	}
}

// requireData enforces the holder's data level: insufficient permission
// is a uniform 403 "Permission denied" (REQ-API-007).
func (h *Handler) requireData(w http.ResponseWriter, enc string, sub *subject, min int, fn func()) {
	if !sub.hasData(min) {
		writeError(w, enc, http.StatusForbidden, "Permission denied")
		return
	}
	fn()
}

// storeError renders a data-store failure: 500, no internal detail
// (REQ-API-006).
func (h *Handler) storeError(w http.ResponseWriter, enc string) {
	writeError(w, enc, http.StatusInternalServerError, "Internal error")
}

// --- response rendering (REQ-API-013: the requested format wins) ---

// render writes rows as JSON or CSV. Rows are always a non-nil slice of
// struct values; the struct field order is the response key order.
func render(w http.ResponseWriter, enc string, delimiter rune, rows any) {
	if enc == "json" {
		writeJSON(w, rows)
	} else {
		writeCSV(w, delimiter, rows)
	}
}

func writeJSON(w http.ResponseWriter, rows any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rows)
}

// writeCount renders the returnContent=count import response (REQ-API-142):
// {"count": N} for JSON callers, a bare count line otherwise.
func writeCount(w http.ResponseWriter, enc string, n int) {
	if enc == "json" {
		writeJSON(w, struct {
			Count int `json:"count"`
		}{Count: n})
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	_, _ = fmt.Fprintln(w, n)
}

// writeCSV renders the same rows with a header line. The header is always
// written — even for a zero-row result, so callers get the column names
// (REDCap returns a header row for an empty result). Header and values are
// derived from the struct via reflection, so the two encodings never drift.
func writeCSV(w http.ResponseWriter, delimiter rune, rows any) {
	w.Header().Set("Content-Type", "text/csv")
	cw := csv.NewWriter(w)
	cw.Comma = delimiter
	rv := reflect.ValueOf(rows)
	if rv.Kind() != reflect.Slice {
		cw.Flush()
		return
	}
	// Element type from the slice type, valid whether or not any rows are
	// present (rv.Index(0) would not be for an empty slice).
	ft := rv.Type().Elem()
	if ft.Kind() == reflect.Struct {
		header := make([]string, ft.NumField())
		for i := range header {
			header[i] = jsonName(ft.Field(i))
		}
		_ = cw.Write(header)
	}
	for i := 0; i < rv.Len(); i++ {
		fv := rv.Index(i)
		vals := make([]string, ft.NumField())
		for j := range vals {
			vals[j] = fmt.Sprintf("%v", fv.Field(j).Interface())
		}
		_ = cw.Write(vals)
	}
	cw.Flush()
}

// jsonName is the response key of a struct field (its json tag).
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" || tag == "-" {
		return f.Name
	}
	return tag
}

// writeError renders the §3.2 error body in the requested response
// format: {"error": …} for json, the bare single message line for csv.
func writeError(w http.ResponseWriter, enc string, status int, message string) {
	if enc == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		b, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{Error: message})
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, message)
}
