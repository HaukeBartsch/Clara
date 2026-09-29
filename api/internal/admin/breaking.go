package admin

// Breaking-change classification (GD-20, REQ-API-108/111,
// API_Endpoints_Design.md §4.21). The rule: a change is breaking when it would
// make existing recorded data inconsistent or inaccessible; everything else is
// non-breaking.
//
// One function computes the whole diff and classifies every entry, and it is
// the only source of the answer — the staging view that warns the project
// admin, the commit rejection, and the analysis-mode acknowledgement all ask
// the same question, so what the UI warned about is exactly what commit does.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// stagedChange is one entry of the staged diff: what changed, on which object,
// and whether it breaks stored data (the changes[] shape of §4.21).
type stagedChange struct {
	Kind     string `json:"kind"`
	Object   string `json:"object"`
	Breaking bool   `json:"breaking"`
	Reason   string `json:"reason,omitempty"`
}

// valueProbe answers the two questions the classification cannot read off the
// design alone: which values a field holds, and whether an object holds any.
type valueProbe interface {
	// fieldValues returns the distinct stored values of one field name.
	fieldValues(ctx context.Context, fieldName string) ([]string, error)
	// holdsFieldData reports whether any value is stored under these field names.
	holdsFieldData(ctx context.Context, fieldNames []string) (bool, error)
	// holdsEventData reports whether any value is stored at these events.
	holdsEventData(ctx context.Context, uniqueEventNames []string) (bool, error)
	// validationRegistry resolves the regex validation types of §4.2, which a
	// changed validation_type has to be checked against.
	validationRegistry(ctx context.Context) (*validate.Registry, error)
}

// storeValues is the valueProbe backed by the live data table. The classification
// always asks about recorded data, which by definition lives in the active
// design's tables — never in the staged snapshot (REQ-API-107).
type storeValues struct {
	store     *db.Store
	projectID int64
}

func (p storeValues) fieldValues(ctx context.Context, fieldName string) ([]string, error) {
	rows, err := p.store.DB.QueryContext(ctx,
		`SELECT DISTINCT value FROM data WHERE project_id = ? AND field_name = ?`,
		p.projectID, fieldName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p storeValues) holdsFieldData(ctx context.Context, fieldNames []string) (bool, error) {
	return p.anyRow(ctx, `field_name`, fieldNames)
}

func (p storeValues) holdsEventData(ctx context.Context, uniqueEventNames []string) (bool, error) {
	return p.anyRow(ctx, `unique_event_name`, uniqueEventNames)
}

// validationRegistry loads the validation_types registry (§4.2, REQ-VAL-042). A
// field naming a type that has no row must fail the check rather than pass it —
// that is what ValidateValue does with a nil lookup miss, and it is the safe
// direction for a breaking-change decision.
func (p storeValues) validationRegistry(ctx context.Context) (*validate.Registry, error) {
	rows, err := p.store.ListValidationTypes(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]validate.RegistryEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, validate.RegistryEntry{Name: r.Name, Regex: r.Regex, Builtin: r.Builtin})
	}
	return validate.NewRegistry(entries)
}

