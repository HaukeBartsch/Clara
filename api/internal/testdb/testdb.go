// Package testdb lets the test fixtures run against either storage engine
// (REQ-TECH-005/007: SQLite in development and tests, MariaDB in production;
// SQL targets features common to both). It is test support only — nothing in
// the server imports it.
//
// By default nothing changes: a fixture keeps the SQLite file it configured.
// With CLARA_TEST_DB=mariadb, Use rewrites the fixture's config to a fresh,
// uniquely named MariaDB database that is dropped when the test ends:
//
//	docker compose -f ci/mariadb.compose.yml up -d --wait
//	CLARA_TEST_DB=mariadb go test ./...
//
// Server address and administrative account come from CLARA_TEST_MARIADB_HOST
// (127.0.0.1), _PORT (3306), _USER (root) and _PASSWORD (clara-dev-root, the
// development-only value of ci/mariadb.compose.yml). The account needs
// CREATE/DROP DATABASE.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"csms/api/internal/config"
)

// Engine reports the engine the fixtures run on: "sqlite" or "mariadb".
func Engine() string {
	if strings.EqualFold(os.Getenv("CLARA_TEST_DB"), "mariadb") {
		return "mariadb"
	}
	return "sqlite"
}

// Use points cfg at a fresh MariaDB database when CLARA_TEST_DB=mariadb and
// leaves it untouched otherwise. Call it after building the fixture's config
// and before db.Open.
func Use(t testing.TB, cfg *config.Config) {
	t.Helper()
	if Engine() != "mariadb" {
		return
	}
	host, port := env("CLARA_TEST_MARIADB_HOST", "127.0.0.1"), env("CLARA_TEST_MARIADB_PORT", "3306")
	user, pass := env("CLARA_TEST_MARIADB_USER", "root"), env("CLARA_TEST_MARIADB_PASSWORD", "clara-dev-root")

	admin, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/", user, pass, host, port))
	if err != nil {
		t.Fatalf("testdb: open admin connection: %v", err)
	}
	defer admin.Close()

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("testdb: random name: %v", err)
	}
	name := "clara_t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("testdb: create database on %s:%s (is ci/mariadb.compose.yml up?): %v", host, port, err)
	}
	t.Cleanup(func() {
		if os.Getenv("CLARA_TEST_KEEP_DB") != "" {
			t.Logf("testdb: kept database %s", name)
			return
		}
		a, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/", user, pass, host, port))
		if err != nil {
			return
		}
		defer a.Close()
		// A connection still holding a transaction on the test database blocks
		// DROP DATABASE on its metadata lock. SQLite fixtures never notice (the
		// file is just deleted); here it is a leak worth failing the test for,
		// so wait briefly instead of forever.
		a.SetMaxOpenConns(1)
		_, _ = a.Exec("SET SESSION lock_wait_timeout = 10")
		if _, err := a.Exec("DROP DATABASE IF EXISTS `" + name + "`"); err != nil {
			t.Errorf("testdb: drop %s: %v (a transaction or result set was left open on the store?)", name, err)
		}
	})

	cfg.DBConnection = "mariadb"
	cfg.DBHost, cfg.DBPort = host, port
	cfg.DBUsername, cfg.DBPassword = user, pass
	cfg.DBDatabase = name
}

// SQLiteOnly skips a test that inspects SQLite internals (sqlite_master,
// PRAGMA, file paths) when the fixtures run on MariaDB.
func SQLiteOnly(t testing.TB) {
	t.Helper()
	if Engine() != "sqlite" {
		t.Skip("SQLite-specific test; CLARA_TEST_DB=" + Engine())
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
