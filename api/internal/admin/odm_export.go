package admin

// GET /api/v1/projects/{id}/export/odm (REQ-API-169, Design/ODM_Interchange_Design.md):
// the project as one CDISC ODM document — Study + MetaDataVersion describing
// the current design, then ClinicalData carrying the records — streamed, an
// installation administrator's route (REQ-ODM-002). `odm_content=project_only`
// stops after the metadata and writes no ClinicalData (REQ-ODM-066); the default,
// `project_and_data`, is the document this route always returned.
//
// Two halves, deliberately built differently. The metadata is a single pass over
// the design tables (§3): small, and everything must be declared before any value
// refers to it. The values come from the shared sensitivity pipeline as rows
// (dataapi.RunExportRows, O-6/REQ-ODM-021) — this file never reads the `data`
// table and never transforms anything, so an ODM file holds exactly what a CSV of
// the same call would hold: same per-pair levels, hashes, date shifts, checkbox
// columns and record scope. Skipping that pipeline is therefore the one thing this
// format does not get for free: a design-only export writes its own audit row.

import (
	"context"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

func (h *Handler) registerODM(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/export/odm", h.exportODM)
	mux.HandleFunc("POST /api/v1/projects/import/odm", h.importODM)
}

// noEventOID names the synthetic event an ODM document of a project without
// events groups its data under: ODM requires a StudyEventData level, and CLARA
// allows a project with none (an instrument mapped to no event). Import reads it
// back as "no events" through clara:Event/@synthetic.
const (
	noEventOID   = "EVT___clara_no_event__"
	noEventName  = "__clara_no_event__"
	yesAttr      = "Yes"
	noAttr       = "No"
	itemDataName = "ItemData"
)

// The two contents `odm_content` selects (REQ-ODM-066): the whole study as before,
// or its design alone — one `Study` and one `MetaDataVersion`, no `ClinicalData`.
// The default is the historical document, so a caller that names nothing gets what
// it got before this option existed.
const (
	odmContentAll    = "project_and_data"
	odmContentDesign = "project_only"
)

// odmContentFor reads the `odm_content` query parameter: an omitted value is the
// default, and anything outside the two names answers 400 rather than quietly
// widening or narrowing what left the installation (REQ-ODM-066).
func odmContentFor(value string) (string, bool) {
	switch value {
	case "":
		return odmContentAll, true
	case odmContentAll, odmContentDesign:
		return value, true
	default:
		return "", false
	}
}

func (h *Handler) exportODM(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r) // REQ-ODM-002: above project_admin, a whole study in one file
	if !ok {
		return
	}
	dialect, ok := odmDialectForVersion(r.URL.Query().Get("odm_version"))
	if !ok {
		errBadRequest(w, "odm_version must be 1.3 or 2.0")
		return
	}
	content, ok := odmContentFor(r.URL.Query().Get("odm_content"))
	if !ok {
		errBadRequest(w, "odm_content must be project_and_data or project_only")
		return
	}
	designOnly := content == odmContentDesign // the study's design, with no record values
	projectID, ok := pathID(r, "id")
	if !ok {
		errNotFound(w)
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
	lv, err := h.access(ctx, u, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	arms, eventsByArm, err := h.CanonicalEvents(ctx, projectID) // GD-15 order
	if err != nil {
		errInternal(w)
		return
	}
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
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}

	// The plan is built before the stream starts: it decides which items the
	// document may declare, and a document that cannot name its records is refused
	// here rather than after the pipeline has run and audited (REQ-AUD-004).
	//
	// ranks holds, per instrument, the most permissive export level any candidate
	// pair of it holds — the reading §3.9 uses, so an item is declared exactly when
	// a value of it can reach the file.
	ranks := map[int64]int{}
	for _, instr := range instruments {
		best := 0
		for _, a := range arms {
			for _, e := range eventsByArm[a.ArmNum] {
				if r := lv.PairOrArmDefault(e.ID, instr.ID, a.ArmNum).ExportRankOf(); r > best {
					best = r
				}
			}
		}
		ranks[instr.ID] = best
	}
	plan := newODMPlan(project, arms, eventsByArm, instruments, fields, pairs, ranks)
	plan.v13 = dialect.isODM13() // the two dialects differ in vocabulary (REQ-ODM-027/029)

	// The identifier column decides what keys the document can use: where the
	// applied level removed it, ODM still requires a key per record, so the plan
	// numbers records `S1`, `S2`, … in this file's own first-appearance order and
	// the response header says so — an operator must not read one export's `S17`
	// as another's (REQ-ODM-031, O-5). A surrogate is never a hash of the stored
	// identifier: §5.1's hash is a salt-recoverable pseudonym, which a level that
	// deletes the identifier does not license. A design-only document carries no
	// records to key, so it makes no claim about keys at all (REQ-ODM-066).
	if !plan.identifierDelivered && !designOnly {
		w.Header().Set("X-CLARA-ODM-Key", "surrogate")
	}
	counts := newODMCounts(dialect.selection, content)

	// The row stream starts before a byte is written: a scope holding no permitted
	// pair answers as an error, not as a document that begins `<?xml` (§6.1). A
	// design-only document reads no records, so it starts nothing — and asks no
	// data-access-group question either, which bounds records rather than columns
	// (REQ-API-092, REQ-ODM-066).
	var rows *dataapi.RowSource
	if !designOnly {
		// The record scope is the data-access-group rule (REQ-API-092), as on the
		// tabular export: the level governs transformation, the group governs records.
		var groupID *int64
		if asg, err := h.Store.GetAssignment(ctx, u.ID, projectID); err != nil {
			errInternal(w)
			return
		} else if asg != nil {
			ms, err := h.Store.ListDAGMembershipsByAssignment(ctx, asg.ID)
			if err != nil {
				errInternal(w)
				return
			}
			for _, m := range ms {
				if m.IsActive {
					gid := m.GroupID
					groupID = &gid
					break
				}
			}
		}

		eventNames := make([]string, 0, len(instruments))
		candidateArms := make([]int, 0, len(arms))
		for _, a := range arms {
			candidateArms = append(candidateArms, a.ArmNum)
			for _, e := range eventsByArm[a.ArmNum] {
				eventNames = append(eventNames, e.UniqueEventName)
			}
		}

		status, source, err := h.Data.RunExportRows(ctx, dataapi.ExportSpec{
			ProjectID:      projectID,
			User:           u,
			Surface:        audit.SourceAPI,
			AuditAs:        audit.ODMExport, // REQ-ODM-061: distinguishable from a CSV of the same data
			Arms:           candidateArms,
			Events:         eventNames,
			RestrictEvents: true,
			GroupID:        groupID,
			PairExport: func(eventID int64, armNum int, instrumentID int64) int {
				return lv.PairOrArmDefault(eventID, instrumentID, armNum).ExportRankOf()
			},
			Enc:           "json", // the row layout; the ODM encoding is written from it (§6.1)
			SurrogateKeys: true,   // needed only where the level removed the identifier (§4.1)
			// The audit row is the pipeline's; these are this format's own keys (§9).
			AuditExtra: counts.details,
		})
		if err != nil {
			errInternal(w)
			return
		}
		if status != http.StatusOK {
			switch status {
			case http.StatusForbidden:
				errForbidden(w) // no permitted pair in scope (REQ-AUTH-069), nothing audited (REQ-AUD-004)
			case http.StatusBadRequest:
				errBadRequest(w, "the export request is not valid")
			default:
				errInternal(w)
			}
			return
		}
		rows = source
		defer rows.Close()
	} else {
		// Nothing runs the ladder for a document of no values, so this format writes
		// its own `odm_export` row (REQ-ODM-061) — deferred to after the bytes went
		// out, because a client that vanished mid-write still made a download that
		// belongs in the trail (REQ-ODM-025).
		floor := 0
		for _, r := range ranks {
			if r > 0 && (floor == 0 || r < floor) {
				floor = r // most protective among the items the document declares (§3.9)
			}
		}
		defer func() {
			if h.Audit == nil {
				return
			}
			details := counts.details()
			details["sensitivity"] = authz.ExportName(floor)
			auditCtx := context.WithoutCancel(ctx) // the trail outlives the response
			_ = h.Audit.Insert(auditCtx, audit.Entry{
				EventType: audit.ODMExport,
				Source:    audit.SourceAPI,
				UserID:    u.ID,
				Email:     u.Email,
				ProjectID: projectID,
				Details:   details,
			})
		}()
	}

	schema, err := h.Store.SchemaVersion(ctx)
	if err != nil {
		schema = 0 // the version is an informational envelope attribute (§2)
	}
	schemaVersion := strconv.Itoa(schema)
	now := time.Now().UTC()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+odmFilename(project.ProjectName, now, dialect.selection)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	enc := odmWriter{enc: xml.NewEncoder(w), v13: plan.v13}
	if err := writeODMRoot(enc, plan, now, schemaVersion, dialect); err != nil {
		return // a broken pipe is the client leaving; there is nothing to answer
	}
	if err := plan.writeMetadata(enc); err != nil {
		return
	}
	// A design-only document stops with the metadata: ODM 1.3.2 makes `ClinicalData`
	// optional, so the study definition stands alone rather than carrying an empty
	// element that promises records it does not hold (REQ-ODM-020, REQ-ODM-066).
	if !designOnly {
		if err := h.writeODMClinicalData(enc, plan, rows, counts); err != nil {
			return // truncated on purpose: no synthesized close (REQ-ODM-025, O-2)
		}
	}
	if err := enc.end("ODM"); err != nil {
		return
	}
	_ = enc.enc.Flush()
}

// odmCounts are the numbers an odm_export audit row carries beyond the
// pipeline's own (§9): how many records reached the file, how many ItemData
// elements that was, and which delivered columns the document describes no item
// for. Counted as the file is written, so they describe the bytes that went out;
// read by the audit hook once the row stream has settled.
type odmCounts struct {
	mu      sync.Mutex
	version string
	content string // which of the two documents this was (REQ-ODM-066)
	records int
	items   int
	skipped map[string]bool
}

func newODMCounts(version, content string) *odmCounts {
	return &odmCounts{version: version, content: content, skipped: map[string]bool{}}
}

func (c *odmCounts) addRecord() { c.mu.Lock(); c.records++; c.mu.Unlock() }
func (c *odmCounts) addItem(n int) {
	c.mu.Lock()
	c.items += n
	c.mu.Unlock()
}
func (c *odmCounts) addSkip(name string) {
	c.mu.Lock()
	c.skipped[name] = true
	c.mu.Unlock()
}

// details is the ExportSpec.AuditExtra hook's answer: this format's keys, merged
// into the audit row the pipeline writes.
func (c *odmCounts) details() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"surface":         "api", // the administration surface, as §3.5 spells it
		"odm_version":     c.version,
		"content":         c.content, // project_and_data | project_only (REQ-ODM-066)
		"records":         c.records,
		"items":           c.items,
		"skipped_columns": len(c.skipped),
	}
}

