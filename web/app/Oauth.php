<?php
// Sequence A — the OAuth2 authorization-code round trip
// (Authentication_Authorization_Design.md §2.1, REQ-AUTH-001/002/066). PHP owns this
// flow entirely: it generates `state` and the PKCE verifier, holds them in the session
// across the two browser hops, exchanges the code at the provider's token endpoint
// server-side over TLS, and reads the account address out of the userinfo response (or
// the ID token) under the configured claim (REQ-AUTH-004). The API sees none of it: it
// gets the ordinary Sequence C login call naming the identity that came back.
//
// What never leaves this file: the client secret, the authorization code, the access
// token and the ID token. Each is used for one request and then dropped — not stored,
// not logged, not rendered (REQ-AUTH-036). And `state` leaves the session exactly once:
// takeOauthTransaction() consumes it before anything is decided, so a callback URL
// somebody captured cannot be presented twice (§2.1 step 3).

declare(strict_types=1);

namespace Clara;

final class Oauth
{
    /**
     * The scope requested of every provider. No variable configures it
     * (`System_Configuration_Design.md` §3.4 fixes the OAuth2 set), so the generic
     * minimum for "give me this person's address" is fixed here: `openid` for the ID
     * token, `email` and `profile` for the userinfo claims Sequence A reads. Adding a
     * per-provider scope is a configuration-level change, not something to invent in
     * PHP.
     */
    private const SCOPE = 'openid email profile';

    /** Discovery results per issuer, for the life of this object (one request). */
    private array $endpointsCache = [];

    public function __construct(
        private readonly Config $config,
        private readonly Logger $logger,
        private readonly Transport $transport = new CurlTransport()
    ) {}

    /** The configured provider with this index, or null when there is none. */
    public function provider(int $index): ?array
    {
        foreach ($this->config->oauthProviders as $provider) {
            if ($provider['index'] === $index) {
                return $provider;
            }
        }

        return null;
    }

    /**
     * Sequence A step 1: remember `state` and the PKCE verifier against this browser
     * and produce the URL that starts the provider's own login. The caller turns it
     * into a redirect — nothing about the user is decided until the callback returns.
     */
    public function begin(array $provider, string $sourceName): string
    {
        // 16 random bytes in hex, the form §2.1 step 1 fixes for `state`.
        $state = bin2hex(random_bytes(16));
        // RFC 7636 §4.1 allows 43..128 characters of [A-Za-z0-9-_]; 48 random bytes,
        // base64url-encoded, is 64 of them.
        $verifier = rtrim(strtr(base64_encode(random_bytes(48)), '+/', '-_'), '=');

        Session::beginOauthTransaction([
            'provider_index' => (int) $provider['index'],
            'state' => $state,
            'code_verifier' => $verifier,
            'source_name' => $sourceName,
        ]);

        return $this->endpoints($provider)['authorize'] . '?' . http_build_query([
            'response_type' => 'code',
            'client_id' => $provider['client_id'],
            'redirect_uri' => $this->redirectUri($provider),
            'scope' => self::SCOPE,
            'state' => $state,
            'code_challenge' => $this->challenge($verifier),
            'code_challenge_method' => 'S256',
        ]);
    }

    /**
     * Sequence A steps 3–5. Verifies `state` against the session (single use),
     * exchanges the code, and resolves the identity to an address.
     *
     * Throws ApiException with `state_mismatch` — the round trip was not this
     * browser's, or is no longer open — or `provider_unavailable` for anything the
     * provider side could not complete; both map to a translated line (§2.2).
     *
     * @return array{email: string, display_name: string, provider: string, source_name: string}
     */
    public function complete(string $code, string $state): array
    {
        // Read first, decide after: the transaction is spent whether what follows
        // succeeds or fails, which is what makes `state` single-use (§2.1 step 3).
        $txn = Session::takeOauthTransaction();
        $provider = $txn === [] ? null : $this->provider((int) $txn['provider_index']);

        if ($txn === [] || $provider === null || $code === '' || !hash_equals($txn['state'], $state)) {
            // One answer for "never started", "already used", "timed out" and "differs"
            // — the differences are not actionable, and one of them is an attack.
            $this->logger->warn('oauth callback rejected', ['reason' => 'state_mismatch']);

            throw new ApiException('state_mismatch', '', 401);
        }

        $tokens = $this->exchange($provider, $txn['code_verifier'], $code);
        $claims = $this->identity($provider, $tokens);

        return [
            'email' => $claims['email'],
            'display_name' => $claims['display_name'],
            // Sequence C names the concrete winning source (§2.3).
            'provider' => $provider['issuer'],
            'source_name' => $txn['source_name'],
        ];
    }

    // --- the provider's side ---------------------------------------------------

    /**
     * The token-endpoint exchange (Sequence A step 4), server-side over TLS.
     *
     * @return array<mixed> the token response
     */
    private function exchange(array $provider, string $verifier, string $code): array
    {
        $response = $this->transport->request(
            'POST',
            $this->endpoints($provider)['token'],
            ['Accept: application/json', 'Content-Type: application/x-www-form-urlencoded'],
            http_build_query([
                'grant_type' => 'authorization_code',
                'code' => $code,
                'redirect_uri' => $this->redirectUri($provider),
                'client_id' => $provider['client_id'],
                'client_secret' => $provider['client_secret'],
                'code_verifier' => $verifier,
            ])
        );

        $tokens = is_string($response['body']) && $response['body'] !== ''
            ? json_decode($response['body'], true)
            : null;

        if ($response['status'] < 200 || $response['status'] >= 300 || !is_array($tokens)
            || trim((string) ($tokens['access_token'] ?? '')) === '') {
            // The provider's own text can name the client, the code or the redirect —
            // it goes to the log, never to the page (REQ-API-006), and no token in it
            // is ever logged either (REQ-AUTH-036).
            $this->logger->warn('oauth token exchange failed', [
                'provider' => $provider['issuer'],
                'status' => $response['status'],
            ]);

            throw new ApiException('provider_unavailable', '', 502);
        }

        return $tokens;
    }

