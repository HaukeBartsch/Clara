<?php
/**
 * Control Panel — Projects (§5.2, REQ-UI-012). The project list with an Edit action, and one
 * form for create and edit covering the simplified creation fields of REQ-DB-006 (GD-17).
 * `?edit={id}` pre-fills it from the project's detail read. Creation makes arm 1 only; after
 * a create the page offers the link into the new project (§5.2 — its Setup arrives with M4,
 * so the link is the project's own entry, which lands on its first available section).
 */

use Clara\View;

/** @var list<array<string, mixed>> $projects */
/** @var array<string, string> $form */
/** @var list<string> $organizations */
$form = $form ?? [];
$editId = (int) ($editId ?? 0);
$createdId = (int) ($createdId ?? 0);
$value = static fn (string $key): string => View::e($form[$key] ?? '');
$fields = [
    ['project_name', 'text', true], ['pi_name', 'text', true], ['pi_email', 'email', true],
    ['dm_name', 'text', false], ['dm_email', 'email', false], ['rek_number', 'text', false],
    ['rek_start_date', 'date', false], ['rek_end_date', 'date', false],
    ['start_date', 'date', false], ['end_date', 'date', false],
];
$created = null;
foreach ($projects as $project) {
    if ((int) $project['id'] === $createdId) {
        $created = $project;
    }
}
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.projects.title')) ?></h1>

<?php if ($created !== null): ?>
    <div class="alert alert-info py-2" role="status">
        <a class="alert-link" href="/projects/<?= (int) $created['id'] ?>"><?= View::e($view->t('admin.projects.open_new', ['name' => (string) $created['project_name']])) ?></a>
    </div>
<?php endif; ?>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<details class="mb-3 clara-create"<?= ($editId !== 0 || ($error ?? '') !== '') ? ' open' : '' ?>>
    <summary class="btn btn-sm btn-primary"><?= View::e($view->t($editId !== 0 ? 'admin.projects.edit_title' : 'admin.projects.create')) ?></summary>
    <form method="post" action="/admin?section=projects&amp;action=<?= $editId !== 0 ? 'update_project' : 'create_project' ?>"
          class="card card-body mt-2" autocomplete="off">
        <?= $view->csrfField() ?>
        <?php if ($editId !== 0): ?>
            <input type="hidden" name="id" value="<?= $editId ?>">
        <?php endif; ?>
        <div class="row g-2">
            <?php foreach ($fields as [$key, $type, $required]): ?>
                <div class="col-md-6">
                    <label class="form-label" for="project-<?= $key ?>"><?= View::e($view->t('admin.projects.field.' . $key)) ?><?= $required ? ' *' : '' ?></label>
                    <input class="form-control form-control-sm" type="<?= $type ?>" id="project-<?= $key ?>" name="<?= $key ?>"
                           value="<?= $value($key) ?>"<?= $required ? ' required' : '' ?>>
                </div>
                <?php if ($key === 'project_name'): ?>
                    <div class="col-md-6">
                        <label class="form-label" for="project-organization"><?= View::e($view->t('admin.projects.field.organization')) ?> *</label>
                        <select class="form-select form-select-sm" id="project-organization" name="organization" required>
                            <option value=""></option>
                            <?php foreach ($organizations as $organization): ?>
                                <option value="<?= View::e($organization) ?>"<?= ($form['organization'] ?? '') === $organization ? ' selected' : '' ?>><?= View::e($organization) ?></option>
                            <?php endforeach; ?>
                        </select>
                    </div>
                <?php endif; ?>
            <?php endforeach; ?>
            <div class="col-md-6">
                <label class="form-label" for="project-participant_names"><?= View::e($view->t('admin.projects.field.participant_names')) ?> *</label>
                <input class="form-control form-control-sm font-monospace" type="text" id="project-participant_names" name="participant_names"
                       value="<?= $value('participant_names') ?>" required placeholder="8DISC[0-9][0-9][0-9]">
                <div class="form-text"><?= View::e($view->t('admin.projects.participant_names_help')) ?></div>
            </div>
        </div>
        <div class="d-flex gap-2 mt-2">
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t($editId !== 0 ? 'action.save' : 'admin.projects.create_submit')) ?></button>
            <?php if ($editId !== 0): ?>
                <a class="btn btn-sm btn-outline-secondary" href="/admin?section=projects"><?= View::e($view->t('action.cancel')) ?></a>
            <?php endif; ?>
        </div>
    </form>
</details>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-admin-projects">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('nav.project_list')) ?></th>
            <th scope="col" class="clara-stat"><?= View::e($view->t('dashboard.project.records')) ?></th>
            <th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($projects === []): ?>
            <tr><td colspan="3" class="clara-region-empty"><?= View::e($view->t('admin.projects.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($projects as $project): ?>
            <tr>
                <td>
                    <a href="/projects/<?= (int) $project['id'] ?>"><?= View::e($project['project_name']) ?></a>
                    <div class="text-body-secondary small"><?= View::e($project['organization']) ?></div>
                </td>
                <td class="clara-stat"><?= (int) $project['record_count'] ?></td>
                <td class="text-end">
                    <a class="btn btn-sm btn-outline-secondary" href="/admin?section=projects&amp;edit=<?= (int) $project['id'] ?>"><?= View::e($view->t('admin.projects.edit')) ?></a>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
