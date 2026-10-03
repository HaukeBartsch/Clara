<?php
/**
 * Project page — Data access groups (§5.5, REQ-UI-015). Readable at data access ≥ read_only;
 * create and delete are offered only to a project_admin (or is_admin) — hidden means not
 * emitted (REQ-UI-003), and the API re-checks. Deleting confirms first, warning that the API
 * refuses while records are still assigned (REQ-API-088; the 409 reason is shown per §3.4).
 */

use Clara\View;

/** @var list<array<string, mixed>> $groups */
$base = '/projects/' . (int) $projectId . '/groups';
?>
<h1 class="h4 mb-3"><?= View::e($view->t('groups.title')) ?></h1>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-groups">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('groups.name')) ?></th>
            <?php if ($canManage): ?>
                <th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th>
            <?php endif; ?>
        </tr>
        </thead>
        <tbody>
        <?php if ($groups === []): ?>
            <tr><td colspan="2" class="clara-region-empty"><?= View::e($view->t('groups.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($groups as $group): ?>
            <tr>
                <td><?= View::e($group['name'] ?? '') ?></td>
                <?php if ($canManage): ?>
                    <td class="text-end">
                        <form method="post" action="<?= $base ?>?action=delete_group" class="d-inline"
                              data-clara-confirm="<?= View::e($view->t('groups.confirm_delete', ['name' => (string) ($group['name'] ?? '')])) ?>">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="group_id" value="<?= (int) ($group['id'] ?? 0) ?>">
                            <input type="hidden" name="name" value="<?= View::e($group['name'] ?? '') ?>">
                            <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('groups.delete')) ?></button>
                        </form>
                    </td>
                <?php endif; ?>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>

<?php if ($canManage): ?>
    <form method="post" action="<?= $base ?>?action=create_group" class="d-flex gap-2 align-items-end mt-3">
        <?= $view->csrfField() ?>
        <div>
            <label class="form-label mb-0" for="group-name"><?= View::e($view->t('groups.name')) ?></label>
            <input class="form-control form-control-sm" type="text" id="group-name" name="name" required maxlength="255">
        </div>
        <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('groups.create')) ?></button>
    </form>
<?php endif; ?>
