<?php
// Configuration for the CLARA web layer: environment variables, optionally
// loaded from a .env file (REQ-CFG-001), with the process environment taking
// precedence (REQ-CFG-002). Both components read the same variable names so a
// single .env configures the whole system (REQ-CFG-003); this class only reads
// the subset the web layer consumes.
//
// Validation happens once, at boot of the entry point, and refuses to continue
// with an operator-readable message naming every problem (REQ-CFG-005) — the same
// posture the API takes in REQ-CFG-004. Configuration is immutable after load:
// changing it requires a process restart (REQ-CFG-006).
//
// fromValues() is the whole of the validation and takes an explicit key/value
// map; load() only reads the environment and .env into that shape. That split is
// what lets the router, session and gating tests build a real configuration
// without touching a process environment.

declare(strict_types=1);

namespace Clara;

final class Config
{
    /** Installed theme identifiers (REQ-TECH-027) — mirrors config.UIThemes in Go. */
    public const THEME_INSTALL_ORDER = ['bootstrap', 'darkly', 'yeti'];

    public string $appEnv = 'development';

    public bool $isDevelopment = true;

    /** Base URL of the Go API as PHP reaches it, derived from API_ADDR. */
    public string $apiBaseUrl = '';

    public string $internalServiceToken = '';

    public string $webPublicUrl = '';

    public string $logLevelWeb = 'info';

    public string $sessionDir = '';

    public string $sessionCookieName = 'csms_session';

    public int $sessionLifetime = 28800;

    public bool $sessionCookieSecure = false;

    /** Installation default theme (REQ-CFG-031); a user may override it. */
    public string $uiTheme = 'bootstrap';

    /**
     * Authentication sources as configured: one entry per (name, source) pair —
     * the relation is many-to-many (REQ-CFG-032, REQ-AUTH-064). An empty list
     * means no names are configured anywhere, so all sources form one implicit
     * default set and the login picker is skipped (REQ-AUTH-067).
     *
     * @var list<array{kind: string, name: string}>
     */
    public array $authSources = [];

    private function __construct() {}

    /**
     * Reads the environment (process first, then .env) and validates it.
     *
     * @param string|null $dotenvPath overrides the .env location (tests)
     */
    public static function load(?string $dotenvPath = null): self
    {
        $file = self::readDotEnv($dotenvPath ?? CLARA_REPO_ROOT . '/.env');

        $values = [];
        foreach ($file as $key => $value) {
            $values[$key] = $value;
        }
        // Process environment wins (REQ-CFG-002).
        foreach (array_keys($values) as $key) {
            $fromEnv = self::environment($key);
            if ($fromEnv !== null) {
                $values[$key] = $fromEnv;
            }
        }
        // Variables that only exist in the environment still count.
        foreach (self::relevantKeys() as $key) {
            $fromEnv = self::environment($key);
            if ($fromEnv !== null) {
                $values[$key] = $fromEnv;
            }
        }

        return self::fromValues($values);
    }