    /**
     * The address the provider vouches for (Sequence A step 5, REQ-AUTH-004).
     *
     * The userinfo endpoint is asked first: it returns what the issuer says about the
     * holder of that access token, fetched over the same TLS channel that just
     * delivered the token. The ID token is the documented alternative and serves as
     * the fallback when userinfo is absent or withholds the claim — its signature is
     * not verified here, which is sound only because it arrives directly from the
     * issuer over TLS on a channel already bound to this browser by PKCE and the
     * client credentials. Verifying it would mean fetching and caching the issuer's
     * JWKS: an addition worth making if a deployment ever needs to distrust that
     * channel, and a configuration-level decision rather than a PHP default.
     *
     * @param array<mixed> $tokens
     * @return array{email: string, display_name: string}
     */
    private function identity(array $provider, array $tokens): array
    {
        $claim = $provider['email_attr'];
        $sets = [
            $this->userInfo($provider, (string) $tokens['access_token']),
            $this->idTokenClaims((string) ($tokens['id_token'] ?? '')),
        ];

        foreach ($sets as $claims) {
            $email = strtolower(trim((string) ($claims[$claim] ?? '')));
            if ($email !== '') {
                return [
                    'email' => $email,
                    'display_name' => trim((string) ($claims['name'] ?? '')),
                ];
            }
        }

        // The provider answered and named no address for the configured claim: there
        // is nothing to log in as, and no account to disclose anything about.
        $this->logger->warn('oauth identity incomplete', [
            'provider' => $provider['issuer'],
            'claim' => $claim,
        ]);

        throw new ApiException('provider_unavailable', '', 502);
    }

    /** @return array<mixed> the userinfo response, or [] when it cannot be read */
    private function userInfo(array $provider, string $accessToken): array
    {
        if ($accessToken === '') {
            return [];
        }

        $response = $this->transport->request(
            'GET',
            $this->endpoints($provider)['userinfo'],
            ['Accept: application/json', 'Authorization: Bearer ' . $accessToken],
            null
        );

        if ($response['status'] < 200 || $response['status'] >= 300) {
            return [];
        }

        $claims = json_decode((string) $response['body'], true);

        return is_array($claims) ? $claims : [];
    }

    /**
     * The payload of a JWT, as claims. Unpackable or absent input yields []: the
     * caller then has no address, which is the same outcome as an empty claim.
     *
     * @return array<mixed>
     */
    private function idTokenClaims(string $idToken): array
    {
        $parts = explode('.', $idToken);
        if (count($parts) !== 3) {
            return [];
        }

        $payload = base64_decode(strtr($parts[1], '-_', '+/'), true);
        if ($payload === false) {
            return [];
        }

        $claims = json_decode($payload, true);

        return is_array($claims) ? $claims : [];
    }

    /**
     * The provider's endpoints. ASM-AUTH-1 fixes that the installation configures the
     * issuer root and derives authorize/token/userinfo from it; reading them from the
     * OpenID Connect discovery document is how that stays generic across providers
     * whose paths differ, without teaching this file a quirk per vendor. A provider
     * that publishes no document falls back to the conventional paths under the
     * issuer.
     *
     * @return array{authorize: string, token: string, userinfo: string}
     */
    private function endpoints(array $provider): array
    {
        $issuer = $provider['issuer'];
        if (isset($this->endpointsCache[$issuer])) {
            return $this->endpointsCache[$issuer];
        }

        $document = $this->transport->request(
            'GET',
            $issuer . '/.well-known/openid-configuration',
            ['Accept: application/json'],
            null
        );
        $discovered = $document['status'] >= 200 && $document['status'] < 300
            ? (json_decode((string) $document['body'], true) ?: [])
            : [];

        $authorize = trim((string) ($discovered['authorization_endpoint'] ?? ''));
        $token = trim((string) ($discovered['token_endpoint'] ?? ''));

        if ($authorize === '' || $token === '') {
            // The conventional derivation, which is what ASM-AUTH-1 describes for a
            // provider that does not publish the document.
            $authorize = $issuer . '/authorize';
            $token = $issuer . '/token';
        }

        return $resolved[$issuer] = [
            'authorize' => $authorize,
            'token' => $token,
            'userinfo' => trim((string) ($discovered['userinfo_endpoint'] ?? '')) !== ''
                ? trim((string) $discovered['userinfo_endpoint'])
                : $issuer . '/userinfo',
        ];
    }

    /** The redirect URI registered for this provider (REQ-CFG-011's default). */
    private function redirectUri(array $provider): string
    {
        return $provider['redirect_uri'] !== ''
            ? $provider['redirect_uri']
            : $this->config->webPublicUrl . '/auth/callback';
    }

    /** RFC 7636 §4.2: the S256 challenge for a verifier. */
    private function challenge(string $verifier): string
    {
        return rtrim(strtr(base64_encode(hash('sha256', $verifier, true)), '+/', '-_'), '=');
    }
}
