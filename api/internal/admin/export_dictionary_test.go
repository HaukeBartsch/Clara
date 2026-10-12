package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"csms/api/internal/db"
)

// A dictionary exercising every column the export writes back (§4.10 export table).
const roundTripCSV = redcapHeader +
	`record_id,demo,,text,"Record ID",,,,,,,,,,,,,
dob,demo,"Personal data",text,"Date of birth",,"dd-mm-yyyy",date_dmy,,,y,,y,,,,,
visit_at,demo,,text,"Visit",,,datetime_mdy,,,,,,,,,,
email,demo,,text,"Email",,,email,,,,,,,,,,
intro,demo,,descriptive,"Welcome<br/>Second line",,,,,,,,,,,,,
sex,demo,,radio,"Sex","1, Male | 2, Female",,,,,,,,,,,,
age,demo,,text,"Age",,,integer,0,120,,,,,,,,
weight,demo,,text,"Weight",,,number,,,,,,,,,,
age2,demo,,calc,"Age doubled","[age] * 2",,,,,,,,,,,,
status,demo,,text,"Status",,,,,,,"[sex] = '1'",,,,,," @READONLY"
q1,demo,,radio,"Q1","1, Low | 2, High",,,,,,,,,,grid,,
q2,demo,,radio,"Q2","1, Low | 2, High",,,,,,,,,,grid,,
smoker,demo,,yesno,"Smoker?",,,,,,,,,,,,,
flags,habits,,checkbox,"Habits","a, Coffee | b, Tea",,,,,,"[smoker] = '1' and [flags(a)] = '0'",,,,,,
notes,habits,,notes,"Notes
over two lines",,,,,,,,,,,,,
dest,habits,,dropdown,"Destination","PECTMRI, PECTMRI | QC-NHPhantom, QC - NH Phantom",,,,,,,,,,,,
`

// exportZip reads the export's archive into its entries.
func exportZip(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the export is not a ZIP: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(raw)
	}
	return out
}

// fieldShape is everything the export carries for one field.
func fieldShape(f db.Field, instrument string) string {
	s := func(n sql.NullString) string { return n.String }
	return strings.Join([]string{instrument, f.FieldType, s(f.FieldLabel), s(f.SectionHeader), s(f.FieldNote), s(f.Choices),
		s(f.ValidationType), s(f.ValidationFormat), s(f.ValidationMin), s(f.ValidationMax), boolString(f.Required),
		s(f.BranchingLogic), s(f.Calculation), s(f.MatrixGroup), s(f.FieldAnnotation), boolString(f.DirectIdentifier)}, " | ")
}

