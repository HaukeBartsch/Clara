package admin

// The ODM reader (Design/ODM_Interchange_Design.md §6.2 steps 1–2, §9): the
// document's bytes into an intermediate model, plus the structural checks that
// decide whether the document contradicts itself.
//
// The parse is token-driven rather than struct-tagged for three reasons the
// requirements name: nesting depth has to be bounded before the parser recurses
// (REQ-ODM-014), content CLARA does not model has to be *counted* as ignored
// rather than silently dropped by an unmarshal into a fixed shape (REQ-ODM-053),
// and every reference — StudyEventRef, FormRef, ItemGroupRef, ItemRef,
// CodeListRef, MethodOID, and the OIDs the data section uses — has to be checked
// against what the document declares, because a self-contradictory file is a 400
// with nothing written (REQ-ODM-054).

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// --- the intermediate model ---------------------------------------------------

// odmDoc is one parsed ODM document; `study` is nil only for a document that
// already failed fatally. The file-level attributes — FileOID, CreationDateTime,
// FileType, ODMVersion, SourceSystem — are accepted and ignored: they identify a
// download, not a project (REQ-ODM-011), and the audit line records the request's
// own counts (§9).
type odmDoc struct {
	study        *odmStudy
	subjects     []*odmSubject
	subjectOrder []string // SubjectKey in first-appearance order
}

type odmStudy struct {
	oid              string
	studyName        string
	studyDescription string
	protocolName     string
	project          map[string]string   // clara:Project (§3.1, §5)
	arms             []map[string]string // clara:Arm — arms with no events of their own (§5)
	mdvs             []*odmMetaDataVersion
}

type odmMetaDataVersion struct {
	oid  string
	name string

	events    []*odmEventDef
	forms     []*odmFormDef
	groups    []*odmGroupDef
	items     []*odmItemDef
	codeLists map[string]*odmCodeList
	methods   map[string]*odmMethodDef
}

// odmEventFormRef is one FormRef inside a StudyEventDef: the event↔form pair the
// document states. (The export plan's own view of the same pair is odmFormRef, in
// odm_metadata.go — this is the reader's, and it carries ODM's attributes.)
type odmEventFormRef struct {
	oid   string
	order int
	// repeating is the FormRef's own attribute, kept as a pointer because absent
	// says something different from No: without it the importer falls back to the
	// item group's flag (§3.3), with it the pair speaks for itself (REQ-ODM-050).
	repeating *bool
}

type odmItemRef struct {
	oid         string
	order       int
	mandatory   bool
	keySequence bool // GD-8 restored from the schema's own key marker (§3.3)
	methodOID   string
}

type odmGroupRef struct {
	oid   string
	order int
}

type odmEventDef struct {
	oid       string
	name      string
	category  string
	eventType string
	repeating bool
	formRefs  []odmEventFormRef
	ext       map[string]string // clara:Event (§5)
}

type odmFormDef struct {
	oid       string
	name      string
	groupRefs []odmGroupRef
	ext       map[string]string // clara:Form (§5)
}

type odmGroupDef struct {
	oid       string
	name      string
	repeating bool
	itemRefs  []odmItemRef
	statics   []map[string]string // clara:StaticField rows (§3.7)
}

type odmItemDef struct {
	oid         string
	name        string
	dataType    string
	description string
	question    string
	min, max    string
	codelistOID string
	ext         map[string]string // clara:Field (§5)
	choiceExt   map[string]string // clara:Choice: this is a checkbox's derived column (§3.6)
}

type odmCodeItem struct {
	code  string
	label string
	order int
}

type odmCodeList struct {
	oid      string
	name     string
	dataType string
	items    []odmCodeItem
}

type odmMethodDef struct {
	oid        string
	name       string
	expression string // carried as text and never evaluated (REQ-ODM-047)
}

// odmSubject is one record's data: the SubjectKey it arrived under and the
// event/group/item tree beneath it, in document order.
type odmSubject struct {
	subjectKey string
	events     []*odmEventData
}

type odmEventData struct {
	eventOID string
	groups   []*odmGroupData
}

type odmGroupData struct {
	groupOID  string
	repeatKey int // 0 when the attribute is absent (the non-repeating case, §4)
	items     []odmItemValue
}

type odmItemValue struct {
	itemOID string
	value   string
	present bool // false for @IsNull="Yes" and for an item carrying no value (§3 of the requirements: REQ-ODM-043)
}

// --- the reader ----------------------------------------------------------------

