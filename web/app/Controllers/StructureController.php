<?php
// What the Setup page (§6.2) and the Instrument Designer (§7) share: both edit the project's
// structure, both are `project_admin` pages, and both carry the same mode-dependent controls.
//
//   * development — edits apply directly, no staging controls (§6.7 last sentence);
//   * production  — edits need an open staging set: **Start staging**, then a persistent
//                   banner with **Commit staged changes** (a dialog listing the staged diff,
//                   breaking changes acknowledged first) and **Discard** (§6.7, REQ-UI-034).
//                   While no set is open the edit controls are absent — the API would refuse
//                   every one of them with 409 — and the page says why;
//   * analysis    — edits apply directly, and one the API classifies as breaking comes back
//                   409 once; the page then asks, naming the change, and resubmits the
//                   identical request with `acknowledge_breaking` (§6.8, REQ-UI-037).
//
// Every write is a form POST to the page route with `?action=` (§2.1) followed by a redirect
// back (PRG); results that outlive the redirect — the breaking-change question, the commit
// dialog to reopen, a calculation test — ride in the one-shot session stash.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Permissions;
use Clara\Response;
use Clara\Session;

abstract class StructureController extends ProjectPageController
{
    /** The view options both pages share: the runtime, the confirmation module, the editor. */
    protected const SCRIPTS = ['/assets/app.js', '/assets/js/admin.js', '/assets/js/design.js'];

    /** UI strings the two modules read from the data-i18n block (§9). */
    protected const JS_KEYS = [
        'admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied',
        'design.refs.loading', 'design.refs.failed', 'design.refs.choose', 'design.refs.none',
        'design.name_long', 'design.direct_identifier_cleared',
    ];

    /**
     * The project_admin gate of §2.1 for Setup and Design: the page is the API's to refuse
     * as well (every structure write is project_admin), but a member who may not use it gets
     * the §3.4 refusal page rather than a page of controls that would all fail.
     *
     * @param array<string, mixed> $detail
     */
    protected function requireDesigner(array $detail): void
    {
        if (!Permissions::fromProjectDetail($detail)->projectAdmin) {
            throw new ApiException('forbidden', '', 403);
        }
    }

    /**
     * The mode-dependent state a structure page renders from: the mode, the open staging set
     * with its diff split into non-breaking and breaking (§6.7, read only when one is open),
     * whether edit controls are offered at all, and the one-shot dialogs left by the
     * previous request.
     *
     * @return array<string, mixed>
     */
    protected function structureState(int $projectId, string $back): array
    {
        $state = $this->modeState($projectId);
        $staging = null;
        if ($state['mode'] === 'production' && $state['staging_open']) {
            $staging = $this->stagingDiff($projectId);
        }

        return [
            'mode' => $state['mode'],
            'stagingOpen' => $state['staging_open'],
            'staging' => $staging,
            // Production without an open set: the API refuses every structure write (409),
            // so the controls are not emitted and the banner offers Start staging instead.
            'canEdit' => $state['mode'] !== 'production' || $state['staging_open'],
            'stagingBase' => $back,
            'breaking' => Session::take('breaking'),
            'reopenCommit' => Session::take('commit_open') !== null,
        ];
    }

    /**
     * `GET …/staging` split for the commit dialog (§6.7): the non-breaking and the breaking
     * changes, each breaking one with its reason (classification is the API's, §4.21).
     *
     * @return array{opened_at: string, nonBreaking: list<array<string, mixed>>, breaking: list<array<string, mixed>>, count: int}
     */
    private function stagingDiff(int $projectId): array
    {
        $read = $this->api->get('/api/v1/projects/' . $projectId . '/staging');
        $nonBreaking = [];
        $breaking = [];
        foreach ((is_array($read['changes'] ?? null) ? $read['changes'] : []) as $change) {
            if (!is_array($change)) {
                continue;
            }
            if (!empty($change['breaking'])) {
                $breaking[] = $change;
            } else {
                $nonBreaking[] = $change;
            }
        }

        return [
            'opened_at' => (string) ($read['opened_at'] ?? ''),
            'nonBreaking' => $nonBreaking,
            'breaking' => $breaking,
            'count' => count($nonBreaking) + count($breaking),
        ];
    }

    /** True when this request is the resubmission that acknowledges a breaking change (§6.8). */
    protected function acknowledged(): bool
    {
        return $this->request->field('acknowledge_breaking') === '1';
    }

