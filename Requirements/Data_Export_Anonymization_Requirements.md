# Data Export and Anonymization — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source plan:** `Plan/Data_Export_Anonymization.md`, `VISION_AND_REQUIREMENTS.md` (master spec)\
**Status:** Approved for development handoff\
**Date:** 2026-09-23

## 1. Purpose

Defines the requirements for the export and anonymization area: the export surfaces and formats, the sensitivity levels and their transformation semantics, the anonymization rules (direct-identifier removal, personal-field hashing, free-text approval, date shifting), and the end-of-project provision (BR-008/BR-009, success criterion 4). This document is the home for the export/anonymization semantics that the area requirements reference by name (REQ-API-026/075, REQ-AUTH-018, REQ-DB-013/023).

The surrounding machinery is specified in the area documents this one complements, and is not restated here:

| Concern | Specified in |
|---|------|
| transport, filters, `filterLogic`, row shape, error format | `API_Endpoints_Requirements.md` (REQ-API-024…029, 075/076) |
| export levels and their ordering | `Authentication_Authorization_Requirements.md` (REQ-AUTH-017/018) |
| `personal_information`, `export_approved`, offset persistence | `Database_Schema_Requirements.md` (REQ-DB-013/023) |
| salt and shift-range configuration | `System_Configuration_Requirements.md` (REQ-CFG-015/016) |
| export events and record-view rows | `Audit_Logging_Requirements.md` (REQ-AUD-011/013) |
| streaming and performance | `Technology_Stack_Requirements.md` (REQ-TECH-011) |

`Design/Data_Export_Anonymization_Design.md` contains the normative pipeline, the field categories, and the algorithms (D-1…D-7).

## 2. Export Surfaces

| ID | Requirement |
|---|------|
| REQ-EXP-001 | Project data MUST be exportable through two surfaces: the REDCap-compatible data API (`POST /api/` `content=record&action=export`) and the administration API (`GET /api/v1/projects/{id}/export`) (BR-008, REQ-API-024…030, REQ-API-075/076). |
| REQ-EXP-002 | The applied sensitivity MUST be determined by the token holder's (data API) or acting user's (administration API) export level **per (instrument, event) pair** — each exported column carries its own transformation (GD-2, REQ-API-026/075, REQ-AUTH-017/018/069); a pair at `export_none` contributes no columns, and a call whose scope leaves no permitted pair in scope MUST be rejected with 403 (nothing written to either audit table, REQ-AUD-004). |
| REQ-EXP-003 | The export applies its transformation **per (instrument, event) pair**: an export spanning several arms or instruments delivers each column at its own pair's level, and one pair's stricter level never weakens another's; a pair the caller may not export contributes no columns (REQ-AUTH-069, REQ-API-026). Columns belonging to no pair — the record identifier (GD-8) and the `redcap_*` framing columns — MUST follow the most protective level among the pairs in scope. A weaker transformation for one instrument is obtained by exporting that pair's data on its own (D-4, revised 2026-10-03 with the move of export levels from arm to pair). |
| REQ-EXP-004 | A filter (`records[]`, `fields[]`, `forms[]`, `events[]`, `filterLogic`) MUST govern selection only and MUST NOT escalate the applied sensitivity: the level governs the transformation, the filter governs the selection (REQ-API-024/025/092). |

## 3. Formats and Row Shape

