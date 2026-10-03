<?php
// The PHP-owned session (GD-1): file storage under SESSION_DIR, never the
// application database (REQ-CFG-017), and never visible to the API (REQ-API-044).
//
// Key set is Authentication_Authorization_Design.md §3 — user_id, email,
// display_name, is_admin, auth_source, auth_source_name, issued_at, csrf_token —
// plus the acting user's own two display preferences, ui_language and ui_theme:
// §3.8 resolves the effective theme from "the session's user object", so those
// fields are expected here; they are presentation settings, not authorization
// inputs. Nothing permission-shaped and no project token is ever stored —
// permissions are re-derived from the API on every request (REQ-AUTH-033).
//
// The two underscore-prefixed keys are explicitly not part of the auth schema:
//   _flash        one-shot alert buffer for the redirect-back pattern (§3.4, §3.8)
//   _i18n_bundle  the translated overlay for the current language, invalidated
//                 on POST /lang (Plan/Web_Implementation.md §5 sanctions it)
// A third holds the in-flight OAuth2 round trip (`_oauth_txn`) — the `state` and
// PKCE verifier Sequence A needs between the two browser hops (§2.1).

declare(strict_types=1);

namespace Clara;

final class Session
{
    /** How long a first-factor success stays pending (§2.7: TTL 5 minutes). */
    public const PENDING_SECOND_FACTOR_SECONDS = 300;

    /** How long an OAuth2 round trip may stay unfinished before its state is dropped. */
    public const OAUTH_TRANSACTION_SECONDS = 600;

    private function __construct() {}

    /**
     * Configures and starts the session. Called once, from the front controller,
     * before anything reads or writes session state.
     */
    public static function start(Config $config): void
    {
        if (session_status() === PHP_SESSION_ACTIVE) {
            return;
        }

        // Storage: our own directory, mode 0700 (checked at boot, REQ-CFG-017).
        session_save_path($config->sessionDir);
        ini_set('session.gc_probability', '1');
        ini_set('session.gc_divisor', '100');
        // Garbage collection removes idle files; the authoritative expiry check
        // is hasTimedOut(), which does not depend on GC having run.
        ini_set('session.gc_maxlifetime', (string) $config->sessionLifetime);

        // Reject a session id the client invented rather than one we issued.
        ini_set('session.use_strict_mode', '1');
        ini_set('session.use_only_cookies', '1');
        // The sid stays at PHP's default — 32 hexadecimal characters, 128 bits of
        // entropy, which is the strength the deprecation RFC calls the right choice
        // for a secret. Changing session.sid_length or session.sid_bits_per_character
        // is deprecated in PHP 8.4, and with display_errors on (development) the
        // notice prints before the doctype and drops every page into quirks mode.

        session_name($config->sessionCookieName);
        session_set_cookie_params([
            // Browser-scoped cookie: the server-side timeout is authoritative,
            // and a persistent cookie would widen the fixation window.
            'lifetime' => 0,
            'path' => '/',
            'secure' => $config->sessionCookieSecure,
            'httponly' => true,
            'samesite' => 'Lax',
        ]);

        session_start();

        if (!isset($_SESSION['csrf_token'])) {
            $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
        }
    }

    /** True once the identity keys are present and no pre-auth state stands. */
    public static function isAuthenticated(): bool
    {
        return isset($_SESSION['user_id']) && !isset($_SESSION['tfa_pending']);
    }

    /**
     * A `tfa_pending` session is pre-authentication: not a session, and every
     * page guard rejects it (Authentication_Authorization_Design.md §2.7). What
     * *is* reachable while it stands is the second-factor panel and the
     * enrollment wizard — both owned by the `/login` route, which reads the
     * state through pendingSecondFactor() instead of treating it as signed in.
     */
    public static function hasPendingSecondFactor(): bool
    {
        return self::pendingSecondFactor() !== [];
    }

