package admin

// GET /api/v1/projects/{id}/export (API_Endpoints_Design.md §4.14,
// REQ-API-075/076): the UI-surface export of the shared sensitivity pipeline
// (dataapi.streamExport). Unknown query parameters are accepted and ignored
// (REQ-API-017); every successful call is audited as an `export` event with
// surface "ui" (§3.5), rejections carry no audit row (REQ-AUD-004).

import (
	"net/http"
	"strconv"
	"strings"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/dataapi"
)

func (h *Handler) registerExport(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/export", h.exportProject)
}

func (h *Handler) exportProject(w http.ResponseWriter, r *http.Request) {
	u, ok := actor(r)
	if !ok {
		errForbidden(w)
		return
	}
	projectID, ok := pathID(r, "id")
	if !ok {
		errNotFound(w)
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

	q := r.URL.Query()
	format := strings.ToLower(q.Get("format"))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		errBadRequest(w, "format must be csv or json")
		return
	}
	var delimiter rune // 0 = comma in the core (REQ-EXP-013)
	if raw := q.Get("csvDelimiter"); raw != "" {
		rs := []rune(raw)
		if len(rs) != 1 {
			errBadRequest(w, "csvDelimiter must be a single character")
			return
		}
		delimiter = rs[0]
	}

	// Candidate arms (§4.14): the arm= parameters, or — when none is given —
	// every arm the caller may export. An unknown arm number is 404; an arm
	// the caller cannot export is 403. Both answer without an audit row
	// (REQ-AUD-004), and neither discloses more than the uniform shape.
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	known := map[int]bool{}
	for _, a := range arms {
		known[a.ArmNum] = true
	}
	var candidates []int
	if want := q["arm"]; len(want) > 0 {
		seen := map[int]bool{}
		for _, raw := range want {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				errBadRequest(w, "arm must be an arm number")
				return
			}
			if !known[n] {
				errNotFound(w)
				return
			}
			if authz.ExportRank(lv.ExportFor(n)) == 0 {
				errForbidden(w)
				return
			}
			if !seen[n] {
				seen[n] = true
				candidates = append(candidates, n)
			}
		}
	} else {
		for _, a := range arms {
			if authz.ExportRank(lv.ExportFor(a.ArmNum)) > 0 {
				candidates = append(candidates, a.ArmNum)
			}
		}
	}
	if len(candidates) == 0 {
		errForbidden(w) // export_none on every arm — nothing may be exported
		return
	}

	// The applied level is the lowest among the candidate arms (D-4): every
	// row is delivered at a level no weaker than any arm allows, and this
	// exact minimum is what the audit event records.
	level := authz.ExportRank(lv.ExportFor(candidates[0]))
	for _, n := range candidates[1:] {
		if rk := authz.ExportRank(lv.ExportFor(n)); rk < level {
			level = rk
		}
	}

	// Candidate events: the events of the candidate arms. Restricting is
	// always on here — an arm without events exports no rows, never the
	// whole project.
	_, eventsByArm, err := h.CanonicalEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	var eventNames []string
	for _, n := range candidates {
		for _, e := range eventsByArm[n] {
			eventNames = append(eventNames, e.UniqueEventName)
		}
	}

	// Record selection follows the data-access-group rule (REQ-API-092):
	// the acting user's active group scopes which records appear.
	var groupID *int64
	asg, err := h.Store.GetAssignment(ctx, u.ID, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	if asg != nil {
		ms, err := h.Store.ListDAGMembershipsByAssignment(ctx, asg.ID)
		if err != nil {
			errInternal(w)
			return
		}
		for _, m := range ms {
			if m.IsActive {
				gid := m.GroupID
				groupID = &gid
				break
			}
		}
	}

	h.Data.RunExport(w, r, dataapi.ExportSpec{
		ProjectID:         projectID,
		User:              u,
		Surface:           audit.SourceUI,
		Level:             level,
		Arms:              candidates,
		Events:            eventNames,
		RestrictEvents:    true,
		GroupID:           groupID,
		RawOrLabel:        q.Get("rawOrLabel"),
		RawOrLabelHeaders: q.Get("rawOrLabelHeaders"),
		Enc:               format,
		Delimiter:         delimiter,
		Error:             func(w http.ResponseWriter, _ int, _ string) { errInternal(w) },
	})
}
