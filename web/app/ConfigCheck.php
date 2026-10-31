<?php
// The Configuration Check (User_Interface_Design.md §5.10, REQ-UI-061): INSTALL.md turned
// into checks an administrator can read in the Control Panel. Three sources feed it — the PHP
// process itself (probe()), the API's own facts about its configuration, its host and the
// database (`GET /api/v1/system/check`, REQ-API-159), and the browser, which asks nginx the
// routing questions only an outside caller can (config-check.js) — and this class decides,
// for the first two, which value is a problem.
//
// Every result names its outcome by catalog key (REQ-UI-008) and its INSTALL.md section, never
// a secret: a weak secret or a placeholder is reported by its variable name only (REQ-CFG-021).
// Hardening findings that a development checkout has by design (writable code, plain HTTP,
// development secrets) are warnings in production and information in development.

declare(strict_types=1);

namespace Clara;

final class ConfigCheck
{
    public const OK = 'ok';
    public const WARN = 'warn';
    public const FAIL = 'fail';
    public const INFO = 'info';
    public const SKIP = 'skip';

    /** Extensions the web layer cannot run without (Technology_Stack_Design.md §2, INSTALL.md §4.2). */
    private const REQUIRED_EXTENSIONS = ['curl', 'json', 'mbstring', 'openssl', 'session'];

    /** In the design's extension set but unused by the web layer itself (INSTALL.md §4.2). */
    private const DESIGN_EXTENSIONS = ['pdo', 'pdo_mysql'];

    /** Imports accept up to 32 MiB (INSTALL.md §10–§11); PHP must accept the same body. */
    private const IMPORT_BYTES = 32 * 1024 * 1024;

    /** INSTALL.md §10 pool value. */
    private const MEMORY_BYTES = 256 * 1024 * 1024;

    /** INSTALL.md §8.1: room for single values from a 32 MiB import. */
    private const PACKET_BYTES = 64 * 1024 * 1024;

    /** What INSTALL.md cannot let this page check: the host's outside and its operations (§17). */
    public const MANUAL = [
        ['key' => 'admin.check.manual.firewall', 'ref' => '§12'],
        ['key' => 'admin.check.manual.certificate', 'ref' => '§11'],
        ['key' => 'admin.check.manual.mariadb_secure', 'ref' => '§8.1'],
        ['key' => 'admin.check.manual.backups', 'ref' => '§15.2'],
    ];

    /** The probes config-check.js runs from the browser (§5.10): rendered pending, then filled. */
    public const BROWSER_PROBES = [
        ['id' => 'browser.admin_api', 'ref' => '§11.2'],
        ['id' => 'browser.docs', 'ref' => '§13.1'],
        ['id' => 'browser.assets', 'ref' => '§11.2'],
        ['id' => 'browser.hsts', 'ref' => '§11.2'],
    ];

    private function __construct() {}

