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

A `validation_type` is either a **built-in structured type** (validated by dedicated logic — min/max, format tokens, calendar validity, timezone) or a **named regular expression from the validation-type registry** (§4.2, REQ-VAL-042). The whole value must match the pattern; patterns are Go RE2, stored pre-anchored.

| `validation_type` | Kind | Grammar (Go regular expression) | Notes |
|---|---|---|---|
| `integer` | built-in | `^-?[0-9]+$` | optional leading minus, digits only; then inclusive min/max (REQ-VAL-015) |
| `floating point` | built-in | `^[+-]?[0-9]+(\.[0-9]+)?$` | optional sign, single decimal point; then inclusive min/max (REQ-VAL-016) |
| `date` | built-in | per `validation_format` (§4.1) | valid calendar date required (REQ-VAL-019) |
| `datetime` | built-in | per `validation_format` (§4.1) | valid date **and** time (REQ-VAL-039) |
| `email` | registry (seeded) | `^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$` | practical RFC 5322 subset (resolves REQ-VAL-017); domain must contain a label |
| `MRN` | registry (seeded) | `^[0-9]{11}$` | exactly 11 digits (REQ-VAL-018) |
| `international phone` | registry (seeded) | `^\+[1-9][0-9 ]{7,14}$` | `+`, non-zero country-code digit, then 7–14 digits with single spaces allowed — e.g. `+47 55566777` (REQ-VAL-043) |
| `national phone` | registry (seeded) | `^[0-9]{8}$` | eight digits, no country code or separators — e.g. `55566777` (Norwegian national format; REQ-VAL-043) |
| (empty) | — | any non-empty UTF-8 | free-form text, content policy applies (§5) (REQ-VAL-021) |

`validation_min`/`validation_max` apply to `integer`/`floating point` only; ignored elsewhere — including every registry type (REQ-VAL-020). Choice fields (dropdown/radio/matrix rows) validate against the field's numeric codes regardless of type (REQ-VAL-022/023).

### 4.1 Date and date-time formats

Format tokens (REDCap-style): `Y` = 4-digit year, `m` = 2-digit month, `d` = 2-digit day, `H` = 2-digit hour, `i` = 2-digit minute; separators `-` between date parts, `:` between time parts, single space between date and time.

- Accepted input format = the field's `validation_format` (DB column added for this; defaults `Y-m-d` for `date`, `Y-m-d H:i` for `datetime`). A value already in canonical form (§4.1 canonical storage) is also accepted and its stored offset is kept.
- Calendar validation: month 1–12; day 1–last-day-of-month (leap-year aware); hour 0–23; minute 0–59
- **Canonical storage** (resolves ASM-VAL-1, matches `Database_Schema_Design.md` §1; **timezone of collection** — GD-16, REQ-VAL-041):
  - dates: `YYYY-MM-DD±HH:MM` — e.g. `2026-03-01+01:00`
  - date-times: `YYYY-MM-DD HH:MM±HH:MM` — e.g. `2026-03-01 09:30+01:00`
  - the offset is `±HH:MM` (UTC is `+00:00`); the API appends the collection offset on store and returns the canonical form (offset included) on export — values are **stored as collected, never converted** (GD-7 continues to govern system timestamps only)
- **Collection offset** (normative — REQ-VAL-041), in order:
  1. zone supplied with the import — the **browser's timezone** for UI data entry (the PHP layer resolves it to the offset of the value's date and passes it as `tz`), or the data-API import's optional `tz` parameter (IANA name, resolved to the offset at the value's date, or a direct `±HH:MM` offset);
  2. else `APP_TIMEZONE` (configuration, `System_Configuration_Design.md` §3.10; default `UTC`).
- Examples: format `m-d-Y` accepts `02-30-2026`? No — `2026-02-30` is not a calendar date, so both `30-02-2026` and `2026-02-30` are rejected; `25:00` rejected for `datetime`; `2026-03-01` (format `Y-m-d`, browser zone `Europe/Oslo`, March) stores as `2026-03-01+01:00`

### 4.2 The validation-type registry (REQ-VAL-042/043, REQ-DB-033)

Regex validation types live in the system-wide table `validation_types(name, regex, builtin)` (`Database_Schema_Design.md` §5), seeded by migration with `email`, `MRN`, `international phone` and `national phone` (grammars in §4).

