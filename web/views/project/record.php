<?php
/**
 * Project page — the record view and data-entry form (§8, REQ-UI-025…027).
 *
 * One (record, event, instrument) at a time (§8.1): the event picker and the instruments
 * mapped to the chosen event above the form; the fields in position order with their current
 * values filled in (§8.3); the completion state as the last field (§8.5); the record actions
 * below (§8.7).
 *
 * What a member may do decides what is emitted (REQ-UI-003): a `read_only` member — and
 * everyone in analysis mode (REQ-UI-035) — gets the values as text and no submit control;
 * delete, the group control and the survey link appear only at their own levels.
 *
 * Every value input carries its prefill in a `was[…]` twin so the submission policy (GD-14,
 * REQ-UI-031) can tell a removed value from one that was never there; record.js adds the
 * advisory checks and the branching evaluator (§8.4) and never changes what is sent.
 *
 * Reads: $record, $exists, $recordUrl, $statusUrl, $arms, $armNum, $event, $instrument,
 * $items, $identifier, $prefill, $draft, $errors, $states, $state, $analysis, $canEdit,
 * $deleteScopes, $isSurvey, $canLink, $link, $groups, $patterns, $formats, $clientContext.
 */

use Clara\DataEntry;
use Clara\View;

$uen = (string) ($event['unique_event_name'] ?? '');
$instrumentName = (string) ($instrument['name'] ?? '');
$iid = (int) ($instrument['id'] ?? 0);
$act = static fn (string $action): string => $recordUrl . '?action=' . $action;
$here = ['event' => $uen, 'instrument' => $instrumentName, 'iid' => $iid];
$shown = static fn (string $name): string => $draft !== null && array_key_exists($name, $draft)
    ? (string) $draft[$name] : (string) ($prefill[$name] ?? '');
$hidden = static function (array $fields): string {
    $out = '';
    foreach ($fields as $name => $value) {
        $out .= '<input type="hidden" name="' . View::e($name) . '" value="' . View::e($value) . '">';
    }

    return $out;
};
$stateBadge = [
    'no_data' => 'clara-state-no_data',
    'some_data' => 'clara-state-some_data',
    'finished' => 'clara-state-finished',
];

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
<nav class="small mb-1" aria-label="<?= View::e($view->t('records.title')) ?>">
    <a href="<?= View::e($statusUrl) ?>"><?= View::e($view->t('record.back')) ?></a>
</nav>
<h1 class="h4 mb-2"><?= View::e($view->t('record.heading', ['record' => $record])) ?></h1>

<?php if (!$exists): ?>
    <div class="alert alert-info py-2 clara-record-new"><?= View::e($view->t('record.new_notice')) ?></div>
<?php endif; ?>
<?php if ($analysis): ?>
    <!-- Analysis mode closes data entry for everyone, project_admin included (GD-20, REQ-UI-035). -->
    <div class="alert alert-secondary py-2 clara-analysis-closed"><?= View::e($view->t('record.analysis_notice')) ?></div>
<?php endif; ?>

<!-- §8.1: pick the event, then one of the instruments mapped to it (REQ-DB-012). -->
<form method="get" action="<?= View::e($recordUrl) ?>" class="row g-2 align-items-end mb-2 clara-event-picker" data-clara-autosubmit>
    <div class="col-sm-6 col-md-4">
        <label class="form-label mb-0 small" for="record-event"><?= View::e($view->t('record.event')) ?></label>
        <select class="form-select form-select-sm" id="record-event" name="event">
            <?php foreach ($arms as $arm): ?>
                <optgroup label="<?= View::e($view->t('roles.arm', ['arm' => (string) $arm['arm_num']]) . ($arm['name'] !== '' ? ' — ' . $arm['name'] : '')) ?>">
                    <?php foreach ($arm['events'] as $option): ?>
                        <option value="<?= View::e($option['unique_event_name']) ?>"<?= $option['unique_event_name'] === $uen ? ' selected' : '' ?>>
                            <?= View::e($option['event_name']) ?></option>
                    <?php endforeach; ?>
                </optgroup>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-auto"><button class="btn btn-sm btn-outline-secondary" type="submit" data-clara-autosubmit-button><?= View::e($view->t('record.go')) ?></button></div>
</form>

<?php if ($instrument === null): ?>
    <p class="clara-region-empty"><?= View::e($view->t('record.no_instruments')) ?></p>
