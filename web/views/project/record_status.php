<?php
/**
 * Project page — the Record Status Dashboard (§6.3, REQ-UI-019): the participant overview.
 *
 * The page renders the structure — one tab per arm the member may read, its events as column
 * groups with their instruments in order — as a table head per arm with a loading row; the rows
 * are the route's `records` data region, bound by record-status.js (REQ-UI-032/044), which reads
 * that head as the column structure and replaces the table with a virtually rendered Tabulator
 * grid (AGENTS.md "use Tabulator"). Each cell is a link to the record view on that (record,
 * event, instrument) carrying the three-state colour code: grey no data, amber some data, green
 * finished (§6.3), on the master spec's 3×3 counter whose "on" tiles show how many of the
 * instrument's fields hold a value (REQ-UI-081) — a count record-status.js reads from $countUrl
 * beside the rows, and draws by cloning the marker from the page's template; every marker
 * draws the page's one counter SVG and differs from the others only in its classes. No field
 * value is ever on this page (REQ-API-074).
 *
 * New participant (§6.3): the control the project's naming mode offers — type a record ID, or
 * one click that assigns the next number (REQ-UI-052), never both; present only for a member
 * with data access ≥ view_edit on some arm, and never in analysis mode (§8.1). A viewer who
 * holds no project token because they are not a member (REQ-API-102) gets the explanation in
 * the form's place instead of an affordance that would only fail at Save.
 *
 * Reads: $arms, $newRecord (null | {arms, name, arm, naming}), $notMember, $statusUrl,
 * $countUrl, $recordBase.
 */

use Clara\View;

/** @var list<array{arm_num: int, name: string, events: list<array<string, mixed>>}> $arms */
$multiArm = count($arms) > 1;
$activeArm = (int) ($arms[0]['arm_num'] ?? 0);
?>
<h1 class="h4 mb-3"><?= View::e($view->t('records.title')) ?></h1>

<?php if ($newRecord !== null): ?>
    <?php $autoNaming = ($newRecord['naming'] ?? 'manual') === 'auto'; ?>
    <!-- New participant (§6.3), one control per naming mode and never both (REQ-UI-052):
         type the record ID, or let auto-name number the participant in one click. -->
    <form method="post" action="<?= View::e($statusUrl) ?>?action=<?= $autoNaming ? 'create_auto' : 'new_record' ?>"
          class="row g-2 align-items-end mb-3 clara-new-record">
        <?= $view->csrfField() ?>
        <?php if (!$autoNaming): ?>
            <div class="col-sm-5 col-md-4">
                <label class="form-label mb-0 small" for="new-record-id"><?= View::e($view->t('records.new.label')) ?></label>
                <input class="form-control form-control-sm" type="text" id="new-record-id" name="record_id" maxlength="100"
                       value="<?= View::e($newRecord['name']) ?>" autocomplete="off">
            </div>
        <?php endif; ?>
        <?php if (count($newRecord['arms']) > 1): ?>
            <div class="col-sm-3 col-md-2">
                <label class="form-label mb-0 small" for="new-record-arm"><?= View::e($view->t('records.new.arm')) ?></label>
                <select class="form-select form-select-sm" id="new-record-arm" name="arm">
                    <?php foreach ($newRecord['arms'] as $arm): ?>
                        <option value="<?= (int) $arm['arm_num'] ?>"<?= (int) $arm['arm_num'] === $newRecord['arm'] ? ' selected' : '' ?>>
                            <?= View::e($view->t('roles.arm', ['arm' => (string) $arm['arm_num']]) . ($arm['name'] !== '' ? ' — ' . $arm['name'] : '')) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
        <?php else: ?>
            <input type="hidden" name="arm" value="<?= (int) $newRecord['arms'][0]['arm_num'] ?>">
        <?php endif; ?>
        <div class="col-auto d-flex gap-2">
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t($autoNaming ? 'records.new.auto.add' : 'records.new.open')) ?></button>
        </div>
        <div class="col-12 form-text mt-0"><?= View::e($view->t($autoNaming ? 'records.new.auto.help' : 'records.new.help')) ?></div>
    </form>
<?php elseif (!empty($notMember)): ?>
    <!-- The viewer sees the project without belonging to it (REQ-API-102): no token, so no
         entry — say what to do about it where the new-participant control would have been. -->
    <div class="alert alert-warning py-2 mb-3 clara-new-record-member-required" role="alert">
        <?= View::e($view->t('records.new.not_member')) ?>
    </div>
<?php endif; ?>

<!-- The counter SVG, once for every marker on the page (REQ-UI-081). -->
<?php require __DIR__ . '/../partials/counter-sprite.php'; ?>

<!-- The colour code (§6.3) on the completeness counter (REQ-UI-081): each state in its colouring
     with an example count — none, four and all nine tiles on. -->
<p class="small mb-1 clara-legend">
    <?php foreach (['no_data' => '', 'some_data' => 'counter-yellow counter-3', 'finished' => 'counter-green counter-8'] as $state => $counterClass): ?>
        <?php require __DIR__ . '/../partials/counter.php'; ?>
        <span class="me-3"><?= View::e($view->t('records.state.' . $state)) ?></span>
    <?php endforeach; ?>
