package admin

// DELETE /api/v1/projects/{id} (API_Endpoints_Design.md §4.20, REQ-API-187,
// REQ-EXP-044): removes the whole project — structure, roles, memberships and
// their tokens, data access groups, an open staging set and every stored record
// value — in any mode. is_admin only, like the end provision beside it
// (Data_Export_Anonymization_Design.md §7.5). The audit trail stays: the
// project_deleted entry commits with the deletion, under the deleted id, which
// no later project receives (REQ-DB-055).

import (
	"database/sql"
	"errors"
	"net/http"

	"csms/api/internal/audit"
)

func (h *Handler) registerProjectDelete(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/v1/projects/{id}", h.deleteProject)
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errNotFound(w)
		return
	}
	ctx := r.Context()

	// Every read below is on the transaction: the deletion, the facts its audit
	// entry records and the entry itself are one unit (REQ-AUD-003). The audit
	// year is provisioned before the handler runs (httpapi.rolloverGuard) — see
	// endprovision.go for why no EnsureYear call may appear in here.
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	p, err := h.Store.GetProjectTx(ctx, tx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if p == nil {
		errNotFound(w)
		return
	}

	records, values, err := h.Store.DeleteProjectTx(ctx, tx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		errNotFound(w) // removed by a concurrent call between the read and the delete
		return
	}
	if err != nil {
		errInternal(w)
		return
	}

	// The entry names the project and what went with it, never a stored value
	// (REQ-AUD-038); the project column keeps the deleted id.
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.ProjectDeleted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{
			"project_name":    p.ProjectName,
			"mode":            p.Mode,
			"records_deleted": records,
			"values_deleted":  values,
		},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":      projectID,
		"project_name":    p.ProjectName,
		"records_deleted": records,
		"values_deleted":  values,
	})
}
