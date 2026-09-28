package dataapi

// content=record&action=import (API_Endpoints_Design.md §3.7). The request
// is a set of data[i][…] rows, each a (record, form, event) tuple of field
// values; every value passes the full validation pipeline before storage
// and each record is all-or-nothing in its own transaction.

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// importRow is one result row of the §3.7.2 response.
type importRow struct {
	RecordID       string `json:"record_id"`
	FormName       string `json:"form_name"`
	ImportRecordID int    `json:"import_record_id"`
	ImportFormName string `json:"import_form_name"`
}

const (
	importAdded   = 1
	importUpdated = 2
	importInvalid = 0
)

// dataRowKeyRE splits data[<i>][<key>] parameter names.
var dataRowKeyRE = regexp.MustCompile(`^data\[(\d+)\]\[(.+)\]$`)

// parseImportRows collects the data[i][key] parameters into rows in index
// order. Body values take precedence over the query string, matching
// ParseParams (REQ-API-010).
func parseImportRows(r *http.Request) []map[string]string {
	merged := map[string][]string{}
	if r.URL != nil {
		for k, vs := range r.URL.Query() {
			merged[k] = vs
		}
	}
	for k, vs := range r.PostForm {
		merged[k] = vs
	}
	indexed := map[int]map[string]string{}
	for k, vs := range merged {
		m := dataRowKeyRE.FindStringSubmatch(k)
		if m == nil || len(vs) == 0 {
			continue
		}
		i, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if indexed[i] == nil {
			indexed[i] = map[string]string{}
		}
		indexed[i][m[2]] = vs[0]
	}
	var order []int
	for i := range indexed {
		order = append(order, i)
	}
	sort.Ints(order)
	rows := make([]map[string]string, 0, len(order))
	for _, i := range order {
		rows = append(rows, indexed[i])
	}
	return rows
}

// importMetaKeys are the row tuple keys (and REDCap repeat columns this
// implementation stores as instance 1); everything else names a field.
// record_id is deliberately not here — it doubles as the identifier field
// and is stored like any other value of the row.
var importMetaKeys = map[string]bool{
	"form_name": true, "event_name": true,
	"redcap_repeat_instrument": true, "redcap_repeat_instance": true,
}

func (h *Handler) contentRecordImport(w http.ResponseWriter, r *http.Request, enc string, sub *subject, p Params) {
	ctx := r.Context()

	// Analysis mode rejects every write — reads and exports are unaffected
	// (GD-20, REQ-API-109).
	if sub.Project.Mode == "analysis" {
		writeError(w, enc, http.StatusForbidden, "Project in analysis mode")
		return
	}
	// A survey link is admitted by §3.10 itself; the arm table says nothing
	// about it and would reject every submission.
	if !sub.isLink() && !sub.hasData(lvlViewEdit) {
		writeError(w, enc, http.StatusForbidden, "Permission denied")
		return
	}
	d, err := h.loadDict(ctx, sub.Project.ID)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	reg, err := h.validationRegistry(ctx)
	if err != nil {
		h.storeError(w, enc)
		return
	}
	rows := parseImportRows(r)
	if len(rows) == 0 {
		writeError(w, enc, http.StatusBadRequest, "Invalid request")
		return
	}

	results := make([]importRow, 0, len(rows))
	for _, row := range rows {
		res, fatal := h.importOneRow(ctx, sub, d, reg, p, row)
		if fatal != nil {
			if _, ok := fatal.(*forbiddenError); ok {
				writeError(w, enc, http.StatusForbidden, "Permission denied")
			} else {
				h.storeError(w, enc) // a store failure fails the call (500)
			}
			return
		}
		results = append(results, res)
	}
	render(w, enc, p.Delimiter(), results)
}

