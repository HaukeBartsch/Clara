<?php
/**
 * The login page (§2.2). Local credentials — the path every installation has, and
 * the only one on a first installation without an identity provider (GD-18,
 * REQ-AUTH-051).
 *
 * The source picker appears only when more than one distinct name is configured
 * (REQ-UI-042); with one name, or none named anywhere, that name applies
 * implicitly and the picker is skipped (REQ-AUTH-067). The "Forgot password?" link
 * arrives with the /password-reset route in M2 — until then it is absent rather
 * than a link to a page that does not answer.
 *
 * The password travels browser → PHP over TLS only, is never stored or logged, and
 * is never re-rendered into the page (REQ-AUTH-036).
 */

use Clara\View;
?>
<h1 class="h4 mb-1"><?= View::e($view->t('login.heading')) ?></h1>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2 mt-3" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<form method="post" action="/login?action=credentials" class="mt-3" autocomplete="on">
    <?= $view->csrfField() ?>
    <input type="hidden" name="next" value="<?= View::e($next ?? '/') ?>">

    <?php if (!empty($sources)): ?>
        <!-- Several configured names: choose before credentials (§2.9). -->
        <fieldset class="mb-3">
            <legend class="form-label h6 mb-1"><?= View::e($view->t('login.source')) ?></legend>
            <p class="text-body-secondary small mt-0"><?= View::e($view->t('login.source_help')) ?></p>
            <?php foreach ($sources as $index => $source): ?>
                <div class="form-check">
                    <input class="form-check-input" type="radio" name="source" id="source-<?= $index ?>"
                           value="<?= View::e($source['name']) ?>"<?= $index === 0 ? ' checked' : '' ?>>
                    <label class="form-check-label" for="source-<?= $index ?>">
                        <?= View::e($source['name']) ?>
                    </label>
                </div>
            <?php endforeach; ?>
        </fieldset>
    <?php endif; ?>

    <div class="mb-3">
        <label class="form-label" for="login-email"><?= View::e($view->t('login.email')) ?></label>
        <input class="form-control" type="email" id="login-email" name="email" required
               autocomplete="username" value="<?= View::e($email ?? '') ?>">
    </div>

    <div class="mb-3">
        <label class="form-label" for="login-password"><?= View::e($view->t('login.password')) ?></label>
        <!-- No value attribute: a rejected attempt re-renders the page, and the
             password must not come back with it. -->
        <input class="form-control" type="password" id="login-password" name="password" required
               autocomplete="current-password">
    </div>

    <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.submit')) ?></button>
</form>
