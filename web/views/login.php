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
 * what this page offers, and no credential is in flight to protect. No name is preselected
 * and nothing renders below the picker until one is chosen; choosing submits at once
 * (assets/js/login.js) — the picker has no button, so the page requires JavaScript
 * (owner decision 2026-10-03, DEV-UI-13).
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
            <legend class="form-label h6 mb-1 visually-hidden"><?= View::e($view->t('login.source_help')) ?></legend>
            <?php
            // One tile per name, side by side (wrapping on narrow screens). Each tile is still
            // a real radio: the input is stretched invisibly over the tile inside its label,
            // so keyboard and screen reader behave as with a plain radio group. Once a name is
            // chosen the container carries clara-sources-chosen and the other tiles grey out
            // (app.css, "source tiles").
            ?>
            <?php
            // One hospital picture per tile, picked at random on every render: the icon list is
            // shuffled once for the page so tiles differ from each other, and wraps around if
            // more names than icons are configured.
            $icons = array_values(glob(__DIR__ . '/../assets/hospital_icons/*.png') ?: []);
            shuffle($icons);
            ?>
            <div class="clara-sources<?= !empty($chosen) ? ' clara-sources-chosen' : '' ?>">
                <?php foreach ($sources as $index => $source): ?>
                    <?php
                    // Only a name the user actually chose shows as chosen — never a default.
                    $checked = ($selected ?? '') !== '' && $selected === $source['name'];
                    ?>
                    <label class="clara-source" for="source-<?= $index ?>">
                        <input class="clara-source-input" type="radio" name="source" id="source-<?= $index ?>"
                               value="<?= View::e($source['name']) ?>"<?= $checked ? ' checked' : '' ?>>
                        <span class="clara-source-tile">
                            <?php if ($icons !== []): ?>
                                <img class="clara-source-icon" alt="" aria-hidden="true" width="28" height="28"
                                     src="/assets/hospital_icons/<?= rawurlencode(basename($icons[$index % count($icons)])) ?>">
                            <?php endif; ?>
                            <span class="clara-source-name"><?= View::e($source['name']) ?></span>
                        </span>
                    </label>
                <?php endforeach; ?>
            </div>
        </fieldset>
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

        <!-- "Forgot password?" sits with the form it applies to (§2.2). It is meaningful
             only for a local account, and that is not something this page can know about an
             address it has not seen — which is exactly why /password-reset answers the same
             way whatever it was asked (REQ-AUTH-062). -->
        <p class="text-center mt-2 mb-0">
            <a class="small" href="/password-reset"><?= View::e($view->t('login.forgot')) ?></a>
        </p>
    </form>
<?php elseif (empty($providers) && !empty($chosen)): ?>
    <!-- Nothing configured can verify a password. Saying so beats a form whose answer is
         always "not recognised" (§2.2). -->
    <p class="text-body-secondary mb-0"><?= View::e($view->t('login.no_credentials')) ?></p>
<?php endif; ?>
