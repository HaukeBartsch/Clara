// Package dataapi implements the REDCap-compatible data API: the single
// POST /api/ endpoint (and GET /api/) speaking the form-encoded
// token/content protocol of API_Endpoints_Design.md §3 — the read-only
// contents (project, event, metadata, formEventMapping, exportFieldNames,
// generateNextRecordName), the record actions export/import/delete
// (§3.6–§3.8), and the uniform error and rate-limit contracts they share.
package dataapi

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

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
	DecimalCharacter  string
	Records           []string
	Fields            []string
	Forms             []string
	Events            []string
	FilterLogic       string
	RawOrLabel        string
	RawOrLabelHeaders string
	TZ                string
	Data              string // import payload as a JSON array (§3.7.1, REQ-API-031)
	ReturnContent     string // "count" switches the import response (REQ-API-142)

	// HasData reports a `data` parameter in any encoding; for
	// content=record its presence decides import vs export (REQ-API-012).
	HasData bool
}

// ParseParams extracts the protocol parameters from a GET query string or
// a POST form body. Body values take precedence over the query string —
// the token travels in the body (REQ-API-009, REQ-API-010).
//
// A returned error is always a request-shape failure (form.go): an oversized
// body, a request over maxFormParams, an unsupported encoding. Callers answer
// it as the uniform 400 with paramsErrorMessage, before any token lookup, so
// that none of them can read back as an invalid-token 401.
func ParseParams(r *http.Request) (Params, error) {
	query, err := queryValues(r)
	if err != nil {
		return Params{}, err
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
		DecimalCharacter:  single("decimalCharacter"),
		Records:           list("records"),
		Fields:            list("fields"),
		Forms:             list("forms"),
		Events:            list("events"),
		FilterLogic:       single("filterLogic"),
		RawOrLabel:        single("rawOrLabel"),
		RawOrLabelHeaders: single("rawOrLabelHeaders"),
		TZ:                single("tz"),
		Data:              single("data"),
		ReturnContent:     single("returnContent"),
		HasData:           hasDataKey(body) || hasDataKey(query),
	}, nil
}

// hasDataKey reports a `data` parameter in either encoding — the JSON-array
// spelling (`data`) or indexed rows (`data[0][…]`). Its presence decides
// import vs export for content=record (REQ-API-012).
func hasDataKey(m map[string][]string) bool {
	for k := range m {
		if k == "data" || strings.HasPrefix(k, "data[") {
			return true
		}
	}
	return false
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

// Delimiter is the CSV column separator (REQ-API-029, REQ-EXP-013). ServeHTTP
// has refused a value outside the set, so the comma fallback never decides.
func (p Params) Delimiter() rune {
	d, _ := ParseDelimiter(p.CSVDelimiter)
	return d
}

// Decimal is the record export's decimal character (REQ-EXP-015); 0 keeps the
// stored form. ServeHTTP has refused a value outside the set.
func (p Params) Decimal() byte {
	d, _ := ParseDecimal(p.DecimalCharacter)
	return d
}

// ParseDelimiter reads a csvDelimiter value against REDCap's set
// (REQ-EXP-013): comma — also for an empty value — semicolon, pipe, caret,
// and tab, given as the character or by REDCap's name "tab". Anything else is
// not ok: a double quote, CR and LF cannot delimit a CSV field, and a longer
// value is never cut down to its first character. Both export surfaces read it.
func ParseDelimiter(raw string) (rune, bool) {
	switch raw {
	case "", ",":
		return ',', true
	case ";", "|", "^":
		return rune(raw[0]), true
	case "\t", "tab":
		return '\t', true
	}
	return ',', false
}

// ParseDecimal reads a decimalCharacter value (REQ-EXP-015): empty keeps the
// stored form (0), "." or "," names the separator; anything else is not ok.
func ParseDecimal(raw string) (byte, bool) {
	switch raw {
	case "":
		return 0, true
	case ".", ",":
		return raw[0], true
	}
	return 0, false
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
