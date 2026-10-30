package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"csms/api/internal/db"
)

// --- the dashboard's streamed reads (§4.13 Streaming, REQ-API-172) ---

// statusChunk is how many visible records one round of the dashboard's reads
// covers: the rows one query reads and the marks held in memory at a time.
// With the project id and the completeness count's field list it stays far
// below the bound-parameter limit of either dialect (SQLite 32,766, MariaDB
// 65,535). A variable so the tests can cross chunk boundaries with a handful
// of records.
var statusChunk = 1000

// ndjsonType is the media type of the line-per-record answer.
const ndjsonType = "application/x-ndjson"

// acceptsNDJSON reports whether an Accept header lists application/x-ndjson
// with a quality above zero — the one request that receives a line per record
// (REQ-API-172); every other receives the JSON array.
func acceptsNDJSON(accept string) bool {
	for _, item := range strings.Split(accept, ",") {
		params := strings.Split(item, ";")
		if !strings.EqualFold(strings.TrimSpace(params[0]), ndjsonType) {
			continue
		}
		for _, p := range params[1:] {
			name, value, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(name), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err != nil || q <= 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// recordIDsOf names a chunk's records, in order.
func recordIDsOf(chunk []db.RecordEntity) []string {
	ids := make([]string, len(chunk))
	for i, re := range chunk {
		ids[i] = re.RecordID
	}
	return ids
}

// recordIndexOf places each record of a chunk by its index in the chunk — the
// record half of the statusCell keys the marks of one chunk are held under.
func recordIndexOf(chunk []db.RecordEntity) map[string]int32 {
	at := make(map[string]int32, len(chunk))
	for i, re := range chunk {
		at[re.RecordID] = int32(i)
	}
	return at
}

// inRecords is the `record_id IN (…)` condition of one chunk and its arguments,
// to follow the project id.
func inRecords(chunk []db.RecordEntity) (string, []any) {
	args := make([]any, len(chunk))
	for i, re := range chunk {
		args[i] = re.RecordID
	}
	return `record_id IN (?` + strings.Repeat(", ?", len(chunk)-1) + `)`, args
}

// streamStatus writes one of the dashboard's reads chunk by chunk: rowsOf
// answers the rows of one chunk of visible records, in the chunk's order. The
// first chunk is read before the status line, so a store failure there is the
// usual 500. One after the first byte is logged and ends the response where it
// stands — no end line, no closing bracket — so a cut stream cannot pass for a
// complete project. A caller that stops reading ends the loop at the next
// write. NDJSON when Accept lists it, otherwise the JSON array writeJSON wrote,
// byte for byte: json.Marshal of each row, comma-separated, then a newline.
func streamStatus[T any](w http.ResponseWriter, r *http.Request, visible []db.RecordEntity,
	rowsOf func(context.Context, []db.RecordEntity) ([]T, error)) {
	ctx := r.Context()
	ndjson := acceptsNDJSON(r.Header.Get("Accept"))
	chunkAt := func(start int) []db.RecordEntity {
		return visible[start:min(start+statusChunk, len(visible))]
	}

	var rows []T
	if len(visible) > 0 {
		var err error
		if rows, err = rowsOf(ctx, chunkAt(0)); err != nil {
			errInternal(w)
			return
		}
	}
	if ndjson {
		w.Header().Set("Content-Type", ndjsonType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)

	out := bufio.NewWriterSize(w, 64<<10)
	flusher := http.NewResponseController(w)
	sent := 0
	write := func(rows []T) error {
		for _, row := range rows {
			line, err := json.Marshal(row)
			if err != nil {
				return err
			}
			switch {
			case ndjson:
				line = append(line, '\n')
			case sent > 0:
				if err := out.WriteByte(','); err != nil {
					return err
				}
			}
			if _, err := out.Write(line); err != nil {
				return err
			}
			sent++
		}
		if err := out.Flush(); err != nil {
			return err
		}
		// Both response wrappers pass Flush on; a writer without it still
		// delivers, only later.
		_ = flusher.Flush()
		return nil
	}

	if !ndjson {
		_ = out.WriteByte('[')
	}
	if write(rows) != nil {
		return
	}
	for start := statusChunk; start < len(visible); start += statusChunk {
		rows, err := rowsOf(ctx, chunkAt(start))
		if err != nil {
			if ctx.Err() == nil { // a caller that left is no failure of the store
				slog.Error("dashboard read cut short", "path", r.URL.Path, "records_sent", sent, "err", err)
			}
			return
		}
		if write(rows) != nil {
			return
		}
	}
	if ndjson {
		_, _ = out.WriteString(`{"end":true,"records":` + strconv.Itoa(sent) + "}\n")
	} else {
		_, _ = out.WriteString("]\n")
	}
	_ = out.Flush()
}
