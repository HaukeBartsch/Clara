package db

import (
	"testing"
	"time"
)

// The temporal normalizers are what makes one repository body run against
// both drivers (GD-6, REQ-DB-002): SQLite hands back TEXT, MariaDB hands back
// time.Time with parseTime=true, and a NULL arrives as nil. Each must land in
// the canonical layout, converted to UTC.

func TestDateString(t *testing.T) {
	utc := time.Date(2026, 3, 14, 23, 59, 59, 0, time.UTC)
	east := time.Date(2026, 3, 15, 1, 0, 0, 0, time.FixedZone("UTC+2", 2*3600))

	cases := []struct {
		name string
		in   any
		want string
		ok   bool
	}{
		{"nil is NULL", nil, "", false},
		{"time.Time in UTC", utc, "2026-03-14", true},
		{"time.Time east of UTC normalizes back a day", east, "2026-03-14", true},
		{"text passthrough", "2026-03-14", "2026-03-14", true},
		{"empty text is unset", "", "", false},
		{"bytes from the driver", []byte("2026-03-14"), "2026-03-14", true},
		{"empty bytes are unset", []byte(""), "", false},
		{"nil typed bytes are unset", []byte(nil), "", false},
		{"unexpected type stringifies", 20260314, "20260314", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := dateString(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("dateString(%#v) = %q,%v, want %q,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDatetimeString(t *testing.T) {
	utc := time.Date(2026, 3, 14, 12, 30, 45, 0, time.UTC)
	west := time.Date(2026, 3, 14, 10, 30, 45, 0, time.FixedZone("UTC-2", -2*3600))

	cases := []struct {
		name string
		in   any
		want string
		ok   bool
	}{
		{"nil is NULL", nil, "", false},
		{"time.Time in UTC", utc, "2026-03-14 12:30:45", true},
		{"time.Time west of UTC shifts forward", west, "2026-03-14 12:30:45", true},
		{"text passthrough", "2026-03-14 12:30:45", "2026-03-14 12:30:45", true},
		{"empty text is unset", "", "", false},
		{"bytes from the driver", []byte("2026-03-14 12:30:45"), "2026-03-14 12:30:45", true},
		{"empty bytes are unset", []byte(""), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := datetimeString(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("datetimeString(%#v) = %q,%v, want %q,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// Both normalizers emit the layout their column is declared with (GD-7), so a
// value written by one repository and read back by another never drifts.
func TestTemporalLayoutsRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 5, 4, 0, time.UTC)

	ds, ok := dateString(now)
	if !ok {
		t.Fatalf("dateString(now) reported unset")
	}
	if _, err := time.Parse(dateLayout, ds); err != nil {
		t.Errorf("dateString output %q does not parse as DATE: %v", ds, err)
	}

	dts, ok := datetimeString(now)
	if !ok {
		t.Fatalf("datetimeString(now) reported unset")
	}
	if _, err := time.Parse(datetimeLayout, dts); err != nil {
		t.Errorf("datetimeString output %q does not parse as DATETIME: %v", dts, err)
	}
	// The normalizers are idempotent: what they emit scans back unchanged.
	if again, _ := datetimeString(dts); again != dts {
		t.Errorf("datetimeString is not idempotent: %q then %q", dts, again)
	}
}
