package dataapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// projectInfo is the content=project object (API_Endpoints_Design.md
// §3.3). Keys the system does not store carry neutral "0"/"" values so
// naive parsers keep working (REQ-API-018); struct field order is the
// response key order and the CSV column order.
type projectInfo struct {
	ProjectID              string `json:"project_id"`
	ProjectName            string `json:"project_name"`
	ProjectTitle           string `json:"project_title"`
	ProjectDescription     string `json:"project_description"`
	ProjectPiName          string `json:"project_pi_name"`
	ProjectPiEmail         string `json:"project_pi_email"`
	ProjectRekNumber       string `json:"project_rek_number"`
	ProjectRekStartDate    string `json:"project_rek_start_date"`
	ProjectRekEndDate      string `json:"project_rek_end_date"`
	ProjectStartDate       string `json:"project_start_date"`
	ProjectEndDate         string `json:"project_end_date"`
	ProjectEndProvision    string `json:"project_end_provision"`
	ProjectOrganizational  string `json:"project_organizational"`
	ProjectCreationTime    string `json:"project_creation_time"`
	ProjectPatImportFolder string `json:"project_pat_import_folder"`
	SurveysEnabled         string `json:"surveys_enabled"`
	RandomizationEnabled   string `json:"randomization_enabled"`
	// Classic REDCap keys, neutral values (REQ-API-018).
	CreationTime                    string `json:"creation_time"`
	ProductionTime                  string `json:"production_time"`
	InProduction                    string `json:"in_production"`
	ProjectLanguage                 string `json:"project_language"`
	Purpose                         string `json:"purpose"`
	PurposeOther                    string `json:"purpose_other"`
	ProjectNotes                    string `json:"project_notes"`
	CustomRecordLabel               string `json:"custom_record_label"`
	SecondaryUniqueField            string `json:"secondary_unique_field"`
	IsLongitudinal                  string `json:"is_longitudinal"`
	HasRepeatingInstrumentsOrEvents string `json:"has_repeating_instruments_or_events"`
	SchedulingEnabled               string `json:"scheduling_enabled"`
	RecordAutonumberingEnabled      string `json:"record_autonumbering_enabled"`
	DDPEnabled                      string `json:"ddp_enabled"`
	ProjectIRBNumber                string `json:"project_irb_number"`
	ProjectGrantNumber              string `json:"project_grant_number"`
	ProjectPiFirstName              string `json:"project_pi_firstname"`
	ProjectPiLastName               string `json:"project_pi_lastname"`
	DisplayTodayNowButton           string `json:"display_today_now_button"`
	MissingDataCodes                string `json:"missing_data_codes"`
	ExternalModules                 string `json:"external_modules"`
	BypassBranchingEraseFieldPrompt string `json:"bypass_branching_erase_field_prompt"`
}

func (h *Handler) contentProject(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	pr := sub.Project
	info := projectInfo{
		ProjectID:             strconv.FormatInt(pr.ID, 10),
		ProjectName:           pr.ProjectName,
		ProjectTitle:          pr.ProjectName, // no separate title is stored
		ProjectPiName:         pr.PIName,
		ProjectPiEmail:        pr.PIEmail,
		ProjectRekNumber:      pr.RekNumber.String,
		ProjectRekStartDate:   pr.RekStartDate.String,
		ProjectRekEndDate:     pr.RekEndDate.String,
		ProjectStartDate:      pr.StartDate.String,
		ProjectEndDate:        pr.EndDate.String,
		ProjectOrganizational: pr.Organization,
		ProjectCreationTime:   pr.CreationTime,
		ProjectLanguage:       "English",
		ProjectPiFirstName:    pr.PIName, // first name is not stored separately
		DisplayTodayNowButton: "1",
	}
	if ins, err := h.Store.ListInstruments(ctx, pr.ID); err != nil {
		h.storeError(w, enc)
		return
	} else {
		for _, i := range ins {
			if i.IsSurvey {
				info.SurveysEnabled = "1"
				break
			}
		}
	}
	if evs, err := h.Store.ListEvents(ctx, pr.ID); err != nil {
		h.storeError(w, enc)
		return
	} else if len(evs) > 0 {
		info.IsLongitudinal = "1"
	}
	render(w, enc, p.Delimiter(), []projectInfo{info})
}

// eventRow is one content=event object (REQ-API-020).
type eventRow struct {
	EventName       string `json:"event_name"`
	ArmNum          int    `json:"arm_num"`
	UniqueEventName string `json:"unique_event_name"`
	EventID         int64  `json:"event_id"`
}

