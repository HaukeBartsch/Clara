<?php
// The web layer owns browser-session state; the Go API owns OIDC discovery,
// authorization URL creation, PKCE exchange and ID-token verification.

declare(strict_types=1);

use Clara\ApiClient;
use Clara\Config;
use Clara\Logger;
use Clara\Oauth;
use Clara\Request;
use Clara\Session;

function oauth_config(array $overrides = []): Config
{
    return test_config(array_merge([
        'OAUTH2_1_ISSUER' => 'https://idp.example.org',
        'OAUTH2_1_CLIENT_ID' => 'csms',
        'OAUTH2_1_NAMES' => 'Hospital 1',
    ], $overrides));
}

function oauth_flow_response(array $overrides = []): array
{
    return array_merge([
        'authorize_url' => 'https://idp.example.org/as/authorization.oauth2?response_type=code&state=state-123',
        'state' => 'state-123',
        'code_verifier' => str_repeat('v', 64),
        'nonce' => 'nonce-123',
    ], $overrides);
}

function oauth_client(Config $config): Oauth
{
    $logger = new Logger('error', true);
    $request = http_request('GET', '/login', browser_headers());
    $api = new ApiClient($config, $logger, $request, new FakeTransport());

    return new Oauth($api, $config, $logger);
}

describe('OAuth2 authorization code through the Go API (Sequence A)', function (): void {
    it('asks the API for the provider redirect and stores one browser-bound transaction', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        $config = oauth_config();
        $oauth = oauth_client($config);

        $url = $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        assert_same(oauth_flow_response()['authorize_url'], $url);
        assert_same(1, api_request_body('/api/v1/auth/oauth/start')['provider']);
        assert_same('state-123', $_SESSION['_oauth_txn']['state']);
        assert_same(str_repeat('v', 64), $_SESSION['_oauth_txn']['code_verifier']);
        assert_same('nonce-123', $_SESSION['_oauth_txn']['nonce']);
        assert_same('Hospital 1', $_SESSION['_oauth_txn']['source_name']);
        assert_same('login', $_SESSION['_oauth_txn']['purpose']);
    });

    it('passes only the code, PKCE verifier and nonce to the API callback login', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        api_route('/api/v1/auth/login', [
            'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member',
            'is_admin' => false, 'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
        ]);
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        $result = $oauth->complete('the-code', 'state-123');
        $body = api_request_body('/api/v1/auth/login');

        assert_same('member@example.org', $result['user']['email']);
        assert_same('Hospital 1', $result['source_name']);
        assert_same('oauth2', $body['source']);
        assert_same('https://idp.example.org', $body['provider']);
        assert_same('the-code', $body['authorization_code']);
        assert_same(str_repeat('v', 64), $body['code_verifier']);
        assert_same('nonce-123', $body['assertion_nonce']);
        assert_true(!isset($body['email']), 'PHP does not choose the authenticated email');
        assert_true(!isset($body['assertion']), 'PHP does not inspect or forward an ID token');
    });

    it('consumes state before rejecting a forged or replayed callback', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        $error = assert_throws(
            Clara\ApiException::class,
            static fn () => $oauth->complete('a-code', 'forged-state')
        );
        assert_same('state_mismatch', $error->code());
        $again = assert_throws(
            Clara\ApiException::class,
            static fn () => $oauth->complete('a-code', 'state-123')
        );
        assert_same('state_mismatch', $again->code());
        assert_same(0, api_calls_to('/api/v1/auth/login'));
    });

    it('establishes the PHP session only after the API completes OIDC verification and login', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        api_route('/api/v1/auth/login', [
            'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member',
            'is_admin' => false, 'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
        ]);
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], 'Hospital 1');

        $response = router_for(
            http_request('GET', '/auth/callback', browser_headers(), [], ['code' => 'the-code', 'state' => 'state-123']),
            $config
        )->dispatch();

        assert_same(302, $response->status());
        assert_same('/', $response->headers()['Location']);
        assert_true(Session::isAuthenticated());
        assert_same('member@example.org', Session::email());
        assert_same('Hospital 1', Session::selectedSourceName());
    });

    it('runs Control Panel diagnostics through the API without calling login', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        api_route('/api/v1/auth/oauth/test', [
            'ok' => true, 'issuer' => 'https://idp.example.org',
            'discovery_status' => 200, 'token_status' => 200, 'userinfo_status' => 0,
            'userinfo_claims' => [], 'id_token_claims' => ['provider_subject' => 'subject-7'],
        ]);
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], '', 'auth_test');

        $report = $oauth->completeTest('test-code', 'state-123');
        $body = api_request_body('/api/v1/auth/oauth/test');

        assert_same(true, $report['ok']);
        assert_same('test-code', $body['code']);
        assert_same('nonce-123', $body['nonce']);
        assert_same(0, api_calls_to('/api/v1/auth/login'));
    });

    it('consumes the browser transaction when the provider returns an authorization error', function (): void {
        api_route('/api/v1/auth/oauth/start', oauth_flow_response());
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $config = oauth_config();
        $oauth = oauth_client($config);
        $oauth->begin($config->oauthProviders[0], 'Hospital 1');
        sign_in(['is_admin' => 1]);

        $response = router_for(
            http_request('GET', '/auth/callback', browser_headers(), [], [
                'error' => 'access_denied', 'state' => 'state-123',
            ]),
            $config
        )->dispatch();

        assert_same(200, $response->status());
        assert_true(!isset($_SESSION['_oauth_txn']), 'the callback transaction is single-use');
        assert_same(0, api_calls_to('/api/v1/auth/login'));
    });

    /**
     * Renders the Control Panel's authentication section around one stored diagnostic report,
     * which is what the callback leaves behind (§2.11). The page is where a report becomes an
     * explanation — and where fields the API had already computed used to be dropped.
     */
    $renderAuthReport = static function (array $report): string {
        sign_in(['is_admin' => 1]);
        queue_shell();
        Session::setAuthTestReport(array_merge(['kind' => 'oauth'], $report));

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'auth']))->dispatch();

        return $response->body();
    };

    it('names the stage and reason a provider test failed at, and keeps showing what userinfo answered', function () use ($renderAuthReport): void {
        $body = $renderAuthReport([
            'ok' => false, 'issuer' => 'https://idp.example.org',
            'stage' => 'assertion', 'error' => 'email_unverified',
            'discovery_status' => 200, 'token_status' => 200, 'userinfo_status' => 200,
            'endpoints' => [
                'authorize' => 'https://idp.example.org/as/authorization.oauth2',
                'token' => 'https://idp.example.org/as/token.oauth2',
                'userinfo' => 'https://idp.example.org/idp/userinfo.openid',
            ],
            // The split claim release REQ-AUTH-078 admits: the address came from userinfo, and
            // that answer is the evidence about the provider configuration an administrator is
            // reading a failed run for.
            'userinfo_claims' => ['sub' => 'subject-42', 'email' => 'split@example.org', 'groups' => ['nephrology']],
            'id_token_claims' => [],
        ]);

        assert_contains('ID-token verification', $body);
        assert_contains('No verified e-mail address came back', $body);
        assert_contains('/idp/userinfo.openid', $body);
        assert_contains('split@example.org', $body, 'claims render on a failed run too');
        assert_contains('nephrology', $body);
    });

    it('reports the status each endpoint answered with, so a 404 is not read as an outage', function () use ($renderAuthReport): void {
        $body = $renderAuthReport([
            'ok' => false, 'issuer' => 'https://idp.example.org/idp',
            'stage' => 'discovery', 'error' => 'provider_unavailable',
            'discovery_status' => 404, 'token_status' => 0, 'userinfo_status' => 0,
            'userinfo_claims' => [], 'id_token_claims' => [],
        ]);

        assert_contains('Discovery HTTP 404', $body);
        // A host that never answers is the same report with 0, which is what the status line now
        // says out loud beside this sentence.
        assert_contains('not be reached', $body);
        assert_contains('A 0 means no response arrived', $body);
    });

    it('names an issuer mismatch as configuration rather than as an outage', function () use ($renderAuthReport): void {
        $body = $renderAuthReport([
            'ok' => false, 'issuer' => 'https://idp.example.org',
            'stage' => 'discovery', 'error' => 'provider_issuer_mismatch',
            'discovery_status' => 200, 'token_status' => 0, 'userinfo_status' => 0,
            'userinfo_claims' => [], 'id_token_claims' => [],
        ]);

        assert_contains('Discovery HTTP 200', $body);
        assert_contains('Copy the value from that document into OAUTH2_n_ISSUER', $body);
    });

    it('shows both claim sets of a successful run, each under its own heading', function () use ($renderAuthReport): void {
        $body = $renderAuthReport([
            'ok' => true, 'issuer' => 'https://idp.example.org',
            'discovery_status' => 200, 'token_status' => 200, 'userinfo_status' => 200,
            'endpoints' => ['authorize' => 'https://idp.example.org/as/authorization.oauth2'],
            'userinfo_claims' => ['sub' => 'subject-42', 'email' => 'sso@example.org'],
            'id_token_claims' => ['provider_subject' => 'subject-42', 'address_source' => 'id_token'],
        ]);

        assert_contains('Discovery HTTP 200; token endpoint HTTP 200; userinfo endpoint HTTP 200', $body);
        assert_contains('Claims from the userinfo endpoint', $body);
        assert_contains('Identity from the verified ID token', $body);
        assert_contains('subject-42', $body);
        // Nothing on an unsuccessful run's page, and nothing here either: the report is the
        // only thing rendered, stage and statuses aside.
        assert_not_contains('did not complete', $body);
    });

    it('renders a stage or reason this page has never seen as itself instead of failing the page', function () use ($renderAuthReport): void {
        // Development mode throws on a missing UI string, so an untranslated identifier reaching
        // t() would take the whole Control Panel down on a newer API.
        $body = $renderAuthReport([
            'ok' => false, 'stage' => 'revoked_gracefully', 'error' => 'provider_sent_a_twist',
            'discovery_status' => 200, 'token_status' => 200, 'userinfo_status' => 0,
            'userinfo_claims' => [], 'id_token_claims' => [],
        ]);

        assert_contains('revoked_gracefully', $body);
        assert_contains('provider_sent_a_twist', $body);
    });
});

