<?php
/**
 * The public survey page's single-state answers (§8.8): a link that can no longer be used,
 * too many requests, the service unreachable. One translated line, nothing to retry and
 * nothing that tells a stranger more than that (REQ-AUTH-040).
 *
 * Reads: $message.
 */

use Clara\View;
?>
<h1 class="h5 mb-3"><?= View::e($view->t('survey.title')) ?></h1>
<p class="mb-0 clara-survey-notice"><?= View::e($message) ?></p>
