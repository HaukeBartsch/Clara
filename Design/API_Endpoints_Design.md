# API Endpoints — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/API_Endpoints_Requirement.md`\
**Date:** 2026-09-21

## 1. Purpose and Conventions

The normative endpoint contracts — parameter tables, request/response schemas, status codes, error formats — for both surfaces. The requirements document fixes *what* (which endpoint does what, under which permission); this document fixes *shape*.

- **Two surfaces** (REQ-API-001): the REDCap-compatible data API at `POST /api/` (`GET /api/` is refused as 405, REQ-API-009) and the administration API under `/api/v1/`. Beyond these, only the documentation and health endpoints exist (REQ-API-003).
- **UTC and UTF-8** in all requests and responses for **system** timestamps (REQ-API-004, GD-7); clinical date/date-time values carry their collection timezone in the canonical form (GD-16, REQ-VAL-041, `Data_Validation_Design.md` §4.1).
- **Versioning**: the administration API is versioned in the path (`/api/v1/`); breaking changes only under a new major version (REQ-API-008). The data API is pinned by the REDCap protocol — caller compatibility is the contract (REQ-API-037).
- **Non-disclosure**: a project or record the caller is not entitled to is rejected with a uniform 403 (data API: `Permission denied`; administration API: `forbidden`) — never disclosed as missing (REQ-API-007).
- **Pagination** (`GET /api/v1/audit`, record history): `limit` (default 50, max 200) + opaque `cursor` (encodes the last-seen `(created_at, id)`); the response carries `next_cursor` (`null` when exhausted).
- **JSON shapes** (administration API): objects/arrays; timestamps `YYYY-MM-DD HH:MM:SS` (UTC); absent optionals are `null`.
- **Log hygiene** (REQ-API-005): project tokens, the service secret, and record values MUST NOT appear in application logs at any level; the audit trail is the sole sanctioned carrier (`Audit_Logging_Design.md` §2).

> **Key constraints** — a summary for quick orientation; the cited IDs and sections are normative.
>
> - **This document fixes the shape** of every endpoint; the Requirements document fixes what each one does. `api/openapi/openapi.json` is maintained by hand and served as the API's documentation (REQ-API-002), so a contract change here lands there in the same change.
> - **The API stores no session** (GD-1); PHP owns it.
> - **An unentitled project or record answers a uniform 403**, never a 404 (REQ-API-007).
> - **PUT is idempotent, and every mutating call is audited** (REQ-API-042, REQ-API-043).

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

Neither is routed to a browser. Administrators read the document in the Control Panel's **API documentation** section, which receives `/openapi.json` through the web application as that section's data region (`User_Interface_Design.md` §5.9, REQ-UI-057).

## 3. Data API — `/api/`

### 3.1 Transport and Common Parameters (REQ-API-009…017)

One endpoint: `POST /api/` with an `application/x-www-form-urlencoded` body. `GET /api/` answers **405** — the token is read from the body alone (REQ-API-010), so a GET could not authenticate, and leaving it open would keep alive the one request shape that puts a credential in a URL where every proxy logs it (REQ-API-009, DEV-API-30, master-spec finding F19).

| Parameter | Accepted | Default | Notes |
|---|---|---|---|
| `token` | UUID string | — (required) | **body only** — never the query string, never an `Authorization` header; a `token` that arrives in the query string answers 400 naming the body, without echoing the value (REQ-API-010, REQ-AUTH-031, DEV-API-30) |
| `content` | `project` / `metadata` / `event` / `formEventMapping` / `exportFieldNames` / `generateNextRecordName` / `record` | — (required) | with `content=record`, a call carrying `data` is an import, one without it an export; deletion needs `action=delete` (REQ-API-012) |
| `action` | `export` / `import` / `delete` (with `content=record` only) | — | optional: `export`/`import` are accepted and never override the `data` rule; `delete` is the only action that dispatches; any other value → REDCap-style error (REQ-API-012, DEV-API-24) |
| `format` / `returnFormat` | `json` / `csv` | `csv` (REDCap default) | a present `returnFormat` takes precedence for the response encoding (REQ-API-013) |
| `type` | `flat` / `wide` | `flat` (REDCap default) | `flat` is normative — one row per (record, event); `wide` is the simplified compatibility mode of §3.6.2 (DEV-API-1) |
| `csvDelimiter` | `,` / `;` / `\|` / `^` / tab (the character or `tab`) | `,` | REDCap's set; empty value = comma; any other value → 400 `Invalid csvDelimiter`, never cut down to its first character (REQ-API-029, REQ-EXP-013) |
| `decimalCharacter` | `.` / `,` | stored form | record export: the decimal separator of `calculated` and `floating point` values, in either format (`Data_Export_Anonymization_Design.md` §3.3); empty value = stored form (a period); any other value → 400 `Invalid decimalCharacter` (REQ-EXP-015) |
| `records[]` / `fields[]` / `forms[]` / `events[]` | arrays | — | both REDCap array syntax (`records[0]=…&records[1]=…`) and repeated single values (REQ-API-015) |
| `filterLogic` | expression (§3.6.3) | — | export only (REQ-API-025) |
| `rawOrLabel` | `raw` / `label` | `raw` | choice fields (REQ-API-027) |
| `rawOrLabelHeaders` | `raw` / `label` / `both` | `raw` | field names; `both` renders each header as `<Field Label> (field_name)` — the bare name where the field carries no label (REQ-API-027) |
| `data[i][key]` / `data` | import rows (§3.7.1) | — | import only; indexed form parameters or a JSON array of record objects — its presence makes the call an import (REQ-API-012, REQ-API-031) |
| `returnContent` | `count` (or any other value) | result rows | with `content=record` import: `count` answers `{"count": N}` for the rows applied; anything else keeps the §3.7.2 result rows (REQ-API-142, DEV-API-24) |
| `tz` | IANA timezone name or `±HH:MM` offset | `APP_TIMEZONE` (REQ-CFG-026) | import only — timezone of collection for the call's date/date-time values (GD-16, REQ-VAL-041, REQ-API-031) |
| `exportCheckboxLabel` | `true` / `false` | `false` (REDCap default) | record export with `rawOrLabel=label`: `true` writes a checked box's choice text (unchecked: empty), otherwise `Checked`/`Unchecked` (REQ-API-016, REQ-EXP-053) |
| `exportSurveyFields`, `exportDataAccessGroups` | any | — | accepted and **ignored** (REQ-API-016, DEV-API-2) |

Unknown parameters are accepted and ignored, never rejected — existing callers keep working (REQ-API-017).

**Request bounds (REQ-API-144).** A request is bounded by body size (32 MiB, REQ-TECH-011) and by a parameter count of 250,000. The second bound is needed because the first does not imply it: an indexed call carries one parameter per cell, so a 50-row × 200-field import is 10,004 parameters — over the 10,000 `net/url` applies to one urlencoded string from Go 1.27 (`net/url.defaultMaxParams`), and far under its byte cap. The body is therefore parsed with `url.ParseQuery`'s acceptance rules (`&` separators, `+` as space, `%XX` escapes) without that count cap, and the result published as `r.PostForm`, so a handler reading the field afterwards sees these values rather than re-parsing a body that is already consumed. Either bound answers 400 with the limit named, before the token lookup; a batch over the parameter bound belongs in the JSON `data` encoding (REQ-API-031), which carries the same rows in one parameter.

Only urlencoded bodies are read (REQ-API-009): a `multipart/form-data` body is refused as 400 rather than arriving as empty parameters, while a missing or unexpected Content-Type keeps being read as urlencoded for the PHP callers that omit it. Both rules exist so that no request-shape problem can surface as `Invalid token` — an empty parameter set carries an empty token, and that reads as an authentication failure for a credential that was never the problem (REQ-API-011). Refusing a query-string `token` belongs to the same rule: the credential was never read, so the answer is a 400 naming the body, not a 401 about a token nobody checked (REQ-API-010, DEV-API-30).

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
| request over the body-size or parameter bound (§3.1) | 400 | `Too many parameters: …` / `Request body too large: …` — the limit named, answered before the token lookup (REQ-API-144) |
| `multipart/form-data` body (§3.1) | 400 | `Unsupported Content-Type: send application/x-www-form-urlencoded` |
| `csvDelimiter` / `decimalCharacter` outside its set (§3.1) | 400 | `Invalid csvDelimiter` / `Invalid decimalCharacter` — answered after the rate limit and before the token lookup (REQ-EXP-013/015) |
| unparseable `filterLogic`, or a reference above what the response delivers (§3.6.3) | 400 | `Invalid request` — deliberately one body for both reasons, so the refusal names no field the caller may not read (REQ-API-025, REQ-EXP-054) |
| insufficient permission (incl. `export_none`, read-only import) | 403 | `Permission denied` |
| write to an analysis-mode project (`import` / `delete`; survey submissions included) | 403 | `Project in analysis mode` (GD-20, REQ-API-109) |
| survey link already used for its one submission (§3.10) | 410 | `Survey already submitted` — distinct from 401 on purpose: the respondent needs to know the answer arrived (REQ-API-145) |
| rate limit exceeded (when enabled) | 429 | `Rate limit exceeded` (with `Retry-After`, §3.9) |
| import with per-record validation failures | 200 | — (per-record result codes, §3.7.2) |

Error text never leaks internal implementation details (REQ-API-039, REQ-API-006). Keeping the detail out of the *response* does not mean throwing it away: every 500 logs the error behind it and the code site that produced it — a 500 that leaves no log line is undiagnosable, and one cost a per-statement-metrics hunt rather than a grep in October 2026. Tokens, parameters and record values never reach the log (REQ-API-005).

The uniform 401 `Invalid token` describes **credential states** — unknown, revoked, spent-by-account, or an inactive account — and stays indistinguishable across them (REQ-AUTH-032). A storage failure while resolving a token is not a statement about the token: it answers 500 like any other store failure, logged, and denies access just as firmly. Reporting it as `Invalid token` would tell an operator that someone used a bad credential while the database was down.

### 3.3 `content=project` (REQ-API-018)

Any valid token for the project suffices. JSON response (array with one object):

```json
[{
  "project_id": "33",
  "project_name": "MOBA",
  "project_title": "MOBA study",
  "project_description": "",
  "project_pi_name": "Ole Bull",
  "project_pi_email": "ole.bull@example.org",
  "project_rek_number": "REK-202/123",
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

Standard REDCap project-info keys the system does not store are returned with neutral values (`"0"` / `""`) rather than omitted, so naive parsers keep working (REQ-API-018) — including `project_end_provision` since GD-17 (the end provision is no longer stored; if the owner records one, it is data in an instrument such as `DataTransferProjects`, REQ-DB-032). CSV: a single row of those key/value pairs. `has_repeating_instruments_or_events` is `"1"` once any (instrument, event) pair repeats (REQ-API-155).

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
    "select_choices_or_calculations": "",
    "choice_codes": "",
    "choice_labels": "",
    "validation_type": "",
    "validation_min": "",
    "validation_max": "",
    "required_field": "Y",
    "branching_logic": "",
    "matrix_group_name": "",
    "field_annotation": "",
    "record_identifier": "Y",
    "direct_identifier": "Y"
  },
  {
    "field_name": "status",
    "form_name": "intake",
    "field_type": "dropdown",
    "select_choices_or_calculations": "1, Pending | 2, Done | 3, Cancelled",
    "choice_codes": "1,2,3",
    "choice_labels": "Pending,Done,Cancelled",
    "record_identifier": "",
    "…": "…"
  }
]
```

`record_identifier` is `"Y"` on the record-identifier field (the first field of the earliest instrument with fields, GD-8) and empty elsewhere. `direct_identifier` is this system's key (`"Y"`/`""`, REQ-DB-013, REQ-EXP-020 — an additional key beyond the REDCap shape, tolerated by naive parsers per REQ-API-018). `choice_codes`/`choice_labels` are comma-joined from the stored `code$label##code$label` encoding; a `transfer` field exports its instruction text verbatim in `select_choices_or_calculations` with empty `choice_codes`/`choice_labels` — no code/label parsing applies to it (REQ-DB-050). `field_annotation` is REDCap's own metadata key — the field's annotation as stored, `@READONLY` and any other text (REQ-DB-046). `forms[]` restricts the response to those instruments when supplied — the mechanism §3.10 uses so a survey link sees its own instrument's definitions and no other (REQ-API-083).

### 3.5 `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`

All require data access ≥ `read_only` except `generateNextRecordName` (≥ `view_edit` on the target arm — REQ-API-023).

| content | Response shape (JSON) |
|---|------|
| `event` (REQ-API-020) | `[ {"event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","custom_event_label":null,"event_id":4}, … ]` — `custom_event_label` is not stored: always `null` (an empty CSV cell), present because naive REDCap parsers read the key — events in the **canonical per-arm order** (GD-15: timepoint events by `period` ascending, ties by position; then no-timepoint events by position) |
| `formEventMapping` (REQ-API-021) | `[ {"form_name":"intake","event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","form_event_mapping":"1"}, … ]` — events in the canonical per-arm order (GD-15) |
| `exportFieldNames` (REQ-API-022) | `[ {"field_name":"record_id","form_name":"intake"}, … ]` — restricted to `forms[]` when supplied |
| `generateNextRecordName` (REQ-API-023) | `[ { "next_record_name": "MOBA042" } ]` — a one-row array like the other contents |
| `repeatingFormsEvents` (REQ-API-155) | `[ {"event_name":"baseline_arm_1","form_name":"visits","custom_form_label":""}, … ]` — the repeating pairs (REQ-DB-047), events in canonical order, instruments by position; read-only — a call carrying `data` is 400 (the designation is the administration API's, §4.10) |

`generateNextRecordName` follows the project's naming pattern (REQ-DB-007) — digit-placeholder style (`MOBA[0-9][0-9][0-9]`) and counter-prefix style (`0001_01`, width preserved) — and MUST NOT return a name an existing record already holds (REQ-API-023). The next name is one counter step above the greatest existing name of the same shape (max + 1); names freed by deleted records are not reused (no gap-filling, REQ-API-023). While the project's record naming mode is `auto` (REQ-DB-045) it answers the next plain integer strictly above every numeric record id of the project instead — the first name is `1`, and ids that are not all digits never advance the counter nor collide with it (REQ-API-151).

### 3.6 Record export — `content=record` without `data` (REQ-API-012)

An explicit `action=export` may accompany the call and changes nothing; a `data` parameter on the same call makes it an import instead (§3.7).

#### 3.6.1 Rules

- Filters `records[]`, `fields[]`, `forms[]`, `events[]` combine (intersection) — with one exception: the record identifier stays in every export (REQ-API-028), added when `fields[]`/`forms[]` leave it out; such an added identifier brings neither its instrument's other columns nor its `<instrument>_complete`, and no pair of its instrument into the sensitivity scope. With no filters, all records visible to the holder under the data-access-group rule are returned (REQ-API-024, REQ-AUTH-045, REQ-API-092).
- Sensitivity per the token holder's export level **for the (instrument, event) pair of each exported column** (REQ-API-026): `export_full` → full dataset; `export_no_identifiers` → all identifier fields removed; `export_de_identified` → de-identified per `Data_Export_Anonymization_Requirements.md` (date shift per `Database_Schema_Design.md` §8, salt/range per `System_Configuration_Design.md` §3.6); a pair at `export_none` contributes **no columns** — the permitted pairs still export normally, and only a request with no permitted pair in scope is 403 `Permission denied` (REQ-AUTH-069).
- `rawOrLabel=label` → choice labels for dropdown/radio fields (stored values remain codes, REQ-VAL-022), `Yes`/`No` for a yesno field (REQ-DB-043); checkbox choice columns render REDCap's `Checked`/`Unchecked`, or with `exportCheckboxLabel=true` the choice text when checked and nothing otherwise (`Data_Export_Anonymization_Design.md` §3.2, REQ-EXP-053); `redcap_event_name` renders the event's name — `<event_name>`, or `<event_name> (Arm <n>: <arm name>)` in a project with several arms — while `redcap_repeat_instrument` keeps the instrument name (no form label is stored) (REQ-API-028); `rawOrLabelHeaders` controls field names the same way (REQ-API-027).
- Rows carry the record identifier's value under its field name (GD-8) — the record id itself, on every row of both layouts — `redcap_event_name` per row (flat, projects with events), and empty strings for missing values (REQ-API-028). An export of the identifier alone (`fields[]=record_id`) lists one row per (record, event) that holds data: the identifier counts as delivered by its own instrument's pair wherever that pair is permitted, so those events are not suppressed as rows naming nothing but the event (§4.3).
- `redcap_repeat_instrument` and `redcap_repeat_instance` appear only in projects with a repeating pair (REQ-API-155), as in REDCap; JSON writes `redcap_repeat_instance` as a number on instance rows and `""` on the event's own row.
- Each instrument's field block ends with an `<instrument>_complete` column carrying that instrument's three-state completion for the row's position — `0` not complete, `1` in progress, `2` complete (REQ-API-134). The state is the record-status derivation of §4.13 (REQ-API-074): stored `finished` → `2`, else any value of that instrument at that position → `1`, else `0`; a survey-marked instrument always exports `2` (GD-9); at an event the instrument is not mapped to the cell is empty (no pair, no state — REDCap's shape), so a `finished` assignment on an unmapped (event, instrument) pair stays invisible until the pair is mapped back. The column belongs to whole-instrument exports — a full export or one filtered by `forms[]` carries it for every instrument that contributes at least one value column; a `fields[]`-filtered export omits it entirely (the web application exports whole instruments and gets the column; a field-list export returns exactly the requested fields). It keeps its raw name under `rawOrLabelHeaders` (it names no field), and appears in JSON as an ordinary key. Import does not accept it: a `<instrument>_complete` key remains an unknown field (§3.7).
- CSV is streamed (REQ-TECH-011): quoted per standard CSV rules, honors `csvDelimiter`, formula-triggering leading characters neutralized per `Data_Validation_Design.md` §5.3 in every cell, header row included (REQ-API-029, REQ-EXP-013); never preceded by a byte-order mark (REQ-EXP-016).
- `decimalCharacter` rewrites the separator of `calculated` and `floating point` values after the sensitivity transformation, so hashes never depend on it (REQ-EXP-015, `Data_Export_Anonymization_Design.md` §3.3).
- Every invocation writes a record-view audit row (`audit_record_views`, `Audit_Logging_Design.md` §4) and an `export` event (`Audit_Logging_Design.md` §3.5) (REQ-API-030, REQ-AUD-011).

