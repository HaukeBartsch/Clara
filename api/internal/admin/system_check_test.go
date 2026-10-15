package admin

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"csms/api/internal/db"
	"csms/api/internal/passhash"
	"csms/api/internal/testdb"
)

// systemCheck reads GET /api/v1/system/check as an administrator.
func (e *env) systemCheck() SystemCheck {
	e.t.Helper()
	rec := e.do("GET", "/api/v1/system/check", nil, e.mustAdmin("check-"+strconv.FormatInt(time.Now().UnixNano(), 36)+"@example.org"))
	if rec.Code != http.StatusOK {
		e.t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out SystemCheck
	e.decode(rec, &out)
	return out
}

// Non-admins and anonymous callers are rejected (REQ-API-159).
func TestSystemCheckRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	user := e.mustUser("check-user@example.org")

	for _, actor := range []*db.User{nil, user} {
		if rec := e.do("GET", "/api/v1/system/check", nil, actor); rec.Code != http.StatusForbidden {
			t.Errorf("GET as %v: status = %d, want 403", actor, rec.Code)
		}
	}
}

// The configuration facts mirror the loaded configuration; secrets are only
// ever named, never echoed (REQ-CFG-021).
func TestSystemCheckConfigurationFacts(t *testing.T) {
	e := newEnv(t)
	e.Cfg.APIAddr = "127.0.0.1:8080"
	e.Cfg.AppTimezone = "Europe/Oslo"

	got := e.systemCheck()
	if got.AppEnv != "development" || got.WebPublicURL != "https://csms.example.org" || got.AppTimezone != "Europe/Oslo" {
		t.Errorf("core = %q %q %q", got.AppEnv, got.WebPublicURL, got.AppTimezone)
	}
	if !got.APIListenLoopback {
		t.Error("api_listen_loopback = false for 127.0.0.1:8080")
	}
	// The fixture's "test-token" and "test-salt" are short, hand-typed values.
	if !slices.Equal(got.WeakSecrets, []string{"INTERNAL_SERVICE_TOKEN", "ANON_SALT"}) &&
		!slices.Equal(got.WeakSecrets, []string{"INTERNAL_SERVICE_TOKEN", "ANON_SALT", "DB_PASSWORD"}) {
		t.Errorf("weak_secrets = %v", got.WeakSecrets)
	}
	if got.SMTPConfigured || got.TrustedProxiesConfigured || got.Require2FA {
		t.Errorf("smtp/proxies/2fa = %v/%v/%v, want all false", got.SMTPConfigured, got.TrustedProxiesConfigured, got.Require2FA)
	}
	if got.Bootstrap.PasswordConfigured || got.Bootstrap.PasswordUnchanged != nil {
		t.Errorf("bootstrap = %+v, want not configured", got.Bootstrap)
	}

	rec := e.do("GET", "/api/v1/system/check", nil, e.mustAdmin("check-raw@example.org"))
	for _, secret := range []string{"test-token", "test-salt"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("response carries the secret value %q", secret)
		}
	}
}

// Generated secrets pass; the placeholder and the development defaults do not.
func TestSystemCheckWeakSecrets(t *testing.T) {
	e := newEnv(t)
	e.Cfg.InternalServiceToken = "3f1c9a7e5b2d4f608a1c3e5f7b9d1f2a"
	e.Cfg.AnonSalt = "9b7d5f3a1c2e4f6a8b0d2f4a6c8e0b1d"
	e.Cfg.DBPassword = "5e3c1a9f7d5b3e1c9a7f5d3b"
	if got := e.Handler.weakSecrets(); len(got) != 0 {
		t.Errorf("generated secrets reported weak: %v", got)
	}

	e.Cfg.InternalServiceToken = "dev-internal-token"
	e.Cfg.AnonSalt = "CHANGE_ME_please_change_me_now"
	got := e.Handler.weakSecrets()
	if !slices.Contains(got, "INTERNAL_SERVICE_TOKEN") || !slices.Contains(got, "ANON_SALT") {
		t.Errorf("weak_secrets = %v, want the default token and the placeholder salt", got)
	}
}

