package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerRoles mounts §4.7: the role listing and creation. Both endpoints
// require is_admin; roles are project-scoped (REQ-AUTH-024) and not limited
// to preset examples (REQ-AUTH-020).
func (h *Handler) registerRoles(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/roles", h.listRoles)
	mux.HandleFunc("POST /api/v1/projects/{id}/roles", h.createRole)
}

// --- role objects (§4.7) ---

// roleArmLevels is one per-arm grant of a role (REQ-DB-009).
type roleArmLevels struct {
	Data   string `json:"data"`
	Export string `json:"export"`
}

// roleObject is the §4.7 role representation: the arms map keys are arm
// numbers as strings; every project arm appears, ungranted arms at their
// no-access default (REQ-AUTH-019 — no implicit access).
type roleObject struct {
	ID           int64                    `json:"id"`
	Name         string                   `json:"name"`
	ProjectAdmin bool                     `json:"project_admin"`
	Arms         map[string]roleArmLevels `json:"arms"`
}

// dataLevels and exportLevels are the fixed level vocabularies of
// Authentication_Authorization_Design.md §4.1.
var (
	dataLevels = map[string]bool{
		"no_access": true, "read_only": true, "view_edit": true,
		"delete": true, "edit_survey_responses": true,
	}
	exportLevels = map[string]bool{
		"export_none": true, "export_de_identified": true,
		"export_no_identifiers": true, "export_full": true,
	}
)

// roleArmsByProject loads each role's per-arm grants keyed by role id.
func (h *Handler) roleArmsByProject(ctx context.Context, roles []db.Role) (map[int64]map[string]roleArmLevels, error) {
	out := make(map[int64]map[string]roleArmLevels, len(roles))
	for _, r := range roles {
		arms, err := h.Store.ListRoleArms(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		grants := map[string]roleArmLevels{}
		for _, ra := range arms {
			grants[strconv.Itoa(ra.ArmNum)] = roleArmLevels{
				Data: ra.DataAccessLevel, Export: ra.ExportLevel,
			}
		}
		out[r.ID] = grants
	}
	return out, nil
}

// newRoleObject maps a role plus the project's arms to the §4.7 shape; every
// project arm is present, defaulted to no_access / export_none when the role
// grants nothing on it (the reading authz.Effective applies).
func newRoleObject(r db.Role, projectArms []db.Arm, grants map[string]roleArmLevels) roleObject {
	arms := make(map[string]roleArmLevels, len(projectArms))
	for _, a := range projectArms {
		key := strconv.Itoa(a.ArmNum)
		if g, ok := grants[key]; ok {
			arms[key] = g
			continue
		}
		arms[key] = roleArmLevels{Data: "no_access", Export: "export_none"}
	}
	return roleObject{ID: r.ID, Name: r.RoleName, ProjectAdmin: r.ProjectAdmin, Arms: arms}
}

// --- GET /api/v1/projects/{id}/roles (REQ-API-056) ---

// listRoles returns the project's roles with their per-arm levels. is_admin.
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
	grantsByRole, err := h.roleArmsByProject(ctx, roles)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]roleObject, 0, len(roles))
	for _, role := range roles {
		out = append(out, newRoleObject(role, projectArms, grantsByRole[role.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- POST /api/v1/projects/{id}/roles (REQ-API-057) ---

// createRoleRequest is the §4.7 body: the role object minus id. An arm
// absent from arms defaults to no_access / export_none (REQ-AUTH-019).
type createRoleRequest struct {
	Name         string                    `json:"name"`
	ProjectAdmin bool                      `json:"project_admin"`
	Arms         map[string]roleArmLevels  `json:"arms"`
}

// createRole creates a project role (is_admin). A duplicate name within the
// project is 409 conflict; unknown arm numbers and level values outside the
// fixed vocabularies are 400 invalid_request. 201 — the role object; audit
// role_created (§3.4) with the granted arms.
func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.Store.GetProject(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if p == nil {
		errNotFound(w)
		return
	}

	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, "name", "project_admin", "arms") {
		return
	}
	var body createRoleRequest
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.Name == "" {
		errBadRequest(w, "name is required")
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

	// Validate the per-arm grants against the project's arms and the fixed
	// level vocabularies (REQ-DB-009).
	projectArms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	knownArm := map[int]bool{}
	for _, a := range projectArms {
		knownArm[a.ArmNum] = true
	}
	var roleArms []db.RoleArm
	for key, levels := range body.Arms {
		armNum, err := strconv.Atoi(key)
		if err != nil || !knownArm[armNum] {
			errBadRequest(w, "unknown arm number: "+key)
			return
		}
		if !dataLevels[levels.Data] {
			errBadRequest(w, "invalid data level for arm "+key+": "+levels.Data)
			return
		}
		if !exportLevels[levels.Export] {
			errBadRequest(w, "invalid export level for arm "+key+": "+levels.Export)
			return
		}
		roleArms = append(roleArms, db.RoleArm{
			ArmNum: armNum, DataAccessLevel: levels.Data, ExportLevel: levels.Export,
		})
	}

	role := &db.Role{ProjectID: projectID, RoleName: body.Name, ProjectAdmin: body.ProjectAdmin}
	id, err := h.Store.CreateRole(ctx, role, roleArms)
	if err != nil {
		errInternal(w)
		return
	}
	role.ID = id

	auditArms := map[string]roleArmLevels{}
	for _, ra := range roleArms {
		auditArms[strconv.Itoa(ra.ArmNum)] = roleArmLevels{
			Data: ra.DataAccessLevel, Export: ra.ExportLevel,
		}
	}
	projectAdminInt := 0
	if role.ProjectAdmin {
		projectAdminInt = 1
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.RoleCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: projectID,
		Details: map[string]any{
			"name": role.RoleName, "project_admin": projectAdminInt, "arms": auditArms,
		},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, newRoleObject(*role, projectArms, auditArms))
}
