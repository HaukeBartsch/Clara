<?php
// Sequence A — the OAuth2 authorization-code round trip
// (Authentication_Authorization_Design.md §2.1, REQ-AUTH-001/002/066): what PHP puts into
// the redirect, how it treats the callback, and which failures send the user back to the
// login page without a session. The identity provider is the fake transport: the tests
// state what each IdP endpoint answers, exactly as they do for the API.

declare(strict_types=1);

use Clara\Config;
use Clara\Logger;
use Clara\Oauth;
use Clara\Session;

/** A configuration with one OAuth2 provider answering to "Hospital 1". */
function oauth_config(array $overrides = []): Config
{
    return test_config(array_merge([
        'OAUTH2_1_ISSUER' => 'https://idp.example.org',
        'OAUTH2_1_CLIENT_ID' => 'csms',
        'OAUTH2_1_CLIENT_SECRET' => 'a-secret',
        'OAUTH2_1_NAMES' => 'Hospital 1',
    ], $overrides));
}

/** The provider's discovery document, as a Keycloak-style issuer would answer it. */
function oauth_provider_endpoints(string $issuer = 'https://idp.example.org', int $status = 200): void
{
    // Host-qualified so two issuers in one test answer differently (§2.9).
    api_route((string) parse_url($issuer, PHP_URL_HOST) . '/.well-known/openid-configuration', [
        'authorization_endpoint' => $issuer . '/protocol/openid-connect/auth',
        'token_endpoint' => $issuer . '/protocol/openid-connect/token',
        'userinfo_endpoint' => $issuer . '/protocol/openid-connect/userinfo',
    ], $status);
}

/** The OAuth2 client under test: the real one, answering through the fake transport. */
function oauth_client(Config $config): Oauth
{
    return new Oauth($config, new Logger('error', true), new FakeTransport());
}

/** An unsigned JWT with these claims — the signature is not what these tests are about. */
function oauth_id_token(array $claims): string
{
    $encode = static fn (array $part): string => rtrim(strtr(base64_encode((string) json_encode($part)), '+/', '-_'), '=');

    return $encode(['alg' => 'none']) . '.' . $encode($claims) . '.sig';
}

