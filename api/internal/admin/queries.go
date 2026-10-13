package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
	"csms/api/internal/validate"
)

// registerQueries mounts the query surface of §4: the record-status dashboard
// (§4.13, REQ-API-074), the audit log read (§4.15, REQ-API-077/078), and the
// per-record history (§4.16, REQ-API-079…081). All three are read-only; no
// endpoint here writes, updates, or deletes audit data (REQ-API-077).
func (h *Handler) registerQueries(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{id}/record-status", h.recordStatus)
	mux.HandleFunc("GET /api/v1/audit", h.listAudit)
	mux.HandleFunc("GET /api/v1/projects/{id}/records/{record}/history", h.recordHistory)
}

// --- GET /api/v1/projects/{id}/record-status (§4.13, REQ-API-074) ---

// recordStatusInstrument is one (record, event, instrument) cell of the
// dashboard: three states, no values (REQ-API-074). state is some_data as
// derived from the stored values, or finished when the user assigned it
// (§4.13, REQ-DB-036); no_data cells are not listed (REQ-API-149).
type recordStatusInstrument struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Instances lists, for an instrument repeating at this event, every instance
	// holding a state other than no_data in ascending order (REQ-API-156); State
	// is then the first instance's. Absent for a non-repeating pair.
	Instances []recordStatusInstance `json:"instances,omitempty"`
}

// recordStatusInstance is one instance of a repeating instrument at one event:
// its number and its three-state completion state (REQ-API-156).
type recordStatusInstance struct {
	Instance int    `json:"instance"`
	State    string `json:"state"`
}

// recordStatusEvent is one event with the mapped instruments that hold a
// state other than no_data, in the instrument order of the arm (§4.13 shape,
// REQ-API-149). An event is listed whenever it has a mapped instrument, so an
// empty list says "nothing here yet" rather than "not reported".
type recordStatusEvent struct {
	UniqueEventName string                   `json:"unique_event_name"`
	Instruments     []recordStatusInstrument `json:"instruments"`
}

// recordStatusRow is one visible record with its events.
type recordStatusRow struct {
	RecordID string              `json:"record_id"`
	Events   []recordStatusEvent `json:"events"`
}