// odmReader carries the decoder and the two bounds §9 fixes. Depth is counted on
// every token the reader hands out — including the ones inside a subtree it is
// skipping — so an unbounded-depth document is refused rather than walked.
type odmReader struct {
	dec      *xml.Decoder
	depth    int
	problems *odmProblems
	oids     map[string]string // every OID the document declared, against its kind

	// protocolRefs collects Protocol/StudyEventRef targets to check once the event
	// definitions are all in: a reference may point forward, as ODM allows.
	protocolRefs []string
}

// errODMFatal stops a parse that cannot continue. The problem is already on the
// report; the error only unwinds the recursion.
var errODMFatal = errors.New("odm: fatal document problem")

func (r *odmReader) fatal(code, message string) error {
	r.problems.add("", "", code, message)
	return errODMFatal
}

// enter counts one open element and refuses a document that nests past the bound.
func (r *odmReader) enter() error {
	r.depth++
	if r.depth > odmMaxDepth {
		return r.fatal("too_deep", fmt.Sprintf("an ODM document must not nest deeper than %d elements", odmMaxDepth))
	}
	return nil
}

// leave closes one element counted by enter.
func (r *odmReader) leave() { r.depth-- }

// skip consumes the rest of the element whose start token the caller already
// took, counting depth so that a hostile subtree cannot nest past the bound
// behind an element CLARA ignores.
func (r *odmReader) skip() error {
	open := 1
	for open > 0 {
		tok, err := r.dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return r.fatal("not_odm", "the document ends in the middle of an element")
			}
			return r.fatal("not_odm", "the document is not well-formed XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			open++
			if r.depth+open > odmMaxDepth {
				return r.fatal("too_deep", fmt.Sprintf("an ODM document must not nest deeper than %d elements", odmMaxDepth))
			}
		case xml.EndElement:
			open--
		case xml.ProcInst:
			// A processing instruction inside the body is content; encoding/xml
			// hands it out as its own token and it closes nothing.
			_ = t
		}
	}
	return nil
}

// claim records an OID against the kind that declared it, refusing a document
// that uses one name for two objects (REQ-ODM-054).
func (r *odmReader) claim(kind, oid string) error {
	if oid == "" {
		return r.fatal("dangling_ref", fmt.Sprintf("a %s element carries no OID", kind))
	}
	if seen, taken := r.oids[oid]; taken {
		return r.fatal("duplicate_oid", fmt.Sprintf("OID %q is declared twice (by %s and by %s)", oid, seen, kind))
	}
	r.oids[oid] = kind
	return nil
}

// odmParseDocument reads a body that has already passed odmCheckBody into the
// intermediate model, reporting what it ignores and refusing what contradicts
// itself. A returned doc is usable only when problems.failed() is false.
func odmParseDocument(body []byte, problems *odmProblems) (*odmDoc, error) {
	r := &odmReader{
		dec:      xml.NewDecoder(bytes.NewReader(body)),
		problems: problems,
		oids:     map[string]string{},
	}
	doc := &odmDoc{}

	// The root decides whether this is ODM at all (REQ-ODM-011).
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return doc, r.fatal("not_odm", "the request body holds no XML document")
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "ODM" || !odmNamespaceAccepted(start.Name.Space) {
			return doc, r.fatal("not_odm", fmt.Sprintf(
				"the document's root is %q in namespace %q; CLARA reads ODM 1.3 documents in %s",
				start.Name.Local, start.Name.Space, odmNamespace))
		}
		if err := r.enter(); err != nil {
			return doc, err
		}
		break
	}

	for {
		tok, err := r.dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return doc, r.fatal("not_odm", "the document ends before its root element closes")
			}
			return doc, r.fatal("not_odm", "the document is not well-formed XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return doc, err
			}
			switch {
			case odmLocal(t.Name) == "Study":
				study, err := r.parseStudy(t)
				if err != nil {
					return doc, err
				}
				doc.study = study
			case odmLocal(t.Name) == "ClinicalData":
				if err := r.parseClinicalData(t, doc); err != nil {
					return doc, err
				}
			default:
				if err := r.ignoreElement(t, "the top level"); err != nil {
					return doc, err
				}
			}
			r.leave()
		case xml.EndElement:
			r.leave()
			return doc, r.checkDocument(doc)
		}
	}
}

// odmNamespaceAccepted is the dialect test: ODM 1.3's namespace, which 1.3.2
// documents share (REQ-ODM-010/011). An empty space is accepted only for a child
// element that inherits the root's declaration — the root itself must state it.
func odmNamespaceAccepted(space string) bool {
	return space == odmNamespace
}

// odmLocal is the element name with any namespace prefix resolved away: CLARA
// reads ODM's own elements by their local name whatever prefix the producer used,
// and never mistakes a foreign element for one of them (the space is checked at
// the point where it decides meaning).
func odmLocal(n xml.Name) string { return n.Local }

