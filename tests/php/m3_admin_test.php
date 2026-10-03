<?php
// M3 — the administration surface (Plan/Web_Implementation.md §6): the Control Panel's Users,
// Projects, Audits and Translations sections (§5.1/§5.2/§5.6/§5.7, REQ-UI-047) and the project
// page's Members, Roles and Groups (§5.3/§5.4/§5.5). What is asserted here is what review
// would otherwise have to check by hand: each write carries exactly its endpoint's attributes
// (§7 rule 1), a token is shown once (§3.5), destructive actions confirm first, gates hold,
// and stored values come back escaped (§3.2).

declare(strict_types=1);

use Clara\Session;

const M3_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

function m3_user(array $overrides = []): array
{
    return array_merge([
        'id' => 9, 'email' => 'ada@example.org', 'display_name' => 'Ada', 'enabled' => true, 'is_admin' => false,
        'auth_source' => 'local', 'last_login_at' => null, 'valid_until' => null, 'status' => 'active', 'tfa_method' => 'off',
    ], $overrides);
}

function m3_post(string $path, array $fields, string $action): \Clara\Response
{
    return router_for(http_request('POST', $path, browser_headers(), ['csrf_token' => M3_CSRF] + $fields, ['action' => $action]))->dispatch();
}

/** The method and URL of every API call whose URL contains the fragment. */
function m3_calls(string $fragment): array
{
    return array_values(array_map(
        static fn (array $c): string => $c['method'] . ' ' . parse_url($c['url'], PHP_URL_PATH),
        array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], $fragment))
    ));
}

/** The §4.5 detail object of project 3 (a view_edit member, one arm). */
function m3_detail(): array
{
    return [
        'id' => 3, 'project_name' => '8DISC', 'organization' => 'NAT EU', 'record_count' => 0,
        'arms' => [['arm_num' => 1, 'name' => null, 'events' => []]], 'instruments' => [],
        'permissions' => ['project_admin' => false,
            'arms' => [['arm_num' => 1, 'data_access_level' => 'view_edit', 'export_level' => 'export_none']]],
    ];
}

/** Queues what a project-page section render reads besides its own data. */
function m3_queue_project(): void
{
    queue_shell();
    api_route('/api/v1/projects/3/mode', ['mode' => 'development', 'staging_open' => false]);
}

describe('project overview for an administrator (§2.3, §4)', function (): void {
    it('shows an administrator the empty projects table, not the no-access page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', []);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('data-region="projects"', $response->body());
        assert_not_contains('No projects yet</h1>', $response->body());
    });

    it('still shows a member without projects the no-access page (REQ-UI-006)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects', []);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_contains('No projects yet', $response->body());
        assert_not_contains('data-region="projects"', $response->body());
    });
});

