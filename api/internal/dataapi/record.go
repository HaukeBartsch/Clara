package dataapi

// content=record&action=export|import|delete — API_Endpoints_Design.md
// §3.6–§3.8, with the sensitivity pipeline of Data_Export_Anonymization_
// Design.md §4 and the validation/canonicalization rules of
// Data_Validation_Design.md §2/§4.1. This file holds the pieces the three
// actions share: the project dictionary, the data-access-group scope, and
// the filterLogic resolver over stored values.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// contentRecord dispatches the record actions. A missing action defaults to
// export (the REDCap convention); anything else is a uniform 400.
func (h *Handler) contentRecord(w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	switch strings.ToLower(p.Action) {
	case "", "export":
		h.contentRecordExport(w, r, enc, sub, p)
	case "import":
		h.contentRecordImport(w, r, enc, sub, p)
	case "delete":
		h.contentRecordDelete(w, r, enc, sub, p)
	default:
		writeError(w, enc, http.StatusBadRequest, "Invalid request")
	}
}

// recField is one dictionary entry with its instrument name resolved.
type recField struct {
	db.Field
	Instrument string
}

// projectDict is the export/import view of one project's structure, loaded
// once per call in canonical order (instruments by position, fields by
// instrument then position — ListFields' own order).
type projectDict struct {
	fields      []recField
	byName      map[string]recField
	events      []eventRow // canonical per-arm order (GD-15)
	eventArm    map[string]int
	hasEvents   bool
	identifier  string             // GD-8: first field of the first instrument
	instrEvents map[int64][]string // instrument id -> mapped unique event names
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

func (h *Handler) loadDict(ctx context.Context, projectID int64) (*projectDict, error) {
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
	}
	for _, f := range fields {
		rf := recField{Field: f, Instrument: formOf[f.InstrumentID]}
		d.fields = append(d.fields, rf)
		d.byName[f.FieldName] = rf
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
	for _, e := range events { // canonical order drives the name lists
		for _, pr := range pairs {
			if pr.EventID == e.EventID {
				d.instrEvents[pr.InstrumentID] = append(d.instrEvents[pr.InstrumentID], evName[pr.EventID])
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

// recordValues indexes one record's stored values event -> field -> value.
type recordValues map[string]map[string]string

func valuesIndex(dvs []db.DataValue) recordValues {
	out := recordValues{}
	for _, dv := range dvs {
		if out[dv.UniqueEventName] == nil {
			out[dv.UniqueEventName] = map[string]string{}
		}
		out[dv.UniqueEventName][dv.FieldName] = dv.Value
	}
	return out
}

// eval returns the filterLogic/branching evaluator over these values
// (§7.3): a bare [field] reference (empty Event) falls back to the project's
// first event in canonical order GD-15; choices come from the dictionary.
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
	case f.PersonalInformation:
		return catPersonal
	case f.FieldType == "text":
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
