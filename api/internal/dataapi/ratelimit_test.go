package dataapi

import (
	"testing"
	"time"
)

// The rate limiter is the per-token sliding window of §3.9
// (REQ-API-038): at most rpm calls per rolling minute per key. It is pure
// apart from the injected clock, so the window behavior is tested directly.
func TestRateLimiterWithinBudget(t *testing.T) {
	l := NewRateLimiter(3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow("tok", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("call %d within budget of 3 should be allowed", i+1)
		}
	}
	if l.Allow("tok", now.Add(4*time.Second)) {
		t.Errorf("4th call within a minute should be denied")
	}
}

// The window slides: calls older than a minute fall out of it, freeing budget.
func TestRateLimiterWindowSlides(t *testing.T) {
	l := NewRateLimiter(2)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !l.Allow("tok", t0) || !l.Allow("tok", t0.Add(time.Second)) {
		t.Fatalf("initial calls should be allowed")
	}
	if l.Allow("tok", t0.Add(2*time.Second)) {
		t.Fatalf("third call within a minute should be denied")
	}
	// Once past the one-minute cutoff, budget is available again.
	if !l.Allow("tok", t0.Add(61*time.Second)) {
		t.Errorf("call after the window slides should be allowed")
	}
}

// Tokens are independent: one token exhausting its budget does not affect
// another (REQ-API-038 is per token).
func TestRateLimiterPerToken(t *testing.T) {
	l := NewRateLimiter(1)
	now := time.Now()
	if !l.Allow("a", now) {
		t.Fatalf("token a first call should be allowed")
	}
	if l.Allow("a", now.Add(time.Second)) {
		t.Errorf("token a second call within a minute should be denied")
	}
	if !l.Allow("b", now.Add(time.Second)) {
		t.Errorf("token b should have its own independent budget")
	}
}

// NewRateLimiter floors the budget at 1 so a zero or negative rpm still
// admits at least one call per minute rather than locking the token out.
func TestRateLimiterFloor(t *testing.T) {
	for _, rpm := range []int{0, -5} {
		l := NewRateLimiter(rpm)
		if !l.Allow("tok", time.Now()) {
			t.Errorf("rpm=%d should still allow the first call", rpm)
		}
	}
}
