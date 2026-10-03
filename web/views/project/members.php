<?php
/**
 * Project page — Members and tokens (§5.3, is_admin, REQ-UI-013). One row per member (role or
 * "no role — full permissions" per REQ-AUTH-022, whether a token exists, account state), an
 * add form over the enabled accounts that are not members yet, and per-row role change, token
 * rotation and removal. A token value never appears in the table (REQ-API-005): a just-issued
 * one (add, rotate) is shown exactly once above it, read-only, with copy and warning (§3.5).
 *
 * Per-member data-access-group assignment (§5.3 last row) is not on this page yet: no API read
 * returns a member's current groups, and a form that cannot show them would overwrite blindly.
 */

use Clara\View;

/** @var list<array<string, mixed>> $members */
/** @var list<array<string, mixed>> $roles */
/** @var list<array<string, mixed>> $candidates */
/** @var array{token: string, email: string}|null $reveal */
$base = '/projects/' . (int) $projectId . '/members';
$roleOptions = static function (?string $current) use ($roles, $view): string {
    $html = '<option value=""' . ($current === null ? ' selected' : '') . '>' . View::e($view->t('members.no_role')) . '</option>';
    foreach ($roles as $role) {
        $name = (string) ($role['name'] ?? '');
        $html .= '<option value="' . View::e($name) . '"' . ($current === $name ? ' selected' : '') . '>' . View::e($name) . '</option>';
    }

    return $html;
};
?>
<h1 class="h4 mb-3"><?= View::e($view->t('members.title')) ?></h1>

<?php if (!empty($reveal)): ?>
    <div class="alert alert-warning clara-token-reveal" role="status">
        <div class="fw-semibold mb-1"><?= View::e($view->t('members.token_for', ['email' => $reveal['email']])) ?></div>
        <div class="input-group input-group-sm mb-1">
            <input class="form-control font-monospace" type="text" readonly id="clara-new-token"
                   value="<?= View::e($reveal['token']) ?>" aria-label="<?= View::e($view->t('members.token')) ?>">
            <button class="btn btn-outline-secondary" type="button" data-clara-copy="#clara-new-token"><?= View::e($view->t('members.copy')) ?></button>
        </div>
        <div class="small"><?= View::e($view->t('members.token_once')) ?></div>
    </div>
<?php endif; ?>

<form method="post" action="<?= $base ?>?action=add_member" class="card card-body mb-3">
    <?= $view->csrfField() ?>
    <div class="row g-2 align-items-end">
        <div class="col-md-6">
            <label class="form-label mb-0" for="member-user"><?= View::e($view->t('members.account')) ?></label>
            <select class="form-select form-select-sm" id="member-user" name="user_id" required>
                <option value=""></option>
                <?php foreach ($candidates as $account): ?>
                    <option value="<?= (int) $account['id'] ?>"><?= View::e(($account['email'] ?? '') . ' — ' . ($account['display_name'] ?? '')) ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div class="col-md-4">
            <label class="form-label mb-0" for="member-role"><?= View::e($view->t('members.role')) ?></label>
            <select class="form-select form-select-sm" id="member-role" name="role"><?= $roleOptions(null) ?></select>
        </div>
        <div class="col-md-2">
            <button class="btn btn-sm btn-primary w-100" type="submit"<?= $candidates === [] ? ' disabled' : '' ?>><?= View::e($view->t('members.add')) ?></button>
        </div>
    </div>
    <div class="form-text"><?= View::e($view->t('members.add_help')) ?></div>
</form>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-members">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('admin.users.email')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.display_name')) ?></th>
            <th scope="col"><?= View::e($view->t('members.role')) ?></th>
            <th scope="col"><?= View::e($view->t('members.token')) ?></th>
            <th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($members === []): ?>
            <tr><td colspan="5" class="clara-region-empty"><?= View::e($view->t('members.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($members as $member): ?>
            <?php
            $uid = (int) ($member['user_id'] ?? 0);
            $email = (string) ($member['email'] ?? '');
            $role = isset($member['role']) ? (string) $member['role'] : null;
            ?>
            <tr data-user-id="<?= $uid ?>">
                <td><?= View::e($email) ?>
                    <?php if (empty($member['enabled'])): ?>
                        <span class="badge text-bg-secondary ms-1"><?= View::e($view->t('admin.users.status.disabled')) ?></span>
                    <?php endif; ?>
                </td>
                <td><?= View::e($member['display_name'] ?? '') ?></td>
                <td>
                    <form method="post" action="<?= $base ?>?action=change_role" class="d-flex gap-1">
                        <?= $view->csrfField() ?>
                        <input type="hidden" name="user_id" value="<?= $uid ?>">
                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                        <label class="visually-hidden" for="role-<?= $uid ?>"><?= View::e($view->t('members.role')) ?></label>
                        <select class="form-select form-select-sm" id="role-<?= $uid ?>" name="role"><?= $roleOptions($role) ?></select>
                        <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('members.apply')) ?></button>
                    </form>
                </td>
                <td><span class="badge <?= !empty($member['token_present']) ? 'text-bg-success' : 'text-bg-light' ?>"><?= View::e($view->t(!empty($member['token_present']) ? 'members.token_present' : 'members.token_absent')) ?></span></td>
                <td class="text-end text-nowrap">
                    <form method="post" action="<?= $base ?>?action=rotate_token" class="d-inline"
                          data-clara-confirm="<?= View::e($view->t('members.confirm_rotate', ['email' => $email])) ?>">
                        <?= $view->csrfField() ?>
                        <input type="hidden" name="user_id" value="<?= $uid ?>">
                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                        <button class="btn btn-sm btn-outline-warning" type="submit"><?= View::e($view->t('members.rotate')) ?></button>
                    </form>
                    <form method="post" action="<?= $base ?>?action=remove_member" class="d-inline"
                          data-clara-confirm="<?= View::e($view->t('members.confirm_remove', ['email' => $email])) ?>">
                        <?= $view->csrfField() ?>
                        <input type="hidden" name="user_id" value="<?= $uid ?>">
                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                        <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('members.remove')) ?></button>
                    </form>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
