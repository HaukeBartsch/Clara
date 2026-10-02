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

    /**
     * GET /account/password — change the local password (§2.6, REQ-AUTH-061).
     *
     * The e-mail address is shown read-only and there is no field for it: identity
     * changes go through an administrator (REQ-AUTH-061). An account that authenticates
     * through a provider has no local password to change, so the page says so instead of
     * posting something the API must refuse with 409 `no_local_credential`; the sidebar
     * already hides the entry for such an account (§2.4, GD-23).
     */
    public function password(): Response
    {
        return $this->passwordPage('');
    }

    /**
     * POST /account/password?action=change — `PUT /api/v1/users/me/password`.
     *
     * The repeat field is checked here as well as in the browser: the client check is a
     * convenience, this one is authoritative (§2.6). A wrong current password is the §3.4
     * bad-password line — the API has already audited it and counted it toward the Sequence
     * E lockout — and existing sessions stay valid (DEV-AUTH-13), which is why nothing here
     * touches the session.
     */
    public function changePassword(): Response
    {
        $current = $this->request->field('current_password');
        $password = $this->request->field('new_password');
        $repeat = $this->request->field('repeat_password');

        if ($current === '' || $password === '') {
            return $this->passwordPage($this->i18n->t('account.password.failure.missing'));
        }
        if ($password !== $repeat) {
            return $this->passwordPage($this->i18n->t('account.password.failure.mismatch'));
        }

        try {
            // Exactly the two attributes the endpoint accepts — an extra one is a 400
            // (Plan/Web_Implementation.md §7 rule 1).
            $this->api->put('/api/v1/users/me/password', [
                'current_password' => $current,
                'new_password' => $password,
            ]);
        } catch (ApiException $e) {
            $this->logger->info('password change rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->passwordPage(Messages::forApiException($this->i18n, $e));
        }

        // Nothing is echoed back and nothing is kept: the confirmation is the flash, and
        // the re-rendered page has empty fields (REQ-AUTH-036).
        $this->flashSuccess($this->i18n->t('account.password.changed'));

        return Response::redirect('/account/password');
    }

    /** The change-password panel with an optional failure line above the form. */
    private function passwordPage(string $error): Response
    {
        return $this->page('account/password', [
            'pageTitle' => $this->i18n->t('account.password.title'),
            'error' => $error,
            'hasLocalCredential' => Session::hasLocalCredential(),
            'email' => Session::email(),
            'sidebarProjects' => $this->visibleProjects(),
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js', '/assets/js/password.js'],
            'jsKeys' => ['account.password.mismatch'],
        ]);
    }
}
