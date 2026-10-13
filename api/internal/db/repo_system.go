package db

// Database facts for the Configuration Check (API_Endpoints_Design.md §4.24,
// REQ-API-159): what the server and the application's own account look like
// from inside the connection the API uses. Every value here is something
// INSTALL.md §8–§9 asks the operator to set; the evaluation (which value is
// a problem) belongs to the web layer, so this file only reads.
//
// The MariaDB reads go to server variables and information_schema, which an
// unprivileged account may query for itself. On SQLite only the engine
// version exists — the server settings, grants and audit partitions are
// MariaDB concepts, and their fields stay nil.

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// auditTables are the append-only tables of REQ-DB-024 (INSTALL.md §9.2).
var auditTables = []string{"audit_events", "audit_record_views"}

// DatabaseFacts is the database half of the configuration check.
type DatabaseFacts struct {
	Connection           string              `json:"connection"`
	SchemaVersion        int                 `json:"schema_version"`
	Version              string              `json:"version"`
	CharacterSetServer   *string             `json:"character_set_server"`
	CollationServer      *string             `json:"collation_server"`
	DatabaseCharacterSet *string             `json:"database_character_set"`
	DatabaseCollation    *string             `json:"database_collation"`
	TimeZone             *string             `json:"time_zone"`
	SystemTimeZone       *string             `json:"system_time_zone"`
	MaxAllowedPacket     *int64              `json:"max_allowed_packet"`
	BindAddress          *string             `json:"bind_address"`
	Grants               *GrantFacts         `json:"grants"`
	AuditPartitions      map[string][]string `json:"audit_partitions"`
}

// GrantFacts summarizes the application account's effective privileges on
// its schema — global, schema and table grants combined (roles are not
// followed). The runtime state of `clara-db-grants` leaves AuditWritable and
// TablesMissingWrite empty and Drop false.
type GrantFacts struct {
	Account            string   `json:"account"`
	AuditWritable      []string `json:"audit_writable"`
	TablesMissingWrite []string `json:"tables_missing_write"`
	Drop               bool     `json:"drop"`
}

