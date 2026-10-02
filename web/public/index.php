<?php
// The front controller (Technology_Stack_Design.md §4): config → session →
// router → response, and nothing else. Every browser request enters here; nginx
// routes it with `try_files … /index.php` in production and the built-in server
// with `php -S 127.0.0.1:8000 -t web/public web/public/index.php` in development.
//
// The order is load-bearing: configuration is validated before any output so a
// misconfiguration fails with an operator-readable page naming the variables
// (REQ-CFG-005), the session starts before anything reads identity, and the router
// owns guard → permission gate → content negotiation (REQ-UI-044).

declare(strict_types=1);

use Clara\ApiClient;
use Clara\Auth;
use Clara\Config;
use Clara\ConfigError;
use Clara\CurlTransport;
use Clara\I18n;
use Clara\Logger;
use Clara\Oauth;
use Clara\Request;
use Clara\Response;
use Clara\Router;
use Clara\Session;
use Clara\View;

require __DIR__ . '/../app/bootstrap.php';

// --- configuration, before anything can be rendered (REQ-CFG-005) -----------
try {
    $config = Config::load();
} catch (ConfigError $e) {
    configFailure($e);
}

ini_set('display_errors', $config->isDevelopment ? '1' : '0');
error_reporting(E_ALL);

$logger = Logger::fromConfig($config);
$request = Request::fromGlobals();

// --- vendored static assets -------------------------------------------------
// web/assets sits outside the document root (the tree in
// Technology_Stack_Design.md §4 fixes it there), so the built-in server cannot
// serve it and this handler does: one directory, contained, media type from a
// fixed map. Production nginx aliases the same path (`location /assets/`).
if (str_starts_with($request->path(), '/assets/')) {
    $file = clara_resolve_asset($request->path());
    ($file === '' ? Response::notFound() : Response::asset($file))->send();

    exit(0);
}

// --- session, then the page -------------------------------------------------
Session::start($config);

// The services may fail while being built, so the fallbacks below must not depend
// on them having existed.
$i18n = null;

try {
    // One transport for the process's outbound HTTP: the API client and the identity
    // providers an OAuth2 login talks to share it, so a deployment that needs a
    // different transport changes it in one place (and a test can replace both).
    $transport = new CurlTransport();
    $api = new ApiClient($config, $logger, $request, $transport);
    $i18n = new I18n($api, $logger, $config->isDevelopment);
    $view = new View($config, $i18n, $request);
    $auth = new Auth($api, $logger, $config, new Oauth($config, $logger, $transport));

    $router = new Router($config, $request, $api, $i18n, $view, $auth, $logger);
    $response = $router->dispatch();
} catch (ConfigError $e) {
    // A missing extension or an unusable transport: same operator-facing page as
    // a bad .env, because it is the same class of problem.
    configFailure($e);
} catch (Throwable $e) {
    // One log line with the real reason; the user gets a generic page and no
    // stack trace in any environment they can reach (REQ-API-006, REQ-CFG-005).
    $logger->error('unhandled error', [
        'path' => $request->path(),
        'type' => $e::class,
        'message' => $e->getMessage(),
        'file' => $e->getFile() . ':' . $e->getLine(),
    ]);

    // English copy when the translator itself is what failed.
    $generic = $i18n?->t('error.generic') ?? 'Something went wrong.';

    $response = $request->wantsJson()
        ? Response::apiError('internal', $generic, 500)
        : Response::html(fatalPage(
            $i18n?->t('error.server.page_title') ?? 'Service unavailable',
            $generic
        ), 500);
}

$response->send();

/**
 * Reports a configuration problem and stops: one log line for the operator's log,
 * and a page naming the variables — their values are never part of the message, so
 * showing it leaks nothing (REQ-CFG-005).
 */
function configFailure(ConfigError $error): never
{
    fwrite(STDERR, json_encode([
        'ts' => gmdate('Y-m-d\TH:i:s\Z'),
        'level' => 'error',
        'component' => 'web',
        'msg' => 'configuration refused startup',
        'ctx' => ['problems' => preg_split('/\R/', $error->getMessage()) ?: []],
    ], JSON_UNESCAPED_SLASHES) . "\n");

    Response::html(fatalPage(
        'The web application is misconfigured',
        // In development the operator sees which variables are wrong; in
        // production they see that something is wrong and read the log.
        ($_SERVER['APP_ENV'] ?? '') === 'production'
            ? 'Contact your administrator.'
            : nl2br(htmlspecialchars($error->getMessage(), ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8')),
        true
    ), 500)->send();

    exit(1);
}

/**
 * A page for the failures that cannot use the view layer: bad configuration and an
 * unhandled error. Self-contained (no template, no stylesheet dependency) so it
 * renders when nothing else can, and free of detail beyond what is passed in.
 */
function fatalPage(string $title, string $bodyHtml, bool $preformatted = false): string
{
    $body = $preformatted ? $bodyHtml : htmlspecialchars($bodyHtml, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');

    return '<!doctype html><html lang="en"><head><meta charset="utf-8">'
        . '<meta name="viewport" content="width=device-width, initial-scale=1">'
        . '<title>' . htmlspecialchars($title, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8') . ' — CLARA</title>'
        . '</head><body><h1>' . htmlspecialchars($title, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8') . '</h1><p>'
        . $body . '</p></body></html>';
}
