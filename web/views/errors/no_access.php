<?php
/**
 * The no-access page (§2.3, REQ-UI-006): a signed-in user who is neither an
 * administrator nor a member of any project sees an explanation, not an empty
 * dashboard. The sidebar renders as usual, with only what the user may use.
 *
 * For an administrator §2.3 replaces the explanation with the administration entry
 * points (create project §5.2, manage users §5.1). Those routes land with M3, so
 * until then this page explains rather than linking to a 404.
 */

use Clara\View;
?>
<h1 class="h4 mb-2"><?= View::e($pageTitle ?? $view->t('no_access.title')) ?></h1>

<p class="mb-0">
    <?= View::e($view->isAdmin() ? $view->t('no_access.admin_body') : $view->t('no_access.body')) ?>
</p>
