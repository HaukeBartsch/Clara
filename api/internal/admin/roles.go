package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerRoles mounts §4.7: the role listing, creation and edit. All three
// require is_admin; roles are project-scoped (REQ-AUTH-024) and not limited to
// preset examples (REQ-AUTH-020). A role is editable — its permissions are the
// point of the screen — but there is no delete endpoint: a member's
// `user_projects.role_id` cascades to NULL, which REQ-AUTH-022 reads as full
// permissions, so deleting a role would silently promote everyone holding it
// (Plan/Web_Implementation.md §7 rule 5).
func (h *Handler) registerRoles(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/roles", h.listRoles)
	mux.HandleFunc("POST /api/v1/projects/{id}/roles", h.createRole)
	mux.HandleFunc("PUT /api/v1/projects/{id}/roles/{rid}", h.updateRole)
}

// --- role objects (§4.7) ---

// roleArmLevels is one per-arm default of a role (REQ-DB-009 as revised).
type roleArmLevels struct {
	Data   string `json:"data"`
	Export string `json:"export"`
}

// roleGrantObject is one per-(instrument, event) override of a role's arm
// default (REQ-DB-040). The pair is named — unique event name and instrument
// name — because that is what the designer and the roles matrix address; the
// ids stay inside the API.
type roleGrantObject struct {
	Event        string `json:"event"`
	Instrument   string `json:"instrument"`
	Data         string `json:"data"`
	Export       string `json:"export"`
	DeleteValues bool   `json:"delete_values"`
	EditSurveys  bool   `json:"edit_surveys"`
}

// roleObject is the §4.7 representation. Arms keys are arm numbers as strings
// and every project arm appears, ungranted arms at their no-access default
// (REQ-AUTH-019 — no implicit access). Grants lists only the pairs whose grant
// differs from their arm default; a pair absent from it inherits (REQ-AUTH-069).
type roleObject struct {
	ID           int64                    `json:"id"`
	Name         string                   `json:"name"`
	ProjectAdmin bool                     `json:"project_admin"`
	Arms         map[string]roleArmLevels `json:"arms"`
	Grants       []roleGrantObject        `json:"grants"`
}

// dataLevels and exportLevels are the fixed level vocabularies of
// Authentication_Authorization_Design.md §4.1. The ladder ends at view_edit:
// deleting values and editing collected surveys are the two booleans of a
// grant, never levels (REQ-AUTH-018/070).
var (
	dataLevels = map[string]bool{
		"no_access": true, "read_only": true, "view_edit": true,
	}
	exportLevels = map[string]bool{
		"export_none": true, "export_de_identified": true,
		"export_no_identifiers": true, "export_full": true,
	}
)

// rolePairNames indexes the project's mapped pairs in both directions: by
// "event\x00instrument" for validating a supplied grant, and by pair id for
// rendering one back as names.
type rolePairNames struct {
	byName map[string][2]int64    // "event\x00instrument" -> {event_id, instrument_id}
	names  map[[2]int64][2]string // {event_id, instrument_id} -> {event, instrument}
}

func pairKeyOf(eventID, instrumentID int64) [2]int64 { return [2]int64{eventID, instrumentID} }

// loadRolePairs builds the name index of every mapped (instrument, event) pair
// of the project (REQ-DB-012) — the only pairs a grant may name.
func (h *Handler) loadRolePairs(ctx context.Context, projectID int64) (*rolePairNames, error) {
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	eventName := map[int64]string{}
	for _, e := range events {
		eventName[e.ID] = e.UniqueEventName
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		return nil, err
	}
	instrName := map[int64]string{}
	for _, i := range instruments {
		instrName[i.ID] = i.Name
	}
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := &rolePairNames{
		byName: map[string][2]int64{},
		names:  map[[2]int64][2]string{},
	}
	for _, p := range pairs {
		en, in := eventName[p.EventID], instrName[p.InstrumentID]
		if en == "" || in == "" {
			continue // object gone from the dictionary mid-read
		}
		out.byName[en+"\x00"+in] = pairKeyOf(p.EventID, p.InstrumentID)
		out.names[pairKeyOf(p.EventID, p.InstrumentID)] = [2]string{en, in}
	}
	return out, nil
}

