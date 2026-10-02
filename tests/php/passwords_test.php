<?php
// The password lifecycle of §2.6 (GD-22/GD-23, Sequence H): the self-service change, the
// forgot-password request that discloses nothing, and the set-password page whose token is
// the only credential in play.
//
// What these cases are really about: a password never comes back or gets remembered; a
// public page never says whether an address exists; and every token problem is one
// indistinguishable answer (REQ-API-120).

declare(strict_types=1);

use Clara\Session;

const PWD_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

describe('change own password (§2.6, REQ-AUTH-061)', function (): void {
    it('shows the form with the address read-only for a local account', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();

        $response = router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('member@example.org', $response->body());
        assert_not_contains('name="email"', $response->body(), 'identity changes go through an administrator');
        assert_contains('name="current_password"', $response->body());
    });

    it('offers no form to an account that authenticates through a provider (GD-23)', function (): void {
        sign_in(['auth_source' => 'oauth2']);
        queue_shell();
        queue_sidebar_projects();

        $response = router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_not_contains('name="current_password"', $response->body());
        assert_contains('identity provider', $response->body());
    });

    it('sends exactly the two attributes the endpoint accepts (§7 rule 1)', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();
        api_route('/api/v1/users/me/password', ['ok' => true]);

        router_for(http_request(
            'POST', '/account/password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'current_password' => 'old secret phrase',
                'new_password' => 'a new long secret', 'repeat_password' => 'a new long secret'],
            ['action' => 'change']
        ))->dispatch();

        assert_same(['current_password' => 'old secret phrase', 'new_password' => 'a new long secret'],
            api_request_body('/api/v1/users/me/password'),
            'the repeat field is a client-side check, not an API attribute');
    });

    it('refuses a mismatched pair without asking the API at all', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();

        $response = router_for(http_request(
            'POST', '/account/password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'current_password' => 'old secret phrase',
                'new_password' => 'one long secret', 'repeat_password' => 'another long secret'],
            ['action' => 'change']
        ))->dispatch();

        assert_same(0, api_calls_to('/api/v1/users/me/password'));
        assert_contains('do not match', $response->body());
    });

    it('shows the one bad-password line and keeps nothing from the attempt', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();
        api_route('/api/v1/users/me/password',
            ['error' => 'bad_password', 'message' => 'the current password is not correct', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/account/password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'current_password' => 'wrong secret phrase',
                'new_password' => 'a new long secret', 'repeat_password' => 'a new long secret'],
            ['action' => 'change']
        ))->dispatch();

        assert_contains('The current password is not correct.', $response->body());
        assert_true(!str_contains(serialize($_SESSION), 'secret'), 'no credential of any kind enters the session (REQ-AUTH-036)');
    });

    it('names the policy the API rejected, the way §3.4 asks for invalid_request', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();
        api_route('/api/v1/users/me/password',
            ['error' => 'invalid_request', 'message' => 'password must be at least 12 characters', 'status' => 400], 400);

        $response = router_for(http_request(
            'POST', '/account/password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'current_password' => 'old secret phrase',
                'new_password' => 'short', 'repeat_password' => 'short'],
            ['action' => 'change']
        ))->dispatch();

        assert_contains('at least 12 characters', $response->body());
    });

    it('confirms through the flash on the page after, with empty fields (REQ-AUTH-036)', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();
        api_route('/api/v1/users/me/password', ['ok' => true]);

        $response = router_for(http_request(
            'POST', '/account/password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'current_password' => 'old secret phrase',
                'new_password' => 'a new long secret', 'repeat_password' => 'a new long secret'],
            ['action' => 'change']
        ))->dispatch();

        assert_same(302, $response->status());
        assert_same('/account/password', $response->headers()['Location']);
        assert_contains('password has been changed', (Session::takeFlash()[0]['text'] ?? ''));
    });
});

