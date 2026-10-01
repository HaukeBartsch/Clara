<?php
/**
 * The application shell: brand bar, sidebar, content panel, footer
 * (Design/User_Interface_Design.md §2.4). Every authenticated page renders inside
 * it; selecting a sidebar item is a plain GET to that route and swaps the panel —
 * this is a shared multi-page layout, not a single-page app (REQ-UI-001).
 *
 * Below the Bootstrap `lg` breakpoint the sidebar collapses to the standard
 * off-canvas pattern via .offcanvas-lg: one markup tree serves both layouts, so
 * nothing here changes with the theme or the viewport (REQ-UI-008, REQ-TECH-027).
 *
 * A section is emitted only when it may be used, and a link is emitted only when
 * its route exists in this build — "hidden" means absent from the DOM, not
 * disabled (§3.1, REQ-UI-003).
 */

use Clara\Csrf;
use Clara\View;

$pageTitle = $pageTitle ?? ($titleKey !== '' ? $view->t($titleKey) : '');
?>
<!doctype html>
<html lang="<?= View::e($view->currentLanguage()) ?>">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= View::e($pageTitle === '' ? $view->t('app.name') : $pageTitle . ' · ' . $view->t('app.name')) ?></title>

    <!-- Exactly one Bootstrap stylesheet: the standard file or one installed theme
         (REQ-UI-040). app.css is hand-written and theme-independent. -->
    <link rel="stylesheet" href="<?= View::e($view->stylesheetHref()) ?>">
    <link rel="stylesheet" href="/assets/app.css">

    <?php if ($view->isAuthenticated()): ?>
        <!-- Read by the shared runtime for the X-CSRF-Token header (REQ-UI-005). -->
        <meta name="csrf-token" content="<?= View::e(Csrf::token()) ?>">
    <?php endif; ?>

    <?= $view->jsStringBlock() ?>

    <?php foreach ($view->scripts() as $src): ?>
        <!-- ES modules by relative path, unbundled, same-origin (REQ-UI-045). -->
        <script type="module" src="<?= View::e($src) ?>"></script>
    <?php endforeach; ?>
</head>
<body class="clara-shell">
<a class="visually-hidden-focusable" href="#clara-content"><?= View::e($view->t('nav.skip_to_content')) ?></a>

<header class="navbar navbar-expand-lg border-bottom">
    <div class="container-fluid">
        <button class="btn btn-outline-secondary btn-sm d-lg-none" type="button" data-bs-toggle="offcanvas"
                data-bs-target="#clara-sidebar" aria-controls="clara-sidebar" aria-label="<?= View::e($view->t('nav.projects')) ?>">
            ☰
        </button>
        <a class="navbar-brand clara-brand mb-0" href="/"><?= View::e($view->t('app.name')) ?></a>
        <span class="d-none d-sm-inline text-body-secondary small"><?= View::e($view->t('app.tagline')) ?></span>
    </div>
</header>

<div class="clara-body">
    <?php require __DIR__ . '/../partials/sidebar.php'; ?>

    <main id="clara-content" class="clara-content" tabindex="-1">
        <?php require __DIR__ . '/../partials/alerts.php'; ?>
        <?= $content ?>
    </main>
</div>

<footer class="border-top py-2">
    <div class="container-fluid d-flex flex-wrap align-items-center gap-3 px-3">
        <?php if ($view->isAuthenticated()): ?>
            <form method="post" action="/lang" class="d-flex align-items-center gap-2">
                <?= $view->csrfField() ?>
                <input type="hidden" name="next" value="<?= View::e($view->currentPath()) ?>">
                <label class="form-label mb-0 small text-body-secondary" for="clara-language"><?= View::e($view->t('language.label')) ?></label>
                <select class="form-select form-select-sm w-auto" id="clara-language" name="language">
                    <?php foreach ($view->languages() as $language): ?>
                        <option value="<?= View::e($language['code']) ?>"<?= $language['code'] === $view->currentLanguage() ? ' selected' : '' ?>>
                            <?= View::e($language['display_name']) ?>
                        </option>
                    <?php endforeach; ?>
                </select>
                <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.save')) ?></button>
            </form>

            <form method="post" action="/theme" class="d-flex align-items-center gap-2">
                <?= $view->csrfField() ?>
                <input type="hidden" name="next" value="<?= View::e($view->currentPath()) ?>">
                <label class="form-label mb-0 small text-body-secondary" for="clara-theme"><?= View::e($view->t('theme.label')) ?></label>
                <select class="form-select form-select-sm w-auto" id="clara-theme" name="theme">
                    <option value="default"<?= $view->selectedTheme() === null ? ' selected' : '' ?>>
                        <?= View::e($view->t('theme.default')) ?>
                    </option>
                    <?php foreach ($view->installedThemes() as $theme): ?>
                        <option value="<?= View::e($theme['id']) ?>"<?= $view->selectedTheme() === $theme['id'] ? ' selected' : '' ?>>
                            <?= View::e($theme['label']) ?>
                        </option>
                    <?php endforeach; ?>
                </select>
                <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.save')) ?></button>
            </form>

            <span class="ms-auto text-body-secondary small"><?= View::e($view->displayName()) ?></span>

            <form method="post" action="/logout" class="d-flex">
                <?= $view->csrfField() ?>
                <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('nav.sign_out')) ?></button>
            </form>
        <?php endif; ?>
    </div>
</footer>

<!-- Bootstrap's bundle JS (Popper included), vendored and same-origin. -->
<script src="/assets/vendor/bootstrap/bootstrap.bundle.min.js"></script>
</body>
</html>
