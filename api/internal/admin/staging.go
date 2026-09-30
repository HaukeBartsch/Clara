package admin

// Project staging lifecycle (GD-20, REQ-API-106/107/111,
// API_Endpoints_Design.md §4.21). In production a structure change needs an
// open staging set and lands on the snapshot instead of the live design; data
// collection keeps running on the active design until commit. The mode itself
// is in modes.go — this file is the set: start, read, commit, discard, plus the
// gate every structure endpoint passes through.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

func (h *Handler) registerStaging(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/staging", h.startStaging)
	mux.HandleFunc("GET /api/v1/projects/{id}/staging", h.getStaging)
	mux.HandleFunc("POST /api/v1/projects/{id}/staging/commit", h.commitStaging)
	mux.HandleFunc("POST /api/v1/projects/{id}/staging/discard", h.discardStaging)
}

// --- where a structure change lands ---

// writeTarget is the destination of one structure change.
type writeTarget int

const (
	// writeLive applies to the live structure tables (development mode, and
	// analysis mode after its breaking-change guard).
	writeLive writeTarget = iota
	// writeStaged applies to the open staging set's snapshot; the active design
	// stays as it is until commit (REQ-API-107).
	writeStaged
)

// designWriteGate decides where a structure change goes, per the mode table of
// §4.21: development applies immediately, production requires an open set and
// returns its snapshot, analysis applies immediately (its guard is separate —
// see analysisGuard). acknowledgeBreaking is meaningful in analysis mode only;
// in production the acknowledgement happens at commit (REQ-API-108), so it is
// accepted and ignored here.
func (h *Handler) designWriteGate(
	w http.ResponseWriter, r *http.Request, projectID int64,
) (writeTarget, *stagedDesign, bool) {
	ctx := r.Context()
	mode, err := h.projectMode(ctx, projectID)
	if err != nil {
		errInternal(w)
		return writeLive, nil, false
	}
	switch mode {
	case db.ModeDevelopment:
		return writeLive, nil, true
	case db.ModeProduction:
		doc, err := h.openStagingDesign(ctx, projectID)
		if err != nil {
			errInternal(w)
			return writeLive, nil, false
		}
		if doc == nil {
			errConflict(w, "structure changes in production mode require an open staging set")
			return writeLive, nil, false
		}
		return writeStaged, doc, true
	default: // analysis: setup edits apply to the live design (REQ-API-107/111)
		return writeLive, nil, true
	}
}

// applyFunc is one structure change expressed over a design snapshot, returning
// the rejection it would raise (nil when it applies). Every structure handler
// builds one: production applies it to the open set, analysis mode runs it
// against a throwaway copy only to classify it (REQ-API-111), and the live path
// keeps doing what it has always done. Returning the designError rather than a
// bool means the duplicate-name and GD-8 messages are written once and read the
// same on every path.
type applyFunc func(d *stagedDesign) *designError

// analysisGuard reports the breaking changes this mutation would make to the
// live design — non-empty only in analysis mode, where a breaking change needs
// acknowledge_breaking before it applies (REQ-API-111). A mutation that cannot
// be expressed over a snapshot reports no changes: the handler's own validation
// rejects it.
func (h *Handler) analysisGuard(ctx context.Context, projectID int64, apply applyFunc) ([]stagedChange, error) {
	mode, err := h.projectMode(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if mode != db.ModeAnalysis {
		return nil, nil
	}
	active, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		return nil, err
	}
	next, err := active.clone()
	if err != nil {
		return nil, err
	}
	if err := apply(next); err != nil {
		return nil, nil // an invalid change breaks nothing; the caller rejects it
	}
	changes, err := classifyDesignChange(ctx, active, next, storeValues{store: h.Store, projectID: projectID})
	if err != nil {
		return nil, err
	}
	var breaking []stagedChange
	for _, c := range changes {
		if c.Breaking {
			breaking = append(breaking, c)
		}
	}
	return breaking, nil
}

