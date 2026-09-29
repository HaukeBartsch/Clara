package db

import (
	"context"
	"database/sql"
)

// PasswordToken mirrors one password_tokens row (REQ-DB-039, GD-22/GD-23):
// a single-use invite or reset token of Sequence H. Only the SHA-256 hex of
// the emailed value is stored; the plaintext exists solely inside the email
// and the completion request and never reaches this table, a log, or an
// audit detail (REQ-AUTH-060/062).
type PasswordToken struct {
	UserID     int64
	TokenHash  string
	Purpose    string // invite | reset
	CreatedBy  sql.NullInt64
	CreatedAt  string
	ExpiresAt  string
	ConsumedAt sql.NullString
}

// IssuePasswordToken stores a freshly hashed setup token. A new request of
// the same purpose replaces the account's outstanding one, so an old invite
// or reset link dies the moment a newer one is sent (REQ-AUTH-060/062).
func (s *Store) IssuePasswordToken(ctx context.Context, userID int64, tokenHash, purpose string, createdBy sql.NullInt64, expiresAt string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_tokens WHERE user_id = ? AND purpose = ? AND consumed_at IS NULL`,
		userID, purpose); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO password_tokens (user_id, token_hash, purpose, created_by, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		userID, tokenHash, purpose, nullInt64(createdBy), nowUTC(), expiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

// GetPasswordTokenByHash resolves a presented token by its hash; nil when no
// row matches. Validity (purpose, expiry, single use) is the caller's check —
// every failure answers the same generic error there (REQ-API-120).
func (s *Store) GetPasswordTokenByHash(ctx context.Context, tokenHash string) (*PasswordToken, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT user_id, token_hash, purpose, created_by, created_at, expires_at, consumed_at
		   FROM password_tokens WHERE token_hash = ?`, tokenHash)
	var (
		t         PasswordToken
		created   any
		expires   any
		consumed  any
		createdBy sql.NullInt64
	)
	if err := row.Scan(&t.UserID, &t.TokenHash, &t.Purpose, &createdBy,
		&created, &expires, &consumed); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t.CreatedBy = createdBy
	if v, ok := datetimeString(created); ok {
		t.CreatedAt = v
	}
	if v, ok := datetimeString(expires); ok {
		t.ExpiresAt = v
	}
	if v, ok := datetimeString(consumed); ok {
		t.ConsumedAt = sql.NullString{String: v, Valid: true}
	}
	return &t, nil
}

// ConsumePasswordTokenTx marks the token spent inside the completion
// transaction. The guarded UPDATE makes redemption single use even against a
// concurrent second attempt: only one caller sees rowsAffected = 1.
func (s *Store) ConsumePasswordTokenTx(ctx context.Context, tx *sql.Tx, tokenHash, now string) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE password_tokens SET consumed_at = ?
		 WHERE token_hash = ? AND consumed_at IS NULL`, now, tokenHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// InvalidatePasswordTokensTx removes every outstanding (unconsumed) token of
// the account — used on reset completion so a fresh credential kills all
// older links at once (REQ-AUTH-062). Consumed rows stay as history.
func (s *Store) InvalidatePasswordTokensTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM password_tokens WHERE user_id = ? AND consumed_at IS NULL`, userID)
	return err
}
