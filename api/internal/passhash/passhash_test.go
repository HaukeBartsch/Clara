package passhash

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashRoundTrip(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$", Memory, Iterations, Parallelism)
	if !strings.HasPrefix(hash, want) {
		t.Fatalf("hash %q does not carry its parameters (%s…)", hash, want)
	}
	if !Verify(hash, "correct horse battery staple") {
		t.Fatal("the password does not verify against its own hash")
	}
	if Verify(hash, "correct horse battery stapl") || Verify(hash, "") {
		t.Fatal("a wrong password verified")
	}
}

func TestSaltIsFreshPerHash(t *testing.T) {
	a, _ := Hash("same password twelve")
	b, _ := Hash("same password twelve")
	if a == b {
		t.Fatal("two hashes of one password are identical — the salt is not random")
	}
	if !Verify(a, "same password twelve") || !Verify(b, "same password twelve") {
		t.Fatal("both hashes must verify")
	}
}

// Passwords beyond bcrypt's 72-byte input limit hash and verify in full: the
// 73rd byte matters.
func TestLongAndMultibytePasswords(t *testing.T) {
	long := strings.Repeat("æøå", 100) // 600 bytes
	hash, err := Hash(long)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(hash, long) {
		t.Fatal("a long password does not verify")
	}
	if Verify(hash, long[:len(long)-2]+"x") {
		t.Fatal("a change past byte 72 went unnoticed")
	}
}

// A stored hash keeps verifying with the cost it was made with, so the package
// constants can be raised without invalidating existing passwords.
func TestVerifiesWithTheStoredParameters(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("an older cheaper hash"), salt, 1, 8*1024, 1, 32)
	old := fmt.Sprintf("$argon2id$v=19$m=%d,t=1,p=1$%s$%s", 8*1024, encoding.EncodeToString(salt), encoding.EncodeToString(key))
	if !Verify(old, "an older cheaper hash") {
		t.Fatal("a hash made with other parameters must still verify")
	}
}

// Nothing but argon2id is accepted — no bcrypt carry-over (owner decision
// 2026-10-11) — and a malformed or out-of-bounds value never verifies.
func TestRejectsOtherFormats(t *testing.T) {
	// bcrypt (cost 10) of "legacy password", the format stored before 2026-10-11.
	const legacy = "$2a$10$8hx.XKjF4b5ixv6i9Lwpu.xsyKdbahlJlOvm7q8/NRS5PV5Uihfzu"
	valid, _ := Hash("a valid password")
	salt := encoding.EncodeToString([]byte("0123456789abcdef"))
	key := encoding.EncodeToString(make([]byte, 32))
	for name, encoded := range map[string]string{
		"bcrypt":           legacy,
		"empty":            "",
		"plaintext":        "legacy password",
		"argon2i":          strings.Replace(valid, "$argon2id$", "$argon2i$", 1),
		"wrong version":    strings.Replace(valid, "v=19", "v=16", 1),
		"missing part":     valid[:strings.LastIndex(valid, "$")],
		"bad base64":       valid + "!",
		"absurd memory":    fmt.Sprintf("$argon2id$v=19$m=%d,t=1,p=1$%s$%s", 4*1024*1024, salt, key),
		"absurd passes":    fmt.Sprintf("$argon2id$v=19$m=8192,t=1000,p=1$%s$%s", salt, key),
		"zero lanes":       fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=0$%s$%s", salt, key),
		"short salt":       fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s", encoding.EncodeToString([]byte("abc")), key),
		"trailing garbage": strings.Replace(valid, ",p=4$", ",p=4,x=1$", 1),
	} {
		if Verify(encoded, "legacy password") || Verify(encoded, "a valid password") {
			t.Errorf("%s: %q verified", name, encoded)
		}
	}
	if _, err := Hash(""); err != ErrEmptyPassword {
		t.Errorf("Hash(\"\") = %v, want ErrEmptyPassword", err)
	}
}

// More concurrent logins than CPUs queue for a slot rather than each holding
// its 64 MiB at once, and every one still completes with the right answer.
func TestConcurrentVerificationsShareTheSlots(t *testing.T) {
	hash, _ := Hash("concurrent password")
	n := 3*cap(slots) + 1
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			pw := "concurrent password"
			if i%2 == 1 {
				pw = "wrong password"
			}
			results <- Verify(hash, pw) == (i%2 == 0)
		}(i)
	}
	for i := 0; i < n; i++ {
		if !<-results {
			t.Fatal("a concurrent verification answered wrongly")
		}
	}
	if len(slots) != 0 {
		t.Fatalf("%d slots still held after every verification returned", len(slots))
	}
}

func BenchmarkHash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Hash("benchmark password"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerify(b *testing.B) {
	hash, _ := Hash("benchmark password")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Verify(hash, "benchmark password")
	}
}
