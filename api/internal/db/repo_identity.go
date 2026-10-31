package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// --- users (REQ-DB-008) ---

const userColumns = `id, email, display_name, enabled, auth_source, password_hash,
	valid_until, last_login_at, is_admin, ui_language, ui_theme, session_epoch,
	preferred_region, automatic_region, automatic_region_source, created_at`

func scanUser(row interface{ Scan(dest ...any) error }) (User, error) {
	var (
		u          User
		enabled    int
		isAdmin    int
		validUntil any
		lastLogin  any
	)
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &enabled, &u.AuthSource,
		&u.PasswordHash, &validUntil, &lastLogin, &isAdmin, &u.UILanguage, &u.UITheme, &u.SessionEpoch,
		&u.PreferredRegion, &u.AutomaticRegion, &u.AutomaticRegionSource, &u.CreatedAt)
	if err != nil {
		return u, err
	}
	u.Enabled = enabled != 0
	u.IsAdmin = isAdmin != 0
	if v, ok := dateString(validUntil); ok {
		u.ValidUntil = sql.NullString{String: v, Valid: true}
	}
	if v, ok := datetimeString(lastLogin); ok {
		u.LastLoginAt = sql.NullString{String: v, Valid: true}
	}
	return u, nil
}

// CreateUser inserts a user row and returns the new id (REQ-API-047).
// CreateUserTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) CreateUserTx(ctx context.Context, tx *sql.Tx, u *User) (int64, error) {
	u.AuthSource = normalizeAuthSource(u.AuthSource)
	if u.UILanguage == "" {
		u.UILanguage = "en"
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (email, display_name, enabled, auth_source, password_hash,
			valid_until, last_login_at, is_admin, ui_language, ui_theme, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Email, u.DisplayName, boolToInt(u.Enabled), u.AuthSource, nullStr(u.PasswordHash),
		nullStr(u.ValidUntil), nullStr(u.LastLoginAt), boolToInt(u.IsAdmin), u.UILanguage,
		nullStr(u.UITheme), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CreateUser inserts a user row and returns the new id (REQ-API-047).
func (s *Store) CreateUser(ctx context.Context, u *User) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.CreateUserTx(ctx, tx, u)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertBootstrap ensures the bootstrap admin row exists, is enabled, and is
// an administrator (REQ-AUTH-007, GD-4). Idempotent; returns the user.
func (s *Store) UpsertBootstrap(ctx context.Context, email, displayName, passwordHash string) (*User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.DisplayName, new(int), &u.AuthSource, &u.PasswordHash,
			new(any), new(any), new(int), &u.UILanguage, &u.UITheme, &u.SessionEpoch,
			&u.PreferredRegion, &u.AutomaticRegion, &u.AutomaticRegionSource, &u.CreatedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows {
		u = User{Email: email, DisplayName: displayName, Enabled: true, IsAdmin: true, AuthSource: "local", UILanguage: "en"}
		u.PasswordHash = sql.NullString{String: passwordHash, Valid: true}
		id, err := s.CreateUser(ctx, &u)
		if err != nil {
			return nil, err
		}
		u.ID = id
		return &u, nil
	}
	// Row exists: ensure enabled + admin.
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 1, is_admin = 1 WHERE id = ?`, u.ID); err != nil {
		return nil, err
	}
	u.Enabled, u.IsAdmin = true, true
	return &u, nil
}

// CountEnabledAdmins counts the accounts that are both enabled and
// administrators (REQ-AUTH-068).
func (s *Store) CountEnabledAdmins(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE enabled = 1 AND is_admin = 1`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// EnsureBootstrapForLogin applies the narrowed login-time bootstrap rule
// (REQ-AUTH-007, DEV-AUTH-15 — security finding F10): an email match creates
// the row when absent (the first-installation setup case, an enabled
// administrator with no local password); it never re-enables a disabled
// account, and it sets is_admin = 1 on an existing enabled row only when
// promoteAdmin — the caller passes true for a local login or when no other
// enabled administrator exists. The startup provisioning stays in
// UpsertBootstrap (the operator's setup action). Returns the current row.
func (s *Store) EnsureBootstrapForLogin(ctx context.Context, email, displayName string, promoteAdmin bool) (*User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ?`, email))
	if err == sql.ErrNoRows {
		nu := User{Email: email, DisplayName: displayName, Enabled: true, IsAdmin: true,
			AuthSource: "local", UILanguage: "en"}
		id, err := s.CreateUser(ctx, &nu)
		if err != nil {
			return nil, err
		}
		nu.ID = id
		return &nu, nil
	}
	if err != nil {
		return nil, err
	}
	// A disabled account stays disabled — a deliberate disable must stick even
	// against the bootstrap address (REQ-AUTH-007). Promotion of an existing
	// enabled row follows promoteAdmin only.
	if u.Enabled && !u.IsAdmin && promoteAdmin {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE users SET is_admin = 1 WHERE id = ?`, u.ID); err != nil {
			return nil, err
		}
		u.IsAdmin = true
	}
	return &u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email)
	u, err := scanUser(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// likeEscape quotes the LIKE wildcards out of a user-supplied pattern. The
// escape character is "!" rather than the backslash because the two dialects
// disagree about it: MariaDB treats a backslash as the default LIKE escape and
// also eats one inside a string literal, SQLite treats neither specially. An
// explicit ESCAPE '!' says the same thing in both.
func likeEscape(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

// localPartOf returns the part of an address before its first "@".
func localPartOf(email string) string {
	if i := strings.IndexByte(email, '@'); i >= 0 {
		return email[:i]
	}
	return email
}

// GetUserByLocalPart resolves the login-name shorthand the sign-in page accepts
// alongside an address (REQ-AUTH-087): the part before "@", for someone who
// knows their login name but not their domain.
//
// It returns nil both when no stored address has that local part and when more
// than one does. An ambiguous shorthand is not a hint about which accounts
// exist, so it resolves to nothing and the caller rejects it exactly as an
// unknown account (§2.6 indistinguishability). Two people sharing a login name
// across domains are then served by typing the whole address.
//
// The LIKE narrows on an index prefix; the comparison in Go is what actually
// decides, so a wildcard that survived quoting or an unusual stored value can
// only ever make this return nil, never the wrong account. LIKE and its match
// are case-insensitive for ASCII in both dialects, so the comparison is too —
// otherwise a mixed-case stored address would be found by the query and then
// discarded by it.
func (s *Store) GetUserByLocalPart(ctx context.Context, local string) (*User, error) {
	if local == "" {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email LIKE ? ESCAPE '!' ORDER BY email LIMIT 2`,
		likeEscape(local+"@")+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var match *User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(localPartOf(u.Email), local) {
			continue
		}
		if match != nil {
			return nil, nil // two addresses answer to this name: not resolvable
		}
		match = &u
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return match, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserEnabled sets the enabled flag and, when re-enabling, resets the
// inactivity clock (last_login_at = NULL) per REQ-AUTH-053. Disabling ends
// the account's sessions (REQ-AUTH-074), so a later re-enable cannot revive
// one that was open when the account was switched off.
func (s *Store) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	if enabled {
		_, err := s.DB.ExecContext(ctx,
			`UPDATE users SET enabled = 1, last_login_at = NULL WHERE id = ?`, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 0, session_epoch = session_epoch + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) SetUserValidUntil(ctx context.Context, id int64, validUntil sql.NullString) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET valid_until = ? WHERE id = ?`, nullStr(validUntil), id)
	return err
}

// SetUserPasswordHash stores (or, with an invalid hash, clears) the local
// credential. Every write of the credential ends the account's sessions
// (REQ-AUTH-074): whoever held one opened it under the old password.
// SetUserPasswordHashTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) SetUserPasswordHashTx(ctx context.Context, tx *sql.Tx, id int64, hash sql.NullString) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, session_epoch = session_epoch + 1 WHERE id = ?`, nullStr(hash), id)
	return err
}

// SetUserPasswordHash stores (or, with an invalid hash, clears) the local
// credential. Every write of the credential ends the account's sessions
// (REQ-AUTH-074): whoever held one opened it under the old password.
func (s *Store) SetUserPasswordHash(ctx context.Context, id int64, hash sql.NullString) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SetUserPasswordHashTx(ctx, tx, id, hash); err != nil {
		return err
	}
	return tx.Commit()
}

// BumpSessionEpoch raises the account's session epoch, which ends every PHP
// session of the account at its next administration call (REQ-AUTH-074). It
// runs on whatever executes it — the store or an open transaction — so a
// caller can commit it together with the change that warrants it.
func BumpSessionEpoch(ctx context.Context, x interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id int64) error {
	_, err := x.ExecContext(ctx, `UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, id)
	return err
}

// SessionEpoch reads the account's current session epoch — what a caller
// hands back after raising it on the acting user's own behalf, so the session
// that made the change can adopt the new value instead of ending with the rest.
func (s *Store) SessionEpoch(ctx context.Context, id int64) (int64, error) {
	var epoch int64
	err := s.DB.QueryRowContext(ctx, `SELECT session_epoch FROM users WHERE id = ?`, id).Scan(&epoch)
	return epoch, err
}

func (s *Store) SetUILanguage(ctx context.Context, id int64, lang string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET ui_language = ? WHERE id = ?`, lang, id)
	return err
}

// SetUITheme stores the personal theme override; an invalid NullString means
// no override — the account follows the installation default UI_THEME
// (REQ-API-122, REQ-DB-008, GD-26).
func (s *Store) SetUITheme(ctx context.Context, id int64, theme sql.NullString) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET ui_theme = ? WHERE id = ?`, nullStr(theme), id)
	return err
}

// SetPreferredRegionTx stores the user's own region choice; an invalid NullString clears
// it, so the automatic region applies again (REQ-API-175, REQ-DB-052).
func (s *Store) SetPreferredRegionTx(ctx context.Context, tx *sql.Tx, id int64, region sql.NullString) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET preferred_region = ? WHERE id = ?`, nullStr(region), id)
	return err
}

