<?php
// The web layer owns browser-session state; the Go API owns OIDC discovery,
// authorization URL creation, PKCE exchange and ID-token verification.

declare(strict_types=1);

use Clara\ApiClient;
use Clara\Config;
use Clara\Logger;
use Clara\Oauth;
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
});
