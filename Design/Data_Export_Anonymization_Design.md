# Data Export and Anonymization — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Data_Export_Anonymization_Requirements.md` (REQ-EXP-…, which consolidates the distributed area requirements REQ-API-013/016/017/024…030/075/076/092, REQ-AUTH-017…019/023/045, REQ-DB-013/014/023, REQ-CFG-015/016/021/022, REQ-AUD-004/011/013/014/015, REQ-VAL-022/032, REQ-TECH-011, and charter BR-008/BR-009, success criterion 4) — source plan `Plan/Data_Export_Anonymization.md` (full traceability: §10)
**Date:** 2026-09-23

## 1. Purpose

Fixes the normative export pipeline and the anonymization semantics that the other design documents delegate to this area: what exactly changes at each export level (REQ-API-026/075, REQ-AUTH-018, success criterion 4), the field-hash and date-shift algorithms, the free-text rule, and the end-provision execution (BR-009).

The surrounding machinery is fixed elsewhere and is not repeated here:

| Concern | Fixed in |
|---|---|
| transport, row shape, `filterLogic`, error format | `API_Endpoints_Design.md` §3.1/§3.6 |
| `anon_offsets` table, offset derivation | `Database_Schema_Design.md` §8 (REQ-DB-023) |
| `ANON_SALT`, shift range, startup and redaction rules | `System_Configuration_Design.md` §3.6/§4.1/§4.4 |
| `export` event and record-view rows | `Audit_Logging_Design.md` §3.5/§4 |
| CSV formula-injection neutralization | `Data_Validation_Design.md` §5.3 |

This document is normative for the level pipeline (§4), the algorithms (§5), and the end-provision action (§7); any deviation is a requirements-level change.

## 2. Export Surfaces

Two surfaces, one pipeline (§4):

| Surface | Entry | Level source |
|---|---|---|
| data API (Fiona and any external caller) | `POST /api/` `content=record&action=export` | the token holder's export level for the arm of the exported data (REQ-API-026, GD-2) |
| administration API (web UI) | `GET /api/v1/projects/{id}/export?format=csv\\|json` (default `csv`, REQ-API-075) | the acting user's export level per arm (GD-2, REQ-API-075) |

Common to both (normative):

- the applied level is the GD-2 ordering `export_none < export_de_identified < export_no_identifiers < export_full` (REQ-AUTH-017/018); `export_none` → 403 `Permission denied`, nothing is written to either audit table (REQ-AUD-004, `API_Endpoints_Design.md` §3.2);
- multi-arm level resolution per §4.3;
- record scope per the data-access-group rule — the level governs the transformation, the group governs the records (REQ-API-092, REQ-AUTH-045);
- streaming (REQ-TECH-011);
- audit per §8.

`is_admin` users hold every level on every arm (REQ-AUTH-023) and are never blocked by the level. No implicit access (REQ-AUTH-019).

## 3. Formats and Row Shape

### 3.1 CSV and JSON (REQ-API-013, REQ-API-075, master spec \"Export formats\")

The master spec's \"CSV raw / CSV labels\" pair is the `rawOrLabel` axis on the data API (REQ-API-027) and is available on both surfaces; `format`/`returnFormat` selects the encoding (`json`/`csv`). Stored values remain choice codes (REQ-VAL-022); `rawOrLabel=label` renders dropdown/radio/matrix-row values as their labels, `rawOrLabelHeaders` renders the column names the same way (`raw`/`label`/`both`, REQ-API-027).

### 3.2 Row layout (REQ-API-028, `API_Endpoints_Design.md` §3.6.2)

- `type=flat` is normative — one row per (record, event); `type=wide` is the compatibility mode of DEV-API-1.
- Each row carries the record identifier's value under its field name (GD-8), `redcap_event_name` for projects with events (flat), and empty strings for missing values.
- `description`/`header` fields carry no values and never appear.
- Multi-valued fields: the master spec's per-choice columns (`demo_habits__1`, `demo_habits__2`) are realized by the matrix expansion of REQ-DB-014 — each matrix row is an ordinary expanded field (`<name>__<n>`) with its own column (D-6). An unselected row is an empty string (REQ-API-028); there is no separate 1/0 coding — callers that want it define the row choices as `1/0` (D-6).