// anyRow reports whether the data table holds a row for any name in the list.
// An empty list answers false without touching the database.
func (p storeValues) anyRow(ctx context.Context, column string, names []string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	query := `SELECT 1 FROM data WHERE project_id = ? AND ` + column + ` IN (`
	args := []any{p.projectID}
	for i, n := range names {
		if i > 0 {
			query += `,`
		}
		query += `?`
		args = append(args, n)
	}
	query += `) LIMIT 1`
	var one int
	err := p.store.DB.QueryRowContext(ctx, query, args...).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// classifyDesignChange diffs the staged design against the active one and
// classifies every difference per §4.21. Objects are matched by id, which is
// what distinguishes a rename from a delete plus an add.
func classifyDesignChange(
	ctx context.Context, active, staged *stagedDesign, probe valueProbe,
) ([]stagedChange, error) {
	var out []stagedChange

	arms, err := classifyArms(ctx, active, staged, probe)
	if err != nil {
		return nil, err
	}
	out = append(out, arms...)

	events, err := classifyEvents(ctx, active, staged, probe)
	if err != nil {
		return nil, err
	}
	out = append(out, events...)

	instruments, err := classifyInstruments(ctx, active, staged, probe)
	if err != nil {
		return nil, err
	}
	out = append(out, instruments...)

	fields, err := classifyFields(ctx, active, staged, probe)
	if err != nil {
		return nil, err
	}
	out = append(out, fields...)

	mapping, err := classifyMapping(active, staged)
	if err != nil {
		return nil, err
	}
	out = append(out, mapping...)

	sortChangesStable(out)
	return out, nil
}

// --- arms ---

func classifyArms(ctx context.Context, active, staged *stagedDesign, probe valueProbe) ([]stagedChange, error) {
	var out []stagedChange
	stagedByID := map[int64]stagedArm{}
	for _, a := range staged.Arms {
		stagedByID[a.ID] = a
	}
	for _, a := range active.Arms {
		if _, ok := stagedByID[a.ID]; ok {
			continue
		}
		// An arm that still has events or data cannot go (DEV-API-6); when it
		// holds neither the removal is harmless.
		holds, err := probe.holdsEventData(ctx, uniqueNames(a.Events))
		if err != nil {
			return nil, err
		}
		out = append(out, stagedChange{
			Kind: "arm_deleted", Object: armLabel(a.ArmNum), Breaking: holds,
			Reason: breakingIf(holds, "the values recorded in this arm become inaccessible"),
		})
	}
	for _, a := range staged.Arms {
		old, ok := lookupArm(active, a.ID)
		switch {
		case !ok:
			out = append(out, stagedChange{Kind: "arm_added", Object: armLabel(a.ArmNum)})
		case old.Name.String != a.Name.String:
			out = append(out, stagedChange{Kind: "arm_updated", Object: armLabel(a.ArmNum)})
		}
	}
	return out, nil
}

// --- events ---

func classifyEvents(ctx context.Context, active, staged *stagedDesign, probe valueProbe) ([]stagedChange, error) {
	var out []stagedChange
	for _, sa := range staged.Arms {
		aa, armFound := lookupArm(active, sa.ID)
		for _, se := range sa.Events {
			if !armFound {
				out = append(out, stagedChange{Kind: "event_added", Object: se.UniqueEventName})
				continue
			}
			if _, ok := lookupEvent(aa, se.ID); !ok {
				out = append(out, stagedChange{Kind: "event_added", Object: se.UniqueEventName})
			}
		}
		if !armFound {
			continue
		}
		for _, ae := range aa.Events {
			se, ok := lookupEvent(sa, ae.ID)
			if !ok {
				holds, err := probe.holdsEventData(ctx, []string{ae.UniqueEventName})
				if err != nil {
					return nil, err
				}
				out = append(out, stagedChange{
					Kind: "event_deleted", Object: ae.UniqueEventName, Breaking: holds,
					Reason: breakingIf(holds, "the values recorded at this event become inaccessible"),
				})
				continue
			}
			switch {
			case se.UniqueEventName != ae.UniqueEventName:
				// The unique name is the storage key; a rename moves the values
				// with it in the same transaction (ASM-API-4).
				out = append(out, stagedChange{Kind: "event_renamed",
					Object: ae.UniqueEventName + " → " + se.UniqueEventName})
			case se.EventName != ae.EventName || se.Period != ae.Period ||
				se.SafeRegionStart != ae.SafeRegionStart || se.SafeRegionEnd != ae.SafeRegionEnd:
				out = append(out, stagedChange{Kind: "event_updated", Object: se.UniqueEventName})
			case se.Position != ae.Position:
				out = append(out, stagedChange{Kind: "reordered", Object: se.UniqueEventName})
			}
		}
	}
	return out, nil
}

// --- instruments ---

func classifyInstruments(ctx context.Context, active, staged *stagedDesign, probe valueProbe) ([]stagedChange, error) {
	var out []stagedChange
	stagedByID := map[int64]stagedInstrument{}
	for _, i := range staged.Instruments {
		stagedByID[i.ID] = i
	}
	for _, i := range active.Instruments {
		if _, ok := stagedByID[i.ID]; ok {
			continue
		}
		holds, err := probe.holdsFieldData(ctx, fieldNames(i.Fields))
		if err != nil {
			return nil, err
		}
		out = append(out, stagedChange{
			Kind: "instrument_deleted", Object: i.Name, Breaking: holds,
			Reason: breakingIf(holds, "the values recorded in this instrument become inaccessible"),
		})
	}
	for _, i := range staged.Instruments {
		old, ok := lookupInstrument(active, i.ID)
		switch {
		case !ok:
			out = append(out, stagedChange{Kind: "instrument_added", Object: i.Name})
		case old.Name != i.Name:
			out = append(out, stagedChange{Kind: "instrument_renamed", Object: old.Name + " → " + i.Name})
		case old.IsSurvey != i.IsSurvey || old.BranchingLogic != i.BranchingLogic:
			out = append(out, stagedChange{Kind: "instrument_updated", Object: i.Name})
		case old.Position != i.Position:
			out = append(out, stagedChange{Kind: "reordered", Object: i.Name})
		}
	}
	return out, nil
}

// --- fields ---

func classifyFields(ctx context.Context, active, staged *stagedDesign, probe valueProbe) ([]stagedChange, error) {
	var out []stagedChange
	// Fields are addressed instrument.field (the §4.21 example), so both sides
	// need the instrument a field sits on.
	type ref struct{ inst, field string }
	activeFields := flattenFields(active)
	stagedFields := flattenFields(staged)

	for id, af := range activeFields {
		sf, ok := stagedFields[id]
		if !ok {
			// Deleting a field removes its stored values (DEV-API-5), which the
			// table calls breaking without qualification.
			out = append(out, stagedChange{
				Kind: "field_deleted", Object: af.String(), Breaking: true,
				Reason: "deleting a field makes its stored values inaccessible",
			})
			continue
		}
		changes, err := classifyFieldChange(ctx, af.stagedField, sf.stagedField, sf.String(), probe)
		if err != nil {
			return nil, err
		}
		out = append(out, changes...)
	}
	for id, sf := range stagedFields {
		if _, ok := activeFields[id]; !ok {
			out = append(out, stagedChange{Kind: "field_added", Object: sf.String()})
		}
	}
	return out, nil
}

// classifyFieldChange compares one field's two states and returns at most one
// entry per kind, strongest classification first.
func classifyFieldChange(
	ctx context.Context, old, next stagedField, object string, probe valueProbe,
) ([]stagedChange, error) {
	var out []stagedChange

	if next.FieldType != old.FieldType {
		out = append(out, stagedChange{
			Kind: "field_type_changed", Object: object, Breaking: true,
			Reason: "stored values may no longer satisfy the new field type",
		})
	}
	if renamed := next.FieldName != old.FieldName; renamed {
		// The stored values and their inbound references follow the new name in
		// the same transaction (REQ-VAL-014), so they stay accessible.
		out = append(out, stagedChange{Kind: "field_renamed",
			Object: object + " (was " + old.FieldName + ")"})
	}
	if removed, recoded, changed := diffChoices(old, next); changed {
		// A re-coded option always matters — the label is what the stored code
		// meant. A dropped one matters only if a value still uses it.
		var used []string
		if len(removed) > 0 {
			var err error
			used, err = probe.fieldValues(ctx, old.FieldName)
			if err != nil {
				return nil, err
			}
		}
		var choiceChanges []stagedChange
		for _, code := range recoded {
			choiceChanges = append(choiceChanges, stagedChange{
				Kind: "choice_recoded", Object: object + " option " + code, Breaking: true,
				Reason: "stored values using this code lose their label and meaning",
			})
		}
		for _, code := range removed {
			if usesCode(used, code) {
				choiceChanges = append(choiceChanges, stagedChange{
					Kind: "choice_removed", Object: object + " option " + code, Breaking: true,
					Reason: "stored values using this code lose their label and meaning",
				})
			}
		}
		if len(choiceChanges) > 0 {
			out = append(out, choiceChanges...)
		} else {
			// Additions only, or options nobody chose: non-breaking (§4.21).
			out = append(out, stagedChange{Kind: "choices_extended", Object: object})
		}
	}
	if tightened, err := validationTightened(ctx, old, next, object, probe); err != nil {
		return nil, err
	} else if tightened != nil {
		out = append(out, *tightened)
	}

	if len(out) > 0 {
		return out, nil
	}
	switch {
	case attributeChanged(old, next):
		out = append(out, stagedChange{Kind: "field_updated", Object: object})
	case old.Position != next.Position:
		out = append(out, stagedChange{Kind: "reordered", Object: object})
	}
	return out, nil
}

// validationTightened answers "its validation type beyond the current values"
// empirically: run the new field specification over every value already stored
// under the old name, and call it breaking when one of them no longer passes.
func validationTightened(
	ctx context.Context, old, next stagedField, object string, probe valueProbe,
) (*stagedChange, error) {
	if !validationSpecChanged(old, next) {
		return nil, nil
	}
	values, err := probe.fieldValues(ctx, old.FieldName)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		// Nothing recorded yet: no value can fail the new rules.
		return &stagedChange{Kind: "field_updated", Object: object}, nil
	}
	spec := validate.Field{
		Name: next.FieldName, Type: next.FieldType, ValidationType: next.ValidationType.String,
		ValidationMin: next.ValidationMin.String, ValidationMax: next.ValidationMax.String,
		Choices: next.Choices.String,
	}
	reg, err := probe.validationRegistry(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range values {
		if violation := validate.ValidateValue(spec, v, reg); violation != nil {
			return &stagedChange{
				Kind: "validation_tightened", Object: object, Breaking: true,
				Reason: fmt.Sprintf("stored value %q no longer satisfies the new rules (%s)",
					v, violation.Message),
			}, nil
		}
	}
	return &stagedChange{Kind: "field_updated", Object: object}, nil
}

// --- instrument–event mapping ---

// pairKey identifies one mapping pair by the ids it joins, so renaming an
// instrument or an event cannot read as unmapping and remapping everything.
type pairKey struct{ instrumentID, eventID int64 }

func pairSet(d *stagedDesign) map[pairKey]bool {
	out := map[pairKey]bool{}
	for _, i := range d.Instruments {
		for _, a := range d.Arms {
			for _, u := range d.Mapping[a.ArmNum][i.Name] {
				if e, ok := eventInArm(a, u); ok {
					out[pairKey{i.ID, e.ID}] = true
				}
			}
		}
	}
	return out
}

// pairLabel names a pair for a diff entry (instrument @ unique event), resolved
// in the design the entry describes.
func pairLabel(d *stagedDesign, k pairKey) string {
	inst := "?"
	if i, ok := lookupInstrument(d, k.instrumentID); ok {
		inst = i.Name
	}
	for _, a := range d.Arms {
		if e, ok := lookupEvent(a, k.eventID); ok {
			return inst + "@" + e.UniqueEventName
		}
	}
	return inst + "@(unknown event)"
}

func classifyMapping(active, staged *stagedDesign) ([]stagedChange, error) {
	var out []stagedChange
	activePairs := pairSet(active)
	stagedPairs := pairSet(staged)

	for key := range activePairs {
		if _, ok := stagedPairs[key]; !ok {
			// Unmapping is reversible by construction: the values stay in data
			// and become reachable again when the pair is mapped back (§4.12),
			// so this is non-breaking with no warning — even where records do
			// hold values for the pair.
			out = append(out, stagedChange{Kind: "unmapped", Object: pairLabel(active, key)})
		}
	}
	for key := range stagedPairs {
		if _, ok := activePairs[key]; !ok {
			out = append(out, stagedChange{Kind: "mapped", Object: pairLabel(staged, key)})
		}
	}
	return out, nil
}

// --- helpers ---

func choiceBearing(fieldType string) bool {
	switch fieldType {
	case "dropdown", "radio", "matrix":
		return true
	}
	return false
}

// diffChoices compares two states of the code$label##code$label encoding
// (REQ-DB-014) and reports the options that disappeared and the ones whose
// label changed. Adding options yields neither, but does change the string.
func diffChoices(old, next stagedField) (removed, recoded []string, changed bool) {
	if old.Choices.String == next.Choices.String {
		return nil, nil, false
	}
	if !choiceBearing(old.FieldType) || !choiceBearing(next.FieldType) {
		// The encoding appeared or went away with the type; field_type_changed
		// already carries that.
		return nil, nil, false
	}
	oldCodes := parseChoices(old.Choices.String)
	newCodes := parseChoices(next.Choices.String)
	for code, label := range oldCodes {
		nextLabel, ok := newCodes[code]
		if !ok {
			removed = append(removed, code)
			continue
		}
		if nextLabel != label {
			recoded = append(recoded, code)
		}
	}
	sort.Strings(removed)
	sort.Strings(recoded)
	return removed, recoded, true
}

// usesCode reports whether any stored value equals this choice code.
func usesCode(values []string, code string) bool {
	for _, v := range values {
		if v == code {
			return true
		}
	}
	return false
}

// parseChoices decodes the code$label encoding into code → label.
func parseChoices(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, "##") {
		code, label, found := strings.Cut(part, "$")
		if found && code != "" {
			out[code] = label
		}
	}
	return out
}