// recordStatus returns all records visible to the acting user under the DAG
// rule (REQ-AUTH-045) with a three-state completion state per (record, event,
// instrument) — no_data / some_data derived, finished stored
// (REQ-API-074, REQ-DB-036). The answer is sparse (REQ-API-149): no_data
// cells are left out, because the dense matrix grows with records × events ×
// mapped instruments however little data exists. Requires data access ≥
// read_only + project visibility; events of arms without read access are
// omitted (GD-2). A record-status read is not a record view — no audit row is
// written (§4.13, ASM-AUD-2).
// checkboxDataCells reports every (record, event, instrument, instance) holding
// a checked box: the derived "some data" half of record-status for values stored
// under a checkbox's choice columns (REQ-DB-042, REQ-API-074). Like the join it
// complements, it hands out positions only — never a value. repeating is the
// stored repeating_instrument column — empty for a first instance (REQ-DB-048).
func (h *Handler) checkboxDataCells(ctx context.Context, projectID int64, add func(record, event, instrument, repeating string, instance int)) error {
	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT f.field_name, f.choices, i.name
		 FROM fields f JOIN instruments i ON i.id = f.instrument_id
		 WHERE f.project_id = ? AND f.field_type = 'checkbox'`, projectID)
	if err != nil {
		return err
	}
	instrumentOf := map[string]string{} // choice column -> instrument name
	for rows.Next() {
		var name, instrument string
		var choices sql.NullString
		if err := rows.Scan(&name, &choices, &instrument); err != nil {
			rows.Close()
			return err
		}
		for _, code := range validate.ChoiceCodes(choices.String) {
			instrumentOf[db.CheckboxSubName(name, code)] = instrument
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(instrumentOf) == 0 {
		return nil
	}
	args := make([]any, 0, len(instrumentOf)+1)
	args = append(args, projectID)
	for name := range instrumentOf {
		args = append(args, name)
	}
	cells, err := h.Store.DB.QueryContext(ctx,
		`SELECT DISTINCT record_id, unique_event_name, field_name, repeating_instrument, repeating_instance_number
		 FROM data
		 WHERE project_id = ? AND value <> '' AND field_name IN (`+placeholders(len(instrumentOf))+`)`, args...)
	if err != nil {
		return err
	}
	defer cells.Close()
	for cells.Next() {
		var record, event, name, repeating string
		var instance int
		if err := cells.Scan(&record, &event, &name, &repeating, &instance); err != nil {
			return err
		}
		add(record, event, instrumentOf[name], repeating, instance)
	}
	return cells.Err()
}

func (h *Handler) recordStatus(w http.ResponseWriter, r *http.Request) {
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

	// Visible records under the DAG rule (REQ-AUTH-045): an administrator
	// sees all; a member with an active group sees only the records assigned
	// to it (unassigned records are not visible); a member without a group
	// sees all — the bulk form of authz.RecordVisible.
	entities, err := h.Store.ListRecordEntities(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	group := int64(0)
	if !u.IsAdmin {
		group, err = authz.ActiveGroup(ctx, h.Store, u.ID, projectID)
		if err != nil {
			errInternal(w)
			return
		}
	}
	visible := make([]db.RecordEntity, 0, len(entities))
	for _, re := range entities {
		if group != 0 && !(re.DagGroupID.Valid && re.DagGroupID.Int64 == group) {
			continue
		}
		visible = append(visible, re)
	}

	// Structure: canonical event listing per GD-15 (arms in order, events in
	// canonical order), instruments in position order, and the checked
	// (instrument, event) pairs.
	arms, eventsByArm, err := h.CanonicalEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	instruments, err := h.Store.ListInstruments(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	pairs, err := h.Store.ListInstrumentEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	mapped := map[int64]map[int64]bool{} // event id -> instrument ids
	repeating := map[[2]int64]bool{}     // (event id, instrument id) designated repeating
	for _, ie := range pairs {
		if mapped[ie.EventID] == nil {
			mapped[ie.EventID] = map[int64]bool{}
		}
		mapped[ie.EventID][ie.InstrumentID] = true
		if ie.IsRepeating {
			repeating[[2]int64{ie.EventID, ie.InstrumentID}] = true
		}
	}

	// The derived half of the state, in one pass: a (record, event, instrument)
	// holds some data when any stored value of one of the instrument's fields
	// is non-empty at that event ("any field has a value vs. none"). A value
	// belongs to an instrument through its field — the data row's
	// repeating_instrument column names a repeat instance, and is empty for
	// every value of a first instance, and names the instrument of a further
	// instance (REQ-DB-048) — which is the same test the export's
	// `<instrument>_complete` column applies (REQ-API-134). Values are
	// aggregated away here — none leaves this function (REQ-API-074).
	type cellKey struct {
		record, event, instrument string
		instance                  int
	}
	complete := map[cellKey]bool{}
	// further collects, per (record, event, instrument), the numbers of the
	// instances above 1 that hold something — the instances a repeating cell
	// lists besides its first (REQ-API-156).
	type pairKey struct{ record, event, instrument string }
	further := map[pairKey]map[int]bool{}
	note := func(record, event, instrument, repeatingInstrument string, n int) {
		instance := db.DataValue{RepeatingInstrument: repeatingInstrument, RepeatingInstanceNumber: n}.Instance()
		if instance > 1 && repeatingInstrument != instrument {
			return // a further instance is stored under its own instrument's name
		}
		complete[cellKey{record, event, instrument, instance}] = true
		if instance > 1 {
			k := pairKey{record, event, instrument}
			if further[k] == nil {
				further[k] = map[int]bool{}
			}
			further[k][instance] = true
		}
	}
	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT DISTINCT d.record_id, d.unique_event_name, i.name, d.repeating_instrument, d.repeating_instance_number
		 FROM data d
		 JOIN fields f ON f.project_id = d.project_id AND f.field_name = d.field_name
		 JOIN instruments i ON i.id = f.instrument_id
		 WHERE d.project_id = ? AND d.value <> ''`, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var record, event, instrument, repeatingInstrument string
		var n int
		if err := rows.Scan(&record, &event, &instrument, &repeatingInstrument, &n); err != nil {
			errInternal(w)
			return
		}
		note(record, event, instrument, repeatingInstrument, n)
	}
	if err := rows.Err(); err != nil {
		errInternal(w)
		return
	}
	// A checkbox stores its values under its choice columns, which name no
	// fields row (REQ-DB-042): they reach their instrument through the checkbox
	// in a second pass that only a project with checkboxes pays for.
	if err := h.checkboxDataCells(ctx, projectID, note); err != nil {
		errInternal(w)
		return
	}

	// The stored "finished" assignments (REQ-DB-036) — the sparse set, keyed by
	// the ids the loop below already holds. Nothing here reveals a value.
	type storedKey struct {
		record            string
		event, instrument int64
		instance          int
	}
	finished := map[storedKey]bool{}
	type storedPair struct {
		record            string
		event, instrument int64
	}
	finishedFurther := map[storedPair][]int{}
	completions, err := h.Store.ListInstrumentCompletions(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	for _, c := range completions {
		instance := max(c.Instance, 1)
		finished[storedKey{c.RecordID, c.EventID, c.InstrumentID, instance}] = true
		if instance > 1 {
			k := storedPair{c.RecordID, c.EventID, c.InstrumentID}
			finishedFurther[k] = append(finishedFurther[k], instance)
		}
	}

	out := make([]recordStatusRow, 0, len(visible))
	for _, re := range visible {
		row := recordStatusRow{RecordID: re.RecordID, Events: []recordStatusEvent{}}
		for _, a := range arms { // CanonicalEvents order: arms, then GD-15
			if !u.IsAdmin && !lv.HasData(a.ArmNum, LvlReadOnly) {
				continue // events of inaccessible arms are not disclosed (GD-2)
			}
			for _, ev := range eventsByArm[a.ArmNum] {
				instrs := []recordStatusInstrument{}
				anyMapped := false
				for _, in := range instruments { // instrument order (position)
					if !mapped[ev.ID][in.ID] {
						continue
					}
					anyMapped = true
					// Derived first, then overridden by the user's assignment:
					// finished wins over both, never the other way round, so
					// the badge cannot claim less than the data shows and
					// grey/amber can never contradict it (REQ-API-074). A
					// survey instrument reports finished automatically — its
					// completion info is filled in without an assignment
					// (GD-9; master spec "Instrument level completion info").
					stateOf := func(instance int) string {
						state := StateNoData
						if complete[cellKey{re.RecordID, ev.UniqueEventName, in.Name, instance}] {
							state = StateSomeData
						}
						if in.IsSurvey || finished[storedKey{re.RecordID, ev.ID, in.ID, instance}] {
							state = StateFinished
						}
						return state
					}
					state := stateOf(1)
					if !repeating[[2]int64{ev.ID, in.ID}] {
						// A pair that does not repeat shows its first instance only:
						// further instances it held while it did stay stored, hidden
						// until the pair repeats again (REQ-DB-048).
						if state == StateNoData {
							continue // the listed event already says it (REQ-API-149)
						}
						instrs = append(instrs, recordStatusInstrument{
							Name: in.Name, State: state,
						})
						continue
					}
					numbers := map[int]bool{}
					if state != StateNoData {
						numbers[1] = true
					}
					for n := range further[pairKey{re.RecordID, ev.UniqueEventName, in.Name}] {
						numbers[n] = true
					}
					for _, n := range finishedFurther[storedPair{re.RecordID, ev.ID, in.ID}] {
						numbers[n] = true
					}
					if len(numbers) == 0 {
						continue // no instance holds anything (REQ-API-149)
					}
					instances := make([]recordStatusInstance, 0, len(numbers))
					for n := range numbers {
						instances = append(instances, recordStatusInstance{Instance: n, State: stateOf(n)})
					}
					sort.Slice(instances, func(i, j int) bool { return instances[i].Instance < instances[j].Instance })
					instrs = append(instrs, recordStatusInstrument{
						Name: in.Name, State: state, Instances: instances,
					})
				}
				if !anyMapped {
					continue // events without mapped instruments carry no state
				}
				row.Events = append(row.Events, recordStatusEvent{
					UniqueEventName: ev.UniqueEventName, Instruments: instrs,
				})
			}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- GET /api/v1/audit (§4.15, REQ-API-077/078) ---

// auditEventEntry is one audit_events row of the §4.15 response; details is
// the per-event JSON payload, rendered as-is (the trail is the sanctioned
// carrier, REQ-AUD-007).
type auditEventEntry struct {
	ID        int64           `json:"id"`
	CreatedAt string          `json:"created_at"`
	Source    string          `json:"source"`
	ProjectID *int64          `json:"project_id"`
	UserID    *int64          `json:"user_id"`
	Email     *string         `json:"email"`
	EventType string          `json:"event_type"`
	Details   json.RawMessage `json:"details"`
}

// auditViewEntry is one audit_record_views row — the token is the only actor
// a record pull has (§4 of the audit design), so it appears here.
type auditViewEntry struct {
	ID          int64    `json:"id"`
	CreatedAt   string   `json:"created_at"`
	UserID      *int64   `json:"user_id"`
	Email       *string  `json:"email"`
	Token       string   `json:"token"`
	ProjectID   int64    `json:"project_id"`
	RecordIDs   []string `json:"record_ids"`
	Instruments []string `json:"instruments"`
}

// auditPage is the paginated §4.15 envelope.
type auditPage struct {
	Entries    any `json:"entries"`
	NextCursor any `json:"next_cursor"`
}

// listAudit returns audit entries in reverse chronological order with
// limit/cursor pagination (§1 convention). Audit reads are reserved to
// is_admin (REQ-API-078, finding F2): view entries carry the live data-API
// token and event details carry record values, neither of which a project
// member is entitled to see. Filters: type=events|views, project, user,
// event_type, from/to (UTC, inclusive).
func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()

	table := q.Get("type")
	if table == "" {
		table = "events"
	}
	if table != "events" && table != "views" {
		errBadRequest(w, `type must be "events" or "views"`)
		return
	}

	var projectFilter *int64
	if v := q.Get("project"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errBadRequest(w, "invalid project filter")
			return
		}
		projectFilter = &n
	}
	var userFilter *int64
	if v := q.Get("user"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errBadRequest(w, "invalid user filter")
			return
		}
		userFilter = &n
	}
	eventType := q.Get("event_type")
	if eventType != "" && table == "views" {
		errBadRequest(w, "the event_type filter applies to type=events only")
		return
	}
	from := ""
	if v := q.Get("from"); v != "" {
		var ok bool
		if from, ok = parseRangeBound(v, false); !ok {
			errBadRequest(w, "from must be a UTC date or datetime")
			return
		}
	}
	to := ""
	if v := q.Get("to"); v != "" {
		var ok bool
		if to, ok = parseRangeBound(v, true); !ok {
			errBadRequest(w, "to must be a UTC date or datetime")
			return
		}
	}

	// The stable names audit_events / audit_record_views exist only after the
	// year objects do (audit design §6) — cheap latch check first.
	if err := h.Audit.EnsureYear(ctx); err != nil {
		errInternal(w)
		return
	}

	limit := ParseLimit(r)
	conds := []string{"1=1"}
	var args []any
	if projectFilter != nil {
		conds = append(conds, "project_id = ?")
		args = append(args, *projectFilter)
	}
	if userFilter != nil {
		conds = append(conds, "user_id = ?")
		args = append(args, *userFilter)
	}
	if eventType != "" {
		conds = append(conds, "event_type = ?")
		args = append(args, eventType)
	}
	if from != "" {
		conds = append(conds, "created_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		conds = append(conds, "created_at <= ?")
		args = append(args, to)
	}
	// Reverse-chronological cursor: strictly after the last-seen
	// (created_at, id) pair in DESC order (§1).
	if c := ParseCursor(r); c != nil {
		conds = append(conds, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, c.CreatedAt, c.CreatedAt, c.ID)
	}

	base := " WHERE " + joinConditions(conds) + " ORDER BY created_at DESC, id DESC LIMIT ?"
	if table == "views" {
		h.queryAuditViews(w, ctx, base, args, limit)
		return
	}

	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT id, created_at, source, project_id, user_id, email, event_type, details
		 FROM audit_events`+base, append(args, limit)...)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()
	entries := make([]auditEventEntry, 0, limit)
	var lastCreated string
	var lastID int64
	for rows.Next() {
		var (
			e       auditEventEntry
			created any
			pid     sql.NullInt64
			uid     sql.NullInt64
			email   sql.NullString
			details sql.NullString
		)
		if err := rows.Scan(&e.ID, &created, &e.Source, &pid, &uid, &email,
			&e.EventType, &details); err != nil {
			errInternal(w)
			return
		}
		e.CreatedAt = normDatetime(created)
		e.ProjectID = Int64Ptr(pid)
		e.UserID = Int64Ptr(uid)
		e.Email = NullStrPtr(email)
		if details.Valid {
			e.Details = json.RawMessage(details.String)
		}
		lastCreated, lastID = e.CreatedAt, e.ID
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, auditPage{
		Entries:    entries,
		NextCursor: NextCursor(len(entries), limit, lastCreated, lastID),
	})
}

// queryAuditViews serves the type=views branch of §4.15 — the record-pull
// trail (audit_record_views).
func (h *Handler) queryAuditViews(w http.ResponseWriter, ctx context.Context, where string, args []any, limit int) {
	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT id, created_at, user_id, email, token, project_id, record_ids, instruments
		 FROM audit_record_views`+where, append(args, limit)...)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()
	entries := make([]auditViewEntry, 0, limit)
	var lastCreated string
	var lastID int64
	for rows.Next() {
		var (
			e       auditViewEntry
			created any
			uid     sql.NullInt64
			email   sql.NullString
			recIDs  string
			instrs  sql.NullString
		)
		if err := rows.Scan(&e.ID, &created, &uid, &email, &e.Token,
			&e.ProjectID, &recIDs, &instrs); err != nil {
			errInternal(w)
			return
		}
		e.CreatedAt = normDatetime(created)
		e.UserID = Int64Ptr(uid)
		e.Email = NullStrPtr(email)
		e.RecordIDs = []string{}
		if json.Unmarshal([]byte(recIDs), &e.RecordIDs) != nil {
			e.RecordIDs = []string{}
		}
		e.Instruments = []string{}
		if instrs.Valid {
			if json.Unmarshal([]byte(instrs.String), &e.Instruments) != nil {
				e.Instruments = []string{}
			}
		}
		lastCreated, lastID = e.CreatedAt, e.ID
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, auditPage{
		Entries:    entries,
		NextCursor: NextCursor(len(entries), limit, lastCreated, lastID),
	})
}