    /**
     * The pending first-factor state, or [] when none stands or its five minutes
     * have passed (an expired one is dropped rather than honoured, §2.7).
     *
     * @return array{email: string, source: string, provider: string, source_name: string,
     *               first_factor: string, user_id: int, method: string, verified_at: int}
     */
    public static function pendingSecondFactor(): array
    {
        $pending = $_SESSION['tfa_pending'] ?? null;
        if (!is_array($pending) || !isset($pending['verified_at'])) {
            return [];
        }

        // TTL 5 minutes, the lifetime §3 fixes for this state.
        if ((int) $pending['verified_at'] + self::PENDING_SECOND_FACTOR_SECONDS < time()) {
            unset($_SESSION['tfa_pending']);

            return [];
        }

        return [
            'email' => (string) ($pending['email'] ?? ''),
            'source' => (string) ($pending['source'] ?? ''),
            'provider' => (string) ($pending['provider'] ?? ''),
            'source_name' => (string) ($pending['source_name'] ?? ''),
            // The first-factor handle of REQ-API-131 — a 5-minute signature, not
            // a password; nothing here ever holds one (REQ-AUTH-036).
            'first_factor' => (string) ($pending['first_factor'] ?? ''),
            'user_id' => (int) ($pending['user_id'] ?? 0),
            'method' => (string) ($pending['method'] ?? ''),
            'verified_at' => (int) $pending['verified_at'],
        ];
    }

    /**
     * Records the first-factor success behind a second factor (§2.7). Entering
     * the state rotates the id — an attacker who planted this cookie before the
     * attempt must not end up holding the session the challenge completes into.
     * A resumed challenge does not rotate again: it is the same pending identity
     * re-asking, and its original `verified_at` is passed through so the five
     * minutes count from the first factor rather than from the last wrong code.
     *
     * @param array{email?: string, source?: string, provider?: string,
     *              source_name?: string, first_factor?: string, user_id?: int,
     *              method?: string, verified_at?: int} $pending
     */
    public static function beginSecondFactor(array $pending): void
    {
        $alreadyPending = isset($_SESSION['tfa_pending']) && is_array($_SESSION['tfa_pending']);

        if (!$alreadyPending && session_status() === PHP_SESSION_ACTIVE) {
            session_regenerate_id(true);
        }

        $_SESSION['tfa_pending'] = array_merge([
            'email' => '',
            'source' => '',
            'provider' => '',
            'source_name' => '',
            'first_factor' => '',
            'user_id' => 0,
            'method' => '',
            'verified_at' => time(),
        ], $pending);

        // Anything from an earlier attempt is gone: a stale flash or a half-finished
        // wizard would otherwise reappear inside someone else's challenge.
        unset($_SESSION['_flash']);
    }

    /** Replaces part of the pending state (e.g. the method after a resend). */
    public static function updatePendingSecondFactor(array $fields): void
    {
        if (!isset($_SESSION['tfa_pending']) || !is_array($_SESSION['tfa_pending'])) {
            return;
        }
        $_SESSION['tfa_pending'] = array_merge($_SESSION['tfa_pending'], $fields);
    }

    /** Aborts the challenge: no pending state survives a step back (§2.7). */
    public static function clearPendingSecondFactor(): void
    {
        unset($_SESSION['tfa_pending']);
    }

    /**
     * The in-flight OAuth2 round trip (`_oauth_txn`, not part of §3's auth schema):
     * the `state` and PKCE verifier Sequence A step 1 remembers, the provider it
     * belongs to, and the source name the user selected so the login that completes
     * can carry it (§2.9). Single-use by construction — read it with
     * takeOauthTransaction(), which removes it — because `state` is what stops a
     * forged callback being accepted twice (§2.1 step 3).
     *
     * @param array{provider_index: int, state: string, code_verifier: string, source_name: string} $txn
     */
    public static function beginOauthTransaction(array $txn): void
    {
        $_SESSION['_oauth_txn'] = $txn + ['started_at' => time()];
    }

