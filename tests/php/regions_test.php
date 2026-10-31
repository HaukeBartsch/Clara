<?php
// The preferred region on the web side (master spec "Organization by hospital regions"): the
// account page's selector (§2.8, REQ-UI-075), the Control Panel project list grouped by region
// (§5.2, REQ-UI-077), the Regions section with its new-value count (§5.13, REQ-UI-078), the
// users list's region column (§5.1, REQ-UI-079) and the Authentication Test's region preview
// (§5.12). The overview's grouping runs in the browser (dashboard.js) and is covered end to end.

declare(strict_types=1);

use Clara\I18n;
use Clara\Logger;
use Clara\Regions;
use Clara\Session;

const RG_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/** A POST to one ?action= of a route, with the session's CSRF token. */
function rg_post(string $path, array $fields, string $action, ?\Clara\Config $config = null): \Clara\Response
{
    queue_shell(); // the flash line is translated
    return router_for(http_request('POST', $path, browser_headers(), ['csrf_token' => RG_CSRF] + $fields,
        ['action' => $action, 'section' => 'regions']), $config)->dispatch();
}

/** The method and path of every API call whose URL contains the fragment. */
function rg_calls(string $fragment): array
{
    return array_values(array_map(
        static fn (array $c): string => $c['method'] . ' ' . parse_url($c['url'], PHP_URL_PATH),
        array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], $fragment))
    ));
}

/** A configuration with one OAuth2 provider that reads its roles from `groups`. */
function rg_roles_config(): \Clara\Config
{
    return test_config([
        'OAUTH2_1_ISSUER' => 'https://idp.example.org/idp',
        'OAUTH2_1_CLIENT_ID' => 'clara',
        'OAUTH2_1_CLIENT_SECRET' => 'secret',
        'OAUTH2_1_NAMES' => 'Hospital 3',
        'OAUTH2_1_ROLES_ATTR' => 'groups',
    ]);
}

/** One §4.30 mapping row as the API returns it. */
function rg_row(int $id, string $value, ?string $region, bool $reviewed): array
{
    return ['id' => $id, 'issuer' => 'https://idp.example.org/idp', 'role_value' => $value, 'region' => $region,
        'reviewed' => $reviewed, 'first_seen_at' => '2026-10-30 08:00:00', 'last_seen_at' => '2026-10-31 07:00:00',
        'seen_count' => 4];
}

/** A project of the §4.5 list with the region flag set or not. */
function rg_project(int $id, string $name, string $organization, bool $preferred = false): array
{
    return ['id' => $id, 'project_name' => $name, 'organization' => $organization, 'record_count' => 1,
        'instrument_count' => 1, 'field_count' => 1, 'last_accessed_at' => null, 'in_preferred_region' => $preferred];
}

describe('region grouping (REQ-UI-076/077)', function (): void {
    it('puts the flagged region first, the others in REQ-DB-006 order, unknown codes last', function (): void {
        $groups = Regions::group([
            rg_project(1, 'A', 'SUN'), rg_project(2, 'B', 'VEST'), rg_project(3, 'C', 'Ahus', true),
            rg_project(4, 'D', 'OUS'), rg_project(5, 'E', 'HBE'), rg_project(6, 'F', 'Ahus', true),
        ]);

        assert_same(['Ahus', 'OUS', 'SUN', 'HBE', 'VEST'], array_column($groups, 'code'));
        assert_same([true, false, false, false, false], array_column($groups, 'preferred'));
        assert_same(['C', 'F'], array_column($groups[0]['projects'], 'project_name'), 'the order inside a group is kept');
    });

    it('follows REQ-DB-006 order without a preferred region', function (): void {
        $groups = Regions::group([rg_project(1, 'A', 'OTHER'), rg_project(2, 'B', 'SØ'), rg_project(3, 'C', 'OUS')]);

        assert_same(['OUS', 'SØ', 'OTHER'], array_column($groups, 'code'));
    });

    it('labels a code with the project form text and a withdrawn code as itself', function (): void {
        resetApi();
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
        $i18n = new I18n(new \Clara\ApiClient(test_config(), new Logger('error', true),
            http_request('GET', '/'), new FakeTransport()), new Logger('error', true), true);

        assert_contains('Akershus University Hospital', Regions::label($i18n, 'Ahus'));
        assert_same('Other', Regions::label($i18n, 'OTHER'));
        assert_same('VEST', Regions::label($i18n, 'VEST'));
    });
});