| ID | Requirement |
|---|------|
| REQ-EXP-010 | Project data MUST be exportable in two formats — CSV and JSON (BR-008, plan "Export Formats"); the raw/label axis (choice codes vs. labels) and the header variant MUST be available (REQ-API-027), and stored values MUST remain choice codes (REQ-VAL-022). |
| REQ-EXP-011 | Export rows MUST follow the REDCap-compatible row shape: the record identifier's value under its field name (GD-8), `redcap_event_name` per row for projects with events (flat), and empty strings for missing values (REQ-API-028). `type=flat` is normative; `type=wide` is a compatibility mode (DEV-API-1). |
| REQ-EXP-012 | Multi-valued (matrix) fields MUST export one column per expanded row field (REQ-DB-014); the master spec's per-choice 1/0 coding applies only where the row choices are defined as 1/0 (D-6). |
| REQ-EXP-013 | CSV exports MUST be streamed (REQ-TECH-011) and quoted per standard CSV rules: a field holding the delimiter, a double quote, or a line break is enclosed in double quotes, and an embedded double quote is doubled. Both surfaces MUST honor `csvDelimiter` with REDCap's value set — `,` (also when empty or absent), `;`, `|`, `^`, and tab, given either as the character itself or by the name `tab`; any other value MUST be rejected with 400, never reduced to its first character. Formula-triggering leading characters MUST be neutralized in every cell, header cells included — label headers carry user-authored text (REQ-API-029, REQ-VAL-032). *(Amended 2026-10-11: delimiter value set, header cells — DEV-EXP-6.)* |
| REQ-EXP-014 | The REDCap parameters `exportCheckboxLabel`, `exportSurveyFields`, and `exportDataAccessGroups`, and any other unknown parameter, MUST be accepted and ignored, never rejected — existing callers keep working (REQ-API-016/017, REQ-API-037). |
| REQ-EXP-015 | **Decimal character.** Both surfaces MUST honor REDCap's `decimalCharacter` parameter: empty or absent exports numbers in their stored form (a period, REQ-VAL-016); `.` or `,` writes that character as the decimal separator of every value of a calculated field or a `floating point`-validated text field; any other value MUST be rejected with 400. The option applies to the exported value in either format, and only to a value that is a number in the stored grammar — an integer carries no separator and is unchanged. The conversion MUST run after the sensitivity transformation and MUST NOT feed into it: a personal field is hashed from its stored value, so a hash never depends on the option (REQ-EXP-030). Formula neutralization MUST recognize a converted negative number (`-1,5`) as a number and leave it unprefixed (REQ-VAL-032). The stored value is never altered, and import keeps accepting the period form only (REQ-VAL-016): a comma-decimal file is a copy for reading, not an import source (DEV-EXP-6). |
| REQ-EXP-016 | **Byte-order mark.** A CSV download from the web UI MAY begin with a UTF-8 byte-order mark at the user's choice (REQ-UI-020), so that spreadsheet software opening the file directly reads non-ASCII text (`æ`, `ø`, `å`) as UTF-8. The mark belongs to the browser download only: neither API surface emits one, so programmatic callers — REDCap clients, R, pandas, which would otherwise read the mark into the first column name — always receive the file without it (DEV-EXP-6). |

## 4. Sensitivity Levels and Transformation Semantics

This section is the de-identification semantics referenced by REQ-API-026/075 and REQ-AUTH-018.

| ID | Requirement |
|---|------|
| REQ-EXP-020 | A **direct identifier field** MUST be classified as: the record identifier field (GD-8), or a field whose user-set `direct_identifier` flag is set (REQ-DB-013). The flag is offered on **any field** in the designer and MUST default to set when the field's validation type is `email`, `MRN`, `international phone` or `national phone` (REQ-VAL-043); the user MAY change it. Direct identifier fields MUST be removed — the column absent from the output — at both `export_de_identified` and `export_no_identifiers` (D-1). |
| REQ-EXP-021 | **Fail-safe:** the value of a direct identifier field MUST NOT leave the system untransformed at any level below `export_full`, regardless of the field's personal-information flag (D-1). Clearing the `direct_identifier` flag on a field whose validation type is `email`, `MRN`, `international phone` or `national phone` MUST require an explicit user action and MUST be warned about in the designer (the preset exists so identifier-shaped data is never exported untransformed by omission — DEV-EXP-5). |
| REQ-EXP-022 | A **personal field** is one flagged `personal_information` (REQ-DB-013) and not a direct identifier. At `export_de_identified`, the value of a personal field MUST be replaced by a salted hash (§5); at `export_no_identifiers` and `export_full` it is kept (D-2). |
| REQ-EXP-023 | **Free text fields** (`field_type = text` or `notes` — REQ-DB-044 —, not a personal or direct-identifier field) MUST be excluded from `export_de_identified` output unless the field's `export_approved` flag is set (REQ-DB-013, DEV-DB-2, plan "Anonymization Rules"); at the other levels they are kept (D-3). |
| REQ-EXP-024 | The per-level transformation MUST apply exactly the steps of the design pipeline (`Design/Data_Export_Anonymization_Design.md` §4.2): each weaker level MUST apply a strict subset of the stronger level's steps, honoring the GD-2 ordering (higher includes lower, REQ-AUTH-018) — `export_de_identified`: direct identifiers removed, personal fields hashed, unapproved free text removed, dates shifted; `export_no_identifiers`: direct identifiers removed and nothing else transformed (D-2). |

