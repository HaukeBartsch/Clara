package admin

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// registerSurvey mounts §4.17: reporting, issuing and revoking the public survey
// link of a (record, instrument, event) triple (REQ-API-082/085, REQ-API-145/146).
// The link is what the PHP survey page carries; the browser never calls
// /api/v1/* from it (GD-1, REQ-API-084), and what the link token may do on the
// data API is fixed by §3.10 — once per link.
func (h *Handler) registerSurvey(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.getSurveyLink)
	mux.HandleFunc("POST /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.postSurveyLink)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.deleteSurveyLink)
}

// The four states of a pair's link (§4.17). They are read off the one row, not
// stored: `revoked` and `collected_at` together say everything, and issuing
// clears both (db.IssueSurveyLinkTokenTx), so no fifth state can appear.
const (
	surveyStateNone      = "none"
	surveyStateLive      = "live"
	surveyStateSubmitted = "submitted"
	surveyStateRevoked   = "revoked"
)

// surveyLinkObject is the §4.17 response: the link's state, the public URL —
// present only while it is live, because handing out a spent token's URL would
// only send a respondent to a 410 — the event it belongs to, and the collection
// date once the response arrived (REQ-DB-041, REQ-API-145). The token itself is
// not returned bare: the URL is the contract the caller renders.
type surveyLinkObject struct {
	State       string  `json:"state"`
	URL         string  `json:"url"`
	Event       string  `json:"event"`
	CollectedAt *string `json:"collected_at"` // null until a response arrived (REQ-DB-041)
}

// surveyLinkReport renders one row as the §4.17 object. A row that carries a
// collection date reports `submitted` even when it was revoked afterwards: the
// date is the fact the member acts on, and both states offer the same two
// actions (a fresh link, nothing to copy). The URL comes from h.surveyURL, so
// the value handed to "copy link" and the route that serves it cannot drift
// apart (User_Interface_Design.md §8.8).
func (h *Handler) surveyLinkReport(link *db.SurveyLink, eventName string) surveyLinkObject {
	if link == nil {
		return surveyLinkObject{State: surveyStateNone, Event: eventName}
	}
	state := surveyStateLive
	switch {
	case link.CollectedAt.Valid:
		state = surveyStateSubmitted
	case link.Revoked:
		state = surveyStateRevoked
	}
	url := ""
	if state == surveyStateLive {
		url = h.surveyURL(link.Token)
	}
	var collected *string
	if link.CollectedAt.Valid {
		v := link.CollectedAt.String
		collected = &v
	}
	return surveyLinkObject{State: state, URL: url, Event: eventName, CollectedAt: collected}
}

// surveyLinkContext resolves everything the three §4.17 endpoints share: the
// actor, the project, membership, the event named by `?event=`, record visibility
// (uniform 403, REQ-AUTH-045), data access ≥ view_edit **on that pair**
// (REQ-API-082, REQ-AUTH-069), an existing record and instrument belonging to the
// project (404), and the survey marking plus the mapping — a link for a
// data-collection instrument, or for a pair the design does not map, is an
// invalid request rather than a conflict (§4.2). The instrument and the event id
// are returned for the caller to address the one link they named (REQ-AUTH-039).
func (h *Handler) surveyLinkContext(w http.ResponseWriter, r *http.Request) (*db.User, *db.Instrument, int64, string, int64, bool) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok {
		return nil, nil, 0, "", 0, false
	}
	if !h.requireMember(w, r, lv) {
		return nil, nil, 0, "", 0, false
	}
	recordID := r.PathValue("record")
	if recordID == "" {
		errBadRequest(w, "record id is required")
		return nil, nil, 0, "", 0, false
	}
	instrumentID, ok := pathID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return nil, nil, 0, "", 0, false
	}
	ctx := r.Context()

	// The event is part of the link's identity: an instrument mapped to several
	// events has one distinct link per event (REQ-AUTH-039). It is therefore
	// required — and a project without events can hold no link at all, which is
	// the precondition this revision states out loud (DEV-DB-14).
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", 0, false
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", 0, false
	}
	var (
		eventName = r.URL.Query().Get("event")
		eventID   int64
	)
	if eventName != "" {
		found := false
		for _, e := range events {
			if e.UniqueEventName == eventName {
				eventID, found = e.ID, true
				break
			}
		}
		if !found {
			errBadRequest(w, "unknown event: "+eventName)
			return nil, nil, 0, "", 0, false
		}
	} else {
		errBadRequest(w, "event is required: a survey link belongs to one event")
		return nil, nil, 0, "", 0, false
	}

	// The gate is the pair's own level, not the arm's: view_edit on this
	// instrument at this event (REQ-AUTH-069), with the arm default behind the
	// grant a role never overrode.
	if !u.IsAdmin {
		g := lv.PairOrArmDefault(eventID, instrumentID, eventArmOf(events, arms)[eventID])
		if authz.DataRank(g.Data) < authz.RankViewEdit {
			errForbidden(w)
			return nil, nil, 0, "", 0, false
		}
	}

	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", 0, false
	}
	if !visible {
		errForbidden(w)
		return nil, nil, 0, "", 0, false
	}
	exists, err := h.Store.RecordExists(ctx, projectID, recordID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", 0, false
	}
	if !exists {
		errNotFound(w)
		return nil, nil, 0, "", 0, false
	}
	instrument, err := h.Store.GetInstrument(ctx, instrumentID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", 0, false
	}
	if instrument == nil || instrument.ProjectID != projectID {
		errNotFound(w)
		return nil, nil, 0, "", 0, false
	}
	if !instrument.IsSurvey {
		errBadRequest(w, "the instrument is not survey-marked")
		return nil, nil, 0, "", 0, false
	}
	if eventID != 0 {
		mapped := false
		pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
		if err != nil {
			errInternal(w)
			return nil, nil, 0, "", 0, false
		}
		for _, p := range pairs {
			if p.InstrumentID == instrumentID && p.EventID == eventID {
				mapped = true
				break
			}
		}
		if !mapped {
			errBadRequest(w, "the instrument is not mapped to event "+eventName)
			return nil, nil, 0, "", 0, false
		}
	}
	return u, instrument, projectID, recordID, eventID, true
}

