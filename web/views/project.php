<?php

/**
 * The project home (§6.1, REQ-UI-017): the summary counts, the read-only metadata
 * block, and the action cards that lead into the workspace.
 *
 * Nothing here edits the metadata — that is §5.2 for `project_admin`, and offering it
 * from the wrong page would be a permission decision made in the wrong place. Cards are
 * emitted by the controller only when the acting user may use them and the target route
 * exists in this build (§3.1, REQ-UI-003), so this template renders whatever list it is
 * given and stays silent when the list is empty.
 *
 * Timestamps and dates are printed as the API returned them: system values are UTC and
 * clinical dates are stored as entered, offsets included (Plan/Web_Implementation.md §7
 * rule 9). No locale reformatting happens anywhere in the web layer.
 */

use Clara\View;

/** @var array{records: int, instruments: int, fields: int} $summary */
$summary = $summary ?? ['records' => 0, 'instruments' => 0, 'fields' => 0];
/** @var array<string, string> $metadata */
$metadata = $metadata ?? [];
/** @var list<array{path: string, label: string, help: string}> $cards */
$cards = $cards ?? [];

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
<h1 class="h4 mb-3"><?= View::e($metadata['project_name'] ?? '') ?></h1>

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

<?php if ($cards !== []): ?>
    <h2 class="h6 mb-2"><?= View::e($view->t('project.actions')) ?></h2>
    <div class="row row-cols-1 row-cols-md-2 g-2 mb-4">
        <?php foreach ($cards as $card): ?>
            <div class="col">
                <a class="border rounded p-3 d-block h-100 text-decoration-none clara-card"
                   href="<?= View::e($card['path']) ?>">
                    <span class="d-block fw-semibold"><?= View::e($card['label']) ?></span>
                    <span class="d-block small text-body-secondary"><?= View::e($card['help']) ?></span>
                </a>
            </div>
        <?php endforeach; ?>
    </div>
<?php endif; ?>

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
