package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/db"
)

// registerProjects mounts §4.5: the visibility-filtered dashboard listing,
// project creation (is_admin, single-arm per REQ-DB-011), the full-metadata
// read (data access ≥ read_only + visibility), and the partial metadata
// update (project_admin).
func (h *Handler) registerProjects(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects", h.listProjects)
	mux.HandleFunc("POST /api/v1/projects", h.createProject)
	mux.HandleFunc("GET /api/v1/projects/{id}", h.getProject)
	mux.HandleFunc("PUT /api/v1/projects/{id}", h.updateProject)
}

// --- project objects (§4.5) ---

// projectObject is the metadata representation of §4.5 — the simplified
// projects table (GD-17): identity and ethics fields only.
type projectObject struct {
	ID               int64   `json:"id"`
	ProjectName      string  `json:"project_name"`
	Organization     string  `json:"organization"`
	PIName           string  `json:"pi_name"`
	PIEmail          string  `json:"pi_email"`
	DMName           *string `json:"dm_name"`
	DMEmail          *string `json:"dm_email"`
	RekNumber        *string `json:"rek_number"`
	RekStartDate     *string `json:"rek_start_date"`
	RekEndDate       *string `json:"rek_end_date"`
	StartDate        *string `json:"start_date"`
	EndDate          *string `json:"end_date"`
	ParticipantNames string  `json:"participant_names"`
	CreationTime     string  `json:"creation_time"`
}

func newProjectObject(p *db.Project) projectObject {
	return projectObject{
		ID: p.ID, ProjectName: p.ProjectName, Organization: p.Organization,
		PIName: p.PIName, PIEmail: p.PIEmail,
		DMName: NullStrPtr(p.DMName), DMEmail: NullStrPtr(p.DMEmail),
		RekNumber: NullStrPtr(p.RekNumber),
		RekStartDate: NullStrPtr(p.RekStartDate), RekEndDate: NullStrPtr(p.RekEndDate),
		StartDate: NullStrPtr(p.StartDate), EndDate: NullStrPtr(p.EndDate),
		ParticipantNames: p.ParticipantNames, CreationTime: p.CreationTime,
	}
}

// projectDetail is the GET/PUT /{id} shape — full metadata plus structure.
type projectDetail struct {
	projectObject
	Arms        []projectArmObject        `json:"arms"`
	Instruments []projectInstrumentObject `json:"instruments"`
}

// projectArmObject is one arm with its events in the canonical order of
// GD-15 (§4.9 shape, embedded per §4.5).
type projectArmObject struct {
	ArmNum int           `json:"arm_num"`
	Name   *string       `json:"name"`
	Events []EventObject `json:"events"`
}

// projectInstrumentObject is the instrument summary of the §4.5 detail,
// carrying its field count.
type projectInstrumentObject struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Position   int    `json:"position"`
	FieldCount int    `json:"field_count"`
}

// buildProjectDetail assembles the full-metadata response: arms with their
// canonically ordered events plus instruments with field counts.
func (h *Handler) buildProjectDetail(ctx context.Context, p *db.Project) (projectDetail, error) {
	d := projectDetail{projectObject: newProjectObject(p)}
	arms, eventsByArm, err := h.CanonicalEvents(ctx, p.ID)
	if err != nil {
		return d, err
	}
	d.Arms = make([]projectArmObject, 0, len(arms))
	for _, a := range arms {
		events := eventsByArm[a.ArmNum]
		if events == nil {
			events = []EventObject{}
		}
		d.Arms = append(d.Arms, projectArmObject{
			ArmNum: a.ArmNum, Name: NullStrPtr(a.Name), Events: events,
		})
	}
	instruments, err := h.Store.ListInstruments(ctx, p.ID)
	if err != nil {
		return d, err
	}
	fields, err := h.Store.ListFields(ctx, p.ID)
	if err != nil {
		return d, err
	}
	fieldCount := map[int64]int{}
	for _, f := range fields {
		fieldCount[f.InstrumentID]++
	}
	d.Instruments = make([]projectInstrumentObject, 0, len(instruments))
	for _, i := range instruments {
		d.Instruments = append(d.Instruments, projectInstrumentObject{
			ID: i.ID, Name: i.Name, Position: i.Position, FieldCount: fieldCount[i.ID],
		})
	}
	return d, nil
}

// --- GET /api/v1/projects (REQ-API-049) ---

// projectSummary is the dashboard row of §4.5.
type projectSummary struct {
	ID              int64  `json:"id"`
	ProjectName     string `json:"project_name"`
	Organization    string `json:"organization"`
	RecordCount     int    `json:"record_count"`
	InstrumentCount int    `json:"instrument_count"`
	FieldCount      int    `json:"field_count"`
}

