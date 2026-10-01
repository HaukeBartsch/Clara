<?php
// The route set of User_Interface_Design.md §2.1 — the complete set of
// browser-reachable routes — plus the two conventions that hang off it:
//
//   * mutations are POSTs to the page route with ?action=<name> (§2.1), and
//     every POST carries a CSRF token before its controller runs (§3.3);
//   * the JSON a page's client binds data regions from is served by that same
//     route, selected on Accept (REQ-UI-044). This file holds the only
//     implementation of that decision in the application: guard → permission
//     gate → Accept split → controller, so a data request is authorized exactly
//     like the page render it stands for and no controller or template grows an
//     `if wants_json()` branch of its own. A route with no declared region
//     answers 406 rather than HTML.
//
// Adding a page means adding one row here and one controller method: no routing
// code, and nothing to forget on the second shape.

declare(strict_types=1);

namespace Clara;

final class Router
{
    /**
     * Each route: HTTP method, path pattern with {param} placeholders,
     * controller class, handler method, guard (`public` | `login` | `admin`),
     * optional `?action=` value, and the data region the route can serve as JSON.
     *
     * Only the routes this pass implements are listed; §2.1's remainder arrives
     * with M2–M6 of Plan/Web_Implementation.md, one row each.
     *
     * @var list<array{method: string, pattern: string, controller: class-string, handler: string, guard: string, action?: string, region?: string}>
     */
    private const ROUTES = [
        ['method' => 'GET', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'index', 'guard' => 'public'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'credentials', 'guard' => 'public', 'action' => 'credentials'],
        // The two-factor panel and the enrollment wizard live on the same route
        // (§2.1: "login incl. source-name picker and the two-factor step"), so
        // the pre-auth `tfa_pending` state reaches exactly these handlers and
        // nothing else — every other route's guard rejects it (§2.7).
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'twoFactor', 'guard' => 'public', 'action' => 'mfa'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'resendCode', 'guard' => 'public', 'action' => 'mfa_resend'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollTotp', 'guard' => 'public', 'action' => 'enroll_totp'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'confirmTotp', 'guard' => 'public', 'action' => 'enroll_totp_confirm'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollEmail', 'guard' => 'public', 'action' => 'enroll_email'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'confirmEmail', 'guard' => 'public', 'action' => 'enroll_email_confirm'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollmentDone', 'guard' => 'public', 'action' => 'enroll_done'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'abort', 'guard' => 'public', 'action' => 'abort'],

        ['method' => 'GET', 'pattern' => '/', 'controller' => Controllers\DashboardController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'projects'],

        ['method' => 'POST', 'pattern' => '/logout', 'controller' => Controllers\AccountController::class,
            'handler' => 'logout', 'guard' => 'login'],
        ['method' => 'POST', 'pattern' => '/lang', 'controller' => Controllers\AccountController::class,
            'handler' => 'language', 'guard' => 'login', 'action' => 'language'],
        ['method' => 'POST', 'pattern' => '/theme', 'controller' => Controllers\AccountController::class,
            'handler' => 'theme', 'guard' => 'login', 'action' => 'theme'],
    ];

    public function __construct(
        private readonly Config $config,
        private readonly Request $request,
        private readonly ApiClient $api,
        private readonly I18n $i18n,
        private readonly View $view,
        private readonly Auth $auth,
        private readonly Logger $logger
    ) {}

    /** Matches the request and produces its response. */
    public function dispatch(): Response
    {
        $route = $this->match();

        if ($route === null) {
            return $this->notFound();
        }

        // --- guard, before either shape (§3.1, REQ-UI-044) ---
        $denied = Auth::guard($route['guard'], $this->config, $this->request->path());
        if ($denied !== null) {
            return $this->shapeRefusal($denied);
        }

        // --- CSRF on every mutation, before the controller and before any API
        // call: a rejected request must not reach the write at all (§3.3) ---
        if ($this->request->isPost() && !Csrf::validate($this->request)) {
            $this->logger->warn('csrf rejection', ['path' => $this->request->path()]);
            Session::flash('danger', $this->i18n->t('error.csrf'));

            // Back to the page the mutation was aimed at, which is also the route
            // whose guard we just passed.
            return Response::redirect($this->request->path());
        }

        // --- the one Accept split (REQ-UI-044) ---
        if ($this->request->wantsJson()) {
            if (($route['region'] ?? null) === null) {
                return Response::notAcceptable();
            }

            return $this->invoke($route, 'data');
        }

        return $this->invoke($route, $route['handler']);
    }

