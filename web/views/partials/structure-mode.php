<?php
/**
 * The mode-dependent controls of every structure page — Setup (§6.2) and the Designer (§7).
 *
 *   production, no set open — Start staging, and the reason the edit controls are absent;
 *   production, set open    — the persistent banner with Commit staged changes (a dialog that
 *                             lists the staged diff, breaking changes first acknowledged) and
 *                             Discard (confirmed, naming how many changes go) — §6.7;
 *   analysis                — the question a breaking change raises, when the last save came
 *                             back with one: the change, its consequence, Delete anyway /
 *                             Cancel; confirming replays the identical request with the
 *                             acknowledgement (§6.8). Development shows nothing.
 *
 * Reads: $mode, $stagingOpen, $staging, $stagingBase, $breaking, $reopenCommit.
 */

use Clara\View;

$stagingAction = static fn (string $action): string => $stagingBase . '?action=' . $action;
?>
<?php if ($mode === 'production' && !$stagingOpen): ?>
    <div class="alert alert-secondary d-flex flex-wrap align-items-center gap-2 clara-staging-closed" role="status">
        <span class="me-auto"><?= View::e($view->t('staging.closed')) ?></span>
        <form method="post" action="<?= View::e($stagingAction('staging_start')) ?>">
            <?= $view->csrfField() ?>
            <button class="btn btn-sm btn-primary" type="submit"><?= View::e($view->t('staging.start')) ?></button>
        </form>
    </div>
<?php endif; ?>

<?php if ($mode === 'production' && $stagingOpen && $staging !== null): ?>
    <div class="alert alert-warning d-flex flex-wrap align-items-center gap-2 clara-staging-banner" role="status">
        <span class="me-auto"><?= View::e($view->t('staging.banner')) ?></span>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#clara-commit">
            <?= View::e($view->t('staging.commit')) ?></button>
        <form method="post" action="<?= View::e($stagingAction('staging_discard')) ?>"
              data-clara-confirm="<?= View::e($view->t('staging.confirm_discard', ['count' => (string) $staging['count']])) ?>">
            <?= $view->csrfField() ?>
            <button class="btn btn-sm btn-outline-danger" type="submit"><?= View::e($view->t('staging.discard')) ?></button>
        </form>
    </div>

    <!-- The commit dialog (§6.7): the staged diff in two groups; with breaking changes the
         commit needs the acknowledgement ticked — `required`, so the browser holds the submit
         until it is. A commit without breaking changes posts directly. -->
    <div class="modal fade" id="clara-commit" tabindex="-1" aria-labelledby="clara-commit-title" aria-hidden="true"
         <?= $reopenCommit ? 'data-clara-autoshow' : '' ?>>
        <div class="modal-dialog modal-dialog-scrollable modal-lg">
            <form class="modal-content" method="post" action="<?= View::e($stagingAction('staging_commit')) ?>">
                <?= $view->csrfField() ?>
                <div class="modal-header">
                    <h2 class="modal-title h6" id="clara-commit-title"><?= View::e($view->t('staging.commit_title')) ?></h2>
                    <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="<?= View::e($view->t('action.close')) ?>"></button>
                </div>
                <div class="modal-body">
                    <?php if ($staging['count'] === 0): ?>
                        <p class="mb-0 clara-region-empty"><?= View::e($view->t('staging.no_changes')) ?></p>
                    <?php endif; ?>
                    <?php if ($staging['breaking'] !== []): ?>
                        <h3 class="h6 text-danger"><?= View::e($view->t('staging.breaking')) ?></h3>
                        <ul class="small clara-staging-breaking">
                            <?php foreach ($staging['breaking'] as $change): ?>
                                <li><code><?= View::e($change['kind'] ?? '') ?></code> <?= View::e($change['object'] ?? '') ?>
                                    <?php if (($change['reason'] ?? '') !== ''): ?>— <?= View::e($change['reason']) ?><?php endif; ?></li>
                            <?php endforeach; ?>
                        </ul>
                    <?php endif; ?>
                    <?php if ($staging['nonBreaking'] !== []): ?>
                        <h3 class="h6"><?= View::e($view->t('staging.non_breaking')) ?></h3>
                        <ul class="small clara-staging-nonbreaking">
                            <?php foreach ($staging['nonBreaking'] as $change): ?>
                                <li><code><?= View::e($change['kind'] ?? '') ?></code> <?= View::e($change['object'] ?? '') ?></li>
                            <?php endforeach; ?>
                        </ul>
                    <?php endif; ?>
                    <?php if ($staging['breaking'] !== []): ?>
                        <div class="form-check">
                            <input class="form-check-input" type="checkbox" id="staging-ack" name="acknowledge_breaking" value="1" required>
                            <label class="form-check-label" for="staging-ack"><?= View::e($view->t('staging.acknowledge')) ?></label>
                        </div>
                    <?php endif; ?>
                </div>
                <div class="modal-footer">
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal"><?= View::e($view->t('action.cancel')) ?></button>
                    <button type="submit" class="btn btn-sm btn-primary"><?= View::e($view->t('staging.commit')) ?></button>
                </div>
            </form>
        </div>
    </div>
<?php endif; ?>

<?php if ($breaking !== null): ?>
    <!-- Analysis mode (§6.8, REQ-UI-037): the save came back as a breaking change. Confirming
         resubmits the identical request with the acknowledgement; cancelling leaves it unsent. -->
    <div class="modal fade" id="clara-breaking" tabindex="-1" aria-labelledby="clara-breaking-title" aria-hidden="true" data-clara-autoshow>
        <div class="modal-dialog">
            <form class="modal-content" method="post" action="<?= View::e($breaking['action'] ?? '') ?>">
                <?= $view->csrfField() ?>
                <?php foreach (($breaking['fields'] ?? []) as [$name, $value]): ?>
                    <input type="hidden" name="<?= View::e($name) ?>" value="<?= View::e($value) ?>">
                <?php endforeach; ?>
                <input type="hidden" name="acknowledge_breaking" value="1">
                <div class="modal-header">
                    <h2 class="modal-title h6" id="clara-breaking-title"><?= View::e($view->t('structure.breaking.title')) ?></h2>
                </div>
                <div class="modal-body">
                    <p class="clara-breaking-reason"><?= View::e($breaking['message'] ?? '') ?></p>
                    <p class="mb-0 small text-body-secondary"><?= View::e($view->t('structure.breaking.live')) ?></p>
                </div>
                <div class="modal-footer">
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal"><?= View::e($view->t('action.cancel')) ?></button>
                    <button type="submit" class="btn btn-sm btn-danger"><?= View::e($view->t('structure.breaking.confirm')) ?></button>
                </div>
            </form>
        </div>
    </div>
<?php endif; ?>
