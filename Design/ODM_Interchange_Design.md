# ODM Interchange — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/ODM_Interchange_Requirements.md` (REQ-ODM-…; source plan `Plan/ODM_Interchange.md`), on top of the export pipeline of `Design/Data_Export_Anonymization_Design.md` §4 and the write path of `Design/Data_Validation_Design.md`\
**Date:** 2026-10-23

## 1. Purpose and Normative Scope

Fixes the element-level mapping between CLARA's project structure / record data and CDISC ODM XML in both directions, the CLARA extension namespace, the way this format reaches the sensitivity pipeline without re-implementing it, and the import build order. It is normative for the mapping tables (§3, §4), the extension vocabulary (§5), the integration points (§6), the problem codes (§7) and the loss list (§8).

Element and attribute spellings below follow **CDISC ODM 1.3.2** (`ODM1-3-2-foundation.xsd`, namespace `http://www.cdisc.org/ns/odm/v1.3`) as published, notably where later editions or common paraphrase differ: the root carries `FileOID` (not `ODID`), an item is referenced by `ItemData/@ItemOID` (not `@Name`), a codelist attaches through the child `CodeListRef` (there is no `codelistOID` attribute on `ItemDef`), 1.3.2 has no `NullFlavor`, and a method's body is `FormalExpression` (not `Formulation`). A deviation from this document is a requirements-level change (AGENTS.md).

## 2. Document Envelope

```xml
<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3"
     FileOID="CLARA-<projectID>-<YYYYMMDDTHHMMSSZ>"
     CreationDateTime="2026-10-23T14:03:11Z"
     FileType="Snapshot"
     ODMVersion="1.3.2"
     SourceSystem="CLARA"
     SourceSystemVersion="<build>">
  <Study OID="CLARA-<projectID>">
    <GlobalVariables>
      <StudyName>…</StudyName>
      <StudyDescription>…</StudyDescription>
      <ProtocolName>…</ProtocolName>
    </GlobalVariables>
    <MetaDataVersion OID="CLARA-<projectID>-MDV" Name="…" Description="…">
      <!-- Protocol, StudyEventDef, FormDef, ItemGroupDef, ItemDef, CodeList, MethodDef
           in the order §3.1 fixes -->
    </MetaDataVersion>
  </Study>
  <ClinicalData StudyOID="CLARA-<projectID>" MetaDataVersionOID="CLARA-<projectID>-MDV">
    <!-- one SubjectData per record, §4 -->
  </ClinicalData>
</ODM>
```

Rules:

- **Attribute values.** `FileOID` = `CLARA-<project_id>-<UTC creation stamp>` (unique per download, not reused); `CreationDateTime` UTC with the `Z` suffix (GD-7); `FileType="Snapshot"` always (ASM-ODM-1); `ODMVersion="1.3.2"`; `SourceSystem="CLARA"`. No `Granularity`, `Archival` or `PriorFileOID` is written.
- **OID scheme (O-1).** Every OID is derived from the CLARA name it stands for, never from a database id, so two exports of the same project agree and a partner's mappings survive a reload:

  | Object | OID | Name |
  |---|---|---|
  | Study | `CLARA-<project_id>` | — |
  | MetaDataVersion | `CLARA-<project_id>-MDV` | the project name |
  | event | `EVT_<unique_event_name>` | `unique_event_name` (GD-15) |
  | instrument | `FRM_<instrument_name>` | `instrument_name` |
  | instrument's item group | `IG_<instrument_name>` | `instrument_name` |
  | matrix group | `IG_<matrix_group>` | `matrix_group` |
  | field | `IT_<field_name>` | `field_name` |
  | checkbox choice | `IT_<field_name>___<choice_code>` | the derived column name (REQ-EXP-053) |
  | codelist | `CL_<field_name>` | `field_name` |
  | method (calculated) | `MTH_<field_name>` | `field_name` |

  CLARA names are already lower-case alnum plus underscore per field, so an OID is well-formed NMTOKEN; a name that is not (an event label with punctuation, imported from elsewhere) is sanitized by replacing every run of non-`[A-Za-z0-9._-]` with `_`, and the original travels in the extension (§5), which is also what import restores. OID length is capped at 200 characters with the tail replaced by a hash of the full name, keeping it inside the schema's 256 and collision-free in practice.
