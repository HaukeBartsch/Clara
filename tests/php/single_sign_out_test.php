<?php
// Single sign-out with the provider — Sequence L (Authentication_Authorization_Design.md §2.14,
// REQ-AUTH-097…099, REQ-UI-088, REQ-CFG-036). The API builds the provider's sign-out address and
// verifies logout tokens; what the web layer must get right is the browser half of L1 (the
// sign-out leaves through the provider, the return chooses a line and nothing else), the
// server half of L2 (the configured path picks the provider, a malformed request never reaches
// the API, the account's session files go), and the addresses the Authentication Test tells an
// administrator to register (REQ-AUTH-100).

declare(strict_types=1);

use Clara\Config;
use Clara\Controllers\BackchannelLogoutController;
use Clara\Request;
use Clara\Session;
use Clara\SessionIndex;

const SLO_END_SESSION = 'https://idp.example.org/idp/startSLO.ping?client_id=csms&state=end-state-1';

function slo_config(array $overrides = []): Config
{
    return test_config(array_merge([
        'OAUTH2_1_ISSUER' => 'https://idp.example.org',
        'OAUTH2_1_CLIENT_ID' => 'csms',
        'OAUTH2_1_NAMES' => 'Hospital 1',
        'OIDC_SINGLE_SIGN_OUT' => '1',
        'OAUTH2_1_POST_LOGOUT_REDIRECT_URI' => 'http://localhost:8000/auth/logged-out',
        'OAUTH2_1_BACKCHANNEL_LOGOUT_PATH' => '/auth/backchannel-logout/idp1',
        'SSO_CLIENT_1_ID' => 'fiona',
        'SSO_CLIENT_1_REDIRECT_URI' => 'https://fiona.example.org/login.php',
        'SSO_CLIENT_1_LOGOUT_URI' => 'https://fiona.example.org/index.php',
    ], $overrides));
}

/** A session that signed in through provider 1. */
function slo_sign_in(array $overrides = []): void
{
    sign_in(array_merge(['auth_source' => 'oauth2', 'oauth2_provider' => 1], $overrides));
}

function slo_logout(?Config $config = null, string $path = '/logout', array $post = []): Clara\Response
{
    return router_for(http_request('POST', $path, browser_headers(), array_merge([
        'csrf_token' => str_repeat('a', 64),
    ], $post)), $config ?? slo_config())->dispatch();
}

/** A back-channel request as the provider's server sends it: form-encoded, no cookie. */
function slo_backchannel(string $rawBody, string $path = '/auth/backchannel-logout/idp1', ?Config $config = null, string $method = 'POST'): Clara\Response
{
    parse_str($rawBody, $post);
    $request = new Request($method, $path, [
        'content-type' => 'application/x-www-form-urlencoded',
        'content-length' => (string) strlen($rawBody),
    ], [], $post, [], '', [], '192.0.2.10', null, $rawBody);

    return router_for($request, $config ?? slo_config())->dispatch();
}

function slo_flash_texts(): array
{
    return array_map(static fn (array $f): string => $f['text'], $_SESSION['_flash'] ?? []);
}

