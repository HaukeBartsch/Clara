<?php
/**
 * Control Panel — Translations (§5.7, REQ-UI-030). The key universe is the English catalog in
 * code (Plan/Web_Implementation.md §5 "English array in code, DB overlay") plus any key only
 * the language's overlay carries; each row shows the English source, the language's text, and
 * a "missing" badge where the key falls back to English (REQ-DB-031, REQ-UI-008). One form
 * saves the whole table: PHP sends only the entries that changed, and an emptied text removes
 * the translation (REQ-API-100). Every string is escaped on render (§3.2, REQ-UI-004).
 */

use Clara\View;

/** @var list<array{code: string, display_name: string}> $languages */
/** @var list<array{key: string, english: string, text: string, missing: bool}> $rows */
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
    <div class="form-check mb-1">
        <input class="form-check-input" type="checkbox" id="tr-missing" name="missing" value="1"<?= !empty($missingOnly) ? ' checked' : '' ?>>
        <label class="form-check-label small" for="tr-missing"><?= View::e($view->t('admin.translations.missing_only', ['count' => (string) $missingCount])) ?></label>
    </div>
    <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('admin.translations.show')) ?></button>
</form>

<?php if ($language !== ''): ?>
    <form method="post" action="/admin?section=translations&amp;action=save_translations">
        <?= $view->csrfField() ?>
        <input type="hidden" name="language" value="<?= View::e($language) ?>">
        <?php if (!empty($missingOnly)): ?>
            <input type="hidden" name="missing" value="1">
        <?php endif; ?>
        <div class="table-responsive">
            <table class="table table-sm align-middle clara-translations">
                <thead>
                <tr>
                    <th scope="col"><?= View::e($view->t('admin.translations.key')) ?></th>
                    <th scope="col"><?= View::e($view->t('admin.translations.english')) ?></th>
                    <th scope="col"><?= View::e($view->t('admin.translations.text')) ?></th>
                </tr>
                </thead>
                <tbody>
                <?php if ($rows === []): ?>
                    <tr><td colspan="3" class="clara-region-empty"><?= View::e($view->t('admin.translations.empty')) ?></td></tr>
                <?php endif; ?>
                <?php foreach ($rows as $i => $row): ?>
                    <tr class="<?= $row['missing'] ? 'clara-missing' : '' ?>">
                        <td class="small"><code><?= View::e($row['key']) ?></code>
                            <?php if ($row['missing']): ?>
                                <span class="badge text-bg-warning ms-1"><?= View::e($view->t('admin.translations.missing')) ?></span>
                            <?php endif; ?>
                        </td>
                        <td class="small text-body-secondary"><?= View::e($row['english']) ?></td>
                        <td>
                            <label class="visually-hidden" for="tr-<?= $i ?>"><?= View::e($row['key']) ?></label>
                            <input class="form-control form-control-sm" type="text" id="tr-<?= $i ?>"
                                   name="text[<?= View::e($row['key']) ?>]" value="<?= View::e($row['text']) ?>">
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <p class="small text-body-secondary"><?= View::e($view->t('admin.translations.help')) ?></p>
        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
    </form>
<?php endif; ?>
