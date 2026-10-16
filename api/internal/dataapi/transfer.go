package dataapi

// Cross-project transfers (REQ-API-165, REQ-VAL-046, REQ-AUD-034): a `transfer`
// field is a button whose instruction text (fields.choices) creates one record
// in another project and copies named values of the current record into it.
// This file is the engine; the route lives in admin/transfer.go, because
// cross-project authorization needs the acting account — which the admin API
// knows (X-Internal-User-Id) and a single-project data-API token never can.
// One database serves every project (ASM-API-6), so the source read and the
// target write ride one transaction, and audit entries carry their own project
// per row: both projects' trail commits or rolls back together (REQ-AUD-003).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// TransferResult is the body an executed transfer answers (REQ-API-165): what
// the button did, never the values it carried.
type TransferResult struct {
	TargetProject string   `json:"target_project"`
	TargetRecord  string   `json:"target_record"`
	Created       bool     `json:"created"`
	Copied        []string `json:"copied"`
	SkippedEmpty  []string `json:"skipped_empty"`
}

// TransferFieldStatus is one entry of the availability probe (REQ-API-166):
// whether this actor may run this transfer field here right now.
type TransferFieldStatus struct {
	FieldName string `json:"field_name"`
	Enabled   bool   `json:"enabled"`
}

// TransferConfigError reports that a transfer cannot be executed against the
// current design or data: malformed instructions, an unresolvable target, or a
// source value the target dictionary rejects. The route renders it as
// validation_error (REQ-API-165); design-time validation runs the same
// resolution when the field is saved (REQ-DB-050), so a config that saved
// executes unless the target's design moved under it. ProjectMissing marks the
// one disclosure split: at design time an unknown target project is named
// (REQ-DB-050); at runtime the route answers ErrTransferTargetAccess instead.
type TransferConfigError struct {
	Msg            string
	ProjectMissing bool
}

func (e *TransferConfigError) Error() string { return e.Msg }

// ErrTransferTargetAccess is the single runtime answer for a target the actor
// may not write: unknown project, no membership, a missing pair right, or an
// existing record outside the actor's data access group — one uniform 403
// (REQ-API-007), so executing a transfer never probes which projects exist or
// what records they hold.
var ErrTransferTargetAccess = errors.New("transfer: target access denied")

// transferWrite is one resolved write of a transfer plan.
type transferWrite struct {
	kind        string // "create" (an identifier row) or "copy"
	sourceField string // copy: the source field name; create: ""
	event       string // resolved unique_event_name, "" for an event-less target
	field       string // target field name
}

// transferPlan is a parsed instruction text resolved against everything it can
// reach without record values: the target project, its dictionary, events and
// fields. The create operation is expanded to one identifier row per distinct
// written event — exactly the events the copies name — so an event-enabled
// target cannot be addressed by a create alone: nothing else would say where
// the record's form values live (REQ-API-165).
type transferPlan struct {
	project       *db.Project
	dict          *projectDict
	identifier    string // the target project's record identifier field
	createSource  string // the source field whose value names the target record
	writes        []transferWrite // create rows first (config order), then copies
	eventsInOrder []string        // distinct events, first-appearance order
}

// ResolveTransferTarget runs the target-side half of transfer validation —
// grammar, single project, identifier match, event and field resolution —
// against the live design, reading no record values and checking no
// permissions. The admin design-time check (REQ-DB-050) calls it and discloses
// its messages in full, including an unknown target project name: the one
// deliberate bounded disclosure, never made at runtime (REQ-API-165).
func (h *Handler) ResolveTransferTarget(ctx context.Context, config string) error {
	_, err := h.resolveTransfer(ctx, config)
	return err
}

