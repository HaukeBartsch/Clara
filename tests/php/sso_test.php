<?php
// Sign-in for registered applications — Sequence K (Authentication_Authorization_Design.md
// §2.13, REQ-AUTH-092..096). The API holds every rule; what the web layer must get right is
// the browser half: nothing goes back to an address CLARA does not know, a visitor who is not
// signed in signs in first and comes back, and signing out of CLARA ends the clients' grants.

declare(strict_types=1);

use Clara\Session;

const SSO_REDIRECT = 'https://fiona.example.org/applications/User/login.php';
const SSO_LOGOUT = 'https://fiona.example.org/index.php';

function sso_config(array $overrides = []): Clara\Config
{
    return test_config(array_merge([
        'SSO_CLIENT_1_ID' => 'fiona',
        'SSO_CLIENT_1_NAME' => 'Fiona',
        'SSO_CLIENT_1_REDIRECT_URI' => SSO_REDIRECT,
        'SSO_CLIENT_1_LOGOUT_URI' => SSO_LOGOUT,
    ], $overrides));
}

/** The query a client sends the browser to /sso/authorize with. */
function sso_authorize_query(array $overrides = []): array
{
    return array_merge([
        'response_type' => 'code',
        'client_id' => 'fiona',
        'redirect_uri' => SSO_REDIRECT,
        'code_challenge' => str_repeat('A', 43),
        'code_challenge_method' => 'S256',
        'state' => 'client-state-1',
    ], $overrides);
}

function sso_authorize(array $query, ?Clara\Config $config = null): Clara\Response
{
    return router_for(http_request('GET', '/sso/authorize', browser_headers(), [], $query), $config ?? sso_config())->dispatch();
}

/** The query of an absolute redirect back to the client. */
function sso_redirect_query(Clara\Response $response): array
{
    $location = (string) ($response->headers()['Location'] ?? '');
    parse_str((string) parse_url($location, PHP_URL_QUERY), $params);

    return $params;
}

describe('client sign-in configuration (REQ-CFG-038)', function (): void {
    it('reads a registered client, its name defaulting to its id', function (): void {
        $config = sso_config(['SSO_CLIENT_1_NAME' => '']);
        $client = $config->ssoClient('fiona');

        assert_same('fiona', $client['name'] ?? null);
        assert_same(SSO_REDIRECT, $client['redirect_uri']);
        assert_same(SSO_LOGOUT, $client['logout_uri']);
        assert_same(null, $config->ssoClient('other'));
        assert_same(null, $config->ssoClient(''));
        assert_true(!array_key_exists('secret', $client), 'only the API holds the client secret');
    });

    it('refuses a return address that is not an absolute http(s) URL', function (): void {
        $error = assert_throws(Throwable::class, static fn () => sso_config(['SSO_CLIENT_1_REDIRECT_URI' => '/relative']));
        assert_contains('SSO_CLIENT_1_REDIRECT_URI', $error->getMessage());

        $error = assert_throws(Throwable::class, static fn () => sso_config(['SSO_CLIENT_1_LOGOUT_URI' => 'javascript:alert(1)']));
        assert_contains('SSO_CLIENT_1_LOGOUT_URI', $error->getMessage());
    });
});