    /**
     * A structure body with the analysis-mode acknowledgement added when this request carries
     * it — and only then, so a first attempt never pre-acknowledges anything (REQ-API-111).
     *
     * @param array<string, mixed> $body
     * @return array<string, mixed>
     */
    protected function withAck(array $body): array
    {
        if ($this->acknowledged()) {
            $body['acknowledge_breaking'] = true;
        }

        return $body;
    }

    /**
     * Runs one structure write and redirects back. A 409 that names a breaking change (the
     * API's analysis-mode guard, §4.21) is not a failure line: the request is kept for one
     * render and the page asks — the change in plain words, its consequence, *Delete anyway*
     * or *Cancel* — and confirming replays it with the acknowledgement (§6.8). Every other
     * refusal is one translated line (§3.4), after which `$onFailure` may keep what the page
     * needs to re-render the rejected form as the user left it. `$back` may be a closure,
     * read after the call, when the target depends on its answer (a new field's id).
     */
    protected function structureWrite(string|\Closure $back, callable $call, string $successText, ?callable $onFailure = null): Response
    {
        $projectId = $this->projectIdFromPath();
        try {
            $call($projectId);
        } catch (ApiException $e) {
            $this->logger->info('structure change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            if ($e->code() === 'conflict' && !$this->acknowledged() && self::namesBreakingChange($e)) {
                Session::stash('breaking', [
                    'action' => $this->request->path() . '?action=' . rawurlencode($this->request->action()),
                    'message' => self::breakingReason($e->getMessage()),
                    'fields' => self::flattenFields($this->request->fields()),
                ]);

                return Response::redirect(is_string($back) ? $back : $back());
            }
            $this->flashDanger(Messages::forApiException($this->i18n, $e));
            if ($onFailure !== null) {
                $onFailure($e);
            }

            return Response::redirect(is_string($back) ? $back : $back());
        }

        if ($successText !== '') {
            $this->flashSuccess($successText);
        }
        if ($this->acknowledged()) {
            // Nothing stages an analysis-mode change and there is no commit to undo it from.
            $this->flashSuccess($this->i18n->t('structure.breaking.applied'));
        }

        return Response::redirect(is_string($back) ? $back : $back());
    }

    /**
     * Whether a 409 is the analysis-mode guard rather than a duplicate name or a reference
     * still in use: the guard's message is the one that asks for `acknowledge_breaking`
     * (API §4.21, `writeBreakingChanges`).
     */
    private static function namesBreakingChange(ApiException $e): bool
    {
        return str_contains($e->getMessage(), 'acknowledge_breaking');
    }

    /** The guard's message without its instruction to the API caller. */
    private static function breakingReason(string $message): string
    {
        $cut = strpos($message, ' — resend with acknowledge_breaking');

        return $cut === false ? $message : substr($message, 0, $cut);
    }

    /**
     * A parsed form body as the name/value pairs of hidden inputs, so a nested field
     * (`map[7][]`, `choice_code[]`) replays exactly as it was posted.
     *
     * @param array<array-key, mixed> $fields
     * @return list<array{0: string, 1: string}>
     */
    public static function flattenFields(array $fields, string $prefix = ''): array
    {
        $pairs = [];
        $isList = array_is_list($fields);
        foreach ($fields as $key => $value) {
            $name = $prefix === '' ? (string) $key : $prefix . ($isList ? '[]' : '[' . $key . ']');
            if (is_array($value)) {
                array_push($pairs, ...self::flattenFields($value, $name));
            } elseif (is_scalar($value)) {
                $pairs[] = [$name, (string) $value];
            }
        }

        return $pairs;
    }

    /**
     * The array items of a read that should be a list of objects; anything else is dropped.
     *
     * @return list<array<string, mixed>>
     */
    protected static function listOf(mixed $value): array
    {
        return is_array($value) ? array_values(array_filter($value, 'is_array')) : [];
    }

    // --- staging (production, §6.7, REQ-UI-034) -------------------------------------

