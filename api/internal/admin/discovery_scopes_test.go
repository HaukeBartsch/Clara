package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// startScope runs the authorization start for provider 1 and returns the scope parameter
// of the address the browser is sent to.
func startScope(t *testing.T, e *env) string {
	t.Helper()
	rec := e.do("POST", "/api/v1/auth/oauth/start", map[string]any{"provider": 1}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	e.decode(rec, &got)
	u, err := url.Parse(got["authorize_url"])
	if err != nil {
		t.Fatalf("authorize_url %q: %v", got["authorize_url"], err)
	}
	return u.Query().Get("scope")
}

// TestOAuthStartSendsConfiguredScopes: the authorization request carries OAUTH2_N_SCOPES
// as configured, and the three default scopes without it (REQ-CFG-039).
func TestOAuthStartSendsConfiguredScopes(t *testing.T) {
	e := newEnv(t)
	e.useIdP(t)
	if got := startScope(t, e); got != "openid email profile" {
		t.Errorf("default scope = %q, want %q", got, "openid email profile")
	}

	e2 := newEnv(t)
	e2.useIdP(t)
	e2.Cfg.OAuth2[0].Scopes = []string{"openid", "email", "profile", "groups"}
	if got := startScope(t, e2); got != "openid email profile groups" {
		t.Errorf("configured scope = %q, want %q", got, "openid email profile groups")
	}
}

// metadataHost serves a provider's discovery document somewhere other than under its
// issuer — the OAUTH2_N_DISCOVERY_URL case (REQ-CFG-040). doc is what it answers at
// path; every other request, the conventional address included, is counted and refused.
type metadataHost struct {
	srv    *httptest.Server
	stray  int32
	status int
}

func newMetadataHost(t *testing.T, path string, doc func() []byte) *metadataHost {
	t.Helper()
	m := &metadataHost{status: http.StatusOK}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			atomic.AddInt32(&m.stray, 1)
			http.NotFound(w, r)
			return
		}
		if m.status != http.StatusOK {
			http.Error(w, "unavailable", m.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(doc())
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// relay fetches the test provider's own document, so the override serves the real
// endpoints and keys and only its address differs.
func relay(t *testing.T, idp *testIdP) func() []byte {
	return func() []byte {
		resp, err := http.Get(idp.issuer() + "/.well-known/openid-configuration")
		if err != nil {
			t.Errorf("relay: %v", err)
			return nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return body
	}
}

// TestDiscoveryURLOverrideSignsIn: with OAUTH2_N_DISCOVERY_URL the metadata comes from that
// address — query included — and a full sign-in verifies against the issuer it names.
func TestDiscoveryURLOverrideSignsIn(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useIdP(t)
	meta := newMetadataHost(t, "/tenant/v2.0/metadata", relay(t, idp))
	e.Cfg.OAuth2[0].DiscoveryURL = meta.srv.URL + "/tenant/v2.0/metadata?p=signin"

	rec := e.do("POST", "/api/v1/auth/oauth/start", map[string]any{"provider": 1}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), idp.issuer()+"/authorize?") {
		t.Errorf("authorize_url does not use the discovered endpoint: %s", rec.Body.String())
	}

	e.mustUser("ada@example.org")
	rec = e.do("POST", "/api/v1/auth/login", oauthLoginBody(t, idp, idp.issuer(), "ada@example.org", nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login through the override: %d %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(&meta.stray); n != 0 {
		t.Errorf("%d requests went to the metadata host outside the configured address", n)
	}
}

// TestDiscoveryURLOverrideKeepsIssuerCheck: a document at the override that names another
// issuer is refused exactly as at the conventional address — the override moves where
// metadata is read, never whom tokens are trusted from (REQ-CFG-040, REQ-AUTH-082).
func TestDiscoveryURLOverrideKeepsIssuerCheck(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useIdP(t)
	meta := newMetadataHost(t, "/metadata.json", func() []byte {
		var doc map[string]any
		_ = json.Unmarshal(relay(t, idp)(), &doc)
		doc["issuer"] = "https://elsewhere.example.org"
		body, _ := json.Marshal(doc)
		return body
	})
	e.Cfg.OAuth2[0].DiscoveryURL = meta.srv.URL + "/metadata.json"

	report, _ := oauthTestWithAdmin(t, e, map[string]string{"code_verifier": "v", "nonce": "n"})
	if report.Stage != stageDiscovery || report.Error != reasonIssuerMismatch {
		t.Errorf("stage/error = %q/%q, want %q/%q", report.Stage, report.Error, stageDiscovery, reasonIssuerMismatch)
	}
	if report.DiscoveryStatus != http.StatusOK {
		t.Errorf("discovery_status = %d, want 200 — the document arrived and was judged", report.DiscoveryStatus)
	}

	rec := e.do("POST", "/api/v1/auth/oauth/start", map[string]any{"provider": 1}, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), reasonIssuerMismatch) {
		t.Errorf("start: %d %s, want 502 %s", rec.Code, rec.Body.String(), reasonIssuerMismatch)
	}
}

// TestDiscoveryURLOverrideReportsItsStatus: the diagnostic observes the override's own
// response, so a document missing there reads as the 404 it was (REQ-AUTH-085).
func TestDiscoveryURLOverrideReportsItsStatus(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useIdP(t)
	meta := newMetadataHost(t, "/metadata.json", relay(t, idp))
	meta.status = http.StatusNotFound
	e.Cfg.OAuth2[0].DiscoveryURL = meta.srv.URL + "/metadata.json"

	report, _ := oauthTestWithAdmin(t, e, map[string]string{"code_verifier": "v", "nonce": "n"})
	if report.Stage != stageDiscovery || report.Error != reasonProviderUnavail {
		t.Errorf("stage/error = %q/%q, want %q/%q", report.Stage, report.Error, stageDiscovery, reasonProviderUnavail)
	}
	if report.DiscoveryStatus != http.StatusNotFound {
		t.Errorf("discovery_status = %d, want 404", report.DiscoveryStatus)
	}
}
