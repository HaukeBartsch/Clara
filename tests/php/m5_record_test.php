<?php
// M5 — the Record Status Dashboard (§6.3) and data entry (§8) (Plan/Web_Implementation.md §6).
// What review would otherwise check by hand: the data-access gate, the dashboard region
// carrying states and never values, the new-participant affordance by level and mode, the
// prefill walk (newest first, stopping early), the submission policy of GD-14 on the wire,
// the token cache with its single 401 retry (§8.6), the completion call only on a change
// (§8.5), the read-only and analysis-mode renders (REQ-UI-003/035), and the scoped delete.

declare(strict_types=1);

use Clara\DataEntry;

const M5_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

/**
 * The project read with its permissions block as the API sends it (REQ-API-126): the arm
 * default plus every mapped (instrument, event) pair resolved — here all at `$level`, with the
 * two rights (delete instrument values, edit collected surveys) when `$rights` (REQ-AUTH-069/070).
 */
function m5_detail(string $level = 'view_edit', bool $projectAdmin = false, bool $rights = false): array
{
    $grants = [];
    foreach ([['baseline_arm_1', 'intake'], ['follow_up_arm_1', 'intake'], ['baseline_arm_1', 'scores']] as [$event, $instrument]) {
        $grants[] = ['unique_event_name' => $event, 'instrument' => $instrument, 'data_access_level' => $level,
            'export_level' => 'export_none', 'delete_values' => $rights, 'edit_surveys' => $rights];
    }

    return [
        'id' => 3, 'project_name' => '8DISC', 'organization' => 'NAT EU', 'record_count' => 1,
        'arms' => [['arm_num' => 1, 'name' => null, 'events' => [
            ['id' => 4, 'event_name' => 'baseline', 'unique_event_name' => 'baseline_arm_1', 'period' => 0, 'position' => 1],
            ['id' => 8, 'event_name' => 'follow up', 'unique_event_name' => 'follow_up_arm_1', 'period' => 30, 'position' => 2],
        ]]],
        'instruments' => [
            ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 6],
            ['id' => 12, 'name' => 'scores', 'position' => 2, 'field_count' => 1],
        ],
        'permissions' => ['project_admin' => $projectAdmin,
            'arms' => [['arm_num' => 1, 'data_access_level' => $level, 'export_level' => 'export_none',
                'delete_values' => $rights, 'edit_surveys' => $rights]],
            'grants' => $grants],
    ];
}

function m5_field(int $id, string $name, string $type, array $extra = []): array
{
    return $extra + [
        'id' => $id, 'field_name' => $name, 'field_label' => ucfirst($name), 'field_type' => $type,
        'section_header' => '', 'choices' => '', 'field_note' => '', 'validation_type' => '',
        'validation_format' => null, 'validation_min' => null, 'validation_max' => null,
        'required' => false, 'branching_logic' => '', 'calculation' => '', 'matrix_group' => '',
        'personal_information' => false, 'direct_identifier' => false, 'position' => $id,
    ];
}

function m5_fields(): array
{
    return [
        m5_field(1, 'record_id', 'text'),
        m5_field(2, 'age', 'text', ['validation_type' => 'integer', 'validation_min' => '0', 'validation_max' => '120', 'required' => true]),
        m5_field(3, 'sex', 'radio', ['choices' => '1$Male##2$Female']),
        m5_field(4, 'preg', 'dropdown', ['choices' => '0$No##1$Yes', 'branching_logic' => '[sex] = "2" && [follow_up_arm_1][visit] = "1"']),
        m5_field(5, 'notes', 'text'),
        m5_field(6, 'dbl', 'calculated', ['calculation' => '[baseline_arm_1][age] * 2']),
    ];
}

/** A history page in the §4.16 shape, newest first. */
function m5_history(array $entries, ?string $next = null): array
{
    return ['entries' => $entries, 'next_cursor' => $next];
}

function m5_entry(string $action, string $event, array $fields, string $at = '2026-10-01 10:00:00'): array
{
    $out = [];
    foreach ($fields as $field => [$old, $new]) {
        $out[] = ['field' => $field, 'old' => $old, 'new' => $new];
    }

    return ['created_at' => $at, 'user_id' => 7, 'user_display_name' => 'Test <Member>', 'action' => $action,
        'instrument' => 'intake', 'event' => $event, 'fields' => $out];
}

