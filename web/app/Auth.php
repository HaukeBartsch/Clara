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
// Three first-factor paths, all finalizing through Sequence C so that login side
// effects happen in exactly one place (Authentication_Authorization_Design.md §2.3):
// the local verify of Sequence F, the directory binds of Sequence B — attempted
// concurrently with it, under the name the user selected, per §2.9 — and the OAuth2
// authorization-code round trip of Sequence A, which is a browser flow and so never
// joins the race (REQ-AUTH-066). The second factor then guards whatever the first one
// answered (§2.7).

declare(strict_types=1);

namespace Clara;

final class Auth
{
    private readonly Oauth $oauth;

    private readonly LdapRace $ldap;

    public function __construct(
        private readonly ApiClient $api,
        private readonly Logger $logger,
        private readonly Config $config,
        ?Oauth $oauth = null,
        ?LdapRace $ldap = null
    ) {
        // Injectable rather than constructed inline so the redirect and the directory
        // race can be exercised against a fixture (Plan/Web_Implementation.md §8).
        $this->oauth = $oauth ?? new Oauth($config, $logger);
        $this->ldap = $ldap ?? new LdapRace($config, $logger);
    }

    /**
     * The router's guard step. Returns a redirect when the request may not
     * proceed, or null when it may.
     *
     * `public`   — no session needed (login, the password pages).
     * `login`    — an authenticated identity; an expired session goes to /login.
     * `admin`    — additionally is_admin; a signed-in non-admin is refused, not
     *              redirected, because the header never offered the Control Panel (§3.1).
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
     * Sequence I — the credential race for the name the user selected (§2.9). Every
     * credential source under that name is attempted concurrently: Sequence F's verify
     * step in this process, one directory bind per child process. The first attempt to
     * succeed finalizes through Sequence C alone; when none does, the submission is
     * reported once — with the per-source outcomes — through a call that cannot
     * authenticate (REQ-API-135).
     *
     * Throws ApiException with the API's code on rejection — bad_password,
     * account_disabled, account_expired, provider_unavailable, rate_limited — or with a
     * flow state (mfa_required / tfa_enrollment_required) that the caller must not
     * present as a password failure; by then the pending state is recorded, so the panel
     * it renders has what it needs.
     *
     * @return array<mixed> the user object the API returned
     */
    public function loginWithCredentials(string $email, string $password, string $sourceName): array
    {
        $sources = $this->sourcesFor($sourceName);

        if (!$sources['local'] && $sources['ldap'] === []) {
            // Nothing under this name verifies a password: it is an OAuth2-only name. The
            // page offers that provider instead — accepting a password here would promise
            // a check nobody is able to perform (§2.2, REQ-AUTH-066).
            throw new ApiException('provider_unavailable', 'no credential source under this name', 503);
        }

        $running = $this->ldap->start($sources['ldap'], $email, $password);

        // The local attempt runs here, in this process, while the directory binds are in
        // flight: that overlap is what §2.9 asks for and a sequential chain cannot give.
        // verify-password produces no login side effects, so losing costs nothing — login
        // stays the only place sessions and successes happen (§2.6 step 3). Failed
        // verifies do extend the API-side Sequence E lockout (security finding F1), which
        // is exactly the bound a guessed password needs.
        $localOutcome = null;
        $firstFactor = '';
        if ($sources['local']) {
            [$localOutcome, $firstFactor] = $this->verifyLocal($email, $password);
        }

        if ($localOutcome === 'ok') {
            // First "login ok" wins. The directories still probing are stopped now, so no
            // later success can produce a second session or a duplicate audit success
            // (REQ-AUTH-065).
            $this->ldap->abandon($running);

            return $this->finalize([
                'email' => $email,
                'source' => 'local',
                'provider' => '',
                'source_name' => $sourceName,
                // The handle REQ-API-131's challenge completes with — the one
                // credential-shaped value PHP may hold for five minutes, precisely because
                // it is not one.
                'first_factor' => $firstFactor,
            ], $this->loginBody($email, 'local', $sourceName, [
                'password' => $password,
                'first_factor' => $firstFactor,
            ]));
        }

        $race = $this->ldap->finish($running);
        $attempts = $race['outcomes'];
        if ($localOutcome !== null) {
            $attempts = ['local' => $localOutcome] + $attempts;
        }

        if ($race['winner'] !== null) {
            // The address the directory holds, not the one that was typed: on this path
            // the directory is the authority on identity (§2.2 step 3, REQ-AUTH-004).
            $winner = $race['winner'];

            return $this->finalize([
                'email' => $winner['email'],
                'source' => 'ldap',
                'provider' => $winner['provider'],
                'source_name' => $sourceName,
                'first_factor' => '',
            ], $this->loginBody($winner['email'], 'ldap', $sourceName, [
                'provider' => $winner['provider'],
            ]));
        }

        throw $this->reportFailedRace($email, $sourceName, $sources, $attempts);
    }

