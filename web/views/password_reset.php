<?php

/**
 * Forgot password (§2.6, GD-23, REQ-AUTH-062).
 *
 * One sentence for every outcome: whether or not the address has an account with a local
 * password, whether or not anything was mailed, the page says the same thing. That is the
 * whole of the no-enumeration rule as it appears on screen, so nothing else may be added to
 * the confirmation — not even a "we sent it to {address}" echo.
 */

use Clara\View;

$sent = $sent ?? false;
?>
<h1 class="h4 mb-1"><?= View::e($view->t('password_reset.heading')) ?></h1>
<p class="text-body-secondary small mb-3"><?= View::e($view->t('password_reset.help')) ?></p>

<?php if (($message ?? '') !== ''): ?>
    <div class="alert alert-<?= $sent ? 'success' : 'danger' ?> py-2" role="alert">
        <?= View::e($message) ?>
    </div>
<?php endif; ?>

<?php if ($sent): ?>
    <!-- The request is done; the only way onward is the mailbox or the sign-in page. -->
    <a class="btn btn-outline-primary w-100" href="/login"><?= View::e($view->t('password_reset.back_to_login')) ?></a>
<?php else: ?>
    <form method="post" action="/password-reset?action=request">
        <?= $view->csrfField() ?>
        <div class="mb-3">
            <label class="form-label" for="reset-email"><?= View::e($view->t('login.email')) ?></label>
            <input class="form-control" type="email" id="reset-email" name="email" required autocomplete="email"
                   value="<?= View::e($email ?? '') ?>">
            <div class="form-text"><?= View::e($view->t('password_reset.local_only')) ?></div>
        </div>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('password_reset.submit')) ?></button>
    </form>

    <p class="text-center mt-3 mb-0">
        <a class="small" href="/login"><?= View::e($view->t('password_reset.cancel')) ?></a>
    </p>
<?php endif; ?>