- **Text.** Every `Description`/`Question`/`Decode` holds `<TranslatedText xml:lang="…">`; the language is the catalog language of the label the field carries today (English source of truth, REQ-UI-008) and one element per item — CLARA stores one label per field, not a per-language matrix.
- **Escaping** is `encoding/xml`'s own; nothing builds XML by string concatenation (§9).

## 3. Metadata Mapping (export direction, normative)

Order inside `MetaDataVersion` follows the schema's sequence: `Protocol`, `StudyEventDef`*, `FormDef`*, `ItemGroupDef`*, `ItemDef`*, `CodeList`*, `MethodDef`*.

### 3.1 Study and project metadata

| CLARA (`projects`) | ODM |
|---|---|
| `project_name` | `GlobalVariables/StudyName`; also `MetaDataVersion/@Name` |
| `organization`, `pi_name`, `dm_name` | `clara:Project` extension (§5) — ODM 1.3.2's `Protocol` carries no sponsor/investigator element, and inventing an `AdminData/UserRef` for a person would model a study user that does not exist |
| `rek_number` | `clara:Project/@rekNumber` |
| `rek_start_date`, `rek_end_date`, `start_date`, `end_date` | `clara:Project` attributes (dates as `YYYY-MM-DD`) |
| `record_naming` | `clara:Project/@recordNaming` |
| — | `ProtocolName` repeats the project name (required by nothing, useful to readers that show it) |

### 3.2 Events and arms

CLARA's arm/event pair (REQ-DB-011, GD-15) has no arm element in ODM, so the arm travels twice — visibly for a human reader, machine-readably in the extension:

| CLARA (`events`) | ODM |
|---|---|
| one event | `<StudyEventDef OID="EVT_<unique_event_name>" Name="<unique_event_name>" Repeating="No" Type="Scheduled" Category="Arm <arm_num> — <arm name>">` |
| canonical event order (GD-15: `period`, then `position`) | `Protocol/StudyEventRef/@OrderNumber`, assigned by that ordering, with `Mandatory="No"` (CLARA does not mark a visit mandatory) |
| `event_name` label, `arm_num`, `period`, `safe_region_start`, `safe_region_end` | `clara:Event` extension (§5) |

`Type` is `Scheduled` for every CLARA event: ODM 1.3.2's enumeration is `Scheduled \| Unscheduled \| Common` and CLARA has no unscheduled-visit notion; a repeating *event* is not a thing here either, so `Repeating="No"` (an imported `Repeating="Yes"` or `Type="Unscheduled"` is accepted, converted to No/Scheduled, and warned as `event_flattened`).

The instrument↔event mapping (`instrument_events`, REQ-DB-012) is the `FormRef` list of the event: `<StudyEventDef …><FormRef FormOID="FRM_<instrument>" Mandatory="No" OrderNumber="<position>"/></StudyEventDef>` — the association ODM itself defines. `@Mandatory` stays `No`: CLARA's required-ness lives on fields, not on a form being collected at an event.

### 3.3 Instruments and item groups

