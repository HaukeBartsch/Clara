<?php
// The account page (§2.8, REQ-UI-063) and the three dedicated POST routes it hosts: sign
// out (§2.4), the language selector (§9) and the theme selector (§3.8); the preferred region
// (REQ-UI-075) posts to the page's own route with ?action=set_region. All three are
// CSRF-protected like every other mutation (REQ-UI-005) and both selectors redirect back,
// applying from the next page load — the page already rendered keeps its strings and its
// stylesheet (§9, REQ-UI-041). Sign out is also offered by the header's account menu.
//
// The Project tokens section shows the member's own tokens masked; Show asks the API for one
// value, which records the view first (REQ-API-162, REQ-AUD-033), and the value is rendered
// straight from that response — like the recovery codes of §2.5, it is stored nowhere, so a
// reload or Hide (a plain GET) is the masked page again.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Config;
use Clara\Navigation;
use Clara\Regions;
use Clara\Response;
use Clara\Session;

final class AccountController extends Controller
{
    /** GET /account — Profile: who is signed in, language, theme, region, sign out (§2.8). */
    public function index(): Response
    {
        // The region is read here rather than taken from the session's copy of the user object:
        // an administrator's assignment can set a placeholder after this session began (§2.8,
        // API_Endpoints_Design.md §4.30). A failed read costs the selector, not the page.
        try {
            $region = $this->api->get('/api/v1/users/me/region');
        } catch (ApiException $e) {
            $this->logger->info('region read failed', ['code' => $e->code(), 'status' => $e->status()]);
            $region = null;
        }

        return $this->page('account/profile', [
            'pageTitle' => $this->i18n->t('account.section.profile'),
            'email' => Session::email(),
            'region' => is_array($region) ? $region : null,
            'regionOptions' => Regions::options($this->i18n),
            'regionLabel' => fn (string $code): string => Regions::label($this->i18n, $code),
            'nav' => Navigation::accountNav('profile'),
        ], ['titleKey' => '']);
    }

    /**
     * POST /account?action=set_region — the user's own preferred region (REQ-UI-075):
     * `PUT /api/v1/users/me/region` with a code, or null for Automatic. A value outside the
     * list is refused here before the API refuses it again; nothing is kept in the session, so
     * the overview regroups on its next read (§4).
     */
    public function setRegion(): Response
    {
        $supplied = $this->request->field('region');
        if ($supplied !== '' && !Regions::isCode($supplied)) {
            $this->flashDanger($this->invalidRequest('region'));

            return Response::redirect('/account');
        }

        try {
            // JSON null is "Automatic", so the attribute is always sent.
            $this->api->put('/api/v1/users/me/region', ['region' => $supplied !== '' ? $supplied : null]);
        } catch (ApiException $e) {
            $this->logger->info('region change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));

            return Response::redirect('/account');
        }
        $this->flashSuccess($this->i18n->t('account.region.saved'));

        return Response::redirect('/account');
    }

    /** GET /account/tokens — the member's own project tokens, every one masked (§2.8). */
    public function tokens(): Response
    {
        return $this->tokensPage(null);
    }

    /**
     * POST /account/tokens?action=show_token — unmasks one project's token. The API records the
     * view before it answers the value (REQ-API-162), so only a recorded view reaches the page;
     * a refusal keeps every token masked with the §3.4 line. Each Show is one entry.
     */
    public function showToken(): Response
    {
        $projectId = $this->fieldId('project_id');
        if ($projectId <= 0) {
            $this->flashDanger($this->invalidRequest('project_id'));

            return Response::redirect('/account/tokens');
        }
        try {
            $answer = $this->api->post('/api/v1/users/me/projects/' . $projectId . '/token-reveal');
        } catch (ApiException $e) {
            $this->logger->info('own token reveal refused', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));

            return Response::redirect('/account/tokens');
        }
        $token = is_array($answer) && is_string($answer['token'] ?? null) ? $answer['token'] : '';
        if ($token === '') {
            return Response::redirect('/account/tokens');
        }

        return $this->tokensPage(['project_id' => $projectId, 'token' => $token]);
    }

    /**
     * The Project tokens section with at most one token shown — the one just revealed.
     *
     * @param array{project_id: int, token: string}|null $shown
     */
    private function tokensPage(?array $shown): Response
    {
        $memberships = $this->api->get('/api/v1/users/me/projects');

        return $this->page('account/tokens', [
            'pageTitle' => $this->i18n->t('account.section.tokens'),
            'memberships' => array_values(array_filter(is_array($memberships) ? $memberships : [], 'is_array')),
            'shown' => $shown,
            'nav' => Navigation::accountNav('tokens'),
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js', '/assets/js/admin.js'],
            'jsKeys' => ['admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied'],
        ]);
    }

    /** A positive integer form field, or 0. */
    private function fieldId(string $name): int
    {
        $raw = $this->request->field($name);

        return preg_match('/^\d{1,18}$/', $raw) === 1 ? (int) $raw : 0;
    }

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
            $this->flashDanger($this->apiFailureMessage($e));

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
            $this->flashDanger($this->apiFailureMessage($e));

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
     * posting something the API must refuse with 409 `no_local_credential`; the account
     * page's panel already hides the entry for such an account (§2.8, GD-23).
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
     * E lockout. A change ends every other session of the account (REQ-AUTH-074): the API
     * raised the session epoch and answered the new value, which this session adopts — under
     * a new id — so the person who changed the password stays signed in here.
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
            // (User_Interface_Design.md §12 rule 1).
            $answer = $this->api->put('/api/v1/users/me/password', [
                'current_password' => $current,
                'new_password' => $password,
            ]);
        } catch (ApiException $e) {
            $this->logger->info('password change rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->passwordPage($this->apiFailureMessage($e));
        }
        if (is_int($answer['session_epoch'] ?? null)) {
            Session::adoptEpoch($answer['session_epoch']);
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
            'nav' => Navigation::accountNav('password'),
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js', '/assets/js/password.js'],
            'jsKeys' => ['account.password.mismatch'],
        ]);
    }
}
