# Data Validation Plan

This document outlines the validation rules for the clinical study management system, ensuring data integrity across all inputs.

## Field Types & Validations

### 1. Text Fields
- Integer: Must be a whole number (optional min/max range).
- Floating Point: Must be a decimal number (optional min/max range).
- Email: Must match standard email format.
- Medical Record Number (MRN): Must be exactly 11 digits.
- International Phone: Must match an international number such as "+47 55566777".
- National Phone: Must match a national number such as "55566777".
- Date: Must match specified format (e.g., Y-m-d).
- Extensible: Additional validation types can be added as named regular expressions stored in the database (validation_types table); email, MRN, and both phone types are seeded entries of that registry.

### 2. Choice Fields (Dropdowns / Radio Buttons)
- Validation: Value must match one of the predefined codes. Codes may be text — letters, digits, `-` and `_` (REDCap projects use codes such as `PECTMRI`); checkbox codes only lower-case letters, digits and `_`.
- A choice list may be written in REDCap's form `code, label | code, label` (split on `|`) wherever it is supplied as text; it is stored in this system's encoding.
- Yes/No fields (`yesno`, REQ-DB-043) are radio buttons with the fixed codes `1` = Yes and `0` = No.
- Checkboxes store one value per choice under `<field_name>___<code>` (REQ-DB-042): `1` checks a box, `0` or empty clears it; any other value is rejected.
- In a calculation an unchecked box reads as `0`, so a sum of boxes counts the checked ones (REQ-VAL-038).
- Inside a repeating instrument an expression reads its own instrument's fields at its own event from the same instance, every other reference from the record's first instances; a calculated field of a repeating instrument is computed per instance (REQ-VAL-045).

### 3. Matrix Fields
- Validation: Each row must have a valid selection from the shared choices.

### 4. Notes Fields
- A `notes` field (REQ-DB-044) takes multi-line free text: no validation type, the same content policy as a free-text `text` field.

### 5. Non-Value Fields
- Description and header fields carry text only and are not validated for values.

### 6. Branching Logic
- REDCap's spellings work as written: `'value'`, `and` / `or`, `<>`, `[field(code)]` for a checkbox box; `[field]` and `[event][field]` both work, `[field]` meaning the first event.
- As in REDCap, `[field] = ''` is true when the field is blank, and an unchecked checkbox box reads as `0`.

## Field Names
- Lower-case alphanumeric with underscores; a warning is shown after 26 characters.
- A field name is unique within a project.

## Implementation
- Client-Side: Real-time feedback using JavaScript and HTML5 validation attributes.
- Server-Side: Strict validation in the Go API before saving to the database. The PHP layer only renders the UI and delegates all data access to the API.
