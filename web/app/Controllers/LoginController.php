<?php
// The login page (User_Interface_Design.md §2.2): the credential form, the
// two-factor panel, and the enrollment wizard a mandate opens before a session
// exists — one route, three panels, chosen by what the session holds.
//
// What the page can offer depends on which authentication sources are configured
// (Sequence I, §2.9). With no names configured anywhere, or exactly one distinct
// name, the source picker is skipped and that name applies implicitly
// (REQ-UI-042, REQ-AUTH-067); a local-only installation is precisely that case.
// The OAuth2 redirect (Sequence A) and the LDAP race (Sequence B) are M1's
// remainder; where a selected name has only those sources behind it, the page
// says so plainly rather than accepting a password it cannot verify.
//
// While `tfa_pending` stands nothing else is reachable (§2.7): this controller
// owns both panels, and every other route's guard rejects the state. The panel a
// render shows follows from the pending record — a named method is a challenge to
// answer, an unnamed one is a factor that has to be enrolled first.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Response;
use Clara\Session;

final class LoginController extends Controller
{
    /** GET /login — whichever panel this session's state calls for (§2.2). */
    public function index(): Response
    {
        // Already signed in: the page has nothing to offer.
        if (Session::isAuthenticated()) {
            return Response::redirect('/');
        }

        return $this->renderPanel();
    }

    /** POST /login?action=credentials — the local credential attempt. */
    public function credentials(): Response
    {
        $email = trim($this->request->field('email'));
        $password = $this->request->field('password');
        $sourceName = $this->request->field('source');

        if ($email === '' || $password === '') {
            return $this->renderCredentials($email, $this->i18n->t('login.failure.credentials'));
        }

        try {
            $user = $this->auth->loginWithLocalCredentials($email, $password, $sourceName);
        } catch (ApiException $e) {
            return $this->afterAttempt($e, $email);
        }

        Session::establish($user, $sourceName);

        return Response::redirect($this->safeNext());
    }

