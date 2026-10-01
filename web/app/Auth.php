<?php
// Login, logout and the "is anyone here" question.
//
// The reference application's AC.php established the pattern this follows: an
// access-control step that runs before a page emits anything and redirects to
// login when no session stands behind it (REQ-UI-032). Here it lives in the
// router rather than in a per-page include, for one reason — REQ-UI-044 puts the
// same route behind two shapes (page and data region), and an include that one
// of the two forgets is exactly the hole this file exists to close. The guard is
// declared on the route and evaluated before either shape dispatches, so it
// cannot be skipped; Auth::guard() below is what the router calls.
//
// This pass implements the local (table-based) path only: Sequence F's verify
// step followed by Sequence C's finalize (Authentication_Authorization_Design.md
// §2.6/§2.3). The OAuth2 redirect, the LDAP binds and the parallel race of
// Sequence I are M1's remainder.

declare(strict_types=1);

namespace Clara;

final class Auth
{
    public function __construct(
        private readonly ApiClient $api,
        private readonly Logger $logger,
        private readonly Config $config
    ) {}

    /**
     * The router's guard step. Returns a redirect when the request may not
     * proceed, or null when it may.
     *
     * `public`   — no session needed (login, the password pages).
     * `login`    — an authenticated identity; an expired session goes to /login.
     * `admin`    — additionally is_admin; a signed-in non-admin is refused, not
     *              redirected, because the sidebar never offered the page (§3.1).
     */
    public static function guard(string $level, Config $config, ?string $returnTo = null): ?Response
    {
        if ($level === 'public') {
            return null;
        }

        // A pre-authentication tfa_pending session is not a session: every page
        // guard rejects it (§2.7). Nothing else is reachable while it stands.
        $pending = Session::hasPendingSecondFactor();
        $signedIn = !$pending && Session::isAuthenticated();

        if (!$signedIn) {
            if ($level === 'login' || $level === 'admin') {
                return Response::redirect(self::loginTarget($returnTo));
            }

            return null;
        }

        // Inactivity/expiry: the identity keys are dropped rather than trusted.
        if (Session::hasTimedOut($config)) {
            Session::destroy();

            return Response::redirect(self::loginTarget(null));
        }

        if ($level === 'admin' && !Session::isAdmin()) {
            return Response::html('forbidden', 403);
        }

        return null;
    }

    /** Where an unauthenticated request goes, remembering what it wanted. */
    private static function loginTarget(?string $returnTo): string
    {
        if ($returnTo !== null && $returnTo !== '' && $returnTo !== '/' && str_starts_with($returnTo, '/')) {
            return '/login?next=' . rawurlencode($returnTo);
        }

        return '/login';
    }

    /**
     * The local credential path — Sequence F's verify step followed by
     * Sequence C's finalize. Throws ApiException with the API's code on any
     * rejection — bad_password, account_disabled, account_expired, rate_limited —
     * or with a flow state (mfa_required / tfa_enrollment_required) that the
     * caller must not present as a password failure; by then the pending state is
     * already recorded, so the panel it renders has what it needs.
     *
     * Two calls, deliberately: verify-password is side-effect-free, and login is
     * the only place login side effects happen (§2.6 step 3), which keeps M1's
     * parallel LDAP attempts from producing a second session or a duplicate
     * audit success. Verify also returns the first-factor handle the challenge
     * completes with (REQ-API-131) — the one credential-shaped value PHP is
     * allowed to hold for five minutes, precisely because it is not one.
     *
     * @return array<mixed> the user object the API returned
     */
    public function loginWithLocalCredentials(string $email, string $password, string $sourceName): array
    {
        $verify = $this->api->post('/api/v1/auth/verify-password', [
            'email' => $email,
            'password' => $password,
        ]);

        $state = (string) ($verify['status'] ?? '');
        if ($state !== '' && $state !== 'ok') {
            // verify-password answers ok / bad_password / account_disabled /
            // account_expired without the envelope shape; map it onto one so the
            // caller has a single code path.
            throw new ApiException($state, '', 401);
        }

        $body = ['email' => $email, 'source' => 'local', 'password' => $password];
        if ($sourceName !== '') {
            // Recorded in the login audit details; never affects authorization
            // or identity resolution (REQ-AUTH-067).
            $body['source_name'] = $sourceName;
        }

        return $this->finalize([
            'email' => $email,
            'source' => 'local',
            'provider' => '',
            'source_name' => $sourceName,
            'first_factor' => (string) ($verify['first_factor'] ?? ''),
        ], $body);
    }

