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
// (record, instrument) (REQ-API-082…085). The link is what the PHP survey page
// carries; the browser never calls /api/v1/* from it (GD-1, REQ-API-084), and
// what the link token may do on the data API is fixed by §3.10.
func (h *Handler) registerSurvey(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.getSurveyLink)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/records/{record}/instruments/{iid}/survey-link", h.deleteSurveyLink)
}

// surveyLinkObject is the §4.17 response: the public URL carrying the link
// token, plus whether it still works. The token itself is not returned bare —
// the URL is the contract the caller renders.
type surveyLinkObject struct {
	URL     string `json:"url"`
	Revoked bool   `json:"revoked"`
}

// surveyLinkContext resolves everything both §4.17 endpoints share: the actor,
// the project, membership, record visibility (uniform 403, REQ-AUTH-045), a
// data access level of view_edit (REQ-API-082), an existing record and
// instrument belonging to the project (404), and the survey marking — a link
// for a data-collection instrument is an invalid request, not a conflict
// (§4.2). The instrument is returned for the caller to name in its audit
// payload.
func (h *Handler) surveyLinkContext(w http.ResponseWriter, r *http.Request) (*db.User, *db.Instrument, int64, string, bool) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok {
		return nil, nil, 0, "", false
	}
	if !h.requireMember(w, r, lv) {
		return nil, nil, 0, "", false
	}
	recordID := r.PathValue("record")
	if recordID == "" {
		errBadRequest(w, "record id is required")
		return nil, nil, 0, "", false
	}
	instrumentID, ok := pathID(r, "iid")
	if !ok {
		errBadRequest(w, "invalid instrument id")
		return nil, nil, 0, "", false
	}
	ctx := r.Context()

	// The link is per (record, instrument) with no event in the path, so the
	// arm check is the one §4.17 can mean here: view_edit on at least one arm
	// the caller holds (GD-2), never a specific arm's level.
	if !u.IsAdmin && !lv.AnyData(LvlViewEdit) {
		errForbidden(w)
		return nil, nil, 0, "", false
	}
	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", false
	}
	if !visible {
		errForbidden(w)
		return nil, nil, 0, "", false
	}
	exists, err := h.Store.RecordExists(ctx, projectID, recordID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", false
	}
	if !exists {
		errNotFound(w)
		return nil, nil, 0, "", false
	}
	instrument, err := h.Store.GetInstrument(ctx, instrumentID)
	if err != nil {
		errInternal(w)
		return nil, nil, 0, "", false
	}
	if instrument == nil || instrument.ProjectID != projectID {
		errNotFound(w)
		return nil, nil, 0, "", false
	}
	if !instrument.IsSurvey {
		errBadRequest(w, "the instrument is not survey-marked")
		return nil, nil, 0, "", false
	}
	return u, instrument, projectID, recordID, true
}

// getSurveyLink returns the stable public link for a (record, instrument),
// issuing one on first call (REQ-API-082). Repeat calls return the same URL and
// write no second audit entry; after a revocation the next call mints a fresh
// token, because the old one is dead on every surface (REQ-AUTH-040) and the
// survey would otherwise be closed for good.
func (h *Handler) getSurveyLink(w http.ResponseWriter, r *http.Request) {
	u, instrument, projectID, recordID, ok := h.surveyLinkContext(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	link, err := h.Store.GetSurveyLink(ctx, projectID, recordID, instrument.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if link != nil && !link.Revoked {
		writeJSON(w, http.StatusOK, surveyLinkObject{URL: h.surveyURL(link.Token), Revoked: false})
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
		CreatedBy: sql.NullInt64{Int64: u.ID, Valid: true},
	}
	if err := h.Store.IssueSurveyLinkTokenTx(ctx, tx, fresh); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.SurveyLinkIssued, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID, TargetRecord: recordID,
		Details: map[string]any{"record_id": recordID, "instrument": instrument.Name},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, surveyLinkObject{URL: h.surveyURL(fresh.Token), Revoked: false})
}

// deleteSurveyLink revokes the link; the revocation takes effect on the next
// data-API call with that token (REQ-AUTH-040). 204 whether or not it was
// already revoked, and only an actual revocation is audit-logged (REQ-API-042).
func (h *Handler) deleteSurveyLink(w http.ResponseWriter, r *http.Request) {
	u, instrument, projectID, recordID, ok := h.surveyLinkContext(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	link, err := h.Store.GetSurveyLink(ctx, projectID, recordID, instrument.ID)
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
	revoked, err := h.Store.RevokeSurveyLinkTx(ctx, tx, projectID, recordID, instrument.ID)
	if err != nil {
		errInternal(w)
		return
	}
	if revoked {
		if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
			EventType: audit.SurveyLinkRevoked, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: projectID, TargetRecord: recordID,
			Details: map[string]any{"record_id": recordID, "instrument": instrument.Name},
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
