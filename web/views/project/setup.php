<?php
/**
 * Project page — Setup (§6.2, REQ-UI-018, project_admin). Arms (A) and instruments (C) apply
 * to the whole project; events (B) and the instrument × event matrix (D) belong to one arm
 * each and sit in that arm's tab — arm 1 shown by default (master spec; `?arm=` after a write
 * in another arm's tab).
 *
 * Edit controls are emitted only while the mode allows edits (development, analysis, or
 * production with an open staging set — §6.7); otherwise the page is the read-only structure
 * plus the staging prompt. Reorder controls are up/down forms that send the full ordered id
 * list; events offer them only where their place is not fixed by a timepoint (GD-15).
 *
 * Reads: $arms (each with events_display), $instruments, $mapping, $activeArm, $editEvent,
 * $editInstrument, $canEdit, $setupUrl, $designUrl, and the structure-mode partial's inputs.
 */

use Clara\View;

/** @var list<array<string, mixed>> $arms */
/** @var list<array<string, mixed>> $instruments */
$act = static fn (string $action): string => $setupUrl . '?action=' . $action;
$lastArm = count($arms) === 1;
$lastInstrument = count($instruments) === 1;
$eventCount = array_sum(array_map(static fn (array $a): int => count($a['events_display']), $arms));
$nullable = static fn (mixed $v): string => $v === null ? '' : (string) $v;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('setup.title')) ?></h1>

<?php require __DIR__ . '/../partials/structure-mode.php'; ?>

<!-- Block A — arms (not tabbed). -->
<section class="mb-4" aria-labelledby="setup-arms">
    <h2 class="h6" id="setup-arms"><?= View::e($view->t('setup.arms')) ?></h2>
    <div class="table-responsive">
        <table class="table table-sm align-middle clara-arms">
            <thead><tr>
                <th scope="col"><?= View::e($view->t('setup.arm.number')) ?></th>
                <th scope="col"><?= View::e($view->t('setup.arm.name')) ?></th>
                <?php if ($canEdit): ?><th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th><?php endif; ?>
            </tr></thead>
            <tbody>
            <?php foreach ($arms as $i => $arm): ?>
                <tr>
                    <td><?= (int) ($arm['arm_num'] ?? 0) ?></td>
                    <td><?= View::e($arm['name'] ?? '') ?></td>
                    <?php if ($canEdit): ?>
                        <td class="text-end text-nowrap">
                            <?php foreach (['up' => $i > 0, 'down' => $i < count($arms) - 1] as $dir => $possible): ?>
                                <?php if ($possible): ?>
                                    <form method="post" action="<?= View::e($act('move_arm')) ?>" class="d-inline">
                                        <?= $view->csrfField() ?>
                                        <input type="hidden" name="arm_id" value="<?= (int) ($arm['id'] ?? 0) ?>">
                                        <input type="hidden" name="direction" value="<?= $dir ?>">
                                        <button class="btn btn-sm btn-outline-secondary" type="submit"
                                                aria-label="<?= View::e($view->t('setup.move_' . $dir)) ?>"><?= $dir === 'up' ? '↑' : '↓' ?></button>
                                    </form>
                                <?php endif; ?>
                            <?php endforeach; ?>
                            <form method="post" action="<?= View::e($act('delete_arm')) ?>" class="d-inline"
                                  data-clara-confirm="<?= View::e($view->t($lastArm ? 'setup.arm.confirm_reset' : 'setup.arm.confirm_delete',
                                      ['arm' => (string) ($arm['arm_num'] ?? '')])) ?>">
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="arm_id" value="<?= (int) ($arm['id'] ?? 0) ?>">
                                <input type="hidden" name="name" value="<?= View::e($arm['name'] ?? '') ?>">
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
        <form method="post" action="<?= View::e($act('add_arm')) ?>" class="d-flex flex-wrap gap-2 align-items-end">
            <?= $view->csrfField() ?>
            <div>
                <label class="form-label mb-0 small" for="arm-name"><?= View::e($view->t('setup.arm.name')) ?></label>
                <input class="form-control form-control-sm" type="text" id="arm-name" name="name" required maxlength="255">
            </div>
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('setup.arm.add')) ?></button>
        </form>
    <?php endif; ?>
</section>

