<?php
// The data API (`/api/`, API_Endpoints_Design.md §3) as the web layer uses it: UI data
// entry presents the acting member's own project token to it, exactly as an external
// script would (ASM-API-3, User_Interface_Design.md §8.6). Plan/Web_Implementation.md §4
// names this file: form encoding of import rows (`data[i][key]`), delete, and the token
// discipline around them.
//
// It differs from the administration API in every way that matters here, which is why it
// is a client of its own rather than a mode of ApiClient:
//   * the request is `application/x-www-form-urlencoded` (§3.1), never JSON;
//   * the credential is the `token` parameter in the body — never a header (REQ-API-010,
//     REQ-AUTH-031) — and no X-Internal-* header is sent: the data API trusts the token
//     alone;
//   * failures carry `{"error": "<text>"}` (§3.2), not the `{error, message, status}` code
//     envelope, so the text is mapped onto the stable codes the rest of the layer uses;
//   * unknown parameters are dropped rather than refused (REQ-API-017).
//
// Token discipline (§8.6, Plan §7 rule 7): the member's token comes from the self-service
// fetch `GET …/users/{uid}/token` (REQ-API-102), is cached in the session per project, and
// on a 401 `Invalid token` — rotated or revoked since it was cached (REQ-AUTH-030) — is
// discarded, fetched again and the call retried exactly once. A 403 is a permission result
// and is never retried. The token never reaches a log line or the browser (REQ-API-005).

declare(strict_types=1);

namespace Clara;

final class DataApi
{
    public function __construct(
        private readonly Config $config,
        private readonly Logger $logger,
        private readonly Request $request,
        private readonly Transport $transport,
        private readonly ApiClient $api
    ) {}

    /**
     * One data-API call with the acting member's token for the project, applying the cache
     * and the single stale-token retry of §8.6. `$params` are the call's parameters without
     * the token; `format`/`returnFormat` default to JSON.
     *
     * @param array<string, string|list<string>|array<int, array<string, string>>> $params
     * @return array<mixed> the decoded JSON response
     */
    public function forMember(int $projectId, array $params): array
    {
        $token = Session::cachedProjectToken($projectId) ?? $this->fetchToken($projectId);

        try {
            return $this->call($token, $params);
        } catch (ApiException $e) {
            if ($e->code() !== 'invalid_token') {
                throw $e;
            }
        }

        // The cached token is dead (rotated or revoked, REQ-AUTH-030): forget it, fetch the
        // current one, and try once more. A second 401 is the answer, not a reason to loop.
        Session::forgetProjectToken($projectId);
        $this->logger->info('project token refreshed after 401', ['project' => $projectId]);

        return $this->call($this->fetchToken($projectId), $params);
    }

    /**
     * Performs one call with an explicit token. Public for the survey page (§8.8), whose
     * credential is the link token rather than a member's.
     *
     * @param array<string, mixed> $params
     * @return array<mixed>
     */
    public function call(string $token, array $params): array
    {
        $body = self::encode(['token' => $token, 'format' => 'json', 'returnFormat' => 'json'] + $params);
        $headers = [
            'Accept: application/json',
            'Content-Type: application/x-www-form-urlencoded',
            // The rate limiter counts per caller, so the browser's address travels with the
            // call as on the administration API (REQ-API-125).
            'X-Real-IP: ' . $this->request->clientIp(),
        ];

        $response = $this->transport->request('POST', $this->config->apiBaseUrl . '/api/', $headers, $body);
        $status = $response['status'];
        $content = (string) ($params['content'] ?? '');

        if ($status === 0) {
            $this->logger->error('data api unreachable', ['content' => $content]);

            throw new ApiException('internal', 'the API is not reachable', 502);
        }

        $decoded = json_decode($response['body'], true);
        if ($status >= 400) {
            $text = is_array($decoded) && is_string($decoded['error'] ?? null) ? $decoded['error'] : '';
            $code = self::codeFor($status, $text);
            if ($code === 'internal') {
                $this->logger->error('data api failure', ['content' => $content, 'status' => $status]);
            }

            throw new ApiException($code, $text, $status);
        }
        if (!is_array($decoded)) {
            $this->logger->error('data api returned an unreadable body', ['content' => $content, 'status' => $status]);

            throw new ApiException('internal', '', 502);
        }

        return $decoded;
    }

    /**
     * The §3.2 error texts onto the stable codes Messages and the controllers branch on.
     * The status decides first; the text separates the two 403s, because "analysis mode"
     * is a state of the project the page explains, not a permission the user lacks.
     */
    public static function codeFor(int $status, string $text): string
    {
        return match (true) {
            $status === 401 => 'invalid_token',
            // A link that already carried its one submission (REQ-API-145): told apart from an
            // invalid token on purpose, because "your answer arrived" is the respondent's answer.
            $status === 410 => 'survey_submitted',
            $status === 403 && stripos($text, 'analysis mode') !== false => 'analysis_mode',
            $status === 403 => 'forbidden',
            $status === 429 => 'rate_limited',
            $status === 400 => 'invalid_request',
            default => 'internal',
        };
    }

    /**
     * Form encoding with REDCap's array syntax: `data[0][age]=42`, `records[0]=X` (§3.1,
     * §3.7.1). http_build_query writes exactly that for nested arrays, with RFC 3986
     * percent-encoding so a `+` in a stored offset (`2026-03-01+01:00`) survives.
     *
     * @param array<string, mixed> $params
     */
    public static function encode(array $params): string
    {
        return http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    }

    /**
     * The member's own token via the self-service fetch (REQ-API-102), cached for the
     * session. A 403 here means the acting user is not a member of the project (an
     * administrator may see a project without belonging to it), which is reported as
     * `not_member` so the page can say what to do about it rather than read it as a
     * refusal of the data call.
     */
    private function fetchToken(int $projectId): string
    {
        try {
            $answer = $this->api->get('/api/v1/projects/' . $projectId . '/users/' . Session::userId() . '/token');
        } catch (ApiException $e) {
            if ($e->status() === 403) {
                throw new ApiException('not_member', '', 403);
            }

            throw $e;
        }
        $token = is_string($answer['token'] ?? null) ? $answer['token'] : '';
        if ($token === '') {
            throw new ApiException('internal', '', 502);
        }
        Session::storeProjectToken($projectId, $token);

        return $token;
    }
}