describe('a provider round trip that cannot start (§2.2)', function (): void {
    $unavailable = [
        'error' => 'provider_unavailable', 'message' => 'the authentication provider could not be reached', 'status' => 502,
    ];

    it('forgets the chosen name, so the next visit shows the picker instead of the same failure', function () use ($unavailable): void {
        // A name whose only source is one provider starts its round trip from GET /login itself
        // (REQ-AUTH-066). Remembered after a failed start, it would fail every later visit too.
        api_route('/api/v1/auth/oauth/start', $unavailable, 502);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $config = oauth_config(['OAUTH2_1_NAMES' => 'PingFed', 'LOCAL_LOGIN_NAMES' => 'Local']);
        Session::selectSourceName('PingFed');

        $response = router_for(new Request('GET', '/login', browser_headers(), [], [], [], 'a3f9c21e'), $config)->dispatch();

        assert_same(503, $response->status());
        assert_contains('The sign-in service could not be reached', $response->body());
        // What the generic 500 page showed for debugging (REQ-UI-050); test_config is development.
        assert_contains('Reference: a3f9c21e', $response->body());
        assert_contains(
            'Clara\ApiException: the authentication provider could not be reached at POST /api/v1/auth/oauth/start'
                . ' (OAUTH2_1_ISSUER=https://idp.example.org)',
            $response->body()
        );
        assert_contains('<a class="btn btn-primary btn-sm mt-3" href="/login">Back to sign-in</a>', $response->body());
        assert_same('', Session::selectedSourceName());
        assert_true(!isset($_SESSION['_oauth_txn']), 'a failed start leaves no transaction behind');

        $again = router_for(http_request('GET', '/login', browser_headers()), $config)->dispatch();

        assert_same(200, $again->status());
        assert_contains('data-clara-source-picker', $again->body());
        assert_same(1, api_calls_to('/api/v1/auth/oauth/start'));
    });

    it('keeps the provider address and the failing call off the page in production (REQ-UI-050)', function () use ($unavailable): void {
        api_route('/api/v1/auth/oauth/start', $unavailable, 502);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $config = oauth_config([
            'OAUTH2_1_NAMES' => 'PingFed', 'LOCAL_LOGIN_NAMES' => 'Local',
            'APP_ENV' => 'production', 'WEB_PUBLIC_URL' => 'https://csms.example.org',
        ]);
        Session::selectSourceName('PingFed');

        $body = router_for(new Request('GET', '/login', browser_headers(), [], [], [], 'a3f9c21e'), $config)
            ->dispatch()->body();

        assert_contains('The sign-in service could not be reached', $body);
        assert_contains('Reference: a3f9c21e', $body);
        assert_not_contains('idp.example.org', $body);
        assert_not_contains('/api/v1/', $body);
    });

    it('answers a provider button whose start fails with the same page', function () use ($unavailable): void {
        api_route('/api/v1/auth/oauth/start', $unavailable, 502);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $config = oauth_config([
            'OAUTH2_2_ISSUER' => 'https://idp2.example.org',
            'OAUTH2_2_CLIENT_ID' => 'csms',
            'OAUTH2_2_NAMES' => 'Hospital 1',
            'LOCAL_LOGIN_NAMES' => 'Local',
        ]);
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        Session::selectSourceName('Hospital 1');

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'provider' => '2'],
            ['action' => 'oauth']
        ), $config)->dispatch();

        assert_same(503, $response->status());
        assert_contains('href="/login">Back to sign-in</a>', $response->body());
        assert_same(2, api_request_body('/api/v1/auth/oauth/start')['provider']);
        assert_same('', Session::selectedSourceName());
    });
});
