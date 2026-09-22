# API Endpoints — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/API_Endpoints_Requirement.md`
**Date:** 2026-09-21

## 1. Purpose and Conventions

The normative endpoint contracts — parameter tables, request/response schemas, status codes, error formats — for both surfaces. The requirements document fixes *what* (which endpoint does what, under which permission); this document fixes *shape*.

- **Two surfaces** (REQ-API-001): the REDCap-compatible data API at `POST /api/` (and `GET /api/`) and the administration API under `/api/v1/`. Beyond these, only the documentation and health endpoints exist (REQ-API-003).
- **UTC and UTF-8** in all requests and responses (REQ-API-004).
- **Versioning**: the administration API is versioned in the path (`/api/v1/`); breaking changes only under a new major version (REQ-API-008). The data API is pinned by the REDCap protocol — caller compatibility is the contract (REQ-API-037).
- **Non-disclosure**: a project or record the caller is not entitled to is rejected with a uniform 403 (data API: `Permission denied`; administration API: `forbidden`) — never disclosed as missing (REQ-API-007).
- **Pagination** (`GET /api/v1/audit`, record history): `limit` (default 50, max 200) + opaque `cursor` (encodes the last-seen `(created_at, id)`); the response carries `next_cursor` (`null` when exhausted).
- **JSON shapes** (administration API): objects/arrays; timestamps `YYYY-MM-DD HH:MM:SS` (UTC); absent optionals are `null`.
- **Log hygiene** (REQ-API-005): project tokens, the service secret, and record values MUST NOT appear in application logs at any level; the audit trail is the sole sanctioned carrier (`Audit_Logging_Design.md` §2).

## 2. Non-Protocol Endpoints

### 2.1 Health (REQ-API-003)

`GET /healthz` — unauthenticated; reports liveness without returning data:

```json
{ "status": "ok", "db": "ok" }
```

`503 {"status":"degraded","db":"error"}` when the database check fails.

### 2.2 Documentation (REQ-API-002, REQ-TECH-004)

- `GET /openapi.json` — the OpenAPI 3.1 document covering both surfaces.
- `GET /docs` — Swagger UI (vendored static bundle; internal-only routing per `Technology_Stack_Design.md` §5).

## 3. Data API — `/api/`

### 3.1 Transport and Common Parameters (REQ-API-009…017)

One endpoint: `POST /api/` with an `application/x-www-form-urlencoded` body; `GET /api/` with the same parameters in the query string MUST also work (REQ-API-009).

| Parameter | Accepted | Default | Notes |
|---|---|---|---|
| `token` | UUID string | — (required) | body or query; MUST NOT be read from an `Authorization` header (REQ-API-010, REQ-AUTH-031) |
| `content` | `project` / `metadata` / `event` / `formEventMapping` / `exportFieldNames` / `generateNextRecordName` / `record` | — (required) | `record` requires `action` (REQ-API-012) |
| `action` | `export` / `import` / `delete` (with `content=record` only) | — | unknown or missing → REDCap-style error (REQ-API-012) |
| `format` / `returnFormat` | `json` / `csv` | `csv` (REDCap default) | a present `returnFormat` takes precedence for the response encoding (REQ-API-013) |
| `type` | `flat` / `wide` | `wide` (REDCap default) | `flat` is normative — one row per (record, event); `wide` is the simplified compatibility mode of §3.6.2 (DEV-API-1) |
| `csvDelimiter` | single character | `,` | empty value = comma (REQ-API-029) |
| `records[]` / `fields[]` / `forms[]` / `events[]` | arrays | — | both REDCap array syntax (`records[0]=…&records[1]=…`) and repeated single values (REQ-API-015) |
| `filterLogic` | expression (§3.6.3) | — | export only (REQ-API-025) |
| `rawOrLabel` | `raw` / `label` | `raw` | choice fields (REQ-API-027) |
| `rawOrLabelHeaders` | `raw` / `label` / `both` | `raw` | field names (REQ-API-027) |
| `data[]` | import entries (§3.7.1) | — | import only (REQ-API-031) |
| `exportCheckboxLabel`, `exportSurveyFields`, `exportDataAccessGroups` | any | — | accepted and **ignored** (REQ-API-016, DEV-API-2) |

Unknown parameters are accepted and ignored, never rejected — existing callers keep working (REQ-API-017).

### 3.2 Error Format (REQ-API-039, REQ-API-011)

The error body is rendered in the requested response format (or `csv` when omitted):

| `returnFormat` | Error body |
|---|---|
| `json` | `{ "error": "Invalid token" }` |
| `csv` | `Invalid token` (single line) |

HTTP status mapping (REQ-API-039):

| Situation | Status | `error` string |
|---|---|---|
| missing/invalid token | 401 | `Invalid token` (same body whether or not the token exists — REQ-AUTH-032) |
| unknown/missing `content`, or `record` without supported `action` | 400 | `Invalid content` |
| insufficient permission (incl. `export_none`, read-only import) | 403 | `Permission denied` |
| rate limit exceeded (when enabled) | 429 | `Rate limit exceeded` |
| import with per-record validation failures | 200 | — (per-record result codes, §3.7.2) |

Error text never leaks internal implementation details (REQ-API-039, REQ-API-006).

### 3.3 `content=project` (REQ-API-018)

Any valid token for the project suffices. JSON response (array with one object):

```json
[{
  "project_id": "33",
  "project_name": "8DISC",
  "project_title": "8DISC study",
  "project_description": "",
  "project_pi_name": "Ansgar Espeland",
  "project_pi_email": "ansgar.espeland@example.org",
  "project_rek_number": "REK-2026/123",
  "project_rek_start_date": "2026-01-01",
  "project_rek_end_date": "2028-01-01",
  "project_start_date": "2026-01-01",
  "project_end_date": "2028-01-01",
  "project_end_provision": "anonymize",
  "project_organizational": "NAT EU",
  "project_creation_time": "2026-01-01 09:00:00",
  "project_pat_import_folder": "",
  "surveys_enabled": "0",
  "randomization_enabled": "0"
}]
```

