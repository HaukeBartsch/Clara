<?php
// The Instrument Designer (`User_Interface_Design.md` §7, REQ-UI-021…024, `project_admin`):
// the instrument list (§7.1) and, per instrument, the field editor (§7.2) — an ordered field
// table, and one edit form with the full data-dictionary attribute set for the field picked
// with `?field=<id>` (or `?field=new`). Branching logic and calculations are written in the
// expression editor (§7.3); a calculated field carries the test panel (§7.4).
//
// Field order comes from `GET …/fields` — never `GET …/fields/order`, which routes to the
// reorder handler (Plan/Web_Implementation.md §7 rule 2). Every write sends exactly the
// attributes the field endpoints accept (§7 rule 1); the API is the only validator of names,
// choices and expressions, and its `validation_error` reason is shown verbatim (REQ-VAL-029).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Response;
use Clara\Session;

final class DesignController extends StructureController
{
    /**
     * The field types of §7.2, in the order the type select lists them. `yesno` and `notes`
     * (REQ-DB-043/044) have no choice editor: the API fixes their choices.
     */
    public const FIELD_TYPES = ['text', 'notes', 'dropdown', 'radio', 'checkbox', 'yesno', 'matrix', 'description', 'header', 'calculated'];

    /**
     * Types that carry choices (the `code$label##…` encoding, REQ-VAL-022). A checkbox stores
     * one column per choice instead of a code (REQ-DB-042), but is designed the same way.
     */
    public const CHOICE_TYPES = ['dropdown', 'radio', 'checkbox', 'matrix'];

    /** Validation types that preset `direct_identifier` (REQ-EXP-020, API §4.11). */
    public const IDENTIFIER_TYPES = ['email', 'MRN', 'international phone', 'national phone'];

    /**
     * The most an instrument import takes in one go, all files together (§7.6): the files ride
     * in the session between preview and import, and base64 in the API body (REQ-TECH-011).
     */
    public const IMPORT_LIMIT = 8 * 1024 * 1024;

    /** GET /projects/{id}/design — the instrument list (§7.1). */
    public function index(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDesigner($detail);
        $projectId = $this->projectIdOf($detail);
        $designUrl = '/projects/' . $projectId . '/design';
        $state = $this->structureState($projectId, $designUrl);

        return $this->renderSection($detail, 'project/design', 'design.title', $state + [
            'designUrl' => $designUrl,
            'setupUrl' => '/projects/' . $projectId . '/setup',
            'instruments' => self::listOf($this->api->get('/api/v1/projects/' . $projectId . '/instruments')),
            'import' => self::importView(Session::kept('import')),
        ], ['scripts' => self::SCRIPTS, 'jsKeys' => self::JS_KEYS], $state['mode']);
    }

    // --- instrument import (§7.6, REQ-UI-053) ------------------------------------------

    /**
     * POST …?action=import_preview — the chosen files, in file-name order, through the API's
     * dry run (`POST …/instruments/import`, REQ-API-152). The files stay in the session until
     * the import or a cancel; the page shows the preview from the result. PHP never reads the
     * files: the API is their only reader (DEV-API-27).
     */
    public function importPreview(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/design#import');
        $files = [];
        $total = 0;
        foreach ($this->request->uploads('import_files') as $upload) {
            if ($upload['error'] === UPLOAD_ERR_NO_FILE) {
                continue;
            }
            $name = basename(str_replace('\\', '/', $upload['name']));
            $content = $upload['error'] === UPLOAD_ERR_OK && is_file($upload['tmp_name'])
                ? file_get_contents($upload['tmp_name']) : false;
            if ($content === false) {
                $this->flashDanger($this->i18n->t('design.import.upload_failed', ['name' => $name]));

                return $back;
            }
            $total += strlen($content);
            if ($total > self::IMPORT_LIMIT) {
                $this->flashDanger($this->i18n->t('design.import.too_large', ['limit' => (string) (self::IMPORT_LIMIT >> 20)]));

                return $back;
            }
            $files[] = ['name' => $name, 'content' => base64_encode($content)];
        }
        if ($files === []) {
            $this->flashDanger($this->i18n->t('design.import.none'));

            return $back;
        }
        usort($files, static fn (array $a, array $b): int => strcmp($a['name'], $b['name']));

        return $this->previewImport($projectId, $files);
    }

