# Authentication & Authorization — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Authentication_Authorization.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

Defines the authentication (OAuth2, LDAP fallback, and table-based local-password authentication — GD-18), account validity and inactivity rules (GD-19), and authorization (RBAC, per-arm permission levels) requirements. `Design/Authentication_Authorization_Design.md` contains the login sequences, session schema, and trust model.

## 2. Authentication Requirements

| ID | Requirement |
|---|---|
| REQ-AUTH-001 | The system MUST authenticate users against an external OAuth2 server using the authorization-code flow: redirect to provider → code returned → code exchanged for a token → local session established. |
| REQ-AUTH-002 | The system MUST support multiple OAuth2 providers (generic integration) selected by configuration (REQ-CFG-011). |
| REQ-AUTH-003 | If OAuth2 fails or is unavailable, the system MUST query up to 3 configured LDAP servers sequentially (server 1, then 2, then 3; stop at first successful bind+bind-verified bind). |
| REQ-AUTH-004 | The user's identity MUST be mapped to a system user by email address from the provider assertion, the LDAP directory attributes, or the local login's email (institutional email is the identity for all paths, master spec "Details"). |
| REQ-AUTH-005 | The successful authentication source MUST be recorded on the user record (`auth_source` = `oauth2`|`ldap`|`local`) and in the audit log. |
| REQ-AUTH-006 | A user account MUST exist and be **active** for login to succeed: `enabled = 1`, not expired (`valid_until`, REQ-AUTH-052), and not inactive (REQ-AUTH-053). Otherwise the login is rejected with the specific reason (`account_not_found` / `account_disabled` / `account_expired`) and the failure is audit-logged. The same active rule applies at every authentication check (login, administration-API calls, data-API token checks — REQ-API-048). |
| REQ-AUTH-007 | On first login of the bootstrap admin email (`ADMIN_BOOTSTRAP_EMAIL`), the API MUST create/enable the account with `is_admin = true` (GD-4); with table-based authentication (GD-18) the bootstrap account's local password is provisioned as defined in REQ-AUTH-051. |
| REQ-AUTH-008 | Login attempts (success and failure) and logouts MUST be audit-logged (see `Audit_Logging_Requirements.md`). |

### 2.1 Table-based (local) authentication (decision GD-18)

| ID | Requirement |
|---|---|
| REQ-AUTH-050 | The system MUST support a third authentication path: **table-based (local) login** with the user's email and password, verified against the account's stored bcrypt hash (`users.password_hash`, REQ-DB-008). A local password is the **only** credential the system MAY persist — never plaintext, never logged; the OAuth2 and LDAP paths remain passwordless (REQ-AUTH-036 revised). |
| REQ-AUTH-051 | Local login MUST be attempted **first** on the email+password form (no external dependency), before the LDAP fallback of REQ-AUTH-003; a local failure (no stored hash or hash mismatch) falls through to LDAP when configured. The bootstrap admin (GD-4) MUST be usable on a first installation without any OAuth2 provider or LDAP server: when neither is configured, the API MUST provision the bootstrap account's local password from `ADMIN_BOOTSTRAP_PASSWORD` (configuration, REQ-CFG-025), and the system MUST refuse to start in that configuration without it (startup rule, `System_Configuration_Design.md` §4.1). An administrator MAY set, reset, or clear a user's local password through the users API (REQ-API-047/048); the password value MUST NOT appear in logs or audit details. |
| REQ-AUTH-052 | **Account validity:** an account MAY carry a validity period, stored as the end date `valid_until` (date, UTC; `NULL` = indefinite). The period is supplied as days (`0` = indefinite) at account creation or update (REQ-API-047/048); `valid_until = today + valid_days`. An account whose `valid_until` is before today MUST be rejected at every authentication check with `account_expired` (the account stays enabled; an administrator restores access by setting a new validity period). |
| REQ-AUTH-053 | **Inactivity auto-disable:** `last_login_at` (UTC) MUST be set on every successful login (any source). At an authentication check, an account whose `last_login_at` is older than `AUTH_INACTIVITY_LIMIT_DAYS` (configuration, REQ-CFG-024; default 180; `0` = rule off) MUST be **auto-disabled** — `enabled` set to `0`, the inactivity clock reset — the check MUST be rejected as `account_disabled`, and the event MUST be audit-logged as `account_auto_disabled` (REQ-AUD-024). An administrator MUST be able to re-enable such an account (REQ-API-048); re-enabling MUST reset the inactivity clock (the account is then usable until its next login ages out again). The user overview MUST show last login, validity end, and the account's status (REQ-UI-011). |

### 2.2 Session (decision GD-1)