// validationSpecChanged reports a change to any column the value grammar of
// §4 reads (type is handled separately as field_type_changed).
func validationSpecChanged(old, next stagedField) bool {
	return old.ValidationType != next.ValidationType ||
		old.ValidationFormat != next.ValidationFormat ||
		old.ValidationMin != next.ValidationMin ||
		old.ValidationMax != next.ValidationMax
}

// attributeChanged reports a non-breaking design edit to a field's attributes
// (label, note, headers, branching, required, flags) — the §4.21 row "change a
// field label/description, field note, section header".
func attributeChanged(old, next stagedField) bool {
	return old.FieldLabel != next.FieldLabel ||
		old.SectionHeader != next.SectionHeader ||
		old.FieldNote != next.FieldNote ||
		old.BranchingLogic != next.BranchingLogic ||
		old.MatrixGroup != next.MatrixGroup ||
		old.Required != next.Required ||
		old.PersonalInformation != next.PersonalInformation ||
		old.DirectIdentifier != next.DirectIdentifier ||
		old.ExportApproved != next.ExportApproved ||
		old.Calculation != next.Calculation ||
		old.Choices != next.Choices
}

// fieldAt is one field together with the instrument it sits on, so a diff entry
// can name it the way §4.21 does (instrument.field).
type fieldAt struct {
	stagedField
	instrument string
}

