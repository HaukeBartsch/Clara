<?php
// The PHP-owned session (GD-1): file storage under SESSION_DIR, never the
// application database (REQ-CFG-017), and never visible to the API (REQ-API-044).
//
// Key set is Authentication_Authorization_Design.md §3 — user_id, email,
// display_name, is_admin, auth_source, auth_source_name, issued_at, csrf_token,
// and the lifecycle keys last_seen_at, rotated_at and session_epoch (§3.1) —
// plus the acting user's own two display preferences, ui_language and ui_theme:
// §3.8 resolves the effective theme from "the session's user object", so those
// fields are expected here; they are presentation settings, not authorization
// inputs. Nothing permission-shaped is ever stored — permissions are re-derived
// from the API on every request (REQ-AUTH-033). The one sanctioned exception for
// a credential is the member's own project token for UI data entry, held as a
// cache under `proj_token_{id}` and discarded on a data-API 401
// (User_Interface_Design.md §8.6/§10, Plan/Web_Implementation.md §5): it is never
// an authorization input, and it dies with the session.
//
// The two underscore-prefixed keys are explicitly not part of the auth schema:
//   _flash        one-shot alert buffer for the redirect-back pattern (§3.4, §3.8)
//   _i18n_bundle  the translated overlay for the current language, invalidated
//                 on POST /lang (Plan/Web_Implementation.md §5 sanctions it)
// A third holds the in-flight OAuth2 round trip (`_oauth_txn`) — the `state` and
// PKCE verifier Sequence A needs between the two browser hops (§2.1).
//
// The lifecycle (§3.1, REQ-AUTH-015/072/073/074) runs once per request, before the
// guard: an idle or over-age session ends; every SESSION_ROTATE_INTERVAL the session
// moves to a new id, and the old id is left holding nothing but a `tombstone` that
// points at its successor. For ROTATION_GRACE_SECONDS the tombstone leads there — the
// requests a page fires together were sent with the old cookie — and after that the
// old id coming back means two parties held this session: it ends, and the API is told
// to end every other session of the account. The session epoch the API handed out at
// login rides on every administration call; a raised one ends the session there.

declare(strict_types=1);

namespace Clara;

final class Session
{
    /** How long a first-factor success stays pending (§2.7: TTL 5 minutes). */
    public const PENDING_SECOND_FACTOR_SECONDS = 300;

    /** How long an OAuth2 round trip may stay unfinished before its state is dropped. */
    public const OAUTH_TRANSACTION_SECONDS = 600;

    /**
     * How long a rotated-away id still leads to its successor (REQ-AUTH-072): long enough
     * for the requests already in flight when the new cookie was issued, short enough that
     * a copied cookie is caught at the next rotation. SESSION_ROTATE_INTERVAL must be at
     * least twice this.
     */
    public const ROTATION_GRACE_SECONDS = 60;

    // What lifecycle() finds for this request's session (§3.1).
    /** Nothing to do: a live session, or none signed in. */
    public const LIVE = 'live';
    /** Signed in, and the id is due for replacement (REQ-AUTH-072). */
    public const ROTATE = 'rotate';
    /** A rotated-away id inside its grace window: continue in the successor. */
    public const FOLLOW = 'follow';
    /** A rotated-away id after its grace window: someone holds a copy (REQ-AUTH-073). */
    public const REPLAYED = 'replayed';
    /** Past SESSION_LIFETIME from sign-in, or opened before session epochs existed. */
    public const EXPIRED = 'expired';
    /** Past SESSION_IDLE_TIMEOUT without a request (REQ-AUTH-015). */
    public const IDLE = 'idle';

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

