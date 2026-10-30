<?php
// The Record Status Dashboard (`User_Interface_Design.md` §6.3, REQ-UI-019): the participant
// overview — every record visible under the data-access-group rule (REQ-AUTH-045) as a row,
// the arm's events as column groups with their instruments in order, and one colour-coded
// cell per (record, event, instrument): grey no data, amber some data, green finished.
//
// The page renders the structure — arm tabs, the column headers, the new-participant form —
// and the rows are the route's `records` data region, bound by `record-status.js` from the
// same URL (REQ-UI-032/044) into a virtually rendered Tabulator grid, so only the rows in view
// are in the DOM. The region is the API's record-status read reshaped into cells; like the
// read, it carries no field value (REQ-API-074). The page asks for it as newline-delimited
// JSON, relayed line by line as the API streams it (REQ-UI-074), so the first rows show while
// the rest of a large project is still being read.
//
// A repeating instrument (REQ-UI-055) shows one dot per instance in its cell, then "+" that
// opens the next instance in the record view; the column header marks it. Which pairs repeat
// is the instrument list's `repeating_events` (REQ-API-154), read once per render.
//
// New participant (§6.3): a record name typed by the user or proposed by the data API's
// `generateNextRecordName` (REQ-API-023), then the record view opens on it; nothing is stored
// until its first import (REQ-API-033), which also places it in the member's active data
// access group (REQ-API-093). Offered at data access ≥ `view_edit` on some arm, and never in
// analysis mode (§8.1, REQ-UI-035). Entry additionally needs the project token, which only a
// member holds — an administrator may see a project without belonging to it (REQ-API-102) —
// so membership is asked here, where the page can answer it with what to do about it, rather
// than first at the Save of a form the user has already filled out.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\DataEntry;
use Clara\Permissions;
use Clara\Response;
use Clara\Session;
use Clara\SessionRevoked;
use Clara\StreamSink;

final class RecordStatusController extends DataEntryController
{
    /**
     * Arm numbers the acting user may enter data in — by the arm's default or by a pair
     * grant inside it (REQ-AUTH-069).
     *
     * @param array<int, array<string, list<string>>> $mapping
     * @return list<int>
     */
    private static function editableArmNumbers(Permissions $permissions, array $mapping): array
    {
        $rank = Permissions::dataRank('view_edit');
        $out = [];
        foreach ($mapping as $arm => $byInstrument) {
            if ($permissions->canEdit((int) $arm)) {
                $out[] = (int) $arm;
                continue;
            }
            foreach ($byInstrument as $name => $events) {
                foreach ($events as $event) {
                    if ($permissions->reachesPairData((string) $event, (string) $name, (int) $arm, $rank)) {
                        $out[] = (int) $arm;
                        continue 2;
                    }
                }
            }
        }

        return $out;
    }

