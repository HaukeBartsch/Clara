<?php
/**
 * Control Panel — Users (§5.1, REQ-UI-011). One row per account with the columns §5.1 fixes
 * (badges for enabled / administrator / status / two-factor method), a create form that
 * doubles as re-enable (REQ-API-047), and per-row actions in a disclosure so the table stays
 * condensed (REQ-UI-032). Destructive or hard-to-reverse actions carry `data-clara-confirm`:
 * assets/js/admin.js opens the §3.5 confirmation before the form is sent.
 *
 * A password never comes back into this page: the create form keeps email, name and validity
 * after a refusal, never the password (REQ-AUTH-036).
 */

use Clara\View;

/** @var list<array<string, mixed>> $users */
/** @var array<string, string> $form */
$form = $form ?? [];
$statusClass = [
    'active' => 'text-bg-success',
    'disabled' => 'text-bg-secondary',
    'expired' => 'text-bg-warning',
    'auto_disabled' => 'text-bg-danger',
];
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.users.title')) ?></h1>

<?php if (($error ?? '') !== ''): ?>
    <div class="alert alert-danger py-2" role="alert"><?= View::e($error) ?></div>
<?php endif; ?>

<details class="mb-3 clara-create"<?= ($error ?? '') !== '' ? ' open' : '' ?>>
    <summary class="btn btn-sm btn-primary"><?= View::e($view->t('admin.users.create')) ?></summary>
    <form method="post" action="/admin?section=users&amp;action=create_user" class="card card-body mt-2"
          autocomplete="off" data-clara-create-user>
        <?= $view->csrfField() ?>
        <div class="row g-2">
            <div class="col-md-6">
                <label class="form-label" for="user-email"><?= View::e($view->t('admin.users.email')) ?></label>
                <input class="form-control form-control-sm" type="email" id="user-email" name="email" required
                       value="<?= View::e($form['email'] ?? '') ?>">
            </div>
            <div class="col-md-6">
                <label class="form-label" for="user-name"><?= View::e($view->t('admin.users.display_name')) ?></label>
                <input class="form-control form-control-sm" type="text" id="user-name" name="display_name" required
                       value="<?= View::e($form['display_name'] ?? '') ?>">
            </div>
            <div class="col-md-4">
                <label class="form-label" for="user-valid"><?= View::e($view->t('admin.users.valid_days')) ?></label>
                <input class="form-control form-control-sm" type="number" min="0" step="1" id="user-valid" name="valid_days"
                       value="<?= View::e($form['valid_days'] ?? '0') ?>">
                <div class="form-text"><?= View::e($view->t('admin.users.valid_days_help')) ?></div>
            </div>
            <div class="col-md-4">
                <label class="form-label" for="user-password"><?= View::e($view->t('admin.users.password')) ?></label>
                <input class="form-control form-control-sm" type="password" id="user-password" name="password"
                       autocomplete="new-password">
                <div class="form-text"><?= View::e($view->t('admin.users.password_help')) ?></div>
            </div>
            <div class="col-md-4">
                <label class="form-label" for="user-repeat"><?= View::e($view->t('admin.users.repeat_password')) ?></label>
                <input class="form-control form-control-sm" type="password" id="user-repeat" name="repeat_password"
                       autocomplete="new-password">
            </div>
        </div>
        <p class="small text-body-secondary mt-2 mb-2"><?= View::e($view->t('admin.users.create_help')) ?></p>
        <div><button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('admin.users.create_submit')) ?></button></div>
    </form>
