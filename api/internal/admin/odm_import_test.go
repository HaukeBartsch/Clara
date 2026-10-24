package admin

// The ODM importer's own contract (REQ-API-170, Design/ODM_Interchange_Design.md §6.2,
// §7): a document becomes a **new** project — its structure in one transaction, its
// records one each afterwards — and what cannot be represented is answered with a
// problem list while nothing is written (REQ-ODM-040/054). The round trip is the
// promise these tests hold hardest: a CLARA file imported into CLARA reproduces the
// project it came from, values included (REQ-ODM-050).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// seedInterchange builds a project that exercises the parts of the design an ODM
// document has to carry: two arms with events, a coded dropdown, a checkbox, a matrix
// group, an instrument repeating at one event, and records whose values include the
// identifier row every write path stores (GD-8).
func (e *env) seedInterchange() int64 {
	e.t.Helper()
	ctx := context.Background()
	projectID := e.mustProject("Interchange source")

	arm1 := e.mustArm(projectID, 1)
	arm2 := e.mustArm(projectID, 2)
	baseline := e.mustEvent(projectID, arm1, "Baseline", "baseline_arm_1")
	followup := e.mustEvent(projectID, arm1, "Followup", "followup_arm_1")
	e.mustEvent(projectID, arm2, "Baseline", "baseline_arm_2")

	add := func(instrumentID int64, f db.Field) {
		e.t.Helper()
		f.ProjectID, f.InstrumentID = projectID, instrumentID
		if _, err := e.Store.AddField(ctx, &f); err != nil {
			e.t.Fatalf("AddField %s: %v", f.FieldName, err)
		}
	}

	demography := e.mustInstrument(projectID, "demography")
	add(demography, db.Field{FieldName: "record_id", FieldType: "text", Position: 1})
	add(demography, db.Field{FieldName: "age", FieldType: "text", Position: 2,
		ValidationType: sql.NullString{String: "integer", Valid: true}})
	add(demography, db.Field{FieldName: "sex", FieldType: "dropdown", Position: 3,
		Choices: sql.NullString{String: "1$Male##2$Female", Valid: true}, Required: true})
	add(demography, db.Field{FieldName: "symptoms", FieldType: "checkbox", Position: 4,
		Choices: sql.NullString{String: "1$Mild##2$Severe", Valid: true}, PersonalInformation: true})

	vitals := e.mustInstrument(projectID, "vitals")
	add(vitals, db.Field{FieldName: "weight", FieldType: "text", Position: 1,
		ValidationType: sql.NullString{String: "floating point", Valid: true}})
	add(vitals, db.Field{FieldName: "sbp", FieldType: "text", Position: 2,
		MatrixGroup: sql.NullString{String: "bp_group", Valid: true}})
	add(vitals, db.Field{FieldName: "dbp", FieldType: "text", Position: 3,
		MatrixGroup: sql.NullString{String: "bp_group", Valid: true}})

	adverse := e.mustInstrument(projectID, "adverse_event")
	add(adverse, db.Field{FieldName: "term", FieldType: "notes", Position: 1})

	e.mustMap(projectID, arm1,
		db.InstrumentEvent{InstrumentID: demography, EventID: baseline},
		db.InstrumentEvent{InstrumentID: vitals, EventID: baseline},
		db.InstrumentEvent{InstrumentID: vitals, EventID: followup},
		db.InstrumentEvent{InstrumentID: adverse, EventID: baseline})
	tx, err := e.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		e.t.Fatalf("BeginTx: %v", err)
	}
	if err := e.Store.SetRepeatingEventsTx(ctx, tx, adverse, []int64{baseline}); err != nil {
		e.t.Fatalf("SetRepeatingEventsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		e.t.Fatalf("commit repeating: %v", err)
	}

	for _, record := range []string{"S1", "S2"} {
		e.mustRecord(projectID, record)
		e.mustValue(projectID, record, "baseline_arm_1", "", "record_id", record)
		e.mustValue(projectID, record, "baseline_arm_1", "", "age", "40")
		e.mustValue(projectID, record, "baseline_arm_1", "", "sex", "1")
		e.mustValue(projectID, record, "baseline_arm_1", "", "symptoms___1", "1")
		e.mustValue(projectID, record, "baseline_arm_1", "", "weight", "72.5")
		e.mustValue(projectID, record, "baseline_arm_1", "", "sbp", "120")
		e.mustValue(projectID, record, "baseline_arm_1", "", "dbp", "80")
	}
	// Two instances of the form that repeats: the second lives in its own slot
	// (REQ-DB-048), which is the part of a round trip a flat comparison would miss.
	e.mustInstanceValue(projectID, "S1", "baseline_arm_1", "adverse_event", 1, "term", "headache")
	e.mustInstanceValue(projectID, "S1", "baseline_arm_1", "adverse_event", 2, "term", "nausea")
	// Every row of a record names it (REQ-API-028), which is what the export reads
	// back and puts on each row — so the identifier is stored per event row here, as it
	// would be for a record written through the data API rather than only once.
	e.mustValue(projectID, "S2", "followup_arm_1", "", "record_id", "S2")
	e.mustValue(projectID, "S2", "followup_arm_1", "", "weight", "74.0")

	return projectID
}

