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

// window is a limit without a blockout, i.e. the §3.9 sliding window on its
// own; block tests add the REQ-API-113 period explicitly.
func window(rpm int) Limit { return Limit{RPM: rpm} }

func TestRateLimiterWithinBudget(t *testing.T) {
	l := NewRateLimiter()
	now := time.Now()
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("203.0.113.7", window(3), now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("call %d within budget of 3 should be allowed", i+1)
		}
	}
	if ok, _ := l.Allow("203.0.113.7", window(3), now.Add(4*time.Second)); ok {
		t.Errorf("4th call within a minute should be denied")
	}
}

// The window slides: calls older than a minute fall out of it, freeing budget.
func TestRateLimiterWindowSlides(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if ok, _ := l.Allow("ip", window(2), t0); !ok {
		t.Fatalf("initial call should be allowed")
	}
	if ok, _ := l.Allow("ip", window(2), t0.Add(time.Second)); !ok {
		t.Fatalf("second call should be allowed")
	}
	if ok, _ := l.Allow("ip", window(2), t0.Add(2*time.Second)); ok {
		t.Fatalf("third call within a minute should be denied")
	}
	// Once past the one-minute cutoff, budget is available again.
	if ok, _ := l.Allow("ip", window(2), t0.Add(61*time.Second)); !ok {
		t.Errorf("call after the window slides should be allowed")
	}
}

// Source IPs are independent: one address exhausting its budget does not
// affect another (REQ-API-038 is per source IP).
func TestRateLimiterPerIP(t *testing.T) {
	l := NewRateLimiter()
	now := time.Now()
	if ok, _ := l.Allow("a", window(1), now); !ok {
		t.Fatalf("ip a first call should be allowed")
	}
	if ok, _ := l.Allow("a", window(1), now.Add(time.Second)); ok {
		t.Errorf("ip a second call within a minute should be denied")
	}
	if ok, _ := l.Allow("b", window(1), now.Add(time.Second)); !ok {
		t.Errorf("ip b should have its own independent budget")
	}
}

// A zero or negative threshold still admits at least one call per minute
// rather than locking the caller out.
func TestRateLimiterFloor(t *testing.T) {
	for _, rpm := range []int{0, -5} {
		l := NewRateLimiter()
		if ok, _ := l.Allow("ip", window(rpm), time.Now()); !ok {
			t.Errorf("rpm=%d should still allow the first call", rpm)
		}
	}
}

// Spending the budget starts a block of the configured length, and the denial
// names the whole period as the time until the caller may retry (REQ-API-113).
func TestRateLimiterBlocksOverBudget(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lim := Limit{RPM: 2, Block: 10 * time.Minute}
	if ok, _ := l.Allow("ip", lim, t0); !ok {
		t.Fatalf("first call should be allowed")
	}
	if ok, _ := l.Allow("ip", lim, t0.Add(time.Second)); !ok {
		t.Fatalf("second call should be allowed")
	}
	ok, retry := l.Allow("ip", lim, t0.Add(2*time.Second))
	if ok {
		t.Fatalf("third call over the budget should be denied")
	}
	if retry != 10*time.Minute {
		t.Errorf("retry after = %v, want the full block period 10m", retry)
	}

	// The block runs from the rejection at +2s, so it ends at +10m2s. Mid-block
	// the caller is refused with what is left of the period…
	ok, retry = l.Allow("ip", lim, t0.Add(9*time.Minute))
	if ok {
		t.Fatalf("call during the block should be denied")
	}
	if retry != time.Minute+2*time.Second {
		t.Errorf("retry after = %v, want the remaining %v", retry, time.Minute+2*time.Second)
	}

	// …and once the period has elapsed it is served again, from a fresh
	// window rather than the budget-less one it left behind.
	if ok, _ := l.Allow("ip", lim, t0.Add(10*time.Minute+3*time.Second)); !ok {
		t.Errorf("call after the block expires should be allowed")
	}
	if ok, _ := l.Allow("ip", lim, t0.Add(10*time.Minute+4*time.Second)); !ok {
		t.Errorf("the fresh window should still hold budget for a second call")
	}
}

