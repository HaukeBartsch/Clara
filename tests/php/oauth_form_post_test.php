<?php
// The form_post authorization response (REQ-CFG-041, Authentication_Authorization_Design.md
// §2.1 step 3a). The provider POSTs `code` and `state` cross-site, so the session cookie does
// not come with it: POST /auth/callback parks the response under a single-use handle and sends
// the browser to GET /auth/callback?handoff=…, where the session is present and Sequence A
// continues unchanged. What must hold: the POST runs without a session or CSRF token and calls
// nothing; it is open only while a provider is configured for form_post; the GET keeps taking
// query responses whatever is configured; a handle works once, briefly, and only together with
// the session's own `state`; and anything malformed ends on the login form, not in a sign-in.

declare(strict_types=1);

use Clara\Config;
use Clara\OauthHandoff;
use Clara\Request;
use Clara\Router;
use Clara\Session;

function fp_config(string $mode = 'form_post'): Config
{
    return test_config([
        'OAUTH2_1_ISSUER' => 'https://idp.example.org',
        'OAUTH2_1_CLIENT_ID' => 'csms',
        'OAUTH2_1_NAMES' => 'Hospital 1',
        'OAUTH2_1_RESPONSE_MODE' => $mode,
    ]);
}

/** The provider's auto-submitted form: form-encoded, cross-site, no cookie, no CSRF token. */
function fp_post(string $rawBody, ?Config $config = null): Clara\Response
{
    parse_str($rawBody, $post);
    $request = new Request('POST', '/auth/callback', [
        'content-type' => 'application/x-www-form-urlencoded',
        'content-length' => (string) strlen($rawBody),
        'origin' => 'https://idp.example.org',
    ], [], $post, [], '', [], '192.0.2.20', null, $rawBody);

    return router_for($request, $config ?? fp_config())->dispatch();
}

function fp_get(array $query, ?Config $config = null): Clara\Response
{
    return router_for(http_request('GET', '/auth/callback', browser_headers(), [], $query), $config ?? fp_config())->dispatch();
}

/** The transaction /login left in this browser's session (Sequence A step 1). */
function fp_begin(string $state = 'state-1', string $purpose = 'login'): void
{
    Session::beginOauthTransaction([
        'provider_index' => 1, 'state' => $state, 'code_verifier' => str_repeat('v', 64),
        'nonce' => 'nonce-1', 'source_name' => 'Hospital 1', 'purpose' => $purpose,
    ]);
}

function fp_login_answer(): void
{
    api_route('/api/v1/auth/login', [
        'id' => 12, 'email' => 'member@example.org', 'display_name' => 'Member', 'is_admin' => false,
        'auth_source' => 'oauth2', 'ui_language' => 'en', 'ui_theme' => null,
    ]);
}

/** The handle a 303 from POST /auth/callback names, or '' for the empty one. */
function fp_handle(Clara\Response $response): string
{
    $location = $response->headers()['Location'] ?? '';
    assert_true(str_starts_with($location, '/auth/callback?handoff='), "unexpected Location {$location}");

    return substr($location, strlen('/auth/callback?handoff='));
}

describe('OAUTH2_N_RESPONSE_MODE (REQ-CFG-041)', function (): void {
    it('is query by default, form_post on request, and refuses anything else', function (): void {
        assert_same('query', test_config(['OAUTH2_1_ISSUER' => 'https://idp.example.org', 'OAUTH2_1_CLIENT_ID' => 'csms'])->oauthProviders[0]['response_mode']);
        assert_same('query', fp_config('query')->oauthProviders[0]['response_mode']);
        assert_same('form_post', fp_config()->oauthProviders[0]['response_mode']);
        assert_same(false, fp_config('query')->acceptsFormPostCallback());
        assert_same(true, fp_config()->acceptsFormPostCallback());

        $error = assert_throws(Throwable::class, static fn () => fp_config('fragment'));
        assert_contains('OAUTH2_1_RESPONSE_MODE must be query or form_post', $error->getMessage());
    });
});

describe('POST /auth/callback — the form_post response (REQ-CFG-041)', function (): void {
    it('runs without a session, parks the response and calls nothing', function (): void {
        $request = new Request('POST', '/auth/callback', [], [], ['code' => 'c', 'state' => 's']);
        assert_true(!Router::needsSession($request), 'the cross-site POST carries no session cookie to read');

        $response = fp_post('code=the-code&state=state-1');

        assert_same(303, $response->status(), 'no CSRF token is asked for: state and PKCE bind the response');
        assert_same(1, preg_match('/^[0-9a-f]{64}$/', fp_handle($response)));
        assert_same('no-store', $response->headers()['Cache-Control']);
        assert_same('no-referrer', $response->headers()['Referrer-Policy']);
        assert_same([], $GLOBALS['api_calls']);
        assert_not_contains('the-code', $response->headers()['Location'], 'the code never appears in an address');
    });

    it('is closed while no provider is configured for form_post', function (): void {
        $response = fp_post('code=the-code&state=state-1', fp_config('query'));

        assert_same(405, $response->status());
        assert_same('GET', $response->headers()['Allow']);
    });

    it('sends a malformed response on with a handle that redeems nothing', function (): void {
        foreach ([
            'state=state-1',                                         // neither code nor error
            'code=c',                                                // no state
            'code=c&error=access_denied&state=state-1',              // both
            'code=c&code=d&state=state-1',                           // two codes
            'code=c&state=state-1&state=state-2',                    // two states
            'code=' . str_repeat('c', 5000) . '&state=state-1',      // a field out of bounds
            'code=c&state=state-1&pad=' . str_repeat('x', OauthHandoff::MAX_BODY_BYTES),
        ] as $body) {
            $response = fp_post($body);
            assert_same(303, $response->status(), $body);
            assert_same('', fp_handle($response), substr($body, 0, 60));
        }
    });
});