// eventRows lists the project's events in the canonical per-arm order
// (GD-15, REQ-API-020): arms by arm_num, then within each arm the
// timepoint events by period and the no-timepoint events by position.
func (h *Handler) eventRows(ctx context.Context, projectID int64) ([]eventRow, error) {
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rows := make([]eventRow, 0)
	for _, a := range arms {
		evs, err := h.Store.ListEventsByArm(ctx, projectID, a.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			rows = append(rows, eventRow{
				EventName:       e.EventName,
				ArmNum:          a.ArmNum,
				UniqueEventName: e.UniqueEventName,
				EventID:         e.ID,
			})
		}
	}
	return rows, nil
}

func (h *Handler) contentEvent(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	rows, err := h.eventRows(ctx, sub.Project.ID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	render(w, enc, p.Delimiter(), rows)
}

// metaRow is one content=metadata object (REQ-API-019); matrix rows are
// ordinary field rows sharing matrix_group (REQ-DB-014).
type metaRow struct {
	FieldName        string `json:"field_name"`
	FormName         string `json:"form_name"`
	SectionHeader    string `json:"section_header"`
	FieldType        string `json:"field_type"`
	FieldLabel       string `json:"field_label"`
	FieldNote        string `json:"field_note"`
	ChoiceCodes      string `json:"choice_codes"`
	ChoiceLabels     string `json:"choice_labels"`
	ValidationType   string `json:"validation_type"`
	ValidationMin    string `json:"validation_min"`
	ValidationMax    string `json:"validation_max"`
	RequiredField    string `json:"required_field"`
	BranchingLogic   string `json:"branching_logic"`
	MatrixGroupName  string `json:"matrix_group_name"`
	RecordIdentifier string `json:"record_identifier"`
}

func (h *Handler) contentMetadata(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	pid := sub.Project.ID
	ins, err := h.Store.ListInstruments(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	form := map[int64]string{}
	for _, i := range ins {
		form[i.ID] = i.Name
	}
	fields, err := h.Store.ListFields(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	rows := make([]metaRow, 0, len(fields))
	for i, f := range fields {
		codes, labels := splitChoices(f.Choices.String)
		rows = append(rows, metaRow{
			FieldName:       f.FieldName,
			FormName:        form[f.InstrumentID],
			SectionHeader:   f.SectionHeader.String,
			FieldType:       f.FieldType,
			FieldLabel:      f.FieldLabel.String,
			FieldNote:       f.FieldNote.String,
			ChoiceCodes:     codes,
			ChoiceLabels:    labels,
			ValidationType:  f.ValidationType.String,
			ValidationMin:   f.ValidationMin.String,
			ValidationMax:   f.ValidationMax.String,
			RequiredField:   yn(f.Required),
			BranchingLogic:  f.BranchingLogic.String,
			MatrixGroupName: f.MatrixGroup.String,
			// GD-8: the record identifier is the first field of the
			// first instrument (ListFields is in that order).
			RecordIdentifier: yn(i == 0),
		})
	}
	render(w, enc, p.Delimiter(), rows)
}

// splitChoices expands the stored code$label##code$label encoding into
// the comma-joined choice_codes and choice_labels (§3.4).
func splitChoices(s string) (codes, labels string) {
	if s == "" {
		return "", ""
	}
	c, l := []string{}, []string{}
	for _, pair := range strings.Split(s, "##") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "$", 2)
		c = append(c, kv[0])
		if len(kv) == 2 {
			l = append(l, kv[1])
		} else {
			l = append(l, kv[0])
		}
	}
	return strings.Join(c, ","), strings.Join(l, ",")
}

func yn(b bool) string {
	if b {
		return "Y"
	}
	return ""
}

// mappingRow is one content=formEventMapping object (REQ-API-021).
type mappingRow struct {
	FormName         string `json:"form_name"`
	EventName        string `json:"event_name"`
	ArmNum           int    `json:"arm_num"`
	UniqueEventName  string `json:"unique_event_name"`
	FormEventMapping string `json:"form_event_mapping"`
}

func (h *Handler) contentFormEventMapping(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	pid := sub.Project.ID

	// Canonical per-arm event sequence (GD-15), so arm_num, event name and
	// unique name line up with content=event (eventRows is in that order).
	events, err := h.eventRows(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}

	// Mapped (form, event) pairs, keyed for O(1) lookup.
	pairs, err := h.Store.ListInstrumentEvents(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	mapped := make(map[int64]map[int64]bool)
	for _, pr := range pairs {
		if mapped[pr.InstrumentID] == nil {
			mapped[pr.InstrumentID] = map[int64]bool{}
		}
		mapped[pr.InstrumentID][pr.EventID] = true
	}

	// Instruments in position order; within each the events in the canonical
	// per-arm order (GD-15). Only mapped pairs are emitted; the flag is "1".
	ins, err := h.Store.ListInstruments(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	rows := make([]mappingRow, 0, len(pairs))
	for _, i := range ins {
		m := mapped[i.ID]
		if m == nil {
			continue
		}
		for _, e := range events {
			if !m[e.EventID] {
				continue
			}
			rows = append(rows, mappingRow{
				FormName:         i.Name,
				EventName:        e.EventName,
				ArmNum:           e.ArmNum,
				UniqueEventName:  e.UniqueEventName,
				FormEventMapping: "1",
			})
		}
	}
	render(w, enc, p.Delimiter(), rows)
}

// fieldNameRow is one content=exportFieldNames object (REQ-API-022).
type fieldNameRow struct {
	FieldName string `json:"field_name"`
	FormName  string `json:"form_name"`
}

func (h *Handler) contentExportFieldNames(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	pid := sub.Project.ID
	ins, err := h.Store.ListInstruments(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	form := make(map[int64]string, len(ins))
	for _, i := range ins {
		form[i.ID] = i.Name
	}

	// Restrict to the requested forms when supplied (REQ-API-022); an empty
	// forms[] means all fields.
	var want map[string]bool
	if len(p.Forms) > 0 {
		want = make(map[string]bool, len(p.Forms))
		for _, f := range p.Forms {
			want[f] = true
		}
	}

	fields, err := h.Store.ListFields(ctx, pid)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	rows := make([]fieldNameRow, 0, len(fields))
	for _, f := range fields {
		name := form[f.InstrumentID]
		if want != nil && !want[name] {
			continue
		}
		rows = append(rows, fieldNameRow{FieldName: f.FieldName, FormName: name})
	}
	render(w, enc, p.Delimiter(), rows)
}

// nextNameRow is the content=generateNextRecordName object
// (REQ-API-023), rendered as a single-row array like the other contents.
type nextNameRow struct {
	NextRecordName string `json:"next_record_name"`
}

func (h *Handler) contentGenerateNextRecordName(ctx context.Context, w http.ResponseWriter, enc string, sub *subject, p Params) {
	maxID, _, err := h.Store.MaxRecordID(ctx, sub.Project.ID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	render(w, enc, p.Delimiter(), []nextNameRow{{NextRecordName: nextRecordName(sub.Project.ParticipantNames, maxID)}})
}

// nextRecordName computes the next record name for the project's naming
// pattern (REQ-DB-007, REQ-API-023) from the greatest existing name
// (existingMax, "" when the project has none). Two styles are supported:
//
//   - digit placeholder: "8DISC[0-9][0-9][0-9]" -> fixed prefix plus a
//     zero-padded counter of one digit per "[0-9]", e.g. 8DISC042;
//   - counter prefix:    "0001_01" -> the leading digit run is the counter
//     (width preserved) followed by the fixed remainder, e.g. 0002_01.
//
// The result is one counter step above the greatest existing name of the
// same shape, so it never collides (REQ-API-023); the counter extends past
// the pattern's digit width once the range is exhausted. Names produced by
// this API share a counter width, which is what makes "greatest + 1" safe.
func nextRecordName(pattern, existingMax string) string {
	if i := strings.Index(pattern, "[0-9]"); i >= 0 {
		prefix := pattern[:i]
		width := strings.Count(pattern, "[0-9]")
		if width < 1 {
			width = 1
		}
		counter := 0
		if rest, ok := strings.CutPrefix(existingMax, prefix); ok {
			counter, _ = strconv.Atoi(rest) // non-numeric rest -> stays 0
		}
		return prefix + pad(counter+1, width)
	}

	digits := 0
	for digits < len(pattern) && pattern[digits] >= '0' && pattern[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		digits = 4 // no counter in the pattern: still emit a usable name
	}
	suffix := pattern[digits:]
	counter := 0
	if head, ok := strings.CutSuffix(existingMax, suffix); ok {
		counter, _ = strconv.Atoi(head) // non-numeric head -> stays 0
	}
	return pad(counter+1, digits) + suffix
}

// pad formats n as a base-10 integer with at least width leading zeros;
// n is never truncated, so a full range extends past width digits.
func pad(n, width int) string {
	s := strconv.Itoa(n)
	if len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	return s
}
