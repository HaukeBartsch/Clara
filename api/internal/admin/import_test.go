package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"csms/api/internal/db"
)

// REDCap's data-dictionary header (REQ-API-152, API_Endpoints_Design.md §4.10).
const redcapHeader = `"Variable / Field Name","Form Name","Section Header","Field Type","Field Label","Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number","Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)","Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?","Field Annotation"` + "\n"

// instrumentZip builds a REDCap instrument ZIP: instrument.csv plus OriginID.txt.
func instrumentZip(t *testing.T, csv, origin string, extra ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"instrument.csv": csv, "OriginID.txt": origin} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range extra {
		if _, err := zw.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type importFile struct {
	name    string
	content []byte
}

func importRequest(dryRun bool, files ...importFile) map[string]any {
	list := make([]map[string]any, 0, len(files))
	for _, f := range files {
		list = append(list, map[string]any{"name": f.name, "content": base64.StdEncoding.EncodeToString(f.content)})
	}
	return map[string]any{"files": list, "dry_run": dryRun}
}

// createProjectViaAPI creates a project the way an administrator does — arm
// 1, event baseline and the empty instrument "instrument" (REQ-API-050).
func (e *env) createProjectViaAPI(admin *db.User, name string) int64 {
	e.t.Helper()
	rec := e.do("POST", "/api/v1/projects", map[string]any{
		"project_name": name, "organization": "OTHER", "participant_names": "P[0-9][0-9][0-9]",
	}, admin)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	var p struct {
		ID int64 `json:"id"`
	}
	e.decode(rec, &p)
	return p.ID
}

// Three small dictionaries shaped like FIONA's Incoming folder: the first
// field of the first file moves into the empty instrument; Transfers starts
// with a descriptive field, which is fine because it is not first.
const studiesCSV = redcapHeader +
	`study_instance_uid,incoming_studies,,text,"Study Instance UID",,,,,,,,,,,,,
study_date,incoming_studies,,text,"Study Date",,,date_ymd,,,,,,,,,,
study_arrival,incoming_studies,,text,"Date this Study arrived first on FIONA.",,,datetime_ymd,,,,,,,,,,
`

const seriesCSV = redcapHeader +
	`series_instance_uid,series,,text,"Series Instance UID",,,,,,,,,,,,,
series_num_files,series,,text,"Number of files for this series",,,integer,0,,,,,,,,,
`

const transfersCSV = redcapHeader +
	`transfer_info_text,transfers,,descriptive,"A transfer is initiated if both fields are filled in.",,,,,,,,,,,,,
transfer_project_name,transfers,,text,"Project name",,,,,,,,,,,,,
transfer_date,transfers," The following fields are filled out by FIONA.",text,"Date of the transfer",,"automatically filled in",datetime_ymd,,,,,,,,,," @READONLY"
transfer_error,transfers,,checkbox,"Transfer errors","1, Anonymization error | 2, Send error | 3, Performed pixel anonymization","automatically filled in",,,,,,,,,,," @READONLY"
transfer_error_message,transfers,,notes,"Message generated from last error",,,,,,,,,,,,," @READONLY"
`

func TestImportFolderIntoNewProject(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Incoming")
	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/import"
	files := []importFile{
		{"IncomingStudies_2026-10-12_1141.zip", instrumentZip(t, studiesCSV, "fiona.example.org")},
		{"Series_2026-10-12_1141.zip", instrumentZip(t, seriesCSV, "fiona.example.org")},
		{"Transfers_2026-10-12_1141.zip", instrumentZip(t, transfersCSV, "fiona.example.org", "attachments/logo.png")},
	}

	// The dry run writes nothing and shows the whole result.
	rec := e.do("POST", path, importRequest(true, files...), admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rec.Code, rec.Body.String())
	}
	var dry importResult
	e.decode(rec, &dry)
	if !dry.DryRun || !dry.Importable || len(dry.Problems) != 0 {
		t.Fatalf("dry run result: %+v", dry)
	}
	if dry.RecordIdentifier == nil || *dry.RecordIdentifier != (importIdentifierOut{Field: "study_instance_uid", Instrument: "instrument", Moved: true}) {
		t.Fatalf("record identifier = %+v", dry.RecordIdentifier)
	}
	gotNames := []string{}
	for _, in := range dry.Instruments {
		gotNames = append(gotNames, in.Name)
		if in.ID != nil {
			t.Errorf("dry run instrument %s carries id %d", in.Name, *in.ID)
		}
	}
	if strings.Join(gotNames, ",") != "instrument,incoming_studies,series,transfers" {
		t.Fatalf("instruments = %v", gotNames)
	}
	if !dry.Instruments[0].Existing || len(dry.Instruments[0].Fields) != 1 || dry.Instruments[0].Origin != "fiona.example.org" {
		t.Fatalf("first instrument = %+v", dry.Instruments[0])
	}
	if !hasNote(dry.Warnings, "ignored_entry", "Transfers_2026-10-12_1141.zip") {
		t.Errorf("warnings miss the ignored attachment: %+v", dry.Warnings)
	}
	if fields, _ := e.Store.ListFields(context.Background(), projectID); len(fields) != 0 {
		t.Fatalf("the dry run wrote %d fields", len(fields))
	}

	rec = e.do("POST", path, importRequest(false, files...), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var done importResult
	e.decode(rec, &done)
	for _, in := range done.Instruments {
		if in.ID == nil || *in.ID <= 0 {
			t.Errorf("instrument %s has no live id", in.Name)
		}
		for _, f := range in.Fields {
			if f.ID == nil || *f.ID <= 0 {
				t.Errorf("field %s has no live id", f.FieldName)
			}
		}
	}

	ctx := context.Background()
	instruments, _ := e.Store.ListInstruments(ctx, projectID)
	if len(instruments) != 4 || instruments[0].Name != "instrument" || instruments[3].Name != "transfers" {
		t.Fatalf("live instruments = %+v", instruments)
	}
	fields, _ := e.Store.ListFields(ctx, projectID)
	if len(fields) != 1+2+2+5 || fields[0].FieldName != "study_instance_uid" || fields[0].InstrumentID != instruments[0].ID {
		t.Fatalf("live fields = %d, first %+v", len(fields), fields[0])
	}
	byName := map[string]int{}
	for i, f := range fields {
		byName[f.FieldName] = i
	}
	date := fields[byName["study_date"]]
	if date.ValidationType.String != "date" || date.ValidationFormat.String != "Y-m-d" {
		t.Errorf("study_date validation = %q %q", date.ValidationType.String, date.ValidationFormat.String)
	}
	arrival := fields[byName["study_arrival"]]
	if arrival.ValidationType.String != "datetime" || arrival.ValidationFormat.String != "Y-m-d H:i" {
		t.Errorf("study_arrival validation = %q %q", arrival.ValidationType.String, arrival.ValidationFormat.String)
	}
	if n := fields[byName["series_num_files"]]; n.ValidationType.String != "integer" || n.ValidationMin.String != "0" {
		t.Errorf("series_num_files = %+v", n)
	}
	if f := fields[byName["transfer_info_text"]]; f.FieldType != "description" {
		t.Errorf("descriptive → %q", f.FieldType)
	}
	te := fields[byName["transfer_error"]]
	if te.FieldAnnotation.String != "@READONLY" || te.Choices.String != "1$Anonymization error##2$Send error##3$Performed pixel anonymization" {
		t.Errorf("transfer_error = annotation %q choices %q", te.FieldAnnotation.String, te.Choices.String)
	}
	if h := fields[byName["transfer_date"]].SectionHeader.String; h != "The following fields are filled out by FIONA." {
		t.Errorf("section header = %q", h)
	}

	// No instrument-event mapping: nothing collects data before a person maps it.
	pairs, _ := e.Store.ListInstrumentEvents(ctx, projectID)
	if len(pairs) != 0 {
		t.Errorf("the import mapped %d pairs", len(pairs))
	}

	// One instrument_created per created instrument, one field_created per field,
	// each naming the file and its origin (Audit_Logging_Design.md §3.3).
	var created, fieldEntries int
	rows, err := e.Store.DB.Query(`SELECT event_type, details FROM audit_events WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, details string
		if err := rows.Scan(&kind, &details); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "instrument_created":
			created++
		case "field_created":
			fieldEntries++
		default:
			continue
		}
		if !strings.Contains(details, `"origin":"fiona.example.org"`) {
			t.Errorf("%s details without the origin: %s", kind, details)
		}
	}
	if created != 3 || fieldEntries != 10 {
		t.Errorf("audit: %d instrument_created, %d field_created; want 3 and 10", created, fieldEntries)
	}
}

// A Projects-like dictionary exercising the §4.10 conversion tables.
const projectsCSV = "\xef\xbb\xbf" + redcapHeader +
	`project_name,projects,,text,"Project Name (use the same name on FIONA)",,,,,,,,,,,,,
project_type,projects,,dropdown,"Type","0, with REK | 1, no REK with non-human subjects",,,,,,,,,,,,
project_annotation_type,projects,,dropdown,"Type of the project","1, Radiology | 2, Pathology | 4, RadPath",,,,,,,,,,,," @DEFAULT=""1"""
project_pat_import_folder,projects,,text,"Pathology import folder (on FIONA)<br/>
If a folder has been specified WSI images will not be converted.",,,,,,,"[project_annotation_type] = '2' or [project_annotation_type] = '4'",,,,,,
project_active,projects,,yesno,"Is the project an active imaging project?",,,,,,,,,RH,,,,
project_dest,projects,,dropdown,"Destination","PECTMRI, PECTMRI | QC-NHPhantom, QC - NH Phantom | 8DISC, 8DISC",,,,,,,,,,,,
project_features,projects,"Project features",checkbox,"Project features","0, Remove PR | 1, Never anonymize (dangerous, full responsibility)",,,,,,,,,,,,
project_feature_note,projects,,notes,"Why",,,,,,,"[project_features(1)] = '1'",,,,,,
project_end_data,projects,,file,"Project end export (CDISC)",,,,,,,,,,,,,
project_end_sign,projects,,file,"Person performing deletion",,,signature,,,,,,,,,,
project_ok,projects,,truefalse,"Confirmed",,,,,,,,,,,,,
project_score,projects,,slider,"Score","Low | Mid | High",,number,,,,,,,,,,
project_total,projects,,calc,"Total","[project_score] * 2",,,,,,,,,,,,
project_weight,projects,,text,"Weight",,,number_2dp,,,,,,,,,,
project_contact_email,projects,,text,"Email",,,email,,,y,,,,,,,
project_phone,projects,,text,"Phone",,,phone,,,,,,,,,,
`

func TestImportInstrumentNames(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("instrument-names@example.org")
	pathFor := func(name string) string {
		return "/api/v1/projects/" + itoa(e.createProjectViaAPI(admin, "Instrument name "+name[:min(len(name), 12)])) + "/instruments/import"
	}
	importCSV := func(path, form string) importResult {
		t.Helper()
		csv := redcapHeader +
			"record_id," + form + ",,text,Record ID,,,,,,,,,,,,,\n" +
			"age," + form + ",,text,Age,,,,,,,,,,,,,\n"
		rec := e.do("POST", path, importRequest(false, importFile{"names.csv", []byte(csv)}), admin)
		if rec.Code != http.StatusCreated {
			t.Fatalf("import instrument %q: %d %s", form, rec.Code, rec.Body.String())
		}
		var result importResult
		e.decode(rec, &result)
		return result
	}

	paddedName := " Incoming Studies_2026 "
	result := importCSV(pathFor("Padded Name"), paddedName)
	if len(result.Instruments) != 2 || result.Instruments[1].Name != paddedName || result.Instruments[1].RenamedFrom != nil {
		t.Fatalf("valid instrument name with spaces was changed: %+v", result.Instruments)
	}

	maxName := "Mixed Name_2026 " + strings.Repeat("A", 255-len("Mixed Name_2026 "))
	result = importCSV(pathFor(maxName), maxName)
	if len(result.Instruments) != 2 || result.Instruments[1].Name != maxName || result.Instruments[1].RenamedFrom != nil {
		t.Fatalf("255-character instrument name changed: %+v", result.Instruments)
	}

	overlong := strings.Repeat("B", 256)
	result = importCSV(pathFor(overlong), overlong)
	if len(result.Instruments) != 2 || result.Instruments[1].Name != strings.Repeat("B", 255) {
		t.Fatalf("overlong instrument name was not bounded to 255 characters: %+v", result.Instruments)
	}
	if result.Instruments[1].RenamedFrom == nil || *result.Instruments[1].RenamedFrom != overlong {
		t.Fatalf("overlong instrument name has no source name in result: %+v", result.Instruments[1])
	}
	var warned bool
	for _, warning := range result.Warnings {
		warned = warned || warning.Code == "renamed"
	}
	if !warned {
		t.Fatalf("overlong instrument name has no rename warning: %+v", result.Warnings)
	}
}

func TestImportConversion(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "DataTransferProjects")
	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/import"

	rec := e.do("POST", path, importRequest(false, importFile{"Projects.csv", []byte(projectsCSV)}), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var res importResult
	e.decode(rec, &res)
	for _, want := range []struct{ code, field string }{
		{"skipped", "project_end_data"}, {"skipped", "project_end_sign"},
		{"converted", "project_ok"}, {"converted", "project_score"}, {"converted", "project_weight"},
		{"dropped_attribute", "project_active"}, {"dropped_attribute", "project_phone"},
	} {
		if !hasFieldNote(res.Warnings, want.code, want.field) {
			t.Errorf("no %s warning for %s: %+v", want.code, want.field, res.Warnings)
		}
	}

	fields, _ := e.Store.ListFields(context.Background(), projectID)
	byName := map[string]int{}
	for i, f := range fields {
		byName[f.FieldName] = i
	}
	get := func(name string) dbFieldView {
		i, ok := byName[name]
		if !ok {
			t.Fatalf("field %s was not imported", name)
		}
		f := fields[i]
		return dbFieldView{f.FieldType, f.Choices.String, f.ValidationType.String, f.ValidationMin.String, f.ValidationMax.String,
			f.BranchingLogic.String, f.Calculation.String, f.FieldLabel.String, f.FieldAnnotation.String, f.DirectIdentifier}
	}
	if _, ok := byName["project_end_data"]; ok {
		t.Error("a file field was imported")
	}
	if v := get("project_dest"); v.choices != "PECTMRI$PECTMRI##QC-NHPhantom$QC - NH Phantom##8DISC$8DISC" {
		t.Errorf("text codes = %q", v.choices)
	}
	if v := get("project_features"); v.choices != "0$Remove PR##1$Never anonymize (dangerous, full responsibility)" {
		t.Errorf("checkbox choices = %q", v.choices)
	}
	if v := get("project_pat_import_folder"); v.branching != "[project_annotation_type] = '2' or [project_annotation_type] = '4'" ||
		v.label != "Pathology import folder (on FIONA)\nIf a folder has been specified WSI images will not be converted." {
		t.Errorf("REDCap branching or <br/> label: %+v", v)
	}
	if v := get("project_feature_note"); v.branching != "[project_features(1)] = '1'" {
		t.Errorf("checkbox branching = %q", v.branching)
	}
	if v := get("project_annotation_type"); v.annotation != `@DEFAULT="1"` {
		t.Errorf("annotation = %q", v.annotation)
	}
	if v := get("project_active"); v.fieldType != "yesno" || v.choices != "1$Yes##0$No" {
		t.Errorf("yesno = %+v", v)
	}
	if v := get("project_ok"); v.fieldType != "radio" || v.choices != "1$True##0$False" {
		t.Errorf("truefalse = %+v", v)
	}
	if v := get("project_score"); v.fieldType != "text" || v.validation != "integer" || v.min != "0" || v.max != "100" {
		t.Errorf("slider = %+v", v)
	}
	if v := get("project_total"); v.fieldType != "calculated" || v.calculation != "[baseline_arm_1][project_score] * 2" {
		t.Errorf("calc = %+v", v)
	}
	if v := get("project_weight"); v.validation != "floating point" {
		t.Errorf("number_2dp = %+v", v)
	}
	if v := get("project_contact_email"); v.validation != "email" || !v.directIdentifier {
		t.Errorf("email identifier = %+v", v)
	}
	if v := get("project_phone"); v.validation != "" {
		t.Errorf("phone kept validation %q", v.validation)
	}
	// The first row of the file became the record identifier (GD-8).
	if fields[0].FieldName != "project_name" {
		t.Errorf("record identifier = %s", fields[0].FieldName)
	}
}

type dbFieldView struct {
	fieldType, choices, validation, min, max, branching, calculation, label, annotation string
	directIdentifier                                                                    bool
}

func TestImportRenamesIntoExistingProject(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Routing")
	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/import"
	first := redcapHeader + `record_id,routing,,text,"Record ID",,,,,,,,,,,,,
routing_active,routing,,yesno,"Is this rule active?",,,,,,,,,,,,,
flags,routing,,checkbox,"Flags","1, A | 2, B",,,,,,,,,,,,
`
	if rec := e.do("POST", path, importRequest(false, importFile{"Routing.zip", instrumentZip(t, first, "fiona")}), admin); rec.Code != http.StatusCreated {
		t.Fatalf("first import: %d %s", rec.Code, rec.Body.String())
	}

	// The same names again: every field and the instrument get _2, the
	// references of the second file follow its own renames, and the record
	// identifier stays where it is.
	second := redcapHeader + `record_id,routing,,text,"Record ID",,,,,,,,,,,,,
routing_active,routing,,yesno,"Active?",,,,,,,"[record_id] <> '' and [flags(2)] = '1'",,,,,,
flags,routing,,checkbox,"Flags","1, A | 2, B",,,,,,"[routing_active] = '1'",,,,,,
flags___1,routing,,text,"Collides with a checkbox column",,,,,,,,,,,,,
`
	rec := e.do("POST", path, importRequest(false, importFile{"Routing again.csv", []byte(second)}), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second import: %d %s", rec.Code, rec.Body.String())
	}
	var res importResult
	e.decode(rec, &res)
	if res.RecordIdentifier == nil || res.RecordIdentifier.Field != "record_id" || res.RecordIdentifier.Moved {
		t.Fatalf("record identifier = %+v", res.RecordIdentifier)
	}
	if len(res.Instruments) != 1 || res.Instruments[0].Name != "routing_2" || res.Instruments[0].RenamedFrom == nil {
		t.Fatalf("instrument = %+v", res.Instruments)
	}
	got := map[string]string{}
	for _, f := range res.Instruments[0].Fields {
		from := ""
		if f.RenamedFrom != nil {
			from = *f.RenamedFrom
		}
		got[f.FieldName] = from
	}
	want := map[string]string{"record_id_2": "record_id", "routing_active_2": "routing_active", "flags_2": "flags", "flags___1_2": "flags___1"}
	for name, from := range want {
		if got[name] != from {
			t.Errorf("renames = %v, want %v", got, want)
			break
		}
	}
	fields, _ := e.Store.ListFields(context.Background(), projectID)
	logic := map[string]string{}
	for _, f := range fields {
		logic[f.FieldName] = f.BranchingLogic.String
	}
	if logic["routing_active_2"] != "[record_id_2] <> '' and [flags_2(2)] = '1'" || logic["flags_2"] != "[routing_active_2] = '1'" {
		t.Errorf("rewritten references: %q / %q", logic["routing_active_2"], logic["flags_2"])
	}
	if fields[0].FieldName != "record_id" {
		t.Errorf("record identifier moved to %s", fields[0].FieldName)
	}
}

func TestImportRefusals(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Refusals")
	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/import"

	failure := func(name string, body any, wantCode string) {
		t.Helper()
		rec := e.do("POST", path, body, admin)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		var out struct {
			Error    string       `json:"error"`
			Message  string       `json:"message"`
			Problems []importNote `json:"problems"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if wantCode == "" {
			return
		}
		if !hasNote(out.Problems, wantCode, "") {
			t.Fatalf("%s: problems %+v, want %s", name, out.Problems, wantCode)
		}
	}

	// A form whose rows do not stand together names the out-of-place row.
	interleaved := redcapHeader + `a,demographics,,text,"A",,,,,,,,,,,,,
b,followup,,text,"B",,,,,,,,,,,,,
c,demographics,,text,"C",,,,,,,,,,,,,
`
	failure("interleaved", importRequest(false, importFile{"dictionary.csv", []byte(interleaved)}), "form_not_contiguous")
	rec := e.do("POST", path, importRequest(true, importFile{"dictionary.csv", []byte(interleaved)}), admin)
	var dry importResult
	e.decode(rec, &dry)
	if dry.Importable || len(dry.Problems) != 1 || dry.Problems[0].Row != 4 || dry.Problems[0].Field != "c" {
		t.Fatalf("interleaved dry run: %+v", dry.Problems)
	}

	// The first field would become the record identifier but is descriptive.
	descriptiveFirst := redcapHeader + `intro,transfers,,descriptive,"Intro",,,,,,,,,,,,,
name,transfers,,text,"Name",,,,,,,,,,,,,
`
	failure("descriptive first", importRequest(false, importFile{"Transfers.zip", instrumentZip(t, descriptiveFirst, "x")}), "identifier_not_value")

	// A file field first is skipped, so it cannot become the identifier either.
	fileFirst := redcapHeader + `scan,s,,file,"Scan",,,,,,,,,,,,,
name,s,,text,"Name",,,,,,,,,,,,,
`
	failure("file first", importRequest(false, importFile{"s.csv", []byte(fileFirst)}), "identifier_not_value")

	failure("unsupported type", importRequest(false, importFile{"x.csv", []byte(redcapHeader + `a,f,,sql,"A","select 1",,,,,,,,,,,,
`)}), "unsupported_type")
	failure("missing column", importRequest(false, importFile{"x.csv", []byte("field_name,form_name\na,f\n")}), "missing_column")

	var noCSV bytes.Buffer
	zw := zip.NewWriter(&noCSV)
	if _, err := zw.Create("OriginID.txt"); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	failure("archive without instrument.csv", importRequest(false, importFile{"x.zip", noCSV.Bytes()}), "missing_instrument_csv")

	badCode := redcapHeader + `id,f,,text,"ID",,,,,,,,,,,,,
a,f,,checkbox,"A","1, x | Two, y",,,,,,,,,,,,
`
	failure("checkbox text code", importRequest(false, importFile{"x.csv", []byte(badCode)}), "validation_error")

	badRef := redcapHeader + `a,f,,text,"A",,,,,,,"[nowhere] = '1'",,,,,,
`
	failure("unknown reference", importRequest(false, importFile{"x.csv", []byte(badRef)}), "validation_error")

	// Request-level errors are invalid_request, before any file is read.
	if rec := e.do("POST", path, map[string]any{"files": []any{}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("empty files: %d", rec.Code)
	}
	if rec := e.do("POST", path, map[string]any{"files": []any{map[string]any{"name": "a.csv", "content": "%%%"}}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad base64: %d", rec.Code)
	}
	if rec := e.do("POST", path, map[string]any{"files": []any{}, "mapping": true}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown attribute: %d", rec.Code)
	}

	// Nothing above wrote anything.
	if fields, _ := e.Store.ListFields(context.Background(), projectID); len(fields) != 0 {
		t.Fatalf("refused imports wrote %d fields", len(fields))
	}

	// project_admin only (REQ-API-152).
	member := e.mustUser("member@example.org")
	if rec := e.do("POST", path, importRequest(true, importFile{"x.csv", []byte(studiesCSV)}), member); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("non-admin: %d", rec.Code)
	}
}

func TestImportInProductionStages(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Staged Import")
	base := "/api/v1/projects/" + itoa(projectID)
	if rec := e.do("PUT", base+"/mode", map[string]any{"mode": "production", "keep_data": true}, admin); rec.Code != http.StatusOK {
		t.Fatalf("mode: %d %s", rec.Code, rec.Body.String())
	}
	body := importRequest(false, importFile{"Series.zip", instrumentZip(t, seriesCSV, "fiona")})

	// Without an open set every structure write is refused (REQ-API-107) — the
	// dry run still answers, it writes nothing.
	if rec := e.do("POST", base+"/instruments/import", body, admin); rec.Code != http.StatusConflict {
		t.Fatalf("no staging set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", base+"/instruments/import", importRequest(true, importFile{"Series.zip", instrumentZip(t, seriesCSV, "fiona")}), admin); rec.Code != http.StatusOK {
		t.Fatalf("dry run without a set: %d", rec.Code)
	}
	if rec := e.do("POST", base+"/staging", nil, admin); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("open staging: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.do("POST", base+"/instruments/import", body, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("staged import: %d %s", rec.Code, rec.Body.String())
	}
	var res importResult
	e.decode(rec, &res)
	if len(res.Instruments) != 2 || res.Instruments[1].ID == nil || *res.Instruments[1].ID >= 0 {
		t.Fatalf("staged result should carry provisional ids: %+v", res.Instruments)
	}
	ctx := context.Background()
	if fields, _ := e.Store.ListFields(ctx, projectID); len(fields) != 0 {
		t.Fatalf("the staged import touched the live design: %d fields", len(fields))
	}
	if rec := e.do("POST", base+"/staging/commit", nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	fields, _ := e.Store.ListFields(ctx, projectID)
	if len(fields) != 2 || fields[0].FieldName != "series_instance_uid" {
		t.Fatalf("after commit: %+v", fields)
	}
}

// TestImportFionaProjectSetup imports FIONA's own setup folders when the
// owner's copy is present (assets/ProjectSetupFiona/, not part of the
// repository): each folder's ZIPs, in file-name order, into a project of that
// name.
func TestImportFionaProjectSetup(t *testing.T) {
	root := filepath.Join("..", "..", "..", "assets", "ProjectSetupFiona")
	folders, err := os.ReadDir(root)
	if err != nil {
		t.Skip("assets/ProjectSetupFiona is not present")
	}
	want := map[string]string{
		"CouplingList": "record_id", "DataTransferProjects": "project_name", "Incoming": "study_instance_uid",
		"Routing": "record_id", "WhatIsInIDS7": "record_id",
	}
	// Instruments in order with their field counts; the 5 file fields of
	// DataTransferProjects are skipped.
	shape := map[string]string{
		"CouplingList":         "instrument:1 coupling_list:8",
		"DataTransferProjects": "instrument:1 projects:28 rewritepixelexclusions:2 technical_capabilities_questions:16",
		"Incoming":             "instrument:1 incoming_studies:10 series:7 transfers:10",
		"Routing":              "instrument:1 routing:8",
		"WhatIsInIDS7":         "instrument:1 studylevel:13",
	}
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(root, folder.Name()))
		var files []importFile
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".zip") || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root, folder.Name(), entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, importFile{entry.Name(), raw})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
		if len(files) == 0 {
			continue
		}
		t.Run(folder.Name(), func(t *testing.T) {
			projectID := e.createProjectViaAPI(admin, folder.Name())
			rec := e.do("POST", "/api/v1/projects/"+itoa(projectID)+"/instruments/import", importRequest(false, files...), admin)
			if rec.Code != http.StatusCreated {
				t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
			}
			var res importResult
			e.decode(rec, &res)
			if id, ok := want[folder.Name()]; ok && (res.RecordIdentifier == nil || res.RecordIdentifier.Field != id) {
				t.Errorf("record identifier = %+v, want %s", res.RecordIdentifier, id)
			}
			var got []string
			for _, in := range res.Instruments {
				got = append(got, in.Name+":"+itoa(int64(len(in.Fields))))
			}
			if s, ok := shape[folder.Name()]; ok && strings.Join(got, " ") != s {
				t.Errorf("instruments = %s, want %s", strings.Join(got, " "), s)
			}

			// The project's own dictionary export imports back as the same fields
			// (REQ-API-153).
			exported := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instruments/export", nil, admin)
			if exported.Code != http.StatusOK {
				t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
			}
			copyID := e.createProjectViaAPI(admin, folder.Name()+" copy")
			if rec := e.do("POST", "/api/v1/projects/"+itoa(copyID)+"/instruments/import",
				importRequest(false, importFile{"export.zip", exported.Body.Bytes()}), admin); rec.Code != http.StatusCreated {
				t.Fatalf("re-import of the export: %d %s", rec.Code, rec.Body.String())
			}
			want, have := e.designShape(projectID), e.designShape(copyID)
			if len(want) != len(have) {
				t.Fatalf("the copy has %d fields, the original %d", len(have), len(want))
			}
			for name, s := range want {
				if have[name] != s {
					t.Errorf("%s differs after export and re-import:\n  original %s\n  copy     %s", name, s, have[name])
				}
			}
		})
	}
}