// writeBreakingChanges rejects an analysis-mode structure change that would
// break stored data, naming the changes and their reasons (§4.21: reject once,
// apply when the same call returns with acknowledge_breaking).
func (h *Handler) writeBreakingChanges(w http.ResponseWriter, changes []stagedChange) {
	message := "breaking change"
	if len(changes) > 1 {
		message += "s"
	}
	message += ": " + changes[0].Kind + " on " + changes[0].Object
	if changes[0].Reason != "" {
		message += " (" + changes[0].Reason + ")"
	}
	if len(changes) > 1 {
		message += ", and " + strconv.Itoa(len(changes)-1) + " more"
	}
	errConflict(w, message+" — resend with acknowledge_breaking true to apply it")
}

// saveStagedDesign writes the snapshot back after a staged change (REQ-API-107).
// A set that was committed or discarded underneath this request reports as gone
// rather than being resurrected.
func (h *Handler) saveStagedDesign(ctx context.Context, projectID int64, d *stagedDesign) error {
	doc, err := d.encode()
	if err != nil {
		return err
	}
	ok, err := h.Store.UpdateStagingDesign(ctx, projectID, doc)
	if err != nil {
		return err
	}
	if !ok {
		return errNoStagingSet
	}
	return nil
}

// errNoStagingSet marks a set that closed between read and write.
var errNoStagingSet = errors.New("the staging set is no longer open")

// openStagingDesign returns the snapshot of the open set, or nil when none is.
func (h *Handler) openStagingDesign(ctx context.Context, projectID int64) (*stagedDesign, error) {
	set, err := h.Store.GetStaging(ctx, projectID)
	if err != nil || set == nil {
		return nil, err
	}
	return parseStagedDesign(set.Design)
}

// --- access ---

// stagingAccess resolves the actor, the project and project_admin (the staging
// lifecycle belongs to the project's own admin; is_admin covers it per
// REQ-AUTH-023). §5 lists all four endpoints this way.
func (h *Handler) stagingAccess(w http.ResponseWriter, r *http.Request) (*db.User, int64, bool) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return nil, 0, false
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return nil, 0, false
	}
	lv, err := h.access(r.Context(), u, projectID)
	if err != nil {
		errInternal(w)
		return nil, 0, false
	}
	if !h.requireProjectAdmin(w, r, lv) {
		return nil, 0, false
	}
	return u, projectID, true
}

// structureWrite runs one structure change through the mode rules of §4.21 and
// returns where it lands, as a writeDecision: production without an open set
// (409), or an analysis-mode change that breaks stored data arriving without
// acknowledge_breaking (409, naming the change), are answered here and leave ok
// false. apply is the mutation expressed over a snapshot — the same closure the
// caller applies on the staged path, which is what lets analysis classify it
// before it touches anything.
//
// The third result reports a breaking change the caller acknowledged. It is only
// ever true in analysis mode (production acknowledges at commit, REQ-API-108),
// and a handler that audits its change records it on the event (REQ-AUD-025).
func (h *Handler) structureWrite(
	w http.ResponseWriter, r *http.Request, projectID int64, acknowledge bool, apply applyFunc,
) (target writeTarget, staged *stagedDesign, breakingAcknowledged, ok bool) {
	target, staged, ok = h.designWriteGate(w, r, projectID)
	if !ok {
		return writeLive, nil, false, false
	}
	if target == writeStaged {
		return writeStaged, staged, false, true
	}
	breaking, err := h.analysisGuard(r.Context(), projectID, apply)
	if err != nil {
		errInternal(w)
		return writeLive, nil, false, false
	}
	if len(breaking) > 0 && !acknowledge {
		h.writeBreakingChanges(w, breaking)
		return writeLive, nil, false, false
	}
	return writeLive, nil, len(breaking) > 0, true
}

// withBreakingAcknowledgement adds to a structure event's details that the change
// broke stored data and went ahead because the caller said so, which is what
// makes the warning the admin saw recoverable from the trail
// (Audit_Logging_Design.md §3.8, REQ-AUD-025). Details come back unchanged for
// the ordinary case where no acknowledgement was needed.
func withBreakingAcknowledgement(details map[string]any, acknowledged bool) map[string]any {
	if acknowledged {
		details["breaking_acknowledged"] = true
	}
	return details
}

