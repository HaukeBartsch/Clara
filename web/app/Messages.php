<?php
// The API's stable error code → a translated user line (User_Interface_Design.md
// §3.4, REQ-API-006/007). The API's own `message` is shown only where the
// requirements mandate it: design-time reasons the user can act on — validation
// reasons (REQ-VAL-029), a duplicate name, an attribute that failed a whitelist.
// Everything else gets fixed text so no internal detail reaches the screen, and
// 403/404 stay deliberately indistinguishable for protected resources
// (REQ-API-007).

declare(strict_types=1);

namespace Clara;

final class Messages
{
    private function __construct() {}

    /**
     * Codes that are not failures. Login branches on these before it renders
     * anything: `mfa_required` switches the page to its two-factor panel and
     * `tfa_enrollment_required` opens the enrollment wizard (§2.2).
     */
    public static function isFlowState(string $code): bool
    {
        return in_array($code, ['mfa_required', 'tfa_enrollment_required'], true);
    }

    /** The login-page failure line for an account-state code (§2.2). */
    public static function loginFailure(I18n $i18n, ApiException $e, string $ref = '', bool $isDevelopment = false): string
    {
        return match ($e->code()) {
            // Unknown account and wrong password are one line on purpose: the
            // API does not distinguish them either (Authentication §2.3 step 0).
            'account_not_found', 'bad_password' => $i18n->t('login.failure.credentials'),
            'account_disabled' => $i18n->t('login.failure.disabled'),
            'account_expired' => $i18n->t('login.failure.expired'),
            'bad_mfa_code' => $i18n->t('login.failure.mfa_code'),
            // The proof behind a challenge lapsed or was invalidated: start over,
            // and do not imply the user did anything wrong (REQ-API-131).
            'first_factor_expired' => $i18n->t('login.failure.expired_session'),
            'provider_unavailable' => $i18n->t('login.failure.provider_unavailable'),
            'email_unverified' => $i18n->t('login.failure.email_unverified'),
            'state_mismatch' => $i18n->t('login.failure.state_mismatch'),
            'rate_limited' => self::rateLimited($i18n, $e),
            default => self::generic($i18n, $e, $ref, $isDevelopment),
        };
    }

    /**
     * An error page's correlation-reference line (REQ-UI-050): what a user quotes and an
     * operator greps the log for; '' when the request carries no reference.
     */
    public static function referenceLine(I18n $i18n, string $ref): string
    {
        return $ref !== '' ? $i18n->t('error.reference', ['ref' => $ref]) : '';
    }

    /**
     * An error page's technical detail — exception class, the API's message and the failing
     * call — in development only, and '' in every other environment (REQ-UI-050, REQ-API-006).
     */
    public static function pageDetail(?ApiException $e, bool $isDevelopment): string
    {
        if ($e === null || !$isDevelopment) {
            return '';
        }

        return trim($e::class . ': ' . $e->getMessage() . ($e->endpoint() !== '' ? ' at ' . $e->endpoint() : ''));
    }

    /** 429 with the wait the API asked for (REQ-AUTH-035). */
    public static function rateLimited(I18n $i18n, ApiException $e): string
    {
        $seconds = 0;
        if (preg_match('/(\d+)/', $e->getMessage(), $m) === 1) {
            $seconds = (int) $m[1];
        }
        if ($seconds > 0) {
            return $i18n->t('error.rate_limited_seconds', ['seconds' => $seconds]);
        }

        return $i18n->t('error.rate_limited');
    }

    /**
     * The generic mapping for a failed API call on any page. Where the table
     * says "the message", the API text is appended to the translated lead so the
     * user gets both the fact and the reason.
     *
     * @param string $ref           this request's correlation reference (REQ-UI-050),
     *                              appended to the generic line; '' omits it
     * @param bool   $isDevelopment additionally render the technical detail on the
     *                              generic line — development only, never in an
     *                              environment a user can reach (REQ-API-006)
     */
    public static function forApiException(I18n $i18n, ApiException $e, string $ref = '', bool $isDevelopment = false): string
    {
        if (self::isFlowState($e->code())) {
            // Reached only if a flow state arrives outside login: say nothing
            // specific rather than render a code the user cannot interpret.
            return self::generic($i18n, $e, $ref, $isDevelopment);
        }

        // ApiException carries the code as its message when the API sent none; that is no
        // reason to show, and a raw code is never rendered (§3.4, REQ-UI-008).
        $reason = $e->getMessage() === $e->code() ? '' : $e->getMessage();
        // `no_local_credential` is a 409 that says which authentication path the account
        // actually uses — an actionable reason of exactly the kind §3.4 shows (§2.6).
        $showReason = in_array($e->code(), ['validation_error', 'conflict', 'no_local_credential'], true);
        if ($showReason && $reason === '') {
            // The reason-bearing codes still say what kind of failure it was.
            return $i18n->t($e->code() === 'validation_error' ? 'error.validation' : 'error.conflict');
        }

        $lead = match ($e->code()) {
            // The backend never answered — a transport failure, not the form's fault and
            // not the API's 500. Say which party is responsible rather than blame the page.
            'unreachable' => self::withReference($i18n, $i18n->t('error.unreachable'), $e, $ref, $isDevelopment),
            'invalid_request' => $i18n->t('error.invalid_request'),
            'validation_error' => '',
            'forbidden' => $i18n->t('error.forbidden'),
            'conflict' => '',
            'no_local_credential' => '',
            'not_found' => $i18n->t('error.not_found'),
            // A wrong current password on the self-service change form (§2.6): the one
            // line, no hint about what else the account might be.
            'bad_password' => $i18n->t('error.bad_password'),
            'rate_limited' => self::rateLimited($i18n, $e),
            default => self::generic($i18n, $e, $ref, $isDevelopment),
        };

        if ($showReason && $reason !== '') {
            return $lead === '' ? $reason : $lead . ': ' . $reason;
        }

        // invalid_request names the offending attribute when it can (§3.4).
        if ($e->code() === 'invalid_request' && $reason !== '') {
            return $lead . ': ' . $reason;
        }

        return $lead;
    }

    /**
     * The generic failure line, with the debugging affordances of REQ-UI-050:
     * always the request's correlation reference when one exists — it is what a
     * user can quote and an operator can grep for — and, in development only,
     * the technical detail the screen must otherwise never show (REQ-API-006).
     * The suffixes are not translated: the reference is a token and the detail
     * is developer text.
     */
    private static function generic(I18n $i18n, ApiException $e, string $ref, bool $isDevelopment): string
    {
        return self::withReference($i18n, $i18n->t('error.generic'), $e, $ref, $isDevelopment);
    }

    /**
     * A failure line's debugging affordances (REQ-UI-050): the correlation reference and,
     * in development only, the technical detail. Shared by the generic line and every other
     * lead that stands in for it (an unreachable backend), so both carry the same suffixes.
     */
    private static function withReference(I18n $i18n, string $text, ApiException $e, string $ref, bool $isDevelopment): string
    {
        if ($ref !== '') {
            $text .= ' ' . $i18n->t('error.reference', ['ref' => $ref]);
        }

        if ($isDevelopment) {
            $detail = implode(' ', array_filter([
                $e->code(),
                $e->status() > 0 ? (string) $e->status() : '',
                $e->endpoint(),
                $e->getMessage() === $e->code() ? '' : $e->getMessage(),
            ], static fn (string $part): bool => $part !== ''));
            $text .= ' (internal: ' . $detail . ')';
        }

        return $text;
    }
}
