package admin

// POST /api/v1/projects/import/odm (REQ-API-170, Design/ODM_Interchange_Design.md
// §6.2, §7): an ODM 1.3 or 2.0 document becomes a **new** project — its structure in one
// transaction, its records one transaction each afterwards.
//
// The importer orchestrates; it decides nothing that another component owns. The
// design is built as a stagedDesign snapshot and judged by the designer's own
// rules (validateFieldDesign), so an expression, a branching reference or a choice
// grammar is checked by the code that defines them rather than a second
// implementation (§6.2 step 3, O-7). Every value goes through validate on its way
// in, exactly as the data API's import runs it, which is what makes "ODM accepts
// something the API refuses" structurally impossible rather than merely untended
// (REQ-ODM-042). Nothing in the document is executed: a MethodDef is text that the
// expression grammar judges and storage then holds (REQ-ODM-047).
//
// The order of writes is the order §6.2 fixes — parse, resolve, validate, build,
// load, report — and each boundary is a place where nothing has been written yet,
// so a document that fails answers 400 with its problem list and leaves no
// half-built project behind (REQ-ODM-040/054).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// odmImportReport is the answer of both the live import and its dry run (§7):
// counts, then the problem list in the instrument import's importNote shape.
type odmImportReport struct {
	ProjectID      int64        `json:"project_id"`
	ProjectName    string       `json:"project_name"`
	ODMVersion     string       `json:"-"`
	DryRun         bool         `json:"dry_run"`
	Arms           int          `json:"arms"`
	Events         int          `json:"events"`
	Instruments    int          `json:"instruments"`
	Fields         int          `json:"fields"`
	RecordsAdded   int          `json:"records_added"`
	RecordsUpdated int          `json:"records_updated"`
	RecordsFailed  int          `json:"records_failed"`
	Problems       []odmProblem `json:"problems"`
}

// odmBuildPlan is a resolved document: the design to write, plus the lookups that
// turn the data half's ODM OIDs back into CLARA columns. It is built before
// anything is written and used unchanged by the dry run and the live import, so
// the two cannot disagree about what the document means.
type odmBuildPlan struct {
	design      *stagedDesign
	project     *db.Project
	identifier  string // GD-8, restored from KeySequence or from the first field
	eventOIDs   map[string]string
	formNames   map[string]string // FormDef OID → the instrument name it imported as
	groupByName map[string]odmGroupTarget
	columns     map[string]*odmColumn
}

// odmGroupTarget is what an ItemGroupData's OID resolves to: the instrument whose
// rows it fills, and whether that group repeats (the instance number a further
// ItemGroupData carries then becomes the storage slot).
type odmGroupTarget struct {
	instrument string
	matrix     bool
	repeating  bool
}

// odmColumn is one ItemDef resolved to storage: the CLARA field it stands for, the
// column its values live in, and whether that column is one checkbox box rather
// than a field of its own (REQ-DB-042).
type odmColumn struct {
	field        db.Field
	name         string
	choiceColumn bool
	choiceLabel  string
}

// spec is the column's own validation view — what the data import would validate a
// value of this column against. A checkbox box is validated as the box and not as its
// parent: one code, `1`, with none of the parent's validation type or bounds, exactly
// as the data API's expanded dictionary spells a derived column (record.go:129).
func (c *odmColumn) spec() validate.Field {
	if c.choiceColumn {
		return validate.Field{Name: c.name, Type: "checkbox", Choices: "1$" + strings.TrimSpace(c.choiceLabel)}
	}
	return validate.Field{
		Name:           c.field.FieldName,
		Type:           c.field.FieldType,
		ValidationType: c.field.ValidationType.String,
		ValidationMin:  c.field.ValidationMin.String,
		ValidationMax:  c.field.ValidationMax.String,
		Choices:        c.field.Choices.String,
	}
}

func (h *Handler) importODM(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r) // REQ-ODM-002: a whole study in one file, above any project role
	if !ok {
		return
	}
	ctx := r.Context()

	raw, refused := readODMBody(w, r)
	if refused {
		return
	}
	problems := &odmProblems{}
	if problem := odmCheckBody(raw); problem != nil {
		problems.list = append(problems.list, *problem) // REQ-ODM-013: refused before a parser sees it
		writeODMFailure(w, problems)
		return
	}
	doc, _ := odmParseDocument(raw, problems)
	if problems.failed() {
		writeODMFailure(w, problems) // nothing written, nothing audited (REQ-ODM-054, REQ-AUD-004)
		return
	}

	dryRun := isTruthy(r.URL.Query().Get("dry_run"))
	name := strings.TrimSpace(r.URL.Query().Get("project_name"))
	if name == "" {
		// The document's own study name when the request does not say (REQ-ODM-044).
		name = strings.TrimSpace(doc.study.studyName)
	}
	if name == "" {
		problems.add("", "", "validation_error", "the document names no project and the request carries no project_name")
		writeODMFailure(w, problems)
		return
	}
	dupe, err := h.Store.GetProjectByName(ctx, name)
	if err != nil {
		errInternal(w)
		return
	}
	if dupe != nil {
		// A collision is a conflict and never a rename of the existing project
		// (REQ-ODM-044): this route does not touch a project it did not create.
		errConflict(w, "project name '"+name+"' already exists")
		return
	}

	plan, err := h.odmResolve(ctx, doc, name, problems)
	if errors.Is(err, errODMFatal) || problems.failed() {
		writeODMFailure(w, problems) // a design that cannot be represented stops here (REQ-ODM-042)
		return
	}
	if err != nil {
		errInternal(w) // the document is sound; this installation could not judge it
		return
	}
	applyODMProjectOverrides(plan.project, r.URL.Query())

	report := &odmImportReport{
		ProjectName: name,
		ODMVersion:  doc.version,
		DryRun:      dryRun,
		Arms:        len(plan.design.Arms),
		Events:      plan.design.totalEvents(),
		Instruments: len(plan.design.Instruments),
		Fields:      len(plan.design.dbFields(0)),
		Problems:    problems.out(),
	}

	if dryRun {
		// The whole parse, validation and structure build ran above; the records are
		// validated here without a transaction, so the report says what the live call
		// would do (REQ-ODM-046).
		h.odmLoadRecords(ctx, 0, plan, doc, problems, report, true)
		report.Problems = problems.out()
		h.auditODMImport(ctx, u, report)
		writeJSON(w, http.StatusOK, report)
		return
	}

	projectID, err := h.odmBuildStructure(ctx, plan, problems)
	if err != nil {
		errInternal(w) // the transaction rolled back: no half-built project (REQ-ODM-040)
		return
	}
	report.ProjectID = projectID
	h.odmLoadRecords(ctx, projectID, plan, doc, problems, report, false)
	report.Problems = problems.out()
	h.auditODMImport(ctx, u, report)
	writeJSON(w, http.StatusCreated, report) // 201 live, 200 for the dry run (§7)
}

