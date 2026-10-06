<?php
// Data entry and the record view (`User_Interface_Design.md` §8, REQ-UI-025…027): one
// (record, event, instrument) form at a time — the instrument's fields in position order
// (§8.2), prefilled with the record's current values (§8.3), the completion control as the
// last field (§8.5), and the record actions beside it (§8.7).
//
// Reads per render (Plan §7 rule 13): the project detail (permissions, arms and events), the
// mode, the instruments (survey flag, instrument branching), the mapping, the record-status
// read (this record's completion states, and whether it exists yet), the selected
// instrument's fields, and the record history walked newest-first until every value the
// form shows or its branching needs is settled (§8.3, REQ-API-137). The registry patterns and
// the data access groups are read only when the form or the member needs them.
//
// Writes go through the data API with the member's own token (§8.6): values by
// `content=record&action=import` under the submission policy of GD-14, deletions by
// `action=delete` (§8.7). The completion state, the record's group and the survey link are
// administration-API calls. Gating is presentation (REQ-UI-003): a `read_only` member gets
// the values without a submit control, analysis mode closes data entry for everyone
// (REQ-UI-035), and the API re-checks every call (REQ-AUTH-033).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\DataEntry;
use Clara\Messages;
use Clara\Navigation;
use Clara\Permissions;
use Clara\Response;
use Clara\Session;

final class RecordController extends DataEntryController
{
    /** Validation types with dedicated checks (Data_Validation_Design.md §4); the rest are registry patterns. */
    private const BUILTIN_TYPES = ['', 'integer', 'floating point', 'date', 'datetime'];

    /** Default input formats when a date field names none (§4.1). */
    private const DEFAULT_FORMATS = ['date' => 'Y-m-d', 'datetime' => 'Y-m-d H:i'];

    private const SCRIPTS = ['/assets/app.js', '/assets/js/admin.js', '/assets/js/record.js'];

    private const JS_KEYS = [
        'admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied',
        'js.loading', 'js.load_failed', 'js.retry', 'action.close',
        'record.history.title', 'record.history.when', 'record.history.who', 'record.history.change',
        'record.history.empty', 'record.history.more', 'record.history.action.create',
        'record.history.action.update', 'record.history.action.delete', 'record.history.nobody',
        'record.hint.integer', 'record.hint.float', 'record.hint.min', 'record.hint.max',
        'record.hint.format', 'record.hint.pattern', 'record.hint.choice',
        'record.required_missing', 'record.save_anyway',
    ];

    /** GET /projects/{id}/records/{record} — the record view on one (event, instrument). */
    public function view(): Response
    {
        return $this->renderRecord(null, []);
    }

