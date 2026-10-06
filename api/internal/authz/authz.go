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

// Data access ranks, ascending (REQ-DB-009 as revised 2026-10-03). The ladder
// ends at view_edit: deleting values and editing collected surveys are rights
// of a grant, implied by no level (GD-2, REQ-AUTH-018/070).
const (
	RankNoAccess = iota
	RankReadOnly
	RankViewEdit
)

// DataRank orders the data-access ladder. Higher rank implies the lower ones.
//
// The two retired rungs (`delete`, `edit_survey_responses`) rank as view_edit:
// migration 0011 normalizes every stored row, so this is the defensive reading
// of a value that should no longer exist — both were strictly above read_only,
// and their extra privilege now arrives as an explicit right instead.
func DataRank(level string) int {
	switch level {
	case "read_only":
		return RankReadOnly
	case "view_edit", "delete", "edit_survey_responses":
		return RankViewEdit
	default: // no_access and anything unknown
		return RankNoAccess
	}
}

// Export ranks, ascending (REQ-AUTH-017). Higher rank is less restrictive.
const (
	RankExportNone = iota
	RankExportDeIdentified
	RankExportNoIdentifiers
	RankExportFull
)

// ExportRank orders the export ladder (REQ-API-026).
func ExportRank(level string) int {
	switch level {
	case "export_de_identified":
		return RankExportDeIdentified
	case "export_no_identifiers":
		return RankExportNoIdentifiers
	case "export_full":
		return RankExportFull
	default: // export_none and anything unknown
		return RankExportNone
	}
}

// ExportName is the inverse of ExportRank — the vocabulary the API and the
// audit details speak (REQ-AUD-011).
func ExportName(rank int) string {
	switch rank {
	case RankExportDeIdentified:
		return "export_de_identified"
	case RankExportNoIdentifiers:
		return "export_no_identifiers"
	case RankExportFull:
		return "export_full"
	default:
		return "export_none"
	}
}

// Pair names one (instrument, event) grant scope — the unit permissions are
// held at since GD-2 was revised (REQ-AUTH-069).
type Pair struct {
	EventID      int64
	InstrumentID int64
}

// PairLevels is the access resolved for one pair: the arm default with the
// role's grant over it where one exists, plus the two rights (REQ-AUTH-069/
// 070). The zero value is no access at all.
type PairLevels struct {
	Data         string
	Export       string
	DeleteValues bool // may clear this pair's stored values (REQ-AUTH-018)
	EditSurveys  bool // may modify a collected survey of this pair (REQ-AUTH-071)
}

// DataRankOf ranks the pair's data level.
func (p PairLevels) DataRankOf() int { return DataRank(p.Data) }

// ExportRankOf ranks the pair's export level.
func (p PairLevels) ExportRankOf() int { return ExportRank(p.Export) }

// Levels is the acting user's effective access to one project (§4.1): an arm
// default and, for every mapped (instrument, event) pair, the resolved grant on
// top of it.
type Levels struct {
	IsMember     bool
	ProjectAdmin bool
	// Unrestricted marks a subject that holds everything by definition —
	// is_admin, or a member with no role (REQ-AUTH-022/023). It matters where a
	// scope has no grant to read: an instrument not yet mapped to anything, or a
	// project without events, must resolve as full for them rather than as the
	// no-access default that protects everyone else (REQ-AUTH-019).
	Unrestricted bool
	Data         map[int]string      // arm_num -> arm default data level
	Export       map[int]string      // arm_num -> arm default export level
	Pairs        map[Pair]PairLevels // mapped pair -> resolved access
}

// fullPairLevels is what an unrestricted subject holds on any scope.
func fullPairLevels() PairLevels {
	return PairLevels{Data: fullData, Export: fullExport, DeleteValues: true, EditSurveys: true}
}

// DataFor returns the user's default data level on an arm ("" when none).
func (l *Levels) DataFor(armNum int) string { return l.Data[armNum] }

// ExportFor returns the user's default export level on an arm.
func (l *Levels) ExportFor(armNum int) string { return l.Export[armNum] }

// HasData reports whether the arm's default reaches min. A pair override does
// not count here — a caller that knows the pair asks PairOrArmDefault.
func (l *Levels) HasData(armNum, min int) bool {
	return DataRank(l.Data[armNum]) >= min
}

// PairAt returns the resolved levels of one mapped pair, and whether the pair
// is mapped at all. An unmapped pair has no grant to resolve: the caller falls
// back to the arm default (PairOrArmDefault does exactly that).
func (l *Levels) PairAt(eventID, instrumentID int64) (PairLevels, bool) {
	if l == nil {
		return PairLevels{}, false
	}
	g, ok := l.Pairs[Pair{EventID: eventID, InstrumentID: instrumentID}]
	return g, ok
}

// PairOrArmDefault resolves one pair, falling back to its arm default when no
// grant exists — which covers both an unmapped pair and a project without
// events (armNum is the arm the data belongs to there). The fallback carries
// neither right: rights are granted per pair or not at all (REQ-AUTH-070).
func (l *Levels) PairOrArmDefault(eventID, instrumentID int64, armNum int) PairLevels {
	if l == nil {
		return PairLevels{}
	}
	if g, ok := l.PairAt(eventID, instrumentID); ok {
		return g
	}
	if l.Unrestricted { // nothing to grant, because nothing can be withheld
		return fullPairLevels()
	}
	return PairLevels{Data: l.Data[armNum], Export: l.Export[armNum]}
}

