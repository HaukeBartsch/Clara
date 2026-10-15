package metrics

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestObserveHTTPCounts(t *testing.T) {
	r := New()
	r.ObserveHTTP("/api/", "POST", 200, 10*time.Millisecond, 100, 900)
	r.ObserveHTTP("/api/", "post", 404, 2*time.Millisecond, 50, 60) // method case folds
	r.ObserveHTTP("GET /api/v1/projects/{id}", "GET", 500, time.Second, -1, 0)
	r.ObserveHTTP("", "TRACEWITHBODY", 405, time.Millisecond, 0, 0)

	snap := r.Snapshot()
	if snap.HTTP.RequestsTotal != 4 {
		t.Fatalf("requests_total = %d, want 4", snap.HTTP.RequestsTotal)
	}
	byRoute := map[string]RouteStats{}
	for _, rs := range snap.HTTP.Routes {
		byRoute[rs.Route] = rs
	}
	if _, ok := byRoute["GET /api/v1/projects/{id}"]; !ok {
		t.Errorf("method token of registered pattern not stripped: routes %v", keysOf(byRoute))
	}
	post := byRoute["POST /api/"]
	if post.Count != 2 || post.Status2xx != 1 || post.Status4xx != 1 {
		t.Errorf("POST /api/: count=%d 2xx=%d 4xx=%d, want 2/1/1", post.Count, post.Status2xx, post.Status4xx)
	}
	if post.ReqBytesTotal != 150 || post.RespBytesTotal != 960 {
		t.Errorf("POST /api/ bytes = %d/%d, want 150/960", post.ReqBytesTotal, post.RespBytesTotal)
	}
	if post.SumMs < 12.0-0.01 {
		t.Errorf("POST /api/ sum_ms = %v, want ≥ 12", post.SumMs)
	}
	if post.MeanMs <= 0 || post.P50Ms > post.P95Ms || post.P95Ms > post.P99Ms {
		t.Errorf("POST /api/ percentiles not monotonic: %+v", post)
	}

	// Unknown method and empty pattern collapse to unmatched labels.
	found := false
	for _, rs := range snap.HTTP.Routes {
		if strings.HasPrefix(rs.Route, "OTHER ") && strings.HasSuffix(rs.Route, "unmatched") {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown method/empty pattern must observe as \"OTHER unmatched\", got %v", keysOf(byRoute))
	}

	// Cumulative buckets: the last (+Inf) count equals the route count.
	for _, rs := range snap.HTTP.Routes {
		last := rs.BucketsLeMs[len(rs.BucketsLeMs)-1]
		if last.LeMS != "+Inf" || last.Count != rs.Count {
			t.Errorf("route %q: last bucket = %+v, want +Inf/%d", rs.Route, last, rs.Count)
		}
	}
}

func TestBucketBoundaries(t *testing.T) {
	r := New()
	r.ObserveHTTP("/x", "GET", 200, 500*time.Microsecond, 0, 0) // exactly the first bound (le)
	r.ObserveHTTP("/x", "GET", 200, time.Duration((httpBucketsMs[len(httpBucketsMs)-1]+1)*float64(time.Millisecond)), 0, 0)

	rs := r.Snapshot().HTTP.Routes[0]
	if rs.BucketsLeMs[0].Count != 1 {
		t.Errorf("value exactly on the first bound must count in bucket 0 (le), got %+v", rs.BucketsLeMs[:2])
	}
	if rs.Count != 2 || rs.BucketsLeMs[len(rs.BucketsLeMs)-1].Count != 2 {
		t.Errorf("value above every bound must land in +Inf: %+v", rs.BucketsLeMs)
	}
	// p95 of {0.5 ms, >30 s}: interpolates inside the +Inf-clamped top region —
	// an estimate clamped to the last finite bound.
	if rs.P95Ms != httpBucketsMs[len(httpBucketsMs)-1] {
		t.Errorf("p95 = %v, want clamp to last finite bound %v", rs.P95Ms, httpBucketsMs[len(httpBucketsMs)-1])
	}
}

func TestPercentileEmptySeries(t *testing.T) {
	s := newSeries(dbBucketsMs)
	if s.percentile(0.5) != 0 {
		t.Error("empty percentile must be 0")
	}
}

