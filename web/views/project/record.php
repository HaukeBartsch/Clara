<?php
/**
 * Project page — the record view and data-entry form (§8, REQ-UI-025…027).
 *
 * One (record, event, instrument) at a time (§8.1): the record's instrument table above the
 * form — instruments as rows, events as columns, each cell the dashboard's completeness marker
 * opening that form (REQ-UI-082); the fields in position order with their current values
 * filled in (§8.3); the completion state as the last field (§8.5); the record actions below
 * (§8.7).
 *
 * What a member may do decides what is emitted (REQ-UI-003): a `read_only` member — and
 * everyone in analysis mode (REQ-UI-035) — gets the values as text and no submit control;
 * delete, the group control and the survey link appear only at their own levels.
 *
 * Every value input carries its prefill in a `was[…]` twin so the submission policy (GD-14,
 * REQ-UI-031) can tell a removed value from one that was never there; record.js adds the
 * advisory checks and the branching evaluator (§8.4) and never changes what is sent.
 *
 * A repeating instrument (REQ-UI-055) is one instance at a time: its cell of the table holds a
 * marker per instance and "+" for the next, the instance pills switch between the existing
 * ones, and every form of the page names the open instance.
 *
 * Reads: $record, $exists, $recordUrl, $statusUrl, $arms, $armNum, $event, $instrument,
 * $items, $identifier, $prefill, $draft, $errors, $states, $state, $table, $countsFailed,
 * $repeats, $instance, $instanceList, $analysis, $canEdit, $deleteScopes, $isSurvey, $canLink,
 * $link, $transferAccess, $groups, $patterns, $formats, $clientContext.
 */

use Clara\DataEntry;
use Clara\View;

$uen = (string) ($event['unique_event_name'] ?? '');
$instrumentName = (string) ($instrument['name'] ?? '');
$iid = (int) ($instrument['id'] ?? 0);
$act = static fn (string $action): string => $recordUrl . '?action=' . $action;
$here = ['event' => $uen, 'instrument' => $instrumentName, 'iid' => $iid]
    + ($repeats ? ['repeating' => '1', 'instance' => (string) $instance] : []);
$cellUrl = static fn (string $atEvent, string $name, int $n = 0): string => $recordUrl . '?' . http_build_query(
    ['event' => $atEvent, 'instrument' => $name] + ($n > 0 ? ['instance' => $n] : []), '', '&', PHP_QUERY_RFC3986);
$openUrl = static fn (string $name, int $n = 0): string => $cellUrl($uen, $name, $n);
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
    'unverified' => 'clara-state-unverified',
    'finished' => 'clara-state-finished',
];
// A marker's state and count as its label names them — neither told by colour or tiles alone
// (REQ-UI-081), the same words as the dashboard's (record-status.js stateLabel).
$stateLabel = static fn (string $state, ?array $count): string => $view->t('records.state.' . $state)
    . ($count === null ? '' : ', ' . ($count[0] > 0
        ? $view->t('records.count', ['filled' => (string) $count[0], 'visible' => (string) $count[1]])
        : $view->t('records.count_none')));
$multiArm = count(array_unique(array_column($table['columns'], 'arm_num'))) > 1;

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

