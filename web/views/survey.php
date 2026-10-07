<?php
/**
 * The public survey page (§8.8, REQ-UI-028): one instrument for one record, in the standalone
 * panel — no navigation, no account footer, no session (§2.4, GD-1).
 *
 * The fields render through the same partial as the record view, and survey.js runs the same
 * form behaviour over them (§8.4). The form has no submit control yet: answers cannot be sent
 * through a link until the API tells the page which record and event the link belongs to (see
 * SurveyController). The page says so instead of offering a button that would fail
 * (REQ-UI-003).
 *
 * Reads: $instrument, $items, and the form partial's inputs ($canEdit, $exists, $identifier,
 * $record, $prefill, $draft, $errors, $patterns, $formats), $clientContext.
 */

use Clara\View;
?>
<h1 class="h5 mb-1"><?= View::e($view->t('survey.title')) ?></h1>
<p class="text-body-secondary small mb-3"><?= View::e($instrument) ?></p>

<!-- The evaluator's inputs (§8.4): a survey form knows only its own fields, so a reference
     to one of them reads its input whatever event it names (anyEvent). Data for the evaluator
     only; UI copy travels in the data-i18n block. -->
<script type="application/json" data-clara-record-context><?= json_encode($clientContext,
    JSON_HEX_TAG | JSON_HEX_AMP | JSON_HEX_APOS | JSON_HEX_QUOT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) ?></script>

<form class="clara-survey-form" novalidate data-clara-survey-form aria-label="<?= View::e($instrument) ?>">
    <?php require __DIR__ . '/partials/form-fields.php'; ?>

    <?php if ($items === []): ?>
        <p class="clara-region-empty"><?= View::e($view->t('record.no_fields')) ?></p>
    <?php endif; ?>
</form>

<div class="alert alert-secondary py-2 mt-3 mb-0 clara-survey-closed" role="status">
    <?= View::e($view->t('survey.submit_unavailable')) ?>
</div>
