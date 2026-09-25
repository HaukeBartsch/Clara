// Package dataapi implements the REDCap-compatible data API: the single
// POST /api/ endpoint (and GET /api/) speaking the form-encoded
// token/content protocol of API_Endpoints_Design.md §3. This slice covers
// the read-only contents — project, event, metadata, formEventMapping,
// exportFieldNames, generateNextRecordName — plus the uniform error and
// rate-limit contracts they share.
package dataapi

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxFormBytes bounds the form body — import payloads are the large ones
// (REQ-TECH-011), 32 MiB is generous for any single call.
const maxFormBytes = 32 << 20

// Params is the parsed data-API request (API_Endpoints_Design.md §3.1).
// Unknown parameters are accepted and dropped — existing callers keep
// working (REQ-API-017).
type Params struct {
	Token             string
	Content           string
	Action            string
	Format            string
	ReturnFormat      string
	Type              string
	CSVDelimiter      string
	Records           []string
	Fields            []string
	Forms             []string
	Events            []string
	FilterLogic       string
	RawOrLabel        string
	RawOrLabelHeaders string
	TZ                string
}

// ParseParams extracts the protocol parameters from a GET query string or
// a POST form body. Body values take precedence over the query string —
// the token travels in the body (REQ-API-009, REQ-API-010).
func ParseParams(r *http.Request) (Params, error) {
	query := map[string][]string{}
	if r.URL != nil {
		for k, vs := range r.URL.Query() {
			query[k] = vs
		}
	}
	body := map[string][]string{}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		b, err := formValues(r)
		if err != nil {
			return Params{}, err
		}
		body = b
	}

	single := func(key string) string {
		if vs := body[key]; len(vs) > 0 {
			return vs[0]
		}
		if vs := query[key]; len(vs) > 0 {
			return vs[0]
		}
		return ""
	}
	list := func(key string) []string {
		// REDCap indexed syntax (records[0]=…, records[1]=…) first,
		// then repeated single values (REQ-API-015).
		if vs := indexed(body, key); len(vs) > 0 {
			return vs
		}
		if vs := indexed(query, key); len(vs) > 0 {
			return vs
		}
		if vs := body[key]; len(vs) > 0 {
			return vs
		}
		if vs := query[key]; len(vs) > 0 {
			return vs
		}
		return nil
	}

	return Params{
		Token:             single("token"),
		Content:           single("content"),
		Action:            single("action"),
		Format:            single("format"),
		ReturnFormat:      single("returnFormat"),
		Type:              single("type"),
		CSVDelimiter:      single("csvDelimiter"),
		Records:           list("records"),
		Fields:            list("fields"),
		Forms:             list("forms"),
		Events:            list("events"),
		FilterLogic:       single("filterLogic"),
		RawOrLabel:        single("rawOrLabel"),
		RawOrLabelHeaders: single("rawOrLabelHeaders"),
		TZ:                single("tz"),
	}, nil
}

// formValues parses the POST body as urlencoded, tolerating a missing or
// unexpected Content-Type header (PHP callers occasionally omit it).
func formValues(r *http.Request) (map[string][]string, error) {
	if err := r.ParseForm(); err == nil {
		return r.PostForm, nil
	}
	if r.Body == nil {
		return map[string][]string{}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxFormBytes))
	if err != nil {
		return nil, err
	}
	v, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Encoding is the response encoding (REQ-API-013): a present
// returnFormat wins, then format, then the REDCap default csv.
func (p Params) Encoding() string {
	e := p.ReturnFormat
	if e == "" {
		e = p.Format
	}
	if strings.EqualFold(e, "json") {
		return "json"
	}
	return "csv"
}

// Delimiter is the CSV column separator (REQ-API-029): a single
// character; empty means comma.
func (p Params) Delimiter() rune {
	if p.CSVDelimiter == "" {
		return ','
	}
	return []rune(p.CSVDelimiter)[0]
}

var indexedKeyRE = regexp.MustCompile(`^(.*)\[(\d+)\]$`)

// indexed collects the values of key[0], key[1], … in index order
// (REQ-API-015).
func indexed(m map[string][]string, key string) []string {
	type pair struct {
		idx  int
		vals []string
	}
	var found []pair
	for k, vs := range m {
		g := indexedKeyRE.FindStringSubmatch(k)
		if g == nil || g[1] != key {
			continue
		}
		n, _ := strconv.Atoi(g[2])
		found = append(found, pair{n, vs})
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].idx < found[j].idx })
	var out []string
	for _, p := range found {
		out = append(out, p.vals...)
	}
	return out
}
