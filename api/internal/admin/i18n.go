package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

// registerI18n mounts §4.19: the language list, the acting user's own UI
// language and theme, and the translation table (REQ-API-097…100/122). English is the
// fallback everywhere — a key with no translation in the requested language
// renders in English rather than blank (REQ-UI-008) — which also means the
// English rows define the key universe the missing flag is computed against.
func (h *Handler) registerI18n(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/i18n/languages", h.listLanguages)
	mux.HandleFunc("GET /api/v1/i18n/bundle", h.getBundle)
	mux.HandleFunc("PUT /api/v1/users/me/ui-language", h.putMyUILanguage)
	mux.HandleFunc("PUT /api/v1/users/me/ui-theme", h.putMyUITheme)
	mux.HandleFunc("GET /api/v1/i18n/strings", h.listStrings)
	mux.HandleFunc("PUT /api/v1/i18n/strings", h.putStrings)
}

// --- GET /api/v1/i18n/languages (§4.19, REQ-API-097) ---

// languageObject is the enabled-language representation of §4.19 — code and
// display name only; the id is an internal detail.
type languageObject struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name"`
}

// listLanguages returns the languages a user can choose. Any authenticated
// user may call it: the login shell and the footer selector both need it before
// any project context exists (REQ-API-097).
func (h *Handler) listLanguages(w http.ResponseWriter, r *http.Request) {
	if _, ok := actor(r); !ok {
		errForbidden(w)
		return
	}
	languages, err := h.Store.ListLanguages(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]languageObject, 0, len(languages))
	for _, l := range languages {
		if !l.Enabled {
			continue
		}
		out = append(out, languageObject{Code: l.Code, DisplayName: l.DisplayName})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- PUT /api/v1/users/me/ui-language (§4.19, REQ-API-098) ---

// putMyUILanguage sets the acting user's own UI language. Self-service: there
// is no target user in the path, and no is_admin gate. An unknown or disabled
// language is 400 — the setting must resolve to something the renderer can use.
//
// No audit entry: the catalog carries i18n_updated for translation changes
// (REQ-API-100) and user_updated for an administrator changing an account
// (REQ-API-048); a member choosing their own display language is neither, and
// inventing a code for it would widen the trail without a requirement behind it.
func (h *Handler) putMyUILanguage(w http.ResponseWriter, r *http.Request) {
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
	if !RejectUnknownAttrs(w, supplied, "language") {
		return
	}
	var code string
	if raw, has := supplied["language"]; !has || json.Unmarshal(raw, &code) != nil || code == "" {
		errBadRequest(w, "language is required")
		return
	}
	ctx := r.Context()
	lang, err := h.Store.GetLanguageByCode(ctx, code)
	if err != nil {
		errInternal(w)
		return
	}
	if lang == nil || !lang.Enabled {
		errBadRequest(w, "unknown or disabled language")
		return
	}
	if err := h.Store.SetUILanguage(ctx, u.ID, code); err != nil {
		errInternal(w)
		return
	}
	u.UILanguage = code // the response carries the new setting (REQ-API-098)
	writeJSON(w, http.StatusOK, NewUserObject(u, time.Now().UTC()))
}

// --- PUT /api/v1/users/me/ui-theme (§4.19, REQ-API-122) ---

// putMyUITheme sets the acting user's own theme override (GD-26). Self-service
// like the language: no target user in the path, no is_admin gate. The value
// must name an installed theme (REQ-TECH-027) or be null to clear the override
// and follow the installation default UI_THEME (REQ-CFG-031); anything else is
// 400. The API only stores the identifier — the PHP layer resolves the
// effective theme and links exactly one stylesheet at render time (REQ-UI-040).
func (h *Handler) putMyUITheme(w http.ResponseWriter, r *http.Request) {
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
	if !RejectUnknownAttrs(w, supplied, "theme") {
		return
	}
	raw, has := supplied["theme"]
	if !has {
		errBadRequest(w, "theme is required (send null to follow the installation default)")
		return
	}
	theme := sql.NullString{} // JSON null = no override (REQ-DB-008)
	if string(raw) != "null" {
		var id string
		if json.Unmarshal(raw, &id) != nil || !config.ValidUITheme(id) {
			errBadRequest(w, "theme must name an installed theme (bootstrap, darkly, yeti) or be null")
			return
		}
		theme = sql.NullString{String: id, Valid: true}
	}
	if err := h.Store.SetUITheme(r.Context(), u.ID, theme); err != nil {
		errInternal(w)
		return
	}
	u.UITheme = theme // the response carries the new setting (REQ-API-122)
	writeJSON(w, http.StatusOK, NewUserObject(u, time.Now().UTC()))
}

// --- GET /api/v1/i18n/bundle (§4.19, REQ-API-124) ---

// getBundle serves one language's translations as a flat key→text map for
// render-time overlay (REQ-API-124). English is the application's source of
// truth and lives in the PHP layer, not the table (REQ-DB-031), so this is
// the member-accessible read the shell needs: PHP overlays the bundle on its
// own English strings; a key absent here renders in English. Any
// authenticated user may call it — every page renders translated (§9 of
// User_Interface_Design.md) and PHP has no database access (REQ-TECH-006).
// Without an explicit language parameter the acting user's stored
// ui_language is served (default en, REQ-API-098).
func (h *Handler) getBundle(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	code := r.URL.Query().Get("language")
	if code == "" {
		code = u.UILanguage
		if code == "" {
			code = defaultUILanguage
		}
	}
	ctx := r.Context()
	lang, err := h.Store.GetLanguageByCode(ctx, code)
	if err != nil {
		errInternal(w)
		return
	}
	if lang == nil || !lang.Enabled {
		errBadRequest(w, "unknown or disabled language")
		return
	}
	strings, err := h.Store.ListI18nStringsByCode(ctx, code)
	if err != nil {
		errInternal(w)
		return
	}
	if strings == nil {
		strings = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"language": code, "strings": strings})
}

