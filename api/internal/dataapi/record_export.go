package dataapi

// content=record without `data` — export (API_Endpoints_Design.md §3.6,
// REQ-API-012) and the
// administration export GET /api/v1/projects/{id}/export (§4.14) share one
// streaming core: row shape flat (normative) or simplified wide (DEV-API-1),
// the sensitivity pipeline of Data_Export_Anonymization_Design.md §4.2 applied
// per (instrument, event) pair (§4.3), CSV formula neutralization (§3.3), and
// the export audit event (§8). Callers resolve authorization — what each pair
// may be exported at, candidate arms/events, DAG scope — and hand the result to
// streamExport as an ExportSpec.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// ExportSpec is one fully-resolved export. The caller has made every
// authorization decision it can make without knowing the dictionary: PairExport
// answers what it may export per (instrument, event) pair, Events/RestrictEvents
// bound the candidate events, and GroupID applies the data-access-group scope
// (REQ-API-092). The two surfaces differ only in how they fill this in.
type ExportSpec struct {
	ProjectID  int64
	User       *db.User // acting identity recorded on the audit rows
	Token      string   // token column — the project/link token; "" for the UI surface
	Surface    string   // audit.SourceAPI | audit.SourceUI (Details["surface"], §3.5)
	RecordView bool     // write the audit_record_views row when values returned (§4)

	// PairExport reports the acting caller's export level rank for one
	// (instrument, event) pair — expNone when the pair may contribute nothing.
	// Sensitivity is held per pair and never as one level for the whole file:
	// each column carries its own pair's transformation, so one file can hold
	// export_full values beside hashed ones (REQ-API-026/075,
	// Data_Export_Design.md §4.3). armNum is the event's arm, consulted only
	// where the pair carries no grant of its own (REQ-AUTH-069). nil means the
	// caller may export nothing anywhere, which is a 403 (§4.3).
	PairExport func(eventID int64, armNum int, instrumentID int64) int
	Arms       []int    // candidate arms; exactly one stamps the audit ArmNum
	Events     []string // candidate unique event names (see RestrictEvents)
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
	// Decimal is the decimalCharacter (REQ-EXP-015): the separator written into
	// calculated and floating-point values, in either encoding; 0 keeps the
	// stored form.
	Decimal byte

	// Error renders a failure in the surface's own error shape (the data API
	// speaks §3.2 text, the administration API §4.2 JSON). It receives the status
	// streamExport decided on — 403 when the scope holds no permitted pair, 400
	// for a malformed filterLogic, 500 otherwise — so a surface never turns a
	// refusal into a server error. nil = REDCap text.
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

	// events[] filter; the candidate arms of the exported data bound the scope.
	// Sensitivity is no longer computed from them — every column is rendered at
	// its own pair's level (Data_Export_Design.md §4.3), so the holder travels
	// into the spec as a per-pair answer instead of as one applied rank.
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

	groupID, err := h.activeGroupID(ctx, sub)
	if err != nil {
		h.storeError(w, enc)
		return
	}

	h.streamExport(w, r, d, ExportSpec{
		ProjectID:  sub.Project.ID,
		User:       sub.User,
		Token:      p.Token,
		Surface:    audit.SourceAPI,
		RecordView: true,
		PairExport: func(eventID int64, armNum int, instrumentID int64) int {
			return sub.pairAt(eventID, instrumentID, armNum).ExportRankOf()
		},
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
		Decimal:           p.Decimal(),
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

	// --- columns, and their sensitivity per (instrument, event) pair (§4.3) ---
	// The filters run first (a filter naming a column the level would remove is
	// silently absent — REQ-API-092), then every surviving field is rendered at
	// the level of *its own* pair: one file carries export_full values beside
	// hashed ones, and a pair at export_none contributes nothing while the rest
	// of the request proceeds (REQ-API-026/075).
	type column struct {
		f        recField
		cat      exportCategory
		dateBrg  bool
		pairLess bool // the record identifier: belongs to no pair, follows the floor (GD-8)
		decimal  bool // a number decimalCharacter rewrites: calculated or floating point (REQ-EXP-015)
	}
	wantForms := toSet(s.Forms)
	wantFields := toSet(s.Fields)
	var wanted []column
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
		wanted = append(wanted, column{
			f: f, cat: categorize(f, d.identifier), dateBrg: isDateBearing(f.Field),
			pairLess: f.FieldName == d.identifier,
			decimal:  f.FieldType == "calculated" || f.ValidationType.String == "floating point",
		})
	}

	// mapped reports whether the instrument is mapped to the event: only a
	// mapped pair exists, so only a mapped pair contributes (the mapping_updated
	// rule the dashboard follows too).
	mapped := func(instrID int64, event string) bool {
		for _, n := range d.instrEvents[instrID] {
			if n == event {
				return true
			}
		}
		return false
	}

	// The candidate pairs are every candidate event × every instrument the
	// filters left a value column on, over the mapped combinations only. An
	// event-less project has the single scope event "", which is where its values
	// are stored and what its pair key uses.
	type scopeEvent struct {
		name    string
		eventID int64
		armNum  int
	}
	scope := make([]scopeEvent, 0, len(events)+1)
	if d.hasEvents {
		for _, e := range events {
			scope = append(scope, scopeEvent{name: e.UniqueEventName, eventID: e.EventID, armNum: e.ArmNum})
		}
	} else {
		scope = append(scope, scopeEvent{})
	}
	valueInstrs := map[int64]bool{}
	for _, c := range wanted {
		valueInstrs[c.f.InstrumentID] = true
	}

	pairKey := func(event string, instrID int64) string {
		return event + "|" + strconv.FormatInt(instrID, 10)
	}
	// rankOf answers one pair. floor is the most protective level among the
	// permitted ones — what the audit event records and what every pair-less
	// column follows, so nothing leaves the system less protected than the
	// weakest part of the request (D-4 revised). A pair at export_none is counted
	// as dropped and lowers no level: it is simply not in the file.
	rankOf := map[string]int{}
	// candidatesAt counts the pairs this call could have delivered at each event,
	// so a row whose every pair was refused can be recognised as one (§4.3).
	candidatesAt := map[string]int{}
	floor, permitted, dropped := expFull, 0, 0
	for _, sc := range scope {
		for instrID := range valueInstrs {
			if d.hasEvents && !mapped(instrID, sc.name) {
				continue
			}
			rank := expNone
			if s.PairExport != nil {
				rank = s.PairExport(sc.eventID, sc.armNum, instrID)
			}
			rankOf[pairKey(sc.name, instrID)] = rank
			candidatesAt[sc.name]++
			if rank <= expNone {
				dropped++
				continue
			}
			permitted++
			if rank < floor {
				floor = rank
			}
		}
	}
	if permitted == 0 && dropped > 0 {
		// Pairs in scope and none of them exportable: 403, and a rejection writes
		// nothing to either audit table (REQ-AUD-004).
		s.fail(w, http.StatusForbidden, "Permission denied")
		return
	}
	if permitted == 0 {
		// The filters left no pair at all: nothing to disclose and no level to
		// state, so the value-carrying columns stay out and the floor says so.
		floor = expNone
	}

	// visible[i] lists the events where column i appears. A pair that may not
	// contribute is a missing *slot* in wide layout, where every slot names its
	// event; flat layout has one header for all events, so a column stays for the
	// events that permit it and renders empty for the others — how an absent
	// value already reads (REQ-API-028). A column no candidate event permits is
	// absent from the file entirely.
	var cols []column
	visible := make([]map[string]bool, 0, len(wanted))
	// deliveredAt counts the columns a given event actually contributes: an event
	// that had pairs and delivered none is a row of pair-less columns only, and
	// naming an event the caller may not export from is itself a disclosure. Such a
	// row is left out (§4.3's "nothing less protected than the weakest part").
	deliveredAt := map[string]int{}
	dateShiftNeeded := false
	// pairLessCols indexes the surviving identifier columns in cols; their slots
	// are filled once emptied() can say which events the file names at all.
	var pairLessCols []int
	for _, c := range wanted {
		if c.pairLess {
			// The record identifier belongs to no pair (GD-8), so it follows the
			// floor: an export holding any restricted pair never ships a raw one.
			if floor < expFull {
				continue
			}
			pairLessCols = append(pairLessCols, len(cols))
			visible = append(visible, map[string]bool{})
			cols = append(cols, c)
			continue
		}
		at := map[string]bool{}
		for _, sc := range scope {
			rank, known := rankOf[pairKey(sc.name, c.f.InstrumentID)]
			if !known || rank <= expNone {
				continue // no pair at this event, or one that contributes nothing
			}
			if rank < expFull && c.cat == catDirectID {
				continue // identifier field of this pair (§4.2 step 1)
			}
			if rank == expDeIdentified && c.cat == catFreeText && !c.f.ExportApproved {
				continue // unapproved free text at de-identified strength (§4.2 step 3)
			}
			if c.dateBrg && rank == expDeIdentified {
				dateShiftNeeded = true
			}
			at[sc.name] = true
			deliveredAt[sc.name]++
		}
		if len(at) == 0 {
			continue
		}
		visible = append(visible, at)
		cols = append(cols, c)
	}

	// emptied reports an event whose every candidate pair was refused: there were
	// pairs in scope and none of them may deliver a column, so the only thing left
	// to say about it would be that the event exists (see the flat row loop).
	emptied := func(event string) bool {
		return candidatesAt[event] > 0 && deliveredAt[event] == 0
	}
	// The pair-less slots appear wherever the event itself appears in the file:
	// a wide record_id_<event> column on an emptied event would name it exactly
	// like the suppressed flat row did, and flat rows only exist for events that
	// are not emptied (§4.3).
	for _, i := range pairLessCols {
		for _, sc := range scope {
			if !emptied(sc.name) {
				visible[i][sc.name] = true
			}
		}
	}
	// pairRefused reports whether a mapped pair was refused — the question an
	// <instrument>_complete column asks of its pair (§4.3): the completion state
	// is one of the columns a refused pair contributes nothing of. An unmapped
	// combination has no pair to refuse it, and keeps the derived three-state
	// reading it had before per-pair sensitivity existed.
	pairRefused := func(event string, instrID int64) bool {
		rank, known := rankOf[pairKey(event, instrID)]
		return known && rank <= expNone
	}

	// Completion columns (REQ-API-134): whole-instrument exports — full or
	// forms[]-filtered — end every instrument's block with an
	// `<instrument>_complete` slot; a fields[]-filtered export returns exactly
	// the requested fields and carries none. d.fields — and with it cols — are
	// grouped by instrument in position order, so first appearance is the
	// right place; dictInstrument carries the survey flag (GD-9: always
	// finished).
	withComplete := wantFields == nil
	blockInstrs := make([]dictInstrument, 0, len(d.instruments))
	hasBlock := map[int64]bool{}
	for _, c := range cols {
		hasBlock[c.f.InstrumentID] = true
	}
	for _, instr := range d.instruments {
		if hasBlock[instr.ID] {
			blockInstrs = append(blockInstrs, instr)
		}
	}
	instrValueFields := d.valueFieldsByInstrument()

	// Stored finished assignments (§4.13, REQ-DB-036): the sparse set joined
	// in memory per (record, event, instrument), as on the dashboard — only
	// mapped pairs are visible (§ mapping_updated rule).
	finished := map[string]bool{} // record|event|instrumentID
	evNameOf := map[int64]string{}
	for _, e := range d.events {
		evNameOf[e.EventID] = e.UniqueEventName
	}
	if withComplete && d.hasEvents {
		comps, err := h.Store.ListInstrumentCompletions(ctx, s.ProjectID)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Internal error")
			return
		}
		for _, c := range comps {
			name, known := evNameOf[c.EventID]
			if !known {
				continue // event unknown to the dictionary (deleted mid-read)
			}
			finished[completionKey(c.RecordID, name, c.InstrumentID)] = true
		}
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
	// Compiled once here — the per-record check runs the parsed program
	// instead of re-tokenizing and re-parsing for every record (security
	// finding F5).
	evaluate := func(_ recordValues) bool { return true }
	if s.FilterLogic != "" {
		prog, err := validate.CompileLogic(s.FilterLogic)
		if err != nil {
			s.fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		evaluate = func(rv recordValues) bool { return prog.Eval(rv.eval(d)) }
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
		event string          // "" = bare name (single-event field)
		comp  *dictInstrument // set = an <instrument>_complete slot, col unused
	}
	// cols grouped by instrument, in position order — one pass, and the
	// group walk below emits each instrument's block plus its completion
	// column (REQ-API-134).
	colsOf := map[int64][]int{}
	for i, c := range cols {
		if i == identifierCol {
			continue
		}
		colsOf[c.f.InstrumentID] = append(colsOf[c.f.InstrumentID], i)
	}
	var slots []wideSlot
	if !wide {
		if identifierCol >= 0 {
			header = append(header, headerOf(cols[identifierCol]))
		}
		if d.hasEvents {
			header = append(header, "redcap_event_name", "redcap_repeat_instrument", "redcap_repeat_instance")
		}
		for _, instr := range blockInstrs {
			instr := instr
			for _, i := range colsOf[instr.ID] {
				header = append(header, headerOf(cols[i]))
				slots = append(slots, wideSlot{col: i})
			}
			if withComplete {
				header = append(header, instr.Name+"_complete")
				slots = append(slots, wideSlot{comp: &instr})
			}
		}
	} else {
		// Wide (§3.6.2): a field present in exactly one candidate event
		// keeps its bare name; a field present in several repeats as
		// <field>_<unique_event_name>. "Present" is the instrument-event
		// mapping restricted to the candidate events. The completion column
		// follows the same rule (REQ-API-134).
		for _, instr := range blockInstrs {
			instr := instr
			fieldEvents := d.eventsForInstrument(instr.ID, events)
			for _, i := range colsOf[instr.ID] {
				// Wide layout names a slot for one event, so a pair that may not
				// contribute loses that slot outright — the literal "contributes no
				// columns" of §4.3. The bare-vs-suffixed naming keeps following the
				// mapping rather than the permission, so which events an instrument
				// spans never changes a header a caller parses.
				if len(fieldEvents) > 1 {
					for _, e := range fieldEvents {
						if !visible[i][e.UniqueEventName] {
							continue
						}
						header = append(header, headerOf(cols[i])+"_"+e.UniqueEventName)
						slots = append(slots, wideSlot{col: i, event: e.UniqueEventName})
					}
					continue
				}
				// Single-event field: bare name, looked up in that event; an
				// event-less project keeps the "" (scan) lookup.
				ev := ""
				if len(fieldEvents) == 1 {
					ev = fieldEvents[0].UniqueEventName
				}
				if !visible[i][ev] {
					continue
				}
				header = append(header, headerOf(cols[i]))
				slots = append(slots, wideSlot{col: i, event: ev})
			}
			if !withComplete {
				continue
			}
			if len(fieldEvents) > 1 {
				for _, e := range fieldEvents {
					// The completion state belongs to the pair like any other
					// column: a refused pair loses its slot outright (§4.3).
					if pairRefused(e.UniqueEventName, instr.ID) {
						continue
					}
					header = append(header, instr.Name+"_complete_"+e.UniqueEventName)
					slots = append(slots, wideSlot{comp: &instr, event: e.UniqueEventName})
				}
			} else {
				ev := ""
				if len(fieldEvents) == 1 {
					ev = fieldEvents[0].UniqueEventName
				}
				header = append(header, instr.Name+"_complete")
				slots = append(slots, wideSlot{comp: &instr, event: ev})
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
		// Header cells are neutralized like values: a label header is the
		// designer's text, and may begin with any character (REQ-EXP-013).
		cells := make([]string, len(header))
		for i, name := range header {
			cells[i] = validate.CSVCell(name)
		}
		_ = cw.Write(cells)
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

		// De-identified date shift needs the record's persisted offset. The pass
		// runs only when a visible column is date-bearing at a pair held at
		// de-identified strength, so an export that shifts nothing persists nothing.
		var offsetDays int
		offsetReady := false
		if dateShiftNeeded {
			off, err := db.EnsureAnonOffset(ctx, h.Store, s.ProjectID, id,
				h.Cfg.AnonSalt, h.Cfg.AnonDateShiftMin, h.Cfg.AnonDateShiftMax)
			if err != nil {
				s.fail(w, http.StatusInternalServerError, "Internal error")
				return
			}
			offsetDays, offsetReady = off, true
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
			// The value carries the transformation of its own pair (REQ-API-026):
			// two instruments in one file, one hashing and one not, is the normal
			// case rather than something to reconcile.
			rank, known := rankOf[pairKey(event, c.f.InstrumentID)]
			if !known && c.pairLess {
				rank = floor // the record identifier follows the floor (GD-8)
			}
			if c.cat == catPersonal && rank == expDeIdentified {
				v = fieldHash(h.Cfg.AnonSalt, s.ProjectID, c.f.FieldName, v)
			}
			if rank == expDeIdentified && c.dateBrg && offsetReady {
				v = shiftDatePart(v, offsetDays)
			}
			if labelValues && c.f.Choices.String != "" {
				if l := validate.LabelForCode(c.f.Choices.String, v); l != "" {
					v = l
				}
			}
			// The decimal character is presentation, applied last: a personal
			// field was hashed from its stored value above, so no hash depends
			// on the option, and a hash is no number for WithDecimal to rewrite
			// (REQ-EXP-015).
			if c.decimal {
				v = validate.WithDecimal(v, s.Decimal)
			}
			return v
		}

		// <instrument>_complete (REQ-API-134): the dashboard's three-state
		// derivation rendered as 0/1/2. An exact event stays exact; a bare
		// slot scans the record's events, mirroring cell above.
		completeCell := func(instr dictInstrument, event, id string) string {
			if instr.IsSurvey {
				return "2" // GD-9: completion info is filled in automatically
			}
			fieldsOf := instrValueFields[instr.ID]
			hasValue := func(ev string) bool {
				for name := range fieldsOf {
					if rv[ev][name] != "" {
						return true
					}
				}
				return false
			}
			isFinished := func(ev string) bool {
				return finished[completionKey(id, ev, instr.ID)] && mapped(instr.ID, ev)
			}
			if event != "" {
				if isFinished(event) {
					return "2"
				}
				if hasValue(event) {
					return "1"
				}
				return "0"
			}
			for ev := range rv {
				if isFinished(ev) {
					return "2"
				}
			}
			if hasValue("") {
				return "1"
			}
			for ev := range rv {
				if hasValue(ev) {
					return "1"
				}
			}
			return "0"
		}

		if !wide {
			// flat: one row per (record, event) that holds data (§3.6.2).
			type rowSpec struct{ event string }
			var specs []rowSpec
			if d.hasEvents {
				for _, e := range events {
					// A row is written for an event that holds data and delivers at
					// least one permitted column: pairs in scope that all refused
					// would leave a line naming nothing but the event itself.
					if len(rv[e.UniqueEventName]) > 0 && !emptied(e.UniqueEventName) {
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
					if sl.comp != nil {
						if pairRefused(rs.event, sl.comp.ID) {
							// This row's pair refuses even its completion state; the cell
							// reads as an absent value, like a refused value column.
							cells = append(cells, "")
							continue
						}
						cells = append(cells, completeCell(*sl.comp, rs.event, id))
						continue
					}
					if !visible[sl.col][rs.event] {
						// This row's pair may not contribute, so the cell reads as an
						// absent value (REQ-API-028) while the column stays for the
						// events that do permit it.
						cells = append(cells, "")
						continue
					}
					cells = append(cells, cell(cols[sl.col], rs.event))
				}
				writeRow(cells)
				returned = append(returned, id)
				for _, sl := range slots {
					if sl.comp != nil {
						continue // the record-view list stays value-driven
					}
					if !visible[sl.col][rs.event] {
						continue // nothing was delivered from this pair
					}
					if cellsValueHasData(cell(cols[sl.col], rs.event)) {
						instruments[cols[sl.col].f.Instrument] = true
					}
				}
			}
		} else if len(dvs) > 0 {
			cells := make([]string, 0, len(header))
			for _, sl := range slots {
				if sl.comp != nil {
					cells = append(cells, completeCell(*sl.comp, sl.event, id))
					continue
				}
				cells = append(cells, cell(cols[sl.col], sl.event))
			}
			writeRow(cells)
			returned = append(returned, id)
			for _, sl := range slots {
				if sl.comp != nil {
					continue
				}
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
			"surface": surface,
			// The most protective level among the pairs delivered, plus how many
			// pairs the request held that were dropped for want of a level — never
			// a higher level some individual pair would have permitted (§4.3).
			"sensitivity":   exportName(floor),
			"pairs_dropped": dropped,
			"filters":       filters,
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

// completionKey indexes the stored finished assignments by
// (record, event, instrument) — the join key of the REQ-API-134 derivation.
func completionKey(recordID, event string, instrumentID int64) string {
	return recordID + "\x00" + event + "\x00" + strconv.FormatInt(instrumentID, 10)
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