// applyStagedChange writes the mutated snapshot back and reports the failure as
// a response. Staged edits are not audited individually: the commit's
// staging_committed entry is the record of the batch (Audit_Logging_Design.md §3.8).
func (h *Handler) applyStagedChange(
	w http.ResponseWriter, r *http.Request, projectID int64, staged *stagedDesign, apply applyFunc,
) bool {
	if err := apply(staged); err != nil {
		writeDesignError(w, err)
		return false
	}
	if err := h.saveStagedDesign(r.Context(), projectID, staged); err != nil {
		writeApplyError(w, err)
		return false
	}
	return true
}

// acknowledgeFrom reads the optional acknowledge_breaking attribute of a
// structure request (§4.21 analysis mode). Every structure write body accepts
// it; elsewhere it is meaningless and ignored.
func acknowledgeFrom(supplied map[string]json.RawMessage) bool {
	raw, ok := supplied["acknowledge_breaking"]
	if !ok {
		return false
	}
	var ack bool
	if err := json.Unmarshal(raw, &ack); err != nil {
		return false
	}
	return ack
}

// stagedRead returns the design a structure read should answer from: the open
// staging set when one is open, nil when the live tables are (REQ-API-107 and
// the decision that every reader sees the staged design while a set is up).
func (h *Handler) stagedRead(ctx context.Context, projectID int64) (*stagedDesign, error) {
	return h.openStagingDesign(ctx, projectID)
}

// stagedInstrumentOr404 resolves one instrument inside the open set — the
// staged twin of Store.GetInstrument. An instrument created while a set is open
// carries a provisional id and has no live row, so the snapshot is the only
// place it exists (REQ-API-107).
func (h *Handler) stagedInstrumentOr404(w http.ResponseWriter, d *stagedDesign, instID int64) (*stagedInstrument, bool) {
	si, ok := d.instrument(instID)
	if !ok {
		errNotFound(w)
		return nil, false
	}
	return si, true
}

// stagedProjectFor finds the open staging set that contains an object two
// endpoints address without a project in their path (DELETE /api/v1/arms/{id}
// and PUT /api/v1/events/{id}, whose paths are fixed by ASM-API-1). An id taken
// from a staged read has no live row, so the set holding it is found by looking
// through the open sets — at most one per project, which keeps the scan short.
func (h *Handler) stagedProjectFor(ctx context.Context, contains func(*stagedDesign) bool) (int64, *stagedDesign, error) {
	rows, err := h.Store.DB.QueryContext(ctx, `SELECT project_id, design FROM project_staging`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID int64
		var doc string
		if err := rows.Scan(&projectID, &doc); err != nil {
			return 0, nil, err
		}
		d, err := parseStagedDesign(doc)
		if err != nil {
			continue
		}
		if contains(d) {
			return projectID, d, nil
		}
	}
	return 0, nil, rows.Err()
}

// deleteStagedArm removes an arm that exists only inside an open staging set —
// a provisional id from a staged read, with no live row to resolve the project
// from. handled is false when no open set carries the id, leaving the caller to
// answer 404. Permission is checked here because the caller could not resolve a
// project before this point.
func (h *Handler) deleteStagedArm(
	w http.ResponseWriter, r *http.Request, u *db.User, armID int64,
) (handled bool, err error) {
	if armID >= 0 {
		return false, nil
	}
	return h.stagedObjectWrite(w, r, u,
		func(d *stagedDesign) bool { _, ok := d.armByID(armID); return ok },
		func(d *stagedDesign) *designError {
			a, _ := d.armByID(armID)
			if len(a.Events) > 0 {
				return conflictf("arm still has events")
			}
			d.deleteArm(armID)
			return nil
		},
		func() { w.WriteHeader(http.StatusNoContent) })
}