// listProjects returns the projects within the acting user's visibility —
// is_admin sees all, everyone else only their memberships (REQ-AUTH-026);
// nothing outside that set appears (REQ-API-007).
func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	ctx := r.Context()
	var visible map[int64]bool // nil = all (is_admin)
	if !u.IsAdmin {
		assignments, err := h.Store.ListAssignmentsByUser(ctx, u.ID)
		if err != nil {
			errInternal(w)
			return
		}
		visible = make(map[int64]bool, len(assignments))
		for _, a := range assignments {
			visible[a.ProjectID] = true
		}
	}
	projects, err := h.Store.ListProjects(ctx)
	if err != nil {
		errInternal(w)
		return
	}
	out := make([]projectSummary, 0, len(projects))
	for _, p := range projects {
		if visible != nil && !visible[p.ID] {
			continue
		}
		records, err := h.Store.ListRecordEntities(ctx, p.ID)
		if err != nil {
			errInternal(w)
			return
		}
		instruments, err := h.Store.ListInstruments(ctx, p.ID)
		if err != nil {
			errInternal(w)
			return
		}
		fields, err := h.Store.ListFields(ctx, p.ID)
		if err != nil {
			errInternal(w)
			return
		}
		out = append(out, projectSummary{
			ID: p.ID, ProjectName: p.ProjectName, Organization: p.Organization,
			RecordCount: len(records), InstrumentCount: len(instruments),
			FieldCount: len(fields),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- POST /api/v1/projects (REQ-API-050) ---

// projectAttrs are the creation/update attributes of §4.5. Everything else —
// including the GD-17 removals (end_provision, option_* flags,
// agreed_to_end_user_contract, event_names) — is rejected as unknown
// (REQ-API-052).
var projectAttrs = []string{
	"project_name", "organization", "pi_name", "pi_email",
	"dm_name", "dm_email", "rek_number", "rek_start_date", "rek_end_date",
	"start_date", "end_date", "participant_names",
}

// projectRequest is the POST body; PUT reuses it with presence checked in
// the raw attribute map, so omitted fields stay unchanged (REQ-API-042).
type projectRequest struct {
	ProjectName      string  `json:"project_name"`
	Organization     string  `json:"organization"`
	PIName           string  `json:"pi_name"`
	PIEmail          string  `json:"pi_email"`
	DMName           *string `json:"dm_name"`
	DMEmail          *string `json:"dm_email"`
	RekNumber        *string `json:"rek_number"`
	RekStartDate     *string `json:"rek_start_date"`
	RekEndDate       *string `json:"rek_end_date"`
	StartDate        *string `json:"start_date"`
	EndDate          *string `json:"end_date"`
	ParticipantNames string  `json:"participant_names"`
}

// validDate checks the DATE layout "YYYY-MM-DD" of the schema (§4.5 body).
func validDate(v *string) bool {
	if v == nil {
		return true
	}
	_, err := time.Parse("2006-01-02", *v)
	return err == nil
}

// createProject creates a project (is_admin — master spec). Single-arm per
// REQ-DB-011/DEV-API-4: arm 1 is created here; initial events come through
// §4.9 afterwards (GD-17). A duplicate name is 409 conflict. 201 with the
// project object; audit `project_created`.
func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	u, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, projectAttrs...) {
		return
	}
	var body projectRequest
	raw, _ := json.Marshal(supplied)
	if err := json.Unmarshal(raw, &body); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if body.ProjectName == "" {
		errBadRequest(w, "project_name is required")
		return
	}
	for _, d := range []*string{body.RekStartDate, body.RekEndDate,
		body.StartDate, body.EndDate} {
		if !validDate(d) {
			errBadRequest(w, "dates must use the YYYY-MM-DD format")
			return
		}
	}
	dupe, err := h.Store.GetProjectByName(ctx, body.ProjectName)
	if err != nil {
		errInternal(w)
		return
	}
	if dupe != nil {
		errConflict(w, "project name '"+body.ProjectName+"' already exists")
		return
	}

	p := &db.Project{
		ProjectName: body.ProjectName, Organization: body.Organization,
		PIName: body.PIName, PIEmail: body.PIEmail,
		DMName:   toNullStr(body.DMName), DMEmail: toNullStr(body.DMEmail),
		RekNumber:    toNullStr(body.RekNumber),
		RekStartDate: toNullStr(body.RekStartDate), RekEndDate: toNullStr(body.RekEndDate),
		StartDate: toNullStr(body.StartDate), EndDate: toNullStr(body.EndDate),
		ParticipantNames: body.ParticipantNames,
	}
	id, err := h.Store.CreateProject(ctx, p)
	if err != nil {
		errInternal(w)
		return
	}
	p.ID = id
	// Creation is single-arm (REQ-DB-011): arm 1 only, unnamed.
	if _, err := h.Store.AddArm(ctx, &db.Arm{ProjectID: id, ArmNum: 1}); err != nil {
		errInternal(w)
		return
	}
	if err := h.Audit.Insert(ctx, audit.Entry{
		EventType: audit.ProjectCreated, Source: audit.SourceUI,
		UserID: u.ID, Email: u.Email, ProjectID: id,
		Details: map[string]any{"project_name": p.ProjectName},
	}); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, newProjectObject(p))
}

