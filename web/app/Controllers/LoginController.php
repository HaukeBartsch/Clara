<?php
// The login page (User_Interface_Design.md §2.2) — local credentials only in
// this pass.
//
// What the page can offer depends on which authentication sources are configured
// (Sequence I, §2.9). With no names configured anywhere, or exactly one distinct
// name, the source picker is skipped and that name applies implicitly
// (REQ-UI-042, REQ-AUTH-067); a local-only installation is precisely that case.
// Where a selected name has only OAuth2 or LDAP sources behind it, this build has
// no path to complete them — the page says so plainly rather than accepting a
// password it cannot verify.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Response;
use Clara\Session;

final class LoginController extends Controller
{
    /** GET /login */
    public function index(): Response
    {
        // Already signed in: the page has nothing to offer.
        if (Session::isAuthenticated() && !Session::hasPendingSecondFactor()) {
            return Response::redirect('/');
        }

        return $this->renderLogin();
    }

    /** POST /login?action=credentials — the local credential attempt. */
    public function credentials(): Response
    {
        $email = trim($this->request->field('email'));
        $password = $this->request->field('password');
        $sourceName = $this->request->field('source');

        if ($email === '' || $password === '') {
            return $this->renderLogin($email, $this->i18n->t('login.failure.credentials'));
        }

        try {
            $user = $this->auth->loginWithLocalCredentials($email, $password, $sourceName);
        } catch (ApiException $e) {
            // A flow state is not a failure: mfa_required / tfa_enrollment_required
            // mean the first factor was accepted. The two-factor panels are M1's
            // remainder, so say what is missing instead of reporting a bad
            // password (branching on the code, never the status alone).
            if (Messages::isFlowState($e->code())) {
                $this->logger->info('second factor required but unsupported', ['code' => $e->code()]);

                return $this->renderLogin($email, $this->i18n->t('login.mfa_unsupported'));
            }

            // The API has already audited the failure; PHP shows one translated
            // line and never a reason it did not receive (§2.2, REQ-API-006).
            $this->logger->info('login rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->renderLogin($email, Messages::loginFailure($this->i18n, $e));
        }

        Session::establish($user, $sourceName);

        return Response::redirect($this->safeNext());
    }

    /** @param string $error a failure line to show above the form */
    private function renderLogin(string $email = '', string $error = ''): Response
    {
        $names = $this->auth->sourceNames();

        // A rejected credential re-renders the page with one translated line
        // (§3.3): the status stays 200 so the form is usable, and the reason is
        // logged rather than encoded in a status the browser cannot show.
        return $this->standalone('login', [
            'pageTitle' => $this->i18n->t('login.title'),
            'email' => $email,
            'error' => $error,
            // The picker appears only when there is a real choice to make.
            'sources' => count($names) > 1 ? $names : [],
            'next' => $this->safeNext(),
        ], ['titleKey' => 'login.title']);
    }

    /** The page to land on after login: only a same-site path is honoured. */
    private function safeNext(): string
    {
        $next = $this->request->query('next');
        if ($next !== '' && str_starts_with($next, '/') && !str_starts_with($next, '//')) {
            return $next;
        }

        return '/';
    }
}
