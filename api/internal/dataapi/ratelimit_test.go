package dataapi

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"csms/api/internal/config"
)

// The rate limiter is the per-source-IP sliding window of §3.9
// (REQ-API-038): at most rpm calls per rolling minute per key, the budget
// arriving per call from the system settings (REQ-API-112). It is pure
// apart from the injected clock, so the window behavior is tested directly.
func TestRateLimiterWithinBudget(t *testing.T) {
	l := NewRateLimiter()
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow("203.0.113.7", 3, now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("call %d within budget of 3 should be allowed", i+1)
		}
	}
	if l.Allow("203.0.113.7", 3, now.Add(4*time.Second)) {
		t.Errorf("4th call within a minute should be denied")
	}
}

// The window slides: calls older than a minute fall out of it, freeing budget.
func TestRateLimiterWindowSlides(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !l.Allow("ip", 2, t0) || !l.Allow("ip", 2, t0.Add(time.Second)) {
		t.Fatalf("initial calls should be allowed")
	}
	if l.Allow("ip", 2, t0.Add(2*time.Second)) {
		t.Fatalf("third call within a minute should be denied")
	}
	// Once past the one-minute cutoff, budget is available again.
	if !l.Allow("ip", 2, t0.Add(61*time.Second)) {
		t.Errorf("call after the window slides should be allowed")
	}
}

// Source IPs are independent: one address exhausting its budget does not
// affect another (REQ-API-038 is per source IP).
func TestRateLimiterPerIP(t *testing.T) {
	l := NewRateLimiter()
	now := time.Now()
	if !l.Allow("a", 1, now) {
		t.Fatalf("ip a first call should be allowed")
	}
	if l.Allow("a", 1, now.Add(time.Second)) {
		t.Errorf("ip a second call within a minute should be denied")
	}
	if !l.Allow("b", 1, now.Add(time.Second)) {
		t.Errorf("ip b should have its own independent budget")
	}
}

// A zero or negative threshold still admits at least one call per minute
// rather than locking the caller out.
func TestRateLimiterFloor(t *testing.T) {
	for _, rpm := range []int{0, -5} {
		l := NewRateLimiter()
		if !l.Allow("ip", rpm, time.Now()) {
			t.Errorf("rpm=%d should still allow the first call", rpm)
		}
	}
}

// SourceIP (REQ-API-111): X-Real-IP is authoritative only from a trusted
// proxy; everything else keys on the connection address.
func TestSourceIP(t *testing.T) {
	cfg := &config.Config{TrustedProxyCIDRs: parseNets(t, "127.0.0.0/8")}

	// Trusted proxy (nginx/PHP on loopback): the forwarded header wins.
	r := httptest.NewRequest("GET", "http://test/api/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Real-IP", "203.0.113.7")
	if got := SourceIP(cfg, r); got != "203.0.113.7" {
		t.Errorf("trusted peer: got %q, want 203.0.113.7", got)
	}

	// Untrusted peer: a client-supplied X-Real-IP is ignored.
	r = httptest.NewRequest("GET", "http://test/api/", nil)
	r.RemoteAddr = "198.51.100.4:1234"
	r.Header.Set("X-Real-IP", "203.0.113.7")
	if got := SourceIP(cfg, r); got != "198.51.100.4" {
		t.Errorf("untrusted peer: got %q, want 198.51.100.4", got)
	}

	// Trusted proxy without the header: fall back to the connection address.
	r = httptest.NewRequest("GET", "http://test/api/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	if got := SourceIP(cfg, r); got != "127.0.0.1" {
		t.Errorf("trusted peer without header: got %q, want 127.0.0.1", got)
	}
}

func parseNets(t *testing.T, list ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", s, err)
		}
		out = append(out, n)
	}
	return out
}