func applyODMProjectOverrides(project *db.Project, values map[string][]string) {
	get := func(key string) (string, bool) {
		items, ok := values[key]
		if !ok || len(items) == 0 {
			return "", false
		}
		return strings.TrimSpace(items[0]), true
	}

	if value, ok := get("organization"); ok {
		project.Organization = value
	}
	if value, ok := get("pi_name"); ok {
		project.PIName = value
	}
	if value, ok := get("pi_email"); ok {
		project.PIEmail = value
	}
	if value, ok := get("participant_names"); ok {
		project.ParticipantNames = value
	}
	for key, target := range map[string]*sql.NullString{
		"dm_name": &project.DMName, "dm_email": &project.DMEmail, "rek_number": &project.RekNumber,
		"rek_start_date": &project.RekStartDate, "rek_end_date": &project.RekEndDate,
		"start_date": &project.StartDate, "end_date": &project.EndDate,
	} {
		if value, ok := get(key); ok {
			*target = sql.NullString{String: value, Valid: value != ""}
		}
	}
}

// readODMBody takes the request body within the administration import bound
// (REQ-API-144). The answer is the bytes and whether the call was already refused.
func readODMBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		errBadRequest(w, "the request body must hold an ODM document")
		return nil, true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, importBodyLimit+1))
	if err != nil {
		errBadRequest(w, "the request body could not be read")
		return nil, true
	}
	if len(raw) > importBodyLimit {
		// The same answer the instrument import gives for the same bound (REQ-ODM-014).
		errBadRequest(w, fmt.Sprintf("the document is larger than %d MiB", importBodyLimit>>20))
		return nil, true
	}
	if len(raw) == 0 {
		errBadRequest(w, "the request body must hold an ODM document")
		return nil, true
	}
	return raw, false
}

// --- step 2 and 3: resolve the design, then judge it ----------------------------

// odmResolve turns the parsed document into the design CLARA would hold: names
// sanitized and de-duplicated, checkbox parents reconstructed, matrix rows placed
// in the instrument that owns them, GD-8 restored — and every field then run
// through the designer's own rules (§6.2 steps 2–3).
func (h *Handler) odmResolve(ctx context.Context, doc *odmDoc, name string, problems *odmProblems) (*odmBuildPlan, error) {
	if doc.study == nil || len(doc.study.mdvs) == 0 {
		// checkDocument refuses this already; the guard keeps a hostile document from
		// reaching an index that does not exist.
		return nil, problems.fatalRow("", "", "no_metadataversion", "the document holds no metadata version to build from")
	}
	mdv := doc.study.mdvs[0]
	knownTypes, err := h.knownValidationTypes(ctx)
	if err != nil {
		return nil, err
	}
	d := newEmptyStagedDesign()

	plan := &odmBuildPlan{
		design:      d,
		eventOIDs:   map[string]string{},
		formNames:   map[string]string{},
		groupByName: map[string]odmGroupTarget{},
		columns:     map[string]*odmColumn{},
		project: &db.Project{
			ProjectName:      name,
			ParticipantNames: "REC[0-9][0-9][0-9]",
			RecordNaming:     db.RecordNamingManual,
		},
	}
	applyProjectExtension(plan.project, doc.study.project)

	// Events and arms first: a field's references and the mapping both name an
	// event, and CLARA's arm numbers arrive with them (§3.2).
	for _, def := range mdv.events {
		armNum := odmArmNum(def)
		if armNum <= 0 {
			armNum = 1
		}
		if def.repeating || !strings.EqualFold(def.eventType, "Scheduled") {
			// CLARA has no unscheduled visit and no repeating event (§3.2): the file's
			// claim is accepted, flattened, and named.
			problems.add("", "", "event_flattened", fmt.Sprintf(
				"the event %q is imported as a scheduled, non-repeating event", def.name))
		}
		if (def.ext != nil && def.ext["synthetic"] == "1") || def.oid == noEventOID {
			// The stand-in for a project with no events (see odm_export.go): it names
			// no CLARA event, and its data rows land on the empty event name.
			plan.eventOIDs[def.oid] = ""
			continue
		}
		arm, err := odmPutArm(d, armNum, odmArmName(def, armNum), problems)
		if err != nil {
			return nil, err
		}
		unique := strings.TrimSpace(def.name)
		if unique == "" {
			unique = strings.TrimPrefix(def.oid, "EVT_")
		}
		if suffix := "_arm_" + strconv.Itoa(armNum); !strings.HasSuffix(unique, suffix) {
			// CLARA's unique name is `<label>_arm_<n>` (GD-15); a partner that spells
			// it otherwise gets the arm it belongs to appended.
			problems.add("", "", "name_renamed", fmt.Sprintf(
				"the event %q is imported as %q to carry its arm number", def.name, unique+suffix))
			unique += suffix
		}
		if _, _, clash := d.eventByUniqueName(unique); clash {
			return nil, problems.fatalRow("", "", "dangling_ref",
				fmt.Sprintf("two events are named %q; a project names each event once", unique))
		}
		event := db.Event{
			ProjectID:       0,
			EventName:       odmEventLabel(def, unique, armNum),
			UniqueEventName: unique,
			Period:          nullIntExt(def.ext, "period"),
			SafeRegionStart: nullIntExt(def.ext, "safeRegionStart"),
			SafeRegionEnd:   nullIntExt(def.ext, "safeRegionEnd"),
		}
		if _, ok := d.putEvent(arm.ID, event); !ok {
			return nil, problems.fatalRow("", "", "dangling_ref",
				fmt.Sprintf("the event %q has no arm %d to belong to", unique, armNum))
		}
		plan.eventOIDs[def.oid] = unique
	}
	for _, attrs := range doc.study.arms {
		// Arms that hold no events of their own travel only in the extension (§5).
		armNum, _ := strconv.Atoi(attrs["armNum"])
		if armNum <= 0 {
			continue
		}
		if _, found := d.arm(armNum); found {
			continue
		}
		if _, err := odmPutArm(d, armNum, attrs["name"], problems); err != nil {
			return nil, err
		}
	}

	// Instruments, from the forms (§3.3).
	instrumentsByFormOID := map[string]*stagedInstrument{}
	for i, def := range mdv.forms {
		baseName, _ := importedInstrumentName(def.name, i+1)
		instName := baseName
		for n := 2; ; n++ {
			if _, taken := d.instrumentByName(instName); !taken {
				break
			}
			instName = numberedInstrumentName(baseName, n)
		}
		if instName != def.name {
			problems.add("", "", "name_renamed", fmt.Sprintf("the form %q is imported as %q", def.name, instName))
		}
		inst := d.putInstrument(db.Instrument{
			Name:           instName,
			Position:       i + 1,
			IsSurvey:       extBool(def.ext, "isSurvey"),
			BranchingLogic: nullStrExt(def.ext, "branchingLogic"),
		})
		instrumentsByFormOID[def.oid] = inst
		plan.formNames[def.oid] = inst.Name
	}

	// Item groups resolve to the instrument whose rows they fill: every group a form
	// declares fills that form's instrument, and the one that is not the instrument's
	// own group — `IG_<instrument>`, which CLARA names after it (§3.3) — is a matrix
	// group sitting inside it. Forms are walked in document order so a group a partner
	// shares across two forms keeps the first declaration rather than a map iteration
	// order.
	targets := map[string]odmGroupTarget{}
	for _, form := range mdv.forms {
		inst, known := instrumentsByFormOID[form.oid]
		if !known {
			continue
		}
		for _, ref := range instrumentsGroupRefs(mdv, form.oid) {
			if _, taken := targets[ref.oid]; taken {
				continue
			}
			targets[ref.oid] = odmGroupTarget{instrument: inst.Name, matrix: !ref.isOwnGroup}
		}
	}
	for _, def := range mdv.groups {
		target, ok := targets[def.oid]
		if !ok {
			// An unreferenced group describes nothing the project can collect; it is
			// named as a loss rather than built (§8).
			problems.add("", "", "element_ignored",
				fmt.Sprintf("the item group %q is referenced by no form and is not imported (REQ-ODM-053)", def.name))
			continue
		}
		target.repeating = def.repeating
		plan.groupByName[def.oid] = target
	}

	// Fields, per instrument, in the order the document gives them (§3.4).
	for _, inst := range d.Instruments {
		if err := h.odmResolveInstrument(mdv, plan, inst.Name, knownTypes, problems); err != nil {
			return nil, err
		}
	}

	// The mapping is the events' FormRef lists (§3.2), and repeating follows the
	// per-pair spelling when the document carries one.
	repeatingByInstrument := map[string][]string{}
	for _, def := range mdv.events {
		unique := plan.eventOIDs[def.oid]
		if unique == "" {
			continue // the synthetic stand-in maps no pair (there is no event)
		}
		for _, ref := range def.formRefs {
			form := mdv.formByOID(ref.oid)
			if form == nil {
				continue // checked fatally already; kept defensive
			}
			inst, found := d.instrumentByName(odmResolveFormName(plan, mdv, ref.oid))
			if !found {
				problems.add("", "", "element_ignored", fmt.Sprintf(
					"the event %q maps the form %q, which imports under no name (REQ-ODM-053)", def.name, ref.oid))
				continue
			}
			d.setMappingForArm(odmArmNum(def), mergeArmMapping(d.Mapping[odmArmNum(def)], inst.Name, unique))
			repeats := groupRepeats(plan, mdv, ref.oid, inst.Name)
			if claraRepeatingForm(def.ext, ref.oid) {
				repeats = true // 1.3 spells the per-pair flag in the extension (REQ-ODM-029)
			}
			if ref.repeating != nil {
				repeats = *ref.repeating // a FormRef that carries the attribute speaks for itself (REQ-ODM-050)
			}
			if repeats {
				repeatingByInstrument[inst.Name] = append(repeatingByInstrument[inst.Name], unique)
			}
		}
	}
	for instrument, uniques := range repeatingByInstrument {
		d.setRepeating(instrument, uniques) // only pairs already mapped keep the flag
	}

	if err := h.odmRestoreIdentifier(d, mdv, problems); err != nil {
		return nil, err
	}
	if err := h.odmValidateDesign(ctx, d, plan, problems); err != nil {
		return nil, err
	}
	return plan, nil
}