</p>
<!-- The marker record-status.js clones into every cell, adding its colouring and count. -->
<template id="clara-counter-marker"><?php $counterClass = ''; require __DIR__ . '/../partials/counter.php'; ?></template>
<p class="small text-body-secondary mb-2"><?= View::e($view->t('records.dots.help')) ?></p>
<!-- Shown by record-status.js when the count cannot be read: the dots then show the state only. -->
<p class="small text-danger mb-2 clara-counts-failed" role="status" hidden><?= View::e($view->t('records.counts_failed')) ?></p>

<?php if ($arms === []): ?>
    <p class="clara-region-empty"><?= View::e($view->t('records.no_arms')) ?></p>
<?php endif; ?>

<?php if ($multiArm): ?>
    <ul class="nav nav-tabs mb-2" role="tablist">
        <?php foreach ($arms as $arm): ?>
            <?php $num = (int) $arm['arm_num']; ?>
            <li class="nav-item" role="presentation">
                <button class="nav-link<?= $num === $activeArm ? ' active' : '' ?>" id="status-tab-<?= $num ?>" type="button" role="tab"
                        data-bs-toggle="tab" data-bs-target="#status-pane-<?= $num ?>" aria-controls="status-pane-<?= $num ?>"
                        aria-selected="<?= $num === $activeArm ? 'true' : 'false' ?>">
                    <?= View::e($view->t('roles.arm', ['arm' => (string) $num]) . ($arm['name'] !== '' ? ' — ' . $arm['name'] : '')) ?>
                </button>
            </li>
        <?php endforeach; ?>
    </ul>
<?php endif; ?>

<div class="<?= $multiArm ? 'tab-content' : '' ?>">
    <?php foreach ($arms as $arm): ?>
        <?php
        $num = (int) $arm['arm_num'];
        $events = array_values(array_filter($arm['events'], static fn (array $e): bool => $e['instruments'] !== []));
        $columns = 1;
        foreach ($events as $event) {
            $columns += count($event['instruments']);
        }
        ?>
        <div class="<?= $multiArm ? 'tab-pane fade' . ($num === $activeArm ? ' show active' : '') : '' ?>"
             id="status-pane-<?= $num ?>"<?= $multiArm ? ' role="tabpanel" aria-labelledby="status-tab-' . $num . '" tabindex="0"' : '' ?>>
            <?php if ($events === []): ?>
                <p class="clara-region-empty"><?= View::e($view->t('records.no_instruments')) ?></p>
            <?php else: ?>
                <!-- The arm's `records` region: the table below is its column structure and loading
                     state until record-status.js builds the grid in its place. -->
                <div class="clara-record-status" data-arm="<?= $num ?>" data-region="records" data-source="<?= View::e($statusUrl) ?>"
                     data-count-source="<?= View::e($countUrl) ?>" data-record-base="<?= View::e($recordBase) ?>" data-columns="<?= $columns ?>">
                    <div class="table-responsive">
                        <table class="table table-sm table-bordered align-middle">
                            <thead>
                            <tr>
                                <th scope="col" rowspan="2" class="align-bottom"><?= View::e($view->t('records.record')) ?></th>
                                <?php foreach ($events as $event): ?>
                                    <th scope="colgroup" colspan="<?= count($event['instruments']) ?>" class="text-center"
                                        title="<?= View::e($event['unique_event_name']) ?>"><?= View::e($event['event_name']) ?></th>
                                <?php endforeach; ?>
                            </tr>
                            <tr>
                                <?php foreach ($events as $event): ?>
                                    <?php foreach ($event['instruments'] as $instrument): ?>
                                        <!-- data-col keys the cell record-status.js puts under this header; a
                                             repeating instrument's column also says so, and whether "+" is
                                             offered in it (REQ-UI-055). -->
                                        <th scope="col" class="small text-center clara-status-col"
                                            data-col="<?= View::e($event['unique_event_name'] . '|' . $instrument['name']) ?>"
                                            data-event="<?= View::e($event['unique_event_name']) ?>"
                                            data-event-label="<?= View::e($event['event_name']) ?>"
                                            data-instrument="<?= View::e($instrument['name']) ?>"
                                            <?php if (!empty($instrument['repeating'])): ?>data-repeating="1"<?= !empty($instrument['canAdd']) ? ' data-add="1"' : '' ?>
                                            title="<?= View::e($view->t('records.repeating_column', ['instrument' => $instrument['name']])) ?>"<?php endif; ?>><?= View::e($instrument['name']) ?><?php if (!empty($instrument['repeating'])): ?> <span aria-hidden="true">⟳</span><?php endif; ?></th>
                                    <?php endforeach; ?>
                                <?php endforeach; ?>
                            </tr>
                            </thead>
                            <tbody>
                            <tr class="clara-region-notice"><td colspan="<?= $columns ?>" class="clara-region-empty"><?= View::e($view->t('js.loading')) ?></td></tr>
                            </tbody>
                        </table>
                    </div>
                </div>
            <?php endif; ?>
        </div>
    <?php endforeach; ?>
</div>
