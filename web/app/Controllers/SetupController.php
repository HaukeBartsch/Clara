<?php
// The Setup page (`User_Interface_Design.md` §6.2, REQ-UI-018, `project_admin`): one page,
// four blocks — A arms, B events per arm, C instruments, D the instrument × event matrix per
// arm. The arm-dependent blocks (B, D) are tabs, one per arm, arm 1 shown by default; a write
// in an arm's tab returns to that tab (`?arm=`).
//
// Reads per render: the project detail (permissions, name), the mode, the arms with their
// events, the instruments, the mapping — and the staging diff only while a set is open.
// Reordering is computed here from a fresh read and sent as the full ordered id list every
// order endpoint requires (§4.8–§4.10); the arms' and events' ids never change by it
// (REQ-API-129). New projects already hold arm 1, the `baseline` event and the instrument
// `instrument` (REQ-API-050) — nothing here creates them (Plan/Web_Implementation.md §7 rule 4).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\Response;

final class SetupController extends StructureController
{
    /** GET /projects/{id}/setup */
    public function index(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDesigner($detail);
        $projectId = $this->projectIdOf($detail);
        $base = '/api/v1/projects/' . $projectId;
        $setupUrl = '/projects/' . $projectId . '/setup';

        $state = $this->structureState($projectId, $setupUrl);
        $arms = self::listOf($this->api->get($base . '/arms'));
        $instruments = self::listOf($this->api->get($base . '/instruments'));

        // arm_num → instrument name → list of mapped unique event names.
        $mapping = [];
        foreach (self::listOf($this->api->get($base . '/instrument-event-mapping')) as $arm) {
            $mapping[(int) ($arm['arm_num'] ?? 0)] = is_array($arm['mapping'] ?? null) ? $arm['mapping'] : [];
        }

        $armNums = array_map(static fn (array $a): int => (int) ($a['arm_num'] ?? 0), $arms);
        $activeArm = (int) $this->request->query('arm', '1');
        if (!in_array($activeArm, $armNums, true)) {
            $activeArm = $armNums[0] ?? 1;
        }

        return $this->renderSection($detail, 'project/setup', 'setup.title', $state + [
            'setupUrl' => $setupUrl,
            'designUrl' => '/projects/' . $projectId . '/design',
            'arms' => array_map(static fn (array $arm): array => $arm + [
                'events_display' => self::withMoveFlags(self::listOf($arm['events'] ?? [])),
            ], $arms),
            'instruments' => $instruments,
            'mapping' => $mapping,
            'activeArm' => $activeArm,
            'editEvent' => self::parseObjectId($this->request->query('edit_event')),
            'editInstrument' => self::parseObjectId($this->request->query('edit_instrument')),
        ], ['scripts' => self::SCRIPTS, 'jsKeys' => self::JS_KEYS], $state['mode']);
    }

    // --- Block A: arms --------------------------------------------------------------

