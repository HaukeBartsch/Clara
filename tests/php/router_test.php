<?php
// Router dispatch, including both shapes of one route (REQ-UI-044): the guard runs
// before either shape, a data request answers with the API's failure envelope
// rather than HTML, and a route that declares no region refuses JSON with 406.

declare(strict_types=1);

use Clara\Request;
use Clara\Response;
use Clara\Router;
use Clara\Session;

// router_for(), http_request(), browser_headers() and json_headers() live in
// support.php: every suite that drives a dispatch needs the same object graph.

describe('router — guard and dispatch', function (): void {
    it('sends an anonymous visitor to the login page', function (): void {
        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(302, $response->status());
        assert_same('/login', $response->headers()['Location']);
    });

    it('does not remember the dashboard as a destination', function (): void {
        // '/' is where an anonymous visitor ends up anyway, so no next= is added.
        $request = new Request('GET', '/', browser_headers(), ['next' => '/projects/4'], []);
        $response = router_for($request)->dispatch();

        assert_same('/login', $response->headers()['Location']);
    });

    it('renders the dashboard for a signed-in user and declares its data region', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 5, 'field_count' => 128]]);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('data-region="projects"', $response->body());
    });

    it('renders the project overview as a single panel with no left panel (§2.4 A, REQ-UI-009)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 5, 'field_count' => 128]]);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        // The first screen after login is information, not navigation: no left panel at all,
        // and so no off-canvas toggle for one either (REQ-UI-046).
        assert_not_contains('id="clara-nav"', $response->body());
        // An ordinary member sees no administration entry point anywhere (REQ-UI-003).
        assert_not_contains('href="/admin"', $response->body());
    });

    it('offers an administrator the Control Panel button in the header (§2.4, REQ-UI-009/047)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 5, 'field_count' => 128]]);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_contains('href="/admin"', $response->body());
        assert_contains('Control Panel', $response->body());
    });

    it('serves the same route as JSON when Accept asks for it (REQ-UI-044)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 5, 'field_count' => 128]]);

        $response = router_for(http_request('GET', '/', json_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('application/json', $response->headers()['Content-Type']);
        $payload = json_decode($response->body(), true);
        assert_same(1, count($payload));
        assert_same('8DISC', $payload[0]['project_name']);
    });

    it('discloses no more in the data region than the page shows', function (): void {
        sign_in();
        queue_shell();
        // The API row carries fields the dashboard does not render.
        api_route('/api/v1/projects', [['id' => 1, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 5, 'field_count' => 128, 'pi_email' => 'pi@example.org']]);

        $response = router_for(http_request('GET', '/', json_headers()))->dispatch();

        assert_not_contains('pi@example.org', $response->body());
        assert_not_contains('pi_email', $response->body());
    });

    it('answers an unauthenticated data request with the failure envelope, not HTML', function (): void {
        $response = router_for(http_request('GET', '/', json_headers()))->dispatch();

        assert_same(403, $response->status());
        $payload = json_decode($response->body(), true);
        assert_same('forbidden', $payload['error']);
        assert_not_contains('<!doctype html>', $response->body());
    });

    it('refuses JSON for a route that declares no data region', function (): void {
        queue_shell();

        $response = router_for(http_request('GET', '/login', json_headers()))->dispatch();

        assert_same(406, $response->status());
    });

    it('answers an unknown route with 404', function (): void {
        $response = router_for(http_request('GET', '/nope', browser_headers()))->dispatch();

        assert_same(404, $response->status());
    });

    it('sends a signed-in user to the no-access page instead of an empty dashboard (REQ-UI-006)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', []);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('No projects yet', $response->body());
    });

    it('makes one API call for the dashboard read — no fan-out per row (§7 rule 13)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', [
            ['id' => 1, 'project_name' => 'A', 'organization' => '', 'record_count' => 1, 'instrument_count' => 1, 'field_count' => 1],
            ['id' => 2, 'project_name' => 'B', 'organization' => '', 'record_count' => 2, 'instrument_count' => 2, 'field_count' => 2],
        ]);

        router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(1, api_calls_to('/api/v1/projects'), 'two rows must not cost two reads');
    });
});