    /** GET /projects/{id}/record-status */
    public function index(): Response
    {
        $detail = $this->projectDetail();
        $permissions = $this->requireDataAccess($detail);
        $projectId = $this->projectIdOf($detail);
        $mode = $this->projectMode($projectId);

        $mapping = $this->mapping($projectId);
        $arms = self::withRepeating(self::structure($detail, $permissions, $mapping,
            self::byPosition(is_array($detail['instruments'] ?? null) ? $detail['instruments'] : [])),
            self::listOf($this->api->get('/api/v1/projects/' . $projectId . '/instruments')),
            $permissions, $mode !== 'analysis');
        // Entry is held per (instrument, event), so an arm offers a new participant when its
        // default reaches view_edit or one pair in it does (REQ-AUTH-069).
        $editable = self::editableArmNumbers($permissions, $mapping);
        $editableArms = array_values(array_filter($arms,
            static fn (array $arm): bool => in_array($arm['arm_num'], $editable, true)));

        $proposed = Session::take('new_record');

        // Entry is the token's right, and only a member holds one: an administrator may see
        // a project without belonging to it (REQ-API-102), so the affordance is asked here —
        // where the page can point to the Members page — rather than first at the Save of a
        // form the user has already filled out. The fetch warms the cache every later data
        // call reads (§8.6); it runs only when an offer would otherwise be made, so a purely
        // read-only visit spends nothing extra.
        $offer = $mode !== 'analysis' && $editableArms !== [];
        $member = !$offer || $this->api->dataApi()->hasMemberToken($projectId);

        return $this->renderSection($detail, 'project/record_status', 'records.title', [
            'statusUrl' => '/projects/' . $projectId . '/record-status',
            'recordBase' => '/projects/' . $projectId . '/records/',
            'arms' => $arms,
            // §8.1 / REQ-UI-035: the new-participant affordance is absent in analysis mode,
            // and for a member who may enter data nowhere (REQ-UI-003).
            'newRecord' => $offer && $member ? [
                'arms' => $editableArms,
                'name' => (string) ($proposed['name'] ?? ''),
                'arm' => (int) ($proposed['arm'] ?? 0),
                // The project's naming mode decides which control the form offers — never
                // both (REQ-UI-052): type a name, or one click that numbers the participant.
                'naming' => (string) ($detail['record_naming'] ?? 'manual'),
            ] : null,
            // The explanation that takes the form's place for a viewer who holds no token.
            'notMember' => $offer && !$member,
        ], [
            'scripts' => ['/assets/app.js', '/assets/js/record-status.js'],
            // The rows bind into a virtually rendered Tabulator grid (AGENTS.md "use Tabulator").
            'styles' => ['/assets/vendor/tabulator/tabulator_bootstrap5.min.css'],
            'jsKeys' => ['js.loading', 'js.load_failed', 'js.retry', 'records.empty',
                'records.state.no_data', 'records.state.some_data', 'records.state.finished',
                'records.open_cell', 'records.open_instance', 'records.add_instance', 'records.more_instances',
                'records.loading_count', 'records.load_incomplete'],
        ], $mode);
    }

    /**
     * Marks every (event, instrument) column of the structure: `repeating` when the instrument
     * repeats at that event (REQ-API-154), `canAdd` when its "+" is offered — entry on the
     * pair (REQ-AUTH-069) and outside analysis mode (REQ-UI-035).
     *
     * @param list<array<string, mixed>> $arms        DataEntryController::structure
     * @param list<array<string, mixed>> $instruments `GET …/instruments`
     * @return list<array<string, mixed>>
     */
    public static function withRepeating(array $arms, array $instruments, Permissions $permissions, bool $open): array
    {
        $repeating = [];
        foreach ($instruments as $instrument) {
            foreach (is_array($instrument['repeating_events'] ?? null) ? $instrument['repeating_events'] : [] as $event) {
                $repeating[(string) ($instrument['name'] ?? '')][(string) $event] = true;
            }
        }
        foreach ($arms as &$arm) {
            foreach ($arm['events'] as &$event) {
                $uen = (string) $event['unique_event_name'];
                foreach ($event['instruments'] as &$instrument) {
                    $name = (string) ($instrument['name'] ?? '');
                    $instrument['repeating'] = isset($repeating[$name][$uen]);
                    $instrument['canAdd'] = $instrument['repeating'] && $open
                        && $permissions->canEditPair($uen, $name, (int) $arm['arm_num']);
                }
                unset($instrument);
            }
            unset($event);
        }
        unset($arm);

        return $arms;
    }