describe('GET /auth/callback with a handoff (REQ-CFG-041)', function (): void {
    it('completes the sign-in the session started, with the parked code', function (): void {
        fp_begin('state-1');
        fp_login_answer();
        $handle = fp_handle(fp_post('code=the-code&state=state-1'));

        $response = fp_get(['handoff' => $handle]);

        assert_true(Session::isAuthenticated());
        assert_same('/', $response->headers()['Location'] ?? null);
        $login = api_request_body('/api/v1/auth/login');
        assert_same('the-code', $login['authorization_code']);
        assert_same(str_repeat('v', 64), $login['code_verifier']);
    });

    it('redeems a handle once', function (): void {
        fp_begin('state-1');
        fp_login_answer();
        $handle = fp_handle(fp_post('code=the-code&state=state-1'));
        fp_get(['handoff' => $handle]);
        Session::destroy();
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];
        fp_begin('state-1');
        resetApi();

        $again = fp_get(['handoff' => $handle]);

        assert_true(!Session::isAuthenticated());
        assert_same(0, api_calls_to('/api/v1/auth/login'));
        assert_contains('The sign-in attempt was interrupted', $again->body());
        assert_same('', Session::oauthTransactionPurpose(), 'the session\'s transaction is cleared');
    });

    it('needs the state of this browser\'s own transaction', function (): void {
        // An attacker's own response, parked and handed to a victim, carries the attacker's state.
        $handle = fp_handle(fp_post('code=attacker-code&state=attacker-state'));
        fp_begin('victim-state');
        fp_login_answer();

        fp_get(['handoff' => $handle]);

        assert_true(!Session::isAuthenticated());
        assert_same(0, api_calls_to('/api/v1/auth/login'));
    });

    it('treats an unknown, empty or expired handle as an interrupted sign-in', function (): void {
        foreach ([str_repeat('0', 64), '', '../../etc/passwd'] as $handle) {
            $_SESSION = ['csrf_token' => str_repeat('a', 64)];
            fp_begin('state-1');
            fp_login_answer();

            $response = fp_get(['handoff' => $handle, 'code' => 'query-code', 'state' => 'state-1']);

            assert_true(!Session::isAuthenticated(), 'beside a handle, the query\'s own code is ignored');
            assert_same(0, api_calls_to('/api/v1/auth/login'));
            assert_contains('The sign-in attempt was interrupted', $response->body());
            resetApi();
        }

        $store = new OauthHandoff(fp_config()->sessionDir);
        $handle = $store->put(['code' => 'c', 'state' => 's', 'error' => ''], 1_000);
        assert_same(null, $store->take((string) $handle, 1_000 + OauthHandoff::HANDLE_SECONDS + 1));
    });

    it('passes the provider\'s posted error on as a failed sign-in', function (): void {
        fp_begin('state-1');
        $handle = fp_handle(fp_post('error=access_denied&state=state-1'));

        $response = fp_get(['handoff' => $handle]);

        assert_true(!Session::isAuthenticated());
        assert_same(0, api_calls_to('/api/v1/auth/login'));
        assert_contains('The sign-in attempt was interrupted', $response->body());
    });

    it('keeps taking a query response, whatever the provider is configured for', function (): void {
        foreach (['form_post', 'query'] as $mode) {
            $_SESSION = ['csrf_token' => str_repeat('a', 64)];
            resetApi();
            fp_begin('state-1');
            fp_login_answer();

            fp_get(['code' => 'query-code', 'state' => 'state-1'], fp_config($mode));

            assert_true(Session::isAuthenticated(), $mode);
            assert_same('query-code', api_request_body('/api/v1/auth/login')['authorization_code']);
        }
    });
});

describe('a provider that does not offer form_post (REQ-CFG-041)', function (): void {
    it('stops the sign-in before the browser leaves, with a line for the administrator', function (): void {
        api_route('/api/v1/auth/oauth/start', ['error' => 'response_mode_unsupported', 'message' => '', 'status' => 502], 502);
        Session::selectSourceName('Hospital 1');

        $body = router_for(http_request('GET', '/login', browser_headers()), fp_config())->dispatch()->body();

        assert_contains('not set up the way CLARA asks for it', $body);
    });

    it('names the reason on the Authentication Test, and shows the configured mode', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/auth/oauth/start', ['error' => 'response_mode_unsupported', 'message' => '', 'status' => 502], 502);

        $body = router_for(http_request('POST', '/admin', browser_headers(), [
            'csrf_token' => str_repeat('a', 64), 'provider' => '1',
        ], ['action' => 'test_oauth']), fp_config())->dispatch()->body();

        assert_contains('does not list form_post in response_modes_supported', $body);
        assert_contains('<code>form_post</code>', $body);
    });
});

describe('Request::formValues', function (): void {
    it('lists every value of one field, decoded, in order', function (): void {
        assert_same(['a b', 'c&d'], Request::formValues('x=a+b&y=1&x=c%26d', 'x'));
        assert_same([''], Request::formValues('x=&y=1', 'x'));
        assert_same([], Request::formValues('', 'x'));
    });
});
