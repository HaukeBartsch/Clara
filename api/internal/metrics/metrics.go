// Package metrics accumulates the API's own performance counters (REQ-API-163):
// HTTP latency per registered route pattern, data-API calls per content value,
// database statements per verb, and connection dials. Everything lives in
// process memory only — nothing is persisted, all values reset on restart —
// and GET /api/v1/system/metrics (admin/system_metrics.go) is the single
// consumer; it adds the runtime memory/GC numbers and the sql.DB pool stats at
// snapshot time. The bucket boundaries and the JSON schema are normative in
// Design/API_Endpoints_Design.md §4.25: field names and units never change,
// and a change to one side means changing the other.
//
// Cardinality is closed by construction — route labels come only from
// server-side registered patterns (else "unmatched"), content values are
// whitelisted, verbs form a fixed enum, methods an allow-list — so no request
// input can ever mint a series or leak an id, path value or token into a
// label. The registry never sees SQL parameters or row values (no PHI crosses
// the statement boundary, REQ-API-005/006): the database wrapper in internal/db
// passes only verb, duration, row counts and error flags.
package metrics

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// httpBucketsMs / dbBucketsMs are the cumulative histogram bucket upper bounds
// in milliseconds; an implicit +Inf bucket catches everything above the last.
// The HTTP grid brackets the 300 ms p95 admin-read target (REQ-TECH-010 on the
// reference hardware of Technology_Stack_Design.md §7) and spans sub-millisecond
// health checks to streamed exports; the DB grid spans sub-100 µs SQLite point
// reads to multi-second export queries and covers the busy_timeout(5000) wait.
var (
	httpBucketsMs = []float64{0.5, 1, 2.5, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 400, 500, 750, 1000, 2500, 5000, 10000, 30000}
	dbBucketsMs   = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 500, 1000, 3000, 10000}
)

// contentVocabulary is the data API's supported content= set
// (dataapi/handler.go); anything else — including an empty value — observes
// into "other" so a caller string never becomes a label.
var contentVocabulary = map[string]bool{
	"project": true, "event": true, "metadata": true,
	"formEventMapping": true, "exportFieldNames": true, "repeatingFormsEvents": true,
	"generateNextRecordName": true, "record": true,
}

// dbVerbs is the closed verb set observed by the database wrapper; sqlVerb in
// internal/db maps a statement's leading word onto it — everything else
// (PRAGMA, DDL, BEGIN issued raw) is OTHER.
var dbVerbs = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"COMMIT": true, "ROLLBACK": true, "OTHER": true,
}

// httpMethods is the allow-list for the method label; r.Method is
// caller-controlled and must not mint series.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true,
}

// Registry holds every counter behind one mutex: contention is noise next to a
// database round-trip at this system's request rates (sharding by label would
// be the escape hatch if a profile ever says otherwise). All Observe methods
// are nil-receiver safe so callers may hold an uninstrumented nil Registry.
type Registry struct {
	mu       sync.Mutex
	started  time.Time               // immutable after New; read without the mutex
	routes   map[string]*routeSeries // key = "<METHOD> <path-template>"
	contents map[string]*series      // data API calls per content value
	dbOps    map[string]*dbSeries    // statements per verb

	requestsTotal uint64
	dialed        uint64
	dialErrs      uint64
	dialSumNs     int64
}

// New returns an empty registry; the uptime baseline starts now.
func New() *Registry {
	return &Registry{
		started:  time.Now(),
		routes:   map[string]*routeSeries{},
		contents: map[string]*series{},
		dbOps:    map[string]*dbSeries{},
	}
}

// ObserveHTTP records one served request. pattern is the server-side
// registered pattern or path template (it may carry a leading method token —
// as ServeMux patterns do — which is replaced by the actual request method);
// an empty pattern observes into "unmatched". A negative reqBytes means the
// length was unknown and contributes nothing to the byte totals.
func (r *Registry) ObserveHTTP(pattern, method string, status int, d time.Duration, reqBytes, respBytes int64) {
	if r == nil {
		return
	}
	key := observeKey(pattern, method)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestsTotal++
	s := r.routes[key]
	if s == nil {
		s = &routeSeries{series: newSeries(httpBucketsMs)}
		r.routes[key] = s
	}
	s.observe(d)
	if idx := status / 100; idx >= 2 && idx <= 5 {
		s.status[idx]++
	}
	if reqBytes > 0 {
		s.reqBytes += reqBytes
	}
	if respBytes > 0 {
		s.respBytes += respBytes
	}
}

