<?php
// M4 — Setup and the Instrument Designer (Plan/Web_Implementation.md §6): §6.2 Setup, §7 the
// designer, §6.6 the mode card, §6.7 staging, §6.8 analysis-mode acknowledgement. What is
// asserted here is what review would otherwise check by hand: the project_admin gate, each
// write's exact attribute set (§7 rule 1), the full ordered lists the order endpoints take,
// staging controls by mode, the breaking-change replay, and the mode card's is_admin gate.

declare(strict_types=1);

use Clara\Controllers\DesignController;
use Clara\Controllers\SetupController;
use Clara\Controllers\StructureController;

const M4_CSRF = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

function m4_detail(bool $projectAdmin = true): array
{
    return [
        'id' => 3, 'project_name' => '8DISC', 'organization' => 'NAT EU', 'record_count' => 1,
        'arms' => [['arm_num' => 1, 'name' => '', 'events' => []]],
        'instruments' => [['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 2]],
        'permissions' => ['project_admin' => $projectAdmin,
            'arms' => [['arm_num' => 1, 'data_access_level' => $projectAdmin ? 'edit_survey_responses' : 'view_edit',
                'export_level' => 'export_full']]],
    ];
}

function m4_arms(): array
{
    return [['id' => 5, 'arm_num' => 1, 'name' => '', 'events' => [
        ['id' => 4, 'event_name' => 'baseline', 'unique_event_name' => 'baseline_arm_1', 'period' => 0,
            'safe_region_start' => null, 'safe_region_end' => null, 'position' => 1],
        ['id' => 8, 'event_name' => 'follow_up', 'unique_event_name' => 'follow_up_arm_1', 'period' => 90,
            'safe_region_start' => -2, 'safe_region_end' => 3, 'position' => 2],
        ['id' => 9, 'event_name' => 'screening', 'unique_event_name' => 'screening_arm_1', 'period' => null,
            'safe_region_start' => null, 'safe_region_end' => null, 'position' => 3],
        ['id' => 10, 'event_name' => 'extra', 'unique_event_name' => 'extra_arm_1', 'period' => null,
            'safe_region_start' => null, 'safe_region_end' => null, 'position' => 4],
    ]]];
}

function m4_instruments(): array
{
    return [
        ['id' => 7, 'name' => 'intake', 'position' => 1, 'field_count' => 2, 'is_survey' => false, 'branching_logic' => ''],
        ['id' => 12, 'name' => 'scores', 'position' => 2, 'field_count' => 1, 'is_survey' => true, 'branching_logic' => '[baseline_arm_1][age] > 18'],
    ];
}

function m4_fields(): array
{
    return [
        ['id' => 11, 'field_name' => 'record_id', 'field_label' => 'Record', 'field_type' => 'text', 'section_header' => '',
            'choices' => '', 'field_note' => '', 'validation_type' => '', 'validation_format' => null, 'validation_min' => null,
            'validation_max' => null, 'required' => false, 'branching_logic' => '', 'calculation' => '', 'matrix_group' => '',
            'personal_information' => false, 'direct_identifier' => false, 'position' => 1],
        ['id' => 14, 'field_name' => 'bmi', 'field_label' => 'BMI <b>', 'field_type' => 'calculated', 'section_header' => '',
            'choices' => '', 'field_note' => '', 'validation_type' => '', 'validation_format' => null, 'validation_min' => null,
            'validation_max' => null, 'required' => false, 'branching_logic' => '', 'calculation' => '[baseline_arm_1][w] / 2',
            'matrix_group' => '', 'personal_information' => false, 'direct_identifier' => false, 'position' => 2],
    ];
}

/** The reads a structure page spends besides its own, in fake-transport match order. */
function m4_queue(string $mode = 'development', bool $stagingOpen = false, bool $projectAdmin = true, ?array $instruments = null): void
{
    queue_shell();
    api_route('/api/v1/projects/3/mode', ['mode' => $mode, 'staging_open' => $stagingOpen]);
    api_route('/api/v1/projects/3/staging', ['open' => true, 'opened_at' => '2026-10-03 08:00:00', 'opened_by' => 1, 'changes' => [
        ['kind' => 'field_added', 'object' => 'intake.age', 'breaking' => false],
        ['kind' => 'field_deleted', 'object' => 'intake.old_score', 'breaking' => true, 'reason' => 'deleting a field makes its stored values inaccessible'],
    ]]);
    api_route('/api/v1/projects/3/arms', m4_arms());
    api_route('/api/v1/projects/3/instruments/7/fields', m4_fields());
    api_route('/api/v1/projects/3/instruments/12/fields', [m4_fields()[0]]);
    api_route('/api/v1/projects/3/instruments/-1/fields', []);
    api_route('/api/v1/projects/3/instruments', $instruments ?? m4_instruments());
    api_route('/api/v1/projects/3/instrument-event-mapping', [['arm_num' => 1, 'mapping' => [
        'intake' => ['baseline_arm_1', 'follow_up_arm_1'], 'scores' => []]]]);
    api_route('/api/v1/projects/3/record-status', [['record_id' => '8DISC001', 'events' => []], ['record_id' => '8DISC<2>', 'events' => []]]);
    api_route('/api/v1/validationTypes', [['name' => 'integer', 'regex' => '', 'builtin' => true], ['name' => 'email', 'regex' => '.+@.+', 'builtin' => false]]);
    api_route('/api/v1/projects/3', m4_detail($projectAdmin));
}

function m4_get(string $path, array $query = []): \Clara\Response
{
    return router_for(http_request('GET', $path, browser_headers(), [], $query))->dispatch();
}

function m4_post(string $path, array $fields, string $action): \Clara\Response
{
    // The flashed line of a mutation is translated, which reads the bundle once.
    queue_shell();

    return router_for(http_request('POST', $path, browser_headers(), ['csrf_token' => M4_CSRF] + $fields, ['action' => $action]))->dispatch();
}

/** The decoded JSON body of the last call with this method whose path ends with the suffix. */
function m4_body(string $method, string $pathSuffix): ?array
{
    $found = null;
    foreach ($GLOBALS['api_calls'] as $call) {
        $path = (string) parse_url($call['url'], PHP_URL_PATH);
        if ($call['method'] === $method && str_ends_with($path, $pathSuffix)) {
            $found = $call;
        }
    }
    if ($found === null) {
        throw new TestFailure("no {$method} call to …{$pathSuffix}");
    }

    return $found['body'] === null ? null : json_decode($found['body'], true);
}

function m4_count(string $method, string $pathSuffix): int
{
    return count(array_filter($GLOBALS['api_calls'], static fn (array $c): bool =>
        $c['method'] === $method && str_ends_with((string) parse_url($c['url'], PHP_URL_PATH), $pathSuffix)));
}

describe('Setup page (§6.2, REQ-UI-018)', function (): void {
    it('is a project_admin page: a member without it gets the refusal page, and no structure read', function (): void {
        sign_in();
        m4_queue(projectAdmin: false);

        $response = m4_get('/projects/3/setup');

        assert_contains('You do not have permission', $response->body());
        assert_not_contains('clara-arms', $response->body());
        assert_same(0, m4_count('GET', '/arms'));
    });

    it('renders the four blocks with arm tabs, arm 1 active, and Setup/Design in the panel', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/setup')->body();

        assert_contains('table table-sm align-middle clara-arms', $body);
        assert_contains('clara-instruments', $body);
        assert_contains('id="arm-pane-1"', $body);
        assert_contains('class="tab-pane fade show active" id="arm-pane-1"', $body);
        assert_contains('name="map[7][]" value="4"', $body);
        assert_contains('href="/projects/3/setup"', $body);
        assert_contains('href="/projects/3/design"', $body);
        // Development: edits apply directly — no staging controls (§6.7).
        assert_not_contains('Start staging', $body);
        assert_not_contains('clara-staging-banner', $body);
        assert_same(0, m4_count('GET', '/staging'));
    });

    it('checks exactly the mapped pairs of the matrix (§6.2 D)', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/setup')->body();

        assert_contains('data-pair="intake|follow_up_arm_1" checked', $body);
        assert_not_contains('data-pair="scores|baseline_arm_1" checked', $body);
    });

    it('offers reorder controls only where the canonical order leaves a choice (GD-15)', function (): void {
        $flags = SetupController::withMoveFlags(m4_arms()[0]['events']);

        // baseline (period 0) and follow_up (90) are fixed by their timepoints…
        assert_same([false, false], [$flags[0]['can_move_up'], $flags[0]['can_move_down']]);
        assert_same([false, false], [$flags[1]['can_move_up'], $flags[1]['can_move_down']]);
        // …the two no-timepoint events can trade places.
        assert_same([false, true], [$flags[2]['can_move_up'], $flags[2]['can_move_down']]);
        assert_same([true, false], [$flags[3]['can_move_up'], $flags[3]['can_move_down']]);
    });

    it('adds an event with exactly the §4.9 attributes, a blank timepoint as null', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/events', ['id' => 20], 201);

        $response = m4_post('/projects/3/setup', ['arm_num' => '1', 'event_name' => 'visit', 'period' => '',
            'safe_region_start' => '-2', 'safe_region_end' => '3'], 'add_event');

        assert_same(302, $response->status());
        assert_same('/projects/3/setup?arm=1', $response->headers()['Location']);
        assert_same(['arm_num' => 1, 'event_name' => 'visit', 'period' => null, 'safe_region_start' => -2,
            'safe_region_end' => 3], m4_body('POST', '/projects/3/events'));
    });

    it('moves a no-timepoint event by sending the arm\'s full ordered id list', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/arms', m4_arms());
        api_route('/api/v1/projects/3/events/order', ['ok' => true]);

        m4_post('/projects/3/setup', ['event_id' => '10', 'arm_num' => '1', 'direction' => 'up'], 'move_event');

        assert_same(['arm_num' => 1, 'order' => [4, 8, 10, 9]], m4_body('PUT', '/events/order'));
    });

    it('refuses to move an event whose place its timepoint fixes — no write at all', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/arms', m4_arms());

        m4_post('/projects/3/setup', ['event_id' => '8', 'arm_num' => '1', 'direction' => 'down'], 'move_event');

        assert_same(0, m4_count('PUT', '/events/order'));
    });

    it('applies the tabbed arm\'s full matrix by name, every instrument present (§4.12)', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/arms', m4_arms());
        api_route('/api/v1/projects/3/instruments', m4_instruments());
        api_route('/api/v1/projects/3/instrument-event-mapping', ['ok' => true]);

        m4_post('/projects/3/setup', ['arm_num' => '1', 'map' => ['7' => ['4', '9', '999']]], 'save_mapping');

        assert_same(['arm_num' => 1, 'mapping' => ['intake' => ['baseline_arm_1', 'screening_arm_1'], 'scores' => []]],
            m4_body('PUT', '/instrument-event-mapping'));
    });

    it('updates an instrument with exactly name, is_survey and branching_logic (§4.10)', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/12', ['id' => 12]);

        m4_post('/projects/3/setup', ['instrument_id' => '12', 'name' => 'scores', 'is_survey' => '1',
            'branching_logic' => ' [baseline_arm_1][age] > 18 '], 'update_instrument');

        assert_same(['name' => 'scores', 'is_survey' => true, 'branching_logic' => '[baseline_arm_1][age] > 18'],
            m4_body('PUT', '/instruments/12'));
    });

    it('says when the last event was reset to baseline instead of deleted (REQ-API-133)', function (): void {
        sign_in();
        api_route('/api/v1/events/4', ['id' => 4, 'event_name' => 'baseline']);

        m4_post('/projects/3/setup', ['event_id' => '4', 'arm_num' => '1', 'name' => 'visit'], 'delete_event');

        assert_same(null, m4_body('DELETE', '/api/v1/events/4'));
        assert_contains('reset to baseline', json_encode($_SESSION['_flash']));
    });

    it('shows a duplicate-name refusal as the API\'s reason (§3.4)', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments', ['error' => 'conflict', 'message' => "instrument 'intake' already exists in this project", 'status' => 409], 409);

        m4_post('/projects/3/setup', ['name' => 'intake'], 'add_instrument');

        assert_contains("instrument 'intake' already exists", json_encode($_SESSION['_flash']));
        assert_true(!isset($_SESSION['_stash']['breaking']), 'a plain conflict is not a breaking change');
    });

    it('serves the active references as its data region, never a page (REQ-UI-044)', function (): void {
        sign_in();
        m4_queue();

        $response = router_for(http_request('GET', '/projects/3/setup', json_headers()))->dispatch();
        $data = json_decode($response->body(), true);

        assert_same(200, $response->status());
        $refs = array_column($data['references'], 'reference');
        // intake is mapped to two events; scores to none, so none of its fields is active.
        assert_same(['[baseline_arm_1][record_id]', '[baseline_arm_1][bmi]', '[follow_up_arm_1][record_id]', '[follow_up_arm_1][bmi]'], $refs);
        assert_same(0, m4_count('GET', '/instruments/12/fields'));
    });

    it('refuses the data region to a member without project_admin', function (): void {
        sign_in();
        m4_queue(projectAdmin: false);

        $response = router_for(http_request('GET', '/projects/3/setup', json_headers()))->dispatch();

        assert_same(403, $response->status());
        assert_same('forbidden', json_decode($response->body(), true)['error']);
    });
});