/** Every read the two pages make, in fake-transport match order (specific before prefix). */
function m5_queue(string $level = 'view_edit', string $mode = 'development', bool $projectAdmin = false, ?array $status = null, ?array $history = null, bool $rights = false, ?array $link = null): void
{
    queue_shell();
    api_route('/api/v1/projects/3/mode', ['mode' => $mode, 'staging_open' => false]);
    api_route('/records/8DISC001/history', $history ?? m5_history([
        m5_entry('update', 'baseline_arm_1', ['age' => ['41', '42'], 'notes' => ['', '<b>ok</b>']]),
        m5_entry('create', 'baseline_arm_1', ['record_id' => [null, '8DISC001'], 'age' => [null, '41'], 'sex' => [null, '2']]),
        m5_entry('create', 'follow_up_arm_1', ['visit' => [null, '1']]),
    ]));
    api_route('/api/v1/projects/3/record-status', $status ?? [
        ['record_id' => '8DISC001', 'events' => [
            ['unique_event_name' => 'baseline_arm_1', 'instruments' => [['name' => 'intake', 'state' => 'some_data'], ['name' => 'scores', 'state' => 'finished']]],
            ['unique_event_name' => 'follow_up_arm_1', 'instruments' => [['name' => 'intake', 'state' => 'mystery']]],
        ]],
    ]);
    api_route('/api/v1/projects/3/instrument-event-mapping', [['arm_num' => 1, 'mapping' => [
        'intake' => ['baseline_arm_1', 'follow_up_arm_1'], 'scores' => ['baseline_arm_1']]]]);
    api_route('/api/v1/projects/3/instruments/7/fields', m5_fields());
    api_route('/api/v1/projects/3/instruments/12/fields', [m5_field(9, 'score', 'text')]);
    api_route('/api/v1/projects/3/instruments', [
        ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 6, 'is_survey' => false, 'branching_logic' => ''],
        ['id' => 12, 'name' => 'scores', 'position' => 2, 'field_count' => 1, 'is_survey' => true, 'branching_logic' => '[baseline_arm_1][age] > 18'],
    ]);
    api_route('/api/v1/projects/3/data-access-groups', [['id' => 5, 'name' => 'Center <A>']]);
    api_route('/api/v1/projects/3/users/7/token', ['token' => 'tok-1']);
    // The link report a survey pair's view reads on every render (§4.17, REQ-API-082): live by
    // default, so a test passes its own `state` when it needs another. Queued before the bare
    // project prefix below, which would otherwise answer it with a project detail.
    api_route('/survey-link', $link ?? ['state' => 'live', 'event' => 'baseline_arm_1',
        'url' => 'http://localhost:8000/s/link-token', 'collected_at' => null]);
    api_route('/api/v1/projects/3', m5_detail($level, $projectAdmin, $rights));
}

function m5_get(string $path, array $query = [], ?array $headers = null): \Clara\Response
{
    return router_for(http_request('GET', $path, $headers ?? browser_headers(), [], $query))->dispatch();
}

function m5_post(string $path, array $fields, string $action): \Clara\Response
{
    return router_for(http_request('POST', $path, browser_headers(), ['csrf_token' => M5_CSRF] + $fields, ['action' => $action]))->dispatch();
}

/** The fields a save of intake at baseline posts, as the rendered form carries them. */
function m5_save_fields(array $values, array $was, array $extra = []): array
{
    return $extra + ['event' => 'baseline_arm_1', 'instrument' => 'intake', 'iid' => '7', 'tz' => 'Europe/Oslo',
        'value' => $values, 'was' => $was, 'completion' => 'some_data', 'completion_was' => 'some_data'];
}

function m5_count(string $method, string $fragment): int
{
    return count(array_filter($GLOBALS['api_calls'], static fn (array $c): bool =>
        $c['method'] === $method && str_contains($c['url'], $fragment)));
}