// --- GET /api/v1/projects/{id}/records/{record}/history (§4.16) ---

// historyField is one changed field with its old and new value — null on the
// untouched side per REQ-AUD-009 (create: old null; delete: new null).
type historyField struct {
	Field string  `json:"field"`
	Old   *string `json:"old"`
	New   *string `json:"new"`
}

// historyEntry is one chronological data-change row of the §4.16 response.
type historyEntry struct {
	CreatedAt       string  `json:"created_at"`
	UserID          *int64  `json:"user_id"`
	UserDisplayName *string `json:"user_display_name"`
	Action          string  `json:"action"`
	Instrument      string  `json:"instrument"`
	Event           string  `json:"event"`
	// Instance is the form instance the change belongs to: 1 for every form that
	// does not repeat, 2, 3, … for further instances of a repeating instrument
	// (REQ-DB-048, REQ-API-156).
	Instance int            `json:"instance"`
	Fields   []historyField `json:"fields"`
}

// historyDetails is the data-change payload of record_created /
// record_updated / record_deleted (Audit_Logging_Design.md §3.2).
type historyDetails struct {
	Action     string         `json:"action"`
	Instrument string         `json:"instrument"`
	Event      string         `json:"event"`
	Instance   int            `json:"instance"` // absent = the first instance
	Fields     []historyField `json:"fields"`
}

