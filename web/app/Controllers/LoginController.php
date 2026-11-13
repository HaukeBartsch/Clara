<?php
// The login page (User_Interface_Design.md §2.2): the credential form, the
// two-factor panel, and the enrollment wizard a mandate opens before a session
// exists — one route, three panels, chosen by what the session holds.
//
// What the page can offer depends on which authentication sources are configured
// (Sequence I, §2.9). With no names configured anywhere, or exactly one distinct
// name, the source picker is skipped and that name applies implicitly
// (REQ-UI-042, REQ-AUTH-067); a local-only installation is precisely that case.
// The OAuth2 redirect (Sequence A) and the directory race (Sequence B) are wired here:
// what the selected name offers decides what shows below the picker — the credential form
// when a local source or a directory stands behind it, one "Sign in with …" button per
// provider that needs a browser round trip (REQ-AUTH-066). A name whose sources are all
// OAuth2 says so plainly rather than accepting a password it cannot verify.
//
// While `tfa_pending` stands nothing else is reachable (§2.7): this controller
// owns both panels, and every other route's guard rejects the state. The panel a
// render shows follows from the pending record — a named method is a challenge to
// answer, an unnamed one is a factor that has to be enrolled first.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\OauthHandoff;
use Clara\Response;
use Clara\Session;

final class LoginController extends Controller
{
    /**
     * The landing path an OAuth2 round trip carried in the session (REQ-AUTH-096): the
     * provider returns to the fixed /auth/callback, so callback() takes it from there before
     * the sign-in empties the session, and every page this request renders keeps it.
     */
    private string $rememberedNext = '';

    /** GET /login — whichever panel this session's state calls for (§2.2). */
    public function index(): Response
    {
        // Already signed in: the page has nothing to offer — but where the user was going
        // still stands (a client's sign-in request, REQ-AUTH-096).
        if (Session::isAuthenticated()) {
            return Response::redirect($this->safeNext());
        }

        // A name whose only source is a single OAuth2 provider continues directly into
        // that provider's flow — there is nothing here left to ask first (§2.2,
        // REQ-AUTH-066). Mid-challenge or mid-enrollment, the panel wins: the user is
        // partway through something else.
        if (!Session::hasPendingSecondFactor() && !$this->awaitingChoice()) {
            $name = $this->selectedName();
            $providers = $this->auth->oauthProvidersFor($name);

            if (count($providers) === 1 && !$this->auth->offersCredentialForm($name)) {
                return $this->startOauth($providers[0], $name);
            }
        }

        return $this->renderPanel();
    }

    /**
     * POST /login?action=source — the source-name picker (§2.2). The selection is stored
     * before any credential exchange (§2.9) and the page re-renders for it; what the name
     * offers changes, which is the whole point of choosing.
     */
    public function chooseSource(): Response
    {
        Session::selectSourceName(trim($this->request->field('source')));

        // A redirect rather than a render: the browser's own reload then repeats this
        // POST, and the page it lands on is a GET of the login panel.
        return Response::redirect('/login' . ($this->safeNext() === '/' ? '' : '?next=' . rawurlencode($this->safeNext())));
    }

    /**
     * POST /login?action=oauth — "Sign in with `<provider>`" (§2.2). Sequence A step 1:
     * state and PKCE verifier go into the session, the browser goes to the provider.
     */
    public function authorize(): Response
    {
        // No name chosen yet: no provider is on offer (§2.2) — show the picker.
        if ($this->awaitingChoice()) {
            return $this->renderCredentials();
        }

        $providers = $this->auth->oauthProvidersFor($this->selectedName());
        $wanted = (int) $this->request->field('provider');

        foreach ($providers as $provider) {
            if ((int) $provider['index'] === $wanted) {
                return $this->startOauth($provider, $this->selectedName());
            }
        }

        // A provider the selected name does not offer is not offered — including the case
        // where a hand-made request names one under another name (§2.9).
        return $this->renderCredentials('', $this->i18n->t('login.failure.provider_unavailable'));
    }

