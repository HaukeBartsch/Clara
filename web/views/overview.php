<?php

/**
 * The project's Overview section (§6.1, REQ-UI-017): the summary counts and the read-only
 * metadata block, shown in the right-hand panel of the project page. The functions that
 * used to be action cards are the left panel's entries now (§2.4 B, REQ-UI-046), so this
 * template shows information only — no navigation of its own, and nothing that edits the
 * metadata (that is §5.2 for `project_admin`, and offering it from the wrong page would be
 * a permission decision made in the wrong place).
 *
 * The mode badge of §6.6 sits beside the project name (REQ-UI-033), from `GET …/mode`; when
 * that read is refused or fails, nothing is emitted for it rather than a placeholder.
 *
 * Below the information an installation administrator — and nobody else (REQ-UI-003) — gets
 * two cards: the mode card (§6.6) and the End of project card (§6.5, REQ-UI-084), whose end
 * provision and project deletion are the destructive actions of this page.
 *
 * Timestamps and dates are printed as the API returned them: system values are UTC and
 * clinical dates are stored as entered, offsets included (User_Interface_Design.md §12
 * rule 9). No locale reformatting happens anywhere in the web layer.
 */

use Clara\View;

/** @var array{records: int, instruments: int, fields: int} $summary */
$summary = $summary ?? ['records' => 0, 'instruments' => 0, 'fields' => 0];
/** @var array<string, string> $metadata */
$metadata = $metadata ?? [];

/** metadata field name → the catalog key holding its label (the §6.1 block). */
$labels = [
    'project_name' => 'project.field.project_name',
    'organization' => 'project.field.organization',
    'pi_name' => 'project.field.pi_name',
    'pi_email' => 'project.field.pi_email',
    'dm_name' => 'project.field.dm_name',
    'dm_email' => 'project.field.dm_email',
    'rek_number' => 'project.field.rek_number',
    'rek_start_date' => 'project.field.rek_start_date',
    'rek_end_date' => 'project.field.rek_end_date',
    'start_date' => 'project.field.start_date',
    'end_date' => 'project.field.end_date',
    'participant_names' => 'project.field.participant_names',
];
?>
<h1 class="h4 mb-3"><?= View::e($metadata['project_name'] ?? '') ?>
    <?php $badgeMode = $mode ?? ''; require __DIR__ . '/partials/mode-badge.php'; ?></h1>

<!-- Summary (REQ-UI-017): the three counts, from the one detail read this page makes. -->
<div class="row row-cols-1 row-cols-sm-3 g-2 mb-4 clara-summary">
    <?php foreach ([
        ['key' => 'records', 'labelKey' => 'project.summary.records'],
        ['key' => 'instruments', 'labelKey' => 'project.summary.instruments'],
        ['key' => 'fields', 'labelKey' => 'project.summary.fields'],
    ] as $stat): ?>
        <div class="col">
            <div class="border rounded p-2 h-100">
                <div class="text-body-secondary small"><?= View::e($view->t($stat['labelKey'])) ?></div>
                <div class="fs-4"><?= View::e((string) $summary[$stat['key']]) ?></div>
            </div>
        </div>
    <?php endforeach; ?>
</div>

<h2 class="h6 mb-2"><?= View::e($view->t('project.metadata')) ?></h2>
<table class="table table-sm clara-metadata">
    <tbody>
        <?php foreach ($metadata as $field => $value): ?>
            <?php if (!isset($labels[$field]) || $field === 'project_name') {
                continue; // the name is the page heading, not a row of its own block
            } ?>
            <tr>
                <th scope="row" class="text-body-secondary fw-normal"><?= View::e($view->t($labels[$field])) ?></th>
                <td><?= View::e($value) ?></td>
            </tr>
        <?php endforeach; ?>
    </tbody>
</table>

<?php if (($modeCard ?? null) !== null): ?>
    <!-- Mode card (§6.6, REQ-UI-033): is_admin only, and only the allowed transitions.
         Every change confirms first (§3.5); leaving development asks whether stored data is
         kept, and deleting it takes a second, explicit confirmation. -->
    <?php $cardBase = '/projects/' . (int) ($projectId ?? 0) . '/overview?action=set_mode'; ?>
    <section class="card mt-4 clara-mode-card" aria-labelledby="mode-card-title">
        <div class="card-body">
            <h2 class="h6" id="mode-card-title"><?= View::e($view->t('mode.card_title')) ?></h2>
            <p class="small text-body-secondary mb-2"><?= View::e($view->t('mode.current', ['mode' => $view->t('project.mode.' . $modeCard['mode'])])) ?></p>

            <?php if ($modeCard['stagingOpen']): ?>
                <!-- No transition while a staging set is open (§6.6): the card points at the
                     banner instead, where the set is committed or discarded. -->
                <p class="mb-0 small"><?= View::e($view->t('mode.staging_blocks')) ?>
                    <a href="<?= View::e($modeCard['setupUrl']) ?>"><?= View::e($view->t('mode.to_staging')) ?></a></p>
            <?php endif; ?>

            <div class="d-flex flex-wrap gap-3 align-items-start">
                <?php foreach ($modeCard['transitions'] as $target): ?>
                    <?php $targetLabel = $view->t('project.mode.' . $target); ?>
                    <?php if ($modeCard['mode'] === 'development' && $target === 'production'): ?>
                        <form method="post" action="<?= View::e($cardBase) ?>"
                              data-clara-confirm="<?= View::e($view->t('mode.confirm.keep', ['mode' => $targetLabel])) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="mode" value="production">
                            <input type="hidden" name="keep_data" value="1">
                            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('mode.to_production_keep')) ?></button>
                        </form>
                        <form method="post" action="<?= View::e($cardBase) ?>" class="border border-danger-subtle rounded p-2"
                              data-clara-confirm="<?= View::e($view->t('mode.confirm.delete')) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="mode" value="production">
                            <input type="hidden" name="keep_data" value="0">
                            <div class="form-check small mb-2">
                                <input class="form-check-input" type="checkbox" id="mode-confirm-delete" name="confirm_delete" value="1" required>
                                <label class="form-check-label" for="mode-confirm-delete"><?= View::e($view->t('mode.delete_ack')) ?></label>
                            </div>
                            <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('mode.to_production_delete')) ?></button>
                        </form>
                    <?php else: ?>
                        <form method="post" action="<?= View::e($cardBase) ?>"
                              data-clara-confirm="<?= View::e($view->t($target === 'analysis' ? 'mode.confirm.analysis' : 'mode.confirm.keep', ['mode' => $targetLabel])) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="mode" value="<?= View::e($target) ?>">
                            <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('mode.to', ['mode' => $targetLabel])) ?></button>
                        </form>
                    <?php endif; ?>
                <?php endforeach; ?>
            </div>
        </div>
    </section>
