<?php
/**
 * The layout for pages with no session and no navigation: login (§2.2), the public
 * password pages (§2.6) and the survey page (§8.8) — a single centred panel, the
 * pattern the reference application uses for its login (DEV-UI-1).
 *
 * It renders with the installation theme, since there is no user context to
 * resolve an override from (§3.8).
 */

use Clara\View;

$pageTitle = $pageTitle ?? ($titleKey !== '' ? $view->t($titleKey) : '');
?>
<!doctype html>
<html lang="<?= View::e($view->currentLanguage()) ?>">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= View::e($pageTitle === '' ? $view->t('app.name') : $pageTitle . ' · ' . $view->t('app.name')) ?></title>
    <link rel="stylesheet" href="<?= View::e($view->stylesheetHref()) ?>">
    <link rel="stylesheet" href="/assets/app.css">
    <?= $view->jsStringBlock() ?>
    <?php foreach ($view->scripts() as $src): ?>
        <script type="module" src="<?= View::e($src) ?>"></script>
    <?php endforeach; ?>
</head>
<body class="clara-standalone">
    <div class="card clara-standalone-card shadow-sm">
        <div class="card-body p-4">
            <div class="clara-brand mb-3"><?= View::e($view->t('app.name')) ?></div>
            <?php require __DIR__ . '/../partials/alerts.php'; ?>
            <?= $content ?>
        </div>
    </div>
</body>
</html>
