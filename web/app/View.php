<?php
// Template rendering: layout + page template, with escaping as the only way to
// interpolate (REQ-UI-004, REQ-TECH-020) and CSP already on the response by way
// of Response. No template engine — plain PHP templates with one escaping rule,
// which is what keeps "escaped by default" reviewable: every interpolation in a
// view calls View::e(), and the single exception below is greppable.
//
// htmlAllowed() is that exception and must stay the only place stored allowlist
// HTML reaches the page (Data_Validation_Design.md §5.2). It renders nothing of
// its own accord: it takes text the API already sanitized, and this pass has no
// stored values to show, so no view calls it yet.

declare(strict_types=1);

namespace Clara;

final class View
{
    /** @var list<string> module paths for <script type="module"> */
    private array $scripts = [];

    /** @var list<string> UI-string keys the page's modules need */
    private array $jsKeys = [];

    public function __construct(
        private readonly Config $config,
        private readonly I18n $i18n,
        private readonly Request $request
    ) {}

    /** Escapes for HTML text and attribute contexts. Escape everything. */
    public static function e(mixed $value): string
    {
        if ($value === null) {
            return '';
        }
        if (is_bool($value)) {
            return $value ? '1' : '0';
        }

        return htmlspecialchars((string) $value, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
    }

    /**
     * The only allowlist-HTML escape hatch: stored free-text values, sanitized on
     * write by the API's content policy. Never call it with anything else — a
     * review rule keeps it that way (Plan/Web_Implementation.md §9).
     */
    public static function htmlAllowed(?string $sanitized): string
    {
        return (string) $sanitized;
    }

    /**
     * Renders a page template inside a layout.
     *
     * @param array<string, mixed> $data     variables the template reads
     * @param array{layout?: string|null, titleKey?: string, scripts?: list<string>, jsKeys?: list<string>} $options
     */
    public function render(string $template, array $data = [], array $options = []): Response
    {
        $this->scripts = $options['scripts'] ?? [];
        $this->jsKeys = $options['jsKeys'] ?? [];

        $content = $this->capture($template, $data);

        $layout = $options['layout'] ?? 'shell';
        if ($layout === null) {
            $body = $content;
        } else {
            $body = $this->capture('layout/' . $layout, array_merge($data, [
                'content' => $content,
                'titleKey' => $options['titleKey'] ?? '',
            ]));
        }

        return Response::html($body);
    }

    /** Translated line — the one call a template makes for copy (REQ-UI-008). */
    public function t(string $key, array $params = []): string
    {
        return $this->i18n->t($key, $params);
    }

    /** @return list<array{level: string, text: string}> */
    public function alerts(): array
    {
        return Session::takeFlash();
    }

    /** True when a section may be emitted at all — hidden means absent (§3.1). */
    public function isAdmin(): bool
    {
        return Session::isAdmin();
    }

    /**
     * Whether the account has a local password to change — the gate on the Password entry
     * in the shell footer (§2.4, GD-23).
     */
    public function hasLocalCredential(): bool
    {
        return Session::hasLocalCredential();
    }

    public function displayName(): string
    {
        return Session::displayName();
    }

    /** The acting user's e-mail, shown where the name alone is ambiguous. */
    public function email(): string
    {
        return Session::email();
    }

    public function isAuthenticated(): bool
    {
        return Session::isAuthenticated();
    }

    /** The path being rendered — what marks the active left-panel entry. */
    public function currentPath(): string
    {
        return $this->request->path();
    }

    /** True when this navigation item is the page on screen (exact or nested). */
    public function isActive(string $path): bool
    {
        $current = $this->currentPath();

        return $current === $path
            || ($path !== '/' && str_starts_with($current, rtrim($path, '/') . '/'));
    }

    /** The value a selector's form posts back as `next` (redirect back, §9/§3.8). */
    public function self(): string
    {
        $query = $this->request->query('next');

        return $query === '' ? $this->currentPath() : $this->currentPath() . '?next=' . rawurlencode($query);
    }

    /** Hidden CSRF input for a POST form (REQ-UI-005). */
    public function csrfField(): string
    {
        return Csrf::field();
    }

    /**
     * Exactly one Bootstrap stylesheet per page, resolved server-side: the user's
     * override when set, else the installation default. Only identifiers that
     * name an installed file resolve, so no request value reaches the href
     * (REQ-UI-040, REQ-TECH-027). Public pages have no user context and take the
     * installation default.
     */
    public function stylesheetHref(): string
    {
        $theme = $this->isAuthenticated() ? Session::uiThemeOverride() : null;
        if ($theme === null || !Config::themeIsInstalled($theme)) {
            $theme = $this->config->uiTheme;
        }

        return Config::themeHref($theme);
    }

    /** The installation default, for the selector's "Default" entry. */
    public function defaultTheme(): string
    {
        return $this->config->uiTheme;
    }

    /** Installed themes with translated labels, for the theme selector (§3.8). */
    public function installedThemes(): array
    {
        $labels = [
            'bootstrap' => $this->t('theme.bootstrap'),
            'darkly' => $this->t('theme.darkly'),
            'yeti' => $this->t('theme.yeti'),
        ];

        $themes = [];
        foreach (Config::installedThemes() as $theme) {
            $themes[] = ['id' => $theme, 'label' => $labels[$theme] ?? ucfirst($theme)];
        }

        return $themes;
    }

    /** The personal override, or null when following the installation default. */
    public function selectedTheme(): ?string
    {
        return $this->isAuthenticated() ? Session::uiThemeOverride() : null;
    }

    public function languages(): array
    {
        return $this->i18n->languages();
    }

    public function currentLanguage(): string
    {
        return $this->isAuthenticated() ? Session::uiLanguage() : 'en';
    }

    /**
     * The JS-visible string block (§9). Data only — never a data value, only UI
     * copy — and JSON-encoded so no `</script>` sequence can appear.
     */
    public function jsStringBlock(): string
    {
        if ($this->jsKeys === []) {
            return '';
        }

        return '<script type="application/json" data-i18n>' . $this->i18n->jsStrings($this->jsKeys) . '</script>';
    }

    /** @return list<string> */
    public function scripts(): array
    {
        return $this->scripts;
    }

    /** The absolute path of a view template, or null when it does not exist. */
    private function templatePath(string $template): ?string
    {
        // Templates come from this repository's code, never from a request; the
        // containment check is defence against a future caller that forgets that.
        $path = realpath(CLARA_WEB_ROOT . '/views/' . $template . '.php');
        if ($path === false || !str_starts_with($path, realpath(CLARA_WEB_ROOT . '/views') ?: '')) {
            return null;
        }

        return $path;
    }

    /** @param array<string, mixed> $data */
    private function capture(string $template, array $data): string
    {
        $path = $this->templatePath($template);
        if ($path === null) {
            throw new \RuntimeException("view template not found: {$template}");
        }

        $render = static function (string $file, array $scope, View $view): string {
            extract($scope, EXTR_SKIP);
            ob_start();
            require $file;

            return (string) ob_get_clean();
        };

        return $render($path, $data, $this);
    }
}
