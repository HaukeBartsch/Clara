package admin

import (
	"net/http"
	"time"
)

// Starred projects (§4.32, REQ-API-188, REQ-DB-056). A user stars the projects they work
// in on the project overview, and the overview can then show those alone (REQ-UI-087).
// The star is the acting user's own and orders presentation only — no authorization
// path reads it.
//
// No audit entry, for the reason the UI language and theme carry none (§4.19): a member
// marking a project for their own overview is neither a data change nor an
// administration event. An entry would also be project-scoped, and the overview's "last
// used" is the user's newest event in a project (REQ-UI-071) — starring a project would
// make it read "today" and move it to the front of its group.

func (h *Handler) registerStars(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/users/me/starred-projects/{id}", h.putProjectStar)
	mux.HandleFunc("DELETE /api/v1/users/me/starred-projects/{id}", h.deleteProjectStar)
}

// projectStar is the §4.32 answer: the project and whether it is now starred.
type projectStar struct {
	ProjectID int64 `json:"project_id"`
	Starred   bool  `json:"starred"`
}

func (h *Handler) putProjectStar(w http.ResponseWriter, r *http.Request) {
	h.setProjectStar(w, r, true)
}

func (h *Handler) deleteProjectStar(w http.ResponseWriter, r *http.Request) {
	h.setProjectStar(w, r, false)
}

// setProjectStar stars or unstars a project the acting user can see — every project for
// is_admin, the user's memberships otherwise (REQ-AUTH-026). A non-administrator gets the
// uniform 403 for a project outside their memberships whether it exists or not
// (REQ-API-007); an administrator gets 404 for one that does not exist. Both directions
// are idempotent (REQ-API-042).
func (h *Handler) setProjectStar(w http.ResponseWriter, r *http.Request, on bool) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return
	}
	ctx := r.Context()
	if u.IsAdmin {
		p, err := h.Store.GetProject(ctx, projectID)
		if err != nil {
			errInternal(w)
			return
		}
		if p == nil {
			errNotFound(w)
			return
		}
	} else {
		a, err := h.Store.GetAssignment(ctx, u.ID, projectID)
		if err != nil {
			errInternal(w)
			return
		}
		if a == nil {
			errForbidden(w)
			return
		}
	}
	if err := h.Store.SetProjectStar(ctx, u.ID, projectID, on, time.Now()); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, projectStar{ProjectID: projectID, Starred: on})
}
