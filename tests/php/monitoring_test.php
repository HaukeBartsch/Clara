<?php
// The Control Panel's Monitoring section (§5.11, REQ-UI-064): its left-panel entry between
// Settings and Configuration Check, the first paint read from `GET /api/v1/system/metrics`
// (Design §4.25) and `GET /api/v1/system/blocked-ips` (§4.26, REQ-API-164), the combined
// {metrics, rate_limit} data region its script re-reads once per second (REQ-UI-044), and
// that an API which cannot answer is itself the finding rather than an error page.

declare(strict_types=1);

/** A snapshot shaped like GET /api/v1/system/metrics (§4.25). */
function monitoring_metrics(array $overrides = []): array
{
    return array_replace_recursive([
        'generated_at' => '2026-10-15 12:34:56',
        'uptime_seconds' => 123.4,
        'process' => ['goroutines' => 7, 'heap_alloc_bytes' => 2097152, 'gc_pause_last_ms' => 0.123],
        'http' => ['requests_total' => 1000, 'routes' => [
            ['route' => 'GET /api/v1/projects/{id}', 'count' => 600, 'sum_ms' => 9000.5, 'resp_bytes_total' => 4194304],
        ]],
        'db' => ['pool' => ['max_open' => 25, 'open' => 5, 'in_use' => 3, 'idle' => 2, 'wait_count' => 11, 'wait_ms' => 40]],
    ], $overrides);
}

/** A payload shaped like GET /api/v1/system/blocked-ips (§4.26). */
function monitoring_rate_limit(array $overrides = []): array
{
    return array_replace_recursive([
        'generated_at' => '2026-10-15 12:34:56',
        'enabled' => true,
        'rpm' => 600,
        'block_minutes' => 10,
        'blocked' => [
            ['ip' => '203.0.113.7', 'blocked_until' => '2026-10-15 12:40:00', 'seconds_remaining' => 240],
        ],
    ], $overrides);
}

/** Queue the Monitoring section's two upstream reads. */
function queue_monitoring_facts(?array $metrics = null, ?array $rateLimit = null): void
{
    api_route('/api/v1/system/metrics', $metrics ?? monitoring_metrics());
    api_route('/api/v1/system/blocked-ips', $rateLimit ?? monitoring_rate_limit());
}

/** A JSON request for the Monitoring section's data region, played out into a sink. */
function monitoring_region(): array
{
    $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'monitoring']))->dispatch();
    $sink = new RecordingSink();
    $response->sendTo($sink);

    return [$response, $sink];
}

