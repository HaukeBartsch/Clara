package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// Preferred region (§4.30, REQ-API-175…178, master spec "Organization by hospital
// regions"). A region is a projects.organization code; a user's effective region decides
// which projects the overview shows first (REQ-UI-076) and nothing else — no
// authorization path reads it (REQ-AUTH-089).

// Organizations is the REQ-DB-006 enumeration in its fixed order — the one region
// allowlist of the API (Database_Schema_Design.md §4). The order is load-bearing: it
// breaks ties when a sign-in's roles map to several regions (REQ-AUTH-088), and the
// overview lists the groups after the preferred one in it (REQ-UI-076). The PHP layer
// keeps the display text in the same order (ControlPanelController::ORGANIZATIONS).
var Organizations = []string{"OUS", "Ahus", "SØ", "SiV", "ST", "SSHF", "SI", "VVHF", "SUN", "NAT EU", "OTHER"}

// validRegion reports whether code is one of the enumeration's codes.
func validRegion(code string) bool { return slices.Contains(Organizations, code) }

// regionRank is a code's position in the enumeration; a code withdrawn from it ranks
// after every listed one, so a stale mapping can still win only when nothing current does.
func regionRank(code string) int {
	if i := slices.Index(Organizations, code); i >= 0 {
		return i
	}
	return len(Organizations)
}

// maxRoleValues bounds how many distinct role values one sign-in records
// (Authentication_Authorization_Design.md §2.12): a directory that releases every group a
// person is in must not turn each sign-in into hundreds of writes.
const maxRoleValues = 50

func (h *Handler) registerRegions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/users/me/region", h.getMyRegion)
	mux.HandleFunc("PUT /api/v1/users/me/region", h.putMyRegion)
	mux.HandleFunc("GET /api/v1/provider-roles", h.listProviderRoles)
	mux.HandleFunc("POST /api/v1/provider-roles", h.createProviderRole)
	mux.HandleFunc("PUT /api/v1/provider-roles/{id}", h.updateProviderRole)
	mux.HandleFunc("DELETE /api/v1/provider-roles/{id}", h.deleteProviderRole)
}

// regionState is the §4.30 region shape, carried on its own by /users/me/region and
// inside every user object (REQ-API-176).
type regionState struct {
	Region                *string `json:"region"` // effective: own choice, else automatic, else null
	PreferredRegion       *string `json:"preferred_region"`
	AutomaticRegion       *string `json:"automatic_region"`
	AutomaticRegionSource *string `json:"automatic_region_source"` // provider | assignment
}

func newRegionState(u *db.User) regionState {
	s := regionState{
		PreferredRegion:       NullStrPtr(u.PreferredRegion),
		AutomaticRegion:       NullStrPtr(u.AutomaticRegion),
		AutomaticRegionSource: NullStrPtr(u.AutomaticRegionSource),
	}
	if r := u.EffectiveRegion(); r != "" {
		s.Region = &r
	}
	return s
}

// regionValue reads a body's region attribute: a code of the enumeration, or JSON null.
// ok is false for anything else, which callers answer with 400.
func regionValue(raw json.RawMessage) (sql.NullString, bool) {
	if string(raw) == "null" {
		return sql.NullString{}, true
	}
	var code string
	if json.Unmarshal(raw, &code) != nil || !validRegion(code) {
		return sql.NullString{}, false
	}
	return sql.NullString{String: code, Valid: true}, true
}

const regionValueMessage = "region must be one of OUS, Ahus, SØ, SiV, ST, SSHF, SI, VVHF, SUN, NAT EU, OTHER, or null"

// --- GET/PUT /api/v1/users/me/region (REQ-API-175) ---

// getMyRegion answers the acting user's region state. The account page reads it here
// rather than from its session copy of the user object, because an administrator's
// assignment can set a placeholder after that session began (§4.30).
func (h *Handler) getMyRegion(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, newRegionState(u))
}

