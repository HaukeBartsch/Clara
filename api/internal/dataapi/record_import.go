package dataapi

// content=record with a `data` parameter (API_Endpoints_Design.md §3.7,
// REQ-API-012). The request is either data[i][…] form rows or a single
// `data` parameter holding a JSON array of record objects (§3.7.1), each a
// (record, form, event) tuple of field values — a row may omit form_name and
// store flat, every field on its own instrument. Every value passes the full
// validation pipeline before storage and each record is all-or-nothing in
// its own transaction; returnContent=count answers the applied count
// (REQ-API-142).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
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

// parseImportRows collects the import rows in either §3.7.1 encoding:
// indexed data[i][key] parameters (body over query, matching ParseParams —
// REQ-API-010), or — when none are present — a single `data` parameter
// holding a JSON array of record objects (REQ-API-031). A malformed JSON
// encoding yields no rows, which the caller answers as the uniform 400.
func parseImportRows(r *http.Request, p Params) []map[string]string {
	merged := map[string][]string{}
	// Both maps come from the uncapped parsers in form.go: r.PostForm is
	// published there for exactly this, and the query was already checked by
	// ParseParams, so its error cannot be a limit here.
	query, _ := queryValues(r)
	for k, vs := range query {
		merged[k] = vs
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
	if len(order) == 0 {
		return parseJSONData(p.Data)
	}
	sort.Ints(order)
	rows := make([]map[string]string, 0, len(order))
	for _, i := range order {
		rows = append(rows, indexed[i])
	}
	return rows
}

// parseJSONData reads the JSON-array encoding of `data` (REQ-API-031): an
// array of objects whose values are coerced to their string spelling — a
// number keeps its literal text ("18"), a boolean becomes "1"/"0", null
// becomes "". Anything that is not an array of objects yields no rows.
func parseJSONData(s string) []map[string]string {
	if s == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var items []any
	if err := dec.Decode(&items); err != nil {
		return nil
	}
	rows := make([]map[string]string, 0, len(items))
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			return nil
		}
		row := make(map[string]string, len(obj))
		for k, v := range obj {
			row[k] = jsonValueString(v)
		}
		rows = append(rows, row)
	}
	return rows
}

func jsonValueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "1"
		}
		return "0"
	default: // a nested object or array keeps its compact JSON
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// importMetaKeys are the row tuple keys (and REDCap repeat columns this
// implementation stores as instance 1); everything else names a field.
// The record identifier (GD-8) is deliberately not here — its key carries the
// record id and is stored like any other value of the row. redcap_event_name is the
// accepted alias of event_name (REQ-API-138), listed so it never reaches the
// unknown-field pass; no project field can carry the name — reserved
// (Data_Validation_Design.md §9).
var importMetaKeys = map[string]bool{
	"form_name": true, "event_name": true, "redcap_event_name": true,
	"redcap_repeat_instrument": true, "redcap_repeat_instance": true,
}

// linkLockStripes is how many independent locks the per-link serialisation uses
// (handler.go): contention is between submissions of one link, so a fixed set of
// stripes keyed by link id keeps unrelated calls apart without a map to grow.
const linkLockStripes = 32

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
	rows := parseImportRows(r, p)
	if len(rows) == 0 {
		writeError(w, enc, http.StatusBadRequest, "Invalid request")
		return
	}

	// The conditional stamp decides which submission of a link is the one (REQ-API-145).
	// Inside this process they also take turns, so the loser opens its transaction
	// after the winner committed and reads 410 rather than SQLite's write conflict;
	// across processes the stamp alone decides.
	if sub.isLink() && sub.Link != nil {
		mu := h.linkLock(sub.Link.ID)
		mu.Lock()
		defer mu.Unlock()
	}

	results := make([]importRow, 0, len(rows))
	for _, row := range rows {
		res, fatal := h.importOneRow(ctx, sub, d, reg, p, row)
		if fatal != nil {
			switch fatal.(type) {
			case *forbiddenError:
				writeError(w, enc, http.StatusForbidden, "Permission denied")
			case *surveyUsedError:
				// Another submission took this link's one stamp (REQ-API-145);
				// nothing of this row was stored.
				writeError(w, enc, http.StatusGone, "Survey already submitted")
			default:
				h.storeError(w, enc) // a store failure fails the call (500)
			}
			return
		}
		results = append(results, res)
	}
	// returnContent=count answers the number of rows applied — added and
	// updated; rejected rows are not counted and their detail is not
	// rendered (REQ-API-142). Any other value keeps the §3.7.2 result rows.
	if strings.EqualFold(p.ReturnContent, "count") {
		applied := 0
		for _, res := range results {
			if res.ImportRecordID == importAdded || res.ImportRecordID == importUpdated {
				applied++
			}
		}
		writeCount(w, enc, applied)
		return
	}
	render(w, enc, p.Delimiter(), results)
}

