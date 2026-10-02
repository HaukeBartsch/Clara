<?php
// The two public password pages of §2.6 (GD-23, Sequence H): the forgot-password request
// and the set-password page an invite or reset link opens.
//
// Neither page establishes an identity. There is no session behind them and none is
// created by completing one — the token is the whole credential, and it buys exactly one
// action on the one account it was issued for (`Authentication_Authorization_Design.md`
// §2.8). Two consequences run through this file:
//
//   * Nothing a visitor does here may disclose whether an address has an account. The
//     request page answers with one line whatever came back (REQ-AUTH-062), and every token
//     problem — unknown, expired, consumed, wrong purpose — is the same generic line
//     (REQ-API-120).
//   * A token value is a credential: it is carried in a hidden field, never logged, and
//     never repeated anywhere but that field (§2.6).
//
// The emailed link carries no purpose marker (`Authentication_Authorization_Design.md` §2.8
// fixes its URL as `/set-password?token=…`), so "route by purpose" means trying one purpose
// and then the other — which is only possible because a wrong-purpose token is
// indistinguishable from an invalid one, and neither has any effect at all.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Response;

final class PasswordResetController extends Controller
{
    /** GET /password-reset — the address field (§2.6). */
    public function requestForm(): Response
    {
        return $this->requestPage('');
    }

    /**
     * POST /password-reset?action=request — ask for a reset link.
     *
     * The API answers 202 whether or not anything was sent, and this page passes that
     * indifference straight through: one line, no hint about the address (REQ-AUTH-062). Only
     * a rate-limit rejection changes the text, because waiting is the user's to do.
     */
    public function request(): Response
    {
        $email = trim($this->request->field('email'));
        if ($email === '') {
            return $this->requestPage($this->i18n->t('password_reset.failure.missing'));
        }

        try {
            $this->api->post('/api/v1/auth/password-reset/request', ['email' => $email]);
        } catch (ApiException $e) {
            if ($e->status() === 429) {
                $this->logger->info('password reset request rate limited');

                return $this->requestPage(Messages::rateLimited($this->i18n, $e));
            }

            // Anything else is an internal problem; the line stays generic and the same
            // "if that address…" sentence would be dishonest to show instead.
            $this->logger->error('password reset request failed', ['code' => $e->code()]);

            return $this->requestPage($this->i18n->t('error.generic'));
        }

        return $this->requestPage($this->i18n->t('password_reset.sent'), sent: true);
    }

    /**
     * GET /set-password?token=… — the new-password fields (§2.6). A link with no token is
     * not a form waiting to fail: it says so, with the way back in.
     */
    public function setForm(): Response
    {
        $token = trim($this->request->query('token'));
        if ($token === '') {
            return $this->setPage('', invalid: true);
        }

        return $this->setPage($token);
    }

    /**
     * POST /set-password?action=complete — set the password the link authorises.
     *
     * The repeat field is checked here as well as in the browser, and the API checks the
     * policy once the token has proven itself (passwords.go) — so a short password on a good
     * token reads back as "the form contains invalid values: …", which is what §3.4 asks for.
     */
    public function complete(): Response
    {
        $token = trim($this->request->field('token'));
        $password = $this->request->field('new_password');
        $repeat = $this->request->field('repeat_password');

        if ($token === '') {
            return $this->setPage('', invalid: true);
        }
        if ($password === '') {
            return $this->setPage($token, $this->i18n->t('account.password.failure.missing'));
        }
        if ($password !== $repeat) {
            return $this->setPage($token, $this->i18n->t('account.password.failure.mismatch'));
        }

        try {
            $this->redeem($token, $password);
        } catch (ApiException $e) {
            if ($e->code() === 'invalid_setup_token') {
                // One line for every token problem, with the way back in and — since a
                // reset is what an address without a working password asks for — a fresh
                // link to offer (REQ-API-120, §2.6).
                $this->logger->info('setup token refused');

                return $this->setPage('', invalid: true);
            }

            return $this->setPage($token, Messages::forApiException($this->i18n, $e));
        }

        // Success is the same sentence for both purposes: the password now works and the
        // next step is signing in (§2.6). The token is not carried into this render at all.
        return $this->standalone('set_password', [
            'pageTitle' => $this->i18n->t('set_password.title'),
            'done' => true,
        ], ['titleKey' => 'set_password.title']);
    }

    // --- the two purposes, in order -------------------------------------------

    /**
     * Redeems the token against either purpose. An invite is tried first: it is the rarer
     * path and the one whose failure costs nothing to retry, and a reset token simply does
     * not verify as an invite — no state changes on the way (REQ-API-120).
     *
     * @throws ApiException when neither purpose accepts the token, or when the password
     *                      itself is what the API refused
     */
    private function redeem(string $token, string $password): void
    {
        try {
            $this->api->post('/api/v1/auth/invite/complete', ['token' => $token, 'password' => $password]);

            return;
        } catch (ApiException $e) {
            if ($e->code() !== 'invalid_setup_token') {
                throw $e;
            }
        }

        $this->api->post('/api/v1/auth/password-reset/complete', ['token' => $token, 'password' => $password]);
    }

    // --- panels ---------------------------------------------------------------

    private function requestPage(string $message, bool $sent = false): Response
    {
        return $this->standalone('password_reset', [
            'pageTitle' => $this->i18n->t('password_reset.title'),
            'message' => $message,
            'sent' => $sent,
            'email' => $sent ? '' : trim($this->request->field('email')),
        ], ['titleKey' => 'password_reset.title']);
    }

    /**
     * The set-password panel. `$token` is rendered into one hidden field and nowhere else
     * (§2.6); `''` means there is nothing to complete, which the template shows as the
     * generic token failure with a link back to the sign-in page.
     */
    private function setPage(string $token, string $error = '', bool $invalid = false): Response
    {
        return $this->standalone('set_password', [
            'pageTitle' => $this->i18n->t('set_password.title'),
            'token' => $token,
            'error' => $error,
            'invalid' => $invalid || $token === '',
        ], [
            'titleKey' => 'set_password.title',
            'scripts' => ['/assets/app.js', '/assets/js/password.js'],
            'jsKeys' => ['account.password.mismatch'],
        ]);
    }
}
