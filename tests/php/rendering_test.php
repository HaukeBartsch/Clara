<?php
// The remaining rules that are cheap to get wrong: escaping as the only interpolation
// path (REQ-UI-004), the asset handler's containment, CSP on every response, and the
// configuration validation that refuses to boot (REQ-CFG-005).

declare(strict_types=1);

use Clara\Config;
use Clara\ConfigError;
use Clara\Response;
use Clara\StreamSink;
use Clara\View;

/** A StreamSink that keeps what it was given, so a file-backed body can be inspected. */
final class AssetRecordingSink implements StreamSink
{
    public int $status = 0;
    /** @var array<string, string> */
    public array $headers = [];
    /** @var list<string> */
    public array $chunks = [];

    public function begin(int $status, array $headers): void
    {
        $this->status = $status;
        $this->headers = $headers;
    }

    public function write(string $chunk): void
    {
        $this->chunks[] = $chunk;
    }

    public function body(): string
    {
        return implode('', $this->chunks);
    }
}

/**
 * A temporary file of `$bytes` bytes, which the caller unlinks. Written out in small pieces so that
 * building the fixture is not itself what the memory assertion below measures.
 */
function asset_temp_file(int $bytes): string
{
    $path = (string) tempnam(sys_get_temp_dir(), 'clara-asset-');
    $piece = str_repeat('a', 65536);
    $handle = fopen($path, 'wb');
    for ($left = $bytes; $left > 0; $left -= strlen($piece)) {
        fwrite($handle, substr($piece, 0, (int) min(strlen($piece), $left)));
    }
    fclose($handle);

    return $path;
}

describe('escaping (REQ-UI-004, REQ-TECH-020)', function (): void {
    it('escapes markup, quotes and ampersands', function (): void {
        assert_same(
            '&lt;script&gt;alert(&#039;x&#039;)&lt;/script&gt;',
            View::e("<script>alert('x')</script>")
        );
        assert_same('a &amp; b', View::e('a & b'));
    });

    it('renders null and false as nothing, not as text', function (): void {
        assert_same('', View::e(null));
        assert_same('0', View::e(false));
    });

    it('substitutes an invalid byte rather than returning nothing', function (): void {
        // ENT_SUBSTITUTE: a broken stored value still renders, escaped.
        assert_true(strlen(View::e("bad \xC3\x28 value")) > 0);
    });

    it('keeps htmlAllowed the one pass-through for sanitized storage', function (): void {
        assert_same('<b>note</b>', View::htmlAllowed('<b>note</b>'));
    });
});

