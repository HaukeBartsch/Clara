// Package authz is the single explicit authorization function of the Go API
// (ASM-AUTH-5): effective levels per (user, project), the account-active
// rule evaluated at every authentication check (§4.4), and data-access-group
// record visibility (§4.2). No external policy engine; every decision is
// explicit and auditable (REQ-AUTH-019).
package authz

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/config"
	"csms/api/internal/db"
)

// Data access level ranks (REQ-DB-009). Higher rank implies the lower ones.
func DataRank(level string) int {
	switch level {
	case "read_only":
		return 1
	case "view_edit":
		return 2
	case "delete":
		return 3
	case "edit_survey_responses":
		return 4
	default: // no_access and anything unknown
		return 0
	}
}

// ExportRank orders the export ladder (REQ-API-026). Higher rank is less
// restrictive.
func ExportRank(level string) int {
	switch level {
	case "export_de_identified":
		return 1
	case "export_no_identifiers":
		return 2
	case "export_full":
		return 3
	default: // export_none and anything unknown
		return 0
	}
}

// Levels is the acting user's effective access to one project (§4.1).
type Levels struct {
	IsMember     bool
	ProjectAdmin bool
	Data         map[int]string // arm_num -> data level
	Export       map[int]string // arm_num -> export level
}

// DataFor returns the user's data level on an arm ("" when none).
func (l *Levels) DataFor(armNum int) string { return l.Data[armNum] }

// ExportFor returns the user's export level on an arm.
func (l *Levels) ExportFor(armNum int) string { return l.Export[armNum] }

// HasData reports whether the user's level on the arm reaches min
// (read_only=1 … edit_survey_responses=4).
func (l *Levels) HasData(armNum, min int) bool {
	return DataRank(l.Data[armNum]) >= min
}

// AnyData reports whether the user reaches min on at least one arm — the
// reading of "data access ≥ read_only" for project-level reads (§4.5).
func (l *Levels) AnyData(min int) bool {
	for _, level := range l.Data {
		if DataRank(level) >= min {
			return true
		}
	}
	return false
}

const (
	fullData   = "edit_survey_responses"
	fullExport = "export_full"
)