    /**
     * Reads what the web checks need from this PHP process — kept apart from evaluate() so
     * a test can hand in any environment.
     *
     * @param array<string, mixed> $server the request's server variables ($_SERVER)
     * @return array<string, mixed>
     */
    public static function probe(Config $config, array $server): array
    {
        $extensions = [];
        foreach (array_merge(self::REQUIRED_EXTENSIONS, ['ldap'], self::DESIGN_EXTENSIONS) as $name) {
            $extensions[$name] = extension_loaded($name);
        }

        $cli = '';
        if (function_exists('proc_open')) {
            try {
                $cli = PhpCli::binary();
            } catch (ConfigError) {
                $cli = '';
            }
        }

        $webRoot = self::real(CLARA_WEB_ROOT);
        $writable = [];
        foreach (['app', 'views', 'public', 'assets'] as $dir) {
            if (is_writable(CLARA_WEB_ROOT . '/' . $dir)) {
                $writable[] = 'web/' . $dir;
            }
        }

        $sessionDir = self::real($config->sessionDir);
        $https = strtolower((string) ($server['HTTPS'] ?? ''));

        return [
            'php_version' => PHP_VERSION,
            'php_version_id' => PHP_VERSION_ID,
            'extensions' => $extensions,
            'opcache' => extension_loaded('Zend OPcache') && filter_var(ini_get('opcache.enable'), FILTER_VALIDATE_BOOL),
            'proc_open' => function_exists('proc_open'),
            'php_cli' => $cli,
            'post_max_size' => self::bytes((string) ini_get('post_max_size')),
            'upload_max_filesize' => self::bytes((string) ini_get('upload_max_filesize')),
            'memory_limit' => self::bytes((string) ini_get('memory_limit')),
            'https' => $https !== '' && $https !== 'off',
            'host' => strtolower((string) ($server['HTTP_HOST'] ?? '')),
            // Both halves of the caller-address question (§5.10, REQ-API-125): what this host saw on
            // the wire, and what the proxy said the caller was.
            'remote_addr' => trim((string) ($server['REMOTE_ADDR'] ?? '')),
            'real_ip_header' => trim((string) ($server['HTTP_X_REAL_IP'] ?? '')),
            'env_file' => self::envFile(CLARA_REPO_ROOT . '/.env'),
            'session_dir' => [
                'mode' => is_dir($config->sessionDir) ? (fileperms($config->sessionDir) & 0777) : null,
                'in_public' => self::inside($sessionDir, $webRoot . '/public'),
                'in_web_root' => self::inside($sessionDir, $webRoot),
            ],
            'code_writable' => $writable,
        ];
    }

    /**
     * Every check, grouped as the page shows them.
     *
     * @param array<string, mixed>      $env      probe()'s snapshot
     * @param array<string, mixed>|null $api      the API's facts, null when they could not be read
     * @param string                    $apiError the API's error code when $api is null
     * @return list<array{key: string, checks: list<array<string, mixed>>}>
     */
    public static function evaluate(Config $config, array $env, ?array $api, string $apiError, int $year): array
    {
        $production = !$config->isDevelopment;
        // Hardening a development checkout lacks by design: a warning only where it counts.
        $harden = $production ? self::WARN : self::INFO;

        return [
            ['key' => 'php', 'checks' => self::phpChecks($config, $env)],
            ['key' => 'web', 'checks' => self::webChecks($config, $env, $production, $harden)],
            ['key' => 'api', 'checks' => self::apiChecks($config, $api, $apiError, $production, $harden)],
            ['key' => 'db', 'checks' => self::databaseChecks($api, $year, $production, $harden)],
        ];
    }

    /** The overall line: how many results are problems and how many warnings. */
    public static function summary(array $groups): array
    {
        $counts = [self::FAIL => 0, self::WARN => 0];
        foreach ($groups as $group) {
            foreach ($group['checks'] as $check) {
                if (isset($counts[$check['status']])) {
                    $counts[$check['status']]++;
                }
            }
        }

        return $counts;
    }

    // --- PHP runtime (INSTALL.md §4.2, §10) ---------------------------------------

