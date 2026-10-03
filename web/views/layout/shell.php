<?php
/**
 * The application shell (§2.4): header, an optional left panel, the content panel and
 * the footer. One shell serves the three page shapes — the project overview renders no
 * left panel at all (REQ-UI-009), while the project page and the Control Panel pass one
 * in `$nav` (REQ-UI-046/047). Selecting a panel item is a plain GET to that route,
 * rendered back into this shell: a shared multi-page layout, not a single-page app
 * (REQ-UI-001).
 *
 * Below the Bootstrap `lg` breakpoint a left panel collapses to the standard
 * off-canvas pattern via .offcanvas-lg: one markup tree serves both layouts, so
 * nothing here changes with the theme or the viewport (REQ-UI-008, REQ-TECH-027).
 *
 * A section is emitted only when it may be used, and a link is emitted only when
 * its route exists in this build — "hidden" means absent from the DOM, not
 * disabled (§3.1, REQ-UI-003). The panel's entries therefore arrive computed, from
 * Clara\Navigation.
 */

use Clara\Csrf;
use Clara\Navigation;
use Clara\View;

$pageTitle = $pageTitle ?? ($titleKey !== '' ? $view->t($titleKey) : '');

/**
 * The left panel: ['headingKey' => string, 'items' => list<{path, labelKey}>], supplied
 * by the page that has sections. Empty or absent means this page shows a single content
 * panel — which is the project overview's shape (§2.4 A).
 *
 * @var array{headingKey?: string, items?: list<array{path: string, labelKey: string}>} $nav
 */
$nav = $nav ?? [];
$navItems = $nav['items'] ?? [];
if ($navItems === []) {
    $nav = [];
}

// The Control Panel button appears for an administrator only when the panel has at
// least one section this build serves (§2.4, REQ-UI-003/009/047).
$controlPanel = $view->isAdmin() && Navigation::controlPanelExists();
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
        <?php if ($nav !== []): ?>
            <!-- The off-canvas toggle exists only where there is a panel to open (§2.4). -->
            <button class="btn btn-outline-secondary btn-sm d-lg-none" type="button" data-bs-toggle="offcanvas"
                    data-bs-target="#clara-nav" aria-controls="clara-nav"
                    aria-label="<?= View::e($view->t($nav['headingKey'] ?? 'nav.sections')) ?>">
                ☰
            </button>
        <?php endif; ?>

        <a class="navbar-brand clara-brand mb-0" href="/"><?= View::e($view->t('app.name')) ?></a>
        <?php if (($brandProject ?? '') !== ''): ?>
            <!-- Inside a project the header names it, and the name goes back to that
                 project's Overview (§2.4) — plain navigation like every other panel item,
                 not a client-side view switch (REQ-UI-001). -->
            <span class="text-body-secondary small px-1" aria-hidden="true">/</span>
            <?php if (($brandProjectUrl ?? '') !== ''): ?>
                <a class="text-body-emphasis text-decoration-none small"
                   href="<?= View::e($brandProjectUrl) ?>"><?= View::e($brandProject) ?></a>
            <?php else: ?>
                <span class="text-body-secondary small"><?= View::e($brandProject) ?></span>
            <?php endif; ?>
        <?php else: ?>
            <span class="d-none d-sm-inline text-body-secondary small"><?= View::e($view->t('app.tagline')) ?></span>
        <?php endif; ?>

        <div class="ms-auto d-flex align-items-center gap-2">
            <?php if ($controlPanel): ?>
                <!-- The single entry point to installation administration (§5, REQ-UI-047);
                     absent from the DOM for anyone who is not an administrator. -->
                <a class="btn btn-sm btn-outline-primary<?= $view->isActive('/admin') ? ' active' : '' ?>"
                   href="/admin"><?= View::e($view->t('nav.control_panel')) ?></a>
            <?php endif; ?>
        </div>
    </div>
</header>

<div class="clara-body">
    <?php if ($nav !== []): ?>
        <?php require __DIR__ . '/../partials/nav-panel.php'; ?>
    <?php endif; ?>

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

            <!-- Account entries (§2.4): user-scoped, so they live outside any section
                 panel and stay put while the panel changes. The password link appears only
                 for an account with a local credential to change (GD-23) — an entry whose
                 only outcome is "this account signs in through a provider" offers nothing
                 (§3.1). -->
            <nav class="clara-account-links d-flex flex-wrap gap-2" aria-label="<?= View::e($view->t('nav.account')) ?>">
                <a class="small" href="/account/two-factor"><?= View::e($view->t('nav.two_factor')) ?></a>
                <?php if ($view->hasLocalCredential()): ?>
                    <a class="small" href="/account/password"><?= View::e($view->t('nav.password')) ?></a>
                <?php endif; ?>
            </nav>

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