describe('oauth2 authorization code (Sequence A, §2.1)', function (): void {
    it('starts the round trip with state and an S256 PKCE challenge', function (): void {
        oauth_provider_endpoints();
        $config = oauth_config();
        $oauth = oauth_client($config);

        $url = $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        parse_str((string) parse_url($url, PHP_URL_QUERY), $params);
        assert_same('code', $params['response_type']);
        assert_same('csms', $params['client_id']);
        assert_same('S256', $params['code_challenge_method']);
        assert_same(
            'http://localhost:8000/auth/callback',
            $params['redirect_uri'],
            'the documented default: WEB_PUBLIC_URL + /auth/callback (REQ-CFG-011)'
        );

        // The endpoints come from the discovery document, so a provider whose paths
        // differ needs no configuration for them (ASM-AUTH-1).
        assert_true(str_starts_with($url, 'https://idp.example.org/protocol/openid-connect/auth?'), 'discovered authorize endpoint');

        $txn = $_SESSION['_oauth_txn'] ?? [];
        assert_same(32, strlen((string) ($txn['state'] ?? '')), 'state is 16 random bytes in hex (§2.1 step 1)');
        assert_true(preg_match('/^[A-Za-z0-9_-]{43,128}$/', (string) $txn['code_verifier']) === 1, 'a verifier RFC 7636 accepts');
        assert_same(
            rtrim(strtr(base64_encode(hash('sha256', (string) $txn['code_verifier'], true)), '+/', '-_'), '='),
            $params['code_challenge'],
            'the challenge is S256 of the verifier'
        );
        assert_same(1, $txn['provider_index']);
        assert_same('Hospital 1', $txn['source_name']);
    });

    it('falls back to the conventional endpoints when no discovery document exists', function (): void {
        oauth_provider_endpoints('https://idp.example.org', 404);
        $config = oauth_config();
        $oauth = oauth_client($config);

        $url = $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        assert_true(str_starts_with($url, 'https://idp.example.org/authorize?'), 'ASM-AUTH-1 derivation under the issuer');
    });

    it('exchanges the code with the verifier and reads the address from userinfo', function (): void {
        oauth_provider_endpoints();
        api_route('/protocol/openid-connect/token', ['access_token' => 'at-1', 'expires_in' => 300]);
        api_route('/protocol/openid-connect/userinfo', ['sub' => '42', 'email' => 'Researcher@Example.org', 'name' => 'Rae Researcher']);

        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], 'Hospital 1');
        $txn = $_SESSION['_oauth_txn'];

        $identity = $oauth->complete('the-code', (string) $txn['state']);

        assert_same('researcher@example.org', $identity['email'], 'the claim the provider vouches for (§2.1 step 5)');
        assert_same('Rae Researcher', $identity['display_name']);
        assert_same('https://idp.example.org', $identity['provider'], 'Sequence C names the concrete winning source (§2.3)');
        assert_same('Hospital 1', $identity['source_name'], 'the name the user selected rides along (REQ-AUTH-067)');

        // The exchange is form-encoded, as RFC 6749 §4.1.3 requires of the client.
        parse_str(api_request_raw('/protocol/openid-connect/token'), $tokenRequest);
        assert_same('authorization_code', $tokenRequest['grant_type']);
        assert_same('the-code', $tokenRequest['code']);
        assert_same($txn['code_verifier'], $tokenRequest['code_verifier'], 'the verifier proves this is the same browser (PKCE)');
        assert_same('a-secret', $tokenRequest['client_secret'], 'the client authenticates to its own token endpoint (§2.1 step 4)');
    });

    it('reads a non-default claim, and falls back to the ID token when userinfo withholds it', function (): void {
        oauth_provider_endpoints();
        api_route('/protocol/openid-connect/token', ['access_token' => 'at-1', 'id_token' => oauth_id_token(['mail' => 'from@idtoken.example'])]);
        api_route('/protocol/openid-connect/userinfo', ['sub' => '42']);

        $config = oauth_config(['OAUTH2_1_EMAIL_ATTR' => 'mail']);
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], '');

        $identity = $oauth->complete('the-code', (string) $_SESSION['_oauth_txn']['state']);

        assert_same('from@idtoken.example', $identity['email'], 'the ID token is the documented alternative (§2.1 step 5)');
    });

    it('rejects a callback whose state is not this browser\'s, once', function (): void {
        oauth_provider_endpoints();
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], '');

        $state = (string) $_SESSION['_oauth_txn']['state'];

        $error = assert_throws(Clara\ApiException::class, static fn () => $oauth->complete('a-code', 'forged-state'));
        assert_same('state_mismatch', $error->code());

        // Single use: the state is spent by the attempt, so a captured callback URL
        // cannot be presented again — not even with the value that was right (§2.1 step 3).
        $again = assert_throws(Clara\ApiException::class, static fn () => $oauth->complete('a-code', $state));
        assert_same('state_mismatch', $again->code());
    });

    it('reports an outage rather than a credential failure when the token endpoint refuses', function (): void {
        oauth_provider_endpoints();
        api_route('/protocol/openid-connect/token', ['error' => 'temporarily_unavailable'], 503);

        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], '');

        $error = assert_throws(Clara\ApiException::class, static fn () => $oauth->complete('a-code', (string) $_SESSION['_oauth_txn']['state']));
        assert_same('provider_unavailable', $error->code());
    });

    it('completes the login through Sequence C and establishes the session', function (): void {
        oauth_provider_endpoints();
        api_route('/protocol/openid-connect/token', ['access_token' => 'at-1']);
        api_route('/protocol/openid-connect/userinfo', ['email' => 'member@example.org']);
        api_route('/api/v1/auth/login', [
            'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member',
            'is_admin' => false, 'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
        ]);

        $config = oauth_config();
        $request = http_request('GET', '/auth/callback', browser_headers(), [], [
            'code' => 'the-code', 'state' => peek_oauth_state($config),
        ]);
        $response = router_for($request, $config)->dispatch();

        assert_same(302, $response->status());
        assert_same('/', $response->headers()['Location']);
        assert_true(Session::isAuthenticated(), 'the round trip ends signed in (§2.1 step 7)');
        assert_same('member@example.org', Session::email());

        $body = api_request_body('/api/v1/auth/login');
        assert_same('oauth2', $body['source']);
        assert_same('https://idp.example.org', $body['provider']);
        assert_same('Hospital 1', $body['source_name']);
    });

    it('sends a failed callback back to the login page with one line and no session', function (): void {
        oauth_provider_endpoints();
        $config = oauth_config();

        // No transaction was ever started: this callback came from somewhere else.
        $response = router_for(
            http_request('GET', '/auth/callback', browser_headers(), [], ['code' => 'x', 'state' => 'nope']),
            $config
        )->dispatch();

        assert_contains('interrupted', $response->body());
        assert_true(!Session::isAuthenticated(), 'a rejected callback must not sign anyone in');
    });

    it('keeps the local credential path beside a single provider (GD-18)', function (): void {
        oauth_provider_endpoints();
        // One provider is configured, and nothing puts the local source under another
        // name, so it stays in the implicit default set — which is what lets a first
        // installation sign in with the bootstrap account before the identity provider
        // holds anybody's address (REQ-AUTH-051). With one distinct name there is no
        // picker to skip either, so both paths are offered rather than redirecting into
        // one; §2.2's direct hand-off applies to a name that carries a provider and no
        // credential source, which the "Google" case below shows.
        $config = oauth_config();

        $response = router_for(http_request('GET', '/login', browser_headers()), $config)->dispatch();

        assert_same(200, $response->status());
        assert_contains('Sign in with idp.example.org', $response->body());
        assert_contains('name="password"', $response->body());
    });

    it('offers several providers individually instead of picking one', function (): void {
        oauth_provider_endpoints();
        oauth_provider_endpoints('https://accounts.google.com');
        // Both answer to the same name, so there is no picker — and no single provider to
        // redirect to: each gets its own button (REQ-AUTH-066).
        $config = oauth_config([
            'OAUTH2_2_ISSUER' => 'https://accounts.google.com',
            'OAUTH2_2_CLIENT_ID' => 'other-client',
            'OAUTH2_2_CLIENT_SECRET' => 'another-secret',
            'OAUTH2_2_NAMES' => 'Hospital 1',
        ]);

        $response = router_for(http_request('GET', '/login', browser_headers()), $config)->dispatch();

        assert_same(200, $response->status());
        assert_contains('Sign in with idp.example.org', $response->body());
        assert_contains('Sign in with accounts.google.com', $response->body());
    });

    it('offers a provider only under the name that carries it (§2.9)', function (): void {
        oauth_provider_endpoints();
        oauth_provider_endpoints('https://accounts.google.com');
        // Two distinct names: the picker appears, and Google belongs to "Google" alone.
        $config = oauth_config([
            'LOCAL_LOGIN_NAMES' => 'Hospital 1',
            'OAUTH2_2_ISSUER' => 'https://accounts.google.com',
            'OAUTH2_2_CLIENT_ID' => 'other-client',
            'OAUTH2_2_CLIENT_SECRET' => 'another-secret',
            'OAUTH2_2_NAMES' => 'Google',
        ]);

        $underHospital = router_for(
            http_request('GET', '/login', browser_headers(), [], ['source' => 'Hospital 1']),
            $config
        )->dispatch();
        assert_not_contains('accounts.google.com', $underHospital->body());
        assert_contains('name="password"', $underHospital->body(), 'the local source is under this name');

        // Under "Google" the only source is that provider, so the page continues straight
        // into its flow — and it is Google's flow, not the other provider's (§2.2).
        $underGoogle = router_for(
            http_request('GET', '/login', browser_headers(), [], ['source' => 'Google']),
            $config
        )->dispatch();
        assert_same(302, $underGoogle->status());
        assert_contains('accounts.google.com', (string) $underGoogle->headers()['Location']);
    });
});

/**
 * Starts a round trip and returns its state, so a callback test can present the value
 * this browser would hold. The session survives into the dispatch below it.
 */
function peek_oauth_state(Config $config): string
{
    $oauth = oauth_client($config);
    $oauth->begin($config->oauthProviders[0], 'Hospital 1');

    return (string) $_SESSION['_oauth_txn']['state'];
}