    private static function phpChecks(Config $config, array $env): array
    {
        $checks = [];
        $ldap = $config->ldapServers !== [];

        $id = (int) $env['php_version_id'];
        $checks[] = self::result('php.version', match (true) {
            $id < 80300 => self::FAIL,
            $id >= 80500 => self::WARN,
            default => self::OK,
        }, match (true) {
            $id < 80300 => 'old',
            $id >= 80500 => 'new',
            default => 'ok',
        }, ['version' => (string) $env['php_version']], '§4.2');

        $missing = array_values(array_filter(self::REQUIRED_EXTENSIONS, static fn (string $e): bool => !$env['extensions'][$e]));
        $checks[] = $missing === []
            ? self::result('php.extensions', self::OK, 'ok', [], '§4.2')
            : self::result('php.extensions', self::FAIL, 'missing', ['list' => implode(', ', $missing)], '§4.2');

        $checks[] = match (true) {
            $env['extensions']['ldap'] => self::result('php.ldap', self::OK, 'ok', [], '§4.2'),
            $ldap => self::result('php.ldap', self::FAIL, 'missing_used', [], '§4.2'),
            default => self::result('php.ldap', self::WARN, 'missing', [], '§4.2'),
        };

        // Without proc_open or a CLI interpreter an LDAP login cannot start its directory attempts.
        $spawnSeverity = $ldap ? self::FAIL : self::WARN;
        $checks[] = match (true) {
            !$env['proc_open'] => self::result('php.cli', $spawnSeverity, 'no_proc_open', [], '§10'),
            $env['php_cli'] === '' => self::result('php.cli', $spawnSeverity, 'missing', [], '§4.2'),
            default => self::result('php.cli', self::OK, 'ok', ['path' => (string) $env['php_cli']], '§4.2'),
        };

        $checks[] = $env['opcache']
            ? self::result('php.opcache', self::OK, 'ok', [], '§4.2')
            : self::result('php.opcache', self::WARN, 'off', [], '§4.2');

        $missingDesign = array_values(array_filter(self::DESIGN_EXTENSIONS, static fn (string $e): bool => !$env['extensions'][$e]));
        $checks[] = $missingDesign === []
            ? self::result('php.pdo', self::OK, 'ok', [], '§4.2')
            : self::result('php.pdo', self::INFO, 'missing', ['list' => implode(', ', $missingDesign)], '§4.2');

        $upload = min((int) $env['post_max_size'], (int) $env['upload_max_filesize']);
        $checks[] = $upload >= self::IMPORT_BYTES || $upload <= 0
            ? self::result('php.upload', self::OK, 'ok', ['size' => $upload <= 0 ? '∞' : self::mib($upload)], '§10')
            : self::result('php.upload', self::WARN, 'small', ['size' => self::mib($upload)], '§10');

        $memory = (int) $env['memory_limit'];
        $checks[] = $memory < 0 || $memory >= self::MEMORY_BYTES
            ? self::result('php.memory', self::OK, 'ok', ['size' => $memory < 0 ? '∞' : self::mib($memory)], '§10')
            : self::result('php.memory', self::WARN, 'small', ['size' => self::mib($memory)], '§10');

        return $checks;
    }

    // --- Web configuration and files (INSTALL.md §5, §6, §14) -------------------