describe('/sso/authorize (Sequence K steps 1–2)', function (): void {
    it('never sends the browser to an unknown client or an unregistered address (REQ-AUTH-093)', function (): void {
        foreach ([
            sso_authorize_query(['client_id' => 'unknown']),
            sso_authorize_query(['redirect_uri' => 'https://evil.example.org/cb']),
            sso_authorize_query(['redirect_uri' => SSO_REDIRECT . '?extra=1']),
            sso_authorize_query(['redirect_uri' => '']),
        ] as $query) {
            sign_in();
            api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
            $response = sso_authorize($query);

            assert_same(400, $response->status(), 'a refusal page, not a redirect');
            assert_true(!isset($response->headers()['Location']));
            assert_contains('Sign-in request refused', $response->body());
        }
        assert_same(0, api_calls_to('/api/v1/sso/codes'));
    });

    it('sends a signed-out visitor to sign in with the whole request as next', function (): void {
        $response = sso_authorize(sso_authorize_query());

        assert_same(302, $response->status());
        $location = $response->headers()['Location'];
        assert_true(str_starts_with($location, '/login?next='), $location);
        parse_str((string) parse_url($location, PHP_URL_QUERY), $login);
        assert_true(str_starts_with($login['next'], '/sso/authorize?'));
        parse_str((string) parse_url($login['next'], PHP_URL_QUERY), $resumed);
        assert_same(sso_authorize_query(), $resumed, 'the resumed request is the checked one');
        assert_same(0, api_calls_to('/api/v1/sso/codes'));
    });

    it('treats a pending second factor as not signed in', function (): void {
        sign_in(['tfa_pending' => ['user_id' => 7, 'email' => 'member@example.org', 'method' => 'totp']]);
        $response = sso_authorize(sso_authorize_query());

        assert_true(str_starts_with($response->headers()['Location'], '/login?next='));
        assert_same(0, api_calls_to('/api/v1/sso/codes'));
    });

    it('answers prompt=none for a signed-out visitor at the client, with login_required (REQ-AUTH-095)', function (): void {
        $response = sso_authorize(sso_authorize_query(['prompt' => 'none']));

        assert_same(302, $response->status());
        assert_true(str_starts_with($response->headers()['Location'], SSO_REDIRECT . '?'));
        assert_same(['error' => 'login_required', 'state' => 'client-state-1'], sso_redirect_query($response));
    });

    it('gives a signed-in user a code at once and returns it with the state', function (): void {
        $issuedAt = time() - 120;
        sign_in(['issued_at' => $issuedAt]);
        api_route('/api/v1/sso/codes', ['code' => 'the-code', 'expires_in' => 60, 'redirect_uri' => SSO_REDIRECT]);

        $response = sso_authorize(sso_authorize_query(['prompt' => 'none']));

        assert_same(302, $response->status());
        assert_same(['code' => 'the-code', 'state' => 'client-state-1'], sso_redirect_query($response));

        $body = api_request_body('/api/v1/sso/codes');
        assert_same('fiona', $body['client_id']);
        assert_same(SSO_REDIRECT, $body['redirect_uri']);
        assert_same(str_repeat('A', 43), $body['code_challenge']);
        assert_same('S256', $body['code_challenge_method']);
        assert_same($issuedAt, $body['session_issued_at']);
        assert_same(Session::existingSsoSid(), $body['sid'], 'the session keeps the sign-in id it sent');
        assert_same(64, strlen($body['sid']));

        $headers = implode("\n", api_headers_for('/api/v1/sso/codes'));
        assert_contains('X-Internal-User-Id: 7', $headers, 'the code is issued for the signed-in user');
    });

    it('keeps one sign-in id per session across codes', function (): void {
        sign_in();
        api_route('/api/v1/sso/codes', ['code' => 'c', 'expires_in' => 60]);
        sso_authorize(sso_authorize_query());
        $first = Session::existingSsoSid();
        sso_authorize(sso_authorize_query(['state' => 'second']));

        assert_true($first !== null);
        assert_same($first, Session::existingSsoSid());
    });

    it('answers a malformed request at the registered address, never with a code', function (): void {
        $cases = [
            [sso_authorize_query(['response_type' => 'token']), 'unsupported_response_type'],
            [sso_authorize_query(['code_challenge_method' => 'plain']), 'invalid_request'],
            [sso_authorize_query(['code_challenge' => 'short']), 'invalid_request'],
        ];
        foreach ($cases as [$query, $error]) {
            sign_in();
            $response = sso_authorize($query);
            assert_same(['error' => $error, 'state' => 'client-state-1'], sso_redirect_query($response));
        }

        // A state CLARA cannot repeat safely is not repeated.
        sign_in();
        $response = sso_authorize(sso_authorize_query(['state' => '']));
        assert_same(['error' => 'invalid_request'], sso_redirect_query($response));
        assert_same(0, api_calls_to('/api/v1/sso/codes'));
    });

    it('shows the refusal page when the API does not know the client this layer knows', function (): void {
        sign_in();
        api_route('/api/v1/sso/codes', ['error' => 'invalid_client', 'message' => ''], 400);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);

        $response = sso_authorize(sso_authorize_query());

        assert_same(400, $response->status());
        assert_true(!isset($response->headers()['Location']));
    });

    it('tells the client when the API cannot issue a code', function (): void {
        sign_in();
        api_route('/api/v1/sso/codes', ['error' => 'internal', 'message' => ''], 500);

        $response = sso_authorize(sso_authorize_query());

        assert_same(['error' => 'temporarily_unavailable', 'state' => 'client-state-1'], sso_redirect_query($response));
    });
});

