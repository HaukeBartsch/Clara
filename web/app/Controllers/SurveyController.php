<?php
// The public survey page (`User_Interface_Design.md` §8.8, REQ-UI-028): `GET /s/{link}`, the
// URL the API hands out when a member issues a link (API §4.17). No login and no session —
// the route runs outside the PHP session altogether (GD-1, Router::needsSession), so no
// cookie is issued — and the browser never calls the API (REQ-API-084): PHP presents the
// link token to the data API, the only credential the page has (REQ-AUTH-039).
//
// The page renders the link's instrument from `content=metadata`, the one read a link token
// is admitted to (§3.10, REQ-API-083), with the same markup and client behaviour as the
// record view (views/partials/form-fields.php, form.js), so the show/hide logic of §8.4 runs
// identically. A link the API refuses — unknown, or revoked (REQ-AUTH-040) — is one
// translated "no longer valid" state with no retry.
//
// Submitting (`POST /s/{link}`) sends field values and nothing else: the row names no record,
// instrument or event, because the API reads all three from the link it was addressed to
// (§3.10, REQ-API-083). That is what keeps the study's record identifier out of the
// respondent's browser — the page could not name the target even if it wanted to.
//
// What a respondent already answered cannot be read back: `content=record` without `data` is
// the export path, which no link is granted (REQ-AUTH-039), and under one submission there is
// nothing to re-open over — a second fill starts from an empty form reached by a new link
// (REQ-UI-028). The submission policy sends only entered values (GD-14), so an untouched field
// cannot blank what is stored.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\DataEntry;
use Clara\Response;

final class SurveyController extends Controller
{
    /** What a link token looks like (the API issues UUIDs); anything else is not asked about. */
    private const TOKEN = '/^[A-Za-z0-9_-]{16,128}$/';

    private const JS_KEYS = [
        'record.hint.integer', 'record.hint.float', 'record.hint.min', 'record.hint.max',
        'record.hint.format', 'record.hint.pattern', 'record.required_missing',
    ];

    /** Default input formats when a date field names none (Data_Validation_Design §4.1). */
    private const DEFAULT_FORMATS = ['date' => 'Y-m-d', 'datetime' => 'Y-m-d H:i'];

    /** GET /s/{link} */
    public function show(): Response
    {
        $token = $this->request->pathParam('link');
        if (preg_match(self::TOKEN, $token) !== 1) {
            return $this->invalid();
        }

        return $this->renderForm($token, null, []);
    }

    /**
     * POST /s/{link} — the respondent's answers (§8.8, REQ-UI-028). The row carries field
     * values only: the record, instrument and event come from the link the API resolves this
     * token to (§3.10, REQ-API-083), so nothing here names them and nothing back there needs
     * to be told to a browser that must not learn them (REQ-AUTH-039).
     *
     * The submission policy is the data-entry form's (GD-14, REQ-UI-031): entered values plus
     * explicitly cleared ones, with the browser zone as the collection timezone (GD-16).
     */
    public function submit(): Response
    {
        $token = $this->request->pathParam('link');
        if (preg_match(self::TOKEN, $token) !== 1) {
            return $this->invalid();
        }

        $values = $this->request->fieldMap('value');
        $params = ['content' => 'record', 'action' => 'import',
            'data' => [DataEntry::submission($values, $this->request->fieldMap('was'))]];
        $tz = $this->request->field('tz');
        if (DataEntry::validTimezone($tz)) {
            $params['tz'] = $tz;
        }

        try {
            $result = $this->api->dataApi()->call($token, $params);
        } catch (ApiException $e) {
            $this->logger->info('survey submission refused', ['code' => $e->code(), 'status' => $e->status()]);

            return match ($e->code()) {
                // Analysis mode closes the survey (GD-20, REQ-API-109). No call a link token
                // may make reports the project's mode, so submitting is where the page learns
                // it — hence the closed state here rather than a read-only render (§8.8).
                'analysis_mode' => $this->notice('survey.closed', 403),
                // This link already carried its submission (REQ-API-145) — revoked between
                // opening and sending, or two tabs racing. The respondent hears that the
                // answer is in, which is exactly what a broken link would not say.
                'survey_submitted' => $this->notice('survey.submitted', 410),
                // Revoked between opening and submitting: the same one state (REQ-AUTH-040).
                'invalid_token', 'forbidden' => $this->invalid(),
                'rate_limited' => $this->notice('survey.rate_limited', 429),
                default => $this->notice('survey.unavailable', 502),
            };
        }

        $outcome = is_array($result[0] ?? null) ? $result[0] : [];
        $code = (int) ($outcome['import_record_id'] ?? 0);
        if ($code !== 1 && $code !== 2) {
            // Nothing of the row was stored (REQ-API-035): the respondent's own values come
            // back, with the API's reason against each field — the same rule as §8.6.
            return $this->renderForm($token, $values,
                DataEntry::importErrors((string) ($outcome['import_form_name'] ?? '')));
        }

        // The visit ends here, and so does the link: storing this row spent it (REQ-API-145), so
        // reopening the URL answers 410 — the panel says as much.
        return $this->standalone('survey_done', ['pageTitle' => $this->i18n->t('survey.title')]);
    }

