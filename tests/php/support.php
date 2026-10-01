<?php
// A minimal test harness for the web layer (Design/Technology_Stack_Design.md §6:
// "no framework; smoke tests use a minimal stdlib test harness"). PHPUnit is not on
// the dependency allowlist (REQ-TECH-023/026), so this is the whole framework:
// describe()/it(), four assertions, and a fake transport that answers in place of
// the API — so dispatch, gating and message mapping are tested with no HTTP and no
// database (Plan/Web_Implementation.md §8).

declare(strict_types=1);

require_once __DIR__ . '/../../web/app/bootstrap.php';

use Clara\Config;
use Clara\Transport;

final class TestFailure extends RuntimeException {}

/** @var list<array{suite: string, name: string, error: string|null}> */
$GLOBALS['cases'] = [];

/** @var array<string, list<array{status: int, body: string}>> queued responses per URL fragment */
$GLOBALS['api_routes'] = [];

/** @var list<array{method: string, url: string, headers: list<string>, body: string|null}> */
$GLOBALS['api_calls'] = [];

$GLOBALS['currentSuite'] = '(top level)';

// --- the two-level structure -------------------------------------------------

function describe(string $suite, callable $body): void
{
    $GLOBALS['currentSuite'] = $suite;
    $body();
    $GLOBALS['currentSuite'] = '(top level)';
}

/**
 * Runs one case on a clean slate — no queued API responses, no session — so cases
 * cannot leak into each other regardless of the order they run in.
 */
function it(string $name, callable $body): void
{
    $error = null;
    try {
        resetApi();
        $_SESSION = [];
        $body();
    } catch (Throwable $e) {
        $error = $e::class . ': ' . $e->getMessage() . ' @ ' . basename($e->getFile()) . ':' . $e->getLine();
    }

    $GLOBALS['cases'][] = ['suite' => $GLOBALS['currentSuite'], 'name' => $name, 'error' => $error];
}

// --- assertions --------------------------------------------------------------

function assert_same(mixed $expected, mixed $actual, string $message = ''): void
{
    if ($expected !== $actual) {
        throw new TestFailure(sprintf(
            '%sexpected %s, got %s',
            $message === '' ? '' : $message . ': ',
            var_export($expected, true),
            var_export($actual, true)
        ));
    }
}

function assert_true(mixed $condition, string $message = ''): void
{
    if ($condition !== true) {
        throw new TestFailure(($message === '' ? 'expected true' : $message) . ', got ' . var_export($condition, true));
    }
}

function assert_contains(string $needle, string $haystack, string $message = ''): void
{
    if (!str_contains($haystack, $needle)) {
        throw new TestFailure(($message === '' ? 'expected the text to contain' : $message)
            . ' ' . var_export($needle, true) . " in:\n" . substr($haystack, 0, 800));
    }
}

function assert_not_contains(string $needle, string $haystack, string $message = ''): void
{
    if (str_contains($haystack, $needle)) {
        throw new TestFailure(($message === '' ? 'expected the text NOT to contain' : $message)
            . ' ' . var_export($needle, true));
    }
}

/** Asserts the callable throws, returning the exception for further checks. */
function assert_throws(string $class, callable $body): Throwable
{
    try {
        $body();
    } catch (Throwable $e) {
        if ($e instanceof $class) {
            return $e;
        }

        throw new TestFailure('expected ' . $class . ', got ' . $e::class . ': ' . $e->getMessage());
    }

    throw new TestFailure('expected ' . $class . ', nothing was thrown');
}

// --- configuration and session fixtures --------------------------------------

/** A valid configuration for tests, with the given overrides applied. */
function test_config(array $overrides = []): Config
{
    $dir = sys_get_temp_dir() . '/clara-web-tests';
    if (!is_dir($dir)) {
        mkdir($dir, 0700, true);
    }

    return Config::fromValues(array_merge([
        'APP_ENV' => 'development',
        'API_ADDR' => '127.0.0.1:8080',
        'INTERNAL_SERVICE_TOKEN' => 'test-service-token',
        'WEB_PUBLIC_URL' => 'http://localhost:8000',
        'LOG_LEVEL_WEB' => 'error',
        'SESSION_DIR' => $dir,
        'UI_THEME' => 'bootstrap',
    ], $overrides));
}

/** An authenticated session holding the identity keys of Authentication §3. */
function sign_in(array $overrides = []): void
{
    $_SESSION = array_merge([
        'user_id' => 7,
        'email' => 'member@example.org',
        'display_name' => 'Test Member',
        'is_admin' => 0,
        'auth_source' => 'local',
        'ui_language' => 'en',
        'ui_theme' => null,
        'auth_source_name' => '',
        'issued_at' => time(),
        'csrf_token' => str_repeat('a', 64),
    ], $overrides);
}

// --- the fake transport ------------------------------------------------------

function resetApi(): void
{
    $GLOBALS['api_routes'] = [];
    $GLOBALS['api_calls'] = [];
}

/**
 * Says what the API answers for one endpoint, matched by URL fragment rather than by
 * call order:
 *
 *   api_route('/api/v1/projects', [['id' => 1, …]]);
 *
 * so a test states what each endpoint returns instead of depending on which read a
 * page happens to make first. Queued responses are used in order and the last one
 * repeats, which is what a page that reads the same endpoint twice expects.
 */
function api_route(string $fragment, mixed $body, int $status = 200): void
{
    $encoded = is_string($body) ? $body : (string) json_encode($body);
    $GLOBALS['api_routes'][$fragment][] = ['status' => $status, 'body' => $encoded];
}

/** The two reads an authenticated page render spends before its own data. */
function queue_shell(): void
{
    api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);
    api_route('/api/v1/i18n/languages', [
        ['code' => 'en', 'display_name' => 'English'],
        ['code' => 'nb', 'display_name' => 'Norsk bokmål'],
    ]);
}

final class FakeTransport implements Transport
{
    public function request(string $method, string $url, array $headers, ?string $body): array
    {
        $GLOBALS['api_calls'][] = [
            'method' => $method,
            'url' => $url,
            'headers' => $headers,
            'body' => $body,
        ];

        foreach ($GLOBALS['api_routes'] as $fragment => $queue) {
            if ($queue === [] || !str_contains($url, $fragment)) {
                continue;
            }

            $response = array_shift($queue);
            // Keep the last queued response available for a repeated read.
            $GLOBALS['api_routes'][$fragment] = $queue === [] ? [$response] : $queue;

            return $response;
        }

        throw new TestFailure('no queued API response for ' . $method . ' ' . $url);
    }
}

/** How many calls reached one endpoint — a page's read budget, asserted. */
function api_calls_to(string $fragment): int
{
    return count(array_filter(
        $GLOBALS['api_calls'],
        static fn (array $call): bool => str_contains($call['url'], $fragment)
    ));
}

/** The headers sent with the first call to one endpoint. */
function api_headers_for(string $fragment): array
{
    foreach ($GLOBALS['api_calls'] as $call) {
        if (str_contains($call['url'], $fragment)) {
            return $call['headers'];
        }
    }

    return [];
}

/** The JSON body sent to the first call to one endpoint. */
function api_request_body(string $fragment): array
{
    foreach ($GLOBALS['api_calls'] as $call) {
        if (str_contains($call['url'], $fragment)) {
            $decoded = json_decode((string) $call['body'], true);

            return is_array($decoded) ? $decoded : [];
        }
    }

    return [];
}
