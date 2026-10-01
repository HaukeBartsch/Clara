<?php
// The two-factor challenge and the enrollment wizard a mandate opens: Sequence G
// of Authentication_Authorization_Design.md §2.7, rendered per §2.2 of the UI
// design (REQ-UI-038/039). The properties worth testing are the ones that carry
// security weight — that the pending state is pre-authentication, that completing
// a challenge never needs the password again (REQ-API-131), and that a secret or
// recovery code is shown once and stored nowhere.

declare(strict_types=1);

use Clara\Session;

/** A session holding only a pending first factor (§3). */
function pending_state(array $overrides = []): void
{
    $_SESSION = [
        'csrf_token' => str_repeat('c', 64),
        'tfa_pending' => array_merge([
            'email' => 'a@example.org',
            'source' => 'local',
            'provider' => '',
            'source_name' => '',
            'first_factor' => 'ff1-handle',
            'user_id' => 42,
            'method' => 'totp',
            'verified_at' => time(),
        ], $overrides),
    ];
}

describe('two-factor challenge (Sequence G, §2.7)', function (): void {
    it('switches the login page to the code panel after mfa_required', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok', 'first_factor' => 'ff1-handle']);
        api_route('/auth/login', ['error' => 'mfa_required', 'method' => 'totp', 'message' => '', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'],
            ['action' => 'credentials']
        ))->dispatch();

        assert_true(!Session::isAuthenticated(), 'a first factor alone must never sign anyone in');
        assert_true(Session::hasPendingSecondFactor(), 'the first-factor success is what the panel resumes from');
        assert_contains('authenticator app', $response->body());
        assert_not_contains('not recognised', $response->body());
    });

    it('keeps the handle verify-password issued, not the password (§2.7)', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok', 'first_factor' => 'ff1-handle']);
        api_route('/auth/login', ['error' => 'mfa_required', 'method' => 'totp', 'message' => '', 'status' => 401], 401);

        router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'secret horse'],
            ['action' => 'credentials']
        ))->dispatch();

        $pending = Session::pendingSecondFactor();
        assert_same('ff1-handle', $pending['first_factor']);
        assert_true(!str_contains(serialize($_SESSION), 'secret horse'), 'a password never enters the session (REQ-AUTH-036)');
    });

    it('completes the login with the handle and the code, and no password (REQ-API-131)', function (): void {
        pending_state();
        api_route('/auth/login', ['id' => 42, 'email' => 'a@example.org', 'display_name' => 'A', 'is_admin' => false]);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'code' => '492817'], ['action' => 'mfa']
        ))->dispatch();

        assert_same(302, $response->status());
        assert_true(Session::isAuthenticated(), 'the verified second factor promotes the session (§2.7)');
        assert_true(!Session::hasPendingSecondFactor(), 'nothing pending survives a completed login');

        $body = api_request_body('/auth/login');
        assert_same('492817', $body['mfa_code'] ?? null);
        assert_same('ff1-handle', $body['first_factor'] ?? null, 'the handle stands in for the password');
        assert_true(!array_key_exists('password', $body), 'the second call carries no credential of its own');
    });

    it('re-shows the panel with the failure line for a wrong code, still signed out', function (): void {
        pending_state();
        api_route('/auth/login', ['error' => 'bad_mfa_code', 'message' => 'the code is wrong or has expired', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'code' => '000000'], ['action' => 'mfa']
        ))->dispatch();

        assert_contains('code is wrong', $response->body());
        assert_true(!Session::isAuthenticated(), 'a rejected code must not sign anyone in');
        assert_true(Session::hasPendingSecondFactor(), 'the challenge stays open for another try');
    });

    it('starts the user over when the first-factor proof has lapsed', function (): void {
        pending_state();
        api_route('/auth/login', ['error' => 'first_factor_expired', 'message' => '', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'code' => '492817'], ['action' => 'mfa']
        ))->dispatch();

        assert_true(!Session::hasPendingSecondFactor(), 'an expired proof ends the challenge (§2.7)');
        assert_contains('sign in again', $response->body());
        assert_contains('name="password"', $response->body(), 'the credential form is what comes back');
    });

    it('asks the API for a fresh code on resend, without ever sending one', function (): void {
        pending_state(['method' => 'email']);
        api_route('/auth/login', ['error' => 'mfa_required', 'method' => 'email', 'message' => '', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64)], ['action' => 'mfa_resend']
        ))->dispatch();

        assert_same(200, $response->status());
        $body = api_request_body('/auth/login');
        assert_true(!array_key_exists('mfa_code', $body), 'a resend asks for a code, it does not answer with one');
        assert_contains('a@example.org', $response->body(), 'the panel names the address the code went to (§2.2)');
    });

    it('refuses every other page while a challenge stands (§2.7)', function (): void {
        pending_state();

        $response = router_for(http_request('GET', '/', browser_headers()))->dispatch();

        assert_same(302, $response->status());
        assert_same('/login', $response->headers()['Location']);
    });
});

