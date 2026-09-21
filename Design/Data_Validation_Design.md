# Data Validation — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Data_Validation_Requirements.md`
**Date:** 2026-09-19

## 1. Purpose

The normative validation pipeline, rule codes, value grammars, content policy mechanics, and the two expression languages (calculated fields, branching logic). Everything here is enforced in the Go API at the single storage choke point (REQ-VAL-001/002/004); the UI's client-side checks are advisory mirrors of these rules.

## 2. Validation Pipeline

Every value passes the same ordered pipeline, regardless of entry path (data API import, UI data entry, survey link — REQ-VAL-001/004):

| Step | Rule | On failure |
|---|---|---|
| 1 | entry carries record name + field name (REQ-VAL-007) | code `IMPORT_MISSING_KEY`, per-entry error |
| 2 | field exists in this project's data dictionary (REQ-VAL-005) | `UNKNOWN_FIELD` |
| 3 | field type accepts values — not `description`/`header` (REQ-VAL-006), not `calculated` (REQ-VAL-036) | `NON_VALUE_FIELD` / `CALCULATED_READONLY` |
| 4 | value non-empty? no → "no value" (clear/no-op, REQ-VAL-024) — **except** the record identifier (REQ-VAL-026) | identifier: `EMPTY_IDENTIFIER` |
| 5 | type validator per `validation_type` (§4) | `TYPE_INVALID` / `RANGE` / `CHOICE_INVALID` |
| 6 | content policy for free-form text (§5) | strips markup (rarely errors: `CONTENT_INVALID` for non-UTF-8 / control chars) |
| 7 | identifier rules: existing record's identifier unchanged (REQ-VAL-027) | `IDENTIFIER_CHANGE` |

One failed value fails the whole record's import (all-or-nothing, REQ-VAL-008/REQ-API-035) with per-field detail (REQ-API-034). Rules come **only** from the data dictionary (REQ-VAL-003) — no per-project or per-caller rule sets.

## 3. Rule Codes (stable, machine-readable — REQ-VAL-009)

| Code | Meaning | Example message |
|---|---|---|
| `IMPORT_MISSING_KEY` | entry lacks record or field name | "import entry missing record name" |
| `UNKNOWN_FIELD` | field not in project dictionary | "unknown field: foo" |
| `NON_VALUE_FIELD` | description/header field | "field 'note' does not accept values" |
| `CALCULATED_READONLY` | calculated field is system-managed | "field 'total' is calculated and read-only" |
| `EMPTY_IDENTIFIER` | identifier empty on import | "record identifier must not be empty" |
| `IDENTIFIER_CHANGE` | existing record's identifier changed | "record identifier of existing record cannot change" |
| `TYPE_INVALID` | value fails the type grammar (§4) | "value 'abc' is not a valid integer" |
| `RANGE` | min/max violated | "value 42 outside range 0…10" |
| `CHOICE_INVALID` | code not in the field's choices | "value '9' is not a choice of 'status'" |
| `CONTENT_INVALID` | non-UTF-8 or disallowed control character | "value contains disallowed control characters" |

Codes are stable across versions (callers may branch on them); messages are human-readable and MUST NOT leak internals (REQ-API-006/039).

## 4. Type Validators (normative)

| `validation_type` | Grammar (Go regular expression) | Notes |
|---|---|---|
| `integer` | `^-?[0-9]+$` | optional leading minus, digits only; then inclusive min/max (REQ-VAL-015) |
| `floating point` | `^[+-]?[0-9]+(\.[0-9]+)?$` | optional sign, single decimal point; then inclusive min/max (REQ-VAL-016) |
| `email` | `^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$` | practical RFC 5322 subset (resolves REQ-VAL-017); domain must contain a label |
| `MRN` | `^[0-9]{11}$` | exactly 11 digits (REQ-VAL-018) |
| `date` | per `validation_format` (§4.1) | valid calendar date required (REQ-VAL-019) |
| `datetime` | per `validation_format` (§4.1) | valid date **and** time (REQ-VAL-039) |
| (empty) | any non-empty UTF-8 | free-form text, content policy applies (§5) (REQ-VAL-021) |

`validation_min`/`validation_max` apply to `integer`/`floating point` only; ignored elsewhere (REQ-VAL-020). Choice fields (dropdown/radio/matrix rows) validate against the field's numeric codes regardless of type (REQ-VAL-022/023).

### 4.1 Date and date-time formats

Format tokens (REDCap-style): `Y` = 4-digit year, `m` = 2-digit month, `d` = 2-digit day, `H` = 2-digit hour, `i` = 2-digit minute; separators `-` between date parts, `:` between time parts, single space between date and time.

- Accepted input format = the field's `validation_format` (DB column added for this; defaults `Y-m-d` for `date`, `Y-m-d H:i` for `datetime`)
- Calendar validation: month 1–12; day 1–last-day-of-month (leap-year aware); hour 0–23; minute 0–59
- **Canonical storage** (resolves ASM-VAL-1, matches `Database_Schema_Design.md` §1): dates `YYYY-MM-DD`; date-times `YYYY-MM-DD HH:MM` — the API converts from the accepted format to canonical on store and back on export
- Examples: format `m-d-Y` accepts `02-30-2026`? No — `2026-02-30` is not a calendar date, so both `30-02-2026` and `2026-02-30` are rejected; `25:00` rejected for `datetime`