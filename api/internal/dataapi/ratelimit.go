package dataapi

import (
	"sync"
	"time"
)

// RateLimiter is the per-token sliding-window limit of
// API_Endpoints_Design.md §3.9 (REQ-API-038, REQ-CFG-020): each token at
// most rpm calls per rolling minute. In-memory by design — the API is
// stateless and a restart only resets the window.
type RateLimiter struct {
	mu   sync.Mutex
	rpm  int
	hits map[string][]time.Time
}

// NewRateLimiter builds a limiter allowing rpm calls per minute per key.
func NewRateLimiter(rpm int) *RateLimiter {
	if rpm < 1 {
		rpm = 1
	}
	return &RateLimiter{rpm: rpm, hits: map[string][]time.Time{}}
}

// Allow reports whether the key may proceed at now, recording the call
// in the window.
func (l *RateLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	h := l.hits[key]
	keep := h[:0]
	for _, t := range h {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.rpm {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}