describe('the /assets handler', function (): void {
    it('serves a vendored file that exists', function (): void {
        $path = clara_resolve_asset('/assets/vendor/bootstrap/bootstrap.min.css');

        assert_true($path !== '' && is_file($path));
    });

    it('refuses to walk out of the asset tree', function (): void {
        assert_same('', clara_resolve_asset('/assets/../app/Config.php'));
        assert_same('', clara_resolve_asset('/assets/../../.env'));
        assert_same('', clara_resolve_asset('/assets/..'));
        assert_same('', clara_resolve_asset('/assets/etc/passwd'));
    });

    it('refuses a directory and a missing file', function (): void {
        assert_same('', clara_resolve_asset('/assets/vendor'));
        assert_same('', clara_resolve_asset('/assets/nope.css'));
    });

    it('answers 404 rather than serving an unresolved path', function (): void {
        assert_same(404, Response::asset('/does/not/exist')->status());
    });

    it('tells the third-party tree from the application files', function (): void {
        assert_true(clara_asset_is_vendored(clara_resolve_asset('/assets/vendor/bootstrap/bootstrap.min.css')));
        assert_true(!clara_asset_is_vendored(clara_resolve_asset('/assets/js/translations.js')));
        assert_true(!clara_asset_is_vendored(clara_resolve_asset('/assets/vendor/../app.js')));
    });

    it('revalidates application assets so a stale script never runs under a new page', function (): void {
        $response = Response::asset(clara_resolve_asset('/assets/js/translations.js'));

        assert_same(200, $response->status());
        assert_same('no-cache', $response->headers()['Cache-Control']);
        assert_true(preg_match('/^"[0-9a-f]{32}"$/', $response->headers()['ETag']) === 1);
    });

    it('keeps the freshness window for vendored libraries', function (): void {
        $file = clara_resolve_asset('/assets/vendor/bootstrap/bootstrap.min.css');

        assert_same('public, max-age=86400', Response::asset($file, true)->headers()['Cache-Control']);
    });

    it('answers a matching If-None-Match with an empty 304', function (): void {
        $file = clara_resolve_asset('/assets/app.js');
        $etag = Response::asset($file)->headers()['ETag'];

        foreach ([$etag, 'W/' . $etag, '"other", ' . $etag, '*'] as $ifNoneMatch) {
            $response = Response::asset($file, false, $ifNoneMatch);
            assert_same(304, $response->status());
            assert_same('', $response->body());
            assert_same($etag, $response->headers()['ETag']);
            assert_same('no-cache', $response->headers()['Cache-Control']);
        }
        assert_same(200, Response::asset($file, false, '"other"')->status());
    });

    it('announces the length it sends and asks the proxy not to buffer it', function (): void {
        $file = clara_resolve_asset('/assets/app.js');
        $response = Response::asset($file);

        assert_same((string) filesize($file), $response->headers()['Content-Length']);
        assert_same('no', $response->headers()['X-Accel-Buffering']);
        // The bytes come off disk as the response goes out, so there is no body to read beforehand.
        assert_true($response->isStream());
        assert_same('', $response->body());
    });

    it('sends a large asset in pieces, byte for byte', function (): void {
        // Several times the piece Response reads at a time. The two weight shards that made this a
        // real failure are 311 MB each; their size is what matters, not their contents.
        $path = asset_temp_file(16 * 1024 * 1024);
        try {
            $sink = new AssetRecordingSink();
            Response::asset($path)->sendTo($sink);

            assert_same(200, $sink->status);
            assert_same('16777216', $sink->headers['Content-Length']);
            assert_true(count($sink->chunks) > 1, 'handed on piece by piece, not collected');
            assert_same(md5_file($path), md5($sink->body()), 'every byte of it, in order');
        } finally {
            unlink($path);
        }
    });

    it('sends a large asset without holding it in memory', function (): void {
        // The sink here keeps nothing: what is under test is that producing the body costs nothing
        // proportional to the file, which is exactly what collecting it did. A 16 MiB body read as
        // it goes out moves this process by a couple of pieces, not by the file.
        $path = asset_temp_file(16 * 1024 * 1024);
        $bytes = 0;
        $sink = new class ($bytes) implements StreamSink {
            public function __construct(private int &$written) {}

            public function begin(int $status, array $headers): void {}

            public function write(string $chunk): void
            {
                $this->written += strlen($chunk);
            }
        };

        try {
            $peakBefore = memory_get_peak_usage(true);
            Response::asset($path)->sendTo($sink);
            $growth = memory_get_peak_usage(true) - $peakBefore;

            assert_same(16 * 1024 * 1024, $bytes);
            assert_true($growth < 4 * 1024 * 1024, "peak memory grew by {$growth} bytes over a 16 MiB body");
        } finally {
            unlink($path);
        }
    });
});

describe('response headers (REQ-TECH-020)', function (): void {
    it('sends a CSP on every response shape', function (): void {
        foreach ([
            Response::html('<p>x</p>'),
            Response::json(['a' => 1]),
            Response::redirect('/login'),
            Response::notFound(),
            Response::apiError('forbidden', 'no', 403),
        ] as $response) {
            assert_contains("script-src 'self'", $response->headers()['Content-Security-Policy']);
        }
    });

    it('allows no inline script or style execution anywhere in the policy', function (): void {
        assert_not_contains('unsafe-inline', Response::CSP);
        // The quoted form is what a JS-eval grant looks like; the assistant's WebAssembly
        // allowance is the distinct token 'wasm-unsafe-eval', whose own quotes sit elsewhere.
        assert_not_contains("'unsafe-eval'", Response::CSP);
    });

    it('lets the assistant compile its WebAssembly model library', function (): void {
        // Without this source the browser refuses WebAssembly.instantiate and the installed
        // model never loads — see Response.php and AI_Assistant_Design.md §5.
        assert_contains("'wasm-unsafe-eval'", Response::CSP);
    });

    it('never caches a page behind a session', function (): void {
        assert_same('no-store', Response::html('')->headers()['Cache-Control']);
    });

    it('reports a data-region failure in the API envelope shape', function (): void {
        $payload = json_decode(Response::apiError('forbidden', 'You do not have permission for this action', 403)->body(), true);

        assert_same('forbidden', $payload['error']);
        assert_same(403, $payload['status']);
    });
});

