<?php
// The three dedicated POST routes of the shell's Account section: sign out
// (§2.4), the language selector (§9) and the theme selector (§3.8). All three are
// CSRF-protected like every other mutation (REQ-UI-005) and both selectors
// redirect back, applying from the next page load — the page already rendered
// keeps its strings and its stylesheet (§9, REQ-UI-041).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Config;
use Clara\Messages;
use Clara\Response;
use Clara\Session;

final class AccountController extends Controller
{
    /** POST /logout — Sequence D: audit first, then destroy (REQ-UI-007). */
    public function logout(): Response
    {
        $this->auth->logout();

        return Response::redirect('/login');
    }

    /** POST /lang — persist the acting user's UI language (REQ-API-098). */
    public function language(): Response
    {
        $code = $this->request->field('language');
        if ($code === '') {
            return $this->redirectBack();
        }

        try {
            // The endpoint accepts exactly one attribute; anything else is a 400.
            $this->api->put('/api/v1/users/me/ui-language', ['language' => $code]);
        } catch (ApiException $e) {
            $this->logger->warn('language change failed', ['code' => $e->code()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $this->redirectBack();
        }

        // The session copy drives the next render's bundle lookup; the cached
        // overlay is dropped so the new language is fetched (I18n::loadOverlay).
        Session::setUiLanguage($code);

        return $this->redirectBack();
    }

    /** POST /theme — set or clear the personal theme override (REQ-API-122). */
    public function theme(): Response
    {
        $supplied = $this->request->field('theme');

        // "Default" clears the override and follows the installation theme; an
        // unknown identifier is refused here as well as by the API, so no value
        // that names no installed file ever reaches a session (§3.8).
        $theme = null;
        if ($supplied !== '' && $supplied !== 'default') {
            if (!Config::themeIsInstalled($supplied)) {
                $this->logger->warn('rejected unknown theme identifier', ['theme' => $supplied]);

                return $this->redirectBack();
            }
            $theme = $supplied;
        }

        try {
            // JSON null is the "no override" value, so the field must be present.
            $this->api->put('/api/v1/users/me/ui-theme', ['theme' => $theme]);
        } catch (ApiException $e) {
            $this->logger->warn('theme change failed', ['code' => $e->code()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $this->redirectBack();
        }

        Session::setUiThemeOverride($theme);

        return $this->redirectBack();
    }
}
