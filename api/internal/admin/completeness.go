package admin

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// --- GET /api/v1/projects/{id}/record-completeness (§4.13, REQ-API-171) ---

// completenessInstance is one instance of a repeating instrument at one event:
// its number and its count (REQ-API-156 shape).
type completenessInstance struct {
	Instance int `json:"instance"`
	Filled   int `json:"filled"`
	Visible  int `json:"visible"`
}

// completenessInstrument is one cell holding a value: of the instrument's
// fields the branching shows there (visible), how many hold a value (filled).
// For a repeating pair, Filled/Visible are the first instance's and Instances
// lists every instance holding a value, ascending.
type completenessInstrument struct {
	Name      string                 `json:"name"`
	Filled    int                    `json:"filled"`
	Visible   int                    `json:"visible"`
	Instances []completenessInstance `json:"instances,omitempty"`
}

// completenessEvent is one event where the record holds a value: the mapped
// instruments its instrument-level logic hides there, and the cells holding a
// value, in the instrument order of the arm.
type completenessEvent struct {
	UniqueEventName string                   `json:"unique_event_name"`
	Hidden          []string                 `json:"hidden"`
	Instruments     []completenessInstrument `json:"instruments"`
}

// completenessRow is one visible record: its count summed over every cell of
// the listed events, and those events.
type completenessRow struct {
	RecordID string              `json:"record_id"`
	Filled   int                 `json:"filled"`
	Visible  int                 `json:"visible"`
	Events   []completenessEvent `json:"events"`
}

