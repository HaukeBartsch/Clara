API Endpoint Documentation Plan

This document outlines the API for the clinical study management system, implemented in Go with Swagger/OpenAPI. In all calls the caller token is passed as the `token` parameter in the request body (REDCap-compatible protocol), not in an Authorization header.

1. REDCap-Compatible Data API (external callers, e.g. Fiona)
- Single endpoint: POST /api/ (also GET /api/) with form-encoded parameters.
- Common parameters: token, content, format (json|csv), type (flat|wide), returnFormat.
- Supported content values:
    - project: Return project info (name, description, PI, REK number, start/end dates).
    - metadata: Return the full data dictionary (fields by instrument by project).
    - event: Return the events (event_name, arm_num, unique_event_name, event_id).
    - formEventMapping: Return the instrument-by-event mapping.
    - exportFieldNames: Return the field names of the project.
    - generateNextRecordName: Generate the next record name following the project's participant naming.
    - record + action=export: Export records. Parameters: records[], fields[], forms[], events[], rawOrLabel, rawOrLabelHeaders, exportCheckboxLabel, exportSurveyFields, exportDataAccessGroups, filterLogic, csvDelimiter.
    - record + action=import: Store values into fields (data entry), validated against the field rules.
- Permissions are enforced per token: view, change, add, export all, export anonymized.
- The existing Fiona call examples (form body with token=..., content=..., filterLogic=...) must keep working unchanged.

2. Administration API (used exclusively by the web application)
- Session: POST /api/v1/auth/login (establish the local session after OAuth2/LDAP authentication), POST /api/v1/auth/logout.
- Users: GET /api/v1/users, POST /api/v1/users (create/enable an account), PUT /api/v1/users/{id} (disable/enable).
- Projects: GET /api/v1/projects (only accessible projects), POST /api/v1/projects, GET /api/v1/projects/{id}, PUT /api/v1/projects/{id}.
- Members: GET /api/v1/projects/{id}/users, PUT /api/v1/projects/{id}/users/{uid} (assign a role; issue/rotate the project token).
- Roles: GET/POST /api/v1/projects/{id}/roles (create custom roles with a mixture of permissions).
- Arms: GET/POST /api/v1/projects/{id}/arms, DELETE /api/v1/arms/{id} (permission project_admin).
- Events: GET/POST /api/v1/projects/{id}/events (label, period, safe region), PUT /api/v1/events/{id}.
- Instruments: GET/POST /api/v1/projects/{id}/instruments, PUT /api/v1/projects/{id}/instruments/order (reorder the list).
- Fields (Designer): GET/POST /api/v1/projects/{id}/instruments/{iid}/fields, PUT/DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}, PUT .../fields/order (reorder fields).
- Mapping: GET/PUT /api/v1/projects/{id}/instrument-event-mapping (checkbox table per arm).
- Record Status: GET /api/v1/projects/{id}/record-status (records with instruments by event and completion state).
- Export: GET /api/v1/projects/{id}/export (full or anonymized based on the user's permissions).
- Audit Log: GET /api/v1/audit (read-only, authorized users only).
