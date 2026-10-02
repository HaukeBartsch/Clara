package db

import (
	"context"
)

// --- system settings (REQ-DB-037) ---

// The column `key` is a reserved word in MariaDB; it is quoted with backticks,
// which SQLite accepts as well, so these queries use interpreted strings.

// SystemSettings returns the runtime settings registry as key → raw JSON
// scalar text (rate_limit_enabled, rate_limit_rpm, …). Values are
// interpreted and validated at the API boundary (REQ-API-112).
func (s *Store) SystemSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT `key`, value FROM system_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSystemSetting upserts one runtime setting (REQ-API-112). Portable
// update-then-insert: the primary key keeps a racing insert safe (one wins).
func (s *Store) SetSystemSetting(ctx context.Context, key, value string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE system_settings SET value = ? WHERE `key` = ?", value, key)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	// MariaDB reports rows *changed*, not rows matched: re-saving the current
	// value affects 0 rows although the key exists, and the INSERT below
	// would then fail on the primary key. Check before inserting.
	var exists int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM system_settings WHERE `key` = ?", key).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO system_settings (`key`, value) VALUES (?, ?)", key, value)
	return err
}