// SetProviderRegion records the region a sign-in derived from the provider's roles
// (REQ-AUTH-088). It replaces an earlier provider region and an assignment placeholder
// alike, and never touches the user's own choice.
func (s *Store) SetProviderRegion(ctx context.Context, id int64, region string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET automatic_region = ?, automatic_region_source = ? WHERE id = ?`,
		region, RegionSourceProvider, id)
	return err
}

// SetRegionPlaceholderTx gives an account without any region the assigning
// administrator's one (REQ-API-179). The update is conditional on both columns still
// being NULL, so a region derived at a concurrent sign-in, or chosen by the user in the
// meantime, is never overwritten; it reports whether the placeholder was set.
func (s *Store) SetRegionPlaceholderTx(ctx context.Context, tx *sql.Tx, id int64, region string) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET automatic_region = ?, automatic_region_source = ?
		 WHERE id = ? AND preferred_region IS NULL AND automatic_region IS NULL`,
		region, RegionSourceAssignment, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// AutoDisable marks a user disabled and clears the inactivity clock
// (REQ-AUTH-053), used by the account-active rule. Like every disable it ends
// the account's sessions (REQ-AUTH-074).
func (s *Store) AutoDisable(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 0, last_login_at = NULL, session_epoch = session_epoch + 1 WHERE id = ?`, id)
	return err
}

func (s *Store) TouchLastLogin(ctx context.Context, id int64, authSource string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET last_login_at = ?, auth_source = ? WHERE id = ?`, nowUTC(), normalizeAuthSource(authSource), id)
	return err
}

