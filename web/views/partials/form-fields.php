<?php
/**
 * The fields of one instrument form, in position order (§8.2, REQ-UI-025): single fields,
 * matrix groups with the coding stated once, checkbox groups (one value per box, REQ-DB-042),
 * yes/no pairs labelled in the interface language (REQ-DB-043), text areas for notes (REQ-DB-044),
 * section headings, descriptions and headers, calculated values read-only, and a field annotated
 * `@READONLY` shown the way a member without edit rights sees it — its value, no input
 * (REQ-DB-046). Labels, section headers and notes keep their line breaks. Shared by the record view (§8) and the public survey page
 * (§8.8) so the two render one instrument the same way — the evaluator they both run
 * (branching.js, §8.4) reads the same `data-clara-*` attributes from this markup.
 *
 * Reads: $items (DataEntry::layout), $canEdit (inputs vs. read-only text), $exists (history
 * buttons), $identifier (the record-identifier field, shown read-only; '' for none),
 * $recordNaming ('auto' shows it as plain text, REQ-UI-052; default 'manual'), $record,
 * $prefill (stored values by field), $draft (a rejected submission's values, or null),
 * $errors (per-field reasons), $patterns (registry regexes), $formats (default date formats).
 */

use Clara\DataEntry;
use Clara\View;

/** Translates a key in the interface language — a yesno field's labels (REQ-DB-043). */
$t = static fn (string $key): string => $view->t($key);

$shown = static fn (string $name): string => $draft !== null && array_key_exists($name, $draft)
    ? (string) $draft[$name] : (string) ($prefill[$name] ?? '');

/**
 * Whether one checkbox box (`<field>___<code>`, REQ-DB-042) shows checked: as a rejected
 * submission left it — a browser posts only checked boxes, so an absent one was unchecked —
 * otherwise as stored.
 */
$isChecked = static fn (string $box): bool => $draft !== null
    ? (string) ($draft[$box] ?? '') !== '' : (string) ($prefill[$box] ?? '') !== '';

/**
 * The display text of a stored value: a choice's label (with its code), a free-text value as
 * its stored allowlist HTML (§3.2 — the only escape hatch), anything else escaped.
 */
$display = static function (array $field, string $value) use ($t): string {
    if ($value === '') {
        return '<span class="text-body-secondary">—</span>';
    }
    foreach (DataEntry::shownChoices($field, $t) as $choice) {
        if ($choice['code'] === $value) {
            return View::e($choice['label']) . ' <span class="text-body-secondary small">(' . View::e($value) . ')</span>';
        }
    }

    return DataEntry::isFreeText($field) ? View::htmlAllowed($value) : View::e($value);
};

/**
 * The attributes record.js reads for the advisory checks and the evaluator (§8.4). A field the
 * page shows without an input — read-only, calculated, the identifier — carries its value for
 * the evaluator in `data-clara-value`; it is already on the page as text.
 */
