package dataapi

// content=record&action=export (API_Endpoints_Design.md §3.6). Row shape is
// flat (normative) or simplified wide (DEV-API-1); the sensitivity pipeline
// is Data_Export_Anonymization_Design.md §4.2; CSV streams with formula
// neutralization (§3.3); every successful call is audited (§8).

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

	// Columns: value-carrying fields after the forms/fields filters, then
	// the level's column removals (§4.2 steps 1 and 3 — removed means the
	// column is absent, not empty). A filter naming a removed column is
	// silently absent (REQ-API-092): the filter runs first, the level wins.
	wantForms := toSet(p.Forms)
	wantFields := toSet(p.Fields)
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
		if level < expFull && cat == catDirectID {
			continue
		}
		if level == expDeIdentified && cat == catFreeText && !f.ExportApproved {
			continue
		}
		cols = append(cols, column{f: f, cat: cat, dateBrg: isDateBearing(f.Field)})
	}

	// Records: visible under the data-access-group rule, intersected with
	// records[] (filters combine — REQ-API-024).
	groupID, err := h.activeGroupID(ctx, sub)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	ids, err := h.Store.ListRecordIDs(ctx, sub.Project.ID, groupID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	wantRecords := toSet(p.Records)

	// filterLogic: a malformed expression is a client error, caught before
	// any output starts (REQ-API-025; grammar of Data_Validation_Design.md §7).
	evaluate := func(_ recordValues) bool { return true }
	if p.FilterLogic != "" {
		if err := validate.ValidateBranching(p.FilterLogic, nil); err != nil {
			writeError(w, enc, http.StatusBadRequest, "Invalid request")
			return
		}
		evaluate = func(rv recordValues) bool {
			ok, _ := validate.EvalLogic(p.FilterLogic, rv.eval(d))
			return ok
		}
	}

	labelValues := strings.EqualFold(p.RawOrLabel, "label")
	labelHeaders := strings.EqualFold(p.RawOrLabelHeaders, "label")
	wide := strings.EqualFold(p.Type, "wide")

	// Header. flat: the record identifier first (GD-8), then the redcap_*
	// columns for projects with events (§3.2), then fields in dictionary
	// order. wide: a field present in one event keeps its bare name; in
	// several events it repeats as <field>_<unique_event_name>.
	headerOf := func(c column) string {
		if labelHeaders && c.f.FieldLabel.Valid && c.f.FieldLabel.String != "" {
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
	cw.Comma = p.Delimiter()
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
		dvs, err := h.Store.ListDataValuesByRecord(ctx, sub.Project.ID, id)
		if err != nil {
			h.storeError(w, enc) // header may already be out; nothing better to do
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
		if level == expDeIdentified {
			for _, c := range cols {
				if c.dateBrg {
					off, err := db.EnsureAnonOffset(ctx, h.Store, sub.Project.ID, id,
						h.Cfg.AnonSalt, h.Cfg.AnonDateShiftMin, h.Cfg.AnonDateShiftMax)
					if err != nil {
						h.storeError(w, enc)
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
			if c.cat == catPersonal && level == expDeIdentified {
				v = fieldHash(h.Cfg.AnonSalt, sub.Project.ID, c.f.FieldName, v)
			}
			if level == expDeIdentified && c.dateBrg && offsetReady {
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
			for _, s := range specs {
				cells := make([]string, 0, len(header))
				if identifierCol >= 0 {
					cells = append(cells, cell(cols[identifierCol], s.event))
				}
				if d.hasEvents {
					cells = append(cells, s.event, "", "")
				}
				for _, sl := range slots {
					cells = append(cells, cell(cols[sl.col], s.event))
				}
				writeRow(cells)
				returned = append(returned, id)
				for _, sl := range slots {
					if cellsValueHasData(cell(cols[sl.col], s.event)) {
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

	// Audit (§8): the export event on every successful call, the record-view
	// row only when values were returned (REQ-AUD-015). No exported or
	// transformed values are recorded (REQ-AUD-014).
	singleArm := 0
	if len(arms) == 1 {
		singleArm = arms[0]
	}
	filters := map[string]any{}
	addFilter(filters, "records", p.Records)
	addFilter(filters, "fields", p.Fields)
	addFilter(filters, "forms", p.Forms)
	addFilter(filters, "events", p.Events)
	if p.FilterLogic != "" {
		filters["filter_logic"] = p.FilterLogic
	}
	instrList := make([]string, 0, len(instruments))
	for i := range instruments {
		instrList = append(instrList, i)
	}
	sort.Strings(instrList)
	h.writeExportAudit(ctx, audit.Entry{
		EventType: audit.Export,
		Source:    audit.SourceAPI,
		UserID:    sub.User.ID,
		Email:     sub.User.Email,
		Token:     p.Token,
		ProjectID: sub.Project.ID,
		ArmNum:    singleArm,
		Details: map[string]any{
			"sensitivity": exportName(level),
			"filters":     filters,
		},
	}, audit.RecordView{
		UserID:      sub.User.ID,
		Email:       sub.User.Email,
		Token:       p.Token,
		ProjectID:   sub.Project.ID,
		RecordIDs:   returned,
		Instruments: instrList,
	}, len(returned) > 0)
}

// cellsValueHasData reports whether an emitted cell carried a value — the
// record-view row lists instruments actually present in the returned rows.
func cellsValueHasData(v string) bool { return v != "" }

func addFilter(m map[string]any, key string, vals []string) {
	if len(vals) > 0 {
		m[key] = vals
	}
}

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