    private static function webChecks(Config $config, array $env, bool $production, string $harden): array
    {
        $checks = [];

        $checks[] = $production
            ? self::result('web.app_env', self::OK, 'production', [], '§6')
            : self::result('web.app_env', self::WARN, 'development', [], '§6');

        $checks[] = match (true) {
            $env['https'] => self::result('web.https', self::OK, 'ok', [], '§11'),
            $production => self::result('web.https', self::FAIL, 'plain', [], '§11'),
            default => self::result('web.https', self::INFO, 'plain', [], '§11'),
        };

        $url = parse_url($config->webPublicUrl);
        $urlHost = strtolower((string) ($url['host'] ?? '')) . (isset($url['port']) ? ':' . $url['port'] : '');
        $checks[] = match (true) {
            ($url['scheme'] ?? '') !== 'https' => self::result('web.public_url', $harden, 'not_https', ['url' => $config->webPublicUrl], '§6'),
            $env['host'] !== '' && $env['host'] !== $urlHost => self::result('web.public_url', self::WARN, 'other_host',
                ['url' => $config->webPublicUrl, 'host' => (string) $env['host']], '§6'),
            default => self::result('web.public_url', self::OK, 'ok', ['url' => $config->webPublicUrl], '§6'),
        };

        // The address the API limits by and audits (REQ-API-125): REMOTE_ADDR, unless the peer sits
        // inside TRUSTED_PROXY_CIDRS and forwarded another one. Neither half is the caller's choice
        // — which is exactly why it is worth a line here: a chain that arrives as one address for
        // every browser quietly disables limiting, and a chain that believes the browser lets
        // anyone pick the identity to be limited by (finding F9).
        $peer = filter_var($env['remote_addr'] ?? '', FILTER_VALIDATE_IP);
        $forwarded = filter_var($env['real_ip_header'] ?? '', FILTER_VALIDATE_IP);
        $checks[] = match (true) {
            $peer === false => self::result('web.client_ip', $harden, 'no_peer', [], '§11'),
            $forwarded === false => self::result('web.client_ip', self::INFO, 'no_header',
                ['ranges' => $config->trustedProxies->describe()], '§11'),
            $forwarded === $peer => self::result('web.client_ip', self::OK, 'same', [], '§11'),
            $config->trustedProxies->contains((string) $peer) => self::result('web.client_ip', self::OK, 'forwarded', [], '§11'),
            default => self::result('web.client_ip', $harden, 'ignored',
                ['ranges' => $config->trustedProxies->describe()], '§11'),
        };

        $file = $env['env_file'];
        if (!$file['exists']) {
            $checks[] = self::result('web.env_file', self::INFO, 'absent', [], '§6');
        } else {
            $mode = (int) $file['mode'];
            $checks[] = match (true) {
                ($mode & 0007) !== 0 => self::result('web.env_file', self::FAIL, 'world', ['mode' => sprintf('%04o', $mode)], '§6'),
                $file['writable'] => self::result('web.env_file', $harden, 'writable', ['mode' => sprintf('%04o', $mode)], '§6'),
                default => self::result('web.env_file', self::OK, 'ok', ['mode' => sprintf('%04o', $mode)], '§6'),
            };
            $checks[] = $file['placeholders'] === []
                ? self::result('web.placeholders', self::OK, 'ok', [], '§6')
                : self::result('web.placeholders', self::FAIL, 'found', ['list' => implode(', ', $file['placeholders'])], '§6');
        }

        $session = $env['session_dir'];
        $sessionMode = $session['mode'] === null ? '' : sprintf('%04o', $session['mode']);
        $checks[] = match (true) {
            $session['in_public'] => self::result('web.session_dir', self::FAIL, 'public', ['path' => $config->sessionDir], '§5'),
            $session['mode'] !== null && ($session['mode'] & 0077) !== 0 => self::result('web.session_dir', $harden, 'open',
                ['path' => $config->sessionDir, 'mode' => $sessionMode], '§5'),
            $session['in_web_root'] => self::result('web.session_dir', $harden, 'in_code', ['path' => $config->sessionDir], '§5'),
            default => self::result('web.session_dir', self::OK, 'ok', ['path' => $config->sessionDir, 'mode' => $sessionMode], '§5'),
        };

        $checks[] = $env['code_writable'] === []
            ? self::result('web.code', self::OK, 'ok', [], '§5')
            : self::result('web.code', $harden, 'writable', ['list' => implode(', ', $env['code_writable'])], '§5');

        $plain = [];
        foreach ($config->ldapServers as $server) {
            if (stripos($server['url'], 'ldap://') === 0) {
                $plain[] = 'LDAP_SERVER_' . $server['index'] . '_URL';
            }
        }
        foreach ($config->oauthProviders as $provider) {
            if (stripos($provider['issuer'], 'http://') === 0) {
                $plain[] = 'OAUTH2_' . $provider['index'] . '_ISSUER';
            }
        }
        $checks[] = match (true) {
            $config->ldapServers === [] && $config->oauthProviders === [] => self::result('web.sign_in', self::INFO, 'local_only', [], '§14'),
            $plain !== [] => self::result('web.sign_in', $harden, 'plain', ['list' => implode(', ', $plain)], '§14'),
            default => self::result('web.sign_in', self::OK, 'ok', [
                'oauth' => (string) count($config->oauthProviders), 'ldap' => (string) count($config->ldapServers),
            ], '§14'),
        };
        if ($config->oauthProviders !== []) {
            $checks[] = self::result('web.oidc_email_verified', self::INFO, 'required', [], '§14');
            // Which providers' roles set a user's preferred region (REQ-CFG-037): a fact, not a finding.
            $roles = [];
            foreach ($config->oauthProviders as $provider) {
                if ($provider['roles_attr'] !== '') {
                    $roles[] = 'OAUTH2_' . $provider['index'] . '_ROLES_ATTR=' . $provider['roles_attr'];
                }
            }
            $checks[] = $roles === []
                ? self::result('web.oidc_roles', self::INFO, 'unset', [], '§14')
                : self::result('web.oidc_roles', self::INFO, 'set', ['list' => implode(', ', $roles)], '§14');
        }

        return $checks;
    }

    // --- The API service (INSTALL.md §6, §7, §9) --------------------------------

