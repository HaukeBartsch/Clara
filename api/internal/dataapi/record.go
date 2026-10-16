package dataapi

// content=record — API_Endpoints_Design.md §3.6–§3.8, dispatched by the
// presence of `data` (REQ-API-012), with the sensitivity pipeline of
// Data_Export_Anonymization_
// Design.md §4 and the validation/canonicalization rules of
// Data_Validation_Design.md §2/§4.1. This file holds the pieces the three
// actions share: the project dictionary, the data-access-group scope, and
// the filterLogic resolver over stored values.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// contentRecord dispatches the record actions by the request itself
// (REQ-API-012): a call carrying `data` is an import, one without it an
// export — the REDCap convention the recorded callers use, which sends no
// action at all. Deletion stays explicit (`action=delete`); an explicit
// `export`/`import` is accepted and never overrides the data rule. Any
// other action is a uniform 400.
func (h *Handler) contentRecord(w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	switch {
	case p.HasData:
		h.contentRecordImport(w, r, enc, sub, p)
	case strings.EqualFold(p.Action, "delete"):
		h.contentRecordDelete(w, r, enc, sub, p)
	case p.Action == "" || strings.EqualFold(p.Action, "export") || strings.EqualFold(p.Action, "import"):
		h.contentRecordExport(w, r, enc, sub, p)
	default:
		writeError(w, enc, http.StatusBadRequest, "Invalid request")
	}
}

// recField is one dictionary entry with its instrument name resolved. A
// checkbox choice column (REQ-DB-042) is an entry of its own: Parent names the
// checkbox it belongs to and ChoiceLabel is the choice's text; both are empty
// for every stored dictionary row.
type recField struct {
	db.Field
	Instrument  string
	Parent      string
	ChoiceLabel string
}

// isChoiceColumn reports whether f is a checkbox's derived value column rather
// than a stored dictionary row (REQ-DB-042).
func (f recField) isChoiceColumn() bool { return f.Parent != "" }

// isCheckboxParent reports whether f is a checkbox's own dictionary row. It
// carries no value: its values live under its choice columns (REQ-DB-042).
func (f recField) isCheckboxParent() bool { return f.FieldType == "checkbox" && f.Parent == "" }

// dictInstrument is the export view of one instrument: the `<instrument>_complete`
// column needs its name, its position-order place and its survey flag (GD-9 —
// a survey-marked instrument always exports finished).
type dictInstrument struct {
	ID       int64
	Name     string
	IsSurvey bool
}

// projectDict is the export/import view of one project's structure, in
// canonical order (instruments by position, fields by instrument then
// position — ListFields' own order). loadDict builds it at most once per
// structure generation and shares it read-only across calls.
type projectDict struct {
	fields []recField
	// expanded is the dictionary with every checkbox field followed inline by
	// its derived choice columns `base___<code>` (REQ-DB-042) — the order the
	// export columns and import validation walk. byName resolves those names
	// too; fields stays the pure design-order list.
	expanded    []recField
	byName      map[string]recField
	instruments []dictInstrument // position order (drives the _complete columns)
	events      []eventRow       // canonical per-arm order (GD-15)
	eventArm    map[string]int
	hasEvents   bool
	identifier  string             // GD-8: first field of the first instrument
	instrEvents map[int64][]string // instrument id -> mapped unique event names
	// repeating holds the pairs designated repeating (REQ-DB-047), unique event
	// name -> instrument name.
	repeating map[string]map[string]bool
}

// repeats reports whether an instrument is repeating at an event (REQ-DB-047).
func (d *projectDict) repeats(event, instrument string) bool {
	return d.repeating[event][instrument]
}

// hasRepeating reports whether any pair of the project repeats.
func (d *projectDict) hasRepeating() bool {
	return len(d.repeating) > 0
}

