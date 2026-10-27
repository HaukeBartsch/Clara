package admin

// The ODM export's own contract (REQ-API-169, Design/ODM_Interchange_Design.md §2):
// an installation administrator downloads the project as one well-formed ODM 1.3
// document; anyone else gets the uniform 403 and nothing is audited (REQ-AUD-004).

import (
	"context"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"csms/api/internal/audit"
	"net/http"
	"strings"
	"testing"
	"time"
)

// odmAttr reads one attribute off a start element, "" when absent.
func odmAttr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// odmNesting is what the data half of a document nests inside what, read back from
// an export so an assertion can name an element rather than grep for a substring.
type odmNesting struct {
	groups   int // ItemGroupData elements seen
	parents  map[string]int
	naked    []string                   // groups whose parent is not FormData, as "<parent>/<group>"
	inForm   map[string][]string        // FormOID → ItemGroupOIDs inside it, in order
	formKeys map[string][]string        // FormOID → @FormRepeatKey of each FormData, "" when unwritten
	formDefs map[string]bool            // declared FormDef OIDs
	formRefs map[string]map[string]bool // FormDef OID → ItemGroupRef OIDs it declares
}

func odmWalk(t *testing.T, body string) odmNesting {
	t.Helper()
	out := odmNesting{parents: map[string]int{}, inForm: map[string][]string{},
		formKeys: map[string][]string{}, formDefs: map[string]bool{},
		formRefs: map[string]map[string]bool{}}
	dec := xml.NewDecoder(strings.NewReader(body))
	stack := make([]xml.StartElement, 0, 16)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			stack = append(stack, el)
			switch el.Name.Local {
			case "FormDef":
				out.formDefs[odmAttr(el, "OID")] = true
				out.formRefs[odmAttr(el, "OID")] = map[string]bool{}
			case "ItemGroupRef":
				if len(stack) >= 2 {
					if form := stack[len(stack)-2]; form.Name.Local == "FormDef" {
						out.formRefs[odmAttr(form, "OID")][odmAttr(el, "ItemGroupOID")] = true
					}
				}
			case "ItemGroupData":
				parent := ""
				if len(stack) >= 2 {
					parent = stack[len(stack)-2].Name.Local
				}
				out.parents[parent]++
				out.groups++
				group := odmAttr(el, "ItemGroupOID")
				if parent != "FormData" {
					out.naked = append(out.naked, parent+"/"+group)
					break
				}
				form := stack[len(stack)-2]
				formOID := odmAttr(form, "FormOID")
				out.inForm[formOID] = append(out.inForm[formOID], group)
				out.formKeys[formOID] = append(out.formKeys[formOID], odmAttr(form, "FormRepeatKey"))
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return out
}

// TestExportODMDataNesting holds each dialect to its own schema rule (REQ-ODM-027):
// ODM 1.3.2 lets StudyEventData hold only AuditRecord, Signature, Annotation and
// FormData, so a clinical ItemGroupData has no other parent; ODM 2.0 removed the
// level and hangs groups directly off StudyEventData. TestExportODMShape above runs
// on a project with no records and could not have seen the difference — which is how
// one shape served both dialects while matching neither's data rules in 1.3.
func TestExportODMDataNesting(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-nesting@example.org")
	projectID := e.seedInterchange()

	one := func(version string) string {
		e.t.Helper()
		path := "/api/v1/projects/" + itoa(projectID) + "/export/odm"
		if version != "" {
			path += "?odm_version=" + version
		}
		rec := e.do("GET", path, nil, admin)
		if rec.Code != http.StatusOK {
			e.t.Fatalf("odm_version=%q: status %d body %s", version, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	v13 := odmWalk(t, one(""))
	if v13.groups == 0 {
		t.Fatalf("the seeded project exported no ItemGroupData at all")
	}
	if len(v13.naked) > 0 {
		t.Errorf("ODM 1.3 leaves ItemGroupData outside a FormData: %v (REQ-ODM-027)", v13.naked)
	}
	for formOID, groups := range v13.inForm {
		if !v13.formDefs[formOID] {
			t.Errorf("FormData names %q, which no FormDef declares", formOID)
		}
		for _, g := range groups {
			if !v13.formRefs[formOID][g] {
				t.Errorf("%s holds %s, but that FormDeclares no ItemGroupRef to it", formOID, g)
			}
		}
	}
	// The instrument that repeats at this event gets one FormData per instance; the
	// first leaves @FormRepeatKey unwritten, as @ItemGroupRepeatKey does (§4).
	keys := v13.formKeys["FRM_adverse_event"]
	if len(keys) != 2 || keys[0] != "" || keys[1] != "2" {
		t.Errorf("FRM_adverse_event FormRepeatKeys = %q, want [\"\" 2]", keys)
	}
	// An instrument that does not repeat never writes the attribute, whichever event
	// it appears at (FRM_vitals maps to two events here).
	for _, k := range v13.formKeys["FRM_vitals"] {
		if k != "" {
			t.Errorf("FRM_vitals wrote FormRepeatKey %q although the instrument does not repeat", k)
		}
	}
	// A matrix group belongs inside its instrument's form, not beside it (§3.3).
	if !odmHasGroup(v13.inForm["FRM_vitals"], "IG_bp_group") {
		t.Errorf("the matrix group is not inside FRM_vitals: %v", v13.inForm)
	}

	v20 := odmWalk(t, one("2.0"))
	if v20.groups == 0 {
		t.Fatalf("the 2.0 export carried no ItemGroupData")
	}
	if v20.parents["FormData"] > 0 || strings.Contains(one("2.0"), "<FormData") {
		t.Errorf("ODM 2.0 wrote a FormData level that 2.0 removed (REQ-ODM-027)")
	}
	for parent, n := range v20.parents {
		if parent != "StudyEventData" {
			t.Errorf("ODM 2.0 nests ItemGroupData in %q %d time(s), want StudyEventData", parent, n)
		}
	}
}

// TestExportODMRoundTrip is the same promise on the default 1.3 dialect, which had no
// round-trip test of its own: only the 2.0 path did, so a 1.3 document that nested its
// data the way 2.0 does — and a per-pair repeating flag 1.3 cannot legally carry on a
// FormRef — passed by agreeing with itself at both ends (REQ-ODM-050).
func TestExportODMRoundTrip(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-v13@example.org")
	sourceID := e.seedInterchange()

	exported := e.do("GET", "/api/v1/projects/"+itoa(sourceID)+"/export/odm", nil, admin)
	if exported.Code != http.StatusOK {
		t.Fatalf("export status = %d: %s", exported.Code, exported.Body.String())
	}
	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=ODM%201.3%20Copy", exported.Body.String(), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if len(report.Problems) != 0 || report.RecordsAdded != 2 || report.RecordsFailed != 0 {
		t.Fatalf("1.3 import report = %+v", report)
	}
	if want, got := e.designDump(sourceID), e.designDump(report.ProjectID); strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("1.3 round-trip design differs:\n source: %v\n copy:   %v", want, got)
	}
	if want, got := e.valueDump(sourceID), e.valueDump(report.ProjectID); strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("1.3 round-trip values differ:\n source: %v\n copy:   %v", want, got)
	}
	// The repeating designation is the part a 1.3 document cannot spell on the FormRef,
	// so check it survived rather than trusting the value dump to notice (REQ-ODM-029).
	if !e.pairRepeating(report.ProjectID, "adverse_event", "baseline_arm_1") {
		t.Errorf("the copy lost the repeating pair adverse_event/baseline_arm_1")
	}
}

// TestExportODMSASNames: ODM's own `sasName` type caps a SAS dataset or variable name
// at eight characters of [A-Za-z_][A-Za-z0-9_]*, so CLARA writes one only where the
// name fits and leaves it out otherwise — a truncation would collide between fields
// and could not round-trip (REQ-ODM-028). The fixture has both cases: `age` at three
// characters survives, `record_id` at nine does not.
func TestExportODMSASNames(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-sasnames@example.org")
	projectID := e.seedInterchange()

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	sasName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dec := xml.NewDecoder(strings.NewReader(body))
	seen := map[string]bool{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, a := range el.Attr {
			if a.Name.Local != "SASFieldName" && a.Name.Local != "SASDatasetName" {
				continue
			}
			key := el.Name.Local + "/" + a.Name.Local + "=" + a.Value
			seen[el.Name.Local+"\x00"+a.Value] = true
			if len(a.Value) > 8 || !sasName.MatchString(a.Value) {
				t.Errorf("%s violates ODM's sasName type: %s (REQ-ODM-028)", key, a.Value)
			}
		}
	}
	for _, want := range []struct{ element, value string }{
		{"ItemDef", "age"}, {"ItemGroupDef", "vitals"}, // fit: written
	} {
		if !seen[want.element+"\x00"+want.value] {
			t.Errorf("%s lost its SAS name %q, which fits sasName (REQ-ODM-028)", want.element, want.value)
		}
	}
	for _, gone := range []struct{ element, value string }{
		{"ItemDef", "record_id"}, {"ItemGroupDef", "demography"}, {"ItemGroupDef", "adverse_event"},
	} {
		if seen[gone.element+"\x00"+gone.value] {
			t.Errorf("%s carries SAS name %q, which sasName refuses — omit it, do not truncate (REQ-ODM-028)",
				gone.element, gone.value)
		}
	}
	// The names themselves stay: @Name is what import reads back (O-1).
	if !strings.Contains(body, `Name="record_id"`) {
		t.Error("dropping an over-long SAS name also dropped the item's @Name")
	}
}

func odmHasGroup(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func TestExportODMShape(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-admin@example.org")
	projectID := e.mustProject("Interchange")

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "_odm_") || !strings.HasSuffix(cd, ".xml\"") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	body := rec.Body.String()
	if strings.HasPrefix(body, "\uFEFF") {
		t.Error("the document opens with a byte-order mark (REQ-EXP-016)")
	}

	if !strings.HasPrefix(body, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("document does not open with the XML declaration: %.60s", body)
	}

	var doc struct {
		XMLName          xml.Name `xml:"ODM"`
		Namespace        string   `xml:"xmlns,attr"`
		FileType         string   `xml:"FileType,attr"`
		ODMVersion       string   `xml:"ODMVersion,attr"`
		SourceSystem     string   `xml:"SourceSystem,attr"`
		CreationDateTime string   `xml:"CreationDateTime,attr"`
		Study            struct {
			OID        string `xml:"OID,attr"`
			GlobalVars struct {
				StudyName string `xml:"StudyName"`
			} `xml:"GlobalVariables"`
			MetaDataVersion struct {
				OID string `xml:"OID,attr"`
			} `xml:"MetaDataVersion"`
		} `xml:"Study"`
		ClinicalData any `xml:"ClinicalData"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n---\n%.700s\n---", err, body)
	}
	if doc.XMLName.Local != "ODM" || doc.Namespace != odmNamespace {
		t.Errorf("root = %s in %q, want ODM in %s", doc.XMLName.Local, doc.Namespace, odmNamespace)
	}
	if doc.FileType != "Snapshot" || doc.ODMVersion != odmVersion || doc.SourceSystem != "CLARA" {
		t.Errorf("envelope = %+v/%q/%q", doc.FileType, doc.ODMVersion, doc.SourceSystem)
	}
	if !strings.HasSuffix(doc.CreationDateTime, "Z") {
		t.Errorf("CreationDateTime %q is not UTC (GD-7)", doc.CreationDateTime)
	}
	if doc.Study.OID != "CLARA-"+itoa(projectID) || doc.Study.MetaDataVersion.OID != "CLARA-"+itoa(projectID)+"-MDV" {
		t.Errorf("OIDs = %q / %q (O-1)", doc.Study.OID, doc.Study.MetaDataVersion.OID)
	}
	if doc.Study.GlobalVars.StudyName != "Interchange" {
		t.Errorf("StudyName = %q", doc.Study.GlobalVars.StudyName)
	}

	// The namespace is declared once on the root and inherited, not restated.
	if n := strings.Count(body, `xmlns="`+odmNamespace+`"`); n != 1 {
		t.Errorf("the ODM namespace is declared %d times, want once", n)
	}

	// The audit row is the pipeline's own, with the counts §9 fixes — and it lands
	// from the export goroutine once the row stream settles, so the wait comes before
	// the count rather than after it.
	var details []map[string]any
	for i := 0; i < 50 && len(details) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		details = e.auditDetails(audit.ODMExport, projectID)
	}
	if len(details) != 1 {
		t.Fatalf("odm_export audit rows = %d, want 1: %+v", len(details), details)
	}
	for _, key := range []string{"surface", "sensitivity", "records", "items", "skipped_columns"} {
		if _, ok := details[0][key]; !ok {
			t.Errorf("audit details carry no %q: %+v", key, details[0])
		}
	}
}

func TestExportODMGating(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-gate@example.org")
	projectID := e.mustProject("Gated")
	outsider := e.mustUser("odm-outsider@example.org")

	if rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm", nil, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403 (REQ-ODM-002)", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/999999/export/odm", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rec.Code)
	}
	// A refused call writes no audit entry (REQ-AUD-004).
	if n := e.auditCount(audit.ODMExport, projectID); n != 0 {
		t.Errorf("refused exports wrote %d audit rows", n)
	}
}

func TestExportODM20RoundTrip(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-v20@example.org")
	sourceID := e.seedInterchange()

	exported := e.do("GET", "/api/v1/projects/"+itoa(sourceID)+"/export/odm?odm_version=2.0", nil, admin)
	if exported.Code != http.StatusOK {
		t.Fatalf("export status = %d: %s", exported.Code, exported.Body.String())
	}
	var envelope struct {
		Namespace  string `xml:"xmlns,attr"`
		ODMVersion string `xml:"ODMVersion,attr"`
	}
	if err := xml.Unmarshal(exported.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal v2.0 envelope: %v", err)
	}
	if envelope.Namespace != odm20Namespace || envelope.ODMVersion != odm20Version {
		t.Fatalf("v2.0 envelope = namespace %q, version %q", envelope.Namespace, envelope.ODMVersion)
	}
	if disposition := exported.Header().Get("Content-Disposition"); !strings.Contains(disposition, "_odm_v2.0_") {
		t.Errorf("v2.0 Content-Disposition = %q", disposition)
	}

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=ODM%202%20Copy", exported.Body.String(), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if len(report.Problems) != 0 || report.RecordsAdded != 2 || report.RecordsFailed != 0 {
		t.Fatalf("v2.0 import report = %+v", report)
	}

	wantDesign, gotDesign := e.designDump(sourceID), e.designDump(report.ProjectID)
	if strings.Join(wantDesign, "\n") != strings.Join(gotDesign, "\n") {
		t.Errorf("v2.0 round-trip design differs:\n source: %v\n copy:   %v", wantDesign, gotDesign)
	}
	wantValues, gotValues := e.valueDump(sourceID), e.valueDump(report.ProjectID)
	if strings.Join(wantValues, "\n") != strings.Join(gotValues, "\n") {
		t.Errorf("v2.0 round-trip values differ:\n source: %v\n copy:   %v", wantValues, gotValues)
	}
	var exportDetails []map[string]any
	for i := 0; i < 50 && len(exportDetails) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		exportDetails = e.auditDetails(audit.ODMExport, sourceID)
	}
	if len(exportDetails) != 1 || exportDetails[0]["odm_version"] != "2.0" {
		t.Errorf("v2.0 export audit details = %+v", exportDetails)
	}
	if details := e.auditDetails(audit.ODMImport, report.ProjectID); len(details) != 1 || details[0]["odm_version"] != "2.0" {
		t.Errorf("v2.0 import audit details = %+v", details)
	}
}

// Each dialect is written the way its own schema spells it, and neither borrows from
// the other (REQ-ODM-015). The schema check proves a document is valid; it cannot prove
// this half — a 1.3 document with a stray 2.0 spelling would still be checked against
// 1.3 alone — so the shapes §3 promises are asserted here: an instrument is a Form-typed
// item group rather than a FormDef, an event names item groups rather than forms, prose
// names its media type, and a method's body sits in a Code child.
func TestExportODMWritesItsOwnDialect(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-dialect@example.org")
	sourceID := e.seedInterchange()

	body := func(version string) string {
		t.Helper()
		path := "/api/v1/projects/" + itoa(sourceID) + "/export/odm"
		if version != "" {
			path += "?odm_version=" + version
		}
		rec := e.do("GET", path, nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("export %q status = %d: %s", version, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	holds := func(label, body string, want []string) {
		t.Helper()
		for _, s := range want {
			if !strings.Contains(body, s) {
				t.Errorf("%s holds no %q", label, s)
			}
		}
	}
	omits := func(label, body string, absent []string) {
		t.Helper()
		for _, s := range absent {
			if strings.Contains(body, s) {
				t.Errorf("%s holds %q, which belongs to the other dialect or to no dialect at all", label, s)
			}
		}
	}

	v20 := body("2.0")
	holds("ODM 2.0", v20, []string{
		`<Study OID="CLARA-` + itoa(sourceID) + `" StudyName="Interchange source" ProtocolName=`,
		`ItemGroupDef OID="IG_vitals" Name="vitals" Repeating="No" Type="Form"`,
		`ItemGroupDef OID="IG_bp_group" Name="bp_group" Repeating="No" Type="Section"`,
		`ItemGroupDef OID="IG_adverse_event" Name="adverse_event" Repeating="Simple" Type="Form">`,
		`<TranslatedText xml:lang="en" Type="text/plain">`,
		`<MethodSignature></MethodSignature>`, // an empty signature: the expression takes no parameters
		`<FormalExpression><Code>[baseline_arm_1][weight]</Code></FormalExpression>`,
		`<ItemGroupRef ItemGroupOID="IG_vitals"`,
		`<ItemData ItemOID="IT_age"><Value>40</Value></ItemData>`,
	})
	omits("ODM 2.0", v20, []string{
		"<GlobalVariables", "<FormDef", "<FormRef", "<Protocol",
		"SASFieldName", "SASDatasetName", // 2.0 declares neither; @DatasetName carries it
		`Repeating="Yes"`, // the type 2.0 replaced with No|Simple|Dynamic|Static
	})

	v13 := body("")
	holds("ODM 1.3", v13, []string{
		`<Study OID="CLARA-` + itoa(sourceID) + `">`,
		"<GlobalVariables><StudyName>Interchange source</StudyName>",
		`<FormDef OID="FRM_vitals"`,
		`<FormRef FormOID="FRM_vitals"`,
		`<ItemData ItemOID="IT_age" Value="40">`,
		`SASDatasetName="vitals"`,
		"<FormalExpression>[baseline_arm_1][weight]</FormalExpression>", // text, not a Code child
	})
	omits("ODM 1.3", v13, []string{
		`Type="text/plain"`, `Type="Form"`, `<MethodSignature`, `<Code>`, `Repeating="Simple"`,
	})
}

func TestExportODMRejectsUnknownVersion(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-invalid-version@example.org")
	projectID := e.mustProject("Invalid ODM version")

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm?odm_version=3.0", nil, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if n := e.auditCount(audit.ODMExport, projectID); n != 0 {
		t.Errorf("invalid export wrote %d audit rows", n)
	}
}

// pairRepeating reads the stored repeating flag of one instrument↔event pair.
func (e *env) pairRepeating(projectID int64, instrument, unique string) bool {
	e.t.Helper()
	var repeating bool
	err := e.Store.DB.QueryRowContext(context.Background(),
		`SELECT ie.is_repeating FROM instrument_events ie
		 JOIN events e ON e.id = ie.event_id
		 JOIN instruments i ON i.id = ie.instrument_id
		 WHERE e.project_id = ? AND i.name = ? AND e.unique_event_name = ?`,
		projectID, instrument, unique).Scan(&repeating)
	if err != nil {
		e.t.Fatalf("pair lookup %s/%s: %v", instrument, unique, err)
	}
	return repeating
}

// TestExportODMConformanceDump writes both dialects of the fixture project into the
// directory named by CLARA_ODM_DUMP_DIR so tools/odm_conformance.py can hold them to
// the published CDISC schemas (REQ-ODM-055). It skips when the variable is unset —
// every ordinary run — because the documents are a build artifact and the program
// itself never validates against a schema (ASM-ODM-5).
func TestExportODMConformanceDump(t *testing.T) {
	dir := os.Getenv("CLARA_ODM_DUMP_DIR")
	if dir == "" {
		t.Skip("CLARA_ODM_DUMP_DIR is unset, so there is nothing to dump")
	}
	e := newEnv(t)
	admin := e.mustAdmin("odm-conformance@example.org")
	projectID := e.seedInterchange()
	for _, version := range []string{"", "2.0"} {
		path := "/api/v1/projects/" + itoa(projectID) + "/export/odm"
		name := "project_odm_1.3.xml"
		if version != "" {
			path += "?odm_version=" + version
			name = "project_odm_2.0.xml"
		}
		rec := e.do("GET", path, nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("export %q status = %d: %s", version, rec.Code, rec.Body.String())
		}
		if err := os.WriteFile(filepath.Join(dir, name), rec.Body.Bytes(), 0o644); err != nil {
			t.Fatalf("dump %s: %v", name, err)
		}
	}
}

// odmConformance locates the offline schema check and skips when it cannot run: the validator is
// development tooling on the order of Playwright, so its absence skips rather than fails, matching
// the advisory step in ci/run.sh (REQ-ODM-055, ASM-ODM-5). Returns the interpreter and the tool.
func odmConformance(t *testing.T) (string, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed, so the schema check cannot run")
	}
	if out, err := exec.Command(python, "-c", "import lxml.etree").CombinedOutput(); err != nil {
		t.Skipf("python3 lxml is not installed, so the schema check cannot run: %s",
			strings.TrimSpace(string(out)))
	}
	tool, err := filepath.Abs(filepath.Join("..", "..", "..", "tools", "odm_conformance.py"))
	if err != nil {
		t.Fatalf("locate the conformance tool: %v", err)
	}
	return python, tool
}

// TestExportODMConformanceAgainstSchema holds a real export to the published CDISC schema,
// which is the one judgment a round trip cannot make: exporter and importer can agree with
// each other about a shape neither dialect defines, and the suite stays green (O-7). It runs
// tools/odm_conformance.py over exports of both dialects.
//
// Soft by decision: the validator is development tooling on the order of Playwright, so its
// absence skips rather than fails, and REQ-ODM-055's build gate only warns. What it checks,
// once it can run, is both dialects — each writes its own header and value carrier now
// (REQ-ODM-015), so neither has a deviation left to excuse.
func TestExportODMConformanceAgainstSchema(t *testing.T) {
	python, tool := odmConformance(t)

	e := newEnv(t)
	admin := e.mustAdmin("odm-schema@example.org")
	projectID := e.seedInterchange()
	for _, dialect := range []struct {
		version string
		name    string
		enforce bool
	}{
		{"", "project_odm_1.3.xml", true},
		{"2.0", "project_odm_2.0.xml", true},
	} {
		path := "/api/v1/projects/" + itoa(projectID) + "/export/odm"
		if dialect.version != "" {
			path += "?odm_version=" + dialect.version
		}
		rec := e.do("GET", path, nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("export %q status = %d: %s", dialect.version, rec.Code, rec.Body.String())
		}
		file := filepath.Join(t.TempDir(), dialect.name)
		if err := os.WriteFile(file, rec.Body.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", dialect.name, err)
		}

		out, err := exec.Command(python, tool, file).CombinedOutput()
		report := strings.TrimSpace(string(out))
		if dialect.enforce && err != nil {
			t.Errorf("ODM 1.3 export does not conform to the published schema (REQ-ODM-055): %v\n%s",
				err, report)
		} else if err != nil {
			t.Logf("ODM %s deviates from the published schema, pending its mapping decision:\n%s",
				dialect.version, report)
		} else {
			t.Log(report)
		}
	}
}

// TestExportODMWrapperEarnsItsPlace is the control for CLARA's redefining wrapper, and without it
// the check above could not tell a standard-conformant export from an unvalidated one: run the same
// documents against vanilla CDISC ODM (`--plain`, no wrapper) and every finding must name
// urn:x-clara:odm-ext:v1 — nothing else. A finding at another position would mean the wrapper was
// masking a real defect; no finding at all would mean the schema REQ-ODM-055 makes CLARA publish
// fills hooks ODM never left open, i.e. that it is dead weight. The wrapped run must then come back
// clean for 1.3, and free of extension-namespace findings for 2.0, whose structural deviations the
// test above reports and waits on: this one says only that the wrapper covers its own namespace in
// both dialects, so a future 2.0 finding is never mistaken for CLARA's extension.
func TestExportODMWrapperEarnsItsPlace(t *testing.T) {
	python, tool := odmConformance(t)

	e := newEnv(t)
	admin := e.mustAdmin("odm-wrapper@example.org")
	projectID := e.seedInterchange()

	for _, dialect := range []struct {
		version string
		name    string
		clean   bool // the wrapped run must exit 0, not merely stop naming the extension
	}{
		{"", "project_odm_1.3.xml", true},
		{"2.0", "project_odm_2.0.xml", true},
	} {
		path := "/api/v1/projects/" + itoa(projectID) + "/export/odm"
		if dialect.version != "" {
			path += "?odm_version=" + dialect.version
		}
		rec := e.do("GET", path, nil, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("export %q status = %d: %s", dialect.version, rec.Code, rec.Body.String())
		}
		file := filepath.Join(t.TempDir(), dialect.name)
		if err := os.WriteFile(file, rec.Body.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", dialect.name, err)
		}
		label := "1.3"
		if dialect.version != "" {
			label = dialect.version
		}

		plain, plainErr := exec.Command(python, tool, "--plain", file).CombinedOutput()
		findings := odmFindings(string(plain))
		if len(findings) == 0 {
			t.Errorf("vanilla ODM %s accepted an extension-bearing export outright — either it lost\n"+
				"its extension or the vendored schema is not CDISC's (REQ-ODM-055):\n%s",
				label, plain)
		}
		for _, finding := range findings {
			if strings.Contains(finding, claraExtNamespace) {
				continue // exactly what vanilla ODM must refuse: the extension, nothing else
			}
			t.Errorf("vanilla ODM %s rejects something that is not a CLARA extension element — either\n"+
				"the mapping deviates from the schema or this wrapper hides it: %s", label, finding)
		}

		wrapped, wrappedErr := exec.Command(python, tool, file).CombinedOutput()
		if left := odmFindings(string(wrapped)); len(left) != 0 {
			for _, finding := range left {
				if strings.Contains(finding, claraExtNamespace) {
					t.Errorf("the ODM %s wrapper leaves an extension position unfilled (REQ-ODM-055): %s\n%s",
						label, finding, wrapped)
				}
			}
		}
		if dialect.clean && wrappedErr != nil {
			t.Errorf("the wrapped ODM %s export must validate; the check said: %v\n%s", label, wrappedErr, wrapped)
		}
		if plainErr == nil {
			t.Errorf("vanilla ODM %s exited without a finding for an extension-bearing document", label)
		}
	}
}

// odmFindings picks the schema findings out of the conformance tool's report — one per line, each
// carrying its libxml2 error code — so a test can assert on what was refused rather than on prose.
func odmFindings(report string) []string {
	found := []string{}
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "SCHEMAV_") {
			found = append(found, strings.TrimSpace(line))
		}
	}
	return found
}