<?php endif; ?>

<?php if (($endCard ?? null) !== null): ?>
    <!-- End of project card (§6.5, REQ-UI-084): is_admin only. The end provision opens its own
         confirmation modal holding the choice; deleting the whole project needs the typed word
         — `required` + `pattern`, so the browser holds the submit until it is there, and the
         controller checks it again — and then the §3.5 confirmation. -->
    <?php $endBase = '/projects/' . (int) ($projectId ?? 0) . '/overview?action='; ?>
    <section class="card mt-4 border-danger-subtle clara-end-card" aria-labelledby="end-card-title">
        <div class="card-body">
            <h2 class="h6" id="end-card-title"><?= View::e($view->t('endproject.card_title')) ?></h2>
            <p class="small text-body-secondary"><?= View::e($view->t('endproject.intro')) ?></p>

            <h3 class="h6 mt-3"><?= View::e($view->t('endproject.provision_title')) ?></h3>
            <p class="small mb-2"><?= View::e($view->t('endproject.provision_intro')) ?></p>
            <button class="btn btn-sm btn-outline-danger" type="button" data-bs-toggle="modal" data-bs-target="#clara-end-provision">
                <?= View::e($view->t('endproject.provision_open')) ?></button>

            <hr>

            <h3 class="h6"><?= View::e($view->t('endproject.delete_title')) ?></h3>
            <p class="small mb-2"><?= View::e($view->t('endproject.delete_warning')) ?></p>
            <div class="alert alert-warning small py-2 clara-delete-export" role="note">
                <?= View::e($view->t('endproject.delete_export')) ?>
                <a class="alert-link" href="<?= View::e($endCard['exportUrl']) ?>"><?= View::e($view->t('endproject.delete_export_link')) ?></a>
            </div>
            <?php $typeLabel = $view->t('endproject.delete_type', ['word' => $endCard['confirmWord']]); ?>
            <form method="post" action="<?= View::e($endBase . 'delete_project') ?>" class="clara-delete-project"
                  data-clara-confirm="<?= View::e($view->t('endproject.delete_confirm', ['name' => $endCard['projectName']])) ?>">
                <?= $view->csrfField() ?>
                <label class="form-label small" for="delete-confirm-text"><?= View::e($typeLabel) ?></label>
                <div class="d-flex flex-wrap gap-2 align-items-start">
                    <input class="form-control form-control-sm w-auto" type="text" id="delete-confirm-text" name="confirm_text"
                           required pattern="<?= View::e($endCard['confirmWord']) ?>" title="<?= View::e($typeLabel) ?>"
                           autocomplete="off" autocapitalize="off" spellcheck="false"
                           data-clara-typed-confirm="<?= View::e($endCard['confirmWord']) ?>">
                    <button class="btn btn-sm btn-danger" type="submit"><?= View::e($view->t('endproject.delete_submit')) ?></button>
                </div>
            </form>
        </div>
    </section>

    <div class="modal fade" id="clara-end-provision" tabindex="-1" aria-labelledby="clara-end-provision-title" aria-hidden="true">
        <div class="modal-dialog">
            <form class="modal-content" method="post" action="<?= View::e($endBase . 'end_provision') ?>">
                <?= $view->csrfField() ?>
                <div class="modal-header">
                    <h2 class="modal-title h6" id="clara-end-provision-title"><?= View::e($view->t('endproject.provision_modal_title')) ?></h2>
                    <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="<?= View::e($view->t('action.close')) ?>"></button>
                </div>
                <div class="modal-body">
                    <p class="small"><?= View::e($view->t('endproject.provision_warning')) ?></p>
                    <?php foreach (['delete', 'anonymize'] as $provision): ?>
                        <div class="form-check mb-2">
                            <input class="form-check-input" type="radio" name="provision" required
                                   id="end-provision-<?= View::e($provision) ?>" value="<?= View::e($provision) ?>">
                            <label class="form-check-label" for="end-provision-<?= View::e($provision) ?>">
                                <?= View::e($view->t('endproject.provision.' . $provision)) ?>
                                <span class="d-block small text-body-secondary"><?= View::e($view->t('endproject.provision.' . $provision . '_help')) ?></span>
                            </label>
                        </div>
                    <?php endforeach; ?>
                </div>
                <div class="modal-footer">
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal"><?= View::e($view->t('action.cancel')) ?></button>
                    <button type="submit" class="btn btn-sm btn-danger"><?= View::e($view->t('endproject.provision_submit')) ?></button>
                </div>
            </form>
        </div>
    </div>
<?php endif; ?>
