package dataapi

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
	"csms/api/internal/metrics"
)

// Handler serves the data API against one store.
type Handler struct {
	Store   *db.Store
	Cfg     *config.Config
	Limiter *RateLimiter  // nil = rate limiting off (default, REQ-CFG-020)
	Audit   *audit.Writer // nil = audit writes skipped (unit tests)
	// Metrics receives one observation per dispatched call keyed by the
	// closed content vocabulary (REQ-API-163); nil turns it off.
	Metrics *metrics.Registry

	// Cross-call caches; see dictcache.go. Each is internally synchronized,
	// so the zero values work in place.
	dicts  dictCache
	regs   registryCache
	levels authzCache

	// linkLocks serialise survey-link submissions of one link inside this process
	// (record_import.go, REQ-API-145). They are striped by link id, so an import of
	// another record never waits behind a submission, and they are an optimisation
	// only: the conditional stamp is what decides a race, here and across processes.
	linkLocks [linkLockStripes]sync.Mutex
}

// linkLock returns the mutex that keeps submissions of one survey link from
// writing at the same instant in this process.
func (h *Handler) linkLock(linkID int64) *sync.Mutex {
	i := linkID % linkLockStripes
	if i < 0 {
		i += linkLockStripes
	}
	return &h.linkLocks[i]
}

// ServeHTTP dispatches one data-API call (API_Endpoints_Design.md §3).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	default:
		// GET left with the query-string token (REQ-API-010, DEV-API-30): a request
		// that cannot carry its credential is not worth answering, and the only place
		// it could carry it is what every proxy access log keeps. The text names the
		// method and the body so a caller that used to navigate here knows what to change.
		writeError(w, "json", http.StatusMethodNotAllowed,
			"Method not allowed: POST /api/ with the parameters, token included, in the request body")
		return
	}

	p, err := ParseParams(r)
	if err != nil {
		// A request-shape failure (form.go), answered before the token lookup
		// and with the limit named: an oversized batch must never read back as
		// an invalid-token 401.
		writeError(w, "csv", http.StatusBadRequest, paramsErrorMessage(err))
		return
	}
	// One observation per dispatched call keyed by the closed content
	// vocabulary (REQ-API-163): started now, recorded on return, so the
	// duration covers rate limiting, token resolution and the handler work —
	// everything after the parameters are known. Unknown or missing content
	// lands in "other" inside ObserveContent; no caller string mints a series.
	callStart := time.Now()
	defer func() { h.Metrics.ObserveContent(p.Content, time.Since(callStart)) }()

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

	// The CSV options validate strictly (REQ-EXP-013/015): a delimiter outside
	// REDCap's set or an unknown decimal character is a malformed request, never
	// silently read as something else. Answered after the rate limit, which counts
	// it, and before the token lookup.
	if _, ok := ParseDelimiter(p.CSVDelimiter); !ok {
		writeError(w, enc, http.StatusBadRequest, "Invalid csvDelimiter")
		return
	}
	if _, ok := ParseDecimal(p.DecimalCharacter); !ok {
		writeError(w, enc, http.StatusBadRequest, "Invalid decimalCharacter")
		return
	}

	sub, rerr := h.resolveToken(r.Context(), p.Token)
	if errors.Is(rerr, errSurveySubmitted) {
		// A link that already carried its one submission (REQ-API-145): 410, not
		// the uniform 401 — the respondent needs to know the answer arrived, and
		// this is the only disclosure of a link's state to whoever holds it.
		writeError(w, enc, http.StatusGone, "Survey already submitted")
		return
	}
	if errors.Is(rerr, errInvalidToken) {
		writeError(w, enc, http.StatusUnauthorized, "Invalid token")
		return
	}
	if rerr != nil {
		h.storeError(w, enc, rerr)
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
	case "event", "metadata", "formEventMapping", "exportFieldNames", "repeatingFormsEvents":
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
			case "repeatingFormsEvents":
				h.contentRepeatingFormsEvents(ctx, w, enc, sub, p)
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

// storeError logs a data-store failure and renders 500 with no internal detail
// (REQ-API-006). Keeping the cause out of the *response* never meant throwing it
// away: a 500 that leaves no log line gives an operator nothing to chase, so
// every site hands its error here. Only the error and the site that produced it
// are recorded — never tokens, parameters or values (REQ-API-005).
func (h *Handler) storeError(w http.ResponseWriter, enc string, err error) {
	slog.Error("data api: data store failure", "err", err, "at", callerSite(1))
	writeError(w, enc, http.StatusInternalServerError, "Internal error")
}

// failStore is the export counterpart of storeError: log the cause, then answer
// 500 through the surface's own error renderer (ExportSpec.Error), which is how
// §4.14 keeps its JSON shape while §3.2 keeps its text.
func (s ExportSpec) failStore(w http.ResponseWriter, err error) {
	slog.Error("data api: data store failure", "err", err, "at", callerSite(1))
	s.fail(w, http.StatusInternalServerError, "Internal error")
}

// callerSite names the file:line of the caller's caller — the place a failure
// originated, without anyone at a call site having to write it down.
func callerSite(skip int) string {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
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
			f := fv.Field(j)
			switch {
			case f.Kind() == reflect.Pointer && f.IsNil():
				vals[j] = "" // JSON's null is an empty CSV cell
			case f.Kind() == reflect.Pointer:
				vals[j] = fmt.Sprintf("%v", f.Elem().Interface())
			default:
				vals[j] = fmt.Sprintf("%v", f.Interface())
			}
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