describe('staging in production (§6.7, REQ-UI-034)', function (): void {
    it('offers Start staging and no edit control while no set is open', function (): void {
        sign_in();
        m4_queue('production', false);

        $body = m4_get('/projects/3/setup')->body();

        assert_contains('action=staging_start', $body);
        assert_not_contains('action=add_event', $body);
        assert_not_contains('action=save_mapping', $body);
        assert_not_contains('name="map[', $body);
    });

    it('shows the banner and a commit dialog that requires the acknowledgement for breaking changes', function (): void {
        sign_in();
        m4_queue('production', true);

        $body = m4_get('/projects/3/setup')->body();

        assert_contains('clara-staging-banner', $body);
        assert_contains('action=staging_discard', $body);
        assert_contains('action=add_event', $body);
        assert_contains('intake.old_score', $body);
        assert_contains('deleting a field makes its stored values inaccessible', $body);
        assert_contains('name="acknowledge_breaking" value="1" required', $body);
        assert_contains('Its 2 staged changes are thrown away', $body);
    });

    it('commits with the acknowledgement only when it was ticked', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/staging/commit', ['applied' => ['instruments' => 0, 'fields' => 1, 'events' => 0, 'mapping_pairs' => 0]]);

        m4_post('/projects/3/setup', [], 'staging_commit');
        assert_same([], m4_body('POST', '/staging/commit'));

        m4_post('/projects/3/setup', ['acknowledge_breaking' => '1'], 'staging_commit');
        assert_same(['acknowledge_breaking' => true], m4_body('POST', '/staging/commit'));
    });

    it('reopens the commit dialog after a 409', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/staging/commit', ['error' => 'conflict', 'message' => 'the staged set contains breaking changes', 'status' => 409], 409);

        m4_post('/projects/3/design', [], 'staging_commit');
        m4_queue('production', true);
        $body = m4_get('/projects/3/design')->body();

        assert_contains('id="clara-commit" tabindex="-1" aria-labelledby="clara-commit-title" aria-hidden="true"
         data-clara-autoshow', $body);
    });

    it('starts staging with an empty JSON object, never an array', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/staging', ['opened_at' => 'x', 'opened_by' => 1], 201);

        m4_post('/projects/3/design', [], 'staging_start');

        $calls = array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool => $c['method'] === 'POST'));
        assert_same('{}', $calls[0]['body']);
    });
});

