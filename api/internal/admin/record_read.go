package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
)

// The audited record read (§4.28, REQ-API-167): the web interface's record view
// asks for one entry of its own before it shows a record's values, so "who
// opened this record" sits in the trail beside "who pulled it" (REQ-AUD-035).
// It is a write of an audit entry and nothing else — it returns no data — which
// is why it lives beside the completion endpoint rather than with the read-only
// queries of queries.go.

func (h *Handler) registerRecordRead(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/records/{record}/read", h.recordRead)
}

// recordReadDetails is the §3.9 payload: what the page displayed, never a value
// (REQ-AUD-014). Instance names a further instance only, exactly as §3.2 does.
type recordReadDetails struct {
	Event      string `json:"event,omitempty"`
	Instrument string `json:"instrument,omitempty"`
	Instance   int    `json:"instance,omitempty"`
}

// recordRead writes one record_read entry for the acting account (REQ-AUD-035)
// and answers {"ok": true}. PHP calls it while rendering the record view and
// sends the form only once this call succeeded, so a read that cannot be
// recorded is never displayed — the order token_viewed keeps for a token value
// (REQ-AUD-033, User_Interface_Design.md §8.3).
//
// The gate is the record history read's own (§4.16): data access ≥ read_only on
// the record's arm + project visibility (REQ-API-007) + record visibility under
// the data access group rule (REQ-AUTH-045). Every denial writes nothing, and a
// record that does not exist is 404 with nothing written — it discloses no
// values, which is why PHP does not call this endpoint for the new-record form
// (REQ-AUD-004).
func (h *Handler) recordRead(w http.ResponseWriter, r *http.Request) {
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

	// The body carries nothing: empty or {} (REQ-API-167).
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied) {
		return
	}

	// The displayed triple is recorded, not resolved against the design: a stale
	// selection still records the read rather than losing it (REQ-API-167).
	q := r.URL.Query()
	event, instrument := q.Get("event"), q.Get("instrument")
	if !readNameOK(event) || !readNameOK(instrument) {
		errBadRequest(w, "event and instrument must be names of at most 255 characters without control characters")
		return
	}
	instance := 0
	if v := q.Get("instance"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errBadRequest(w, "instance must be a positive integer")
			return
		}
		instance = n
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
	asg, err := h.Store.GetAssignment(ctx, u.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	lv, err := authz.EffectiveForAssignment(ctx, h.Store, u, projectID, asg)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireMember(w, r, lv) {
		return
	}
	if !u.IsAdmin && !lv.AnyData(LvlReadOnly) {
		errForbidden(w)
		return
	}
	visible, err := authz.RecordVisibleForAssignment(ctx, h.Store, u, asg, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !visible { // uniform 403 — never discloses the record (REQ-API-007)
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

	// source=ui and no token: a browser read has none, which is the whole reason
	// this is an event rather than an audit_record_views row (REQ-AUD-017/018,
	// DEV-AUD-6). A failed write answers 500 with nothing displayed.
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.RecordRead, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		TargetRecord: recordID,
		Details:      recordReadDetails{Event: event, Instrument: instrument, Instance: furtherInstance(instance)},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// readNameOK accepts an unset name (a page that rendered no selection) and any
// name of at most 255 characters without control characters (REQ-API-167). The
// value is never looked up, so nothing here resolves against the design.
func readNameOK(v string) bool {
	if len(v) > 255 {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