// odmResolveInstrument builds one instrument's fields from the item groups that
// belong to it, in document order, plus its static display-only rows (§3.7).
func (h *Handler) odmResolveInstrument(mdv *odmMetaDataVersion, plan *odmBuildPlan, instName string,
	knownTypes map[string]bool, problems *odmProblems) error {

	d := plan.design
	inst, found := d.instrumentByName(instName)
	if !found {
		return nil
	}
	var entries []odmFieldEntry
	order := 0

	for _, def := range mdv.groups {
		target, ok := plan.groupByName[def.oid]
		if !ok || target.instrument != instName {
			continue
		}
		for _, ref := range def.itemRefs {
			item := mdv.itemByOID(ref.oid)
			if item == nil {
				continue // checked fatally already
			}
			order++
			f, column, err := odmFieldOf(mdv, item, ref, target, order, knownTypes, problems)
			if err != nil {
				return err
			}
			// A checkbox arrives as one ItemDef per box (§3.6), and every one of them
			// spells the same parent field: the parent is built once, its boxes are the
			// columns. Two rows for one parent would fail the design's own uniqueness
			// rule (fields(project_id, field_name)).
			if f.FieldType != "checkbox" || !holdsParent(entries, f.FieldName) {
				hint, hasHint := 0, false
				if raw := extStr(item.ext, "position"); raw != "" {
					if n, convErr := strconv.Atoi(strings.TrimSpace(raw)); convErr == nil && n > 0 {
						hint, hasHint = n, true
					}
				}
				entries = append(entries, odmFieldEntry{position: hint, order: order, field: f, hasHint: hasHint})
			}
			plan.columns[item.oid] = column
		}
		for _, attrs := range def.statics { // description/header/transfer rows (§3.7)
			order++
			f := db.Field{
				FieldName:           attrs["fieldName"],
				FieldType:           attrs["fieldType"],
				FieldLabel:          sql.NullString{String: attrs["label"], Valid: attrs["label"] != ""},
				Choices:             sql.NullString{String: attrs["instructions"], Valid: attrs["instructions"] != ""},
				PersonalInformation: extBool(attrs, "personalInformation"),
				DirectIdentifier:    extBool(attrs, "directIdentifier"),
				ExportApproved:      extBool(attrs, "exportApproved"),
			}
			hint, hasHint := 0, false
			if n, convErr := strconv.Atoi(strings.TrimSpace(attrs["position"])); convErr == nil && n > 0 {
				hint, hasHint = n, true
			}
			entries = append(entries, odmFieldEntry{position: hint, order: order, field: f, hasHint: hasHint})
		}
	}

	// The extension's position is the exact one from the exporting project; where
	// no document says, the order the file lists wins (§5: ODM first, extension a
	// hint — and here the extension is the only spelling that survives a matrix
	// group being written after the fields it sits among).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].hasHint != entries[j].hasHint {
			return entries[i].hasHint // hinted positions first: an unhinted field follows what it can rank against
		}
		if entries[i].position != entries[j].position {
			return entries[i].position < entries[j].position
		}
		return entries[i].order < entries[j].order
	})
	for i, e := range entries {
		f := e.field
		f.Position = i + 1 // CLARA numbers a form's fields densely, from one
		sf, ok := d.putField(inst.ID, f)
		if !ok {
			return problems.fatalRow("", f.FieldName, "dangling_ref", "the instrument "+instName+" disappeared while resolving it")
		}
		for _, column := range plan.columns {
			if column.field.FieldName == "" || column.name != f.FieldName {
				continue
			}
			column.field = sf.field(0)
		}
	}
	return nil
}