// checkboxColumns returns the derived value-column entries a checkbox field
// expands into (REQ-DB-042). Each carries the parent's instrument and
// sensitivity flags; its Choices are the one coding of a checked box
// ("1$<choice label>"), which is what label export and value validation read.
// A stored value is only ever "1", so the parent's validation type has nothing
// to check and is dropped — a box must never fail an email pattern or shift
// like a date.
func checkboxColumns(p recField) []recField {
	if p.FieldType != "checkbox" {
		return nil
	}
	var out []recField
	for _, pair := range strings.Split(p.Choices.String, "##") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "$", 2)
		label := kv[0]
		if len(kv) == 2 {
			label = kv[1]
		}
		sub := p.Field
		sub.FieldName = db.CheckboxSubName(p.FieldName, kv[0])
		sub.FieldType = "checkbox"
		sub.Choices = sql.NullString{String: "1$" + label, Valid: true}
		sub.ValidationType = sql.NullString{}
		sub.ValidationFormat = sql.NullString{}
		sub.ValidationMin = sql.NullString{}
		sub.ValidationMax = sql.NullString{}
		out = append(out, recField{Field: sub, Instrument: p.Instrument, Parent: p.FieldName, ChoiceLabel: label})
	}
	return out
}

// valueFieldsByInstrument maps each instrument id to the names of its
// value-carrying fields — the derived "has data" test of REQ-API-134 runs
// against every stored value, independent of which columns an export keeps.
func (d *projectDict) valueFieldsByInstrument() map[int64]map[string]bool {
	out := make(map[int64]map[string]bool, len(d.instruments))
	for _, f := range d.expanded { // includes checkbox choice columns (REQ-DB-042)
		if f.FieldType == "description" || f.FieldType == "header" || f.FieldType == "transfer" || f.isCheckboxParent() {
			continue
		}
		set := out[f.InstrumentID]
		if set == nil {
			set = map[string]bool{}
			out[f.InstrumentID] = set
		}
		set[f.FieldName] = true
	}
	return out
}

// withChoiceColumns returns names with every checkbox among them followed by
// its choice columns — a checkbox's values are stored under those (REQ-DB-042).
// Other names, known or not, pass through unchanged.
func (d *projectDict) withChoiceColumns(names []string) []string {
	if len(names) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name)
		if f, ok := d.byName[name]; ok && f.isCheckboxParent() {
			for _, c := range checkboxColumns(f) {
				out = append(out, c.FieldName)
			}
		}
	}
	return out
}

// eventIDOf maps a unique event name back to its id. The empty name — how a
// project without events stores its values — has no id, so it resolves to 0 and
// then to its arm default; such a project holds no survey link and no pair grant
// of its own (DEV-DB-14).
func (d *projectDict) eventIDOf(eventName string) int64 {
	if eventName == "" {
		return 0
	}
	for _, e := range d.events {
		if e.UniqueEventName == eventName {
			return e.EventID
		}
	}
	return 0
}

// eventNameOf is eventIDOf in reverse: the unique event name a survey link's
// event id stands for. A submission through a link names no event (§3.10), so
// this is how the link's own event becomes the target of the row. The empty
// answer means the link points at an event this project does not have, which
// its caller refuses rather than storing the row under no event at all.
func (d *projectDict) eventNameOf(eventID int64) string {
	for _, e := range d.events {
		if e.EventID == eventID {
			return e.UniqueEventName
		}
	}
	return ""
}

// pairAccess resolves the grant scope of one stored value: its instrument and
// the event it is stored under, falling back to that event's arm default when the
// pair carries no grant of its own (REQ-AUTH-069). This is where a data row meets
// the unit permissions are held at since GD-2 was revised.
func (d *projectDict) pairAccess(sub *subject, eventName, fieldName string) authz.PairLevels {
	var instrID int64
	if f, ok := d.byName[fieldName]; ok {
		instrID = f.InstrumentID
	}
	return sub.pairAt(d.eventIDOf(eventName), instrID, d.eventArm[eventName])
}

// eventsForInstrument returns the candidate events the instrument is mapped
// to, in canonical order — the wide-layout "present in several events" test.
func (d *projectDict) eventsForInstrument(instrumentID int64, candidates []eventRow) []eventRow {
	mapped := map[string]bool{}
	for _, n := range d.instrEvents[instrumentID] {
		mapped[n] = true
	}
	var out []eventRow
	for _, e := range candidates {
		if mapped[e.UniqueEventName] {
			out = append(out, e)
		}
	}
	return out
}

// loadDict returns the project's structure view, reusing the cached
// dictionary while the store's structure generation is unchanged (see
// dictcache.go). The returned value is shared across requests and must not
// be mutated.
func (h *Handler) loadDict(ctx context.Context, projectID int64) (*projectDict, error) {
	gen := h.Store.StructureGeneration()
	if d, ok := h.dicts.get(projectID, gen); ok {
		return d, nil
	}
	d, err := h.buildDict(ctx, projectID)
	if err != nil {
		return nil, err
	}
	h.dicts.put(projectID, gen, d)
	return d, nil
}