// armDefaults maps each arm of the project to its stored default, filling the
// arms a role says nothing about with no_access / export_none — the reading
// authz.Effective applies (REQ-AUTH-019).
func armDefaults(projectArms []db.Arm, stored []db.RoleArm) map[string]roleArmLevels {
	out := make(map[string]roleArmLevels, len(projectArms))
	for _, a := range projectArms {
		out[strconv.Itoa(a.ArmNum)] = roleArmLevels{Data: "no_access", Export: "export_none"}
	}
	for _, ra := range stored {
		key := strconv.Itoa(ra.ArmNum)
		if _, known := out[key]; !known {
			continue // arm no longer in the project (stale row)
		}
		out[key] = roleArmLevels{Data: ra.DataAccessLevel, Export: ra.ExportLevel}
	}
	return out
}

// grantObjects renders a role's stored grants for §4.7, dropping the rows that
// repeat their arm default — an override equal to what it overrides carries no
// information, and omitting it is exactly what "inherits" means (REQ-AUTH-069).
func grantObjects(stored []db.RoleGrant, names *rolePairNames, defaults map[string]roleArmLevels, eventArm map[int64]int) []roleGrantObject {
	out := make([]roleGrantObject, 0, len(stored))
	for _, g := range stored {
		pair := pairKeyOf(g.EventID, g.InstrumentID)
		named, known := names.names[pair]
		if !known {
			continue // pair no longer mapped (a design change deleted it)
		}
		def := defaults[strconv.Itoa(eventArm[g.EventID])]
		if def.Data == g.DataAccessLevel && def.Export == g.ExportLevel &&
			!g.DeleteValues && !g.EditSurveys {
			continue
		}
		out = append(out, roleGrantObject{
			Event: named[0], Instrument: named[1],
			Data: g.DataAccessLevel, Export: g.ExportLevel,
			DeleteValues: g.DeleteValues, EditSurveys: g.EditSurveys,
		})
	}
	return out
}

// newRoleObject maps a role plus everything loaded about the project to the
// §4.7 shape.
func newRoleObject(r db.Role, defaults map[string]roleArmLevels,
	grants []roleGrantObject) roleObject {
	if grants == nil {
		grants = []roleGrantObject{}
	}
	return roleObject{
		ID: r.ID, Name: r.RoleName, ProjectAdmin: r.ProjectAdmin,
		Arms: defaults, Grants: grants,
	}
}

// eventArmOf maps an event id to its arm number, so a grant can be compared
// with the arm default it overrides.
func eventArmOf(events []db.Event, arms []db.Arm) map[int64]int {
	armNumOf := map[int64]int{}
	for _, a := range arms {
		armNumOf[a.ID] = a.ArmNum
	}
	out := map[int64]int{}
	for _, e := range events {
		out[e.ID] = armNumOf[e.ArmID]
	}
	return out
}

// --- GET /api/v1/projects/{id}/roles (REQ-API-056) ---

