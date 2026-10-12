package admin

// The instrument import (REQ-API-152, API_Endpoints_Design.md §4.10,
// DEV-API-27): REDCap instrument ZIPs or data-dictionary CSVs become
// instruments and fields of a project in one all-or-nothing change.
//
// The import is computed on a design snapshot (staged_design.go) rather than
// on the tables, which gives every mode one path: a dry run applies it to a
// throwaway copy; production applies it to the open staging set; development
// and analysis apply it to a copy of the live design and write the difference
// with the staging commit's own applier, in one transaction (staged_apply.go).
// The import maps no instrument to an event — nothing it creates collects data
// before a person assigns it (REQ-DB-012).

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// importBodyLimit bounds the request body (REQ-TECH-011); the files travel
// base64-encoded inside it.
const importBodyLimit = 32 << 20

// importFieldOut is one field of the result.
type importFieldOut struct {
	ID               *int64  `json:"id"`
	Row              int     `json:"row"`
	FieldName        string  `json:"field_name"`
	RenamedFrom      *string `json:"renamed_from"`
	SourceType       string  `json:"source_type"`
	FieldType        string  `json:"field_type"`
	ValidationType   string  `json:"validation_type"`
	ValidationFormat *string `json:"validation_format"`
	pid              int64   // the field's id in the design the import was applied to
}

// importInstrumentOut is one instrument of the result. Existing marks the
// project's empty first instrument, which receives the import's first field
// (GD-8) rather than being created.
type importInstrumentOut struct {
	ID          *int64           `json:"id"`
	Name        string           `json:"name"`
	RenamedFrom *string          `json:"renamed_from"`
	Existing    bool             `json:"existing"`
	File        string           `json:"file"`
	Origin      string           `json:"origin"`
	Fields      []importFieldOut `json:"fields"`
	pid         int64
}

type importIdentifierOut struct {
	Field      string `json:"field"`
	Instrument string `json:"instrument"`
	Moved      bool   `json:"moved"`
}

// importResult is the §4.10 response of both the dry run and the import.
type importResult struct {
	DryRun           bool                  `json:"dry_run"`
	Importable       bool                  `json:"importable"`
	RecordIdentifier *importIdentifierOut  `json:"record_identifier"`
	Instruments      []importInstrumentOut `json:"instruments"`
	Warnings         []importNote          `json:"warnings"`
	Problems         []importNote          `json:"problems"`
}

type importUpload struct {
	name    string
	content []byte
}

// decodeImportBody reads { "files": [ { "name", "content" } ], "dry_run",
// "acknowledge_breaking" } — content base64-encoded — within the body bound.
func decodeImportBody(w http.ResponseWriter, r *http.Request) (files []importUpload, dryRun, ack, ok bool) {
	if r.Body == nil {
		errBadRequest(w, "files must be a non-empty array of {name, content} objects")
		return nil, false, false, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, importBodyLimit+1))
	if err != nil {
		errBadRequest(w, "malformed JSON body")
		return nil, false, false, false
	}
	if len(raw) > importBodyLimit {
		errBadRequest(w, fmt.Sprintf("the request is larger than %d MiB — import fewer files at once", importBodyLimit>>20))
		return nil, false, false, false
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(raw, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return nil, false, false, false
	}
	if !RejectUnknownAttrs(w, supplied, "files", "dry_run", "acknowledge_breaking") {
		return nil, false, false, false
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(supplied["files"], &entries); err != nil || len(entries) == 0 {
		errBadRequest(w, "files must be a non-empty array of {name, content} objects")
		return nil, false, false, false
	}
	for _, e := range entries {
		if !RejectUnknownAttrs(w, e, "name", "content") {
			return nil, false, false, false
		}
		var name, content string
		if json.Unmarshal(e["name"], &name) != nil || name == "" || json.Unmarshal(e["content"], &content) != nil {
			errBadRequest(w, "every file needs a name and its content")
			return nil, false, false, false
		}
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			errBadRequest(w, fmt.Sprintf("the content of %q is not base64", name))
			return nil, false, false, false
		}
		files = append(files, importUpload{name: name, content: data})
	}
	if v, present := supplied["dry_run"]; present {
		if err := json.Unmarshal(v, &dryRun); err != nil {
			errBadRequest(w, "dry_run must be true or false")
			return nil, false, false, false
		}
	}
	return files, dryRun, acknowledgeFrom(supplied), true
}