// odmFieldOf maps one ItemDef and its ItemRef onto a CLARA field plus the column
// its values live in (§3.4 inverse, §3.6 checkbox parents).
func odmFieldOf(mdv *odmMetaDataVersion, item *odmItemDef, ref odmItemRef, target odmGroupTarget,
	order int, knownTypes map[string]bool, problems *odmProblems) (db.Field, *odmColumn, error) {

	name, renamed := odmSanitizeName(item.name, "field", order)
	if renamed {
		problems.add("", "", "name_renamed", fmt.Sprintf("the item %q is imported as %q", item.name, name))
	}
	f := db.Field{FieldName: name}

	// The extension carries CLARA's own spelling; without it the ODM types and a
	// codelist decide, and what cannot be represented is converted with notice.
	if kind := extStr(item.ext, "fieldType"); kind != "" && fieldTypes[kind] {
		f.FieldType = kind
	} else if kind != "" {
		problems.add("", name, "type_mapped", fmt.Sprintf(
			"the extension calls %q a %q field, which CLARA has no such type for; the ODM type is used instead", name, kind))
	}

	switch {
	case item.choiceExt != nil:
		// A checkbox's derived column (REQ-EXP-053): the parent field is what gets
		// created, and this column is one of its boxes (§3.6).
		base := extStr(item.choiceExt, "fieldName")
		if base == "" {
			base, _, _ = db.SplitCheckboxSub(name)
		}
		if base == "" {
			return f, nil, problems.fatalRow("", name, "dangling_ref",
				"a checkbox choice column names no parent field")
		}
		f.FieldName = base
		f.FieldType = "checkbox"
		f.Choices = sql.NullString{String: extStr(item.choiceExt, "choices"), Valid: extStr(item.choiceExt, "choices") != ""}
		f.Required = extBool(item.choiceExt, "required")
		if !f.Choices.Valid {
			// A file that carries the boxes but not the parent's grammar gets one
			// built from what arrived: the code stands in for its own label.
			code := strings.TrimPrefix(name, base+db.CheckboxSubSep)
			label := strings.TrimSpace(strings.TrimPrefix(item.description, ", "))
			f.Choices = sql.NullString{String: code + "$" + label, Valid: true}
			problems.add("", base, "type_mapped", fmt.Sprintf(
				"the checkbox %q names no choice labels; its codes are imported as their own labels", base))
		}
	case f.FieldType == "":
		f.FieldType = odmFieldType(item, mdv)
	}

	if vt := extStr(item.ext, "validationType"); vt != "" {
		if builtinValidationTypes[vt] || knownTypes[vt] {
			f.ValidationType = sql.NullString{String: vt, Valid: true}
		} else {
			// A type this installation has no row for cannot be kept: the values it
			// would have constrained arrive unchecked, and that is named (§3.4).
			problems.add("", name, "validation_dropped", fmt.Sprintf(
				"the validation type %q of field %q is unknown here and is not imported", vt, name))
		}
	} else if item.choiceExt == nil {
		// A checkbox box's DataType says what a *box* holds (an integer 1/0), not what
		// the parent field CLARA builds from it validates: taking it over would give the
		// parent a validation type no checkbox of this project ever had (§3.6).
		f.ValidationType = odmValidationType(item)
	}
	if fmtStr := extStr(item.ext, "validationFormat"); fmtStr != "" {
		f.ValidationFormat = sql.NullString{String: fmtStr, Valid: true}
	}
	if matrix := extStr(item.ext, "matrixGroup"); matrix != "" {
		f.MatrixGroup = sql.NullString{String: matrix, Valid: true}
	} else if target.matrix {
		f.MatrixGroup = sql.NullString{String: target.instrument + "_group", Valid: true}
	}
	if header := extStr(item.ext, "sectionHeader"); header != "" {
		f.SectionHeader = sql.NullString{String: header, Valid: true}
	}
	if note := extStr(item.ext, "annotation"); note != "" {
		f.FieldAnnotation = sql.NullString{String: note, Valid: true}
	}
	if branch := extStr(item.ext, "branchingLogic"); branch != "" {
		// Stored under the display-only rule and never executed (REQ-ODM-047, GD-13).
		f.BranchingLogic = sql.NullString{String: branch, Valid: true}
	}
	if item.description != "" {
		f.FieldLabel = sql.NullString{String: item.description, Valid: true}
	}
	if item.question != "" {
		f.FieldNote = sql.NullString{String: item.question, Valid: true}
	}
	// The sensitivity flags of a field that states them; a field that states none
	// takes CLARA's creation defaults — not personal, not an identifier, not
	// approved (REQ-ODM-052). AddFieldTx applies the preset for the identifier
	// validation types afterwards, exactly as every other write path does.
	flags := item.ext
	if item.choiceExt != nil {
		// A checkbox's three flags travel on clara:Choice, beside the choice grammar and
		// the required flag of the parent they describe (§3.6).
		flags = item.choiceExt
	}
	if flags != nil {
		f.PersonalInformation = extBool(flags, "personalInformation")
		f.DirectIdentifier = extBool(flags, "directIdentifier")
		f.ExportApproved = extBool(flags, "exportApproved")
	}
	// @Mandatory and the RangeChecks are ODM's own spelling and win (§5) — with one
	// exception: §3.3 writes Mandatory="Y" as part of the KeySequence="1" marker that
	// names the record identifier, so on that item the attribute says which field GD-8
	// is, not that CLARA's required flag is set. Reading it as a flag would hand back a
	// project whose identifier is flagged required when the source never flagged it
	// (REQ-ODM-050).
	if !ref.keySequence {
		f.Required = f.Required || ref.mandatory
	}
	if item.min != "" {
		f.ValidationMin = sql.NullString{String: item.min, Valid: true}
	}
	if item.max != "" {
		f.ValidationMax = sql.NullString{String: item.max, Valid: true}
	}
	if ref.methodOID != "" && mdv.methods[ref.methodOID] != nil {
		expr := mdv.methods[ref.methodOID].expression
		f.Calculation = sql.NullString{String: expr, Valid: expr != ""}
		if f.FieldType == "" {
			f.FieldType = "calculated"
		}
	}

	// A CodeList is ODM's own spelling of a choice list, so it becomes CLARA's stored
	// choice grammar wherever the extension did not already carry one: an item whose
	// codes arrived in the document cannot import as free text, or its values would
	// pass validation that the dictionary says they must fail (§3.5, REQ-ODM-042). A
	// checkbox box is exempt — its CL_BOOLEAN codes say checked/unchecked, and the
	// parent's own grammar travels in clara:Choice (§3.6).
	if item.choiceExt == nil && item.codelistOID != "" && f.Choices.String == "" {
		if cl := mdv.codeLists[item.codelistOID]; cl != nil {
			if grammar := odmChoiceGrammar(cl); grammar != "" {
				f.Choices = sql.NullString{String: grammar, Valid: true}
			}
		}
	}

	column := &odmColumn{name: f.FieldName, field: f}
	if item.choiceExt != nil {
		// Only a document that says so, through the extension's parent name, contributes
		// a box: an ordinary item whose name happens to hold `___` stays an ordinary
		// field, because there is no parent to fold it into (§3.6).
		column.choiceColumn = true
		column.name = name // the derived column, not the parent field
		column.choiceLabel = item.description
	}
	return f, column, nil
}