// buildDict reads the dictionary from the store; it is tagged into the
// cache by loadDict with the generation read before this call started.
func (h *Handler) buildDict(ctx context.Context, projectID int64) (*projectDict, error) {
	d := &projectDict{byName: map[string]recField{}, eventArm: map[string]int{}}
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		return nil, err
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		return nil, err
	}
	formOf := map[int64]string{}
	for _, i := range instruments {
		formOf[i.ID] = i.Name
		d.instruments = append(d.instruments, dictInstrument{ID: i.ID, Name: i.Name, IsSurvey: i.IsSurvey})
	}
	for _, f := range fields {
		rf := recField{Field: f, Instrument: formOf[f.InstrumentID]}
		d.fields = append(d.fields, rf)
		d.byName[f.FieldName] = rf
		d.expanded = append(d.expanded, rf)
		// A checkbox's stored values live under its derived choice columns;
		// resolving those names here is what lets import, permissions, audit
		// grouping and branching treat them as ordinary fields (REQ-DB-042).
		for _, sub := range checkboxColumns(rf) {
			if _, taken := d.byName[sub.FieldName]; !taken {
				d.byName[sub.FieldName] = sub
			}
			d.expanded = append(d.expanded, sub)
		}
	}
	if len(d.fields) > 0 {
		d.identifier = d.fields[0].FieldName
	}
	events, err := h.eventRows(ctx, projectID)
	if err != nil {
		return nil, err
	}
	d.events = events
	d.hasEvents = len(events) > 0
	for _, e := range events {
		d.eventArm[e.UniqueEventName] = e.ArmNum
	}
	// Instrument-event mapping as unique event names per instrument, in the
	// canonical order (used by the wide layout and import form checks).
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	evName := map[int64]string{}
	for _, e := range events {
		evName[e.EventID] = e.UniqueEventName
	}
	d.instrEvents = map[int64][]string{}
	d.repeating = map[string]map[string]bool{}
	for _, e := range events { // canonical order drives the name lists
		for _, pr := range pairs {
			if pr.EventID == e.EventID {
				d.instrEvents[pr.InstrumentID] = append(d.instrEvents[pr.InstrumentID], evName[pr.EventID])
				if pr.IsRepeating {
					if d.repeating[e.UniqueEventName] == nil {
						d.repeating[e.UniqueEventName] = map[string]bool{}
					}
					d.repeating[e.UniqueEventName][formOf[pr.InstrumentID]] = true
				}
			}
		}
	}
	return d, nil
}

// activeGroupID is the holder's data-access-group scope: the id of their
// active membership when one exists, nil otherwise — a holder without a
// group receives all records of the project (REQ-AUTH-045). A survey link
// has no member and therefore no group (REQ-AUTH-044): its record is placed
// in no group rather than in someone's.
func (h *Handler) activeGroupID(ctx context.Context, sub *subject) (*int64, error) {
	if sub.isLink() {
		return nil, nil
	}
	ms, err := h.Store.ListDAGMembershipsByAssignment(ctx, sub.Assignment.ID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.IsActive {
			gid := m.GroupID
			return &gid, nil
		}
	}
	return nil, nil
}

// recordValues indexes one record's first-instance values event -> field ->
// value: every form's first instance, including that of a repeating instrument
// (REQ-DB-048). Further instances are indexed apart (recordInstances), so no
// reader of the first instance can pick up a value of another one.
type recordValues map[string]map[string]string

func valuesIndex(dvs []db.DataValue) recordValues {
	out := recordValues{}
	for _, dv := range dvs {
		if dv.Instance() != 1 {
			continue
		}
		if out[dv.UniqueEventName] == nil {
			out[dv.UniqueEventName] = map[string]string{}
		}
		out[dv.UniqueEventName][dv.FieldName] = dv.Value
	}
	return out
}

// instanceKey addresses one further instance (2, 3, …) of a repeating
// instrument at one event (REQ-DB-048).
type instanceKey struct {
	event, instrument string
	instance          int
}

// recordInstances indexes one record's further instances: instanceKey ->
// field -> value.
type recordInstances map[instanceKey]map[string]string

