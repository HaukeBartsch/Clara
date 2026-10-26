package admin

// The ODM metadata half of the project export (Design/ODM_Interchange_Design.md
// §3): the design tables read once into a plan, then written as Protocol,
// StudyEventDef, FormDef, ItemGroupDef, ItemDef, CodeList and MethodDef — in that
// order, which is the schema's sequence.
//
// The plan decides one thing the format cannot delegate: whether a field is
// declared at all. A column the applied level removes must not appear as an item,
// so the decision uses the pipeline's own classification
// (dataapi.ExportFieldKeptAt) rather than a second reading of the rules
// (REQ-ODM-021, §3.9). Declaring too little would leave a delivered value naming no
// item, so an item is kept when its column survives at the most permissive level
// any of its pairs holds — never under-describing, and over-describing only where
// a level genuinely delivers the value somewhere.

import (
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// kv is one attribute of an element written through the token API.
type kv struct{ name, value string }

// odmWriter wraps the encoder with the three shapes this document needs.
type odmWriter struct{ enc *xml.Encoder }

func (w odmWriter) start(name string, attrs ...kv) error {
	return w.enc.EncodeToken(odmStart(name, attrs...))
}

func (w odmWriter) end(name string) error {
	return w.enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}})
}

// elem writes a self-closed element in the ODM namespace.
func (w odmWriter) elem(name string, attrs ...kv) error {
	return w.enc.EncodeElement("", odmStart(name, attrs...))
}

// text writes `<name><TranslatedText xml:lang="…">value</TranslatedText></name>`,
// skipping empty values so the document carries no empty prose.
func (w odmWriter) text(name, value string) error {
	if value == "" {
		return nil
	}
	if err := w.start(name); err != nil {
		return err
	}
	el := xml.StartElement{
		Name: xml.Name{Local: "TranslatedText"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "xml:lang"}, Value: odmLanguage}},
	}
	if err := w.enc.EncodeElement(value, el); err != nil {
		return err
	}
	return w.end(name)
}

// ext writes a self-closed element in the CLARA extension namespace (§5): a
// conforming reader ignores it, a CLARA reader restores what ODM cannot express.
func (w odmWriter) ext(name string, attrs ...kv) error {
	el := xml.StartElement{Name: xml.Name{Local: "clara:" + name}}
	for _, a := range attrs {
		el.Attr = append(el.Attr, xml.Attr{Name: xml.Name{Local: a.name}, Value: a.value})
	}
	return w.enc.EncodeElement("", el)
}

// odmLanguage is the language a label written today carries. English is the source
// of truth and CLARA stores one label per field, not a per-language matrix, so one
// TranslatedText per item (REQ-UI-008).
const odmLanguage = "en"

// odmPlan is the export's view of a project's design.
type odmPlan struct {
	studyOID string
	mdvOID   string
	project  *db.Project
	arms     []db.Arm
	events   []odmEvent // candidate events in canonical order (GD-15)

	instruments []db.Instrument
	groups      []*odmGroupPlan // each instrument's own group, then the matrix groups
	codelists   []odmCodeListPlan
	methods     []odmMethodPlan

	identifier          string // GD-8: first field of the earliest instrument with fields
	identifierDelivered bool   // the applied level delivers that column (REQ-ODM-030)
}

type odmEvent struct {
	uniqueName  string
	label       string
	armNum      int
	armName     string
	period      *int64
	safeStart   *int64
	safeEnd     *int64
	instruments []odmFormRef // mapped instruments, in instrument order
	synthetic   bool         // the stand-in for a project with no events (noEventOID)
}

// odmFormRef is one event↔form pair: the instrument it maps and whether that pair
// repeats. The per-pair flag travels on the FormRef as ODM's own spelling, because
// §3.3's group-level marking cannot say which of an instrument's events repeat —
// and a project where one event repeats and another does not has to survive the
// round trip (REQ-ODM-050).
type odmFormRef struct {
	name      string
	repeating bool
}

type odmItemPlan struct {
	oid       string
	name      string // the CLARA column name it stands for (O-1)
	dataType  string
	label     string
	note      string
	min, max  string
	mandatory bool
	keySeq    bool
	methodOID string
	codelist  string
	field     db.Field
	choiceOf  string // set on a checkbox choice column: its parent field name
}

