<?php
// Shared footing for the page controllers: the collaborators every page needs,
// plus the two small conventions pages repeat — a safe "redirect back" and a
// guard against an anonymous visitor reaching a signed-in page by hand.
//
// Controllers never inspect Accept or decide page-versus-JSON (that is the
// router's single decision, REQ-UI-044); each declares one page handler and, if
// its route serves one, a `data()` handler returning the region payload.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiClient;
use Clara\Auth;
use Clara\Config;
use Clara\I18n;
use Clara\Logger;
use Clara\Request;
use Clara\Response;
use Clara\Session;
use Clara\View;

abstract class Controller
{
    public function __construct(
        protected readonly Config $config,
        protected readonly Request $request,
        protected readonly ApiClient $api,
        protected readonly I18n $i18n,
        protected readonly View $view,
        protected readonly Auth $auth,
        protected readonly Logger $logger
    ) {}

    /**
     * Where a mutation returns to. Only a same-site absolute path is honoured —
     // never an arbitrary URL from the request (REQ-TECH-024 covers redirect
     // targets) — and `//host` is rejected because browsers read it as
     // scheme-relative.
     */
    protected function redirectBack(string $fallback = '/'): Response
    {
        $next = $this->request->field('next');
        if ($next === '') {
            $next = $this->request->query('next');
        }
        if ($next !== '' && str_starts_with($next, '/') && !str_starts_with($next, '//')) {
            return Response::redirect($next);
        }

        return Response::redirect($fallback);
    }

    /** Queues a success line for the next render (§3.4's confirmation style). */
    protected function flashSuccess(string $text): void
    {
        Session::flash('success', $text);
    }

    protected function flashDanger(string $text): void
    {
        Session::flash('danger', $text);
    }

    /** A page rendered inside the application shell. */
    protected function page(string $template, array $data = [], array $options = []): Response
    {
        return $this->view->render($template, $data, $options + ['layout' => 'shell']);
    }

    /** A single centred panel with no navigation (login, password pages). */
    protected function standalone(string $template, array $data = [], array $options = []): Response
    {
        return $this->view->render($template, $data, $options + ['layout' => 'standalone']);
    }

    /**
     * The projects the acting user may see, with the quick statistics exactly as
     * the API returned them (REQ-API-049): visibility is the API's decision, so PHP
     * renders what comes back and filters nothing
     * (`User_Interface_Design.md` §4, REQ-AUTH-026).
     *
     * The project overview's table is its only caller (§2.4 A): one call per render, and
     * rows are never enriched per row (Plan/Web_Implementation.md §7 rule 13). The page
     * that needs one project's permissions asks for that project, which is why entering a
     * project resolves there rather than here (REQ-UI-017).
     *
     * @return list<array<string, mixed>>
     */
    protected function visibleProjects(): array
    {
        $projects = $this->api->get('/api/v1/projects');
        if (!is_array($projects)) {
            return [];
        }

        $out = [];
        foreach ($projects as $project) {
            if (!is_array($project) || !isset($project['id'])) {
                continue;
            }
            $out[] = [
                'id' => (int) $project['id'],
                'project_name' => (string) ($project['project_name'] ?? ''),
                'organization' => (string) ($project['organization'] ?? ''),
                'record_count' => (int) ($project['record_count'] ?? 0),
                'instrument_count' => (int) ($project['instrument_count'] ?? 0),
                'field_count' => (int) ($project['field_count'] ?? 0),
            ];
        }

        return $out;
    }
}