<!-- Block C — instruments (not tabbed). The fields of each are edited in the Designer (§7). -->
<section class="mb-4" aria-labelledby="setup-instruments">
    <h2 class="h6" id="setup-instruments"><?= View::e($view->t('setup.instruments')) ?></h2>
    <div class="table-responsive">
        <table class="table table-sm align-middle clara-instruments">
            <thead><tr>
                <th scope="col"><?= View::e($view->t('setup.instrument.name')) ?></th>
                <th scope="col" class="text-end"><?= View::e($view->t('setup.instrument.fields')) ?></th>
                <th scope="col"><?= View::e($view->t('setup.instrument.survey')) ?></th>
                <th scope="col"><?= View::e($view->t('setup.instrument.branching')) ?></th>
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
                            <a class="btn btn-sm btn-outline-primary" href="<?= View::e($setupUrl . '?edit_instrument=' . $iid . '#instrument-edit') ?>"><?= View::e($view->t('setup.edit')) ?></a>
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
                        $exprRefsUrl = $setupUrl;
                        require __DIR__ . '/../partials/expression-editor.php';
                        ?>
                    </div>
                </div>
                <div class="mt-2 d-flex gap-2">
                    <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
                    <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($setupUrl) ?>"><?= View::e($view->t('action.cancel')) ?></a>
                </div>
            </form>
        <?php endforeach; ?>

        <form method="post" action="<?= View::e($act('add_instrument')) ?>" class="d-flex flex-wrap gap-2 align-items-end">
            <?= $view->csrfField() ?>
            <div>
                <label class="form-label mb-0 small" for="instrument-name"><?= View::e($view->t('setup.instrument.name')) ?></label>
                <input class="form-control form-control-sm" type="text" id="instrument-name" name="name" required maxlength="255">
            </div>
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('setup.instrument.add')) ?></button>
        </form>
    <?php endif; ?>
</section>

