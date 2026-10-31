package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// ProviderRoleRegion is one row of the provider role mapping (REQ-DB-053, migration
// 0020): a role value an OpenID Connect provider released, against the region it
// names. Region NULL means "undecided" while Reviewed is false and "names no region"
// once it is true. No account is linked to a value — which roles a person holds is
// not stored (DEV-DB-18).
type ProviderRoleRegion struct {
	ID          int64
	Issuer      string
	RoleValue   string
	Region      sql.NullString
	Reviewed    bool
	FirstSeenAt sql.NullString // UTC; NULL for a value entered before any sign-in carried it
	LastSeenAt  sql.NullString // UTC
	SeenCount   int64
}

// MaxRoleValueLength is the widest role value the mapping stores (REQ-DB-053); a
// sign-in carrying a longer one skips it rather than truncating it into a different
// value (Authentication_Authorization_Design.md §2.12).
const MaxRoleValueLength = 255

const providerRoleRegionColumns = `id, issuer, role_value, region, reviewed,
	first_seen_at, last_seen_at, seen_count`

func scanProviderRoleRegion(row interface{ Scan(dest ...any) error }) (ProviderRoleRegion, error) {
	var (
		m         ProviderRoleRegion
		reviewed  int
		firstSeen any
		lastSeen  any
	)
	if err := row.Scan(&m.ID, &m.Issuer, &m.RoleValue, &m.Region, &reviewed,
		&firstSeen, &lastSeen, &m.SeenCount); err != nil {
		return m, err
	}
	m.Reviewed = reviewed != 0
	if v, ok := datetimeString(firstSeen); ok {
		m.FirstSeenAt = sql.NullString{String: v, Valid: true}
	}
	if v, ok := datetimeString(lastSeen); ok {
		m.LastSeenAt = sql.NullString{String: v, Valid: true}
	}
	return m, nil
}

// ListProviderRoleRegions returns the whole mapping in the order the Control Panel
// shows it: values no administrator has decided on first, then by issuer and value
// (REQ-API-177).
func (s *Store) ListProviderRoleRegions(ctx context.Context) ([]ProviderRoleRegion, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+providerRoleRegionColumns+`
		FROM provider_role_regions ORDER BY reviewed, issuer, role_value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderRoleRegion{}
	for rows.Next() {
		m, err := scanProviderRoleRegion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetProviderRoleRegion returns one mapping row, or nil when the id names none.
func (s *Store) GetProviderRoleRegion(ctx context.Context, id int64) (*ProviderRoleRegion, error) {
	m, err := scanProviderRoleRegion(s.DB.QueryRowContext(ctx, `SELECT `+providerRoleRegionColumns+`
		FROM provider_role_regions WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetProviderRoleRegionByValue returns the row of one (issuer, role value) pair, or nil.
func (s *Store) GetProviderRoleRegionByValue(ctx context.Context, issuer, value string) (*ProviderRoleRegion, error) {
	m, err := scanProviderRoleRegion(s.DB.QueryRowContext(ctx, `SELECT `+providerRoleRegionColumns+`
		FROM provider_role_regions WHERE issuer = ? AND role_value = ?`, issuer, value))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// CreateProviderRoleRegionTx enters a value an administrator maps before any sign-in
// has carried it (REQ-API-178): reviewed, never seen, counted zero.
func (s *Store) CreateProviderRoleRegionTx(ctx context.Context, tx *sql.Tx, m *ProviderRoleRegion) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO provider_role_regions (issuer, role_value, region, reviewed, seen_count)
		 VALUES (?, ?, ?, 1, 0)`,
		m.Issuer, m.RoleValue, nullStr(m.Region))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateProviderRoleRegionTx records an administrator's decision on a value: its
// region, or NULL for "names no region". Either way the row is reviewed from now on.
func (s *Store) UpdateProviderRoleRegionTx(ctx context.Context, tx *sql.Tx, id int64, region sql.NullString) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE provider_role_regions SET region = ?, reviewed = 1 WHERE id = ?`, nullStr(region), id)
	return err
}

// DeleteProviderRoleRegionTx removes a row; a later sign-in carrying the value enters
// it again as undecided (REQ-API-178).
func (s *Store) DeleteProviderRoleRegionTx(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM provider_role_regions WHERE id = ?`, id)
	return err
}

// RecordRoleSightings notes that a sign-in carried these role values (REQ-AUTH-088): a
// new value enters undecided, a known one is counted and its last sighting moved, and a
// value an administrator entered ahead of time gets its first sighting. One upsert per
// value in one transaction; the caller has already trimmed, bounded and de-duplicated
// the list.
func (s *Store) RecordRoleSightings(ctx context.Context, issuer string, values []string, now time.Time) error {
	if len(values) == 0 {
		return nil
	}
	ts := now.UTC().Format(datetimeLayout)
	stmt := `INSERT INTO provider_role_regions (issuer, role_value, reviewed, first_seen_at, last_seen_at, seen_count)
		VALUES (?, ?, 0, ?, ?, 1)
		ON CONFLICT (issuer, role_value) DO UPDATE SET
			first_seen_at = COALESCE(provider_role_regions.first_seen_at, excluded.first_seen_at),
			last_seen_at = excluded.last_seen_at,
			seen_count = provider_role_regions.seen_count + 1`
	if s.Dialect == DialectMariaDB {
		stmt = `INSERT INTO provider_role_regions (issuer, role_value, reviewed, first_seen_at, last_seen_at, seen_count)
		VALUES (?, ?, 0, ?, ?, 1)
		ON DUPLICATE KEY UPDATE
			first_seen_at = COALESCE(first_seen_at, VALUES(first_seen_at)),
			last_seen_at = VALUES(last_seen_at),
			seen_count = seen_count + 1`
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range values {
		if _, err := tx.ExecContext(ctx, stmt, issuer, v, ts, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MappedRegions returns, for the given values of one provider, the region each maps
// to — only reviewed rows that name a region appear (REQ-AUTH-088).
func (s *Store) MappedRegions(ctx context.Context, issuer string, values []string) (map[string]string, error) {
	out := map[string]string{}
	if len(values) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(values)+1)
	args = append(args, issuer)
	for _, v := range values {
		args = append(args, v)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT role_value, region FROM provider_role_regions
		WHERE issuer = ? AND reviewed = 1 AND region IS NOT NULL
		AND role_value IN (?`+strings.Repeat(", ?", len(values)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var value, region string
		if err := rows.Scan(&value, &region); err != nil {
			return nil, err
		}
		out[value] = region
	}
	return out, rows.Err()
}
