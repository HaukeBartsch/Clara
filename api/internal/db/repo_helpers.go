package db

import (
	"crypto/rand"
	"database/sql"
	"fmt"
)

// boolToInt adapts a bool to the INTEGER 0/1 flag storage (ASM-DB-2).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStr adapts a sql.NullString to a driver value (NULL when not valid).
func nullStr(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// nullInt64 adapts a sql.NullInt64 to a driver value (NULL when not valid).
func nullInt64(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

// newToken returns a random UUID v4 string (GD-5, REQ-AUTH-029) from
// crypto/rand, in the canonical hyphenated 8-4-4-4-12 form (CHAR(36)).
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generate token: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
