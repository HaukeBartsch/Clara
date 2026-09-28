package main

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/config"
	"csms/api/internal/db"
)

// openTestStore is the fixture for these tests: a SQLite store with the schema
// applied, mirroring internal/db/migrate_test.go. bootstrapAdmin drives the
// users table, so migrations must run first (REQ-DB-003).
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	cfg := &config.Config{
		AppEnv:               "development",
		DBConnection:         "sqlite",
		DBDatabase:           filepath.Join(t.TempDir(), "server.sqlite"),
		AnonSalt:             "test-salt",
		InternalServiceToken: "test-token",
	}
	store, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func userCount(t *testing.T, store *db.Store) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// TestLogLevel covers the LOG_LEVEL_API mapping (System_Configuration_Design.md
// §3.1). config.Load rejects anything outside the four lowercase names, so the
// fallback matters for an unset or unvalidated value: it must land on info and
// never silence the startup log.
func TestLogLevel(t *testing.T) {
	cases := []struct {
		label string
		in    string
		want  slog.Level
	}{
		{"debug", "debug", slog.LevelDebug},
		{"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"empty falls back to info", "", slog.LevelInfo},
		{"unknown falls back to info", "trace", slog.LevelInfo},
		// The accepted names are lowercase; a capitalized value is invalid input,
		// not a second spelling.
		{"uppercase falls back to info", "INFO", slog.LevelInfo},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := logLevel(tc.in); got != tc.want {
				t.Errorf("logLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestBootstrapAdminUnsetIsNoOp: the bootstrap pair is optional when an OAuth2
// provider or LDAP server exists (System_Configuration_Design.md §4.1), so a
// half-configured or unset pair must not create an account (REQ-CFG-014/025).
func TestBootstrapAdminUnsetIsNoOp(t *testing.T) {
	cases := []struct {
		label    string
		email    string
		password string
	}{
		{"both unset", "", ""},
		{"password unset", "admin@example.org", ""},
		{"email unset", "", "bootstrap-passphrase"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			cfg := &config.Config{AdminBootstrapEmail: tc.email, AdminBootstrapPassword: tc.password}

			if err := bootstrapAdmin(ctx, store, cfg); err != nil {
				t.Fatalf("bootstrapAdmin: %v", err)
			}
			if n := userCount(t, store); n != 0 {
				t.Errorf("users after bootstrapAdmin = %d, want 0 (no account without both settings)", n)
			}
			if tc.email != "" {
				u, err := store.GetUserByEmail(ctx, tc.email)
				if err != nil {
					t.Fatalf("GetUserByEmail: %v", err)
				}
				if u != nil {
					t.Errorf("bootstrap admin %q was created with an unset password", tc.email)
				}
			}
		})
	}
}

// TestBootstrapAdminCreatesAccount proves the first-install path: the account
// from ADMIN_BOOTSTRAP_EMAIL exists, is enabled and an administrator (GD-4,
// REQ-AUTH-007), and its password is stored as a bcrypt hash of
// ADMIN_BOOTSTRAP_PASSWORD — never the plaintext (GD-18, REQ-CFG-025,
// REQ-AUTH-036/050).
func TestBootstrapAdminCreatesAccount(t *testing.T) {
	const (
		email     = "admin@example.org"
		plaintext = "bootstrap-passphrase"
	)
	store := openTestStore(t)
	ctx := context.Background()
	cfg := &config.Config{AdminBootstrapEmail: email, AdminBootstrapPassword: plaintext}

	if err := bootstrapAdmin(ctx, store, cfg); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	u, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u == nil {
		t.Fatalf("bootstrap admin %q not created", email)
	}
	if u.Email != email {
		t.Errorf("stored email = %q, want %q", u.Email, email)
	}
	if u.DisplayName != "Administrator" {
		t.Errorf("display_name = %q, want %q", u.DisplayName, "Administrator")
	}
	if !u.Enabled {
		t.Errorf("bootstrap admin created disabled (REQ-AUTH-006: it must be usable at once)")
	}
	if !u.IsAdmin {
		t.Errorf("bootstrap admin created with is_admin = false (GD-4, REQ-AUTH-007)")
	}
	if !u.PasswordHash.Valid || u.PasswordHash.String == "" {
		t.Fatalf("password_hash is NULL — the bootstrap account would have no local password (REQ-CFG-025)")
	}
	stored := u.PasswordHash.String
	if stored == plaintext {
		t.Errorf("password_hash holds the plaintext %q (REQ-AUTH-036)", stored)
	}
	if strings.Contains(stored, plaintext) {
		t.Errorf("password_hash %q leaks the plaintext password", stored)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(plaintext)); err != nil {
		t.Errorf("stored hash does not verify the configured password: %v", err)
	}
	// Cost >= 10 (Authentication_Authorization_Design.md; bcrypt.DefaultCost).
	if cost, err := bcrypt.Cost([]byte(stored)); err != nil {
		t.Errorf("stored hash is not a valid bcrypt hash: %v", err)
	} else if cost < 10 {
		t.Errorf("bcrypt cost = %d, want >= 10", cost)
	}
	if n := userCount(t, store); n != 1 {
		t.Errorf("users after bootstrapAdmin = %d, want 1", n)
	}
}

// TestBootstrapAdminIdempotent: the API re-runs bootstrapAdmin on every start
// (main.go), so it must neither duplicate the account nor reset a password an
// administrator changed through the users API — password changes go through
// REQ-API-047/048, not startup (REQ-AUTH-050).
func TestBootstrapAdminIdempotent(t *testing.T) {
	const (
		email     = "admin@example.org"
		plaintext = "bootstrap-passphrase"
	)
	store := openTestStore(t)
	ctx := context.Background()
	cfg := &config.Config{AdminBootstrapEmail: email, AdminBootstrapPassword: plaintext}

	if err := bootstrapAdmin(ctx, store, cfg); err != nil {
		t.Fatalf("first bootstrapAdmin: %v", err)
	}
	first, err := store.GetUserByEmail(ctx, email)
	if err != nil || first == nil {
		t.Fatalf("GetUserByEmail after first run: %v", err)
	}
	if err := bootstrapAdmin(ctx, store, cfg); err != nil {
		t.Fatalf("second bootstrapAdmin: %v", err)
	}
	second, err := store.GetUserByEmail(ctx, email)
	if err != nil || second == nil {
		t.Fatalf("GetUserByEmail after second run: %v", err)
	}
	if n := userCount(t, store); n != 1 {
		t.Errorf("users after two runs = %d, want 1 (bootstrap must be idempotent)", n)
	}
	if second.ID != first.ID {
		t.Errorf("second run created user %d, want the existing %d", second.ID, first.ID)
	}
	if second.PasswordHash.String != first.PasswordHash.String {
		t.Errorf("second run rewrote password_hash (idempotency)")
	}

	// A password changed after provisioning — e.g. an administrator rotating it
	// in the UI — must survive a restart.
	rotated, err := bcrypt.GenerateFromPassword([]byte("rotated-passphrase"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := store.SetUserPasswordHash(ctx, first.ID, sql.NullString{String: string(rotated), Valid: true}); err != nil {
		t.Fatalf("SetUserPasswordHash: %v", err)
	}
	if err := bootstrapAdmin(ctx, store, cfg); err != nil {
		t.Fatalf("third bootstrapAdmin: %v", err)
	}
	after, err := store.GetUserByEmail(ctx, email)
	if err != nil || after == nil {
		t.Fatalf("GetUserByEmail after third run: %v", err)
	}
	if after.PasswordHash.String != string(rotated) {
		t.Errorf("restart overwrote a rotated password_hash (REQ-AUTH-050: changes go through the users API)")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(after.PasswordHash.String), []byte(plaintext)); err == nil {
		t.Error("the bootstrap password still verifies after rotation — the hash was replaced")
	}
	if n := userCount(t, store); n != 1 {
		t.Errorf("users after three runs = %d, want 1", n)
	}
	// The row stays an enabled administrator across restarts (REQ-AUTH-007).
	if !after.Enabled || !after.IsAdmin {
		t.Errorf("after re-bootstrap enabled=%v is_admin=%v, want both true", after.Enabled, after.IsAdmin)
	}
}
