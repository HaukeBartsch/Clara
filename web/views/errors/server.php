<?php
/**
 * The 500 page: one translated line, no stack trace, no internals
 * (REQ-API-006, REQ-CFG-005). The real reason went to the log.
 */

use Clara\View;
?>
<h1 class="h4 mb-2"><?= View::e($title) ?></h1>

<p class="mb-0"><?= View::e($body) ?></p>
