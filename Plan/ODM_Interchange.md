# ODM Interchange Plan

This document outlines how a CLARA **project** — one study's structure and record data, not the platform — is exchanged with other clinical systems in CDISC ODM XML (Operational Data Model). The master spec (VISION_AND_REQUIREMENTS.md, "Project interchange (CDISC ODM XML)") states the capability; the sensitivity machinery it inherits is specified in `Plan/Data_Export_Anonymization.md` and this plan does not restate it.

## Interchange Format
- CDISC ODM XML in the `http://www.cdisc.org/ns/odm/v1.3` namespace, snapshot documents, UTF-8, produced to 1.3.2 element usage for reach (REQ-ODM-010, DEV-ODM-5).
- One document per project: `Study` + one `MetaDataVersion` + `ClinicalData`. Metadata-only is valid for an empty project (REQ-ODM-020).
- A CLARA extension namespace carries what ODM has no element for — branching logic, the three sensitivity flags, transfer copy instructions, matrix grouping (REQ-ODM-051, DEV-ODM-3). Standard consumers ignore it; a file without it still imports (REQ-ODM-052).

## Surfaces
- `GET /api/v1/projects/{id}/export/odm` — streamed download (REQ-ODM-025, REQ-TECH-011).
- `POST /api/v1/projects/import/odm` — creates a new project; `dry_run=1` answers the report without writing (REQ-ODM-046).
- Administration API and web UI, `is_admin` only: a whole-study copy is bulk data movement, above what a project role takes or injects (REQ-ODM-001/002). The Export page offers the ODM download to installation administrators; the Control Panel's New Project flow accepts an ODM upload and metadata fields (REQ-ODM-005/006, DEV-ODM-6). The REDCap-compatible data API keeps its `csv`/`json` set untouched (REQ-ODM-003).

## Export
- Values are stored codes; the raw/label axis belongs to the tabular exports and an ODM consumer resolves `CodedValue` against the shipped `CodeList` (REQ-ODM-022).
- Absent values stay absent — no empty `ItemData`, no invented `NullFlavor` (REQ-ODM-023).
- The document is written through the **same** export pipeline as CSV and JSON: per-pair levels, the §4.2 step order, field hash, date shift, checkbox expansion, DAG record scope. Nothing in this format re-implements a rung of that ladder (REQ-ODM-021). Because the route is `is_admin`-only, the level actually delivered today is `export_full` (REQ-AUTH-023, DEV-ODM-2) — wired, not hardcoded.
- `SubjectKey` is the record identifier where the level delivers it; below `export_full` the identifier column is gone and ODM still needs a key, so a per-file surrogate (`S1`, `S2`, …) takes its place and carries no meaning across exports (REQ-ODM-030/031).

## Import
- Structure first, atomically: project row, arms, events, instruments, fields, mapping, repeat flags in one transaction — no half-built project (REQ-ODM-040).
- Records second, one transaction per record, each keeping or failing on its own, exactly as the data import path behaves (REQ-ODM-041).
- Nothing is trusted because it arrived in an ODM document: type and choice validation, date canonicalization, content policy and length bounds all run as on any other write (REQ-ODM-042), and no expression in the file is ever executed (REQ-ODM-047).
- The new project's name comes from the request or `StudyName`; a collision is 409, never a rename of an existing project (REQ-ODM-044). Values entered in the import form override matching project metadata from the ODM extension; optional blanks clear those values (REQ-ODM-006).

## Mapping (summary — normative tables in the design document)
| CLARA | ODM |
|---|---|
| project | `Study` + `GlobalVariables`; REK number as a `Protocol/Reference` |
| arm + event (GD-15) | `EventDef Type="Standard"`, OID encoding `unique_event_name` (`…_arm_<n>`) |
| instrument (+ repeat per pair, REQ-DB-047) | `FormDef`, `ItemGroupDef Repeating` |
| field | `ItemDef` (DataType/Length from the validation type, `RangeCheck` for min/max) |
| choices (`code$label##…`) | `CodeList` with `CodeListItem` decodes |
| calculated field | `MethodDef MethodType="Formula"` + `ItemDef/MethodRef` |
| record identifier (GD-8) | `ItemRef KeySequence="1" Mandatory="Y"`; `SubjectKey` |
| checkbox choice `<f>___<code>` (REQ-DB-042) | its own `ItemDef` in the parent's `ItemGroupDef` |
| EAV row (record, event, instance) | `ClinicalData/SubjectData/ItemGroupData` (`@ItemGroupRepeatKey`) |

## Reporting and Audit
- Import answers counts plus an `importNote` problem list — warnings for what was converted or dropped, failures for what stopped the structure or one record (REQ-ODM-060). Codes in `Design/ODM_Interchange_Design.md` §7.
- `odm_export` and `odm_import` audit events: level, scope, counts, options — never a value or a document body (REQ-ODM-061); refused calls write nothing (REQ-AUD-004).

## Out of scope for this step
Transactional/delta ODM, an existing-project update through the staging set, XSD-validating against a schema at runtime, CRF page layout and `LogicalPageDef`, and DAG membership and audit history in `AdminData` (ASM-ODM-1/5, DEV-ODM-4).
