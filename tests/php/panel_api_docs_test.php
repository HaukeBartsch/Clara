<?php
// The Control Panel's API documentation section (§5.9, REQ-UI-057): its left-panel entry, the
// page that hosts Swagger UI and the relevance Sankey (REQ-UI-060), and the data region that
// passes the API's OpenAPI document on verbatim — the API's own /docs page is never routed to a
// browser (INSTALL.md §13.1). Built on the M6 fixtures (RecordingSink), which run before this file.

declare(strict_types=1);

/** A JSON request for the API documentation section's data region, played out into a sink. */
function api_docs_region(): array
{
    $response = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'api']))->dispatch();
    $sink = new RecordingSink();
    $response->sendTo($sink);

    return [$response, $sink];
}

describe('API documentation section (§5.9, REQ-UI-057)', function (): void {
    it('is the last entry of the Control Panel\'s left panel', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/settings', ['rate_limit_enabled' => false, 'rate_limit_rpm' => 600, 'rate_limit_block_minutes' => 10]);

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'settings']))->dispatch()->body();

        assert_contains('href="/admin?section=api"', $body);
        assert_contains('API documentation', $body);
        assert_true(strpos($body, 'href="/admin?section=settings"') < strpos($body, 'href="/admin?section=api"'));
    });

    it('hosts Swagger UI: the section module and stylesheet, and no API read on render', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $response = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'api']))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_contains('<script type="module" src="/assets/js/api-docs.js"></script>', $body);
        assert_contains('href="/assets/vendor/swagger-ui/swagger-ui.css"', $body);
        assert_contains('data-clara-api-docs data-clara-region="/admin?section=api"', $body);
        assert_contains('href="/admin?section=api"', $body);
        assert_contains('aria-current="page"', $body);
        assert_same(0, api_calls_to('/openapi.json'));
    });

    it('embeds the relevance Sankey above the document (REQ-UI-060)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();

        $body = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'api']))->dispatch()->body();

        assert_contains('data-clara-relevance-sankey', $body);
        // At the top of the section: before the Swagger host.
        assert_true(strpos($body, 'data-clara-relevance-sankey') < strpos($body, 'data-clara-api-docs'));

        // The payload is the generated structure with translated labels — three columns, and the
        // class totals that the classification document states (docs/Requirement_Relevance_Classification.md).
        assert_true(preg_match('#<script type="application/json" data-clara-sankey-data>(.*?)</script>#s', $body, $m) === 1);
        $payload = json_decode($m[1], true);
        assert_true(is_array($payload));

        $byId = [];
        foreach ($payload['nodes'] as $node) {
            $byId[$node['id']] = $node;
            assert_true(($node['label'] ?? '') !== '', 'labels are translated server-side (REQ-UI-008)');
        }
        foreach (['a-api-endpoints', 'g-api-endpoints-req', 'c-security', 'c-resilience'] as $id) {
            assert_true(isset($byId[$id]), "missing node {$id}");
        }
        assert_same('REQ-API', $byId['g-api-endpoints-req']['label']);

        $security = 0;
        foreach ($payload['links'] as $link) {
            if ($link['target'] === 'c-security') {
                $security += $link['value'];
            }
        }
        assert_same($byId['c-security']['codes'], $security);
    });

    it('passes the OpenAPI document on byte for byte as its data region', function (): void {
        sign_in(['is_admin' => 1]);
        // Empty objects must stay objects: a decode/encode round trip would make them lists.
        $spec = '{"openapi":"3.1.0","info":{"title":"CLARA API","version":"1.0.0"},"paths":{},"components":{"schemas":{"Ok":{"type":"object","properties":{}}}}}';
        api_route('/openapi.json', $spec, 200, ['Content-Type' => 'application/json']);

        [$response, $sink] = api_docs_region();

        assert_true($response->isStream());
        assert_same(200, $sink->status);
        assert_same('application/json', $sink->headers['Content-Type']);
        assert_same($spec, $sink->body());
    });

    it('answers a JSON failure when the API cannot give the document', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell(); // the failure line is translated
        api_route('/openapi.json', ['error' => 'internal', 'message' => 'internal error', 'status' => 500], 500);

        [, $sink] = api_docs_region();

        assert_same(500, $sink->status);
        assert_same('internal', json_decode($sink->body(), true)['error']);
    });

    it('is closed to anyone who is not an administrator, page and region alike', function (): void {
        sign_in(['is_admin' => 0]);
        queue_shell();

        $page = router_for(http_request('GET', '/admin', browser_headers(), [], ['section' => 'api']))->dispatch();
        $region = router_for(http_request('GET', '/admin', json_headers(), [], ['section' => 'api']))->dispatch();

        assert_same(403, $page->status());
        assert_same(403, $region->status());
        assert_same(0, api_calls_to('/openapi.json'), 'the guard answers before any read');
    });
});
