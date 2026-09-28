package admin

import (
	"encoding/json"
	"net/http"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// Completion states of the record-status dashboard (§4.13, REQ-API-074).
// no_data and some_data are derived from the stored values on every read;
// finished is the one state a user assigns and the only one stored
// (REQ-DB-036), so an instrument can never display a completion the data
// contradicts.
const (
	StateNoData   = "no_data"
	StateSomeData = "some_data"
	StateFinished = "finished"

	// StateUnfinished is the clear verb of the PUT body, never a stored or
	// returned state: §8.5 folds the dashboard's "no data" and "some data"
	// choices into one action, after which the read re-derives which of the
	// two applies.
	StateUnfinished = "unfinished"
)

// registerCompletion mounts the completion write endpoint of §4.13
// (REQ-API-110). The read side lives with the dashboard in queries.go, which
// stays free of writes.
func (h *Handler) registerCompletion(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/projects/{id}/records/{record}/events/{event}/instruments/{iid}/completion",
		h.putCompletion)
}

// completionObject is the §4.13 response: the resulting stored state —
// "finished" after a set, "unfinished" after a clear (the cell then reads as
// its derived no_data/some_data on the next record-status call).
type completionObject struct {
	State string `json:"state"`
}

// completionDetails is the audit payload of instrument_completed /
// instrument_uncompleted (Audit_Logging_Design.md §3.2). It names the cell and
// nothing else — no field values, because the call changes none (REQ-AUD-026).
type completionDetails struct {
	RecordID   string `json:"record_id"`
	Event      string `json:"event"`
	Instrument string `json:"instrument"`
}

// putCompletion sets or clears the user-assigned "finished" state of one
// (record, event, instrument) — the control at the end of a data-collection
// instrument's form (REQ-API-110, UI §8.5).
//
// The event path segment is its unique_event_name, the identifier record-status
// returns and the dashboard binds to; {iid} is the instrument id, as in every
// other instrument route. Rules enforced:
//   - data access ≥ view_edit on the arm the event belongs to (REQ-API-110),
//     project visibility (REQ-API-007) and record visibility under the DAG rule
//     (REQ-AUTH-045) — the uniform 403 in both visibility cases;
//   - an unknown record, event or instrument is 404; an (event, instrument) pair
//     not mapped in the active design is 409, and so is a survey-marked
//     instrument, which takes no assignment (GD-9);
//   - idempotent (REQ-API-042): re-setting a finished cell answers 200 with no
//     second write and no second audit entry, as the mode endpoint does;
//   - no field value is written, so validation and the analysis-mode write rules
//     for values do not apply here (GD-20 scopes those to imports).
func (h *Handler) putCompletion(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	ctx := r.Context()
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return
	}
	recordID := r.PathValue("record")
	if recordID == "" {
		errBadRequest(w, "record id is required")
		return
	}
	eventName := r.PathValue("event")
	if eventName == "" {
		errBadRequest(w, "event is required")
		return
	}
	instrumentID, ok := pathID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return
	}

	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "state") {
		return
	}
	rawState, present := supplied["state"]
	if !present {
		errBadRequest(w, `state is required ("finished" or "unfinished")`)
		return
	}
	var want string
	if err := json.Unmarshal(rawState, &want); err != nil ||
		(want != StateFinished && want != StateUnfinished) {
		errBadRequest(w, `state must be "finished" or "unfinished"`)
		return
	}

	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if p == nil {
		errNotFound(w)
		return
	}
	lv, err := h.access(ctx, u, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireMember(w, r, lv) {
		return
	}
	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !visible { // uniform 403 — never discloses the record (REQ-API-007)
		errForbidden(w)
		return
	}

	// The event fixes the arm the view_edit level is checked against
	// (REQ-API-110), so it resolves before anything else does.
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	var event *db.Event
	for i := range events {
		if events[i].UniqueEventName == eventName {
			event = &events[i]
			break
		}
	}
	if event == nil {
		errNotFound(w)
		return
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	armNum := 0
	for _, a := range arms {
		if a.ID == event.ArmID {
			armNum = a.ArmNum
			break
		}
	}
	if !lv.HasData(armNum, LvlViewEdit) {
		errForbidden(w)
		return
	}

	exists, err := h.Store.RecordExists(ctx, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !exists {
		errNotFound(w)
		return
	}
	instrument, err := h.Store.GetInstrument(ctx, instrumentID)
	if err != nil {
		errInternal(w)
		return
	}
	if instrument == nil || instrument.ProjectID != projectID {
		errNotFound(w)
		return
	}

	// The cell must exist in the active design, and a survey instrument never
	// takes an assignment (REQ-DB-036, GD-9).
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	mapped := false
	for _, ie := range pairs {
		if ie.EventID == event.ID && ie.InstrumentID == instrumentID {
			mapped = true
			break
		}
	}
	if !mapped {
		errConflict(w, "instrument "+instrument.Name+" is not mapped to event "+eventName)
		return
	}
	if instrument.IsSurvey {
		errConflict(w, "a survey instrument takes no completion assignment")
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	var changed bool
	eventType := audit.InstrumentCompleted
	if want == StateFinished {
		changed, err = h.Store.SetInstrumentCompletionTx(ctx, tx, projectID, recordID,
			event.ID, instrumentID, u.ID)
	} else {
		eventType = audit.InstrumentUncompleted
		changed, err = h.Store.ClearInstrumentCompletionTx(ctx, tx, projectID, recordID,
			event.ID, instrumentID)
	}
	if err != nil {
		errInternal(w)
		return
	}
	// A call that changed nothing writes no audit entry (REQ-API-042 — the same
	// reading the mode endpoint applies to a PUT naming the current mode).
	if changed {
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: eventType, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: projectID,
			ArmNum: armNum, TargetRecord: recordID,
			Details: completionDetails{RecordID: recordID, Event: eventName, Instrument: instrument.Name},
		}); err != nil {
			errInternal(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, completionObject{State: want})
}
