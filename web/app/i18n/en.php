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
    // One button per OAuth2 provider under the selected name (§2.2, REQ-AUTH-066). The
    // provider is named by its issuer host: no variable carries a display name for a
    // provider itself, only the source names it may answer to.
    'login.provider.submit' => 'Sign in with {provider}',
    // A name whose sources all need a browser round trip has no password form to show.
    'login.no_credentials' => 'This institution signs in through one of the providers above.',
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

    // --- project home (User_Interface_Design.md §6.1, REQ-UI-017) ---
    'project.summary.records' => 'Records',
    'project.summary.instruments' => 'Instruments',
    'project.summary.fields' => 'Fields',
    'project.actions' => 'This project',
    'project.metadata' => 'Project details',
    // The read-only metadata block; editing it is §5.2.
    'project.field.project_name' => 'Project name',
    'project.field.organization' => 'Organization',
    'project.field.pi_name' => 'Principal investigator',
    'project.field.pi_email' => 'Principal investigator e-mail',
    'project.field.dm_name' => 'Data manager',
    'project.field.dm_email' => 'Data manager e-mail',
    'project.field.rek_number' => 'REK number',
    'project.field.rek_start_date' => 'REK start date',
    'project.field.rek_end_date' => 'REK end date',
    'project.field.start_date' => 'Start date',
    'project.field.end_date' => 'End date',
    'project.field.participant_names' => 'Participant name pattern',

    // --- two-factor settings (§2.5, REQ-UI-039) ---
    'tfa.title' => 'Two-factor authentication',
    'tfa.heading' => 'Two-factor sign-in',
    'tfa.current_method' => 'Current method',
    'tfa.method.off' => 'Off',
    'tfa.method.totp' => 'Authenticator app',
    'tfa.method.email' => 'Code by e-mail',
    'tfa.enrolled_on' => 'Enabled {when}',
    'tfa.codes_remaining' => '{count} recovery codes left',
    'tfa.enable_help' => 'A second factor keeps an account usable even after its password appears somewhere it should not. Choose one to enable.',
    'tfa.start_totp' => 'Use an authenticator app',
    'tfa.start_email' => 'Use a code by e-mail',
    'tfa.totp_step1' => 'Add the key below to your authenticator app.',
    'tfa.totp_step2' => 'Enter the six-digit code it shows to finish.',
    'tfa.manual_key' => 'Manual-entry key',
    'tfa.setup_link' => 'Or open this setup link in your app:',
    'tfa.copy' => 'Copy',
    'tfa.copied' => 'Copied',
    // A wrong confirmation code does not earn the key a second showing (§2.5).
    'tfa.key_shown_once' => 'The key was shown once and is not repeated here. Another code from your app still works; if you never added the key, cancel and start again.',
    'tfa.code' => 'Code',
    'tfa.confirm' => 'Confirm and enable',
    'tfa.cancel' => 'Cancel',
    'tfa.email_help' => 'Enter the code we sent to {email}. It is valid for a few minutes and works once.',
    'tfa.resend' => 'Send another code',
    'tfa.recovery_warning' => 'These recovery codes are shown once and cannot be shown again. Save them somewhere you can reach without your phone.',
    'tfa.saved' => 'I have saved my recovery codes',
    'tfa.enabled' => 'Two-factor authentication is on.',
    'tfa.disabled' => 'Two-factor authentication is off.',
    'tfa.disable_heading' => 'Turn two-factor off',
    // The mandate lives in the API’s configuration, which this page has no reason to read:
    // the sentence is conditional so it stays true either way (§2.5).
    'tfa.disable_help' => 'Turning it off asks for a current code or a recovery code first. If your installation requires two-factor sign-in, you will be asked to set it up again at your next sign-in.',
    'tfa.disable_code' => 'Current code or recovery code',
    'tfa.disable' => 'Turn two-factor off',
    'tfa.failure.code' => 'That code is wrong or has expired.',
    // The standing note where the e-mail option used to be, after its own attempt said why
    // it cannot work (REQ-AUTH-057).
    'tfa.email_unavailable' => 'Codes by e-mail are not available on this installation: it has no mail server configured.',
    'tfa.failure.no_email' => 'This installation cannot send e-mail, so a code by e-mail is not available. Use an authenticator app instead.',
    'tfa.failure.send_failed' => 'The code could not be sent. Try again shortly or use a recovery code.',

    // --- change own password (§2.6, REQ-AUTH-061) ---
    'account.password.title' => 'Change password',
    'account.password.heading' => 'Change your password',
    'account.password.address' => 'Signed in as',
    'account.password.current' => 'Current password',
    'account.password.new' => 'New password',
    'account.password.repeat' => 'Repeat the new password',
    // The policy the API applies (passwords.go, security finding F12) — stated where it can
    // be met rather than discovered in a rejection.
    'account.password.policy' => 'At least 12 characters.',
    'account.password.mismatch' => 'The two passwords do not match.',
    'account.password.changed' => 'Your password has been changed.',
    'account.password.no_local_credential' => 'This account signs in through an identity provider, so it has no local password to change.',
    'account.password.failure.missing' => 'Enter your current password and the new one twice.',
    'account.password.failure.mismatch' => 'The two new passwords do not match.',

    // --- forgot password (§2.6, GD-23, REQ-AUTH-062) ---
    'password_reset.title' => 'Forgot password',
    'password_reset.heading' => 'Reset your password',
    'password_reset.help' => 'Enter the e-mail address you sign in with and we will send a link to choose a new password.',
    'password_reset.local_only' => 'This works for accounts with a local password. An account that signs in through an identity provider resets its password there.',
    'password_reset.submit' => 'Send reset link',
    // The one sentence every outcome gets — nothing here may confirm or deny an account.
    'password_reset.sent' => 'If that address has an account with a local password, a reset link has been sent.',
    'password_reset.cancel' => 'Back to sign in',
    'password_reset.back_to_login' => 'Return to sign in',
    'password_reset.failure.missing' => 'Enter the e-mail address you sign in with.',

    // --- set password through an invite or reset token (§2.6, Sequence H) ---
    'set_password.title' => 'Set your password',
    'set_password.heading' => 'Choose a new password',
    'set_password.help' => 'Pick a password you have not used here before. It needs at least 12 characters.',
    'set_password.submit' => 'Set password',
    'set_password.done_heading' => 'Password set',
    'set_password.done_help' => 'You can sign in with your new password now.',
    'set_password.sign_in' => 'Sign in',
    'set_password.sign_in_instead' => 'Go to sign in',
    // One line for unknown, expired, consumed and wrong-purpose tokens alike (REQ-API-120).
    'set_password.invalid' => 'This setup link does not work. It may have expired, been used already, or belong to a different kind of request.',
    'set_password.request_new' => 'Request a new reset link',

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
    // The self-service password change (§2.6): the one line a rejected credential gets.
    'error.bad_password' => 'The current password is not correct.',
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
