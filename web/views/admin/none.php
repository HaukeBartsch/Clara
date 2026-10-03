<?php

/**
 * The Control Panel with no section to show (§5). Every installation-administration function
 * arrives with M3; until the first one exists this page answers a typed URL and nothing more,
 * because the header's Control Panel button is absent when there is no section behind it —
 * a button to an empty page would be a disabled control by another name (§3.1, REQ-UI-003).
 */

use Clara\View;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.title')) ?></h1>

<p class="text-body-secondary"><?= View::e($view->t('admin.none')) ?></p>