// parseStudy reads Study: GlobalVariables, its MetaDataVersions and the study-level
// extension. The envelope's OID is recorded but not authoritative for CLARA
// identity (REQ-ODM-045).
func (r *odmReader) parseStudy(el xml.StartElement) (*odmStudy, error) {
	study := &odmStudy{}
	for _, a := range el.Attr {
		if odmLocal(a.Name) == "OID" {
			study.oid = a.Value
		}
	}
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return study, r.fatal("not_odm", "the Study element ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return study, err
			}
			switch {
			case odmLocal(t.Name) == "GlobalVariables":
				if err := r.parseGlobalVariables(t, study); err != nil {
					return study, err
				}
			case odmLocal(t.Name) == "MetaDataVersion":
				mdv, err := r.parseMetaDataVersion(t, study)
				if err != nil {
					return study, err
				}
				study.mdvs = append(study.mdvs, mdv)
			case r.isExtension(t.Name, claraExtNamespace):
				// Project and Arm are read from their attributes, and either way the
				// element's subtree is consumed here: its end tag must never read as
				// the end of Study.
				if odmLocal(t.Name) == "Project" {
					study.project = attrsOf(t)
				} else if odmLocal(t.Name) == "Arm" {
					study.arms = append(study.arms, attrsOf(t))
				}
				if err := r.skip(); err != nil {
					return study, err
				}
			default:
				if err := r.ignoreElement(t, "Study"); err != nil {
					return study, err
				}
			}
			r.leave()
		case xml.EndElement:
			return study, nil
		}
	}
}

func (r *odmReader) parseGlobalVariables(el xml.StartElement, study *odmStudy) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "GlobalVariables ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch odmLocal(t.Name) {
			case "StudyName", "StudyDescription", "ProtocolName":
				text, err := r.elementText(t)
				if err != nil {
					return err
				}
				switch odmLocal(t.Name) {
				case "StudyName":
					study.studyName = text
				case "StudyDescription":
					study.studyDescription = text
				default:
					study.protocolName = text
				}
			default:
				if err := r.ignoreElement(t, "GlobalVariables"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

// parseMetaDataVersion reads one metadata version. `study` receives the study-level
// extension rows CLARA writes at the end of MetaDataVersion — where the schema puts its
// element-extension hook (§3.1) — because that is what a CLARA file looks like; a
// document that carries them directly under Study instead is read there as well.
func (r *odmReader) parseMetaDataVersion(el xml.StartElement, study *odmStudy) (*odmMetaDataVersion, error) {
	mdv := &odmMetaDataVersion{codeLists: map[string]*odmCodeList{}, methods: map[string]*odmMethodDef{}}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			mdv.oid = a.Value
		case "Name":
			mdv.name = a.Value
		}
	}
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return mdv, r.fatal("not_odm", "MetaDataVersion ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return mdv, err
			}
			var err error
			switch odmLocal(t.Name) {
			case "Protocol":
				err = r.parseProtocol(t, mdv)
			case "StudyEventDef":
				err = r.parseStudyEventDef(t, mdv)
			case "FormDef":
				err = r.parseFormDef(t, mdv)
			case "ItemGroupDef":
				err = r.parseItemGroupDef(t, mdv)
			case "ItemDef":
				err = r.parseItemDef(t, mdv)
			case "CodeList":
				err = r.parseCodeList(t, mdv)
			case "MethodDef":
				err = r.parseMethodDef(t, mdv)
			case "Project", "Arm":
				// The study-level extension rows, which CLARA writes here (§3.1). A
				// same-named element of another namespace is unknown content and gets
				// the treatment every unknown element gets (REQ-ODM-053).
				if !r.isExtension(t.Name, claraExtNamespace) {
					err = r.ignoreElement(t, "MetaDataVersion")
					break
				}
				if odmLocal(t.Name) == "Project" {
					study.project = attrsOf(t)
				} else {
					study.arms = append(study.arms, attrsOf(t))
				}
				err = r.skip()
			case "DefAnnotation", "AdminData", "LogicReplayStudy", "Description":
				// Known ODM CLARA does not model (§8); the annotation of a definition
				// is a note, and AdminData names study users this feature does not carry.
				r.problems.add("", "", "element_ignored", "MetaDataVersion/"+odmLocal(t.Name)+" is not read (REQ-ODM-053)")
				err = r.skip()
			default:
				err = r.ignoreElement(t, "MetaDataVersion")
			}
			if err != nil {
				return mdv, err
			}
			r.leave()
		case xml.EndElement:
			return mdv, nil
		}
	}
}

