package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"csms/api/internal/audit"
	"csms/api/internal/authz"
	"csms/api/internal/db"
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
// dashboard: three states, no values (REQ-API-074). state is no_data or
// some_data as derived from the stored values, or finished when the user
// assigned it (§4.13, REQ-DB-036).
type recordStatusInstrument struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// recordStatusEvent is one event with its mapped instruments in the
// instrument order of the arm (§4.13 shape).
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
// (REQ-API-074, REQ-DB-036). Requires data access ≥ read_only + project
// visibility; events
// of arms without read access are omitted (GD-2). A record-status read is not
// a record view — no audit row is written (§4.13, ASM-AUD-2).
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
	for _, ie := range pairs {
		if mapped[ie.EventID] == nil {
			mapped[ie.EventID] = map[int64]bool{}
		}
		mapped[ie.EventID][ie.InstrumentID] = true
	}

	// The derived half of the state, in one pass: a (record, event, instrument)
	// holds some data when any stored value of it is non-empty ("any field has
	// a value vs. none"). Values are aggregated away here — none leaves this
	// function (REQ-API-074).
	type cellKey struct{ record, event, instrument string }
	complete := map[cellKey]bool{}
	rows, err := h.Store.DB.QueryContext(ctx,
		`SELECT DISTINCT record_id, unique_event_name, repeating_instrument
		 FROM data WHERE project_id = ? AND value <> ''`, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k cellKey
		if err := rows.Scan(&k.record, &k.event, &k.instrument); err != nil {
			errInternal(w)
			return
		}
		complete[k] = true
	}
	if err := rows.Err(); err != nil {
		errInternal(w)
		return
	}

	// The stored "finished" assignments (REQ-DB-036) — the sparse set, keyed by
	// the ids the loop below already holds. Nothing here reveals a value.
	type storedKey struct {
		record            string
		event, instrument int64
	}
	finished := map[storedKey]bool{}
	completions, err := h.Store.ListInstrumentCompletions(ctx, projectID)
	if err != nil {
		errInternal(w)
		return
	}
	for _, c := range completions {
		finished[storedKey{c.RecordID, c.EventID, c.InstrumentID}] = true
	}

	out := make([]recordStatusRow, 0, len(visible))
	for _, re := range visible {
		row := recordStatusRow{RecordID: re.RecordID, Events: []recordStatusEvent{}}
		for _, a := range arms { // CanonicalEvents order: arms, then GD-15
			if !u.IsAdmin && !lv.HasData(a.ArmNum, LvlReadOnly) {
				continue // events of inaccessible arms are not disclosed (GD-2)
			}
			for _, ev := range eventsByArm[a.ArmNum] {
				var instrs []recordStatusInstrument
				for _, in := range instruments { // instrument order (position)
					if !mapped[ev.ID][in.ID] {
						continue
					}
					// Derived first, then overridden by the user's assignment:
					// finished wins over both, never the other way round, so
					// the badge cannot claim less than the data shows and
					// grey/amber can never contradict it (REQ-API-074). A
					// survey instrument reports finished automatically — its
					// completion info is filled in without an assignment
					// (GD-9; master spec "Instrument level completion info").
					state := StateNoData
					if complete[cellKey{re.RecordID, ev.UniqueEventName, in.Name}] {
						state = StateSomeData
					}
					if in.IsSurvey || finished[storedKey{re.RecordID, ev.ID, in.ID}] {
						state = StateFinished
					}
					instrs = append(instrs, recordStatusInstrument{
						Name: in.Name, State: state,
					})
				}
				if len(instrs) == 0 {
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
	CreatedAt       string         `json:"created_at"`
	UserID          *int64         `json:"user_id"`
	UserDisplayName *string        `json:"user_display_name"`
	Action          string         `json:"action"`
	Instrument      string         `json:"instrument"`
	Event           string         `json:"event"`
	Fields          []historyField `json:"fields"`
}

// historyDetails is the data-change payload of record_created /
// record_updated / record_deleted (Audit_Logging_Design.md §3.2).
type historyDetails struct {
	Action     string         `json:"action"`
	Instrument string         `json:"instrument"`
	Event      string         `json:"event"`
	Fields     []historyField `json:"fields"`
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

	limit := ParseLimit(r)
	// The record-scoped page: target_record + the three data-change codes,
	// chronological (REQ-API-079), served by idx_audit_events_record (§6.4).
	conds := []string{
		"ae.project_id = ?", "ae.target_record = ?",
		fmt.Sprintf("ae.event_type IN (%s, %s, %s)", "?", "?", "?"),
	}
	args := []any{projectID, recordID,
		audit.RecordCreated, audit.RecordUpdated, audit.RecordDeleted}
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
		`SELECT ae.id, ae.created_at, ae.user_id, u.display_name, ae.details
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
			id      int64
			created any
			userID  sql.NullInt64
			display sql.NullString
			details sql.NullString
		)
		if err := rows.Scan(&id, &created, &userID, &display, &details); err != nil {
			errInternal(w)
			return
		}
		scanned++
		lastCreated = normDatetime(created)
		lastID = id

		var d historyDetails
		if !details.Valid || json.Unmarshal([]byte(details.String), &d) != nil {
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
		if fieldFilter != "" {
			hit := false
			for _, f := range d.Fields {
				if f.Field == fieldFilter {
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
