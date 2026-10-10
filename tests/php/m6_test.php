<?php
// M6 — export (§6.4), the public survey page (§8.8) and hardening (Plan/Web_Implementation.md
// §6). What review would otherwise check by hand: the export card's gate and the level it
// states, a download that streams in pieces with exactly the parameters the API reads, a
// refusal that becomes a line instead of a broken file, the survey route running outside the
// session with one state for every unusable link, and every API error code reaching the user
// as a translated line.

declare(strict_types=1);

use Clara\ApiClient;
use Clara\Controllers\ExportController;
use Clara\Controllers\SurveyController;
use Clara\I18n;
use Clara\Logger;
use Clara\Messages;
use Clara\ApiException;
use Clara\Router;
use Clara\StreamSink;

const M6_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/** A sink that keeps what a streamed response sends, for assertions. */
final class RecordingSink implements StreamSink
{
    public int $status = 0;
    /** @var array<string, string> */
    public array $headers = [];
    /** @var list<string> */
    public array $chunks = [];

    public function begin(int $status, array $headers): void
    {
        $this->status = $status;
        $this->headers = $headers;
    }

    public function write(string $chunk): void
    {
        $this->chunks[] = $chunk;
    }

    public function body(): string
    {
        return implode('', $this->chunks);
    }
}

/**
 * Project 3 with two arms; `$arms` maps arm_num => [data level, export level], and `$grants`
 * are resolved pairs as the API sends them (REQ-API-126).
 */
