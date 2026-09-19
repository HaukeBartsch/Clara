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
| REQ-VAL-001 | Every value written to the database — on any entry path (data API import, UI data entry) — MUST pass server-side validation in the Go API before storage (BR-004, REQ-API-032); invalid values MUST NOT be stored. |
| REQ-VAL-002 | Validation MUST be authoritative in the API (master spec: \"authoritative validation in the API\"); client-side feedback (JavaScript + HTML5 validation attributes) is advisory only and MUST NOT be relied upon for integrity (User_Interface plan, data entry form; REQ-TECH-006). |
| REQ-VAL-003 | Validation rules MUST be derived entirely from the field's data dictionary entry (REQ-DB-013): field type, choices, validation type + min/max, required flag; there MUST be no hard-coded, per-project, or per-caller rule sets. |
| REQ-VAL-004 | The same field MUST be validated identically for every caller (API token, UI, administration surface); no rule MAY be relaxed based on the caller. |
| REQ-VAL-005 | A value supplied for a field name that does not exist in the project's data dictionary MUST be rejected as an unknown-field validation error and MUST NOT be stored in the EAV table (DEV-VAL-1). |
| REQ-VAL-006 | A value supplied for a description or header field MUST be rejected: these field types carry text only and accept no values (plan §4). |
| REQ-VAL-007 | Each `data[]` import entry MUST provide a record name and a field name; an entry missing either MUST be reported as a per-entry validation error (REQ-API-031, REQ-API-034 result code `0`). |
| REQ-VAL-008 | A validation failure MUST NOT store any value of the record (all-or-nothing, REQ-API-035) and MUST report per-field details (field name, violated rule, offending value) sufficient for the import error response (REQ-API-034). |
| REQ-VAL-009 | Validation error responses MUST carry a stable machine-readable rule code plus a human-readable message; they MUST NOT leak internal implementation details (REQ-API-006, REQ-API-039). |
| REQ-VAL-010 | A field's `validation_type` MUST be empty (no validation) or one of: `integer`, `floating point`, `email`, `MRN`, `date`; the designer MUST reject unsupported types (REQ-DB-013). |

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
| REQ-VAL-017 | `email`: the value MUST match a standard email format (local part, `@`, single domain); the exact grammar is defined in the design document. |
| REQ-VAL-018 | `MRN`: the value MUST be exactly 11 digits (master spec). |
| REQ-VAL-019 | `date`: the value MUST match the format specified for the field (e.g. `Y-m-d`, `m-d-Y`) and MUST be a valid calendar date (e.g. `2026-02-30` is rejected); the canonical storage form is defined by the design document (ASM-VAL-1). |
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
| REQ-VAL-024 | An empty-string value MUST be treated as \"no value\": it is exempt from type validation, clears an existing stored value (or creates none), and round-trips with the export's empty string for missing values (REQ-API-028; DEV-VAL-2). |

## 5. Record Identifier Rules (GD-8)

| ID | Requirement |
|---|---|
| REQ-VAL-025 | The record identifier is the field at position 1 of the instrument at position 1 of the project (GD-8); its value is the record name (`record_id`, REQ-DB-020). |
| REQ-VAL-026 | On import, the identifier field's value for a row MUST be non-empty (this overrides the no-value semantics of REQ-VAL-024 for the identifier field); it determines the record's identity for the upsert (REQ-API-033) and the EAV key (REQ-DB-015). |
| REQ-VAL-027 | In phase 1, the identifier value of an existing record MUST NOT be changed; an import attempting to change it MUST be rejected with a validation error (DEV-VAL-3). Correction of a wrong identifier is available through delete + re-import (GD-3, REQ-API-036). |

## 6. Required and Branching Flags (phase-1 semantics)

| ID | Requirement |
|---|---|
| REQ-VAL-028 | The `required` flag MUST be exposed in the metadata and the designer; in phase 1, an import MUST NOT be rejected because a required value is missing (partial records are first-class, ASM-VAL-3); the data entry form MUST present required fields and validate them before submission (User_Interface plan, data entry form). |
| REQ-VAL-029 | Branching logic MUST be exposed in the metadata; in phase 1, the API MUST NOT reject imported values based on branching logic, and the UI MUST evaluate it for field display (show/hide) (DEV-VAL-5). |

## 7. Assumptions

| ID | Assumption |
|---|---|
| ASM-VAL-1 | The canonical storage form for dates (after validating against the field's specified format) is defined by the design document; anonymized export requires parseable dates (REQ-DB-023) and UTC normalization (GD-7). |
| ASM-VAL-2 | There is no fixed maximum length for free-text values beyond the storage type (`TEXT`/`LONGTEXT`, REQ-DB-019); the design document MAY define an upper bound. |
| ASM-VAL-3 | Partial records are a first-class state: the record status dashboard tracks per-instrument completion (User_Interface plan §6), so completion is tracked rather than enforced at import. |

## 8. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-VAL-1 | Unknown field names are rejected at import | The plan is silent; a value without a dictionary entry has no rules and MUST NOT create an orphaned EAV row (REQ-VAL-005). |
| DEV-VAL-2 | Empty import values act as \"no value\" (clear/no-op) | The plan is silent; REDCap-compatible round-trip with the export shape of empty strings (REQ-API-028). |
| DEV-VAL-3 | The identifier value is immutable for an existing record in phase 1 | GD-8 defines derivation but not renaming; immutability protects the EAV key (REQ-DB-015) and audit continuity; delete/re-import remains available (GD-3). |
| DEV-VAL-4 | Field renames atomically rename stored values | The plan is silent; the EAV layout makes this a single key update; consistent with the audit plan's \"Project Structure\" events. |
| DEV-VAL-5 | `required` and branching-logic flags are advisory for imports in phase 1 | The plan stores the flags without enforcement semantics; partial records plus the record status dashboard (ASM-VAL-3) indicate completion is tracked, not enforced at import. |