// odmChoiceGrammar renders a CodeList as CLARA's stored choice grammar —
// `code$label##code$label` in the document's order (§1, §3.5). A code that carries
// no Decode labels itself: an EnumeratedItem states its code and nothing else.
func odmChoiceGrammar(cl *odmCodeList) string {
	items := make([]odmCodeItem, len(cl.items))
	copy(items, cl.items)
	sort.SliceStable(items, func(i, j int) bool { return items[i].order < items[j].order })
	pairs := make([]string, 0, len(items))
	for _, it := range items {
		label := it.label
		if label == "" {
			label = it.code
		}
		pairs = append(pairs, it.code+"$"+label)
	}
	return strings.Join(pairs, "##")
}

// odmFieldType is the inverse of §3.4 for a document that carries no extension:
// what the ODM type and a codelist say the field is.
func odmFieldType(item *odmItemDef, mdv *odmMetaDataVersion) string {
	if item.codelistOID != "" && mdv.codeLists[item.codelistOID] != nil {
		return "dropdown" // a coded field with decodes: the one choice-list type CLARA defaults to
	}
	switch item.dataType {
	case "integer", "float", "date", "datetime":
		return "text"
	default:
		return "text"
	}
}

// odmValidationType is the validation a DataType implies when the extension says
// nothing (§3.4 inverse). A text field keeps no validation type; an identifier-
// shaped one cannot be recovered from ODM, which §3.4 already warns about on the
// way out.
func odmValidationType(item *odmItemDef) sql.NullString {
	switch item.dataType {
	case "integer":
		return sql.NullString{String: "integer", Valid: true}
	case "float":
		return sql.NullString{String: "floating point", Valid: true}
	case "date":
		return sql.NullString{String: "date", Valid: true}
	case "datetime":
		return sql.NullString{String: "datetime", Valid: true}
	}
	return sql.NullString{}
}

// odmRestoreIdentifier puts GD-8 back: the field the document marks with
// KeySequence="1", or the first field of the earliest non-empty instrument
// where it marks nothing, is placed first in that instrument (§6.2 step 2).
func (h *Handler) odmRestoreIdentifier(d *stagedDesign, mdv *odmMetaDataVersion, problems *odmProblems) error {
	marked := ""
	for _, def := range mdv.groups {
		for _, ref := range def.itemRefs {
			if ref.keySequence {
				if item := mdv.itemByOID(ref.oid); item != nil {
					marked = item.name
				}
			}
		}
	}
	instruments := sortedInstruments(d.Instruments)
	if len(instruments) == 0 {
		return problems.fatalRow("", "", "no_metadataversion", "the document describes no instrument")
	}
	var first *stagedInstrument
	var ordered []stagedField
	for i := range instruments {
		fields := sortedFields(instruments[i].Fields)
		if len(fields) > 0 {
			first = &instruments[i]
			ordered = fields
			break
		}
	}
	if first == nil {
		return nil // nothing to identify a record with; the project holds an empty form
	}
	target := ordered[0].FieldName
	if marked != "" {
		for i := range d.Instruments {
			inst := &d.Instruments[i]
			for _, f := range inst.Fields {
				if f.FieldName == marked {
					target = marked
					if inst.ID != first.ID {
						return problems.fatalRow("", marked, "dangling_ref", fmt.Sprintf(
							"the record identifier %q is keyed in the instrument %q, not in the earliest instrument with fields", marked, inst.Name))
					}
				}
			}
		}
	}
	if ordered[0].FieldName == target {
		return nil
	}
	// Move the marked field to the front of its instrument.
	var ids []int64
	for _, f := range sortedFields(first.Fields) {
		if f.FieldName == target {
			ids = append([]int64{f.ID}, ids...)
			continue
		}
		ids = append(ids, f.ID)
	}
	if !d.orderFields(first.ID, ids) {
		return problems.fatalRow("", target, "dangling_ref", "the record identifier could not be placed first")
	}
	return nil
}

// odmValidateDesign runs the designer's own rules over every field of the built
// design (§6.2 step 3). A failure is fatal: a structure that cannot be represented
// does not get written at all (REQ-ODM-042).
func (h *Handler) odmValidateDesign(ctx context.Context, d *stagedDesign, plan *odmBuildPlan, problems *odmProblems) error {
	dc := d.designContext(0)
	for _, inst := range d.Instruments {
		for _, sf := range inst.Fields {
			f := sf.field(0)
			if _, derr := h.validateFieldDesign(ctx, &f, f.ID, dc); derr != nil {
				return problems.fatalRow("", f.FieldName, "validation_error", derr.msg)
			}
			d.putField(inst.ID, f) // the rules normalize (min/max, format, presets)
			for _, column := range plan.columns {
				if column.name == f.FieldName || column.field.FieldName == f.FieldName {
					column.field = f
				}
			}
		}
	}
	return nil
}

// --- step 4: build ----------------------------------------------------------------

// odmBuildStructure writes the project and its whole design in one transaction, so
// a file that fails halfway leaves neither a project nor orphan rows (REQ-ODM-040).
func (h *Handler) odmBuildStructure(ctx context.Context, plan *odmBuildPlan, problems *odmProblems) (int64, error) {
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	projectID, err := h.Store.CreateProjectTx(ctx, tx, plan.project)
	if err != nil {
		return 0, err
	}
	armIDs := map[int]int64{}
	for _, arm := range plan.design.Arms {
		row := arm.arm(projectID)
		id, err := h.Store.AddArmTx(ctx, tx, &row)
		if err != nil {
			return 0, err
		}
		armIDs[arm.ArmNum] = id
	}
	if len(armIDs) == 0 {
		// A project always holds at least one arm: creation makes one (REQ-DB-011) and
		// deletion of the last turns into a rename instead (REQ-API-128). A document
		// that names none — a no-event study, whose only event is the synthetic stand-in
		// — still gets CLARA's own opening arm, unnamed, or every reader that resolves a
		// project by its first arm would find nothing.
		if _, err := h.Store.AddArmTx(ctx, tx, &db.Arm{ProjectID: projectID, ArmNum: 1}); err != nil {
			return 0, err
		}
	}
	eventIDs := map[string]int64{}
	for _, arm := range plan.design.Arms {
		for _, ev := range arm.Events {
			row := ev.event(projectID, armIDs[arm.ArmNum])
			id, err := h.Store.AddEventTx(ctx, tx, &row)
			if err != nil {
				return 0, err
			}
			eventIDs[row.UniqueEventName] = id
		}
	}
	instrumentIDs := map[string]int64{}
	for _, inst := range plan.design.Instruments {
		row := inst.instrument(projectID)
		id, err := h.Store.AddInstrumentTx(ctx, tx, &row)
		if err != nil {
			return 0, err
		}
		instrumentIDs[inst.Name] = id
	}
	for _, inst := range plan.design.Instruments {
		for _, sf := range sortedFields(inst.Fields) {
			row := sf.field(projectID)
			row.ProjectID = projectID
			row.InstrumentID = instrumentIDs[inst.Name]
			if _, err := h.Store.AddFieldTx(ctx, tx, &row); err != nil {
				return 0, err
			}
		}
	}
	// The mapping per arm, then the repeating designations: SetInstrumentEventsForArm
	// deliberately preserves flags it did not create rather than reading them off the
	// matrix, so on a project this route has just made the flag has to follow in its
	// own statement (repo_design.go:559).
	for _, arm := range plan.design.Arms {
		var pairs []db.InstrumentEvent
		for instrument, uniques := range plan.design.Mapping[arm.ArmNum] {
			instrumentID, ok := instrumentIDs[instrument]
			if !ok {
				continue
			}
			for _, unique := range uniques {
				eventID, ok := eventIDs[unique]
				if !ok {
					continue
				}
				pairs = append(pairs, db.InstrumentEvent{InstrumentID: instrumentID, EventID: eventID})
			}
		}
		if len(pairs) == 0 {
			continue
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].EventID < pairs[j].EventID })
		if err := h.Store.SetInstrumentEventsForArmTx(ctx, tx, projectID, armIDs[arm.ArmNum], pairs); err != nil {
			return 0, err
		}
	}
	// The repeating designations follow the mapping in their own statement, and they
	// name the events by the ids *this build* assigned: the snapshot's own ids are
	// provisional negatives that no row answers to, and an UPDATE against one of those
	// quietly designates nothing repeating — a round trip that loses a repeating form.
	for _, inst := range plan.design.Instruments {
		var repeatIDs []int64
		seen := map[int64]bool{}
		for _, arm := range plan.design.Arms {
			for _, unique := range plan.design.Repeating[arm.ArmNum][inst.Name] {
				id, ok := eventIDs[unique]
				if !ok || seen[id] {
					continue
				}
				seen[id] = true
				repeatIDs = append(repeatIDs, id)
			}
		}
		if len(repeatIDs) == 0 {
			continue
		}
		if err := h.Store.SetRepeatingEventsTx(ctx, tx, instrumentIDs[inst.Name], repeatIDs); err != nil {
			return 0, err
		}
	}
	if err := h.Store.MaterializePairGrantsTx(ctx, tx, projectID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	h.Store.BumpStructureCache() // after the commit, never inside it
	return projectID, nil
}