    /** Validates an explicit configuration map (REQ-CFG-005). */
    public static function fromValues(array $values): self
    {
        $c = new self();
        $errors = [];
        $get = static fn (string $key, string $default = ''): string => trim((string) ($values[$key] ?? $default));

        // --- core (REQ-CFG-007, REQ-CFG-019) ---
        $c->appEnv = strtolower($get('APP_ENV', 'development'));
        if (!in_array($c->appEnv, ['development', 'production'], true)) {
            $errors[] = "APP_ENV must be 'development' or 'production', got '{$c->appEnv}' (REQ-CFG-007)";
            $c->appEnv = 'development';
        }
        $c->isDevelopment = $c->appEnv === 'development';

        $c->logLevelWeb = strtolower($get('LOG_LEVEL_WEB', 'info'));
        if (!in_array($c->logLevelWeb, ['debug', 'info', 'warn', 'error'], true)) {
            $errors[] = "LOG_LEVEL_WEB must be one of debug|info|warn|error, got '{$c->logLevelWeb}' (REQ-CFG-019)";
            $c->logLevelWeb = 'info';
        }

        // --- upstream API address ---
        // No variable names "the URL PHP calls" (System_Configuration_Design.md §3
        // gives API_ADDR to the API as its listen address), so it is derived here:
        // same host or trusted internal segment (ASM-TECH-3) means the listen
        // address is reachable verbatim, except that a wildcard bind is not a
        // connect target — 0.0.0.0 and :: become loopback. If the two components
        // are ever split across hosts, a dedicated variable is a requirements-level
        // addition to System_Configuration_*, not something to solve in PHP.
        $addr = $get('API_ADDR', '127.0.0.1:8080');
        $c->apiBaseUrl = self::normaliseApiAddr($addr);
        if ($c->apiBaseUrl === '') {
            $errors[] = "API_ADDR must be host:port, got '{$addr}'";
        }

        // --- service boundary (REQ-CFG-013) ---
        $c->internalServiceToken = $get('INTERNAL_SERVICE_TOKEN');
        if ($c->internalServiceToken === '') {
            $errors[] = 'INTERNAL_SERVICE_TOKEN is required and must not be empty (REQ-CFG-013)';
        }

        // --- public URL (REQ-CFG-018) ---
        $c->webPublicUrl = rtrim($get('WEB_PUBLIC_URL'), '/');
        if ($c->webPublicUrl === '') {
            if (!$c->isDevelopment) {
                $errors[] = 'WEB_PUBLIC_URL is required outside development (REQ-CFG-018)';
            }
            $c->webPublicUrl = 'http://localhost:8000';
        }

        // --- sessions (REQ-CFG-017) ---
        $c->sessionDir = $get('SESSION_DIR', CLARA_WEB_ROOT . '/sessions');
        if ($c->sessionDir === '') {
            $errors[] = 'SESSION_DIR must name a directory (REQ-CFG-017)';
        } elseif (!is_dir($c->sessionDir) && !@mkdir($c->sessionDir, 0700, true)) {
            $errors[] = "SESSION_DIR '{$c->sessionDir}' does not exist and could not be created (REQ-CFG-017)";
        } elseif (!is_writable($c->sessionDir)) {
            $errors[] = "SESSION_DIR '{$c->sessionDir}' is not writable by the PHP process (REQ-CFG-017)";
        }

        $c->sessionCookieName = $get('SESSION_COOKIE_NAME', 'csms_session');
        if (preg_match('/^[A-Za-z0-9_]++$/', $c->sessionCookieName) !== 1) {
            $errors[] = "SESSION_COOKIE_NAME must be a cookie-safe token, got '{$c->sessionCookieName}'";
            $c->sessionCookieName = 'csms_session';
        }

        $lifetime = $get('SESSION_LIFETIME', '28800');
        if (preg_match('/^\d+$/', $lifetime) !== 1 || (int) $lifetime < 60) {
            $errors[] = "SESSION_LIFETIME must be an integer number of seconds ≥ 60, got '{$lifetime}'";
            $c->sessionLifetime = 28800;
        } else {
            $c->sessionLifetime = (int) $lifetime;
        }

        $secure = $get('SESSION_COOKIE_SECURE', $c->isDevelopment ? '0' : '1');
        if (!in_array($secure, ['0', '1'], true)) {
            $errors[] = "SESSION_COOKIE_SECURE must be 0 or 1, got '{$secure}'";
            $secure = $c->isDevelopment ? '0' : '1';
        }
        $c->sessionCookieSecure = $secure === '1';

        // --- appearance (REQ-CFG-031, REQ-TECH-027) ---
        $c->uiTheme = strtolower($get('UI_THEME', 'bootstrap'));
        if (!in_array($c->uiTheme, self::THEME_INSTALL_ORDER, true)) {
            $errors[] = 'UI_THEME must name an installed theme ('
                . implode(', ', self::THEME_INSTALL_ORDER) . "), got '{$c->uiTheme}' (REQ-CFG-031)";
        } elseif (!self::themeIsInstalled($c->uiTheme)) {
            // The selectable set is the set of installed files (REQ-TECH-027): a
            // configured theme whose stylesheet was not vendored is a deployment
            // error, and saying so beats serving a page with no styling.
            $errors[] = "UI_THEME '{$c->uiTheme}' has no vendored stylesheet under "
                . 'assets/vendor/bootstrap/themes/ (REQ-TECH-027)';
        }

        // --- authentication sources (REQ-CFG-032) ---
        $c->authSources = self::readAuthSources($get, $errors);

        if ($errors !== []) {
            throw new ConfigError(implode("\n", $errors));
        }

        return $c;
    }

