# Data Export and Anonymization Plan

This document outlines the strategy for exporting data from the clinical study management system, including anonymization techniques to protect patient privacy. The master spec (VISION_AND_REQUIREMENTS.md) defines the export permissions and the end provision; the anonymization mechanics below are system design decisions.

## Export Formats
- CSV: For easy import into statistical software (e.g., R, SPSS).
- JSON: For API-based data transfer.

## Anonymization Rules
At the `export_de_identified` level, the following rules apply:
- Direct Identifiers: fields flagged `direct_identifier` are removed. The flag is set by the user on **any** field (preset when the validation type is email, MRN, or a phone type) — it is not a fixed list of name/email/MRN columns (REQ-EXP-020).
- Dates: Shifted by a random number of days (consistent for each patient) to preserve relative timelines while obscuring exact dates.
- Free Text: Fields marked as "free text" are excluded unless explicitly approved for export.

## Field-Level Anonymization
- Personal Information Checkbox: Each field in an instrument will have a "Personal Information" flag.
- Anonymized Export: Fields marked as personal information will be replaced with a salted hash.
- Access Levels: **moved, then returned narrower.** This plan gave each *instrument* three levels (Full / Anonymized / None); GD-2 moved them to the *arm* as one export level per arm — `export_none`, `export_de_identified`, `export_no_identifiers`, `export_full` — with per-field control left to the `personal_information` and `export_approved` flags (DEV-EXP-1, REQ-AUTH-017/018). As of 2026-10-03 the export level is held **per (instrument, event) pair** under the arm default (REQ-AUTH-069), so per-instrument control is back — as a role grant on a pair, not as a separate scheme, and with the fourth level retained.

## Access Control
Export is gated by the acting user's (or token holder's) **export level for the (instrument, event) pair** of each exported column, one of the four above; there is no separate "export all" / "export anonymized" permission (REQ-AUTH-017/069). Each column carries its own transformation, a pair at `export_none` contributes no columns, and only a request with no permitted pair in scope is rejected — the cross-arm minimum of the per-arm model went with that granularity (REQ-EXP-003).

## End-of-Project Provision
- The end provision (delete or anonymize) is decided per project but is **no longer a `projects` attribute** — GD-17 removed it from the table along with the option flags. The owner records it as data in an ordinary instrument (e.g. `DataTransferProjects`) or outside the system; the REK end date remains project metadata (REQ-DB-032, BR-009).
- At the end of the project (REK-END / end date), the stored data is handled according to the provision: deleted, or anonymized using the rules above. It is a one-shot action per project, executed by an installation administrator (`is_admin`) via `POST /api/v1/projects/{id}/end-provision` with `{ "provision": "delete" | "anonymize" }`; a second execution conflicts (the `project_ended` audit event is the idempotency state) and the response reports the affected record and value counts (REQ-EXP-040).
