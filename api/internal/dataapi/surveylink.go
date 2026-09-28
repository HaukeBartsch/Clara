package dataapi

// Survey-link tokens on the data API (API_Endpoints_Design.md §3.10,
// REQ-API-083, REQ-AUTH-039): a link renders and fills exactly one
// (record, instrument) pair, and every submission — accepted or rejected —
// is audit-logged as survey_submitted (Audit_Logging_Design.md §3.6).

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"csms/api/internal/audit"
)

// serveSurveyLink dispatches one call made with a survey-link token. Only
// content=metadata for the link's instrument and content=record&
// action=import for its record are admitted; every other content — export,
// delete, anything else — is the uniform 403 (REQ-API-083). The rate limit
// of §3.9 applies unchanged: it is checked before token resolution in
// ServeHTTP (REQ-API-038).
func (h *Handler) serveSurveyLink(ctx context.Context, w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	switch p.Content {
	case "":
		writeError(w, enc, http.StatusBadRequest, "Invalid content")
	case "metadata":
		// forms[] may narrow the render, never widen it past the link.
		for _, f := range p.Forms {
			if f != sub.Instrument {
				writeError(w, enc, http.StatusForbidden, "Permission denied")
				return
			}
		}
		p.Forms = []string{sub.Instrument}
		// Called directly rather than through requireData: a link holds no
		// arm levels, and §3.10 grants this call by the link itself.
		h.contentMetadata(ctx, w, enc, sub, p)
	case "record":
		// An absent action means export in the REDCap convention, which a
		// link never gets (REQ-API-083).
		if !strings.EqualFold(p.Action, "import") {
			writeError(w, enc, http.StatusForbidden, "Permission denied")
			return
		}
		h.contentRecordImport(w, r, enc, sub, p)
	default:
		writeError(w, enc, http.StatusForbidden, "Permission denied")
	}
}

// surveyFieldChange is one element of the survey_submitted fields array.
type surveyFieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// surveySubmittedEntry builds one survey_submitted audit entry (REQ-AUD-021).
// The link token goes in the fixed token column and never into details
// (REQ-AUTH-041); changes is the submission's old/new map, empty for a
// rejected submission.
func surveySubmittedEntry(sub *subject, recordID, status, reason string, changes map[string]map[string]string) audit.Entry {
	fields := make([]surveyFieldChange, 0, len(changes))
	for name, c := range changes {
		fields = append(fields, surveyFieldChange{Field: name, Old: c["old"], New: c["new"]})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Field < fields[j].Field })

	var reasonValue any // null when there is nothing to say (success)
	if reason != "" {
		reasonValue = reason
	}
	return audit.Entry{
		Token:        sub.Link.Token,
		EventType:    audit.SurveySubmitted,
		Source:       audit.SourceAPI,
		ProjectID:    sub.Project.ID,
		TargetRecord: recordID,
		Details: map[string]any{
			"status":     status,
			"reason":     reasonValue,
			"record_id":  recordID,
			"instrument": sub.Instrument,
			"fields":     fields,
		},
	}
}

// auditSurveyFailure logs a submission the API turned away before any
// transaction opened — with no transaction there is nothing to carry the
// entry, so it is written on its own (REQ-AUD-021 covers failures too).
// Only rows aimed at the link's own pair count as submissions: the 403
// paths (another record or instrument, analysis mode) are permission
// denials, which the data API does not audit for any caller.
func (h *Handler) auditSurveyFailure(ctx context.Context, sub *subject, recordID, reason string) {
	if h.Audit == nil {
		return
	}
	_ = h.Audit.Insert(ctx, surveySubmittedEntry(sub, recordID, "failure", reason, nil))
}
