<?php
/**
 * Control Panel — Translations (§5.7, REQ-UI-030). The key table is a Tabulator-backed data
 * region (AGENTS.md "use Tabulator"; REQ-UI-044): this page renders the language picker, the
 * filter controls and an empty container, and `web/assets/js/translations.js` binds the rows
 * from this route's JSON region — the key universe is the English catalog in code plus any
 * key only the language's overlay carries, each with a "missing" badge where the key falls
 * back to English (REQ-DB-031, REQ-UI-008). Edits are collected into the plain POST form on
 * save: a CSRF-protected PRG mutation (§3.3), sending only changed entries, an emptied text
 * removing the translation (REQ-API-100). Every string is escaped on render (§3.2, REQ-UI-004).
 */

use Clara\View;

/** @var list<array{code: string, display_name: string}> $languages */
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.translations.title')) ?></h1>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<form method="get" action="/admin" class="d-flex flex-wrap gap-2 align-items-end mb-3">
    <input type="hidden" name="section" value="translations">
    <div>
        <label class="form-label mb-0 small" for="tr-language"><?= View::e($view->t('admin.translations.language')) ?></label>
        <select class="form-select form-select-sm" id="tr-language" name="language">
            <?php foreach ($languages as $lang): ?>
                <option value="<?= View::e($lang['code']) ?>"<?= $lang['code'] === $language ? ' selected' : '' ?>><?= View::e($lang['display_name'] . ' (' . $lang['code'] . ')') ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('admin.translations.show')) ?></button>
</form>

<?php if ($language !== ''): ?>
    <form method="post" action="/admin?section=translations&amp;action=save_translations" id="tr-form">
        <?= $view->csrfField() ?>
        <input type="hidden" name="language" value="<?= View::e($language) ?>">

        <div class="d-flex flex-wrap gap-2 align-items-center mb-2">
            <label class="visually-hidden" for="tr-search"><?= View::e($view->t('admin.translations.search')) ?></label>
            <input class="form-control form-control-sm w-auto flex-grow-1" id="tr-search" type="search"
                   placeholder="<?= View::e($view->t('admin.translations.search')) ?>" autocomplete="off">
            <div class="form-check mb-0">
                <input class="form-check-input" type="checkbox" id="tr-missing" value="1"<?= !empty($missingOnly) ? ' checked' : '' ?>>
                <label class="form-check-label small" for="tr-missing"><?= View::e($view->t('admin.translations.missing_only')) ?></label>
            </div>
            <span class="small text-body-secondary" id="tr-status"></span>
            <button class="btn btn-sm btn-primary ms-auto" type="submit" id="tr-save" disabled><?= View::e($view->t('action.save')) ?></button>
        </div>

        <div id="clara-translations" data-region="translations"
             data-language="<?= View::e($language) ?>"<?= !empty($missingOnly) ? ' data-missing-only="1"' : '' ?>>
            <div class="clara-region-empty py-3"><?= View::e($view->t('js.loading')) ?></div>
        </div>

        <p class="small text-body-secondary mt-2"><?= View::e($view->t('admin.translations.help')) ?></p>
    </form>
<?php endif; ?>
