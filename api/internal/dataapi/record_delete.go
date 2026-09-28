package dataapi

// content=record&action=delete (API_Endpoints_Design.md §3.8, GD-3). The
// call is scoped by records[] and optionally events[]/fields[]; a record's
// identity row goes with its last value, and the audit entry carries the
// deleted values (REQ-AUD-009).

import (
	"net/http"

	"csms/api/internal/audit"
)

// uniqueStrings deduplicates, preserving order — a record named twice in
// records[] is deleted once and reported once.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

type deleteRow struct {
	RecordID string `json:"record_id"`
	FormName string `json:"form_name"`
	Deleted  int    `json:"deleted"`
}

func (h *Handler) contentRecordDelete(w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	ctx := r.Context()

	if sub.Project.Mode == "analysis" {
		writeError(w, enc, http.StatusForbidden, "Project in analysis mode")
		return
	}
	if !sub.hasData(lvlDelete) {
		writeError(w, enc, http.StatusForbidden, "Permission denied")
		return
	}
	if len(p.Records) == 0 {
		// Deletion must name its records; an unscoped delete is rejected.
		writeError(w, enc, http.StatusBadRequest, "Invalid request")
		return
	}
	d, err := h.loadDict(ctx, sub.Project.ID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	groupID, err := h.activeGroupID(ctx, sub)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	visible := map[string]bool{}
	ids, err := h.Store.ListRecordIDs(ctx, sub.Project.ID, groupID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	for _, id := range ids {
		visible[id] = true
	}

	results := make([]deleteRow, 0, len(p.Records))
	for _, recordID := range uniqueStrings(p.Records) {
		row := deleteRow{RecordID: recordID}
		if !visible[recordID] {
			// Outside the holder's data-access-group scope (REQ-AUTH-045):
			// reported as not deleted, never as an existence disclosure.
			results = append(results, row)
			continue
		}

		tx, err := h.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			h.storeError(w, enc)
			return
		}
		// Arm gate: every event holding this record's data must be at the
		// holder's delete level; otherwise nothing of the record is touched.
		dvs, err := h.Store.ListDataValuesByRecordTx(ctx, tx, sub.Project.ID, recordID)
		if err != nil {
			tx.Rollback()
			h.storeError(w, enc)
			return
		}
		gated := false
		for _, dv := range dvs {
			if arm, ok := d.eventArm[dv.UniqueEventName]; ok &&
				!sub.User.IsAdmin && sub.dataLevels[arm] < lvlDelete {
				gated = true
			}
		}
		if gated {
			tx.Rollback()
			results = append(results, row)
			continue
		}

		removed, err := h.Store.DeleteRecordValuesTx(ctx, tx, sub.Project.ID, recordID, p.Events, p.Fields)
		if err != nil {
			tx.Rollback()
			h.storeError(w, enc)
			return
		}
		if len(removed) > 0 {
			anyLeft, err := h.Store.RecordHasAnyDataTx(ctx, tx, sub.Project.ID, recordID)
			if err != nil {
				tx.Rollback()
				h.storeError(w, enc)
				return
			}
			if !anyLeft {
				// The record itself goes with its last value (REQ-API-036).
				if err := h.Store.DeleteRecordTx(ctx, tx, sub.Project.ID, recordID); err != nil {
					tx.Rollback()
					h.storeError(w, enc)
					return
				}
			}
			// Audit with the deleted values (REQ-AUD-009).
			values := make([]map[string]string, 0, len(removed))
			for _, dv := range removed {
				values = append(values, map[string]string{
					"event": dv.UniqueEventName, "field": dv.FieldName, "value": dv.Value,
				})
			}
			if err := h.AuditTx(ctx, tx, audit.Entry{
				EventType:    audit.RecordDeleted,
				Source:       audit.SourceAPI,
				UserID:       sub.User.ID,
				Email:        sub.User.Email,
				Token:        p.Token,
				ProjectID:    sub.Project.ID,
				TargetRecord: recordID,
				Details:      map[string]any{"values": values},
			}); err != nil {
				tx.Rollback()
				h.storeError(w, enc)
				return
			}
			row.Deleted = 1
		}
		if err := tx.Commit(); err != nil {
			h.storeError(w, enc)
			return
		}
		results = append(results, row)
	}
	render(w, enc, p.Delimiter(), results)
}
