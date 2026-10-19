package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Finding F15: the three in-memory limiters used to keep every key they were ever
// given, so invented addresses grew them for the life of the process. Each sweep
// runs once a minute from inside the mutating call — these tests move a clock
// rather than waiting on one, which is why the calls take `now`.

func TestAuthLockoutSweepForgetsAddressesWithNoLiveState(t *testing.T) {
	l := newAuthLockout()
	start := time.Now()

	for i := 0; i < 500; i++ {
		l.failure(fmt.Sprintf("probe%d@example.org", i), start)
	}
	if len(l.m) != 500 {
		t.Fatalf("map holds %d keys, want the 500 addresses that failed", len(l.m))
	}

	// A failure after the window has passed sweeps first: nothing those addresses
	// did can count toward a lockout any more, so none of their keys survives.
	l.failure("late@example.org", start.Add(11*time.Minute))
	if len(l.m) != 1 {
		t.Fatalf("map holds %d keys after the sweep, want only the live one", len(l.m))
	}
}

func TestAuthLockoutSweepKeepsALiveLockout(t *testing.T) {
	l := newAuthLockout()
	start := time.Now()

	for i := 0; i < lockoutThreshold; i++ {
		l.failure("target@example.org", start.Add(time.Duration(i)*time.Second))
	}

	// Past the failure window but inside the lock, swept by an unrelated lookup.
	// A locked entry holds no failures at all — the lock is its state — so a sweep
	// that only looked at failures would silently lift a lockout.
	if l.locked("unrelated@example.org", start.Add(11*time.Minute)) {
		t.Fatal("an unknown address must not read as locked")
	}
	if len(l.m) != 1 {
		t.Fatalf("map holds %d keys, want the locked address kept", len(l.m))
	}
	if _, live := l.m[emailKey("target@example.org")]; !live {
		t.Fatal("the sweep dropped an address that is still locked out")
	}

	// Once the lock itself has run out, the entry goes too.
	l.locked("unrelated2@example.org", start.Add(16*time.Minute))
	if len(l.m) != 0 {
		t.Fatalf("map holds %d keys after the lock expired, want none", len(l.m))
	}
}

func TestAddressLimiterSweepBoundsItsKeys(t *testing.T) {
	l := newAddressLimiter()
	start := time.Now()

	for i := 0; i < 300; i++ {
		l.allow(fmt.Sprintf("probe%d@example.org", i), start)
	}
	if len(l.at) != 300 {
		t.Fatalf("limiter holds %d keys, want the 300 addresses that asked", len(l.at))
	}
	l.allow("late@example.org", start.Add(16*time.Minute)) // one past resetWindow
	if len(l.at) != 1 {
		t.Fatalf("limiter holds %d keys after the sweep, want only the live one", len(l.at))
	}
}

func TestSendLimiterSweepForgetsAccountsThatStoppedAsking(t *testing.T) {
	l := newSendLimiter()
	start := time.Now()

	for id := int64(1); id <= 200; id++ {
		l.allow(id, start)
	}
	if len(l.at) != 200 {
		t.Fatalf("limiter holds %d accounts, want the 200 that sent", len(l.at))
	}
	l.allow(9999, start.Add(6*time.Minute)) // one past sendWindow
	if len(l.at) != 1 {
		t.Fatalf("limiter holds %d accounts after the sweep, want only the live one", len(l.at))
	}
}

func TestEmailKeyCapsWhatOneRequestCanStore(t *testing.T) {
	// decodeBody admits 8 MiB and the lockout is consulted before any account is
	// looked up, so the address an attacker sends is a string this map would keep.
	huge := strings.Repeat("a", 4<<20)
	if got := len(emailKey(huge)); got != maxEmailKeyBytes {
		t.Fatalf("emailKey kept %d bytes, want the %d-byte cap", got, maxEmailKeyBytes)
	}
	if got := emailKey("  Mixed.Case@Example.ORG  "); got != "mixed.case@example.org" {
		t.Fatalf("emailKey = %q, want the trimmed lower-case address", got)
	}

	l := newAuthLockout()
	now := time.Now()
	l.failure(huge, now)
	if _, stored := l.m[emailKey(huge)]; !stored {
		t.Fatal("the capped key is not the one the lockout stores")
	}
}
