package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/config"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// This package is the security-critical glue between nginx/PHP and the two API
// surfaces: the administration boundary (REQ-API-041, REQ-AUTH-011…013), the
// health endpoint (REQ-API-003) and the per-source-IP budget on /api/v1/*
// (REQ-API-038/111/112). The tests are package-internal so the middlewares are
// exercised directly, and every rejection is checked against the audit row that
// actually landed (REQ-AUD-008), not against an attempted write.

// --- fixture ---

// env is the shared fixture: a migrated SQLite store, an audit writer, and the
// administration boundary wrapped around a probe handler. The probe stands in
// for the administration mux — reaching it proves the boundary accepted the
// call, and its body reports the actor the boundary placed in the request
// context (REQ-AUTH-013).
type env struct {
	t        *testing.T
	Store    *db.Store
	Cfg      *config.Config
	Audit    *audit.Writer
	Boundary http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := &config.Config{
		AppEnv:               "development",
		DBConnection:         "sqlite",
		DBDatabase:           filepath.Join(t.TempDir(), "httpapi.sqlite"),
		AnonSalt:             "test-salt",
		InternalServiceToken: "test-token",
		WebPublicURL:         "https://csms.example.org",
	}
	store, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	aw := audit.NewWriter(store.DB, string(store.Dialect))
	if err := aw.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	e := &env{t: t, Store: store, Cfg: cfg, Audit: aw}
	e.Boundary = AdminBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a probeActor
		if actor := authz.ActorFrom(r.Context()); actor != nil {
			a = probeActor{ID: actor.ID, Email: actor.Email}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(a)
	}), store, cfg, aw)
	return e
}

// probeActor is the probe's report of the acting user it was handed; a zero ID
// means the handler ran without one.
type probeActor struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// errorReply is the wire shape of the administration error object
// (API_Endpoints_Design.md §4.2).
type errorReply struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// serve dispatches req to h and turns a handler panic into a failure: the
// boundary answers 401/403, it does not crash (REQ-API-041). The recorder comes
// back either way so the caller can report what was written.
func serve(t *testing.T, h http.Handler, req *http.Request) (rec *httptest.ResponseRecorder) {
	t.Helper()
	rec = httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("%s %s panicked instead of answering: %v", req.Method, req.URL.Path, p)
		}
	}()
	h.ServeHTTP(rec, req)
	return rec
}

// do runs one request through the boundary. A nil pointer means the header is
// absent; str("") sends it present but empty — different inputs to the header
// parsing (REQ-AUTH-013).
func (e *env) do(method, path string, token, userID *string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != nil {
		req.Header.Set("X-Internal-Service-Token", *token)
	}
	if userID != nil {
		req.Header.Set("X-Internal-User-Id", *userID)
	}
	return serve(e.t, e.Boundary, req)
}

// actor decodes the probe's report; it fails when the probe did not run.
func (e *env) actor(rec *httptest.ResponseRecorder) probeActor {
	e.t.Helper()
	if rec.Code != http.StatusOK {
		e.t.Fatalf("probe not reached: %d %s", rec.Code, rec.Body.String())
	}
	var a probeActor
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		e.t.Fatalf("decode probe body %q: %v", rec.Body.String(), err)
	}
	return a
}

// mustError asserts the §4.2 error shape of a rejection.
func (e *env) mustError(rec *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	if rec.Code != status {
		e.t.Errorf("status = %d, want %d (body %q)", rec.Code, status, rec.Body.String())
	}
	var body errorReply
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		e.t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if body.Error != code {
		e.t.Errorf("error code = %q, want %q", body.Error, code)
	}
	if body.Status != status {
		e.t.Errorf("body status = %d, want %d", body.Status, status)
	}
}

// --- audit assertions (REQ-AUD-008) ---

// rejection is one stored admin_rejected row, read back from the trail. The
// reason vocabulary is fixed by Audit_Logging_Design.md §3.1.
type rejection struct {
	Source string
	Path   string
	Reason string
}