describe('single sign-out configuration (REQ-CFG-036)', function (): void {
    it('is off by default and reads each provider\'s sign-out settings', function (): void {
        $off = test_config(['OAUTH2_1_ISSUER' => 'https://idp.example.org', 'OAUTH2_1_CLIENT_ID' => 'csms']);
        assert_same(false, $off->oidcSingleSignOut);
        assert_same('', $off->oauthProviders[0]['backchannel_logout_path']);
        assert_same(null, $off->providerByBackchannelPath('/auth/backchannel-logout/idp1'));

        $on = slo_config();
        assert_same(true, $on->oidcSingleSignOut);
        assert_same('http://localhost:8000/auth/logged-out', $on->oauthProviders[0]['post_logout_redirect_uri']);
        assert_same(1, $on->providerByBackchannelPath('/auth/backchannel-logout/idp1')['index'] ?? null);
        assert_same(null, $on->providerByBackchannelPath('/auth/backchannel-logout/other'));
    });

    it('refuses to start half-configured or with a path it cannot route', function (): void {
        foreach ([
            [['OIDC_SINGLE_SIGN_OUT' => 'yes'], 'OIDC_SINGLE_SIGN_OUT'],
            [['OAUTH2_1_BACKCHANNEL_LOGOUT_PATH' => ''], 'OAUTH2_1_BACKCHANNEL_LOGOUT_PATH is required'],
            [['OAUTH2_1_BACKCHANNEL_LOGOUT_PATH' => '/auth/backchannel-logout/a/b'], 'OAUTH2_1_BACKCHANNEL_LOGOUT_PATH must be'],
            [['OAUTH2_1_BACKCHANNEL_LOGOUT_PATH' => '/logout/idp1'], 'OAUTH2_1_BACKCHANNEL_LOGOUT_PATH must be'],
            [[
                'OAUTH2_2_ISSUER' => 'https://other.example.org', 'OAUTH2_2_CLIENT_ID' => 'csms',
                'OAUTH2_2_BACKCHANNEL_LOGOUT_PATH' => '/auth/backchannel-logout/idp1',
            ], 'is already OAUTH2_1_BACKCHANNEL_LOGOUT_PATH'],
        ] as [$overrides, $message]) {
            $error = assert_throws(Throwable::class, static fn () => slo_config($overrides));
            assert_contains($message, $error->getMessage());
        }
    });
});

describe('sign-out through the provider (Sequence L1, REQ-AUTH-097, REQ-UI-088)', function (): void {
    it('remembers the provider an OAuth2 sign-in came through', function (): void {
        $config = slo_config();
        api_route('/api/v1/auth/oauth/start', [
            'authorize_url' => 'https://idp.example.org/authorize?state=s-1', 'state' => 's-1',
            'code_verifier' => str_repeat('v', 64), 'nonce' => 'n-1',
        ]);
        api_route('/api/v1/auth/login', [
            'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member', 'is_admin' => false,
            'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
        ]);
        Session::selectSourceName('Hospital 1');
        router_for(http_request('GET', '/login', browser_headers()), $config)->dispatch();
        router_for(http_request('GET', '/auth/callback', browser_headers(), [], ['code' => 'c', 'state' => 's-1']), $config)->dispatch();

        assert_true(Session::isAuthenticated());
        assert_same(1, Session::oauthProvider());
    });

    it('ends CLARA\'s session first, then sends the browser to the provider', function (): void {
        slo_sign_in(['sso_sid' => str_repeat('d', 64)]);
        api_route('/api/v1/sso/end', ['status' => 'ok', 'clients' => []]);
        api_route('/api/v1/auth/oauth/end-session', ['end_session_url' => SLO_END_SESSION, 'state' => 'end-state-1']);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        $response = slo_logout();

        assert_same(SLO_END_SESSION, $response->headers()['Location']);
        assert_same(['provider' => 1], api_request_body('/api/v1/auth/oauth/end-session'));
        assert_contains('X-Internal-User-Id: 7', implode("\n", api_headers_for('/api/v1/auth/oauth/end-session')),
            'the address is built as the signed-in user, with the session epoch');
        $order = array_values(array_filter(array_map(
            static fn (array $c): string => (string) parse_url($c['url'], PHP_URL_PATH),
            $GLOBALS['api_calls']
        ), static fn (string $p): bool => str_starts_with($p, '/api/v1/sso/') || str_starts_with($p, '/api/v1/auth/')));
        assert_same(['/api/v1/sso/end', '/api/v1/auth/oauth/end-session', '/api/v1/auth/logout'], $order);
        assert_true(!Session::isAuthenticated(), 'CLARA\'s sign-out completed before the browser left');
        assert_same('end-state-1', $_SESSION['oidc_logout_state']['state'] ?? null);
        assert_same('', $_SESSION['oidc_logout_state']['return'] ?? null);
        assert_true(!isset($_SESSION['user_id']), 'the fresh session holds no identity');
    });

    it('still signs out of CLARA when the provider address cannot be built, and says so', function (): void {
        slo_sign_in();
        api_route('/api/v1/auth/oauth/end-session', ['error' => 'end_session_unsupported', 'message' => '', 'status' => 502], 502);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        $response = slo_logout();

        assert_same('/login', $response->headers()['Location']);
        assert_same(1, api_calls_to('/api/v1/auth/logout'));
        assert_true(!Session::isAuthenticated());
        assert_contains('may still be active', implode(' ', slo_flash_texts()));
    });

    it('leaves local sessions, and every session with the switch off, as before', function (): void {
        foreach ([
            'local session, switch on' => [slo_config(), true],
            'OAuth2 session, switch off' => [slo_config(['OIDC_SINGLE_SIGN_OUT' => '0', 'OAUTH2_1_BACKCHANNEL_LOGOUT_PATH' => '']), false],
        ] as [$config, $local]) {
            resetApi();
            slo_sign_in($local ? ['auth_source' => 'local'] : []);
            if ($local) {
                unset($_SESSION['oauth2_provider']);
            }
            api_route('/api/v1/auth/logout', ['status' => 'ok']);

            $response = slo_logout($config);

            assert_same('/login', $response->headers()['Location']);
            assert_same(0, api_calls_to('/api/v1/auth/oauth/end-session'));
            assert_same([], slo_flash_texts());
        }
    });

    it('carries a client\'s sign-out link through the provider back to the client', function (): void {
        slo_sign_in();
        api_route('/api/v1/auth/oauth/end-session', ['end_session_url' => SLO_END_SESSION, 'state' => 'end-state-1']);
        api_route('/api/v1/auth/logout', ['status' => 'ok']);

        $response = slo_logout(null, '/sso/logout', ['client_id' => 'fiona']);

        assert_same(SLO_END_SESSION, $response->headers()['Location']);
        assert_same('https://fiona.example.org/index.php', $_SESSION['oidc_logout_state']['return'] ?? null);

        $back = router_for(http_request('GET', '/auth/logged-out', browser_headers(), [], ['state' => 'end-state-1']), slo_config())->dispatch();
        assert_same('https://fiona.example.org/index.php', $back->headers()['Location']);
    });
});

