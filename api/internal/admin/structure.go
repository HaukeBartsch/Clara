package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// registerStructure mounts the project-design surface of §4: arms (§4.8),
// events (§4.9), instruments (§4.10), fields (§4.11), and the instrument–
// event mapping (§4.12). Reads need data access ≥ read_only on some arm;
// every write needs project_admin (is_admin covered by REQ-AUTH-023).

func (h *Handler) registerStructure(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/arms", h.listArms)
	mux.HandleFunc("POST /api/v1/projects/{id}/arms", h.createArm)
	mux.HandleFunc("DELETE /api/v1/arms/{id}", h.deleteArm)

	mux.HandleFunc("GET /api/v1/projects/{id}/events", h.listEvents)
	mux.HandleFunc("POST /api/v1/projects/{id}/events", h.createEvent)
	mux.HandleFunc("PUT /api/v1/events/{id}", h.updateEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", h.deleteEventHTTP)
	mux.HandleFunc("PUT /api/v1/projects/{id}/events/order", h.orderEvents)

	mux.HandleFunc("GET /api/v1/projects/{id}/instruments", h.listInstruments)
	mux.HandleFunc("POST /api/v1/projects/{id}/instruments", h.createInstrument)
	mux.HandleFunc("PUT /api/v1/projects/{id}/instruments/order", h.orderInstruments)
	mux.HandleFunc("PUT /api/v1/projects/{id}/instruments/{iid}", h.updateInstrument)

	mux.HandleFunc("GET /api/v1/validationTypes", h.validationTypes)

	mux.HandleFunc("GET /api/v1/projects/{id}/instruments/{iid}/fields/order", h.orderFields)
	mux.HandleFunc("GET /api/v1/projects/{id}/instruments/{iid}/fields", h.listFields)
	mux.HandleFunc("POST /api/v1/projects/{id}/instruments/{iid}/fields", h.createField)
	mux.HandleFunc("PUT /api/v1/projects/{id}/instruments/{iid}/fields/order", h.orderFields)
	mux.HandleFunc("PUT /api/v1/projects/{id}/instruments/{iid}/fields/{fid}", h.updateField)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/instruments/{iid}/fields/{fid}", h.deleteField)

	mux.HandleFunc("POST /api/v1/projects/{id}/records/{record}/fields/{fid}/test", h.testField)

	mux.HandleFunc("GET /api/v1/projects/{id}/instrument-event-mapping", h.getMapping)
	mux.HandleFunc("PUT /api/v1/projects/{id}/instrument-event-mapping", h.putMapping)
}

// --- shared gates and design context ---

// structureAccess resolves the actor, the {id} project (404 when the row is
// missing), and the effective levels; callers apply requireMember /
// requireProjectAdmin on top.
func (h *Handler) structureAccess(w http.ResponseWriter, r *http.Request) (*db.User, *authz.Levels, int64, bool) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return nil, nil, 0, false
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return nil, nil, 0, false
	}
	p, err := h.Store.GetProject(r.Context(), projectID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, false
	}
	if p == nil {
		errNotFound(w)
		return nil, nil, 0, false
	}
	lv, err := h.access(r.Context(), u, projectID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, false
	}
	return u, lv, projectID, true
}

// requireRead guards the structure listings: membership plus data access ≥
// read_only on some arm (§4.8–§4.12); a non-member gets the uniform 403.
func (h *Handler) requireRead(w http.ResponseWriter, u *db.User, lv *authz.Levels) bool {
	if !lv.IsMember {
		errForbidden(w)
		return false
	}
	if !u.IsAdmin && !lv.AnyData(LvlReadOnly) {
		errForbidden(w)
		return false
	}
	return true
}

// designCtx is the data dictionary snapshot the §6.2/§7.2 reference rules are
// checked against: every field by name, every event by unique name, and the
// instrument–event mapping ("active at the referenced event", REQ-DB-012).
type designCtx struct {
	fields   map[string]db.Field // by field_name (project scope)
	events   map[string]db.Event // by unique_event_name
	activeAt map[string]map[int64]bool
	deps     []db.CalculatedDependency
	// firstEvent is the project's first event in canonical order (GD-15:
	// arm_num ascending, then per arm timepoint events by period, ties and
	// no-timepoint events by position). A bare [field] reference resolves as
	// if it named this event (§7.1).
	firstEvent string
}

func (h *Handler) loadDesign(ctx context.Context, projectID int64) (*designCtx, error) {
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		return nil, err
	}
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	deps, err := h.Store.ListCalculatedDependencies(ctx, projectID)
	if err != nil {
		return nil, err
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	d := &designCtx{
		fields:   make(map[string]db.Field, len(fields)),
		events:   make(map[string]db.Event, len(events)),
		activeAt: map[string]map[int64]bool{},
		deps:     deps,
	}
	// The first event in canonical order (GD-15) — the fallback target of a
	// bare [field] reference. Arms come from ListArms in arm order; within an
	// arm SortEventsCanonical is the GD-15 order.
	byArm := map[int64][]db.Event{}
	for _, e := range events {
		byArm[e.ArmID] = append(byArm[e.ArmID], e)
	}
	for _, a := range arms {
		list := byArm[a.ID]
		SortEventsCanonical(list)
		if len(list) > 0 {
			d.firstEvent = list[0].UniqueEventName
			break
		}
	}
	for _, f := range fields {
		d.fields[f.FieldName] = f
	}
	eventID := make(map[int64]string, len(events))
	for _, e := range events {
		d.events[e.UniqueEventName] = e
		eventID[e.ID] = e.UniqueEventName
	}
	for _, p := range pairs {
		if name, ok := eventID[p.EventID]; ok {
			if d.activeAt[name] == nil {
				d.activeAt[name] = map[int64]bool{}
			}
			d.activeAt[name][p.InstrumentID] = true
		}
	}
	return d, nil
}

// resolves reports whether a reference names an existing, value-carrying
// field that is active at the referenced event (§6.2 rule 2, §7.2). A bare
// [field] reference (empty Event) falls back to the project's first event —
// the branching-logic allowance of API_Endpoints_Design.md §3.6.3.
func (d *designCtx) resolves(r validate.Ref) bool {
	if r.Event == "" {
		r.Event = d.firstEvent
	}
	if _, ok := d.events[r.Event]; !ok {
		return false
	}
	f, ok := d.fields[r.Field]
	if !ok || !valueCarrying(f.FieldType) {
		return false
	}
	return d.activeAt[r.Event][f.InstrumentID]
}

// valueCarrying excludes the description and header field types — they carry
// no stored value and cannot be expression operands (§6.2).
func valueCarrying(fieldType string) bool {
	return fieldType != "description" && fieldType != "header"
}

// designError is a rejected design-time write: an HTTP status (409 conflict
// vs 400 validation_error / invalid_request) and the machine-readable
// reason (§9).
type designError struct {
	status int
	msg    string
}

func (e *designError) Error() string { return e.msg }

func conflictf(format string, args ...any) *designError {
	return &designError{status: http.StatusConflict, msg: fmt.Sprintf(format, args...)}
}
func validationf(format string, args ...any) *designError {
	return &designError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

// writeDesignError renders a designError with its status code.
func writeDesignError(w http.ResponseWriter, e *designError) {
	if e.status == http.StatusConflict {
		errConflict(w, e.msg)
		return
	}
	errValidation(w, e.msg)
}

// validateCalc checks a calculated expression against the §6.2 rules:
// well-formedness, reference resolution (including the active-at-event rule),
// and acyclicity of the dependency graph with selfID's edges replaced by the
// new expression. It returns the referenced fields for dependency storage.
func (d *designCtx) validateCalc(expr string, selfID int64) ([]validate.Ref, *designError) {
	refs, err := validate.ParseCalcExpression(expr)
	if err != nil {
		return nil, validationf("expression is not well-formed: %s", err)
	}
	for _, ref := range refs {
		if !d.resolves(ref) {
			return nil, validationf("reference %s does not name an active value-carrying field at that event", ref)
		}
	}
	if derr := d.checkAcyclic(selfID, refs); derr != nil {
		return nil, derr
	}
	return refs, nil
}

// checkAcyclic builds the directed graph calculated field → referenced
// calculated field from the stored dependencies (with selfID's edges
// replaced by newRefs) and rejects a cycle through selfID (REQ-VAL-035).
// A brand-new field (selfID 0) is referenced by nothing yet, so it cannot
// close a cycle.
func (d *designCtx) checkAcyclic(selfID int64, newRefs []validate.Ref) *designError {
	if selfID == 0 {
		return nil
	}
	idOf := map[string]int64{} // field name → calculated field id
	for _, f := range d.fields {
		if f.FieldType == "calculated" {
			idOf[f.FieldName] = f.ID
		}
	}
	edges := map[int64][]int64{}
	add := func(from int64, refs []validate.Ref) {
		for _, ref := range refs {
			if to, ok := idOf[ref.Field]; ok {
				edges[from] = append(edges[from], to)
			}
		}
	}
	for _, dep := range d.deps {
		if dep.CalculatedFieldID != selfID {
			add(dep.CalculatedFieldID, []validate.Ref{{Field: dep.RefFieldName}})
		}
	}
	add(selfID, newRefs)

	// Reachability: a path from selfID back to selfID is the cycle. A node
	// already fully explored returned false, so re-visits prune safely.
	seen := map[int64]bool{}
	var visit func(id int64) bool
	visit = func(id int64) bool {
		if seen[id] {
			return id == selfID
		}
		seen[id] = true
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		return false
	}
	if visit(selfID) {
		return validationf("expression would introduce a calculation cycle")
	}
	return nil
}

// --- arms (§4.8) ---

// armEventObject is an event inside the arm listing — the §4.8 shape carries
// no arm_num (the enclosing object supplies it).
type armEventObject struct {
	ID              int64  `json:"id"`
	EventName       string `json:"event_name"`
	UniqueEventName string `json:"unique_event_name"`
	Period          *int64 `json:"period"`
	SafeRegionStart *int64 `json:"safe_region_start"`
	SafeRegionEnd   *int64 `json:"safe_region_end"`
	Position        int    `json:"position"`
}

type armObject struct {
	ID     int64            `json:"id"`
	ArmNum int              `json:"arm_num"`
	Name   string           `json:"name"`
	Events []armEventObject `json:"events"`
}

// listArms returns each arm with its events in the canonical per-arm order
// (GD-15). Data access ≥ read_only.
func (h *Handler) listArms(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	if staged, err := h.stagedRead(r.Context(), projectID); err != nil {
		errInternal(w)
		return
	} else if staged != nil {
		// A staging set is open, so readers see the staged design (§4.21).
		writeJSON(w, http.StatusOK, staged.armObjects())
		return
	}
	arms, eventsByArm, err := h.CanonicalEvents(r.Context(), projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]armObject, 0, len(arms))
	for _, a := range arms {
		objs := make([]armEventObject, 0, len(eventsByArm[a.ArmNum]))
		for _, e := range eventsByArm[a.ArmNum] {
			objs = append(objs, armEventObject{
				ID: e.ID, EventName: e.EventName, UniqueEventName: e.UniqueEventName,
				Period: e.Period, SafeRegionStart: e.SafeRegionStart,
				SafeRegionEnd: e.SafeRegionEnd, Position: e.Position,
			})
		}
		out = append(out, armObject{ID: a.ID, ArmNum: a.ArmNum, Name: a.Name.String, Events: objs})
	}
	writeJSON(w, http.StatusOK, out)
}

