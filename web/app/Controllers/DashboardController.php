<?php
// The project overview (User_Interface_Design.md §4, REQ-UI-009) — the page after login,
// showing the projects the acting user may see as a bento grid: boxes of three sizes, most
// recently used first and largest (REQ-UI-070/071).
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

use Clara\ApiException;
use Clara\Regions;
use Clara\Response;
use Clara\Session;

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
            // The order the region groups follow after the user's own (REQ-UI-076): REQ-DB-006's.
            'regionOrder' => Regions::codes(),
            // Names the browser's memory of the "Starred only" switch, which is this account's own
            // on a browser several people sign in to (REQ-UI-087).
            'account' => Session::userId(),
        ], [
            'titleKey' => 'dashboard.title',
            // The shared runtime plus this page's own section module — a page
            // loads only what it uses (REQ-UI-045).
            'scripts' => ['/assets/app.js', '/assets/js/dashboard.js'],
            // Every label a project box renders client-side. The table this page used to draw needed
            // none of them in JS — its headers came from the server and its cells held numbers alone.
            // A box carries its own labels, so they cross into the data region with it (REQ-UI-008).
            'jsKeys' => [
                'js.load_failed', 'js.retry', 'dashboard.empty',
                'dashboard.project.records', 'dashboard.project.instruments', 'dashboard.project.fields',
                'dashboard.project.open', 'dashboard.project.last_used',
                'dashboard.project.mode.development', 'dashboard.project.mode.production',
                'dashboard.project.mode.analysis',
                'dashboard.project.last_used.today', 'dashboard.project.last_used.yesterday',
                'dashboard.project.never_used', 'dashboard.group.preferred', 'dashboard.group.region',
                // What the search and the star filter write (REQ-UI-086/087); their own controls are
                // rendered by the page with their labels.
                'dashboard.group.preferred.filtered', 'dashboard.group.region.filtered',
                'dashboard.search.none', 'dashboard.search.none_starred', 'dashboard.starred.none',
                'dashboard.project.star', 'dashboard.project.star_failed',
            ],
        ]);
    }

    /** The `projects` data region of this route, for Accept: application/json. */
    public function data(): Response
    {
        return Response::json($this->visibleProjects());
    }

    /**
     * POST /?action=star_project — stars (`starred=1`) or unstars (`starred=0`) one project for
     * the acting user (REQ-UI-087): `PUT` or `DELETE /api/v1/users/me/starred-projects/{id}`
     * (REQ-API-188). dashboard.js posts it and reads the answer, so it answers JSON either way —
     * `{project_id, starred}` as the API stored it, or the §4.2 failure with the translated line.
     * Whether the user may see the project is the API's decision, as for the list.
     */
    public function star(): Response
    {
        $raw = $this->request->field('project_id');
        $projectId = preg_match('/^[1-9]\d{0,17}$/', $raw) === 1 ? (int) $raw : 0;
        $starred = $this->request->field('starred');
        if ($projectId === 0 || ($starred !== '0' && $starred !== '1')) {
            return Response::apiError(
                'invalid_request',
                $this->invalidRequest(...($projectId === 0 ? ['project_id'] : ['starred'])),
                400,
                $this->request->ref()
            );
        }

        $path = '/api/v1/users/me/starred-projects/' . $projectId;
        try {
            $answer = $starred === '1' ? $this->api->put($path) : $this->api->delete($path);
        } catch (ApiException $e) {
            $this->logger->info('project star refused', ['code' => $e->code(), 'status' => $e->status()]);

            return Response::apiError($e->code(), $this->apiFailureMessage($e), max($e->status(), 400), $this->request->ref());
        }

        return Response::json([
            'project_id' => $projectId,
            'starred' => ($answer['starred'] ?? null) === true,
        ]);
    }
}