</details>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-users">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('admin.users.email')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.display_name')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.status')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.source')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.last_login')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.valid_until')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.tfa')) ?></th>
            <th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($users === []): ?>
            <tr><td colspan="8" class="clara-region-empty"><?= View::e($view->t('admin.users.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($users as $user): ?>
            <?php
            $id = (int) ($user['id'] ?? 0);
            $email = (string) ($user['email'] ?? '');
            $status = (string) ($user['status'] ?? '');
            $enabled = !empty($user['enabled']);
            $isAdmin = !empty($user['is_admin']);
            $tfa = (string) ($user['tfa_method'] ?? 'off');
            $local = ($user['auth_source'] ?? '') === 'local';
            ?>
            <tr class="<?= $status === 'auto_disabled' ? 'table-danger' : '' ?>" data-user-id="<?= $id ?>">
                <td><?= View::e($email) ?>
                    <?php if ($isAdmin): ?>
                        <span class="badge text-bg-primary ms-1"><?= View::e($view->t('admin.users.admin_badge')) ?></span>
                    <?php endif; ?>
                </td>
                <td><?= View::e($user['display_name'] ?? '') ?></td>
                <td>
                    <span class="badge <?= $statusClass[$status] ?? 'text-bg-light' ?>"><?= View::e(isset($statusClass[$status]) ? $view->t('admin.users.status.' . $status) : $status) ?></span>
                    <?php if ($status === 'auto_disabled'): ?>
                        <div class="small text-danger-emphasis"><?= View::e($view->t('admin.users.auto_disabled_reason')) ?></div>
                    <?php endif; ?>
                </td>
                <td><?= View::e($user['auth_source'] ?? '') ?></td>
                <td class="text-nowrap"><?= View::e(($user['last_login_at'] ?? null) === null ? $view->t('admin.users.never') : (string) $user['last_login_at']) ?></td>
                <td class="text-nowrap"><?= View::e(($user['valid_until'] ?? null) === null ? $view->t('admin.users.indefinite') : (string) $user['valid_until']) ?></td>
                <td><span class="badge <?= $tfa === 'off' ? 'text-bg-light' : 'text-bg-info' ?>"><?= View::e($tfa) ?></span></td>
                <td class="text-end">
                    <details class="clara-row-actions">
                        <summary class="btn btn-sm btn-outline-secondary"><?= View::e($view->t('admin.users.manage')) ?></summary>
                        <div class="card card-body mt-1 text-start small">
                            <?php // enable / disable (§5.1): disabling confirms, naming the token consequence ?>
                            <form method="post" action="/admin?section=users&amp;action=user_enabled" class="mb-2"
                                  <?= $enabled ? 'data-clara-confirm="' . View::e($view->t('admin.users.confirm_disable', ['email' => $email])) . '"' : '' ?>>
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>">
                                <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                <input type="hidden" name="enabled" value="<?= $enabled ? '0' : '1' ?>">
                                <button class="btn btn-sm <?= $enabled ? 'btn-outline-danger' : 'btn-outline-success' ?>" type="submit">
                                    <?= View::e($view->t($enabled ? 'admin.users.disable' : 'admin.users.enable')) ?>
                                </button>
                            </form>

                            <form method="post" action="/admin?section=users&amp;action=user_validity" class="mb-2 d-flex gap-1 align-items-end">
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>">
                                <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                <div>
                                    <label class="form-label mb-0" for="valid-<?= $id ?>"><?= View::e($view->t('admin.users.valid_days')) ?></label>
                                    <input class="form-control form-control-sm" type="number" min="0" step="1" id="valid-<?= $id ?>" name="valid_days" value="0">
                                </div>
                                <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('admin.users.set_validity')) ?></button>
                            </form>

                            <form method="post" action="/admin?section=users&amp;action=user_password" class="mb-2" autocomplete="off">
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>">
                                <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                <div class="d-flex gap-1 align-items-end">
                                    <div>
                                        <label class="form-label mb-0" for="pw-<?= $id ?>"><?= View::e($view->t('admin.users.password')) ?></label>
                                        <input class="form-control form-control-sm" type="password" id="pw-<?= $id ?>" name="password" autocomplete="new-password">
                                    </div>
                                    <div>
                                        <label class="form-label mb-0" for="pw2-<?= $id ?>"><?= View::e($view->t('admin.users.repeat_password')) ?></label>
                                        <input class="form-control form-control-sm" type="password" id="pw2-<?= $id ?>" name="repeat_password" autocomplete="new-password">
                                    </div>
                                    <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('admin.users.set_password')) ?></button>
                                </div>
                                <div class="form-text"><?= View::e($view->t('admin.users.set_password_help')) ?></div>
                            </form>

                            <div class="d-flex flex-wrap gap-1">
                                <?php if ($local): ?>
                                    <form method="post" action="/admin?section=users&amp;action=user_invite"
                                          data-clara-confirm="<?= View::e($view->t('admin.users.confirm_invite', ['email' => $email])) ?>">
                                        <?= $view->csrfField() ?>
                                        <input type="hidden" name="id" value="<?= $id ?>">
                                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                        <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('admin.users.invite')) ?></button>
                                    </form>
                                <?php endif; ?>
                                <?php if ($tfa !== 'off'): ?>
                                    <form method="post" action="/admin?section=users&amp;action=user_tfa_reset"
                                          data-clara-confirm="<?= View::e($view->t('admin.users.confirm_tfa_reset', ['email' => $email])) ?>">
                                        <?= $view->csrfField() ?>
                                        <input type="hidden" name="id" value="<?= $id ?>">
                                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                        <button class="btn btn-sm btn-outline-warning" type="submit"><?= View::e($view->t('admin.users.tfa_reset_action')) ?></button>
                                    </form>
                                <?php endif; ?>
                                <?php // The control stays on the last administrator too: the 409 then explains (REQ-AUTH-068). ?>
                                <form method="post" action="/admin?section=users&amp;action=user_admin"
                                      data-clara-confirm="<?= View::e($view->t($isAdmin ? 'admin.users.confirm_revoke_admin' : 'admin.users.confirm_grant_admin', ['email' => $email])) ?>">
                                    <?= $view->csrfField() ?>
                                    <input type="hidden" name="id" value="<?= $id ?>">
                                    <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                    <input type="hidden" name="is_admin" value="<?= $isAdmin ? '0' : '1' ?>">
                                    <button class="btn btn-sm <?= $isAdmin ? 'btn-outline-danger' : 'btn-outline-primary' ?>" type="submit">
                                        <?= View::e($view->t($isAdmin ? 'admin.users.revoke_admin' : 'admin.users.grant_admin')) ?>
                                    </button>
                                </form>
                            </div>
                        </div>
                    </details>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
