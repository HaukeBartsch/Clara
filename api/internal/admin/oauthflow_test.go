package admin

// The Control Panel diagnostic report for an OAuth/OIDC provider (REQ-AUTH-085,
// Authentication_Authorization_Design.md §2.11): which stage a provider test stopped at, the
// status that stage actually returned, and which claim sets survive to be shown.
//
// These cases exist because the report used to answer three questions with one number each and
// no words: an issuer pointing at a root that serves no discovery document, a host behind a
// firewall, and a provider that refused the assertion all looked alike. That is precisely the
// debugging an administrator standing in front of PingFederate needs to tell configuration from
// outage from claim release (the behaviours .env.example documents for it).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"csms/api/internal/config"
)

// diagnosticReport is one read of the report the web layer renders. Typed rather than a map so
// that a renamed or dropped field fails a test instead of silently rendering as nothing.
type diagnosticReport struct {
	OK              bool              `json:"ok"`
	Stage           string            `json:"stage"`
	Error           string            `json:"error"`
	Issuer          string            `json:"issuer"`
	DiscoveryStatus int               `json:"discovery_status"`
	TokenStatus     int               `json:"token_status"`
	UserinfoStatus  int               `json:"userinfo_status"`
	Endpoints       map[string]string `json:"endpoints"`
	UserinfoClaims  map[string]any    `json:"userinfo_claims"`
	IDTokenClaims   map[string]any    `json:"id_token_claims"`
}

// oauthTestWithAdmin runs one diagnostic against provider 1 as an administrator, which is the
// only role the endpoint answers (REQ-AUTH-085 keeps it administrator-only). It returns the
// decoded report and the raw body, since one expectation is about bytes rather than fields.
func oauthTestWithAdmin(t *testing.T, e *env, flow map[string]string) (diagnosticReport, string) {
	t.Helper()
	rec := e.do("POST", "/api/v1/auth/oauth/test", map[string]any{
		"provider": 1, "code": "test-code",
		"code_verifier": flow["code_verifier"], "nonce": flow["nonce"],
	}, e.mustAdmin("admin@example.org"))
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostic status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var report diagnosticReport
	e.decode(rec, &report)
	return report, rec.Body.String()
}

// providerAtIssuer names one configured provider pointing at an arbitrary issuer URL, for the
// cases where the point is what discovery answers rather than what it signs.
func providerAtIssuer(issuer string) config.OAuth2Provider {
	return config.OAuth2Provider{
		N: 1, Issuer: issuer, ClientID: "clara", ClientSecret: "secret",
		RedirectURI: "https://csms.example.org/auth/callback",
		EmailAttr:   "email", SubjectAttr: "sub", IDTokenAlg: "RS256",
	}
}

// TestOAuthDiagnosticReportsDiscoveryStatusAndStage is the PingFederate wrong-issuer-root case:
// the configured issuer serves no discovery document, and the report says so with the status
// that came back (REQ-AUTH-082's named provider error, carried to the page by REQ-AUTH-085).
func TestOAuthDiagnosticReportsDiscoveryStatusAndStage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	e := newEnv(t)
	e.Cfg.OAuth2 = []config.OAuth2Provider{providerAtIssuer(srv.URL + "/idp")}

	// No transaction is needed: the flow dies before a code is ever redeemed.
	report, _ := oauthTestWithAdmin(t, e, map[string]string{"code_verifier": "v", "nonce": "n"})

	if report.OK {
		t.Errorf("ok = true, want false for a provider with no discovery document")
	}
	if report.Stage != stageDiscovery {
		t.Errorf("stage = %q, want %q", report.Stage, stageDiscovery)
	}
	if report.Error != reasonProviderUnavail {
		t.Errorf("error = %q, want %q", report.Error, reasonProviderUnavail)
	}
	if report.DiscoveryStatus != http.StatusNotFound {
		t.Errorf("discovery_status = %d, want the 404 that answered", report.DiscoveryStatus)
	}
}

