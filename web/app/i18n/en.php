<?php
// The English UI catalog — the application's source of truth for its own strings
// (REQ-DB-031). Other languages hold only overrides, which the API serves from
// i18n_strings and I18n overlays on top of this file; a key with no override
// renders in English rather than blank (REQ-UI-008).
//
// Keys are dotted lowercase ([a-z0-9._-]{1,64}) so the translation management
// page (§5.7) can list them and an editor can read them. Placeholders are
// {braced}. Every key a page uses must exist here — I18n::t() throws in
// development when it does not. Author entries as pages land; do not translate
// them by hand into the other languages, that is what §5.7 is for.

declare(strict_types=1);

return [
    // --- application chrome ---
    'app.name' => 'CLARA',
    'app.tagline' => 'Clinical study data capture',
    'nav.projects' => 'Projects',
    'nav.dashboard' => 'Dashboard',
    'nav.administration' => 'Administration',
    'nav.users' => 'User accounts',
    'nav.project_list' => 'Projects',
    'nav.audit' => 'Audit log',
    'nav.translations' => 'Translations',
    'nav.settings' => 'System settings',
    'nav.account' => 'Account',
    'nav.two_factor' => 'Two-factor authentication',
    'nav.password' => 'Password',
    'nav.sign_out' => 'Sign out',
    'nav.skip_to_content' => 'Skip to main content',
    'language.label' => 'Language',
    'theme.label' => 'Theme',
    'theme.default' => 'Default',
    'theme.bootstrap' => 'Standard',
    'theme.darkly' => 'Darkly',
    'theme.yeti' => 'Yeti',
    'action.save' => 'Save',
    'action.cancel' => 'Cancel',
    'action.close' => 'Close',

    // --- login (User_Interface_Design.md §2.2) ---
    'login.title' => 'Sign in',
    'login.heading' => 'Sign in to CLARA',
    'login.source' => 'Where are you signing in from?',
    'login.source_help' => 'Choose your institution, then enter your credentials.',
    'login.email' => 'E-mail address',
    'login.password' => 'Password',
    'login.submit' => 'Sign in',
    'login.forgot' => 'Forgot password?',
    'login.failure.credentials' => 'That e-mail address and password combination is not recognised.',
    'login.failure.disabled' => 'This account is disabled — an administrator must enable it again before you can sign in.',
    'login.failure.expired' => 'This account’s validity period has ended — an administrator must extend it.',
    'login.failure.mfa_code' => 'That code is wrong or has expired.',
    'login.failure.provider_unavailable' => 'The sign-in service could not be reached. Please try again shortly.',
    'login.failure.state_mismatch' => 'The sign-in attempt was interrupted. Please try again.',
    // The second factor exists in the API but this build has no challenge panel yet
    // (M1's remainder); saying so beats reporting a wrong password.
    'login.mfa_unsupported' => 'This account uses two-factor authentication, which this version of the interface cannot complete yet. An administrator can reset it for you.',

    // --- dashboard (User_Interface_Design.md §4) ---
    'dashboard.title' => 'Your projects',
    'dashboard.project.records' => 'Records',
    'dashboard.project.instruments' => 'Instruments',
    'dashboard.project.fields' => 'Fields',
    'dashboard.project.open' => 'Open project',

    // --- no-access page (§2.3, REQ-UI-006) ---
    'no_access.title' => 'No projects yet',
    'no_access.body' => 'You are signed in, but you are not a member of any project yet. An administrator adds members to a project; once that is done the project appears here.',
    'no_access.admin_title' => 'Nothing has been set up yet',
    'no_access.admin_body' => 'Create a project and add user accounts to get started.',
    'no_access.create_project' => 'Create a project',
    'no_access.manage_users' => 'Manage user accounts',

    // --- error pages and the §3.4 message table ---
    'error.generic' => 'Something went wrong. Please try again; if it continues, contact your administrator.',
    'error.invalid_request' => 'The form contains invalid values',
    'error.forbidden' => 'You do not have permission for this action',
    'error.not_found' => 'The object does not exist or you do not have access',
    // The form was accepted but the request did not carry this session's token:
    // nothing was sent to the API (§3.3).
    'error.csrf' => 'Your session has changed since this page was rendered. Please try again.',
    'error.rate_limited' => 'Too many attempts — please wait a moment and try again.',
    'error.rate_limited_seconds' => 'Too many attempts — please wait {seconds} seconds and try again.',
    'error.not_found.page_title' => 'Page not found',
    'error.not_found.page_body' => 'The page you asked for does not exist.',
    'error.forbidden.page_title' => 'Not allowed',
    'error.forbidden.page_body' => 'You do not have permission to open this page.',
    'error.server.page_title' => 'Service unavailable',
    'error.server.page_body' => 'The application could not complete your request. Your administrator has been notified.',

    // --- strings the browser modules show (injected via the data-i18n block) ---
    'js.loading' => 'Loading…',
    'js.load_failed' => 'The list could not be loaded.',
    'js.retry' => 'Retry',
];