// recomputedDetails is the calculated_recomputed payload of §3.2: one stored
// result, where it is stored, and its old and new value. The history serves it
// as an update of that field, so a calculated value has a history and a current
// value like any entered one (REQ-API-079, REQ-AUD-023).
type recomputedDetails struct {
	Field      string `json:"field"`
	Instrument string `json:"instrument"`
	Event      string `json:"event"`
	Instance   int    `json:"instance"`
	Old        string `json:"old"`
	New        string `json:"new"`
}

// asHistory reads one entry's payload into the data-change shape: the
// data-change codes as written, a recomputation as an update of its one field.
// A payload without that shape yields false and is skipped.
func asHistory(eventType string, raw []byte) (historyDetails, bool) {
	if eventType == audit.CalculatedRecomputed {
		var c recomputedDetails
		if json.Unmarshal(raw, &c) != nil || c.Field == "" {
			return historyDetails{}, false
		}
		old, nw := c.Old, c.New
		f := historyField{Field: c.Field, Old: &old, New: &nw}
		if old == "" {
			f.Old = nil
		}
		if nw == "" {
			f.New = nil
		}
		return historyDetails{Action: "update", Instrument: c.Instrument, Event: c.Event,
			Instance: c.Instance, Fields: []historyField{f}}, true
	}
	var d historyDetails
	if json.Unmarshal(raw, &d) != nil {
		return historyDetails{}, false
	}
	return d, true
}