// rejections reads the admin_rejected rows that actually landed, oldest first.
func (e *env) rejections() []rejection {
	e.t.Helper()
	rows, err := e.Store.DB.QueryContext(context.Background(),
		`SELECT source, details FROM audit_events WHERE event_type = ? ORDER BY id`,
		audit.AdminRejected)
	if err != nil {
		e.t.Fatalf("query audit_events: %v", err)
	}
	defer rows.Close()
	var out []rejection
	for rows.Next() {
		var src, details string
		if err := rows.Scan(&src, &details); err != nil {
			e.t.Fatalf("scan audit_events: %v", err)
		}
		var d struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			e.t.Fatalf("audit details %q: %v", details, err)
		}
		out = append(out, rejection{Source: src, Path: d.Path, Reason: d.Reason})
	}
	if err := rows.Err(); err != nil {
		e.t.Fatalf("read audit_events: %v", err)
	}
	return out
}

// mustAuditOne asserts the request since `before` wrote exactly one
// admin_rejected row with this reason and "METHOD /path" (Audit §3.1). Exactly
// one: a rejection audited twice is as wrong as one not audited at all.
func (e *env) mustAuditOne(before []rejection, reason, path string) {
	e.t.Helper()
	now := e.rejections()
	if len(now) != len(before)+1 {
		e.t.Fatalf("admin_rejected rows = %d (was %d), want exactly one more for %s",
			len(now), len(before), path)
	}
	got := now[len(now)-1]
	if got.Reason != reason {
		e.t.Errorf("audit reason = %q, want %q", got.Reason, reason)
	}
	if got.Path != path {
		e.t.Errorf("audit path = %q, want %q", got.Path, path)
	}
	if got.Source != audit.SourceUI {
		e.t.Errorf("audit source = %q, want %q", got.Source, audit.SourceUI)
	}
}

// --- users for the fixture ---

func (e *env) mustCreate(u *db.User) *db.User {
	e.t.Helper()
	id, err := e.Store.CreateUser(context.Background(), u)
	if err != nil {
		e.t.Fatalf("CreateUser(%s): %v", u.Email, err)
	}
	u.ID = id
	return u
}

// mustUser inserts an active, non-administrative user.
func (e *env) mustUser(email string) *db.User {
	e.t.Helper()
	return e.mustCreate(&db.User{Email: email, DisplayName: "User", Enabled: true})
}

// mustAdmin inserts an active administrator.
func (e *env) mustAdmin(email string) *db.User {
	e.t.Helper()
	return e.mustCreate(&db.User{Email: email, DisplayName: "Admin", Enabled: true, IsAdmin: true})
}

// mustDisabled inserts a user that is enabled=0 — the account-disabled case of
// the active rule (Authentication_Authorization_Design.md §4.4).
func (e *env) mustDisabled(email string) *db.User {
	e.t.Helper()
	return e.mustCreate(&db.User{Email: email, DisplayName: "Gone", Enabled: false})
}

// mustExpired inserts an enabled account whose valid_until has passed — the
// expired case of the same rule (§4.4), which the boundary must also reject.
func (e *env) mustExpired(email string) *db.User {
	e.t.Helper()
	return e.mustCreate(&db.User{
		Email: email, DisplayName: "Expired", Enabled: true,
		ValidUntil: sql.NullString{String: "2020-01-01", Valid: true},
	})
}

func str(s string) *string { return &s }

// --- the administration boundary (REQ-API-041) ---

// TestAdminBoundaryServiceToken covers the first gate: a missing or wrong
// X-Internal-Service-Token is 401 service_token_invalid and never reaches the
// handler, enforced regardless of any other configuration (REQ-CFG-023). A
// valid acting user does not rescue a bad service token — the two credentials
// are independent (REQ-AUTH-011/012).
func TestAdminBoundaryServiceToken(t *testing.T) {
	e := newEnv(t)
	active := e.mustUser("active@example.org")
	validID := strconv.FormatInt(active.ID, 10)

	cases := []struct {
		name  string
		token *string
	}{
		{"absent", nil},
		{"present but empty", str("")},
		{"wrong", str("wrong-token")},
		{"prefix of the real token", str("test")},
		{"real token with trailing space", str("test-token ")},
		{"real token uppercased", str("TEST-TOKEN")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := e.rejections()
			rec := e.do(http.MethodGet, "/api/v1/users", tc.token, str(validID))
			e.mustError(rec, http.StatusUnauthorized, "service_token_invalid")
			e.mustAuditOne(before, "service_token_invalid", "GET /api/v1/users")
		})
	}
}

