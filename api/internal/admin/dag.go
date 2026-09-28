package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
)

// registerDAG mounts §4.18: data access groups (REQ-API-086…091). The read side
// of the same feature — which records a grouped member sees — is already
// enforced by authz (REQ-AUTH-045); these endpoints are what makes a group
// exist, holds members, and owns records.
func (h *Handler) registerDAG(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/data-access-groups", h.listDAGs)
	mux.HandleFunc("POST /api/v1/projects/{id}/data-access-groups", h.createDAG)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/data-access-groups/{gid}", h.deleteDAG)
	mux.HandleFunc("PUT /api/v1/projects/{id}/users/{uid}/data-access-groups", h.putMemberDAGs)
	mux.HandleFunc("PUT /api/v1/projects/{id}/active-data-access-group", h.putActiveDAG)
	mux.HandleFunc("PUT /api/v1/projects/{id}/records/{record}/data-access-group", h.putRecordDAG)
}

// --- GET / POST / DELETE /api/v1/projects/{id}/data-access-groups (§4.18) ---

// dagObject is the group representation of §4.18 — id and name; membership and
// record counts are not part of it (they are separate calls).
type dagObject struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// listDAGs returns the project's groups, possibly empty (REQ-API-086). Reads
// need data access ≥ read_only like every structure listing.
func (h *Handler) listDAGs(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireRead(w, u, lv) {
		return
	}
	groups, err := h.Store.ListDAGGroups(r.Context(), projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]dagObject, 0, len(groups))
	for _, g := range groups {
		out = append(out, dagObject{ID: g.ID, Name: g.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// createDAG adds a group whose name is unique within the project
// (REQ-API-087, REQ-AUTH-043). A duplicate is 409, never a silent second row.
func (h *Handler) createDAG(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireMember(w, r, lv) || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "name") {
		return
	}
	var name string
	if raw, has := supplied["name"]; !has || json.Unmarshal(raw, &name) != nil || name == "" {
		errBadRequest(w, "name is required")
		return
	}
	ctx := r.Context()
	groups, err := h.Store.ListDAGGroups(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	for _, g := range groups {
		if g.Name == name {
			errConflict(w, "a data access group named '"+name+"' already exists")
			return
		}
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	dg := &db.DagGroup{ProjectID: projectID, Name: name}
	if err := h.Store.CreateDAGGroupTx(ctx, tx, dg); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.DagCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{"name": name},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, dagObject{ID: dg.ID, Name: dg.Name})
}

// deleteDAG removes a group and its member assignments (REQ-API-088). Records
// still assigned to it block the call with 409 — deleting a group out from
// under them would silently change what every grouped member sees (ASM-AUTH-4)
// — and that rejected call writes no audit entry (REQ-AUD-004).
func (h *Handler) deleteDAG(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireMember(w, r, lv) || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	groupID, ok := pathID(r, "gid")
	if !ok {
		errBadRequest(w, "invalid group id")
		return
	}
	ctx := r.Context()
	group, err := h.Store.GetDAGGroup(ctx, groupID)
	if err != nil {
		errInternal(w)
		return
	}
	if group == nil || group.ProjectID != projectID {
		errNotFound(w)
		return
	}
	assigned, err := h.Store.CountRecordsByDAGGroup(ctx, projectID, groupID)
	if err != nil {
		errInternal(w)
		return
	}
	if assigned > 0 {
		errConflict(w, "records are still assigned to this data access group")
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if err := h.Store.DeleteDAGGroupTx(ctx, tx, groupID); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.DagDeleted, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{"name": group.Name, "record_count": 0},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- PUT /api/v1/projects/{id}/users/{uid}/data-access-groups (§4.18) ---

// memberDAGBody is the whole-set body of REQ-API-089: the member's groups and
// which one is active. An empty list clears the member.
type memberDAGBody struct {
	Groups        []int64 `json:"groups"`
	ActiveGroupID *int64  `json:"active_group_id"`
}

// putMemberDAGs replaces one member's group set (REQ-API-089). Reserved to
// is_admin — a project's own admin may name groups and assign records but not
// decide who else sees a data access group (REQ-AUTH-044). Exactly one active
// group when the set is non-empty; clearing needs no active group.
func (h *Handler) putMemberDAGs(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return
	}
	userID, ok := pathID(r, "uid")
	if !ok {
		errBadRequest(w, "invalid user id")
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "groups", "active_group_id") {
		return
	}
	var body memberDAGBody
	rawGroups, hasGroups := supplied["groups"]
	if !hasGroups {
		errBadRequest(w, "groups is required (an empty list clears the member)")
		return
	}
	if err := json.Unmarshal(rawGroups, &body.Groups); err != nil {
		errBadRequest(w, "groups must be an array of group ids")
		return
	}
	if raw, has := supplied["active_group_id"]; has && string(raw) != "null" {
		var gid int64
		if err := json.Unmarshal(raw, &gid); err != nil || gid <= 0 {
			errBadRequest(w, "active_group_id must be a group id or null")
			return
		}
		body.ActiveGroupID = &gid
	}
	ctx := r.Context()

	target, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		errInternal(w)
		return
	}
	if target == nil {
		errNotFound(w)
		return
	}
	asg, err := h.Store.GetAssignment(ctx, userID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if asg == nil { // not a member — nothing to assign (REQ-API-007: never disclosed beyond 404/403 parity)
		errNotFound(w)
		return
	}
	// Every named group must be one of this project's groups, and the active
	// one must be in the set.
	known := map[int64]bool{}
	groups, err := h.Store.ListDAGGroups(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	for _, g := range groups {
		known[g.ID] = true
	}
	seen := map[int64]bool{}
	for _, gid := range body.Groups {
		if !known[gid] {
			errBadRequest(w, "unknown data access group")
			return
		}
		if seen[gid] {
			errBadRequest(w, "duplicate group id in groups")
			return
		}
		seen[gid] = true
	}
	if len(body.Groups) > 0 {
		if body.ActiveGroupID == nil || !seen[*body.ActiveGroupID] {
			errBadRequest(w, "exactly one of the member's groups must be active")
			return
		}
	}
	active := int64(0)
	if body.ActiveGroupID != nil {
		active = *body.ActiveGroupID
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if err := h.Store.SetDAGMembershipsTx(ctx, tx, asg.ID, body.Groups, active); err != nil {
		errInternal(w)
		return
	}
	details := map[string]any{
		"member_email": target.Email, "groups": body.Groups,
	}
	if active > 0 {
		details["active_group_id"] = active
	} else {
		details["active_group_id"] = nil // JSON null — the member has no active group
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.DagMembershipChanged, Source: audit.SourceUI,
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
	writeJSON(w, http.StatusOK, memberDAGBody{Groups: body.Groups, ActiveGroupID: body.ActiveGroupID})
}

// --- PUT /api/v1/projects/{id}/active-data-access-group (§4.18) ---

// putActiveDAG switches the acting member's active group (REQ-API-090,
// REQ-AUTH-046). Self-service: no project_admin here, and no target user — the
// actor is the member. The switch takes effect on the next request, since every
// visibility check reads the active membership.
func (h *Handler) putActiveDAG(w http.ResponseWriter, r *http.Request) {
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
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "group_id") {
		return
	}
	var groupID int64
	if raw, has := supplied["group_id"]; !has || json.Unmarshal(raw, &groupID) != nil || groupID <= 0 {
		errBadRequest(w, "group_id is required")
		return
	}
	ctx := r.Context()

	lv, err := h.access(ctx, u, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if !h.requireMember(w, r, lv) {
		return
	}
	asg, err := h.Store.GetAssignment(ctx, u.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if asg == nil {
		errForbidden(w)
		return
	}
	mine, err := h.Store.ListDAGMembershipsByAssignment(ctx, asg.ID)
	if err != nil {
		errInternal(w)
		return
	}
	inSet := false
	for _, m := range mine {
		if m.GroupID == groupID {
			inSet = true
			break
		}
	}
	if !inSet { // REQ-API-090: only a group the member actually holds
		errBadRequest(w, "group is not one of the member's data access groups")
		return
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	if err := h.Store.SetDAGActiveTx(ctx, tx, asg.ID, groupID); err != nil {
		if err == sql.ErrNoRows { // lost the membership between check and write
			errBadRequest(w, "group is not one of the member's data access groups")
			return
		}
		errInternal(w)
		return
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.DagActiveSwitched, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{"member_email": u.Email, "group_id": groupID},
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_id": groupID})
}

// --- PUT /api/v1/projects/{id}/records/{record}/data-access-group (§4.18) ---

// putRecordDAG assigns a record to a group, or unassigns it when group_id is
// null (REQ-API-091). project_admin plus record visibility: an outsider cannot
// move records between groups any more than read them (REQ-AUTH-045), and the
// 403 never says whether the record exists (REQ-API-007).
func (h *Handler) putRecordDAG(w http.ResponseWriter, r *http.Request) {
	u, lv, projectID, ok := h.structureAccess(w, r)
	if !ok || !h.requireMember(w, r, lv) || !h.requireProjectAdmin(w, r, lv) {
		return
	}
	recordID := r.PathValue("record")
	if recordID == "" {
		errBadRequest(w, "record id is required")
		return
	}
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "group_id") {
		return
	}
	rawGroup, has := supplied["group_id"]
	if !has {
		errBadRequest(w, "group_id is required (null unassigns the record)")
		return
	}
	var groupID *int64
	if string(rawGroup) != "null" {
		var gid int64
		if err := json.Unmarshal(rawGroup, &gid); err != nil || gid <= 0 {
			errBadRequest(w, "group_id must be a group id or null")
			return
		}
		groupID = &gid
	}
	ctx := r.Context()

	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !visible {
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
	if groupID != nil {
		group, err := h.Store.GetDAGGroup(ctx, *groupID)
		if err != nil {
			errInternal(w)
			return
		}
		if group == nil || group.ProjectID != projectID {
			errBadRequest(w, "unknown data access group")
			return
		}
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		errInternal(w)
		return
	}
	defer tx.Rollback()
	nullGroup := sql.NullInt64{}
	if groupID != nil {
		nullGroup = sql.NullInt64{Int64: *groupID, Valid: true}
	}
	if err := h.Store.SetRecordDAGTx(ctx, tx, projectID, recordID, nullGroup); err != nil {
		errInternal(w)
		return
	}
	details := map[string]any{"record_id": recordID}
	if groupID != nil {
		details["group_id"] = *groupID
	} else {
		details["group_id"] = nil // null = unassigned (Audit_Logging_Design.md §3.7)
	}
	if err := h.Audit.InsertTx(ctx, tx, audit.Entry{
		EventType: audit.DagRecordAssigned, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		TargetRecord: recordID, Details: details,
	}); err != nil {
		errInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		errInternal(w)
		return
	}
	out := map[string]any{"record_id": recordID, "group_id": any(nil)}
	if groupID != nil {
		out["group_id"] = *groupID
	}
	writeJSON(w, http.StatusOK, out)
}
