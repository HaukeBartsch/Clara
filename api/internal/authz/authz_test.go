package authz

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

func openStore(t *testing.T) (*db.Store, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "authz-test.sqlite"),
	}
	s, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s, cfg
}

func mkUser(t *testing.T, s *db.Store, email string, admin bool) *db.User {
	t.Helper()
	u := &db.User{Email: email, DisplayName: email, AuthSource: "local", Enabled: true, IsAdmin: admin}
	id, err := s.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatalf("CreateUser %s: %v", email, err)
	}
	u.ID = id
	return u
}

// seedProject returns a project with two arms (arm_num 1 and 2).
func seedProject(t *testing.T, s *db.Store, name string) int64 {
	t.Helper()
	ctx := context.Background()
	pid, err := s.CreateProject(ctx, &db.Project{ProjectName: name, ParticipantNames: "8DISC[0-9][0-9][0-9]"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, n := range []int{1, 2} {
		if _, err := s.AddArm(ctx, &db.Arm{ProjectID: pid, ArmNum: n}); err != nil {
			t.Fatalf("AddArm %d: %v", n, err)
		}
	}
	return pid
}

func TestEffectiveAdmin(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, "eff-admin")
	admin := mkUser(t, s, "admin@example.org", true)

	lv, err := Effective(ctx, s, admin, pid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !lv.IsMember || !lv.ProjectAdmin {
		t.Errorf("is_admin must cover membership and project_admin (REQ-AUTH-023)")
	}
	for _, arm := range []int{1, 2} {
		if lv.DataFor(arm) != fullData || lv.ExportFor(arm) != fullExport {
			t.Errorf("arm %d: got %q/%q, want full/full", arm, lv.DataFor(arm), lv.ExportFor(arm))
		}
	}
	if !lv.AnyData(LvlEditSurveyRank) {
		t.Errorf("AnyData(edit_survey_responses) must hold for is_admin")
	}
}

// LvlEditSurveyRank mirrors the admin package's rank constant for tests here.
const LvlEditSurveyRank = 4

func TestEffectiveRolelessMember(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, "eff-roleless")
	u := mkUser(t, s, "member@example.org", false)
	if _, err := s.AddAssignment(ctx, &db.Assignment{UserID: u.ID, ProjectID: pid, Token: "tok-roleless"}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}

	lv, err := Effective(ctx, s, u, pid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// Role-less member holds full rights (REQ-AUTH-022).
	if !lv.IsMember || !lv.ProjectAdmin {
		t.Errorf("role-less member must be project_admin")
	}
	for _, arm := range []int{1, 2} {
		if lv.DataFor(arm) != fullData || lv.ExportFor(arm) != fullExport {
			t.Errorf("arm %d: got %q/%q, want full/full", arm, lv.DataFor(arm), lv.ExportFor(arm))
		}
	}
}

func TestEffectiveRoleMemberNoImplicitAccess(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, "eff-role")
	u := mkUser(t, s, "ro@example.org", false)

	roleID, err := s.CreateRole(ctx,
		&db.Role{ProjectID: pid, RoleName: "reader", ProjectAdmin: false},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_no_identifiers"}})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := s.AddAssignment(ctx, &db.Assignment{
		UserID: u.ID, ProjectID: pid, Token: "tok-role",
		RoleID: sql.NullInt64{Int64: roleID, Valid: true},
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}

	lv, err := Effective(ctx, s, u, pid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !lv.IsMember || lv.ProjectAdmin {
		t.Errorf("member with non-admin role: IsMember=%v ProjectAdmin=%v", lv.IsMember, lv.ProjectAdmin)
	}
	if lv.DataFor(1) != "read_only" || lv.ExportFor(1) != "export_no_identifiers" {
		t.Errorf("arm 1 grants = %q/%q", lv.DataFor(1), lv.ExportFor(1))
	}
	// Arm 2 is absent from the role: no implicit access (REQ-AUTH-019).
	if lv.DataFor(2) != "no_access" || lv.ExportFor(2) != "export_none" {
		t.Errorf("arm 2 must default to no_access/export_none, got %q/%q", lv.DataFor(2), lv.ExportFor(2))
	}
	if !lv.HasData(1, 1) || lv.HasData(1, 2) || lv.HasData(2, 1) {
		t.Errorf("HasData ranks wrong: 1@1=%v 1@2=%v 2@1=%v", lv.HasData(1, 1), lv.HasData(1, 2), lv.HasData(2, 1))
	}
	if !lv.AnyData(1) {
		t.Errorf("AnyData(read_only) must hold via arm 1")
	}
}

func TestEffectiveNonMember(t *testing.T) {
	s, _ := openStore(t)
	pid := seedProject(t, s, "eff-outsider")
	u := mkUser(t, s, "outsider@example.org", false)

	lv, err := Effective(context.Background(), s, u, pid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if lv.IsMember || lv.ProjectAdmin || len(lv.Data) != 0 || len(lv.Export) != 0 {
		t.Errorf("non-member must get an all-zero Levels, got %+v", lv)
	}
}

func TestUserStatus(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		u    db.User
		want string
	}{
		{"active", db.User{Enabled: true}, "active"},
		{"disabled", db.User{Enabled: false, LastLoginAt: sql.NullString{String: "2026-01-01 10:00:00", Valid: true}}, "disabled"},
		{"auto_disabled", db.User{Enabled: false}, "auto_disabled"},
		{"expired", db.User{Enabled: true, ValidUntil: sql.NullString{String: "2026-09-25", Valid: true}}, "expired"},
		{"valid_until_today", db.User{Enabled: true, ValidUntil: sql.NullString{String: "2026-09-26", Valid: true}}, "active"},
	}
	for _, c := range cases {
		if got := UserStatus(&c.u, now); got != c.want {
			t.Errorf("%s: UserStatus = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCheckActiveDisabledAndExpired(t *testing.T) {
	s, cfg := openStore(t)
	ctx := context.Background()
	aw := audit.NewWriter(s.DB, string(s.Dialect))
	now := time.Now().UTC()

	disabled := mkUser(t, s, "off@example.org", false)
	if err := s.SetUserEnabled(ctx, disabled.ID, false); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}
	disabled.Enabled = false
	if st, err := CheckActive(ctx, s, aw, cfg, disabled, now); err != nil || st != AccountDisabled {
		t.Errorf("disabled account: %v/%v, want account_disabled", st, err)
	}

	expired := mkUser(t, s, "expired@example.org", false)
	if err := s.SetUserValidUntil(ctx, expired.ID, sql.NullString{String: "2020-01-01", Valid: true}); err != nil {
		t.Fatalf("SetUserValidUntil: %v", err)
	}
	expired.ValidUntil = sql.NullString{String: "2020-01-01", Valid: true}
	if st, err := CheckActive(ctx, s, aw, cfg, expired, now); err != nil || st != AccountExpired {
		t.Errorf("expired account: %v/%v, want account_expired", st, err)
	}
}

func TestCheckActiveInactivityAutoDisable(t *testing.T) {
	s, cfg := openStore(t)
	cfg.AuthInactivityLimitDays = 180
	ctx := context.Background()
	aw := audit.NewWriter(s.DB, string(s.Dialect))

	u := mkUser(t, s, "ghost@example.org", false)
	stale := time.Now().UTC().Add(-200 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, stale, u.ID); err != nil {
		t.Fatalf("seed last_login_at: %v", err)
	}
	u.LastLoginAt = sql.NullString{String: stale, Valid: true}

	st, err := CheckActive(ctx, s, aw, cfg, u, time.Now().UTC())
	if err != nil {
		t.Fatalf("CheckActive: %v", err)
	}
	if st != AccountDisabled {
		t.Fatalf("stale account: %v, want account_disabled", st)
	}

	fresh, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if fresh.Enabled || fresh.LastLoginAt.Valid {
		t.Errorf("auto-disable must clear enabled and the inactivity clock, got enabled=%v last_login=%v",
			fresh.Enabled, fresh.LastLoginAt)
	}

	year := time.Now().UTC().Year()
	var n int
	err = s.DB.QueryRow(
		`SELECT count(*) FROM audit_events_`+itoa(year)+
			` WHERE event_type='account_auto_disabled' AND user_id=?`, u.ID).Scan(&n)
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if n != 1 {
		t.Fatalf("account_auto_disabled audit entries = %d, want 1 (REQ-AUD-024)", n)
	}

	// The derived status now reads auto_disabled (GD-19).
	if got := UserStatus(fresh, time.Now().UTC()); got != "auto_disabled" {
		t.Errorf("UserStatus after auto-disable = %q, want auto_disabled", got)
	}
}

func TestActiveGroupAndRecordVisibility(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, "dag-vis")
	admin := mkUser(t, s, "dag-admin@example.org", true)
	grouped := mkUser(t, s, "grouped@example.org", false)
	ungrouped := mkUser(t, s, "ungrouped@example.org", false)

	g1, err := s.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: pid, Name: "site-a"})
	if err != nil {
		t.Fatalf("CreateDAGGroup: %v", err)
	}
	asgG, err := s.AddAssignment(ctx, &db.Assignment{UserID: grouped.ID, ProjectID: pid, Token: "tok-dag-g"})
	if err != nil {
		t.Fatalf("AddAssignment grouped: %v", err)
	}
	if _, err := s.AddAssignment(ctx, &db.Assignment{UserID: ungrouped.ID, ProjectID: pid, Token: "tok-dag-u"}); err != nil {
		t.Fatalf("AddAssignment ungrouped: %v", err)
	}
	if _, err := s.AddDAGMembership(ctx, &db.DagMembership{AssignmentID: asgG, GroupID: g1, IsActive: true}); err != nil {
		t.Fatalf("AddDAGMembership: %v", err)
	}

	if got, err := ActiveGroup(ctx, s, grouped.ID, pid); err != nil || got != g1 {
		t.Fatalf("ActiveGroup = %d/%v, want %d", got, err, g1)
	}
	if got, err := ActiveGroup(ctx, s, ungrouped.ID, pid); err != nil || got != 0 {
		t.Fatalf("ActiveGroup ungrouped = %d/%v, want 0", got, err)
	}

	must := func(record string, dag sql.NullInt64) {
		if err := s.CreateRecordEntity(ctx, &db.RecordEntity{ProjectID: pid, RecordID: record, DagGroupID: dag}); err != nil {
			t.Fatalf("CreateRecordEntity %s: %v", record, err)
		}
	}
	must("8DISC001", sql.NullInt64{Int64: g1, Valid: true}) // in the group
	must("8DISC002", sql.NullInt64{})                       // unassigned

	vis := func(u *db.User, record string) bool {
		ok, err := RecordVisible(ctx, s, u, pid, record)
		if err != nil {
			t.Fatalf("RecordVisible %s/%s: %v", u.Email, record, err)
		}
		return ok
	}
	if !vis(admin, "8DISC001") || !vis(admin, "8DISC002") {
		t.Errorf("administrator must see all records")
	}
	if !vis(ungrouped, "8DISC001") || !vis(ungrouped, "8DISC002") {
		t.Errorf("member without a group sees all records")
	}
	if !vis(grouped, "8DISC001") {
		t.Errorf("grouped member must see records in the active group")
	}
	if vis(grouped, "8DISC002") {
		t.Errorf("unassigned records are not visible to grouped members (§4.2)")
	}

	// A record assigned to a different group is invisible as well.
	g2, err := s.CreateDAGGroup(ctx, &db.DagGroup{ProjectID: pid, Name: "site-b"})
	if err != nil {
		t.Fatalf("CreateDAGGroup 2: %v", err)
	}
	must("8DISC003", sql.NullInt64{Int64: g2, Valid: true})
	if vis(grouped, "8DISC003") {
		t.Errorf("records of another group must not be visible")
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