// designDump lists a project's structure as sortable lines: every field with the
// attributes an interchange file carries, then every mapped pair with its flag.
func (e *env) designDump(projectID int64) []string {
	e.t.Helper()
	rows, err := e.Store.DB.QueryContext(context.Background(),
		`SELECT i.name, f.field_name, f.field_type, COALESCE(f.choices,''),
		        COALESCE(f.validation_type,''), COALESCE(f.matrix_group,''), f.required, f.position,
		        COALESCE(f.branching_logic,''), f.personal_information, f.direct_identifier, f.export_approved
		 FROM fields f JOIN instruments i ON i.id = f.instrument_id
		 WHERE f.project_id = ? ORDER BY i.position, f.position`, projectID)
	if err != nil {
		e.t.Fatalf("design dump: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var instrument, fieldName, fieldType, choices, validationType, matrixGroup, branching string
		var required, personal, identifier, approved bool
		var position int
		if err := rows.Scan(&instrument, &fieldName, &fieldType, &choices, &validationType,
			&matrixGroup, &required, &position, &branching, &personal, &identifier, &approved); err != nil {
			e.t.Fatalf("design scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|choices=%s|vt=%s|matrix=%s|required=%v|pos=%d|branch=%s|pi=%v|id=%v|ok=%v",
			instrument, fieldName, fieldType, choices, validationType, matrixGroup,
			required, position, branching, personal, identifier, approved))
	}

	mappings, err := e.Store.DB.QueryContext(context.Background(),
		`SELECT a.arm_num, i.name, e.unique_event_name, ie.is_repeating
		 FROM instrument_events ie
		 JOIN events e ON e.id = ie.event_id
		 JOIN arms a ON a.id = e.arm_id
		 JOIN instruments i ON i.id = ie.instrument_id
		 WHERE e.project_id = ? ORDER BY a.arm_num, i.name, e.unique_event_name`, projectID)
	if err != nil {
		e.t.Fatalf("mapping dump: %v", err)
	}
	defer mappings.Close()
	for mappings.Next() {
		var armNum int
		var instrument, unique string
		var repeating bool
		if err := mappings.Scan(&armNum, &instrument, &unique, &repeating); err != nil {
			e.t.Fatalf("mapping scan: %v", err)
		}
		out = append(out, fmt.Sprintf("map|%d|%s|%s|repeating=%v", armNum, instrument, unique, repeating))
	}
	return out
}

// valueDump lists every stored value of a project as a sortable line.
func (e *env) valueDump(projectID int64) []string {
	e.t.Helper()
	rows, err := e.Store.DB.QueryContext(context.Background(),
		`SELECT record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name, value
		 FROM data WHERE project_id = ?
		 ORDER BY record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name`, projectID)
	if err != nil {
		e.t.Fatalf("value dump: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var record, event, slot, fieldName, value string
		var instance int
		if err := rows.Scan(&record, &event, &slot, &instance, &fieldName, &value); err != nil {
			e.t.Fatalf("value scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%d|%s=%s", record, event, slot, instance, fieldName, value))
	}
	return out
}

func TestImportODMRoundTrip(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-roundtrip@example.org")
	sourceID := e.seedInterchange()

	exported := e.do("GET", "/api/v1/projects/"+itoa(sourceID)+"/export/odm", nil, admin)
	if exported.Code != http.StatusOK {
		t.Fatalf("export status = %d: %s", exported.Code, exported.Body.String())
	}

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Cohort%20Copy", exported.Body.String(), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if report.DryRun || report.ProjectID == 0 || report.ProjectName != "Cohort Copy" {
		t.Errorf("report = %+v", report)
	}
	if len(report.Problems) != 0 {
		t.Errorf("a CLARA document imported with %d problems: %+v", len(report.Problems), report.Problems)
	}
	if report.Arms != 2 || report.Instruments != 3 || report.Fields != 8 || report.Events != 3 {
		t.Errorf("counts = arms %d, instruments %d, fields %d, events %d, want 2/3/8/3",
			report.Arms, report.Instruments, report.Fields, report.Events)
	}
	if report.RecordsAdded != 2 || report.RecordsFailed != 0 {
		t.Errorf("records = added %d, failed %d, want 2/0", report.RecordsAdded, report.RecordsFailed)
	}

	// Structure and values are the round trip's whole promise: compare them line by
	// line rather than trusting the counts above (REQ-ODM-050).
	wantDesign, gotDesign := e.designDump(sourceID), e.designDump(report.ProjectID)
	if len(wantDesign) != len(gotDesign) {
		t.Fatalf("the copy holds %d design lines, the source %d:\n source: %v\n copy:   %v",
			len(gotDesign), len(wantDesign), wantDesign, gotDesign)
	}
	for i := range wantDesign {
		if wantDesign[i] != gotDesign[i] {
			t.Errorf("design line %d differs\n source: %s\n copy:   %s", i, wantDesign[i], gotDesign[i])
		}
	}
	wantValues, gotValues := e.valueDump(sourceID), e.valueDump(report.ProjectID)
	if len(wantValues) != len(gotValues) {
		t.Fatalf("the copy holds %d values, the source %d:\n source: %v\n copy:   %v",
			len(gotValues), len(wantValues), wantValues, gotValues)
	}
	for i := range wantValues {
		if wantValues[i] != gotValues[i] {
			t.Errorf("value line %d differs\n source: %s\n copy:   %s", i, wantValues[i], gotValues[i])
		}
	}

	// One odm_import row for one import, holding counts and never a value (§9).
	if n := e.auditCount(audit.ODMImport, report.ProjectID); n != 1 {
		t.Errorf("odm_import audit rows = %d, want 1", n)
	}
	for _, details := range e.auditDetails(audit.ODMImport, report.ProjectID) {
		for _, key := range []string{"project_id", "dry_run", "arms", "events", "instruments", "fields",
			"records_added", "records_failed", "problems"} {
			if _, ok := details[key]; !ok {
				t.Errorf("audit details carry no %q: %+v", key, details)
			}
		}
		raw, _ := json.Marshal(details)
		for _, forbidden := range []string{"headache", "nausea", "72.5", "Male", "demography"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("audit details hold %q, which no value or name from the document may be: %s", forbidden, raw)
			}
		}
	}
}