    private static function apiChecks(Config $config, ?array $api, string $apiError, bool $production, string $harden): array
    {
        if ($api === null) {
            return [self::result('api.reachable', self::FAIL, 'failed', ['code' => $apiError !== '' ? $apiError : 'internal'], '§9.3')];
        }

        $checks = [self::result('api.reachable', self::OK, 'ok', [], '§9.3')];

        $apiEnv = (string) ($api['app_env'] ?? '');
        $apiUrl = rtrim((string) ($api['web_public_url'] ?? ''), '/');
        $checks[] = $apiEnv === $config->appEnv && $apiUrl === $config->webPublicUrl
            ? self::result('api.same_config', self::OK, 'ok', [], '§1')
            : self::result('api.same_config', self::WARN, 'differs', [
                'api_env' => $apiEnv, 'web_env' => $config->appEnv, 'api_url' => $apiUrl, 'web_url' => $config->webPublicUrl,
            ], '§1');

        $checks[] = !empty($api['api_listen_loopback'])
            ? self::result('api.listen', self::OK, 'ok', ['addr' => (string) ($api['api_addr'] ?? '')], '§1')
            : self::result('api.listen', $harden, 'open', ['addr' => (string) ($api['api_addr'] ?? '')], '§1');

        $checks[] = match ($api['running_as_root'] ?? null) {
            false => self::result('api.user', self::OK, 'ok', [], '§9.1'),
            true => self::result('api.user', $harden, 'root', [], '§9.1'),
            default => self::result('api.user', self::SKIP, 'unknown', [], '§9.1'),
        };

        $weak = is_array($api['weak_secrets'] ?? null) ? $api['weak_secrets'] : [];
        $checks[] = $weak === []
            ? self::result('api.secrets', self::OK, 'ok', [], '§6')
            : self::result('api.secrets', $production ? self::FAIL : self::INFO, 'weak', ['list' => implode(', ', $weak)], '§6');

        $bootstrap = is_array($api['bootstrap'] ?? null) ? $api['bootstrap'] : [];
        $checks[] = match (true) {
            empty($bootstrap['password_configured']) => self::result('api.bootstrap', self::OK, 'not_configured', [], '§6'),
            ($bootstrap['password_unchanged'] ?? null) === true => self::result('api.bootstrap', $harden, 'unchanged', [], '§13.2'),
            ($bootstrap['password_unchanged'] ?? null) === false => self::result('api.bootstrap', self::OK, 'changed', [], '§13.2'),
            default => self::result('api.bootstrap', self::INFO, 'no_account', [], '§13.2'),
        };

        $checks[] = !empty($api['smtp_configured'])
            ? self::result('api.smtp', self::OK, 'ok', [], '§14')
            : self::result('api.smtp', self::INFO, 'off', [], '§14');

        $checks[] = !empty($api['trusted_proxies_configured'])
            ? self::result('api.proxies', self::OK, 'ok', [], '§6')
            : self::result('api.proxies', $harden, 'off', [], '§6');

        $host = is_array($api['host'] ?? null) ? $api['host'] : [];
        $checks[] = match ($host['clock_synchronized'] ?? null) {
            true => self::result('api.clock', self::OK, 'ok', [], '§4.1'),
            false => self::result('api.clock', self::WARN, 'unsynced', [], '§4.1'),
            default => self::result('api.clock', self::SKIP, 'unknown', [], '§4.1'),
        };

        $checks[] = match ($host['selinux'] ?? null) {
            'enforcing' => self::result('api.selinux', self::OK, 'ok', [], '§7'),
            'permissive' => self::result('api.selinux', $harden, 'permissive', [], '§7'),
            'disabled' => self::result('api.selinux', $harden, 'disabled', [], '§7'),
            default => self::result('api.selinux', self::SKIP, 'unknown', [], '§7'),
        };

        return $checks;
    }

    // --- The database (INSTALL.md §8, §9.2, §15.5) -----------------------------

