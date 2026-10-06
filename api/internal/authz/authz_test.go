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
	"csms/api/internal/testdb"
)

func openStore(t *testing.T) (*db.Store, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		AppEnv:       "development",
		DBConnection: "sqlite",
		DBDatabase:   filepath.Join(t.TempDir(), "authz-test.sqlite"),
	}
	testdb.Use(t, cfg)
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
	if !lv.AnyData(RankViewEdit) {
		t.Errorf("AnyData(view_edit) must hold for is_admin — the ladder ends there now")
	}
	if !lv.Unrestricted {
		t.Errorf("is_admin must be unrestricted, so a scope with no grant is not a denial (REQ-AUTH-023)")
	}
}

// TestEffectivePairGrants covers the revised GD-2 resolution: an arm default with
// a per-(instrument, event) grant over it, and denial beating a right on the pair
// it names (REQ-AUTH-069/070).
func TestEffectivePairGrants(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	pid := seedProject(t, s, "eff-pairs")

	armID := int64(0)
	arms, err := s.ListArms(ctx, pid)
	if err != nil || len(arms) == 0 {
		t.Fatalf("ListArms: %v", err)
	}
	armID = arms[0].ID
	eventID, err := s.AddEvent(ctx, &db.Event{
		ProjectID: pid, ArmID: armID, EventName: "Visit 1", UniqueEventName: "v1_arm_1",
	})
	if err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	instruments := make([]int64, 0, 3)
	for i, name := range []string{"open", "denied", "closable"} {
		id, err := s.AddInstrument(ctx, &db.Instrument{ProjectID: pid, Name: name, Position: i + 1})
		if err != nil {
			t.Fatalf("AddInstrument %s: %v", name, err)
		}
		instruments = append(instruments, id)
	}
	if err := s.SetInstrumentEventsForArm(ctx, pid, armID, []db.InstrumentEvent{
		{InstrumentID: instruments[0], EventID: eventID},
		{InstrumentID: instruments[1], EventID: eventID},
		{InstrumentID: instruments[2], EventID: eventID},
	}); err != nil {
		t.Fatalf("SetInstrumentEventsForArm: %v", err)
	}

	u := mkUser(t, s, "pairs@example.org", false)
	roleID, err := s.CreateRole(ctx,
		&db.Role{ProjectID: pid, RoleName: "pairwise"},
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "view_edit", ExportLevel: "export_full"}},
		// The design change materialized all three pairs at read_only /
		// export_none; this edit overrides two of them — one to no_access with a
		// right that must not survive it, one keeping the levels and adding both
		// rights (REQ-AUTH-018/070).
		[]db.RoleGrant{
			{EventID: eventID, InstrumentID: instruments[1], DataAccessLevel: "no_access",
				ExportLevel: "export_none", DeleteValues: true},
			{EventID: eventID, InstrumentID: instruments[2], DataAccessLevel: "view_edit",
				ExportLevel: "export_de_identified", DeleteValues: true, EditSurveys: true},
		})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := s.AddAssignment(ctx, &db.Assignment{
		UserID: u.ID, ProjectID: pid, Token: "tok-pairs",
		RoleID: sql.NullInt64{Int64: roleID, Valid: true},
	}); err != nil {
		t.Fatalf("AddAssignment: %v", err)
	}

	lv, err := Effective(ctx, s, u, pid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	// Arm default unchanged by grants the pair does not carry.
	if got := lv.PairOrArmDefault(eventID, instruments[0], 1); got.Data != "view_edit" ||
		got.Export != "export_full" || got.DeleteValues || got.EditSurveys {
		t.Errorf("inherited pair = %+v, want the arm default with neither right", got)
	}
	// Denial wins over the right it names (REQ-AUTH-070).
	if got := lv.PairOrArmDefault(eventID, instruments[1], 1); got.Data != "no_access" ||
		got.DeleteValues || got.EditSurveys {
		t.Errorf("denied pair = %+v, want no_access with both rights cleared", got)
	}
	// The pair's own levels and rights, overriding the arm default.
	if got := lv.PairOrArmDefault(eventID, instruments[2], 1); got.Data != "view_edit" ||
		got.Export != "export_de_identified" || !got.DeleteValues || !got.EditSurveys {
		t.Errorf("closable pair = %+v, want the grant's levels and both rights", got)
	}
	// A read_only arm default with one view_edit pair still reaches view_edit
	// for a project-level gate (REQ-AUTH-069).
	if !lv.AnyData(RankViewEdit) {
		t.Errorf("AnyData(view_edit) must hold through the pair grant")
	}
}

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
		[]db.RoleArm{{ArmNum: 1, DataAccessLevel: "read_only", ExportLevel: "export_no_identifiers"}}, nil)
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
	// As at server startup: makes the stable name audit_events cover the
	// current year on SQLite and partitions the table on MariaDB.
	if err := aw.EnsureYear(ctx); err != nil {
		t.Fatalf("EnsureYear: %v", err)
	}

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

	var n int
	err = s.DB.QueryRow(
		`SELECT count(*) FROM audit_events`+
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

	// A record id with no identity row is simply not visible. GetRecordEntity
	// reports not-found as (nil, nil) by repo convention, so this also guards
	// the grouped path against dereferencing that nil — a caller passes an
	// arbitrary record id from the URL (REQ-AUTH-045).
	if vis(grouped, "8DISC999") {
		t.Errorf("a record that does not exist must not be visible")
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

// TestAnyLevelsUnrestricted pins the two project-level gates for a subject whose
// maps are empty: unrestricted holds, because an arm-less or event-less project
// has nothing to enumerate (REQ-AUTH-022/023); restricted holds nothing, because
// access is never implicit (REQ-AUTH-019).
func TestAnyLevelsUnrestricted(t *testing.T) {
	lv := &Levels{IsMember: true, ProjectAdmin: true, Unrestricted: true}
	if !lv.AnyData(RankViewEdit) || !lv.AnyExport() {
		t.Errorf("an unrestricted subject with empty maps must hold the ladder")
	}
	if got := lv.PairOrArmDefault(0, 0, 0); DataRank(got.Data) != RankViewEdit ||
		!got.DeleteValues || !got.EditSurveys {
		t.Errorf("unrestricted fallback pair = %+v, want full rights", got)
	}
	if empty := (&Levels{IsMember: true}); empty.AnyData(RankReadOnly) || empty.AnyExport() {
		t.Errorf("a member with no grant anywhere must hold nothing (REQ-AUTH-019)")
	}
}
