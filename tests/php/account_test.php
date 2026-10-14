<?php
// The account page (§2.8, REQ-UI-063): the header's account menu replaces the old footer, the
// Profile section carries language, theme and Sign out, and Project tokens shows the member's
// own tokens masked until an audited Show (REQ-API-162).
//
// What these cases are really about: a token value reaches the page only from the reveal
// call that recorded it, and appears in no other render — not the masked page, not the
// next GET after a Show.

declare(strict_types=1);

use Clara\Session;

const ACC_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const ACC_TOKEN = '0f1e2d3c-4b5a-4968-8776-655443322110';

function acc_memberships(): void
{
    api_route('/api/v1/users/me/projects', [
        ['project_id' => 3, 'project_name' => '8DISC', 'role' => null],
        ['project_id' => 5, 'project_name' => 'EMIT-23', 'role' => 'data-entry'],
    ]);
}

describe('shell header — account menu (§2.4, REQ-UI-063)', function (): void {
    it('names the user, links the account page, signs out in one click, and has no footer', function (): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();

        $body = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();

        assert_contains('id="clara-account-menu"', $body);
        assert_contains('Test Member', $body);
        assert_contains('href="/account"', $body);
        assert_contains('action="/logout"', $body);
        assert_not_contains('<footer', $body, 'the account entries moved off the footer');
        assert_not_contains('id="clara-language"', $body, 'the selectors live on the account page now');
    });
});

describe('account page — Profile (§2.8)', function (): void {
    it('shows name, address, the language and theme selectors and Sign out, in the account panel', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/account', browser_headers()))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_contains('member@example.org', $body);
        assert_contains('id="clara-language"', $body);
        assert_contains('id="clara-theme"', $body);
        assert_contains('<input type="hidden" name="next" value="/account">', $body);
        assert_contains('class="dropdown-item text-danger"', $body);
        assert_contains('id="clara-nav"', $body, 'a section-bearing page renders its left panel');
        assert_contains('href="/account/tokens"', $body);
        assert_contains('href="/account/two-factor"', $body);
        assert_contains('href="/account/password"', $body);
    });

    it('lists no Password section for an account that authenticates through a provider (GD-23)', function (): void {
        sign_in(['auth_source' => 'oauth2']);
        queue_shell();

        $body = router_for(http_request('GET', '/account', browser_headers()))->dispatch()->body();

        assert_contains('href="/account/tokens"', $body);
        assert_not_contains('href="/account/password"', $body);
    });

    it('gives the two-factor and password sections the same panel', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa', ['method' => 'none', 'enrolled_at' => null, 'recovery_codes_remaining' => 0]);

        foreach (['/account/two-factor', '/account/password'] as $path) {
            $body = router_for(http_request('GET', $path, browser_headers()))->dispatch()->body();
            assert_contains('id="clara-nav"', $body, $path);
            assert_contains('href="/account/tokens"', $body, $path);
            assert_contains('aria-current="page"', $body, $path);
        }
    });
});

describe('account page — Project tokens (§2.8, REQ-API-161/162)', function (): void {
    it('lists the memberships with every token masked and no value in the page', function (): void {
        sign_in();
        queue_shell();
        acc_memberships();

        $body = router_for(http_request('GET', '/account/tokens', browser_headers()))->dispatch()->body();

        assert_contains('8DISC', $body);
        assert_contains('EMIT-23', $body);
        assert_contains('data-entry', $body);
        assert_contains('No role (full permissions)', $body);
        assert_same(2, substr_count($body, 'action="/account/tokens?action=show_token"'));
        assert_same(0, api_calls_to('/token-reveal'), 'listing reveals nothing');
        assert_not_contains(ACC_TOKEN, $body);
    });

    it('shows a member without memberships one empty-state line', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/projects', []);

        $body = router_for(http_request('GET', '/account/tokens', browser_headers()))->dispatch()->body();

        assert_contains('not a member of any project', $body);
    });

    it('reveals one token from the audited call and renders it only in that response', function (): void {
        sign_in();
        queue_shell();
        // The more specific fragment first: the list's path is a prefix of the reveal's.
        api_route('/api/v1/users/me/projects/5/token-reveal', ['token' => ACC_TOKEN]);
        acc_memberships();

        $response = router_for(http_request('POST', '/account/tokens', browser_headers(),
            ['csrf_token' => ACC_CSRF, 'project_id' => '5'], ['action' => 'show_token']))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_same(1, api_calls_to('/api/v1/users/me/projects/5/token-reveal'));
        assert_contains('value="' . ACC_TOKEN . '"', $body);
        assert_contains('data-clara-copy="#clara-token-5"', $body);
        assert_contains('href="/account/tokens" data-clara-token-hide', $body);
        assert_same(1, substr_count($body, 'action="/account/tokens?action=show_token"'), 'the other project stays masked');
        assert_true(!str_contains(json_encode($_SESSION) ?: '', ACC_TOKEN), 'the value is kept nowhere');

        // Hide is the next GET: masked again, nothing revealed.
        $again = router_for(http_request('GET', '/account/tokens', browser_headers()))->dispatch()->body();
        assert_not_contains(ACC_TOKEN, $again);
        assert_same(1, api_calls_to('/api/v1/users/me/projects/5/token-reveal'));
    });

    it('keeps every token masked and says why when the API refuses the reveal', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/projects/9/token-reveal',
            ['error' => 'forbidden', 'message' => 'forbidden', 'status' => 403], 403);
        acc_memberships();

        $response = router_for(http_request('POST', '/account/tokens', browser_headers(),
            ['csrf_token' => ACC_CSRF, 'project_id' => '9'], ['action' => 'show_token']))->dispatch();

        assert_same(302, $response->status());
        assert_same('/account/tokens', $response->headers()['Location']);
        assert_contains('permission', implode(' ', array_column(Session::takeFlash(), 'text')));
    });

    it('refuses a missing project id without asking the API', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('POST', '/account/tokens', browser_headers(),
            ['csrf_token' => ACC_CSRF, 'project_id' => 'x'], ['action' => 'show_token']))->dispatch();

        assert_same(302, $response->status());
        assert_same(0, api_calls_to('/token-reveal'));
        assert_contains('project_id', implode(' ', array_column(Session::takeFlash(), 'text')));
    });

    it('rejects a Show without the CSRF token before any API call (REQ-UI-005)', function (): void {
        sign_in();
        queue_shell();

        router_for(http_request('POST', '/account/tokens', browser_headers(),
            ['csrf_token' => 'wrong', 'project_id' => '5'], ['action' => 'show_token']))->dispatch();

        assert_same(0, api_calls_to('/token-reveal'));
        Session::takeFlash();
    });
});