describe('M5 data-entry rules (DataEntry)', function (): void {
    it('derives current values newest-first and stops once every wanted value is settled (§8.3)', function (): void {
        $pages = [
            null => m5_history([m5_entry('update', 'e1', ['a' => ['1', '2']]), m5_entry('delete', 'e1', ['b' => ['x', null]])], 'c1'),
            'c1' => m5_history([m5_entry('create', 'e1', ['a' => [null, '1'], 'b' => [null, 'x'], 'c' => [null, 'y']])], 'c2'),
            'c2' => m5_history([m5_entry('create', 'e1', ['d' => [null, 'never read']])]),
        ];
        $fetched = [];
        $walk = DataEntry::currentValues(function (?string $cursor) use ($pages, &$fetched): array {
            $fetched[] = $cursor;

            return $pages[$cursor ?? ''];
        }, ['e1|a', 'e1|b', 'e1|c']);

        // a: the newest value; b: deleted after it was set, so no value; c: from page two.
        assert_same(['e1|a' => '2', 'e1|c' => 'y'], $walk['values']);
        assert_same([null, 'c1'], $fetched, 'the third page is never read');
        assert_true(!$walk['truncated']);
    });

    it('stops at the page ceiling and says so', function (): void {
        $walk = DataEntry::currentValues(static fn (?string $c): array => m5_history([], 'more'), ['e|x'], 3);
        assert_true($walk['truncated']);
        assert_same([], $walk['values']);
        // Nothing wanted, nothing read.
        $calls = 0;
        DataEntry::currentValues(function () use (&$calls): array { $calls++; return m5_history([]); }, []);
        assert_same(0, $calls);
    });

    it('sends values and explicit clears, never untouched empties (GD-14, REQ-UI-031)', function (): void {
        $sent = DataEntry::submission(
            ['age' => '42', 'notes' => '', 'sex' => '', 'record_id' => 'evil', 'BAD-NAME' => '1'],
            ['age' => '41', 'notes' => 'old', 'sex' => '', 'preg' => '1']
        );
        // age carries a value; notes and preg were stored and removed; sex never had one.
        assert_same(['age' => '42', 'notes' => '', 'preg' => ''], $sent);
    });

    it('splits a rejected row into one line per field (§3.7.2)', function (): void {
        $errors = DataEntry::importErrors('Validation error: age: TYPE_INVALID — value "abc" is not a valid integer; '
            . 'sex: CHOICE_INVALID — value "9" is not a choice; event_name CONTENT_INVALID odd');
        assert_same('TYPE_INVALID — value "abc" is not a valid integer', $errors['age']);
        assert_same('CHOICE_INVALID — value "9" is not a choice', $errors['sex']);
        assert_same('event_name CONTENT_INVALID odd', $errors['']);
    });

    it('collects branching references, resolving [field] to the first event (§7.1)', function (): void {
        assert_same(['first|sex', 'e2|visit'], DataEntry::references('[sex] = "[not|a ref]" && [e2][visit] = "1" || [sex] = "2"', 'first'));
    });

    it('groups consecutive matrix fields into one item and keeps section headers', function (): void {
        $items = DataEntry::layout([
            m5_field(1, 'a', 'text', ['section_header' => 'Part 1']),
            m5_field(2, 'm1', 'matrix', ['matrix_group' => 'g', 'choices' => '1$Low##2$High']),
            m5_field(3, 'm2', 'matrix', ['matrix_group' => 'g', 'choices' => '1$Low##2$High']),
            m5_field(4, 'b', 'text'),
        ]);
        assert_same(['field', 'matrix', 'field'], array_column($items, 'kind'));
        assert_same('Part 1', $items[0]['section']);
        assert_same(['m1', 'm2'], array_column($items[1]['fields'], 'field_name'));
        assert_same([['code' => '1', 'label' => 'Low'], ['code' => '2', 'label' => 'High']], $items[1]['choices']);
    });

    it('accepts sensible record names and timezones only', function (): void {
        assert_true(DataEntry::validRecordName('8DISC042'));
        assert_true(!DataEntry::validRecordName(''));
        assert_true(!DataEntry::validRecordName('a/b'));
        assert_true(!DataEntry::validRecordName(' padded'));
        assert_true(DataEntry::validTimezone('Europe/Oslo'));
        assert_true(DataEntry::validTimezone('+01:00'));
        assert_true(!DataEntry::validTimezone("Europe/Oslo\r\nX: y"));
    });
});

