package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerUsers mounts §4.4: the user listing, creation (with re-enable),
// and update. All three require is_admin; a non-admin call is 403.

func (h *Handler) registerUsers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/users", h.listUsers)
	mux.HandleFunc("POST /api/v1/users", h.createUser)
	mux.HandleFunc("PUT /api/v1/users/{id}", h.updateUser)
}

// listUsers returns every account as the full §4.3 user object, derived
// status included (REQ-API-046, GD-19).
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	tfaMethods, err := h.Store.ListTFAMethods(r.Context()) // one batched lookup (REQ-API-116)
	if err != nil {
		errInternal(w)
		return
	}
	now := time.Now().UTC()
	out := make([]UserObject, 0, len(users))
	for i := range users {
		obj := NewUserObject(&users[i], now)
		if m, ok := tfaMethods[users[i].ID]; ok {
			obj.TFAMethod = m
		}
		out = append(out, obj)
	}
	writeJSON(w, http.StatusOK, out)
}

// validUntilFromDays maps a valid_days value to the stored DATE: 0 (or an
// absent value handled by the caller) means indefinite — NULL (REQ-AUTH-052).
func validUntilFromDays(days int, now time.Time) sql.NullString {
	if days <= 0 {
		return sql.NullString{}
	}
	return sql.NullString{
		String: now.UTC().AddDate(0, 0, days).Format("2006-01-02"), Valid: true,
	}
}

