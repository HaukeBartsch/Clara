<?php
/**
 * Flash alerts (§3.4): one translated line at the top of the panel after a
 * mutation, naming the object it acted on. The buffer is drained here, so a
 * message shows on exactly one render.
 */

use Clara\View;

foreach ($view->alerts() as $alert): ?>
    <div class="alert alert-<?= View::e($alert['level']) ?> py-2" role="alert">
        <?= View::e($alert['text']) ?>
    </div>
<?php endforeach; ?>