// listRoles returns the project's roles with their arm defaults and pair
// grants. is_admin.
func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
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
	roles, err := h.Store.ListRoles(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	projectArms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	names, err := h.loadRolePairs(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	evArm := eventArmOf(events, projectArms)

	out := make([]roleObject, 0, len(roles))
	for _, role := range roles {
		storedArms, err := h.Store.ListRoleArms(ctx, role.ID)
		if err != nil {
			errInternal(w)
			return
		}
		defaults := armDefaults(projectArms, storedArms)
		storedGrants, err := h.Store.ListRoleGrants(ctx, role.ID)
		if err != nil {
			errInternal(w)
			return
		}
		out = append(out, newRoleObject(role, defaults,
			grantObjects(storedGrants, names, defaults, evArm)))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- the request body of POST and PUT (§4.7) ---

// roleRequest is the §4.7 body: the role object minus id. An arm absent from
// arms defaults to no_access / export_none (REQ-AUTH-019), and a pair absent
// from grants inherits its arm default (REQ-AUTH-069).
type roleRequest struct {
	Name         string                   `json:"name"`
	ProjectAdmin bool                     `json:"project_admin"`
	Arms         map[string]roleArmLevels `json:"arms"`
	Grants       []roleGrantObject        `json:"grants"`
}

// parsedRole is a validated roleRequest: the arm defaults keyed by arm number
// and the grants resolved to pair ids.
type parsedRole struct {
	arms   []db.RoleArm
	grants []db.RoleGrant
}

// parseRole validates a role body against the project — arm numbers, level
// vocabularies, and that every named pair is actually mapped (REQ-API-057/143).
// A grant at no_access keeps neither right: the denial wins over the right and
// storing it would only invite a reader to wonder (REQ-AUTH-070).
func (h *Handler) parseRole(body roleRequest, projectArms []db.Arm,
	names *rolePairNames) (*parsedRole, string) {

	knownArm := map[int]bool{}
	for _, a := range projectArms {
		knownArm[a.ArmNum] = true
	}
	var arms []db.RoleArm
	for key, levels := range body.Arms {
		armNum, err := strconv.Atoi(key)
		if err != nil || !knownArm[armNum] {
			return nil, "unknown arm number: " + key
		}
		if !dataLevels[levels.Data] {
			return nil, "invalid data level for arm " + key + ": " + levels.Data
		}
		if !exportLevels[levels.Export] {
			return nil, "invalid export level for arm " + key + ": " + levels.Export
		}
		arms = append(arms, db.RoleArm{
			ArmNum: armNum, DataAccessLevel: levels.Data, ExportLevel: levels.Export,
		})
	}

	var grants []db.RoleGrant
	seen := map[[2]int64]bool{}
	for _, g := range body.Grants {
		pair, ok := names.byName[g.Event+"\x00"+g.Instrument]
		if !ok {
			return nil, "unknown instrument-event pair: " + g.Instrument + " at " + g.Event
		}
		if seen[pair] {
			return nil, "duplicate grant for " + g.Instrument + " at " + g.Event
		}
		seen[pair] = true
		if !dataLevels[g.Data] {
			return nil, "invalid data level for " + g.Instrument + " at " + g.Event + ": " + g.Data
		}
		if !exportLevels[g.Export] {
			return nil, "invalid export level for " + g.Instrument + " at " + g.Event + ": " + g.Export
		}
		deleteValues, editSurveys := g.DeleteValues, g.EditSurveys
		if g.Data == "no_access" {
			deleteValues, editSurveys = false, false
		}
		grants = append(grants, db.RoleGrant{
			EventID: pair[0], InstrumentID: pair[1],
			DataAccessLevel: g.Data, ExportLevel: g.Export,
			DeleteValues: deleteValues, EditSurveys: editSurveys,
		})
	}
	return &parsedRole{arms: arms, grants: grants}, ""
}

// roleAuditShape is the permission set as it appears in an audit detail — names
// rather than ids, and never a record value (REQ-AUD-014).
func roleAuditShape(defaults map[string]roleArmLevels, grants []db.RoleGrant,
	names *rolePairNames) map[string]any {

	named := make([]roleGrantObject, 0, len(grants))
	for _, g := range grants {
		n, ok := names.names[pairKeyOf(g.EventID, g.InstrumentID)]
		if !ok {
			continue
		}
		named = append(named, roleGrantObject{
			Event: n[0], Instrument: n[1],
			Data: g.DataAccessLevel, Export: g.ExportLevel,
			DeleteValues: g.DeleteValues, EditSurveys: g.EditSurveys,
		})
	}
	return map[string]any{"arms": defaults, "grants": named}
}

// readRoleBody decodes and validates the §4.7 body shape, rejecting unknown
// attributes the way every other administration write does.
func readRoleBody(w http.ResponseWriter, r *http.Request) (roleRequest, bool) {
	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return roleRequest{}, false
	}
	if !RejectUnknownAttrs(w, supplied, "name", "project_admin", "arms", "grants") {
		return roleRequest{}, false
	}
	var body roleRequest
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return roleRequest{}, false
	}
	if body.Name == "" {
		errBadRequest(w, "name is required")
		return roleRequest{}, false
	}
	return body, true
}

// projectForRoles loads the project and everything a role body is validated
// against; it writes the 400/404 for the caller and reports failure.
func (h *Handler) projectForRoles(w http.ResponseWriter, r *http.Request) (
	int64, []db.Arm, []db.Event, *rolePairNames, bool) {

	projectID, ok := pathID(r, "id")
	if !ok {
		errBadRequest(w, "invalid project id")
		return 0, nil, nil, nil, false
	}
	ctx := r.Context()
	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return 0, nil, nil, nil, false
	}
	if p == nil {
		errNotFound(w)
		return 0, nil, nil, nil, false
	}
	projectArms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return 0, nil, nil, nil, false
	}
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return 0, nil, nil, nil, false
	}
	names, err := h.loadRolePairs(ctx, projectID)
	if err != nil {
		errInternal(w)
		return 0, nil, nil, nil, false
	}
	return projectID, projectArms, events, names, true
}