describe('account page — preferred region (§2.8, REQ-UI-075)', function (): void {
    it('reads the region at render and says what Automatic currently means', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/region', ['region' => 'Ahus', 'preferred_region' => null,
            'automatic_region' => 'Ahus', 'automatic_region_source' => 'assignment']);

        $body = router_for(http_request('GET', '/account', browser_headers()))->dispatch()->body();

        assert_contains('action="/account?action=set_region"', $body);
        assert_contains('Automatic — Akershus University Hospital', $body);
        assert_contains('(from a project assignment)', $body);
        assert_contains('<option value="" selected>', $body);
        assert_contains('only decides which projects are listed first', $body);
    });

    it('selects the own choice and keeps a withdrawn code selectable', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/region', ['region' => 'VEST', 'preferred_region' => 'VEST',
            'automatic_region' => null, 'automatic_region_source' => null]);

        $body = router_for(http_request('GET', '/account', browser_headers()))->dispatch()->body();

        assert_contains('Automatic — none', $body);
        assert_contains('<option value="VEST" selected>VEST</option>', $body);
    });

    it('keeps the page when the region cannot be read', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/region', ['error' => 'internal', 'message' => 'internal error'], 500);

        $response = router_for(http_request('GET', '/account', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('could not be loaded', $response->body());
        assert_not_contains('set_region', $response->body());
    });

    it('stores a code, and sends null for Automatic', function (): void {
        sign_in();
        api_route('/api/v1/users/me/region', ['region' => 'SiV']);

        $response = rg_post('/account', ['region' => 'SiV'], 'set_region');

        assert_same('/account', $response->headers()['Location']);
        assert_same(['region' => 'SiV'], api_request_body('/api/v1/users/me/region'));

        resetApi();
        sign_in();
        api_route('/api/v1/users/me/region', ['region' => null]);
        rg_post('/account', ['region' => ''], 'set_region');
        assert_same(['region' => null], api_request_body('/api/v1/users/me/region'));
    });

    it('refuses a value outside the list before the API is called', function (): void {
        sign_in();

        $response = rg_post('/account', ['region' => 'Oslo'], 'set_region');

        assert_same('/account', $response->headers()['Location']);
        assert_same(0, api_calls_to('/api/v1/users/me/region'));
        assert_contains('region', implode(' ', array_column(Session::takeFlash(), 'text')));
    });
});

describe('Control Panel — Projects grouped by region (§5.2, REQ-UI-077)', function (): void {
    it('lists the administrator\'s region first, in name order, under divider rows', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', [
            rg_project(1, 'zeta', 'OUS'), rg_project(2, 'Alpha', 'SUN'), rg_project(3, 'beta', 'SUN'),
            rg_project(4, 'Gamma', 'Ahus', true),
        ]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'projects']))->dispatch()->body();

        assert_contains('class="table-group-divider"', $body);
        $positions = array_map(static fn (string $text): int|false => strpos($body, $text),
            ['Your region: Ahus · 1', 'Gamma', 'OUS · 1', 'zeta', 'SUN · 2', 'Alpha', 'beta']);
        assert_true(!in_array(false, $positions, true), 'every heading and project is listed');
        $sorted = $positions;
        sort($sorted);
        assert_same($sorted, $positions, 'groups in order, names in order inside');
    });

    it('draws no divider when every project shares one region', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/projects', [rg_project(1, 'A', 'OUS'), rg_project(2, 'B', 'OUS', false)]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'projects']))->dispatch()->body();

        assert_not_contains('table-group-divider', $body);
        assert_not_contains('OUS · 2', $body);
    });
});

