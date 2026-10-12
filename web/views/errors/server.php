<?php
/**
 * The 500 page: one translated line, no stack trace, no internals
 * (REQ-API-006, REQ-CFG-005). The real reason went to the log under the
 * reference shown here (REQ-UI-050); `$detail` is filled only in development.
 */

use Clara\View;

$ref_line = $ref_line ?? '';
$detail = $detail ?? '';
?>
<h1 class="h4 mb-2"><?= View::e($title) ?></h1>

<p class="mb-0"><?= View::e($body) ?></p>

<?php if ($ref_line !== ''): ?>
    <p class="small text-secondary mt-2 mb-0"><?= View::e($ref_line) ?></p>
<?php endif; ?>

<?php if ($detail !== ''): ?>
    <pre class="small text-danger mt-2 mb-0"><?= View::e($detail) ?></pre>
<?php endif; ?>
