package totp

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// rfcSecret is the RFC 4226 shared secret "12345678901234567890",
// base32-encoded — the vectors in Appendix B of RFC 6238 use it.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestCodeRFCVectors checks Code against the RFC 6238 SHA-1 test vectors,
// truncated to six digits (the CLARA code length).
func TestCodeRFCVectors(t *testing.T) {
	cases := []struct {
		unix int64
		want string // RFC 8-digit value mod 10^6
	}{
		{59, "287082"},         // 94287082
		{1111111109, "081804"}, // 07081804
		{1234567890, "005924"}, // 89005924
		{2000000000, "279037"}, // 69279037
	}
	for _, c := range cases {
		at := time.Unix(c.unix, 0).UTC()
		got, err := Code(rfcSecret, at)
		if err != nil {
			t.Fatalf("Code(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("Code at t=%d = %q, want %q", c.unix, got, c.want)
		}
	}
}

func TestVerifyWindow(t *testing.T) {
	now := time.Unix(1234567890, 0).UTC() // step 41152263
	prev, err := Code(rfcSecret, now.Add(-Period*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	current, err := Code(rfcSecret, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Code(rfcSecret, now.Add(Period*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	farNext, err := Code(rfcSecret, now.Add(2*Period*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		code string
		ok   bool
	}{
		{prev, true},
		{current, true},
		{next, true},
		{farNext, false}, // two steps ahead is outside the ±1 window
		{"000000", false},
		{"12345", false},
		{" abcdef ", false},
	} {
		if _, ok := Verify(rfcSecret, c.code, now); ok != c.ok {
			t.Errorf("Verify(%q) ok = %v, want %v", c.code, ok, c.ok)
		}
	}
}

func TestVerifyReturnsMatchedStep(t *testing.T) {
	now := time.Unix(1234567890, 0).UTC()
	code, err := Code(rfcSecret, now.Add(-Period*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	step, ok := Verify(rfcSecret, code, now)
	if !ok {
		t.Fatal("Verify rejected its own previous-step code")
	}
	if want := now.Unix()/Period - 1; step != want {
		t.Errorf("matched step = %d, want %d", step, want)
	}
}

func TestGenerateSecret(t *testing.T) {
	a, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || a == b {
		t.Errorf("secrets look wrong: %q vs %q (len %d)", a, b, len(a))
	}
	if _, err := b32.DecodeString(a); err != nil {
		t.Errorf("secret is not base32: %v", err)
	}
}

func TestOTPAuthURL(t *testing.T) {
	got := OTPAuthURL("CLARA", "user@example.org", "JBSWY3DPEHPK3PXP")
	// '@' is legal in a URI path segment, so PathEscape leaves the account
	// name as authenticator apps expect to read it.
	want := "otpauth://totp/CLARA:user@example.org" +
		"?algorithm=SHA1&digits=6&issuer=CLARA&period=30&secret=JBSWY3DPEHPK3PXP"
	if got != want {
		t.Errorf("OTPAuthURL() =\n %q\nwant\n %q", got, want)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCount {
		t.Fatalf("got %d codes, want %d", len(codes), recoveryCount)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{6}-[0-9a-f]{4}$`)
	seen := map[string]bool{}
	for _, c := range codes {
		if !pattern.MatchString(c) {
			t.Errorf("recovery code %q has wrong shape", c)
		}
		if seen[c] {
			t.Errorf("duplicate recovery code %q", c)
		}
		seen[c] = true
	}
}

func TestHashCodeNormalization(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	c := codes[0]
	// Case and dash placement must not change the hash — users retype these.
	if HashCode(c) != HashCode(strings.ToUpper(c)) {
		t.Error("hash is case-sensitive")
	}
	if HashCode(c) != HashCode(strings.ReplaceAll(c, "-", "")) {
		t.Error("hash is dash-sensitive")
	}
	if HashCode(c) == c {
		t.Error("hash returned the code itself")
	}
}
