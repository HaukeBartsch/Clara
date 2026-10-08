package dataapi

// Request-body parsing for the data API (API_Endpoints_Design.md §3.1).
//
// Go 1.27 gave net/url a hard cap of 10,000 parameters per urlencoded string
// (net/url.defaultMaxParams), and REDCap's indexed encoding runs into it long
// before the byte limit does: one import batch of 50 rows × 200 fields is
// 10,004 parameters. url.ParseQuery then fails outright — after having read
// the body, which parsePostForm never hands back — so a caller that ignores
// the error and re-reads r.Body finds an empty parameter set and no error at
// all. That is how a valid token came to answer 401 "Invalid token": the token
// was dropped with every other parameter, and an empty token is deliberately
// indistinguishable from a wrong one (REQ-AUTH-032).
//
// The body is therefore parsed here: url.ParseQuery's acceptance rules, no
// parameter cap, and every failure returned rather than swallowed. What bounds
// a request instead is maxFormBytes (REQ-TECH-011) and maxFormParams below —
// both answered 400 with the limit named, before any token lookup, so a batch
// that is merely too large never reads as an authentication failure.

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// maxFormBytes bounds the form body — import payloads are the large ones
// (REQ-TECH-011), 32 MiB is generous for any single call.
const maxFormBytes = 32 << 20

// maxFormParams bounds the parameter count of one request, replacing net/url's
// fixed 10,000 with a limit that clears a realistic indexed batch: 250,000
// parameters is a 1,250-row × 200-field import (about 7 MB of body) and holds
// worst-case map growth to a few tens of megabytes even for a body made of
// bare `&` separators. A larger batch belongs in the JSON data encoding
// (REQ-API-031), which carries the same rows in a single parameter.
const maxFormParams = 250_000

// Request-shape failures of the parser. Each is a client error answered before
// authentication; none may be reported as an invalid token.
var (
	errFormTooLarge        = errors.New("form body exceeds maxFormBytes")
	errTooManyParams       = errors.New("request carries more parameters than maxFormParams")
	errUnsupportedEncoding = errors.New("request encoding is not supported")
)

// formValues parses the POST body as urlencoded, tolerating a missing or
// unexpected Content-Type header (PHP callers occasionally omit it). The
// result is also published as r.PostForm, so anything that reaches for the
// net/http field afterwards — here or in later code — reads the same values
// instead of re-parsing a body that is already consumed.
func formValues(r *http.Request) (map[string][]string, error) {
	if isMultipart(r) {
		return nil, errUnsupportedEncoding
	}
	raw, err := readFormBody(r)
	if err != nil {
		return nil, err
	}
	values, err := parseFormBody(raw)
	if err != nil {
		return nil, err
	}
	r.PostForm = url.Values(values)
	return values, nil
}

// isMultipart reports a multipart/form-data request. This surface speaks
// urlencoded only (API_Endpoints_Design.md §3.1); the name is checked so that
// one arrives as an explicit "unsupported encoding" rather than as empty
// parameters and a misleading 401. A malformed Content-Type is not multipart
// and falls through to the urlencoded reading, which is what the tolerance in
// formValues is for.
func isMultipart(r *http.Request) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && ct == "multipart/form-data"
}

// readFormBody reads the request body under maxFormBytes. The extra byte is
// what distinguishes a body at the cap from one over it, so the limit is an
// error rather than a silent truncation (REQ-TECH-011).
func readFormBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxFormBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxFormBytes {
		return nil, errFormTooLarge
	}
	return raw, nil
}

// parseFormBody parses an urlencoded parameter string — a request body or a
// query string — under the same rules net/url applies, without its 10,000
// parameter cap. Segments are `&`-separated; an empty segment is skipped, a
// segment holding a bare `;` or an invalid escape is skipped and reported, and
// a segment without `=` sets an empty value. The map is partial when an error
// is returned, exactly as url.ParseQuery leaves it.
//
// The error choice copies net/url rather than improving on it: a `;` slip
// replaces whatever the parse had recorded so far, while a decode slip keeps
// the first one. Both are the uniform 400 either way, and keeping the copy
// exact is what lets form_test.go diff this against url.ParseQuery.
func parseFormBody(raw []byte) (map[string][]string, error) {
	s := string(raw)
	if s == "" {
		return map[string][]string{}, nil
	}
	if n := strings.Count(s, "&") + 1; n > maxFormParams {
		return nil, errTooManyParams
	}
	values := map[string][]string{}
	var parseErr error
	for s != "" {
		var segment string
		segment, s, _ = strings.Cut(s, "&")
		if strings.Contains(segment, ";") {
			parseErr = errors.New("invalid semicolon separator in query")
			continue
		}
		if segment == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(segment, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		values[key] = append(values[key], value)
	}
	return values, parseErr
}

// queryValues returns the URL query parameters without net/url's parameter cap.
// Decoding slips stay tolerated, as r.URL.Query() tolerates them; only a query
// over maxFormParams is reported, so it cannot come back as empty parameters.
func queryValues(r *http.Request) (map[string][]string, error) {
	if r.URL == nil || r.URL.RawQuery == "" {
		return map[string][]string{}, nil
	}
	values, err := parseFormBody([]byte(r.URL.RawQuery))
	if errors.Is(err, errTooManyParams) {
		return nil, err
	}
	return values, nil
}

// paramsErrorMessage is the client-facing text for a request-shape failure
// (handler.go answers it as the uniform 400). Naming the limit and the way
// around it is the point: an oversized batch used to arrive as "Invalid
// token", which sent callers off to rotate a token that was never the
// problem. The uniform 401 for tokens themselves is REQ-AUTH-032 and stays.
func paramsErrorMessage(err error) string {
	switch {
	case errors.Is(err, errTooManyParams):
		return fmt.Sprintf("Too many parameters: a request carries at most %d; send a large batch as the JSON data encoding (REQ-API-031)", maxFormParams)
	case errors.Is(err, errFormTooLarge):
		return fmt.Sprintf("Request body too large: the limit is %d bytes", maxFormBytes)
	case errors.Is(err, errUnsupportedEncoding):
		return "Unsupported Content-Type: send application/x-www-form-urlencoded"
	default:
		return "Invalid content"
	}
}