function m6_detail(array $arms, array $grants = []): array
{
    $armRows = [];
    $permissionArms = [];
    foreach ($arms as $num => [$data, $export]) {
        $armRows[] = ['arm_num' => $num, 'name' => $num === 2 ? 'second' : null, 'events' => [
            ['id' => $num * 10, 'event_name' => 'baseline', 'unique_event_name' => 'baseline_arm_' . $num, 'period' => 0, 'position' => 1],
        ]];
        $permissionArms[] = ['arm_num' => $num, 'data_access_level' => $data, 'export_level' => $export,
            'delete_values' => false, 'edit_surveys' => false];
    }

    return ['id' => 3, 'project_name' => '8DISC study / 2026', 'organization' => 'NAT EU', 'record_count' => 2,
        'arms' => $armRows, 'instruments' => [['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 3]],
        'permissions' => ['project_admin' => false, 'arms' => $permissionArms, 'grants' => $grants]];
}

function m6_queue(array $detail, string $mode = 'production'): void
{
    queue_shell();
    api_route('/api/v1/projects/3/mode', ['mode' => $mode, 'staging_open' => false]);
    api_route('/api/v1/projects/3/export', "record_id,age,sex\nE001,42,Female\nE002,37,Male\n", 200,
        ['Content-Type' => 'text/csv; charset=utf-8']);
    api_route('/api/v1/projects/3', $detail);
}

function m6_get(string $path, array $query = []): \Clara\Response
{
    return router_for(http_request('GET', $path, browser_headers(), [], $query))->dispatch();
}

function m6_stream(\Clara\Response $response): RecordingSink
{
    $sink = new RecordingSink();
    $response->sendTo($sink);

    return $sink;
}

/** The query of the export call, as the API parses it: repeated names kept as lists. */
function m6_export_query(): array
{
    foreach ($GLOBALS['api_calls'] as $call) {
        if (str_contains($call['url'], '/api/v1/projects/3/export')) {
            $out = [];
            foreach (explode('&', (string) parse_url($call['url'], PHP_URL_QUERY)) as $pair) {
                [$k, $v] = array_pad(explode('=', $pair, 2), 2, '');
                $out[rawurldecode($k)][] = rawurldecode($v);
            }

            return $out;
        }
    }
    throw new TestFailure('no export call');
}

/** A metadata row of the data API (API §3.4) for the survey page. */
function m6_meta(string $name, string $type, array $extra = []): array
{
    return $extra + ['field_name' => $name, 'form_name' => 'feedback', 'section_header' => '', 'field_type' => $type,
        'field_label' => ucfirst($name), 'field_note' => '', 'select_choices_or_calculations' => '', 'choice_codes' => '',
        'choice_labels' => '', 'validation_type' => '', 'validation_min' => '', 'validation_max' => '', 'required_field' => '',
        'branching_logic' => '', 'matrix_group_name' => '', 'record_identifier' => '', 'direct_identifier' => ''];
}

const M6_LINK = '/s/8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83';

describe('export page (§6.4, REQ-UI-020)', function (): void {
    it('states the most protective level of the selected arms, with one checkbox per exportable arm', function (): void {
        sign_in();
        m6_queue(m6_detail([1 => ['view_edit', 'export_full'], 2 => ['read_only', 'export_de_identified']]));

        $body = m6_get('/projects/3/export')->body();

        assert_contains('data-clara-export-form', $body);
        assert_contains('data-clara-export-level data-level="export_de_identified"', $body);
        assert_contains('De-identified', $body);
        assert_contains('name="arm[]" id="export-arm-1"', $body);
        assert_contains('data-level="export_full"', $body);
        assert_contains('name="delimiter"', $body);
        assert_contains('/assets/js/export.js', $body);
        assert_contains('href="/projects/3/export"', $body, 'the left panel carries the entry');
        assert_same(0, api_calls_to('/export'), 'rendering the page exports nothing');
    });

    it('offers no arm whose default is export_none, and hides the selector for a single arm', function (): void {
        sign_in();
        m6_queue(m6_detail([1 => ['view_edit', 'export_no_identifiers'], 2 => ['view_edit', 'export_none']]));

        $body = m6_get('/projects/3/export')->body();

        assert_not_contains('export-arm-2', $body);
        assert_contains('<input type="hidden" name="arm[]" value="1">', $body);
        assert_contains('Identifiers removed', $body);
    });

    it('refuses a member with no export level anywhere, and says so when only pair grants export', function (): void {
        sign_in();
        m6_queue(m6_detail([1 => ['view_edit', 'export_none']]));
        assert_contains('You do not have permission', m6_get('/projects/3/export')->body());

        resetApi();
        // Export rights only as a pair grant: the export endpoint reads arm defaults alone, so the
        // page states the gap instead of offering a download the API would refuse.
        m6_queue(m6_detail([1 => ['view_edit', 'export_none']], [[
            'unique_event_name' => 'baseline_arm_1', 'instrument' => 'intake', 'data_access_level' => 'view_edit',
            'export_level' => 'export_full', 'delete_values' => false, 'edit_surveys' => false]]));
        $body = m6_get('/projects/3/export')->body();
        assert_contains('clara-export-pairs-only', $body);
        assert_not_contains('data-clara-export-form', $body);
    });
});

describe('export download (REQ-TECH-011)', function (): void {
    it('streams the API body in pieces as an attachment, with the base headers', function (): void {
        sign_in();
        m6_queue(m6_detail([1 => ['view_edit', 'export_full'], 2 => ['view_edit', 'export_de_identified']]));

        $response = m6_get('/projects/3/export', ['download' => '1', 'format' => 'csv', 'arm' => ['1', '2'],
            'rawOrLabel' => 'label', 'rawOrLabelHeaders' => 'both', 'delimiter' => 'semicolon', 'smuggle' => 'x']);
        assert_true($response->isStream(), 'a download is a streamed response');
        $sink = m6_stream($response);

        assert_same(200, $sink->status);
        assert_same('text/csv; charset=UTF-8', $sink->headers['Content-Type']);
        assert_same(1, preg_match('/^attachment; filename="8DISC_study_2026_\d{4}-\d{2}-\d{2}\.csv"$/', $sink->headers['Content-Disposition']));
        assert_same('no', $sink->headers['X-Accel-Buffering']);
        assert_true(isset($sink->headers['Content-Security-Policy']), 'the base headers ride along');
        assert_same("record_id,age,sex\nE001,42,Female\nE002,37,Male\n", $sink->body());
        assert_true(count($sink->chunks) > 1, 'handed on piece by piece, not collected');

        // Exactly the parameters the API reads (§4.14): repeated arm=, the delimiter character.
        assert_same(['format' => ['csv'], 'arm' => ['1', '2'], 'rawOrLabel' => ['label'],
            'rawOrLabelHeaders' => ['both'], 'csvDelimiter' => [';']], m6_export_query());
    });

    it('sends only known values, drops arms the member may not export, and refuses an empty selection', function (): void {
        sign_in();
        m6_queue(m6_detail([1 => ['view_edit', 'export_full'], 2 => ['view_edit', 'export_none']]));
        m6_stream(m6_get('/projects/3/export', ['download' => '1', 'format' => 'xml', 'arm' => ['1', '2', '9', 'x'],
            'rawOrLabel' => 'bogus', 'delimiter' => 'comma']));
        assert_same(['format' => ['csv'], 'arm' => ['1'], 'csvDelimiter' => [',']], m6_export_query());

        resetApi();
        sign_in(['csrf_token' => M6_CSRF]);
        m6_queue(m6_detail([1 => ['view_edit', 'export_full']]));
        $response = m6_get('/projects/3/export', ['download' => '1', 'format' => 'json', 'arm' => ['9'], 'delimiter' => 'tab']);
        assert_true(!$response->isStream());
        assert_same('/projects/3/export', $response->headers()['Location']);
        assert_same(0, api_calls_to('/export'));
        assert_contains('Choose at least one arm', $_SESSION['_flash'][0]['text']);
    });

    it('turns a refusal before the first byte into a line on the export page', function (): void {
        sign_in();
        queue_shell();
        api_route('/api/v1/projects/3/mode', ['mode' => 'production', 'staging_open' => false]);
        api_route('/api/v1/projects/3/export', ['error' => 'forbidden', 'message' => 'forbidden', 'status' => 403], 403);
        api_route('/api/v1/projects/3', m6_detail([1 => ['view_edit', 'export_full']]));

        $sink = m6_stream(m6_get('/projects/3/export', ['download' => '1', 'arm' => ['1']]));

        assert_same(303, $sink->status);
        assert_same('/projects/3/export', $sink->headers['Location']);
        assert_same('', $sink->body(), 'no partial file');
        assert_contains('You do not have permission', $_SESSION['_flash'][0]['text']);
    });

    it('names the file safely and repeats list parameters the way the API reads them', function (): void {
        assert_same(1, preg_match('/^export_\d{4}-\d{2}-\d{2}\.json$/', ExportController::filename('../..', 'json')));
        assert_same('arm=1&arm=3&format=csv', ApiClient::queryString(['arm' => [1, 3], 'format' => 'csv']));
        assert_same('export_de_identified', ExportController::appliedLevel([
            ['level' => 'export_full'], ['level' => 'export_de_identified'], ['level' => 'export_no_identifiers']]));
    });
});

describe('public survey page (§8.8, REQ-UI-028)', function (): void {
    it('runs outside the session, and nothing else does (GD-1)', function (): void {
        assert_true(!Router::needsSession(http_request('GET', M6_LINK)));
        assert_true(Router::needsSession(http_request('GET', '/login')));
        assert_true(Router::needsSession(http_request('GET', '/projects/3/export')));
        assert_true(Router::needsSession(http_request('GET', '/no/such/page')), 'a 404 renders like any page');
        // Submitting is sessionless too, and needs no CSRF token for it (§8.8).
        assert_true(!Router::needsSession(http_request('POST', M6_LINK)));
    });

    it('renders the link instrument with the shared form markup, without identifier but with a submit control', function (): void {
        queue_shell();
        api_route('data:content=metadata', [
            m6_meta('record_id', 'text', ['form_name' => 'intake', 'record_identifier' => 'Y']),
            m6_meta('happy', 'radio', ['select_choices_or_calculations' => '1, Yes | 2, No, not really', 'required_field' => 'Y']),
            m6_meta('why', 'text', ['branching_logic' => '[baseline_arm_1][happy] = "2"']),
            m6_meta('age', 'text', ['validation_type' => 'integer', 'validation_min' => '0']),
        ]);

        $response = m6_get(M6_LINK);
        $body = $response->body();

        assert_same(200, $response->status());
        assert_contains('data-clara-survey-form', $body);
        assert_contains('No, not really', $body, 'a label may contain a comma');
        assert_same(1, preg_match('/name="value\[happy\]"\s+value="2"/', $body), 'the radio carries the choice code');
        assert_contains('data-clara-branching="[baseline_arm_1][happy] = &quot;2&quot;"', $body);
        assert_contains('data-clara-required="1"', $body);
        assert_contains('"anyEvent":true', $body);
        assert_contains('/assets/js/survey.js', $body);
        assert_not_contains('data-clara-field="record_id"', $body, 'the identifier is the study\'s, not shown');
        assert_contains('type="submit"', $body, 'REQ-UI-028 asks the page for a submit action');
        assert_contains('name="tz"', $body, 'the collection zone of GD-16 rides with the form');
        assert_not_contains('clara-survey-closed', $body, 'a survey that can be answered is not closed');
        assert_not_contains('csrf', $body);
        assert_not_contains('Sign out', $body, 'standalone panel, no account footer');

        $sent = data_api_body('content=metadata');
        assert_same('8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83', $sent['token']);
        foreach (api_headers_for('8080/api/') as $header) {
            assert_true(!str_starts_with($header, 'X-Internal-'), 'the link token is the only credential');
        }
    });

    it('answers every unusable link with the same state and no detail (REQ-AUTH-040)', function (): void {
        foreach ([[401, 'Invalid token'], [403, 'Permission denied']] as [$status, $error]) {
            resetApi();
            queue_shell();
            api_route('data:content=metadata', ['error' => $error], $status);
            $response = m6_get(M6_LINK);
            assert_same(404, $response->status());
            assert_contains('no longer valid', $response->body());
            assert_not_contains($error, $response->body());
        }

        resetApi();
        queue_shell();
        $response = m6_get('/s/not a token');
        assert_same(404, $response->status());
        assert_same(0, data_api_calls('content='), 'a malformed token is never sent');

        resetApi();
        queue_shell();
        api_route('data:content=metadata', ['error' => 'Rate limit exceeded'], 429);
        assert_same(429, m6_get(M6_LINK)->status());
    });

    it('answers a spent link with "already submitted", which is not the broken-link state (§8.8, REQ-API-145)', function (): void {
        resetApi();
        queue_shell();
        api_route('data:content=metadata', ['error' => 'Survey already submitted'], 410);

        $response = m6_get(M6_LINK);
        assert_same(410, $response->status(), 'the status the API gave is the status the respondent gets');
        $body = $response->body();
        assert_contains('already submitted', $body);
        assert_not_contains('no longer valid', $body, 'a finished survey must not read as a broken link');
        assert_not_contains('data-clara-survey-form', $body, 'there is no second form on this link');

        // Spent between opening the page and sending: two tabs, or a back-button resend. The same
        // state, because the answer did arrive either way.
        resetApi();
        queue_shell();
        api_route('data:action=import', ['error' => 'Survey already submitted'], 410);
        $again = router_for(http_request('POST', M6_LINK, browser_headers(),
            ['value' => ['age' => '1'], 'was' => []]))->dispatch();
        assert_same(410, $again->status());
        assert_contains('already submitted', $again->body());
    });

    it('reads the combined choices column into the stored encoding', function (): void {
        assert_same('1$Yes##2$No, not really##3$3', SurveyController::choicesFromCombined('1, Yes | 2, No, not really | 3'));
        assert_same('', SurveyController::choicesFromCombined(''));
    });

    it('sends the answers alone and ends on the closing panel (§8.8, REQ-API-083)', function (): void {
        resetApi();
        queue_shell();
        api_route('data:action=import', [['record_id' => 'S001', 'form_name' => 'intake',
            'import_record_id' => 2, 'import_form_name' => 'intake']]);

        $response = router_for(http_request('POST', M6_LINK, browser_headers(), [
            // `age` is untouched: entered values plus explicitly cleared ones only (GD-14).
            'value' => ['happy' => '2', 'why' => 'the coffee', 'age' => ''],
            'was' => ['age' => ''],
            'tz' => 'Europe/Oslo',
        ]))->dispatch();

        assert_same(200, $response->status());
        assert_contains('clara-survey-done', $response->body());
        assert_not_contains('data-clara-survey-form', $response->body(), 'the visit ends here');
        // The closing panel is where the one-submission rule is told, not a footnote (REQ-UI-028).
        assert_contains('cannot be used again', $response->body());

        $sent = data_api_body('action=import');
        assert_same('8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83', $sent['token'], 'the link token is the credential');
        // The row names no target at all: record, instrument and event come from the link, so
        // the study's record identifier never reaches the respondent (§3.10, REQ-AUTH-039).
        $row = $sent['data'][0];
        foreach (['record_id', 'form_name', 'event_name', 'redcap_event_name'] as $absent) {
            assert_true(!array_key_exists($absent, $row), 'the row must not name ' . $absent);
        }
        assert_same('2', $row['happy']);
        assert_same('the coffee', $row['why']);
        assert_true(!array_key_exists('age', $row), 'an untouched field is not sent (REQ-UI-031)');
        assert_same('Europe/Oslo', $sent['tz'], 'the browser zone is the collection zone (GD-16)');
    });

    it('re-shows the form with the API reasons when a value is refused (REQ-API-035)', function (): void {
        resetApi();
        queue_shell();
        api_route('data:action=import', [['record_id' => 'S001', 'form_name' => '',
            'import_record_id' => 0,
            'import_form_name' => 'Validation error: age: CONTENT_INVALID — not an integer']]);
        api_route('data:content=metadata', [m6_meta('age', 'text', ['validation_type' => 'integer'])]);

        $response = router_for(http_request('POST', M6_LINK, browser_headers(), [
            'value' => ['age' => 'forty-one'], 'was' => ['age' => ''],
        ]))->dispatch();

        assert_same(200, $response->status());
        $body = $response->body();
        assert_contains('data-clara-survey-form', $body, 'the respondent gets the form back');
        assert_contains('not an integer', $body, 'the API reason, against its field');
        assert_contains('value="forty-one"', $body, 'their own value comes back, not a blank form');
        assert_contains('Nothing was saved', $body);
    });

    it('states a refusal no field input can carry, rather than promising reasons it hides', function (): void {
        resetApi();
        queue_shell();
        api_route('data:action=import', [['record_id' => 'S001', 'form_name' => '',
            'import_record_id' => 0,
            'import_form_name' => 'Validation error: event_name: CONTENT_INVALID — unknown event']]);
        api_route('data:content=metadata', [m6_meta('age', 'text')]);

        $body = router_for(http_request('POST', M6_LINK, browser_headers(),
            ['value' => ['age' => '1'], 'was' => []]))->dispatch()->body();

        assert_contains('unknown event', $body, 'the reason is on screen, not only in the log');
    });

    it('closes the survey in analysis mode and answers a revoked link with the one state (§8.8)', function (): void {
        resetApi();
        queue_shell();
        api_route('data:action=import', ['error' => 'Project in analysis mode'], 403);

        $response = router_for(http_request('POST', M6_LINK, browser_headers(),
            ['value' => ['age' => '1'], 'was' => []]))->dispatch();

        assert_same(403, $response->status());
        assert_contains('no longer accepts responses', $response->body());
        assert_not_contains('analysis mode', $response->body(), 'the project mode is not the respondent\'s business');

        // Revoked between opening the link and submitting: the same one state (REQ-AUTH-040).
        resetApi();
        queue_shell();
        api_route('data:action=import', ['error' => 'Permission denied'], 403);
        $revoked = router_for(http_request('POST', M6_LINK, browser_headers(),
            ['value' => ['age' => '1'], 'was' => []]))->dispatch();
        assert_same(404, $revoked->status());
        assert_contains('no longer valid', $revoked->body());
    });
});

describe('error mapping (§3.4)', function (): void {
    it('turns every code the API emits into a translated line, never the code itself', function (): void {
        $i18n = new I18n(new ApiClient(test_config(), new Logger('error', true), http_request('GET', '/'), new FakeTransport()), new Logger('error', true), true);
        $codes = ['account_disabled', 'account_expired', 'account_not_found', 'bad_mfa_code', 'bad_password',
            'code_send_limited', 'conflict', 'first_factor_expired', 'forbidden', 'internal', 'invalid_request',
            'invalid_setup_token', 'mfa_required', 'no_local_credential', 'not_found', 'provider_unavailable',
            'rate_limited', 'service_token_invalid', 'smtp_not_configured', 'smtp_send_failed',
            'tfa_enrollment_required', 'validation_error'];
        queue_shell();
        foreach ($codes as $code) {
            $line = Messages::forApiException($i18n, new ApiException($code, '', 400));
            // A sentence, never the bare code or a catalog key (§3.4, REQ-UI-008).
            assert_true($line !== $code && preg_match('/^[a-z0-9_.]*$/', $line) !== 1 && str_contains($line, ' '),
                "{$code} maps to a line, got " . var_export($line, true));
        }
    });
});
