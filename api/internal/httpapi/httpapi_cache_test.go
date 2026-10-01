package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// structureInvalidation bumps the store's structure generation exactly for
// successful non-GET administration writes under a structure prefix — the
// designer flows that write arms, events, instruments, fields and the
// instrument-event mapping with raw SQL inside their own transactions,
// where the db.Store bump hooks cannot reach (httpapi.go). The probe stands
// in for the administration mux; its status is what the middleware gates on.
func TestStructureInvalidation(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		method, path string
		status       int
		wantBump     bool
	}{
		{http.MethodPost, "/api/v1/projects/7/instruments", http.StatusCreated, true},
		{http.MethodPut, "/api/v1/events/3", http.StatusOK, true},
		{http.MethodDelete, "/api/v1/arms/2", http.StatusNoContent, true},
		{http.MethodPut, "/api/v1/projects/7/instrument-event-mapping", http.StatusOK, true},
		{http.MethodPost, "/api/v1/projects/7/staging/commit", http.StatusOK, true},
		{http.MethodGet, "/api/v1/projects/7/instruments", http.StatusOK, false},
		{http.MethodGet, "/api/v1/events/3", http.StatusOK, false},
		{http.MethodPost, "/api/v1/users", http.StatusCreated, false}, // outside the structure prefixes
		{http.MethodPut, "/api/v1/projects/7/mode", http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		status := tc.status
		probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
		before := e.Store.StructureGeneration()
		rec := serve(t, structureInvalidation(e.Store, probe), httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s %s: status = %d, want %d (pass-through)", tc.method, tc.path, rec.Code, tc.status)
		}
		moved := e.Store.StructureGeneration() != before
		if moved != tc.wantBump {
			t.Errorf("%s %s → %d: bumped = %v, want %v", tc.method, tc.path, tc.status, moved, tc.wantBump)
		}
	}
}