describe('analysis-mode acknowledgement (§6.8, REQ-UI-037)', function (): void {
    it('turns a breaking 409 into a question that replays the identical request with the acknowledgement', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/11', ['error' => 'conflict', 'status' => 409,
            'message' => 'breaking change: field_deleted on intake.age (deleting a field makes its stored values inaccessible) — resend with acknowledge_breaking true to apply it'], 409);

        $response = m4_post('/projects/3/design/instruments/7', ['field_id' => '11', 'name' => 'age'], 'delete_field');
        assert_same('/projects/3/design/instruments/7', $response->headers()['Location']);
        assert_true(!isset($_SESSION['_flash']), 'the question replaces the failure line');

        m4_queue('analysis');
        $body = m4_get('/projects/3/design/instruments/7')->body();

        assert_contains('id="clara-breaking"', $body);
        assert_contains('action="/projects/3/design/instruments/7?action=delete_field"', $body);
        assert_contains('name="field_id" value="11"', $body);
        assert_contains('name="acknowledge_breaking" value="1"', $body);
        assert_contains('field_deleted on intake.age (deleting a field makes its stored values inaccessible)', $body);
        assert_not_contains('resend with acknowledge_breaking', $body);
    });

    it('sends the acknowledgement on the replay and says the change is live', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/11', '', 204);

        m4_post('/projects/3/design/instruments/7', ['field_id' => '11', 'name' => 'age', 'acknowledge_breaking' => '1'], 'delete_field');

        assert_same(['acknowledge_breaking' => true], m4_body('DELETE', '/fields/11'));
        assert_contains('The change is live now.', json_encode($_SESSION['_flash']));
    });

    it('replays nested fields exactly as posted', function (): void {
        assert_same([['arm_num', '1'], ['map[7][]', '4'], ['map[7][]', '9'], ['map[12][]', '8']],
            StructureController::flattenFields(['arm_num' => '1', 'map' => ['7' => ['4', '9'], '12' => ['8']]]));
    });
});

