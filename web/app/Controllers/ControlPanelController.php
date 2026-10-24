<?php
// The Control Panel (User_Interface_Design.md §5, REQ-UI-047): the one is_admin page that
// carries every installation-administration function. Its left panel lists the sections
// from Clara\Navigation,
// `?section=` picks the one in the right-hand panel (Projects when absent), and every
// mutation is a POST to /admin with its ?action= (§2.1, REQ-UI-005; the router has already
// checked the CSRF token).
//
// Each write sends exactly the attributes its endpoint whitelists — the administration API
// answers 400 to anything else (Plan/Web_Implementation.md §7 rule 1) — and every outcome is
// one translated line (§3.4): success after a redirect (PRG), failure either as a re-rendered
// form that keeps what was typed (create forms) or as a flashed line (row actions).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\ConfigCheck;
use Clara\LdapRace;
use Clara\MonitoringStressTest;
use Clara\Navigation;
use Clara\Response;
use Clara\Session;
use Clara\StreamSink;

final class ControlPanelController extends Controller
{
    /**
     * The main supporting institutions of REQ-DB-006 (GD-17) — the organization select: the
     * stored code => its display text (the hospitals of Helse Sør-Øst with their sites). OTHER's
     * text is translated (`admin.projects.organization.other`), hence empty here.
     */
    public const ORGANIZATIONS = [
        'OUS' => 'Oslo University Hospital, Rikshospitalet, Ullevål, Radiumhospitalet, Aker',
        'Ahus' => 'Akershus University Hospital, Nordbyhagen (Lørenskog), Kongsvinger',
        'SØ' => 'Sykehuset Østfold, Kalnes (Sarpsborg), Moss',
        'SiV' => 'Sykehuset i Vestfold, Tønsberg, Larvik',
        'ST' => 'Sykehuset Telemark, Skien, Notodden, Porsgrunn, Rjukan',
        'SSHF' => 'Sørlandet Sykehus, Kristiansand, Arendal, Flekkefjord',
        'SI' => 'Sykehuset Innlandet, Hamar, Elverum, Lillehammer, Gjøvik, Kongsvinger (psychiatry)',
        'VVHF' => 'Vestre Viken, Drammen, Bærum, Ringerike (Hønefoss), Kongsberg',
        'SUN' => 'Sunnaas Sykehus, Nesodden (Specialized rehabilitation hospital)',
        'NAT EU' => 'NAT EU',
        'OTHER' => '',
    ];

    /** The optional project attributes (§5.2): empty on edit means "clear" (null). */
    private const PROJECT_OPTIONAL = [
        'dm_name', 'dm_email', 'rek_number', 'rek_start_date', 'rek_end_date', 'start_date', 'end_date',
    ];

    /** The required project attributes (§5.2, REQ-DB-006). */
    private const PROJECT_REQUIRED = ['project_name', 'organization', 'pi_name', 'pi_email', 'participant_names'];

    /**
     * The event codes of Audit_Logging_Design.md §3 — the audit filter's select (§5.6) — in
     * alphabetical order, exactly the API's catalog (api/internal/audit/audit.go; a test keeps the
     * two in step). Sources (`api`/`ui`/`system`) are not event types and are not listed.
     */
    public const AUDIT_EVENT_TYPES = [
        'account_auto_disabled', 'admin_rejected', 'arm_created', 'arm_deleted', 'arm_reordered', 'arm_updated',
        'audit_export', 'calculated_recomputed', 'dag_active_switched', 'dag_created', 'dag_deleted',
        'dag_membership_changed', 'dag_record_assigned', 'event_created', 'event_deleted', 'event_reordered',
        'event_updated', 'export', 'field_created', 'field_deleted', 'field_moved', 'field_reordered',
        'field_updated', 'i18n_updated', 'instrument_completed', 'instrument_created', 'instrument_deleted',
        'instrument_reordered', 'instrument_uncompleted', 'instrument_updated', 'invite_accepted', 'login_failure',
        'login_success', 'logout', 'mapping_updated', 'membership_changed', 'password_changed',
        'password_reset_completed', 'password_reset_requested', 'project_created', 'project_ended',
        'project_mode_changed', 'project_updated', 'record_created', 'record_deleted', 'record_read', 'record_updated',
        'role_created', 'role_updated', 'session_replay_detected', 'settings_updated', 'staging_committed',
        'staging_discarded', 'staging_started', 'survey_link_issued', 'survey_link_revoked', 'survey_submitted',
        'tfa_disabled', 'tfa_enrolled', 'tfa_reset', 'token_issued', 'token_revoked', 'token_rotated',
        'token_viewed', 'transfer_executed', 'user_created', 'user_invited', 'user_updated',
    ];

    /** Audit rows per page (§3.6). */
    private const AUDIT_PAGE = 50;

    public function index(): Response
    {
        return $this->renderSection(Navigation::resolveAdminSection($this->request->query('section')));
    }

    private function renderSection(array $section): Response
    {
        return match ($section['key'] ?? '') {
            'users' => $this->usersPage(),
            'projects' => $this->projectsPage(),
            'audits' => $this->auditsPage(),
            'translations' => $this->translationsPage(),
            'settings' => $this->settingsPage(''),
            'monitoring' => $this->monitoringPage(),
            'assistant' => $this->assistantPage(),
            'check' => $this->configCheckPage(),
            'auth' => $this->authTestPage(Session::takeAuthTestReport()),
            'api' => $this->apiDocsPage(),
            default => $this->page('admin/none', [
                'pageTitle' => $this->i18n->t('admin.title'),
            ], ['titleKey' => 'admin.title']),
        };
    }

    /** Tests one configured LDAP source without calling the login API. */
    public function testLdap(): Response
    {
        $index = filter_var($this->request->field('server'), FILTER_VALIDATE_INT);
        $login = trim($this->request->field('username'));
        $password = $this->request->field('password');
        $server = null;
        foreach ($this->config->ldapServers as $candidate) {
            if ($candidate['index'] === $index) {
                $server = $candidate;
                break;
            }
        }
        if ($server === null || $login === '' || $password === '' || strlen($login) > 256 || strlen($password) > 1024) {
            return $this->authTestPage([], $this->i18n->t('admin.auth.invalid'));
        }

        $result = (new LdapRace($this->config, $this->logger))->testServer($server, $login, $password);
        unset($password, $result['email'], $result['display_name']);
        Session::setAuthTestReport(['kind' => 'ldap', 'server' => (int) $index, 'result' => $result]);

        return Response::redirect('/admin?section=auth');
    }

