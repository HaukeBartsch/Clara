<?php
/**
 * The dashboard (§4, REQ-UI-009): the signed-in user's name, then the projects they may see as a
 * bento grid — three tiers of box, most recently used first and largest (REQ-UI-070/071). Each box
 * carries the facts this page's former table split into columns (name, organization, and the record,
 * instrument and field counts) plus when the acting user last worked there; which way round they sit
 * follows the shape of the box, so the markup here and in dashboard.js makes no layout choice.
 *
 * The grid is a data region: the server renders the container and its loading line, and
 * assets/js/dashboard.js fills it from this same route's JSON (REQ-UI-032, REQ-UI-044). Navigation
 * stays a plain GET — binding fills targets inside the page, it never switches pages (§3.7).
 */

use Clara\View;
?>
<h1 class="h4 mb-3"><?= View::e($view->displayName()) ?></h1>

<!-- The heading row also carries the search by name and the "Starred only" switch, which filter every
     group of the grid below (REQ-UI-086/087). dashboard.js binds them; it shows them once the list holds
     a project. The two lines under the row say when the filters leave nothing to show and when a star
     could not be saved. -->
<div class="d-flex flex-wrap align-items-baseline gap-2 mb-2">
    <h2 class="h6 mb-0"><?= View::e($view->t('nav.projects')) ?> (<span data-clara-project-count><?= count($projects) ?></span>)</h2>
    <div class="clara-project-tools ms-auto" role="search" data-clara-project-tools<?= $projects === [] ? ' hidden' : '' ?>>
        <input type="search" class="form-control form-control-sm clara-project-search" data-clara-project-search
               placeholder="<?= View::e($view->t('dashboard.search.placeholder')) ?>"
               aria-label="<?= View::e($view->t('dashboard.search.label')) ?>" autocomplete="off" spellcheck="false">
        <div class="form-check form-switch mb-0">
            <input class="form-check-input" type="checkbox" role="switch" id="clara-starred-only">
            <label class="form-check-label" for="clara-starred-only"><?= View::e($view->t('dashboard.starred_only')) ?></label>
        </div>
    </div>
</div>
<p class="clara-region-empty mb-2" data-clara-project-filter-empty aria-live="polite" hidden></p>
<p class="text-danger small mb-2" data-clara-star-failed role="alert" hidden></p>

<!-- Populated by assets/js/dashboard.js from GET / with Accept: application/json, and re-read every
     two seconds while the page is visible, together with the count above (REQ-UI-073). The role stays on
     the list because app.css removes its bullets, and Safari drops the list semantics of a bulleted
     list once it has none — the count of projects is what a listener is there to hear. The projects are
     grouped by region, the user's own first and the rest in the order data-clara-region-order gives
     (REQ-UI-076). data-clara-account keys the memory of the "Starred only" switch in this browser to
     the signed-in account (REQ-UI-087). -->
<ul class="clara-projects" role="list" data-region="projects"
    data-clara-region-order="<?= View::e((string) json_encode($regionOrder ?? [], JSON_UNESCAPED_UNICODE)) ?>"
    data-clara-account="<?= View::e((string) ($account ?? 0)) ?>">
    <li class="clara-project-cell clara-project-cell--message">
        <p class="clara-region-empty mb-0"><?= View::e($view->t('js.loading')) ?></p>
    </li>
</ul>
