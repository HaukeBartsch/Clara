<?php
// Repeating instruments in the web layer (REQ-UI-054, REQ-UI-055): the Design page's
// "Repeating" dropdown, the Record Status Dashboard's instance dots and "+", and the record
// view's instances — what each reads, renders and sends. Built on the M4 and M5 fixtures,
// which run before this file.

declare(strict_types=1);

use Clara\Controllers\DesignController;
use Clara\Controllers\RecordController;

/** M4's instruments plus `visits`, which repeats at baseline. */
function rep_design_instruments(): array
{
    return [
        ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 2, 'is_survey' => false, 'branching_logic' => '', 'repeating_events' => []],
        ['id' => 12, 'name' => 'scores', 'position' => 2, 'field_count' => 1, 'is_survey' => true, 'branching_logic' => '', 'repeating_events' => []],
        ['id' => 15, 'name' => 'visits', 'position' => 3, 'field_count' => 3, 'is_survey' => false, 'branching_logic' => '', 'repeating_events' => ['baseline_arm_1']],
    ];
}

/** M5's reads with `visits` (id 15) repeating at baseline and its instances in record-status. */
function rep_record_queue(array $history = [], string $mode = 'development'): void
{
    $visits = ['id' => 15, 'name' => 'visits', 'position' => 3, 'field_count' => 2, 'is_survey' => false,
        'branching_logic' => '', 'repeating_events' => ['baseline_arm_1']];
    // Every instrument's fields ahead of the instruments list below, whose fragment would
    // otherwise answer them: the record identifier is found by reading the earlier instruments'
    // fields (GD-8), and intake's record_id must win over visits' first field.
    api_route('/api/v1/projects/3/instruments/7/fields', m5_fields());
    api_route('/api/v1/projects/3/instruments/12/fields', [m5_field(9, 'score', 'text')]);
    api_route('/api/v1/projects/3/instruments/15/fields', [
        m5_field(20, 'visit_note', 'text'),
        m5_field(21, 'visit_score', 'text', ['validation_type' => 'integer']),
    ]);
    api_route('/api/v1/projects/3/instrument-event-mapping', [['arm_num' => 1, 'mapping' => [
        'intake' => ['baseline_arm_1', 'follow_up_arm_1'], 'scores' => ['baseline_arm_1'], 'visits' => ['baseline_arm_1']]]]);
    api_route('/api/v1/projects/3/record-status', [
        ['record_id' => '8DISC001', 'events' => [
            ['unique_event_name' => 'baseline_arm_1', 'instruments' => [
                ['name' => 'intake', 'state' => 'some_data'],
                ['name' => 'visits', 'state' => 'some_data', 'instances' => [
                    ['instance' => 1, 'state' => 'some_data'], ['instance' => 2, 'state' => 'finished'], ['instance' => 4, 'state' => 'mystery'],
                ]],
            ]],
            ['unique_event_name' => 'follow_up_arm_1', 'instruments' => []],
        ]],
    ]);
    api_route('/api/v1/projects/3/instruments', [
        ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 6, 'is_survey' => false, 'branching_logic' => '', 'repeating_events' => []],
        ['id' => 12, 'name' => 'scores', 'position' => 2, 'field_count' => 1, 'is_survey' => true, 'branching_logic' => '', 'repeating_events' => []],
        $visits,
    ]);
    $history = $history ?: [
        ['created_at' => '2026-10-02 10:00:00', 'user_id' => 7, 'user_display_name' => 'Test', 'action' => 'update',
            'instrument' => 'visits', 'event' => 'baseline_arm_1', 'instance' => 2,
            'fields' => [['field' => 'visit_note', 'old' => null, 'new' => 'second visit']]],
        ['created_at' => '2026-10-01 10:00:00', 'user_id' => 7, 'user_display_name' => 'Test', 'action' => 'update',
            'instrument' => 'visits', 'event' => 'baseline_arm_1', 'instance' => 1,
            'fields' => [['field' => 'visit_note', 'old' => null, 'new' => 'first visit']]],
    ];
    m5_queue('view_edit', $mode, false, null, m5_history($history), true);
    // The detail's instruments carry visits too; its pairs are granted like the others.
    $detail = m5_detail('view_edit', false, true);
    $detail['instruments'][] = ['id' => 15, 'name' => 'visits', 'position' => 3, 'field_count' => 2];
    $detail['permissions']['grants'][] = ['unique_event_name' => 'baseline_arm_1', 'instrument' => 'visits',
        'data_access_level' => 'view_edit', 'export_level' => 'export_none', 'delete_values' => true, 'edit_surveys' => true];
    $GLOBALS['api_routes']['/api/v1/projects/3'] = [['status' => 200, 'body' => (string) json_encode($detail), 'headers' => []]];
}

