<?php
// The project home (§6.1, REQ-UI-017): one detail read serves the summary and the
// metadata block, the data region discloses exactly what the page shows, and a refused
// read becomes the §3.4 refusal rather than an empty panel.

declare(strict_types=1);

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

/** Queues the detail read and the sidebar's list read, most specific fragment first. */
function queue_project_home(array $detail = []): void
{
    queue_shell();
    api_route('/api/v1/projects/3', $detail === [] ? project_home_detail() : $detail);
    api_route('/api/v1/projects', [
        ['id' => 3, 'project_name' => '8DISC', 'organization' => 'NAT EU',
            'record_count' => 42, 'instrument_count' => 2, 'field_count' => 30],
    ]);
}

describe('project home (§6.1, REQ-UI-017)', function (): void {
    it('shows the summary counts from the one detail read', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('8DISC', $response->body());
        // 42 records; two instruments; 24 + 6 fields summed from the structure.
        assert_contains('>42<', $response->body());
        assert_contains('>2<', $response->body());
        assert_contains('>30<', $response->body());
    });

    it('reads the project once — the sidebar list is the only other call (§7 rule 13)', function (): void {
        sign_in();
        queue_project_home();

        router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_same(1, api_calls_to('/api/v1/projects/3'), 'the detail read happens once per render');
    });

    it('renders the metadata block and drops the fields that hold nothing', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_contains('REK-2026/123', $response->body());
        assert_contains('Ansgar Espeland', $response->body());
        // dm_name and the empty dates are null in the response: no row, no empty label.
        assert_not_contains('Data manager', $response->body());
    });

    it('names the project in the brand bar while the user is inside it (§2.4)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        assert_contains('href="/projects/3"', $response->body());
    });

    it('offers no card for a page this build does not have (§3.1)', function (): void {
        sign_in();
        queue_project_home();

        $response = router_for(http_request('GET', '/projects/3', browser_headers()))->dispatch();

        // Setup, Design, Record status and the rest arrive with M3–M5; until then no link
        // to them exists anywhere on the page.
        assert_not_contains('/setup', $response->body());
        assert_not_contains('/record-status', $response->body());
        assert_not_contains('/design', $response->body());
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
