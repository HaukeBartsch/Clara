package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerMembers mounts §4.6: the member listing and the single mutation
// point for membership (REQ-API-053…055), plus the self-service token fetch
// (REQ-API-102). The two administrative endpoints require is_admin; the
// self-service fetch requires only that the acting user is the member.
func (h *Handler) registerMembers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/users", h.listMembers)
	mux.HandleFunc("PUT /api/v1/projects/{id}/users/{uid}", h.putMember)
	mux.HandleFunc("GET /api/v1/projects/{id}/users/{uid}/token", h.fetchOwnToken)
}

// --- member objects (§4.6) ---

// memberObject is the list representation: token_present reports existence
// without revealing the value (REQ-API-005); role is null for a role-less
// member, who then holds full permissions (REQ-AUTH-022).
type memberObject struct {
	UserID       int64   `json:"user_id"`
	Email        string  `json:"email"`
	DisplayName  string  `json:"display_name"`
	Role         *string `json:"role"`
	TokenPresent bool    `json:"token_present"`
	Enabled      bool    `json:"enabled"`
}

// memberWithToken is the add/rotation response — the only place the token
// value appears (REQ-API-055); never logged, never in audit details.
type memberWithToken struct {
	UserID      int64   `json:"user_id"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Role        *string `json:"role"`
	Token       string  `json:"token"`
}

// roleName resolves a role id to its name (nil = role-less).
func roleName(roles map[int64]string, roleID sql.NullInt64) *string {
	if !roleID.Valid {
		return nil
	}
	if n, ok := roles[roleID.Int64]; ok {
		return &n
	}
	return nil
}

// projectRoles maps the project's role ids to names for member listings.
func (h *Handler) projectRoles(ctx context.Context, projectID int64) (map[int64]string, error) {
	roles, err := h.Store.ListRoles(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(roles))
	for _, r := range roles {
		out[r.ID] = r.RoleName
	}
	return out, nil
}

// --- GET /api/v1/projects/{id}/users (REQ-API-053) ---

// listMembers returns the project's members. is_admin only.
func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
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
	assignments, err := h.Store.ListAssignments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	roles, err := h.projectRoles(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]memberObject, 0, len(assignments))
	for _, a := range assignments {
		u, err := h.Store.GetUser(ctx, a.UserID)
		if err != nil {
			errInternal(w)
			return
		}
		if u == nil { // dangling membership — never happens under FK integrity
			continue
		}
		out = append(out, memberObject{
			UserID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
			Role: roleName(roles, a.RoleID), TokenPresent: a.Token != "",
			Enabled: u.Enabled,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- PUT /api/v1/projects/{id}/users/{uid} (REQ-API-054/055) ---

// memberRequest is the §4.6 single mutation point: at most one of role
// (null or omitted = role-less), rotate_token, remove — a bare body adds or
// keeps a role-less member.
type memberRequest struct {
	Role        *json.RawMessage `json:"role"`
	RotateToken bool             `json:"rotate_token"`
	Remove      bool             `json:"remove"`
}

// putMember is the single mutation point for membership (REQ-API-054):
// add (201 + new token), role change (200), token rotation (200 + new
// token), removal (200). is_admin only.
func (h *Handler) putMember(w http.ResponseWriter, r *http.Request) {
	actorUser, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
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
	if !RejectUnknownAttrs(w, supplied, "role", "rotate_token", "remove") {
		return
	}
	var body memberRequest
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	// The three mutations are mutually exclusive; a bare body is the role
	// mutation with a null role (omitted = role-less, REQ-AUTH-022).
	actions := 0
	for _, k := range []string{"role", "rotate_token", "remove"} {
		if _, present := supplied[k]; present {
			actions++
		}
	}
	if actions > 1 {
		errBadRequest(w, `"role", "rotate_token" and "remove" are mutually exclusive`)
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
	target, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		errInternal(w)
		return
	}
	if target == nil {
		errNotFound(w)
		return
	}

	switch {
	case body.Remove:
		h.removeMember(w, ctx, actorUser, projectID, target)
	case body.RotateToken:
		h.rotateMemberToken(w, ctx, actorUser, projectID, target)
	default:
		h.addOrChangeRole(w, ctx, actorUser, projectID, target, supplied["role"])
	}
}

// auditMember writes one §3.4 administration entry for a membership change;
// the token value never enters details (REQ-API-005).
func (h *Handler) auditMember(ctx context.Context, actorUser *db.User, projectID int64, eventType string, details map[string]any) error {
	return h.Audit.Insert(ctx, audit.Entry{
		EventType: eventType, Source: audit.SourceUI,
		UserID: actorUser.ID, Email: actorUser.Email, ProjectID: projectID,
		Details: details,
	})
}

// removeMember deletes the membership; its token is invalid immediately
// (REQ-API-055, REQ-AUTH-030). Idempotent: removing a non-member is a 200
// no-op without audit entries (REQ-API-042).
func (h *Handler) removeMember(w http.ResponseWriter, ctx context.Context, actorUser *db.User, projectID int64, target *db.User) {
	a, err := h.Store.GetAssignment(ctx, target.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := h.Store.RemoveAssignment(ctx, target.ID, projectID); err != nil {
		errInternal(w)
		return
	}
	if err := h.auditMember(ctx, actorUser, projectID, audit.MembershipChanged, map[string]any{
		"member_email": target.Email, "action": "remove", "role": nil,
	}); err != nil {
		errInternal(w)
		return
	}
	if err := h.auditMember(ctx, actorUser, projectID, audit.TokenRevoked, map[string]any{
		"member_email": target.Email, "reason": "member_removed",
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// rotateMemberToken replaces the member's token; the previous one is invalid
// immediately (REQ-AUTH-030, REQ-API-055). The new value is returned once.
func (h *Handler) rotateMemberToken(w http.ResponseWriter, ctx context.Context, actorUser *db.User, projectID int64, target *db.User) {
	a, err := h.Store.GetAssignment(ctx, target.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if a == nil {
		errNotFound(w)
		return
	}
	token, err := h.Store.RotateAssignmentToken(ctx, target.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if err := h.auditMember(ctx, actorUser, projectID, audit.TokenRotated, map[string]any{
		"member_email": target.Email,
	}); err != nil {
		errInternal(w)
		return
	}
	roles, err := h.projectRoles(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, memberWithToken{
		UserID: target.ID, Email: target.Email, DisplayName: target.DisplayName,
		Role: roleName(roles, a.RoleID), Token: token,
	})
}

// addOrChangeRole applies the role mutation: an existing membership gets a
// role change (200, no token in the response); a missing one is added with
// a fresh token (201, REQ-API-055). A null or omitted role means role-less =
// full permissions (REQ-AUTH-022).
func (h *Handler) addOrChangeRole(w http.ResponseWriter, ctx context.Context, actorUser *db.User, projectID int64, target *db.User, roleRaw json.RawMessage) {
	roleNameStr, roleID, ok := h.resolveRole(w, ctx, projectID, roleRaw)
	if !ok {
		return
	}
	a, err := h.Store.GetAssignment(ctx, target.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}

	if a != nil {
		if err := h.Store.SetAssignmentRole(ctx, target.ID, projectID, roleID); err != nil {
			errInternal(w)
			return
		}
		if err := h.auditMember(ctx, actorUser, projectID, audit.MembershipChanged, map[string]any{
			"member_email": target.Email, "action": "role_change", "role": roleNameStr,
		}); err != nil {
			errInternal(w)
			return
		}
		writeJSON(w, http.StatusOK, memberObject{
			UserID: target.ID, Email: target.Email, DisplayName: target.DisplayName,
			Role: roleNameStr, TokenPresent: a.Token != "", Enabled: target.Enabled,
		})
		return
	}

	newAssignment := &db.Assignment{UserID: target.ID, ProjectID: projectID, RoleID: roleID}
	if _, err := h.Store.AddAssignment(ctx, newAssignment); err != nil {
		errInternal(w)
		return
	}
	if err := h.auditMember(ctx, actorUser, projectID, audit.MembershipChanged, map[string]any{
		"member_email": target.Email, "action": "add", "role": roleNameStr,
	}); err != nil {
		errInternal(w)
		return
	}
	if err := h.auditMember(ctx, actorUser, projectID, audit.TokenIssued, map[string]any{
		"member_email": target.Email,
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, memberWithToken{
		UserID: target.ID, Email: target.Email, DisplayName: target.DisplayName,
		Role: roleNameStr, Token: newAssignment.Token,
	})
}

// resolveRole maps the body's role attribute to (name, role id). null or
// omitted = role-less (REQ-AUTH-022); a name must exist in this project —
// an unknown one is an invalid attribute (400 invalid_request).
func (h *Handler) resolveRole(w http.ResponseWriter, ctx context.Context, projectID int64, roleRaw json.RawMessage) (*string, sql.NullInt64, bool) {
	var name *string
	if len(roleRaw) > 0 {
		if err := json.Unmarshal(roleRaw, &name); err != nil {
			errBadRequest(w, "role must be a string or null")
			return nil, sql.NullInt64{}, false
		}
	}
	if name == nil {
		return nil, sql.NullInt64{}, true
	}
	role, err := h.Store.GetRoleByProjectName(ctx, projectID, *name)
	if err != nil {
		errInternal(w)
		return nil, sql.NullInt64{}, false
	}
	if role == nil {
		errBadRequest(w, "unknown role for this project")
		return nil, sql.NullInt64{}, false
	}
	return &role.RoleName, sql.NullInt64{Int64: role.ID, Valid: true}, true
}

// --- GET /api/v1/projects/{id}/users/{uid}/token (REQ-API-102) ---

// fetchOwnToken is the self-service token fetch: uid must equal the acting
// user and the acting user must be a member; every other case is the uniform
// 403 that never discloses existence (REQ-API-007). No audit event — the
// data-API calls carrying the token log it in the fixed token column.
func (h *Handler) fetchOwnToken(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errForbidden(w)
		return
	}
	userID, ok := pathID(r, "uid")
	if !ok || userID != u.ID {
		errForbidden(w)
		return
	}
	a, err := h.Store.GetAssignment(r.Context(), u.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if a == nil {
		errForbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": a.Token})
}
