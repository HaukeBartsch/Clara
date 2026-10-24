<?php
// The Control Panel's Configuration Check section (§5.10, REQ-UI-061): its left-panel entry, the
// page that renders INSTALL.md as checks from the PHP probe and the API's facts
// (`GET /api/v1/system/check`, REQ-API-159), and ConfigCheck's rating of both — which value is
// a problem, which only matters in production, and that no secret value is ever shown.

declare(strict_types=1);

use Clara\ConfigCheck;

/** A production configuration as INSTALL.md §6 writes it. */
function check_production_config(array $overrides = []): Clara\Config
{
    return test_config(array_merge([
        'APP_ENV' => 'production',
        'WEB_PUBLIC_URL' => 'https://clara.example.org',
        'INTERNAL_SERVICE_TOKEN' => 'secret-token-value-not-for-display',
        'SESSION_COOKIE_SECURE' => '1',
    ], $overrides));
}

/** probe()'s shape for a host installed exactly as INSTALL.md says. */
function check_healthy_env(array $overrides = []): array
{
    return array_replace_recursive([
        'php_version' => '8.3.12',
        'php_version_id' => 80312,
        'extensions' => ['curl' => true, 'json' => true, 'mbstring' => true, 'openssl' => true, 'session' => true,
            'ldap' => true, 'pdo' => true, 'pdo_mysql' => true],
        'opcache' => true,
        'proc_open' => true,
        'php_cli' => '/usr/bin/php',
        'post_max_size' => 32 * 1048576,
        'upload_max_filesize' => 32 * 1048576,
        'memory_limit' => 256 * 1048576,
        'https' => true,
        'host' => 'clara.example.org',
        // INSTALL.md §11: nginx passes its own $remote_addr twice over — as REMOTE_ADDR
        // (fastcgi_params) and as the X-Real-IP PHP forwards to the API.
        'remote_addr' => '203.0.113.7',
        'real_ip_header' => '203.0.113.7',
        'env_file' => ['exists' => true, 'mode' => 0640, 'writable' => false, 'placeholders' => []],
        'session_dir' => ['mode' => 0700, 'in_public' => false, 'in_web_root' => false],
        'code_writable' => [],
    ], $overrides);
}

/** The API's facts for the same host (API_Endpoints_Design.md §4.24). */
function check_healthy_facts(array $overrides = []): array
{
    $year = (int) gmdate('Y');
    $partitions = ['p' . ($year - 1), 'p' . $year, 'p' . ($year + 1)];

    return array_replace_recursive([
        'app_env' => 'production',
        'web_public_url' => 'https://clara.example.org',
        'api_addr' => '127.0.0.1:8080',
        'api_listen_loopback' => true,
        'running_as_root' => false,
        'weak_secrets' => [],
        'bootstrap' => ['password_configured' => true, 'password_unchanged' => false],
        'smtp_configured' => true,
        'trusted_proxies_configured' => true,
        'require_2fa' => false,
        'app_timezone' => 'Europe/Oslo',
        'host' => ['clock_synchronized' => true, 'selinux' => 'enforcing'],
        'database' => [
            'connection' => 'mariadb',
            'schema_version' => 15,
            'version' => '11.4.13-MariaDB',
            'character_set_server' => 'utf8mb4',
            'collation_server' => 'utf8mb4_unicode_ci',
            'database_character_set' => 'utf8mb4',
            'database_collation' => 'utf8mb4_unicode_ci',
            'time_zone' => '+00:00',
            'system_time_zone' => 'UTC',
            'max_allowed_packet' => 64 * 1048576,
            'bind_address' => '127.0.0.1',
            'grants' => ['account' => 'clara@127.0.0.1', 'audit_writable' => [], 'tables_missing_write' => [], 'drop' => false],
            'audit_partitions' => ['audit_events' => $partitions, 'audit_record_views' => $partitions],
        ],
    ], $overrides);
}

/** id → status over every group. */
function check_statuses(array $groups): array
{
    $out = [];
    foreach ($groups as $group) {
        foreach ($group['checks'] as $check) {
            $out[$check['id']] = $check['status'];
        }
    }

    return $out;
}

function check_find(array $groups, string $id): array
{
    foreach ($groups as $group) {
        foreach ($group['checks'] as $check) {
            if ($check['id'] === $id) {
                return $check;
            }
        }
    }

    throw new RuntimeException("no check {$id}");
}

function check_evaluate(?Clara\Config $config = null, array $env = [], ?array $facts = null, string $apiError = ''): array
{
    return ConfigCheck::evaluate($config ?? check_production_config(), check_healthy_env($env),
        $facts ?? check_healthy_facts(), $apiError, (int) gmdate('Y'));
}

