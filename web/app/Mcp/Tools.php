<?php
// The assistant's toolset (AI_Assistant_Design.md §3, REQ-AI-008/009): every tool maps 1:1 onto
// a call the page controllers already make — the assistant adds no new write path to the API.
// Each definition carries the mode gate (`read` | `write`), checked here before execution
// (REQ-AI-006): the session's read-only flag is enforced at this dispatcher, never only in what
// the browser is shown. A tool result is always an MCP result object — API failures come back as
// `isError` results carrying the API's stable code so the model can see and re-plan, never as
// stack details (REQ-CFG-005).

declare(strict_types=1);

namespace Clara\Mcp;

use Clara\ApiClient;
use Clara\ApiException;
use Clara\Logger;
use Clara\Session;

final class Tools
{
    /** Rows an export-style tool hands to the model at most (design §3: bounded for context). */
    private const ROW_CAP = 200;

    /** Rows a statistics tool reads through at most. */
    private const STATS_ROW_CAP = 50_000;

    /** Rows one `create_records` call may import (the data API's own limit is respected too). */
    private const IMPORT_CAP = 500;

    public function __construct(
        private readonly ApiClient $api,
        private readonly Logger $logger
    ) {}

    /**
     * The MCP tool definitions for `tools/list`. Descriptions are written to steer a small
     * tool-calling model: what the tool answers, and what its arguments mean.
     *
     * @return list<array<string, mixed>>
     */
    public function definitions(): array
    {
        return [
            $this->def('list_projects', 'List every project the administrator can see: id, name, organization and record/instrument/field counts.', [], 'read', []),
            $this->def('get_project_setup', 'One project\'s structure: mode, arms with their events (unique_event_name identifies an event), instruments (id, name, is_survey, repeating_events) and the instrument-to-event mapping per arm.', [
                'project_id' => ['type' => 'integer', 'description' => 'Numeric project id'],
            ], 'read', ['project_id']),
            $this->def('get_record_status', 'The Record Status Dashboard of a project: one row per record with each instrument\'s completion. Rows are capped; the result says when truncated.', [
                'project_id' => ['type' => 'integer'],
            ], 'read', ['project_id']),
            $this->def('export_records', 'Export data rows of a project (REDCap semantics): pick fields by field_name, optionally one event. Returns up to 200 rows.', [
                'project_id' => ['type' => 'integer'],
                'fields' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'field_name values to export'],
                'event' => ['type' => 'string', 'description' => 'Optional unique_event_name filter'],
            ], 'read', ['project_id', 'fields']),
            $this->def('marginal_stats', 'Marginal distribution of one field across all records: value counts and shares; for numeric fields also n, mean, SD, min, max and histogram bins.', [
                'project_id' => ['type' => 'integer'],
                'field' => ['type' => 'string', 'description' => 'field_name to summarise'],
                'event' => ['type' => 'string', 'description' => 'Optional unique_event_name filter'],
            ], 'read', ['project_id', 'field']),
            $this->def('ttest', 'Welch two-sample t-test: compares a numeric value_field between the two groups formed by group_field (must have exactly two distinct values). Missing values are skipped.', [
                'project_id' => ['type' => 'integer'],
                'value_field' => ['type' => 'string'],
                'group_field' => ['type' => 'string'],
                'event' => ['type' => 'string', 'description' => 'Optional unique_event_name filter'],
            ], 'read', ['project_id', 'value_field', 'group_field']),
            $this->def('create_records', 'Create or update records in bulk (REDCap import semantics): each object holds the record id field plus any field values. Up to 500 rows per call.', [
                'project_id' => ['type' => 'integer'],
                'records' => ['type' => 'array', 'items' => ['type' => 'object', 'additionalProperties' => true]],
            ], 'write', ['project_id', 'records']),
            $this->def('add_event', 'Add an event to an arm (Setup): arm_num plus a readable event_name.', [
                'project_id' => ['type' => 'integer'],
                'arm_num' => ['type' => 'integer'],
                'event_name' => ['type' => 'string'],
            ], 'write', ['project_id', 'arm_num', 'event_name']),
            $this->def('add_instrument', 'Add an instrument by name to a project (Setup).', [
                'project_id' => ['type' => 'integer'],
                'name' => ['type' => 'string'],
            ], 'write', ['project_id', 'name']),
            $this->def('set_instrument_survey', 'Turn the survey flag of an instrument on or off (Design).', [
                'project_id' => ['type' => 'integer'],
                'instrument_id' => ['type' => 'integer'],
                'is_survey' => ['type' => 'boolean'],
            ], 'write', ['project_id', 'instrument_id', 'is_survey']),
            $this->def('set_instrument_repeatable', 'Set which events an instrument repeats in (empty list turns repeating off).', [
                'project_id' => ['type' => 'integer'],
                'instrument_id' => ['type' => 'integer'],
                'events' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'unique_event_name values'],
            ], 'write', ['project_id', 'instrument_id', 'events']),
            $this->def('assign_instruments_to_events', 'Replace one arm\'s instrument-to-event mapping: object of instrument name to list of unique_event_name.', [
                'project_id' => ['type' => 'integer'],
                'arm_num' => ['type' => 'integer'],
                'mapping' => ['type' => 'object', 'additionalProperties' => ['type' => 'array', 'items' => ['type' => 'string']]],
            ], 'write', ['project_id', 'arm_num', 'mapping']),
        ];
    }

    /**
     * Execute one tool call. The write-mode gate is this method's first act on a `write` tool
     * (REQ-AI-006); everything after it runs as the acting administrator through the same API
     * client every page uses, so the API's own permission checks and audit trail apply unchanged.
     *
     * @param array<string, mixed> $args
     * @return array{content: list<array<string, mixed>>, structuredContent?: array<string, mixed>, isError?: bool}
     */
    public function call(string $name, array $args): array
    {
        $definition = null;
        foreach ($this->definitions() as $candidate) {
            if ($candidate['name'] === $name) {
                $definition = $candidate;
                break;
            }
        }
        if ($definition === null) {
            return self::failure('unknown_tool', 'There is no tool named "' . $name . '".');
        }

        if ($definition['x_mode'] === 'write' && !Session::assistantWriteMode()) {
            return self::failure('read_only',
                'The assistant session is in read-only mode, so this change was refused. Ask the administrator to enable read-write mode on the Assistant page first.');
        }

        try {
            return match ($name) {
                'list_projects' => $this->listProjects(),
                'get_project_setup' => $this->getProjectSetup($args),
                'get_record_status' => $this->getRecordStatus($args),
                'export_records' => $this->exportRecords($args),
                'marginal_stats' => $this->marginalStats($args),
                'ttest' => $this->ttest($args),
                'create_records' => $this->createRecords($args),
                'add_event' => $this->addEvent($args),
                'add_instrument' => $this->addInstrument($args),
                'set_instrument_survey' => $this->setInstrumentSurvey($args),
                'set_instrument_repeatable' => $this->setInstrumentRepeatable($args),
                'assign_instruments_to_events' => $this->assignInstrumentsToEvents($args),
                default => self::failure('unknown_tool', 'unreachable'),
            };
        } catch (ApiException $e) {
            // The API's stable code travels to the model; its human-readable message is safe to
            // show (it names rejected attributes, never internals).
            $this->logger->info('assistant tool refused by api', [
                'tool' => $name, 'code' => $e->code(), 'status' => $e->status(),
            ]);

            return self::failure($e->code(), $e->getMessage() !== '' ? $e->getMessage() : $e->code());
        } catch (\InvalidArgumentException $e) {
            return self::failure('invalid_request', $e->getMessage());
        }
    }

    // --- read tools (REQ-AI-008) -----------------------------------------------------------

    /** @return array<string, mixed> */
    private function listProjects(): array
    {
        $rows = [];
        foreach (self::rowsOf($this->api->get('/api/v1/projects')) as $project) {
            $rows[] = [
                'id' => (int) ($project['id'] ?? 0),
                'name' => (string) ($project['project_name'] ?? ''),
                'organization' => (string) ($project['organization'] ?? ''),
                'records' => (int) ($project['record_count'] ?? 0),
                'instruments' => (int) ($project['instrument_count'] ?? 0),
                'fields' => (int) ($project['field_count'] ?? 0),
            ];
        }

        return self::success(
            count($rows) . ' project(s): ' . implode('; ', array_map(static fn (array $r): string => $r['id'] . ' ' . $r['name'], $rows)),
            ['display' => 'table', 'columns' => ['id', 'name', 'organization', 'records', 'instruments', 'fields'], 'rows' => $rows]
        );
    }

    /** @return array<string, mixed> */
    private function getProjectSetup(array $args): array
    {
        $projectId = self::projectId($args);
        $base = '/api/v1/projects/' . $projectId;

        $detail = $this->api->get($base);
        $arms = self::rowsOf($this->api->get($base . '/arms'));
        $instruments = self::rowsOf($this->api->get($base . '/instruments'));
        $mapping = self::rowsOf($this->api->get($base . '/instrument-event-mapping'));

        $summary = [
            'project' => [
                'id' => (int) ($detail['id'] ?? $projectId),
                'name' => (string) ($detail['project_name'] ?? ''),
                'mode' => (string) ($detail['mode'] ?? ''),
                'record_naming' => (string) ($detail['record_naming'] ?? ''),
            ],
            'arms' => array_map(static fn (array $arm): array => [
                'arm_num' => (int) ($arm['arm_num'] ?? 0),
                'name' => (string) ($arm['name'] ?? ''),
                'events' => array_map(static fn (array $event): array => [
                    'unique_event_name' => (string) ($event['unique_event_name'] ?? ''),
                    'event_name' => (string) ($event['event_name'] ?? ''),
                ], self::rowsOf($arm['events'] ?? [])),
            ], $arms),
            'instruments' => array_map(static fn (array $instrument): array => [
                'id' => (int) ($instrument['id'] ?? 0),
                'name' => (string) ($instrument['name'] ?? ''),
                'is_survey' => (bool) ($instrument['is_survey'] ?? false),
                'repeating_events' => array_values(array_map('strval', self::rowsOf($instrument['repeating_events'] ?? []))),
            ], $instruments),
            'mapping' => $mapping,
        ];

        return self::success(
            self::json($summary),
            ['display' => 'json']
        );
    }

    /** @return array<string, mixed> */
    private function getRecordStatus(array $args): array
    {
        $projectId = self::projectId($args);
        $rows = self::rowsOf($this->api->get('/api/v1/projects/' . $projectId . '/record-status'));
        $total = count($rows);
        $truncated = $total > self::ROW_CAP;

        return self::success(
            self::json(['total' => $total, 'truncated' => $truncated, 'rows' => array_slice($rows, 0, self::ROW_CAP)]),
            ['display' => 'json']
        );
    }

    /** @return array<string, mixed> */
    private function exportRecords(array $args): array
    {
        $projectId = self::projectId($args);
        $fields = self::stringList($args['fields'] ?? [], 'fields');
        if ($fields === []) {
            throw new \InvalidArgumentException('fields must name at least one field_name');
        }

        $params = ['content' => 'record', 'fields' => $fields];
        if (isset($args['event']) && $args['event'] !== '') {
            $params['events'] = [(string) $args['event']];
        }
        $rows = self::rowsOf($this->api->dataApi()->forMember($projectId, $params));
        $total = count($rows);
        $truncated = $total > self::ROW_CAP;
        $rows = array_slice($rows, 0, self::ROW_CAP);

        $columns = array_values(array_unique(array_merge(
            ['record_id'],
            ...array_map(static fn (array $row): array => array_keys($row), $rows === [] ? [[]] : $rows)
        )));

        return self::success(
            self::json(['total' => $total, 'truncated' => $truncated, 'rows' => $rows]),
            ['display' => 'table', 'columns' => $columns, 'rows' => $rows]
        );
    }

    /** @return array<string, mixed> */
    private function marginalStats(array $args): array
    {
        $projectId = self::projectId($args);
        $field = self::requiredString($args, 'field');

        $params = ['content' => 'record', 'fields' => [$field]];
        if (isset($args['event']) && $args['event'] !== '') {
            $params['events'] = [(string) $args['event']];
        }
        $rows = self::rowsOf($this->api->dataApi()->forMember($projectId, $params));
        if (count($rows) > self::STATS_ROW_CAP) {
            throw new \InvalidArgumentException('the project is larger than the statistics tools can read in one call (' . self::STATS_ROW_CAP . ' rows)');
        }

        $values = array_map(static fn (array $row): ?string => isset($row[$field]) && !is_array($row[$field]) ? (string) $row[$field] : null, $rows);
        $stats = Stats::marginal($values);

        $chart = $stats['is_numeric_column']
            ? ['display' => 'histogram', 'bins' => $stats['histogram'] ?? []]
            : ['display' => 'bar_chart', 'categories' => array_map(
                static fn (array $c): array => ['label' => $c['value'], 'value' => $c['count']],
                $stats['categories']
            )];

        return self::success(
            self::json(['field' => $field] + $stats),
            $chart + ['display' => $stats['is_numeric_column'] ? 'histogram' : 'bar_chart']
        );
    }

    /** @return array<string, mixed> */
    private function ttest(array $args): array
    {
        $projectId = self::projectId($args);
        $valueField = self::requiredString($args, 'value_field');
        $groupField = self::requiredString($args, 'group_field');

        $params = ['content' => 'record', 'fields' => [$valueField, $groupField]];
        if (isset($args['event']) && $args['event'] !== '') {
            $params['events'] = [(string) $args['event']];
        }
        $rows = self::rowsOf($this->api->dataApi()->forMember($projectId, $params));
        if (count($rows) > self::STATS_ROW_CAP) {
            throw new \InvalidArgumentException('the project is larger than the statistics tools can read in one call (' . self::STATS_ROW_CAP . ' rows)');
        }

        $values = array_map(static fn (array $row): ?string => isset($row[$valueField]) && !is_array($row[$valueField]) ? (string) $row[$valueField] : null, $rows);
        $groups = array_map(static fn (array $row): ?string => isset($row[$groupField]) && !is_array($row[$groupField]) ? (string) $row[$groupField] : null, $rows);

        $result = Stats::welch($values, $groups);
        if ($result === null) {
            return self::failure('not_two_groups',
                'The group field must take exactly two distinct non-empty values and each group needs at least two numeric observations.');
        }

        return self::success(
            sprintf(
                'Welch t(%s) = %s, mean difference %s, two-sided p = %s. Group "%s": n=%d mean=%s sd=%s; group "%s": n=%d mean=%s sd=%s.',
                $result['df'], $result['t'], $result['mean_difference'], $result['p_two_sided'],
                $result['group_a']['label'], $result['group_a']['n'], $result['group_a']['mean'], $result['group_a']['sd'],
                $result['group_b']['label'], $result['group_b']['n'], $result['group_b']['mean'], $result['group_b']['sd']
            ),
            ['display' => 'ttest'] + $result
        );
    }

    // --- write tools (REQ-AI-009) ------------------------------------------------------------

    /** @return array<string, mixed> */
    private function createRecords(array $args): array
    {
        $projectId = self::projectId($args);
        if (!is_array($args['records'] ?? null) || $args['records'] === []) {
            throw new \InvalidArgumentException('records must be a non-empty list of objects');
        }
        if (count($args['records']) > self::IMPORT_CAP) {
            throw new \InvalidArgumentException('at most ' . self::IMPORT_CAP . ' records per call — split the import');
        }
        foreach ($args['records'] as $index => $row) {
            if (!is_array($row) || $row === []) {
                throw new \InvalidArgumentException('records[' . $index . '] must be an object of field values');
            }
        }

        $answer = $this->api->dataApi()->forMember($projectId, [
            'content' => 'record', 'action' => 'import', 'data' => array_values($args['records']),
        ]);
        $reported = is_array($answer) ? ($answer['success_count'] ?? count($args['records'])) : count($args['records']);

        return self::success(
            self::json(['imported' => $reported, 'requested' => count($args['records'])]),
            ['display' => 'json']
        );
    }

    /** @return array<string, mixed> */
    private function addEvent(array $args): array
    {
        $projectId = self::projectId($args);
        $answer = $this->api->post('/api/v1/projects/' . $projectId . '/events', [
            'arm_num' => (int) ($args['arm_num'] ?? 0),
            'event_name' => self::requiredString($args, 'event_name'),
        ]);

        return self::success('Event created: ' . self::json($answer), ['display' => 'json']);
    }

    /** @return array<string, mixed> */
    private function addInstrument(array $args): array
    {
        $projectId = self::projectId($args);
        $answer = $this->api->post('/api/v1/projects/' . $projectId . '/instruments', [
            'name' => self::requiredString($args, 'name'),
        ]);

        return self::success('Instrument created: ' . self::json($answer), ['display' => 'json']);
    }

    /** @return array<string, mixed> */
    private function setInstrumentSurvey(array $args): array
    {
        $projectId = self::projectId($args);
        $instrumentId = (int) ($args['instrument_id'] ?? 0);
        if ($instrumentId <= 0) {
            throw new \InvalidArgumentException('instrument_id must be a positive integer');
        }

        // The endpoint whitelists exactly name/is_survey/branching_logic, so the current values
        // of the other two travel along unchanged (§4.10).
        $current = null;
        foreach (self::rowsOf($this->api->get('/api/v1/projects/' . $projectId . '/instruments')) as $instrument) {
            if ((int) ($instrument['id'] ?? 0) === $instrumentId) {
                $current = $instrument;
                break;
            }
        }
        if ($current === null) {
            return self::failure('not_found', 'No instrument ' . $instrumentId . ' in this project.');
        }

        $this->api->put('/api/v1/projects/' . $projectId . '/instruments/' . $instrumentId, [
            'name' => (string) ($current['name'] ?? ''),
            'is_survey' => (bool) ($args['is_survey'] ?? false),
            'branching_logic' => (string) ($current['branching_logic'] ?? ''),
        ]);

        return self::success('Survey flag set to ' . ((bool) ($args['is_survey'] ?? false) ? 'on' : 'off') . '.', ['display' => 'json']);
    }

    /** @return array<string, mixed> */
    private function setInstrumentRepeatable(array $args): array
    {
        $projectId = self::projectId($args);
        $instrumentId = (int) ($args['instrument_id'] ?? 0);
        if ($instrumentId <= 0) {
            throw new \InvalidArgumentException('instrument_id must be a positive integer');
        }
        $events = self::stringList($args['events'] ?? [], 'events');

        $this->api->put('/api/v1/projects/' . $projectId . '/instruments/' . $instrumentId . '/repeating', [
            'events' => $events,
        ]);

        return self::success($events === []
            ? 'Repeating turned off for the instrument.'
            : 'Instrument now repeats in: ' . implode(', ', $events) . '.', ['display' => 'json']);
    }

    /** @return array<string, mixed> */
    private function assignInstrumentsToEvents(array $args): array
    {
        $projectId = self::projectId($args);
        if (!is_array($args['mapping'] ?? null)) {
            throw new \InvalidArgumentException('mapping must be an object of instrument name to event list');
        }
        $mapping = [];
        foreach ($args['mapping'] as $instrument => $events) {
            $mapping[(string) $instrument] = self::stringList(is_array($events) ? $events : [], 'mapping[' . $instrument . ']');
        }

        $this->api->put('/api/v1/projects/' . $projectId . '/instrument-event-mapping', [
            'arm_num' => (int) ($args['arm_num'] ?? 0),
            'mapping' => (object) $mapping,
        ]);

        return self::success('Mapping saved for arm ' . (int) ($args['arm_num'] ?? 0) . '.', ['display' => 'json']);
    }

    // --- shared bits -----------------------------------------------------------------------

    /** @param list<string> $required */
    private function def(string $name, string $description, array $properties, string $mode, array $required): array
    {
        return [
            'name' => $name,
            'description' => $description,
            'inputSchema' => ['type' => 'object', 'properties' => $properties, 'required' => $required],
            'annotations' => ['readOnlyHint' => $mode === 'read'],
            // Not part of the MCP Tool shape; stripped by Server for tools/list. The mode gate
            // and the UI's confirmation prompt read it.
            'x_mode' => $mode,
        ];
    }

    /** @param array<string, mixed> $structured */
    private static function success(string $text, array $structured): array
    {
        return ['content' => [['type' => 'text', 'text' => $text]], 'structuredContent' => $structured];
    }

    private static function failure(string $code, string $message): array
    {
        return ['content' => [['type' => 'text', 'text' => $code . ': ' . $message]], 'isError' => true];
    }

    /** Compact JSON for tool text: slashes and Unicode stay readable. */
    private static function json(mixed $value): string
    {
        return (string) json_encode($value, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
    }

    private static function projectId(array $args): int
    {
        $projectId = (int) ($args['project_id'] ?? 0);
        if ($projectId <= 0) {
            throw new \InvalidArgumentException('project_id must be a positive integer');
        }

        return $projectId;
    }

    /** @param array<string, mixed> $args */
    private static function requiredString(array $args, string $key): string
    {
        $value = trim((string) ($args[$key] ?? ''));
        if ($value === '') {
            throw new \InvalidArgumentException($key . ' is required');
        }

        return $value;
    }

    /** @param mixed $value */
    private static function stringList(mixed $value, string $key): array
    {
        if (!is_array($value)) {
            throw new \InvalidArgumentException($key . ' must be a list of strings');
        }
        $out = [];
        foreach ($value as $item) {
            if (!is_scalar($item)) {
                throw new \InvalidArgumentException($key . ' must contain only strings');
            }
            $out[] = (string) $item;
        }

        return $out;
    }

    /** @return list<array<string, mixed>> */
    private static function rowsOf(mixed $value): array
    {
        return is_array($value) ? array_values(array_filter($value, 'is_array')) : [];
    }
}