// putMyRegion sets or clears the user's own choice. null is "Automatic": the automatic
// region applies again at once, since it was never overwritten (DEV-DB-18). Self-service
// like the theme (REQ-API-122), but audited (REQ-AUD-036); an unchanged value writes
// nothing (REQ-AUD-004).
func (h *Handler) putMyRegion(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "region") {
		return
	}
	raw, has := supplied["region"]
	if !has {
		errBadRequest(w, "region is required (send null for Automatic)")
		return
	}
	region, ok := regionValue(raw)
	if !ok {
		errBadRequest(w, regionValueMessage)
		return
	}
	if region != u.PreferredRegion {
		ctx := r.Context()
		err := h.auditTx(ctx, func(tx *sql.Tx) error {
			return h.Store.SetPreferredRegionTx(ctx, tx, u.ID, region)
		}, audit.Entry{
			EventType: audit.PreferredRegionChanged, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email,
			Details: map[string]any{"old": NullStrPtr(u.PreferredRegion), "new": NullStrPtr(region)},
		})
		if err != nil {
			errInternal(w)
			return
		}
		u.PreferredRegion = region
	}
	writeJSON(w, http.StatusOK, newRegionState(u))
}

// --- /api/v1/provider-roles (REQ-API-177/178) ---

// providerRoleObject is one mapping row as §4.30 returns it.
type providerRoleObject struct {
	ID          int64   `json:"id"`
	Issuer      string  `json:"issuer"`
	RoleValue   string  `json:"role_value"`
	Region      *string `json:"region"`
	Reviewed    bool    `json:"reviewed"`
	FirstSeenAt *string `json:"first_seen_at"`
	LastSeenAt  *string `json:"last_seen_at"`
	SeenCount   int64   `json:"seen_count"`
}

func newProviderRoleObject(m *db.ProviderRoleRegion) providerRoleObject {
	return providerRoleObject{
		ID: m.ID, Issuer: m.Issuer, RoleValue: m.RoleValue, Region: NullStrPtr(m.Region),
		Reviewed: m.Reviewed, FirstSeenAt: NullStrPtr(m.FirstSeenAt), LastSeenAt: NullStrPtr(m.LastSeenAt),
		SeenCount: m.SeenCount,
	}
}

// regionMappingEntry is the region_mapping_changed entry of one decision (REQ-AUD-036).
func regionMappingEntry(u *db.User, issuer, value, action string, oldRegion, newRegion sql.NullString) audit.Entry {
	return audit.Entry{
		EventType: audit.RegionMappingChanged, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email,
		Details: map[string]any{
			"issuer": issuer, "role_value": value, "action": action,
			"region": map[string]any{"old": NullStrPtr(oldRegion), "new": NullStrPtr(newRegion)},
		},
	}
}

