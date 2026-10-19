package dataapi

// Finding F19, closed by REQ-API-010 and DEV-API-30: the credential travels in the
// request body, because a URL is what proxy and server access logs keep — and those
// outlive the token. The requirement is one-directional, so these tests are too: a
// token read back out of somebody's log line must buy nothing, while the body shape
// every recorded caller already sends keeps working untouched.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postQuery posts with its parameters in the query string and an empty body: the
// shape a not-yet-migrated caller sends, and exactly what an access log line holds.
func postQuery(t *testing.T, h *Handler, query string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://test/api/?"+query, nil))
	return rec.Code, rec.Body.String()
}

// TestValidTokenInTheQueryStringAuthenticatesNothing is the finding itself: this is
// the seeded project's real token, presented where a log would have kept it.
func TestValidTokenInTheQueryStringAuthenticatesNothing(t *testing.T) {
	f := newFionaFixture(t)

	code, body := postQuery(t, f.h, "token="+fionaToken+"&content=project&format=json")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a query-string credential must not authorise (body %q)", code, body)
	}
	if strings.Contains(body, "DataTransferProjects") {
		t.Errorf("the call read project data through a query-string token: %s", body)
	}
	if !strings.Contains(body, "request body") {
		t.Errorf("body %q should name the request body as the fix", body)
	}
	if strings.Contains(body, fionaToken) {
		t.Errorf("the refusal echoes the credential it just refused: %s", body)
	}
}

// TestTokenInBodyStillAuthorizes guards against over-tightening: token in a
// form-encoded body is the shape of every recorded caller (web/app/DataApi.php,
// stress_api.py, FIONA's getProjects.php) and must answer exactly as before.
func TestTokenInBodyStillAuthorizes(t *testing.T) {
	f := newFionaFixture(t)

	code, body := postRaw(t, f.h, "token="+fionaToken+"&content=project&format=json&returnFormat=json")
	mustStatus(t, code, http.StatusOK, body)
	if !strings.Contains(body, "DataTransferProjects") {
		t.Errorf("body %q should carry the seeded project", body)
	}
}

// TestGetApiIsRefused checks that the retired method fails as a method problem with
// the fix named — not as `Invalid token`, which would send a caller looking at its
// credentials for a request shape that was never going to authenticate.
func TestGetApiIsRefused(t *testing.T) {
	f := newFionaFixture(t)

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"http://test/api/?token="+fionaToken+"&content=project", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body %q)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "POST /api/") {
		t.Errorf("body %q should name POST /api/ as the way in", body)
	}
	if strings.Contains(body, "Invalid token") {
		t.Errorf("a retired method must not read as a credential failure: %s", body)
	}
	if strings.Contains(body, fionaToken) {
		t.Errorf("the refusal echoes the credential: %s", body)
	}
}