// stagedObjectWrite serves the two structure routes whose path carries only an
// object id (DELETE /api/v1/arms/{id}, PUT /api/v1/events/{id} — fixed by
// ASM-API-1) when that id is provisional: it exists only inside an open staging
// set, so there is no live row to resolve the project from. The set is found,
// project_admin checked against its project, and fn applied to it. handled is
// false when no open set holds the id, leaving the caller to answer 404.
func (h *Handler) stagedObjectWrite(
	w http.ResponseWriter, r *http.Request, u *db.User,
	holds func(*stagedDesign) bool, fn func(*stagedDesign) *designError, onSuccess func(),
) (handled bool, err error) {
	projectID, d, err := h.stagedProjectFor(r.Context(), holds)
	if err != nil {
		return false, err
	}
	if d == nil {
		return false, nil
	}
	lv, err := h.access(r.Context(), u, projectID)
	if err != nil {
		return true, err
	}
	if !h.requireProjectAdmin(w, r, lv) {
		return true, nil
	}
	if derr := fn(d); derr != nil {
		writeDesignError(w, derr)
		return true, nil
	}
	if err := h.saveStagedDesign(r.Context(), projectID, d); err != nil {
		writeApplyError(w, err)
		return true, nil
	}
	onSuccess()
	return true, nil
}

// --- POST /api/v1/projects/{id}/staging (REQ-API-106) ---

// stagingObject is the start response: when the set opened and who opened it.
type stagingObject struct {
	OpenedAt string `json:"opened_at"`
	OpenedBy int64  `json:"opened_by"`
}

