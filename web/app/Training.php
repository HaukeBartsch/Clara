<?php
// The training plans of the Training page (User_Interface_Design.md §2.7, REQ-UI-059): one
// directory per plan under web/assets/training/, holding the plan's text and its pictures, so a
// plan is added or edited by dropping files rather than by changing code. The page's data region
// is what this class reads from there — no API call, because the plans are part of the
// installation, not of any project.
//
// A plan directory holds
//   plan.md            the text (English, the fallback)
//   plan.<lang>.md     optional, the same text in a UI language (nb, nn, …)
//   *.png|jpg|…        the gallery pictures, shown in file-name order; the first is the card's
//
// The text is a small Markdown subset, parsed here into blocks the client renders with
// textContent (§3.7): `# ` the title (first line), paragraphs, `## ` headings, `- ` and `1. `
// lists, **bold** and `code`. Nothing in it ever reaches the page as HTML.

declare(strict_types=1);

namespace Clara;

final class Training
{
    /** Directory and file names that may be served — nothing else in the tree is listed. */
    private const PLAN_ID = '/^[a-z0-9][a-z0-9_-]{0,63}$/';
    private const IMAGE = '/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.(png|jpe?g|gif|webp)$/i';
    private const LANGUAGE = '/^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$/';

    public function __construct(
        private readonly string $directory,
        private readonly string $urlBase = '/assets/training'
    ) {}

    /** The plans the installation ships, under web/assets/training/. */
    public static function installed(): self
    {
        return new self(CLARA_WEB_ROOT . '/assets/training');
    }

    /**
     * Every plan, in directory-name order, with its text in `$language` where the plan has a
     * translation and in English otherwise. A directory without a plan.md is not a plan.
     *
     * @return list<array{id: string, title: string, summary: string, images: list<string>, blocks: list<array<string, mixed>>}>
     */
    public function plans(string $language): array
    {
        $entries = is_dir($this->directory) ? scandir($this->directory) : false;
        if ($entries === false) {
            return [];
        }
        $ids = array_values(array_filter(
            $entries,
            fn (string $name): bool => preg_match(self::PLAN_ID, $name) === 1 && is_dir($this->directory . '/' . $name)
        ));
        natsort($ids);

        $plans = [];
        foreach ($ids as $id) {
            $plan = $this->plan($id, $language);
            if ($plan !== null) {
                $plans[] = $plan;
            }
        }

        return $plans;
    }

    /** @return array{id: string, title: string, summary: string, images: list<string>, blocks: list<array<string, mixed>>}|null */
    private function plan(string $id, string $language): ?array
    {
        $dir = $this->directory . '/' . $id;
        $text = $this->text($dir, $language);
        if ($text === null) {
            return null;
        }

        $parsed = self::parse($text);

        return [
            'id' => $id,
            'title' => $parsed['title'] !== '' ? $parsed['title'] : $id,
            'summary' => $parsed['summary'],
            'images' => array_map(
                fn (string $file): string => $this->urlBase . '/' . $id . '/' . rawurlencode($file),
                $this->images($dir)
            ),
            'blocks' => $parsed['blocks'],
        ];
    }

    /** The plan's text in the UI language when it has one, else plan.md; null when neither exists. */
    private function text(string $dir, string $language): ?string
    {
        $candidates = [];
        if ($language !== '' && preg_match(self::LANGUAGE, $language) === 1) {
            $candidates[] = $dir . '/plan.' . $language . '.md';
        }
        $candidates[] = $dir . '/plan.md';

        foreach ($candidates as $file) {
            if (is_file($file)) {
                $text = file_get_contents($file);
                if ($text !== false) {
                    return $text;
                }
            }
        }

        return null;
    }

    /** @return list<string> the picture file names, in natural file-name order */
    private function images(string $dir): array
    {
        $entries = scandir($dir);
        if ($entries === false) {
            return [];
        }
        $images = array_values(array_filter(
            $entries,
            static fn (string $name): bool => preg_match(self::IMAGE, $name) === 1 && is_file($dir . '/' . $name)
        ));
        natcasesort($images);

        return array_values($images);
    }