describe('document head (REQ-UI-080)', function (): void {
    /** The parts of the head both layouts must carry, checked on one rendered page. */
    $assertHead = static function (string $body, string $origin): void {
        assert_contains('<meta name="robots" content="noindex, nofollow">', $body);
        assert_contains('<meta http-equiv="Cache-Control" content="no-cache, no-store, must-revalidate">', $body);
        assert_contains('<meta name="theme-color" content="#15304f">', $body);
        assert_contains('<link rel="icon" href="/assets/icons/clara.svg" type="image/svg+xml">', $body);
        assert_contains('<link rel="apple-touch-icon" href="/assets/icons/apple-touch-icon.png">', $body);
        assert_contains('<meta property="og:site_name" content="CLARA">', $body);
        assert_contains('<meta property="og:image" content="' . $origin . '/assets/icons/clara-512.png">', $body);

        preg_match('#<title>([^<]*)</title>#', $body, $title);
        assert_contains('<meta property="og:title" content="' . $title[1] . '">', $body, 'og:title is the document title');
    };

    it('carries the head on the standalone layout, with URLs from WEB_PUBLIC_URL', function () use ($assertHead): void {
        $config = test_config(['WEB_PUBLIC_URL' => 'https://clara.example.org/']);
        $headers = browser_headers() + ['host' => 'attacker.example'];

        $body = router_for(http_request('GET', '/login', $headers), $config)->dispatch()->body();

        $assertHead($body, 'https://clara.example.org');
        assert_contains('<meta property="og:url" content="https://clara.example.org/login">', $body);
        assert_not_contains('attacker.example', $body, 'a client-supplied Host never becomes a link');
    });

    it('carries the same head on the application shell', function () use ($assertHead): void {
        sign_in();
        queue_shell();
        queue_sidebar_projects();

        $body = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();

        assert_contains('clara-shell', $body);
        $assertHead($body, 'http://localhost:8000');
        assert_same(1, substr_count($body, '<title>'), 'the partial owns the one title');
    });

    it('links only icons the /assets handler serves', function (): void {
        foreach (['clara.svg', 'clara-32.png', 'apple-touch-icon.png', 'clara-512.png'] as $icon) {
            assert_true(clara_resolve_asset('/assets/icons/' . $icon) !== '', $icon . ' is served');
        }
    });
});

