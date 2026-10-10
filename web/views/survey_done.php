<?php
/**
 * The public survey page's closing state (§8.8, REQ-UI-028): the answers were stored and the
 * visit ends here. It says so without implying the link is spent — re-opening it serves the form
 * again, since the respondent may change their responses (REQ-AUTH-042). Standalone panel, no
 * form, no session (§2.4, GD-1).
 */

use Clara\View;
?>
<h1 class="h5 mb-3"><?= View::e($view->t('survey.title')) ?></h1>
<div class="alert alert-success py-2 mb-0 clara-survey-done" role="status">
    <?= View::e($view->t('survey.done')) ?>
</div>
<p class="small text-body-secondary mt-3 mb-0 clara-survey-reopen"><?= View::e($view->t('survey.done.once')) ?></p>
