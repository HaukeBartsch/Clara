<?php
// Sign-in for registered applications — Sequence K
// (`Authentication_Authorization_Design.md` §2.13, REQ-AUTH-092..096).
//
// Another application on the same site (Fiona is the first) sends its user here instead of
// asking for a password itself. The browser half is an authorization-code request with PKCE:
//
//   1. GET /sso/authorize — the client's id, its registered return address, a PKCE challenge
//      and its `state`. A user who is not signed in to CLARA signs in first, the whole request
//      riding along as `next`; one who is gets a code at once.
//   2. The browser returns to the client with `code` and `state`.
//   3. POST /sso/token — the client's server redeems the code with its secret and the PKCE
//      verifier, and receives a grant and the user it names.
//   4. POST /sso/check — every few minutes the client's server asks whether the grant still
//      stands. Signing out of CLARA, a disabled or expired account and a revoked session all
//      end it; the client signs its user out at the next check.
//
// The API holds every rule (`/api/v1/sso/*`, API §4.31): this file only checks what it must
// before it lets the browser leave — that the client is registered and the return address is
// exactly the registered one — and relays the rest. A request that fails that check never
// goes back to the address it names: that would be an open redirect (REQ-AUTH-093).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Auth;
use Clara\Messages;
use Clara\Response;
use Clara\Session;

final class SsoController extends Controller
{
    /** The PKCE S256 challenge: 32 bytes of SHA-256, base64url without padding. */
    private const CHALLENGE = '/^[A-Za-z0-9_-]{43}$/';

    /** The client's `state`: opaque to CLARA, printable ASCII, bounded. */
    private const STATE = '/^[\x21-\x7E]{1,512}$/';

    /** GET /sso/authorize — Sequence K steps 1–2. */
    public function authorize(): Response
    {
        $clientId = $this->request->query('client_id');
        $redirectUri = $this->request->query('redirect_uri');
        $client = $this->config->ssoClient($clientId);

        if ($client === null || $redirectUri === '' || !hash_equals($client['redirect_uri'], $redirectUri)) {
            $reason = $client === null ? 'unknown_client' : 'redirect_uri_mismatch';
            $this->logger->warn('client sign-in request refused', [
                'client' => substr($clientId, 0, 64),
                'reason' => $reason,
            ]);

            return $this->refusal($reason, $client);
        }

        // From here on the return address is the registered one, so a malformed request is
        // answered there, the way the client expects to hear about it.
        $state = $this->request->query('state');
        if (preg_match(self::STATE, $state) !== 1) {
            return $this->backToClient($client, ['error' => 'invalid_request']);
        }
        if ($this->request->query('response_type') !== 'code') {
            return $this->backToClient($client, ['error' => 'unsupported_response_type', 'state' => $state]);
        }
        $challenge = $this->request->query('code_challenge');
        if ($this->request->query('code_challenge_method') !== 'S256' || preg_match(self::CHALLENGE, $challenge) !== 1) {
            return $this->backToClient($client, ['error' => 'invalid_request', 'state' => $state]);
        }
        $silent = $this->request->query('prompt') === 'none';

        // Not signed in, or a second factor still pending: sign in first. The resumed request
        // is rebuilt from the checked values only, so `next` carries nothing else.
        $resume = '/sso/authorize?' . http_build_query([
            'response_type' => 'code',
            'client_id' => $client['id'],
            'redirect_uri' => $client['redirect_uri'],
            'code_challenge' => $challenge,
            'code_challenge_method' => 'S256',
            'state' => $state,
        ], '', '&', PHP_QUERY_RFC3986);
        $denied = Auth::guard('login', $this->config, $resume);
        if ($denied !== null) {
            // `prompt=none` asks only whether the user is signed in already: the client
            // offers its own sign-in when the answer is no (REQ-AUTH-095).
            return $silent
                ? $this->backToClient($client, ['error' => 'login_required', 'state' => $state])
                : $denied;
        }

        try {
            $issued = $this->api->post('/api/v1/sso/codes', [
                'client_id' => $client['id'],
                'redirect_uri' => $client['redirect_uri'],
                'code_challenge' => $challenge,
                'code_challenge_method' => 'S256',
                'sid' => Session::ssoSid(),
                'session_issued_at' => Session::issuedAt(),
            ]);
        } catch (ApiException $e) {
            // `reason`, not `code`: the logger drops every `code` value as a possible secret.
            $this->logger->warn('client sign-in code refused', [
                'client' => $client['id'],
                'reason' => $e->code(),
                'status' => (string) $e->status(),
            ]);

            return match (true) {
                // The API's registration and this one disagree: the same .env was not read
                // by both. Nothing goes to an address the API does not know.
                in_array($e->code(), ['invalid_client', 'invalid_redirect_uri'], true) => $this->refusal($e->code(), $client),
                $e->code() === 'invalid_request' => $this->backToClient($client, ['error' => 'invalid_request', 'state' => $state]),
                $e->status() >= 500 => $this->backToClient($client, ['error' => 'temporarily_unavailable', 'state' => $state]),
                default => $this->backToClient($client, ['error' => 'access_denied', 'state' => $state]),
            };
        }

        $this->logger->info('client sign-in code issued', ['client' => $client['id']]);

        return $this->backToClient($client, ['code' => (string) ($issued['code'] ?? ''), 'state' => $state]);
    }