    /** Starts an isolated OAuth/OIDC test flow for one configured provider. */
    public function testOauth(): Response
    {
        $index = filter_var($this->request->field('provider'), FILTER_VALIDATE_INT);
        foreach ($this->config->oauthProviders as $provider) {
            if ($provider['index'] === $index) {
                return Response::redirect($this->auth->beginOauthTestRedirect($provider));
            }
        }

        return $this->authTestPage([], $this->i18n->t('admin.auth.invalid'));
    }

    /** Renders non-secret source settings and a one-time diagnostic report. */
    private function authTestPage(array $report = [], string $error = ''): Response
    {
        $providers = array_map(fn (array $provider): array => [
            'index' => $provider['index'],
            'issuer' => $provider['issuer'],
            'client_id' => $provider['client_id'],
            'redirect_uri' => $provider['redirect_uri'] !== ''
                ? $provider['redirect_uri']
                : $this->config->webPublicUrl . '/auth/callback',
            'email_attr' => $provider['email_attr'],
            'names' => $provider['names'],
        ], $this->config->oauthProviders);
        $servers = array_map(static fn (array $server): array => [
            'index' => $server['index'],
            'url' => $server['url'],
            'bind_dn' => $server['bind_dn'],
            'search_base' => $server['search_base'],
            'uid_attr' => $server['uid_attr'],
            'email_attr' => $server['email_attr'],
            'name_attr' => $server['name_attr'],
            'names' => $server['names'],
        ], $this->config->ldapServers);

        return $this->section('auth', 'admin/auth_test', 'admin.auth.title', [
            'providers' => $providers,
            'servers' => $servers,
            'report' => $report,
            'error' => $error,
        ]);
    }

    /**
     * Renders one section inside the Control Panel shell: the left panel marks it active
     * (the path alone cannot, §2.1), and the shared admin module wires confirmations and
     * copy buttons (§3.5). `$extras` adds a section's own scripts, stylesheets and
     * JS-string keys — only the sections that use them pay for them (REQ-UI-045).
     *
     * @param array{scripts?: list<string>, styles?: list<string>, jsKeys?: list<string>} $extras
     */
    private function section(string $key, string $template, string $titleKey, array $data, array $extras = []): Response
    {
        return $this->page($template, $data + [
            'pageTitle' => $this->i18n->t($titleKey),
            'nav' => [
                'headingKey' => 'admin.title',
                'items' => array_map(
                    static fn (array $section): array => [
                        'path' => '/admin?section=' . $section['key'],
                        'labelKey' => $section['labelKey'],
                        'active' => $section['key'] === $key,
                    ],
                    Navigation::availableAdminSections()
                ),
            ],
        ], [
            'titleKey' => '',
            'scripts' => array_merge(['/assets/app.js', '/assets/js/admin.js'], $extras['scripts'] ?? []),
            'styles' => $extras['styles'] ?? [],
            'jsKeys' => array_merge(
                ['admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied'],
                $extras['jsKeys'] ?? []
            ),
        ]);
    }

    private function toSection(string $key, string $query = ''): Response
    {
        return Response::redirect('/admin?section=' . $key . $query);
    }

    // --- Users (§5.1, REQ-UI-011) -----------------------------------------------

    /** @param array<string, string> $form the create form's sticky values (never a password) */
    private function usersPage(string $error = '', array $form = []): Response
    {
        $users = $this->api->get('/api/v1/users');

        return $this->section('users', 'admin/users', 'admin.users.title', [
            'users' => is_array($users) ? array_values(array_filter($users, 'is_array')) : [],
            'error' => $error,
            'form' => $form,
            'selfId' => Session::userId(),
        ]);
    }

