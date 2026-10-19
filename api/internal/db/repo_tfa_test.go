package db

import (
	"context"
	"testing"
	"time"
)

func tfaUser(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id, err := s.CreateUser(ctx, &User{Email: "tfa@example.org", DisplayName: "TFA", Enabled: true})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

func TestGetTFANoRowMeansOff(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	tfa, err := s.GetTFA(ctx, id)
	if err != nil || tfa != nil {
		t.Fatalf("GetTFA = %+v, %v; want nil row (method off)", tfa, err)
	}
	methods, err := s.ListTFAMethods(ctx)
	if err != nil || len(methods) != 0 {
		t.Fatalf("ListTFAMethods = %v, %v; want empty", methods, err)
	}
}

func TestTOTPPendingSecretAndActivation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	if err := s.SetTFAPendingSecret(ctx, id, "SECRET-A"); err != nil {
		t.Fatalf("SetTFAPendingSecret: %v", err)
	}
	tfa, err := s.GetTFA(ctx, id)
	if err != nil || tfa == nil {
		t.Fatalf("GetTFA after pending: %+v, %v", tfa, err)
	}
	if tfa.Method != "off" || !tfa.TotpSecret.Valid || tfa.TotpSecret.String != "SECRET-A" {
		t.Errorf("pending row = %+v, want method off with secret stored", tfa)
	}
	// A second enrollment replaces the pending secret and its watermark.
	if err := s.SetTFAPendingSecret(ctx, id, "SECRET-B"); err != nil {
		t.Fatalf("SetTFAPendingSecret again: %v", err)
	}

	if err := s.ActivateTFA(ctx, id, "totp", `[{"h":"x","used":false}]`); err != nil {
		t.Fatalf("ActivateTFA: %v", err)
	}
	tfa, _ = s.GetTFA(ctx, id)
	if tfa.Method != "totp" || !tfa.EnrolledAt.Valid || tfa.RecoveryCodes == "" {
		t.Errorf("activated row = %+v", tfa)
	}

	methods, err := s.ListTFAMethods(ctx)
	if err != nil || methods[id] != "totp" {
		t.Fatalf("ListTFAMethods = %v, %v; want {%d: totp}", methods, err, id)
	}

	if err := s.ResetTFA(ctx, id); err != nil {
		t.Fatalf("ResetTFA: %v", err)
	}
	tfa, _ = s.GetTFA(ctx, id)
	if tfa != nil {
		t.Errorf("row survived ResetTFA: %+v", tfa)
	}
}

func TestEmailCodeAtOMICConsume(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	if err := s.SetTFAEmailCode(ctx, id, "HASH-1", future()); err != nil {
		t.Fatalf("SetTFAEmailCode: %v", err)
	}
	// Wrong hash does not consume and leaves the pending code intact.
	ok, err := s.ConsumeTFAEmailCode(ctx, id, "nope")
	if err != nil || ok {
		t.Fatalf("ConsumeTFAEmailCode(wrong) = %v, %v; want false", ok, err)
	}
	tfa, _ := s.GetTFA(ctx, id)
	if !tfa.EmailCodeHash.Valid || tfa.EmailCodeHash.String != "HASH-1" {
		t.Fatalf("pending code was disturbed: %+v", tfa)
	}
	// Right hash consumes exactly once.
	if ok, err = s.ConsumeTFAEmailCode(ctx, id, "HASH-1"); err != nil || !ok {
		t.Fatalf("ConsumeTFAEmailCode(right) = %v, %v; want true", ok, err)
	}
	if ok, err = s.ConsumeTFAEmailCode(ctx, id, "HASH-1"); err != nil || ok {
		t.Fatalf("replayed consume = %v, %v; want false", ok, err)
	}
}

func TestEmailCodeExpiry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	expired := time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")
	if err := s.SetTFAEmailCode(ctx, id, "HASH-E", expired); err != nil {
		t.Fatalf("SetTFAEmailCode: %v", err)
	}
	if ok, err := s.ConsumeTFAEmailCode(ctx, id, "HASH-E"); err != nil || ok {
		t.Fatalf("expired code consumed = %v, %v; want false", ok, err)
	}
}