describe('Control Panel — Users (§5.1, REQ-UI-011)', function (): void {
    it('lists accounts with the derived status, and escapes what it shows', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', [m3_user(['display_name' => '<script>x</script>', 'status' => 'auto_disabled', 'enabled' => false])]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users']))->dispatch()->body();

        assert_contains('ada@example.org', $body);
        assert_contains('auto-disabled', $body);
        assert_contains('&lt;script&gt;x&lt;/script&gt;', $body);
        assert_not_contains('<script>x</script>', $body);
    });

    it('creates with exactly the whitelisted attributes, and no password when none is given', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', m3_user(), 201);

        $response = m3_post('/admin', ['email' => 'new@example.org', 'display_name' => 'New', 'valid_days' => '30',
            'password' => '', 'repeat_password' => ''], 'create_user');

        assert_same(302, $response->status());
        assert_same('/admin?section=users', $response->headers()['Location']);
        assert_same(['email' => 'new@example.org', 'display_name' => 'New', 'valid_days' => 30], api_request_body('/api/v1/users'));
    });

    it('refuses mismatched passwords before any API call and keeps no password in the page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', [m3_user()]);

        $response = m3_post('/admin', ['email' => 'new@example.org', 'display_name' => 'New', 'valid_days' => '0',
            'password' => 'first-password-1', 'repeat_password' => 'other-password-2'], 'create_user');

        assert_same(200, $response->status());
        assert_same([], array_filter(m3_calls('/api/v1/users'), static fn (string $c): bool => str_starts_with($c, 'POST')));
        assert_contains('value="new@example.org"', $response->body());
        assert_not_contains('first-password-1', $response->body());
    });

    it('shows the API conflict on the re-rendered form (§3.4)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', ['error' => 'conflict', 'message' => 'an enabled account with this email already exists', 'status' => 409], 409);
        api_route('/api/v1/users', [m3_user()]); // the re-rendered page's list read

        $response = m3_post('/admin', ['email' => 'ada@example.org', 'display_name' => 'Ada', 'valid_days' => '0'], 'create_user');

        assert_contains('an enabled account with this email already exists', $response->body());
    });

    it('sends one attribute per row action (§7 rule 1)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users/9', m3_user(['enabled' => false]));

        m3_post('/admin', ['id' => '9', 'email' => 'ada@example.org', 'enabled' => '0'], 'user_enabled');

        assert_same(['PUT /api/v1/users/9'], m3_calls('/api/v1/users/9'));
        assert_same(['enabled' => false], api_request_body('/api/v1/users/9'));
    });

    it('asks for confirmation before disabling, resetting two-factor and changing the admin flag (§3.5)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users', [m3_user(['tfa_method' => 'totp'])]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users']))->dispatch()->body();

        assert_same(1, preg_match('/action=user_enabled"[^>]*data-clara-confirm=/', $body), 'disable confirms');
        assert_same(1, preg_match('/action=user_tfa_reset"[^>]*data-clara-confirm=/', $body), 'two-factor reset confirms');
        assert_same(1, preg_match('/action=user_admin"[^>]*data-clara-confirm=/', $body), 'admin flag confirms');
        assert_contains('/assets/js/admin.js', $body);
    });

    it('explains a missing mail relay instead of failing silently (REQ-CFG-028)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users/9/invite', ['error' => 'smtp_not_configured', 'message' => 'x', 'status' => 409], 409);

        m3_post('/admin', ['id' => '9', 'email' => 'ada@example.org'], 'user_invite');

        assert_contains('E-mail delivery is not configured', implode(' ', array_column(Session::takeFlash(), 'text')));
    });

    it('follows a self-revoke: the cached flag is cleared and the Control Panel is left', function (): void {
        sign_in(['is_admin' => 1, 'user_id' => 7]);
        queue_shell();
        api_route('/api/v1/users/7', m3_user(['id' => 7, 'is_admin' => false]));

        $response = m3_post('/admin', ['id' => '7', 'email' => 'member@example.org', 'is_admin' => '0'], 'user_admin');

        assert_same(['is_admin' => false], api_request_body('/api/v1/users/7'));
        assert_same('/', $response->headers()['Location']);
        assert_true(!Session::isAdmin(), 'the session no longer claims the flag');
    });
});

describe('Control Panel — Projects (§5.2, REQ-UI-012)', function (): void {
    it('creates with the required fields and only the optional fields that were filled', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', ['id' => 12, 'project_name' => 'EMIT'], 201);

        $response = m3_post('/admin', ['project_name' => 'EMIT', 'organization' => 'HBE', 'pi_name' => 'PI',
            'pi_email' => 'pi@example.org', 'participant_names' => 'E[0-9][0-9]', 'rek_number' => 'REK-1',
            'dm_name' => '', 'start_date' => ''], 'create_project');

        assert_same('/admin?section=projects&created=12', $response->headers()['Location']);
        assert_same(['project_name' => 'EMIT', 'organization' => 'HBE', 'pi_name' => 'PI', 'pi_email' => 'pi@example.org',
            'participant_names' => 'E[0-9][0-9]', 'rek_number' => 'REK-1'], api_request_body('/api/v1/projects'));
    });

    it('clears an emptied optional field on edit (null), keeping the others', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects/12', ['id' => 12]);

        m3_post('/admin', ['id' => '12', 'project_name' => 'EMIT', 'organization' => 'HBE', 'pi_name' => 'PI',
            'pi_email' => 'pi@example.org', 'participant_names' => 'E[0-9][0-9]', 'dm_name' => ''], 'update_project');

        $body = api_request_body('/api/v1/projects/12');
        assert_same(null, $body['dm_name']);
        assert_same('EMIT', $body['project_name']);
        assert_same(['PUT /api/v1/projects/12'], m3_calls('/api/v1/projects/12'));
    });
});