// ObserveContent records one data API call by its content= value; values
// outside the supported vocabulary land in "other".
func (r *Registry) ObserveContent(content string, d time.Duration) {
	if r == nil {
		return
	}
	if !contentVocabulary[content] {
		content = "other"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.contents[content]
	if s == nil {
		base := newSeries(httpBucketsMs)
		s = &base
		r.contents[content] = s
	}
	s.observe(d)
}

// ObserveDB records one database statement. verb must come from the closed
// dbVerbs set (sqlVerb in internal/db); anything else observes into OTHER.
// rows counts rows read or written; a negative value means the driver could
// not say and contributes nothing.
func (r *Registry) ObserveDB(verb string, d time.Duration, rows int64, failed bool) {
	if r == nil {
		return
	}
	if !dbVerbs[verb] {
		verb = "OTHER"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.dbOps[verb]
	if s == nil {
		s = &dbSeries{series: newSeries(dbBucketsMs)}
		r.dbOps[verb] = s
	}
	s.observe(d)
	if failed {
		s.errs++
	}
	if rows > 0 {
		s.rows += rows
	}
}

// ObserveDial records one connection dial of the wrapped database connector.
func (r *Registry) ObserveDial(d time.Duration, failed bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialed++
	r.dialSumNs += d.Nanoseconds()
	if failed {
		r.dialErrs++
	}
}

// observeKey composes the route label "<METHOD> <path-template>": the method
// passes the allow-list (else OTHER) and a leading ALL/method token of the
// registered pattern is dropped, so every observation for one template shares
// one series whatever shape the resolver returned it in.
func observeKey(pattern, method string) string {
	m := strings.ToUpper(method)
	if !httpMethods[m] {
		m = "OTHER"
	}
	p := strings.TrimSpace(pattern)
	if i := strings.IndexByte(p, ' '); i > 0 && isAllCapsWord(p[:i]) {
		p = strings.TrimSpace(p[i+1:])
	}
	if p == "" {
		p = "unmatched"
	}
	return m + " " + p
}

func isAllCapsWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < 'A' || c > 'Z') && c != '-' {
			return false
		}
	}
	return true
}

// series is one fixed-bucket histogram plus count and sum. Buckets hold
// per-bucket counts (index len(bounds) is the +Inf bucket); Snapshot renders
// them cumulative.
type series struct {
	bounds []float64
	count  uint64
	sumNs  int64
	buck   []uint64
}

func newSeries(bounds []float64) series {
	return series{bounds: bounds, buck: make([]uint64, len(bounds)+1)}
}

func (s *series) observe(d time.Duration) {
	s.count++
	s.sumNs += d.Nanoseconds()
	// First bucket whose bound is ≥ the value; a value exactly on a bound
	// counts in that bucket (cumulative "le" semantics), and everything above
	// the last bound lands in the +Inf bucket at index len(bounds).
	ms := float64(d.Nanoseconds()) / 1e6
	i := sort.Search(len(s.bounds), func(i int) bool { return s.bounds[i] >= ms })
	s.buck[i]++
}

// percentile estimates the q-quantile by linear interpolation inside the
// bucket that holds it (Prometheus histogram_quantile style); a quantile in
// the +Inf bucket clamps to the last finite bound. Estimates, not exact values.
func (s *series) percentile(q float64) float64 {
	if s.count == 0 {
		return 0
	}
	rank := q * float64(s.count)
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i := 0; i < len(s.bounds); i++ {
		if s.buck[i] > 0 && float64(cum)+float64(s.buck[i]) >= rank {
			lower := 0.0
			if i > 0 {
				lower = s.bounds[i-1]
			}
			upper := s.bounds[i]
			frac := (rank - float64(cum)) / float64(s.buck[i])
			return lower + (upper-lower)*frac
		}
		cum += s.buck[i]
	}
	return s.bounds[len(s.bounds)-1]
}

// latency derives the reported summary numbers from one series.
func latency(s *series) (mean, p50, p95, p99 float64) {
	if s.count > 0 {
		mean = float64(s.sumNs) / 1e6 / float64(s.count)
	}
	return mean, s.percentile(0.50), s.percentile(0.95), s.percentile(0.99)
}

type routeSeries struct {
	series
	status    [6]uint64 // index = status/100; only 2..5 populated
	reqBytes  int64
	respBytes int64
}

type dbSeries struct {
	series
	errs uint64
	rows int64
}

// Snapshot renders the registry plus process numbers. Field names and units
// are a stable contract (Design/API_Endpoints_Design.md §4.25): fields may be
// added, never renamed or changed in unit.
type Snapshot struct {
	GeneratedAt   string       `json:"generated_at"` // canonical UTC (REQ-DB-005)
	UptimeSeconds float64      `json:"uptime_seconds"`
	Process       ProcessStats `json:"process"`
	HTTP          HTTPSnap     `json:"http"`
	DataAPI       DataAPISnap  `json:"data_api"`
	DB            DBSnap       `json:"db"`
}