// importOneRow runs one data[] entry: validate everything first, then apply
// the whole row in a single transaction (REQ-API-035). The fatal error is a
// store failure — validation failures are response rows, not fatals.
func (h *Handler) importOneRow(ctx context.Context, sub *subject, d *projectDict, reg *validate.Registry,
	p Params, row map[string]string) (importRow, error) {

	// The record id travels under the record identifier's own field name —
	// the first field of the first instrument (GD-8) — exactly as an export
	// writes it, so `record_id` is only the key when the project names its
	// first field that way.
	idKey := d.identifier
	recordID := ""
	if idKey != "" {
		recordID = row[idKey]
	}
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

	if idKey == "" {
		// A project without fields has no identifier to address a record by.
		return fail("CONTENT_INVALID — the project has no record identifier field (GD-8)")
	}
	if sub.isLink() && recordID == "" {
		// A survey link is the whole address: its row already holds the
		// (record, instrument, event) this submission targets, so the public
		// page names none of them (§3.10, REQ-API-083). The token is looked up
		// instead of the record being told to a browser that must not learn it;
		// a row that *does* name another triple is still the 403 below.
		recordID = sub.Link.RecordID
		// The identifier field's value is the record id and is not a meta key,
		// so writing it back keeps storage identical to a named submission —
		// including for a record this call creates.
		row[idKey] = recordID
	}
	if recordID == "" {
		return fail(idKey + ": CONTENT_INVALID — a record id is required")
	}
	// A row without form_name is flat (REQ-API-031): every supplied field
	// stores on its own instrument from the data dictionary, so a caller can
	// post back an export without naming forms. The cross-instrument rule in
	// the value loop below keeps each value on its own field's instrument.
	// A survey link fills its own (record, instrument) and nothing else: the
	// pin replaces form_name there, and a row *naming* another instrument is
	// a permission mismatch, not a validation failure — the same shape as
	// the arm check below (§3.10, REQ-API-083).
	if sub.isLink() && (recordID != sub.Link.RecordID || (formName != "" && formName != sub.Instrument)) {
		return importRow{}, errForbiddenRequest
	}
	if formName != "" {
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
	}

	// Event and arm. Projects without events store under the empty event
	// name; the data-level check then falls back to any arm (already done
	// for the call).
	//
	//
	// redcap_event_name is accepted next to event_name where an event exists
	// at all (REQ-API-138): it is the spelling a caller round-tripping an
	// export sees (REQ-API-028), so accepting it keeps export → edit → import
	// one shape. The alias is unambiguous — no project may name a field that
	// (reserved names, Data_Validation_Design.md §9). An empty value counts as
	// not supplied (the REQ-VAL-024 reading of empty), so sending both keys
	// with one filled is how a caller picks either spelling; two *different*
	// events name an ambiguous target, and taking one silently would put the
	// row's values in the wrong event. The reported key is the one the caller
	// used, which keeps the message about what was sent rather than about what
	// was expected.
	event, eventKey := row["event_name"], "event_name"
	if d.hasEvents {
		if alias := row["redcap_event_name"]; alias != "" {
			switch {
			case event == "":
				event, eventKey = alias, "redcap_event_name"
			case event != alias:
				return fail("event_name: CONTENT_INVALID — event_name '" + event +
					"' conflicts with redcap_event_name '" + alias + "'")
			}
		}
		// A row through a link that names no event takes the link's own — the
		// public page cannot know the event name any more than the record id,
		// and the link already says which of the instrument's events this is
		// (§3.10, REQ-API-083). A spelling that *is* supplied stays, so the
		// mismatch check below still turns a link used at another event into a
		// 403 rather than silently retargeting the row.
		if event == "" && sub.isLink() {
			event = d.eventNameOf(sub.Link.EventID)
			if event == "" {
				// The link names an event this project no longer has: its design
				// changed under the respondent. There is no target to fill, and
				// letting it fall through would answer with a validation error
				// about an empty event name — permission-shaped, so 403 (§3.10).
				return importRow{}, errForbiddenRequest
			}
		}
		if _, ok := d.eventArm[event]; !ok {
			return fail(eventKey + ": CONTENT_INVALID — unknown event '" + event + "'")
		}
		// A survey link carries no grant at all: §3.10 admits it for its own
		// (record, instrument, event) whatever the pair says — and only for that
		// event, since a link of an instrument mapped to three events is three
		// distinct links (REQ-AUTH-039).
		if sub.isLink() {
			if sub.Link.EventID != d.eventIDOf(event) {
				return importRow{}, errForbiddenRequest
			}
		} else {
			// Pair gate (REQ-AUTH-069): every field this row carries must be
			// editable at its own (instrument, event) pair, not merely at the
			// arm of the event it names. Editing a survey that has already been
			// collected needs the edit_surveys right on that pair as well
			// (REQ-AUTH-071) — checked where the link's collection stamp is read.
			for name := range row {
				if _, known := d.byName[name]; !known {
					continue // not a field: the record identifier or a framing key
				}
				if authz.DataRank(d.pairAccess(sub, event, name).Data) < lvlViewEdit {
					return importRow{}, errForbiddenRequest
				}
			}
		}
	} else if sub.isLink() {
		// A project without events holds no survey link at all (DEV-DB-14), so
		// a link reaching here names an event that no longer exists — its
		// design changed under it. There is no empty-event store it could mean.
		return importRow{}, errForbiddenRequest
	}

	// Repeating instruments (REQ-API-155): redcap_repeat_instrument and
	// redcap_repeat_instance — REDCap's own columns, so an exported row imports
	// back where it came from — address one instance of an instrument that
	// repeats at the row's event. A row naming neither fills the first instance
	// of every form it carries, which is also where instance 1 of a repeating
	// form lives (REQ-DB-048).
	repeatInstrument := row["redcap_repeat_instrument"]
	instance := 1
	if rawInstance := row["redcap_repeat_instance"]; repeatInstrument == "" {
		// "1" alone is the first instance as well; anything more would be a
		// repeating event, which this implementation does not have.
		if rawInstance != "" && rawInstance != "1" {
			return fail("redcap_repeat_instance: CONTENT_INVALID — instance '" + rawInstance +
				"' needs redcap_repeat_instrument naming a repeating instrument (repeating events are not supported)")
		}
	} else {
		if sub.isLink() {
			// A link fills its own survey form, which never repeats (REQ-API-154).
			return importRow{}, errForbiddenRequest
		}
		if formName != "" && formName != repeatInstrument {
			return fail("redcap_repeat_instrument: CONTENT_INVALID — redcap_repeat_instrument '" + repeatInstrument +
				"' conflicts with form_name '" + formName + "'")
		}
		if !d.repeats(event, repeatInstrument) {
			return fail("redcap_repeat_instrument: CONTENT_INVALID — instrument '" + repeatInstrument +
				"' is not repeating at event '" + event + "'")
		}
		n, err := strconv.Atoi(rawInstance)
		if err != nil || n < 1 {
			return fail("redcap_repeat_instance: CONTENT_INVALID — '" + rawInstance +
				"' is not an instance number (1, 2, …)")
		}
		instance = n
	}

	offsetFor := h.collectionOffsetFor(p.TZ)

	// Validate every supplied value before storing anything (§3.7.1).
	type pending struct {
		field recField
		value string // canonicalized, non-empty = set; empty = clear
	}
	var pendings []pending
	var problems []string
	// Deterministic order: dictionary order over the row's keys. A checkbox is
	// written through its choice columns, which follow it in the expanded
	// dictionary (REQ-DB-042).
	for _, f := range d.expanded {
		raw, ok := row[f.FieldName]
		if !ok || importMetaKeys[f.FieldName] {
			continue
		}
		if f.isCheckboxParent() {
			// The checkbox's own name addresses no data column; its boxes do.
			problems = append(problems, f.FieldName+": "+string(validate.CodeNonValueField)+
				" — checkbox field '"+f.FieldName+"' takes its values per choice as "+
				db.CheckboxSubName(f.FieldName, "<code>"))
			continue
		}
		// A row writes its named instrument and the record identifier —
		// never fields of other instruments (security finding F3); a flat
		// row writes each field's own instrument, the same rule read from
		// the dictionary instead of from the caller. The survey link's pin
		// must mean "this instrument only", or an anonymous respondent could
		// overwrite clinician data elsewhere in the record; for project
		// tokens, form_name means what it says.
		own := formName
		if repeatInstrument != "" {
			own = repeatInstrument // the row is one instance of this instrument
		}
		if own == "" {
			own = f.Instrument // flat: the dictionary assigns the instrument
			if sub.isLink() {
				own = sub.Instrument // the link's pin replaces form_name
			}
			if raw == "" && f.FieldName != d.identifier && d.repeats(event, f.Instrument) {
				// A flat row names no instance: REDCap's own export leaves the
				// columns of a repeating instrument empty on the event's row and
				// carries their values on the instance rows, so an empty cell here
				// is not a clear (REQ-API-155).
				continue
			}
		}
		if repeatInstrument != "" && f.Instrument != repeatInstrument && raw == "" {
			// An instance row carries one instrument; REDCap's own export leaves the
			// other columns of such a row empty, which says nothing about them.
			continue
		}
		if f.FieldName == d.identifier && instance > 1 {
			// The identifier belongs to the record's first instance (GD-8); on a
			// further instance's row it is a framing key only.
			continue
		}
		if f.Instrument != own && f.FieldName != d.identifier {
			problems = append(problems, f.FieldName+": CONTENT_INVALID — field '"+f.FieldName+
				"' belongs to instrument '"+f.Instrument+"', not '"+own+"'")
			continue
		}
		if f.isChoiceColumn() {
			// One box: 1 checks it, 0 clears it exactly like an empty value —
			// only checked boxes hold a stored row (REQ-DB-042).
			switch raw {
			case "0":
				raw = ""
			case "", "1":
			default:
				problems = append(problems, f.FieldName+": "+string(validate.CodeChoiceInvalid)+
					" — value '"+raw+"' of checkbox choice '"+f.FieldName+"' must be 0 or 1")
				continue
			}
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
		// §5.1 content policy after the type validator, before the write: UTF-8,
		// control characters and the §5.2 HTML allowlist, applied at this one
		// place so the data API import, the UI's data entry and survey links all
		// store identical bytes for identical input (REQ-VAL-030/001/004). The
		// stored result is what REQ-VAL-031 lets the UI render as allowlist HTML.
		policy, cv := validate.ApplyContentPolicy(vf, raw, h.Cfg.MaxValueBytes)
		if cv != nil {
			problems = append(problems, f.FieldName+": "+string(cv.Code)+" — "+cv.Message)
			continue
		}
		raw = policy
		pendings = append(pendings, pending{field: f, value: raw})
	}
	// Unknown keys, in dictionary order (REQ-API-139): map iteration would
	// shuffle this list between otherwise identical calls, so a caller
	// diffing two responses — or an audit comparison of two rejected batches —
	// could not tell whether the data changed.
	var unknown []string
	for k := range row {
		if importMetaKeys[k] {
			continue
		}
		if _, ok := d.byName[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		problems = append(problems, k+": "+string(validate.CodeUnknownField)+" — unknown field '"+k+"'")
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
	// Data-access-group scope (security finding F4): a holder with an active
	// group may only write records of that group — the same visibility rule
	// export and delete apply. An existing record never changes its group on
	// import (REQ-API-093), so a foreign record is a row-level failure; a new
	// record joins the holder's group below. A survey link has no group and
	// stays pinned to its own record by the check in the preamble.
	if entity != nil {
		groupID, err := h.activeGroupID(ctx, sub)
		if err != nil {
			return importRow{}, err
		}
		if groupID != nil && (!entity.DagGroupID.Valid || entity.DagGroupID.Int64 != *groupID) {
			return fail(idKey + ": PERMISSION_DENIED — the record belongs to another data access group")
		}
	}
	existed := len(oldValues) > 0 || entity != nil
	rv := valuesIndex(oldValues)
	ri := d.instancesIndex(oldValues)
	slotInstrument, slotNumber := db.InstanceSlot(repeatInstrument, instance)
	// target is the value map of the instance this row fills (REQ-DB-048).
	target := func() map[string]string {
		if instance == 1 {
			if rv[event] == nil {
				rv[event] = map[string]string{}
			}
			return rv[event]
		}
		k := instanceKey{event: event, instrument: repeatInstrument, instance: instance}
		if ri[k] == nil {
			ri[k] = map[string]string{}
		}
		return ri[k]
	}

	changes := map[string]map[string]string{}
	recordChange := func(field, oldV, newV string) {
		if oldV == newV {
			return
		}
		changes[field] = map[string]string{"old": oldV, "new": newV}
	}

	for _, pd := range pendings {
		values := target()
		oldV := values[pd.field.FieldName]
		dv := &db.DataValue{ProjectID: sub.Project.ID, RecordID: recordID,
			UniqueEventName: event, RepeatingInstrument: slotInstrument, RepeatingInstanceNumber: slotNumber,
			FieldName: pd.field.FieldName, Value: pd.value}
		if pd.value == "" {
			// Intentional clear (REQ-VAL-024): empty removes the value.
			if oldV != "" {
				if err := h.Store.DeleteDataValueTx(ctx, tx, dv); err != nil {
					return importRow{}, err
				}
				recordChange(pd.field.FieldName, oldV, "")
			}
			delete(values, pd.field.FieldName)
			continue
		}
		if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
			return importRow{}, err
		}
		recordChange(pd.field.FieldName, oldV, pd.value)
		values[pd.field.FieldName] = pd.value
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
	calcChanges, err := h.recomputeCalculated(ctx, tx, sub.Project.ID, recordID, rv, ri, d)
	if err != nil {
		return importRow{}, err
	}

	// Audit with old/new values in the data-change shape of
	// Audit_Logging_Design.md §3.2 (REQ-AUD-009): one entry per (record,
	// instrument, event) group, which is what the record history reads back
	// and filters (§4.16, REQ-API-079/080). A survey submission has no account
	// behind it: the actor columns stay unset and the link itself is the token
	// column (REQ-AUTH-041).
	eventType, action := audit.RecordCreated, "create"
	if existed {
		eventType, action = audit.RecordUpdated, "update"
	}
	if len(changes) > 0 || !existed {
		form := formName
		if repeatInstrument != "" {
			form = repeatInstrument
		}
		for _, details := range importChangeGroups(d, action, recordID, form, event, instance, changes) {
			if err := h.AuditTx(ctx, tx, audit.Entry{Token: p.Token,
				EventType:    eventType,
				Source:       audit.SourceAPI,
				UserID:       sub.actorUserID(),
				Email:        sub.actorEmail(),
				ProjectID:    sub.Project.ID,
				TargetRecord: recordID,
				Details:      details,
			}); err != nil {
				return importRow{}, err
			}
		}
	}
	// One calculated_recomputed entry per changed result (§3.2, REQ-AUD-023),
	// naming where it is stored and which change of this row set it off.
	for _, c := range sortedCalculated(calcChanges) {
		var triggerField, triggerEvent *string
		if c.Instance == instance || instance == 1 {
			// A row's changes reach every instance through the first one, and
			// their own instance directly (REQ-VAL-045).
			triggerField, triggerEvent = importTrigger(d.byName[c.Field].Calculation.String, event, changes)
		}
		if err := h.AuditTx(ctx, tx, audit.Entry{Token: p.Token,
			EventType:    audit.CalculatedRecomputed,
			Source:       audit.SourceAPI,
			UserID:       sub.actorUserID(),
			Email:        sub.actorEmail(),
			ProjectID:    sub.Project.ID,
			TargetRecord: recordID,
			Details: calculatedDetails{RecordID: recordID, Field: c.Field, Instrument: c.Instrument,
				Event: c.Event, Instance: furtherInstance(c.Instance), Old: c.Old, New: c.New,
				TriggerField: triggerField, TriggerEvent: triggerEvent},
		}); err != nil {
			return importRow{}, err
		}
	}
	if sub.isLink() {
		// Storing a submitted row stamps the link's collection date and, under
		// one-submission links, consumes it (REQ-DB-041, REQ-API-145) — this is
		// what makes the response "collected" for the edit-collected-surveys
		// right (REQ-AUTH-071). It rides in this transaction so a stored response
		// cannot lack the stamp. The write is conditional, so its row count is
		// also the guard: a submission racing a second one on the same link stamps
		// nothing, and the `defer tx.Rollback()` puts its values back with it.
		stamped, err := h.Store.MarkSurveyLinkCollectedTx(ctx, tx, sub.Link.ID)
		if err != nil {
			return importRow{}, err
		}
		if !stamped {
			return importRow{}, errSurveyUsed
		}
		// The submission's own entry rides along too, so a rollback cannot
		// leave a success on the record (REQ-AUD-021).
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

// errSurveyUsed marks a submission that lost the race for its link's one stamp
// (REQ-API-145): nothing was stored, and the call answers 410 rather than a
// result row — the link is spent, so there is no per-row outcome to report.
var errSurveyUsed = &surveyUsedError{}

type surveyUsedError struct{}

func (*surveyUsedError) Error() string { return "survey already submitted" }

// recomputeCalculated evaluates every calculated field of the project for
// one record against its current values and stores changed results in tx,
// returning each changed result with the event it is stored at (REQ-VAL-037).
// An unchecked checkbox box reads as 0 (§6.3, REQ-DB-042).
func (h *Handler) recomputeCalculated(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, rv recordValues, ri recordInstances, d *projectDict) ([]calculatedChange, error) {

	isBox := func(name string) bool { f, ok := d.byName[name]; return ok && f.isChoiceColumn() }
	// first resolves a reference in the record's first instances.
	first := func(ref validate.Ref) (string, bool) {
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
	}
	var out []calculatedChange
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
			changed, err := h.recomputeFurther(ctx, tx, projectID, recordID, f, event, ri, d, first, isBox)
			if err != nil {
				return nil, err
			}
			out = append(out, changed...)
			value, _, err := validate.EvalCalc(f.Calculation.String, validate.UncheckedAsZero(first, isBox))
			if err != nil {
				continue // malformed expression: never rewrite data with it
			}
			oldV := rv[event][f.FieldName]
			if oldV == value {
				continue
			}
			dv := &db.DataValue{ProjectID: projectID, RecordID: recordID,
				UniqueEventName: event, RepeatingInstanceNumber: 1, FieldName: f.FieldName, Value: value}
			if value == "" {
				if oldV != "" {
					if err := h.Store.DeleteDataValueTx(ctx, tx, dv); err != nil {
						return nil, err
					}
					out = append(out, calculatedChange{Field: f.FieldName, Instrument: f.Instrument,
						Event: event, Instance: 1, Old: oldV, New: ""})
					delete(rv[event], f.FieldName)
				}
				continue
			}
			if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
				return nil, err
			}
			out = append(out, calculatedChange{Field: f.FieldName, Instrument: f.Instrument,
				Event: event, Instance: 1, Old: oldV, New: value})
			if rv[event] == nil {
				rv[event] = map[string]string{}
			}
			rv[event][f.FieldName] = value
		}
	}
	return out, nil
}

// recomputeFurther recomputes one calculated field on the further instances of
// its instrument at one event (REQ-VAL-045): a reference to the instrument's own
// fields at that event — or without an event — reads the instance being
// computed, every other reference the record's first instances. An event where
// the instrument does not repeat has no visible further instance (REQ-DB-048).
func (h *Handler) recomputeFurther(ctx context.Context, tx *sql.Tx, projectID int64, recordID string,
	f recField, event string, ri recordInstances, d *projectDict,
	first func(validate.Ref) (string, bool), isBox func(string) bool) ([]calculatedChange, error) {

	if !d.repeats(event, f.Instrument) {
		return nil, nil
	}
	var out []calculatedChange
	for _, n := range ri.numbers(event, f.Instrument) {
		own := ri[instanceKey{event: event, instrument: f.Instrument, instance: n}]
		value, _, err := validate.EvalCalc(f.Calculation.String, validate.UncheckedAsZero(func(ref validate.Ref) (string, bool) {
			if rf, ok := d.byName[ref.Field]; ok && rf.Instrument == f.Instrument && (ref.Event == "" || ref.Event == event) {
				v := own[ref.Field]
				return v, v != ""
			}
			return first(ref)
		}, isBox))
		if err != nil {
			return out, nil // malformed expression: never rewrite data with it
		}
		oldV := own[f.FieldName]
		if oldV == value {
			continue
		}
		dv := &db.DataValue{ProjectID: projectID, RecordID: recordID, UniqueEventName: event,
			RepeatingInstrument: f.Instrument, RepeatingInstanceNumber: n, FieldName: f.FieldName, Value: value}
		if value == "" {
			if err := h.Store.DeleteDataValueTx(ctx, tx, dv); err != nil {
				return nil, err
			}
			delete(own, f.FieldName)
		} else {
			if err := h.Store.UpsertDataValueTx(ctx, tx, dv); err != nil {
				return nil, err
			}
			own[f.FieldName] = value
		}
		out = append(out, calculatedChange{Field: f.FieldName, Instrument: f.Instrument,
			Event: event, Instance: n, Old: oldV, New: value})
	}
	return out, nil
}

// validationRegistry builds the §4.2 registry from the seeded and custom
// validation_types rows, reusing it while the store's structure generation
// is unchanged (dictcache.go).
func (h *Handler) validationRegistry(ctx context.Context) (*validate.Registry, error) {
	gen := h.Store.StructureGeneration()
	return h.regs.get(gen, func() (*validate.Registry, error) {
		rows, err := h.Store.ListValidationTypes(ctx)
		if err != nil {
			return nil, err
		}
		entries := make([]validate.RegistryEntry, 0, len(rows))
		for _, vt := range rows {
			entries = append(entries, validate.RegistryEntry{Name: vt.Name, Regex: vt.Regex, Builtin: vt.Builtin})
		}
		return validate.NewRegistry(entries)
	})
}

// AuditTx writes one audit entry inside tx through the handler's writer; a
// handler without a writer (unit tests) skips it.
func (h *Handler) AuditTx(ctx context.Context, tx *sql.Tx, e audit.Entry) error {
	if h.Audit == nil {
		return nil
	}
	return h.Audit.InsertTx(ctx, tx, e)
}