describe('Instrument Designer (§7, REQ-UI-021…023)', function (): void {
    it('lists the instruments with links into the field editor', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/design')->body();

        assert_contains('href="/projects/3/design/instruments/12"', $body);
        assert_contains('clara-design-instruments', $body);
    });

    it('renders the field table escaped, and the add form on ?field=new', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/design/instruments/7', ['field' => 'new'])->body();

        assert_contains('BMI &lt;b&gt;', $body);
        assert_contains('id="field-form"', $body);
        assert_contains('<option value="email">email</option>', $body);
        // A new field reads no records: the test panel belongs to a stored calculated field.
        assert_same(0, m4_count('GET', '/record-status'));
    });

    it('answers 404 for an instrument the project does not have', function (): void {
        sign_in();
        m4_queue();

        $response = m4_get('/projects/3/design/instruments/99');

        assert_contains('does not exist', $response->body());
        assert_not_contains('clara-fields', $response->body());
    });

    it('accepts a provisional (negative) instrument id while staging is open (§7 rule 3)', function (): void {
        sign_in();
        m4_queue('production', true, instruments: array_merge(m4_instruments(),
            [['id' => -1, 'name' => 'staged', 'position' => 3, 'field_count' => 0]]));

        $response = m4_get('/projects/3/design/instruments/-1');

        assert_same(200, $response->status());
        assert_contains('Fields of &quot;staged&quot;', $response->body());
    });

    it('creates a field with exactly the §4.11 attributes, choices encoded', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields', ['id' => 30], 201);

        $response = m4_post('/projects/3/design/instruments/7', [
            'field_id' => '0', 'field_name' => 'sex', 'field_label' => 'Sex', 'field_type' => 'radio',
            'section_header' => '', 'field_note' => '', 'validation_type' => '', 'validation_format' => '',
            'validation_min' => '', 'validation_max' => '', 'required' => '1', 'branching_logic' => '',
            'calculation' => 'ignored', 'matrix_group' => 'ignored',
            'choice_code' => ['1', '2', ''], 'choice_label' => ['Male', 'Female', ''],
        ], 'save_field');

        assert_same('/projects/3/design/instruments/7?field=30', $response->headers()['Location']);
        $body = m4_body('POST', '/instruments/7/fields');
        assert_same(['field_name', 'field_label', 'field_type', 'section_header', 'choices', 'field_note',
            'validation_type', 'validation_format', 'validation_min', 'validation_max', 'required',
            'branching_logic', 'calculation', 'matrix_group', 'personal_information', 'direct_identifier'], array_keys($body));
        assert_same('1$Male##2$Female', $body['choices']);
        assert_same('', $body['calculation']);
        assert_same('', $body['matrix_group']);
        assert_same(true, $body['required']);
    });

    it('leaves direct_identifier to the API preset on a new identifier-typed field, unless cleared', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields', ['id' => 31], 201);
        $fields = ['field_id' => '0', 'field_name' => 'mail', 'field_type' => 'text', 'validation_type' => 'email'];

        m4_post('/projects/3/design/instruments/7', $fields, 'save_field');
        assert_true(!array_key_exists('direct_identifier', m4_body('POST', '/instruments/7/fields')), 'preset left to the API');

        m4_post('/projects/3/design/instruments/7', $fields + ['direct_identifier_cleared' => '1'], 'save_field');
        assert_same(false, m4_body('POST', '/instruments/7/fields')['direct_identifier']);
    });

    it('keeps the designer\'s draft and shows the validation reason when a save is refused', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/14', ['error' => 'validation_error',
            'message' => 'calculation is not valid: unknown field [baseline_arm_1][nope]', 'status' => 400], 400);

        m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'field_name' => 'bmi', 'field_type' => 'calculated',
            'calculation' => '[baseline_arm_1][nope] * 2'], 'save_field');
        m4_queue();
        $body = m4_get('/projects/3/design/instruments/7', ['field' => '14'])->body();

        assert_contains('unknown field [baseline_arm_1][nope]', $body);
        assert_contains('[baseline_arm_1][nope] * 2</textarea>', $body);
    });

    it('moves a field with the full ordered list from GET …/fields, never GET …/fields/order', function (): void {
        sign_in();
        api_route('/api/v1/projects/3/instruments/7/fields/order', ['ok' => true]);
        api_route('/api/v1/projects/3/instruments/7/fields', m4_fields());

        m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'direction' => 'up'], 'move_field');

        assert_same(['order' => [14, 11]], m4_body('PUT', '/fields/order'));
        assert_same(0, m4_count('GET', '/fields/order'));
    });

    it('offers the calculation test with the visible records, escaped', function (): void {
        sign_in();
        m4_queue();

        $body = m4_get('/projects/3/design/instruments/7', ['field' => '14'])->body();

        assert_contains('clara-calc-test', $body);
        assert_contains('<option value="8DISC&lt;2&gt;">8DISC&lt;2&gt;</option>', $body);
    });

    it('dry-runs the stored expression with an empty object and shows every problem (REQ-VAL-038)', function (): void {
        sign_in();
        api_route('/records/8DISC%3C2%3E/fields/14/test', ['value' => '', 'problems' => [
            ['operand' => '[baseline_arm_1][w]', 'problem' => 'missing_value'],
            ['operand' => '[baseline_arm_1][x]', 'problem' => 'division_by_zero']]]);

        m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'record' => '8DISC<2>', 'expression' => ''], 'test_calc');
        $call = array_values(array_filter($GLOBALS['api_calls'], static fn (array $c): bool => str_contains($c['url'], '/test')))[0];
        assert_same('{}', $call['body']);

        m4_queue();
        $body = m4_get('/projects/3/design/instruments/7', ['field' => '14'])->body();
        assert_contains('missing value', $body);
        assert_contains('division by zero', $body);
        assert_contains('[baseline_arm_1][x]', $body);
    });

    it('sends a draft expression to the test when one is given', function (): void {
        sign_in();
        api_route('/fields/14/test', ['value' => '9', 'problems' => []]);

        m4_post('/projects/3/design/instruments/7', ['field_id' => '14', 'record' => '8DISC001', 'expression' => '4 + 5'], 'test_calc');

        assert_same(['expression' => '4 + 5'], m4_body('POST', '/fields/14/test'));
    });

    it('round-trips the choice encoding', function (): void {
        assert_same('1$Yes##0$No', DesignController::encodeChoices(['1', '0', ' '], ['Yes', 'No', '']));
        assert_same([['code' => '1', 'label' => 'Yes'], ['code' => '0', 'label' => 'a$b']],
            DesignController::decodeChoices('1$Yes##0$a$b'));
    });
});

