<?php
/**
 * One instrument's "Repeating" control on the Design page (§7.1, REQ-UI-054): a dropdown
 * whose button says where the instrument repeats, holding one switch per event of every arm.
 * Only an event the instrument is mapped to can be switched on (REQ-DB-047); the others are
 * listed disabled with the reason, so the designer sees where the mapping is missing. A
 * survey instrument and the first instrument (it holds the record identifier, GD-8) show the
 * reason instead of switches (REQ-API-154). The switches post the whole set at once —
 * design.js submits on every change and hides the Save button, which is there for a browser
 * without JavaScript. Where structure writes are closed (production without a staging set,
 * §6.7) the switches show the state and accept nothing.
 *
 * Reads: $instrument, $repeating (DesignController::repeatingChoices), $designUrl, $canEdit.
 */

use Clara\View;

$iid = (int) ($instrument['id'] ?? 0);
$name = (string) ($instrument['name'] ?? '');
$choice = $repeating[$iid] ?? ['locked' => 'unmapped', 'on' => [], 'arms' => []];
$labels = [];
foreach ($choice['arms'] as $arm) {
    foreach ($arm['events'] as $event) {
        if ($event['on']) {
            $labels[] = $event['event_name'];
        }
    }
}
// A staged instrument carries a provisional negative id (§6.7); the element ids stay valid.
$menuId = 'repeating-' . ($iid < 0 ? 'n' . -$iid : (string) $iid);
?>
<div class="dropdown clara-repeating" data-clara-repeating="<?= View::e($name) ?>">
    <button class="btn btn-sm <?= $labels === [] ? 'btn-outline-secondary' : 'btn-outline-primary' ?> dropdown-toggle"
            type="button" id="<?= $menuId ?>" data-bs-toggle="dropdown" data-bs-auto-close="outside"
            data-bs-popper-config='{"strategy":"fixed"}' aria-expanded="false">
        <?= View::e($labels === [] ? $view->t('design.repeating.off') : $view->t('design.repeating.on', ['events' => implode(', ', $labels)])) ?>
    </button>
    <div class="dropdown-menu p-2 clara-repeating-menu" aria-labelledby="<?= $menuId ?>">
        <?php if ($choice['locked'] !== ''): ?>
            <p class="small text-body-secondary mb-0"><?= View::e($view->t('design.repeating.locked.' . $choice['locked'])) ?></p>
        <?php else: ?>
            <form method="post" action="<?= View::e($designUrl) ?>?action=save_repeating" data-clara-repeating-form>
                <?= $view->csrfField() ?>
                <input type="hidden" name="iid" value="<?= $iid ?>">
                <input type="hidden" name="instrument" value="<?= View::e($name) ?>">
                <div class="small fw-semibold mb-1"><?= View::e($view->t('design.repeating.title', ['instrument' => $name])) ?></div>
                <?php foreach ($choice['arms'] as $arm): ?>
                    <?php if (count($choice['arms']) > 1): ?>
                        <div class="small text-body-secondary mt-1"><?= View::e($view->t('roles.arm', ['arm' => (string) $arm['arm_num']]) . ($arm['name'] !== '' ? ' — ' . $arm['name'] : '')) ?></div>
                    <?php endif; ?>
                    <?php foreach ($arm['events'] as $event): ?>
                        <?php $toggleId = $menuId . '-' . $event['unique_event_name']; ?>
                        <div class="form-check form-switch">
                            <input class="form-check-input" type="checkbox" role="switch" id="<?= View::e($toggleId) ?>"
                                   name="events[]" value="<?= View::e($event['unique_event_name']) ?>"
                                   <?= $event['on'] ? 'checked' : '' ?> <?= !$event['mapped'] || !$canEdit ? 'disabled' : '' ?>>
                            <label class="form-check-label small" for="<?= View::e($toggleId) ?>"><?= View::e($event['event_name']) ?>
                                <?php if (!$event['mapped']): ?><span class="text-body-secondary">— <?= View::e($view->t('design.repeating.unmapped')) ?></span><?php endif; ?></label>
                        </div>
                    <?php endforeach; ?>
                <?php endforeach; ?>
                <p class="small text-body-secondary mt-2 mb-1"><?= View::e($view->t('design.repeating.hint')) ?></p>
                <?php if ($canEdit): ?>
                    <button class="btn btn-sm btn-primary" type="submit" data-clara-repeating-save><?= View::e($view->t('design.repeating.save')) ?></button>
                <?php endif; ?>
            </form>
        <?php endif; ?>
    </div>
</div>