// instancesIndex collects the further instances a reader may see: those of a
// pair designated repeating, each value under its own field's instrument. An
// instance kept from a time the pair repeated stays hidden while it does not
// (REQ-DB-048).
func (d *projectDict) instancesIndex(dvs []db.DataValue) recordInstances {
	out := recordInstances{}
	for _, dv := range dvs {
		n := dv.Instance()
		if n == 1 || !d.repeats(dv.UniqueEventName, dv.RepeatingInstrument) {
			continue
		}
		if f, ok := d.byName[dv.FieldName]; !ok || f.Instrument != dv.RepeatingInstrument {
			continue // a value is stored under its own field's instrument only
		}
		k := instanceKey{event: dv.UniqueEventName, instrument: dv.RepeatingInstrument, instance: n}
		if out[k] == nil {
			out[k] = map[string]string{}
		}
		out[k][dv.FieldName] = dv.Value
	}
	return out
}

// numbers returns the further instances of one (event, instrument) in
// ascending order.
func (ri recordInstances) numbers(event, instrument string) []int {
	var out []int
	for k := range ri {
		if k.event == event && k.instrument == instrument {
			out = append(out, k.instance)
		}
	}
	sort.Ints(out)
	return out
}

// eval returns the filterLogic/branching evaluator over these values
// (§7.3): a bare [field] reference (empty Event) falls back to the project's
// first event in canonical order GD-15; choices come from the dictionary, and
// so does which names are checkbox boxes — an unchecked one reads 0
// (REQ-VAL-044).
func (rv recordValues) eval(d *projectDict) validate.LogicEval {
	firstEvent := ""
	if len(d.events) > 0 {
		firstEvent = d.events[0].UniqueEventName
	}
	return validate.LogicEval{
		Value: func(ref validate.Ref) string {
			if ref.Event == "" {
				return rv[firstEvent][ref.Field]
			}
			return rv[ref.Event][ref.Field]
		},
		Choices: func(ref validate.Ref) string {
			if f, ok := d.byName[ref.Field]; ok {
				return f.Choices.String
			}
			return ""
		},
		IsBox: func(ref validate.Ref) bool {
			f, ok := d.byName[ref.Field]
			return ok && f.isChoiceColumn()
		},
	}
}

// fieldHash is the §5.1 anonymization hash for personal fields:
// HEX(SHA-256(ANON_SALT ':' project_id ':' field_name ':' value)), full
// 64-character digest, deterministic per (project, field, value).
func fieldHash(salt string, projectID int64, fieldName, value string) string {
	sum := sha256.Sum256([]byte(salt + ":" + strconv.FormatInt(projectID, 10) + ":" + fieldName + ":" + value))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// --- canonical date/datetime forms (Data_Validation_Design.md §4.1) ---

var (
	canonicalDateRE = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})(\+[0-9]{2}:[0-9]{2}|-[0-9]{2}:[0-9]{2})?$`)
	canonicalTimeRE = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2}) ([0-9]{2}:[0-9]{2})(?::[0-9]{2})?(\+[0-9]{2}:[0-9]{2}|-[0-9]{2}:[0-9]{2})?$`)
)

