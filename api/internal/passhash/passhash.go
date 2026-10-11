// Package passhash hashes and verifies local account passwords with argon2id
// (GD-18, REQ-AUTH-036/050): the only stored credential is the one-way hash,
// in the PHC string format
//
//	$argon2id$v=19$m=<KiB>,t=<iterations>,p=<lanes>$<salt>$<hash>
//
// (salt and hash in unpadded standard base64). The parameters travel inside
// every hash, so raising them later changes new hashes only and every stored
// one still verifies with the cost it was made with. Nothing else is accepted:
// the system has no stored bcrypt hashes to carry over (owner decision
// 2026-10-11), so a value in any other format simply does not verify.
package passhash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The cost of a new hash: RFC 9106 §4's second recommended setting — 64 MiB of
// memory, three passes, four lanes. Measured 2026-10-11 on an Apple M2 Pro it
// takes ~33 ms per hash or verification, against ~58 ms for the bcrypt cost 10
// it replaces, while making a stolen hash far dearer to attack on GPUs.
const (
	Memory      uint32 = 64 * 1024 // KiB
	Iterations  uint32 = 3
	Parallelism uint8  = 4
	saltBytes          = 16
	keyBytes           = 32
)

// Bounds a stored hash must respect to be evaluated at all, so a corrupted or
// hand-edited row cannot make one login allocate gigabytes or spin for minutes.
const (
	maxMemory     uint32 = 1024 * 1024 // KiB (1 GiB)
	maxIterations uint32 = 64
)

var encoding = base64.RawStdEncoding

// Every computation holds its full memory cost until it returns, and the login
// lockout (REQ-AUTH-035) counts per address, not per host — so a burst of
// logins against many valid addresses could otherwise pile up 64 MiB each.
// Running at most one computation per CPU keeps the peak at GOMAXPROCS × Memory
// and costs nothing in throughput: argon2 is CPU-bound, extra ones would only
// share the same cores.
var slots = make(chan struct{}, runtime.GOMAXPROCS(0))

func idKey(password, salt []byte, iterations, memory uint32, parallelism uint8, keyLen uint32) []byte {
	slots <- struct{}{}
	defer func() { <-slots }()
	return argon2.IDKey(password, salt, iterations, memory, parallelism, keyLen)
}

// ErrEmptyPassword is returned by Hash for an empty password; the callers'
// password policy rejects those long before this point.
var ErrEmptyPassword = errors.New("passhash: empty password")

// Hash returns the PHC string of an argon2id hash of password with a fresh
// random salt and the package's current cost.
func Hash(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("passhash: salt: %w", err)
	}
	key := idKey([]byte(password), salt, Iterations, Memory, Parallelism, keyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, Memory, Iterations, Parallelism,
		encoding.EncodeToString(salt), encoding.EncodeToString(key)), nil
}

// Verify reports whether password matches the stored PHC string. It recomputes
// with the parameters recorded in the hash and compares in constant time. A
// value that is not a well-formed argon2id hash within the bounds above never
// matches.
func Verify(encoded, password string) bool {
	p, ok := parse(encoded)
	if !ok {
		return false
	}
	key := idKey([]byte(password), p.salt, p.iterations, p.memory, p.parallelism, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(key, p.key) == 1
}

type params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt, key   []byte
}

// parse reads `$argon2id$v=19$m=…,t=…,p=…$salt$hash`.
func parse(encoded string) (params, bool) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=…,t=…,p=…", salt, hash
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params{}, false
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return params{}, false
	}
	// Sscanf stops at the last verb and ignores whatever follows, so the
	// parameter segment must also read back exactly as it would be written.
	var p params
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil || n != 3 ||
		parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.memory, p.iterations, p.parallelism) {
		return params{}, false
	}
	if p.memory == 0 || p.memory > maxMemory || p.iterations == 0 || p.iterations > maxIterations ||
		p.parallelism == 0 || p.memory < 8*uint32(p.parallelism) {
		return params{}, false
	}
	var err error
	if p.salt, err = encoding.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return params{}, false
	}
	if p.key, err = encoding.DecodeString(parts[5]); err != nil || len(p.key) < 16 {
		return params{}, false
	}
	return p, true
}
