package db

import (
	"context"
	"database/sql"
	"fmt"
)

// --- users (REQ-DB-008) ---

const userColumns = `id, email, display_name, enabled, auth_source, password_hash,
	valid_until, last_login_at, is_admin, ui_language, created_at`

func scanUser(row interface{ Scan(dest ...any) error }) (User, error) {
	var (
		u          User
		enabled    int
		isAdmin    int
		validUntil any
		lastLogin  any
	)
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &enabled, &u.AuthSource,
		&u.PasswordHash, &validUntil, &lastLogin, &isAdmin, &u.UILanguage, &u.CreatedAt)
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
func (s *Store) CreateUser(ctx context.Context, u *User) (int64, error) {
	u.AuthSource = normalizeAuthSource(u.AuthSource)
	if u.UILanguage == "" {
		u.UILanguage = "en"
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (email, display_name, enabled, auth_source, password_hash,
			valid_until, last_login_at, is_admin, ui_language, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Email, u.DisplayName, boolToInt(u.Enabled), u.AuthSource, nullStr(u.PasswordHash),
		nullStr(u.ValidUntil), nullStr(u.LastLoginAt), boolToInt(u.IsAdmin), u.UILanguage, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertBootstrap ensures the bootstrap admin row exists, is enabled, and is
// an administrator (REQ-AUTH-007, GD-4). Idempotent; returns the user.
func (s *Store) UpsertBootstrap(ctx context.Context, email, displayName, passwordHash string) (*User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.DisplayName, new(int), &u.AuthSource, &u.PasswordHash,
			new(any), new(any), new(int), &u.UILanguage, &u.CreatedAt)
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
// inactivity clock (last_login_at = NULL) per REQ-AUTH-053.
func (s *Store) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	if enabled {
		_, err := s.DB.ExecContext(ctx,
			`UPDATE users SET enabled = 1, last_login_at = NULL WHERE id = ?`, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 0 WHERE id = ?`, id)
	return err
}

func (s *Store) SetUserValidUntil(ctx context.Context, id int64, validUntil sql.NullString) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET valid_until = ? WHERE id = ?`, nullStr(validUntil), id)
	return err
}

func (s *Store) SetUserPasswordHash(ctx context.Context, id int64, hash sql.NullString) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, nullStr(hash), id)
	return err
}

func (s *Store) SetUILanguage(ctx context.Context, id int64, lang string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET ui_language = ? WHERE id = ?`, lang, id)
	return err
}

// AutoDisable marks a user disabled and clears the inactivity clock
// (REQ-AUTH-053), used by the account-active rule.
func (s *Store) AutoDisable(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 0, last_login_at = NULL WHERE id = ?`, id)
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

// --- roles (REQ-DB-009) ---

func (s *Store) CreateRole(ctx context.Context, r *Role, arms []RoleArm) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
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
	for i := range arms {
		a := arms[i]
		a.RoleID = id
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_arms (role_id, arm_num, data_access_level, export_level) VALUES (?, ?, ?, ?)`,
			a.RoleID, a.ArmNum, a.DataAccessLevel, a.ExportLevel); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
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

// ListRoleArms returns the per-arm levels of a role in arm order
// (REQ-DB-009).
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

// AddAssignment inserts a membership row and returns the new id
// (REQ-API-054). An empty token is filled with a fresh UUID v4 (GD-5,
// REQ-AUTH-029); the caller reads it back from a.Token.
func (s *Store) AddAssignment(ctx context.Context, a *Assignment) (int64, error) {
	if a.Token == "" {
		a.Token = newToken()
	}
	a.CreatedAt = nowUTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO user_projects (user_id, project_id, role_id, token, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		a.UserID, a.ProjectID, nullInt64(a.RoleID), a.Token, a.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