    /**
     * Sequence F's verify step, expressed the way the race needs it: an outcome word for
     * the attempts map, plus the first-factor handle when a second factor guards the
     * account. It never throws — one source failing must not end the race (§2.9).
     *
     * @return array{0: string, 1: string}
     */
    private function verifyLocal(string $email, string $password): array
    {
        try {
            $verify = $this->api->post('/api/v1/auth/verify-password', [
                'email' => $email,
                'password' => $password,
            ]);
        } catch (ApiException $e) {
            return [self::localOutcome($e->code()), ''];
        }

        // verify-password answers ok / bad_password / account_disabled / account_expired
        // without the error-envelope shape; anything else means the API did not answer as
        // itself, which is an outage on this source rather than a credential result.
        $state = (string) ($verify['status'] ?? 'ok');

        return [$state === '' ? 'ok' : $state, (string) ($verify['first_factor'] ?? '')];
    }

    /** What a local attempt's API answer means in the race's `attempts` map (§2.6). */
    private static function localOutcome(string $code): string
    {
        return match ($code) {
            'ok' => 'ok',
            // Unknown account and wrong password are one outcome, as everywhere else in
            // login: telling them apart is an enumeration oracle (REQ-AUTH-050).
            'bad_password', 'account_not_found' => 'bad_password',
            'account_disabled' => 'account_disabled',
            'account_expired' => 'account_expired',
            default => 'unreachable',
        };
    }

    /**
     * One login body, carrying exactly the attributes Sequence C defines (§2.3) and none
     * that are empty — the same discipline every other write keeps, since the API answers
     * 400 on attributes outside its whitelist.
     *
     * @param array<string, string> $extra
     * @return array<string, scalar>
     */
    private function loginBody(string $email, string $source, string $sourceName, array $extra): array
    {
        $body = ['email' => $email, 'source' => $source];

        if ($sourceName !== '') {
            // Recorded in the login audit details; never affects authorization or
            // identity resolution (REQ-AUTH-067).
            $body['source_name'] = $sourceName;
        }

        foreach ($extra as $key => $value) {
            if ($value !== '') {
                $body[$key] = $value;
            }
        }

        return $body;
    }