    /**
     * Parses a plan's text into its title, the card's summary (the first paragraph, plain) and
     * the blocks of the modal. Each block's text is a list of runs — `{text, strong, code}` —
     * so emphasis survives without any markup reaching the client.
     *
     * @return array{title: string, summary: string, blocks: list<array<string, mixed>>}
     */
    public static function parse(string $text): array
    {
        $text = str_replace(["\r\n", "\r"], "\n", $text);
        if (str_starts_with($text, "\u{FEFF}")) {
            $text = substr($text, 3);
        }

        $title = '';
        $blocks = [];
        $paragraph = null; // list<string> of lines while a paragraph is open
        $list = null;      // array{ordered: bool, items: list<string>} while a list is open

        $close = static function () use (&$paragraph, &$list, &$blocks): void {
            if ($paragraph !== null) {
                $blocks[] = ['type' => 'paragraph', 'text' => self::runs(implode(' ', $paragraph))];
                $paragraph = null;
            }
            if ($list !== null) {
                $blocks[] = [
                    'type' => 'list',
                    'ordered' => $list['ordered'],
                    'items' => array_map([self::class, 'runs'], $list['items']),
                ];
                $list = null;
            }
        };

        foreach (explode("\n", $text) as $raw) {
            $line = trim($raw);

            if ($line === '') {
                $close();
                continue;
            }
            if ($title === '' && $blocks === [] && $paragraph === null && $list === null
                && preg_match('/^#\s+(.+)$/', $line, $m) === 1) {
                $title = self::plain($m[1]);
                continue;
            }
            if (preg_match('/^#{2,6}\s+(.+)$/', $line, $m) === 1) {
                $close();
                $blocks[] = ['type' => 'heading', 'text' => self::runs($m[1])];
                continue;
            }

            $ordered = preg_match('/^\d{1,3}[.)]\s+(.+)$/', $line, $om) === 1;
            $bullet = !$ordered && preg_match('/^[-*]\s+(.+)$/', $line, $bm) === 1;
            if ($ordered || $bullet) {
                if ($paragraph !== null || ($list !== null && $list['ordered'] !== $ordered)) {
                    $close();
                }
                $list ??= ['ordered' => $ordered, 'items' => []];
                $list['items'][] = $ordered ? $om[1] : $bm[1];
                continue;
            }

            // An indented line under a list item continues that item (a wrapped step); any other
            // line belongs to the open paragraph or starts one.
            if ($list !== null && preg_match('/^\s/', $raw) === 1) {
                $list['items'][count($list['items']) - 1] .= ' ' . $line;
                continue;
            }
            if ($list !== null) {
                $close();
            }
            $paragraph[] = $line;
        }
        $close();

        $summary = '';
        foreach ($blocks as $block) {
            if ($block['type'] === 'paragraph') {
                $summary = implode('', array_column($block['text'], 'text'));
                break;
            }
        }

        return ['title' => $title, 'summary' => $summary, 'blocks' => $blocks];
    }

    /**
     * One line of text as runs: `` `…` `` marks a code run (a field name, a pattern) and `**…**`
     * a strong one. Inside code nothing is a marker; an unpaired marker stays as text.
     *
     * @return list<array{text: string, strong: bool, code: bool}>
     */
    public static function runs(string $line): array
    {
        $runs = [];
        foreach (self::pairs($line, '`') as $index => $segment) {
            if ($index % 2 === 1) {
                $runs[] = ['text' => $segment, 'strong' => false, 'code' => true];
                continue;
            }
            foreach (self::pairs($segment, '**') as $inner => $part) {
                if ($part !== '') {
                    $runs[] = ['text' => $part, 'strong' => $inner % 2 === 1, 'code' => false];
                }
            }
        }

        return array_values(array_filter($runs, static fn (array $run): bool => $run['text'] !== ''));
    }

    /**
     * Splits on a marker so the odd-numbered parts are the marked ones. A last marker without a
     * partner is literal: it is glued back onto the text it would have opened.
     *
     * @return list<string>
     */
    private static function pairs(string $line, string $marker): array
    {
        $parts = explode($marker, $line);
        if (count($parts) % 2 === 0) {
            $last = array_pop($parts);
            $parts[count($parts) - 1] .= $marker . $last;
        }

        return $parts;
    }

    /** A line with its bold markers removed: the title and the card summary are plain text. */
    private static function plain(string $line): string
    {
        return implode('', array_column(self::runs($line), 'text'));
    }
}