// TestAdminBoundaryActingUserHeader covers X-Internal-User-Id parsing with a
// valid service token: anything that is not a positive integer in base 10 is
// user_unknown → 403 (REQ-AUTH-013, API_Endpoints_Design.md §4.1). The header
// is authoritative, so a permissive parse would be an authorization hole. No
// user is seeded: every value must fail before the store is consulted.
func TestAdminBoundaryActingUserHeader(t *testing.T) {
	e := newEnv(t)

	cases := []struct {
		name   string
		userID *string
	}{
		{"absent", nil},
		{"present but empty", str("")},
		{"not a number", str("nobody")},
		{"email instead of id", str("active@example.org")},
		{"zero", str("0")},
		{"negative", str("-7")},
		{"leading space", str(" 4")},
		{"exponent form", str("1e3")},
		{"hex form", str("0x4")},
		{"int64 overflow", str("99999999999999999999")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := e.rejections()
			rec := e.do(http.MethodGet, "/api/v1/users", str("test-token"), tc.userID)
			e.mustError(rec, http.StatusForbidden, "forbidden")
			e.mustAuditOne(before, "user_unknown", "GET /api/v1/users")
		})
	}
}

// TestAdminBoundaryUnknownUserIsForbidden covers an id that parses but names no
// account. API_Endpoints_Design.md §4.1 fixes the answer as 403 forbidden +
// audit admin_rejected/user_unknown: not a pass, not a 500, and not a crash —
// store.GetUser reports not-found as (nil, nil) by repo convention, so the
// boundary must reject before authz.CheckActive sees a nil user. The
// assertions below are non-fatal so one run shows status, body and audit trail.
func TestAdminBoundaryUnknownUserIsForbidden(t *testing.T) {
	e := newEnv(t)
	const unknownID = "4242"

	before := len(e.rejections())
	rec := e.do(http.MethodGet, "/api/v1/users", str("test-token"), str(unknownID))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (body %q)", rec.Code, rec.Body.String())
	}
	var body errorReply
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Errorf("error body %q: %v, want the §4.2 forbidden object", rec.Body.String(), err)
	} else if body.Error != "forbidden" {
		t.Errorf("error code = %q, want forbidden", body.Error)
	}
	got := e.rejections()
	if len(got) != before+1 {
		t.Errorf("admin_rejected rows = %d (was %d), want exactly one more with reason user_unknown",
			len(got), before)
	} else if r := got[len(got)-1]; r.Reason != "user_unknown" || r.Path != "GET /api/v1/users" {
		t.Errorf("audit row = %+v, want reason user_unknown on GET /api/v1/users", r)
	}
}

// TestAdminBoundaryInactiveUserIsForbidden covers the account-active rule at an
// administration check (Authentication_Authorization_Design.md §4.4): a
// disabled account is 403 with reason user_disabled; an expired one is rejected
// by the same call (§3.1 has no separate reason code for expiry).
func TestAdminBoundaryInactiveUserIsForbidden(t *testing.T) {
	t.Run("disabled account", func(t *testing.T) {
		e := newEnv(t)
		u := e.mustDisabled("gone@example.org")

		before := e.rejections()
		rec := e.do(http.MethodGet, "/api/v1/users", str("test-token"),
			str(strconv.FormatInt(u.ID, 10)))
		e.mustError(rec, http.StatusForbidden, "forbidden")
		e.mustAuditOne(before, "user_disabled", "GET /api/v1/users")
	})

	t.Run("expired account", func(t *testing.T) {
		e := newEnv(t)
		u := e.mustExpired("old@example.org")

		before := e.rejections()
		rec := e.do(http.MethodGet, "/api/v1/users", str("test-token"),
			str(strconv.FormatInt(u.ID, 10)))
		e.mustError(rec, http.StatusForbidden, "forbidden")
		now := e.rejections()
		if len(now) != len(before)+1 {
			e.t.Fatalf("admin_rejected rows = %d (was %d), want exactly one more",
				len(now), len(before))
		}
		// The reason must come from the fixed §3.1 vocabulary; expiry has no
		// code of its own, so user_disabled is the only acceptable answer.
		if got := now[len(now)-1].Reason; got != "user_disabled" {
			e.t.Errorf("audit reason = %q, want user_disabled", got)
		}
	})
}

