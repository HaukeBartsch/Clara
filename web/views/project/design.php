<?php
/**
 * Project page — Design (§7.1, REQ-UI-021, project_admin): the project's instruments in
 * position order; choosing one opens its field editor. Creating, reordering and the survey /
 * branching attributes of instruments belong to Setup block C (§6.2) — this page edits fields.
 *
 * Reads: $instruments, $designUrl, $setupUrl, and the structure-mode partial's inputs.
 */

use Clara\View;

/** @var list<array<string, mixed>> $instruments */
?>
<h1 class="h4 mb-3"><?= View::e($view->t('design.title')) ?></h1>

<?php require __DIR__ . '/../partials/structure-mode.php'; ?>

<p class="small text-body-secondary"><?= View::e($view->t('design.intro')) ?>
    <a href="<?= View::e($setupUrl) ?>"><?= View::e($view->t('nav.setup')) ?></a></p>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-design-instruments">
        <thead><tr>
            <th scope="col"><?= View::e($view->t('setup.instrument.name')) ?></th>
            <th scope="col" class="text-end"><?= View::e($view->t('setup.instrument.fields')) ?></th>
            <th scope="col"><?= View::e($view->t('setup.instrument.survey')) ?></th>
            <th scope="col"><?= View::e($view->t('setup.instrument.branching')) ?></th>
        </tr></thead>
        <tbody>
        <?php foreach ($instruments as $instrument): ?>
            <tr>
                <td><a href="<?= View::e($designUrl . '/instruments/' . (int) ($instrument['id'] ?? 0)) ?>"><?= View::e($instrument['name'] ?? '') ?></a></td>
                <td class="text-end"><?= (int) ($instrument['field_count'] ?? 0) ?></td>
                <td><?php if (!empty($instrument['is_survey'])): ?><span class="badge text-bg-info"><?= View::e($view->t('setup.instrument.survey_badge')) ?></span><?php endif; ?></td>
                <td><code class="small"><?= View::e($instrument['branching_logic'] ?? '') ?></code></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