// createArm adds an arm; arm_num is the next 1-based number. project_admin.
func (h *Handler) createArm(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "name", "acknowledge_breaking") {
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	name := ""
	if body.Name != nil {
		name = *body.Name
	}
	var created stagedArm
	apply := func(d *stagedDesign) *designError {
		arm := d.putArm(db.Arm{
			ProjectID: projectID, ArmNum: d.nextArmNum(),
			Name: sql.NullString{String: name, Valid: name != ""},
		})
		created = *arm
		return nil
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		// The arm joins the staged design; the live tables keep collecting
		// against the active one until commit (REQ-API-107).
		if !h.applyStagedChange(w, r, projectID, staged, apply) {
			return
		}
		writeJSON(w, http.StatusCreated, armObject{
			ID: created.ID, ArmNum: created.ArmNum, Name: name, Events: []armEventObject{},
		})
		return
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	next := 1
	for _, a := range arms {
		if a.ArmNum >= next {
			next = a.ArmNum + 1
		}
	}
	if dupe, err := h.Store.GetArmByNum(ctx, projectID, next); err != nil {
		errInternal(w)
		return
	} else if dupe != nil {
		errConflict(w, "arm number already exists")
		return
	}
	arm := &db.Arm{ProjectID: projectID, ArmNum: next, Name: sql.NullString{String: name, Valid: name != ""}}
	id, err := h.Store.AddArm(ctx, arm)
	if err != nil {
		errInternal(w)
		return
	}
	arm.ID = id
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.ArmCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{"arm_num": next, "name": name}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, armObject{ID: arm.ID, ArmNum: next, Name: name, Events: []armEventObject{}})
}

// deleteArm removes an empty arm (path verbatim per ASM-API-1). An arm that
// still has events or data → 409 (DEV-API-6). project_admin.
func (h *Handler) deleteArm(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	armID, ok := pathObjectID(r, "id")
	if !ok {
		errBadRequest(w, "invalid arm id")
		return
	}
	ctx := r.Context()
	arm, err := h.Store.GetArm(ctx, armID)
	if err != nil {
		errInternal(w)
		return
	}
	if arm == nil {
		// An arm created while a staging set is open has no live row; the set
		// holding it answers for it.
		if handled, err := h.deleteStagedArm(w, r, u, armID); err != nil {
			errInternal(w)
			return
		} else if handled {
			return
		}
		errNotFound(w)
		return
	}
	lv, err := h.access(ctx, u, arm.ProjectID)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireProjectAdmin(w, r, lv) {
		return
	}
	apply := func(d *stagedDesign) *designError {
		a, ok := d.armByID(armID)
		if !ok {
			return conflictf("arm %d is not part of the staged design", armID)
		}
		if len(a.Events) > 0 {
			return conflictf("arm still has events")
		}
		d.deleteArm(armID)
		return nil
	}
	// Nothing to acknowledge here: an arm that holds events or recorded data
	// cannot be deleted at all (DEV-API-6), so the change is never breaking.
	target, staged, _, ok := h.structureWrite(w, r, arm.ProjectID, false, apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, arm.ProjectID, staged, apply) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	events, err := h.Store.ListEventsByArm(ctx, arm.ProjectID, arm.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if len(events) > 0 {
		errConflict(w, "arm still has events")
		return
	}
	var dataCount int64
	err = h.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM data d WHERE d.project_id = ? AND EXISTS (
			SELECT 1 FROM events e WHERE e.project_id = d.project_id
			 AND e.unique_event_name = d.unique_event_name AND e.arm_id = ?)`,
		arm.ProjectID, armID).Scan(&dataCount)
	if err != nil {
		errInternal(w)
		return
	}
	if dataCount > 0 {
		errConflict(w, "arm still has data")
		return
	}
	if err := h.Store.DeleteArm(ctx, armID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.ArmDeleted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: arm.ProjectID,
		Details: map[string]any{"arm_num": arm.ArmNum, "name": arm.Name.String},
	}); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- events (§4.9) ---

// listEvents returns every event of the project in canonical per-arm order,
// stamped with its arm number. Data access ≥ read_only.
func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	if staged, err := h.stagedRead(r.Context(), projectID); err != nil {
		errInternal(w)
		return
	} else if staged != nil {
		writeJSON(w, http.StatusOK, staged.eventObjects())
		return
	}
	arms, eventsByArm, err := h.CanonicalEvents(r.Context(), projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]EventObject, 0)
	for _, a := range arms {
		out = append(out, eventsByArm[a.ArmNum]...)
	}
	writeJSON(w, http.StatusOK, out)
}

// createEvent adds an event to an arm; the unique name is derived as
// <label>_arm_<n> (REQ-DB-011). project_admin.
func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "arm_num", "event_name", "period",
		"safe_region_start", "safe_region_end", "acknowledge_breaking") {
		return
	}
	var body struct {
		ArmNum          *int    `json:"arm_num"`
		EventName       *string `json:"event_name"`
		Period          *int64  `json:"period"`
		SafeRegionStart *int64  `json:"safe_region_start"`
		SafeRegionEnd   *int64  `json:"safe_region_end"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.ArmNum == nil || body.EventName == nil || *body.EventName == "" {
		errBadRequest(w, "arm_num and event_name are required")
		return
	}
	label := *body.EventName
	var created stagedEvent
	var createdArmNum int
	apply := func(d *stagedDesign) *designError {
		a, ok := d.arm(*body.ArmNum)
		if !ok {
			return validationf("unknown arm number %d", *body.ArmNum)
		}
		unique := uniqueEventName(label, a.ArmNum)
		for _, e := range a.Events {
			if e.EventName == label {
				return conflictf("event label '%s' already exists in arm %d", label, a.ArmNum)
			}
		}
		if _, _, clash := d.eventByUniqueName(unique); clash {
			return conflictf("unique event name '%s' already exists", unique)
		}
		ev, ok := d.putEvent(a.ID, db.Event{
			ProjectID: projectID, ArmID: a.ID, EventName: label, UniqueEventName: unique,
			Period:          nullInt64FromBody(body.Period),
			SafeRegionStart: nullInt64FromBody(body.SafeRegionStart),
			SafeRegionEnd:   nullInt64FromBody(body.SafeRegionEnd),
		})
		if !ok {
			return validationf("unknown arm number %d", *body.ArmNum)
		}
		created, createdArmNum = *ev, a.ArmNum
		return nil
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, staged, apply) {
			return
		}
		writeJSON(w, http.StatusCreated, stagedEventObject(created, createdArmNum))
		return
	}
	arm, err := h.Store.GetArmByNum(ctx, projectID, *body.ArmNum)
	if err != nil {
		errInternal(w)
		return
	}
	if arm == nil {
		errBadRequest(w, "unknown arm number")
		return
	}
	unique := label + "_arm_" + strconv.Itoa(arm.ArmNum)
	events, err := h.Store.ListEventsByArm(ctx, projectID, arm.ID)
	if err != nil {
		errInternal(w)
		return
	}
	for _, e := range events {
		if e.EventName == label {
			errConflict(w, "event label '"+label+"' already exists in arm "+strconv.Itoa(arm.ArmNum))
			return
		}
	}
	if clash, err := h.Store.GetEventByUniqueName(ctx, projectID, unique); err != nil {
		errInternal(w)
		return
	} else if clash != nil {
		errConflict(w, "unique event name '"+unique+"' already exists")
		return
	}
	ev := &db.Event{
		ProjectID: projectID, ArmID: arm.ID, EventName: label, UniqueEventName: unique,
		Period:          nullInt64FromBody(body.Period),
		SafeRegionStart: nullInt64FromBody(body.SafeRegionStart),
		SafeRegionEnd:   nullInt64FromBody(body.SafeRegionEnd),
	}
	id, err := h.Store.AddEvent(ctx, ev)
	if err != nil {
		errInternal(w)
		return
	}
	ev.ID = id
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.EventCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{
			"event_name": label, "unique_event_name": unique, "arm_num": arm.ArmNum,
		}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	obj := NewEventObject(*ev)
	obj.ArmNum = arm.ArmNum
	writeJSON(w, http.StatusCreated, obj)
}