// TestAdminBoundaryActiveUserReachesHandlerWithActor is the positive case: the
// boundary hands the request down and the acting user travels in the context,
// which is what every administration handler reads (REQ-AUTH-013). The boundary
// checks activity only — a non-administrator gets through it and is limited by
// the handlers, not here.
func TestAdminBoundaryActiveUserReachesHandlerWithActor(t *testing.T) {
	cases := []struct {
		name string
		seed func(e *env) *db.User
	}{
		{"active member", func(e *env) *db.User { return e.mustUser("member@example.org") }},
		{"active administrator", func(e *env) *db.User { return e.mustAdmin("root@example.org") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			u := tc.seed(e)

			before := e.rejections()
			rec := e.do(http.MethodGet, "/api/v1/projects", str("test-token"),
				str(strconv.FormatInt(u.ID, 10)))
			got := e.actor(rec)
			if got.ID != u.ID {
				t.Errorf("handler saw user id %d, want %d — actor not in the context", got.ID, u.ID)
			}
			if got.Email != u.Email {
				t.Errorf("handler saw %q, want %q", got.Email, u.Email)
			}
			if n := len(e.rejections()); n != len(before) {
				t.Errorf("%d admin_rejected rows for an accepted call, want none", n-len(before))
			}
		})
	}
}

// TestAdminBoundaryLoginExemption covers the sole exception to
// X-Internal-User-Id (Authentication_Authorization_Design.md §2.3): login is
// reached with a service token alone, the identity riding in the body. The
// exemption is from the user header only — the service token still gates it.
func TestAdminBoundaryLoginExemption(t *testing.T) {
	t.Run("service token alone reaches the login handler", func(t *testing.T) {
		e := newEnv(t)
		before := e.rejections()
		rec := e.do(http.MethodPost, loginPath, str("test-token"), nil)
		got := e.actor(rec)
		if got.ID != 0 {
			t.Errorf("login ran with actor id %d, want none — the body carries the identity", got.ID)
		}
		if n := len(e.rejections()); n != len(before) {
			t.Errorf("%d admin_rejected rows for an exempt call, want none", n-len(before))
		}
	})

	t.Run("a bogus user id on login is not rejected by the boundary", func(t *testing.T) {
		e := newEnv(t)
		before := e.rejections()
		rec := e.do(http.MethodPost, loginPath, str("test-token"), str("0"))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want the probe to run (body %q)", rec.Code, rec.Body.String())
		}
		if n := len(e.rejections()); n != len(before) {
			t.Errorf("%d admin_rejected rows for an exempt call, want none", n-len(before))
		}
	})

	t.Run("login without a service token is still 401", func(t *testing.T) {
		e := newEnv(t)
		before := e.rejections()
		rec := e.do(http.MethodPost, loginPath, nil, nil)
		e.mustError(rec, http.StatusUnauthorized, "service_token_invalid")
		e.mustAuditOne(before, "service_token_invalid", "POST "+loginPath)
	})

	t.Run("login with a wrong service token and a valid user is still 401", func(t *testing.T) {
		e := newEnv(t)
		u := e.mustAdmin("root@example.org")
		before := e.rejections()
		rec := e.do(http.MethodPost, loginPath, str("nope"), str(strconv.FormatInt(u.ID, 10)))
		e.mustError(rec, http.StatusUnauthorized, "service_token_invalid")
		e.mustAuditOne(before, "service_token_invalid", "POST "+loginPath)
	})
}

