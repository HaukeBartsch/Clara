package admin

import (
	"database/sql"
	"net/http"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// registerSurvey mounts §4.17: issuing and revoking the public survey link of a
// (record, instrument, event) triple (REQ-API-082…085). The link is what the PHP
// survey page carries; the browser never calls /api/v1/* from it (GD-1,
// REQ-API-084), and what the link token may do on the data API is fixed by §3.10.
func (h *Handler) registerSurvey(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.getSurveyLink)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.deleteSurveyLink)
}

// surveyLinkObject is the §4.17 response: the public URL carrying the link
// token, the event it belongs to, whether it still works, and whether the survey
// has been collected through it (REQ-DB-041). The token itself is not returned
// bare — the URL is the contract the caller renders.
type surveyLinkObject struct {
	URL         string `json:"url"`
	Event       string `json:"event"`
	Revoked     bool   `json:"revoked"`
	CollectedAt string `json:"collected_at"` // "" = not collected yet
}

// surveyLinkContext resolves everything both §4.17 endpoints share: the actor,
// the project, membership, the event named by `?event=`, record visibility
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

// getSurveyLink returns the stable public link for a (record, instrument, event),
// issuing one on first call (REQ-API-082). Repeat calls return the same URL and
// write no second audit entry; after a revocation the next call mints a fresh
// token, because the old one is dead on every surface (REQ-AUTH-040) and the
// survey would otherwise be closed for good.
func (h *Handler) getSurveyLink(w http.ResponseWriter, r *http.Request) {
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
	eventName := r.URL.Query().Get("event")
	if link != nil && !link.Revoked {
		writeJSON(w, http.StatusOK, surveyLinkObject{
			URL: h.surveyURL(link.Token), Event: eventName, Revoked: false,
			CollectedAt: link.CollectedAt.String,
		})
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
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
		URL: h.surveyURL(fresh.Token), Event: eventName, Revoked: false,
	})
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
