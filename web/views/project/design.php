<?php
/**
 * Project page — Design (§7.1, REQ-UI-021, REQ-UI-067, project_admin): the project's
 * instruments in position order, with the controls that make and change them — **Add**,
 * **Edit** (name, survey flag, instrument branching), **Reorder** and **Remove** — and,
 * choosing one, its field editor (§7.2). These came from Setup block C (§6.2); Setup keeps
 * the instrument × event mapping, which is what decides where an instrument collects data.
 *
 * Each instrument's "Repeating" dropdown (REQ-UI-054) holds one switch per event it is
 * mapped to: on, the form can be filled out more than once for a record at that event
 * (REQ-API-154). Switching saves at once (design.js); without JavaScript the Save button does.
 *
 * Below the list, the instrument import (§7.6, REQ-UI-053): REDCap instrument ZIPs or
 * data-dictionary CSVs, previewed through the API's dry run before anything is written.
 *
 * Edit controls are emitted only while the mode allows edits (§6.7); reorder sends the full
 * ordered id list, and removing the last instrument resets it instead of deleting it
 * (REQ-API-127) — the confirmation says which will happen. The strings of the moved controls
 * keep their `setup.instrument.*` keys: they are the translated ones (`web/app/i18n/en.php`).
 *
 * Reads: $instruments, $repeating (DesignController::repeatingChoices), $editInstrument,
 * $designUrl, $setupUrl, $import (DesignController::importView, or null), $canEdit, and the
 * structure-mode partial's inputs.
 */

use Clara\View;

/** @var list<array<string, mixed>> $instruments */
$act = static fn (string $action): string => $designUrl . '?action=' . $action;
$lastInstrument = count($instruments) === 1;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('design.title')) ?></h1>

<?php require __DIR__ . '/../partials/structure-mode.php'; ?>

