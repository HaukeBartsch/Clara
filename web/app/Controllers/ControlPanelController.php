<?php
// The Control Panel (User_Interface_Design.md §5, REQ-UI-047): the one is_admin page that
// carries every installation-administration function. Its left panel lists the sections
// (Users, Projects, Audits, Translations, Settings — Clara\Navigation), `?section=` picks the
// one in the right-hand panel, and every mutation is a POST to /admin with its ?action=
// (§2.1, REQ-UI-005; the router has already checked the CSRF token).
//
// Each write sends exactly the attributes its endpoint whitelists — the administration API
// answers 400 to anything else (Plan/Web_Implementation.md §7 rule 1) — and every outcome is
// one translated line (§3.4): success after a redirect (PRG), failure either as a re-rendered
// form that keeps what was typed (create forms) or as a flashed line (row actions).

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\ApiException;
use Clara\Messages;
use Clara\Navigation;
use Clara\Response;
use Clara\Session;

final class ControlPanelController extends Controller
{
    /** The main supporting institutions of REQ-DB-006 (GD-17) — the organization select. */
    public const ORGANIZATIONS = ['OTHER', 'VEST', 'HBE', 'SUS', 'FOR', 'FON', 'UIB', 'UIS', 'HVL', 'NAT EU'];

    /** The optional project attributes (§5.2): empty on edit means "clear" (null). */
    private const PROJECT_OPTIONAL = [
        'dm_name', 'dm_email', 'rek_number', 'rek_start_date', 'rek_end_date', 'start_date', 'end_date',
    ];

    /** The required project attributes (§5.2, REQ-DB-006). */
    private const PROJECT_REQUIRED = ['project_name', 'organization', 'pi_name', 'pi_email', 'participant_names'];