describe('forgot password (§2.6, REQ-AUTH-062)', function (): void {
    it('asks for one address and nothing else', function (): void {
        queue_shell();

        $response = router_for(http_request('GET', '/password-reset', browser_headers()))->dispatch();

        assert_contains('name="email"', $response->body());
        assert_contains('/login', $response->body(), 'the way back is always offered');
    });

    it('answers the same sentence whether or not anything was sent', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];
        api_route('/api/v1/auth/password-reset/request', ['ok' => true], 202);

        $response = router_for(http_request(
            'POST', '/password-reset', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'email' => 'nobody@example.org'], ['action' => 'request']
        ))->dispatch();

        assert_contains('If that address has an account with a local password, a reset link has been sent.',
            $response->body());
        // The address is not echoed into the confirmation: it would pair an address with
        // "yes, that one works" as surely as a different sentence would.
        assert_not_contains('nobody@example.org', $response->body());
    });

    it('shows the wait when the address is rate limited', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];
        api_route('/api/v1/auth/password-reset/request',
            ['error' => 'rate_limited', 'message' => 'too many requests for this address — try again later', 'status' => 429], 429);

        $response = router_for(http_request(
            'POST', '/password-reset', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'email' => 'somebody@example.org'], ['action' => 'request']
        ))->dispatch();

        assert_contains('Too many attempts', $response->body());
        assert_not_contains('a reset link has been sent', $response->body());
    });
});

describe('set password from a token (§2.6, Sequence H)', function (): void {
    it('says so plainly when the link carried no token', function (): void {
        queue_shell();

        $response = router_for(http_request('GET', '/set-password', browser_headers()))->dispatch();

        assert_contains('does not work', $response->body());
        assert_not_contains('name="new_password"', $response->body(), 'there is nothing for the form to authorise');
    });

    it('carries the token in one hidden field and nowhere else (§2.6)', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];

        $response = router_for(http_request(
            'GET', '/set-password', browser_headers(), [], ['token' => 'abc123tokendef456']
        ))->dispatch();

        assert_contains('type="hidden" name="token" value="abc123tokendef456"', $response->body());
        assert_same(1, substr_count($response->body(), 'abc123tokendef456'), 'one place, so it cannot leak from another');
    });

    it('redeems an invite token and sends the user to sign in', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];
        api_route('/api/v1/auth/invite/complete', ['ok' => true]);

        $response = router_for(http_request(
            'POST', '/set-password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'token' => 'invitetoken', 'new_password' => 'a new long secret',
                'repeat_password' => 'a new long secret'],
            ['action' => 'complete']
        ))->dispatch();

        assert_contains('Password set', $response->body());
        assert_same(0, api_calls_to('/api/v1/auth/password-reset/complete'),
            'the first purpose that accepts the token ends the flow');
    });

    it('falls through to the reset purpose, since the link names neither (REQ-API-120)', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];
        api_route('/api/v1/auth/invite/complete',
            ['error' => 'invalid_setup_token', 'message' => '', 'status' => 401], 401);
        api_route('/api/v1/auth/password-reset/complete', ['ok' => true]);

        $response = router_for(http_request(
            'POST', '/set-password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'token' => 'resettoken', 'new_password' => 'a new long secret',
                'repeat_password' => 'a new long secret'],
            ['action' => 'complete']
        ))->dispatch();

        assert_contains('Password set', $response->body());
        assert_same(1, api_calls_to('/api/v1/auth/password-reset/complete'));
    });

    it('answers every token problem with the same line and establishes no session', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];
        api_route('/api/v1/auth/invite/complete',
            ['error' => 'invalid_setup_token', 'message' => '', 'status' => 401], 401);
        api_route('/api/v1/auth/password-reset/complete',
            ['error' => 'invalid_setup_token', 'message' => '', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/set-password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'token' => 'deadtoken', 'new_password' => 'a new long secret',
                'repeat_password' => 'a new long secret'],
            ['action' => 'complete']
        ))->dispatch();

        assert_contains('does not work', $response->body());
        assert_true(!Session::isAuthenticated(), 'completing a password page never signs anybody in (§2.8)');
        // Neither the purpose nor the token ever appears: what failed is not disclosed, and
        // neither is the credential that was presented (§2.6, REQ-API-120).
        assert_not_contains('invite', strtolower($response->body()));
        assert_not_contains('deadtoken', $response->body());
    });

    it('checks the repeat field before either endpoint is called', function (): void {
        queue_shell();
        $_SESSION = ['csrf_token' => PWD_CSRF];

        $response = router_for(http_request(
            'POST', '/set-password', browser_headers(),
            ['csrf_token' => PWD_CSRF, 'token' => 'anytoken', 'new_password' => 'one long secret',
                'repeat_password' => 'two long secrets'],
            ['action' => 'complete']
        ))->dispatch();

        assert_same(0, api_calls_to('/auth/invite/complete'));
        assert_contains('do not match', $response->body());
    });
});