// ProcessStats is the runtime.MemStats subset the evaluation needs; GC pause
// percentiles are nearest-rank estimates over the last ≤256 pauses.
type ProcessStats struct {
	Goroutines      int     `json:"goroutines"`
	HeapAllocBytes  uint64  `json:"heap_alloc_bytes"` // heap in use
	HeapSysBytes    uint64  `json:"heap_sys_bytes"`
	TotalAllocBytes uint64  `json:"total_alloc_bytes"` // cumulative, includes reclaimed
	SysBytes        uint64  `json:"sys_bytes"`
	NumGC           uint32  `json:"num_gc"`
	GCPauseTotalMs  float64 `json:"gc_pause_total_ms"`
	GCPauseLastMs   float64 `json:"gc_pause_last_ms"`
	GCPauseP50Ms    float64 `json:"gc_pause_p50_ms"`
	GCPauseP95Ms    float64 `json:"gc_pause_p95_ms"`
	GCPauseMaxMs    float64 `json:"gc_pause_max_ms"`
}

type HTTPSnap struct {
	RequestsTotal uint64       `json:"requests_total"`
	Routes        []RouteStats `json:"routes"` // sorted by label for a stable answer
}

type RouteStats struct {
	Route          string   `json:"route"` // e.g. "GET /api/v1/projects/{id}", "POST /api/", "unmatched"
	Count          uint64   `json:"count"`
	Status2xx      uint64   `json:"status_2xx"`
	Status3xx      uint64   `json:"status_3xx"`
	Status4xx      uint64   `json:"status_4xx"`
	Status5xx      uint64   `json:"status_5xx"`
	SumMs          float64  `json:"sum_ms"`
	MeanMs         float64  `json:"mean_ms"`
	P50Ms          float64  `json:"p50_ms"`
	P95Ms          float64  `json:"p95_ms"`
	P99Ms          float64  `json:"p99_ms"`
	BucketsLeMs    []Bucket `json:"buckets_le_ms"`
	ReqBytesTotal  int64    `json:"req_bytes_total"`
	RespBytesTotal int64    `json:"resp_bytes_total"`
}

type ContentStats struct {
	Content   string   `json:"content"`
	Count     uint64   `json:"count"`
	SumMs     float64  `json:"sum_ms"`
	MeanMs    float64  `json:"mean_ms"`
	P50Ms     float64  `json:"p50_ms"`
	P95Ms     float64  `json:"p95_ms"`
	P99Ms     float64  `json:"p99_ms"`
	BucketsLe []Bucket `json:"buckets_le_ms"`
}

type DataAPISnap struct {
	Calls []ContentStats `json:"calls"` // sorted by content value
}

type DBSnap struct {
	Statements        []DBVerbStats `json:"statements"` // sorted by verb
	ConnectionsDialed uint64        `json:"connections_dialed_total"`
	DialSumMs         float64       `json:"dial_sum_ms"`
	DialErrors        uint64        `json:"dial_errors"`
	Pool              *PoolStats    `json:"pool"` // filled by the endpoint from sql.DB.Stats()
}

type DBVerbStats struct {
	Verb      string   `json:"verb"`
	Count     uint64   `json:"count"`
	Errors    uint64   `json:"errors"`
	RowsTotal int64    `json:"rows_total"` // rows the drivers reported (queries: Next counts, writes: RowsAffected)
	SumMs     float64  `json:"sum_ms"`
	MeanMs    float64  `json:"mean_ms"`
	P50Ms     float64  `json:"p50_ms"`
	P95Ms     float64  `json:"p95_ms"`
	P99Ms     float64  `json:"p99_ms"`
	BucketsLe []Bucket `json:"buckets_le_ms"`
}

// PoolStats mirrors the relevant fields of sql.DBStats — pool starvation reads
// as a rising WaitCount next to InUse == MaxOpen.
type PoolStats struct {
	MaxOpen       int   `json:"max_open"`
	Open          int   `json:"open"`
	InUse         int   `json:"in_use"`
	Idle          int   `json:"idle"`
	WaitCount     int64 `json:"wait_count"`
	WaitMs        int64 `json:"wait_ms"`
	MaxIdleClosed int64 `json:"max_idle_closed"`
}

// Bucket is one cumulative histogram bucket; the last entry of every list
// carries the string "+Inf" as its bound.
type Bucket struct {
	LeMS  any    `json:"le_ms"` // float64 bound, or "+Inf" for the last bucket
	Count uint64 `json:"count"`
}

