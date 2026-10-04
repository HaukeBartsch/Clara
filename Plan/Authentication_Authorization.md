# Authentication and Authorization Plan

This document outlines the security model for the clinical study management system, utilizing **named authentication sources** — each resolving internally to an OAuth2 provider, an LDAP(s) server, or a table-based local-password path — for authentication (master spec "Authentication order", DEV-AUTH-14), optionally extended by a per-user two-factor step (TOTP or email code — GD-21) on the CLARA-verified paths, and role-based access control (RBAC) for authorization.

## Authentication
- Protocol: OAuth2 (authorization code), LDAP(s), and a local table-based path — each configured as a **named authentication source** the user selects before login
- Provider: External OAuth2 Server(s); selected by the user's source-name choice, not by an implicit fallback chain
- Flow:
    1. User opens the login page and **selects an authentication source by name** (e.g. "Hospital 1", "Hospital 2" — REQ-AUTH-063). Selecting a name selects its whole set of sources; credentials are sent only to those.
    2. Credential-checked sources under the selected name (local, LDAP) are tried **in parallel**; the first "login ok" response wins (REQ-AUTH-065). A name mapping to an OAuth2 source continues into that provider's authorization-code flow instead: the browser is redirected to the OAuth2 provider.
    3. Upon successful authentication, the provider returns an authorization code; the system exchanges it for an access token and resolves the user by email.
    4. The **PHP web application** establishes its own session (PHP-native, `HttpOnly`/`Secure`/`SameSite=Lax` — REQ-AUTH-009); the Go API stays stateless and stores no session of its own (REQ-API-044). The user's API token (UUID) for a project comes from the user-project assignment.

### Named Authentication Sources (authentication order)
- Names ↔ sources are **many-to-many**: several sources MAY share one name, and one source MAY carry several names; the login page shows the distinct set of names (REQ-AUTH-064). With exactly one name configured the picker is skipped (REQ-AUTH-067).
- **Parallel first-success:** on an email+password login all local/LDAP sources under the selected name are attempted simultaneously; later or losing responses are discarded — no second session, no duplicate audit success. An account-state rejection (`account_disabled`/`account_expired`) is surfaced when nothing succeeds (REQ-AUTH-065).
- **OAuth2 stays interactive:** a provider cannot be probed without a browser round-trip, so it is selected and redirected to, not raced (REQ-AUTH-066).
- **Containment:** credentials entered under a name never travel to sources registered under a different name — an improvement over the old chain, which sent a failed local password onward to every configured LDAP server.
- The selected name and winning source are recorded in the login audit event and session (REQ-AUTH-067).

