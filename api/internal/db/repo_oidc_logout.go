package db

import (
	"context"
	"database/sql"
	"time"
)

// The writes of Sequence L2 (Authentication_Authorization_Design.md §2.14, REQ-AUTH-099):
// a provider's back-channel logout token, once verified, is applied in ONE transaction —
// its jti recorded, the account's session epoch raised, its client grants ended and the
// audit entry written — so a failure anywhere leaves nothing behind and the provider's
// retry of the same token is accepted rather than refused as a replay. Each function here
// therefore takes the caller's transaction instead of opening its own.

// RecordLogoutJTI enters one accepted logout token in the replay window (REQ-DB-057,
// oidc_logout_jtis). It first deletes the rows whose expires_at has passed — the window
// rolls on insert and no job runs — and then inserts keyHash. It reports false when the key
// is already there: the same (issuer, jti) was accepted before, which is a replay.
//
// The conflict is detected by the insert itself (a dialect "ignore" insert whose affected
// row count is 0), not by a read first, so two concurrent deliveries of one token cannot
// both pass. On SQLite the transaction began IMMEDIATE (the DSN's _txlock), on MariaDB the
// primary key decides.
func (s *Store) RecordLogoutJTI(ctx context.Context, tx *sql.Tx, keyHash string, expiresAt, now time.Time) (bool, error) {
	nowStr := now.UTC().Format(datetimeLayout)
	if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_logout_jtis WHERE expires_at < ?`, nowStr); err != nil {
		return false, err
	}
	insert := `INSERT INTO oidc_logout_jtis (key_hash, expires_at, created_at) VALUES (?, ?, ?)
		ON CONFLICT (key_hash) DO NOTHING`
	if s.Dialect == DialectMariaDB {
		insert = `INSERT IGNORE INTO oidc_logout_jtis (key_hash, expires_at, created_at) VALUES (?, ?, ?)`
	}
	res, err := tx.ExecContext(ctx, insert, keyHash, expiresAt.UTC().Format(datetimeLayout), nowStr)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ProviderLinkUserTx resolves (issuer, subject) to the account it is bound to inside the
// caller's transaction (REQ-AUTH-099: the subject link only — the address is never
// consulted). It returns 0 when no link exists.
func ProviderLinkUserTx(ctx context.Context, tx *sql.Tx, issuer, subject string) (int64, error) {
	var userID int64
	err := tx.QueryRowContext(ctx,
		`SELECT user_id FROM provider_subject_links WHERE issuer = ? AND subject = ?`,
		issuer, subject).Scan(&userID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return userID, err
}

// EndSSOGrantsForUserTx ends every live row of sso_grants the account holds — codes not
// yet redeemed and grants, whatever CLARA session issued them (REQ-AUTH-099) — and returns
// the distinct client ids whose rows ended, in order. Unlike EndSSOGrantsForSession it is
// not bound to one sign-in id: the provider says the person signed out, not which browser.
func EndSSOGrantsForUserTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT client_id FROM sso_grants
		  WHERE user_id = ? AND ended_at IS NULL ORDER BY client_id`, userID)
	if err != nil {
		return nil, err
	}
	clients := []string{}
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
		`UPDATE sso_grants SET ended_at = ? WHERE user_id = ? AND ended_at IS NULL`,
		now.UTC().Format(datetimeLayout), userID); err != nil {
		return nil, err
	}
	return clients, nil
}