func normalizeAuthSource(s string) string {
	switch s {
	case "oauth2", "ldap", "local":
		return s
	default:
		return "local"
	}
}

// --- roles (REQ-DB-009/040) ---

// CreateRole inserts a role with its per-arm defaults and its per-pair grants
// in one transaction, so a role never exists with half of its permission set
// (REQ-AUTH-069).
// CreateRoleTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) CreateRoleTx(ctx context.Context, tx *sql.Tx, r *Role, arms []RoleArm, grants []RoleGrant) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO roles (project_id, role_name, project_admin) VALUES (?, ?, ?)`,
		r.ProjectID, r.RoleName, boolToInt(r.ProjectAdmin))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.ID = id
	if err := s.replaceRolePermissionsTx(ctx, tx, r, arms, grants); err != nil {
		return 0, err
	}
	return id, nil
}

// CreateRole inserts a role with its per-arm defaults and its per-pair grants
// in one transaction, so a role never exists with half of its permission set
// (REQ-AUTH-069).
func (s *Store) CreateRole(ctx context.Context, r *Role, arms []RoleArm, grants []RoleGrant) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.CreateRoleTx(ctx, tx, r, arms, grants)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateRole replaces a role's name, project_admin flag, arm defaults and pair
// grants in one transaction (REQ-API-143): the PUT of §4.7 is a whole-object
// replace, so a pair absent from grants reverts to its arm default by having
// its row deleted, not by being rewritten.
// UpdateRoleTx writes inside the caller's transaction, so a change and its audit entry commit
// together instead of the change committing first with the entry to follow
// (REQ-AUD-003, DEV-AUD-1).
func (s *Store) UpdateRoleTx(ctx context.Context, tx *sql.Tx, r *Role, arms []RoleArm, grants []RoleGrant) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE roles SET role_name = ?, project_admin = ? WHERE id = ?`,
		r.RoleName, boolToInt(r.ProjectAdmin), r.ID); err != nil {
		return err
	}
	if err := s.replaceRolePermissionsTx(ctx, tx, r, arms, grants); err != nil {
		return err
	}
	return nil
}

// UpdateRole replaces a role's name, project_admin flag, arm defaults and pair
// grants in one transaction (REQ-API-143): the PUT of §4.7 is a whole-object
// replace, so a pair absent from grants reverts to its arm default by having
// its row deleted, not by being rewritten.
func (s *Store) UpdateRole(ctx context.Context, r *Role, arms []RoleArm, grants []RoleGrant) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.UpdateRoleTx(ctx, tx, r, arms, grants); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceRolePermissionsTx rewrites both grant tables of the role from the
// supplied sets (complete after the caller's merge, REQ-AUTH-069). The cascades
// from roles/events/instruments also remove a pair's row when the design object
// goes away, so a re-created name inherits nothing (REQ-AUTH-069).
func (s *Store) replaceRolePermissionsTx(ctx context.Context, tx *sql.Tx, r *Role,
	arms []RoleArm, grants []RoleGrant) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_arms WHERE role_id = ?`, r.ID); err != nil {
		return err
	}
	for i := range arms {
		a := arms[i]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_arms (role_id, arm_num, data_access_level, export_level) VALUES (?, ?, ?, ?)`,
			r.ID, a.ArmNum, a.DataAccessLevel, a.ExportLevel); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_grants WHERE role_id = ?`, r.ID); err != nil {
		return err
	}
	for i := range grants {
		g := grants[i]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_grants (role_id, event_id, instrument_id, data_access_level,
			                         export_level, delete_values, edit_surveys)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.ID, g.EventID, g.InstrumentID, g.DataAccessLevel, g.ExportLevel,
			boolToInt(g.DeleteValues), boolToInt(g.EditSurveys)); err != nil {
			return err
		}
	}
	return nil
}