type odmGroupPlan struct {
	oid            string
	name           string
	repeating      bool
	instrumentName string // the instrument whose rows fill it
	formOID        string
	items          []*odmItemPlan
	statics        []db.Field // description/header/transfer rows (§3.7)
	isMatrix       bool
}

type odmCodeListPlan struct {
	oid     string
	name    string
	items   [][2]string // code, label in stored order
	integer bool        // the shared boolean list codes 1/0
}

type odmMethodPlan struct {
	oid, name, expression string
}

// newODMPlan reads the design into an export plan. ranks holds, per instrument, the
// most permissive export level any candidate pair of it holds (0 where it has none).
func newODMPlan(project *db.Project, arms []db.Arm, eventsByArm map[int][]EventObject,
	instruments []db.Instrument, fields []db.Field, pairs []db.InstrumentEvent,
	ranks map[int64]int) *odmPlan {

	sort.SliceStable(instruments, func(i, j int) bool { return instruments[i].Position < instruments[j].Position })
	byInstr := map[int64][]db.Field{}
	for _, f := range fields {
		byInstr[f.InstrumentID] = append(byInstr[f.InstrumentID], f)
	}
	for id, fs := range byInstr {
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].Position < fs[j].Position })
		byInstr[id] = fs
	}

	p := &odmPlan{
		studyOID: "CLARA-" + strconv.FormatInt(project.ID, 10),
		mdvOID:   "CLARA-" + strconv.FormatInt(project.ID, 10) + "-MDV",
		project:  project, arms: arms, instruments: instruments,
	}

	// Events with the instruments mapped to each (§3.2).
	eventNames := map[int64]string{}
	for _, list := range eventsByArm {
		for _, e := range list {
			eventNames[e.ID] = e.UniqueEventName
		}
	}
	mapped := map[int64][]odmFormRef{} // event ID → instruments, in instrument order
	repeating := map[[2]int64]bool{}
	for _, instr := range instruments {
		for _, pe := range pairs {
			if pe.InstrumentID != instr.ID {
				continue
			}
			mapped[pe.EventID] = append(mapped[pe.EventID], odmFormRef{name: instr.Name, repeating: pe.IsRepeating})
			repeating[[2]int64{instr.ID, pe.EventID}] = pe.IsRepeating
		}
	}
	for _, e := range eventsByArmOrder(eventsByArm) {
		armName := ""
		for _, a := range arms {
			if a.ArmNum == e.ArmNum {
				armName = a.Name.String
			}
		}
		p.events = append(p.events, odmEvent{
			uniqueName: e.UniqueEventName, label: e.EventName, armNum: e.ArmNum, armName: armName,
			period: e.Period, safeStart: e.SafeRegionStart, safeEnd: e.SafeRegionEnd,
			instruments: mapped[eventIDByName(eventsByArm, e.UniqueEventName)],
		})
	}

	// Items and groups (§3.3–§3.8).
	for _, instr := range instruments {
		g := &odmGroupPlan{
			oid: odmOID("IG_", instr.Name), name: instr.Name,
			instrumentName: instr.Name, formOID: odmOID("FRM_", instr.Name),
		}
		p.groups = append(p.groups, g)
		for _, f := range byInstr[instr.ID] {
			if p.identifier == "" {
				p.identifier = f.FieldName // GD-8, the first field of the earliest instrument with fields
			}
			kept := dataapi.ExportFieldKeptAt(f, p.identifier, ranks[instr.ID])
			if !kept {
				continue
			}
			target := g
			if f.MatrixGroup.String != "" {
				target = p.matrixGroup(f.MatrixGroup.String, g)
			}
			switch f.FieldType {
			case "description", "header", "transfer":
				g.statics = append(g.statics, f) // they carry no value, so no ItemDef (§3.7)
			case "checkbox":
				for _, ch := range odmChoices(f.Choices.String) {
					col := f.FieldName + "___" + ch[0] // the derived column name (REQ-EXP-053)
					target.items = append(target.items, &odmItemPlan{
						oid: odmOID("IT_", col), name: col, dataType: "integer",
						label: strings.TrimSpace(f.FieldLabel.String + ", " + ch[1]),
						field: f, choiceOf: f.FieldName, codelist: booleanCodeListOID,
					})
				}
				p.codelists = append(p.codelists, odmBooleanCodeList())
			default:
				item := &odmItemPlan{
					oid: odmOID("IT_", f.FieldName), name: f.FieldName, dataType: odmDataType(f),
					label: f.FieldLabel.String, note: f.FieldNote.String,
					min: f.ValidationMin.String, max: f.ValidationMax.String,
					mandatory: f.Required || f.FieldName == p.identifier,
					keySeq:    f.FieldName == p.identifier,
					field:     f,
				}
				if choices := odmChoices(f.Choices.String); len(choices) > 0 {
					item.codelist = odmOID("CL_", f.FieldName)
					p.codelists = append(p.codelists, odmCodeListPlan{
						oid: item.codelist, name: f.FieldName, items: choices})
				}
				if f.FieldType == "calculated" && f.Calculation.String != "" {
					item.methodOID = odmOID("MTH_", f.FieldName)
					p.methods = append(p.methods, odmMethodPlan{
						oid: item.methodOID, name: f.FieldName, expression: f.Calculation.String})
				}
				target.items = append(target.items, item)
			}
		}
	}

	// A group repeats when its pair repeats at any candidate event (REQ-DB-047).
	for _, instr := range instruments {
		g := p.groupFor(instr.Name)
		for _, e := range eventsByArmOrder(eventsByArm) {
			if g != nil && repeating[[2]int64{instr.ID, e.ID}] {
				g.repeating = true
			}
		}
	}

	// The record identifier follows the floor of no pair: it belongs to none, so
	// its own instrument's most permissive level decides whether the column is
	// delivered at all (REQ-EXP-003, GD-8).
	p.identifierDelivered = ranks[p.identifierInstrument(byInstr)] >= int(authz.RankExportFull)
	return p
}