<p class="small text-body-secondary"><?= View::e($view->t('design.intro')) ?>
    <a href="<?= View::e($setupUrl) ?>"><?= View::e($view->t('nav.setup')) ?></a></p>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-design-instruments">
        <thead><tr>
            <th scope="col"><?= View::e($view->t('setup.instrument.name')) ?></th>
            <th scope="col" class="text-end"><?= View::e($view->t('setup.instrument.fields')) ?></th>
            <th scope="col"><?= View::e($view->t('setup.instrument.survey')) ?></th>
            <th scope="col"><?= View::e($view->t('setup.instrument.branching')) ?></th>
            <th scope="col"><?= View::e($view->t('design.repeating.column')) ?></th>
            <?php if ($canEdit): ?><th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th><?php endif; ?>
        </tr></thead>
        <tbody>
        <?php foreach ($instruments as $i => $instrument): ?>
            <?php $iid = (int) ($instrument['id'] ?? 0); ?>
            <tr data-instrument="<?= View::e($instrument['name'] ?? '') ?>">
                <td><a href="<?= View::e($designUrl . '/instruments/' . $iid) ?>"><?= View::e($instrument['name'] ?? '') ?></a></td>
                <td class="text-end"><?= (int) ($instrument['field_count'] ?? 0) ?></td>
                <td><?php if (!empty($instrument['is_survey'])): ?><span class="badge text-bg-info"><?= View::e($view->t('setup.instrument.survey_badge')) ?></span><?php endif; ?></td>
                <td><code class="small"><?= View::e($instrument['branching_logic'] ?? '') ?></code></td>
                <td><?php require __DIR__ . '/../partials/repeating-dropdown.php'; ?></td>
                <?php if ($canEdit): ?>
                    <td class="text-end text-nowrap">
                        <?php foreach (['up' => $i > 0, 'down' => $i < count($instruments) - 1] as $dir => $possible): ?>
                            <?php if ($possible): ?>
                                <form method="post" action="<?= View::e($act('move_instrument')) ?>" class="d-inline">
                                    <?= $view->csrfField() ?>
                                    <input type="hidden" name="instrument_id" value="<?= $iid ?>">
                                    <input type="hidden" name="direction" value="<?= $dir ?>">
                                    <button class="btn btn-sm btn-outline-secondary" type="submit"
                                            aria-label="<?= View::e($view->t('setup.move_' . $dir)) ?>"><?= $dir === 'up' ? '↑' : '↓' ?></button>
                                </form>
                            <?php endif; ?>
                        <?php endforeach; ?>
                        <a class="btn btn-sm btn-outline-primary" href="<?= View::e($designUrl . '?edit_instrument=' . $iid . '#instrument-edit') ?>"><?= View::e($view->t('setup.edit')) ?></a>
                        <form method="post" action="<?= View::e($act('delete_instrument')) ?>" class="d-inline"
                              data-clara-confirm="<?= View::e($view->t($lastInstrument ? 'setup.instrument.confirm_reset' : 'setup.instrument.confirm_delete',
                                  ['name' => (string) ($instrument['name'] ?? ''), 'count' => (string) (int) ($instrument['field_count'] ?? 0)])) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="instrument_id" value="<?= $iid ?>">
                            <input type="hidden" name="name" value="<?= View::e($instrument['name'] ?? '') ?>">
                            <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('setup.remove')) ?></button>
                        </form>
                    </td>
                <?php endif; ?>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<?php if ($canEdit): ?>
    <?php foreach ($instruments as $instrument): ?>
        <?php if ((int) ($instrument['id'] ?? 0) !== $editInstrument || $editInstrument === 0) {
            continue;
        } ?>
        <!-- Edit one instrument: name, survey flag (§7.5), instrument-level branching (§7.3). -->
        <form method="post" action="<?= View::e($act('update_instrument')) ?>" class="card card-body mb-3" id="instrument-edit">
            <?= $view->csrfField() ?>
            <input type="hidden" name="instrument_id" value="<?= (int) $instrument['id'] ?>">
            <div class="row g-2">
                <div class="col-md-6">
                    <label class="form-label mb-0 small" for="instrument-edit-name"><?= View::e($view->t('setup.instrument.name')) ?></label>
                    <input class="form-control form-control-sm" type="text" id="instrument-edit-name" name="name" required maxlength="255"
                           value="<?= View::e($instrument['name'] ?? '') ?>">
                </div>
                <div class="col-md-6 d-flex align-items-end">
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" id="instrument-edit-survey" name="is_survey" value="1"
                            <?= !empty($instrument['is_survey']) ? 'checked' : '' ?>>
                        <label class="form-check-label" for="instrument-edit-survey"><?= View::e($view->t('setup.instrument.survey_help')) ?></label>
                    </div>
                </div>
                <div class="col-12">
                    <?php
                    $exprId = 'instrument-edit-branching';
                    $exprName = 'branching_logic';
                    $exprValue = (string) ($instrument['branching_logic'] ?? '');
                    $exprGrammar = 'branching';
                    $exprLabel = $view->t('setup.instrument.branching');
                    $exprRefsUrl = $designUrl;
                    require __DIR__ . '/../partials/expression-editor.php';
                    ?>
                </div>
            </div>
            <div class="mt-2 d-flex gap-2">
                <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
                <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($designUrl) ?>"><?= View::e($view->t('action.cancel')) ?></a>
            </div>
        </form>
    <?php endforeach; ?>

    <form method="post" action="<?= View::e($act('add_instrument')) ?>" class="d-flex flex-wrap gap-2 align-items-end mb-4">
        <?= $view->csrfField() ?>
        <div>
            <label class="form-label mb-0 small" for="instrument-name"><?= View::e($view->t('setup.instrument.name')) ?></label>
            <input class="form-control form-control-sm" type="text" id="instrument-name" name="name" required maxlength="255">
        </div>
        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('setup.instrument.add')) ?></button>
    </form>
<?php endif; ?>

