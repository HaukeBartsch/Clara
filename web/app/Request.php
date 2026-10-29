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
     * @param string                $ref        the per-request correlation reference (REQ-UI-050)
     * @param array<string, mixed>  $files      uploaded files as PHP received them ($_FILES)
     * @param string                $remoteAddr the direct TCP peer as PHP received it (REMOTE_ADDR),
     *                                          which is what clientIp() starts from
     * @param TrustedProxies|null   $trusted    peers allowed to name another caller in X-Real-IP
     *                                          (TRUSTED_PROXY_CIDRS); null trusts nobody
     */
    public function __construct(
        private readonly string $method,
        private readonly string $path,
        private readonly array $headers,
        private readonly array $query,
        private readonly array $post,
        private readonly array $pathParams = [],
        private readonly string $ref = '',
        private readonly array $files = [],
        private readonly string $remoteAddr = '',
        private readonly ?TrustedProxies $trusted = null
    ) {}

    public static function fromGlobals(?TrustedProxies $trusted = null): self
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
            is_array($_POST) ? $_POST : [],
            [],
            bin2hex(random_bytes(4)),
            is_array($_FILES) ? $_FILES : [],
            (string) ($_SERVER['REMOTE_ADDR'] ?? ''),
            $trusted
        );
    }

    /**
     * The per-request correlation reference (REQ-UI-050): stamped into every log
     * line of this request and shown next to a generic failure, so a user report
     * finds the logged cause. '' on a hand-built request (tests) — callers skip
     * the reference then rather than show an empty one.
     */
    public function ref(): string
    {
        return $this->ref;
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
        return new self(
            $this->method,
            $this->path,
            $this->headers,
            $this->query,
            $this->post,
            $pathParams,
            $this->ref,
            $this->files,
            $this->remoteAddr,
            $this->trusted
        );
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

    /**
     * A query parameter posted as a list (`arm[]=1&arm[]=3`), scalars only — the export's arm
     * checkboxes (§6.4). A single value reads as a one-item list.
     *
     * @return list<string>
     */
    public function queryList(string $key): array
    {
        $value = $this->query[$key] ?? null;
        if (is_scalar($value)) {
            return [(string) $value];
        }
        if (!is_array($value)) {
            return [];
        }

        return array_values(array_map('strval', array_filter($value, 'is_scalar')));
    }

    /** One body field as a string; absent or non-scalar reads as ''. */
    public function field(string $key, string $default = ''): string
    {
        $value = $this->post[$key] ?? null;

        return is_scalar($value) ? (string) $value : $default;
    }

    /**
     * A body field posted as a map (`name[key]=value`), scalars only — the translations
     * form posts one entry per key (§5.7). Anything else in the map is dropped.
     *
     * @return array<string, string>
     */
    public function fieldMap(string $key): array
    {
        $value = $this->post[$key] ?? null;
        if (!is_array($value)) {
            return [];
        }
        $out = [];
        foreach ($value as $k => $v) {
            if (is_scalar($v)) {
                $out[(string) $k] = (string) $v;
            }
        }

        return $out;
    }

    /**
     * A body field posted as a list (`name[]=a&name[]=b`), scalars only — the choice rows
     * of the field editor (§7.2). A scalar posted under the name reads as a one-item list.
     *
     * @return list<string>
     */
    public function fieldList(string $key): array
    {
        $value = $this->post[$key] ?? null;
        if (is_scalar($value)) {
            return [(string) $value];
        }
        if (!is_array($value)) {
            return [];
        }

        return array_values(array_map('strval', array_filter($value, 'is_scalar')));
    }

    /**
     * A body field posted as a map of lists (`name[key][]=value`) — the mapping matrix of
     * the setup page (§6.2 D). Non-scalar leaves are dropped.
     *
     * @return array<string, list<string>>
     */
    public function fieldListMap(string $key): array
    {
        $value = $this->post[$key] ?? null;
        if (!is_array($value)) {
            return [];
        }
        $out = [];
        foreach ($value as $k => $list) {
            $out[(string) $k] = is_array($list)
                ? array_values(array_map('strval', array_filter($list, 'is_scalar')))
                : (is_scalar($list) ? [(string) $list] : []);
        }

        return $out;
    }

    /**
     * The whole posted body except the CSRF token, as parsed — what a page replays when it
     * resubmits the identical request with an acknowledgement added (§6.8, REQ-UI-037).
     *
     * @return array<string, mixed>
     */
    public function fields(): array
    {
        $fields = $this->post;
        unset($fields['csrf_token']);

        return $fields;
    }

    /**
     * The uploads of one file input — `name="key[]"` with `multiple`, or a single `name="key"`
     * — as PHP received them: the client's file name, the temporary path PHP wrote it to, its
     * size and PHP's UPLOAD_ERR_* code. The temporary path comes from PHP itself ($_FILES is
     * never filled from request data), so a controller may read it.
     *
     * @return list<array{name: string, tmp_name: string, size: int, error: int}>
     */
    public function uploads(string $key): array
    {
        $entry = $this->files[$key] ?? null;
        if (!is_array($entry) || !isset($entry['name'])) {
            return [];
        }
        $names = is_array($entry['name']) ? $entry['name'] : [$entry['name']];
        $out = [];
        foreach ($names as $i => $name) {
            $pick = static fn (string $part): mixed => is_array($entry[$part] ?? null) ? ($entry[$part][$i] ?? null) : ($entry[$part] ?? null);
            $out[] = [
                'name' => is_string($name) ? $name : '',
                'tmp_name' => (string) ($pick('tmp_name') ?? ''),
                'size' => (int) ($pick('size') ?? 0),
                'error' => (int) ($pick('error') ?? UPLOAD_ERR_NO_FILE),
            ];
        }

        return $out;
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
     * Whether this is a page's own timer re-reading its data region (`X-Clara-Refresh: 1`,
     * REQ-UI-073) rather than something the user did. Such a read does not count as activity
     * for the idle bound (REQ-AUTH-015). The header can only make a session end sooner, never
     * keep one open, so trusting the client with it gives nothing away; a page render or a POST
     * is always the user's own.
     */
    public function isBackgroundRefresh(): bool
    {
        return !$this->isPost() && $this->wantsJson() && $this->header('x-clara-refresh') === '1';
    }

    /**
     * The caller's address, derived by the same rule the API applies to its own direct peer
     * (REQ-API-125, `dataapi.SourceIP`): REMOTE_ADDR is the answer, and a proxy inside
     * TRUSTED_PROXY_CIDRS may replace it with the X-Real-IP it received from that caller — no one
     * else. The header used to win whenever it was present, which let a browser hand itself a new
     * address on every request: past the rate limiter the API keys on this value, and into the
     * `password_reset_requested` audit entries that carry an IP (finding F9). nginx overwriting
     * HTTP_X_REAL_IP with its own $remote_addr (Technology_Stack_Design.md §5, INSTALL.md §11) is
     * the second line of defence, not the rule — a deployment whose nginx forgets it now limits and
     * audits by the connection address instead of believing the browser.
     *
     * The result goes out as X-Real-IP on every server-side API call, so it is always an address:
     * a header that names none is dropped rather than forwarded (a value carrying a newline would
     * be a header-injection vector against the internal API), and a REMOTE_ADDR PHP could not read
     * — a unix-socket pool without fastcgi_params — becomes 0.0.0.0, one shared bucket for callers
     * the platform cannot tell apart.
     */
    public function clientIp(): string
    {
        $remote = self::address($this->remoteAddr);

        if ($remote === '' || $this->trusted === null || !$this->trusted->contains($remote)) {
            return $remote !== '' ? $remote : '0.0.0.0';
        }

        // From a declared proxy the header is authoritative; a proxy that sends none still names
        // itself as far as this host can see.
        $real = self::address($this->header('x-real-ip'));

        return $real !== '' ? $real : $remote;
    }

    /** One value as an IP address, or '' when it names none (both families, no zone identifier). */
    private static function address(mixed $value): string
    {
        return is_string($value) ? (string) filter_var($value, FILTER_VALIDATE_IP) : '';
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
