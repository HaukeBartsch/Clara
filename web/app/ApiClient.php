<?php
// The only route from the web layer to the Go API (REQ-TECH-006: PHP holds no
// SQL and no database driver — every read and write goes through here).
//
// Identity on the internal boundary: X-Internal-Service-Token proves the caller
// is this application, and X-Internal-User-Id names the acting user. The header
// is authoritative inside the API and PHP adds nothing else
// (API_Endpoints_Design.md §4.1); nginx strips both from any externally
// originated request (Technology_Stack_Design.md §5). The caller's address is
// forwarded as X-Real-IP so the API's rate limiter sees a distinct source per
// user rather than one per web server (REQ-API-125).
//
// Two behaviours of the API are easy to get wrong from here and are worth
// stating once (Plan/Web_Implementation.md §7 rules 1 and 12):
//   * administration writes answer 400 on an attribute outside their whitelist —
//     send exactly the names the endpoint accepts, per area;
//   * statuses are not uniform: DELETE answers 204, creates 201 (the member add
//     carries its one-time token there), most mutations 200. Callers branch on
//     the code they get, never on "was it 200".

declare(strict_types=1);

namespace Clara;

final class ApiClient
{
    public function __construct(
        private readonly Config $config,
        private readonly Logger $logger,
        private readonly Request $request,
        private readonly Transport $transport = new CurlTransport(),
        /**
         * Names the acting user for a call made *before* a session stands — the
         * two-factor enrollment wizard, which the API lets PHP drive for an
         * identity whose first factor it just verified
         * (Authentication_Authorization_Design.md §2.7, REQ-API-115). Set only
         * through asUser(); null means "the session's user, if any".
         */
        private readonly ?int $actorOverride = null
    ) {}

    /**
     * The same client speaking for one pre-authentication identity. Returns a
     * client rather than mutating this one: the override must not be able to
     * outlive the wizard call it belongs to, and an authenticated request must
     * never find itself speaking as somebody else.
     */
    public function asUser(int $userId): self
    {
        return new self($this->config, $this->logger, $this->request, $this->transport, $userId);
    }

    /**
     * @param array<string, scalar|null> $query
     * @return array<mixed> decoded JSON body; [] for a 204
     */
    public function get(string $path, array $query = []): array
    {
        return $this->call('GET', $path, $query, null);
    }

    /** @return array<mixed> */
    public function post(string $path, array $body = [], array $query = []): array
    {
        return $this->call('POST', $path, $query, $body);
    }

    /** @return array<mixed> */
    public function put(string $path, array $body = [], array $query = []): array
    {
        return $this->call('PUT', $path, $query, $body);
    }

    /** @return array<mixed> */
    public function delete(string $path, array $query = []): array
    {
        return $this->call('DELETE', $path, $query, null);
    }

    /**
     * Performs one call and decodes the envelope. Any status ≥ 400 becomes an
     * ApiException carrying the API's stable error code, so callers branch on
     * `code()` — the status alone is not enough (login's two-factor states are
     * 401s that are not failures).
     *
     * @param array<string, scalar|null> $query
     * @param array<mixed>|null          $body
     * @return array<mixed>
     */
    private function call(string $method, string $path, array $query, ?array $body): array
    {
        $url = $this->config->apiBaseUrl . $path;
        if ($query !== []) {
            $url .= (str_contains($url, '?') ? '&' : '?') . http_build_query($query);
        }

        $headers = [
            'Accept: application/json',
            'X-Internal-Service-Token: ' . $this->config->internalServiceToken,
            // Overwritten by nginx for externally-originated requests; forwarded
            // verbatim from the browser-facing request here (REQ-API-125).
            'X-Real-IP: ' . $this->request->clientIp(),
        ];
        if ($body !== null) {
            $headers[] = 'Content-Type: application/json';
        }

        // The acting user, when one is authenticated. Pre-authentication calls
        // (login, verify-password, the password-reset trio) are the API's own
        // exception set and carry no user header (DEV-API-16). A wizard call
        // made inside a tfa_pending state names the identity whose first factor
        // PHP has verified — that is the override, not a session (§2.7).
        $actor = $this->actorOverride ?? (Session::isAuthenticated() ? Session::userId() : null);
        if ($actor !== null) {
            $headers[] = 'X-Internal-User-Id: ' . $actor;
        }

        $response = $this->transport->request($method, $url, $headers, $body === null ? null : (string) json_encode($body));
        $status = $response['status'];

        if ($status === 0) {
            // The API is unreachable: an operator problem, never a detail to show.
            $this->logger->error('api unreachable', ['method' => $method, 'path' => $path]);

            throw new ApiException('internal', 'the API is not reachable', 502);
        }

        $decoded = null;
        if ($response['body'] !== '') {
            $parsed = json_decode($response['body'], true);
            $decoded = is_array($parsed) ? $parsed : null;
        }

        if ($status >= 400) {
            // A non-JSON failure body means something between us and the API
            // answered (a proxy, an FPM error): log it, present it as internal.
            if ($decoded === null || !isset($decoded['error'])) {
                $this->logger->error('api returned an unreadable error', [
                    'method' => $method,
                    'path' => $path,
                    'status' => $status,
                ]);
                $decoded = ['error' => 'internal', 'message' => ''];
            }

            throw ApiException::fromBody($decoded, $status);
        }

        if ($status === 204 || $decoded === null) {
            return [];
        }

        return $decoded;
    }
}
