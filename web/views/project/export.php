<?php
/**
 * Project page — Export (§6.4, REQ-UI-020): CSV or JSON, codes or labels, the header style,
 * the CSV delimiter and the arms to include, with a badge stating the sensitivity level the
 * download will be delivered at. The form is a plain GET to this route with `download=1`,
 * which streams the file (REQ-TECH-011); export.js only recomputes the badge when the arm
 * selection changes — without it the badge states the level for all arms, which is what the
 * form sends by default.
 *
 * Reads: $exportUrl, $arms (list of {arm_num, name, level}), $level, $formats, $values,
 * $headers, $delimiters.
 */

use Clara\View;

$multiArm = count($arms) > 1;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('export.title')) ?></h1>

<?php if ($arms === []): ?>
    <!-- Export rights that exist only as grants on single instruments and events: the export
         endpoint reads arm defaults alone and would refuse the download (see ExportController). -->
    <div class="alert alert-secondary py-2 clara-export-pairs-only"><?= View::e($view->t('export.pair_only')) ?></div>
<?php else: ?>
    <p class="text-body-secondary small"><?= View::e($view->t('export.intro')) ?></p>

    <form method="get" action="<?= View::e($exportUrl) ?>" class="card card-body clara-export-form" data-clara-export-form>
        <input type="hidden" name="download" value="1">

        <div class="row g-3">
            <fieldset class="col-sm-6 col-lg-3">
                <legend class="form-label small mb-1"><?= View::e($view->t('export.format')) ?></legend>
                <?php foreach ($formats as $i => $format): ?>
                    <div class="form-check">
                        <input class="form-check-input" type="radio" name="format" id="export-format-<?= View::e($format) ?>"
                               value="<?= View::e($format) ?>"<?= $i === 0 ? ' checked' : '' ?> data-clara-export-format>
                        <label class="form-check-label" for="export-format-<?= View::e($format) ?>"><?= View::e($view->t('export.format.' . $format)) ?></label>
                    </div>
                <?php endforeach; ?>
            </fieldset>

            <div class="col-sm-6 col-lg-3">
                <label class="form-label small mb-1" for="export-values"><?= View::e($view->t('export.values')) ?></label>
                <select class="form-select form-select-sm" id="export-values" name="rawOrLabel">
                    <?php foreach ($values as $value): ?>
                        <option value="<?= View::e($value) ?>"><?= View::e($view->t('export.values.' . $value)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="col-sm-6 col-lg-3">
                <label class="form-label small mb-1" for="export-headers"><?= View::e($view->t('export.headers')) ?></label>
                <select class="form-select form-select-sm" id="export-headers" name="rawOrLabelHeaders">
                    <?php foreach ($headers as $header): ?>
                        <option value="<?= View::e($header) ?>"><?= View::e($view->t('export.headers.' . $header)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="col-sm-6 col-lg-3" data-clara-export-csv>
                <label class="form-label small mb-1" for="export-delimiter"><?= View::e($view->t('export.delimiter')) ?></label>
                <select class="form-select form-select-sm" id="export-delimiter" name="delimiter">
                    <?php foreach ($delimiters as $delimiter): ?>
                        <option value="<?= View::e($delimiter) ?>"><?= View::e($view->t('export.delimiter.' . $delimiter)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <?php if ($multiArm): ?>
                <!-- One checkbox per arm the member may export, all checked (§6.4, REQ-EXP-003). -->
                <fieldset class="col-12">
                    <legend class="form-label small mb-1"><?= View::e($view->t('export.arms')) ?></legend>
                    <div class="d-flex flex-wrap gap-3">
                        <?php foreach ($arms as $arm): ?>
                            <div class="form-check mb-0">
                                <input class="form-check-input" type="checkbox" name="arm[]" id="export-arm-<?= (int) $arm['arm_num'] ?>"
                                       value="<?= (int) $arm['arm_num'] ?>" checked data-clara-export-arm data-level="<?= View::e($arm['level']) ?>">
                                <label class="form-check-label" for="export-arm-<?= (int) $arm['arm_num'] ?>">
                                    <?= View::e($view->t('roles.arm', ['arm' => (string) $arm['arm_num']]) . ($arm['name'] !== '' ? ' — ' . $arm['name'] : '')) ?>
                                    <span class="text-body-secondary small">(<?= View::e($view->t('export.level.' . $arm['level'])) ?>)</span>
                                </label>
                            </div>
                        <?php endforeach; ?>
                    </div>
                    <div class="form-text"><?= View::e($view->t('export.lowest_rule')) ?></div>
                </fieldset>
            <?php else: ?>
                <input type="hidden" name="arm[]" value="<?= (int) $arms[0]['arm_num'] ?>">
            <?php endif; ?>
        </div>

        <!-- The sensitivity being applied (§6.4, REQ-UI-020): stated before the download starts. -->
        <div class="mt-3 clara-export-level" role="status" aria-live="polite">
            <span class="small"><?= View::e($view->t('export.level')) ?>:</span>
            <span class="badge text-bg-primary" data-clara-export-level data-level="<?= View::e($level) ?>"><?= View::e($view->t('export.level.' . $level)) ?></span>
            <div class="form-text mt-1" data-clara-export-level-help><?= View::e($view->t('export.level_help.' . $level)) ?></div>
        </div>

        <div class="alert alert-warning py-2 mt-3 d-none" data-clara-export-no-arm role="alert"><?= View::e($view->t('export.no_arm')) ?></div>

        <div class="mt-3 d-flex flex-wrap align-items-center gap-3">
            <button class="btn btn-sm btn-primary" type="submit" data-clara-export-download><?= View::e($view->t('export.download')) ?></button>
            <span class="form-text m-0"><?= View::e($view->t('export.audit_note')) ?></span>
        </div>
    </form>
<?php endif; ?>