#### 3.6.2 `type=flat` (normative) and `type=wide` (compatibility)

`flat` — one row per (record, event); the two `redcap_repeat_*` columns exist because this project has a repeating instrument:

```json
[
  {
    "record_id": "MOBA042",
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

`wide` (DEV-API-1, simplified for caller compatibility): one row per record, opening with the record identifier under its bare name; a field present in exactly one event keeps its bare name; a field present in several events is emitted once per event as `<field>_<unique_event_name>`. An instrument's `<instrument>_complete` follows the same rule — suffixed once per event when the instrument is active in several (REQ-API-134). Missing values are empty strings. `flat` is what all known callers use and what an omitted `type` selects (REDCap's default); `wide` exists for callers that ask for it.

**Repeating instruments** (REQ-API-155, REDCap's layout). At an event where an instrument repeats (REQ-DB-047), `flat` writes the event's own row with that instrument's columns — its `<instrument>_complete` included — left empty, and then one row per instance that holds a value or is finished, ascending: `redcap_repeat_instrument` names the instrument, `redcap_repeat_instance` the number, the identifier column the record, and only that instrument's columns are filled (the others empty). The event's own row is written only when the event holds a first-instance value outside its repeating instruments (the identifier counts):

```json
[
  { "record_id": "MOBA042", "redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "", "redcap_repeat_instance": "",
    "age": "42", "intake_complete": "1", "visit_score": "", "visits_complete": "" },
  { "record_id": "MOBA042", "redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": 1,
    "age": "", "intake_complete": "", "visit_score": "3", "visits_complete": "1" },
  { "record_id": "MOBA042", "redcap_event_name": "baseline_arm_1", "redcap_repeat_instrument": "visits", "redcap_repeat_instance": 2,
    "age": "", "intake_complete": "", "visit_score": "5", "visits_complete": "2" }
]
```

`wide` adds, after an instrument's own block, one block per further instance `n` (2 … the highest among the records the call may return) at each event the instrument repeats at, every column suffixed `__<n>` — `visit_score__2` for a single-event field, `visit_score_baseline_arm_1__2` for one present at several events; a record without that instance leaves the block empty. Hidden instances (REQ-DB-048) are in neither layout. `filterLogic` evaluates first instances only (REQ-VAL-045).

#### 3.6.3 `filterLogic` (REQ-API-025)

Minimum normative support: equality on string fields, `[field]="value"`, and compound conditions with `&&` / `||` and parentheses — the full grammar (value comparisons `=`, `!=`, `<`, `>`, `<=`, `>=`, numeric/chronological/string comparison semantics, `text_contains`, `is_blank`, `is_not_blank`, precedence) is the branching-logic grammar of `Data_Validation_Design.md` §7, evaluated against the record's stored values **within the bound the caller's export level draws around them** (REQ-EXP-054). A single-segment reference `[field]` names no event and falls back to the project's **first event** in canonical order GD-15 (`Data_Validation_Design.md` §7.1) — a filter is evaluated per record and has no current event, unlike branching logic, where the shorthand names the event of the form (DEV-VAL-13). REDCap's own spellings — `'value'`, `and`/`or`, `<>`, `[field(code)]` — and its blank test `[field] = ''` are part of that grammar (REQ-VAL-044), so a filter copied from a REDCap caller means what it meant there. `filterLogic` filters which **records** are returned; it never changes the sensitivity level.

**What the filter may read (REQ-EXP-054).** The expression is compiled once per request (finding F5) and its references then checked against the column set this call delivers, before any record is read: each reference resolves to a pair — its own event, or the first event for `[field]` (GD-15) — whose export level must carry the value **as stored**. Out of reach, therefore: a pair at `export_none` or unmapped at that event, a direct identifier below `export_full`, unapproved free text at `export_de_identified`, and anything the file transforms — a hashed personal field, a date shifted for that record (REQ-EXP-022/023/033). Row presence is otherwise an oracle worth one bit per guess against values the columns withhold, up to a whole instrument the caller may not export at all (master-spec finding F6), and it is quiet: the audit row of such a call reads like any other export. A field carrying no values at all (`description`, `header`, `transfer`, a checkbox's own row) discloses nothing and keeps reading blank. The record identifier belongs to no pair, so it reads where its column appears — only when the most protective level in scope is `export_full` (GD-8). The bound is drawn by the level, never by `fields[]`/`forms[]`: filtering on a value the caller may export but did not request stays available, as in REDCap, which keeps the recorded Fiona calls working (`VISION_AND_REQUIREMENTS.md` "Endpoints used by Fiona", REQ-API-037). A reference outside the set answers 400 with **the same body an unparseable expression answers** — one answer for every reason, so nothing about the dictionary's unreadable fields leaks through the refusal (REQ-API-039) — and writes no audit row (REQ-AUD-004). The gate lives in the shared export core, so the administration surface (§4.14) behaves identically.

### 3.7 Record import — `content=record` with `data` (REQ-API-012)

The presence of the `data` parameter makes the call an import; an explicit `action=import` may accompany it and changes nothing. Requires data access ≥ `view_edit` on the record's arm (REQ-API-033); a `read_only`/`no_access` token is rejected (403 `Permission denied`). In **analysis mode** every import is rejected — 403 `Project in analysis mode` (GD-20, REQ-API-109); reads and exports are unaffected.

#### 3.7.1 Request shape (REQ-API-031)

Each row is a (record, form, event) tuple of field values, supplied in either REDCap encoding:

```
token=…&content=record
&data[0][record_id]=MOBA042&data[0][form_name]=intake&data[0][event_name]=baseline_arm_1
&data[0][age]=42&data[0][status]=2&data[0][notes]=ok
&data[1][record_id]=MOBA043&data[1][form_name]=intake&data[1][event_name]=baseline_arm_1
&data[1][age]=&data[1][status]=1
```

or as a single `data` parameter holding a **JSON array of record objects** — the encoding of the recorded callers (DEV-API-24), whose values may be JSON numbers or booleans and are coerced to their string spelling:

```
token=…&content=record&format=json&type=flat&overwriteBehavior=overwrite
&returnContent=count
&data=[{"record_id":"1.3.6.1.4.1.45037.411…","ids7_patient_name":"ENDO_MONT_049",
        "ids7_number_of_series":18,"ids7_study_date":"20221202"}]
```

The record is named under the **record identifier's field name** — the first field of the earliest instrument with fields (GD-8), the column an export writes it under (§3.6), so an export imports back unchanged. The examples use a project whose identifier field is `record_id`; in a project whose identifier field is `study_instance_uid` the row carries `data[0][study_instance_uid]=…` and a `record_id` key is an unknown field (DEV-API-29). A row without the identifier is rejected as `<identifier>: CONTENT_INVALID — a record id is required`; a survey link supplies it from the link instead (§3.10).

A row may omit `form_name` — a **flat row**: every supplied field is stored on its own instrument from the data dictionary, so a caller can post back an export without naming forms. The event rules are unchanged: a project with events still requires each row to name its event (`event_name`/`redcap_event_name`). Indexed parameters win when a call carries both encodings. `overwriteBehavior` and `forceAutoNumber` are accepted and ignored (REQ-API-017): import always upserts (REQ-API-033) and never assigns record ids itself.

**A large batch belongs in the JSON encoding.** An indexed call carries one parameter per cell, so its size grows with rows × fields and meets the request bound of §3.1 at 250,000 parameters — about 1,250 rows of 200 fields; past that the call is refused 400 naming the limit rather than half-parsed (REQ-API-144). The same batch as one `data` parameter stays one parameter however wide it is, bounded only by the body size.

Each value passes the full validation pipeline (`Data_Validation_Design.md` §2) before storage; invalid values are not stored (REQ-API-032, REQ-VAL-001). Empty values act as "no value" (clear/no-op, REQ-VAL-024) — an **intentional** clear: the UI data-entry path sends empties only for fields the user explicitly cleared (GD-14, REQ-UI-031). Values for calculated fields are rejected (`CALCULATED_READONLY`, REQ-API-095). A `checkbox` field is written through its derived names `<field_name>___<choice_code>` (REQ-DB-042): the value must be `0` or `1` (any other value is `CHOICE_INVALID`); `1` stores the checked row, `0` clears it exactly like an empty value — only checked boxes hold stored rows. The checkbox's own name addresses no data column: a value sent under it is `NON_VALUE_FIELD`. Date/date-time values are stored with the collection offset — the `tz` parameter when present (UI: the browser's zone, sent by PHP), else `APP_TIMEZONE` (GD-16, REQ-VAL-041).

The row's event key is accepted as `event_name` **or** `redcap_event_name` (REQ-API-138): the latter names the same value in a flat export (REQ-API-028), so a caller can post back what it pulled without renaming. An empty value means "not supplied", so one spelling is in force per row; two different events name an ambiguous target and the row is rejected — `event_name: CONTENT_INVALID — event_name 'baseline_arm_1' conflicts with redcap_event_name 'followup_arm_1'` — storing nothing of it (REQ-API-035). The alias applies to projects **with** events: a project without events stores under the empty event name, so there the alias would move the row's values out of the slot the rest of the code reads (calculated fields recompute into it) and it is ignored instead.

**Repeating instruments** (REQ-API-155). A row naming `redcap_repeat_instrument` and `redcap_repeat_instance` fills that instance of an instrument repeating at the row's event — instance 1 lands where every first instance is stored, so the rows of a flat export import back unchanged. Such a row carries that instrument's fields only: an empty cell of another instrument is skipped (REDCap's rows leave them empty), a value is the usual cross-instrument rejection; the record identifier is a framing key on a further instance and is not stored there (GD-8). `form_name`, when present, must name the same instrument. Refusals, as row results: the instrument does not repeat at the event (hidden instances are not writable, REQ-DB-048); the instance is not a positive integer; `redcap_repeat_instance` other than `1` without an instrument (repeating events do not exist here). A row naming neither fills first instances, as before; a **flat** row without repeat columns skips the empty value of a field whose instrument repeats at the row's event — the event's own row of a REDCap export leaves those cells empty, so reading them as clears would wipe instance 1 on a round trip. Calculated fields are recomputed per instance (REQ-VAL-045). The audit payloads name a further instance as `instance` (`Audit_Logging_Design.md` §3.2).

#### 3.7.2 Response (REQ-API-034, REQ-VAL-008, REQ-API-142)

With `returnContent=count` the response is HTTP 200 and the number of rows applied (added + updated) — `{"count": 86}` in JSON, a bare count line in CSV; rejected rows are not counted and no per-row detail is rendered (REQ-API-142). Otherwise — any other value or none — HTTP 200 with one result row per imported record:

```json
[
  { "record_id": "MOBA042", "form_name": "intake", "import_record_id": 2, "import_form_name": "intake" },
  { "record_id": "MOBA043", "form_name": "intake", "import_record_id": 0,
    "import_form_name": "Validation error: age: TYPE_INVALID — value \"abc\" is not a valid integer; status: CHOICE_INVALID — value \"9\" is not a choice of \"status\"" }
]
```

| `import_record_id` | Meaning |
|---|------|
| `1` | record added |
| `2` | record updated |
| `0` | validation error(s) — `import_form_name` lists the per-field detail `<field>: <CODE> — <message>`, joined by `; ` (rule codes from `Data_Validation_Design.md` §3) |
| `255` | fatal request-level error — e.g. unknown `content`; the whole call fails (HTTP 400 with the §3.2 error body) |

The detail's order is fixed (REQ-API-139): the row's field problems in data-dictionary order, then its unknown keys as `<field>: UNKNOWN_FIELD — unknown field '<name>'` in dictionary order, so a rejected batch reads the same every time and a caller can diff two responses. A row stops at its first **tuple** problem — a missing record identifier or an unknown form reports that alone and skips the row's remaining field detail; a row without `form_name` has no such problem, it is flat (§3.7.1). Unknown event values are the batch-level exception (REQ-API-140): the API preflights event names before applying any row, de-duplicates and sorts values per key, then returns one request-level 400 error. If any are invalid, no row is applied, even when other rows are valid. For example, repeated `redcap_event_name=v1_arm_1_arm_1` values yield `{"error":"The following values of redcap_event_name are invalid: v1_arm_1_arm_1"}`. An empty event name is rendered as `""`. This error takes precedence over `returnContent=count`; CSV receives the message as a single line.

All-or-nothing per record: a failed value stores nothing of that record (single transaction, REQ-API-035); other records in the same call are unaffected. Successful imports are audit-logged (`record_created`/`record_updated` with old/new values) and trigger calculated-field recomputation in the same transaction (REQ-API-095, REQ-VAL-037). A new record is assigned to the holder's active data-access group, or none (REQ-API-093); an import never changes an existing record's group.

#### 3.7.3 Aggregated invalid event values (REQ-API-140)

Unknown event names are collected before `importOneRow` runs. Each distinct value is reported once under the input key the caller used, with values sorted for deterministic output. The request fails with HTTP 400 and no rows are applied; this deliberately chooses request-level failure over preserving successful-row results in a mixed batch. Other row validation remains per-row. The event preflight is skipped for survey links and projects without events, where the existing link and empty-event rules apply.

### 3.8 `content=record&action=delete` (GD-3, REQ-API-036)

Requires the **delete instrument values** right on every (instrument, event) pair whose values the call clears — all-or-nothing (REQ-API-036). In analysis mode the call is rejected — 403 `Project in analysis mode` (GD-20, REQ-API-109). Removes the record's values — scoped by the supplied `records[]`/`events[]`/`fields[]` (a `checkbox` named in `fields[]` scopes to every one of its choice columns, where its values are stored — REQ-DB-042), or the whole record — and, when a record's last value is removed, the record itself. Audit: `record_deleted` **with the deleted values** (REQ-AUD-009, `Audit_Logging_Design.md` §3.2).

Request: `token=…&content=record&action=delete&records[0]=MOBA042` (optionally `events[]`/`fields[]` to scope, and `repeat_instance` — REDCap's parameter — to delete one instance only: `1` the first, `n` a further instance of a repeating instrument, REQ-API-155; without it every instance in scope goes, hidden ones included; a value that is no positive integer is 400).

Response (JSON):

```json
[ { "record_id": "MOBA042", "form_name": "", "deleted": 1 } ]
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

**Source IP (REQ-API-125).** When the direct TCP peer is inside `TRUSTED_PROXY_CIDRS` (`System_Configuration_Design.md` §3.9), the limiter keys on the proxy-provided `X-Real-IP`; otherwise on the connection's remote address; a client-supplied `X-Real-IP` from an untrusted peer is ignored. nginx overwrites `X-Real-IP` with its own `$remote_addr` on every routed request and never appends a chain (`Technology_Stack_Design.md` §5), so each external caller keeps a distinct address. Web-application calls arrive server-side from PHP on the application host, and PHP applies this same rule to its own peer before forwarding anything: with `include fastcgi_params` nginx already passes its `$remote_addr` through as PHP's `REMOTE_ADDR`, so the browser address arrives without a header being believed, and only a peer listed in `TRUSTED_PROXY_CIDRS` may supply it in `X-Real-IP` instead. Loopback is listed by default, so per-browser limiting works despite the shared server hop either way (finding F9; `User_Interface_Design.md` §12 rule 10).

### 3.10 Survey Link Tokens (GD-9, REQ-API-083, REQ-API-145)

A survey link token (`survey_links.token`, `Database_Schema_Design.md` §8) is accepted as the `token` parameter of the data API **while it is live**, but only for the calls that render and fill its (record, instrument, event):

| Call | Allowed scope |
|---|------|
| `content=metadata` | the field definitions of that instrument (a `forms[]` naming another instrument → 403) |
| `content=record` with `data` (REQ-API-012) | values for that record, instrument and event — once |

**The link is the address.** A row submitted through a link names none of that triple: the API reads the record, the instrument and the event out of the `survey_links` row and stores under them, which is what lets the public page submit answers without ever telling the respondent which record they are filling (`User_Interface_Design.md` §8.8). A row that does name a record, an instrument or an event must name the link's own — anything else is the 403 below, never a silent retarget.

**One submission per link.** The save that stores a submitted row stamps `survey_links.collected_at` in the submission's own transaction, so a stored response cannot exist without the stamp that makes it *collected* (REQ-DB-041, REQ-AUTH-071) — and under one-submission links that same stamp is what ends the link. No second column is involved:

| Link state | Test | Data-API answer with its token |
|---|---|---|
| live | `revoked = 0 AND collected_at IS NULL` | admitted (§3.10 scope above) |
| submitted | `collected_at IS NOT NULL` | **410** `Survey already submitted` (REQ-API-145) |
| revoked, or no such token | `revoked = 1`, or unknown | 401 `Invalid token` (REQ-API-011, REQ-AUTH-040) |

The stamp is a conditional write (`… WHERE collected_at IS NULL`) and its affected-row count is the concurrency guard: of two submissions racing on one link, only the transaction that stamps stores anything, and the other rolls back to 410 having written neither value nor a success audit entry. A row rejected by validation never reaches the stamp (REQ-API-035), so a respondent who mis-typed may correct and send again — it is the store that consumes the link, not the attempt.

