# ODM Interchange — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source plan:** `Plan/ODM_Interchange.md`, `VISION_AND_REQUIREMENTS.md` (master spec)\
**Status:** Approved for development handoff\
**Date:** 2026-10-23

## 1. Purpose

Defines the requirements for exchanging a **project** — one study's structure and record data, not the platform — with other clinical systems in **CDISC ODM XML** (Operational Data Model, `http://www.cdisc.org/ns/odm/v1.3`). It fixes which surface carries the format, what a CLARA ODM document contains, how CLARA's design and values map onto ODM elements in both directions, what a round trip promises to preserve, and what it admits to losing.

The surrounding machinery is specified elsewhere and is not restated here:

| Concern | Specified in |
|---|------|
| the level pipeline an export runs through (removal, hashing, date shift) | `Data_Export_Anonymization_Requirements.md` (REQ-EXP-020…035), `Design/Data_Export_Anonymization_Design.md` §4 |
| which columns a caller may see, per (instrument, event) pair | `Authentication_Authorization_Requirements.md` (REQ-AUTH-017/018/023/069) |
| value grammars, choice codes, content policy, length caps | `Data_Validation_Requirements.md` (REQ-VAL-016/021/022/024/032) |
| project structure the design half describes | `Database_Schema_Requirements.md` (REQ-DB-011…014/042/047) |
| streaming and body bounds | `Technology_Stack_Requirements.md` (REQ-TECH-011), `API_Endpoints_Requirement.md` (REQ-API-144) |
| audit of an export or an import | `Audit_Logging_Requirements.md` (REQ-AUD-004/011/014) |

`Design/ODM_Interchange_Design.md` is normative for the element mapping, the CLARA extension namespace, and the algorithms (O-1…O-8).

## 2. Surfaces and Gating

| ID | Requirement |
|---|------|
| REQ-ODM-001 | A project MUST be exportable to, and importable from, ODM XML through two administration-API endpoints: `GET /api/v1/projects/{id}/export/odm` and `POST /api/v1/projects/import/odm` (BR-008). Both are `/api/v1/*` routes and therefore never publicly routable (REQ-TECH-018, REQ-AUTH-014). |
| REQ-ODM-002 | Both endpoints MUST be restricted to an installation administrator (`is_admin`, REQ-AUTH-023); `project_admin` MUST NOT suffice. A whole-study interchange file is a bulk copy of a study's structure and data, which exceeds what a project-level role may take or inject (DEV-ODM-2). |
| REQ-ODM-003 | The REDCap-compatible data API MUST NOT offer the format: `content=record` keeps its `csv`/`json` encoding set, and an unknown `format`/`returnFormat` value keeps falling back to CSV as it does today (REQ-API-016/017). A caller that wants ODM uses the administration API. The two formats of the master spec's "Export formats" clause stay exactly as they were on the data API (DEV-ODM-1). |
| REQ-ODM-004 | Import MUST create a **new** project and MUST NOT modify an existing one — not its structure, not its records (DEV-ODM-4). A file whose metadata describes changes to a project the installation already has is loaded as its own new project; nothing in ODM identifies a CLARA project, and guessing from a study name would be worse than asking. |
| REQ-ODM-005 | No web UI route is part of this feature: the endpoints are reached through the administration API only. `*(Not implemented (added 2026-10-23).)*` The Export page (`web/views/project/export.php`, REQ-UI-020) keeps its two formats and gains no ODM option until an owner decision opens it to a non-administrator audience, since the page is reachable by members who hold no `is_admin` (REQ-ODM-002). |

## 3. Dialect

| ID | Requirement |
|---|------|
| REQ-ODM-010 | A CLARA ODM document MUST use the ODM 1.3 namespace `http://www.cdisc.org/ns/odm/v1.3`, MUST declare `FileType="Snapshot"` and a `CreationDateTime` in UTC, and MUST be UTF-8. The producer targets ODA/ODM 1.3.2 element usage so that readers built for 1.3.2 accept the file (DEV-ODM-5). |
| REQ-ODM-011 | An import MUST accept a document in that namespace and reject any other root element or namespace with 400; `Version`, `ODID` and file-level attributes CLARA does not model are read for the audit line and otherwise ignored. |
| REQ-ODM-012 | `FileType="Transactional"` MUST NOT be required of a producer (CLARA writes snapshots), and an importer MUST NOT reject a transactional file: it reads the data elements it understands and reports the rest (REQ-ODM-060). |
| REQ-ODM-013 | A document MUST NOT carry a `DOCTYPE` declaration or any general/parameter entity reference; both are refused with 400 before parsing, whatever the parser would have done with them (entity expansion is not a feature an interchange reader should exercise on clinical data). |
| REQ-ODM-014 | The document's size MUST stay within the administration API's import bound of 32 MiB (REQ-API-144); over it, 400 with the same answer the instrument import gives. Nesting depth is bounded and an unbounded-depth document is refused rather than parsed to exhaustion. |