func (p *odmPlan) groupFor(name string) *odmGroupPlan {
	for _, g := range p.groups {
		if g.name == name && !g.isMatrix {
			return g
		}
	}
	return nil
}

func (p *odmPlan) identifierInstrument(byInstr map[int64][]db.Field) int64 {
	for id, fs := range byInstr {
		for _, f := range fs {
			if f.FieldName == p.identifier {
				return id
			}
		}
	}
	return 0
}

// matrixGroup finds or creates the item group a matrix's rows share (§3.3).
func (p *odmPlan) matrixGroup(name string, owner *odmGroupPlan) *odmGroupPlan {
	for _, g := range p.groups {
		if g.name == name && g.isMatrix {
			return g
		}
	}
	g := &odmGroupPlan{oid: odmOID("IG_", name), name: name, formOID: owner.formOID,
		instrumentName: owner.instrumentName, isMatrix: true}
	p.groups = append(p.groups, g)
	return g
}

func (p *odmPlan) eventOID(uniqueEventName string) string {
	if uniqueEventName == "" || uniqueEventName == noEventName {
		return noEventOID // a project with no events groups its data under one stand-in
	}
	return odmOID("EVT_", uniqueEventName)
}

// events returns the events to declare: the project's own, or the single stand-in.
func (p *odmPlan) declaredEvents() []odmEvent {
	if len(p.events) > 0 {
		return p.events
	}
	all := make([]odmFormRef, 0, len(p.instruments))
	for _, i := range p.instruments {
		all = append(all, odmFormRef{name: i.Name})
	}
	return []odmEvent{{uniqueName: noEventName, instruments: all, synthetic: true}}
}

// groupRow is one ItemGroupData's content: the columns of one group this row
// carries a value for, in declared order.
type groupRow struct {
	oid       string
	repeatKey int
	items     []itemRow
}

type itemRow struct{ oid, value string }

