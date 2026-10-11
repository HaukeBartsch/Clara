<?php
// The API refused a call because the session's epoch is no longer the account's
// (401 `session_revoked`, REQ-AUTH-074 / REQ-API-147): a password change, a disable, a
// two-factor reset or a reported replay ended every session of the account since this
// one signed in. By the time this is thrown ApiClient has already ended the session.
//
// Deliberately not an ApiException. Controllers catch those to turn a refusal into a
// translated line on the page they were rendering — the right answer for "you may not",
// the wrong one for "you are no longer signed in", which has to leave the page entirely.
// The router catches this one and sends the browser to the login page (§3.4).

declare(strict_types=1);

namespace Clara;

final class SessionRevoked extends \RuntimeException
{
}