    /**
     * Submits the second factor for whoever is pending and promotes the session
     * when it verifies (§2.7). Throws `first_factor_expired` when nothing is
     * pending any more — the five minutes ran out between rendering the panel and
     * submitting it, which is a reason to start over, not a wrong code.
     *
     * @return array<mixed> the user object the API returned
     */
    public function submitSecondFactor(string $code): array
    {
        $pending = Session::pendingSecondFactor();
        if ($pending === []) {
            throw new ApiException('first_factor_expired', '', 401);
        }

        $user = $this->finalize($pending, $this->challengeBody($pending, $code));
        Session::promoteSecondFactor($user, $pending['source_name']);

        return $user;
    }

    /**
     * Re-opens the challenge without a code: the API answers `mfa_required` again
     * and, for an account on the email method, delivers a fresh code — delivery is
     * the API's, rate-limited per account (§2.7, REQ-AUTH-058). This is also how
     * login continues the moment an enrollment has activated the factor: the next
     * code is what completes it.
     *
     * @return bool true while a second factor is still outstanding; false when the
     *              account turned out to need none and the session now stands
     */
    public function refreshChallenge(): bool
    {
        $pending = Session::pendingSecondFactor();
        if ($pending === []) {
            return false;
        }

        try {
            $user = $this->finalize($pending, $this->challengeBody($pending, ''));
        } catch (ApiException $e) {
            if (!Messages::isFlowState($e->code())) {
                throw $e;
            }

            // What the answer says about the challenge wins; an absent field
            // keeps what we already knew rather than blanking it.
            $update = [];
            if ($e->contextString('method') !== '') {
                $update['method'] = $e->contextString('method');
            }
            if ($e->contextInt('user_id') !== 0) {
                $update['user_id'] = $e->contextInt('user_id');
            }
            Session::updatePendingSecondFactor($update);

            return true;
        }

        // No gate left to pass: complete the login this resumed.
        Session::promoteSecondFactor($user, $pending['source_name']);

        return false;
    }

    // --- the enrollment wizard, run pre-session (§2.7) -------------------------
    //
    // The self-service endpoints take the acting identity from
    // X-Internal-User-Id, and PHP supplies an id whose first factor the login
    // call just verified — that is what makes the wizard drivable before a
    // session exists (REQ-API-115). Nothing here stores what the wizard returns:
    // a secret or a recovery code shown once is rendered from this response and
    // never persisted (REQ-AUTH-056).

    /** Starts a TOTP enrollment: `{secret, otpauth_uri}`, shown once. */
    public function enrollTotp(): array
    {
        return $this->wizard()->post('/api/v1/users/me/tfa/totp/enroll');
    }

    /** Confirms it; the response carries the one-time recovery codes. */
    public function confirmTotp(string $code): array
    {
        return $this->wizard()->post('/api/v1/users/me/tfa/totp/confirm', ['code' => $code]);
    }

    /** Starts an email-factor enrollment (409 `smtp_not_configured` without a relay). */
    public function startEmailFactor(): array
    {
        return $this->wizard()->post('/api/v1/users/me/tfa/email/start');
    }

    /** Confirms it; the response carries the one-time recovery codes. */
    public function confirmEmailFactor(string $code): array
    {
        return $this->wizard()->post('/api/v1/users/me/tfa/email/confirm', ['code' => $code]);
    }