// groupsForRow maps an exported row onto the groups it fills (REQ-ODM-024). A
// repeating instrument names its instance in redcap_repeat_instrument and
// _instance; another instrument's cells stay empty there, so nothing is written
// twice (REQ-API-155).
func (p *odmPlan) groupsForRow(row dataapi.ExportRow, repeatOf string, instance int) []groupRow {
	out := make([]groupRow, 0, len(p.groups))
	for _, g := range p.groups {
		gr := groupRow{oid: g.oid}
		if !g.isMatrix && g.instrumentName == repeatOf && instance > 1 {
			gr.repeatKey = instance
		}
		for _, it := range g.items {
			v, ok := row[it.name]
			if !ok || v == "" {
				continue // absent, never an empty ItemData (REQ-ODM-023)
			}
			gr.items = append(gr.items, itemRow{oid: it.oid, value: v})
		}
		if len(gr.items) > 0 {
			out = append(out, gr)
		}
	}
	return out
}

// writeMetadata writes Study with its MetaDataVersion (§3).
func (p *odmPlan) writeMetadata(w odmWriter) error {
	if err := w.start("Study", kv{"OID", p.studyOID}); err != nil {
		return err
	}
	if err := w.start("GlobalVariables"); err != nil {
		return err
	}
	for _, el := range [][2]string{
		{"StudyName", p.project.ProjectName},
		{"StudyDescription", p.project.ProjectName},
		{"ProtocolName", p.project.ProjectName}, // repeats the name for readers that show it (§3.1)
	} {
		if err := w.enc.EncodeElement(el[1], odmStart(el[0])); err != nil {
			return err
		}
	}
	if err := w.end("GlobalVariables"); err != nil {
		return err
	}

	if err := w.start("MetaDataVersion", kv{"OID", p.mdvOID}, kv{"Name", p.project.ProjectName}); err != nil {
		return err
	}
	for _, step := range []func(odmWriter) error{
		p.writeProtocol, p.writeEventDefs, p.writeFormDefs, p.writeItemGroups,
		p.writeItemDefs, p.writeCodeLists, p.writeMethods, p.writeExtension,
	} {
		if err := step(w); err != nil {
			return err
		}
	}
	if err := w.end("MetaDataVersion"); err != nil {
		return err
	}
	return w.end("Study")
}

func (p *odmPlan) writeProtocol(w odmWriter) error {
	if err := w.start("Protocol"); err != nil {
		return err
	}
	for i, ev := range p.declaredEvents() { // OrderNumber carries the canonical order (GD-15)
		if err := w.elem("StudyEventRef",
			kv{"StudyEventOID", p.eventOID(ev.uniqueName)},
			kv{"OrderNumber", strconv.Itoa(i + 1)}, kv{"Mandatory", noAttr}); err != nil {
			return err
		}
	}
	return w.end("Protocol")
}

func (p *odmPlan) writeEventDefs(w odmWriter) error {
	for _, ev := range p.declaredEvents() {
		attrs := []kv{{"OID", p.eventOID(ev.uniqueName)}, {"Name", ev.uniqueName},
			{"Repeating", noAttr}, {"Type", "Scheduled"}}
		if ev.armNum > 0 {
			category := "Arm " + strconv.Itoa(ev.armNum)
			if ev.armName != "" {
				category += " — " + ev.armName
			}
			attrs = append(attrs, kv{"Category", category})
		}
		if err := w.start("StudyEventDef", attrs...); err != nil {
			return err
		}
		for i, ref := range ev.instruments { // the event↔form association ODM defines (§3.2)
			attrs := []kv{{"FormOID", odmOID("FRM_", ref.name)}, {"Mandatory", noAttr},
				{"OrderNumber", strconv.Itoa(i + 1)}}
			// §3.3 marks the item group repeating, which cannot say *which* of an
			// instrument's events that means; the FormRef's own attribute does, so a
			// project where one pair repeats and another does not round-trips (REQ-ODM-050).
			if ref.repeating {
				attrs = append(attrs, kv{"Repeating", yesAttr})
			}
			if err := w.elem("FormRef", attrs...); err != nil {
				return err
			}
		}
		if ev.synthetic {
			if err := w.ext("Event", kv{"synthetic", "1"}); err != nil {
				return err
			}
		} else if err := w.ext("Event",
			kv{"armNum", strconv.Itoa(ev.armNum)}, kv{"eventName", ev.label},
			optKV("period", ev.period), optKV("safeRegionStart", ev.safeStart),
			optKV("safeRegionEnd", ev.safeEnd)); err != nil {
			return err
		}
		if err := w.end("StudyEventDef"); err != nil {
			return err
		}
	}
	return nil
}