// --- step 5: load the records -----------------------------------------------------

// odmLoadRecords stores one record per transaction in the file's order, each
// keeping or failing on its own (REQ-ODM-041). A dry run walks the same path with
// the writes removed, so it reports exactly what the live call would have done
// (REQ-ODM-046).
func (h *Handler) odmLoadRecords(ctx context.Context, projectID int64, plan *odmBuildPlan, doc *odmDoc,
	problems *odmProblems, report *odmImportReport, dryRun bool) {

	reg, err := h.odmValidationRegistry(ctx)
	if err != nil {
		problems.add("", "", "validation_error", "the validation registry could not be loaded")
		return
	}

	for _, subject := range doc.subjects {
		recordID := strings.TrimSpace(subject.subjectKey)
		if recordID == "" {
			problems.add("", "", "record_failed", "a record carries no SubjectKey")
			report.RecordsFailed++
			continue
		}
		var writes []odmWrite
		failed := false
		for _, event := range subject.events {
			unique, known := plan.eventOIDs[event.eventOID]
			if !known && event.eventOID != "" {
				problems.add(recordID, "", "unknown_event",
					"the record fills the event "+event.eventOID+", which the document's metadata does not declare")
				failed = true
				continue
			}
			for _, group := range event.groups {
				target, ok := plan.groupByName[group.groupOID]
				if !ok {
					problems.add(recordID, "", "unknown_item",
						"the record fills the item group "+group.groupOID+", which imports under no instrument")
					failed = true
					continue
				}
				instance := group.repeatKey
				if instance < 1 {
					instance = 1
				}
				if instance > 1 && !target.repeating {
					// The storage slot for a further instance only exists for a pair the
					// design designates repeating (REQ-DB-047/048): as the data import
					// refuses an unknown repeat instrument, so here the row cannot land.
					problems.add(recordID, "", "record_failed", fmt.Sprintf(
						"the instrument %q is not designated repeating, so instance %d has nowhere to go",
						target.instrument, instance))
					failed = true
					continue
				}
				slot, number := db.InstanceSlot(target.instrument, instance)
				if target.matrix {
					// A matrix group's rows are ordinary fields of the instrument, so they
					// live in the instrument's own first instance (§3.3).
					slot, number = "", 1
				}
				for _, item := range group.items {
					column, ok := plan.columns[item.itemOID]
					if !ok {
						// An unknown column is a per-record problem, not a failure of the
						// document: this is how the data import reports an unknown key (§6.2).
						problems.add(recordID, "", "unknown_item",
							"the record carries an item CLARA has no field for ("+item.itemOID+")")
						continue
					}
					if !item.present || item.value == "" {
						continue // absent, and a cleared value is indistinguishable from it (REQ-ODM-043)
					}
					value, violation := h.odmValidateValue(column, item.value, reg)
					if violation != nil {
						problems.add(recordID, column.name, "value_invalid", string(violation.Code)+" — "+violation.Message)
						failed = true
						continue
					}
					if value == "" {
						continue
					}
					writes = append(writes, odmWrite{
						value: &db.DataValue{
							ProjectID: projectID, RecordID: recordID, UniqueEventName: unique,
							RepeatingInstrument: slot, RepeatingInstanceNumber: number,
							FieldName: column.name, Value: value,
						},
					})
				}
			}
		}
		if failed {
			report.RecordsFailed++ // the record's own values fail together; the rest continue (REQ-ODM-041)
			continue
		}
		if dryRun {
			report.RecordsAdded++
			continue
		}
		stored, err := h.odmStoreRecord(ctx, projectID, recordID, writes)
		if err != nil {
			problems.add(recordID, "", "record_failed", "the record could not be stored")
			report.RecordsFailed++
			continue
		}
		if stored {
			report.RecordsAdded++
		} else {
			report.RecordsUpdated++
		}
	}
}

// odmWrite is one value that passed validation, held until the record's whole set
// is known good: a record whose second value fails stores neither (REQ-ODM-041).
type odmWrite struct {
	value *db.DataValue
}

