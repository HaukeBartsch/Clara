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
	"context"
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
	// AuditAs names the audit event this export writes, defaulting to `export`.
	// The ODM project export names it so a whole-study interchange file is
	// distinguishable in the trail from a CSV of the same data (REQ-ODM-061).
	AuditAs string

	// AuditExtra adds the caller's own detail keys to the export audit row, read
	// just before it is written (Design/ODM_Interchange_Design.md §9): a format
	// that counts what it wrote reports them here, so this stays the one place an
	// export audit entry is assembled. nil adds nothing.
	AuditExtra func() map[string]any

	// RowsSettled is waited on before the audit row is written — RunExportRows
	// sets it to the channel that closes once the consumer of this export's rows
	// has taken them all, so counts reported through AuditExtra describe the file
	// that went out. A canceled request stops the waiting; the row is still
	// written (REQ-ODM-025).
	RowsSettled <-chan struct{}

	// SurrogateKeys stamps each flat JSON row with the reserved member
	// SurrogateKeyColumn, naming the record's position among the records that
	// contributed rows (S1, S2, …). The ODM exporter needs a key per record where
	// the applied level removed the identifier column, and only the pipeline knows
	// where one record's rows end (REQ-ODM-031). Off by default: a tabular export
	// gains nothing from it, and the member is stream metadata rather than a column.
	SurrogateKeys bool

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
	// CheckboxLabel is REDCap's exportCheckboxLabel (REQ-EXP-053): in label mode a
	// checked box exports its choice label and an unchecked one nothing; without
	// it — REDCap's default — the boxes read Checked / Unchecked.
	CheckboxLabel bool
	Type          string // "" flat (normative) | "wide" (§3.6.2)
	Enc           string // "csv" | "json"
	Delimiter     rune   // CSV separator; 0 = comma (REQ-EXP-013)
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
		CheckboxLabel:     strings.EqualFold(p.ExportCheckboxLabel, "true"),
		Type:              p.Type,
		Enc:               enc,
		Delimiter:         p.Delimiter(),
		Decimal:           p.Decimal(),
	})
}

