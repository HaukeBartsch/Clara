<?php
/**
 * The sidebar (§2.4). Each section is present only when the acting user may use
 * it, and only routes that exist in this build are linked — a link to a page that
 * is not implemented would be a disabled control by another name (§3.1,
 * REQ-UI-003). The Account section's two-factor and password entries arrive with
 * M2, when those routes do.
 *
 * Rendered inside the shell; $view comes from View::capture().
 */

use Clara\View;

/** @var list<array<string, mixed>> $sidebarProjects visible projects, supplied by the page that fetched them */
$projects = $sidebarProjects ?? [];
?>
<nav id="clara-sidebar" class="offcanvas-lg offcanvas-start clara-sidebar tab-focus"
     tabindex="-1" aria-labelledby="clara-sidebar-label">
    <div class="offcanvas-header">
        <h5 class="offcanvas-title" id="clara-sidebar-label"><?= View::e($view->t('app.name')) ?></h5>
        <button type="button" class="btn-close" data-bs-dismiss="offcanvas" data-bs-target="#clara-sidebar"
                aria-label="<?= View::e($view->t('action.close')) ?>"></button>
    </div>

    <div class="offcanvas-body flex-column p-2">
        <!-- Projects: any authenticated user (the dashboard is always reachable). -->
        <div class="clara-nav-heading"><?= View::e($view->t('nav.projects')) ?></div>
        <ul class="nav nav-pills flex-column clara-nav mb-3">
            <li class="nav-item">
                <a class="nav-link<?= $view->isActive('/') ? ' active' : '' ?>" href="/">
                    <?= View::e($view->t('nav.dashboard')) ?>
                </a>
            </li>
            <?php foreach ($projects as $project): ?>
                <li class="nav-item">
                    <a class="nav-link<?= $view->isActive('/projects/' . $project['id']) ? ' active' : '' ?>"
                       href="/projects/<?= View::e((string) $project['id']) ?>">
                        <?= View::e($project['project_name']) ?>
                        <?php if (($project['organization'] ?? '') !== ''): ?>
                            <span class="d-block small text-body-secondary"><?= View::e($project['organization']) ?></span>
                        <?php endif; ?>
                    </a>
                </li>
            <?php endforeach; ?>
        </ul>

        <?php
        /**
         * Administration entries, is_admin only. Users, Projects, Audit, Translations
         * and Settings land with M3; until then the list is empty and the section is
         * not rendered at all — an heading over nothing, like a link to a route that
         * 404s, is a disabled control by another name (§3.1).
         */
        $adminSections = $view->isAdmin() ? [] : [];
        ?>
        <?php if ($adminSections !== []): ?>
            <div class="clara-nav-heading"><?= View::e($view->t('nav.administration')) ?></div>
            <ul class="nav nav-pills flex-column clara-nav">
                <?php foreach ($adminSections as $admin): ?>
                    <li class="nav-item">
                        <a class="nav-link<?= $view->isActive($admin['path']) ? ' active' : '' ?>"
                           href="<?= View::e($admin['path']) ?>"><?= View::e($admin['label']) ?></a>
                    </li>
                <?php endforeach; ?>
            </ul>
        <?php endif; ?>
    </div>
</nav>
