<?php
/**
 * Project page — Role editor (§5.4, is_admin, REQ-UI-014). Roles are create-only — the API has
 * no role update or delete (Plan/Web_Implementation.md §7 rule 5), so none is offered. The
 * create form has one block per arm with a data access level and an export level; an arm left
 * at the defaults submits no_access / export_none (no implicit access, REQ-AUTH-019).
 */

use Clara\View;

/** @var list<array<string, mixed>> $roles */
/** @var list<array<string, mixed>> $arms */
$base = '/projects/' . (int) $projectId . '/roles';
?>
<h1 class="h4 mb-3"><?= View::e($view->t('roles.title')) ?></h1>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-roles">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('roles.name')) ?></th>
            <th scope="col"><?= View::e($view->t('roles.project_admin')) ?></th>
            <th scope="col"><?= View::e($view->t('roles.arms')) ?></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($roles === []): ?>
            <tr><td colspan="3" class="clara-region-empty"><?= View::e($view->t('roles.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($roles as $role): ?>
            <tr>
                <td><?= View::e($role['name'] ?? '') ?></td>
                <td><?= !empty($role['project_admin']) ? '<span class="badge text-bg-primary">' . View::e($view->t('roles.yes')) . '</span>' : '' ?></td>
                <td class="small">
                    <?php foreach ((is_array($role['arms'] ?? null) ? $role['arms'] : []) as $armNum => $levels): ?>
                        <div><?= View::e($view->t('roles.arm', ['arm' => (string) $armNum])) ?>:
                            <code><?= View::e($levels['data'] ?? '') ?></code> · <code><?= View::e($levels['export'] ?? '') ?></code></div>
                    <?php endforeach; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<h2 class="h6 mt-4"><?= View::e($view->t('roles.create')) ?></h2>
<form method="post" action="<?= $base ?>?action=create_role" class="card card-body">
    <?= $view->csrfField() ?>
    <div class="row g-2 align-items-end mb-2">
        <div class="col-md-6">
            <label class="form-label mb-0" for="role-name"><?= View::e($view->t('roles.name')) ?></label>
            <input class="form-control form-control-sm" type="text" id="role-name" name="name" required maxlength="255">
        </div>
        <div class="col-md-6">
            <div class="form-check">
                <input class="form-check-input" type="checkbox" id="role-admin" name="project_admin" value="1">
                <label class="form-check-label" for="role-admin"><?= View::e($view->t('roles.project_admin_help')) ?></label>
            </div>
        </div>
    </div>
    <?php foreach ($arms as $arm): ?>
        <?php $num = (int) ($arm['arm_num'] ?? 0); ?>
        <fieldset class="border rounded p-2 mb-2">
            <legend class="float-none w-auto px-1 small mb-0"><?= View::e($view->t('roles.arm', ['arm' => (string) $num]) . (($arm['name'] ?? '') !== '' && ($arm['name'] ?? null) !== null ? ' — ' . $arm['name'] : '')) ?></legend>
            <div class="row g-2">
                <div class="col-md-6">
                    <label class="form-label mb-0 small" for="data-<?= $num ?>"><?= View::e($view->t('roles.data_level')) ?></label>
                    <select class="form-select form-select-sm" id="data-<?= $num ?>" name="data[<?= $num ?>]">
                        <?php foreach ($dataLevels as $level): ?>
                            <option value="<?= $level ?>"><?= View::e($level) ?></option>
                        <?php endforeach; ?>
                    </select>
                </div>
                <div class="col-md-6">
                    <label class="form-label mb-0 small" for="export-<?= $num ?>"><?= View::e($view->t('roles.export_level')) ?></label>
                    <select class="form-select form-select-sm" id="export-<?= $num ?>" name="export[<?= $num ?>]">
                        <?php foreach ($exportLevels as $level): ?>
                            <option value="<?= $level ?>"><?= View::e($level) ?></option>
                        <?php endforeach; ?>
                    </select>
                </div>
            </div>
        </fieldset>
    <?php endforeach; ?>
    <div><button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('roles.create_submit')) ?></button></div>
</form>
