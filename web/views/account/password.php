<?php

/**
 * Change the local password (§2.6, REQ-AUTH-061).
 *
 * The address is read-only text, not a field: an account's identity changes through an
 * administrator, and a form input here would suggest otherwise (REQ-AUTH-061). Nothing on
 * this page is remembered — the new password goes to the API in one call and appears in no
 * later render, log line or session (REQ-AUTH-036).
 *
 * The browser checks that the two new-password fields agree; this form is authoritative
 * about it and about the length policy the API applies (12 characters minimum, security
 * finding F12), so the hint states the rule rather than leaving it to a rejection.
 */

use Clara\View;

$hasLocalCredential = $hasLocalCredential ?? true;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('account.password.heading')) ?></h1>

<p class="text-body-secondary small">
    <?= View::e($view->t('account.password.address')) ?>
    <span class="fw-semibold"><?= View::e($email ?? '') ?></span>
</p>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<?php if (!$hasLocalCredential): ?>
    <!-- Nothing to change: the account authenticates through a provider (GD-23). -->
    <p class="text-body-secondary"><?= View::e($view->t('account.password.no_local_credential')) ?></p>
<?php else: ?>
    <form method="post" action="/account/password?action=change" class="clara-password-form" data-clara-password-form>
        <?= $view->csrfField() ?>

        <div class="mb-3">
            <label class="form-label" for="current-password"><?= View::e($view->t('account.password.current')) ?></label>
            <input class="form-control" type="password" id="current-password" name="current_password"
                   required autocomplete="current-password">
        </div>

        <div class="mb-2">
            <label class="form-label" for="new-password"><?= View::e($view->t('account.password.new')) ?></label>
            <input class="form-control" type="password" id="new-password" name="new_password"
                   required autocomplete="new-password" minlength="12" aria-describedby="password-policy">
        </div>
        <div class="mb-3" id="password-policy">
            <div class="form-text"><?= View::e($view->t('account.password.policy')) ?></div>
            <!-- Where the client's agreement check reports itself; the server checks again. -->
            <div class="form-text text-danger d-none" data-clara-password-mismatch>
                <?= View::e($view->t('account.password.mismatch')) ?>
            </div>
        </div>

        <div class="mb-3">
            <label class="form-label" for="repeat-password"><?= View::e($view->t('account.password.repeat')) ?></label>
            <input class="form-control" type="password" id="repeat-password" name="repeat_password"
                   required autocomplete="new-password" minlength="12">
        </div>

        <button class="btn btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
    </form>
<?php endif; ?>