// resolveTransfer parses and resolves a transfer field's instruction text
// against the live target design. It reads no record values and checks no
// permissions — the executor layers those on; the design-time validator in
// admin/structure.go runs exactly this call for its target-side checks.
func (h *Handler) resolveTransfer(ctx context.Context, config string) (*transferPlan, error) {
	ops, err := validate.ParseTransferConfig(config)
	if err != nil {
		return nil, &TransferConfigError{Msg: err.Error()}
	}
	name := ops[0].TargetProject
	for _, op := range ops[1:] {
		if op.TargetProject != name {
			return nil, &TransferConfigError{Msg: fmt.Sprintf(
				"all operations must address one target project, not both '%s' and '%s'", name, op.TargetProject)}
		}
	}
	proj, err := h.Store.GetProjectByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if proj == nil {
		return nil, &TransferConfigError{Msg: "no project named '" + name + "'", ProjectMissing: true}
	}
	d, err := h.loadDict(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	if d.identifier == "" {
		return nil, &TransferConfigError{Msg: "the target project '" + name + "' has no fields yet"}
	}
	if ops[0].RecordIDField != d.identifier {
		return nil, &TransferConfigError{Msg: fmt.Sprintf(
			"the create operation must name the target project's record identifier field '%s', not '%s'",
			d.identifier, ops[0].RecordIDField)}
	}

	var copies []transferWrite
	var eventsInOrder []string
	seenEvent := map[string]bool{}
	for _, op := range ops[1:] {
		ev, err := resolveTransferEvent(d, name, op.Event)
		if err != nil {
			return nil, err
		}
		tf, ok := d.byName[op.TargetField]
		if !ok {
			return nil, &TransferConfigError{Msg: "the target project '" + name + "' has no field '" + op.TargetField + "'"}
		}
		if !transferValueField(tf) {
			return nil, &TransferConfigError{Msg: "'" + op.TargetField + "' of the target project cannot receive a transferred value"}
		}
		copies = append(copies, transferWrite{kind: "copy", sourceField: op.SourceField, event: ev, field: op.TargetField})
		if !seenEvent[ev] {
			seenEvent[ev] = true
			eventsInOrder = append(eventsInOrder, ev)
		}
	}
	if d.hasEvents && len(copies) == 0 {
		return nil, &TransferConfigError{Msg: "a transfer into the event-enabled project '" + name +
			"' needs at least one copy operation naming a target event"}
	}
	if !d.hasEvents && len(eventsInOrder) == 0 {
		eventsInOrder = []string{""} // a create-only config into an event-less target anchors at ""
	}
	plan := &transferPlan{project: proj, dict: d, identifier: d.identifier, createSource: ops[0].SourceField}
	for _, ev := range eventsInOrder {
		plan.writes = append(plan.writes, transferWrite{kind: "create", event: ev, field: d.identifier})
	}
	plan.writes = append(plan.writes, copies...)
	plan.eventsInOrder = eventsInOrder
	return plan, nil
}

// resolveTransferEvent resolves one copy operation's event token against the
// target dictionary: a full unique_event_name always, a plain event_name only
// when it names exactly one event (REQ-API-165). An event-less target takes no
// token at all.
func resolveTransferEvent(d *projectDict, projectName, token string) (string, error) {
	if !d.hasEvents {
		if token != "" {
			return "", &TransferConfigError{Msg: "the target project '" + projectName +
				"' has no events; address its fields as [project][field]"}
		}
		return "", nil
	}
	if token == "" {
		return "", &TransferConfigError{Msg: "the target project '" + projectName +
			"' is event-enabled; address fields as [project][event][field]"}
	}
	if _, ok := d.eventArm[token]; ok {
		return token, nil
	}
	match := ""
	for _, e := range d.events {
		if e.EventName == token {
			if match != "" {
				return "", &TransferConfigError{Msg: "event '" + token + "' of the target project '" + projectName +
					"' is ambiguous — name its unique_event_name"}
			}
			match = e.UniqueEventName
		}
	}
	if match == "" {
		return "", &TransferConfigError{Msg: "the target project '" + projectName + "' has no event '" + token + "'"}
	}
	return match, nil
}

// transferValueField reports whether f can carry a value across a transfer:
// none of the non-value types (REQ-VAL-046), and not a checkbox's own row —
// its values live under its choice columns (REQ-DB-042), which are themselves
// nameable sources and targets.
func transferValueField(f recField) bool {
	switch f.FieldType {
	case "description", "header", "transfer":
		return false
	}
	return !f.isCheckboxParent()
}

// transferTargetSubject builds the acting user's data-API subject on the target
// project — the shape every pair and group check reads. A holder without a
// membership row is denied unless they hold everything anyway (an organization
// administrator, whose Levels carry no pairs to miss).
func (h *Handler) transferTargetSubject(ctx context.Context, actor *db.User, proj *db.Project) (*subject, error) {
	levels, err := authz.Effective(ctx, h.Store, actor, proj.ID)
	if err != nil {
		return nil, err
	}
	assignment, err := h.Store.GetAssignment(ctx, actor.ID, proj.ID)
	if err != nil {
		return nil, err
	}
	if assignment == nil && !levels.Unrestricted {
		return nil, ErrTransferTargetAccess
	}
	return &subject{Assignment: assignment, User: actor, Project: proj, Levels: levels}, nil
}

// transferPairGate checks the actor's edit right at every (instrument, event)
// pair the plan writes — identifier rows included, they are value writes like
// any other (REQ-API-165). All pairs or none: one missing right denies the
// whole button.
func transferPairGate(d *projectDict, sub *subject, plan *transferPlan) bool {
	for _, w := range plan.writes {
		if authz.DataRank(d.pairAccess(sub, w.event, w.field).Data) < lvlViewEdit {
			return false
		}
	}
	return true
}

// transferActiveGroup is the target-side data-access-group scope of the actor:
// their active membership on the target project, or none. An organization
// administrator without a membership row has no group to join — like an import
// by a groupless holder, the new record lands in no group (REQ-API-093).
func (h *Handler) transferActiveGroup(ctx context.Context, sub *subject) (*int64, error) {
	if sub.Assignment == nil {
		return nil, nil
	}
	return h.activeGroupID(ctx, sub)
}

// ExecuteTransfer runs one transfer field's button for one record (REQ-API-165).
// The caller — the admin route — has already established that actor may read
// the source record; everything on the target side is re-established here at
// execution time, because designs and memberships rot: every reference the
// instructions make is resolved against the live dictionaries, every right read
// afresh, and any failure leaves nothing behind (the whole operation is one
// transaction, REQ-AUD-003). A transfer whose target record already exists
// updates it instead of creating — the stable record name from the create
// source value is what makes re-clicking idempotent.
func (h *Handler) ExecuteTransfer(ctx context.Context, actor *db.User, srcProjectID int64,
	recordID, event, instrument, fieldName string) (*TransferResult, error) {
	if actor == nil {
		return nil, ErrTransferTargetAccess
	}
	srcDict, err := h.loadDict(ctx, srcProjectID)
	if err != nil {
		return nil, err
	}
	tf, ok := srcDict.byName[fieldName]
	if !ok || tf.FieldType != "transfer" || tf.Instrument != instrument {
		return nil, &TransferConfigError{Msg: fmt.Sprintf(
			"no transfer field '%s' on instrument '%s'", fieldName, instrument)}
	}
	if !tf.Choices.Valid || strings.TrimSpace(tf.Choices.String) == "" {
		return nil, &TransferConfigError{Msg: "the transfer field carries no copy instructions"}
	}
	if srcDict.hasEvents {
		if _, ok := srcDict.eventArm[event]; !ok {
			return nil, &TransferConfigError{Msg: "unknown event '" + event + "' for the source project"}
		}
	} else {
		event = ""
	}

	plan, err := h.resolveTransfer(ctx, tf.Choices.String)
	if err != nil {
		var ce *TransferConfigError
		if errors.As(err, &ce) && ce.ProjectMissing {
			return nil, ErrTransferTargetAccess // the runtime never discloses project existence (REQ-API-007)
		}
		return nil, err
	}
	sub, err := h.transferTargetSubject(ctx, actor, plan.project)
	if err != nil {
		return nil, err
	}
	if !transferPairGate(plan.dict, sub, plan) {
		return nil, ErrTransferTargetAccess
	}
	// Source-side re-check against the saved dictionary (design-time checked
	// the staged one): every source a copy reads must be a value-carrying
	// field of the transfer field's own instrument (REQ-VAL-046).
	for _, w := range plan.writes {
		if w.kind != "copy" {
			continue
		}
		sf, ok := srcDict.byName[w.sourceField]
		if !ok || sf.Instrument != tf.Instrument || !transferValueField(sf) {
			return nil, &TransferConfigError{Msg: fmt.Sprintf(
				"'%s' is not a value-carrying field of instrument '%s'", w.sourceField, tf.Instrument)}
		}
	}

	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// The button copies what is stored, not a browser draft: this record's
	// saved first-instance values at the button's event (REQ-API-165).
	srcValues, err := h.Store.ListDataValuesByRecordTx(ctx, tx, srcProjectID, recordID)
	if err != nil {
		return nil, err
	}
	sv := valuesIndex(srcValues)[event]

	// The create operation names the target record by a value of this record.
	rawName := sv[plan.createSource]
	name := strings.TrimSpace(rawName)
	if name == "" {
		return nil, &TransferConfigError{Msg: fmt.Sprintf(
			"the value of '%s' that names the target record is empty", plan.createSource)}
	}
	if len(name) > 255 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 }) {
		return nil, &TransferConfigError{Msg: "the value of '" + plan.createSource +
			"' is not a usable record name"}
	}

	tgtValues, err := h.Store.ListDataValuesByRecordTx(ctx, tx, plan.project.ID, name)
	if err != nil {
		return nil, err
	}
	entity, err := h.Store.GetRecordEntityTx(ctx, tx, plan.project.ID, name)
	if err != nil {
		return nil, err
	}
	groupID, err := h.transferActiveGroup(ctx, sub)
	if err != nil {
		return nil, err
	}
	if entity != nil && groupID != nil &&
		(!entity.DagGroupID.Valid || entity.DagGroupID.Int64 != *groupID) {
		// A record of another data access group is invisible, let alone
		// writable — the uniform denial, so this cannot enumerate records.
		return nil, ErrTransferTargetAccess
	}
	existed := len(tgtValues) > 0 || entity != nil
	rvTgt := valuesIndex(tgtValues)
	riTgt := plan.dict.instancesIndex(tgtValues)

	reg, err := h.validationRegistry(ctx)
	if err != nil {
		return nil, err
	}
	offsetFor := h.collectionOffsetFor("") // no caller timezone: the canonical stored form (REQ-VAL-025)

	copied := []string{}
	skippedEmpty := []string{}
	changesByEvent := map[string]map[string]map[string]string{}
	recordChange := func(ev, field, oldV, newV string) {
		if oldV == newV {
			return
		}
		m := changesByEvent[ev]
		if m == nil {
			m = map[string]map[string]string{}
			changesByEvent[ev] = m
		}
		m[field] = map[string]string{"old": oldV, "new": newV}
	}

	for _, w := range plan.writes {
		value := name // a create row carries the record's own name
		if w.kind == "copy" {
			raw := sv[w.sourceField]
			if raw == "" {
				// An empty source value skips the operation — it does NOT
				// clear the target (REQ-API-165; a deliberate deviation from
				// import's empty-clears, REQ-VAL-024: the button copies what
				// is there, not what is missing).
				skippedEmpty = append(skippedEmpty, w.sourceField)
				continue
			}
			tf2 := plan.dict.byName[w.field]
			vf := validate.Field{
				Name:           tf2.FieldName,
				Type:           tf2.FieldType,
				ValidationType: tf2.ValidationType.String,
				ValidationMin:  tf2.ValidationMin.String,
				ValidationMax:  tf2.ValidationMax.String,
				Choices:        tf2.Choices.String,
			}
			if v := validate.ValidateValue(vf, raw, reg); v != nil {
				return nil, &TransferConfigError{Msg: fmt.Sprintf(
					"the target project rejects the value of '%s' for field '%s': %s",
					w.sourceField, w.field, v.Message)}
			}
			if vf.ValidationType == "date" || vf.ValidationType == "datetime" {
				canonical, ok := canonicalizeDate(raw, vf.ValidationType, tf2.ValidationFormat.String, offsetFor)
				if !ok {
					return nil, &TransferConfigError{Msg: fmt.Sprintf(
						"the value of '%s' is not a valid %s for field '%s'", w.sourceField, vf.ValidationType, w.field)}
				}
				raw = canonical
			}
			value = raw
		}
		if rvTgt[w.event] == nil {
			rvTgt[w.event] = map[string]string{}
		}
		oldV := rvTgt[w.event][w.field]
		// First-instance slots only in v1: a transfer addresses the form, not
		// one of its instances (documented scope limit).
		slotInstrument, slotNumber := db.InstanceSlot("", 1)
		dv := &db.DataValue{ProjectID: plan.project.ID, RecordID: name,
			UniqueEventName: w.event, RepeatingInstrument: slotInstrument, RepeatingInstanceNumber: slotNumber,
			FieldName: w.field, Value: value}
		if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
			return nil, err
		}
		recordChange(w.event, w.field, oldV, value)
		rvTgt[w.event][w.field] = value
		if w.kind == "copy" {
			copied = append(copied, w.sourceField)
		}
	}

	// A new target record joins the actor's active group on the target — or
	// none (REQ-API-093 pattern); an existing one never changes its group.
	if entity == nil {
		re := &db.RecordEntity{ProjectID: plan.project.ID, RecordID: name,
			CreatedBy: sql.NullInt64{Int64: actor.ID, Valid: true},
			CreatedAt: time.Now().UTC().Format("2006-01-02 15:04:05")}
		if groupID != nil {
			re.DagGroupID = sql.NullInt64{Int64: *groupID, Valid: true}
		}
		if err := h.Store.CreateRecordEntityTx(ctx, tx, re); err != nil {
			return nil, err
		}
	}

	// Calculated fields of the target recompute in the same transaction
	// (REQ-VAL-037).
	calcChanges, err := h.recomputeCalculated(ctx, tx, plan.project.ID, name, rvTgt, riTgt, plan.dict)
	if err != nil {
		return nil, err
	}

	// Target-side audit: the standard data-change shape (REQ-AUD-034 reading
	// REQ-AUD-009), one entry group per (instrument, event), each carrying the
	// transfer's origin. A re-click that changed nothing writes none of these;
	// the execution entry below is written either way.
	action, eventType := "create", audit.RecordCreated
	if existed {
		action, eventType = "update", audit.RecordUpdated
	}
	srcProj, err := h.Store.GetProject(ctx, srcProjectID)
	if err != nil {
		return nil, err
	}
	origin := &transferOrigin{Project: srcProj.ProjectName, RecordID: recordID}
	for i, ev := range plan.eventsInOrder {
		changes := changesByEvent[ev]
		if len(changes) == 0 && (existed || i > 0) {
			continue // no change of this event; the birth entry rides the first one alone
		}
		for _, details := range importChangeGroups(plan.dict, action, name, "", ev, 1, changes) {
			details.Origin = origin
			if err := h.AuditTx(ctx, tx, audit.Entry{
				EventType:    eventType,
				Source:       audit.SourceUI, // UI-driven through the admin API; no token column (REQ-AUD-018)
				UserID:       actor.ID,
				Email:        actor.Email,
				ProjectID:    plan.project.ID,
				TargetRecord: name,
				Details:      details,
			}); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range sortedCalculated(calcChanges) {
		if err := h.AuditTx(ctx, tx, audit.Entry{
			EventType:    audit.CalculatedRecomputed,
			Source:       audit.SourceUI,
			UserID:       actor.ID,
			Email:        actor.Email,
			ProjectID:    plan.project.ID,
			TargetRecord: name,
			Details: calculatedDetails{RecordID: name, Field: c.Field, Instrument: c.Instrument,
				Event: c.Event, Instance: furtherInstance(c.Instance), Old: c.Old, New: c.New},
		}); err != nil {
			return nil, err
		}
	}

	// The source project's own entry (REQ-AUD-034): every executed transfer is
	// on the trail there, even one that changed nothing downstream — and never
	// with values.
	if err := h.AuditTx(ctx, tx, audit.Entry{
		EventType:    audit.TransferExecuted,
		Source:       audit.SourceUI,
		UserID:       actor.ID,
		Email:        actor.Email,
		ProjectID:    srcProjectID,
		TargetRecord: recordID,
		Details: transferExecutedDetails{Field: fieldName, TargetProject: plan.project.ProjectName,
			TargetRecord: name, Created: !existed, Copied: copied, SkippedEmpty: skippedEmpty},
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TransferResult{TargetProject: plan.project.ProjectName, TargetRecord: name,
		Created: !existed, Copied: copied, SkippedEmpty: skippedEmpty}, nil
}

// transferExecutedDetails is the source project's transfer_executed payload
// (REQ-AUD-034): what ran and where, never what it carried.
type transferExecutedDetails struct {
	Field         string   `json:"field"`
	TargetProject string   `json:"target_project"`
	TargetRecord  string   `json:"target_record"`
	Created       bool     `json:"created"`
	Copied        []string `json:"copied"`
	SkippedEmpty  []string `json:"skipped_empty"`
}

// TransferAvailability answers the probe of REQ-API-166 for one instrument: per
// transfer field, whether this actor may run it here — instructions parse, the
// target resolves, and the actor holds edit rights at every written pair. It
// writes nothing and fails closed: any failure, permission-shaped or not, reads
// as disabled, and no reason is returned (the button is simply absent,
// REQ-UI-065). The executing path re-checks everything, including data-group
// visibility of the existing target record.
func (h *Handler) TransferAvailability(ctx context.Context, actor *db.User, srcProjectID int64,
	instrument string) ([]TransferFieldStatus, error) {
	if actor == nil {
		return []TransferFieldStatus{}, nil
	}
	srcDict, err := h.loadDict(ctx, srcProjectID)
	if err != nil {
		return nil, err
	}
	out := []TransferFieldStatus{}
	for _, f := range srcDict.fields {
		if f.FieldType != "transfer" || f.Instrument != instrument {
			continue
		}
		out = append(out, TransferFieldStatus{FieldName: f.FieldName, Enabled: h.transferEnabled(ctx, actor, srcDict, f)})
	}
	return out, nil
}

func (h *Handler) transferEnabled(ctx context.Context, actor *db.User, srcDict *projectDict, tf recField) bool {
	if !tf.Choices.Valid || strings.TrimSpace(tf.Choices.String) == "" {
		return false
	}
	plan, err := h.resolveTransfer(ctx, tf.Choices.String)
	if err != nil {
		return false
	}
	for _, w := range plan.writes {
		if w.kind != "copy" {
			continue
		}
		sf, ok := srcDict.byName[w.sourceField]
		if !ok || sf.Instrument != tf.Instrument || !transferValueField(sf) {
			return false
		}
	}
	sub, err := h.transferTargetSubject(ctx, actor, plan.project)
	if err != nil {
		return false // fail closed: a probe discloses nothing (REQ-API-166)
	}
	return transferPairGate(plan.dict, sub, plan)
}
