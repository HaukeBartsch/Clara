Data Export and Anonymization Plan

This document outlines the strategy for exporting data from the clinical study management system, including anonymization techniques to protect patient privacy. The master spec (Endpoints.md) defines the export permissions and the end provision; the anonymization mechanics below are system design decisions.

Export Formats
- CSV: For easy import into statistical software (e.g., R, SPSS).
- JSON: For API-based data transfer.

Anonymization Rules
When "Export Anonymized" is selected, the following rules apply:
- Direct Identifiers: Names, emails, and Medical Record Numbers (MRNs) are completely removed.
- Dates: Shifted by a random number of days (consistent for each patient) to preserve relative timelines while obscuring exact dates.
- Free Text: Fields marked as "free text" are excluded unless explicitly approved for export.

Field-Level Anonymization
- Personal Information Checkbox: Each field in an instrument will have a "Personal Information" flag.
- Anonymized Export: Fields marked as personal information will be replaced with a salted hash.
- Access Levels: Instruments will support three permission levels:
    - Full Access: View all data as entered.
    - Anonymized: View data with personal fields hashed.
    - No Access: Instrument data is completely hidden.

Access Control
- Full Export: Requires "export all" permission.
- Anonymized Export: Requires "export anonymized" permission.

End-of-Project Provision
- Each project is created with an end provision: delete or anonymize.
- At the end of the project (REK-END / end date), the stored data is handled according to the provision: deleted, or anonymized using the rules above.