| CLARA | ODM |
|---|---|
| instrument | `<FormDef OID="FRM_<name>" Name="<name>" Repeating="No">` with one `ItemGroupRef` to `IG_<name>` |
| instrument's fields, in `position` order | `<ItemGroupDef OID="IG_<name>" Name="<name>" Repeating="No" SASDatasetName="<name>">` and one `ItemRef ItemOID="IT_<field>" Mandatory="Y\|N" OrderNumber="<position>"` per exportable field (REQ-DB-013) |
| pair is repeating (`instrument_events.is_repeating`, REQ-DB-047) | `@Repeating="Yes"` on the **`ItemGroupRef`'s target** `ItemGroupDef`, and `@ItemGroupRepeatKey` on each data row (§4) — ODM marks the group repeating, not the form |
| matrix group (`matrix_group`, REQ-DB-014) | its own `ItemGroupDef IG_<matrix_group>` (non-repeating), referenced from the instrument's `FormDef` after the main group; each row field is an `ItemRef` of that group with its own `OrderNumber` |
| record identifier (GD-8) | `ItemRef/@KeySequence="1"` on the identifier field, plus `Mandatory="Y"` — import restores GD-8 from it (§6.2) |

`checkbox`, `description`, `header` and `transfer` fields are handled by §3.6 and §3.7; every other field type gets one `ItemDef`.

### 3.4 Fields → ItemDef

`ItemDef/@Name` is the CLARA machine name (round-trip anchor, O-1) and `@SASFieldName` repeats it; the human-readable label goes to `Description/TranslatedText`, the field note to `Question/TranslatedText`.

| CLARA `field_type` / validation | `ItemDef/@DataType` | Extras |
|---|---|---|
| `text`, no validation | `text` | — |
| `text`, `integer` | `integer` | `@Length` from the digit count of `validation_max` when both bounds are integers, else none |
| `text`, `floating point` | `float` | `@SignificantDigits` left unset (CLARA stores no scale) |
| `text`, `date` | `date` | format in `clara:Field/@format` (`validation_format`) |
| `text`, `datetime` | `datetime` | as above |
| `text`, `email` / `MRN` / `international phone` / `national phone` (REQ-VAL-043) | `text` | `clara:Field/@validation` carries the registry name — ODM has no identifier-type vocabulary, and a `Role="Identifier"` would not survive a partner's import |
| any other registry validation type (REQ-VAL-042) | `text` | `clara:Field/@validation`, warned as `validation_dropped` |
| `notes` | `text` | — |
| `dropdown`, `radio` | `text` + `CodeListRef` | one `CodeList` per field (§3.5) |
| `yesno` | `text` + `CodeListRef` → shared `CL_BOOLEAN` | choices are fixed `1$Yes##0$No` (REQ-DB-043) |
| `matrix` row | as the row's own validation, with the field's own `CodeListRef` when it has choices | lives in its matrix group (§3.3) |
| `calculated` | by validation type (`integer`/`float`/`text`) | `ItemRef/@MethodOID="MTH_<field>"` and one `MethodDef` (§3.8) |
| `checkbox` | *not* an `ItemDef` for the parent — see §3.6 | |
| `description`, `header`, `transfer` | no `ItemDef` at all — §3.7 | |

`validation_min` / `validation_max` of a numeric or date field become `<RangeCheck SoftHard="Hard" Comparator="GE"><CheckValue Value="<min>"/></RangeCheck>` and the `LE` twin (1.3.2 puts bounds in `CheckValue`, not in `LowerBound`/`UpperBound` attributes). `required=1` → `ItemRef/@Mandatory="Y"`.

### 3.5 Choices → CodeList