// tinyODM is a complete ODM 1.3 document in the importer's own terms: one event, one
// form, two items — the identifier keyed with KeySequence (GD-8) and an integer — with
// the records substituted for %s. It carries no CLARA extension at all, which is what
// REQ-ODM-052 asks the importer to accept.
func tinyODM(records string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3" FileType="Snapshot" ODMVersion="1.3.2" FileOID="tiny" CreationDateTime="2026-10-24T00:00:00Z">
 <Study OID="STY_tiny"><GlobalVariables><StudyName>Tiny study</StudyName></GlobalVariables>
  <MetaDataVersion OID="MDV_tiny" Name="Tiny study">
   <Protocol><StudyEventRef StudyEventOID="EVT_visit_arm_1" OrderNumber="1"/></Protocol>
   <StudyEventDef OID="EVT_visit_arm_1" Name="visit_arm_1" Type="Scheduled" Repeating="No">
    <FormRef FormOID="FRM_form" OrderNumber="1"/>
   </StudyEventDef>
   <FormDef OID="FRM_form" Name="form"><ItemGroupRef ItemGroupOID="IG_form" OrderNumber="1"/></FormDef>
   <ItemGroupDef OID="IG_form" Name="form" Repeating="No">
    <ItemRef ItemOID="IT_record_id" OrderNumber="1" KeySequence="1" Mandatory="Y"/>
    <ItemRef ItemOID="IT_age" OrderNumber="2"/>
   </ItemGroupDef>
   <ItemDef OID="IT_record_id" Name="record_id" DataType="text"/>
   <ItemDef OID="IT_age" Name="age" DataType="integer"/>
  </MetaDataVersion>
 </Study>
` + records + `
</ODM>`
}

// tinyRecords is one SubjectData of that document.
func tinyRecords(key, age string) string {
	return `<ClinicalData StudyOID="STY_tiny" MetaDataVersionOID="MDV_tiny">
 <SubjectData SubjectKey="` + key + `"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="` + key + `"/>
   <ItemData ItemOID="IT_age" Value="` + age + `"/>
  </ItemGroupData></StudyEventData></SubjectData>
</ClinicalData>`
}

// A document that states nothing in the CLARA extension still imports, with CLARA's
// own creation defaults where it is silent (REQ-ODM-052).
func TestImportODMWithoutExtension(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-bare@example.org")

	rec := e.do("POST", "/api/v1/projects/import/odm", tinyODM(tinyRecords("S1", "40")), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if report.ProjectName != "Tiny study" { // the document's own StudyName (REQ-ODM-044)
		t.Errorf("project_name = %q, want the document's StudyName", report.ProjectName)
	}
	if report.RecordsAdded != 1 {
		t.Errorf("records_added = %d, want 1: %+v", report.RecordsAdded, report.Problems)
	}

	// The identifier's Mandatory="Y" travels with its KeySequence="1" marker, so it
	// names GD-8 here rather than setting CLARA's required flag (§3.3, REQ-ODM-050).
	want := []string{
		"form|record_id|text|choices=|vt=|matrix=|required=false|pos=1|branch=|pi=false|id=false|ok=false",
		"form|age|text|choices=|vt=integer|matrix=|required=false|pos=2|branch=|pi=false|id=false|ok=false",
		"map|1|form|visit_arm_1|repeating=false",
	}
	if got := e.designDump(report.ProjectID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("design differs\n want: %v\n got:  %v", want, got)
	}
	wantValues := []string{"S1|visit_arm_1||1|age=40", "S1|visit_arm_1||1|record_id=S1"}
	if got := e.valueDump(report.ProjectID); strings.Join(got, "\n") != strings.Join(wantValues, "\n") {
		t.Errorf("values differ\n want: %v\n got:  %v", wantValues, got)
	}
}

// A dry run answers what the live call would do and writes nothing at all — not even
// the project row (REQ-ODM-046).
func TestImportODMDryRun(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-dry@example.org")
	before := e.projectCount()

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Dry&dry_run=1", tinyODM(tinyRecords("S1", "40")), admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if !report.DryRun || report.ProjectID != 0 {
		t.Errorf("report = %+v, want a dry run naming no project", report)
	}
	if report.Instruments != 1 || report.Fields != 2 || report.RecordsAdded != 1 {
		t.Errorf("a dry run reports what the live call would do: %+v", report)
	}
	if e.projectCount() != before {
		t.Errorf("a dry run created a project (projects %d → %d)", before, e.projectCount())
	}
	if got := e.valueDump(report.ProjectID); len(got) != 0 {
		t.Errorf("a dry run stored values: %v", got)
	}
}

// A name that exists is a conflict, never a rename of the project already there
// (REQ-ODM-044).
func TestImportODMNameConflict(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-dupe@example.org")
	existing := e.mustProject("Taken")

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Taken", tinyODM(""), admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if e.projectCount() != 1 { // `Taken`, and nothing else
		t.Errorf("a refused import changed the project list")
	}
	if _, err := e.Store.GetProject(context.Background(), existing); err != nil {
		t.Fatalf("the colliding project should be untouched: %v", err)
	}
}

// A document that contradicts itself, or is not ODM at all, is refused with the code
// §7 names — nothing written, nothing audited (REQ-ODM-054, REQ-AUD-004).
func TestImportODMRefusals(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-refuse@example.org")

	// An ItemRef naming no ItemDef: the document contradicts itself.
	dangling := strings.Replace(tinyODM(""), `<ItemDef OID="IT_age" Name="age" DataType="integer"/>`, "", 1)

	cases := []struct{ name, body, code string }{
		{"not_odm", `<html><body>no</body></html>`, "not_odm"},
		{"dtd_forbidden", `<?xml version="1.0"?><!DOCTYPE ODM [<!ENTITY x "y">]>` +
			`<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3"/>`, "dtd_forbidden"},
		{"no_metadataversion", `<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3"><Study OID="S"/></ODM>`, "no_metadataversion"},
		{"dangling_ref", dangling, "dangling_ref"},
	}
	for _, tc := range cases {
		before := e.projectCount()
		rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Nope", tc.body, admin)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", tc.name, rec.Code, rec.Body.String())
			continue
		}
		var body struct {
			Error    string       `json:"error"`
			Problems []odmProblem `json:"problems"`
		}
		e.decode(rec, &body)
		if body.Error != "odm_import_rejected" {
			t.Errorf("%s: error = %q", tc.name, body.Error)
		}
		found := false
		for _, p := range body.Problems {
			if p.Code == tc.code {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no %q problem in %+v", tc.name, tc.code, body.Problems)
		}
		if e.projectCount() != before {
			t.Errorf("%s: a refused import created a project", tc.name)
		}
		if n := e.auditRows(audit.ODMImport); n != 0 {
			t.Errorf("%s: a refused import audited %d rows (REQ-AUD-004)", tc.name, n)
		}
	}
}

