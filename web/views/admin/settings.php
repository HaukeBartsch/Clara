<?php

/**
 * The Control Panel's Settings section (§5.8, REQ-UI-043): the system-wide runtime
 * settings and their editor. The values are the installation's own — never a project's or a
 * user's — which is why this section exists only behind the `is_admin` guard of §2.1.
 *
 * The helper text states the scope and the effect time (rate limiting applies to every
 * caller IP immediately after saving, with no restart); the API stays authoritative about
 * the ranges, and a refusal comes back through the §3.4 mapping above the form.
 */

use Clara\View;

$enabled = $enabled ?? false;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.settings.title')) ?></h1>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<p class="text-body-secondary small"><?= View::e($view->t('admin.settings.help')) ?></p>

<form method="post" action="/admin?action=save_settings">
    <?= $view->csrfField() ?>
    <input type="hidden" name="section" value="settings">

    <div class="form-check form-switch mb-3">
        <input class="form-check-input" type="checkbox" role="switch" id="rate-limit-enabled"
               name="rate_limit_enabled" value="1"<?= $enabled ? ' checked' : '' ?>>
        <label class="form-check-label" for="rate-limit-enabled">
            <?= View::e($view->t('settings.rate_limit_enabled')) ?>
        </label>
    </div>

    <div class="mb-3">
        <label class="form-label" for="rate-limit-rpm"><?= View::e($view->t('settings.rate_limit_rpm')) ?></label>
        <input class="form-control form-control-sm" type="number" id="rate-limit-rpm" name="rate_limit_rpm"
               min="1" step="1" inputmode="numeric" value="<?= View::e((string) ($rpm ?? '')) ?>">
    </div>

    <div class="mb-3">
        <label class="form-label" for="rate-limit-block-minutes">
            <?= View::e($view->t('settings.rate_limit_block_minutes')) ?>
        </label>
        <input class="form-control form-control-sm" type="number" id="rate-limit-block-minutes"
               name="rate_limit_block_minutes" min="1" max="1440" step="1" inputmode="numeric"
               value="<?= View::e((string) ($blockMinutes ?? '')) ?>">
    </div>

    <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
</form>