describe('repeating instruments on the Design page (REQ-UI-054)', function (): void {
    it('offers a switch per event: on where it repeats, disabled where it is not mapped', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instrument-event-mapping', [['arm_num' => 1, 'mapping' => [
            'intake' => ['baseline_arm_1'], 'scores' => ['baseline_arm_1'], 'visits' => ['baseline_arm_1', 'follow_up_arm_1']]]]);
        m4_queue('development', false, true, rep_design_instruments());

        $body = m4_get('/projects/3/design')->body();

        assert_contains('⟳ baseline', $body);
        assert_contains('id="repeating-15-baseline_arm_1"', $body);
        assert_true(preg_match('/id="repeating-15-baseline_arm_1"\s+name="events\[\]" value="baseline_arm_1"\s+checked /', $body) === 1, 'baseline switch on');
        assert_true(preg_match('/id="repeating-15-follow_up_arm_1"\s+name="events\[\]" value="follow_up_arm_1"\s+ >/', $body) === 1, 'follow-up switch off and enabled');
        assert_true(preg_match('/id="repeating-15-screening_arm_1"\s+name="events\[\]" value="screening_arm_1"\s+ disabled>/', $body) === 1, 'unmapped event disabled');
        // The first instrument and the survey say why instead of offering switches.
        assert_contains('The first instrument holds the record ID and cannot repeat.', $body);
        assert_contains('A survey cannot repeat: one link fills one form.', $body);
        assert_contains('action=save_repeating', $body);
    });

    it('locks the switches where structure writes are closed (production, no set)', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instrument-event-mapping', [['arm_num' => 1, 'mapping' => ['visits' => ['baseline_arm_1']]]]);
        m4_queue('production', false, true, rep_design_instruments());

        $body = m4_get('/projects/3/design')->body();

        assert_true(preg_match('/value="baseline_arm_1"\s+checked disabled>/', $body) === 1, 'state shown, not editable');
        assert_not_contains('data-clara-repeating-save', $body);
    });

    it('sends the switched-on events as the instrument\'s whole set', function (): void {
        sign_in(['csrf_token' => M4_CSRF]);
        m4_queue();
        api_route('/instruments/15/repeating', ['id' => 15, 'repeating_events' => ['baseline_arm_1', 'follow_up_arm_1']]);

        $response = m4_post('/projects/3/design', ['iid' => '15', 'instrument' => 'visits',
            'events' => ['baseline_arm_1', 'follow_up_arm_1', 'bad name']], 'save_repeating');

        assert_same('/projects/3/design', $response->headers()['Location']);
        assert_same(['events' => ['baseline_arm_1', 'follow_up_arm_1']], m4_body('PUT', '/instruments/15/repeating'));
    });

    it('turns repeating off with an empty list, never an object', function (): void {
        sign_in(['csrf_token' => M4_CSRF]);
        m4_queue();
        api_route('/instruments/15/repeating', ['id' => 15, 'repeating_events' => []]);

        m4_post('/projects/3/design', ['iid' => '15', 'instrument' => 'visits'], 'save_repeating');

        assert_contains('"events":[]', api_request_raw('/instruments/15/repeating'));
    });

    it('lists every arm with its events, marked mapped and on', function (): void {
        $choices = DesignController::repeatingChoices(m4_arms(), [['arm_num' => 1, 'mapping' => ['visits' => ['baseline_arm_1']]]],
            rep_design_instruments());

        assert_same('first', $choices[7]['locked']);
        assert_same('survey', $choices[12]['locked']);
        assert_same('', $choices[15]['locked']);
        $events = $choices[15]['arms'][0]['events'];
        assert_same([true, true], [$events[0]['mapped'], $events[0]['on']]);
        assert_same([false, false], [$events[1]['mapped'], $events[1]['on']]);
    });
});

