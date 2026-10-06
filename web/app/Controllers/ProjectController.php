<?php
// The project page (`User_Interface_Design.md` §6.1, REQ-UI-017): a left panel of the
// project's own functions with the selected one in the right-hand panel. This controller
// serves two routes of that shell — the entry point `GET /projects/{id}`, which resolves
// the first available section and answers 303, and Overview, which shows the summary, the
// read-only metadata block and the mode badge.
//
// One read serves a render — `GET /api/v1/projects/{id}` carries the metadata, the
// structure the counts come from, and the acting user's effective permissions
// (REQ-API-126). The permissions block is disclosure only: it decides which panel entries
// exist and which section opens, and every call behind them is re-checked by the API
// (REQ-AUTH-033, Plan/Web_Implementation.md §7 rule 15).
//
// Gating is the API's own — a member with no data access on any arm is refused by the
// read itself (§4.5) and lands on the §3.4 refusal page; PHP adds no visibility rule of
// its own (REQ-API-007).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Navigation;
use Clara\Permissions;
use Clara\Response;
use Clara\Session;

final class ProjectController extends ProjectPageController
{
    /**
     * GET /projects/{id} — entering a project (§6.1, REQ-UI-017). Not a page of its own:
     * the first available section after Overview opens — Setup for a member who may change
     * the setup, Record Status Dashboard for one with data access, Overview for nobody else
     * having a way in. The permission read this needs is the same detail read the entry
     * makes anyway, which is why the project-overview page does not resolve it per row
     * (REQ-API-049 carries no permissions block).
     */
    public function index(): Response
    {
        $detail = $this->projectDetail();

        return Response::redirect(
            Navigation::defaultProjectSection($this->projectIdOf($detail), Permissions::fromProjectDetail($detail)),
            303
        );
    }

    /** GET /projects/{id}/overview — the project's own summary page (§6.1). */
    public function overview(): Response
    {
        $detail = $this->projectDetail();
        $projectId = $this->projectIdOf($detail);
        $state = $this->modeState($projectId);
        $mode = $state['mode'];

        return $this->page('overview', [
            // The browser tab carries the project's own name — data, not a UI string, so it
            // is not translated (the shell falls back to the application name when empty).
            'pageTitle' => $this->projectName($detail),
            'projectId' => $projectId,
            'metadata' => $this->metadata($detail),
            'summary' => $this->summary($detail),
            // The mode badge beside the project name (§6.6, REQ-UI-033); '' omits it.
            'mode' => $mode,
            // The mode card (§6.6, REQ-UI-033): is_admin only — a project's own project_admin
            // sees the badge and nothing else, so for everyone else this is null and the card
            // is not emitted (REQ-UI-003).
            'modeCard' => Session::isAdmin() && $mode !== '' ? [
                'mode' => $mode,
                'stagingOpen' => $state['staging_open'],
                'transitions' => $state['staging_open'] ? [] : self::TRANSITIONS[$mode],
                'setupUrl' => '/projects/' . $projectId . '/setup',
            ] : null,
            // The header names the project while the user is inside it, and the name goes
            // back to this page (§2.4); the breadcrumb carries the same badge (§6.6).
            'brandProject' => $this->projectName($detail),
            'brandProjectUrl' => '/projects/' . $projectId . '/overview',
            'brandProjectMode' => $mode,
            // The project's left panel (§6.1): every entry this build serves and this
            // member may use, in canonical order (REQ-UI-003).
            'nav' => [
                'headingKey' => 'nav.project_sections',
                'items' => Navigation::projectSections($projectId, Permissions::fromProjectDetail($detail)),
            ],
        ], [
            'titleKey' => '',
            'scripts' => ['/assets/app.js', '/assets/js/admin.js'],
            'jsKeys' => ['admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied'],
        ]);
    }

    /**
     * The transitions the mode card offers from each mode (§6.6, API §4.21) — and only those:
     * development → analysis is never offered, analysis is entered from production alone.
     */
    public const TRANSITIONS = [
        'development' => ['production'],
        'production' => ['development', 'analysis'],
        'analysis' => ['production', 'development'],
    ];