// odmStoreRecord applies one record in its own transaction. It reports whether the
// record is new, which is what separates records_added from records_updated when a
// document names one record twice.
func (h *Handler) odmStoreRecord(ctx context.Context, projectID int64, recordID string, writes []odmWrite) (bool, error) {
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	entity, err := h.Store.GetRecordEntityTx(ctx, tx, projectID, recordID)
	if err != nil {
		return false, err
	}
	existing, err := h.Store.ListDataValuesByRecordTx(ctx, tx, projectID, recordID)
	if err != nil {
		return false, err
	}
	newRecord := entity == nil && len(existing) == 0
	if entity == nil {
		if err := h.Store.CreateRecordEntityTx(ctx, tx, &db.RecordEntity{ProjectID: projectID, RecordID: recordID}); err != nil {
			return false, err
		}
	}
	for _, w := range writes {
		if err := h.Store.UpsertDataValueTx(ctx, tx, w.value); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return newRecord, nil
}

// odmValidateValue runs one value through the same three questions the data API's
// import asks — the type validator, the date canonicalizer, the content policy —
// so an interchange file cannot store a byte the API would refuse (REQ-ODM-042,
// O-7). A checkbox box answers 1 or 0 first, as the import path does: only a
// checked box holds a row (REQ-DB-042).
//
// The order is the record import's (dataapi/record_import.go:501-520) and is not
// interchangeable: the type validator sees the value as it arrived, the canonical
// form is what the content policy measures and stores.
func (h *Handler) odmValidateValue(column *odmColumn, raw string, reg *validate.Registry) (string, *validate.Violation) {
	f := column.spec()
	if column.choiceColumn {
		// One box: 1 checks it and 0 clears it exactly as an absent value does, so a
		// cleared box stores nothing rather than a zero (REQ-DB-042).
		switch raw {
		case "0":
			return "", nil
		case "1":
		default:
			return "", &validate.Violation{Code: validate.CodeChoiceInvalid,
				Message: fmt.Sprintf("value %q of checkbox choice %q must be 0 or 1", raw, column.name)}
		}
	}
	if v := validate.ValidateValue(f, raw, reg); v != nil {
		return "", v
	}
	value := raw
	if f.ValidationType == "date" || f.ValidationType == "datetime" {
		// The shared canonicalizer (GD-16): same grammar, same field format, the
		// configured collection offset — the interchange route carries no tz parameter.
		canonical, ok := h.Data.CanonicalizeImportedDate(raw, f.ValidationType, column.field.ValidationFormat.String)
		if !ok {
			return "", &validate.Violation{Code: validate.CodeTypeInvalid,
				Message: fmt.Sprintf("value %q is not a valid %s", raw, f.ValidationType)}
		}
		value = canonical
	}
	stored, violation := validate.ApplyContentPolicy(f, value, h.Cfg.MaxValueBytes)
	if violation != nil {
		return "", violation
	}
	return stored, nil
}

// --- reporting, audit and helpers ---------------------------------------------------

// writeODMFailure answers a refused import: 400 with the problem list in the report
// shape, and nothing written or audited (REQ-ODM-054, REQ-AUD-004).
func writeODMFailure(w http.ResponseWriter, problems *odmProblems) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":    "odm_import_rejected",
		"message":  odmFirstProblemMessage(problems),
		"status":   http.StatusBadRequest,
		"problems": problems.out(),
	})
}

func odmFirstProblemMessage(problems *odmProblems) string {
	if len(problems.list) > 0 {
		return problems.list[0].Message
	}
	return "the document cannot be imported"
}

// auditODMImport records the call: counts and options, never a value, a field name
// or any part of the document (REQ-ODM-061, REQ-AUD-014). A dry run writes this one
// entry and nothing else — it changed no state to account for (REQ-ODM-046), and its
// `dry_run` key is what separates the two in the trail (§9).
//
// The write follows the commit, so a failure cannot be undone by rollback; the entry
// is still owed, and it is written uncanceled like the audit export's own.
func (h *Handler) auditODMImport(ctx context.Context, u *db.User, report *odmImportReport) {
	_ = h.Audit.Insert(context.WithoutCancel(ctx), audit.Entry{
		EventType: audit.ODMImport,
		Source:    audit.SourceAPI,
		UserID:    u.ID,
		Email:     u.Email,
		ProjectID: report.ProjectID, // 0 for a dry run: there is no project to name
		Details: map[string]any{
			"project_id":     report.ProjectID,
			"odm_version":    report.ODMVersion,
			"dry_run":        report.DryRun,
			"arms":           report.Arms,
			"events":         report.Events,
			"instruments":    report.Instruments,
			"fields":         report.Fields,
			"records_added":  report.RecordsAdded,
			"records_failed": report.RecordsFailed,
			"problems":       len(report.Problems),
		},
	})
}