    /**
     * The link's instrument as a fillable form. `$draft` is a rejected submission's values, or
     * null for a fresh page.
     *
     * @param array<string, string>|null $draft
     * @param array<string, string>      $errors
     */
    private function renderForm(string $token, ?array $draft, array $errors): Response
    {
        try {
            $rows = $this->api->dataApi()->call($token, ['content' => 'metadata']);
        } catch (ApiException $e) {
            $this->logger->info('survey link refused', ['code' => $e->code(), 'status' => $e->status()]);

            return match ($e->code()) {
                // Unknown and revoked look the same to the respondent: one state, no retry
                // (§8.8, REQ-AUTH-040). A spent link is deliberately another state — it tells
                // them their answer arrived (REQ-API-145).
                'invalid_token', 'forbidden' => $this->invalid(),
                'survey_submitted' => $this->notice('survey.submitted', 410),
                'rate_limited' => $this->notice('survey.rate_limited', 429),
                default => $this->notice('survey.unavailable', 502),
            };
        }

        $fields = self::fieldsFromMetadata($rows);
        $instrument = (string) ($fields[0]['form_name'] ?? '');

        // A reason keyed on a field the respondent has an input for belongs under that input;
        // one keyed on anything else (a framing key the API names, or nothing at all) would
        // leave the page promising reasons it shows nowhere — so it rides in the general line.
        $names = array_column($fields, 'field_name');
        $note = '';
        foreach (array_diff_key($errors, array_flip($names)) as $key => $reason) {
            $note .= ($note === '' ? '' : ' ') . ($key === '' ? '' : $key . ': ') . $reason;
        }

        return $this->standalone('survey', [
            'pageTitle' => $this->i18n->t('survey.title'),
            'instrument' => $instrument,
            'items' => DataEntry::layout($fields),
            // The shared partial's inputs: a fresh form — nothing stored is readable through a
            // link (REQ-AUTH-039), no history, no identifier shown (it is the study's, not the
            // respondent's), inputs live so the show/hide logic can be followed.
            'canEdit' => true,
            'exists' => false,
            'identifier' => '',
            'record' => '',
            'prefill' => [],
            'draft' => $draft,
            'errors' => array_intersect_key($errors, array_flip($names)),
            'formNote' => $note,
            'patterns' => [],
            'formats' => self::DEFAULT_FORMATS,
            'clientContext' => ['firstEvent' => '', 'event' => '', 'anyEvent' => true, 'values' => (object) []],
        ], [
            'scripts' => ['/assets/app.js', '/assets/js/survey.js'],
            'jsKeys' => self::JS_KEYS,
        ]);
    }

    /**
     * The `content=metadata` rows (API §3.4) in the field shape the form partial renders —
     * the structure endpoints' shape (§4.11). The record identifier is left out: it names the
     * record, which the respondent neither enters nor sees. Choices come from the combined
     * `code, label | code, label` column, split at each pair's first `, ` — codes are numbers
     * (REQ-VAL-022), so a label may itself contain a comma.
     *
     * @param array<mixed> $rows
     * @return list<array<string, mixed>>
     */
    public static function fieldsFromMetadata(array $rows): array
    {
        $fields = [];
        foreach ($rows as $row) {
            if (!is_array($row) || ($row['record_identifier'] ?? '') === 'Y') {
                continue;
            }
            $type = (string) ($row['field_type'] ?? 'text');
            $combined = (string) ($row['select_choices_or_calculations'] ?? '');
            $fields[] = [
                'field_name' => (string) ($row['field_name'] ?? ''),
                'form_name' => (string) ($row['form_name'] ?? ''),
                'field_label' => (string) ($row['field_label'] ?? ''),
                'field_type' => $type,
                'section_header' => (string) ($row['section_header'] ?? ''),
                'field_note' => (string) ($row['field_note'] ?? ''),
                'choices' => $type === 'calculated' ? '' : self::choicesFromCombined($combined),
                'validation_type' => (string) ($row['validation_type'] ?? ''),
                'validation_min' => (string) ($row['validation_min'] ?? ''),
                'validation_max' => (string) ($row['validation_max'] ?? ''),
                'validation_format' => '',
                'required' => ($row['required_field'] ?? '') === 'Y',
                'branching_logic' => (string) ($row['branching_logic'] ?? ''),
                'matrix_group' => (string) ($row['matrix_group_name'] ?? ''),
            ];
        }

        return $fields;
    }

    /** `1, Yes | 2, No` → the stored `1$Yes##2$No` encoding the form partial reads. */
    public static function choicesFromCombined(string $combined): string
    {
        if (trim($combined) === '') {
            return '';
        }
        $pairs = [];
        foreach (explode(' | ', $combined) as $pair) {
            [$code, $label] = array_pad(explode(', ', $pair, 2), 2, '');
            $code = trim($code);
            if ($code !== '') {
                $pairs[] = $code . '$' . ($label === '' ? $code : $label);
            }
        }

        return implode('##', $pairs);
    }

    /** The one state of a link that cannot be used (§8.8): no detail, no retry. */
    private function invalid(): Response
    {
        return $this->notice('survey.invalid', 404);
    }

    private function notice(string $key, int $status): Response
    {
        $response = $this->standalone('survey_notice', [
            'pageTitle' => $this->i18n->t('survey.title'),
            'message' => $this->i18n->t($key),
        ]);

        return Response::html($response->body(), $status);
    }
}
