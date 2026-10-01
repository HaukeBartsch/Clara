<?php
// The Accept parsing behind REQ-UI-044: only an explicit application/json selects a
// data region — a browser's wildcard must keep rendering the page, or every normal
// navigation would get JSON instead of HTML.

declare(strict_types=1);

use Clara\Request;

function request_with_accept(string $accept): Request
{
    return new Request('GET', '/', ['accept' => $accept], [], []);
}

describe('Accept parsing (REQ-UI-044)', function (): void {
    it('treats a browser Accept header as a page request', function (): void {
        $browser = 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8';

        assert_true(!request_with_accept($browser)->wantsJson());
    });

    it('treats a bare wildcard as a page request, not a data request', function (): void {
        assert_true(!request_with_accept('*/*')->wantsJson());
    });

    it('treats an absent Accept header as a page request', function (): void {
        $request = new Request('GET', '/', [], [], []);

        assert_true(!$request->wantsJson());
    });

    it('takes the shared runtime’s request as a data request', function (): void {
        assert_true(request_with_accept('application/json')->wantsJson());
    });

    it('honours application/json with an explicit quality', function (): void {
        assert_true(request_with_accept('application/json;q=0.9, text/html;q=0.4')->wantsJson());
    });

    it('prefers html when the client ranks it above json', function (): void {
        assert_true(!request_with_accept('text/html;q=1.0, application/json;q=0.3')->wantsJson());
    });

    it('still answers JSON when json is listed and html only via the wildcard', function (): void {
        assert_true(request_with_accept('application/json, */*;q=0.5')->wantsJson());
    });

    it('ignores a zero-quality json range', function (): void {
        assert_true(!request_with_accept('text/html, application/json;q=0')->wantsJson());
    });

    it('matches the type wildcard for application', function (): void {
        assert_true(request_with_accept('application/*')->wantsJson());
    });
});

describe('Request basics', function (): void {
    it('reads the mutation name from ?action=', function (): void {
        $request = new Request('POST', '/login', [], ['action' => 'credentials'], []);

        assert_same('credentials', $request->action());
    });

    it('reads a body field and reports absence', function (): void {
        $request = new Request('POST', '/login', [], [], ['email' => 'a@example.org']);

        assert_same('a@example.org', $request->field('email'));
        assert_true(!$request->has('password'));
    });

    it('forwards a well-formed X-Real-IP and ignores a malformed one (REQ-API-125)', function (): void {
        $good = new Request('GET', '/', ['x-real-ip' => '10.1.2.3'], [], []);
        assert_same('10.1.2.3', $good->clientIp());

        // A header carrying a newline would be an injection vector against the
        // internal API; it must never reach it.
        $bad = new Request('GET', '/', ['x-real-ip' => "1.2.3.4\r\nX-Internal-User-Id: 1"], [], []);
        assert_true($bad->clientIp() !== "1.2.3.4\r\nX-Internal-User-Id: 1");
    });
});