func boolString(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func (e *env) designShape(projectID int64) map[string]string {
	e.t.Helper()
	ctx := context.Background()
	instruments, _ := e.Store.ListInstruments(ctx, projectID)
	names := map[int64]string{}
	for _, i := range instruments {
		names[i.ID] = i.Name
	}
	fields, _ := e.Store.ListFields(ctx, projectID)
	out := map[string]string{}
	for _, f := range fields {
		out[f.FieldName] = fieldShape(f, names[f.InstrumentID])
	}
	return out
}

// TestExportDictionaryRoundTrip: what one project exports, another imports as
// the same fields (REQ-API-153).
func TestExportDictionaryRoundTrip(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	source := e.createProjectViaAPI(admin, "RT Source")
	if rec := e.do("POST", "/api/v1/projects/"+itoa(source)+"/instruments/import",
		importRequest(false, importFile{"source.csv", []byte(roundTripCSV)}), admin); rec.Code != http.StatusCreated {
		t.Fatalf("import into the source: %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do("GET", "/api/v1/projects/"+itoa(source)+"/instruments/export", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !regexp.MustCompile(`^attachment; filename="RT_Source_\d{4}-\d{2}-\d{2}_\d{4}\.zip"$`).MatchString(disposition) {
		t.Errorf("Content-Disposition = %q", disposition)
	}
	archive := rec.Body.Bytes()
	entries := exportZip(t, archive)
	if entries["OriginID.txt"] != "csms.example.org" {
		t.Errorf("OriginID.txt = %q", entries["OriginID.txt"])
	}
	csvText := entries["instrument.csv"]
	if !strings.HasPrefix(csvText, redcapHeader) {
		t.Fatalf("instrument.csv does not start with REDCap's header:\n%.300s", csvText)
	}
	lines := strings.Split(csvText, "\n")
	// The record identifier moved into the empty `instrument` on import, and the
	// instrument's name stands in Form Name (REQ-API-153).
	if !strings.HasPrefix(lines[1], "record_id,instrument,") || !strings.HasPrefix(lines[2], "dob,demo,") {
		t.Errorf("first rows: %q / %q", lines[1], lines[2])
	}
	for _, want := range []string{
		`intro,demo,,descriptive,`, `age2,demo,,calc,Age doubled,[baseline_arm_1][age] * 2,`,
		`q1,demo,,radio,Q1,"1, Low | 2, High"`, `,grid,,`, `smoker,demo,,yesno,Smoker?,,`,
		`dob,demo,Personal data,text,Date of birth,,dd-mm-yyyy,date_dmy,,,y,,y,`,
		`visit_at,demo,,text,Visit,,,datetime_mdy,`, `weight,demo,,text,Weight,,,number,`,
		`email,demo,,text,Email,,,email,,,y,`, `@READONLY`,
		`dest,habits,,dropdown,Destination,"PECTMRI, PECTMRI | QC-NHPhantom, QC - NH Phantom"`,
	} {
		if !strings.Contains(csvText, want) {
			t.Errorf("instrument.csv lacks %q", want)
		}
	}

	target := e.createProjectViaAPI(admin, "RT Target")
	rec = e.do("POST", "/api/v1/projects/"+itoa(target)+"/instruments/import",
		importRequest(false, importFile{"RT_Source.zip", archive}), admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import of the export: %d %s", rec.Code, rec.Body.String())
	}
	var res importResult
	e.decode(rec, &res)
	if len(res.Warnings) != 0 {
		t.Errorf("the export imports with warnings: %+v", res.Warnings)
	}
	want, got := e.designShape(source), e.designShape(target)
	if len(got) != len(want) {
		t.Fatalf("target has %d fields, source %d", len(got), len(want))
	}
	for name, shape := range want {
		if got[name] != shape {
			t.Errorf("%s:\n  source %s\n  target %s", name, shape, got[name])
		}
	}
}

// The export's own vocabulary (§4.10 export table), field by field.
func TestExportDictionaryVocabulary(t *testing.T) {
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	cases := []struct {
		field db.Field
		want  []string // Field Type, Choices, Validation, Matrix Group Name
	}{
		{db.Field{FieldType: "matrix", Choices: str("1$Low##2$High"), MatrixGroup: str("grid")}, []string{"radio", "1, Low | 2, High", "", "grid"}},
		{db.Field{FieldType: "header"}, []string{"descriptive", "", "", ""}},
		{db.Field{FieldType: "description"}, []string{"descriptive", "", "", ""}},
		{db.Field{FieldType: "calculated", Calculation: str("[e][a] + 1")}, []string{"calc", "[e][a] + 1", "", ""}},
		{db.Field{FieldType: "yesno", Choices: str("1$Yes##0$No")}, []string{"yesno", "", "", ""}},
		{db.Field{FieldType: "text", ValidationType: str("date")}, []string{"text", "", "date_ymd", ""}},
		{db.Field{FieldType: "text", ValidationType: str("date"), ValidationFormat: str("m-d-Y")}, []string{"text", "", "date_mdy", ""}},
		{db.Field{FieldType: "text", ValidationType: str("datetime"), ValidationFormat: str("d-m-Y H:i")}, []string{"text", "", "datetime_dmy", ""}},
		{db.Field{FieldType: "text", ValidationType: str("floating point")}, []string{"text", "", "number", ""}},
		{db.Field{FieldType: "text", ValidationType: str("national phone")}, []string{"text", "", "national phone", ""}},
	}
	for _, c := range cases {
		row := redcapRow(c.field, "f")
		if got := []string{row[3], row[5], row[7], row[15]}; strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s/%s: got %q, want %q", c.field.FieldType, c.field.ValidationType.String, got, c.want)
		}
		if len(row) != 18 {
			t.Errorf("a row has %d columns, REDCap's header 18", len(row))
		}
	}
	now := time.Date(2026, 10, 12, 9, 5, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"Incoming":            "Incoming_2026-10-12_0905.zip",
		"Data Transfer: 8/10": "Data_Transfer_8_10_2026-10-12_0905.zip",
		"æøå":                 "project_2026-10-12_0905.zip",
	} {
		if got := dictionaryFilename(in, now); got != want {
			t.Errorf("dictionaryFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if originHost("") != "clara" || originHost("https://clara.example.org:8443/x") != "clara.example.org" {
		t.Errorf("originHost: %q / %q", originHost(""), originHost("https://clara.example.org:8443/x"))
	}
}

func TestExportDictionaryAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("admin@example.org")
	projectID := e.createProjectViaAPI(admin, "Closed")
	outsider := e.mustUser("outsider@example.org")
	if rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/instruments/export", nil, outsider); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("a non-member got %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/999999/instruments/export", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown project got %d", rec.Code)
	}
}