    /**
     * The `history` data region (REQ-UI-044): the per-field history of §8.3 — who entered or
     * changed each value, when (UTC as returned) and old → new — read newest-first and paged
     * with the API's cursor. `?field=` narrows it to one field (REQ-API-080), `?event=` to one
     * event. Gated like the page; the API applies its own per-arm and record visibility
     * checks on top (REQ-AUTH-045), so this discloses nothing the record view would not.
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDataAccess($detail);
        $projectId = $this->projectIdOf($detail);
        $record = $this->recordFromPath();

        $field = $this->request->query('field');
        $event = $this->request->query('event');
        $cursor = $this->request->query('cursor');
        if (($field !== '' && preg_match('/^[a-z0-9_]{1,100}$/', $field) !== 1)
            || ($event !== '' && preg_match('/^[a-z0-9_]{1,100}$/', $event) !== 1)
            || preg_match('/^[A-Za-z0-9_\-=]{0,512}$/', $cursor) !== 1) {
            return Response::apiError('invalid_request', $this->i18n->t('error.invalid_request'), 400);
        }

        $query = array_filter(['order' => 'newest', 'limit' => '50', 'field' => $field, 'event' => $event, 'cursor' => $cursor],
            static fn (string $v): bool => $v !== '');
        $page = $this->api->get('/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record) . '/history', $query);

        $entries = [];
        foreach ((is_array($page['entries'] ?? null) ? $page['entries'] : []) as $entry) {
            if (!is_array($entry)) {
                continue;
            }
            foreach ((is_array($entry['fields'] ?? null) ? $entry['fields'] : []) as $change) {
                if (!is_array($change) || ($field !== '' && ($change['field'] ?? '') !== $field)) {
                    continue;
                }
                $entries[] = [
                    'created_at' => (string) ($entry['created_at'] ?? ''),
                    'user' => is_string($entry['user_display_name'] ?? null) ? $entry['user_display_name'] : '',
                    'action' => (string) ($entry['action'] ?? ''),
                    'event' => (string) ($entry['event'] ?? ''),
                    'field' => (string) ($change['field'] ?? ''),
                    'old' => is_scalar($change['old'] ?? null) ? (string) $change['old'] : null,
                    'new' => is_scalar($change['new'] ?? null) ? (string) $change['new'] : null,
                ];
            }
        }
        $next = $page['next_cursor'] ?? null;

        return Response::json(['entries' => $entries, 'next_cursor' => is_string($next) && $next !== '' ? $next : null]);
    }

    // --- writes -------------------------------------------------------------------------

    /**
     * POST …?action=save — the data-API import of §8.6 under the submission policy of GD-14,
     * then the completion state of §8.5 when the user changed it. A row the API rejects
     * (`import_record_id` 0) re-renders the form as the user left it with the per-field
     * detail beside each offending input — rendered, not redirected, so no record value has
     * to outlive the request in the session.
     */
    public function save(): Response
    {
        $projectId = $this->projectIdFromPath();
        $record = $this->recordFromPath();
        $event = $this->request->field('event');
        $instrument = $this->request->field('instrument');
        $iid = $this->objectId('iid');
        $back = Response::redirect(self::recordUrl($projectId, $record, $event, $instrument));

        if (!self::isName($event) || !self::isName($instrument)) {
            $this->flashDanger($this->i18n->t('error.invalid_request'));

            return $back;
        }

        $values = $this->request->fieldMap('value');
        $row = ['record_id' => $record, 'form_name' => $instrument, 'event_name' => $event]
            + DataEntry::submission($values, $this->request->fieldMap('was'));
        $params = ['content' => 'record', 'action' => 'import', 'data' => [$row]];
        $tz = $this->request->field('tz');
        if (DataEntry::validTimezone($tz)) {
            $params['tz'] = $tz;
        }

        try {
            $result = $this->dataCall($projectId, $params);
        } catch (ApiException $e) {
            $this->flashDanger($this->dataApiFailure($e));

            return $back;
        }

        $outcome = is_array($result[0] ?? null) ? $result[0] : [];
        $code = (int) ($outcome['import_record_id'] ?? 0);
        if ($code !== 1 && $code !== 2) {
            // Nothing of the row was stored (REQ-API-035): show the reasons per field.
            $this->flashDanger($this->i18n->t('record.save.invalid'));

            return $this->renderRecord($values, DataEntry::importErrors((string) ($outcome['import_form_name'] ?? '')));
        }

        $this->flashSuccess($this->i18n->t($code === 1 ? 'record.save.created' : 'record.save.updated',
            ['record' => $record, 'instrument' => $instrument, 'event' => $event]));
        $this->applyCompletion($projectId, $record, $event, $iid);

        return $back;
    }

    /**
     * POST …?action=delete — the data API's delete (§8.7, GD-3, REQ-API-036) in one of three
     * scopes the confirmation names: the whole record, its values at one event, or one
     * instrument's values at one event. The record identifier is kept by the instrument scope
     * — deleting a form's data is not deleting the participant. Deleted values are written to
     * the audit trail by the API (REQ-AUD-009).
     */
    public function delete(): Response
    {
        $projectId = $this->projectIdFromPath();
        $record = $this->recordFromPath();
        $scope = $this->request->field('scope');
        $event = $this->request->field('event');
        $instrument = $this->request->field('instrument');
        $iid = $this->objectId('iid');
        $back = Response::redirect(self::recordUrl($projectId, $record, $event, $instrument));

        if (!in_array($scope, ['record', 'event', 'instrument'], true)
            || ($scope !== 'record' && !self::isName($event))) {
            $this->flashDanger($this->i18n->t('error.invalid_request'));

            return $back;
        }

        $params = ['content' => 'record', 'action' => 'delete', 'records' => [$record]];
        if ($scope !== 'record') {
            $params['events'] = [$event];
        }

        try {
            if ($scope === 'instrument') {
                $params['fields'] = $this->instrumentValueFields($projectId, $iid);
                if ($params['fields'] === []) {
                    $this->flashSuccess($this->i18n->t('record.delete.nothing'));

                    return $back;
                }
            }
            $result = $this->dataCall($projectId, $params);
        } catch (ApiException $e) {
            $this->flashDanger($this->dataApiFailure($e));

            return $back;
        }

        $deleted = 0;
        foreach ($result as $row) {
            $deleted += is_array($row) ? (int) ($row['deleted'] ?? 0) : 0;
        }
        $this->flashSuccess($this->i18n->t('record.delete.done.' . $scope,
            ['record' => $record, 'event' => $event, 'instrument' => $instrument, 'count' => (string) $deleted]));

        return $scope === 'record'
            ? Response::redirect('/projects/' . $projectId . '/record-status')
            : $back;
    }

