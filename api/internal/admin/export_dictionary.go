package admin

// The data-dictionary export (REQ-API-153, API_Endpoints_Design.md §4.10,
// DEV-API-28): the project's active design as a REDCap instrument ZIP —
// instrument.csv in REDCap's data-dictionary headings and vocabulary, every
// instrument's rows naming it in Form Name, plus OriginID.txt. It is the
// instrument import's conversion run backwards (import_redcap.go), so the
// file imports into another project as the same fields.

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// redcapDictionaryHeader is REDCap's data-dictionary header line, written the
// way REDCap writes it.
const redcapDictionaryHeader = `"Variable / Field Name","Form Name","Section Header","Field Type","Field Label",` +
	`"Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number",` +
	`"Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)",` +
	`"Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?",` +
	`"Field Annotation"` + "\n"

// exportInstruments is GET /api/v1/projects/{id}/instruments/export. Data
// access ≥ read_only: the file carries design, no record values.
func (h *Handler) exportInstruments(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	ctx := r.Context()
	project, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if project == nil {
		errNotFound(w)
		return
	}
	// The active design, as data collection uses it — never an open staging
	// set (REQ-API-107).
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	now := time.Now().UTC()
	archive, err := dictionaryArchive(instruments, fields, originHost(h.Cfg.WebPublicURL), now)
	if err != nil {
		errInternal(w)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+dictionaryFilename(project.ProjectName, now)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

// dictionaryArchive builds the ZIP: instrument.csv and OriginID.txt.
func dictionaryArchive(instruments []db.Instrument, fields []db.Field, origin string, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body []byte
	}{
		{"OriginID.txt", []byte(origin)},
		{"instrument.csv", dictionaryCSV(instruments, fields)},
	} {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(entry.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dictionaryCSV writes the header and one row per field in the order ListFields
// gives them — instruments by position, fields by position — so the record
// identifier (GD-8) is the first row.
func dictionaryCSV(instruments []db.Instrument, fields []db.Field) []byte {
	form := make(map[int64]string, len(instruments))
	for _, i := range instruments {
		form[i.ID] = i.Name
	}
	var buf bytes.Buffer
	buf.WriteString(redcapDictionaryHeader)
	cw := csv.NewWriter(&buf)
	for _, f := range fields {
		_ = cw.Write(redcapRow(f, form[f.InstrumentID])) // a bytes.Buffer never fails a write
	}
	cw.Flush()
	return buf.Bytes()
}

// redcapRow is one field in REDCap's columns and vocabulary (§4.10 export table).
func redcapRow(f db.Field, form string) []string {
	fieldType, choices, group := f.FieldType, "", ""
	switch f.FieldType {
	case "dropdown", "radio", "checkbox":
		choices = redcapChoices(f.Choices.String)
	case "matrix":
		// REDCap's matrix is radio rows sharing a group name; the import reads
		// them back as matrix rows (REQ-DB-014).
		fieldType, choices, group = "radio", redcapChoices(f.Choices.String), f.MatrixGroup.String
	case "description", "header":
		fieldType = "descriptive"
	case "calculated":
		fieldType, choices = "calc", f.Calculation.String
	}
	yes := func(b bool) string {
		if b {
			return "y"
		}
		return ""
	}
	return []string{
		f.FieldName, form, f.SectionHeader.String, fieldType, f.FieldLabel.String,
		choices, f.FieldNote.String, redcapValidation(f.ValidationType.String, f.ValidationFormat.String),
		f.ValidationMin.String, f.ValidationMax.String, yes(f.DirectIdentifier), f.BranchingLogic.String,
		yes(f.Required), "", "", group, "", f.FieldAnnotation.String,
	}
}

// redcapChoices writes the stored code$label##… encoding in REDCap's form
// "code, label | code, label" (REQ-VAL-022).
func redcapChoices(stored string) string {
	var parts []string
	for _, code := range validate.ChoiceCodes(stored) {
		parts = append(parts, code+", "+validate.LabelForCode(stored, code))
	}
	return strings.Join(parts, " | ")
}

// redcapValidation names a validation the way REDCap does: integer, number,
// the date and date-time names by the order of the format's Y, m and d
// tokens, and a registry type by its own name.
func redcapValidation(validationType, format string) string {
	switch validationType {
	case "integer", "":
		return validationType
	case "floating point":
		return "number"
	case "date", "datetime":
		return validationType + "_" + dateOrder(format)
	}
	return validationType
}

// dateOrder is ymd, dmy or mdy by where the format puts its date tokens; an
// unset format is the Y-m-d default (§4.1), and any other order — which
// REDCap cannot name — is written as ymd.
func dateOrder(format string) string {
	var order []rune
	for _, c := range format {
		if c == 'Y' || c == 'm' || c == 'd' {
			order = append(order, c)
		}
	}
	switch string(order) {
	case "dmY":
		return "dmy"
	case "mdY":
		return "mdy"
	}
	return "ymd"
}

// unsafeFilename is every run of characters a download name should not carry.
var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// dictionaryFilename is <project>_<YYYY-MM-DD>_<HHMM>.zip, the time in UTC.
func dictionaryFilename(projectName string, now time.Time) string {
	base := strings.Trim(unsafeFilename.ReplaceAllString(projectName, "_"), "._-")
	if base == "" {
		base = "project"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return base + "_" + now.Format("2006-01-02_1504") + ".zip"
}

// originHost is what OriginID.txt names: this installation's host, as REDCap
// names its server.
func originHost(publicURL string) string {
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "clara"
}