func hasNote(notes []importNote, code, file string) bool {
	for _, n := range notes {
		if n.Code == code && (file == "" || n.File == file) {
			return true
		}
	}
	return false
}

func hasFieldNote(notes []importNote, code, field string) bool {
	for _, n := range notes {
		if n.Code == code && n.Field == field {
			return true
		}
	}
	return false
}

// A problem never hides the rest of the preview: the dry run still lists every
// instrument and field it would create, so the person sees the whole import
// beside what blocks it (§7.6).
func TestImportPreviewKeepsInstrumentsWithProblems(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Preview Problems")
	path := "/api/v1/projects/" + itoa(projectID) + "/instruments/import"
	preview := func(csv string) importResult {
		t.Helper()
		rec := e.do("POST", path, importRequest(true, importFile{"dictionary.csv", []byte(csv)}), admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("dry run: %d %s", rec.Code, rec.Body.String())
		}
		var res importResult
		e.decode(rec, &res)
		return res
	}
	shape := func(res importResult) string {
		var parts []string
		for _, in := range res.Instruments {
			parts = append(parts, in.Name+":"+itoa(int64(len(in.Fields))))
		}
		return strings.Join(parts, " ")
	}

	// An unsupported row in an otherwise good file: the identifier still moves.
	res := preview(redcapHeader + `id,a,,text,"ID",,,,,,,,,,,,,
x,a,,sql,"X","select 1",,,,,,,,,,,,
y,b,,text,"Y",,,,,,,,,,,,,
`)
	if res.Importable || !hasNote(res.Problems, "unsupported_type", "") {
		t.Fatalf("problems: %+v", res.Problems)
	}
	if shape(res) != "instrument:1 b:1" || res.RecordIdentifier == nil || !res.RecordIdentifier.Moved {
		t.Errorf("preview = %s, identifier %+v", shape(res), res.RecordIdentifier)
	}

	// A first field that cannot be the identifier: nothing moves, the rest is listed.
	res = preview(redcapHeader + `intro,a,,descriptive,"Intro",,,,,,,,,,,,,
name,a,,text,"Name",,,,,,,,,,,,,
`)
	if res.Importable || !hasNote(res.Problems, "identifier_not_value", "") {
		t.Fatalf("problems: %+v", res.Problems)
	}
	if shape(res) != "a:2" {
		t.Errorf("preview = %s", shape(res))
	}
}
