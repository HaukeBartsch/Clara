<?php
// The project overview (User_Interface_Design.md §4, REQ-UI-009) — the page after login,
// listing the projects the acting user may see as rows with their statistics in columns.
// It is a single content panel: no left panel is passed to the shell, because the first
// screen after login is information rather than navigation (§2.4 A, REQ-UI-046).
// Visibility is the API's decision (REQ-API-049/007): PHP asks for `GET /api/v1/projects`
// and renders what comes back, with no filtering of its own and no notion of a project it
// was not told about.
//
// The route also serves the §4 list as a data region (REQ-UI-044): `data()` is reached by
// the same guard, performs the same read, and returns the same three counts the rendered
// rows show — never more.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\Response;

final class DashboardController extends Controller
{
    /** GET / — the rendered page. */
    public function index(): Response
    {
        $projects = $this->visibleProjects();

        // A user who is neither an administrator nor a member of any project sees an
        // information page, not an empty overview (REQ-UI-006, §2.3). An administrator
        // sees every project (REQ-API-049), so an empty list only means the installation
        // has none yet — the overview then shows its empty table (owner, 2026-10-03).
        if ($projects === [] && !$this->view->isAdmin()) {
            return $this->page('errors/no_access', [
                'pageTitle' => $this->i18n->t('no_access.title'),
            ], [
                'titleKey' => 'no_access.title',
                'scripts' => ['/assets/app.js'],
            ]);
        }

        return $this->page('dashboard', [
            'projects' => $projects,
        ], [
            'titleKey' => 'dashboard.title',
            // The shared runtime plus this page's own section module — a page
            // loads only what it uses (REQ-UI-045).
            'scripts' => ['/assets/app.js', '/assets/js/dashboard.js'],
            'jsKeys' => ['js.loading', 'js.load_failed', 'js.retry', 'dashboard.empty'],
        ]);
    }

    /** The `projects` data region of this route, for Accept: application/json. */
    public function data(): Response
    {
        return Response::json($this->visibleProjects());
    }
}