// Snapshot copies the registry under its mutex and reads the runtime stats
// (ReadMemStats briefly stops the world — this runs only on the admin
// endpoint, never per operation). DB.Pool is left nil; the handler fills it
// from sql.DB.Stats().
func (r *Registry) Snapshot() *Snapshot {
	if r == nil { // nil-registry callers see an empty snapshot, not a panic
		r = New()
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	now := time.Now()
	snap := &Snapshot{
		GeneratedAt:   now.UTC().Format("2006-01-02 15:04:05"),
		UptimeSeconds: now.Sub(r.started).Seconds(),
		Process:       processStats(&mem),
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	snap.HTTP.RequestsTotal = r.requestsTotal
	snap.HTTP.Routes = []RouteStats{}
	for _, key := range sortedKeys(r.routes) {
		rs := r.routes[key]
		mean, p50, p95, p99 := latency(&rs.series)
		snap.HTTP.Routes = append(snap.HTTP.Routes, RouteStats{
			Route:          key,
			Count:          rs.count,
			Status2xx:      rs.status[2],
			Status3xx:      rs.status[3],
			Status4xx:      rs.status[4],
			Status5xx:      rs.status[5],
			SumMs:          float64(rs.sumNs) / 1e6,
			MeanMs:         mean,
			P50Ms:          p50,
			P95Ms:          p95,
			P99Ms:          p99,
			BucketsLeMs:    renderBuckets(&rs.series),
			ReqBytesTotal:  rs.reqBytes,
			RespBytesTotal: rs.respBytes,
		})
	}
	snap.DataAPI.Calls = []ContentStats{}
	for _, name := range sortedKeys(r.contents) {
		s := r.contents[name]
		mean, p50, p95, p99 := latency(s)
		snap.DataAPI.Calls = append(snap.DataAPI.Calls, ContentStats{
			Content: name, Count: s.count, SumMs: float64(s.sumNs) / 1e6,
			MeanMs: mean, P50Ms: p50, P95Ms: p95, P99Ms: p99, BucketsLe: renderBuckets(s),
		})
	}
	snap.DB.Statements = []DBVerbStats{}
	for _, verb := range sortedKeys(r.dbOps) {
		d := r.dbOps[verb]
		mean, p50, p95, p99 := latency(&d.series)
		snap.DB.Statements = append(snap.DB.Statements, DBVerbStats{
			Verb: verb, Count: d.count, Errors: d.errs, RowsTotal: d.rows,
			SumMs: float64(d.sumNs) / 1e6, MeanMs: mean, P50Ms: p50, P95Ms: p95, P99Ms: p99,
			BucketsLe: renderBuckets(&d.series),
		})
	}
	snap.DB.ConnectionsDialed = r.dialed
	snap.DB.DialSumMs = float64(r.dialSumNs) / 1e6
	snap.DB.DialErrors = r.dialErrs
	return snap
}

// renderBuckets turns per-bucket counts into the cumulative le-list.
func renderBuckets(s *series) []Bucket {
	out := make([]Bucket, 0, len(s.buck))
	var cum uint64
	for i := range s.buck {
		cum += s.buck[i]
		if i < len(s.bounds) {
			out = append(out, Bucket{LeMS: s.bounds[i], Count: cum})
		} else {
			out = append(out, Bucket{LeMS: "+Inf", Count: cum})
		}
	}
	return out
}

func processStats(mem *runtime.MemStats) ProcessStats {
	p := ProcessStats{
		Goroutines:      runtime.NumGoroutine(),
		HeapAllocBytes:  mem.HeapAlloc,
		HeapSysBytes:    mem.HeapSys,
		TotalAllocBytes: mem.TotalAlloc,
		SysBytes:        mem.Sys,
		NumGC:           mem.NumGC,
		GCPauseTotalMs:  float64(mem.PauseTotalNs) / 1e6,
	}
	n := mem.NumGC
	if n > 256 { // PauseNs is a ring of at most 256 entries
		n = 256
	}
	if n == 0 {
		return p
	}
	pauses := make([]float64, 0, n)
	for i := uint32(0); i < n; i++ {
		idx := (mem.NumGC + 255 - i) % 256 // PauseNs is written at NumGC%256; this walks back newest-first
		pauses = append(pauses, float64(mem.PauseNs[idx])/1e6)
	}
	p.GCPauseLastMs = pauses[0]
	sort.Float64s(pauses)
	at := func(q float64) float64 {
		i := int(q * float64(len(pauses)))
		if i >= len(pauses) {
			i = len(pauses) - 1
		}
		return pauses[i]
	}
	p.GCPauseP50Ms, p.GCPauseP95Ms, p.GCPauseMaxMs = at(0.50), at(0.95), pauses[len(pauses)-1]
	return p
}

// sortedKeys returns the map keys in ascending order for a stable snapshot.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