describe('Control Panel — Regions (§5.13, REQ-UI-078)', function (): void {
    it('says no provider reads roles, hides the add form and spends no read on a count', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'regions']))->dispatch()->body();

        assert_contains('OAUTH2_N_ROLES_ATTR', $body);
        assert_not_contains('action=add_role', $body);
        assert_contains('data-region="regions"', $body);
        assert_same(0, api_calls_to('/api/v1/provider-roles'));
    });

    it('counts new values beside the panel entry on every Control Panel page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', ['rate_limit_enabled' => true, 'rate_limit_rpm' => 600, 'rate_limit_block_minutes' => 10]);
        api_route('/api/v1/provider-roles', [rg_row(1, 'HSO-OUS', null, false), rg_row(2, 'HSO-SiV', null, false),
            rg_row(3, 'HSO-Ahus', 'Ahus', true)]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'settings']), rg_roles_config())
            ->dispatch()->body();

        assert_contains('href="/admin?section=regions"', $body);
        assert_contains('<span class="badge rounded-pill text-bg-warning ms-1">2</span>', $body);
        assert_same(1, api_calls_to('/api/v1/provider-roles'), 'one read per page');
    });

    it('offers the add form for the providers that read roles', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/provider-roles', [rg_row(3, 'HSO-Ahus', 'Ahus', true)]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'regions']), rg_roles_config())
            ->dispatch()->body();

        assert_contains('action=add_role', $body);
        assert_contains('>Hospital 3</option>', $body);
        assert_contains('next sign-in', $body);
        assert_not_contains('badge rounded-pill', $body, 'no count when nothing is new');
    });

    it('serves the mapping as its data region, each row with its provider named', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/provider-roles', [rg_row(1, '<b>HSO</b>', null, false)]);

        $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'regions']), rg_roles_config())
            ->dispatch();
        $data = json_decode($response->body(), true);

        assert_same(200, $response->status());
        assert_same('Hospital 3', $data['rows'][0]['provider']);
        assert_same('<b>HSO</b>', $data['rows'][0]['role_value'], 'text, rendered with textContent');
        assert_same(false, $data['rows'][0]['reviewed']);
    });

    it('maps a value, with an empty region meaning "No region"', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/provider-roles/4', rg_row(4, 'HSO-OUS', 'OUS', true));

        $response = rg_post('/admin', ['id' => '4', 'region' => 'OUS'], 'map_role', rg_roles_config());

        assert_same('/admin?section=regions', $response->headers()['Location']);
        assert_same(['PUT /api/v1/provider-roles/4'], rg_calls('/api/v1/provider-roles'));
        assert_same(['region' => 'OUS'], api_request_body('/api/v1/provider-roles/4'));

        resetApi();
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/provider-roles/4', rg_row(4, 'HSO-OUS', null, true));
        rg_post('/admin', ['id' => '4', 'region' => ''], 'map_role', rg_roles_config());
        assert_same(['region' => null], api_request_body('/api/v1/provider-roles/4'));
    });

    it('refuses an unknown region before the API is called', function (): void {
        sign_in(['is_admin' => 1]);

        rg_post('/admin', ['id' => '4', 'region' => 'Oslo'], 'map_role', rg_roles_config());

        assert_same(0, api_calls_to('/api/v1/provider-roles'));
        assert_contains('region', implode(' ', array_column(Session::takeFlash(), 'text')));
    });

    it('adds a value, and names a duplicate', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/provider-roles', rg_row(9, 'HSO-SUN', 'SUN', true), 201);

        $response = rg_post('/admin', ['issuer' => 'https://idp.example.org/idp', 'role_value' => ' HSO-SUN ',
            'region' => 'SUN'], 'add_role', rg_roles_config());

        assert_same('/admin?section=regions', $response->headers()['Location']);
        assert_same(['issuer' => 'https://idp.example.org/idp', 'role_value' => 'HSO-SUN', 'region' => 'SUN'],
            api_request_body('/api/v1/provider-roles'));

        resetApi();
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/provider-roles', ['error' => 'conflict', 'message' => 'this role value is already listed'], 409);
        $body = rg_post('/admin', ['issuer' => 'https://idp.example.org/idp', 'role_value' => 'HSO-SUN',
            'region' => 'SUN'], 'add_role', rg_roles_config())->body();
        assert_contains('This role value is already listed.', $body);
        assert_contains('value="HSO-SUN"', $body, 'the form keeps what was typed');
    });

    it('refuses an issuer that reads no roles', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/provider-roles', []);

        $body = rg_post('/admin', ['issuer' => 'https://elsewhere.example.org', 'role_value' => 'x', 'region' => 'OUS'],
            'add_role', rg_roles_config())->body();

        assert_contains('issuer', $body);
        assert_same([], array_filter(rg_calls('/api/v1/provider-roles'), static fn (string $c): bool => str_starts_with($c, 'POST')));
    });

    it('removes a value', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/provider-roles/4', ['ok' => true]);

        $response = rg_post('/admin', ['id' => '4'], 'remove_role', rg_roles_config());

        assert_same('/admin?section=regions', $response->headers()['Location']);
        assert_same(['DELETE /api/v1/provider-roles/4'], rg_calls('/api/v1/provider-roles'));
    });
});

describe('Authentication Test — region preview (§5.12)', function (): void {
    it('shows the roles claim, what the mapping says about each value and the region a sign-in would set', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/provider-roles', []);
        Session::setAuthTestReport(['kind' => 'oauth', 'ok' => true, 'region_preview' => [
            'claim' => 'groups', 'claim_present' => true, 'region' => 'SiV',
            'values' => [
                ['value' => 'HSO-SiV', 'listed' => true, 'reviewed' => true, 'region' => 'SiV'],
                ['value' => '<i>new</i>', 'listed' => false, 'reviewed' => false, 'region' => null],
            ],
        ]]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'auth']), rg_roles_config())
            ->dispatch()->body();

        assert_contains('Roles claim &quot;groups&quot;', $body);
        assert_contains('not in the mapping', $body);
        assert_contains('&lt;i&gt;new&lt;/i&gt;', $body);
        assert_contains('A sign-in would set the region Sykehuset i Vestfold', $body);
    });
});

describe('Control Panel — Users region column (§5.1, REQ-UI-079)', function (): void {
    it('shows the effective region and where it came from', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        $base = ['display_name' => 'U', 'enabled' => true, 'is_admin' => false, 'auth_source' => 'oauth2',
            'last_login_at' => null, 'valid_until' => null, 'status' => 'active', 'tfa_method' => 'off'];
        api_route('/api/v1/users', [
            $base + ['id' => 1, 'email' => 'own@example.org', 'region' => 'SUN', 'preferred_region' => 'SUN',
                'automatic_region' => 'OUS', 'automatic_region_source' => 'provider'],
            $base + ['id' => 2, 'email' => 'placed@example.org', 'region' => 'Ahus', 'preferred_region' => null,
                'automatic_region' => 'Ahus', 'automatic_region_source' => 'assignment'],
            $base + ['id' => 3, 'email' => 'none@example.org', 'region' => null, 'preferred_region' => null,
                'automatic_region' => null, 'automatic_region_source' => null],
        ]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'users']))->dispatch()->body();

        assert_contains('own choice', $body);
        assert_contains('assignment', $body);
        assert_contains('>Region</th>', $body);
    });
});