| ID | Requirement |
|---|---|
| REQ-AUTH-009 | The session MUST be owned by the PHP web application (PHP-native session, `HttpOnly`/`Secure`/`SameSite=Lax` cookie). The session MUST store at least: user id, email, display name, `is_admin`, authentication source, and issue time. |
| REQ-AUTH-010 | The browser MUST NOT call the administration API (`/api/v1/*`) directly; all administration API calls are made server-side by PHP with the session context. |
| REQ-AUTH-011 | PHP MUST identify itself to the API with the shared service token and the user id: headers `X-Internal-Service-Token` (shared secret) and `X-Internal-User-Id`. The API MUST reject `/api/v1/*` calls lacking a valid service token. |
| REQ-AUTH-012 | The API MUST compare the service token in constant time and MUST NOT log its value. |
| REQ-AUTH-013 | The API MUST treat `X-Internal-User-Id` as authoritative for authorization on `/api/v1/*`; the user MUST exist and be enabled, else the call is rejected (403) and audit-logged. |
| REQ-AUTH-014 | The reverse proxy MUST strip `X-Internal-Service-Token` and `X-Internal-User-Id` from all externally-originated requests; `/api/v1/*` MUST not be routable from the public network (REQ-TECH-018). |
| REQ-AUTH-015 | Sessions MUST expire after a configurable inactivity timeout (default 8 h) and on explicit logout. Logout MUST call `POST /api/v1/auth/logout` (audit) before destroying the session. |
| REQ-AUTH-016 | `POST /api/v1/auth/login` (called by PHP after successful IdP authentication, **or with `source: "local"` and the password for table-based login** — REQ-AUTH-050) MUST: for local login, verify the stored hash in constant time before any other step (mismatch or absent hash → `bad_password`, no disclosure difference); then verify the user row exists and is active (REQ-AUTH-006), apply the bootstrap-admin promotion (REQ-AUTH-007), record a login audit event with the source, set `last_login_at`, and return the user object including `is_admin`. The local password MUST NOT be logged or stored anywhere except as the bcrypt hash. |

## 3. Authorization Requirements (RBAC)

### 3.1 Permission Set (decision GD-2)

| ID | Requirement |
|---|---|
| REQ-AUTH-017 | The permission set MUST be exactly (GD-2, revised 2026-09-19): per arm — a **data access level** (`no_access`, `read_only`, `view_edit`, `delete`, `edit_survey_responses`) and an **export level** (`export_none`, `export_de_identified`, `export_no_identifiers`, `export_full`); plus the project-level `project_admin`. An arm not listed in a role defaults to `no_access` / `export_none` (no implicit access, REQ-AUTH-019). The former seven permissions (view/change/add/export_*) are superseded by these levels. |
| REQ-AUTH-018 | Permission semantics (per arm, ordered — a higher level includes everything below): **data access** — `no_access` = the arm and its data are hidden (absent from the UI, REQ-AUTH-027; rejected by the API); `read_only` = read the arm's record values and the project structure; `view_edit` = additionally enter and change record values on the arm; `delete` = additionally delete values/records on the arm; `edit_survey_responses` = additionally modify responses collected via a survey link (GD-9). **Export** — `export_none` = no export of the arm's data; `export_de_identified` = direct identifiers removed, personal fields hashed, dates shifted (`Data_Export_Anonymization_Requirements.md`); `export_no_identifiers` = all identifier fields removed; `export_full` = full dataset. **Project** — `project_admin` = modify project structure and metadata. |
| REQ-AUTH-019 | Every API operation and every UI action MUST require an explicit permission (see mapping tables in the API and UI requirement documents); there MUST be no implicit access. |

### 3.2 Roles

| ID | Requirement |
|---|---|
| REQ-AUTH-020 | Roles are defined per project, and a project MAY have none at all (role-less members then hold full permissions, REQ-AUTH-022). `data-manager` (suggested: per arm `delete` + `export_full`, plus `project_admin`), `data-entry` (per arm `view_edit` + `export_none`), and `controller` (per arm `read_only` + `export_none`) are examples/presets that a project MAY create — they are not mandatory built-ins; a project MAY create its own variants with different names, arms, and level combinations (DEV-AUTH-4, DEV-AUTH-5). |
| REQ-AUTH-021 | An administrator MUST be able to create roles per project with any name and any combination of per-arm data access and export levels (REQ-AUTH-017), optionally with `project_admin`; role creation MUST NOT be limited to the example presets of REQ-AUTH-020. |
| REQ-AUTH-022 | A project member without an assigned role MUST hold the highest levels on every arm (`edit_survey_responses` + `export_full`) plus `project_admin`, for that project only (master spec, BR-002). |
| REQ-AUTH-023 | A user with `is_admin = true` MUST hold all permission levels on every arm of every project (including `project_admin`) and MUST see every project in the dashboard (BR-010). |
| REQ-AUTH-024 | Roles are project-scoped: a role on project A grants nothing on project B. |
| REQ-AUTH-025 | Assigning a role to a member MUST replace the member's effective permissions for that project (no permission union across multiple roles in phase 1). |

