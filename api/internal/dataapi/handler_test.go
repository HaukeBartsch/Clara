package dataapi

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The two response encodings are derived from one row struct so they cannot
// drift (REQ-API-013): the field order is the key order for JSON and the column
// order for CSV. contents_test.go covers these end to end; what is pinned here
// is the rendering contract itself, including the cases a content handler never
// produces but a caller can hit.

type renderRow struct {
	Record   string `json:"record_id"`
	Age      int    `json:"age"`
	Comment  string `json:"comment,omitempty"`
	Untagged string
}

func TestJSONName(t *testing.T) {
	ft := reflect.TypeOf(renderRow{})
	cases := map[string]string{
		"Record":   "record_id",
		"Age":      "age",
		"Comment":  "comment", // the ",omitempty" suffix is not part of the key
		"Untagged": "Untagged",
	}
	for name, want := range cases {
		f, ok := ft.FieldByName(name)
		if !ok {
			t.Fatalf("renderRow has no field %s", name)
		}
		if got := jsonName(f); got != want {
			t.Errorf("jsonName(%s) = %q, want %q", name, got, want)
		}
	}
}

// writeCSV always emits the header line — even for zero rows, so a caller
// parsing the body sees the column names of an empty result (REDCap behavior).
func TestWriteCSVHeaderAlwaysPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCSV(rec, ',', []renderRow{})

	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	body := rec.Body.String()
	want := "record_id,age,comment,Untagged\n"
	if body != want {
		t.Errorf("empty-result body = %q, want the header line %q", body, want)
	}
}

func TestWriteCSVRows(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCSV(rec, ',', []renderRow{
		{Record: "8DISC001", Age: 42, Comment: "ok"},
		{Record: "8DISC002", Age: 7},
	})

	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("body is not parseable CSV (%q): %v", rec.Body.String(), err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d CSV lines, want header + 2 rows: %v", len(rows), rows)
	}
	if want := []string{"record_id", "age", "comment", "Untagged"}; !reflect.DeepEqual(rows[0], want) {
		t.Errorf("header = %v, want %v", rows[0], want)
	}
	// A zero int renders as 0 and an absent string as empty: values are the
	// stored scalars, not Go-omitted.
	if want := []string{"8DISC001", "42", "ok", ""}; !reflect.DeepEqual(rows[1], want) {
		t.Errorf("row 1 = %v, want %v", rows[1], want)
	}
	if want := []string{"8DISC002", "7", "", ""}; !reflect.DeepEqual(rows[2], want) {
		t.Errorf("row 2 = %v, want %v", rows[2], want)
	}
}

// The delimiter is the caller's (REQ-API-015: semicolon, tab, pipe), and CSV
// quoting handles a value that contains the delimiter or a quote.
func TestWriteCSVDelimiterAndQuoting(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCSV(rec, ';', []renderRow{{Record: `a;b,"c"`, Age: 1}})

	r := csv.NewReader(strings.NewReader(rec.Body.String()))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("body is not parseable CSV (%q): %v", rec.Body.String(), err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(rows), rows)
	}
	if got := rows[1][0]; got != `a;b,"c"` {
		t.Errorf("round-tripped value = %q, want %q", got, `a;b,"c"`)
	}
}

// A non-slice payload has no columns to describe, so only the (empty) stream is
// written rather than a panic on reflection over the wrong kind.
func TestWriteCSVNonSlice(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCSV(rec, ',', renderRow{Record: "x"})

	if got := rec.Body.String(); got != "" {
		t.Errorf("body = %q for a non-slice payload, want empty", got)
	}
}

// writeError answers in the format the caller asked for: the {"error": …} body
// for JSON (REQ-API-005), the bare message line for CSV (§3.2).
func TestWriteErrorJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, "json", http.StatusForbidden, "Permission denied")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"error":"Permission denied"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWriteErrorCSV(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, "csv", http.StatusBadRequest, "Invalid content")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	if got := rec.Body.String(); got != "Invalid content\n" {
		t.Errorf("body = %q, want the single message line", got)
	}
}

// render picks the encoding from the request, not from the payload (§3.2: the
// requested response format wins over the default).
func TestRenderSelectsEncoding(t *testing.T) {
	rows := []renderRow{{Record: "8DISC001", Age: 42}}

	jsonRec := httptest.NewRecorder()
	render(jsonRec, "json", ',', rows)
	if got := jsonRec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("json Content-Type = %q", got)
	}
	if !strings.Contains(jsonRec.Body.String(), `"record_id":"8DISC001"`) {
		t.Errorf("json body = %s, want the row keyed by record_id", jsonRec.Body.String())
	}

	csvRec := httptest.NewRecorder()
	render(csvRec, "csv", ',', rows)
	if got := csvRec.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("csv Content-Type = %q", got)
	}
	if !strings.HasPrefix(csvRec.Body.String(), "record_id,age") {
		t.Errorf("csv body = %s, want the header line first", csvRec.Body.String())
	}
}

// writeJSON never emits a null where a caller expects an array: an empty result
// is `[]`, so a client can iterate without a nil check.
func TestWriteJSONEmptySlice(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, []renderRow{})

	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// A nil limiter means rate limiting is off (REQ-CFG-020): the request proceeds
// to token resolution instead of panicking on the missing limiter.
func TestHandlerNilLimiterProceeds(t *testing.T) {
	h, full, _ := testHandler(t)
	h.Limiter = nil

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://test/api/",
		strings.NewReader("token="+full+"&content=project&format=json"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d with a nil limiter, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// A method the API does not implement is rejected before any parsing or token
// work, in the default response format (§3.2).
func TestServeHTTPRejectsUnsupportedMethods(t *testing.T) {
	h, _, _ := testHandler(t)

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "http://test/api/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}