describe('Monitoring section (§5.11, REQ-UI-064)', function (): void {
    it('sits between Settings and Configuration Check in the left panel', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        queue_monitoring_facts();

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']))->dispatch()->body();

        assert_contains('href="/admin?section=monitoring"', $body);
        assert_true(strpos($body, 'href="/admin?section=settings"') < strpos($body, 'href="/admin?section=monitoring"'));
        assert_true(strpos($body, 'href="/admin?section=monitoring"') < strpos($body, 'href="/admin?section=check"'));
    });

    it('renders the first paint from both endpoints and hands over to monitoring.js', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        queue_monitoring_facts();

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        // d3 as a UMD script before the section module; both load after app.js and admin.js.
        assert_contains('<script type="module" src="/assets/vendor/d3/d3.v7.min.js"></script>', $body);
        assert_contains('<script type="module" src="/assets/js/monitoring.js"></script>', $body);
        assert_true(strpos($body, '/assets/vendor/d3/d3.v7.min.js') < strpos($body, '/assets/js/monitoring.js'));

        // Gauges the first snapshot already answers; rates need two samples, so they stay em-dashed.
        assert_contains('data-clara-mon-stat="goroutines">7<', $body);
        assert_contains('data-clara-mon-stat="heap">2.0 MiB<', $body);
        assert_contains('data-clara-mon-stat="pool">3 / 25<', $body);
        assert_contains('data-clara-mon-stat="uptime">0:02<', $body);
        assert_contains('data-clara-mon-stat="blocked">1<', $body);
        assert_contains('data-clara-mon-stress-button', $body);
        assert_contains('action="/admin?section=monitoring&amp;action=monitoring_stress_test"', $body);
        assert_contains('StressTest01', $body);
        assert_contains('data-clara-mon-stress-progress', $body);

        // The eight small multiples; the blocked table shows its one row, and the settings line
        // tells "nothing is blocked" from "rate limiting is off".
        foreach (['rps', 'latency', 'goroutines', 'heap', 'pool', 'waits', 'gc', 'throughput'] as $chart) {
            assert_contains('data-clara-mon-chart="' . $chart . '"', $body);
        }
        assert_contains('203.0.113.7', $body);
        assert_contains('600 requests per minute', $body);
        assert_contains('data-clara-mon-failed hidden', $body);

        // The settings line is re-rendered by monitoring.js every tick: its two strings must be
        // among the injected JS keys, or the script would print raw keys over the first paint.
        assert_contains('"admin.monitoring.blocked.on"', $body);
        assert_contains('"admin.monitoring.blocked.off"', $body);
    });

    it('renders the configured chart period, ten minutes by default (REQ-CFG-034)', function (): void {
        sign_in(['is_admin' => 1]);

        // The period reaches monitoring.js as a data attribute — the browser never reads the
        // environment (REQ-UI-002) — and the intro sentence names the same number.
        queue_shell();
        queue_monitoring_facts();
        $default = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']))
            ->dispatch()->body();
        assert_contains('data-clara-mon-period-minutes="10"', $default);
        assert_contains('The charts cover the last 10 minutes', $default);

        // MONITORING_PERIOD_MINUTES decides it; nothing about an hour is baked into the view.
        queue_shell();
        queue_monitoring_facts();
        $configured = router_for(
            http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']),
            test_config(['MONITORING_PERIOD_MINUTES' => '30'])
        )->dispatch()->body();
        assert_contains('data-clara-mon-period-minutes="30"', $configured);
        assert_contains('The charts cover the last 30 minutes', $configured);
    });

    it('passes {metrics, rate_limit} through as its data region (REQ-UI-044)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_monitoring_facts();

        [$response, $sink] = monitoring_region();
        assert_same(200, $sink->status);
        assert_same('application/json; charset=UTF-8', $sink->headers['Content-Type']);

        $payload = json_decode($sink->body(), true);
        assert_same('2026-10-15 12:34:56', $payload['metrics']['generated_at']);
        assert_same(7, $payload['metrics']['process']['goroutines']);
        assert_same(600, $payload['rate_limit']['rpm']);
        assert_same('203.0.113.7', $payload['rate_limit']['blocked'][0]['ip']);
        assert_same(null, $payload['stress_test']);
        assert_true(!isset($payload['metrics_error']) || $payload['metrics_error'] === null);
        assert_true(!isset($payload['rate_limit_error']) || $payload['rate_limit_error'] === null);
    });

    it('renders an API that cannot answer as the finding, not an error page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/system/metrics', ['error' => 'unavailable'], 503);
        api_route('/api/v1/system/blocked-ips', ['error' => 'unavailable'], 503);

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_true(strpos($body, 'data-clara-mon-failed') !== false);
        assert_true(strpos($body, 'data-clara-mon-failed hidden') === false, 'the failure line is shown');
        assert_contains('unavailable', $body);

        // The data region answers too: each key null beside its error code, so the polling
        // page can keep showing what it knows instead of dying on one failed endpoint.
        [, $sink] = monitoring_region();
        assert_same(200, $sink->status);
        $payload = json_decode($sink->body(), true);
        assert_same(null, $payload['metrics']);
        assert_same('unavailable', $payload['metrics_error']);
        assert_same(null, $payload['rate_limit']);
        assert_same('unavailable', $payload['rate_limit_error']);
    });

    it('distinguishes "rate limiting is off" in the blocked table', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        queue_monitoring_facts(monitoring_metrics(), monitoring_rate_limit(['enabled' => false, 'blocked' => []]));

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'monitoring']))->dispatch()->body();

        assert_contains('Rate limiting is off', $body);
        assert_contains('No addresses are blocked.', $body);
    });
});