// TestAdminBoundaryRejectionsAreAudited walks one rejection per reason and
// checks the trail end to end (REQ-AUD-008): one row each, in order, with the
// method-and-path the caller asked for and a source of "ui" (REQ-AUD-017).
func TestAdminBoundaryRejectionsAreAudited(t *testing.T) {
	e := newEnv(t)
	disabled := e.mustDisabled("gone@example.org")

	// One rejection per reason, on three different methods and paths.
	e.do(http.MethodPost, "/api/v1/projects", nil, nil)
	e.do(http.MethodDelete, "/api/v1/projects/7", str("test-token"), nil)
	e.do(http.MethodPut, "/api/v1/users/3", str("test-token"), str(strconv.FormatInt(disabled.ID, 10)))

	want := []rejection{
		{Source: audit.SourceUI, Path: "POST /api/v1/projects", Reason: "service_token_invalid"},
		{Source: audit.SourceUI, Path: "DELETE /api/v1/projects/7", Reason: "user_unknown"},
		{Source: audit.SourceUI, Path: "PUT /api/v1/users/3", Reason: "user_disabled"},
	}
	got := e.rejections()
	if len(got) != len(want) {
		t.Fatalf("%d admin_rejected rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// --- health endpoint (REQ-API-003) ---

// TestHealthHandler checks both branches of the bounded database probe: 200
// with db ok while the store answers, 503 degraded when it does not. The body
// is JSON either way — a monitoring system reads the status field, not HTML.
func TestHealthHandler(t *testing.T) {
	e := newEnv(t)

	t.Run("database answering", func(t *testing.T) {
		rec := serve(t, healthHandler(e.Store), httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
		if body["status"] != "ok" || body["db"] != "ok" {
			t.Errorf("body = %+v, want status ok and db ok", body)
		}
	})

	t.Run("database not answering", func(t *testing.T) {
		down := newEnv(t)
		if err := down.Store.Close(); err != nil {
			t.Fatalf("closing the store for the degraded case: %v", err)
		}
		rec := serve(t, healthHandler(down.Store), httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503 (body %q)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
		if body["status"] != "degraded" {
			t.Errorf("status field = %q, want degraded", body["status"])
		}
		if body["db"] != "error" {
			t.Errorf("db field = %q, want error", body["db"])
		}
	})
}

// --- rate limiting on the administration surface (REQ-API-038/111/112) ---

// TestRateLimitMiddleware exercises the middleware over one limiter and one
// store: the enable flag and threshold are read per request from the system
// settings (REQ-API-112), so flipping them takes effect without rebuilding the
// handler. The budget is per source IP (REQ-API-038).
func TestRateLimitMiddleware(t *testing.T) {
	e := newEnv(t)
	hits := 0
	h := rateLimit(e.Store, e.Cfg, dataapi.NewRateLimiter(),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			w.WriteHeader(http.StatusOK)
		}))

	send := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.RemoteAddr = ip + ":1234" // the connection address is the key (REQ-API-111)
		return serve(t, h, req)
	}
	setting := func(key, value string) {
		if err := e.Store.SetSystemSetting(context.Background(), key, value); err != nil {
			t.Fatalf("SetSystemSetting(%s): %v", key, err)
		}
	}

	t.Run("disabled lets everything through", func(t *testing.T) {
		// Rate limiting is opt-in (REQ-CFG-020), so the flag is set explicitly
		// rather than relying on the seeded default.
		setting("rate_limit_enabled", "false")
		setting("rate_limit_rpm", "1")
		before := hits
		for i := 0; i < 5; i++ {
			if rec := send("203.0.113.7"); rec.Code != http.StatusOK {
				t.Fatalf("call %d with the limiter off: %d %s", i+1, rec.Code, rec.Body.String())
			}
		}
		if hits-before != 5 {
			t.Errorf("%d calls reached the handler, want 5", hits-before)
		}
	})

	t.Run("enabled with a budget of one", func(t *testing.T) {
		setting("rate_limit_enabled", "true")
		setting("rate_limit_rpm", "1")

		before := hits
		if rec := send("198.51.100.4"); rec.Code != http.StatusOK {
			t.Fatalf("first call: %d %s", rec.Code, rec.Body.String())
		}
		rec := send("198.51.100.4")
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("second call within the minute: %d, want 429 (body %q)", rec.Code, rec.Body.String())
		}
		var body errorReply
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
		if body.Error != "rate_limited" {
			t.Errorf("error code = %q, want rate_limited", body.Error)
		}
		if hits-before != 1 {
			t.Errorf("%d calls reached the handler, want only the first", hits-before)
		}

		// A different source IP keeps its own budget (REQ-API-038).
		if rec := send("198.51.100.5"); rec.Code != http.StatusOK {
			t.Errorf("first call from a second IP: %d, want 200", rec.Code)
		}
		if hits-before != 2 {
			t.Errorf("%d calls reached the handler, want 2", hits-before)
		}
	})

	t.Run("an over-budget IP stays blocked for the configured period", func(t *testing.T) {
		// REQ-API-113: the refusal names the remaining blockout in Retry-After,
		// and further calls count down that same block instead of restarting it.
		setting("rate_limit_enabled", "true")
		setting("rate_limit_rpm", "1")
		setting("rate_limit_block_minutes", "7")

		if rec := send("198.51.100.9"); rec.Code != http.StatusOK {
			t.Fatalf("first call: %d %s", rec.Code, rec.Body.String())
		}
		rec := send("198.51.100.9")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second call over the budget: %d, want 429 (body %q)", rec.Code, rec.Body.String())
		}
		first := retryAfter(t, rec)
		if first < 415 || first > 420 {
			t.Errorf("Retry-After = %d, want the remaining seconds of the 7-minute block", first)
		}

		before := hits
		rec = send("198.51.100.9")
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("call during the block: %d, want 429", rec.Code)
		}
		if again := retryAfter(t, rec); again > first || again < first-2 {
			t.Errorf("Retry-After during the block = %d, want the same block counting down from %d", again, first)
		}
		if hits != before {
			t.Errorf("%d calls reached the handler during the block, want none", hits-before)
		}
	})
}