    /**
     * Sequence A step 1 for one provider, or the page saying it could not start (§2.2). The
     * start fails before the browser leaves — at discovery, REQ-API-173 — and the chosen name
     * is forgotten with it: kept, it would send every later visit to /login straight back
     * into the same failing start, and this session could never reach the picker again.
     *
     * @param array<string, string|int> $provider one entry of Auth::oauthProvidersFor()
     */
    private function startOauth(array $provider, string $name): Response
    {
        // The provider returns to /auth/callback, which carries no `next`: the session keeps
        // it for the callback (REQ-AUTH-096).
        Session::rememberLoginNext($this->safeNext());

        try {
            return Response::redirect($this->auth->beginOauthRedirect($provider, $name));
        } catch (ApiException $e) {
            Session::selectSourceName('');
            // `reason`, not `code`: the logger drops every `code` value as a possible OAuth code.
            $this->logger->warn('oauth start failed', [
                'provider' => (string) $provider['issuer'],
                'reason' => $e->code(),
                'status' => (string) $e->status(),
            ]);

            $page = $this->standalone('errors/server', [
                'title' => $this->i18n->t('error.server.page_title'),
                'body' => Messages::loginFailure($this->i18n, $e),
                // What the generic 500 page shows for a failure, for whoever has to debug it:
                // the reference always, the API's reason and the failing call in development.
                'ref_line' => Messages::referenceLine($this->i18n, $this->request->ref()),
                'detail' => $this->withIssuer(Messages::pageDetail($e, $this->config->isDevelopment), $provider),
                'back_href' => '/login',
                'back_label' => $this->i18n->t('login.unavailable.back'),
            ], ['titleKey' => 'error.server.page_title']);

            // A render answers 200; this page reports a service that is down.
            return Response::html($page->body(), 503);
        }
    }

    /**
     * The failing call names CLARA's own API, which answered; what could not be reached is
     * the provider, so the detail names the setting that points at it. Development only, like
     * the detail it extends: an empty detail stays empty (REQ-UI-050).
     *
     * @param array<string, string|int> $provider
     */
    private function withIssuer(string $detail, array $provider): string
    {
        if ($detail === '') {
            return '';
        }

        return $detail . ' (OAUTH2_' . (int) $provider['index'] . '_ISSUER=' . (string) $provider['issuer'] . ')';
    }

    /**
     * GET /auth/callback — the provider's redirect back (Sequence A steps 3–7). Any
     * failure returns the user to the login page with one translated line; nothing about
     * the provider's response is shown (§2.1, REQ-API-006).
     */
    public function callback(): Response
    {
        $response = $this->authorizationResponse();

        if (Session::oauthTransactionPurpose() === 'auth_test') {
            if (!Session::isAdmin()) {
                try {
                    $this->auth->completeOauthTest('', $response['state']);
                } catch (ApiException) {
                    // Consume the transaction; the diagnostic is never resumed without its admin session.
                }

                return Response::redirect('/login');
            }

            try {
                $report = $this->auth->completeOauthTest(
                    $response['error'] === '' ? $response['code'] : '',
                    $response['state']
                );
            } catch (ApiException $e) {
                // No report came back at all, which is itself the finding: name the stage that
                // produced nothing and the API's own code, rather than handing the administrator
                // three zeroes and a shrug (REQ-AUTH-085).
                $report = ['ok' => false, 'stage' => 'exchange', 'error' => $e->code()];
            }
            Session::setAuthTestReport(['kind' => 'oauth'] + $report);

            return Response::redirect('/admin?section=auth');
        }

        // The error the provider reports (access_denied and friends) is its own words and
        // never a reason to log anybody in: one line, back to the form.
        if ($response['error'] !== '') {
            $this->auth->cancelOauth($response['state']);
            $this->logger->info('oauth provider reported an error', [
                'error' => substr($response['error'], 0, 64),
            ]);

            return $this->renderCredentials('', $this->i18n->t('login.failure.state_mismatch'));
        }

        // Taken before the sign-in, which starts from an empty session (REQ-AUTH-096).
        $this->rememberedNext = Session::takeLoginNext();

        try {
            $signedIn = $this->auth->completeOauthLogin($response['code'], $response['state']);
        } catch (ApiException $e) {
            return $this->afterAttempt($e, '');
        }

        Session::establish($signedIn['user'], $signedIn['source_name']);
        // The provider Sequence L1 signs this session out of (Authentication §3).
        Session::setOauthProvider($signedIn['provider_index']);

        return Response::redirect($this->safeNext());
    }

    /**
     * The authorization response this callback answers (Sequence A step 3): from the query
     * for a provider answering with `query`, or — with `handoff` in the query — the one
     * POST /auth/callback parked for a provider answering with `form_post` (REQ-CFG-041).
     * A handle that redeems nothing (unknown, used, expired, or the empty one a malformed
     * POST leaves) reads as a provider error, so the transaction is cleared and the login
     * form says the sign-in did not complete. The query's own code and state are ignored
     * beside a handle: one response, one source.
     *
     * @return array{code: string, state: string, error: string}
     */
    private function authorizationResponse(): array
    {
        if (!$this->request->hasQuery('handoff')) {
            return [
                'code' => $this->request->query('code'),
                'state' => $this->request->query('state'),
                'error' => $this->request->query('error'),
            ];
        }
        $taken = (new OauthHandoff($this->config->sessionDir))->take($this->request->query('handoff'), time());
        if ($taken === null) {
            $this->logger->info('oauth form_post handoff not redeemed', []);

            return ['code' => '', 'state' => '', 'error' => 'handoff_invalid'];
        }

        return $taken;
    }

