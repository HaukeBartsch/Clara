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
// Not yet possible, and stated on the page rather than offered as a control that would fail
// (REQ-UI-003): submitting answers. An import must name the link's record and event, and no
// call a link token may make tells the page what they are; the API change that closes this
// (and the prefill of a reopened link, REQ-AUTH-042) is listed in the M6 report.

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

        try {
            $rows = $this->api->dataApi()->call($token, ['content' => 'metadata']);
        } catch (ApiException $e) {
            $this->logger->info('survey link refused', ['code' => $e->code(), 'status' => $e->status()]);

            return match ($e->code()) {
                // Unknown and revoked look the same to the respondent: one state, no retry
                // (§8.8, REQ-AUTH-040).
                'invalid_token', 'forbidden' => $this->invalid(),
                'rate_limited' => $this->notice('survey.rate_limited', 429),
                default => $this->notice('survey.unavailable', 502),
            };
        }

        $fields = self::fieldsFromMetadata($rows);
        $instrument = (string) ($fields[0]['form_name'] ?? '');

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
            'draft' => null,
            'errors' => [],
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
