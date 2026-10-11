package admin

// GET /api/v1/projects/{id}/export (API_Endpoints_Design.md §4.14,
// REQ-API-075/076): the UI-surface export of the shared sensitivity pipeline
// (dataapi.streamExport), so sensitivity follows the same per-(instrument,
// event) resolution as the data API's own export. Unknown query parameters are
// accepted and ignored (REQ-API-017); every successful call is audited as an
// `export` event with surface "ui" (§3.5), rejections carry no audit row
// (REQ-AUD-004).

import (
	"net/http"
	"strconv"
	"strings"

	"csms/api/internal/audit"
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
	// every arm of the project. Which of them contributes is a per-pair question
	// the exporter answers from the dictionary (Data_Export_Design.md §4.3), so
	// an arm holding no permitted pair simply adds no columns and the call is
	// refused only when its whole scope holds none. An unknown arm number is 404;
	// both answer without an audit row (REQ-AUD-004), and neither discloses more
	// than the uniform shape.
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
			if !seen[n] {
				seen[n] = true
				candidates = append(candidates, n)
			}
		}
	} else {
		for _, a := range arms {
			candidates = append(candidates, a.ArmNum)
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
		ProjectID:      projectID,
		User:           u,
		Surface:        audit.SourceUI,
		Arms:           candidates,
		Events:         eventNames,
		RestrictEvents: true,
		GroupID:        groupID,
		// Every column at its own pair's level, arm default underneath (GD-2 as
		// revised, REQ-AUTH-069) — the same resolution the boundary applies to a
		// write, read out for the export instead of re-derived here.
		PairExport: func(eventID int64, armNum int, instrumentID int64) int {
			return lv.PairOrArmDefault(eventID, instrumentID, armNum).ExportRankOf()
		},
		RawOrLabel:        q.Get("rawOrLabel"),
		RawOrLabelHeaders: q.Get("rawOrLabelHeaders"),
		Enc:               format,
		Delimiter:         delimiter,
		// The pipeline's status, rendered in this surface's envelope (§4.2): a
		// scope with no permitted pair is the uniform 403, never a server error.
		Error: func(w http.ResponseWriter, status int, message string) {
			switch status {
			case http.StatusForbidden:
				errForbidden(w)
			case http.StatusBadRequest:
				errBadRequest(w, message)
			default:
				errInternal(w)
			}
		},
	})
}
