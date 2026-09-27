package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerModes mounts the project-mode half of §4.21 (GD-20): the mode read
// with its staging flag, and the transition endpoint. Staging itself is a
// production concern and lives in staging.go.
func (h *Handler) registerModes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/mode", h.getProjectMode)
	mux.HandleFunc("PUT /api/v1/projects/{id}/mode", h.putProjectMode)
}

// modeAttrs are the accepted request attributes of PUT …/mode; anything else
// is rejected as unknown (same rule as the project metadata endpoints).
var modeAttrs = []string{"mode", "keep_data"}

// modeTransitionAllowed reports whether from → to is one of the GD-20
// transitions. A project enters analysis only from production — where the data
// entry that analysis disables has actually happened — but returns to
// development directly (2026-09-27); development → analysis is not offered.
func modeTransitionAllowed(from, to string) bool {
	switch from {
	case db.ModeDevelopment:
		return to == db.ModeProduction
	case db.ModeProduction:
		return to == db.ModeDevelopment || to == db.ModeAnalysis
	case db.ModeAnalysis:
		return to == db.ModeDevelopment || to == db.ModeProduction
	}
	return false
}

// modeObject is the GET …/mode response (API_Endpoints_Design.md §4.21).
type modeObject struct {
	Mode        string `json:"mode"`
	StagingOpen bool   `json:"staging_open"`
}

// modeUpdateObject is the PUT …/mode response; records_deleted is set only by
// development → production with keep_data = false.
type modeUpdateObject struct {
	Mode           string `json:"mode"`
	RecordsDeleted int64  `json:"records_deleted"`
}

// --- GET /api/v1/projects/{id}/mode (REQ-API-105) ---

// getProjectMode returns the current mode and whether a staging set is open.
// Requires data access ≥ read_only plus project visibility (§4.21); is_admin is
// covered by REQ-AUTH-023. The badge on the project home reads this endpoint,
// so every member may call it.
func (h *Handler) getProjectMode(w http.ResponseWriter, r *http.Request) {
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
	if !u.IsAdmin && !lv.AnyData(LvlReadOnly) {
		errForbidden(w)
		return
	}
	open, err := h.Store.StagingOpen(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, modeObject{Mode: p.Mode, StagingOpen: open})
}

// --- PUT /api/v1/projects/{id}/mode (REQ-API-105) ---

// putProjectMode changes the project mode per the GD-20 transition table.
// Reserved to is_admin — a project's own project_admin is rejected here, which
// is deliberate: the development → production transition can delete every
// stored record value, the same action the end-provision endpoint reserves to
// installation administrators (GD-20, 2026-09-27).
//
// Rules enforced (REQ-API-105):
//   - the target must be one of the three modes (400 otherwise);
//   - development → production requires an explicit keep_data flag (400 when
//     absent); false deletes the stored record data with the end-provision
//     delete scope and reports how many records went;
//   - any other transition keeps all data; a pair outside the table is 409;
//   - no transition at all while a staging set is open (409) — commit or
//     discard it first;
//   - idempotent: a PUT naming the current mode succeeds without effect.
func (h *Handler) putProjectMode(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return
	}

	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, modeAttrs...) {
		return
	}
	rawMode, present := supplied["mode"]
	if !present {
		errBadRequest(w, "mode is required")
		return
	}
	var target string
	if err := json.Unmarshal(rawMode, &target); err != nil || !db.ValidMode(target) {
		errBadRequest(w, "mode must be one of development, production, analysis")
		return
	}
	keepData := false
	if raw, has := supplied["keep_data"]; has {
		if err := json.Unmarshal(raw, &keepData); err != nil {
			errBadRequest(w, "keep_data must be a boolean")
			return
		}
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
	// Same mode: idempotent success, no write and no audit entry (REQ-API-042).
	if p.Mode == target {
		writeJSON(w, http.StatusOK, modeUpdateObject{Mode: p.Mode})
		return
	}
	if !modeTransitionAllowed(p.Mode, target) {
		errConflict(w, "transition "+p.Mode+" → "+target+" is not an allowed mode change")
		return
	}
	// keep_data is demanded by exactly one transition (development → production).
	if p.Mode == db.ModeDevelopment && target == db.ModeProduction {
		if _, has := supplied["keep_data"]; !has {
			errBadRequest(w, "keep_data is required for the development → production transition")
			return
		}
	}
	// An open staging set blocks every way out of and into production
	// (REQ-API-105): its staged changes would be left behind.
	open, err := h.Store.StagingOpen(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if open {
		errConflict(w, "a staging set is open — commit or discard it before changing the mode")
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	var recordsDeleted int64
	if p.Mode == db.ModeDevelopment && target == db.ModeProduction && !keepData {
		recordsDeleted, err = h.Store.PurgeProjectDataTx(ctx, tx, projectID)
		if err != nil {
			errInternal(w)
			return
		}
	}
	if err := h.Store.SetProjectModeTx(ctx, tx, projectID, target); err != nil {
		errInternal(w)
		return
	}

	details := map[string]any{"old": p.Mode, "new": target}
	if p.Mode == db.ModeDevelopment && target == db.ModeProduction {
		details["keep_data"] = keepData
		details["records_deleted"] = recordsDeleted
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.ProjectModeChanged, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: details,
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, modeUpdateObject{Mode: target, RecordsDeleted: recordsDeleted})
}

// projectMode returns a project's mode for the gates in other areas (the
// data-API write rules and the structure endpoints). An unknown project reads
// as development so a missing row never grants a production-only freedom.
func (h *Handler) projectMode(ctx context.Context, projectID int64) (string, error) {
	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	if p == nil || p.Mode == "" {
		return db.ModeDevelopment, nil
	}
	return p.Mode, nil
}