    /** The pending round trip, or [] when none stands, was already used, or timed out. */
    public static function takeOauthTransaction(): array
    {
        $txn = $_SESSION['_oauth_txn'] ?? null;
        unset($_SESSION['_oauth_txn']);

        if (!is_array($txn) || !isset($txn['started_at'])) {
            return [];
        }

        // A round trip left unfinished is discarded rather than honoured later: the
        // browser may have come back to this tab hours afterwards.
        if ((int) $txn['started_at'] + self::OAUTH_TRANSACTION_SECONDS < time()) {
            return [];
        }

        return [
            'provider_index' => (int) ($txn['provider_index'] ?? 0),
            'state' => (string) ($txn['state'] ?? ''),
            'code_verifier' => (string) ($txn['code_verifier'] ?? ''),
            'source_name' => (string) ($txn['source_name'] ?? ''),
        ];
    }

    /**
     * Absolute session bound: the normative `issued_at` key plus SESSION_LIFETIME
     * (REQ-AUTH-015). Reading §3's key set literally, issued_at is the only
     * timestamp available, so this is an absolute rather than a sliding window —
     * strictly stronger, and it does not depend on PHP's session GC running.
     */
    public static function hasTimedOut(Config $config): bool
    {
        $issuedAt = (int) ($_SESSION['issued_at'] ?? 0);
        if ($issuedAt === 0) {
            return false;
        }

        return $issuedAt + $config->sessionLifetime < time();
    }

    /**
     * Writes the identity keys after a successful login and defends against
     * session fixation by rotating the id with the old file removed (§3).
     *
     * @param array<string, mixed> $user the user object the API returned
     */
    public static function establish(array $user, string $authSourceName): void
    {
        // Drop anything from a pre-auth or anonymous session first.
        $_SESSION = [];

        // The fixation defense: a new id, the old file removed (§3). It needs an
        // active session — which every request has by the time this runs, since
        // the front controller starts the session before routing; outside one (a
        // CLI test) the key rotation below is still correct.
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_regenerate_id(true);
        }

