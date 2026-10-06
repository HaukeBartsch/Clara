// Package admin implements the administration API surface under /api/v1/
// (API_Endpoints_Design.md §4) — one file per area. The boundary itself
// (service token + acting user, audit admin_rejected) is the httpapi
// middleware; handlers here work with the actor from the request context
// (authz.ActorFrom) and the permission summary of §5.
package admin

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/config"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
	"csms/api/internal/mailer"
)

// Handler serves the administration API against one store. Areas register
// their routes on the mux passed to New (registerAuth, registerUsers, …).
type Handler struct {
	Store   *db.Store
	Cfg     *config.Config
	Audit   *audit.Writer
	Mail    *mailer.Mailer   // email second-factor codes and setup links (REQ-AUTH-057/060/062)
	Data    *dataapi.Handler // serves the shared streaming export core (§4.14)
	Handler http.Handler     // the /api/v1/ mux, built by New

	// SendMail overrides Mail.Send for tests; nil uses the configured relay.
	SendMail func(to, subject, body string) error

	tfaSends   *sendLimiter    // per-account cap on emailed codes (in-memory)
	resetSends *addressLimiter // per-address cap on reset requests (REQ-API-119)
	lockouts   *authLockout    // Sequence E brute-force store (§2.5)
}

// New builds the administration handler and registers every area's routes.
func New(store *db.Store, cfg *config.Config, aw *audit.Writer) *Handler {
	h := &Handler{Store: store, Cfg: cfg, Audit: aw, Mail: mailer.New(cfg),
		Data:     &dataapi.Handler{Store: store, Cfg: cfg, Audit: aw},
		tfaSends: newSendLimiter(), resetSends: newAddressLimiter(), lockouts: newAuthLockout()}
	mux := http.NewServeMux()
	h.registerAuth(mux)
	h.registerTfa(mux)
	h.registerUsers(mux)
	h.registerPasswords(mux)
	h.registerProjects(mux)
	h.registerModes(mux)
	h.registerStaging(mux)
	h.registerMembers(mux)
	h.registerRoles(mux)
	h.registerStructure(mux)
	h.registerDAG(mux)
	h.registerSurvey(mux)
	h.registerExport(mux)
	h.registerEndProvision(mux)
	h.registerQueries(mux)
	h.registerCompletion(mux)
	h.registerI18n(mux)
	h.registerSettings(mux)
	h.Handler = mux
	return h
}

// NewUUID returns a random UUID v4 string (crypto/rand, canonical hyphenated
// 8-4-4-4-12 form) — the shape of project tokens and survey-link tokens
// (GD-5, GD-9). Values are never logged or written to audit details.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("admin: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- JSON contract (§4.2) ---

// ErrorBody is the uniform administration error object: a stable machine
// code, a human-readable message that never leaks internals (REQ-API-006),
// and the HTTP status.
type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// APIError renders the §4.2 error shape; exported for the boundary
// middleware in httpapi.
func APIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: code, Message: message, Status: status})
}