<?php if ($table['rows'] !== []): ?>
    <!-- §8.1, REQ-UI-082: the record's instruments (rows) by event (columns, in order), each
         cell the dashboard's completeness marker opening that form of this record. The counter
         SVG is on the page once; every marker draws it (REQ-UI-081). -->
    <?php require __DIR__ . '/../partials/counter-sprite.php'; ?>
    <?php if ($countsFailed): ?>
        <p class="small text-danger mb-2 clara-counts-failed" role="status"><?= View::e($view->t('records.counts_failed')) ?></p>
    <?php endif; ?>
    <div class="table-responsive mb-3">
        <table class="table table-sm table-bordered align-middle w-auto mb-0 clara-instrument-nav clara-record-instruments"
               aria-label="<?= View::e($view->t('record.instruments', ['record' => $record])) ?>">
            <thead>
            <?php if ($multiArm): ?>
                <tr>
                    <th scope="col" rowspan="2" class="align-bottom clara-instrument-name"><?= View::e($view->t('record.instrument')) ?></th>
                    <?php
                    // One header cell per arm over its events, which the columns list in order.
                    $spans = [];
                    foreach ($table['columns'] as $column) {
                        $last = array_key_last($spans);
                        if ($last !== null && $spans[$last]['arm_num'] === $column['arm_num']) {
                            $spans[$last]['span']++;
                        } else {
                            $spans[] = ['arm_num' => $column['arm_num'], 'name' => $column['arm_name'], 'span' => 1];
                        }
                    }
                    ?>
                    <?php foreach ($spans as $span): ?>
                        <th scope="colgroup" colspan="<?= (int) $span['span'] ?>" class="text-center">
                            <?= View::e($view->t('roles.arm', ['arm' => (string) $span['arm_num']]) . ($span['name'] !== '' ? ' — ' . $span['name'] : '')) ?></th>
                    <?php endforeach; ?>
                </tr>
            <?php endif; ?>
            <tr>
                <?php if (!$multiArm): ?>
                    <th scope="col" class="clara-instrument-name"><?= View::e($view->t('record.instrument')) ?></th>
                <?php endif; ?>
                <?php foreach ($table['columns'] as $column): ?>
                    <th scope="col" class="small text-center text-nowrap<?= $column['unique_event_name'] === $uen ? ' table-active' : '' ?>"
                        data-event="<?= View::e($column['unique_event_name']) ?>" title="<?= View::e($column['unique_event_name']) ?>">
                        <?= View::e($column['event_name']) ?></th>
                <?php endforeach; ?>
            </tr>
            </thead>
            <tbody>
            <?php foreach ($table['rows'] as $row): ?>
                <?php $name = (string) $row['instrument']['name']; ?>
                <tr data-instrument="<?= View::e($name) ?>">
                    <th scope="row" class="small fw-normal text-nowrap clara-instrument-name<?= $name === $instrumentName ? ' table-active' : '' ?>">
                        <?= View::e($name) ?><?php if ($row['repeats']): ?> <span aria-hidden="true">⟳</span><?php endif; ?>
                        <?php if (!empty($row['instrument']['is_survey'])): ?><span class="badge text-bg-light border ms-1"><?= View::e($view->t('record.survey_badge')) ?></span><?php endif; ?>
                    </th>
                    <?php foreach ($row['cells'] as $i => $cell): ?>
                        <?php if ($cell === null): ?>
                            <td></td>
                            <?php continue; ?>
                        <?php endif; ?>
                        <?php
                        $column = $table['columns'][$i];
                        $atEvent = $column['unique_event_name'];
                        $eventLabel = $column['event_name'];
                        $open = $atEvent === $uen && $name === $instrumentName;
                        // Instrument branching is evaluated for the open event only, whose values
                        // the evaluator holds (§8.4): form.js hides that marker while it is false.
                        $branching = $atEvent === $uen ? (string) ($row['instrument']['branching_logic'] ?? '') : '';
                        ?>
                        <td class="text-center<?= $open ? ' table-active' : '' ?>" data-event="<?= View::e($atEvent) ?>"
                            data-instrument="<?= View::e($name) ?>" data-state="<?= View::e($cell['state']) ?>">
                            <span<?= $cell['repeats'] ? ' class="clara-instances"' : '' ?> data-clara-instrument="<?= View::e($name) ?>"<?= $branching !== '' ? ' data-clara-branching="' . View::e($branching) . '"' : '' ?>>
                                <?php foreach ($cell['markers'] as $marker): ?>
                                    <?php
                                    $n = (int) $marker['instance'];
                                    $count = $marker['count'];
                                    $lit = $count === null ? 0 : DataEntry::litTiles($count[0], $count[1]);
                                    $label = $n > 0
                                        ? $view->t('records.open_instance', ['record' => $record, 'instrument' => $name, 'event' => $eventLabel,
                                            'instance' => (string) $n, 'state' => $stateLabel($marker['state'], $count)])
                                        : $view->t('records.open_cell', ['record' => $record, 'instrument' => $name, 'event' => $eventLabel,
                                            'state' => $stateLabel($marker['state'], $count)]);
                                    $current = $open && ($n === 0 || $n === $instance);
                                    $counterClass = DataEntry::counterClass($marker['state'], $lit);
                                    $counterLit = $count === null ? null : $lit;
                                    ?>
                                    <a class="clara-state-link" href="<?= View::e($cellUrl($atEvent, $name, $n)) ?>"
                                       title="<?= View::e($label) ?>" aria-label="<?= View::e($label) ?>"<?= $current ? ' aria-current="page"' : '' ?>><?php require __DIR__ . '/../partials/counter.php'; ?></a>
                                <?php endforeach; ?>
                                <?php if ($cell['more'] > 0): ?>
                                    <?php $more = $view->t('records.more_instances', ['count' => (string) $cell['more']]); ?>
                                    <a class="clara-instance-more" href="<?= View::e($cellUrl($atEvent, $name)) ?>"
                                       title="<?= View::e($more) ?>" aria-label="<?= View::e($more) ?>">+<?= (int) $cell['more'] ?></a>
                                <?php endif; ?>
                                <?php if ($cell['add'] !== null): ?>
                                    <?php $addLabel = $view->t('records.add_instance', ['record' => $record, 'instrument' => $name,
                                        'event' => $eventLabel, 'instance' => (string) $cell['add']]); ?>
                                    <!-- "+" opens the instrument's next instance at this event (REQ-UI-055). -->
                                    <a class="clara-instance-add" href="<?= View::e($cellUrl($atEvent, $name, (int) $cell['add'])) ?>"
                                       title="<?= View::e($addLabel) ?>" aria-label="<?= View::e($addLabel) ?>"
                                       data-clara-add-instance="<?= (int) $cell['add'] ?>"<?= $open && $instance === $cell['add'] ? ' aria-current="page"' : '' ?>>+</a>
                                <?php endif; ?>
                            </span>
                        </td>
                    <?php endforeach; ?>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
