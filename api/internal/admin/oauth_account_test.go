package admin

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// REQ-AUTH-091 on the OAuth2 leg (DEV-AUTH-21): a provider whose assertion Sequence J
// has verified is the authority on who the user is, so a first sign-in creates the
// account — not an administrator, in no project — exactly as a first LDAP sign-in does.

func oauthFirstSignIn(t *testing.T, e *env, idp *testIdP, issuer string, claims map[string]any) *UserObject {
	t.Helper()
	body := loginArgs(claims["email"].(string), idp.mint(claims), "")
	body["provider"] = issuer
	rec := e.do("POST", "/api/v1/auth/login", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var obj UserObject
	e.decode(rec, &obj)
	return &obj
}

func TestOAuthFirstSignInCreatesTheAccount(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	ctx := context.Background()

	obj := oauthFirstSignIn(t, e, idp, provider.Issuer, map[string]any{
		"email": "kari.nordmann@hospital.example.org", "email_verified": true, "name": "Kari Nordmann",
	})
	if obj.Email != "kari.nordmann@hospital.example.org" || obj.DisplayName != "Kari Nordmann" ||
		obj.IsAdmin || !obj.Enabled || obj.AuthSource != "oauth2" || obj.LastLoginAt == nil {
		t.Errorf("user object = %+v", obj)
	}

	link, err := e.Store.GetProviderLinkBySubject(ctx, provider.Issuer, "subject-42")
	if err != nil || link == nil || link.UserID != obj.ID {
		t.Fatalf("link = %+v, err %v; want the new account bound to the subject", link, err)
	}
	stored, err := e.Store.GetUser(ctx, obj.ID)
	if err != nil || stored == nil || stored.PasswordHash.Valid {
		t.Fatalf("stored = %+v, err %v; want an account without a local password", stored, err)
	}
	d := lastAuditDetails(t, e, "user_created")
	if d["automatic"] != true || d["source"] != "oauth2" || d["provider"] != provider.Issuer {
		t.Errorf("user_created details = %v", d)
	}

	// In no project: the dashboard is the information page (REQ-AUTH-028).
	rec := e.do("GET", "/api/v1/users/me/projects", nil, stored)
	var mine []map[string]any
	e.decode(rec, &mine)
	if rec.Code != http.StatusOK || len(mine) != 0 {
		t.Errorf("my projects = %d %v, want none", rec.Code, mine)
	}

	// The next sign-in resolves by subject and creates nothing, whatever the address says.
	before := countUsers(t, e)
	again := oauthFirstSignIn(t, e, idp, provider.Issuer, map[string]any{
		"email": "renamed@hospital.example.org", "email_verified": true,
	})
	if again.ID != obj.ID || countUsers(t, e) != before {
		t.Errorf("second sign-in = account %d with %d accounts; want %d and no new one", again.ID, countUsers(t, e), obj.ID)
	}
}

func TestOAuthFirstSignInNamesTheAccount(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"given and family name", map[string]any{"given_name": "Ola", "family_name": "Nordmann"}, "Ola Nordmann"},
		{"no name claim", map[string]any{}, "ola"},
		{"a name that is not a string", map[string]any{"name": 42}, "ola"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			idp, provider := e.useIdP(t)
			claims := map[string]any{"email": "ola@hospital.example.org", "email_verified": true}
			for k, v := range tc.claims {
				claims[k] = v
			}
			if got := oauthFirstSignIn(t, e, idp, provider.Issuer, claims).DisplayName; got != tc.want {
				t.Errorf("display_name = %q, want %q", got, tc.want)
			}
		})
	}
}

// Sequence J refuses before any account is read or created: an unverified address
// names nobody (REQ-AUTH-078).
func TestOAuthUnverifiedAddressCreatesNothing(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	body := loginArgs("ghost@example.org", idp.mint(map[string]any{"email": "ghost@example.org", "email_verified": false}), "")
	body["provider"] = provider.Issuer
	if rec := e.do("POST", "/api/v1/auth/login", body, nil); rec.Code == http.StatusOK {
		t.Fatalf("an unverified address signed in: %s", rec.Body.String())
	}
	if n := countUsers(t, e); n != 0 {
		t.Errorf("accounts = %d, want none", n)
	}
}

// An administrator-made account whose address differs only in case is that person's:
// the first sign-in binds it rather than creating a second one.
func TestOAuthFirstSignInFindsAnAccountIgnoringCase(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	existing := e.mustUser("Kari.Nordmann@Hospital.example.org")

	obj := oauthFirstSignIn(t, e, idp, provider.Issuer, map[string]any{
		"email": "kari.nordmann@hospital.example.org", "email_verified": true,
	})
	if obj.ID != existing.ID || countUsers(t, e) != 1 {
		t.Errorf("signed in as %d with %d accounts, want the existing account %d", obj.ID, countUsers(t, e), existing.ID)
	}
}

// The case-insensitive match does not get around REQ-AUTH-080: an account already bound
// to another subject at this provider is a conflict, and nothing is created beside it.
func TestOAuthFirstSignInKeepsTheSubjectConflict(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	existing := e.mustUser("Kari@Hospital.example.org")
	if err := e.Store.EnsureProviderLink(context.Background(), provider.Issuer, "someone-else", existing.ID, time.Now()); err != nil {
		t.Fatalf("EnsureProviderLink: %v", err)
	}

	body := loginArgs("kari@hospital.example.org", idp.mint(map[string]any{"email": "kari@hospital.example.org", "email_verified": true}), "")
	body["provider"] = provider.Issuer
	rec := e.do("POST", "/api/v1/auth/login", body, nil)
	if rec.Code != http.StatusForbidden || decodeError(t, rec) != "subject_conflict" {
		t.Errorf("= %d %s, want 403 subject_conflict", rec.Code, rec.Body.String())
	}
	if countUsers(t, e) != 1 {
		t.Error("an account was created beside the conflicting one")
	}
}