// createUser creates an account, or re-enables a disabled one with the same
// email (REQ-API-047). A live account with the email is a conflict. The
// password is optional and stored only as a bcrypt hash (REQ-AUTH-050); it
// never enters the audit trail (REQ-AUTH-036).
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "email", "display_name", "valid_days", "password") {
		return
	}
	var body struct {
		Email       *string `json:"email"`
		DisplayName *string `json:"display_name"`
		ValidDays   *int    `json:"valid_days"`
		Password    *string `json:"password"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.Email == nil || *body.Email == "" {
		errBadRequest(w, "email is required")
		return
	}
	if body.ValidDays != nil && *body.ValidDays < 0 {
		errBadRequest(w, "valid_days must be >= 0 (0 = indefinite)")
		return
	}
	email := *body.Email
	displayName := ""
	if body.DisplayName != nil {
		displayName = *body.DisplayName
	}
	now := time.Now().UTC()
	validUntil := validUntilFromDays(derefInt(body.ValidDays), now)

	existing, err := h.Store.GetUserByEmail(ctx, email)
	if err != nil {
		errInternal(w)
		return
	}
	if existing != nil && existing.Enabled {
		errConflict(w, "an enabled account with this email already exists")
		return
	}

	var hash sql.NullString
	if body.Password != nil && *body.Password != "" {
		bh, err := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		if err != nil {
			errInternal(w)
			return
		}
		hash = sql.NullString{String: string(bh), Valid: true}
	}

	reEnabled := existing != nil // a disabled account with this email
	if reEnabled {
		// Re-enabling resets the inactivity clock (REQ-AUTH-053).
		if _, err := h.Store.DB.ExecContext(ctx,
			`UPDATE users SET enabled = 1, last_login_at = NULL, display_name = ?,
				valid_until = ? WHERE id = ?`,
			displayName, nullStr(validUntil), existing.ID); err != nil {
			errInternal(w)
			return
		}
		if hash.Valid {
			if err := h.Store.SetUserPasswordHash(ctx, existing.ID, hash); err != nil {
				errInternal(w)
				return
			}
		}
		existing.Enabled = true
		existing.DisplayName = displayName
		existing.LastLoginAt = sql.NullString{}
		existing.ValidUntil = validUntil
		if hash.Valid {
			existing.PasswordHash = hash
		}
	} else {
		nu := &db.User{Email: email, DisplayName: displayName, Enabled: true,
			PasswordHash: hash, ValidUntil: validUntil, UILanguage: "en"}
		id, err := h.Store.CreateUser(ctx, nu)
		if err != nil {
			errInternal(w)
			return
		}
		nu.ID = id
		existing = nu
	}

	validJSON := any(nil)
	if validUntil.Valid {
		validJSON = validUntil.String
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.UserCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email,
		Details: map[string]any{
			"email": email, "display_name": displayName,
			"re_enabled": reEnabled, "valid_until": validJSON,
		},
	}); err != nil {
		errInternal(w)
		return
	}
	if reEnabled {
		writeJSON(w, http.StatusOK, h.userObject(ctx, existing, now))
		return
	}
	writeJSON(w, http.StatusCreated, h.userObject(ctx, existing, now))
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// updateUser applies any subset of {enabled, valid_days, password}
// (REQ-API-048). enabled is authoritative and idempotent; re-enabling resets
// the inactivity clock; an empty password clears the stored hash. The audit
// entry records the changed attributes, never the password itself.
func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	actorUser, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	userID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid user id")
		return
	}
	ctx := r.Context()
	target, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		errInternal(w)
		return
	}
	if target == nil {
		errNotFound(w)
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "enabled", "valid_days", "password") {
		return
	}

	now := time.Now().UTC()
	next := *target
	changes := map[string]any{"email": target.Email}
	writesEnabled := false
	writesValidUntil := false
	var writesPassword sql.NullString
	writePw := false

	if raw, present := supplied["enabled"]; present {
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			errBadRequest(w, "enabled must be a boolean")
			return
		}
		if v != target.Enabled {
			changes["enabled"] = map[string]any{"old": boolToIntJSON(target.Enabled), "new": boolToIntJSON(v)}
			next.Enabled = v
			writesEnabled = true
			if v {
				// Re-enabling resets the inactivity clock (REQ-AUTH-053).
				next.LastLoginAt = sql.NullString{}
			}
		}
	}
	if raw, present := supplied["valid_days"]; present {
		var days int
		if err := json.Unmarshal(raw, &days); err != nil || days < 0 {
			errBadRequest(w, "valid_days must be an integer >= 0")
			return
		}
		vu := validUntilFromDays(days, now)
		if vu != target.ValidUntil {
			oldJSON, newJSON := any(nil), any(nil)
			if target.ValidUntil.Valid {
				oldJSON = target.ValidUntil.String
			}
			if vu.Valid {
				newJSON = vu.String
			}
			changes["valid_until"] = map[string]any{"old": oldJSON, "new": newJSON}
			next.ValidUntil = vu
			writesValidUntil = true
		}
	}
	if raw, present := supplied["password"]; present {
		var pw string
		if err := json.Unmarshal(raw, &pw); err != nil {
			errBadRequest(w, "password must be a string")
			return
		}
		if pw == "" {
			writePw = true // clears the hash
			writesPassword = sql.NullString{}
		} else {
			bh, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
			if err != nil {
				errInternal(w)
				return
			}
			writePw = true
			writesPassword = sql.NullString{String: string(bh), Valid: true}
		}
		changes["password_changed"] = true // never the value (REQ-AUTH-036)
	}

	if len(changes) > 1 { // more than just "email"
		tx, err := h.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			errInternal(w)
			return
		}
		defer tx.Rollback()
		if writesEnabled || writesValidUntil {
			lastLogin := any(nil)
			if next.LastLoginAt.Valid {
				lastLogin = next.LastLoginAt.String
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET enabled = ?, valid_until = ?, last_login_at = ? WHERE id = ?`,
				boolToIntParam(next.Enabled), nullStr(next.ValidUntil), lastLogin, target.ID); err != nil {
				errInternal(w)
				return
			}
		}
		if writePw {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET password_hash = ? WHERE id = ?`,
				nullStr(writesPassword), target.ID); err != nil {
				errInternal(w)
				return
			}
		}
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.UserUpdated, Source: audit.SourceUI,
			UserID: actorUser.ID, Email: actorUser.Email,
			Details: changes,
		}); err != nil {
			errInternal(w)
			return
		}
		if err := tx.Commit(); err != nil {
			errInternal(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, h.userObject(ctx, &next, now))
}