describe('the sign-in that /sso/authorize sends a visitor to (REQ-AUTH-096)', function (): void {
    it('lands on the posted next field after a credential sign-in', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['id' => 7, 'email' => 'a@example.org', 'display_name' => 'A', 'is_admin' => false]);
        $next = '/sso/authorize?' . http_build_query(sso_authorize_query());

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw', 'next' => $next],
            ['action' => 'credentials']
        ), sso_config())->dispatch();

        assert_same($next, $response->headers()['Location'], 'the form posts next as a field');
    });

    it('still refuses an off-site next field', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['id' => 7, 'email' => 'a@example.org', 'display_name' => 'A', 'is_admin' => false]);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw', 'next' => '//evil.example.org/'],
            ['action' => 'credentials']
        ))->dispatch();

        assert_same('/', $response->headers()['Location']);
    });

    it('carries next across an OAuth2 round trip', function (): void {
        $config = sso_config([
            'OAUTH2_1_ISSUER' => 'https://idp.example.org',
            'OAUTH2_1_CLIENT_ID' => 'csms',
            'OAUTH2_1_NAMES' => 'Hospital 1',
        ]);
        $next = '/sso/authorize?' . http_build_query(sso_authorize_query());
        api_route('/api/v1/auth/oauth/start', [
            'authorize_url' => 'https://idp.example.org/authorize?state=state-123',
            'state' => 'state-123',
            'code_verifier' => str_repeat('v', 64),
            'nonce' => 'nonce-123',
        ]);
        api_route('/api/v1/auth/login', [
            'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member',
            'is_admin' => false, 'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
        ]);

        // A name whose only source is the provider starts it straight from GET /login.
        Session::selectSourceName('Hospital 1');
        $start = router_for(http_request('GET', '/login', browser_headers(), [], ['next' => $next]), $config)->dispatch();
        assert_true(str_starts_with($start->headers()['Location'], 'https://idp.example.org/'));

        $back = router_for(
            http_request('GET', '/auth/callback', browser_headers(), [], ['code' => 'the-code', 'state' => 'state-123']),
            $config
        )->dispatch();

        assert_true(Session::isAuthenticated());
        assert_same($next, $back->headers()['Location']);
        assert_same('', Session::takeLoginNext(), 'the remembered path serves one sign-in only');
    });

    it('sends an already signed-in visitor of /login on to next', function (): void {
        sign_in();
        $response = router_for(http_request('GET', '/login', browser_headers(), [], ['next' => '/projects/4']))->dispatch();

        assert_same('/projects/4', $response->headers()['Location']);
    });
});

describe('/sso/token and /sso/check (Sequence K steps 3–4)', function (): void {
    it('relays a token request to the API and answers JSON without a session', function (): void {
        api_route('/api/v1/sso/token', [
            'grant' => 'g', 'expires_in' => 3600,
            'user' => ['id' => 7, 'email' => 'member@example.org', 'display_name' => 'Member'],
        ]);
        $request = http_request('POST', '/sso/token', ['accept' => 'text/html'], [
            'grant_type' => 'authorization_code', 'client_id' => 'fiona', 'client_secret' => 's3cret',
            'code' => 'the-code', 'code_verifier' => str_repeat('v', 43), 'redirect_uri' => SSO_REDIRECT,
        ]);
        assert_true(!Clara\Router::needsSession($request), 'a server-to-server call gets no session');

        $response = router_for($request, sso_config())->dispatch();

        assert_same(200, $response->status(), 'no CSRF token is asked for: the secret is the credential');
        assert_contains('application/json', $response->headers()['Content-Type']);
        assert_same('g', json_decode($response->body(), true)['grant']);
        assert_same([
            'client_id' => 'fiona', 'client_secret' => 's3cret', 'code' => 'the-code',
            'code_verifier' => str_repeat('v', 43), 'redirect_uri' => SSO_REDIRECT,
        ], api_request_body('/api/v1/sso/token'));
        $headers = implode("\n", api_headers_for('/api/v1/sso/token'));
        assert_not_contains('X-Internal-User-Id', $headers, 'a pre-authentication call names no user');
    });

    it('accepts the client credentials as HTTP Basic', function (): void {
        api_route('/api/v1/sso/check', ['active' => true, 'reason' => '', 'expires_in' => 10]);
        $request = http_request('POST', '/sso/check', [
            'authorization' => 'Basic ' . base64_encode('fiona:' . rawurlencode('a secret')),
        ], ['grant' => 'the-grant']);

        $response = router_for($request, sso_config())->dispatch();

        assert_same(200, $response->status());
        assert_same(true, json_decode($response->body(), true)['active']);
        assert_same(['client_id' => 'fiona', 'client_secret' => 'a secret', 'grant' => 'the-grant'], api_request_body('/api/v1/sso/check'));
    });

    it('passes a refusal through as {"error"} with the API status', function (): void {
        api_route('/api/v1/sso/token', ['error' => 'invalid_grant', 'message' => 'x'], 400);
        $response = router_for(http_request('POST', '/sso/token', json_headers(), ['client_id' => 'fiona']), sso_config())->dispatch();

        assert_same(400, $response->status());
        assert_same(['error' => 'invalid_grant'], json_decode($response->body(), true));
    });

    it('answers an unreachable API as temporarily_unavailable, not as a signed-out user', function (): void {
        api_route('/api/v1/sso/check', ['error' => 'internal', 'message' => ''], 500);
        $response = router_for(http_request('POST', '/sso/check', json_headers(), ['grant' => 'g']), sso_config())->dispatch();

        assert_same(503, $response->status());
        assert_same(['error' => 'temporarily_unavailable'], json_decode($response->body(), true));
    });

    it('refuses any grant_type but authorization_code', function (): void {
        $response = router_for(http_request('POST', '/sso/token', [], ['grant_type' => 'password']), sso_config())->dispatch();

        assert_same(400, $response->status());
        assert_same(['error' => 'unsupported_grant_type'], json_decode($response->body(), true));
        assert_same(0, api_calls_to('/api/v1/sso/token'));
    });
});