The 410 is the one place a link token's fate is told to the holder, and deliberately so: distinguishing "your answer arrived" from "this link was revoked" is what stops a respondent calling the study team over nothing, and the token is an unguessable UUID rather than an account credential. Unknown and revoked tokens still share the single indistinguishable 401 (REQ-AUTH-032).

Every other `content` (including `export` and `delete`), another record, or another instrument is rejected with 403 `Permission denied` (REQ-API-083, REQ-AUTH-039). In an analysis-mode project the permitted import is rejected too — `Project in analysis mode` (GD-20, REQ-API-109); the survey page then shows its closed state (`User_Interface_Design.md` §8.8). Link tokens are subject to the §3.9 rate limit (REQ-API-038). Submissions are audit-logged as `survey_submitted` — success and failure (`Audit_Logging_Design.md` §3.6). The public survey page is served by the PHP application; the browser never calls `/api/v1/*` from it (GD-1, REQ-API-084).

## 4. Administration API — `/api/v1/`

### 4.1 Boundary and Authentication (REQ-API-040, REQ-API-041)

- Reachable only from the trusted internal path (REQ-TECH-018, REQ-AUTH-014); the proxy strips `X-Internal-Service-Token`, `X-Internal-User-Id` and `X-Internal-Session-Epoch` from every externally-originated request (`Technology_Stack_Design.md` §5). The browser MUST NOT call this surface directly (GD-1, BR-006, REQ-API-040).
- Every request MUST present a valid `X-Internal-Service-Token` and `X-Internal-User-Id` (REQ-API-041, REQ-AUTH-011…013); the exceptions are `POST /api/v1/auth/login` (§4.3, `Authentication_Authorization_Design.md` §2.3), the three pre-authentication password endpoints of Sequence H (§4.3, `Authentication_Authorization_Design.md` §2.8 — DEV-API-16), `POST /api/v1/auth/verify-password` (§4.3, `Authentication_Authorization_Design.md` §2.9 — DEV-API-17), and `POST /api/v1/auth/oauth/start` (§4.3, `Authentication_Authorization_Design.md` §2.1 — DEV-API-32), which carry the service token only.
- The service token (config per `System_Configuration_Design.md` §3.3) is compared in constant time (REQ-AUTH-012) and MUST NOT be logged (REQ-API-005); it is enforced regardless of any other configuration (REQ-CFG-023). Missing/invalid → 401 + audit `admin_rejected`.
- `X-Internal-User-Id` is authoritative for authorization (REQ-AUTH-013): the API applies the acting user's effective levels (`Authentication_Authorization_Design.md` §4.1) — the PHP layer adds none. Unknown or disabled user → 403 + audit `admin_rejected`.
- `X-Internal-Session-Epoch` (REQ-API-147) — present on every call PHP makes for an established session, absent on the enrollment wizard's pending-identity calls and on `POST /api/v1/auth/session-replay` — MUST equal the acting user's `session_epoch`; a different or non-integer value → 401 `session_revoked` + audit `admin_rejected` (reason `session_revoked`). The check follows the account-active rule, so a disabled user keeps the 403 above. Raising the epoch is how the API ends PHP sessions without storing them (`Authentication_Authorization_Design.md` §3.1).
- JSON request/response bodies with conventional REST semantics (GET read, POST create, PUT update, DELETE remove); all PUT endpoints are idempotent (REQ-API-042).
- Every mutating call is audit-logged with the acting user, the target project, and the operation (REQ-API-043; `source=ui`, `Audit_Logging_Design.md` §5).
- The API is stateless: it MUST NOT create or store a session (GD-1).

### 4.2 Error Format (REQ-API-006, REQ-API-007)

Every error is a JSON object with a consistent shape:

```json
{ "error": "conflict", "message": "project name 'MOBA' already exists", "status": 409 }
```

`error` is a stable machine code (callers may branch on it); `message` is human-readable and MUST NOT leak internal details (REQ-API-006). A project or record the acting user is not entitled to is rejected with the uniform 403 `forbidden` — never disclosed as missing (REQ-API-007).

| Status | `error` | When |
|---|------|------|
| 400 | `invalid_request` | malformed JSON body; a missing or invalid attribute (e.g. non-survey instrument for a link, §4.17); a GD-8 identifier-invariant violation (§4.10, §4.11) |
| 400 | `validation_error` | design-time rejection of a data-dictionary entry — ill-formed expression, unknown/inactive reference, cycle, malformed name (`Data_Validation_Design.md` §6.2, §7.2, §9) — or an unexecutable transfer instruction / rejected transferred value at execution (§4.27); the reason is in `message` (REQ-VAL-029) |
| 401 | `service_token_invalid` | missing/invalid `X-Internal-Service-Token` (audit `admin_rejected`) |
| 401 | `session_revoked` | `X-Internal-Session-Epoch` no longer matches the acting user's session epoch — the account's sessions were ended since this one signed in (REQ-API-147; audit `admin_rejected`). PHP ends the session. |
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

**`POST /api/v1/auth/login`** — called by the PHP application after a successful LDAP bind, after the OAuth2 provider's callback, **or with `source: "local"` and the password for table-based login** (GD-18, REQ-AUTH-050/051; REQ-AUTH-016). Exempt from `X-Internal-User-Id`, like the Sequence H endpoints below (DEV-API-16); it still requires the service token. Body:

```json
{ "source": "oauth2", "provider": "https://idp.example.org", "source_name": "Hospital 1",
  "authorization_code": "…", "code_verifier": "…", "assertion_nonce": "…" }
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

**The `oauth2` leg (REQ-API-044, REQ-AUTH-076…082/086, `Authentication_Authorization_Design.md` §2.1/§2.10).** The body names no identity: `provider` is the issuer, and `authorization_code`, `code_verifier` and `assertion_nonce` come from PHP's single-use browser transaction (the values `POST /api/v1/auth/oauth/start` returned, plus the code from the provider's callback). An `email` sent beside them is never used as the identity. Before any account lookup the API redeems the code at the discovery document's token endpoint and verifies the returned ID token (Sequence J steps 1–7); the identity is the verified token's address, and the account is resolved by its (`iss`, `sub`) link first (`provider_subject_links`, REQ-DB-051, Sequence J step 8). The address lockout check (REQ-AUTH-035) therefore runs after verification on this leg, never on a caller-supplied address. Refusals specific to this leg:

| Status | `error` | Cause |
|---|---|---|
| 401 | `unknown_provider` | `provider` names no configured issuer |
| 401 | `assertion_invalid` | incomplete authorization response, no ID token returned, malformed token, algorithm other than the pinned one, unknown key, bad signature, `iss`/`aud`/`exp`/`nbf` failure |
| 401 | `nonce_mismatch` | the token's `nonce` differs from `assertion_nonce`, or is missing while `OAUTH2_N_NONCE_REQUIRED=1` |
| 401 | `email_unverified` | `email_verified` is not the JSON boolean `true` in the response that supplied the address |
| 403 | `assurance_insufficient` | `OAUTH2_N_MIN_ACR` is set and neither `acr` nor `amr` satisfies it |
| 403 | `subject_conflict` | the subject link and the verified address point at different accounts — an administrator's decision |
| 502 | `provider_issuer_mismatch` | the discovery document's `issuer` differs from the configured one (REQ-AUTH-082) |
| 502 | `provider_unavailable` | discovery document or userinfo endpoint unreachable or unusable, or the token endpoint did not redeem the code |

Each refusal is audited as `login_failure` with that reason and the assertion details observed so far — issuer, subject, assurance claims, address verification — never token material (REQ-AUTH-084, REQ-AUTH-036). The account-state answers below (`account_not_found`, `account_disabled`, `account_expired`) apply unchanged once the account is resolved; the two-factor gate does not apply to this leg (REQ-AUTH-055).

**`POST /api/v1/auth/oauth/start`** — `{ "provider": 1 }` (the `N` of `OAUTH2_N_*`): begins Sequence A (REQ-API-173). The API resolves the provider's discovery document and answers 200

```json
{ "authorize_url": "https://idp.example.org/as/authorization.oauth2?response_type=code&client_id=…&redirect_uri=…&scope=openid+email+profile&state=…&code_challenge=…&code_challenge_method=S256&nonce=…",
  "state": "…", "code_verifier": "…", "nonce": "…" }
```

`state` and `nonce` are 32 random bytes each, base64url-encoded; `code_verifier` is a fresh PKCE verifier. PHP stores the three in the session as one single-use transaction and redirects the browser to `authorize_url`; the API keeps none of them (GD-1) and receives them back on the login call. Pre-authentication: service token only, no `X-Internal-User-Id` (DEV-API-32). Errors: 400 `unknown_provider`; 502 `provider_issuer_mismatch` / `provider_unavailable` when discovery fails.

**`POST /api/v1/auth/oauth/test`** — `is_admin`; `{ "provider": 1, "code": "…", "code_verifier": "…", "nonce": "…" }`: the OAuth half of the Control Panel's authentication diagnostic (REQ-API-174, REQ-AUTH-085, `Authentication_Authorization_Design.md` §2.11). PHP starts it through `oauth/start` like a login, marks the transaction `auth_test`, and on the callback calls this endpoint instead of `login`. The API runs discovery, the token exchange, a userinfo read, and Sequence J steps 1–7, and answers 200 with a report whatever the outcome — it resolves no account, writes no audit event and establishes no login:

```json
{ "ok": false, "stage": "assertion", "error": "email_unverified", "issuer": "https://idp.example.org",
  "endpoints": { "authorize": "…", "token": "…", "userinfo": "…" },
  "discovery_status": 200, "token_status": 200, "userinfo_status": 200,
  "userinfo_claims": { "sub": "…", "email": "user@example.org", "email_verified": "true" },
  "id_token_claims": {} }
```

`stage` (`discovery` | `token_exchange` | `assertion`) and `error` appear only when `ok` is `false`; `endpoints` is absent when discovery failed. `discovery_status` and `userinfo_status` are the provider's HTTP statuses (`0` = no response or not reached); `token_status` is `200` after a successful exchange, otherwise the status the login call would have answered. `id_token_claims` is filled on success with the verified assertion's audit details (`provider_issuer`, `provider_subject`, `email_verified`, `address_source`, `acr`, `amr`). `userinfo_claims` holds only allowlisted identity, assurance and group/role claims, each value cut to 500 characters and each list to 30 entries. No code, token, signature or client secret appears in the report (REQ-AUTH-036). Errors: 400 `unknown_provider`; 403 for a caller without `is_admin`.

**`POST /api/v1/auth/verify-password`** — `{ "email": "…", "password": "***" }`: the side-effect-free verify step of the named-source credential race (`Authentication_Authorization_Design.md` §2.9, REQ-API-123). Runs the login endpoint's hash check and account-active rule only and answers `ok` / `bad_password` / `account_disabled` / `account_expired` — no `last_login_at`/`auth_source` write, no audit event, no user object; login finalizes exactly once through `POST /api/v1/auth/login`. Pre-authentication: service token only, no `X-Internal-User-Id` (DEV-API-17); password never logged (REQ-AUTH-036).

For an account a second factor still guards, the `ok` answer additionally carries the handle the challenge's second login call presents in place of the password (REQ-API-131, DEV-API-19):

```json
{ "status": "ok", "first_factor": "eyJ2IjoiZmYxIiw…", "expires_in": 300 }
```

The handle is `base64url(JSON claims) + "." + base64url(HMAC-SHA256)` over the claims — version, user id, expiry — signed with a key derived from `INTERNAL_SERVICE_TOKEN` for this one purpose (`hmac(service_token, "clara/first-factor/v1")`). It is stateless by construction, so §2.7's "the API keeps no challenge state" still holds; it is bound to one user id, lives 5 minutes (the `tfa_pending` lifetime), and is issued only where gate 1.5 would still close behind it — an account with `method: "off"` and no mandate gets `{"status":"ok"}` and nothing else. It never appears in a log line or an audit detail.

Processing (in order, `Authentication_Authorization_Design.md` §2.3): for `source: "local"` — the row exists (401 `account_not_found`) and the argon2id hash matches in constant time (else 401 `bad_password`; the password is never logged, REQ-AUTH-036), or an unexpired handle from `verify-password` stands in for the hash check where a second factor still guards the account (else 401 `first_factor_expired`, REQ-API-131); the account is active per the rule of `Authentication_Authorization_Design.md` §4.4 — `enabled`, not expired (403 `account_expired`), not inactive (auto-disable + 403 `account_disabled`, REQ-AUTH-053); **two-factor gate** (`source: "local"`/`"ldap"` only, GD-21, §2.7): method not `off` and no `mfa_code` → 401 `{"error":"mfa_required","method":"totp|email"}`; invalid/expired/replayed `mfa_code` → audit `login_failure` (`bad_mfa_code`) + 401 `bad_mfa_code`; `AUTH_REQUIRE_2FA` on with method `off` → 401 `{"error":"tfa_enrollment_required","user_id":<id>}` — the pending identity's id lets PHP drive the enrollment wizard (§4.4 TFA endpoints, presented as `X-Internal-User-Id`) before a session exists (`Authentication_Authorization_Design.md` §2.7); bootstrap-admin promotion (REQ-AUTH-007, DEV-AUTH-15): if the email equals `ADMIN_BOOTSTRAP_EMAIL`, create the row when absent (enabled administrator — the first-installation setup case); never re-enable a disabled account; set `is_admin = 1` on an existing enabled row only for `source: "local"` or while no other enabled administrator exists; set the row's `auth_source` and `last_login_at` (REQ-AUTH-005, REQ-AUTH-053); audit `login_success` with the source (and the factor used when the gate applied, REQ-AUD-028). The API MUST NOT create or store a session (GD-1).

200 — the user object (used by all §4.4 user endpoints):

```json
{ "id": 3, "email": "user@example.org", "display_name": "User", "enabled": true, "is_admin": true,
  "auth_source": "oauth2", "ui_language": "en", "ui_theme": null,
  "last_login_at": "2026-09-20 08:14:05", "valid_until": null, "status": "active",
  "tfa_method": "off", "session_epoch": 0 }
