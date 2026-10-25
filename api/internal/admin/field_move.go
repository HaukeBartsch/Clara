package admin

// Moving a field (REQ-API-157, API_Endpoints_Design.md §4.11): to a place in
// another instrument of the project, or to another place in its own — before a
// named field, or last. The field keeps its id, name, attributes and stored
// values. Values are keyed by field name and event, so a first instance stays
// where it is stored and shows wherever the target instrument is used; what the
// target does not show — an event it is not mapped to, a further instance kept
// under the source instrument's name — stays stored, hidden until the field
// moves back (REQ-DB-049). That makes the move reversible by construction and
// never breaking, like unmapping a pair (§4.21).

import (
	"context"
	"encoding/json"
	"net/http"

	"csms/api/internal/audit"
)

// moveField moves one field: POST …/instruments/{iid}/fields/{fid}/move with
// {"instrument_id": <target>, "before_field_id": <a field of the target> | null}
// (null or absent: last). Both instruments are renumbered 1..n; the record
// identifier must stay where it is while records exist (GD-8, 400). A move to
// the place the field already holds writes nothing. project_admin; production
// stages it like every structure write (diff kind field_moved).
func (h *Handler) moveField(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	instID, ok := pathObjectID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}
	fieldID, ok := pathObjectID(r, "fid")
	if !ok {
		errBadRequest(w, "invalid field id")
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "instrument_id", "before_field_id", "acknowledge_breaking") {
		return
	}
	var body struct {
		InstrumentID  *int64 `json:"instrument_id"`
		BeforeFieldID *int64 `json:"before_field_id"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil || body.InstrumentID == nil {
		errBadRequest(w, "instrument_id is required")
		return
	}
	targetID := *body.InstrumentID
	var beforeID int64
	if body.BeforeFieldID != nil {
		beforeID = *body.BeforeFieldID
	}

	// While a set is open any of the ids may be provisional — an instrument or a
	// field created during staging exists only in the snapshot (REQ-API-107).
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
		if home, found := staged.fieldHome(fieldID); !found || home.ID != instID {
			errNotFound(w)
			return
		}
	}

	apply := func(d *stagedDesign) *designError {
		if home, found := d.fieldHome(fieldID); !found || home.ID != instID {
			return validationf("field %d is not part of instrument %d", fieldID, instID)
		}
		target, found := d.instrument(targetID)
		if !found {
			return validationf("unknown instrument id: %d", targetID)
		}
		if beforeID == fieldID {
			return validationf("before_field_id names the field being moved")
		}
		if beforeID != 0 {
			if home, found := d.fieldHome(beforeID); !found || home.ID != targetID {
				return validationf("before_field_id %d is not a field of instrument %s", beforeID, target.Name)
			}
		}
		d.moveField(fieldID, targetID, beforeID)
		return nil
	}
	dest, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if dest == writeStaged {
		// GD-8 is about stored records, so the live check applies to the staged
		// design unchanged.
		if bad, err := h.moveChangesIdentifier(ctx, projectID, set, apply); err != nil {
			errInternal(w)
			return
		} else if bad {
			errBadRequest(w, "the move would change the record identifier field (GD-8)")
			return
		}
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		moved, _ := set.field(fieldID)
		writeJSON(w, http.StatusOK, newFieldObject(moved.field(projectID)))
		return
	}

	// The live design as a snapshot runs the same checks the staged path does
	// and yields both instruments' final order.
	live, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	source, found := live.fieldHome(fieldID)
	if !found || source.ID != instID {
		errNotFound(w)
		return
	}
	if bad, err := h.moveChangesIdentifier(ctx, projectID, live, apply); err != nil {
		errInternal(w)
		return
	} else if bad {
		errBadRequest(w, "the move would change the record identifier field (GD-8)")
		return
	}
	sourceName := source.Name
	order := fieldIDs(source.Fields)
	if derr := apply(live); derr != nil {
		writeDesignError(w, derr)
		return
	}
	target, _ := live.fieldHome(fieldID)
	moved, _ := live.field(fieldID)
	if target.ID == instID && equalIDs(order, fieldIDs(target.Fields)) {
		writeJSON(w, http.StatusOK, newFieldObject(moved.field(projectID))) // already there
		return
	}
	instruments := []*stagedInstrument{target}
	if source.ID != target.ID {
		instruments = append(instruments, source)
	}
	recompute := source.ID != target.ID &&
		(len(live.repeatingEvents(sourceName)) > 0 || len(live.repeatingEvents(target.Name)) > 0)
	var design *designCtx
	if recompute {
		if design, err = h.loadDesign(ctx, projectID); err != nil {
			errInternal(w)
			return
		}
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	// Two-phase write: fields carry UNIQUE (project_id, instrument_id,
	// position), so every field of both instruments parks negative first.
	parked := 0
	for _, inst := range instruments {
		for _, f := range inst.Fields {
			parked++
			if _, err := tx.ExecContext(ctx, `UPDATE fields SET position = ? WHERE id = ?`, -parked, f.ID); err != nil {
				errInternal(w)
				return
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fields SET instrument_id = ? WHERE id = ?`, target.ID, fieldID); err != nil {
		errInternal(w)
		return
	}
	for _, inst := range instruments {
		for _, f := range inst.Fields {
			if _, err := tx.ExecContext(ctx, `UPDATE fields SET position = ? WHERE id = ?`, f.Position, f.ID); err != nil {
				errInternal(w)
				return
			}
		}
	}
	if recompute {
		// Inside a repeating instrument a calculation reads its own instrument's
		// fields from its own instance and every other field from the first
		// (REQ-VAL-045): moving a field across that boundary changes what the
		// moved calculation and those reading the moved field compute for a
		// further instance, so those are recomputed (REQ-VAL-037). A first
		// instance reads first instances either way and keeps its value.
		names := storedNames(moved.FieldName, moved.FieldType, moved.Choices)
		for _, inst := range instruments {
			for _, f := range inst.Fields {
				if f.FieldType != "calculated" || f.ID != fieldID && !exprNamesAny(f.Calculation.String, names) {
					continue
				}
				if err := h.recomputeCalc(ctx, tx, u, projectID, inst.Name, f.field(projectID), design.isBox, false); err != nil {
					errInternal(w)
					return
				}
			}
		}
	}
	details := map[string]any{
		"field": moved.FieldName, "from_instrument": sourceName, "to_instrument": target.Name,
		"position": moved.Position, "before": nil,
	}
	if beforeID != 0 {
		if before, found := live.field(beforeID); found {
			details["before"] = before.FieldName
		}
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.FieldMoved, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(details, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, newFieldObject(moved.field(projectID)))
}

// moveChangesIdentifier applies the move to a copy of d and reports whether it
// would put another field in the record identifier's place — the first field
// of the earliest instrument with fields — while records exist (GD-8). A move the apply itself
// refuses reports false; the caller's apply answers it.
func (h *Handler) moveChangesIdentifier(ctx context.Context, projectID int64, d *stagedDesign, apply applyFunc) (bool, error) {
	next, err := d.clone()
	if err != nil {
		return false, err
	}
	if apply(next) != nil {
		return false, nil
	}
	return h.gd8Violation(ctx, projectID, next.firstFieldID())
}

// fieldIDs lists fields' ids in position order.
func fieldIDs(fields []stagedField) []int64 {
	out := make([]int64, 0, len(fields))
	for _, f := range sortedFields(fields) {
		out = append(out, f.ID)
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// exprNamesAny reports whether a stored expression references any of these
// names — a field and, for a checkbox, its choice columns (REQ-DB-042).
func exprNamesAny(expr string, names []string) bool {
	for _, name := range names {
		if exprNamesField(expr, name) {
			return true
		}
	}
	return false
}