    /**
     * The `records` data region (REQ-UI-044): one row per visible record with the events the
     * API reported for it and the cells that hold a state other than no data, keyed
     * `<unique_event_name>|<instrument name>` — sparse like the read it relays (REQ-API-149),
     * so a project's size in pairs never multiplies into the payload. A repeating instrument's
     * cell also lists its instances as `[instance, state]` pairs under the same key in
     * `instances` (REQ-API-156). Authorized like the page — the same detail read and
     * data-access gate — and disclosing exactly what the API's record-status read does: names
     * and states, no values (REQ-API-074).
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDataAccess($detail);
        $projectId = $this->projectIdOf($detail);

        $records = [];
        foreach ($this->api->get('/api/v1/projects/' . $projectId . '/record-status') as $row) {
            $record = is_array($row) ? self::regionRow($row) : null;
            if ($record !== null) {
                $records[] = $record;
            }
        }

        return Response::json(['records' => $records]);
    }

    /**
     * The `records` region as newline-delimited JSON (REQ-UI-074, §6.3 Progressive load): the
     * API's streamed record-status read (REQ-API-172) relayed line by line, each record
     * reshaped into the row data() serves, then the API's end line. Authorized like data()
     * before anything is sent, so a refusal is still the region's JSON error; PHP holds one
     * chunk of the transfer and its unfinished line, never the read.
     */
    public function stream(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDataAccess($detail);
        $path = '/api/v1/projects/' . $this->projectIdOf($detail) . '/record-status';

        return Response::stream(function (StreamSink $sink) use ($path): void {
            $tail = '';
            try {
                $this->api->stream($path, [],
                    static function () use ($sink): void {
                        // The session lock would block the user's other pages for as long as
                        // the read runs (Session::close). The relay's own work grows with the
                        // project, and the transfer has no overall limit, only the stalled-
                        // transfer one of the transport (REQ-UI-074).
                        Session::close();
                        if (function_exists('set_time_limit')) {
                            set_time_limit(0);
                        }
                        $sink->begin(200, [
                            'Content-Type' => 'application/x-ndjson; charset=UTF-8',
                            'Cache-Control' => 'no-store',
                            // Asks nginx not to buffer the relayed lines (INSTALL.md §11).
                            'X-Accel-Buffering' => 'no',
                        ]);
                    },
                    static function (string $chunk) use ($sink, &$tail): void {
                        // A chunk may end inside a line; that tail waits for the next chunk.
                        $lines = explode("\n", $tail . $chunk);
                        $tail = (string) array_pop($lines);
                        $out = '';
                        foreach ($lines as $line) {
                            $out .= self::relayLine($line);
                        }
                        if ($out !== '') {
                            $sink->write($out);
                        }
                    },
                    'application/x-ndjson'
                );
            } catch (ApiException $e) {
                // Refused before the first byte. The producer runs after the router has
                // returned, so it answers the region's failure itself (§3.4).
                self::fail($sink, Response::apiError($e->code(), $this->apiFailureMessage($e), $e->status(),
                    $this->request->ref()));
            } catch (SessionRevoked) {
                // As the router answers a revocation surfaced mid-request (REQ-AUTH-074): the
                // session is over, and a data request learns it as a refusal.
                Session::flash('warning', $this->i18n->t('session.ended.revoked'));
                self::fail($sink, Response::apiError('forbidden', $this->i18n->t('error.forbidden'), 403));
            }
        });
    }

    /** Sends an ordinary failure response through a stream's sink, before its first byte. */
    private static function fail(StreamSink $sink, Response $error): void
    {
        $sink->begin($error->status(), $error->headers());
        $sink->write($error->body());
    }

