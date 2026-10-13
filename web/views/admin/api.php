<?php

/**
 * The Control Panel's API documentation section (§5.9, REQ-UI-057): the OpenAPI document of
 * both API surfaces, rendered by the vendored Swagger UI, under the relevance Sankey of
 * REQ-UI-060. The API's own `/docs` page is never routed to a browser (INSTALL.md §13.1), so
 * this section is where an administrator reads it.
 *
 * `api-docs.js` loads the two vendored libraries and reads the document from this section's data
 * region (`GET /admin?section=api` with `Accept: application/json`); without JavaScript both
 * placeholders stay. Read-only: no request can be sent from here — the administration API is
 * not reachable from a browser, and the data API takes project tokens, not this session.
 *
 * The Sankey's structure arrives as `$sankey` — the generated classification (REQ-UI-060) with
 * translated labels, assembled in ControlPanelController::relevanceSankey(); it is null when the
 * generated file is absent, and the block is then simply not rendered. Data for the renderer
 * only; UI copy travels in the data-i18n block (REQ-UI-008).
 */

use Clara\View;
?>
<h1 class="h4 mb-3"><?= View::e($view->t('admin.api.title')) ?></h1>

<p class="text-body-secondary small"><?= View::e($view->t('admin.api.intro')) ?></p>

<?php if (isset($sankey)): ?>
    <section class="clara-relevance-sankey mb-4" aria-label="<?= View::e($view->t('admin.api.sankey.aria')) ?>">
        <h2 class="h6 mb-1"><?= View::e($view->t('admin.api.sankey.title')) ?></h2>
        <p class="text-body-secondary small mb-2"><?= View::e($view->t('admin.api.sankey.intro')) ?></p>
        <div class="clara-relevance-sankey-canvas rounded border p-2" data-clara-relevance-sankey></div>
        <script type="application/json" data-clara-sankey-data><?= json_encode($sankey,
            JSON_HEX_TAG | JSON_HEX_AMP | JSON_HEX_APOS | JSON_HEX_QUOT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) ?></script>
    </section>
<?php endif; ?>

<div class="clara-api-docs rounded border" data-clara-api-docs data-clara-region="/admin?section=api">
    <p class="p-3 mb-0 small" data-clara-api-docs-status><?= View::e($view->t('admin.api.loading')) ?></p>
</div>
