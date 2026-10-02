package db

import (
	"context"
	"database/sql"
)

// i18n string writes (REQ-DB-031, GD-12) in the transactional form §4.19 needs:
// one PUT upserts a batch and every key it actually changed is audit-logged
// (REQ-API-100), so the apply step has to report what moved rather than just
// succeeding.

// I18nEntry is one requested translation of the §4.19 PUT body. An empty Text
// is not "translate to nothing" — it removes the translation, and the key falls
// back to English (REQ-DB-031).
type I18nEntry struct {
	Key  string
	Text string
}

// I18nChange is one entry that actually changed something: "set" carries the
// old text ("" when the key was new), "removed" only fires when a row went.
type I18nChange struct {
	Key    string
	Action string // "set" | "removed"
	Old    string
}

// Action values of the i18n_updated audit payload (Audit_Logging_Design.md §3.4).
const (
	I18nActionSet     = "set"
	I18nActionRemoved = "removed"
)

// SetI18nStringsTx applies a batch of translations for one language inside tx
// and returns the changes it made, in request order. An entry whose text
// already matches writes nothing and is not reported, so a repeated PUT is
// idempotent and produces no audit noise (REQ-API-042).
func (s *Store) SetI18nStringsTx(ctx context.Context, tx *sql.Tx, languageID int64,
	entries []I18nEntry) ([]I18nChange, error) {
	var changes []I18nChange
	for _, e := range entries {
		var current string
		err := tx.QueryRowContext(ctx,
			"SELECT text FROM i18n_strings WHERE language_id = ? AND `key` = ?",
			languageID, e.Key).Scan(&current)
		switch {
		case err == sql.ErrNoRows:
			current = ""
		case err != nil:
			return nil, err
		}

		if e.Text == "" {
			if current == "" {
				continue // nothing to remove — the key was never translated
			}
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM i18n_strings WHERE language_id = ? AND `key` = ?",
				languageID, e.Key); err != nil {
				return nil, err
			}
			changes = append(changes, I18nChange{Key: e.Key, Action: I18nActionRemoved, Old: current})
			continue
		}
		if current == e.Text {
			continue // unchanged
		}
		if s.Dialect == DialectMariaDB {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO i18n_strings (language_id, `key`, text) VALUES (?, ?, ?)"+
					" ON DUPLICATE KEY UPDATE text = VALUES(text)",
				languageID, e.Key, e.Text); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO i18n_strings (language_id, `key`, text) VALUES (?, ?, ?)"+
					" ON CONFLICT (language_id, `key`) DO UPDATE SET text = excluded.text",
				languageID, e.Key, e.Text); err != nil {
				return nil, err
			}
		}
		changes = append(changes, I18nChange{Key: e.Key, Action: I18nActionSet, Old: current})
	}
	return changes, nil
}

// ListI18nStringsByCode returns one language's translations keyed by string key
// — the shape §4.19's listing and missing-key computation reads.
func (s *Store) ListI18nStringsByCode(ctx context.Context, code string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT i.`key`, i.text FROM i18n_strings i"+
			" JOIN languages l ON l.id = i.language_id"+
			" WHERE l.code = ? ORDER BY i.`key`", code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, text string
		if err := rows.Scan(&key, &text); err != nil {
			return nil, err
		}
		out[key] = text
	}
	return out, rows.Err()
}