```

`last_login_at` is `null` when the account has never logged in; `valid_until` is `null` when indefinite; `status` ∈ `active | disabled | expired | auto_disabled` (derived — GD-19, REQ-AUTH-052/053); `ui_theme` is `null` when the account follows the installation default theme (`UI_THEME`, GD-26, REQ-DB-008); `session_epoch` is the account's session epoch, which PHP keeps in the session it establishes and presents as `X-Internal-Session-Epoch` (REQ-AUTH-074).

**`POST /api/v1/auth/logout`** — records the `logout` audit event (REQ-AUTH-008) and returns 200. Destruction of the PHP session remains the PHP layer's responsibility, performed after this call (GD-1, REQ-AUTH-015; `Authentication_Authorization_Design.md` §2.4).

**`POST /api/v1/auth/session-replay`** — no body; PHP calls it when a session id it rotated away came back after the grace window (REQ-AUTH-073), naming that id's account in `X-Internal-User-Id` and sending no `X-Internal-Session-Epoch`. In one transaction the API raises the account's `session_epoch` — every session of the account then ends at its next call — and records `session_replay_detected` with `{"ip": "…"}` (REQ-AUD-030; never a session id). 200 `{ "status": "ok" }` (REQ-API-148). Like logout, it stores no session (GD-1).

**Out-of-band password endpoints (Sequence H — GD-22/GD-23).** Pre-authentication: service token only, no `X-Internal-User-Id` (DEV-API-16); full mechanics in `Authentication_Authorization_Design.md` §2.8.

| Endpoint | Contract |
|---|------|
| `POST /api/v1/auth/password-reset/request` | body `{ "email": "…" }`; **always 202 with an identical body** — whether or not a matching active local account exists (no enumeration, REQ-AUTH-062); when one does, emails the set-password link with a single-use `reset` token (`password_tokens`, REQ-DB-039; TTL `AUTH_PASSWORD_TOKEN_TTL_DAYS`, REQ-CFG-030); rate-limited per address and IP (default 3/15 min/address); audit `password_reset_requested` (address + IP, never the token) (REQ-API-119) |
| `POST /api/v1/auth/password-reset/complete` | body `{ "token": "…", "password": "***" }`; constant-time hash verification, purpose `reset`, expiry, single use; success stores the argon2id hash, consumes the token, invalidates all outstanding tokens of the account, raises its `session_epoch` (REQ-AUTH-074), audits `password_reset_completed` → 200 `{ "ok": true }`; any failure → generic 401 `invalid_setup_token`; no temporary password exists (REQ-AUTH-062, REQ-API-120) |
| `POST /api/v1/auth/invite/complete` | as above for purpose `invite` — sets the invited user's own chosen password, audits `invite_accepted`, generic 401 on any failure; login afterwards runs Sequence F with the full second-factor gate (REQ-AUTH-060, REQ-API-121) |

### 4.4 Users (REQ-API-046…048)

All three require `is_admin`; a call by a non-admin is rejected (403 `forbidden`).

| Endpoint | Contract |
|---|------|
| `GET /api/v1/users` | 200 — array of user objects (`id`, `email`, `display_name`, `enabled`, `is_admin`, `auth_source`, `last_login_at`, `valid_until`, `status`, **`tfa_method`** (`off` \| `totp` \| `email`, GD-21, REQ-API-116) — the full user object of §4.3) (REQ-API-046) |
| `POST /api/v1/users` | body `{ "email": "…", "display_name": "…", "valid_days": 90, "password": "***" }` (`valid_days` ≥ 0, `0` = indefinite; `password` optional — stored only as an argon2id hash, REQ-AUTH-050); a new account → 201 user object; a disabled account with the same email is re-enabled → 200 user object (re-enabling resets the inactivity clock, REQ-AUTH-053) (REQ-API-047); audit `user_created` (`re_enabled` flag, `valid_until`) |
| `PUT /api/v1/users/{id}` | body — any subset of `{ "enabled": true\|false, "is_admin": true\|false, "valid_days": 90, "password": "***" }`; `enabled` is authoritative (idempotent, REQ-API-042); `is_admin` grants/revokes the system-administrator flag (idempotent like `enabled`, REQ-API-136) — any change that would leave zero **enabled** administrators (a revocation, a disabling, or both in one call) is rejected 409 `conflict` with nothing written and no audit entry (REQ-AUTH-068); the guard rides the same UPDATE statement that writes the row (`… WHERE id = ? AND EXISTS (SELECT 1 FROM users AS u2 WHERE u2.is_admin = 1 AND u2.enabled = 1 AND u2.id <> users.id)`), evaluated under the write lock so concurrent revocations cannot race to zero; `valid_days` re-sets `valid_until` (`0` → `NULL` = indefinite, REQ-AUTH-052); `password` set/resets the local hash, an **empty string clears** it (never returned, never logged, REQ-AUTH-036); re-enabling resets the inactivity clock (REQ-AUTH-053); 200 user object; disabling a user denies the effective permissions of that user's API tokens at call time (REQ-AUTH-033, REQ-API-048); a disabling or a password write raises the account's `session_epoch` in the same transaction, ending its PHP sessions, and the returned user object carries the new value (REQ-AUTH-074); audit `user_updated` with the changed attributes (`enabled`, `is_admin` old/new, `valid_until`, `password_changed` — the password value itself is never in the trail) |

**Two-factor endpoints (GD-21).** Self-service (acting user = self; also valid in the pending-first-factor context of `Authentication_Authorization_Design.md` §2.7 under a mandate), REQ-API-115:

| Endpoint | Contract |
|---|------|
| `GET /api/v1/users/me/tfa` | 200 `{ "method": "off\|totp\|email", "enrolled_at": … }` — never the secret or a code |
| `POST /api/v1/users/me/tfa/totp/enroll` | 200 `{ "secret": "<base32>", "otpauth_uri": "otpauth://totp/…" }` — shown once; pending until confirmed (REQ-AUTH-056) |
| `POST /api/v1/users/me/tfa/totp/confirm` | body `{ "code": "…" }`; a valid current RFC 6238 code activates `totp` and returns `{ "recovery_codes": [ … ] }` exactly once; wrong → 400 `bad_code`; audit `tfa_enrolled` |
| `POST /api/v1/users/me/tfa/email/start` | sends a confirmation code to the account address via the SMTP relay; 409 `smtp_not_configured` without one; rate-limited per account (REQ-AUTH-057) |
| `POST /api/v1/users/me/tfa/email/confirm` | body `{ "code": "…" }`; activates `email`, returns the recovery codes once; audit `tfa_enrolled` |
| `POST /api/v1/users/me/tfa/disable` | requires a valid current code or recovery code; sets method `off`, clears secret/codes; audit `tfa_disabled` |

Administrator (requires `is_admin`), REQ-API-116: `POST /api/v1/users/{id}/tfa/reset` — deletes the account's `user_two_factor` row back to `off` (lost device/email) and raises the account's `session_epoch` (REQ-AUTH-074); 200 `{ "status": "ok", "session_epoch": n }`; audit `tfa_reset`. No endpoint ever returns a secret, pending code, or recovery code outside its single enrollment/activation response.

**Password lifecycle endpoints (GD-22/GD-23).** Administrator: `POST /api/v1/users/{id}/invite` — sends the invitation email with a single-use `invite` token to the account's address (REQ-AUTH-060); requires `is_admin`; 409 `smtp_not_configured` without a relay (the admin then sets the password directly); a re-invite replaces any outstanding invite token (the old link dies); 200 `{ "ok": true }` — the token value is never in the response; audit `user_invited` (REQ-API-117). Self-service: `PUT /api/v1/users/me/password` with `{ "current_password": "…", "new_password": "…" }` — current password verified against `password_hash` first (failure → 401 `bad_password`, audit-logged, counted toward the REQ-AUTH-035 lockout), then the new argon2id hash is stored and the account's `session_epoch` raised — its other sessions end — and the answer is 200 `{ "ok": true, "session_epoch": n }`, which the calling session adopts (REQ-AUTH-074, superseding DEV-AUTH-13); accounts without a local credential → 409 `no_local_credential`; audit `password_changed` (REQ-AUTH-061, REQ-API-118).

### 4.5 Projects (REQ-API-049…052)

**`GET /api/v1/projects`** — project visibility only (`is_admin` or member, REQ-AUTH-026); projects outside the acting user's visibility do not appear (REQ-API-007). 200 — dashboard summaries:

```json
[ { "id": 33, "project_name": "MOBA", "organization": "NAT EU", "record_count": 42, "instrument_count": 5, "field_count": 128,
    "last_accessed_at": "2026-10-20 08:00:00" } ]
