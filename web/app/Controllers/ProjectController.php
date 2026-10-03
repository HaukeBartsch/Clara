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

final class ProjectController extends Controller
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
        $mode = $this->projectMode($projectId);

        return $this->page('overview', [
            // The browser tab carries the project's own name — data, not a UI string, so it
            // is not translated (the shell falls back to the application name when empty).
            'pageTitle' => $this->projectName($detail),
            'metadata' => $this->metadata($detail),
            'summary' => $this->summary($detail),
            // The mode badge beside the project name (§6.6, REQ-UI-033); '' omits it.
            'mode' => $mode,
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
            'scripts' => ['/assets/app.js'],
        ]);
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

    // --- the read, once -------------------------------------------------------

    /**
     * The detail object of §4.5 for this route's project id. A non-numeric id names no
     * project at all, and answering 404 here is what keeps the page from sending it to
     * the API as a path segment (the API would read it as an unknown id anyway).
     *
     * @return array<string, mixed>
     */
    private function projectDetail(): array
    {
        $raw = $this->request->pathParam('id');
        if (preg_match('/^\d{1,18}$/', $raw) !== 1) {
            throw new ApiException('not_found', '', 404);
        }

        $detail = $this->api->get('/api/v1/projects/' . (int) $raw);

        return is_array($detail) ? $detail : [];
    }

    /**
     * The project's mode for the badge of §6.6 (REQ-UI-033): `GET …/mode`, readable by
     * every member. The badge is information beside the name, not the page's content, so
     * a refused or failed read — or a value outside the three modes of GD-20 — omits it
     * ('') instead of costing the user the Overview.
     */
    private function projectMode(int $projectId): string
    {
        try {
            $read = $this->api->get('/api/v1/projects/' . $projectId . '/mode');
        } catch (ApiException $e) {
            $this->logger->info('project mode read failed', ['code' => $e->code(), 'status' => $e->status()]);

            return '';
        }
        $mode = is_array($read) ? (string) ($read['mode'] ?? '') : '';

        return in_array($mode, ['development', 'production', 'analysis'], true) ? $mode : '';
    }

    /**
     * The id to build the project's own routes from: the detail object's, falling back to
     * the path parameter when a stubbed or partial read carries none.
     *
     * @param array<string, mixed> $detail
     */
    private function projectIdOf(array $detail): int
    {
        return (int) ($detail['id'] ?? $this->request->pathParam('id'));
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

    /** @param array<string, mixed> $detail */
    private function projectName(array $detail): string
    {
        return (string) ($detail['project_name'] ?? '');
    }

    // --- Project-scoped administration (§5.3/§5.4/§5.5, REQ-UI-013/014/015) -------------

    /** The data access levels of a role arm (REQ-AUTH-019), in rising order. */
    public const DATA_LEVELS = ['no_access', 'read_only', 'view_edit', 'delete', 'edit_survey_responses'];

    /** The export levels of a role arm (REQ-API-075), in rising order. */
    public const EXPORT_LEVELS = ['export_none', 'export_de_identified', 'export_no_identifiers', 'export_full'];

    /**
     * Renders one section of the project page: the same shell as Overview — left panel,
     * breadcrumb with the mode badge (§6.1, §6.6) — around this section's template.
     *
     * @param array<string, mixed> $detail
     */
    private function projectSection(array $detail, string $template, string $titleKey, array $data): Response
    {
        $projectId = $this->projectIdOf($detail);

        return $this->page($template, $data + [
            'pageTitle' => $this->i18n->t($titleKey) . ' · ' . $this->projectName($detail),
            'projectId' => $projectId,
            'projectName' => $this->projectName($detail),
            'brandProject' => $this->projectName($detail),
            'brandProjectUrl' => '/projects/' . $projectId . '/overview',
            'brandProjectMode' => $this->projectMode($projectId),
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

    private function projectIdFromPath(): int
    {
        $raw = $this->request->pathParam('id');
        if (preg_match('/^\d{1,18}$/', $raw) !== 1) {
            throw new ApiException('not_found', '', 404);
        }

        return (int) $raw;
    }

    private function fieldId(string $name): int
    {
        $raw = $this->request->field($name);

        return preg_match('/^\d{1,18}$/', $raw) === 1 ? (int) $raw : 0;
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
        $roles = $this->api->get('/api/v1/projects/' . $projectId . '/roles');

        return $this->projectSection($detail, 'project/roles', 'roles.title', [
            'roles' => array_values(array_filter(is_array($roles) ? $roles : [], 'is_array')),
            'arms' => array_values(array_filter(is_array($detail['arms'] ?? null) ? $detail['arms'] : [], 'is_array')),
            'dataLevels' => self::DATA_LEVELS,
            'exportLevels' => self::EXPORT_LEVELS,
        ]);
    }

    public function createRole(): Response
    {
        $name = trim($this->request->field('name'));
        $data = $this->request->fieldMap('data');
        $export = $this->request->fieldMap('export');

        // One block per arm; an arm left at its defaults is sent as no_access/export_none so
        // nothing is granted implicitly (REQ-AUTH-019).
        $arms = [];
        foreach (array_unique(array_merge(array_keys($data), array_keys($export))) as $arm) {
            if (preg_match('/^\d{1,4}$/', (string) $arm) !== 1) {
                continue;
            }
            $arms[(string) $arm] = [
                'data' => in_array($data[$arm] ?? '', self::DATA_LEVELS, true) ? $data[$arm] : 'no_access',
                'export' => in_array($export[$arm] ?? '', self::EXPORT_LEVELS, true) ? $export[$arm] : 'export_none',
            ];
        }

        return $this->mutate('roles', function (int $projectId) use ($name, $arms): void {
            $this->api->post('/api/v1/projects/' . $projectId . '/roles', [
                'name' => $name,
                'project_admin' => $this->request->field('project_admin') === '1',
                'arms' => (object) $arms,
            ]);
        }, $this->i18n->t('roles.created', ['name' => $name]));
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