// AnyData reports whether the user reaches min anywhere — on an arm default or
// on any pair grant. This is the reading of "data access ≥ read_only" for
// project-level reads, where no pair has been named yet (§4.5, REQ-AUTH-069).
func (l *Levels) AnyData(min int) bool {
	if l == nil {
		return false
	}
	if l.Unrestricted {
		// Nothing to grant, because nothing can be withheld — and a project
		// without arms or events has no map left to search (REQ-AUTH-022).
		return DataRank(fullData) >= min
	}
	for _, level := range l.Data {
		if DataRank(level) >= min {
			return true
		}
	}
	for _, g := range l.Pairs {
		if DataRank(g.Data) >= min {
			return true
		}
	}
	return false
}

// AnyExport reports whether anything at all may be exported — the project-
// level gate of the export card (REQ-UI-020). The per-column sensitivity is
// resolved per pair, never from this (Data_Export_Anonymization_Design.md §4.3).
func (l *Levels) AnyExport() bool {
	if l == nil {
		return false
	}
	if l.Unrestricted {
		return ExportRank(fullExport) > RankExportNone
	}
	for _, level := range l.Export {
		if ExportRank(level) > RankExportNone {
			return true
		}
	}
	for _, g := range l.Pairs {
		if ExportRank(g.Export) > RankExportNone {
			return true
		}
	}
	return false
}

const (
	fullData   = "view_edit"
	fullExport = "export_full"
)

// Effective computes the effective levels for a user on a project at call time
// (REQ-AUTH-033): is_admin covers everything (REQ-AUTH-023); a role gives its
// arm defaults and per-pair grants with no implicit access (REQ-AUTH-019); a
// member without a role holds full rights (REQ-AUTH-022). A non-member gets an
// all-zero Levels — the uniform 403 of REQ-API-007 never discloses membership.
func Effective(ctx context.Context, store *db.Store, user *db.User, projectID int64) (*Levels, error) {
	asg, err := store.GetAssignment(ctx, user.ID, projectID)
	if err != nil {
		return nil, err
	}
	return EffectiveForAssignment(ctx, store, user, projectID, asg)
}

// EffectiveForAssignment is §4.1 for a caller that has already resolved the
// assignment — the data API, whose token lookup *is* the assignment lookup
// (REQ-AUTH-032), reads its levels without paying for the row twice. A nil
// assignment is "not a member".
func EffectiveForAssignment(ctx context.Context, store *db.Store, user *db.User,
	projectID int64, asg *db.Assignment) (*Levels, error) {
	arms, err := store.ListArms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	events, err := store.ListEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pairs, err := store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	lv := &Levels{Data: map[int]string{}, Export: map[int]string{}, Pairs: map[Pair]PairLevels{}}

	// arm_of[event] lets a pair fall back to the default of its own arm.
	armNumOf := map[int64]int{}
	for _, a := range arms {
		armNumOf[a.ID] = a.ArmNum
	}
	eventArm := map[int64]int{}
	for _, e := range events {
		eventArm[e.ID] = armNumOf[e.ArmID]
	}

	full := func() { // every arm and every pair, rights included
		lv.ProjectAdmin = true
		lv.Unrestricted = true
		for _, a := range arms {
			lv.Data[a.ArmNum] = fullData
			lv.Export[a.ArmNum] = fullExport
		}
		for _, p := range pairs {
			lv.Pairs[Pair{EventID: p.EventID, InstrumentID: p.InstrumentID}] =
				PairLevels{Data: fullData, Export: fullExport, DeleteValues: true, EditSurveys: true}
		}
	}

	if user.IsAdmin { // REQ-AUTH-023
		lv.IsMember = true
		full()
		return lv, nil
	}
	if asg == nil { // repo convention: not-found is (nil, nil) — not a member
		return lv, nil
	}
	lv.IsMember = true
	for _, a := range arms { // default: no implicit access (REQ-AUTH-019)
		lv.Data[a.ArmNum] = "no_access"
		lv.Export[a.ArmNum] = "export_none"
	}
	if !asg.RoleID.Valid { // role-less member: full permissions (REQ-AUTH-022)
		full()
		return lv, nil
	}
	role, err := store.GetRole(ctx, asg.RoleID.Int64)
	if err != nil {
		return nil, err
	}
	if role == nil {
		// Dangling role reference: treating it as role-less would grant too
		// much, so it stays at the no-access default (REQ-AUTH-019).
		return lv, nil
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
	grants, err := store.ListRoleGrants(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	grantOf := map[Pair]db.RoleGrant{}
	for _, g := range grants {
		grantOf[Pair{EventID: g.EventID, InstrumentID: g.InstrumentID}] = g
	}
	// Only a mapped pair resolves (REQ-AUTH-069): the grant over its arm
	// default when one exists, the arm default otherwise.
	for _, p := range pairs {
		key := Pair{EventID: p.EventID, InstrumentID: p.InstrumentID}
		armNum := eventArm[p.EventID]
		g := PairLevels{Data: lv.Data[armNum], Export: lv.Export[armNum]}
		if stored, ok := grantOf[key]; ok {
			g = PairLevels{
				Data: stored.DataAccessLevel, Export: stored.ExportLevel,
				DeleteValues: stored.DeleteValues, EditSurveys: stored.EditSurveys,
			}
		}
		if DataRank(g.Data) == RankNoAccess { // denial wins over a right (REQ-AUTH-070)
			g.DeleteValues, g.EditSurveys = false, false
		}
		lv.Pairs[key] = g
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
			"email":                 user.Email,
			"last_login_at":         last,
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
	if err != nil {
		return 0, err
	}
	if asg == nil { // not a member: no active group
		return 0, nil
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
	if re == nil { // repo convention: not-found is (nil, nil) — nothing to match
		return false, nil
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