Standard REDCap project-info keys the system does not store are returned with neutral values (`"0"` / `""`) rather than omitted, so naive parsers keep working (REQ-API-018). CSV: a single row of those key/value pairs.

### 3.4 `content=metadata` (REQ-API-019)

Requires data access ≥ `read_only`. JSON response: one object per field, matrix rows expanded (REQ-DB-014):

```json
[
  {
    "field_name": "record_id",
    "form_name": "intake",
    "section_header": "",
    "field_type": "text",
    "field_label": "Record ID",
    "field_note": "",
    "choice_codes": "",
    "choice_labels": "",
    "validation_type": "",
    "validation_min": "",
    "validation_max": "",
    "required_field": "Y",
    "branching_logic": "",
    "matrix_group_name": "",
    "record_identifier": "Y"
  },
  {
    "field_name": "status",
    "form_name": "intake",
    "field_type": "dropdown",
    "choice_codes": "1,2,3",
    "choice_labels": "Pending,Done,Cancelled",
    "record_identifier": "",
    "…": "…"
  }
]
```

`record_identifier` is `"Y"` on the record-identifier field (position 1 of instrument position 1, GD-8) and empty elsewhere. `choice_codes`/`choice_labels` are comma-joined from the stored `code$label##code$label` encoding.

### 3.5 `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`

All require data access ≥ `read_only` except `generateNextRecordName` (≥ `view_edit` on the target arm — REQ-API-023).

| content | Response shape (JSON) |
|---|---|
| `event` (REQ-API-020) | `[ {"event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","event_id":4}, … ]` |
| `formEventMapping` (REQ-API-021) | `[ {"form_name":"intake","event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","form_event_mapping":"1"}, … ]` |
| `exportFieldNames` (REQ-API-022) | `[ {"field_name":"record_id","form_name":"intake"}, … ]` — restricted to `forms[]` when supplied |
| `generateNextRecordName` (REQ-API-023) | `{ "next_record_name": "8DISC042" }` |

`generateNextRecordName` follows the project's naming pattern (REQ-DB-007) — digit-placeholder style (`8DISC[0-9][0-9][0-9]`) and counter-prefix style (`0001_01`, width preserved) — and MUST NOT return a name an existing record already holds (REQ-API-023).

### 3.6 `content=record&action=export`

#### 3.6.1 Rules

- Filters `records[]`, `fields[]`, `forms[]`, `events[]` combine (intersection); with no filters, all records visible to the holder under the data-access-group rule are returned (REQ-API-024, REQ-AUTH-045, REQ-API-092).
- Sensitivity per the token holder's export level for the arm(s) of the exported data (REQ-API-026): `export_full` → full dataset; `export_no_identifiers` → all identifier fields removed; `export_de_identified` → de-identified per `Data_Export_Anonymization_Requirements.md` (date shift per `Database_Schema_Design.md` §8, salt/range per `System_Configuration_Design.md` §3.6); `export_none` → 403 `Permission denied`.
- `rawOrLabel=label` → choice labels for dropdown/radio fields (stored values remain codes, REQ-VAL-022); `rawOrLabelHeaders` controls field names the same way (REQ-API-027).
- Rows carry the record identifier's value under its field name (GD-8), `redcap_event_name` per row (flat, projects with events), and empty strings for missing values (REQ-API-028).
- CSV is streamed (REQ-TECH-011): quoted per standard CSV rules, honors `csvDelimiter`, formula-triggering leading characters neutralized per `Data_Validation_Design.md` §5.3 (REQ-API-029).
- Every invocation writes a record-view audit row (`audit_record_views`, `Audit_Logging_Design.md` §4) and an `export` event (`Audit_Logging_Design.md` §3.5) (REQ-API-030, REQ-AUD-011).

#### 3.6.2 `type=flat` (normative) and `type=wide` (compatibility)

`flat` — one row per (record, event):

```json
[
  {
    "record_id": "8DISC042",
    "redcap_event_name": "baseline_arm_1",
    "redcap_repeat_instrument": "",
    "redcap_repeat_instance": "",
    "age": "42",
    "status": "2",
    "notes": ""
  }
]
```

`wide` (DEV-API-1, simplified for caller compatibility): one row per record; a field present in exactly one event keeps its bare name; a field present in several events is emitted once per event as `<field>_<unique_event_name>`. Missing values are empty strings. `flat` is what all known callers use; `wide` exists so requests that omit `type` (REDCap default) keep working.

#### 3.6.3 `filterLogic` (REQ-API-025)

Minimum normative support: equality on string fields, `[field]="value"`, and compound conditions with `&&` / `||` and parentheses — the full grammar (value comparisons `=`, `!=`, `<`, `>`, `<=`, `>=`, numeric/chronological/string comparison semantics, `text_contains`, `is_blank`, `is_not_blank`, precedence) is the branching-logic grammar of `Data_Validation_Design.md` §7, evaluated against the record's stored values. `filterLogic` filters which **records** are returned; it never changes the sensitivity level.

### 3.7 `content=record&action=import`

Requires data access ≥ `view_edit` on the record's arm (REQ-API-033); a `read_only`/`no_access` token is rejected (403 `Permission denied`).

#### 3.7.1 Request shape (REQ-API-031)

`data[]` entries, each a (record, form, event) row of field values:

```
token=…&content=record&action=import
&data[0][record_id]=8DISC042&data[0][form_name]=intake&data[0][event_name]=baseline_arm_1
&data[0][age]=42&data[0][status]=2&data[0][notes]=ok
&data[1][record_id]=8DISC043&data[1][form_name]=intake&data[1][event_name]=baseline_arm_1
&data[1][age]=&data[1][status]=1
```

Each value passes the full validation pipeline (`Data_Validation_Design.md` §2) before storage; invalid values are not stored (REQ-API-032, REQ-VAL-001). Empty values act as "no value" (clear/no-op, REQ-VAL-024). Values for calculated fields are rejected (`CALCULATED_READONLY`, REQ-API-095).

#### 3.7.2 Response (REQ-API-034, REQ-VAL-008)

HTTP 200 with one result row per imported record:

```json
[
  { "record_id": "8DISC042", "form_name": "intake", "import_record_id": 2, "import_form_name": "intake" },
  { "record_id": "8DISC043", "form_name": "intake", "import_record_id": 0,
    "import_form_name": "Validation error: age: TYPE_INVALID — value 'abc' is not a valid integer; status: CHOICE_INVALID — value '9' is not a choice of 'status'" }
]
```

| `import_record_id` | Meaning |
|---|---|
| `1` | record added |
| `2` | record updated |
| `0` | validation error(s) — `import_form_name` lists the per-field detail `<field>: <CODE> — <message>`, joined by `; ` (rule codes from `Data_Validation_Design.md` §3) |
| `255` | fatal request-level error — e.g. unknown `content`; the whole call fails (HTTP 400 with the §3.2 error body) |

All-or-nothing per record: a failed value stores nothing of that record (single transaction, REQ-API-035); other records in the same call are unaffected. Successful imports are audit-logged (`record_created`/`record_updated` with old/new values) and trigger calculated-field recomputation in the same transaction (REQ-API-095, REQ-VAL-037). A new record is assigned to the holder's active data-access group, or none (REQ-API-093); an import never changes an existing record's group.

### 3.8 `content=record&action=delete` (GD-3, REQ-API-036)

Requires data access ≥ `delete` on the record's arm. Removes the record's values — scoped by the supplied `records[]`/`events[]`/`fields[]`, or the whole record — and, when a record's last value is removed, the record itself. Audit: `record_deleted` **with the deleted values** (REQ-AUD-009, `Audit_Logging_Design.md` §3.2).

Request: `token=…&content=record&action=delete&records[0]=8DISC042` (optionally `events[]`/`fields[]` to scope).

Response (JSON):

```json
[ { "record_id": "8DISC042", "form_name": "", "deleted": 1 } ]
```

### 3.9 Rate Limiting (REQ-API-038, REQ-CFG-020)

When enabled (`RATE_LIMIT_ENABLED=1`, default disabled — `System_Configuration_Design.md` §3.9), each token is limited to `RATE_LIMIT_RPM` requests per minute (default 600); a call over the limit is answered with HTTP 429 and the §3.2 error body (`Rate limit exceeded`).

### 3.10 Survey Link Tokens (GD-9, REQ-API-083)

A survey link token (`survey_links.token`, `Database_Schema_Design.md` §8) is accepted as the `token` parameter of the data API, but only for the calls that render and fill its (record, instrument):

| Call | Allowed scope |
|---|---|
| `content=metadata` | the field definitions of that instrument (a `forms[]` naming another instrument → 403) |
| `content=record&action=import` | values for that record and that instrument |

Every other `content` (including `export` and `delete`), another record, or another instrument is rejected with 403 `Permission denied` (REQ-API-083, REQ-AUTH-039). A revoked link is rejected on every call (REQ-AUTH-040). Link tokens are subject to the §3.9 rate limit (REQ-API-038). Submissions are audit-logged as `survey_submitted` — success and failure (`Audit_Logging_Design.md` §3.6). The public survey page is served by the PHP application; the browser never calls `/api/v1/*` from it (GD-1, REQ-API-084).

## 4. Administration API — `/api/v1/`

### 4.1 Boundary and Authentication (REQ-API-040, REQ-API-041)

- Reachable only from the trusted internal path (REQ-TECH-018, REQ-AUTH-014); the proxy strips `X-Internal-Service-Token` and `X-Internal-User-Id` from every externally-originated request (`Technology_Stack_Design.md` §5). The browser MUST NOT call this surface directly (GD-1, BR-006, REQ-API-040).
- Every request MUST present a valid `X-Internal-Service-Token` and `X-Internal-User-Id` (REQ-API-041, REQ-AUTH-011…013); the sole exception is `POST /api/v1/auth/login` (§4.3, `Authentication_Authorization_Design.md` §2.3).
- The service token (config per `System_Configuration_Design.md` §3.3) is compared in constant time (REQ-AUTH-012) and MUST NOT be logged (REQ-API-005); it is enforced regardless of any other configuration (REQ-CFG-023). Missing/invalid → 401 + audit `admin_rejected`.
- `X-Internal-User-Id` is authoritative for authorization (REQ-AUTH-013): the API applies the acting user's effective levels (`Authentication_Authorization_Design.md` §4.1) — the PHP layer adds none. Unknown or disabled user → 403 + audit `admin_rejected`.
- JSON request/response bodies with conventional REST semantics (GET read, POST create, PUT update, DELETE remove); all PUT endpoints are idempotent (REQ-API-042).
- Every mutating call is audit-logged with the acting user, the target project, and the operation (REQ-API-043; `source=ui`, `Audit_Logging_Design.md` §5).
- The API is stateless: it MUST NOT create or store a session (GD-1).

### 4.2 Error Format (REQ-API-006, REQ-API-007)

Every error is a JSON object with a consistent shape:

```json
{ "error": "conflict", "message": "project name '8DISC' already exists", "status": 409 }
```

`error` is a stable machine code (callers may branch on it); `message` is human-readable and MUST NOT leak internal details (REQ-API-006). A project or record the acting user is not entitled to is rejected with the uniform 403 `forbidden` — never disclosed as missing (REQ-API-007).

| Status | `error` | When |
|---|---|---|
| 400 | `invalid_request` | malformed JSON body; a missing or invalid attribute (e.g. non-survey instrument for a link, §4.17); a GD-8 identifier-invariant violation (§4.10, §4.11) |
| 400 | `validation_error` | design-time rejection of a data-dictionary entry — ill-formed expression, unknown/inactive reference, cycle, malformed name (`Data_Validation_Design.md` §6.2, §7.2, §9); the reason is in `message` (REQ-VAL-029) |
| 401 | `service_token_invalid` | missing/invalid `X-Internal-Service-Token` (audit `admin_rejected`) |
| 401 | `account_not_found` | login with an email that has no user row (REQ-AUTH-006; audit `login_failure`) |
| 403 | `forbidden` | insufficient permission; project or record outside the acting user's visibility (uniform, REQ-API-007); unknown/disabled acting user (audit `admin_rejected`) |
| 403 | `account_disabled` | login with a disabled account (audit `login_failure`) |
| 404 | `not_found` | an unknown path resource (a user, project, arm, event, instrument, field, or group that does not exist) |
| 409 | `conflict` | a state violation — duplicate name (project, role, event label, instrument, field, group); deleting an arm that still has events or data (DEV-API-6); deleting a group that still has records (ASM-AUTH-4); deleting a field referenced by an active expression; an event rename colliding with an existing `unique_event_name` (§4.9) |
| 500 | `internal` | unexpected failure; `message` carries no details |

### 4.3 Session (REQ-API-044, REQ-API-045)

**`POST /api/v1/auth/login`** — called by the PHP application after successful OAuth2/LDAP authentication (REQ-AUTH-016). The only endpoint exempt from `X-Internal-User-Id`; it still requires the service token. Body:

```json
{ "email": "user@example.org", "source": "oauth2", "provider": "https://idp.example.org" }
```

Processing (in order, `Authentication_Authorization_Design.md` §2.3): the user row exists and `enabled = 1` — otherwise 401 `account_not_found` / 403 `account_disabled` + audit `login_failure`; bootstrap-admin promotion (REQ-AUTH-007): if the email equals `ADMIN_BOOTSTRAP_EMAIL`, ensure the row exists, is enabled, and has `is_admin = 1` (idempotent); set the row's `auth_source` (REQ-AUTH-005); audit `login_success`. The API MUST NOT create or store a session (GD-1).