func toNullStr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// --- GET /api/v1/projects/{id} (REQ-API-051) ---

// getProject returns the full metadata plus structure. Requires data access
// ≥ read_only (Levels.AnyData — one arm suffices, §4.5) and project
// visibility; is_admin is covered by REQ-AUTH-023. A non-member gets the
// uniform 403 that never discloses existence (REQ-API-007).
func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
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
	detail, err := h.buildProjectDetail(ctx, p)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// --- PUT /api/v1/projects/{id} (REQ-API-052) ---

// updateProject applies any subset of the metadata fields (omitted fields
// are unchanged — idempotent, REQ-API-042). project_admin (is_admin covered
// by REQ-AUTH-023). A duplicate project_name is 409 conflict. Metadata
// changes are audit-logged with old and new values (`project_updated`,
// Audit_Logging_Design.md §3.3). 200 — the updated object, same shape as GET.
func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
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
	if !h.requireMember(w, r, lv) || !h.requireProjectAdmin(w, r, lv) {
		return
	}

	var supplied map[string]json.RawMessage
	if err := decodeBody(r, &supplied); err != nil {
		errBadRequest(w, "malformed JSON body")
		return
	}
	if !RejectUnknownAttrs(w, supplied, projectAttrs...) {
		return
	}

	changes := map[string]any{}
	// NOT NULL text fields: a string is required, empty is rejected.
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"project_name", &p.ProjectName}, {"organization", &p.Organization},
		{"pi_name", &p.PIName}, {"pi_email", &p.PIEmail},
		{"participant_names", &p.ParticipantNames},
	} {
		raw, present := supplied[f.key]
		if !present {
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) != nil || v == "" {
			errBadRequest(w, f.key+" must be a non-empty string")
			return
		}
		if v != *f.dst {
			changes[f.key] = map[string]any{"old": *f.dst, "new": v}
			*f.dst = v
		}
	}
	// Nullable text/DATE fields: null clears, a string sets.
	for _, f := range []struct {
		key  string
		dst  *sql.NullString
		date bool
	}{
		{"dm_name", &p.DMName, false}, {"dm_email", &p.DMEmail, false},
		{"rek_number", &p.RekNumber, false},
		{"rek_start_date", &p.RekStartDate, true}, {"rek_end_date", &p.RekEndDate, true},
		{"start_date", &p.StartDate, true}, {"end_date", &p.EndDate, true},
	} {
		raw, present := supplied[f.key]
		if !present {
			continue
		}
		var v *string
		if json.Unmarshal(raw, &v) != nil {
			errBadRequest(w, f.key+" must be a string or null")
			return
		}
		if v != nil && f.date && !validDate(v) {
			errBadRequest(w, f.key+" must use the YYYY-MM-DD format")
			return
		}
		var old any
		if f.dst.Valid {
			old = f.dst.String
		}
		if v == nil {
			if f.dst.Valid {
				changes[f.key] = map[string]any{"old": old, "new": nil}
				*f.dst = sql.NullString{}
			}
			continue
		}
		if !f.dst.Valid || f.dst.String != *v {
			changes[f.key] = map[string]any{"old": old, "new": *v}
			*f.dst = sql.NullString{String: *v, Valid: true}
		}
	}

	if _, changed := changes["project_name"]; changed {
		dupe, err := h.Store.GetProjectByName(ctx, p.ProjectName)
		if err != nil {
			errInternal(w)
			return
		}
		if dupe != nil && dupe.ID != p.ID {
			errConflict(w, "project name '"+p.ProjectName+"' already exists")
			return
		}
	}

	if err := h.Store.UpdateProject(ctx, p); err != nil {
		errInternal(w)
		return
	}
	if len(changes) > 0 {
		if err := h.Audit.Insert(ctx, audit.Entry{
			EventType: audit.ProjectUpdated, Source: audit.SourceUI,
			UserID: u.ID, Email: u.Email, ProjectID: p.ID,
			Details: map[string]any{"changes": changes},
		}); err != nil {
			errInternal(w)
			return
		}
	}
	detail, err := h.buildProjectDetail(ctx, p)
	if err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}
