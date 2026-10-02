<?php
// The self-service second factor (`User_Interface_Design.md` §2.5, REQ-UI-039): status,
// enrollment for either method, and disable — the same endpoints the pre-login wizard of
// §2.7 drives, here acting for a signed-in user through the session identity.
//
// Two rules shape this controller more than any other page:
//
//   * A secret or a set of recovery codes is shown exactly once, from the response that
//     produced it, and stored nowhere (REQ-AUTH-056). Nothing here survives a reload: no
//     step state sits in the session, so refreshing the page returns the status view —
//     which is what "once" means when the user's next action is F5.
//   * Disabling re-proves possession with a current code or recovery code before the API
//     is asked at all (§2.5); the answer of a wrong one is the panel's own failure line,
//     not a page-level error.
//
// Whether e-mail can be offered is not something PHP knows in advance: the API answers
// 409 `smtp_not_configured` when no relay exists (REQ-AUTH-057), and that answer is what
// hides the control for the render it arrives on — a cached "no SMTP" would outlive the
// configuration change it describes.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Response;

final class TwoFactorController extends Controller
{
    /** GET /account/two-factor — current method and the actions it allows (§2.5). */
    public function index(): Response
    {
        return $this->render($this->status());
    }

    /** POST /account/two-factor?action=totp_start — a fresh secret, shown once. */
    public function startTotp(): Response
    {
        try {
            $enrollment = $this->api->post('/api/v1/users/me/tfa/totp/enroll');
        } catch (ApiException $e) {
            return $this->afterFailure($e, $this->status());
        }

        return $this->render(null, [
            'step' => 'totp',
            'secret' => is_array($enrollment) ? (string) ($enrollment['secret'] ?? '') : '',
            'otpauthUri' => is_array($enrollment) ? (string) ($enrollment['otpauth_uri'] ?? '') : '',
        ]);
    }

    /** POST /account/two-factor?action=totp_confirm — activate it; codes show once. */
    public function confirmTotp(): Response
    {
        return $this->confirmFactor('/api/v1/users/me/tfa/totp/confirm', 'totp');
    }

    /** POST /account/two-factor?action=email_start — the confirmation code by e-mail. */
    public function startEmail(): Response
    {
        try {
            $this->api->post('/api/v1/users/me/tfa/email/start');
        } catch (ApiException $e) {
            return $this->afterFailure($e, $this->status());
        }

        return $this->render(null, ['step' => 'email']);
    }

    /** POST /account/two-factor?action=email_confirm — activate the e-mail factor. */
    public function confirmEmail(): Response
    {
        return $this->confirmFactor('/api/v1/users/me/tfa/email/confirm', 'email');
    }

    /**
     * POST /account/two-factor?action=enroll_cancel — leave an enrollment half done.
     * There is nothing to undo on the API: an unconfirmed enrollment leaves the method
     * `off` (§4.3), so the status page is already the truth. A later start issues a new
     * secret over the abandoned one.
     */
    public function cancelEnrollment(): Response
    {
        return Response::redirect('/account/two-factor');
    }

    /** POST /account/two-factor?action=enroll_done — past the codes, back to status. */
    public function enrollmentDone(): Response
    {
        $this->flashSuccess($this->i18n->t('tfa.enabled'));

        return Response::redirect('/account/two-factor');
    }

    /**
     * POST /account/two-factor?action=disable — turn the factor off after a current code
     * or recovery code proves possession (REQ-AUTH-056).
     */
    public function disable(): Response
    {
        $code = trim($this->request->field('code'));
        if ($code === '') {
            return $this->render(null, ['error' => $this->i18n->t('tfa.failure.code')]);
        }

        try {
            $this->api->post('/api/v1/users/me/tfa/disable', ['code' => $code]);
        } catch (ApiException $e) {
            return $this->afterFailure($e, $this->status());
        }

        $this->flashSuccess($this->i18n->t('tfa.disabled'));

        return Response::redirect('/account/two-factor');
    }

    // --- shared steps ---------------------------------------------------------