// parseProtocol reads the StudyEventRef list. OrderNumber carries the canonical
// event order (GD-15) and is kept as the reference's position.
func (r *odmReader) parseProtocol(el xml.StartElement, mdv *odmMetaDataVersion) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "Protocol ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			if odmLocal(t.Name) == "StudyEventRef" {
				// The protocol's event references are checked against the event
				// definitions at the end of the parse (dangling_ref, REQ-ODM-054).
				r.protocolRefs = append(r.protocolRefs, attrOf(t, "StudyEventOID"))
				if err := r.skip(); err != nil {
					return err
				}
			} else if err := r.ignoreElement(t, "Protocol"); err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseStudyEventDef(el xml.StartElement, mdv *odmMetaDataVersion) error {
	def := &odmEventDef{eventType: "Scheduled"}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			def.oid = a.Value
		case "Name":
			def.name = a.Value
		case "Category":
			def.category = a.Value
		case "Type":
			def.eventType = a.Value
		case "Repeating":
			def.repeating = odmTrue(a.Value)
		}
	}
	if err := r.claim("StudyEventDef", def.oid); err != nil {
		return err
	}
	mdv.events = append(mdv.events, def)
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "StudyEventDef ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch {
			case odmLocal(t.Name) == "FormRef":
				def.formRefs = append(def.formRefs, odmEventFormRef{
					oid:       attrOf(t, "FormOID"),
					order:     intAttr(t, "OrderNumber"),
					repeating: yesNoAttr(t, "Repeating"),
				})
				if err := r.skip(); err != nil {
					return err
				}
			case r.isExtension(t.Name, claraExtNamespace) && odmLocal(t.Name) == "Event":
				def.ext = attrsOf(t)
				if err := r.skip(); err != nil {
					return err
				}
			default:
				if err := r.ignoreElement(t, "StudyEventDef"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseFormDef(el xml.StartElement, mdv *odmMetaDataVersion) error {
	def := &odmFormDef{}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			def.oid = a.Value
		case "Name":
			def.name = a.Value
		}
	}
	if err := r.claim("FormDef", def.oid); err != nil {
		return err
	}
	mdv.forms = append(mdv.forms, def)
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "FormDef ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch {
			case odmLocal(t.Name) == "ItemGroupRef":
				def.groupRefs = append(def.groupRefs, odmGroupRef{
					oid:   attrOf(t, "ItemGroupOID"),
					order: intAttr(t, "OrderNumber"),
				})
				if err := r.skip(); err != nil {
					return err
				}
			case r.isExtension(t.Name, claraExtNamespace) && odmLocal(t.Name) == "Form":
				def.ext = attrsOf(t)
				if err := r.skip(); err != nil {
					return err
				}
			default:
				if err := r.ignoreElement(t, "FormDef"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseItemGroupDef(el xml.StartElement, mdv *odmMetaDataVersion) error {
	def := &odmGroupDef{}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			def.oid = a.Value
		case "Name":
			def.name = a.Value
		case "Repeating":
			def.repeating = odmTrue(a.Value)
		}
	}
	if err := r.claim("ItemGroupDef", def.oid); err != nil {
		return err
	}
	mdv.groups = append(mdv.groups, def)
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "ItemGroupDef ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch {
			case odmLocal(t.Name) == "ItemRef":
				ref := odmItemRef{
					oid:         attrOf(t, "ItemOID"),
					order:       intAttr(t, "OrderNumber"),
					mandatory:   odmTrue(attrOf(t, "Mandatory")),
					keySequence: intAttr(t, "KeySequence") == 1, // GD-8's own spelling (§3.3)
					methodOID:   attrOf(t, "MethodOID"),
				}
				def.itemRefs = append(def.itemRefs, ref)
				if err := r.skip(); err != nil {
					return err
				}
			case r.isExtension(t.Name, claraExtNamespace) && odmLocal(t.Name) == "StaticField":
				def.statics = append(def.statics, attrsOf(t))
				if err := r.skip(); err != nil {
					return err
				}
			default:
				if err := r.ignoreElement(t, "ItemGroupDef"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseItemDef(el xml.StartElement, mdv *odmMetaDataVersion) error {
	def := &odmItemDef{}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			def.oid = a.Value
		case "Name":
			def.name = a.Value
		case "DataType":
			def.dataType = a.Value
		}
	}
	if err := r.claim("ItemDef", def.oid); err != nil {
		return err
	}
	mdv.items = append(mdv.items, def)
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "ItemDef ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			var err error
			switch {
			case odmLocal(t.Name) == "Description":
				def.description, err = r.elementText(t)
			case odmLocal(t.Name) == "Question":
				def.question, err = r.elementText(t)
			case odmLocal(t.Name) == "CodeListRef":
				def.codelistOID = attrOf(t, "CodeListOID")
				err = r.skip()
			case odmLocal(t.Name) == "RangeCheck":
				err = r.parseRangeCheck(t, def)
			case r.isExtension(t.Name, claraExtNamespace) && odmLocal(t.Name) == "Field":
				def.ext = attrsOf(t)
				err = r.skip()
			case r.isExtension(t.Name, claraExtNamespace) && odmLocal(t.Name) == "Choice":
				def.choiceExt = attrsOf(t)
				err = r.skip()
			default:
				err = r.ignoreElement(t, "ItemDef")
			}
			if err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

