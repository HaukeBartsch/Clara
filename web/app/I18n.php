<?php
// UI strings, resolved server-side at render time (REQ-UI-008, GD-12) — there is
// no i18n library on the client and none in PHP.
//
// English is the source of truth and lives here in the web layer
// (REQ-DB-031, API_Endpoints_Design.md §4.19); the database holds only the
// overrides for the other languages, served by GET /api/v1/i18n/bundle and
// overlaid on top of this catalog. A key the overlay does not carry renders in
// English — never blank, never a raw key (REQ-UI-008). An override whose text is
// empty means "removed" and also falls back to English (§5.7 of the UI design).
//
// Anonymous pages have no user, so they have no language to look up: they render
// English without calling the API at all (the bundle read needs an actor).

declare(strict_types=1);

namespace Clara;

final class I18n
{
    /** @var array<string, string> the English catalog */
    private array $english;

    /** @var array<string, string> the active language's overrides */
    private array $overlay = [];

    /** How long a fetched language list stays fresh inside one session. */
    private const LANGUAGE_CACHE_SECONDS = 900;

    public function __construct(
        private readonly ApiClient $api,
        private readonly Logger $logger,
        private readonly bool $isDevelopment
    ) {
        $catalog = require __DIR__ . '/i18n/en.php';
        if (!is_array($catalog)) {
            throw new \LogicException('app/i18n/en.php must return an array');
        }
        $this->english = $catalog;

        if (Session::isAuthenticated()) {
            $this->loadOverlay();
        }
    }

    /**
     * The translated line for a key, with `{name}` placeholders substituted from
     * $params. Values are returned as-is: escaping happens once, in the view
     * (REQ-UI-004), and the same string also feeds the JS block of §9.
     *
     * @param array<string, string|int> $params
     */
    /**
     * The English catalog itself — the key universe the translations section edits
     * (§5.7: "English array in code, DB overlay", Plan/Web_Implementation.md §5).
     *
     * @return array<string, string>
     */
    public function englishCatalog(): array
    {
        return $this->english;
    }

    public function t(string $key, array $params = []): string
    {
        $text = $this->overlay[$key] ?? null;
        if ($text === null || $text === '') {
            $text = $this->english[$key] ?? null;
        }

        if ($text === null) {
            // Every key must exist in English; a miss is a developer error, not
            // something to show the user. Fail loudly in development.
            if ($this->isDevelopment) {
                throw new \LogicException("missing UI string for key '{$key}'");
            }
            $this->logger->warn('missing UI string', ['key' => $key]);
            $text = $key;
        }

        if ($params === []) {
            return $text;
        }

        $replacements = [];
        foreach ($params as $name => $value) {
            $replacements['{' . $name . '}'] = (string) $value;
        }

        return strtr($text, $replacements);
    }

    /**
     * The enabled languages for the selector (§9), code + display name.
     *
     * Cached for a quarter of an hour per session: the selector is on every page,
     * and one read per page would spend a third of the render's API budget on it
     * (Plan/Web_Implementation.md §7 rule 13). A language an administrator enables
     * appears for everyone within that window.
     */
    public function languages(): array
    {
        $cached = Session::isAuthenticated() ? self::cachedLanguages() : null;
        if ($cached !== null) {
            return $cached;
        }

        try {
            $languages = $this->api->get('/api/v1/i18n/languages');
        } catch (ApiException $e) {
            // The selector is decoration: without it the page still renders in
            // English, so log and continue rather than fail the request.
            $this->logger->warn('language list unavailable', ['code' => $e->code()]);

            return [['code' => 'en', 'display_name' => 'English']];
        }

        $out = [];
        foreach ($languages as $language) {
            if (is_array($language) && isset($language['code'], $language['display_name'])) {
                $out[] = [
                    'code' => (string) $language['code'],
                    'display_name' => (string) $language['display_name'],
                ];
            }
        }
        if ($out === []) {
            return [['code' => 'en', 'display_name' => 'English']];
        }

        if (Session::isAuthenticated()) {
            $_SESSION['_i18n_languages'] = ['fetched_at' => time(), 'languages' => $out];
        }

        return $out;
    }

    /** @return list<array{code: string, display_name: string}>|null */
    private static function cachedLanguages(): ?array
    {
        $cached = $_SESSION['_i18n_languages'] ?? null;
        if (!is_array($cached) || !isset($cached['fetched_at'], $cached['languages'])) {
            return null;
        }
        if ((int) $cached['fetched_at'] + self::LANGUAGE_CACHE_SECONDS < time()) {
            return null;
        }

        return is_array($cached['languages']) ? $cached['languages'] : null;
    }

    /**
     * The JS-visible strings for one page, as the contents of a
     * `<script type="application/json" data-i18n>` element (§9). Values are
     * already translated here — a module never carries a UI string of its own
     * (REQ-UI-045) — and the JSON_HEX flags make `</script>` breakout impossible.
     * Data values must never be passed in: this block holds UI copy only.
     *
     * @param list<string> $keys
     */
    public function jsStrings(array $keys): string
    {
        $out = [];
        foreach ($keys as $key) {
            $out[$key] = $this->t($key);
        }

        // JSON_HEX_TAG is what makes the breakout impossible (`<` and `>` never
        // reach the document); slashes stay unescaped so the text reads as the
        // author wrote it, which also keeps the encoded block diffable.
        return (string) json_encode(
            $out,
            JSON_HEX_TAG | JSON_HEX_AMP | JSON_HEX_QUOT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES
        );
    }

    /**
     * Fetches the active language's override map once per session (the plan fixes
     * "cache the bundle per session and invalidate on POST /lang"). A failure is
     * not cached: English renders meanwhile and the next request retries.
     */
    private function loadOverlay(): void
    {
        $language = Session::uiLanguage();

        $cached = Session::cachedBundle();
        if ($cached !== null && ($cached['language'] ?? '') === $language) {
            $strings = $cached['strings'] ?? [];
            $this->overlay = is_array($strings) ? array_map('strval', $strings) : [];

            return;
        }

        try {
            $bundle = $this->api->get('/api/v1/i18n/bundle', ['language' => $language]);
        } catch (ApiException $e) {
            // English fallback is the specified behaviour, not an error state —
            // but a disabled language is worth one log line.
            $this->logger->warn('i18n bundle unavailable', ['code' => $e->code(), 'language' => $language]);

            return;
        }

        $strings = $bundle['strings'] ?? [];
        if (!is_array($strings)) {
            $strings = [];
        }
        $this->overlay = array_map('strval', $strings);
        Session::storeBundle($language, $this->overlay);
    }
}