### Identity Providers & LDAP
- Multiple Providers: The system will support generic OAuth2 integration with multiple identity providers (`OAUTH2_N_*`, REQ-CFG-011), each registered under one or more source names.
- LDAP: up to 3 configured servers, tried in parallel within the selected name's set rather than as a sequential fallback (REQ-AUTH-003, DEV-AUTH-14).
- Local Password Accounts (GD-18): when no OAuth2 provider and no LDAP server is configured, accounts authenticate against `users.password_hash` (bcrypt), starting from a bootstrap administrator taken from configuration (`ADMIN_BOOTSTRAP_EMAIL`/`_PASSWORD`, REQ-CFG-025). Accounts carry a validity period and are auto-disabled after a configurable inactivity period (GD-19, default 180 days; re-enableable by an administrator).
- System Administrator Assignment: administrators grant or revoke `is_admin` on existing accounts through the users API (`PUT /api/v1/users/{id}`, REQ-API-136); at least one enabled administrator always exists — revoking or disabling the last one is rejected (REQ-AUTH-068). The bootstrap account is a setup mechanism only: login-time promotion creates the row when absent, never re-enables a disabled account, and promotes an existing row only on a local login or while no other enabled admin exists (REQ-AUTH-007, DEV-AUTH-15).
- Account Onboarding and Password Lifecycle (GD-22/GD-23): an administrator creating a local account without a password can send an **invitation** — an email with a single-use, expiring set-password link (`AUTH_PASSWORD_TOKEN_TTL_DAYS`, default 7; REQ-AUTH-060); unavailable without SMTP, where the admin sets the password directly. Users change their own local password self-service by proving the current one (REQ-AUTH-061); "forgot password" issues an emailed single-use reset link that takes the new password directly — no temporary passwords, and a response identical whether or not the account exists, so nothing is disclosed (REQ-AUTH-062). Tokens are stored as one-way hashes only (`password_tokens`, REQ-DB-039). The email address is the identity on every path (REQ-AUTH-004) and changes only by an administrator; the reference app's access-application form stays out of scope (DEV-AUTH-12).
- Two-Factor Authentication (GD-21): each user MAY enable a second factor — `totp` (RFC 6238 authenticator app on the user's phone; works offline) or `email` (one-time code via a configured SMTP relay). It is verified after first-factor success on the local and LDAP form logins, before any session is established; the OAuth2 path leaves MFA to the identity provider. Self-service enrollment with one-time recovery codes; an installation-wide mandate (`AUTH_REQUIRE_2FA`) forces enrollment; administrators reset it per account (REQ-AUTH-054…059).
- Identity only, not authorization: the identity provider or directory supplies the **identity** (email, display name) — never roles or permissions. Project roles are created and assigned per project by an administrator inside this system (REQ-AUTH-020/021); the earlier notion of mapping directory group attributes to system permissions was not taken up (ASM-AUTH-3).

## Authorization
- Model: Role-Based Access Control (RBAC); roles are per-project collections of permissions, created by an administrator with any name and any combination (REQ-AUTH-021).
- One (external, OAuth or LDAP) role for general access to web-interface (no administration tools, no project access, info page only with instructions on how to apply)
- Permissions are held **per arm as a default and per (instrument, event) pair as an override** (GD-2, REQ-AUTH-017/018/069):
    - **data access level**: `no_access` < `read_only` < `view_edit`
    - **export level**: `export_none` < `export_de_identified` < `export_no_identifiers` < `export_full`
    - two **rights**, independent of both ladders: **delete instrument values** and **edit collected surveys** (REQ-AUTH-070)
  plus the project-level `project_admin` flag for study design and setup. An arm with no explicit grant means no access — never an implicit one; a pair with no grant of its own inherits its arm, and a pair created by a design change is materialized as `read_only` + `export_none` with both rights unset (REQ-AUTH-069).
  The earlier flat permission set (`view`, `change`, `add`; `export all`, `export anonymized`, `export only non-sensitive data`) is **superseded** by these levels; there are four export levels, not three, and "non-sensitive" is not one of them (DEV-AUTH-5). `delete` and `edit_survey_responses` were rungs of the data access ladder until 2026-10-03 and are now the two rights.
- Roles (suggested presets, not a fixed catalogue — REQ-AUTH-020):
    - data-manager: `view_edit` + **delete instrument values** + `export_full`, plus `project_admin`.
    - data-entry: `view_edit` + `export_none` on every pair, both rights unset.
    - controller: `read_only` + `export_none` on every pair, both rights unset.
    - Custom roles: any combination of the levels and rights per arm and pair, optionally with `project_admin`; naming a role is how a new one is created and the whole set stays editable afterwards (REQ-AUTH-021, REQ-API-143).
- Data Access Groups (GD-10): a project MAY have groups; a member assigned to one or more has exactly one active and sees only that group's records, while a member with no group sees all of them. A record belongs to exactly one group or none, taken from its creator's active group.
- Project Visibility: A project is visible to a user only if the user is in the administrator group or is a member of the project.
- Role-less Members: All members of a project that are not assigned a project role have full permissions for this project only.
- UI Gating: The interface only presents objects and sub-pages if the role/permission of the user allows them to use them — hidden means absent from the DOM, not merely disabled (REQ-AUTH-027). E.g. no admin interface for non-admin user.

## Token Management
- API tokens (UUID) are mapped to user accounts and specific projects (user-project assignment). Generated in the admin interface.
- The token is passed as the `token` parameter in the API request body (REDCap-compatible protocol), not in an Authorization header.