$fieldAttrs = static function (array $field, ?string $staticValue = null) use ($patterns, $formats): string {
    $vt = (string) ($field['validation_type'] ?? '');
    $attrs = [
        'data-clara-field' => (string) ($field['field_name'] ?? ''),
        'data-clara-type' => (string) ($field['field_type'] ?? ''),
        'data-clara-branching' => (string) ($field['branching_logic'] ?? ''),
        'data-clara-required' => !empty($field['required']) ? '1' : '',
        'data-clara-validation' => $vt,
        'data-clara-min' => (string) ($field['validation_min'] ?? ''),
        'data-clara-max' => (string) ($field['validation_max'] ?? ''),
        'data-clara-format' => in_array($vt, ['date', 'datetime'], true)
            ? (string) (($field['validation_format'] ?? '') ?: $formats[$vt]) : '',
        'data-clara-pattern' => (string) ($patterns[$vt] ?? ''),
        'data-clara-choices' => (string) ($field['choices'] ?? ''),
    ];
    $out = $staticValue !== null ? ' data-clara-value="' . View::e($staticValue) . '"' : '';
    foreach ($attrs as $name => $value) {
        if ($value !== '') {
            $out .= ' ' . $name . '="' . View::e($value) . '"';
        }
    }

    return $out;
};
?>
<?php foreach ($items as $item): ?>
    <?php if ($item['section'] !== ''): ?>
        <h2 class="h6 border-bottom pb-1 mt-3 clara-section-header"><?= View::lines($item['section']) ?></h2>
    <?php endif; ?>

    <?php if ($item['kind'] === 'matrix'): ?>
        <!-- A matrix group (REQ-DB-014): one row per sub-field, the coding stated once. -->
        <div class="table-responsive mb-3">
            <table class="table table-sm align-middle clara-matrix" data-clara-matrix="<?= View::e($item['group']) ?>">
                <thead><tr>
                    <th scope="col"><span class="visually-hidden"><?= View::e($view->t('record.field')) ?></span></th>
                    <?php foreach ($item['choices'] as $choice): ?>
                        <th scope="col" class="text-center small"><?= View::e($choice['label']) ?>
                            <span class="text-body-secondary">(<?= View::e($choice['code']) ?>)</span></th>
                    <?php endforeach; ?>
                    <th scope="col"><span class="visually-hidden"><?= View::e($view->t('record.history.title')) ?></span></th>
                </tr></thead>
                <tbody>
                <?php foreach ($item['fields'] as $field): ?>
                    <?php $name = (string) $field['field_name']; $value = $shown($name); $rowEditable = $canEdit && !DataEntry::isReadonly($field); ?>
                    <tr<?= $fieldAttrs($field, $rowEditable ? null : $value) ?>>
                        <th scope="row" class="fw-normal">
                            <?= View::lines($field['field_label'] ?? $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?>
                            <?php if (isset($errors[$name])): ?><div class="text-danger small" data-clara-error><?= View::e($errors[$name]) ?></div><?php endif; ?>
                        </th>
                        <?php foreach ($item['choices'] as $choice): ?>
                            <td class="text-center">
                                <?php if ($rowEditable): ?>
                                    <input class="form-check-input" type="radio" name="value[<?= View::e($name) ?>]" value="<?= View::e($choice['code']) ?>"
                                           aria-label="<?= View::e(($field['field_label'] ?? $name) . ': ' . $choice['label']) ?>"<?= $value === $choice['code'] ? ' checked' : '' ?>>
                                <?php elseif ($value === $choice['code']): ?>
                                    <span aria-label="<?= View::e($choice['label']) ?>">●</span>
                                <?php endif; ?>
                            </td>
                        <?php endforeach; ?>
                        <td class="text-end text-nowrap">
                            <?php if ($rowEditable): ?>
                                <input type="hidden" name="was[<?= View::e($name) ?>]" value="<?= View::e($prefill[$name] ?? '') ?>">
                                <button class="btn btn-sm btn-link p-0 me-1 d-none" type="button" data-clara-reset><?= View::e($view->t('record.reset')) ?></button>
                            <?php endif; ?>
                            <?php if ($exists): ?>
                                <button class="btn btn-sm btn-outline-secondary py-0" type="button" data-clara-history="<?= View::e($name) ?>"
                                        aria-label="<?= View::e($view->t('record.history.for', ['field' => $name])) ?>">⟲</button>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <?php continue; ?>
    <?php endif; ?>

    <?php
    $field = $item['fields'][0];
    $name = (string) $field['field_name'];
    $type = (string) $field['field_type'];
    $label = (string) ($field['field_label'] ?? '');
    $id = 'f-' . $name;
    $value = $shown($name);
    $isIdentifier = $name === $identifier;
    // `@READONLY` (REQ-DB-046): shown, never an input — on the survey page as well.
    $editable = $canEdit && !DataEntry::isReadonly($field);
    // A rejected box's reason belongs to its checkbox (REQ-DB-042).
    $error = implode('; ', array_values(array_intersect_key($errors,
        array_flip(array_merge([$name], $type === 'checkbox' ? DataEntry::valueNames($field) : [])))));
    ?>
    <?php if ($type === 'header'): ?>
        <h2 class="h6 mt-3 clara-field-header"<?= $fieldAttrs($field) ?>><?= View::lines($label) ?></h2>
        <?php continue; ?>
    <?php endif; ?>
    <?php if ($type === 'description'): ?>
        <p class="clara-field-description"<?= $fieldAttrs($field) ?>><?= View::lines($label) ?>
            <?php if (($field['field_note'] ?? '') !== ''): ?><span class="d-block form-text"><?= View::lines($field['field_note']) ?></span><?php endif; ?></p>
        <?php continue; ?>
    <?php endif; ?>
    <?php if ($type === 'transfer'): ?>
        <?php /* A transfer button (REQ-UI-065) carries no value. It renders only where the
               availability probe said this member may run it right now — absent, never
               disabled (REQ-UI-003) — and never on a survey page, where $transferAccess is
               not set at all. The form it submits lives outside the values form (record.php);
               the button joins it by id, so no transfer control nests in the save form. */ ?>
        <?php if (!empty($transferAccess[$name])): ?>
            <div class="mb-3 clara-field clara-transfer">
                <div class="form-label mb-1"><?= View::lines($label !== '' ? $label : $name) ?></div>
                <button class="btn btn-sm btn-primary" type="submit" form="clara-transfer-<?= View::e($name) ?>"><?= View::e($view->t('record.transfer.button')) ?></button>
            </div>
        <?php endif; ?>
        <?php continue; ?>
    <?php endif; ?>

    <?php $static = !$editable || $isIdentifier || $type === 'calculated' ? ($isIdentifier && $value === '' ? $record : $value) : null; ?>
    <div class="mb-3 clara-field<?= $error !== '' ? ' clara-field-invalid' : '' ?>"<?= $fieldAttrs($field, $static) ?>>
        <div class="d-flex align-items-start gap-2">
            <div class="flex-grow-1">
                <?php if (in_array($type, ['radio', 'yesno', 'checkbox'], true) && $editable && !$isIdentifier): ?>
                    <div class="form-label mb-1" id="<?= View::e($id) ?>-label"><?= View::lines($label !== '' ? $label : $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?></div>
                <?php else: ?>
                    <label class="form-label mb-1" for="<?= View::e($id) ?>"><?= View::lines($label !== '' ? $label : $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?><?php if ($canEdit && !$editable): ?>
                        <span class="badge text-bg-light border ms-1 clara-readonly"><?= View::e($view->t('record.readonly')) ?></span><?php endif; ?></label>
                <?php endif; ?>

                <?php if ($isIdentifier): ?>
                    <?php if (($recordNaming ?? 'manual') === 'auto'): ?>
                        <!-- The record identifier (GD-8) in auto naming mode (REQ-UI-052): the
                             number is the record — text on the page, not a field. -->
                        <div class="form-control-plaintext py-0 clara-value" id="<?= View::e($id) ?>"><?= View::e($value !== '' ? $value : $record) ?></div>
                    <?php else: ?>
                        <!-- The record identifier (GD-8): the record's name, never edited here. -->
                        <input class="form-control form-control-sm" type="text" id="<?= View::e($id) ?>" value="<?= View::e($value !== '' ? $value : $record) ?>" readonly>
                    <?php endif; ?>
                <?php elseif ($type === 'calculated'): ?>
                    <!-- Calculated (REQ-VAL-036): a read-only value, recomputed by the API on import. -->
                    <output class="form-control form-control-sm bg-body-tertiary" id="<?= View::e($id) ?>"><?= $value === '' ? '<span class="text-body-secondary">—</span>' : View::e($value) ?></output>
                <?php elseif (!$editable && $type === 'checkbox'): ?>
                    <!-- The checked boxes; every box carries its state for the evaluator (§8.4). -->
                    <div class="form-control-plaintext py-0 clara-value" id="<?= View::e($id) ?>">
                        <?php $anyChecked = false; ?>
                        <?php foreach ($item['choices'] as $choice): ?>
                            <?php $box = $name . DataEntry::CHOICE_SEPARATOR . $choice['code']; $on = $isChecked($box); $anyChecked = $anyChecked || $on; ?>
                            <span class="me-2<?= $on ? '' : ' d-none' ?>" data-clara-choice="<?= View::e($box) ?>" data-clara-value="<?= $on ? '1' : '' ?>"
                                  data-clara-choices="<?= View::e('1$' . $choice['label']) ?>">✓ <?= View::e($choice['label']) ?>
                                <span class="text-body-secondary small">(<?= View::e($choice['code']) ?>)</span></span>
                        <?php endforeach; ?>
                        <?php if (!$anyChecked): ?><span class="text-body-secondary">—</span><?php endif; ?>
                    </div>
                <?php elseif (!$editable): ?>
                    <div class="form-control-plaintext py-0 clara-value<?= $type === 'notes' ? ' clara-notes' : '' ?>" id="<?= View::e($id) ?>"><?= $display($field, $value) ?></div>
                <?php elseif ($type === 'dropdown'): ?>
                    <select class="form-select form-select-sm" id="<?= View::e($id) ?>" name="value[<?= View::e($name) ?>]">
                        <option value=""></option>
                        <?php foreach ($item['choices'] as $choice): ?>
                            <option value="<?= View::e($choice['code']) ?>"<?= $value === $choice['code'] ? ' selected' : '' ?>><?= View::e($choice['label']) ?></option>
                        <?php endforeach; ?>
                        <?php if ($value !== '' && !in_array($value, array_column($item['choices'], 'code'), true)): ?>
                            <option value="<?= View::e($value) ?>" selected><?= View::e($value) ?></option>
                        <?php endif; ?>
                    </select>
                <?php elseif ($type === 'radio' || $type === 'yesno'): ?>
                    <!-- A yesno field is this pair with the fixed coding 1 = Yes, 0 = No (REQ-DB-043). -->
                    <div role="radiogroup" aria-labelledby="<?= View::e($id) ?>-label" class="d-flex flex-wrap gap-3">
                        <?php foreach (DataEntry::shownChoices($field, $t) as $i => $choice): ?>
                            <div class="form-check mb-0">
                                <input class="form-check-input" type="radio" id="<?= View::e($id . '-' . $i) ?>" name="value[<?= View::e($name) ?>]"
                                       value="<?= View::e($choice['code']) ?>"<?= $value === $choice['code'] ? ' checked' : '' ?>>
                                <label class="form-check-label" for="<?= View::e($id . '-' . $i) ?>"><?= View::e($choice['label']) ?></label>
                            </div>
                        <?php endforeach; ?>
                        <button class="btn btn-sm btn-link p-0 d-none" type="button" data-clara-reset><?= View::e($view->t('record.reset')) ?></button>
                    </div>
                <?php elseif ($type === 'checkbox'): ?>
                    <!-- One box per choice, each its own value column `<field>___<code>` (REQ-DB-042):
                         a checked box posts 1; an unchecked one posts nothing, and was[…] tells the
                         controller which stored box the user cleared (GD-14). -->
                    <div role="group" aria-labelledby="<?= View::e($id) ?>-label" class="d-flex flex-wrap gap-3">
                        <?php foreach ($item['choices'] as $i => $choice): ?>
                            <?php $box = $name . DataEntry::CHOICE_SEPARATOR . $choice['code']; ?>
                            <div class="form-check mb-0">
                                <input class="form-check-input" type="checkbox" id="<?= View::e($id . '-' . $i) ?>" name="value[<?= View::e($box) ?>]" value="1"
                                       data-clara-choice="<?= View::e($box) ?>" data-clara-choices="<?= View::e('1$' . $choice['label']) ?>"<?= $isChecked($box) ? ' checked' : '' ?>>
                                <label class="form-check-label" for="<?= View::e($id . '-' . $i) ?>"><?= View::e($choice['label']) ?></label>
                            </div>
                        <?php endforeach; ?>
                    </div>
                <?php elseif ($type === 'notes'): ?>
                    <!-- Multi-line free text (REQ-DB-044). The newline after the tag is the one a
                         parser drops, so a value that starts with a line break keeps it. -->
                    <textarea class="form-control form-control-sm<?= $error !== '' ? ' is-invalid' : '' ?>" id="<?= View::e($id) ?>"
                              name="value[<?= View::e($name) ?>]" rows="5">
<?= View::e($value) ?></textarea>
                <?php else: ?>
                    <?php
                    $vt = (string) ($field['validation_type'] ?? '');
                    $format = in_array($vt, ['date', 'datetime'], true) ? (string) (($field['validation_format'] ?? '') ?: $formats[$vt]) : '';
                    $mode = ['integer' => 'numeric', 'floating point' => 'decimal'][$vt] ?? 'text';
                    // A date/date-time field the browser control can hold is entered by picking it
                    // rather than typing it (REQ-UI-065): the control carries no offset, so the
                    // stored value shows without its one (§4.1 of Data_Validation_Design.md) and
                    // goes back as stored if left alone — nativeDateValues() in DataEntry.php. A
                    // field with another format, or a stored value in no recognizable shape,
                    // keeps the text input below rather than an empty box over a value it has.
                    $isDate = in_array($vt, ['date', 'datetime'], true);
                    $control = DataEntry::nativeDateControl($field, $format);
                    $entered = $control === '' ? null : DataEntry::nativeDateValue($control, $value);
                    if ($entered === null) {
                        $control = '';
                    }
                    ?>
                    <?php if ($isDate): ?>
                        <div class="input-group input-group-sm clara-date">
                    <?php endif; ?>
                    <input class="form-control form-control-sm<?= $error !== '' ? ' is-invalid' : '' ?>" type="<?= $control !== '' ? $control : 'text' ?>" id="<?= View::e($id) ?>"
                           name="value[<?= View::e($name) ?>]" value="<?= View::e($control !== '' ? (string) $entered : $value) ?>"<?= $control === '' ? ' inputmode="' . $mode . '"' : '' ?>
                           <?= $format !== '' && $control === '' ? 'placeholder="' . View::e($format) . '"' : '' ?> autocomplete="off">
                    <?php if ($isDate): ?>
                        <?php // The "now" shortcut: form.js fills the current date — and time, here — in this field's own format, and reveals the button with it. ?>
                        <button class="btn btn-outline-secondary d-none" type="button" data-clara-now><?= View::e($view->t('record.now')) ?></button>
                    </div>
                    <?php endif; ?>
                <?php endif; ?>

                <?php if ($editable && !$isIdentifier && DataEntry::isEnterable($field)): ?>
                    <?php foreach (DataEntry::valueNames($field) as $valueName): ?>
                        <input type="hidden" name="was[<?= View::e($valueName) ?>]" value="<?= View::e($prefill[$valueName] ?? '') ?>">
                    <?php endforeach; ?>
                <?php endif; ?>
                <?php if (($field['field_note'] ?? '') !== ''): ?>
                    <div class="form-text"><?= View::lines($field['field_note']) ?></div>
                <?php endif; ?>
                <?php if ($error !== ''): ?>
                    <div class="text-danger small" data-clara-error><?= View::e($error) ?></div>
                <?php endif; ?>
                <div class="small text-warning-emphasis d-none" data-clara-hint></div>
            </div>
            <?php if ($exists && !$isIdentifier): ?>
                <!-- Per-field history (§8.3, REQ-UI-026): who, when (UTC), old → new. -->
                <button class="btn btn-sm btn-outline-secondary mt-4 py-0" type="button" data-clara-history="<?= View::e($name) ?>"
                        aria-label="<?= View::e($view->t('record.history.for', ['field' => $name])) ?>"
                        title="<?= View::e($view->t('record.history.title')) ?>">⟲</button>
            <?php endif; ?>
        </div>
    </div>
<?php endforeach; ?>