func TestObserveContentVocabulary(t *testing.T) {
	r := New()
	r.ObserveContent("record", time.Millisecond)
	r.ObserveContent("bogus; DROP TABLE x", time.Millisecond)
	r.ObserveContent("", time.Millisecond)

	calls := map[string]ContentStats{}
	for _, c := range r.Snapshot().DataAPI.Calls {
		calls[c.Content] = c
	}
	if calls["record"].Count != 1 {
		t.Errorf("record count = %d, want 1", calls["record"].Count)
	}
	if calls["other"].Count != 2 {
		t.Errorf("other count = %d, want 2", calls["other"].Count)
	}
	for name := range calls {
		if !contentVocabulary[name] && name != "other" {
			t.Errorf("label %q escaped the closed vocabulary", name)
		}
	}
}

func TestObserveDBVerbGuardAndRows(t *testing.T) {
	r := New()
	r.ObserveDB("SELECT", time.Millisecond, 7, false)
	r.ObserveDB("select", time.Millisecond, 3, true) // lowercase is not the enum → OTHER
	r.ObserveDB("INSERT", 2*time.Millisecond, -1, false)
	r.ObserveDB("INSERT", time.Millisecond, 5, true)

	st := map[string]DBVerbStats{}
	for _, s := range r.Snapshot().DB.Statements {
		st[s.Verb] = s
	}
	if st["SELECT"].Count != 1 || st["SELECT"].RowsTotal != 7 {
		t.Errorf("SELECT = %+v, want 1/7", st["SELECT"])
	}
	if st["OTHER"].Count != 1 || st["OTHER"].Errors != 1 {
		t.Errorf("lowercase verb must observe as OTHER: %+v", st["OTHER"])
	}
	if st["INSERT"].Count != 2 || st["INSERT"].RowsTotal != 5 || st["INSERT"].Errors != 1 {
		t.Errorf("INSERT = %+v, want count 2, rows 5 (−1 contributes), errors 1", st["INSERT"])
	}

	r.ObserveDial(3*time.Millisecond, false)
	r.ObserveDial(time.Millisecond, true)
	snap := r.Snapshot()
	if snap.DB.ConnectionsDialed != 2 || snap.DB.DialErrors != 1 {
		t.Errorf("dials = %d/%d, want 2/1", snap.DB.ConnectionsDialed, snap.DB.DialErrors)
	}
}

func TestNilRegistrySafe(t *testing.T) {
	var r *Registry
	r.ObserveHTTP("/x", "GET", 200, time.Second, 0, 0)
	r.ObserveContent("record", time.Second)
	r.ObserveDB("SELECT", time.Second, 1, false)
	r.ObserveDial(time.Second, false)

	// Empty registry snapshots with empty arrays, never nulls.
	out, err := json.Marshal(New().Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"routes":[]`, `"calls":[]`, `"statements":[]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("snapshot JSON lacks %s: %s", want, out)
		}
	}
}

func TestConcurrentObserve(t *testing.T) {
	r := New()
	const goroutines, rounds = 8, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch i % 4 {
				case 0:
					r.ObserveHTTP("/api/", "POST", 200, time.Duration(i)*time.Microsecond, int64(i), int64(i))
				case 1:
					r.ObserveContent("record", time.Duration(i)*time.Microsecond)
				case 2:
					r.ObserveDB("SELECT", time.Duration(i)*time.Millisecond, int64(i%7), i%13 == 0)
				default:
					r.ObserveDial(time.Microsecond, false)
				}
			}
		}(g)
	}
	wg.Wait()

	snap := r.Snapshot()
	if snap.HTTP.RequestsTotal != goroutines*rounds/4 {
		t.Fatalf("requests_total = %d, want %d", snap.HTTP.RequestsTotal, goroutines*rounds/4)
	}
	for _, rs := range snap.HTTP.Routes {
		if rs.Route == "POST /api/" && rs.Count != goroutines*rounds/4 {
			t.Errorf("POST /api/ count = %d", rs.Count)
		}
	}
	var sel DBVerbStats
	for _, s := range snap.DB.Statements {
		if s.Verb == "SELECT" {
			sel = s
		}
	}
	if sel.Count != goroutines*rounds/4 {
		t.Errorf("SELECT count = %d, want %d", sel.Count, goroutines*rounds/4)
	}
	if snap.DB.ConnectionsDialed != goroutines*rounds/4 {
		t.Errorf("dialed = %d", snap.DB.ConnectionsDialed)
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	r := New()
	r.ObserveHTTP("GET /healthz", "GET", 200, time.Millisecond, 0, 42)
	out, err := json.Marshal(r.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"generated_at"`, `"uptime_seconds"`, `"heap_alloc_bytes"`, `"buckets_le_ms"`, `"+Inf"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("snapshot JSON lacks %s", want)
		}
	}
}

func keysOf(m map[string]RouteStats) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
