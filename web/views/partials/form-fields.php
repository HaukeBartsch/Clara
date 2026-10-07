<?php
/**
 * The fields of one instrument form, in position order (§8.2, REQ-UI-025): single fields,
 * matrix groups with the coding stated once, section headings, descriptions and headers,
 * calculated values read-only. Shared by the record view (§8) and the public survey page
 * (§8.8) so the two render one instrument the same way — the evaluator they both run
 * (branching.js, §8.4) reads the same `data-clara-*` attributes from this markup.
 *
 * Reads: $items (DataEntry::layout), $canEdit (inputs vs. read-only text), $exists (history
 * buttons), $identifier (the record-identifier field, shown read-only; '' for none), $record,
 * $prefill (stored values by field), $draft (a rejected submission's values, or null),
 * $errors (per-field reasons), $patterns (registry regexes), $formats (default date formats).
 */

use Clara\DataEntry;
use Clara\View;

$shown = static fn (string $name): string => $draft !== null && array_key_exists($name, $draft)
    ? (string) $draft[$name] : (string) ($prefill[$name] ?? '');

/**
 * The display text of a stored value: a choice's label (with its code), a free-text value as
 * its stored allowlist HTML (§3.2 — the only escape hatch), anything else escaped.
 */
$display = static function (array $field, string $value): string {
    if ($value === '') {
        return '<span class="text-body-secondary">—</span>';
    }
    foreach (DataEntry::choices((string) ($field['choices'] ?? '')) as $choice) {
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
        <h2 class="h6 border-bottom pb-1 mt-3 clara-section-header"><?= View::e($item['section']) ?></h2>
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
                    <?php $name = (string) $field['field_name']; $value = $shown($name); ?>
                    <tr<?= $fieldAttrs($field, $canEdit ? null : $value) ?>>
                        <th scope="row" class="fw-normal">
                            <?= View::e($field['field_label'] ?? $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?>
                            <?php if (isset($errors[$name])): ?><div class="text-danger small" data-clara-error><?= View::e($errors[$name]) ?></div><?php endif; ?>
                        </th>
                        <?php foreach ($item['choices'] as $choice): ?>
                            <td class="text-center">
                                <?php if ($canEdit): ?>
                                    <input class="form-check-input" type="radio" name="value[<?= View::e($name) ?>]" value="<?= View::e($choice['code']) ?>"
                                           aria-label="<?= View::e(($field['field_label'] ?? $name) . ': ' . $choice['label']) ?>"<?= $value === $choice['code'] ? ' checked' : '' ?>>
                                <?php elseif ($value === $choice['code']): ?>
                                    <span aria-label="<?= View::e($choice['label']) ?>">●</span>
                                <?php endif; ?>
                            </td>
                        <?php endforeach; ?>
                        <td class="text-end text-nowrap">
                            <?php if ($canEdit): ?>
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
    ?>
    <?php if ($type === 'header'): ?>
        <h2 class="h6 mt-3 clara-field-header"<?= $fieldAttrs($field) ?>><?= View::e($label) ?></h2>
        <?php continue; ?>
    <?php endif; ?>
    <?php if ($type === 'description'): ?>
        <p class="clara-field-description"<?= $fieldAttrs($field) ?>><?= View::e($label) ?>
            <?php if (($field['field_note'] ?? '') !== ''): ?><span class="d-block form-text"><?= View::e($field['field_note']) ?></span><?php endif; ?></p>
        <?php continue; ?>
    <?php endif; ?>

    <?php $static = !$canEdit || $isIdentifier || $type === 'calculated' ? ($isIdentifier && $value === '' ? $record : $value) : null; ?>
    <div class="mb-3 clara-field<?= isset($errors[$name]) ? ' clara-field-invalid' : '' ?>"<?= $fieldAttrs($field, $static) ?>>
        <div class="d-flex align-items-start gap-2">
            <div class="flex-grow-1">
                <?php if (in_array($type, ['radio'], true) && $canEdit && !$isIdentifier): ?>
                    <div class="form-label mb-1" id="<?= View::e($id) ?>-label"><?= View::e($label !== '' ? $label : $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?></div>
                <?php else: ?>
                    <label class="form-label mb-1" for="<?= View::e($id) ?>"><?= View::e($label !== '' ? $label : $name) ?><?php if (!empty($field['required'])): ?><span class="clara-required" aria-hidden="true"> *</span><?php endif; ?></label>
                <?php endif; ?>

                <?php if ($isIdentifier): ?>
                    <!-- The record identifier (GD-8): the record's name, never edited here. -->
                    <input class="form-control form-control-sm" type="text" id="<?= View::e($id) ?>" value="<?= View::e($value !== '' ? $value : $record) ?>" readonly>
                <?php elseif ($type === 'calculated'): ?>
                    <!-- Calculated (REQ-VAL-036): a read-only value, recomputed by the API on import. -->
                    <output class="form-control form-control-sm bg-body-tertiary" id="<?= View::e($id) ?>"><?= $value === '' ? '<span class="text-body-secondary">—</span>' : View::e($value) ?></output>
                <?php elseif (!$canEdit): ?>
                    <div class="form-control-plaintext py-0 clara-value" id="<?= View::e($id) ?>"><?= $display($field, $value) ?></div>
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
                <?php elseif ($type === 'radio'): ?>
                    <div role="radiogroup" aria-labelledby="<?= View::e($id) ?>-label" class="d-flex flex-wrap gap-3">
                        <?php foreach ($item['choices'] as $i => $choice): ?>
                            <div class="form-check mb-0">
                                <input class="form-check-input" type="radio" id="<?= View::e($id . '-' . $i) ?>" name="value[<?= View::e($name) ?>]"
                                       value="<?= View::e($choice['code']) ?>"<?= $value === $choice['code'] ? ' checked' : '' ?>>
                                <label class="form-check-label" for="<?= View::e($id . '-' . $i) ?>"><?= View::e($choice['label']) ?></label>
                            </div>
                        <?php endforeach; ?>
                        <button class="btn btn-sm btn-link p-0 d-none" type="button" data-clara-reset><?= View::e($view->t('record.reset')) ?></button>
                    </div>
                <?php else: ?>
                    <?php
                    $vt = (string) ($field['validation_type'] ?? '');
                    $format = in_array($vt, ['date', 'datetime'], true) ? (string) (($field['validation_format'] ?? '') ?: $formats[$vt]) : '';
                    $mode = ['integer' => 'numeric', 'floating point' => 'decimal'][$vt] ?? 'text';
                    ?>
                    <input class="form-control form-control-sm<?= isset($errors[$name]) ? ' is-invalid' : '' ?>" type="text" id="<?= View::e($id) ?>"
                           name="value[<?= View::e($name) ?>]" value="<?= View::e($value) ?>" inputmode="<?= $mode ?>"
                           <?= $format !== '' ? 'placeholder="' . View::e($format) . '"' : '' ?> autocomplete="off">
                <?php endif; ?>

                <?php if ($canEdit && !$isIdentifier && DataEntry::isEnterable($field)): ?>
                    <input type="hidden" name="was[<?= View::e($name) ?>]" value="<?= View::e($prefill[$name] ?? '') ?>">
                <?php endif; ?>
                <?php if (($field['field_note'] ?? '') !== ''): ?>
                    <div class="form-text"><?= View::e($field['field_note']) ?></div>
                <?php endif; ?>
                <?php if (isset($errors[$name])): ?>
                    <div class="text-danger small" data-clara-error><?= View::e($errors[$name]) ?></div>
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
