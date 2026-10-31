<?php
/**
 * The <head> both layouts share after charset and viewport (§2.4, REQ-UI-080): the
 * document title, the crawler and cache directives, the browser icon and theme color,
 * and the Open Graph tags a chat client reads when a CLARA link is pasted into it.
 *
 * Open Graph wants absolute URLs. They are built from WEB_PUBLIC_URL (REQ-CFG-018),
 * never from the client-supplied Host header, and og:url carries the path alone: a query
 * string can hold a one-time token (`/set-password?token=…`, REQ-AUTH-049).
 *
 * The theme color is the night blue of the icon's tile (assets/icons/clara.svg), so the
 * browser chrome and the tab icon read as one brand under every installed theme.
 */

use Clara\View;

$documentTitle = $pageTitle === '' ? $view->t('app.name') : $pageTitle . ' · ' . $view->t('app.name');
?>
    <title><?= View::e($documentTitle) ?></title>
    <meta name="description" content="<?= View::e($view->t('app.tagline')) ?>">
    <!-- CLARA holds study data behind a sign-in: no page is meant for a search index. -->
    <meta name="robots" content="noindex, nofollow">
    <!-- Browsers obey the Cache-Control header every page is sent with (Response::html,
         no-store); this element repeats the rule for a copy of the page read outside HTTP. -->
    <meta http-equiv="Cache-Control" content="no-cache, no-store, must-revalidate">
    <meta name="theme-color" content="#15304f">

    <!-- The PNG first with its size, so a browser that renders SVG icons prefers the SVG. -->
    <link rel="icon" href="/assets/icons/clara-32.png" sizes="32x32" type="image/png">
    <link rel="icon" href="/assets/icons/clara.svg" type="image/svg+xml">
    <link rel="apple-touch-icon" href="/assets/icons/apple-touch-icon.png">

    <meta property="og:type" content="website">
    <meta property="og:site_name" content="<?= View::e($view->t('app.name')) ?>">
    <meta property="og:title" content="<?= View::e($documentTitle) ?>">
    <meta property="og:description" content="<?= View::e($view->t('app.tagline')) ?>">
    <meta property="og:url" content="<?= View::e($view->publicUrl($view->currentPath())) ?>">
    <meta property="og:image" content="<?= View::e($view->publicUrl('/assets/icons/clara-512.png')) ?>">
    <meta property="og:image:type" content="image/png">
    <meta property="og:image:width" content="512">
    <meta property="og:image:height" content="512">
