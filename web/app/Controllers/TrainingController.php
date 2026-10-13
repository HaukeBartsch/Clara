<?php
// The Training page (User_Interface_Design.md §2.7, REQ-UI-059): cards for the training plans
// installed under web/assets/training/, each opening a modal with the plan's picture gallery
// above its text. Any signed-in user may read it — the plans describe the application, not a
// project — so it renders in the single-panel shell of the project overview (§2.4 A).
//
// The page renders the empty card grid and the modal; assets/js/training.js fills both from this
// route's `plans` region (REQ-UI-044). The region reads files, not the API.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\Response;
use Clara\Training;

final class TrainingController extends Controller
{
    /** GET /training — the rendered page. */
    public function index(): Response
    {
        return $this->page('training', [], [
            'titleKey' => 'training.title',
            'scripts' => ['/assets/app.js', '/assets/js/training.js'],
            'jsKeys' => [
                'js.loading', 'js.load_failed', 'js.retry', 'training.empty',
                'training.gallery.counter', 'training.gallery.thumbnail', 'training.image_alt',
            ],
        ]);
    }

    /** The `plans` data region: every plan, in the acting user's UI language where it has one. */
    public function data(): Response
    {
        return Response::json(Training::installed()->plans($this->view->currentLanguage()));
    }
}