describe('Record Status Dashboard (§6.3, REQ-UI-019)', function (): void {
    it('renders the arm grid structure, the legend and the region container', function (): void {
        sign_in();
        m5_queue();

        $body = m5_get('/projects/3/record-status')->body();

        assert_contains('data-region="records"', $body);
        assert_contains('data-col="baseline_arm_1|intake"', $body);
        assert_contains('data-col="baseline_arm_1|scores"', $body);
        assert_contains('data-col="follow_up_arm_1|intake"', $body);
        assert_not_contains('follow_up_arm_1|scores', $body, 'an unmapped pair has no column');
        assert_contains('/assets/js/record-status.js', $body);
        assert_contains('clara-state-finished', $body);
        // The entry is now built and allowed, and it is the page on screen.
        assert_contains('href="/projects/3/record-status"', $body);
        // The page itself reads no record: the rows are the data region (REQ-UI-044).
        assert_same(0, api_calls_to('/record-status'));
    });

    it('offers a new participant at view_edit, not at read_only, and never in analysis mode', function (): void {
        sign_in();
        m5_queue();
        assert_contains('action=new_record', m5_get('/projects/3/record-status')->body());

        resetApi();
        m5_queue('read_only');
        assert_not_contains('action=new_record', m5_get('/projects/3/record-status')->body());

        resetApi();
        m5_queue('view_edit', 'analysis', rights: true);
        assert_not_contains('action=new_record', m5_get('/projects/3/record-status')->body());
    });

    it('refuses a member without data access, before any data read', function (): void {
        sign_in();
        m5_queue('no_access');

        $body = m5_get('/projects/3/record-status')->body();

        assert_contains('You do not have permission', $body);
        assert_same(0, api_calls_to('/instrument-event-mapping'));
    });

    it('serves the rows as cells keyed by event and instrument, and no values (REQ-API-074)', function (): void {
        sign_in();
        m5_queue();

        $response = m5_get('/projects/3/record-status', [], json_headers());

        assert_same(200, $response->status());
        $payload = json_decode($response->body(), true);
        assert_same([['record_id' => '8DISC001', 'cells' => [
            'baseline_arm_1|intake' => 'some_data',
            'baseline_arm_1|scores' => 'finished',
            'follow_up_arm_1|intake' => 'no_data', // an unknown state never reaches the client
        ]]], $payload['records']);
    });

    it('opens the record view on a valid new name and keeps an invalid one in the field', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        queue_shell();

        $ok = m5_post('/projects/3/record-status', ['record_id' => '8DISC 9', 'arm' => '1'], 'new_record');
        assert_same('/projects/3/records/8DISC%209?arm=1', $ok->headers()['Location']);

        $bad = m5_post('/projects/3/record-status', ['record_id' => 'a/b', 'arm' => '1'], 'new_record');
        assert_same('/projects/3/record-status', $bad->headers()['Location']);
        assert_same('a/b', $_SESSION['_stash']['new_record']['name']);
        assert_same(0, count($GLOBALS['api_calls']) - api_calls_to('/i18n/'), 'nothing is stored by opening a participant');
    });

    it('auto-names through the data API with the member token, cached for the session (§8.6)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:content=generateNextRecordName', [['next_record_name' => '8DISC002']]);

        m5_post('/projects/3/record-status', ['arm' => '1'], 'auto_name');
        m5_post('/projects/3/record-status', ['arm' => '1'], 'auto_name');

        assert_same(1, api_calls_to('/users/7/token'), 'the token is fetched once and cached');
        assert_same(2, data_api_calls('content=generateNextRecordName'));
        $sent = data_api_body('content=generateNextRecordName');
        assert_same('tok-1', $sent['token']);
        assert_same('json', $sent['format']);
        // The token travels in the body only — never in a header (REQ-API-010).
        foreach ($GLOBALS['api_calls'] as $call) {
            if (str_ends_with($call['url'], '/api/')) {
                assert_not_contains('tok-1', implode("\n", $call['headers']));
            }
        }

        // The proposed name fills the field on the next render.
        resetApi();
        m5_queue();
        assert_contains('value="8DISC002"', m5_get('/projects/3/record-status')->body());
    });
});

