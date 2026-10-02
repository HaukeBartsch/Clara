<?php
// The self-service second factor (§2.5, REQ-UI-039): the status view, enrollment driven
// through the session identity, disable behind a fresh code — and the one property that
// makes the whole page safe to reload: a secret or a recovery code exists on exactly one
// response and in no state at all (REQ-AUTH-056).
//
// Queue order matters in this file: the fake transport answers the first fragment it finds
// in the URL, and `/users/me/tfa` is a prefix of every action under it.

declare(strict_types=1);

use Clara\Session;

const TFA_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/** Queues the status read plus the shell's own reads (bundle, languages, sidebar). */
function queue_tfa_status(array $status = []): void
{
    queue_shell();
    queue_sidebar_projects();
    api_route('/api/v1/users/me/tfa', array_merge([
        'method' => 'off', 'enrolled_at' => null, 'recovery_codes_remaining' => 0,
    ], $status));
}

/** POSTs one of the page's mutations with a valid CSRF token. */
function tfa_post(string $action, array $fields = []): Clara\Response
{
    return router_for(http_request(
        'POST', '/account/two-factor', browser_headers(),
        array_merge(['csrf_token' => TFA_CSRF], $fields),
        ['action' => $action]
    ))->dispatch();
}

describe('two-factor settings (§2.5, REQ-UI-039)', function (): void {
    it('shows the current method and the ways to enable one', function (): void {
        sign_in();
        queue_tfa_status();

        $response = router_for(http_request('GET', '/account/two-factor', browser_headers()))->dispatch();

        assert_same(200, $response->status());
        assert_contains('Off', $response->body());
        assert_contains('authenticator app', $response->body());
        assert_contains('code by e-mail', $response->body());
    });

    it('shows the method that is on, with when it was enabled', function (): void {
        sign_in();
        queue_tfa_status(['method' => 'totp', 'enrolled_at' => '2026-09-30 12:00:00', 'recovery_codes_remaining' => 8]);

        $response = router_for(http_request('GET', '/account/two-factor', browser_headers()))->dispatch();

        assert_contains('Authenticator app', $response->body());
        assert_contains('2026-09-30 12:00:00', $response->body(), 'a system timestamp is shown as returned (§7 rule 9)');
        assert_contains('8 recovery codes left', $response->body());
    });

    it('offers the disable control only when a factor is on (§3.1)', function (): void {
        sign_in();
        queue_tfa_status();

        $response = router_for(http_request('GET', '/account/two-factor', browser_headers()))->dispatch();

        assert_not_contains('action=disable', $response->body());
    });

    it('shows the TOTP secret once and keeps it out of the session (REQ-AUTH-056)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/totp/enroll', [
            'secret' => 'JBSWY3DPEHPK3PXP',
            'otpauth_uri' => 'otpauth://totp/CLARA:member@example.org?secret=JBSWY3DPEHPK3PXP',
        ]);
        queue_tfa_status();

        $response = tfa_post('totp_start');

        assert_contains('JBSWY3DPEHPK3PXP', $response->body());
        assert_true(!str_contains(serialize($_SESSION), 'JBSWY3DPEHPK3PXP'),
            'a value shown once is stored nowhere (REQ-AUTH-056)');
    });

    it('enrolls through the session identity, not a pre-auth override (REQ-API-115)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/totp/enroll', ['secret' => 'SECRET', 'otpauth_uri' => 'otpauth://x']);
        queue_tfa_status();

        tfa_post('totp_start');

        assert_true(in_array('X-Internal-User-Id: 7', api_headers_for('/tfa/totp/enroll'), true),
            'the signed-in user is the acting identity');
    });

    it('shows the recovery codes exactly once, on the response that created them', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/totp/confirm', [
            'method' => 'totp', 'recovery_codes' => ['ab12cd-ef34567890', '012345-abcdef6789'],
        ]);
        queue_tfa_status(['method' => 'totp']);

        $response = tfa_post('totp_confirm', ['code' => '492817']);

        assert_contains('ab12cd-ef34567890', $response->body());
        assert_contains('shown once', strtolower($response->body()));
        assert_true(!str_contains(serialize($_SESSION), 'ab12cd-ef34567890'));

        $body = api_request_body('/tfa/totp/confirm');
        assert_same('492817', $body['code'] ?? null);
    });

    it('re-shows the panel for a wrong code without earning the key a second showing', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/totp/confirm', [
            'error' => 'bad_mfa_code', 'message' => 'the code is not valid right now', 'status' => 401,
        ], 401);
        queue_tfa_status();

        $response = tfa_post('totp_confirm', ['code' => '000000']);

        assert_contains('wrong or has expired', $response->body());
        assert_not_contains('Manual-entry key', $response->body(), 'the secret is not repeated (§2.5)');
    });

    it('hides the e-mail method when the installation cannot deliver it (REQ-AUTH-057)', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/email/start', [
            'error' => 'smtp_not_configured', 'message' => 'email delivery is not configured', 'status' => 409,
        ], 409);
        queue_tfa_status();

        $response = tfa_post('email_start');

        assert_contains('cannot send e-mail', $response->body());
        assert_not_contains('action=email_start', $response->body(), 'the control is absent, not disabled (§3.1)');
    });

    it('asks for a current code before the API is told to disable anything (§2.5)', function (): void {
        sign_in();
        queue_tfa_status(['method' => 'totp']);

        $response = tfa_post('disable', ['code' => '']);

        assert_same(0, api_calls_to('/tfa/disable'), 'an empty code never reaches the write');
        assert_contains('wrong or has expired', $response->body());
    });

    it('disables with a current code and confirms on the page after', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/users/me/tfa/disable', ['method' => 'off']);
        queue_tfa_status();

        $response = tfa_post('disable', ['code' => '492817']);

        assert_same(302, $response->status());
        assert_same('/account/two-factor', $response->headers()['Location']);
        $flash = Session::takeFlash();
        assert_contains('Two-factor authentication is off.', $flash[0]['text'] ?? '');
    });

    it('refuses a mutation with no CSRF token, before any API call (§3.3)', function (): void {
        sign_in();
        queue_tfa_status();

        $response = router_for(http_request(
            'POST', '/account/two-factor', browser_headers(), [], ['action' => 'totp_start']
        ))->dispatch();

        assert_same(302, $response->status());
        assert_same(0, api_calls_to('/tfa/totp/enroll'), 'a rejected request must not reach the write');
    });

    it('sends an anonymous visitor to the login page, remembering where they meant to go', function (): void {
        $response = router_for(http_request('GET', '/account/two-factor', browser_headers()))->dispatch();

        assert_same(302, $response->status());
        assert_same('/login?next=%2Faccount%2Ftwo-factor', $response->headers()['Location']);
    });
});
