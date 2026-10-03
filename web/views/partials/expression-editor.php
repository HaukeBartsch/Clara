<?php
/**
 * The expression editor (§7.3, REQ-UI-021/022) — one component, configured per grammar:
 * `branching` (GD-13: references, constants, comparisons, text_contains / is_blank /
 * is_not_blank, && / ||, parentheses) or `calculation` (GD-11: references, numbers, + - * /,
 * parentheses).
 *
 * Assistance only: a reference picker offering the project's active `[event][field]`
 * references (the page route's `references` data region, fetched when the picker is first
 * opened — REQ-UI-044), quick-insert buttons for the grammar's operators and functions, and a
 * highlighted read-back of the expression. It validates nothing — the API does, at save, and
 * its reason is shown (REQ-VAL-029). Without JavaScript it is a plain text area.
 *
 * Reads: $exprId, $exprName, $exprValue, $exprGrammar, $exprLabel, $exprRefsUrl.
 */

use Clara\View;

$exprInserts = $exprGrammar === 'calculation'
    ? ['+', '-', '*', '/', '(', ')']
    : ['=', '!=', '<', '>', '<=', '>=', '&&', '||', '(', ')', '""', 'text_contains(, "")', 'is_blank()', 'is_not_blank()'];
?>
<div class="clara-expr" data-clara-expression="<?= View::e($exprGrammar) ?>" data-clara-refs="<?= View::e($exprRefsUrl) ?>">
    <label class="form-label mb-0 small" for="<?= View::e($exprId) ?>"><?= View::e($exprLabel) ?></label>
    <textarea class="form-control form-control-sm font-monospace" id="<?= View::e($exprId) ?>" name="<?= View::e($exprName) ?>"
              rows="2" spellcheck="false" autocomplete="off"><?= View::e($exprValue) ?></textarea>
    <div class="d-flex flex-wrap gap-1 mt-1 clara-expr-tools">
        <label class="visually-hidden" for="<?= View::e($exprId) ?>-refs"><?= View::e($view->t('design.refs.label')) ?></label>
        <select class="form-select form-select-sm w-auto" id="<?= View::e($exprId) ?>-refs" data-clara-refs-select>
            <option value=""><?= View::e($view->t('design.refs.choose')) ?></option>
        </select>
        <?php foreach ($exprInserts as $insert): ?>
            <button class="btn btn-sm btn-outline-secondary font-monospace" type="button"
                    data-clara-insert="<?= View::e($insert) ?>"><?= View::e($insert) ?></button>
        <?php endforeach; ?>
    </div>
    <pre class="clara-expr-preview small mb-0 mt-1" aria-hidden="true"></pre>
    <div class="form-text"><?= View::e($view->t($exprGrammar === 'calculation' ? 'design.expr.help_calc' : 'design.expr.help_branching')) ?></div>
</div>