describe('Configuration Check section (§5.10, REQ-UI-061)', function (): void {
    it('sits between Settings and API documentation in the left panel', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', ['rate_limit_enabled' => false, 'rate_limit_rpm' => 600, 'rate_limit_block_minutes' => 10]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'settings']))->dispatch()->body();

        assert_contains('href="/admin?section=check"', $body);
        assert_contains('Configuration Check', $body);
        assert_true(strpos($body, 'href="/admin?section=settings"') < strpos($body, 'href="/admin?section=check"'));
        assert_true(strpos($body, 'href="/admin?section=check"') < strpos($body, 'href="/admin?section=api"'));
    });

    it('renders every group from one API read, with the browser probes and the manual list', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/system/check', check_healthy_facts());

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'check']), check_production_config())->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_same(1, api_calls_to('/api/v1/system/check'));
        assert_contains('<script type="module" src="/assets/js/config-check.js"></script>', $body);
        foreach (['PHP runtime', 'API service and host', 'Database', 'Web server, as your browser sees it', 'Check by hand'] as $heading) {
            assert_contains($heading, $body);
        }
        assert_contains('data-clara-check="db.audit_grants" data-status="ok"', $body);
        assert_contains('data-clara-probe="browser.admin_api"', $body);
        assert_contains('Runs in the browser and needs JavaScript.', $body);
        assert_contains('class="table table-sm', $body);
        // The strings the probes compose client-side travel in the data-i18n block (REQ-UI-008).
        assert_contains('admin.check.browser.docs.exposed', $body);
        assert_not_contains('secret-token-value-not-for-display', $body);
    });

    it('reports an API that cannot answer as the finding instead of failing the page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/system/check', ['error' => 'internal', 'message' => 'internal error', 'status' => 500], 500);

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'check']))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_contains('data-clara-check="api.reachable" data-status="fail"', $body);
        assert_contains('The API could not answer the check (internal)', $body);
        assert_contains('data-clara-check="db.engine" data-status="skip"', $body);
    });

    it('is closed to anyone who is not an administrator', function (): void {
        sign_in(['is_admin' => 0]);
        queue_shell();

        $page = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'check']))->dispatch();

        assert_same(403, $page->status());
        assert_same(0, api_calls_to('/api/v1/system/check'), 'the guard answers before any read');
    });
});

