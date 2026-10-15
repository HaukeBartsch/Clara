package dataapi

import (
	"reflect"
	"testing"
	"time"
)

// Blocked is the read side of the blockout state (§3.9, REQ-API-164): who is
// inside a blockout right now, with how much of it is left. It never mutates
// limiter state — the sweep stays inside Allow — and filters expired entries
// at read time, so it answers correctly even when no traffic has arrived to
// trigger one. A fixed clock keeps every expectation exact.

// blockIP drives one key over budget on an existing limiter: the first call
// spends it, the second is refused and starts the blockout ending exactly at
// now+block.
func blockIP(t *testing.T, l *RateLimiter, ip string, now time.Time, block time.Duration) {
	t.Helper()
	lim := Limit{RPM: 1, Block: block}
	if ok, _ := l.Allow(ip, lim, now); !ok {
		t.Fatalf("first call for %s should be allowed", ip)
	}
	if ok, _ := l.Allow(ip, lim, now); ok {
		t.Fatalf("second call over budget for %s should be denied", ip)
	}
}

func blockedAt(t *testing.T, ip string, now time.Time, block time.Duration) *RateLimiter {
	t.Helper()
	l := NewRateLimiter()
	blockIP(t, l, ip, now, block)
	return l
}

func TestBlockedEmpty(t *testing.T) {
	got := NewRateLimiter().Blocked(time.Now())
	if got == nil {
		t.Fatalf("empty limiter should answer an empty slice, not nil")
	}
	if len(got) != 0 {
		t.Fatalf("fresh limiter has no blocks, got %v", got)
	}
}

func TestBlockedNilReceiver(t *testing.T) {
	var l *RateLimiter // a handler built without a limiter must not panic
	if got := l.Blocked(time.Now()); got == nil || len(got) != 0 {
		t.Fatalf("nil limiter should answer an empty slice, got %v", got)
	}
}

func TestBlockedShapeAndSort(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	l := blockedAt(t, "203.0.113.7", t0, 10*time.Minute)
	blockIP(t, l, "198.51.100.4", t0.Add(time.Second), 10*time.Minute) // second key, sorted first

	got := l.Blocked(t0.Add(3500 * time.Millisecond)) // 600s − 3.5s from the later block's point of view
	if len(got) != 2 {
		t.Fatalf("two blocked IPs expected, got %v", got)
	}
	if got[0].IP != "198.51.100.4" || got[1].IP != "203.0.113.7" {
		t.Errorf("entries must be sorted by address, got %s then %s", got[0].IP, got[1].IP)
	}
	first := got[1] // 203.0.113.7: blocked at t0 until t0+10m
	if first.BlockedUntil != "2026-10-15 12:10:00" {
		t.Errorf("blocked_until must be canonical UTC, got %q", first.BlockedUntil)
	}
	// 596.5 seconds remain → rounded up to 597.
	if first.SecondsRemaining != 597 {
		t.Errorf("seconds_remaining must round up, want 597, got %d", first.SecondsRemaining)
	}
}

func TestBlockedExpiresAtReadTime(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	l := blockedAt(t, "203.0.113.7", t0, 10*time.Minute)

	if got := l.Blocked(t0.Add(10 * time.Minute)); len(got) != 0 {
		t.Errorf("the blockout ends at its end instant, got %v", got)
	}
	if got := l.Blocked(t0.Add(60 * time.Second)); len(got) != 1 {
		t.Errorf("a still-live block must be reported, got %v", got)
	}
}

func TestBlockedDoesNotMutate(t *testing.T) {
	now := time.Now()
	l := blockedAt(t, "203.0.113.7", now, 10*time.Minute)

	first := l.Blocked(now)
	second := l.Blocked(now)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two reads at the same instant must agree: %v vs %v", first, second)
	}
	// The blocked key stays blocked after being listed.
	if ok, _ := l.Allow("203.0.113.7", Limit{RPM: 100, Block: time.Minute}, now.Add(2*time.Second)); ok {
		t.Errorf("listing must not lift or count against the blockout")
	}
}