- **Resolution (step 5 of §2):** a field's `validation_type` that is not empty and not one of the four built-in structured types MUST name a registry row; the value is valid when it fully matches that row's pattern. The API compiles each pattern once at load (`regexp.Compile`, RE2 — no catastrophic backtracking, so validation cost stays bounded for adversarial input) and wraps it as `\A(?:pattern)\z` so anchoring in the stored pattern is belt-and-braces, not load-bearing.
- **Extension:** adding a type is an insert into `validation_types` (a migration or DB operation); no code or schema change, mirroring the languages rule (REQ-DB-031). There is no write endpoint in phase 1; the designer lists available types read-only via `GET /api/v1/validationTypes` (REQ-API-104), which also serves the patterns for advisory client-side hints (server-side validation stays authoritative, REQ-VAL-002).
- **Guards:** a registry row named after a built-in structured type MUST NOT shadow it (REQ-DB-033); deleting a seeded (`builtin = 1`) row is not supported while any field references it; a pattern that fails to compile makes the entry unusable — the API rejects assigning it at design time with a machine-readable reason (§9) and application-logs the compile failure (REQ-TECH-016).
- **Interaction:** registry types never take `validation_min`/`max` (REQ-VAL-020) and are subject to the choice-field rule first, exactly like the built-ins (REQ-VAL-022/023). Fields whose type is one of the four seeded identifier-shaped entries preset the field's `direct_identifier` flag (REQ-EXP-020).

## 5. Free-form Text Content Policy (REQ-VAL-030/031/032)

A single, centralized policy is applied to every free-form text value (a `text` field with no `validation_type`, and the free-text portion of any value) at the storage choke point, identically on all entry paths (REQ-VAL-001/004). It runs after the type validator (step 6 of §2) and before the write. It is deterministic and stateless.


### 5.1 Pipeline

| Step | Rule | On failure |
|---|---|---|
| 1 | value is valid UTF-8 | `CONTENT_INVALID` |
| 2 | C0 control characters (U+0000–U+001F) are rejected **except** tab (U+0009) and line feed (U+000A); carriage return (U+000D) is accepted and normalized to line feed | `CONTENT_INVALID` |
| 3 | length ≤ `MAX_VALUE_BYTES` (default: no application cap; the storage type `TEXT`/`LONGTEXT` is the bound — REQ-VAL-021/ASM-VAL-2; a finite cap MAY be set in configuration, see `System_Configuration_Design.md`) | `CONTENT_INVALID` (only when a cap is configured) |
| 4 | HTML sanitization per §5.2 (strip disallowed markup, preserve text) | — (never errors; transforms) |

The result of step 4 is the stored value. Steps 1–3 reject with `CONTENT_INVALID` (a step-7-style per-field error, all-or-nothing per record, §2).

### 5.2 HTML allowlist and sanitization (normative)

**Allowed elements** (the final list, resolves ASM-VAL-5): `a`, `b`, `br`, `code`, `em`, `i`, `li`, `ol`, `p`, `s`, `strong`, `u`, `ul`, `blockquote`.

**Attributes:** none are allowed except `href` on `a`. The `href` value MUST be an absolute URI whose scheme is `http`, `https`, or `mailto`; any other scheme (or a relative URL) causes the `href` attribute to be removed (the link text is kept).


**Sanitization algorithm** (applied at store):
1. Parse the value as HTML (tolerant parse).
2. Walk the resulting tree:
   - Keep elements whose tag is in the allowlist.
   - On `a`, keep only `href` (scheme-checked per above); drop all other attributes.
   - On any other allowed element, drop all attributes.
   - On a disallowed element that **carries display text** (e.g. `span`, `div`, `font`, `table`, `td`, `h1`–`h6`): replace the element by its flattened text content (the tag is stripped, the words are kept as plain text).
   - On a disallowed element that **carries executable or external content** — `script`, `style`, `noscript`, `iframe`, `frame`, `object`, `embed`, `applet`, `link`, `meta`, `base`, `form`, `input`, `button`, `select`, `textarea` — remove the element **and** its entire content (nothing is preserved).
3. Decode HTML entities in the surviving text; re-escape on serialization (`&`, `<`, `>`, and quotes) so the stored string is inert.
4. Serialize the cleaned tree to the canonical stored string.

**Worked examples:**

| Stored input | Stored output |
|---|---|
| `hello <b>world</b>` | `hello <b>world</b>` |
| `<div>note: <a href=\"https://x.no\">link</a></div>` | `note: <a href=\"https://x.no\">link</a>` |
| `<a href=\"javascript:alert(1)\">x</a>` | `x` |
| `<script>steal()</script>ok` | `ok` |
| `<img src=x onerror=alert(1)>` | `` (removed, no text) |
| `a &amp; b <script>bad()</script>` | `a &amp; b` |

