<?php
// The provider's back-channel logout — Sequence L2 (Authentication_Authorization_Design.md
// §2.14, REQ-AUTH-098/099, REQ-UI-088).
//
// The identity provider's server posts a signed `logout_token` here when a person signs out
// there, or is revoked. This route is not a page: no browser is ever sent here, it runs
// without a session and without a CSRF token (DEV-TECH-4) — the token is the credential, and
// the Go API verifies it against the provider's keys (REQ-API-190). What this layer does is
// pick the provider by the configured path, refuse a malformed request before calling, relay
// the token unchanged, and — when the API resolved an account — delete that account's session
// files through the session index (§3). Every answer carries Cache-Control: no-store.
//
// The reverse proxy exposes /auth/backchannel-logout/ to the provider's addresses only
// (INSTALL.md §11); this controller does not rely on that, but it is why it can be terse.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Request;
use Clara\Response;
use Clara\SessionIndex;

final class BackchannelLogoutController extends Controller
{
    /** The largest body accepted (§2.14 L2 step 1): a logout token is a few hundred bytes. */
    public const MAX_BODY_BYTES = 16384;

    /** POST /auth/backchannel-logout/{segment} */
    public function receive(): Response
    {
        $provider = $this->config->providerByBackchannelPath($this->request->path());
        if ($provider === null) {
            // A path naming no provider — or single sign-out turned off — is no route at all.
            return self::answer(Response::json(['error' => 'not_found'], 404));
        }

        $length = $this->request->header('content-length');
        if ($length !== null && (preg_match('/^\d{1,12}$/', $length) !== 1 || (int) $length > self::MAX_BODY_BYTES)) {
            return self::refuse();
        }
        $token = self::logoutToken($this->request->rawBody(self::MAX_BODY_BYTES), $this->request->field('logout_token'));
        if ($token === null) {
            return self::refuse();
        }

        try {
            $answer = $this->api->post('/api/v1/auth/oauth/backchannel-logout', [
                'provider' => $provider['index'],
                'logout_token' => $token,
            ]);
        } catch (ApiException $e) {
            $this->logger->info('provider logout refused', [
                'provider' => (string) $provider['index'],
                'reason' => $e->code(),
                'status' => (string) $e->status(),
            ]);
            // A refused token is the provider's to fix; anything else — the API unreachable, the
            // provider's keys unreadable — is worth the provider's retry, which is not a replay
            // because nothing was recorded (§2.14 L2 step 4.6).
            return $e->status() === 400
                ? self::refuse()
                : self::answer(Response::json(['error' => 'temporarily_unavailable'], 503));
        }

        // The epoch the API raised already ends each session at its next API call; removing the
        // files also ends it on a request that would make none (§2.14 L2 step 5).
        $userId = $answer['user_id'] ?? null;
        if (($answer['matched'] ?? false) === true && is_int($userId) && $userId > 0) {
            $removed = (new SessionIndex($this->config->sessionDir))->endAll($userId);
            $this->logger->info('provider logout applied', [
                'provider' => (string) $provider['index'],
                'user' => (string) $userId,
                'sessions' => (string) $removed,
            ]);
        }

        return Response::emptyBody(200);
    }

    /** Any other method on the back-channel path: 405, so a browser sent here learns nothing. */
    public function methodNotAllowed(): Response
    {
        return self::answer(Response::json(['error' => 'method_not_allowed'], 405))->withHeader('Allow', 'POST');
    }

    /**
     * The one `logout_token` of a form-encoded body, or null when there is none, it is empty,
     * or it appears more than once (§2.14 L2 step 1). PHP's $_POST would keep the last of two,
     * so the count is taken from the raw body; a body that cannot be read (a test request, a
     * SAPI that consumed it) falls back to the parsed field.
     */
    public static function logoutToken(string $rawBody, string $parsedField): ?string
    {
        if (strlen($rawBody) > self::MAX_BODY_BYTES) {
            return null;
        }
        if ($rawBody === '') {
            return $parsedField !== '' ? $parsedField : null;
        }
        $found = Request::formValues($rawBody, 'logout_token');

        return count($found) === 1 && $found[0] !== '' ? $found[0] : null;
    }

    private static function refuse(): Response
    {
        return self::answer(Response::json(['error' => 'invalid_request'], 400));
    }

    private static function answer(Response $response): Response
    {
        return $response->withHeader('Cache-Control', 'no-store');
    }
}
