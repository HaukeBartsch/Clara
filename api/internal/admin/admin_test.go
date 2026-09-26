package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

// env is the shared test fixture: a migrated SQLite store, an audit writer,
// and the assembled administration handler (the boundary middleware lives in
// httpapi and is exercised there — handlers take the actor from the context).
type env struct {
	t     *testing.T
	Store *db.Store
	Cfg   *config.Config
	Audit *audit.Writer
	Handler *Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := &config.Config{
		AppEnv:               "development",
		DBConnection:         "sqlite",
		DBDatabase:           filepath.Join(t.TempDir(), "admin.sqlite"),
		AnonSalt:             "test-salt",
		InternalServiceToken: "test-token",
		WebPublicURL:         "https://csms.example.org",
	}
	store, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	aw := audit.NewWriter(store.DB, string(store.Dialect))
	if err := aw.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}
	return &env{t: t, Store: store, Cfg: cfg, Audit: aw, Handler: New(store, cfg, aw)}
}

// do runs one request against the administration mux. A non-nil actor is
// placed in the context the way the boundary middleware does; pass nil for
// endpoints reached without an acting user (login).
func (e *env) do(method, path string, body any, actor *db.User) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf *bytes.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			buf = bytes.NewReader([]byte(b))
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				e.t.Fatalf("marshal body: %v", err)
			}
			buf = bytes.NewReader(raw)
		}
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	if actor != nil {
		req = req.WithContext(authz.WithActor(req.Context(), actor))
	}
	rec := httptest.NewRecorder()
	e.Handler.Handler.ServeHTTP(rec, req)
	return rec
}

// decode unmarshals a response body.
func (e *env) decode(rec *httptest.ResponseRecorder, v any) {
	e.t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		e.t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

// mustAdmin inserts an administrator user.
func (e *env) mustAdmin(email string) *db.User {
	e.t.Helper()
	u := &db.User{Email: email, DisplayName: "Admin", Enabled: true, IsAdmin: true}
	id, err := e.Store.CreateUser(context.Background(), u)
	if err != nil {
		e.t.Fatalf("CreateUser: %v", err)
	}
	u.ID = id
	return u
}

// mustUser inserts a regular user.
func (e *env) mustUser(email string) *db.User {
	e.t.Helper()
	u := &db.User{Email: email, DisplayName: "User", Enabled: true}
	id, err := e.Store.CreateUser(context.Background(), u)
	if err != nil {
		e.t.Fatalf("CreateUser: %v", err)
	}
	u.ID = id
	return u
}

// mustProject inserts a minimal project.
func (e *env) mustProject(name string) int64 {
	e.t.Helper()
	id, err := e.Store.CreateProject(context.Background(), &db.Project{
		ProjectName: name, ParticipantNames: "REC[0-9][0-9][0-9]",
	})
	if err != nil {
		e.t.Fatalf("CreateProject: %v", err)
	}
	return id
}

// auditTypes returns the event_type values written so far (both sources).
func (e *env) auditTypes() []string {
	e.t.Helper()
	rows, err := e.Store.DB.Query(`SELECT event_type FROM audit_events ORDER BY id`)
	if err != nil {
		e.t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatalf("audit scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}
