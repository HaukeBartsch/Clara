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
/** @var array<string, mixed>|null $region the §4.30 region state, null when it could not be read */
/** @var array<string, string> $regionOptions code => display text, REQ-DB-006 order */
/** @var callable(string): string $regionLabel */
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

    <?php if ($region !== null): ?>
        <?php
        // Preferred region (REQ-UI-075): Automatic first, saying what it currently means; a stored
        // own choice the enumeration no longer offers stays selectable as an extra option.
        $own = is_string($region['preferred_region'] ?? null) ? $region['preferred_region'] : '';
        $automatic = is_string($region['automatic_region'] ?? null) ? $region['automatic_region'] : '';
        $source = ($region['automatic_region_source'] ?? '') === 'assignment' ? 'assignment' : 'provider';
        $automaticText = $automatic === ''
            ? $view->t('account.region.automatic.none')
            : $view->t('account.region.automatic.' . $source, ['region' => $regionLabel($automatic)]);
        if ($own !== '' && !array_key_exists($own, $regionOptions)) {
            $regionOptions[$own] = $own;
        }
        ?>
        <form method="post" action="/account?action=set_region" class="d-flex flex-wrap align-items-center gap-2">
            <?= $view->csrfField() ?>
            <label class="form-label mb-0 clara-account-label" for="clara-region"><?= View::e($view->t('account.region.label')) ?></label>
            <select class="form-select form-select-sm w-auto" id="clara-region" name="region" aria-describedby="clara-region-help">
                <option value=""<?= $own === '' ? ' selected' : '' ?>><?= View::e($automaticText) ?></option>
                <?php foreach ($regionOptions as $code => $text): ?>
                    <option value="<?= View::e($code) ?>"<?= $own === $code ? ' selected' : '' ?>><?= View::e($text) ?></option>
                <?php endforeach; ?>
            </select>
            <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.save')) ?></button>
            <div class="form-text w-100" id="clara-region-help"><?= View::e($view->t('account.region.help')) ?></div>
        </form>
    <?php else: ?>
        <p class="small text-body-secondary mb-0"><?= View::e($view->t('account.region.unavailable')) ?></p>
    <?php endif; ?>
</div>

<form method="post" action="/logout">
    <?= $view->csrfField() ?>
    <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('nav.sign_out')) ?></button>
</form>
