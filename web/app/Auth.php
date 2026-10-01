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
     * The local credential path. Throws ApiException with the API's code on any
     * rejection — bad_password, account_disabled, account_expired, rate_limited,
     * or a flow state (mfa_required / tfa_enrollment_required) that the caller
     * must not present as a password failure.
     *
     * Two calls, deliberately: verify-password is side-effect-free, and login is
     * the only place login side effects happen (§2.6 step 3), which keeps M1's
     * parallel LDAP attempts from producing a second session or a duplicate
     * audit success.
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

        return $this->api->post('/api/v1/auth/login', $body);
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
