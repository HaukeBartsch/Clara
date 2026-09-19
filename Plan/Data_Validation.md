Data Validation Plan

This document outlines the validation rules for the clinical study management system, ensuring data integrity across all inputs.

Field Types & Validations

1. Text Fields
- Integer: Must be a whole number (optional min/max range).
- Floating Point: Must be a decimal number (optional min/max range).
- Email: Must match standard email format.
- Medical Record Number (MRN): Must be exactly 11 digits.
- Date: Must match specified format (e.g., Y-m-d).

2. Choice Fields (Dropdowns / Radio Buttons)
- Validation: Value must match one of the predefined numeric codes.

3. Matrix Fields
- Validation: Each row must have a valid selection from the shared choices.

4. Non-Value Fields
- Description and header fields carry text only and are not validated for values.

Field Names
- Lower-case alphanumeric with underscores; a warning is shown after 26 characters.
- A field name is unique within a project.

Implementation
- Client-Side: Real-time feedback using JavaScript and HTML5 validation attributes.
- Server-Side: Strict validation in the Go API before saving to the database. The PHP layer only renders the UI and delegates all data access to the API.