// The bootstrap account is reported while it still signs in with the
// password from .env (INSTALL.md §13.2), and no longer once it changed.
func TestSystemCheckBootstrapPassword(t *testing.T) {
	e := newEnv(t)
	const email, password = "bootstrap@example.org", "first-login-password-1"
	e.Cfg.AdminBootstrapEmail, e.Cfg.AdminBootstrapPassword = email, password

	got := e.systemCheck()
	if !got.Bootstrap.PasswordConfigured || got.Bootstrap.PasswordUnchanged != nil {
		t.Errorf("before the account exists: %+v, want configured with no answer", got.Bootstrap)
	}

	hash, err := passhash.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	u := &db.User{Email: email, DisplayName: "Administrator", Enabled: true, IsAdmin: true, AuthSource: "local",
		PasswordHash: sql.NullString{String: hash, Valid: true}}
	if u.ID, err = e.Store.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got = e.systemCheck()
	if got.Bootstrap.PasswordUnchanged == nil || !*got.Bootstrap.PasswordUnchanged {
		t.Errorf("unchanged password: %+v, want password_unchanged true", got.Bootstrap)
	}

	changed, err := passhash.Hash("a-different-password-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetUserPasswordHash(context.Background(), u.ID, sql.NullString{String: changed, Valid: true}); err != nil {
		t.Fatal(err)
	}
	got = e.systemCheck()
	if got.Bootstrap.PasswordUnchanged == nil || *got.Bootstrap.PasswordUnchanged {
		t.Errorf("changed password: %+v, want password_unchanged false", got.Bootstrap)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true, "127.0.0.2:80": true,
		"0.0.0.0:8080": false, ":8080": false, "[::]:8080": false, "10.0.0.5:8080": false, "garbage": false,
	} {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

// The database half: SQLite reports its engine and nothing MariaDB-specific;
// MariaDB reports its server settings and the audit partitions the writer
// created (Audit_Logging_Design.md §6.1).
func TestSystemCheckDatabaseFacts(t *testing.T) {
	e := newEnv(t)
	d := e.systemCheck().Database

	if d.SchemaVersion < 1 || d.Version == "" {
		t.Fatalf("schema_version = %d, version = %q", d.SchemaVersion, d.Version)
	}
	if testdb.Engine() == "sqlite" {
		if d.Connection != "sqlite" || d.CharacterSetServer != nil || d.Grants != nil || d.AuditPartitions != nil {
			t.Errorf("sqlite facts = %+v, want only connection/version/schema", d)
		}
		return
	}

	if d.Connection != "mariadb" || d.CharacterSetServer == nil || d.MaxAllowedPacket == nil || d.TimeZone == nil {
		t.Fatalf("mariadb facts = %+v", d)
	}
	if d.DatabaseCharacterSet == nil || *d.DatabaseCharacterSet != "utf8mb4" {
		t.Errorf("database_character_set = %v, want utf8mb4", d.DatabaseCharacterSet)
	}
	year := time.Now().UTC().Year()
	for _, table := range []string{"audit_events", "audit_record_views"} {
		for _, p := range []string{"p" + strconv.Itoa(year), "p" + strconv.Itoa(year+1)} {
			if !slices.Contains(d.AuditPartitions[table], p) {
				t.Errorf("audit_partitions[%s] = %v, missing %s", table, d.AuditPartitions[table], p)
			}
		}
	}
	// The fixture connects as the administrative test account: every table is
	// writable, the audit tables included — the "install" state.
	if d.Grants == nil || len(d.Grants.AuditWritable) != 2 || len(d.Grants.TablesMissingWrite) != 0 {
		t.Errorf("grants as the test administrator = %+v, want both audit tables writable", d.Grants)
	}
}

// With the runtime grants of INSTALL.md §9.2 the audit tables are append-only
// and nothing else is missing; a table created afterwards without its grants
// is reported until `clara-db-grants runtime` runs again (§15.4).
func TestSystemCheckRuntimeGrants(t *testing.T) {
	if testdb.Engine() != "mariadb" {
		t.Skip("grants exist on MariaDB only (CLARA_TEST_DB=mariadb)")
	}
	e := newEnv(t)
	ctx := context.Background()
	root := e.Store.DB
	schema := e.Cfg.DBDatabase
	account := "chk_" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	grantee := "'" + account + "'@'%'"

	exec := func(q string) {
		t.Helper()
		if _, err := root.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("CREATE USER " + grantee + " IDENTIFIED BY 'check-password-1'")
	t.Cleanup(func() { root.ExecContext(context.Background(), "DROP USER IF EXISTS "+grantee) })
	exec("GRANT SELECT, INSERT, CREATE, ALTER ON `" + schema + "`.* TO " + grantee)
	rows, err := root.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND TABLE_NAME NOT IN ('audit_events', 'audit_record_views')`, schema)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	for _, n := range tables {
		exec("GRANT UPDATE, DELETE ON `" + schema + "`.`" + n + "` TO " + grantee)
	}

	cfg := *e.Cfg
	cfg.DBUsername, cfg.DBPassword = account, "check-password-1"
	store, err := db.Open(&cfg, nil)
	if err != nil {
		t.Fatalf("open as %s: %v", account, err)
	}
	defer store.Close()

	f, err := store.DatabaseFacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.Grants == nil || len(f.Grants.AuditWritable) != 0 || len(f.Grants.TablesMissingWrite) != 0 || f.Grants.Drop {
		t.Fatalf("runtime grants = %+v, want audit append-only and nothing missing", f.Grants)
	}

	exec("CREATE TABLE `" + schema + "`.added_by_upgrade (id INT PRIMARY KEY)")
	f, err = store.DatabaseFacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.Grants == nil || !slices.Equal(f.Grants.TablesMissingWrite, []string{"added_by_upgrade"}) {
		t.Errorf("after a new table: %+v, want added_by_upgrade missing", f.Grants)
	}
}