    /**
     * POST /projects/{id}/overview?action=set_mode — `PUT …/mode` (is_admin, REQ-UI-033).
     * Leaving development asks whether stored data is kept; deleting it needs the second,
     * explicit confirmation the card carries (§6.6, §3.5), checked here as well so a form
     * posted without it never reaches the API. A change while a staging set is open is the
     * API's 409, shown per §3.4.
     */
    public function setMode(): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/overview');
        $target = $this->request->field('mode');
        if (!in_array($target, self::MODES, true)) {
            $this->flashDanger($this->i18n->t('error.invalid_request'));

            return $back;
        }

        $body = ['mode' => $target];
        $current = $this->projectMode($projectId);
        $deleting = false;
        if ($current === 'development' && $target === 'production') {
            $deleting = $this->request->field('keep_data') === '0';
            if ($deleting && $this->request->field('confirm_delete') !== '1') {
                $this->flashDanger($this->i18n->t('mode.delete_unconfirmed'));

                return $back;
            }
            $body['keep_data'] = !$deleting;
        }

        try {
            $answer = $this->api->put('/api/v1/projects/' . $projectId . '/mode', $body);
        } catch (ApiException $e) {
            $this->logger->info('project mode change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $back;
        }

        $label = $this->i18n->t('project.mode.' . $target);
        $this->flashSuccess($deleting
            ? $this->i18n->t('mode.changed_deleted', ['mode' => $label, 'count' => (string) (int) ($answer['records_deleted'] ?? 0)])
            : $this->i18n->t('mode.changed', ['mode' => $label]));

        return $back;
    }

    /**
     * The `project` data region of this route (REQ-UI-044): the same summary and
     * metadata the rendered Overview shows, and nothing beyond what that page discloses.
     * A data request is answered with the object rather than with the entry's redirect.
     */
    public function data(): Response
    {
        $detail = $this->projectDetail();

        return Response::json([
            'summary' => $this->summary($detail),
            'metadata' => $this->metadata($detail),
        ]);
    }

    /**
     * The three counts of §6.1: the record total the detail carries, and the structure
     * counts derived from that same read — instruments, and their fields summed
     * (API_Endpoints_Design.md §4.5).
     *
     * @param array<string, mixed> $detail
     * @return array{records: int, instruments: int, fields: int}
     */
    private function summary(array $detail): array
    {
        $instruments = is_array($detail['instruments'] ?? null) ? $detail['instruments'] : [];
        $fields = 0;
        foreach ($instruments as $instrument) {
            if (is_array($instrument)) {
                $fields += (int) ($instrument['field_count'] ?? 0);
            }
        }

        return [
            'records' => (int) ($detail['record_count'] ?? 0),
            'instruments' => count($instruments),
            'fields' => $fields,
        ];
    }

    /**
     * The read-only metadata block of §6.1 — editing it belongs to §5.2. Empty values
     * are dropped so the block holds only what the study actually recorded; dates and
     * timestamps are passed through exactly as stored (Plan/Web_Implementation.md §7
     * rule 9), which is also why nothing here formats them.
     *
     * @return array<string, string>
     */
    private function metadata(array $detail): array
    {
        $fields = [
            'project_name', 'organization', 'pi_name', 'pi_email', 'dm_name', 'dm_email',
            'rek_number', 'rek_start_date', 'rek_end_date', 'start_date', 'end_date',
            'participant_names',
        ];

        $out = [];
        foreach ($fields as $field) {
            $value = $detail[$field] ?? null;
            if ($value === null || $value === '') {
                continue;
            }
            $out[$field] = is_scalar($value) ? (string) $value : '';
        }

        return $out;
    }

    // --- Project-scoped administration (§5.3/§5.4/§5.5, REQ-UI-013/014/015) -------------

    /**
     * The data access levels of a role grant (REQ-AUTH-019), in rising order. The ladder ends
     * at `view_edit`: deleting values and editing collected surveys are two independent rights
     * of a pair, never rungs above it (REQ-AUTH-070).
     */
    public const DATA_LEVELS = ['no_access', 'read_only', 'view_edit'];

    /** The export levels of a role grant (REQ-API-075/026), in rising order. */
    public const EXPORT_LEVELS = ['export_none', 'export_de_identified', 'export_no_identifiers', 'export_full'];