### 3.3 CSV mechanics (REQ-API-029, REQ-TECH-011)

Streamed (never fully materialized; full flat export < 10 s on the reference hardware of `Technology_Stack_Design.md` §7), quoted per standard CSV rules, `csvDelimiter` honored (empty = comma), and formula-triggering leading characters neutralized per `Data_Validation_Design.md` §5.3 (REQ-VAL-032) — the stored value is unaffected, only the CSV cell.

### 3.4 Parameters accepted and ignored (REQ-API-016/017, DEV-API-2)

`exportCheckboxLabel`, `exportSurveyFields`, `exportDataAccessGroups` and any other unknown parameter are accepted and ignored, never rejected — existing callers keep working (REQ-API-017, REQ-API-037).

## 4. Sensitivity Levels and the Transformation Pipeline (normative)

### 4.1 Field categories

Given the data dictionary of `Database_Schema_Design.md` §5 (REQ-DB-013), the pipeline classifies every exportable field exactly once, in this priority order (D-1, D-3):

| Category | Definition |
|---|---|
| **direct identifier field** | the record identifier field (GD-8 — first field of the instrument at position 1) **or** a field whose `validation_type` is `email` or `MRN` (REQ-VAL-015…039) |
| **personal field** | `personal_information = 1` (REQ-DB-013) and not a direct identifier field (removal wins over hashing) |
| **free text field** | `field_type = text` (REQ-DB-013) and neither of the above; its export approval is `export_approved` (DEV-DB-2) |
| **structured field** | everything else (dropdown, radio, matrix rows, calculated, numeric, non-text) |

A field with `validation_type ∈ {date, datetime}` is **date-bearing** — an orthogonal property: a surviving date-bearing field is shifted at the `export_de_identified` level (§5.2).

Fail-safe property (D-1): an email or MRN value can never leave the system untransformed at any level below `export_full`, even if the data manager never flagged the field as personal.

### 4.2 Per-level pipeline

Applied to the selected rows/columns after filtering (§6), in this order (D-2):

| Step | `export_de_identified` | `export_no_identifiers` | `export_full` |
|---|---|---|---|
| 1. direct identifier fields | **column removed** | **column removed** | kept |
| 2. personal fields | **value → field hash** (§5.1) | kept | kept |
| 3. free text fields | **column removed** unless `export_approved = 1` | kept | kept |
| 4. date-bearing survivors | **date part shifted** by the record's persisted offset (§5.2) | kept | kept |

This realizes REQ-AUTH-018 verbatim — `export_de_identified`: \"direct identifiers removed, personal fields hashed, dates shifted\"; `export_no_identifiers`: \"all identifier fields removed\" (and nothing else) — and success criterion 4: the `export_de_identified` output contains no direct identifiers and hashed personal fields; the `export_no_identifiers` output contains the same data with all identifier fields removed. The ordering (higher level includes everything below, GD-2) is honored: each weaker level applies a strict subset of the steps.

Removed means the column is absent from the output (no empty column); hashing and shifting replace values in place.

### 4.3 Multi-arm level (normative)

For an export spanning several arms, the applied level is the **lowest level in the GD-2 ordering among the arms of the exported data** — the most protective (D-4). Consequences:

- an arm at `export_none` among the exported arms → the whole call is rejected (403), because the minimum is `export_none`;
- the `sensitivity` recorded in the audit event is exactly this applied level (`Audit_Logging_Design.md` §3.5: \"the least restrictive level applied\" — read as the minimum of the set, i.e. the one actually applied to every row);
- the UI sensitivity badge shows the same applied level (REQ-UI-020).

A caller with different levels on different arms therefore obtains the de-identified form of the union; per-arm higher sensitivity is obtained by separate exports per arm.

### 4.4 What the pipeline does not touch

- **Stored values are unchanged** by an export — the pipeline transforms the output only. The in-place exception is the end-provision `anonymize` action (§7.4).
- **Branching logic never affects exports** (GD-13: display-only; `API_Endpoints_Design.md` §3.6.3: `filterLogic` \"never changes the sensitivity level\").
- **Audit carries no values** (REQ-AUD-014): the `export` event and the record-view row record only the applied level, the filters, and the records/instruments accessed — never the exported or transformed values. `ANON_SALT` is a redacted secret and never appears in logs or audit details (REQ-CFG-021/022, `System_Configuration_Design.md` §4.4).

## 5. Anonymization Algorithms (normative)

### 5.1 Field hash (D-5)

```
hash(project, field, value) = HEX( SHA-256( ANON_SALT ‖ ':' ‖ project_id ‖ ':' ‖ field_name ‖ ':' ‖ value ) )
```

- Full 64-character hex digest — no truncation (no collision risk; the hash stays joinable).
- Deterministic: the same (project, field, value) always yields the same hash — two exports of the same record agree, and cohort/group analysis on the exported data remains possible (the plan's consistency intent, the same purpose as the persisted date offset).
- Salted and scoped: `ANON_SALT` is a production-required redacted secret (REQ-CFG-015, `System_Configuration_Design.md` §3.6/§4.1/§4.4) — the hash is not reversible without the salt and is not linkable across projects or fields. Changing the salt invalidates linkage to earlier exports (documented consequence, not an error).
- Applied to the stored value; the output cell is the hex string.

### 5.2 Date shift

Fixed by `Database_Schema_Design.md` §8 (normative) and `System_Configuration_Design.md` §3.6; restated for the completeness of the pipeline:

1. On the first anonymized export of a record (or by the §7.4 action), compute and persist:
   `offset_days = ANON_DATE_SHIFT_MIN + SHA-256(project_id ‖ ':' ‖ record_id ‖ ':' ‖ ANON_SALT) mod (ANON_DATE_SHIFT_MAX − ANON_DATE_SHIFT_MIN + 1)` in `anon_offsets` (REQ-DB-023, DEV-DB-3).
2. Every later shift of that record uses the persisted offset — consistent per record across time and across exports (plan: \"consistent for each patient\").
3. The shift applies to the **date part** of the value only; the time part and the collection offset `±HH:MM` are preserved (GD-16, REQ-VAL-041).
4. Deterministic (salted hash), not random — a deliberate refinement of the plan's \"random number of days\" (see §11): a truly random offset would break the consistency the plan itself requires.

## 6. Filters and Record Scope

- `records[]`/`fields[]`/`forms[]`/`events[]` combine (intersection; REQ-API-024, `API_Endpoints_Design.md` §3.6.1); with no filters, all records visible to the holder are returned.
- `filterLogic` filters which **records** are returned; it never changes the sensitivity level (`API_Endpoints_Design.md` §3.6.3; grammar per `Data_Validation_Design.md` §7, REQ-API-025).
- A `fields[]` entry naming a column that the applied level removes (§4.2, steps 1/3) is silently absent from the output — the level governs the transformation, the filter governs the selection (REQ-API-092); a filter never escalates sensitivity.
- The data-access-group rule scopes the records (REQ-API-092, REQ-AUTH-045): a holder with an active group receives only that group's records; a holder without a group receives all records of the project.

## 7. End-of-Project Provision (BR-009)

### 7.1 Semantics and trigger (D-7)

The provision (delete or anonymize) is **not** system state (GD-17, REQ-DB-006/032): the operator reads it from project data (e.g. a `DataTransferProjects` instrument) or from outside the system, at the REK end date (`projects.rek_end_date`, `Database_Schema_Design.md` §4). Execution is an explicit one-shot operator action — the stack has no scheduler component (`Technology_Stack_Design.md` §4), so nothing runs automatically at the REK end date. The project summary already displays `rek_end_date` (`User_Interface_Design.md` §5.2) as the operator's cue.

### 7.2 Action (normative)

`POST /api/v1/projects/{id}/end-provision`

| Aspect | Rule |
|---|---|
| body | `{ \"provision\": \"delete\" \\| \"anonymize\" }`; any other value → 400 |
| gating | `is_admin` — destroying or irrevocably anonymizing clinical data exceeds `project_admin` (\"modify project structure and metadata\", REQ-AUTH-018); 403 otherwise |
| one-shot | a second execution for the same project → 409; the idempotency state is the `project_ended` audit event of §8 |
| atomicity | the audit row and the data change are written by the same-transaction writer (`Technology_Stack_Design.md` §4) — no partial state on failure |
| result | 200 with `{ \"provision\", \"records_affected\", \"values_affected\" }` |

### 7.3 `provision=delete`

Removes the project's stored record data: all EAV rows of the `data` table, `record_entities`, `survey_links`, and `anon_offsets` — every data-plane table keyed by `project_id` (`Database_Schema_Design.md` §6–§8). Kept: project metadata, structure (arms, events, instruments, fields, mapping), roles/memberships/tokens, and the audit trail — the project remains administrable and the action itself is auditable.

### 7.4 `provision=anonymize` (in-place)

Applies the §4.2 `export_de_identified` pipeline **in place** to the stored values, in one transaction:

1. values of direct identifier fields (§4.1) → cleared;
2. values of personal fields → replaced by the §5.1 hash;
3. values of free text fields with `export_approved = 0` → cleared (approved ones kept);
4. date-bearing values → shifted by the §5.2 offset (offsets computed and persisted first).

Afterwards, every read and export of that project — including `export_full` — returns the anonymized form. Irreversible; both provision branches close the action (one-shot, §7.2).

## 8. Audit Integration (REQ-AUD-011/013…015, BR-008)

- Every export on either surface → an `export` event in `audit_events`: acting user/token, project, the **applied** sensitivity level (§4.3), and the filters supplied — the shape of `Audit_Logging_Design.md` §3.5.
- Every data-API invocation additionally → a record-view row (`audit_record_views`, `Audit_Logging_Design.md` §4) regardless of initiator (external caller such as Fiona, or the PHP layer; REQ-AUD-013, ASM-API-3/ASM-AUD-2).
- Rejected invocations (invalid token, `export_none`, no visibility) write no `export` event and no record-view row (REQ-AUD-004); security-relevant rejections are logged per `Audit_Logging_Design.md` §3.1.
- Reads that return no record values write no record-view row (REQ-AUD-015).
- The `end-provision` action (§7.2) → a `project_ended` event (details: `{\"provision\":\"delete\"|\"anonymize\",\"records_affected\":…,\"values_affected\":…}`) — a new event type to register in the `Audit_Logging_Design.md` §3 catalog (§12).

## 9. Configuration

No new variables — the canonical inventory is `System_Configuration_Design.md` §3 (REQ-CFG-003):

| Variable | Role here | Default |
|---|---|---|
| `ANON_SALT` | hash salt (§5.1) and offset derivation (§5.2); production-required (REQ-CFG-015; startup matrix `System_Configuration_Design.md` §4.1); redacted secret (REQ-CFG-021) | dev-only default |
| `ANON_DATE_SHIFT_MIN` / `ANON_DATE_SHIFT_MAX` | offset range (§5.2; REQ-CFG-016) | `0` / `364` |

## 10. Design Decisions and Traceability

### 10.1 Local design decisions

| ID | Decision |
|---|---|
| D-1 | field categories with the fail-safe property (§4.1): the record identifier (GD-8) and every `email`/`MRN` validation-type field is a direct identifier — removed at both non-full levels, flagged as personal or not |
| D-2 | the fixed four-step pipeline (§4.2); \"removed\" = the column is absent from the output; personal fields are hashed, not removed |
| D-3 | free text = `field_type = text`; gated by `export_approved` at `export_de_identified` (DEV-DB-2, plan \"free text … unless explicitly approved for export\") |
| D-4 | multi-arm ⇒ the lowest level in the GD-2 ordering among the exported arms; `export_none` on any of them ⇒ 403 (§4.3) |
| D-5 | the field hash is the full 64-character SHA-256 hex over salt, project, field, value — deterministic, untruncated, salted (§5.1) |
| D-6 | the master spec's per-choice columns are realized by the matrix expansion of REQ-DB-014; no 1/0 coding unless the row choices are defined as 1/0 (§3.2) |
| D-7 | the end provision is an explicit one-shot `is_admin` action, not a scheduled job (§7) |

### 10.2 Requirement traceability

| Requirement / decision | Fixed here |
|---|---|
| `Requirements/Data_Export_Anonymization_Requirements.md` (REQ-EXP-001…052) | §2–§9 |
| REQ-API-013/016/017 (encodings, ignored parameters) | §3.1, §3.4 |
| REQ-API-024/025 (filters, `filterLogic`) | §6 |
| REQ-API-026/075/076 (sensitivity per level, both surfaces) | §4 |
| REQ-API-027/028 (raw/label, row shape) | §3.1, §3.2 |
| REQ-API-029 (CSV streaming, delimiter, formula injection) | §3.3 |
| REQ-API-030 (record-view audit on every pull) | §8 |
| REQ-API-092 (DAG scope vs. level) | §4.3, §6 |
| REQ-AUTH-017/018 (export levels, semantics) | §4.2, §4.3 |
| REQ-AUTH-019 (no implicit access) | §2 |
| REQ-AUTH-023 (administrator holds all levels) | §2 |
| REQ-AUTH-045 (DAG visibility) | §6 |
| REQ-DB-013 (`personal_information`, `export_approved`) | §4.1, D-3 |
| REQ-DB-014 (matrix expansion) | §3.2, D-6 |
| REQ-DB-023 (`anon_offsets`) | §5.2 |
| REQ-CFG-015 (`ANON_SALT`) | §5.1, §9 |
| REQ-CFG-016 (shift range) | §5.2, §9 |
| REQ-CFG-021/022 (redaction) | §4.4, §5.1 |
| REQ-AUD-004 (no rows on rejection) | §8 |
| REQ-AUD-011 (export events) | §8 |
| REQ-AUD-013 (record-view rows, any initiator) | §8 |
| REQ-AUD-014 (audit carries no values) | §4.4, §8 |
| REQ-AUD-015 (structure reads write no row) | §8 |
| REQ-VAL-022 (stored choice codes) | §3.1 |
| REQ-VAL-032 (CSV formula injection) | §3.3 |
| REQ-TECH-011 (streaming, < 10 s) | §2, §3.3 |
| BR-008 (audit of exports) | §8 |
| BR-009 (end-provision execution) | §7 |
| GD-2 (permission model) | §2, §4 |
| GD-8 (record identifier field) | §4.1, D-1 |
| GD-13 (branching is display-only) | §4.4 |
| GD-16 (collection timezone) | §5.2 |
| GD-17 (provision is not project state) | §7.1 |
| DEV-DB-2 (`export_approved`) | §4.1, D-3 |
| DEV-DB-3 (`anon_offsets`) | §5.2 |
| DEV-API-1 (`type=wide`) | §3.2 |
| DEV-API-2 (ignored parameters) | §3.4 |
| charter success criterion 4 | §4.2, §5 |
| plan `Data_Export_Anonymization.md` (formats, rules, field-level anonymization, end provision) | §3, §4, §5, §7 |

## 11. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| what "de-identified" exactly means (REQ-API-026/075, REQ-AUTH-018) | the per-level pipeline: §4.2, D-1…D-5 |
| "salted hash" for personal fields (plan, "Field-Level Anonymization") | exact algorithm: §5.1 (D-5) |
| date shift "consistent for each patient" (plan; REQ-DB-023) | per-record persisted offset, reused on every export: §5.2 |
| free text "excluded unless explicitly approved" (plan; REQ-DB-013) | `export_approved` gates free text at `export_de_identified`: §4.1, D-3 |
| "end-provision execution" (charter in-scope, BR-009) | one-shot `is_admin` action, delete/anonymize, idempotent, audited: §7 |
| plan's per-instrument access levels (Full/Anonymized/None) | superseded by the GD-2 per-arm export levels; per-field control is the `personal_information`/`export_approved` flags (charter: out of scope, deviation DEV-1) |
| master-spec per-choice columns (`demo_habits__1/2`, 1/0) | matrix expansion (REQ-DB-014): §3.2, D-6 |

## 12. Open Items

| Item | Owner |
|---|---|
| register `POST /api/v1/projects/{id}/end-provision` in `API_Endpoints_Design.md` §4 and `openapi/openapi.json` (rules fixed in §7.2) | API design |
| register the `project_ended` event type in the `Audit_Logging_Design.md` §3 event catalog (rules fixed in §7.2/§8) | audit logging design |
| end-provision action card on the project screen for `is_admin` (`User_Interface_Design.md` §6) | UI design |
| the end-provision decision per project (delete vs. anonymize) — instrument data (e.g. `DataTransferProjects`) or an external record | operations / project owner |