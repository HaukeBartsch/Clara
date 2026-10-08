package dataapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// The body parser exists because Go 1.27's net/url caps one urlencoded string
// at 10,000 parameters (net/url.defaultMaxParams). These tests pin the two
// halves of the fix: an indexed batch that large parses (and is not answered as
// an authentication failure), and a request that really is too big fails with
// the limit named instead of quietly losing its token.

// indexedBatch builds an indexed import body of rows × cols cells plus the
// protocol parameters — the shape that used to trip the cap at 50 × 200.
func indexedBatch(rows, cols int) string {
	var b strings.Builder
	b.WriteString("token=tok-edit&content=record&action=import&returnFormat=json")
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			fmt.Fprintf(&b, "&data[%d][field_%d]=v%d_%d", i, j, i, j)
		}
	}
	return b.String()
}

func postBody(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// TestIndexedBatchOverTheStdlibCapParses is the reported failure: 50 rows ×
// 200 fields is 10,004 parameters, over Go 1.27's cap, and url.ParseQuery
// answers it with an error after having consumed the body — which the old
// fallback turned into empty parameters and a 401 for a valid token.
func TestIndexedBatchOverTheStdlibCapParses(t *testing.T) {
	body := indexedBatch(50, 200)
	if _, err := url.ParseQuery(body); err == nil {
		t.Fatal("the fixture no longer exceeds the stdlib cap; pick a larger batch")
	}

	r := postBody(body)
	p, err := ParseParams(r)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if p.Token != "tok-edit" || p.Content != "record" || p.Action != "import" {
		t.Errorf("protocol parameters lost: %+v", p)
	}
	if !p.HasData {
		t.Error("HasData = false, want true for indexed data rows")
	}

	rows := parseImportRows(r, p)
	if len(rows) != 50 {
		t.Fatalf("rows = %d, want 50", len(rows))
	}
	for i, row := range rows {
		if len(row) != 200 {
			t.Fatalf("row %d has %d fields, want 200", i, len(row))
		}
	}
	if got := rows[49]["field_199"]; got != "v49_199" {
		t.Errorf("rows[49][field_199] = %q, want v49_199", got)
	}
}

// The query string is capped too, and a GET export listing thousands of
// records hits it the same way (the old code used r.URL.Query(), which drops
// the error and returns nothing).
func TestQueryBeyondTheStdlibCapParses(t *testing.T) {
	var q strings.Builder
	for i := 0; i < 12_000; i++ {
		if i > 0 {
			q.WriteString("&")
		}
		fmt.Fprintf(&q, "records[%d]=r%d", i, i)
	}
	r := httptest.NewRequest(http.MethodGet, "http://test/api/?token=t&content=record&"+q.String(), nil)
	p, err := ParseParams(r)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if len(p.Records) != 12_000 {
		t.Fatalf("Records = %d, want 12000", len(p.Records))
	}
	if p.Records[0] != "r0" || p.Records[11_999] != "r11999" {
		t.Errorf("record order broken: first %q, last %q", p.Records[0], p.Records[len(p.Records)-1])
	}
}

// Past maxFormParams the request is refused — but as a 400 naming the limit,
// never as an empty parameter set that reads back as an invalid token.
func TestParameterCapReported(t *testing.T) {
	body := "token=t&content=project&" + strings.Repeat("&", maxFormParams)
	if _, err := ParseParams(postBody(body)); !errors.Is(err, errTooManyParams) {
		t.Fatalf("err = %v, want errTooManyParams", err)
	}

	r := httptest.NewRequest(http.MethodGet, "http://test/api/?"+strings.Repeat("&", maxFormParams), nil)
	if _, err := ParseParams(r); !errors.Is(err, errTooManyParams) {
		t.Fatalf("query err = %v, want errTooManyParams", err)
	}
}

// A missing or unexpected Content-Type still reads as urlencoded — the
// tolerance the old body-reread fallback existed for (PHP callers omit it).
func TestMissingContentTypeStillParses(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://test/api/",
		strings.NewReader("token=t&content=project"))
	p, err := ParseParams(r)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if p.Token != "t" || p.Content != "project" {
		t.Errorf("got %+v, want the body parsed despite the missing Content-Type", p)
	}
}

// multipart/form-data is not this surface's encoding (API_Endpoints_Design.md
// §3.1). It used to arrive as empty parameters and a 401; now it says so.
func TestMultipartRejectedWithAMeaningfulError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://test/api/",
		strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"token\"\r\n\r\nt\r\n--x--\r\n"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	_, err := ParseParams(r)
	if !errors.Is(err, errUnsupportedEncoding) {
		t.Fatalf("err = %v, want errUnsupportedEncoding", err)
	}
	if msg := paramsErrorMessage(err); strings.Contains(msg, "token\"") || strings.Contains(msg, "Invalid token") {
		t.Errorf("message reads as an auth failure: %q", msg)
	}
}