```

`last_accessed_at` is the **acting user's** newest audit event in that project — `SELECT project_id, MAX(created_at) FROM audit_events WHERE user_id = ? AND project_id IS NOT NULL GROUP BY project_id`, one read for the whole list (never one per project), served by `idx_audit_events_user_project` (`Audit_Logging_Design.md` §6.4). The stored UTC text passes through unformatted, and `null` means this account has never generated an event in that project — a project-less event (a login) belongs to no project (REQ-UI-071). It is what the overview orders its boxes by; this endpoint stays in name order, because recency is one page's presentation rather than a property of the list.

**`POST /api/v1/projects`** — `is_admin` (master spec: administrator users create projects). Body — the creation fields of the simplified `projects` (GD-17, REQ-DB-006):

```json
{
  "project_name": "MOBA", "organization": "NAT EU",
  "pi_name": "Ole Bull", "pi_email": "ole.bull@example.org",
  "dm_name": "Alexander Bull", "dm_email": "alexander.bull@example.org",
  "rek_number": "REK-202/123", "rek_start_date": "2026-01-01", "rek_end_date": "2028-01-01",
  "start_date": "2026-01-01", "end_date": "2028-01-01",
  "participant_names": "MOBA[0-9][0-9][0-9]",
  "record_naming": "manual"
}
```

The removed attributes — `end_provision`, the `option_*` flags, `agreed_to_end_user_contract`, `event_names` — are rejected as unknown attributes (400 `invalid_request`, REQ-API-052); if the owner wants them they are data in an ordinary instrument (e.g. `DataTransferProjects`, REQ-DB-032). A duplicate `project_name` → 409 `conflict`. Creation is single-arm (REQ-DB-011, DEV-API-4): the API creates **arm 1 only**, together with its first event **`baseline`** (`unique_event_name = baseline_arm_1`, timepoint `period = 0`) and one instrument **`instrument`** — every project holds at least one arm, event and instrument from creation on (§4.9/§4.10, REQ-API-050). Further events are added afterwards through `POST /api/v1/projects/{id}/events` (REQ-API-062; `event_names` is no longer part of creation, GD-17). 201 — the project object (`id` + the supplied fields + `creation_time`). Audit `project_created`.

**`GET /api/v1/projects/{id}`** — data access ≥ `read_only` + project visibility. 200 — full metadata plus structure plus the acting user's effective permissions (REQ-API-126):

```json
{
  "id": 33, "project_name": "MOBA", "…": "…(all metadata fields)",
  "record_count": 42,
  "arms": [ { "arm_num": 1, "name": "", "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1", "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 } ] } ],
  "instruments": [ { "id": 7, "name": "intake", "position": 1, "field_count": 24 } ],
  "permissions": {
    "project_admin": false,
    "arms": [ { "arm_num": 1, "data_access_level": "view_edit", "export_level": "export_de_identified",
                "delete_values": false, "edit_surveys": false } ],
    "grants": [ { "unique_event_name": "baseline_arm_1", "instrument": "intake",
                  "data_access_level": "read_only", "export_level": "export_none",
                  "delete_values": false, "edit_surveys": false } ]
  }
}
```

`record_count` is the project's record total — the summary the project home shows alongside its structure counts (REQ-UI-017). It is the same number the `GET /api/v1/projects` rows carry, so one detail read answers a project page without the web layer listing every visible project to find one heading (`User_Interface_Design.md` §12 rule 13); instrument and field counts come from `instruments[]` and its `field_count`s.

`permissions` is the effective evaluation of `Authentication_Authorization_Design.md` §4.1 for the acting user — the same result the boundary applies to every call — surfaced so the PHP layer can gate rendering with the control absent from the DOM (`User_Interface_Design.md` §3.1, REQ-UI-003). Rules: one `arms[]` entry per arm of the project in the arm order of the `arms` list above, carrying that arm's **default**; one `grants[]` entry per (instrument, event) pair mapped in an arm the caller may see, carrying the levels and rights **as they resolve for that pair** — its own grant where it has one, the arm default otherwise (REQ-AUTH-069). `is_admin` reports full levels and both rights everywhere and `project_admin: true` (REQ-AUTH-023); a role-less member likewise (REQ-AUTH-022); an ungranted arm reports `no_access` / `export_none` (REQ-AUTH-019), so nothing is ever absent from either list and the caller never infers a level from a missing entry. `grants[]` grows with the design's instrument × event count — the same cardinality as the record status dashboard it gates — and stays in the arm-then-event order of §4.8/§4.9 so the web layer can walk it without sorting. The vocabulary is the §4.7 one (`no_access | read_only | view_edit`, `export_none | export_de_identified | export_no_identifiers | export_full`, plus the two booleans). This read authorizes nothing — it discloses only the caller's own levels, under the same visibility gate as the rest of the response (REQ-API-007), and every endpoint re-checks at call time (REQ-AUTH-033).

**`PUT /api/v1/projects/{id}`** — `project_admin` (an `is_admin` user is covered by REQ-AUTH-023). Body: any subset of the `POST` metadata fields — omitted fields are unchanged (idempotent, REQ-API-042) — plus `record_naming`, the project's record naming mode (`auto` | `manual`, REQ-DB-045; any other value → 400 `invalid_request`, REQ-API-151). A duplicate `project_name` → 409 `conflict`. 200 — the updated project object (same shape as `GET`, `permissions` included — REQ-API-126). Metadata changes are audit-logged with old and new values (`project_updated`, `Audit_Logging_Design.md` §3.3; REQ-API-052, REQ-API-043).

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

**`POST /api/v1/projects/{id}/users/{uid}/token-viewed`** — records that the acting administrator unmasked member `{uid}`'s just-issued token on the Members page (REQ-API-160, `User_Interface_Design.md` §3.5). `is_admin` only. Body empty or `{}` — any attribute is 400 `invalid_request`; unknown project or user, or a non-member, is 404 with nothing written. → 200 `{ "ok": true }`. Audit `token_viewed` `{"member_email":"…"}` — one entry per call, separate from the add's `membership_changed` + `token_issued` (REQ-AUD-033). No token value travels in either direction: the UI already holds it from the add/rotation response and shows it only after this call succeeded, so the endpoint adds no new way to read a token.

**`GET /api/v1/projects/{id}/users/{uid}/token`** — self-service fetch of the member's own token (REQ-API-102). `{uid}` MUST equal the acting user (`X-Internal-User-Id`) and the acting user MUST be a member of the project; any other case is a uniform 403 `forbidden` (never disclosed as missing, REQ-API-007). 200:

```json
{ "token": "8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83" }
```

The value MUST NOT be logged (REQ-API-005). No audit event is written for the fetch itself — it is credential plumbing; the data-API calls that present the token carry it in the fixed `token` column (`Audit_Logging_Design.md` §2, REQ-AUD-018). This is the source the PHP layer uses to present the member's token to the data API for UI data entry (ASM-API-3; `User_Interface_Design.md` §8.6). A person **seeing** their own token goes through the audited reveal below instead.

**`GET /api/v1/users/me/projects`** — the acting user's own memberships, for the account page's Project tokens section (REQ-API-161, `User_Interface_Design.md` §2.8). Any authenticated user. Membership, not visibility: an `is_admin` user sees every project (REQ-API-049) but is listed only where they are a member, since only a membership carries a token. No token value; not audited. 200, in project-id order, possibly `[]`:

```json
[
  { "project_id": 3, "project_name": "8DISC", "role": null },
  { "project_id": 5, "project_name": "EMIT-23", "role": "data-entry" }
]
```

`role` is the role name, `null` for a role-less member with full permissions (REQ-AUTH-022).

**`POST /api/v1/users/me/projects/{id}/token-reveal`** — the member's audited view of their own token (REQ-API-162). Body empty or `{}` — any attribute is 400 `invalid_request`. The acting user MUST be a member of project `{id}`; otherwise — unknown project included — the uniform 403 `forbidden` (REQ-API-007), with nothing written. The `token_viewed` entry `{"member_email":"<own address>"}` (REQ-AUD-033) is written **first**; only once it is stored does the endpoint answer 200 `{ "token": "…" }` — the same shape as the fetch above. A failed write is a 500 with no value, so every view the UI shows is in the trail. Each call is one entry; the value MUST NOT be logged (REQ-API-005). Unlike `token-viewed`, which only records a view of a value the UI already holds, this endpoint both records and returns, because the account page holds no value until Show.

### 4.7 Roles (REQ-API-056…057)

All three endpoints require `is_admin`. Roles are project-scoped (REQ-AUTH-024) and not limited to preset examples (REQ-AUTH-020).

**`GET /api/v1/projects/{id}/roles`** — 200 — the project's roles, possibly an empty array:

```json
[ { "id": 9, "name": "data-entry", "project_admin": false,
    "arms":   { "1": { "data": "view_edit", "export": "export_full" },
                "2": { "data": "no_access", "export": "export_none" } },
    "grants": [ { "event": "baseline_arm_1", "instrument": "intake",
                  "data": "read_only", "export": "export_de_identified",
                  "delete_values": false, "edit_surveys": true } ] } ]
```

`data` levels: `no_access | read_only | view_edit`; `export` levels: `export_none | export_de_identified | export_no_identifiers | export_full` (REQ-DB-009/040; `Authentication_Authorization_Design.md` §4.1). `arms` is the per-arm default and always carries an entry for every arm of the project; `grants` lists only the pairs whose grant differs from their arm default — a pair absent from it inherits (REQ-AUTH-069). The two rights are booleans, never levels (REQ-AUTH-070).

**`POST /api/v1/projects/{id}/roles`** — body: the role object minus `id` (`name`, `project_admin`, `arms`, optional `grants`); an arm absent from `arms` defaults to `no_access` / `export_none` (REQ-AUTH-019 — no implicit access). A duplicate name within the project → 409 `conflict`. 201 — the role object. Audit `role_created` (`Audit_Logging_Design.md` §3.4).

**`PUT /api/v1/projects/{id}/roles/{rid}`** — body: the same shape as `POST`, replacing the role's name, `project_admin`, arm defaults and grants in one transaction; a pair omitted from `grants` reverts to its arm default, and an `arms` map missing an arm sets that arm to `no_access` / `export_none` (REQ-API-143). Unknown `{rid}` → 404; duplicate name → 409; an `event`/`instrument` pair not mapped in the project → 400 naming it, nothing applied. 200 — the updated role object. Audit `role_updated` with the before/after permission set; members holding the role pick the change up on their next request without re-authentication (REQ-AUTH-033).

### 4.8 Arms (REQ-API-058…060, REQ-API-150)

**`GET /api/v1/projects/{id}/arms`** — data access ≥ `read_only`. 200 (events in the canonical per-arm order, GD-15; `period` is `null` when the event has no timepoint):

```json
[ { "id": 5, "arm_num": 1, "name": "",
    "events": [ { "id": 4, "event_name": "baseline", "unique_event_name": "baseline_arm_1",
                  "period": 0, "safe_region_start": null, "safe_region_end": null, "position": 1 },
                { "id": 9, "event_name": "screening", "unique_event_name": "screening_arm_1",
                  "period": null, "safe_region_start": -2, "safe_region_end": 3, "position": 2 } ] } ]
```

**`POST /api/v1/projects/{id}/arms`** — `project_admin`. Body `{ "name": "…" }`; `arm_num` is the next 1-based number (a duplicate → 409 `conflict`). 201 — the arm object. Audit `arm_created`.

**`PUT /api/v1/arms/{id}`** — `project_admin`; the path is kept verbatim (ASM-API-1); idempotent (REQ-API-042). Body `{ "name": "…" }` writes the display label only — id, `arm_num`, position and events are untouched; arm names carry no uniqueness constraint. The rename is never breaking in analysis mode (§4.21) — labels are decoupled from `unique_event_name` values (REQ-DB-011) — yet `acknowledge_breaking` is accepted for uniformity with the other structure writes. 200 — the arm object. Audit `arm_updated` with `{arm_num, changes:{name:{old,new}}}` (the last-arm-reset audit of `DELETE …/arms/{id}` shares this shape; REQ-API-150).

**`DELETE /api/v1/arms/{id}`** — `project_admin`; the path is kept verbatim (ASM-API-1). 204. An arm that still has events or data → 409 `conflict` (DEV-API-6; phase-1 edge case, ASM-API-4) — **unless it is the project's last remaining arm**: that one cannot go either, and the call instead renames it to `arm_1`, keeping its id, `arm_num` and events (a renumbering would rewrite every `unique_event_name` and its stored values; the rename is a label). The answer is then 200 with the renamed arm object. Audit `arm_deleted`, or `arm_updated` with `last_arm_reset` on the rename (REQ-API-128; VISION "Arms, events and instruments").

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

### 4.10 Instruments (REQ-API-064…066, REQ-API-101, REQ-API-130, REQ-API-152, REQ-API-153, REQ-API-154)

**`GET /api/v1/projects/{id}/instruments`** — data access ≥ `read_only`. 200:

```json
[ { "id": 7, "name": "intake", "position": 1, "field_count": 24,
    "is_survey": false, "branching_logic": "", "repeating_events": [] },
  { "id": 9, "name": "visits", "position": 2, "field_count": 6,
    "is_survey": false, "branching_logic": "", "repeating_events": ["baseline_arm_1"] } ]
```

`repeating_events` (REQ-API-154) names the events the instrument repeats at — arms by number, events in canonical order (GD-15), `[]` when none; every instrument object (list, create, update, reset) carries it.

**`POST /api/v1/projects/{id}/instruments`** — `project_admin`. Body `{ "name": "intake" }`; the name is unique within the project (REQ-DB-013; a duplicate → 409 `conflict`). 201 — the instrument object (`position` = end of list). Audit `instrument_created`.

**`PUT /api/v1/projects/{id}/instruments/order`** — `project_admin`; idempotent. Body — the full ordered list of the project's instrument ids (a partial list → 400 `invalid_request`):

```json
{ "order": [7, 12, 3] }
```

The record-identifier invariant MUST hold after reordering (GD-8: the first field of the earliest instrument with fields is the record identifier) — a violation → 400 `invalid_request` (§4.2); the identifier's instrument may not be repeating → 409 `conflict` (REQ-API-154). Audit `instrument_reordered`.

**`PUT /api/v1/projects/{id}/instruments/{iid}`** — `project_admin`; idempotent (REQ-API-101). Body: any subset of `{ "name", "is_survey", "branching_logic" }`, e.g. `{ "is_survey": true }`, `{ "branching_logic": "[baseline][consent]=\"1\"" }`. A `name` rename MUST satisfy REQ-DB-011 (1–255 ASCII letters, digits, spaces or underscores) and remain unique within the project — a collision → 409 `conflict`, an empty, invalid, overlong or non-string name → 400 `validation_error` (REQ-API-130). The rename is a label for a form's first instance — its stored values are keyed by field name and event — and the mapping pairs and their repeating designation stay; the further instances of a repeating instrument are stored under its name and move with it in the same transaction (REQ-DB-048). `is_survey: true` on an instrument that repeats anywhere → 409 `conflict` (REQ-API-154). An invalid branching expression is rejected at design time → 400 `validation_error` (REQ-VAL-029, `Data_Validation_Design.md` §7.2). 200 — the instrument object. Audit `instrument_updated` with old and new values.

**`DELETE /api/v1/projects/{id}/instruments/{iid}`** — `project_admin`. Removes the instrument together with its fields, their stored values (same transaction, like REQ-API-070), the further instances stored under its name — a field's moved to another instrument since included (REQ-DB-049) — and its mapping pairs; losing recorded values makes the change breaking in analysis mode (§4.21). An expression that survives the change naming a doomed field → 409 `conflict` — references inside the deleted design go with it, which is what commit checks against the post-change design too. 204. Audit `instrument_deleted` with the removed field and value counts (REQ-API-127). **A project always keeps at least one instrument**: deleting the last remaining one only deletes its fields and renames the shell to `instrument` — id, position, survey flag and branching logic stay — answering 200 with the object and auditing `instrument_updated` with `last_instrument_reset` (VISION "Arms, events and instruments": "only the fields in that instrument should be deleted. The instrument should be renamed to 'instrument'").

**`PUT /api/v1/projects/{id}/instruments/{iid}/repeating`** — `project_admin`; idempotent (REQ-API-154). Designates the events the instrument repeats at (REQ-DB-047); the list replaces the whole set, `[]` turns repeating off:

```json
{ "events": ["baseline_arm_1", "follow_up_arm_1"] }
```

Every name must be an event the instrument is mapped to — an unknown or unmapped one → 400 `validation_error`; `events` missing or not a list → 400 `invalid_request`. A survey instrument (GD-9: one link fills one form), the first instrument, and the instrument holding the record identifier (GD-8) → 409 `conflict`. Never breaking: turning repeating off keeps the further instances' values, hidden until the pair repeats again (REQ-DB-048). In production the change goes to the open staging set like every structure write (§4.21; diff kinds `repeating_on`/`repeating_off`). 200 — the instrument object. Audit `instrument_updated` with `changes.repeating_events` old → new; an unchanged set writes nothing.

**`POST /api/v1/projects/{id}/instruments/import`** — `project_admin` (REQ-API-152). Creates instruments and their fields from REDCap dictionaries: an **instrument ZIP** (REDCap's instrument export — `instrument.csv` plus `OriginID.txt`, the exporting server's name) or a bare **data-dictionary CSV** (one or several forms). Body — the files in import order, each base64-encoded:

```json
{ "files": [ { "name": "IncomingStudies_2026-10-12_1141.zip", "content": "UEsDBBQ…" },
             { "name": "Series_2026-10-12_1141.zip", "content": "UEsDBBQ…" } ],
  "dry_run": true }
```

A file whose content starts with the ZIP signature `PK\x03\x04` is an archive (`archive/zip`): only `instrument.csv` and `OriginID.txt` are read — anything else (REDCap's `attachments/` folder) is ignored with a warning, and an archive without `instrument.csv` is a failure; `instrument.csv` is read through a 16 MiB limit, so a compressed archive cannot expand without bound. Any other file is the CSV itself. The CSV is UTF-8 (a leading byte-order mark is dropped; invalid UTF-8 is a failure), parsed by `encoding/csv` — quoted cells may span lines. Its header row names REDCap's columns; `Variable / Field Name`, `Form Name`, `Field Type` and `Field Label` are required, and the dictionaries of older REDCap versions, which lack `Matrix Ranking?` and `Field Annotation`, are accepted. Unknown attributes, an empty `files` list or a missing `name`/`content` → 400 `invalid_request`; the request body is bounded by REQ-TECH-011.

**Processing**, as one import over all files in list order:

1. **Rows → instruments.** Each distinct `Form Name` becomes one instrument and is appended to the instrument list in order of first appearance; a form's fields keep their row order. A valid name per REQ-DB-011 is preserved exactly; otherwise, leading/trailing whitespace is trimmed, each run of disallowed characters is replaced by `_`, the result is limited to 255 characters, and a blank result falls back to `instrument_<position>`. A changed name produces a warning with the original and resulting names. The rows of a form MUST be contiguous: a form that reappears after another is a failure naming the first out-of-place row (`form_not_contiguous`). An empty `Form Name` is a failure. A row is numbered as a spreadsheet shows it — the header is row 1, and a quoted cell spanning lines stays one row.
2. **No identifier yet.** When the project has no fields, the import's first field is created **in the first instrument** — it establishes the record identifier (GD-8) — and the rest of its form becomes a new instrument as in step 1 (none, when the form held only that field). The first instrument keeps its name. A first field that cannot carry a value (`descriptive`, or a `file` field, which is skipped) is a failure (`identifier_not_value`) — the preview asks for another file order. If any instrument already has fields, their identifier remains in place, including when the first instrument is empty.
3. **Conversion** per the table below. `file` fields are skipped with a warning (`skipped`); a branching expression that names a skipped field is a failure.
4. **Names.** A field name already in use — by the project, by a derived checkbox name (REQ-DB-042), or by an earlier row of this import — gets `_2`, `_3`, … appended, the first suffix that is free; an instrument name already in use gets the same treatment, truncated as needed so the result stays within 255 characters. Each rename is a warning (`renamed`) naming the old and new name. Every reference to a renamed field in the imported branching logic is rewritten to the new name — `[f]`, `[event][f]` and `[f(code)]` alike, while references to fields the import did not rename stay as written. A field name that breaks REQ-VAL-011 is a failure; one longer than 26 characters is a warning (REQ-VAL-013).
5. **Validation.** Every converted field passes the design-time rules of a designer write (`Data_Validation_Design.md` §9), against one design view that already holds **every** imported field — so a reference may point forward, as REDCap allows. One rule is relaxed: a reference to a field the same import creates is checked for existence and value-carrying only, because the import maps no instrument (step 6) and the active-at-event rule of §7.2 of `Data_Validation_Design.md` could never hold; it applies once a person maps the instrument. References to fields that existed before keep the full rule. A rejection is a failure naming its file, row and reason.
6. **Write.** Without failures, the instruments and fields are written in **one transaction** (all-or-nothing, as REQ-API-132); in production mode they apply to the open staging set as one change (§4.21; no open set → 409), in analysis mode `acknowledge_breaking` is read from the top level as for every structure write. **No instrument–event mapping is created**: the imported instruments collect no data until they are mapped (REQ-DB-012, §4.12).

**Response.** The dry run (`"dry_run": true`) writes nothing and answers 200 with the result, failures included, so the caller can show all of them at once; the import itself answers 201 with the same shape, the created ids filled in, and a failure answers 400 `validation_error` with the same `problems` list.

```json
{ "dry_run": true, "importable": true,
  "record_identifier": { "field": "study_instance_uid", "instrument": "instrument", "moved": true },
  "instruments": [
    { "id": null, "name": "incoming_studies", "renamed_from": null, "existing": false,
      "file": "IncomingStudies_2026-10-12_1141.zip", "origin": "fiona.ihelse.net",
      "fields": [ { "row": 3, "field_name": "study_institution_name", "renamed_from": null,
                    "source_type": "text", "field_type": "text",
                    "validation_type": "", "validation_format": null } ] } ],
  "warnings": [ { "file": "Projects_2026-10-12_1139.zip", "row": 31, "field": "project_end_data",
                  "code": "skipped", "message": "file fields are not supported; the field is not imported" } ],
  "problems": [] }
```

`existing` marks the project's first instrument when it receives the initial identifier field (step 2). `code` ∈ `renamed`, `skipped`, `dropped_attribute`, `converted`, `long_name`, `ignored_entry` for warnings; `form_not_contiguous`, `identifier_not_value`, `unsupported_type`, `invalid_csv`, `invalid_archive`, `missing_column`, `missing_instrument_csv`, `validation_error` for problems. **Audit** (REQ-AUD-010): `instrument_created` per instrument and `field_created` per field, in the same transaction, each carrying `"imported_from": { "file": …, "origin": … }` and — after a rename — `"renamed_from"` (`Audit_Logging_Design.md` §3.3).

**Conversion** (normative). Columns:

| REDCap column | Becomes |
|---|---|
| `Variable / Field Name` | `field_name` (step 4) |
| `Form Name` | the instrument (step 1) |
| `Section Header` | `section_header`; `<br>`, `<br/>`, `<br />` become a line break |
| `Field Type` | `field_type`, per the type table |
| `Field Label` | `field_label`; `<br>` variants become a line break; any other markup stays as written and renders escaped (REQ-VAL-031) |
| `Choices, Calculations, OR Slider Labels` | for choice types the `choices` in REDCap's form — split on `\|` between choices and on the first comma inside a choice (REQ-VAL-022); for `calc` the `calculation` (type table); otherwise ignored |
| `Field Note` | `field_note`; `<br>` variants become a line break |
| `Text Validation Type OR Show Slider Number` | `validation_type` + `validation_format`, per the validation table |
| `Text Validation Min` / `Max` | `validation_min` / `validation_max` for `integer` and `floating point`; elsewhere dropped (`dropped_attribute`, REQ-VAL-020) |
| `Identifier?` | `y` → `direct_identifier` (REDCap removes identifiers from de-identified exports, as REQ-EXP-020 does) |
| `Branching Logic (Show field only if...)` | `branching_logic`, as written — REDCap's spelling is part of the grammar (REQ-VAL-044); only renamed references are rewritten (step 4) |
| `Required Field?` | `y` → `required` |
| `Field Annotation` | `field_annotation`, as written (REQ-DB-046) |
| `Matrix Group Name` | `matrix_group`; a `radio` row with a group becomes a `matrix` row (REQ-DB-014) |
| `Custom Alignment`, `Question Number (surveys only)`, `Matrix Ranking?` | no counterpart — dropped with a warning when not empty (`dropped_attribute`) |

Field types:

| REDCap `Field Type` | `field_type` |
|---|---|
| `text`, `notes`, `dropdown`, `radio`, `checkbox`, `yesno` | the same type (a `checkbox` code outside `^[a-z0-9_]+$` is a failure, REQ-VAL-022) |
| `truefalse` | `radio` with the choices `1, True \| 0, False` (`converted`) |
| `descriptive` | `description` |
| `calc` | `calculated`; a `[field]` reference is qualified with the project's first event in canonical order (GD-15), because the calculation grammar takes `[event][field]` only (§6.1 of `Data_Validation_Design.md`); an expression outside that grammar (REDCap functions such as `round()` or `if()`) is a failure |
| `slider` | `text` with validation `integer` 0–100 — REDCap's stored slider value (`converted`) |
| `file` | skipped with a warning (`skipped`) — also with REDCap's `signature` validation |
| `sql`, any other | a failure (`unsupported_type`) |

Validation types (REDCap name → `validation_type` + `validation_format`):

| REDCap | This system |
|---|---|
| `date_ymd`, `date_dmy`, `date_mdy` | `date` + `Y-m-d` / `d-m-Y` / `m-d-Y` |
| `datetime_ymd`, `datetime_dmy`, `datetime_mdy` | `datetime` + `Y-m-d H:i` / `d-m-Y H:i` / `m-d-Y H:i` |
| `integer` | `integer` |
| `number`, `number_1dp` … `number_4dp` | `floating point` (`converted` for the `_dp` forms: the decimal count is not enforced) |
| `email` | `email` |
| any other name | the registry entry of that name when one exists (§4.2 of `Data_Validation_Design.md`); otherwise dropped with a warning — the field stays free text (`dropped_attribute`). This covers REDCap's `datetime_seconds_*`, `time` and `phone`, which have no counterpart |

**`GET /api/v1/projects/{id}/instruments/export`** — data access ≥ `read_only` (REQ-API-153). The project's data dictionary as a REDCap instrument ZIP: `application/zip` with `Content-Disposition: attachment; filename="<project>_<YYYY-MM-DD>_<HHMM>.zip"` — the project name with every character outside `[A-Za-z0-9._-]` replaced by `_`, the time in UTC (GD-7). The archive holds `instrument.csv` and `OriginID.txt`, the host of `WEB_PUBLIC_URL` (`clara` when unset), as REDCap's instrument export does. The design is the **active** one, the live tables: an open staging set is not exported, as data collection does not use it either (REQ-API-107). The file carries design only, no record values.

`instrument.csv` starts with REDCap's header line exactly as REDCap writes it and has one row per field — instruments by position, fields by position, so the record identifier (GD-8) is the first row. Its conversion is the import's tables above, run backwards:

| Field attribute | REDCap column |
|---|---|
| `field_name` | `Variable / Field Name` |
| the instrument's name | `Form Name` |
| `section_header`, `field_label`, `field_note` | `Section Header`, `Field Label`, `Field Note`, as stored — line breaks kept |
| `field_type` | `Field Type`: `text`, `notes`, `dropdown`, `radio`, `checkbox`, `yesno` as they are; `matrix` → `radio` with its `matrix_group` in `Matrix Group Name`; `description` and `header` → `descriptive`; `calculated` → `calc` |
| `choices` | `Choices, Calculations, OR Slider Labels`: `code, label \| code, label` for the choice types; empty for `yesno`, whose coding is fixed (REQ-DB-043); the expression for `calc` |
| `validation_type` + `validation_format` | `Text Validation Type OR Show Slider Number`: `integer` → `integer`; `floating point` → `number`; `date` / `datetime` → `date_ymd`, `date_dmy`, `date_mdy` / `datetime_ymd`, … by the order of `Y`, `m`, `d` in the format (`Y-m-d` when unset); a registry type by its name |
| `validation_min` / `validation_max` | `Text Validation Min` / `Max` |
| `direct_identifier` | `Identifier?` → `y` |
| `branching_logic` | `Branching Logic (Show field only if...)`, as stored |
| `required` | `Required Field?` → `y` |
| `field_annotation` | `Field Annotation` |
| — | `Custom Alignment`, `Question Number (surveys only)`, `Matrix Ranking?` empty |

Importing the file into a project whose first instrument is empty (step 2 above) recreates the same fields. REDCap's format has no column for the rest, which therefore does not travel: the `personal_information` and `export_approved` flags; an instrument's survey flag and branching logic; events and the instrument–event mapping. A `header` field comes back as a `description`, and a date format whose tokens are not in the order `Y-m-d`, `d-m-Y` or `m-d-Y` comes back as `Y-m-d`. Calculations keep their event-qualified references, which the import leaves as written.

### 4.11 Fields (designer) (REQ-API-067…071, REQ-API-096, REQ-API-132, REQ-API-157)

**`GET /api/v1/projects/{id}/instruments/{iid}/fields`** — data access ≥ `read_only`. 200 — the instrument's fields in position order, with all data-dictionary attributes (REQ-DB-013):

```json
[ { "id": 11, "field_name": "age", "field_label": "Age", "field_type": "text",
    "section_header": "", "choices": "", "field_note": "",
    "validation_type": "integer", "validation_format": null, "validation_min": "18", "validation_max": "99",
    "required": true, "branching_logic": "", "calculation": "", "matrix_group": "",
    "field_annotation": "", "personal_information": false, "position": 2 } ]
```

**`POST /api/v1/projects/{id}/instruments/{iid}/fields`** — `project_admin`. Body: the field object minus `id`/`position` (appended at the end of the list). The field name is lower-case alphanumeric + underscore, unique within the project (REQ-DB-013; a duplicate → 409 `conflict`); names longer than 26 characters are accepted — the warning after 26 is a UI concern (master spec). `validation_type` must be empty, one of the built-in structured types, or an existing `validation_types` name (`Data_Validation_Design.md` §4.2; anything else → 400 `validation_error`, REQ-VAL-010/042); `direct_identifier` is accepted for any field and defaults to `1` when the validation type is `email`, `MRN`, `international phone` or `national phone` (REQ-EXP-020). `choices` is accepted in the stored `code$label##…` encoding or in REDCap's `code, label | code, label` form and always stored in the first (`Data_Validation_Design.md` §9, REQ-VAL-022); `field_annotation` is free text, stored as given (REQ-DB-046); `field_label` and `section_header` have no length limit (DEV-DB-15). `calculation` is only allowed with `field_type = calculated`; the expression and the branching logic are validated at design time → 400 `validation_error` (REQ-VAL-029, REQ-VAL-033/034; `Data_Validation_Design.md` §6.2, §7.2). 201 — the field object. Audit `field_created`.

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

**`POST /api/v1/projects/{id}/instruments/{iid}/fields/{fid}/move`** — `project_admin` (REQ-API-157). Moves the field `{fid}` of instrument `{iid}` into another instrument of the project, or to another place in its own:

```json
{ "instrument_id": 9, "before_field_id": 31 }
```

`instrument_id` is the target (it may be `{iid}`); `before_field_id` is the field of the target the moved one goes in front of — `null` or absent: last, which is also the only place in an instrument without fields. Both instruments are renumbered 1..n. An unknown target, or a `before_field_id` that is the field itself or not a field of the target → 400 `validation_error`; `{fid}` not a field of `{iid}` → 404. The GD-8 invariant holds as for reordering: while records exist the field at position 1 of the first instrument stays there — moving it away, or another field in front of it, → 400 `invalid_request`. A move to the place the field already holds writes nothing (200). 200 — the field object at its new position. Audit `field_moved` (`Audit_Logging_Design.md` §3.3).

The field keeps its id, name, attributes and stored values (REQ-DB-049). Values are keyed by field name and event, so its first instances show wherever the target instrument is used and nothing in `data` is rewritten; from then on the target's mapping, repeating designation and role rights (REQ-AUTH-069) apply to them. What the target does not show stays stored and hidden: values at an event the target is not mapped to, as for an unmapped pair (§4.12), and the further instances kept under the source instrument's name; moving the field back shows them again — which is why a move is never breaking (§4.21). Expressions name fields and events, not instruments, so every expression stays valid. Inside a repeating instrument a calculation reads its own instrument's fields from its own instance and every other field from the first (REQ-VAL-045): a move into or out of an instrument that repeats somewhere recomputes, in the same transaction, the further instances of the moved calculated field and of the two instruments' calculated fields that read it; first instances read first instances either way and keep their values. Production stages the move like every structure write (diff kind `field_moved`); a commit then recomputes nothing, as for any staged change (below).

The move is a `POST` action rather than a `PUT`: its path names the field's present instrument, so the identical request repeated after a move no longer finds the field there (404) — not idempotent as REQ-API-042 requires of a `PUT`.

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

### 4.13 Record status (REQ-API-074, REQ-API-110, REQ-API-149, REQ-API-156, REQ-API-171, REQ-API-172)

**`GET /api/v1/projects/{id}/record-status`** — data access ≥ `read_only` + record visibility (REQ-AUTH-045). 200 — all visible records, with their instruments per event in the instrument order of each arm and a **three-state** completion state per (record, event, instrument), listed **sparsely** (REQ-API-149, DEV-API-26):

```json
[ { "record_id": "MOBA042",
    "events": [ { "unique_event_name": "baseline_arm_1",
                  "instruments": [ { "name": "intake", "state": "finished" },
                                   { "name": "scores", "state": "some_data" } ] },
                { "unique_event_name": "week_4_arm_1",
                  "instruments": [] } ] },
  { "record_id": "MOBA043",
    "events": [ { "unique_event_name": "baseline_arm_1", "instruments": [] },
                { "unique_event_name": "week_4_arm_1",   "instruments": [] } ] } ]
```

`state` is `no_data` | `some_data` | `finished`. Only `finished` is stored (REQ-DB-036); the other two are computed per row from whether any of that instrument's fields in that event holds a value, so the field never reports `no_data` for an instrument that has values or `some_data` for one the user marked finished. A survey-marked instrument always reports `finished` — its completion info is filled in automatically, with nothing stored and no assignment taken; the other two states never apply to it (GD-9; master spec "Instrument level completion info").

**Sparse listing** (REQ-API-149). Every visible record is listed, and under it every event of a readable arm that has at least one mapped instrument; an event's `instruments` carries only the cells that are not `no_data`, as an empty list when there are none. A mapped instrument absent from a listed event is `no_data` — in the example, `labs` at `baseline_arm_1` and everything at `week_4_arm_1`; the mapped pairs come from the instrument–event mapping (§4.12). An event absent from a record is **not reported** (an arm the caller may not read, GD-2) and carries no state, so a client renders it as unknown, never as grey. The dense alternative — one cell per (record, event, mapped instrument) — grew with the design rather than the data: a 32-record stress project with 12,348 mapped pairs answered 15 MB for 102 non-empty cells, beyond what the web layer can decode within its `memory_limit` (DEV-API-26).

**Repeating instruments** (REQ-API-156). For an instrument repeating at the event (REQ-DB-047) the entry adds `instances` — every instance whose state is not `no_data`, ascending, each with its own state — while `state` stays the first instance's; the entry is listed when any instance holds a state:

```json
{ "name": "visits", "state": "some_data",
  "instances": [ { "instance": 1, "state": "some_data" }, { "instance": 2, "state": "finished" } ] }
```

An instance's state is derived like a cell's — from its own values — and `finished` is its own assignment. A pair that does not repeat carries no `instances` and states its first instance only; further instances it kept from a time it repeated are hidden (REQ-DB-048).

The response MUST NOT contain field values (REQ-API-074). A record-status read is not a record view (`Audit_Logging_Design.md` §8, ASM-AUD-2).

**`PUT /api/v1/projects/{id}/records/{record}/events/{event}/instruments/{iid}/completion`** — data access ≥ `view_edit` on the record's arm (REQ-API-110); idempotent (REQ-API-042). Body:

```json
{ "state": "finished", "instance": 2 }
```

`instance` (optional, default 1) addresses one instance of a repeating instrument (REQ-API-156): above 1 only where the pair repeats, else 409 `conflict`; not a positive integer → 400. The audit payload names a further instance as `instance`. `state` is `finished` (set) or `unfinished` (clear, returning the row to its derived `no_data`/`some_data`). The call writes **no field value** — it is a workflow annotation, not data entry, and MUST NOT be rejected by validation or by analysis-mode write rules that apply to values (GD-20 scopes mode rejection to imports of data; this endpoint changes none). Unknown record/event/instrument → 404 `not_found`; an (event, instrument) pair not mapped in the active design → 409 `conflict`; a survey-marked instrument → 409 `conflict` — it needs no assignment, its `finished` state is automatic (REQ-DB-036, GD-9). A record outside the caller's data-access-group scope → 403 `forbidden` (REQ-AUTH-045). 200 — `{ "state": "finished" }`, the resulting stored state. Audit `instrument_completed` / `instrument_uncompleted` (REQ-AUD-026, `Audit_Logging_Design.md` §3).

**`GET /api/v1/projects/{id}/record-completeness`** — the same access, visibility and arm rules as `record-status` (REQ-API-171). 200 — every visible record with its completeness count, listing only the events where the record holds a value of a counted field and, under them, only the cells holding one:

```json
[ { "record_id": "MOBA042", "filled": 31, "visible": 48,
    "events": [ { "unique_event_name": "baseline_arm_1",
                  "hidden": ["pregnancy"],
                  "instruments": [ { "name": "intake", "filled": 12, "visible": 14 },
                                   { "name": "visits", "filled": 5, "visible": 9,
                                     "instances": [ { "instance": 1, "filled": 5, "visible": 9 },
                                                    { "instance": 2, "filled": 3, "visible": 9 } ] } ] } ] },
  { "record_id": "MOBA043", "filled": 0, "visible": 0, "events": [] } ]
```

- **Cell.** `visible` counts the instrument's enterable fields — `text`, `notes`, `dropdown`, `radio`, `checkbox`, `yesno` and matrix rows, without the record identifier field (GD-8) and without fields annotated `@READONLY` (REQ-DB-046) — whose branching logic is true at that (record, event, instance); `filled` counts those holding a value, a checkbox once when any box is checked (REQ-DB-042). A field hidden by branching counts toward neither, whatever it stores. `instances` follows REQ-API-156: every instance holding a value, ascending, while `filled`/`visible` on the entry are the first instance's.
- **Hidden instruments.** `hidden` names the mapped instruments whose instrument-level logic is false at that event (REQ-VAL-040); they count toward nothing and have no entry under `instruments`, even when they hold values (branching is display-only, GD-13).
- **Record.** `filled`/`visible` on the record sum every cell at the listed events: each mapped instrument that is not hidden, with its first instance and each further instance holding a value — an instrument without values adds its `visible` with `filled` 0. An event where the record holds no value of a counted field is not listed and adds nothing, so visits not yet reached do not lower the count — nor does an event that holds only a calculated value (computed wherever its field is active, REQ-VAL-037), a `@READONLY` value, or the identifier, which an import writes on every row's event. A record without values answers `0`/`0`, which a client shows as empty.
- **Counts, not ratios.** Two integers let a client map a cell exactly onto its nine dots and let PHP and JavaScript round the same way; `visible` 0 means nothing to fill.
- **Branching** is evaluated as REQ-VAL-048 states (`Data_Validation_Design.md` §7.3): `[field]` names the cell's event (DEV-VAL-13); a reference to the cell's own repeating instrument reads the cell's instance, any other reads first instances (REQ-VAL-045); an expression that does not compile is true. A reference to an (event, instrument) pair the caller does not hold at `read_only` or above (`authz.Levels.PairOrArmDefault`) reads as empty, so the count discloses nothing of a pair the caller cannot open.

**Cost.** The count is computed on each request and stored nowhere, so it needs no migration, and `record-status` does not pay for it (REQ-API-171); the dashboard draws its grid from `record-status` and fills in the dots from this read when it arrives. Per chunk of records (Streaming, below) the handler reads the same non-empty rows `record-status` reads, with their `value`, and keeps the values of only the fields some branching expression references (`validate.LogicProgram.Refs`). Expressions compile once per request — microseconds against the read, and no cache to keep in step with designer writes. Each expression is evaluated once per (record, event holding a value), and once per instance only when it reads its own repeating instrument. The work therefore grows with stored values and with the events records have reached, never with mapped (event, instrument) pairs that hold nothing — the growth DEV-API-26 removed from `record-status`. Branching never reads another record (REQ-VAL-029), so if measurement calls for it, a per-record cache keyed by the structure generation and cleared by any write to that record would be exact.

The response MUST NOT contain field values (REQ-API-171). A completeness read is not a record view (`Audit_Logging_Design.md` §8, ASM-AUD-2).

**Streaming (REQ-API-172).** Both reads are produced in chunks of 1,000 visible records, taken in the order the records are listed (`record_entities` by `record_id`, REQ-API-074). Before this, a read scanned every non-empty value of the project into memory before writing its first byte — 28 s and 285 MB of `record-status` for the 400,000-record Incoming project (`stress/incoming_stress.py`), longer than the web layer waits for a buffered call.

- **Once per request**, before any byte: the scope — access, the visible records (ids only), the column layout — and, for the count, the compiled design. A refusal therefore still answers the §4.2 error with its status.
- **Per chunk**: the chunk's non-empty positions — `… WHERE project_id = ? AND value <> '' AND record_id IN (…)`, served by the (`project_id`, `record_id`) prefix of the `data` unique key on SQLite and by `idx_data_project_record` on MariaDB (`Database_Schema_Design.md` §9) — and its `finished` assignments (the `instrument_completion` primary key prefix); the count reads the same rows with their `value` and keeps the referenced fields' values as the branching operands. One query, not a second one filtered by `field_name` as well: SQLite serves `field_name IN (…) AND record_id IN (…)` from `idx_data_project_field`, which would read the referenced fields of every record once per chunk. Marks and operands live for one chunk; a record's rows are complete once its chunk is read, since branching never reads another record (REQ-VAL-029).
- **First byte**: the first chunk is computed before the status line, so a store failure there is the usual 500. Each later chunk is written as it is finished and flushed.
- **Formats**, chosen on `Accept`. A request listing `application/x-ndjson` (q > 0) receives `Content-Type: application/x-ndjson`: each record object of the shapes above on its own line, then the end line:

  ```
  {"record_id":"MOBA042","events":[…]}
  {"record_id":"MOBA043","events":[…]}
  {"end":true,"records":2}
  ```

  Any other request receives `application/json`: the array exactly as before — `[`, the record objects separated by `,`, `]`, a newline — the bytes the buffered encoder wrote, so no caller changes.
- **Failure after the first byte** is logged and ends the response where it stands: no end line, no closing `]`. A caller that stops reading ends the loop at the next write.
- **No transaction across chunks.** Each chunk reads what is committed when it is reached, so a record written to mid-stream is reported as it stood when its chunk was read; holding one read transaction for the length of a stream would hold back SQLite's WAL checkpoints for as long.

Measured on the same Incoming project (SQLite, 2026-10-30), both reads answer byte for byte what the buffered reads answered. The first records arrive after 0.33 s instead of 28–30 s, and the server's peak resident memory falls from 2.9 GB to 230 MB. The whole read takes longer: 40 s instead of 28 s for `record-status`, 45 s instead of 30 s for the count. Rows reached through the (`project_id`, `record_id`) prefix are fetched out of storage order, at about twice the page reads of the one project scan they replace. A larger SQLite page cache or `mmap_size` does not change that ratio.

### 4.14 Export (UI) (REQ-API-075…076)

**`GET /api/v1/projects/{id}/export`** — query parameters (all optional; unknown ones accepted and ignored, REQ-API-017; DEV-API-12):

| Parameter | Values | Default | Meaning |
|---|---|---|---|
| `format` | `csv` \| `json` | `csv` | encoding (REQ-API-075) |
| `arm` | arm number, repeatable (`arm=1&arm=3`) | all arms the caller may export | restricts the export to those arms — this is what makes a higher per-arm sensitivity obtainable by separate per-arm exports (REQ-EXP-003); an arm the caller cannot export → 403 `forbidden`; an unknown arm number → 404 `not_found` |
| `rawOrLabel` | `raw` \| `label` | `raw` | choice values as codes or labels (REQ-EXP-010, same semantics as the data API's `rawOrLabel`, §3.1) |
| `rawOrLabelHeaders` | `raw` \| `label` \| `both` | `raw` | column names; `both` renders each header as `<Field Label> (field_name)` — the bare name where the field carries no label (§3.1, REQ-EXP-010) |
| `csvDelimiter` | `,` \| `;` \| `\|` \| `^` \| tab (the character or `tab`) | `,` | the data API's set (§3.1); empty value = comma (REQ-EXP-013); CSV only |
| `decimalCharacter` | `.` \| `,` | stored form | decimal separator of `calculated` and `floating point` values, in either format; empty value = stored form (REQ-EXP-015) |

The response never carries a byte-order mark: the web UI writes one ahead of a CSV download when the user asks for it (REQ-EXP-016, `User_Interface_Design.md` §6.4).

Known parameters validate strictly: `format`, `rawOrLabel`, or `rawOrLabelHeaders` outside their value sets, a `csvDelimiter` or `decimalCharacter` outside its set, or a non-numeric `arm` → 400 `invalid_request`; unknown parameter names stay accepted and ignored (REQ-API-017). A caller with no exportable arm — none by default, or any named arm `export_none` — gets 403 `forbidden`; a rejected call writes no audit entry (REQ-AUD-004).

The response is streamed (REQ-TECH-011). Record selection follows the data-access-group rule (REQ-API-092). Sensitivity follows the acting user's export level **for the (instrument, event) pair of each exported column** (the REQ-API-026 ladder — `export_full` → full dataset; `export_no_identifiers` → identifier fields removed; `export_de_identified` → de-identified per §3.6.1; a pair at `export_none` contributes no columns). The single applied level of the old per-arm model is gone: an export spanning several arms or instruments applies several transformations side by side, one per column, and a stricter pair never weakens a more permissive one (REQ-EXP-003). Columns that belong to no pair — the record identifier (GD-8), `redcap_event_name`, repeat markers — follow the **most protective** export level among the pairs actually in scope, so nothing is delivered less protected than the weakest part of the request (D-4 revised). A request whose scope leaves no permitted pair is 403 `forbidden`. The audit event and the UI badge record that most protective level, plus the count of pairs dropped for want of a level — never a higher level some individual pair would have permitted.

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
                 "event_type": "record_updated", "target_record": "MOBA001",
                 "details": { "action": "update", "…": "…" } } ],
  "next_cursor": null }
