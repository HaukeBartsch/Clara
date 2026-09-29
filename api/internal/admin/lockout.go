package admin

import (
	"strings"
	"sync"
	"time"
)

// authLockout implements the brute-force rule of Sequence E
// (Authentication_Authorization_Design.md §2.5, REQ-AUTH-035): five failed
// logins for the same email within a rolling ten-minute window reject
// further attempts for that address for fifteen minutes. The store is
// per-host and in-memory — restart losing it is allowed by the requirement.
//
// It guards credential-probing paths: the login endpoint and the
// self-service password change, whose failures count toward the same lock
// (REQ-AUTH-061). rate_limited rejections themselves are not recorded, so a
// locked address cannot extend its own lockout by retrying.
type authLockout struct {
	mu sync.Mutex
	m  map[string]*lockState
}

type lockState struct {
	failures    []time.Time
	lockedUntil time.Time
}

const (
	lockoutThreshold = 5
	lockoutWindow    = 10 * time.Minute
	lockoutDuration  = 15 * time.Minute
)

func newAuthLockout() *authLockout { return &authLockout{m: map[string]*lockState{}} }

// locked reports whether the address is currently rejected.
func (l *authLockout) locked(email string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.m[emailKey(email)]
	return s != nil && now.Before(s.lockedUntil)
}

// failure records one failed attempt; crossing the threshold within the
// window starts the lockout.
func (l *authLockout) failure(email string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := emailKey(email)
	s := l.m[key]
	if s == nil {
		s = &lockState{}
		l.m[key] = s
	}
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < lockoutWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	s.failures = kept
	if len(kept) >= lockoutThreshold {
		s.lockedUntil = now.Add(lockoutDuration)
		s.failures = nil // the lock itself is now the gate
	}
}

// success clears the counter for the address (REQ-AUTH-035).
func (l *authLockout) success(email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, emailKey(email))
}

func emailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
