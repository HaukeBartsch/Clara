<?php
// Who the web layer says its caller is (REQ-API-125) — the identity the API's rate limiter counts
// and an audit entry names. The rule is the API's own: REMOTE_ADDR, unless a peer inside
// TRUSTED_PROXY_CIDRS forwarded another address. Applying it here, before anything is forwarded,
// is what stops a browser choosing the address it is limited by or written into an audit row
// (finding F9).

declare(strict_types=1);

use Clara\ApiClient;
use Clara\ConfigError;
use Clara\Logger;
use Clara\Request;
use Clara\TrustedProxies;

/** A request arriving on one connection address and carrying what the peer sent. */
function caller_request(string $peer, array $headers = [], ?TrustedProxies $trusted = null): Request
{
    return new Request('GET', '/', $headers, [], [], [], '', [], $peer, $trusted);
}

/** The default TRUSTED_PROXY_CIDRS: loopback, i.e. the same-host nginx of INSTALL.md §11. */
function loopback_proxies(): TrustedProxies
{
    return trusted_proxies(TrustedProxies::DEFAULT_RAW);
}

describe('TRUSTED_PROXY_CIDRS parsing (§3.9)', function (): void {
    it('accepts a CIDR range and a bare address as one host', function (): void {
        $errors = [];
        $proxies = TrustedProxies::parse('10.0.0.0/8, 192.168.1.5 , ::1', $errors);

        assert_same([], $errors);
        assert_true($proxies->contains('10.1.2.3'));
        assert_true($proxies->contains('192.168.1.5'), 'a bare address is a single-host range');
        assert_true(!$proxies->contains('192.168.1.6'));
        assert_true($proxies->contains('::1'));
    });

    it('skips empty entries and names every value that is no range', function (): void {
        $trailing = [];
        assert_true(TrustedProxies::parse('127.0.0.0/8,', $trailing)->contains('127.0.0.1'));
        assert_same([], $trailing, 'an empty entry is skipped, as the API skips it');

        $errors = [];
        TrustedProxies::parse('localhost, 10.0.0.0/33, ::1/-1', $errors);

        assert_same(3, count($errors), implode(' | ', $errors));
    });

    it('matches within one address family and normalizes what Go normalizes', function (): void {
        $proxies = loopback_proxies();

        assert_true(!$proxies->contains('::127.0.0.1'), 'an IPv4 range holds no IPv6 address…');
        assert_true($proxies->contains('::ffff:127.0.0.1'), '…except the one form that is an IPv4 host');

        $errors = [];
        $linkLocal = TrustedProxies::parse('fe80::/10', $errors);
        assert_true($linkLocal->contains('fe80::1234'));
        assert_true(!$linkLocal->contains('2001:db8::1'));
        assert_true(!$linkLocal->contains(''), 'no address is not a caller');
        assert_true(!$linkLocal->contains('127.0.0.1; DROP TABLE users'));
    });

    it('trusts nobody when the list is empty', function (): void {
        $errors = [];

        assert_true(!TrustedProxies::none()->contains('127.0.0.1'));
        assert_true(!TrustedProxies::parse('', $errors)->contains('127.0.0.1'));
        assert_same('none', TrustedProxies::none()->describe());
    });

    it('spells the ranges back for the Configuration Check page', function (): void {
        assert_same('127.0.0.0/8, ::1/128', loopback_proxies()->describe());
    });
});

describe('Configuration of the caller address (REQ-API-125)', function (): void {
    it('keeps the documented default when the variable is unset or empty', function (): void {
        assert_same('127.0.0.0/8, ::1/128', test_config()->trustedProxies->describe());
        assert_same('127.0.0.0/8, ::1/128', test_config(['TRUSTED_PROXY_CIDRS' => '  '])->trustedProxies->describe());
    });

    it('refuses startup on a value that names no range (REQ-CFG-005)', function (): void {
        $message = '';
        try {
            test_config(['TRUSTED_PROXY_CIDRS' => '10.0.0.0/8, somewhere-private']);
            assert_true(false, 'startup must refuse');
        } catch (ConfigError $e) {
            $message = $e->getMessage();
        }

        assert_true(str_contains($message, 'TRUSTED_PROXY_CIDRS'), $message);
    });
});

