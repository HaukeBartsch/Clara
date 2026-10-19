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
//
// script-src carries one relaxation: 'wasm-unsafe-eval'. Compiling a WebAssembly
// module is a script-source decision in CSP terms even when the bytes came from
// a same-origin fetch, so without it the assistant's compiled model library is
// refused and the model never loads (AI_Assistant_Design.md §5, REQ-AI-003). It
// permits WebAssembly compilation and nothing else: JavaScript `eval` stays
// banned — that would need the bare 'unsafe-eval' token, which must not appear.

declare(strict_types=1);

namespace Clara;

final class Response
{
    public const CSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; "
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

    /**
     * The open file an asset()'s body is read from, or null. Held as a handle rather than a path so
     * the size announced in Content-Length and the bytes handed on below come from one and the same
     * opened file. Closed by sendTo() once the body has gone out.
     *
     * @var resource|null
     */
    private $file = null;

    /** How much of a file-backed body goes out at a time (sendTo). */
    private const CHUNK_BYTES = 1048576;

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
    /**
     * The §4.2 failure envelope. `$ref` is this request's correlation reference
     * (REQ-UI-050): an extra field the browser modules ignore and devtools shows,
     * so a failing call can be matched to the server log without changing the
     * shape existing callers read.
     */
    public static function apiError(string $code, string $message, int $status, string $ref = ''): self
    {
        $body = ['error' => $code, 'message' => $message, 'status' => $status];
        if ($ref !== '') {
            $body['ref'] = $ref;
        }

        return self::json($body, $status);
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

    /** True for a streamed response, whose body() is empty until it is sent — a producer's or a file's. */
    public function isStream(): bool
    {
        return $this->producer !== null || $this->file !== null;
    }

    /**
     * Whether a request-supplied target is a path on this site, as opposed to an
     * address somewhere else (REQ-TECH-024).
     *
     * Two prefixes alone are not enough. Browsers fold a backslash into the slash
     * that follows it before resolving a URL, so `/\host` arrives as `//host` and
     * reads as scheme-relative just as `//host` does; tab and newline are stripped
     * from a URL the same way, so either one in front of `//host` also resolves off
     * site. A target therefore has to start with a single slash and carry no
     * backslash and no control character — everything else is refused, including an
     * empty string and a full URL (master-spec finding F14).
     */
    public static function isSameSitePath(string $path): bool
    {
        if ($path === '' || $path[0] !== '/' || str_starts_with($path, '//') || str_contains($path, '\\')) {
            return false;
        }

        // A preg_match failure reads as a refusal, never as permission.
        return preg_match('/[\x00-\x1F\x7F]/', $path) === 0;
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
     * `$vendored` marks a third-party file below assets/vendor/ (clara_asset_is_vendored);
     * `$ifNoneMatch` is the request's If-None-Match header, answered with a 304.
     *
     * The body is read from disk in pieces as it goes out, never collected whole: the assistant's
     * weight artifacts are hundreds of megabytes each (`web/assets/models/…/params_shard_0.bin` is
     * 311 MB), and a file that big cannot be held in memory — under any finite `memory_limit` the
     * read dies part-way and, because nothing has been written yet, PHP sends its error page as the
     * body of an HTTP 200. Production aliases `/assets/` to the files themselves (INSTALL.md §11),
     * so this handler carries them only where PHP is the last one serving a request — `php -S` in
     * development, or a front door that does not alias the tree.
     */
    public static function asset(string $absolutePath, bool $vendored = false, ?string $ifNoneMatch = null): self
    {
        // Opened here rather than at send() time: a file that cannot be opened has to answer the
        // 404 below, while headers can still be chosen. Opening also fixes the size and the
        // modification time against the same inode the body is read from, so Content-Length cannot
        // describe a different file than the one being sent.
        $handle = @fopen($absolutePath, 'rb');
        if ($handle === false) {
            return self::notFound();
        }

        $stat = fstat($handle);
        $size = (int) $stat['size'];

        // Neither tree is versioned by hash (no build step). Third-party files under
        // vendor/ change only on a library upgrade, so a short freshness window is fine.
        // The application's own scripts and styles change with every deployment, and a
        // page is never cached (no-store): a day-old app script under a current page
        // runs against markup it does not know — controls it never enables stay dead.
        // So those are revalidated on every use; the ETag keeps an unchanged file a 304.
        //
        // The tag is derived from what the file is, not from its contents: hashing every byte to
        // answer a conditional request would read a whole artifact into this process, which is the
        // thing this method must no longer do. Size and mtime change together with any deployment
        // and any re-fetch of an artifact, and the string keeps the strong, quoted, 32-hex-digit
        // shape it always had.
        $etag = '"' . substr(hash('sha256', $size . '-' . $stat['mtime']), 0, 32) . '"';
        if (self::etagMatches($ifNoneMatch, $etag)) {
            fclose($handle);
            $response = new self(304, '');
        } else {
            $response = new self(200, '');
            $response->file = $handle;
            $response->headers['Content-Type'] = self::contentType($absolutePath);
            // Announced so that a body cut short — a failed read, a proxy that gives up part-way —
            // is a length the client can disprove, and so a broken artifact arrives as a failed load
            // instead of as valid bytes that are simply too few.
            $response->headers['Content-Length'] = (string) $size;
            // Ask nginx not to buffer a large body on its way to the browser, as the export
            // producers do (Technology_Stack_Design.md §5).
            $response->headers['X-Accel-Buffering'] = 'no';
        }
        $response->headers['Cache-Control'] = $vendored ? 'public, max-age=86400' : 'no-cache';
        $response->headers['ETag'] = $etag;

        return $response;
    }

    /** If-None-Match against one strong tag: a list, weak forms and `*` all count (RFC 9110 §13.1.2). */
    private static function etagMatches(?string $ifNoneMatch, string $etag): bool
    {
        if ($ifNoneMatch === null) {
            return false;
        }
        foreach (explode(',', $ifNoneMatch) as $candidate) {
            $candidate = trim($candidate);
            if ($candidate === '*' || $candidate === $etag || $candidate === 'W/' . $etag) {
                return true;
            }
        }

        return false;
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
        if ($this->producer !== null || $this->file !== null) {
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
     *
     * A file-backed body (asset()) is copied through the sink in chunks and the handle closed:
     * sending it consumes it, so a response object goes out once, which is how the front
     * controller uses every response.
     */
    public function sendTo(StreamSink $sink): void
    {
        if ($this->file !== null) {
            $sink->begin($this->status, $this->headers);

            // Piece by piece, so the file's size never becomes this process's memory. A failed read
            // stops the body and leaves the announced Content-Length unmet: the client then reports
            // a load that failed rather than accepting bytes that are simply too few.
            while (!feof($this->file)) {
                $chunk = fread($this->file, self::CHUNK_BYTES);
                if ($chunk === false) {
                    break;
                }
                if ($chunk !== '') {
                    $sink->write($chunk);
                }
            }

            fclose($this->file);
            $this->file = null;

            return;
        }

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
            'jpg', 'jpeg' => 'image/jpeg',
            'gif' => 'image/gif',
            'webp' => 'image/webp',
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