// parseRangeCheck reads one bound. Only a plain GE/LE minimum or maximum is
// modelled; anything else in the element is a loss named in §8.
func (r *odmReader) parseRangeCheck(el xml.StartElement, def *odmItemDef) error {
	comparator := attrOf(el, "Comparator")
	var value string
	other := false
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "RangeCheck ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			if odmLocal(t.Name) == "CheckValue" {
				value = attrOf(t, "Value")
			} else {
				other = true // a DateTimeStamp or a soft bound CLARA does not carry
			}
			if err := r.skip(); err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			if other {
				r.problems.add("", def.name, "element_ignored",
					"the RangeCheck of field "+def.name+" holds a bound CLARA does not model (REQ-ODM-053)")
				return nil
			}
			switch comparator {
			case "GE":
				def.min = value
			case "LE":
				def.max = value
			default:
				r.problems.add("", def.name, "element_ignored",
					fmt.Sprintf("the RangeCheck of field %s compares with %q; CLARA stores only a minimum and a maximum", def.name, comparator))
			}
			return nil
		}
	}
}

func (r *odmReader) parseCodeList(el xml.StartElement, mdv *odmMetaDataVersion) error {
	cl := &odmCodeList{}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			cl.oid = a.Value
		case "Name":
			cl.name = a.Value
		case "DataType":
			cl.dataType = a.Value
		}
	}
	if err := r.claim("CodeList", cl.oid); err != nil {
		return err
	}
	if _, dup := mdv.codeLists[cl.oid]; dup {
		return r.fatal("duplicate_oid", fmt.Sprintf("CodeList OID %q is declared twice", cl.oid))
	}
	mdv.codeLists[cl.oid] = cl
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "CodeList ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch odmLocal(t.Name) {
			case "CodeListItem":
				item := odmCodeItem{code: attrOf(t, "CodedValue"), order: intAttr(t, "OrderNumber")}
				if err := r.parseCodeListItem(t, &item); err != nil {
					return err
				}
				cl.items = append(cl.items, item)
			case "EnumeratedItem":
				// §3.5 says CLARA writes CodeListItem; a partner may still send the
				// other member of the choice pair, and its code is all it carries.
				item := odmCodeItem{code: attrOf(t, "CodedValue"), order: intAttr(t, "OrderNumber")}
				cl.items = append(cl.items, item)
				if err := r.skip(); err != nil {
					return err
				}
			default:
				if err := r.ignoreElement(t, "CodeList"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseCodeListItem(el xml.StartElement, item *odmCodeItem) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "CodeListItem ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			if odmLocal(t.Name) == "Decode" {
				// Decode wraps a TranslatedText rather than holding text itself, so it is
				// read as prose: the first language wins and a further one is named (§2).
				text, err := r.elementText(t)
				if err != nil {
					return err
				}
				item.label = text
			} else if err := r.ignoreElement(t, "CodeListItem"); err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseMethodDef(el xml.StartElement, mdv *odmMetaDataVersion) error {
	m := &odmMethodDef{}
	for _, a := range el.Attr {
		switch odmLocal(a.Name) {
		case "OID":
			m.oid = a.Value
		case "Name":
			m.name = a.Value
		}
	}
	if err := r.claim("MethodDef", m.oid); err != nil {
		return err
	}
	if _, dup := mdv.methods[m.oid]; dup {
		return r.fatal("duplicate_oid", fmt.Sprintf("MethodDef OID %q is declared twice", m.oid))
	}
	mdv.methods[m.oid] = m
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "MethodDef ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			// The body is read as text and never evaluated (REQ-ODM-047): a method's
			// formulation reaches CLARA as a calculated field's expression, which the
			// expression grammar then judges. ALGORITHM and COMMENT are §8 losses.
			switch odmLocal(t.Name) {
			case "FormalExpression", "Algorithm":
				text, err := r.elementText(t)
				if err != nil {
					return err
				}
				m.expression = text
			default:
				if err := r.ignoreElement(t, "MethodDef"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

// parseClinicalData reads one ClinicalData section's SubjectData tree. Several
// sections in one document are allowed and merge into the same records, which is
// what a partner that splits a study across sections produces.
func (r *odmReader) parseClinicalData(el xml.StartElement, doc *odmDoc) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "ClinicalData ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			if odmLocal(t.Name) == "SubjectData" {
				if err := r.parseSubjectData(t, doc); err != nil {
					return err
				}
			} else if err := r.ignoreElement(t, "ClinicalData"); err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseSubjectData(el xml.StartElement, doc *odmDoc) error {
	key := attrOf(el, "SubjectKey")
	if key == "" {
		return r.fatal("dangling_ref", "a SubjectData element carries no SubjectKey")
	}
	subject := doc.subjectByKey(key)
	if subject == nil {
		subject = &odmSubject{subjectKey: key}
		doc.subjects = append(doc.subjects, subject)
		doc.subjectOrder = append(doc.subjectOrder, key)
	}
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "SubjectData ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch odmLocal(t.Name) {
			case "StudyEventData":
				event := &odmEventData{eventOID: attrOf(t, "StudyEventOID")}
				if err := r.parseStudyEventData(t, event); err != nil {
					return err
				}
				subject.events = append(subject.events, event)
			case "ItemGroupData":
				// A partner may put a group directly under SubjectData; it belongs to
				// no event, so it lands in the document's event-less slot (§4).
				event := subject.eventFor("")
				group, err := r.parseItemGroupData(t)
				if err != nil {
					return err
				}
				event.groups = append(event.groups, group)
			default:
				if err := r.ignoreElement(t, "SubjectData"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseStudyEventData(el xml.StartElement, event *odmEventData) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "StudyEventData ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			switch odmLocal(t.Name) {
			case "ItemGroupData":
				group, err := r.parseItemGroupData(t)
				if err != nil {
					return err
				}
				event.groups = append(event.groups, group)
			case "FormData":
				// The optional collection-instance level is walked through, not
				// modelled: CLARA's instances are the ItemGroupData rows themselves (§4).
				if err := r.parseFormData(t, event); err != nil {
					return err
				}
			default:
				if err := r.ignoreElement(t, "StudyEventData"); err != nil {
					return err
				}
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseFormData(el xml.StartElement, event *odmEventData) error {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return r.fatal("not_odm", "FormData ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return err
			}
			if odmLocal(t.Name) == "ItemGroupData" {
				group, err := r.parseItemGroupData(t)
				if err != nil {
					return err
				}
				event.groups = append(event.groups, group)
			} else if err := r.ignoreElement(t, "FormData"); err != nil {
				return err
			}
			r.leave()
		case xml.EndElement:
			return nil
		}
	}
}

func (r *odmReader) parseItemGroupData(el xml.StartElement) (*odmGroupData, error) {
	group := &odmGroupData{groupOID: attrOf(el, "ItemGroupOID")}
	if raw := attrOf(el, "ItemGroupRepeatKey"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 {
			return group, r.fatal("dangling_ref",
				fmt.Sprintf("ItemGroupData repeats with the key %q, which is not an instance number", raw))
		}
		group.repeatKey = n
	}
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return group, r.fatal("not_odm", "ItemGroupData ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return group, err
			}
			if odmLocal(t.Name) == "ItemData" {
				item := odmItemValue{itemOID: attrOf(t, "ItemOID"), present: true}
				for _, a := range t.Attr {
					switch odmLocal(a.Name) {
					case "Value":
						item.value = a.Value
					case "IsNull":
						// 1.3.2's null indicator: an item that is null carries no
						// value, and CLARA stores nothing for it (REQ-ODM-043).
						if odmTrue(a.Value) {
							item.present = false
							item.value = ""
						}
					}
				}
				group.items = append(group.items, item)
				if err := r.skip(); err != nil {
					return group, err
				}
			} else if err := r.ignoreElement(t, "ItemGroupData"); err != nil {
				return group, err
			}
			r.leave()
		case xml.EndElement:
			return group, nil
		}
	}
}

