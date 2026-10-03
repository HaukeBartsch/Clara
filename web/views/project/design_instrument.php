<?php
/**
 * Project page — the field editor of one instrument (§7.2, REQ-UI-021/022/023, project_admin).
 *
 * An ordered field table, and below it the edit form of the field picked with `?field=<id>`
 * (`?field=new` adds one at the end). The form carries the full attribute set of §7.2; the
 * type-specific parts (choices, min/max, date format, matrix group, calculation) are all in
 * the markup and `design.js` shows the ones the chosen type uses — without JavaScript every
 * part is simply visible, and the API ignores what does not apply (REQ-VAL-020).
 *
 * Reads: $instrument, $fields, $selected (null | field, id 0 = new), $choiceRows,
 * $validationTypes, $fieldTypes, $choiceTypes, $identifierTypes, $isCalculated, $records,
 * $test, $editorUrl, $designUrl, $canEdit, and the structure-mode partial's inputs.
 */

use Clara\View;

/** @var list<array<string, mixed>> $fields */
$act = static fn (string $action): string => $editorUrl . '?action=' . $action;
$instrumentName = (string) ($instrument['name'] ?? '');
$nullable = static fn (mixed $v): string => $v === null ? '' : (string) $v;
?>
<nav class="small mb-1" aria-label="<?= View::e($view->t('design.title')) ?>">
    <a href="<?= View::e($designUrl) ?>"><?= View::e($view->t('design.all_instruments')) ?></a>
</nav>
<h1 class="h4 mb-3"><?= View::e($view->t('design.instrument_title', ['name' => $instrumentName])) ?></h1>