    /**
     * Confirms whichever factor was being enrolled. The response carries the recovery
     * codes and is the only place they ever appear (REQ-AUTH-056).
     */
    private function confirmFactor(string $endpoint, string $method): Response
    {
        $code = trim($this->request->field('code'));
        if ($code === '') {
            return $this->render(null, ['step' => $method, 'error' => $this->i18n->t('tfa.failure.code')]);
        }

        try {
            $activated = $this->api->post($endpoint, ['code' => $code]);
        } catch (ApiException $e) {
            return $this->afterFailure($e, $this->status(), $method);
        }

        $codes = $activated['recovery_codes'] ?? [];

        return $this->render(null, [
            // The method the API confirmed needs no passing on: the status read this render
            // makes already reports it, and the codes are the only thing this step adds.
            'step' => 'done',
            'recoveryCodes' => is_array($codes) ? array_values(array_map('strval', $codes)) : [],
        ]);
    }

    /**
     * The status read of §4.3 — method, enrollment time, codes remaining. No secret is
     * ever part of it (REQ-AUTH-056), which is why reloading cannot show one again.
     *
     * @return array{method: string, enrolled_at: string, recovery_codes_remaining: int}
     */
    private function status(): array
    {
        $tfa = $this->api->get('/api/v1/users/me/tfa');
        if (!is_array($tfa)) {
            $tfa = [];
        }

        return [
            'method' => (string) ($tfa['method'] ?? 'off'),
            'enrolled_at' => (string) ($tfa['enrolled_at'] ?? ''),
            'recovery_codes_remaining' => (int) ($tfa['recovery_codes_remaining'] ?? 0),
        ];
    }

    /**
     * One failed call, one page: a wrong or expired code re-shows the panel it belongs to
     * with its own line, and everything else returns the status view with a line that says
     * what could not be done (§3.4). `smtp_not_configured` additionally hides the e-mail
     * control for the render it arrives on — the API is the only source for whether a
     * relay exists, and its 409 is authoritative (REQ-AUTH-057).
     *
     * @param array{method: string, enrolled_at: string, recovery_codes_remaining: int} $status
     * @param string $panel the enrollment step to re-open; '' is the status view, which is
     *                      where a failed disable belongs — its form is part of that view
     */
    private function afterFailure(ApiException $e, array $status, string $panel = ''): Response
    {
        $this->logger->info('two-factor change rejected', ['code' => $e->code(), 'status' => $e->status()]);

        $error = match ($e->code()) {
            'bad_mfa_code' => $this->i18n->t('tfa.failure.code'),
            'code_send_limited' => Messages::rateLimited($this->i18n, $e),
            'smtp_not_configured' => $this->i18n->t('tfa.failure.no_email'),
            'smtp_send_failed' => $this->i18n->t('tfa.failure.send_failed'),
            // A conflict names what was already true — an enabled factor, or no
            // enrollment pending — and the §3.4 table shows the API's reason for it.
            default => Messages::forApiException($this->i18n, $e),
        };

        return $this->render($status, [
            'step' => $panel,
            'error' => $error,
            'emailUnavailable' => $e->code() === 'smtp_not_configured',
        ]);
    }

    /**
     * Renders the page. `$status` is the status read; `null` asks for a fresh one, which
     * keeps every failure path from having to fetch it itself — and still means one read
     * per render (Plan/Web_Implementation.md §7 rule 13).
     *
     * @param array{method: string, enrolled_at: string, recovery_codes_remaining: int}|null $status
     * @param array<string, mixed> $view
     */
    private function render(?array $status, array $view = []): Response
    {
        return $this->page('account/two_factor', [
            'pageTitle' => $this->i18n->t('tfa.title'),
            'tfa' => $status ?? $this->status(),
            'step' => (string) ($view['step'] ?? ''),
            'secret' => (string) ($view['secret'] ?? ''),
            'otpauthUri' => (string) ($view['otpauthUri'] ?? ''),
            'recoveryCodes' => is_array($view['recoveryCodes'] ?? null) ? $view['recoveryCodes'] : [],
            'error' => (string) ($view['error'] ?? ''),
            // The e-mail method is offered unless this very render learned it cannot be.
            'emailAvailable' => empty($view['emailUnavailable']),
            'sidebarProjects' => $this->visibleProjects(),
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js', '/assets/js/two_factor.js'],
            'jsKeys' => ['tfa.copy', 'tfa.copied'],
        ]);
    }
}