// recordCompleteness returns, for the records and events record-status answers
// for (same access, DAG and arm rules — statusScope), how many of the fields
// branching shows hold a value, per cell and per record (REQ-API-171): the
// input of the dashboard's 3×3 dots. It is a read of its own, so record-status
// keeps its cost (REQ-API-074). Counts only — no value leaves this handler —
// and, like record-status, no audit row (ASM-AUD-2).
func (h *Handler) recordCompleteness(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.loadStatusScope(w, r)
	if !ok {
		return
	}
	out, err := h.completenessOf(r.Context(), sc)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// countedTypes are the field types a person fills in on the form — the web
// layer's DataEntry::ENTERABLE_TYPES. Description, header and transfer fields
// carry no value; a calculated field is filled by the system (REQ-VAL-036).
var countedTypes = map[string]bool{
	"text": true, "notes": true, "dropdown": true, "radio": true,
	"checkbox": true, "yesno": true, "matrix": true,
}

// complDict is the design as the count reads it.
type complDict struct {
	instruments map[int64]*complInstrument
	// slot places a stored value name — a counted field, or one of a counted
	// checkbox's boxes — in its instrument's counted fields.
	slot map[string]complSlot
	// owner maps every stored value name to its field's instrument: the pair a
	// branching reference reads, and whether it is the cell's own instrument
	// (REQ-VAL-045).
	owner map[string]int64
	// choices and boxes are what the evaluator asks of a reference (§7.3): a
	// field's choices, a box's own "1$<label>" as the data API gives it, and
	// which names are boxes — an unchecked one reads 0 (REQ-VAL-044).
	choices map[string]string
	boxes   map[string]bool
	// refs names, sorted, every value column some expression reads.
	refs []string
}

// complInstrument is one instrument: its own branching and its counted
// fields in design order.
type complInstrument struct {
	program *validate.LogicProgram // nil: always shown
	fields  []complField
}

// complField is one counted field's branching. ownRefs marks logic reading
// a field of its own instrument, the one reference that differs from instance
// to instance (REQ-VAL-045); any other logic is evaluated once per event.
type complField struct {
	program *validate.LogicProgram // nil: always shown
	ownRefs bool
}

type complSlot struct {
	instrument int64
	index      int
}

// newComplDict reads the count's view of the design. The counted fields are
// the enterable ones, without the record identifier — the first field of the
// earliest instrument with fields (GD-8), a key rather than an answer — and
// without fields annotated @READONLY, which the form takes no input for
// (REQ-DB-046). An expression that does not compile is left nil, so its field
// or instrument counts as shown, as the form shows it (REQ-VAL-048).
func newComplDict(fields []db.Field, instruments []db.Instrument) *complDict {
	d := &complDict{
		instruments: make(map[int64]*complInstrument, len(instruments)),
		slot:        map[string]complSlot{},
		owner:       make(map[string]int64, len(fields)),
		choices:     make(map[string]string, len(fields)),
		boxes:       map[string]bool{},
	}
	// Every value name first: whether a reference names the field's own
	// instrument is known only once all of them are.
	for _, f := range fields {
		d.owner[f.FieldName] = f.InstrumentID
		d.choices[f.FieldName] = f.Choices.String
		if f.FieldType != "checkbox" {
			continue
		}
		for _, pair := range strings.Split(f.Choices.String, "##") {
			if pair == "" {
				continue
			}
			kv := strings.SplitN(pair, "$", 2)
			label := kv[0]
			if len(kv) == 2 {
				label = kv[1]
			}
			box := db.CheckboxSubName(f.FieldName, kv[0])
			d.owner[box] = f.InstrumentID
			d.choices[box] = "1$" + label
			d.boxes[box] = true
		}
	}

	refs := map[string]bool{}
	compile := func(expr string) *validate.LogicProgram {
		if strings.TrimSpace(expr) == "" {
			return nil
		}
		prog, err := validate.CompileLogic(expr)
		if err != nil {
			return nil // shown, as the form shows it: never hide by accident
		}
		for _, ref := range prog.Refs() {
			refs[ref.Field] = true
		}
		return prog
	}
	for _, in := range instruments {
		d.instruments[in.ID] = &complInstrument{program: compile(in.BranchingLogic.String)}
	}
	for i, f := range fields {
		in, ok := d.instruments[f.InstrumentID]
		if !ok || !countedTypes[f.FieldType] || i == 0 || f.IsReadOnly() {
			continue
		}
		cf := complField{program: compile(f.BranchingLogic.String)}
		if cf.program != nil {
			for _, ref := range cf.program.Refs() {
				if d.owner[ref.Field] == f.InstrumentID {
					cf.ownRefs = true
					break
				}
			}
		}
		sl := complSlot{instrument: f.InstrumentID, index: len(in.fields)}
		in.fields = append(in.fields, cf)
		d.slot[f.FieldName] = sl
		if f.FieldType == "checkbox" {
			for _, code := range validate.ChoiceCodes(f.Choices.String) {
				d.slot[db.CheckboxSubName(f.FieldName, code)] = sl // filled once, by any box
			}
		}
	}
	d.refs = make([]string, 0, len(refs))
	for name := range refs {
		d.refs = append(d.refs, name)
	}
	slices.Sort(d.refs)
	return d
}

// complValueKey names one referenced value: record (its index among the
// visible rows), instance, event and value column.
type complValueKey struct {
	record   int32
	instance int32
	event    string
	field    string
}

// recordEvent is one (record, event) of the visible rows.
type recordEvent struct {
	record int32
	event  int64
}

// completeness holds one request's reads: the counted fields each cell holds
// (a bitset over the instrument's counted fields), the further instances
// holding one, the (record, event) pairs holding one, and the values branching
// reads. No value leaves it (REQ-API-171).
type completeness struct {
	dict     *complDict
	filled   map[statusCell][]uint64
	further  map[statusPair][]int32
	withData map[recordEvent]bool
	values   map[complValueKey]string
}

// completenessOf computes the count for every visible record. Two reads: the
// positions of the project's non-empty values — record-status's scan, kept
// per field here — and, narrowly, the values of the fields some expression
// references (db.Store.ForEachValueOfFields). Expressions compile once per
// request and are evaluated per (record, event holding a value), per instance
// only where they read their own repeating instrument, so the work follows
// the data rather than the mapped pairs (DEV-API-26).
func (h *Handler) completenessOf(ctx context.Context, sc *statusScope) ([]completenessRow, error) {
	fields, err := h.Store.ListFields(ctx, sc.projectID)
	if err != nil {
		return nil, err
	}
	c := &completeness{
		dict:     newComplDict(fields, sc.instruments),
		filled:   map[statusCell][]uint64{},
		further:  map[statusPair][]int32{},
		withData: map[recordEvent]bool{},
		values:   map[complValueKey]string{},
	}
	recordAt := make(map[string]int32, len(sc.visible))
	for i, re := range sc.visible {
		recordAt[re.RecordID] = int32(i)
	}
	nameOf := make(map[int64]string, len(sc.instruments))
	for _, in := range sc.instruments {
		nameOf[in.ID] = in.Name
	}
	if err := c.readPositions(ctx, h.Store, sc, recordAt, nameOf); err != nil {
		return nil, err
	}
	if err := c.readOperands(ctx, h.Store, sc, recordAt, nameOf); err != nil {
		return nil, err
	}
	return c.rows(sc), nil
}

// readPositions marks which counted fields hold a value in each shown cell.
// A value counts where record-status would show it: at a mapped pair, its
// first instance, or a further instance of a pair designated repeating — one
// kept from a time the pair repeated stays hidden (REQ-DB-048). A value of a
// field the count leaves out — the identifier, a calculated or @READONLY
// field — marks nothing, so it alone never makes an event count (REQ-API-171).
func (c *completeness) readPositions(ctx context.Context, store *db.Store, sc *statusScope,
	recordAt map[string]int32, nameOf map[int64]string) error {
	rows, err := store.DB.QueryContext(ctx,
		`SELECT record_id, unique_event_name, field_name, repeating_instrument, repeating_instance_number
		 FROM data WHERE project_id = ? AND value <> ''`, sc.projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	// RawBytes, as in statusMarksOf: the lookups read the bytes in place.
	var record, event, field, repeating sql.RawBytes
	var n int
	for rows.Next() {
		if err := rows.Scan(&record, &event, &field, &repeating, &n); err != nil {
			return err
		}
		at, ok := recordAt[string(record)]
		if !ok {
			continue
		}
		ev, ok := sc.events[string(event)]
		if !ok {
			continue
		}
		sl, ok := c.dict.slot[string(field)]
		if !ok {
			continue
		}
		// The instance as db.DataValue.Instance reads it; a further instance
		// counts only under its own instrument's name (statusMarksOf).
		instance := 1
		if len(repeating) > 0 && n > 1 {
			if string(repeating) != nameOf[sl.instrument] {
				continue
			}
			instance = n
		}
		rep, mapped := sc.pairs[[2]int64{ev.id, sl.instrument}]
		if !mapped || (instance > 1 && !rep) {
			continue
		}
		cell := statusCell{record: at, instance: int32(instance), event: ev.id, instrument: sl.instrument}
		bits := c.filled[cell]
		if bits == nil {
			bits = make([]uint64, (len(c.dict.instruments[sl.instrument].fields)+63)/64)
			c.filled[cell] = bits
			if instance > 1 {
				p := statusPair{record: at, event: ev.id, instrument: sl.instrument}
				c.further[p] = append(c.further[p], int32(instance))
			}
		}
		bits[sl.index/64] |= 1 << (sl.index % 64)
		c.withData[recordEvent{record: at, event: ev.id}] = true
	}
	return rows.Err()
}

// readOperands loads the values branching reads, as the caller may read them:
// a value at an (event, instrument) pair whose data access the caller does not
// hold at read_only or above is left out, so it reads as empty and the count
// discloses nothing of it (REQ-API-171).
func (c *completeness) readOperands(ctx context.Context, store *db.Store, sc *statusScope,
	recordAt map[string]int32, nameOf map[int64]string) error {
	readable := map[[2]int64]bool{} // (event id, instrument id) -> may read, memoized
	return store.ForEachValueOfFields(ctx, sc.projectID, c.dict.refs, func(dv db.DataValue) error {
		at, ok := recordAt[dv.RecordID]
		if !ok {
			return nil
		}
		ev, ok := sc.events[dv.UniqueEventName]
		if !ok {
			return nil
		}
		instrument := c.dict.owner[dv.FieldName]
		instance := dv.Instance()
		if instance > 1 && dv.RepeatingInstrument != nameOf[instrument] {
			return nil // not this instrument's instance
		}
		pair := [2]int64{ev.id, instrument}
		may, seen := readable[pair]
		if !seen {
			may = sc.user.IsAdmin ||
				sc.levels.PairOrArmDefault(ev.id, instrument, ev.armNum).DataRankOf() >= LvlReadOnly
			readable[pair] = may
		}
		if may {
			c.values[complValueKey{record: at, instance: int32(instance), event: dv.UniqueEventName, field: dv.FieldName}] = dv.Value
		}
		return nil
	})
}

// env is the evaluator over one record's values as the form at (event,
// instrument, instance) reads them (REQ-VAL-048): a reference without an event
// names this event (DEV-VAL-13); one to this instrument's fields at this event
// reads this instance, every other the first instances (REQ-VAL-045).
// Instrument 0 with instance 1 is the first-instance reading that
// instrument-level logic uses (§7.3).
func (c *completeness) env(record int32, event string, instrument int64, instance int32) validate.LogicEval {
	return validate.LogicEval{
		Value: func(ref validate.Ref) string {
			ev := ref.Event
			if ev == "" {
				ev = event
			}
			n := int32(1)
			if instance > 1 && ev == event && c.dict.owner[ref.Field] == instrument {
				n = instance
			}
			return c.values[complValueKey{record: record, instance: n, event: ev, field: ref.Field}]
		},
		Choices: func(ref validate.Ref) string { return c.dict.choices[ref.Field] },
		IsBox:   func(ref validate.Ref) bool { return c.dict.boxes[ref.Field] },
	}
}

// shown evaluates which of an instrument's counted fields the form shows.
// With base — the first instance's answer — only logic reading its own
// instrument is evaluated again; the rest cannot differ between instances.
func (in *complInstrument) shown(env validate.LogicEval, base []bool) []bool {
	out := make([]bool, len(in.fields))
	for k, f := range in.fields {
		switch {
		case f.program == nil:
			out[k] = true
		case base != nil && !f.ownRefs:
			out[k] = base[k]
		default:
			out[k] = f.program.Eval(env)
		}
	}
	return out
}

// tally counts a cell: visible fields, and those of them holding a value. A
// field hidden by branching counts toward neither, whatever it stores.
func tally(shown []bool, bits []uint64) (filled, visible int) {
	for k, s := range shown {
		if !s {
			continue
		}
		visible++
		if bits != nil && bits[k/64]&(1<<(k%64)) != 0 {
			filled++
		}
	}
	return filled, visible
}

// rows assembles the response. Per record, only the events of the layout
// where it holds a counted value are listed and summed (REQ-API-171): there
// every mapped instrument counts — hidden ones excepted — its first instance,
// and each further instance of a repeating pair that holds a value; an
// instrument without values adds its visible fields with none filled.
func (c *completeness) rows(sc *statusScope) []completenessRow {
	out := make([]completenessRow, 0, len(sc.visible))
	for i, re := range sc.visible {
		at := int32(i)
		row := completenessRow{RecordID: re.RecordID, Events: []completenessEvent{}}
		for _, ev := range sc.layout {
			if !c.withData[recordEvent{record: at, event: ev.id}] {
				continue // not reached yet: adds nothing
			}
			firsts := c.env(at, ev.name, 0, 1)
			listed := completenessEvent{UniqueEventName: ev.name, Hidden: []string{}, Instruments: []completenessInstrument{}}
			for _, col := range ev.columns {
				in := c.dict.instruments[col.id]
				if in == nil {
					continue
				}
				if in.program != nil && !in.program.Eval(firsts) {
					listed.Hidden = append(listed.Hidden, col.name) // REQ-VAL-040
					continue
				}
				first := statusCell{record: at, instance: 1, event: ev.id, instrument: col.id}
				shown := in.shown(firsts, nil) // the first instance reads first instances
				bits, holds := c.filled[first]
				filled, visible := tally(shown, bits)
				row.Filled += filled
				row.Visible += visible
				entry := completenessInstrument{Name: col.name, Filled: filled, Visible: visible}
				if !col.repeating {
					if holds {
						listed.Instruments = append(listed.Instruments, entry)
					}
					continue
				}
				further := c.further[statusPair{record: at, event: ev.id, instrument: col.id}]
				slices.Sort(further)
				instances := make([]completenessInstance, 0, len(further)+1)
				if holds {
					instances = append(instances, completenessInstance{Instance: 1, Filled: filled, Visible: visible})
				}
				for _, n := range further {
					cell := first
					cell.instance = n
					fn, vn := tally(in.shown(c.env(at, ev.name, col.id, n), shown), c.filled[cell])
					row.Filled += fn
					row.Visible += vn
					instances = append(instances, completenessInstance{Instance: int(n), Filled: fn, Visible: vn})
				}
				if len(instances) > 0 {
					entry.Instances = instances
					listed.Instruments = append(listed.Instruments, entry)
				}
			}
			row.Events = append(row.Events, listed)
		}
		out = append(out, row)
	}
	return out
}
