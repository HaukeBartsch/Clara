<?php
/**
 * Project page — Export (§6.4, REQ-UI-020/069): the data export — CSV or JSON,
 * codes or labels, the header style, the CSV delimiter, decimal separator and byte-order
 * mark — pre-selected for the UI language ($csvDefaults) — and the arms to include, with a
 * badge stating the sensitivity level the download will be delivered at. Its form is a plain
 * GET to this route with `download=1`, which streams the file (REQ-TECH-011); export.js only
 * recomputes the badge when the arm selection changes — without it the badge states the level
 * for all arms, which is what the form sends by default. Beside it, the data dictionary card
 * links `download=dictionary`: the project's field definitions as `instrument.csv`, design
 * only and no record values, so it carries none of the form's options (REQ-UI-020).
 *
 * Reads: $exportUrl, $arms (list of {arm_num, name, level, dropped}), $level, $dropped,
 * $formats, $values, $headers, $delimiters, $decimals, $csvDefaults, $isAdmin.
 */

use Clara\View;

$multiArm = count($arms) > 1;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('export.title')) ?></h1>

<?php if ($arms === []): ?>
    <!-- The gate is a non-export_none level anywhere (§6.4); reaching here with no arm to
         offer means those levels sit on pairs this project's detail does not place. -->
    <div class="alert alert-secondary py-2 clara-export-none"><?= View::e($view->t('export.none_here')) ?></div>
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
                        <option value="<?= View::e($delimiter) ?>"<?= $delimiter === $csvDefaults['delimiter'] ? ' selected' : '' ?>><?= View::e($view->t('export.delimiter.' . $delimiter)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="col-sm-6 col-lg-3" data-clara-export-csv>
                <label class="form-label small mb-1" for="export-decimal"><?= View::e($view->t('export.decimal')) ?></label>
                <select class="form-select form-select-sm" id="export-decimal" name="decimal">
                    <?php foreach ($decimals as $decimal): ?>
                        <option value="<?= View::e($decimal) ?>"<?= $decimal === $csvDefaults['decimal'] ? ' selected' : '' ?>><?= View::e($view->t('export.decimal.' . $decimal)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="col-sm-12 col-lg-6" data-clara-export-csv>
                <div class="form-check mt-lg-4">
                    <input class="form-check-input" type="checkbox" name="bom" id="export-bom" value="1"<?= $csvDefaults['bom'] ? ' checked' : '' ?>>
                    <label class="form-check-label small" for="export-bom"><?= View::e($view->t('export.bom')) ?></label>
                    <div class="form-text mt-0"><?= View::e($view->t('export.bom_help')) ?></div>
                </div>
            </div>

            <?php if ($multiArm): ?>
                <!-- One checkbox per arm the member may export, all checked (§6.4, REQ-EXP-003). -->
                <fieldset class="col-12">
                    <legend class="form-label small mb-1"><?= View::e($view->t('export.arms')) ?></legend>
                    <div class="d-flex flex-wrap gap-3">
                        <?php foreach ($arms as $arm): ?>
                            <div class="form-check mb-0">
                                <input class="form-check-input" type="checkbox" name="arm[]" id="export-arm-<?= (int) $arm['arm_num'] ?>"
                                       value="<?= (int) $arm['arm_num'] ?>" checked data-clara-export-arm
                                       data-level="<?= View::e($arm['level']) ?>" data-dropped="<?= (int) ($arm['dropped'] ?? 0) ?>">
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
                <!-- One arm: no checkbox to show, but the arm still carries its count of
                     excluded pairs — every offered arm states it in the DOM. -->
                <input type="hidden" name="arm[]" value="<?= (int) $arms[0]['arm_num'] ?>"
                       data-dropped="<?= (int) ($arms[0]['dropped'] ?? 0) ?>">
            <?php endif; ?>
        </div>

        <!-- The sensitivity being applied (§6.4, REQ-UI-020): stated before the download starts. -->
        <div class="mt-3 clara-export-level" role="status" aria-live="polite">
            <span class="small"><?= View::e($view->t('export.level')) ?>:</span>
            <span class="badge text-bg-primary" data-clara-export-level data-level="<?= View::e($level) ?>"><?= View::e($view->t('export.level.' . $level)) ?></span>
            <div class="form-text mt-1" data-clara-export-level-help><?= View::e($view->t('export.level_help.' . $level)) ?></div>
        </div>

        <!-- Pairs outside the member's permission appear in no download; a partly permitted
             scope is stated rather than hidden (§6.4, REQ-UI-020). export.js recounts them
             over the checked arms, since an unselected arm excludes none. -->
        <div class="form-text mt-1 clara-export-dropped<?= $dropped > 0 ? '' : ' d-none' ?>"
             data-clara-export-dropped><?= View::e($dropped > 0 ? $view->t('export.dropped_pairs', ['count' => (string) $dropped]) : '') ?></div>

        <div class="alert alert-warning py-2 mt-3 d-none" data-clara-export-no-arm role="alert"><?= View::e($view->t('export.no_arm')) ?></div>

        <div class="mt-3 d-flex flex-wrap align-items-center gap-3">
            <button class="btn btn-sm btn-primary" type="submit" data-clara-export-download><?= View::e($view->t('export.download')) ?></button>
            <span class="form-text m-0"><?= View::e($view->t('export.audit_note')) ?></span>
        </div>
    </form>
<?php endif; ?>

<?php if ($isAdmin): ?>
    <div class="card card-body mt-4 clara-odm-export">
        <h2 class="h6 mb-1"><?= View::e($view->t('export.odm_title')) ?></h2>
        <p class="text-body-secondary small mb-3"><?= View::e($view->t('export.odm_intro')) ?></p>
        <form method="get" action="<?= View::e($exportUrl) ?>" class="d-flex flex-wrap align-items-end gap-2">
            <input type="hidden" name="download" value="odm">
            <div>
                <label class="form-label small mb-1" for="export-odm-version"><?= View::e($view->t('export.odm_version')) ?></label>
                <select class="form-select form-select-sm" id="export-odm-version" name="odm_version">
                    <?php foreach ($odmVersions as $version): ?>
                        <option value="<?= View::e($version) ?>"<?= $version === $odmVersion ? ' selected' : '' ?>><?= View::e($view->t('export.odm_version.' . str_replace('.', '_', $version))) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <button class="btn btn-sm btn-outline-primary" type="submit" data-clara-odm-download><?= View::e($view->t('export.odm_download')) ?></button>
        </form>
    </div>
<?php endif; ?>

<!-- The second download of the page (REQ-UI-020): the data dictionary, design only and no
     record values — a plain link, since it carries none of the form's options. -->
<div class="card card-body mt-4 clara-dictionary-export">
    <h2 class="h6 mb-1"><?= View::e($view->t('export.dictionary_title')) ?></h2>
    <p class="text-body-secondary small mb-3"><?= View::e($view->t('export.dictionary_intro')) ?></p>
    <div>
        <a class="btn btn-sm btn-outline-primary" data-clara-dictionary-download
           href="<?= View::e($exportUrl . '?download=dictionary') ?>"><?= View::e($view->t('export.dictionary_download')) ?></a>
    </div>
</div>
