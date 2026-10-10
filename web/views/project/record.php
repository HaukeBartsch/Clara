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
            <?php if ($link['url'] !== ''): ?>
                <div class="input-group input-group-sm mb-2">
                    <input class="form-control" type="text" id="clara-survey-url" value="<?= View::e($link['url']) ?>" readonly
                           aria-label="<?= View::e($view->t('record.survey.title')) ?>">
                    <button class="btn btn-outline-secondary" type="button" data-clara-copy="#clara-survey-url"><?= View::e($view->t('record.survey.copy')) ?></button>
                </div>
            <?php endif; ?>
            <div class="d-flex flex-wrap gap-2">
                <form method="post" action="<?= View::e($act('survey_link')) ?>"
                      data-clara-confirm="<?= View::e($view->t('record.survey.confirm_get', ['record' => $record, 'instrument' => $instrumentName])) ?>">
                    <?= $view->csrfField() ?><?= $hidden($here) ?>
                    <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('record.survey.get')) ?></button>
                </form>
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