// ignoreElement reports one element CLARA does not read and consumes its subtree.
// Elements in a namespace this feature knows nothing about get the same treatment
// as ODM's own unmodelled ones — named, never silently dropped (REQ-ODM-053).
func (r *odmReader) ignoreElement(el xml.StartElement, where string) error {
	name := odmLocal(el.Name)
	if el.Name.Space != "" && el.Name.Space != odmNamespace {
		r.problems.add("", "", "element_ignored",
			fmt.Sprintf("%s/%s in namespace %s is not read (REQ-ODM-053)", where, name, el.Name.Space))
	} else {
		r.problems.add("", "", "element_ignored", fmt.Sprintf("%s/%s is not read (REQ-ODM-053)", where, name))
	}
	return r.skip()
}

// isExtension reports whether an element belongs to a namespace the importer
// reads by prefix — the CLARA extension only; anything else is ignored content.
func (r *odmReader) isExtension(name xml.Name, namespace string) bool {
	return name.Space == namespace
}

// elementText reads an element that holds prose: its TranslatedText children, or
// plain character content when a producer writes the text directly. The first
// language wins and a further one is named as a loss (§2, §8).
func (r *odmReader) elementText(el xml.StartElement) (string, error) {
	var text string
	seen := false
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return text, r.fatal("not_odm", "a text element ends before it closes")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := r.enter(); err != nil {
				return text, err
			}
			if odmLocal(t.Name) == "TranslatedText" {
				one, err := r.translatedText(t)
				if err != nil {
					return text, err
				}
				if !seen {
					text, seen = one, true
				} else {
					r.problems.add("", "", "element_ignored",
						"a second language of one label is not stored; the first is kept (REQ-ODM-053)")
				}
			} else if err := r.ignoreElement(t, odmLocal(el.Name)); err != nil {
				return text, err
			}
			r.leave()
		case xml.CharData:
			if !seen && strings.TrimSpace(string(t)) != "" {
				text = string(t)
			}
		case xml.EndElement:
			return strings.TrimSpace(text), nil
		}
	}
}

