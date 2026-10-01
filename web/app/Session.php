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

declare(strict_types=1);

namespace Clara;

final class Session
{
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
        ini_set('session.sid_bits_per_character', '6');
        ini_set('session.sid_length', '48');

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
     * page guard rejects it (Authentication_Authorization_Design.md §2.7). This
     * pass has no second-factor flow yet, but the gate exists so that M1 cannot
     * land a hole by forgetting it.
     */
    public static function hasPendingSecondFactor(): bool
    {
        if (!isset($_SESSION['tfa_pending']) || !is_array($_SESSION['tfa_pending'])) {
            return false;
        }
        $verifiedAt = (int) ($_SESSION['tfa_pending']['verified_at'] ?? 0);

        // TTL 5 minutes; an expired pending state is dropped, not honoured.
        if ($verifiedAt + 300 < time()) {
            unset($_SESSION['tfa_pending']);

            return false;
        }

        return true;
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

    /** Promotes a verified second factor to a full session (§2.7). */
    public static function promoteSecondFactor(array $user): void
    {
        unset($_SESSION['tfa_pending']);
        self::establish($user, (string) ($_SESSION['auth_source_name'] ?? ''));
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
