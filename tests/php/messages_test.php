<?php
// Every row of User_Interface_Design.md §3.4: API code → translated line. The API's
// own message is shown only where the table says so, and 403/404 stay indistinguishable
// for protected resources (REQ-API-006, REQ-API-007).

declare(strict_types=1);

use Clara\ApiClient;
use Clara\ApiException;
use Clara\I18n;
use Clara\Logger;
use Clara\Messages;
use Clara\Request;

function translator(): I18n
{
    $config = test_config();
    $logger = new Logger('error', true);
    $request = new Request('GET', '/', [], [], []);

    return new I18n(new ApiClient($config, $logger, $request, new FakeTransport()), $logger, false);
}

function failure(string $code, string $message = '', int $status = 400): ApiException
{
    return new ApiException($code, $message, $status);
}

describe('API errors → user messages (§3.4)', function (): void {
    it('leads with the form line and appends the attribute for invalid_request', function (): void {
        $text = Messages::forApiException(translator(), failure('invalid_request', 'project_name is required'));

        assert_contains('The form contains invalid values', $text);
        assert_contains('project_name is required', $text);
    });

    it('shows a validation_error message verbatim — it is the design-time reason (REQ-VAL-029)', function (): void {
        $text = Messages::forApiException(translator(), failure('validation_error', 'field "age" has an unknown validation format'));

        assert_same('field "age" has an unknown validation format', $text);
    });

    it('says the same thing for every forbidden, whatever the API said', function (): void {
        $text = Messages::forApiException(translator(), failure('forbidden', 'project 7 is not visible to user 3', 403));

        assert_same('You do not have permission for this action', $text);
        assert_not_contains('project 7', $text, 'internals never reach the screen (REQ-API-006)');
    });

    it('keeps the conflict reason — a duplicate name is actionable', function (): void {
        $text = Messages::forApiException(translator(), failure('conflict', 'an instrument with this name exists', 409));

        assert_contains('an instrument with this name exists', $text);
    });

    it('answers not_found uniformly with forbidden', function (): void {
        assert_same(
            'The object does not exist or you do not have access',
            Messages::forApiException(translator(), failure('not_found', '', 404))
        );
    });

    it('falls back to the generic line for an internal failure', function (): void {
        $text = Messages::forApiException(translator(), failure('internal', 'sql: no such table', 500));

        assert_contains('Something went wrong', $text);
        assert_not_contains('sql', $text);
    });

    it('names the wait when the caller is rate limited (REQ-AUTH-035)', function (): void {
        $text = Messages::forApiException(translator(), failure('rate_limited', 'retry after 42 seconds', 429));

        assert_contains('42', $text);
    });

    it('treats the two-factor states as flow, not failure', function (): void {
        assert_true(Messages::isFlowState('mfa_required'));
        assert_true(Messages::isFlowState('tfa_enrollment_required'));
        assert_true(!Messages::isFlowState('bad_mfa_code'));

        // Rendered anywhere outside login, a flow state says nothing specific.
        assert_contains('Something went wrong', Messages::forApiException(translator(), failure('mfa_required', '', 401)));
    });
});

describe('login failure lines (§2.2)', function (): void {
    it('does not distinguish an unknown account from a wrong password', function (): void {
        $i18n = translator();

        assert_same(
            Messages::loginFailure($i18n, failure('bad_password')),
            Messages::loginFailure($i18n, failure('account_not_found'))
        );
    });

    it('points a disabled account at an administrator', function (): void {
        $text = Messages::loginFailure(translator(), failure('account_disabled', '', 403));

        assert_contains('disabled', $text);
        assert_contains('administrator', $text);
    });

    it('explains an expired validity period separately', function (): void {
        $text = Messages::loginFailure(translator(), failure('account_expired', '', 403));

        assert_contains('validity period', $text);
    });

    it('keeps the two-factor code line on the challenge panel', function (): void {
        assert_contains('code is wrong', Messages::loginFailure(translator(), failure('bad_mfa_code', '', 401)));
    });
});
