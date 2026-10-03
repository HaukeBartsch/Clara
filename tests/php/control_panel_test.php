<?php
// The Control Panel (§5, REQ-UI-047): one is_admin page whose left panel selects the
// section and whose right-hand panel shows it; `?section=` behaves like `?action=` — an
// allowlisted parameter resolved in PHP, unknown values falling back rather than failing.
// Users, Projects, Audits, Translations (M3, m3_admin_test.php) and Settings (§5.8, REQ-UI-043)
// are the sections this build serves.

declare(strict_types=1);

const CP_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/** The §4.22 settings object as the API returns it. */
function cp_settings(array $overrides = []): array
{
    return array_merge([
        'rate_limit_enabled' => true,
        'rate_limit_rpm' => 600,
        'rate_limit_block_minutes' => 10,
    ], $overrides);
}

describe('Control Panel (§5, REQ-UI-047)', function (): void {
    it('shows the section selected in the left panel, with its values', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', cp_settings());

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'settings']))->dispatch();

        assert_same(200, $response->status());
        assert_contains('System settings', $response->body());
        assert_contains('value="600"', $response->body());
        // The panel is rendered and this section is the one on screen (§2.4 B).
        assert_contains('id="clara-nav"', $response->body());
        assert_contains('aria-current="page"', $response->body());
    });

    it('defaults to the first available section, and an unknown pick is not an error (§2.1)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', [['id' => 1, 'email' => 'admin@example.org', 'display_name' => 'Admin',
            'enabled' => true, 'is_admin' => true, 'auth_source' => 'local', 'status' => 'active', 'tfa_method' => 'off']]);

        $plain = router_for(http_request('GET', '/admin', browser_headers()))->dispatch();
        $unknown = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'nonsense']))->dispatch();

        // Users is the first section of the panel (§5, REQ-UI-047).
        assert_same(200, $plain->status());
        assert_contains('admin@example.org', $plain->body());
        assert_contains('action=create_user', $plain->body());
        assert_same(200, $unknown->status(), 'a section this build does not serve renders the first available one');
        assert_contains('action=create_user', $unknown->body());
    });

    it('lists only the sections this build serves (§3.1)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', cp_settings());

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'settings']))->dispatch();

        // All five sections are built since M3, in the panel's canonical order (§5).
        $body = $response->body();
        $positions = array_map(static fn (string $s): int|false => strpos($body, 'href="/admin?section=' . $s . '"'),
            ['users', 'projects', 'audits', 'translations', 'settings']);
        assert_true(!in_array(false, $positions, true), 'every section has its panel entry');
        $sorted = $positions;
        sort($sorted);
        assert_same($sorted, $positions, 'the entries keep the canonical order');
    });

    it('is closed to anyone who is not an administrator', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/admin', browser_headers()))->dispatch();

        assert_same(403, $response->status());
        assert_same(0, api_calls_to('/api/v1/settings'), 'the guard answers before any read');
    });

    it('sends a signed-out browser to the login page', function (): void {
        $response = router_for(http_request('GET', '/admin', browser_headers()))->dispatch();

        assert_same(302, $response->status());
        assert_contains('/login', $response->headers()['Location']);
    });

    it('saves with exactly the three attributes and their declared types (§7 rule 1)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', cp_settings());

        $response = router_for(http_request(
            'POST', '/admin', browser_headers(),
            ['csrf_token' => CP_CSRF, 'section' => 'settings', 'rate_limit_enabled' => '1',
                'rate_limit_rpm' => '300', 'rate_limit_block_minutes' => '5'],
            ['action' => 'save_settings']
        ))->dispatch();

        assert_same(302, $response->status());
        assert_same('/admin?section=settings', $response->headers()['Location']);
        assert_same([
            'rate_limit_enabled' => true,
            'rate_limit_rpm' => 300,
            'rate_limit_block_minutes' => 5,
        ], api_request_body('/api/v1/settings'));
    });

    it('reads an unchecked switch as false rather than dropping the attribute', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', cp_settings());

        router_for(http_request(
            'POST', '/admin', browser_headers(),
            ['csrf_token' => CP_CSRF, 'section' => 'settings',
                'rate_limit_rpm' => '600', 'rate_limit_block_minutes' => '10'],
            ['action' => 'save_settings']
        ))->dispatch();

        assert_same(false, api_request_body('/api/v1/settings')['rate_limit_enabled']);
    });

    it('keeps a non-numeric limit out of the API and says so', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', cp_settings());

        $response = router_for(http_request(
            'POST', '/admin', browser_headers(),
            ['csrf_token' => CP_CSRF, 'section' => 'settings', 'rate_limit_enabled' => '1',
                'rate_limit_rpm' => 'lots', 'rate_limit_block_minutes' => '10'],
            ['action' => 'save_settings']
        ))->dispatch();

        assert_same(0, api_calls_to('/api/v1/settings'), 'a value that is not a number is never sent');
        assert_contains('whole numbers', $response->body());
    });

    it('shows the API refusal and keeps what was typed (§3.4)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        // A rejected save re-renders with the submitted values, so this render makes one
        // call only — the PUT — and the queue holds nothing but its refusal.
        api_route('/api/v1/settings', ['error' => 'invalid_request', 'message' => 'rate_limit_block_minutes must be an integer 1..1440', 'status' => 400], 400);

        $response = router_for(http_request(
            'POST', '/admin', browser_headers(),
            ['csrf_token' => CP_CSRF, 'section' => 'settings', 'rate_limit_enabled' => '1',
                'rate_limit_rpm' => '300', 'rate_limit_block_minutes' => '5000'],
            ['action' => 'save_settings']
        ))->dispatch();

        assert_contains('value="5000"', $response->body(), 'the rejected value is shown back, not silently reverted');
    });

    it('refuses the mutation with no CSRF token, before any API call (§3.3)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $response = router_for(http_request(
            'POST', '/admin', browser_headers(),
            ['section' => 'settings', 'rate_limit_rpm' => '1'],
            ['action' => 'save_settings']
        ))->dispatch();

        assert_same(0, api_calls_to('/api/v1/settings'), 'the token is checked before the write');
        // Back to the page the mutation aimed at, carrying the translated failure line as a
        // flash — the settings read happens on that next render (§3.3).
        assert_same(302, $response->status());
        assert_same('/admin', $response->headers()['Location']);
    });
});
