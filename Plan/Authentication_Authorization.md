# Authentication and Authorization Plan

This document outlines the security model for the clinical study management system, utilizing OAuth2 (with LDAP fallback) for authentication and role-based access control (RBAC) for authorization.

## Authentication
- Protocol: OAuth2
- Provider: External OAuth2 Server
- Flow:
    1. User initiates login and is redirected to the OAuth2 provider.
    2. Upon successful authentication, the provider returns an authorization code.
    3. The system exchanges the code for an access token.
    4. A local session is established; the user's API token (UUID) for a project is taken from the user-project assignment.

### Identity Providers & LDAP Fallback
- Multiple Providers: The system will support generic OAuth2 integration with multiple identity providers.
- LDAP Fallback: If OAuth2 fails or is unavailable, the system will query up to 3 configured LDAP servers sequentially.
- Role Mapping: User roles will be read directly from the identity provider or LDAP directory attributes and mapped to system permissions.

## Authorization
- Model: Role-Based Access Control (RBAC); roles are per-project collections of permissions.
- One (external, OAuth or LDAP) role for general access to web-interface (no administration tools, no project access, info page only with instructions on how to apply)
- Roles:
    - data-manager: Can change the project structure and enter/change records.
    - data-entry: Can enter and change records; cannot modify the project structure.
    - controller: Can see the project structure; cannot change records.
    - Custom roles: Additional roles can be created with a mixture of permissions (import/export/tools).
- Permissions: view, change, add for data. For export: export all, export anonymized, export only non-sensitive data. project_admin role/permission to allow study design and setup for a project.
- Project Visibility: A project is visible to a user only if the user is in the administrator group or is a member of the project.
- Role-less Members: All members of a project that are not assigned a project role have full permissions for this project only.
- UI Gating: The interface only presents objects and sub-pages if the role/permission of the user allows them to use them. E.g. no admin interface for non-admin user.

## Token Management
- API tokens (UUID) are mapped to user accounts and specific projects (user-project assignment). Generated in the admin interface.
- The token is passed as the `token` parameter in the API request body (REDCap-compatible protocol), not in an Authorization header.