describe('router — CSRF (REQ-UI-005)', function (): void {
    it('rejects a mutation without a token and keeps the session', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('POST', '/logout', browser_headers()))->dispatch();

        // Back to the page, still signed in, and no logout call was made.
        assert_same(302, $response->status());
        assert_true(Session::isAuthenticated(), 'the session must survive a CSRF rejection');
        assert_same(0, count(array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], '/auth/logout'))));
    });

    it('rejects a wrong token', function (): void {
        sign_in();
        queue_shell();

        router_for(http_request('POST', '/logout', browser_headers(), ['csrf_token' => str_repeat('b', 64)]))->dispatch();

        assert_true(Session::isAuthenticated(), 'a wrong token must not end the session');
    });

    it('accepts the token from a form field', function (): void {
        sign_in();
        queue_shell();
        api_route('/auth/logout', '');

        $response = router_for(http_request('POST', '/logout', browser_headers(), ['csrf_token' => str_repeat('a', 64)]))->dispatch();

        assert_same(302, $response->status());
        assert_same('/login', $response->headers()['Location']);
    });
});

describe('login — the local credential path (Sequences F + C)', function (): void {
    it('establishes a session and lands on the dashboard', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['id' => 7, 'email' => 'admin@example.org', 'display_name' => 'Administrator',
            'is_admin' => true, 'auth_source' => 'local', 'ui_language' => 'en', 'ui_theme' => null]);

        $response = router_for(http_request(
            'POST',
            '/login',
            browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'admin@example.org', 'password' => 'correct horse'],
            ['action' => 'credentials']
        ))->dispatch();

        assert_same(302, $response->status());
        assert_same('/', $response->headers()['Location']);
        assert_true(Session::isAuthenticated(), 'the login must establish a session');
        assert_same(7, Session::userId());
        assert_true(Session::isAdmin(), 'the admin flag comes from the API response');
    });

    it('honours a same-site next= after login', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['id' => 7, 'email' => 'a@example.org', 'display_name' => 'A', 'is_admin' => false]);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'],
            ['action' => 'credentials', 'next' => '/projects/4']
        ))->dispatch();

        assert_same('/projects/4', $response->headers()['Location']);
    });

    it('refuses a scheme-relative next= (REQ-TECH-024)', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['id' => 7, 'email' => 'a@example.org', 'display_name' => 'A', 'is_admin' => false]);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'],
            ['action' => 'credentials', 'next' => '//evil.example.org/phishing']
        ))->dispatch();

        assert_same('/', $response->headers()['Location']);
    });

    it('shows one translated line for a wrong password and no session', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['error' => 'bad_password', 'message' => 'invalid email or password', 'status' => 401], 401);
        // A failed race reports itself once, through the login endpoint carrying the
        // per-source outcomes — that call is what writes the submission's audit row, and
        // it cannot authenticate whatever it says (REQ-API-135).
        api_route('/auth/login', ['error' => 'bad_password', 'message' => 'invalid email or password', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'wrong'], ['action' => 'credentials']
        ))->dispatch();

        assert_true(!Session::isAuthenticated(), 'a rejected credential must not sign anyone in');
        assert_contains('not recognised', $response->body());

        assert_same(1, api_calls_to('/auth/login'), 'exactly one login_failure per submission (§2.9)');
        $reported = api_request_body('/auth/login');
        assert_same(['local' => 'bad_password'], $reported['attempts']);
        assert_true(!array_key_exists('password', $reported), 'the failure report carries no password (REQ-API-135)');
    });

    it('names the account state when the account is disabled, not a password failure', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['error' => 'account_disabled', 'message' => 'this account is disabled', 'status' => 403], 403);
        // The account's own state outranks the generic credential line, and the API says
        // which it is rather than PHP guessing (§2.6 step 4).
        api_route('/auth/login', ['error' => 'account_disabled', 'message' => 'this account is disabled', 'status' => 403], 403);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'], ['action' => 'credentials']
        ))->dispatch();

        assert_contains('disabled', $response->body());
        assert_not_contains('password combination', $response->body());
    });

    it('opens the two-factor panel rather than reporting a bad password', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok']);
        api_route('/auth/login', ['error' => 'mfa_required', 'method' => 'totp', 'message' => 'second factor required', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'], ['action' => 'credentials']
        ))->dispatch();

        // A flow state is not a failure: the page asks for a code, and says
        // nothing about the credential that was in fact accepted (§2.2).
        assert_contains('Confirm it is you', $response->body());
        assert_not_contains('not recognised', $response->body());
    });
});