    /**
     * Renders one section of the project page: the same shell as Overview — left panel,
     * breadcrumb with the mode badge (§6.1, §6.6) — around this section's template.
     *
     * @param array<string, mixed> $detail
     * @param list<string> $extraScripts this section's own script, after the shared two
     */
    private function projectSection(array $detail, string $template, string $titleKey, array $data,
        array $extraScripts = []): Response
    {
        return $this->renderSection($detail, $template, $titleKey, $data, [
            'scripts' => array_merge(['/assets/app.js', '/assets/js/admin.js'], $extraScripts),
            'jsKeys' => ['admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied'],
        ], $this->projectMode($this->projectIdOf($detail)));
    }

    /** One project-scoped mutation; a refusal is one flashed line (§3.4), then back (PRG). */
    private function mutate(string $section, callable $call, string $successText): Response
    {
        $projectId = $this->projectIdFromPath();
        try {
            $call($projectId);
        } catch (ApiException $e) {
            $this->logger->info('project ' . $section . ' change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return Response::redirect('/projects/' . $projectId . '/' . $section);
        }
        if ($successText !== '') {
            $this->flashSuccess($successText);
        }

        return Response::redirect('/projects/' . $projectId . '/' . $section);
    }

    // Members and tokens (§5.3, is_admin) -----------------------------------------------

    /** GET /projects/{id}/members — the member table, the add form, a just-issued token once. */
    public function members(): Response
    {
        $detail = $this->projectDetail();
        $projectId = $this->projectIdOf($detail);
        $members = $this->api->get('/api/v1/projects/' . $projectId . '/users');
        $members = is_array($members) ? array_values(array_filter($members, 'is_array')) : [];
        $roles = $this->api->get('/api/v1/projects/' . $projectId . '/roles');
        $accounts = $this->api->get('/api/v1/users');

        // The add form offers enabled accounts that are not members yet.
        $memberIds = array_map(static fn (array $m): int => (int) ($m['user_id'] ?? 0), $members);
        $candidates = array_values(array_filter(
            is_array($accounts) ? $accounts : [],
            static fn ($a): bool => is_array($a) && !empty($a['enabled']) && !in_array((int) ($a['id'] ?? 0), $memberIds, true)
        ));

        return $this->projectSection($detail, 'project/members', 'members.title', [
            'members' => $members,
            'roles' => array_values(array_filter(is_array($roles) ? $roles : [], 'is_array')),
            'candidates' => $candidates,
            'reveal' => Session::takeReveal(),
        ]);
    }

    public function addMember(): Response
    {
        $uid = $this->fieldId('user_id');
        $role = $this->request->field('role');

        return $this->memberWrite($uid, ['role' => $role === '' ? null : $role], 'members.added');
    }

    public function changeRole(): Response
    {
        $role = $this->request->field('role');

        return $this->memberWrite($this->fieldId('user_id'), ['role' => $role === '' ? null : $role], 'members.role_changed');
    }

    public function rotateToken(): Response
    {
        return $this->memberWrite($this->fieldId('user_id'), ['rotate_token' => true], 'members.rotated');
    }

    public function removeMember(): Response
    {
        return $this->memberWrite($this->fieldId('user_id'), ['remove' => true], 'members.removed');
    }

    /**
     * `PUT …/users/{uid}` with exactly one of role / rotate_token / remove (§4.6). A response
     * that carries a token (add 201, rotate 200) is held for one render — shown once with a
     * copy button and the "will not be shown again" warning (§3.5, REQ-UI-013).
     */
    private function memberWrite(int $uid, array $body, string $successKey): Response
    {
        $projectId = $this->projectIdFromPath();
        $back = Response::redirect('/projects/' . $projectId . '/members');
        try {
            $answer = $this->api->put('/api/v1/projects/' . $projectId . '/users/' . $uid, $body);
        } catch (ApiException $e) {
            $this->logger->info('project members change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $back;
        }

        // The add form names the account only by id; the member object the API returns
        // carries the address the confirmation line names (§3.4).
        $email = is_array($answer) && is_string($answer['email'] ?? null) ? $answer['email'] : trim($this->request->field('email'));
        $token = is_array($answer) && is_string($answer['token'] ?? null) ? $answer['token'] : '';
        if ($token !== '') {
            Session::stashReveal(['token' => $token, 'email' => $email]);
        }
        $this->flashSuccess($this->i18n->t($successKey, ['email' => $email]));

        return $back;
    }

    // Role editor (§5.4, is_admin) ------------------------------------------------------

    public function roles(): Response
    {
        $detail = $this->projectDetail();
        $projectId = $this->projectIdOf($detail);
        $roles = array_values(array_filter(
            $this->api->get('/api/v1/projects/' . $projectId . '/roles'), 'is_array'));

        // One role at a time is on the form; the list links each role into it (REQ-UI-014).
        $editing = null;
        foreach ($roles as $role) {
            if ((int) ($role['id'] ?? 0) === (int) $this->request->query('role')) {
                $editing = $role;
                break;
            }
        }

        return $this->projectSection($detail, 'project/roles', 'roles.title', [
            'roles' => $roles,
            'arms' => array_values(array_filter(is_array($detail['arms'] ?? null) ? $detail['arms'] : [], 'is_array')),
            // The matrix's rows: every mapped (instrument, event) pair of the arm.
            'pairs' => self::pairRows($detail, $this->instrumentEventMap($projectId)),
            'editing' => $editing,
            'dataLevels' => self::DATA_LEVELS,
            'exportLevels' => self::EXPORT_LEVELS,
        ], ['/assets/js/roles.js']);
    }

    /**
     * `GET …/instrument-event-mapping` as arm_num → instrument name → mapped event names.
     *
     * @return array<int, array<string, list<string>>>
     */
    private function instrumentEventMap(int $projectId): array
    {
        $out = [];
        foreach ($this->api->get('/api/v1/projects/' . $projectId . '/instrument-event-mapping') as $arm) {
            if (!is_array($arm)) {
                continue;
            }
            $byInstrument = [];
            foreach ((is_array($arm['mapping'] ?? null) ? $arm['mapping'] : []) as $name => $events) {
                $byInstrument[(string) $name] = is_array($events) ? array_values(array_map('strval', $events)) : [];
            }
            $out[(int) ($arm['arm_num'] ?? 0)] = $byInstrument;
        }

        return $out;
    }

    /**
     * The matrix rows per arm: the project's instruments in position order, each with the
     * events it is mapped to in the canonical order of GD-15 — the design order the master
     * spec's "Role adjustment" picture shows (REQ-UI-014). A pair exists only when both of
     * its objects do, so nothing here has to guess at a removed instrument.
     *
     * @param array<string, mixed>                    $detail the project detail (arms, instruments)
     * @param array<int, array<string, list<string>>> $mapping
     * @return array<int, list<array{event: string, instrument: string}>>
     */
    private static function pairRows(array $detail, array $mapping): array
    {
        $instruments = [];
        foreach ((is_array($detail['instruments'] ?? null) ? $detail['instruments'] : []) as $instrument) {
            if (is_array($instrument) && ($instrument['name'] ?? '') !== '') {
                $instruments[] = [(string) $instrument['name'], (int) ($instrument['position'] ?? 0)];
            }
        }
        usort($instruments, static fn (array $a, array $b): int => [$a[1], $a[0]] <=> [$b[1], $b[0]]);

        $rows = [];
        foreach ((is_array($detail['arms'] ?? null) ? $detail['arms'] : []) as $arm) {
            if (!is_array($arm)) {
                continue;
            }
            $armNum = (int) ($arm['arm_num'] ?? 0);
            $events = [];
            foreach ((is_array($arm['events'] ?? null) ? $arm['events'] : []) as $event) {
                if (is_array($event) && ($event['unique_event_name'] ?? '') !== '') {
                    $events[] = (string) $event['unique_event_name'];
                }
            }
            foreach ($instruments as [$name, $_]) {
                foreach ($events as $event) {
                    if (in_array($event, $mapping[$armNum][$name] ?? [], true)) {
                        $rows[$armNum][] = ['event' => $event, 'instrument' => $name];
                    }
                }
            }
        }

        return $rows;
    }

    /**
     * Submits the editor: `POST …/roles` for a new role, `PUT …/roles/{rid}` for the one on the
     * form (REQ-API-057/143). The body is the §4.7 role object — arm defaults plus one grant
     * per row of the matrix; a pair no row names reverts to its arm default (REQ-API-143).
     */
    public function saveRole(): Response
    {
        $projectId = $this->projectIdFromPath();
        $name = trim($this->request->field('name'));
        $roleId = (int) $this->request->field('role_id');

        // Every row posts its pair key, so a checkbox left out reads as unset rather than as a
        // missing value; the key carries arm, instrument and event in one flat map entry.
        $data = $this->request->fieldMap('data');
        $export = $this->request->fieldMap('export');
        $deleteValues = $this->request->fieldMap('delete_values');
        $editSurveys = $this->request->fieldMap('edit_surveys');
        $armData = $this->request->fieldMap('arm_data');
        $armExport = $this->request->fieldMap('arm_export');

        $arms = [];
        foreach ($armData as $armNum => $level) {
            if (preg_match('/^\d{1,4}$/', (string) $armNum) !== 1) {
                continue;
            }
            $arms[(string) $armNum] = [
                'data' => in_array($level, self::DATA_LEVELS, true) ? $level : 'no_access',
                'export' => in_array($armExport[$armNum] ?? '', self::EXPORT_LEVELS, true)
                    ? $armExport[$armNum] : 'export_none',
            ];
        }

        $grants = [];
        foreach ($this->request->fieldList('pair') as $key) {
            $parts = explode('|', $key);
            if (count($parts) !== 3) {
                continue;
            }
            [$armNum, $instrument, $event] = $parts;
            if ($instrument === '' || $event === '') {
                continue;
            }
            $grants[] = [
                'event' => $event,
                'instrument' => $instrument,
                'data' => in_array($data[$key] ?? '', self::DATA_LEVELS, true) ? $data[$key] : 'no_access',
                'export' => in_array($export[$key] ?? '', self::EXPORT_LEVELS, true)
                    ? $export[$key] : 'export_none',
                'delete_values' => ($deleteValues[$key] ?? '') === '1',
                'edit_surveys' => ($editSurveys[$key] ?? '') === '1',
            ];
        }

        $body = [
            'name' => $name,
            'project_admin' => $this->request->field('project_admin') === '1',
            'arms' => (object) $arms,
            'grants' => $grants,
        ];

        try {
            if ($roleId > 0) {
                $this->api->put('/api/v1/projects/' . $projectId . '/roles/' . $roleId, $body);
            } else {
                $this->api->post('/api/v1/projects/' . $projectId . '/roles', $body);
            }
        } catch (ApiException $e) {
            $this->logger->info('role change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return Response::redirect('/projects/' . $projectId . '/roles'
                . ($roleId > 0 ? '?role=' . $roleId : ''));
        }

        $this->flashSuccess($this->i18n->t($roleId > 0 ? 'roles.saved' : 'roles.created', ['name' => $name]));

        return Response::redirect('/projects/' . $projectId . '/roles');
    }

    // Data access groups (§5.5: read ≥ read_only, mutate project_admin) ------------------

    public function groups(): Response
    {
        $detail = $this->projectDetail();
        $projectId = $this->projectIdOf($detail);
        $groups = $this->api->get('/api/v1/projects/' . $projectId . '/data-access-groups');

        return $this->projectSection($detail, 'project/groups', 'groups.title', [
            'groups' => array_values(array_filter(is_array($groups) ? $groups : [], 'is_array')),
            // Hidden means not emitted (REQ-UI-003): only a project_admin (or is_admin) may
            // create or delete; the API re-checks either way.
            'canManage' => Permissions::fromProjectDetail($detail)->projectAdmin || Session::isAdmin(),
        ]);
    }

    public function createGroup(): Response
    {
        $name = trim($this->request->field('name'));

        return $this->mutate('groups', function (int $projectId) use ($name): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/data-access-groups', ['name' => $name]);
        }, $this->i18n->t('groups.created', ['name' => $name]));
    }

    public function deleteGroup(): Response
    {
        $gid = $this->fieldId('group_id');
        $name = trim($this->request->field('name'));

        return $this->mutate('groups', function (int $projectId) use ($gid): void {
            $this->api->delete('/api/v1/projects/' . $projectId . '/data-access-groups/' . $gid);
        }, $this->i18n->t('groups.deleted', ['name' => $name]));
    }
}