// Effective computes the effective levels for a user on a project at call
// time (REQ-AUTH-033): is_admin covers everything (REQ-AUTH-023); a role
// gives its per-arm grants with no implicit access (REQ-AUTH-019); a member
// without a role holds full rights (REQ-AUTH-022). A non-member gets an all-
// zero Levels — the uniform 403 of REQ-API-007 never discloses membership.
func Effective(ctx context.Context, store *db.Store, user *db.User, projectID int64) (*Levels, error) {
	lv := &Levels{Data: map[int]string{}, Export: map[int]string{}}
	if user.IsAdmin {
		lv.IsMember = true
		lv.ProjectAdmin = true
		arms, err := store.ListArms(ctx, projectID)
		if err != nil {
			return nil, err
		}
		for _, a := range arms {
			lv.Data[a.ArmNum] = fullData
			lv.Export[a.ArmNum] = fullExport
		}
		return lv, nil
	}
	asg, err := store.GetAssignment(ctx, user.ID, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return lv, nil // not a member
	}
	if err != nil {
		return nil, err
	}
	lv.IsMember = true

	arms, err := store.ListArms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, a := range arms { // default: no implicit access (REQ-AUTH-019)
		lv.Data[a.ArmNum] = "no_access"
		lv.Export[a.ArmNum] = "export_none"
	}

	if !asg.RoleID.Valid { // role-less member: full permissions (REQ-AUTH-022)
		lv.ProjectAdmin = true
		for _, a := range arms {
			lv.Data[a.ArmNum] = fullData
			lv.Export[a.ArmNum] = fullExport
		}
		return lv, nil
	}
	role, err := store.GetRole(ctx, asg.RoleID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		// Dangling role reference: treat as role-less would grant too much;
		// deny (no implicit access).
		return lv, nil
	}
	if err != nil {
		return nil, err
	}
	lv.ProjectAdmin = role.ProjectAdmin
	roleArms, err := store.ListRoleArms(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	for _, ra := range roleArms {
		lv.Data[ra.ArmNum] = ra.DataAccessLevel
		lv.Export[ra.ArmNum] = ra.ExportLevel
	}
	return lv, nil
}

// ActiveState is the outcome of the account-active rule (§4.4).
type ActiveState string

const (
	Active          ActiveState = "active"
	AccountDisabled ActiveState = "account_disabled"
	AccountExpired  ActiveState = "account_expired"
)

// CheckActive evaluates the account-active rule at an authentication check
// (login, administration API, data-API token): enabled, not expired, not
// inactive. The inactivity case performs the auto-disable — enabled=0 and
// last_login_at reset, same transaction as the account_auto_disabled audit
// entry — and mutates user accordingly (REQ-AUTH-053, REQ-AUD-024).
func CheckActive(ctx context.Context, store *db.Store, aw *audit.Writer, cfg *config.Config,
	user *db.User, now time.Time) (ActiveState, error) {
	if !user.Enabled {
		return AccountDisabled, nil
	}
	if user.ValidUntil.Valid && user.ValidUntil.String < now.UTC().Format("2006-01-02") {
		return AccountExpired, nil
	}
	if cfg.AuthInactivityLimitDays > 0 && user.LastLoginAt.Valid {
		last, err := parseUTC(user.LastLoginAt.String)
		if err == nil && now.UTC().Sub(last) > time.Duration(cfg.AuthInactivityLimitDays)*24*time.Hour {
			if err := autoDisable(ctx, store, aw, cfg, user, now); err != nil {
				return AccountDisabled, err
			}
			return AccountDisabled, nil
		}
	}
	return Active, nil
}

// autoDisable sets enabled=0 and clears the inactivity clock, committing the
// account_auto_disabled audit entry in the same transaction (REQ-AUD-003).
func autoDisable(ctx context.Context, store *db.Store, aw *audit.Writer, cfg *config.Config,
	user *db.User, now time.Time) error {
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	last := ""
	if user.LastLoginAt.Valid {
		last = user.LastLoginAt.String
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET enabled = 0, last_login_at = NULL WHERE id = ?`, user.ID); err != nil {
		return err
	}
	if err := aw.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.AccountAutoDisabled,
		Source:    audit.SourceSystem,
		UserID:    user.ID,
		Details: map[string]any{
			"email":                user.Email,
			"last_login_at":        last,
			"inactivity_limit_days": cfg.AuthInactivityLimitDays,
		},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	user.Enabled = false
	user.LastLoginAt = sql.NullString{}
	return nil
}

// UserStatus derives the displayed account status (GD-19, REQ-AUTH-052/053):
// auto_disabled is a disabled account whose inactivity clock was reset by the
// auto-disable rule (last_login_at cleared).
func UserStatus(u *db.User, now time.Time) string {
	if !u.Enabled {
		if !u.LastLoginAt.Valid {
			return "auto_disabled"
		}
		return "disabled"
	}
	if u.ValidUntil.Valid && u.ValidUntil.String < now.UTC().Format("2006-01-02") {
		return "expired"
	}
	return "active"
}

// ActiveGroup returns the acting member's active data-access group for a
// project (0 = none) — §4.2. The record scope it produces is orthogonal to
// the permission levels (REQ-AUTH-045).
func ActiveGroup(ctx context.Context, store *db.Store, userID, projectID int64) (int64, error) {
	asg, err := store.GetAssignment(ctx, userID, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	memberships, err := store.ListDAGMembershipsByAssignment(ctx, asg.ID)
	if err != nil {
		return 0, err
	}
	for _, m := range memberships {
		if m.IsActive {
			return m.GroupID, nil
		}
	}
	return 0, nil
}

// RecordVisible reports whether a record is visible to the acting user under
// the DAG rule (§4.2): administrators see all records; a member with an
// active group sees only records assigned to it (unassigned records are not
// visible to grouped members); a member without a group sees all.
func RecordVisible(ctx context.Context, store *db.Store, user *db.User, projectID int64, recordID string) (bool, error) {
	if user.IsAdmin {
		return true, nil
	}
	group, err := ActiveGroup(ctx, store, user.ID, projectID)
	if err != nil || group == 0 {
		return group == 0, err
	}
	re, err := store.GetRecordEntity(ctx, projectID, recordID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return re.DagGroupID.Valid && re.DagGroupID.Int64 == group, nil
}

func parseUTC(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unparseable timestamp: " + s)
}

// --- request context (acting user set by the administration middleware) ---

type ctxKey int

const actorKey ctxKey = 1

// WithActor returns a context carrying the acting user resolved by the
// administration-API middleware (X-Internal-User-Id, REQ-AUTH-013).
func WithActor(ctx context.Context, u *db.User) context.Context {
	return context.WithValue(ctx, actorKey, u)
}

// ActorFrom returns the acting user from the context, or nil.
func ActorFrom(ctx context.Context) *db.User {
	u, _ := ctx.Value(actorKey).(*db.User)
	return u
}
