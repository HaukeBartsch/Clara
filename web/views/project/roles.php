<?php
/**
 * Project page — Role editor (§5.4, is_admin, REQ-UI-014). One role at a time is on the form:
 * the list above links each role into it in edit mode (`PUT …/roles/{rid}`, REQ-API-143), and an
 * empty name creates (REQ-API-057). The body of the screen is the permission matrix — one row per
 * mapped (instrument, event) pair of the arm, the arms behind tabs — with the project-scoped
 * `project_admin` checkbox below it (§4.7: a right of the project, never of a pair).
 */

use Clara\View;

/** @var list<array<string, mixed>> $roles */
/** @var list<array<string, mixed>> $arms */
/** @var array<int, list<array{event: string, instrument: string}>> $pairs */
/** @var array<string, mixed>|null $editing */
/** @var list<string> $dataLevels */
/** @var list<string> $exportLevels */
$base = '/projects/' . (int) $projectId . '/roles';
$editingId = (int) ($editing['id'] ?? 0);

// A pair shows its own grant, or the arm default it inherits when the role says nothing about
// it — which is exactly what the API's role object leaves out (REQ-AUTH-069).
$armDefaults = is_array($editing['arms'] ?? null) ? $editing['arms'] : [];
$grantOf = [];
foreach ((is_array($editing['grants'] ?? null) ? $editing['grants'] : []) as $grant) {
    if (is_array($grant)) {
        $grantOf[(string) ($grant['event'] ?? '') . '|' . (string) ($grant['instrument'] ?? '')] = $grant;
    }
}
$defaultOf = static function (int $armNum) use ($armDefaults): array {
    $arm = is_array($armDefaults[(string) $armNum] ?? null) ? $armDefaults[(string) $armNum] : [];

    return ['data' => (string) ($arm['data'] ?? 'no_access'), 'export' => (string) ($arm['export'] ?? 'export_none')];
};
$multiArm = count($arms) > 1;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('roles.title')) ?></h1>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-roles">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('roles.name')) ?></th>
            <th scope="col"><?= View::e($view->t('roles.project_admin')) ?></th>
            <th scope="col"><?= View::e($view->t('roles.summary')) ?></th>
            <th scope="col"><span class="visually-hidden"><?= View::e($view->t('roles.edit')) ?></span></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($roles === []): ?>
            <tr><td colspan="4" class="clara-region-empty"><?= View::e($view->t('roles.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($roles as $role): ?>
            <?php $id = (int) ($role['id'] ?? 0); ?>
            <tr<?= $id === $editingId ? ' class="table-active"' : '' ?>>
                <td><?= View::e($role['name'] ?? '') ?></td>
                <td><?= !empty($role['project_admin'])
                    ? '<span class="badge text-bg-primary">' . View::e($view->t('roles.yes')) . '</span>' : '' ?></td>
                <td class="small">
                    <?php foreach ((is_array($role['arms'] ?? null) ? $role['arms'] : []) as $armNum => $levels): ?>
                        <div><?= View::e($view->t('roles.arm', ['arm' => (string) $armNum])) ?>:
                            <code><?= View::e($levels['data'] ?? '') ?></code> · <code><?= View::e($levels['export'] ?? '') ?></code></div>
                    <?php endforeach; ?>
                    <?php $granted = count(is_array($role['grants'] ?? null) ? $role['grants'] : []); ?>
                    <?php if ($granted > 0): ?>
                        <div class="text-body-secondary"><?= View::e($view->t('roles.grants_count', ['count' => (string) $granted])) ?></div>
                    <?php endif; ?>
                </td>
                <td><a class="btn btn-sm btn-outline-primary" href="<?= View::e($base . '?role=' . $id) ?>"><?= View::e($view->t('roles.edit')) ?></a></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<form method="post" action="<?= View::e($base . '?action=save_role') ?>" class="card card-body" data-clara-roles-form>
    <?= $view->csrfField() ?>
    <?php if ($editingId > 0): ?>
        <input type="hidden" name="role_id" value="<?= $editingId ?>">
    <?php endif; ?>

    <div class="row g-2 align-items-end mb-3">
        <div class="col-md-6">
            <label class="form-label mb-0" for="role-name"><?= View::e($view->t('roles.name')) ?></label>
            <input class="form-control form-control-sm" type="text" id="role-name" name="name" required maxlength="255"
                   value="<?= View::e((string) ($editing['name'] ?? '')) ?>">
            <div class="form-text"><?= View::e($view->t('roles.name_help')) ?></div>
        </div>
    </div>

    <h4 class="h6 mb-2"><?= View::e($view->t('roles.matrix.title')) ?></h4>
    <?php if ($pairs === []): ?>
        <!-- A project without events has no pair to grant (DEV-DB-14): the arm defaults below
             decide everything, and no survey link can be issued yet. -->
        <p class="text-body-secondary small"><?= View::e($view->t('roles.matrix.no_pairs')) ?></p>
    <?php endif; ?>

    <ul class="nav nav-tabs mb-2<?= $multiArm ? '' : ' d-none' ?>" role="tablist">
        <?php foreach ($arms as $index => $arm): ?>
            <?php $armNum = (int) ($arm['arm_num'] ?? 0); ?>
            <li class="nav-item" role="presentation">
                <button class="nav-link<?= $index === 0 ? ' active' : '' ?>" type="button" role="tab"
                        id="role-arm-<?= $armNum ?>-tab" data-bs-toggle="tab" data-clara-role-tab
                        data-bs-target="#role-arm-<?= $armNum ?>" aria-controls="role-arm-<?= $armNum ?>"
                        aria-selected="<?= $index === 0 ? 'true' : 'false' ?>">
                    <?= View::e($view->t('roles.arm', ['arm' => (string) $armNum])) ?>
                    <span class="badge text-bg-warning d-none clara-dirty"><?= View::e($view->t('form.unsaved')) ?></span>
                </button>
            </li>
        <?php endforeach; ?>
    </ul>

    <div class="tab-content">
        <?php foreach ($arms as $index => $arm): ?>
            <?php
            $armNum = (int) ($arm['arm_num'] ?? 0);
            $default = $defaultOf($armNum);
            $rows = $pairs[$armNum] ?? [];
            ?>
            <div class="tab-pane fade<?= $index === 0 ? ' show active' : '' ?>" id="role-arm-<?= $armNum ?>"
                 role="tabpanel" aria-labelledby="role-arm-<?= $armNum ?>-tab">

                <!-- The arm default its rows inherit unless a row says otherwise (REQ-AUTH-069). -->
                <div class="row g-2 align-items-end mb-2">
                    <div class="col-auto">
                        <label class="form-label mb-0 small" for="arm-data-<?= $armNum ?>"><?= View::e($view->t('roles.arm_default')) ?></label>
                    </div>
                    <div class="col-6 col-md-3">
                        <select class="form-select form-select-sm" id="arm-data-<?= $armNum ?>" name="arm_data[<?= $armNum ?>]">
                            <?php foreach ($dataLevels as $level): ?>
                                <option value="<?= $level ?>"<?= $level === $default['data'] ? ' selected' : '' ?>><?= View::e($view->t('roles.level.' . $level)) ?></option>
                            <?php endforeach; ?>
                        </select>
                    </div>
                    <div class="col-6 col-md-3">
                        <select class="form-select form-select-sm" id="arm-export-<?= $armNum ?>" name="arm_export[<?= $armNum ?>]">
                            <?php foreach ($exportLevels as $level): ?>
                                <option value="<?= $level ?>"<?= $level === $default['export'] ? ' selected' : '' ?>><?= View::e($view->t('roles.level.' . $level)) ?></option>
                            <?php endforeach; ?>
                        </select>
                    </div>
                    <div class="col form-text"><?= View::e($view->t('roles.arm_default_help')) ?></div>
                </div>

                <div class="table-responsive clara-matrix-scroll">
                    <table class="table table-sm align-middle clara-matrix clara-role-matrix" data-clara-role-arm="<?= $armNum ?>">
                        <thead>
                        <tr>
                            <th scope="col" rowspan="2"><?= View::e($view->t('roles.matrix.pair')) ?></th>
                            <th scope="colgroup" colspan="<?= count($dataLevels) ?>" class="text-center"><?= View::e($view->t('roles.data_level')) ?></th>
                            <th scope="col" rowspan="2" class="text-center"><span class="visually-hidden"><?= View::e($view->t('roles.delete_values')) ?></span></th>
                            <th scope="col" rowspan="2" class="text-center"><span class="visually-hidden"><?= View::e($view->t('roles.edit_surveys')) ?></span></th>
                            <th scope="colgroup" colspan="<?= count($exportLevels) ?>" class="text-center"><?= View::e($view->t('roles.export_level')) ?></th>
                        </tr>
                        <tr>
                            <?php foreach ($dataLevels as $level): ?>
                                <th scope="col" class="text-center fw-normal">
                                    <?= View::e($view->t('roles.level.' . $level)) ?>
                                    <button class="btn btn-link btn-sm p-0 ms-1 clara-bulk-set" type="button"
                                            data-column="data" data-value="<?= $level ?>"
                                            aria-label="<?= View::e($view->t('roles.bulk', ['value' => $view->t('roles.level.' . $level)])) ?>">⇓</button>
                                </th>
                            <?php endforeach; ?>
                            <th scope="col" class="text-center fw-normal">
                                <button class="btn btn-link btn-sm p-0 clara-bulk-set" type="button" data-column="delete_values" data-value="1"
                                        aria-label="<?= View::e($view->t('roles.bulk_all')) ?>">⇓</button>
                            </th>
                            <th scope="col" class="text-center fw-normal">
                                <button class="btn btn-link btn-sm p-0 clara-bulk-set" type="button" data-column="edit_surveys" data-value="1"
                                        aria-label="<?= View::e($view->t('roles.bulk_all')) ?>">⇓</button>
                            </th>
                            <?php foreach ($exportLevels as $level): ?>
                                <th scope="col" class="text-center fw-normal">
                                    <?= View::e($view->t('roles.level.' . $level)) ?>
                                    <button class="btn btn-link btn-sm p-0 ms-1 clara-bulk-set" type="button"
                                            data-column="export" data-value="<?= $level ?>"
                                            aria-label="<?= View::e($view->t('roles.bulk', ['value' => $view->t('roles.level.' . $level)])) ?>">⇓</button>
                                </th>
                            <?php endforeach; ?>
                        </tr>
                        </thead>
                        <tbody>
                        <?php if ($rows === []): ?>
                            <tr><td colspan="<?= 3 + count($dataLevels) + count($exportLevels) ?>" class="clara-region-empty">
                                <?= View::e($view->t('roles.matrix.empty_arm')) ?></td></tr>
                        <?php endif; ?>
                        <?php foreach ($rows as $row): ?>
                            <?php
                            // One key names the pair in every column of the row: arm, instrument, event.
                            $key = $armNum . '|' . $row['instrument'] . '|' . $row['event'];
                            $grant = $grantOf[$row['event'] . '|' . $row['instrument']] ?? null;
                            $dataValue = (string) ($grant['data'] ?? $default['data']);
                            $exportValue = (string) ($grant['export'] ?? $default['export']);
                            // Denial wins over a right on the pair it names (REQ-AUTH-070): the boxes are
                            // rendered disabled rather than silently ignored (REQ-UI-003).
                            $denied = $dataValue === 'no_access';
                            ?>
                            <tr data-clara-role-row>
                                <th scope="row" class="fw-normal">
                                    <?= View::e($row['instrument']) ?>
                                    <span class="text-body-secondary">(<?= View::e($row['event']) ?>)</span>
                                    <input type="hidden" name="pair[]" value="<?= View::e($key) ?>">
                                </th>
                                <?php foreach ($dataLevels as $level): ?>
                                    <td class="text-center">
                                        <input class="form-check-input m-0" type="radio"
                                               name="data[<?= View::e($key) ?>]" value="<?= $level ?>"
                                               data-clara-role-cell="data" data-key="<?= View::e($key) ?>"
                                               <?= $level === $dataValue ? 'checked' : '' ?>>
                                        <span class="visually-hidden"><?= View::e($view->t('roles.level.' . $level)) ?></span>
                                    </td>
                                <?php endforeach; ?>
                                <?php
                                $rights = [
                                    'delete_values' => !empty($grant['delete_values']),
                                    'edit_surveys' => !empty($grant['edit_surveys']),
                                ];
                                foreach ($rights as $right => $on): ?>
                                    <td class="text-center">
                                        <input class="form-check-input m-0" type="checkbox"
                                               name="<?= $right ?>[<?= View::e($key) ?>]" value="1"
                                               data-clara-role-cell="<?= $right ?>" data-key="<?= View::e($key) ?>"
                                               <?= $on ? 'checked' : '' ?><?= $denied ? ' disabled title="' . View::e($view->t('roles.right_denied')) . '"' : '' ?>>
                                    </td>
                                <?php endforeach; ?>
                                <?php foreach ($exportLevels as $level): ?>
                                    <td class="text-center">
                                        <input class="form-check-input m-0" type="radio"
                                               name="export[<?= View::e($key) ?>]" value="<?= $level ?>"
                                               data-clara-role-cell="export" data-key="<?= View::e($key) ?>"
                                               <?= $level === $exportValue ? 'checked' : '' ?>>
                                        <span class="visually-hidden"><?= View::e($view->t('roles.level.' . $level)) ?></span>
                                    </td>
                                <?php endforeach; ?>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
            </div>
        <?php endforeach; ?>
    </div>

    <h4 class="h6 mt-3 mb-2"><?= View::e($view->t('roles.project_admin')) ?></h4>
    <div class="form-check">
        <input class="form-check-input" type="checkbox" id="role-admin" name="project_admin" value="1"
               <?= $projectAdmin ? 'checked' : '' ?>>
        <label class="form-check-label" for="role-admin"><?= View::e($view->t('roles.project_admin_help')) ?></label>
    </div>

    <div class="d-flex gap-2 mt-3">
        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t($editingId > 0 ? 'roles.save_submit' : 'roles.create_submit')) ?></button>
        <?php if ($editingId > 0): ?>
            <a class="btn btn-sm btn-outline-secondary" href="<?= View::e($base) ?>"><?= View::e($view->t('roles.new_role')) ?></a>
        <?php endif; ?>
    </div>
</form>