    /**
     * The event codes of Audit_Logging_Design.md §3 — the audit filter's select (§5.6).
     * Sources (`api`/`ui`/`system`) are not event types and are not listed.
     */
    public const AUDIT_EVENT_TYPES = [
        'account_auto_disabled', 'admin_rejected', 'arm_created', 'arm_deleted', 'arm_reordered', 'arm_updated',
        'calculated_recomputed', 'dag_active_switched', 'dag_created', 'dag_deleted', 'dag_membership_changed',
        'dag_record_assigned', 'event_created', 'event_deleted', 'event_reordered', 'event_updated', 'export',
        'field_created', 'field_deleted', 'field_reordered', 'field_updated', 'instrument_completed',
        'instrument_created', 'instrument_deleted', 'instrument_reordered', 'instrument_uncompleted',
        'instrument_updated', 'invite_accepted', 'login_failure', 'login_success', 'logout', 'mapping_updated',
        'membership_changed', 'password_changed', 'password_reset_completed', 'password_reset_requested',
        'project_created', 'project_ended', 'project_mode_changed', 'project_updated', 'record_created',
        'record_deleted', 'record_updated', 'role_created', 'settings_updated', 'staging_committed',
        'staging_discarded', 'staging_started', 'survey_link_issued', 'survey_link_revoked', 'survey_submitted',
        'tfa_disabled', 'tfa_enrolled', 'tfa_reset', 'token_issued', 'token_revoked', 'token_rotated',
        'user_created', 'user_invited', 'user_updated',
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
            default => $this->page('admin/none', [
                'pageTitle' => $this->i18n->t('admin.title'),
            ], ['titleKey' => 'admin.title']),
        };
    }

    /**
     * Renders one section inside the Control Panel shell: the left panel marks it active
     * (the path alone cannot, §2.1), and the shared admin module wires confirmations and
     * copy buttons (§3.5).
     */
    private function section(string $key, string $template, string $titleKey, array $data): Response
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
            'scripts' => ['/assets/app.js', '/assets/js/admin.js'],
            'jsKeys' => ['admin.confirm.title', 'action.cancel', 'admin.confirm.ok', 'tfa.copied'],
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

            return $this->usersPage(Messages::forApiException($this->i18n, $e), $form);
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
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

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
                : Messages::forApiException($this->i18n, $e));

            return $this->toSection('users');
        }
        $this->flashSuccess($this->i18n->t('admin.users.invited', ['email' => $this->targetEmail()]));

        return $this->toSection('users');
    }

    public function resetUserTfa(): Response
    {
        $id = $this->targetUserId();
        try {
            $this->api->post('/api/v1/users/' . $id . '/tfa/reset');
        } catch (ApiException $e) {
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $this->toSection('users');
        }
        $this->flashSuccess($this->i18n->t('admin.users.tfa_reset', ['email' => $this->targetEmail()]));

        return $this->toSection('users');
    }

    /** One `PUT /api/v1/users/{id}` row action with exactly one attribute (§7 rule 1). */
    private function userUpdate(array $body, string $successKey): Response
    {
        $id = $this->targetUserId();
        try {
            $this->api->put('/api/v1/users/' . $id, $body);
        } catch (ApiException $e) {
            $this->logger->info('user update rejected', ['code' => $e->code(), 'status' => $e->status()]);
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

            return $this->toSection('users');
        }
        $this->flashSuccess($this->i18n->t($successKey, ['email' => $this->targetEmail()]));

        return $this->toSection('users');
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

            return $this->projectsPage(Messages::forApiException($this->i18n, $e), $form);
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

            return $this->projectsPage(Messages::forApiException($this->i18n, $e), $form, $id);
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

        return $form;
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
        $cursor = $this->request->query('cursor');
        $cursor = preg_match('/^[A-Za-z0-9_=-]{1,512}$/', $cursor) === 1 ? $cursor : '';

        $query = ['type' => $filters['type'], 'limit' => (string) self::AUDIT_PAGE];
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
            $error = Messages::forApiException($this->i18n, $e);
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
        ]);
    }

    private function dateParam(string $raw): string
    {
        return preg_match('/^\d{4}-\d{2}-\d{2}$/', $raw) === 1 ? $raw : '';
    }

    // --- Translations (§5.7, REQ-UI-030) ----------------------------------------

    private function translationsPage(string $error = ''): Response
    {
        $languages = $this->api->get('/api/v1/i18n/languages');
        $languages = is_array($languages) ? array_values(array_filter($languages, 'is_array')) : [];
        $codes = array_column($languages, 'code');
        $language = $this->request->query('language');
        if (!in_array($language, $codes, true)) {
            // The first language other than the English source, when there is one.
            $language = array_values(array_diff($codes, ['en']))[0] ?? ($codes[0] ?? '');
        }

        $rows = [];
        if ($language !== '') {
            $current = $this->currentStrings($language);
            foreach ($this->i18n->englishCatalog() as $key => $english) {
                $rows[$key] = ['key' => $key, 'english' => $english, 'text' => $current[$key] ?? '', 'missing' => !isset($current[$key])];
            }
            // A key only the overlay carries still shows up, so it can be cleared.
            foreach ($current as $key => $text) {
                $rows[$key] ??= ['key' => $key, 'english' => '', 'text' => $text, 'missing' => false];
            }
            ksort($rows);
        }
        $missingOnly = $this->request->query('missing') === '1';

        return $this->section('translations', 'admin/translations', 'admin.translations.title', [
            'languages' => $languages,
            'language' => $language,
            'rows' => array_values($missingOnly ? array_filter($rows, static fn (array $r): bool => $r['missing']) : $rows),
            'missingCount' => count(array_filter($rows, static fn (array $r): bool => $r['missing'])),
            'missingOnly' => $missingOnly,
            'error' => $error,
        ]);
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
            $this->flashDanger(Messages::forApiException($this->i18n, $e));

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

            return $this->settingsPage(Messages::forApiException($this->i18n, $e), $enabled, $rpm, $blockMinutes);
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