```

`target_record` names the record of a record-scoped event (`Audit_Logging_Design.md` §6.4) and is `null` for every other event; the Audits page shows it in its Record column (`User_Interface_Design.md` §5.6).

Entries MAY carry the `token` column and payload values — the trail is the sole sanctioned carrier (REQ-AUD-007, `Audit_Logging_Design.md` §2). The endpoint is read-only: no endpoint exists that writes, updates, or deletes audit data (REQ-DB-024, REQ-API-077).

### 4.16 Record history (REQ-API-079…081, REQ-API-156)

**`GET /api/v1/projects/{id}/records/{record}/history`** — data access ≥ `read_only` on the record's arm (GD-2) + project visibility (REQ-API-007) + record visibility (REQ-AUTH-045). Query parameters: `instrument`, `event`, `field` (filters, REQ-API-080 — `field` naming a `checkbox` matches the changes of every one of its choice columns, which is where its values are stored, REQ-DB-042), `order` (`chrono` default | `newest`, REQ-API-137 — see below) and `limit`/`cursor` per §1. Chronological, covering all changes of the record since creation, transparent across the yearly rollover (REQ-AUD-006). 200:

```json
{ "entries": [ { "created_at": "2026-09-18 14:02:11", "user_id": 3, "user_display_name": "User",
                 "action": "update", "instrument": "intake", "event": "baseline_arm_1", "instance": 1,
                 "fields": [ { "field": "age", "old": "41", "new": "42" } ] } ],
  "next_cursor": null }
