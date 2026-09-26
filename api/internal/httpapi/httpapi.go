// Package httpapi assembles the Go API's routing and middleware
// (Technology_Stack_Design.md §4): the administration boundary (service
// token + acting user, §6 of Authentication_Authorization_Design.md), the
// health endpoint, and the mounts for the data API, the administration API,
// and the documentation endpoints.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"csms/api/internal/admin"
	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/config"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// NewMux builds the full route table. The administration surface is wrapped
// in the service-token boundary; the data API and health endpoint are public
// (nginx additionally keeps /api/v1/* and /docs off the public network —
// Technology_Stack_Design.md §5).
func NewMux(store *db.Store, cfg *config.Config, aw *audit.Writer) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthHandler(store))

	data := &dataapi.Handler{Store: store, Cfg: cfg}
	if cfg.RateLimitEnabled { // off by default (REQ-CFG-020)
		data.Limiter = dataapi.NewRateLimiter(cfg.RateLimitRPM)
	}
	mux.Handle("/api/", data)

	adminAPI := admin.New(store, cfg, aw)
	mux.Handle("/api/v1/", AdminBoundary(adminAPI.Handler, store, cfg, aw))

	return mux
}

// loginPath is the sole administration endpoint exempt from
// X-Internal-User-Id (Authentication_Authorization_Design.md §2.3).
const loginPath = "/api/v1/auth/login"

// AdminBoundary enforces the internal trust boundary on /api/v1/*
// (REQ-API-041, REQ-AUTH-011…013): a valid X-Internal-Service-Token
// (constant-time compare, never logged) and — except for login — an active
// acting user from the authoritative X-Internal-User-Id header. Rejections
// are audit-logged as admin_rejected (REQ-AUD-008).
func AdminBoundary(next http.Handler, store *db.Store, cfg *config.Config, aw *audit.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get("X-Internal-Service-Token")
		if subtle.ConstantTimeCompare([]byte(presented), []byte(cfg.InternalServiceToken)) != 1 {
			rejectAdmin(w, r, aw, "service_token_invalid")
			return
		}
		if r.URL.Path == loginPath {
			next.ServeHTTP(w, r) // service token only; the identity rides in the body
			return
		}

		raw := r.Header.Get("X-Internal-User-Id")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			rejectAdmin(w, r, aw, "user_unknown")
			return
		}
		u, err := store.GetUser(r.Context(), id)
		if err != nil {
			rejectAdmin(w, r, aw, "user_unknown")
			return
		}
		state, err := authz.CheckActive(r.Context(), store, aw, cfg, u, time.Now())
		if err != nil {
			internalError(w)
			return
		}
		if state != authz.Active {
			rejectAdmin(w, r, aw, "user_disabled")
			return
		}
		next.ServeHTTP(w, r.WithContext(authz.WithActor(r.Context(), u)))
	})
}

// rejectAdmin answers 401 or 403 in the §4.2 shape and writes the
// admin_rejected audit entry (reason vocabulary per Audit §3.1).
func rejectAdmin(w http.ResponseWriter, r *http.Request, aw *audit.Writer, reason string) {
	code := "forbidden"
	status := http.StatusForbidden
	if reason == "service_token_invalid" {
		code = "service_token_invalid"
		status = http.StatusUnauthorized
	}
	admin.APIError(w, status, code, r.URL.Path)
	_ = aw.Insert(r.Context(), audit.Entry{
		EventType: audit.AdminRejected,
		Source:    audit.SourceUI,
		Details: map[string]string{
			"path":   r.Method + " " + r.URL.Path,
			"reason": reason,
		},
	})
}

func internalError(w http.ResponseWriter) {
	admin.APIError(w, http.StatusInternalServerError, "internal", "internal error")
}

// healthHandler reports liveness plus a bounded database check
// (REQ-API-003): 200 {"status":"ok","db":"ok"}, or 503 degraded.
func healthHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "ok"
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.DB.PingContext(ctx); err != nil {
			dbStatus = "error"
		}
		code := http.StatusOK
		body := map[string]string{"status": "ok", "db": dbStatus}
		if dbStatus != "ok" {
			code = http.StatusServiceUnavailable
			body["status"] = "degraded"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
}