One `CodeList OID="CL_<field_name>" Name="<field_name>" DataType="text"` per field with choices, holding one `<CodeListItem CodedValue="<code>" OrderNumber="<n>"><Decode><TranslatedText xml:lang="…"><label></TranslatedText></Decode></CodeListItem>` per choice in stored order (the `code$label##` grammar of REQ-DB-013). `EnumeratedItem` is not used: it carries no `Decode`, and every CLARA choice has a label. A codelist shared by nothing is not emitted; the shared `CL_BOOLEAN` (`1`/`Checked`, `0`/`Unchecked`, matching REQ-EXP-053's words) is emitted once per document when any checkbox field exists. `@SASFormatName` is left unset — CLARA has no format-name concept.

### 3.6 Checkbox expansion

Per REQ-EXP-053/REQ-DB-042 the export works on derived columns, and so does ODM: a `checkbox` field contributes **no** `ItemDef` of its own but one per choice — `<ItemDef OID="IT_<field>___<code>" Name="<field>___<code>" DataType="integer">` with `Description` = `"<field label>, <choice label>"`, referenced from the instrument's `ItemGroupDef` in choice order directly after the parent's dictionary position, and sharing `CL_BOOLEAN`. A document that has both a checkbox parent item and its derived items would describe a column the data never carries; the parent row is display metadata and stays in the extension (§5).

### 3.7 Fields with no value of their own

`description`, `header` and `transfer` fields carry no data row (REQ-DB-050) and ODM has no display-only item, so they are **not** emitted as `ItemDef`s — a phantom item would advertise a variable no consumer will ever receive. They travel in the extension (§5) with their `OrderNumber`, which is what preserves them and their position across a CLARA→CLARA round trip; a standard consumer sees an instrument without static text, which is the honest reading (REQ-ODM-053).

### 3.8 Calculated fields

`<MethodDef OID="MTH_<field_name>" Name="<field_name>" Type="Calculation"><Description><TranslatedText>…</TranslatedText></Description><FormalExpression><TranslatedText xml:lang="en"><calculation as stored></TranslatedText></FormalExpression></MethodDef>`, referenced from the field's `ItemRef/@MethodOID`. The expression is carried **as text**: import stores it after grammar validation and never evaluates it (REQ-ODM-047). Its dependencies (`calculated_dependencies`) are recomputed by the ordinary write path, not read from the file.

### 3.9 What the metadata leaves out

Columns the applied level removes (§4.2 steps 1 and 3 of `Data_Export_Anonymization_Design.md`) get **no** `ItemDef`, no `ItemRef` and no `CodeList`: the document describes exactly what it can deliver, so a consumer never reads an item whose values are absent because a level withheld them. The field *names* of removed columns still appear in the data dictionary export to anyone with design access (REQ-API-153), so omitting their items is not what protects a value — but a self-consistent document is, and ODM has no "declared, never populated" state worth inventing.

## 4. Clinical Data Mapping

```xml
<ClinicalData StudyOID="CLARA-7" MetaDataVersionOID="CLARA-7-MDV">
  <SubjectData SubjectKey="1042">
    <StudyEventData StudyEventOID="EVT_screening_arm_1">
      <ItemGroupData ItemGroupOID="IG_demographics">
        <ItemData ItemOID="IT_age" Value="57"/>
      </ItemGroupData>
    </StudyEventData>
  </SubjectData>
</ClinicalData>
```

| CLARA (`data` EAV, REQ-DB-015…020) | ODM |
|---|---|
| one record | one `SubjectData/@SubjectKey` (§4.1) |
| `(record, unique_event_name)` | one `StudyEventData/@StudyEventOID` (`EVT_…`) |
| one instance of a repeating instrument (REQ-DB-047) | one `ItemGroupData/@ItemGroupRepeatKey` = `repeating_instance_number`; the non-repeating case omits the attribute entirely rather than writing `1` |
| one stored value | `<ItemData ItemOID="IT_<field_name>" Value="<stored value>"/>` — codes, not labels (REQ-ODM-022) |
| no stored value | **no element** (REQ-ODM-023): 1.3.2's `@IsNull` stays unwritten and nothing is invented for a cleared value |
| instrument completion (`<form>_complete`) | dropped with notice — ODM has no per-form completion state; `FormData` would be the carrier and it models collection instances, not a review status (REQ-ODM-053) |
| DAG of the record | dropped in this step: `AdminData/UserRef` names study users, which CLARA's DAG groups are not; carried later only if an owner asks (REQ-ODM-053) |

The optional `FormData` level is **not** written (one `ItemGroupData` per group directly under `StudyEventData`), and is accepted on import when a partner emits it.

### 4.1 SubjectKey

`SubjectKey` is the record identifier value when the pipeline delivered that column (REQ-ODM-030) — every level an `is_admin` holds (REQ-AUTH-023). Where the level removed it, the exporter assigns `S1`, `S2`, … in first-appearance order and never a hash or any derived form of the stored id (REQ-ODM-031, O-5): §5.1's hash is a salt-recoverable pseudonym, which a level that deletes the identifier does not license, and a surrogate cannot collide with a real record id in a mixed file. The consequence — surrogates do not identify a record across exports — is stated in the response header `X-CLARA-ODM-Key: surrogate` so an operator cannot mistake one file's `S17` for another's.

### 4.2 Streaming order (O-2)

The exporter writes `<ODM>`, `<Study>` and the whole `MetaDataVersion` first (it comes from the cached dictionary, one pass), then opens `ClinicalData` and emits one `<SubjectData>` per record as its rows arrive, closing the element after each; it closes the document only after the last record. Nothing is buffered beyond one record's rows, which keeps REQ-TECH-011 true of a format that has a mandatory closing tag. A client disconnect leaves a truncated document — deliberately not repaired with a synthesized close, so a partial file cannot pass as complete (REQ-ODM-025).

## 5. CLARA Extension Namespace

Namespace `urn:x-clara:odm-ext:v1`, nested in the element the data belongs to, inside the schema's element-extension hook. A conforming ODM reader ignores it; a CLARA reader restores what ODM cannot express (REQ-ODM-051). Only these elements exist:

| Element | Where | Attributes / content | Carries |
|---|---|---|---|
| `clara:Project` | `Study` | `organization`, `piName`, `piEmail`, `dmName`, `dmEmail`, `rekNumber`, `rekStartDate`, `rekEndDate`, `startDate`, `endDate`, `recordNaming` | §3.1 |
| `clara:Event` | each `StudyEventDef` | `armNum`, `eventName`, `period`, `safeRegionStart`, `safeRegionEnd`, `oidSource` (the unsanitized name when the OID was rewritten, O-1) | §3.2 |
| `clara:Arm` | `MetaDataVersion` | `armNum`, `name`, `position` | arms that have no events of their own |
| `clara:Form` | each `FormDef` | `isSurvey`, `branchingLogic`, `position` | GD-9, GD-13 |
| `clara:Field` | each `ItemDef` | `fieldType`, `validationType`, `validationFormat`, `matrixGroup`, `sectionHeader`, `personalInformation`, `directIdentifier`, `exportApproved`, `annotation`, `branchingLogic`, `position` | REQ-DB-013/042/044, REQ-EXP-020/023, GD-13, DEV-DB-2 |
| `clara:StaticField` | the instrument's `ItemGroupDef` | `fieldName`, `fieldType` (`description` \| `header` \| `transfer`), `label`, `instructions`, `position`, plus the sensitivity attributes of `clara:Field` | §3.7, REQ-DB-050 |
| `clara:Choice` | a checkbox parent's extension point | `fieldName`, `choices` (the stored grammar), plus its sensitivity attributes | §3.6's parent row |

Rules: the extension **adds**, never contradicts — where both spell a value (a field's `position` against `ItemRef/@OrderNumber`), the ODM spelling wins on import and the extension is a hint; booleans are `0`/`1`; text attributes hold raw text, escaped by the encoder. An importer treats a missing extension as "not stated" and applies CLARA's own creation defaults — a field is *not* personal, *not* a direct identifier, *not* export-approved (REQ-ODM-052, fail-safe per REQ-EXP-021).

## 6. Integration

### 6.1 Export reuses the pipeline (O-6)

The ODM writer never reads the `data` table and never transforms a value. It consumes the **same row stream** the CSV and JSON exports produce, obtained from `dataapi`:

```go
// dataapi: RunExportRows streams an export as transformed rows in the flat
// layout (one JSON object per row to w) for a writer that is neither csv nor
// json — the ODM exporter. Values pass the whole §4.2 pipeline exactly as they
// would for the tabular formats; nothing here transforms anything.
func (h *Handler) RunExportRows(w io.Writer, r *http.Request, s ExportSpec) error
```

It is `streamExport` with `Enc="json"`, `Type=flat`, driven through a header-discarding writer: the level resolution, per-pair ranks, column selection, filterLogic gate (REQ-EXP-054), DAG scope, checkbox expansion, hashing and date shifting all stay where they are. The ODM exporter reads row objects as a token stream (`json.Decoder` over an `io.Pipe`, constant memory) and maps each row's keys: the record identifier key → `SubjectKey` (or the surrogate, §4.1), `redcap_event_name` → `StudyEventOID`, `redcap_repeat_instrument`/`redcap_repeat_instance` → the repeating group and its `@ItemGroupRepeatKey` (`record_export.go:685-689` writes exactly these), every other key → one `ItemData` under `IT_<key>` when the value is non-empty. Keys with no item in the metadata (a column the level kept but this document does not describe) are skipped and counted in the audit line, never guessed at.

`RunExportRows` returns the status the pipeline chose **before** any byte was forwarded, so a 403 for a scope with no permitted pair (REQ-AUTH-069) becomes the administration API's own error shape instead of an XML document that starts `<?xml`. After the first byte the stream is the answer: a mid-stream store failure truncates (as CSV does, §4.2).

The audit row is written by the pipeline (`writeExportAudit`, `record_export.go:1235`) with one extra detail key the spec carries (§9), so an ODM download appears as `odm_export` and not as an anonymous `export`.

**Equivalence guard.** Because the format reaches the same data through a second door, a test asserts that for one fixture project the value under each `ItemData` equals the JSON export's value for the same (record, event, instance, column) — including hashed personal fields and shifted dates at a de-identified rank — so the two doors cannot drift apart unnoticed.

### 6.2 Import build order

1. **Parse** (§9 limits first): decode into an intermediate model — `Study`/`GlobalVariables`, one `MetaDataVersion` (events, forms, groups, items, codelists, methods), and `ClinicalData` grouped by `SubjectKey`. Unknown elements are recorded as problems, not errors (REQ-ODM-053). A second `MetaDataVersion` in the document is a problem (`mdv_ignored`) and only the first builds.
2. **Resolve** the design against CLARA's rules: names sanitized and de-duplicated, `field_name` uniqueness per project enforced (a name used twice across instruments is renamed with a suffix and warned, since `fields(project_id, field_name)` is unique), matrix groups collected, checkbox parents reconstructed from derived items when no parent extension row exists, GD-8 taken from `KeySequence="1"` or the first field of the first form.
3. **Validate** the design through the existing design path: the same `stagedDesign` snapshot and validation view the instrument import uses (`admin/staged_design.go`, REQ-API-152), so calculation expressions, branching references, choice grammars and the record-identifier rule are checked by the code that owns them rather than a second implementation.
4. **Build** — one transaction: project row (name from `project_name` or `StudyName`, 409 on collision, REQ-ODM-044), arms, events, instruments, fields, mapping, repeat flags, then `MaterializePairGrants`. Any failure here rolls the whole thing back and answers 400 with the problem list (REQ-ODM-040).
5. **Load records** — after commit, one transaction per record in file order: resolve event → `unique_event_name`, instrument → owning form, instance number, then per value run `validate.ValidateValue` against the field's spec and `validate.ApplyContentPolicy` (the same calls `dataapi/record_import.go:501-520` makes), canonicalize dates, and write with `Store.UpsertDataValueTx` / clear with `DeleteDataValueTx`. A value that fails fails **its record** and the rest continue (REQ-ODM-041); an unknown item is a per-record problem, not a failure, matching how the data import reports unknown keys.
6. **Report** — §7's counts plus problems, and audit (§9).

`dry_run=1` stops after step 3 and answers the same report with nothing written (REQ-ODM-046).

### 6.3 Import validation is shared, not re-decided (O-7)

Value semantics live in `validate`, storage in `db`, and the ODM importer orchestrates: it decides *which* (event, instrument, instance, field) a value belongs to and then asks the same questions the record-import path asks. What this means concretely — an invalid choice code, a malformed date, an over-long value — is decided by `validate` and reported with its existing codes, so "ODM accepts something the API refuses" is structurally prevented rather than tested into agreement (REQ-ODM-042). The guard test asserts that one bad value produces the same `CODE` string here as on `POST /api/`.

## 7. Routes, Report and Problem Codes

| Route | Contract |
|---|---|
| `GET /api/v1/projects/{id}/export/odm` | `is_admin`; streamed `application/xml; charset=utf-8`, `Content-Disposition: attachment; filename="<project-slug>_odm_<YYYY-MM-DD>.xml"` (the dictionary export's sanitizer, REQ-API-153); no query options in this step — the file is the project, at the caller's level (REQ-ODM-026) |
| `POST /api/v1/projects/import/odm` | `is_admin`; body `application/xml` (the document itself, not base64 in JSON — an XML upload has no reason to be wrapped); query/form parameters `project_name` (optional), `dry_run` (`1`); answer 201 with the report on success, 200 for a dry run |

Report shape (both calls' import answer; mirrors the instrument import's `importNote` list, REQ-ODM-060):

```json
{"project_id":7,"project_name":"Cohort 2026","dry_run":false,
 "arms":1,"events":3,"instruments":4,"fields":52,
 "records_added":118,"records_updated":0,"records_failed":2,
 "problems":[{"file":"study.xml","row":"S17","field":"age","code":"value_invalid","message":"TYPE_INVALID — not an integer"}]}
```

| Code | Level | Meaning |
|---|---|---|
| `not_odm` | fatal | root element or namespace is not ODM 1.3 (REQ-ODM-011) |
| `dtd_forbidden` | fatal | a `DOCTYPE` or entity declaration is present (REQ-ODM-013) |
| `too_deep`, `too_large` | fatal | structural bound exceeded (§9, REQ-ODM-014) |
| `duplicate_oid`, `dangling_ref`, `empty_itemgroup`, `no_metadataversion` | fatal | self-contradictory document (REQ-ODM-054) |
| `name_renamed` | warn | a field/event/instrument name collided or was illegal and was rewritten, with the old→new pair |
| `type_mapped`, `validation_dropped`, `event_flattened` | warn | an ODM construct was converted (`float`→`text`, an unknown registry validation type dropped, a repeating/unscheduled event flattened) |
| `element_ignored`, `attribute_ignored` | warn | recognized ODM (or another namespace's) content CLARA does not model — the §8 list |
| `mdv_ignored` | warn | more than one `MetaDataVersion`; only the first built |
| `value_invalid`, `unknown_item`, `unknown_event`, `record_failed` | per record | the value/row problems of step 5, with `validate`'s code inside the message |

## 8. Loss List (normative summary)

Dropped **with a warning**, never silently (REQ-ODM-053): instrument completion status; DAG membership and any `AdminData`; `AuditRecord`/`TechnicalAudit` history; `Annotation`s and `Comment`s beyond a field's own note; `LogicalPageDef`, `Presentation`, `ArchiveLayout`; `Protocol` content past study name/description (`Objective`, `TimePoint`, `SubjectGroup`); `ValueListDef` and any `RangeCheck` that is not a plain min/max; `ImputationMethod`; multi-language `TranslatedText` for languages the installation does not carry (the first one wins, warned per element); `MeasurementUnitRef`; anything in a namespace CLARA does not read.

## 9. Limits, XML Safety and Audit

- **Size.** The import body is bounded by the administration API's 32 MiB (`importBodyLimit`, REQ-API-144) with the same 400 the instrument import answers; the reverse proxy's own bound (32 MiB in both reference configs) stays the outer limit, so no `deploy/` change is needed.
- **XML safety.** A `DOCTYPE` declaration or any entity declaration is refused before parsing (`dtd_forbidden`, REQ-ODM-013) — `encoding/xml` does not expand general entities, and refusing anyway keeps a future parser change from turning this into an expansion path. Element nesting is bounded (64) and the token count is bounded by the byte limit, so a deeply-nested or explosive document costs one request (REQ-ODM-014). Only `utf-8` is accepted; a BOM is tolerated and stripped (as the CSV reader does).
- **Output.** All XML is written with `encoding/xml` token writers (`xml.Encoder`/`Flush`), never string concatenation, so no value can escape its element or forge an attribute regardless of what a record holds.
- **Imported text is untrusted** from the moment it is stored: labels, notes and choice text pass `validate.ApplyContentPolicy` and render escaped (REQ-ODM-062).
- **Audit.** `odm_export` — `{surface:"api", sensitivity:<ladder name>, records:N, items:N, skipped_columns:N}`; `odm_import` — `{project_id, dry_run, arms, events, instruments, fields, records_added, records_failed, problems:N}`. Never a value, a label, or any part of the document body (REQ-AUD-014); refused calls write nothing (REQ-AUD-004). Both codes join the catalog of `Audit_Logging_Design.md` §3.5.
- **No new configuration variables** — the inventory stays `System_Configuration_Design.md` §3 (REQ-CFG-003).

## 10. Decisions and Traceability

### 10.1 Local design decisions

| ID | Decision |
|---|------|
| O-1 | OIDs derive from CLARA names, not database ids, and a sanitized OID keeps its source in `clara:Event/@oidSource` / the field's own name: two exports of one project agree, so a partner's mappings survive a reload. |
| O-2 | Metadata first, then one record at a time, closing each as it goes — REQ-TECH-011 holds for a format with a mandatory close, and a truncated file stays recognizable as one. |
| O-3 | Raw codes only; the raw/label axis belongs to the tabular exports and an ODM consumer resolves `CodedValue` against the shipped `CodeList` (REQ-ODM-022). |
| O-4 | A private extension namespace for what ODM cannot express, additive and ignorable, with "missing means CLARA's default" defined (REQ-ODM-051/052, DEV-ODM-3). |
| O-5 | `SubjectKey` is the record identifier where the level delivers it; otherwise a per-file surrogate, never a hash of it (§4.1, REQ-ODM-031). |
| O-6 | The exporter consumes the shared row stream (`RunExportRows`) instead of reading storage or re-running the ladder; the importer shares `validate` and `db` rather than restating their rules (REQ-ODM-021/042). |
| O-7 | Round-trip fidelity is gated by a test — export, import into a fresh project, compare structure and values — rather than by validating against an XSD at runtime, which the pure-Go constraint rules out (ASM-ODM-5). |
| O-8 | `DOCTYPE` refused, depth bounded, everything written through `encoding/xml`, imported text run through the content policy (§9). |

### 10.2 Requirement traceability

| Requirement | Section |
|---|---|
| REQ-ODM-001…005 (surfaces, gating, no UI) | §7, §9 |
| REQ-ODM-010…014 (dialect, refusal, limits) | §2, §9 |
| REQ-ODM-020…026 (export content) | §3, §4, §6.1 |
| REQ-ODM-030/031 (subject key) | §4.1 |
| REQ-ODM-040…047 (import effects) | §6.2 |
| REQ-ODM-050…054 (fidelity, losses) | §5, §8, §10.1 O-7 |
| REQ-ODM-060…063 (reporting, audit, privacy) | §7, §9 |
