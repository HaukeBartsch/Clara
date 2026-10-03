<?php
// The Control Panel (`User_Interface_Design.md` §5, REQ-UI-047) — the one page that carries
// installation administration, reached from the header button of §2.4 and from nowhere else.
// Its left panel lists the sections this build serves; the right-hand panel shows the section
// `?section=` names, defaulting to the first available (§2.1).
//
// Sections arrive with M3 (Users, Projects, Audits, Translations): each one adds an entry in
// Clara\Navigation and a case in renderSection(). An unknown or not-yet-built `section`
// renders the first available section rather than an error — the panel is the truth about what
// exists, and a link to nothing is a disabled control by another name (§3.1, REQ-UI-003).
//
// The whole page is `is_admin`: the route guard decides it before any handler runs, so no
// handler re-checks it and none can be reached by a signed-out browser.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Navigation;
use Clara\Response;

final class ControlPanelController extends Controller
{
    /** GET /admin — the Control Panel with one section open (§5). */
    public function index(): Response
    {
        return $this->renderSection(Navigation::resolveAdminSection($this->request->query('section')), '');
    }

    /**
     * POST /admin?action=save_settings — `PUT /api/v1/settings` (§5.8, REQ-UI-043,
     * REQ-API-112). The three attributes are sent as the types the endpoint declares; a
     * value that is not a number never reaches the API, and every range rule stays the
     * API's own (REQ-VAL-002), reported through the §3.4 mapping. An applied change is
     * audit-logged server-side with old and new values (REQ-AUD-027) and takes effect on
     * the next request without a restart.
     */
    public function saveSettings(): Response
    {
        $enabled = $this->request->field('rate_limit_enabled') === '1';
        $rpm = $this->request->field('rate_limit_rpm');
        $blockMinutes = $this->request->field('rate_limit_block_minutes');

        if (preg_match('/^\d+$/', $rpm) !== 1 || preg_match('/^\d+$/', $blockMinutes) !== 1) {
            return $this->settingsPage($this->i18n->t('admin.settings.invalid'), $enabled, $rpm, $blockMinutes);
        }

        try {
            // Exactly the three attributes §4.22 accepts — an extra one is a 400
            // (Plan/Web_Implementation.md §7 rule 1).
            $this->api->put('/api/v1/settings', [
                'rate_limit_enabled' => $enabled,
                'rate_limit_rpm' => (int) $rpm,
                'rate_limit_block_minutes' => (int) $blockMinutes,
            ]);
        } catch (ApiException $e) {
            $this->logger->info('settings change rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->settingsPage(Messages::forApiException($this->i18n, $e), $enabled, $rpm, $blockMinutes);
        }

        $this->flashSuccess($this->i18n->t('admin.settings.saved'));

        return Response::redirect('/admin?section=settings');
    }

    // --- sections ------------------------------------------------------------

    /**
     * The page with one section open: the section's own panel in the right-hand pane, the
     * available sections in the left one. A build that serves no section answers with the
     * information page — reachable only by typing the URL, since the header button is
     * absent when there is nowhere to go (§3.1).
     *
     * @param array{key?: string, labelKey?: string}|array{} $section
     */
    private function renderSection(array $section, string $error): Response
    {
        return match ($section['key'] ?? '') {
            'settings' => $this->settingsPage($error),
            default => $this->page('admin/none', [
                'pageTitle' => $this->i18n->t('admin.title'),
            ], ['titleKey' => 'admin.title']),
        };
    }

    /**
     * The Settings section (§5.8). With no posted values it reads the current ones from the
     * API; after a rejected save it shows back what was submitted, so a refusal does not
     * silently revert the form to the stored values.
     */
    private function settingsPage(string $error, ?bool $enabled = null, ?string $rpm = null, ?string $blockMinutes = null): Response
    {
        if ($enabled === null || $rpm === null || $blockMinutes === null) {
            $settings = $this->api->get('/api/v1/settings');
            $settings = is_array($settings) ? $settings : [];
            $enabled = !empty($settings['rate_limit_enabled']);
            $rpm = (string) ($settings['rate_limit_rpm'] ?? '');
            $blockMinutes = (string) ($settings['rate_limit_block_minutes'] ?? '');
        }

        return $this->page('admin/settings', [
            'pageTitle' => $this->i18n->t('admin.settings.title'),
            'error' => $error,
            'enabled' => $enabled,
            'rpm' => $rpm,
            'blockMinutes' => $blockMinutes,
            // The Control Panel's own left panel (§2.4 B): the sections this build serves.
            // This page is the Settings section, so it marks itself active — a query
            // parameter is invisible to path matching (§2.1).
            'nav' => [
                'headingKey' => 'admin.title',
                'items' => array_map(
                    static fn (array $section): array => [
                        'path' => '/admin?section=' . $section['key'],
                        'labelKey' => $section['labelKey'],
                        'active' => $section['key'] === 'settings',
                    ],
                    Navigation::availableAdminSections()
                ),
            ],
        ], ['titleKey' => '']);
    }
}
