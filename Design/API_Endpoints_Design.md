# API Endpoints — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/API_Endpoints_Requirement.md`\
**Date:** 2026-09-21

## 1. Purpose and Conventions

The normative endpoint contracts — parameter tables, request/response schemas, status codes, error formats — for both surfaces. The requirements document fixes *what* (which endpoint does what, under which permission); this document fixes *shape*.

- **Two surfaces** (REQ-API-001): the REDCap-compatible data API at `POST /api/` (and `GET /api/`) and the administration API under `/api/v1/`. Beyond these, only the documentation and health endpoints exist (REQ-API-003).
- **UTC and UTF-8** in all requests and responses for **system** timestamps (REQ-API-004, GD-7); clinical date/date-time values carry their collection timezone in the canonical form (GD-16, REQ-VAL-041, `Data_Validation_Design.md` §4.1).
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
| `content` | `project` / `metadata` / `event` / `formEventMapping` / `exportFieldNames` / `generateNextRecordName` / `record` | — (required) | with `content=record`, a call carrying `data` is an import, one without it an export; deletion needs `action=delete` (REQ-API-012) |
| `action` | `export` / `import` / `delete` (with `content=record` only) | — | optional: `export`/`import` are accepted and never override the `data` rule; `delete` is the only action that dispatches; any other value → REDCap-style error (REQ-API-012, DEV-API-24) |
| `format` / `returnFormat` | `json` / `csv` | `csv` (REDCap default) | a present `returnFormat` takes precedence for the response encoding (REQ-API-013) |
| `type` | `flat` / `wide` | `wide` (REDCap default) | `flat` is normative — one row per (record, event); `wide` is the simplified compatibility mode of §3.6.2 (DEV-API-1) |
| `csvDelimiter` | single character | `,` | empty value = comma (REQ-API-029) |
| `records[]` / `fields[]` / `forms[]` / `events[]` | arrays | — | both REDCap array syntax (`records[0]=…&records[1]=…`) and repeated single values (REQ-API-015) |
| `filterLogic` | expression (§3.6.3) | — | export only (REQ-API-025) |
| `rawOrLabel` | `raw` / `label` | `raw` | choice fields (REQ-API-027) |
| `rawOrLabelHeaders` | `raw` / `label` / `both` | `raw` | field names; `both` renders each header as `<Field Label> (field_name)` — the bare name where the field carries no label (REQ-API-027) |
| `data[i][key]` / `data` | import rows (§3.7.1) | — | import only; indexed form parameters or a JSON array of record objects — its presence makes the call an import (REQ-API-012, REQ-API-031) |
| `returnContent` | `count` (or any other value) | result rows | with `content=record` import: `count` answers `{"count": N}` for the rows applied; anything else keeps the §3.7.2 result rows (REQ-API-142, DEV-API-24) |
| `tz` | IANA timezone name or `±HH:MM` offset | `APP_TIMEZONE` (REQ-CFG-026) | import only — timezone of collection for the call's date/date-time values (GD-16, REQ-VAL-041, REQ-API-031) |
| `exportCheckboxLabel`, `exportSurveyFields`, `exportDataAccessGroups` | any | — | accepted and **ignored** (REQ-API-016, DEV-API-2) |

Unknown parameters are accepted and ignored, never rejected — existing callers keep working (REQ-API-017).

### 3.2 Error Format (REQ-API-039, REQ-API-011)

The error body is rendered in the requested response format (or `csv` when omitted):

| `returnFormat` | Error body |
|---|------|
| `json` | `{ "error": "Invalid token" }` |
| `csv` | `Invalid token` (single line) |

HTTP status mapping (REQ-API-039):

| Situation | Status | `error` string |
|---|---|---|
| missing/invalid token | 401 | `Invalid token` (same body whether or not the token exists — REQ-AUTH-032) |
| unknown/missing `content`, or `record` without supported `action` | 400 | `Invalid content` |
| insufficient permission (incl. `export_none`, read-only import) | 403 | `Permission denied` |
| write to an analysis-mode project (`import` / `delete`; survey submissions included) | 403 | `Project in analysis mode` (GD-20, REQ-API-109) |
| rate limit exceeded (when enabled) | 429 | `Rate limit exceeded` (with `Retry-After`, §3.9) |
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
  "project_end_provision": "",
  "project_organizational": "NAT EU",
  "project_creation_time": "2026-01-01 09:00:00",
  "project_pat_import_folder": "",
  "surveys_enabled": "0",
  "randomization_enabled": "0"
}]
```

Standard REDCap project-info keys the system does not store are returned with neutral values (`"0"` / `""`) rather than omitted, so naive parsers keep working (REQ-API-018) — including `project_end_provision` since GD-17 (the end provision is no longer stored; if the owner records one, it is data in an instrument such as `DataTransferProjects`, REQ-DB-032). CSV: a single row of those key/value pairs.

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
    "record_identifier": "Y",
    "direct_identifier": "Y"
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

`record_identifier` is `"Y"` on the record-identifier field (position 1 of instrument position 1, GD-8) and empty elsewhere. `direct_identifier` is this system's key (`"Y"`/`""`, REQ-DB-013, REQ-EXP-020 — an additional key beyond the REDCap shape, tolerated by naive parsers per REQ-API-018). `choice_codes`/`choice_labels` are comma-joined from the stored `code$label##code$label` encoding. `forms[]` restricts the response to those instruments when supplied — the mechanism §3.10 uses so a survey link sees its own instrument's definitions and no other (REQ-API-083).

### 3.5 `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`

All require data access ≥ `read_only` except `generateNextRecordName` (≥ `view_edit` on the target arm — REQ-API-023).

| content | Response shape (JSON) |
|---|------|
| `event` (REQ-API-020) | `[ {"event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","event_id":4}, … ]` — events in the **canonical per-arm order** (GD-15: timepoint events by `period` ascending, ties by position; then no-timepoint events by position) |
| `formEventMapping` (REQ-API-021) | `[ {"form_name":"intake","event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","form_event_mapping":"1"}, … ]` — events in the canonical per-arm order (GD-15) |
| `exportFieldNames` (REQ-API-022) | `[ {"field_name":"record_id","form_name":"intake"}, … ]` — restricted to `forms[]` when supplied |
| `generateNextRecordName` (REQ-API-023) | `{ "next_record_name": "8DISC042" }` |

`generateNextRecordName` follows the project's naming pattern (REQ-DB-007) — digit-placeholder style (`8DISC[0-9][0-9][0-9]`) and counter-prefix style (`0001_01`, width preserved) — and MUST NOT return a name an existing record already holds (REQ-API-023). The next name is one counter step above the greatest existing name of the same shape (max + 1); names freed by deleted records are not reused (no gap-filling, REQ-API-023).

### 3.6 Record export — `content=record` without `data` (REQ-API-012)

An explicit `action=export` may accompany the call and changes nothing; a `data` parameter on the same call makes it an import instead (§3.7).

#### 3.6.1 Rules

- Filters `records[]`, `fields[]`, `forms[]`, `events[]` combine (intersection); with no filters, all records visible to the holder under the data-access-group rule are returned (REQ-API-024, REQ-AUTH-045, REQ-API-092).
- Sensitivity per the token holder's export level for the arm(s) of the exported data (REQ-API-026): `export_full` → full dataset; `export_no_identifiers` → all identifier fields removed; `export_de_identified` → de-identified per `Data_Export_Anonymization_Requirements.md` (date shift per `Database_Schema_Design.md` §8, salt/range per `System_Configuration_Design.md` §3.6); `export_none` → 403 `Permission denied`.
- `rawOrLabel=label` → choice labels for dropdown/radio fields (stored values remain codes, REQ-VAL-022); `rawOrLabelHeaders` controls field names the same way (REQ-API-027).
- Rows carry the record identifier's value under its field name (GD-8), `redcap_event_name` per row (flat, projects with events), and empty strings for missing values (REQ-API-028).
- Each instrument's field block ends with an `<instrument>_complete` column carrying that instrument's three-state completion for the row's position — `0` not complete, `1` in progress, `2` complete (REQ-API-134). The state is the record-status derivation of §4.13 (REQ-API-074): stored `finished` → `2`, else any value of that instrument at that position → `1`, else `0`; a survey-marked instrument always exports `2` (GD-9); a `finished` assignment on an unmapped (event, instrument) pair stays invisible until the pair is mapped back. The column belongs to whole-instrument exports — a full export or one filtered by `forms[]` carries it for every instrument that contributes at least one value column; a `fields[]`-filtered export omits it entirely (the web application exports whole instruments and gets the column; a field-list export returns exactly the requested fields). It keeps its raw name under `rawOrLabelHeaders` (it names no field), and appears in JSON as an ordinary key. Import does not accept it: a `<instrument>_complete` key remains an unknown field (§3.7).
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
    "notes": "",
    "intake_complete": "1"
  }
]
```

`wide` (DEV-API-1, simplified for caller compatibility): one row per record; a field present in exactly one event keeps its bare name; a field present in several events is emitted once per event as `<field>_<unique_event_name>`. An instrument's `<instrument>_complete` follows the same rule — suffixed once per event when the instrument is active in several (REQ-API-134). Missing values are empty strings. `flat` is what all known callers use; `wide` exists so requests that omit `type` (REDCap default) keep working.

#### 3.6.3 `filterLogic` (REQ-API-025)

Minimum normative support: equality on string fields, `[field]="value"`, and compound conditions with `&&` / `||` and parentheses — the full grammar (value comparisons `=`, `!=`, `<`, `>`, `<=`, `>=`, numeric/chronological/string comparison semantics, `text_contains`, `is_blank`, `is_not_blank`, precedence) is the branching-logic grammar of `Data_Validation_Design.md` §7, evaluated against the record's stored values. A single-segment reference `[field]` names no event and falls back to the project's **first event** in canonical order GD-15 (`Data_Validation_Design.md` §7.1). `filterLogic` filters which **records** are returned; it never changes the sensitivity level.

### 3.7 Record import — `content=record` with `data` (REQ-API-012)

The presence of the `data` parameter makes the call an import; an explicit `action=import` may accompany it and changes nothing. Requires data access ≥ `view_edit` on the record's arm (REQ-API-033); a `read_only`/`no_access` token is rejected (403 `Permission denied`). In **analysis mode** every import is rejected — 403 `Project in analysis mode` (GD-20, REQ-API-109); reads and exports are unaffected.

#### 3.7.1 Request shape (REQ-API-031)

Each row is a (record, form, event) tuple of field values, supplied in either REDCap encoding:

```
token=…&content=record
&data[0][record_id]=8DISC042&data[0][form_name]=intake&data[0][event_name]=baseline_arm_1
&data[0][age]=42&data[0][status]=2&data[0][notes]=ok
&data[1][record_id]=8DISC043&data[1][form_name]=intake&data[1][event_name]=baseline_arm_1
&data[1][age]=&data[1][status]=1
```

or as a single `data` parameter holding a **JSON array of record objects** — the encoding of the recorded callers (DEV-API-24), whose values may be JSON numbers or booleans and are coerced to their string spelling:

```
token=…&content=record&format=json&type=flat&overwriteBehavior=overwrite
&returnContent=count
&data=[{"record_id":"1.3.6.1.4.1.45037.411…","ids7_patient_name":"ENDO_MONT_049",
        "ids7_number_of_series":18,"ids7_study_date":"20221202"}]
