<?php
/**
 * A completeness marker (§6.3, REQ-UI-081): the page's one counter SVG (partials/counter-sprite.php,
 * included once per page) drawn by reference. Markers differ only in their classes, as in the master
 * spec ("Visual appearence"): `counter`, the `counter-<n>` that turns tiles on (app.css), and the
 * colouring — none for grey, `counter-blue`, `counter-yellow`, `counter-green`.
 *
 * Reads: $counterClass — the classes after `counter`, e.g. `counter-yellow counter-3` (may be '');
 * $counterLit — how many tiles are on once the count is known, as `data-lit` (absent or null: unknown).
 */

use Clara\View;

$counterClasses = trim('counter ' . ($counterClass ?? ''));
$counterLitAttr = isset($counterLit) ? ' data-lit="' . (int) $counterLit . '"' : '';
?>
<svg class="<?= View::e($counterClasses) ?>"<?= $counterLitAttr ?> aria-hidden="true" focusable="false"><use href="#clara-counter"></use></svg>
