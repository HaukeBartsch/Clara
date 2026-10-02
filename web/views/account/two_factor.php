<?php

/**
 * The self-service second factor (§2.5, REQ-UI-039): the current method, the two ways to
 * enable one, and disable behind a fresh code.
 *
 * The secret and the recovery codes arrive from the API response of the step that created
 * them and appear on no other page — reloading shows the status view instead (REQ-AUTH-056),
 * which is why the copy says so rather than leaving the user to find out.
 *
 * As in the login wizard, the `otpauth://` URI is offered as text: a scannable QR needs a
 * client-side generator vendored under assets/vendor/, and admitting one is an amendment to
 * `Design/Technology_Stack_Design.md` §3 first (REQ-TECH-026). The manual key works with
 * every authenticator app in the meantime.
 */

use Clara\View;

/** @var array{method: string, enrolled_at: string, recovery_codes_remaining: int} $tfa */
$tfa = $tfa ?? ['method' => 'off', 'enrolled_at' => '', 'recovery_codes_remaining' => 0];
$step = $step ?? '';
?>
<h1 class="h4 mb-3"><?= View::e($view->t('tfa.heading')) ?></h1>

<!-- Current method (§2.5). -->
<div class="mb-4">
    <div class="d-flex align-items-center gap-2">
        <span class="text-body-secondary"><?= View::e($view->t('tfa.current_method')) ?></span>
        <?php if ($tfa['method'] === 'off'): ?>
            <span class="badge text-bg-secondary"><?= View::e($view->t('tfa.method.off')) ?></span>
        <?php elseif ($tfa['method'] === 'totp'): ?>
            <span class="badge text-bg-success"><?= View::e($view->t('tfa.method.totp')) ?></span>
        <?php else: ?>
            <span class="badge text-bg-success"><?= View::e($view->t('tfa.method.email')) ?></span>
        <?php endif; ?>
    </div>
    <?php if ($tfa['method'] !== 'off' && $tfa['enrolled_at'] !== ''): ?>
        <div class="small text-body-secondary mt-1">
            <?= View::e($view->t('tfa.enrolled_on', ['when' => $tfa['enrolled_at']])) ?>
            <?php if ($tfa['recovery_codes_remaining'] > 0): ?>
                · <?= View::e($view->t('tfa.codes_remaining', ['count' => $tfa['recovery_codes_remaining']])) ?>
            <?php endif; ?>
        </div>
    <?php endif; ?>
</div>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<?php if ($step === 'done'): ?>
    <!-- Recovery codes: this response is the only place they exist. -->
    <div class="alert alert-warning py-2 small" role="alert">
        <?= View::e($view->t('tfa.recovery_warning')) ?>
    </div>
    <div class="font-monospace small bg-body-tertiary rounded p-3 mb-2 clara-recovery-codes user-select-all"
         id="clara-recovery-codes">
        <?php foreach (($recoveryCodes ?? []) as $code): ?>
            <div><?= View::e($code) ?></div>
        <?php endforeach; ?>
    </div>
    <button class="btn btn-sm btn-outline-secondary mb-3" type="button" data-clara-copy="#clara-recovery-codes">
        <?= View::e($view->t('tfa.copy')) ?>
    </button>
    <form method="post" action="/account/two-factor?action=enroll_done">
        <?= $view->csrfField() ?>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('tfa.saved')) ?></button>
    </form>