    /** POST …?action=staging_start — `POST …/staging`. */
    public function startStaging(): Response
    {
        return $this->stagingCall('staging_start', function (int $projectId): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/staging');
        }, $this->i18n->t('staging.started'));
    }

    /**
     * POST …?action=staging_commit — `POST …/staging/commit`. The dialog's acknowledgement
     * travels as `acknowledge_breaking`; a commit without breaking changes posts directly.
     * A 409 (breaking changes not acknowledged — or the diff changed under the dialog)
     * reopens the dialog with the list refreshed (§6.7).
     */
    public function commitStaging(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect($this->request->path());
        $body = $this->request->field('acknowledge_breaking') === '1' ? ['acknowledge_breaking' => true] : [];
        try {
            $answer = $this->api->post('/api/v1/projects/' . $projectId . '/staging/commit', $body);
        } catch (ApiException $e) {
            $this->logger->info('staging commit rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));
            if ($e->code() === 'conflict') {
                Session::stash('commit_open', ['open' => true]);
            }

            return $back;
        }

        $applied = is_array($answer['applied'] ?? null) ? $answer['applied'] : [];
        $this->flashSuccess($this->i18n->t('staging.committed', [
            'instruments' => (string) (int) ($applied['instruments'] ?? 0),
            'fields' => (string) (int) ($applied['fields'] ?? 0),
            'events' => (string) (int) ($applied['events'] ?? 0),
            'pairs' => (string) (int) ($applied['mapping_pairs'] ?? 0),
        ]));

        return $back;
    }

    /** POST …?action=staging_discard — `POST …/staging/discard` (confirmed first, §3.5). */
    public function discardStaging(): Response
    {
        return $this->stagingCall('staging_discard', function (int $projectId): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/staging/discard');
        }, $this->i18n->t('staging.discarded'));
    }

    private function stagingCall(string $what, callable $call, string $success): Response
    {
        $projectId = $this->projectIdFromPath();
        try {
            $call($projectId);
        } catch (ApiException $e) {
            $this->logger->info($what . ' rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return Response::redirect($this->request->path());
        }
        $this->flashSuccess($success);

        return Response::redirect($this->request->path());
    }

    // --- the reference picker's data region (§7.3, REQ-UI-044) ----------------------

    /**
     * The `references` data region both pages serve: the project's **active** `[event][field]`
     * references — every value-carrying field of an instrument mapped to an event, under that
     * event's unique name (§7.3, `Data_Validation_Design.md` §7.1). Fetched by the expression
     * editor only when the designer opens the picker, so a page render never pays for it;
     * the fields of each mapped instrument are one read each, bounded by the instrument
     * count, never by a table's rows (Plan/Web_Implementation.md §7 rule 13).
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();
        $this->requireDesigner($detail);
        $projectId = $this->projectIdOf($detail);
        $base = '/api/v1/projects/' . $projectId;

        $arms = $this->api->get($base . '/arms');
        $eventNames = [];
        foreach ($arms as $arm) {
            foreach ((is_array($arm) && is_array($arm['events'] ?? null) ? $arm['events'] : []) as $event) {
                if (is_array($event) && isset($event['unique_event_name'])) {
                    $eventNames[(string) $event['unique_event_name']] = (string) ($event['event_name'] ?? '');
                }
            }
        }

        $instruments = [];
        foreach ($this->api->get($base . '/instruments') as $instrument) {
            if (is_array($instrument) && isset($instrument['name'], $instrument['id'])) {
                $instruments[(string) $instrument['name']] = (int) $instrument['id'];
            }
        }

        // instrument name → the events it is mapped to, over every arm.
        $mappedTo = [];
        foreach ($this->api->get($base . '/instrument-event-mapping') as $arm) {
            foreach ((is_array($arm) && is_array($arm['mapping'] ?? null) ? $arm['mapping'] : []) as $name => $events) {
                foreach (is_array($events) ? $events : [] as $unique) {
                    $mappedTo[(string) $name][] = (string) $unique;
                }
            }
        }

        $references = [];
        foreach ($mappedTo as $name => $events) {
            if (!isset($instruments[$name])) {
                continue;
            }
            $fields = $this->api->get($base . '/instruments/' . $instruments[$name] . '/fields');
            foreach ($events as $unique) {
                foreach ($fields as $field) {
                    if (!is_array($field) || in_array($field['field_type'] ?? '', ['description', 'header'], true)) {
                        continue; // no value, nothing to reference (REQ-VAL NON_VALUE_FIELD)
                    }
                    $references[] = [
                        'reference' => '[' . $unique . '][' . (string) ($field['field_name'] ?? '') . ']',
                        'event' => $unique,
                        'event_label' => $eventNames[$unique] ?? '',
                        'instrument' => $name,
                        'field' => (string) ($field['field_name'] ?? ''),
                        'label' => (string) ($field['field_label'] ?? ''),
                    ];
                }
            }
        }

        return Response::json(['references' => $references]);
    }
}