    /** Path of the vendored stylesheet for a theme (REQ-UI-040). */
    public static function themeHref(string $theme): string
    {
        return $theme === 'bootstrap'
            ? '/assets/vendor/bootstrap/bootstrap.min.css'
            : '/assets/vendor/bootstrap/themes/' . $theme . '/bootstrap.min.css';
    }

    /** Whether a theme's stylesheet is present in the vendored tree. */
    public static function themeIsInstalled(string $theme): bool
    {
        if ($theme === 'bootstrap') {
            return is_file(CLARA_WEB_ROOT . '/assets/vendor/bootstrap/bootstrap.min.css');
        }

        return in_array($theme, self::THEME_INSTALL_ORDER, true)
            && is_file(CLARA_WEB_ROOT . '/assets/vendor/bootstrap/themes/' . $theme . '/bootstrap.min.css');
    }

    /**
     * The themes a user may choose from — installed files only (REQ-UI-041).
     *
     * @return list<string>
     */
    public static function installedThemes(): array
    {
        return array_values(array_filter(
            self::THEME_INSTALL_ORDER,
            static fn (string $theme): bool => self::themeIsInstalled($theme)
        ));
    }

    /**
     * Turns a listen address into a connect target. `0.0.0.0`, `::` and an empty
     * host are wildcards: valid to bind, not to dial, so they become loopback
     * (ASM-TECH-3 keeps API and PHP on one host or a trusted segment).
     */
    private static function normaliseApiAddr(string $addr): string
    {
        $addr = trim($addr);
        if ($addr === '') {
            return '';
        }

        // Bracketed IPv6 literal, e.g. [::1]:8080
        if (str_starts_with($addr, '[')) {
            $close = strpos($addr, ']');
            if ($close === false) {
                return '';
            }
            $host = substr($addr, 1, $close - 1);
            $rest = substr($addr, $close + 1);
        } else {
            $colon = strrpos($addr, ':');
            if ($colon === false) {
                return 'http://' . $addr; // no port: the API's default is implied
            }
            $host = substr($addr, 0, $colon);
            $rest = substr($addr, $colon); // keeps ":port"
        }

        if ($host === '' || $host === '0.0.0.0' || $host === '::' || $host === '*') {
            $host = '127.0.0.1';
        }
        if (preg_match('/^[A-Za-z0-9._:\-]+$/', $host) !== 1) {
            return '';
        }

        return 'http://' . $host . $rest;
    }

    /**
     * Collects the configured authentication sources and their display names
     * (REQ-CFG-032). Only what the login page needs in order to decide what it can
     * offer is parsed here — provider details stay with the API.
     *
     * @param callable(string, string=): string $get
     * @param list<string>                      $errors collected name-list problems
     * @return list<array{kind: string, name: string}>
     */
    private static function readAuthSources(callable $get, array &$errors): array
    {
        $sources = [];

        // The local source exists exactly once (one users table, GD-18) but may
        // carry several names; unset means it joins the implicit default set.
        foreach (self::nameList('LOCAL_LOGIN_NAMES', $get('LOCAL_LOGIN_NAMES'), $errors) as $name) {
            $sources[] = ['kind' => 'local', 'name' => $name];
        }

        for ($n = 1; $n <= 3; $n++) {
            // An OAuth2 provider counts when its issuer and client are set.
            if (rtrim($get("OAUTH2_{$n}_ISSUER"), '/') !== '' && $get("OAUTH2_{$n}_CLIENT_ID") !== '') {
                foreach (self::nameList("OAUTH2_{$n}_NAMES", $get("OAUTH2_{$n}_NAMES"), $errors) as $name) {
                    $sources[] = ['kind' => 'oauth2', 'name' => $name];
                }
            }

            // An LDAP server counts when URL and search base are set.
            if ($get("LDAP_SERVER_{$n}_URL") !== '' && $get("LDAP_SERVER_{$n}_SEARCH_BASE") !== '') {
                foreach (self::nameList("LDAP_SERVER_{$n}_NAMES", $get("LDAP_SERVER_{$n}_NAMES"), $errors) as $name) {
                    $sources[] = ['kind' => 'ldap', 'name' => $name];
                }
            }
        }

        return $sources;
    }

