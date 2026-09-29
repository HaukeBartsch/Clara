package dataapi

// content=record&action=export (API_Endpoints_Design.md §3.6) and the
// administration export GET /api/v1/projects/{id}/export (§4.14) share one
// streaming core: row shape flat (normative) or simplified wide (DEV-API-1),
// the sensitivity pipeline of Data_Export_Anonymization_Design.md §4.2, CSV
// formula neutralization (§3.3), and the export audit event (§8). Callers
// resolve authorization — the applied level, candidate arms/events, DAG
// scope — and hand the result to streamExport as an ExportSpec.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// ExportSpec is one fully-resolved export. The caller has made every
// authorization decision: Level is the applied sensitivity rank (the lowest
// among Arms, D-4) and must be above none; Events/RestrictEvents bound the
// candidate events; GroupID applies the data-access-group scope
// (REQ-API-092). The two surfaces differ only in how they fill this in.
type ExportSpec struct {
	ProjectID  int64
	User       *db.User // acting identity recorded on the audit rows
	Token      string   // token column — the project/link token; "" for the UI surface
	Surface    string   // audit.SourceAPI | audit.SourceUI (Details["surface"], §3.5)
	RecordView bool     // write the audit_record_views row when values returned (§4)

	Level  int      // applied export level rank (> expNone), caller-resolved
	Arms   []int    // candidate arms; exactly one stamps the audit ArmNum
	Events []string // candidate unique event names (see RestrictEvents)
	// RestrictEvents filters the dictionary to Events — an explicitly empty
	// set then exports no rows. Without it, every event is a candidate.
	RestrictEvents bool
	GroupID        *int64 // data-access-group filter; nil = all records

	Records     []string // call parameters recorded in the audit filters (§3.5)
	Fields      []string
	Forms       []string
	FilterLogic string
	// FilterEvents is the events[] call parameter for the audit filters —
	// distinct from Events, which on the administration surface carries the
	// candidate arms' events rather than a filter the caller supplied.
	FilterEvents []string

	RawOrLabel        string // "label" renders choice labels (REQ-EXP-010)
	RawOrLabelHeaders string // raw | label | both (REQ-API-027)
	Type              string // "" flat (normative) | "wide" (§3.6.2)
	Enc               string // "csv" | "json"
	Delimiter         rune   // CSV separator; 0 = comma (REQ-EXP-013)

	// Error renders a failure in the surface's own error shape (the data API
	// speaks §3.2 text, the administration API §4.2 JSON). nil = REDCap text.
	Error func(w http.ResponseWriter, status int, message string)
}

func (s ExportSpec) fail(w http.ResponseWriter, status int, message string) {
	if s.Error != nil {
		s.Error(w, status, message)
		return
	}
	writeError(w, s.Enc, status, message)
}

// RunExport streams an export for another surface (the administration API,
// §4.14). The spec carries the caller-resolved authorization; RunExport only
// renders and audits.
func (h *Handler) RunExport(w http.ResponseWriter, r *http.Request, s ExportSpec) {
	d, err := h.loadDict(r.Context(), s.ProjectID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Internal error")
		return
	}
	h.streamExport(w, r, d, s)
}

func (h *Handler) contentRecordExport(w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	ctx := r.Context()
	d, err := h.loadDict(ctx, sub.Project.ID)
	if err != nil {
		h.storeError(w, enc)
		return
	}

	// events[] filter; the candidate arms of the exported data decide the
	// applied level — the lowest among them (D-4). No events filter means
	// every arm is a candidate.
	wantEvents := toSet(p.Events)
	var events []eventRow
	for _, e := range d.events {
		if wantEvents == nil || wantEvents[e.UniqueEventName] {
			events = append(events, e)
		}
	}
	armSeen := map[int]bool{}
	var arms []int
	for _, e := range events {
		if !armSeen[e.ArmNum] {
			armSeen[e.ArmNum] = true
			arms = append(arms, e.ArmNum)
		}
	}
	level := sub.appliedExportLevel(arms)
	if level <= expNone {
		// export_none answers 403 and writes no audit row (REQ-AUD-004).
		writeError(w, enc, http.StatusForbidden, "Permission denied")
		return
	}

	groupID, err := h.activeGroupID(ctx, sub)
	if err != nil {
		h.storeError(w, enc)
		return
	}

	h.streamExport(w, r, d, ExportSpec{
		ProjectID:         sub.Project.ID,
		User:              sub.User,
		Token:             p.Token,
		Surface:           audit.SourceAPI,
		RecordView:        true,
		Level:             level,
		Arms:              arms,
		Events:            p.Events,
		RestrictEvents:    len(p.Events) > 0,
		GroupID:           groupID,
		Records:           p.Records,
		Fields:            p.Fields,
		Forms:             p.Forms,
		FilterLogic:       p.FilterLogic,
		FilterEvents:      p.Events,
		RawOrLabel:        p.RawOrLabel,
		RawOrLabelHeaders: p.RawOrLabelHeaders,
		Type:              p.Type,
		Enc:               enc,
		Delimiter:         p.Delimiter(),
	})
}

