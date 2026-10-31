package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestUserRegionColumns covers the three users columns of REQ-DB-052: the user's own
// choice wins over the automatic region and clearing it brings the automatic one back;
// a placeholder only lands on an account without any region; a provider region replaces
// a placeholder but never the user's own choice.
func TestUserRegionColumns(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	id := seedUser(t, s, ctx, "region@example.org")

	read := func() *User {
		t.Helper()
		u, err := s.GetUser(ctx, id)
		if err != nil || u == nil {
			t.Fatalf("GetUser: %v", err)
		}
		return u
	}
	if u := read(); u.EffectiveRegion() != "" || u.PreferredRegion.Valid || u.AutomaticRegion.Valid {
		t.Fatalf("new user region = %+v, want none", u)
	}

	placeholder := func(region string) bool {
		t.Helper()
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()
		set, err := s.SetRegionPlaceholderTx(ctx, tx, id, region)
		if err != nil {
			t.Fatalf("SetRegionPlaceholderTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		return set
	}
	if !placeholder("Ahus") {
		t.Fatal("placeholder on an account without a region was not set")
	}
	if u := read(); u.EffectiveRegion() != "Ahus" || u.AutomaticRegionSource.String != RegionSourceAssignment {
		t.Fatalf("after placeholder: %+v", u)
	}
	if placeholder("OUS") {
		t.Error("a second placeholder replaced the first, want the first to stay")
	}

	if err := s.SetProviderRegion(ctx, id, "SUN"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}
	if u := read(); u.EffectiveRegion() != "SUN" || u.AutomaticRegionSource.String != RegionSourceProvider {
		t.Fatalf("after provider region: %+v", u)
	}

	setOwn := func(region sql.NullString) {
		t.Helper()
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()
		if err := s.SetPreferredRegionTx(ctx, tx, id, region); err != nil {
			t.Fatalf("SetPreferredRegionTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	setOwn(sql.NullString{String: "SiV", Valid: true})
	if err := s.SetProviderRegion(ctx, id, "ST"); err != nil {
		t.Fatalf("SetProviderRegion: %v", err)
	}
	if u := read(); u.EffectiveRegion() != "SiV" || u.AutomaticRegion.String != "ST" {
		t.Fatalf("own choice = %+v, want SiV to win over the provider's ST", u)
	}
	setOwn(sql.NullString{})
	if u := read(); u.EffectiveRegion() != "ST" {
		t.Errorf("after Automatic: effective = %q, want the kept automatic ST", u.EffectiveRegion())
	}
}

// TestRoleSightings covers RecordRoleSightings and MappedRegions (REQ-DB-053,
// REQ-AUTH-088): a new value enters undecided, each sighting counts and moves the last
// sighting, a value entered ahead of time gets its first sighting, and only reviewed rows
// that name a region map.
func TestRoleSightings(t *testing.T) {
	s := migrateTestStore(t)
	ctx := context.Background()
	const issuer = "https://idp.example.org"
	first := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	later := first.Add(48 * time.Hour)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := s.CreateProviderRoleRegionTx(ctx, tx, &ProviderRoleRegion{Issuer: issuer, RoleValue: "ahead",
		Region: sql.NullString{String: "Ahus", Valid: true}}); err != nil {
		t.Fatalf("CreateProviderRoleRegionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := s.RecordRoleSightings(ctx, issuer, []string{"seen", "ahead"}, first); err != nil {
		t.Fatalf("RecordRoleSightings: %v", err)
	}
	if err := s.RecordRoleSightings(ctx, issuer, []string{"seen"}, later); err != nil {
		t.Fatalf("RecordRoleSightings: %v", err)
	}
	if err := s.RecordRoleSightings(ctx, "https://other.example.org", []string{"seen"}, later); err != nil {
		t.Fatalf("RecordRoleSightings other issuer: %v", err)
	}

	seen, err := s.GetProviderRoleRegionByValue(ctx, issuer, "seen")
	if err != nil || seen == nil {
		t.Fatalf("GetProviderRoleRegionByValue: %v", err)
	}
	if seen.Reviewed || seen.Region.Valid || seen.SeenCount != 2 {
		t.Errorf("seen = %+v, want undecided and counted twice", seen)
	}
	if seen.FirstSeenAt.String != "2026-10-01 08:00:00" || seen.LastSeenAt.String != "2026-10-03 08:00:00" {
		t.Errorf("seen sightings = %q … %q", seen.FirstSeenAt.String, seen.LastSeenAt.String)
	}
	ahead, _ := s.GetProviderRoleRegionByValue(ctx, issuer, "ahead")
	if !ahead.Reviewed || ahead.Region.String != "Ahus" || ahead.SeenCount != 1 || ahead.FirstSeenAt.String != "2026-10-01 08:00:00" {
		t.Errorf("ahead = %+v, want its decision kept and its first sighting recorded", ahead)
	}

	rows, err := s.ListProviderRoleRegions(ctx)
	if err != nil {
		t.Fatalf("ListProviderRoleRegions: %v", err)
	}
	if len(rows) != 3 || rows[len(rows)-1].RoleValue != "ahead" {
		t.Errorf("list = %+v, want three rows with the reviewed one last", rows)
	}

	mapped, err := s.MappedRegions(ctx, issuer, []string{"seen", "ahead", "never"})
	if err != nil {
		t.Fatalf("MappedRegions: %v", err)
	}
	if len(mapped) != 1 || mapped["ahead"] != "Ahus" {
		t.Errorf("mapped = %v, want only ahead → Ahus", mapped)
	}

	// "Names no region" is reviewed but maps nothing.
	tx, _ = s.DB.BeginTx(ctx, nil)
	if err := s.UpdateProviderRoleRegionTx(ctx, tx, seen.ID, sql.NullString{}); err != nil {
		t.Fatalf("UpdateProviderRoleRegionTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got, _ := s.GetProviderRoleRegion(ctx, seen.ID); !got.Reviewed || got.Region.Valid {
		t.Errorf("after decision = %+v, want reviewed with no region", got)
	}
	if mapped, _ := s.MappedRegions(ctx, issuer, []string{"seen"}); len(mapped) != 0 {
		t.Errorf("mapped = %v, want nothing for a value that names no region", mapped)
	}
}
