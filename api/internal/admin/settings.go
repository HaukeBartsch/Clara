package admin

// System settings (API_Endpoints_Design.md §4.22, REQ-API-112): the
// system-wide runtime registry stored in system_settings (REQ-DB-037). The
// rate limiter reads these values per request (§3.9), so an applied change
// takes effect on the next call without a restart.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
)

// Setting keys of the registry (REQ-DB-037 seed).
const (
	settingRateLimitEnabled = "rate_limit_enabled"
	settingRateLimitRPM     = "rate_limit_rpm"
)

// SettingsObject is the §4.22 response shape.
type SettingsObject struct {
	RateLimitEnabled bool `json:"rate_limit_enabled"`
	RateLimitRPM     int  `json:"rate_limit_rpm"`
}

func (h *Handler) registerSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", h.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", h.putSettings)
}

// settings reads the effective values: the stored rows when present, the
// seeded defaults otherwise.
func (h *Handler) settings(ctx context.Context) (SettingsObject, error) {
	s, err := h.Store.SystemSettings(ctx)
	if err != nil {
		return SettingsObject{}, err
	}
	out := SettingsObject{
		RateLimitEnabled: s[settingRateLimitEnabled] == "true",
		RateLimitRPM:     600,
	}
	if v, err := strconv.Atoi(s[settingRateLimitRPM]); err == nil && v >= 1 {
		out.RateLimitRPM = v
	}
	return out, nil
}

// getSettings returns the current system settings (is_admin, REQ-API-112).
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	s, err := h.settings(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// putSettings applies a subset of the settings idempotently (REQ-API-042):
// out-of-range values and unknown attributes are 400; an applied change is
// audit-logged with old and new values (REQ-AUD-027), and a PUT that changes
// nothing writes no entry (REQ-AUD-004).
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		errBadRequest(w, "invalid JSON body")
		return
	}
	cur, err := h.settings(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	old := map[string]string{
		settingRateLimitEnabled: strconv.FormatBool(cur.RateLimitEnabled),
		settingRateLimitRPM:     strconv.Itoa(cur.RateLimitRPM),
	}

	// Validate everything before writing anything.
	next := map[string]string{}
	for k, v := range raw {
		switch k {
		case settingRateLimitEnabled:
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				errBadRequest(w, "rate_limit_enabled must be a boolean")
				return
			}
			next[k] = strconv.FormatBool(b)
		case settingRateLimitRPM:
			var n int
			if err := json.Unmarshal(v, &n); err != nil || n < 1 {
				errBadRequest(w, "rate_limit_rpm must be an integer >= 1")
				return
			}
			next[k] = strconv.Itoa(n)
		default:
			errBadRequest(w, "unknown attribute "+k)
			return
		}
	}

	changes := map[string]map[string]string{}
	for k, v := range next {
		if old[k] == v {
			continue
		}
		if err := h.Store.SetSystemSetting(r.Context(), k, v); err != nil {
			errInternal(w)
			return
		}
		changes[k] = map[string]string{"old": old[k], "new": v}
	}
	if len(changes) > 0 {
		if err := h.Audit.Insert(r.Context(), audit.Entry{
			EventType: audit.SettingsUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email,
			Details: map[string]any{"changes": changes},
		}); err != nil {
			errInternal(w)
			return
		}
	}

	s, err := h.settings(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, s)
}