// translatedText reads the text of one TranslatedText element, whose start token
// the caller already took.
func (r *odmReader) translatedText(el xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return "", r.fatal("not_odm", "TranslatedText ends before it closes")
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			// Markup inside a label is text, not structure: keep its characters and
			// say so — imported text renders escaped everywhere (REQ-ODM-062).
			r.problems.add("", "", "attribute_ignored",
				"a label holds markup that is stored as plain text (REQ-ODM-062)")
			if err := r.skip(); err != nil {
				return "", err
			}
		case xml.EndElement:
			return strings.TrimSpace(b.String()), nil
		}
	}
}

// --- the document-level checks --------------------------------------------------

// checkDocument applies the rules that need the whole document: a metadata
// version must exist, only the first of several builds, and every reference in
// either half must name something the document declares (REQ-ODM-054).
func (r *odmReader) checkDocument(doc *odmDoc) error {
	if doc.study == nil {
		r.problems.add("", "", "no_metadataversion", "the document holds no Study with a MetaDataVersion")
		return errODMFatal
	}
	if len(doc.study.mdvs) == 0 {
		r.problems.add("", "", "no_metadataversion", "the Study holds no MetaDataVersion")
		return errODMFatal
	}
	if extra := len(doc.study.mdvs) - 1; extra > 0 {
		r.problems.add("", "", "mdv_ignored",
			fmt.Sprintf("the document declares %d further MetaDataVersion elements; only the first builds (REQ-ODM-053)", extra))
	}
	mdv := doc.study.mdvs[0]

	for _, oid := range r.protocolRefs {
		if mdv.eventByOID(oid) == nil {
			return r.fatal("dangling_ref", fmt.Sprintf("Protocol names the StudyEventRef %q, which no StudyEventDef declares", oid))
		}
	}
	for _, event := range mdv.events {
		for _, ref := range event.formRefs {
			if mdv.formByOID(ref.oid) == nil {
				return r.fatal("dangling_ref", fmt.Sprintf(
					"the event %q references the form %q, which no FormDef declares", event.name, ref.oid))
			}
		}
	}
	for _, form := range mdv.forms {
		for _, ref := range form.groupRefs {
			if mdv.groupByOID(ref.oid) == nil {
				return r.fatal("dangling_ref", fmt.Sprintf(
					"the form %q references the item group %q, which no ItemGroupDef declares", form.name, ref.oid))
			}
		}
	}
	for _, group := range mdv.groups {
		if len(group.itemRefs) == 0 && len(group.statics) == 0 {
			return r.fatal("empty_itemgroup", fmt.Sprintf(
				"the item group %q holds no ItemRef and no static field", group.name))
		}
		for _, ref := range group.itemRefs {
			if mdv.itemByOID(ref.oid) == nil {
				return r.fatal("dangling_ref", fmt.Sprintf(
					"the item group %q references the item %q, which no ItemDef declares", group.name, ref.oid))
			}
			if ref.methodOID != "" && mdv.methods[ref.methodOID] == nil {
				return r.fatal("dangling_ref", fmt.Sprintf(
					"the item reference to %q names the method %q, which no MethodDef declares", ref.oid, ref.methodOID))
			}
		}
	}
	for _, item := range mdv.items {
		if item.codelistOID != "" && mdv.codeLists[item.codelistOID] == nil {
			return r.fatal("dangling_ref", fmt.Sprintf(
				"the item %q references the codelist %q, which no CodeList declares", item.name, item.codelistOID))
		}
	}
	// The data half must name what the metadata half declares. An ItemGroupData
	// without a group is a contradiction of the document (fatal); an ItemData
	// without an item stays a per-record problem, as the data import reports an
	// unknown key (§6.2 step 5).
	for _, subject := range doc.subjects {
		if countItemData(subject) == 0 {
			return r.fatal("empty_itemgroup", fmt.Sprintf(
				"the SubjectData %q carries no ItemData", subject.subjectKey))
		}
		for _, event := range subject.events {
			for _, group := range event.groups {
				if mdv.groupByOID(group.groupOID) == nil {
					return r.fatal("dangling_ref", fmt.Sprintf(
						"the record %q fills the item group %q, which no ItemGroupDef declares",
						subject.subjectKey, group.groupOID))
				}
			}
		}
	}
	if len(doc.study.mdvs) > 0 && mdv.items == nil && mdv.groups == nil {
		// A metadata version that describes nothing cannot build a project.
		r.problems.add("", "", "no_metadataversion", "the MetaDataVersion declares no item group and no item")
		return errODMFatal
	}
	return nil
}