    /**
     * POST /sso/token — Sequence K step 3, called by the client's server. The fields are
     * form-encoded as in an OAuth2 token request; the client may present its credentials as
     * HTTP Basic instead of fields.
     */
    public function token(): Response
    {
        $grantType = $this->request->field('grant_type');
        if ($grantType !== '' && $grantType !== 'authorization_code') {
            return Response::json(['error' => 'unsupported_grant_type'], 400);
        }
        [$id, $secret] = $this->clientCredentials();

        return $this->relay('/api/v1/sso/token', [
            'client_id' => $id,
            'client_secret' => $secret,
            'code' => $this->request->field('code'),
            'code_verifier' => $this->request->field('code_verifier'),
            'redirect_uri' => $this->request->field('redirect_uri'),
        ]);
    }

    /** POST /sso/check — Sequence K step 4: does the grant still stand? */
    public function check(): Response
    {
        [$id, $secret] = $this->clientCredentials();

        return $this->relay('/api/v1/sso/check', [
            'client_id' => $id,
            'client_secret' => $secret,
            'grant' => $this->request->field('grant'),
        ]);
    }

    /**
     * GET /sso/logout?client_id=… — the client's "sign out" link. It only asks: a GET that
     * signed out could be fired by any page that embeds it (REQ-AUTH-094, REQ-UI-083). Nobody signed in
     * means nothing to ask, and the browser goes straight on.
     */
    public function logoutForm(): Response
    {
        $client = $this->config->ssoClient($this->request->query('client_id'));
        if (!Session::isAuthenticated()) {
            return Response::redirect($this->afterLogout($client));
        }

        return $this->standalone('sso/logout', [
            'client_id' => $client['id'] ?? '',
            'client_name' => $client['name'] ?? '',
            'stay_href' => $client !== null && $client['logout_uri'] !== '' ? $client['logout_uri'] : '/',
        ], ['titleKey' => 'sso.logout.title']);
    }

    /**
     * POST /sso/logout — Sequence D for the whole sign-in: CLARA's session and every client
     * grant issued under it end (Auth::logout), then the browser returns to the client's
     * registered sign-out address, or to CLARA's sign-in page.
     */
    public function logout(): Response
    {
        $client = $this->config->ssoClient($this->request->field('client_id'));
        if (Session::isAuthenticated()) {
            // With single sign-out the provider's sign-out comes first, and its return to
            // /auth/logged-out carries on to the client's address (Sequence L1 step 2).
            $returnTo = $client !== null ? $client['logout_uri'] : '';
            $outcome = $this->auth->logout($returnTo);
            if ($outcome['provider_url'] !== '') {
                return Response::redirect($outcome['provider_url']);
            }
            if ($outcome['provider_failed'] && $returnTo === '' && $this->config->logoutLandingUrl === '') {
                Session::flash('warning', $this->i18n->t('logout.provider_may_be_active'));
            }
        }

        return Response::redirect($this->afterLogout($client));
    }

