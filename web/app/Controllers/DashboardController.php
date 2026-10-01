<?php
// The dashboard (User_Interface_Design.md §4, REQ-UI-009) — the page after login,
// listing the projects the acting user may see. Visibility is the API's decision
// (REQ-API-049/007): PHP asks for `GET /api/v1/projects` and renders what comes
// back, with no filtering of its own and no notion of a project it was not told
// about.
//
// The route also serves the §4 list as a data region (REQ-UI-044): `data()` is
// reached by the same guard, performs the same read, and returns the same three
// counts the rendered rows show — never more.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\Response;

final class DashboardController extends Controller
{
    /** GET / — the rendered page. */
    public function index(): Response
    {
        $projects = $this->visibleProjects();

        // A user with no projects sees an information page, not an empty
        // dashboard (REQ-UI-006, §2.3); an administrator gets the setup entry
        // points instead of the explanation.
        if ($projects === []) {
            return $this->page('errors/no_access', [
                'pageTitle' => $this->i18n->t(
                    $this->view->isAdmin() ? 'no_access.admin_title' : 'no_access.title'
                ),
            ], [
                'titleKey' => $this->view->isAdmin() ? 'no_access.admin_title' : 'no_access.title',
                'scripts' => ['/assets/app.js'],
            ]);
        }

        return $this->page('dashboard', [
            'projects' => $projects,
            // The sidebar lists the visible projects too (§2.4) — the same read,
            // not a second one (Plan/Web_Implementation.md §7 rule 13).
            'sidebarProjects' => $projects,
        ], [
            'titleKey' => 'dashboard.title',
            // The shared runtime plus this page's own section module — a page
            // loads only what it uses (REQ-UI-045).
            'scripts' => ['/assets/app.js', '/assets/js/dashboard.js'],
            'jsKeys' => ['js.loading', 'js.load_failed', 'js.retry'],
        ]);
    }

    /** The `projects` data region of this route, for Accept: application/json. */
    public function data(): Response
    {
        return Response::json($this->visibleProjects());
    }

    /**
     * The visible projects with the quick statistics exactly as returned
     * (record_count, instrument_count, field_count). One call per render — the
     * rows are not enriched per row (Plan/Web_Implementation.md §7 rule 13).
     *
     * @return list<array<string, mixed>>
     */
    private function visibleProjects(): array
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
