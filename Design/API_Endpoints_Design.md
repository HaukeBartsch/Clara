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
