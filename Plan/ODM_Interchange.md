# ODM Interchange Plan

This document outlines how a CLARA **project** — one study's structure and record data, not the platform — is exchanged with other clinical systems in CDISC ODM XML (Operational Data Model). The master spec (VISION_AND_REQUIREMENTS.md, "Project interchange (CDISC ODM XML)") states the capability; the sensitivity machinery it inherits is specified in `Plan/Data_Export_Anonymization.md` and this plan does not restate it.

## Interchange Format
- CDISC ODM XML in either the 1.3 namespace (`http://www.cdisc.org/ns/odm/v1.3`, `ODMVersion="1.3.2"`) or the 2.0 namespace (`http://www.cdisc.org/ns/odm/v2.0`, `ODMVersion="2.0"`), snapshot documents, UTF-8. Export offers both versions and defaults to 1.3; import detects either from the uploaded XML (REQ-ODM-010/011/064).
- One document per project: `Study` + one `MetaDataVersion` + `ClinicalData`. Metadata-only is valid for an empty project (REQ-ODM-020).
- A CLARA extension namespace carries what ODM has no element for — branching logic, the three sensitivity flags, transfer copy instructions, matrix grouping (REQ-ODM-051, DEV-ODM-3). Standard consumers ignore it; a file without it still imports (REQ-ODM-052). Because ODM 1.3.2 defines no extension point a foreign element can occupy (`xs:any` appears nowhere in it), CLARA also publishes a wrapper schema that fills the spec's empty extension groups, so a strict validator can check a CLARA document rather than being handed one it must reject (REQ-ODM-055).
- SAS dataset and variable names are written only where they fit ODM's own `sasName` type — 8 characters, `[A-Za-z_][A-Za-z0-9_]*` — and omitted otherwise; the machine name rides on `@Name`, which is what import reads back (REQ-ODM-028). 2.0 declares no SAS attribute: the group's dataset name becomes `@DatasetName` there and an item carries only `@Name`. Likewise a repeating pair: neither dialect can state it on the reference a pair is made of — 1.3.2 gives `FormRef` no `Repeating` attribute and 2.0 has no `FormRef` at all — so both name the event's repeating groups in `clara:Event/@repeatingForms`, and import still reads a partner's `FormRef/@Repeating` (REQ-ODM-029).
- Each dialect writes its own document, not one shared shape wearing a different namespace (REQ-ODM-015): 2.0 names the study with `Study/@StudyName`/`@ProtocolName` and has no `GlobalVariables`; requires `@Type="text/plain"` on every `TranslatedText`; spells an instrument as a top-level `ItemGroupDef @Type="Form"` (a matrix group inside it is `Section`) because it declares no `FormDef`; references groups from the event with `ItemGroupRef` and writes no `Protocol`, whose `StudyEventRef` it removed; spells a repeating group `Simple` where 1.3 spells it `Yes`; and carries an item's value in a `<Value>` child where 1.3 uses `@Value`. Import reads whichever spelling it meets; the 2.0 files CLARA wrote before this rule existed are not read, because they were never valid 2.0.

## Surfaces
- `GET /api/v1/projects/{id}/export/odm` — streamed download (REQ-ODM-025, REQ-TECH-011).
- `odm_version=1.3|2.0` selects the export dialect; omitted values keep the existing 1.3 output (REQ-ODM-064).
- `odm_content=project_and_data|project_only` selects what the file holds: the study with its records, or the study definition alone with no `ClinicalData` element at all — for handing a partner a CRF to review without a record value leaving the installation. Omitted means `project_and_data`, an unknown name is 400 (REQ-ODM-066).
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
- The importer selects the dialect from the root namespace and accepts both 1.3 and 2.0; the same project structure/value round trip applies to either (REQ-ODM-011/050/064, DEV-ODM-7).
- Instrument names matching REQ-DB-011 are preserved; invalid or overlong ODM form names are normalized to that contract, with a `name_renamed` warning (REQ-ODM-050).
- Nothing is trusted because it arrived in an ODM document: type and choice validation, date canonicalization, content policy and length bounds all run as on any other write (REQ-ODM-042), and no expression in the file is ever executed (REQ-ODM-047).
- The new project's name comes from the request or `StudyName`; a collision is 409, never a rename of an existing project (REQ-ODM-044). Values entered in the import form override matching project metadata from the ODM extension; optional blanks clear those values (REQ-ODM-006).