// listProviderRoles returns the whole mapping, undecided values first (REQ-API-177). It
// is not paginated: it holds distinct role values, not sign-ins.
func (h *Handler) listProviderRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	rows, err := h.Store.ListProviderRoleRegions(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]providerRoleObject, 0, len(rows))
	for i := range rows {
		out = append(out, newProviderRoleObject(&rows[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// createProviderRole enters a value before any sign-in has carried it (REQ-API-178) —
// typically one the Authentication Test's preview showed. The issuer must be a configured
// provider's, so no row can name a provider that will never sign anyone in.
func (h *Handler) createProviderRole(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "issuer", "role_value", "region") {
		return
	}
	var issuer, value string
	if json.Unmarshal(supplied["issuer"], &issuer) != nil {
		errBadRequest(w, "issuer is required")
		return
	}
	if _, configured := h.providerByIssuer(issuer); !configured {
		errBadRequest(w, "issuer must name a configured OAuth2 provider")
		return
	}
	if json.Unmarshal(supplied["role_value"], &value) != nil {
		errBadRequest(w, "role_value is required")
		return
	}
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > db.MaxRoleValueLength {
		errBadRequest(w, "role_value must be 1 to 255 characters")
		return
	}
	raw, has := supplied["region"]
	if !has {
		errBadRequest(w, "region is required (null records that the value names no region)")
		return
	}
	region, ok := regionValue(raw)
	if !ok {
		errBadRequest(w, regionValueMessage)
		return
	}
	existing, err := h.Store.GetProviderRoleRegionByValue(ctx, issuer, value)
	if err != nil {
		errInternal(w)
		return
	}
	if existing != nil {
		errConflict(w, "this role value is already listed")
		return
	}
	row := &db.ProviderRoleRegion{Issuer: issuer, RoleValue: value, Region: region}
	if err := h.auditTx(ctx, func(tx *sql.Tx) error {
		id, err := h.Store.CreateProviderRoleRegionTx(ctx, tx, row)
		row.ID = id
		return err
	}, regionMappingEntry(u, issuer, value, "add", sql.NullString{}, region)); err != nil {
		errInternal(w)
		return
	}
	created, err := h.Store.GetProviderRoleRegion(ctx, row.ID)
	if err != nil || created == nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, newProviderRoleObject(created))
}

// updateProviderRole decides on a value: a region, or null for "names no region".
// Either way the row is reviewed. A decision that changes nothing writes no entry.
func (h *Handler) updateProviderRole(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid id")
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "region") {
		return
	}
	raw, has := supplied["region"]
	if !has {
		errBadRequest(w, "region is required (null records that the value names no region)")
		return
	}
	region, ok := regionValue(raw)
	if !ok {
		errBadRequest(w, regionValueMessage)
		return
	}
	row, err := h.Store.GetProviderRoleRegion(ctx, id)
	if err != nil {
		errInternal(w)
		return
	}
	if row == nil {
		errNotFound(w)
		return
	}
	if !row.Reviewed || row.Region != region {
		if err := h.auditTx(ctx, func(tx *sql.Tx) error {
			return h.Store.UpdateProviderRoleRegionTx(ctx, tx, id, region)
		}, regionMappingEntry(u, row.Issuer, row.RoleValue, "update", row.Region, region)); err != nil {
			errInternal(w)
			return
		}
		row.Region, row.Reviewed = region, true
	}
	writeJSON(w, http.StatusOK, newProviderRoleObject(row))
}

// deleteProviderRole removes a row; a later sign-in carrying the value enters it again
// as undecided (REQ-API-178).
func (h *Handler) deleteProviderRole(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid id")
		return
	}
	row, err := h.Store.GetProviderRoleRegion(ctx, id)
	if err != nil {
		errInternal(w)
		return
	}
	if row == nil {
		errNotFound(w)
		return
	}
	if err := h.auditTx(ctx, func(tx *sql.Tx) error {
		return h.Store.DeleteProviderRoleRegionTx(ctx, tx, id)
	}, regionMappingEntry(u, row.Issuer, row.RoleValue, "remove", row.Region, sql.NullString{})); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- region from provider roles at sign-in (REQ-AUTH-088, §2.12) ---

// normalizeRoleValues turns a roles claim into the values a sign-in records: a JSON string
// is one value, an array contributes its string elements, any other type nothing. Values
// are trimmed, empty ones dropped and duplicates collapsed; at most maxRoleValues are
// kept, and a value longer than the mapping stores is skipped rather than truncated into
// a different one. The counts of both limits are returned for the log — never the values.
func normalizeRoleValues(raw json.RawMessage) (values []string, tooLong, overLimit int) {
	var candidates []string
	var one string
	if json.Unmarshal(raw, &one) == nil {
		candidates = []string{one}
	} else {
		var many []any
		if json.Unmarshal(raw, &many) != nil {
			return nil, 0, 0
		}
		for _, item := range many {
			if s, ok := item.(string); ok {
				candidates = append(candidates, s)
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		v := strings.TrimSpace(c)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		if utf8.RuneCountInString(v) > db.MaxRoleValueLength {
			tooLong++
			continue
		}
		if len(values) == maxRoleValues {
			overLimit++
			continue
		}
		values = append(values, v)
	}
	return values, tooLong, overLimit
}

// deriveRegion picks the region a sign-in sets from the values it carried and the
// mapping: the first in enumeration order among the regions they map to, or "" when
// none maps (REQ-AUTH-088).
func deriveRegion(values []string, mapped map[string]string) string {
	best := ""
	for _, v := range values {
		region, ok := mapped[v]
		if !ok || region == "" {
			continue
		}
		if best == "" || regionRank(region) < regionRank(best) {
			best = region
		}
	}
	return best
}

// applyProviderRegion is Sequence C step 3.5 (Authentication_Authorization_Design.md
// §2.12): record the role values the verified assertion carried and, when one maps to a
// region, make it the account's automatic region. It never refuses a login — a failure
// is logged and the sign-in completes as it would without the roles claim. u is updated
// in place so the login response carries the region (REQ-API-176).
func (h *Handler) applyProviderRegion(ctx context.Context, a *verifiedAssertion, u *db.User, now time.Time) {
	if a == nil || !a.RolesPresent {
		return
	}
	if a.RolesTooLong > 0 || a.RolesOverLimit > 0 {
		slog.Info("roles claim values skipped", "issuer", a.Issuer,
			"too_long", a.RolesTooLong, "over_limit", a.RolesOverLimit)
	}
	if err := h.Store.RecordRoleSightings(ctx, a.Issuer, a.RoleValues, now); err != nil {
		slog.Error("role values not recorded", "issuer", a.Issuer, "user_id", u.ID, "err", err)
		return
	}
	mapped, err := h.Store.MappedRegions(ctx, a.Issuer, a.RoleValues)
	if err != nil {
		slog.Error("role mapping not read", "issuer", a.Issuer, "user_id", u.ID, "err", err)
		return
	}
	region := deriveRegion(a.RoleValues, mapped)
	if region == "" {
		return // no mapped value says nothing about the region: leave it as it is
	}
	if u.AutomaticRegion.String == region && u.AutomaticRegionSource.String == db.RegionSourceProvider {
		return
	}
	if err := h.Store.SetProviderRegion(ctx, u.ID, region); err != nil {
		slog.Error("provider region not stored", "issuer", a.Issuer, "user_id", u.ID, "err", err)
		return
	}
	u.AutomaticRegion = sql.NullString{String: region, Valid: true}
	u.AutomaticRegionSource = sql.NullString{String: db.RegionSourceProvider, Valid: true}
}

// regionPreview is the Authentication Test's view of the same derivation (§2.11): the
// configured claim, each value with what the mapping says about it, and the region a
// sign-in would set. It records nothing and changes no account.
func (h *Handler) regionPreview(ctx context.Context, rolesAttr string, a *verifiedAssertion) (map[string]any, error) {
	rows := make([]map[string]any, 0, len(a.RoleValues))
	mapped := map[string]string{}
	for _, v := range a.RoleValues {
		m, err := h.Store.GetProviderRoleRegionByValue(ctx, a.Issuer, v)
		if err != nil {
			return nil, err
		}
		row := map[string]any{"value": truncateDiagnostic(v, diagnosticValueLimit), "listed": m != nil,
			"reviewed": false, "region": nil}
		if m != nil {
			row["reviewed"] = m.Reviewed
			row["region"] = NullStrPtr(m.Region)
			if m.Reviewed && m.Region.Valid {
				mapped[v] = m.Region.String
			}
		}
		rows = append(rows, row)
	}
	out := map[string]any{"claim": rolesAttr, "claim_present": a.RolesPresent, "values": rows, "region": nil}
	if region := deriveRegion(a.RoleValues, mapped); region != "" {
		out["region"] = region
	}
	return out, nil
}
