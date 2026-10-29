<?php
// The session lifecycle (Authentication_Authorization_Design.md §3.1): the two bounds of
// REQ-AUTH-015 (idle and absolute), id rotation with a short grace window (REQ-AUTH-072),
// replay detection (REQ-AUTH-073), the session epoch that lets the API end sessions it
// never stored (REQ-AUTH-074), and the cookie rules (REQ-AUTH-075).
//
// The decision (Session::verdict) and the router's handling of it run in-process like
// every other case here. The id swaps need a real session store, so the rotation cases
// at the end drive tests/php/session_request.php — one child process per request.

declare(strict_types=1);

use Clara\Config;
use Clara\ConfigError;
use Clara\Session;

const LIFECYCLE_NOW = 1_790_000_000;

/** A configuration with the default lifecycle settings unless overridden. */
function lifecycle_config(array $overrides = []): Config
{
    return test_config($overrides);
}

describe('session cookie and lifecycle settings (REQ-AUTH-015/072/075, REQ-CFG-017)', function (): void {
    it('defaults to a 30-minute idle bound, 15-minute rotation and an 8-hour lifetime', function (): void {
        $config = lifecycle_config();

        assert_same(1800, $config->sessionIdleTimeout);
        assert_same(900, $config->sessionRotateInterval);
        assert_same(28800, $config->sessionLifetime);
    });

    it('accepts 0 to turn idle timeout and rotation off', function (): void {
        $config = lifecycle_config(['SESSION_IDLE_TIMEOUT' => '0', 'SESSION_ROTATE_INTERVAL' => '0']);

        assert_same(0, $config->sessionIdleTimeout);
        assert_same(0, $config->sessionRotateInterval);
    });

    it('refuses an idle bound under a minute', function (): void {
        $error = assert_throws(ConfigError::class, static fn () => lifecycle_config(['SESSION_IDLE_TIMEOUT' => '30']));

        assert_contains('SESSION_IDLE_TIMEOUT', $error->getMessage());
    });

    it('refuses a rotation interval shorter than twice the grace window', function (): void {
        $error = assert_throws(ConfigError::class, static fn () => lifecycle_config(['SESSION_ROTATE_INTERVAL' => '119']));
        assert_contains('SESSION_ROTATE_INTERVAL', $error->getMessage());

        assert_same(120, lifecycle_config(['SESSION_ROTATE_INTERVAL' => '120'])->sessionRotateInterval);
    });

    it('keeps the plain cookie name for a development cookie without Secure', function (): void {
        $config = lifecycle_config(['SESSION_COOKIE_NAME' => 'csms_session', 'SESSION_COOKIE_SECURE' => '0']);

        assert_true(!$config->sessionCookieSecure);
        assert_same('csms_session', $config->sessionCookieName);
    });

    it('prefixes a Secure cookie with __Host- (REQ-AUTH-075)', function (): void {
        $config = lifecycle_config(['SESSION_COOKIE_NAME' => 'csms_session', 'SESSION_COOKIE_SECURE' => '1']);

        assert_same('__Host-csms_session', $config->sessionCookieName);
    });

    it('defaults production to a Secure, prefixed cookie', function (): void {
        $config = lifecycle_config(['APP_ENV' => 'production', 'SESSION_COOKIE_NAME' => 'csms_session']);

        assert_true($config->sessionCookieSecure);
        assert_same('__Host-csms_session', $config->sessionCookieName);
    });

    it('refuses to start production with SESSION_COOKIE_SECURE=0', function (): void {
        $error = assert_throws(ConfigError::class, static fn () => lifecycle_config([
            'APP_ENV' => 'production',
            'SESSION_COOKIE_SECURE' => '0',
        ]));

        assert_contains('SESSION_COOKIE_SECURE=0', $error->getMessage());
        assert_contains('REQ-AUTH-075', $error->getMessage());
    });
});

