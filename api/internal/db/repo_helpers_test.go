package db

import (
	"database/sql"
	"strings"
	"testing"
)

// The driver adapters are pure (ASM-DB-2): a Go bool is stored as the INTEGER
// 0/1 flag, and a nullable model field reaches the driver as nil for NULL so
// both drivers bind it the same way.

func TestBoolToInt(t *testing.T) {
	if got := boolToInt(true); got != 1 {
		t.Errorf("boolToInt(true) = %d, want 1", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Errorf("boolToInt(false) = %d, want 0", got)
	}
}

func TestNullAdapters(t *testing.T) {
	if got := nullStr(sql.NullString{String: "x", Valid: true}); got != "x" {
		t.Errorf("nullStr(valid) = %v, want \"x\"", got)
	}
	// An invalid NullString is NULL even when it carries a string value.
	if got := nullStr(sql.NullString{String: "ignored"}); got != nil {
		t.Errorf("nullStr(invalid) = %v, want nil", got)
	}
	if got := nullInt64(sql.NullInt64{Int64: 7, Valid: true}); got != int64(7) {
		t.Errorf("nullInt64(valid) = %v, want 7", got)
	}
	if got := nullInt64(sql.NullInt64{Int64: 7}); got != nil {
		t.Errorf("nullInt64(invalid) = %v, want nil", got)
	}
	// A valid zero is a stored 0, not NULL — the two are distinct states.
	if got := nullInt64(sql.NullInt64{Valid: true}); got != int64(0) {
		t.Errorf("nullInt64(valid zero) = %v, want 0", got)
	}
}

// newToken produces the CHAR(36) UUID v4 form (GD-5, REQ-AUTH-029): the
// version nibble is 4 and the RFC 4122 variant nibble is one of 8-9-a-b.
func TestNewTokenShape(t *testing.T) {
	const hexDigits = "0123456789abcdef"
	for range 50 {
		tok := newToken()
		if len(tok) != 36 {
			t.Fatalf("newToken() = %q has length %d, want 36", tok, len(tok))
		}
		for _, dash := range []int{8, 13, 18, 23} {
			if tok[dash] != '-' {
				t.Fatalf("newToken() = %q, want '-' at index %d", tok, dash)
			}
		}
		if tok[14] != '4' {
			t.Errorf("newToken() = %q is not version 4 (index 14 = %q)", tok, tok[14])
		}
		if !strings.ContainsRune("89ab", rune(tok[19])) {
			t.Errorf("newToken() = %q is not RFC 4122 variant (index 19 = %q)", tok, tok[19])
		}
		for j := 0; j < len(tok); j++ {
			if j == 8 || j == 13 || j == 18 || j == 23 {
				continue
			}
			if !strings.ContainsRune(hexDigits, rune(tok[j])) {
				t.Fatalf("newToken() = %q has non-hex byte %q at %d (mixed case?)", tok, tok[j], j)
			}
		}
	}
}

// Tokens are the only credential a data-API caller presents, so a collision
// would hand one caller another's project. Drawn from crypto/rand, they must
// come out distinct.
func TestNewTokensAreDistinct(t *testing.T) {
	const n = 5000
	seen := make(map[string]bool, n)
	for range n {
		tok := newToken()
		if seen[tok] {
			t.Fatalf("newToken() repeated %q — tokens must be unique", tok)
		}
		seen[tok] = true
	}
}
