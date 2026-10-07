<?php
// The project page (§6.1, REQ-UI-017): entering a project resolves the first available
// section, Overview shows the summary and metadata from one detail read, and the left panel
// lists exactly the entries this member may use and this build serves. A refused read
// becomes the §3.4 refusal rather than an empty panel.

declare(strict_types=1);

use Clara\Navigation;
use Clara\Permissions;
use Clara\Session;

/**
 * The §4.5 detail object for project 3, with the shape the API returns: metadata,
 * structure the counts come from, and the permissions block that gates rendering only.
 */
function project_home_detail(array $overrides = []): array
{
    return array_merge([
        'id' => 3,
        'project_name' => '8DISC',
        'organization' => 'NAT EU',
        'pi_name' => 'Ansgar Espeland',
        'pi_email' => 'pi@example.org',
        'dm_name' => null,
        'dm_email' => null,
        'rek_number' => 'REK-2026/123',
        'rek_start_date' => '2026-01-01',
        'rek_end_date' => null,
        'start_date' => null,
        'end_date' => null,
        'participant_names' => '8DISC[0-9][0-9][0-9]',
        'creation_time' => '2026-09-01 08:00:00',
        'record_count' => 42,
        'arms' => [['arm_num' => 1, 'name' => null, 'events' => []]],
        'instruments' => [
            ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 24],
            ['id' => 8, 'name' => 'followup', 'position' => 2, 'field_count' => 6],
        ],
        'permissions' => [
            'project_admin' => false,
            'arms' => [['arm_num' => 1, 'data_access_level' => 'view_edit', 'export_level' => 'export_de_identified']],
        ],
    ], $overrides);
}

/**
 * Queues the two reads an Overview render makes: the mode for the badge (§6.6) and the
 * detail. The mode goes first — the fake transport answers the first fragment it finds in
 * the URL, and `/api/v1/projects/3` is a prefix of `/api/v1/projects/3/mode`.
 */
function queue_project_home(array $detail = [], string $mode = 'development'): void
{
    queue_shell();
    api_route('/api/v1/projects/3/mode', ['mode' => $mode, 'staging_open' => false]);
    api_route('/api/v1/projects/3', $detail === [] ? project_home_detail() : $detail);
}

