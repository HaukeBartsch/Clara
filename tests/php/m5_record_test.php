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
function m5_detail(string $level = 'view_edit', bool $projectAdmin = false, bool $rights = false, ?string $naming = null): array
{
    $grants = [];
    foreach ([['baseline_arm_1', 'intake'], ['follow_up_arm_1', 'intake'], ['baseline_arm_1', 'scores']] as [$event, $instrument]) {
        $grants[] = ['unique_event_name' => $event, 'instrument' => $instrument, 'data_access_level' => $level,
            'export_level' => 'export_none', 'delete_values' => $rights, 'edit_surveys' => $rights];
    }

    return [
        'id' => 3, 'project_name' => '8DISC', 'organization' => 'NAT EU', 'record_count' => 1,
        // The record naming mode the detail carries (REQ-API-151), default manual.
        'record_naming' => $naming ?? 'manual',
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
function m5_queue(string $level = 'view_edit', string $mode = 'development', bool $projectAdmin = false, ?array $status = null, ?array $history = null, bool $rights = false, ?array $link = null, ?string $naming = null, bool $member = true): void
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
    // The self-service token fetch: a member gets one; a non-member — an administrator who
    // sees the project without belonging to it (REQ-API-102) — is answered 403.
    api_route('/api/v1/projects/3/users/7/token', $member ? ['token' => 'tok-1'] : ['error' => 'forbidden'], $member ? 200 : 403);
    // The link report a survey pair's view reads on every render (§4.17, REQ-API-082): live by
    // default, so a test passes its own `state` when it needs another. Queued before the bare
    // project prefix below, which would otherwise answer it with a project detail.
    api_route('/survey-link', $link ?? ['state' => 'live', 'event' => 'baseline_arm_1',
        'url' => 'http://localhost:8000/s/link-token', 'collected_at' => null]);
    api_route('/api/v1/projects/3', m5_detail($level, $projectAdmin, $rights, $naming));
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
            ['age' => '42', 'notes' => '', 'sex' => '', 'record_id' => 'evil', 'form_name' => 'x', 'BAD-NAME' => '1'],
            ['age' => '41', 'notes' => 'old', 'sex' => '', 'preg' => '1'],
            'record_id'
        );
        // age carries a value; notes and preg were stored and removed; sex never had one.
        assert_same(['age' => '42', 'notes' => '', 'preg' => ''], $sent);
        // The identifier is the project's first field (GD-8), whatever its name: that one is
        // reserved, and a field that merely happens to be called record_id is not.
        assert_same(['record_id' => 'R-7'], DataEntry::submission(
            ['study_instance_uid' => 'evil', 'record_id' => 'R-7'], [], 'study_instance_uid'));
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

describe('date and date-time entry controls (§8.2, REQ-UI-065)', function (): void {
    it('gives the native control to the two default formats only', function (): void {
        assert_same('date', DataEntry::nativeDateControl(m5_field(1, 'visit', 'text', ['validation_type' => 'date']), 'Y-m-d'));
        assert_same('datetime-local', DataEntry::nativeDateControl(m5_field(2, 'drawn', 'text', ['validation_type' => 'datetime']), 'Y-m-d H:i'));
        // A pattern the browser control cannot express keeps the text input and its placeholder.
        assert_same('', DataEntry::nativeDateControl(m5_field(3, 'visit', 'text', ['validation_type' => 'date']), 'm-d-Y'));
        assert_same('', DataEntry::nativeDateControl(m5_field(4, 'drawn', 'text', ['validation_type' => 'datetime']), 'Y-m-d H:i:s'));
        // And a field that holds no date keeps the input its type asks for — including one with
        // neither a validation type nor a format, where both sides of the comparison are empty.
        assert_same('', DataEntry::nativeDateControl(m5_field(5, 'age', 'text', ['validation_type' => 'integer']), 'Y-m-d'));
        assert_same('', DataEntry::nativeDateControl(m5_field(6, 'name', 'text'), ''));
    });

    it('shows the stored value without the collection offset the control has no place for', function (): void {
        assert_same('2026-03-01', DataEntry::nativeDateValue('date', '2026-03-01+01:00'));
        assert_same('2026-03-01', DataEntry::nativeDateValue('date', '2026-03-01'));
        assert_same('', DataEntry::nativeDateValue('date', ''));
        assert_same('2026-03-01T09:30', DataEntry::nativeDateValue('datetime-local', '2026-03-01 09:30+01:00'));
        // A value in no shape the control holds: null, so the field falls back to its text input
        // rather than rendering an empty box over a value the record has.
        assert_same(null, DataEntry::nativeDateValue('date', '01/03/2026'));
        assert_same(null, DataEntry::nativeDateValue('datetime-local', '2026-03-01 09:30:15+01:00'));
    });

    it('posts the storage grammar and returns an untouched value exactly as stored (REQ-VAL-041)', function (): void {
        $fields = [
            m5_field(1, 'visit', 'text', ['validation_type' => 'date']),
            m5_field(2, 'drawn', 'text', ['validation_type' => 'datetime']),
            m5_field(3, 'sex', 'radio', ['choices' => '1$Male##2$Female']),
        ];
        $posted = DataEntry::nativeDateValues(
            // visit: the control still shows the day it opened with. drawn: a new minute.
            ['visit' => '2026-03-01', 'drawn' => '2026-03-01T10:15', 'sex' => '2'],
            ['visit' => '2026-03-01+01:00', 'drawn' => '2026-03-01 09:30+01:00', 'sex' => '1'],
            $fields
        );
        // GD-14 re-submits an untouched field (REQ-UI-031): sent as stored, so its collection
        // offset survives the save of a different field on the same form.
        assert_same('2026-03-01+01:00', $posted['visit']);
        // A changed one goes on as wall time in the browser zone, which the API stamps (GD-16).
        assert_same('2026-03-01 10:15', $posted['drawn']);
        assert_same('2', $posted['sex']);
    });

    it('takes a first value bare and leaves other fields, formats and clears untouched', function (): void {
        $fields = [
            m5_field(1, 'visit', 'text', ['validation_type' => 'date']),
            m5_field(2, 'dob', 'text', ['validation_type' => 'date', 'validation_format' => 'm-d-Y']),
            m5_field(3, 'text', 'text'),
        ];
        $posted = DataEntry::nativeDateValues(
            ['visit' => '2026-03-01', 'dob' => '03-2026T01', 'text' => 'note 2026-03-01T10:15'],
            // visit has no stored value yet; dob and text keep whatever the user typed.
            [],
            $fields
        );
        assert_same('2026-03-01', $posted['visit']);
        assert_same('03-2026T01', $posted['dob'], 'a field with another format is a text input');
        assert_same('note 2026-03-01T10:15', $posted['text']);
        // A cleared control posts nothing new: the empty value stays the clear it is (REQ-VAL-024).
        $cleared = DataEntry::nativeDateValues(['visit' => ''], ['visit' => '2026-03-01+01:00'], [m5_field(1, 'visit', 'text', ['validation_type' => 'date'])]);
        assert_same('', $cleared['visit']);
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
        assert_same([['record_id' => '8DISC001', 'events' => ['baseline_arm_1', 'follow_up_arm_1'], 'cells' => [
            'baseline_arm_1|intake' => 'some_data',
            'baseline_arm_1|scores' => 'finished',
            'follow_up_arm_1|intake' => 'no_data', // an unknown state never reaches the client
        ], 'instances' => []]], $payload['records']);
    });

    it('relays the sparse read: reported events without cells, an empty cell map (REQ-API-149)', function (): void {
        sign_in();
        m5_queue(status: [
            ['record_id' => '8DISC001', 'events' => [
                ['unique_event_name' => 'baseline_arm_1', 'instruments' => [['name' => 'intake', 'state' => 'some_data']]],
                ['unique_event_name' => 'follow_up_arm_1', 'instruments' => []],
            ]],
            ['record_id' => '8DISC002', 'events' => [
                ['unique_event_name' => 'baseline_arm_1', 'instruments' => []],
            ]],
        ]);

        $response = m5_get('/projects/3/record-status', [], json_headers());

        assert_same(200, $response->status());
        // The cell map stays a JSON object even when empty — the client indexes it by key.
        assert_contains('"cells":{}', $response->body());
        $payload = json_decode($response->body(), true);
        assert_same([
            ['record_id' => '8DISC001', 'events' => ['baseline_arm_1', 'follow_up_arm_1'],
                'cells' => ['baseline_arm_1|intake' => 'some_data'], 'instances' => []],
            ['record_id' => '8DISC002', 'events' => ['baseline_arm_1'], 'cells' => [], 'instances' => []],
        ], $payload['records']);
    });

    it('opens the record view on a valid new name and keeps an invalid one in the field', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        queue_shell();
        api_route('/api/v1/projects/3/users/7/token', ['token' => 'tok-1']);

        $ok = m5_post('/projects/3/record-status', ['record_id' => '8DISC 9', 'arm' => '1'], 'new_record');
        assert_same('/projects/3/records/8DISC%209?arm=1', $ok->headers()['Location']);

        $bad = m5_post('/projects/3/record-status', ['record_id' => 'a/b', 'arm' => '1'], 'new_record');
        assert_same('/projects/3/record-status', $bad->headers()['Location']);
        assert_same('a/b', $_SESSION['_stash']['new_record']['name']);
        // The token fetch proves membership; nothing else — and no store — is touched.
        assert_same(0, count($GLOBALS['api_calls']) - api_calls_to('/i18n/') - api_calls_to('/users/7/token'),
            'nothing is stored by opening a participant');
    });

    it('offers the form to a member but explains non-membership to one who holds no token (REQ-API-102)', function (): void {
        sign_in();
        m5_queue(member: false);

        $body = m5_get('/projects/3/record-status')->body();

        assert_not_contains('action=new_record', $body, 'the affordance that could only fail at Save is absent');
        assert_contains('not a member of this project', $body);
        // The dashboard itself stays readable — the refusal is about entry, not the page.
        assert_contains('data-region="records"', $body);

        // A read-only visit asks the membership question not at all.
        resetApi();
        m5_queue('read_only');
        m5_get('/projects/3/record-status');
        assert_same(0, api_calls_to('/users/7/token'), 'no offer, no token probe');
    });

    it('turns a new-participant post from a non-member back before opening the record view', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue(member: false);

        $response = m5_post('/projects/3/record-status', ['record_id' => '8DISC042', 'arm' => '1'], 'new_record');

        assert_same('/projects/3/record-status', $response->headers()['Location']);
        assert_contains('not a member of this project', $_SESSION['_flash'][0]['text']);
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

    it('emits only the new-participant control of the project naming mode (REQ-UI-052)', function (): void {
        sign_in();
        m5_queue();
        $manual = m5_get('/projects/3/record-status')->body();
        assert_contains('action=new_record', $manual);
        assert_not_contains('create_auto', $manual);

        resetApi();
        m5_queue(naming: 'auto');
        $auto = m5_get('/projects/3/record-status')->body();
        assert_contains('action=create_auto', $auto);
        assert_not_contains('action=new_record', $auto);
        assert_not_contains('name="record_id"', $auto, 'auto mode has no free-text field');
    });

    it('create_auto numbers the participant through the data API and opens the record at once', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue(naming: 'auto');
        api_route('data:content=generateNextRecordName', [['next_record_name' => '7']]);

        $response = m5_post('/projects/3/record-status', ['arm' => '1'], 'create_auto');

        assert_same('/projects/3/records/7?arm=1', $response->headers()['Location']);
        assert_same(1, data_api_calls('content=generateNextRecordName'));
    });

    it('names the backend service — not the form — when it does not answer at add-participant', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue(naming: 'auto');
        // The data API call fails at transport level (status 0): nothing behind it answered.
        api_route('data:content=generateNextRecordName', '', 0);

        $response = m5_post('/projects/3/record-status', ['arm' => '1'], 'create_auto');

        assert_same('/projects/3/record-status', $response->headers()['Location']);
        $text = $_SESSION['_flash'][0]['text'];
        assert_contains('could not reach its backend service', $text);
        assert_not_contains('form contains invalid values', $text, 'a dead backend is never the form\'s fault');
    });

    it('names the responsible fields when a save posts malformed event or instrument names', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();

        m5_post('/projects/3/records/8DISC001', ['event' => 'Bad Event', 'instrument' => '', 'iid' => '7'], 'save');

        assert_contains('The form contains invalid values: event, instrument', $_SESSION['_flash'][0]['text']);
    });
});