const roleColumns = `id, project_id, role_name, project_admin`

func scanRole(row interface{ Scan(dest ...any) error }) (Role, error) {
	var (
		r       Role
		isAdmin int
	)
	if err := row.Scan(&r.ID, &r.ProjectID, &r.RoleName, &isAdmin); err != nil {
		return r, err
	}
	r.ProjectAdmin = isAdmin != 0
	return r, nil
}

func (s *Store) GetRole(ctx context.Context, id int64) (*Role, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+roleColumns+` FROM roles WHERE id = ?`, id)
	r, err := scanRole(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// GetRoleByProjectName resolves a role within one project; roles are
// project-scoped (REQ-AUTH-024). nil when no such role exists.
func (s *Store) GetRoleByProjectName(ctx context.Context, projectID int64, name string) (*Role, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+roleColumns+` FROM roles WHERE project_id = ? AND role_name = ?`, projectID, name)
	r, err := scanRole(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListRoles(ctx context.Context, projectID int64) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+roleColumns+` FROM roles WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const roleArmColumns = `id, role_id, arm_num, data_access_level, export_level`

// ListRoleArms returns the per-arm defaults of a role in arm order (REQ-DB-009).
func (s *Store) ListRoleArms(ctx context.Context, roleID int64) ([]RoleArm, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+roleArmColumns+` FROM role_arms WHERE role_id = ? ORDER BY arm_num`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleArm
	for rows.Next() {
		var a RoleArm
		if err := rows.Scan(&a.ID, &a.RoleID, &a.ArmNum, &a.DataAccessLevel, &a.ExportLevel); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const roleGrantColumns = `id, role_id, event_id, instrument_id, data_access_level, export_level, delete_values, edit_surveys`

// ListRoleGrants returns the per-pair overrides of a role in (event,
// instrument) order (REQ-DB-040). Only overridden pairs appear — an absent row
// is the arm default, never a permission (REQ-AUTH-069).
func (s *Store) ListRoleGrants(ctx context.Context, roleID int64) ([]RoleGrant, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+roleGrantColumns+` FROM role_grants WHERE role_id = ?
		 ORDER BY event_id, instrument_id`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleGrant
	for rows.Next() {
		var (
			g           RoleGrant
			deleteValue int
			editSurvey  int
		)
		if err := rows.Scan(&g.ID, &g.RoleID, &g.EventID, &g.InstrumentID,
			&g.DataAccessLevel, &g.ExportLevel, &deleteValue, &editSurvey); err != nil {
			return nil, err
		}
		g.DeleteValues, g.EditSurveys = deleteValue != 0, editSurvey != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// --- user_projects (REQ-DB-010, GD-5) ---

const assignmentColumns = `id, user_id, project_id, role_id, token, created_at`

func scanAssignment(row interface{ Scan(dest ...any) error }) (Assignment, error) {
	var (
		a       Assignment
		created any
	)
	err := row.Scan(&a.ID, &a.UserID, &a.ProjectID, &a.RoleID, &a.Token, &created)
	if err != nil {
		return a, err
	}
	if v, ok := datetimeString(created); ok {
		a.CreatedAt = v
	}
	return a, nil
}

// AddAssignmentTx inserts a membership row inside the caller's transaction.
// An empty token is filled with a fresh UUID v4 (GD-5, REQ-AUTH-029).
func (s *Store) AddAssignmentTx(ctx context.Context, tx *sql.Tx, a *Assignment) (int64, error) {
	if a.Token == "" {
		a.Token = newToken()
	}
	a.CreatedAt = nowUTC()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO user_projects (user_id, project_id, role_id, token, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		a.UserID, a.ProjectID, nullInt64(a.RoleID), a.Token, a.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddAssignment inserts a membership row and returns the new id (REQ-API-054).
func (s *Store) AddAssignment(ctx context.Context, a *Assignment) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.AddAssignmentTx(ctx, tx, a)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) GetAssignment(ctx context.Context, userID, projectID int64) (*Assignment, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+assignmentColumns+` FROM user_projects WHERE user_id = ? AND project_id = ?`,
		userID, projectID)
	a, err := scanAssignment(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// GetAssignmentByToken is the data-API token check: a single indexed
// lookup (REQ-AUTH-032). nil when the token is unknown.
func (s *Store) GetAssignmentByToken(ctx context.Context, token string) (*Assignment, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+assignmentColumns+` FROM user_projects WHERE token = ?`, token)
	a, err := scanAssignment(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListAssignments(ctx context.Context, projectID int64) ([]Assignment, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+assignmentColumns+` FROM user_projects WHERE project_id = ? ORDER BY user_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAssignmentsByUser returns the projects a user is a member of
// (REQ-AUTH-026 project visibility).
func (s *Store) ListAssignmentsByUser(ctx context.Context, userID int64) ([]Assignment, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+assignmentColumns+` FROM user_projects WHERE user_id = ? ORDER BY project_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAssignmentRole changes the membership's role; an invalid roleID
// means role-less = full permissions (REQ-AUTH-022).
func (s *Store) SetAssignmentRole(ctx context.Context, userID, projectID int64, roleID sql.NullInt64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_projects SET role_id = ? WHERE user_id = ? AND project_id = ?`,
		nullInt64(roleID), userID, projectID)
	return err
}

// RotateAssignmentToken replaces the row's token with a fresh one and
// returns the new value; the previous token is invalid immediately
// (REQ-AUTH-030).
func (s *Store) RotateAssignmentToken(ctx context.Context, userID, projectID int64) (string, error) {
	tok := newToken()
	res, err := s.DB.ExecContext(ctx,
		`UPDATE user_projects SET token = ? WHERE user_id = ? AND project_id = ?`,
		tok, userID, projectID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", fmt.Errorf("rotate token: no assignment for user %d in project %d", userID, projectID)
	}
	return tok, nil
}

// RemoveAssignment deletes the membership row; its token is invalid
// immediately (REQ-API-055, REQ-AUTH-030).
func (s *Store) RemoveAssignment(ctx context.Context, userID, projectID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM user_projects WHERE user_id = ? AND project_id = ?`, userID, projectID)
	return err
}
