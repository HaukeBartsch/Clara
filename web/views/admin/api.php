<?php

/**
 * The Control Panel's API documentation section (§5.9, REQ-UI-057): the OpenAPI document of
 * both API surfaces, rendered by the vendored Swagger UI. The API's own `/docs` page is never
 * routed to a browser (INSTALL.md §13.1), so this section is where an administrator reads it.
 *
 * `api-docs.js` loads the Swagger UI bundle and reads the document from this section's data
 * region (`GET /admin?section=api` with `Accept: application/json`); without JavaScript the
 * placeholder stays. Read-only: no request can be sent from here — the administration API is
 * not reachable from a browser, and the data API takes project tokens, not this session.
 */

use Clara\View;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.api.title')) ?></h1>

<p class="text-body-secondary small"><?= View::e($view->t('admin.api.intro')) ?></p>

<div class="clara-api-docs rounded border" data-clara-api-docs data-clara-region="/admin?section=api">
    <p class="p-3 mb-0 small" data-clara-api-docs-status><?= View::e($view->t('admin.api.loading')) ?></p>
</div>