func nullInt64FromBody(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// updateEvent renames or re-times an event (path verbatim per ASM-API-1).
// A label change re-derives the unique name and moves stored values to it in
// the same transaction (ASM-API-4). project_admin.
func (h *Handler) updateEvent(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	eventID, ok := pathObjectID(r, "id")
	if !ok {
		errBadRequest(w, "invalid event id")
		return
	}
	ctx := r.Context()
	ev, err := h.Store.GetEvent(ctx, eventID)
	if err != nil {
		errInternal(w)
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "event_name", "period", "safe_region_start",
		"safe_region_end", "acknowledge_breaking") {
		return
	}

	// The same edit over a design snapshot: production routes it to the open set,
	// and an event created while staging lives only there (§4.21).
	var editedArmNum int
	var edited stagedEvent
	applyEdit := func(d *stagedDesign) *designError {
		se, sa, ok := d.event(eventID)
		if !ok {
			return validationf("event %d is not part of the staged design", eventID)
		}
		if raw, present := supplied["event_name"]; present {
			var label string
			if err := json.Unmarshal(raw, &label); err != nil || label == "" {
				return validationf("event_name must be a non-empty string")
			}
			if label != se.EventName {
				unique := uniqueEventName(label, sa.ArmNum)
				for _, other := range sa.Events {
					if other.ID != se.ID && other.EventName == label {
						return conflictf("event label '%s' already exists in arm %d", label, sa.ArmNum)
					}
				}
				if _, _, clash := d.eventByUniqueName(unique); clash {
					return conflictf("unique event name '%s' already exists", unique)
				}
				se.EventName, se.UniqueEventName = label, unique
			}
		}
		for _, attr := range []struct {
			key string
			dst *sql.NullInt64
		}{
			{"period", &se.Period}, {"safe_region_start", &se.SafeRegionStart},
			{"safe_region_end", &se.SafeRegionEnd},
		} {
			raw, present := supplied[attr.key]
			if !present {
				continue
			}
			var v *int64
			if err := json.Unmarshal(raw, &v); err != nil {
				return validationf("invalid value for %s", attr.key)
			}
			*attr.dst = nullInt64FromBody(v)
		}
		edited, editedArmNum = *se, sa.ArmNum
		return nil
	}
	stagedResponse := func() {
		writeJSON(w, http.StatusOK, stagedEventObject(edited, editedArmNum))
	}
	if ev == nil {
		handled, err := h.stagedObjectWrite(w, r, u,
			func(d *stagedDesign) bool { _, _, ok := d.event(eventID); return ok },
			applyEdit, stagedResponse)
		if err != nil {
			errInternal(w)
			return
		}
		if !handled {
			errNotFound(w)
		}
		return
	}
	lv, err := h.access(ctx, u, ev.ProjectID)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireProjectAdmin(w, r, lv) {
		return
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, ev.ProjectID, acknowledgeFrom(supplied), applyEdit)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, ev.ProjectID, staged, applyEdit) {
			return
		}
		stagedResponse()
		return
	}

	next := *ev
	changes := map[string]any{}
	if raw, present := supplied["event_name"]; present {
		var label string
		if err := json.Unmarshal(raw, &label); err != nil || label == "" {
			errBadRequest(w, "event_name must be a non-empty string")
			return
		}
		if label != ev.EventName {
			arm, err := h.Store.GetArm(ctx, ev.ArmID)
			if err != nil {
				errInternal(w)
				return
			}
			if arm == nil {
				errInternal(w)
				return
			}
			newUnique := label + "_arm_" + strconv.Itoa(arm.ArmNum)
			events, err := h.Store.ListEventsByArm(ctx, ev.ProjectID, ev.ArmID)
			if err != nil {
				errInternal(w)
				return
			}
			for _, other := range events {
				if other.ID != ev.ID && other.EventName == label {
					errConflict(w, "event label '"+label+"' already exists in arm "+strconv.Itoa(arm.ArmNum))
					return
				}
			}
			if clash, err := h.Store.GetEventByUniqueName(ctx, ev.ProjectID, newUnique); err != nil {
				errInternal(w)
				return
			} else if clash != nil && clash.ID != ev.ID {
				errConflict(w, "unique event name '"+newUnique+"' already exists")
				return
			}
			changes["label"] = map[string]any{"old": ev.EventName, "new": label}
			next.EventName = label
			next.UniqueEventName = newUnique
		}
	}
	for _, attr := range []struct {
		key  string // request/audit key for period is period_days (§3.3)
		oldV sql.NullInt64
		dst  *sql.NullInt64
	}{
		{"period_days", ev.Period, &next.Period},
		{"safe_region_start", ev.SafeRegionStart, &next.SafeRegionStart},
		{"safe_region_end", ev.SafeRegionEnd, &next.SafeRegionEnd},
	} {
		raw, present := supplied[attr.key]
		if attr.key == "period_days" {
			raw, present = supplied["period"]
		}
		if !present {
			continue
		}
		var v *int64
		if err := json.Unmarshal(raw, &v); err != nil {
			errBadRequest(w, "invalid value for "+attr.key)
			return
		}
		newVal := nullInt64FromBody(v)
		if newVal == attr.oldV {
			continue
		}
		changes[attr.key] = map[string]any{"old": nullableIntJSON(attr.oldV), "new": nullableIntJSON(newVal)}
		*attr.dst = newVal
	}

	renamed := next.UniqueEventName != ev.UniqueEventName
	if len(changes) > 0 {
		tx, err := h.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			errInternal(w)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx,
			`UPDATE events SET event_name = ?, unique_event_name = ?,
				period = ?, safe_region_start = ?, safe_region_end = ? WHERE id = ?`,
			next.EventName, next.UniqueEventName, nullInt64Param(next.Period),
			nullInt64Param(next.SafeRegionStart), nullInt64Param(next.SafeRegionEnd), ev.ID); err != nil {
			errInternal(w)
			return
		}
		if renamed {
			// Stored values and dependency rows follow the new unique name in
			// the same transaction (ASM-API-4).
			if _, err := tx.ExecContext(ctx,
				`UPDATE data SET unique_event_name = ? WHERE project_id = ? AND unique_event_name = ?`,
				next.UniqueEventName, ev.ProjectID, ev.UniqueEventName); err != nil {
				errInternal(w)
				return
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE calculated_dependencies SET ref_unique_event_name = ?
				 WHERE project_id = ? AND ref_unique_event_name = ?`,
				next.UniqueEventName, ev.ProjectID, ev.UniqueEventName); err != nil {
				errInternal(w)
				return
			}
		}
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.EventUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: ev.ProjectID,
			Details: withBreakingAcknowledgement(map[string]any{"event_id": ev.ID, "changes": changes}, breakingAcknowledged),
		}); err != nil {
			errInternal(w)
			return
		}
		if err := tx.Commit(); err != nil {
			errInternal(w)
			return
		}
	}
	out := NewEventObject(next)
	if arm, err := h.Store.GetArm(ctx, ev.ArmID); err == nil && arm != nil {
		out.ArmNum = arm.ArmNum
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteEventHTTP removes one event (§4.9). A project always keeps at least
// one event — deleting the last remaining event is 409 (its rename and
// reorder stay available). The event's instrument–event mapping pairs go with
// it; an event that holds values makes them unreachable, which analysis mode
// acknowledges (§4.21). project_admin; 204. Audit `event_deleted`.
func (h *Handler) deleteEventHTTP(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	eventID, ok := pathObjectID(r, "id")
	if !ok {
		errBadRequest(w, "invalid event id")
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ev, err := h.Store.GetEvent(ctx, eventID)
	if err != nil {
		errInternal(w)
		return
	}
	applyDelete := func(d *stagedDesign) *designError {
		if _, _, ok := d.event(eventID); !ok {
			return validationf("event %d is not part of the staged design", eventID)
		}
		if d.totalEvents() <= 1 {
			return conflictf("a project must keep at least one event")
		}
		d.deleteEvent(eventID)
		return nil
	}
	if ev == nil {
		// An event created while a staging set is open has no live row; the
		// set holding it answers for it (same route as DELETE /arms/{id}).
		handled, err := h.stagedObjectWrite(w, r, u,
			func(d *stagedDesign) bool { _, _, ok := d.event(eventID); return ok },
			applyDelete, func() { w.WriteHeader(http.StatusNoContent) })
		if err != nil {
			errInternal(w)
			return
		}
		if !handled {
			errNotFound(w)
		}
		return
	}
	lv, err := h.access(ctx, u, ev.ProjectID)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireProjectAdmin(w, r, lv) {
		return
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(
		w, r, ev.ProjectID, acknowledgeFrom(supplied), applyDelete)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, ev.ProjectID, staged, applyDelete) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	events, err := h.Store.ListEvents(ctx, ev.ProjectID)
	if err != nil {
		errInternal(w)
		return
	}
	if len(events) <= 1 {
		errConflict(w, "a project must keep at least one event")
		return
	}
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM instrument_events WHERE event_id = ?`, eventID); err != nil {
		errInternal(w)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calculated_dependencies WHERE project_id = ? AND ref_unique_event_name = ?`,
		ev.ProjectID, ev.UniqueEventName); err != nil {
		errInternal(w)
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, eventID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.EventDeleted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: ev.ProjectID,
		Details: withBreakingAcknowledgement(map[string]any{
			"event_id": eventID, "unique_event_name": ev.UniqueEventName,
		}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nullInt64Param renders a sql.NullInt64 as an SQL parameter.
func nullInt64Param(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// nullStr renders a sql.NullString as an SQL parameter (NULL when unset).
func nullStr(ns sql.NullString) any {
	if !ns.Valid {
		return nil
	}
	return ns.String
}

// boolToIntParam writes booleans to the integer flag columns.
func boolToIntParam(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullableIntJSON renders a sql.NullInt64 for an audit changes map (null when
// the timepoint is absent).
func nullableIntJSON(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// orderEvents writes the full user order of one arm's events (REQ-API-103);
// the canonical display order GD-15 then governs listings. project_admin.
func (h *Handler) orderEvents(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "arm_num", "order", "acknowledge_breaking") {
		return
	}
	var body struct {
		ArmNum *int    `json:"arm_num"`
		Order  []int64 `json:"order"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil || body.ArmNum == nil {
		errBadRequest(w, "arm_num and order are required")
		return
	}
	apply := func(d *stagedDesign) *designError {
		a, ok := d.arm(*body.ArmNum)
		if !ok {
			return validationf("unknown arm number %d", *body.ArmNum)
		}
		if !d.orderEvents(a.ID, body.Order) {
			return validationf("order must list every event of the arm exactly once")
		}
		return nil
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		a, found := staged.arm(*body.ArmNum)
		if !found {
			errBadRequest(w, "unknown arm number")
			return
		}
		if !h.applyStagedChange(w, r, projectID, staged, apply) {
			return
		}
		out := make([]EventObject, 0, len(a.Events))
		for _, e := range canonicalStagedEvents(a.Events) {
			out = append(out, stagedEventObject(e, a.ArmNum))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	arm, err := h.Store.GetArmByNum(ctx, projectID, *body.ArmNum)
	if err != nil {
		errInternal(w)
		return
	}
	if arm == nil {
		errBadRequest(w, "unknown arm number")
		return
	}
	events, err := h.Store.ListEventsByArm(ctx, projectID, arm.ID)
	if err != nil {
		errInternal(w)
		return
	}
	member := map[int64]bool{}
	for _, e := range events {
		member[e.ID] = true
	}
	seen := map[int64]bool{}
	for _, id := range body.Order {
		if !member[id] || seen[id] {
			errBadRequest(w, "order must list every event of the arm exactly once")
			return
		}
		seen[id] = true
	}
	if len(body.Order) != len(events) {
		errBadRequest(w, "order must list every event of the arm exactly once")
		return
	}
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	for i, id := range body.Order {
		if _, err := tx.ExecContext(ctx, `UPDATE events SET position = ? WHERE id = ?`, i+1, id); err != nil {
			errInternal(w)
			return
		}
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.EventReordered, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{"arm_num": arm.ArmNum, "order": body.Order}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	// Respond with the arm's events in their new canonical order.
	events, err = h.Store.ListEventsByArm(ctx, projectID, arm.ID)
	if err != nil {
		errInternal(w)
		return
	}
	SortEventsCanonical(events)
	out := make([]EventObject, 0, len(events))
	for _, e := range events {
		o := NewEventObject(e)
		o.ArmNum = arm.ArmNum
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- instruments (§4.10) ---

type instrumentObject struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Position       int    `json:"position"`
	FieldCount     int    `json:"field_count"`
	IsSurvey       bool   `json:"is_survey"`
	BranchingLogic string `json:"branching_logic"`
}

// listInstruments returns the project's instruments in position order with
// their field counts. Data access ≥ read_only.
func (h *Handler) listInstruments(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	if staged, err := h.stagedRead(r.Context(), projectID); err != nil {
		errInternal(w)
		return
	} else if staged != nil {
		writeJSON(w, http.StatusOK, staged.instrumentObjects())
		return
	}
	ctx := r.Context()
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	counts, err := h.fieldCounts(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]instrumentObject, 0, len(instruments))
	for _, i := range instruments {
		out = append(out, instrumentObject{
			ID: i.ID, Name: i.Name, Position: i.Position, FieldCount: counts[i.ID],
			IsSurvey: i.IsSurvey, BranchingLogic: i.BranchingLogic.String,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// fieldCounts maps instrument id → number of fields (one grouped query).
func (h *Handler) fieldCounts(ctx context.Context, projectID int64) (map[int64]int, error) {
	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT instrument_id, COUNT(*) FROM fields WHERE project_id = ? GROUP BY instrument_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// createInstrument adds an instrument (name unique within the project).
// project_admin.
func (h *Handler) createInstrument(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "name", "acknowledge_breaking") {
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil || body.Name == nil || *body.Name == "" {
		errBadRequest(w, "name is required")
		return
	}
	name := *body.Name
	var created stagedInstrument
	apply := func(d *stagedDesign) *designError {
		if _, dup := d.instrumentByName(name); dup {
			return conflictf("instrument '%s' already exists in this project", name)
		}
		inst := d.putInstrument(db.Instrument{ProjectID: projectID, Name: name})
		created = *inst
		return nil
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, staged, apply) {
			return
		}
		writeJSON(w, http.StatusCreated, instrumentObject{
			ID: created.ID, Name: name, Position: created.Position, FieldCount: 0,
		})
		return
	}
	if dupe, err := h.Store.GetInstrumentByName(ctx, projectID, name); err != nil {
		errInternal(w)
		return
	} else if dupe != nil {
		errConflict(w, "instrument '"+name+"' already exists in this project")
		return
	}
	inst := &db.Instrument{ProjectID: projectID, Name: name}
	id, err := h.Store.AddInstrument(ctx, inst)
	if err != nil {
		errInternal(w)
		return
	}
	inst.ID = id
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.InstrumentCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{"name": name, "position": inst.Position}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, instrumentObject{
		ID: inst.ID, Name: name, Position: inst.Position, FieldCount: 0,
	})
}

// orderInstruments writes the full project instrument order; the GD-8
// record-identifier invariant must survive (violation → 400). project_admin.
func (h *Handler) orderInstruments(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "order", "acknowledge_breaking") {
		return
	}
	var body struct {
		Order []int64 `json:"order"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	apply := func(d *stagedDesign) *designError {
		if !d.orderInstruments(body.Order) {
			return validationf("order must list every instrument of the project exactly once")
		}
		return nil
	}
	target, staged, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		// GD-8 is about stored records, so the live check applies unchanged.
		if len(body.Order) > 0 {
			first, found := staged.instrument(body.Order[0])
			if !found {
				errBadRequest(w, "order must list every instrument of the project exactly once")
				return
			}
			var newFirst int64
			if fields := sortedFields(first.Fields); len(fields) > 0 {
				newFirst = fields[0].ID
			}
			if bad, err := h.gd8Violation(ctx, projectID, newFirst); err != nil {
				errInternal(w)
				return
			} else if bad {
				errBadRequest(w, "reordering would move the record identifier field (GD-8)")
				return
			}
		}
		if !h.applyStagedChange(w, r, projectID, staged, apply) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	member := map[int64]db.Instrument{}
	for _, i := range instruments {
		member[i.ID] = i
	}
	seen := map[int64]bool{}
	for _, id := range body.Order {
		if _, ok := member[id]; !ok || seen[id] {
			errBadRequest(w, "order must list every instrument of the project exactly once")
			return
		}
		seen[id] = true
	}
	if len(body.Order) != len(instruments) {
		errBadRequest(w, "order must list every instrument of the project exactly once")
		return
	}
	// GD-8: the first field of the first instrument is the record
	// identifier; with records present the identifier may not move.
	if len(body.Order) > 0 {
		firstFields, err := h.Store.ListFieldsByInstrument(ctx, projectID, body.Order[0])
		if err != nil {
			errInternal(w)
			return
		}
		var newFirst int64
		if len(firstFields) > 0 {
			newFirst = firstFields[0].ID
		}
		if bad, err := h.gd8Violation(ctx, projectID, newFirst); err != nil {
			errInternal(w)
			return
		} else if bad {
			errBadRequest(w, "reordering would move the record identifier field (GD-8)")
			return
		}
	}
	names := make([]string, 0, len(body.Order))
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	for i, id := range body.Order {
		if _, err := tx.ExecContext(ctx, `UPDATE instruments SET position = ? WHERE id = ?`, i+1, id); err != nil {
			errInternal(w)
			return
		}
		names = append(names, member[id].Name)
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.InstrumentReordered, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{"order": names}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// gd8Violation reports whether making newFirstFieldID the first field of the
// first instrument would move an established record identifier while records
// exist (GD-8). A project without records may set its identifier freely.
func (h *Handler) gd8Violation(ctx context.Context, projectID int64, newFirstFieldID int64) (bool, error) {
	var n int64
	err := h.Store.DB.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM record_entities WHERE project_id = ?)
		     + (SELECT COUNT(*) FROM data WHERE project_id = ?)`,
		projectID, projectID).Scan(&n)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	current, err := h.recordIdentifierField(ctx, projectID)
	if err != nil {
		return false, err
	}
	if current == 0 {
		return false, nil // no identifier established yet — any placement is legal
	}
	return newFirstFieldID != current, nil
}

// recordIdentifierField returns the id of the GD-8 record-identifier field
// (position 1 of the instrument at position 1); 0 when none exists.
func (h *Handler) recordIdentifierField(ctx context.Context, projectID int64) (int64, error) {
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil || len(instruments) == 0 {
		return 0, err
	}
	fields, err := h.Store.ListFieldsByInstrument(ctx, projectID, instruments[0].ID)
	if err != nil || len(fields) == 0 {
		return 0, err
	}
	return fields[0].ID, err
}

// updateInstrument sets is_survey and/or branching_logic (REQ-API-101). An
// invalid branching expression is rejected at design time (§7.2).
// project_admin.
func (h *Handler) updateInstrument(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	instID, ok := pathObjectID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "is_survey", "branching_logic", "acknowledge_breaking") {
		return
	}

	// While a set is open the instrument is the snapshot's — one created during
	// staging has no live row to read or write (REQ-API-107). With no set open the
	// gate below answers 409 in production, so the live row is resolved after it.
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
	}

	// The edit over a design snapshot: production applies it to the open set, and
	// analysis mode runs it against a throwaway copy only to classify it (§4.21).
	// References resolve against the design being edited, so an expression naming a
	// staged field validates here rather than failing against the active tables.
	apply := func(d *stagedDesign) *designError {
		si, found := d.instrument(instID)
		if !found {
			return validationf("instrument %d is not part of the staged design", instID)
		}
		if raw, present := supplied["is_survey"]; present {
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return validationf("is_survey must be a boolean")
			}
			si.IsSurvey = v
		}
		if raw, present := supplied["branching_logic"]; present {
			var expr string
			if err := json.Unmarshal(raw, &expr); err != nil {
				return validationf("branching_logic must be a string")
			}
			if expr != "" {
				if verr := validate.ValidateBranching(expr, d.designContext(projectID).resolves); verr != nil {
					return validationf("branching logic is not valid: %s", verr)
				}
			}
			si.BranchingLogic = sql.NullString{String: expr, Valid: expr != ""}
		}
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		obj, _ := set.instrumentObjectFor(instID)
		writeJSON(w, http.StatusOK, obj)
		return
	}

	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	next := *inst
	changes := map[string]any{}
	if raw, present := supplied["is_survey"]; present {
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			errBadRequest(w, "is_survey must be a boolean")
			return
		}
		if v != inst.IsSurvey {
			changes["is_survey"] = map[string]any{"old": boolToIntJSON(inst.IsSurvey), "new": boolToIntJSON(v)}
			next.IsSurvey = v
		}
	}
	if raw, present := supplied["branching_logic"]; present {
		var expr string
		if err := json.Unmarshal(raw, &expr); err != nil {
			errBadRequest(w, "branching_logic must be a string")
			return
		}
		if expr != "" {
			d, err := h.loadDesign(ctx, projectID)
			if err != nil {
				errInternal(w)
				return
			}
			if verr := validate.ValidateBranching(expr, d.resolves); verr != nil {
				errValidation(w, "branching logic is not valid: "+verr.Error())
				return
			}
		}
		if expr != inst.BranchingLogic.String {
			changes["branching_logic"] = map[string]any{"old": inst.BranchingLogic.String, "new": expr}
			next.BranchingLogic = sql.NullString{String: expr, Valid: expr != ""}
		}
	}
	if len(changes) > 0 {
		if err := h.Store.UpdateInstrument(ctx, &next); err != nil {
			errInternal(w)
			return
		}
		if err := h.Audit.Insert(ctx, audit.Entry{
			EventType: audit.InstrumentUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: projectID,
			Details: withBreakingAcknowledgement(map[string]any{"name": inst.Name, "changes": changes}, breakingAcknowledged),
		}); err != nil {
			errInternal(w)
			return
		}
	}
	counts, err := h.fieldCounts(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, instrumentObject{
		ID: next.ID, Name: next.Name, Position: next.Position, FieldCount: counts[next.ID],
		IsSurvey: next.IsSurvey, BranchingLogic: next.BranchingLogic.String,
	})
}

func boolToIntJSON(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- fields (§4.11) ---

// fieldObject is the §4.11 representation. Nullable free-text attributes
// render as ""; validation_format/min/max keep JSON null when unset.
type fieldObject struct {
	ID                  int64   `json:"id"`
	FieldName           string  `json:"field_name"`
	FieldLabel          string  `json:"field_label"`
	FieldType           string  `json:"field_type"`
	SectionHeader       string  `json:"section_header"`
	Choices             string  `json:"choices"`
	FieldNote           string  `json:"field_note"`
	ValidationType      string  `json:"validation_type"`
	ValidationFormat    *string `json:"validation_format"`
	ValidationMin       *string `json:"validation_min"`
	ValidationMax       *string `json:"validation_max"`
	Required            bool    `json:"required"`
	BranchingLogic      string  `json:"branching_logic"`
	Calculation         string  `json:"calculation"`
	MatrixGroup         string  `json:"matrix_group"`
	PersonalInformation bool    `json:"personal_information"`
	DirectIdentifier    bool    `json:"direct_identifier"`
	Position            int     `json:"position"`
}

func newFieldObject(f db.Field) fieldObject {
	return fieldObject{
		ID: f.ID, FieldName: f.FieldName, FieldLabel: f.FieldLabel.String,
		FieldType: f.FieldType, SectionHeader: f.SectionHeader.String,
		Choices: f.Choices.String, FieldNote: f.FieldNote.String,
		ValidationType: f.ValidationType.String, ValidationFormat: NullStrPtr(f.ValidationFormat),
		ValidationMin: NullStrPtr(f.ValidationMin), ValidationMax: NullStrPtr(f.ValidationMax),
		Required: f.Required, BranchingLogic: f.BranchingLogic.String,
		Calculation: f.Calculation.String, MatrixGroup: f.MatrixGroup.String,
		PersonalInformation: f.PersonalInformation, DirectIdentifier: f.DirectIdentifier,
		Position: f.Position,
	}
}

var (
	fieldNameRE   = regexp.MustCompile(`^[a-z0-9_]+$`)
	choiceCodeRE  = regexp.MustCompile(`^[0-9]+$`)
	formatTokenRE = regexp.MustCompile(`^[YmdHi \-:]+$`)
	integerLitRE  = regexp.MustCompile(`^-?[0-9]+$`)
	floatLitRE    = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?$`)
)

// fieldTypes is the §1 schema vocabulary of fields.field_type.
var fieldTypes = map[string]bool{
	"text": true, "dropdown": true, "radio": true, "matrix": true,
	"description": true, "header": true, "calculated": true,
}

// builtinValidationTypes are the four built-in structured types (§4).
var builtinValidationTypes = map[string]bool{
	"integer": true, "floating point": true, "date": true, "datetime": true,
}

// reservedFieldNames never appear in a data dictionary — the flat export row
// would otherwise be ambiguous (REQ-API-028).
var reservedFieldNames = map[string]bool{
	"redcap_event_name": true, "redcap_repeat_instrument": true, "redcap_repeat_instance": true,
}

// validateFieldDesign enforces the §9 dictionary rules on the proposed field
// state. selfID is 0 for a new field; uniqueness and cycle checks exclude it.
// d is the design the proposal is checked against — the live tables on a direct
// edit, the open set's snapshot while one is up (stagedDesign.designContext) —
// which is also what carries the uniqueness rule: field_name is unique per
// project on both sides, so the dictionary view answers for the UNIQUE constraint
// either way. It returns the validated calculation references when the field is
// calculated (nil otherwise).
func (h *Handler) validateFieldDesign(ctx context.Context, f *db.Field, selfID int64, d *designCtx) ([]validate.Ref, *designError) {
	if !fieldNameRE.MatchString(f.FieldName) {
		return nil, validationf("field_name must match ^[a-z0-9_]+$")
	}
	if reservedFieldNames[f.FieldName] {
		return nil, validationf("field_name %q is reserved", f.FieldName)
	}
	if dupe, taken := d.fields[f.FieldName]; taken && dupe.ID != selfID {
		return nil, conflictf("field_name %q already exists in this project", f.FieldName)
	}
	if !fieldTypes[f.FieldType] {
		return nil, validationf("unknown field_type %q", f.FieldType)
	}
	if f.Choices.String != "" {
		if derr := validateChoices(f.Choices.String); derr != nil {
			return nil, derr
		}
	}
	vt := f.ValidationType.String
	if vt != "" && !builtinValidationTypes[vt] {
		types, err := h.Store.ListValidationTypes(ctx)
		if err != nil {
			return nil, &designError{status: http.StatusInternalServerError, msg: "internal"}
		}
		found := false
		for _, t := range types {
			if t.Name == vt {
				found = true
				break
			}
		}
		if !found {
			return nil, validationf("unknown validation_type %q", vt)
		}
	}
	if vt == "integer" || vt == "floating point" {
		numRE := floatLitRE
		if vt == "integer" {
			numRE = integerLitRE
		}
		var minV, maxV *float64
		for _, pair := range []struct {
			name string
			v    sql.NullString
			dst  **float64
		}{
			{"validation_min", f.ValidationMin, &minV},
			{"validation_max", f.ValidationMax, &maxV},
		} {
			if !pair.v.Valid || pair.v.String == "" {
				continue
			}
			if !numRE.MatchString(pair.v.String) {
				return nil, validationf("%s must be a valid %s: %q", pair.name, vt, pair.v.String)
			}
			n, err := strconv.ParseFloat(pair.v.String, 64)
			if err != nil {
				return nil, validationf("%s must be a valid %s: %q", pair.name, vt, pair.v.String)
			}
			*pair.dst = &n
		}
		if minV != nil && maxV != nil && *minV > *maxV {
			return nil, validationf("validation_min must be ≤ validation_max")
		}
	} else {
		// min/max are ignored for every other type (REQ-VAL-020).
		f.ValidationMin = sql.NullString{}
		f.ValidationMax = sql.NullString{}
	}
	if vt == "date" || vt == "datetime" {
		if f.ValidationFormat.Valid && f.ValidationFormat.String != "" {
			if derr := validateFormatTokens(vt, f.ValidationFormat.String); derr != nil {
				return nil, derr
			}
		}
	} else {
		f.ValidationFormat = sql.NullString{}
	}
	var refs []validate.Ref
	calc := f.Calculation.String
	if calc != "" && f.FieldType != "calculated" {
		return nil, validationf("calculation is only allowed for calculated fields")
	}
	// The dictionary must see this field under its (possibly new) name so that
	// expression checks resolve it (§6.2). d belongs to this request alone — every
	// caller loads or derives it fresh — so the overlay cannot leak into a later read.
	d.fields[f.FieldName] = *f
	if f.FieldType == "calculated" {
		if calc == "" {
			return nil, validationf("a calculated field requires a calculation expression")
		}
		var derr *designError
		refs, derr = d.validateCalc(calc, selfID)
		if derr != nil {
			return nil, derr
		}
	}
	if f.BranchingLogic.String != "" {
		if verr := validate.ValidateBranching(f.BranchingLogic.String, d.resolves); verr != nil {
			return nil, validationf("branching logic is not valid: %s", verr)
		}
	}
	return refs, nil
}

// validateChoices checks the code$label##code$label encoding: codes are
// non-empty, numeric and unique; labels are non-empty (REQ-VAL-022).
func validateChoices(s string) *designError {
	seen := map[string]bool{}
	for _, part := range strings.Split(s, "##") {
		idx := strings.Index(part, "$")
		if idx <= 0 || idx == len(part)-1 {
			return validationf("choices must use the code$label##code$label encoding")
		}
		code, label := part[:idx], part[idx+1:]
		if !choiceCodeRE.MatchString(code) {
			return validationf("choice code %q must be numeric", code)
		}
		if seen[code] {
			return validationf("duplicate choice code %q", code)
		}
		seen[code] = true
		if strings.TrimSpace(label) == "" {
			return validationf("choice label for code %q must not be empty", code)
		}
	}
	return nil
}

// validateFormatTokens checks a date/datetime validation_format: only the
// §4.1 tokens and separators, each token present exactly once.
func validateFormatTokens(vt, format string) *designError {
	if !formatTokenRE.MatchString(format) {
		return validationf("validation_format may only use the tokens Y m d%s with the separators - : space", map[bool]string{true: " H i"}[vt == "datetime"])
	}
	required := []byte{'Y', 'm', 'd'}
	if vt == "datetime" {
		required = append(required, 'H', 'i')
	}
	for _, tok := range required {
		if strings.Count(format, string(tok)) != 1 {
			return validationf("validation_format must contain the token %q exactly once", string(tok))
		}
	}
	return nil
}

// listFields returns one instrument's fields in position order. Data access
// ≥ read_only.
func (h *Handler) listFields(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	instID, ok := pathObjectID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}
	ctx := r.Context()
	if staged, err := h.stagedRead(ctx, projectID); err != nil {
		errInternal(w)
		return
	} else if staged != nil {
		// The instrument may itself be staged (a provisional id), so it is
		// resolved from the set rather than the live tables.
		if _, ok := staged.instrument(instID); !ok {
			errNotFound(w)
			return
		}
		writeJSON(w, http.StatusOK, staged.fieldObjects(projectID, instID))
		return
	}
	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	fields, err := h.Store.ListFieldsByInstrument(ctx, projectID, instID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]fieldObject, 0, len(fields))
	for _, f := range fields {
		out = append(out, newFieldObject(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// fieldInput is the §4.11 request body: any subset of the writable
// dictionary attributes (POST requires field_name and field_type).
type fieldInput struct {
	FieldName           *string `json:"field_name"`
	FieldLabel          *string `json:"field_label"`
	FieldType           *string `json:"field_type"`
	SectionHeader       *string `json:"section_header"`
	Choices             *string `json:"choices"`
	FieldNote           *string `json:"field_note"`
	ValidationType      *string `json:"validation_type"`
	ValidationFormat    *string `json:"validation_format"`
	ValidationMin       *string `json:"validation_min"`
	ValidationMax       *string `json:"validation_max"`
	Required            *bool   `json:"required"`
	BranchingLogic      *string `json:"branching_logic"`
	Calculation         *string `json:"calculation"`
	MatrixGroup         *string `json:"matrix_group"`
	PersonalInformation *bool   `json:"personal_information"`
	DirectIdentifier    *bool   `json:"direct_identifier"`
}

// fieldInputAttrs are the writable §4.11 dictionary attributes plus
// acknowledge_breaking, which every structure write accepts (§4.21: an
// analysis-mode breaking change applies only once the caller acknowledges it;
// in production and development the flag is ignored). It is not design data, so
// fieldInput has no matching attribute — decodeFieldInput reports it separately.
var fieldInputAttrs = []string{
	"field_name", "field_label", "field_type", "section_header", "choices", "field_note",
	"validation_type", "validation_format", "validation_min", "validation_max",
	"required", "branching_logic", "calculation", "matrix_group",
	"personal_information", "direct_identifier", "acknowledge_breaking",
}

func (h *Handler) decodeFieldInput(w http.ResponseWriter, r *http.Request) (*fieldInput, bool, bool) {
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return nil, false, false
	}
	if !RejectUnknownAttrs(w, supplied, fieldInputAttrs...) {
		return nil, false, false
	}
	var in fieldInput
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &in); err != nil {
		errBadRequest(w, "malformed JSON body")
		return nil, false, false
	}
	return &in, acknowledgeFrom(supplied), true
}

// apply overlays the present attributes onto the proposed field state and
// reports which attributes actually changed (audit "changes" keys).
func (in *fieldInput) apply(f *db.Field) map[string]any {
	changes := map[string]any{}
	setStr := func(key string, src *string, dst *sql.NullString) {
		if src == nil || dst.String == *src {
			return
		}
		changes[key] = map[string]any{"old": dst.String, "new": *src}
		*dst = sql.NullString{String: *src, Valid: *src != ""}
	}
	setStr("field_label", in.FieldLabel, &f.FieldLabel)
	setStr("section_header", in.SectionHeader, &f.SectionHeader)
	setStr("choices", in.Choices, &f.Choices)
	setStr("field_note", in.FieldNote, &f.FieldNote)
	setStr("validation_type", in.ValidationType, &f.ValidationType)
	setStr("validation_format", in.ValidationFormat, &f.ValidationFormat)
	setStr("validation_min", in.ValidationMin, &f.ValidationMin)
	setStr("validation_max", in.ValidationMax, &f.ValidationMax)
	setStr("branching_logic", in.BranchingLogic, &f.BranchingLogic)
	setStr("calculation", in.Calculation, &f.Calculation)
	setStr("matrix_group", in.MatrixGroup, &f.MatrixGroup)
	if in.FieldType != nil && f.FieldType != *in.FieldType {
		changes["field_type"] = map[string]any{"old": f.FieldType, "new": *in.FieldType}
		f.FieldType = *in.FieldType
	}
	setBool := func(key string, src *bool, dst *bool) {
		if src == nil || *dst == *src {
			return
		}
		changes[key] = map[string]any{"old": boolToIntJSON(*dst), "new": boolToIntJSON(*src)}
		*dst = *src
	}
	setBool("required", in.Required, &f.Required)
	setBool("personal_information", in.PersonalInformation, &f.PersonalInformation)
	setBool("direct_identifier", in.DirectIdentifier, &f.DirectIdentifier)
	return changes
}

// createField appends a field to an instrument. project_admin.
func (h *Handler) createField(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	instID, ok := pathObjectID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}
	ctx := r.Context()
	in, ack, ok := h.decodeFieldInput(w, r)
	if !ok {
		return
	}
	if in.FieldName == nil || in.FieldType == nil {
		errBadRequest(w, "field_name and field_type are required")
		return
	}

	// While a set is open the instrument may itself be staged — an instrument
	// created during staging has a provisional id and no live row, so it resolves
	// from the snapshot and nowhere else (REQ-API-107).
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
	}

	var created stagedField
	apply := func(d *stagedDesign) *designError {
		f := &db.Field{ProjectID: projectID, InstrumentID: instID, FieldName: *in.FieldName, FieldType: *in.FieldType}
		in.apply(f)
		// AddField presets the identifier flag from a direct-identifier validation
		// type; a staged field writes no row, so the preset happens here (REQ-EXP-020).
		if !f.DirectIdentifier && validate.PresetDirectIdentifier(f.ValidationType.String) {
			f.DirectIdentifier = true
		}
		// Dictionary rules run against the snapshot: uniqueness is the project's,
		// and an expression may name a field that only exists in this set (§6.2).
		if _, derr := h.validateFieldDesign(ctx, f, 0, d.designContext(projectID)); derr != nil {
			return derr
		}
		sf, placed := d.putField(instID, *f)
		if !placed {
			return validationf("instrument %d is not part of the staged design", instID)
		}
		created = *sf
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, ack, apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		writeJSON(w, http.StatusCreated, newFieldObject(created.field(projectID)))
		return
	}

	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	design, err := h.loadDesign(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	f := &db.Field{ProjectID: projectID, InstrumentID: instID, FieldName: *in.FieldName, FieldType: *in.FieldType}
	in.apply(f)
	refs, derr := h.validateFieldDesign(ctx, f, 0, design)
	if derr != nil {
		writeDesignError(w, derr)
		return
	}
	id, err := h.Store.AddField(ctx, f)
	if err != nil {
		errInternal(w)
		return
	}
	f.ID = id
	if len(refs) > 0 {
		tx, err := h.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			errInternal(w)
			return
		}
		defer tx.Rollback()
		if err := putCalcDepsTx(ctx, tx, projectID, id, refs); err != nil {
			errInternal(w)
			return
		}
		if err := tx.Commit(); err != nil {
			errInternal(w)
			return
		}
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.FieldCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{
			"instrument": inst.Name, "field": f.FieldName, "type": f.FieldType,
		}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, newFieldObject(*f))
}

// putCalcDepsTx (re)populates calculated_dependencies for one field (§6.2).
func putCalcDepsTx(ctx context.Context, tx *sql.Tx, projectID, fieldID int64, refs []validate.Ref) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calculated_dependencies WHERE project_id = ? AND calculated_field_id = ?`,
		projectID, fieldID); err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calculated_dependencies
			 (project_id, calculated_field_id, ref_unique_event_name, ref_field_name)
			 VALUES (?, ?, ?, ?)`,
			projectID, fieldID, ref.Event, ref.Field); err != nil {
			return err
		}
	}
	return nil
}

// updateField changes any subset of a field's attributes (idempotent). A
// rename moves the stored values in the same transaction (REQ-VAL-014); a
// changed calculated expression recomputes every record in it (REQ-VAL-037).
// project_admin.
func (h *Handler) updateField(w http.ResponseWriter, r *http.Request) {
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
	in, ack, ok := h.decodeFieldInput(w, r)
	if !ok {
		return
	}

	// While a set is open either id may be provisional — an instrument or a field
	// created during staging exists only in the snapshot (REQ-API-107).
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
		if sf, found := staged.field(fieldID); !found || sf.InstrumentID != instID {
			errNotFound(w)
			return
		}
	}

	// One edit over a design snapshot. A rename stays a rename here: the staged
	// row keeps its id and changes name, which is what commit reads as "move the
	// stored values with it" rather than a delete plus an add (REQ-VAL-014).
	var edited db.Field
	apply := func(d *stagedDesign) *designError {
		sf, found := d.field(fieldID)
		if !found || sf.InstrumentID != instID {
			return validationf("field %d is not part of the staged design", fieldID)
		}
		next := sf.field(projectID)
		changes := in.apply(&next)
		renamed := in.FieldName != nil && *in.FieldName != sf.FieldName
		if renamed {
			next.FieldName = *in.FieldName
		}
		if len(changes) == 0 && !renamed {
			edited = next // the response reports the field as it stands
			return nil    // an idempotent repeat writes nothing, as on the live path
		}
		// A validation type that names a direct identifier presets the flag the way
		// AddField does, unless the request sets it explicitly.
		if in.DirectIdentifier == nil && !next.DirectIdentifier &&
			validate.PresetDirectIdentifier(next.ValidationType.String) {
			next.DirectIdentifier = true
		}
		if _, derr := h.validateFieldDesign(ctx, &next, fieldID, d.designContext(projectID)); derr != nil {
			return derr
		}
		if _, placed := d.putField(instID, next); !placed {
			return validationf("instrument %d is not part of the staged design", instID)
		}
		edited = next
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, ack, apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		writeJSON(w, http.StatusOK, newFieldObject(edited))
		return
	}

	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	old, err := h.Store.GetField(ctx, fieldID)
	if err != nil {
		errInternal(w)
		return
	}
	if old == nil || old.InstrumentID != instID {
		errNotFound(w)
		return
	}
	next := *old
	changes := in.apply(&next)
	var rename string
	if in.FieldName != nil && *in.FieldName != old.FieldName {
		rename = *in.FieldName
		changes["name"] = map[string]any{"old": old.FieldName, "new": rename}
		next.FieldName = rename
	}
	// A validation type that names a direct identifier presets the flag the
	// way AddField does, unless the request sets it explicitly.
	if in.DirectIdentifier == nil && !next.DirectIdentifier &&
		validate.PresetDirectIdentifier(next.ValidationType.String) {
		next.DirectIdentifier = true
		changes["direct_identifier"] = map[string]any{"old": 0, "new": 1}
	}
	calcChanged := next.FieldType == "calculated" && next.Calculation.String != old.Calculation.String
	if len(changes) > 0 {
		design, err := h.loadDesign(ctx, projectID)
		if err != nil {
			errInternal(w)
			return
		}
		refs, derr := h.validateFieldDesign(ctx, &next, fieldID, design)
		if derr != nil {
			writeDesignError(w, derr)
			return
		}
		tx, err := h.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			errInternal(w)
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx,
			`UPDATE fields SET field_name = ?, field_label = ?, field_type = ?,
				section_header = ?, choices = ?, field_note = ?,
				validation_type = ?, validation_format = ?, validation_min = ?, validation_max = ?,
				required = ?, branching_logic = ?, calculation = ?, matrix_group = ?,
				personal_information = ?, direct_identifier = ?
			 WHERE id = ?`,
			next.FieldName, nullStr(next.FieldLabel), next.FieldType,
			nullStr(next.SectionHeader), nullStr(next.Choices), nullStr(next.FieldNote),
			nullStr(next.ValidationType), nullStr(next.ValidationFormat), nullStr(next.ValidationMin), nullStr(next.ValidationMax),
			boolToIntParam(next.Required), nullStr(next.BranchingLogic), nullStr(next.Calculation), nullStr(next.MatrixGroup),
			boolToIntParam(next.PersonalInformation), boolToIntParam(next.DirectIdentifier), fieldID); err != nil {
			errInternal(w)
			return
		}
		if rename != "" {
			// The stored values and every inbound dependency reference follow
			// the new name atomically (REQ-VAL-014, DEV-VAL-4).
			if _, err := tx.ExecContext(ctx,
				`UPDATE data SET field_name = ? WHERE project_id = ? AND field_name = ?`,
				rename, projectID, old.FieldName); err != nil {
				errInternal(w)
				return
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE calculated_dependencies SET ref_field_name = ?
				 WHERE project_id = ? AND ref_field_name = ?`,
				rename, projectID, old.FieldName); err != nil {
				errInternal(w)
				return
			}
		}
		if calcChanged || rename != "" && next.FieldType == "calculated" {
			if err := putCalcDepsTx(ctx, tx, projectID, fieldID, refs); err != nil {
				errInternal(w)
				return
			}
		}
		if calcChanged {
			if err := h.recomputeCalcField(ctx, tx, u, projectID, next); err != nil {
				errInternal(w)
				return
			}
		}
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.FieldUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: projectID,
			Details: withBreakingAcknowledgement(map[string]any{
				"instrument": inst.Name, "field": next.FieldName, "changes": changes,
			}, breakingAcknowledged),
		}); err != nil {
			errInternal(w)
			return
		}
		if err := tx.Commit(); err != nil {
			errInternal(w)
			return
		}
		fresh, err := h.Store.GetField(ctx, fieldID)
		if err != nil {
			errInternal(w)
			return
		}
		if fresh != nil {
			next = *fresh
		}
	}
	writeJSON(w, http.StatusOK, newFieldObject(next))
}

