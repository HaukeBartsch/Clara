package dataapi

// RunExportRows — the export pipeline's rows for a writer that is neither CSV
// nor JSON (Design/ODM_Interchange_Design.md §6.1, O-6, REQ-ODM-021).
//
// The ODM exporter must not read the `data` table or transform anything itself:
// it has to deliver exactly what the tabular exports deliver, per (instrument,
// event) pair — the same column selection, the same §4.2 step order, the same
// field hash, date shift, checkbox expansion and record scope. So this drives
// streamExport unchanged in its flat JSON layout and hands the rows over as they
// are produced, one at a time, through an io.Pipe: no buffering of the export,
// and one pipeline rather than two that have to agree.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// ExportRow is one exported row: column name to value, every value a string (a
// repeat instance arrives as the digits the JSON export wrote for it). It is the
// flat layout — the record identifier under its own field name, redcap_event_name,
// and redcap_repeat_instrument / redcap_repeat_instance where instruments repeat
// (record_export.go's header rules, REQ-API-028/155).
type ExportRow map[string]string

// RowSource reads the rows of a running export. Close it when reading stops
// early: that unblocks the producing goroutine, whose write to a pipe nobody
// reads would otherwise wait for the request's context.
type RowSource struct {
	dec      *json.Decoder
	closer   *io.PipeReader
	settled  chan struct{} // closed when the reader has taken everything (or gave up)
	once     sync.Once
	finished bool
}

// Settled reports on the channel that closes when this source has been read to
// its end or closed. The pipeline waits for it before auditing, so a format that
// counts what it wrote audits the counts of the file that actually went out
// rather than a snapshot taken while the last rows were still in flight.
func (rs *RowSource) Settled() <-chan struct{} { return rs.settled }

// settle releases a pipeline waiting on Settled. Exactly once, whether the read
// ended by itself or through Close.
func (rs *RowSource) settle() { rs.once.Do(func() { close(rs.settled) }) }

// RunExportRows starts an export as a row stream and returns the HTTP status the
// pipeline chose once it is known: 200 with a source to read, or the refusal —
// 403 when the scope holds no permitted pair, 400 for a malformed filterLogic —
// with nothing streamed. The caller renders that status in its own error shape,
// so an ODM download that is refused answers as an error and not as a document
// that begins `<?xml` (REQ-AUTH-069, REQ-API-075).
func (h *Handler) RunExportRows(ctx context.Context, s ExportSpec) (int, *RowSource, error) {
	pr, pw := io.Pipe()
	fw := &rowStreamWriter{pw: pw, ready: make(chan struct{})}
	rs := &RowSource{closer: pr, settled: make(chan struct{})}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/", nil)
	if err != nil {
		pw.Close()
		rs.settle()
		return http.StatusInternalServerError, nil, err
	}
	s.RowsSettled = rs.settled // the pipeline waits on this before auditing (§6.1)
	go func() { h.RunExport(fw, req, s); pw.Close() }()

	select {
	case <-fw.ready:
	case <-ctx.Done():
		pw.CloseWithError(ctx.Err())
		pr.Close()
		rs.settle()
		return 0, nil, ctx.Err()
	}
	if status := fw.get(); status != http.StatusOK {
		go func() { _, _ = io.Copy(io.Discard, pr); pr.Close() }() // let the producer finish
		rs.settle()
		return status, nil, nil
	}

	dec := json.NewDecoder(pr)
	tok, err := dec.Token() // the '[' every JSON export opens with
	if err != nil {
		pr.Close()
		rs.settle()
		if status := fw.get(); status != http.StatusOK {
			return status, nil, nil
		}
		return http.StatusInternalServerError, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		pr.Close()
		rs.settle()
		return http.StatusInternalServerError, nil, errors.New("export stream did not open as a JSON array")
	}
	rs.dec = dec
	return http.StatusOK, rs, nil
}