// DatabaseFacts reads the facts. Only the schema version and engine version
// are required; a MariaDB value the account may not read is left nil rather
// than failing the whole check.
func (s *Store) DatabaseFacts(ctx context.Context) (DatabaseFacts, error) {
	f := DatabaseFacts{Connection: string(s.Dialect)}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		return f, err
	}
	f.SchemaVersion = v

	if s.Dialect == DialectSQLite {
		err := s.DB.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&f.Version)
		return f, err
	}

	if err := s.DB.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&f.Version); err != nil {
		return f, err
	}
	f.CharacterSetServer = s.stringVar(ctx, `SELECT @@global.character_set_server`)
	f.CollationServer = s.stringVar(ctx, `SELECT @@global.collation_server`)
	f.TimeZone = s.stringVar(ctx, `SELECT @@global.time_zone`)
	f.SystemTimeZone = s.stringVar(ctx, `SELECT @@system_time_zone`)
	f.BindAddress = s.stringVar(ctx, `SELECT @@global.bind_address`)
	var packet int64
	if err := s.DB.QueryRowContext(ctx, `SELECT @@global.max_allowed_packet`).Scan(&packet); err == nil {
		f.MaxAllowedPacket = &packet
	}
	var cs, coll string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA
		 WHERE SCHEMA_NAME = DATABASE()`).Scan(&cs, &coll); err == nil {
		f.DatabaseCharacterSet, f.DatabaseCollation = &cs, &coll
	}
	if g, err := s.grantFacts(ctx); err == nil {
		f.Grants = g
	}
	if p, err := s.auditPartitions(ctx); err == nil {
		f.AuditPartitions = p
	}
	return f, nil
}

// stringVar reads one server variable: nil when it cannot be read, "" when
// it is NULL — an unset bind_address, for one, means every interface.
func (s *Store) stringVar(ctx context.Context, query string) *string {
	var v sql.NullString
	if err := s.DB.QueryRowContext(ctx, query).Scan(&v); err != nil {
		return nil
	}
	return &v.String
}

// grantFacts combines the account's global, schema and table privileges.
// information_schema names the grantee as 'user'@'host', built here from
// CURRENT_USER() — the account the server matched, not the login name.
func (s *Store) grantFacts(ctx context.Context) (*GrantFacts, error) {
	var current string
	if err := s.DB.QueryRowContext(ctx, `SELECT CURRENT_USER()`).Scan(&current); err != nil {
		return nil, err
	}
	at := strings.LastIndex(current, "@")
	if at < 0 {
		return nil, sql.ErrNoRows
	}
	grantee := "'" + current[:at] + "'@'" + current[at+1:] + "'"

	broad := map[string]bool{} // global + schema level: apply to every table
	for _, q := range []string{
		`SELECT PRIVILEGE_TYPE FROM information_schema.USER_PRIVILEGES WHERE GRANTEE = ?`,
		`SELECT PRIVILEGE_TYPE FROM information_schema.SCHEMA_PRIVILEGES
		 WHERE GRANTEE = ? AND TABLE_SCHEMA = DATABASE()`,
	} {
		if err := s.collect(ctx, q, func(rows *sql.Rows) error {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			broad[p] = true
			return nil
		}, grantee); err != nil {
			return nil, err
		}
	}

	perTable := map[string]map[string]bool{}
	if err := s.collect(ctx,
		`SELECT TABLE_NAME, PRIVILEGE_TYPE FROM information_schema.TABLE_PRIVILEGES
		 WHERE GRANTEE = ? AND TABLE_SCHEMA = DATABASE()`, func(rows *sql.Rows) error {
			var t, p string
			if err := rows.Scan(&t, &p); err != nil {
				return err
			}
			if perTable[t] == nil {
				perTable[t] = map[string]bool{}
			}
			perTable[t][p] = true
			return nil
		}, grantee); err != nil {
		return nil, err
	}

	var tables []string
	if err := s.collect(ctx,
		`SELECT TABLE_NAME FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`, func(rows *sql.Rows) error {
			var t string
			if err := rows.Scan(&t); err != nil {
				return err
			}
			tables = append(tables, t)
			return nil
		}); err != nil {
		return nil, err
	}
	sort.Strings(tables)

	has := func(table, priv string) bool { return broad[priv] || perTable[table][priv] }
	g := &GrantFacts{Account: current, AuditWritable: []string{}, TablesMissingWrite: []string{}, Drop: broad["DROP"]}
	for _, t := range tables {
		if isAuditTable(t) {
			if has(t, "UPDATE") || has(t, "DELETE") || has(t, "DROP") {
				g.AuditWritable = append(g.AuditWritable, t)
			}
			continue
		}
		if !has(t, "UPDATE") || !has(t, "DELETE") {
			g.TablesMissingWrite = append(g.TablesMissingWrite, t)
		}
	}
	return g, nil
}

// auditPartitions lists each audit table's partitions by name (`p2026`, …);
// an unpartitioned table maps to an empty list.
func (s *Store) auditPartitions(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	for _, t := range auditTables {
		out[t] = []string{}
	}
	err := s.collect(ctx,
		`SELECT TABLE_NAME, PARTITION_NAME FROM information_schema.PARTITIONS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN (?, ?) AND PARTITION_NAME IS NOT NULL
		 ORDER BY TABLE_NAME, PARTITION_ORDINAL_POSITION`, func(rows *sql.Rows) error {
			var t, p string
			if err := rows.Scan(&t, &p); err != nil {
				return err
			}
			out[t] = append(out[t], p)
			return nil
		}, auditTables[0], auditTables[1])
	return out, err
}

// collect runs a query and hands every row to fn.
func (s *Store) collect(ctx context.Context, query string, fn func(*sql.Rows) error, args ...any) error {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func isAuditTable(name string) bool {
	for _, t := range auditTables {
		if name == t {
			return true
		}
	}
	return false
}