    public function addArm(): Response
    {
        $name = trim($this->request->field('name'));

        return $this->structureWrite($this->back(), function (int $projectId) use ($name): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/arms', $this->withAck(['name' => $name]));
        }, $this->i18n->t('setup.arm.added', ['name' => $name]));
    }

    public function moveArm(): Response
    {
        $armId = $this->objectId('arm_id');
        $direction = $this->request->field('direction');

        return $this->structureWrite($this->back(), function (int $projectId) use ($armId, $direction): void {
            $ids = array_map(static fn (array $a): int => (int) ($a['id'] ?? 0),
                self::listOf($this->api->get('/api/v1/projects/' . $projectId . '/arms')));
            $this->api->put('/api/v1/projects/' . $projectId . '/arms/order',
                $this->withAck(['order' => self::moved($ids, $armId, $direction)]));
        }, $this->i18n->t('setup.reordered'));
    }

    /**
     * DELETE /api/v1/arms/{id}: 409 while the arm still has events or data (§3.4), except as
     * the last remaining arm, which the API renames to `arm_1` instead (200, REQ-API-128).
     */
    public function deleteArm(): Response
    {
        $armId = $this->objectId('arm_id');
        $name = $this->request->field('name');

        return $this->structureWrite($this->setupUrl(), function () use ($armId, $name): void {
            $answer = $this->api->delete('/api/v1/arms/' . $armId, [], $this->withAck([]) ?: null);
            $this->flashSuccess($answer !== []
                ? $this->i18n->t('setup.arm.reset')
                : $this->i18n->t('setup.arm.deleted', ['name' => $name]));
        }, '');
    }

    // --- Block B: events (per arm) --------------------------------------------------

    public function addEvent(): Response
    {
        $label = trim($this->request->field('event_name'));
        $body = ['arm_num' => (int) $this->request->field('arm_num'), 'event_name' => $label] + $this->eventTimes();

        return $this->structureWrite($this->back(), function (int $projectId) use ($body): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/events', $this->withAck($body));
        }, $this->i18n->t('setup.event.added', ['name' => $label]));
    }

    /** PUT /api/v1/events/{id} — a blank timepoint clears it (`period: null`, GD-15). */
    public function updateEvent(): Response
    {
        $eventId = $this->objectId('event_id');
        $label = trim($this->request->field('event_name'));
        $body = ['event_name' => $label] + $this->eventTimes();

        return $this->structureWrite($this->back(), function () use ($eventId, $body): void {
            $this->api->put('/api/v1/events/' . $eventId, $this->withAck($body));
        }, $this->i18n->t('setup.event.updated', ['name' => $label]));
    }

    /**
     * DELETE /api/v1/events/{id}. As the project's last remaining event the API resets it to
     * the plain `baseline` state instead (200 with the renamed object, REQ-API-133) — the
     * confirmation said so, and the success line says which happened.
     */
    public function deleteEvent(): Response
    {
        $eventId = $this->objectId('event_id');
        $name = $this->request->field('name');

        return $this->structureWrite($this->back(), function () use ($eventId, $name): void {
            $answer = $this->api->delete('/api/v1/events/' . $eventId, [], $this->withAck([]) ?: null);
            $this->flashSuccess($answer !== []
                ? $this->i18n->t('setup.event.reset')
                : $this->i18n->t('setup.event.deleted', ['name' => $name]));
        }, '');
    }

    /**
     * Up/down on an arm's events (GD-15, REQ-API-103): only no-timepoint events, and ties
     * among timepoint events, have a place of their own to change — the page offers the
     * controls only there, and the move is refused here too when its neighbour belongs to
     * another group (the canonical order would put it straight back).
     */
    public function moveEvent(): Response
    {
        $eventId = $this->objectId('event_id');
        $armNum = (int) $this->request->field('arm_num');
        $direction = $this->request->field('direction');

        return $this->structureWrite($this->back(), function (int $projectId) use ($eventId, $armNum, $direction): void {
            $events = [];
            foreach (self::listOf($this->api->get('/api/v1/projects/' . $projectId . '/arms')) as $arm) {
                if ((int) ($arm['arm_num'] ?? 0) === $armNum) {
                    $events = self::listOf($arm['events'] ?? []);
                }
            }
            $flags = self::withMoveFlags($events);
            foreach ($flags as $event) {
                if ((int) ($event['id'] ?? 0) === $eventId
                    && !($direction === 'up' ? $event['can_move_up'] : $event['can_move_down'])) {
                    return; // nothing to move: the canonical order fixes this event's place
                }
            }
            $ids = array_map(static fn (array $e): int => (int) ($e['id'] ?? 0), $events);
            $this->api->put('/api/v1/projects/' . $projectId . '/events/order',
                $this->withAck(['arm_num' => $armNum, 'order' => self::moved($ids, $eventId, $direction)]));
        }, $this->i18n->t('setup.reordered'));
    }

    /**
     * The timepoint and safe region of the event form: blank = no value (`null`); anything
     * else is sent as the integer it reads as, and the API judges it (§4.9).
     *
     * @return array{period: int|null, safe_region_start: int|null, safe_region_end: int|null}
     */
    private function eventTimes(): array
    {
        $read = function (string $name): ?int {
            $raw = trim($this->request->field($name));

            return preg_match('/^-?\d{1,9}$/', $raw) === 1 ? (int) $raw : null;
        };

        return [
            'period' => $read('period'),
            'safe_region_start' => $read('safe_region_start'),
            'safe_region_end' => $read('safe_region_end'),
        ];
    }

    // --- Block C: instruments -------------------------------------------------------

    public function addInstrument(): Response
    {
        $name = trim($this->request->field('name'));

        return $this->structureWrite($this->back(), function (int $projectId) use ($name): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/instruments', $this->withAck(['name' => $name]));
        }, $this->i18n->t('setup.instrument.added', ['name' => $name]));
    }

    /** PUT …/instruments/{iid} with exactly `name`, `is_survey`, `branching_logic` (§4.10). */
    public function updateInstrument(): Response
    {
        $iid = $this->objectId('instrument_id');
        $name = trim($this->request->field('name'));
        $body = [
            'name' => $name,
            'is_survey' => $this->request->field('is_survey') === '1',
            'branching_logic' => trim($this->request->field('branching_logic')),
        ];

        return $this->structureWrite($this->back(), function (int $projectId) use ($iid, $body): void {
            $this->api->put('/api/v1/projects/' . $projectId . '/instruments/' . $iid, $this->withAck($body));
        }, $this->i18n->t('setup.instrument.updated', ['name' => $name]));
    }

    public function moveInstrument(): Response
    {
        $iid = $this->objectId('instrument_id');
        $direction = $this->request->field('direction');

        return $this->structureWrite($this->back(), function (int $projectId) use ($iid, $direction): void {
            $ids = array_map(static fn (array $i): int => (int) ($i['id'] ?? 0),
                self::listOf($this->api->get('/api/v1/projects/' . $projectId . '/instruments')));
            $this->api->put('/api/v1/projects/' . $projectId . '/instruments/order',
                $this->withAck(['order' => self::moved($ids, $iid, $direction)]));
        }, $this->i18n->t('setup.reordered'));
    }

    /**
     * DELETE …/instruments/{iid}: 409 while a surviving expression names one of its fields;
     * as the last remaining instrument only its fields go and it is renamed `instrument`
     * (200, REQ-API-127).
     */
    public function deleteInstrument(): Response
    {
        $iid = $this->objectId('instrument_id');
        $name = $this->request->field('name');

        return $this->structureWrite($this->back(), function (int $projectId) use ($iid, $name): void {
            $answer = $this->api->delete('/api/v1/projects/' . $projectId . '/instruments/' . $iid, [],
                $this->withAck([]) ?: null);
            $this->flashSuccess($answer !== []
                ? $this->i18n->t('setup.instrument.reset')
                : $this->i18n->t('setup.instrument.deleted', ['name' => $name]));
        }, '');
    }

    // --- Block D: the instrument × event matrix (per arm) ---------------------------

    /**
     * PUT …/instrument-event-mapping with the full matrix of the tabbed arm (§6.2 D). The form
     * posts ids (`map[<instrument id>][]=<event id>`) so no stored name has to survive being a
     * form key; they are translated back to the names the endpoint takes from a fresh read.
     * Every instrument appears in the body, an unticked one with an empty list.
     */
    public function saveMapping(): Response
    {
        $armNum = (int) $this->request->field('arm_num');
        $ticked = $this->request->fieldListMap('map');

        return $this->structureWrite($this->back(), function (int $projectId) use ($armNum, $ticked): void {
            $base = '/api/v1/projects/' . $projectId;
            $events = [];
            foreach (self::listOf($this->api->get($base . '/arms')) as $arm) {
                if ((int) ($arm['arm_num'] ?? 0) === $armNum) {
                    foreach (self::listOf($arm['events'] ?? []) as $event) {
                        $events[(int) ($event['id'] ?? 0)] = (string) ($event['unique_event_name'] ?? '');
                    }
                }
            }
            $mapping = [];
            foreach (self::listOf($this->api->get($base . '/instruments')) as $instrument) {
                $chosen = [];
                foreach ($ticked[(string) ($instrument['id'] ?? '')] ?? [] as $eventId) {
                    $eventId = self::parseObjectId($eventId);
                    if (isset($events[$eventId])) {
                        $chosen[] = $events[$eventId];
                    }
                }
                $mapping[(string) ($instrument['name'] ?? '')] = $chosen;
            }
            $this->api->put($base . '/instrument-event-mapping',
                $this->withAck(['arm_num' => $armNum, 'mapping' => (object) $mapping]));
        }, $this->i18n->t('setup.mapping.saved', ['arm' => (string) $armNum]));
    }

    // --- helpers --------------------------------------------------------------------

    private function setupUrl(): string
    {
        return '/projects/' . $this->projectIdFromPath() . '/setup';
    }

    /** Back to the setup page, on the arm tab the form belonged to. */
    private function back(): string
    {
        $arm = $this->request->field('arm_num');

        return $this->setupUrl() . (preg_match('/^\d{1,4}$/', $arm) === 1 ? '?arm=' . $arm : '');
    }

    /**
     * The list with one id moved one place up or down — the full ordered list the order
     * endpoints require. An id that is absent, or already at that end, leaves it unchanged.
     *
     * @param list<int> $ids
     * @return list<int>
     */
    public static function moved(array $ids, int $id, string $direction): array
    {
        $at = array_search($id, $ids, true);
        if ($at === false) {
            return $ids;
        }
        $to = $direction === 'up' ? $at - 1 : $at + 1;
        if ($to < 0 || $to >= count($ids)) {
            return $ids;
        }
        [$ids[$at], $ids[$to]] = [$ids[$to], $ids[$at]];

        return $ids;
    }

    /**
     * Marks which events of an arm (already in canonical order, GD-15) may move: a move swaps
     * neighbours, and only neighbours of the same group can trade places — two no-timepoint
     * events, or two timepoint events sharing a period (a tie, ordered by position).
     *
     * @param list<array<string, mixed>> $events
     * @return list<array<string, mixed>>
     */
    public static function withMoveFlags(array $events): array
    {
        $group = static fn (array $e): string => ($e['period'] ?? null) === null ? 'none' : 'p' . (string) $e['period'];
        $out = [];
        foreach ($events as $i => $event) {
            $out[] = $event + [
                'can_move_up' => $i > 0 && $group($events[$i - 1]) === $group($event),
                'can_move_down' => $i < count($events) - 1 && $group($events[$i + 1]) === $group($event),
            ];
        }

        return $out;
    }
}
