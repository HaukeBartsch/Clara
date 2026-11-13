// Package dataapi implements the REDCap-compatible data API: the single
// POST /api/ endpoint speaking the form-encoded token/content protocol of
// API_Endpoints_Design.md §3 — the read-only
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
	Token            string
	Content          string
	Action           string
	Format           string
	ReturnFormat     string
	Type             string
	CSVDelimiter     string
	DecimalCharacter string
	Records          []string
	Fields           []string
	Forms            []string
	Events           []string
	FilterLogic      string
	// DateRangeBegin and DateRangeEnd are REDCap's change window for the record
	// export (REQ-API-181), as sent; parseChangeWindow reads them.
	DateRangeBegin    string
	DateRangeEnd      string
	RawOrLabel        string
	RawOrLabelHeaders string
	// ExportCheckboxLabel is REDCap's flag for checkbox values in label mode
	// (REQ-EXP-053); "true" selects the choice label.
	ExportCheckboxLabel string
	TZ                  string
	Data                string // import payload as a JSON array (§3.7.1, REQ-API-031)
	ReturnContent       string // "count" switches the import response (REQ-API-142)
	OverwriteBehavior   string // "normal" keeps stored values under empty cells (REQ-VAL-024, DEV-API-43)
	// RepeatInstance narrows a delete to one form instance (REDCap's
	// repeat_instance, REQ-API-155); "" = every instance.
	RepeatInstance string

	// HasData reports a `data` parameter in any encoding; for
	// content=record its presence decides import vs export (REQ-API-012).
	HasData bool
}

// ParseParams extracts the protocol parameters from a POST form body. Only the
// parameters read below are interpreted; exportSurveyFields,
// exportDataAccessGroups and every unknown name are accepted and ignored, never
// rejected (REQ-EXP-014, REQ-API-016). Other
// parameters may also arrive in the query string, which some callers append —
// the token never does (REQ-API-009, REQ-API-010).
//
// A returned error is always a request-shape failure (form.go): an oversized
// body, a request over maxFormParams, an unsupported encoding, or a token riding
// in the query string. Callers answer it as the uniform 400 with
// paramsErrorMessage, before any token lookup, so that none of them can read
// back as an invalid-token 401.
func ParseParams(r *http.Request) (Params, error) {
	query, err := queryValues(r)
	if err != nil {
		return Params{}, err
	}
	// A credential in a URL is a credential in an access log, so this shape is
	// refused outright rather than ignoring a value that would then fail as an
	// invalid token (master-spec finding F19, REQ-API-010, DEV-API-30). Checked
	// before the body: the request cannot succeed either way, and this is its
	// reason — and one that tells the caller how to fix it.
	if len(query["token"]) > 0 {
		return Params{}, errTokenInQuery
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
	// bodyOnly is single without the query-string fallback. It exists for one
	// parameter on purpose: the asymmetry is the fix, and a future edit that
	// folds `token` back into single() would reopen finding F19 silently.
	bodyOnly := func(key string) string {
		if vs := body[key]; len(vs) > 0 {
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
		Token:               bodyOnly("token"),
		Content:             single("content"),
		Action:              single("action"),
		Format:              single("format"),
		ReturnFormat:        single("returnFormat"),
		Type:                single("type"),
		CSVDelimiter:        single("csvDelimiter"),
		DecimalCharacter:    single("decimalCharacter"),
		Records:             list("records"),
		Fields:              list("fields"),
		Forms:               list("forms"),
		Events:              list("events"),
		FilterLogic:         single("filterLogic"),
		DateRangeBegin:      single("dateRangeBegin"),
		DateRangeEnd:        single("dateRangeEnd"),
		RawOrLabel:          single("rawOrLabel"),
		RawOrLabelHeaders:   single("rawOrLabelHeaders"),
		ExportCheckboxLabel: single("exportCheckboxLabel"),
		TZ:                  single("tz"),
		Data:                single("data"),
		ReturnContent:       single("returnContent"),
		OverwriteBehavior:   single("overwriteBehavior"),
		RepeatInstance:      single("repeat_instance"),
		HasData:             hasDataKey(body) || hasDataKey(query),
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

// redcapFormName is REDCap's unique instrument name for an instrument name as
// this system stores it: lower case, every space an underscore. REDCap derives
// that name from the instrument label and puts it in `forms[]`, while this
// system stores one name that carries the label's capitals and spaces
// (`incoming_studies` names the instrument "Incoming Studies" — FIONA's
// recorded calls, REQ-API-037), so a forms[] entry is read in both spellings.
// Instrument names are ASCII letters, digits, spaces and underscores (REQ-DB-011),
// which those two rules cover on their own.
func redcapFormName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

// formSet is a forms[] filter: nil for no filter — every instrument — and
// otherwise the names one call selects (REQ-API-015, REQ-API-022/024). Each
// entry selects an instrument by its stored name and by REDCap's spelling of it,
// in either direction, so `forms[]=incoming_studies` narrows a render to the
// instrument "Incoming Studies" and the recorded exact spellings keep matching.
// Both spellings select, so a project holding both an instrument "A B" and one
// literally named "a_b" is selected by either entry: forms[] governs selection
// only, never what the surviving columns carry, which stays the export level's
// question (REQ-API-024, REQ-EXP-004).
type formSet map[string]bool

func toFormSet(names []string) formSet {
	if len(names) == 0 {
		return nil
	}
	set := make(formSet, 2*len(names))
	for _, n := range names {
		set[n] = true
		set[redcapFormName(n)] = true
	}
	return set
}

// has reports whether the instrument named name is selected. A nil filter
// selects every instrument.
func (s formSet) has(name string) bool {
	return s == nil || s[name] || s[redcapFormName(name)]
}

// Encoding is the response encoding (REQ-API-013): a present
// returnFormat wins, then format, then the REDCap default csv. The data API
// has no ODM encoding — `odm` falls back to csv like any unknown value; ODM
// lives on the administration API only (REQ-ODM-003).
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
