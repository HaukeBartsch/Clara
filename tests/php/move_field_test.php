<?php
// Moving a field in the web layer (REQ-UI-056): the field list's Move button and its dialog,
// the Designer's data region the dialog reads another instrument's fields from, and the one
// write it sends (`POST …/fields/{fid}/move`, REQ-API-157). Built on the M4 fixtures, which
// run before this file.

declare(strict_types=1);

describe('Move a field (§7.2, REQ-UI-056)', function (): void {
    it('gives every field a Move button and the page one dialog: every instrument, this one chosen', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/design/instruments/7')->body();

        assert_contains('data-bs-target="#clara-move"', $body);
        assert_contains('data-clara-move-field="11" data-clara-move-name="record_id"', $body);
        assert_contains('data-clara-move-field="14" data-clara-move-name="bmi"', $body);
        assert_contains('id="clara-move"', $body);
        assert_contains('action="/projects/3/design/instruments/7?action=relocate_field"', $body);
        assert_contains('<option value="7" selected>intake</option>', $body);
        assert_contains('<option value="12">scores</option>', $body);
        // This instrument's fields as before-field targets, labels escaped, and the end.
        assert_contains('<option value="14">bmi — BMI &lt;b&gt;</option>', $body);
        assert_contains('<option value="end">(end of the instrument)</option>', $body);
        // The dialog reads nothing on render: another instrument's fields come from the region.
        assert_same(0, m4_count('GET', '/instruments/12/fields'));
        // The Move buttons and the instrument picker render disabled; design.js enables them
        // once the dialog is wired, so a script that did not run leaves no dead control.
        assert_contains('data-bs-target="#clara-move" disabled', $body);
        assert_same(0, preg_match_all('/<button [^>]*data-bs-target="#clara-move"(?![^>]*\bdisabled\b)[^>]*>/', $body));
        assert_same(1, preg_match('/<select [^>]*id="move-instrument"[^>]*\bdisabled\b/', $body));
    });

    it('offers no Move while production has no open staging set (§6.7)', function (): void {
        sign_in();
        m4_queue('production', false);

        $body = m4_get('/projects/3/design/instruments/7')->body();

        assert_not_contains('data-clara-move-field', $body);
        assert_not_contains('id="clara-move"', $body);
    });

    it('serves every instrument with its fields in the Designer\'s data region, unmapped ones too', function (): void {
        sign_in();
        m4_queue();

        $response = router_for(http_request('GET', '/projects/3/design/instruments/7', json_headers()))->dispatch();
        $data = json_decode($response->body(), true);

        assert_same(200, $response->status());
        assert_same(['intake', 'scores'], array_column($data['instruments'], 'name'));
        assert_same([7, 12], array_column($data['instruments'], 'id'));
        assert_same([['id' => 11, 'field_name' => 'record_id', 'field_label' => 'Record']], $data['instruments'][1]['fields']);
        assert_same(['record_id', 'bmi'], array_column($data['instruments'][0]['fields'], 'field_name'));
        // One read per instrument, also when the references need it too.
        assert_same(1, m4_count('GET', '/instruments/7/fields'));
        assert_same(4, count($data['references']));
    });

    it('moves with exactly the target and the field to go before, then shows the target', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/14/move', ['id' => 14, 'field_name' => 'bmi', 'position' => 1]);

        $response = m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'name' => 'bmi',
            'target_iid' => '12', 'before_field_id' => '11'], 'relocate_field');

        assert_same(['instrument_id' => 12, 'before_field_id' => 11], m4_body('POST', '/fields/14/move'));
        assert_same('/projects/3/design/instruments/12', $response->headers()['Location']);
        assert_contains('Field \"bmi\" moved.', json_encode($_SESSION['_flash']));
    });

    it('sends the end of the instrument as before_field_id null, provisional ids as they are', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/14/move', ['id' => 14]);

        $response = m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'name' => 'bmi',
            'target_iid' => '-1', 'before_field_id' => 'end'], 'relocate_field');

        assert_same(['instrument_id' => -1, 'before_field_id' => null], m4_body('POST', '/fields/14/move'));
        assert_same('/projects/3/design/instruments/-1', $response->headers()['Location']);
    });

    it('stays on this instrument with the API\'s reason when the move is refused (GD-8)', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/11/move', ['error' => 'invalid_request', 'status' => 400,
            'message' => 'the move would change the record identifier field (GD-8)'], 400);

        $response = m4_post('/projects/3/design/instruments/7', ['field_id' => '11', 'name' => 'record_id',
            'target_iid' => '12', 'before_field_id' => 'end'], 'relocate_field');

        assert_same('/projects/3/design/instruments/7', $response->headers()['Location']);
        assert_contains('the move would change the record identifier field (GD-8)', json_encode($_SESSION['_flash']));
    });

    it('writes nothing for a dialog posted without a field or a target', function (): void {
        sign_in();

        $response = m4_post('/projects/3/design/instruments/7', ['field_id' => '', 'name' => '',
            'target_iid' => '12', 'before_field_id' => 'end'], 'relocate_field');

        assert_same('/projects/3/design/instruments/7', $response->headers()['Location']);
        assert_same(0, m4_count('POST', '/move'));
    });
});
