package openapi

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// routePattern matches a registration such as
// mux.HandleFunc("PUT /api/v1/projects/{id}/mode", h.putMode).
var routePattern = regexp.MustCompile(`\.Handle(?:Func)?\("(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^"]*)"`)

// pathParam folds every path parameter to one token, so `{id}` in the code and
// `{pid}` in the document still name the same route.
var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// TestEveryAdminRouteDocumented keeps the hand-maintained document in step with
// the routes the API actually serves (REQ-TECH-004, REQ-API-002): a route
// registered under /api/v1/ without an operation here fails the build.
func TestEveryAdminRouteDocumented(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(Spec, &doc); err != nil {
		t.Fatalf("openapi.json does not parse: %v", err)
	}
	documented := map[string]bool{}
	for path, ops := range doc.Paths {
		for method := range ops {
			documented[strings.ToUpper(method)+" "+pathParam.ReplaceAllString(path, "{}")] = true
		}
	}

	registered := map[string]string{}
	err := filepath.WalkDir("../internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range routePattern.FindAllStringSubmatch(string(src), -1) {
			registered[m[1]+" "+pathParam.ReplaceAllString(m[2], "{}")] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning route registrations: %v", err)
	}
	if len(registered) == 0 {
		t.Fatal("no /api/v1/ route registrations found — the scan pattern no longer matches the code")
	}

	var missing []string
	for route, file := range registered {
		if !documented[route] {
			missing = append(missing, route+"  ("+file+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("route registered but absent from openapi.json: %s", m)
	}
}