// --- lookups and attribute helpers ---------------------------------------------

func (d *odmDoc) subjectByKey(key string) *odmSubject {
	for _, s := range d.subjects {
		if s.subjectKey == key {
			return s
		}
	}
	return nil
}

// eventFor returns the record's data for an event OID, creating the empty slot
// for the event-less case (the empty OID) that parseSubjectData fills in place.
func (s *odmSubject) eventFor(eventOID string) *odmEventData {
	for _, e := range s.events {
		if e.eventOID == eventOID {
			return e
		}
	}
	e := &odmEventData{eventOID: eventOID}
	s.events = append(s.events, e)
	return e
}

func (m *odmMetaDataVersion) eventByOID(oid string) *odmEventDef {
	for _, e := range m.events {
		if e.oid == oid {
			return e
		}
	}
	return nil
}

func (m *odmMetaDataVersion) formByOID(oid string) *odmFormDef {
	for _, f := range m.forms {
		if f.oid == oid {
			return f
		}
	}
	return nil
}

func (m *odmMetaDataVersion) groupByOID(oid string) *odmGroupDef {
	for _, g := range m.groups {
		if g.oid == oid {
			return g
		}
	}
	return nil
}

func (m *odmMetaDataVersion) itemByOID(oid string) *odmItemDef {
	for _, i := range m.items {
		if i.oid == oid {
			return i
		}
	}
	return nil
}

// countItemData counts the ItemData elements a record carries. Every one counts,
// including an @IsNull stand-in: REQ-ODM-054 refuses a SubjectData without items,
// not one whose values all happen to be null.
func countItemData(s *odmSubject) int {
	n := 0
	for _, event := range s.events {
		for _, group := range event.groups {
			n += len(group.items)
		}
	}
	return n
}

func attrOf(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if odmLocal(a.Name) == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

// attrsOf flattens an extension element's attributes. Namespaces are dropped:
// the extension spells its own vocabulary and carries no foreign attributes.
func attrsOf(el xml.StartElement) map[string]string {
	out := make(map[string]string, len(el.Attr))
	for _, a := range el.Attr {
		out[odmLocal(a.Name)] = a.Value
	}
	return out
}

func intAttr(el xml.StartElement, name string) int {
	raw := attrOf(el, name)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return n
}

// yesNoAttr reads an ODM YesNo attribute, keeping "absent" distinct from "No": a
// document that says nothing defers to another element's word, and one that says
// No has decided (REQ-ODM-050).
func yesNoAttr(el xml.StartElement, name string) *bool {
	raw := attrOf(el, name)
	if raw == "" {
		return nil
	}
	yes := odmTrue(raw)
	return &yes
}

// odmTrue is ODM's boolean read both ways: the schema spells it Y and N, and producers
// — CLARA's own exporter among them — write Yes and No. A reader that knew only one of
// the two would import a mandatory field as optional, which is the quieter half of the
// same mistake REQ-ODM-042 refuses on the value side.
func odmTrue(v string) bool {
	v = strings.TrimSpace(v)
	return strings.EqualFold(v, "Y") || strings.EqualFold(v, "Yes")
}

func extBool(attrs map[string]string, name string) bool {
	return attrs[name] == "1" || strings.EqualFold(attrs[name], "true")
}

func extStr(attrs map[string]string, name string) string {
	if attrs == nil {
		return ""
	}
	return attrs[name]
}
