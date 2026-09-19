# Authentication & Authorization — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Authentication_Authorization.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

Defines the authentication (OAuth2 with LDAP fallback) and authorization (RBAC, seven permissions) requirements. `Design/Authentication_Authorization_Design.md` contains the login sequences, session schema, and trust model.

## 2. Authentication Requirements

| ID | Requirement |
|---|---|
| REQ-AUTH-001 | The system MUST authenticate users against an external OAuth2 server using the authorization-code flow: redirect to provider → code returned → code exchanged for a token → local session established. |
| REQ-AUTH-002 | The system MUST support multiple OAuth2 providers (generic integration) selected by configuration (REQ-CFG-011). |
| REQ-AUTH-003 | If OAuth2 fails or is unavailable, the system MUST query up to 3 configured LDAP servers sequentially (server 1, then 2, then 3; stop at first successful bind+bind-verified bind). |
| REQ-AUTH-004 | The user's identity MUST be mapped to a system user by email address from the provider assertion or LDAP directory attributes. |
| REQ-AUTH-005 | The successful authentication source MUST be recorded on the user record (`auth_source` = `oauth2`|`ldap`) and in the audit log. |
| REQ-AUTH-006 | A user account MUST exist and be `enabled` in the system for login to succeed; otherwise the login is rejected with "account not enabled" and the failure is audit-logged. |
| REQ-AUTH-007 | On first login of the bootstrap admin email (`ADMIN_BOOTSTRAP_EMAIL`), the API MUST create/enable the account with `is_admin = true` (GD-4). |
| REQ-AUTH-008 | Login attempts (success and failure) and logouts MUST be audit-logged (see `Audit_Logging_Requirements.md`). |

### 2.1 Session (decision GD-1)

| ID | Requirement |
|---|---|
| REQ-AUTH-009 | The session MUST be owned by the PHP web application (PHP-native session, `HttpOnly`/`Secure`/`SameSite=Lax` cookie). The session MUST store at least: user id, email, display name, `is_admin`, authentication source, and issue time. |
| REQ-AUTH-010 | The browser MUST NOT call the administration API (`/api/v1/*`) directly; all administration API calls are made server-side by PHP with the session context. |
| REQ-AUTH-011 | PHP MUST identify itself to the API with the shared service token and the user id: headers `X-Internal-Service-Token` (shared secret) and `X-Internal-User-Id`. The API MUST reject `/api/v1/*` calls lacking a valid service token. |
| REQ-AUTH-012 | The API MUST compare the service token in constant time and MUST NOT log its value. |
| REQ-AUTH-013 | The API MUST treat `X-Internal-User-Id` as authoritative for authorization on `/api/v1/*`; the user MUST exist and be enabled, else the call is rejected (403) and audit-logged. |
| REQ-AUTH-014 | The reverse proxy MUST strip `X-Internal-Service-Token` and `X-Internal-User-Id` from all externally-originated requests; `/api/v1/*` MUST not be routable from the public network (REQ-TECH-018). |
| REQ-AUTH-015 | Sessions MUST expire after a configurable inactivity timeout (default 8 h) and on explicit logout. Logout MUST call `POST /api/v1/auth/logout` (audit) before destroying the session. |
| REQ-AUTH-016 | `POST /api/v1/auth/login` (called by PHP after successful IdP authentication) MUST: verify the user row exists and is enabled, apply the bootstrap-admin promotion (REQ-AUTH-007), record a login audit event, and return the user object including `is_admin`. |

## 3. Authorization Requirements (RBAC)

### 3.1 Permission Set (decision GD-2)

| ID | Requirement |
|---|---|
| REQ-AUTH-017 | The permission set MUST be exactly: `view`, `change`, `add`, `export_all`, `export_anonymized`, `export_non_sensitive`, `project_admin`. |
| REQ-AUTH-018 | Permission semantics: `view` = read project structure and record values; `change` = modify existing record values (update, delete); `add` = create new records/values; `export_all` = full (unredacted) export; `export_anonymized` = export with anonymization rules; `export_non_sensitive` = export with sensitive fields excluded entirely; `project_admin` = modify project structure and metadata. |
| REQ-AUTH-019 | Every API operation and every UI action MUST require an explicit permission (see mapping tables in the API and UI requirement documents); there MUST be no implicit access. |

### 3.2 Roles

| ID | Requirement |
|---|---|
| REQ-AUTH-020 | Built-in roles MUST exist per project: `data-manager` (default: `view, change, add, project_admin`), `data-entry` (default: `view, change, add`), `controller` (default: `view`). Defaults are editable when a project copies them into custom roles. |
| REQ-AUTH-021 | Administrators MUST be able to create custom roles per project with any mixture of the seven permissions. |
| REQ-AUTH-022 | A project member without an assigned role MUST have all seven permissions for that project only (master spec, BR-002). |
| REQ-AUTH-023 | A user with `is_admin = true` MUST have all seven permissions on every project and MUST see every project in the dashboard (BR-010). |
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

## 4. Non-Functional Security Requirements

| ID | Requirement |
|---|---|
| REQ-AUTH-034 | All inter-component traffic between the web application and the API MUST be over a trusted path (loopback or internal network) or TLS. |
| REQ-AUTH-035 | Brute-force protection: after 5 failed logins for the same email within 10 minutes, further attempts for that email MUST be rejected for 15 minutes (per host, in memory or session store); failures are audit-logged. |
| REQ-AUTH-036 | The system MUST NOT store user passwords; authentication is delegated to the IdP or LDAP bind. |
| REQ-AUTH-037 | CSRF protection: all state-changing browser requests MUST carry a per-session CSRF token; the API's admin surface is additionally protected by the service-token boundary (GD-1). |

## 5. Assumptions

| ID | Assumption |
|---|---|
| ASM-AUTH-1 | The OAuth2 provider exposes standard authorization-code endpoints (authorize, token, userinfo or ID token with email). Provider-specific quirks are out of scope beyond generic OAuth2. |
| ASM-AUTH-2 | LDAP servers accept simple bind for authentication (bind-as-user); directory reads use a configured bind DN. |
| ASM-AUTH-3 | The "administrator group" of the master spec is realized by `is_admin` (GD-4), not by an IdP group. |

## 6. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-AUTH-1 | `export_non_sensitive` added to the permission set | Decision GD-2 (user-confirmed: seven permissions). |
| DEV-AUTH-2 | Session ownership fixed to the PHP layer; API is stateless w.r.t. admin sessions | Decision GD-1 (user-confirmed). The plan's `POST /api/v1/auth/login` is re-interpreted as a PHP→API call establishing user state + audit, not a browser-facing session endpoint. |
| DEV-AUTH-3 | Built-in role default permission sets made explicit | The plan names roles but not their default permission sets; defaults chosen conservatively and overridable via custom roles. |
