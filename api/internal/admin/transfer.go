package admin

import (
	"errors"
	"net/http"

	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
	"csms/api/internal/db"
)

// Cross-project transfers (§4.23, REQ-API-165/166): the routes in front of the
// engine in dataapi/transfer.go. They mount on the administration API because
// a transfer needs the acting account to authorize against a second project —
// which the data API's single-project token can never carry. The PHP layer
// proxies the data-entry button here (REQ-UI-002); the browser never calls
// these routes directly.

func (h *Handler) registerTransfer(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/records/{record}/transfers", h.postTransfer)
	mux.HandleFunc("GET /api/v1/projects/{id}/records/{record}/transfers", h.getTransfers)
}

// transferSourceGate is the source-side gate both §4.23 endpoints share: an
// acting account, and the uniform 403 for a project or record the actor may
// not see (REQ-API-007, REQ-AUTH-045). Everything beyond visibility of the
// source — every right on the target — is the engine's question, re-asked at
// execution time; this gate never answers for it.
func (h *Handler) transferSourceGate(w http.ResponseWriter, r *http.Request) (*db.User, int64, bool) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return nil, 0, false
	}
	ctx := r.Context()
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return nil, 0, false
	}
	if r.PathValue("record") == "" {
		errBadRequest(w, "record id is required")
		return nil, 0, false
	}
	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return nil, 0, false
	}
	if p == nil {
		errNotFound(w)
		return nil, 0, false
	}
	lv, err := h.access(ctx, u, projectID)
	if err != nil {
		errInternal(w)
		return nil, 0, false
	}
	if !h.requireMember(w, r, lv) {
		return nil, 0, false
	}
	recordID := r.PathValue("record")
	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return nil, 0, false
	}
	if !visible { // uniform 403 — never discloses the record (REQ-API-007)
		errForbidden(w)
		return nil, 0, false
	}
	return u, projectID, true
}

// postTransfer executes one transfer field of the addressed record
// (REQ-API-165): parameters event and instrument name the field's context,
// field names the transfer field itself. The engine runs the whole operation
// in one transaction — target authorization included — so this route only
// translates its errors: a denied or missing target is the uniform 403 that
// discloses nothing (REQ-API-007), an unexecutable design or rejecting value is
// validation_error with the detail the designer needs, anything else is ours.
func (h *Handler) postTransfer(w http.ResponseWriter, r *http.Request) {
	u, projectID, ok := h.transferSourceGate(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	event := q.Get("event")
	instrument := q.Get("instrument")
	field := q.Get("field")
	if instrument == "" || field == "" {
		errBadRequest(w, "instrument and field parameters are required")
		return
	}
	res, err := h.Data.ExecuteTransfer(r.Context(), u, projectID, r.PathValue("record"), event, instrument, field)
	if err != nil {
		var ce *dataapi.TransferConfigError
		switch {
		case errors.Is(err, dataapi.ErrTransferTargetAccess):
			errForbidden(w)
		case errors.As(err, &ce):
			errValidation(w, ce.Msg)
		default:
			errInternal(w)
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// getTransfers answers the availability probe of REQ-API-166 for every transfer
// field of the addressed instrument: {transfers: [{field_name, enabled}]}.
// The event parameter is accepted for symmetry with the POST — availability is
// the target's and the actor's, not the event context's — and the executing
// call validates it. Nothing here writes, and an unavailable transfer discloses
// no reason: the button is simply absent (REQ-UI-065).
func (h *Handler) getTransfers(w http.ResponseWriter, r *http.Request) {
	u, projectID, ok := h.transferSourceGate(w, r)
	if !ok {
		return
	}
	instrument := r.URL.Query().Get("instrument")
	if instrument == "" {
		errBadRequest(w, "instrument parameter is required")
		return
	}
	statuses, err := h.Data.TransferAvailability(r.Context(), u, projectID, instrument)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": statuses})
}