describe('repeating instruments on the dashboard and the record view (REQ-UI-055)', function (): void {
    it('marks the repeating column and offers "+" where entry is', function (): void {
        sign_in();
        rep_record_queue();

        $body = m5_get('/projects/3/record-status')->body();

        assert_true(preg_match('/data-instrument="visits"\s+data-repeating="1" data-add="1"/', $body) === 1, 'repeating column');
        assert_true(preg_match('/data-instrument="intake"\s+data-repeating/', $body) === 0, 'a column that does not repeat is not marked');
    });

    it('offers no "+" in analysis mode', function (): void {
        sign_in();
        rep_record_queue([], 'analysis');

        $body = m5_get('/projects/3/record-status')->body();

        assert_contains('data-repeating="1"', $body);
        assert_not_contains('data-add="1"', $body);
    });

    it('relays the instances of a repeating cell, states made safe', function (): void {
        sign_in();
        rep_record_queue();

        $payload = json_decode(m5_get('/projects/3/record-status', [], json_headers())->body(), true);

        assert_same([[1, 'some_data'], [2, 'finished'], [4, 'no_data']], $payload['records'][0]['instances']['baseline_arm_1|visits']);
    });

    it('opens one instance: its own values, the pills, and "+" for the next', function (): void {
        sign_in();
        rep_record_queue();

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'visits', 'instance' => '2'])->body();

        assert_contains('value="second visit"', $body);
        assert_not_contains('value="first visit"', $body);
        assert_contains('<input type="hidden" name="repeating" value="1"><input type="hidden" name="instance" value="2">', $body);
        assert_contains('data-instance="2"', $body);
        foreach (['1', '2', '4'] as $n) {
            assert_contains('data-clara-instance="' . $n . '"', $body);
        }
        // "+" beside the name opens the instance after the highest.
        assert_contains('data-clara-add-instance="5"', $body);
        assert_contains('instrument=visits&amp;instance=5', $body);
        // Deleting here deletes this instance.
        assert_contains('Delete instance #2', $body);
    });

    it('shows a new instance as empty and among the pills', function (): void {
        sign_in();
        rep_record_queue();

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'visits', 'instance' => '5'])->body();

        assert_contains('data-clara-instance="5"', $body);
        assert_not_contains('value="second visit"', $body);
    });

    it('ignores ?instance= on a form that does not repeat', function (): void {
        sign_in();
        rep_record_queue();

        $body = m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake', 'instance' => '3'])->body();

        assert_not_contains('name="repeating"', $body);
        assert_not_contains('clara-instance-nav', $body);
    });

    it('names the instance in the audited read, and never numbers a form that does not repeat (REQ-AUD-035)', function (): void {
        sign_in();
        rep_record_queue();

        m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'visits', 'instance' => '2']);

        $reads = array_values(array_filter($GLOBALS['api_calls'],
            static fn (array $c): bool => str_contains($c['url'], '/records/8DISC001/read')));
        assert_same(1, count($reads));
        assert_contains('instrument=visits', $reads[0]['url']);
        assert_contains('instance=2', $reads[0]['url']);

        resetApi();
        rep_record_queue();
        m5_get('/projects/3/records/8DISC001', ['event' => 'baseline_arm_1', 'instrument' => 'intake', 'instance' => '3']);

        $plain = array_values(array_filter($GLOBALS['api_calls'],
            static fn (array $c): bool => str_contains($c['url'], '/records/8DISC001/read')));
        assert_same(1, count($plain));
        assert_not_contains('instance=', $plain[0]['url'], 'the page rendered its first instance (§3.2)');
    });

    it('saves an instance with REDCap\'s repeat columns and finishes that instance', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        rep_record_queue();
        api_route('data:action=import', [['record_id' => '8DISC001', 'form_name' => 'visits', 'import_record_id' => 2, 'import_form_name' => 'visits']]);
        api_route('/instruments/15/completion', ['state' => 'finished']);

        $response = m5_post('/projects/3/records/8DISC001', [
            'event' => 'baseline_arm_1', 'instrument' => 'visits', 'iid' => '15', 'repeating' => '1', 'instance' => '3',
            'value' => ['visit_note' => 'third'], 'was' => [], 'completion' => 'finished', 'completion_was' => 'no_data',
        ], 'save');

        assert_same('/projects/3/records/8DISC001?event=baseline_arm_1&instrument=visits&instance=3', $response->headers()['Location']);
        $row = data_api_body('action=import')['data'][0];
        assert_same(['visits', '3', 'third'], [$row['redcap_repeat_instrument'], $row['redcap_repeat_instance'], $row['visit_note']]);
        assert_same(['state' => 'finished', 'instance' => 3], api_request_body('/instruments/15/completion'));
    });

    it('deletes one instance through repeat_instance', function (): void {
        sign_in(['csrf_token' => M5_CSRF]);
        rep_record_queue();
        api_route('data:action=delete', [['record_id' => '8DISC001', 'form_name' => '', 'deleted' => 1]]);

        m5_post('/projects/3/records/8DISC001', ['scope' => 'instance', 'event' => 'baseline_arm_1', 'instrument' => 'visits',
            'iid' => '15', 'repeating' => '1', 'instance' => '2'], 'delete');

        $sent = data_api_body('action=delete');
        assert_same('2', $sent['repeat_instance']);
        assert_same(['visit_note', 'visit_score'], $sent['fields']);
    });

    it('keys the pills: listed instances, the first, and the open one', function (): void {
        assert_same([
            ['instance' => 1, 'state' => 'no_data', 'current' => false],
            ['instance' => 3, 'state' => 'finished', 'current' => false],
            ['instance' => 7, 'state' => 'no_data', 'current' => true],
        ], RecordController::instancePills([[3, 'finished']], 7));
    });
});
