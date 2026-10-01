<?php
/**
 * A refused page: 403 or 404 rendered as a page (Router::fromApiException). The
 * text is the §3.4 line for the API's code — uniform for forbidden and not-found on
 * protected resources, so nothing here discloses whether an object exists
 * (REQ-API-007).
 */

use Clara\View;
?>
<h1 class="h4 mb-2"><?= View::e($title) ?></h1>

<p class="mb-3"><?= View::e($body) ?></p>

<a class="btn btn-outline-secondary btn-sm" href="/"><?= View::e($view->t('nav.dashboard')) ?></a>