// writeODMRoot opens the document: the declaration and the `<ODM>` element with
// the envelope attributes of §2. The namespace is declared here once, so every
// element below it inherits it, and the XML declaration carries no byte-order
// mark (REQ-EXP-016 keeps that to the browser's CSV download).
func writeODMRoot(w odmWriter, plan *odmPlan, now time.Time, schemaVersion string, dialect odmDialect) error {
	if err := w.enc.EncodeToken(xml.ProcInst{Target: "xml", Inst: []byte(`version="1.0" encoding="UTF-8"`)}); err != nil {
		return err
	}
	return w.start("ODM",
		kv{"xmlns", dialect.namespace},
		kv{"xmlns:clara", claraExtNamespace},
		kv{"FileOID", plan.fileOID(now)},
		kv{"CreationDateTime", now.Format(time.RFC3339)},
		kv{"FileType", "Snapshot"}, // CLARA writes snapshots (REQ-ODM-010, ASM-ODM-1)
		kv{"ODMVersion", dialect.version},
		kv{"SourceSystem", "CLARA"},
		kv{"SourceSystemVersion", schemaVersion}) // the installation's data-format revision
}

// writeODMClinicalData streams `<ClinicalData>` and one `<SubjectData>` per record
// as its rows arrive, closing each when the next record starts — constant memory,
// and a file cut short stays recognizable as one (REQ-ODM-019/025).
func (h *Handler) writeODMClinicalData(w odmWriter, plan *odmPlan, rows *dataapi.RowSource, counts *odmCounts) error {
	enc := w.enc
	start := func(name string, attrs ...kv) error { return enc.EncodeToken(odmStart(name, attrs...)) }
	end := func(name string) error { return enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}}) }

	if err := start("ClinicalData",
		kv{"StudyOID", plan.studyOID},
		kv{"MetaDataVersionOID", plan.mdvOID}); err != nil {
		return err
	}
	var (
		subject, event string
		openFormOID    string // the FormData this stream is inside, when 1.3 wrote one
		openFormInst   int
		haveSubject    bool
		haveEvent      bool
		haveForm       bool
	)
	closeForm := func() error {
		if haveForm {
			haveForm = false
			return end("FormData")
		}
		return nil
	}
	closeEvent := func() error {
		if err := closeForm(); err != nil {
			return err
		}
		if haveEvent {
			haveEvent = false
			return end("StudyEventData")
		}
		return nil
	}
	closeSubject := func() error {
		if err := closeEvent(); err != nil {
			return err
		}
		if haveSubject {
			haveSubject = false
			return end("SubjectData")
		}
		return nil
	}

	for {
		row, ok, err := rows.Next()
		if err != nil {
			_ = closeSubject()
			_ = end("ClinicalData")
			_ = enc.Flush()
			return err
		}
		if !ok {
			break
		}
		key := plan.subjectKey(row)
		if key != subject || !haveSubject {
			if err := closeSubject(); err != nil {
				return err
			}
			counts.addRecord()
			// A surrogate key means the applied level removed the record identifier:
			// it groups one record's rows inside this document and means nothing
			// outside it (REQ-ODM-031, O-5); X-CLARA-ODM-Key says so up front.
			if err := start("SubjectData", kv{"SubjectKey", key}); err != nil {
				return err
			}
			subject, haveSubject = key, true
			haveEvent = false
		}

		eventName := row["redcap_event_name"]
		if eventName == "" {
			eventName = noEventName // a project with no events (see noEventOID)
		}
		if eventName != event || !haveEvent {
			if err := closeEvent(); err != nil {
				return err
			}
			if err := start("StudyEventData", kv{"StudyEventOID", plan.eventOID(eventName)}); err != nil {
				return err
			}
			event, haveEvent = eventName, true
		}

		// One row spans several instruments and matrix groups, so group its columns
		// before writing: each group becomes one ItemGroupData (REQ-ODM-024).
		repeatOf := row["redcap_repeat_instrument"]
		instance := 1
		if n, err := strconv.Atoi(row["redcap_repeat_instance"]); err == nil && n > 0 {
			instance = n
		}
		formInstance := 1 // a form instance exists only for the instrument that repeats
		if repeatOf != "" {
			formInstance = instance
		}
		for _, name := range plan.undescribed(row) {
			counts.addSkip(name) // delivered, and no item in this document describes it (§6.1)
		}
		for _, group := range plan.groupsForRow(row, repeatOf, instance) {
			// In 1.3 a clinical ItemGroupData has no parent but FormData, so open one per
			// (form, instance): an instrument's own group and its matrix groups share it,
			// and a later instance of a repeating instrument opens the next (REQ-ODM-027).
			if plan.v13 && (!haveForm || group.formOID != openFormOID || formInstance != openFormInst) {
				if err := closeForm(); err != nil {
					return err
				}
				fattrs := []kv{{"FormOID", group.formOID}}
				if formInstance > 1 {
					fattrs = append(fattrs, kv{"FormRepeatKey", strconv.Itoa(formInstance)})
				}
				if err := start("FormData", fattrs...); err != nil {
					return err
				}
				openFormOID, openFormInst, haveForm = group.formOID, formInstance, true
			}
			attrs := []kv{{"ItemGroupOID", group.oid}}
			if group.repeatKey > 1 {
				attrs = append(attrs, kv{"ItemGroupRepeatKey", strconv.Itoa(group.repeatKey)})
			}
			if err := start("ItemGroupData", attrs...); err != nil {
				return err
			}
			counts.addItem(len(group.items))
			for _, item := range group.items {
				// An absent value stays absent: no empty ItemData, no null indicator
				// invented for it (REQ-ODM-023).
				if plan.v13 {
					if err := enc.EncodeElement("", odmStart(itemDataName, kv{"ItemOID", item.oid}, kv{"Value", item.value})); err != nil {
						return err
					}
					continue
				}
				// 2.0 declares no `Value` attribute on ItemData: the value is a `Value`
				// child element (REQ-ODM-015).
				if err := start("ItemData", kv{"ItemOID", item.oid}); err != nil {
					return err
				}
				if err := enc.EncodeElement(item.value, odmStart("Value")); err != nil {
					return err
				}
				if err := end("ItemData"); err != nil {
					return err
				}
			}
			if err := end("ItemGroupData"); err != nil {
				return err
			}
		}
		if err := enc.Flush(); err != nil {
			return err
		}
	}
	if err := closeSubject(); err != nil {
		return err
	}
	if err := end("ClinicalData"); err != nil {
		return err
	}
	return enc.Flush()
}