describe('two-factor enrollment wizard (mandated, §2.7)', function (): void {
    it('opens the wizard when the installation mandates a factor', function (): void {
        $_SESSION = ['csrf_token' => str_repeat('c', 64)];
        api_route('/auth/verify-password', ['status' => 'ok', 'first_factor' => 'ff1-handle']);
        api_route('/auth/login', [
            'error' => 'tfa_enrollment_required', 'user_id' => 42, 'message' => '', 'status' => 401,
        ], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'email' => 'a@example.org', 'password' => 'pw'],
            ['action' => 'credentials']
        ))->dispatch();

        assert_true(!Session::isAuthenticated());
        assert_same(42, Session::pendingSecondFactor()['user_id'], 'the wizard needs the pending identity (§2.7)');
        assert_contains('authenticator app', $response->body());
    });

    it('drives the wizard as the pending identity and shows the secret once', function (): void {
        pending_state(['method' => '']);
        api_route('/tfa/totp/enroll', ['secret' => 'JBSWY3DPEHPK3PXP', 'otpauth_uri' => 'otpauth://totp/CLARA:a@example.org?secret=JBSWY3DPEHPK3PXP']);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64)], ['action' => 'enroll_totp']
        ))->dispatch();

        assert_contains('JBSWY3DPEHPK3PXP', $response->body());
        assert_true(in_array('X-Internal-User-Id: 42', api_headers_for('/tfa/totp/enroll'), true),
            'the wizard speaks for the identity whose first factor was verified (REQ-API-115)');
        assert_true(!str_contains(serialize($_SESSION), 'JBSWY3DPEHPK3PXP'), 'a secret shown once is not stored (REQ-AUTH-056)');
    });

    it('shows the recovery codes exactly once, on the response that created them', function (): void {
        pending_state(['method' => '']);
        api_route('/tfa/totp/confirm', ['method' => 'totp', 'recovery_codes' => ['ab12cd-ef34567890', '012345-abcdef6789']]);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64), 'code' => '492817'], ['action' => 'enroll_totp_confirm']
        ))->dispatch();

        assert_contains('ab12cd-ef34567890', $response->body());
        assert_contains('shown once', strtolower($response->body()));
        assert_true(!str_contains(serialize($_SESSION), 'ab12cd-ef34567890'), 'codes are never persisted (REQ-AUTH-056)');
    });

    it('returns to the challenge after enrollment, asking for a current code', function (): void {
        pending_state(['method' => '']);
        // Activation set the method, so the resumed login now demands a code.
        api_route('/auth/login', ['error' => 'mfa_required', 'method' => 'totp', 'message' => '', 'status' => 401], 401);

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64)], ['action' => 'enroll_done']
        ))->dispatch();

        assert_same('totp', Session::pendingSecondFactor()['method']);
        assert_contains('authenticator app', $response->body());
    });

    it('clears the pending state when the user steps out of the challenge', function (): void {
        pending_state();

        $response = router_for(http_request(
            'POST', '/login', browser_headers(),
            ['csrf_token' => str_repeat('c', 64)], ['action' => 'abort']
        ))->dispatch();

        assert_true(!Session::hasPendingSecondFactor());
        assert_contains('name="password"', $response->body());
    });
});