<?php require __DIR__ . '/../partials/structure-mode.php'; ?>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-fields">
        <thead><tr>
            <th scope="col" class="text-end">#</th>
            <th scope="col"><?= View::e($view->t('design.field.name')) ?></th>
            <th scope="col"><?= View::e($view->t('design.field.label')) ?></th>
            <th scope="col"><?= View::e($view->t('design.field.type')) ?></th>
            <th scope="col"><?= View::e($view->t('design.field.section_header')) ?></th>
            <th scope="col"><?= View::e($view->t('design.field.required')) ?></th>
            <?php if ($canEdit): ?><th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th><?php endif; ?>
        </tr></thead>
        <tbody>
        <?php if ($fields === []): ?>
            <tr><td colspan="7" class="clara-region-empty"><?= View::e($view->t('design.field.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($fields as $i => $field): ?>
            <?php $fid = (int) ($field['id'] ?? 0); ?>
            <tr data-field="<?= View::e($field['field_name'] ?? '') ?>"<?= $selected !== null && (int) $selected['id'] === $fid ? ' class="table-active"' : '' ?>>
                <td class="text-end"><?= (int) ($field['position'] ?? 0) ?></td>
                <td><code><?= View::e($field['field_name'] ?? '') ?></code></td>
                <td><?= View::e($field['field_label'] ?? '') ?></td>
                <td><?= View::e($field['field_type'] ?? '') ?><?php if (($field['validation_type'] ?? '') !== ''): ?>
                        <span class="text-body-secondary small">(<?= View::e($field['validation_type']) ?>)</span><?php endif; ?></td>
                <td class="small"><?= View::e($field['section_header'] ?? '') ?></td>
                <td><?php if (!empty($field['required'])): ?><span class="badge text-bg-secondary"><?= View::e($view->t('design.field.required')) ?></span><?php endif; ?></td>
                <?php if ($canEdit): ?>
                    <td class="text-end text-nowrap">
                        <?php foreach (['up' => $i > 0, 'down' => $i < count($fields) - 1] as $dir => $possible): ?>
                            <?php if ($possible): ?>
                                <form method="post" action="<?= View::e($act('move_field')) ?>" class="d-inline">
                                    <?= $view->csrfField() ?>
                                    <input type="hidden" name="field_id" value="<?= $fid ?>">
                                    <input type="hidden" name="direction" value="<?= $dir ?>">
                                    <button class="btn btn-sm btn-outline-secondary" type="submit"
                                            aria-label="<?= View::e($view->t('setup.move_' . $dir)) ?>"><?= $dir === 'up' ? '↑' : '↓' ?></button>
                                </form>
                            <?php endif; ?>
                        <?php endforeach; ?>
                        <a class="btn btn-sm btn-outline-primary" href="<?= View::e($editorUrl . '?field=' . $fid . '#field-form') ?>"><?= View::e($view->t('setup.edit')) ?></a>
                        <form method="post" action="<?= View::e($act('delete_field')) ?>" class="d-inline"
                              data-clara-confirm="<?= View::e($view->t('design.field.confirm_delete', ['name' => (string) ($field['field_name'] ?? '')])) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="field_id" value="<?= $fid ?>">
                            <input type="hidden" name="name" value="<?= View::e($field['field_name'] ?? '') ?>">
                            <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('setup.remove')) ?></button>
                        </form>
                    </td>
                <?php endif; ?>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<?php if ($canEdit && ($selected === null || (int) $selected['id'] !== 0)): ?>
    <a class="btn btn-sm btn-primary mb-3" href="<?= View::e($editorUrl . '?field=new#field-form') ?>"><?= View::e($view->t('design.field.add')) ?></a>
<?php endif; ?>

<?php if ($selected !== null && $canEdit): ?>
    <?php
    $isNew = (int) $selected['id'] === 0;
    $type = (string) ($selected['field_type'] ?? 'text');
    $vt = (string) ($selected['validation_type'] ?? '');
    $name = (string) ($selected['field_name'] ?? '');
    ?>
    <!-- The edit form (§7.2): every attribute of the data dictionary (REQ-DB-013). -->
    <form method="post" action="<?= View::e($act('save_field')) ?>" class="card card-body mb-4" id="field-form" data-clara-field-form>
        <?= $view->csrfField() ?>
        <input type="hidden" name="field_id" value="<?= (int) $selected['id'] ?>">
        <h2 class="h6"><?= View::e($isNew ? $view->t('design.field.add') : $view->t('design.field.edit', ['name' => $name])) ?></h2>

        <div class="row g-2">
            <div class="col-md-4">
                <label class="form-label mb-0 small" for="field-name"><?= View::e($view->t('design.field.name')) ?></label>
                <input class="form-control form-control-sm font-monospace" type="text" id="field-name" name="field_name" required
                       maxlength="100" pattern="[a-z0-9_]+" value="<?= View::e($name) ?>" data-clara-field-name>
                <!-- A warning, never a refusal: names over 26 characters are accepted (REQ-VAL-013). -->
                <div class="form-text text-warning-emphasis<?= strlen($name) > 26 ? '' : ' d-none' ?>" data-clara-name-warning>
                    <?= View::e($view->t('design.name_long')) ?></div>
            </div>
            <div class="col-md-5">
                <label class="form-label mb-0 small" for="field-label"><?= View::e($view->t('design.field.label')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-label" name="field_label" value="<?= View::e($selected['field_label'] ?? '') ?>">
            </div>
            <div class="col-md-3">
                <label class="form-label mb-0 small" for="field-type"><?= View::e($view->t('design.field.type')) ?></label>
                <select class="form-select form-select-sm" id="field-type" name="field_type" data-clara-field-type>
                    <?php foreach ($fieldTypes as $option): ?>
                        <option value="<?= View::e($option) ?>"<?= $option === $type ? ' selected' : '' ?>><?= View::e($view->t('design.type.' . $option)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="col-md-6">
                <label class="form-label mb-0 small" for="field-section"><?= View::e($view->t('design.field.section_header')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-section" name="section_header" value="<?= View::e($selected['section_header'] ?? '') ?>">
            </div>
            <div class="col-md-6">
                <label class="form-label mb-0 small" for="field-note"><?= View::e($view->t('design.field.note')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-note" name="field_note" value="<?= View::e($selected['field_note'] ?? '') ?>">
            </div>

            <!-- Choices (dropdown / radio / matrix): code + label rows, sent as code$label##… -->
            <fieldset class="col-12 border rounded p-2" data-clara-for-types="<?= View::e(implode('|', $choiceTypes)) ?>">
                <legend class="float-none w-auto px-1 small mb-0"><?= View::e($view->t('design.field.choices')) ?></legend>
                <table class="table table-sm mb-1 clara-choices">
                    <thead><tr>
                        <th scope="col" class="w-25"><?= View::e($view->t('design.choice.code')) ?></th>
                        <th scope="col"><?= View::e($view->t('design.choice.label')) ?></th>
                    </tr></thead>
                    <tbody data-clara-choice-rows>
                    <?php foreach (array_merge($choiceRows, [['code' => '', 'label' => ''], ['code' => '', 'label' => '']]) as $row): ?>
                        <tr>
                            <td><input class="form-control form-control-sm" type="text" inputmode="numeric" name="choice_code[]"
                                       aria-label="<?= View::e($view->t('design.choice.code')) ?>" value="<?= View::e($row['code']) ?>"></td>
                            <td><input class="form-control form-control-sm" type="text" name="choice_label[]"
                                       aria-label="<?= View::e($view->t('design.choice.label')) ?>" value="<?= View::e($row['label']) ?>"></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
                <button class="btn btn-sm btn-outline-secondary d-none" type="button" data-clara-add-choice><?= View::e($view->t('design.choice.add')) ?></button>
                <div class="form-text"><?= View::e($view->t('design.choice.help')) ?></div>
            </fieldset>

            <div class="col-md-4" data-clara-for-types="matrix">
                <label class="form-label mb-0 small" for="field-matrix"><?= View::e($view->t('design.field.matrix_group')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-matrix" name="matrix_group" value="<?= View::e($selected['matrix_group'] ?? '') ?>">
            </div>

            <div class="col-md-4" data-clara-for-types="text|dropdown|radio|matrix">
                <label class="form-label mb-0 small" for="field-validation"><?= View::e($view->t('design.field.validation_type')) ?></label>
                <select class="form-select form-select-sm" id="field-validation" name="validation_type" data-clara-validation-type
                        data-clara-identifier-types="<?= View::e(implode('|', $identifierTypes)) ?>">
                    <option value=""><?= View::e($view->t('design.validation.none')) ?></option>
                    <?php foreach ($validationTypes as $option): ?>
                        <option value="<?= View::e($option['name']) ?>"<?= $option['name'] === $vt ? ' selected' : '' ?>><?= View::e($option['name']) ?></option>
                    <?php endforeach; ?>
                    <?php if ($vt !== '' && !in_array($vt, array_column($validationTypes, 'name'), true)): ?>
                        <option value="<?= View::e($vt) ?>" selected><?= View::e($vt) ?></option>
                    <?php endif; ?>
                </select>
            </div>
            <div class="col-md-4" data-clara-for-validation="date|datetime">
                <label class="form-label mb-0 small" for="field-format"><?= View::e($view->t('design.field.validation_format')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-format" name="validation_format"
                       value="<?= View::e($nullable($selected['validation_format'] ?? null)) ?>">
            </div>
            <div class="col-md-2" data-clara-for-validation="integer|floating point">
                <label class="form-label mb-0 small" for="field-min"><?= View::e($view->t('design.field.min')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-min" name="validation_min" value="<?= View::e($nullable($selected['validation_min'] ?? null)) ?>">
            </div>
            <div class="col-md-2" data-clara-for-validation="integer|floating point">
                <label class="form-label mb-0 small" for="field-max"><?= View::e($view->t('design.field.max')) ?></label>
                <input class="form-control form-control-sm" type="text" id="field-max" name="validation_max" value="<?= View::e($nullable($selected['validation_max'] ?? null)) ?>">
            </div>

            <div class="col-12 d-flex flex-wrap gap-3">
                <div class="form-check">
                    <input class="form-check-input" type="checkbox" id="field-required" name="required" value="1"<?= !empty($selected['required']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="field-required"><?= View::e($view->t('design.field.required')) ?></label>
                </div>
                <div class="form-check">
                    <input class="form-check-input" type="checkbox" id="field-personal" name="personal_information" value="1"<?= !empty($selected['personal_information']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="field-personal"><?= View::e($view->t('design.field.personal_information')) ?></label>
                </div>
                <div class="form-check">
                    <!-- Preset for identifier-shaped validation types; clearing a preset is an
                         explicit, warned action that the form reports (REQ-EXP-020/021). -->
                    <input class="form-check-input" type="checkbox" id="field-direct" name="direct_identifier" value="1"
                           data-clara-direct-identifier<?= !empty($selected['direct_identifier']) ? ' checked' : '' ?>>
                    <label class="form-check-label" for="field-direct"><?= View::e($view->t('design.field.direct_identifier')) ?></label>
                    <input type="hidden" name="direct_identifier_cleared" value="0" data-clara-direct-cleared>
                    <div class="form-text text-warning-emphasis d-none" data-clara-direct-warning><?= View::e($view->t('design.direct_identifier_cleared')) ?></div>
                </div>
            </div>

            <div class="col-12">
                <?php
                $exprId = 'field-branching';
                $exprName = 'branching_logic';
                $exprValue = (string) ($selected['branching_logic'] ?? '');
                $exprGrammar = 'branching';
                $exprLabel = $view->t('design.field.branching');
                $exprRefsUrl = $editorUrl;
                require __DIR__ . '/../partials/expression-editor.php';
                ?>
            </div>
            <div class="col-12" data-clara-for-types="calculated">
                <?php
                $exprId = 'field-calculation';
                $exprName = 'calculation';
                $exprValue = (string) ($selected['calculation'] ?? '');
                $exprGrammar = 'calculation';
                $exprLabel = $view->t('design.field.calculation');
                require __DIR__ . '/../partials/expression-editor.php';
                ?>
            </div>
        </div>

        <div class="mt-3 d-flex gap-2">
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
            <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($editorUrl) ?>"><?= View::e($view->t('action.cancel')) ?></a>
        </div>
    </form>
<?php endif; ?>

<?php if ($isCalculated): ?>
    <!-- Test a calculation (§7.4, REQ-UI-023): a dry run against one visible record — the stored
         expression, or a draft. Nothing is stored (REQ-API-096). -->
    <section class="card card-body mb-4 clara-calc-test" aria-labelledby="calc-test-title">
        <h2 class="h6" id="calc-test-title"><?= View::e($view->t('design.test.title')) ?></h2>
        <?php if ($records === []): ?>
            <p class="small clara-region-empty mb-0"><?= View::e($view->t('design.test.no_records')) ?></p>
        <?php else: ?>
            <form method="post" action="<?= View::e($act('test_calc')) ?>" class="row g-2 align-items-end">
                <?= $view->csrfField() ?>
                <input type="hidden" name="field_id" value="<?= (int) $selected['id'] ?>">
                <div class="col-md-3">
                    <label class="form-label mb-0 small" for="calc-record"><?= View::e($view->t('design.test.record')) ?></label>
                    <select class="form-select form-select-sm" id="calc-record" name="record" required>
                        <?php foreach ($records as $record): ?>
                            <option value="<?= View::e($record) ?>"<?= ($test['record'] ?? '') === $record ? ' selected' : '' ?>><?= View::e($record) ?></option>
                        <?php endforeach; ?>
                    </select>
                </div>
                <div class="col-md-7">
                    <label class="form-label mb-0 small" for="calc-draft"><?= View::e($view->t('design.test.draft')) ?></label>
                    <input class="form-control form-control-sm font-monospace" type="text" id="calc-draft" name="expression"
                           value="<?= View::e($test['expression'] ?? '') ?>" placeholder="<?= View::e($selected['calculation'] ?? '') ?>">
                </div>
                <div class="col-md-2"><button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('design.test.run')) ?></button></div>
            </form>
        <?php endif; ?>

        <?php if (!empty($test['ran'])): ?>
            <div class="mt-3 clara-calc-result" role="status">
                <div><?= View::e($view->t('design.test.value')) ?>
                    <?php if (($test['value'] ?? '') === ''): ?>
                        <span class="text-body-secondary"><?= View::e($view->t('design.test.empty_value')) ?></span>
                    <?php else: ?>
                        <code class="fs-6" data-clara-calc-value><?= View::e($test['value']) ?></code>
                    <?php endif; ?>
                </div>
                <?php if (($test['problems'] ?? []) !== []): ?>
                    <!-- Every evaluation problem is flagged visibly (REQ-VAL-038). -->
                    <ul class="list-unstyled mb-0 mt-2 clara-calc-problems">
                        <?php foreach ($test['problems'] as $problem): ?>
                            <li class="text-danger-emphasis"><span class="badge text-bg-warning"><?= View::e(in_array($problem['problem'], ['missing_value', 'non_numeric_operand', 'division_by_zero'], true)
                                    ? $view->t('design.problem.' . $problem['problem']) : $problem['problem']) ?></span>
                                <code><?= View::e($problem['operand']) ?></code></li>
                        <?php endforeach; ?>
                    </ul>
                <?php endif; ?>
            </div>
        <?php endif; ?>
    </section>
<?php endif; ?>