## 5. Anonymization Rules

### 5.1 Field Hash

| ID | Requirement |
|---|------|
| REQ-EXP-030 | The personal-field hash MUST be salted with the deployment salt (REQ-CFG-015), MUST be deterministic — the same (project, field, value) always yields the same hash — and MUST be scoped so that identical values in different projects or fields do not collide (D-5). |
| REQ-EXP-031 | The hash MUST be one-way for anyone without the salt: the salt MUST be a redacted secret (REQ-CFG-021/022) and MUST NOT appear in logs, audit details, or be derivable from a hash (REQ-TECH-017). |
| REQ-EXP-032 | Hashing MUST be a transformation of the output value only; the stored value MUST NOT be altered by an export (the sole in-place exception is the end-provision `anonymize` action, §7). |

### 5.2 Date Shifting

This section is the date-shift consistency semantics referenced by REQ-DB-023.

| ID | Requirement |
|---|------|
| REQ-EXP-033 | At `export_de_identified`, the date part of a surviving date/date-time value MUST be shifted by a per-record offset; the shift MUST be consistent for a given record across time and across exports (plan "consistent for each patient"; REQ-DB-023). |
| REQ-EXP-034 | The per-record offset MUST be persisted on first use (REQ-DB-023) and MUST be derived from the deployment salt and a configured range (REQ-CFG-015/016); the derivation is normative in the design document (D-5). |
| REQ-EXP-035 | The shift MUST apply to the date part only and MUST preserve the time part and the collection timezone offset (GD-16, REQ-VAL-041); chronological comparisons MUST use absolute instants (GD-16). |

## 6. End-of-Project Provision (BR-009)

| ID | Requirement |
|---|------|
| REQ-EXP-040 | The system MUST provide a mechanism to execute a project's end provision at the REK end date: `delete` (removes the project's stored record data, keeping structure, metadata, memberships, and the audit trail) or `anonymize` (applies the `export_de_identified` transformation in place to the stored values, after which every level — including `export_full` — returns the anonymized form) (charter in-scope "end-provision execution"). |
| REQ-EXP-041 | The provision itself MUST NOT be system state (GD-17, REQ-DB-006/032): the system executes the provision supplied by the operator — from project data such as a `DataTransferProjects` instrument, or from outside the system — and the REK end date remains project metadata (REQ-DB-006). |
| REQ-EXP-042 | Execution MUST be one-shot (a second execution MUST be rejected), atomic (no partial state on failure), and audit-logged with the provision and the affected scope (REQ-AUD-003); the action MUST be restricted to `is_admin` (D-7). |
| REQ-EXP-043 | Execution MUST NOT be an automatic background job: it is an explicit operator action, since the stack has no scheduler component (D-7, `Technology_Stack_Requirements.md` §4). |

## 7. Audit and Privacy Boundaries

| ID | Requirement |
|---|------|
| REQ-EXP-050 | Every export on either surface MUST be audit-logged with the applied sensitivity level and the filters supplied (REQ-AUD-011, BR-008); data-API invocations MUST additionally write a record-view row regardless of initiator (REQ-AUD-013). |
| REQ-EXP-051 | Neither the audit trail nor the application logs MAY contain exported or transformed values, or the anonymization salt (REQ-AUD-014, REQ-CFG-021/022, REQ-TECH-017). |
| REQ-EXP-052 | Branching logic MUST NOT affect export (GD-13): it is display-only and MUST NOT change which values are returned or how they are transformed. |
| REQ-EXP-053 | A `checkbox` field (REQ-DB-042) exports as one column per choice, named `<field_name>___<choice_code>` in choice order directly after the field's dictionary position. Raw export writes `1` for a checked box and `0` for an unchecked one; label export writes the choice's text when checked and nothing otherwise. The `fields[]` filter matches either the parent name (selecting every choice column) or an individual derived name. Choice columns inherit the parent field's sensitivity: they are never hashed (the stored value carries no information beyond checked/unchecked). Data-dictionary exports (`content=metadata`, `exportFieldNames`) show the checkbox as its single parent row with its choices, not as derived rows. |