// ParseParams publishes the parsed body as r.PostForm so that code reaching for
// the net/http field afterwards sees the same values instead of re-parsing a
// consumed body — the trap that produced this bug.
func TestPostFormPublishedOnRequest(t *testing.T) {
	r := postBody("token=t&content=record&action=import&data[0][record_id]=8DISC020")
	if _, err := ParseParams(r); err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if got := r.PostForm.Get("token"); got != "t" {
		t.Errorf("r.PostForm[token] = %q, want t", got)
	}
	if got := r.PostForm.Get("data[0][record_id]"); got != "8DISC020" {
		t.Errorf("r.PostForm indexed key = %q, want 8DISC020", got)
	}
	if err := r.ParseForm(); err != nil {
		t.Errorf("ParseForm after ours: %v", err)
	}
	if got := r.FormValue("token"); got != "t" {
		t.Errorf("r.FormValue(token) = %q, want t", got)
	}
}

// parseFormBody must agree with url.ParseQuery on every input the stdlib
// accepts — and refuse the same slips — since it replaces it verbatim.
func TestParseFormBodyParityWithNetURL(t *testing.T) {
	for _, in := range []string{
		"", "a=1", "a=1&a=2&a=3", "token=t&content=project",
		"data%5B0%5D%5Bage%5D=7&data[1][age]=8", // encoded and literal brackets
		"a+b=c+d", "a&b=1", "&&a=1&&", "=2", "%61=1", "a=1&a=",
		"a=%C3%A5", "a=x%2By", "a=%zz", "a=1%2",
		"a=1;b=2", "a=%zz&b=1;c=2", // slips: same first/last error as net/url
	} {
		want, wantErr := url.ParseQuery(in)
		got, gotErr := parseFormBody([]byte(in))
		if !reflect.DeepEqual(map[string][]string(want), map[string][]string(got)) {
			t.Errorf("parse(%q) = %#v, want %#v", in, got, want)
		}
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) {
			t.Errorf("parse(%q) err = %v, want %v", in, gotErr, wantErr)
		}
	}
}

func TestParamsErrorMessages(t *testing.T) {
	cases := []struct {
		err  error
		want []string
	}{
		{errTooManyParams, []string{"Too many parameters", "REQ-API-031"}},
		{errFormTooLarge, []string{"body too large", "bytes"}},
		{errUnsupportedEncoding, []string{"x-www-form-urlencoded"}},
		{errors.New("something else"), []string{"Invalid content"}},
	}
	for _, tc := range cases {
		msg := paramsErrorMessage(tc.err)
		for _, want := range tc.want {
			if !strings.Contains(msg, want) {
				t.Errorf("paramsErrorMessage(%v) = %q, want it to mention %q", tc.err, msg, want)
			}
		}
		if strings.Contains(msg, "Invalid token") {
			t.Errorf("a request-shape error must not read as an auth failure: %q", msg)
		}
	}
}

// End to end on the real handler: a valid import carrying enough unknown
// parameters to pass 10,000 used to answer 401 "Invalid token" (unknown
// parameters are accepted and dropped, REQ-API-017, so nothing about the call
// is wrong but its size).
func TestLargeIndexedSendIsNotAnAuthFailure(t *testing.T) {
	f := newRecordFixture(t)

	var b strings.Builder
	b.WriteString("token=tok-edit&content=record&action=import&returnFormat=json")
	b.WriteString("&data[0][record_id]=8DISC020&data[0][form_name]=demo")
	b.WriteString("&data[0][event_name]=baseline_arm_1&data[0][age]=44")
	for i := 0; i < 10_005; i++ {
		fmt.Fprintf(&b, "&junk_%d=x", i)
	}
	code, body := postRaw(t, f.h, b.String())
	if code == http.StatusUnauthorized {
		t.Fatalf("oversized-but-valid send answered 401: %s", body)
	}
	mustStatus(t, code, http.StatusOK, body)
	if got := storedValue(t, f, "8DISC020", "baseline_arm_1", "age"); got != "44" {
		t.Errorf("stored age = %q, want 44 — the row must import, not just parse", got)
	}
}

// And past maxFormParams the same caller gets a 400 that names the limit and
// the JSON encoding to use instead.
func TestOversizedSendNamesTheLimit(t *testing.T) {
	f := newRecordFixture(t)
	code, body := postRaw(t, f.h, "token=tok-edit&content=project&"+strings.Repeat("&", maxFormParams))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", code, body)
	}
	if !strings.Contains(body, "Too many parameters") {
		t.Errorf("body = %q, want the parameter limit named", body)
	}
	if strings.Contains(body, "Invalid token") {
		t.Errorf("body = %q, must not read as an auth failure", body)
	}
}
