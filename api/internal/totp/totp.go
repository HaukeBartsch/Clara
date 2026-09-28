// Package totp implements the RFC 6238 time-based one-time password used as
// CLARA's phone-based second factor (REQ-AUTH-054, GD-21), plus the one-time
// recovery codes issued alongside any enabled method (REQ-AUTH-059). Built on
// the standard library only — crypto/hmac and crypto/sha1 are already part of
// the production toolchain (Technology Stack design §3).
//
// Nothing in this package logs; secrets and codes must never reach a log
// line or an audit detail (REQ-AUTH-059).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period is the RFC 6238 time step in seconds.
	Period = 30
	// Digits is the length of a generated code.
	Digits = 6
	// Window is how many steps a verifier accepts on either side of the
	// current one, tolerating small clock drift between server and phone.
	Window = 1

	secretBytes    = 20 // 160-bit secret per RFC 4226
	recoveryBytes  = 5  // 40 bits of entropy per recovery code
	recoveryCount  = 10 // codes issued at activation (REQ-AUTH-059)
	modDigits      = 1_000_000
	base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// b32 is the shared base32 encoding: standard alphabet, no padding, as
// authenticator apps expect it in an otpauth:// URI.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a fresh 160-bit shared secret, base32-encoded
// without padding (32 characters).
func GenerateSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return b32.EncodeToString(buf), nil
}

// Code returns the expected code for the given instant. It is used to show
// the user what their app should display; verification always goes through
// Verify, which also enforces replay prevention.
func Code(secret string, at time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("totp: decode secret: %w", err)
	}
	return codeForStep(key, at.Unix()/Period), nil
}

// Verify checks a submitted code against the shared secret with the ±Window
// drift allowance. It returns the matched step (which the caller persists as
// the replay watermark via SetLastStep) and whether the code was valid. The
// comparison is constant-time across all candidate steps.
func Verify(secret, code string, at time.Time) (int64, bool) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return 0, false
	}
	want := strings.TrimSpace(code)
	current := at.Unix() / Period
	for delta := -Window; delta <= Window; delta++ {
		step := current + int64(delta)
		if subtle.ConstantTimeCompare([]byte(codeForStep(key, step)), []byte(want)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// codeForStep computes the RFC 4226 HOTP value for one counter value.
func codeForStep(key []byte, step int64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, value%modDigits)
}

// OTPAuthURL builds the provisioning URI an authenticator app reads from the
// enrollment QR code.
func OTPAuthURL(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))
	return fmt.Sprintf("otpauth://totp/%s?%s", label, q.Encode())
}

// RandomDigits returns n uniformly random decimal digits, drawn from
// crypto/rand with rejection sampling. It produces the email-delivered login
// code (REQ-AUTH-057); unlike Code it is not derived from a shared secret.
func RandomDigits(n int) (string, error) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		for {
			buf := make([]byte, 1)
			if _, err := rand.Read(buf); err != nil {
				return "", fmt.Errorf("totp: random digits: %w", err)
			}
			if buf[0] < 250 { // largest multiple of 10 below 256
				b.WriteByte('0' + buf[0]%10)
				break
			}
		}
	}
	return b.String(), nil
}

// GenerateRecoveryCodes returns recoveryCount one-time codes in the form
// "xxxxxx-xxxxxx" (lowercase hex, 40 bits each). The plaintext is shown to
// the user exactly once; only HashCode output is ever stored.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, recoveryCount)
	for i := range codes {
		buf := make([]byte, recoveryBytes)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("totp: generate recovery code: %w", err)
		}
		h := hex.EncodeToString(buf)
		codes[i] = h[:6] + "-" + h[6:]
	}
	return codes, nil
}

// HashCode is the one-way form in which a recovery code (or an email OTP) is
// persisted: SHA-256 over the normalized code, hex-encoded.
func HashCode(code string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
