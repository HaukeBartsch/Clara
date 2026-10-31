<?php
/**
 * The 500 page: one translated line, no stack trace, no internals
 * (REQ-API-006, REQ-CFG-005). The real reason went to the log under the
 * reference shown here (REQ-UI-050); `$detail` is filled only in development.
 * A caller that knows the way back passes `$back_href` and `$back_label`: the
 * login page's failed provider start points at the source-name picker (§2.2).
 */

use Clara\View;

$ref_line = $ref_line ?? '';
$detail = $detail ?? '';
$back_href = $back_href ?? '';
$back_label = $back_label ?? '';
?>
<h1 class="h4 mb-2"><?= View::e($title) ?></h1>

<p class="mb-0"><?= View::e($body) ?></p>

<?php if ($ref_line !== ''): ?>
    <p class="small text-secondary mt-2 mb-0"><?= View::e($ref_line) ?></p>
<?php endif; ?>

<?php if ($detail !== ''): ?>
    <pre class="small text-danger text-wrap text-break mt-2 mb-0"><?= View::e($detail) ?></pre>
<?php endif; ?>

<?php if ($back_href !== '' && $back_label !== ''): ?>
    <a class="btn btn-primary btn-sm mt-3" href="<?= View::e($back_href) ?>"><?= View::e($back_label) ?></a>
<?php endif; ?>
