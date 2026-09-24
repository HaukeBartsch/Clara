package db

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
)

var projectSeq int

func migrateTestStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedProject(t *testing.T, s *Store, ctx context.Context) int64 {
	t.Helper()
	projectSeq++
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO projects (project_name, organization, pi_name, pi_email, participant_names, creation_time)
		 VALUES (?, 'OTHER', 'PI', 'pi@example.org', 'REC', ?)`,
		"project-"+t.Name()+"-"+strconv.Itoa(projectSeq), nowUTC())
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed project id: %v", err)
	}
	return id
}

func seedUser(t *testing.T, s *Store, ctx context.Context, email string) int64 {
	t.Helper()
	id, err := s.CreateUser(ctx, &User{Email: email, DisplayName: "Test User", Enabled: true})
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// assertUUID checks the CHAR(36) UUID v4 form of a project token.
func assertUUID(t *testing.T, tok string) {
	t.Helper()
	if len(tok) != 36 || tok[8] != '-' || tok[13] != '-' || tok[18] != '-' || tok[23] != '-' || tok[14] != '4' {
		t.Fatalf("token %q is not a UUID v4 string", tok)
	}
}

func TestUserLifecycle(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	id, err := s.CreateUser(ctx, &User{Email: "alice@example.org", DisplayName: "Alice", Enabled: true, IsAdmin: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id <= 0 {
		t.Fatalf("CreateUser id = %d, want > 0", id)
	}

	got, err := s.GetUser(ctx, id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Email != "alice@example.org" || !got.Enabled || !got.IsAdmin {
		t.Errorf("GetUser = %+v, want alice enabled admin", got)
	}
	if got.AuthSource != "local" { // normalized default
		t.Errorf("AuthSource = %q, want local", got.AuthSource)
	}
	if got.UILanguage != "en" { // default
		t.Errorf("UILanguage = %q, want en", got.UILanguage)
	}
	if got.ValidUntil.Valid || got.LastLoginAt.Valid {
		t.Errorf("new user: ValidUntil/LastLoginAt = %+v/%+v, want both NULL", got.ValidUntil, got.LastLoginAt)
	}

	byEmail, err := s.GetUserByEmail(ctx, "alice@example.org")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail == nil || byEmail.ID != id {
		t.Fatalf("GetUserByEmail = %+v, want id %d", byEmail, id)
	}
	if none, _ := s.GetUser(ctx, 999999); none != nil {
		t.Errorf("GetUser(unknown) = %+v, want nil", none)
	}
	if none, _ := s.GetUserByEmail(ctx, "nobody@example.org"); none != nil {
		t.Errorf("GetUserByEmail(unknown) = %+v, want nil", none)
	}

	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].ID != id {
		t.Fatalf("ListUsers = %+v, want exactly alice", users)
	}

	// valid_until: set, then clear back to NULL (REQ-API-048, REQ-AUTH-052).
	if err := s.SetUserValidUntil(ctx, id, sql.NullString{String: "2026-12-31", Valid: true}); err != nil {
		t.Fatalf("SetUserValidUntil: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if !got.ValidUntil.Valid || got.ValidUntil.String != "2026-12-31" {
		t.Errorf("ValidUntil = %+v, want 2026-12-31", got.ValidUntil)
	}
	if err := s.SetUserValidUntil(ctx, id, sql.NullString{}); err != nil {
		t.Fatalf("clear ValidUntil: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if got.ValidUntil.Valid {
		t.Errorf("ValidUntil = %+v, want NULL", got.ValidUntil)
	}

	// password hash: set, then clear (REQ-AUTH-036, REQ-API-048).
	if err := s.SetUserPasswordHash(ctx, id, sql.NullString{String: "$2a$10$hash", Valid: true}); err != nil {
		t.Fatalf("SetUserPasswordHash: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if !got.PasswordHash.Valid || got.PasswordHash.String != "$2a$10$hash" {
		t.Errorf("PasswordHash = %+v, want the hash", got.PasswordHash)
	}
	if err := s.SetUserPasswordHash(ctx, id, sql.NullString{}); err != nil {
		t.Fatalf("clear PasswordHash: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if got.PasswordHash.Valid {
		t.Errorf("PasswordHash = %+v, want NULL", got.PasswordHash)
	}

	// last_login_at and inactivity clock (GD-19, REQ-AUTH-053).
	if err := s.TouchLastLogin(ctx, id, "oauth2"); err != nil {
		t.Fatalf("TouchLastLogin: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if !got.LastLoginAt.Valid || got.AuthSource != "oauth2" {
		t.Errorf("after TouchLastLogin: LastLoginAt=%+v AuthSource=%q, want set + oauth2", got.LastLoginAt, got.AuthSource)
	}
	if err := s.AutoDisable(ctx, id); err != nil {
		t.Fatalf("AutoDisable: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if got.Enabled || got.LastLoginAt.Valid {
		t.Errorf("after AutoDisable: Enabled=%v LastLoginAt=%+v, want disabled + clock cleared", got.Enabled, got.LastLoginAt)
	}
	if err := s.SetUserEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if !got.Enabled || got.LastLoginAt.Valid {
		t.Errorf("after re-enable: Enabled=%v LastLoginAt=%+v, want enabled + clock reset", got.Enabled, got.LastLoginAt)
	}
	if err := s.SetUserEnabled(ctx, id, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ = s.GetUser(ctx, id)
	if got.Enabled {
		t.Error("user still enabled after disable")
	}
}

func TestUpsertBootstrap(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()

	// Creation path: first installation without an IdP — the row is created
	// enabled, admin, with the bootstrap password provisioned
	// (REQ-AUTH-007, REQ-AUTH-051, GD-4).
	created, err := s.UpsertBootstrap(ctx, "admin@example.org", "Admin", "$2a$10$bootstrap")
	if err != nil {
		t.Fatalf("UpsertBootstrap: %v", err)
	}
	if created.ID <= 0 || !created.Enabled || !created.IsAdmin {
		t.Fatalf("bootstrap = %+v, want id>0 enabled admin", created)
	}
	if !created.PasswordHash.Valid || created.PasswordHash.String != "$2a$10$bootstrap" {
		t.Errorf("bootstrap PasswordHash = %+v, want provisioned", created.PasswordHash)
	}

	// Idempotent: same row, still promoted.
	again, err := s.UpsertBootstrap(ctx, "admin@example.org", "Admin", "$2a$10$bootstrap")
	if err != nil {
		t.Fatalf("second UpsertBootstrap: %v", err)
	}
	if again.ID != created.ID || !again.Enabled || !again.IsAdmin {
		t.Errorf("second UpsertBootstrap = %+v, want id %d enabled admin", again, created.ID)
	}

	// Promotion path: a pre-existing disabled, non-admin account with the
	// bootstrap email is enabled and made admin (REQ-AUTH-007); its local
	// password hash is left untouched (never clobbered).
	u := &User{Email: "promo@example.org", DisplayName: "Old Admin", Enabled: false,
		PasswordHash: sql.NullString{String: "$2a$10$existing", Valid: true}}
	prior, err := s.CreateUser(ctx, u)
	if err != nil {
		t.Fatalf("seed disabled admin: %v", err)
	}
	got, err := s.UpsertBootstrap(ctx, "promo@example.org", "Admin", "$2a$10$bootstrap")
	if err != nil {
		t.Fatalf("UpsertBootstrap (promotion): %v", err)
	}
	if got.ID != prior || !got.Enabled || !got.IsAdmin {
		t.Fatalf("promotion = %+v, want id %d enabled admin", got, prior)
	}
	if got.PasswordHash.String != "$2a$10$existing" {
		t.Errorf("existing hash = %q, want untouched", got.PasswordHash.String)
	}
}

func TestRoles(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)

	rid, err := s.CreateRole(ctx,
		&Role{ProjectID: pid, RoleName: "data-entry", ProjectAdmin: false},
		[]RoleArm{
			{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_full"},
			{ArmNum: 2, DataAccessLevel: "no_access", ExportLevel: "export_none"},
		})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if rid <= 0 {
		t.Fatalf("CreateRole id = %d, want > 0", rid)
	}

	got, err := s.GetRole(ctx, rid)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if got.RoleName != "data-entry" || got.ProjectID != pid || got.ProjectAdmin {
		t.Errorf("GetRole = %+v, want data-entry on project %d, not admin", got, pid)
	}

	byName, err := s.GetRoleByProjectName(ctx, pid, "data-entry")
	if err != nil {
		t.Fatalf("GetRoleByProjectName: %v", err)
	}
	if byName == nil || byName.ID != rid {
		t.Fatalf("GetRoleByProjectName = %+v, want id %d", byName, rid)
	}
	if none, _ := s.GetRoleByProjectName(ctx, pid, "no-such-role"); none != nil {
		t.Errorf("GetRoleByProjectName(unknown) = %+v, want nil", none)
	}
	if none, _ := s.GetRole(ctx, 999999); none != nil {
		t.Errorf("GetRole(unknown) = %+v, want nil", none)
	}

	roles, err := s.ListRoles(ctx, pid)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 1 || roles[0].ID != rid {
		t.Fatalf("ListRoles = %+v, want exactly data-entry", roles)
	}

	arms, err := s.ListRoleArms(ctx, rid)
	if err != nil {
		t.Fatalf("ListRoleArms: %v", err)
	}
	if len(arms) != 2 {
		t.Fatalf("ListRoleArms = %+v, want 2 arms", arms)
	}
	if arms[0].ArmNum != 1 || arms[0].DataAccessLevel != "view_edit" || arms[0].ExportLevel != "export_full" {
		t.Errorf("arm 1 = %+v, want view_edit/export_full", arms[0])
	}
	if arms[1].ArmNum != 2 || arms[1].DataAccessLevel != "no_access" || arms[1].ExportLevel != "export_none" {
		t.Errorf("arm 2 = %+v, want no_access/export_none", arms[1])
	}

	// Roles are project-scoped: the same name in another project is fine.
	pid2 := seedProject(t, s, ctx)
	rid2, err := s.CreateRole(ctx, &Role{ProjectID: pid2, RoleName: "data-entry"}, nil)
	if err != nil {
		t.Fatalf("CreateRole (other project): %v", err)
	}
	if rid2 == rid {
		t.Error("role ids collide across projects")
	}
	other, err := s.GetRoleByProjectName(ctx, pid2, "data-entry")
	if err != nil {
		t.Fatalf("GetRoleByProjectName (other project): %v", err)
	}
	if other == nil || other.ID != rid2 {
		t.Fatalf("GetRoleByProjectName = %+v, want id %d", other, rid2)
	}

	// Duplicate name within one project must fail (unique constraint).
	if _, err := s.CreateRole(ctx, &Role{ProjectID: pid, RoleName: "data-entry"}, nil); err == nil {
		t.Error("duplicate role name accepted, want conflict")
	}
}

func TestAssignments(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, ctx)
	uid := seedUser(t, s, ctx, "bob@example.org")
	other := seedUser(t, s, ctx, "carol@example.org")

	rid, err := s.CreateRole(ctx, &Role{ProjectID: pid, RoleName: "entry"}, nil)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	// Add with a role: a fresh UUID v4 token is generated (REQ-API-054, GD-5).
	a := &Assignment{UserID: uid, ProjectID: pid, RoleID: sql.NullInt64{Int64: rid, Valid: true}}
	aid, err := s.AddAssignment(ctx, a)
	if err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}
	if aid <= 0 || a.Token == "" || a.CreatedAt == "" {
		t.Fatalf("AddAssignment = id %d token %q created %q, want all set", aid, a.Token, a.CreatedAt)
	}
	assertUUID(t, a.Token)

	got, err := s.GetAssignment(ctx, uid, pid)
	if err != nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	if got == nil || got.ID != aid || got.RoleID.Int64 != rid || !got.RoleID.Valid {
		t.Fatalf("GetAssignment = %+v, want id %d with role %d", got, aid, rid)
	}
	if got.Token != a.Token {
		t.Error("stored token differs from returned token")
	}
	if none, _ := s.GetAssignment(ctx, other, pid); none != nil {
		t.Errorf("GetAssignment(non-member) = %+v, want nil", none)
	}

	byTok, err := s.GetAssignmentByToken(ctx, a.Token)
	if err != nil {
		t.Fatalf("GetAssignmentByToken: %v", err)
	}
	if byTok == nil || byTok.UserID != uid || byTok.ProjectID != pid {
		t.Fatalf("GetAssignmentByToken = %+v, want user %d project %d", byTok, uid, pid)
	}
	if none, _ := s.GetAssignmentByToken(ctx, "00000000-0000-4000-8000-000000000000"); none != nil {
		t.Errorf("GetAssignmentByToken(unknown) = %+v, want nil", none)
	}

	// Role-less member = full permissions (REQ-AUTH-022).
	c := &Assignment{UserID: other, ProjectID: pid}
	if _, err := s.AddAssignment(ctx, c); err != nil {
		t.Fatalf("AddAssignment (role-less): %v", err)
	}
	if c.Token == "" || c.Token == a.Token {
		t.Fatalf("second token %q, want distinct UUID", c.Token)
	}
	cgot, err := s.GetAssignment(ctx, other, pid)
	if err != nil {
		t.Fatalf("GetAssignment (role-less): %v", err)
	}
	if cgot == nil || cgot.RoleID.Valid {
		t.Fatalf("role-less member = %+v, want RoleID NULL", cgot)
	}

	list, err := s.ListAssignments(ctx, pid)
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListAssignments = %+v, want 2", list)
	}
	byUser, err := s.ListAssignmentsByUser(ctx, uid)
	if err != nil {
		t.Fatalf("ListAssignmentsByUser: %v", err)
	}
	if len(byUser) != 1 || byUser[0].ProjectID != pid {
		t.Fatalf("ListAssignmentsByUser = %+v, want project %d", byUser, pid)
	}

	// Role change: to role-less and back (REQ-API-054).
	if err := s.SetAssignmentRole(ctx, uid, pid, sql.NullInt64{}); err != nil {
		t.Fatalf("SetAssignmentRole (null): %v", err)
	}
	got, _ = s.GetAssignment(ctx, uid, pid)
	if got.RoleID.Valid {
		t.Errorf("role = %+v, want NULL", got.RoleID)
	}
	if err := s.SetAssignmentRole(ctx, uid, pid, sql.NullInt64{Int64: rid, Valid: true}); err != nil {
		t.Fatalf("SetAssignmentRole (role): %v", err)
	}
	got, _ = s.GetAssignment(ctx, uid, pid)
	if !got.RoleID.Valid || got.RoleID.Int64 != rid {
		t.Errorf("role = %+v, want %d", got.RoleID, rid)
	}

	// Rotation: new token works, old token is dead immediately (REQ-AUTH-030).
	newTok, err := s.RotateAssignmentToken(ctx, uid, pid)
	if err != nil {
		t.Fatalf("RotateAssignmentToken: %v", err)
	}
	assertUUID(t, newTok)
	if newTok == a.Token {
		t.Error("rotation returned the old token")
	}
	if old, _ := s.GetAssignmentByToken(ctx, a.Token); old != nil {
		t.Errorf("old token still resolves: %+v", old)
	}
	fresh, err := s.GetAssignmentByToken(ctx, newTok)
	if err != nil {
		t.Fatalf("GetAssignmentByToken (rotated): %v", err)
	}
	if fresh == nil || fresh.UserID != uid {
		t.Fatalf("rotated token = %+v, want user %d", fresh, uid)
	}
	if _, err := s.RotateAssignmentToken(ctx, uid, 424242); err == nil {
		t.Error("rotation for a non-member succeeded, want error")
	}

	// Removal: membership and token are gone immediately (REQ-API-055).
	if err := s.RemoveAssignment(ctx, other, pid); err != nil {
		t.Fatalf("RemoveAssignment: %v", err)
	}
	if gone, _ := s.GetAssignment(ctx, other, pid); gone != nil {
		t.Errorf("removed membership = %+v, want nil", gone)
	}
	if tok, _ := s.GetAssignmentByToken(ctx, c.Token); tok != nil {
		t.Errorf("removed token still resolves: %+v", tok)
	}

	// One membership per (user, project) (unique constraint).
	if _, err := s.AddAssignment(ctx, &Assignment{UserID: uid, ProjectID: pid}); err == nil {
		t.Error("duplicate membership accepted, want conflict")
	}
}
