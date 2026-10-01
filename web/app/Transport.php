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
}
