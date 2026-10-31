<?php
/**
 * Control Panel — Regions (§5.13, REQ-UI-078): where an administrator maps the role values an
 * OpenID Connect provider releases to regions, so a sign-in can set the user's automatic preferred
 * region (REQ-AUTH-088). The mapping is a Tabulator-backed data region bound by
 * `web/assets/js/regions.js` from this route's JSON (REQ-UI-044); each row's region select and
 * Remove are plain CSRF-protected POST forms (§3.3). Role values are provider text and are only
 * ever written as text (REQ-UI-004).
 */

use Clara\View;

/** @var list<array{issuer: string, name: string}> $providers the providers that read a roles claim */
/** @var array<string, string> $regionOptions code => display text, REQ-DB-006 order */
/** @var array<string, string> $form the add form's sticky values */
$form = $form ?? [];
$newCount = (int) ($newCount ?? 0);
?>
<div class="d-flex align-items-baseline gap-2 mb-1">
    <h1 class="h4 mb-0"><?= View::e($view->t('admin.regions.title')) ?></h1>
    <?php if ($newCount > 0): ?>
        <span class="badge text-bg-warning"><?= View::e($view->t('admin.regions.new_count', ['count' => (string) $newCount])) ?></span>
    <?php endif; ?>
</div>
<p class="text-body-secondary small"><?= View::e($view->t('admin.regions.intro')) ?></p>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<?php if ($providers === []): ?>
    <p class="small"><?= View::e($view->t('admin.regions.unconfigured')) ?>
        <a href="/admin?section=check"><?= View::e($view->t('admin.section.check')) ?></a></p>
<?php else: ?>
    <details class="mb-3"<?= $form !== [] ? ' open' : '' ?>>
        <summary class="small"><?= View::e($view->t('admin.regions.add')) ?></summary>
        <form method="post" action="/admin?section=regions&amp;action=add_role" class="row g-2 align-items-end mt-1">
            <?= $view->csrfField() ?>
            <div class="col-md-3">
                <label class="form-label small mb-0" for="role-issuer"><?= View::e($view->t('admin.regions.provider')) ?></label>
                <select class="form-select form-select-sm" id="role-issuer" name="issuer" required>
                    <?php foreach ($providers as $provider): ?>
                        <option value="<?= View::e($provider['issuer']) ?>"<?= ($form['issuer'] ?? '') === $provider['issuer'] ? ' selected' : '' ?>><?= View::e($provider['name']) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-4">
                <label class="form-label small mb-0" for="role-value"><?= View::e($view->t('admin.regions.value')) ?></label>
                <input class="form-control form-control-sm" id="role-value" name="role_value" required maxlength="255"
                       value="<?= View::e($form['role_value'] ?? '') ?>" autocomplete="off">
            </div>
            <div class="col-md-3">
                <label class="form-label small mb-0" for="role-region"><?= View::e($view->t('admin.regions.region')) ?></label>
                <select class="form-select form-select-sm" id="role-region" name="region">
                    <option value=""><?= View::e($view->t('admin.regions.no_region')) ?></option>
                    <?php foreach ($regionOptions as $code => $text): ?>
                        <option value="<?= View::e($code) ?>"<?= ($form['region'] ?? '') === $code ? ' selected' : '' ?>><?= View::e($text) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-2"><button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('admin.regions.add_submit')) ?></button></div>
        </form>
    </details>
<?php endif; ?>

<!-- Populated by assets/js/regions.js from GET /admin?section=regions with Accept: application/json. -->
<div id="clara-regions" data-region="regions"
     data-clara-regions="<?= View::e((string) json_encode($regionOptions, JSON_UNESCAPED_UNICODE)) ?>">
    <div class="clara-region-empty py-3"><?= View::e($view->t('js.loading')) ?></div>
</div>