describe('record view (§8, REQ-UI-025…027)', function (): void {
    it('renders the form prefilled from the history, read newest-first (§8.3)', function (): void {
        sign_in();
        m5_queue();

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_contains('name="value[age]" value="42"', $body);
        assert_contains('name="was[age]" value="42"', $body);
        assert_contains('value="2" checked', $body, 'the radio carries its stored code');
        // The free-text value is the user's to edit: in the input it is escaped, not markup.
        assert_contains('name="value[notes]" value="&lt;b&gt;ok&lt;/b&gt;"', $body);
        // The identifier is shown, never posted (GD-8).
        assert_not_contains('name="value[record_id]"', $body);
        assert_not_contains('name="value[dbl]"', $body, 'a calculated field is never an input');
        assert_contains('clara-required', $body);
        assert_contains('name="completion"', $body);
        assert_contains('<option value="some_data" selected', $body);
        // The out-of-form reference of the branching logic travels to the evaluator.
        assert_contains('"follow_up_arm_1|visit":"1"', $body);
        assert_contains('data-clara-branching="[sex] = &quot;2&quot;', $body);

        $history = array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], '/history')));
        assert_same(1, count($history));
        assert_contains('order=newest', $history[0]['url']);
        assert_contains('limit=200', $history[0]['url']);
    });

    it('shows a read_only member the values and no submit control (REQ-UI-003)', function (): void {
        sign_in();
        m5_queue('read_only');

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_not_contains('data-clara-save', $body);
        assert_not_contains('name="value[', $body);
        assert_not_contains('action=delete', $body);
        // Stored free text is allowlist HTML and renders as such (§3.2); a choice shows its label.
        assert_contains('<b>ok</b>', $body);
        assert_contains('Female', $body);
    });

    it('closes data entry in analysis mode, project_admin included (REQ-UI-035)', function (): void {
        sign_in();
        m5_queue('view_edit', 'analysis', true, rights: true);

        $body = m5_get('/projects/3/records/8DISC001')->body();

        assert_contains('clara-analysis-closed', $body);
        assert_not_contains('data-clara-save', $body);
        assert_not_contains('action=delete', $body);
    });

    it('opens a new participant without reading any history', function (): void {
        sign_in();
        m5_queue();

        $body = m5_get('/projects/3/records/8DISC009', ['arm' => '1'])->body();

        assert_contains('clara-record-new', $body);
        assert_contains('name="value[age]" value=""', $body);
        assert_same(0, api_calls_to('/history'));
        assert_not_contains('data-clara-history=', $body, 'no history to show yet');
    });

    it('offers the record actions by level: delete, group to project_admin, survey link', function (): void {
        sign_in();
        m5_queue('view_edit', 'development', true, rights: true);

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'scores'])->body();

        assert_contains('data-scope="instrument"', $body);
        assert_contains('data-scope="record"', $body);
        assert_contains('action=assign_group', $body);
        assert_contains('Center &lt;A&gt;', $body);
        assert_contains('action=survey_link', $body);
        // A survey instrument's completion is automatic: shown, not offered (§8.5, GD-9).
        assert_not_contains('name="completion"', $body);

        resetApi();
        m5_queue('view_edit');
        $member = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();
        assert_not_contains('action=delete', $member);
        assert_not_contains('action=assign_group', $member);
        assert_not_contains('action=survey_link', $member, 'not a survey instrument');
    });

    it('serves the per-field history as its data region, values as data (REQ-UI-044)', function (): void {
        sign_in();
        m5_queue();

        $response = m5_get('/projects/3/records/8DISC001', ['field' => 'age', 'event' => 'baseline_arm_1'], json_headers());

        $payload = json_decode($response->body(), true);
        assert_same(2, count($payload['entries']));
        assert_same(['created_at' => '2026-10-01 10:00:00', 'user' => 'Test <Member>', 'action' => 'update',
            'event' => 'baseline_arm_1', 'field' => 'age', 'old' => '41', 'new' => '42'], $payload['entries'][0]);
        assert_contains('field=age', $GLOBALS['api_calls'][count($GLOBALS['api_calls']) - 1]['url']);

        $bad = m5_get('/projects/3/records/8DISC001', ['field' => 'x;drop'], json_headers());
        assert_same(400, $bad->status());
    });
});