```

`instance` names the form instance the change belongs to — 1 for every form that does not repeat, read as 1 from an entry that names none — and `?instance=n` narrows the history to one instance (REQ-API-156; not a positive integer → 400).

`action` ∈ `create | update | delete`; for `create`, `old` is `null`; for `delete`, `new` is `null` and the `old` values are the deleted values (REQ-AUD-009). The endpoint is read-only with respect to the audit trail (REQ-AUD-002). The data entry form presents this per-field history — who entered or changed the value, when, and the old → new values — fetched from this endpoint (REQ-API-081; User_Interface plan, data entry form).

`order=newest` serves the same entries in reverse chronological order (`created_at DESC, id DESC`) and the cursor pages backwards (`REQ-API-137`, DEV-API-23): each page's `next_cursor` continues from its last (oldest) row with a strictly-earlier condition. The filters apply within the page exactly as in `chrono`, so paging semantics are unchanged. This is the read direction the data entry form uses to derive current values — walking backwards it stops per field at the first entry seen (`User_Interface_Design.md` §8.3, closing its §11 open item 3). Any other `order` value → 400 `invalid_request`.

### 4.17 Survey links (GD-9, REQ-API-082…085, REQ-API-145/146)

All three endpoints address the one link of a **(record, instrument, event)** triple — named by the path plus `?event=` — and share its gate: data access ≥ `view_edit` **on that pair** (REQ-AUTH-069) plus record visibility (REQ-AUTH-045). The instrument MUST be survey-marked (`is_survey = 1`) and mapped to the event, otherwise 400 `invalid_request`.

**`GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link?event={unique_event_name}`** — reports that link and its state; it never mints a token (REQ-API-082). 200:

```json
{ "state": "live", "url": "https://csms.example.org/s/8f2b1c9e-4a7d-4f6a-9c3e-1d0b5a2f6e83",
  "event": "baseline_arm_1", "collected_at": null }
