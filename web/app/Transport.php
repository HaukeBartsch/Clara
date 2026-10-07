<?php
// The one seam between the web layer and the network. It exists so the parts of
// the application that matter most — routing, the content-negotiated page/data
// split (REQ-UI-044), gating, CSRF, message mapping — can be exercised without a
// running API, which is what Plan/Web_Implementation.md §8 asks of the PHP harness
// ("Unit (no HTTP)"). Production uses CurlTransport and nothing else.

declare(strict_types=1);

namespace Clara;

interface Transport
{
    /**
     * Performs one HTTP exchange and returns the status and raw body. Transport
     * failures are reported as a zero status with an empty body — turning those
     * into an ApiException is ApiClient's job, so that both a refused connection
     * and an error response follow one code path.
     *
     * @param list<string> $headers "Name: value" lines
     * @return array{status: int, body: string}
     */
    public function request(string $method, string $url, array $headers, ?string $body): array;

    /**
     * Performs one exchange whose body is handed on while it arrives instead of being
     * collected — the export download (REQ-TECH-011: no full materialization in memory).
     *
     * `$onStart(int $status, array $headers): bool` runs once, before the first body byte,
     * with the status and the response headers (names lower-cased). Returning true streams
     * the body through `$onChunk(string $chunk)`; returning false buffers it instead — that
     * is how an error body still reaches the caller to be read. A transport failure is a
     * zero status, as for request(); `complete` is false when a started stream broke off.
     *
     * @param list<string> $headers "Name: value" lines
     * @return array{status: int, body: string, streamed: bool, complete: bool}
     */
    public function stream(string $method, string $url, array $headers, callable $onStart, callable $onChunk): array;
}