describe('saving and deleting (§8.6, §8.7)', function (): void {
    it('imports exactly the entered and the cleared values, with the browser zone (GD-14, GD-16)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 2, 'import_form_name' => 'intake']]);

        $response = m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['age' => '43', 'notes' => '', 'preg' => ''], ['age' => '42', 'notes' => '<b>ok</b>', 'sex' => '2', 'preg' => '']), 'save');

        assert_same('/projects/3/records/8DISC001?event=baseline_arm_1&instrument=intake', $response->headers()['Location']);
        $sent = data_api_body('action=import');
        assert_same(['record_id' => '8DISC001', 'form_name' => 'intake', 'event_name' => 'baseline_arm_1',
            'age' => '43', 'notes' => '', 'sex' => ''], $sent['data'][0]);
        assert_same('Europe/Oslo', $sent['tz']);
        assert_same('record', $sent['content']);
        // The completion dropdown did not change: no assignment call (§8.5).
        assert_same(0, m5_count('PUT', '/completion'));
        assert_contains('saved', $_SESSION['_flash'][0]['text']);
    });

    it('sets "finished" after the import, and clears it only when leaving finished (§8.5)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'import_record_id' => 2, 'import_form_name' => 'intake']]);
        api_route('/completion', ['state' => 'finished']);

        m5_post('/projects/3/records/8DISC001', m5_save_fields([], [], ['completion' => 'finished']), 'save');
        assert_same(['state' => 'finished'], m4_body('PUT', '/events/baseline_arm_1/instruments/7/completion'));

        m5_post('/projects/3/records/8DISC001', m5_save_fields([], [], ['completion' => 'no_data', 'completion_was' => 'finished']), 'save');
        assert_same(['state' => 'unfinished'], m4_body('PUT', '/events/baseline_arm_1/instruments/7/completion'));

        m5_post('/projects/3/records/8DISC001', m5_save_fields([], [], ['completion' => 'no_data', 'completion_was' => 'some_data']), 'save');
        assert_same(2, m5_count('PUT', '/completion'), 'moving between the derived states is no assignment');
    });

    it('re-renders a rejected row with the reasons beside the fields and nothing else changed', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 0,
            'import_form_name' => 'Validation error: age: TYPE_INVALID — value \'abc\' is not a valid integer']]);

        $response = m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['age' => 'abc'], ['age' => '42'], ['completion' => 'finished']), 'save');

        assert_same(200, $response->status());
        assert_contains('TYPE_INVALID — value &#039;abc&#039; is not a valid integer', $response->body());
        assert_contains('name="value[age]" value="abc"', $response->body(), 'the user keeps what they typed');
        assert_contains('name="was[age]" value="42"', $response->body(), 'the baseline stays the stored value');
        assert_contains('Nothing was saved', $response->body());
        assert_same(0, m5_count('PUT', '/completion'));
        assert_true(!isset($_SESSION['_stash']), 'no record value outlives the request');
    });

    it('discards a refused token, fetches it again and retries exactly once (§8.6 step 3)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        $_SESSION['proj_token_3'] = 'stale';
        m5_queue();
        api_route('data:token=stale', ['error' => 'Invalid token'], 401);
        api_route('data:token=tok-1', [['record_id' => '8DISC001', 'import_record_id' => 2, 'import_form_name' => 'intake']]);

        m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => '43'], ['age' => '42']), 'save');

        assert_same(1, api_calls_to('/users/7/token'));
        assert_same(1, data_api_calls('token=stale'));
        assert_same(1, data_api_calls('token=tok-1'));
        assert_same('tok-1', $_SESSION['proj_token_3']);
        assert_same('success', $_SESSION['_flash'][0]['level']);
    });

    it('never retries a permission refusal, and names analysis mode and non-membership', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', ['error' => 'Permission denied'], 403);
        m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => '43'], []), 'save');
        assert_same(1, data_api_calls('action=import'));
        assert_contains('You do not have permission', $_SESSION['_flash'][0]['text']);

        sign_in(['csrf_token' => M5_CSRF]);
        resetApi();
        m5_queue();
        api_route('data:action=import', ['error' => 'Project in analysis mode'], 403);
        m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => '43'], []), 'save');
        assert_contains('analysis mode', $_SESSION['_flash'][0]['text']);

        sign_in(['csrf_token' => M5_CSRF, 'is_admin' => 1]);
        resetApi();
        queue_shell();
        api_route('/users/7/token', ['error' => 'forbidden', 'message' => '', 'status' => 403], 403);
        m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => '43'], []), 'save');
        assert_contains('not a member of this project', $_SESSION['_flash'][0]['text']);
        assert_same(0, data_api_calls('action=import'));
    });

    it('deletes one instrument\'s values but keeps the record identifier (§8.7)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue('view_edit', rights: true);
        api_route('data:action=delete', [['record_id' => '8DISC001', 'form_name' => '', 'deleted' => 3]]);

        $response = m5_post('/projects/3/records/8DISC001',
            ['scope' => 'instrument', 'event' => 'baseline_arm_1', 'instrument' => 'intake', 'iid' => '7'], 'delete');

        $sent = data_api_body('action=delete');
        assert_same(['8DISC001'], $sent['records']);
        assert_same(['baseline_arm_1'], $sent['events']);
        assert_same(['age', 'sex', 'preg', 'notes', 'dbl'], $sent['fields']);
        assert_contains('/records/8DISC001?event=baseline_arm_1', $response->headers()['Location']);
    });

    it('deletes the whole record and returns to the dashboard', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue('view_edit', rights: true);
        api_route('data:action=delete', [['record_id' => '8DISC001', 'form_name' => '', 'deleted' => 9]]);

        $response = m5_post('/projects/3/records/8DISC001', ['scope' => 'record'], 'delete');

        $sent = data_api_body('action=delete');
        assert_true(!isset($sent['events']) && !isset($sent['fields']), 'no scope narrows a whole-record delete');
        assert_same('/projects/3/record-status', $response->headers()['Location']);
    });

    it('assigns a group, "No group" unassigning, and issues a link with POST (§8.7)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        queue_shell();
        api_route('/data-access-group', ['record_id' => '8DISC001', 'group_id' => null]);
        m5_post('/projects/3/records/8DISC001', ['group_id' => 'none'], 'assign_group');
        assert_same(['group_id' => null], m4_body('PUT', '/records/8DISC001/data-access-group'));

        // Issuing is the POST now: GET only reports, so rendering a record view can never replace
        // a live link (REQ-API-082/146).
        api_route('/survey-link', ['state' => 'live', 'event' => 'baseline_arm_1',
            'url' => 'http://localhost:8000/s/link-token', 'collected_at' => null]);
        m5_post('/projects/3/records/8DISC001', ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores'], 'survey_link');
        assert_same(1, m4_count('POST', '/survey-link'));
    });

    it('reports the survey link state on every render and hides what it rules out (§8.7, REQ-UI-028)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue('view_edit');
        $live = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'scores'])->body();
        assert_contains('value="http://localhost:8000/s/link-token"', $live);
        assert_contains('Revoke link', $live);

        resetApi();
        m5_queue('view_edit');
        assert_contains('/s/link-token',
            m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'scores'])->body(),
            'a live link is reported every time, not once: looking at it changes nothing (REQ-API-145)');

        // Submitted: no URL to copy, the date instead, and no revoke of a link that spent itself.
        resetApi();
        m5_queue('view_edit', link: ['state' => 'submitted', 'event' => 'baseline_arm_1',
            'url' => '', 'collected_at' => '2026-10-08 09:15:00']);
        $done = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'scores'])->body();
        assert_not_contains('clara-survey-url', $done, 'a spent link has no URL to copy');
        assert_contains('Submitted on 2026-10-08 09:15:00', $done);
        assert_not_contains('Revoke link', $done);
        assert_contains('Get a new link', $done, 're-issue stays offered; the API refuses it over stored values');
    });
});
