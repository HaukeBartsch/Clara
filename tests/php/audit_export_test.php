<?php
// The Audits export's server halves (§5.6, REQ-UI-058, REQ-API-158): `download=1` streams
// the API's ZIP through untouched — filters along, pagination never bounding it (REQ-AUD-031)
// — and `estimate=1` answers its JSON for the > 1 GiB warning (REQ-AUD-032); a refusal before
// the first byte becomes a line on the page. The Audits listing itself is covered by
// control_panel_test.php / m3_admin_test.php.

declare(strict_types=1);

use Clara\StreamSink;

/** A sink that keeps what a streamed response sends, for assertions. */
final class AuditExportSink implements StreamSink
{
    public int $status = 0;
    /** @var array<string, string> */
    public array $headers = [];
    /** @var list<string> */
    public array $chunks = [];

    public function begin(int $status, array $headers): void
    {
        $this->status = $status;
        $this->headers = $headers;
    }

    public function write(string $chunk): void
    {
        $this->chunks[] = $chunk;
    }

    public function body(): string
    {
        return implode('', $this->chunks);
    }
}

/** The query of the last call to one API path, parsed as the API reads it. */
function audit_export_query(string $fragment): array
{
    foreach (array_reverse($GLOBALS['api_calls']) as $call) {
        if (!str_contains((string) $call['url'], $fragment)) {
            continue;
        }
        $out = [];
        foreach (explode('&', (string) parse_url((string) $call['url'], PHP_URL_QUERY)) as $pair) {
            [$k, $v] = array_pad(explode('=', $pair, 2), 2, '');
            $out[rawurldecode($k)][] = rawurldecode($v);
        }

        return $out;
    }

    throw new TestFailure('no call to ' . $fragment);
}

function audits_request(array $query): \Clara\Response
{
    return router_for(http_request('GET', '/admin', browser_headers(), [], $query + ['section' => 'audits']))->dispatch();
}

describe('audit export (§5.6, REQ-UI-058, REQ-API-158)', function (): void {
    it('streams the audit ZIP through with the API\'s file name and no page bounding it', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        $zip = "PK\x03\x04" . str_repeat("1,2026-10-13 12:00:00,ui\n", 40);
        // Queued before any bare `/api/v1/audit` fragment: the fake transport answers the
        // first fragment it finds in the URL, and the export URL contains the listing's.
        api_route('/api/v1/audit/export', $zip, 200, [
            'Content-Type' => 'application/zip',
            'Content-Disposition' => 'attachment; filename="audit_events_2026-10-13_1200.zip"',
        ]);

        $response = audits_request(['download' => '1', 'type' => 'events',
            'event_type' => 'login_failure', 'from' => '2026-10-01']);
        assert_true($response->isStream(), 'a download is a streamed response');
        $sink = new AuditExportSink();
        $response->sendTo($sink);

        assert_same(200, $sink->status);
        assert_same('application/zip', $sink->headers['Content-Type']);
        assert_same('attachment; filename="audit_events_2026-10-13_1200.zip"', $sink->headers['Content-Disposition']);
        assert_same('no', $sink->headers['X-Accel-Buffering']);
        assert_same($zip, $sink->body());
        assert_true(count($sink->chunks) > 1, 'handed on piece by piece, not collected (REQ-TECH-011)');

        $query = audit_export_query('/api/v1/audit/export');
        assert_same(['events'], $query['type']);
        assert_same(['login_failure'], $query['event_type']);
        assert_same(['2026-10-01'], $query['from']);
        assert_true(!isset($query['limit']) && !isset($query['cursor']), 'pagination never bounds an export');
    });

    it('prices the export as JSON for the size warning (REQ-AUD-032)', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit/export', ['rows' => 9000000, 'estimated_bytes' => 4831838208]);

        $response = audits_request(['estimate' => '1', 'type' => 'views', 'project' => '7']);
        assert_same(200, $response->status());
        $decoded = json_decode($response->body(), true);
        assert_same(9000000, $decoded['rows']);
        assert_same(4831838208, $decoded['estimated_bytes']);

        $query = audit_export_query('/api/v1/audit/export');
        assert_same(['1'], $query['estimate']);
        assert_same(['views'], $query['type']);
        assert_same(['7'], $query['project']);
    });

    it('answers a failed estimate with JSON, so the export starts unwarned rather than blocked', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit/export', ['error' => 'internal', 'message' => 'internal', 'status' => 500], 500);

        $response = audits_request(['estimate' => '1']);
        assert_same(500, $response->status());
        $decoded = json_decode($response->body(), true);
        assert_same('internal', $decoded['error'], 'JSON for the fetch to read, not an error page');
    });

    it('turns a refusal before the first byte into a line on the Audits page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit/export', ['error' => 'forbidden', 'message' => 'forbidden', 'status' => 403], 403);

        $sink = new AuditExportSink();
        audits_request(['download' => '1'])->sendTo($sink);

        assert_same(303, $sink->status);
        assert_same('/admin?section=audits', $sink->headers['Location']);
        assert_same('', $sink->body(), 'no partial file');
    });

    it('passes on only a file name of the shape the API writes', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit/export', "PK\x03\x04zip", 200, [
            'Content-Type' => 'application/zip',
            'Content-Disposition' => 'attachment; filename="../../etc/passwd"',
        ]);

        $sink = new AuditExportSink();
        audits_request(['download' => '1'])->sendTo($sink);

        assert_same(200, $sink->status);
        assert_true(preg_match('/^attachment; filename="audit_export_\d{4}-\d{2}-\d{2}_\d{4}\.zip"$/',
            $sink->headers['Content-Disposition']) === 1, 'the hostile name is replaced: ' . $sink->headers['Content-Disposition']);
    });

    it('offers the Export control and its estimate script on the Audits page', function (): void {
        sign_in(['is_admin' => 1]);
        queue_shell();
        api_route('/api/v1/audit', ['entries' => [], 'next_cursor' => null]);
        api_route('/api/v1/users', []);
        queue_sidebar_projects();

        $body = audits_request([])->body();
        assert_contains('name="download" value="1"', $body, 'the Export button carries the filters with download=1');
        assert_contains('Export CSV', $body);
        assert_contains('/assets/js/audits.js', $body, 'the estimate-and-warning module loads on this page');
        assert_contains('"admin.audits.export.warning_body"', $body, 'the modal strings are injected (REQ-UI-008)');
    });
});
