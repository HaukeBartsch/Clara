<?php
// Session state (Authentication_Authorization_Design.md §3): identity only, an
// absolute bound from the normative issued_at key, a pre-authentication second-factor
// state that no page accepts, and a flash buffer that drains once.

declare(strict_types=1);

use Clara\Session;

describe('session (Authentication §3)', function (): void {
    it('is unauthenticated with no identity keys', function (): void {
        $_SESSION = [];

        assert_true(!Session::isAuthenticated());
        assert_same(0, Session::userId());
    });

    it('is authenticated once the identity keys stand', function (): void {
        sign_in();

        assert_true(Session::isAuthenticated());
        assert_same(7, Session::userId());
        assert_same('Test Member', Session::displayName());
        assert_true(!Session::isAdmin());
    });

    it('falls back to the e-mail when no display name was set', function (): void {
        sign_in(['display_name' => '']);

        assert_same('member@example.org', Session::displayName());
    });

    it('treats a tfa_pending session as pre-authentication (§2.7)', function (): void {
        sign_in(['tfa_pending' => ['email' => 'member@example.org', 'source' => 'local', 'verified_at' => time()]]);

        assert_true(Session::hasPendingSecondFactor(), 'the pending state is recognised');
        assert_true(!Session::isAuthenticated(), 'no authenticated page may be served from it');
    });

    it('drops a pending second factor after its five minutes', function (): void {
        sign_in(['tfa_pending' => ['email' => 'a@example.org', 'source' => 'local', 'verified_at' => time() - 301]]);

        assert_true(!Session::hasPendingSecondFactor());
        assert_true(Session::isAuthenticated(), 'the stale state is cleared rather than honoured');
    });

    it('expires at the configured lifetime from issued_at (REQ-AUTH-015)', function (): void {
        sign_in(['issued_at' => time() - 7200]);
        $config = test_config(['SESSION_LIFETIME' => '3600']);

        assert_true(Session::hasTimedOut($config));
    });

    it('stays live inside the lifetime', function (): void {
        sign_in(['issued_at' => time() - 60]);
        $config = test_config(['SESSION_LIFETIME' => '3600']);

        assert_true(!Session::hasTimedOut($config));
    });

    it('never treats an anonymous session as timed out', function (): void {
        $_SESSION = [];

        assert_true(!Session::hasTimedOut(test_config()));
    });

    it('drains the flash buffer exactly once (§3.4)', function (): void {
        $_SESSION = [];
        Session::flash('success', 'Event follow_up updated');

        assert_same(1, count(Session::takeFlash()));
        assert_same(0, count(Session::takeFlash()), 'a message shows on one render only');
    });

    it('reports a local credential for the Password link’s gate (§2.4)', function (): void {
        sign_in(['auth_source' => 'local']);
        assert_true(Session::hasLocalCredential());

        sign_in(['auth_source' => 'oauth2']);
        assert_true(!Session::hasLocalCredential());
    });

    it('keeps the theme override separate from the installation default (§3.8)', function (): void {
        sign_in(['ui_theme' => null]);
        assert_same(null, Session::uiThemeOverride());

        sign_in(['ui_theme' => 'darkly']);
        assert_same('darkly', Session::uiThemeOverride());
    });
});