    /**
     * The submission's single `login_failure` (§2.9 "All failed"). PHP holds no database
     * access to write it (REQ-TECH-006), so the API records it — through the login
     * endpoint, with the per-source outcomes attached and a contract that makes such a
     * call unable to authenticate (REQ-API-135). That contract is what makes this safe
     * for `source: "ldap"`, whose word the API otherwise takes at face value.
     *
     * @param array{local: bool, ldap: list<array<string, int|string>>} $sources
     * @param array<string, string>                                      $attempts
     */
    private function reportFailedRace(string $email, string $sourceName, array $sources, array $attempts): ApiException
    {
        $body = $this->loginBody($email, $sources['local'] ? 'local' : 'ldap', $sourceName, []);
        if (!$sources['local'] && $sources['ldap'] !== []) {
            $body['provider'] = 'ldap-' . $sources['ldap'][0]['index'];
        }
        $body['attempts'] = $attempts;

        try {
            $this->api->post('/api/v1/auth/login', $body);
        } catch (ApiException $e) {
            // The API's answer is the line the page shows: a credential failure, or the
            // outage when nothing could be reached, or the account's own state — which
            // outranks both (§2.6 step 4).
            return $e;
        }

        // Unreachable while REQ-API-135 holds. Should that contract ever break, no session
        // is established from the answer either way.
        $this->logger->error('login authenticated an attempts-bearing failure report');

        return new ApiException('internal', '', 500);
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

    /**
     * Which sources a selected name selects (REQ-AUTH-063/064): the local kind, and the
     * OAuth2 providers and LDAP servers whose name list contains it. Credentials entered
     * for a name never go to a source registered under another (§2.9).
     *
     * Two conveniences from REQ-AUTH-067 apply here rather than in the page:
     *
     *   * with exactly one distinct name configured, no picker is shown and that name
     *     applies implicitly — so an empty selection resolves to it;
     *   * a source carrying no name belongs to the implicit default set, selected when no
     *     name is in play. It also answers under a single-name installation, where this
     *     source was never given the chance to carry that name and would otherwise be
     *     unreachable through a picker that does not exist.
     *
     * @return array{local: bool, oauth2: list<array<string, string|int>>, ldap: list<array<string, string|int>>}
     */
    public function sourcesFor(string $selectedName): array
    {
        $distinct = array_column($this->sourceNames(), 'name');
        $name = trim($selectedName);
        $implicit = false;

        if ($name === '' && count($distinct) === 1) {
            $name = $distinct[0];
            $implicit = true;
        }

        $selects = static function (array $namesOfSource) use ($name, $implicit): bool {
            if ($namesOfSource === []) {
                return $name === '' || $implicit;
            }

            return in_array($name, $namesOfSource, true);
        };

        return [
            'local' => $selects($this->config->localNames),
            'oauth2' => array_values(array_filter(
                $this->config->oauthProviders,
                static fn (array $provider): bool => $selects($provider['names'])
            )),
            'ldap' => array_values(array_filter(
                $this->config->ldapServers,
                static fn (array $server): bool => $selects($server['names'])
            )),
        ];
    }

    /**
     * Whether an email+password form makes sense for this name at all (§2.2). A name
     * whose only sources are OAuth2 offers providers, and the page says so plainly
     * rather than accepting a password it cannot verify.
     */
    public function offersCredentialForm(string $selectedName): bool
    {
        $sources = $this->sourcesFor($selectedName);

        return $sources['local'] || $sources['ldap'] !== [];
    }

    /**
     * The OAuth2 providers this name offers. One of them continues directly into its
     * authorization-code flow; several are offered for individual selection
     * (REQ-AUTH-066), and none of them joins the credential race.
     *
     * @return list<array<string, string|int>>
     */
    public function oauthProvidersFor(string $selectedName): array
    {
        return $this->sourcesFor($selectedName)['oauth2'];
    }

    /** Sequence A step 1: the URL that starts this provider's own login. */
    public function beginOauthRedirect(array $provider, string $sourceName): string
    {
        return $this->oauth->begin($provider, $sourceName);
    }

    /** The provider a stored callback transaction belongs to (null if it was removed). */
    public function oauthProvider(int $index): ?array
    {
        return $this->oauth->provider($index);
    }

    /**
     * Sequence A steps 3–6: verify the callback, resolve the identity the provider
     * vouches for, and finalize through Sequence C with `source:"oauth2"`. The OAuth2
     * path is never challenged by the second factor — the identity provider owns its own
     * (DEV-AUTH-10) — so no pending state can arise here.
     *
     * @return array{user: array<mixed>, source_name: string}
     */
    public function completeOauthLogin(string $code, string $state): array
    {
        $identity = $this->oauth->complete($code, $state);

        $user = $this->finalize([
            'email' => $identity['email'],
            'source' => 'oauth2',
            'provider' => $identity['provider'],
            'source_name' => $identity['source_name'],
            'first_factor' => '',
        ], $this->loginBody($identity['email'], 'oauth2', $identity['source_name'], [
            'provider' => $identity['provider'],
        ]));

        return ['user' => $user, 'source_name' => $identity['source_name']];
    }

    /** @return list<array{kind: string, name: string}> */
    private function configSources(): array
    {
        // Config owns the parsing (it is configuration); Auth owns what it means.
        return $this->config->authSources;
    }
}
