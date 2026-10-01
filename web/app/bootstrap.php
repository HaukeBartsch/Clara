<?php
// Bootstrap for the web layer: paths and class loading, with no Composer and no
// generated autoloader (REQ-TECH-023 — the standard distribution; nothing here
// needs a dependency). Everything else is constructed by public/index.php.

declare(strict_types=1);

if (!defined('CLARA_WEB_ROOT')) {
    /** The web application's own tree: app/, views/, assets/. */
    define('CLARA_WEB_ROOT', dirname(__DIR__));
}

if (!defined('CLARA_REPO_ROOT')) {
    /** The repository root, where the shared .env lives (REQ-CFG-001). */
    define('CLARA_REPO_ROOT', dirname(__DIR__, 2));
}

spl_autoload_register(static function (string $class): void {
    // Clara\… maps onto web/app/…, one class per file, no discovery scan.
    if (!str_starts_with($class, 'Clara\\')) {
        return;
    }

    $relative = str_replace('\\', '/', substr($class, strlen('Clara\\')));
    if (preg_match('#^[A-Za-z0-9_/]+$#', $relative) !== 1) {
        return;
    }

    $file = CLARA_WEB_ROOT . '/app/' . $relative . '.php';
    if (is_file($file)) {
        require $file;
    }
});

/**
 * Resolves a /assets/… URL to a file inside web/assets, or '' when it does not
 * resolve. Only real files below that directory do, so `..`, absolute paths and
 * symlinks pointing elsewhere fall through to a 404 — and no PHP file can ever be
 * served or executed through this path. Lives here because both the front
 * controller and the test harness need the same rules.
 */
function clara_resolve_asset(string $urlPath): string
{
    $root = realpath(CLARA_WEB_ROOT . '/assets');
    if ($root === false) {
        return '';
    }

    $resolved = realpath($root . substr($urlPath, strlen('/assets')));
    if ($resolved === false || !str_starts_with($resolved, $root . DIRECTORY_SEPARATOR)) {
        return '';
    }

    return is_file($resolved) ? $resolved : '';
}
