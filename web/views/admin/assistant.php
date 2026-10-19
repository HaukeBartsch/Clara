<?php
/**
 * The Control Panel's Clara assistant section (§5.12, REQ-AI-001): the frame for a conversation
 * with an in-browser model. Everything interactive — engine loading, streaming, tool execution,
 * confirmations and result rendering — lives in `assistant.js`; this template renders the shell,
 * the mode switch (REQ-AI-006) and the two states the server already knows: whether write mode
 * is on, and whether model artifacts are installed at all (REQ-AI-012).
 */

use Clara\View;

$writeMode = !empty($writeMode);
$modelInstalled = !empty($modelInstalled);
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.assistant.title')) ?></h1>

<p class="text-body-secondary small"><?= View::e($view->t('admin.assistant.intro')) ?></p>

<?php if (!$modelInstalled): ?>
    <div class="alert alert-warning py-2" role="alert" data-clara-as-model-missing>
        <?= View::e($view->t('admin.assistant.model_missing')) ?>
    </div>
<?php endif; ?>

<div class="d-flex flex-wrap align-items-center gap-3 mb-3">
    <!-- The read-write switch (REQ-AI-006): a plain form POST, PRG like every other Control
         Panel mutation. The server refuses write tools while the flag is off regardless of
         what this control shows. -->
    <form method="post" action="/admin?action=assistant_mode" class="d-flex align-items-center gap-2 mb-0">
        <?= $view->csrfField() ?>
        <input type="hidden" name="mode" value="<?= $writeMode ? 'read' : 'write' ?>">
        <span class="badge <?= $writeMode ? 'text-bg-warning' : 'text-bg-secondary' ?>">
            <?= View::e($view->t($writeMode ? 'admin.assistant.mode.badge_write' : 'admin.assistant.mode.badge_read')) ?>
        </span>
        <button class="btn btn-sm <?= $writeMode ? 'btn-outline-secondary' : 'btn-outline-warning' ?>" type="submit">
            <?= View::e($view->t($writeMode ? 'admin.assistant.mode.turn_off' : 'admin.assistant.mode.turn_on')) ?>
        </button>
    </form>

    <span class="small text-body-secondary" data-clara-as-status>
        <?= View::e($view->t('admin.assistant.status.idle')) ?>
    </span>
</div>

<!-- The chat log: bubbles are created by assistant.js with createElement/textContent only
     (REQ-UI-004). aria-live announces streamed answers to assistive technology. -->
<div class="border rounded p-2 mb-2 clara-as-log" data-clara-as-log aria-live="polite">
</div>

<form data-clara-as-form class="d-flex gap-2">
    <textarea class="form-control" rows="2" data-clara-as-input
              placeholder="<?= View::e($view->t('admin.assistant.placeholder')) ?>"
              aria-label="<?= View::e($view->t('admin.assistant.placeholder')) ?>"
              aria-describedby="clara-as-input-hint"></textarea>
    <button class="btn btn-primary align-self-end" type="submit" data-clara-as-send>
        <?= View::e($view->t('admin.assistant.send')) ?>
    </button>
    <button class="btn btn-outline-secondary align-self-end" type="button" data-clara-as-clear>
        <?= View::e($view->t('admin.assistant.clear')) ?>
    </button>
</form>
<!-- Enter and Shift+Enter are handled in assistant.js; the rule is stated next to the field it
     describes, since a textarea that sends on Enter needs to say so before the first message. -->
<div class="form-text" id="clara-as-input-hint"><?= View::e($view->t('admin.assistant.hint')) ?></div>

<!-- The write confirmation (REQ-AI-007): the exact tool arguments are shown before anything is
     sent; dismissing it performs nothing and answers the model that the change was declined. -->
<div class="modal fade" id="clara-as-confirm" tabindex="-1" aria-labelledby="clara-as-confirm-title" aria-hidden="true">
    <div class="modal-dialog modal-lg modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h6" id="clara-as-confirm-title"><?= View::e($view->t('admin.assistant.confirm.title')) ?></h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="<?= View::e($view->t('action.cancel')) ?>"></button>
            </div>
            <div class="modal-body">
                <p class="small text-body-secondary mb-2" data-clara-as-confirm-intro></p>
                <pre class="small bg-body-tertiary border rounded p-2 mb-0" data-clara-as-confirm-payload></pre>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal"><?= View::e($view->t('action.cancel')) ?></button>
                <button type="button" class="btn btn-warning" data-clara-as-confirm-ok><?= View::e($view->t('admin.assistant.confirm.ok')) ?></button>
            </div>
        </div>
    </div>
</div>