    /**
     * Splits a comma-separated name list, trimming entries. An entry that is empty
     * after trimming is rejected at startup (REQ-CFG-032); an empty list
     * contributes nothing, which is how the implicit default set forms
     * (REQ-AUTH-067).
     *
     * @param list<string> $errors
     * @return list<string>
     */
    private static function nameList(string $variable, string $raw, array &$errors): array
    {
        if (trim($raw) === '') {
            return [];
        }

        $names = [];
        foreach (explode(',', $raw) as $part) {
            $name = trim($part);
            if ($name === '') {
                $errors[] = "{$variable}: \"{$raw}\" contains an entry that is empty after trimming (REQ-CFG-032)";
                continue;
            }
            $names[] = $name;
        }

        return $names;
    }

    /**
     * The variables this layer reads — so a value present only in the process
     * environment is seen even when .env does not mention it.
     *
     * @return list<string>
     */
    private static function relevantKeys(): array
    {
        $keys = [
            'APP_ENV', 'API_ADDR', 'INTERNAL_SERVICE_TOKEN', 'WEB_PUBLIC_URL', 'LOG_LEVEL_WEB',
            'SESSION_DIR', 'SESSION_COOKIE_NAME', 'SESSION_LIFETIME', 'SESSION_COOKIE_SECURE',
            'UI_THEME', 'LOCAL_LOGIN_NAMES',
        ];
        for ($n = 1; $n <= 3; $n++) {
            $keys[] = "OAUTH2_{$n}_ISSUER";
            $keys[] = "OAUTH2_{$n}_CLIENT_ID";
            $keys[] = "OAUTH2_{$n}_NAMES";
            $keys[] = "LDAP_SERVER_{$n}_URL";
            $keys[] = "LDAP_SERVER_{$n}_SEARCH_BASE";
            $keys[] = "LDAP_SERVER_{$n}_NAMES";
        }

        return $keys;
    }

    /** One process-environment variable, or null when it is unset or empty. */
    private static function environment(string $key): ?string
    {
        foreach ([$_SERVER[$key] ?? null, $_ENV[$key] ?? null, getenv($key)] as $candidate) {
            if (is_string($candidate) && $candidate !== '') {
                return $candidate;
            }
        }

        return null;
    }

    /**
     * Reads a KEY=VALUE .env file. A missing or unreadable file yields an empty
     * map — the file is optional (REQ-CFG-001). Blank lines and # comments are
     * skipped, one layer of quotes is stripped, and an `export ` prefix is
     * tolerated.
     *
     * @return array<string, string>
     */
    private static function readDotEnv(string $path): array
    {
        $values = [];
        if (!is_file($path) || !is_readable($path)) {
            return $values;
        }

        $handle = fopen($path, 'rb');
        if ($handle === false) {
            return $values;
        }
        while (($line = fgets($handle)) !== false) {
            $line = trim($line);
            if ($line === '' || str_starts_with($line, '#')) {
                continue;
            }
            if (str_starts_with($line, 'export ')) {
                $line = trim(substr($line, 7));
            }
            $eq = strpos($line, '=');
            if ($eq === false) {
                continue;
            }
            $key = trim(substr($line, 0, $eq));
            if ($key === '' || preg_match('/^[A-Z][A-Z0-9_]*$/', $key) !== 1) {
                continue;
            }
            $value = trim(substr($line, $eq + 1));
            if (strlen($value) >= 2
                && (($value[0] === '"' && str_ends_with($value, '"'))
                    || ($value[0] === "'" && str_ends_with($value, "'")))) {
                $value = substr($value, 1, -1);
            }
            $values[$key] = $value;
        }
        fclose($handle);

        return $values;
    }
}