// auditRows counts every row of one event type, whichever project it names.
func (e *env) auditRows(eventType string) int {
	e.t.Helper()
	var n int
	if err := e.Store.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE event_type = ?`, eventType).Scan(&n); err != nil {
		e.t.Fatalf("count %s: %v", eventType, err)
	}
	return n
}

// A bad value fails its own record and leaves the others alone (REQ-ODM-041), with
// validate's code inside the message — the same string the data API answers for the
// same value (REQ-ODM-042, §6.3's guard).
func TestImportODMValueFailsItsRecordOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-badvalue@example.org")

	body := tinyODM(`<ClinicalData StudyOID="STY_tiny" MetaDataVersionOID="MDV_tiny">
 <SubjectData SubjectKey="S1"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="S1"/><ItemData ItemOID="IT_age" Value="forty"/>
  </ItemGroupData></StudyEventData></SubjectData>
 <SubjectData SubjectKey="S2"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="S2"/><ItemData ItemOID="IT_age" Value="41"/>
  </ItemGroupData></StudyEventData></SubjectData>
</ClinicalData>`)

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Partial", body, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if report.RecordsAdded != 1 || report.RecordsFailed != 1 {
		t.Errorf("records = added %d, failed %d, want 1/1: %+v", report.RecordsAdded, report.RecordsFailed, report.Problems)
	}
	if len(report.Problems) == 0 {
		t.Fatalf("no problem was reported for the value that failed")
	}
	problem := report.Problems[0]
	if problem.Row != "S1" || problem.Field != "age" || problem.Code != "value_invalid" {
		t.Errorf("problem = %+v, want row S1, field age, code value_invalid", problem)
	}
	if !strings.Contains(problem.Message, "TYPE_INVALID") {
		t.Errorf("the message does not carry validate's own code: %q", problem.Message)
	}

	want := []string{"S2|visit_arm_1||1|age=41", "S2|visit_arm_1||1|record_id=S2"}
	if got := e.valueDump(report.ProjectID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the good record stored on its own\n want: %v\n got:  %v", want, got)
	}
}

// A second instance of a form the design does not designate repeating has nowhere to
// go, and saying so is the importer's job (REQ-DB-047/048).
func TestImportODMInstanceOfNonRepeatingForm(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-instance@example.org")

	body := tinyODM(`<ClinicalData StudyOID="STY_tiny" MetaDataVersionOID="MDV_tiny">
 <SubjectData SubjectKey="S1"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form" ItemGroupRepeatKey="2">
   <ItemData ItemOID="IT_age" Value="40"/>
  </ItemGroupData></StudyEventData></SubjectData>
</ClinicalData>`)

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Repeat", body, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if report.RecordsFailed != 1 {
		t.Errorf("records_failed = %d, want 1: %+v", report.RecordsFailed, report.Problems)
	}
	if len(report.Problems) == 0 || !strings.Contains(report.Problems[0].Message, "not designated repeating") {
		t.Errorf("the loss is not named: %+v", report.Problems)
	}
}

// Only an installation administrator may bring a whole study in, and a refused call
// leaves no trace (REQ-ODM-002, REQ-AUD-004).
func TestImportODMGating(t *testing.T) {
	e := newEnv(t)
	outsider := e.mustUser("odm-outsider@example.org")

	for _, path := range []string{
		"/api/v1/projects/import/odm",
		"/api/v1/projects/import/odm?dry_run=1",
	} {
		rec := e.do("POST", path, tinyODM(""), outsider)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (REQ-ODM-002)", path, rec.Code)
		}
	}
	if n := e.auditRows(audit.ODMImport); n != 0 {
		t.Errorf("refused imports wrote %d audit rows", n)
	}
}

// An empty body and one over the administration import bound are refused before a
// parser sees them (REQ-ODM-014).
func TestImportODMBodyBound(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-body@example.org")

	if rec := e.do("POST", "/api/v1/projects/import/odm", "", admin); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: status = %d, want 400", rec.Code)
	}
	oversized := tinyODM("") + strings.Repeat(" ", importBodyLimit)
	if rec := e.do("POST", "/api/v1/projects/import/odm", oversized, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body: status = %d, want 400", rec.Code)
	}
}

// projectCount is how many projects exist — the number a refused or dry-run import
// must not move.
func (e *env) projectCount() int {
	e.t.Helper()
	var n int
	if err := e.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		e.t.Fatalf("count projects: %v", err)
	}
	return n
}

// extensionODM states CLARA's own attributes: a date field, a field whose validation
// type this installation has no row for, and a checkbox arriving as two derived columns
// with their parent's grammar on clara:Choice (§3.6).
func extensionODM(records string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<ODM xmlns="http://www.cdisc.org/ns/odm/v1.3" xmlns:clara="urn:x-clara:odm-ext:v1"
     FileType="Snapshot" ODMVersion="1.3.2" FileOID="ext" CreationDateTime="2026-10-24T00:00:00Z">
 <Study OID="STY_ext"><GlobalVariables><StudyName>Extension study</StudyName></GlobalVariables>
  <MetaDataVersion OID="MDV_ext" Name="Extension study">
   <Protocol><StudyEventRef StudyEventOID="EVT_visit_arm_1" OrderNumber="1"/></Protocol>
   <StudyEventDef OID="EVT_visit_arm_1" Name="visit_arm_1" Type="Scheduled">
    <FormRef FormOID="FRM_form" OrderNumber="1"/>
   </StudyEventDef>
   <FormDef OID="FRM_form" Name="form"><ItemGroupRef ItemGroupOID="IG_form" OrderNumber="1"/></FormDef>
   <ItemGroupDef OID="IG_form" Name="form">
    <ItemRef ItemOID="IT_record_id" OrderNumber="1" KeySequence="1"/>
    <ItemRef ItemOID="IT_visited" OrderNumber="2"/>
    <ItemRef ItemOID="IT_mrno" OrderNumber="3"/>
    <ItemRef ItemOID="IT_symptoms___1" OrderNumber="4"/>
    <ItemRef ItemOID="IT_symptoms___2" OrderNumber="5"/>
   </ItemGroupDef>
   <ItemDef OID="IT_record_id" Name="record_id" DataType="text"/>
   <ItemDef OID="IT_visited" Name="visited" DataType="date"/>
   <ItemDef OID="IT_mrno" Name="mrno" DataType="text">
    <clara:Field fieldType="text" validationType="local_mrn"/>
   </ItemDef>
   <ItemDef OID="IT_symptoms___1" Name="symptoms___1" DataType="integer">
    <clara:Choice fieldName="symptoms" choices="1$Mild##2$Severe" personalInformation="1"/>
   </ItemDef>
   <ItemDef OID="IT_symptoms___2" Name="symptoms___2" DataType="integer">
    <clara:Choice fieldName="symptoms" choices="1$Mild##2$Severe" personalInformation="1"/>
   </ItemDef>
  </MetaDataVersion>
 </Study>
` + records + `
</ODM>`
}