describe('record view (§8, REQ-UI-025…027)', function (): void {
    it('presents the identifier per naming mode — read-only input in manual, plain text in auto (REQ-UI-052)', function (): void {
        sign_in();
        m5_queue();
        $manual = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();
        assert_contains('id="f-record_id" value="8DISC001" readonly', $manual);

        resetApi();
        m5_queue(naming: 'auto');
        $auto = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();
        assert_not_contains('id="f-record_id" value=', $auto, 'no input in auto mode');
        assert_contains('<div class="form-control-plaintext py-0 clara-value" id="f-record_id">8DISC001</div>', $auto);
    });


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
            'event' => 'baseline_arm_1', 'instance' => 1, 'field' => 'age', 'old' => '41', 'new' => '42'], $payload['entries'][0]);
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

    it('keys the import row on the project\'s own identifier field, not a fixed record_id (GD-8)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        // A FIONA-style dictionary: the first field of the first instrument is study_instance_uid,
        // and a later field happens to be called record_id.
        api_route('/instruments/7/fields', array_merge(
            [m5_field(1, 'study_instance_uid', 'text'), m5_field(10, 'record_id', 'text')], array_slice(m5_fields(), 1)));
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 2, 'import_form_name' => 'intake']]);

        m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['age' => '43', 'study_instance_uid' => 'forged', 'record_id' => 'R-7'], ['age' => '42']), 'save');

        $row = data_api_body('action=import')['data'][0];
        assert_same('8DISC001', $row['study_instance_uid'], 'the record name, never a posted value');
        assert_same('R-7', $row['record_id'] ?? null, 'record_id is an ordinary field here');
        assert_same('43', $row['age']);
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

    it('states a refusal no field input can carry, rather than promising reasons it hides', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 0,
            'import_form_name' => "Validation error: age: TYPE_INVALID — value 'abc' is not a valid integer; "
                . "event_name: CONTENT_INVALID — unknown event 'old_arm_1'; gone_field: UNKNOWN_FIELD — unknown field 'gone_field'"]]);

        $body = m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => 'abc'], ['age' => '42']), 'save')->body();

        assert_contains('TYPE_INVALID — value &#039;abc&#039; is not a valid integer', $body, 'the field reason stays under its input');
        assert_contains('event_name: CONTENT_INVALID — unknown event &#039;old_arm_1&#039;', $body,
            'a framing-key reason is on screen, not only in the log');
        assert_contains("gone_field: UNKNOWN_FIELD — unknown field &#039;gone_field&#039;", $body,
            'so is one keyed on a field this form no longer renders');
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
        api_route('/api/v1/projects/3/instruments/7/fields', m5_fields());
        api_route('/api/v1/projects/3/instruments', [['id' => 7, 'name' => 'intake', 'position' => 1]]);
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

    it('offers a not-yet-stored participant its survey link, with no report to read (REQ-UI-051)', function (): void {
        sign_in();
        m5_queue('view_edit');
        $body = m5_get('/projects/3/records/8DISC009', ['event' => 'baseline_arm_1', 'instrument' => 'scores'])->body();

        assert_contains('clara-survey-link', $body);
        assert_contains('name="create_record" value="1"', $body);
        assert_contains('Getting a link stores 8DISC009 as a participant first.', $body);
        assert_contains('Store 8DISC009 as a new participant and issue a survey link for form scores at baseline?', $body);
        assert_not_contains('Revoke link', $body);
        assert_same(0, api_calls_to('/survey-link'), 'the API holds no link for a record it does not have');

        // Not a survey, not view_edit on the pair, or analysis mode: nothing to offer.
        foreach ([['view_edit', 'development', 'intake'], ['read_only', 'development', 'scores'],
            ['view_edit', 'analysis', 'scores']] as [$level, $mode, $instrument]) {
            resetApi();
            m5_queue($level, $mode);
            $closed = m5_get('/projects/3/records/8DISC009', ['event' => 'baseline_arm_1', 'instrument' => $instrument])->body();
            assert_contains('clara-record-new', $closed, 'the view itself still renders');
            assert_not_contains('action=survey_link', $closed, $level . '/' . $mode . '/' . $instrument);
        }
    });

    it('stores a new participant with the identifier-only import, then issues its link (REQ-UI-051)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC009', 'form_name' => 'scores', 'import_record_id' => 1, 'import_form_name' => 'scores']]);

        $response = m5_post('/projects/3/records/8DISC009',
            ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores', 'create_record' => '1'], 'survey_link');

        assert_same('/projects/3/records/8DISC009?event=baseline_arm_1&instrument=scores', $response->headers()['Location']);
        // Exactly what a save of the empty form sends: the identifier at the selected event (GD-8).
        assert_same(['record_id' => '8DISC009', 'form_name' => 'scores', 'event_name' => 'baseline_arm_1'],
            data_api_body('action=import')['data'][0]);
        $calls = array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool =>
            str_ends_with($c['url'], '/api/') || str_contains($c['url'], '/survey-link')));
        assert_same(2, count($calls));
        assert_true(str_ends_with($calls[0]['url'], '/api/') && $calls[1]['method'] === 'POST',
            'the record is stored before its link is issued');
        assert_contains('event=baseline_arm_1', $calls[1]['url']);
        assert_contains('8DISC009 was stored as a participant', $_SESSION['_flash'][0]['text']);

        // A stored record's re-issue never imports.
        sign_in(['csrf_token' => M5_CSRF]);
        resetApi();
        m5_queue();
        m5_post('/projects/3/records/8DISC001', ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores'], 'survey_link');
        assert_same(0, data_api_calls('action=import'));
        assert_same(1, m4_count('POST', '/survey-link'));

        // The identifier-only import names the record under the project's own first field.
        sign_in(['csrf_token' => M5_CSRF]);
        resetApi();
        api_route('/instruments/7/fields', array_merge([m5_field(1, 'study_instance_uid', 'text')], array_slice(m5_fields(), 1)));
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC009', 'form_name' => 'scores', 'import_record_id' => 1, 'import_form_name' => 'scores']]);
        m5_post('/projects/3/records/8DISC009',
            ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores', 'create_record' => '1'], 'survey_link');
        assert_same(['study_instance_uid' => '8DISC009', 'form_name' => 'scores', 'event_name' => 'baseline_arm_1'],
            data_api_body('action=import')['data'][0]);
    });

    it('issues no link when the new participant cannot be stored, and says why (REQ-UI-051)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC009', 'form_name' => 'scores', 'import_record_id' => 0,
            'import_form_name' => 'Validation error: record_id: PERMISSION_DENIED — the record belongs to another data access group']]);

        m5_post('/projects/3/records/8DISC009',
            ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores', 'create_record' => '1'], 'survey_link');

        assert_same(0, m4_count('POST', '/survey-link'));
        assert_contains('8DISC009 could not be stored, so no survey link was issued', $_SESSION['_flash'][0]['text']);
        assert_contains('belongs to another data access group', $_SESSION['_flash'][0]['text']);

        // Analysis mode refuses the import itself (REQ-API-109): named, and still no link.
        sign_in(['csrf_token' => M5_CSRF]);
        resetApi();
        m5_queue();
        api_route('data:action=import', ['error' => 'Project in analysis mode'], 403);
        m5_post('/projects/3/records/8DISC009',
            ['iid' => '12', 'event' => 'baseline_arm_1', 'instrument' => 'scores', 'create_record' => '1'], 'survey_link');
        assert_contains('analysis mode', $_SESSION['_flash'][0]['text']);
        assert_same(0, m4_count('POST', '/survey-link'));
    });
});

