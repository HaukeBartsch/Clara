package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// --- §4.19 i18n (REQ-API-097…100) ---

// i18nAuditRows returns the translation-change trail in insertion order.
func i18nAuditRows(t *testing.T, e *env) []map[string]any {
	t.Helper()
	rows, err := e.Store.DB.Query(
		`SELECT details FROM audit_events WHERE event_type = ? ORDER BY id`, audit.I18nUpdated)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var details string
		if err := rows.Scan(&details); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			t.Fatalf("audit details %q: %v", details, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

// TestListLanguages covers REQ-API-097: the seeded languages are offered to any
// authenticated user, and a disabled language is not.
func TestListLanguages(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")

	rec := e.do("GET", "/api/v1/i18n/languages", nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("list languages: got %d %s", rec.Code, rec.Body.String())
	}
	var langs []languageObject
	e.decode(rec, &langs)
	codes := map[string]string{}
	for _, l := range langs {
		codes[l.Code] = l.DisplayName
	}
	// The seeded set (0002_seed.sql): English plus the two Norwegian forms.
	for _, want := range []string{"en", "nb", "nn"} {
		if _, ok := codes[want]; !ok {
			t.Errorf("language %q missing from %+v", want, langs)
		}
	}
	if codes["en"] != "English" {
		t.Errorf("English display name = %q", codes["en"])
	}

	// A disabled language is not offered.
	if _, err := e.Store.CreateLanguage(context.Background(), &db.Language{
		Code: "de", DisplayName: "Deutsch", Enabled: false,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	rec = e.do("GET", "/api/v1/i18n/languages", nil, member)
	e.decode(rec, &langs)
	for _, l := range langs {
		if l.Code == "de" {
			t.Errorf("disabled language offered: %+v", langs)
		}
	}

	// No actor at all — the handler's own guard, behind the boundary.
	if rec := e.do("GET", "/api/v1/i18n/languages", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("unauthenticated: got %d, want 403", rec.Code)
	}
}

// TestPutMyUILanguage covers REQ-API-098: a member sets their own language, it
// persists, and only an enabled language is accepted.
func TestPutMyUILanguage(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")

	rec := e.do("PUT", "/api/v1/users/me/ui-language", map[string]string{"language": "nb"}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("set language: got %d %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.UILanguage != "nb" {
		t.Errorf("response ui_language = %q, want nb", obj.UILanguage)
	}
	if obj.ID != member.ID {
		t.Errorf("response describes user %d, want the acting user %d", obj.ID, member.ID)
	}

	// Persisted — "across sessions" means stored, not request-scoped.
	stored, err := e.Store.GetUser(context.Background(), member.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if stored.UILanguage != "nb" {
		t.Errorf("stored ui_language = %q, want nb", stored.UILanguage)
	}

	// Unknown language → 400; a disabled one too.
	if rec := e.do("PUT", "/api/v1/users/me/ui-language", map[string]string{"language": "xx"}, member); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown language: got %d %s, want 400", rec.Code, rec.Body.String())
	}
	if _, err := e.Store.CreateLanguage(context.Background(), &db.Language{
		Code: "de", DisplayName: "Deutsch", Enabled: false,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	if rec := e.do("PUT", "/api/v1/users/me/ui-language", map[string]string{"language": "de"}, member); rec.Code != http.StatusBadRequest {
		t.Errorf("disabled language: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/users/me/ui-language", map[string]string{}, member); rec.Code != http.StatusBadRequest {
		t.Errorf("missing language: got %d, want 400", rec.Code)
	}

	// Another member's setting is untouched — the endpoint has no target user.
	other := e.mustUser("other@example.org")
	if storedOther, err := e.Store.GetUser(context.Background(), other.ID); err != nil || storedOther.UILanguage != "en" {
		t.Errorf("other member's language = %q, want the default en", storedOther.UILanguage)
	}
}

// TestPutMyUITheme covers REQ-API-122: a member sets their own theme override,
// null clears it back to the installation default, only installed identifiers
// are accepted, and the setting persists across requests.
func TestPutMyUITheme(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")

	// A fresh account carries no override — the response says null (GD-26).
	rec := e.do("PUT", "/api/v1/users/me/ui-theme", map[string]any{"theme": "darkly"}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("set theme: got %d %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	if obj.UITheme == nil || *obj.UITheme != "darkly" {
		t.Errorf("response ui_theme = %v, want darkly", obj.UITheme)
	}
	if obj.ID != member.ID {
		t.Errorf("response describes user %d, want the acting user %d", obj.ID, member.ID)
	}

	// Persisted — "across sessions" means stored, not request-scoped.
	stored, err := e.Store.GetUser(context.Background(), member.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !stored.UITheme.Valid || stored.UITheme.String != "darkly" {
		t.Errorf("stored ui_theme = %+v, want darkly", stored.UITheme)
	}

	// Every installed identifier is accepted (REQ-TECH-027).
	for _, id := range []string{"bootstrap", "yeti"} {
		if rec := e.do("PUT", "/api/v1/users/me/ui-theme", map[string]any{"theme": id}, member); rec.Code != http.StatusOK {
			t.Errorf("theme %q: got %d %s, want 200", id, rec.Code, rec.Body.String())
		}
	}

	// null clears the override — stored NULL, response null (REQ-CFG-031).
	recClear := e.do("PUT", "/api/v1/users/me/ui-theme", map[string]any{"theme": nil}, member)
	if recClear.Code != http.StatusOK {
		t.Fatalf("clear theme: got %d %s", recClear.Code, recClear.Body.String())
	}
	var cleared UserObject
	e.decode(recClear, &cleared)
	if cleared.UITheme != nil {
		t.Errorf("response ui_theme = %q after clear, want null", *cleared.UITheme)
	}
	if stored, err = e.Store.GetUser(context.Background(), member.ID); err != nil || stored.UITheme.Valid {
		t.Errorf("stored ui_theme = %+v after clear, want NULL", stored.UITheme)
	}

	// Unknown identifier, empty string, wrong type, and a missing key → 400.
	for _, body := range []any{
		map[string]any{"theme": "cyborg"},
		map[string]any{"theme": ""},
		map[string]any{"theme": 3},
		map[string]any{},
	} {
		if rec := e.do("PUT", "/api/v1/users/me/ui-theme", body, member); rec.Code != http.StatusBadRequest {
			t.Errorf("body %v: got %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}

	// No actor at all — the handler's own guard, behind the boundary.
	if rec := e.do("PUT", "/api/v1/users/me/ui-theme", map[string]any{"theme": "darkly"}, nil); rec.Code != http.StatusForbidden {
		t.Errorf("unauthenticated: got %d, want 403", rec.Code)
	}

	// Another member's setting is untouched — the endpoint has no target user.
	other := e.mustUser("other@example.org")
	if storedOther, err := e.Store.GetUser(context.Background(), other.ID); err != nil || storedOther.UITheme.Valid {
		t.Errorf("other member's theme = %+v, want NULL", storedOther.UITheme)
	}
}

// TestI18nStrings covers REQ-API-099/100: upsert, the missing flag computed
// against English, removal by empty text, per-key audit, and idempotence.
func TestI18nStrings(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustUser("member@example.org")

	// Both surfaces are is_admin (REQ-API-099/100).
	if rec := e.do("GET", "/api/v1/i18n/strings?language=nb", nil, member); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin listing strings: got %d, want 403", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/i18n/strings",
		map[string]any{"language": "nb", "entries": []any{}}, member); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin writing strings: got %d, want 403", rec.Code)
	}

	// A key defined in English only → missing for Bokmål.
	rec := e.do("PUT", "/api/v1/i18n/strings", map[string]any{
		"language": "en",
		"entries":  []map[string]string{{"key": "ui.dashboard.title", "text": "Dashboard"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed English: got %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/i18n/strings?language=nb", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list nb: got %d %s", rec.Code, rec.Body.String())
	}
	var strs []stringObject
	e.decode(rec, &strs)
	if len(strs) != 1 || strs[0].Key != "ui.dashboard.title" || !strs[0].Missing || strs[0].Text != "" {
		t.Fatalf("nb before translating: %+v, want one missing key with empty text", strs)
	}

	// Translate it → no longer missing.
	rec = e.do("PUT", "/api/v1/i18n/strings", map[string]any{
		"language": "nb",
		"entries":  []map[string]string{{"key": "ui.dashboard.title", "text": "Oversikt"}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("translate: got %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/i18n/strings?language=nb", nil, admin)
	e.decode(rec, &strs)
	if len(strs) != 1 || strs[0].Missing || strs[0].Text != "Oversikt" {
		t.Fatalf("nb after translating: %+v", strs)
	}

	// Repeating the identical PUT changes nothing and writes no audit entry
	// (REQ-API-042).
	before := i18nAuditRows(t, e)
	e.do("PUT", "/api/v1/i18n/strings", map[string]any{
		"language": "nb",
		"entries":  []map[string]string{{"key": "ui.dashboard.title", "text": "Oversikt"}},
	}, admin)
	if after := i18nAuditRows(t, e); len(after) != len(before) {
		t.Errorf("unchanged PUT wrote audit rows: %+v", after[len(before):])
	}

	// Empty text removes the translation → the key is missing again.
	rec = e.do("PUT", "/api/v1/i18n/strings", map[string]any{
		"language": "nb",
		"entries":  []map[string]string{{"key": "ui.dashboard.title", "text": ""}},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: got %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/i18n/strings?language=nb", nil, admin)
	e.decode(rec, &strs)
	if len(strs) != 1 || !strs[0].Missing {
		t.Fatalf("nb after removal: %+v, want the key missing again", strs)
	}

	// One audit entry per actual change: set (en), set (nb), removed (nb).
	rows := i18nAuditRows(t, e)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3: %+v", len(rows), rows)
	}
	wantActions := []string{"set", "set", "removed"}
	wantLanguages := []string{"en", "nb", "nb"}
	for i, row := range rows {
		if row["action"] != wantActions[i] || row["language"] != wantLanguages[i] ||
			row["key"] != "ui.dashboard.title" {
			t.Errorf("audit[%d] = %+v, want %s/%s of ui.dashboard.title",
				i, row, wantLanguages[i], wantActions[i])
		}
	}

	// Validation.
	if rec := e.do("GET", "/api/v1/i18n/strings", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("missing language parameter: got %d, want 400", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/i18n/strings?language=xx", nil, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown language parameter: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/i18n/strings",
		map[string]any{"language": "xx", "entries": []any{}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown language: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/i18n/strings",
		map[string]any{"language": "nb", "entries": []map[string]string{{"text": "no key"}}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("entry without a key: got %d, want 400", rec.Code)
	}
	if rec := e.do("PUT", "/api/v1/i18n/strings", map[string]any{"language": "nb"}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("missing entries: got %d, want 400", rec.Code)
	}
}