describe('configuration validation (REQ-CFG-005)', function (): void {
    it('refuses to start without the service secret (REQ-CFG-013)', function (): void {
        $error = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => '']);
        });

        assert_contains('INTERNAL_SERVICE_TOKEN', $error->getMessage());
    });

    it('refuses a service secret shorter than the Go API accepts (finding F8)', function (): void {
        // Other variables may be missing here; what this asserts is that the length
        // complaint appears exactly at the boundary and in production only, so that a
        // short secret fails in whichever component reads it first.
        $complaint = static function (array $values): string {
            try {
                Config::fromValues($values + ['WEB_PUBLIC_URL' => 'https://csms.example.org']);
            } catch (ConfigError $e) {
                return $e->getMessage();
            }

            return '';
        };

        assert_contains('at least 32 bytes', $complaint([
            'APP_ENV' => 'production',
            'INTERNAL_SERVICE_TOKEN' => str_repeat('t', 31),
        ]));
        assert_not_contains('at least 32 bytes', $complaint([
            'APP_ENV' => 'production',
            'INTERNAL_SERVICE_TOKEN' => str_repeat('t', 64), // what INSTALL.md §6 issues
        ]));
        assert_not_contains('at least 32 bytes', $complaint([
            'INTERNAL_SERVICE_TOKEN' => 'test-service-token', // development keeps its short token
        ]));
    });

    it('names every problem in one pass rather than one per request', function (): void {
        $error = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues([
                'APP_ENV' => 'staging',
                'INTERNAL_SERVICE_TOKEN' => '',
                'UI_THEME' => 'flatly',
            ]);
        });

        assert_contains('APP_ENV', $error->getMessage());
        assert_contains('INTERNAL_SERVICE_TOKEN', $error->getMessage());
        assert_contains('UI_THEME', $error->getMessage());
    });

    it('rejects an unknown environment (REQ-CFG-007)', function (): void {
        $message = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['APP_ENV' => 'staging', 'INTERNAL_SERVICE_TOKEN' => 'x']);
        })->getMessage();

        assert_contains("must be 'development' or 'production'", $message);
    });

    it('rejects a theme that is not one of the installed identifiers (REQ-CFG-031)', function (): void {
        $message = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x', 'UI_THEME' => 'flatly']);
        })->getMessage();

        assert_contains('UI_THEME', $message);
    });

    it('rejects a session lifetime below a minute', function (): void {
        $message = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x', 'SESSION_LIFETIME' => '5']);
        })->getMessage();

        assert_contains('SESSION_LIFETIME', $message);
    });

    it('takes the Monitoring chart period and refuses one outside its range (REQ-CFG-034)', function (): void {
        assert_same(10, Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x'])->monitoringPeriodMinutes);
        assert_same(
            30,
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x', 'MONITORING_PERIOD_MINUTES' => '30'])->monitoringPeriodMinutes
        );

        foreach (['0', '1441', 'ten', ''] as $raw) {
            $message = assert_throws(ConfigError::class, static function () use ($raw): void {
                Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x', 'MONITORING_PERIOD_MINUTES' => $raw]);
            })->getMessage();

            assert_contains('MONITORING_PERIOD_MINUTES', $message);
        }
    });

    it('rejects a name list with an empty entry (REQ-CFG-032)', function (): void {
        $message = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x', 'LOCAL_LOGIN_NAMES' => 'Clinic A,, Clinic B']);
        })->getMessage();

        assert_contains('empty after trimming', $message);
    });

    it('derives the API base URL from API_ADDR and normalises a wildcard bind', function (): void {
        $build = static fn (string $addr): string => Config::fromValues([
            'INTERNAL_SERVICE_TOKEN' => 'x',
            'API_ADDR' => $addr,
        ])->apiBaseUrl;

        assert_same('http://127.0.0.1:8080', $build('127.0.0.1:8080'));
        assert_same('http://127.0.0.1:9000', $build('0.0.0.0:9000'), 'a wildcard bind is not a connect target');
        assert_same('http://127.0.0.1:8080', $build('[::]:8080'));
        assert_same('http://api.internal:8080', $build('api.internal:8080'));
    });

    it('collects the configured authentication-source names (REQ-CFG-032)', function (): void {
        $config = Config::fromValues([
            'INTERNAL_SERVICE_TOKEN' => 'x',
            'LOCAL_LOGIN_NAMES' => 'Clinic A, Walk-in',
            'OAUTH2_1_ISSUER' => 'https://idp.example.org',
            'OAUTH2_1_CLIENT_ID' => 'csms',
            'OAUTH2_1_NAMES' => 'Hospital 1',
        ]);

        assert_same(
            [['kind' => 'local', 'name' => 'Clinic A'], ['kind' => 'local', 'name' => 'Walk-in'],
                ['kind' => 'oauth2', 'name' => 'Hospital 1']],
            $config->authSources
        );

        // PHP keeps only display metadata; the Go API alone loads the client secret.
        assert_true(!array_key_exists('client_secret', $config->oauthProviders[0]));
        assert_same('email', $config->oauthProviders[0]['email_attr'], 'the documented claim default');
        assert_same(['Hospital 1'], $config->oauthProviders[0]['names']);
    });

    it('requires the OAuth client id in PHP and leaves client-secret validation to Go', function (): void {
        $error = assert_throws(Clara\ConfigError::class, static fn () => Config::fromValues([
            'INTERNAL_SERVICE_TOKEN' => 'x',
            'OAUTH2_1_ISSUER' => 'https://idp.example.org',
        ]));
        assert_contains('OAUTH2_1_CLIENT_ID', $error->getMessage());

        $config = Config::fromValues([
            'INTERNAL_SERVICE_TOKEN' => 'x',
            'OAUTH2_1_ISSUER' => 'https://idp.example.org',
            'OAUTH2_1_CLIENT_ID' => 'csms',
        ]);
        assert_same('csms', $config->oauthProviders[0]['client_id']);
        assert_true(!array_key_exists('client_secret', $config->oauthProviders[0]));

        // The same posture for a directory that has a URL but no search base.
        $error = assert_throws(Clara\ConfigError::class, static fn () => Config::fromValues([
            'INTERNAL_SERVICE_TOKEN' => 'x',
            'LDAP_SERVER_1_URL' => 'ldap://dir.example.org:389',
        ]));
        assert_contains('LDAP_SERVER_1_SEARCH_BASE', $error->getMessage());
    });

    it('treats an installation with no names as one implicit default set (REQ-AUTH-067)', function (): void {
        $config = Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x']);

        assert_same([], $config->authSources);
    });
});