func (p *odmPlan) writeFormDefs(w odmWriter) error {
	for _, instr := range p.instruments {
		formOID := odmOID("FRM_", instr.Name)
		if err := w.start("FormDef", kv{"OID", formOID}, kv{"Name", instr.Name},
			kv{"Repeating", noAttr}); err != nil {
			return err
		}
		order := 0
		for _, g := range p.groups {
			if g.formOID != formOID {
				continue
			}
			order++
			if err := w.elem("ItemGroupRef", kv{"ItemGroupOID", g.oid},
				kv{"Mandatory", noAttr}, kv{"OrderNumber", strconv.Itoa(order)}); err != nil {
				return err
			}
		}
		if err := w.ext("Form", kv{"isSurvey", bool01(instr.IsSurvey)},
			kv{"branchingLogic", instr.BranchingLogic.String}); err != nil {
			return err
		}
		if err := w.end("FormDef"); err != nil {
			return err
		}
	}
	return nil
}

func (p *odmPlan) writeItemGroups(w odmWriter) error {
	for _, g := range p.groups {
		if err := w.start("ItemGroupDef", kv{"OID", g.oid}, kv{"Name", g.name},
			kv{"Repeating", boolYN(g.repeating)}, kv{"SASDatasetName", g.name}); err != nil {
			return err
		}
		for i, it := range g.items {
			attrs := []kv{{"ItemOID", it.oid}, {"Mandatory", boolYN(it.mandatory)},
				{"OrderNumber", strconv.Itoa(i + 1)}}
			if it.keySeq {
				attrs = append(attrs, kv{"KeySequence", "1"}) // GD-8 restored on import (§6.2)
			}
			if it.methodOID != "" {
				attrs = append(attrs, kv{"MethodOID", it.methodOID})
			}
			if err := w.elem("ItemRef", attrs...); err != nil {
				return err
			}
		}
		for _, f := range g.statics { // §3.7: display-only rows live here, with their position
			if err := w.ext("StaticField",
				kv{"fieldName", f.FieldName}, kv{"fieldType", f.FieldType},
				kv{"label", f.FieldLabel.String}, kv{"instructions", f.Choices.String},
				kv{"position", strconv.Itoa(f.Position)},
				kv{"personalInformation", bool01(f.PersonalInformation)},
				kv{"directIdentifier", bool01(f.DirectIdentifier)},
				kv{"exportApproved", bool01(f.ExportApproved)}); err != nil {
				return err
			}
		}
		if err := w.end("ItemGroupDef"); err != nil {
			return err
		}
	}
	return nil
}