// recomputeCalcField evaluates the field's expression for every record of
// the project at each event where its instrument is active, overwriting the
// stored values and auditing each change (§6.3 trigger row 2, ASM-VAL-4).
func (h *Handler) recomputeCalcField(ctx context.Context, tx *sql.Tx, u *db.User, projectID int64, f db.Field) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT e.unique_event_name
		 FROM events e JOIN instrument_events ie ON ie.event_id = e.id
		 WHERE ie.instrument_id = ? AND e.project_id = ?`, f.InstrumentID, projectID)
	if err != nil {
		return err
	}
	var activeEvents []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		activeEvents = append(activeEvents, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	recRows, err := tx.QueryContext(ctx,
		`SELECT record_id FROM record_entities WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	var records []string
	for recRows.Next() {
		var id string
		if err := recRows.Scan(&id); err != nil {
			recRows.Close()
			return err
		}
		records = append(records, id)
	}
	recRows.Close()
	if err := recRows.Err(); err != nil {
		return err
	}
	for _, recordID := range records {
		vals, err := loadRecordValues(ctx, tx, projectID, recordID)
		if err != nil {
			return err
		}
		for _, eventName := range activeEvents {
			value, _, err := validate.EvalCalc(f.Calculation.String, func(ref validate.Ref) (string, bool) {
				v, ok := vals[ref.Event+"|"+ref.Field]
				return v, ok && v != ""
			})
			if err != nil {
				return err
			}
			oldVal := vals[eventName+"|"+f.FieldName]
			if oldVal == value {
				continue
			}
			if err := upsertDataValueTx(ctx, tx, h.Store.Dialect, projectID, recordID, eventName, f.FieldName, value); err != nil {
				return err
			}
			if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
				EventType: audit.CalculatedRecomputed, Source: audit.SourceUI,
				UserID: u.ID, Email: u.Email, ProjectID: projectID, TargetRecord: recordID,
				Details: map[string]any{
					"record_id": recordID, "field": f.FieldName,
					"old": oldVal, "new": value,
					"trigger_field": nil, "trigger_event": nil,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadRecordValues loads one record's non-repeating values keyed by
// "event|field" for expression resolution.
func loadRecordValues(ctx context.Context, tx *sql.Tx, projectID int64, recordID string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT unique_event_name, field_name, value FROM data
		 WHERE project_id = ? AND record_id = ? AND repeating_instrument = ''`,
		projectID, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ev, fld, val string
		if err := rows.Scan(&ev, &fld, &val); err != nil {
			return nil, err
		}
		out[ev+"|"+fld] = val
	}
	return out, rows.Err()
}

// upsertDataValueTx writes one EAV value inside the caller's transaction;
// the conflict clause is dialect-specific (mirrors db.UpsertDataValue).
func upsertDataValueTx(ctx context.Context, tx *sql.Tx, dialect db.Dialect, projectID int64, recordID, event, field, value string) error {
	q := `INSERT INTO data (project_id, record_id, unique_event_name,
		repeating_instrument, repeating_instance_number, field_name, value)
		VALUES (?, ?, ?, '', 1, ?, ?)`
	if dialect == db.DialectMariaDB {
		q += ` ON DUPLICATE KEY UPDATE value = ?`
	} else {
		q += ` ON CONFLICT (project_id, record_id, unique_event_name,
			repeating_instrument, repeating_instance_number, field_name)
		 DO UPDATE SET value = ?`
	}
	_, err := tx.ExecContext(ctx, q, projectID, recordID, event, field, value, value)
	return err
}

// deleteField removes a field and its stored values (DEV-API-5). A field
// referenced by an active expression → 409 (§6.2 invariant). project_admin.
func (h *Handler) deleteField(w http.ResponseWriter, r *http.Request) {
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

	// A DELETE carries the acknowledgement in its body (§4.21 spells the retry out
	// as the same call returning with acknowledge_breaking): deleting a field is
	// breaking by construction, so without a way to acknowledge it an
	// analysis-mode project could never drop a field at all.
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}

	// While a set is open the field may be a staged one, and removing it edits the
	// snapshot — its stored values stay where they are until commit (REQ-API-107).
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
		if sf, found := staged.field(fieldID); !found || sf.InstrumentID != instID {
			errNotFound(w)
			return
		}
	}

	apply := func(d *stagedDesign) *designError {
		sf, found := d.field(fieldID)
		if !found || sf.InstrumentID != instID {
			return validationf("field %d is not part of the staged design", fieldID)
		}
		// The expression guard reads the design that is about to become active, so a
		// staged expression still naming this field blocks the removal (§6.2).
		if referenced, err := designReferencesField(d, sf.FieldName); err != nil {
			return &designError{status: http.StatusInternalServerError, msg: "internal"}
		} else if referenced {
			return conflictf("field is referenced by an active expression; update the expression first")
		}
		d.deleteField(fieldID)
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	f, err := h.Store.GetField(ctx, fieldID)
	if err != nil {
		errInternal(w)
		return
	}
	if f == nil || f.InstrumentID != instID {
		errNotFound(w)
		return
	}
	referenced, err := h.fieldReferencedByExpressions(ctx, projectID, f.FieldName)
	if err != nil {
		errInternal(w)
		return
	}
	if referenced {
		errConflict(w, "field is referenced by an active expression; update the expression first")
		return
	}
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	var removed int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM data WHERE project_id = ? AND field_name = ?`,
		projectID, f.FieldName).Scan(&removed); err != nil {
		errInternal(w)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM data WHERE project_id = ? AND field_name = ?`,
		projectID, f.FieldName); err != nil {
		errInternal(w)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calculated_dependencies WHERE project_id = ? AND calculated_field_id = ?`,
		projectID, fieldID); err != nil {
		errInternal(w)
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fields WHERE id = ?`, fieldID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.FieldDeleted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{
			"instrument": inst.Name, "field": f.FieldName, "values_removed": removed,
		}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fieldReferencedByExpressions reports whether any stored calculated or
// branching expression names the field. Stored expressions were validated
// at write time; a parse failure is skipped (it cannot name anything).
func (h *Handler) fieldReferencedByExpressions(ctx context.Context, projectID int64, fieldName string) (bool, error) {
	fields, err := h.Store.ListFields(ctx, projectID)
	if err != nil {
		return false, err
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		return false, err
	}
	namesRef := func(expr string) bool {
		refs, err := validate.ParseCalcExpression(expr)
		if err == nil {
			for _, r := range refs {
				if r.Field == fieldName {
					return true
				}
			}
			return false
		}
		brefs, berr := validate.ParseBranchingRefs(expr)
		if berr == nil {
			for _, r := range brefs {
				if r.Field == fieldName {
					return true
				}
			}
		}
		return false
	}
	for _, f := range fields {
		if f.Calculation.String != "" && namesRef(f.Calculation.String) {
			return true, nil
		}
		if f.BranchingLogic.String != "" && namesRef(f.BranchingLogic.String) {
			return true, nil
		}
	}
	for _, i := range instruments {
		if i.BranchingLogic.String != "" && namesRef(i.BranchingLogic.String) {
			return true, nil
		}
	}
	return false, nil
}

// orderFields writes the full field order of one instrument; the GD-8
// identifier invariant must survive. project_admin.
func (h *Handler) orderFields(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	instID, ok := pathObjectID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "order", "acknowledge_breaking") {
		return
	}
	var body struct {
		Order []int64 `json:"order"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}

	// While a set is open the instrument and its fields may both be staged, so the
	// order lands on the snapshot (REQ-API-107).
	staged, err := h.stagedRead(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if staged != nil {
		if _, found := h.stagedInstrumentOr404(w, staged, instID); !found {
			return
		}
	}

	apply := func(d *stagedDesign) *designError {
		if !d.orderFields(instID, body.Order) {
			return validationf("order must list every field of the instrument exactly once")
		}
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		// GD-8 is about stored records, so the live check applies unchanged — and it
		// applies when the instrument being ordered is itself staged.
		if set.isFirstInstrument(instID) && len(body.Order) > 0 {
			if bad, err := h.gd8Violation(ctx, projectID, body.Order[0]); err != nil {
				errInternal(w)
				return
			} else if bad {
				errBadRequest(w, "reordering would move the record identifier field (GD-8)")
				return
			}
		}
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	inst, err := h.Store.GetInstrument(ctx, instID)
	if err != nil {
		errInternal(w)
		return
	}
	if inst == nil || inst.ProjectID != projectID {
		errNotFound(w)
		return
	}
	fields, err := h.Store.ListFieldsByInstrument(ctx, projectID, instID)
	if err != nil {
		errInternal(w)
		return
	}
	member := map[int64]db.Field{}
	for _, f := range fields {
		member[f.ID] = f
	}
	seen := map[int64]bool{}
	for _, id := range body.Order {
		if _, ok := member[id]; !ok || seen[id] {
			errBadRequest(w, "order must list every field of the instrument exactly once")
			return
		}
		seen[id] = true
	}
	if len(body.Order) != len(fields) {
		errBadRequest(w, "order must list every field of the instrument exactly once")
		return
	}
	// GD-8 applies when this is the first instrument.
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if len(instruments) > 0 && instruments[0].ID == instID && len(body.Order) > 0 {
		if bad, err := h.gd8Violation(ctx, projectID, body.Order[0]); err != nil {
			errInternal(w)
			return
		} else if bad {
			errBadRequest(w, "reordering would move the record identifier field (GD-8)")
			return
		}
	}
	names := make([]string, 0, len(body.Order))
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	// Two-phase write: fields carry UNIQUE(project_id, instrument_id,
	// position), so intermediate positions are parked negative first.
	for i, id := range body.Order {
		if _, err := tx.ExecContext(ctx, `UPDATE fields SET position = ? WHERE id = ?`, -(i + 1), id); err != nil {
			errInternal(w)
			return
		}
	}
	for i, id := range body.Order {
		if _, err := tx.ExecContext(ctx, `UPDATE fields SET position = ? WHERE id = ?`, i+1, id); err != nil {
			errInternal(w)
			return
		}
		names = append(names, member[id].FieldName)
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.FieldReordered, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{"instrument": inst.Name, "order": names}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- validation types (§4.11, REQ-API-104) ---

// builtinTypeObjects are the four built-in structured types with their §4
// grammars; date and datetime have no single pattern (the format is per
// field), so their advisory regex is empty.
var builtinTypeObjects = []db.ValidationType{
	{Name: "integer", Regex: `^-?[0-9]+$`, Builtin: true},
	{Name: "floating point", Regex: `^[+-]?[0-9]+(\.[0-9]+)?$`, Builtin: true},
	{Name: "date", Regex: "", Builtin: true},
	{Name: "datetime", Regex: "", Builtin: true},
}

type validationTypeObject struct {
	Name    string `json:"name"`
	Regex   string `json:"regex"`
	Builtin bool   `json:"builtin"`
}

// validationTypes lists the assignable types for the designer — any
// authenticated user, read-only (REQ-API-104).
func (h *Handler) validationTypes(w http.ResponseWriter, r *http.Request) {
	if _, ok := actor(r); !ok {
		errForbidden(w)
		return
	}
	rows, err := h.Store.ListValidationTypes(r.Context())
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]validationTypeObject, 0, len(builtinTypeObjects)+len(rows))
	seen := map[string]bool{}
	for _, t := range builtinTypeObjects {
		seen[t.Name] = true
		out = append(out, validationTypeObject{Name: t.Name, Regex: t.Regex, Builtin: true})
	}
	for _, t := range rows {
		if seen[t.Name] {
			continue // a registry row may never shadow a built-in (§4.2)
		}
		seen[t.Name] = true
		out = append(out, validationTypeObject{Name: t.Name, Regex: t.Regex, Builtin: t.Builtin})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- design-time test action (§4.11, REQ-API-096) ---

// testField evaluates a calculated field's expression (or a supplied draft)
// against one record and returns the value with explicit problem flags
// (§6.4). It stores nothing. project_admin + record visibility (REQ-AUTH-045).
func (h *Handler) testField(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	fieldID, ok := pathObjectID(r, "fid")
	if !ok {
		errBadRequest(w, "invalid field id")
		return
	}
	recordID := r.PathValue("record")
	ctx := r.Context()
	f, err := h.Store.GetField(ctx, fieldID)
	if err != nil {
		errInternal(w)
		return
	}
	if f == nil || f.ProjectID != projectID {
		errNotFound(w)
		return
	}
	if f.FieldType != "calculated" {
		errBadRequest(w, "field is not a calculated field")
		return
	}
	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !visible {
		errForbidden(w)
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "expression") {
		return
	}
	expr := f.Calculation.String
	if raw, present := supplied["expression"]; present {
		if err := json.Unmarshal(raw, &expr); err != nil || expr == "" {
			errBadRequest(w, "expression must be a non-empty string")
			return
		}
	}
	// Draft expressions are checked like stored ones before evaluation.
	d, err := h.loadDesign(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if _, derr := d.validateCalc(expr, f.ID); derr != nil {
		writeDesignError(w, derr)
		return
	}
	vals, err := loadRecordValuesRead(ctx, h.Store.DB, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	value, problems, err := validate.EvalCalc(expr, func(ref validate.Ref) (string, bool) {
		v, ok := vals[ref.Event+"|"+ref.Field]
		return v, ok && v != ""
	})
	if err != nil {
		errValidation(w, "expression is not well-formed: "+err.Error())
		return
	}
	out := struct {
		Value    string `json:"value"`
		Problems []struct {
			Operand string `json:"operand"`
			Problem string `json:"problem"`
		} `json:"problems"`
	}{Value: value, Problems: []struct {
		Operand string `json:"operand"`
		Problem string `json:"problem"`
	}{}}
	for _, p := range problems {
		out.Problems = append(out.Problems, struct {
			Operand string `json:"operand"`
			Problem string `json:"problem"`
		}{Operand: p.Operand, Problem: string(p.Problem)})
	}
	writeJSON(w, http.StatusOK, out)
}

// loadRecordValuesRead is the non-transactional twin of loadRecordValues.
func loadRecordValuesRead(ctx context.Context, sqlDB *sql.DB, projectID int64, recordID string) (map[string]string, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT unique_event_name, field_name, value FROM data
		 WHERE project_id = ? AND record_id = ? AND repeating_instrument = ''`,
		projectID, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ev, fld, val string
		if err := rows.Scan(&ev, &fld, &val); err != nil {
			return nil, err
		}
		out[ev+"|"+fld] = val
	}
	return out, rows.Err()
}

// --- instrument–event mapping (§4.12) ---

type armMappingObject struct {
	ArmNum  int                 `json:"arm_num"`
	Mapping map[string][]string `json:"mapping"`
}

// getMapping returns the instrument × event matrix per arm (checked state,
// REQ-DB-012). Data access ≥ read_only.
func (h *Handler) getMapping(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	ctx := r.Context()
	if staged, err := h.stagedRead(ctx, projectID); err != nil {
		errInternal(w)
		return
	} else if staged != nil {
		writeJSON(w, http.StatusOK, staged.mappingObjects())
		return
	}
	arms, eventsByArm, err := h.CanonicalEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	checked := map[[2]int64]bool{}
	for _, p := range pairs {
		checked[[2]int64{p.InstrumentID, p.EventID}] = true
	}
	// Event ids per arm in canonical order.
	eventIDsByArm := map[int][]struct {
		id     int64
		unique string
	}{}
	for _, a := range arms {
		for _, e := range eventsByArm[a.ArmNum] {
			eventIDsByArm[a.ArmNum] = append(eventIDsByArm[a.ArmNum], struct {
				id     int64
				unique string
			}{e.ID, e.UniqueEventName})
		}
	}
	out := make([]armMappingObject, 0, len(arms))
	for _, a := range arms {
		mapping := make(map[string][]string, len(instruments))
		for _, i := range instruments {
			list := []string{}
			for _, e := range eventIDsByArm[a.ArmNum] {
				if checked[[2]int64{i.ID, e.id}] {
					list = append(list, e.unique)
				}
			}
			mapping[i.Name] = list
		}
		out = append(out, armMappingObject{ArmNum: a.ArmNum, Mapping: mapping})
	}
	writeJSON(w, http.StatusOK, out)
}

// putMapping replaces one arm's instrument–event matrix (idempotent). An
// unknown instrument or event name → 400 invalid_request. project_admin.
func (h *Handler) putMapping(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	ctx := r.Context()
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "arm_num", "mapping", "acknowledge_breaking") {
		return
	}
	var body struct {
		ArmNum  *int                `json:"arm_num"`
		Mapping map[string][]string `json:"mapping"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil || body.ArmNum == nil {
		errBadRequest(w, "arm_num and mapping are required")
		return
	}

	// The same replace over a design snapshot: while a set is open the matrix names
	// the staged instruments and events, which may have no live row yet. Unknown
	// names are rejected here — the mutator would drop them silently, and a matrix
	// that quietly lost a row would commit as a partial answer (REQ-API-073).
	apply := func(d *stagedDesign) *designError {
		sa, found := d.arm(*body.ArmNum)
		if !found {
			return validationf("unknown arm number")
		}
		for name, eventNames := range body.Mapping {
			if _, ok := d.instrumentByName(name); !ok {
				return validationf("unknown instrument name: %s", name)
			}
			for _, evName := range eventNames {
				if _, ok := eventInArm(*sa, evName); !ok {
					return validationf("unknown event name for arm %d: %s", *body.ArmNum, evName)
				}
			}
		}
		d.setMappingForArm(*body.ArmNum, body.Mapping)
		return nil
	}
	target, set, breakingAcknowledged, ok := h.structureWrite(w, r, projectID, acknowledgeFrom(supplied), apply)
	if !ok {
		return
	}
	if target == writeStaged {
		if !h.applyStagedChange(w, r, projectID, set, apply) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	arm, err := h.Store.GetArmByNum(ctx, projectID, *body.ArmNum)
	if err != nil {
		errInternal(w)
		return
	}
	if arm == nil {
		errBadRequest(w, "unknown arm number")
		return
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	byName := map[string]int64{}
	for _, i := range instruments {
		byName[i.Name] = i.ID
	}
	events, err := h.Store.ListEventsByArm(ctx, projectID, arm.ID)
	if err != nil {
		errInternal(w)
		return
	}
	armEvent := map[string]db.Event{}
	for _, e := range events {
		armEvent[e.UniqueEventName] = e
	}
	var pairs []db.InstrumentEvent
	for name, eventNames := range body.Mapping {
		instID, ok := byName[name]
		if !ok {
			errBadRequest(w, "unknown instrument name: "+name)
			return
		}
		for _, evName := range eventNames {
			ev, ok := armEvent[evName]
			if !ok {
				errBadRequest(w, "unknown event name for arm "+strconv.Itoa(arm.ArmNum)+": "+evName)
				return
			}
			pairs = append(pairs, db.InstrumentEvent{InstrumentID: instID, EventID: ev.ID})
		}
	}
	oldPairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.SetInstrumentEventsForArm(ctx, projectID, arm.ID, pairs); err != nil {
		errInternal(w)
		return
	}
	// Audit the changed instruments with their full per-arm checked state
	// (1/0 over the arm's events), §3.3 mapping_updated shape.
	oldSet := map[[2]int64]bool{}
	newSet := map[[2]int64]bool{}
	for _, p := range oldPairs {
		oldSet[[2]int64{p.InstrumentID, p.EventID}] = true
	}
	for _, p := range pairs {
		newSet[[2]int64{p.InstrumentID, p.EventID}] = true
	}
	pairsAudit := map[string]map[string]int{}
	for name, id := range byName {
		changed := false
		state := map[string]int{}
		for _, e := range events {
			was, now := oldSet[[2]int64{id, e.ID}], newSet[[2]int64{id, e.ID}]
			if was != now {
				changed = true
			}
			v := 0
			if now {
				v = 1
			}
			state[e.UniqueEventName] = v
		}
		if changed {
			pairsAudit[name] = state
		}
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.MappingUpdated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: withBreakingAcknowledgement(map[string]any{
			"arm_num": arm.ArmNum, "pairs": pairsAudit,
		}, breakingAcknowledged),
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