### 3.3 Project Visibility and UI Gating

| ID | Requirement |
|---|---|
| REQ-AUTH-026 | A project MUST be visible to a user only if the user is `is_admin` or is a member of the project. |
| REQ-AUTH-027 | The UI MUST present a page, section, or action only if the user's effective permission allows it (e.g. no admin interface for non-admins; no Setup action without `project_admin`). Hidden means absent from the DOM, not merely disabled. |
| REQ-AUTH-028 | A user who is not a member of any project and not an admin MUST land on an information page explaining how to apply for project access (authentication plan, "info page only with instructions"). |

### 3.4 API Tokens

| ID | Requirement |
|---|---|
| REQ-AUTH-029 | Each (user, project) assignment MUST carry one token (UUID, GD-5). Tokens MUST be globally unique. |
| REQ-AUTH-030 | Tokens MUST be created and rotatable from the admin interface (member assignment screen); rotation MUST invalidate the previous token immediately and be audit-logged. |
| REQ-AUTH-031 | The REDCap-compatible API MUST accept the token only as the `token` request-body parameter (REDCap protocol), not in an Authorization header (GD-5). |
| REQ-AUTH-032 | Token validation MUST be a single indexed lookup (token → user, project, role); an invalid token MUST produce the REDCap-style error response without disclosing whether the token exists. |
| REQ-AUTH-033 | Token permissions MUST be the effective permissions of the (user, project) assignment at call time (role changes take effect immediately, no token refresh needed). |

### 3.5 Surveys (decision GD-9)

| ID | Requirement |
|---|---|
| REQ-AUTH-038 | An instrument MAY be marked as a survey (`is_survey`, REQ-DB-011); ONLY survey-marked instruments can be filled out via a public survey link (GD-9). |
| REQ-AUTH-039 | A survey link MUST be a stable, record-specific public web URL carrying an opaque link token; it MUST grant a person without a login fill-only access to exactly that (record, survey instrument): read the instrument's field definitions for that record, and submit or change values for it. It MUST grant nothing else (no other record or instrument, no export, no structure, no administration API). |
| REQ-AUTH-040 | Link tokens MUST be revocable; revocation MUST take effect immediately for all further calls and MUST be audit-logged (REQ-AUD-021). |
| REQ-AUTH-041 | Survey link submissions MUST pass the same field validation as any other write (REQ-VAL-001/004) and MUST be audit-logged with the link token as the acting principal (REQ-AUD-021). |
| REQ-AUTH-042 | A member modifying survey-originated responses in the UI requires the data access level `edit_survey_responses` on the arm (GD-2); the respondent's own re-submission through the link is not restricted by member permissions. |

### 3.6 Data Access Groups (decision GD-10)

| ID | Requirement |
|---|---|
| REQ-AUTH-043 | A project MAY have none, one, or several data access groups; a group name MUST be unique within the project (GD-10, REQ-DB-028). |
| REQ-AUTH-044 | A member MAY be assigned to none, one, or several groups of the project; with one or more assigned, exactly one MUST be active. Assignments are managed by `is_admin` (membership management, REQ-API-054 context); a member's own active group is visible to and switchable by the member (REQ-AUTH-046). |
| REQ-AUTH-045 | Visibility rule: a member with an active group G MUST see and access (read, export, record status, history) only the records whose data access group is G; a member without a group MUST see all records of the project regardless of their assignment; an `is_admin` user sees all records (REQ-AUTH-023). The rule is orthogonal to the permission levels (GD-2): the level governs which actions are allowed, the group governs which records they apply to. |
| REQ-AUTH-046 | A member with one or more groups MUST be able to switch the active group (self-service); the switch MUST take effect immediately for subsequent calls and MUST be audit-logged (REQ-AUD-022). |
| REQ-AUTH-047 | A record created by a member MUST be assigned to the creator's active group, or to no group if the creator has none; a record holds exactly one group (REQ-DB-029); a later import into the record MUST NOT change its group. |
| REQ-AUTH-048 | A record's group MUST be assignable or changeable after creation (GD-10); the action requires the project-level permission `project_admin` (ASM-AUTH-4) and MUST be audit-logged (REQ-AUD-022). |

## 4. Non-Functional Security Requirements