// The block runs from the first rejection: calls made during it are refused
// without moving its end, so hammering cannot lock an IP out indefinitely.
func TestRateLimiterBlockNotExtended(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lim := Limit{RPM: 1, Block: time.Minute}
	if ok, _ := l.Allow("ip", lim, t0); !ok {
		t.Fatalf("first call should be allowed")
	}
	if ok, _ := l.Allow("ip", lim, t0.Add(2*time.Second)); ok {
		t.Fatalf("second call over the budget should be denied")
	}
	for d := 3 * time.Second; d < time.Minute; d += time.Second {
		if ok, _ := l.Allow("ip", lim, t0.Add(d)); ok {
			t.Fatalf("call at +%v during the block should be denied", d)
		}
	}
	// The original expiry (rejection at +2s plus one minute) still holds after
	// all those refusals, and passes right afterwards.
	if ok, _ := l.Allow("ip", lim, t0.Add(time.Minute+time.Second)); ok {
		t.Fatalf("call before the original block end should still be denied")
	}
	if ok, _ := l.Allow("ip", lim, t0.Add(time.Minute+3*time.Second)); !ok {
		t.Errorf("call just after the original block end should be allowed")
	}
}

// A block belongs to one source IP only (REQ-API-038/113).
func TestRateLimiterBlockPerIP(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lim := Limit{RPM: 1, Block: 10 * time.Minute}
	if ok, _ := l.Allow("a", lim, t0); !ok {
		t.Fatalf("ip a first call should be allowed")
	}
	if ok, _ := l.Allow("a", lim, t0.Add(time.Second)); ok {
		t.Fatalf("ip a second call should be denied and blocked")
	}
	if ok, _ := l.Allow("b", lim, t0.Add(2*time.Second)); !ok {
		t.Errorf("ip b should be unaffected by ip a's block")
	}
}

// Without a block period the limiter keeps the pre-blockout behavior: the
// denial is for that call only, and the sliding window governs retries.
func TestRateLimiterZeroBlockDoesNotLock(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if ok, _ := l.Allow("ip", window(1), t0); !ok {
		t.Fatalf("first call should be allowed")
	}
	ok, retry := l.Allow("ip", window(1), t0.Add(time.Second))
	if ok {
		t.Fatalf("second call within the window should be denied")
	}
	if retry != 0 {
		t.Errorf("retry after = %v, want 0 (no block announced without a block period)", retry)
	}
}

// Stale keys are pruned so the maps stay bounded: an address that is neither
// blocked nor inside a window disappears at the next sweep (§3.9).
func TestRateLimiterSweepPrunesStaleKeys(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if ok, _ := l.Allow(ip, window(5), t0); !ok {
			t.Fatalf("seed call for %s should be allowed", ip)
		}
	}
	l.Allow("4.4.4.4", Limit{RPM: 1, Block: 30 * time.Minute}, t0)
	l.Allow("4.4.4.4", Limit{RPM: 1, Block: 30 * time.Minute}, t0.Add(time.Second)) // blocked

	// A call well after the window triggers the sweep; the addresses that are
	// neither blocked nor inside a window go, the blocked one stays.
	if ok, _ := l.Allow("5.5.5.5", window(5), t0.Add(2*time.Minute)); !ok {
		t.Fatalf("call for a fresh key should be allowed")
	}
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if _, live := l.hits[ip]; live {
			t.Errorf("stale hit window of %s should have been swept", ip)
		}
	}
	if until, live := l.blocked["4.4.4.4"]; !live || !until.After(t0.Add(2*time.Minute)) {
		t.Errorf("a still-blocked ip must keep its block through a sweep")
	}
	if _, live := l.hits["4.4.4.4"]; !live {
		t.Errorf("a blocked ip keeps its window so it returns to the budget it spent")
	}
	if _, live := l.hits["5.5.5.5"]; !live {
		t.Errorf("the calling key should hold its new hit")
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

// SetRetryAfter rounds up to whole seconds and stays silent when there is
// nothing to announce (REQ-API-113).
func TestSetRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	SetRetryAfter(rec, 90*time.Second)
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want 90", got)
	}

	rec = httptest.NewRecorder()
	SetRetryAfter(rec, 59*time.Second+500*time.Millisecond)
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60 (rounded up)", got)
	}

	rec = httptest.NewRecorder()
	SetRetryAfter(rec, 0)
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want unset without a block period", got)
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