    /**
     * GET /auth/logged-out — the provider's return after Sequence L1 (REQ-AUTH-097, REQ-UI-088).
     * The `state` is compared with the one this browser's pre-authentication session holds and
     * is cleared whatever the outcome. A match within the time bound goes on to a registered
     * client's sign-out address, or to the login page saying the user is signed out of CLARA and
     * of the provider. Anything else — no state, a wrong or expired one, no session — says only
     * that the user is signed out of CLARA. The return proves nothing and changes no session or
     * account: it only chooses a line of text.
     */
    public function loggedOut(): Response
    {
        $return = Session::takeLogoutReturn($this->request->query('state'), time());
        if ($return !== null && $return !== '') {
            return Response::redirect($return);
        }
        if (!Session::isAuthenticated()) {
            Session::flash($return !== null ? 'success' : 'info', $this->i18n->t(
                $return !== null ? 'logout.signed_out_provider' : 'logout.signed_out'
            ));
        }

        return Response::redirect('/login');
    }

    /**
     * POST /login?action=credentials — the credential race for the selected name: local
     * verify and every directory under that name, concurrently, first success wins
     * (§2.9). A name with no credential source under it never reaches this path: the page
     * offers its providers instead (§2.2).
     */
    public function credentials(): Response
    {
        $email = trim($this->request->field('email'));
        $password = $this->request->field('password');
        $sourceName = $this->selectedName();

        // No name chosen yet: there is no credential race to run (§2.2) — show the picker.
        if ($this->awaitingChoice()) {
            return $this->renderCredentials($email);
        }

        if ($email === '' || $password === '') {
            return $this->renderCredentials($email, $this->i18n->t('login.failure.credentials'));
        }

        try {
            $user = $this->auth->loginWithCredentials($email, $password, $sourceName);
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
            'method' => 'totp',
            'secret' => (string) ($enrollment['secret'] ?? ''),
            'otpauth_uri' => (string) ($enrollment['otpauth_uri'] ?? ''),
        ]);
    }

    /** POST /login?action=enroll_totp_confirm — activate it; recovery codes show once. */
    public function confirmTotp(): Response
    {
        return $this->confirmFactor('totp');
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
        return $this->confirmFactor('email');
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

    /**
     * Activates the factor the wizard was on, then shows its recovery codes —
     * exactly once, from this response only (REQ-AUTH-056): nothing here is
     * stored, so reloading cannot bring the codes back and the user has to be
     * told that in the same breath.
     */
    private function confirmFactor(string $method): Response
    {
        $code = trim($this->request->field('code'));
        if ($code === '') {
            return $this->renderEnroll(['method' => $method], $this->i18n->t('login.failure.mfa_code'));
        }

        try {
            $activated = $method === 'totp'
                ? $this->auth->confirmTotp($code)
                : $this->auth->confirmEmailFactor($code);
        } catch (ApiException $e) {
            return $this->afterAttempt($e, Session::pendingSecondFactor()['email'] ?? '');
        }

        return $this->renderEnroll([
            'method' => 'done',
            'recovery_codes' => $activated['recovery_codes'] ?? [],
        ]);
    }

    // --- panels ---------------------------------------------------------------

    /**
     * Renders the panel the session's state calls for: credentials, the code
     * challenge, or the enrollment wizard (§2.2).
     */
    private function renderPanel(string $error = '', string $email = ''): Response
    {
        $pending = Session::pendingSecondFactor();
        if ($pending === []) {
            return $this->renderCredentials($email, $error);
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
            // ?recovery=1 switches the field's help and hints to a recovery code
            // (REQ-UI-038's alternate entry). It is a rendering hint only: the API
            // accepts either kind in the same call, and nothing about what is
            // authorized changes.
            'recovery' => $this->request->query('recovery') === '1',
            'error' => $error,
            'next' => $this->safeNext(),
        ], ['titleKey' => 'login.mfa.title']);
    }

    /**
     * The enrollment wizard. `$enrollment` carries what the API returned for this
     * one step — a secret, recovery codes, or nothing — and none of it is stored:
     * what is shown once is rendered from the response that produced it and
     * appears on no later page (REQ-AUTH-056).
     *
     * @param array<string, mixed> $enrollment
     */
    private function renderEnroll(array $enrollment = [], string $error = ''): Response
    {
        $codes = $enrollment['recovery_codes'] ?? [];

        return $this->standalone('login_enroll', [
            'pageTitle' => $this->i18n->t('login.enroll.title'),
            'email' => Session::pendingSecondFactor()['email'] ?? '',
            'step' => (string) ($enrollment['method'] ?? 'choose'),
            'secret' => (string) ($enrollment['secret'] ?? ''),
            'otpauthUri' => (string) ($enrollment['otpauth_uri'] ?? ''),
            'recoveryCodes' => is_array($codes) ? array_values(array_map('strval', $codes)) : [],
            'error' => $error,
            'next' => $this->safeNext(),
        ], [
            'titleKey' => 'login.enroll.title',
            // Draws the TOTP step's QR code (§2.5) — an enhancement: the manual key works
            // without it (REQ-UI-045).
            'scripts' => ['/assets/js/two_factor.js'],
        ]);
    }

    /** @param string $error a failure line to show above the form */
    private function renderCredentials(string $email = '', string $error = ''): Response
    {
        $names = $this->auth->sourceNames();
        $selected = $this->selectedName();
        // Until a name is chosen the page offers the picker and nothing else (§2.2):
        // no form, no provider button, no "nothing to verify" line.
        $chosen = !$this->awaitingChoice();

        // A rejected credential re-renders the page with one translated line
        // (§3.3): the status stays 200 so the form is usable, and the reason is
        // logged rather than encoded in a status the browser cannot show.
        return $this->standalone('login', [
            'pageTitle' => $this->i18n->t('login.title'),
            'email' => $email,
            'error' => $error,
            // The picker appears only when there is a real choice to make.
            'sources' => count($names) > 1 ? $names : [],
            'selected' => $chosen ? $selected : '',
            'chosen' => $chosen,
            // What the selected name can verify: the credential form when a local source
            // or a directory stands behind it (REQ-AUTH-065), and one button per provider
            // whose login needs a browser round trip (REQ-AUTH-066).
            'offersForm' => $chosen && $this->auth->offersCredentialForm($selected),
            // Named on its button by the issuer's host: no variable carries a display
            // name for a provider itself — the names in configuration are the source
            // names it may answer to, which several sources can share (§2.9).
            'providers' => array_map(
                static fn (array $provider): array => [
                    'index' => (int) $provider['index'],
                    'label' => (string) (parse_url((string) $provider['issuer'], PHP_URL_HOST) ?: $provider['issuer']),
                ],
                $chosen ? $this->auth->oauthProvidersFor($selected) : []
            ),
            'next' => $this->safeNext(),
        ], [
            'titleKey' => 'login.title',
            // Choosing a name submits it at once (§2.2) — the picker has no button of its
            // own, so this module is required, not an enhancement (DEV-UI-13, REQ-UI-045).
            'scripts' => ['/assets/js/login.js'],
        ]);
    }

    /**
     * The source name in play: what this request selected, or what the session remembers.
     * §2.9 stores the selection before any credential exchange, so a re-render and the
     * credential POST that follows both act on the name the user chose.
     */
    private function selectedName(): string
    {
        $posted = trim($this->request->field('source'));
        if ($posted === '') {
            $posted = trim($this->request->query('source'));
        }

        return $posted !== '' ? $posted : Session::selectedSourceName();
    }

    /**
     * True while the picker is on screen and no configured name has been chosen yet — the
     * page then waits for the choice (§2.2, DEV-UI-13). With a single name there is no
     * picker and the name applies implicitly (REQ-AUTH-067). A remembered name that is no
     * longer configured counts as no choice.
     */
    private function awaitingChoice(): bool
    {
        $names = array_column($this->auth->sourceNames(), 'name');
        if (count($names) <= 1) {
            return false;
        }

        return !in_array($this->selectedName(), $names, true);
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
            return $this->renderPanel(Messages::loginFailure($this->i18n, $e, $this->request->ref(), $this->config->isDevelopment));
        }

        return $this->renderCredentials($email, Messages::loginFailure($this->i18n, $e, $this->request->ref(), $this->config->isDevelopment));
    }

    /**
     * The page to land on after login: only a same-site path is honoured. A GET carries it
     * in the query; every form of the login page posts it back as a field, and an OAuth2
     * round trip brings it back from the session (REQ-AUTH-096).
     */
    private function safeNext(): string
    {
        foreach ([$this->request->query('next'), $this->request->field('next'), $this->rememberedNext] as $next) {
            if ($next !== '' && Response::isSameSitePath($next)) {
                return $next;
            }
        }

        return '/';
    }
}