    /**
     * The client's registered return address with the answer appended. Only ever called
     * with a registered client, so the address is one an administrator configured.
     *
     * @param array{redirect_uri: string} $client
     * @param array<string, string>        $params
     */
    private function backToClient(array $client, array $params): Response
    {
        $params = array_filter($params, static fn (string $value): bool => $value !== '');
        $uri = $client['redirect_uri'];

        return Response::redirect($uri . (str_contains($uri, '?') ? '&' : '?')
            . http_build_query($params, '', '&', PHP_QUERY_RFC3986));
    }

    /**
     * The page for a request CLARA cannot answer at the address it names (REQ-AUTH-093,
     * REQ-UI-083). In development the detail says which setting to look at.
     *
     * @param array{index: int}|null $client
     */
    private function refusal(string $reason, ?array $client): Response
    {
        $detail = '';
        if ($this->config->isDevelopment) {
            $detail = match ($reason) {
                'unknown_client' => 'client_id is not one of the SSO_CLIENT_n_ID settings',
                'redirect_uri_mismatch' => 'redirect_uri is not exactly SSO_CLIENT_' . ($client['index'] ?? 0) . '_REDIRECT_URI',
                default => 'the API refused the request: ' . $reason,
            };
        }

        $page = $this->standalone('errors/server', [
            'title' => $this->i18n->t('sso.refused.title'),
            'body' => $this->i18n->t('sso.refused.body'),
            'ref_line' => Messages::referenceLine($this->i18n, $this->request->ref()),
            'detail' => $detail,
            'back_href' => '/',
            'back_label' => $this->i18n->t('sso.refused.back'),
        ], ['titleKey' => 'sso.refused.title']);

        return Response::html($page->body(), 400);
    }

    /**
     * The client's id and secret: HTTP Basic when the request carries it (RFC 6749 §2.3.1,
     * each part form-encoded), the `client_id` and `client_secret` fields otherwise.
     *
     * @return array{0: string, 1: string}
     */
    private function clientCredentials(): array
    {
        $header = trim((string) $this->request->header('authorization'));
        if (preg_match('/^Basic\s+([A-Za-z0-9+\/]+={0,2})$/i', $header, $m) === 1) {
            $decoded = base64_decode($m[1], true);
            if (is_string($decoded) && str_contains($decoded, ':')) {
                [$id, $secret] = explode(':', $decoded, 2);

                return [urldecode($id), urldecode($secret)];
            }
        }

        return [$this->request->field('client_id'), $this->request->field('client_secret')];
    }

    /**
     * One server-to-server call relayed: the API's answer as it came, or its refusal as
     * `{"error": …}` with the API's status. An API that cannot answer is the client's cue to
     * try again, never a reason to sign its user out.
     *
     * @param array<string, string> $body
     */
    private function relay(string $path, array $body): Response
    {
        try {
            return Response::json($this->api->post($path, $body));
        } catch (ApiException $e) {
            $this->logger->info('client call refused', [
                'path' => $path,
                'reason' => $e->code(),
                'status' => (string) $e->status(),
            ]);
            if ($e->status() >= 500) {
                return Response::json(['error' => 'temporarily_unavailable'], 503);
            }

            return Response::json(['error' => $e->code()], $e->status());
        }
    }

    /** @param array{logout_uri: string}|null $client */
    private function afterLogout(?array $client): string
    {
        // A registered client's own address wins; LOGOUT_LANDING_URL replaces only the
        // login-page default (REQ-CFG-043).
        return $client !== null && $client['logout_uri'] !== '' ? $client['logout_uri'] : $this->config->afterSignOut();
    }
}
