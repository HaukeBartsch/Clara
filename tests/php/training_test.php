<?php
// The Training page (§2.7, REQ-UI-059): the header link every signed-in user gets, the page with
// its empty card grid and modal, the `plans` region read from web/assets/training/, and the plan
// format — directory order, picture order, the language fallback, and a text that never becomes
// markup.

declare(strict_types=1);

use Clara\Training;

/** A throwaway plan tree; `$files` maps a relative path to its content. */
function training_tree(array $files): string
{
    $root = sys_get_temp_dir() . '/clara-training-' . bin2hex(random_bytes(6));
    foreach ($files as $path => $content) {
        $file = $root . '/' . $path;
        if (!is_dir(dirname($file))) {
            mkdir(dirname($file), 0700, true);
        }
        file_put_contents($file, $content);
    }

    return $root;
}

function training_remove(string $root): void
{
    $items = new RecursiveIteratorIterator(
        new RecursiveDirectoryIterator($root, FilesystemIterator::SKIP_DOTS),
        RecursiveIteratorIterator::CHILD_FIRST
    );
    foreach ($items as $item) {
        $item->isDir() ? rmdir($item->getPathname()) : unlink($item->getPathname());
    }
    rmdir($root);
}

describe('Training page (§2.7, REQ-UI-059)', function (): void {
    it('links the page in the header for every signed-in user, beside the Control Panel', function (): void {
        sign_in(['is_admin' => 0]);
        queue_shell();
        queue_sidebar_projects();

        $member = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();
        assert_contains('href="/training"', $member);
        assert_not_contains('href="/admin"', $member);

        sign_in(['is_admin' => 1]);
        $admin = router_for(http_request('GET', '/', browser_headers()))->dispatch()->body();
        assert_true(strpos($admin, 'href="/training"') < strpos($admin, 'href="/admin"'), 'Training sits before Control Panel');
    });

    it('renders the empty card grid and the modal, with no API read of its own', function (): void {
        sign_in();
        queue_shell();

        $response = router_for(http_request('GET', '/training', browser_headers()))->dispatch();
        $body = $response->body();

        assert_same(200, $response->status());
        assert_contains('<script type="module" src="/assets/js/training.js"></script>', $body);
        assert_contains('data-region="plans"', $body);
        assert_contains('id="clara-training-modal"', $body);
        assert_contains('data-training-gallery', $body);
        assert_true(preg_match('#btn-outline-secondary active"\s+href="/training"#', $body) === 1, 'the header link is marked current');
        assert_same(0, api_calls_to('/api/v1/projects'));
    });

    it('serves the installed plans as its data region, in directory order', function (): void {
        sign_in();

        $response = router_for(http_request('GET', '/training', json_headers()))->dispatch();
        $plans = json_decode($response->body(), true);

        assert_same(200, $response->status());
        assert_same(
            ['01-create-project', '02-data-collection-forms', '03-collect-data', '04-export-data', '05-export-audit-trails'],
            array_column($plans, 'id')
        );
        assert_same('Create a new project', $plans[0]['title']);
        foreach ($plans as $plan) {
            assert_true($plan['summary'] !== '', $plan['id'] . ' has a card text');
            assert_true($plan['images'] !== [], $plan['id'] . ' has pictures');
            foreach ($plan['images'] as $url) {
                assert_true(clara_resolve_asset($url) !== '', $url . ' is served by the asset handler');
            }
        }
    });

    it('is closed to an anonymous visitor, page and region alike', function (): void {
        $page = router_for(http_request('GET', '/training', browser_headers()))->dispatch();
        $region = router_for(http_request('GET', '/training', json_headers()))->dispatch();

        assert_same(302, $page->status());
        assert_same('/login?next=%2Ftraining', $page->headers()['Location']);
        assert_same(403, $region->status());
    });
});

