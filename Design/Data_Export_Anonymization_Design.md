# Data Export and Anonymization — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** the export/anonymization area of the charter baseline (`Requirements/Project_Charter_Requirements.md`, BR-008/BR-009, success criterion 4) and the distributed area requirements REQ-API-013/016/017/024…030/075/076/092, REQ-AUTH-017…019/023/045, REQ-DB-013/014/023, REQ-CFG-015/016/021/022, REQ-AUD-004/011/013/014/015, REQ-VAL-022/032, REQ-TECH-011 — source plan `Plan/Data_Export_Anonymization.md` (full traceability: §10)
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
- **Audit carries no values** (REQ-AUD-014):