describe('project page (§6.1, REQ-UI-017)', function (): void {
    it('opens the first available section instead of a page of its own (REQ-UI-017)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        // A data-entry member (view_edit, no project_admin) lands on the Record Status
        // Dashboard — the first entry after Overview they may use (REQ-UI-017, M5).
        assert_same(303, $response->status());
        assert_same('/projects/3/record-status', $response->headers()['Location']);
    });

    it('shows the summary counts from the one detail read', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('8DISC', $response->body());
        // 42 records; two instruments; 24 + 6 fields summed from the structure.
        assert_contains('>42<', $response->body());
        assert_contains('>2<', $response->body());
        assert_contains('>30<', $response->body());
    });

    it('reads the project once and never the project list (§7 rule 13)', function (): void {
        sign_in();
        queue_project_home();

        router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        // The detail read, plus the mode read the badge needs (§6.6) — and no list call.
        assert_same(1, api_calls_to('/api/v1/projects/3/mode'), 'one mode read for the badge');
        assert_same(2, api_calls_to('/api/v1/projects'), 'detail + mode are the only projects calls: no panel list any more');
    });

    it('shows the mode badge beside the project name and in the header breadcrumb (§6.6)', function (): void {
        sign_in();
        queue_project_home([], 'production');

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_same(2, substr_count($response->body(), 'data-mode="production"'), 'Overview heading + header breadcrumb');
        assert_contains('>Production<', $response->body());
    });

    it('omits the badge, not the page, when the mode read is refused (§6.6)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects/3/mode', ['error' => 'forbidden', 'message' => '', 'status' => 403], 403);
        api_route('/api/v1/projects/3', project_home_detail());

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('8DISC', $response->body());
        assert_not_contains('clara-mode-badge', $response->body());
    });

    it('renders no badge for a value outside the three modes of GD-20', function (): void {
        sign_in();
        queue_project_home([], 'archived');

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_not_contains('data-mode=', $response->body());
        assert_not_contains('archived', $response->body());
    });

    it('renders the metadata block and drops the fields that hold nothing', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_contains('REK-2026/123', $response->body());
        assert_contains('Ansgar Espeland', $response->body());
        // dm_name and the empty dates are null in the response: no row, no empty label.
        assert_not_contains('Data manager', $response->body());
    });

    it('names the project in the header breadcrumb, linking back to Overview (§2.4)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_contains('href="/projects/3/overview"', $response->body());
    });

    it('carries a left panel of the project\'s own functions (§2.4 B, REQ-UI-046)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        assert_contains('id="clara-nav"', $response->body());
        // Overview is the entry that exists, and it is the one on screen.
        assert_contains('aria-current="page"', $response->body());
    });

    it('offers exactly the sections this member may use (§3.1)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();

        // A view_edit member with an export level: Record Status (M5) and Export (M6) are
        // theirs; Setup and Design are project_admin-only, Members and Roles is_admin-only,
        // so no link to any of them exists on the page (REQ-UI-003).
        assert_contains('/projects/3/record-status', $response->body());
        assert_contains('/projects/3/export', $response->body());
        assert_not_contains('/setup', $response->body());
        assert_not_contains('/design', $response->body());
        assert_not_contains('/projects/3/members', $response->body());

        // Without an export level anywhere the Export entry is absent, not disabled (§6.4).
        resetApi();
        $detail = project_home_detail();
        $detail['permissions']['arms'][0]['export_level'] = 'export_none';
        queue_project_home($detail);
        $response = router_for(http_request('GET', '/projects/3/overview', browser_headers()))->dispatch();
        assert_not_contains('/export', $response->body());
    });

    it('serves the same summary as JSON, and no more (REQ-UI-044)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', json_headers()))->dispatch();

        assert_same(200, $response->status());
        $payload = json_decode($response->body(), true);
        assert_same(42, $payload['summary']['records']);
        assert_same(2, $payload['summary']['instruments']);
        assert_same(30, $payload['summary']['fields']);
        assert_same('NAT EU', $payload['metadata']['organization']);
    });

    it('keeps the permissions block out of the data region — it renders, it is not disclosed', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', json_headers()))->dispatch();

        assert_not_contains('data_access_level', $response->body());
        assert_not_contains('export_level', $response->body());
        assert_not_contains('project_admin', $response->body());
    });

    it('refuses an anonymous data request with the envelope, not HTML', function (): void {
        $response = router_for(http_request('GET', '/projects/3', json_headers()))->dispatch();

        assert_same(403, $response->status());
        assert_not_contains('<!doctype html>', $response->body());
    });

    it('renders the refusal page when the API refuses the read (§3.4)', function (): void {
        // A member with no data access on any arm fails the read_only floor of §4.5.
        sign_in();
        queue_shell();
        api_route('/api/v1/projects/3', ['error' => 'forbidden', 'message' => '', 'status' => 403], 403);

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_same(200, $response->status(), 'a page-level refusal renders a page');
        assert_contains('do not have permission', $response->body());
    });

    it('treats an id that names no project as not found without asking the API', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/projects/not-a-number', browser_headers()))->dispatch();

        assert_same(0, api_calls_to('/api/v1/projects/'), 'a malformed id never reaches the API as a path segment');
        assert_contains('does not exist', $response->body());
    });

    it('leaves the pre-authentication challenge out — a pending second factor is no session (§2.7)', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64), 'tfa_pending' => [
            'email' => 'a@example.org', 'source' => 'local', 'provider' => '', 'source_name' => '',
            'first_factor' => 'ff1', 'user_id' => 42, 'method' => 'totp', 'verified_at' => time(),
        ]];

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_same(302, $response->status());
        assert_true(!Session::isAuthenticated());
    });
});

