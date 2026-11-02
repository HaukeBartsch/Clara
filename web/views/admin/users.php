<?php
/**
 * Control Panel — Users (§5.1, REQ-UI-011). One row per account with the columns §5.1 fixes
 * (badges for enabled / administrator / status / two-factor method), a create form that
 * doubles as re-enable (REQ-API-047), and per-row actions in a disclosure so the table stays
 * condensed (REQ-UI-032). Destructive or hard-to-reverse actions carry `data-clara-confirm`:
 * assets/js/admin.js opens the §3.5 confirmation before the form is sent.
 *
 * The list is one page of a server-side search (REQ-API-186): a GET filter form (search by
 * address or name, sign-in source, "in no project", order) above the table, and Previous/Next
 * below it. Every row form carries the list's query as `back`, so an action returns to the
 * same filtered page.
 *
 * A password never comes back into this page: the create form keeps email, name and validity
 * after a refusal, never the password (REQ-AUTH-036).
 */

use Clara\View;

/** @var list<array<string, mixed>> $users */
/** @var array<string, string> $form */
/** @var array{q: string, source: string, projects: string, sort: string, page: int} $filters */
/** @var callable(int): string $pageUrl */
$form = $form ?? [];
$filtered = $filters['q'] !== '' || $filters['source'] !== '' || $filters['projects'] !== '';
$backField = '<input type="hidden" name="back" value="' . View::e($back) . '">';
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

<form method="get" action="/admin" class="row g-2 align-items-end mb-2 clara-user-filters" role="search">
    <input type="hidden" name="section" value="users">
    <div class="col-12 col-md-4">
        <label class="form-label mb-0 small" for="users-q"><?= View::e($view->t('admin.users.filter.q')) ?></label>
        <input class="form-control form-control-sm" type="search" id="users-q" name="q" maxlength="200"
               value="<?= View::e($filters['q']) ?>">
    </div>
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="users-source"><?= View::e($view->t('admin.users.filter.source')) ?></label>
        <select class="form-select form-select-sm" id="users-source" name="source">
            <option value=""><?= View::e($view->t('admin.users.filter.source_any')) ?></option>
            <?php foreach (['local', 'ldap', 'oauth2'] as $source): ?>
                <option value="<?= $source ?>"<?= $filters['source'] === $source ? ' selected' : '' ?>><?= $source ?></option>
            <?php endforeach; ?>
        </select>
    </div>
    <div class="col-6 col-md-2">
        <label class="form-label mb-0 small" for="users-sort"><?= View::e($view->t('admin.users.filter.sort')) ?></label>
        <select class="form-select form-select-sm" id="users-sort" name="sort">
            <option value=""><?= View::e($view->t('admin.users.filter.sort_email')) ?></option>
            <option value="created"<?= $filters['sort'] === 'created' ? ' selected' : '' ?>><?= View::e($view->t('admin.users.filter.sort_created')) ?></option>
        </select>
    </div>
    <div class="col-12 col-md-2">
        <div class="form-check mb-1">
            <input class="form-check-input" type="checkbox" id="users-no-projects" name="projects" value="none"<?= $filters['projects'] === 'none' ? ' checked' : '' ?>>
            <label class="form-check-label small" for="users-no-projects"><?= View::e($view->t('admin.users.filter.no_projects')) ?></label>
        </div>
    </div>
    <div class="col-12 col-md-2 d-flex gap-1">
        <button class="btn btn-sm btn-outline-primary" type="submit"><?= View::e($view->t('admin.users.filter.apply')) ?></button>
        <?php if ($filtered || $filters['sort'] !== ''): ?>
            <a class="btn btn-sm btn-outline-secondary" href="/admin?section=users"><?= View::e($view->t('admin.users.filter.clear')) ?></a>
        <?php endif; ?>
    </div>
</form>

<p class="small text-body-secondary mb-2" aria-live="polite">
    <?php if ($total > 0): ?>
        <?= View::e($view->t('admin.users.count', [
            'from' => (string) (($page - 1) * $perPage + 1),
            'to' => (string) min($total, $page * $perPage),
            'total' => (string) $total,
        ])) ?>
    <?php endif; ?>