func (p *odmPlan) writeItemDefs(w odmWriter) error {
	for _, g := range p.groups {
		for _, it := range g.items {
			if err := w.start("ItemDef", kv{"OID", it.oid}, kv{"Name", it.name},
				kv{"DataType", it.dataType}, kv{"SASFieldName", it.name}); err != nil {
				return err
			}
			if err := w.text("Description", it.label); err != nil {
				return err
			}
			if err := w.text("Question", it.note); err != nil {
				return err
			}
			// 1.3.2 puts a bound in a CheckValue child, not a LowerBound/UpperBound
			// attribute (§3.4).
			for _, bound := range []struct{ comparator, value string }{{"GE", it.min}, {"LE", it.max}} {
				if bound.value == "" {
					continue
				}
				if err := w.start("RangeCheck", kv{"Comparator", bound.comparator}, kv{"SoftHard", "Hard"}); err != nil {
					return err
				}
				if err := w.elem("CheckValue", kv{"Value", bound.value}); err != nil {
					return err
				}
				if err := w.end("RangeCheck"); err != nil {
					return err
				}
			}
			if it.codelist != "" {
				if err := w.elem("CodeListRef", kv{"CodeListOID", it.codelist}); err != nil {
					return err
				}
			}
			if it.choiceOf != "" { // the derived column names its parent (§3.6)
				// The checkbox parent has no ItemDef of its own, so this extension row is
				// the only place its grammar and flags travel: without them a round trip
				// loses the choice labels, the required flag and the three sensitivity
				// flags, all of which §5 promises back (REQ-ODM-050/051).
				if err := w.ext("Choice",
					kv{"fieldName", it.choiceOf},
					kv{"choices", it.field.Choices.String},
					kv{"required", bool01(it.field.Required)},
					kv{"personalInformation", bool01(it.field.PersonalInformation)},
					kv{"directIdentifier", bool01(it.field.DirectIdentifier)},
					kv{"exportApproved", bool01(it.field.ExportApproved)}); err != nil {
					return err
				}
			} else if err := w.ext("Field",
				kv{"fieldType", it.field.FieldType},
				kv{"validationType", it.field.ValidationType.String},
				kv{"validationFormat", it.field.ValidationFormat.String},
				kv{"matrixGroup", it.field.MatrixGroup.String},
				kv{"sectionHeader", it.field.SectionHeader.String},
				kv{"annotation", it.field.FieldAnnotation.String},
				kv{"branchingLogic", it.field.BranchingLogic.String},
				kv{"position", strconv.Itoa(it.field.Position)},
				kv{"personalInformation", bool01(it.field.PersonalInformation)},
				kv{"directIdentifier", bool01(it.field.DirectIdentifier)},
				kv{"exportApproved", bool01(it.field.ExportApproved)}); err != nil {
				return err
			}
			if err := w.end("ItemDef"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *odmPlan) writeCodeLists(w odmWriter) error {
	seen := map[string]bool{}
	for _, cl := range p.codelists {
		if seen[cl.oid] {
			continue // CL_BOOLEAN is written once per document (§3.5)
		}
		seen[cl.oid] = true
		dataType := "text"
		if cl.integer {
			dataType = "integer"
		}
		if err := w.start("CodeList", kv{"OID", cl.oid}, kv{"Name", cl.name}, kv{"DataType", dataType}); err != nil {
			return err
		}
		for i, item := range cl.items {
			// EnumeratedItem carries no Decode, and every CLARA choice has a label (§3.5).
			if err := w.start("CodeListItem", kv{"CodedValue", item[0]},
				kv{"OrderNumber", strconv.Itoa(i + 1)}); err != nil {
				return err
			}
			if err := w.start("Decode"); err != nil {
				return err
			}
			if err := w.text2(item[1]); err != nil {
				return err
			}
			if err := w.end("Decode"); err != nil {
				return err
			}
			if err := w.end("CodeListItem"); err != nil {
				return err
			}
		}
		if err := w.end("CodeList"); err != nil {
			return err
		}
	}
	return nil
}

func (p *odmPlan) writeMethods(w odmWriter) error {
	for _, m := range p.methods {
		if err := w.start("MethodDef", kv{"OID", m.oid}, kv{"Name", m.name},
			kv{"Type", "Calculation"}); err != nil {
			return err
		}
		if err := w.text("Description", m.name); err != nil {
			return err
		}
		// The expression travels as text and is never evaluated here (REQ-ODM-047). It
		// sits inside a TranslatedText like every other piece of prose in the document
		// (§3.8), and it inherits the root's namespace declaration instead of restating
		// it: spelling the namespace on this one element would emit a second xmlns (§2).
		if err := w.start("FormalExpression"); err != nil {
			return err
		}
		if err := w.text2(m.expression); err != nil {
			return err
		}
		if err := w.end("FormalExpression"); err != nil {
			return err
		}
		if err := w.end("MethodDef"); err != nil {
			return err
		}
	}
	return nil
}

// writeExtension carries the project-level facts ODM 1.3.2 has no element for
// (§3.1, §5) — sponsor and investigator names, the REK number and dates, and arms
// that hold no events of their own. It sits last inside MetaDataVersion, where the
// schema's element-extension hook is.
func (p *odmPlan) writeExtension(w odmWriter) error {
	if err := w.ext("Project",
		kv{"organization", p.project.Organization}, kv{"piName", p.project.PIName},
		kv{"piEmail", p.project.PIEmail}, kv{"dmName", p.project.DMName.String},
		kv{"dmEmail", p.project.DMEmail.String}, kv{"rekNumber", p.project.RekNumber.String},
		kv{"rekStartDate", p.project.RekStartDate.String}, kv{"rekEndDate", p.project.RekEndDate.String},
		kv{"startDate", p.project.StartDate.String}, kv{"endDate", p.project.EndDate.String},
		kv{"recordNaming", p.project.RecordNaming}); err != nil {
		return err
	}
	for _, a := range p.arms {
		if err := w.ext("Arm", kv{"armNum", strconv.Itoa(a.ArmNum)},
			kv{"name", a.Name.String}, kv{"position", strconv.Itoa(a.Position)}); err != nil {
			return err
		}
	}
	return nil
}

// text2 writes a bare TranslatedText (the Decode child already opens the element).
func (w odmWriter) text2(value string) error {
	el := xml.StartElement{
		Name: xml.Name{Local: "TranslatedText"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "xml:lang"}, Value: odmLanguage}},
	}
	return w.enc.EncodeElement(value, el)
}

func optKV(name string, v *int64) kv {
	if v == nil {
		return kv{name, ""}
	}
	return kv{name, strconv.FormatInt(*v, 10)}
}

func bool01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func boolYN(b bool) string {
	if b {
		return yesAttr
	}
	return noAttr
}

// eventsByArmOrder flattens the canonical event map into one slice ordered by arm
// number, each arm's events already in canonical order (GD-15).
func eventsByArmOrder(byArm map[int][]EventObject) []EventObject {
	nums := make([]int, 0, len(byArm))
	for n := range byArm {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var out []EventObject
	for _, n := range nums {
		out = append(out, byArm[n]...)
	}
	return out
}

// eventIDByName is the reverse of the map CanonicalEvents returns: it hands back a
// single arm's events for a unique event name (an event belongs to one arm).
func eventIDByName(byArm map[int][]EventObject, uniqueName string) int64 {
	for _, list := range byArm {
		for _, e := range list {
			if e.UniqueEventName == uniqueName {
				return e.ID
			}
		}
	}
	return 0
}

// booleanCodeListOID is the shared codelist every checkbox choice column refers to
// (§3.5): the two words the raw export writes, emitted once per document.
const booleanCodeListOID = "CL_BOOLEAN"

func odmBooleanCodeList() odmCodeListPlan {
	return odmCodeListPlan{
		oid:     booleanCodeListOID,
		name:    "boolean",
		items:   [][2]string{{"1", "Checked"}, {"0", "Unchecked"}},
		integer: true,
	}
}

// fileOID identifies one download (§2): the project's OID plus the UTC instant the
// file was cut, so no two exports of a project share one and a partner can tell
// them apart.
func (p *odmPlan) fileOID(now time.Time) string {
	return p.studyOID + "-" + now.Format("20060102T150405Z")
}

// subjectKey is the SubjectKey one row's record carries: the record identifier
// verbatim where the applied level delivers that column (REQ-ODM-030), and
// otherwise the per-document surrogate the pipeline assigned in first-appearance
// order — never a hash of the stored identifier, and meaningless outside this file
// (REQ-ODM-031, O-5). The header says which of the two a reader is holding.
func (p *odmPlan) subjectKey(row dataapi.ExportRow) string {
	if p.identifierDelivered {
		return row[p.identifier]
	}
	return row[dataapi.SurrogateKeyColumn]
}

// undescribed lists the columns a row carries that this document declares no item
// for — the count §6.1 wants in the audit line, never silently dropped. The stream's
// own bookkeeping (event, repeat, surrogate), the record identifier and a form's
// completion status are not value columns: completion has no ODM carrier at all and
// is listed as a loss (§4) rather than counted as an omission here.
func (p *odmPlan) undescribed(row dataapi.ExportRow) []string {
	items := map[string]bool{}
	for _, g := range p.groups {
		for _, it := range g.items {
			items[it.name] = true
		}
	}
	meta := map[string]bool{
		"redcap_event_name":        true,
		"redcap_repeat_instrument": true,
		"redcap_repeat_instance":   true,
		dataapi.SurrogateKeyColumn: true,
		p.identifier:               true,
	}
	var out []string
	for name := range row {
		if meta[name] || strings.HasSuffix(name, "_complete") || items[name] {
			continue
		}
		out = append(out, name)
	}
	return out
}
