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

// Limit is one caller's budget: at most RPM calls per rolling minute, and a
// block of Block once the budget is spent (API_Endpoints_Design.md §3.9).
type Limit struct {
	RPM   int
	Block time.Duration
}

// RateLimiter is the per-source-IP sliding-window limit of
// API_Endpoints_Design.md §3.9 (REQ-API-038): each source IP at most rpm
// calls per rolling minute, and blocked for lim.Block once it exceeds that
// budget (REQ-API-113). Thresholds are read from the system settings on
// every call (REQ-API-112). In-memory by design — the API is stateless and
// a restart only resets the windows and the blocks.
type RateLimiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time // window timestamps per source IP
	blocked map[string]time.Time   // blockout end per blocked source IP
	swept   time.Time              // last full sweep of stale keys
}

// NewRateLimiter builds an empty limiter; the budget arrives per call.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		hits:    map[string][]time.Time{},
		blocked: map[string]time.Time{},
	}
}

// Allow reports whether key may proceed at now under lim, recording the call
// in the window. The second result is how long the caller must wait to retry:
// the remaining blockout when it is blocked, the full period when the block
// starts here, and zero when the call is admitted (or when no block period is
// configured, so there is nothing to announce).
//
// A blocked key is denied before anything else: the call is not counted and
// the blockout end does not move, so hammering cannot extend one's own lock
// (REQ-API-113). Spending the budget starts a block of lim.Block; a non-positive
// Block therefore means "refuse this call only", the pre-blockout behaviour.
// rpm < 1 still admits one call rather than locking the caller out.
func (l *RateLimiter) Allow(key string, lim Limit, now time.Time) (bool, time.Duration) {
	if lim.RPM < 1 {
		lim.RPM = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)

	if until, ok := l.blocked[key]; ok {
		if until.After(now) {
			return false, until.Sub(now)
		}
		// Expired: the IP returns with a fresh window, no memory of the offence.
		delete(l.blocked, key)
		delete(l.hits, key)
	}

	cutoff := now.Add(-time.Minute)
	h := l.hits[key]
	keep := h[:0]
	for _, t := range h {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= lim.RPM {
		l.hits[key] = keep
		if lim.Block > 0 {
			l.blocked[key] = now.Add(lim.Block)
		}
		return false, lim.Block
	}
	l.hits[key] = append(keep, now)
	return true, 0
}

// sweepLocked drops keys with no live state — block expired and window
// emptied — so the maps stay bounded by the addresses seen in about a minute
// plus the blocked ones. Runs at most once per minute inside whichever call
// notices, which keeps the limiter free of goroutines and timers. The caller
// holds l.mu.
func (l *RateLimiter) sweepLocked(now time.Time) {
	if !now.After(l.swept.Add(time.Minute)) {
		return
	}
	l.swept = now
	cutoff := now.Add(-time.Minute)
	for key, until := range l.blocked {
		if !until.After(now) {
			delete(l.blocked, key)
		}
	}
	for key, h := range l.hits {
		if _, blocked := l.blocked[key]; blocked {
			continue
		}
		alive := false
		for _, t := range h {
			if t.After(cutoff) {
				alive = true
				break
			}
		}
		if !alive {
			delete(l.hits, key)
		}
	}
}

// RateLimitSettings reads the enable flag and both thresholds from
// system_settings (REQ-API-112). A missing or unreadable value falls back to
// disabled / 600 rpm / a 10-minute block — rate limiting is opt-in (REQ-CFG-020).
func RateLimitSettings(ctx context.Context, store *db.Store) (enabled bool, lim Limit) {
	s, err := store.SystemSettings(ctx)
	if err != nil {
		return false, Limit{RPM: 600, Block: defaultRateLimitBlock}
	}
	enabled = s["rate_limit_enabled"] == "true"
	lim = Limit{RPM: 600, Block: defaultRateLimitBlock}
	if v, err := strconv.Atoi(s["rate_limit_rpm"]); err == nil && v >= 1 {
		lim.RPM = v
	}
	if v, err := strconv.Atoi(s["rate_limit_block_minutes"]); err == nil && v >= 1 {
		// Clamped even though the settings endpoint validates: a row edited
		// straight in the database must not be able to lock every caller out
		// indefinitely (REQ-API-113).
		if v > maxRateLimitBlockMinutes {
			v = maxRateLimitBlockMinutes
		}
		lim.Block = time.Duration(v) * time.Minute
	}
	return enabled, lim
}

// defaultRateLimitBlock is the blockout period applied to an over-budget
// source IP when the setting is absent or unreadable, and maxRateLimitBlockMinutes
// the longest period honoured (both REQ-API-113; the settings endpoint applies
// the same range, admin.settings).
const (
	defaultRateLimitBlock    = 10 * time.Minute
	maxRateLimitBlockMinutes = 1440
)

// SourceIP derives the rate-limit identity of a request (REQ-API-125): the
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

// SetRetryAfter names the seconds until the caller may try again on a 429
// response (REQ-API-113), whole seconds rounded up. Both surfaces use it so
// the header is spelled the same everywhere.
func SetRetryAfter(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		return
	}
	secs := int((retryAfter + time.Second - time.Nanosecond) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}
