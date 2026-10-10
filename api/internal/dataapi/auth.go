package dataapi

import (
	"context"
	"errors"
	"time"

	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// errInvalidToken is the uniform 401 failure of the data API: a missing,
// unknown, or rejected token all render as "Invalid token" so callers
// cannot probe for valid ones (REQ-AUTH-032, REQ-API-011).
var errInvalidToken = errors.New("invalid token")

// errSurveySubmitted is a survey link that already carried its one submission
// (REQ-API-145). It is deliberately kept apart from errInvalidToken: unknown
// and revoked tokens stay indistinguishable from one another (REQ-AUTH-032),
// while the respondent who reopens a used link needs to hear that their answer
// arrived — handler.go renders this as 410, the single place where anything
// about a link's fate is disclosed to its holder.
var errSurveySubmitted = errors.New("survey link already submitted")

// The two ladders, re-exported from authz so this package reads the same way as
// the administration API does. They are aliases and nothing else: one ranking
// exists, in authz (REQ-DB-009 as revised, REQ-AUTH-017) — a second copy here is
// what let the two surfaces disagree once before.
const (
	lvlReadOnly = authz.RankReadOnly
	lvlViewEdit = authz.RankViewEdit

	expNone          = authz.RankExportNone
	expDeIdentified  = authz.RankExportDeIdentified
	expNoIdentifiers = authz.RankExportNoIdentifiers
	expFull          = authz.RankExportFull
)

// exportName is authz's, aliased for the audit details of this surface.
var exportName = authz.ExportName

// subject is the authenticated data-API caller: the token's (user, project)
// plus the effective levels resolved once by authz (§4.1) — arm defaults and
// the per-(instrument, event) grants on top of them (REQ-AUTH-069). A
// survey-link caller has Link and Instrument set and no account at all — User
// and Assignment are nil there (§3.10), so nothing may dereference them, and
// Levels stays empty: a link holds no permissions of its own.
type subject struct {
	Assignment *db.Assignment
	User       *db.User
	Project    *db.Project
	Levels     *authz.Levels

	Link       *db.SurveyLink // set only for a survey-link token (§3.10)
	Instrument string         // the link's instrument name
}

// isLink reports whether the caller authenticated with a survey-link token
// rather than a project API token (API_Endpoints_Design.md §3.10).
func (s *subject) isLink() bool { return s.Link != nil }

// actorUserID and actorEmail are the audit columns for the caller: the
// account behind a project token, unset for an anonymous survey submission
// (the link itself travels in the fixed token column, REQ-AUTH-041).
func (s *subject) actorUserID() int64 {
	if s.isLink() {
		return 0 // unset: audit_events.user_id is nullable
	}
	return s.User.ID
}

func (s *subject) actorEmail() string {
	if s.isLink() {
		return ""
	}
	return s.User.Email
}

// hasData reports whether the holder reaches minRank somewhere — on an arm
// default or on any pair grant. An administrator holds every level on every
// arm and pair (REQ-AUTH-023) and a role-less member everything else
// (REQ-AUTH-022), both of which authz already folded into Levels; a survey
// link holds no data level at all, since what it may do is fixed per call by
// API_Endpoints_Design.md §3.10 rather than by a grant.
func (s *subject) hasData(minRank int) bool {
	if s.isLink() {
		return false
	}
	return s.Levels.AnyData(minRank)
}

// pairAt resolves one (instrument, event) pair's access — the unit every
// data-API decision is made at since GD-2 was revised (REQ-AUTH-069). armNum is
// the arm of the event, used only when the pair carries no grant of its own;
// pass 0 where the project has no events at all, which leaves the caller with
// the arm-default reading the row-level checks apply.
func (s *subject) pairAt(eventID, instrumentID int64, armNum int) authz.PairLevels {
	if s.isLink() {
		return authz.PairLevels{}
	}
	return s.Levels.PairOrArmDefault(eventID, instrumentID, armNum)
}

// appliedExportLevel is the lowest export level among the given arms — the most
// protective reading of a mixed request. An empty arm list means the project has
// no arms to check, so the holder's highest level applies (a role-less member or
// an administrator holds full).
//
// OUTSTANDING (§4.3 of Data_Export_Anonymization_Design.md, revised 2026-10-03):
// sensitivity is now held per (instrument, event) pair, so the normative shape is
// one file carrying export_full for one instrument next to hashed columns for
// another, with a pair at export_none contributing no columns instead of failing
// the call. Until that lands in the exporter, every column of the response is
// rendered at the strictest level in scope and an unpermitted arm still answers
// 403 — stricter than entitled, never looser.
func (s *subject) appliedExportLevel(arms []int) int {
	if len(arms) == 0 {
		best := expNone
		for _, r := range s.Levels.Export {
			if r2 := authz.ExportRank(r); r2 > best {
				best = r2
			}
		}
		if s.Levels.Unrestricted {
			return expFull
		}
		return best
	}
	best := expFull
	for _, a := range arms {
		if r := authz.ExportRank(s.Levels.Export[a]); r < best {
			best = r
		}
	}
	return best
}

// canDeleteValues reports whether the holder may clear stored values anywhere in
// the project at all. The right is held per (instrument, event) pair
// (REQ-AUTH-018), so this is only the cheap upfront no — the per-record gate of
// §3.8 decides each record on its own populated pairs, all of them or none
// (REQ-API-036).
func (s *subject) canDeleteValues() bool {
	if s.isLink() {
		return false
	}
	if s.Levels.Unrestricted {
		return true
	}
	for _, g := range s.Levels.Pairs {
		if g.DeleteValues {
			return true
		}
	}
	return false
}

// resolveToken is the data-API token check (Authentication_
// Authorization_Design.md §4.3): the single indexed lookup, the account
// active rule, and the effective levels — all read at call time, so role
// changes and user disabling take effect immediately (REQ-AUTH-033). A
// token that is not a project API token may still be a survey link
// (§3.10); both misses render as the same uniform 401.
func (h *Handler) resolveToken(ctx context.Context, token string) (*subject, error) {
	if token == "" {
		return nil, errInvalidToken
	}
	a, err := h.Store.GetAssignmentByToken(ctx, token)
	if err != nil {
		return nil, errInvalidToken
	}
	if a == nil {
		return h.resolveSurveyLink(ctx, token)
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
	// Effective levels from the assignment this lookup already produced — one
	// resolution of §4.1 for both surfaces, arm defaults and pair grants alike
	// (REQ-AUTH-069). A role that vanished resolves to no access, not to the
	// full rights of a role-less member.
	lv, err := authz.EffectiveForAssignment(ctx, h.Store, u, p.ID, a)
	if err != nil {
		return nil, err
	}
	return &subject{Assignment: a, User: u, Project: p, Levels: lv}, nil
}

// resolveSurveyLink admits a survey-link token (API_Endpoints_Design.md
// §3.10): one indexed lookup on survey_links.token, the revocation check
// (a revoked link is rejected on every call, REQ-AUTH-040), the spent-link check
// (one submission per link, REQ-API-145), and the project, instrument and event
// the link names. Levels stay empty — a link holds no permissions; the
// dispatcher grants the two calls of §3.10 and nothing else (REQ-API-083,
// REQ-AUTH-039).
func (h *Handler) resolveSurveyLink(ctx context.Context, token string) (*subject, error) {
	link, err := h.Store.GetSurveyLinkByToken(ctx, token)
	if err != nil || link == nil || link.Revoked {
		return nil, errInvalidToken
	}
	if link.CollectedAt.Valid {
		// Spent. The save that stamped this link stored its one submission
		// (REQ-DB-041, REQ-API-145); the answer is 410, not the 401 an unknown
		// or revoked token draws, so a respondent reopening the URL learns that
		// what they sent reached the study.
		return nil, errSurveySubmitted
	}
	p, err := h.Store.GetProject(ctx, link.ProjectID)
	if err != nil || p == nil {
		return nil, errInvalidToken
	}
	ins, err := h.Store.GetInstrument(ctx, link.InstrumentID)
	if err != nil || ins == nil || ins.ProjectID != p.ID {
		return nil, errInvalidToken
	}
	return &subject{
		Project:    p,
		Link:       link,
		Instrument: ins.Name,
		Levels:     &authz.Levels{Data: map[int]string{}, Export: map[int]string{}, Pairs: map[authz.Pair]authz.PairLevels{}},
	}, nil
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
