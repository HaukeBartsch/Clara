package db

import (
	"context"
	"database/sql"
	"time"
)

// ProviderLink binds one provider subject to one account (REQ-DB-… / REQ-AUTH-080,
// DEV-AUTH-18, migration 0018). It is what lets a verified assertion resolve an
// account without trusting the email address that travels beside it: an address
// edited or reassigned at the identity provider then cannot move an account into
// different hands. The pair (issuer, subject) is the key; `subject` is stored and
// compared verbatim and is never parsed, since a pairwise identifier is opaque.
type ProviderLink struct {
	Issuer    string
	Subject   string
	UserID    int64
	CreatedAt string
}

const providerLinkColumns = `issuer, subject, user_id, created_at`

func scanProviderLink(row interface{ Scan(dest ...any) error }) (ProviderLink, error) {
	var (
		l         ProviderLink
		createdAt any
	)
	err := row.Scan(&l.Issuer, &l.Subject, &l.UserID, &createdAt)
	if err != nil {
		return l, err
	}
	if v, ok := dateString(createdAt); ok {
		l.CreatedAt = v
	}
	return l, nil
}

// GetProviderLinkBySubject returns the account a provider subject is bound to, or
// nil when no link exists — the first-verified-login case, where the caller links
// rather than refusing (Sequence J step 8).
func (s *Store) GetProviderLinkBySubject(ctx context.Context, issuer, subject string) (*ProviderLink, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+providerLinkColumns+`
		FROM provider_subject_links WHERE issuer = ? AND subject = ?`, issuer, subject)
	l, err := scanProviderLink(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// ListProviderLinksByUser answers the reverse question an administrator asks when a
// login is refused as a conflict: what else is this account bound to (REQ-AUTH-080
// leaves the resolution of a conflict to a person, so the row set has to be
// readable). Ordered by issuer so the list is stable across calls.
func (s *Store) ListProviderLinksByUser(ctx context.Context, userID int64) ([]ProviderLink, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+providerLinkColumns+`
		FROM provider_subject_links WHERE user_id = ? ORDER BY issuer, subject`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderLink
	for rows.Next() {
		l, err := scanProviderLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// EnsureProviderLink records the binding written at a successful verified provider
// login. It is idempotent by design: two concurrent logins of the same person race
// to write the same pair, and the loser's row already says what it should — an
// error there would fail a login that had already been satisfied. The first writer
// keeps its created_at, which names when the binding was made.
func (s *Store) EnsureProviderLink(ctx context.Context, issuer, subject string, userID int64, now time.Time) error {
	ts := now.UTC().Format("2006-01-02 15:04:05")
	if s.Dialect == DialectMariaDB {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO provider_subject_links (issuer, subject, user_id, created_at)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE created_at = created_at`, issuer, subject, userID, ts)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO provider_subject_links (issuer, subject, user_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (issuer, subject) DO NOTHING`, issuer, subject, userID, ts)
	return err
}

// DeleteProviderLinksByUser removes every binding of an account. A deleted or
// merged account must not keep a subject bound to it: the next login of that
// person would resolve against a row no one can see (REQ-AUTH-080).
func (s *Store) DeleteProviderLinksByUser(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM provider_subject_links WHERE user_id = ?`, userID)
	return err
}