// --- GET / PUT /api/v1/i18n/strings (§4.19, REQ-API-099/100) ---

// stringObject is one translation key of the §4.19 listing. missing is true for
// a key the language has no row for; text is then empty and the renderer falls
// back to English (REQ-DB-031).
type stringObject struct {
	Key     string `json:"key"`
	Text    string `json:"text"`
	Missing bool   `json:"missing"`
}

// listStrings returns one language's keys with the missing flag (REQ-API-099),
// reserved to is_admin. The key universe is the English rows unioned with the
// language's own, so a key that exists only in the target language still shows
// up (and can be cleared) rather than becoming unreachable.
func (h *Handler) listStrings(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	code := r.URL.Query().Get("language")
	if code == "" {
		errBadRequest(w, "the language parameter is required")
		return
	}
	ctx := r.Context()
	lang, err := h.Store.GetLanguageByCode(ctx, code)
	if err != nil {
		errInternal(w)
		return
	}
	if lang == nil || !lang.Enabled {
		errBadRequest(w, "unknown or disabled language")
		return
	}
	current, err := h.Store.ListI18nStringsByCode(ctx, code)
	if err != nil {
		errInternal(w)
		return
	}
	universe := current
	if code != defaultUILanguage {
		english, err := h.Store.ListI18nStringsByCode(ctx, defaultUILanguage)
		if err != nil {
			errInternal(w)
			return
		}
		universe = map[string]string{}
		for k := range english {
			universe[k] = ""
		}
		for k := range current {
			universe[k] = ""
		}
	}
	keys := make([]string, 0, len(universe))
	for k := range universe {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]stringObject, 0, len(keys))
	for _, k := range keys {
		text, ok := current[k]
		out = append(out, stringObject{Key: k, Text: text, Missing: !ok})
	}
	writeJSON(w, http.StatusOK, out)
}

// defaultUILanguage is the fallback every missing key resolves to (REQ-DB-031).
const defaultUILanguage = "en"

// stringsBody is the §4.19 PUT body — one language plus the keys to set, where
// an empty text removes a translation.
type stringsBody struct {
	Language string          `json:"language"`
	Entries  []i18nEntryWire `json:"entries"`
}

// i18nEntryWire is one entry of the PUT body; it maps onto db.I18nEntry for the
// store call.
type i18nEntryWire struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// putStrings upserts translations and audit-logs each key that actually
// changed (REQ-API-100). An entry whose text already matches is not a change,
// so repeating the call writes no second entry (REQ-API-042).
func (h *Handler) putStrings(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "language", "entries") {
		return
	}
	var body stringsBody
	if raw, has := supplied["language"]; !has || json.Unmarshal(raw, &body.Language) != nil || body.Language == "" {
		errBadRequest(w, "language is required")
		return
	}
	rawEntries, has := supplied["entries"]
	if !has {
		errBadRequest(w, "entries is required (an empty list changes nothing)")
		return
	}
	if err := json.Unmarshal(rawEntries, &body.Entries); err != nil {
		errBadRequest(w, `entries must be an array of {"key":…,"text":…}`)
		return
	}
	for _, e := range body.Entries {
		if e.Key == "" {
			errBadRequest(w, "every entry needs a key")
			return
		}
	}
	ctx := r.Context()
	lang, err := h.Store.GetLanguageByCode(ctx, body.Language)
	if err != nil {
		errInternal(w)
		return
	}
	if lang == nil || !lang.Enabled {
		errBadRequest(w, "unknown or disabled language")
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	entries := make([]db.I18nEntry, 0, len(body.Entries))
	for _, e := range body.Entries {
		entries = append(entries, db.I18nEntry{Key: e.Key, Text: e.Text})
	}
	changes, err := h.Store.SetI18nStringsTx(ctx, tx, lang.ID, entries)
	if err != nil {
		errInternal(w)
		return
	}
	for _, c := range changes {
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.I18nUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email,
			Details: map[string]any{
				"language": body.Language, "key": c.Key, "action": c.Action,
			},
		}); err != nil {
			errInternal(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"language": body.Language, "changed": len(changes),
	})
}