// Next returns the next row, or ok=false at the end of the export. A truncated
// stream — the pipeline died after its header went out, which is what CSV does
// too — reports the read error rather than pretending the rows were all there.
func (rs *RowSource) Next() (ExportRow, bool, error) {
	if rs.finished {
		return nil, false, nil
	}
	if !rs.dec.More() {
		rs.finished = true
		_, err := rs.dec.Token() // consume ']'
		rs.settle()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		return nil, false, nil
	}
	var raw map[string]json.RawMessage
	if err := rs.dec.Decode(&raw); err != nil {
		rs.finished = true
		rs.settle()
		return nil, false, err
	}
	row := make(ExportRow, len(raw))
	for k, v := range raw {
		row[k] = jsonString(v)
	}
	return row, true, nil
}

// Close ends the read side. An unfinished stream leaves the producer blocked on
// the pipe, so it is released here with an error rather than left running.
func (rs *RowSource) Close() {
	rs.settle()
	if rs.finished {
		_ = rs.closer.Close()
		return
	}
	rs.finished = true
	_ = rs.closer.CloseWithError(context.Canceled)
}

// jsonString reads a JSON string's value, keeping the digits of the numeric cell
// the JSON export writes for redcap_repeat_instance and treating null as empty.
func jsonString(v json.RawMessage) string {
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	if v[0] != '"' {
		return string(v) // a number the export formatted itself
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

// auditEvent names the entry this export writes: `export` unless the caller
// renamed it (ExportSpec.AuditAs, REQ-ODM-061).
func (s ExportSpec) auditEvent() string {
	if s.AuditAs != "" {
		return s.AuditAs
	}
	return audit.Export
}

// ExportFieldKeptAt reports whether one field's column survives the sensitivity
// pipeline at a given export level rank — the same two removals streamExport
// applies (§4.2 steps 1 and 3 of Data_Export_Anonymization_Design.md), shared
// rather than restated so that a format describing the project does not declare
// an item whose values a level withholds (REQ-ODM-021, ODM_Interchange_Design.md
// §3.9). A checkbox parent is always kept: its derived choice columns are what
// carry values, and those inherit the parent's flags yet are never free text and
// never hashed (REQ-EXP-053).
func ExportFieldKeptAt(f db.Field, identifier string, rank int) bool {
	if f.FieldType == "checkbox" {
		return true
	}
	if rank < expFull && (f.FieldName == identifier || f.DirectIdentifier) {
		return false
	}
	freeText := f.FieldType == "text" || f.FieldType == "notes" // REQ-DB-044
	if rank == expDeIdentified && freeText && !f.ExportApproved {
		return false
	}
	return true
}

// rowStreamWriter is the http.ResponseWriter streamExport writes into: it
// records the status the pipeline settled on and forwards only the successful
// body. An error body is discarded rather than forwarded — the caller renders a
// refusal in its own envelope, and forwarding it would also block this writer's
// reader, which has already stopped reading to answer the status.
type rowStreamWriter struct {
	pw    *io.PipeWriter
	ready chan struct{}

	mu     sync.Mutex
	status int
	once   sync.Once
}

func (f *rowStreamWriter) Header() http.Header { return http.Header{} }

func (f *rowStreamWriter) WriteHeader(code int) { f.setStatus(code) }

func (f *rowStreamWriter) Write(p []byte) (int, error) {
	if status := f.get(); status != 0 && status != http.StatusOK {
		return len(p), nil // an error body: the caller renders the refusal itself
	}
	f.setStatus(http.StatusOK) // an implicit 200 from a body write releases the waiter
	return f.pw.Write(p)
}

// setStatus records the first status seen and releases the waiting caller; a
// later WriteHeader (there is none in practice) cannot change it.
func (f *rowStreamWriter) setStatus(code int) {
	f.mu.Lock()
	if f.status == 0 {
		f.status = code
	}
	f.mu.Unlock()
	f.once.Do(func() { close(f.ready) })
}

func (f *rowStreamWriter) get() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}