// importOneRow runs one data[] entry: validate everything first, then apply
// the whole row in a single transaction (REQ-API-035). The fatal error is a
// store failure — validation failures are response rows, not fatals.
func (h *Handler) importOneRow(ctx context.Context, sub *subject, d *projectDict, reg *validate.Registry,
	p Params, row map[string]string) (importRow, error) {

	recordID := row["record_id"]
	formName := row["form_name"]
	// A rejected submission is audit-logged like an accepted one
	// (REQ-AUD-021); nothing changed, so its field list is empty.
	fail := func(msg string) (importRow, error) {
		if sub.isLink() {
			h.auditSurveyFailure(ctx, sub, recordID, msg)
		}
		return importRow{RecordID: recordID, FormName: formName,
			ImportRecordID: importInvalid, ImportFormName: "Validation error: " + msg}, nil
	}

	if recordID == "" {
		return fail("record_id: CONTENT_INVALID — a record id is required")
	}
	if formName == "" {
		return fail("form_name: CONTENT_INVALID — a form name is required")
	}
	// A survey link fills its own (record, instrument) and nothing else. A
	// row naming another pair is a permission mismatch, not a validation
	// failure — the same shape as the arm check below (§3.10, REQ-API-083).
	if sub.isLink() && (recordID != sub.Link.RecordID || formName != sub.Instrument) {
		return importRow{}, errForbiddenRequest
	}
	formKnown := false
	for _, f := range d.fields {
		if f.Instrument == formName {
			formKnown = true
			break
		}
	}
	if !formKnown {
		return fail("form_name: CONTENT_INVALID — unknown form '" + formName + "'")
	}

	// Event and arm. Projects without events store under the empty event
	// name; the data-level check then falls back to any arm (already done
	// for the call).
	event := row["event_name"]
	if d.hasEvents {
		arm, ok := d.eventArm[event]
		if !ok {
			return fail("event_name: CONTENT_INVALID — unknown event '" + event + "'")
		}
		// A survey link carries no arm grant at all: §3.10 admits it for its
		// own (record, instrument) whatever arm the event belongs to.
		if !sub.isLink() && !sub.User.IsAdmin && sub.dataLevels[arm] < lvlViewEdit {
			// A permission mismatch is request-level, not a data row.
			return importRow{}, errForbiddenRequest
		}
	}

	offsetFor := h.collectionOffsetFor(p.TZ)

	// Validate every supplied value before storing anything (§3.7.1).
	type pending struct {
		field recField
		value string // canonicalized, non-empty = set; empty = clear
	}
	var pendings []pending
	var problems []string
	// Deterministic order: dictionary order over the row's keys.
	for _, f := range d.fields {
		raw, ok := row[f.FieldName]
		if !ok || importMetaKeys[f.FieldName] {
			continue
		}
		vf := validate.Field{
			Name:           f.FieldName,
			Type:           f.FieldType,
			ValidationType: f.ValidationType.String,
			ValidationMin:  f.ValidationMin.String,
			ValidationMax:  f.ValidationMax.String,
			Choices:        f.Choices.String,
		}
		v := validate.ValidateValue(vf, raw, reg)
		if v == nil && raw != "" && (vf.ValidationType == "date" || vf.ValidationType == "datetime") {
			canonical, ok := canonicalizeDate(raw, vf.ValidationType, f.ValidationFormat.String, offsetFor)
			if !ok {
				v = &validate.Violation{Code: validate.CodeTypeInvalid,
					Message: "value '" + raw + "' is not a valid " + vf.ValidationType}
			} else {
				raw = canonical
			}
		}
		if v != nil {
			problems = append(problems, f.FieldName+": "+string(v.Code)+" — "+v.Message)
			continue
		}
		pendings = append(pendings, pending{field: f, value: raw})
	}
	for k := range row {
		if importMetaKeys[k] {
			continue
		}
		if _, ok := d.byName[k]; !ok {
			problems = append(problems, k+": "+string(validate.CodeUnknownField)+" — unknown field '"+k+"'")
		}
	}
	if len(problems) > 0 {
		return fail(strings.Join(problems, "; "))
	}

	// --- apply: one transaction per record (REQ-API-035) ---
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return importRow{}, err
	}
	defer tx.Rollback()

	oldValues, err := h.Store.ListDataValuesByRecordTx(ctx, tx, sub.Project.ID, recordID)
	if err != nil {
		return importRow{}, err
	}
	entity, err := h.Store.GetRecordEntityTx(ctx, tx, sub.Project.ID, recordID)
	if err != nil {
		return importRow{}, err
	}
	existed := len(oldValues) > 0 || entity != nil
	rv := valuesIndex(oldValues)

	changes := map[string]map[string]string{}
	recordChange := func(field, oldV, newV string) {
		if oldV == newV {
			return
		}
		changes[field] = map[string]string{"old": oldV, "new": newV}
	}

	for _, pd := range pendings {
		oldV := rv[event][pd.field.FieldName]
		dv := &db.DataValue{ProjectID: sub.Project.ID, RecordID: recordID,
			UniqueEventName: event, FieldName: pd.field.FieldName, Value: pd.value}
		if pd.value == "" {
			// Intentional clear (REQ-VAL-024): empty removes the value.
			if oldV != "" {
				if err := h.Store.DeleteDataValueTx(ctx, tx, dv); err != nil {
					return importRow{}, err
				}
				recordChange(pd.field.FieldName, oldV, "")
			}
			delete(rv[event], pd.field.FieldName)
			continue
		}
		if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
			return importRow{}, err
		}
		recordChange(pd.field.FieldName, oldV, pd.value)
		if rv[event] == nil {
			rv[event] = map[string]string{}
		}
		rv[event][pd.field.FieldName] = pd.value
	}

	// New records join the holder's active data access group — or none;
	// an import never changes an existing record's group (REQ-API-093).
	if entity == nil {
		groupID, err := h.activeGroupID(ctx, sub)
		if err != nil {
			return importRow{}, err
		}
		re := &db.RecordEntity{ProjectID: sub.Project.ID, RecordID: recordID,
			// An anonymous survey submission has no creating account; the
			// nullable column stays NULL rather than naming a stranger.
			CreatedBy: sql.NullInt64{Int64: sub.actorUserID(), Valid: !sub.isLink()},
			CreatedAt: time.Now().UTC().Format("2006-01-02 15:04:05")}
		if groupID != nil {
			re.DagGroupID = sql.NullInt64{Int64: *groupID, Valid: true}
		}
		if err := h.Store.CreateRecordEntityTx(ctx, tx, re); err != nil {
			return importRow{}, err
		}
	}

	// Calculated fields recompute in the same transaction (REQ-VAL-037).
	calcChanges, err := h.recomputeCalculated(ctx, tx, sub.Project.ID, recordID, rv, d)
	if err != nil {
		return importRow{}, err
	}

	// Audit with old/new values (§3.7.2, REQ-AUD-009's data-change family).
	// A survey submission has no account behind it: the actor columns stay
	// unset and the link itself is the token column (REQ-AUTH-041).
	eventType := audit.RecordCreated
	if existed {
		eventType = audit.RecordUpdated
	}
	if len(changes) > 0 || !existed {
		if err := h.AuditTx(ctx, tx, audit.Entry{Token: p.Token,
			EventType:    eventType,
			Source:       audit.SourceAPI,
			UserID:       sub.actorUserID(),
			Email:        sub.actorEmail(),
			ProjectID:    sub.Project.ID,
			TargetRecord: recordID,
			Details:      map[string]any{"changes": changes},
		}); err != nil {
			return importRow{}, err
		}
	}
	if len(calcChanges) > 0 {
		if err := h.AuditTx(ctx, tx, audit.Entry{Token: p.Token,
			EventType:    audit.CalculatedRecomputed,
			Source:       audit.SourceAPI,
			UserID:       sub.actorUserID(),
			Email:        sub.actorEmail(),
			ProjectID:    sub.Project.ID,
			TargetRecord: recordID,
			Details:      map[string]any{"changes": calcChanges},
		}); err != nil {
			return importRow{}, err
		}
	}
	// The submission's own entry rides in the same transaction, so a
	// rollback cannot leave a success on the record (REQ-AUD-021).
	if sub.isLink() {
		if err := h.AuditTx(ctx, tx, surveySubmittedEntry(sub, recordID, "success", "", changes)); err != nil {
			return importRow{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return importRow{}, err
	}
	code := importAdded
	if existed {
		code = importUpdated
	}
	return importRow{RecordID: recordID, FormName: formName,
		ImportRecordID: code, ImportFormName: formName}, nil
}

// errForbiddenRequest marks a per-arm permission mismatch discovered while
// walking rows; it aborts the whole call with 403 (not a result row).
var errForbiddenRequest = &forbiddenError{}

type forbiddenError struct{}

func (*forbiddenError) Error() string { return "permission denied" }

// recomputeCalculated evaluates every calculated field of the project for
// one record against its current values and stores changed results in tx,
// returning their old/new map (REQ-VAL-037).
func (h *Handler) recomputeCalculated(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, rv recordValues, d *projectDict) (map[string]map[string]string, error) {

	out := map[string]map[string]string{}
	for _, f := range d.fields {
		if f.FieldType != "calculated" || !f.Calculation.Valid || f.Calculation.String == "" {
			continue
		}
		var targets []string
		if d.hasEvents {
			for _, e := range d.eventsForInstrument(f.InstrumentID, d.events) {
				targets = append(targets, e.UniqueEventName)
			}
		} else {
			targets = []string{""}
		}
		for _, event := range targets {
			value, _, err := validate.EvalCalc(f.Calculation.String, func(ref validate.Ref) (string, bool) {
				if ref.Event != "" {
					v := rv[ref.Event][ref.Field]
					return v, v != ""
				}
				for _, byField := range rv {
					if v := byField[ref.Field]; v != "" {
						return v, true
					}
				}
				return "", false
			})
			if err != nil {
				continue // malformed expression: never rewrite data with it
			}
			oldV := rv[event][f.FieldName]
			if oldV == value {
				continue
			}
			dv := &db.DataValue{ProjectID: projectID, RecordID: recordID,
				UniqueEventName: event, FieldName: f.FieldName, Value: value}
			if value == "" {
				if oldV != "" {
					if err := h.Store.DeleteDataValueTx(ctx, tx, dv); err != nil {
						return nil, err
					}
					out[f.FieldName] = map[string]string{"old": oldV, "new": ""}
					delete(rv[event], f.FieldName)
				}
				continue
			}
			if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
				return nil, err
			}
			out[f.FieldName] = map[string]string{"old": oldV, "new": value}
			if rv[event] == nil {
				rv[event] = map[string]string{}
			}
			rv[event][f.FieldName] = value
		}
	}
	return out, nil
}

// validationRegistry builds the §4.2 registry from the seeded and custom
// validation_types rows.
func (h *Handler) validationRegistry(ctx context.Context) (*validate.Registry, error) {
	rows, err := h.Store.ListValidationTypes(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]validate.RegistryEntry, 0, len(rows))
	for _, vt := range rows {
		entries = append(entries, validate.RegistryEntry{Name: vt.Name, Regex: vt.Regex, Builtin: vt.Builtin})
	}
	return validate.NewRegistry(entries)
}

// AuditTx writes one audit entry inside tx through the handler's writer; a
// handler without a writer (unit tests) skips it.
func (h *Handler) AuditTx(ctx context.Context, tx *sql.Tx, e audit.Entry) error {
	if h.Audit == nil {
		return nil
	}
	return h.Audit.InsertTx(ctx, tx, e)
}
