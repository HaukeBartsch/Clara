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

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// Handler serves the data API against one store.
type Handler struct {
	Store   *db.Store
	Cfg     *config.Config
	Limiter *RateLimiter // nil = rate limiting off (default, REQ-CFG-020)
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

	sub, rerr := h.resolveToken(r.Context(), p.Token)
	if errors.Is(rerr, errInvalidToken) {
		writeError(w, enc, http.StatusUnauthorized, "Invalid token")
		return
	}
	if rerr != nil {
		writeError(w, enc, http.StatusInternalServerError, "Internal error")
		return
	}

	// Rate limiting is per token (REQ-API-038).
	if h.Limiter != nil && !h.Limiter.Allow(p.Token, time.Now()) {
		writeError(w, enc, http.StatusTooManyRequests, "Rate limit exceeded")
		return
	}

	ctx := r.Context()
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
		// record&action=export|import|delete is the next slice (API_
		// Endpoints_Design.md §3.6–§3.8); until it lands the call fails
		// with the §3.2 contract for an unsupported content/action.
		writeError(w, enc, http.StatusBadRequest, "Invalid content")
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

// writeCSV renders the same rows with a header line. Header and values
// are derived from the struct via reflection, so the two encodings never
// drift.
func writeCSV(w http.ResponseWriter, delimiter rune, rows any) {
	w.Header().Set("Content-Type", "text/csv")
	cw := csv.NewWriter(w)
	cw.Comma = delimiter
	rv := reflect.ValueOf(rows)
	if rv.Kind() != reflect.Slice {
		cw.Flush()
		return
	}
	if rv.Len() > 0 {
		ft := rv.Index(0).Type()
		header := make([]string, ft.NumField())
		for i := range header {
			header[i] = jsonName(ft.Field(i))
		}
		_ = cw.Write(header)
	}
	for i := 0; i < rv.Len(); i++ {
		fv := rv.Index(i)
		ft := fv.Type()
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
