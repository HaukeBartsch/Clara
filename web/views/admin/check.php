<?php

/**
 * The Control Panel's Configuration Check section (§5.10, REQ-UI-061): INSTALL.md as a list of
 * checks, one table per source — the PHP runtime, the web layer's configuration and files, the
 * API service and its host, the database — each row a status, what was checked, the finding,
 * and the INSTALL.md section that explains the setting. Read-only; "Check again" reloads.
 *
 * The browser rows render as "Not checked — needs JavaScript"; `config-check.js` marks them
 * "Checking…" and fills them by asking nginx what only an outside caller can (§13.1). The last
 * block lists what no software check can see from inside the installation (§17).
 */

use Clara\ConfigCheck;
use Clara\View;

$badge = static fn (string $status): string => match ($status) {
    ConfigCheck::OK => 'text-bg-success',
    ConfigCheck::WARN => 'text-bg-warning',
    ConfigCheck::FAIL => 'text-bg-danger',
    ConfigCheck::INFO => 'text-bg-info',
    default => 'text-bg-secondary',
};
$problems = (int) ($summary[ConfigCheck::FAIL] ?? 0);
$warnings = (int) ($summary[ConfigCheck::WARN] ?? 0);
$head = static function (View $view): string {
    return '<thead><tr>'
        . '<th scope="col" class="clara-check-status">' . View::e($view->t('admin.check.column.status')) . '</th>'
        . '<th scope="col">' . View::e($view->t('admin.check.column.check')) . '</th>'
        . '<th scope="col">' . View::e($view->t('admin.check.column.finding')) . '</th>'
        . '<th scope="col" class="text-nowrap">' . View::e($view->t('admin.check.column.ref')) . '</th>'
        . '</tr></thead>';
};
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.check.title')) ?></h1>

<p class="text-body-secondary small"><?= View::e($view->t('admin.check.intro')) ?></p>

<div class="d-flex flex-wrap align-items-center gap-2 mb-4">
    <div class="alert <?= $problems > 0 ? 'alert-danger' : ($warnings > 0 ? 'alert-warning' : 'alert-success') ?> py-2 mb-0 flex-grow-1" role="status">
        <?php if ($problems === 0 && $warnings === 0): ?>
            <?= View::e($view->t('admin.check.summary.clean')) ?>
        <?php else: ?>
            <?= View::e($view->t('admin.check.summary.found', ['problems' => (string) $problems, 'warnings' => (string) $warnings])) ?>
        <?php endif; ?>
    </div>
    <a class="btn btn-sm btn-outline-secondary" href="/admin?section=check"><?= View::e($view->t('admin.check.again')) ?></a>
</div>

<?php foreach ($groups as $group): ?>
    <section class="mb-4" aria-labelledby="clara-check-<?= View::e($group['key']) ?>">
        <h2 class="h6" id="clara-check-<?= View::e($group['key']) ?>"><?= View::e($view->t('admin.check.group.' . $group['key'])) ?></h2>
        <div class="table-responsive">
            <table class="table table-sm align-middle clara-config-check">
                <?= $head($view) ?>
                <tbody>
                <?php foreach ($group['checks'] as $check): ?>
                    <tr data-clara-check="<?= View::e($check['id']) ?>" data-status="<?= View::e($check['status']) ?>">
                        <td><span class="badge <?= $badge($check['status']) ?>"><?= View::e($view->t('admin.check.status.' . $check['status'])) ?></span></td>
                        <td><?= View::e($view->t('admin.check.' . $check['id'] . '.title')) ?></td>
                        <td><?= View::e($view->t($check['messageKey'], $check['params'])) ?></td>
                        <td class="text-nowrap small text-body-secondary"><?= View::e($check['ref']) ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    </section>
<?php endforeach; ?>

<section class="mb-4" aria-labelledby="clara-check-browser">
    <h2 class="h6" id="clara-check-browser"><?= View::e($view->t('admin.check.group.browser')) ?></h2>
    <p class="text-body-secondary small mb-2"><?= View::e($view->t('admin.check.browser.intro')) ?></p>
    <div class="table-responsive">
        <table class="table table-sm align-middle clara-config-check">
            <?= $head($view) ?>
            <tbody>
            <?php foreach ($probes as $probe): ?>
                <tr data-clara-probe="<?= View::e($probe['id']) ?>" data-status="skip">
                    <td><span class="badge text-bg-secondary" data-clara-probe-status><?= View::e($view->t('admin.check.status.skip')) ?></span></td>
                    <td><?= View::e($view->t('admin.check.' . $probe['id'] . '.title')) ?></td>
                    <td data-clara-probe-finding><?= View::e($view->t('admin.check.browser.needs_js')) ?></td>
                    <td class="text-nowrap small text-body-secondary"><?= View::e($probe['ref']) ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</section>

<section aria-labelledby="clara-check-manual">
    <h2 class="h6" id="clara-check-manual"><?= View::e($view->t('admin.check.group.manual')) ?></h2>
    <p class="text-body-secondary small mb-2"><?= View::e($view->t('admin.check.manual.intro')) ?></p>
    <ul class="small">
        <?php foreach ($manual as $item): ?>
            <li><?= View::e($view->t($item['key'])) ?> <span class="text-body-secondary">(INSTALL.md <?= View::e($item['ref']) ?>)</span></li>
        <?php endforeach; ?>
    </ul>
</section>