// --- POST /api/v1/projects/{id}/roles (REQ-API-057) ---

// createRole creates a project role (is_admin). A duplicate name within the
// project is 409 conflict; unknown arm numbers, level values outside the fixed
// vocabularies and unmapped pairs are 400 invalid_request. 201 — the role
// object; audit role_created (§3.4) with the permission set.
func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	projectID, projectArms, events, names, ok := h.projectForRoles(w, r)
	if !ok {
		return
	}
	body, ok := readRoleBody(w, r)
	if !ok {
		return
	}
	dupe, err := h.Store.GetRoleByProjectName(ctx, projectID, body.Name)
	if err != nil {
		errInternal(w)
		return
	}
	if dupe != nil {
		errConflict(w, "role name '"+body.Name+"' already exists in this project")
		return
	}
	parsed, problem := h.parseRole(body, projectArms, names)
	if problem != "" {
		errBadRequest(w, problem)
		return
	}

	role := &db.Role{ProjectID: projectID, RoleName: body.Name, ProjectAdmin: body.ProjectAdmin}
	id, err := h.Store.CreateRole(ctx, role, parsed.arms, parsed.grants)
	if err != nil {
		errInternal(w)
		return
	}
	role.ID = id

	defaults := armDefaults(projectArms, parsed.arms)
	projectAdminInt := 0
	if role.ProjectAdmin {
		projectAdminInt = 1
	}
	shape := roleAuditShape(defaults, parsed.grants, names)
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.RoleCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{
			"name":          role.RoleName,
			"project_admin": projectAdminInt,
			"arms":          shape["arms"],
			"grants":        shape["grants"],
		},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, newRoleObject(*role, defaults,
		grantObjects(parsed.grants, names, defaults, eventArmOf(events, projectArms))))
}

// updateRole replaces a role's name, project_admin flag, arm defaults and pair
// grants in one transaction (REQ-API-143). A pair omitted from grants reverts
// to its arm default; an arms map missing an arm sets that arm to no_access /
// export_none. Unknown {rid} → 404; a name another role already holds → 409; an
// unmapped pair or an unknown level → 400, nothing applied. Audit role_updated
// carries the before and after permission set; members holding the role pick the
// change up on their next request without re-authentication (REQ-AUTH-033).
func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	projectID, projectArms, events, names, ok := h.projectForRoles(w, r)
	if !ok {
		return
	}
	roleID, ok := pathID(r, "rid")
	if !ok {
		errBadRequest(w, "invalid role id")
		return
	}
	role, err := h.Store.GetRole(ctx, roleID)
	if err != nil {
		errInternal(w)
		return
	}
	if role == nil || role.ProjectID != projectID {
		errNotFound(w) // a role of another project is not here (REQ-API-007)
		return
	}
	body, ok := readRoleBody(w, r)
	if !ok {
		return
	}
	dupe, err := h.Store.GetRoleByProjectName(ctx, projectID, body.Name)
	if err != nil {
		errInternal(w)
		return
	}
	if dupe != nil && dupe.ID != role.ID {
		errConflict(w, "role name '"+body.Name+"' already exists in this project")
		return
	}
	parsed, problem := h.parseRole(body, projectArms, names)
	if problem != "" {
		errBadRequest(w, problem)
		return
	}

	// The state before the write, for the audit pair (REQ-AUD-003).
	beforeArms, err := h.Store.ListRoleArms(ctx, role.ID)
	if err != nil {
		errInternal(w)
		return
	}
	beforeGrants, err := h.Store.ListRoleGrants(ctx, role.ID)
	if err != nil {
		errInternal(w)
		return
	}
	before := roleAuditShape(armDefaults(projectArms, beforeArms), beforeGrants, names)

	updated := db.Role{ID: role.ID, ProjectID: projectID, RoleName: body.Name,
		ProjectAdmin: body.ProjectAdmin}
	if err := h.Store.UpdateRole(ctx, &updated, parsed.arms, parsed.grants); err != nil {
		errInternal(w)
		return
	}

	defaults := armDefaults(projectArms, parsed.arms)
	after := roleAuditShape(defaults, parsed.grants, names)
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.RoleUpdated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{
			"role_id": role.ID, "name": body.Name,
			"before": before, "after": after,
		},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, newRoleObject(updated, defaults,
		grantObjects(parsed.grants, names, defaults, eventArmOf(events, projectArms))))
}
