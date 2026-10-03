<?php
/**
 * The no-access page (§2.3, REQ-UI-006): a signed-in user who is neither an
 * administrator nor a member of any project sees an explanation, not an empty
 * dashboard. It renders as the single content panel of the overview shell — no left
 * panel, exactly like the page it stands in for (§2.4 A).
 *
 * An administrator never lands here: `is_admin` sees every project (REQ-API-049), so an
 * empty list means the installation has none yet and the overview shows its empty table
 * (§2.3, §4).
 */

use Clara\View;
?>
<h1 class="h4 mb-2"><?= View::e($pageTitle ?? $view->t('no_access.title')) ?></h1>

<p class="mb-0">
    <?= View::e($view->t('no_access.body')) ?>
</p>
