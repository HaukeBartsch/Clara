<?php
/**
 * The two-factor panel (§2.2, REQ-UI-038). One code field with method-specific
 * help, a resend action for the email method, and alternate entry for a recovery
 * code. Nothing else on the site is reachable while this state stands (§2.7) —
 * which is why there is no navigation here, not even a link home.
 *
 * The code travels to PHP over TLS and onward to the API's login call; it is
 * never stored, never re-rendered after a failure, and never logged
 * (REQ-AUTH-036/058). A recovery code is accepted by the same field — the API
 * decides which kind it was — so `?recovery=1` changes only what the form asks
 * for, never what it may do.
 */

use Clara\View;

$isEmail = ($method ?? '') === 'email';
$recovery = (bool) ($recovery ?? false);
$next = $next ?? '/';
?>
<h1 class="h4 mb-1"><?= View::e($view->t('login.mfa.heading')) ?></h1>

<p class="text-body-secondary small mb-3">
    <?= View::e($recovery
        ? $view->t('login.mfa.help_recovery')
        : ($isEmail
            ? $view->t('login.mfa.help_email', ['email' => $email ?? ''])
            : $view->t('login.mfa.help_totp'))) ?>
</p>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<form method="post" action="/login?action=mfa" class="mt-2">
    <?= $view->csrfField() ?>
    <input type="hidden" name="next" value="<?= View::e($next) ?>">

    <div class="mb-3">
        <label class="form-label" for="login-code"><?= View::e($view->t('login.mfa.code')) ?></label>
        <input class="form-control form-control-lg text-center font-monospace"
               type="text" id="login-code" name="code" required autofocus
               autocomplete="one-time-code" inputmode="<?= $recovery ? 'text' : 'numeric' ?>"
               <?php if (!$recovery): ?>maxlength="6" pattern="[0-9]{6}"<?php endif; ?>
               placeholder="<?= View::e($recovery ? 'abc123-def4567890' : '123456') ?>">
        <div class="form-text">
            <?php if ($recovery): ?>
                <a href="/login"><?= View::e($view->t('login.mfa.use_app_code')) ?></a>
            <?php else: ?>
                <a href="/login?recovery=1"><?= View::e($view->t('login.mfa.use_recovery_code')) ?></a>
            <?php endif; ?>
        </div>
    </div>

    <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.mfa.submit')) ?></button>
</form>

<div class="d-flex justify-content-between mt-3">
    <?php if ($isEmail): ?>
        <!-- The API sends the code and rate-limits it per account; this only asks
             again (REQ-AUTH-057/058). -->
        <form method="post" action="/login?action=mfa_resend">
            <?= $view->csrfField() ?>
            <input type="hidden" name="next" value="<?= View::e($next) ?>">
            <button class="btn btn-link btn-sm px-0" type="submit"><?= View::e($view->t('login.mfa.resend')) ?></button>
        </form>
    <?php else: ?>
        <span></span>
    <?php endif; ?>

    <form method="post" action="/login?action=abort">
        <?= $view->csrfField() ?>
        <button class="btn btn-link btn-sm px-0 text-body-secondary" type="submit"><?= View::e($view->t('login.mfa.abort')) ?></button>
    </form>
</div>
