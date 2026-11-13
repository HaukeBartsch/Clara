<?php
// POST /auth/callback — the form_post authorization response (REQ-CFG-041,
// Authentication_Authorization_Design.md §2.1 step 3a).
//
// A provider configured with OAUTH2_N_RESPONSE_MODE=form_post returns the browser with an
// auto-submitting form. That POST is cross-site: the SameSite=Lax session cookie does not come
// with it and it carries no CSRF token, so it runs without the session (DEV-TECH-4: `state` and
// PKCE are what bind the response to the sign-in, as on the GET). It does one thing — park the
// response under a single-use handle and send the browser on to
// GET /auth/callback?handoff=<handle>, where the session cookie is present and Sequence A
// continues exactly as for a query response. Nothing is verified here and nothing is called.
//
// The route is open only while some provider is configured for form_post; GET /auth/callback
// stays open whatever is configured.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\OauthHandoff;
use Clara\Response;

final class OauthCallbackController extends Controller
{
    public function receive(): Response
    {
        if (!$this->config->acceptsFormPostCallback()) {
            return Response::emptyBody(405)->withHeader('Allow', 'GET');
        }

        $length = $this->request->header('content-length');
        $fields = null;
        if ($length === null || (preg_match('/^\d{1,12}$/', $length) === 1 && (int) $length <= OauthHandoff::MAX_BODY_BYTES)) {
            $fields = OauthHandoff::responseFields(
                $this->request->rawBody(OauthHandoff::MAX_BODY_BYTES),
                ['code' => $this->request->field('code'), 'state' => $this->request->field('state'), 'error' => $this->request->field('error')]
            );
        }

        $handle = null;
        if ($fields !== null) {
            $handle = (new OauthHandoff($this->config->sessionDir))->put($fields, time());
            if ($handle === null) {
                $this->logger->error('oauth form_post response could not be kept', []);
            }
        } else {
            $this->logger->info('oauth form_post response refused', ['reason' => 'malformed']);
        }

        // A response that is not one still goes on to the GET, with a handle that redeems
        // nothing: the browser lands on the login form with the failure line and the session's
        // transaction is cleared there, rather than on a bare error page from this route.
        return Response::redirect('/auth/callback?handoff=' . ($handle ?? ''), 303)
            ->withHeader('Referrer-Policy', 'no-referrer');
    }
}
