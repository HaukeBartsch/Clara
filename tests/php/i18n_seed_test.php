<?php
// The draft-catalog loader (tools/seed_translations.php, M6 §5.7): validation is total before
// anything reaches PUT /api/v1/i18n/strings (REQ-API-100) — unknown keys, empty texts (they mean
// "remove" on the API) and {placeholder} multiset drift all refuse the run. No HTTP here; the
// committed nb/nn drafts are checked against the live English catalog with the same functions.

declare(strict_types=1);

require_once __DIR__ . '/../../tools/seed_translations.php';

describe('translation seed validation (M6, REQ-API-100)', function (): void {
    it('counts placeholder multisets, not just names', function (): void {
        assert_same([], seed_placeholders('plain text'));
        assert_same(['count' => 1], seed_placeholders('{count} records'));
        assert_same(['a' => 2, 'b' => 1], seed_placeholders('{a} {b} {a}'));
    });

    it('accepts a draft that mirrors the English placeholders', function (): void {
        $problems = seed_validate(
            ['greeting' => 'Hei {name}, {count} ting', 'plain' => 'Ok'],
            ['greeting' => 'Hi {name}, {count} things', 'plain' => 'OK']
        );

        assert_same([], $problems);
    });

    it('refuses keys the English catalog does not have', function (): void {
        $problems = seed_validate(['no.such.key' => 'Verdi'], ['other.key' => 'Value']);

        assert_true(count($problems) === 1);
        assert_contains('no.such.key', $problems[0]);
    });

    it('refuses an empty text — on the API that removes the override', function (): void {
        $problems = seed_validate(['k' => ''], ['k' => 'Value']);

        assert_true(count($problems) === 1);
        assert_contains('empty text', $problems[0]);
    });

    it('refuses missing, extra and miscounted placeholders', function (): void {
        $missing = seed_validate(['k' => 'Tapt'], ['k' => 'Has {x}']);
        $extra = seed_validate(['k' => 'For my {y}'], ['k' => 'Plain']);
        $miscounted = seed_validate(['k' => 'én {x}'], ['k' => '{x} and {x}']);

        assert_contains('{x}', $missing[0]);
        assert_contains('{y}', $extra[0]);
        assert_contains('2×', $miscounted[0]);
    });

    it('chunks entries into API wire batches of the given size', function (): void {
        $entries = [];
        for ($i = 0; $i < 457; $i++) {
            $entries['key.' . $i] = 'text';
        }
        $batches = seed_batches('nb', $entries, 200);

        assert_same(3, count($batches));
        assert_same(200, count($batches[0]['entries']));
        assert_same(57, count($batches[2]['entries']));
        assert_same('nb', $batches[0]['language']);
        assert_same(['key' => 'key.0', 'text' => 'text'], $batches[0]['entries'][0]);
    });

    it('skips @-prefixed metadata and rejects non-string values when loading a draft', function (): void {
        $path = tempnam(sys_get_temp_dir(), 'clara-i18n-') . '.json';
        file_put_contents($path, json_encode(['@draft' => 'note', 'a.b' => 'Verdi']));
        assert_same(['a.b' => 'Verdi'], seed_load_draft($path));

        file_put_contents($path, json_encode(['a.b' => 42]));
        assert_throws(RuntimeException::class, static fn (): array => seed_load_draft($path));

        unlink($path);
    });

    // The committed drafts must never carry keys the catalog lost, empty texts, or placeholder
    // drift against the live English values — the loader would refuse them, so CI checks first.
    it('the committed nb and nn drafts stay valid against the English catalog', function (): void {
        $english = seed_english_catalog();
        foreach (['nb', 'nn'] as $language) {
            $draft = seed_load_draft(__DIR__ . '/../../tools/i18n/' . $language . '.json');
            foreach ($draft as $key => $text) {
                assert_true(array_key_exists($key, $english), $language . ': stale key ' . $key);
                assert_true($text !== '', $language . ': empty text for ' . $key);
                if (seed_placeholders($text) !== seed_placeholders($english[$key])) {
                    throw new RuntimeException($language . ': placeholder drift on ' . $key);
                }
            }
        }
    });
});