    private static function databaseChecks(?array $api, int $year, bool $production, string $harden): array
    {
        $db = is_array($api['database'] ?? null) ? $api['database'] : null;
        if ($db === null) {
            return [self::result('db.engine', self::SKIP, 'unknown', [], '§8')];
        }

        $version = (string) ($db['version'] ?? '');
        $schema = ['version' => (string) ($db['schema_version'] ?? '')];
        if (($db['connection'] ?? '') !== 'mariadb') {
            return [
                self::result('db.engine', $production ? self::FAIL : self::INFO, 'sqlite', ['version' => $version], '§2'),
                self::result('db.schema', self::INFO, 'version', $schema, '§15.4'),
            ];
        }

        $checks = [self::result('db.engine', self::OK, 'mariadb', [], '§2')];

        $checks[] = match (true) {
            preg_match('/^(\d+)\.(\d+)/', $version, $m) !== 1 || !str_contains($version, 'MariaDB')
                => self::result('db.version', self::FAIL, 'other', ['version' => $version], '§4.3'),
            (int) $m[1] < 11 => self::result('db.version', self::FAIL, 'old', ['version' => $version], '§4.3'),
            (int) $m[1] === 11 && (int) $m[2] < 4 => self::result('db.version', self::WARN, 'old', ['version' => $version], '§4.3'),
            default => self::result('db.version', self::OK, 'ok', ['version' => $version], '§4.3'),
        };

        $charsets = [
            $db['character_set_server'] ?? null, $db['database_character_set'] ?? null,
        ];
        $collations = [$db['collation_server'] ?? null, $db['database_collation'] ?? null];
        $checks[] = match (true) {
            in_array(null, $charsets, true) || in_array(null, $collations, true) => self::result('db.charset', self::SKIP, 'unknown', [], '§8.1'),
            $charsets === ['utf8mb4', 'utf8mb4'] && $collations === ['utf8mb4_unicode_ci', 'utf8mb4_unicode_ci']
                => self::result('db.charset', self::OK, 'ok', [], '§8.1'),
            default => self::result('db.charset', self::WARN, 'other', [
                'server' => $charsets[0] . ' / ' . $collations[0], 'database' => $charsets[1] . ' / ' . $collations[1],
            ], '§8.1'),
        };

        $zone = $db['time_zone'] ?? null;
        $utc = ['+00:00', 'UTC', 'Etc/UTC', 'GMT'];
        $checks[] = match (true) {
            $zone === null => self::result('db.time_zone', self::SKIP, 'unknown', [], '§8.1'),
            in_array($zone, $utc, true), $zone === 'SYSTEM' && in_array($db['system_time_zone'] ?? '', $utc, true)
                => self::result('db.time_zone', self::OK, 'ok', [], '§8.1'),
            default => self::result('db.time_zone', self::WARN, 'other', [
                'zone' => $zone === 'SYSTEM' ? 'SYSTEM (' . ($db['system_time_zone'] ?? '?') . ')' : (string) $zone,
            ], '§8.1'),
        };

        $packet = $db['max_allowed_packet'] ?? null;
        $checks[] = match (true) {
            !is_int($packet) => self::result('db.packet', self::SKIP, 'unknown', [], '§8.1'),
            $packet >= self::PACKET_BYTES => self::result('db.packet', self::OK, 'ok', ['size' => self::mib($packet)], '§8.1'),
            default => self::result('db.packet', self::WARN, 'small', ['size' => self::mib($packet)], '§8.1'),
        };

        $bind = $db['bind_address'] ?? null;
        $checks[] = match (true) {
            $bind === null => self::result('db.bind', self::SKIP, 'unknown', [], '§8.1'),
            in_array($bind, ['127.0.0.1', 'localhost', '::1'], true) => self::result('db.bind', self::OK, 'ok', ['address' => $bind], '§8.1'),
            $bind === '' || $bind === '*' || $bind === '0.0.0.0' || $bind === '::'
                => self::result('db.bind', $harden, 'all', [], '§8.1'),
            default => self::result('db.bind', $harden, 'other', ['address' => (string) $bind], '§8.1'),
        };

        $grants = is_array($db['grants'] ?? null) ? $db['grants'] : null;
        if ($grants === null) {
            $checks[] = self::result('db.audit_grants', self::SKIP, 'unknown', [], '§9.2');
            $checks[] = self::result('db.table_grants', self::SKIP, 'unknown', [], '§9.2');
        } else {
            $account = ['account' => (string) ($grants['account'] ?? '')];
            $writable = is_array($grants['audit_writable'] ?? null) ? $grants['audit_writable'] : [];
            $checks[] = match (true) {
                $writable !== [] => self::result('db.audit_grants', $production ? self::FAIL : self::INFO, 'writable',
                    $account + ['list' => implode(', ', $writable)], '§9.2'),
                !empty($grants['drop']) => self::result('db.audit_grants', $harden, 'drop', $account, '§9.2'),
                default => self::result('db.audit_grants', self::OK, 'ok', $account, '§9.2'),
            };
            $missing = is_array($grants['tables_missing_write'] ?? null) ? $grants['tables_missing_write'] : [];
            $checks[] = $missing === []
                ? self::result('db.table_grants', self::OK, 'ok', [], '§15.4')
                : self::result('db.table_grants', self::FAIL, 'missing', ['list' => implode(', ', $missing)], '§15.4');
        }

        $partitions = is_array($db['audit_partitions'] ?? null) ? $db['audit_partitions'] : null;
        if ($partitions === null) {
            $checks[] = self::result('db.partitions', self::SKIP, 'unknown', [], '§15.5');
        } else {
            $absent = [];
            foreach (['audit_events', 'audit_record_views'] as $table) {
                foreach (['p' . $year, 'p' . ($year + 1)] as $name) {
                    if (!in_array($name, is_array($partitions[$table] ?? null) ? $partitions[$table] : [], true)) {
                        $absent[] = $table . '.' . $name;
                    }
                }
            }
            $checks[] = $absent === []
                ? self::result('db.partitions', self::OK, 'ok', ['year' => (string) $year, 'next' => (string) ($year + 1)], '§15.5')
                : self::result('db.partitions', self::WARN, 'missing', ['list' => implode(', ', $absent)], '§15.5');
        }

        $checks[] = self::result('db.schema', self::INFO, 'version', $schema, '§15.4');

        return $checks;
    }