// TestOAuthDiagnosticSeparatesNoAnswerFromBadAnswer is the other half of the pair: a host that
// never replies keeps discovery at 0 while one that replies badly carries its status. Before
// the status was observed, both read "Discovery HTTP 0" and the administrator had a log search
// ahead of them instead of a diagnosis.
func TestOAuthDiagnosticSeparatesNoAnswerFromBadAnswer(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	downURL := down.URL
	down.Close() // the address stands, nothing listens

	e := newEnv(t)
	e.Cfg.OAuth2 = []config.OAuth2Provider{providerAtIssuer(downURL + "/idp")}

	report, _ := oauthTestWithAdmin(t, e, map[string]string{"code_verifier": "v", "nonce": "n"})
	if report.Stage != stageDiscovery || report.Error != reasonProviderUnavail {
		t.Fatalf("stage/error = %q/%q, want %q/%q",
			report.Stage, report.Error, stageDiscovery, reasonProviderUnavail)
	}
	if report.DiscoveryStatus != 0 {
		t.Errorf("discovery_status = %d, want 0 for a host that never answered", report.DiscoveryStatus)
	}
}

// TestOAuthDiagnosticNamesIssuerMismatch covers the misconfiguration .env.example warns about in
// particular: OAUTH2_n_ISSUER must be copied from the discovery document, because the value
// inside it is what the check compares against. It has to read differently from an outage — the
// provider is reachable and healthy, the configuration is not.
func TestOAuthDiagnosticNamesIssuerMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, discoveryPathSuffix) {
			http.NotFound(w, r)
			return
		}
		self := "https://" + r.Host // the endpoints are never fetched; only `issuer` is judged
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://idp.example.org/idp", // not this server
			"authorization_endpoint": self + "/as/authorization.oauth2",
			"token_endpoint":         self + "/as/token.oauth2",
			"userinfo_endpoint":      self + "/idp/userinfo.openid",
			"jwks_uri":               self + "/as/jwks.json",
		})
	}))
	t.Cleanup(srv.Close)

	e := newEnv(t)
	e.Cfg.OAuth2 = []config.OAuth2Provider{providerAtIssuer(srv.URL)}

	report, _ := oauthTestWithAdmin(t, e, map[string]string{"code_verifier": "v", "nonce": "n"})
	if report.Error != reasonIssuerMismatch {
		t.Errorf("error = %q, want %q", report.Error, reasonIssuerMismatch)
	}
	if report.Stage != stageDiscovery {
		t.Errorf("stage = %q, want %q", report.Stage, stageDiscovery)
	}
	// The document arrived and was read: the refusal is about what it says, which is why a
	// 200 here and a 404 in the case above are the pair that names the fault.
	if report.DiscoveryStatus != http.StatusOK {
		t.Errorf("discovery_status = %d, want 200 — the document was fetched", report.DiscoveryStatus)
	}
}