</p>

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
            <th scope="col"><?= View::e($view->t('admin.users.region')) ?></th>
            <th scope="col" class="text-end"><?= View::e($view->t('admin.users.projects')) ?></th>
            <th scope="col"><?= View::e($view->t('admin.users.created_at')) ?></th>
            <th scope="col"><span class="visually-hidden"><?= View::e($view->t('admin.users.actions')) ?></span></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($users === []): ?>
            <tr><td colspan="11" class="clara-region-empty"><?= View::e($view->t($filtered ? 'admin.users.no_match' : 'admin.users.empty')) ?></td></tr>
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
            // The effective region and where it came from (REQ-UI-079, REQ-API-176).
            $region = is_string($user['region'] ?? null) ? $user['region'] : '';
            $regionSource = is_string($user['preferred_region'] ?? null) ? 'own'
                : (($user['automatic_region_source'] ?? '') === 'assignment' ? 'assignment' : 'provider');
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
                <td class="text-nowrap">
                    <?php if ($region === ''): ?>—<?php else: ?>
                        <?= View::e($region) ?>
                        <div class="text-body-secondary small"><?= View::e($view->t('admin.users.region.' . $regionSource)) ?></div>
                    <?php endif; ?>
                </td>
                <td class="text-end"><?= (int) ($user['project_count'] ?? 0) ?></td>
                <td class="text-nowrap"><?= View::e((string) ($user['created_at'] ?? '')) ?></td>
                <td class="text-end">
                    <details class="clara-row-actions">
                        <summary class="btn btn-sm btn-outline-secondary"><?= View::e($view->t('admin.users.manage')) ?></summary>
                        <div class="card card-body mt-1 text-start small">
                            <?php // enable / disable (§5.1): disabling confirms, naming the token consequence ?>
                            <form method="post" action="/admin?section=users&amp;action=user_enabled" class="mb-2"
                                  <?= $enabled ? 'data-clara-confirm="' . View::e($view->t('admin.users.confirm_disable', ['email' => $email])) . '"' : '' ?>>
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
                                <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                <input type="hidden" name="enabled" value="<?= $enabled ? '0' : '1' ?>">
                                <button class="btn btn-sm <?= $enabled ? 'btn-outline-danger' : 'btn-outline-success' ?>" type="submit">
                                    <?= View::e($view->t($enabled ? 'admin.users.disable' : 'admin.users.enable')) ?>
                                </button>
                            </form>

                            <form method="post" action="/admin?section=users&amp;action=user_validity" class="mb-2 d-flex gap-1 align-items-end">
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
                                <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                <div>
                                    <label class="form-label mb-0" for="valid-<?= $id ?>"><?= View::e($view->t('admin.users.valid_days')) ?></label>
                                    <input class="form-control form-control-sm" type="number" min="0" step="1" id="valid-<?= $id ?>" name="valid_days" value="0">
                                </div>
                                <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('admin.users.set_validity')) ?></button>
                            </form>

                            <form method="post" action="/admin?section=users&amp;action=user_password" class="mb-2" autocomplete="off">
                                <?= $view->csrfField() ?>
                                <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
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
                                        <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
                                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                        <button class="btn btn-sm btn-outline-secondary" type="submit"><?= View::e($view->t('admin.users.invite')) ?></button>
                                    </form>
                                <?php endif; ?>
                                <?php if ($tfa !== 'off'): ?>
                                    <form method="post" action="/admin?section=users&amp;action=user_tfa_reset"
                                          data-clara-confirm="<?= View::e($view->t('admin.users.confirm_tfa_reset', ['email' => $email])) ?>">
                                        <?= $view->csrfField() ?>
                                        <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
                                        <input type="hidden" name="email" value="<?= View::e($email) ?>">
                                        <button class="btn btn-sm btn-outline-warning" type="submit"><?= View::e($view->t('admin.users.tfa_reset_action')) ?></button>
                                    </form>
                                <?php endif; ?>
                                <?php // The control stays on the last administrator too: the 409 then explains (REQ-AUTH-068). ?>
                                <form method="post" action="/admin?section=users&amp;action=user_admin"
                                      data-clara-confirm="<?= View::e($view->t($isAdmin ? 'admin.users.confirm_revoke_admin' : 'admin.users.confirm_grant_admin', ['email' => $email])) ?>">
                                    <?= $view->csrfField() ?>
                                    <input type="hidden" name="id" value="<?= $id ?>"><?= $backField ?>
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

<?php if ($pages > 1): ?>
    <nav class="d-flex gap-2 align-items-center" aria-label="<?= View::e($view->t('admin.users.pages')) ?>">
        <?php if ($page > 1): ?>
            <a class="btn btn-sm btn-outline-secondary" rel="prev" href="<?= View::e($pageUrl($page - 1)) ?>"><?= View::e($view->t('admin.users.previous')) ?></a>
        <?php endif; ?>
        <span class="small text-body-secondary"><?= View::e($view->t('admin.users.page', ['page' => (string) $page, 'pages' => (string) $pages])) ?></span>
        <?php if ($page < $pages): ?>
            <a class="btn btn-sm btn-outline-secondary" rel="next" href="<?= View::e($pageUrl($page + 1)) ?>"><?= View::e($view->t('admin.users.next')) ?></a>
        <?php endif; ?>
    </nav>
<?php endif; ?>
