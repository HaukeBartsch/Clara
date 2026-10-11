package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// The session epoch (REQ-AUTH-074): the API keeps no sessions (GD-1), so it
// ends them by raising a per-account counter the web layer must present on
// every call. These tests pin which writes raise it — every credential write,
// every disable, an administrator's two-factor reset, a reported replay — and
// which do not, and that the acting session gets the new value back wherever
// it is the one that made the change.

// epoch reads the account's stored session epoch.
func (e *env) epoch(userID int64) int64 {
	e.t.Helper()
	got, err := e.Store.SessionEpoch(context.Background(), userID)
	if err != nil {
		e.t.Fatalf("SessionEpoch(%d): %v", userID, err)
	}
	return got
}

// answeredEpoch decodes the session_epoch attribute of a response body.
func answeredEpoch(t *testing.T, body []byte) int64 {
	t.Helper()
	var v struct {
		SessionEpoch *int64 `json:"session_epoch"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if v.SessionEpoch == nil {
		t.Fatalf("body %s carries no session_epoch", body)
	}
	return *v.SessionEpoch
}

func TestLoginAnswersTheSessionEpoch(t *testing.T) {
	e := newEnv(t)
	member := e.mustLocalUser("member@example.org", "right-password-1")
	if _, err := e.Store.DB.Exec(`UPDATE users SET session_epoch = 4 WHERE id = ?`, member.ID); err != nil {
		t.Fatalf("seed epoch: %v", err)
	}

	rec := e.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": member.Email, "source": "local", "password": "right-password-1"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := answeredEpoch(t, rec.Body.Bytes()); got != 4 {
		t.Errorf("login session_epoch = %d, want the stored 4", got)
	}
	if got := e.epoch(member.ID); got != 4 {
		t.Errorf("stored epoch = %d after login, want 4 — logging in revokes nothing", got)
	}
}

func TestChangeMyPasswordRaisesAndAnswersTheEpoch(t *testing.T) {
	e := newEnv(t)
	member := e.mustLocalUser("member@example.org", "old-password-1")

	rec := e.do(http.MethodPut, "/api/v1/users/me/password",
		map[string]string{"current_password": "old-password-1", "new_password": "fresh-water-91"}, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("change = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var answer struct {
		OK bool `json:"ok"`
	}
	e.decode(rec, &answer)
	if !answer.OK {
		t.Errorf("body = %s, want ok:true kept beside the epoch", rec.Body.String())
	}
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("stored epoch = %d, want 1 — the change ends the other sessions", got)
	}
	if got := answeredEpoch(t, rec.Body.Bytes()); got != 1 {
		t.Errorf("answered epoch = %d, want 1 — the acting session adopts it", got)
	}

	// A rejected proof changes nothing.
	rec = e.do(http.MethodPut, "/api/v1/users/me/password",
		map[string]string{"current_password": "wrong", "new_password": "fresh-water-92"}, member)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong proof = %d, want 401", rec.Code)
	}
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("stored epoch = %d after a rejected change, want 1", got)
	}
}

func TestSetupCompletionRaisesTheEpoch(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustLocalUser("member@example.org", "old-password-1")
	tokenOf := e.captureMail()

	e.do(http.MethodPost, "/api/v1/auth/password-reset/request", map[string]string{"email": member.Email}, nil)
	rec := e.do(http.MethodPost, "/api/v1/auth/password-reset/complete",
		map[string]string{"token": tokenOf(), "password": "a-brand-new-pw"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset complete = %d %s", rec.Code, rec.Body.String())
	}
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("epoch after reset = %d, want 1", got)
	}

	e.do(http.MethodPost, "/api/v1/users/"+itoa(member.ID)+"/invite", nil, admin)
	rec = e.do(http.MethodPost, "/api/v1/auth/invite/complete",
		map[string]string{"token": tokenOf(), "password": "another-new-pw"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite complete = %d %s", rec.Code, rec.Body.String())
	}
	if got := e.epoch(member.ID); got != 2 {
		t.Errorf("epoch after invite completion = %d, want 2", got)
	}
}

func TestUpdateUserRaisesTheEpochOnCredentialAndDisable(t *testing.T) {
	cases := []struct {
		name  string
		body  map[string]any
		raise bool
	}{
		{"password set", map[string]any{"password": "set-by-admin-1"}, true},
		{"password cleared", map[string]any{"password": ""}, true},
		{"disabled", map[string]any{"enabled": false}, true},
		{"validity changed", map[string]any{"valid_days": 30}, false},
		{"already enabled", map[string]any{"enabled": true}, false},
		{"made administrator", map[string]any{"is_admin": true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			admin := e.mustAdmin("admin@example.org")
			member := e.mustLocalUser("member@example.org", "old-password-1")

			rec := e.do(http.MethodPut, "/api/v1/users/"+itoa(member.ID), tc.body, admin)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT = %d %s, want 200", rec.Code, rec.Body.String())
			}
			want := int64(0)
			if tc.raise {
				want = 1
			}
			if got := e.epoch(member.ID); got != want {
				t.Errorf("stored epoch = %d, want %d", got, want)
			}
			if got := answeredEpoch(t, rec.Body.Bytes()); got != want {
				t.Errorf("user object session_epoch = %d, want %d", got, want)
			}
		})
	}
}

func TestDisabledThenReenabledKeepsTheRaisedEpoch(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustUser("member@example.org")

	e.do(http.MethodPut, "/api/v1/users/"+itoa(member.ID), map[string]any{"enabled": false}, admin)
	rec := e.do(http.MethodPut, "/api/v1/users/"+itoa(member.ID), map[string]any{"enabled": true}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable = %d %s", rec.Code, rec.Body.String())
	}
	// Re-enabling must not hand an old session its epoch back.
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("epoch after disable + re-enable = %d, want 1", got)
	}
}

func TestTFAResetRaisesAndAnswersTheEpoch(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	member := e.mustUser("member@example.org")

	rec := e.do(http.MethodPost, "/api/v1/users/"+itoa(member.ID)+"/tfa/reset", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", rec.Code, rec.Body.String())
	}
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("stored epoch = %d, want 1", got)
	}
	if got := answeredEpoch(t, rec.Body.Bytes()); got != 1 {
		t.Errorf("answered epoch = %d, want 1", got)
	}
	if got := e.epoch(admin.ID); got != 0 {
		t.Errorf("acting administrator's epoch = %d, want 0 — only the target's sessions end", got)
	}
}

func TestSessionReplayRaisesTheEpochAndAudits(t *testing.T) {
	e := newEnv(t)
	member := e.mustUser("member@example.org")
	other := e.mustUser("other@example.org")

	rec := e.do(http.MethodPost, "/api/v1/auth/session-replay", nil, member)
	if rec.Code != http.StatusOK {
		t.Fatalf("session-replay = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := e.epoch(member.ID); got != 1 {
		t.Errorf("epoch = %d, want 1 — every session of the account ends", got)
	}
	if got := e.epoch(other.ID); got != 0 {
		t.Errorf("another account's epoch = %d, want 0", got)
	}

	var userID int64
	var details string
	if err := e.Store.DB.QueryRow(
		`SELECT user_id, details FROM audit_events WHERE event_type = 'session_replay_detected'`).
		Scan(&userID, &details); err != nil {
		t.Fatalf("session_replay_detected row: %v", err)
	}
	if userID != member.ID {
		t.Errorf("audit user_id = %d, want %d", userID, member.ID)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(details), &payload); err != nil {
		t.Fatalf("details %q: %v", details, err)
	}
	if _, ok := payload["ip"]; !ok || len(payload) != 1 {
		t.Errorf("details = %v, want exactly the presenting address", payload)
	}

	// The boundary always supplies an actor; without one the handler refuses.
	if rec := e.do(http.MethodPost, "/api/v1/auth/session-replay", nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("no actor = %d, want 403", rec.Code)
	}
}
