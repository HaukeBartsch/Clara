<?php
/**
 * Account page — Profile (§2.8, REQ-UI-063): who is signed in, the language selector (§9,
 * REQ-UI-029), the theme selector (§3.8, REQ-UI-041) and Sign out (REQ-UI-007). Both
 * selectors post to their dedicated routes and come back here; the choice applies from the
 * next page load. The name and address are read-only: identity changes go through an
 * administrator (REQ-AUTH-061).
 */

use Clara\View;

/** @var string $email */
?>
<h1 class="h4 mb-3"><?= View::e($view->t('account.section.profile')) ?></h1>

<dl class="row small mb-4 clara-account-identity">
    <dt class="col-sm-3 text-body-secondary"><?= View::e($view->t('account.profile.name')) ?></dt>
    <dd class="col-sm-9"><?= View::e($view->displayName()) ?></dd>
    <dt class="col-sm-3 text-body-secondary"><?= View::e($view->t('account.profile.email')) ?></dt>
    <dd class="col-sm-9"><?= View::e($email) ?></dd>
</dl>

<h2 class="h6"><?= View::e($view->t('account.profile.preferences')) ?></h2>
<div class="d-flex flex-column gap-2 mb-4">
    <form method="post" action="/lang" class="d-flex flex-wrap align-items-center gap-2">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="/account">
        <label class="form-label mb-0 clara-account-label" for="clara-language"><?= View::e($view->t('language.label')) ?></label>
        <select class="form-select form-select-sm w-auto" id="clara-language" name="language">
            <?php foreach ($view->languages() as $language): ?>
                <option value="<?= View::e($language['code']) ?>"<?= $language['code'] === $view->currentLanguage() ? ' selected' : '' ?>>
                    <?= View::e($language['display_name']) ?>
                </option>
            <?php endforeach; ?>
        </select>
        <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.save')) ?></button>
    </form>

    <form method="post" action="/theme" class="d-flex flex-wrap align-items-center gap-2">
        <?= $view->csrfField() ?>
        <input type="hidden" name="next" value="/account">
        <label class="form-label mb-0 clara-account-label" for="clara-theme"><?= View::e($view->t('theme.label')) ?></label>
        <select class="form-select form-select-sm w-auto" id="clara-theme" name="theme">
            <option value="default"<?= $view->selectedTheme() === null ? ' selected' : '' ?>>
                <?= View::e($view->t('theme.default')) ?>
            </option>
            <?php foreach ($view->installedThemes() as $theme): ?>
                <option value="<?= View::e($theme['id']) ?>"<?= $view->selectedTheme() === $theme['id'] ? ' selected' : '' ?>>
                    <?= View::e($theme['label']) ?>
                </option>
            <?php endforeach; ?>
        </select>
        <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.save')) ?></button>
    </form>
</div>

<form method="post" action="/logout">
    <?= $view->csrfField() ?>
    <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('nav.sign_out')) ?></button>
</form>