// getSurveyLink reports the link of a (record, instrument, event) and its state
// (REQ-API-082 as revised): it never mints a token, because issuing is what
// consumes a live link and invalidates its URL — a read that did that would make
// the record view destroy links by rendering them. Reporting writes no audit
// entry; issuance and revocation do (REQ-API-043).
func (h *Handler) getSurveyLink(w http.ResponseWriter, r *http.Request) {
	_, instrument, projectID, recordID, eventID, ok := h.surveyLinkContext(w, r)
	if !ok {
		return
	}
	link, err := h.Store.GetSurveyLink(r.Context(), projectID, recordID, instrument.ID, eventID)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.surveyLinkReport(link, r.URL.Query().Get("event")))
}

// postSurveyLink issues a fresh token for the pair and answers the object above
// with state `live` (REQ-API-146). It replaces any live link of that pair, whose
// URL admits nothing from its next call: "get another link" means the previous one
// is no longer the way in. Issuing onto a survey that still holds answers is
// refused — see the value check below — and both outcomes keep their audit entry
// only on success (REQ-API-043).
func (h *Handler) postSurveyLink(w http.ResponseWriter, r *http.Request) {
	u, instrument, projectID, recordID, eventID, ok := h.surveyLinkContext(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	eventName := r.URL.Query().Get("event")

	// The fields whose stored values mean "this survey has answers" — the
	// instrument's own, minus the GD-8 record identifier, which every record
	// stores and whose presence would refuse every re-issue (REQ-API-146).
	fields, err := h.surveyValueFields(ctx, projectID, instrument.ID)
	if err != nil {
		errInternal(w)
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	// A second fill starts from nothing, so the answers collected through the old
	// link go first: the caller clears them with the delete action of §3.8, which
	// needs the delete right (REQ-API-036). The check is named rather than
	// demanded up front — a member without that right is told what to ask for, not
	// met with a bare 403 on an endpoint they may use. It runs inside this
	// transaction so a submission arriving between the read and the issue cannot
	// slip its values under a link minted over them.
	stored, err := h.Store.InstrumentHasStoredValueTx(ctx, tx, projectID, recordID, eventName, fields)
	if err != nil {
		errInternal(w)
		return
	}
	if stored {
		errConflict(w, "this instrument already holds values for this record at this event — "+
			"delete this instrument's values before issuing a new survey link")
		return
	}

	fresh := &db.SurveyLink{
		ProjectID: projectID, RecordID: recordID, InstrumentID: instrument.ID,
		EventID: eventID, CreatedBy: sql.NullInt64{Int64: u.ID, Valid: true},
	}
	if err := h.Store.IssueSurveyLinkTokenTx(ctx, tx, fresh); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.SurveyLinkIssued, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID, TargetRecord: recordID,
		Details: map[string]any{"record_id": recordID, "instrument": instrument.Name,
			"event": eventName},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, surveyLinkObject{
		State: surveyStateLive, URL: h.surveyURL(fresh.Token), Event: eventName,
	})
}

// surveyValueFields lists the fields whose non-empty stored values make a survey
// instrument hold answers for one record at one event (REQ-API-146). Two kinds of
// field are left out: the GD-8 record identifier, a stored value of every record
// that would otherwise refuse every re-issue, and the two presentation-only types
// (`description`, `header`), which store nothing — the same exclusion the derived
// "has data" test makes (REQ-API-134).
func (h *Handler) surveyValueFields(ctx context.Context, projectID, instrumentID int64) ([]string, error) {
	fields, err := h.Store.ListFieldsByInstrument(ctx, projectID, instrumentID)
	if err != nil {
		return nil, err
	}
	identifierID, err := h.recordIdentifierField(ctx, projectID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.ID == identifierID || f.FieldType == "description" || f.FieldType == "header" {
			continue
		}
		names = append(names, f.FieldName)
	}
	return names, nil
}

// deleteSurveyLink revokes the link of one pair; the revocation takes effect on
// the next data-API call with that token (REQ-AUTH-040), and the same
// instrument's links at other events stay valid (REQ-API-085). 204 whether or not
// it was already revoked, and only an actual revocation is audit-logged
// (REQ-API-042).
func (h *Handler) deleteSurveyLink(w http.ResponseWriter, r *http.Request) {
	u, instrument, projectID, recordID, eventID, ok := h.surveyLinkContext(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	link, err := h.Store.GetSurveyLink(ctx, projectID, recordID, instrument.ID, eventID)
	if err != nil {
		errInternal(w)
		return
	}
	if link == nil {
		errNotFound(w)
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	revoked, err := h.Store.RevokeSurveyLinkTx(ctx, tx, projectID, recordID, instrument.ID, eventID)
	if err != nil {
		errInternal(w)
		return
	}
	if revoked {
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.SurveyLinkRevoked, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: projectID, TargetRecord: recordID,
			Details: map[string]any{"record_id": recordID, "instrument": instrument.Name,
				"event": r.URL.Query().Get("event")},
		}); err != nil {
			errInternal(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// surveyURL renders the public link from the configured base — the vendored
// front-controller route of §8.8 in User_Interface_Design.md, with no session
// and no login in front of it (GD-9).
func (h *Handler) surveyURL(token string) string {
	return strings.TrimSuffix(h.Cfg.WebPublicURL, "/") + "/s/" + token
}
