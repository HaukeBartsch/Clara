<?php
// The incoming browser request, behind one small value object so the router and
// controllers never reach into superglobals (and so the router's dispatch —
// including both shapes of one route, REQ-UI-044 — is testable without HTTP).

declare(strict_types=1);

namespace Clara;

final class Request
{
    /**
     * @param array<string, string> $headers    lower-cased header names
     * @param array<string, mixed>  $query      parsed query string
     * @param array<string, mixed>  $post       parsed body (form-encoded)
     * @param array<string, string> $pathParams the `{name}` values the router matched,
     *                                          filled in by withPathParams()
     */
    public function __construct(
        private readonly string $method,
        private readonly string $path,
        private readonly array $headers,
        private readonly array $query,
        private readonly array $post,
        private readonly array $pathParams = []
    ) {}

    public static function fromGlobals(): self
    {
        $uri = $_SERVER['REQUEST_URI'] ?? '/';
        $path = parse_url((string) $uri, PHP_URL_PATH);
        if (!is_string($path) || $path === '') {
            $path = '/';
        }
        // A trailing slash is not a different route (and never re-redirects: a
        // redirect would turn a POST into a GET).
        if ($path !== '/' && str_ends_with($path, '/')) {
            $path = rtrim($path, '/');
        }

        return new self(
            strtoupper($_SERVER['REQUEST_METHOD'] ?? 'GET'),
            $path,
            self::collectHeaders(),
            is_array($_GET) ? $_GET : [],
            is_array($_POST) ? $_POST : []
        );
    }

    public function method(): string
    {
        return $this->method;
    }

    public function isPost(): bool
    {
        return $this->method === 'POST';
    }

    public function path(): string
    {
        return $this->path;
    }

    /**
     * The value the router matched for one `{name}` placeholder of the route
     * pattern (§2.1) — '' when the route has no such placeholder. Controllers read
     * path segments through this rather than re-splitting the path, so a route
     * change is a change to one row in Router and nowhere else.
     */
    public function pathParam(string $name, string $default = ''): string
    {
        return $this->pathParams[$name] ?? $default;
    }

    /** This request with the router's matched placeholders attached (immutably). */
    public function withPathParams(array $pathParams): self
    {
        return new self($this->method, $this->path, $this->headers, $this->query, $this->post, $pathParams);
    }

    public function header(string $name): ?string
    {
        return $this->headers[strtolower($name)] ?? null;
    }

    /** One query parameter as a string; absent or non-scalar reads as ''. */
    public function query(string $key, string $default = ''): string
    {
        $value = $this->query[$key] ?? null;

        return is_scalar($value) ? (string) $value : $default;
    }

    /** One body field as a string; absent or non-scalar reads as ''. */
    public function field(string $key, string $default = ''): string
    {
        $value = $this->post[$key] ?? null;

        return is_scalar($value) ? (string) $value : $default;
    }

    /** True when the request carries a body field at all (e.g. an empty password). */
    public function has(string $key): bool
    {
        return array_key_exists($key, $this->post);
    }

    /**
     * The mutation name of a `POST /route?action=<name>` request (§2.1 of
     * User_Interface_Design.md). Empty for an unparameterised POST.
     */
    public function action(): string
    {
        return $this->query('action');
    }

    /**
     * Whether this request wants the route's data region instead of its page
     * (REQ-UI-044). The decision is made here, once, for every route.
     *
     * A bare wildcard Accept — what a browser sends and what curl sends when
     * told nothing — is deliberately NOT a data request: only an explicit
     * `application/json` outranking `text/html` is. That is exactly the signal
     * the shared runtime in assets/app.js puts on the wire.
     */
    public function wantsJson(): bool
    {
        $accept = $this->header('accept');
        if ($accept === null) {
            return false;
        }
        $accept = trim($accept);
        if ($accept === '' || $accept === '*/*') {
            return false;
        }

        [$jsonQuality, $jsonListed] = self::quality($accept, 'application/json');
        if (!$jsonListed) {
            return false;
        }

        [$htmlQuality, $htmlListed] = self::quality($accept, 'text/html');
        if ($htmlListed && $htmlQuality > $jsonQuality) {
            return false;
        }

        return $jsonQuality > 0.0;
    }

    /**
     * The caller's address as the proxy reported it. nginx sets X-Real-IP from
     * $remote_addr and overwrites anything the client sent
     * (Technology_Stack_Design.md §5); PHP forwards that value unchanged on its
     * own API calls so the API's limiter sees a distinct address per caller
     * (REQ-API-125). A malformed header is ignored rather than forwarded — it
     * must never become a header-injection vector against the internal API.
     */
    public function clientIp(): string
    {
        $real = $this->header('x-real-ip');
        if ($real !== null && preg_match('/^[0-9A-Fa-f:.]+$/', $real) === 1) {
            return $real;
        }

        $remote = $_SERVER['REMOTE_ADDR'] ?? '';

        return is_string($remote) && preg_match('/^[0-9A-Fa-f:.]+$/', $remote) === 1 ? $remote : '0.0.0.0';
    }

    /**
     * Best matching quality for one media type in an Accept header, and whether
     * it matched at all. A range matches when it is the exact type, the type's
     * own wildcard form, or the all-types wildcard; parameters other than `q`
     * are ignored.
     *
     * @return array{0: float, 1: bool}
     */
    private static function quality(string $accept, string $type): array
    {
        [$wantType, $wantSubtype] = explode('/', $type, 2);

        $best = 0.0;
        $matched = false;

        foreach (explode(',', $accept) as $range) {
            $parts = explode(';', trim($range));
            $media = strtolower(trim($parts[0]));
            if ($media === '') {
                continue;
            }

            $q = 1.0;
            foreach (array_slice($parts, 1) as $param) {
                $param = trim($param);
                if (stripos($param, 'q=') === 0) {
                    $value = (float) substr($param, 2);
                    // A q outside 0..1 or unparsable means "not a real range".
                    $q = max(0.0, min(1.0, $value));
                }
            }

            $ok = $media === strtolower($type)
                || $media === strtolower($wantType) . '/*'
                || $media === '*/*';
            if ($ok && $q > $best) {
                $best = $q;
                $matched = true;
            }
        }

        return [$best, $matched];
    }

    /** @return array<string, string> */
    private static function collectHeaders(): array
    {
        $headers = [];
        foreach ($_SERVER as $key => $value) {
            if (!is_string($key) || !str_starts_with($key, 'HTTP_')) {
                continue;
            }
            $name = strtolower(str_replace('_', '-', substr($key, 5)));
            $headers[$name] = (string) $value;
        }

        // The two the CGI/SAPI form does not prefix with HTTP_.
        if (isset($_SERVER['CONTENT_TYPE'])) {
            $headers['content-type'] = (string) $_SERVER['CONTENT_TYPE'];
        }

        return $headers;
    }
}
