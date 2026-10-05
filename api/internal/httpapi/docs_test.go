package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The documentation endpoints serve the embedded OpenAPI 3.1 document and the
// vendored Swagger UI bundle (API_Endpoints_Design.md §2.2). They are public
// in Go — nginx keeps them off the public network — so the tests check the
// payload, not an authentication outcome.

func TestOpenAPIJSONEndpoint(t *testing.T) {
	e := newEnv(t)
	mux := NewMux(e.Store, e.Cfg, e.Audit)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want 200 (body %.200s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var doc struct {
		OpenAPI string                     `json:"openapi"`
		Info    struct{ Title string }     `json:"info"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("spec does not parse as JSON: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi version = %q, want 3.1.x", doc.OpenAPI)
	}
	if doc.Info.Title == "" {
		t.Error("info.title is empty")
	}
	for _, p := range []string{"/healthz", "/openapi.json", "/docs", "/api/", "/api/v1/auth/login"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("spec is missing path %s", p)
		}
	}
	if len(doc.Paths) < 50 {
		t.Errorf("spec has %d paths, want the full endpoint table (≥50)", len(doc.Paths))
	}
}

func TestDocsUIEndpoint(t *testing.T) {
	e := newEnv(t)
	mux := NewMux(e.Store, e.Cfg, e.Audit)

	for _, path := range []string{"/docs", "/docs/"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", path, ct)
		}
		body := rec.Body.String()
		for _, want := range []string{"/docs/swaggerui/swagger-ui.css", "/docs/swaggerui/swagger-ui-bundle.js", "SwaggerUIBundle", `url: '/openapi.json'`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s page is missing %q", path, want)
			}
		}
	}
}

func TestDocsAssetsServed(t *testing.T) {
	e := newEnv(t)
	mux := NewMux(e.Store, e.Cfg, e.Audit)

	cases := []struct {
		path        string
		contentType string // prefix; empty means any
	}{
		{"/docs/swaggerui/swagger-ui.css", "text/css"},
		{"/docs/swaggerui/swagger-ui-bundle.js", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", c.path, rec.Code)
		}
		if body, _ := io.ReadAll(rec.Body); len(body) == 0 {
			t.Errorf("GET %s returned an empty body", c.path)
		}
		if c.contentType != "" {
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.contentType) {
				t.Errorf("GET %s Content-Type = %q, want prefix %q", c.path, ct, c.contentType)
			}
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/swaggerui/not-there.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET missing asset status = %d, want 404", rec.Code)
	}
}
