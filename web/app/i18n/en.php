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
    // The proof behind a challenge lapsed (REQ-API-131): not a wrong code, and
    // the only honest instruction is to start again.
    'login.failure.expired_session' => 'That sign-in attempt has expired. Please sign in again.',

    // --- two-factor challenge at login (§2.2, REQ-UI-038) ---
    'login.mfa.title' => 'Second factor',
    'login.mfa.heading' => 'Confirm it is you',
    'login.mfa.code' => 'Code',
    'login.mfa.submit' => 'Sign in',
    'login.mfa.help_totp' => 'Enter the code from your authenticator app.',
    'login.mfa.help_email' => 'We sent a code to {email}.',
    'login.mfa.help_recovery' => 'Enter one of your recovery codes. Each works once.',
    'login.mfa.use_recovery_code' => 'Use a recovery code instead',
    'login.mfa.use_app_code' => 'Use the code from your app',
    'login.mfa.resend' => 'Send another code',
    'login.mfa.sent' => 'A new code is on its way to your e-mail address.',
    'login.mfa.abort' => 'Sign in as somebody else',

    // --- two-factor enrollment wizard (§2.7, REQ-UI-039) ---
    'login.enroll.title' => 'Set up two-factor sign-in',
    'login.enroll.heading' => 'Set up your second factor',
    'login.enroll.help' => 'This installation requires a second factor before you can sign in. Choose one — you will finish with a code from it.',
    'login.enroll.totp' => 'Use an authenticator app',
    'login.enroll.email' => 'Use a code by e-mail',
    'login.enroll.totp_step1' => 'Add the key below to your authenticator app.',
    'login.enroll.totp_step2' => 'Enter the six-digit code it shows to finish.',
    'login.enroll.manual_key' => 'Manual-entry key',
    'login.enroll.setup_link' => 'Or open this setup link in your app:',
    'login.enroll.code' => 'Code',
    'login.enroll.confirm' => 'Confirm and enable',
    'login.enroll.email_help' => 'Enter the code we sent to {email}. It is valid for a few minutes and works once.',
    'login.enroll.done_help' => 'Two-factor sign-in is on. One more step: enter a current code to finish signing in.',
    'login.enroll.recovery_warning' => 'These recovery codes are shown once and cannot be shown again. Save them somewhere you can reach without your phone.',
    'login.enroll.saved' => 'I have saved my recovery codes',

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