describe('training plan directories (§2.7)', function (): void {
    it('lists plan directories in natural order and ignores everything else', function (): void {
        $root = training_tree([
            '10-later/plan.md' => "# Later\n\nLast.",
            '2-earlier/plan.md' => "# Earlier\n\nFirst.",
            'no-text/01.png' => 'png',
            'Upper/plan.md' => "# Upper case is not a plan id\n",
            '.hidden/plan.md' => "# Hidden\n",
            'loose.md' => "# Not a directory\n",
        ]);
        try {
            $plans = (new Training($root))->plans('en');
            assert_same(['2-earlier', '10-later'], array_column($plans, 'id'));
            assert_same('Earlier', $plans[0]['title']);
            assert_same('First.', $plans[0]['summary']);
            assert_same([], $plans[0]['images']);
        } finally {
            training_remove($root);
        }
    });

    it('lists pictures in natural file-name order and only picture files', function (): void {
        $root = training_tree([
            'p/plan.md' => "# P\n",
            'p/10.png' => 'x', 'p/2.jpg' => 'x', 'p/01.webp' => 'x', 'p/3.GIF' => 'x',
            'p/notes.txt' => 'x', 'p/script.svg' => 'x', 'p/.hidden.png' => 'x', 'p/with space.png' => 'x',
        ]);
        try {
            $plan = (new Training($root, '/t'))->plans('en')[0];
            assert_same(['/t/p/01.webp', '/t/p/2.jpg', '/t/p/3.GIF', '/t/p/10.png'], $plan['images']);
        } finally {
            training_remove($root);
        }
    });

    it('takes the text in the UI language when the plan has it, English otherwise', function (): void {
        $root = training_tree([
            'a/plan.md' => "# Create\n",
            'a/plan.nb.md' => "# Opprett\n",
            'b/plan.md' => "# Only English\n",
        ]);
        try {
            $training = new Training($root);
            assert_same(['Opprett', 'Only English'], array_column($training->plans('nb'), 'title'));
            assert_same(['Create', 'Only English'], array_column($training->plans('en'), 'title'));
            // The language is part of a file name: anything but a language tag is ignored.
            assert_same(['Create', 'Only English'], array_column($training->plans('../a/plan'), 'title'));
        } finally {
            training_remove($root);
        }
    });

    it('falls back to the directory name for a text without a title', function (): void {
        $root = training_tree(['untitled/plan.md' => "Just a paragraph.\n"]);
        try {
            $plan = (new Training($root))->plans('en')[0];
            assert_same('untitled', $plan['title']);
            assert_same('Just a paragraph.', $plan['summary']);
        } finally {
            training_remove($root);
        }
    });
});

describe('training plan text (§2.7)', function (): void {
    it('parses the title, paragraphs, headings and both kinds of list', function (): void {
        $parsed = Training::parse("\u{FEFF}# Title **here**\r\n\r\nFirst line\nwraps on.\n\n1. One\n   continued\n2) Two\n- Bullet\n\n## Next\nAfter.\n");

        assert_same('Title here', $parsed['title']);
        assert_same('First line wraps on.', $parsed['summary']);
        assert_same(['paragraph', 'list', 'list', 'heading', 'paragraph'], array_column($parsed['blocks'], 'type'));
        assert_true($parsed['blocks'][1]['ordered']);
        assert_same('One continued', $parsed['blocks'][1]['items'][0][0]['text']);
        assert_same('Two', $parsed['blocks'][1]['items'][1][0]['text']);
        assert_true(!$parsed['blocks'][2]['ordered']);
        assert_same('Next', $parsed['blocks'][3]['text'][0]['text']);
    });

    it('marks bold and code runs, and leaves an unpaired marker as text', function (): void {
        assert_same([
            ['text' => 'Choose ', 'strong' => false, 'code' => false],
            ['text' => 'Save', 'strong' => true, 'code' => false],
            ['text' => ' for ', 'strong' => false, 'code' => false],
            ['text' => 'a**b', 'strong' => false, 'code' => true],
        ], Training::runs('Choose **Save** for `a**b`'));
        assert_same([['text' => '2 ** 3 `x', 'strong' => false, 'code' => false]], Training::runs('2 ** 3 `x'));
    });

    it('passes markup through as plain text — the client writes it with textContent', function (): void {
        $parsed = Training::parse("# <b>T</b>\n\n<script>alert(1)</script> & <img src=x>\n");

        assert_same('<b>T</b>', $parsed['title']);
        assert_same('<script>alert(1)</script> & <img src=x>', $parsed['summary']);
        assert_same([['type' => 'paragraph', 'text' => [
            ['text' => '<script>alert(1)</script> & <img src=x>', 'strong' => false, 'code' => false],
        ]]], $parsed['blocks']);
    });
});