// odmFilename is the download name: <project>_odm_<YYYY-MM-DD>.xml, built with the
// data dictionary's sanitizer so a project name cannot smuggle a path (REQ-ODM-026).
func odmFilename(projectName string, now time.Time, version string) string {
	base := strings.Trim(unsafeFilename.ReplaceAllString(projectName, "_"), "._-")
	if base == "" {
		base = "project"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	versionLabel := ""
	if version == "2.0" {
		versionLabel = "v2.0_"
	}
	return base + "_odm_" + versionLabel + now.Format("2006-01-02") + ".xml"
}

func odmStart(name string, attrs ...kv) xml.StartElement {
	// The element name carries no namespace of its own: the document declares the
	// ODM namespace once on the root and every element below inherits it. Writing
	// the Space per name would make encoding/xml restate the declaration on every
	// element, and it refuses an end tag whose space does not match its start — so
	// names here are plain and foreign prefixes are spelled out ("clara:", "xml:lang").
	el := xml.StartElement{Name: xml.Name{Local: name}}
	for _, a := range attrs {
		an := xml.Name{Local: a.name}
		if strings.HasPrefix(a.name, "{") {
			open := strings.Index(a.name, "}")
			an = xml.Name{Space: a.name[1:open], Local: a.name[open+1:]}
		}
		el.Attr = append(el.Attr, xml.Attr{Name: an, Value: a.value})
	}
	return el
}

// odmDataType maps a field onto an ODM DataType, choices and all (§3.4).
func odmDataType(f db.Field) string {
	switch f.FieldType {
	case "dropdown", "radio", "yesno":
		return "text"
	case "calculated":
		switch f.ValidationType.String {
		case "integer":
			return "integer"
		case "floating point":
			return "float"
		default:
			return "text"
		}
	case "checkbox":
		return "integer" // the derived choice columns carry 1/0 (REQ-EXP-053)
	}
	switch f.ValidationType.String {
	case "integer":
		return "integer"
	case "floating point":
		return "float"
	case "date":
		return "date"
	case "datetime":
		return "datetime"
	default:
		return "text" // notes, plain text, and every identifier-shaped or registry type (§3.4)
	}
}

// odmChoices splits the stored choice grammar (`code$label##code$label`,
// REQ-DB-013) into its pairs, keeping order.
func odmChoices(s string) [][2]string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := make([][2]string, 0, 4)
	for _, pair := range strings.Split(s, "##") {
		code, label, _ := strings.Cut(pair, "$")
		if strings.TrimSpace(code) == "" {
			continue
		}
		out = append(out, [2]string{code, label})
	}
	return out
}