describe('Control Panel — Audits (§5.6, REQ-UI-016)', function (): void {
    it('passes only allowlisted filters to the API, and escapes every detail value', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit', ['entries' => [[
            'id' => 1, 'created_at' => '2026-10-03 08:00:00', 'source' => 'ui', 'project_id' => null, 'user_id' => 1,
            'email' => 'admin@example.org', 'event_type' => 'user_created', 'details' => ['display_name' => '<b>x</b>'],
        ]], 'next_cursor' => 'abc123']);
        api_route('/api/v1/projects', []);
        api_route('/api/v1/users', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], [
            'section' => 'audits', 'event_type' => 'not_a_code', 'from' => '2026-10-01', 'to' => 'yesterday',
        ]))->dispatch()->body();

        $url = '';
        foreach ($GLOBALS['api_calls'] as $call) {
            if (str_contains($call['url'], '/api/v1/audit')) {
                $url = $call['url'];
            }
        }
        assert_contains('from=2026-10-01', $url);
        assert_not_contains('event_type=', $url, 'an unknown event code is not forwarded');
        assert_not_contains('to=', $url, 'a malformed date is not forwarded');
        assert_contains('&lt;b&gt;x&lt;/b&gt;', $body);
        assert_contains('cursor=abc123', $body, 'the next page carries the opaque cursor');
    });
});

describe('Control Panel — Translations (§5.7, REQ-UI-030)', function (): void {
    it('lists the code catalog with the missing badge and saves only changed entries', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/i18n/strings', [['key' => 'login.heading', 'text' => 'Logg inn', 'missing' => false]]);
        api_route('/api/v1/i18n/languages', [['code' => 'en', 'display_name' => 'English'], ['code' => 'nb', 'display_name' => 'Norsk bokmål']]);

        $page = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'translations', 'language' => 'nb']))->dispatch();
        assert_contains('value="Logg inn"', $page->body());
        assert_contains('name="text[nav.sign_out]"', $page->body(), 'a code key without a translation is listed');

        m3_post('/admin', ['language' => 'nb', 'text' => [
            'login.heading' => 'Logg inn', // unchanged → not sent
            'nav.sign_out' => 'Logg ut',   // new → sent
            'nav.overview' => '',          // still missing → not sent
        ]], 'save_translations');

        assert_same(['language' => 'nb', 'entries' => [['key' => 'nav.sign_out', 'text' => 'Logg ut']]], m3_last_put('/api/v1/i18n/strings'));
    });
});

/** The body of the last PUT to an endpoint. */
function m3_last_put(string $fragment): array
{
    $body = [];
    foreach ($GLOBALS['api_calls'] as $call) {
        if ($call['method'] === 'PUT' && str_contains($call['url'], $fragment)) {
            $body = json_decode((string) $call['body'], true) ?: [];
        }
    }

    return $body;
}

