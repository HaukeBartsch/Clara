<?php
// The remaining rules that are cheap to get wrong: escaping as the only interpolation
// path (REQ-UI-004), the asset handler's containment, CSP on every response, and the
// configuration validation that refuses to boot (REQ-CFG-005).

declare(strict_types=1);

use Clara\Config;
use Clara\ConfigError;
use Clara\Response;
use Clara\View;

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
        assert_not_contains('unsafe-eval', Response::CSP);
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

describe('configuration validation (REQ-CFG-005)', function (): void {
    it('refuses to start without the service secret (REQ-CFG-013)', function (): void {
        $error = assert_throws(ConfigError::class, static function (): void {
            Config::fromValues(['INTERNAL_SERVICE_TOKEN' => '']);
        });

        assert_contains('INTERNAL_SERVICE_TOKEN', $error->getMessage());
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
    });

    it('treats an installation with no names as one implicit default set (REQ-AUTH-067)', function (): void {
        $config = Config::fromValues(['INTERNAL_SERVICE_TOKEN' => 'x']);

        assert_same([], $config->authSources);
    });
});
