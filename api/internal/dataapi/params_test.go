package dataapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// parseFrom builds a request whose parameters are either in the query string
// (GET) or the form body (POST), then runs ParseParams.
func parseFrom(t *testing.T, method string, form url.Values) Params {
	t.Helper()
	var r *http.Request
	if method == http.MethodPost {
		r = httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(http.MethodGet, "http://test/api/?"+form.Encode(), nil)
	}
	p, err := ParseParams(r)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	return p
}

func TestParseParamsGet(t *testing.T) {
	p := parseFrom(t, http.MethodGet, url.Values{
		"token": {"t"}, "content": {"project"}, "format": {"json"},
	})
	if p.Token != "t" || p.Content != "project" || p.Format != "json" {
		t.Fatalf("got %+v", p)
	}
}

// Body values take precedence over the query string (REQ-API-009, REQ-API-010):
// the token travels in the body.
func TestParseParamsBodyPrecedence(t *testing.T) {
	// The query string carries one value, the body another; the body wins.
	p, err := ParseParams(func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://test/api/?token=query-tok&content=project",
			strings.NewReader("token=body-tok"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}())
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}
	if p.Token != "body-tok" {
		t.Errorf("token = %q, want body-tok (body wins over query)", p.Token)
	}
	if p.Content != "project" {
		t.Errorf("content = %q, want project (from query, absent in body)", p.Content)
	}
}

// The REDCap indexed list syntax records[0]=… records[1]=… is collected in
// index order, even when the form delivers them out of order (REQ-API-015).
func TestParseParamsIndexedOrder(t *testing.T) {
	// Deliberately encoded in reverse index order to prove ordering by index.
	p := parseFrom(t, http.MethodPost, url.Values{
		"records[2]": {"c"},
		"records[0]": {"a"},
		"records[1]": {"b"},
	})
	if len(p.Records) != 3 || p.Records[0] != "a" || p.Records[1] != "b" || p.Records[2] != "c" {
		t.Fatalf("Records = %v, want [a b c]", p.Records)
	}
}

// Repeated single-valued keys (fields=a&fields=b) are also collected.
func TestParseParamsRepeatedValues(t *testing.T) {
	p := parseFrom(t, http.MethodGet, url.Values{
		"fields": {"age", "sex"},
	})
	if len(p.Fields) != 2 || p.Fields[0] != "age" || p.Fields[1] != "sex" {
		t.Fatalf("Fields = %v, want [age sex]", p.Fields)
	}
}

// An oversized body is rejected rather than read in full (REQ-TECH-011): the
// cap now holds on the primary ParseForm path, not just the fallback. This
// also exercises http.MaxBytesReader with a nil ResponseWriter.
func TestOversizedBodyRejected(t *testing.T) {
	big := strings.Repeat("a", maxFormBytes+1)
	req := httptest.NewRequest(http.MethodPost, "http://test/api/", strings.NewReader("token=t&"+big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := ParseParams(req); err == nil {
		t.Fatal("want an error for a body over maxFormBytes")
	}
}

// An index list and a bare repeated key are distinct; the indexed form wins
// when both are present (indexed syntax is checked first in list()).
func TestParseParamsIndexedWinsOverRepeated(t *testing.T) {
	p := parseFrom(t, http.MethodPost, url.Values{
		"events[0]": {"baseline_arm_1"},
		"events":    {"ignored"},
	})
	if len(p.Events) != 1 || p.Events[0] != "baseline_arm_1" {
		t.Fatalf("Events = %v, want [baseline_arm_1]", p.Events)
	}
}

// Encoding precedence (REQ-API-013): returnFormat, then format, then the
// REDCap default csv. "json" is matched case-insensitively.
func TestEncoding(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		want string
	}{
		{"default is csv", Params{}, "csv"},
		{"format json", Params{Format: "json"}, "json"},
		{"format is case-insensitive", Params{Format: "JSON"}, "json"},
		{"returnFormat wins over format", Params{Format: "csv", ReturnFormat: "json"}, "json"},
		{"unknown format falls back to csv", Params{Format: "xml"}, "csv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Encoding(); got != tc.want {
				t.Errorf("Encoding() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Delimiter is a single character; empty means the default comma
// (REQ-API-029).
func TestDelimiter(t *testing.T) {
	if got := (Params{}).Delimiter(); got != ',' {
		t.Errorf("empty delimiter = %q, want ,", got)
	}
	if got := (Params{CSVDelimiter: "|"}).Delimiter(); got != '|' {
		t.Errorf("pipe delimiter = %q, want |", got)
	}
	// Only the first rune is honored.
	if got := (Params{CSVDelimiter: ";;"}).Delimiter(); got != ';' {
		t.Errorf("multi-char delimiter = %q, want ;", got)
	}
}
