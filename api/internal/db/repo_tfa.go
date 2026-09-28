package db

import (
	"context"
	"database/sql"
)

// UserTFA mirrors one user_two_factor row (REQ-DB-038, DEV-DB-12). The
// second factor guards local and LDAP logins (GD-21); absence of a row means
// "off". Challenge state lives entirely here so the login endpoint stays
// stateless (DEV-API-15). Secrets and code hashes must never be logged.
type UserTFA struct {
	UserID             int64
	Method             string // off | totp | email
	TotpSecret         sql.NullString
	TotpLastStep       sql.NullInt64
	EmailCodeHash      sql.NullString
	EmailCodeExpiresAt sql.NullString
	RecoveryCodes      string // JSON [{"h":…,"used":bool}]; "" means none
	EnrolledAt         sql.NullString
}

// GetTFA returns the user's two-factor record, or nil when none exists
// (method "off").
func (s *Store) GetTFA(ctx context.Context, userID int64) (*UserTFA, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT user_id, method, totp_secret, totp_last_step, email_code_hash,
		        email_code_expires_at, recovery_codes, enrolled_at
		   FROM user_two_factor WHERE user_id = ?`, userID)
	var t UserTFA
	err := row.Scan(&t.UserID, &t.Method, &t.TotpSecret, &t.TotpLastStep,
		&t.EmailCodeHash, &t.EmailCodeExpiresAt, &t.RecoveryCodes, &t.EnrolledAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ensureTFARow inserts the sparse "off" row when absent so later updates have
// a target. Select-then-insert keeps the statement common to both dialects.
func (s *Store) ensureTFARow(ctx context.Context, userID int64) error {
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM user_two_factor WHERE user_id = ?`, userID).Scan(&one)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO user_two_factor (user_id, method, recovery_codes) VALUES (?, 'off', '[]')`, userID)
	return err
}

// SetTFAPendingSecret stores a TOTP secret awaiting confirmation: the method
// stays "off" until ActivateTFA proves possession of the code generator. A
// re-enrollment replaces any previous pending secret and its watermark.
func (s *Store) SetTFAPendingSecret(ctx context.Context, userID int64, secret string) error {
	if err := s.ensureTFARow(ctx, userID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor SET totp_secret = ?, totp_last_step = NULL WHERE user_id = ?`,
		secret, userID)
	return err
}

// ActivateTFA switches a confirmed enrollment on: the method becomes "totp"
// or "email", the other method's state is cleared, and the freshly issued
// recovery-code hashes replace any previous set (REQ-AUTH-059).
func (s *Store) ActivateTFA(ctx context.Context, userID int64, method, recoveryJSON string) error {
	if err := s.ensureTFARow(ctx, userID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor
		    SET method = ?, enrolled_at = ?, recovery_codes = ?,
		        email_code_hash = NULL, email_code_expires_at = NULL
		  WHERE user_id = ?`,
		method, nowUTC(), recoveryJSON, userID)
	return err
}

// SetTFAEmailCode stores the pending email OTP as a one-way hash with its
// expiry; the delivered code itself is never persisted (REQ-AUTH-057). It
// updates in place, so each new send replaces the previous code.
func (s *Store) SetTFAEmailCode(ctx context.Context, userID int64, hash, expiresAt string) error {
	if err := s.ensureTFARow(ctx, userID); err != nil { // enrollment may not have a row yet
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor SET email_code_hash = ?, email_code_expires_at = ? WHERE user_id = ?`,
		hash, expiresAt, userID)
	return err
}

// ConsumeTFAEmailCode atomically verifies and invalidates the pending email
// code: it clears the row only when the hash matches and the expiry has not
// passed, so a code is single-use even under concurrent attempts. Returns
// whether the code was accepted.
func (s *Store) ConsumeTFAEmailCode(ctx context.Context, userID int64, hash string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor
		    SET email_code_hash = NULL, email_code_expires_at = NULL
		  WHERE user_id = ? AND email_code_hash = ? AND email_code_expires_at > ?`,
		userID, hash, nowUTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetTFALastStep records the newest accepted TOTP step so a code cannot be
// replayed within its validity window.
func (s *Store) SetTFALastStep(ctx context.Context, userID int64, step int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor SET totp_last_step = ? WHERE user_id = ?`, step, userID)
	return err
}

// SetTFARecoveryCodes persists the updated recovery-code hashes, e.g. after
// one was consumed.
func (s *Store) SetTFARecoveryCodes(ctx context.Context, userID int64, recoveryJSON string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_two_factor SET recovery_codes = ? WHERE user_id = ?`, recoveryJSON, userID)
	return err
}

// ResetTFA removes the record entirely — self-service disable and admin reset
// both fall back to "off" (REQ-AUTH-056).
func (s *Store) ResetTFA(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM user_two_factor WHERE user_id = ?`, userID)
	return err
}

// ListTFAMethods returns the enabled method of every user, keyed by user id.
// Users without an enabled method are absent; callers render them as "off"
// (REQ-API-116).
func (s *Store) ListTFAMethods(ctx context.Context) (map[int64]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT user_id, method FROM user_two_factor WHERE method <> 'off'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var m string
		if err := rows.Scan(&id, &m); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}