## Mapping (summary — normative tables in the design document)
| CLARA | ODM |
|---|---|
| project | 1.3: `Study` + `GlobalVariables`; 2.0: `Study` with `@StudyName`/`@ProtocolName` and a `Description` child, no `GlobalVariables`. Organization, investigator, data manager and REK number travel in `clara:Project`, because 1.3.2's `Protocol` has no sponsor, investigator or reference element (§3.1) |
| arm + event (GD-15) | `StudyEventDef Repeating="No" Type="Scheduled"` — there is no `EventDef` and no `Standard` type in ODM — with the arm in `@Category`; OID encodes `unique_event_name` (`…_arm_<n>`). Canonical order: 1.3 states it on `Protocol/StudyEventRef/@OrderNumber`, and 2.0, which removed that reference, in the declaration order of the `StudyEventDef`s (§3.2) |
| instrument (+ repeat per pair, REQ-DB-047) | 1.3: `FormDef` with one `ItemGroupRef` to the instrument's `ItemGroupDef`. 2.0 has no form element, so the instrument **is** its `ItemGroupDef`, marked `@Type="Form"`, holding a `@Type="Section"` matrix group by reference (§3.3). The *per-pair* designation is a different thing and neither dialect carries it on the reference: both name the event's repeating groups in `clara:Event/@repeatingForms` (REQ-ODM-029) |
| field | `ItemDef` with lowercase `@DataType` (`text`, `integer`, `float`, `date`, `datetime` — ODM 1.3.2's own enumeration), `@Length` only where validation supplies one, and min/max as two `RangeCheck`s whose `CheckValue` holds the bound as **text** — neither dialect declares a `Value` attribute on it (§3.4, REQ-ODM-065) |
| choices (`code$label##…`) | `CodeList DataType="text"` with one `CodeListItem/@CodedValue` and its `Decode/TranslatedText` per choice in stored order; never `EnumeratedItem`, which carries no label (§3.5) |
| calculated field | `MethodDef Type="Computation"` — the word both dialects enumerate, not "Calculation" — with the mandatory `MethodSignature` child (empty: no parameters) and the expression as `FormalExpression` text in 1.3 or its `Code` child in 2.0, referenced by `ItemRef/@MethodOID` and never evaluated (§3.8, REQ-ODM-065) |
| record identifier (GD-8) | `ItemRef/@KeySequence="1"` with `@Mandatory="Yes"`; `SubjectKey` per §4.1 |
| checkbox choice `<f>___<code>` (REQ-DB-042) | its own `ItemDef DataType="integer"` in the instrument's `ItemGroupDef`, sharing `CL_BOOLEAN`; the parent row is display metadata and stays in `clara:Choice` (§3.6, §3.7) |
| EAV row (record, event, instance) | 1.3: `ClinicalData/SubjectData/StudyEventData/FormData/ItemGroupData`; 2.0: the same without `FormData`, which 2.0 removed (`@ItemGroupRepeatKey` on the group either way) (REQ-ODM-027) |

## Reporting and Audit
- Import answers counts plus an `importNote` problem list — warnings for what was converted or dropped, failures for what stopped the structure or one record (REQ-ODM-060). Codes in `Design/ODM_Interchange_Design.md` §7.
- `odm_export` and `odm_import` audit events: level, scope, counts, options — never a value or a document body (REQ-ODM-061); refused calls write nothing (REQ-AUD-004).

## Out of scope for this step
Transactional/delta ODM, an existing-project update through the staging set, validating against a schema **at runtime** (ASM-ODM-5 — the program stays pure Go, `CGO_ENABLED=0`), CRF page layout and `LogicalPageDef`, and DAG membership and audit history in `AdminData` (DEV-ODM-4). Conformance checking itself is not out of scope, only its location: `tools/odm_conformance.py` validates an exported document against the published CDISC schema for each dialect plus CLARA's extension wrapper (REQ-ODM-055), and `ci/run.sh` runs it as an **advisory** step — it reports findings and skips with a notice where lxml is not installed, rather than holding the build for a dev tool.