<?php else: ?>
    <ul class="nav nav-pills mb-3 clara-instrument-nav">
        <?php foreach ($event['instruments'] as $candidate): ?>
            <?php
            $name = (string) $candidate['name'];
            $candidateState = $states[$uen . '|' . $name] ?? 'no_data';
            ?>
            <li class="nav-item" data-clara-instrument="<?= View::e($name) ?>"
                <?= ($candidate['branching_logic'] ?? '') !== '' ? ' data-clara-branching="' . View::e($candidate['branching_logic']) . '"' : '' ?>>
                <a class="nav-link py-1<?= $name === $instrumentName ? ' active' : '' ?>"<?= $name === $instrumentName ? ' aria-current="page"' : '' ?>
                   href="<?= View::e($recordUrl . '?' . http_build_query(['event' => $uen, 'instrument' => $name], '', '&', PHP_QUERY_RFC3986)) ?>">
                    <span class="clara-state <?= $stateBadge[$candidateState] ?? $stateBadge['no_data'] ?>"
                          title="<?= View::e($view->t('records.state.' . $candidateState)) ?>"></span>
                    <?= View::e($name) ?>
                    <?php if (!empty($candidate['is_survey'])): ?><span class="badge text-bg-light border ms-1"><?= View::e($view->t('record.survey_badge')) ?></span><?php endif; ?>
                </a>
            </li>
        <?php endforeach; ?>
    </ul>

    <!-- The branching evaluator's inputs (§8.4): this event, the project's first event for the
         [field] shorthand, and the current values the logic reads outside this form. UI copy
         travels in the data-i18n block; this block is data for the evaluator only. -->
    <script type="application/json" data-clara-record-context><?= json_encode($clientContext,
        JSON_HEX_TAG | JSON_HEX_AMP | JSON_HEX_APOS | JSON_HEX_QUOT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) ?></script>

    <div class="alert alert-secondary py-2 d-none clara-instrument-hidden" data-clara-instrument-hidden>
        <?= View::e($view->t('record.instrument_hidden')) ?></div>

    <form method="post" action="<?= View::e($act('save')) ?>" class="clara-record-form mb-4" novalidate
          data-clara-record-form data-history="<?= View::e($recordUrl) ?>" data-event="<?= View::e($uen) ?>"
          data-instrument="<?= View::e($instrumentName) ?>"
          aria-label="<?= View::e($instrumentName) ?>">
        <?php if ($canEdit): ?>
            <?= $view->csrfField() ?>
            <?= $hidden($here) ?>
            <!-- The browser's timezone, the collection zone of date values (GD-16); filled by record.js. -->
            <input type="hidden" name="tz" value="" data-clara-tz>
        <?php endif; ?>

        <?php if (isset($errors[''])): ?>
            <div class="alert alert-danger py-2"><?= View::e($errors['']) ?></div>
        <?php endif; ?>

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

        <?php if ($items === []): ?>
            <p class="clara-region-empty"><?= View::e($view->t('record.no_fields')) ?></p>
        <?php endif; ?>

        <!-- Completion — always the last field (§8.5). -->
        <div class="border-top pt-3 mt-3 clara-completion">
            <?php if ($isSurvey): ?>
                <!-- A survey instrument's completion is automatic (GD-9): shown, never assigned. -->
                <div class="small"><?= View::e($view->t('record.completion')) ?>:
                    <span class="clara-state clara-state-finished" aria-hidden="true"></span>
                    <?= View::e($view->t('record.completion.survey')) ?></div>
            <?php elseif ($canEdit): ?>
                <label class="form-label mb-0 small" for="record-completion"><?= View::e($view->t('record.completion')) ?></label>
                <select class="form-select form-select-sm w-auto" id="record-completion" name="completion">
                    <?php foreach (DataEntry::STATES as $option): ?>
                        <option value="<?= $option ?>"<?= $option === $state ? ' selected' : '' ?>><?= View::e($view->t('records.state.' . $option)) ?></option>
                    <?php endforeach; ?>
                </select>
                <input type="hidden" name="completion_was" value="<?= View::e($state) ?>">
            <?php else: ?>
                <div class="small"><?= View::e($view->t('record.completion')) ?>:
                    <span class="clara-state <?= $stateBadge[$state] ?? $stateBadge['no_data'] ?>" aria-hidden="true"></span>
                    <?= View::e($view->t('records.state.' . $state)) ?></div>
            <?php endif; ?>
        </div>

        <?php if ($canEdit): ?>
            <!-- The required-field check before submission (REQ-VAL-028): advisory, and a partly
                 filled instrument can still be saved (GD-14). Filled by record.js. -->
            <div class="alert alert-warning py-2 mt-3 d-none" data-clara-required-warning role="alert"></div>
            <div class="mt-3">
                <button class="btn btn-sm btn-primary" type="submit" data-clara-save><?= View::e($view->t('record.save')) ?></button>
            </div>
        <?php endif; ?>
    </form>

    <!-- Record actions (§8.7, REQ-UI-027) -->
    <?php if ($canLink): ?>
        <section class="card card-body mb-3 clara-survey-link" aria-labelledby="survey-link-title">
            <h2 class="h6" id="survey-link-title"><?= View::e($view->t('record.survey.title')) ?></h2>
            <?php if ($link !== ''): ?>
                <div class="input-group input-group-sm mb-2">
                    <input class="form-control" type="text" id="clara-survey-url" value="<?= View::e($link) ?>" readonly
                           aria-label="<?= View::e($view->t('record.survey.title')) ?>">
                    <button class="btn btn-outline-secondary" type="button" data-clara-copy="#clara-survey-url"><?= View::e($view->t('record.survey.copy')) ?></button>
                </div>
            <?php endif; ?>
            <div class="d-flex flex-wrap gap-2">
                <form method="post" action="<?= View::e($act('survey_link')) ?>">
                    <?= $view->csrfField() ?><?= $hidden($here) ?>
                    <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('record.survey.get')) ?></button>
                </form>
                <form method="post" action="<?= View::e($act('revoke_link')) ?>"
                      data-clara-confirm="<?= View::e($view->t('record.survey.confirm_revoke', ['record' => $record, 'instrument' => $instrumentName])) ?>">
                    <?= $view->csrfField() ?><?= $hidden($here) ?>
                    <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('record.survey.revoke')) ?></button>
                </form>
            </div>
        </section>
    <?php endif; ?>
