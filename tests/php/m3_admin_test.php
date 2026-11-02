<?php
// M3 — the administration surface (Plan/Web_Implementation.md §6): the Control Panel's Users,
// Projects, Audits and Translations sections (§5.1/§5.2/§5.6/§5.7, REQ-UI-047) and the project
// page's Members, Roles and Groups (§5.3/§5.4/§5.5). What is asserted here is what review
// would otherwise have to check by hand: each write carries exactly its endpoint's attributes
// (§12 rule 1), a token is shown once (§3.5), destructive actions confirm first, gates hold,
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
        api_users_page([m3_user(['display_name' => '<script>x</script>', 'status' => 'auto_disabled', 'enabled' => false])]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users']))->dispatch()->body();

        assert_contains('ada@example.org', $body);
        assert_contains('auto-disabled', $body);
        assert_contains('&lt;script&gt;x&lt;/script&gt;', $body);
        assert_not_contains('<script>x</script>', $body);
    });

    it('sends the checked filters and the page to the search (REQ-API-186)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([m3_user(['project_count' => 3, 'created_at' => '2026-10-01 08:00:00'])], 120, 50);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users',
            'q' => '  kari ', 'source' => 'ldap', 'projects' => 'none', 'sort' => 'created', 'page' => '2']))->dispatch()->body();

        assert_same([['q' => 'kari', 'source' => 'ldap', 'projects' => 'none', 'sort' => 'created', 'limit' => '50', 'offset' => '50']],
            api_queries('/api/v1/users/search'));
        assert_contains('Accounts 51–100 of 120', $body);
        assert_contains('Page 2 of 3', $body);
        assert_contains('href="/admin?section=users&amp;q=kari&amp;source=ldap&amp;projects=none&amp;sort=created"', $body, 'Previous: page 1 is the default');
        assert_contains('href="/admin?section=users&amp;q=kari&amp;source=ldap&amp;projects=none&amp;sort=created&amp;page=3"', $body);
        assert_contains('name="back" value="q=kari&amp;source=ldap&amp;projects=none&amp;sort=created&amp;page=2"', $body);
        assert_contains('2026-10-01 08:00:00', $body);
        assert_same(1, preg_match('/<td class="text-end">3<\/td>/', $body), 'the project count');
        assert_contains('value="kari"', $body, 'the search box keeps the query');
    });

    it('drops a filter it does not know rather than forwarding it (§2.1)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users',
            'source' => 'saml', 'projects' => 'all', 'sort' => 'name', 'page' => '-1', 'enabled' => '1']))->dispatch()->body();

        assert_same([['limit' => '50', 'offset' => '0']], api_queries('/api/v1/users/search'));
        assert_contains('No accounts.', $body);
        assert_not_contains('Page 1 of', $body, 'one page needs no pager');
    });

    it('says when nothing matches the filters', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users', 'q' => 'nobody']))->dispatch()->body();

        assert_contains('No account matches these filters.', $body);
        assert_contains('href="/admin?section=users"', $body, 'Clear');
    });

    it('shows the last page when asked for one past the end', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([m3_user()], 60, 50);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users', 'page' => '9']))->dispatch()->body();

        assert_same(['400', '50'], array_column(api_queries('/api/v1/users/search'), 'offset'));
        assert_contains('Page 2 of 2', $body);
    });

    it('returns a row action to the filtered page it came from — and only to allowed filters', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/users/9', m3_user(['enabled' => false]));

        $done = m3_post('/admin', ['id' => '9', 'email' => 'ada@example.org', 'enabled' => '0',
            'back' => 'q=kari&page=2&source=saml&section=projects'], 'user_enabled');
        assert_same('/admin?section=users&q=kari&page=2', $done->headers()['Location']);

        // A refusal before any call returns there too.
        $refused = m3_post('/admin', ['id' => '9', 'valid_days' => 'x', 'back' => 'projects=none'], 'user_validity');
        assert_same('/admin?section=users&projects=none', $refused->headers()['Location']);
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

    it('sends one attribute per row action (§12 rule 1)', function (): void {
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
        api_users_page([m3_user(['tfa_method' => 'totp'])]);

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

    it('imports an ODM file with all project metadata supplied by the administrator', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects/import/odm', [
            'project_id' => 43, 'project_name' => 'Imported study', 'records_added' => 2,
            'records_updated' => 0, 'records_failed' => 0, 'problems' => [],
        ], 201);
        $file = tempnam(sys_get_temp_dir(), 'clara-odm-');
        assert_true(is_string($file), 'temporary ODM upload is available');
        $xml = '<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3"/>';
        file_put_contents($file, $xml);
        $fields = [
            'project_name' => 'Imported study', 'organization' => 'OUS', 'pi_name' => 'PI',
            'pi_email' => 'pi@example.org', 'participant_names' => 'IMPORTED_[0-9][0-9][0-9]',
            'dm_name' => '', 'dm_email' => '', 'rek_number' => 'REK-43', 'rek_start_date' => '',
            'rek_end_date' => '', 'start_date' => '2026-01-01', 'end_date' => '',
        ];
        try {
            $request = new \Clara\Request('POST', '/admin', browser_headers(),
                ['section' => 'projects', 'action' => 'import_project'],
                ['csrf_token' => M3_CSRF] + $fields, [], '', [
                    'odm_file' => ['name' => 'study.xml', 'tmp_name' => $file, 'size' => strlen($xml), 'error' => UPLOAD_ERR_OK],
                ]);
            $response = router_for($request)->dispatch();
        } finally {
            unlink($file);
        }

        assert_same('/admin?section=projects&created=43', $response->headers()['Location']);
        $call = array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool =>
            str_contains($c['url'], '/api/v1/projects/import/odm')))[0];
        assert_same($xml, $call['body'], 'the original XML is forwarded as the request body');
        assert_true(in_array('Content-Type: application/xml', $call['headers'], true));
        parse_str((string) parse_url($call['url'], PHP_URL_QUERY), $query);
        foreach ($fields as $key => $value) {
            assert_same($value, $query[$key] ?? null, "import metadata query $key");
        }
        assert_contains('2 records added', $_SESSION['_flash'][0]['text']);
    });

    it('keeps project import inaccessible to non-administrators', function (): void {
        sign_in();

        $response = m3_post('/admin', [], 'import_project');

        assert_same(403, $response->status());
        assert_same(0, api_calls_to('/api/v1/projects/import/odm'));
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

    it('offers the Helse Sør-Øst institutions by code and does not display the name pattern', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'projects']))->dispatch()->body();

        assert_contains('<option value="OUS">Oslo University Hospital, Rikshospitalet, Ullevål, Radiumhospitalet, Aker</option>', $body);
        assert_contains('<option value="SUN">Sunnaas Sykehus, Nesodden (Specialized rehabilitation hospital)</option>', $body);
        assert_contains('<option value="NAT EU">NAT EU</option>', $body);
        assert_contains('<option value="OTHER">Other</option>', $body);
        assert_contains('action=import_project', $body);
        assert_contains('name="odm_file"', $body);
        assert_contains('Import project</button>', $body);
        assert_same(0, preg_match('/value="HBE"/', $body), 'the previous codes are gone');
        assert_same(1, preg_match('/<input type="hidden" id="project-participant_names" name="participant_names"/', $body));
        assert_same(0, preg_match('/<label[^>]*for="project-participant_names"/', $body), 'no label for the pattern');
    });

    it('keeps a project\'s retired institution code selectable on edit', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects/12', ['id' => 12, 'project_name' => 'EMIT', 'organization' => 'HBE',
            'pi_name' => 'PI', 'pi_email' => 'pi@example.org', 'participant_names' => 'E[0-9][0-9]']);
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'projects', 'edit' => '12']))->dispatch()->body();

        assert_contains('<option value="HBE" selected>HBE</option>', $body);
        assert_contains('name="participant_names" value="E[0-9][0-9]"', $body);
    });

    it('derives the pattern from the name when the form carries none (no JavaScript)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', ['id' => 13, 'project_name' => 'Heart study'], 201);

        m3_post('/admin', ['project_name' => ' Heart study ', 'organization' => 'OUS', 'pi_name' => 'PI',
            'pi_email' => 'pi@example.org', 'participant_names' => ''], 'create_project');

        assert_same('HEART_STUDY_[0-9][0-9][0-9]', api_request_body('/api/v1/projects')['participant_names']);
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

    it('names the record of a record-scoped event in the Record column, and nothing for others', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit', ['entries' => [
            ['id' => 2, 'created_at' => '2026-10-13 20:08:38', 'source' => 'api', 'project_id' => 1, 'user_id' => 4,
                'email' => 'ingrid.berg@example.org', 'event_type' => 'record_updated', 'target_record' => 'HEART004',
                'details' => ['action' => 'update', 'record_id' => 'HEART004']],
            ['id' => 1, 'created_at' => '2026-10-13 20:08:37', 'source' => 'ui', 'project_id' => 1, 'user_id' => 1,
                'email' => 'admin@example.org', 'event_type' => 'project_updated', 'target_record' => null,
                'details' => ['changes' => []]],
        ], 'next_cursor' => null]);
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits']))->dispatch()->body();

        assert_same(1, preg_match('#<code>record_updated</code></td>\s*<td class="small">HEART004</td>#', $body));
        assert_same(1, preg_match('#<code>project_updated</code></td>\s*<td class="small"></td>#', $body));
    });
    it('offers every event code of the API\'s catalog in the filter, and nothing else (§3)', function (): void {
        // The catalog is the const block that opens api/internal/audit/audit.go and ends before
        // the source values (REQ-AUD-017): those are a column, not event types.
        $go = (string) file_get_contents(__DIR__ . '/../../api/internal/audit/audit.go');
        $start = strpos($go, '// Event catalog');
        $end = strpos($go, '// Source values');
        assert_true($start !== false && $end !== false, 'audit.go still marks its event catalog');
        preg_match_all('/^\s+\w+\s*=\s*"([a-z0-9_]+)"/m', substr($go, $start, $end - $start), $m);
        $catalog = $m[1];
        sort($catalog);

        $filter = \Clara\Controllers\ControlPanelController::AUDIT_EVENT_TYPES;
        assert_true(count($catalog) > 50, 'the catalog parsed');
        assert_same($catalog, $filter, 'the filter lists the catalog, alphabetically');
    });

    it('filters by user through a search field, never a list of every account', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit', ['entries' => [], 'next_cursor' => null]);
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits']))->dispatch()->body();

        assert_same(1, preg_match('/<input[^>]*name="user"[^>]*list="audit-user-options"[^>]*value=""[^>]*data-clara-account-search="\/admin\?section=audits" data-clara-account-min="3"/', $body));
        assert_contains('<datalist id="audit-user-options"></datalist>', $body);
        assert_not_contains('<select class="form-select form-select-sm" id="audit-user"', $body);
        assert_same(0, api_calls_to('/api/v1/users'), 'no account list, and no search without a user filter');
    });

    it('sends the id of the account the address names, preferring the exact spelling', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([
            m3_user(['id' => 5, 'email' => 'Kari@example.org']),
            m3_user(['id' => 6, 'email' => 'kari@example.org']),
            m3_user(['id' => 7, 'email' => 'kari@example.org.uk']),
        ]);
        api_route('/api/v1/audit', ['entries' => [], 'next_cursor' => 'abc123']);
        api_route('/api/v1/projects', []);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits', 'user' => ' kari@example.org ']))->dispatch()->body();

        assert_same([['q' => 'kari@example.org', 'limit' => '200']], api_queries('/api/v1/users/search'));
        assert_same('6', api_queries('/api/v1/audit')[0]['user'] ?? null);
        assert_contains('value="kari@example.org"', $body, 'the field keeps the address');
        assert_contains('user=kari%40example.org&amp;cursor=abc123', $body, 'Next keeps the user filter');
    });

    it('takes the one account an address names ignoring case, and refuses two', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([m3_user(['id' => 5, 'email' => 'kari@example.org']), m3_user(['id' => 9, 'email' => 'ola@example.org'])]);
        api_route('/api/v1/audit', ['entries' => [], 'next_cursor' => null]);
        api_route('/api/v1/projects', []);

        router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits', 'user' => 'KARI@example.org']))->dispatch();
        assert_same('5', api_queries('/api/v1/audit')[0]['user'] ?? null);

        resetApi();
        queue_shell();
        api_users_page([m3_user(['id' => 5, 'email' => 'Kari@example.org']), m3_user(['id' => 6, 'email' => 'kari@example.org'])]);
        api_route('/api/v1/projects', []);
        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits', 'user' => 'KARI@example.org']))->dispatch()->body();
        assert_contains('No account has the address KARI@example.org.', $body);
        assert_same(0, api_calls_to('/api/v1/audit'));
    });

    it('shows no trail, exports nothing and prices nothing for an address no account has', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_users_page([m3_user(['id' => 7, 'email' => 'kari@example.org.uk'])]);
        api_route('/api/v1/projects', []);

        $page = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits', 'user' => 'kari@example.org']))->dispatch();
        assert_same(200, $page->status());
        assert_contains('No account has the address kari@example.org.', $page->body());

        $download = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'audits', 'user' => 'kari@example.org', 'download' => '1']))->dispatch();
        assert_contains('No account has the address kari@example.org.', $download->body(), 'the download shows the line instead');

        $estimate = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'audits', 'user' => 'kari@example.org', 'estimate' => '1']))->dispatch();
        assert_same(400, $estimate->status());
        assert_same(0, api_calls_to('/api/v1/audit'), 'neither the trail nor its export was asked for');
    });

    it('suggests accounts from three characters, with id, address and name only (the audits region)', function (): void {
        sign_in(['is_admin' => 1]);
        api_users_page([m3_user(['id' => 12, 'email' => 'kari@example.org', 'display_name' => 'Kari', 'enabled' => false,
            'is_admin' => true, 'tfa_method' => 'totp'])]);

        $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'audits', 'q' => 'kar']))->dispatch();

        assert_same(200, $response->status());
        assert_same([['id' => 12, 'email' => 'kari@example.org', 'display_name' => 'Kari']], json_decode($response->body(), true));
        assert_same([['q' => 'kar', 'limit' => '20']], api_queries('/api/v1/users/search'), 'disabled accounts are suggested too');

        $short = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'audits', 'q' => 'ka']))->dispatch();
        assert_same('[]', $short->body());
        assert_same(1, api_calls_to('/api/v1/users/search'), 'two characters are not searched');
    });

    it('answers the export estimate to the JSON request audits.js sends (REQ-AUD-032)', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/audit/export', ['rows' => 9000000, 'estimated_bytes' => 4831838208]);

        $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'audits', 'estimate' => '1', 'type' => 'views']))->dispatch();

        assert_same(200, $response->status(), 'a JSON request to this section is no longer refused 406');
        assert_same(4831838208, json_decode($response->body(), true)['estimated_bytes'] ?? null);
        assert_same('views', api_queries('/api/v1/audit/export')[0]['type'] ?? null);
    });
});

