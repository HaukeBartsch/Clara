<?php
/**
 * The dashboard (§4, REQ-UI-009): the signed-in user's name, then one row per
 * visible project with the quick statistics exactly as the API returned them.
 *
 * The rows are a data region: the server renders the table and its loading
 * placeholder, and assets/js/dashboard.js fills the body from this same route's
 * JSON (REQ-UI-032, REQ-UI-044). Navigation stays a plain GET — binding fills
 * targets inside the page, it never switches pages (§3.7).
 */

use Clara\View;
?>
<h1 class="h4 mb-3"><?= View::e($view->displayName()) ?></h1>

<div class="d-flex align-items-baseline gap-2 mb-2">
    <h2 class="h6 mb-0"><?= View::e($view->t('nav.projects')) ?> (<?= count($projects) ?>)</h2>
</div>

<table class="table table-sm clara-projects align-middle">
    <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('nav.project_list')) ?></th>
            <th scope="col" class="clara-stat"><?= View::e($view->t('dashboard.project.records')) ?></th>
            <th scope="col" class="clara-stat"><?= View::e($view->t('dashboard.project.instruments')) ?></th>
            <th scope="col" class="clara-stat"><?= View::e($view->t('dashboard.project.fields')) ?></th>
        </tr>
    </thead>
    <!-- Populated by assets/js/dashboard.js from GET / with Accept: application/json. -->
    <tbody data-region="projects">
        <tr>
            <td colspan="4" class="clara-region-empty"><?= View::e($view->t('js.loading')) ?></td>
        </tr>
    </tbody>
</table>
