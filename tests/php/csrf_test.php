<?php
// CSRF issue and validation (REQ-UI-005, REQ-AUTH-037): a token is always present,
// both presentation channels are accepted, and nothing passes without the token
// this session was issued.

declare(strict_types=1);

use Clara\Csrf;
use Clara\Request;

describe('CSRF (REQ-UI-005)', function (): void {
    it('issues a 64-character hex token once and keeps it', function (): void {
        $_SESSION = [];
        $token = Csrf::token();

        assert_same(64, strlen($token));
        assert_true(preg_match('/^[0-9a-f]{64}$/', $token) === 1, 'the token is 32 random bytes in hex');
        assert_same($token, Csrf::token(), 'the token is stable for the session');
    });

    it('accepts the token from a form field', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];
        $request = new Request('POST', '/x', [], [], ['csrf_token' => str_repeat('a', 64)]);

        assert_true(Csrf::validate($request));
    });

    it('accepts the token from the X-CSRF-Token header', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];
        $request = new Request('POST', '/x', ['x-csrf-token' => str_repeat('a', 64)], [], []);

        assert_true(Csrf::validate($request));
    });

    it('rejects a missing token', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];

        assert_true(!Csrf::validate(new Request('POST', '/x', [], [], [])));
    });

    it('rejects another session’s token', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];
        $request = new Request('POST', '/x', [], [], ['csrf_token' => str_repeat('b', 64)]);

        assert_true(!Csrf::validate($request));
    });

    it('rejects a prefix of the token', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('a', 64)];
        $request = new Request('POST', '/x', [], [], ['csrf_token' => str_repeat('a', 63)]);

        assert_true(!Csrf::validate($request));
    });

    it('renders the hidden field escaped and carrying the token', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];

        assert_contains('name="csrf_token" value="' . str_repeat('c', 64) . '"', Csrf::field());
    });
});