describe('Control Panel — Translations (§5.7, REQ-UI-030)', function (): void {
    it('renders the editor shell; the key table is this route\'s data region (REQ-UI-044)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/i18n/strings', [
            ['key' => 'login.heading', 'text' => 'Logg inn', 'missing' => false],
            ['key' => 'overlay.only.key', 'text' => 'Kun overlay', 'missing' => false],
        ]);
        api_route('/api/v1/i18n/languages', [['code' => 'en', 'display_name' => 'English'], ['code' => 'nb', 'display_name' => 'Norsk bokmål']]);

        $page = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'translations', 'language' => 'nb']))->dispatch();
        // The Tabulator table binds rows client-side: the page carries the container, the
        // section module and the library stylesheet — not a server-rendered input per key.
        assert_contains('data-region="translations"', $page->body());
        assert_contains('/assets/js/translations.js', $page->body());
        assert_contains('/assets/vendor/tabulator/', $page->body());

        // The data region is the universe the rendered table would show: every catalog key
        // with its current text and missing flag, plus any key only the overlay carries.
        $json = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'translations', 'language' => 'nb']))->dispatch();
        assert_same(200, $json->status());
        assert_contains('application/json', $json->headers()['Content-Type']);
        $payload = json_decode($json->body(), true);
        assert_same('nb', $payload['language']);
        $byKey = [];
        foreach ($payload['rows'] as $row) {
            $byKey[$row['key']] = $row;
        }
        assert_same('Logg inn', $byKey['login.heading']['text']);
        assert_same(false, $byKey['login.heading']['missing']);
        // A code key without a translation is listed as missing (§5.7).
        assert_same(true, $byKey['nav.sign_out']['missing']);
        assert_same('', $byKey['nav.sign_out']['text']);
        // A key only the overlay carries still shows up, so it can be cleared.
        assert_same('Kun overlay', $byKey['overlay.only.key']['text']);
        assert_same(false, $byKey['overlay.only.key']['missing']);
    });

    it('has no data region for the other sections — they answer 406 (REQ-UI-044)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'users']))->dispatch();

        assert_same(406, $response->status());
    });

    it('saves only changed entries through the unchanged PRG mutation (§3.3)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/i18n/strings', [['key' => 'login.heading', 'text' => 'Logg inn', 'missing' => false]]);

        m3_post('/admin', ['language' => 'nb', 'text' => [
            'login.heading' => 'Logg inn', // unchanged → not sent
            'nav.sign_out' => 'Logg ut',   // new → sent
            'nav.overview' => '',          // still missing → not sent
        ]], 'save_translations');

        assert_same(['language' => 'nb', 'entries' => [['key' => 'nav.sign_out', 'text' => 'Logg ut']]], m3_last_put('/api/v1/i18n/strings'));
    });
});

