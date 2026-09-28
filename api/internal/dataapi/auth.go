package dataapi

import (
	"context"
	"errors"
	"time"

	"csms/api/internal/db"
)

// errInvalidToken is the uniform 401 failure of the data API: a missing,
// unknown, or rejected token all render as "Invalid token" so callers
// cannot probe for valid ones (REQ-AUTH-032, REQ-API-011).
var errInvalidToken = errors.New("invalid token")

// Data-level ranks, in ascending privilege (REQ-DB-009).
const (
	lvlNoAccess = iota
	lvlReadOnly
	lvlViewEdit
	lvlDelete
	lvlEditSurveyResponses
)

func dataRank(level string) int {
	switch level {
	case "read_only":
		return lvlReadOnly
	case "view_edit":
		return lvlViewEdit
	case "delete":
		return lvlDelete
	case "edit_survey_responses":
		return lvlEditSurveyResponses
	default:
		return lvlNoAccess
	}
}

// Export-level ranks, in ascending privilege (GD-2, REQ-AUTH-017).
const (
	expNone = iota
	expDeIdentified
	expNoIdentifiers
	expFull
)

func exportRank(level string) int {
	switch level {
	case "export_de_identified":
		return expDeIdentified
	case "export_no_identifiers":
		return expNoIdentifiers
	case "export_full":
		return expFull
	default:
		return expNone
	}
}

func exportName(rank int) string {
	switch rank {
	case expDeIdentified:
		return "export_de_identified"
	case expNoIdentifiers:
		return "export_no_identifiers"
	case expFull:
		return "export_full"
	default:
		return "export_none"
	}
}

// subject is the authenticated data-API caller: the token's
// (user, project) plus the effective per-arm data and export levels
// (Authentication_Authorization_Design.md §4.1).
type subject struct {
	Assignment   *db.Assignment
	User         *db.User
	Project      *db.Project
	projectAdmin bool
	dataLevels   map[int]int // arm_num -> data level rank
	exportLevels map[int]int // arm_num -> export level rank (GD-2)
}

// appliedExportLevel is the lowest export level among the given arms —
// the most protective, per Data_Export_Anonymization_Design.md §4.3. An
// empty arm list means the project has no arms to check; the holder's
// highest level applies (a role-less member or administrator holds full).
func (s *subject) appliedExportLevel(arms []int) int {
	if len(arms) == 0 {
		best := expNone
		for _, r := range s.exportLevels {
			if r > best {
				best = r
			}
		}
		return best
	}
	best := expFull
	for _, a := range arms {
		if r := s.exportLevels[a]; r < best {
			best = r
		}
	}
	return best
}

// hasData reports whether the holder reaches minRank on at least one arm.
// An administrator holds every level on every arm (REQ-AUTH-023).
func (s *subject) hasData(minRank int) bool {
	if s.User.IsAdmin {
		return true
	}
	for _, r := range s.dataLevels {
		if r >= minRank {
			return true
		}
	}
	return false
}

// resolveToken is the data-API token check (Authentication_
// Authorization_Design.md §4.3): the single indexed lookup, the account
// active rule, and the effective levels — all read at call time, so role
// changes and user disabling take effect immediately (REQ-AUTH-033).
func (h *Handler) resolveToken(ctx context.Context, token string) (*subject, error) {
	if token == "" {
		return nil, errInvalidToken
	}
	a, err := h.Store.GetAssignmentByToken(ctx, token)
	if err != nil || a == nil {
		return nil, errInvalidToken
	}
	u, err := h.Store.GetUser(ctx, a.UserID)
	if err != nil || u == nil || !u.Enabled {
		return nil, errInvalidToken
	}
	if !accountActive(u, h.Cfg.AuthInactivityLimitDays) {
		return nil, errInvalidToken
	}
	p, err := h.Store.GetProject(ctx, a.ProjectID)
	if err != nil || p == nil {
		return nil, errInvalidToken
	}
	arms, err := h.Store.ListArms(ctx, p.ID)
	if err != nil {
		return nil, err
	}

	sub := &subject{
		Assignment:   a,
		User:         u,
		Project:      p,
		dataLevels:   map[int]int{},
		exportLevels: map[int]int{},
	}
	// Effective levels (Authentication_Authorization_Design.md §4.1):
	// an administrator or a role-less member holds full permissions on
	// every arm (REQ-AUTH-023, REQ-AUTH-022); otherwise the role's
	// per-arm rows with no implicit access (REQ-AUTH-019).
	full := u.IsAdmin || !a.RoleID.Valid
	for _, arm := range arms {
		if full {
			sub.dataLevels[arm.ArmNum] = lvlEditSurveyResponses
			sub.exportLevels[arm.ArmNum] = expFull
		} else {
			sub.dataLevels[arm.ArmNum] = lvlNoAccess
			sub.exportLevels[arm.ArmNum] = expNone
		}
	}
	if full {
		sub.projectAdmin = true
	} else if a.RoleID.Valid {
		role, err := h.Store.GetRole(ctx, a.RoleID.Int64)
		if err != nil || role == nil {
			return nil, errInvalidToken
		}
		sub.projectAdmin = role.ProjectAdmin
		ra, err := h.Store.ListRoleArms(ctx, role.ID)
		if err != nil {
			return nil, err
		}
		for _, l := range ra {
			sub.dataLevels[l.ArmNum] = dataRank(l.DataAccessLevel)
			sub.exportLevels[l.ArmNum] = exportRank(l.ExportLevel)
		}
	}
	return sub, nil
}

// accountActive is the account active rule of Authentication_
// Authorization_Design.md §4.4 as evaluated on the data API: enabled
// (checked by the caller), not expired (REQ-AUTH-052), not inactive
// (REQ-AUTH-053). The auto-disable write lands with the audit writer;
// rejecting here is the observable contract.
func accountActive(u *db.User, inactivityDays int) bool {
	if u.ValidUntil.Valid && u.ValidUntil.String != "" {
		today := time.Now().UTC().Format("2006-01-02")
		if u.ValidUntil.String < today {
			return false
		}
	}
	if inactivityDays <= 0 || !u.LastLoginAt.Valid || u.LastLoginAt.String == "" {
		return true
	}
	last, err := time.Parse("2006-01-02 15:04:05", u.LastLoginAt.String)
	if err != nil {
		return true // unreadable timestamp: do not disable on it
	}
	return time.Since(last) <= time.Duration(inactivityDays)*24*time.Hour
}