<!-- Blocks B and D — per arm, one tab each, arm 1 by default. -->
<section aria-labelledby="setup-per-arm">
    <h2 class="h6" id="setup-per-arm"><?= View::e($view->t('setup.per_arm')) ?></h2>
    <ul class="nav nav-tabs mb-2" role="tablist">
        <?php foreach ($arms as $arm): ?>
            <?php $num = (int) ($arm['arm_num'] ?? 0); ?>
            <li class="nav-item" role="presentation">
                <button class="nav-link<?= $num === $activeArm ? ' active' : '' ?>" id="arm-tab-<?= $num ?>" type="button" role="tab"
                        data-bs-toggle="tab" data-bs-target="#arm-pane-<?= $num ?>" aria-controls="arm-pane-<?= $num ?>"
                        aria-selected="<?= $num === $activeArm ? 'true' : 'false' ?>">
                    <?= View::e($view->t('roles.arm', ['arm' => (string) $num]) . (($arm['name'] ?? '') !== '' ? ' — ' . $arm['name'] : '')) ?>
                </button>
            </li>
        <?php endforeach; ?>
    </ul>

    <div class="tab-content">
        <?php foreach ($arms as $arm): ?>
            <?php
            $num = (int) ($arm['arm_num'] ?? 0);
            $events = $arm['events_display'];
            $armMapping = $mapping[$num] ?? [];
            ?>
            <div class="tab-pane fade<?= $num === $activeArm ? ' show active' : '' ?>" id="arm-pane-<?= $num ?>" role="tabpanel"
                 aria-labelledby="arm-tab-<?= $num ?>" tabindex="0">

                <!-- Block B — events, canonical order (GD-15). -->
                <h3 class="h6 mt-2"><?= View::e($view->t('setup.events')) ?></h3>
                <div class="table-responsive">
                    <table class="table table-sm align-middle clara-events" data-arm="<?= $num ?>">
                        <thead><tr>
                            <th scope="col"><?= View::e($view->t('setup.event.label')) ?></th>
                            <th scope="col"><?= View::e($view->t('setup.event.unique')) ?></th>
                            <th scope="col" class="text-end"><?= View::e($view->t('setup.event.period')) ?></th>
                            <th scope="col" class="text-end"><?= View::e($view->t('setup.event.safe_start')) ?></th>
                            <th scope="col" class="text-end"><?= View::e($view->t('setup.event.safe_end')) ?></th>
                            <?php if ($canEdit): ?><th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th><?php endif; ?>
                        </tr></thead>
                        <tbody>
                        <?php if ($events === []): ?>
                            <tr><td colspan="6" class="clara-region-empty"><?= View::e($view->t('setup.event.empty')) ?></td></tr>
                        <?php endif; ?>
                        <?php foreach ($events as $event): ?>
                            <?php $eid = (int) ($event['id'] ?? 0); ?>
                            <?php if ($canEdit && $eid === $editEvent && $editEvent !== 0): ?>
                                <tr class="table-active">
                                    <td><input class="form-control form-control-sm" form="event-edit" type="text" name="event_name" required
                                               aria-label="<?= View::e($view->t('setup.event.label')) ?>" value="<?= View::e($event['event_name'] ?? '') ?>"></td>
                                    <td class="small text-body-secondary"><?= View::e($event['unique_event_name'] ?? '') ?></td>
                                    <td><input class="form-control form-control-sm text-end" form="event-edit" type="number" name="period"
                                               aria-label="<?= View::e($view->t('setup.event.period')) ?>" value="<?= View::e($nullable($event['period'] ?? null)) ?>"></td>
                                    <td><input class="form-control form-control-sm text-end" form="event-edit" type="number" name="safe_region_start"
                                               aria-label="<?= View::e($view->t('setup.event.safe_start')) ?>" value="<?= View::e($nullable($event['safe_region_start'] ?? null)) ?>"></td>
                                    <td><input class="form-control form-control-sm text-end" form="event-edit" type="number" name="safe_region_end"
                                               aria-label="<?= View::e($view->t('setup.event.safe_end')) ?>" value="<?= View::e($nullable($event['safe_region_end'] ?? null)) ?>"></td>
                                    <td class="text-end text-nowrap">
                                        <form method="post" action="<?= View::e($act('update_event')) ?>" id="event-edit" class="d-inline">
                                            <?= $view->csrfField() ?>
                                            <input type="hidden" name="event_id" value="<?= $eid ?>">
                                            <input type="hidden" name="arm_num" value="<?= $num ?>">
                                            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('action.save')) ?></button>
                                        </form>
                                        <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($setupUrl . '?arm=' . $num) ?>"><?= View::e($view->t('action.cancel')) ?></a>
                                    </td>
                                </tr>
                                <?php continue; ?>
                            <?php endif; ?>
                            <tr data-event="<?= View::e($event['unique_event_name'] ?? '') ?>">
                                <td><?= View::e($event['event_name'] ?? '') ?></td>
                                <td class="small text-body-secondary"><?= View::e($event['unique_event_name'] ?? '') ?></td>
                                <td class="text-end"><?= View::e($nullable($event['period'] ?? null)) ?></td>
                                <td class="text-end"><?= View::e($nullable($event['safe_region_start'] ?? null)) ?></td>
                                <td class="text-end"><?= View::e($nullable($event['safe_region_end'] ?? null)) ?></td>
                                <?php if ($canEdit): ?>
                                    <td class="text-end text-nowrap">
                                        <?php foreach (['up' => $event['can_move_up'], 'down' => $event['can_move_down']] as $dir => $possible): ?>
                                            <?php if ($possible): ?>
                                                <form method="post" action="<?= View::e($act('move_event')) ?>" class="d-inline">
                                                    <?= $view->csrfField() ?>
                                                    <input type="hidden" name="event_id" value="<?= $eid ?>">
                                                    <input type="hidden" name="arm_num" value="<?= $num ?>">
                                                    <input type="hidden" name="direction" value="<?= $dir ?>">
                                                    <button class="btn btn-sm btn-outline-secondary" type="submit"
                                                            aria-label="<?= View::e($view->t('setup.move_' . $dir)) ?>"><?= $dir === 'up' ? '↑' : '↓' ?></button>
                                                </form>
                                            <?php endif; ?>
                                        <?php endforeach; ?>
                                        <a class="btn btn-sm btn-outline-primary" href="<?= View::e($setupUrl . '?arm=' . $num . '&edit_event=' . $eid) ?>"><?= View::e($view->t('setup.edit')) ?></a>
                                        <form method="post" action="<?= View::e($act('delete_event')) ?>" class="d-inline"
                                              data-clara-confirm="<?= View::e($view->t($eventCount === 1 ? 'setup.event.confirm_reset' : 'setup.event.confirm_delete',
                                                  ['name' => (string) ($event['event_name'] ?? '')])) ?>">
                                            <?= $view->csrfField() ?>
                                            <input type="hidden" name="event_id" value="<?= $eid ?>">
                                            <input type="hidden" name="arm_num" value="<?= $num ?>">
                                            <input type="hidden" name="name" value="<?= View::e($event['event_name'] ?? '') ?>">
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
                    <form method="post" action="<?= View::e($act('add_event')) ?>" class="row g-2 align-items-end mb-4 clara-add-event">
                        <?= $view->csrfField() ?>
                        <input type="hidden" name="arm_num" value="<?= $num ?>">
                        <div class="col-sm-4">
                            <label class="form-label mb-0 small" for="event-name-<?= $num ?>"><?= View::e($view->t('setup.event.label')) ?></label>
                            <input class="form-control form-control-sm" type="text" id="event-name-<?= $num ?>" name="event_name" required maxlength="255">
                        </div>
                        <div class="col-sm-2">
                            <label class="form-label mb-0 small" for="event-period-<?= $num ?>"><?= View::e($view->t('setup.event.period')) ?></label>
                            <input class="form-control form-control-sm" type="number" id="event-period-<?= $num ?>" name="period">
                        </div>
                        <div class="col-sm-2">
                            <label class="form-label mb-0 small" for="event-start-<?= $num ?>"><?= View::e($view->t('setup.event.safe_start')) ?></label>
                            <input class="form-control form-control-sm" type="number" id="event-start-<?= $num ?>" name="safe_region_start">
                        </div>
                        <div class="col-sm-2">
                            <label class="form-label mb-0 small" for="event-end-<?= $num ?>"><?= View::e($view->t('setup.event.safe_end')) ?></label>
                            <input class="form-control form-control-sm" type="number" id="event-end-<?= $num ?>" name="safe_region_end">
                        </div>
                        <div class="col-sm-2"><button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('setup.event.add')) ?></button></div>
                        <div class="col-12 form-text mt-0"><?= View::e($view->t('setup.event.period_help')) ?></div>
                    </form>
                <?php endif; ?>

                <!-- Block D — instruments × events, this arm (REQ-API-072/073). -->
                <h3 class="h6"><?= View::e($view->t('setup.mapping')) ?></h3>
                <?php if ($events === [] || $instruments === []): ?>
                    <p class="clara-region-empty small"><?= View::e($view->t('setup.mapping.empty')) ?></p>
                <?php else: ?>
                    <?php if ($canEdit): ?>
                    <form method="post" action="<?= View::e($act('save_mapping')) ?>" class="clara-mapping" data-arm="<?= $num ?>">
                        <?= $view->csrfField() ?>
                        <input type="hidden" name="arm_num" value="<?= $num ?>">
                    <?php endif; ?>
                        <div class="table-responsive">
                            <table class="table table-sm table-bordered align-middle text-center">
                                <thead><tr>
                                    <th scope="col" class="text-start"><?= View::e($view->t('setup.instrument.name')) ?></th>
                                    <?php foreach ($events as $event): ?>
                                        <th scope="col" class="small"><?= View::e($event['event_name'] ?? '') ?></th>
                                    <?php endforeach; ?>
                                </tr></thead>
                                <tbody>
                                <?php foreach ($instruments as $instrument): ?>
                                    <?php
                                    $iname = (string) ($instrument['name'] ?? '');
                                    $mapped = is_array($armMapping[$iname] ?? null) ? $armMapping[$iname] : [];
                                    ?>
                                    <tr>
                                        <th scope="row" class="text-start fw-normal"><?= View::e($iname) ?></th>
                                        <?php foreach ($events as $event): ?>
                                            <?php
                                            $on = in_array((string) ($event['unique_event_name'] ?? ''), $mapped, true);
                                            $cellLabel = $iname . ' × ' . (string) ($event['event_name'] ?? '');
                                            ?>
                                            <td>
                                                <?php if ($canEdit): ?>
                                                    <input class="form-check-input" type="checkbox" aria-label="<?= View::e($cellLabel) ?>"
                                                           name="map[<?= (int) ($instrument['id'] ?? 0) ?>][]" value="<?= (int) ($event['id'] ?? 0) ?>"
                                                           data-pair="<?= View::e($iname . '|' . ($event['unique_event_name'] ?? '')) ?>"<?= $on ? ' checked' : '' ?>>
                                                <?php elseif ($on): ?>
                                                    <span aria-label="<?= View::e($cellLabel) ?>">✓</span>
                                                <?php endif; ?>
                                            </td>
                                        <?php endforeach; ?>
                                    </tr>
                                <?php endforeach; ?>
                                </tbody>
                            </table>
                        </div>
                    <?php if ($canEdit): ?>
                        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('setup.mapping.apply')) ?></button>
                    </form>
                    <?php endif; ?>
                <?php endif; ?>
            </div>
        <?php endforeach; ?>
    </div>
</section>