    /** POST /login?action=mfa — the second factor for whoever is pending (§2.7). */
    public function twoFactor(): Response
    {
        $code = trim($this->request->field('code'));
        if ($code === '') {
            return $this->renderPanel($this->i18n->t('login.failure.mfa_code'));
        }

        try {
            $this->auth->submitSecondFactor($code);
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        // The pending state is gone and the identity keys stand: the login
        // completes exactly as it would have without a second factor (§2.2).
        return Response::redirect($this->safeNext());
    }

    /** POST /login?action=mfa_resend — a fresh emailed code, or just the retry. */
    public function resendCode(): Response
    {
        try {
            $outstanding = $this->auth->refreshChallenge();
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        if (!$outstanding) {
            return Response::redirect($this->safeNext());
        }

        // Delivery is the API's; this only says what was asked for. A method
        // other than email has nothing to resend, and the panel says so.
        $pending = Session::pendingSecondFactor();
        if ($pending['method'] === 'email') {
            $this->flashSuccess($this->i18n->t('login.mfa.sent'));
        }

        return $this->renderPanel();
    }

    /** POST /login?action=enroll_totp — start a TOTP enrollment (§2.7). */
    public function enrollTotp(): Response
    {
        try {
            $enrollment = $this->auth->enrollTotp();
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        return $this->renderEnroll([
            'secret' => (string) ($enrollment['secret'] ?? ''),
            'otpauth_uri' => (string) ($enrollment['otpauth_uri'] ?? ''),
        ]);
    }

    /** POST /login?action=enroll_totp_confirm — activate it; recovery codes show once. */
    public function confirmTotp(): Response
    {
        return $this->confirmEnrollment('totp', '/api/v1/users/me/tfa/totp/confirm');
    }

    /** POST /login?action=enroll_email — ask for the emailed confirmation code. */
    public function enrollEmail(): Response
    {
        try {
            $this->auth->startEmailFactor();
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        return $this->renderEnroll(['method' => 'email']);
    }

    /** POST /login?action=enroll_email_confirm — activate the email factor. */
    public function confirmEmail(): Response
    {
        return $this->confirmEnrollment('email', '/api/v1/users/me/tfa/email/confirm');
    }

    /** POST /login?action=enroll_done — past the recovery codes, back to the code. */
    public function enrollmentDone(): Response
    {
        try {
            $outstanding = $this->auth->refreshChallenge();
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        return $outstanding ? $this->renderPanel() : Response::redirect($this->safeNext());
    }

    /** POST /login?action=abort — leave the challenge; start over (§2.7). */
    public function abort(): Response
    {
        Session::clearPendingSecondFactor();

        return $this->renderCredentials();
    }

    // --- panels ---------------------------------------------------------------

    /**
     * Renders the panel the session's state calls for: credentials, the code
     * challenge, or the enrollment wizard (§2.2).
     */
    private function renderPanel(string $error = ''): Response
    {
        $pending = Session::pendingSecondFactor();
        if ($pending === []) {
            return $this->renderCredentials(Session::pendingSecondFactor()['email'] ?? '', $error);
        }

        if ($pending['method'] === '') {
            // No method named means tfa_enrollment_required: the installation
            // mandates a second factor this account does not have yet (§2.7).
            return $this->renderEnroll(['method' => 'choose'], $error);
        }

        return $this->renderChallenge($pending, $error);
    }

    /** @param array{email: string, method: string} $pending */
    private function renderChallenge(array $pending, string $error = ''): Response
    {
        return $this->standalone('login_tfa', [
            'pageTitle' => $this->i18n->t('login.mfa.title'),
            'email' => $pending['email'],
            'method' => $pending['method'],
            'error' => $error,
            'next' => $this->safeNext(),
        ], ['titleKey' => 'login.mfa.title']);
    }

    /**
     * The enrollment wizard. `$enrollment` carries what the API returned for this
     * step — a secret, or nothing — and is never stored: a secret shown once is
     * rendered from the response that produced it and appears on no later page
     * (REQ-AUTH-056).
     *
     * @param array<string, mixed> $enrollment
     */
    private function renderEnroll(array $enrollment = [], string $error = ''): Response
    {
        return $this->standalone('login_enroll', [
            'pageTitle' => $this->i18n->t('login.enroll.title'),
            'email' => Session::pendingSecondFactor()['email'] ?? '',
            'enrollment' => $enrollment,
            'method' => (string) ($enrollment['method'] ?? 'choose'),
            'recoveryCodes' => $enrollment['recovery_codes'] ?? [],
            'error' => $error,
            'next' => $this->safeNext(),
        ], ['titleKey' => 'login.enroll.title']);
    }

    /** @param string $error a failure line to show above the form */
    private function renderCredentials(string $email = '', string $error = ''): Response
    {
        $names = $this->auth->sourceNames();

        // A rejected credential re-renders the page with one translated line
        // (§3.3): the status stays 200 so the form is usable, and the reason is
        // logged rather than encoded in a status the browser cannot show.
        return $this->standalone('login', [
            'pageTitle' => $this->i18n->t('login.title'),
            'email' => $email,
            'error' => $error,
            // The picker appears only when there is a real choice to make.
            'sources' => count($names) > 1 ? $names : [],
            'next' => $this->safeNext(),
        ], ['titleKey' => 'login.title']);
    }

    /**
     * One place decides what an attempt's outcome means for the page: a flow
     * state renders the panel it opened, an expired proof starts the user over,
     * and every rejection shows one translated line.
     */
    private function afterAttempt(ApiException $e, string $email): Response
    {
        if (Messages::isFlowState($e->code())) {
            // The first factor was accepted; Auth has already recorded the
            // pending state, so renderPanel() lands on the right panel (§2.7).
            return $this->renderPanel();
        }

        if ($e->code() === 'first_factor_expired') {
            // Not a wrong code: the proof behind it lapsed, so the credential
            // form is the only honest thing left to show (REQ-API-131).
            Session::clearPendingSecondFactor();

            return $this->renderCredentials($email, $this->i18n->t('login.failure.expired_session'));
        }

        // The API has already audited the failure; PHP shows one translated
        // line and never a reason it did not receive (§2.2, REQ-API-006).
        $this->logger->info('login rejected', ['code' => $e->code(), 'status' => $e->status()]);

        if (Session::hasPendingSecondFactor()) {
            return $this->renderPanel(Messages::loginFailure($this->i18n, $e));
        }

        return $this->renderCredentials($email, Messages::loginFailure($this->i18n, $e));
    }

    /** The page to land on after login: only a same-site path is honoured. */
    private function safeNext(): string
    {
        $next = $this->request->query('next');
        if ($next !== '' && str_starts_with($next, '/') && !str_starts_with($next, '//')) {
            return $next;
        }

        return '/';
    }
}