    public function createUser(): Response
    {
        $form = [
            'email' => trim($this->request->field('email')),
            'display_name' => trim($this->request->field('display_name')),
            'valid_days' => trim($this->request->field('valid_days', '0')),
        ];
        $password = $this->request->field('password');

        if ($form['email'] === '' || $form['display_name'] === '') {
            return $this->usersPage($this->i18n->t('admin.users.required'), $form);
        }
        if (preg_match('/^\d{1,5}$/', $form['valid_days']) !== 1) {
            return $this->usersPage($this->i18n->t('admin.users.valid_days_invalid'), $form);
        }
        if ($password !== $this->request->field('repeat_password')) {
            return $this->usersPage($this->i18n->t('account.password.failure.mismatch'), $form);
        }

        $body = [
            'email' => $form['email'],
            'display_name' => $form['display_name'],
            'valid_days' => (int) $form['valid_days'],
        ];
        // No password = the account is created on the invite path (GD-22).
        if ($password !== '') {
            $body['password'] = $password;
        }

        try {
            $this->api->post('/api/v1/users', $body);
        } catch (ApiException $e) {
            $this->logger->info('user create rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->usersPage($this->apiFailureMessage($e), $form);
        }

        $this->flashSuccess($this->i18n->t(
            $password === '' ? 'admin.users.created_invite' : 'admin.users.created',
            ['email' => $form['email']]
        ));

        return $this->toSection('users');
    }

    public function setUserEnabled(): Response
    {
        $enabled = $this->request->field('enabled') === '1';

        return $this->userUpdate(['enabled' => $enabled], $enabled ? 'admin.users.enabled' : 'admin.users.disabled');
    }

    public function setUserValidity(): Response
    {
        $days = trim($this->request->field('valid_days'));
        if (preg_match('/^\d{1,5}$/', $days) !== 1) {
            $this->flashDanger($this->i18n->t('admin.users.valid_days_invalid'));

            return $this->toSection('users');
        }

        return $this->userUpdate(['valid_days' => (int) $days], 'admin.users.validity_set');
    }

    public function setUserPassword(): Response
    {
        $password = $this->request->field('password');
        if ($password !== $this->request->field('repeat_password')) {
            $this->flashDanger($this->i18n->t('account.password.failure.mismatch'));

            return $this->toSection('users');
        }

        // An empty password clears the local credential (§5.1).
        return $this->userUpdate(['password' => $password],
            $password === '' ? 'admin.users.password_cleared' : 'admin.users.password_set');
    }

    public function setUserAdmin(): Response
    {
        $makeAdmin = $this->request->field('is_admin') === '1';
        $id = $this->targetUserId();
        try {
            $this->api->put('/api/v1/users/' . $id, ['is_admin' => $makeAdmin]);
        } catch (ApiException $e) {
            // 409: the last enabled administrator stays (REQ-AUTH-068) — the API's reason.
            $this->logger->info('admin flag change rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));

            return $this->toSection('users');
        }
        $this->flashSuccess($this->i18n->t(
            $makeAdmin ? 'admin.users.admin_granted' : 'admin.users.admin_revoked',
            ['email' => $this->targetEmail()]
        ));

        // A self-revoke leaves the cached flag stale while every API call already refuses
        // (§5.1): follow it, and leave the Control Panel this account may no longer open.
        if (!$makeAdmin && $id === Session::userId()) {
            Session::setAdmin(false);

            return Response::redirect('/');
        }

        return $this->toSection('users');
    }

    public function inviteUser(): Response
    {
        $id = $this->targetUserId();
        try {
            $this->api->post('/api/v1/users/' . $id . '/invite');
        } catch (ApiException $e) {
            // No SMTP relay: the direct password is then the only path (§5.1, REQ-CFG-028).
            $this->flashDanger($e->code() === 'smtp_not_configured'
                ? $this->i18n->t('admin.users.smtp_not_configured')
                : $this->apiFailureMessage($e));

            return $this->toSection('users');
        }
        $this->flashSuccess($this->i18n->t('admin.users.invited', ['email' => $this->targetEmail()]));

        return $this->toSection('users');
    }

    public function resetUserTfa(): Response
    {
        $id = $this->targetUserId();
        try {
            $answer = $this->api->post('/api/v1/users/' . $id . '/tfa/reset');
        } catch (ApiException $e) {
            $this->flashDanger($this->apiFailureMessage($e));

            return $this->toSection('users');
        }
        $this->keepOwnSession($id, $answer);
        $this->flashSuccess($this->i18n->t('admin.users.tfa_reset', ['email' => $this->targetEmail()]));

        return $this->toSection('users');
    }

    /** One `PUT /api/v1/users/{id}` row action with exactly one attribute (§7 rule 1). */
    private function userUpdate(array $body, string $successKey): Response
    {
        $id = $this->targetUserId();
        try {
            $answer = $this->api->put('/api/v1/users/' . $id, $body);
        } catch (ApiException $e) {
            $this->logger->info('user update rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));

            return $this->toSection('users');
        }
        $this->keepOwnSession($id, $answer);
        $this->flashSuccess($this->i18n->t($successKey, ['email' => $this->targetEmail()]));

        return $this->toSection('users');
    }

    /**
     * A password write or a two-factor reset ends the target account's sessions
     * (REQ-AUTH-074). When the administrator acted on their own row, the session doing it
     * adopts the raised epoch the API answered rather than ending with the others — unless
     * the change disabled the account, which is meant to end this session too.
     *
     * @param array<mixed> $answer
     */
    private function keepOwnSession(int $id, array $answer): void
    {
        if ($id === Session::userId() && is_int($answer['session_epoch'] ?? null)
            && $answer['session_epoch'] !== Session::epoch() && ($answer['enabled'] ?? true) !== false) {
            Session::adoptEpoch($answer['session_epoch']);
        }
    }

    private function targetUserId(): int
    {
        $raw = $this->request->field('id');

        return preg_match('/^\d{1,18}$/', $raw) === 1 ? (int) $raw : 0;
    }

    /** The account the row names — display only, for the confirmation line (§3.4). */
    private function targetEmail(): string
    {
        return trim($this->request->field('email'));
    }

    // --- Projects (§5.2, REQ-UI-012) --------------------------------------------

    /** @param array<string, string> $form sticky create/edit values */
    private function projectsPage(string $error = '', array $form = [], int $editId = 0): Response
    {
        $editId = $editId !== 0 ? $editId : $this->idParam($this->request->query('edit'));
        if ($editId !== 0 && $form === []) {
            $detail = $this->api->get('/api/v1/projects/' . $editId);
            foreach (array_merge(self::PROJECT_REQUIRED, self::PROJECT_OPTIONAL) as $attr) {
                $form[$attr] = is_scalar($detail[$attr] ?? null) ? (string) $detail[$attr] : '';
            }
        }

        return $this->section('projects', 'admin/projects', 'admin.projects.title', [
            'projects' => $this->visibleProjects(),
            'organizations' => self::ORGANIZATIONS,
            'error' => $error,
            'form' => $form,
            'editId' => $editId,
            'createdId' => $this->idParam($this->request->query('created')),
        ]);
    }

    public function createProject(): Response
    {
        $form = $this->projectForm();
        $body = [];
        foreach (self::PROJECT_REQUIRED as $attr) {
            $body[$attr] = $form[$attr];
        }
        // On create an empty optional field is simply not sent.
        foreach (self::PROJECT_OPTIONAL as $attr) {
            if ($form[$attr] !== '') {
                $body[$attr] = $form[$attr];
            }
        }

        try {
            $created = $this->api->post('/api/v1/projects', $body);
        } catch (ApiException $e) {
            $this->logger->info('project create rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->projectsPage($this->apiFailureMessage($e), $form);
        }

        $this->flashSuccess($this->i18n->t('admin.projects.created', ['name' => $form['project_name']]));

        return $this->toSection('projects', '&created=' . (int) ($created['id'] ?? 0));
    }

    public function updateProject(): Response
    {
        $id = $this->idParam($this->request->field('id'));
        $form = $this->projectForm();
        $body = [];
        foreach (self::PROJECT_REQUIRED as $attr) {
            $body[$attr] = $form[$attr];
        }
        // On edit an emptied optional field clears it (REQ-API-042: any subset may change).
        foreach (self::PROJECT_OPTIONAL as $attr) {
            $body[$attr] = $form[$attr] === '' ? null : $form[$attr];
        }

        try {
            $this->api->put('/api/v1/projects/' . $id, $body);
        } catch (ApiException $e) {
            $this->logger->info('project update rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->projectsPage($this->apiFailureMessage($e), $form, $id);
        }

        $this->flashSuccess($this->i18n->t('admin.projects.updated', ['name' => $form['project_name']]));

        return $this->toSection('projects');
    }

    /** @return array<string, string> */
    private function projectForm(): array
    {
        $form = [];
        foreach (array_merge(self::PROJECT_REQUIRED, self::PROJECT_OPTIONAL) as $attr) {
            $form[$attr] = trim($this->request->field($attr));
        }
        // The pattern is not shown on the form: admin.js derives it from the name, an edit
        // carries the stored one, and without JavaScript the same derivation happens here.
        if ($form['participant_names'] === '') {
            $form['participant_names'] = self::derivedPattern($form['project_name']);
        }

        return $form;
    }

    /** The project-name-derived pattern (admin.js derivedPattern): upper case, spaces to underscores, three-digit counter. */
    public static function derivedPattern(string $name): string
    {
        $base = str_replace(' ', '_', mb_strtoupper(trim($name)));

        return $base === '' ? '' : $base . '_[0-9][0-9][0-9]';
    }

    private function idParam(string $raw): int
    {
        return preg_match('/^\d{1,18}$/', $raw) === 1 ? (int) $raw : 0;
    }

    // --- Audits (§5.6, REQ-UI-016) ----------------------------------------------

    private function auditsPage(): Response
    {
        // The filters ride in the query string so a page is reloadable (§3.6). Each value is
        // checked here before it becomes an API query parameter (§2.1: allowlisted, never
        // forwarded as typed).
        $filters = [
            'type' => in_array($this->request->query('type'), ['events', 'views'], true) ? $this->request->query('type') : 'events',
            'project' => (string) $this->idParam($this->request->query('project')),
            'user' => (string) $this->idParam($this->request->query('user')),
            'event_type' => in_array($this->request->query('event_type'), self::AUDIT_EVENT_TYPES, true)
                ? $this->request->query('event_type') : '',
            'from' => $this->dateParam($this->request->query('from')),
            'to' => $this->dateParam($this->request->query('to')),
        ];
        // The Export control (REQ-UI-058) rides the same filter form: `download=1` streams
        // the ZIP, `estimate=1` prices it for the > 1 GiB warning (REQ-AUD-032). Both reuse
        // the allowlisted filters — nothing from the browser reaches the API unchecked.
        $filtered = $this->auditFilterQuery($filters);
        if ($this->request->query('download') === '1') {
            return $this->auditExportStream($filtered);
        }
        if ($this->request->query('estimate') === '1') {
            return $this->auditExportEstimate($filtered);
        }

        $cursor = $this->request->query('cursor');
        $cursor = preg_match('/^[A-Za-z0-9_=-]{1,512}$/', $cursor) === 1 ? $cursor : '';

        $query = $filtered;
        $query['limit'] = (string) self::AUDIT_PAGE;
        if ($cursor !== '') {
            $query['cursor'] = $cursor;
        }

        $error = '';
        $page = ['entries' => [], 'next_cursor' => null];
        try {
            $read = $this->api->get('/api/v1/audit', $query);
            $page = is_array($read) ? $read + $page : $page;
        } catch (ApiException $e) {
            if ($e->status() !== 400) {
                throw $e;
            }
            $error = $this->apiFailureMessage($e);
        }
        $users = $this->api->get('/api/v1/users');

        return $this->section('audits', 'admin/audits', 'admin.audits.title', [
            'filters' => $filters,
            'cursor' => $cursor,
            'entries' => is_array($page['entries']) ? array_values(array_filter($page['entries'], 'is_array')) : [],
            'nextCursor' => is_string($page['next_cursor'] ?? null) ? $page['next_cursor'] : '',
            'projects' => $this->visibleProjects(),
            'users' => is_array($users) ? array_values(array_filter($users, 'is_array')) : [],
            'eventTypes' => self::AUDIT_EVENT_TYPES,
            'error' => $error,
        ], [
            'scripts' => ['/assets/js/audits.js'],
            'jsKeys' => ['admin.audits.export.warning_title', 'admin.audits.export.warning_body', 'admin.audits.export.confirm'],
        ]);
    }

    /**
     * The audited filters as API query parameters (the listing and the export share them):
     * `type` always, the rest only when set — a 0 id or empty string means "all".
     *
     * @param array<string, string> $filters
     * @return array<string, scalar>
     */
    private function auditFilterQuery(array $filters): array
    {
        $query = ['type' => $filters['type']];
        foreach (['project', 'user'] as $key) {
            if ($filters[$key] !== '0') {
                $query[$key] = $filters[$key];
            }
        }
        foreach (['event_type', 'from', 'to'] as $key) {
            if ($filters[$key] !== '') {
                $query[$key] = $filters[$key];
            }
        }

        return $query;
    }

    /**
     * `estimate=1` (REQ-API-158): the API's {rows, estimated_bytes} passed straight through
     * as JSON for audits.js. A failure answers JSON too — the JS treats any non-ok estimate
     * as "start the export without a warning" rather than blocking it.
     *
     * @param array<string, scalar> $query
     */
    private function auditExportEstimate(array $query): Response
    {
        try {
            $estimate = $this->api->get('/api/v1/audit/export', $query + ['estimate' => '1']);
        } catch (ApiException $e) {
            return Response::json(['error' => $e->code()], max($e->status(), 400));
        }

        return Response::json($estimate);
    }

    /**
     * `download=1` (REQ-AUD-031): the API's ZIP streamed to the browser — one chunk at a
     * time, never materialized (REQ-TECH-011), the mechanics of ExportController::download.
     * A refusal arrives before any byte and flashes on the Audits page; a transfer that
     * breaks off later can only be logged.
     *
     * @param array<string, scalar> $query
     */
    private function auditExportStream(array $query): Response
    {
        return Response::stream(function (StreamSink $sink) use ($query): void {
            try {
                $this->api->stream('/api/v1/audit/export', $query,
                    static function (int $status, array $upstream) use ($sink): void {
                        // The download holds the session lock open otherwise (Session::close).
                        Session::close();
                        $sink->begin(200, [
                            'Content-Type' => 'application/zip',
                            'Content-Disposition' => self::auditZipDisposition($upstream['content-disposition'] ?? ''),
                            'Cache-Control' => 'no-store',
                            // Asks nginx not to buffer the proxied body (Technology_Stack §5).
                            'X-Accel-Buffering' => 'no',
                        ]);
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->info('audit export refused', ['code' => $e->code(), 'status' => $e->status()]);
                Session::flash('danger', $this->apiFailureMessage($e));
                $sink->begin(303, ['Location' => '/admin?section=audits', 'Cache-Control' => 'no-store']);
            }
        });
    }

    /** The API's sanitized `audit_<type>_<date>_<time>.zip` name passed through — a value
     *  that is not a plain zip filename falls back to a locally built one. */
    private static function auditZipDisposition(string $upstream): string
    {
        if (preg_match('/filename="([A-Za-z0-9._-]{1,128}\.zip)"/', $upstream, $m) === 1) {
            return 'attachment; filename="' . $m[1] . '"';
        }

        return 'attachment; filename="audit_export_' . gmdate('Y-m-d_Hi') . '.zip"';
    }

    private function dateParam(string $raw): string
    {
        return preg_match('/^\d{4}-\d{2}-\d{2}$/', $raw) === 1 ? $raw : '';
    }

    // --- Translations (§5.7, REQ-UI-030) ----------------------------------------

    /**
     * The page renders the shell of the editor — language picker, filter controls and an
     * empty data-region container; the key table itself is a Tabulator-backed data region
     * bound by `web/assets/js/translations.js` from this route's JSON (§5.7, REQ-UI-044,
     * AGENTS.md "use Tabulator"). Saving stays the plain PRG form POST below (§3.3).
     */
    private function translationsPage(string $error = ''): Response
    {
        [$languages, $language] = $this->translationLanguages();

        return $this->section('translations', 'admin/translations', 'admin.translations.title', [
            'languages' => $languages,
            'language' => $language,
            'missingOnly' => $this->request->query('missing') === '1',
            'error' => $error,
        ], [
            'scripts' => ['/assets/js/translations.js'],
            'styles' => ['/assets/vendor/tabulator/tabulator_bootstrap5.min.css'],
            'jsKeys' => [
                'js.loading', 'js.load_failed', 'js.retry',
                // The three column titles: the rows left the page for the data region, so the
                // header text now comes from the injected block rather than the PHP view.
                'admin.translations.key', 'admin.translations.english', 'admin.translations.text',
                'admin.translations.empty', 'admin.translations.missing', 'admin.translations.missing_status',
                'admin.translations.edited',
                // The import result line (REQ-UI-049) is composed client-side over the staged rows.
                'admin.translations.import_applied', 'admin.translations.import_skipped',
                'admin.translations.import_empty', 'admin.translations.import_nothing',
                'admin.translations.import_failed',
            ],
        ]);
    }

    /**
     * The data region of `GET /admin` for `Accept: application/json` (§2.1, REQ-UI-044):
     * the Translations section's key rows — exactly what its rendered table would show,
     * nothing more — and the Monitoring section's `{metrics, rate_limit, stress_test}` values
     * (§5.11, REQ-UI-064/068), which its script re-reads once per second. Every other
     * section carries no region and answers 406.
     */
    public function data(): Response
    {
        $section = Navigation::resolveAdminSection($this->request->query('section'));
        if (($section['key'] ?? '') === 'api') {
            return $this->apiSpec();
        }
        if (($section['key'] ?? '') === 'monitoring') {
            $facts = $this->monitoringFacts();
            $facts['stress_test'] = $this->monitoringStressStatus();

            return Response::json($facts);
        }
        if (($section['key'] ?? '') !== 'translations') {
            return Response::notAcceptable();
        }

        [, $language] = $this->translationLanguages();

        return Response::json(['language' => $language, 'rows' => $this->translationRows($language)]);
    }

    // --- Configuration Check (§5.10, REQ-UI-061) -------------------------------------

    /**
     * The Configuration Check section: INSTALL.md as checks. The PHP process probes itself,
     * the API reports what only it can see — its configuration, its host, the database through
     * its own account (`GET /api/v1/system/check`, REQ-API-159) — and ConfigCheck rates both;
     * `config-check.js` adds the routing probes only a browser can make. An API that cannot
     * answer is itself the finding, so a failure renders rather than throwing.
     */
    private function configCheckPage(): Response
    {
        $facts = null;
        $apiError = '';
        try {
            $facts = $this->api->get('/api/v1/system/check');
        } catch (ApiException $e) {
            $this->logger->warn('configuration check: api facts unavailable', ['code' => $e->code(), 'status' => $e->status()]);
            $apiError = $e->code();
        }

        $groups = ConfigCheck::evaluate($this->config, ConfigCheck::probe($this->config, $_SERVER), $facts, $apiError,
            (int) gmdate('Y'));

        return $this->section('check', 'admin/check', 'admin.check.title', [
            'groups' => $groups,
            'summary' => ConfigCheck::summary($groups),
            'probes' => ConfigCheck::BROWSER_PROBES,
            'manual' => ConfigCheck::MANUAL,
        ], [
            'scripts' => ['/assets/js/config-check.js'],
            'jsKeys' => array_merge(
                ['admin.check.status.ok', 'admin.check.status.warn', 'admin.check.status.fail', 'admin.check.status.info',
                    'admin.check.status.skip', 'admin.check.status.pending', 'admin.check.browser.failed'],
                self::probeKeys()
            ),
        ]);
    }

    /** The outcome lines config-check.js composes (REQ-UI-008): every browser probe's. */
    private static function probeKeys(): array
    {
        return [
            'admin.check.browser.admin_api.ok', 'admin.check.browser.admin_api.exposed',
            'admin.check.browser.docs.ok', 'admin.check.browser.docs.exposed',
            'admin.check.browser.assets.ok', 'admin.check.browser.assets.cache', 'admin.check.browser.assets.vendor',
            'admin.check.browser.hsts.ok', 'admin.check.browser.hsts.missing', 'admin.check.browser.hsts.plain',
        ];
    }

    // --- Monitoring (§5.11, REQ-UI-064) ----------------------------------------------

    /**
     * The Monitoring section: live API performance, rate-limiter state, and background stress-test
     * status. The server-rendered first paint reads the two API endpoints the page then polls —
     * `GET /api/v1/system/metrics` (Design §4.25, REQ-API-163) and
     * `GET /api/v1/system/blocked-ips` (§4.26, REQ-API-164) — and monitoring.js takes over:
     * once a second while the page is visible it re-reads this section's data region, appends
     * the sample to a browser-side buffer, and redraws the d3 line charts of the configured
     * period (MONITORING_PERIOD_MINUTES, REQ-CFG-034) as observed since the page was opened —
     * samples older than that period are dropped. An API that cannot answer is itself the
     * finding, so a failure renders rather than throwing.
     */
    private function monitoringPage(): Response
    {
        $facts = $this->monitoringFacts();

        return $this->section('monitoring', 'admin/monitoring', 'admin.monitoring.title', [
            'metrics' => $facts['metrics'],
            'metricsError' => $facts['metrics_error'],
            'rateLimit' => $facts['rate_limit'],
            'rateLimitError' => $facts['rate_limit_error'],
            'stressTest' => $this->monitoringStressStatus(),
            // The one place the browser learns the period: a data attribute, never an env read
            // (REQ-UI-002). monitoring.js keeps its buffer to it (REQ-CFG-034, REQ-UI-064).
            'periodMinutes' => $this->config->monitoringPeriodMinutes,
        ], [
            // d3 is a UMD library attaching to the global; module scripts run in document
            // order, so monitoring.js sees it. Static extras — no dynamic loadScript dance.
            'scripts' => ['/assets/vendor/d3/d3.v7.min.js', '/assets/js/monitoring.js'],
            'jsKeys' => [
                'admin.monitoring.stat.rps', 'admin.monitoring.stat.latency', 'admin.monitoring.stat.goroutines',
                'admin.monitoring.stat.heap', 'admin.monitoring.stat.pool', 'admin.monitoring.stat.waits',
                'admin.monitoring.stat.throughput', 'admin.monitoring.stat.blocked', 'admin.monitoring.stat.uptime',
                'admin.monitoring.chart.rps', 'admin.monitoring.chart.latency', 'admin.monitoring.chart.goroutines',
                'admin.monitoring.chart.heap', 'admin.monitoring.chart.pool', 'admin.monitoring.chart.waits',
                'admin.monitoring.chart.gc', 'admin.monitoring.chart.throughput',
                'admin.monitoring.blocked.none', 'admin.monitoring.blocked.on', 'admin.monitoring.blocked.off',
                'admin.monitoring.failed', 'admin.monitoring.updated',
                'admin.monitoring.stress.phase.startup', 'admin.monitoring.stress.phase.setup',
                'admin.monitoring.stress.phase.import', 'admin.monitoring.stress.phase.export',
                'admin.monitoring.stress.phase.complete', 'admin.monitoring.stress.phase.failed',
                'admin.monitoring.stress.progress',
                'admin.monitoring.stress.progress_percent',
                'admin.monitoring.stress.error.project_names_exist',
                'admin.monitoring.stress.error.status_unavailable', 'admin.monitoring.stress.error.worker_failed',
            ],
        ]);
    }

    /** Starts the Monitoring section's authenticated background stress-test worker. */
    public function startMonitoringStressTest(): Response
    {
        try {
            (new MonitoringStressTest($this->config))->start(Session::userId());
        } catch (\RuntimeException $e) {
            $this->logger->error('monitoring stress test could not start', [
                'reason' => $e->getMessage(),
                'user' => Session::userId(),
            ]);
            $this->flashDanger($this->i18n->t('admin.monitoring.stress.start_failed'));

            return $this->toSection('monitoring');
        }

        $this->flashSuccess($this->i18n->t('admin.monitoring.stress.started'));

        return $this->toSection('monitoring');
    }

    /** @return array<string, mixed>|null */
    private function monitoringStressStatus(): ?array
    {
        try {
            return (new MonitoringStressTest($this->config))->status();
        } catch (\RuntimeException $e) {
            $this->logger->error('monitoring stress test status unavailable', ['reason' => $e->getMessage()]);

            return [
                'status' => 'failed',
                'phase' => 'failed',
                'progress' => 0,
                'error' => 'status_unavailable',
            ];
        }
    }

    /**
     * The Monitoring section's two upstream reads (§5.11): each key is null with its error code
     * when that endpoint cannot be reached — the page and the data region (REQ-UI-044) render
     * either way, and one failing endpoint must not hide the other's live numbers. The data
     * region answers with this same object once per second while the page is open.
     *
     * @return array{metrics: ?array<mixed>, metrics_error: ?string, rate_limit: ?array<mixed>, rate_limit_error: ?string}
     */
    private function monitoringFacts(): array
    {
        $out = [];
        foreach (['metrics' => '/api/v1/system/metrics', 'rate_limit' => '/api/v1/system/blocked-ips'] as $key => $path) {
            try {
                $out[$key] = $this->api->get($path);
                $out[$key . '_error'] = null;
            } catch (ApiException $e) {
                $this->logger->warn('monitoring: api facts unavailable', ['endpoint' => $path, 'code' => $e->code(), 'status' => $e->status()]);
                $out[$key] = null;
                $out[$key . '_error'] = $e->code();
            }
        }

        return $out;
    }

    // --- Clara assistant (§5.12, REQ-AI-001) ----------------------------------------------

    /**
     * The Assistant section: the chat surface for the in-browser model (AI_Assistant_Design.md §6).
     * The server renders the frame — status card, mode switch, empty chat pane — and hands the
     * page its two script extras; `assistant.js` probes WebGPU, loads the locally hosted model
     * artifacts when they are installed, and drives the conversation over `POST /mcp`. Whether
     * the model artifacts exist is a file check, never a guess: absent artifacts render as the
     * translated "not installed" state rather than a broken engine (REQ-AI-012).
     */
    private function assistantPage(): Response
    {
        return $this->section('assistant', 'admin/assistant', 'admin.assistant.title', [
            'writeMode' => Session::assistantWriteMode(),
            'modelInstalled' => $this->modelArtifactsInstalled(),
        ], [
            'scripts' => ['/assets/vendor/d3/d3.v7.min.js', '/assets/js/assistant.js'],
            'jsKeys' => [
                'admin.assistant.status.model_loading', 'admin.assistant.status.model_ready',
                'admin.assistant.status.thinking', 'admin.assistant.status.empty_answer',
                'admin.assistant.status.tool_running',
                'admin.assistant.status.tool_done', 'admin.assistant.status.tool_failed',
                'admin.assistant.status.declined', 'admin.assistant.status.tool_repeated',
                'admin.assistant.who.user', 'admin.assistant.who.assistant',
                'admin.assistant.confirm.body',
                'admin.assistant.error.no_webgpu', 'admin.assistant.error.model_missing',
                'admin.assistant.error.turn_detail', 'admin.assistant.error.engine_detail',
                'admin.assistant.error.mcp', 'admin.assistant.error.too_many_rounds',
                'admin.assistant.error.repeat_stopped',
                // The one string that explains a CSP refusal has to reach the page to be read: an
                // unregistered key renders as itself, which is what an administrator saw here.
                'admin.assistant.error.wasm_blocked',
                'admin.assistant.diagnostics.title',
            ],
        ]);
    }

    /**
     * Whether this installation holds a complete set of model artifacts (REQ-AI-012). Config and
     * manifest without the weight shards they name — what an interrupted fetch leaves behind — are
     * not installed, nor is an install missing any tokenizer file the chat config names or the
     * engine's tensor-cache manifest: the section renders its translated "not installed" notice
     * rather than a form whose engine can only fail. Local files only, never a network probe; a
     * record that points off this origin cannot be checked here, so it is left to the engine.
     */
    private function modelArtifactsInstalled(): bool
    {
        $config = json_decode((string) @file_get_contents(CLARA_WEB_ROOT . '/assets/models/app-config.json'), true);
        $record = is_array($config) ? ($config['model_list'][0] ?? null) : null;
        $modelPath = is_array($record) ? ($record['model'] ?? null) : null;
        if (!is_string($modelPath)) {
            return false;
        }
        if (preg_match('#^[a-z]+://#', $modelPath) === 1) {
            return true;
        }

        // The record holds a site-relative path; the bundle appends resolve/main/ to it, which is
        // where the manifest and the shards it names live.
        $dir = CLARA_WEB_ROOT . '/' . ltrim($modelPath, '/') . '/resolve/main';
        $manifest = json_decode((string) @file_get_contents($dir . '/ndarray-cache.json'), true);
        $records = is_array($manifest) ? ($manifest['records'] ?? null) : null;
        if (!is_array($records) || $records === []) {
            return false;
        }

        foreach ($records as $entry) {
            $shard = is_array($entry) ? ($entry['dataPath'] ?? null) : null;
            if (is_string($shard) && !is_file($dir . '/' . $shard)) {
                return false;
            }
        }

        // The tokenizer files the engine fetches next to the shards, named by the chat config it
        // loads before them. Without one of these the page offers a model that cannot load, and
        // the only sign of it was the browser's 404 on /resolve/main/tokenizer.json.
        $chat = json_decode((string) @file_get_contents($dir . '/mlc-chat-config.json'), true);
        $tokenizerFiles = is_array($chat) ? ($chat['tokenizer_files'] ?? null) : null;
        if (!is_array($tokenizerFiles) || $tokenizerFiles === []) {
            return false;
        }
        foreach ($tokenizerFiles as $file) {
            if (!is_string($file) || !is_file($dir . '/' . $file)) {
                return false;
            }
        }

        // The tensor-cache manifest, which the engine loads its weights through. Its name comes from
        // a constant in the bundle — web-llm v0.2.86 asks for `<model>/resolve/main/tensor-cache.json`
        // unconditionally, before any shard — so no file in this directory names it and nothing else
        // here would notice its absence; the browser's 404 on it was the only report. It sits beside
        // the shards, and `app-config.json` never sets `model_manifest`, the one record field that
        // could point the engine at a differently named manifest elsewhere.
        if (!is_file($dir . '/tensor-cache.json')) {
            return false;
        }

        $lib = is_array($record) ? ($record['model_lib'] ?? null) : null;
        if (is_string($lib) && preg_match('#^[a-z]+://#', $lib) !== 1) {
            return is_file(CLARA_WEB_ROOT . '/' . ltrim($lib, '/'));
        }

        return true;
    }

    /**
     * POST /admin?action=assistant_mode — the session's read-write switch (REQ-AI-006). It holds
     * no more authority than a checkbox: the MCP dispatcher refuses every write tool while the
     * flag is off, and the flag resets with the session.
     */
    public function setAssistantMode(): Response
    {
        $on = $this->request->field('mode') === 'write';
        Session::setAssistantWriteMode($on);
        $this->flashSuccess($this->i18n->t($on ? 'admin.assistant.mode.on' : 'admin.assistant.mode.off'));

        return $this->toSection('assistant');
    }

    // --- API documentation (§5.9, REQ-UI-057) ----------------------------------------

    /**
     * The API documentation section: the relevance Sankey of REQ-UI-060 above the API's own
     * OpenAPI document rendered by the vendored Swagger UI, inside the Control Panel. The API's
     * `/docs` page is never routed to a browser (INSTALL.md §13.1, REQ-TECH-018), so the section
     * is how an administrator reads it; `api-docs.js` loads both libraries — d3 for the diagram,
     * Swagger UI for the document — and reads the document from this section's data region.
     * Read-only: the administration API it describes is not reachable from a browser.
     */
    private function apiDocsPage(): Response
    {
        return $this->section('api', 'admin/api', 'admin.api.title', [
            'sankey' => $this->relevanceSankey(),
        ], [
            'scripts' => ['/assets/js/api-docs.js'],
            'styles' => ['/assets/vendor/swagger-ui/swagger-ui.css'],
            'jsKeys' => ['admin.api.failed', 'admin.api.sankey.tooltip.codes', 'admin.api.sankey.tooltip.link'],
        ]);
    }

    /**
     * The relevance Sankey's render payload (REQ-UI-060, §5.9): the generated code-to-relevance
     * structure with every visible line translated server-side (REQ-UI-008) — area one-liners and
     * class names from the catalog, group nodes carrying their code family prefix verbatim. The
     * file is generated from docs/Requirement_Relevance_Classification.md by
     * tools/build_relevance_sankey.php; when it is absent the section simply renders without the
     * diagram — documentation of the OpenAPI document never depends on the classification build.
     *
     * @return array{nodes: list<array<string, mixed>>, links: list<array<string, mixed>>}|null
     */
    private function relevanceSankey(): ?array
    {
        $path = CLARA_WEB_ROOT . '/app/data/relevance_sankey.php';
        if (!is_file($path)) {
            return null;
        }

        $data = require $path;
        if (!is_array($data) || !isset($data['nodes'], $data['links'])) {
            $this->logger->warn('relevance sankey data unreadable', ['path' => $path]);

            return null;
        }

        $nodes = [];
        foreach ($data['nodes'] as $node) {
            if (($node['column'] ?? -1) === 0) {
                $label = $this->i18n->t('admin.api.sankey.feature.' . $node['area']);
                $class = null;
            } elseif ($node['column'] === 1) {
                $label = (string) $node['prefix'];
                $class = null;
            } else {
                $label = $this->i18n->t('admin.api.sankey.class.' . $node['class']);
                $class = $this->i18n->t('admin.api.sankey.class_desc.' . $node['class']);
            }

            $nodes[] = array_filter([
                'id' => $node['id'],
                // The class id is data, not display — the renderer colours each class by it.
                'class' => $node['class'] ?? null,
                'column' => $node['column'],
                'order' => $node['order'],
                'label' => $label,
                // Group nodes carry the family word for their tooltip ("REQ-API · requirement").
                'kind' => isset($node['kind']) ? $this->i18n->t('admin.api.sankey.kind.' . $node['kind']) : null,
                'desc' => $class,
                'codes' => $node['codes'] ?? null,
            ], static fn (mixed $v): bool => $v !== null);
        }

        return ['nodes' => $nodes, 'links' => $data['links']];
    }

    /**
     * The API documentation section's data region: `GET /openapi.json` passed through as the
     * API serves it — verbatim, since decoding and re-encoding it would turn its empty objects
     * into lists. A refusal before the first byte answers like any other data region (§3.4).
     */
    private function apiSpec(): Response
    {
        return Response::stream(function (StreamSink $sink): void {
            try {
                $this->api->stream('/openapi.json', [],
                    static function (int $status, array $upstream) use ($sink): void {
                        Session::close();
                        $sink->begin(200, ['Content-Type' => 'application/json', 'Cache-Control' => 'no-store']);
                    },
                    static fn (string $chunk) => $sink->write($chunk)
                );
            } catch (ApiException $e) {
                $this->logger->warn('api documentation unavailable', ['code' => $e->code(), 'status' => $e->status()]);
                $sink->begin($e->status(), ['Content-Type' => 'application/json', 'Cache-Control' => 'no-store']);
                $sink->write((string) json_encode(['error' => $e->code(), 'message' => $this->i18n->t('admin.api.failed')]));
            }
        });
    }

    /** The enabled languages and the one selected for editing (§5.7). */
    private function translationLanguages(): array
    {
        $languages = $this->api->get('/api/v1/i18n/languages');
        $languages = is_array($languages) ? array_values(array_filter($languages, 'is_array')) : [];
        $codes = array_column($languages, 'code');
        $language = $this->request->query('language');
        if (!in_array($language, $codes, true)) {
            // The first language other than the English source, when there is one.
            $language = array_values(array_diff($codes, ['en']))[0] ?? ($codes[0] ?? '');
        }

        return [$languages, $language];
    }

    /**
     * The key universe: the English catalog in code (REQ-DB-031) merged with the
     * language's overlay — every key carries its current text and missing flag, and a
     * key only the overlay carries still shows up so it can be cleared. Sorted by key.
     *
     * @return list<array{key: string, english: string, text: string, missing: bool}>
     */
    private function translationRows(string $language): array
    {
        if ($language === '') {
            return [];
        }

        $current = $this->currentStrings($language);
        $rows = [];
        foreach ($this->i18n->englishCatalog() as $key => $english) {
            $rows[$key] = ['key' => $key, 'english' => $english, 'text' => $current[$key] ?? '', 'missing' => !isset($current[$key])];
        }
        foreach ($current as $key => $text) {
            $rows[$key] ??= ['key' => $key, 'english' => '', 'text' => $text, 'missing' => false];
        }
        ksort($rows);

        return array_values($rows);
    }

    /** @return array<string, string> key → text the language has (no row = missing) */
    private function currentStrings(string $language): array
    {
        $listing = $this->api->get('/api/v1/i18n/strings', ['language' => $language]);
        $out = [];
        foreach (is_array($listing) ? $listing : [] as $row) {
            if (is_array($row) && empty($row['missing']) && isset($row['key'])) {
                $out[(string) $row['key']] = (string) ($row['text'] ?? '');
            }
        }

        return $out;
    }

    public function saveTranslations(): Response
    {
        $language = trim($this->request->field('language'));
        $submitted = $this->request->fieldMap('text');
        $current = $this->currentStrings($language);

        // Send only what changed: a new or edited text, or an emptied one that existed
        // (empty = remove, falls back to English — REQ-API-100).
        $entries = [];
        foreach ($submitted as $key => $text) {
            $text = trim($text);
            $had = $current[$key] ?? null;
            if (($had === null && $text !== '') || ($had !== null && $had !== $text)) {
                $entries[] = ['key' => $key, 'text' => $text];
            }
        }

        $back = '&language=' . rawurlencode($language) . ($this->request->field('missing') === '1' ? '&missing=1' : '');
        if ($entries === []) {
            $this->flashSuccess($this->i18n->t('admin.translations.unchanged'));

            return $this->toSection('translations', $back);
        }

        try {
            $this->api->put('/api/v1/i18n/strings', ['language' => $language, 'entries' => $entries]);
        } catch (ApiException $e) {
            $this->logger->info('translations rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger($this->apiFailureMessage($e));

            return $this->toSection('translations', $back);
        }

        $this->flashSuccess($this->i18n->t('admin.translations.saved', ['count' => (string) count($entries)]));

        return $this->toSection('translations', $back);
    }

    // --- Settings (§5.8, REQ-UI-043) --------------------------------------------

    public function saveSettings(): Response
    {
        $enabled = $this->request->field('rate_limit_enabled') === '1';
        $rpm = $this->request->field('rate_limit_rpm');
        $blockMinutes = $this->request->field('rate_limit_block_minutes');

        // Whole numbers only before anything reaches the API — the API checks the ranges.
        if (preg_match('/^\d+$/', $rpm) !== 1 || preg_match('/^\d+$/', $blockMinutes) !== 1) {
            return $this->settingsPage($this->i18n->t('admin.settings.invalid'), $enabled, $rpm, $blockMinutes);
        }

        try {
            $this->api->put('/api/v1/settings', [
                'rate_limit_enabled' => $enabled,
                'rate_limit_rpm' => (int) $rpm,
                'rate_limit_block_minutes' => (int) $blockMinutes,
            ]);
        } catch (ApiException $e) {
            $this->logger->info('settings change rejected', ['code' => $e->code(), 'status' => $e->status()]);

            return $this->settingsPage($this->apiFailureMessage($e), $enabled, $rpm, $blockMinutes);
        }

        $this->flashSuccess($this->i18n->t('admin.settings.saved'));

        return Response::redirect('/admin?section=settings');
    }

    private function settingsPage(string $error, ?bool $enabled = null, ?string $rpm = null, ?string $blockMinutes = null): Response
    {
        if ($enabled === null || $rpm === null || $blockMinutes === null) {
            $settings = $this->api->get('/api/v1/settings');
            $settings = is_array($settings) ? $settings : [];
            $enabled = !empty($settings['rate_limit_enabled']);
            $rpm = (string) ($settings['rate_limit_rpm'] ?? '');
            $blockMinutes = (string) ($settings['rate_limit_block_minutes'] ?? '');
        }

        return $this->section('settings', 'admin/settings', 'admin.settings.title', [
            'error' => $error,
            'enabled' => $enabled,
            'rpm' => $rpm,
            'blockMinutes' => $blockMinutes,
        ]);
    }
}