```

`state` is `none` (nothing issued for the pair), `live`, `submitted`, or `revoked`. `url` carries a value only while `live`: handing out a spent token's URL would only send a respondent to a 410. An instrument mapped to three events has three such links (`Database_Schema_Design.md` §8). With `state = submitted`, `collected_at` is when the response arrived (REQ-DB-041) — and that stamp is also what spent the link (§3.10, REQ-API-145), so the record view can say *submitted on …* without probing stored values. Reporting writes no audit entry; issuance and revocation do (REQ-API-043).

**`POST /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link?event={unique_event_name}`** — issues a fresh token for the pair and answers the object above with `state: live` (REQ-API-146). It replaces any live link of that pair, whose URL then admits nothing from its next call: "get another link" means the previous one is no longer the way in. **Refused 409 `conflict`** while the instrument still holds stored values for that record at that event — a second fill starts from nothing, so the collected answers go first through the delete action of §3.8 (REQ-API-036, REQ-UI-027), and the response says so rather than leaving the caller to guess. That check counts the instrument's fields **except the GD-8 record identifier**, which every record stores and whose presence would otherwise refuse every re-issue. Audit `survey_link_issued`.

**`DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link?event={unique_event_name}`** — revokes the link token **of that pair**; the links of the same instrument at other events stay valid (REQ-API-082). The revocation takes effect immediately (REQ-AUTH-040). 204. Audit `survey_link_revoked`.

The data-API behaviour of a link token is fixed by §3.10 (render and fill that (record, instrument, event) only, once); the public survey page is served by the PHP application — the browser MUST NOT call `/api/v1/*` from it (GD-1, REQ-API-084).

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
| change a field label/description, field note, section header; reorder fields/instruments/events (GD-8 invariant enforced as ever) | non-breaking — a field is `reordered` when its rank among the fields that stay in its instrument changes, so the fields an add, a delete or a move merely shifts are not listed |
| move a field to another instrument or place (REQ-API-157; diff kind `field_moved`, object `<from>.<field> → <to>.<field>`) | non-breaking — values stay keyed by field name and event; what the target instrument does not show stays stored, hidden until the field moves back (REQ-DB-049) |
| rename a field (stored values renamed in the same transaction, REQ-VAL-014) | non-breaking — values stay accessible under the new name |
| rename an instrument (REQ-API-130) | non-breaking — values are keyed by field name and event, not instrument name |
| turn repeating on or off for a mapped pair (REQ-API-154; diff kinds `repeating_on` / `repeating_off`) | non-breaking — a first instance is stored where it always was; further instances stay stored, hidden while the pair does not repeat (REQ-DB-048) |
| add options to an existing dropdown/radio/matrix/checkbox | non-breaking |
| delete a field | **breaking** — its stored values are removed (DEV-API-5) |
| change a field's type, or its validation type beyond the current values | **breaking** — stored values may no longer satisfy the new rules |
| remove a choice option that stored values use, or re-code existing options | **breaking** — stored codes lose their label/meaning (removing an unused option is non-breaking) |
| delete an instrument or event that holds data; delete an arm with events or data (DEV-API-6) | **breaking** — the keyed values become inaccessible. An instrument holds data when a field that goes with it holds a value — a field the same set moves elsewhere does not go — or a further instance is stored under its name (REQ-DB-049) |
| unmap an instrument–event pair whose records **do** hold values | non-breaking, no warning — nothing is deleted: the values stay in `data` and are reachable again once the pair is mapped back, so the change is reversible by construction (master spec "Unmap is misclassified"; retention rule §4.12) |

### 4.22 System settings (REQ-API-112, REQ-API-113)

Both endpoints require `is_admin`; a call by a non-admin is rejected (403 `forbidden`). The values live in `system_settings` (`Database_Schema_Design.md` §8, REQ-DB-037); the rate limiter reads them per request (§3.9), so an applied change takes effect on the next request without a restart.

| Endpoint | Behavior |
|---|------|
| `GET /api/v1/settings` | 200 — `{ "rate_limit_enabled": false, "rate_limit_rpm": 600, "rate_limit_block_minutes": 10 }` (the effective values: the stored row when present, the seeded default otherwise) |
| `PUT /api/v1/settings` | Body: any subset of the fields (idempotent, REQ-API-042). Validation: `rate_limit_enabled` a boolean; `rate_limit_rpm` an integer ≥ 1; `rate_limit_block_minutes` an integer 1–1440 — else 400 `bad_request`; unknown attributes → 400. The upper bound keeps a mistyped value from locking every caller out for days. 200 — the full settings object after the update. Audit `settings_updated` with the old and new value of each changed key (REQ-AUD-027); a PUT that changes nothing writes no entry |

### 4.23 Audit log export (REQ-API-158)

**`GET /api/v1/audit/export`** — `is_admin` only; any other acting user gets the uniform 403 (REQ-API-078). Query parameters are the §4.15 filters (`type`, `project`, `user`, `event_type`, `from`, `to`) plus:

| Parameter | Meaning |
|---|------|
| `estimate` | `1` — answer the JSON estimate instead of the file (below) |

There is no `limit`/`cursor`: an export covers **every** entry the filters match, never just one page (REQ-AUD-031); filter values validate exactly as for the listing (else 400 `invalid_request`).

Default response — `application/zip`, streamed: a ZIP written straight to the response (`archive/zip` over the `ResponseWriter`; non-seekable output, so entries carry data descriptors) holding one deflate-compressed CSV member named `audit_events.csv` or `audit_record_views.csv` by `type`, with `Content-Disposition: attachment; filename="audit_<type>_<YYYY-MM-DD>_<HHMM>.zip"` (UTC, the name sanitized of quotes and path characters as the dictionary ZIP's is) and `Cache-Control: no-store`. Entries are written **chronologically** (`created_at, id` ascending — an archive reads front to back, unlike the reverse-chronological listing), one row per entry behind a header row:

- events: `id, created_at, source, user_id, email, token, project_id, arm_num, role, target_record, event_type, details` — the full fixed-column set plus the payload as stored JSON text (`Audit_Logging_Design.md` §2);
- views: `id, created_at, user_id, email, token, project_id, record_ids, instruments`.

Every cell passes CSV formula neutralization (REQ-VAL-032) as record exports do; the response carries no BOM (REQ-EXP-016). One row in hand at a time — the log is never materialized (REQ-TECH-011).

`estimate=1` → 200 `{ "rows": 48213, "estimated_bytes": 15728640 }`: `rows` is the exact count of matching entries; `estimated_bytes` = rows × the average row byte length sampled from at most the newest 1000 matching rows — a bounded, advisory figure for the > 1 GiB confirmation (REQ-AUD-032), never a promise.

Every export that ends writes one `audit_export` entry naming the filters applied, the rows delivered, and `truncated: true` when the stream broke off before the last row (`Audit_Logging_Design.md` §3.5).

### 4.24 Configuration check (REQ-API-159)

**`GET /api/v1/system/check`** — `is_admin` only (403 `forbidden` otherwise); read-only and not audited. It feeds the Control Panel's Configuration Check (`User_Interface_Design.md` §5.10, REQ-UI-061) with what only the API can see. The answer is **facts, not ratings** — which value is a problem is decided by the web layer against `INSTALL.md` — and never a secret (REQ-CFG-021).

| Field | Source |
|---|------|
| `app_env`, `web_public_url`, `api_addr`, `app_timezone`, `smtp_configured`, `trusted_proxies_configured`, `require_2fa` | the loaded configuration |
| `api_listen_loopback` | `API_ADDR`'s host is `localhost` or a loopback IP |
| `running_as_root` | effective user id 0; `null` where the platform has none |
| `weak_secrets` | names among `INTERNAL_SERVICE_TOKEN`, `ANON_SALT` and (MariaDB) `DB_PASSWORD` whose value is empty, the development default, contains `CHANGE_ME`, or is shorter than 16 characters |
| `bootstrap.password_configured`, `bootstrap.password_unchanged` | `ADMIN_BOOTSTRAP_PASSWORD` set; the `ADMIN_BOOTSTRAP_EMAIL` account's stored hash still verifies against it (one argon2id verification) — `null` when not set or no such account |
| `host.clock_synchronized` | Linux `adjtimex(2)` read without mode bits: `TIME_ERROR` = unsynchronized; `null` elsewhere |
| `host.selinux` | `/sys/fs/selinux/enforce` (`enforcing`/`permissive`; absent = `disabled`); `null` off Linux or unreadable |
| `database.connection`, `version`, `schema_version` | the dialect; `VERSION()` / `sqlite_version()`; the migration table |
| `database.character_set_server`, `collation_server`, `time_zone`, `system_time_zone`, `max_allowed_packet`, `bind_address` | MariaDB global variables (`bind_address` `""` when unset — every interface) |
| `database.database_character_set`, `database_collation` | `information_schema.SCHEMATA` for `DATABASE()` |
| `database.grants` | `CURRENT_USER()`'s rows in `information_schema.USER_PRIVILEGES`, `SCHEMA_PRIVILEGES` and `TABLE_PRIVILEGES` combined: `audit_writable` (audit tables with `UPDATE`, `DELETE` or `DROP`), `tables_missing_write` (other base tables lacking `UPDATE` or `DELETE` — a table a migration added after the last `clara-db-grants runtime`), `drop` (schema- or global-level `DROP`). Roles are not followed |
| `database.audit_partitions` | partition names per audit table from `information_schema.PARTITIONS` (`Audit_Logging_Design.md` §6.1) |

On SQLite only `connection`, `version` and `schema_version` are filled; every MariaDB field is `null`, as is any MariaDB value the account cannot read. A database that cannot be read at all answers 500 `internal`.

### 4.25 Performance metrics (REQ-API-163)

**`GET /api/v1/system/metrics`** — `is_admin` only (403 `forbidden` otherwise); read-only and not audited, like §4.24. The API's own performance snapshot, in process memory: **nothing measured is persisted and all values reset on restart** — deliberate zero-config, no environment variable (the endpoint adds no network surface behind the §4.1 boundary and nginx, costs about one timestamp pair plus one mutex per operation, and its memory is bounded by fixed label vocabularies). Facts, never ratings; never a secret, user data, or SQL text.

**Measurement points** (`internal/metrics`, standard library only — no new dependency):

- An HTTP middleware mounted **outermost** on both measured surfaces: `/healthz`, `/api/`, and the whole `/api/v1/` chain — so rate-limit 429s and boundary 401/403 rejections are inside the measured latency (desired traffic accounting). The documentation endpoints stay uninstrumented. Route labels come from the server-side registered pattern only — a fixed label per mount for `/healthz` and `/api/` (`metrics.StaticRoute`), and a pure `ServeMux.Handler` lookup on the administration mux for `/api/v1/` (`httpapi.adminRoute`) — else `"unmatched"`; the metrics endpoint counts itself.
- Inside the data API handler, after parameters parse: one observation per call keyed by the fixed `content` vocabulary (`project`, `event`, `metadata`, `formEventMapping`, `exportFieldNames`, `repeatingFormsEvents`, `generateNextRecordName`, `record`) — anything else lands in `"other"`, so a caller string never mints a series.
- A transparent `database/sql` connector wrapper (`sql.OpenDB` around the driver's own connector): per statement by **verb** (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `COMMIT`, `ROLLBACK`, `OTHER` — from the leading word only; the query text is never retained, and neither parameters nor row values are ever observed), rows read (counted through `Next`, recorded once at exhaustion or close) or written (`RowsAffected`), duration, error flag; per connection dial duration/count. Statements inside transactions flow through the same path; commit/rollback timings surface SQLite fsync cost.
- Process numbers from `runtime.ReadMemStats` and `sql.DB.Stats()`, read when the snapshot is taken — not per operation.

**Labels are closed sets by construction**: server-side route templates, the content vocabulary above, the verb enum, and an HTTP-method allow-list (anything else → `OTHER`). No id, path value, token or query text ever becomes a label; cardinality cannot grow beyond the server's own registrations.

**Latency histograms** use fixed cumulative buckets in ms; percentiles are Prometheus-style linear interpolations **inside** the bucket — estimates, with the top open bucket clamped to its lower bound:

| Surface | Bucket upper bounds (ms) |
|---|---|
| HTTP | 0.5, 1, 2.5, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 400, 500, 750, 1000, 2500, 5000, 10000, 30000 |
| DB statement | 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 500, 1000, 3000, 10000 |

The HTTP grid brackets the 300 ms p95 admin-read target (REQ-TECH-010) on the §7 reference hardware; the DB grid spans sub-100 µs SQLite point reads to multi-second export queries and covers the `busy_timeout(5000)` wait.

**Response shape.** Schema rule: fields may be added; existing field names and units never change; unknown verbs or content values appear as data, never as new fields. Timestamps are canonical UTC (REQ-DB-005).

| Field | Unit / source |
|---|---|
| `generated_at`, `uptime_seconds` | snapshot time; since process start |
| `process.goroutines` | `runtime.NumGoroutine` |
| `process.heap_alloc_bytes`, `heap_sys_bytes`, `total_alloc_bytes`, `sys_bytes` | `runtime.MemStats` (`HeapAlloc` = heap in use) |
| `process.num_gc`, `gc_pause_total_ms`, `gc_pause_last_ms`, `gc_pause_p50_ms`, `gc_pause_p95_ms`, `gc_pause_max_ms` | `runtime.MemStats` GC pause ring (last ≤256 pauses; percentiles are estimates) |
| `http.requests_total`, `http.routes[]` | middleware totals; routes sorted by label |
| `routes[].route` | server pattern + method, e.g. `"GET /api/v1/projects/{id}"`, or `"unmatched"` |
| `routes[].count`, `status_2xx`…`status_5xx` | request count per status class |
| `routes[].sum_ms`, `mean_ms`, `p50_ms`, `p95_ms`, `p99_ms`, `buckets_le_ms[]` | latency histogram (`{"le_ms": …, "count": …}`, cumulative, last bucket `"le_ms": "+Inf"`) |
| `routes[].req_bytes_total`, `resp_bytes_total` | payload bytes (request body length; response body as written) |
| `data_api.calls[]` | per content value: `content`, `count`, `sum_ms`, `mean_ms`, percentiles, `buckets_le_ms[]` |
| `db.statements[]` | per verb: `verb`, `count`, `errors`, `rows_total` (sum of rows the drivers reported — queries count rows read, writes `RowsAffected`; where a driver cannot answer, nothing is added), durations as above |
| `db.connections_dialed_total`, `dial_sum_ms`, `dial_errors` | connector wrapper dials |
| `db.pool.*` | `sql.DBStats`: `max_open`, `open`, `in_use`, `idle`, `wait_count`, `wait_ms`, `max_idle_closed` — pool starvation reads as a rising `wait_count` next to `in_use == max_open` |

**Sufficiency** (REQ-API-163's evaluation goal): per-route p95 against REQ-TECH-010/011, DB verb totals and durations against "is the database the bottleneck", pool waits against CPU/memory headroom (`process.*`) — enough to decide whether an infrastructure or database extension is warranted, from one JSON document.

### 4.26 Rate-limit blocks (REQ-API-164)

**`GET /api/v1/system/blocked-ips`** — `is_admin` only (403 `forbidden` otherwise); read-only and not audited, like §4.24/§4.25. The live state of the per-source-IP rate limiter (§3.9, REQ-API-038/113): every source IP currently inside its blockout with the moment its block expires, plus the effective enable flag and both thresholds (read from the system settings like every other request, REQ-API-112). The answer comes from the one shared limiter instance at request time — nothing is persisted and blocks reset on restart like the limiter itself; expired entries never surface even when traffic has stopped and the periodic sweep inside `Allow` has not run.

Unlike §4.25's never-user-data snapshot, **this payload carries source IP addresses — personal data** — which is exactly why it sits behind the same `is_admin` boundary as the limiter's settings (REQ-API-007) and is consumed only by the Control Panel's Monitoring section (REQ-UI-064).

| Field | Unit / source |
|---|------|
| `generated_at` | snapshot time, canonical UTC (REQ-DB-005) — clients align ticks to it rather than their own clock |
| `enabled`, `rpm`, `block_minutes` | the effective settings (`RateLimitSettings`) so a reader can tell "nothing blocked" from "limiter off" |
| `blocked[]` | one entry per blocked source IP, sorted by address: `ip`, `blocked_until` (canonical UTC), `seconds_remaining` (whole seconds, rounded up) |

The Monitoring page polls this endpoint once per second next to `/system/metrics`; both calls ride the administration chain and count against the limiter like any other request — with a low `rate_limit_rpm` an open Monitoring page can spend the budget of PHP's source address and show itself blocked. That is honest behaviour, not a defect: the blockout exists precisely to refuse over-frequent callers.

### 4.27 Cross-project transfer (REQ-API-165…168)

**`POST /api/v1/projects/{id}/records/{record}/transfers`** — executes one `transfer` field (REQ-VAL-046) of the addressed source record. Query parameters: `event`, `instrument` (the field's context — whose stored values are read) and `field` (the transfer field name); `instrument` and `field` are required, else 400 `invalid_request`. The routes mount on the administration API because one call must authorize the **acting account** against a second project — a data-API token resolves to exactly one project, and two token-scoped calls could not share one transaction (DEV-API-31); the PHP layer proxies the data-entry button here with the member's identity (`User_Interface_Design.md` §8.2), the browser never calls them directly (REQ-UI-002).

Authorization runs on both sides: **source** — project membership + record visibility under DAG rules, a record outside the caller's scope answers the uniform 403 (REQ-AUTH-045); **target** — data access ≥ `view_edit` at every written target (instrument, event) pair (the identifier rows are value writes like any other) and visibility of an existing target record under DAG rules; a missing or forbidden target — unknown project, no membership, a missing pair right, a foreign-group record — answers the same uniform 403 and never discloses whether the target exists (REQ-API-165, REQ-API-007).

Behavior — **one transaction across all affected rows** (ASM-API-6): record creation, value copies, target-side calculated recomputation and both projects' audit entries commit or roll back together; a failed transfer writes nothing. The stored instructions (grammar normative in `Data_Validation_Design.md` §12) are re-parsed and re-resolved against the live design at execution time — every reference is checked again, because designs and memberships move. The button copies the record's **saved first-instance values** at the button's event, not a browser draft. For `create_record`, the operation names the target record by its source value (trimmed, non-empty, ≤ 255 characters, no control characters — else 400 `validation_error`); if no target record of that name exists it is created (REQ-API-031's identifier rules apply to the target project) and joins the actor's active DAG group on the target, or none (REQ-API-093); an existing record keeps its group. Each `copy` value passes the **target dictionary's validation exactly as an import would** (REQ-VAL-004; date/datetime values canonicalize with `APP_TIMEZONE` — no browser zone rides a transfer); **an empty source value skips its copy operation and never clears a stored target value** — a transfer copies, it does not own the target row (the deliberate difference from import's empty-clears, REQ-VAL-024). The target project's calculated fields recompute in the same transaction (REQ-VAL-037). Re-executing transfers to the same record updates in place: unchanged values produce no target-side data-change entries — a no-op click costs no audit churn; the source project's `transfer_executed` entry is written either way. Further instances of repeating instruments are out of scope in v1: copies address first-instance slots.

For `create_project`, the source value supplies the new project's name; a named template supplies its arms, events, instruments, fields and instrument/event mappings, and an instruction naming none creates an **empty** project — no arms, events, instruments or fields (REQ-VAL-047). The actor receives a role-less membership (therefore project-admin permissions) and is the project's only member. `projects` carries no creator column, so the `project_created` entry written against the new project under the acting user's id and email is the record of who raised it, its details naming `"template_project"` only when a template was cloned. If supplied, the third and fourth instruction arguments name the source fields that receive the generated membership token and new project ID — positions available only behind a template. Existing project names cause no project, membership or source-record change, and no token/ID writeback, but the attempted transfer is still audited. The project, membership, writebacks and audit entries are atomic; the project token is never included in audit details (REQ-VAL-047, REQ-API-168).

200 — what the button did, never the values it carried (REQ-API-165):

```json
{ "target_project": "followup", "target_record": "MOBA042", "created": true,
  "copied": ["age", "sex"], "skipped_empty": ["notes"] }
```

Errors: uniform 403 `forbidden` for every source- or target-side denial (REQ-API-007 — executing never probes which projects exist or what records they hold); 400 `validation_error` with the detail the designer needs when the stored instructions cannot execute against the current design or the target dictionary rejects a value; 401 session errors as elsewhere in §4. Audit: one `transfer_executed` entry on the **source** project; the target's writes appear there as standard `record_created`/`record_updated` (or none on a no-op) carrying a `"transfer"` origin reference, recomputations as `calculated_recomputed`; all entries ride the operation's transaction (REQ-AUD-034, REQ-AUD-003, `Audit_Logging_Design.md` §3.2).

**`GET /api/v1/projects/{id}/records/{record}/transfers?instrument=…`** — the availability probe (REQ-API-166): for every `transfer` field of the addressed instrument, whether this acting user may run it here now — the stored instructions parse, their target references resolve against the current design, and the user holds the edit rights REQ-API-165 demands at every written pair; `create_project` is enabled when its template resolves or it names none (an empty-project button needs nothing beyond the source record). It exists so the data-entry form renders each button or leaves it absent in one call (REQ-UI-065). `event` is accepted for symmetry with the POST and ignored — availability belongs to the target design and the actor, not the event context; the executing call validates it. The probe **fails closed**: any failure reads as `"enabled": false`, no reason is returned, nothing is written, and no target detail beyond availability is disclosed. The source-side gate is identical to the POST (membership + record visibility, uniform 403).

200:

```json
{ "transfers": [ { "field_name": "send_to_followup", "enabled": true } ] }
```

### 4.28 Record read (REQ-API-167)

**`POST /api/v1/projects/{id}/records/{record}/read`** — records that the acting account opened the record in the web interface, as one `record_read` entry (`Audit_Logging_Design.md` §3.9, REQ-AUD-035). PHP calls it while rendering `GET /projects/{id}/records/{record}` and sends the form only once this call succeeded — the browser never does (REQ-UI-002). Query parameters `event`, `instrument`, `instance` name what is about to be displayed; they are recorded, not resolved against the design, and checked only for shape (≤ 255 characters without control characters; `instance` a positive integer, else 400 `invalid_request`), so a stale selection still records the read instead of losing it. Body empty or `{}` — any attribute is 400 `invalid_request`. → 200 `{ "ok": true }`.

The gate is the record history read's own (§4.16): data access ≥ `read_only` on the record's arm (GD-2) + project visibility (REQ-API-007) + record visibility under the data access group rule (REQ-AUTH-045). Every denial answers the uniform error of §4.2 and writes nothing; an unknown project or record is 404 with nothing written. PHP therefore renders its error state where this call fails for any reason — **no entry means no values displayed**, the order `token_viewed` keeps for a token value (REQ-API-162, REQ-AUD-033). A record that does not exist yet discloses nothing, so PHP does not call this endpoint for the new-record form (REQ-AUD-004).

The endpoint writes one entry per call and returns no data: it discloses nothing a caller of §4.16 did not already hold, and it is the sole write path for REQ-AUD-035. It belongs on the administration surface because its actor is the acting account — `source=ui`, no token (REQ-AUD-018) — while record pulls keep keying `audit_record_views` by their token (§4.15, REQ-AUD-013, DEV-AUD-6).

### 4.29 Project interchange — CDISC ODM XML (REQ-API-169…170)

**`GET /api/v1/projects/{id}/export/odm`** — the project as one CDISC ODM document: a `Study` (named through `GlobalVariables` under 1.3 and through its own attributes under 2.0) with one `MetaDataVersion` describing the current structure, and `ClinicalData` for the records in scope — each dialect spelled as its own schema defines it (`ODM_Interchange_Design.md` §2–§4, REQ-ODM-015). Unknown query parameters are accepted and ignored except for the supported version and content selectors (REQ-API-017). Gates on `is_admin` — not `project_admin`, since a whole-study file is bulk data movement (REQ-ODM-002); any other actor gets the uniform 403 of §4.2 and nothing is written (REQ-AUD-004).

| Parameter | Values | Default | Meaning |
|---|---|---|---|
| `odm_version` | `1.3`, `2.0` | `1.3` | Selects namespace and `ODMVersion`; any other value answers 400 (REQ-ODM-064). |
| `odm_content` | `project_and_data`, `project_only` | `project_and_data` | Whether the document carries its records. `project_only` writes the `Study` and its `MetaDataVersion` and no `ClinicalData` element at all, reads no record, and so never answers 403 for want of a readable one; any other value answers 400 (REQ-ODM-066). |

→ 200 `application/xml; charset=utf-8`, `Content-Disposition: attachment; filename="<slug>_odm_<YYYY-MM-DD>.xml"` for 1.3 or `"<slug>_odm_v2.0_<YYYY-MM-DD>.xml"` for 2.0 (the dictionary export's filename sanitizer, REQ-API-153), streamed with metadata first and one `<SubjectData>` per record as it is produced (REQ-TECH-011, O-2). No byte-order mark (REQ-EXP-016). A store failure after the first byte truncates the stream, as CSV does — no synthesized closing element, so a partial file cannot pass as complete.

Values arrive through the same pipeline `format=csv|json` uses (`dataapi.RunExportRows`, §4.14's transformation and per-pair levels included), so an ODM export at a de-identified rank holds the same hashes and shifted dates the CSV holds (REQ-ODM-021, D-8 of `Data_Export_Anonymization_Design.md`). Where that pipeline removes the record identifier column (below `export_full`, REQ-EXP-020), ODM still needs a key and gets a per-file surrogate with `X-CLARA-ODM-Key: surrogate` naming it (§4.1). Audit: `odm_export` (§3.5 of the audit design).

**`POST /api/v1/projects/import/odm`** — creates a new project from an ODM document. Body: the XML itself (`application/xml`; not base64 in JSON — an XML upload needs no envelope, unlike REQ-API-152's ZIP payloads), bounded by the 32 MiB import limit with the same 400 (REQ-API-144, REQ-ODM-014). Gates on `is_admin`.

| Parameter | Values | Default | Meaning |
|---|---|---|---|
| `project_name` | any valid project name | `StudyName` | the new project's name; a collision → 409 `conflict`, never a rename of the existing project (REQ-ODM-044) |
| `dry_run` | `1` | off | parse, resolve and validate the whole document, answer the report, write nothing (REQ-ODM-046) |

→ 201 (live) / 200 (`dry_run`) with `{project_id, project_name, dry_run, arms, events, instruments, fields, records_added, records_updated, records_failed, problems[]}`, each problem `{file,row,field,code,message}` with the codes of `ODM_Interchange_Design.md` §7 — the instrument import's shape (REQ-ODM-060). The importer detects 1.3 or 2.0 from the root namespace; 400 for a body outside those dialects (`not_odm`), carrying a `DOCTYPE` (`dtd_forbidden`), breaking a structural bound (`too_deep`, `too_large`) or contradicting itself (`duplicate_oid`, `dangling_ref`, `no_metadataversion`); the problem list says which, and nothing is written (REQ-ODM-054). Structure builds in one transaction; records load one transaction per record after it, each value validated by `validate` as on any other write path (REQ-ODM-040/041/042, O-6/O-7). Audit: `odm_import`, with counts and options only — never a value or document text (REQ-AUD-014).

Neither route is called directly by the browser: the administrator-only PHP UI proxies the export and import through the internal service-token boundary (REQ-ODM-005, DEV-ODM-6, REQ-TECH-018).

## 5. Permission summary

The normative endpoint → permission mapping is in `API_Endpoints_Requirement.md` §4.22; it is reproduced here as an overview:

| Endpoints | Required permission |
|---|------|
| data API `/api/` | the token's levels per §3 (data/export level per arm and per (instrument, event) pair; link tokens scoped per §3.10) |
| `GET/POST /api/v1/users`, `PUT /api/v1/users/{id}` | `is_admin` (PUT also grants/revokes `is_admin`; the last enabled administrator cannot be revoked or disabled — REQ-API-136, REQ-AUTH-068) |
| `POST /api/v1/users/{id}/invite` (§4.4, REQ-API-117) | `is_admin` |
| `PUT /users/me/password` (§4.4, REQ-API-118) | any authenticated user (self, with local credential) |
| `POST /api/v1/auth/password-reset/request`, `…/complete`, `POST /api/v1/auth/invite/complete` (§4.3, REQ-API-119/120/121) | pre-authentication — service token only, no user id (DEV-API-16) |
| `POST /api/v1/auth/verify-password` (§4.3, REQ-API-123) | pre-authentication — service token only, no user id (DEV-API-17) |
| `POST /api/v1/auth/oauth/start` (§4.3, REQ-API-173) | pre-authentication — service token only, no user id (DEV-API-32) |
| `POST /api/v1/auth/oauth/test` (§4.3, REQ-API-174) | `is_admin` |
| `POST /api/v1/auth/session-replay` (§4.3, REQ-API-148) | any active user (self — the account whose session id was replayed; no session epoch) |
| `POST /api/v1/projects` | `is_admin` |
| `GET /api/v1/projects/{id}/export/odm`, `POST /api/v1/projects/import/odm` (§4.29) | `is_admin` only (REQ-ODM-002) |
| `GET /api/v1/projects` | project visibility (REQ-API-007) |
| `GET /api/v1/projects/{id}` | data access ≥ `read_only` + visibility |
| `PUT /api/v1/projects/{id}` | `project_admin` |
| `GET/PUT …/users` (members), `GET/POST …/roles`, `PUT …/roles/{rid}` | `is_admin` |
| `POST …/users/{uid}/token-viewed` (REQ-API-160) | `is_admin` |
| `GET …/users/{uid}/token` | self-service: acting user is a member (REQ-API-102) |
| `GET /users/me/projects` (REQ-API-161) | any authenticated user (self) |
| `POST /users/me/projects/{id}/token-reveal` (REQ-API-162) | self-service: acting user is a member |
| arms, events, instruments, fields, mapping — mutations (POST/PUT/DELETE) | `project_admin` |
| arms, events, instruments, fields, mapping — reads (GET) | data access ≥ `read_only` |
| `GET …/record-status`, `GET …/records/{record}/history` | data access ≥ `read_only` (+ record visibility) |
| `POST …/records/{record}/read` (§4.28, REQ-API-167) | data access ≥ `read_only` (+ record visibility) — writes one `record_read` entry, returns nothing (REQ-AUD-035) |
| `PUT …/records/{record}/events/{event}/instruments/{iid}/completion` | data access ≥ `view_edit` on the record's arm (+ record visibility) |
| `GET/POST …/records/{record}/transfers` (§4.27, REQ-API-165/166) | source: member + record visibility; target: data access ≥ `view_edit` on every written (instrument, event) pair (+ target-record DAG visibility) — both sides checked, uniform 403 |
| `POST …/fields/{fid}/test` | `project_admin` + record visibility |
| `GET /api/v1/validationTypes` | any authenticated user |
| `GET …/export` | export level per (instrument, event) pair — unpermitted pairs contribute no columns; 403 only when no permitted pair is in scope |
| survey links (issue/revoke, §4.17) | data access ≥ `view_edit` **on the pair** (+ record visibility) |
| `GET …/data-access-groups` | data access ≥ `read_only` |
| `POST/DELETE …/data-access-groups`, `PUT …/records/{record}/data-access-group` | `project_admin` |
| `PUT …/users/{uid}/data-access-groups` | `is_admin` |
| `PUT …/active-data-access-group` | an assigned member (self-service) |
| `GET /api/v1/audit` | `is_admin` (all projects) or a member of the queried project (REQ-API-078) |
| `GET /api/v1/audit/export` (§4.23, REQ-API-158) | `is_admin` only (REQ-API-078) |
| `GET /api/v1/system/check` (§4.24, REQ-API-159) | `is_admin` only |
| `GET /api/v1/system/metrics` (§4.25, REQ-API-163) | `is_admin` only |
| `GET /api/v1/system/blocked-ips` (§4.26, REQ-API-164) | `is_admin` only |
| `GET /i18n/languages`, `PUT /users/me/ui-language`, `PUT /users/me/ui-theme` (§4.19) | any authenticated user |
| `GET/PUT /i18n/strings` | `is_admin` |
| `POST …/end-provision` (§4.20) | `is_admin` (BR-009) |
| `GET …/mode` (§4.21) | data access ≥ `read_only` + visibility |
| `PUT …/mode` (§4.21) | `is_admin` — `project_admin` is rejected (403) |
| staging start/commit/discard (§4.21) | `project_admin` |
| `GET …/staging` (§4.21) | `project_admin` |
| `GET/PUT /api/v1/settings` (§4.22) | `is_admin` |

`is_admin` users hold all permission levels on every arm of every project (REQ-AUTH-023), so a permission requirement never excludes an administrator.