describe('mode card (§6.6, REQ-UI-033)', function (): void {
    $overview = static function (string $mode, bool $staging): string {
        queue_shell();
        api_route('/api/v1/projects/3/mode', ['mode' => $mode, 'staging_open' => $staging]);
        api_route('/api/v1/projects/3', m4_detail());

        return m4_get('/projects/3/overview')->body();
    };

    it('is absent for a project_admin who is not an installation administrator', function () use ($overview): void {
        sign_in();

        assert_not_contains('clara-mode-card', $overview('development', false));
    });

    it('offers only the allowed transitions to is_admin', function () use ($overview): void {
        sign_in(['is_admin' => 1]);

        $dev = $overview('development', false);
        assert_contains('Move to production — keep stored data', $dev);
        assert_contains('name="confirm_delete" value="1" required', $dev);
        assert_not_contains('Change to Analysis', $dev);

        resetApi();
        $production = $overview('production', false);
        assert_contains('Change to Development', $production);
        assert_contains('Change to Analysis', $production);
    });

    it('offers no transition while a staging set is open', function () use ($overview): void {
        sign_in(['is_admin' => 1]);

        $body = $overview('production', true);

        assert_contains('clara-mode-card', $body);
        assert_not_contains('action=set_mode', $body);
        assert_contains('href="/projects/3/setup"', $body);
    });

    it('deletes data on the way to production only with the second confirmation', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/projects/3/mode', ['mode' => 'development', 'staging_open' => false]);

        m4_post('/projects/3/overview', ['mode' => 'production', 'keep_data' => '0'], 'set_mode');
        assert_same(0, m4_count('PUT', '/mode'));

        resetApi();
        api_route('/api/v1/projects/3/mode', ['mode' => 'development', 'staging_open' => false]);
        m4_post('/projects/3/overview', ['mode' => 'production', 'keep_data' => '0', 'confirm_delete' => '1'], 'set_mode');
        assert_same(['mode' => 'production', 'keep_data' => false], m4_body('PUT', '/mode'));
    });

    it('sends keep_data only for development → production', function (): void {
        sign_in(['is_admin' => 1]);
        api_route('/api/v1/projects/3/mode', ['mode' => 'production', 'staging_open' => false]);

        m4_post('/projects/3/overview', ['mode' => 'analysis', 'keep_data' => '1'], 'set_mode');

        assert_same(['mode' => 'analysis'], m4_body('PUT', '/mode'));
    });

    it('is refused to a non-admin by the route guard, before any API call', function (): void {
        sign_in();

        $response = m4_post('/projects/3/overview', ['mode' => 'production', 'keep_data' => '1'], 'set_mode');

        assert_true($response->status() >= 300, 'not handled');
        assert_same(0, m4_count('PUT', '/mode'));
    });
});
