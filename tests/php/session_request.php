<?php
// One simulated browser request against a real PHP session store, for the rotation
// cases of session_lifecycle_test.php. The harness runs in a single process where no
// session is ever started, so the id swaps of Session::lifecycle() — session_create_id,
// the tombstone write, strict mode, session_regenerate_id — are exercised here instead,
// one child process per request, exactly as PHP-FPM gives each request a fresh state.
//
//   php tests/php/session_request.php '{"dir": "…", "cookie": "…", "now": 1790000000}'
//
// Input (JSON in argv[1]): `dir` the session directory, `cookie` the id the browser
// presents (null for none), `now` the clock lifecycle() reads, and optionally `establish`
// — a user object to sign in with instead of running the lifecycle. Output (JSON on
// stdout): the id the request ended with, the lifecycle outcome, and who is signed in.
// With `inspect` (a session id) instead, it prints that session file's contents.
// Not a test file itself: run.php only collects *_test.php.

declare(strict_types=1);

require __DIR__ . '/../../web/app/bootstrap.php';

use Clara\Config;
use Clara\Session;

$in = json_decode($argv[1] ?? '', true);
if (!is_array($in)) {
    fwrite(STDERR, "usage: session_request.php '<json>'\n");
    exit(2);
}

$config = Config::fromValues([
    'APP_ENV' => 'development',
    'API_ADDR' => '127.0.0.1:8080',
    'INTERNAL_SERVICE_TOKEN' => 'test-service-token-0123456789abcdefghij',
    'WEB_PUBLIC_URL' => 'http://localhost:8000',
    'LOG_LEVEL_WEB' => 'error',
    'SESSION_DIR' => (string) $in['dir'],
    'SESSION_COOKIE_NAME' => 'clara_test',
    'UI_THEME' => 'bootstrap',
]);

// Inspection, not a request: what one session file holds, read without locking or
// writing it (the harness itself never has an active session to decode with).
if (is_string($in['inspect'] ?? null)) {
    if (!is_file($in['dir'] . '/sess_' . $in['inspect'])) {
        echo 'null';
        exit(0);
    }
    session_save_path((string) $in['dir']);
    session_id($in['inspect']);
    session_start(['read_and_close' => true]);
    echo json_encode($_SESSION);
    exit(0);
}

if (is_string($in['cookie'] ?? null)) {
    $_COOKIE[$config->sessionCookieName] = $in['cookie'];
}
$now = (int) $in['now'];

Session::start($config);

$outcome = null;
if (is_array($in['establish'] ?? null)) {
    Session::establish($in['establish'], '');
    // The simulated clock, not the wall clock, is what later requests compare against.
    $_SESSION['issued_at'] = $now;
    $_SESSION['last_seen_at'] = $now;
    $_SESSION['rotated_at'] = $now;
} else {
    $outcome = Session::lifecycle($config, $now);
}

echo json_encode([
    'sid' => session_id(),
    'outcome' => $outcome,
    'user_id' => Session::userId(),
    'authenticated' => Session::isAuthenticated(),
    'epoch' => Session::epoch(),
]);

session_write_close();