// Convenience helpers for the fixed code table of §4.2.
func errBadRequest(w http.ResponseWriter, msg string) {
	APIError(w, http.StatusBadRequest, "invalid_request", msg)
}
func errValidation(w http.ResponseWriter, msg string) {
	APIError(w, http.StatusBadRequest, "validation_error", msg)
}
func errForbidden(w http.ResponseWriter) {
	APIError(w, http.StatusForbidden, "forbidden", "forbidden")
}
func errNotFound(w http.ResponseWriter) {
	APIError(w, http.StatusNotFound, "not_found", "not found")
}
func errConflict(w http.ResponseWriter, msg string) {
	APIError(w, http.StatusConflict, "conflict", msg)
}
func errInternal(w http.ResponseWriter) {
	APIError(w, http.StatusInternalServerError, "internal", "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeBody decodes a JSON request body; malformed input is 400
// invalid_request (REQ-API-042 idempotent PUTs reuse it). An empty body
// decodes into the zero value.
func decodeBody(r *http.Request, v any) error {
	body := r.Body
	if body == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(body, 8<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// pathID parses a numeric path parameter (ServeMux r.PathValue).
func pathID(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

// pathObjectID parses the id of an object a structure route addresses. Unlike
// pathID it admits a negative value: while a staging set is open the structure
// endpoints hand out provisional ids (staged_design.go), and an object created
// during staging has no live row to be addressed by anything else — so
// `…/instruments/-1/fields` names the staged instrument, not a malformed id.
// Zero is still rejected; it names nothing on either side.
func pathObjectID(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n != 0
}

// --- acting user and guards ---

// actor returns the acting user set by the middleware. A missing actor means
// a handler was mounted outside the boundary — treated as forbidden.
func actor(r *http.Request) (*db.User, bool) {
	u := authz.ActorFrom(r.Context())
	return u, u != nil
}

// access resolves the acting user's effective levels on a project (§4.1).
func (h *Handler) access(ctx context.Context, u *db.User, projectID int64) (*authz.Levels, error) {
	return authz.Effective(ctx, h.Store, u, projectID)
}

// requireMember guards project visibility: non-members get the uniform 403
// that never discloses existence (REQ-API-007). Returns false when rejected.
func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request, lv *authz.Levels) bool {
	if !lv.IsMember {
		errForbidden(w)
		return false
	}
	return true
}

// requireProjectAdmin guards project_admin (is_admin covered by REQ-AUTH-023).
func (h *Handler) requireProjectAdmin(w http.ResponseWriter, r *http.Request, lv *authz.Levels) bool {
	if !lv.ProjectAdmin {
		errForbidden(w)
		return false
	}
	return true
}

// requireAdmin guards the is_admin-only endpoints (§5).
func requireAdmin(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	u, ok := actor(r)
	if !ok || !u.IsAdmin {
		errForbidden(w)
		return nil, false
	}
	return u, true
}

// requireData guards data access ≥ min on an arm (GD-2).
func (h *Handler) requireData(w http.ResponseWriter, r *http.Request, lv *authz.Levels, armNum, min int) bool {
	if !lv.HasData(armNum, min) {
		errForbidden(w)
		return false
	}
	return true
}

// Data level rank constants re-exported for area files — the values are
// authz's, so no second ladder can drift here (REQ-DB-009 as revised). The
// ladder ends at view_edit: delete and survey editing are rights of a grant
// (REQ-AUTH-018/070), tested through authz.PairLevels.
const (
	LvlReadOnly = authz.RankReadOnly
	LvlViewEdit = authz.RankViewEdit
)

// --- pagination (§1 convention) ---

// Cursor is the opaque last-seen (created_at, id) pair.
type Cursor struct {
	CreatedAt string `json:"c"`
	ID        int64  `json:"i"`
}

// ParseLimit reads the limit parameter (default 50, max 200).
func ParseLimit(r *http.Request) int {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	return limit
}

// ParseCursor decodes the opaque cursor; absent or unparseable = first page.
func ParseCursor(r *http.Request) *Cursor {
	v := r.URL.Query().Get("cursor")
	if v == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil
	}
	var c Cursor
	if json.Unmarshal(raw, &c) != nil || c.CreatedAt == "" {
		return nil
	}
	return &c
}

// NextCursor renders the cursor for the last row of a page, or null JSON
// when the page was not full (exhausted).
func NextCursor(rows int, limit int, createdAt string, id int64) any {
	if rows < limit {
		return nil
	}
	raw, _ := json.Marshal(Cursor{CreatedAt: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// --- shared object shapes ---

// UserObject is the user representation of §4.3, used by login and all user
// endpoints. status is derived (GD-19).
type UserObject struct {
	ID          int64   `json:"id"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Enabled     bool    `json:"enabled"`
	IsAdmin     bool    `json:"is_admin"`
	AuthSource  string  `json:"auth_source"`
	UILanguage  string  `json:"ui_language"`
	UITheme     *string `json:"ui_theme"` // null = follow installation default UI_THEME (GD-26)
	LastLoginAt *string `json:"last_login_at"`
	ValidUntil  *string `json:"valid_until"`
	Status      string  `json:"status"`
	TFAMethod   string  `json:"tfa_method"` // off | totp | email (REQ-API-116)
}

// NewUserObject maps a db.User to the response shape. tfa_method defaults to
// "off"; callers that know the account's second factor overwrite it (the
// users listing batches the lookup, login reads its own row).
func NewUserObject(u *db.User, now time.Time) UserObject {
	o := UserObject{
		ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
		Enabled: u.Enabled, IsAdmin: u.IsAdmin, AuthSource: u.AuthSource,
		UILanguage: u.UILanguage, UITheme: NullStrPtr(u.UITheme),
		Status: authz.UserStatus(u, now), TFAMethod: "off",
	}
	if u.LastLoginAt.Valid {
		s := u.LastLoginAt.String
		o.LastLoginAt = &s
	}
	if u.ValidUntil.Valid {
		s := u.ValidUntil.String
		o.ValidUntil = &s
	}
	return o
}

// --- small shared helpers for area files ---

// nullStrPtr maps a sql.NullString to *string (JSON null when unset).
func NullStrPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

// int64Ptr maps a sql.NullInt64 to *int64 (JSON null when unset).
func Int64Ptr(ni sql.NullInt64) *int64 {
	if !ni.Valid {
		return nil
	}
	v := ni.Int64
	return &v
}

// parseUTCTime parses the canonical DATETIME layout for filter parameters.
func ParseUTCTime(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// uniqueStrings deduplicates, preserving order.
func UniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- canonical event listing (§4.8/§4.9) ---

// EventObject is the shared event representation of §4.8/§4.9; period and
// safe-region bounds are null when the event has no timepoint (GD-15).
type EventObject struct {
	ID              int64  `json:"id"`
	ArmNum          int    `json:"arm_num"`
	EventName       string `json:"event_name"`
	UniqueEventName string `json:"unique_event_name"`
	Period          *int64 `json:"period"`
	SafeRegionStart *int64 `json:"safe_region_start"`
	SafeRegionEnd   *int64 `json:"safe_region_end"`
	Position        int    `json:"position"`
}

// NewEventObject maps a db.Event to the response shape.
func NewEventObject(e db.Event) EventObject {
	return EventObject{
		ID: e.ID, ArmNum: 0, EventName: e.EventName, UniqueEventName: e.UniqueEventName,
		Period:          Int64Ptr(e.Period),
		SafeRegionStart: Int64Ptr(e.SafeRegionStart), SafeRegionEnd: Int64Ptr(e.SafeRegionEnd),
		Position: e.Position,
	}
}

// CanonicalEvents returns the project's arms (arm_num ascending) with each
// arm's events in the canonical order of GD-15. The arm number is stamped
// onto every event object (§4.9).
func (h *Handler) CanonicalEvents(ctx context.Context, projectID int64) ([]db.Arm, map[int][]EventObject, error) {
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	byArm := map[int64][]db.Event{}
	for _, e := range events {
		byArm[e.ArmID] = append(byArm[e.ArmID], e)
	}
	out := map[int][]EventObject{}
	for _, a := range arms {
		list := byArm[a.ID]
		SortEventsCanonical(list)
		objs := make([]EventObject, 0, len(list))
		for _, e := range list {
			o := NewEventObject(e)
			o.ArmNum = a.ArmNum
			objs = append(objs, o)
		}
		out[a.ArmNum] = objs
	}
	return arms, out, nil
}

// SortEventsCanonical orders an arm's events per GD-15: events with a
// timepoint first by period ascending (ties by position), then no-timepoint
// events by position. Applies to every event listing (§4.9).
func SortEventsCanonical(events []db.Event) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		an, ab := a.Period.Valid, b.Period.Valid
		switch {
		case an && ab:
			if a.Period.Int64 != b.Period.Int64 {
				return a.Period.Int64 < b.Period.Int64
			}
		case an != ab:
			return an // timepoint events first
		}
		return a.Position < b.Position
	})
}

// errUnknownAttrs rejects attributes removed by GD-17 or otherwise unknown
// (400 invalid_request, REQ-API-052).
func RejectUnknownAttrs(w http.ResponseWriter, supplied map[string]json.RawMessage, allowed ...string) bool {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var bad []string
	for k := range supplied {
		if !ok[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		errBadRequest(w, "unknown attributes: "+strings.Join(bad, ", "))
		return false
	}
	return true
}