describe('project page — Members (§5.3, REQ-UI-013)', function (): void {
    it('is closed to a project member who is not an administrator', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch();

        assert_same(403, $response->status());
        assert_same(0, api_calls_to('/api/v1/projects/3/users'));
    });

    it('adds with the role name only, and shows the new token exactly once', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'ada@example.org', 'token' => 'tok-secret-123'], 201);

        $post = m3_post('/projects/3/members', ['user_id' => '9', 'role' => 'data-entry'], 'add_member');
        assert_same('/projects/3/members', $post->headers()['Location']);
        assert_same(['role' => 'data-entry'], m3_last_put('/api/v1/projects/3/users/9'));
        // The add form names the account by id only: the line names it from the API's answer.
        assert_contains('ada@example.org added.', implode(' ', array_column(Session::takeFlash(), 'text')));

        api_route('/api/v1/projects/3/users', []);
        api_route('/api/v1/projects/3/roles', []);
        api_route('/api/v1/users', []);
        api_route('/api/v1/projects/3', m3_detail());
        $first = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        $second = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();

        assert_contains('value="tok-secret-123"', $first);
        assert_contains('data-clara-copy="#clara-new-token"', $first);
        assert_not_contains('tok-secret-123', $second, 'the token is shown once');
    });

    it('sends "no role" as null, rotation and removal as their single flag', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'token' => 't2']);

        m3_post('/projects/3/members', ['user_id' => '9', 'role' => ''], 'change_role');
        assert_same(['role' => null], m3_last_put('/api/v1/projects/3/users/9'));
        m3_post('/projects/3/members', ['user_id' => '9'], 'rotate_token');
        assert_same(['rotate_token' => true], m3_last_put('/api/v1/projects/3/users/9'));
        m3_post('/projects/3/members', ['user_id' => '9'], 'remove_member');
        assert_same(['remove' => true], m3_last_put('/api/v1/projects/3/users/9'));
    });

    it('confirms before rotating or removing (§3.5) and never prints a stored token', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users', [['user_id' => 9, 'email' => 'ada@example.org', 'display_name' => 'Ada',
            'role' => null, 'token_present' => true, 'enabled' => true]]);
        api_route('/api/v1/projects/3/roles', []);
        api_route('/api/v1/users', []);
        api_route('/api/v1/projects/3', m3_detail());

        $body = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();

        assert_same(1, preg_match('/action=rotate_token"[^>]*data-clara-confirm=/', $body));
        assert_same(1, preg_match('/action=remove_member"[^>]*data-clara-confirm=/', $body));
        assert_contains('No role (full permissions)', $body);
        assert_not_contains('clara-new-token', $body);
    });
});

describe('project page — Roles (§5.4, REQ-UI-014)', function (): void {
    it('creates with name, project_admin and one level pair per arm', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/roles', ['id' => 1, 'name' => 'data-entry'], 201);

        m3_post('/projects/3/roles', ['name' => 'data-entry', 'data' => ['1' => 'view_edit'], 'export' => ['1' => 'bogus']], 'create_role');

        assert_same(['name' => 'data-entry', 'project_admin' => false,
            'arms' => ['1' => ['data' => 'view_edit', 'export' => 'export_none']]], api_request_body('/api/v1/projects/3/roles'));
    });
});

describe('project page — Groups (§5.5, REQ-UI-015)', function (): void {
    it('lets a member with data access read, but emits no create or delete control', function (): void {
        sign_in();
        m3_queue_project();
        api_route('/api/v1/projects/3/data-access-groups', [['id' => 4, 'name' => 'Bergen']]);
        api_route('/api/v1/projects/3', m3_detail());

        $body = router_for(http_request('GET', '/projects/3/groups', browser_headers()))->dispatch()->body();

        assert_contains('Bergen', $body);
        assert_not_contains('action=create_group', $body);
        assert_not_contains('action=delete_group', $body);
    });

    it('deletes through the API after a confirmation', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/data-access-groups/4', ['ok' => true]);

        m3_post('/projects/3/groups', ['group_id' => '4', 'name' => 'Bergen'], 'delete_group');

        assert_same(['DELETE /api/v1/projects/3/data-access-groups/4'], m3_calls('/api/v1/projects/3/data-access-groups/4'));
    });
});

describe('footer switches (§2.1, §3.8, §9)', function (): void {
    it('reaches the language and theme handlers with the bare POST the footer forms send', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/ui-language', ['ui_language' => 'nb']);
        api_route('/api/v1/users/me/ui-theme', ['ui_theme' => 'darkly']);

        $language = router_for(http_request('POST', '/lang', browser_headers(),
            ['csrf_token' => M3_CSRF, 'language' => 'nb', 'next' => '/']))->dispatch();
        $theme = router_for(http_request('POST', '/theme', browser_headers(),
            ['csrf_token' => M3_CSRF, 'theme' => 'darkly', 'next' => '/']))->dispatch();

        // Both used to answer 404: the route table required an ?action= the forms never send.
        assert_same(302, $language->status());
        assert_same(302, $theme->status());
        assert_same(1, api_calls_to('/api/v1/users/me/ui-language'));
        assert_same(1, api_calls_to('/api/v1/users/me/ui-theme'));
    });
});
