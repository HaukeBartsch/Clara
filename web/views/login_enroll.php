<?php
/**
 * The enrollment wizard a mandate opens before login completes (§2.7, REQ-AUTH-059,
 * rendered per REQ-UI-039). Four steps in one template: choose a method, set up
 * TOTP, confirm the email code, and read the recovery codes once.
 *
 * The secret and the recovery codes reach this page from the API response of the
 * step that produced them and are stored nowhere (REQ-AUTH-056): reloading loses
 * them, which is what "shown exactly once" means, and the copy says so rather
 * than leaving the user to discover it.
 *
 * The `otpauth://` link is offered as text. A scannable QR needs a client-side
 * generator vendored under assets/vendor/ (§2.5 of the UI design), and admitting
 * one is an amendment to Technology_Stack_Design.md §3 — until that happens the
 * manual key below is the way in, and it works with every authenticator app.
 */

use Clara\View;

$step = $step ?? 'choose';
$email = $email ?? '';
$next = $next ?? '/';
?>
<h1 class="h4 mb-1"><?= View::e($view->t('login.enroll.heading')) ?></h1>

<p class="text-body-secondary small mb-3">
    <?= View::e($step === 'done'
        ? $view->t('login.enroll.done_help')
        : $view->t('login.enroll.help', ['email' => $email])) ?>
</p>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<?php if ($step === 'choose'): ?>
    <!-- Email only where a relay exists: the API answers 409 smtp_not_configured
         and the panel says so (REQ-AUTH-057). -->
    <form method="post" action="/login?action=enroll_totp" class="mb-2">
        <?= $view->csrfField() ?>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.enroll.totp')) ?></button>
    </form>
    <form method="post" action="/login?action=enroll_email">
        <?= $view->csrfField() ?>
        <button class="btn btn-outline-primary w-100" type="submit"><?= View::e($view->t('login.enroll.email')) ?></button>
    </form>

<?php elseif ($step === 'totp'): ?>
    <ol class="ps-3 small text-body-secondary">
        <li><?= View::e($view->t('login.enroll.totp_step1')) ?></li>
        <li><?= View::e($view->t('login.enroll.totp_step2')) ?></li>
    </ol>

    <div class="mb-3">
        <div class="form-label"><?= View::e($view->t('login.enroll.manual_key')) ?></div>
        <output class="d-block font-monospace user-select-all bg-body-tertiary rounded p-2"><?= View::e($secret ?? '') ?></output>
        <?php if (($otpauthUri ?? '') !== ''): ?>
            <div class="form-text text-break"><?= View::e($view->t('login.enroll.setup_link')) ?>
                <span class="font-monospace"><?= View::e($otpauthUri) ?></span>
            </div>
        <?php endif; ?>
    </div>

    <form method="post" action="/login?action=enroll_totp_confirm">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="<?= View::e($next) ?>">
        <div class="mb-3">
            <label class="form-label" for="enroll-code"><?= View::e($view->t('login.enroll.code')) ?></label>
            <input class="form-control font-monospace text-center" type="text" id="enroll-code" name="code"
                   required autocomplete="one-time-code" inputmode="numeric" maxlength="6" pattern="[0-9]{6}">
        </div>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.enroll.confirm')) ?></button>
    </form>

<?php elseif ($step === 'email'): ?>
    <form method="post" action="/login?action=enroll_email_confirm">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="<?= View::e($next) ?>">
        <div class="mb-3">
            <label class="form-label" for="enroll-code"><?= View::e($view->t('login.enroll.code')) ?></label>
            <input class="form-control font-monospace text-center" type="text" id="enroll-code" name="code"
                   required autocomplete="one-time-code" inputmode="numeric" maxlength="6" pattern="[0-9]{6}">
            <div class="form-text"><?= View::e($view->t('login.enroll.email_help', ['email' => $email])) ?></div>
        </div>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.enroll.confirm')) ?></button>
    </form>

<?php elseif ($step === 'done'): ?>
    <div class="alert alert-warning py-2 small" role="alert">
        <?= View::e($view->t('login.enroll.recovery_warning')) ?>
    </div>
    <div class="font-monospace small bg-body-tertiary rounded p-3 mb-3 clara-recovery-codes user-select-all">
        <?php foreach (($recoveryCodes ?? []) as $code): ?>
            <div><?= View::e($code) ?></div>
        <?php endforeach; ?>
    </div>
    <form method="post" action="/login?action=enroll_done">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="<?= View::e($next) ?>">
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('login.enroll.saved')) ?></button>
    </form>
<?php endif; ?>

<?php if ($step !== 'done'): ?>
    <form method="post" action="/login?action=abort" class="mt-3 text-center">
        <?= $view->csrfField() ?>
        <button class="btn btn-link btn-sm px-0 text-body-secondary" type="submit"><?= View::e($view->t('login.mfa.abort')) ?></button>
    </form>
<?php endif; ?>
