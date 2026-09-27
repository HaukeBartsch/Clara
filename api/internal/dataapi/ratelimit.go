package dataapi

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// RateLimiter is the per-source-IP sliding-window limit of
// API_Endpoints_Design.md §3.9 (REQ-API-038): each source IP at most rpm
// calls per rolling minute, the threshold read from the system settings on
// every call (REQ-API-112). In-memory by design — the API is stateless and
// a restart only resets the windows.
type RateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewRateLimiter builds an empty limiter; the budget arrives per call.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{hits: map[string][]time.Time{}}
}

// Allow reports whether key may proceed at now under a budget of rpm calls
// per rolling minute, recording the call in the window. rpm < 1 still
// admits one call rather than locking the caller out.
func (l *RateLimiter) Allow(key string, rpm int, now time.Time) bool {
	if rpm < 1 {
		rpm = 1
	}
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
	if len(keep) >= rpm {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

// RateLimitSettings reads the enable flag and the per-source-IP threshold
// from system_settings (REQ-API-112). A missing or unreadable value falls
// back to disabled / 600 — rate limiting is opt-in (REQ-CFG-020).
func RateLimitSettings(ctx context.Context, store *db.Store) (enabled bool, rpm int) {
	s, err := store.SystemSettings(ctx)
	if err != nil {
		return false, 600
	}
	enabled = s["rate_limit_enabled"] == "true"
	rpm = 600
	if v, err := strconv.Atoi(s["rate_limit_rpm"]); err == nil && v >= 1 {
		rpm = v
	}
	return enabled, rpm
}

// SourceIP derives the rate-limit identity of a request (REQ-API-111): the
// proxy-provided X-Real-IP when the direct TCP peer is inside a trusted
// proxy range, otherwise the connection's remote address. A client-supplied
// X-Real-IP from an untrusted peer is ignored.
func SourceIP(cfg *config.Config, r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	peerIP := net.ParseIP(peer)
	if cfg.IsTrustedProxy(peerIP) {
		if rip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); rip != nil {
			return rip.String()
		}
	}
	if peerIP != nil {
		return peerIP.String()
	}
	return peer
}