    /** POST …?action=import_reorder — one file up or down, then the dry run again. */
    public function importReorder(): Response
    {
        $projectId = $this->projectIdFromPath();
        $kept = Session::kept('import');
        if ($kept === null) {
            $this->flashDanger($this->i18n->t('design.import.expired'));

            return Response::redirect('/projects/' . $projectId . '/design#import');
        }
        $files = array_values($kept['files'] ?? []);
        $index = (int) $this->request->field('index');
        $target = $this->request->field('direction') === 'up' ? $index - 1 : $index + 1;
        if (isset($files[$index], $files[$target])) {
            [$files[$index], $files[$target]] = [$files[$target], $files[$index]];
        }

        return $this->previewImport($projectId, $files);
    }

    /**
     * POST …?action=import_instruments — the previewed files, in the shown order, imported for
     * real; a structure write like any other (§6.7/§6.8), so a breaking change is asked about
     * and replayed. The import maps no instrument, which the success line says.
     */
    public function importInstruments(): Response
    {
        $projectId = $this->projectIdFromPath();
        $designUrl = '/projects/' . $projectId . '/design';
        $kept = Session::kept('import');
        if ($kept === null) {
            $this->flashDanger($this->i18n->t('design.import.expired'));

            return Response::redirect($designUrl . '#import');
        }
        $view = self::importView($kept);

        return $this->structureWrite($designUrl, function (int $projectId) use ($kept): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/instruments/import', $this->withAck(['files' => $kept['files']]));
            Session::forget('import');
        }, $this->i18n->t('design.import.done', [
            'instruments' => (string) ($view['created'] ?? 0),
            'fields' => (string) ($view['fieldCount'] ?? 0),
        ]));
    }

    /** POST …?action=import_cancel — drops the held files and their preview. */
    public function importCancel(): Response
    {
        Session::forget('import');

        return Response::redirect('/projects/' . $this->projectIdFromPath() . '/design');
    }

    /** @param list<array{name: string, content: string}> $files */
    private function previewImport(int $projectId, array $files): Response
    {
        $back = Response::redirect('/projects/' . $projectId . '/design#import');
        try {
            $result = $this->api->post('/api/v1/projects/' . $projectId . '/instruments/import',
                ['files' => $files, 'dry_run' => true]);
        } catch (ApiException $e) {
            $this->logger->info('import preview rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));
            Session::forget('import');

            return $back;
        }
        Session::keep('import', ['files' => $files, 'result' => is_array($result) ? $result : []]);

        return $back;
    }

    /**
     * The preview the page renders from a held import: the file names in import order, the
     * dry run's result, each field's notes by file and row, and the counts the import line
     * names. Null when nothing is held.
     *
     * @param array<string, mixed>|null $kept
     * @return array<string, mixed>|null
     */
    public static function importView(?array $kept): ?array
    {
        if ($kept === null) {
            return null;
        }
        $result = is_array($kept['result'] ?? null) ? $kept['result'] : [];
        $notes = [];
        foreach ((is_array($result['warnings'] ?? null) ? $result['warnings'] : []) as $note) {
            if (is_array($note) && (int) ($note['row'] ?? 0) > 0) {
                $notes[($note['file'] ?? '') . '|' . (int) $note['row']][] = $note;
            }
        }
        $created = 0;
        $fieldCount = 0;
        $origins = [];
        foreach ((is_array($result['instruments'] ?? null) ? $result['instruments'] : []) as $instrument) {
            if (!is_array($instrument)) {
                continue;
            }
            if (empty($instrument['existing'])) {
                $created++;
            }
            $fieldCount += count(is_array($instrument['fields'] ?? null) ? $instrument['fields'] : []);
            if (($instrument['origin'] ?? '') !== '') {
                $origins[(string) $instrument['file']] = (string) $instrument['origin'];
            }
        }

        return [
            'files' => array_map(static fn (array $f): string => (string) ($f['name'] ?? ''), $kept['files'] ?? []),
            'origins' => $origins,
            'result' => $result,
            'notes' => $notes,
            'created' => $created,
            'fieldCount' => $fieldCount,
        ];
    }

    /** GET /projects/{id}/design/instruments/{iid} — the field editor (§7.2). */
    public function instrument(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDesigner($detail);
        $projectId = $this->projectIdOf($detail);
        $iid = $this->instrumentIdFromPath();
        $base = '/api/v1/projects/' . $projectId;
        $editorUrl = '/projects/' . $projectId . '/design/instruments/' . $iid;

        $instrument = null;
        foreach (self::listOf($this->api->get($base . '/instruments')) as $candidate) {
            if ((int) ($candidate['id'] ?? 0) === $iid) {
                $instrument = $candidate;
            }
        }
        if ($instrument === null) {
            throw new ApiException('not_found', '', 404);
        }

        $state = $this->structureState($projectId, $editorUrl);
        $fields = self::listOf($this->api->get($base . '/instruments/' . $iid . '/fields'));

        // The field the edit form shows: one of this instrument's, or a blank one to add.
        $requested = $this->request->query('field');
        $selected = null;
        if ($requested === 'new' && $state['canEdit']) {
            $selected = ['id' => 0];
        } else {
            foreach ($fields as $field) {
                if ((string) ($field['id'] ?? '') === $requested) {
                    $selected = $field;
                }
            }
        }
        // A rejected save re-renders the form as the designer left it, reason above it.
        $draft = Session::take('field_draft');
        if ($draft !== null && $selected !== null && (int) ($draft['id'] ?? 0) === (int) $selected['id']) {
            $selected = $draft + $selected;
        }

        $validationTypes = [];
        if ($selected !== null) {
            foreach ($this->api->get('/api/v1/validationTypes') as $type) {
                if (is_array($type) && isset($type['name'])) {
                    $validationTypes[] = ['name' => (string) $type['name'], 'builtin' => !empty($type['builtin'])];
                }
            }
        }

        // The test panel's record picker (§7.4) lists the records this user can see
        // (REQ-AUTH-045) — read only when a stored calculated field is open, since it is the
        // one place the page needs it. The record-status read carries no values (REQ-API-074).
        $records = [];
        $isCalculated = $selected !== null && (int) $selected['id'] !== 0
            && ($selected['field_type'] ?? '') === 'calculated';
        if ($isCalculated) {
            foreach ($this->api->get($base . '/record-status') as $row) {
                if (is_array($row) && isset($row['record_id'])) {
                    $records[] = (string) $row['record_id'];
                }
            }
        }
        $test = Session::take('calc_test');
        if ($test !== null && $selected !== null && (int) ($test['field_id'] ?? 0) !== (int) $selected['id']) {
            $test = null;
        }

        return $this->renderSection($detail, 'project/design_instrument', 'design.title', $state + [
            'editorUrl' => $editorUrl,
            'designUrl' => '/projects/' . $projectId . '/design',
            'instrument' => $instrument,
            'fields' => $fields,
            'selected' => $selected,
            'choiceRows' => $selected === null ? [] : self::decodeChoices((string) ($selected['choices'] ?? '')),
            'validationTypes' => $validationTypes,
            'fieldTypes' => self::FIELD_TYPES,
            'choiceTypes' => self::CHOICE_TYPES,
            'identifierTypes' => self::IDENTIFIER_TYPES,
            'isCalculated' => $isCalculated,
            'records' => $records,
            'test' => $test,
        ], ['scripts' => self::SCRIPTS, 'jsKeys' => self::JS_KEYS], $state['mode']);
    }

    // --- field writes (§7.2) ----------------------------------------------------------

    /**
     * POST …?action=save_field — `POST …/fields` for a new field (appended at the end),
     * `PUT …/fields/{fid}` for an existing one. A rename renames the stored values in the same
     * transaction (REQ-VAL-014); a name over 26 characters is a warning in the form, never a
     * refusal here (REQ-VAL-013). A successful save returns to the field list — the form closes;
     * a refused write keeps `?field=` so the draft re-renders.
     */
    public function saveField(): Response
    {
        $iid = $this->instrumentIdFromPath();
        $fieldId = $this->objectId('field_id');
        $body = $this->fieldBody($fieldId === 0);
        $name = (string) $body['field_name'];
        $editor = $this->editorUrl($iid);
        $savedId = $fieldId;
        $stored = false;

        return $this->structureWrite(
            static function () use ($editor, &$savedId, &$stored): string {
                // A saved field closes the form; a rejected one re-opens it with the draft.
                if ($stored) {
                    return $editor;
                }

                return $editor . '?field=' . ($savedId === 0 ? 'new' : $savedId);
            },
            function (int $projectId) use ($iid, $fieldId, $body, &$savedId, &$stored): void {
                $path = '/api/v1/projects/' . $projectId . '/instruments/' . $iid . '/fields';
                if ($fieldId === 0) {
                    $created = $this->api->post($path, $this->withAck($body));
                    $savedId = (int) ($created['id'] ?? 0);
                } else {
                    $this->api->put($path . '/' . $fieldId, $this->withAck($body));
                }
                $stored = true;
            },
            $this->i18n->t($fieldId === 0 ? 'design.field.added' : 'design.field.updated', ['name' => $name]),
            function () use ($fieldId, $body): void {
                Session::stash('field_draft', ['id' => $fieldId] + $body);
            }
        );
    }

    public function moveField(): Response
    {
        $iid = $this->instrumentIdFromPath();
        $fieldId = $this->objectId('field_id');
        $direction = $this->request->field('direction');

        return $this->structureWrite($this->editorUrl($iid), function (int $projectId) use ($iid, $fieldId, $direction): void {
            $path = '/api/v1/projects/' . $projectId . '/instruments/' . $iid . '/fields';
            $ids = array_map(static fn (array $f): int => (int) ($f['id'] ?? 0), self::listOf($this->api->get($path)));
            $this->api->put($path . '/order', $this->withAck(['order' => SetupController::moved($ids, $fieldId, $direction)]));
        }, $this->i18n->t('setup.reordered'));
    }

    /** DELETE …/fields/{fid}: stored values go in the same transaction (DEV-API-5). */
    public function deleteField(): Response
    {
        $iid = $this->instrumentIdFromPath();
        $fieldId = $this->objectId('field_id');
        $name = $this->request->field('name');

        return $this->structureWrite($this->editorUrl($iid), function (int $projectId) use ($iid, $fieldId): void {
            $this->api->delete('/api/v1/projects/' . $projectId . '/instruments/' . $iid . '/fields/' . $fieldId, [],
                $this->withAck([]) ?: null);
        }, $this->i18n->t('design.field.deleted', ['name' => $name]));
    }

    /**
     * POST …?action=test_calc — the dry run of §7.4 (`POST …/records/{record}/fields/{fid}/test`,
     * REQ-API-096): the stored expression, or the draft when one is given. Nothing is stored;
     * the result — value plus one line per evaluation problem (REQ-VAL-038) — is held for the
     * next render of the field editor.
     */
    public function testCalculation(): Response
    {
        $projectId = $this->projectIdFromPath();
        $iid = $this->instrumentIdFromPath();
        $fieldId = $this->objectId('field_id');
        $record = $this->request->field('record');
        $expression = trim($this->request->field('expression'));
        $back = Response::redirect($this->editorUrl($iid) . '?field=' . $fieldId);
        $result = ['field_id' => $fieldId, 'record' => $record, 'expression' => $expression];

        if ($record === '' || $fieldId === 0) {
            $this->flashDanger($this->i18n->t('design.test.no_record'));
            Session::stash('calc_test', $result);

            return $back;
        }

        try {
            $answer = $this->api->post(
                '/api/v1/projects/' . $projectId . '/records/' . rawurlencode($record) . '/fields/' . $fieldId . '/test',
                $expression === '' ? [] : ['expression' => $expression]
            );
        } catch (ApiException $e) {
            $this->logger->info('calculation test rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));
            Session::stash('calc_test', $result);

            return $back;
        }

        $problems = [];
        foreach ((is_array($answer['problems'] ?? null) ? $answer['problems'] : []) as $problem) {
            if (is_array($problem)) {
                $problems[] = ['operand' => (string) ($problem['operand'] ?? ''), 'problem' => (string) ($problem['problem'] ?? '')];
            }
        }
        Session::stash('calc_test', $result + [
            'ran' => true,
            'value' => (string) ($answer['value'] ?? ''),
            'problems' => $problems,
        ]);

        return $back;
    }

    // --- helpers ----------------------------------------------------------------------

    /**
     * The field object of the edit form, as the field endpoints accept it (§4.11): every
     * writable attribute, and nothing else. Choices are encoded from the code/label rows;
     * a calculation is sent only for a calculated field (the API refuses one elsewhere).
     *
     * `direct_identifier` is preset by the API for the identifier-shaped validation types
     * (REQ-EXP-020). On a new field whose box the form left unticked, the attribute is
     * omitted so that preset applies — unless the designer cleared it explicitly, which the
     * form reports and which is then sent as `false` (REQ-EXP-021).
     *
     * @return array<string, mixed>
     */
    private function fieldBody(bool $creating): array
    {
        $type = $this->request->field('field_type');
        $validationType = $this->request->field('validation_type');
        $body = [
            // The form turns the typed name into a working one (the JS does the same as the
            // user types): lowercase, every other character an underscore — the shape the
            // API enforces (`^[a-z0-9_]+$`).
            'field_name' => preg_replace('/[^a-z0-9_]/', '_', strtolower(trim($this->request->field('field_name')))),
            'field_label' => $this->request->field('field_label'),
            'field_type' => $type,
            'section_header' => $this->request->field('section_header'),
            'choices' => in_array($type, self::CHOICE_TYPES, true)
                ? self::encodeChoices($this->request->fieldList('choice_code'), $this->request->fieldList('choice_label'))
                : '',
            'field_note' => $this->request->field('field_note'),
            'validation_type' => $validationType,
            'validation_format' => trim($this->request->field('validation_format')),
            'validation_min' => trim($this->request->field('validation_min')),
            'validation_max' => trim($this->request->field('validation_max')),
            'required' => $this->request->field('required') === '1',
            'branching_logic' => trim($this->request->field('branching_logic')),
            'calculation' => $type === 'calculated' ? trim($this->request->field('calculation')) : '',
            'matrix_group' => $type === 'matrix' ? trim($this->request->field('matrix_group')) : '',
            'field_annotation' => trim($this->request->field('field_annotation')),
            'personal_information' => $this->request->field('personal_information') === '1',
        ];

        $ticked = $this->request->field('direct_identifier') === '1';
        $cleared = $this->request->field('direct_identifier_cleared') === '1';
        if (!$creating || $ticked || $cleared || !in_array($validationType, self::IDENTIFIER_TYPES, true)) {
            $body['direct_identifier'] = $ticked;
        }

        return $body;
    }

    /**
     * Encodes the choice rows as `code$label##code$label` (REQ-VAL-022). A row left entirely
     * blank is dropped; anything else is passed on as typed, for the API to judge.
     *
     * @param list<string> $codes
     * @param list<string> $labels
     */
    public static function encodeChoices(array $codes, array $labels): string
    {
        $parts = [];
        foreach ($codes as $i => $code) {
            $code = trim($code);
            $label = trim($labels[$i] ?? '');
            if ($code === '' && $label === '') {
                continue;
            }
            $parts[] = $code . '$' . $label;
        }

        return implode('##', $parts);
    }

    /**
     * The stored encoding back into rows for the list editor.
     *
     * @return list<array{code: string, label: string}>
     */
    public static function decodeChoices(string $choices): array
    {
        if ($choices === '') {
            return [];
        }
        $rows = [];
        foreach (explode('##', $choices) as $part) {
            [$code, $label] = array_pad(explode('$', $part, 2), 2, '');
            $rows[] = ['code' => $code, 'label' => $label];
        }

        return $rows;
    }

    /** `{iid}` of the route — a provisional (negative) id while a staging set is open. */
    private function instrumentIdFromPath(): int
    {
        $iid = self::parseObjectId($this->request->pathParam('iid'));
        if ($iid === 0) {
            throw new ApiException('not_found', '', 404);
        }

        return $iid;
    }

    private function editorUrl(int $iid): string
    {
        return '/projects/' . $this->projectIdFromPath() . '/design/instruments/' . $iid;
    }
}