// phpToGoLayout converts a REDCap-style format token string (Y m d H i with
// literal separators) into a Go reference layout. Unknown characters pass
// through escaped so they match literally.
func phpToGoLayout(f string) string {
	var b strings.Builder
	for _, c := range f {
		switch c {
		case 'Y':
			b.WriteString("2006")
		case 'm':
			b.WriteString("01")
		case 'd':
			b.WriteString("02")
		case 'H':
			b.WriteString("15")
		case 'i':
			b.WriteString("04")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// canonicalizeDate validates an imported date/datetime value and returns it
// in the §4.1 canonical storage form with the collection offset appended
// (GD-16, REQ-VAL-041). A value already in canonical form keeps its stored
// offset; a bare value gets the offset supplied by offsetFor at the value's
// date. vtype is "date" or "datetime"; format is the field's
// validation_format ("" selects the default Y-m-d / Y-m-d H:i).
func canonicalizeDate(value, vtype, format string, offsetFor func(time.Time) string) (string, bool) {
	if vtype == "date" {
		if m := canonicalDateRE.FindStringSubmatch(value); m != nil {
			t, err := time.Parse("2006-01-02", m[1])
			if err != nil {
				return "", false // calendar-invalid (e.g. 2026-02-30)
			}
			if m[2] != "" {
				return t.Format("2006-01-02") + m[2], true
			}
			return t.Format("2006-01-02") + offsetFor(t), true
		}
	} else {
		if m := canonicalTimeRE.FindStringSubmatch(value); m != nil {
			t, err := time.Parse("2006-01-02 15:04", m[1]+" "+m[2])
			if err != nil {
				return "", false
			}
			if m[3] != "" {
				return t.Format("2006-01-02 15:04") + m[3], true
			}
			return t.Format("2006-01-02 15:04") + offsetFor(t), true
		}
	}
	if format == "" {
		if vtype == "date" {
			format = "Y-m-d"
		} else {
			format = "Y-m-d H:i"
		}
	}
	t, err := time.Parse(phpToGoLayout(format), value)
	if err != nil {
		return "", false
	}
	if vtype == "date" {
		return t.Format("2006-01-02") + offsetFor(t), true
	}
	return t.Format("2006-01-02 15:04") + offsetFor(t), true
}

// collectionOffsetFor resolves the §4.1 collection-offset order: the tz
// parameter (a direct ±HH:MM or an IANA name resolved at the value's date)
// when present and parseable, else APP_TIMEZONE.
func (h *Handler) collectionOffsetFor(tz string) func(time.Time) string {
	zone := h.Cfg.AppTimezone
	if tz != "" {
		if isOffsetLiteral(tz) {
			return func(time.Time) string { return tz }
		}
		if _, err := time.LoadLocation(tz); err == nil {
			zone = tz
		}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return func(t time.Time) string { return offsetString(t.In(loc)) }
}

func isOffsetLiteral(s string) bool {
	return canonicalDateRE.MatchString("2000-01-01"+s) && len(s) == 6
}

// offsetString renders a location's offset for t as ±HH:MM (UTC +00:00).
func offsetString(t time.Time) string {
	_, off := t.Zone()
	return fmt.Sprintf("%+03d:%02d", off/3600, abs(off%3600)/60)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// shiftDatePart shifts the date part of a canonical value by offsetDays,
// preserving the time part and the collection offset (§5.2 rule 3). Values
// that do not parse are returned unchanged.
func shiftDatePart(value string, offsetDays int) string {
	if m := canonicalTimeRE.FindStringSubmatch(value); m != nil {
		t, err := time.Parse("2006-01-02 15:04", m[1]+" "+m[2])
		if err != nil {
			return value
		}
		return t.AddDate(0, 0, offsetDays).Format("2006-01-02 15:04") + m[3]
	}
	if m := canonicalDateRE.FindStringSubmatch(value); m != nil {
		t, err := time.Parse("2006-01-02", m[1])
		if err != nil {
			return value
		}
		return t.AddDate(0, 0, offsetDays).Format("2006-01-02") + m[2]
	}
	return value
}

// isDateBearing reports whether a field's values carry a date part that the
// de-identified pipeline shifts (Data_Export_Anonymization_Design.md §4.1).
func isDateBearing(f db.Field) bool {
	vt := f.ValidationType.String
	return vt == "date" || vt == "datetime"
}

// exportCategory classifies a field once, in the priority order of
// Data_Export_Anonymization_Design.md §4.1 (D-1).
type exportCategory int

const (
	catStructured exportCategory = iota
	catFreeText
	catPersonal
	catDirectID
)

func categorize(f recField, identifier string) exportCategory {
	switch {
	case f.FieldName == identifier || f.DirectIdentifier:
		return catDirectID
	case f.isChoiceColumn():
		// A checkbox choice column inherits its parent's identifier flag, but a
		// stored box says nothing beyond checked/unchecked: it is never hashed
		// and is no free text (REQ-EXP-053).
		return catStructured
	case f.PersonalInformation:
		return catPersonal
	case f.FieldType == "text" || f.FieldType == "notes": // REQ-DB-044
		return catFreeText
	default:
		return catStructured
	}
}

// auditWriter is nil-safe so unit tests without an audit writer keep
// working; the production handler always carries one (httpapi).
func (h *Handler) writeExportAudit(ctx context.Context, e audit.Entry, v audit.RecordView, withView bool) {
	if h.Audit == nil {
		return
	}
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err := h.Audit.InsertTx(ctx, tx, e); err != nil {
		return
	}
	if withView {
		if err := h.Audit.InsertViewTx(ctx, tx, v); err != nil {
			return
		}
	}
	_ = tx.Commit()
}