<?php endif; ?>

<?php if ($instrument === null): ?>
    <p class="clara-region-empty"><?= View::e($view->t('record.no_instruments')) ?></p>
<?php else: ?>

    <?php if ($repeats): ?>
        <!-- The instances of this repeating instrument at this event (REQ-UI-055). -->
        <nav class="mb-3 clara-instance-nav" aria-label="<?= View::e($view->t('record.instances', ['instrument' => $instrumentName])) ?>">
            <ul class="nav nav-pills small align-items-center">
                <li class="nav-item text-body-secondary me-2"><?= View::e($view->t('record.instances', ['instrument' => $instrumentName])) ?></li>
                <?php foreach ($instanceList as $pill): ?>
                    <li class="nav-item" data-clara-instance="<?= (int) $pill['instance'] ?>">
                        <a class="nav-link py-0 px-2<?= $pill['current'] ? ' active' : '' ?>"<?= $pill['current'] ? ' aria-current="page"' : '' ?>
                           href="<?= View::e($openUrl($instrumentName, (int) $pill['instance'])) ?>">
                            <span class="clara-state <?= $stateBadge[$pill['state']] ?? $stateBadge['no_data'] ?>"
                                  title="<?= View::e($view->t('records.state.' . $pill['state'])) ?>"></span>
                            <?= View::e($view->t('record.instance', ['instance' => (string) $pill['instance']])) ?>
                        </a>
                    </li>
                <?php endforeach; ?>
            </ul>
        </nav>
    <?php endif; ?>

    <!-- The branching evaluator's inputs (§8.4): this event, the project's first event for the
         [field] shorthand, and the current values the logic reads outside this form — with the
         checkbox boxes and the choices of the fields they belong to. UI copy travels in the
         data-i18n block; this block is data for the evaluator only. -->
    <script type="application/json" data-clara-record-context><?= json_encode($clientContext,
        JSON_HEX_TAG | JSON_HEX_AMP | JSON_HEX_APOS | JSON_HEX_QUOT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) ?></script>

    <div class="alert alert-secondary py-2 d-none clara-instrument-hidden" data-clara-instrument-hidden>
        <?= View::e($view->t('record.instrument_hidden')) ?></div>

    <form method="post" action="<?= View::e($act('save')) ?>" class="clara-record-form mb-4" novalidate
          data-clara-record-form data-history="<?= View::e($recordUrl) ?>" data-event="<?= View::e($uen) ?>"
          data-instrument="<?= View::e($instrumentName) ?>"<?= $repeats ? ' data-instance="' . (int) $instance . '"' : '' ?>
          aria-label="<?= View::e($repeats ? $view->t('record.instance_of', ['instrument' => $instrumentName, 'instance' => (string) $instance]) : $instrumentName) ?>">
        <?php if ($canEdit): ?>
            <?= $view->csrfField() ?>
            <?= $hidden($here) ?>
            <!-- The browser's timezone, the collection zone of date values (GD-16); filled by record.js. -->
            <input type="hidden" name="tz" value="" data-clara-tz>
        <?php endif; ?>

        <?php if (isset($errors[''])): ?>
            <div class="alert alert-danger py-2"><?= View::e($errors['']) ?></div>
        <?php endif; ?>

        <?php require __DIR__ . '/../partials/form-fields.php'; ?>

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
        <!-- The link's state comes from the API on every render (REQ-UI-028): a submission spends
             it, not a look (REQ-API-145), so there is nothing to remember about an earlier click.
             Re-issue sits beside the delete action whose absence is the only reason it fails
             (REQ-API-146). -->
        <section class="card card-body mb-3 clara-survey-link" aria-labelledby="survey-link-title">
            <h2 class="h6" id="survey-link-title"><?= View::e($view->t('record.survey.title')) ?></h2>
            <p class="small mb-2<?= $link['state'] === 'submitted' ? ' fw-semibold' : '' ?>">
                <?= View::e($link['state'] === 'submitted'
                    ? $view->t('record.survey.state.submitted', ['date' => $link['collected_at']])
                    : $view->t('record.survey.state.' . $link['state'])) ?>
            </p>
            <?php if (!$exists): ?>
                <!-- A participant not stored yet: getting the link stores it first (REQ-UI-051). -->
                <p class="small mb-2"><?= View::e($view->t('record.survey.new_record', ['record' => $record])) ?></p>
            <?php endif; ?>
            <?php if ($link['url'] !== ''): ?>
                <div class="input-group input-group-sm mb-2">
                    <input class="form-control" type="text" id="clara-survey-url" value="<?= View::e($link['url']) ?>" readonly
                           aria-label="<?= View::e($view->t('record.survey.title')) ?>">
                    <button class="btn btn-outline-secondary" type="button" data-clara-copy="#clara-survey-url"><?= View::e($view->t('record.survey.copy')) ?></button>
                </div>
            <?php endif; ?>
            <div class="d-flex flex-wrap gap-2">
                <?php if ($exists): ?>
                    <form method="post" action="<?= View::e($act('survey_link')) ?>"
                          data-clara-confirm="<?= View::e($view->t('record.survey.confirm_get', ['record' => $record, 'instrument' => $instrumentName])) ?>">
                        <?= $view->csrfField() ?><?= $hidden($here) ?>
                        <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('record.survey.get')) ?></button>
                    </form>
                <?php else: ?>
                    <form method="post" action="<?= View::e($act('survey_link')) ?>"
                          data-clara-confirm="<?= View::e($view->t('record.survey.confirm_create',
                              ['record' => $record, 'instrument' => $instrumentName, 'event' => (string) ($event['event_name'] ?? $uen)])) ?>">
                        <?= $view->csrfField() ?><?= $hidden($here + ['create_record' => '1']) ?>
                        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('record.survey.get_first')) ?></button>
                    </form>
                <?php endif; ?>
                <?php if ($link['state'] === 'live'): ?>
                    <form method="post" action="<?= View::e($act('revoke_link')) ?>"
                          data-clara-confirm="<?= View::e($view->t('record.survey.confirm_revoke', ['record' => $record, 'instrument' => $instrumentName])) ?>">
                        <?= $view->csrfField() ?><?= $hidden($here) ?>
                        <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('record.survey.revoke')) ?></button>
                    </form>
                <?php endif; ?>
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
                $confirm = $view->t('record.delete.confirm.' . $scope, ['record' => $record, 'event' => (string) ($event['event_name'] ?? $uen),
                    'instrument' => $instrumentName, 'instance' => (string) $instance]);
            ?>
                <form method="post" action="<?= View::e($act('delete')) ?>" data-clara-confirm="<?= View::e($confirm) ?>">
                    <?= $view->csrfField() ?><?= $hidden($here + ['scope' => $scope]) ?>
                    <button class="btn btn-sm btn-outline-danger" type="submit" data-scope="<?= $scope ?>"><?= View::e($view->t('record.delete.' . $scope, ['instance' => (string) $instance])) ?></button>
                </form>
            <?php endforeach; ?>
        </div>
    </section>
<?php endif; ?>

<?php if (!empty($transferAccess)): ?>
    <!-- Transfer buttons (REQ-UI-065) sit in the field layout beside their labels; the forms
         they submit ride here, outside the values form — a form may not nest — and the button
         joins its form through the HTML `form` attribute. Each carries the §3.5 confirmation;
         execution re-checks target access server-side whatever this page decided (REQ-API-165). -->
    <?php foreach (array_keys($transferAccess) as $transferField): ?>
        <form id="clara-transfer-<?= View::e((string) $transferField) ?>" method="post" action="<?= View::e($act('transfer')) ?>" class="d-none"
              data-clara-confirm="<?= View::e($view->t('record.transfer.confirm', ['record' => $record, 'instrument' => $instrumentName])) ?>">
            <?= $view->csrfField() ?><?= $hidden(['event' => $uen, 'instrument' => $instrumentName, 'field' => (string) $transferField]) ?>
        </form>
    <?php endforeach; ?>
<?php endif; ?>
