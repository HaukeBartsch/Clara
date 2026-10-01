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
    public static function loginFailure(I18n $i18n, ApiException $e): string
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
            'state_mismatch' => $i18n->t('login.failure.state_mismatch'),
            'rate_limited' => self::rateLimited($i18n, $e),
            default => $i18n->t('error.generic'),
        };
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
     */
    public static function forApiException(I18n $i18n, ApiException $e): string
    {
        if (self::isFlowState($e->code())) {
            // Reached only if a flow state arrives outside login: say nothing
            // specific rather than render a code the user cannot interpret.
            return $i18n->t('error.generic');
        }

        $reason = $e->getMessage();
        $showReason = in_array($e->code(), ['validation_error', 'conflict'], true);

        $lead = match ($e->code()) {
            'invalid_request' => $i18n->t('error.invalid_request'),
            'validation_error' => '',
            'forbidden' => $i18n->t('error.forbidden'),
            'conflict' => '',
            'not_found' => $i18n->t('error.not_found'),
            'rate_limited' => self::rateLimited($i18n, $e),
            default => $i18n->t('error.generic'),
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
}