func TestLastStepAndRecoveryCodes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	if err := s.SetTFAPendingSecret(ctx, id, "S"); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateTFA(ctx, id, "totp", `[]`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTFALastStep(ctx, id, 12345); err != nil {
		t.Fatal(err)
	}
	tfa, _ := s.GetTFA(ctx, id)
	if !tfa.TotpLastStep.Valid || tfa.TotpLastStep.Int64 != 12345 {
		t.Errorf("last step = %+v, want 12345", tfa.TotpLastStep)
	}
	if ok, err := s.SetTFARecoveryCodesIfUnchanged(ctx, id, `[{"h":"a","used":true}]`, tfa.RecoveryCodes); err != nil || !ok {
		t.Fatalf("compare-and-swap over the set just read = %v, %v; want written", ok, err)
	}
	tfa, _ = s.GetTFA(ctx, id)
	if tfa.RecoveryCodes != `[{"h":"a","used":true}]` {
		t.Errorf("recovery codes = %q", tfa.RecoveryCodes)
	}
}

// TestAdvanceTFALastStepGrantsOneWriter is the store half of finding F18: the
// watermark write is what accepts a TOTP step, so exactly one request per step can
// be the one that moved it.
func TestAdvanceTFALastStepGrantsOneWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	if err := s.SetTFAPendingSecret(ctx, id, "S"); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateTFA(ctx, id, "totp", `[]`); err != nil {
		t.Fatal(err)
	}
	// A fresh enrollment carries no watermark (NULL), which the first accepted
	// step has to be able to write.
	if ok, err := s.AdvanceTFALastStep(ctx, id, 100); err != nil || !ok {
		t.Fatalf("first advance = %v, %v; want granted", ok, err)
	}
	for _, step := range []int64{100, 99} {
		if ok, err := s.AdvanceTFALastStep(ctx, id, step); err != nil || ok {
			t.Fatalf("advance to %d after 100 = %v, %v; want refused", step, ok, err)
		}
	}
	if ok, err := s.AdvanceTFALastStep(ctx, id, 101); err != nil || !ok {
		t.Fatalf("advance to a newer step = %v, %v; want granted", ok, err)
	}
	tfa, _ := s.GetTFA(ctx, id)
	if tfa.TotpLastStep.Int64 != 101 {
		t.Errorf("last step = %d, want 101", tfa.TotpLastStep.Int64)
	}
}

// TestRecoveryCodesCompareAndSwapLosesTheSecondWriter covers the other half of F18:
// a write over recovery codes lands only while the row still holds what the caller
// read, so racing writers cannot both spend one code and neither can discard the
// other's change.
func TestRecoveryCodesCompareAndSwapLosesTheSecondWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	seed := `[{"h":"a","used":false},{"h":"b","used":false}]`
	if err := s.ActivateTFA(ctx, id, "totp", seed); err != nil {
		t.Fatal(err)
	}
	read, err := s.GetTFA(ctx, id)
	if err != nil || read.RecoveryCodes != seed {
		t.Fatalf("GetTFA = %q, %v; want the seeded set", read.RecoveryCodes, err)
	}

	spentA := `[{"h":"a","used":true},{"h":"b","used":false}]`
	if ok, err := s.SetTFARecoveryCodesIfUnchanged(ctx, id, spentA, seed); err != nil || !ok {
		t.Fatalf("first writer = %v, %v; want written", ok, err)
	}
	// A second request holding the same read finds nothing to write over — it must
	// not be able to restore "a" to unused.
	stale := `[{"h":"a","used":false},{"h":"b","used":true}]`
	if ok, err := s.SetTFARecoveryCodesIfUnchanged(ctx, id, stale, seed); err != nil || ok {
		t.Fatalf("second writer over a stale read = %v, %v; want refused", ok, err)
	}
	tfa, _ := s.GetTFA(ctx, id)
	if tfa.RecoveryCodes != spentA {
		t.Errorf("recovery codes = %q, want the first writer's %q", tfa.RecoveryCodes, spentA)
	}
}

func TestResetTFACascadesWithUser(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := tfaUser(t, s)

	if err := s.ActivateTFA(ctx, id, "totp", `[]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		t.Fatalf("delete user: %v (foreign key cascade not in effect)", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_two_factor`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d tfa rows survived user deletion, want cascade", n)
	}
}

func future() string {
	return time.Now().UTC().Add(10 * time.Minute).Format("2006-01-02 15:04:05")
}