<?php endif; ?>

<?php if ($groups !== null): ?>
    <!-- Data access group (§8.7, REQ-API-091): project_admin assigns or changes it. -->
    <section class="card card-body mb-3 clara-record-group" aria-labelledby="record-group-title">
        <h2 class="h6" id="record-group-title"><?= View::e($view->t('record.group.title')) ?></h2>
        <form method="post" action="<?= View::e($act('assign_group')) ?>" class="row g-2 align-items-end">
            <?= $view->csrfField() ?><?= $hidden(['event' => $uen, 'instrument' => $instrumentName]) ?>
            <div class="col-sm-6 col-md-4">
                <label class="form-label mb-0 small" for="record-group"><?= View::e($view->t('record.group.label')) ?></label>
                <select class="form-select form-select-sm" id="record-group" name="group_id" required>
                    <option value="" selected disabled><?= View::e($view->t('record.group.choose')) ?></option>
                    <option value="none"><?= View::e($view->t('record.group.none')) ?></option>
                    <?php foreach ($groups as $group): ?>
                        <option value="<?= (int) ($group['id'] ?? 0) ?>"><?= View::e($group['name'] ?? '') ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-auto"><button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('record.group.assign')) ?></button></div>
            <div class="col-12 form-text mt-0"><?= View::e($view->t('record.group.help')) ?></div>
        </form>
    </section>
<?php endif; ?>

<?php if ($deleteScopes !== []): ?>
    <!-- Delete (§8.7, GD-3): three scopes, each confirmed with its consequence named (§3.5). -->
    <section class="card card-body mb-3 border-danger-subtle clara-record-delete" aria-labelledby="record-delete-title">
        <h2 class="h6" id="record-delete-title"><?= View::e($view->t('record.delete.title')) ?></h2>
        <div class="d-flex flex-wrap gap-2">
            <?php
            foreach ($deleteScopes as $scope):
                $confirm = $view->t('record.delete.confirm.' . $scope, ['record' => $record, 'event' => (string) ($event['event_name'] ?? $uen), 'instrument' => $instrumentName]);
            ?>
                <form method="post" action="<?= View::e($act('delete')) ?>" data-clara-confirm="<?= View::e($confirm) ?>">
                    <?= $view->csrfField() ?><?= $hidden($here + ['scope' => $scope]) ?>
                    <button class="btn btn-sm btn-outline-danger" type="submit" data-scope="<?= $scope ?>"><?= View::e($view->t('record.delete.' . $scope)) ?></button>
                </form>
            <?php endforeach; ?>
        </div>
    </section>
<?php endif; ?>