// streamExport renders one export against an already-loaded dictionary.
func (h *Handler) streamExport(w http.ResponseWriter, r *http.Request, d *projectDict, s ExportSpec) {
	ctx := r.Context()
	enc := s.Enc
	if enc == "" {
		enc = "csv"
	}

	// Candidate events: the spec's event names filter the dictionary (the
	// data API's events[] parameter; the administration surface passes its
	// candidate arms' events). An empty unrestricted set is "every event";
	// a restricted empty set exports no rows.
	var events []eventRow
	if s.RestrictEvents {
		wantEvents := toSet(s.Events)
		for _, e := range d.events {
			if wantEvents != nil && wantEvents[e.UniqueEventName] {
				events = append(events, e)
			}
		}
	} else {
		events = d.events
	}

	// Columns: value-carrying fields after the forms/fields filters, then
	// the level's column removals (§4.2 steps 1 and 3 — removed means the
	// column is absent, not empty). A filter naming a removed column is
	// silently absent (REQ-API-092): the filter runs first, the level wins.
	wantForms := toSet(s.Forms)
	wantFields := toSet(s.Fields)
	type column struct {
		f       recField
		cat     exportCategory
		dateBrg bool
	}
	var cols []column
	for _, f := range d.fields {
		if f.FieldType == "description" || f.FieldType == "header" {
			continue // carry no values, never appear (§3.2)
		}
		if wantForms != nil && !wantForms[f.Instrument] {
			continue
		}
		if wantFields != nil && !wantFields[f.FieldName] {
			continue
		}
		cat := categorize(f, d.identifier)
		if s.Level < expFull && cat == catDirectID {
			continue
		}
		if s.Level == expDeIdentified && cat == catFreeText && !f.ExportApproved {
			continue
		}
		cols = append(cols, column{f: f, cat: cat, dateBrg: isDateBearing(f.Field)})
	}

	// Records: visible under the data-access-group rule, intersected with
	// records[] (filters combine — REQ-API-024).
	ids, err := h.Store.ListRecordIDs(ctx, s.ProjectID, s.GroupID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Internal error")
		return
	}
	wantRecords := toSet(s.Records)

	// filterLogic: a malformed expression is a client error, caught before
	// any output starts (REQ-API-025; grammar of Data_Validation_Design.md §7).
	evaluate := func(_ recordValues) bool { return true }
	if s.FilterLogic != "" {
		if err := validate.ValidateBranching(s.FilterLogic, nil); err != nil {
			s.fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		evaluate = func(rv recordValues) bool {
			ok, _ := validate.EvalLogic(s.FilterLogic, rv.eval(d))
			return ok
		}
	}

	labelValues := strings.EqualFold(s.RawOrLabel, "label")
	var labelHeaders, bothHeaders bool
	switch strings.ToLower(s.RawOrLabelHeaders) {
	case "label":
		labelHeaders = true
	case "both":
		// REQ-API-027: "<Field Label> (field_name)".
		bothHeaders = true
	}

	// Header. flat: the record identifier first (GD-8), then the redcap_*
	// columns for projects with events (§3.2), then fields in dictionary
	// order. wide: a field present in one event keeps its bare name; in
	// several events it repeats as <field>_<unique_event_name>.
	wide := strings.EqualFold(s.Type, "wide")
	headerOf := func(c column) string {
		if (labelHeaders || bothHeaders) && c.f.FieldLabel.Valid && c.f.FieldLabel.String != "" {
			if bothHeaders {
				return c.f.FieldLabel.String + " (" + c.f.FieldName + ")"
			}
			return c.f.FieldLabel.String
		}
		return c.f.FieldName
	}
	var header []string
	identifierCol := -1 // index into cols of the surviving identifier field
	for i, c := range cols {
		if c.f.FieldName == d.identifier {
			identifierCol = i
		}
	}
	type wideSlot struct {
		col   int
		event string // "" = bare name (single-event field)
	}
	var slots []wideSlot
	if !wide {
		if identifierCol >= 0 {
			header = append(header, headerOf(cols[identifierCol]))
		}
		if d.hasEvents {
			header = append(header, "redcap_event_name", "redcap_repeat_instrument", "redcap_repeat_instance")
		}
		for i, c := range cols {
			if i == identifierCol {
				continue
			}
			header = append(header, headerOf(c))
			slots = append(slots, wideSlot{col: i})
		}
	} else {
		// Wide (§3.6.2): a field present in exactly one candidate event
		// keeps its bare name; a field present in several repeats as
		// <field>_<unique_event_name>. "Present" is the instrument-event
		// mapping restricted to the candidate events.
		for i, c := range cols {
			fieldEvents := d.eventsForInstrument(c.f.InstrumentID, events)
			if len(fieldEvents) > 1 {
				for _, e := range fieldEvents {
					header = append(header, headerOf(c)+"_"+e.UniqueEventName)
					slots = append(slots, wideSlot{col: i, event: e.UniqueEventName})
				}
			} else {
				// Single-event field: bare name, looked up in that event;
				// an event-less project keeps the "" (scan) lookup.
				ev := ""
				if len(fieldEvents) == 1 {
					ev = fieldEvents[0].UniqueEventName
				}
				header = append(header, headerOf(c))
				slots = append(slots, wideSlot{col: i, event: ev})
			}
		}
	}

	// --- stream ---
	cw := csv.NewWriter(w)
	cw.Comma = s.Delimiter
	if cw.Comma == 0 {
		cw.Comma = ','
	}
	if enc == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusOK)
		_ = cw.Write(header)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("["))
	}

	var (
		returned    []string
		instruments = map[string]bool{}
		firstJSON   = true
		writeRow    func(cells []string)
	)
	if enc == "csv" {
		writeRow = func(cells []string) {
			for i, v := range cells {
				cells[i] = validate.CSVCell(v) // REQ-VAL-032, CSV only
			}
			_ = cw.Write(cells)
			cw.Flush()
		}
	} else {
		writeRow = func(cells []string) {
			if !firstJSON {
				_, _ = w.Write([]byte(","))
			}
			firstJSON = false
			var b strings.Builder
			b.WriteByte('{')
			for i, name := range header {
				if i > 0 {
					b.WriteByte(',')
				}
				k, _ := json.Marshal(name)
				v, _ := json.Marshal(cells[i])
				b.Write(k)
				b.WriteByte(':')
				b.Write(v)
			}
			b.WriteByte('}')
			_, _ = w.Write([]byte(b.String()))
		}
	}

	for _, id := range ids {
		if wantRecords != nil && !wantRecords[id] {
			continue
		}
		dvs, err := h.Store.ListDataValuesByRecord(ctx, s.ProjectID, id)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Internal error") // header may already be out; nothing better to do
			return
		}
		rv := valuesIndex(dvs)
		if !evaluate(rv) {
			continue
		}

		// De-identified date shift needs the record's persisted offset;
		// computed lazily only when a date-bearing column survives.
		var offsetDays int
		offsetReady := false
		if s.Level == expDeIdentified {
			for _, c := range cols {
				if c.dateBrg {
					off, err := db.EnsureAnonOffset(ctx, h.Store, s.ProjectID, id,
						h.Cfg.AnonSalt, h.Cfg.AnonDateShiftMin, h.Cfg.AnonDateShiftMax)
					if err != nil {
						s.fail(w, http.StatusInternalServerError, "Internal error")
						return
					}
					offsetDays, offsetReady = off, true
					break
				}
			}
		}
		cell := func(c column, event string) string {
			v := rv[event][c.f.FieldName]
			if v == "" && event == "" {
				// Bare-name lookups (wide single-event slots, event-less
				// projects) scan the record's events; an event-qualified
				// lookup — every flat row — stays exact.
				for _, byField := range rv {
					if x := byField[c.f.FieldName]; x != "" {
						v = x
						break
					}
				}
			}
			if v == "" {
				return ""
			}
			if c.cat == catPersonal && s.Level == expDeIdentified {
				v = fieldHash(h.Cfg.AnonSalt, s.ProjectID, c.f.FieldName, v)
			}
			if s.Level == expDeIdentified && c.dateBrg && offsetReady {
				v = shiftDatePart(v, offsetDays)
			}
			if labelValues && c.f.Choices.String != "" {
				if l := validate.LabelForCode(c.f.Choices.String, v); l != "" {
					v = l
				}
			}
			return v
		}

		if !wide {
			// flat: one row per (record, event) that holds data (§3.6.2).
			type rowSpec struct{ event string }
			var specs []rowSpec
			if d.hasEvents {
				for _, e := range events {
					if len(rv[e.UniqueEventName]) > 0 {
						specs = append(specs, rowSpec{event: e.UniqueEventName})
					}
				}
			} else if len(dvs) > 0 {
				specs = append(specs, rowSpec{})
			}
			for _, rs := range specs {
				cells := make([]string, 0, len(header))
				if identifierCol >= 0 {
					cells = append(cells, cell(cols[identifierCol], rs.event))
				}
				if d.hasEvents {
					cells = append(cells, rs.event, "", "")
				}
				for _, sl := range slots {
					cells = append(cells, cell(cols[sl.col], rs.event))
				}
				writeRow(cells)
				returned = append(returned, id)
				for _, sl := range slots {
					if cellsValueHasData(cell(cols[sl.col], rs.event)) {
						instruments[cols[sl.col].f.Instrument] = true
					}
				}
			}
		} else if len(dvs) > 0 {
			cells := make([]string, 0, len(header))
			for _, sl := range slots {
				cells = append(cells, cell(cols[sl.col], sl.event))
			}
			writeRow(cells)
			returned = append(returned, id)
			for _, sl := range slots {
				instruments[cols[sl.col].f.Instrument] = true
			}
		}
	}

	if enc == "csv" {
		cw.Flush()
	} else {
		_, _ = w.Write([]byte("]"))
	}

	// Audit (§8): the export event on every successful call; the record-view
	// row is written for data-API exports when values were returned
	// (REQ-AUD-013/015 — the table keys on the presented token, so UI exports
	// carry only the export event). No exported or transformed values are
	// recorded (REQ-AUD-014).
	singleArm := 0
	if len(s.Arms) == 1 {
		singleArm = s.Arms[0]
	}
	surface := "data_api"
	if s.Surface == audit.SourceUI {
		surface = "ui"
	}
	filters := map[string]any{ // omitted filters are empty arrays (§3.5)
		"records":      nonNil(s.Records),
		"fields":       nonNil(s.Fields),
		"forms":        nonNil(s.Forms),
		"events":       nonNil(s.FilterEvents),
		"filter_logic": s.FilterLogic,
	}
	instrList := make([]string, 0, len(instruments))
	for i := range instruments {
		instrList = append(instrList, i)
	}
	sort.Strings(instrList)
	h.writeExportAudit(ctx, audit.Entry{
		EventType: audit.Export,
		Source:    s.Surface,
		UserID:    s.User.ID,
		Email:     s.User.Email,
		Token:     s.Token,
		ProjectID: s.ProjectID,
		ArmNum:    singleArm,
		Details: map[string]any{
			"surface":     surface,
			"sensitivity": exportName(s.Level),
			"filters":     filters,
		},
	}, audit.RecordView{
		UserID:      s.User.ID,
		Email:       s.User.Email,
		Token:       s.Token,
		ProjectID:   s.ProjectID,
		RecordIDs:   returned,
		Instruments: instrList,
	}, s.RecordView && len(returned) > 0)
}

func nonNil(vals []string) []string {
	if vals == nil {
		return []string{}
	}
	return vals
}

// cellsValueHasData reports whether an emitted cell carried a value — the
// record-view row lists instruments actually present in the returned rows.
func cellsValueHasData(v string) bool { return v != "" }

func toSet(vals []string) map[string]bool {
	if len(vals) == 0 {
		return nil
	}
	s := make(map[string]bool, len(vals))
	for _, v := range vals {
		s[v] = true
	}
	return s
}