        // A tombstone stays exactly what rotation left (§3.1): lifecycle() reads it next,
        // and nothing is ever rendered from it.
        if (!isset($_SESSION['csrf_token']) && !isset($_SESSION['tombstone'])) {
            $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
        }
    }

    /**
     * Reads what this request's session is (§3.1) without changing anything — the whole
     * decision, kept apart from the id swaps so it can be tested on its own.
     */
    public static function verdict(Config $config, int $now): string
    {
        $tombstone = $_SESSION['tombstone'] ?? null;
        if (is_array($tombstone)) {
            return (int) ($tombstone['at'] ?? 0) + self::ROTATION_GRACE_SECONDS >= $now
                ? self::FOLLOW
                : self::REPLAYED;
        }

        // Pre-authentication states keep their own clocks (§2.7, the OAuth2 round trip).
        if (!self::isAuthenticated()) {
            return self::LIVE;
        }

        // A session from before epochs existed cannot be revoked, so it is not kept.
        if (!isset($_SESSION['session_epoch'])) {
            return self::EXPIRED;
        }

        $issuedAt = (int) ($_SESSION['issued_at'] ?? 0);
        if ($issuedAt + $config->sessionLifetime < $now) {
            return self::EXPIRED;
        }

        $lastSeen = (int) ($_SESSION['last_seen_at'] ?? $issuedAt);
        if ($config->sessionIdleTimeout > 0 && $lastSeen + $config->sessionIdleTimeout < $now) {
            return self::IDLE;
        }

        $rotatedAt = (int) ($_SESSION['rotated_at'] ?? $issuedAt);
        if ($config->sessionRotateInterval > 0 && $rotatedAt + $config->sessionRotateInterval <= $now) {
            return self::ROTATE;
        }

        return self::LIVE;
    }

    /**
     * Applies the lifecycle to this request's session (§3.1) and returns what it found,
     * so the caller can tell the user why they are at the login page and report a replay
     * to the API — the two things that need more than the session. For REPLAYED,
     * `user_id` names the account whose old id came back; it is 0 otherwise.
     *
     * `$activity` is false for a page's own background refresh (REQ-UI-073): the bounds
     * and rotation apply as to any request, but `last_seen_at` stays where the user left
     * it, so a page left open still reaches SESSION_IDLE_TIMEOUT (REQ-AUTH-015).
     *
     * @return array{verdict: string, user_id: int}
     */
    public static function lifecycle(Config $config, int $now, bool $activity = true): array
    {
        $verdict = self::verdict($config, $now);

        $followed = false;
        if ($verdict === self::FOLLOW) {
            self::followSuccessor();
            $followed = true;
            $verdict = self::verdict($config, $now);
            // A successor that is itself a tombstone is not followed a second time.
            if ($verdict === self::FOLLOW || $verdict === self::REPLAYED) {
                self::reset();

                return ['verdict' => self::EXPIRED, 'user_id' => 0];
            }
        }

        switch ($verdict) {
            case self::REPLAYED:
                $userId = (int) ($_SESSION['tombstone']['user_id'] ?? 0);
                self::reset();

                return ['verdict' => self::REPLAYED, 'user_id' => $userId];
            case self::EXPIRED:
            case self::IDLE:
                self::reset();

                return ['verdict' => $verdict, 'user_id' => 0];
            case self::ROTATE:
                self::rotate($now);
                break;
        }

        if ($activity && self::isAuthenticated()) {
            $_SESSION['last_seen_at'] = $now;
        }

        return ['verdict' => $followed && $verdict === self::LIVE ? self::FOLLOW : $verdict, 'user_id' => 0];
    }

    /**
     * Moves the session to a new id (REQ-AUTH-072) and leaves the old one holding only a
     * tombstone: the successor, when it was made, and whose session it was — nothing a
     * page could be rendered from, and nothing that signs anyone in.
     */
    private static function rotate(int $now): void
    {
        if (session_status() !== PHP_SESSION_ACTIVE) {
            // Outside a running session (a CLI test) there is no id to swap; the bookkeeping
            // is what remains.
            $_SESSION['rotated_at'] = $now;

            return;
        }

        $successor = session_create_id();
        if (!is_string($successor) || $successor === '') {
            return; // still due: the next request tries again
        }

        $data = $_SESSION;
        $_SESSION = ['tombstone' => [
            'successor' => $successor,
            'at' => $now,
            'user_id' => (int) ($data['user_id'] ?? 0),
        ]];
        session_write_close();

        // Strict mode adopts only ids the save handler already knows, and the successor has
        // no file until this start creates it. It is an id this request just made with
        // session_create_id(), so strict mode steps aside for this one start; PHP restores
        // the setting when the request ends.
        ini_set('session.use_strict_mode', '0');
        session_id($successor);
        session_start(); // issues the new cookie
        $_SESSION = $data;
        $_SESSION['rotated_at'] = $now;
    }

    /**
     * Continues in the successor a tombstone names (FOLLOW). Strict mode is on again here:
     * a successor that no longer exists — signed out, expired, collected — yields a fresh,
     * empty session instead of one this request invented.
     */
    private static function followSuccessor(): void
    {
        $successor = (string) ($_SESSION['tombstone']['successor'] ?? '');
        if (session_status() !== PHP_SESSION_ACTIVE || preg_match('/^[A-Za-z0-9,-]{22,256}$/', $successor) !== 1) {
            self::reset();

            return;
        }

        session_write_close();
        session_id($successor);
        session_start(); // re-issues the successor's cookie to the client that missed it
        if (!isset($_SESSION['csrf_token']) && !isset($_SESSION['tombstone'])) {
            $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
        }
    }

    /**
     * Ends the signed-in identity but keeps a fresh anonymous session under a new id, so
     * the login page that follows has a CSRF token and the line saying why (REQ-UI-007).
     * The old id's file goes: whatever it held — an identity, a tombstone — it opens
     * nothing again.
     */
    public static function reset(): void
    {
        $_SESSION = [];
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_regenerate_id(true);
        }
        $_SESSION['csrf_token'] = bin2hex(random_bytes(32));
    }

    /**
     * The session epoch the API handed out at sign-in (REQ-AUTH-074), presented on every
     * administration call made for this session — or null when none is recorded.
     */
    public static function epoch(): ?int
    {
        $epoch = $_SESSION['session_epoch'] ?? null;

        return is_int($epoch) ? $epoch : null;
    }

    /**
     * Keeps this session alive across a change it made itself (REQ-AUTH-074): the API
     * raised the epoch to end the account's *other* sessions and answered the new value.
     * The id moves too — a credential change is a privilege boundary like sign-in.
     */
    public static function adoptEpoch(int $epoch): void
    {
        $_SESSION['session_epoch'] = $epoch;
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_regenerate_id(true);
        }
        $_SESSION['rotated_at'] = time();
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
     * @param array{provider_index: int, state: string, code_verifier: string, nonce: string, source_name: string, purpose?: string} $txn
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
            'nonce' => (string) ($txn['nonce'] ?? ''),
            'source_name' => (string) ($txn['source_name'] ?? ''),
            'purpose' => (string) ($txn['purpose'] ?? 'login'),
        ];
    }

    /** The purpose of a live OAuth transaction, or '' when none is available. */
    public static function oauthTransactionPurpose(): string
    {
        $txn = $_SESSION['_oauth_txn'] ?? null;
        if (!is_array($txn) || !isset($txn['started_at'])
            || (int) $txn['started_at'] + self::OAUTH_TRANSACTION_SECONDS < time()) {
            return '';
        }

        return (string) ($txn['purpose'] ?? 'login');
    }

    /** Stores a bounded, non-secret diagnostic report for the next admin page render. */
    public static function setAuthTestReport(array $report): void
    {
        $_SESSION['_auth_test_report'] = $report;
    }

    /** Returns and removes the one-time diagnostic report. */
    public static function takeAuthTestReport(): array
    {
        $report = $_SESSION['_auth_test_report'] ?? [];
        unset($_SESSION['_auth_test_report']);

        return is_array($report) ? $report : [];
    }

    /**
     * Both session bounds of REQ-AUTH-015: SESSION_LIFETIME from sign-in (`issued_at`),
     * and SESSION_IDLE_TIMEOUT from the last request (`last_seen_at`). lifecycle() ends
     * such a session before any guard runs; the guard asks again so a page can never be
     * served from one, and neither check depends on PHP's session GC having run.
     */
    public static function hasTimedOut(Config $config): bool
    {
        $issuedAt = (int) ($_SESSION['issued_at'] ?? 0);
        if ($issuedAt === 0) {
            return false;
        }
        $now = time();
        if ($issuedAt + $config->sessionLifetime < $now) {
            return true;
        }
        $lastSeen = (int) ($_SESSION['last_seen_at'] ?? $issuedAt);

        return $config->sessionIdleTimeout > 0 && $lastSeen + $config->sessionIdleTimeout < $now;
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
        $now = time();
        $_SESSION['issued_at'] = $now;
        $_SESSION['last_seen_at'] = $now;
        $_SESSION['rotated_at'] = $now;
        // An API older than session epochs omits the value; 0 is what such an API's
        // accounts would hold, and that API ignores the header anyway (REQ-AUTH-074).
        $_SESSION['session_epoch'] = (int) ($user['session_epoch'] ?? 0);
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

    /**
     * Writes the session and releases its lock before a long response. PHP holds the session
     * file locked for the whole request, so a download streaming for minutes would otherwise
     * block every other page the same user opens meanwhile. Nothing written to the session
     * after this persists.
     */
    public static function close(): void
    {
        if (session_status() === PHP_SESSION_ACTIVE) {
            session_write_close();
        }
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
     * add/rotate POST redirects, and the page it lands on shows the reveal once. The value
     * lives only in this session entry until that render takes it. `visible` says whether
     * that render shows the value or keeps it masked — masked unless the administrator
     * chose Show, which is audited (REQ-UI-062).
     *
     * @param array{token: string, email: string, user_id: int, visible: bool} $reveal
     */
    public static function stashReveal(array $reveal): void
    {
        $_SESSION['_reveal'] = $reveal;
    }

    /**
     * Keeps the reveal a Members render just took for that page's Show/Hide (REQ-UI-062):
     * those POSTs release the value from here, never from the browser, and the next render
     * that is not their redirect replaces it — null drops it, so a reload ends the reveal.
     *
     * @param array{token: string, email: string, user_id: int, visible: bool}|null $reveal
     */
    public static function holdReveal(int $projectId, ?array $reveal): void
    {
        if ($reveal === null) {
            unset($_SESSION['_reveal_held']);

            return;
        }
        $_SESSION['_reveal_held'] = ['project_id' => $projectId] + $reveal;
    }

    /**
     * The reveal the Members page of this project is showing, or null when there is none.
     *
     * @return array{token: string, email: string, user_id: int, visible: bool}|null
     */
    public static function heldReveal(int $projectId): ?array
    {
        $held = $_SESSION['_reveal_held'] ?? null;
        if (!is_array($held) || ($held['project_id'] ?? null) !== $projectId || !is_string($held['token'] ?? null)) {
            return null;
        }
        unset($held['project_id']);

        return $held;
    }

    /**
     * The acting member's project token, cached for UI data entry (§8.6 step 1) — or null
     * when this session has not fetched it yet. The session is per user and is emptied on
     * every login (establish()), so the cache is per (user, project) by construction.
     */
    public static function cachedProjectToken(int $projectId): ?string
    {
        $token = $_SESSION['proj_token_' . $projectId] ?? null;

        return is_string($token) && $token !== '' ? $token : null;
    }

    public static function storeProjectToken(int $projectId, string $token): void
    {
        $_SESSION['proj_token_' . $projectId] = $token;
    }

    /** Drops a token the data API refused as invalid (§8.6 step 3). */
    public static function forgetProjectToken(int $projectId): void
    {
        unset($_SESSION['proj_token_' . $projectId]);
    }

    /**
     * Whether the Clara assistant may run write tools in this session (REQ-AI-006): read-only
     * is the default — the flag simply is not there until the administrator switches it on —
     * and `establish()`/`reset()` clear it with everything else, so every new sign-in starts
     * read-only again.
     */
    public static function assistantWriteMode(): bool
    {
        return ($_SESSION['assistant_write'] ?? false) === true;
    }

    public static function setAssistantWriteMode(bool $on): void
    {
        $_SESSION['assistant_write'] = $on;
    }

    /** @return array{token: string, email: string, user_id: int, visible: bool}|null */
    public static function takeReveal(): ?array
    {
        $reveal = $_SESSION['_reveal'] ?? null;
        unset($_SESSION['_reveal']);

        return is_array($reveal) && isset($reveal['token']) ? $reveal : null;
    }

    /**
     * Holds one value for the next render only — the same one-shot discipline as the flash
     * buffer, for what a page shows after a redirect besides a line of text: a calculation
     * test result (§7.4), a breaking change waiting for its acknowledgement (§6.8), the
     * commit dialog to reopen (§6.7). Nothing stashed here is a credential or a record value.
     */
    public static function stash(string $key, array $value): void
    {
        $_SESSION['_stash'][$key] = $value;
    }

    /**
     * Holds a value across renders until it is forgotten — an instrument import's uploaded
     * files and its preview between the preview and the import (§7.6). Unlike stash(),
     * reading it leaves it in place.
     */
    public static function keep(string $key, array $value): void
    {
        $_SESSION['_keep'][$key] = $value;
    }

    /** A kept value, or null when none is held. */
    public static function kept(string $key): ?array
    {
        $value = $_SESSION['_keep'][$key] ?? null;

        return is_array($value) ? $value : null;
    }

    public static function forget(string $key): void
    {
        unset($_SESSION['_keep'][$key]);
    }

    /** Takes a stashed value (and forgets it), or null when none was left. */
    public static function take(string $key): ?array
    {
        $value = $_SESSION['_stash'][$key] ?? null;
        unset($_SESSION['_stash'][$key]);

        return is_array($value) ? $value : null;
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