    // --- helpers ----------------------------------------------------------------

    /** @return array{id: string, status: string, messageKey: string, params: array<string, string>, ref: string} */
    private static function result(string $id, string $status, string $outcome, array $params, string $ref): array
    {
        return [
            'id' => $id,
            'status' => $status,
            'messageKey' => 'admin.check.' . $id . '.' . $outcome,
            'params' => $params,
            'ref' => $ref,
        ];
    }

    /**
     * The .env file as INSTALL.md §6 wants it: mode, whether this process could rewrite it,
     * and which variables still carry the `CHANGE_ME` placeholder — names only.
     *
     * @return array{exists: bool, mode: int|null, writable: bool, placeholders: list<string>}
     */
    public static function envFile(string $path): array
    {
        if (!is_file($path)) {
            return ['exists' => false, 'mode' => null, 'writable' => false, 'placeholders' => []];
        }

        $placeholders = [];
        $lines = is_readable($path) ? file($path, FILE_IGNORE_NEW_LINES) : false;
        foreach ($lines === false ? [] : $lines as $line) {
            if (preg_match('/^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=(.*)$/', $line, $m) === 1 && str_contains($m[2], 'CHANGE_ME')) {
                $placeholders[] = $m[1];
            }
        }

        return [
            'exists' => true,
            'mode' => fileperms($path) & 0777,
            'writable' => is_writable($path),
            'placeholders' => $placeholders,
        ];
    }

    /** An ini size ("32M", "-1") in bytes; -1 means unlimited. */
    private static function bytes(string $value): int
    {
        $value = trim($value);
        if ($value === '' || $value === '-1') {
            return -1;
        }

        return (int) @ini_parse_quantity($value);
    }

    private static function mib(int $bytes): string
    {
        return rtrim(rtrim(number_format($bytes / 1048576, 1, '.', ''), '0'), '.') . ' MiB';
    }

    private static function real(string $path): string
    {
        $real = realpath($path);

        return $real === false ? rtrim($path, '/') : $real;
    }

    private static function inside(string $path, string $dir): bool
    {
        return $path === $dir || str_starts_with($path, $dir . DIRECTORY_SEPARATOR);
    }
}
