package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// GET /api/v1/system/blocked-ips (§4.26, REQ-API-164): the live limiter state
// behind the is_admin boundary — read-only and not audited, like §4.25, but
// unlike it the payload carries source IP addresses (personal data), so the
// permission check is the whole story for anonymous and non-admin callers.

type blockedIpsBody struct {
	GeneratedAt  string              `json:"generated_at"`
	Enabled      bool                `json:"enabled"`
	RPM          int                 `json:"rpm"`
	BlockMinutes int                 `json:"block_minutes"`
	Blocked      []dataapi.BlockedIP `json:"blocked"`
}

func TestBlockedIpsRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	user := e.mustUser("plain@example.org")
	for _, actor := range []*db.User{nil, user} {
		rec := e.do("GET", "/api/v1/system/blocked-ips", nil, actor)
		if rec.Code != http.StatusForbidden {
			t.Errorf("actor %v: want 403, got %d", actor, rec.Code)
		}
		var body map[string]any
		e.decode(rec, &body)
		if body["error"] != "forbidden" {
			t.Errorf("actor %v: want error forbidden, got %v", actor, body["error"])
		}
	}
	// Read-only like §4.25: a rejection must not audit anything either —
	// admin_rejected is the boundary middleware's job (exercised in httpapi).
	if types := e.auditTypes(); len(types) != 0 {
		t.Errorf("want no audit entries, got %v", types)
	}
}

func TestBlockedIpsEchoesSettings(t *testing.T) {
	e := newEnv(t)
	adminUser := e.mustAdmin("admin@example.org")
	ctx := context.Background()
	for k, v := range map[string]string{
		"rate_limit_enabled":       "true",
		"rate_limit_rpm":           "42",
		"rate_limit_block_minutes": "3",
	} {
		if err := e.Store.SetSystemSetting(ctx, k, v); err != nil {
			t.Fatalf("SetSystemSetting %s: %v", k, err)
		}
	}

	rec := e.do("GET", "/api/v1/system/blocked-ips", nil, adminUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body blockedIpsBody
	e.decode(rec, &body)
	if !body.Enabled || body.RPM != 42 || body.BlockMinutes != 3 {
		t.Errorf("settings must be echoed as configured, got %+v", body)
	}
	if _, err := time.Parse("2006-01-02 15:04:05", body.GeneratedAt); err != nil {
		t.Errorf("generated_at must be canonical UTC, got %q", body.GeneratedAt)
	}
	// A handler without a limiter (the test fixture's nil) answers an empty
	// list — JSON [] , never null and never a panic.
	if body.Blocked == nil {
		t.Fatalf("blocked must serialize as [], got null: %s", rec.Body.String())
	}
	var raw map[string]json.RawMessage
	e.decode(rec, &raw)
	if string(raw["blocked"]) != "[]" {
		t.Errorf("want literal [], got %s", raw["blocked"])
	}
}

func TestBlockedIpsListsBlockouts(t *testing.T) {
	e := newEnv(t)
	adminUser := e.mustAdmin("admin2@example.org")
	e.Handler.Limiter = dataapi.NewRateLimiter()

	// Drive one source IP over budget so the shared limiter blocks it —
	// exactly how the httpapi middleware would at request time.
	now := time.Now()
	lim := dataapi.Limit{RPM: 1, Block: 10 * time.Minute}
	e.Handler.Limiter.Allow("10.9.9.9", lim, now) // admits, spends the budget
	e.Handler.Limiter.Allow("10.9.9.9", lim, now) // over budget → blocked
	e.Handler.Limiter.Allow("10.9.9.8", lim, now) // admitted, never exceeded

	rec := e.do("GET", "/api/v1/system/blocked-ips", nil, adminUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body blockedIpsBody
	e.decode(rec, &body)
	if len(body.Blocked) != 1 {
		t.Fatalf("only the over-budget IP is blocked, got %+v", body.Blocked)
	}
	entry := body.Blocked[0]
	if entry.IP != "10.9.9.9" {
		t.Errorf("want 10.9.9.9, got %s", entry.IP)
	}
	// Ten minutes from a moment ago: the handler answers at request time, so
	// remaining is within a few seconds of the full blockout.
	if entry.SecondsRemaining <= 590 || entry.SecondsRemaining > 600 {
		t.Errorf("want ~600 seconds remaining, got %d", entry.SecondsRemaining)
	}
}