describe('GET /auth/logged-out (Sequence L1 step 3)', function (): void {
    it('says "signed out of CLARA and of the provider" for the state it sent, once', function (): void {
        Session::rememberLogoutReturn('end-state-1', '');

        $response = router_for(http_request('GET', '/auth/logged-out', browser_headers(), [], ['state' => 'end-state-1']), slo_config())->dispatch();

        assert_same('/login', $response->headers()['Location']);
        assert_same(['You are signed out of CLARA and of your identity provider.'], slo_flash_texts());
        assert_true(!isset($_SESSION['oidc_logout_state']), 'the state is single use');
    });

    it('says only "signed out of CLARA" for any other return, and changes nothing else', function (): void {
        foreach ([
            ['saved' => 'end-state-1', 'at' => time(), 'query' => ['state' => 'forged']],
            ['saved' => 'end-state-1', 'at' => time(), 'query' => []],
            ['saved' => 'end-state-1', 'at' => time() - Session::LOGOUT_RETURN_SECONDS - 1, 'query' => ['state' => 'end-state-1']],
            ['saved' => null, 'at' => 0, 'query' => ['state' => 'end-state-1']],
        ] as $case) {
            $_SESSION = ['csrf_token' => str_repeat('a', 64)];
            if ($case['saved'] !== null) {
                $_SESSION['oidc_logout_state'] = ['state' => $case['saved'], 'at' => $case['at'], 'return' => 'https://fiona.example.org/index.php'];
            }

            $response = router_for(http_request('GET', '/auth/logged-out', browser_headers(), [], $case['query']), slo_config())->dispatch();

            assert_same('/login', $response->headers()['Location'], 'a forged return is never sent on to the client address');
            assert_same(['You are signed out of CLARA.'], slo_flash_texts());
            assert_true(!isset($_SESSION['oidc_logout_state']));
            assert_true(!Session::isAuthenticated());
        }
    });
});

