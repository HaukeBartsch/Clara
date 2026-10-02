<?php

/**
 * Set a password through an invite or reset token (§2.6, GD-22/GD-23, Sequence H).
 *
 * The token appears exactly once in this document — the hidden field that carries it back —
 * and never in a heading, a confirmation or a log line (§2.6, REQ-AUTH-049). A failure says
 * only that the link does not work: unknown, expired, consumed and wrong-purpose tokens are
 * one indistinguishable answer by design (REQ-API-120), and this page does not undo that.
 *
 * The browser checks the two new-password fields agree; the server checks again, and the API
 * applies the length policy after the token verifies (passwords.go).
 */

use Clara\View;

$done = $done ?? false;
$invalid = $invalid ?? false;
?>
<?php if ($done): ?>
    <h1 class="h4 mb-1"><?= View::e($view->t('set_password.done_heading')) ?></h1>
    <p class="text-body-secondary small mb-3"><?= View::e($view->t('set_password.done_help')) ?></p>
    <a class="btn btn-primary w-100" href="/login"><?= View::e($view->t('set_password.sign_in')) ?></a>
<?php else: ?>
    <h1 class="h4 mb-1"><?= View::e($view->t('set_password.heading')) ?></h1>

    <?php if ($invalid): ?>
        <!-- One line for every token problem, with the two ways onward. -->
        <div class="alert alert-danger py-2" role="alert"><?= View::e($view->t('set_password.invalid')) ?></div>
        <p class="small mb-3">
            <a href="/password-reset"><?= View::e($view->t('set_password.request_new')) ?></a>
        </p>
        <a class="btn btn-outline-primary w-100" href="/login"><?= View::e($view->t('set_password.sign_in_instead')) ?></a>
    <?php else: ?>
        <p class="text-body-secondary small mb-3"><?= View::e($view->t('set_password.help')) ?></p>

        <?php if (($error ?? '') !== ''): ?>
            <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
        <?php endif; ?>

        <form method="post" action="/set-password?action=complete" data-clara-password-form>
            <?= $view->csrfField() ?>
            <input type="hidden" name="token" value="<?= View::e($token ?? '') ?>">

            <div class="mb-2">
                <label class="form-label" for="new-password"><?= View::e($view->t('account.password.new')) ?></label>
                <input class="form-control" type="password" id="new-password" name="new_password"
                       required autocomplete="new-password" minlength="12" aria-describedby="password-policy">
            </div>
            <div class="mb-3" id="password-policy">
                <div class="form-text"><?= View::e($view->t('account.password.policy')) ?></div>
                <div class="form-text text-danger d-none" data-clara-password-mismatch>
                    <?= View::e($view->t('account.password.mismatch')) ?>
                </div>
            </div>

            <div class="mb-3">
                <label class="form-label" for="repeat-password"><?= View::e($view->t('account.password.repeat')) ?></label>
                <input class="form-control" type="password" id="repeat-password" name="repeat_password"
                       required autocomplete="new-password" minlength="12">
            </div>

            <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('set_password.submit')) ?></button>
        </form>
    <?php endif; ?>
<?php endif; ?>
