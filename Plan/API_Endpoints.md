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
    - generateNextRecordName: Generate the next record name following the project's participant naming, as one step above the greatest existing name (max + 1); names of deleted records are not reused.
    - record + action=export: Export records. Parameters: records[], fields[], forms[], events[], rawOrLabel, rawOrLabelHeaders, exportCheckboxLabel, exportSurveyFields, exportDataAccessGroups, filterLogic, csvDelimiter.
    - record + action=import: Store values into fields (data entry), validated against the field rules. In analysis-mode projects all writes (import, delete, survey submissions) are rejected; reads and exports are unaffected.
- Permissions are enforced per token, **per arm** (GD-2): a data access level (`no_access` < `read_only` < `view_edit` < `delete` < `edit_survey_responses`) and an export level (`export_none` < `export_de_identified` < `export_no_identifiers` < `export_full`). The former flat set (view / change / add / export all / export anonymized) is superseded.
- Optional rate limiting applies **per source IP address** (default 600 requests/minute, disabled by default), covering both the data API and the administration surface so web-application traffic and external scripts are limited alike; over the limit answers HTTP 429 with a REDCap-style error. The caller IP comes from the reverse proxy's `X-Real-IP` when the direct peer is a trusted proxy, otherwise from the connection (REQ-API-038/111).
- The existing Fiona call examples (form body with token=..., content=..., filterLogic=...) must keep working unchanged.

2. Administration API (used exclusively by the web application)
- Authentication: POST /api/v1/auth/login (verify credentials for the OAuth2 callback, an LDAP bind, or a local table-based account — `source: "local"`), POST /api/v1/auth/logout. The login endpoint **does not create or store a session** (REQ-API-044): it authenticates and returns the identity, and the PHP web application owns the session it establishes from that result (REQ-AUTH-009). The Go API stays stateless.
- Users: GET /api/v1/users, POST /api/v1/users (create/enable an account), PUT /api/v1/users/{id} (disable/enable).
- Projects: GET /api/v1/projects (only accessible projects), POST /api/v1/projects, GET /api/v1/projects/{id}, PUT /api/v1/projects/{id}.
- Members: GET /api/v1/projects/{id}/users, PUT /api/v1/projects/{id}/users/{uid} (assign a role; issue/rotate the project token).
- Roles: GET/POST /api/v1/projects/{id}/roles (create custom roles with a mixture of permissions).
- Arms: GET/POST /api/v1/projects/{id}/arms, DELETE /api/v1/arms/{id} (permission project_admin).
- Events: GET/POST /api/v1/projects/{id}/events (label, period, safe region), PUT /api/v1/events/{id}.
- Instruments: GET/POST /api/v1/projects/{id}/instruments, PUT /api/v1/projects/{id}/instruments/order (reorder the list).
- Fields (Designer): GET/POST /api/v1/projects/{id}/instruments/{iid}/fields, PUT/DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}, PUT .../fields/order (reorder fields).
- Mapping: GET/PUT /api/v1/projects/{id}/instrument-event-mapping (checkbox table per arm).
- Record Status: GET /api/v1/projects/{id}/record-status (records with instruments by event and a three-state completion state — `no_data` / `some_data` / `finished`). PUT /api/v1/projects/{id}/records/{record}/events/{event}/instruments/{iid}/completion sets or clears the user-assigned `finished` state; it writes no field value (REQ-API-074/110).
- Export: GET /api/v1/projects/{id}/export — parameters `format` (csv|json), `arm` (repeatable, restricts the export so a higher per-arm sensitivity stays reachable), `rawOrLabel`, `rawOrLabelHeaders`, `csvDelimiter`. The applied level is the lowest of the exported arms; unknown parameters are ignored (REQ-API-075, REQ-EXP-003/010/013).
- Audit Log: GET /api/v1/audit (read-only, authorized users only).
- Project Modes: GET /api/v1/projects/{id}/mode, PUT /api/v1/projects/{id}/mode (development | production | analysis; permission project_admin; development to production requires an explicit keep-or-delete-data decision).
- Staging (production mode, permission project_admin): POST /api/v1/projects/{id}/staging (start a staging set), GET /api/v1/projects/{id}/staging (state and staged changes with breaking/non-breaking classification), POST /api/v1/projects/{id}/staging/commit (activate the whole set; breaking changes must be acknowledged), POST /api/v1/projects/{id}/staging/discard. While a set is open, structure changes apply to the staged design; data collection continues on the active design.
- System settings: GET /api/v1/settings, PUT /api/v1/settings (permission is_admin) — system-wide runtime settings persisted in the database (`system_settings`); holds the rate-limit enable flag and requests-per-minute threshold per source IP, effective immediately without restart (REQ-API-112).
