package admin

// Reading REDCap dictionaries for the instrument import (REQ-API-152,
// API_Endpoints_Design.md §4.10): an instrument ZIP — REDCap's instrument
// export, instrument.csv plus OriginID.txt — or a bare data-dictionary CSV,
// converted row by row into this system's field rows. Nothing here reads the
// project: the names, the record identifier and the design-time rules are
// applied afterwards, against the design the import lands in (import.go).

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"database/sql"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// importCSVLimit bounds the instrument.csv read out of an archive, so a small
// compressed file cannot expand without limit (§4.10).
const importCSVLimit = 16 << 20

// importNote is one warning or failure of an import, located by file and
// spreadsheet row (the header is row 1).
type importNote struct {
	File    string `json:"file"`
	Row     int    `json:"row,omitempty"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// redcapField is one converted dictionary row: the field as it will be
// created (its name still as written — the import renames afterwards), the
// REDCap type it came from, and a calculation's REDCap source, which only
// becomes an expression here once the project's first event is known.
type redcapField struct {
	row        int
	sourceType string
	field      db.Field
	calcSource string
}

// redcapForm is one instrument of a file: its Form Name and its fields in row order.
type redcapForm struct {
	name     string
	file     string
	origin   string
	firstRow int
	fields   []redcapField
}

// importReader accumulates the forms, warnings and failures of every file.
type importReader struct {
	registry map[string]bool // validation-type registry names (§4.2)
	forms    []redcapForm
	warnings []importNote
	problems []importNote
	// first is the import's first dictionary row — the field that moves into an
	// empty first instrument and becomes the record identifier (GD-8).
	first struct {
		seen             bool
		file, name, kind string
		row              int
		kept             bool
	}
}

func (rd *importReader) warn(file string, row int, field, code, format string, args ...any) {
	rd.warnings = append(rd.warnings, importNote{File: file, Row: row, Field: field, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (rd *importReader) fail(file string, row int, field, code, format string, args ...any) {
	rd.problems = append(rd.problems, importNote{File: file, Row: row, Field: field, Code: code, Message: fmt.Sprintf(format, args...)})
}

// zipSignature opens every ZIP archive (PK\x03\x04).
var zipSignature = []byte("PK\x03\x04")

// readFile reads one uploaded file: an archive is opened for instrument.csv and
// OriginID.txt only; anything else is the CSV itself.
func (rd *importReader) readFile(name string, content []byte) {
	origin := ""
	data := content
	if bytes.HasPrefix(content, zipSignature) {
		zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			rd.fail(name, 0, "", "invalid_archive", "the file is not a readable ZIP archive: %s", err)
			return
		}
		var csvFile *zip.File
		var ignored []string
		for _, f := range zr.File {
			switch f.Name {
			case "instrument.csv":
				csvFile = f
			case "OriginID.txt":
				origin = readOrigin(f)
			default:
				if !f.FileInfo().IsDir() {
					ignored = append(ignored, f.Name)
				}
			}
		}
		if len(ignored) > 0 {
			rd.warn(name, 0, "", "ignored_entry", "ignored %d other archive entries (%s) — only instrument.csv is imported", len(ignored), strings.Join(ignored, ", "))
		}
		if csvFile == nil {
			rd.fail(name, 0, "", "missing_instrument_csv", "the archive holds no instrument.csv")
			return
		}
		rc, err := csvFile.Open()
		if err != nil {
			rd.fail(name, 0, "", "invalid_archive", "instrument.csv cannot be read: %s", err)
			return
		}
		data, err = io.ReadAll(io.LimitReader(rc, importCSVLimit+1))
		rc.Close()
		if err != nil {
			rd.fail(name, 0, "", "invalid_archive", "instrument.csv cannot be read: %s", err)
			return
		}
		if len(data) > importCSVLimit {
			rd.fail(name, 0, "", "invalid_csv", "instrument.csv is larger than %d MiB", importCSVLimit>>20)
			return
		}
	}
	rd.readCSV(name, origin, data)
}

// readOrigin reads OriginID.txt — the exporting server's name — bounded and trimmed.
func readOrigin(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 256))
	if err != nil || !utf8.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// dictionaryColumns maps REDCap's column headings — the data-dictionary CSV's,
// and the snake_case keys of its metadata export — to one key each.
var dictionaryColumns = map[string]string{
	"variable / field name": "name", "field_name": "name",
	"form name": "form", "form_name": "form",
	"section header": "section", "section_header": "section",
	"field type": "type", "field_type": "type",
	"field label": "label", "field_label": "label",
	"choices, calculations, or slider labels": "choices", "select_choices_or_calculations": "choices",
	"field note": "note", "field_note": "note",
	"text validation type or show slider number": "validation", "text_validation_type_or_show_slider_number": "validation",
	"text validation min": "min", "text_validation_min": "min",
	"text validation max": "max", "text_validation_max": "max",
	"identifier?": "identifier", "identifier": "identifier",
	"branching logic (show field only if...)": "branching", "branching_logic": "branching",
	"required field?": "required", "required_field": "required",
	"custom alignment": "alignment", "custom_alignment": "alignment",
	"question number (surveys only)": "question", "question_number": "question",
	"matrix group name": "matrix", "matrix_group_name": "matrix",
	"matrix ranking?": "ranking", "matrix_ranking": "ranking",
	"field annotation": "annotation", "field_annotation": "annotation",
}

// columnTitle names a column the way REDCap's own dictionary heads it.
var columnTitle = map[string]string{
	"alignment": "Custom Alignment", "question": "Question Number (surveys only)",
	"ranking": "Matrix Ranking?", "min": "Text Validation Min", "max": "Text Validation Max",
}

// readCSV parses one dictionary and converts its rows into forms.
func (rd *importReader) readCSV(file, origin string, data []byte) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // a spreadsheet's byte-order mark
	if !utf8.Valid(data) {
		rd.fail(file, 0, "", "invalid_csv", "the dictionary is not UTF-8 text")
		return
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		rd.fail(file, 1, "", "invalid_csv", "the dictionary has no readable header row: %s", csvReason(err))
		return
	}
	index := map[string]int{}
	for i, h := range header {
		if key, ok := dictionaryColumns[strings.ToLower(strings.TrimSpace(h))]; ok {
			if _, dup := index[key]; !dup {
				index[key] = i
			}
		}
	}
	missing := false
	for _, need := range []struct{ key, title string }{
		{"name", "Variable / Field Name"}, {"form", "Form Name"}, {"type", "Field Type"}, {"label", "Field Label"},
	} {
		if _, ok := index[need.key]; !ok {
			rd.fail(file, 1, "", "missing_column", "the header row has no %q column", need.title)
			missing = true
		}
	}
	if missing {
		return
	}

	formAt := map[string]int{} // form name → index in rd.forms, this file only
	current := ""
	row := 1
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			rd.fail(file, row, "", "invalid_csv", "the dictionary cannot be parsed: %s", csvReason(err))
			return
		}
		cols := map[string]string{}
		blank := true
		for key, i := range index {
			if i < len(record) {
				cols[key] = record[i]
				if strings.TrimSpace(record[i]) != "" {
					blank = false
				}
			}
		}
		if blank {
			continue
		}
		name := strings.TrimSpace(cols["name"])
		form := strings.TrimSpace(cols["form"])
		if form == "" {
			rd.fail(file, row, name, "validation_error", "the row names no form")
			continue
		}
		at, seen := formAt[form]
		if !seen {
			rd.forms = append(rd.forms, redcapForm{name: form, file: file, origin: origin, firstRow: row})
			at = len(rd.forms) - 1
			formAt[form] = at
		} else if form != current {
			rd.fail(file, row, name, "form_not_contiguous",
				"form %q, which started at row %d, continues here after form %q at row %d — the rows of a form must stand together",
				form, rd.forms[at].firstRow, current, rd.forms[formAt[current]].firstRow)
		}
		current = form
		f, kept := rd.convertRow(file, row, name, cols)
		if !rd.first.seen {
			rd.first.seen = true
			rd.first.file, rd.first.row, rd.first.name = file, row, name
			rd.first.kind, rd.first.kept = f.field.FieldType, kept
			if !kept {
				rd.first.kind = strings.ToLower(strings.TrimSpace(cols["type"]))
			}
		}
		if kept {
			rd.forms[at].fields = append(rd.forms[at].fields, f)
		}
	}
}

// csvReason is encoding/csv's message without its own line prefix — the note
// already carries the row.
func csvReason(err error) string {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// lineBreakRE matches REDCap's HTML line break in labels and notes, together
// with a newline that directly follows it in the source — the two are one break.
var lineBreakRE = regexp.MustCompile(`(?i)<br\s*/?>\n?`)

// textOf is a label, header or note with REDCap's <br> markup as line breaks
// and its line endings normalized; other markup stays as written and renders
// escaped (REQ-VAL-031).
func textOf(s string) sql.NullString {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = lineBreakRE.ReplaceAllString(s, "\n")
	s = strings.TrimSpace(s)
	return sql.NullString{String: s, Valid: s != ""}
}

func nullText(s string) sql.NullString {
	s = strings.TrimSpace(s)
	return sql.NullString{String: s, Valid: s != ""}
}

// convertRow turns one dictionary row into a field (§4.10 conversion tables).
// kept is false for a row that is not imported: a skipped file field or an
// unsupported type.
func (rd *importReader) convertRow(file string, row int, name string, cols map[string]string) (redcapField, bool) {
	sourceType := strings.ToLower(strings.TrimSpace(cols["type"]))
	f := db.Field{
		FieldName:       name,
		FieldLabel:      textOf(cols["label"]),
		SectionHeader:   textOf(cols["section"]),
		FieldNote:       textOf(cols["note"]),
		BranchingLogic:  nullText(cols["branching"]),
		FieldAnnotation: nullText(cols["annotation"]),
		Required:        strings.EqualFold(strings.TrimSpace(cols["required"]), "y"),
		// REDCap removes identifiers from a de-identified export, as this
		// system does with direct identifiers (REQ-EXP-020).
		DirectIdentifier: strings.EqualFold(strings.TrimSpace(cols["identifier"]), "y"),
	}
	out := redcapField{row: row, sourceType: sourceType}
	choices := strings.TrimSpace(cols["choices"])
	validation := strings.TrimSpace(cols["validation"])
	choiceType := false

	switch sourceType {
	case "text", "notes", "dropdown", "radio", "checkbox", "yesno":
		f.FieldType = sourceType
		choiceType = sourceType == "dropdown" || sourceType == "radio" || sourceType == "checkbox"
	case "truefalse":
		f.FieldType = "radio"
		f.Choices = sql.NullString{String: "1$True##0$False", Valid: true}
		rd.warn(file, row, name, "converted", "REDCap's truefalse field becomes a radio pair 1 = True, 0 = False")
	case "descriptive":
		f.FieldType = "description"
	case "calc":
		f.FieldType = "calculated"
		out.calcSource = choices
		choices = ""
	case "slider":
		f.FieldType = "text"
		f.ValidationType = sql.NullString{String: "integer", Valid: true}
		f.ValidationMin = sql.NullString{String: "0", Valid: true}
		f.ValidationMax = sql.NullString{String: "100", Valid: true}
		rd.warn(file, row, name, "converted", "REDCap's slider becomes an integer field from 0 to 100 — the value a slider stores")
		choices, validation = "", ""
		cols["min"], cols["max"] = "", ""
	case "file":
		rd.warn(file, row, name, "skipped", "file fields are not supported; the field is not imported")
		return out, false
	case "":
		rd.fail(file, row, name, "unsupported_type", "the row has no field type")
		return out, false
	default:
		rd.fail(file, row, name, "unsupported_type", "REDCap's %q field type has no counterpart here", sourceType)
		return out, false
	}

	if choiceType {
		f.Choices = nullText(validate.NormalizeChoices(choices))
	} else if choices != "" {
		rd.warn(file, row, name, "dropped_attribute", "the choices column is ignored for a %s field", sourceType)
	}

	if group := strings.TrimSpace(cols["matrix"]); group != "" {
		if f.FieldType == "radio" {
			f.FieldType = "matrix"
			f.MatrixGroup = sql.NullString{String: group, Valid: true}
		} else {
			rd.warn(file, row, name, "dropped_attribute", "matrix group %q is ignored: a %s matrix has no counterpart here", group, sourceType)
		}
	}

	if f.FieldType == "text" && validation != "" {
		rd.convertValidation(file, row, name, validation, &f)
	} else if validation != "" && sourceType != "slider" {
		rd.warn(file, row, name, "dropped_attribute", "validation %q is ignored for a %s field", validation, sourceType)
	}
	vt := f.ValidationType.String
	for _, key := range []string{"min", "max"} {
		v := strings.TrimSpace(cols[key])
		if v == "" {
			continue
		}
		if vt != "integer" && vt != "floating point" {
			rd.warn(file, row, name, "dropped_attribute", "%s %q applies to integer and number fields only (REQ-VAL-020)", columnTitle[key], v)
			continue
		}
		if key == "min" {
			f.ValidationMin = sql.NullString{String: v, Valid: true}
		} else {
			f.ValidationMax = sql.NullString{String: v, Valid: true}
		}
	}

	for _, key := range []string{"alignment", "question", "ranking"} {
		if v := strings.TrimSpace(cols[key]); v != "" {
			rd.warn(file, row, name, "dropped_attribute", "%s %q has no counterpart here and is dropped", columnTitle[key], v)
		}
	}
	out.field = f
	return out, true
}

// redcapDates maps REDCap's date and date-time validations to a structured
// type and its input format (§4.1 of Data_Validation_Design.md).
var redcapDates = map[string][2]string{
	"date_ymd": {"date", "Y-m-d"}, "date_dmy": {"date", "d-m-Y"}, "date_mdy": {"date", "m-d-Y"},
	"datetime_ymd": {"datetime", "Y-m-d H:i"}, "datetime_dmy": {"datetime", "d-m-Y H:i"},
	"datetime_mdy": {"datetime", "m-d-Y H:i"},
}

// convertValidation sets a text field's validation from REDCap's name (§4.10
// validation table): dates, integer, number and email map; any other name is
// kept when the registry knows it and dropped otherwise — the field stays
// free text.
func (rd *importReader) convertValidation(file string, row int, name, v string, f *db.Field) {
	set := func(vt, format string) {
		f.ValidationType = sql.NullString{String: vt, Valid: true}
		f.ValidationFormat = sql.NullString{String: format, Valid: format != ""}
	}
	if d, ok := redcapDates[v]; ok {
		set(d[0], d[1])
		return
	}
	switch {
	case v == "integer":
		set("integer", "")
	case v == "number":
		set("floating point", "")
	case strings.HasPrefix(v, "number_"):
		set("floating point", "")
		rd.warn(file, row, name, "converted", "validation %q becomes floating point; its decimal places or decimal comma are not enforced", v)
	case v == "email":
		set("email", "")
	case rd.registry[v]:
		set(v, "")
	default:
		rd.warn(file, row, name, "dropped_attribute", "validation %q has no counterpart here; the field stays free text", v)
	}
}
