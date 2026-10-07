<?php
// Outgoing responses. Every one of them carries the Content-Security-Policy
// header (REQ-TECH-020, User_Interface_Design.md §3.2) — including redirects and
// error pages, which is why it is applied in the constructor rather than in the
// view layer.
//
// The policy has no 'unsafe-inline' anywhere, which fixes two things about the
// markup this application may emit: no inline `on*=` handlers and no inline
// `style=` attributes (styling goes through app.css or Bootstrap classes), and
// scripts load only as same-origin files. The <script type="application/json"
// data-i18n> block of §9 is data rather than script, so it needs no exemption.

declare(strict_types=1);

namespace Clara;

final class Response
{
    public const CSP = "default-src 'self'; script-src 'self'; style-src 'self'; "
        . "img-src 'self' data:; font-src 'self'; connect-src 'self'; "
        . "form-action 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'";

    /** @var array<string, string> */
    private array $headers = [];

    /**
     * The body producer of a streamed response (stream()), or null for an ordinary one.
     *
     * @var (callable(StreamSink): void)|null
     */
    private $producer = null;

    private function __construct(
        private readonly int $status,
        private readonly string $body
    ) {
        $this->headers['Content-Security-Policy'] = self::CSP;
        $this->headers['X-Content-Type-Options'] = 'nosniff';
        $this->headers['Referrer-Policy'] = 'same-origin';
    }

    public static function html(string $body, int $status = 200): self
    {
        $response = new self($status, $body);
        $response->headers['Content-Type'] = 'text/html; charset=UTF-8';
        // Pages carry per-user content behind a session: never cached, never
        // stored by a shared browser.
        $response->headers['Cache-Control'] = 'no-store';

        return $response;
    }

    /**
     * A data region (REQ-UI-044). Failures keep the API's status and stable
     * error code so the client renders the §3.4 line instead of parsing HTML
     * out of response.json().
     */
    public static function json(mixed $value, int $status = 200): self
    {
        $encoded = json_encode(
            $value,
            JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR
        );

        $response = new self($status, $encoded);
        $response->headers['Content-Type'] = 'application/json; charset=UTF-8';
        $response->headers['Cache-Control'] = 'no-store';

        return $response;
    }

    /** The §4.2 error envelope, so a data request fails exactly like the API. */
    public static function apiError(string $code, string $message, int $status): self
    {
        return self::json(['error' => $code, 'message' => $message, 'status' => $status], $status);
    }

    /** 406 — a data request for a route that declares no data region. */
    public static function notAcceptable(): self
    {
        return self::apiError(
            'invalid_request',
            'this route serves no JSON data region',
            406
        );
    }

    /**
     * A response whose status, headers and body are produced while it is being sent — the
     * export download, proxied piece by piece so nothing is held in memory (REQ-TECH-011).
     * `$producer(StreamSink $sink)` calls `$sink->begin()` once with its status and headers,
     * then `$sink->write()` for each piece. The base headers of every response (CSP,
     * nosniff, referrer policy) are merged into what it begins with.
     *
     * The producer runs after the router has returned, outside its error handling, so it
     * must answer its own failures — typically by beginning a redirect instead of a body.
     *
     * @param callable(StreamSink): void $producer
     */
    public static function stream(callable $producer): self
    {
        $response = new self(200, '');
        $response->producer = $producer;

        return $response;
    }

    /** True for a streamed response, whose body() is empty until it is sent. */
    public function isStream(): bool
    {
        return $this->producer !== null;
    }

    public static function redirect(string $to, int $status = 302): self
    {
        $response = new self($status, '');
        $response->headers['Location'] = $to;
        $response->headers['Cache-Control'] = 'no-store';

        return $response;
    }

    /**
     * Serves a file from the vendored asset tree. The caller has already
     * resolved and contained the path (see FrontController::asset); the content
     * type comes from a fixed map so nothing unexpected is ever served inline.
     */
    public static function asset(string $absolutePath): self
    {
        $body = @file_get_contents($absolutePath);
        if ($body === false) {
            return self::notFound();
        }

        $response = new self(200, $body);
        $response->headers['Content-Type'] = self::contentType($absolutePath);
        // Vendored files are replaced on deployment, not versioned by hash yet:
        // a short freshness window keeps upgrades visible without a build step.
        $response->headers['Cache-Control'] = 'public, max-age=86400';

        return $response;
    }

    public static function notFound(): self
    {
        return self::html('<!doctype html><meta charset="utf-8"><title>Not found</title>'
            . '<p>The requested page does not exist.</p>', 404);
    }

    public function status(): int
    {
        return $this->status;
    }

    public function body(): string
    {
        return $this->body;
    }

    /** @return array<string, string> */
    public function headers(): array
    {
        return $this->headers;
    }

    public function withHeader(string $name, string $value): self
    {
        $clone = clone $this;
        $clone->headers[$name] = $value;

        return $clone;
    }

    /** Emits status, headers and body. Called once, by the front controller. */
    public function send(): void
    {
        if ($this->producer !== null) {
            $this->sendTo(new PhpOutputSink());

            return;
        }

        if (!headers_sent()) {
            http_response_code($this->status);
            foreach ($this->headers as $name => $value) {
                header($name . ': ' . $value);
            }
        }

        echo $this->body;
    }

    /**
     * Sends this response into a sink — a streamed one by running its producer, an ordinary
     * one as status, headers and body. The test harness passes a recording sink.
     */
    public function sendTo(StreamSink $sink): void
    {
        if ($this->producer === null) {
            $sink->begin($this->status, $this->headers);
            $sink->write($this->body);

            return;
        }

        $base = $this->headers;
        ($this->producer)(new class ($sink, $base) implements StreamSink {
            private bool $begun = false;

            /** @param array<string, string> $base */
            public function __construct(private readonly StreamSink $inner, private readonly array $base) {}

            public function begin(int $status, array $headers): void
            {
                if ($this->begun) {
                    return;
                }
                $this->begun = true;
                $this->inner->begin($status, $headers + $this->base);
            }

            public function write(string $chunk): void
            {
                if (!$this->begun) {
                    $this->begin(200, []);
                }
                $this->inner->write($chunk);
            }
        });
    }

    /** Extension → media type for the asset handler; unknown extensions 404. */
    private static function contentType(string $path): string
    {
        return match (strtolower((string) pathinfo($path, PATHINFO_EXTENSION))) {
            'css' => 'text/css; charset=UTF-8',
            'js', 'mjs' => 'text/javascript; charset=UTF-8',
            'svg' => 'image/svg+xml',
            'png' => 'image/png',
            'gif' => 'image/gif',
            'ico' => 'image/x-icon',
            'ttf' => 'font/ttf',
            'otf' => 'font/otf',
            'woff' => 'font/woff',
            'woff2' => 'font/woff2',
            'map' => 'application/json',
            'txt' => 'text/plain; charset=UTF-8',
            default => 'application/octet-stream',
        };
    }
}