    /** The client the wizard speaks through: the pending identity, or nothing. */
    private function wizard(): ApiClient
    {
        $pending = Session::pendingSecondFactor();
        if ($pending['user_id'] === 0) {
            // A challenge that never carried an id cannot enroll: the state is
            // stale or was recorded by a version that predates REQ-API-131.
            throw new ApiException('first_factor_expired', '', 401);
        }

        return $this->api->asUser($pending['user_id']);
    }

    /**
     * The login body for a resumed challenge: identity, the handle standing in
     * for the password (local path — REQ-API-131), and the code when there is
     * one. Never the password itself.
     *
     * @param array<string, string> $pending
     */
    private function challengeBody(array $pending, string $code): array
    {
        $body = ['email' => $pending['email'], 'source' => $pending['source']];
        foreach (['provider' => $pending['provider'], 'source_name' => $pending['source_name'],
                  'first_factor' => $pending['first_factor'], 'mfa_code' => $code] as $key => $value) {
            if ($value !== '') {
                $body[$key] = $value;
            }
        }

        return $body;
    }

    /**
     * Posts one login call and records the pending state when the answer is a
     * flow state instead of a user object (§2.7). Every login path in this class
     * goes through here, so no path can forget the step — or record an identity
     * it did not verify.
     *
     * @param array<string, mixed>  $pending what to remember if a second factor follows
     * @param array<string, scalar> $body    exactly what the API receives
     * @return array<mixed>
     */
    private function finalize(array $pending, array $body): array
    {
        // Read before the attempt: a resumed challenge must keep the window it
        // started with, so five minutes means five minutes from the first factor
        // and not five minutes since the last wrong code (§2.7).
        $alreadyPending = Session::pendingSecondFactor();

        try {
            return $this->api->post('/api/v1/auth/login', $body);
        } catch (ApiException $e) {
            if (!Messages::isFlowState($e->code())) {
                throw $e;
            }

            $record = array_merge($pending, [
                'method' => $e->contextString('method'),
                'user_id' => $e->contextInt('user_id'),
            ]);
            if ($alreadyPending !== []) {
                $record['verified_at'] = $alreadyPending['verified_at'];
            }
            Session::beginSecondFactor($record);

            throw $e;
        }
    }

    /**
     * Sequence D: the audit call precedes session destruction (REQ-UI-007,
     * REQ-AUTH-015). A failing audit must not strand the user in a session we
     * cannot end, so its outcome is logged and destruction proceeds either way.
     */
    public function logout(): void
    {
        try {
            $this->api->post('/api/v1/auth/logout');
        } catch (ApiException $e) {
            $this->logger->warn('logout audit call failed', ['code' => $e->code()]);
        }

        Session::destroy();
    }

    /**
     * The distinct authentication-source names configured for this installation,
     * each with the kinds of source behind it (REQ-CFG-032, REQ-AUTH-064). With
     * exactly one distinct name the login picker is skipped and that name applies
     * implicitly; an empty result means every source forms one implicit default
     * set (REQ-AUTH-067) — which is also the case for a local-only installation.
     *
     * @return list<array{name: string, kinds: list<string>}>
     */
    public function sourceNames(): array
    {
        $byName = [];
        foreach ($this->configSources() as $source) {
            $name = $source['name'];
            $byName[$name][] = $source['kind'];
        }

        $names = [];
        foreach ($byName as $name => $kinds) {
            $names[] = ['name' => $name, 'kinds' => array_values(array_unique($kinds))];
        }
        usort($names, static fn (array $a, array $b): int => strcmp($a['name'], $b['name']));

        return $names;
    }

    /** @return list<array{kind: string, name: string}> */
    private function configSources(): array
    {
        // Config owns the parsing (it is configuration); Auth owns what it means.
        return $this->config->authSources;
    }
}