/** intake with a checkbox `habits` after its other fields (REQ-DB-042). */
function m5_checkbox_fields(array $extra = []): array
{
    return array_merge(m5_fields(), [m5_field(7, 'habits', 'checkbox', $extra + ['choices' => '1$Smoking##2$Drinking##3$Sports'])]);
}

/** A history in which box 1 was checked and box 3 checked, then cleared. */
function m5_checkbox_history(): array
{
    return m5_history([
        m5_entry('update', 'baseline_arm_1', ['habits___3' => ['1', null]], '2026-10-02 10:00:00'),
        m5_entry('update', 'baseline_arm_1', ['habits___1' => [null, '1'], 'habits___3' => [null, '1'], 'age' => ['41', '42']]),
    ]);
}

describe('checkboxes in data entry (REQ-DB-042, REQ-UI-025)', function (): void {
    it('reads REDCap\'s spellings in the references a form fetches values for (REQ-VAL-044)', function (): void {
        assert_same(['e1|a', 'ev|f___2', 'e1|g___1'],
            DataEntry::references("[a] = '[x]' or [ev][f(2)] = \"[y]\" and [g(1)]", 'e1'));
        assert_true(DataEntry::isReadonly(['field_annotation' => ' @READONLY']));
        assert_true(DataEntry::isReadonly(['field_annotation' => '@DEFAULT="1" @readonly']));
        assert_true(!DataEntry::isReadonly(['field_annotation' => '@READONLY-SURVEY']), 'another REDCap tag');
        assert_true(!DataEntry::isReadonly(['field_annotation' => '0032,4000']));
    });

    it('drops a value posted for a @READONLY field before the import (REQ-DB-046)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        m5_queue();
        $GLOBALS['api_routes']['/api/v1/projects/3/instruments/7/fields'] = [];
        api_route('/api/v1/projects/3/instruments/7/fields', array_map(
            static fn (array $f): array => $f['field_name'] === 'notes' ? $f + ['field_annotation' => '@READONLY'] : $f, m5_fields()));
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'event_name' => 'baseline_arm_1',
            'import_record_id' => 2, 'import_form_name' => 'intake']]);
        m5_post('/projects/3/records/8DISC001', m5_save_fields(['age' => '43', 'notes' => 'forged'], ['notes' => '<b>ok</b>']), 'save');
        $row = data_api_body('action=import')['data'][0];
        assert_same('43', $row['age']);
        assert_true(!array_key_exists('notes', $row), 'neither the forged value nor a clear reaches the import');
    });

    it('names a checkbox\'s values per box and recognises a box of its checkbox', function (): void {
        $habits = m5_field(7, 'habits', 'checkbox', ['choices' => '1$Smoking##12$Drinking']);
        assert_same(['habits___1', 'habits___12'], DataEntry::valueNames($habits));
        assert_same(['age'], DataEntry::valueNames(m5_field(2, 'age', 'text')));
        assert_true(DataEntry::isEnterable($habits));
        assert_true(DataEntry::isChoiceColumnOf('habits___12', 'habits'));
        // A checkbox code is lower-case letters, digits and _ (REQ-VAL-022).
        assert_true(DataEntry::isChoiceColumnOf('habits___x', 'habits'));
        assert_true(!DataEntry::isChoiceColumnOf('habits___X', 'habits'), 'no upper case in a box code');
        assert_true(!DataEntry::isChoiceColumnOf('habits___a-b', 'habits'), 'no hyphen in a box code');
        assert_true(!DataEntry::isChoiceColumnOf('habits_old___1', 'habits'));
        assert_true(!DataEntry::isChoiceColumnOf('habits', 'habits'));
    });

    it('renders one box per choice, checked as the history left it, each with its own baseline', function (): void {
        sign_in();
        api_route('/instruments/7/fields', m5_checkbox_fields(['required' => true]));
        m5_queue(history: m5_checkbox_history());

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_contains('name="value[habits___1]" value="1"', $body);
        assert_contains('data-clara-choice="habits___1" data-clara-choices="1$Smoking" checked>', $body);
        assert_contains('data-clara-choice="habits___2" data-clara-choices="1$Drinking">', $body);
        assert_contains('data-clara-choice="habits___3" data-clara-choices="1$Sports">', $body, 'cleared by the newer entry');
        assert_contains('name="was[habits___1]" value="1"', $body);
        assert_contains('name="was[habits___3]" value=""', $body);
        assert_not_contains('name="value[habits]"', $body, 'the checkbox itself carries no value');
        assert_contains('role="group" aria-labelledby="f-habits-label"', $body);
        assert_contains('data-clara-field="habits" data-clara-type="checkbox"', $body);
    });

    it('shows a read_only member the checked boxes, every box still readable by the evaluator', function (): void {
        sign_in();
        api_route('/instruments/7/fields', m5_checkbox_fields());
        m5_queue('read_only', history: m5_checkbox_history());

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_not_contains('name="value[habits___', $body);
        assert_contains('class="me-2" data-clara-choice="habits___1" data-clara-value="1"', $body);
        assert_contains('class="me-2 d-none" data-clara-choice="habits___2" data-clara-value=""', $body);
        assert_contains('✓ Smoking', $body);
    });

    it('saves a checked box as 1 and a stored box the user unchecked as an explicit clear (GD-14)', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        api_route('/instruments/7/fields', m5_checkbox_fields());
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 2, 'import_form_name' => 'intake']]);

        // The browser posts only checked boxes; was[…] carries every box's stored state.
        m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['habits___2' => '1'], ['habits___1' => '1', 'habits___2' => '', 'habits___3' => '']), 'save');

        assert_same(['record_id' => '8DISC001', 'form_name' => 'intake', 'event_name' => 'baseline_arm_1',
            'habits___2' => '1', 'habits___1' => ''], data_api_body('action=import')['data'][0]);
    });

    it('re-renders a refused save with the boxes as the user left them and the reason under the checkbox', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        api_route('/instruments/7/fields', m5_checkbox_fields());
        m5_queue(history: m5_checkbox_history());
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 0,
            'import_form_name' => 'Validation error: habits___2: CHOICE_INVALID — value \'7\' of checkbox choice \'habits___2\' must be 0 or 1']]);

        $body = m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['habits___2' => '7'], ['habits___1' => '1', 'habits___2' => '', 'habits___3' => '']), 'save')->body();

        assert_contains('data-clara-choice="habits___1" data-clara-choices="1$Smoking">', $body, 'unchecked by the user, not re-checked from storage');
        assert_contains('data-clara-choice="habits___2" data-clara-choices="1$Drinking" checked>', $body);
        assert_contains('clara-field clara-field-invalid" data-clara-field="habits"', $body);
        assert_contains('must be 0 or 1', $body);
    });

    it('serves a checkbox\'s history as that of its boxes (REQ-UI-026)', function (): void {
        sign_in();
        m5_queue(history: m5_checkbox_history());

        $payload = json_decode(m5_get('/projects/3/records/8DISC001', ['field' => 'habits', 'event' => 'baseline_arm_1'], json_headers())->body(), true);

        assert_same(['habits___3', 'habits___1', 'habits___3'], array_column($payload['entries'], 'field'));
        assert_contains('field=habits', $GLOBALS['api_calls'][count($GLOBALS['api_calls']) - 1]['url']);
    });
});

