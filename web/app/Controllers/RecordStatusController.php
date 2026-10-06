<?php
// The Record Status Dashboard (`User_Interface_Design.md` §6.3, REQ-UI-019): the participant
// overview — every record visible under the data-access-group rule (REQ-AUTH-045) as a row,
// the arm's events as column groups with their instruments in order, and one colour-coded
// cell per (record, event, instrument): grey no data, amber some data, green finished.
//
// The page renders the structure — arm tabs, the column headers, the new-participant form —
// and the rows are the route's `records` data region, bound by `record-status.js` from the
// same URL (REQ-UI-032/044). The region is the API's record-status read reshaped into cells;
// like the read, it carries no field value (REQ-API-074).
//
// New participant (§6.3): a record name typed by the user or proposed by the data API's
// `generateNextRecordName` (REQ-API-023), then the record view opens on it; nothing is stored
// until its first import (REQ-API-033), which also places it in the member's active data
// access group (REQ-API-093). Offered at data access ≥ `view_edit` on some arm, and never in
// analysis mode (§8.1, REQ-UI-035).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\DataEntry;
use Clara\Response;
use Clara\Session;

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
        $arms = self::structure($detail, $permissions, $mapping,
            self::byPosition(is_array($detail['instruments'] ?? null) ? $detail['instruments'] : []));
        // Entry is held per (instrument, event), so an arm offers a new participant when its
        // default reaches view_edit or one pair in it does (REQ-AUTH-069).
        $editable = self::editableArmNumbers($permissions, $mapping);
        $editableArms = array_values(array_filter($arms,
            static fn (array $arm): bool => in_array($arm['arm_num'], $editable, true)));

        $proposed = Session::take('new_record');

        return $this->renderSection($detail, 'project/record_status', 'records.title', [
            'statusUrl' => '/projects/' . $projectId . '/record-status',
            'recordBase' => '/projects/' . $projectId . '/records/',
            'arms' => $arms,
            // §8.1 / REQ-UI-035: the new-participant affordance is absent in analysis mode,
            // and for a member who may enter data nowhere (REQ-UI-003).
            'newRecord' => $mode !== 'analysis' && $editableArms !== [] ? [
                'arms' => $editableArms,
                'name' => (string) ($proposed['name'] ?? ''),
                'arm' => (int) ($proposed['arm'] ?? 0),
            ] : null,
        ], [
            'scripts' => ['/assets/app.js', '/assets/js/record-status.js'],
            'jsKeys' => ['js.loading', 'js.load_failed', 'js.retry', 'records.empty',
                'records.state.no_data', 'records.state.some_data', 'records.state.finished',
                'records.open_cell'],
        ], $mode);
    }

    /**
     * The `records` data region (REQ-UI-044): one row per visible record, each cell keyed
     * `<unique_event_name>|<instrument name>` with its three-state completion. Authorized
     * like the page — the same detail read and data-access gate — and disclosing exactly
     * what the API's record-status read does: names and states, no values (REQ-API-074).
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDataAccess($detail);
        $projectId = $this->projectIdOf($detail);

        $records = [];
        foreach ($this->api->get('/api/v1/projects/' . $projectId . '/record-status') as $row) {
            if (!is_array($row) || !isset($row['record_id'])) {
                continue;
            }
            $cells = [];
            foreach ((is_array($row['events'] ?? null) ? $row['events'] : []) as $event) {
                $uen = (string) ($event['unique_event_name'] ?? '');
                foreach ((is_array($event['instruments'] ?? null) ? $event['instruments'] : []) as $instrument) {
                    $state = (string) ($instrument['state'] ?? '');
                    $cells[$uen . '|' . (string) ($instrument['name'] ?? '')] =
                        in_array($state, DataEntry::STATES, true) ? $state : 'no_data';
                }
            }
            $records[] = ['record_id' => (string) $row['record_id'], 'cells' => (object) $cells];
        }

        return Response::json(['records' => $records]);
    }

    /**
     * POST …/record-status?action=new_record — open the record view on a new participant
     * (§6.3). Only the name is checked here; the record exists once its first import stores
     * it (REQ-API-033), and a name that already exists simply opens that record.
     */
    public function create(): Response
    {
        $projectId = $this->projectIdFromPath();
        $name = trim($this->request->field('record_id'));
        $arm = $this->fieldId('arm');

        if (!DataEntry::validRecordName($name)) {
            $this->flashDanger($this->i18n->t('records.new.invalid'));
            Session::stash('new_record', ['name' => $name, 'arm' => $arm]);

            return Response::redirect('/projects/' . $projectId . '/record-status');
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

        // The JSON form answers a one-element list; the object form is accepted as well.
        $row = isset($answer['next_record_name']) ? $answer : ($answer[0] ?? []);
        $name = is_array($row) && is_scalar($row['next_record_name'] ?? null) ? (string) $row['next_record_name'] : '';
        if ($name === '') {
            $this->flashDanger($this->i18n->t('error.generic'));

            return $back;
        }
        Session::stash('new_record', ['name' => $name, 'arm' => $arm]);

        return $back;
    }
}
