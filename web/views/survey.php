<?php
/**
 * The public survey page (§8.8, REQ-UI-028): one instrument for one record, in the standalone
 * panel — no navigation, no account footer, no session (§2.4, GD-1).
 *
 * The fields render through the same partial as the record view, and survey.js runs the same
 * form behaviour over them (§8.4). Submitting posts to this same path — the dedicated POST of
 * §2.1, since there is no session here to carry an `?action=` — and carries field values only:
 * the API resolves the record, instrument and event from the link token, so the study's record
 * identifier never travels to the respondent's browser (REQ-API-083).
 *
 * Reads: $instrument, $items, $formNote (a refusal no field input can carry), and the form
 * partial's inputs ($canEdit, $exists, $identifier, $record, $prefill, $draft, $errors,
 * $patterns, $formats), $clientContext.
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

<form class="clara-survey-form" method="post" novalidate data-clara-survey-form aria-label="<?= View::e($instrument) ?>">
    <?php require __DIR__ . '/partials/form-fields.php'; ?>

    <?php if ($items === []): ?>
        <p class="clara-region-empty"><?= View::e($view->t('record.no_fields')) ?></p>
    <?php else: ?>
        <!-- The collection zone of the respondent's dates (GD-16), filled by survey.js. -->
        <input type="hidden" name="tz" value="" data-clara-tz>

        <?php if ($errors !== [] || $formNote !== ''): ?>
            <div class="alert alert-danger py-2 mt-3 mb-0" role="alert">
                <?= View::e($view->t('record.save.invalid')) ?><?php if ($formNote !== ''): ?>
                    <span class="d-block"><?= View::e($formNote) ?></span>
                <?php endif; ?>
            </div>
        <?php endif; ?>

        <div class="mt-3">
            <button class="btn btn-primary" type="submit"><?= View::e($view->t('survey.submit')) ?></button>
        </div>
    <?php endif; ?>
</form>
