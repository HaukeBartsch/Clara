package dataapi

import (
	"database/sql"
	"testing"
	"time"

	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// nullStr builds a valid sql.NullString, the shape the store hands back.
func nullStr(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

// accountActive is the account-active rule evaluated on the data API
// (Authentication_Authorization_Design.md §4.4): not expired (REQ-AUTH-052)
// and not inactive (REQ-AUTH-053). It is pure, so it is tested directly.
func TestAccountActive(t *testing.T) {
	day := func(ago int) string {
		return time.Now().UTC().AddDate(0, 0, -ago).Format("2006-01-02 15:04:05")
	}
	date := func(offset int) string {
		return time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
	}
	cases := []struct {
		name     string
		u        *db.User
		inactive int // AuthInactivityLimitDays
		want     bool
	}{
		{"no expiry, no inactivity limit", &db.User{Enabled: true}, 0, true},
		{"indefinite (nil valid_until)", &db.User{Enabled: true}, 30, true},
		{"not yet expired", &db.User{Enabled: true, ValidUntil: nullStr(date(+1))}, 0, true},
		{"already expired", &db.User{Enabled: true, ValidUntil: nullStr(date(-1))}, 0, false},
		{"inactivity limit off", &db.User{Enabled: true, LastLoginAt: nullStr(day(999))}, 0, true},
		{"active within window", &db.User{Enabled: true, LastLoginAt: nullStr(day(1))}, 30, true},
		{"inactive past window", &db.User{Enabled: true, LastLoginAt: nullStr(day(31))}, 30, false},
		{"unreadable timestamp is not disabling",
			&db.User{Enabled: true, LastLoginAt: nullStr("not-a-date")}, 30, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountActive(tc.u, tc.inactive); got != tc.want {
				t.Errorf("accountActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// DataRank orders the revised ladder (REQ-DB-009 as amended 2026-10-03): it ends
// at view_edit, and the two rungs that used to sit above it rank as view_edit —
// their extra privilege is an explicit right on a grant now (REQ-AUTH-018/070).
func TestDataRank(t *testing.T) {
	cases := []struct {
		level string
		want  int
	}{
		{"read_only", authz.RankReadOnly},
		{"view_edit", authz.RankViewEdit},
		{"delete", authz.RankViewEdit},                // retired rung, ranked as view_edit
		{"edit_survey_responses", authz.RankViewEdit}, // retired rung, ranked as view_edit
		{"", authz.RankNoAccess},
		{"garbage", authz.RankNoAccess},
	}
	for _, tc := range cases {
		if got := authz.DataRank(tc.level); got != tc.want {
			t.Errorf("DataRank(%q) = %d, want %d", tc.level, got, tc.want)
		}
	}
	if !(authz.RankNoAccess < authz.RankReadOnly && authz.RankReadOnly < authz.RankViewEdit) {
		t.Errorf("data-level ranks are not in ascending order")
	}
}

// hasData reports whether the holder reaches minRank on any arm default or pair
// grant. What is_admin and a role-less member hold is already in their Levels
// (REQ-AUTH-022/023), so the subject carries no special case for either.
func TestHasData(t *testing.T) {
	full := &subject{User: &db.User{}, Levels: &authz.Levels{
		Data: map[int]string{1: "view_edit"}, Unrestricted: true,
	}}
	if !full.hasData(authz.RankViewEdit) {
		t.Errorf("an unrestricted member must reach every data level")
	}

	ro := &subject{User: &db.User{}, Levels: &authz.Levels{Data: map[int]string{1: "read_only"}}}
	if !ro.hasData(authz.RankReadOnly) {
		t.Errorf("read_only holder should reach read_only")
	}
	if ro.hasData(authz.RankViewEdit) {
		t.Errorf("read_only holder must not reach view_edit")
	}

	// A pair grant reaching higher than every arm default counts (REQ-AUTH-069).
	pairOnly := &subject{User: &db.User{}, Levels: &authz.Levels{
		Data:  map[int]string{1: "read_only"},
		Pairs: map[authz.Pair]authz.PairLevels{{EventID: 7, InstrumentID: 3}: {Data: "view_edit"}},
	}}
	if !pairOnly.hasData(authz.RankViewEdit) {
		t.Errorf("a view_edit pair grant must reach view_edit where every arm is read_only")
	}

	// A survey link holds no data level at all (REQ-AUTH-039).
	link := &subject{Link: &db.SurveyLink{}, Levels: &authz.Levels{Data: map[int]string{1: "view_edit"}}}
	if link.hasData(authz.RankReadOnly) {
		t.Errorf("a survey link must hold no data level")
	}
}