| ID | Requirement |
|---|---|
| REQ-AUTH-034 | All inter-component traffic between the web application and the API MUST be over a trusted path (loopback or internal network) or TLS. |
| REQ-AUTH-035 | Brute-force protection: after 5 failed logins for the same email within 10 minutes, further attempts for that email MUST be rejected for 15 minutes (per host, in memory or session store); failures are audit-logged. |
| REQ-AUTH-036 | User passwords MUST NOT be stored in plaintext and MUST NOT appear in logs or audit details. The **only** persisted credential is the one-way bcrypt hash of a local (table-based) account's password (`users.password_hash`, GD-18, REQ-AUTH-050); the OAuth2 and LDAP paths remain passwordless (the LDAP password goes to the directory and is never persisted). |
| REQ-AUTH-037 | CSRF protection: all state-changing browser requests MUST carry a per-session CSRF token; the API's admin surface is additionally protected by the service-token boundary (GD-1). |
| REQ-AUTH-049 | The reverse proxy and web server MUST NOT log query strings of `/api/` requests (the bearer token may ride in them per REQ-API-009); token values in access logs MUST be redacted (REQ-API-005). |

## 5. Assumptions

| ID | Assumption |
|---|---|
| ASM-AUTH-1 | The OAuth2 provider exposes standard authorization-code endpoints (authorize, token, userinfo or ID token with email). Provider-specific quirks are out of scope beyond generic OAuth2. |
| ASM-AUTH-2 | LDAP servers accept simple bind for authentication (bind-as-user); directory reads use a configured bind DN. |
| ASM-AUTH-3 | The "administrator group" of the master spec is realized by `is_admin` (GD-4), not by an IdP group. |
| ASM-AUTH-4 | Data access group details: a record's group is per `record_id` (across all its events); a member with one or more groups always has exactly one active; a record created by a member without a group is unassigned (visible only to members without a group and to administrators); reassignment requires `project_admin` (REQ-AUTH-048); deleting a group that still has assigned records is rejected. |
| ASM-AUTH-5 | The authorization decision is a single explicit function over attributes — subject, `is_admin`, role, arm, data access level, export level, active data access group (REQ-AUTH-045) — i.e. an ABAC-style evaluation implemented in the Go API; no external policy engine is required, and every decision stays explicit and auditable (REQ-AUTH-019). |

## 6. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-AUTH-1 | (superseded by DEV-AUTH-5) `export_non_sensitive` added to the permission set | Decision GD-2 (user-confirmed: seven permissions); the seven-permission model was reworked into per-arm levels on 2026-09-19. |
| DEV-AUTH-2 | Session ownership fixed to the PHP layer; API is stateless w.r.t. admin sessions | Decision GD-1 (user-confirmed). The plan's `POST /api/v1/auth/login` is re-interpreted as a PHP→API call establishing user state + audit, not a browser-facing session endpoint. |
| DEV-AUTH-3 | Suggested permission sets recorded for the example presets | The plan names roles but not their permission sets; suggested defaults are documented in REQ-AUTH-020, where the presets are optional. |
| DEV-AUTH-4 | The named roles are optional presets, not mandatory built-ins | Owner decision (2026-09-19): `data-entry`/`data-manager`/`controller` are examples; a project MAY define none or different versions, and permissions MUST be matchable to any defined role (REQ-AUTH-021). |
| DEV-AUTH-5 | Permission model reworked to per-arm levels (data access + export), superseding the seven atomic permissions | Owner decision (2026-09-19): make GD-2 more explicit — per-arm data viewing levels (No Access/hidden, Read Only, View & Edit, Delete, Edit survey responses) and export levels (no access, de-identified, remove all identifier fields, full dataset). |
| DEV-AUTH-6 | Surveys in scope in limited form (link-filled survey instruments), narrowing the charter's survey out-of-scope | Owner decision (2026-09-19, GD-9): instruments marked as surveys are fillable via a record-specific public link without login. |
| DEV-AUTH-7 | Data access groups added (record-level visibility grouping, orthogonal to the permission levels) | Owner decision (2026-09-19, GD-10): projects MAY have groups; a member's active group scopes the visible records; group-less members see all records. |
| DEV-AUTH-8 | Table-based (local password) authentication added as a third login path; `REQ-AUTH-036` revised from "no password storage" to "no plaintext storage; bcrypt hash only" | Owner decision (2026-09-22, GD-18; master spec "Details"): first-install usability without an IdP; bootstrap admin password from `ADMIN_BOOTSTRAP_PASSWORD`; local tried first on the password form, LDAP remains the fallback (REQ-AUTH-050/051). |
| DEV-AUTH-9 | Account validity period and inactivity auto-disable added (GD-19) | Owner decisions (2026-09-22; master spec "Details"): accounts valid for a limited time in days (`0` = indefinite); auto-disable after 180 days without login; admin re-enable; status visible on the user overview (REQ-AUTH-052/053). |