    /**
     * Finds the route row for this request. A POST whose row declares an
     * `action` only matches when `?action=` carries that value — the mutation
     * convention of §2.1 — and a row without one matches any POST to its path.
     *
     * @return array{method: string, pattern: string, controller: class-string, handler: string, guard: string, action?: string, region?: string}|null
     */
    private function match(): ?array
    {
        $fallback = null;

        foreach (self::ROUTES as $route) {
            if ($route['method'] !== $this->request->method()) {
                continue;
            }
            if (!$this->matchesPattern($route['pattern'])) {
                continue;
            }

            $expected = $route['action'] ?? null;
            if ($expected === null) {
                return $route;
            }
            if (hash_equals($expected, $this->request->action())) {
                return $route;
            }
            // Remember a path match with the wrong action: an unknown mutation on
            // an existing route is a 405-ish client error, not a 404 — but it must
            // not shadow an exact match later in the table.
            $fallback ??= $route;
        }

        return null;
    }

    /** Path pattern matching with `{name}` placeholders (no regex from user input). */
    private function matchesPattern(string $pattern): bool
    {
        $regex = preg_replace('/\{([a-zA-Z_][a-zA-Z0-9_]*)\}/', '[^/]+', $pattern);
        if ($regex === null) {
            return false;
        }

        return preg_match('#^' . $regex . '$#', $this->request->path()) === 1;
    }

    /** @param array{controller: class-string, handler: string, region?: string} $route */
    private function invoke(array $route, string $handler): Response
    {
        $class = $route['controller'];
        $controller = new $class(
            $this->config,
            $this->request,
            $this->api,
            $this->i18n,
            $this->view,
            $this->auth,
            $this->logger
        );

        try {
            return $controller->{$handler}();
        } catch (ApiException $e) {
            return $this->fromApiException($e, $route);
        }
    }

    /**
     * A failed API call becomes the page or the JSON failure the §3.4 table fixes.
     * The status passes through; the text is translated and never an internal.
     *
     * @param array{region?: string} $route
     */
    private function fromApiException(ApiException $e, array $route): Response
    {
        $wantsJson = $this->request->wantsJson() && ($route['region'] ?? null) !== null;
        $text = Messages::forApiException($this->i18n, $e);

        if ($wantsJson) {
            return Response::apiError($e->code(), $text, $e->status());
        }

        // A page-level failure: log the real reason, show the user the line.
        $this->logger->warn('page read failed', [
            'path' => $this->request->path(),
            'code' => $e->code(),
            'status' => $e->status(),
        ]);

        if ($e->status() === 403 || $e->status() === 404) {
            return $this->view->render('errors/denied', [
                'title' => $e->status() === 403
                    ? $this->i18n->t('error.forbidden.page_title')
                    : $this->i18n->t('error.not_found.page_title'),
                'body' => $text,
            ], ['layout' => 'shell', 'titleKey' => '']);
        }

        return $this->serverError();
    }

    /** The 500 page: no stack trace, no internals (REQ-CFG-005, REQ-API-006). */
    public function serverError(): Response
    {
        if ($this->request->wantsJson()) {
            return Response::apiError('internal', $this->i18n->t('error.generic'), 500);
        }

        try {
            return $this->view->render('errors/server', [
                'title' => $this->i18n->t('error.server.page_title'),
                'body' => $this->i18n->t('error.server.page_body'),
            ], ['layout' => null]);
        } catch (\Throwable) {
            // The view itself failed (often a missing template): answer plainly.
            return Response::html('<!doctype html><meta charset="utf-8"><title>Error</title>'
                . '<p>The application could not complete your request.</p>', 500);
        }
    }

    private function notFound(): Response
    {
        if ($this->request->wantsJson()) {
            return Response::apiError('not_found', $this->i18n->t('error.not_found'), 404);
        }

        return Response::html('<!doctype html><meta charset="utf-8"><title>Not found</title>'
            . '<p>The page you asked for does not exist.</p>', 404);
    }

    /**
     * A guard refusal keeps its shape: a browser gets the redirect, a data
     * request gets the API's own failure envelope so the client renders the §3.4
     * line instead of feeding HTML to response.json().
     */
    private function shapeRefusal(Response $denied): Response
    {
        if (!$this->request->wantsJson()) {
            return $denied;
        }

        return match ($denied->status()) {
            403 => Response::apiError('forbidden', $this->i18n->t('error.forbidden'), 403),
            404 => Response::apiError('not_found', $this->i18n->t('error.not_found'), 404),
            // An unauthenticated data request: the client treats a redirect to
            // login as "session over, go sign in" — so answer it as 403 rather
            // than let fetch() follow to an HTML page.
            default => Response::apiError('forbidden', $this->i18n->t('error.forbidden'), 403),
        };
    }
}