describe('Request::clientIp (REQ-API-125, finding F9)', function (): void {
    it('ignores a browser-supplied X-Real-IP when the peer is not a listed proxy', function (): void {
        // The attack this closes: rotate a fake address per request and the limiter behind PHP
        // stops seeing callers — and password_reset_requested records whatever was claimed.
        $request = caller_request('198.51.100.9', ['x-real-ip' => '203.0.113.7'], loopback_proxies());

        assert_same('198.51.100.9', $request->clientIp());
    });

    it('takes the forwarded address from a listed proxy', function (): void {
        $request = caller_request('127.0.0.1', ['x-real-ip' => '203.0.113.7'], loopback_proxies());

        assert_same('203.0.113.7', $request->clientIp());
    });

    it('falls back to the peer when a listed proxy forwards nothing', function (): void {
        assert_same('127.0.0.1', caller_request('127.0.0.1', [], loopback_proxies())->clientIp());
    });

    it('ignores a forwarded value that names no address, even from a listed proxy', function (): void {
        foreach (['', ' ', 'localhost', '999.1.1.1', "1.2.3.4\r\nX-Internal-User-Id: 1", 'fe80::1%eth0'] as $claim) {
            $request = caller_request('127.0.0.1', ['x-real-ip' => $claim], loopback_proxies());

            assert_same('127.0.0.1', $request->clientIp(), "claim \"{$claim}\"");
        }
    });

    it('trusts nobody when the request carries no proxy list', function (): void {
        // A hand-built request — and any caller that forgot to wire the configuration in — must not
        // become the one path that believes a header.
        assert_same('198.51.100.9', caller_request('198.51.100.9', ['x-real-ip' => '203.0.113.7'])->clientIp());
    });

    it('answers 0.0.0.0 when PHP never learned the connection address', function (): void {
        // A unix-socket pool without fastcgi_params: callers cannot be told apart, and the header is
        // still no answer — there is no peer that could have been trusted.
        assert_same('0.0.0.0', caller_request('', ['x-real-ip' => '203.0.113.7'], loopback_proxies())->clientIp());
    });

    it('reads the peer off the server variables the front controller starts from', function (): void {
        // The wiring index.php depends on: fromGlobals() carries REMOTE_ADDR and the configured
        // proxy list into the request, or every browser behind nginx is measured by nginx.
        $server = $_SERVER;
        $_SERVER['REQUEST_URI'] = '/projects';
        $_SERVER['REQUEST_METHOD'] = 'GET';
        $_SERVER['REMOTE_ADDR'] = '127.0.0.1';
        $_SERVER['HTTP_X_REAL_IP'] = '203.0.113.7';

        try {
            assert_same('203.0.113.7', Request::fromGlobals(loopback_proxies())->clientIp());
            assert_same('127.0.0.1', Request::fromGlobals()->clientIp(), 'no list wired in: nothing is trusted');
        } finally {
            $_SERVER = $server;
        }
    });

    it('keeps the peer and the proxy list through the router’s path parameters', function (): void {
        $request = caller_request('127.0.0.1', ['x-real-ip' => '203.0.113.7'], loopback_proxies())
            ->withPathParams(['id' => '7']);

        assert_same('7', $request->pathParam('id'));
        assert_same('203.0.113.7', $request->clientIp());
    });
});

describe('The address that travels to the API (REQ-API-125)', function (): void {
    it('sends the derived address, never the browser’s claim', function (): void {
        api_route('/api/v1/settings', ['rate_limit_enabled' => false, 'rate_limit_rpm' => 600, 'rate_limit_block_minutes' => 10]);
        $client = new ApiClient(
            test_config(),
            new Logger('error', true),
            caller_request('198.51.100.9', ['x-real-ip' => '203.0.113.7'], loopback_proxies()),
            new FakeTransport()
        );

        $client->get('/api/v1/settings');
        $headers = api_headers_for('/api/v1/settings');

        assert_true(in_array('X-Real-IP: 198.51.100.9', $headers, true), implode(' | ', $headers));
        assert_true(!in_array('X-Real-IP: 203.0.113.7', $headers, true), 'the claimed address must not travel');
    });
});