// filterMayRead reports whether every field reference of a compiled filterLogic
// expression names a value this export delivers verbatim. A value the caller may
// not export, or one that leaves only transformed — a stripped identifier, a
// hashed personal field, a shifted date — must not be observable through which
// records the filter keeps: row presence is one bit per guess, and the audit row
// says nothing more than "export" (master-spec finding F6, REQ-EXP-054). The
// bound is the sensitivity question and never the forms[]/fields[] one: a field
// the caller may export but did not request stays filterable, as in REDCap
// (REQ-API-025). A reference to something that carries no values at all — a
// description, header or transfer field, a checkbox's own row — discloses
// nothing and keeps reading blank, as it always has (§3.2).
func filterMayRead(d *projectDict, pairExport func(eventID int64, armNum int, instrumentID int64) int, prog *validate.LogicProgram, floor int) bool {
	// A single-segment reference reads the project's first event (GD-15) — the
	// same fallback Eval applies, so the gate asks about the value it reads.
	firstEvent := ""
	if len(d.events) > 0 {
		firstEvent = d.events[0].UniqueEventName
	}
	for _, r := range prog.Refs() {
		f, known := d.byName[r.Field]
		if !known {
			return false // a name this dictionary does not carry
		}
		if f.FieldName == d.identifier {
			// Pair-less: the record identifier follows the floor (GD-8), so it
			// reads exactly where the file carries raw identifiers.
			if floor < expFull {
				return false
			}
			continue
		}
		switch f.FieldType {
		case "description", "header", "transfer":
			continue // carry no values, so nothing to disclose
		}
		if f.isCheckboxParent() {
			continue // its values live under the choice columns (REQ-DB-042)
		}
		event := r.Event
		if event == "" {
			event = firstEvent
		}
		if d.hasEvents && !d.mapsPair(f.InstrumentID, event) {
			return false // no pair at that event: the value ships nowhere
		}
		rank := expNone
		if pairExport != nil {
			rank = pairExport(d.eventIDOf(event), d.eventArm[event], f.InstrumentID)
		}
		cat := categorize(f, d.identifier)
		switch {
		case rank <= expNone:
			return false // the pair contributes nothing to this file (§4.3)
		case rank < expFull && cat == catDirectID:
			return false // removed below export_full (REQ-EXP-020/021)
		case rank == expDeIdentified && cat == catPersonal:
			return false // hashed in the file; its stored value would leak (REQ-EXP-022)
		case rank == expDeIdentified && cat == catFreeText && !f.ExportApproved:
			return false // removed at de-identified strength (§4.2 step 3, REQ-EXP-023)
		case rank == expDeIdentified && isDateBearing(f.Field):
			return false // shifted in the file; its stored date would leak (REQ-EXP-033)
		}
	}
	return true
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
		// forced marks the record identifier kept although forms[]/fields[] left it
		// out (REQ-API-028): it names the row's record and widens no pair scope.
		forced bool
	}
	wantForms := toSet(s.Forms)
	wantFields := toSet(s.Fields)
	var wanted []column
	// The expanded dictionary puts every checkbox's choice columns right after
	// it; the checkbox row itself carries no value and is replaced by them
	// (REQ-EXP-053).
	for _, f := range d.expanded {
		if f.FieldType == "description" || f.FieldType == "header" || f.FieldType == "transfer" || f.isCheckboxParent() {
			continue // carry no values, never appear (§3.2)
		}
		// fields[] names a choice column directly or through its checkbox, which
		// selects every choice column (REQ-EXP-053).
		filtered := (wantForms != nil && !wantForms[f.Instrument]) ||
			(wantFields != nil && !wantFields[f.FieldName] && !(f.isChoiceColumn() && wantFields[f.Parent]))
		// Every row names its record (REQ-API-028): the identifier survives a filter
		// that leaves it out, without bringing its instrument's pairs into scope.
		if filtered && f.FieldName != d.identifier {
			continue
		}
		wanted = append(wanted, column{
			f: f, cat: categorize(f, d.identifier), dateBrg: isDateBearing(f.Field),
			pairLess: f.FieldName == d.identifier,
			decimal:  f.FieldType == "calculated" || f.ValidationType.String == "floating point",
			forced:   filtered,
		})
	}

	// mapped reports whether the instrument is mapped to the event: only a
	// mapped pair exists, so only a mapped pair contributes (the mapping_updated
	// rule the dashboard follows too).
	mapped := d.mapsPair

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
		if !c.forced {
			valueInstrs[c.f.InstrumentID] = true
		}
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
			// Asked for by name, the identifier is what its own instrument's pair
			// delivers: an export of record_id alone lists every (record, event)
			// instead of finding each event emptied. A forced one widens nothing.
			if !c.forced {
				for _, sc := range scope {
					if rank, known := rankOf[pairKey(sc.name, c.f.InstrumentID)]; known && rank > expNone {
						deliveredAt[sc.name]++
					}
				}
			}
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
		if !c.forced { // a forced identifier opens no block of its instrument
			hasBlock[c.f.InstrumentID] = true
		}
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
	finished := map[string]bool{} // record|event|instrumentID|instance
	// finishedFurther lists, per record|event|instrumentID, the further
	// instances marked finished — an instance that holds no value but is
	// finished still has a row (REQ-API-155).
	finishedFurther := map[string][]int{}
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
			finished[completionKey(c.RecordID, name, c.InstrumentID, c.Instance)] = true
			if c.Instance > 1 {
				finishedFurther[completionKey(c.RecordID, name, c.InstrumentID, 1)] =
					append(finishedFurther[completionKey(c.RecordID, name, c.InstrumentID, 1)], c.Instance)
			}
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
	// finding F5). A reference to a value this response cannot carry verbatim
	// answers the same 400 as a malformed one: one body, so the refusal adds no
	// field enumeration to what the dictionary already lets the caller read
	// (finding F6, REQ-EXP-054).
	evaluate := func(_ recordValues) bool { return true }
	if s.FilterLogic != "" {
		prog, err := validate.CompileLogic(s.FilterLogic)
		if err != nil {
			s.fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		if !filterMayRead(d, s.PairExport, prog, floor) {
			s.fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		evaluate = func(rv recordValues) bool { return prog.Eval(rv.eval(d)) }
	}

	labelValues := strings.EqualFold(s.RawOrLabel, "label")
	// eventCell is the redcap_event_name cell: the unique name, or in label mode
	// the event's name as REDCap writes it — "<event>" in a one-arm project,
	// "<event> (Arm <n>: <arm name>)" otherwise.
	eventCell := func(unique string) string { return unique }
	if labelValues && d.hasEvents {
		arms, err := h.Store.ListArms(ctx, s.ProjectID)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Internal error")
			return
		}
		armName := make(map[int]string, len(arms))
		for _, a := range arms {
			name := a.Name.String
			if name == "" {
				name = "Arm " + strconv.Itoa(a.ArmNum)
			}
			armName[a.ArmNum] = name
		}
		label := make(map[string]string, len(d.events))
		for _, e := range d.events {
			l := e.EventName
			if len(arms) > 1 {
				l += " (Arm " + strconv.Itoa(e.ArmNum) + ": " + armName[e.ArmNum] + ")"
			}
			label[e.UniqueEventName] = l
		}
		eventCell = func(unique string) string {
			if l, ok := label[unique]; ok {
				return l
			}
			return unique
		}
	}
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
			label := c.f.FieldLabel.String
			if c.f.isChoiceColumn() {
				label += ", " + c.f.ChoiceLabel // "<Field Label>, <Choice Label>" (REQ-EXP-053)
			}
			if bothHeaders {
				return label + " (" + c.f.FieldName + ")"
			}
			return label
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
		col      int
		event    string          // "" = bare name (single-event field)
		comp     *dictInstrument // set = an <instrument>_complete slot, col unused
		instance int             // 0 = the first instance; n ≥ 2 = a further one (REQ-API-155)
	}
	// Further instances in the wide layout (REQ-API-155): the header names one
	// slot per instance, so it needs the highest instance number of every
	// repeating (instrument, event) among the records this call may return. One
	// grouped read answers it; values are not read here.
	maxInstance := map[string]int{} // instrument name|event -> highest instance
	if wide && d.hasRepeating() {
		candidates := map[string]bool{}
		for _, id := range ids {
			if wantRecords == nil || wantRecords[id] {
				candidates[id] = true
			}
		}
		rows, err := h.Store.DB.QueryContext(ctx,
			`SELECT record_id, repeating_instrument, unique_event_name, MAX(repeating_instance_number)
			 FROM data WHERE project_id = ? AND repeating_instrument <> ''
			 GROUP BY record_id, repeating_instrument, unique_event_name`, s.ProjectID)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Internal error")
			return
		}
		for rows.Next() {
			var record, instrument, event string
			var n int
			if err := rows.Scan(&record, &instrument, &event, &n); err != nil {
				rows.Close()
				s.fail(w, http.StatusInternalServerError, "Internal error")
				return
			}
			if candidates[record] && d.repeats(event, instrument) && n > maxInstance[instrument+"|"+event] {
				maxInstance[instrument+"|"+event] = n
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			s.fail(w, http.StatusInternalServerError, "Internal error")
			return
		}
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
	// The repeat columns exist where REDCap writes them: in projects with a
	// repeating instrument (REQ-API-155).
	repeatCols := d.hasEvents && d.hasRepeating()
	// instanceCol is the header index of redcap_repeat_instance, which JSON
	// writes as a number like REDCap does (REQ-API-155); -1 = no such column.
	instanceCol := -1
	var slots []wideSlot
	// Both layouts open with the record identifier (GD-8, REQ-API-028).
	if identifierCol >= 0 {
		header = append(header, headerOf(cols[identifierCol]))
	}
	if !wide {
		if d.hasEvents {
			header = append(header, "redcap_event_name")
		}
		if repeatCols {
			header = append(header, "redcap_repeat_instrument", "redcap_repeat_instance")
			instanceCol = len(header) - 1
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
			if withComplete {
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
			// The further instances follow the instrument's own block: per event
			// it repeats at, one block for each instance 2…n, its column names
			// suffixed __<n> (REQ-API-155).
			for _, e := range fieldEvents {
				top := maxInstance[instr.Name+"|"+e.UniqueEventName]
				for n := 2; n <= top; n++ {
					suffix := "__" + strconv.Itoa(n)
					if len(fieldEvents) > 1 {
						suffix = "_" + e.UniqueEventName + suffix
					}
					for _, i := range colsOf[instr.ID] {
						if !visible[i][e.UniqueEventName] {
							continue
						}
						header = append(header, headerOf(cols[i])+suffix)
						slots = append(slots, wideSlot{col: i, event: e.UniqueEventName, instance: n})
					}
					if withComplete && !pairRefused(e.UniqueEventName, instr.ID) {
						header = append(header, instr.Name+"_complete"+suffix)
						slots = append(slots, wideSlot{comp: &instr, event: e.UniqueEventName, instance: n})
					}
				}
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
		subjectTag  string // the current record's surrogate key, when SurrogateKeys is on
		emitted     int    // records that contributed rows, in emission order
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
				b.Write(k)
				b.WriteByte(':')
				if i == instanceCol && cells[i] != "" {
					b.WriteString(cells[i]) // a positive integer the export itself formatted
					continue
				}
				v, _ := json.Marshal(cells[i])
				b.Write(v)
			}
			if s.SurrogateKeys {
				// A reserved member, lifted out of the row by RunExportRows and never
				// a column: it names which record this row belongs to where the
				// applied level removed the identifier column itself. The value is a
				// count in emission order, not an identifier, so nothing personal
				// crosses here (REQ-ODM-031, ODM_Interchange_Design.md §4.1).
				k, _ := json.Marshal(SurrogateKeyColumn)
				t, _ := json.Marshal(subjectTag)
				b.WriteByte(',')
				b.Write(k)
				b.WriteByte(':')
				b.Write(t)
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
		emitted++
		if s.SurrogateKeys {
			subjectTag = surrogateKeyOf(emitted)
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
		ri := d.instancesIndex(dvs)
		// present renders one stored value of column c at event: the
		// transformations of its pair, then presentation (REQ-API-026).
		present := func(c column, event, v string) string {
			if c.f.isChoiceColumn() {
				// A box is checked or not: raw writes 1/0; labels write REDCap's
				// Checked/Unchecked, or with exportCheckboxLabel the choice's text or
				// nothing (REQ-EXP-053). The cell only exists where its pair may
				// contribute, so a 0 never stands in for a refused one.
				switch {
				case !labelValues && v == "":
					return "0"
				case !labelValues:
					return v
				case s.CheckboxLabel && v == "":
					return ""
				case s.CheckboxLabel:
					return c.f.ChoiceLabel
				case v == "":
					return "Unchecked"
				}
				return "Checked"
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
		// cell renders column c of the first instance at event.
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
			return present(c, event, v)
		}

		// <instrument>_complete (REQ-API-134): the dashboard's three-state
		// derivation rendered as 0/1/2. An exact event stays exact; a bare
		// slot scans the record's events, mirroring cell above.
		completeCell := func(instr dictInstrument, event, id string) string {
			if event != "" && d.hasEvents && !mapped(instr.ID, event) {
				// No pair at this event, so no completion state to report — REDCap's
				// empty cell for an instrument not collected there (REQ-API-134).
				return ""
			}
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
				return finished[completionKey(id, ev, instr.ID, 1)] && mapped(instr.ID, ev)
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
		// instanceComplete is the same state for one instance of a repeating
		// instrument at an exact event, read from that instance's values
		// (REQ-API-155).
		instanceComplete := func(instr dictInstrument, event, id string, instance int, values map[string]string) string {
			if finished[completionKey(id, event, instr.ID, instance)] && mapped(instr.ID, event) {
				return "2"
			}
			for name := range instrValueFields[instr.ID] {
				if values[name] != "" {
					return "1"
				}
			}
			return "0"
		}
		// instanceValues is the value map of one instance: the first instance
		// lives with the record's first-instance values (REQ-DB-048).
		instanceValues := func(event, instrument string, instance int) map[string]string {
			if instance <= 1 {
				return rv[event]
			}
			return ri[instanceKey{event: event, instrument: instrument, instance: instance}]
		}
		// instancesOf lists the instances of a repeating instrument at an event
		// that hold a value or are marked finished, ascending (REQ-API-155).
		instancesOf := func(event string, instr dictInstrument) []int {
			var out []int
			first := finished[completionKey(id, event, instr.ID, 1)]
			for name := range instrValueFields[instr.ID] {
				first = first || rv[event][name] != ""
			}
			if first {
				out = append(out, 1)
			}
			seen := map[int]bool{}
			for _, n := range ri.numbers(event, instr.Name) {
				seen[n] = true
			}
			for _, n := range finishedFurther[completionKey(id, event, instr.ID, 1)] {
				seen[n] = true
			}
			further := make([]int, 0, len(seen))
			for n := range seen {
				further = append(further, n)
			}
			sort.Ints(further)
			return append(out, further...)
		}

		if !wide {
			// flat: one row per (record, event) that holds data (§3.6.2) and —
			// REDCap's layout — one row per instance of every instrument that
			// repeats at that event, naming it in redcap_repeat_instrument and
			// redcap_repeat_instance (REQ-API-155). The event's own row leaves the
			// repeating instruments' columns empty; an instance row carries its
			// own instrument's columns only.
			type rowSpec struct {
				event      string
				instrument *dictInstrument // nil = the event's own row
				instance   int
				values     map[string]string // the instance's values (instance rows)
			}
			// ownHolds reports a first-instance value at the event outside the
			// instruments repeating there — the content of the event's own row.
			ownHolds := func(event string) bool {
				for name := range rv[event] {
					if f, ok := d.byName[name]; !ok || !d.repeats(event, f.Instrument) {
						return true
					}
				}
				return false
			}
			var specs []rowSpec
			if d.hasEvents {
				for _, e := range events {
					ev := e.UniqueEventName
					// A row is written for an event that holds data and delivers at
					// least one permitted column: pairs in scope that all refused
					// would leave a line naming nothing but the event itself.
					if emptied(ev) {
						continue
					}
					if ownHolds(ev) {
						specs = append(specs, rowSpec{event: ev})
					}
					for k := range d.instruments {
						instr := &d.instruments[k]
						if !d.repeats(ev, instr.Name) || !hasBlock[instr.ID] || pairRefused(ev, instr.ID) {
							continue
						}
						for _, n := range instancesOf(ev, *instr) {
							specs = append(specs, rowSpec{event: ev, instrument: instr, instance: n,
								values: instanceValues(ev, instr.Name, n)})
						}
					}
				}
			} else if len(dvs) > 0 {
				specs = append(specs, rowSpec{})
			}
			// slotValue renders one slot of one row; ok is false where the row
			// carries nothing of that slot's pair.
			slotValue := func(rs rowSpec, sl wideSlot) (string, bool) {
				instrID, instrName := int64(0), ""
				if sl.comp != nil {
					instrID, instrName = sl.comp.ID, sl.comp.Name
				} else {
					instrID, instrName = cols[sl.col].f.InstrumentID, cols[sl.col].f.Instrument
				}
				if rs.instrument != nil && instrID != rs.instrument.ID {
					return "", false // an instance row carries its own instrument only
				}
				if rs.instrument == nil && d.repeats(rs.event, instrName) {
					return "", false // left to the instrument's instance rows
				}
				if sl.comp != nil {
					if pairRefused(rs.event, sl.comp.ID) {
						// This row's pair refuses even its completion state; the cell
						// reads as an absent value, like a refused value column.
						return "", false
					}
					if rs.instrument != nil {
						return instanceComplete(*sl.comp, rs.event, id, rs.instance, rs.values), true
					}
					return completeCell(*sl.comp, rs.event, id), true
				}
				if !visible[sl.col][rs.event] {
					// This row's pair may not contribute, so the cell reads as an
					// absent value (REQ-API-028) while the column stays for the
					// events that do permit it.
					return "", false
				}
				if rs.instrument != nil {
					return present(cols[sl.col], rs.event, rs.values[cols[sl.col].f.FieldName]), true
				}
				return cell(cols[sl.col], rs.event), true
			}
			for _, rs := range specs {
				cells := make([]string, 0, len(header))
				if identifierCol >= 0 {
					// The identifier is the record's (GD-8): every row names the record
					// it belongs to, whether or not the value is stored at its event.
					cells = append(cells, present(cols[identifierCol], rs.event, id))
				}
				if d.hasEvents {
					cells = append(cells, eventCell(rs.event))
				}
				if repeatCols {
					if rs.instrument != nil {
						cells = append(cells, rs.instrument.Name, strconv.Itoa(rs.instance))
					} else {
						cells = append(cells, "", "")
					}
				}
				for _, sl := range slots {
					v, _ := slotValue(rs, sl)
					cells = append(cells, v)
				}
				writeRow(cells)
				returned = append(returned, id)
				for _, sl := range slots {
					if sl.comp != nil {
						continue // the record-view list stays value-driven
					}
					if v, ok := slotValue(rs, sl); ok && cellsValueHasData(cols[sl.col].f, v) {
						instruments[cols[sl.col].f.Instrument] = true
					}
				}
			}
		} else if len(dvs) > 0 {
			cells := make([]string, 0, len(header))
			if identifierCol >= 0 {
				cells = append(cells, present(cols[identifierCol], "", id))
			}
			for _, sl := range slots {
				if sl.instance > 1 {
					// A further instance's slot (REQ-API-155); one this record does
					// not have reads as an absent value.
					if sl.comp != nil {
						values := instanceValues(sl.event, sl.comp.Name, sl.instance)
						if values == nil && !finished[completionKey(id, sl.event, sl.comp.ID, sl.instance)] {
							cells = append(cells, "")
							continue
						}
						cells = append(cells, instanceComplete(*sl.comp, sl.event, id, sl.instance, values))
						continue
					}
					values := instanceValues(sl.event, cols[sl.col].f.Instrument, sl.instance)
					if values == nil {
						cells = append(cells, "")
						continue
					}
					cells = append(cells, present(cols[sl.col], sl.event, values[cols[sl.col].f.FieldName]))
					continue
				}
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
	// A caller streaming rows through RunExportRows counts what its own writer
	// emitted, and its numbers only mean something once those rows have been
	// taken; the wait is bounded by the request's context, so a client that left
	// mid-download still gets this row (REQ-ODM-025).
	// The trail outlives the response: a client that vanished mid-download still
	// gets its audit row (REQ-ODM-025), and waiting for a row consumer must not
	// be cut short by the request ending.
	auditCtx := context.WithoutCancel(ctx)
	if s.RowsSettled != nil {
		select {
		case <-s.RowsSettled:
		case <-auditCtx.Done():
		}
	}
	details := map[string]any{
		"surface": surface,
		// The most protective level among the pairs delivered, plus how many
		// pairs the request held that were dropped for want of a level — never
		// a higher level some individual pair would have permitted (§4.3).
		"sensitivity":   exportName(floor),
		"pairs_dropped": dropped,
		"filters":       filters,
	}
	if s.AuditExtra != nil {
		for k, v := range s.AuditExtra() {
			details[k] = v // the format's own keys (§9 of the ODM design)
		}
	}
	h.writeExportAudit(auditCtx, audit.Entry{
		EventType: s.auditEvent(),
		Source:    s.Surface,
		UserID:    s.User.ID,
		Email:     s.User.Email,
		Token:     s.Token,
		ProjectID: s.ProjectID,
		ArmNum:    singleArm,
		Details:   details,
	}, audit.RecordView{
		UserID:      s.User.ID,
		Email:       s.User.Email,
		Token:       s.Token,
		ProjectID:   s.ProjectID,
		RecordIDs:   returned,
		Instruments: instrList,
	}, s.RecordView && len(returned) > 0)
}

// SurrogateKeyColumn is the reserved row member carrying a record's surrogate
// key while ExportSpec.SurrogateKeys is on. It describes the stream, not the
// study: a consumer that did not ask for it ignores it, and it never appears as
// a column of a tabular export.
const SurrogateKeyColumn = "_clara_subject"

// surrogateKeyOf names the n-th record that contributed rows (S1, S2, …): a
// position in one document's first-appearance order, meaningless outside the file
// it was written in and never derived from the stored identifier (REQ-ODM-031).
func surrogateKeyOf(n int) string { return "S" + strconv.Itoa(n) }

func nonNil(vals []string) []string {
	if vals == nil {
		return []string{}
	}
	return vals
}

// cellsValueHasData reports whether an emitted cell carried a value — the
// record-view row lists instruments actually present in the returned rows. An
// unchecked box exports as 0 but holds nothing (REQ-DB-042).
func cellsValueHasData(f recField, v string) bool {
	return v != "" && !(f.isChoiceColumn() && v == "0")
}

// completionKey indexes the stored finished assignments by
// (record, event, instrument, instance) — the join key of the REQ-API-134
// derivation; instance is 1 for every form that does not repeat (REQ-DB-048).
func completionKey(recordID, event string, instrumentID int64, instance int) string {
	return recordID + "\x00" + event + "\x00" + strconv.FormatInt(instrumentID, 10) +
		"\x00" + strconv.Itoa(max(instance, 1))
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