/** The body of the last PUT to an endpoint. */
/** The reads of a Members render (§5.3) besides the project shell. */
function m3_queue_members(): void
{
    api_route('/api/v1/projects/3/users', []);
    api_route('/api/v1/projects/3/roles', []);
    api_route('/api/v1/projects/3', m3_detail());
}

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

    it('adds with the role name only, and shows the new token exactly once — masked', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'ada@example.org', 'token' => 'tok-secret-123'], 201);

        $post = m3_post('/projects/3/members', ['user_id' => '9', 'role' => 'data-entry'], 'add_member');
        assert_same('/projects/3/members', $post->headers()['Location']);
        assert_same(['role' => 'data-entry'], m3_last_put('/api/v1/projects/3/users/9'));
        // The add form names the account by id only: the line names it from the API's answer.
        assert_contains('ada@example.org added.', implode(' ', array_column(Session::takeFlash(), 'text')));

        m3_queue_members();
        $first = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        $second = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();

        // Masked (REQ-UI-062): the reveal is there, the value is not — not even hidden in the input.
        assert_contains('Project token for ada@example.org', $first);
        assert_same(1, preg_match('/<input[^>]*type="password"[^>]*id="clara-new-token"[^>]*value=""/', $first));
        assert_contains('action=show_token', $first);
        assert_not_contains('tok-secret-123', $first, 'a masked token is not in the page');
        assert_not_contains('data-clara-copy="#clara-new-token"', $first, 'no copy before Show');
        assert_not_contains('clara-new-token', $second, 'the reveal is shown once');
        assert_same(0, api_calls_to('/token-viewed'));
    });

    it('records every Show as token_viewed before the value reaches the page (REQ-AUD-033)', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9/token-viewed', ['ok' => true]);
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'ada@example.org', 'token' => 'tok-secret-123']);
        m3_queue_members();
        m3_post('/projects/3/members', ['user_id' => '9'], 'rotate_token');
        Session::takeFlash();
        router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch();

        $show = m3_post('/projects/3/members', [], 'show_token');
        assert_same('/projects/3/members', $show->headers()['Location']);
        assert_same(['POST /api/v1/projects/3/users/9/token-viewed'], m3_calls('/token-viewed'));
        assert_same('{}', api_request_raw('/token-viewed'), 'the call carries no value');
        $shown = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        assert_contains('value="tok-secret-123"', $shown);
        assert_contains('data-clara-copy="#clara-new-token"', $shown);
        assert_contains('action=hide_token', $shown);

        // Hide masks it again and records nothing; a second Show is a second entry.
        m3_post('/projects/3/members', [], 'hide_token');
        $hidden = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        assert_not_contains('tok-secret-123', $hidden);
        assert_contains('action=show_token', $hidden);
        assert_same(1, api_calls_to('/token-viewed'));
        m3_post('/projects/3/members', [], 'show_token');
        assert_same(2, api_calls_to('/token-viewed'));
        assert_contains('value="tok-secret-123"',
            router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body());

        // Leaving the page ends the reveal: a reload drops it, and Show then has nothing to release.
        router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch();
        m3_post('/projects/3/members', [], 'show_token');
        assert_same(2, api_calls_to('/token-viewed'));
        assert_contains('no longer on this page', implode(' ', array_column(Session::takeFlash(), 'text')));
        assert_not_contains('tok-secret-123',
            router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body());
    });

    it('keeps the token masked when the view cannot be recorded', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9/token-viewed', ['error' => 'internal', 'message' => 'x'], 500);
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'ada@example.org', 'token' => 'tok-secret-123'], 201);
        m3_queue_members();
        m3_post('/projects/3/members', ['user_id' => '9'], 'add_member');
        Session::takeFlash();
        router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch();

        m3_post('/projects/3/members', [], 'show_token');
        assert_same(1, api_calls_to('/token-viewed'));
        $body = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        assert_not_contains('tok-secret-123', $body, 'no record, no value');
        assert_contains('action=show_token', $body, 'the reveal stays, masked, for another try');
    });

    it('names the account by its address and reads no account list (§5.3)', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        m3_queue_members();

        $body = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();

        assert_same(1, preg_match('/<input[^>]*name="account"[^>]*list="member-candidates"[^>]*data-clara-account-search="\/projects\/3\/members"/', $body));
        assert_contains('<datalist id="member-candidates"></datalist>', $body);
        assert_not_contains('name="user_id" required', $body, 'no account dropdown');
        assert_same(0, api_calls_to('/api/v1/users'), 'the page never loads every account');
    });

    it('adds the account whose address is exactly the typed one, ignoring case', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_users_page([m3_user(['id' => 12, 'email' => 'kari.n@example.org']), m3_user(['id' => 9, 'email' => 'Kari@Example.org'])]);
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'Kari@Example.org', 'token' => 't1'], 201);

        $post = m3_post('/projects/3/members', ['account' => ' kari@example.org ', 'role' => 'data-entry'], 'add_member');

        assert_same('/projects/3/members', $post->headers()['Location']);
        assert_same([['q' => 'kari@example.org', 'enabled' => '1', 'not_in_project' => '3', 'limit' => '200']],
            api_queries('/api/v1/users/search'));
        assert_same(['role' => 'data-entry'], m3_last_put('/api/v1/projects/3/users/9'));
        assert_same(0, api_calls_to('/api/v1/projects/3/users/12'));
    });

    it('refuses an address no candidate has exactly — a near match is never taken', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_users_page([m3_user(['id' => 12, 'email' => 'kari.n@example.org'])]);

        $post = m3_post('/projects/3/members', ['account' => 'kari@example.org'], 'add_member');
        assert_same('/projects/3/members', $post->headers()['Location']);
        $flash = Session::takeFlash();
        assert_same('danger', $flash[0]['level'] ?? '');
        assert_contains('No enabled account outside this project has that address', $flash[0]['text'] ?? '');

        m3_post('/projects/3/members', ['account' => ''], 'add_member');
        assert_same(1, api_calls_to('/api/v1/users/search'), 'an empty field is not searched');
        assert_same([], array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool => $c['method'] === 'PUT')));
    });

    it('suggests enabled non-members with id, address and name only (the candidates region)', function (): void {
        sign_in(['is_admin' => 1]);
        api_users_page([m3_user(['id' => 12, 'email' => 'kari@example.org', 'display_name' => 'Kari', 'is_admin' => true,
            'tfa_method' => 'totp', 'project_count' => 4])]);

        $response = router_for(http_request('GET', '/projects/3/members', json_headers(), [], ['q' => 'ka']))->dispatch();

        assert_same(200, $response->status());
        assert_same([['id' => 12, 'email' => 'kari@example.org', 'display_name' => 'Kari']], json_decode($response->body(), true));
        assert_same([['q' => 'ka', 'enabled' => '1', 'not_in_project' => '3', 'limit' => '20']], api_queries('/api/v1/users/search'));

        $short = router_for(http_request('GET', '/projects/3/members', json_headers(), [], ['q' => 'k']))->dispatch();
        assert_same('[]', $short->body());
        assert_same(1, api_calls_to('/api/v1/users/search'), 'one character is not searched');
    });

    it('keeps the suggestions from a member who is not an administrator', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/projects/3/members', json_headers(), [], ['q' => 'ka']))->dispatch();

        assert_same(403, $response->status());
        assert_same(0, api_calls_to('/api/v1/users/search'));
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

    it('adopts an existing REDCap token without echoing it anywhere (REQ-AUTH-090)', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        $value = '0123456789ABCDEF0123456789ABCDEF'; // made up, in REDCap's form
        // The API answers an adoption with the member object and no token (§4.6).
        api_route('/api/v1/projects/3/users/9', ['user_id' => 9, 'email' => 'ada@example.org', 'token_present' => true]);
        m3_queue_members();

        $answer = m3_post('/projects/3/members', ['user_id' => '9', 'email' => 'ada@example.org', 'token' => "  $value\n"], 'adopt_token');
        assert_same('/projects/3/members', $answer->headers()['Location']);
        assert_same(['adopt_token' => $value], m3_last_put('/api/v1/projects/3/users/9'), 'sent once, paste whitespace dropped');
        $flash = implode(' ', array_column(Session::takeFlash(), 'text'));
        assert_contains('ada@example.org', $flash);
        assert_not_contains($value, $flash);

        $page = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();
        assert_not_contains($value, $page, 'nothing is shown back');
        assert_not_contains('clara-new-token', $page, 'no reveal: the administrator supplied the value');
    });

    it('shows a refused adoption as an error and never refills the input', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users/9', ['error' => 'conflict', 'message' => 'token already in use'], 409);
        m3_queue_members();
        m3_post('/projects/3/members', ['user_id' => '9', 'token' => 'FEDCBA9876543210FEDCBA9876543210'], 'adopt_token');
        $flash = Session::takeFlash();
        assert_same('danger', $flash[0]['level'] ?? '');
        assert_contains('token already in use', $flash[0]['text'] ?? '');
        assert_not_contains('FEDCBA9876543210FEDCBA9876543210', implode(' ', array_column($flash, 'text')));
    });

    it('confirms before rotating, adopting or removing (§3.5) and never prints a stored token', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/users', [['user_id' => 9, 'email' => 'ada@example.org', 'display_name' => 'Ada',
            'role' => null, 'token_present' => true, 'enabled' => true]]);
        api_route('/api/v1/projects/3/roles', []);
        api_route('/api/v1/projects/3', m3_detail());

        $body = router_for(http_request('GET', '/projects/3/members', browser_headers()))->dispatch()->body();

        assert_same(1, preg_match('/action=rotate_token"[^>]*data-clara-confirm=/', $body));
        assert_same(1, preg_match('/action=remove_member"[^>]*data-clara-confirm=/', $body));
        // Adoption: confirmed, and the value goes in masked, empty, unremembered (REQ-AUTH-090).
        assert_same(1, preg_match('/action=adopt_token"[^>]*data-clara-confirm=/', $body));
        assert_same(1, preg_match('/<input[^>]*type="password"[^>]*name="token"[^>]*pattern="\[0-9A-F\]\{32\}"[^>]*autocomplete="off"/', $body));
        assert_same(0, preg_match('/name="token"[^>]*value=/', $body), 'the adoption input carries no value');
        assert_contains('No role (full permissions)', $body);
        assert_not_contains('clara-new-token', $body);
    });
});

