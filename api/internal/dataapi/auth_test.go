package dataapi

import (
	"database/sql"
	"testing"
	"time"

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

// dataRank maps the stored data-access-level strings to their ascending
// privilege rank (REQ-DB-009).
func TestDataRank(t *testing.T) {
	cases := []struct {
		level string
		want  int
	}{
		{"read_only", lvlReadOnly},
		{"view_edit", lvlViewEdit},
		{"delete", lvlDelete},
		{"edit_survey_responses", lvlEditSurveyResponses},
		{"", lvlNoAccess},
		{"garbage", lvlNoAccess},
	}
	for _, tc := range cases {
		if got := dataRank(tc.level); got != tc.want {
			t.Errorf("dataRank(%q) = %d, want %d", tc.level, got, tc.want)
		}
	}
	// Ranks must be strictly ordered so the min-rank comparisons hold.
	if !(lvlNoAccess < lvlReadOnly && lvlReadOnly < lvlViewEdit &&
		lvlViewEdit < lvlDelete && lvlDelete < lvlEditSurveyResponses) {
		t.Errorf("data-level ranks are not in ascending order")
	}
}

// hasData reports whether the holder reaches minRank on at least one arm; an
// administrator always passes (REQ-AUTH-023).
func TestHasData(t *testing.T) {
	admin := &subject{User: &db.User{IsAdmin: true}}
	if !admin.hasData(lvlEditSurveyResponses) {
		t.Errorf("admin must reach every data level")
	}

	ro := &subject{
		User:       &db.User{IsAdmin: false},
		dataLevels: map[int]int{1: lvlReadOnly},
	}
	if !ro.hasData(lvlReadOnly) {
		t.Errorf("read_only holder should reach read_only")
	}
	if ro.hasData(lvlViewEdit) {
		t.Errorf("read_only holder must not reach view_edit")
	}

	// A higher level on a different arm still satisfies a lower min-rank.
	multi := &subject{
		User:       &db.User{IsAdmin: false},
		dataLevels: map[int]int{1: lvlNoAccess, 2: lvlDelete},
	}
	if !multi.hasData(lvlViewEdit) {
		t.Errorf("delete holder on arm 2 should reach view_edit")
	}
}