200 — the user object (used by all §4.4 user endpoints):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "enabled": true, "is_admin": true, "auth_source": "oauth2", "ui_language": "en" }
```

**`POST /api/v1/auth/logout`** — records the `logout` audit event (REQ-AUTH-008) and returns 200. Destruction of the PHP session remains the PHP layer's responsibility, performed after this call (GD-1, REQ-AUTH-015; `Authentication_Authorization_Design.md` §2.4).

### 4.4 Users (REQ-API-046…048)

All three require `is_admin`; a call by a non-admin is rejected (403 `forbidden`).

| Endpoint | Contract |
|---|---|
| `GET /api/v1/users` | 200 — array of user objects (`id`, `email`, `display_name`, `enabled`, `is_admin`, `auth_source`) (REQ-API-046) |
| `POST /api/v1/users` | body `{ "email": "…", "display_name": "…" }`; a new account → 201 user object; a disabled account with the same email is re-enabled → 200 user object (REQ-API-047); audit `user_created` (`re_enabled` flag) |
| `PUT /api/v1/users/{id}` | body `{ "enabled": true\|false }` — the supplied flag is authoritative (idempotent, REQ-API-042); 200 user object; disabling a user denies the effective permissions of that user's API tokens at call time (REQ-AUTH-033, REQ-API-048); audit `user_updated` |

### 4.5 Projects (REQ-API-049…052)

**`GET /api/v1/projects`** — project visibility only (`is_admin` or member, REQ-AUTH-026); projects outside the acting user's visibility do not appear (REQ-API-007). 200 — dashboard summaries:

```json
[ { "id": 33, "project_name": "8DISC", "organization": "NAT EU", "record_count": 42, "instrument_count": 5, "field_count": 128 } ]
```

**`POST /api/v1/projects`** — `is_admin` (master spec: administrator users create projects). Body — all creation fields of REQ-DB-006:

```json
{
  "project_name": "8DISC", "organization": "NAT EU",
  "pi_name": "Ansgar Espeland", "pi_email": "ansgar.espeland@example.org",
  "dm_name": "Lars Akslen", "dm_email": "lars.akslen@uib.no",
  "rek_number": "REK-2026/123", "rek_start_date": "2026-01-01", "rek_end_date": "2028-01-01",
  "start_date": "2026-01-01", "end_date": "2028-01-01", "end_provision": "anonymize",
  "option_radiology": false, "option_pathology": false, "option_pathology_type": null,
  "option_redcap_only": false, "option_data_collection_from_home": false,
  "agreed_to_end_user_contract": true,
  "participant_names": "8DISC[0-9][0-9][0-9]",
  "event_names": ["baseline", "follow_up"]
}
```

A duplicate `project_name` → 409 `conflict`. Creation is single-arm (REQ-DB-011, DEV-API-4): the API creates arm 1 and one event per `event_names` entry (`unique_event_name = <label>_arm_1`). 201 — the project object (`id` + the supplied fields + `creation_time`). Audit `project_created`.

**`GET /api/v1/projects/{id}`** — data access ≥ `read_only` + project visibility. 200 — full metadata plus structure:

```json
{
  "id": 33, "project_name": "8DISC", "…": "…(all metadata fields)",
  "arms": [ { "arm_num": 1, "name": "", "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1", "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 } ] } ],
  "instruments": [ { "id": 7, "name": "intake", "position": 1, "field_count": 24 } ]
}
```

**`PUT /api/v1/projects/{id}`** — `project_admin` (an `is_admin` user is covered by REQ-AUTH-023). Body: any subset of the `POST` metadata fields — omitted fields are unchanged (idempotent, REQ-API-042). A duplicate `project_name` → 409 `conflict`. 200 — the updated project object (same shape as `GET`). Metadata changes are audit-logged with old and new values (`project_updated`, `Audit_Logging_Design.md` §3.3; REQ-API-052, REQ-API-043).

### 4.6 Members and Tokens (REQ-API-053…055, 102)

Both endpoints require `is_admin` (master spec: administrator users assign users to projects given a role).

**`GET /api/v1/projects/{id}/users`** — 200 — the project's members:

```json
[ { "user_id": 3, "email": "user@example.org", "display_name": "User",
    "role": "data-entry", "token_present": true, "enabled": true } ]
```

`role` is `null` for a role-less member (full permissions, REQ-AUTH-022); `token_present` reports existence without revealing the value (REQ-API-005).

**`PUT /api/v1/projects/{id}/users/{uid}`** — the single mutation point for membership (REQ-API-054):

- **Add** a member: body `{ "role": "data-entry" }` (`"role": null` or omitted = role-less). → 201 — the member object **with the new token** (returned once, for display to the administrator; REQ-API-055). Audit `membership_changed` (action `add`) + `token_issued`.
- **Change the role**: body `{ "role": "data-manager" }` or `{ "role": null }` (role-less = full permissions, REQ-AUTH-022). → 200. Audit `membership_changed` (action `role_change`).
- **Rotate the token**: body `{ "rotate_token": true }` → 200 — the member object **with the new token**; the previous token is invalid immediately (REQ-AUTH-030, REQ-API-055). Audit `token_rotated`.
- **Remove** a member: body `{ "remove": true }` → 200; the member's token is invalid immediately (REQ-API-055, REQ-AUTH-030). Audit `membership_changed` (action `remove`) + `token_revoked`.

Response shape for add and rotation:

```json
{ "user_id": 3, "email": "user@example.org", "display_name": "User",
  "role": "data-entry", "token": "8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83" }
```

The token value appears only in the add/rotation response — never in logs (REQ-API-005) and never in audit `details` (`Audit_Logging_Design.md` §2, §3.4).

**`GET /api/v1/projects/{id}/users/{uid}/token`** — self-service fetch of the member's own token (REQ-API-102). `{uid}` MUST equal the acting user (`X-Internal-User-Id`) and the acting user MUST be a member of the project; any other case is a uniform 403 `forbidden` (never disclosed as missing, REQ-API-007). 200:

```json
{ "token": "8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83" }
```

The value MUST NOT be logged (REQ-API-005). No audit event is written for the fetch itself — it is credential plumbing; the data-API calls that present the token carry it in the fixed `token` column (`Audit_Logging_Design.md` §2, REQ-AUD-018). This is the source the PHP layer uses to present the member's token to the data API for UI data entry (ASM-API-3; `User_Interface_Design.md` §8.6).

### 4.7 Roles (REQ-API-056…057)

Both endpoints require `is_admin`. Roles are project-scoped (REQ-AUTH-024) and not limited to preset examples (REQ-AUTH-020).

**`GET /api/v1/projects/{id}/roles`** — 200 — the project's roles, possibly an empty array:

```json
[ { "id": 9, "name": "data-entry", "project_admin": false,
    "arms": { "1": { "data": "view_edit", "export": "export_full" },
              "2": { "data": "no_access", "export": "export_none" } } } ]
```

`data` levels: `no_access | read_only | view_edit | delete | edit_survey_responses`; `export` levels: `export_none | export_de_identified | export_no_identifiers | export_full` (REQ-DB-009; `Authentication_Authorization_Design.md` §4.1).

**`POST /api/v1/projects/{id}/roles`** — body: the role object minus `id` (`name`, `project_admin`, `arms`); an arm absent from `arms` defaults to `no_access` / `export_none` (REQ-AUTH-019 — no implicit access). A duplicate name within the project → 409 `conflict`. 201 — the role object. Audit `role_created` (`Audit_Logging_Design.md` §3.4).

### 4.8 Arms (REQ-API-058…060)

**`GET /api/v1/projects/{id}/arms`** — data access ≥ `read_only`. 200:

```json
[ { "id": 5, "arm_num": 1, "name": "",
    "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1",
                  "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 } ] } ]
```

**`POST /api/v1/projects/{id}/arms`** — `project_admin`. Body `{ "name": "…" }`; `arm_num` is the next 1-based number (a duplicate → 409 `conflict`). 201 — the arm object. Audit `arm_created`.

**`DELETE /api/v1/arms/{id}`** — `project_admin`; the path follows the master plan verbatim (ASM-API-1). 204. An arm that still has events or data → 409 `conflict` (DEV-API-6; phase-1 edge case, ASM-API-4). Audit `arm_deleted`.

### 4.9 Events (REQ-API-061…063)

**`GET /api/v1/projects/{id}/events`** — data access ≥ `read_only`. 200 — all events, all arms:

```json
[ { "id": 4, "arm_num": 1, "event_name": "baseline", "unique_event_name": "baseline_arm_1",
    "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 } ]
```

**`POST /api/v1/projects/{id}/events`** — `project_admin`. Body:

```json
{ "arm_num": 1, "event_name": "follow_up", "period": 90, "safe_region_start": -2, "safe_region_end": 3 }
```

The API derives `unique_event_name = <label>_arm_<n>` (REQ-DB-011); a duplicate label within the same arm → 409 `conflict`. 201 — the event object. Audit `event_created`.

**`PUT /api/v1/events/{id}`** — `project_admin` (path verbatim, ASM-API-1); idempotent (REQ-API-042). Body: any subset of `{ "event_name", "period", "safe_region_start", "safe_region_end" }`. A label change re-derives `unique_event_name`; a collision with an existing `unique_event_name` → 409 `conflict` (§4.2). An event that already holds data keeps its values — the rename updates the stored `unique_event_name` key in the same transaction (ASM-API-4). Audit `event_updated` with old and new values.

### 4.10 Instruments (REQ-API-064…066, REQ-API-101)

**`GET /api/v1/projects/{id}/instruments`** — data access ≥ `read_only`. 200:

```json
[ { "id": 7, "name": "intake", "position": 1, "field_count": 24,
    "is_survey": false, "branching_logic": "" } ]
```

**`POST /api/v1/projects/{id}/instruments`** — `project_admin`. Body `{ "name": "intake" }`; the name is unique within the project (REQ-DB-013; a duplicate → 409 `conflict`). 201 — the instrument object (`position` = end of list). Audit `instrument_created`.

**`PUT /api/v1/projects/{id}/instruments/order`** — `project_admin`; idempotent. Body — the full ordered list of the project's instrument ids (a partial list → 400 `invalid_request`):

```json
{ "order": [7, 12, 3] }
```

The record-identifier invariant MUST hold after reordering (GD-8: the first field of the first instrument is the record identifier) — a violation → 400 `invalid_request` (§4.2). Audit `instrument_reordered`.

**`PUT /api/v1/projects/{id}/instruments/{iid}`** — `project_admin`; idempotent (REQ-API-101). Body: `{ "is_survey": true }`, `{ "branching_logic": "[baseline][consent]=\"1\"" }`, or both. An invalid branching expression is rejected at design time → 400 `validation_error` (REQ-VAL-029, `Data_Validation_Design.md` §7.2). 200 — the instrument object. Audit `instrument_updated` with old and new values.

### 4.11 Fields (designer) (REQ-API-067…071, REQ-API-096)

**`GET /api/v1/projects/{id}/instruments/{iid}/fields`** — data access ≥ `read_only`. 200 — the instrument's fields in position order, with all data-dictionary attributes (REQ-DB-013):

```json
[ { "id": 11, "field_name": "age", "field_label": "Age", "field_type": "text",
    "section_header": "", "choices": "", "field_note": "",
    "validation_type": "integer", "validation_format": null, "validation_min": "18", "validation_max": "99",
    "required": true, "branching_logic": "", "calculation": "", "matrix_group": "",
    "personal_information": false, "position": 2 } ]
```

**`POST /api/v1/projects/{id}/instruments/{iid}/fields`** — `project_admin`. Body: the field object minus `id`/`position` (appended at the end of the list). The field name is lower-case alphanumeric + underscore, unique within the project (REQ-DB-013; a duplicate → 409 `conflict`); names longer than 26 characters are accepted — the warning after 26 is a UI concern (master spec). `calculation` is only allowed with `field_type = calculated`; the expression and the branching logic are validated at design time → 400 `validation_error` (REQ-VAL-029, REQ-VAL-033/034; `Data_Validation_Design.md` §6.2, §7.2). 201 — the field object. Audit `field_created`.

**`PUT /api/v1/projects/{id}/instruments/{iid}/fields/{fid}`** — `project_admin`; idempotent. Body: any subset of the attributes — including a `field_name` rename (the stored values are renamed in the same transaction, REQ-VAL-014, `Data_Validation_Design.md` §9), the `calculation` expression of a calculated field (REQ-VAL-033/034), and the `branching_logic` expression (REQ-VAL-029). A change to a calculated field's expression triggers recomputation of all project records in the same transaction (REQ-VAL-037, `Data_Validation_Design.md` §6.3). 200 — the field object. Audit `field_updated` with old and new values.

**`DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}`** — `project_admin`. 204. The field's stored values are removed in the same transaction (DEV-API-5); a field referenced by an active expression → 409 `conflict` (§4.2). Audit `field_deleted` with the count of removed values.

**`PUT /api/v1/projects/{id}/instruments/{iid}/fields/order`** — `project_admin`; idempotent. Body — the full ordered list of the instrument's field ids (a partial list → 400 `invalid_request`):

```json
{ "order": [11, 12, 15] }
```

The GD-8 record-identifier invariant MUST hold after reordering — a violation → 400 `invalid_request` (§4.2). Audit `field_reordered`.

**`POST /api/v1/projects/{id}/records/{record}/fields/{fid}/test`** — `project_admin` + record visibility under the data-access-group rule (REQ-AUTH-045). Body: optionally `{ "expression": "[baseline][a] + [baseline][b] * 2" }` (a draft expression; omitted = the field's stored expression). 200 — the evaluation result with explicit flags for each evaluation problem (REQ-VAL-038, `Data_Validation_Design.md` §6.4):

```json
{ "value": "9", "problems": [] }
```

```json
{ "value": "", "problems": [
  { "operand": "[baseline][c]", "problem": "division_by_zero" },
  { "operand": "[baseline][d]", "problem": "missing_value" } ] }
```

`problem` ∈ `missing_value | non_numeric_operand | division_by_zero`. The call MUST NOT store or modify anything — it is the designer's test action (REQ-API-096; `Data_Validation_Design.md` §6.4).

### 4.12 Instrument–event mapping (REQ-API-072…073)

**`GET /api/v1/projects/{id}/instrument-event-mapping`** — data access ≥ `read_only`. 200 — the instrument × event matrix per arm (checked state per pair, REQ-DB-012):

```json
[ { "arm_num": 1,
    "mapping": { "intake": ["baseline_arm_1", "follow_up_arm_1"],
                 "scores": ["baseline_arm_1"] } } ]
```

Each array lists the events an instrument is mapped to; an empty array = mapped to no event.

**`PUT /api/v1/projects/{id}/instrument-event-mapping`** — `project_admin`; idempotent (REQ-API-042). Body — the full matrix for the supplied arm, replacing that arm's mapping (an unknown instrument or event name → 400 `invalid_request`):

```json
{ "arm_num": 1,
  "mapping": { "intake": ["baseline_arm_1", "follow_up_arm_1"],
               "scores": ["baseline_arm_1"] } }
```

An instrument is active for data entry once it is mapped to at least one event (REQ-DB-012). 200. Audit `mapping_updated`.

### 4.13 Record status (REQ-API-074)

**`GET /api/v1/projects/{id}/record-status`** — data access ≥ `read_only` + record visibility (REQ-AUTH-045). 200 — all visible records, with their instruments per event in the instrument order of each arm and a completion state per (record, event, instrument) — any field has a value vs. none:

```json
[ { "record_id": "8DISC042",
    "events": [ { "unique_event_name": "baseline_arm_1",
                  "instruments": [ { "name": "intake", "complete": true },
                                   { "name": "scores", "complete": false } ] } ] } ]
```

The response MUST NOT contain field values (REQ-API-074). A record-status read is not a record view (`Audit_Logging_Design.md` §8, ASM-AUD-2).

### 4.14 Export (UI) (REQ-API-075…076)

**`GET /api/v1/projects/{id}/export`** — query parameter `format=csv|json` (default `csv`); the response is streamed (REQ-TECH-011). Record selection follows the data-access-group rule (REQ-API-092); the sensitivity follows the acting user's export level per arm (the REQ-API-026 ladder — `export_full` → full dataset; `export_no_identifiers` → identifier fields removed; `export_de_identified` → de-identified per §3.6.1; `export_none` → 403 `forbidden`); for a multi-arm export the least restrictive level applied is the one recorded.

Every call is audit-logged as an `export` event with `surface: "ui"`, the project, and the sensitivity level (REQ-API-076, BR-007, `Audit_Logging_Design.md` §3.5).

### 4.15 Audit log (REQ-API-077…078)

**`GET /api/v1/audit`** — `is_admin` (all projects) or a member of the queried project (REQ-API-078, ASM-API-2). Query parameters:

| Parameter | Meaning |
|---|---|
| `type` | `events` (default) or `views` — selects `audit_events` or `audit_record_views` |
| `project` | project id filter — **required** for a non-admin |
| `user` / `event_type` | filter by user id / event code |
| `from` / `to` | UTC date range (inclusive) |
| `limit` / `cursor` | pagination per §1 |

Reverse chronological (REQ-API-077). 200:

```json
{ "entries": [ { "id": 913, "created_at": "2026-09-18 14:02:11", "source": "ui",
                 "project_id": 33, "user_id": 3, "email": "user@example.org",
                 "event_type": "record_updated", "details": { "action": "update", "…": "…" } } ],
  "next_cursor": null }
```

Entries MAY carry the `token` column and payload values — the trail is the sole sanctioned carrier (REQ-AUD-007, `Audit_Logging_Design.md` §2). The endpoint is read-only: no endpoint exists that writes, updates, or deletes audit data (REQ-DB-024, REQ-API-077).

### 4.16 Record history (REQ-API-079…081)

**`GET /api/v1/projects/{id}/records/{record}/history`** — data access ≥ `read_only` on the record's arm (GD-2) + project visibility (REQ-API-007) + record visibility (REQ-AUTH-045). Query parameters: `instrument`, `event`, `field` (filters, REQ-API-080) and `limit`/`cursor` per §1. Chronological, covering all changes of the record since creation, transparent across the yearly rollover (REQ-AUD-006). 200:

```json
{ "entries": [ { "created_at": "2026-09-18 14:02:11", "user_id": 3, "user_display_name": "User",
                 "action": "update", "instrument": "intake", "event": "baseline_arm_1",
                 "fields": [ { "field": "age", "old": "41", "new": "42" } ] } ],
  "next_cursor": null }
```

`action` ∈ `create | update | delete`; for `create`, `old` is `null`; for `delete`, `new` is `null` and the `old` values are the deleted values (REQ-AUD-009). The endpoint is read-only with respect to the audit trail (REQ-AUD-002). The data entry form presents this per-field history — who entered or changed the value, when, and the old → new values — fetched from this endpoint (REQ-API-081; User_Interface plan, data entry form).

### 4.17 Survey links (GD-9, REQ-API-082…085)

**`GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link`** — data access ≥ `view_edit` on the arm (GD-2) + record visibility (REQ-AUTH-045). The instrument MUST be survey-marked (`is_survey = 1`) — otherwise 400 `invalid_request`. 200 — the stable public link (URL carrying the link token; stable per (project, record, instrument) until revoked, `Database_Schema_Design.md` §8):

```json
{ "url": "https://csms.example.org/s/8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83", "revoked": false }
```

Audit `survey_link_issued`.

**`DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link`** — revokes the link token; the revocation takes effect immediately (REQ-AUTH-040). 204. Audit `survey_link_revoked`.

The data-API behaviour of a link token is fixed by §3.10 (render and fill that (record, instrument) only); the public survey page is served by the PHP application — the browser MUST NOT call `/api/v1/*` from it (GD-1, REQ-API-084).

### 4.18 Data access groups (GD-10, REQ-API-086…094)

**`GET /api/v1/projects/{id}/data-access-groups`** — data access ≥ `read_only`. 200 — the project's groups, possibly an empty array:

```json
[ { "id": 3, "name": "Center A" } ]
```

**`POST /api/v1/projects/{id}/data-access-groups`** — `project_admin` (an `is_admin` user is covered, REQ-AUTH-023). Body `{ "name": "Center A" }` — unique within the project (REQ-AUTH-043; a duplicate → 409 `conflict`). 201 — the group object. Audit `dag_created`.

**`DELETE /api/v1/projects/{id}/data-access-groups/{gid}`** — `project_admin`. 204 — the group and its member assignments are removed; while records are still assigned → 409 `conflict` (ASM-AUTH-4; a rejected call writes no audit entry, REQ-AUD-004). Audit `dag_deleted`.

**`PUT /api/v1/projects/{id}/users/{uid}/data-access-groups`** — `is_admin` (REQ-AUTH-044). Body — the member's group assignments and the active one; an empty list clears:

```json
{ "groups": [3, 7], "active_group_id": 3 }
```

Exactly one active group when `groups` is non-empty (otherwise 400 `invalid_request`). 200. Audit `dag_membership_changed`.

**`PUT /api/v1/projects/{id}/active-data-access-group`** — self-service for the acting member (who holds a non-empty assignment). Body `{ "group_id": 7 }` — MUST be one of the member's assigned groups (otherwise 400 `invalid_request`). The switch takes effect immediately (REQ-AUTH-046). 200. Audit `dag_active_switched`.

**`PUT /api/v1/projects/{id}/records/{record}/data-access-group`** — `project_admin` (REQ-AUTH-048); the record MUST be visible to the acting user (REQ-AUTH-045). Body `{ "group_id": 3 }` or `{ "group_id": null }` (unassign). 200. Audit `dag_record_assigned`.

Record scope and transformation are orthogonal (REQ-API-092, REQ-AUTH-045): the group governs **which records** the holder sees on both export surfaces; the export level governs **how** the data is transformed (the §3.6.1 ladder). A record created via import takes the holder's active group, or none (REQ-API-093, §3.7.2).

### 4.19 i18n (GD-12, REQ-API-097…100)

**`GET /api/v1/i18n/languages`** — any authenticated user. 200 — the enabled languages (code, display name):

```json
[ { "code": "en", "display_name": "English" }, { "code": "nb", "display_name": "Norsk bokmål" } ]
```

**`PUT /api/v1/users/me/ui-language`** — any authenticated user (acting on themselves). Body `{ "language": "nb" }` — MUST be an enabled language (otherwise 400 `invalid_request`); the default is `en`. The setting persists across sessions (REQ-DB-008). 200 — the user object.

**`GET /api/v1/i18n/strings?language=<code>`** — `is_admin`. 200 — the translation keys with the current translation and a missing flag (a missing key falls back to English, REQ-DB-031):

```json
[ { "key": "ui.dashboard.title", "text": "Oppsummering", "missing": false },
  { "key": "ui.record.history", "text": "", "missing": true } ]
```

**`PUT /api/v1/i18n/strings`** — `is_admin`. Body — upsert of translations; an empty `text` removes the translation (fallback to English, REQ-DB-031):

```json
{ "language": "nb", "entries": [ { "key": "ui.dashboard.title", "text": "Oppsummering" } ] }
```

200. Audit `i18n_updated` per changed key.

## 5. Permission summary

The normative endpoint → permission mapping is in `API_Endpoints_Requirement.md` §4.19; it is reproduced here as an overview:

| Endpoints | Required permission |
|---|---|
| data API `/api/` | the token's levels per §3 (data/export level per arm; link tokens scoped per §3.10) |
| `GET/POST /api/v1/users`, `PUT /api/v1/users/{id}` | `is_admin` |
| `POST /api/v1/projects` | `is_admin` |
| `GET /api/v1/projects` | project visibility (REQ-API-007) |
| `GET /api/v1/projects/{id}` | data access ≥ `read_only` + visibility |
| `PUT /api/v1/projects/{id}` | `project_admin` |
| `GET/PUT …/users` (members), `GET/POST …/roles` | `is_admin` |
| `GET …/users/{uid}/token` | self-service: acting user is a member (REQ-API-102) |
| arms, events, instruments, fields, mapping — mutations (POST/PUT/DELETE) | `project_admin` |
| arms, events, instruments, fields, mapping — reads (GET) | data access ≥ `read_only` |
| `GET …/record-status`, `GET …/records/{record}/history` | data access ≥ `read_only` (+ record visibility) |
| `POST …/fields/{fid}/test` | `project_admin` + record visibility |
| `GET …/export` | export level per arm (GD-2; `export_none` → 403) |
| survey links (issue/revoke, §4.17) | data access ≥ `view_edit` on the arm (+ record visibility) |
| `GET …/data-access-groups` | data access ≥ `read_only` |
| `POST/DELETE …/data-access-groups`, `PUT …/records/{record}/data-access-group` | `project_admin` |
| `PUT …/users/{uid}/data-access-groups` | `is_admin` |
| `PUT …/active-data-access-group` | an assigned member (self-service) |
| `GET /api/v1/audit` | `is_admin` (all projects) or a member of the queried project (REQ-API-078) |
| `GET /i18n/languages`, `PUT /users/me/ui-language` | any authenticated user |
| `GET/PUT /i18n/strings` | `is_admin` |

`is_admin` users hold all permission levels on every arm of every project (REQ-AUTH-023), so a permission requirement never excludes an administrator.