## 4. Export Content

| ID | Requirement |
|---|------|
| REQ-ODM-020 | An export of a project MUST contain one `Study` (identifying the CLARA project) with its `GlobalVariables`, one `MetaDataVersion` describing the project's current structure, and — when the project has records this caller may read — `ClinicalData` carrying them. A project without records exports a valid metadata-only document rather than an error or an empty `ClinicalData`. |
| REQ-ODM-021 | The export MUST run through the **same** sensitivity pipeline as the CSV and JSON exports: the same per-(instrument, event) level resolution, the same §4.2 step order, the same field hash and date-shift derivation, the same checkbox expansion and record scoping (REQ-EXP-002/003/012, REQ-AUTH-069). It MUST NOT re-implement any part of that ladder in its own code, and MUST NOT bypass it because the caller is an administrator — a level read added later must transform ODM output the way it transforms every other format (DEV-ODM-2 makes `export_full` the level actually delivered today). |
| REQ-ODM-022 | Values MUST leave as stored codes: choice codes, not labels (`REQ-VAL-022`), the stored decimal separator, dates in canonical form. The raw/label axis belongs to the tabular exports and does not apply here — an ODM consumer resolves a `CodedValue` against the `CodeList` it ships with (O-3). |
| REQ-ODM-023 | A field with no stored value for a row MUST be absent from that row's `ItemGroupData`; neither an empty `ItemData` nor an invented null indicator (`@IsNull`, the 1.3.2 spelling — there is no `NullFlavor` in this dialect) stands in for it, and a value cleared on purpose is indistinguishable from one never entered — as it is in storage (REQ-VAL-024). |
| REQ-ODM-024 | A `checkbox` field MUST export as one `ItemDef` per choice under the derived name `<field_name>___<choice_code>` (REQ-EXP-053), and a repeating instrument's instances as separate `ItemGroupData` rows with `@ItemGroupRepeatKey` (REQ-DB-042/047). |
| REQ-ODM-025 | The export MUST be streamed — metadata first, then one record at a time — and MUST NOT materialize the document in memory (REQ-TECH-011). A client that disconnects mid-download leaves no partial state behind, and the audit row is still written. |
| REQ-ODM-026 | The download MUST be `application/xml; charset=utf-8` with a `Content-Disposition: attachment` filename derived from the project name the same way the dictionary export derives its own (REQ-API-153), and MUST carry no byte-order mark (REQ-EXP-016 keeps that to the browser's CSV download). |

### 4.1 The subject key

| ID | Requirement |
|---|------|
| REQ-ODM-030 | `ClinicalData/@SubjectKey` and each `SubjectData/@SubjectKey` MUST be the record identifier value when the applied level delivers that column verbatim (GD-8) — which is the case at every level an administrator holds. |
| REQ-ODM-031 | Where the applied level removes the record identifier (below `export_full`, REQ-EXP-020), ODM still requires a key: the export MUST substitute a **surrogate** assigned in the file's own first-appearance order (`S1`, `S2`, …) and MUST NOT emit the stored identifier, a hash of it, or any other value derived from it. The surrogate groups one record's rows inside one document and means nothing outside it: surrogates are not stable across exports and MUST NOT be read as joinable identifiers (O-5). Hashing was rejected because §5.1's hash is a *pseudonym the installation can re-open with the salt*, which a level that removes the identifier does not license, and because `S<n>` cannot collide with a real record id in a mixed file. |

## 5. Import Content and Effects

| ID | Requirement |
|---|------|
| REQ-ODM-040 | An import MUST create the project row and its structure — arms, events, instruments, fields, instrument–event mapping, repeat flags — in **one transaction**, so that a file that fails halfway leaves no half-built project and no orphan rows (the atomicity rule of `Data_Export_Anonymization_Design.md` §7.2 applied to the design half). |
| REQ-ODM-041 | A document with `ClinicalData` MUST load its records **after** the structure exists, one record per transaction in the file's order, each record keeping or failing on its own: a rejected record MUST NOT roll back the records already stored, and MUST be reported (REQ-ODM-060). This is the data-import rule of REQ-VAL-024/REQ-API-033, not a new one. |
| REQ-ODM-042 | Every imported value MUST pass the same validation pipeline as any other write path — type and choice validation, date canonicalization, content policy, length bounds (REQ-VAL-016/021/022/024) — and MUST NOT be trusted because it arrived in an ODM document. A value that fails validation fails its record; a structure attribute that cannot be represented fails the structure (REQ-ODM-040). |
| REQ-ODM-043 | An imported `ItemData/@Value` is stored as given after canonicalization; a null indicator (`@IsNull="Yes"`, or an item that carries no value at all) MUST NOT create a value and is treated as absent (REQ-ODM-023's inverse). |
| REQ-ODM-044 | The new project's name MUST come from the request (`project_name`), defaulting to `Study/GlobalVariables/StudyName` when absent; it MUST satisfy the existing project-naming rules and MUST be refused with 409 when a project of that name already exists (`projects.project_name` is unique, REQ-DB-006). A CLARA import never renames or reuses an existing project to resolve a collision. |
| REQ-ODM-045 | `StudyOID`, `MetaDataVersionID` and other ODM identifiers are **not** authoritative for CLARA identity: they are recorded in the audit line and otherwise discarded. The record identifier field is the one the document marks (KeySequence/Mandatory on the first instrument's first item, or an explicit CLARA extension), defaulting to the first field of the first instrument as elsewhere (GD-8). |
| REQ-ODM-046 | `dry_run=1` MUST run the whole parse, validation and structure build without writing anything, and answer with the same report shape the live call answers plus the problems found — mirroring the instrument import's dry run (REQ-API-152). A dry run writes no audit row beyond the request's own record of a refused change, since nothing changed. |
| REQ-ODM-047 | Import MUST NOT execute anything it reads: `MethodDef` formulations are read as text for a calculated field's expression and validated by the expression grammar (REQ-VAL-033/037), never evaluated; branching logic is stored under the display-only rule (GD-13, REQ-VAL-029); no value is re-computed at import beyond what the ordinary write path recomputes. |

## 6. Fidelity and Losses

| ID | Requirement |
|---|------|
| REQ-ODM-050 | A CLARA export followed by an import MUST reproduce a project with the same instruments, fields (name, type, validation, required flag, choices and their order), arms, events, mapping, repeat flags and record values. This is the round-trip promise and it is tested as one (O-7). |
| REQ-ODM-051 | Concepts ODM has no home for — `branching_logic`, `personal_information`, `direct_identifier`, `export_approved`, `field_annotation`, `matrix_group`, `transfer` copy instructions, survey flags and instrument order within an arm — MUST be carried in a **CLARA extension namespace** nested in the element they belong to (O-4), so that one CLARA file round-trips completely while a standard consumer sees ordinary ODM and ignores the rest. |
| REQ-ODM-052 | A conforming ODM document that omits the extension MUST still import: what is not stated is not imported, and defaults follow CLARA's own creation defaults (a field is not personal, not a direct identifier, not approved for export — the fail-safe direction of REQ-EXP-021). |
| REQ-ODM-053 | Where ODM carries more than CLARA models, the surplus is dropped **with notice**, never silently: `Protocol` detail beyond study name and description, `AdminData` users, `AuditRecord`/`TechnicalAudit`, `Annotation`s, `LogicalPageDef`, `ValueListDef` ranges beyond a field's min/max, multi-language decodes for languages the installation does not have, and elements of a version CLARA does not read. |
| REQ-ODM-054 | Importing a document whose structure contradicts itself — an `ItemRef` naming no `ItemDef`, an `ItemGroupData` naming no `ItemGroupDef`, an unknown `CodeList` reference, a duplicate OID, a `SubjectData` without items — MUST fail with 400 and a problem list that names the offending element (O-6); nothing is written. |

## 7. Reporting, Audit and Privacy

| ID | Requirement |
|---|------|
| REQ-ODM-060 | Import answers with counts and a **problem list** in the shape the instrument import uses: `importNote{file,row,field,code,message}` rows, split into warnings (non-fatal: something dropped or converted) and failures (fatal to the structure, or per-record for data). Codes are declared in the design document (§7) so a caller can branch on them. |
| REQ-ODM-061 | Every ODM export MUST write an `odm_export` audit event and every import an `odm_import` one, with the applied sensitivity, the scope (project, record counts), and the request's options — and MUST NOT put any record value, label text or imported document body in the details (REQ-AUD-011/014). A refused call (not `is_admin`, malformed document, over-limit) writes neither (REQ-AUD-004). |
| REQ-ODM-062 | Text that arrives in an ODM document is untrusted input from the moment it is stored: it passes the content policy of REQ-VAL-* like any other value and renders escaped everywhere (the existing output-escaping rule), so a `<TranslatedText>` carrying markup cannot become a script in the designer. |
| REQ-ODM-063 | `ANON_SALT` and derived hashes are unaffected by this feature: an ODM export at a hashing level emits the same hash the CSV export emits for the same (project, field, value) (REQ-EXP-030). |

## 8. Assumptions

| ID | Assumption |
|---|------|
| ASM-ODM-1 | Interchange partners exchange **snapshot** documents; transactional ODM (delta loads, `TransactionType` semantics) is out of scope for phase 1 beyond not rejecting such a file. |
| ASM-ODM-2 | One `MetaDataVersion` per document: CLARA's design is live state with no released/working version axis, so there is nothing to version against (a future change-request workflow would add it). |
| ASM-ODM-3 | An installation administrator is trusted with the project's data at every level (REQ-AUTH-023); this document therefore does not treat `is_admin` as a caller to be protected from its own export. |
| ASM-ODM-4 | The ODM file arrives over the internal network or the administration API's authenticated boundary; no encryption-at-rest requirement attaches to a downloaded file beyond the operator's own handling (GD-1, REQ-AUTH-034). |
| ASM-ODM-5 | Pure-Go `encoding/xml` is sufficient: no XSD validation library is admitted (REQ-TECH-002, `CGO_ENABLED=0`), so conformance is enforced structurally by this document's rules and exercised against fixtures rather than a schema validator (O-8). |

## 9. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-ODM-1 | ODM XML is a **third** format where the master spec's "Export formats" names two (CSV raw, CSV labels) — added on the administration API only, leaving the data API's REDCap-compatible set untouched. | Owner instruction (2026-10-23, master spec "Project interchange (CDISC ODM XML)"): export and import of a project in CDISC ODM for interchange with other clinical systems; the master spec's clause governs the tabular data export a REDCap caller sees, which is unchanged. |
| DEV-ODM-2 | The feature is gated to `is_admin` rather than opened to whatever export levels a member holds on the project's pairs. | Owner instruction (2026-10-23): "make this feature specific to the admin user". Consequence: the level an ODM export actually applies is always `export_full` today (REQ-AUTH-023), so REQ-ODM-021 keeps the pipeline wired rather than special-casing full output — a gate widened without re-checking this must not become an untransformed data path. |
| DEV-ODM-3 | CLARA-only design attributes travel in a private extension namespace instead of standard ODM constructs. | CDISC models none of: display-only branching logic (GD-13), the three sensitivity flags (REQ-DB-013), `transfer` copy instructions (REQ-DB-050). Forcing them into `MethodDef`/`PresentFrom` would misrepresent them to a standards consumer and still lose them on import; an ignored namespace is honest, and REQ-ODM-052 keeps the file importable without it. |
| DEV-ODM-4 | Import creates a new project instead of updating an existing one through the staging set (GD-20, REQ-API-152's dry-run/apply pair). | Owner answer (2026-10-23) to the scope question; also structural: ODM names no CLARA project, and a design diff against a live study would drag `breaking.go`'s empirical re-validation of stored values into a format whose own validation surface is already new. A staged ODM update stays available as a later addition without contradicting anything here. |
| DEV-ODM-5 | The producer targets 1.3.2 element usage inside the shared 1.3 namespace rather than ODM 1.4 or 1.3.3-only constructs (`LogicalPageDef`, multi-language extensions). | Interchange reach: 1.3.2 is the widest-implemented revision, and everything in the 1.3 namespace validates against it. Nothing CLARA needs requires 1.3.3; the namespace stays `v1.3` so a reader that knows only 1.3.2 accepts the document. |
| DEV-ODM-6 | No web UI for either direction in this step (REQ-ODM-005). | Follows from DEV-ODM-2: the Export page is a member-facing screen and this feature's audience is `is_admin`. The same reasoning as REQ-CFG-035/036, marked "Not implemented" rather than implied. |
