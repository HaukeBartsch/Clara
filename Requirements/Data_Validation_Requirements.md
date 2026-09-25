# Data Validation — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Data_Validation.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-19

## 1. Purpose

Defines the validation requirements that apply to all data written into the system: the field-level value rules the Go API MUST enforce before storage (BR-004), the field name rules of the data dictionary, and the record-identifier rules (GD-8, REQ-DB-020). `Design/Data_Validation_Design.md` contains the normative validator table, error codes, and format grammars.

## 2. General Requirements

| ID | Requirement |
|---|---|
| REQ-VAL-001 | Every value written to the database — on any entry path (data API import, UI data entry, survey link submission) — MUST pass server-side validation in the Go API before storage (BR-004, REQ-API-032); invalid values MUST NOT be stored. |
| REQ-VAL-002 | Validation MUST be authoritative in the API (master spec: \"authoritative validation in the API\"); client-side feedback (JavaScript + HTML5 validation attributes) is advisory only and MUST NOT be relied upon for integrity (User_Interface plan, data entry form; REQ-TECH-006). |
| REQ-VAL-003 | Validation rules MUST be derived entirely from the field's data dictionary entry (REQ-DB-013): field type, choices, validation type + min/max, required flag; there MUST be no hard-coded, per-project, or per-caller rule sets. |
| REQ-VAL-004 | The same field MUST be validated identically for every caller (API token, UI, administration surface); no rule MAY be relaxed based on the caller. |
| REQ-VAL-005 | A value supplied for a field name that does not exist in the project's data dictionary MUST be rejected as an unknown-field validation error and MUST NOT be stored in the EAV table (DEV-VAL-1). |
| REQ-VAL-006 | A value supplied for a description or header field MUST be rejected: these field types carry text only and accept no values (plan §4). |
| REQ-VAL-007 | Each `data[]` import entry MUST provide a record name and a field name; an entry missing either MUST be reported as a per-entry validation error (REQ-API-031, REQ-API-034 result code `0`). |
| REQ-VAL-008 | A validation failure MUST NOT store any value of the record (all-or-nothing, REQ-API-035) and MUST report per-field details (field name, violated rule, offending value) sufficient for the import error response (REQ-API-034). |
| REQ-VAL-009 | Validation error responses MUST carry a stable machine-readable rule code plus a human-readable message; they MUST NOT leak internal implementation details (REQ-API-006, REQ-API-039). |
| REQ-VAL-010 | A field's `validation_type` MUST be empty (no validation), one of the built-in structured types (`integer`, `floating point`, `date`, `datetime`), or a name present in the **validation-type registry** (REQ-VAL-042, REQ-DB-033); the designer MUST reject unsupported types (REQ-DB-013). |

## 3. Field Name Rules (data dictionary)

| ID | Requirement |
|---|---|
| REQ-VAL-011 | A field name MUST consist of lower-case alphanumeric characters and underscores only (REQ-DB-013, master spec). |
| REQ-VAL-012 | A field name MUST be unique within a project (REQ-DB-013). |
| REQ-VAL-013 | A field name longer than 26 characters MUST be accepted; the warning after 26 characters is a UI concern, not an API rejection (master spec; REQ-API-068). |
| REQ-VAL-014 | Renaming a field MUST atomically rename its stored values in the same transaction (no orphaned values) and MUST be audit-logged (REQ-API-043); the EAV layout makes this a single key update (DEV-VAL-4). |

## 4. Value Rules by Field Type

### 4.1 Text validation types

| ID | Requirement |
|---|---|
| REQ-VAL-015 | `integer`: the value MUST be a whole number (optional leading minus, digits only, no decimal point, no exponent) and, when `validation_min`/`validation_max` are set, MUST lie within the inclusive range (REQ-DB-013). |
| REQ-VAL-016 | `floating point`: the value MUST be a decimal number (optional sign, digits, single decimal point) and, when min/max are set, MUST lie within the inclusive range. |
| REQ-VAL-017 | `email`: the value MUST match a standard email format (local part, `@`, single domain); the exact grammar is the seeded registry entry defined in the design document. |
| REQ-VAL-018 | `MRN`: the value MUST be exactly 11 digits (master spec); the grammar is a seeded registry entry (REQ-VAL-042). |
| REQ-VAL-042 | Validation MUST be **extensible**: a validation type is either one of the four built-in structured types (`integer`, `floating point`, `date`, `datetime` — validated by dedicated logic for min/max, format tokens, calendar validity and timezone) or a **named regular expression stored in the database** (the validation-type registry, REQ-DB-033). A regex type matches when the whole value matches the entry's pattern (Go RE2 syntax); adding a type MUST be an insert into the registry, not a schema or code change (mirrors the languages rule, REQ-DB-031). |
| REQ-VAL-043 | The registry MUST be seeded with: `email`, `MRN`, `international phone` — a number in international format such as `+47 55566777` (`+`, country code, optional single spaces) — and `national phone` — a national number without country code such as `55566777`; the exact grammars are defined in the design document. |
| REQ-VAL-019 | `date`: the value MUST match the format specified for the field (e.g. `Y-m-d`, `m-d-Y`) and MUST be a valid calendar date (e.g. `2026-02-30` is rejected); the canonical storage form is defined by the design document (ASM-VAL-1) and carries the timezone of collection (GD-16, REQ-VAL-041). |
| REQ-VAL-039 | `datetime`: the value MUST match the format specified for the field (e.g. `Y-m-d H:i`, `m-d-Y H:i`), MUST be a valid calendar date AND a valid time of day (e.g. `2026-02-30` and `25:00` are both rejected); the canonical storage form is defined by the design document (ASM-VAL-1, DEV-VAL-8) and carries the timezone of collection (GD-16, REQ-VAL-041). |
| REQ-VAL-020 | `validation_min`/`validation_max` apply only to `integer` and `floating point` (plan §1); for other validation types they MUST be ignored. |
| REQ-VAL-021 | A text field with no validation type MUST accept any non-empty UTF-8 string without a fixed length limit beyond the storage type (ASM-VAL-2, REQ-DB-019). |

### 4.2 Choice fields (dropdown, radio)

| ID | Requirement |
|---|---|
| REQ-VAL-022 | The value MUST be one of the field's predefined numeric codes; it is stored as the code, not the label (plan §2, master spec: \"choice is stored as code\"). |

### 4.3 Matrix fields

| ID | Requirement |
|---|---|
| REQ-VAL-023 | Each matrix row (the expanded field rows of a `matrix_group`, REQ-DB-014) MUST be validated against the choices/validation shared by the group; an invalid selection fails that row's field with per-field error detail (REQ-VAL-008). |

### 4.4 Empty values

| ID | Requirement |
|---|---|
| REQ-VAL-024 | An empty-string value MUST be treated as \"no value\": it is exempt from type validation, clears an existing stored value (or creates none), and round-trips with the export's empty string for missing values (REQ-API-028; DEV-VAL-2). An empty value on import is therefore an **intentional clear**: the web application MUST NOT send empty values for fields whose previous value the user has not removed (GD-14, REQ-UI-031). |

### 4.5 Free-form text content policy

| ID | Requirement |
|---|---|
| REQ-VAL-030 | All free-form text values MUST pass a single centralized content policy enforced by the Go API at storage, applied identically on every entry path (data API import, UI data entry, survey link submission — REQ-VAL-001/004). The policy covers: valid UTF-8, a maximum length (design document; the default remains REQ-VAL-021), rejection of C0 control characters other than tab/newline, and an **HTML allowlist**: free-form text MAY contain HTML restricted to the safe elements `a`, `b`, `br`, `code`, `em`, `i`, `li`, `ol`, `p`, `s`, `strong`, `u`, `ul`, `blockquote` (the exact list is final in the design document, ASM-VAL-5); no attributes are allowed except `href` on `a` with scheme allowlist `http`/`https`/`mailto`; any markup outside the allowlist MUST be stripped at storage, with its text content preserved as plain text. |
| REQ-VAL-031 | Rendering: free-form text values are stored as sanitized allowlist HTML (REQ-VAL-030) and MAY be rendered as HTML in the UI (data entry form, record view, record history, audit views); **all other** user-supplied content (choice values, field labels, record names, user names, audit fields) MUST be escaped server-side (REQ-TECH-020); the Content-Security-Policy header remains the backstop (REQ-TECH-020). |
| REQ-VAL-032 | An exported CSV cell whose value begins with a formula-triggering character (`=`, `+`, `-`, `@`) MUST be neutralized so that spreadsheet software does not evaluate it (e.g. single-quote prefix; the mechanism is in the design document) (REQ-API-029). |

### 4.6 Calculated fields (GD-11)

| ID | Requirement |
|---|---|
| REQ-VAL-033 | A new field type `calculated` MUST be supported. The field carries a calculation expression (stored with the field, REQ-DB-013) composed of field references of the form `[<unique_event_name>][<field_name>]`, numeric constants, the operators `+`, `-`, `*`, `/`, and optional parentheses with standard precedence (ASM-VAL-4). The expression MUST be validated at creation and update. |
| REQ-VAL-034 | A field reference MUST name an existing value-carrying field of the project that is active at the referenced event (REQ-DB-012); a reference to another calculated field is allowed if the dependency graph stays acyclic (REQ-VAL-035). An expression referencing a nonexistent field, a non-value field (description/header), or a field not active at that event MUST be rejected at design time (REQ-API-065/069). |
| REQ-VAL-035 | The dependency graph of calculated fields MUST be acyclic: a creation or update that would introduce a cycle MUST be rejected. |
| REQ-VAL-036 | A calculated field's value is system-managed: an import of a value into a calculated field MUST be rejected (REQ-API-095). |
| REQ-VAL-037 | The value is stored at each (record, event, instrument, instance) position where the field is active (REQ-DB-030). These values MUST be recomputed in the same transaction whenever the value of a referenced (event, field) changes or is deleted; a change of the expression MUST trigger recomputation for all records of the project (ASM-VAL-4). |
| REQ-VAL-038 | Evaluation: a missing/empty referenced value, a non-numeric operand, or division by zero MUST yield an empty value for the calculation; the triggering import itself MUST still succeed (its own value was valid); the evaluation failure MUST be application-logged (REQ-TECH-016). Every recomputation MUST be audit-logged with the triggering user, the record, the field, and old/new values (REQ-AUD-023). |

## 5. Record Identifier Rules (GD-8)

| ID | Requirement |
|---|---|
| REQ-VAL-025 | The record identifier is the field at position 1 of the instrument at position 1 of the project (GD-8); its value is the record name (`record_id`, REQ-DB-020). |
| REQ-VAL-026 | On import, the identifier field's value for a row MUST be non-empty (this overrides the no-value semantics of REQ-VAL-024 for the identifier field); it determines the record's identity for the upsert (REQ-API-033) and the EAV key (REQ-DB-015). |
| REQ-VAL-027 | In phase 1, the identifier value of an existing record MUST NOT be changed; an import attempting to change it MUST be rejected with a validation error (DEV-VAL-3). Correction of a wrong identifier is available through delete + re-import (GD-3, REQ-API-036). |

## 6. Required and Branching Logic (GD-13)

| ID | Requirement |
|---|---|
| REQ-VAL-028 | The `required` flag MUST be exposed in the metadata and the designer; in phase 1, an import MUST NOT be rejected because a required value is missing (partial records are first-class, ASM-VAL-3); the data entry form MUST present required fields and validate them before submission (User_Interface plan, data entry form). |
| REQ-VAL-029 | A field's branching logic MUST be an expression over other fields of the same project, arm, and record: references `[event][field]`; value comparisons with the operators `=`, `!=`, `<`, `>`, `<=`, `>=` between a reference or a constant (e.g. `[ev][age] >= 18`, `[ev][status] = "done"`); the usual functions `text_contains(ref, "substring")`, `is_blank(ref)`, `is_not_blank(ref)`; logical AND/OR; parentheses (GD-13); a radio/checkbox reference evaluates to `1` when checked/selected and `0` otherwise; a missing value evaluates to `0` (ASM-VAL-6); the field is displayed only while the expression evaluates to true (1), in surveys (GD-9) and in normal data entry; the display state MUST be re-evaluated whenever a referenced field's value changes; branching logic MUST NOT affect import, export, or audit (display-only; DEV-VAL-5); an invalid expression (unknown field, syntax error) MUST be rejected at design time. |
| REQ-VAL-040 | An instrument's branching logic MUST use the same expression grammar (GD-13, REQ-VAL-029); the instrument — and with it all of its fields — is displayed only while the expression evaluates to true (1); a hidden instrument hides its fields regardless of their own branching logic; it applies in surveys and in normal data entry; it MUST NOT affect import, export, or audit (display-only). |
| REQ-VAL-041 | **Timezone of collection (GD-16).** The canonical storage form of a `date` value is `YYYY-MM-DD±HH:MM` and of a `datetime` value `YYYY-MM-DD HH:MM±HH:MM` — the wall time as collected, plus the timezone offset (`±HH:MM`; UTC is `+00:00`); values are stored as collected, never converted to UTC. The offset for an imported value is determined, in order, from: (1) the zone supplied with the import — the browser's timezone for UI data entry (sent by the PHP layer), or an optional `tz` parameter (IANA name or `±HH:MM` offset) of the data-API import; (2) the deployment default `APP_TIMEZONE` (configuration; default `UTC`). A value already in canonical form keeps its stored offset. Export returns the stored canonical form (offset included). Anonymized-export date shifting (REQ-DB-023) applies to the date part and preserves the offset. Chronological comparisons (branching logic, `filterLogic`) MUST compare absolute instants (stored wall time + offset). |

## 7. Assumptions

| ID | Assumption |
|---|---|
| ASM-VAL-1 | The canonical storage form for dates and date-times (after validating against the field's specified format) is defined by the design document; it carries the timezone of collection (GD-16, REQ-VAL-041) — values are stored as collected, not normalized to UTC; anonymized export requires parseable dates (REQ-DB-023) and shifts the date part, preserving the offset. |
| ASM-VAL-2 | There is no fixed maximum length for free-text values beyond the storage type (`TEXT`/`LONGTEXT`, REQ-DB-019); the design document MAY define an upper bound. |
| ASM-VAL-3 | Partial records are a first-class state: the record status dashboard tracks per-instrument completion (User_Interface plan §6), so completion is tracked rather than enforced at import. |
| ASM-VAL-4 | Calculated-field details: optional parentheses with standard precedence (multiplication/division before addition/subtraction); calculated fields may reference calculated fields while the dependency graph stays acyclic; the value is stored at each active (record, event, instrument, instance) position (REQ-DB-030); an expression change triggers project-wide recomputation. |
| ASM-VAL-5 | Free-form text details: the exact element allowlist is final in the design document (REQ-VAL-030 default list); the chosen behavior is to **strip** disallowed markup (value accepted, content preserved) rather than reject the whole value; attributes are not allowed except `href` on `a` (`http`/`https`/`mailto`); designer metadata (labels, notes, choices) is out of scope of the allowlist and remains escaped-rendered. |
| ASM-VAL-6 | Branching logic details: a comparison is numeric when both operands are numeric, chronological when both parse as dates/datetimes in their fields' formats, otherwise lexicographic string comparison; a missing referenced value makes the comparison evaluate to `0` (false) for every operator; a reference used outside a comparison evaluates by truthiness (empty → `0`, non-empty → `1`; numeric `0` → `0`); the function set (`text_contains`, `is_blank`, `is_not_blank`) and the operator syntax (`&&`/`||` vs. `and`/`or`) are final in the design document. |

## 8. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-VAL-1 | Unknown field names are rejected at import | The plan is silent; a value without a dictionary entry has no rules and MUST NOT create an orphaned EAV row (REQ-VAL-005). |
| DEV-VAL-2 | Empty import values act as \"no value\" (clear/no-op) | The plan is silent; REDCap-compatible round-trip with the export shape of empty strings (REQ-API-028). |
| DEV-VAL-3 | The identifier value is immutable for an existing record in phase 1 | GD-8 defines derivation but not renaming; immutability protects the EAV key (REQ-DB-015) and audit continuity; delete/re-import remains available (GD-3). |
| DEV-VAL-4 | Field renames atomically rename stored values | The plan is silent; the EAV layout makes this a single key update; consistent with the audit plan's \"Project Structure\" events. |
| DEV-VAL-5 | `required` and branching-logic flags are advisory for imports in phase 1 | The plan stores the flags without enforcement semantics; partial records plus the record status dashboard (ASM-VAL-3) indicate completion is tracked, not enforced at import. |
| DEV-VAL-6 | Calculated field type added | Owner decision (2026-09-19, GD-11): expressions over other fields (`[event][field]`, `+ - * /`, numeric constants) with automatic recomputation. |
| DEV-VAL-7 | Centralized free-form text content policy (HTML allowlist, safe elements) and CSV formula-injection neutralization | Owner concern (2026-09-19): stored XSS / spreadsheet-injection defense enforced at the single storage choke point; free-form text may carry allowlisted basic formatting, all other content stays escaped-rendered (REQ-VAL-030/031). |
| DEV-VAL-8 | `datetime` validation type added (date + time) | Owner request (2026-09-19): the plan's validation type list covers `date` but not date+time (REQ-VAL-039). |
| DEV-VAL-9 | Branching logic upgraded from flag to expression (fields and instruments) | Owner request (2026-09-19, GD-13): `[event][field]` references, value comparisons (`= != < > <= >=`) and usual functions (`text_contains`, `is_blank`, `is_not_blank`), AND/OR, parentheses; radio/checkbox = `1`/`0`; display-only in surveys and data entry. |
| DEV-VAL-10 | Timezone added to the internal (canonical) date and date-time form; values stored as collected, never converted to UTC | Owner decision (2026-09-22, GD-16; master spec "Details": "Support different time zones for the internal storage of dates and times … using the browser timezone information"): canonical forms `YYYY-MM-DD±HH:MM` / `YYYY-MM-DD HH:MM±HH:MM`; offset from import-supplied zone (browser tz / `tz` parameter) else `APP_TIMEZONE` (REQ-VAL-041). |
| DEV-VAL-11 | Validation types made extensible via a database registry of named regular expressions; `international phone` and `national phone` added | Owner decision (2026-09-25, master spec "Field validation"): the plan's closed type list becomes four built-in structured types plus DB-stored regex entries seeded with `email`, `MRN`, `international phone` (`+47 55566777`) and `national phone` (`55566777`) (REQ-VAL-042/043, REQ-DB-033). |