describe('signing out (REQ-AUTH-094)', function (): void {
    it('asks before signing out at a client\'s request', function (): void {
        sign_in();
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $response = router_for(http_request('GET', '/sso/logout', browser_headers(), [], ['client_id' => 'fiona']), sso_config())->dispatch();

        assert_same(200, $response->status());
        assert_contains('Fiona asked to sign you out', $response->body());
        assert_contains('action="/sso/logout"', $response->body());
        assert_contains('name="csrf_token"', $response->body());
        assert_contains('href="' . SSO_LOGOUT . '"', $response->body());
        assert_true(Session::isAuthenticated(), 'a GET signs nobody out');
        assert_same(0, api_calls_to('/api/v1/auth/logout'));
    });

    it('goes straight on when nobody is signed in', function (): void {
        $response = router_for(http_request('GET', '/sso/logout', browser_headers(), [], ['client_id' => 'fiona']), sso_config())->dispatch();
        assert_same(SSO_LOGOUT, $response->headers()['Location']);

        $response = router_for(http_request('GET', '/sso/logout', browser_headers(), [], ['client_id' => 'unknown']), sso_config())->dispatch();
        assert_same('/login', $response->headers()['Location'], 'an unknown client gets no redirect to its address');
    });

    it('ends the client grants, then the session, then returns to the client', function (): void {
        sign_in(['sso_sid' => str_repeat('d', 64)]);
        api_route('/api/v1/sso/end', ['status' => 'ok', 'clients' => ['fiona']]);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        $response = router_for(http_request('POST', '/sso/logout', browser_headers(), [
            'csrf_token' => str_repeat('a', 64), 'client_id' => 'fiona',
        ]), sso_config())->dispatch();

        assert_same(SSO_LOGOUT, $response->headers()['Location']);
        assert_same(['sid' => str_repeat('d', 64)], api_request_body('/api/v1/sso/end'));
        assert_contains('X-Internal-User-Id: 7', implode("\n", api_headers_for('/api/v1/sso/end')));
        assert_same(1, api_calls_to('/api/v1/auth/logout'));
        assert_true(!Session::isAuthenticated());
    });

    it('refuses the sign-out POST without the CSRF token', function (): void {
        sign_in(['sso_sid' => str_repeat('d', 64)]);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $response = router_for(http_request('POST', '/sso/logout', browser_headers(), ['client_id' => 'fiona']), sso_config())->dispatch();

        assert_same('/sso/logout', $response->headers()['Location']);
        assert_true(Session::isAuthenticated());
        assert_same(0, api_calls_to('/api/v1/sso/end'));
    });

    it('ends the client grants on CLARA\'s own sign-out too', function (): void {
        sign_in(['sso_sid' => str_repeat('d', 64)]);
        api_route('/api/v1/sso/end', ['status' => 'ok', 'clients' => ['fiona']]);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        router_for(http_request('POST', '/logout', browser_headers(), ['csrf_token' => str_repeat('a', 64)]))->dispatch();

        assert_same(1, api_calls_to('/api/v1/sso/end'));
        assert_same(1, api_calls_to('/api/v1/auth/logout'));
    });

    it('spends no call on grants a session never issued', function (): void {
        sign_in();
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        router_for(http_request('POST', '/logout', browser_headers(), ['csrf_token' => str_repeat('a', 64)]))->dispatch();

        assert_same(0, api_calls_to('/api/v1/sso/end'));
        assert_same(1, api_calls_to('/api/v1/auth/logout'));
    });

    it('signs out even when ending the grants fails', function (): void {
        sign_in(['sso_sid' => str_repeat('d', 64)]);
        api_route('/api/v1/sso/end', ['error' => 'internal', 'message' => ''], 500);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        router_for(http_request('POST', '/logout', browser_headers(), ['csrf_token' => str_repeat('a', 64)]))->dispatch();

        assert_same(1, api_calls_to('/api/v1/auth/logout'));
        assert_true(!Session::isAuthenticated());
    });
});
