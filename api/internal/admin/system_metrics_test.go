package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"csms/api/internal/db"
	"csms/api/internal/metrics"
	"csms/api/internal/testdb"
)

// GET /api/v1/system/metrics is is_admin only (REQ-API-163): anonymous and
// authenticated-but-not-admin callers get 403, like §4.24.
func TestSystemMetricsRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	user := e.mustUser("metrics-user@example.org")

	for _, actor := range []*db.User{nil, user} {
		if rec := e.do("GET", "/api/v1/system/metrics", nil, actor); rec.Code != http.StatusForbidden {
			t.Errorf("GET as %v: status = %d, want 403", actor, rec.Code)
		}
	}
}

// The admin answer is the registry snapshot plus live pool stats, decodable
// with the metrics package's own types (the shape IS the wire contract).
func TestSystemMetricsSnapshotForAdmin(t *testing.T) {
	e := newEnv(t)
	reg := metrics.New()
	e.Handler.Metrics = reg

	// A few observations of every family, plus one real query so the pool
	// section reflects a live sql.DB.
	reg.ObserveHTTP("/api/", "POST", 201, 12*time.Millisecond, 100, 900)
	reg.ObserveContent("record", 5*time.Millisecond)
	reg.ObserveDB("SELECT", 3*time.Millisecond, 7, false)
	reg.ObserveDial(time.Millisecond, false)
	if _, err := e.Store.DB.ExecContext(context.Background(), `SELECT 1`); err != nil {
		t.Fatal(err)
	}

	admin := e.mustAdmin("metrics-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org")
	rec := e.do("GET", "/api/v1/system/metrics", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var snap metrics.Snapshot
	e.decode(rec, &snap)

	var route *metrics.RouteStats
	for i := range snap.HTTP.Routes {
		if snap.HTTP.Routes[i].Route == "POST /api/" {
			route = &snap.HTTP.Routes[i]
		}
	}
	if route == nil || route.Count != 1 || route.Status2xx != 1 || route.RespBytesTotal != 900 {
		t.Errorf("route POST /api/ = %+v", route)
	}

	var sel *metrics.DBVerbStats
	for i := range snap.DB.Statements {
		if snap.DB.Statements[i].Verb == "SELECT" {
			sel = &snap.DB.Statements[i]
		}
	}
	if sel == nil || sel.Count != 1 || sel.RowsTotal != 7 {
		t.Errorf("SELECT statement stats = %+v", sel)
	}

	if snap.DB.Pool == nil {
		t.Fatal("pool section missing — the endpoint must fill it from sql.DB.Stats()")
	}
	wantMaxOpen := 10 // db.configurePool: SQLite keeps its pool modest
	if testdb.Engine() == "mariadb" {
		wantMaxOpen = 25
	}
	if snap.DB.Pool.Open < 1 || snap.DB.Pool.MaxOpen != wantMaxOpen {
		t.Errorf("pool = %+v, want open ≥ 1 and max_open %d (db.Open limits)", snap.DB.Pool, wantMaxOpen)
	}
	if snap.Process.HeapAllocBytes == 0 {
		t.Error("process.heap_alloc_bytes = 0")
	}

	// The response is facts only: no token or user data in the payload.
	for _, forbidden := range []string{"test-token", "metrics-user@"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Errorf("response leaks %q", forbidden)
		}
	}
}

// A nil registry (the uninstrumented path) still answers 200 with an empty,
// well-shaped snapshot rather than failing.
func TestSystemMetricsNilRegistryServesEmpty(t *testing.T) {
	e := newEnv(t) // fixture builds the handler with a nil registry
	admin := e.mustAdmin("metrics-nil-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org")
	rec := e.do("GET", "/api/v1/system/metrics", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var snap metrics.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.HTTP.Routes) != 0 || len(snap.DB.Statements) != 0 {
		t.Errorf("expected empty snapshot, got routes=%d statements=%d", len(snap.HTTP.Routes), len(snap.DB.Statements))
	}
	if snap.DB.Pool == nil {
		t.Error("pool must be present even with a nil registry")
	}
}
