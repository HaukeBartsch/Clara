package admin

// Rate-limit blocks endpoint (API_Endpoints_Design.md §4.26, REQ-API-164): the
// live state of the per-source-IP limiter — every address currently inside its
// blockout with its expiry, plus the effective enable flag and thresholds so a
// reader can tell "nothing blocked" from "limiter off". Read-only and not
// audited, like §4.24/§4.25. Unlike §4.25's never-user-data snapshot this
// payload carries source IP addresses — personal data behind the is_admin
// boundary, consumed only by the Control Panel's Monitoring section (REQ-UI-064).

import (
	"net/http"
	"time"

	"csms/api/internal/dataapi"
)

func (h *Handler) registerSystemBlockedIps(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/blocked-ips", h.getSystemBlockedIps)
}

// blockedIpsResponse is the §4.26 shape: snapshot time, the effective settings
// (REQ-API-112), and the live blockout list. Schema rule as elsewhere: fields
// may be added; existing names and units never change.
type blockedIpsResponse struct {
	GeneratedAt  string              `json:"generated_at"` // canonical UTC (REQ-DB-005)
	Enabled      bool                `json:"enabled"`
	RPM          int                 `json:"rpm"`
	BlockMinutes int                 `json:"block_minutes"`
	Blocked      []dataapi.BlockedIP `json:"blocked"`
}

// getSystemBlockedIps answers from live limiter state at request time — nothing
// is persisted and blocks reset on restart like the limiter itself. A handler
// built without a limiter (nil Limiter) answers an empty list, never a panic.
func (h *Handler) getSystemBlockedIps(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	enabled, lim := dataapi.RateLimitSettings(r.Context(), h.Store)
	now := time.Now()
	writeJSON(w, http.StatusOK, blockedIpsResponse{
		GeneratedAt:  now.UTC().Format("2006-01-02 15:04:05"),
		Enabled:      enabled,
		RPM:          lim.RPM,
		BlockMinutes: int(lim.Block / time.Minute),
		Blocked:      h.Limiter.Blocked(now),
	})
}