func (h *Handler) startStaging(w http.ResponseWriter, r *http.Request) {
	u, projectID, ok := h.stagingAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	mode, err := h.projectMode(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if mode != db.ModeProduction {
		errConflict(w, "staging is available in production mode only")
		return
	}
	open, err := h.Store.StagingOpen(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if open {
		errConflict(w, "a staging set is already open for this project")
		return
	}
	d, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	doc, err := d.encode()
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.Store.OpenStaging(ctx, projectID, doc, u.ID); err != nil {
		errInternal(w)
		return
	}
	set, err := h.Store.GetStaging(ctx, projectID)
	if err != nil || set == nil {
		errInternal(w)
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.StagingStarted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{}, // §3.8: no payload
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, stagingObject{OpenedAt: set.OpenedAt, OpenedBy: u.ID})
}

// --- GET /api/v1/projects/{id}/staging (REQ-API-106) ---

// stagingState is the state plus the staged diff, computed against the active
// design so the list cannot disagree with what commit would do.
type stagingState struct {
	Open     bool           `json:"open"`
	OpenedAt *string        `json:"opened_at"`
	OpenedBy *int64         `json:"opened_by"`
	Changes  []stagedChange `json:"changes"`
}

func (h *Handler) getStaging(w http.ResponseWriter, r *http.Request) {
	_, projectID, ok := h.stagingAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	set, err := h.Store.GetStaging(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := stagingState{Changes: []stagedChange{}}
	if set == nil {
		writeJSON(w, http.StatusOK, out) // open: false, no changes
		return
	}
	staged, err := parseStagedDesign(set.Design)
	if err != nil {
		errInternal(w)
		return
	}
	active, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	changes, err := classifyDesignChange(ctx, active, staged, storeValues{store: h.Store, projectID: projectID})
	if err != nil {
		errInternal(w)
		return
	}
	if changes != nil {
		out.Changes = changes
	}
	openedAt := set.OpenedAt
	out.Open = true
	out.OpenedAt = &openedAt
	if set.OpenedBy.Valid {
		by := set.OpenedBy.Int64
		out.OpenedBy = &by
	}
	writeJSON(w, http.StatusOK, out)
}

// --- POST /api/v1/projects/{id}/staging/discard (REQ-API-106) ---

func (h *Handler) discardStaging(w http.ResponseWriter, r *http.Request) {
	u, projectID, ok := h.stagingAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	set, err := h.Store.GetStaging(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if set == nil {
		errConflict(w, "no staging set is open")
		return
	}
	if _, err := h.Store.DeleteStaging(ctx, projectID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.StagingDiscarded, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{"opened_at": set.OpenedAt},
	}); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /api/v1/projects/{id}/staging/commit (REQ-API-106/108) ---

// appliedCounts is the commit response: what reached the live design.
type appliedCounts struct {
	Instruments  int `json:"instruments"`
	Fields       int `json:"fields"`
	Events       int `json:"events"`
	MappingPairs int `json:"mapping_pairs"`
}

type commitResponse struct {
	Applied appliedCounts `json:"applied"`
}

func (h *Handler) commitStaging(w http.ResponseWriter, r *http.Request) {
	u, projectID, ok := h.stagingAccess(w, r)
	if !ok {
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "acknowledge_breaking") {
		return
	}
	var body struct {
		AcknowledgeBreaking *bool `json:"acknowledge_breaking"`
	}
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	ack := body.AcknowledgeBreaking != nil && *body.AcknowledgeBreaking

	ctx := r.Context()
	set, err := h.Store.GetStaging(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if set == nil {
		errConflict(w, "no staging set is open")
		return
	}
	staged, err := parseStagedDesign(set.Design)
	if err != nil {
		errInternal(w)
		return
	}
	active, err := h.stagedDesignOf(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	changes, err := classifyDesignChange(ctx, active, staged, storeValues{store: h.Store, projectID: projectID})
	if err != nil {
		errInternal(w)
		return
	}
	var breaking []stagedChange
	for _, c := range changes {
		if c.Breaking {
			breaking = append(breaking, c)
		}
	}
	if len(breaking) > 0 && !ack {
		// §4.21: the rejection lists them; nothing is applied and no entry is
		// written (REQ-AUD-004).
		writeBreakingList(w, breaking)
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()

	applied, err := h.applyStagedDesignTx(ctx, tx, projectID, active, staged)
	if err != nil {
		writeApplyError(w, err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM project_staging WHERE project_id = ?`, projectID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.StagingCommitted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: commitAuditDetails(applied, breaking),
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, commitResponse{Applied: applied})
}

// commitAuditDetails is the §3.8 staging_committed payload: the applied counts
// plus the breaking changes this commit acknowledged (omitted when none).
func commitAuditDetails(applied appliedCounts, breaking []stagedChange) map[string]any {
	breakingList := []map[string]any{}
	for _, c := range breaking {
		breakingList = append(breakingList, map[string]any{"kind": c.Kind, "object": c.Object})
	}
	details := map[string]any{
		"applied": map[string]any{
			"instruments": applied.Instruments, "fields": applied.Fields,
			"events": applied.Events, "mapping_pairs": applied.MappingPairs,
		},
	}
	if len(breakingList) > 0 {
		details["breaking_acknowledged"] = breakingList
	}
	return details
}

// writeBreakingList rejects a commit whose staged set contains breaking
// changes, listing them (§4.21).
func writeBreakingList(w http.ResponseWriter, breaking []stagedChange) {
	type listed struct {
		Kind   string `json:"kind"`
		Object string `json:"object"`
		Reason string `json:"reason,omitempty"`
	}
	out := make([]listed, 0, len(breaking))
	for _, c := range breaking {
		out = append(out, listed{Kind: c.Kind, Object: c.Object, Reason: c.Reason})
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":    "conflict",
		"message":  "the staged set contains breaking changes; resend with acknowledge_breaking true",
		"status":   http.StatusConflict,
		"breaking": out,
	})
}

// writeApplyError renders a failed apply: a designError keeps its own status,
// anything else is an internal error with no detail (REQ-API-006). A set that
// closed underneath the request is a conflict.
func writeApplyError(w http.ResponseWriter, err error) {
	var de *designError
	if errors.As(err, &de) {
		writeDesignError(w, de)
		return
	}
	if errors.Is(err, errNoStagingSet) {
		errConflict(w, err.Error())
		return
	}
	errInternal(w)
}
