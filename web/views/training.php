<?php
/**
 * The Training page (§2.7, REQ-UI-059): one Bootstrap card per training plan, and the modal a
 * card opens — the plan's picture gallery above its text.
 *
 * Both are data regions: the server renders the empty grid with its loading line and the modal's
 * fixed parts (labels, controls), and assets/js/training.js fills them from this route's JSON
 * (REQ-UI-032, REQ-UI-044). The plans themselves live under web/assets/training/, one directory
 * each, so adding one needs no change here.
 */

use Clara\View;
?>
<h1 class="h4 mb-2"><?= View::e($view->t('training.title')) ?></h1>
<p class="text-body-secondary mb-3"><?= View::e($view->t('training.intro')) ?></p>

<!-- Populated by assets/js/training.js from GET /training with Accept: application/json. -->
<div class="row row-cols-1 row-cols-sm-2 row-cols-lg-3 row-cols-xxl-4 g-3 clara-training-plans" data-region="plans">
    <div class="col">
        <p class="clara-region-empty mb-0"><?= View::e($view->t('js.loading')) ?></p>
    </div>
</div>

<div class="modal fade" id="clara-training-modal" tabindex="-1" aria-labelledby="clara-training-modal-title" aria-hidden="true">
    <div class="modal-dialog modal-xl modal-dialog-scrollable modal-fullscreen-lg-down">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h5" id="clara-training-modal-title"></h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="<?= View::e($view->t('action.close')) ?>"></button>
            </div>
            <div class="modal-body">
                <!-- The gallery: one slide per picture, previous/next, a counter and a thumbnail
                     strip. Hidden for a plan without pictures. -->
                <div class="clara-training-gallery mb-3" data-training-gallery hidden>
                    <div class="carousel slide" id="clara-training-carousel">
                        <div class="carousel-inner" data-training-slides></div>
                        <button class="carousel-control-prev" type="button" data-training-prev>
                            <span class="carousel-control-prev-icon" aria-hidden="true"></span>
                            <span class="visually-hidden"><?= View::e($view->t('training.gallery.previous')) ?></span>
                        </button>
                        <button class="carousel-control-next" type="button" data-training-next>
                            <span class="carousel-control-next-icon" aria-hidden="true"></span>
                            <span class="visually-hidden"><?= View::e($view->t('training.gallery.next')) ?></span>
                        </button>
                    </div>
                    <div class="d-flex align-items-center gap-2 mt-2">
                        <span class="small text-body-secondary text-nowrap" data-training-counter aria-live="polite"></span>
                        <div class="clara-training-thumbs d-flex gap-1" data-training-thumbs></div>
                    </div>
                </div>
                <div class="clara-training-text" data-training-text></div>
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-secondary" data-bs-dismiss="modal"><?= View::e($view->t('action.close')) ?></button>
            </div>
        </div>
    </div>
</div>