describe('ConfigCheck rating (INSTALL.md)', function (): void {
    it('finds nothing to report on a host installed as the guide says', function (): void {
        $groups = check_evaluate();

        assert_same([ConfigCheck::FAIL => 0, ConfigCheck::WARN => 0], ConfigCheck::summary($groups));
        assert_same(['php', 'web', 'api', 'db'], array_column($groups, 'key'));
    });

    it('rates the caller address by who could have chosen it (REQ-API-125)', function (): void {
        // The reference chain: nginx reports its own $remote_addr as both halves, so the address is
        // the connection's and no one else's.
        assert_same('ok', check_statuses(check_evaluate())['web.client_ip']);

        // A proxy in front of nginx forwards the browser address it saw, and is listed for it.
        $forwarded = check_statuses(check_evaluate(
            check_production_config(['TRUSTED_PROXY_CIDRS' => '10.0.0.0/8']),
            ['remote_addr' => '10.0.0.5', 'real_ip_header' => '203.0.113.7']
        ));
        assert_same('ok', $forwarded['web.client_ip'], 'listed proxy: the forwarded address is used');

        // The same peer unlisted: its forwarded address is discarded, which leaves every caller
        // behind it sharing one rate-limit bucket — a misconfiguration worth naming.
        $ignored = check_statuses(check_evaluate(null, ['remote_addr' => '10.0.0.5', 'real_ip_header' => '203.0.113.7']));
        assert_same('warn', $ignored['web.client_ip']);

        // Nothing forwarded: right under fastcgi_params, wrong behind another proxy — so the page
        // states it without calling it a problem.
        assert_same('info', check_statuses(check_evaluate(null, ['real_ip_header' => '']))['web.client_ip']);

        // A PHP-FPM unix socket that never learned who called (no fastcgi_params at all).
        assert_same('warn', check_statuses(check_evaluate(null, ['remote_addr' => '']))['web.client_ip']);
    });

    it('rates the PHP runtime: version, extensions, the LDAP prerequisites and limits', function (): void {
        $statuses = check_statuses(check_evaluate(null, [
            'php_version' => '8.2.20', 'php_version_id' => 80220,
            'extensions' => ['mbstring' => false, 'ldap' => false, 'pdo_mysql' => false],
            'proc_open' => false, 'opcache' => false,
            'post_max_size' => 8 * 1048576, 'memory_limit' => 128 * 1048576,
        ]));

        assert_same('fail', $statuses['php.version']);
        assert_same('fail', $statuses['php.extensions']);
        assert_same('warn', $statuses['php.ldap'], 'no LDAP server configured: a warning');
        assert_same('warn', $statuses['php.cli']);
        assert_same('warn', $statuses['php.opcache']);
        assert_same('info', $statuses['php.pdo']);
        assert_same('warn', $statuses['php.upload']);
        assert_same('warn', $statuses['php.memory']);

        $missing = check_find(check_evaluate(null, ['extensions' => ['mbstring' => false]]), 'php.extensions');
        assert_same('mbstring', $missing['params']['list']);
    });

    it('makes the LDAP prerequisites problems once an LDAP server is configured', function (): void {
        $config = check_production_config([
            'LDAP_SERVER_1_URL' => 'ldap://dir.example.org', 'LDAP_SERVER_1_SEARCH_BASE' => 'dc=example,dc=org',
        ]);
        $statuses = check_statuses(check_evaluate($config, ['php_cli' => '']));

        assert_same('fail', $statuses['php.cli']);
        assert_same('warn', $statuses['web.sign_in'], 'ldap:// rather than ldaps://');
        assert_same('LDAP_SERVER_1_URL', check_find(check_evaluate($config), 'web.sign_in')['params']['list']);
    });

    it('reports the required OIDC email verification claim when OAuth is configured', function (): void {
        $config = check_production_config([
            'OAUTH2_1_ISSUER' => 'https://idp.example.org',
            'OAUTH2_1_CLIENT_ID' => 'clara',
            'OAUTH2_1_CLIENT_SECRET' => 'secret',
        ]);
        $check = check_find(check_evaluate($config), 'web.oidc_email_verified');

        assert_same(ConfigCheck::INFO, $check['status']);
        assert_same('admin.check.web.oidc_email_verified.required', $check['messageKey']);
    });

    it('rates the web files: .env mode and placeholders, session directory, writable code', function (): void {
        $statuses = check_statuses(check_evaluate(null, [
            'https' => false,
            'host' => 'other.example.org',
            'env_file' => ['exists' => true, 'mode' => 0644, 'writable' => false, 'placeholders' => ['ANON_SALT', 'DB_PASSWORD']],
            'session_dir' => ['mode' => 0755, 'in_public' => false, 'in_web_root' => false],
            'code_writable' => ['web/app'],
        ]));

        assert_same('fail', $statuses['web.https']);
        assert_same('warn', $statuses['web.public_url']);
        assert_same('fail', $statuses['web.env_file']);
        assert_same('fail', $statuses['web.placeholders']);
        assert_same('warn', $statuses['web.session_dir']);
        assert_same('warn', $statuses['web.code']);

        $public = check_statuses(check_evaluate(null, ['session_dir' => ['mode' => 0700, 'in_public' => true, 'in_web_root' => true]]));
        assert_same('fail', $public['web.session_dir']);
    });

    it('rates the API side: weak secrets, an unchanged bootstrap password, clock and SELinux', function (): void {
        $groups = check_evaluate(null, [], check_healthy_facts([
            'weak_secrets' => ['ANON_SALT'],
            'bootstrap' => ['password_unchanged' => true],
            'api_listen_loopback' => false, 'api_addr' => '0.0.0.0:8080',
            'running_as_root' => true,
            'trusted_proxies_configured' => false,
            'smtp_configured' => false,
            'host' => ['clock_synchronized' => false, 'selinux' => 'permissive'],
            'app_env' => 'development',
        ]));
        $statuses = check_statuses($groups);

        assert_same('fail', $statuses['api.secrets']);
        assert_same('ANON_SALT', check_find($groups, 'api.secrets')['params']['list']);
        assert_same('warn', $statuses['api.bootstrap']);
        assert_same('warn', $statuses['api.listen']);
        assert_same('warn', $statuses['api.user']);
        assert_same('warn', $statuses['api.proxies']);
        assert_same('info', $statuses['api.smtp']);
        assert_same('warn', $statuses['api.clock']);
        assert_same('warn', $statuses['api.selinux']);
        assert_same('warn', $statuses['api.same_config'], 'the API reads another APP_ENV than PHP');
    });

    it('rates the database: version, settings, grants and partitions', function (): void {
        $year = (int) gmdate('Y');
        $facts = check_healthy_facts(['database' => [
            'version' => '10.11.6-MariaDB',
            'character_set_server' => 'latin1',
            'time_zone' => 'SYSTEM', 'system_time_zone' => 'CEST',
            'max_allowed_packet' => 16 * 1048576,
            'bind_address' => '',
            'grants' => ['audit_writable' => ['audit_events'], 'tables_missing_write' => ['added_by_upgrade']],
        ]]);
        // Lists are replaced whole, not merged index by index.
        $facts['database']['audit_partitions']['audit_record_views'] = ['p' . $year];
        $groups = check_evaluate(null, [], $facts);
        $statuses = check_statuses($groups);

        assert_same('fail', $statuses['db.version']);
        assert_same('warn', $statuses['db.charset']);
        assert_same('warn', $statuses['db.time_zone']);
        assert_same('warn', $statuses['db.packet']);
        assert_same('warn', $statuses['db.bind']);
        assert_same('fail', $statuses['db.audit_grants']);
        assert_same('fail', $statuses['db.table_grants']);
        assert_same('warn', $statuses['db.partitions']);
        assert_same('audit_record_views.p' . ($year + 1), check_find($groups, 'db.partitions')['params']['list']);
        assert_same('info', $statuses['db.schema']);
    });

    it('treats SQLite as a production problem and a development fact', function (): void {
        $sqlite = check_healthy_facts();
        $sqlite['database'] = ['connection' => 'sqlite', 'schema_version' => 15, 'version' => '3.50.1'];

        assert_same('fail', check_statuses(check_evaluate(null, [], $sqlite))['db.engine']);
        $dev = check_statuses(check_evaluate(test_config(), ['https' => false, 'host' => 'localhost:8000'], $sqlite + ['app_env' => 'development']));
        assert_same('info', $dev['db.engine']);
        assert_true(!isset($dev['db.audit_grants']), 'no MariaDB checks on SQLite');
    });

    it('turns hardening findings into information in development', function (): void {
        $facts = check_healthy_facts([
            'app_env' => 'development', 'web_public_url' => 'http://localhost:8000',
            'weak_secrets' => ['INTERNAL_SERVICE_TOKEN', 'ANON_SALT'],
            'database' => ['grants' => ['audit_writable' => ['audit_events', 'audit_record_views'], 'drop' => true]],
        ]);
        $statuses = check_statuses(check_evaluate(test_config(), [
            'https' => false, 'host' => 'localhost:8000', 'code_writable' => ['web/app'],
            'env_file' => ['exists' => true, 'mode' => 0640, 'writable' => true, 'placeholders' => []],
        ], $facts));

        assert_same('warn', $statuses['web.app_env'], 'running development is itself worth a warning');
        assert_same('info', $statuses['web.https']);
        assert_same('info', $statuses['web.code']);
        assert_same('info', $statuses['web.env_file']);
        assert_same('info', $statuses['api.secrets']);
        assert_same('info', $statuses['db.audit_grants']);
        assert_same('ok', $statuses['api.same_config']);
    });

    it('reads .env placeholders by variable name only', function (): void {
        $path = sys_get_temp_dir() . '/clara-check-' . bin2hex(random_bytes(4)) . '.env';
        file_put_contents($path, "# a comment with CHANGE_ME\nAPP_ENV=production\nDB_PASSWORD=CHANGE_ME\nexport ANON_SALT=\"CHANGE_ME\"\nWEB_PUBLIC_URL=https://x\n");
        chmod($path, 0640);

        $file = ConfigCheck::envFile($path);
        unlink($path);

        assert_true($file['exists']);
        assert_same(0640, $file['mode']);
        assert_same(['DB_PASSWORD', 'ANON_SALT'], $file['placeholders']);
        assert_same(['exists' => false, 'mode' => null, 'writable' => false, 'placeholders' => []], ConfigCheck::envFile($path));
    });

    it('names every outcome by a catalog key that exists', function (): void {
        $en = require CLARA_WEB_ROOT . '/app/i18n/en.php';
        $variants = [
            check_evaluate(),
            check_evaluate(test_config(), ['https' => false]),
        ];
        // An API that cannot answer: evaluate with no facts.
        $variants[] = ConfigCheck::evaluate(check_production_config(), check_healthy_env(), null, 'internal', 2026);
        // The caller-address chain in each state it can arrive in (REQ-API-125).
        foreach ([[], ['remote_addr' => ''], ['real_ip_header' => ''], ['remote_addr' => '10.0.0.5']] as $env) {
            $variants[] = check_evaluate(null, $env);
        }
        $variants[] = check_evaluate(
            check_production_config(['TRUSTED_PROXY_CIDRS' => '10.0.0.0/8']),
            ['remote_addr' => '10.0.0.5', 'real_ip_header' => '203.0.113.7']
        );
        foreach ($variants as $groups) {
            foreach ($groups as $group) {
                assert_true(isset($en['admin.check.group.' . $group['key']]));
                foreach ($group['checks'] as $check) {
                    assert_true(isset($en['admin.check.' . $check['id'] . '.title']), $check['id'] . ' title');
                    assert_true(isset($en[$check['messageKey']]), $check['messageKey']);
                }
            }
        }
        foreach (ConfigCheck::BROWSER_PROBES as $probe) {
            assert_true(isset($en['admin.check.' . $probe['id'] . '.title']), $probe['id']);
        }
        foreach (ConfigCheck::MANUAL as $item) {
            assert_true(isset($en[$item['key']]), $item['key']);
        }
    });
});