func (f fieldAt) String() string { return f.instrument + "." + f.FieldName }

// flattenFields indexes a design's fields by id.
func flattenFields(d *stagedDesign) map[int64]fieldAt {
	out := map[int64]fieldAt{}
	for _, i := range d.Instruments {
		for _, f := range i.Fields {
			out[f.ID] = fieldAt{stagedField: f, instrument: i.Name}
		}
	}
	return out
}

func lookupArm(d *stagedDesign, id int64) (stagedArm, bool) {
	for _, a := range d.Arms {
		if a.ID == id {
			return a, true
		}
	}
	return stagedArm{}, false
}

func lookupInstrument(d *stagedDesign, id int64) (stagedInstrument, bool) {
	for _, i := range d.Instruments {
		if i.ID == id {
			return i, true
		}
	}
	return stagedInstrument{}, false
}

func lookupEvent(a stagedArm, id int64) (stagedEvent, bool) {
	for _, e := range a.Events {
		if e.ID == id {
			return e, true
		}
	}
	return stagedEvent{}, false
}

func uniqueNames(events []stagedEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.UniqueEventName)
	}
	return out
}

func fieldNames(fields []stagedField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.FieldName)
	}
	return out
}

func armLabel(armNum int) string { return "arm_" + strconv.Itoa(armNum) }

func breakingIf(cond bool, reason string) string {
	if cond {
		return reason
	}
	return ""
}

// sortChangesStable orders the diff by object then kind so that two runs over
// the same pair of designs produce an identical list (the map iteration above
// would otherwise shuffle it).
func sortChangesStable(changes []stagedChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Object != changes[j].Object {
			return changes[i].Object < changes[j].Object
		}
		return changes[i].Kind < changes[j].Kind
	})
}
