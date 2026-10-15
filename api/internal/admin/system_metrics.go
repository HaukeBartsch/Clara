package admin

// Performance metrics endpoint (API_Endpoints_Design.md §4.25, REQ-API-163):
// the in-process measurement snapshot — per-route latency histograms and
// payload bytes for this API's own work, per-content data-API timings,
// per-verb database statement timings with row counts, pool waits, dials, and
// process memory/GC — sufficient to evaluate infrastructure and database
// extensions against the REQ-TECH-010/011 targets. Facts, never ratings; the
// snapshot carries only closed-set labels (route templates, verbs, content
// vocabulary) and no secrets, user data, or query text (REQ-API-005/006).
// Values live in memory and reset on restart by design.

import (
	"net/http"

	"csms/api/internal/metrics"
)

func (h *Handler) registerSystemMetrics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/metrics", h.getSystemMetrics)
}

// getSystemMetrics answers the snapshot (is_admin only, REQ-API-163; 403 for
// everyone else via requireAdmin). Read-only and not audited, like §4.24. The
// live pool numbers are read here rather than in the registry so the metrics
// package stays free of database/sql imports.
func (h *Handler) getSystemMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	snap := h.Metrics.Snapshot() // nil-safe: an empty snapshot, never a panic
	st := h.Store.DB.Stats()
	snap.DB.Pool = &metrics.PoolStats{
		MaxOpen:       st.MaxOpenConnections,
		Open:          st.OpenConnections,
		InUse:         st.InUse,
		Idle:          st.Idle,
		WaitCount:     st.WaitCount,
		WaitMs:        st.WaitDuration.Milliseconds(),
		MaxIdleClosed: st.MaxIdleClosed,
	}
	writeJSON(w, http.StatusOK, snap)
}