    /**
     * One line of the API's streamed read as the region relays it: a record as its region row,
     * the end line with the API's count. A line that does not decode is dropped — the count
     * then no longer matches what arrived, and the client reports a failed load rather than a
     * complete list (REQ-UI-074).
     */
    private static function relayLine(string $line): string
    {
        $item = json_decode($line, true);
        if (!is_array($item)) {
            return '';
        }
        $out = ($item['end'] ?? false) === true
            ? ['end' => true, 'records' => is_int($item['records'] ?? null) ? $item['records'] : -1]
            : self::regionRow($item);

        return $out === null ? '' : json_encode($out, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n";
    }

    /**
     * One record of the read as the region's row: the events the API reported for it, the
     * cells holding a state other than no data keyed `<unique_event_name>|<instrument>`, and
     * a repeating cell's instances; null for an entry without a record id.
     *
     * @param array<mixed> $row
     * @return array{record_id: string, events: list<string>, cells: object, instances: object}|null
     */
    private static function regionRow(array $row): ?array
    {
        if (!isset($row['record_id'])) {
            return null;
        }
        $events = [];
        $cells = [];
        $instances = [];
        foreach ((is_array($row['events'] ?? null) ? $row['events'] : []) as $event) {
            $uen = (string) ($event['unique_event_name'] ?? '');
            $events[] = $uen;
            foreach ((is_array($event['instruments'] ?? null) ? $event['instruments'] : []) as $instrument) {
                $key = $uen . '|' . (string) ($instrument['name'] ?? '');
                $cells[$key] = self::knownState($instrument['state'] ?? '');
                if (is_array($instrument['instances'] ?? null)) {
                    $instances[$key] = self::instanceStates($instrument['instances']);
                }
            }
        }

        return ['record_id' => (string) $row['record_id'], 'events' => $events, 'cells' => (object) $cells,
            'instances' => (object) $instances];
    }

    /** A state the dashboard knows, or no_data. */
    private static function knownState(mixed $state): string
    {
        return is_string($state) && in_array($state, DataEntry::STATES, true) ? $state : 'no_data';
    }

    /**
     * A record-status `instances` list (REQ-API-156) as `[instance, state]` pairs in ascending
     * instance order; malformed entries are dropped.
     *
     * @param array<mixed> $instances
     * @return list<array{0: int, 1: string}>
     */
    public static function instanceStates(array $instances): array
    {
        $out = [];
        foreach ($instances as $entry) {
            $n = is_array($entry) ? (int) ($entry['instance'] ?? 0) : 0;
            if ($n >= 1) {
                $out[$n] = [$n, self::knownState($entry['state'] ?? '')];
            }
        }
        ksort($out);

        return array_values($out);
    }

    /**
     * POST …/record-status?action=new_record — open the record view on a new participant
     * (§6.3). Only the name is checked here; the record exists once its first import stores
     * it (REQ-API-033), and a name that already exists simply opens that record. A posting
     * from a user who holds no token — one who was never offered the form — is turned back
     * with the answer the dashboard gives (REQ-API-102).
     */
    public function create(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/record-status');

        if (!$this->api->dataApi()->hasMemberToken($projectId)) {
            $this->flashDanger($this->i18n->t('record.not_member'));

            return $back;
        }

        $name = trim($this->request->field('record_id'));
        $arm = $this->fieldId('arm');

        if (!DataEntry::validRecordName($name)) {
            $this->flashDanger($this->i18n->t('records.new.invalid'));
            Session::stash('new_record', ['name' => $name, 'arm' => $arm]);

            return $back;
        }

        $target = self::recordUrl($projectId, $name);

        return Response::redirect($arm > 0 ? $target . '?arm=' . $arm : $target);
    }

    /**
     * POST …/record-status?action=auto_name — the data API's next record name
     * (`generateNextRecordName`, REQ-API-023: ≥ `view_edit` on the target arm, never a name
     * an existing record holds) filled into the new-participant field on the next render.
     */
    public function autoName(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/record-status');
        $arm = $this->fieldId('arm');

        try {
            $answer = $this->dataCall($projectId, ['content' => 'generateNextRecordName']);
        } catch (ApiException $e) {
            $this->flashDanger($this->dataApiFailure($e));

            return $back;
        }

        $name = self::generatedName($answer);
        if ($name === '') {
            $this->flashDanger($this->i18n->t('error.generic'));

            return $back;
        }
        Session::stash('new_record', ['name' => $name, 'arm' => $arm]);

        return $back;
    }

    /**
     * POST …/record-status?action=create_auto — the dashboard's one-click add in auto naming
     * mode (REQ-UI-052): there is no free-text field, so the next number comes straight from
     * `generateNextRecordName` (REQ-API-151) and the record view opens on it. Nothing is
     * stored until the record's first import (REQ-API-033).
     */
    public function createAuto(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/record-status');
        $arm = $this->fieldId('arm');

        try {
            $answer = $this->dataCall($projectId, ['content' => 'generateNextRecordName']);
        } catch (ApiException $e) {
            $this->flashDanger($this->dataApiFailure($e));

            return $back;
        }

        $name = self::generatedName($answer);
        if ($name === '') {
            $this->flashDanger($this->i18n->t('error.generic'));

            return $back;
        }
        $target = self::recordUrl($projectId, $name);

        return Response::redirect($arm > 0 ? $target . '?arm=' . $arm : $target);
    }

    /**
     * The name of a `generateNextRecordName` answer: the JSON form answers a one-element
     * list; the object form is accepted as well.
     *
     * @param array<array-key, mixed> $answer
     */
    private static function generatedName(array $answer): string
    {
        $row = isset($answer['next_record_name']) ? $answer : ($answer[0] ?? []);

        return is_array($row) && is_scalar($row['next_record_name'] ?? null) ? (string) $row['next_record_name'] : '';
    }
}
