package db

import (
	"context"
	"database/sql"
	"time"
)

// SSOGrant is one row of sso_grants (REQ-DB-054, Sequence K): an authorization
// code CLARA issued to a registered client, and — once the client redeemed it —
// the grant that keeps the client's session tied to the CLARA session it came
// from. Every random value is held only as its SHA-256 hex (REQ-AUTH-036).
type SSOGrant struct {
	ID            int64
	ClientID      string
	UserID        int64
	SIDHash       string
	CodeHash      string
	CodeChallenge string
	RedirectURI   string
	SessionEpoch  int64
	CodeExpiresAt string
	GrantHash     sql.NullString
	RedeemedAt    sql.NullString
	ExpiresAt     string
	EndedAt       sql.NullString
	CreatedAt     string
}

const ssoGrantColumns = `id, client_id, user_id, sid_hash, code_hash, code_challenge, redirect_uri,
	session_epoch, code_expires_at, grant_hash, redeemed_at, expires_at, ended_at, created_at`

// ssoGrantRetention is how long a row stays after it can no longer be used, so a
// late check still finds it and answers "ended" or "expired" rather than "unknown".
const ssoGrantRetention = 24 * time.Hour

func scanSSOGrant(row interface{ Scan(dest ...any) error }) (*SSOGrant, error) {
	var (
		g                                        SSOGrant
		codeExpires, redeemed, expires, ended, c any
	)
	if err := row.Scan(&g.ID, &g.ClientID, &g.UserID, &g.SIDHash, &g.CodeHash, &g.CodeChallenge,
		&g.RedirectURI, &g.SessionEpoch, &codeExpires, &g.GrantHash, &redeemed, &expires,
		&ended, &c); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if v, ok := datetimeString(codeExpires); ok {
		g.CodeExpiresAt = v
	}
	if v, ok := datetimeString(redeemed); ok {
		g.RedeemedAt = sql.NullString{String: v, Valid: true}
	}
	if v, ok := datetimeString(expires); ok {
		g.ExpiresAt = v
	}
	if v, ok := datetimeString(ended); ok {
		g.EndedAt = sql.NullString{String: v, Valid: true}
	}
	if v, ok := datetimeString(c); ok {
		g.CreatedAt = v
	}
	return &g, nil
}

// InsertSSOCode stores a freshly issued authorization code (REQ-API-182) and
// removes the rows that can no longer be used by anyone: codes past their
// expiry that nobody redeemed, and grants past their end by more than a day.
func (s *Store) InsertSSOCode(ctx context.Context, g *SSOGrant, now time.Time) (int64, error) {
	cutoff := now.Add(-ssoGrantRetention).UTC().Format(datetimeLayout)
	nowStr := now.UTC().Format(datetimeLayout)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sso_grants
		  WHERE (redeemed_at IS NULL AND code_expires_at < ?) OR expires_at < ?`,
		cutoff, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO sso_grants (client_id, user_id, sid_hash, code_hash, code_challenge,
			redirect_uri, session_epoch, code_expires_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ClientID, g.UserID, g.SIDHash, g.CodeHash, g.CodeChallenge, g.RedirectURI,
		g.SessionEpoch, g.CodeExpiresAt, g.ExpiresAt, nowStr)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// GetSSOGrantByCode resolves a presented code by its hash; nil when none matches.
// Whether it may still be redeemed is the caller's decision.
func (s *Store) GetSSOGrantByCode(ctx context.Context, codeHash string) (*SSOGrant, error) {
	return scanSSOGrant(s.DB.QueryRowContext(ctx,
		`SELECT `+ssoGrantColumns+` FROM sso_grants WHERE code_hash = ?`, codeHash))
}

// GetSSOGrantByHandle resolves a client's grant handle by its hash; nil when none
// matches.
func (s *Store) GetSSOGrantByHandle(ctx context.Context, grantHash string) (*SSOGrant, error) {
	return scanSSOGrant(s.DB.QueryRowContext(ctx,
		`SELECT `+ssoGrantColumns+` FROM sso_grants WHERE grant_hash = ?`, grantHash))
}

// RedeemSSOCode turns an unredeemed, unexpired, unended code into a grant
// holding grantHash. It reports false when another redemption won the race, the
// code expired meanwhile, or it was ended: a code is redeemed at most once
// (REQ-AUTH-093).
func (s *Store) RedeemSSOCode(ctx context.Context, id int64, grantHash string, now time.Time) (bool, error) {
	nowStr := now.UTC().Format(datetimeLayout)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sso_grants SET grant_hash = ?, redeemed_at = ?
		  WHERE id = ? AND redeemed_at IS NULL AND ended_at IS NULL AND code_expires_at >= ?`,
		grantHash, nowStr, id, nowStr)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// EndSSOGrant ends one row — used when a redemption is refused, so a code that
// failed a check cannot be tried again with a better guess.
func (s *Store) EndSSOGrant(ctx context.Context, id int64, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sso_grants SET ended_at = ? WHERE id = ? AND ended_at IS NULL`,
		now.UTC().Format(datetimeLayout), id)
	return err
}

// EndSSOGrantsForSession ends every live row issued for one CLARA session of one
// account (REQ-AUTH-094) and returns the client ids whose grants ended, in order
// and without repeats. Codes not yet redeemed end with them.
func (s *Store) EndSSOGrantsForSession(ctx context.Context, userID int64, sidHash string, now time.Time) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT client_id FROM sso_grants
		  WHERE user_id = ? AND sid_hash = ? AND ended_at IS NULL ORDER BY client_id`,
		userID, sidHash)
	if err != nil {
		return nil, err
	}
	var clients []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		clients = append(clients, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sso_grants SET ended_at = ? WHERE user_id = ? AND sid_hash = ? AND ended_at IS NULL`,
		now.UTC().Format(datetimeLayout), userID, sidHash); err != nil {
		return nil, err
	}
	return clients, tx.Commit()
}

// GetUserByEmailFold finds the account whose address equals email ignoring case,
// for a directory sign-in whose `mail` attribute is spelled differently from the
// stored address (REQ-AUTH-091). It returns how many accounts matched, capped at
// 2: two addresses differing only in case are ambiguous, and the caller then
// neither picks one nor creates a third.
func (s *Store) GetUserByEmailFold(ctx context.Context, email string) (*User, int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE LOWER(email) = LOWER(?) ORDER BY id LIMIT 2`, email)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var (
		match *User
		n     int
	)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		n++
		if match == nil {
			match = &u
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if n != 1 {
		return nil, n, nil
	}
	return match, 1, nil
}
