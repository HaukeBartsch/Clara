package dataapi

// The audit payloads of the data-change family (Audit_Logging_Design.md §3.2,
// REQ-AUD-009/023). Import and delete write them; the record history
// (API_Endpoints_Design.md §4.16, REQ-API-079/080) reads them back, so the
// shape here is the contract that endpoint parses — action, record, instrument,
// event, and the changed fields with their old and new values.

import (
	"sort"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// fieldChange is one changed field of a data-change entry. A side that holds
// no value is null: old on a create, new on a delete or a clear (§3.2).
type fieldChange struct {
	Field string  `json:"field"`
	Old   *string `json:"old"`
	New   *string `json:"new"`
}

// dataChangeDetails is the §3.2 data-change shape — one entry per
// (record, instrument, event) group of an operation.
type dataChangeDetails struct {
	Action     string        `json:"action"`
	RecordID   string        `json:"record_id"`
	Instrument string        `json:"instrument"`
	Event      string        `json:"event"`
	Fields     []fieldChange `json:"fields"`
}

// calculatedChange is one stored result a recomputation changed: the field,
// where it is stored, and its old and new value.
type calculatedChange struct {
	Field      string
	Instrument string
	Event      string
	Old        string
	New        string
}

// calculatedDetails is the calculated_recomputed payload of §3.2. The stored
// position (instrument, event) rides along so the record history can place the
// result like any other value change (REQ-API-079 names both per field).
type calculatedDetails struct {
	RecordID     string  `json:"record_id"`
	Field        string  `json:"field"`
	Instrument   string  `json:"instrument"`
	Event        string  `json:"event"`
	Old          string  `json:"old"`
	New          string  `json:"new"`
	TriggerField *string `json:"trigger_field"`
	TriggerEvent *string `json:"trigger_event"`
}

func valueOrNull(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// importChangeGroups splits one import row's changes into §3.2 entries, one per
// instrument the changed fields belong to, fields in dictionary order. A row is
// one event; its fields are its form's plus the record identifier (security
// finding F3), so a new record written through a later instrument yields two
// entries — the identifier's instrument and the row's own. A create that
// changed nothing still gets the entry of its own form, so the record's birth
// is on the trail. A flat row (no form_name, REQ-API-031) groups the same way —
// every field already sits on its own instrument.
func importChangeGroups(d *projectDict, action, recordID, form, event string, changes map[string]map[string]string) []dataChangeDetails {
	var out []dataChangeDetails
	at := map[string]int{}
	for _, f := range d.fields {
		c, ok := changes[f.FieldName]
		if !ok {
			continue
		}
		i, seen := at[f.Instrument]
		if !seen {
			i = len(out)
			at[f.Instrument] = i
			out = append(out, dataChangeDetails{Action: action, RecordID: recordID,
				Instrument: f.Instrument, Event: event, Fields: []fieldChange{}})
		}
		old := valueOrNull(c["old"])
		if action == "create" {
			old = nil
		}
		out[i].Fields = append(out[i].Fields, fieldChange{Field: f.FieldName, Old: old, New: valueOrNull(c["new"])})
	}
	if len(out) == 0 {
		// A flat row (REQ-API-031) names no form: the record's birth is then
		// placed on the identifier's instrument (GD-8).
		if form == "" {
			form = d.byName[d.identifier].Instrument
		}
		out = append(out, dataChangeDetails{Action: action, RecordID: recordID,
			Instrument: form, Event: event, Fields: []fieldChange{}})
	}
	return out
}

// deleteChangeGroups renders the values a delete removed as §3.2 entries — one
// per (instrument, event), the deleted value on the old side (REQ-AUD-009).
// A value whose field is no longer in the dictionary keeps an empty instrument
// rather than being dropped from the trail.
func deleteChangeGroups(d *projectDict, recordID string, removed []db.DataValue) []dataChangeDetails {
	type key struct{ instrument, event string }
	var out []dataChangeDetails
	at := map[key]int{}
	for _, dv := range removed {
		k := key{event: dv.UniqueEventName}
		if f, ok := d.byName[dv.FieldName]; ok {
			k.instrument = f.Instrument
		}
		i, seen := at[k]
		if !seen {
			i = len(out)
			at[k] = i
			out = append(out, dataChangeDetails{Action: "delete", RecordID: recordID,
				Instrument: k.instrument, Event: k.event, Fields: []fieldChange{}})
		}
		out[i].Fields = append(out[i].Fields, fieldChange{Field: dv.FieldName, Old: valueOrNull(dv.Value)})
	}
	return out
}

// importTrigger names the change that set one recomputation off (§3.2
// trigger_field / trigger_event): the first reference of the calculation, in
// expression order, whose value this row changed at its event. A result that
// moved without such a change — a stale value corrected by an unrelated import
// — has no trigger, null, as the designer path writes for an expression change.
func importTrigger(calculation, event string, changes map[string]map[string]string) (*string, *string) {
	refs, err := validate.ParseCalcExpression(calculation)
	if err != nil {
		return nil, nil
	}
	for _, ref := range refs {
		if _, changed := changes[ref.Field]; changed && (ref.Event == "" || ref.Event == event) {
			field, ev := ref.Field, event
			return &field, &ev
		}
	}
	return nil, nil
}

// sortedCalculated orders recomputed results by event, then field, so the
// entries of one import land in a stable order.
func sortedCalculated(changes []calculatedChange) []calculatedChange {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Event != changes[j].Event {
			return changes[i].Event < changes[j].Event
		}
		return changes[i].Field < changes[j].Field
	})
	return changes
}