// TestOAuthDiagnosticKeepsUserinfoClaimsWhenVerificationFails is the split claim release
// REQ-AUTH-078 admits: PingFederate sets the ID-token release and `/idp/userinfo.openid`
// independently, so the run that fails verification still has to show what userinfo answered —
// that answer is the evidence about the provider's configuration. The report carries it with
// `ok:false`, which is why the page must render claims on a failed run too.
func TestOAuthDiagnosticKeepsUserinfoClaimsWhenVerificationFails(t *testing.T) {
	e := newEnv(t)
	idp, _ := e.useIdP(t)
	flow := beginOAuthTransaction(t, e, idp)

	// The ID token releases an address it does not attest; userinfo releases the same address
	// with `email_verified` true — a working deployment for login through the fallback, and a
	// refusal when the address came from the ID token half.
	idp.tokenClaims = map[string]any{"nonce": flow["nonce"], "email": "split@example.org", "email_verified": false}
	idp.userInfoClaims = map[string]any{
		"sub": "subject-42", "email": "split@example.org", "email_verified": true,
		"groups": []string{"nephrology"},
	}

	report, body := oauthTestWithAdmin(t, e, flow)
	if report.OK {
		t.Errorf("ok = true, want false for an unattested ID-token address")
	}
	if report.Stage != stageAssertion || report.Error != reasonEmailUnverified {
		t.Errorf("stage/error = %q/%q, want %q/%q",
			report.Stage, report.Error, stageAssertion, reasonEmailUnverified)
	}
	if report.DiscoveryStatus != http.StatusOK || report.TokenStatus != http.StatusOK {
		t.Errorf("discovery/token = %d/%d, want 200/200 before the refusal",
			report.DiscoveryStatus, report.TokenStatus)
	}
	if report.UserinfoStatus != http.StatusOK {
		t.Errorf("userinfo_status = %d, want 200 — the diagnostic always reads the endpoint discovery named",
			report.UserinfoStatus)
	}
	if report.UserinfoClaims["email"] != "split@example.org" {
		t.Errorf("userinfo_claims = %v, want the released address kept for the report", report.UserinfoClaims)
	}
	if _, ok := report.UserinfoClaims["groups"]; !ok {
		t.Errorf("userinfo_claims = %v, want the membership claim shown as well", report.UserinfoClaims)
	}
	assertNoTokenMaterial(t, body)
}

// TestOAuthDiagnosticReportsEndpointsAndVerifiedIdentity is the healthy run: the three endpoints
// come from the discovery document rather than a conventional path (REQ-AUTH-082), and the ID-token
// block carries the verified identity without any token material.
func TestOAuthDiagnosticReportsEndpointsAndVerifiedIdentity(t *testing.T) {
	e := newEnv(t)
	idp, provider := e.useIdP(t)
	flow := beginOAuthTransaction(t, e, idp)

	idp.tokenClaims = map[string]any{"nonce": flow["nonce"], "email": "sso@example.org", "email_verified": true}
	idp.userInfoClaims = map[string]any{"sub": "subject-42", "email": "sso@example.org", "email_verified": true}

	report, body := oauthTestWithAdmin(t, e, flow)

	if !report.OK {
		t.Fatalf("ok = false, stage = %q, error = %q", report.Stage, report.Error)
	}
	if report.DiscoveryStatus != http.StatusOK || report.TokenStatus != http.StatusOK || report.UserinfoStatus != http.StatusOK {
		t.Errorf("statuses = discovery %d, token %d, userinfo %d; want 200/200/200",
			report.DiscoveryStatus, report.TokenStatus, report.UserinfoStatus)
	}
	if report.Endpoints["authorize"] != provider.Issuer+"/authorize" ||
		report.Endpoints["token"] != provider.Issuer+"/token" ||
		report.Endpoints["userinfo"] != provider.Issuer+"/userinfo" {
		t.Errorf("endpoints = %v, want the three the discovery document names", report.Endpoints)
	}
	if report.IDTokenClaims["provider_subject"] != "subject-42" {
		t.Errorf("id_token_claims = %v, want the verified subject", report.IDTokenClaims)
	}
	if report.IDTokenClaims["address_source"] != addressFromIDToken {
		t.Errorf("address_source = %v, want %q for an ID-token address",
			report.IDTokenClaims["address_source"], addressFromIDToken)
	}
	assertNoTokenMaterial(t, body)
}

// assertNoTokenMaterial holds the boundary REQ-AUTH-085 draws: the report may name stages,
// statuses and allowlisted claims, and a JWT appearing in it would be a token. `eyJ` is the
// base64url of a JSON object header, so any signed assertion trips this; the access token is
// named by its value because that is the string an operator could replay. Field names that read
// like tokens — `id_token_claims`, `"address_source": "id_token"` — are the report saying where a
// claim came from, which is what REQ-AUTH-085 asks it to say.
func assertNoTokenMaterial(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"eyJ", "test-access-token"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("report carries %q — REQ-AUTH-085 excludes tokens from report data:\n%s", forbidden, body)
		}
	}
}
