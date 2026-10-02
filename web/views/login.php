<?php
/**
 * The login page (§2.2). What it offers depends on the selected source name: the email +
 * password form where a local account or a directory can verify it — the only path on a
 * first installation without an identity provider (GD-18, REQ-AUTH-051) — and one button
 * per OAuth2 provider whose login needs a browser round trip (REQ-AUTH-066).
 *
 * The source picker appears only when more than one distinct name is configured
 * (REQ-UI-042); with one name, or none named anywhere, that name applies implicitly and
 * the picker is skipped (REQ-AUTH-067). Choosing a name is a POST of its own: it changes
 * what this page offers, and no credential is in flight to protect. The button below the
 * radios is the same action for a browser without JavaScript (assets/js/login.js submits
 * it on selection), because nothing here may depend on script running (§2.4).
 *
 * The "Forgot password?" link arrives with the /password-reset route in M2 — until then it
 * is absent rather than a link to a page that does not answer.
 *
 * The password travels browser → PHP over TLS only, is never stored or logged, and is
 * never re-rendered into the page (REQ-AUTH-036).
 */

use Clara\View;
?>
<h1 class="h4 mb-1"><?= View::e($view->t('login.heading')) ?></h1>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2 mt-3" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<?php if (!empty($sources)): ?>
    <form method="post" action="/login?action=source" data-clara-source-picker>
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="<?= View::e($next ?? '/') ?>">

        <fieldset class="mb-2">
            <legend class="form-label h6 mb-1"><?= View::e($view->t('login.source')) ?></legend>
            <p class="text-body-secondary small mt-0"><?= View::e($view->t('login.source_help')) ?></p>
            <?php foreach ($sources as $index => $source): ?>
                <?php
                // The stored selection wins; with none yet, the first name is what the
                // form would submit anyway, so it is what shows as chosen.
                $checked = ($selected ?? '') !== ''
                    ? $selected === $source['name']
                    : $index === 0;
                ?>
                <div class="form-check">
                    <input class="form-check-input" type="radio" name="source" id="source-<?= $index ?>"
                           value="<?= View::e($source['name']) ?>"<?= $checked ? ' checked' : '' ?>>
                    <label class="form-check-label" for="source-<?= $index ?>">
                        <?= View::e($source['name']) ?>
                    </label>
                </div>
            <?php endforeach; ?>
        </fieldset>

        <button class="btn btn-outline-secondary btn-sm" type="submit">
            <?= View::e($view->t('login.source.continue')) ?>
        </button>
    </form>
<?php endif; ?>

<?php if (!empty($providers)): ?>
    <!-- One form per provider, because each starts its own authorization-code round trip
         (Sequence A). They are never part of the credential race (§2.9). -->
    <div class="d-grid gap-2 <?= empty($offersForm) ? '' : 'mb-3' ?>">
        <?php foreach ($providers as $provider): ?>
            <form method="post" action="/login?action=oauth">
                <?= $view->csrfField() ?>
                <input type="hidden" name="provider" value="<?= View::e((string) $provider['index']) ?>">
                <button class="btn btn-outline-primary" type="submit">
                    <?= View::e($view->t('login.provider.submit', ['provider' => (string) $provider['label']])) ?>
                </button>
            </form>
        <?php endforeach; ?>
    </div>
<?php endif; ?>

<?php if (!empty($offersForm)): ?>
    <form method="post" action="/login?action=credentials" autocomplete="on">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="<?= View::e($next ?? '/') ?>">

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
<?php elseif (empty($providers)): ?>
    <!-- Nothing configured can verify a password. Saying so beats a form whose answer is
         always "not recognised" (§2.2). -->
    <p class="text-body-secondary mb-0"><?= View::e($view->t('login.no_credentials')) ?></p>
<?php endif; ?>