// odmArmNum is the arm an event belongs to: the extension's word first, then the
// Category the exporter spells (§3.2), then 1.
func odmArmNum(def *odmEventDef) int {
	if raw := extStr(def.ext, "armNum"); raw != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
			return n
		}
	}
	if def.category != "" {
		head := strings.TrimPrefix(def.category, "Arm ")
		digits := head
		if i := strings.IndexAny(digits, " —-"); i >= 0 {
			digits = digits[:i]
		}
		if n, err := strconv.Atoi(strings.TrimSpace(digits)); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

func odmArmName(def *odmEventDef, armNum int) string {
	if def.category != "" && strings.HasPrefix(def.category, "Arm ") {
		name := strings.TrimSpace(strings.TrimPrefix(def.category, "Arm "+strconv.Itoa(armNum)))
		name = strings.TrimPrefix(name, "—")
		return strings.TrimSpace(name)
	}
	return ""
}

// odmEventLabel is the human event name: the extension's, or the unique name with
// its arm suffix taken back off.
func odmEventLabel(def *odmEventDef, unique string, armNum int) string {
	if label := extStr(def.ext, "eventName"); label != "" {
		return label
	}
	return strings.TrimSuffix(unique, "_arm_"+strconv.Itoa(armNum))
}

// applyProjectExtension fills the project columns the extension carries (§3.1):
// organization, the two people, the REK number and dates, the record naming mode.
func applyProjectExtension(p *db.Project, attrs map[string]string) {
	if attrs == nil {
		return
	}
	p.Organization = attrs["organization"]
	p.PIName = attrs["piName"]
	p.PIEmail = attrs["piEmail"]
	p.DMName = nullStrExt(attrs, "dmName")
	p.DMEmail = nullStrExt(attrs, "dmEmail")
	p.RekNumber = nullStrExt(attrs, "rekNumber")
	p.RekStartDate = nullStrExt(attrs, "rekStartDate")
	p.RekEndDate = nullStrExt(attrs, "rekEndDate")
	p.StartDate = nullStrExt(attrs, "startDate")
	p.EndDate = nullStrExt(attrs, "endDate")
	if naming := attrs["recordNaming"]; naming == db.RecordNamingAuto || naming == db.RecordNamingManual {
		p.RecordNaming = naming
	}
}

// odmSanitizeName repairs field names the file came with. Instrument names use
// the more permissive REQ-DB-011 contract shared with the dictionary importer.
func odmSanitizeName(name, kind string, position int) (string, bool) {
	if kind == "instrument" {
		return importedInstrumentName(name, position)
	}
	original := strings.TrimSpace(name)
	candidate := strings.ToLower(odmLocalName.ReplaceAllString(original, "_"))
	candidate = strings.Trim(candidate, "_")
	if candidate == "" {
		candidate = kind + "_" + strconv.Itoa(position)
	}
	if len(candidate) > 64 {
		candidate = candidate[:64]
	}
	return candidate, candidate != original
}

// nullStrExt and nullIntExt read the extension's optional attributes.
func nullStrExt(attrs map[string]string, key string) sql.NullString {
	v := extStr(attrs, key)
	return sql.NullString{String: v, Valid: v != ""}
}

func nullIntExt(attrs map[string]string, key string) sql.NullInt64 {
	raw := extStr(attrs, key)
	if raw == "" {
		return sql.NullInt64{}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

func isTruthy(v string) bool {
	return v == "1" || strings.EqualFold(v, "true")
}

// newEmptyStagedDesign builds the design an import writes into. parseStagedDesign
// is the codebase's own door onto an empty document: it allocates the two mapping
// maps every mutator indexes and starts the provisional id counter where put*
// expects to find it.
func newEmptyStagedDesign() *stagedDesign {
	d, err := parseStagedDesign("{}")
	if err != nil {
		// An empty literal cannot fail to parse; a build that could is a bug, not a
		// runtime state to answer a caller for.
		panic("admin: the empty staged design does not parse: " + err.Error())
	}
	return d
}

// groupRepeats reports whether an instrument's rows repeat at one event when the
// document says nothing on the FormRef itself: the item group's own flag, which is
// where §3.3 puts it.
func groupRepeats(plan *odmBuildPlan, mdv *odmMetaDataVersion, formOID, instrument string) bool {
	form := mdv.formByOID(formOID)
	if form == nil {
		return false
	}
	for _, ref := range form.groupRefs {
		if target, ok := plan.groupByName[ref.oid]; ok && target.instrument == instrument && !target.matrix {
			return target.repeating
		}
	}
	return false
}

// claraRepeatingForm reports whether a CLARA event extension names this form among
// the instruments that repeat at that event. ODM 1.3.2 gives a FormRef no Repeating
// attribute, so a 1.3 document spells the per-pair flag here instead; the list holds
// form OIDs, which contain no whitespace by construction (REQ-ODM-029).
func claraRepeatingForm(ext map[string]string, formOID string) bool {
	for _, name := range strings.Fields(ext["repeatingForms"]) {
		if name == formOID {
			return true
		}
	}
	return false
}

// odmFieldEntry is one field waiting to be placed in an instrument: the row itself,
// the position its extension asked for, and the document order that breaks a tie
// between two fields the file ranks alike (§5).
type odmFieldEntry struct {
	position int // the extension's hint; document order breaks a tie
	order    int
	field    db.Field
	hasHint  bool
}

// holdsParent reports whether this instrument's entries already hold a field of
// this name — the one row several checkbox boxes share (§3.6).
func holdsParent(entries []odmFieldEntry, name string) bool {
	for _, e := range entries {
		if e.field.FieldName == name {
			return true
		}
	}
	return false
}

// --- the helpers the resolution asks for ---------------------------------------

// odmPutArm finds the design's arm with this number, creating it when a document
// names an arm only through its events: ODM carries no Arm element of its own, so
// the events arrive first and the arm follows them (§3.2). An unnamed arm stays
// unnamed, as CLARA's own single-arm creation leaves it (REQ-DB-011).
func odmPutArm(d *stagedDesign, armNum int, name string, problems *odmProblems) (*stagedArm, error) {
	if armNum <= 0 {
		return nil, problems.fatalRow("", "", "dangling_ref", fmt.Sprintf(
			"the arm number %d is not one a project can hold; arms are numbered from one", armNum))
	}
	if arm, found := d.arm(armNum); found {
		return arm, nil
	}
	return d.putArm(db.Arm{ArmNum: armNum, Name: sql.NullString{String: name, Valid: name != ""}}), nil
}

// odmFormGroupRef is one ItemGroupRef as its form declares it, annotated with the
// one fact ODM does not state: whether the group *is* the instrument — its own group,
// which CLARA names after it — or a matrix group sitting inside it (§3.3). Which of
// the two it is decides whether the group's rows are the instrument's instances or
// ordinary fields of its first one.
type odmFormGroupRef struct {
	oid        string
	isOwnGroup bool
}

// instrumentsGroupRefs lists the groups one form declares. A reference naming no
// ItemGroupDef drops out: checkDocument refuses the document for it already
// (REQ-ODM-054), so a partial answer here never hides a problem.
func instrumentsGroupRefs(mdv *odmMetaDataVersion, formOID string) []odmFormGroupRef {
	form := mdv.formByOID(formOID)
	if form == nil {
		return nil
	}
	own := odmOID("IG_", form.name)
	out := make([]odmFormGroupRef, 0, len(form.groupRefs))
	for _, ref := range form.groupRefs {
		group := mdv.groupByOID(ref.oid)
		if group == nil {
			continue
		}
		// The exporter writes an instrument's own group with the OID its name builds
		// (§3.3); a partner that spells the same thing differently is recognised by the
		// group carrying the form's own name.
		out = append(out, odmFormGroupRef{oid: ref.oid, isOwnGroup: ref.oid == own || group.name == form.name})
	}
	return out
}

// odmResolveFormName is the name one FormDef imported as. The answer comes from the
// plan, because building the instrument is where a name can be sanitized or
// de-duplicated (§6.2 step 2) — and an event's FormRef and a record's ItemGroupData
// have to land on the same instrument whatever the document called the form.
func odmResolveFormName(plan *odmBuildPlan, mdv *odmMetaDataVersion, formOID string) string {
	if name := plan.formNames[formOID]; name != "" {
		return name
	}
	if form := mdv.formByOID(formOID); form != nil {
		name, _ := odmSanitizeName(form.name, "instrument", 0)
		return name
	}
	return ""
}

// mergeArmMapping adds one event to one instrument's list in an arm's mapping and
// returns the result: a FormRef list is read pair by pair while setMappingForArm
// replaces the whole document for the arm (§6.2 step 4), so each pair arrives here
// with the pairs already collected behind it. A pair a document names twice stays once.
func mergeArmMapping(mapping map[string][]string, instrument, uniqueEvent string) map[string][]string {
	for _, u := range mapping[instrument] {
		if u == uniqueEvent {
			return mapping
		}
	}
	next := make(map[string][]string, len(mapping)+1)
	for name, uniques := range mapping {
		next[name] = uniques
	}
	next[instrument] = append(append([]string{}, mapping[instrument]...), uniqueEvent)
	return next
}

// knownValidationTypes is the field-validation vocabulary this installation can hold
// (§4.2): the built-in structured types plus every validation_types row. The importer
// asks it before keeping a document's word, so a type with no row here is dropped with
// notice rather than stored as a promise nothing will enforce (§3.4).
func (h *Handler) knownValidationTypes(ctx context.Context) (map[string]bool, error) {
	rows, err := h.Store.ListValidationTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows)+len(builtinValidationTypes))
	for name := range builtinValidationTypes {
		out[name] = true
	}
	for _, r := range rows {
		out[r.Name] = true
	}
	return out, nil
}

// odmValidationRegistry loads the registry the value validator reads (§4.2,
// REQ-VAL-042) — once per import, as the data import loads it once per call, and with
// the same direction of failure: a type neither built in nor in the registry is
// refused rather than waved through.
func (h *Handler) odmValidationRegistry(ctx context.Context) (*validate.Registry, error) {
	rows, err := h.Store.ListValidationTypes(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]validate.RegistryEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, validate.RegistryEntry{Name: r.Name, Regex: r.Regex, Builtin: r.Builtin})
	}
	return validate.NewRegistry(entries)
}