describe('the provider\'s back-channel logout (Sequence L2, REQ-AUTH-098/099)', function (): void {
    it('relays the token without a session, and removes the account\'s session files', function (): void {
        $config = slo_config();
        $index = new SessionIndex($config->sessionDir);
        $ids = ['slotestaaaaaaaaaaaaaaaaaaaaaaaa1', 'slotestaaaaaaaaaaaaaaaaaaaaaaaa2'];
        foreach ($ids as $id) {
            file_put_contents($config->sessionDir . '/sess_' . $id, 'x');
            $index->add(21, $id);
        }
        api_route('/api/v1/auth/oauth/backchannel-logout', ['status' => 'ok', 'matched' => true, 'user_id' => 21, 'clients' => ['fiona']]);

        $request = new Request('POST', '/auth/backchannel-logout/idp1', [], [], ['logout_token' => 'a.b.c']);
        assert_true(!Clara\Router::needsSession($request), 'a server-to-server call gets no session');
        $response = slo_backchannel('logout_token=eyJ.eyJ.sig', '/auth/backchannel-logout/idp1', $config);

        assert_same(200, $response->status(), 'no CSRF token is asked for: the signed token is the credential');
        assert_same('', $response->body());
        assert_same('no-store', $response->headers()['Cache-Control']);
        assert_same(['provider' => 1, 'logout_token' => 'eyJ.eyJ.sig'], api_request_body('/api/v1/auth/oauth/backchannel-logout'));
        assert_not_contains('X-Internal-User-Id', implode("\n", api_headers_for('/api/v1/auth/oauth/backchannel-logout')),
            'a pre-authentication call names no user (DEV-API-46)');
        foreach ($ids as $id) {
            assert_true(!is_file($config->sessionDir . '/sess_' . $id), "session {$id} still exists");
        }
        assert_same([], $index->sessionIds(21));
    });

    it('answers an unmatched subject with 200 and removes nothing', function (): void {
        api_route('/api/v1/auth/oauth/backchannel-logout', ['status' => 'ok', 'matched' => false, 'user_id' => null, 'clients' => []]);

        $response = slo_backchannel('logout_token=eyJ.eyJ.sig');

        assert_same(200, $response->status());
    });

    it('refuses a malformed request before calling the API', function (): void {
        foreach ([
            'logout_token=one&logout_token=two',
            'other=field',
            'logout_token=',
            'logout_token=' . str_repeat('a', BackchannelLogoutController::MAX_BODY_BYTES),
        ] as $body) {
            resetApi();
            $response = slo_backchannel($body);

            assert_same(400, $response->status(), $body);
            assert_same(['error' => 'invalid_request'], json_decode($response->body(), true));
            assert_same('no-store', $response->headers()['Cache-Control']);
            assert_same(0, api_calls_to('/api/v1/auth/oauth/backchannel-logout'));
        }
    });

    it('relays a refused token as 400 and an API that cannot answer as 503', function (): void {
        api_route('/api/v1/auth/oauth/backchannel-logout', ['error' => 'invalid_logout_token', 'message' => '', 'status' => 400], 400);
        $refused = slo_backchannel('logout_token=eyJ.eyJ.sig');
        assert_same(400, $refused->status());
        assert_same(['error' => 'invalid_request'], json_decode($refused->body(), true));

        resetApi();
        api_route('/api/v1/auth/oauth/backchannel-logout', ['error' => 'provider_unavailable', 'message' => '', 'status' => 502], 502);
        $outage = slo_backchannel('logout_token=eyJ.eyJ.sig');
        assert_same(503, $outage->status(), 'the provider may retry: nothing was recorded');
        assert_same(['error' => 'temporarily_unavailable'], json_decode($outage->body(), true));
    });

    it('is no route for a path naming no provider, or with single sign-out off', function (): void {
        assert_same(404, slo_backchannel('logout_token=x', '/auth/backchannel-logout/unknown')->status());
        $off = slo_config(['OIDC_SINGLE_SIGN_OUT' => '0']);
        assert_same(404, slo_backchannel('logout_token=x', '/auth/backchannel-logout/idp1', $off)->status());
        assert_same(0, api_calls_to('/api/v1/auth/oauth/backchannel-logout'));
    });

    it('answers every other method with 405', function (): void {
        $response = slo_backchannel('', '/auth/backchannel-logout/idp1', null, 'GET');

        assert_same(405, $response->status());
        assert_same('POST', $response->headers()['Allow']);
    });
});

