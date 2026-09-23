# API Endpoints — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/API_Endpoints.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-18

## 1. Purpose

Defines the API surface requirements: the REDCap-compatible data API for external callers (Fiona/RIS) and the administration API used exclusively by the PHP web application. `Design/API_Endpoints_Design.md` contains the normative endpoint contracts: parameter tables, request/response schemas, and error formats.

## 2. Common Requirements (both surfaces)

| ID | Requirement |
|---|---|
| REQ-API-001 | A single Go API process (REQ-TECH-003) MUST expose exactly two API surfaces: the REDCap-compatible data API at `/api/` and the administration API under `/api/v1/`. No application endpoints MAY exist beyond these, the API documentation, and the health endpoint (REQ-API-003). |
| REQ-API-002 | Both surfaces MUST be described by an OpenAPI (Swagger) specification and served at a stable URL with an interactive documentation UI (REQ-TECH-004, BR-005). |
| REQ-API-003 | An unauthenticated health/readiness endpoint (path per design document) MUST report process and database liveness without returning data. |
| REQ-API-004 | All **system** timestamps in requests and responses (audit, `created_at`, `creation_time`, history, …) MUST be UTC (GD-7, as scoped 2026-09-22); clinical date/date-time **values** carry their collection timezone in the canonical form (GD-16, REQ-VAL-041). All string content MUST be UTF-8. |
| REQ-API-005 | Tokens, the service secret, and record values MUST NOT appear in application logs at any log level (REQ-TECH-016). |
| REQ-API-006 | Administration API errors MUST be JSON objects with a consistent shape (`error` machine code, human-readable `message`, `status`); stack traces and internal details MUST NOT be exposed. |
| REQ-API-007 | Access to a project or record the caller is not entitled to MUST be rejected without disclosing its existence (consistent 403/404 semantics); a project is visible only to `is_admin` or members (BR-010, REQ-AUTH-026). |
| REQ-API-008 | The administration API is versioned in the path (`/api/v1/`); breaking changes MUST be introduced only under a new major version. |

## 3. REDCap-Compatible Data API (`/api/`)

### 3.1 Protocol

| ID | Requirement |
|---|---|
| REQ-API-009 | All REDCap protocol operations MUST be available on a single endpoint, `POST /api/` with a form-encoded body (`application/x-www-form-urlencoded`); `GET /api/` with parameters in the query string MUST also be accepted (master spec). |
| REQ-API-010 | The caller's token MUST be passed as the `token` request parameter (body or query) and MUST NOT be read from an `Authorization` header (GD-5, REQ-AUTH-031). |
| REQ-API-011 | A missing, invalid, or disabled token MUST produce HTTP 401 with a REDCap-style `Invalid token` error in the requested response format, without disclosing whether the token exists (REQ-AUTH-032). |
| REQ-API-012 | Every call MUST specify a supported `content` value: `project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, or `record` with `action` = `export` \| `import` \| `delete`. Unknown or missing `content`, or `record` with a missing/unsupported `action`, MUST be rejected with a REDCap-style error. |
| REQ-API-013 | `format` and `returnFormat` MUST accept `json` and `csv`; when omitted the response MUST default to `csv` (REDCap default) and a present `returnFormat` MUST take precedence for the response encoding. |
| REQ-API-014 | `type` MUST accept `flat` and `wide` (REDCap default when omitted: `wide`). `flat` (used by all known callers) is normative: one row per (record, event). `wide` MUST be accepted for caller compatibility with the simplified semantics of DEV-API-1. |
| REQ-API-015 | Array parameters (`records[]`, `fields[]`, `forms[]`, `events[]`) MUST be accepted both in REDCap array syntax (`records[0]=…&records[1]=…`) and as repeated single values. |
| REQ-API-016 | The parameters `exportCheckboxLabel`, `exportSurveyFields`, and `exportDataAccessGroups` MUST be accepted without error and ignored (these REDCap flags stay out of scope; surveys per GD-9 and data access groups per GD-10 do not change the handling of these parameters; DEV-API-2). |
| REQ-API-017 | Unknown parameters MUST be accepted and ignored, never rejected, so that existing callers sending extra parameters keep working. |

### 3.2 Read operations

| ID | Requirement |
|---|---|
| REQ-API-018 | `content=project` MUST return the project identification (id, name/title, description, PI name/email, REK/IRB number, start/end dates, creation time); any valid token for the project suffices. Standard REDCap project-info keys the system does not store (e.g. `surveys_enabled`, `randomization_enabled`, and `project_end_provision` since GD-17) MUST be returned with neutral values (`0`/`\"\"`/`false`) rather than omitted, so naive parsers keep working (master spec example response). |
| REQ-API-019 | `content=metadata` MUST return the full data dictionary in REDCap metadata shape (field name, instrument, section header, field type, label, choices, note, validation type/min/max, required, branching logic, matrix group, identifier flag), with matrix rows expanded per REQ-DB-014. Requires data access level ≥ `read_only` (GD-2). |
| REQ-API-020 | `content=event` MUST return one row per event with `event_name`, `arm_num`, `unique_event_name`, `event_id` (master spec), **in the canonical per-arm order** (GD-15, REQ-DB-011: timepoint events by `period` ascending — ties by position — then no-timepoint events by position). Requires data access level ≥ `read_only` (GD-2). |
| REQ-API-021 | `content=formEventMapping` MUST return the instrument-by-event mapping per arm (instruments, events, arm, mapping flag). Requires data access level ≥ `read_only` (GD-2). |
| REQ-API-022 | `content=exportFieldNames` MUST return the project's field names, restricted to the given `forms[]` when supplied. Requires data access level ≥ `read_only` (GD-2). |
| REQ-API-023 | `content=generateNextRecordName` MUST return the next record name following the project's participant naming pattern, supporting both styles of REQ-DB-007 (digit placeholders `8DISC[0-9][0-9][0-9]` and counter prefix `0001_01` with preserved width), and MUST NOT return a name that an existing record already holds. Requires data access level ≥ `view_edit` on the target arm (GD-2). |