    /**
     * POST …?action=assign_group — `PUT …/records/{record}/data-access-group` (§8.7,
     * REQ-API-091; `project_admin`). "No group" unassigns the record.
     */
    public function assignGroup(): Response
    {
        $projectId = $this->projectIdFromPath();
        $record = $this->recordFromPath();
        $back = Response::redirect(self::recordUrl($projectId, $record,
            $this->request->field('event'), $this->request->field('instrument')));
        $raw = $this->request->field('group_id');
        if ($raw !== 'none' && preg_match('/^\d{1,18}$/', $raw) !== 1) {
            $this->flashDanger($this->i18n->t('record.group.choose'));

            return $back;
        }

        try {
            $this->api->put('/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record) . '/data-access-group',
                ['group_id' => $raw === 'none' ? null : (int) $raw]);
        } catch (ApiException $e) {
            $this->logger->info('record group change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $back;
        }
        $this->flashSuccess($this->i18n->t($raw === 'none' ? 'record.group.unassigned' : 'record.group.assigned',
            ['record' => $record]));

        return $back;
    }

    /**
     * POST …?action=survey_link — the stable public link of a (record, survey instrument, event)
     * (§8.7, REQ-API-082): issued on first request, the same URL afterwards. The event travels as
     * the `event` parameter, because an instrument mapped to three events holds three links.
     * Fetching it is a write on the API side (issue + audit), so the browser asks with a POST and
     * the URL is shown on the next render, once, in a read-only field with a copy button.
     */
    public function surveyLink(): Response
    {
        return $this->linkCall(function (int $projectId, string $record, int $iid, string $event): void {
            $answer = $this->api->get($this->linkPath($projectId, $record, $iid), ['event' => $event]);
            $url = is_string($answer['url'] ?? null) ? $answer['url'] : '';
            Session::stash('survey_link', ['record' => $record, 'iid' => $iid, 'url' => $url]);
        }, '');
    }

    /** POST …?action=revoke_link — `DELETE …/survey-link`, effective immediately (REQ-AUTH-040). */
    public function revokeLink(): Response
    {
        return $this->linkCall(function (int $projectId, string $record, int $iid, string $event): void {
            $this->api->delete($this->linkPath($projectId, $record, $iid), ['event' => $event]);
        }, 'record.survey.revoked');
    }

    // --- rendering ----------------------------------------------------------------------

    /**
     * Renders the record view; `$draft` and `$errors` carry a rejected submission back into
     * the form (the user's values, the API's per-field reasons).
     *
     * @param array<string, string>|null $draft
     * @param array<string, string>      $errors
     */
    private function renderRecord(?array $draft, array $errors): Response
    {
        $detail = $this->projectDetail();
        $permissions = $this->requireDataAccess($detail);
        $projectId = $this->projectIdOf($detail);
        $record = $this->recordFromPath();
        $base = '/api/v1/projects/' . $projectId;
        $mode = $this->projectMode($projectId);

        $instruments = self::byPosition(self::listOf($this->api->get($base . '/instruments')));
        $mapping = $this->mapping($projectId);
        $arms = self::structure($detail, $permissions, $mapping, $instruments);
        [$armNum, $event, $instrument] = $this->selection($arms);
        $status = $this->recordStates($projectId, $record);
        $exists = $status !== null;
        $firstEvent = self::firstEvent($detail);
        $uen = $event['unique_event_name'] ?? '';

        $fields = [];
        $identifier = '';
        if ($instrument !== null) {
            $fields = self::listOf($this->api->get($base . '/instruments/' . (int) $instrument['id'] . '/fields'));
            // The record identifier is the first field of the first instrument (GD-8): shown,
            // never edited — the record's name is what every import row carries anyway.
            if ((int) ($instrument['id'] ?? 0) === (int) ($instruments[0]['id'] ?? 0)) {
                $identifier = (string) ($fields[0]['field_name'] ?? '');
            }
        }

        // The values this render needs: the form's own fields at the selected event, and every
        // reference the field and instrument branching logic of this event reads (§8.4).
        $own = [];
        $refs = [];
        foreach ($fields as $field) {
            $name = (string) ($field['field_name'] ?? '');
            if (DataEntry::isEnterable($field) || ($field['field_type'] ?? '') === 'calculated') {
                $own[] = $uen . '|' . $name;
            }
            $refs = array_merge($refs, DataEntry::references((string) ($field['branching_logic'] ?? ''), $firstEvent));
        }
        foreach ($event['instruments'] ?? [] as $candidate) {
            $refs = array_merge($refs, DataEntry::references((string) ($candidate['branching_logic'] ?? ''), $firstEvent));
        }
        $values = $exists ? $this->currentValues($projectId, $record, array_values(array_unique(array_merge($own, $refs)))) : [];

        $prefill = [];
        foreach ($fields as $field) {
            $name = (string) ($field['field_name'] ?? '');
            $prefill[$name] = $values[$uen . '|' . $name] ?? '';
        }
        $context = [];
        foreach (array_unique($refs) as $key) {
            if (!in_array($key, $own, true) && isset($values[$key])) {
                $context[$key] = $values[$key];
            }
        }

        $analysis = $mode === 'analysis';
        // Entry is decided on the selected pair, not on the arm (REQ-AUTH-069).
        $instrumentName = (string) ($instrument['name'] ?? '');
        $canEdit = !$analysis && $instrument !== null
            && $permissions->canEditPair($uen, $instrumentName, $armNum);
        $isSurvey = $instrument !== null && !empty($instrument['is_survey']);
        $iid = (int) ($instrument['id'] ?? 0);
        $link = Session::take('survey_link');

        return $this->renderSection($detail, 'project/record', 'record.title', [
            'record' => $record,
            'exists' => $exists,
            'recordUrl' => self::recordUrl($projectId, $record),
            'statusUrl' => '/projects/' . $projectId . '/record-status',
            'arms' => $arms,
            'armNum' => $armNum,
            'event' => $event,
            'instrument' => $instrument,
            'items' => DataEntry::layout($fields),
            'identifier' => $identifier,
            'prefill' => $prefill,
            'draft' => $draft,
            'errors' => $errors,
            'states' => $status ?? [],
            'state' => $status[$uen . '|' . ($instrument['name'] ?? '')] ?? 'no_data',
            'analysis' => $analysis,
            'canEdit' => $canEdit,
            // Which of the three delete scopes this user may carry out (REQ-API-036/070).
            'deleteScopes' => !$analysis && $exists
                ? self::deleteScopes($permissions, $mapping, $armNum, $uen, $instrumentName) : [],
            'isSurvey' => $isSurvey,
            // Survey-link actions: survey-marked instruments, data access ≥ view_edit (§8.7).
            // Issuing a link needs data access ≥ view_edit on this pair (REQ-API-082).
            'canLink' => $isSurvey && $exists && $permissions->canEditPair($uen, $instrumentName, $armNum),
            'link' => $link !== null && ($link['record'] ?? '') === $record && (int) ($link['iid'] ?? 0) === $iid ? (string) ($link['url'] ?? '') : '',
            'groups' => $permissions->projectAdmin && $exists ? self::listOf($this->api->get($base . '/data-access-groups')) : null,
            'patterns' => $this->patternsFor($fields),
            'formats' => self::DEFAULT_FORMATS,
            'clientContext' => ['firstEvent' => $firstEvent, 'event' => $uen, 'values' => (object) $context],
            // The record view belongs to the Record Status Dashboard's entry of the left panel.
            'nav' => [
                'headingKey' => 'nav.project_sections',
                'items' => array_map(static fn (array $item): array => $item + ['active' => $item['key'] === 'record_status'],
                    Navigation::projectSections($projectId, $permissions)),
            ],
        ], ['scripts' => self::SCRIPTS, 'jsKeys' => self::JS_KEYS], $mode);
    }

    /**
     * The delete scopes the acting user really holds: the API removes an event's or a
     * record's values only when **every** pair in scope carries the delete right
     * (REQ-API-036/070), so a control that would come back 403 is absent rather than
     * offered (REQ-UI-003). The instrument scope names exactly one pair; the wider ones
     * walk the unfiltered mapping, because values may exist on a pair this member cannot
     * read — and those hold the whole delete back.
     *
     * @param array<int, array<string, list<string>>> $mapping arm_num => instrument => events
     * @return list<string> any subset of instrument / event / record, in that order
     */
    private static function deleteScopes(Permissions $permissions, array $mapping,
        int $armNum, string $uen, string $instrumentName): array
    {
        if ($instrumentName !== '' && $permissions->canDeleteValues($uen, $instrumentName, $armNum)) {
            $scopes = ['instrument'];
        } else {
            $scopes = [];
        }

        $eventPairs = 0;
        $eventHeld = true;
        $projectPairs = 0;
        $projectHeld = true;
        foreach ($mapping as $arm => $byInstrument) {
            foreach ($byInstrument as $name => $events) {
                foreach ($events as $event) {
                    $held = $permissions->canDeleteValues((string) $event, (string) $name, (int) $arm);
                    $projectPairs++;
                    $projectHeld = $projectHeld && $held;
                    if ((string) $event === $uen) {
                        $eventPairs++;
                        $eventHeld = $eventHeld && $held;
                    }
                }
            }
        }
        if ($eventPairs > 0 && $eventHeld) {
            $scopes[] = 'event';
        }
        if ($projectPairs > 0 && $projectHeld) {
            $scopes[] = 'record';
        }

        return $scopes;
    }

    /**
     * The (arm, event, instrument) the form shows: the requested ones when they name a mapped
     * pair the member may read, otherwise the first event — of the requested arm, when one is
     * named (a new participant opened from the dashboard) — that has an instrument, and its
     * first instrument. A re-render after a rejected save reads the pair from the body.
     *
     * @param list<array<string, mixed>> $arms
     * @return array{0: int, 1: array<string, mixed>|null, 2: array<string, mixed>|null}
     */
    private function selection(array $arms): array
    {
        $wantEvent = $this->request->query('event') ?: $this->request->field('event');
        $wantInstrument = $this->request->query('instrument') ?: $this->request->field('instrument');
        $wantArm = (int) $this->request->query('arm');

        $fallback = null;
        foreach ($arms as $arm) {
            foreach ($arm['events'] as $event) {
                if ($event['unique_event_name'] === $wantEvent) {
                    return [$arm['arm_num'], $event, self::pickInstrument($event, $wantInstrument)];
                }
                $preferred = $wantArm === 0 || $wantArm === $arm['arm_num'];
                if ($event['instruments'] !== [] && ($fallback === null || (!$fallback[3] && $preferred))) {
                    $fallback = [$arm['arm_num'], $event, $event['instruments'][0], $preferred];
                }
            }
        }
        if ($fallback !== null) {
            return [$fallback[0], $fallback[1], self::pickInstrument($fallback[1], $wantInstrument)];
        }
        $first = $arms[0]['events'][0] ?? null;

        return [(int) ($arms[0]['arm_num'] ?? 0), $first, null];
    }

    /** @param array<string, mixed> $event */
    private static function pickInstrument(array $event, string $name): ?array
    {
        foreach ($event['instruments'] as $instrument) {
            if (($instrument['name'] ?? '') === $name) {
                return $instrument;
            }
        }

        return $event['instruments'][0] ?? null;
    }

    /**
     * This record's completion states from the record-status read (§4.13), keyed
     * `<event>|<instrument>` — or null when the read does not list the record: it has not
     * been stored yet (a new participant, §6.3) or is outside the member's group.
     *
     * @return array<string, string>|null
     */
    private function recordStates(int $projectId, string $record): ?array
    {
        foreach ($this->api->get('/api/v1/projects/' . $projectId . '/record-status') as $row) {
            if (!is_array($row) || (string) ($row['record_id'] ?? '') !== $record) {
                continue;
            }
            $states = [];
            foreach ((is_array($row['events'] ?? null) ? $row['events'] : []) as $event) {
                foreach ((is_array($event['instruments'] ?? null) ? $event['instruments'] : []) as $instrument) {
                    $states[(string) ($event['unique_event_name'] ?? '') . '|' . (string) ($instrument['name'] ?? '')] =
                        (string) ($instrument['state'] ?? 'no_data');
                }
            }

            return $states;
        }

        return null;
    }

    /**
     * Current values for the wanted `<event>|<field>` keys (§8.3), from the record history read
     * most-recent-first. The history is not narrowed by instrument or event: branching may
     * read values of other instruments and events, and the API filters within the
     * record-scoped page, so the filter would not make the walk any shorter.
     *
     * @param list<string> $wanted
     * @return array<string, string>
     */
    private function currentValues(int $projectId, string $record, array $wanted): array
    {
        $path = '/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record) . '/history';
        $walk = DataEntry::currentValues(function (?string $cursor) use ($path): array {
            $query = ['order' => 'newest', 'limit' => (string) DataEntry::HISTORY_PAGE];
            if ($cursor !== null) {
                $query['cursor'] = $cursor;
            }

            return $this->api->get($path, $query);
        }, $wanted);
        if ($walk['truncated']) {
            // Values older than the walk's ceiling would show as empty: say so in the log, where
            // an operator measuring Plan §7 rule 14 will look.
            $this->logger->warn('record history walk truncated', ['project' => $projectId, 'pages' => DataEntry::HISTORY_MAX_PAGES]);
        }

        return $walk['values'];
    }

    /**
     * Advisory patterns for registry validation types on this form (§8.2, REQ-VAL-002) —
     * read only when a field uses one; the API remains the authority.
     *
     * @param list<array<string, mixed>> $fields
     * @return array<string, string>
     */
    private function patternsFor(array $fields): array
    {
        $needed = false;
        foreach ($fields as $field) {
            if (DataEntry::isEnterable($field) && !in_array((string) ($field['validation_type'] ?? ''), self::BUILTIN_TYPES, true)) {
                $needed = true;
            }
        }
        if (!$needed) {
            return [];
        }

        $patterns = [];
        foreach ($this->api->get('/api/v1/validationTypes') as $type) {
            if (is_array($type) && empty($type['builtin']) && is_string($type['regex'] ?? null) && isset($type['name'])) {
                $patterns[(string) $type['name']] = $type['regex'];
            }
        }

        return $patterns;
    }

    // --- helpers ------------------------------------------------------------------------

    /**
     * Sets or clears the user's completion assignment when the dropdown changed (§8.5):
     * "finished" sets it; leaving "finished" for either other state clears it, after which
     * record-status derives no data / some data from the values (REQ-API-110). Moving between
     * the two derived states is not an assignment and makes no call.
     */
    private function applyCompletion(int $projectId, string $record, string $event, int $iid): void
    {
        $now = $this->request->field('completion');
        $was = $this->request->field('completion_was');
        if ($iid === 0 || !in_array($now, DataEntry::STATES, true) || $now === $was
            || ($now !== 'finished' && $was !== 'finished')) {
            return;
        }

        try {
            $this->api->put('/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record)
                . '/events/' . rawurlencode($event) . '/instruments/' . $iid . '/completion',
                ['state' => $now === 'finished' ? 'finished' : 'unfinished']);
        } catch (ApiException $e) {
            $this->logger->info('completion change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));
        }
    }

    /**
     * The value-carrying fields of one instrument for a scoped delete: every field that holds
     * values (calculated ones included), but not the record identifier.
     *
     * @return list<string>
     */
    private function instrumentValueFields(int $projectId, int $iid): array
    {
        $base = '/api/v1/projects/' . $projectId;
        $first = self::byPosition(self::listOf($this->api->get($base . '/instruments')))[0]['id'] ?? 0;
        $fields = self::listOf($this->api->get($base . '/instruments/' . $iid . '/fields'));

        $names = [];
        foreach ($fields as $i => $field) {
            if ($iid === (int) $first && $i === 0) {
                continue; // the record identifier (GD-8) stays with the participant
            }
            if (DataEntry::isEnterable($field) || ($field['field_type'] ?? '') === 'calculated') {
                $names[] = (string) $field['field_name'];
            }
        }

        return $names;
    }

    /** One survey-link call, then back to the instrument it was made for. */
    private function linkCall(callable $call, string $successKey): Response
    {
        $projectId = $this->projectIdFromPath();
        $record = $this->recordFromPath();
        $iid = $this->objectId('iid');
        $event = $this->request->field('event');
        $back = Response::redirect(self::recordUrl($projectId, $record,
            $event, $this->request->field('instrument')));

        try {
            $call($projectId, $record, $iid, $event);
        } catch (ApiException $e) {
            $this->logger->info('survey link call rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $back;
        }
        if ($successKey !== '') {
            $this->flashSuccess($this->i18n->t($successKey));
        }

        return $back;
    }

    private function linkPath(int $projectId, string $record, int $iid): string
    {
        return '/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record) . '/instruments/' . $iid . '/survey-link';
    }

    /** An event or instrument name as the forms carry it (`[a-z0-9_]`, REQ-DB-011/013). */
    private static function isName(string $value): bool
    {
        return preg_match('/^[a-z0-9_]{1,100}$/', $value) === 1;
    }
}