describe('session verdict (§3.1)', function (): void {
    it('leaves an anonymous session alone', function (): void {
        $_SESSION = ['csrf_token' => 'x'];

        assert_same(Session::LIVE, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('leaves a pending second factor to its own five-minute clock', function (): void {
        $_SESSION = ['tfa_pending' => ['email' => 'a@example.org', 'verified_at' => time()]];

        assert_same(Session::LIVE, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('finds a fresh session live', function (): void {
        sign_in(['issued_at' => LIFECYCLE_NOW - 60, 'last_seen_at' => LIFECYCLE_NOW - 60, 'rotated_at' => LIFECYCLE_NOW - 60]);

        assert_same(Session::LIVE, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('ends a session that predates session epochs', function (): void {
        sign_in(['issued_at' => LIFECYCLE_NOW, 'last_seen_at' => LIFECYCLE_NOW, 'rotated_at' => LIFECYCLE_NOW]);
        unset($_SESSION['session_epoch']);

        assert_same(Session::EXPIRED, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('ends a session past its absolute lifetime however active it was', function (): void {
        sign_in(['issued_at' => LIFECYCLE_NOW - 28801, 'last_seen_at' => LIFECYCLE_NOW - 5, 'rotated_at' => LIFECYCLE_NOW - 5]);

        assert_same(Session::EXPIRED, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('ends a session idle for longer than SESSION_IDLE_TIMEOUT', function (): void {
        sign_in(['issued_at' => LIFECYCLE_NOW - 3600, 'last_seen_at' => LIFECYCLE_NOW - 1801, 'rotated_at' => LIFECYCLE_NOW - 1801]);

        assert_same(Session::IDLE, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
        assert_true(Session::verdict(lifecycle_config(['SESSION_IDLE_TIMEOUT' => '0', 'SESSION_ROTATE_INTERVAL' => '0']),
            LIFECYCLE_NOW) === Session::LIVE, 'with the idle bound off, the same session stands');
    });

    it('asks for a new id once SESSION_ROTATE_INTERVAL has passed', function (): void {
        sign_in(['issued_at' => LIFECYCLE_NOW - 1000, 'last_seen_at' => LIFECYCLE_NOW - 10, 'rotated_at' => LIFECYCLE_NOW - 900]);

        assert_same(Session::ROTATE, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
        assert_same(Session::LIVE, Session::verdict(lifecycle_config(['SESSION_ROTATE_INTERVAL' => '0']), LIFECYCLE_NOW));
    });

    it('follows a tombstone inside the grace window and calls it a replay after', function (): void {
        $_SESSION = ['tombstone' => ['successor' => str_repeat('b', 32), 'at' => LIFECYCLE_NOW - 60, 'user_id' => 7]];
        assert_same(Session::FOLLOW, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));

        $_SESSION['tombstone']['at'] = LIFECYCLE_NOW - 61;
        assert_same(Session::REPLAYED, Session::verdict(lifecycle_config(), LIFECYCLE_NOW));
    });

    it('never counts a tombstone as signed in', function (): void {
        $_SESSION = ['tombstone' => ['successor' => str_repeat('b', 32), 'at' => LIFECYCLE_NOW, 'user_id' => 7]];

        assert_true(!Session::isAuthenticated());
        assert_same(0, Session::userId());
    });

    it('applies the idle bound in the guard’s own check too', function (): void {
        sign_in(['issued_at' => time() - 3600, 'last_seen_at' => time() - 1801]);

        assert_true(Session::hasTimedOut(lifecycle_config()));
        assert_true(!Session::hasTimedOut(lifecycle_config(['SESSION_IDLE_TIMEOUT' => '0'])));
    });
});

describe('session lifecycle in the router (§3.1)', function (): void {
    it('sends an idle session to the login page, remembering the page, with the reason', function (): void {
        sign_in(['issued_at' => time() - 3600, 'last_seen_at' => time() - 1801, 'rotated_at' => time() - 1801]);

        $response = router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_same('/login?next=%2Faccount%2Fpassword', $response->headers()['Location']);
        assert_true(!Session::isAuthenticated(), 'the identity is gone');
        assert_same(0, count($GLOBALS['api_calls']), 'an ended session reaches no API');
        $flash = Session::takeFlash();
        assert_contains('period of inactivity', $flash[0]['text'] ?? '');
    });

    it('records the request as activity', function (): void {
        sign_in(['last_seen_at' => time() - 600]);
        queue_shell();
        queue_sidebar_projects();

        router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_true($_SESSION['last_seen_at'] >= time() - 1, 'last_seen_at moves to now');
    });

    it('does not count a page’s background refresh as activity (REQ-UI-073)', function (): void {
        $lastSeen = time() - 600;
        sign_in(['last_seen_at' => $lastSeen]);
        queue_shell();
        queue_sidebar_projects();

        $response = router_for(http_request('GET', '/', json_headers() + ['x-clara-refresh' => '1']))->dispatch();

        assert_same(200, $response->status(), 'the refresh is served like any data read');
        assert_same($lastSeen, $_SESSION['last_seen_at'], 'the idle clock stays where the user left it');
    });

    it('still ends an idle session on a background refresh, answering the data read with 403', function (): void {
        sign_in(['issued_at' => time() - 3600, 'last_seen_at' => time() - 1801, 'rotated_at' => time() - 1801]);

        $response = router_for(http_request('GET', '/', json_headers() + ['x-clara-refresh' => '1']))->dispatch();

        assert_same(403, $response->status());
        assert_true(!Session::isAuthenticated(), 'the identity is gone');
        assert_contains('period of inactivity', Session::takeFlash()[0]['text'] ?? '',
            'the reason waits for the reload that takes the page to the login form');
    });

    it('counts the refresh header only on a data read, never on a page render', function (): void {
        $lastSeen = time() - 600;
        sign_in(['last_seen_at' => $lastSeen]);
        queue_shell();
        queue_sidebar_projects();

        router_for(http_request('GET', '/account/password', browser_headers() + ['x-clara-refresh' => '1']))->dispatch();

        assert_true($_SESSION['last_seen_at'] >= time() - 1, 'a navigation is the user’s own');
    });

    it('reports a replayed id to the API as that account and ends the presenting session', function (): void {
        $_SESSION = ['tombstone' => ['successor' => str_repeat('b', 32), 'at' => time() - 600, 'user_id' => 7]];
        api_route('/api/v1/auth/session-replay', ['status' => 'ok']);

        $response = router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_same('/login?next=%2Faccount%2Fpassword', $response->headers()['Location']);
        assert_same(1, api_calls_to('/api/v1/auth/session-replay'));
        $headers = api_headers_for('/api/v1/auth/session-replay');
        assert_true(in_array('X-Internal-User-Id: 7', $headers, true), 'the account the old id belonged to');
        assert_true(count(preg_grep('/^X-Internal-Session-Epoch:/', $headers)) === 0,
            'no session stands behind the report, so no epoch rides on it');
        assert_true(!isset($_SESSION['tombstone']), 'the tombstone is gone with the old id');
        assert_contains('signed out everywhere', Session::takeFlash()[0]['text'] ?? '');
    });

    it('still ends the presenting session when the replay cannot be reported', function (): void {
        $_SESSION = ['tombstone' => ['successor' => str_repeat('b', 32), 'at' => time() - 600, 'user_id' => 7]];
        api_route('/api/v1/auth/session-replay', ['error' => 'internal', 'message' => '', 'status' => 500], 500);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same('/login', $response->headers()['Location']);
        assert_true(!Session::isAuthenticated());
    });

    it('presents the session epoch on every administration call made for the session', function (): void {
        sign_in(['session_epoch' => 5]);
        queue_shell();
        queue_sidebar_projects();

        router_for(http_request('GET', '/account/password', browser_headers()))->dispatch();

        assert_true(count($GLOBALS['api_calls']) > 0, 'the page reads the API');
        foreach ($GLOBALS['api_calls'] as $call) {
            assert_true(in_array('X-Internal-User-Id: 7', $call['headers'], true), $call['url']);
            assert_true(in_array('X-Internal-Session-Epoch: 5', $call['headers'], true), $call['url']);
        }
    });

    it('ends the session and leaves the page when the API answers session_revoked', function (): void {
        sign_in(['session_epoch' => 2]);
        queue_shell();
        api_route('/api/v1/projects',
            ['error' => 'session_revoked', 'message' => 'session revoked', 'status' => 401], 401);

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same('/login', $response->headers()['Location']);
        assert_true(!Session::isAuthenticated());
        assert_contains('Your session was ended', Session::takeFlash()[0]['text'] ?? '');
    });

    it('is not swallowed by a controller that turns refusals into a line on the page', function (): void {
        // POST /lang catches ApiException and flashes it (§3.4); a revocation must not
        // become a red line on a page the user can no longer use.
        sign_in(['session_epoch' => 2]);
        api_route('/api/v1/users/me/ui-language',
            ['error' => 'session_revoked', 'message' => 'session revoked', 'status' => 401], 401);

        $response = router_for(http_request('POST', '/lang', browser_headers(),
            ['csrf_token' => str_repeat('a', 64), 'language' => 'nb']))->dispatch();

        assert_same('/login', $response->headers()['Location']);
        assert_true(!Session::isAuthenticated());
    });

    it('answers a data request with the unauthenticated shape', function (): void {
        sign_in(['session_epoch' => 2]);
        api_route('/api/v1/projects',
            ['error' => 'session_revoked', 'message' => 'session revoked', 'status' => 401], 401);

        $response = router_for(http_request('GET', '/', json_headers()))->dispatch();

        assert_same(403, $response->status());
        assert_same('forbidden', json_decode($response->body(), true)['error'] ?? null);
    });
});

describe('session epoch adoption (REQ-AUTH-074)', function (): void {
    it('records the epoch the API answered at sign-in, 0 when an older API omits it', function (): void {
        Session::establish(['id' => 7, 'email' => 'member@example.org', 'session_epoch' => 4], '');
        assert_same(4, Session::epoch());

        Session::establish(['id' => 7, 'email' => 'member@example.org'], '');
        assert_same(0, Session::epoch());
    });

    it('keeps the session that changed its own password', function (): void {
        sign_in(['session_epoch' => 2]);
        queue_shell();
        api_route('/api/v1/users/me/password', ['ok' => true, 'session_epoch' => 3]);

        $response = router_for(http_request('POST', '/account/password', browser_headers(),
            ['csrf_token' => str_repeat('a', 64), 'current_password' => 'old secret phrase',
                'new_password' => 'a new long secret', 'repeat_password' => 'a new long secret'],
            ['action' => 'change']))->dispatch();

        assert_same('/account/password', $response->headers()['Location']);
        assert_same(3, Session::epoch());
        assert_true(Session::isAuthenticated());
    });

    it('keeps an administrator’s own session across a password set on their own row', function (): void {
        sign_in(['is_admin' => 1, 'session_epoch' => 1]);
        queue_shell();
        api_route('/api/v1/users/7', ['id' => 7, 'enabled' => true, 'session_epoch' => 2]);

        router_for(http_request('POST', '/admin', browser_headers(),
            ['csrf_token' => str_repeat('a', 64), 'id' => '7', 'password' => 'a new long secret',
                'repeat_password' => 'a new long secret'],
            ['action' => 'user_password', 'section' => 'users']))->dispatch();

        assert_same(2, Session::epoch());
    });

    it('leaves the epoch alone when the administrator acted on someone else', function (): void {
        sign_in(['is_admin' => 1, 'session_epoch' => 1]);
        queue_shell();
        api_route('/api/v1/users/9', ['id' => 9, 'enabled' => true, 'session_epoch' => 6]);

        router_for(http_request('POST', '/admin', browser_headers(),
            ['csrf_token' => str_repeat('a', 64), 'id' => '9', 'password' => 'a new long secret',
                'repeat_password' => 'a new long secret'],
            ['action' => 'user_password', 'section' => 'users']))->dispatch();

        assert_same(1, Session::epoch());
    });

    it('does not keep a session whose own account it just disabled', function (): void {
        sign_in(['is_admin' => 1, 'session_epoch' => 1]);
        queue_shell();
        api_route('/api/v1/users/7', ['id' => 7, 'enabled' => false, 'session_epoch' => 2]);

        router_for(http_request('POST', '/admin', browser_headers(),
            ['csrf_token' => str_repeat('a', 64), 'id' => '7', 'enabled' => '0'],
            ['action' => 'user_enabled', 'section' => 'users']))->dispatch();

        assert_same(1, Session::epoch(), 'the next call is refused and the session ends');
    });
});

/**
 * Runs one simulated request in a child process against the session store in `$dir`
 * (tests/php/session_request.php) and returns what it reported.
 *
 * @return array{sid: string, outcome: array{verdict: string, user_id: int}|null, user_id: int, authenticated: bool, epoch: int|null}
 */
function session_request(string $dir, ?string $cookie, int $now, ?array $establish = null): array
{
    $input = ['dir' => $dir, 'cookie' => $cookie, 'now' => $now];
    if ($establish !== null) {
        $input['establish'] = $establish;
    }
    $command = escapeshellarg(PHP_BINARY) . ' ' . escapeshellarg(__DIR__ . '/session_request.php')
        . ' ' . escapeshellarg((string) json_encode($input)) . ' 2>&1';
    $output = (string) shell_exec($command);
    $decoded = json_decode($output, true);
    if (!is_array($decoded)) {
        throw new TestFailure('the session request failed: ' . $output);
    }

    return $decoded;
}

/** The decoded contents of one session file, or null when it does not exist. */
function session_file(string $dir, string $sid): ?array
{
    $command = escapeshellarg(PHP_BINARY) . ' ' . escapeshellarg(__DIR__ . '/session_request.php')
        . ' ' . escapeshellarg((string) json_encode(['dir' => $dir, 'inspect' => $sid])) . ' 2>&1';
    $output = trim((string) shell_exec($command));
    $decoded = json_decode($output, true);
    if ($output !== 'null' && !is_array($decoded)) {
        throw new TestFailure('the session file could not be read: ' . $output);
    }

    return is_array($decoded) ? $decoded : null;
}

describe('session id rotation against a real session store (REQ-AUTH-072/073)', function (): void {
    $dir = sys_get_temp_dir() . '/clara-session-rotation-' . getmypid();
    @mkdir($dir, 0700, true);
    array_map('unlink', glob($dir . '/sess_*') ?: []);

    $user = ['id' => 7, 'email' => 'member@example.org', 'display_name' => 'Member', 'session_epoch' => 3];
    $t = LIFECYCLE_NOW;
    $state = [];

    it('signs in under a fresh id', function () use ($dir, $user, $t, &$state): void {
        $signedIn = session_request($dir, null, $t, $user);

        assert_true($signedIn['authenticated']);
        assert_same(3, $signedIn['epoch']);
        $state['s0'] = $signedIn['sid'];
    });

    it('keeps the id inside the rotation interval', function () use ($dir, $t, &$state): void {
        $live = session_request($dir, $state['s0'], $t + 899);

        assert_same('live', $live['outcome']['verdict']);
        assert_same($state['s0'], $live['sid']);
    });

    it('moves to a new id once the interval has passed and leaves only a tombstone', function () use ($dir, $t, &$state): void {
        $rotated = session_request($dir, $state['s0'], $t + 900);

        assert_same('rotate', $rotated['outcome']['verdict']);
        assert_true($rotated['sid'] !== $state['s0'], 'a new id');
        assert_true($rotated['authenticated'], 'still signed in under it');
        assert_same(3, $rotated['epoch']);

        $old = session_file($dir, $state['s0']);
        assert_same(['tombstone'], array_keys($old ?? []), 'the old id holds nothing else');
        assert_same($rotated['sid'], $old['tombstone']['successor']);
        assert_same(7, $old['tombstone']['user_id']);
        assert_same(7, session_file($dir, $rotated['sid'])['user_id'] ?? null);
        $state['s1'] = $rotated['sid'];
    });

    it('lets a request still carrying the old id continue in the successor during the grace window', function () use ($dir, $t, &$state): void {
        $followed = session_request($dir, $state['s0'], $t + 960);

        assert_same('follow', $followed['outcome']['verdict']);
        assert_same($state['s1'], $followed['sid']);
        assert_true($followed['authenticated']);
    });

    it('treats the old id after the grace window as a replay and ends that presentation', function () use ($dir, $t, &$state): void {
        $replayed = session_request($dir, $state['s0'], $t + 961);

        assert_same('replayed', $replayed['outcome']['verdict']);
        assert_same(7, $replayed['outcome']['user_id']);
        assert_true(!$replayed['authenticated']);
        assert_true($replayed['sid'] !== $state['s0'] && $replayed['sid'] !== $state['s1'], 'a fresh anonymous id');
        assert_same(null, session_file($dir, $state['s0']), 'the tombstone is removed');
    });

    it('does not detect the same old id twice', function () use ($dir, $t, &$state): void {
        // Strict mode refuses an id with no file behind it: the browser just gets a new one.
        $again = session_request($dir, $state['s0'], $t + 1000);

        assert_same('live', $again['outcome']['verdict']);
        assert_true(!$again['authenticated']);
    });

    it('ends the successor once it has been idle too long', function () use ($dir, $t, &$state): void {
        $idle = session_request($dir, $state['s1'], $t + 960 + 1801);

        assert_same('idle', $idle['outcome']['verdict']);
        assert_true(!$idle['authenticated']);
        assert_same(null, session_file($dir, $state['s1']), 'its file is gone');
        array_map('unlink', glob($dir . '/sess_*') ?: []);
        @rmdir($dir);
    });
});