### 3.3 Record export (`content=record&action=export`)

| ID | Requirement |
|---|---|
| REQ-API-024 | The export MUST honor optional filters `records[]`, `fields[]`, `forms[]`, `events[]` (combined); with no filters, all records of the project visible to the holder under the data access group rule (REQ-AUTH-045) MUST be returned. |
| REQ-API-025 | `filterLogic` MUST be supported: at minimum equality conditions on string fields of the form `[field]=\"value\"` (Fiona's use case, master spec); compound conditions (`&&`, `||`) and comparison operators SHOULD be supported per the design document. |
| REQ-API-026 | The sensitivity of the export MUST be determined by the token holder's export level for the arm of the exported data (GD-2): `export_full` → full dataset; `export_no_identifiers` → all identifier fields removed; `export_de_identified` → de-identified per `Data_Export_Anonymization_Requirements.md`; `export_none` → rejected (403). |
| REQ-API-027 | `rawOrLabel` MUST accept `raw` (default) and `label` (choice labels for dropdown/radio fields); `rawOrLabelHeaders` MUST accept `raw` (default), `label`, and `both` for field names. |
| REQ-API-028 | Export rows MUST contain the record identifier's value under its field name (GD-8), a `redcap_event_name` per row for projects with events (flat), and empty strings for missing values — matching the response shapes in the master spec examples. |
| REQ-API-029 | CSV export MUST be streamable (REQ-TECH-011), MUST honor `csvDelimiter` (empty value = default comma), and MUST quote fields per standard CSV rules; formula-triggering leading characters MUST be neutralized per REQ-VAL-032. |
| REQ-API-030 | Every record pull via this action MUST be written to the record-view audit table with the token, records, and instruments accessed (BR-007; `Audit_Logging_Requirements.md`). |

### 3.4 Record import (`content=record&action=import`)

| ID | Requirement |
|---|---|
| REQ-API-031 | Imports MUST accept values in the REDCap import shape: `data[]` entries of (record, form/instrument, event, field, value); each value MUST be stored under the EAV key of REQ-DB-015. An import MAY carry an optional `tz` parameter (IANA timezone name or `±HH:MM` offset) supplying the **timezone of collection** for the date/date-time values of the call; without it, the deployment default `APP_TIMEZONE` applies (GD-16, REQ-VAL-041, REQ-CFG-026). |
| REQ-API-032 | Every value MUST pass the field's validation rules (`Data_Validation_Requirements.md`) before storage; invalid values MUST NOT be stored. |
| REQ-API-033 | Imports MUST upsert (REQ-DB-016): entering values for a new record or changing values of an existing record requires the data access level ≥ `view_edit` on the record's arm; a token at `read_only` or `no_access` MUST be rejected (GD-2). |
| REQ-API-034 | Import responses MUST use the REDCap result codes: `1` record added, `2` record updated, `0` validation errors (with per-field error detail), `255` fatal error. |
| REQ-API-035 | A record's imported values MUST be stored all-or-nothing (single transaction per record); every successful import MUST be audit-logged with user, token, record, and changed fields (`Audit_Logging_Requirements.md`). |
| REQ-API-095 | An import MUST reject a value for a calculated field (its value is system-managed, REQ-VAL-036); a recomputation triggered by the import MUST complete in the same transaction as the imported value (REQ-VAL-037, REQ-AUD-023). |

### 3.5 Record deletion (`content=record&action=delete`, GD-3)

| ID | Requirement |
|---|---|
| REQ-API-036 | The deletion MUST remove the record's values (scoped by the supplied `records[]`/`events[]`/`fields[]`, or the whole record) and, when a record's last value is removed, the record itself; it requires the data access level ≥ `delete` on the record's arm (GD-2); the deletion including the deleted values MUST be audit-logged (GD-3). |

### 3.6 Compatibility and limits

| ID | Requirement |
|---|---|
| REQ-API-037 | Every Fiona call example in `Endpoints.md` (PHP and cURL) MUST succeed against this API without modification of the caller (charter success criterion 1) and MUST be encoded as executable regression tests (REQ-TECH-022). |
| REQ-API-038 | Optional rate limiting (REQ-CFG-020): when enabled, the API MUST limit requests per token per minute and answer HTTP 429 with a REDCap-style error body when the limit is exceeded. |
| REQ-API-039 | Data API error responses MUST carry a machine-readable `error` string in the body rendered in the requested format, with 4xx HTTP status (400 invalid request, 401 invalid token, 403 insufficient permission); error text MUST NOT leak internal implementation details. |

## 4. Administration API (`/api/v1/`)

### 4.1 Boundary

| ID | Requirement |
|---|---|
| REQ-API-040 | The administration API MUST be reachable only from the trusted internal path (REQ-TECH-018) and MUST be used exclusively by the PHP web application (GD-1, BR-006); the browser MUST NOT call it directly. |
| REQ-API-041 | Every request MUST present a valid `X-Internal-Service-Token` and `X-Internal-User-Id`; the API MUST reject calls violating REQ-AUTH-011 through REQ-AUTH-014. |
| REQ-API-042 | The surface MUST use JSON request/response bodies with conventional REST semantics (GET read, POST create, PUT update, DELETE remove); all PUT endpoints MUST be idempotent. |
| REQ-API-043 | Every mutating call MUST be audit-logged with the acting user, the target project, and the operation (`Audit_Logging_Requirements.md`). |

### 4.2 Session

| ID | Requirement |
|---|---|
| REQ-API-044 | `POST /api/v1/auth/login` (called by the PHP app after successful OAuth2/LDAP authentication, **or with `source: "local"` and the password for table-based login** — GD-18, REQ-AUTH-050/051) MUST accept the authenticated user's identity (email) in a JSON body; for local login it MUST verify the stored bcrypt hash in constant time first (mismatch or absent hash → 401 `bad_password`, never logged, REQ-AUTH-036); then verify that the user row is active (REQ-AUTH-006: `enabled`, not expired `valid_until`, not inactive — `account_disabled`/`account_expired`), apply the bootstrap-admin promotion (REQ-AUTH-007), set `last_login_at` (REQ-AUTH-053), record a login audit event with the source (REQ-AUTH-008), and return the user object (id, email, display name, `is_admin`, authentication source, `ui_language`). The API is stateless with respect to sessions (GD-1): it MUST NOT create or store a session. |
| REQ-API-045 | `POST /api/v1/auth/logout` MUST record a logout audit event (REQ-AUTH-008) and return a 200 response; destruction of the PHP session remains the responsibility of the PHP layer before this call (GD-1, REQ-AUTH-015). |

### 4.3 Users

| ID | Requirement |
|---|---|
| REQ-API-046 | `GET /api/v1/users` MUST return the list of user accounts (id, email, display name, enabled, `is_admin`, authentication source, **last login `last_login_at`, validity end `valid_until`, derived status active/disabled/expired/auto-disabled — GD-19, REQ-AUTH-052/053) for an acting administrator; calls by non-`is_admin` users MUST be rejected (403). |
| REQ-API-047 | `POST /api/v1/users` MUST create a user account by email address and display name; it MAY take `valid_days` (integer ≥ 0, `0` = indefinite → stored as `valid_until = today + valid_days` or `NULL`, REQ-AUTH-052) and a `password` (stored only as a bcrypt hash in `password_hash`, REQ-AUTH-050); if a disabled account with the same email exists, the call MUST re-enable it rather than fail (re-enabling resets the inactivity clock, REQ-AUTH-053). |
| REQ-API-048 | `PUT /api/v1/users/{id}` MUST enable or disable the named account (idempotent, REQ-API-042; the supplied `enabled` flag is authoritative) and MAY change its `valid_days` (REQ-AUTH-052) or set/reset/clear its local `password` (a supplied hash is stored only, never returned or logged — REQ-AUTH-050, REQ-AUTH-036; an empty `password` clears `password_hash`); it requires `is_admin`. Re-enabling an auto-disabled account MUST reset the inactivity clock (REQ-AUTH-053). Disabling a user MUST deny the effective permissions of that user's API tokens at call time (REQ-AUTH-033). The operation MUST be audit-logged with the changed attributes (`password` never — REQ-API-043). |

### 4.4 Projects

| ID | Requirement |
|---|---|
| REQ-API-049 | `GET /api/v1/projects` MUST return the projects visible to the acting user (`is_admin` or member, REQ-AUTH-026) with summary data for the dashboard (name, organization, record count, instrument count, field count); projects outside the acting user's visibility MUST NOT appear (REQ-API-007). |
| REQ-API-050 | `POST /api/v1/projects` MUST create a project from the creation fields of the simplified `projects` (GD-17, REQ-DB-006: name, organization, PI, data manager, REK number/dates, start/end dates, participant naming pattern), reject a duplicate project name (409), create the first arm (single-arm start, REQ-DB-011; DEV-API-4), and require the acting user to be `is_admin` (master spec: administrator users create projects). Initial events are **not** created by the call — `event_names` is no longer part of the creation body (GD-17); events are added through `POST /api/v1/projects/{id}/events` (REQ-API-062). The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-051 | `GET /api/v1/projects/{id}` MUST return the project's full metadata plus its structure (arms, events, instruments with position, field count per instrument); it requires the data access level ≥ `read_only` (GD-2) and project visibility (REQ-API-007). |
| REQ-API-052 | `PUT /api/v1/projects/{id}` MUST update the project's metadata fields — the simplified set of GD-17/REQ-DB-006 (the removed attributes are rejected as unknown attributes, 400) — (idempotent, REQ-API-042); it requires `project_admin` (an `is_admin` user is covered by REQ-AUTH-023). Metadata changes MUST be audit-logged with old and new values (audit plan \"Project Structure\"; REQ-API-043). |

### 4.5 Members and tokens

| ID | Requirement |
|---|---|
| REQ-API-053 | `GET /api/v1/projects/{id}/users` MUST list the project's members with (user id, email, display name, role name or role-less, token present, enabled state); it requires `is_admin` (master spec: administrator users assign users to projects given a role). |
| REQ-API-054 | `PUT /api/v1/projects/{id}/users/{uid}` MUST be the single mutation point for membership: add a member (with or without a role), change the role (including role-less = full permissions, REQ-AUTH-022), or remove a member; it requires `is_admin`. Every change MUST be audit-logged (REQ-API-043). |
| REQ-API-055 | Token issuance and rotation MUST happen through REQ-API-054; the new token MUST be returned in the response (the UI displays it to the administrator), rotation MUST invalidate the previous token immediately, and member removal MUST invalidate the member's token immediately (REQ-AUTH-030). Token values MUST NOT appear in logs (REQ-API-005). |
| REQ-API-102 | `GET /api/v1/projects/{id}/users/{uid}/token` MUST return the member's current project token for project `{id}`. It is **self-service only**: `{uid}` MUST equal the acting user (from `X-Internal-User-Id`) and the acting user MUST be a member of the project — otherwise a uniform 403 `forbidden` (never disclosed as missing, REQ-API-007). The token value MUST NOT appear in logs (REQ-API-005). The fetch is credential plumbing and is NOT audit-logged as a distinct event; the data-API calls that present the token carry it in the fixed `token` column (REQ-AUD-018). It exists so the PHP layer can present the member's own token to the data API for UI data entry (ASM-API-3). |

### 4.6 Roles

| ID | Requirement |
|---|---|
| REQ-API-056 | `GET /api/v1/projects/{id}/roles` MUST list the project's roles — presets and/or custom roles created for the project, possibly an empty list — with name and permission set (REQ-AUTH-020); it requires `is_admin`. |
| REQ-API-057 | `POST /api/v1/projects/{id}/roles` MUST create a role with any name (unique within the project) and per-arm permission assignments — data access and export levels per arm (REQ-AUTH-017), optionally `project_admin`; roles are not limited to the example presets (REQ-AUTH-020). It requires `is_admin` (REQ-AUTH-021). The creation MUST be audit-logged (REQ-API-043). |

### 4.7 Arms

| ID | Requirement |
|---|---|
| REQ-API-058 | `GET /api/v1/projects/{id}/arms` MUST list the arms with (arm_num, name, events); it requires the data access level ≥ `read_only` (GD-2; REQ-API-007). |
| REQ-API-059 | `POST /api/v1/projects/{id}/arms` MUST add an arm (1-based `arm_num`, name); it requires `project_admin` (plan: \"permission project_admin\"). The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-060 | `DELETE /api/v1/arms/{id}` MUST remove an arm; it requires `project_admin` and MUST reject (409) an arm that still has events or data (ASM-API-4, DEV-API-6). The deletion MUST be audit-logged (REQ-API-043). |

### 4.8 Events

| ID | Requirement |
|---|---|
| REQ-API-061 | `GET /api/v1/projects/{id}/events` MUST list the events per arm with (event_name, arm_num, unique_event_name, event_id, period — `null` when the event has no timepoint, GD-15, safe region, position) (REQ-DB-011), in the **canonical per-arm order** (GD-15); it requires the data access level ≥ `read_only` (GD-2). |
| REQ-API-062 | `POST /api/v1/projects/{id}/events` MUST create an event for a given arm with (label, **optional** timepoint `period` in days — `null`/absent = no timepoint, GD-15, safe region start/end in days); the API MUST derive `unique_event_name` as `<label>_arm_<n>` (REQ-DB-011) and MUST reject a duplicate label within the same arm; the new event takes `position` = end of the arm's list; it requires `project_admin`. The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-063 | `PUT /api/v1/events/{id}` MUST update the event's label, timepoint `period` (setting or clearing it to `null`, GD-15), and safe region (idempotent, REQ-API-042); it requires `project_admin`. The semantics of a label change for an event that already holds data are defined by the design document (ASM-API-4). The change MUST be audit-logged (REQ-API-043). |
| REQ-API-103 | `PUT /api/v1/projects/{id}/events/order` MUST reorder the events of the supplied arm from a **full** ordered list of the arm's event ids in the body (a partial list → 400; idempotent, REQ-API-042) by writing `position`; it requires `project_admin`. The canonical order of GD-15 is preserved — the endpoint changes `position`, which governs no-timepoint events and ties among timepoint events. The change MUST be audit-logged as `event_reordered` (REQ-API-043). |

### 4.9 Instruments

| ID | Requirement |
|---|---|
| REQ-API-064 | `GET /api/v1/projects/{id}/instruments` MUST list the instruments with (name, position, field count); it requires the data access level ≥ `read_only` (GD-2). |
| REQ-API-065 | `POST /api/v1/projects/{id}/instruments` MUST create an instrument with a name unique within the project (REQ-DB-013); it requires `project_admin`. The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-066 | `PUT /api/v1/projects/{id}/instruments/order` MUST reorder the instrument list from a full ordered list in the body (idempotent, REQ-API-042); it requires `project_admin`. The record-identifier invariant MUST hold after reordering (GD-8: the first field of the first instrument is the record identifier). The change MUST be audit-logged (REQ-API-043). |
| REQ-API-101 | `PUT /api/v1/projects/{id}/instruments/{iid}` MUST update the instrument's attributes (the `is_survey` flag, REQ-DB-011, and the branching logic expression, REQ-VAL-040; idempotent, REQ-API-042); it requires `project_admin`; an invalid branching expression MUST be rejected at design time (REQ-VAL-029). The change MUST be audit-logged (REQ-API-043). |

### 4.10 Fields (designer)

| ID | Requirement |
|---|---|
| REQ-API-067 | `GET /api/v1/projects/{id}/instruments/{iid}/fields` MUST list the instrument's fields in position order with all data dictionary attributes (REQ-DB-013); it requires the data access level ≥ `read_only` (GD-2). |
| REQ-API-068 | `POST /api/v1/projects/{id}/instruments/{iid}/fields` MUST create a field; the field name MUST be lower-case alphanumeric with underscores and unique within the project (REQ-DB-013), and names longer than 26 characters MUST be accepted (the warning after 26 characters is a UI concern, master spec); it requires `project_admin`. The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-069 | `PUT /api/v1/projects/{id}/instruments/{iid}/fields/{fid}` MUST update the field's attributes (including the calculation expression of a calculated field, REQ-VAL-033/034, and the branching logic expression, REQ-VAL-029; idempotent, REQ-API-042); it requires `project_admin`. The change MUST be audit-logged with old and new values (REQ-API-043). |
| REQ-API-070 | `DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}` MUST remove the field and, in the same transaction, its stored values (DEV-API-5); it requires `project_admin`. The deletion, including the count of removed values, MUST be audit-logged (REQ-API-043). |
| REQ-API-071 | `PUT /api/v1/projects/{id}/instruments/{iid}/fields/order` MUST reorder the fields within the instrument from a full ordered list in the body (idempotent, REQ-API-042); it requires `project_admin`. The record-identifier invariant MUST hold after reordering (GD-8). The change MUST be audit-logged (REQ-API-043). |
| REQ-API-096 | `POST /api/v1/projects/{id}/records/{record}/fields/{fid}/test` MUST evaluate the calculated field's expression (or a draft expression supplied in the body) against the record's current stored values and return the result with explicit flags for each evaluation problem (missing/empty referenced value, non-numeric operand, division by zero — REQ-VAL-038); it MUST NOT store anything or rewrite the stored values. It requires `project_admin` (designer) and record visibility under the data access group rule (REQ-AUTH-045). The designer UI MUST offer this test and flag incorrect computations (User_Interface plan, instrument designer). |

### 4.11 Instrument-event mapping

| ID | Requirement |
|---|---|
| REQ-API-072 | `GET /api/v1/projects/{id}/instrument-event-mapping` MUST return the instrument × event matrix per arm (checked state per pair, REQ-DB-012); it requires the data access level ≥ `read_only` (GD-2). |
| REQ-API-073 | `PUT /api/v1/projects/{id}/instrument-event-mapping` MUST replace the mapping of the supplied arm from a full matrix in the body (idempotent, REQ-API-042); it requires `project_admin`. An instrument is active for data entry once mapped to at least one event (REQ-DB-012). The change MUST be audit-logged (REQ-API-043). |

### 4.12 Record status

| ID | Requirement |
|---|---|
| REQ-API-074 | `GET /api/v1/projects/{id}/record-status` MUST return all record_ids visible to the acting user under the data access group rule (REQ-AUTH-045), with their instruments per event, ordered per the instrument order of each arm, and a completion state per (record, event, instrument) — any field has a value vs. none (User_Interface plan, record status dashboard); it requires the data access level ≥ `read_only` (GD-2). The response MUST NOT contain field values. |

### 4.13 Export (UI)

| ID | Requirement |
|---|---|
| REQ-API-075 | `GET /api/v1/projects/{id}/export` MUST return the project's data, streamed (REQ-TECH-011), as CSV or JSON per a `format` query parameter (default CSV). The sensitivity MUST follow the acting user's export level per arm (GD-2, as REQ-API-026): `export_full` → full dataset; `export_no_identifiers` → all identifier fields removed; `export_de_identified` → de-identified per `Data_Export_Anonymization_Requirements.md`; `export_none` → rejected (403). |
| REQ-API-076 | Every export via this endpoint MUST be audit-logged as an export event with the acting user, the project, and the sensitivity level (audit plan \"Exports\"; BR-007). |

### 4.14 Audit log

| ID | Requirement |
|---|---|
| REQ-API-077 | `GET /api/v1/audit` MUST return audit entries (`audit_events` and `audit_record_views`, selected by a `type` parameter) in reverse chronological order with pagination (limit/cursor). It MUST be read-only: no endpoint MUST exist that writes, updates, or deletes audit data (REQ-DB-024). |
| REQ-API-078 | Audit log access MUST be restricted to authorized users (plan): a non-admin acting user MUST see only entries for projects they are a member of; an `is_admin` user MAY query all projects. A project filter parameter MUST be supported (REQ-API-007, ASM-API-2). |

### 4.15 Record history

| ID | Requirement |
|---|---|
| REQ-API-079 | `GET /api/v1/projects/{id}/records/{record}/history` MUST return the record's change history from the audit trail: every data-change entry in chronological order with the timestamp (UTC), the acting user (id and display name), the action (create/update/delete), and per changed field (field name, instrument, event) the old and new value — for deletions, the deleted value (REQ-AUD-009). It requires the data access level ≥ `read_only` on the record's arm (GD-2), project visibility (REQ-API-007), and record visibility under the data access group rule (REQ-AUTH-045), and MUST be read-only with respect to the audit trail (REQ-AUD-002). |
| REQ-API-080 | The history MUST be filterable by instrument, event, and field, and MUST be paginated (limit/cursor); it MUST cover all changes of the record since creation, transparent across the yearly rollover (REQ-AUD-006). |
| REQ-API-081 | The data entry form (instrument display of a record) MUST present, per field, this history — who entered or changed the value, when, and the old → new values — fetched from REQ-API-079 (User_Interface plan, data entry form). |

### 4.16 Survey links (GD-9)

| ID | Requirement |
|---|---|
| REQ-API-082 | `GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link` MUST return the stable public link (URL carrying the link token) for a survey-marked instrument and record; it MUST be rejected for an instrument that is not marked as a survey (REQ-DB-011); it requires the data access level ≥ `view_edit` on the arm (GD-2) and record visibility under the data access group rule (REQ-AUTH-045). Issuance MUST be audit-logged (REQ-API-043). |
| REQ-API-083 | A link token presented to the data API MUST be accepted only for the calls that render and fill that (record, instrument): the instrument's field definitions and `content=record&action=import` of values for that record and instrument (REQ-AUTH-039); every other content, record, or instrument MUST be rejected (403). Link tokens are subject to optional rate limiting (REQ-API-038). |
| REQ-API-084 | The public survey page MUST be served by the PHP application; the browser MUST NOT call `/api/v1/*` from it; submissions MUST go through the data API (GD-1, REQ-TECH-002). |
| REQ-API-085 | `DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link` MUST revoke the link token; revocation MUST take effect immediately (REQ-AUTH-040) and MUST be audit-logged (REQ-API-043). |

### 4.17 Data Access Groups (GD-10)

| ID | Requirement |
|---|---|
| REQ-API-086 | `GET /api/v1/projects/{id}/data-access-groups` MUST list the project's groups (id, name; possibly an empty list); it requires the data access level ≥ `read_only` (GD-2). |
| REQ-API-087 | `POST /api/v1/projects/{id}/data-access-groups` MUST create a group with a name unique within the project (REQ-AUTH-043); it requires `project_admin` (an `is_admin` user is covered by REQ-AUTH-023). The creation MUST be audit-logged (REQ-API-043). |
| REQ-API-088 | `DELETE /api/v1/projects/{id}/data-access-groups/{gid}` MUST remove a group and its member assignments; it MUST be rejected (409) while records are still assigned to it (ASM-AUTH-4); it requires `project_admin`. The deletion MUST be audit-logged (REQ-API-043). |
| REQ-API-089 | `PUT /api/v1/projects/{id}/users/{uid}/data-access-groups` MUST set a member's data access group assignments for the project (a list of group ids and the active one, or an empty list to clear); it requires `is_admin` (REQ-AUTH-044). The change MUST be audit-logged (REQ-API-043). |
| REQ-API-090 | `PUT /api/v1/projects/{id}/active-data-access-group` MUST switch the acting member's active group (body: a group id that is one of the member's assigned groups); the switch MUST take effect immediately (REQ-AUTH-046) and MUST be audit-logged (REQ-API-043). |
| REQ-API-091 | `PUT /api/v1/projects/{id}/records/{record}/data-access-group` MUST assign or change a record's data access group (body: a group id, or null to unassign); the record MUST be visible to the acting user under REQ-AUTH-045; it requires the project-level permission `project_admin` (REQ-AUTH-048). The change MUST be audit-logged (REQ-API-043). |
| REQ-API-092 | Both export surfaces MUST apply the data access group visibility rule (REQ-AUTH-045) to the record selection: a holder with an active group receives only that group's records; a holder without a group receives all records. The export level (GD-2) governs the data transformation; the group governs the record scope. |
| REQ-API-093 | A record created via import MUST be assigned to the holder's active group, or to no group if the holder has none (REQ-AUTH-047); an import MUST NOT change the group of an existing record. |
| REQ-API-094 | The user interface MUST allow a member to see and switch the active group, and to assign or change a record's group (GD-10; User_Interface plan, record view). |

### 4.18 Multilingual UI (GD-12)

| ID | Requirement |
|---|---|
| REQ-API-097 | `GET /api/v1/i18n/languages` MUST list the enabled languages (code, display name); any authenticated user. |
| REQ-API-098 | `PUT /api/v1/users/me/ui-language` MUST set the acting user's UI language to an enabled language (default `en`); the setting MUST persist across sessions (REQ-DB-008). |
| REQ-API-099 | `GET /api/v1/i18n/strings?language=<code>` MUST list the translation keys with the current translation and a missing flag; it requires `is_admin`. |
| REQ-API-100 | `PUT /api/v1/i18n/strings` MUST upsert translations (language, key, text); an empty text removes the translation (fallback to English, REQ-DB-031); it requires `is_admin`. The change MUST be audit-logged (REQ-API-043). |

### 4.19 Permission summary

| Endpoint(s) | Required permission |
|---|---|
| `GET /api/v1/users`, `POST /api/v1/users`, `PUT /api/v1/users/{id}` | `is_admin` |
| `POST /api/v1/projects` | `is_admin` |
| `GET /api/v1/projects` | project visibility only (REQ-API-007) |
| `GET /api/v1/projects/{id}` | data access ≥ `read_only` + visibility (REQ-API-007) |
| `PUT /api/v1/projects/{id}` | `project_admin` |
| `GET/PUT .../users`, `GET/POST .../roles` | `is_admin` |
| `GET .../users/{uid}/token` | self-service: acting user is a member of the project (REQ-API-102) |
| arms, events, instruments, fields, mapping — mutations (POST/PUT/DELETE) | `project_admin` |
| arms, events, instruments, fields, mapping — reads (GET) | data access ≥ `read_only` |
| `GET .../record-status` | data access ≥ `read_only` |
| `GET .../records/{record}/history` | data access ≥ `read_only` |
| `GET .../export` | export level per arm (GD-2; `export_none` → 403) |
| survey links (issue/revoke, §4.16) | data access ≥ `view_edit` on the arm |
| `GET .../data-access-groups` | data access ≥ `read_only` |
| `POST/DELETE .../data-access-groups`, `PUT .../users/{uid}/data-access-groups` | `project_admin` / `is_admin` |
| `PUT .../active-data-access-group` | an assigned member (self-service) |
| `PUT .../records/{record}/data-access-group` | `project_admin` |
| `GET /i18n/languages`, `PUT /users/me/ui-language` | any authenticated user |
| `GET/PUT /i18n/strings` | `is_admin` |
| `GET /api/v1/audit` | `is_admin`, or member of the queried project (REQ-API-078) |

`is_admin` users hold all permission levels on every arm of every project (REQ-AUTH-023), so a permission requirement never excludes an administrator.

## 5. Assumptions

| ID | Assumption |
|---|---|
| ASM-API-1 | The administration API paths follow `Plan/API_Endpoints.md` verbatim (including `DELETE /api/v1/arms/{id}` and `PUT /api/v1/events/{id}` without a project path segment); the normative request/response schemas and remaining status codes are defined in `Design/API_Endpoints_Design.md`. |
| ASM-API-2 | \"authorized users only\" for `GET /api/v1/audit` (plan) is interpreted as `is_admin` (all projects) or a member of the queried project (REQ-API-078). |
| ASM-API-3 | Data entry through the UI is performed as `content=record&action=import` against the data API (REQ-API-031), initiated by the PHP layer with the user's project token; the administration surface has no separate import endpoint (master spec: the UI accesses the backend exclusively through the API). |
| ASM-API-4 | Arm removal and label changes of events that already hold data are phase-1 edge cases (single-arm start, REQ-DB-011); the cascade/rename semantics are defined in the design document. |
| ASM-API-5 | Fiona/RIS is not bound to a dedicated account: it presents whatever user token it has been given — including tokens of `is_admin` users, which then carry full record visibility (REQ-AUTH-045). Record visibility is therefore per token bearer, not per caller system. |

## 6. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-API-1 | `type=wide` accepted with simplified semantics; `flat` is normative | The plan lists `type (flat\|wide)` without defining wide semantics; all known callers (Fiona) use `flat`, so `wide` is accepted for compatibility (REQ-API-014). |
| DEV-API-2 | `exportCheckboxLabel`, `exportSurveyFields`, `exportDataAccessGroups` accepted and ignored | Surveys and DAGs are out of phase-1 scope (charter §4); existing callers MUST keep working (REQ-API-016/017). |
| DEV-API-3 | Explicit export level ladder on both surfaces | The GD-2 revision of 2026-09-19 (DEV-AUTH-5) defined the four export levels; both surfaces apply them per arm (REQ-API-026/075). Supersedes the earlier three-level deviation (cf. DEV-AUTH-1). |
| DEV-API-4 | Project creation also creates the first arm and the initial events | REQ-DB-006 stores `event_names` (initial events) and phase 1 starts with a single arm (REQ-DB-011); the plan does not state the creation-time behavior. |
| DEV-API-5 | Field deletion cascades to the stored values (same transaction, audit-logged) | The plan is silent on value cascades; consistent with GD-3 (deletions audit-logged) and the audit plan's \"Project Structure\" events. |
| DEV-API-6 | Arm deletion rejected while the arm still has events or data | Single-arm phase-1 scope (ASM-API-4); avoids silently orphaning or removing data. |
| DEV-API-7 | Events-order endpoint added (`PUT /api/v1/projects/{id}/events/order`, REQ-API-103); `period` becomes nullable with the canonical per-arm order rule | Owner decision (2026-09-22, GD-15; master spec "Details"): timepoint events sort by timepoint, non-timepoint events are user-reorderable in the events table. |
| DEV-API-8 | `POST /api/v1/auth/login` accepts `source: "local"` + password; new error codes `bad_password` / `account_expired` | Owner decision (2026-09-22, GD-18/GD-19; master spec "Details"): table-based authentication; account validity and inactivity auto-disable (REQ-AUTH-050…053). |
| DEV-API-9 | Users endpoints extended: `valid_days`/`password` on create/update; `last_login_at`/`valid_until`/status in the user list | Owner decisions (2026-09-22, GD-18/GD-19): local password management and the account-validity/inactivity display on the user overview (REQ-API-046/047/048). |
| DEV-API-10 | Project creation body simplified; `event_names` and the option/contract/end-provision fields are no longer accepted (400 as unknown attributes); creation no longer derives initial events | Owner decision (2026-09-22, GD-17; master spec "Details"): keep PI + REK + main supporting institution; removed attributes MAY live as data in a `DataTransferProjects` instrument (REQ-DB-032). |
