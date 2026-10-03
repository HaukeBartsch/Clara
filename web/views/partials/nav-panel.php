<?php
/**
 * The left panel of a section-bearing page (§2.4 B) — the project page's functions or
 * the Control Panel's sections. Rendered inside the shell, which only requires this file
 * when `$nav` carries at least one item.
 *
 * Entries arrive already filtered by Clara\Navigation: permission and build state are
 * decided there, so this template renders exactly what it is given and asks nothing
 * (§3.1, REQ-UI-003). Selecting an item is a plain GET to that route — the panel swaps
 * by navigating, never by switching a view in the client (REQ-UI-001).
 */

use Clara\View;

/** @var array{headingKey?: string, items: list<array{path: string, labelKey: string, active?: bool}>} $nav */
$items = $nav['items'];
$headingKey = $nav['headingKey'] ?? 'app.name';
?>
<nav id="clara-nav" class="offcanvas-lg offcanvas-start clara-sidebar tab-focus"
     tabindex="-1" aria-labelledby="clara-nav-label">
    <div class="offcanvas-header">
        <h5 class="offcanvas-title" id="clara-nav-label"><?= View::e($view->t($headingKey)) ?></h5>
        <button type="button" class="btn-close" data-bs-dismiss="offcanvas" data-bs-target="#clara-nav"
                aria-label="<?= View::e($view->t('action.close')) ?>"></button>
    </div>

    <div class="offcanvas-body flex-column p-2">
        <div class="clara-nav-heading"><?= View::e($view->t($headingKey)) ?></div>
        <ul class="nav nav-pills flex-column clara-nav mb-3">
            <?php foreach ($items as $item): ?>
                <?php
                // A path-based match marks the project page's entries; a section reached by a
                // query parameter (`/admin?section=…`) says so itself, since the path alone
                // cannot tell the sections apart (§2.1).
                $active = $item['active'] ?? $view->isActive($item['path']);
                ?>
                <li class="nav-item">
                    <a class="nav-link<?= $active ? ' active' : '' ?>"
                       <?= $active ? ' aria-current="page"' : '' ?>
                       href="<?= View::e($item['path']) ?>">
                        <?= View::e($view->t($item['labelKey'])) ?>
                    </a>
                </li>
            <?php endforeach; ?>
        </ul>

        <?php /* A page adds its own panel footer block here when it has one; an empty
                 heading over nothing is what §3.1 forbids, so nothing is emitted by
                 default. */ ?>
    </div>
</nav>