describe('project page — Roles (§5.4, REQ-UI-014)', function (): void {
    it('creates a role from arm defaults plus one grant per matrix row, then edits it (REQ-API-057/143)', function (): void {
        sign_in(['is_admin' => 1]);
        m3_queue_project();
        api_route('/api/v1/projects/3/roles', ['id' => 1, 'name' => 'data-entry'], 201);

        // The editor posts the arm default and, per matrix row, its pair key with the row's
        // levels and the two right checkboxes; an unknown level falls back to the lowest.
        $pair = '1|intake|baseline_arm_1';
        $form = [
            'name' => 'data-entry',
            'arm_data' => ['1' => 'read_only'], 'arm_export' => ['1' => 'bogus'],
            'pair' => [$pair],
            'data' => [$pair => 'view_edit'], 'export' => [$pair => 'export_de_identified'],
            'delete_values' => [$pair => '1'],
        ];
        m3_post('/projects/3/roles', $form, 'save_role');

        $want = ['name' => 'data-entry', 'project_admin' => false,
            'arms' => ['1' => ['data' => 'read_only', 'export' => 'export_none']],
            'grants' => [['event' => 'baseline_arm_1', 'instrument' => 'intake', 'data' => 'view_edit',
                'export' => 'export_de_identified', 'delete_values' => true, 'edit_surveys' => false]]];
        assert_same(['POST /api/v1/projects/3/roles'], m3_calls('/api/v1/projects/3/roles'));
        assert_same($want, api_request_body('/api/v1/projects/3/roles'));

        // The role on the form is replaced as a whole — same body, PUT to its id (REQ-API-143).
        resetApi();
        m3_queue_project();
        api_route('/api/v1/projects/3/roles/1', $want + ['id' => 1]);
        m3_post('/projects/3/roles', $form + ['role_id' => '1', 'project_admin' => '1'], 'save_role');

        assert_same(['PUT /api/v1/projects/3/roles/1'], m3_calls('/api/v1/projects/3/roles'));
        assert_same(array_replace($want, ['project_admin' => true]), api_request_body('/api/v1/projects/3/roles/1'));
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

describe('language and theme switches (§2.1, §2.8, §3.8, §9)', function (): void {
    it('reaches the language and theme handlers with the bare POST the account page forms send', function (): void {
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
