<?php
/**
 * Account page — Project tokens (§2.8, REQ-UI-063): one row per project the user is a member
 * of (REQ-API-161), each with that membership's data-API token **masked** — an empty
 * read-only password input, so the page does not hold the value — and a Show button. Show is
 * a form POST; the API records the view (`token_viewed`, REQ-AUD-033) before it answers the
 * value (REQ-API-162), and this render is the only place it appears: read-only text, Copy,
 * and Hide, which is a plain link back to the masked page. Nothing is kept for a later render.
 */

use Clara\View;

/** @var list<array<string, mixed>> $memberships */
/** @var array{project_id: int, token: string}|null $shown */
?>
<h1 class="h4 mb-3"><?= View::e($view->t('account.section.tokens')) ?></h1>
<p class="text-body-secondary small"><?= View::e($view->t('account.tokens.intro')) ?></p>

<div class="table-responsive">
    <table class="table table-sm align-middle clara-account-tokens">
        <thead>
        <tr>
            <th scope="col"><?= View::e($view->t('account.tokens.project')) ?></th>
            <th scope="col"><?= View::e($view->t('members.role')) ?></th>
            <th scope="col" class="w-50"><?= View::e($view->t('members.token')) ?></th>
        </tr>
        </thead>
        <tbody>
        <?php if ($memberships === []): ?>
            <tr><td colspan="3" class="clara-region-empty"><?= View::e($view->t('account.tokens.empty')) ?></td></tr>
        <?php endif; ?>
        <?php foreach ($memberships as $membership): ?>
            <?php
            $projectId = (int) ($membership['project_id'] ?? 0);
            $visible = $shown !== null && $shown['project_id'] === $projectId;
            $inputId = 'clara-token-' . $projectId;
            $role = $membership['role'] ?? null;
            ?>
            <tr data-project-id="<?= $projectId ?>">
                <td><?= View::e($membership['project_name'] ?? '') ?></td>
                <td><?= View::e(is_string($role) ? $role : $view->t('members.no_role')) ?></td>
                <td>
                    <?php if ($visible): ?>
                        <div class="input-group input-group-sm">
                            <input class="form-control font-monospace" type="text" readonly id="<?= $inputId ?>"
                                   value="<?= View::e($shown['token']) ?>" aria-label="<?= View::e($view->t('members.token')) ?>">
                            <a class="btn btn-outline-secondary" href="/account/tokens" data-clara-token-hide><?= View::e($view->t('members.hide')) ?></a>
                            <button class="btn btn-outline-secondary" type="button" data-clara-copy="#<?= $inputId ?>"><?= View::e($view->t('members.copy')) ?></button>
                        </div>
                    <?php else: ?>
                        <?php // Masked: the value is not in the page at all until Show has been recorded. ?>
                        <form method="post" action="/account/tokens?action=show_token" class="input-group input-group-sm">
                            <?= $view->csrfField() ?>
                            <input type="hidden" name="project_id" value="<?= $projectId ?>">
                            <input class="form-control font-monospace" type="password" readonly id="<?= $inputId ?>"
                                   value="" placeholder="<?= str_repeat("\u{2022}", 36) ?>"
                                   aria-label="<?= View::e($view->t('members.token_masked')) ?>">
                            <button class="btn btn-outline-secondary" type="submit" data-clara-token-show><?= View::e($view->t('members.show')) ?></button>
                        </form>
                    <?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
<p class="small text-body-secondary"><?= View::e($view->t('account.tokens.audited')) ?></p>