**Rendering (REQ-VAL-031):** the stored value is allowlist HTML and MAY be rendered as HTML in the UI (data entry form, record view, record history, audit views). **All other** user-supplied content (choice values, field labels, record names, user names, audit fields) is escaped server-side and never rendered as HTML (REQ-TECH-020). The `Content-Security-Policy` header is the backstop (REQ-TECH-020).

### 5.3 CSV formula-injection neutralization (REQ-VAL-032)

Applied **at CSV serialization only** (export, REQ-API-029), never at store and never for JSON: for each cell value `v`, after trimming leading whitespace,
- if `v` is empty → emit empty;
- else if `v` begins with `=` or `@` → emit `'` + `v`;
- else if `v` begins with `+` or `-` **and** `v` is not itself a valid number (integer/floating-point grammar, §4) → emit `'` + `v`;
- otherwise emit `v` unchanged.

This blocks spreadsheet formula injection (`=CMD`, `@SUM(...)`, `+-x`) while leaving legitimate numeric data such as `-5` and `+3.14` intact. The stored value is unaffected; only the CSV cell is prefixed. (Refines REQ-VAL-032's "e.g. single-quote prefix" to avoid corrupting real numbers.)

## 6. Calculated Fields (GD-11, REQ-VAL-033…038)

### 6.1 Expression grammar

A `calculated` field carries an expression (stored in `fields.calculation`, REQ-DB-013) over other fields of the same project. Tokens:

- **Field reference:** `[<unique_event_name>][<field_name>]`
- **Numeric constant:** integer or floating-point literal (§4 grammar)
- **Operators:** `+`, `-`, `*`, `/`
- **Grouping:** parentheses

**Precedence and associativity:** standard arithmetic — `*` and `/` bind tighter than `+` and `-`; all four are left-associative; parentheses override. Whitespace is ignored. Examples: `[e1][a] + [e1][b] * 2`; `([e1][a] - [e1][b]) / [e2][c]`.

### 6.2 Design-time validation (creation and update)

An expression is rejected (design-time error, REQ-VAL-034, REQ-API-065/069) if:
1. it is not well-formed (parse/precedence error); or
2. any reference names a field that does not exist, is not a value-carrying field (description/header), or is not active at the referenced event (not present in the instrument-event mapping of that event, REQ-DB-012); or
3. the reference graph would introduce a **cycle** — a calculated field may reference another calculated field only while the dependency graph stays acyclic (REQ-VAL-035). The API builds the directed graph (calculated field → referenced calculated fields) and rejects a cycle.

A related design-time invariant: deleting a field (REQ-API-070) that is referenced by an existing calculated expression MUST be rejected while the reference is in use — the expression is updated first, keeping the invariant that every reference names an active field (REQ-VAL-034).

On acceptance the API (re)populates `calculated_dependencies` for the field (REQ-DB-030 support) so recomputation can find affected fields without re-parsing every expression.

### 6.3 Evaluation (runtime)

Evaluation produces a single value (or empty) for one record, in the transaction that triggered it.

1. **Resolve** each reference `[event][field]` to the record's stored value at that (record, event, instrument, instance) position — the instrument and instance of the calculated field being evaluated (empty string if absent).
2. **Coerce** each operand to a number per the §4 grammars (integer or floating point); a choice field's stored code is used as-is (numeric by construction, REQ-VAL-022).
3. **Evaluate** with the §6.1 precedence. If any operand is missing, empty, or non-numeric, or the divisor is 0, the result for that position is **empty** — not an error (REQ-VAL-038): the triggering import still succeeds (its own value was valid), and the evaluation problem is application-logged (REQ-TECH-016), never returned to the caller as an import failure.
4. **Store** the result at each (record, event, instrument, instance) position where the calculated field is active (REQ-VAL-037, REQ-DB-030), overwriting the prior value. Format: the shortest exact decimal, no trailing zeros, no exponent (Go `strconv.FormatFloat(v, 'f', -1, 64)`); integer operands under `+ - *` yield an integer string.
5. **Audit** every recomputation per REQ-AUD-023: triggering user, project, record, calculated field, old and new values, UTC timestamp. The JSON shape of the `details` payload is owned by `Audit_Logging_Design.md`.

The recomputation runs in the transaction that triggered it (REQ-VAL-037, REQ-API-095); a rollback of that transaction rolls back the recomputation with it.

**Triggers** (each in the transaction of the triggering write):

| Trigger | Scope of recomputation |
|---|---|
| a referenced (event, field) value is changed or cleared for that record | all calculated fields that (transitively) reference that (event, field), found via `calculated_dependencies` (REQ-DB-030), evaluated in topological order of the dependency graph (acyclic by REQ-VAL-035) |
| a calculated field's expression changes (REQ-API-069) | all records of the project, same transaction (ASM-VAL-4) |
| a record is deleted (REQ-API-036) | none — the calculated values are removed with the record |

**Worked examples** (record with `a = 5`, `b = 2`, `c = 0`, `d` unset, `n = "abc"`):

| Expression | Result |
|---|---|
| `[e1][a] + [e1][b] * 2` | `9` |
| `([e1][a] - [e1][b]) / [e1][c]` | empty (division by zero; application-logged) |
| `[e1][d] * 3` | empty (missing operand) |
| `[e1][n] + 1` | empty (non-numeric operand) |
| `[e1][a] / [e1][b]` | `2.5` |

### 6.4 Design-time test (REQ-API-096)

`POST /api/v1/projects/{id}/records/{record}/fields/{fid}/test` evaluates the field's expression — or a draft expression supplied in the body — against the record's current stored values and returns the result with explicit flags for each evaluation problem (missing/empty referenced value, non-numeric operand, division by zero); it MUST NOT store anything. It shares the evaluation semantics of §6.3 and is the designer's "test calculation" action (REQ-UI-021).

## 7. Branching Logic (GD-13, REQ-VAL-029/040)

Branching logic is **display-only**: it decides which fields and which instruments are shown in the data entry form and on the survey page (GD-9). It MUST NOT affect import, export, or audit (DEV-VAL-5): the API applies no branching logic on any data path, and a value entered for a field whose logic currently evaluates false is stored normally. Expressions are stored in `fields.branching_logic` / `instruments.branching_logic` (REQ-DB-013), exposed in the data dictionary metadata, and evaluated by the UI (REQ-UI-025/028).

### 7.1 Grammar

Tokens (whitespace ignored):

- **Field reference:** `[<unique_event_name>][<field_name>]`
- **Constants:** a numeric literal (§4 grammar) or a double-quoted string (backslash escapes `\"` and `\\` only)
- **Comparison operators:** `=`, `!=`, `<`, `>`, `<=`, `>=`
- **Functions:** `text_contains(ref, "substring")`, `is_blank(ref)`, `is_not_blank(ref)`
- **Logical:** `&&` (AND), `||` (OR) — final per ASM-VAL-6
- **Grouping:** parentheses

**Precedence** (tightest to loosest): function call → comparison → `&&` → `||`; parentheses override. Examples: `[ev][age] >= 18 && [ev][consent] = 1`; `([ev][status] = "done" || is_blank([ev][status]))`; `text_contains([ev][notes], "follow-up")`.

### 7.2 Design-time validation

A field's or instrument's (REQ-VAL-040) branching expression is rejected with a machine-readable reason (REQ-VAL-029; the designer displays the reason, REQ-UI-021) if:

1. it is not well-formed (parse error, unbalanced parentheses, misplaced operator); or
2. a reference names a field that does not exist, is not value-carrying (description/header), or is not active at the referenced event (REQ-DB-012); or
3. a function is misspelled or has the wrong number of arguments.

No cycle rule applies: branching logic produces no value, so no dependency graph arises.

### 7.3 Evaluation semantics (normative)

The semantics below are the single normative definition; every evaluator (data entry form, survey page) MUST implement them (REQ-VAL-004).

- **Operand resolution.** A reference resolves to the record's stored value (empty if absent at the referenced event). A string constant that exactly matches a choice label of a choice field (dropdown/radio/matrix row) is resolved to that choice's code before comparison — the stored value remains the code (REQ-VAL-022). A radio reference used by itself evaluates to `1` when selected (a value is present) and `0` when not (REQ-VAL-029).
- **Comparison.** If either operand is a missing/empty referenced value → `0` for every operator (ASM-VAL-6). Otherwise: both numeric → numeric comparison; both valid dates/date-times (in the reference's `validation_format` or canonical form, §4.1) → chronological comparison of **absolute instants** (each value's wall time interpreted with its stored collection offset — GD-16, REQ-VAL-041); otherwise → lexicographic (byte-wise UTF-8) comparison.
- **Truthiness.** A reference used outside a comparison: empty → `0`; non-empty → `1` (ASM-VAL-6). For choice fields this is exactly "selected → `1`, not selected → `0`" (REQ-VAL-029), regardless of the code value; for numeric text fields the value `0`/`0.0` → `0`.
- **Functions.** `is_blank(ref)` → `1` if the referenced value is missing/empty, else `0`; `is_not_blank(ref)` → its negation; `text_contains(ref, s)` → `1` if `s` is a substring of the referenced value, else `0` (missing/empty → `0`).
- **Logical.** `&&` → `1` iff both sides are `1`; `||` → `1` if either side is `1`; sides that are not `1` coerce to `0`.

A field (resp. instrument) is displayed only while its expression evaluates to `1` (REQ-VAL-029/040); a hidden instrument hides all of its fields regardless of their own logic (REQ-VAL-040).

### 7.4 Re-evaluation

The display state MUST be re-evaluated whenever a referenced field's value changes — in the data entry form (on change of any referenced field) and on the survey page (on load, and after each submit) (REQ-VAL-029, REQ-UI-025).

## 8. Required Flag and Partial Records (REQ-VAL-028, ASM-VAL-3)

- `fields.required` is exposed in the data dictionary metadata and the designer (REQ-DB-013, REQ-UI-021).
- **Import:** a missing required value MUST NOT reject an import — partial records are first-class (ASM-VAL-3, DEV-VAL-5); completion is tracked by the record status dashboard, not enforced at import.
- **Data entry form:** required fields are presented as such and validated before submission (REQ-VAL-028, REQ-UI-025) — client-side only (advisory, REQ-VAL-002).

## 9. Data Dictionary Rules at Design Time (REQ-VAL-010…014)

When creating or updating a field (REQ-API-068/069) or instrument (REQ-API-065/101), the API validates the dictionary entry itself:

| Attribute | Rule |
|---|---|
| `field_name` | `^[a-z0-9_]+$` (REQ-VAL-011); unique within the project (REQ-VAL-012); longer than 26 characters accepted — the warning after 26 is UI-only (REQ-VAL-013); reserved (design decision — the flat export row would otherwise be ambiguous, REQ-API-028): `redcap_event_name`, `redcap_repeat_instrument`, `redcap_repeat_instance` MUST NOT be used |
| `validation_type` | empty, a built-in structured type (`integer`, `floating point`, `date`, `datetime`), or an existing validation-type registry name whose pattern compiles (§4.2; REQ-VAL-010/042) |
| `validation_min`/`max` | if present, a valid number per the field's type (integer → §4 integer grammar; floating point → decimal grammar) and min ≤ max; ignored for other types (REQ-VAL-020) |
| `choices` | `code$label##code$label` (schema §1); codes non-empty, unique, numeric (REQ-VAL-022); labels non-empty |
| `validation_format` | (date/datetime) composed of the §4.1 tokens — `Y`, `m`, `d`, plus `H`, `i` for datetime — with the specified separators; empty → the §4.1 defaults |
| `calculation` | present and validated per §6.2 for `calculated` fields; empty for all other types |
| `branching_logic` | if present, validated per §7.2 |

Every rejection is a design-time error with a machine-readable reason (REQ-API-065/069; the designer displays the reason, REQ-UI-021).

**Rename (REQ-VAL-014):** renaming a field atomically renames its stored values in the same transaction (a single key update on the EAV layout, DEV-VAL-4) and is audit-logged (REQ-API-043); the new name is validated by the rules above before the write.

## 10. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| ASM-VAL-1 (canonical date forms) | dates `YYYY-MM-DD±HH:MM`; date-times `YYYY-MM-DD HH:MM±HH:MM` — the canonical form carries the timezone of collection (GD-16, REQ-VAL-041): offset from the import-supplied zone (browser tz / `tz` parameter) else `APP_TIMEZONE`; the API converts between the field's accepted format and the canonical form on store and on export; values are never converted to UTC (§4.1) |
| ASM-VAL-2 (free-text length cap) | no application cap beyond the storage type by default (REQ-VAL-021); a finite cap MAY be set in configuration — the key is defined in `System_Configuration_Design.md` (§5.1) |
| ASM-VAL-3 (partial records) | `required` is advisory at import; completion is tracked, not enforced (§8) |
| ASM-VAL-4 (calculated-field details) | `*`/`/` before `+`/`-`, all left-associative, parentheses override; acyclic dependency graph; value stored at each active position; expression change → project-wide recomputation (§6) |
| ASM-VAL-5 (free-form text details) | allowlist final (§5.2); disallowed markup is stripped with its text preserved; only `href` on `a`, schemes `http`/`https`/`mailto` |
| ASM-VAL-6 (branching-logic details) | numeric / chronological / lexicographic comparison; missing referenced value → `0`; truthiness for bare references; `&&`/`||` as the logical operators; function set final (§7) |

## 11. Open Items

| Item | Owner |
|---|---|
| finite value-length cap configuration key (if set) | `System_Configuration_Design.md` |
| `details` JSON shape of the calculated-field recomputation audit event (REQ-AUD-023) | `Audit_Logging_Design.md` |
| advisory client-side validation attributes per field type (mirror of §4) | `User_Interface_Design.md` |