```

A row may omit `form_name` — a **flat row**: every supplied field is stored on its own instrument from the data dictionary, so a caller can post back an export without naming forms. The event rules are unchanged: a project with events still requires each row to name its event (`event_name`/`redcap_event_name`). Indexed parameters win when a call carries both encodings. `overwriteBehavior` and `forceAutoNumber` are accepted and ignored (REQ-API-017): import always upserts (REQ-API-033) and never assigns record ids itself.

Each value passes the full validation pipeline (`Data_Validation_Design.md` §2) before storage; invalid values are not stored (REQ-API-032, REQ-VAL-001). Empty values act as "no value" (clear/no-op, REQ-VAL-024) — an **intentional** clear: the UI data-entry path sends empties only for fields the user explicitly cleared (GD-14, REQ-UI-031). Values for calculated fields are rejected (`CALCULATED_READONLY`, REQ-API-095). Date/date-time values are stored with the collection offset — the `tz` parameter when present (UI: the browser's zone, sent by PHP), else `APP_TIMEZONE` (GD-16, REQ-VAL-041).

The row's event key is accepted as `event_name` **or** `redcap_event_name` (REQ-API-138): the latter names the same value in a flat export (REQ-API-028), so a caller can post back what it pulled without renaming. An empty value means "not supplied", so one spelling is in force per row; two different events name an ambiguous target and the row is rejected — `event_name: CONTENT_INVALID — event_name 'baseline_arm_1' conflicts with redcap_event_name 'followup_arm_1'` — storing nothing of it (REQ-API-035). The alias applies to projects **with** events: a project without events stores under the empty event name, so there the alias would move the row's values out of the slot the rest of the code reads (calculated fields recompute into it) and it is ignored instead.

#### 3.7.2 Response (REQ-API-034, REQ-VAL-008, REQ-API-142)

With `returnContent=count` the response is HTTP 200 and the number of rows applied (added + updated) — `{"count": 86}` in JSON, a bare count line in CSV; rejected rows are not counted and no per-row detail is rendered (REQ-API-142). Otherwise — any other value or none — HTTP 200 with one result row per imported record:

```json
[
  { "record_id": "8DISC042", "form_name": "intake", "import_record_id": 2, "import_form_name": "intake" },
  { "record_id": "8DISC043", "form_name": "intake", "import_record_id": 0,
    "import_form_name": "Validation error: age: TYPE_INVALID — value \"abc\" is not a valid integer; status: CHOICE_INVALID — value \"9\" is not a choice of \"status\"" }
]
```

| `import_record_id` | Meaning |
|---|------|
| `1` | record added |
| `2` | record updated |
| `0` | validation error(s) — `import_form_name` lists the per-field detail `<field>: <CODE> — <message>`, joined by `; ` (rule codes from `Data_Validation_Design.md` §3) |
| `255` | fatal request-level error — e.g. unknown `content`; the whole call fails (HTTP 400 with the §3.2 error body) |

The detail's order is fixed (REQ-API-139): the row's field problems in data-dictionary order, then its unknown keys as `<field>: UNKNOWN_FIELD — unknown field '<name>'` in dictionary order, so a rejected batch reads the same every time and a caller can diff two responses. A row stops at its first **tuple** problem — a missing `record_id`, an unknown form, or an unknown event reports that alone and skips the row's remaining field detail (REQ-API-140 is open on whether it should report everything instead); a row without `form_name` has no such problem, it is flat (§3.7.1). An unknown event names the key the caller used: `redcap_event_name: CONTENT_INVALID — unknown event 'v1_arm_1_arm_1'` for a row that sent the alias.

All-or-nothing per record: a failed value stores nothing of that record (single transaction, REQ-API-035); other records in the same call are unaffected. Successful imports are audit-logged (`record_created`/`record_updated` with old/new values) and trigger calculated-field recomputation in the same transaction (REQ-API-095, REQ-VAL-037). A new record is assigned to the holder's active data-access group, or none (REQ-API-093); an import never changes an existing record's group.

#### 3.7.3 Open — aggregated error reporting (REQ-API-140, draft)

The master spec's "Error messages by api" note shows one invalid value reported once for the whole call (`{"error":"The following values of redcap_event_name are invalid: v1_arm_1_arm_1"}`); this implementation answers one result row per `data[]` entry, so a batch of eight rows sharing that event repeats the same detail eight times (REQ-API-034) and never produces the aggregate. The deviation is deliberate pending the two decisions of REQ-API-140 — request-level body versus an addition to the §3.7.2 rows, and first-problem versus every-problem per row — because each changes what a caller may rely on: a request-level body costs the `1`/`2` results of the rows that were fine (REQ-API-035's per-record granularity), while reporting every problem per row means reordering the tuple checks in `importOneRow` (`api/internal/dataapi/record_import.go`), which is also where the arm-permission and data-access-group rejections sit. Until it is decided, §3.7.2 as written is normative, and nothing in the master spec example should be read as implemented behavior.

### 3.8 `content=record&action=delete` (GD-3, REQ-API-036)

Requires data access ≥ `delete` on the record's arm. In analysis mode the call is rejected — 403 `Project in analysis mode` (GD-20, REQ-API-109). Removes the record's values — scoped by the supplied `records[]`/`events[]`/`fields[]`, or the whole record — and, when a record's last value is removed, the record itself. Audit: `record_deleted` **with the deleted values** (REQ-AUD-009, `Audit_Logging_Design.md` §3.2).

Request: `token=…&content=record&action=delete&records[0]=8DISC042` (optionally `events[]`/`fields[]` to scope).

Response (JSON):

```json
[ { "record_id": "8DISC042", "form_name": "", "deleted": 1 } ]
```

### 3.9 Rate Limiting (REQ-API-038, REQ-API-113, REQ-CFG-020)

The limiter counts **per source IP address** and covers both surfaces — `/api/` and `/api/v1/` — so web-application traffic (through PHP) and external scripts are limited alike (master spec "Rate limitter"). The data API enforces it inside its handler (where the requested error format is known); the administration surface at the boundary middleware in `httpapi`. Both share one limiter instance per API process (`httpapi.NewMux` passes the data handler's limiter into the middleware), so an address blocked on one surface is blocked on the other too. The enable flag and both thresholds are the system settings of §4.22, read from `system_settings` per request — a saved change is effective on the next request, no restart (REQ-API-112). When enabled, each source IP may make `rate_limit_rpm` requests per rolling minute (default 600); a call over the limit is answered with HTTP 429 and the §3.2 error body (`Rate limit exceeded`). In-memory by design — the API stays stateless and a restart only resets the windows and the blocks.

**Blockout (REQ-API-113).** Over the budget is not merely refused once: the first call that would exceed `rate_limit_rpm` blocks the source IP for `rate_limit_block_minutes` (default 10 minutes), answering 429 with a `Retry-After` header naming the remaining whole seconds (rounded up). While blocked, every request from that IP is answered the same way — before any token lookup, and without recording a hit or moving the blockout end. The period therefore runs from the first rejection, not from the last one: a client that keeps calling cannot extend its own lockout indefinitely. When the period elapses the address is admitted again with no memory of the offence (its window starts empty).

```
t=0        601st call in the window → 429, Retry-After: 600   blocked until t=10:00
t=0:30     call                     → 429, Retry-After: 570   block unchanged
t=9:50     call                     → 429, Retry-After: 10    block unchanged
t=10:05    call                     → served; fresh window, budget full again
```

The state is two maps keyed by source IP — the hit timestamps of the rolling window and the blockout end — guarded by one mutex. Entries are dropped when their block has expired and their newest hit left the window; a sweep doing this for every key runs at most once per minute inside the call that notices, so no goroutine and no timer is needed and the maps stay bounded by the distinct caller addresses seen in a minute (plus blocked ones).

**Source IP (REQ-API-125).** When the direct TCP peer is inside `TRUSTED_PROXY_CIDRS` (`System_Configuration_Design.md` §3.9), the limiter keys on the proxy-provided `X-Real-IP`; otherwise on the connection's remote address; a client-supplied `X-Real-IP` from an untrusted peer is ignored. nginx overwrites `X-Real-IP` with its own `$remote_addr` on every routed request and never appends a chain (`Technology_Stack_Design.md` §5), so each external caller keeps a distinct address. Web-application calls arrive server-side from PHP on the application host: PHP forwards the browser's address (its own `REMOTE_ADDR`, which nginx sets) in `X-Real-IP`; loopback is trusted by default, so per-browser limiting works despite the shared server hop.

### 3.10 Survey Link Tokens (GD-9, REQ-API-083)

A survey link token (`survey_links.token`, `Database_Schema_Design.md` §8) is accepted as the `token` parameter of the data API, but only for the calls that render and fill its (record, instrument):

| Call | Allowed scope |
|---|------|
| `content=metadata` | the field definitions of that instrument (a `forms[]` naming another instrument → 403) |
| `content=record` with `data` (REQ-API-012) | values for that record and that instrument |

Every other `content` (including `export` and `delete`), another record, or another instrument is rejected with 403 `Permission denied` (REQ-API-083, REQ-AUTH-039). In an analysis-mode project the permitted import is rejected too — `Project in analysis mode` (GD-20, REQ-API-109); the survey page then shows its closed state (`User_Interface_Design.md` §8.8). A revoked link is rejected on every call (REQ-AUTH-040). Link tokens are subject to the §3.9 rate limit (REQ-API-038). Submissions are audit-logged as `survey_submitted` — success and failure (`Audit_Logging_Design.md` §3.6). The public survey page is served by the PHP application; the browser never calls `/api/v1/*` from it (GD-1, REQ-API-084).

## 4. Administration API — `/api/v1/`

### 4.1 Boundary and Authentication (REQ-API-040, REQ-API-041)

- Reachable only from the trusted internal path (REQ-TECH-018, REQ-AUTH-014); the proxy strips `X-Internal-Service-Token` and `X-Internal-User-Id` from every externally-originated request (`Technology_Stack_Design.md` §5). The browser MUST NOT call this surface directly (GD-1, BR-006, REQ-API-040).
- Every request MUST present a valid `X-Internal-Service-Token` and `X-Internal-User-Id` (REQ-API-041, REQ-AUTH-011…013); the exceptions are `POST /api/v1/auth/login` (§4.3, `Authentication_Authorization_Design.md` §2.3), the three pre-authentication password endpoints of Sequence H (§4.3, `Authentication_Authorization_Design.md` §2.8 — DEV-API-16), and `POST /api/v1/auth/verify-password` (§4.3, `Authentication_Authorization_Design.md` §2.9 — DEV-API-17), which carry the service token only.
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
|---|------|------|
| 400 | `invalid_request` | malformed JSON body; a missing or invalid attribute (e.g. non-survey instrument for a link, §4.17); a GD-8 identifier-invariant violation (§4.10, §4.11) |
| 400 | `validation_error` | design-time rejection of a data-dictionary entry — ill-formed expression, unknown/inactive reference, cycle, malformed name (`Data_Validation_Design.md` §6.2, §7.2, §9); the reason is in `message` (REQ-VAL-029) |
| 401 | `service_token_invalid` | missing/invalid `X-Internal-Service-Token` (audit `admin_rejected`) |
| 401 | `account_not_found` | login with an email that has no user row (REQ-AUTH-006; audit `login_failure`) |
| 401 | `bad_password` | local login: the account has no stored hash, or the password does not match (GD-18, REQ-AUTH-050; the two cases are not distinguished; audit `login_failure`) |
| 401 | `invalid_setup_token` | invite/reset completion with an unknown, expired, consumed, or wrong-purpose token (Sequence H, §4.3; the causes are not distinguished, REQ-API-120/121) |
| 403 | `forbidden` | insufficient permission; project or record outside the acting user's visibility (uniform, REQ-API-007); unknown/disabled acting user (audit `admin_rejected`) |
| 403 | `account_disabled` | login with a disabled account (including auto-disabled by the inactivity rule — `account_auto_disabled` audit first, REQ-AUTH-053; audit `login_failure`) |
| 403 | `account_expired` | login with an account whose `valid_until` has passed (GD-19, REQ-AUTH-052; audit `login_failure`) |
| 404 | `not_found` | an unknown path resource (a user, project, arm, event, instrument, field, or group that does not exist) |
| 409 | `conflict` | a state violation — duplicate name (project, role, event label, instrument, field, group); deleting an arm that still has events or data (DEV-API-6; the last remaining arm is renamed instead, §4.8 REQ-API-128); deleting a group that still has records (ASM-AUTH-4); deleting a field referenced by an active expression, or an instrument whose doomed fields are named by a surviving expression (§4.10, REQ-API-127); an event rename colliding with an existing `unique_event_name` (§4.9); a mode transition outside the allowed set, any mode change while a staging set is open, opening a second staging set, committing breaking changes without acknowledgement, a structure change in production mode while no staging set is open, or a breaking structure change in analysis mode sent without `acknowledge_breaking` (§4.21) |
| 500 | `internal` | unexpected failure; `message` carries no details |

### 4.3 Session (REQ-API-044, REQ-API-045)

**`POST /api/v1/auth/login`** — called by the PHP application after successful OAuth2/LDAP authentication, **or with `source: "local"` and the password for table-based login** (GD-18, REQ-AUTH-050/051; REQ-AUTH-016). Exempt from `X-Internal-User-Id`, like the Sequence H endpoints below (DEV-API-16); it still requires the service token. Body:

```json
{ "email": "user@example.org", "source": "oauth2", "provider": "https://idp.example.org" }
```

```json
{ "email": "user@example.org", "source": "local", "password": "***" }
```

```json
{ "email": "user@example.org", "source": "local", "password": "***", "mfa_code": "492817" }
```

```json
{ "email": "user@example.org", "source": "local", "first_factor": "eyJ2IjoiZmYxIiw…", "mfa_code": "492817" }
```

On the second call of a two-factor challenge the handle from `POST /api/v1/auth/verify-password` below stands in for `password` (REQ-API-131, DEV-API-19): the user enters one code, and nothing between the two calls has to hold their password. A handle is accepted only where a second factor still guards the account, so it cannot complete a login by itself; a forged, misdirected or expired one answers 401 `first_factor_expired` and does not count toward the lockout (REQ-AUTH-035).

An optional `source_name` carries the authentication-source name the user selected on the login page (REQ-AUTH-067); it is recorded in the `login_success` / `login_failure` audit details and never affects identity resolution or authorization.

An optional `attempts` carries the per-source outcomes of a failed named-source credential race (`Authentication_Authorization_Design.md` §2.9) so the single finalizing call yields exactly one `login_failure` with the losing attempts' detail (`Audit_Logging_Design.md` §3.1, DEV-AUD-5):

```json
{ "email": "user@example.org", "source": "ldap", "provider": "ldap-1",
  "source_name": "Hospital 2", "attempts": { "local": "bad_password", "ldap-2": "unreachable" } }
```

The map is source-id → outcome (e.g. `bad_password`, `unreachable`) and MUST never contain credentials (REQ-AUTH-036). With attempts present, a credential rejection settles as `bad_credentials` — or `provider_unavailable` when every attempt was `unreachable`; account-state rejections keep their specific reason.

Such a call **reports a failure and authenticates nothing** (REQ-API-135, DEV-API-21): no user object, no bootstrap promotion, no `auth_source`/`last_login_at` write, and it never reaches the two-factor gate — for `source: "ldap"` the API trusts the PHP layer's word, so falling through would turn a race that bound nobody into a session. The `password` attribute is ignored on it. A successful login carries no `attempts`.

**`POST /api/v1/auth/verify-password`** — `{ "email": "…", "password": "***" }`: the side-effect-free verify step of the named-source credential race (`Authentication_Authorization_Design.md` §2.9, REQ-API-123). Runs the login endpoint's hash check and account-active rule only and answers `ok` / `bad_password` / `account_disabled` / `account_expired` — no `last_login_at`/`auth_source` write, no audit event, no user object; login finalizes exactly once through `POST /api/v1/auth/login`. Pre-authentication: service token only, no `X-Internal-User-Id` (DEV-API-17); password never logged (REQ-AUTH-036).

For an account a second factor still guards, the `ok` answer additionally carries the handle the challenge's second login call presents in place of the password (REQ-API-131, DEV-API-19):

```json
{ "status": "ok", "first_factor": "eyJ2IjoiZmYxIiw…", "expires_in": 300 }
```

The handle is `base64url(JSON claims) + "." + base64url(HMAC-SHA256)` over the claims — version, user id, expiry — signed with a key derived from `INTERNAL_SERVICE_TOKEN` for this one purpose (`hmac(service_token, "clara/first-factor/v1")`). It is stateless by construction, so §2.7's "the API keeps no challenge state" still holds; it is bound to one user id, lives 5 minutes (the `tfa_pending` lifetime), and is issued only where gate 1.5 would still close behind it — an account with `method: "off"` and no mandate gets `{"status":"ok"}` and nothing else. It never appears in a log line or an audit detail.

Processing (in order, `Authentication_Authorization_Design.md` §2.3): for `source: "local"` — the row exists (401 `account_not_found`) and the bcrypt hash matches in constant time (else 401 `bad_password`; the password is never logged, REQ-AUTH-036), or an unexpired handle from `verify-password` stands in for the hash check where a second factor still guards the account (else 401 `first_factor_expired`, REQ-API-131); the account is active per the rule of `Authentication_Authorization_Design.md` §4.4 — `enabled`, not expired (403 `account_expired`), not inactive (auto-disable + 403 `account_disabled`, REQ-AUTH-053); **two-factor gate** (`source: "local"`/`"ldap"` only, GD-21, §2.7): method not `off` and no `mfa_code` → 401 `{"error":"mfa_required","method":"totp|email"}`; invalid/expired/replayed `mfa_code` → audit `login_failure` (`bad_mfa_code`) + 401 `bad_mfa_code`; `AUTH_REQUIRE_2FA` on with method `off` → 401 `{"error":"tfa_enrollment_required","user_id":<id>}` — the pending identity's id lets PHP drive the enrollment wizard (§4.4 TFA endpoints, presented as `X-Internal-User-Id`) before a session exists (`Authentication_Authorization_Design.md` §2.7); bootstrap-admin promotion (REQ-AUTH-007, DEV-AUTH-15): if the email equals `ADMIN_BOOTSTRAP_EMAIL`, create the row when absent (enabled administrator — the first-installation setup case); never re-enable a disabled account; set `is_admin = 1` on an existing enabled row only for `source: "local"` or while no other enabled administrator exists; set the row's `auth_source` and `last_login_at` (REQ-AUTH-005, REQ-AUTH-053); audit `login_success` with the source (and the factor used when the gate applied, REQ-AUD-028). The API MUST NOT create or store a session (GD-1).

200 — the user object (used by all §4.4 user endpoints):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "enabled": true, "is_admin": true,
  "auth_source": "oauth2", "ui_language": "en", "ui_theme": null,
  "last_login_at": "2026-09-20 08:14:05", "valid_until": null, "status": "active" }
```

`last_login_at` is `null` when the account has never logged in; `valid_until` is `null` when indefinite; `status` ∈ `active | disabled | expired | auto_disabled` (derived — GD-19, REQ-AUTH-052/053); `ui_theme` is `null` when the account follows the installation default theme (`UI_THEME`, GD-26, REQ-DB-008).

**`POST /api/v1/auth/logout`** — records the `logout` audit event (REQ-AUTH-008) and returns 200. Destruction of the PHP session remains the PHP layer's responsibility, performed after this call (GD-1, REQ-AUTH-015; `Authentication_Authorization_Design.md` §2.4).

**Out-of-band password endpoints (Sequence H — GD-22/GD-23).** Pre-authentication: service token only, no `X-Internal-User-Id` (DEV-API-16); full mechanics in `Authentication_Authorization_Design.md` §2.8.

| Endpoint | Contract |
|---|------|
| `POST /api/v1/auth/password-reset/request` | body `{ "email": "…" }`; **always 202 with an identical body** — whether or not a matching active local account exists (no enumeration, REQ-AUTH-062); when one does, emails the set-password link with a single-use `reset` token (`password_tokens`, REQ-DB-039; TTL `AUTH_PASSWORD_TOKEN_TTL_DAYS`, REQ-CFG-030); rate-limited per address and IP (default 3/15 min/address); audit `password_reset_requested` (address + IP, never the token) (REQ-API-119) |
| `POST /api/v1/auth/password-reset/complete` | body `{ "token": "…", "password": "***" }`; constant-time hash verification, purpose `reset`, expiry, single use; success stores the bcrypt hash, consumes the token, invalidates all outstanding tokens of the account, audits `password_reset_completed` → 200 `{ "ok": true }`; any failure → generic 401 `invalid_setup_token`; no temporary password exists (REQ-AUTH-062, REQ-API-120) |
| `POST /api/v1/auth/invite/complete` | as above for purpose `invite` — sets the invited user's own chosen password, audits `invite_accepted`, generic 401 on any failure; login afterwards runs Sequence F with the full second-factor gate (REQ-AUTH-060, REQ-API-121) |

### 4.4 Users (REQ-API-046…048)

All three require `is_admin`; a call by a non-admin is rejected (403 `forbidden`).

| Endpoint | Contract |
|---|------|
| `GET /api/v1/users` | 200 — array of user objects (`id`, `email`, `display_name`, `enabled`, `is_admin`, `auth_source`, `last_login_at`, `valid_until`, `status`, **`tfa_method`** (`off` \| `totp` \| `email`, GD-21, REQ-API-116) — the full user object of §4.3) (REQ-API-046) |
| `POST /api/v1/users` | body `{ "email": "…", "display_name": "…", "valid_days": 90, "password": "***" }` (`valid_days` ≥ 0, `0` = indefinite; `password` optional — stored only as a bcrypt hash, REQ-AUTH-050); a new account → 201 user object; a disabled account with the same email is re-enabled → 200 user object (re-enabling resets the inactivity clock, REQ-AUTH-053) (REQ-API-047); audit `user_created` (`re_enabled` flag, `valid_until`) |
| `PUT /api/v1/users/{id}` | body — any subset of `{ "enabled": true\|false, "is_admin": true\|false, "valid_days": 90, "password": "***" }`; `enabled` is authoritative (idempotent, REQ-API-042); `is_admin` grants/revokes the system-administrator flag (idempotent like `enabled`, REQ-API-136) — any change that would leave zero **enabled** administrators (a revocation, a disabling, or both in one call) is rejected 409 `conflict` with nothing written and no audit entry (REQ-AUTH-068); the guard rides the same UPDATE statement that writes the row (`… WHERE id = ? AND EXISTS (SELECT 1 FROM users AS u2 WHERE u2.is_admin = 1 AND u2.enabled = 1 AND u2.id <> users.id)`), evaluated under the write lock so concurrent revocations cannot race to zero; `valid_days` re-sets `valid_until` (`0` → `NULL` = indefinite, REQ-AUTH-052); `password` set/resets the local hash, an **empty string clears** it (never returned, never logged, REQ-AUTH-036); re-enabling resets the inactivity clock (REQ-AUTH-053); 200 user object; disabling a user denies the effective permissions of that user's API tokens at call time (REQ-AUTH-033, REQ-API-048); audit `user_updated` with the changed attributes (`enabled`, `is_admin` old/new, `valid_until`, `password_changed` — the password value itself is never in the trail) |

**Two-factor endpoints (GD-21).** Self-service (acting user = self; also valid in the pending-first-factor context of `Authentication_Authorization_Design.md` §2.7 under a mandate), REQ-API-115:

| Endpoint | Contract |
|---|------|
| `GET /api/v1/users/me/tfa` | 200 `{ "method": "off\|totp\|email", "enrolled_at": … }` — never the secret or a code |
| `POST /api/v1/users/me/tfa/totp/enroll` | 200 `{ "secret": "<base32>", "otpauth_uri": "otpauth://totp/…" }` — shown once; pending until confirmed (REQ-AUTH-056) |
| `POST /api/v1/users/me/tfa/totp/confirm` | body `{ "code": "…" }`; a valid current RFC 6238 code activates `totp` and returns `{ "recovery_codes": [ … ] }` exactly once; wrong → 400 `bad_code`; audit `tfa_enrolled` |
| `POST /api/v1/users/me/tfa/email/start` | sends a confirmation code to the account address via the SMTP relay; 409 `smtp_not_configured` without one; rate-limited per account (REQ-AUTH-057) |
| `POST /api/v1/users/me/tfa/email/confirm` | body `{ "code": "…" }`; activates `email`, returns the recovery codes once; audit `tfa_enrolled` |
| `POST /api/v1/users/me/tfa/disable` | requires a valid current code or recovery code; sets method `off`, clears secret/codes; audit `tfa_disabled` |

Administrator (requires `is_admin`), REQ-API-116: `POST /api/v1/users/{id}/tfa/reset` — deletes the account's `user_two_factor` row back to `off` (lost device/email); 200; audit `tfa_reset`. No endpoint ever returns a secret, pending code, or recovery code outside its single enrollment/activation response.

**Password lifecycle endpoints (GD-22/GD-23).** Administrator: `POST /api/v1/users/{id}/invite` — sends the invitation email with a single-use `invite` token to the account's address (REQ-AUTH-060); requires `is_admin`; 409 `smtp_not_configured` without a relay (the admin then sets the password directly); a re-invite replaces any outstanding invite token (the old link dies); 200 `{ "ok": true }` — the token value is never in the response; audit `user_invited` (REQ-API-117). Self-service: `PUT /api/v1/users/me/password` with `{ "current_password": "…", "new_password": "…" }` — current password verified against `password_hash` first (failure → 401 `bad_password`, audit-logged, counted toward the REQ-AUTH-035 lockout), then the new bcrypt hash is stored; accounts without a local credential → 409 `no_local_credential`; sessions are not revoked (DEV-AUTH-13); audit `password_changed` (REQ-AUTH-061, REQ-API-118).

### 4.5 Projects (REQ-API-049…052)

**`GET /api/v1/projects`** — project visibility only (`is_admin` or member, REQ-AUTH-026); projects outside the acting user's visibility do not appear (REQ-API-007). 200 — dashboard summaries:

```json
[ { "id": 33, "project_name": "8DISC", "organization": "NAT EU", "record_count": 42, "instrument_count": 5, "field_count": 128 } ]
```

**`POST /api/v1/projects`** — `is_admin` (master spec: administrator users create projects). Body — the creation fields of the simplified `projects` (GD-17, REQ-DB-006):

```json
{
  "project_name": "8DISC", "organization": "NAT EU",
  "pi_name": "Ansgar Espeland", "pi_email": "ansgar.espeland@example.org",
  "dm_name": "Lars Akslen", "dm_email": "lars.akslen@uib.no",
  "rek_number": "REK-2026/123", "rek_start_date": "2026-01-01", "rek_end_date": "2028-01-01",
  "start_date": "2026-01-01", "end_date": "2028-01-01",
  "participant_names": "8DISC[0-9][0-9][0-9]"
}
```

The removed attributes — `end_provision`, the `option_*` flags, `agreed_to_end_user_contract`, `event_names` — are rejected as unknown attributes (400 `invalid_request`, REQ-API-052); if the owner wants them they are data in an ordinary instrument (e.g. `DataTransferProjects`, REQ-DB-032). A duplicate `project_name` → 409 `conflict`. Creation is single-arm (REQ-DB-011, DEV-API-4): the API creates **arm 1 only**, together with its first event **`baseline`** (`unique_event_name = baseline_arm_1`, timepoint `period = 0`) and one instrument **`instrument`** — every project holds at least one arm, event and instrument from creation on (§4.9/§4.10, REQ-API-050). Further events are added afterwards through `POST /api/v1/projects/{id}/events` (REQ-API-062; `event_names` is no longer part of creation, GD-17). 201 — the project object (`id` + the supplied fields + `creation_time`). Audit `project_created`.

**`GET /api/v1/projects/{id}`** — data access ≥ `read_only` + project visibility. 200 — full metadata plus structure plus the acting user's effective permissions (REQ-API-126):

```json
{
  "id": 33, "project_name": "8DISC", "…": "…(all metadata fields)",
  "record_count": 42,
  "arms": [ { "arm_num": 1, "name": "", "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1", "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 } ] } ],
  "instruments": [ { "id": 7, "name": "intake", "position": 1, "field_count": 24 } ],
  "permissions": {
    "project_admin": false,
    "arms": [ { "arm_num": 1, "data_access_level": "view_edit", "export_level": "export_de_identified" } ]
  }
}
```

`record_count` is the project's record total — the summary the project home shows alongside its structure counts (REQ-UI-017). It is the same number the `GET /api/v1/projects` rows carry, so one detail read answers a project page without the web layer listing every visible project to find one heading (`Plan/Web_Implementation.md` §7 rule 13); instrument and field counts come from `instruments[]` and its `field_count`s.

`permissions` is the effective evaluation of `Authentication_Authorization_Design.md` §4.1 for the acting user — the same result the boundary applies to every call — surfaced so the PHP layer can gate rendering with the control absent from the DOM (`User_Interface_Design.md` §3.1, REQ-UI-003). Rules: one `arms[]` entry per arm of the project in the arm order of the `arms` list above; `is_admin` reports `edit_survey_responses` / `export_full` on every arm and `project_admin: true` (REQ-AUTH-023); a role-less member likewise (REQ-AUTH-022); an arm the user's role does not grant reports `no_access` / `export_none` (REQ-AUTH-019), so no arm is ever absent from the list and the caller never infers a level from a missing entry. The vocabulary is the §4.7 one (`no_access | read_only | view_edit | delete | edit_survey_responses`, `export_none | export_de_identified | export_no_identifiers | export_full`). This read authorizes nothing — it discloses only the caller's own levels, under the same visibility gate as the rest of the response (REQ-API-007), and every endpoint re-checks at call time (REQ-AUTH-033).

**`PUT /api/v1/projects/{id}`** — `project_admin` (an `is_admin` user is covered by REQ-AUTH-023). Body: any subset of the `POST` metadata fields — omitted fields are unchanged (idempotent, REQ-API-042). A duplicate `project_name` → 409 `conflict`. 200 — the updated project object (same shape as `GET`, `permissions` included — REQ-API-126). Metadata changes are audit-logged with old and new values (`project_updated`, `Audit_Logging_Design.md` §3.3; REQ-API-052, REQ-API-043).

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

**`GET /api/v1/projects/{id}/arms`** — data access ≥ `read_only`. 200 (events in the canonical per-arm order, GD-15; `period` is `null` when the event has no timepoint):

```json
[ { "id": 5, "arm_num": 1, "name": "",
    "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1",
                  "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 },
                { "id": 9, "event_name": "screening", "unique_event_name": "screening_arm_1",
                  "period": null, "safe_region_start": -2, "safe_region_end": 3, "position": 2 } ] } ]
```

**`POST /api/v1/projects/{id}/arms`** — `project_admin`. Body `{ "name": "…" }`; `arm_num` is the next 1-based number (a duplicate → 409 `conflict`). 201 — the arm object. Audit `arm_created`.

**`DELETE /api/v1/arms/{id}`** — `project_admin`; the path follows the master plan verbatim (ASM-API-1). 204. An arm that still has events or data → 409 `conflict` (DEV-API-6; phase-1 edge case, ASM-API-4) — **unless it is the project's last remaining arm**: that one cannot go either, and the call instead renames it to `arm_1`, keeping its id, `arm_num` and events (a renumbering would rewrite every `unique_event_name` and its stored values; the rename is a label). The answer is then 200 with the renamed arm object. Audit `arm_deleted`, or `arm_updated` with `last_arm_reset` on the rename (REQ-API-128; VISION "Arms, events and instruments").

**`PUT /api/v1/projects/{id}/arms/order`** — `project_admin`; idempotent (REQ-API-129). Body: `{ "order": [<arm ids>, …] }` — the **full** list of the project's arms; a partial or foreign list → 400. Writes `position` only, so every arm keeps its id and `arm_num`; `unique_event_name` values and stored data are untouched (the vision's "re-ordering should not change the ids"). The listing order of §4.8 (`ORDER BY position, arm_num`) reflects the new order immediately. 200 `{ "ok": true }`. Audit `arm_reordered`.

### 4.9 Events (REQ-API-061…063, REQ-API-103)

`period` is the event's **timepoint** (days after baseline) and is **nullable**: `null` = the event has no timepoint (GD-15, REQ-DB-011). Canonical per-arm order (applies to every event listing — here, `content=event`, `content=formEventMapping`, the record-status dashboard, and the UI event table):

```
1. events with a period, ascending by period (ties: ascending position)
2. events with period = null, ascending by position (user-orderable)
```

**`GET /api/v1/projects/{id}/events`** — data access ≥ `read_only`. 200 — all events, all arms, in canonical per-arm order:

```json
[ { "id": 4, "arm_num": 1, "event_name": "baseline", "unique_event_name": "baseline_arm_1",
    "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 },
  { "id": 7, "arm_num": 1, "event_name": "follow_up", "unique_event_name": "follow_up_arm_1",
    "period": 90, "safe_region_start": -2, "safe_region_end": 3, "position": 2 },
  { "id": 9, "arm_num": 1, "event_name": "screening", "unique_event_name": "screening_arm_1",
    "period": null, "safe_region_start": null, "safe_region_end": null, "position": 3 } ]
```

**`POST /api/v1/projects/{id}/events`** — `project_admin`. Body (`period` optional — `null`/absent = no timepoint):

```json
{ "arm_num": 1, "event_name": "follow_up", "period": 90, "safe_region_start": -2, "safe_region_end": 3 }
```

The API derives `unique_event_name = <label>_arm_<n>` (REQ-DB-011); a duplicate label within the same arm → 409 `conflict`; the new event takes `position` = end of the arm's list. 201 — the event object. Audit `event_created`.

**`PUT /api/v1/events/{id}`** — `project_admin` (path verbatim, ASM-API-1); idempotent (REQ-API-042). Body: any subset of `{ "event_name", "period", "safe_region_start", "safe_region_end" }` — `period: null` **clears** the timepoint (the event becomes user-orderable, GD-15). A label change re-derives `unique_event_name`; a collision with an existing `unique_event_name` → 409 `conflict` (§4.2). An event that already holds data keeps its values — the rename updates the stored `unique_event_name` key in the same transaction (ASM-API-4). Audit `event_updated` with old and new values.

**`DELETE /api/v1/events/{id}`** — `project_admin` (path verbatim, ASM-API-1). Removes the event together with its instrument–event mapping pairs; values keyed on the event's `unique_event_name` become unreachable, so the change is breaking in analysis mode (§4.21). 204. Audit `event_deleted`. **A project always keeps at least one event**: deleting the last remaining event instead resets it to the plain baseline state — renamed to `baseline` (its stored values and dependency rows follow the new `unique_event_name` in the same transaction, ASM-API-4), offset day (`period`) reset to 0 and any safe region cleared — exactly the state a freshly created project's baseline carries. The answer is then 200 with the renamed event object; the rename of an event that holds values is breaking in analysis mode like any other (§4.21). Audit `event_updated` with `last_event_reset` (REQ-API-133; VISION "Arms, events and instruments"). Rename and reorder stay available in every state.

**`PUT /api/v1/projects/{id}/events/order`** — `project_admin`; idempotent (REQ-API-103). Body — the **full** ordered list of the supplied arm's event ids (a partial list → 400 `invalid_request`):

```json
{ "arm_num": 1, "order": [9, 4, 7] }
```

Writes `position` (1…n) for the arm's events; the canonical order of GD-15 then governs the display (timepoint events by `period`, ties and no-timepoint events by the written `position`). 200. Audit `event_reordered` with the new order (REQ-API-103, `Audit_Logging_Design.md` §3.3).

### 4.10 Instruments (REQ-API-064…066, REQ-API-101, REQ-API-130)

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

**`PUT /api/v1/projects/{id}/instruments/{iid}`** — `project_admin`; idempotent (REQ-API-101). Body: any subset of `{ "name", "is_survey", "branching_logic" }`, e.g. `{ "is_survey": true }`, `{ "branching_logic": "[baseline][consent]=\"1\"" }`. A `name` rename keeps the name unique within the project — a collision → 409 `conflict`, an empty or non-string name → 400 `validation_error` (REQ-API-130). The rename is a label: stored values are keyed by field name and event, not instrument name, so nothing is rewritten and the mapping pairs stay. An invalid branching expression is rejected at design time → 400 `validation_error` (REQ-VAL-029, `Data_Validation_Design.md` §7.2). 200 — the instrument object. Audit `instrument_updated` with old and new values.

**`DELETE /api/v1/projects/{id}/instruments/{iid}`** — `project_admin`. Removes the instrument together with its fields, their stored values (same transaction, like REQ-API-070) and its mapping pairs; losing recorded values makes the change breaking in analysis mode (§4.21). An expression that survives the change naming a doomed field → 409 `conflict` — references inside the deleted design go with it, which is what commit checks against the post-change design too. 204. Audit `instrument_deleted` with the removed field and value counts (REQ-API-127). **A project always keeps at least one instrument**: deleting the last remaining one only deletes its fields and renames the shell to `instrument` — id, position, survey flag and branching logic stay — answering 200 with the object and auditing `instrument_updated` with `last_instrument_reset` (VISION "Arms, events and instruments": "only the fields in that instrument should be deleted. The instrument should be renamed to 'instrument'").

### 4.11 Fields (designer) (REQ-API-067…071, REQ-API-096, REQ-API-132)

**`GET /api/v1/projects/{id}/instruments/{iid}/fields`** — data access ≥ `read_only`. 200 — the instrument's fields in position order, with all data-dictionary attributes (REQ-DB-013):

```json
[ { "id": 11, "field_name": "age", "field_label": "Age", "field_type": "text",
    "section_header": "", "choices": "", "field_note": "",
    "validation_type": "integer", "validation_format": null, "validation_min": "18", "validation_max": "99",
    "required": true, "branching_logic": "", "calculation": "", "matrix_group": "",
    "personal_information": false, "position": 2 } ]
```

**`POST /api/v1/projects/{id}/instruments/{iid}/fields`** — `project_admin`. Body: the field object minus `id`/`position` (appended at the end of the list). The field name is lower-case alphanumeric + underscore, unique within the project (REQ-DB-013; a duplicate → 409 `conflict`); names longer than 26 characters are accepted — the warning after 26 is a UI concern (master spec). `validation_type` must be empty, one of the built-in structured types, or an existing `validation_types` name (`Data_Validation_Design.md` §4.2; anything else → 400 `validation_error`, REQ-VAL-010/042); `direct_identifier` is accepted for any field and defaults to `1` when the validation type is `email`, `MRN`, `international phone` or `national phone` (REQ-EXP-020). `calculation` is only allowed with `field_type = calculated`; the expression and the branching logic are validated at design time → 400 `validation_error` (REQ-VAL-029, REQ-VAL-033/034; `Data_Validation_Design.md` §6.2, §7.2). 201 — the field object. Audit `field_created`.

**`POST /api/v1/projects/{id}/instruments/{iid}/fields/bulk`** — `project_admin` (REQ-API-132). Body: `{ "fields": [ … ] }` — a non-empty array of field objects exactly as accepted by the single-field POST above; unknown attributes anywhere, an empty or missing array, or an entry without `field_name`/`field_type` → 400 `invalid_request`. All entries are validated against one shared dictionary view by the same §9 rules, so a name used twice inside the batch collides on its second occurrence like two consecutive single-field calls (→ 409), and a calculated entry may reference an earlier entry of the same call. The batch is **all-or-nothing**: any rejected entry creates nothing — the live path writes fields, calculation dependencies and audit entries in one transaction; in production mode the entries apply to the open staging set as one change (§4.21). Positions follow request order at the end of the instrument's list. `acknowledge_breaking` (analysis mode) is read from the top level of the body. 201 — the array of created field objects, in request order. Audit `field_created` per field, in the same transaction.

```json
{ "fields": [ { "field_name": "a", "field_type": "text" },
              { "field_name": "total", "field_type": "calculated",
                "calculation": "[baseline_arm_1][a] * 2" } ] }
```

**`PUT /api/v1/projects/{id}/instruments/{iid}/fields/{fid}`** — `project_admin`; idempotent. Body: any subset of the attributes — including a `field_name` rename (the stored values are renamed in the same transaction, REQ-VAL-014, `Data_Validation_Design.md` §9), the `calculation` expression of a calculated field (REQ-VAL-033/034), the `branching_logic` expression (REQ-VAL-029), and the `direct_identifier` flag; an assigned `validation_type` is checked against the registry as at creation (§4.2, REQ-VAL-010/042). A change to a calculated field's expression triggers recomputation of all project records in the same transaction (REQ-VAL-037, `Data_Validation_Design.md` §6.3). 200 — the field object. Audit `field_updated` with old and new values.

**`DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}`** — `project_admin`. 204. The field's stored values are removed in the same transaction (DEV-API-5); a field referenced by an active expression → 409 `conflict` (§4.2). Audit `field_deleted` with the count of removed values.

**`PUT /api/v1/projects/{id}/instruments/{iid}/fields/order`** — `project_admin`; idempotent. Body — the full ordered list of the instrument's field ids (a partial list → 400 `invalid_request`):

```json
{ "order": [11, 12, 15] }
```

The GD-8 record-identifier invariant MUST hold after reordering — a violation → 400 `invalid_request` (§4.2). Audit `field_reordered`.

**`GET /api/v1/validationTypes`** — any authenticated user; read-only (REQ-API-104). 200 — the types the designer may assign: the four built-in structured types plus every `validation_types` row, as `{"name": "…", "regex": "…", "builtin": true}` objects (the pattern is included for advisory client-side hints only — server-side validation remains authoritative, REQ-VAL-002). Registry entries are added by inserting a database row (`Database_Schema_Design.md` §5); there is no write endpoint in phase 1.

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

An instrument is active for data entry once it is mapped to at least one event (REQ-DB-012). **Unmapping never deletes anything:** the pair's `data` rows and its `instrument_completion` rows stay in place (REQ-DB-036 creates none while the pair is unmapped, and removes none), and they become visible again — in the form, on the record-status dashboard, and in exports — as soon as the pair is mapped back. Unmapping a pair that holds values is therefore non-breaking and needs no acknowledgement in any mode (§4.21). 200. Audit `mapping_updated`.

### 4.13 Record status (REQ-API-074, REQ-API-110)

**`GET /api/v1/projects/{id}/record-status`** — data access ≥ `read_only` + record visibility (REQ-AUTH-045). 200 — all visible records, with their instruments per event in the instrument order of each arm and a **three-state** completion state per (record, event, instrument):

```json
[ { "record_id": "8DISC042",
    "events": [ { "unique_event_name": "baseline_arm_1",
                  "instruments": [ { "name": "intake", "state": "finished" },
                                   { "name": "scores", "state": "some_data" },
                                   { "name": "labs",   "state": "no_data" } ] } ] } ]
```

`state` is `no_data` | `some_data` | `finished`. Only `finished` is stored (REQ-DB-036); the other two are computed per row from whether any of that instrument's fields in that event holds a value, so the field never reports `no_data` for an instrument that has values or `some_data` for one the user marked finished. A survey-marked instrument always reports `finished` — its completion info is filled in automatically, with nothing stored and no assignment taken; the other two states never apply to it (GD-9; master spec "Instrument level completion info").

The response MUST NOT contain field values (REQ-API-074). A record-status read is not a record view (`Audit_Logging_Design.md` §8, ASM-AUD-2).

**`PUT /api/v1/projects/{id}/records/{record}/events/{event}/instruments/{iid}/completion`** — data access ≥ `view_edit` on the record's arm (REQ-API-110); idempotent (REQ-API-042). Body:

```json
{ "state": "finished" }
```

`state` is `finished` (set) or `unfinished` (clear, returning the row to its derived `no_data`/`some_data`). The call writes **no field value** — it is a workflow annotation, not data entry, and MUST NOT be rejected by validation or by analysis-mode write rules that apply to values (GD-20 scopes mode rejection to imports of data; this endpoint changes none). Unknown record/event/instrument → 404 `not_found`; an (event, instrument) pair not mapped in the active design → 409 `conflict`; a survey-marked instrument → 409 `conflict` — it needs no assignment, its `finished` state is automatic (REQ-DB-036, GD-9). A record outside the caller's data-access-group scope → 403 `forbidden` (REQ-AUTH-045). 200 — `{ "state": "finished" }`, the resulting stored state. Audit `instrument_completed` / `instrument_uncompleted` (REQ-AUD-026, `Audit_Logging_Design.md` §3).

### 4.14 Export (UI) (REQ-API-075…076)

**`GET /api/v1/projects/{id}/export`** — query parameters (all optional; unknown ones accepted and ignored, REQ-API-017; DEV-API-12):

| Parameter | Values | Default | Meaning |
|---|---|---|---|
| `format` | `csv` \| `json` | `csv` | encoding (REQ-API-075) |
| `arm` | arm number, repeatable (`arm=1&arm=3`) | all arms the caller may export | restricts the export to those arms — this is what makes a higher per-arm sensitivity obtainable by separate per-arm exports (REQ-EXP-003); an arm the caller cannot export → 403 `forbidden`; an unknown arm number → 404 `not_found` |
| `rawOrLabel` | `raw` \| `label` | `raw` | choice values as codes or labels (REQ-EXP-010, same semantics as the data API's `rawOrLabel`, §3.1) |
| `rawOrLabelHeaders` | `raw` \| `label` \| `both` | `raw` | column names; `both` renders each header as `<Field Label> (field_name)` — the bare name where the field carries no label (§3.1, REQ-EXP-010) |
| `csvDelimiter` | single character | `,` | empty value = comma (REQ-EXP-013); CSV only |

Known parameters validate strictly: `format`, `rawOrLabel`, or `rawOrLabelHeaders` outside their value sets, a multi-character `csvDelimiter`, or a non-numeric `arm` → 400 `invalid_request`; unknown parameter names stay accepted and ignored (REQ-API-017). A caller with no exportable arm — none by default, or any named arm `export_none` — gets 403 `forbidden`; a rejected call writes no audit entry (REQ-AUD-004).

The response is streamed (REQ-TECH-011). Record selection follows the data-access-group rule (REQ-API-092). Sensitivity follows the acting user's export level per arm (the REQ-API-026 ladder — `export_full` → full dataset; `export_no_identifiers` → identifier fields removed; `export_de_identified` → de-identified per §3.6.1; `export_none` → 403 `forbidden`). **For an export spanning several arms the applied level is the lowest (most protective) level among those arms** — the minimum in the GD-2 ordering, so every row is delivered at a level no weaker than any arm allows (REQ-EXP-003, `Data_Export_Anonymization_Design.md` §4.3, decision D-4); `export_none` on any exported arm → 403 `forbidden`. The level recorded in the audit event and shown in the UI badge is exactly this applied minimum — never a higher level that some individual arm would have permitted.

Every call is audit-logged as an `export` event with `surface: "ui"`, the project, and the sensitivity level (REQ-API-076, BR-007, `Audit_Logging_Design.md` §3.5).

### 4.15 Audit log (REQ-API-077…078)

**`GET /api/v1/audit`** — `is_admin` only; any other acting user gets the uniform 403 (REQ-API-078, ASM-API-2, finding F2). Query parameters:

| Parameter | Meaning |
|---|------|
| `type` | `events` (default) or `views` — selects `audit_events` or `audit_record_views` |
| `project` | project id filter (optional) |
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

**`GET /api/v1/projects/{id}/records/{record}/history`** — data access ≥ `read_only` on the record's arm (GD-2) + project visibility (REQ-API-007) + record visibility (REQ-AUTH-045). Query parameters: `instrument`, `event`, `field` (filters, REQ-API-080), `order` (`chrono` default | `newest`, REQ-API-137 — see below) and `limit`/`cursor` per §1. Chronological, covering all changes of the record since creation, transparent across the yearly rollover (REQ-AUD-006). 200:

```json
{ "entries": [ { "created_at": "2026-09-18 14:02:11", "user_id": 3, "user_display_name": "User",
                 "action": "update", "instrument": "intake", "event": "baseline_arm_1",
                 "fields": [ { "field": "age", "old": "41", "new": "42" } ] } ],
  "next_cursor": null }
```

`action` ∈ `create | update | delete`; for `create`, `old` is `null`; for `delete`, `new` is `null` and the `old` values are the deleted values (REQ-AUD-009). The endpoint is read-only with respect to the audit trail (REQ-AUD-002). The data entry form presents this per-field history — who entered or changed the value, when, and the old → new values — fetched from this endpoint (REQ-API-081; User_Interface plan, data entry form).

`order=newest` serves the same entries in reverse chronological order (`created_at DESC, id DESC`) and the cursor pages backwards (`REQ-API-137`, DEV-API-23): each page's `next_cursor` continues from its last (oldest) row with a strictly-earlier condition. The filters apply within the page exactly as in `chrono`, so paging semantics are unchanged. This is the read direction the data entry form uses to derive current values — walking backwards it stops per field at the first entry seen (`User_Interface_Design.md` §8.3, closing its §11 open item 3). Any other `order` value → 400 `invalid_request`.

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

### 4.19 i18n and appearance (GD-12/GD-26, REQ-API-097…100/122/124)

**`GET /api/v1/i18n/languages`** — any authenticated user. 200 — the enabled languages (code, display name):

```json
[ { "code": "en", "display_name": "English" }, { "code": "nb", "display_name": "Norsk bokmål" } ]
```

**`GET /api/v1/i18n/bundle?language=<code>`** — any authenticated user (REQ-API-124). The render-time read for the PHP shell: one language's translations as a flat key→text map, overlaid on the English strings the application carries (English is the source of truth and lives in the PHP layer, not the table — REQ-DB-031); a key absent from the map renders in English. Without `language`, the acting user's stored UI language is served (default `en`, REQ-API-098). An unknown or disabled language → 400 `invalid_request`. 200:

```json
{ "language": "nb", "strings": { "ui.dashboard.title": "Oversikt" } }
```

**`PUT /api/v1/users/me/ui-language`** — any authenticated user (acting on themselves). Body `{ "language": "nb" }` — MUST be an enabled language (otherwise 400 `invalid_request`); the default is `en`. The setting persists across sessions (REQ-DB-008). 200 — the user object.

**`PUT /api/v1/users/me/ui-theme`** — any authenticated user (acting on themselves; GD-26, REQ-API-122). Body `{ "theme": "darkly" }` — MUST name an installed theme (`bootstrap` | `darkly` | `yeti`, REQ-TECH-027), or `null` to clear the personal override and follow the installation default `UI_THEME` (REQ-CFG-031); an unknown identifier → 400 `invalid_request`. The setting persists across sessions (`users.ui_theme`, REQ-DB-008). 200 — the user object. The PHP layer resolves the effective theme at render time (`User_Interface_Design.md` §3.8); the API stores the value and never serves CSS.

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

### 4.20 End-of-Project Provision (BR-009, `Data_Export_Anonymization_Design.md` §7)

**`POST /api/v1/projects/{id}/end-provision`** — `is_admin` (otherwise 403 `forbidden`). The semantics are normative in `Data_Export_Anonymization_Design.md` §7; the endpoint is registered here for the completeness of this surface:

- Body: `{ "provision": "delete" | "anonymize" }`; any other value → 400 `invalid_request`.
- One-shot: a second execution for the same project → 409 `conflict` — the idempotency state is the `project_ended` audit event (`Audit_Logging_Design.md` §3.3).
- Atomicity: the audit row and the data change are written by the same-transaction writer; no partial state on failure (§7.2).
- `provision=delete` — removes the project's stored record data (EAV rows, `record_entities`, `survey_links`, `anon_offsets`); project metadata, structure, roles/memberships/tokens, and the audit trail are kept (§7.3).
- `provision=anonymize` — applies the `export_de_identified` pipeline **in place** to the stored values; afterwards every read and export returns the anonymized form — irreversible (§7.4).

200:

```json
{ "provision": "delete", "records_affected": 42, "values_affected": 1287 }
```

The counts report what the execution changed: `delete` counts the project's records (`record_entities` rows) and stored values removed; `anonymize` counts records with at least one rewritten value and the number of values rewritten — a value already in its target form stays untouched and counts neither (§7.4).

Audit: `project_ended` with the provision and the affected counts (`Audit_Logging_Design.md` §3.3; `Data_Export_Anonymization_Design.md` §8).

### 4.21 Project modes and staging (GD-20, REQ-API-105…111)

Every project is in exactly one mode (`projects.mode`, `Database_Schema_Design.md` §4): `development` (default for new projects), `production`, or `analysis`. Changing the mode belongs to `is_admin` alone — an installation admin user, **not** the project's own `project_admin`, who gets a 403 (GD-20, 2026-09-27). Staging is run by `project_admin` as before.

**`GET /api/v1/projects/{id}/mode`** — data access ≥ `read_only` + project visibility. 200:

```json
{ "mode": "production", "staging_open": true }
```

**`PUT /api/v1/projects/{id}/mode`** — `is_admin`; idempotent (REQ-API-042). Body `{ "mode": "production" }`, plus `keep_data` where the transition demands it:

| Transition | Body | Effect |
|---|---|---|
| development → production | `{ "mode": "production", "keep_data": true \| false }` — `keep_data` **required** (missing → 400 `invalid_request`) | `true`: all stored record data is kept. `false`: the project's record data is deleted with the same scope as the end-provision `delete` (`Data_Export_Anonymization_Design.md` §7.3 — EAV rows, `record_entities`, `survey_links`, `anon_offsets`; metadata, structure, memberships, and audit kept); affected counts are in the response |
| production → development | `{ "mode": "development" }` | all data kept; setup edits go back to applying directly, with no staging and no warning |
| production → analysis | `{ "mode": "analysis" }` | all data kept; data entry stops for everyone from then on (REQ-API-109) |
| analysis → production | `{ "mode": "production" }` | all data kept; setup changes go back behind a staging set (REQ-API-107) |
| analysis → development | `{ "mode": "development" }` | all data kept — the way out of analysis without passing through production again (GD-20, 2026-09-27) |
| any other pair (incl. **development → analysis**) | — | 409 `conflict` — not an allowed transition: a project enters `analysis` only from `production`, where the data entry it disables has actually happened (GD-20 table) |

**No mode change while a staging set is open.** Every transition in the table is rejected (409 `conflict`) until the open set is committed or discarded (REQ-API-105, GD-20 2026-09-27). The rule is stated for all transitions rather than only the ones leaving production because staging opens only in production and closing it is now a precondition of every way out.

200 — `{ "mode": "<new mode>", "records_deleted": 0 }` (`records_deleted` set only for the delete-on-transition case). Audit `project_mode_changed` with old/new mode and the `keep_data` decision (`Audit_Logging_Design.md` §3.3).

**Mode effects (normative):**

| Mode | Setup (structure changes) | Data entry (import / delete / survey submission) | Read / export |
|---|---|---|---|
| development | applies immediately | allowed | allowed |
| production | requires an open staging set; applies on commit (§4.21 staging below) | allowed — against the **active** design, also while a set is staged | per permissions |
| analysis | allowed for admin users (`project_admin`); applies immediately — a **breaking** change must first be acknowledged (REQ-API-111) | **disabled** — 403 `Project in analysis mode` on every surface (REQ-API-109) | per permissions ("viewing and exporting remain available", GD-20) |

**Setup changes in analysis mode (REQ-API-111, GD-20 2026-09-27).** No staging set — a `project_admin` edit lands on the live design exactly as it does in development. What analysis mode adds is a guard rather than a gate: an edit that classifies as **breaking** (table below) is rejected once — 409 `conflict`, naming the change and its reason — and applies when the same call returns with `{ "acknowledge_breaking": true }`. A non-breaking edit goes straight through with no round trip. The flag is accepted only in analysis mode: production routes structure changes through staging (REQ-API-107), where the acknowledgement happens at commit (REQ-API-108), and development warns about nothing.

**Staging (production only, REQ-API-106/107).** One open staging set per project (`project_staging`, `Database_Schema_Design.md` §5): a JSON snapshot of the live design taken at start; all structure endpoints (§4.8–§4.12) then read and write the **staged** design for `project_admin` users (response shapes unchanged), while every data-facing endpoint keeps serving the **active** design until commit.

- **`POST /api/v1/projects/{id}/staging`** — `project_admin`; production mode only (409 otherwise); a second set while one is open → 409. Snapshots the live design. 201 — `{ "opened_at": "…", "opened_by": 3 }`. Audit `staging_started`.
- **`GET /api/v1/projects/{id}/staging`** — `project_admin`. 200 — state plus the staged diff, computed against the active design:

```json
{ "open": true, "opened_at": "2026-09-25 08:00:00", "opened_by": 3,
  "changes": [ { "kind": "field_added", "object": "intake.age_at_entry", "breaking": false },
               { "kind": "field_deleted", "object": "intake.old_score", "breaking": true,
                 "reason": "deleting a field makes its stored values inaccessible" } ] }
```

- **`POST /api/v1/projects/{id}/staging/commit`** — `project_admin`; body optionally `{ "acknowledge_breaking": true }`. The staged diff is classified per the table below; if it contains breaking changes and `acknowledge_breaking` is not `true` → 409 `conflict` listing them. On success the snapshot is applied to the live structure tables in **one transaction** (the whole set activates at once, GD-20) and the staging row is removed. 200 — `{ "applied": { "instruments": 2, "fields": 5, "events": 1, "mapping_pairs": 3 } }`. Audit `staging_committed` with the applied counts and the acknowledged breaking changes.
- **`POST /api/v1/projects/{id}/staging/discard`** — `project_admin`. 204; the staging row is removed without applying. Audit `staging_discarded`.

**A commit does not recompute calculated values (owner decision 2026-09-29).** A staged change to a `calculated` field's expression updates that field's dependency rows with the rest of the set, and the values already stored keep their previous result; REQ-VAL-037's same-transaction recomputation runs on the live paths (the field endpoints' own edits, import) but not at commit. Recomputing a whole project inside the commit transaction is deliberately out of scope — it is offered as a separate action instead, so an operator triggers it knowingly. Until that action exists, a deployment treats values derived from a committed expression change as stale.

**Breaking-change classification (normative, REQ-API-108).** The rule: a change is breaking when it would make existing recorded data inconsistent or inaccessible; everything else is non-breaking.

| Staged change | Classification |
|---|------|
| add an arm / event / instrument / field | non-breaking |
| map an instrument to an event; unmap a pair that holds no values | non-breaking |
| change a field label/description, field note, section header; reorder fields/instruments/events (GD-8 invariant enforced as ever) | non-breaking |
| rename a field (stored values renamed in the same transaction, REQ-VAL-014) | non-breaking — values stay accessible under the new name |
| rename an instrument (REQ-API-130) | non-breaking — values are keyed by field name and event, not instrument name |
| add options to an existing dropdown/radio/matrix | non-breaking |
| delete a field | **breaking** — its stored values are removed (DEV-API-5) |
| change a field's type, or its validation type beyond the current values | **breaking** — stored values may no longer satisfy the new rules |
| remove a choice option that stored values use, or re-code existing options | **breaking** — stored codes lose their label/meaning (removing an unused option is non-breaking) |
| delete an instrument or event that holds data; delete an arm with events or data (DEV-API-6) | **breaking** — the keyed values become inaccessible |
| unmap an instrument–event pair whose records **do** hold values | non-breaking, no warning — nothing is deleted: the values stay in `data` and are reachable again once the pair is mapped back, so the change is reversible by construction (master spec "Unmap is misclassified"; retention rule §4.12) |

### 4.22 System settings (REQ-API-112, REQ-API-113)

Both endpoints require `is_admin`; a call by a non-admin is rejected (403 `forbidden`). The values live in `system_settings` (`Database_Schema_Design.md` §8, REQ-DB-037); the rate limiter reads them per request (§3.9), so an applied change takes effect on the next request without a restart.

| Endpoint | Behavior |
|---|------|
| `GET /api/v1/settings` | 200 — `{ "rate_limit_enabled": false, "rate_limit_rpm": 600, "rate_limit_block_minutes": 10 }` (the effective values: the stored row when present, the seeded default otherwise) |
| `PUT /api/v1/settings` | Body: any subset of the fields (idempotent, REQ-API-042). Validation: `rate_limit_enabled` a boolean; `rate_limit_rpm` an integer ≥ 1; `rate_limit_block_minutes` an integer 1–1440 — else 400 `bad_request`; unknown attributes → 400. The upper bound keeps a mistyped value from locking every caller out for days. 200 — the full settings object after the update. Audit `settings_updated` with the old and new value of each changed key (REQ-AUD-027); a PUT that changes nothing writes no entry |

## 5. Permission summary

The normative endpoint → permission mapping is in `API_Endpoints_Requirement.md` §4.21; it is reproduced here as an overview:

| Endpoints | Required permission |
|---|------|
| data API `/api/` | the token's levels per §3 (data/export level per arm; link tokens scoped per §3.10) |
| `GET/POST /api/v1/users`, `PUT /api/v1/users/{id}` | `is_admin` (PUT also grants/revokes `is_admin`; the last enabled administrator cannot be revoked or disabled — REQ-API-136, REQ-AUTH-068) |
| `POST /api/v1/users/{id}/invite` (§4.4, REQ-API-117) | `is_admin` |
| `PUT /users/me/password` (§4.4, REQ-API-118) | any authenticated user (self, with local credential) |
| `POST /api/v1/auth/password-reset/request`, `…/complete`, `POST /api/v1/auth/invite/complete` (§4.3, REQ-API-119/120/121) | pre-authentication — service token only, no user id (DEV-API-16) |
| `POST /api/v1/auth/verify-password` (§4.3, REQ-API-123) | pre-authentication — service token only, no user id (DEV-API-17) |
| `POST /api/v1/projects` | `is_admin` |
| `GET /api/v1/projects` | project visibility (REQ-API-007) |
| `GET /api/v1/projects/{id}` | data access ≥ `read_only` + visibility |
| `PUT /api/v1/projects/{id}` | `project_admin` |
| `GET/PUT …/users` (members), `GET/POST …/roles` | `is_admin` |
| `GET …/users/{uid}/token` | self-service: acting user is a member (REQ-API-102) |
| arms, events, instruments, fields, mapping — mutations (POST/PUT/DELETE) | `project_admin` |
| arms, events, instruments, fields, mapping — reads (GET) | data access ≥ `read_only` |
| `GET …/record-status`, `GET …/records/{record}/history` | data access ≥ `read_only` (+ record visibility) |
| `PUT …/records/{record}/events/{event}/instruments/{iid}/completion` | data access ≥ `view_edit` on the record's arm (+ record visibility) |
| `POST …/fields/{fid}/test` | `project_admin` + record visibility |
| `GET /api/v1/validationTypes` | any authenticated user |
| `GET …/export` | export level per arm (GD-2; `export_none` → 403) |
| survey links (issue/revoke, §4.17) | data access ≥ `view_edit` on the arm (+ record visibility) |
| `GET …/data-access-groups` | data access ≥ `read_only` |
| `POST/DELETE …/data-access-groups`, `PUT …/records/{record}/data-access-group` | `project_admin` |
| `PUT …/users/{uid}/data-access-groups` | `is_admin` |
| `PUT …/active-data-access-group` | an assigned member (self-service) |
| `GET /api/v1/audit` | `is_admin` (all projects) or a member of the queried project (REQ-API-078) |
| `GET /i18n/languages`, `PUT /users/me/ui-language`, `PUT /users/me/ui-theme` (§4.19) | any authenticated user |
| `GET/PUT /i18n/strings` | `is_admin` |
| `POST …/end-provision` (§4.20) | `is_admin` (BR-009) |
| `GET …/mode` (§4.21) | data access ≥ `read_only` + visibility |
| `PUT …/mode` (§4.21) | `is_admin` — `project_admin` is rejected (403) |
| staging start/commit/discard (§4.21) | `project_admin` |
| `GET …/staging` (§4.21) | `project_admin` |
| `GET/PUT /api/v1/settings` (§4.22) | `is_admin` |

`is_admin` users hold all permission levels on every arm of every project (REQ-AUTH-023), so a permission requirement never excludes an administrator.
