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
     * The data-API client (§3 of API_Endpoints_Design.md) over this client's transport — one
     * transport for the process's outbound HTTP, as the front controller arranges it — and
     * with this client for the member's self-service token fetch (§8.6, REQ-API-102).
     */
    public function dataApi(): DataApi
    {
        return new DataApi($this->config, $this->logger, $this->request, $this->transport, $this);
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

    /**
     * A DELETE, optionally with a body: an analysis-mode deletion is acknowledged by the
     * same call returning with `acknowledge_breaking` in its body (API §4.21, REQ-API-111).
     *
     * @return array<mixed>
     */
    public function delete(string $path, array $query = [], ?array $body = null): array
    {
        return $this->call('DELETE', $path, $query, $body);
    }

    /**
     * A GET whose response body is passed to `$onChunk` as it arrives rather than collected
     * — the project export (REQ-TECH-011, `User_Interface_Design.md` §6.4). `$onStart` runs
     * once a 2xx status is known, before the first body byte, and receives the response
     * headers (names lower-cased); nothing has been handed on before it returns.
     *
     * A failure before streaming starts throws the API's error as an ApiException, exactly
     * like get(). A list value in `$query` is sent as a repeated parameter (`arm=1&arm=3`,
     * API §4.14), which is how the API reads it. Once the stream has started there is no
     * way left to report a failure to the browser; a transfer that breaks off is logged.
     *
     * @param array<string, scalar|list<scalar>> $query
     * @param callable(int, array<string, string>): void $onStart
     * @param callable(string): void $onChunk
     */
    public function stream(string $path, array $query, callable $onStart, callable $onChunk): void
    {
        $url = $this->config->apiBaseUrl . $path;
        $encoded = self::queryString($query);
        if ($encoded !== '') {
            $url .= '?' . $encoded;
        }

        $response = $this->transport->stream('GET', $url, $this->boundaryHeaders('*/*'),
            static function (int $status, array $headers) use ($onStart): bool {
                if ($status < 200 || $status >= 300) {
                    return false; // keep the error body to read it below
                }
                $onStart($status, $headers);

                return true;
            },
            $onChunk
        );

        if ($response['streamed']) {
            if (!$response['complete']) {
                $this->logger->error('api stream broke off', ['path' => $path, 'status' => $response['status']]);
            }

            return;
        }
        if ($response['status'] === 0) {
            $this->logger->error('api unreachable', ['method' => 'GET', 'path' => $path]);

            throw new ApiException('internal', 'the API is not reachable', 502);
        }

        $decoded = json_decode($response['body'], true);
        if (!is_array($decoded) || !isset($decoded['error'])) {
            $this->logger->error('api returned an unreadable error', [
                'method' => 'GET', 'path' => $path, 'status' => $response['status'],
            ]);
            $decoded = ['error' => 'internal', 'message' => ''];
        }
        $this->noteOperatorFailure($decoded, 'GET', $path);

        throw ApiException::fromBody($decoded, $response['status']);
    }

    /**
     * The query string with list values repeated under their own name (`arm=1&arm=3`) rather
     * than PHP's bracket form, RFC 3986 encoded.
     *
     * @param array<string, scalar|list<scalar>> $query
     */
    public static function queryString(array $query): string
    {
        $parts = [];
        foreach ($query as $name => $value) {
            foreach (is_array($value) ? $value : [$value] as $item) {
                $parts[] = rawurlencode((string) $name) . '=' . rawurlencode((string) $item);
            }
        }

        return implode('&', $parts);
    }

    /**
     * A refused service token is never the user's doing: the web layer and the API disagree on
     * INTERNAL_SERVICE_TOKEN. The user sees the generic line, and the operator is told in the
     * log (User_Interface_Design.md §3.4) — at error level, naming the variable, never its value.
     *
     * @param array<mixed> $decoded
     */
    private function noteOperatorFailure(array $decoded, string $method, string $path): void
    {
        if (($decoded['error'] ?? '') === 'service_token_invalid') {
            $this->logger->error('the API refused the service token — check INTERNAL_SERVICE_TOKEN on both components', [
                'method' => $method,
                'path' => $path,
            ]);
        }
    }

    /**
     * The headers every call carries across the internal boundary: the service token, the
     * caller's address (REQ-API-125) and — when one is authenticated — the acting user.
     *
     * @return list<string>
     */
    private function boundaryHeaders(string $accept): array
    {
        $headers = [
            'Accept: ' . $accept,
            'X-Internal-Service-Token: ' . $this->config->internalServiceToken,
            // Overwritten by nginx for externally-originated requests; forwarded
            // verbatim from the browser-facing request here (REQ-API-125).
            'X-Real-IP: ' . $this->request->clientIp(),
        ];

        // The acting user, when one is authenticated. Pre-authentication calls
        // (login, verify-password, the password-reset trio) are the API's own
        // exception set and carry no user header (DEV-API-16). A wizard call
        // made inside a tfa_pending state names the identity whose first factor
        // PHP has verified — that is the override, not a session (§2.7).
        $actor = $this->actorOverride ?? (Session::isAuthenticated() ? Session::userId() : null);
        if ($actor !== null) {
            $headers[] = 'X-Internal-User-Id: ' . $actor;
        }

        return $headers;
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

        $headers = $this->boundaryHeaders('application/json');
        if ($body !== null) {
            $headers[] = 'Content-Type: application/json';
        }

        // Every API body is a JSON object (§4.1); PHP encodes an empty array as `[]`, which the
        // API refuses as malformed — so an empty body goes out as `{}` (staging start, a dry run
        // of the stored expression).
        $encoded = $body === null ? null : ($body === [] ? '{}' : (string) json_encode($body));
        $response = $this->transport->request($method, $url, $headers, $encoded);
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
            $this->noteOperatorFailure($decoded, $method, $path);

            throw ApiException::fromBody($decoded, $status);
        }

        if ($status === 204 || $decoded === null) {
            return [];
        }

        return $decoded;
    }
}