// recordHistory returns the record's data-change history from the audit trail:
// every create/update/delete entry with the acting user, the action, and per
// changed field the old → new values (REQ-API-079). order=chrono (default)
// serves entries oldest-first; order=newest reverses both the ORDER BY and the
// cursor so a client deriving current values walks backwards from the newest
// change (REQ-API-137, DEV-API-23) — same handler, same disclosure either way.
// Requires data access ≥ read_only on the record's arm (GD-2), project
// visibility (REQ-API-007) and record visibility under the DAG rule
// (REQ-AUTH-045). Filters instrument, event and field apply within the
// record-scoped page (REQ-API-080); the cursor advances over the DB page so
// filtered pages keep paging. Queries use the stable audit_events name and
// are transparent across the yearly rollover (REQ-AUD-006).
func (h *Handler) recordHistory(w http.ResponseWriter, r *http.Request) {
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
	recordID := r.PathValue("record")
	if recordID == "" {
		errBadRequest(w, "record id is required")
		return
	}
	order := r.URL.Query().Get("order")
	if order != "" && order != "chrono" && order != "newest" {
		errBadRequest(w, "order must be chrono or newest")
		return
	}
	newest := order == "newest"
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
	visible, err := authz.RecordVisible(ctx, h.Store, u, projectID, recordID)
	if err != nil {
		errInternal(w)
		return
	}
	if !visible { // uniform 403 — never discloses the record (REQ-API-007)
		errForbidden(w)
		return
	}
	if err := h.Audit.EnsureYear(ctx); err != nil {
		errInternal(w)
		return
	}

	// Event → arm resolution for the per-arm read check (GD-2).
	events, err := h.Store.ListEvents(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	arms, err := h.Store.ListArms(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	armNumByArmID := map[int64]int{}
	for _, a := range arms {
		armNumByArmID[a.ID] = a.ArmNum
	}
	armNumByEvent := map[string]int{}
	for _, e := range events {
		armNumByEvent[e.UniqueEventName] = armNumByArmID[e.ArmID]
	}

	instrumentFilter := r.URL.Query().Get("instrument")
	eventFilter := r.URL.Query().Get("event")
	fieldFilter := r.URL.Query().Get("field")
	instanceFilter := 0
	if v := r.URL.Query().Get("instance"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errBadRequest(w, "instance must be a positive integer")
			return
		}
		instanceFilter = n
	}
	// A checkbox's history is that of its choice columns, which carry its values
	// (REQ-DB-042): filtering by the checkbox matches every one of them.
	fieldMatch := map[string]bool{fieldFilter: true}
	if fieldFilter != "" {
		f, err := h.Store.GetFieldByName(ctx, projectID, fieldFilter)
		if err != nil {
			errInternal(w)
			return
		}
		if f != nil {
			for _, sub := range checkboxSubs(*f) {
				fieldMatch[sub] = true
			}
		}
	}

	limit := ParseLimit(r)
	// The record-scoped page: target_record + the data-change codes of §3.2 —
	// recomputed calculated results included, they change the record's values
	// too — chronological (REQ-API-079), served by idx_audit_events_record (§6.4).
	conds := []string{
		"ae.project_id = ?", "ae.target_record = ?",
		fmt.Sprintf("ae.event_type IN (%s, %s, %s, %s)", "?", "?", "?", "?"),
	}
	args := []any{projectID, recordID,
		audit.RecordCreated, audit.RecordUpdated, audit.RecordDeleted, audit.CalculatedRecomputed}
	dir := "ASC"
	pageOp := ">"
	if newest { // REQ-API-137 — the same walk from the newest change backwards
		dir = "DESC"
		pageOp = "<"
	}
	if c := ParseCursor(r); c != nil {
		conds = append(conds, fmt.Sprintf(
			"(ae.created_at %s ? OR (ae.created_at = ? AND ae.id %s ?))", pageOp, pageOp))
		args = append(args, c.CreatedAt, c.CreatedAt, c.ID)
	}

	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT ae.id, ae.created_at, ae.user_id, u.display_name, ae.event_type, ae.details
		 FROM audit_events ae LEFT JOIN users u ON u.id = ae.user_id
		 WHERE `+joinConditions(conds)+`
		 ORDER BY ae.created_at `+dir+`, ae.id `+dir+` LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()
	entries := make([]historyEntry, 0, limit)
	scanned := 0
	var lastCreated string
	var lastID int64
	for rows.Next() {
		var (
			id        int64
			created   any
			userID    sql.NullInt64
			display   sql.NullString
			eventType string
			details   sql.NullString
		)
		if err := rows.Scan(&id, &created, &userID, &display, &eventType, &details); err != nil {
			errInternal(w)
			return
		}
		scanned++
		lastCreated = normDatetime(created)
		lastID = id

		if !details.Valid {
			continue
		}
		d, ok := asHistory(eventType, []byte(details.String))
		if !ok {
			continue // unparseable payload — nothing renderable (defensive)
		}
		// Per-arm read check (GD-2): entries of arms the acting user cannot
		// read are omitted. An event no longer in the design (deleted) keeps
		// its history row — the trail stays complete (REQ-API-080).
		if armNum, known := armNumByEvent[d.Event]; known && !u.IsAdmin &&
			!lv.HasData(armNum, LvlReadOnly) {
			continue
		}
		if instrumentFilter != "" && d.Instrument != instrumentFilter {
			continue
		}
		if eventFilter != "" && d.Event != eventFilter {
			continue
		}
		instance := max(d.Instance, 1)
		if instanceFilter != 0 && instance != instanceFilter {
			continue
		}
		if fieldFilter != "" {
			hit := false
			for _, f := range d.Fields {
				if fieldMatch[f.Field] {
					hit = true
					break
				}
			}
			if !hit {
				continue // the field filter matches within the page (REQ-API-080)
			}
		}
		fields := d.Fields
		if fields == nil {
			fields = []historyField{}
		}
		entries = append(entries, historyEntry{
			CreatedAt:       lastCreated,
			UserID:          Int64Ptr(userID),
			UserDisplayName: NullStrPtr(display),
			Action:          d.Action,
			Instrument:      d.Instrument,
			Event:           d.Event,
			Instance:        instance,
			Fields:          fields,
		})
	}
	if err := rows.Err(); err != nil {
		errInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, auditPage{
		Entries: entries,
		// The cursor advances over the record-scoped DB page, not the
		// filtered view of it, so filtering never skips entries.
		NextCursor: NextCursor(scanned, limit, lastCreated, lastID),
	})
}

// --- shared helpers for the query surface ---

// joinConditions ANDs the WHERE fragments of a built query.
func joinConditions(conds []string) string {
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}

// parseRangeBound normalizes a from/to filter value to the canonical DATETIME
// layout: a full "YYYY-MM-DD HH:MM:SS" passes through; a bare "YYYY-MM-DD"
// becomes the start or end of that UTC day (the range is inclusive).
func parseRangeBound(v string, endOfDay bool) (string, bool) {
	if t, ok := ParseUTCTime(v); ok {
		return t.Format("2006-01-02 15:04:05"), true
	}
	if d, err := time.Parse("2006-01-02", v); err == nil {
		if endOfDay {
			return d.Format("2006-01-02") + " 23:59:59", true
		}
		return d.Format("2006-01-02") + " 00:00:00", true
	}
	return "", false
}

// normDatetime normalizes a scanned DATETIME value to the canonical UTC text
// (the db package's normalizer is unexported; MariaDB drivers hand back
// time.Time with parseTime=true).
func normDatetime(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprintf("%v", v)
	}
}