<?php if ($canEdit): ?>
<section class="card mb-4 clara-import" id="import" aria-labelledby="import-title">
    <div class="card-body">
        <h2 class="h5" id="import-title"><?= View::e($view->t('design.import.title')) ?></h2>
        <p class="small text-body-secondary"><?= View::e($view->t('design.import.intro')) ?></p>

        <?php if ($import === null): ?>
            <form method="post" action="<?= View::e($designUrl) ?>?action=import_preview" enctype="multipart/form-data"
                  class="d-flex flex-wrap gap-2 align-items-end" data-clara-import-form>
                <?= $view->csrfField() ?>
                <div>
                    <label class="form-label mb-0 small" for="import-files"><?= View::e($view->t('design.import.files')) ?></label>
                    <input class="form-control form-control-sm" type="file" id="import-files" name="import_files[]" multiple accept=".zip,.csv" required>
                </div>
                <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('design.import.preview')) ?></button>
            </form>
        <?php else: ?>
            <?php
            $result = $import['result'];
            $problems = is_array($result['problems'] ?? null) ? $result['problems'] : [];
            $warnings = is_array($result['warnings'] ?? null) ? $result['warnings'] : [];
            $identifier = is_array($result['record_identifier'] ?? null) ? $result['record_identifier'] : null;
            $place = static fn (array $note): string => trim(($note['file'] ?? '')
                . ((int) ($note['row'] ?? 0) > 0 ? ', ' . $view->t('design.import.row', ['row' => (string) (int) $note['row']]) : ''), ', ');
            ?>
            <h3 class="h6"><?= View::e($view->t('design.import.order')) ?></h3>
            <ol class="list-group list-group-numbered mb-3" data-clara-import-files>
                <?php foreach ($import['files'] as $i => $file): ?>
                    <li class="list-group-item d-flex align-items-center gap-2 py-1">
                        <span class="flex-grow-1 font-monospace small"><?= View::e($file) ?>
                            <?php if (isset($import['origins'][$file])): ?><span class="text-body-secondary">— <?= View::e($import['origins'][$file]) ?></span><?php endif; ?></span>
                        <?php foreach (['up' => '↑', 'down' => '↓'] as $direction => $arrow): ?>
                            <?php if (($direction === 'up' && $i > 0) || ($direction === 'down' && $i < count($import['files']) - 1)): ?>
                                <form method="post" action="<?= View::e($designUrl) ?>?action=import_reorder" class="d-inline">
                                    <?= $view->csrfField() ?>
                                    <input type="hidden" name="index" value="<?= (int) $i ?>">
                                    <input type="hidden" name="direction" value="<?= $direction ?>">
                                    <button class="btn btn-sm btn-outline-secondary py-0" type="submit"
                                            aria-label="<?= View::e($view->t('design.import.' . $direction, ['name' => $file])) ?>"><?= $arrow ?></button>
                                </form>
                            <?php endif; ?>
                        <?php endforeach; ?>
                    </li>
                <?php endforeach; ?>
            </ol>

            <?php if ($problems !== []): ?>
                <div class="alert alert-danger py-2" role="alert" data-clara-import-problems>
                    <div class="fw-semibold small mb-1"><?= View::e($view->t('design.import.problems')) ?></div>
                    <ul class="mb-0 small">
                        <?php foreach ($problems as $note): ?>
                            <li><?php if ($place($note) !== ''): ?><span class="font-monospace"><?= View::e($place($note)) ?></span>: <?php endif; ?><?= View::e($note['message'] ?? '') ?></li>
                        <?php endforeach; ?>
                    </ul>
                </div>
            <?php endif; ?>

            <?php if ($identifier !== null): ?>
                <p class="small" data-clara-import-identifier><?= View::e($view->t(!empty($identifier['moved']) ? 'design.import.identifier_moved' : 'design.import.identifier_kept',
                    ['field' => (string) ($identifier['field'] ?? ''), 'instrument' => (string) ($identifier['instrument'] ?? '')])) ?></p>
            <?php endif; ?>

            <?php foreach (is_array($result['instruments'] ?? null) ? $result['instruments'] : [] as $instrument): ?>
                <h3 class="h6 mt-3"><?= View::e($instrument['name'] ?? '') ?>
                    <?php if (!empty($instrument['existing'])): ?><span class="badge text-bg-info"><?= View::e($view->t('design.import.existing')) ?></span><?php endif; ?>
                    <?php if (($instrument['renamed_from'] ?? null) !== null): ?><span class="badge text-bg-warning"><?= View::e($view->t('design.import.renamed_from', ['name' => (string) $instrument['renamed_from']])) ?></span><?php endif; ?>
                    <span class="small text-body-secondary fw-normal font-monospace"><?= View::e($instrument['file'] ?? '') ?></span></h3>
                <div class="table-responsive">
                    <table class="table table-sm align-middle clara-import-fields">
                        <thead><tr>
                            <th scope="col" class="text-end"><?= View::e($view->t('design.import.col.row')) ?></th>
                            <th scope="col"><?= View::e($view->t('design.import.col.field')) ?></th>
                            <th scope="col"><?= View::e($view->t('design.import.col.type')) ?></th>
                            <th scope="col"><?= View::e($view->t('design.import.col.validation')) ?></th>
                            <th scope="col"><?= View::e($view->t('design.import.col.notes')) ?></th>
                        </tr></thead>
                        <tbody>
                        <?php foreach (is_array($instrument['fields'] ?? null) ? $instrument['fields'] : [] as $field): ?>
                            <?php $rowNotes = $import['notes'][($instrument['file'] ?? '') . '|' . (int) ($field['row'] ?? 0)] ?? []; ?>
                            <tr>
                                <td class="text-end small"><?= (int) ($field['row'] ?? 0) ?></td>
                                <td class="font-monospace small"><?= View::e($field['field_name'] ?? '') ?></td>
                                <td class="small"><?= View::e($field['source_type'] ?? '') ?> → <?= View::e($field['field_type'] ?? '') ?></td>
                                <td class="small"><?= View::e(trim(($field['validation_type'] ?? '') . ' ' . ($field['validation_format'] ?? ''))) ?></td>
                                <td class="small">
                                    <?php foreach ($rowNotes as $note): ?>
                                        <div><span class="badge text-bg-light border"><?= View::e($view->t('design.import.note.' . ($note['code'] ?? 'converted'))) ?></span>
                                            <?= View::e($note['message'] ?? '') ?></div>
                                    <?php endforeach; ?>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            <?php endforeach; ?>

            <?php $unplaced = array_filter($warnings, static fn (array $n): bool => in_array($n['code'] ?? '', ['skipped', 'ignored_entry'], true) || (int) ($n['row'] ?? 0) === 0); ?>
            <?php if ($unplaced !== []): ?>
                <div class="alert alert-warning py-2 small" data-clara-import-warnings>
                    <div class="fw-semibold mb-1"><?= View::e($view->t('design.import.warnings')) ?></div>
                    <ul class="mb-0">
                        <?php foreach ($unplaced as $note): ?>
                            <li><?php if ($place($note) !== ''): ?><span class="font-monospace"><?= View::e($place($note)) ?></span>: <?php endif; ?><?= View::e($note['message'] ?? '') ?></li>
                        <?php endforeach; ?>
                    </ul>
                </div>
            <?php endif; ?>

            <p class="small text-body-secondary"><?= View::e($view->t('design.import.no_mapping')) ?></p>
            <div class="d-flex gap-2">
                <form method="post" action="<?= View::e($designUrl) ?>?action=import_instruments">
                    <?= $view->csrfField() ?>
                    <button class="btn btn-sm btn-primary" type="submit"<?= empty($result['importable']) ? ' disabled' : '' ?>><?= View::e($view->t('design.import.submit')) ?></button>
                </form>
                <form method="post" action="<?= View::e($designUrl) ?>?action=import_cancel">
                    <?= $view->csrfField() ?>
                    <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('action.cancel')) ?></button>
                </form>
            </div>
        <?php endif; ?>
    </div>
</section>
<?php endif; ?>
