# Authentication and Authorization Plan

This document outlines the security model for the clinical study management system, utilizing OAuth2 (with LDAP fallback and a table-based local-password path) for authentication — optionally extended by a per-user two-factor step (TOTP or email code — GD-21) on the CLARA-verified paths — and role-based access control (RBAC) for authorization.

## Authentication
- Protocol: OAuth2 (authorization code), with LDAP fallback and a local table-based path
- Provider: External OAuth2 Server(s); up to three, selected by configuration
- Flow:
    1. User initiates login and is redirected to the OAuth2 provider.
    2. Upon successful authentication, the provider returns an authorization code.
    3. The system exchanges the code for an access token and resolves the user by email.
    4. The **PHP web application** establishes its own session (PHP-native, `HttpOnly`/`Secure`/`SameSite=Lax` — REQ-AUTH-009); the Go API stays stateless and stores no session of its own (REQ-API-044). The user's API token (UUID) for a project comes from the user-project assignment.

### Identity Providers & LDAP Fallback
- Multiple Providers: The system will support generic OAuth2 integration with multiple identity providers (`OAUTH2_N_*`, REQ-CFG-011).
- LDAP Fallback: If OAuth2 fails or is unavailable, the system will query up to 3 configured LDAP servers sequentially.
- Local Password Accounts (GD-18): when no OAuth2 provider and no LDAP server is configured, accounts authenticate against `users.password_hash` (bcrypt), starting from a bootstrap administrator taken from configuration (`ADMIN_BOOTSTRAP_EMAIL`/`_PASSWORD`, REQ-CFG-025). Accounts carry a validity period and are auto-disabled after a configurable inactivity period (GD-19, default 180 days; re-enableable by an administrator).
- Two-Factor Authentication (GD-21): each user MAY enable a second factor — `totp` (RFC 6238 authenticator app on the user's phone; works offline) or `email` (one-time code via a configured SMTP relay). It is verified after first-factor success on the local and LDAP form logins, before any session is established; the OAuth2 path leaves MFA to the identity provider. Self-service enrollment with one-time recovery codes; an installation-wide mandate (`AUTH_REQUIRE_2FA`) forces enrollment; administrators reset it per account (REQ-AUTH-054…059).
- Identity only, not authorization: the identity provider or directory supplies the **identity** (email, display name) — never roles or permissions. Project roles are created and assigned per project by an administrator inside this system (REQ-AUTH-020/021); the earlier notion of mapping directory group attributes to system permissions was not taken up (ASM-AUTH-3).

## Authorization
- Model: Role-Based Access Control (RBAC); roles are per-project collections of permissions, created by an administrator with any name and any combination (REQ-AUTH-021).
- One (external, OAuth or LDAP) role for general access to web-interface (no administration tools, no project access, info page only with instructions on how to apply)
- Permissions are held **per arm** as two ordered levels (GD-2, REQ-AUTH-017/018):
    - **data access level**: `no_access` < `read_only` < `view_edit` < `delete` < `edit_survey_responses`
    - **export level**: `export_none` < `export_de_identified` < `export_no_identifiers` < `export_full`
  plus the project-level `project_admin` flag for study design and setup. An arm with no explicit grant means no access — never an implicit one.
  The earlier flat permission set (`view`, `change`, `add`; `export all`, `export anonymized`, `export only non-sensitive data`) is **superseded** by these levels; there are four export levels, not three, and "non-sensitive" is not one of them (DEV-AUTH-5).
- Roles (suggested presets, not a fixed catalogue — REQ-AUTH-020):
    - data-manager: per arm `delete` + `export_full`, plus `project_admin`.
    - data-entry: per arm `view_edit` + `export_none`.
    - controller: per arm `read_only` + `export_none`.
    - Custom roles: any combination of the two levels per arm, optionally with `project_admin`.
- Data Access Groups (GD-10): a project MAY have groups; a member assigned to one or more has exactly one active and sees only that group's records, while a member with no group sees all of them. A record belongs to exactly one group or none, taken from its creator's active group.
- Project Visibility: A project is visible to a user only if the user is in the administrator group or is a member of the project.
- Role-less Members: All members of a project that are not assigned a project role have full permissions for this project only.
- UI Gating: The interface only presents objects and sub-pages if the role/permission of the user allows them to use them — hidden means absent from the DOM, not merely disabled (REQ-AUTH-027). E.g. no admin interface for non-admin user.

## Token Management
- API tokens (UUID) are mapped to user accounts and specific projects (user-project assignment). Generated in the admin interface.
- The token is passed as the `token` parameter in the API request body (REDCap-compatible protocol), not in an Authorization header.
