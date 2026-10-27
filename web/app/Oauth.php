<?php
// The web layer owns the browser session and callback state. Discovery, authorization
// URL construction, PKCE exchange and ID-token verification live in the Go API
// (Authentication_Authorization_Design.md §2.1/§2.10, REQ-AUTH-076/077).

declare(strict_types=1);

namespace Clara;

final class Oauth
{
    public function __construct(
        private readonly ApiClient $api,
        private readonly Config $config,
        private readonly Logger $logger
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
     * Sequence A step 1: the API builds the authorization URL and creates the
     * state, PKCE verifier and nonce; PHP retains them only in this browser session.
     */
    public function begin(array $provider, string $sourceName, string $purpose = 'login'): string
    {
        $flow = $this->api->post('/api/v1/auth/oauth/start', [
            'provider' => (int) $provider['index'],
        ]);
        if (!isset($flow['authorize_url'], $flow['state'], $flow['code_verifier'], $flow['nonce'])
            || !is_string($flow['authorize_url']) || !is_string($flow['state'])
            || !is_string($flow['code_verifier']) || !is_string($flow['nonce'])) {
            $this->logger->error('OAuth initiation returned an incomplete response', [
                'provider' => $provider['issuer'],
            ]);
            throw new ApiException('provider_unavailable', '', 502);
        }

        Session::beginOauthTransaction([
            'provider_index' => (int) $provider['index'],
            'state' => $flow['state'],
            'code_verifier' => $flow['code_verifier'],
            'nonce' => $flow['nonce'],
            'source_name' => $sourceName,
            'purpose' => $purpose,
        ]);

        return $flow['authorize_url'];
    }

    /**
     * Sends the authorization code to the API for PKCE exchange and assertion
     * verification, then returns the API's authenticated user object.
     *
     * @return array{user: array<mixed>, source_name: string}
     */
    public function complete(string $code, string $state): array
    {
        $txn = $this->takeTransaction($code, $state, 'login');
        $provider = $this->provider((int) $txn['provider_index']);
        if ($provider === null) {
            throw new ApiException('state_mismatch', '', 401);
        }

        $user = $this->api->post('/api/v1/auth/login', [
            'source' => 'oauth2',
            'provider' => $provider['issuer'],
            'source_name' => $txn['source_name'],
            'authorization_code' => $code,
            'code_verifier' => $txn['code_verifier'],
            'assertion_nonce' => $txn['nonce'],
        ]);

        return ['user' => $user, 'source_name' => $txn['source_name']];
    }

    /**
     * Completes the Control Panel diagnostic flow without establishing a login.
     *
     * @return array<string, mixed>
     */
    public function completeTest(string $code, string $state): array
    {
        $txn = $this->takeTransaction($code, $state, 'auth_test');
        $report = $this->api->post('/api/v1/auth/oauth/test', [
            'provider' => (int) $txn['provider_index'],
            'code' => $code,
            'code_verifier' => $txn['code_verifier'],
            'nonce' => $txn['nonce'],
        ]);

        return $report;
    }

    /** Consume state whether the provider returned a code or an authorization error. */
    public function cancel(string $state): void
    {
        $txn = Session::takeOauthTransaction();
        if ($txn === [] || !hash_equals((string) $txn['state'], $state)) {
            $this->logger->warn('oauth callback rejected', ['reason' => 'state_mismatch']);
        }
    }

    /**
     * @return array{provider_index: int, state: string, code_verifier: string, nonce: string, source_name: string, purpose: string}
     */
    private function takeTransaction(string $code, string $state, string $purpose): array
    {
        $txn = Session::takeOauthTransaction();
        if ($txn === [] || ($txn['purpose'] ?? 'login') !== $purpose || $code === ''
            || !hash_equals((string) $txn['state'], $state)) {
            $this->logger->warn('oauth callback rejected', ['reason' => 'state_mismatch']);
            throw new ApiException('state_mismatch', '', 401);
        }

        return $txn;
    }
}
