<?php
// The route set of User_Interface_Design.md §2.1 — the complete set of
// browser-reachable routes — plus the two conventions that hang off it:
//
//   * mutations are POSTs to the page route with ?action=<name> (§2.1), and
//     every POST carries a CSRF token before its controller runs (§3.3);
//   * the JSON a page's client binds data regions from is served by that same
//     route, selected on Accept (REQ-UI-044). This file holds the only
//     implementation of that decision in the application: guard → permission
//     gate → Accept split → controller, so a data request is authorized exactly
//     like the page render it stands for and no controller or template grows an
//     `if wants_json()` branch of its own. A route with no declared region
//     answers 406 rather than HTML.
//
// Adding a page means adding one row here and one controller method: no routing
// code, and nothing to forget on the second shape.

declare(strict_types=1);

namespace Clara;

final class Router
{
    /**
     * Each route: HTTP method, path pattern with {param} placeholders,
     * controller class, handler method, guard (`public` | `login` | `admin`),
     * optional `?action=` value, and the data region the route can serve as JSON.
     *
     * Only the routes this pass implements are listed; §2.1's remainder arrives
     * with M2–M6 of Plan/Web_Implementation.md, one row each.
     *
     * A row marked `'session' => false` runs without the PHP session — no cookie, no CSRF
     * token (the public survey page, §8.8).
     *
     * @var list<array{method: string, pattern: string, controller: class-string, handler: string, guard: string, action?: string, region?: string, session?: bool}>
     */
    private const ROUTES = [
        ['method' => 'GET', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'index', 'guard' => 'public'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'credentials', 'guard' => 'public', 'action' => 'credentials'],
        // The source-name picker and the OAuth2 hand-off live on the same route as the
        // credential form (§2.1): choosing a name re-renders the panel for it, and a
        // provider button leaves for the IdP (Sequence A, REQ-AUTH-066).
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'chooseSource', 'guard' => 'public', 'action' => 'source'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'authorize', 'guard' => 'public', 'action' => 'oauth'],
        // The provider's redirect back. Public by necessity — it is how a first-time
        // browser arrives with no session — and the only thing it can do is verify `state`
        // against the transaction this browser started (§2.1 step 3).
        ['method' => 'GET', 'pattern' => '/auth/callback', 'controller' => Controllers\LoginController::class,
            'handler' => 'callback', 'guard' => 'public'],
        // The two-factor panel and the enrollment wizard live on the same route
        // (§2.1: "login incl. source-name picker and the two-factor step"), so
        // the pre-auth `tfa_pending` state reaches exactly these handlers and
        // nothing else — every other route's guard rejects it (§2.7).
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'twoFactor', 'guard' => 'public', 'action' => 'mfa'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'resendCode', 'guard' => 'public', 'action' => 'mfa_resend'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollTotp', 'guard' => 'public', 'action' => 'enroll_totp'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'confirmTotp', 'guard' => 'public', 'action' => 'enroll_totp_confirm'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollEmail', 'guard' => 'public', 'action' => 'enroll_email'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'confirmEmail', 'guard' => 'public', 'action' => 'enroll_email_confirm'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'enrollmentDone', 'guard' => 'public', 'action' => 'enroll_done'],
        ['method' => 'POST', 'pattern' => '/login', 'controller' => Controllers\LoginController::class,
            'handler' => 'abort', 'guard' => 'public', 'action' => 'abort'],

        ['method' => 'GET', 'pattern' => '/', 'controller' => Controllers\DashboardController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'projects'],

        // The project workspace (§6.1). The entry route resolves the first available
        // section and answers 303 (REQ-UI-017); its data region is the same summary the
        // Overview shows — records, instruments, fields, and the metadata block — so a
        // data request gets the object rather than the redirect (REQ-UI-044).
        ['method' => 'GET', 'pattern' => '/projects/{id}', 'controller' => Controllers\ProjectController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'project'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/overview', 'controller' => Controllers\ProjectController::class,
            'handler' => 'overview', 'guard' => 'login'],

        // The mode card on Overview (§6.6, REQ-UI-033): is_admin only — a project's own
        // project_admin is refused by the API too (§4.21).
        ['method' => 'POST', 'pattern' => '/projects/{id}/overview', 'controller' => Controllers\ProjectController::class,
            'handler' => 'setMode', 'guard' => 'admin', 'action' => 'set_mode'],

        // M4 — Setup (§6.2) and the Instrument Designer (§7), both project_admin pages; the
        // controllers refuse anyone else (the guard here only needs a session — project_admin is a
        // per-project permission the API discloses, REQ-API-126). Each page's data region is the
        // expression editor's reference list (§7.3), fetched only when the picker opens.
        ['method' => 'GET', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'references'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'addArm', 'guard' => 'login', 'action' => 'add_arm'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'moveArm', 'guard' => 'login', 'action' => 'move_arm'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'deleteArm', 'guard' => 'login', 'action' => 'delete_arm'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'addEvent', 'guard' => 'login', 'action' => 'add_event'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'updateEvent', 'guard' => 'login', 'action' => 'update_event'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'deleteEvent', 'guard' => 'login', 'action' => 'delete_event'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'moveEvent', 'guard' => 'login', 'action' => 'move_event'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'addInstrument', 'guard' => 'login', 'action' => 'add_instrument'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'updateInstrument', 'guard' => 'login', 'action' => 'update_instrument'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'moveInstrument', 'guard' => 'login', 'action' => 'move_instrument'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'deleteInstrument', 'guard' => 'login', 'action' => 'delete_instrument'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'saveMapping', 'guard' => 'login', 'action' => 'save_mapping'],
        // Staging (§6.7) is offered on every structure page and returns to the page it came from.
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'startStaging', 'guard' => 'login', 'action' => 'staging_start'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'commitStaging', 'guard' => 'login', 'action' => 'staging_commit'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/setup', 'controller' => Controllers\SetupController::class,
            'handler' => 'discardStaging', 'guard' => 'login', 'action' => 'staging_discard'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/design', 'controller' => Controllers\DesignController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'references'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design', 'controller' => Controllers\DesignController::class,
            'handler' => 'startStaging', 'guard' => 'login', 'action' => 'staging_start'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design', 'controller' => Controllers\DesignController::class,
            'handler' => 'commitStaging', 'guard' => 'login', 'action' => 'staging_commit'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design', 'controller' => Controllers\DesignController::class,
            'handler' => 'discardStaging', 'guard' => 'login', 'action' => 'staging_discard'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'instrument', 'guard' => 'login', 'region' => 'references'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'saveField', 'guard' => 'login', 'action' => 'save_field'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'moveField', 'guard' => 'login', 'action' => 'move_field'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'deleteField', 'guard' => 'login', 'action' => 'delete_field'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'testCalculation', 'guard' => 'login', 'action' => 'test_calc'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'startStaging', 'guard' => 'login', 'action' => 'staging_start'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'commitStaging', 'guard' => 'login', 'action' => 'staging_commit'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/design/instruments/{iid}', 'controller' => Controllers\DesignController::class,
            'handler' => 'discardStaging', 'guard' => 'login', 'action' => 'staging_discard'],

        // M5 — the Record Status Dashboard (§6.3) and the record view (§8). Both need data access
        // ≥ read_only on some arm, which is a per-project permission the API discloses
        // (REQ-API-126): the guard needs a session, the controllers refuse anyone else. The
        // dashboard's region is its rows; the record view's is the per-field history (§8.3).
        ['method' => 'GET', 'pattern' => '/projects/{id}/record-status', 'controller' => Controllers\RecordStatusController::class,
            'handler' => 'index', 'guard' => 'login', 'region' => 'records'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/record-status', 'controller' => Controllers\RecordStatusController::class,
            'handler' => 'create', 'guard' => 'login', 'action' => 'new_record'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/record-status', 'controller' => Controllers\RecordStatusController::class,
            'handler' => 'autoName', 'guard' => 'login', 'action' => 'auto_name'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'view', 'guard' => 'login', 'region' => 'history'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'save', 'guard' => 'login', 'action' => 'save'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'delete', 'guard' => 'login', 'action' => 'delete'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'assignGroup', 'guard' => 'login', 'action' => 'assign_group'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'surveyLink', 'guard' => 'login', 'action' => 'survey_link'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/records/{record}', 'controller' => Controllers\RecordController::class,
            'handler' => 'revokeLink', 'guard' => 'login', 'action' => 'revoke_link'],

        // M6 — the export action (§6.4): the page, and with `?download=1` the file it streams
        // (REQ-TECH-011). A GET like every read; the API audits each download (REQ-API-076).
        ['method' => 'GET', 'pattern' => '/projects/{id}/export', 'controller' => Controllers\ExportController::class,
            'handler' => 'index', 'guard' => 'login'],

        // M6 — the public survey page (§8.8): the URL the API issues for a survey link
        // (API §4.17). Outside the session altogether (GD-1): no session is started, no
        // cookie issued, and the link token in the path is the only credential.
        ['method' => 'GET', 'pattern' => '/s/{link}', 'controller' => Controllers\SurveyController::class,
            'handler' => 'show', 'guard' => 'public', 'session' => false],

        // The Control Panel (§5, REQ-UI-047): one is_admin route whose `?section=` picks the
        // section shown in the right-hand panel, defaulting to the first available.
        ['method' => 'GET', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'index', 'guard' => 'admin'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'saveSettings', 'guard' => 'admin', 'action' => 'save_settings'],
        // M3 (§5.1/§5.2/§5.7): every Control Panel mutation posts to /admin with its
        // ?action=; the section to return to rides along as ?section=.
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'createUser', 'guard' => 'admin', 'action' => 'create_user'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'setUserEnabled', 'guard' => 'admin', 'action' => 'user_enabled'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'setUserValidity', 'guard' => 'admin', 'action' => 'user_validity'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'setUserPassword', 'guard' => 'admin', 'action' => 'user_password'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'inviteUser', 'guard' => 'admin', 'action' => 'user_invite'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'resetUserTfa', 'guard' => 'admin', 'action' => 'user_tfa_reset'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'setUserAdmin', 'guard' => 'admin', 'action' => 'user_admin'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'createProject', 'guard' => 'admin', 'action' => 'create_project'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'updateProject', 'guard' => 'admin', 'action' => 'update_project'],
        ['method' => 'POST', 'pattern' => '/admin', 'controller' => Controllers\ControlPanelController::class,
            'handler' => 'saveTranslations', 'guard' => 'admin', 'action' => 'save_translations'],

        // Project-scoped administration on the project page (§5.3/§5.4/§5.5, REQ-UI-047):
        // Members and Roles are is_admin; Groups are readable by members with data access
        // and changed by project_admin — the API decides both (REQ-AUTH-033).
        ['method' => 'GET', 'pattern' => '/projects/{id}/members', 'controller' => Controllers\ProjectController::class,
            'handler' => 'members', 'guard' => 'admin'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/members', 'controller' => Controllers\ProjectController::class,
            'handler' => 'addMember', 'guard' => 'admin', 'action' => 'add_member'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/members', 'controller' => Controllers\ProjectController::class,
            'handler' => 'changeRole', 'guard' => 'admin', 'action' => 'change_role'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/members', 'controller' => Controllers\ProjectController::class,
            'handler' => 'rotateToken', 'guard' => 'admin', 'action' => 'rotate_token'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/members', 'controller' => Controllers\ProjectController::class,
            'handler' => 'removeMember', 'guard' => 'admin', 'action' => 'remove_member'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/roles', 'controller' => Controllers\ProjectController::class,
            'handler' => 'roles', 'guard' => 'admin'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/roles', 'controller' => Controllers\ProjectController::class,
            'handler' => 'saveRole', 'guard' => 'admin', 'action' => 'save_role'],
        ['method' => 'GET', 'pattern' => '/projects/{id}/groups', 'controller' => Controllers\ProjectController::class,
            'handler' => 'groups', 'guard' => 'login'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/groups', 'controller' => Controllers\ProjectController::class,
            'handler' => 'createGroup', 'guard' => 'login', 'action' => 'create_group'],
        ['method' => 'POST', 'pattern' => '/projects/{id}/groups', 'controller' => Controllers\ProjectController::class,
            'handler' => 'deleteGroup', 'guard' => 'login', 'action' => 'delete_group'],

        // Self-service account pages (§2.4 item 4): the second factor (§2.5) and the
        // local password (§2.6). Both live in the shell; both are mutations on their
        // own route with ?action= like every other write (§2.1).
        ['method' => 'GET', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'index', 'guard' => 'login'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'startTotp', 'guard' => 'login', 'action' => 'totp_start'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'confirmTotp', 'guard' => 'login', 'action' => 'totp_confirm'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'startEmail', 'guard' => 'login', 'action' => 'email_start'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'confirmEmail', 'guard' => 'login', 'action' => 'email_confirm'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'cancelEnrollment', 'guard' => 'login', 'action' => 'enroll_cancel'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'enrollmentDone', 'guard' => 'login', 'action' => 'enroll_done'],
        ['method' => 'POST', 'pattern' => '/account/two-factor', 'controller' => Controllers\TwoFactorController::class,
            'handler' => 'disable', 'guard' => 'login', 'action' => 'disable'],
        ['method' => 'GET', 'pattern' => '/account/password', 'controller' => Controllers\AccountController::class,
            'handler' => 'password', 'guard' => 'login'],
        ['method' => 'POST', 'pattern' => '/account/password', 'controller' => Controllers\AccountController::class,
            'handler' => 'changePassword', 'guard' => 'login', 'action' => 'change'],

        // The two public password pages (§2.6, GD-23). Public means no session is
        // required and none is implied: the token in the field is the credential,
        // and these routes establish no identity (Sequence H, Authentication §2.8).
        ['method' => 'GET', 'pattern' => '/password-reset', 'controller' => Controllers\PasswordResetController::class,
            'handler' => 'requestForm', 'guard' => 'public'],
        ['method' => 'POST', 'pattern' => '/password-reset', 'controller' => Controllers\PasswordResetController::class,
            'handler' => 'request', 'guard' => 'public', 'action' => 'request'],
        ['method' => 'GET', 'pattern' => '/set-password', 'controller' => Controllers\PasswordResetController::class,
            'handler' => 'setForm', 'guard' => 'public'],
        ['method' => 'POST', 'pattern' => '/set-password', 'controller' => Controllers\PasswordResetController::class,
            'handler' => 'complete', 'guard' => 'public', 'action' => 'complete'],

        ['method' => 'POST', 'pattern' => '/logout', 'controller' => Controllers\AccountController::class,
            'handler' => 'logout', 'guard' => 'login'],
        // Dedicated POST routes (§2.1: `POST /lang`, `POST /theme`) — the footer forms post to
        // the bare path, so no ?action= is required (it was, and every switch answered 404).
        ['method' => 'POST', 'pattern' => '/lang', 'controller' => Controllers\AccountController::class,
            'handler' => 'language', 'guard' => 'login'],
        ['method' => 'POST', 'pattern' => '/theme', 'controller' => Controllers\AccountController::class,
            'handler' => 'theme', 'guard' => 'login'],
    ];

    public function __construct(
        private readonly Config $config,
        private readonly Request $request,
        private readonly ApiClient $api,
        private readonly I18n $i18n,
        private readonly View $view,
        private readonly Auth $auth,
        private readonly Logger $logger
    ) {}

    /** Matches the request and produces its response. */
    public function dispatch(): Response
    {
        $matched = self::find($this->request);

        if ($matched === null) {
            return $this->notFound();
        }
        [$route, $pathParams] = $matched;

        // --- guard, before either shape (§3.1, REQ-UI-044) ---
        $denied = Auth::guard($route['guard'], $this->config, $this->request->path());
        if ($denied !== null) {
            return $this->shapeRefusal($denied);
        }

        // --- CSRF on every mutation, before the controller and before any API
        // call: a rejected request must not reach the write at all (§3.3). A route
        // that runs without a session has no token to check against: its credential
        // travels in its own path (the survey link, §8.8) ---
        if ($this->request->isPost() && self::usesSession($route) && !Csrf::validate($this->request)) {
            $this->logger->warn('csrf rejection', ['path' => $this->request->path()]);
            Session::flash('danger', $this->i18n->t('error.csrf'));

            // Back to the page the mutation was aimed at, which is also the route
            // whose guard we just passed.
            return Response::redirect($this->request->path());
        }

        // --- the one Accept split (REQ-UI-044) ---
        if ($this->request->wantsJson()) {
            if (($route['region'] ?? null) === null) {
                return Response::notAcceptable();
            }

            return $this->invoke($route, 'data', $pathParams);
        }

        return $this->invoke($route, $route['handler'], $pathParams);
    }

    /**
     * Finds the route row for this request and the values its `{name}`
     * placeholders matched. A POST whose row declares an `action` only matches when
     * `?action=` carries that value — the mutation convention of §2.1 — and a row
     * without one matches any POST to its path.
     *
     * @return array{0: array{method: string, pattern: string, controller: class-string, handler: string, guard: string, action?: string, region?: string}, 1: array<string, string>}|null
     */
    private static function find(Request $request): ?array
    {
        foreach (self::ROUTES as $route) {
            if ($route['method'] !== $request->method()) {
                continue;
            }
            $pathParams = [];
            if (!self::matchesPattern($route['pattern'], $request->path(), $pathParams)) {
                continue;
            }

            $expected = $route['action'] ?? null;
            // A row that declares an action matches only that mutation (§2.1); a
            // POST to the same path with any other `action` is not a route, and the
            // request ends as a 404 like any unknown path — the shell never offers a
            // mutation this table does not carry, so nothing else can answer it.
            if ($expected === null || hash_equals($expected, $request->action())) {
                return [$route, $pathParams];
            }
        }

        return null;
    }

    /**
     * Whether this request runs inside the PHP session — asked by the front controller
     * before it starts one. Only a route marked `'session' => false` runs without: the
     * public survey page, which GD-1 keeps outside the session altogether (§8.8) — no
     * cookie is issued and none is read. An unknown path keeps the session, so the 404
     * renders like every other page.
     */
    public static function needsSession(Request $request): bool
    {
        $matched = self::find($request);

        return $matched === null || self::usesSession($matched[0]);
    }

    /** @param array{session?: bool} $route */
    private static function usesSession(array $route): bool
    {
        return ($route['session'] ?? true) !== false;
    }

    /**
     * Path pattern matching with `{name}` placeholders (no regex from user input:
     * only the fixed placeholder syntax becomes a named group). Matched values are
     * written into `$pathParams`.
     */
    private static function matchesPattern(string $pattern, string $path, array &$pathParams): bool
    {
        $regex = preg_replace('/\{([a-zA-Z_][a-zA-Z0-9_]*)\}/', '(?P<$1>[^/]+)', $pattern);
        if ($regex === null) {
            return false;
        }

        $matches = [];
        if (preg_match('#^' . $regex . '$#', $path, $matches) !== 1) {
            return false;
        }
        // Keep the named groups only: offset 0 is the whole path.
        $pathParams = array_map('strval', array_filter(
            $matches,
            'is_string',
            ARRAY_FILTER_USE_KEY
        ));

        return true;
    }

    /**
     * Builds the controller for one route and calls its handler. The controller
     * receives the request with this route's placeholders attached, so a page reads
     * its `{id}` from the request and never re-parses the path.
     *
     * @param array{controller: class-string, handler: string, region?: string} $route
     * @param array<string, string> $pathParams
     */
    private function invoke(array $route, string $handler, array $pathParams = []): Response
    {
        $class = $route['controller'];
        $controller = new $class(
            $this->config,
            $this->request->withPathParams($pathParams),
            $this->api,
            $this->i18n,
            $this->view,
            $this->auth,
            $this->logger
        );

        try {
            return $controller->{$handler}();
        } catch (ApiException $e) {
            return $this->fromApiException($e, $route);
        }
    }

    /**
     * A failed API call becomes the page or the JSON failure the §3.4 table fixes.
     * The status passes through; the text is translated and never an internal.
     *
     * @param array{region?: string} $route
     */
    private function fromApiException(ApiException $e, array $route): Response
    {
        $wantsJson = $this->request->wantsJson() && ($route['region'] ?? null) !== null;
        $text = Messages::forApiException($this->i18n, $e);

        if ($wantsJson) {
            return Response::apiError($e->code(), $text, $e->status());
        }

        // A page-level failure: log the real reason, show the user the line.
        $this->logger->warn('page read failed', [
            'path' => $this->request->path(),
            'code' => $e->code(),
            'status' => $e->status(),
        ]);

        if ($e->status() === 403 || $e->status() === 404) {
            return $this->view->render('errors/denied', [
                'title' => $e->status() === 403
                    ? $this->i18n->t('error.forbidden.page_title')
                    : $this->i18n->t('error.not_found.page_title'),
                'body' => $text,
            ], ['layout' => 'shell', 'titleKey' => '']);
        }

        return $this->serverError();
    }

    /** The 500 page: no stack trace, no internals (REQ-CFG-005, REQ-API-006). */
    public function serverError(): Response
    {
        if ($this->request->wantsJson()) {
            return Response::apiError('internal', $this->i18n->t('error.generic'), 500);
        }

        try {
            return $this->view->render('errors/server', [
                'title' => $this->i18n->t('error.server.page_title'),
                'body' => $this->i18n->t('error.server.page_body'),
            ], ['layout' => null]);
        } catch (\Throwable) {
            // The view itself failed (often a missing template): answer plainly.
            return Response::html('<!doctype html><meta charset="utf-8"><title>Error</title>'
                . '<p>The application could not complete your request.</p>', 500);
        }
    }

    private function notFound(): Response
    {
        if ($this->request->wantsJson()) {
            return Response::apiError('not_found', $this->i18n->t('error.not_found'), 404);
        }

        return Response::html('<!doctype html><meta charset="utf-8"><title>Not found</title>'
            . '<p>The page you asked for does not exist.</p>', 404);
    }

    /**
     * A guard refusal keeps its shape: a browser gets the redirect, a data
     * request gets the API's own failure envelope so the client renders the §3.4
     * line instead of feeding HTML to response.json().
     */
    private function shapeRefusal(Response $denied): Response
    {
        if (!$this->request->wantsJson()) {
            return $denied;
        }

        return match ($denied->status()) {
            403 => Response::apiError('forbidden', $this->i18n->t('error.forbidden'), 403),
            404 => Response::apiError('not_found', $this->i18n->t('error.not_found'), 404),
            // An unauthenticated data request: the client treats a redirect to
            // login as "session over, go sign in" — so answer it as 403 rather
            // than let fetch() follow to an HTML page.
            default => Response::apiError('forbidden', $this->i18n->t('error.forbidden'), 403),
        };
    }
}