// retryAfter reads the Retry-After header of a 429 response (REQ-API-113).
func retryAfter(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	v := rec.Header().Get("Retry-After")
	secs, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("Retry-After = %q, want whole seconds", v)
	}
	return secs
}

// --- route table ---

// TestNewMuxRouting checks that each prefix lands on the right surface: health
// is public and routed, /api/v1/* is behind the boundary (and not claimed by
// the data API), /api/* reaches the data API. The data-API assertion uses a
// bogus token on purpose — "Invalid token" proves routing without needing
// credentials.
func TestNewMuxRouting(t *testing.T) {
	e := newEnv(t)
	root := e.mustAdmin("root@example.org")
	mux := NewMux(e.Store, e.Cfg, e.Audit)

	t.Run("GET /healthz", func(t *testing.T) {
		rec := serve(t, mux, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
		if body["status"] != "ok" {
			t.Errorf("body = %+v, want status ok", body)
		}
	})

	t.Run("/api/v1/* without the service token is rejected by the boundary", func(t *testing.T) {
		before := e.rejections()
		rec := serve(t, mux, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %q)", rec.Code, rec.Body.String())
		}
		// The administration error shape, not the data API's: a body reading
		// "Invalid token" would mean /api/ had claimed the request.
		if strings.Contains(rec.Body.String(), "Invalid token") {
			t.Errorf("body %q came from the data API, want the boundary", rec.Body.String())
		}
		var body errorReply
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
		if body.Error != "service_token_invalid" {
			t.Errorf("error code = %q, want service_token_invalid", body.Error)
		}
		e.mustAuditOne(before, "service_token_invalid", "GET /api/v1/users")
	})

	t.Run("/api/v1/* with a bound actor reaches the administration mux", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.Header.Set("X-Internal-Service-Token", "test-token")
		req.Header.Set("X-Internal-User-Id", strconv.FormatInt(root.ID, 10))
		rec := serve(t, mux, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var users []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
			t.Fatalf("administration handlers should answer with a user list: %v\n%s", err, rec.Body.String())
		}
	})

	t.Run("/api/* reaches the data API", func(t *testing.T) {
		rec := serve(t, mux, httptest.NewRequest(http.MethodGet,
			"/api/?content=project&format=json&token=nope", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %q)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Invalid token") {
			t.Errorf("body %q, want the data API's uniform \"Invalid token\" (REQ-API-011)", rec.Body.String())
		}
	})
}