        $_SESSION['user_id'] = (int) ($user['id'] ?? 0);
        $_SESSION['email'] = (string) ($user['email'] ?? '');
        $_SESSION['display_name'] = (string) ($user['display_name'] ?? '');
        $_SESSION['is_admin'] = !empty($user['is_admin']) ? 1 : 0;
        $_SESSION['auth_source'] = (string) ($user['auth_source'] ?? '');
        $_SESSION['ui_language'] = (string) ($user['ui_language'] ?? 'en');
        $_SESSION['ui_theme'] = isset($user['ui_theme']) && is_string($user['ui_theme']) ? $user['ui_theme'] : null;
        $_SESSION['auth_source_name'] = $authSourceName;
        $_SESSION['issued_at'] = time();
        $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
    }

    /**
     * Promotes a verified second factor to a full session (§2.7). The source name
     * the user selected on the login page carries over from the pending state —
     * it is recorded in the login audit and nothing else (REQ-AUTH-067).
     *
     * @param array<string, mixed> $user the user object the API returned
     */
    public static function promoteSecondFactor(array $user, string $authSourceName = ''): void
    {
        unset($_SESSION['tfa_pending']);
        self::establish($user, $authSourceName !== ''
            ? $authSourceName
            : (string) ($_SESSION['auth_source_name'] ?? ''));
    }

    /** Destroys the session and its cookie (Sequence D step 4). */
    public static function destroy(): void
    {
        $_SESSION = [];
        if (ini_get('session.use_cookies')) {
            $params = session_get_cookie_params();
            setcookie(
                session_name(),
                '',
                time() - 42000,
                $params['path'],
                $params['domain'],
                $params['secure'],
                $params['httponly']
            );
        }
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_destroy();
        }
    }

    public static function userId(): int
    {
        return (int) ($_SESSION['user_id'] ?? 0);
    }

    public static function email(): string
    {
        return (string) ($_SESSION['email'] ?? '');
    }

    public static function displayName(): string
    {
        $name = (string) ($_SESSION['display_name'] ?? '');

        return $name !== '' ? $name : self::email();
    }

    public static function isAdmin(): bool
    {
        return (int) ($_SESSION['is_admin'] ?? 0) === 1;
    }

    public static function authSource(): string
    {
        return (string) ($_SESSION['auth_source'] ?? '');
    }

    /**
     * The authentication-source name in play. §3 lists `auth_source_name` as the
     * identity key login writes, and §2.9 stores the user's picker selection under the
     * same key before any credential exchange — so this reads both states, and a signed-in
     * user's session carries the name they logged in with.
     */
    public static function selectedSourceName(): string
    {
        return (string) ($_SESSION['auth_source_name'] ?? '');
    }

    /** Remembers the picker selection so the panels under it re-render for it (§2.2). */
    public static function selectSourceName(string $name): void
    {
        $_SESSION['auth_source_name'] = $name;
    }

    /** True when the account has a local credential — the Password link's gate (§2.4). */
    public static function hasLocalCredential(): bool
    {
        return self::authSource() === 'local';
    }

    public static function uiLanguage(): string
    {
        $language = (string) ($_SESSION['ui_language'] ?? '');

        return $language !== '' ? $language : 'en';
    }

    /** The personal theme override, or null to follow the installation default. */
    public static function uiThemeOverride(): ?string
    {
        $theme = $_SESSION['ui_theme'] ?? null;

        return is_string($theme) && $theme !== '' ? $theme : null;
    }

    public static function setUiLanguage(string $code): void
    {
        $_SESSION['ui_language'] = $code;
        unset($_SESSION['_i18n_bundle']);
    }

    public static function setUiThemeOverride(?string $theme): void
    {
        $_SESSION['ui_theme'] = $theme;
    }

    /** @return array{language: string, strings: array<string,string>}|null */
    public static function cachedBundle(): ?array
    {
        $bundle = $_SESSION['_i18n_bundle'] ?? null;

        return is_array($bundle) ? $bundle : null;
    }

    public static function storeBundle(string $language, array $strings): void
    {
        $_SESSION['_i18n_bundle'] = ['language' => $language, 'strings' => $strings];
    }

    /**
     * Holds a just-issued project token for exactly one render (§3.5, REQ-UI-013): the
     * add/rotate POST redirects, and the page it lands on shows the value once. The value
     * lives only in this session entry until that render takes it.
     *
     * @param array{token: string, email: string} $reveal
     */
    public static function stashReveal(array $reveal): void
    {
        $_SESSION['_reveal'] = $reveal;
    }

    /** @return array{token: string, email: string}|null */
    public static function takeReveal(): ?array
    {
        $reveal = $_SESSION['_reveal'] ?? null;
        unset($_SESSION['_reveal']);

        return is_array($reveal) && isset($reveal['token']) ? $reveal : null;
    }

    /**
     * Follows an administrator-flag change on the signed-in account itself (§5.1): after a
     * self-revoke the cached flag (REQ-AUTH-009) would be stale while every API call
     * already refuses.
     */
    public static function setAdmin(bool $isAdmin): void
    {
        $_SESSION['is_admin'] = $isAdmin ? 1 : 0;
    }

    /** Queues one translated line for the next rendered page. */
    public static function flash(string $level, string $text): void
    {
        $_SESSION['_flash'][] = ['level' => $level, 'text' => $text];
    }

    /**
     * Drains the flash buffer. Called by the view layer exactly once per page,
     * so a message shows on one render and never again.
     *
     * @return list<array{level: string, text: string}>
     */
    public static function takeFlash(): array
    {
        $flash = $_SESSION['_flash'] ?? [];
        unset($_SESSION['_flash']);

        return is_array($flash) ? $flash : [];
    }
}