// importInstruments is POST /api/v1/projects/{id}/instruments/import. project_admin.
func (h *Handler) importInstruments(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	files, dryRun, ack, ok := decodeImportBody(w, r)
	if !ok {
		return
	}
	types, err := h.Store.ListValidationTypes(ctx)
	if err != nil {
		errInternal(w)
		return
	}
	rd := &importReader{registry: map[string]bool{}}
	for _, t := range types {
		rd.registry[t.Name] = true
	}
	for _, f := range files {
		rd.readFile(f.name, f.content)
	}

	var result *importResult
	apply := func(d *stagedDesign) *designError {
		result = h.applyImport(ctx, projectID, d, rd)
		if len(result.Problems) > 0 {
			return validationf("%s", noteText(result.Problems[0]))
		}
		return nil
	}

	// Every request first runs on a throwaway copy of the design it would land
	// in — the open set, else the live design — which is the whole dry run and,
	// for the import itself, the check that nothing fails before anything is
	// written.
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	probe := staged
	if probe == nil {
		probe, err = h.stagedDesignOf(ctx, projectID)
	} else {
		probe, err = probe.clone()
	}
	if err != nil {
		errInternal(w)
		return
	}
	failed := apply(probe) != nil
	if dryRun {
		result.DryRun = true
		writeJSON(w, http.StatusOK, result)
		return
	}
	if failed {
		writeImportFailure(w, result)
		return
	}

	target, set, acknowledged, ok := h.structureWrite(w, r, projectID, ack, apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		// Staged objects carry provisional ids until the set commits (§4.21).
		result.fillIDs(func(name string) (int64, bool) { return 0, false }, true)
		writeJSON(w, http.StatusCreated, result)
		return
	}

	active, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	next, err := active.clone()
	if err != nil {
		errInternal(w)
		return
	}
	if apply(next) != nil {
		writeImportFailure(w, result)
		return
	}
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err := h.applyStagedDesignTx(ctx, tx, projectID, active, next); err != nil {
		writeApplyError(w, err)
		return
	}
	for _, entry := range result.auditEntries(next) {
		entry.Source, entry.UserID, entry.Email, entry.ProjectID = audit.SourceUI, u.ID, u.Email, projectID
		details, _ := entry.Details.(map[string]any)
		entry.Details = withBreakingAcknowledgement(details, acknowledged)
		if err := h.Audit.InsertTx(ctx, tx, entry); err != nil {
			errInternal(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	h.Store.BumpStructureCache()

	ids, err := h.liveIDs(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	result.fillIDs(func(name string) (int64, bool) { id, ok := ids[name]; return id, ok }, false)
	writeJSON(w, http.StatusCreated, result)
}

// applyImport applies the read dictionaries to one design (§4.10 steps 2–5):
// the empty-first-instrument rule, name allocation with `_2`, reference
// rewriting, and the design-time rules of a designer write. It fills a fresh
// result each call — the structure write may run it more than once.
func (h *Handler) applyImport(ctx context.Context, projectID int64, d *stagedDesign, rd *importReader) *importResult {
	res := &importResult{
		Instruments: []importInstrumentOut{},
		Warnings:    append([]importNote{}, rd.warnings...),
		Problems:    append([]importNote{}, rd.problems...),
	}
	defer func() { res.Importable = len(res.Problems) == 0 }()
	total := 0
	for _, form := range rd.forms {
		total += len(form.fields)
	}
	if total == 0 {
		if len(res.Problems) == 0 {
			res.Problems = append(res.Problems, importNote{Code: "invalid_csv", Message: "the files hold no field to import"})
		}
		return res
	}
	firstEvent := d.designContext(projectID).firstEvent

	// Names already in use: fields with their checkbox value columns
	// (REQ-DB-042), and instruments.
	fieldTaken := map[string]bool{}
	instTaken := map[string]bool{}
	for _, si := range d.Instruments {
		instTaken[si.Name] = true
		for _, sf := range si.Fields {
			for _, n := range occupiedNames(sf.FieldName, sf.FieldType, sf.Choices.String) {
				fieldTaken[n] = true
			}
		}
	}
	free := func(name, fieldType, choices string) bool {
		for _, n := range occupiedNames(name, fieldType, choices) {
			if fieldTaken[n] {
				return false
			}
		}
		return true
	}

	// Step 4: every field gets the first free name, in import order; a rename
	// is a warning and redirects the references of its own file.
	renames := map[string]map[string]string{} // file → name as written → name given
	fileBoxes := map[string]map[string]bool{} // file → checkbox value columns as written
	names := make([][]string, len(rd.forms))
	for fi, form := range rd.forms {
		names[fi] = make([]string, len(form.fields))
		if fileBoxes[form.file] == nil {
			fileBoxes[form.file] = map[string]bool{}
		}
		for k, rf := range form.fields {
			f := rf.field
			if f.FieldType == "checkbox" {
				for _, code := range validate.ChoiceCodes(f.Choices.String) {
					fileBoxes[form.file][db.CheckboxSubName(f.FieldName, code)] = true
				}
			}
			name := f.FieldName
			for i := 2; !free(name, f.FieldType, f.Choices.String); i++ {
				name = f.FieldName + "_" + strconv.Itoa(i)
			}
			for _, n := range occupiedNames(name, f.FieldType, f.Choices.String) {
				fieldTaken[n] = true
			}
			names[fi][k] = name
			if name != f.FieldName {
				if renames[form.file] == nil {
					renames[form.file] = map[string]string{}
				}
				renames[form.file][f.FieldName] = name
				res.Warnings = append(res.Warnings, importNote{File: form.file, Row: rf.row, Field: f.FieldName, Code: "renamed",
					Message: fmt.Sprintf("field %q is already in use and is imported as %q", f.FieldName, name)})
			}
			if len(name) > 26 {
				res.Warnings = append(res.Warnings, importNote{File: form.file, Row: rf.row, Field: name, Code: "long_name",
					Message: fmt.Sprintf("field name %q is longer than 26 characters", name)})
			}
		}
	}

	// The field as it will be created: final name, references following the
	// renames of its own file, a calculation qualified with the first event.
	build := func(fi, k int) db.Field {
		form, rf := rd.forms[fi], rd.forms[fi].fields[k]
		f := rf.field
		f.ProjectID = projectID
		f.FieldName = names[fi][k]
		boxes := fileBoxes[form.file]
		isBox := func(name string) bool { return boxes[name] }
		if f.BranchingLogic.Valid {
			f.BranchingLogic.String = validate.RenameRefs(f.BranchingLogic.String, renames[form.file], isBox)
		}
		if f.FieldType == "calculated" {
			expr := validate.QualifyRefs(validate.RenameRefs(rf.calcSource, renames[form.file], isBox), firstEvent)
			f.Calculation = sql.NullString{String: expr, Valid: expr != ""}
		}
		fixTypeAttrs(&f)
		if !f.DirectIdentifier && validate.PresetDirectIdentifier(f.ValidationType.String) {
			f.DirectIdentifier = true
		}
		return f
	}

	// Step 2: an empty first instrument receives the import's first field,
	// which becomes the record identifier (GD-8). A first field that cannot be
	// the identifier is a failure; nothing moves then, but every instrument is
	// still placed so the preview shows the whole import with its problems.
	instruments := sortedInstruments(d.Instruments)
	firstInst := instruments[0]
	moved := len(firstInst.Fields) == 0
	if moved {
		first := rd.first
		switch {
		case !first.kept:
			res.Problems = append(res.Problems, importNote{File: first.file, Row: first.row, Field: first.name, Code: "identifier_not_value",
				Message: fmt.Sprintf("field %q would become the record identifier, but a %s field is not imported — import another file first", first.name, first.kind)})
			moved = false
		case !identifierType(first.kind):
			res.Problems = append(res.Problems, importNote{File: first.file, Row: first.row, Field: first.name, Code: "identifier_not_value",
				Message: fmt.Sprintf("field %q would become the record identifier, but a %s field carries no value of its own — import another file first", first.name, first.kind)})
			moved = false
		}
	} else if fields := sortedFields(firstInst.Fields); len(fields) > 0 {
		res.RecordIdentifier = &importIdentifierOut{Field: fields[0].FieldName, Instrument: firstInst.Name}
	}

	type placement struct {
		instOut, fieldOut int
		instID, fieldID   int64
		file              string
		row               int
	}
	var places []placement
	fieldOut := func(rf redcapField, f db.Field, pid int64) importFieldOut {
		out := importFieldOut{Row: rf.row, FieldName: f.FieldName, SourceType: rf.sourceType, FieldType: f.FieldType,
			ValidationType: f.ValidationType.String, ValidationFormat: NullStrPtr(f.ValidationFormat), pid: pid}
		if f.FieldName != rf.field.FieldName {
			from := rf.field.FieldName
			out.RenamedFrom = &from
		}
		return out
	}
	if moved {
		form := rd.forms[0]
		f := build(0, 0)
		sf, _ := d.putField(firstInst.ID, f)
		res.Instruments = append(res.Instruments, importInstrumentOut{
			Name: firstInst.Name, Existing: true, File: form.file, Origin: form.origin, pid: firstInst.ID,
			Fields: []importFieldOut{fieldOut(form.fields[0], f, sf.ID)},
		})
		places = append(places, placement{instOut: 0, fieldOut: 0, instID: firstInst.ID, fieldID: sf.ID, file: form.file, row: form.fields[0].row})
		res.RecordIdentifier = &importIdentifierOut{Field: f.FieldName, Instrument: firstInst.Name, Moved: true}
	}
	for fi, form := range rd.forms {
		start := 0
		if fi == 0 && moved {
			start = 1
		}
		if start >= len(form.fields) {
			if len(form.fields) == 0 {
				res.Warnings = append(res.Warnings, importNote{File: form.file, Row: form.firstRow, Code: "skipped",
					Message: fmt.Sprintf("form %q has no field left to import; no instrument is created for it", form.name)})
			}
			continue
		}
		name := form.name
		for i := 2; instTaken[name]; i++ {
			name = form.name + "_" + strconv.Itoa(i)
		}
		instTaken[name] = true
		out := importInstrumentOut{Name: name, File: form.file, Origin: form.origin}
		if name != form.name {
			from := form.name
			out.RenamedFrom = &from
			res.Warnings = append(res.Warnings, importNote{File: form.file, Row: form.firstRow, Code: "renamed",
				Message: fmt.Sprintf("instrument %q is already in use and is imported as %q", form.name, name)})
		}
		instID := d.putInstrument(db.Instrument{ProjectID: projectID, Name: name}).ID
		out.pid = instID
		res.Instruments = append(res.Instruments, out)
		at := len(res.Instruments) - 1
		for k := start; k < len(form.fields); k++ {
			f := build(fi, k)
			sf, _ := d.putField(instID, f)
			res.Instruments[at].Fields = append(res.Instruments[at].Fields, fieldOut(form.fields[k], f, sf.ID))
			places = append(places, placement{instOut: at, fieldOut: len(res.Instruments[at].Fields) - 1,
				instID: instID, fieldID: sf.ID, file: form.file, row: form.fields[k].row})
		}
	}

	// Step 5: the design-time rules of a designer write, against the design
	// that already holds every imported field — so a reference may point
	// forward, as REDCap allows. References to imported fields skip the
	// active-at-event rule: the import maps nothing (designCtx.imported).
	dc := d.designContext(projectID)
	dc.imported = map[string]bool{}
	for _, p := range places {
		if sf, ok := d.field(p.fieldID); ok {
			for _, n := range occupiedNames(sf.FieldName, sf.FieldType, sf.Choices.String) {
				dc.imported[n] = true
			}
		}
	}
	for _, p := range places {
		sf, ok := d.field(p.fieldID)
		if !ok {
			continue
		}
		f := sf.field(projectID)
		if _, derr := h.validateFieldDesign(ctx, &f, f.ID, dc); derr != nil {
			res.Problems = append(res.Problems, importNote{File: p.file, Row: p.row, Field: f.FieldName, Code: "validation_error", Message: derr.msg})
			continue
		}
		d.putField(p.instID, f) // the rules may normalize (min/max, format)
		out := &res.Instruments[p.instOut].Fields[p.fieldOut]
		out.ValidationType, out.ValidationFormat = f.ValidationType.String, NullStrPtr(f.ValidationFormat)
	}
	return res
}

// occupiedNames is every name a field claims in the project's name space: its
// own and, for a checkbox, each value column `<name>___<code>` (REQ-DB-042).
func occupiedNames(name, fieldType, choices string) []string {
	out := []string{name}
	if fieldType == "checkbox" {
		for _, code := range validate.ChoiceCodes(choices) {
			out = append(out, db.CheckboxSubName(name, code))
		}
	}
	return out
}

// identifierType reports whether a field of this type can be the record
// identifier: it must carry a value of its own that a person enters (GD-8).
func identifierType(fieldType string) bool {
	switch fieldType {
	case "description", "header", "calculated", "checkbox":
		return false
	}
	return true
}

// fillIDs sets the ids of the created objects: the provisional ids of the
// design the import was applied to (a staging set), or the live ids looked up
// by name after the write.
func (res *importResult) fillIDs(live func(name string) (int64, bool), provisional bool) {
	for i := range res.Instruments {
		in := &res.Instruments[i]
		if provisional {
			id := in.pid
			in.ID = &id
		} else if id, ok := live("instrument:" + in.Name); ok {
			in.ID = &id
		}
		for k := range in.Fields {
			f := &in.Fields[k]
			if provisional {
				id := f.pid
				f.ID = &id
			} else if id, ok := live("field:" + f.FieldName); ok {
				f.ID = &id
			}
		}
	}
}

// liveIDs maps the project's instrument and field names to their ids.
func (h *Handler) liveIDs(ctx context.Context, projectID int64) (map[string]int64, error) {
	out := map[string]int64{}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, i := range instruments {
		out["instrument:"+i.Name] = i.ID
	}
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		out["field:"+f.FieldName] = f.ID
	}
	return out, nil
}

// auditEntries are the §3.3 entries of a live import: instrument_created per
// created instrument and field_created per field, each naming its source file
// and origin (Audit_Logging_Design.md §3.3).
func (res *importResult) auditEntries(d *stagedDesign) []audit.Entry {
	var out []audit.Entry
	for _, in := range res.Instruments {
		from := map[string]any{"file": in.File, "origin": in.Origin}
		if !in.Existing {
			details := map[string]any{"name": in.Name, "imported_from": from}
			if si, ok := d.instrument(in.pid); ok {
				details["position"] = si.Position
			}
			if in.RenamedFrom != nil {
				details["renamed_from"] = *in.RenamedFrom
			}
			out = append(out, audit.Entry{EventType: audit.InstrumentCreated, Details: details})
		}
		for _, f := range in.Fields {
			details := map[string]any{"instrument": in.Name, "field": f.FieldName, "type": f.FieldType, "imported_from": from}
			if f.RenamedFrom != nil {
				details["renamed_from"] = *f.RenamedFrom
			}
			out = append(out, audit.Entry{EventType: audit.FieldCreated, Details: details})
		}
	}
	return out
}

// noteText renders a note with its place: "file, row 12: message".
func noteText(n importNote) string {
	place := n.File
	if n.Row > 0 {
		if place != "" {
			place += ", "
		}
		place += "row " + strconv.Itoa(n.Row)
	}
	if place == "" {
		return n.Message
	}
	return place + ": " + n.Message
}

// writeImportFailure answers a refused import: 400 validation_error naming the
// first problem, with every problem and warning listed (§4.10).
func writeImportFailure(w http.ResponseWriter, res *importResult) {
	msg := "the import cannot be applied"
	if len(res.Problems) > 0 {
		msg = noteText(res.Problems[0])
		if len(res.Problems) > 1 {
			msg += fmt.Sprintf(" (and %d more)", len(res.Problems)-1)
		}
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": "validation_error", "message": msg, "status": http.StatusBadRequest,
		"problems": res.Problems, "warnings": res.Warnings,
	})
}