/** intake with a yes/no `consent` and a notes field `remarks` after its other fields (REQ-DB-043/044). */
function m5_yesno_notes_fields(): array
{
    return array_merge(m5_fields(), [
        m5_field(7, 'consent', 'yesno', ['choices' => '1$Yes##0$No']),
        m5_field(8, 'remarks', 'notes'),
    ]);
}

function m5_yesno_notes_history(): array
{
    return m5_history([
        m5_entry('update', 'baseline_arm_1', ['consent' => [null, '0'], 'remarks' => [null, "Line one\nline <two>"]]),
    ]);
}

describe('yes/no and notes fields in data entry (REQ-DB-043/044, REQ-UI-025)', function (): void {
    it('treats both as enterable, notes as free text, and labels yes/no in the interface language', function (): void {
        $consent = m5_field(7, 'consent', 'yesno', ['choices' => '1$Yes##0$No']);
        assert_true(DataEntry::isEnterable($consent) && DataEntry::isEnterable(m5_field(8, 'remarks', 'notes')));
        assert_true(DataEntry::isFreeText(m5_field(8, 'remarks', 'notes')));
        assert_true(!DataEntry::isFreeText($consent));
        $nb = static fn (string $key): string => ['record.yes' => 'Ja', 'record.no' => 'Nei'][$key];
        assert_same([['code' => '1', 'label' => 'Ja'], ['code' => '0', 'label' => 'Nei']], DataEntry::shownChoices($consent, $nb));
        $radio = m5_field(3, 'sex', 'radio', ['choices' => '1$Male##2$Female']);
        assert_same(DataEntry::choices('1$Male##2$Female'), DataEntry::shownChoices($radio, $nb), 'other fields keep their labels');
    });

    it('renders yes/no as a radio pair and notes as a text area, filled as the history left them', function (): void {
        sign_in();
        api_route('/instruments/7/fields', m5_yesno_notes_fields());
        m5_queue(history: m5_yesno_notes_history());

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_contains('role="radiogroup" aria-labelledby="f-consent-label"', $body);
        assert_contains('name="value[consent]"' . "\n" . '                                       value="1">', $body);
        assert_contains('name="value[consent]"' . "\n" . '                                       value="0" checked>', $body, 'No is an answer');
        assert_contains('for="f-consent-0">Yes</label>', $body);
        assert_contains('for="f-consent-1">No</label>', $body);
        assert_contains('data-clara-field="consent" data-clara-type="yesno"', $body);
        assert_contains('data-clara-choices="1$Yes##0$No"', $body, 'branching resolves the stored labels');
        assert_contains('name="value[remarks]" rows="4">' . "\n" . "Line one\nline &lt;two&gt;</textarea>", $body);
        assert_contains('name="was[remarks]" value="Line one' . "\n" . 'line &lt;two&gt;"', $body);
    });

    it('labels the pair in Norwegian for a Norwegian session', function (): void {
        sign_in(['ui_language' => 'nb']);
        api_route('/api/v1/i18n/bundle', ['language' => 'nb', 'strings' => ['record.yes' => 'Ja', 'record.no' => 'Nei']]);
        api_route('/instruments/7/fields', m5_yesno_notes_fields());
        m5_queue(history: m5_yesno_notes_history());

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_contains('for="f-consent-0">Ja</label>', $body);
        assert_contains('for="f-consent-1">Nei</label>', $body);
    });

    it('shows a read_only member the translated answer and the note with its line breaks', function (): void {
        sign_in();
        api_route('/instruments/7/fields', m5_yesno_notes_fields());
        m5_queue('read_only', history: m5_yesno_notes_history());

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake'])->body();

        assert_not_contains('name="value[consent]"', $body);
        assert_contains('id="f-consent">No <span class="text-body-secondary small">(0)</span>', $body);
        assert_contains('class="form-control-plaintext py-0 clara-value clara-notes" id="f-remarks"', $body);
    });

    it('sends a text area\'s CRLF line breaks as LF, so an untouched note arrives as stored', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        api_route('/instruments/7/fields', m5_yesno_notes_fields());
        m5_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'intake', 'import_record_id' => 2, 'import_form_name' => 'intake']]);

        m5_post('/projects/3/records/8DISC001',
            m5_save_fields(['consent' => '1', 'remarks' => "Line one\r\nline two"], ['consent' => '0', 'remarks' => "Line one\nline two"]), 'save');

        $row = data_api_body('action=import')['data'][0];
        assert_same('1', $row['consent']);
        assert_same("Line one\nline two", $row['remarks']);
    });
});