// Every value passes the questions the data API asks, in its order: a date is stored
// canonical (GD-16), a validation type this installation has no row for is dropped with
// notice rather than kept unverifiable (§3.4), and a checkbox box answers 0 or 1 — only
// a checked box holds a row (REQ-DB-042).
func TestImportODMValueSemantics(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-values@example.org")

	body := extensionODM(`<ClinicalData StudyOID="STY_ext" MetaDataVersionOID="MDV_ext">
 <SubjectData SubjectKey="S1"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="S1"/>
   <ItemData ItemOID="IT_visited" Value="2026-03-05"/>
   <ItemData ItemOID="IT_mrno" Value="X-1"/>
   <ItemData ItemOID="IT_symptoms___1" Value="1"/>
   <ItemData ItemOID="IT_symptoms___2" Value="0"/>
  </ItemGroupData></StudyEventData></SubjectData>
 <SubjectData SubjectKey="S2"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="S2"/>
   <ItemData ItemOID="IT_visited" Value="05/03/2026"/>
  </ItemGroupData></StudyEventData></SubjectData>
 <SubjectData SubjectKey="S3"><StudyEventData StudyEventOID="EVT_visit_arm_1">
  <ItemGroupData ItemGroupOID="IG_form">
   <ItemData ItemOID="IT_record_id" Value="S3"/>
   <ItemData ItemOID="IT_symptoms___1" Value="2"/>
  </ItemGroupData></StudyEventData></SubjectData>
</ClinicalData>`)

	rec := e.do("POST", "/api/v1/projects/import/odm?project_name=Values", body, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var report odmImportReport
	e.decode(rec, &report)
	if report.RecordsAdded != 1 || report.RecordsFailed != 2 {
		t.Errorf("records = added %d, failed %d, want 1/2: %+v", report.RecordsAdded, report.RecordsFailed, report.Problems)
	}

	// The date arrives as ODM writes it and is stored canonical; the unchecked box
	// stores nothing at all (REQ-ODM-043's inverse of REQ-DB-042).
	// `mrno` keeps its value: dropping an unverifiable validation type is a design loss,
	// not a refusal of the data it would have constrained (§3.4).
	want := []string{
		"S1|visit_arm_1||1|mrno=X-1",
		"S1|visit_arm_1||1|record_id=S1",
		"S1|visit_arm_1||1|symptoms___1=1",
		"S1|visit_arm_1||1|visited=2026-03-05+00:00",
	}
	if got := e.valueDump(report.ProjectID); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("values differ\n want: %v\n got:  %v", want, got)
	}

	codes := map[string]string{}
	for _, p := range report.Problems {
		codes[p.Row] = p.Code + ": " + p.Message
	}
	if !strings.Contains(codes["S2"], "TYPE_INVALID") {
		t.Errorf("the malformed date did not answer the data API's code: %q", codes["S2"])
	}
	if !strings.Contains(codes["S3"], "CHOICE_INVALID") {
		t.Errorf("a box beyond 0 or 1 did not answer the data API's code: %q", codes["S3"])
	}

	// The unknown validation type is named and dropped; the checkbox parent exists once,
	// holding the grammar its two boxes shared (REQ-ODM-050).
	dropped := false
	for _, p := range report.Problems {
		if p.Code == "validation_dropped" && strings.Contains(p.Message, "local_mrn") {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("no validation_dropped problem for the type this installation lacks: %+v", report.Problems)
	}
	fields := map[string]string{}
	for _, line := range e.designDump(report.ProjectID) {
		if strings.HasPrefix(line, "map|") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		fields[parts[1]] = line
	}
	if len(fields) != 4 {
		t.Fatalf("the checkbox parent was built %d times over: %v", len(fields), fields)
	}
	if !strings.Contains(fields["symptoms"], "choices=1$Mild##2$Severe") ||
		!strings.Contains(fields["symptoms"], "pi=true") {
		t.Errorf("the checkbox parent lost its grammar or flags: %s", fields["symptoms"])
	}
	if !strings.Contains(fields["mrno"], "vt=|") {
		t.Errorf("an unknown validation type was kept anyway: %s", fields["mrno"])
	}
	if !strings.Contains(fields["visited"], "vt=date|") {
		t.Errorf("the date field holds no date validation: %s", fields["visited"])
	}
}