<?php elseif ($step === 'totp'): ?>
    <ol class="ps-3 small text-body-secondary">
        <li><?= View::e($view->t('tfa.totp_step1')) ?></li>
        <li><?= View::e($view->t('tfa.totp_step2')) ?></li>
    </ol>

    <?php if (($secret ?? '') !== ''): ?>
        <div class="mb-3">
            <div class="form-label"><?= View::e($view->t('tfa.manual_key')) ?></div>
            <output id="clara-tfa-secret" class="d-block font-monospace user-select-all bg-body-tertiary rounded p-2"><?= View::e($secret) ?></output>
            <button class="btn btn-sm btn-outline-secondary mt-1" type="button" data-clara-copy="#clara-tfa-secret">
                <?= View::e($view->t('tfa.copy')) ?>
            </button>
            <?php if (($otpauthUri ?? '') !== ''): ?>
                <div class="form-text text-break mt-2"><?= View::e($view->t('tfa.setup_link')) ?>
                    <span class="font-monospace"><?= View::e($otpauthUri) ?></span>
                </div>
            <?php endif; ?>
        </div>
    <?php else: ?>
        <!-- A wrong code does not earn the key a second showing (§2.5): another code from
             the app still works, and somebody who never added it starts over. -->
        <p class="small text-body-secondary"><?= View::e($view->t('tfa.key_shown_once')) ?></p>
    <?php endif; ?>

    <form method="post" action="/account/two-factor?action=totp_confirm">
        <?= $view->csrfField() ?>
        <div class="mb-3">
            <label class="form-label" for="tfa-code"><?= View::e($view->t('tfa.code')) ?></label>
            <input class="form-control font-monospace text-center" type="text" id="tfa-code" name="code"
                   required autocomplete="one-time-code" inputmode="numeric" maxlength="16">
        </div>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('tfa.confirm')) ?></button>
    </form>
    <form method="post" action="/account/two-factor?action=enroll_cancel" class="mt-2">
        <?= $view->csrfField() ?>
        <button class="btn btn-link btn-sm px-0 text-body-secondary" type="submit"><?= View::e($view->t('tfa.cancel')) ?></button>
    </form>

<?php elseif ($step === 'email'): ?>
    <p class="small text-body-secondary"><?= View::e($view->t('tfa.email_help', ['email' => $view->email()])) ?></p>
    <form method="post" action="/account/two-factor?action=email_confirm">
        <?= $view->csrfField() ?>
        <div class="mb-3">
            <label class="form-label" for="tfa-code"><?= View::e($view->t('tfa.code')) ?></label>
            <input class="form-control font-monospace text-center" type="text" id="tfa-code" name="code"
                   required autocomplete="one-time-code" inputmode="numeric" maxlength="16">
        </div>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('tfa.confirm')) ?></button>
    </form>
    <form method="post" action="/account/two-factor?action=email_start" class="mt-2">
        <?= $view->csrfField() ?>
        <button class="btn btn-link btn-sm px-0" type="submit"><?= View::e($view->t('tfa.resend')) ?></button>
    </form>
    <form method="post" action="/account/two-factor?action=enroll_cancel" class="mt-2">
        <?= $view->csrfField() ?>
        <button class="btn btn-link btn-sm px-0 text-body-secondary" type="submit"><?= View::e($view->t('tfa.cancel')) ?></button>
    </form>

<?php elseif ($tfa['method'] === 'off'): ?>
    <!-- Nothing enabled yet: offer the methods this installation can deliver (§2.5). -->
    <p class="text-body-secondary small mb-3"><?= View::e($view->t('tfa.enable_help')) ?></p>
    <form method="post" action="/account/two-factor?action=totp_start" class="mb-2">
        <?= $view->csrfField() ?>
        <button class="btn btn-primary w-100" type="submit"><?= View::e($view->t('tfa.start_totp')) ?></button>
    </form>
    <?php if ($emailAvailable ?? true): ?>
        <form method="post" action="/account/two-factor?action=email_start">
            <?= $view->csrfField() ?>
            <button class="btn btn-outline-primary w-100" type="submit"><?= View::e($view->t('tfa.start_email')) ?></button>
        </form>
    <?php else: ?>
        <p class="small text-body-secondary mb-0"><?= View::e($view->t('tfa.email_unavailable')) ?></p>
    <?php endif; ?>

<?php else: ?>
    <!-- A factor is on: disable asks for a current code or a recovery code (§2.5). -->
    <h2 class="h6 mt-4"><?= View::e($view->t('tfa.disable_heading')) ?></h2>
    <p class="small text-body-secondary"><?= View::e($view->t('tfa.disable_help')) ?></p>
    <form method="post" action="/account/two-factor?action=disable">
        <?= $view->csrfField() ?>
        <div class="mb-3">
            <label class="form-label" for="tfa-disable-code"><?= View::e($view->t('tfa.disable_code')) ?></label>
            <input class="form-control font-monospace text-center" type="text" id="tfa-disable-code" name="code"
                   required autocomplete="one-time-code" maxlength="16">
        </div>
        <button class="btn btn-outline-danger w-100" type="submit"><?= View::e($view->t('tfa.disable')) ?></button>
    </form>
<?php endif; ?>