describe('project page panel rules (§6.1, REQ-UI-003/017)', function (): void {
    /** The allowed flags of the panel's candidates, keyed by section. */
    function project_panel_allows(array $detail): array
    {
        $allowed = [];
        foreach (Navigation::projectSectionDefinitions(3, Permissions::fromProjectDetail($detail)) as $section) {
            $allowed[$section['key']] = $section['allowed'];
        }

        return $allowed;
    }

    it('gives Setup and Design to a project administrator only', function (): void {
        sign_in();
        $admin = project_panel_allows(project_home_detail(['permissions' => [
            'project_admin' => true,
            'arms' => [['arm_num' => 1, 'data_access_level' => 'no_access', 'export_level' => 'export_none']],
        ]]));
        assert_true($admin['setup'], 'project_admin sees Setup');
        assert_true($admin['design'], 'project_admin sees Design');

        $member = project_panel_allows(project_home_detail());
        assert_true(!$member['setup'], 'a member without project_admin sees no Setup');
        assert_true(!$member['design'], '…nor Design');
    });

    it('gives the record status dashboard at data access read_only or better', function (): void {
        assert_true(project_panel_allows(project_home_detail())['record_status']);

        $none = project_panel_allows(project_home_detail(['permissions' => [
            'project_admin' => false,
            'arms' => [['arm_num' => 1, 'data_access_level' => 'no_access', 'export_level' => 'export_none']],
        ]]));
        assert_true(!$none['record_status'], 'no data access on any arm means no dashboard entry');
    });

    it('gives Export only with a non-export_none level on some arm', function (): void {
        assert_true(project_panel_allows(project_home_detail())['export']);

        $none = project_panel_allows(project_home_detail(['permissions' => [
            'project_admin' => true,
            'arms' => [['arm_num' => 1, 'data_access_level' => 'view_edit', 'export_level' => 'export_none']],
        ]]));
        assert_true(!$none['export']);
    });

    it('gives Members and Roles to an installation administrator only', function (): void {
        sign_in(['is_admin' => 1]);
        $admin = project_panel_allows(project_home_detail());
        assert_true($admin['members']);
        assert_true($admin['roles']);

        sign_in();
        $member = project_panel_allows(project_home_detail());
        assert_true(!$member['members'], 'a project member sees no administration entry');
        assert_true(!$member['roles']);
    });

    it('lists every section in canonical order once all are built (§6.1, REQ-UI-017)', function (): void {
        sign_in(['is_admin' => 1]);
        $detail = project_home_detail();
        $detail['permissions']['project_admin'] = true;
        $permissions = Permissions::fromProjectDetail($detail);
        $paths = array_column(Navigation::projectSections(3, $permissions), 'path');

        // Setup and Design since M4, the Record Status Dashboard since M5, Export since M6;
        // Members, Roles (is_admin) and Groups (data access) since M3.
        assert_same(['/projects/3/overview', '/projects/3/setup', '/projects/3/design',
            '/projects/3/record-status', '/projects/3/export', '/projects/3/members', '/projects/3/roles',
            '/projects/3/groups'], $paths);
        // The `built` switch stays in place for sections a later build adds; none is off now.
        assert_same([], array_column(array_filter(Navigation::projectSectionDefinitions(3, $permissions),
            static fn (array $s): bool => !$s['built']), 'key'));
    });

    it('opens the first section after Overview, and Overview only as the fallback (REQ-UI-017)', function (): void {
        sign_in();
        // A data-entry member lands on the Record Status Dashboard (M5)…
        assert_same('/projects/3/record-status',
            Navigation::defaultProjectSection(3, Permissions::fromProjectDetail(project_home_detail())));
        // …a project_admin on Setup…
        $admin = project_home_detail();
        $admin['permissions']['project_admin'] = true;
        assert_same('/projects/3/setup', Navigation::defaultProjectSection(3, Permissions::fromProjectDetail($admin)));
        // …and a member with neither on Overview, the fallback.
        assert_same('/projects/3/overview', Navigation::defaultProjectSection(3, Permissions::none()));

        // …and the rule reads "first entry after Overview", so it needs no change when the
        // sections land: Overview is never the answer because it comes first.
        $definitions = Navigation::projectSectionDefinitions(3, Permissions::fromProjectDetail(project_home_detail()));
        assert_same('overview', $definitions[0]['key']);
        assert_same('setup', $definitions[1]['key']);
        assert_same('record_status', $definitions[3]['key']);
    });
});