describe('the session index (Authentication §3)', function (): void {
    it('lists live sessions per account, forgets ended ones, and ends them all', function (): void {
        $dir = sys_get_temp_dir() . '/clara-index-' . bin2hex(random_bytes(4));
        mkdir($dir, 0700);
        $index = new SessionIndex($dir);
        $a = 'idxaaaaaaaaaaaaaaaaaaaaaaaaaaaa1';
        $b = 'idxaaaaaaaaaaaaaaaaaaaaaaaaaaaa2';
        $c = 'idxaaaaaaaaaaaaaaaaaaaaaaaaaaaa3';
        file_put_contents("{$dir}/sess_{$a}", 'x');
        file_put_contents("{$dir}/sess_{$b}", 'x');
        file_put_contents("{$dir}/sess_{$c}", 'x');

        $index->add(5, $a);
        $index->add(5, $b);
        $index->add(5, $b);
        $index->add(6, $c);
        $index->add(5, '../../etc/passwd');
        assert_same([$a, $b], $index->sessionIds(5));

        unlink("{$dir}/sess_{$a}"); // ended by timeout or garbage collection
        $index->add(5, $c);
        assert_same([$b, $c], $index->sessionIds(5), 'a listed id without a session file is dropped');
        $index->remove(5, $c);
        assert_same([$b], $index->sessionIds(5));

        assert_same(1, $index->endAll(5));
        assert_true(!is_file("{$dir}/sess_{$b}"));
        assert_true(is_file("{$dir}/sess_{$c}"), 'another account\'s session stays');
        assert_same([$c], $index->sessionIds(6));
        array_map('unlink', glob("{$dir}/*") ?: []);
        rmdir($dir);
    });
});

describe('Authentication Test: addresses to register at the provider (REQ-AUTH-100)', function (): void {
    it('lists the redirect, post-logout and back-channel addresses with their settings', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'auth']), slo_config())->dispatch()->body();

        assert_contains('Register at the identity provider', $body);
        assert_contains('<code>http://localhost:8000/auth/callback</code>', $body);
        assert_contains('<code>http://localhost:8000/auth/logged-out</code>', $body);
        assert_contains('<code>http://localhost:8000/auth/backchannel-logout/idp1</code>', $body);
        assert_contains('OAUTH2_1_POST_LOGOUT_REDIRECT_URI', $body);
        assert_contains('OAUTH2_1_BACKCHANNEL_LOGOUT_PATH', $body);
        assert_not_contains('suggested value', $body);
        assert_contains('Single sign-out is on', $body);
    });

    it('suggests the sign-out addresses when they are not configured yet', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        $config = test_config(['OAUTH2_1_ISSUER' => 'https://idp.example.org', 'OAUTH2_1_CLIENT_ID' => 'csms']);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'auth']), $config)->dispatch()->body();

        assert_contains('<code>http://localhost:8000/auth/logged-out</code>', $body);
        assert_contains('<code>http://localhost:8000/auth/backchannel-logout/idp1</code>', $body);
        assert_same(2, substr_count($body, 'not configured — suggested value'));
        assert_contains('Single sign-out is off', $body);
    });
});