## 8. Assumptions

| ID | Assumption |
|---|------|
| ASM-EXP-1 | The `personal_information` and `export_approved` flags are set by the data manager in the instrument designer at design time; the system defines no default beyond `0` (REQ-DB-013). |
| ASM-EXP-2 | Anonymization transforms the export output (or the one-time in-place execution, REQ-EXP-040); there is no continuous or background anonymization process and no re-identification capability. |
| ASM-EXP-3 | An export's only write side effect is the persistence of the anonymization offset (REQ-DB-023); it never creates or updates a record, value, or structure. |

## 9. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-EXP-1 | The plan's per-instrument access levels (Full/Anonymized/None) were superseded by GD-2's per-arm export levels and **returned on 2026-10-03 as the export level of a role grant on an (instrument, event) pair** (REQ-AUTH-017/069), with the fourth ladder step kept; per-field control stays with the `personal_information`/`export_approved` flags (REQ-EXP-022/023) | GD-2 revised the permission model to per-arm levels (charter §5, 2026-09-19); the charter lists per-instrument levels as out of scope (phase 1). |
| DEV-EXP-2 | The plan's "random number of days" date shift is realized as a deterministic salted offset persisted per record (REQ-EXP-033/034) | The plan itself requires the shift to be "consistent for each patient"; a random offset per export would break that consistency (REQ-DB-023). |
| DEV-EXP-3 | The master spec's checkbox 1/0 per-choice columns are realized via matrix row expansion (REQ-EXP-012); this system has no checkbox field type (REQ-DB-013) | Matrix rows are already expanded into individual fields (REQ-DB-014), so each row is its own column; 1/0 coding is available where the row choices are defined as 1/0. |
| DEV-EXP-4 | The plan's direct-identifier set (names, emails, MRNs) is realized as the record identifier (GD-8) plus `email`/`MRN` validation types (REQ-EXP-020/021); name-type fields are controlled via the personal-information flag and hashed (REQ-EXP-022) | The data dictionary carries no name/identifier flag; the record identifier and validation types are the available, fail-safe markers. Hashing a name is one-way pseudonymization, consistent with the plan's personal-information mechanism. **Revised 2026-09-25:** the data dictionary now carries an explicit per-field flag — see DEV-EXP-5. |
| DEV-EXP-5 | Direct-identifier classification changed from validation-type-derived (`email`/`MRN`) to a user-set `direct_identifier` flag on any field; preset for `email`/`MRN`/`international phone`/`national phone` | Owner decision (2026-09-25, master spec "Field validation"): identifier status is a user-defined choice for any field; the preset keeps identifier-shaped data fail-safe while letting owners mark e.g. a name or address text field as a direct identifier (REQ-EXP-020/021). |
| DEV-EXP-6 | The CSV export gains spreadsheet-locale options the master spec's format list does not name: REDCap's `decimalCharacter` (REQ-EXP-015), an optional byte-order mark on the UI download (REQ-EXP-016), and UI defaults that follow the user's language — semicolon, decimal comma and the mark for `nb`/`nn` (REQ-UI-020). The `csvDelimiter` value set narrows from "any single character" to REDCap's (REQ-EXP-013) | Owner question (2026-10-11, master spec "Export features"): Norwegian spreadsheet software uses the comma as decimal separator and the semicolon as list separator, so a comma-delimited, period-decimal file opens with numbers read as text or dates (`1.5` → 1 May) and, without the mark, with garbled `æøå`. A semicolon alone does not fix the numbers, hence the decimal character. The language defaults are a pre-selection in the UI, not a silent rewrite: exports also feed R, SPSS and Stata, so every option stays visible and changeable, and the API keeps REDCap's defaults (comma, stored decimals, no mark) for existing callers. Accepting `tab` by name alongside the character covers callers that follow REDCap's spelling, which the data API had reduced to the letter `t`; `"`, CR and LF cannot delimit a CSV field at all. |
