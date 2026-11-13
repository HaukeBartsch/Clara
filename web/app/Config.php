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

    /**
     * Color scheme each installed theme compiles for (REQ-UI-040). A dark Bootswatch
     * theme carries its component palette — subtle backgrounds, emphasis, form controls —
     * under [data-bs-theme=dark] selectors while its :root keeps the light values, so the
     * <html> element must declare the scheme or those variables never flip.
     */
    public const THEME_COLOR_SCHEMES = ['bootstrap' => 'light', 'darkly' => 'dark', 'yeti' => 'light'];

    /** How many indexed OAuth2 providers / LDAP servers the variable set allows (`OAUTH2_1…3`). */
    private const SOURCE_INDEX_MAX = 3;

    /** Same floor the Go API refuses to start below — REQ-CFG-004 keeps both sides identical (finding F8). */
    private const MIN_INTERNAL_SERVICE_TOKEN_BYTES = 32;

    /**
     * The ceiling for MONITORING_PERIOD_MINUTES (REQ-CFG-034): a day of once-a-second samples is as
     * much history as a browser tab redraws usefully, so anything above it reads as a typo.
     */
    public const MAX_MONITORING_PERIOD_MINUTES = 1440;

    /**
     * The signing algorithms `OAUTH2_N_ID_TOKEN_ALG` may name (REQ-AUTH-077, REQ-CFG-035) —
     * the asymmetric set only, keyed by upper-cased spelling and valued by the canonical name
     * verification pins. `none` and the HMAC family are absent because they must not be
     * configurable even by mistake: identity providers advertise both in their metadata, and an
     * algorithm whose key the asserting side could also obtain is not a verification. The same
     * set `config.asymmetricAlgs` fixes on the API side, so one `.env` cannot satisfy one
     * component and refuse the other.
     */
    public const ID_TOKEN_ALGORITHMS = [
        'RS256' => 'RS256', 'RS384' => 'RS384', 'RS512' => 'RS512',
        'PS256' => 'PS256', 'PS384' => 'PS384', 'PS512' => 'PS512',
        'ES256' => 'ES256', 'ES384' => 'ES384', 'ES512' => 'ES512',
        'EDDSA' => 'EdDSA',
    ];

    /** `OAUTH2_JWKS_TTL` default (REQ-CFG-035), in seconds. */
    public const DEFAULT_JWKS_TTL_SECONDS = 3600;

    /**
     * Where a provider posts its back-channel logout token (REQ-CFG-036): one segment under
     * this prefix per provider, so the reverse proxy needs a single location rule for all of
     * them (INSTALL.md §11).
     */
    public const BACKCHANNEL_LOGOUT_PREFIX = '/auth/backchannel-logout/';

    /** OAUTH2_N_RESPONSE_MODE values (REQ-CFG-041); `query` is the default. */
    public const RESPONSE_MODE_QUERY = 'query';
    public const RESPONSE_MODE_FORM_POST = 'form_post';

    /** CLARA's page the provider returns the browser to after Sequence L1 (REQ-AUTH-097). */
    public const LOGGED_OUT_PATH = '/auth/logged-out';

    public string $appEnv = 'development';

    public bool $isDevelopment = true;

    /** Base URL of the Go API as PHP reaches it, derived from API_ADDR. */
    public string $apiBaseUrl = '';

    public string $internalServiceToken = '';

    public string $webPublicUrl = '';

    public string $logLevelWeb = 'info';

    public string $sessionDir = '';

    /**
     * The cookie name on the wire: SESSION_COOKIE_NAME, carrying the `__Host-` prefix
     * whenever the cookie is Secure (REQ-AUTH-075) — the browser then refuses it unless it
     * is Secure, host-only and on path `/`, so neither a sibling subdomain nor a plain-HTTP
     * page can plant or overwrite it.
     */
    public string $sessionCookieName = 'csms_session';

    /** Absolute bound from sign-in (REQ-AUTH-015): SESSION_LIFETIME, default 8 h. */
    public int $sessionLifetime = 28800;

    /** Inactivity bound (REQ-AUTH-015): SESSION_IDLE_TIMEOUT, default 30 min; 0 = off. */
    public int $sessionIdleTimeout = 1800;

    /** Session id rotation (REQ-AUTH-072): SESSION_ROTATE_INTERVAL, default 15 min; 0 = off. */
    public int $sessionRotateInterval = 900;

    public bool $sessionCookieSecure = false;

    /** Installation default theme (REQ-CFG-031); a user may override it. */
    public string $uiTheme = 'bootstrap';

    /**
     * How far back the Monitoring charts reach (REQ-CFG-034): the browser keeps one period of
     * samples and drops every timepoint older than it, so an open page holds a bounded buffer
     * instead of everything since it was opened. Consumed by the web layer's Monitoring section,
     * which carries it to `monitoring.js` as a data attribute (REQ-UI-064).
     */
    public int $monitoringPeriodMinutes = 10;

    /**
     * Authentication sources as configured: one entry per (name, source) pair —
     * the relation is many-to-many (REQ-CFG-032, REQ-AUTH-064). An empty list
     * means no names are configured anywhere, so all sources form one implicit
     * default set and the login picker is skipped (REQ-AUTH-067).
     *
     * @var list<array{kind: string, name: string}>
     */
    public array $authSources = [];

    /**
     * The OAuth2 providers configured for this installation (REQ-CFG-011). The Go API
     * owns discovery, authorization-code exchange and assertion verification. PHP keeps
     * only the provider metadata needed to route the callback and render the Control
     * Panel; it does not load or retain the client secret.
     *
     * @var list<array{index: int, issuer: string, client_id: string, redirect_uri: string, email_attr: string, roles_attr: string, min_acr: list<string>, names: list<string>, post_logout_redirect_uri: string, backchannel_logout_path: string, response_mode: string}>
     */
    public array $oauthProviders = [];

    /**
     * Sign-out coordinated with the OAuth2 providers (`OIDC_SINGLE_SIGN_OUT`, REQ-CFG-036,
     * Sequence L). Both components read the same switch: the API builds the provider sign-out
     * address and verifies logout tokens, this layer sends the browser and receives the
     * provider's back-channel request. Off, neither route exists.
     */
    public bool $oidcSingleSignOut = false;

    /**
     * How long a fetched provider key set may be reused before a scheduled refetch
     * (`OAUTH2_JWKS_TTL`, REQ-CFG-035). The API enforces it; the web layer reads it only to
     * report the gate it configures, so an administrator sees one number for both components.
     */
    public int $oauthJwksTtl = self::DEFAULT_JWKS_TTL_SECONDS;

    /**
     * The LDAP servers configured for this installation (REQ-CFG-012). PHP binds
     * the directory itself (Sequence B), so it holds the search credentials.
     *
     * @var list<array{index: int, url: string, bind_dn: string, bind_password: string, search_base: string, uid_attr: string, email_attr: string, name_attr: string, names: list<string>}>
     */
    public array $ldapServers = [];

    /**
     * Names the one local source answers to (REQ-CFG-032). Empty means it joins
     * whichever implicit default set applies (REQ-AUTH-067); the local kind exists
     * at most once because there is one `users` table (GD-18).
     *
     * @var list<string>
     */
    public array $localNames = [];

    /**
     * The applications this installation signs users in to (Sequence K, REQ-CFG-038): id, the
     * name shown to the user, and the two addresses a browser may be sent back to. The client
     * secret is not here — only the API checks it (REQ-API-183) — and the API refuses to start
     * with a client it could not validate, so these entries are display and routing data.
     *
     * @var list<array{index: int, id: string, name: string, redirect_uri: string, logout_uri: string}>
     */
    public array $ssoClients = [];

    /**
     * The peers whose forwarded `X-Real-IP` may name the caller (TRUSTED_PROXY_CIDRS) — the same
     * ranges the API trusts its own direct peer by, so one variable decides the question for both
     * components (REQ-API-125, System_Configuration_Design.md §3.9). The web layer needs it because
     * it forwards the address itself: believing the header from anyone would let a browser choose
     * the address the API's limiter keys on (finding F9).
     */
    public TrustedProxies $trustedProxies;

    private function __construct()
    {
        // A safe value before fromValues fills it: no peer may name another caller, so a
        // configuration that never reached this line measures everyone by their connection address.
        $this->trustedProxies = TrustedProxies::none();
    }

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
        } elseif (!$c->isDevelopment && strlen($c->internalServiceToken) < self::MIN_INTERNAL_SERVICE_TOKEN_BYTES) {
            // This secret acts as any user, so a short one is guessable; the Go API
            // refuses to start below the same length and this layer reports it the way it
            // reports every other variable (master-spec finding F8). Development keeps its
            // short fixed token, as it keeps `dev-internal-token`.
            $errors[] = sprintf(
                'INTERNAL_SERVICE_TOKEN must be at least %d bytes — openssl rand -hex 32 issues one (REQ-CFG-013)',
                self::MIN_INTERNAL_SERVICE_TOKEN_BYTES
            );
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

        // Inactivity and rotation (REQ-AUTH-015/072): 0 turns either off. A rotation
        // interval must outlast the grace window twice over, or the requests a page fires
        // together would still be following one rotation when the next one starts.
        $idle = $get('SESSION_IDLE_TIMEOUT', '1800');
        if (preg_match('/^\d+$/', $idle) !== 1 || ((int) $idle !== 0 && (int) $idle < 60)) {
            $errors[] = "SESSION_IDLE_TIMEOUT must be 0 (off) or an integer number of seconds ≥ 60, got '{$idle}'";
            $c->sessionIdleTimeout = 1800;
        } else {
            $c->sessionIdleTimeout = (int) $idle;
        }

        $rotate = $get('SESSION_ROTATE_INTERVAL', '900');
        $minimum = 2 * Session::ROTATION_GRACE_SECONDS;
        if (preg_match('/^\d+$/', $rotate) !== 1 || ((int) $rotate !== 0 && (int) $rotate < $minimum)) {
            $errors[] = "SESSION_ROTATE_INTERVAL must be 0 (off) or an integer number of seconds ≥ {$minimum}, got '{$rotate}'";
            $c->sessionRotateInterval = 900;
        } else {
            $c->sessionRotateInterval = (int) $rotate;
        }

        $secure = $get('SESSION_COOKIE_SECURE', $c->isDevelopment ? '0' : '1');
        if (!in_array($secure, ['0', '1'], true)) {
            $errors[] = "SESSION_COOKIE_SECURE must be 0 or 1, got '{$secure}'";
            $secure = $c->isDevelopment ? '0' : '1';
        } elseif ($secure === '0' && !$c->isDevelopment) {
            // A session cookie that may travel over plain HTTP is a session anyone on the
            // path can take; production refuses to start rather than run that way (REQ-AUTH-075).
            $errors[] = 'SESSION_COOKIE_SECURE=0 is refused when APP_ENV=production — serve the site over HTTPS (REQ-AUTH-075)';
            $secure = '1';
        }
        $c->sessionCookieSecure = $secure === '1';
        if ($c->sessionCookieSecure) {
            $c->sessionCookieName = '__Host-' . $c->sessionCookieName;
        }

        // --- caller address (§3.9, REQ-API-125) ---
        // Read from the API's variable, with the API's default and its tolerance of empty entries,
        // so a single .env says which proxy may name a caller for both components. An unset or empty
        // value keeps the documented default rather than trusting nobody by accident; a malformed
        // entry refuses startup (REQ-CFG-005).
        $proxies = $get('TRUSTED_PROXY_CIDRS');
        $c->trustedProxies = TrustedProxies::parse($proxies === '' ? TrustedProxies::DEFAULT_RAW : $proxies, $errors);

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

        // --- monitoring charts (REQ-CFG-034) ---
        // The Monitoring page samples once a second while it is visible, so this period is also how
        // much history the browser tab carries: one sample per poll up to a day's worth. A value
        // outside the range is a mistake rather than a preference, and refuses startup like every
        // other variable (REQ-CFG-005).
        $period = $get('MONITORING_PERIOD_MINUTES', '10');
        if (preg_match('/^\d+$/', $period) !== 1 || (int) $period < 1 || (int) $period > self::MAX_MONITORING_PERIOD_MINUTES) {
            $errors[] = sprintf(
                'MONITORING_PERIOD_MINUTES must be an integer number of minutes between 1 and %d, got \'%s\'',
                self::MAX_MONITORING_PERIOD_MINUTES,
                $period
            );
        } else {
            $c->monitoringPeriodMinutes = (int) $period;
        }

        // --- provider assertion gate (REQ-CFG-035) ---
        // OAUTH2_JWKS_TTL bounds how long the API reuses a fetched provider key set. It is read
        // here so the web layer can report the number it shares with the API; the API refuses the
        // same bad values at its own startup, and `0` — which reads as "no cache" and would mean
        // "never refetch" — is refused rather than defaulted (REQ-AUTH-077).
        $jwksTtl = $get('OAUTH2_JWKS_TTL');
        if ($jwksTtl !== '') {
            if (preg_match('/^\d+$/', $jwksTtl) !== 1 || (int) $jwksTtl < 1) {
                $errors[] = "OAUTH2_JWKS_TTL: '{$jwksTtl}' is not a positive number of seconds (REQ-CFG-035)";
            } else {
                $c->oauthJwksTtl = (int) $jwksTtl;
            }
        }

        // --- single sign-out with the provider (REQ-CFG-036) ---
        // 0 or 1 and nothing else: a sign-out that silently does not reach the provider is the
        // failure this switch exists to prevent, so an ambiguous value refuses startup.
        $sso = $get('OIDC_SINGLE_SIGN_OUT', '0');
        if (!in_array($sso, ['0', '1'], true)) {
            $errors[] = "OIDC_SINGLE_SIGN_OUT must be 0 or 1, got '{$sso}' (REQ-CFG-036)";
        }
        $c->oidcSingleSignOut = $sso === '1';

        // --- authentication sources (REQ-CFG-032) ---
        $c->authSources = self::readAuthSources($c, $get, $errors);
        self::checkBackchannelPaths($c, $errors);

        // A configured directory is only usable if this PHP can bind one. Naming the
        // missing extension at startup beats a login page that can never verify anybody
        // (REQ-CFG-005), and mirrors how CurlTransport reports a missing cURL.
        if ($c->ldapServers !== [] && !function_exists('ldap_connect')) {
            $errors[] = 'An LDAP server is configured but the PHP LDAP extension is not loaded — install it'
                . ' (Debian/Ubuntu: php' . PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION . '-ldap).';
        }

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
     * (REQ-CFG-032), and validates the PHP-side metadata it needs. The Go API validates
     * OAuth2 client credentials; PHP performs LDAP binds and therefore checks the
     * directory search base here.
     *
     * @param callable(string, string=): string $get
     * @param list<string>                      $errors collected problems
     * @return list<array{kind: string, name: string}>
     */
    private static function readAuthSources(self $c, callable $get, array &$errors): array
    {
        for ($n = 1; $n <= self::SOURCE_INDEX_MAX; $n++) {
            $issuer = rtrim($get("OAUTH2_{$n}_ISSUER"), '/');
            if ($issuer !== '') {
                if ($get("OAUTH2_{$n}_CLIENT_ID") === '') {
                    $errors[] = "OAUTH2_{$n}_CLIENT_ID is required once "
                        . "OAUTH2_{$n}_ISSUER is set (REQ-CFG-011)";
                }
                if (preg_match('#^https?://[A-Za-z0-9._\-]+#i', $issuer) !== 1) {
                    $errors[] = "OAUTH2_{$n}_ISSUER must be an absolute http(s) URL, got a value that is not one (REQ-CFG-011)";
                }

                $c->oauthProviders[] = [
                    'index' => $n,
                    'issuer' => $issuer,
                    'client_id' => $get("OAUTH2_{$n}_CLIENT_ID"),
                    // Mirrored for the Control Panel; the Go API applies the documented
                    // WEB_PUBLIC_URL + /auth/callback default (REQ-CFG-011).
                    'redirect_uri' => $get("OAUTH2_{$n}_REDIRECT_URI"),
                    'email_attr' => $get("OAUTH2_{$n}_EMAIL_ATTR", 'email'),
                    // Display only: the Go API reads and validates the claim (REQ-CFG-037).
                    'roles_attr' => trim($get("OAUTH2_{$n}_ROLES_ATTR")),
                    // Display only, for an administrator's two-factor page (REQ-UI-085): the
                    // Go API applies the assurance gate (REQ-AUTH-081). Split and trimmed the
                    // way the API reads it, so the page names the values the API enforces.
                    'min_acr' => array_values(array_filter(
                        array_map('trim', explode(',', $get("OAUTH2_{$n}_MIN_ACR"))),
                        static fn (string $v): bool => $v !== ''
                    )),
                    'names' => self::nameList("OAUTH2_{$n}_NAMES", $get("OAUTH2_{$n}_NAMES"), $errors),
                    // Display only, for the Authentication Test's list of addresses to register
                    // at the provider: the Go API validates and sends it (REQ-CFG-036).
                    'post_logout_redirect_uri' => $get("OAUTH2_{$n}_POST_LOGOUT_REDIRECT_URI"),
                    // This layer's half of single sign-out: the path the provider posts its
                    // logout token to (Sequence L2). Checked by checkBackchannelPaths().
                    'backchannel_logout_path' => $get("OAUTH2_{$n}_BACKCHANNEL_LOGOUT_PATH"),
                    // How the provider returns the authorization response (REQ-CFG-041). The
                    // API asks for it; this layer opens POST /auth/callback when any provider
                    // uses form_post, and keeps GET /auth/callback open whatever is set.
                    'response_mode' => self::responseMode($n, $get("OAUTH2_{$n}_RESPONSE_MODE"), $errors),
                ];
            }

            $url = $get("LDAP_SERVER_{$n}_URL");
            if ($url !== '') {
                if ($get("LDAP_SERVER_{$n}_SEARCH_BASE") === '') {
                    $errors[] = "LDAP_SERVER_{$n}_SEARCH_BASE is required once LDAP_SERVER_{$n}_URL is set (REQ-CFG-012)";
                }
                if (preg_match('#^ldaps?://[A-Za-z0-9._\-]+#i', $url) !== 1) {
                    $errors[] = "LDAP_SERVER_{$n}_URL must be an ldap:// or ldaps:// URL, got a value that is not one (REQ-CFG-012)";
                }

                $c->ldapServers[] = [
                    'index' => $n,
                    'url' => $url,
                    // Empty search credentials mean an anonymous directory search
                    // (REQ-CFG-012, ASM-AUTH-2); the user's own bind follows.
                    'bind_dn' => $get("LDAP_SERVER_{$n}_BIND_DN"),
                    'bind_password' => $get("LDAP_SERVER_{$n}_BIND_PASSWORD"),
                    'search_base' => $get("LDAP_SERVER_{$n}_SEARCH_BASE"),
                    'uid_attr' => $get("LDAP_SERVER_{$n}_UID_ATTR", 'uid'),
                    'email_attr' => $get("LDAP_SERVER_{$n}_EMAIL_ATTR", 'mail'),
                    'name_attr' => $get("LDAP_SERVER_{$n}_NAME_ATTR", 'cn'),
                    'names' => self::nameList("LDAP_SERVER_{$n}_NAMES", $get("LDAP_SERVER_{$n}_NAMES"), $errors),
                ];
            }
        }

        // Registered sign-in clients (Sequence K, REQ-CFG-038). The API validates them in full
        // and refuses to start otherwise; the web layer only needs where a browser may be sent.
        for ($n = 1; $n <= self::SOURCE_INDEX_MAX; $n++) {
            $id = trim($get("SSO_CLIENT_{$n}_ID"));
            if ($id === '') {
                continue;
            }
            $redirect = trim($get("SSO_CLIENT_{$n}_REDIRECT_URI"));
            $logout = trim($get("SSO_CLIENT_{$n}_LOGOUT_URI"));
            if (!self::isAbsoluteHttpUrl($redirect)) {
                $errors[] = "SSO_CLIENT_{$n}_REDIRECT_URI must be an absolute http(s) URL (REQ-CFG-038)";
            }
            if ($logout !== '' && !self::isAbsoluteHttpUrl($logout)) {
                $errors[] = "SSO_CLIENT_{$n}_LOGOUT_URI must be an absolute http(s) URL or unset (REQ-CFG-038)";
            }
            $name = trim($get("SSO_CLIENT_{$n}_NAME"));
            $c->ssoClients[] = [
                'index' => $n,
                'id' => $id,
                'name' => $name !== '' ? $name : $id,
                'redirect_uri' => $redirect,
                'logout_uri' => $logout,
            ];
        }

        // The local source exists exactly once (one users table, GD-18) but may
        // carry several names; unset means it joins the implicit default set.
        $c->localNames = self::nameList('LOCAL_LOGIN_NAMES', $get('LOCAL_LOGIN_NAMES'), $errors);

        // The pairs the login page selects between: one per (source, name), with an
        // empty name list contributing no pair — which is how a source ends up in the
        // implicit default set instead (REQ-AUTH-067).
        $sources = [];
        foreach ($c->localNames as $name) {
            $sources[] = ['kind' => 'local', 'name' => $name];
        }
        foreach ($c->oauthProviders as $provider) {
            foreach ($provider['names'] as $name) {
                $sources[] = ['kind' => 'oauth2', 'name' => $name];
            }
        }
        foreach ($c->ldapServers as $server) {
            foreach ($server['names'] as $name) {
                $sources[] = ['kind' => 'ldap', 'name' => $name];
            }
        }

        return $sources;
    }

    /**
     * The web layer's half of the single sign-out check (REQ-CFG-036, System_Configuration_Design.md
     * §4.1): a back-channel path, when set, is the prefix plus one segment of letters, digits, `-`
     * or `_`, and no two providers share one — the path is what selects the provider. With the
     * switch on, every provider needs one. The API checks the other half (post-logout address,
     * subject claim) at its own startup, so a half-configured installation starts neither.
     *
     * @param list<string> $errors
     */
    private static function checkBackchannelPaths(self $c, array &$errors): void
    {
        $seen = [];
        foreach ($c->oauthProviders as $provider) {
            $n = $provider['index'];
            $path = $provider['backchannel_logout_path'];
            if ($path === '') {
                if ($c->oidcSingleSignOut) {
                    $errors[] = "OAUTH2_{$n}_BACKCHANNEL_LOGOUT_PATH is required with OIDC_SINGLE_SIGN_OUT=1 — e.g. "
                        . self::BACKCHANNEL_LOGOUT_PREFIX . "idp{$n} (REQ-CFG-036)";
                }
                continue;
            }
            if (preg_match('#^' . preg_quote(self::BACKCHANNEL_LOGOUT_PREFIX, '#') . '[A-Za-z0-9_-]{1,64}$#', $path) !== 1) {
                $errors[] = "OAUTH2_{$n}_BACKCHANNEL_LOGOUT_PATH must be " . self::BACKCHANNEL_LOGOUT_PREFIX
                    . "<segment> with one segment of letters, digits, - or _, got '{$path}' (REQ-CFG-036)";
                continue;
            }
            if (isset($seen[$path])) {
                $errors[] = "OAUTH2_{$n}_BACKCHANNEL_LOGOUT_PATH '{$path}' is already OAUTH2_{$seen[$path]}_BACKCHANNEL_LOGOUT_PATH"
                    . ' — each provider needs its own (REQ-CFG-036)';
                continue;
            }
            $seen[$path] = $n;
        }
    }

    /**
     * OAUTH2_N_RESPONSE_MODE (REQ-CFG-041): `query` (the default) or `form_post`. Anything else
     * refuses startup, as the API does — a guessed mode would send the response where the
     * callback does not take it.
     *
     * @param list<string> $errors
     */
    private static function responseMode(int $n, string $value, array &$errors): string
    {
        $value = trim($value);
        if ($value === '' || $value === self::RESPONSE_MODE_QUERY) {
            return self::RESPONSE_MODE_QUERY;
        }
        if ($value === self::RESPONSE_MODE_FORM_POST) {
            return self::RESPONSE_MODE_FORM_POST;
        }
        $errors[] = "OAUTH2_{$n}_RESPONSE_MODE must be query or form_post, got '{$value}' (REQ-CFG-041)";

        return self::RESPONSE_MODE_QUERY;
    }

    /**
     * Whether POST /auth/callback is open: only when some provider is configured to answer
     * with form_post (REQ-CFG-041). GET /auth/callback is always open.
     */
    public function acceptsFormPostCallback(): bool
    {
        foreach ($this->oauthProviders as $provider) {
            if ($provider['response_mode'] === self::RESPONSE_MODE_FORM_POST) {
                return true;
            }
        }

        return false;
    }

    /**
     * The provider whose back-channel logout path this is (Sequence L2), or null — with
     * single sign-out off, no path names a provider.
     *
     * @return array{index: int, issuer: string, backchannel_logout_path: string}|null
     */
    public function providerByBackchannelPath(string $path): ?array
    {
        if (!$this->oidcSingleSignOut || $path === '') {
            return null;
        }
        foreach ($this->oauthProviders as $provider) {
            if ($provider['backchannel_logout_path'] !== '' && hash_equals($provider['backchannel_logout_path'], $path)) {
                return $provider;
            }
        }

        return null;
    }

    /** An absolute http(s) URL with a host, and neither a fragment nor credentials in it. */
    private static function isAbsoluteHttpUrl(string $url): bool
    {
        $parts = parse_url($url);

        return is_array($parts)
            && in_array(strtolower((string) ($parts['scheme'] ?? '')), ['http', 'https'], true)
            && (string) ($parts['host'] ?? '') !== ''
            && !isset($parts['fragment'])
            && !isset($parts['user']);
    }

    /**
     * The registered sign-in client with this id (Sequence K), or null.
     *
     * @return array{index: int, id: string, name: string, redirect_uri: string, logout_uri: string}|null
     */
    public function ssoClient(string $id): ?array
    {
        if ($id === '') {
            return null;
        }
        foreach ($this->ssoClients as $client) {
            if (hash_equals($client['id'], $id)) {
                return $client;
            }
        }

        return null;
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
            'SESSION_DIR', 'SESSION_COOKIE_NAME', 'SESSION_LIFETIME', 'SESSION_IDLE_TIMEOUT',
            'SESSION_ROTATE_INTERVAL', 'SESSION_COOKIE_SECURE', 'UI_THEME', 'MONITORING_PERIOD_MINUTES',
            'LOCAL_LOGIN_NAMES',
            'TRUSTED_PROXY_CIDRS',
            'OIDC_SINGLE_SIGN_OUT',
        ];
        for ($n = 1; $n <= self::SOURCE_INDEX_MAX; $n++) {
            $keys[] = "OAUTH2_{$n}_ISSUER";
            $keys[] = "OAUTH2_{$n}_CLIENT_ID";
            $keys[] = "OAUTH2_{$n}_CLIENT_SECRET";
            $keys[] = "OAUTH2_{$n}_REDIRECT_URI";
            $keys[] = "OAUTH2_{$n}_EMAIL_ATTR";
            $keys[] = "OAUTH2_{$n}_ROLES_ATTR";
            $keys[] = "OAUTH2_{$n}_MIN_ACR";
            $keys[] = "OAUTH2_{$n}_NAMES";
            $keys[] = "OAUTH2_{$n}_POST_LOGOUT_REDIRECT_URI";
            $keys[] = "OAUTH2_{$n}_BACKCHANNEL_LOGOUT_PATH";
            $keys[] = "OAUTH2_{$n}_RESPONSE_MODE";
            $keys[] = "LDAP_SERVER_{$n}_URL";
            $keys[] = "LDAP_SERVER_{$n}_BIND_DN";
            $keys[] = "LDAP_SERVER_{$n}_BIND_PASSWORD";
            $keys[] = "LDAP_SERVER_{$n}_SEARCH_BASE";
            $keys[] = "LDAP_SERVER_{$n}_UID_ATTR";
            $keys[] = "LDAP_SERVER_{$n}_EMAIL_ATTR";
            $keys[] = "LDAP_SERVER_{$n}_NAME_ATTR";
            $keys[] = "LDAP_SERVER_{$n}_NAMES";
            $keys[] = "SSO_CLIENT_{$n}_ID";
            $keys[] = "SSO_CLIENT_{$n}_NAME";
            $keys[] = "SSO_CLIENT_{$n}_REDIRECT_URI";
            $keys[] = "SSO_CLIENT_{$n}_LOGOUT_URI";
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
