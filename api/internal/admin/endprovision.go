package admin

// POST /api/v1/projects/{id}/end-provision (API_Endpoints_Design.md §4.20,
// BR-009): the one-shot operator action closing a project — delete its record
// data or anonymize it in place (Data_Export_Anonymization_Design.md §7).
// is_admin only: destroying or irrevocably anonymizing clinical data exceeds
// project_admin (§7.2, REQ-AUTH-018).

import (
	"net/http"

	"csms/api/internal/audit"
)

func (h *Handler) registerEndProvision(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/end-provision", h.endProvision)
}

func (h *Handler) endProvision(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errNotFound(w)
		return
	}
	var body struct {
		Provision string `json:"provision"`
	}
	if err := decodeBody(r, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.Provision != "delete" && body.Provision != "anonymize" {
		errBadRequest(w, `provision must be "delete" or "anonymize"`)
		return
	}
	ctx := r.Context()

	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if p == nil {
		errNotFound(w)
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	// One-shot: the project_ended audit event is the idempotency state (§7.2)
	// — a second execution conflicts. The stable name covers every year
	// partition (audit design §6); the latch makes sure it exists.
	if err := h.Audit.EnsureYear(ctx); err != nil {
		errInternal(w)
		return
	}
	var done int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND event_type = ?`,
		projectID, audit.ProjectEnded).Scan(&done); err != nil {
		errInternal(w)
		return
	}
	if done > 0 {
		errConflict(w, "the end-of-project provision has already been executed for this project")
		return
	}

	var records, values int64
	switch body.Provision {
	case "delete":
		// §7.3: every data-plane table keyed by the project; metadata,
		// structure, memberships and the audit trail are kept.
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM data WHERE project_id = ?`, projectID).Scan(&values); err != nil {
			errInternal(w)
			return
		}
		records, err = h.Store.PurgeProjectDataTx(ctx, tx, projectID)
	default: // anonymize — the de-identified pipeline in place (§7.4)
		records, values, err = h.Data.AnonymizeProjectTx(ctx, tx, projectID)
	}
	if err != nil {
		errInternal(w)
		return
	}

	// Same transaction as the data change — no partial state on failure (§7.2).
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.ProjectEnded, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{
			"provision":        body.Provision,
			"records_affected": records,
			"values_affected":  values,
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
		"provision":        body.Provision,
		"records_affected": records,
		"values_affected":  values,
	})
}
