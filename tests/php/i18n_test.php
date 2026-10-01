<?php
// Translation resolution (REQ-UI-008, REQ-DB-031): English is the source of truth, an
// override overlays it, a key with no override renders English rather than blank or
// raw, and the JS-visible block carries copy only — encoded so nothing can break out.

declare(strict_types=1);

use Clara\ApiClient;
use Clara\I18n;
use Clara\Logger;
use Clara\Request;
use Clara\Session;

function i18n_for(bool $development = false): I18n
{
    $config = test_config();
    $logger = new Logger('error', true);
    $request = new Request('GET', '/', [], [], []);

    return new I18n(new ApiClient($config, $logger, $request, new FakeTransport()), $logger, $development);
}

describe('i18n (REQ-UI-008)', function (): void {
    it('resolves an English key', function (): void {
        assert_same('Sign in', i18n_for()->t('login.submit'));
    });

    it('substitutes {placeholders}', function (): void {
        $text = i18n_for()->t('error.rate_limited_seconds', ['seconds' => 90]);

        assert_contains('90', $text);
        assert_not_contains('{seconds}', $text);
    });

    it('overlays the bundle on English for the signed-in language', function (): void {
        sign_in(['ui_language' => 'nb']);
        api_route('/api/v1/i18n/bundle', ['language' => 'nb', 'strings' => ['login.submit' => 'Logg inn']]);

        assert_same('Logg inn', i18n_for()->t('login.submit'));
    });

    it('falls back to English for a key the language has no row for', function (): void {
        sign_in(['ui_language' => 'nb']);
        api_route('/api/v1/i18n/bundle', ['language' => 'nb', 'strings' => ['login.submit' => 'Logg inn']]);

        // Not translated: English, never blank and never the key itself.
        $text = i18n_for()->t('nav.dashboard');
        assert_same('Dashboard', $text);
    });

    it('treats an empty override as a removal and falls back to English (§5.7)', function (): void {
        sign_in(['ui_language' => 'nn']);
        api_route('/api/v1/i18n/bundle', ['language' => 'nn', 'strings' => ['login.submit' => '']]);

        assert_same('Sign in', i18n_for()->t('login.submit'));
    });

    it('caches the bundle for the session and reads it once', function (): void {
        sign_in(['ui_language' => 'nb']);
        api_route('/api/v1/i18n/bundle', ['language' => 'nb', 'strings' => ['login.submit' => 'Logg inn']]);

        $i18n = i18n_for();
        $i18n->t('login.submit');
        $i18n->t('nav.dashboard');

        assert_same(1, api_calls_to('/api/v1/i18n/bundle'));
    });

    it('drops the cached bundle when the language changes (POST /lang)', function (): void {
        sign_in(['ui_language' => 'en']);
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => []]);

        i18n_for()->t('login.submit');
        Session::setUiLanguage('nb');

        assert_same(null, Session::cachedBundle(), 'the next render fetches the new language');
    });

    it('renders English with no API call for an anonymous page', function (): void {
        $_SESSION = [];

        assert_same('Sign in', i18n_for()->t('login.submit'));
        assert_same(0, api_calls_to('/api/v1/i18n/bundle'), 'a public page has no language to look up');
    });

    it('fails loudly in development when a key is missing from English', function (): void {
        $exception = assert_throws(LogicException::class, static function (): void {
            i18n_for(true)->t('no.such.key');
        });

        assert_contains('no.such.key', $exception->getMessage());
    });

    it('never renders a raw key silently in production', function (): void {
        // It logs and shows the key rather than blanking out — visible, traceable.
        assert_same('no.such.key', i18n_for(false)->t('no.such.key'));
    });

    it('encodes the JS string block so no script can be closed from inside it', function (): void {
        sign_in();
        api_route('/api/v1/i18n/bundle', ['language' => 'en', 'strings' => [
            'js.loading' => '</script><script>alert(1)</script>',
        ]]);

        $block = i18n_for()->jsStrings(['js.loading']);

        assert_not_contains('</script><script', $block);
        assert_contains('\\u003c/script\\u003e', strtolower($block));
    });
});
