// Package audit is the event catalog and the same-transaction writer for the
// two audit tables (Audit_Logging_Design.md). The audit INSERT is issued
// inside the transaction of the operation it describes (REQ-AUD-003,
// DEV-AUD-1): a rollback of the operation rolls back the entry, and a failure
// to write the entry fails the operation.
//
// Physical layout behind the stable names audit_events / audit_record_views
// (§6): MariaDB uses yearly partitions on the table itself; SQLite uses
// per-year tables behind a UNION ALL view. Reads always use the stable name;
// writes on SQLite target the current-year physical table directly, because
// a UNION ALL view is not insertable.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event catalog (§3, normative). Adding a code is non-breaking; changing or
// removing one is breaking.
const (
	// §3.1 authentication and administration boundary
	LoginSuccess        = "login_success"
	LoginFailure        = "login_failure"
	Logout              = "logout"
	AdminRejected       = "admin_rejected"
	AccountAutoDisabled = "account_auto_disabled"
	TFAEnrolled         = "tfa_enrolled" // REQ-AUD-028
	TFADisabled         = "tfa_disabled" // REQ-AUD-028
	TFAReset            = "tfa_reset"    // REQ-AUD-028
	// SessionReplayDetected: a rotated-away session id came back (REQ-AUD-030).
	SessionReplayDetected = "session_replay_detected"

	// §3.2 data change events
	RecordCreated         = "record_created"
	RecordUpdated         = "record_updated"
	RecordDeleted         = "record_deleted"
	CalculatedRecomputed  = "calculated_recomputed"
	InstrumentCompleted   = "instrument_completed"   // REQ-AUD-026, REQ-API-110
	InstrumentUncompleted = "instrument_uncompleted" // REQ-AUD-026
	TransferExecuted      = "transfer_executed"      // REQ-AUD-034, REQ-API-165: a cross-project transfer ran (source project)

	// §3.3 project structure events
	ProjectCreated      = "project_created"
	ProjectUpdated      = "project_updated"
	ArmCreated          = "arm_created"
	ArmUpdated          = "arm_updated"
	ArmDeleted          = "arm_deleted"
	ArmReordered        = "arm_reordered"
	EventCreated        = "event_created"
	EventUpdated        = "event_updated"
	EventDeleted        = "event_deleted"
	EventReordered      = "event_reordered"
	InstrumentCreated   = "instrument_created"
	InstrumentUpdated   = "instrument_updated"
	InstrumentDeleted   = "instrument_deleted"
	InstrumentReordered = "instrument_reordered"
	FieldCreated        = "field_created"
	FieldUpdated        = "field_updated"
	FieldDeleted        = "field_deleted"
	FieldReordered      = "field_reordered"
	FieldMoved          = "field_moved" // REQ-API-157: to another instrument, or within its own
	MappingUpdated      = "mapping_updated"
	ProjectEnded        = "project_ended"

	// §3.4 administration events
	UserCreated       = "user_created"
	UserUpdated       = "user_updated"
	MembershipChanged = "membership_changed"
	TokenIssued       = "token_issued"
	TokenRotated      = "token_rotated"
	TokenRevoked      = "token_revoked"
	TokenViewed       = "token_viewed" // REQ-AUD-033: an administrator unmasked a just-issued token, or a member their own
	RoleCreated       = "role_created"
	RoleUpdated       = "role_updated" // REQ-API-143: before/after permission set, never values
	I18nUpdated       = "i18n_updated"
	SettingsUpdated   = "settings_updated" // REQ-AUD-027, REQ-API-112

	// password lifecycle (Sequence H) — token values never enter details (REQ-AUD-029)
	UserInvited            = "user_invited"             // REQ-AUD-029, GD-22
	InviteAccepted         = "invite_accepted"          // REQ-AUD-029, GD-22
	PasswordChanged        = "password_changed"         // REQ-AUD-029, GD-23
	PasswordResetRequested = "password_reset_requested" // REQ-AUD-029, GD-23
	PasswordResetCompleted = "password_reset_completed" // REQ-AUD-029, GD-23

	// §3.5 export events
	Export      = "export"
	AuditExport = "audit_export" // REQ-AUD-031: a read of the trail itself, not of record data
	ODMExport   = "odm_export"   // REQ-ODM-061: an ODM XML project file out (REQ-API-169)
	ODMImport   = "odm_import"   // REQ-ODM-061: an ODM XML project file in (REQ-API-170)

	// §3.6 survey events
	SurveySubmitted   = "survey_submitted"
	SurveyLinkIssued  = "survey_link_issued"
	SurveyLinkRevoked = "survey_link_revoked"

	// §3.7 data access group events
	DagCreated           = "dag_created"
	DagDeleted           = "dag_deleted"
	DagMembershipChanged = "dag_membership_changed"
	DagActiveSwitched    = "dag_active_switched"
	DagRecordAssigned    = "dag_record_assigned"

	// §3.8 project mode and staging events
	ProjectModeChanged = "project_mode_changed"
	StagingStarted     = "staging_started"
	StagingCommitted   = "staging_committed"
	StagingDiscarded   = "staging_discarded"

	// §3.9 record read events — a browser open of a record, never a value
	// (REQ-AUD-035). A pull through the data API is an audit_record_views row
	// instead, which its token keys (§4, REQ-AUD-013, DEV-AUD-6).
	RecordRead = "record_read"
)

// Source values for the fixed column (REQ-AUD-017).
const (
	SourceAPI    = "api"
	SourceUI     = "ui"
	SourceSystem = "system"
)

// Entry is one audit row. Zero values mean "unset" for the optional fixed
// columns; Details is marshaled as JSON (nil → NULL).
type Entry struct {
	EventType    string
	Source       string // SourceAPI | SourceUI | SourceSystem
	UserID       int64  // 0 = unset
	Email        string
	Token        string // data-API and survey-link calls only (REQ-AUD-018)
	ProjectID    int64  // 0 = unset
	ArmNum       int    // 0 = unset (arm numbers are 1-based)
	Role         string
	TargetRecord string // record-scoped events only (§6.4)
	Details      any    // JSON payload per the §3 catalog
}

// Writer writes audit entries for one store. The zero year latch forces a
// rollover check on first use.
type Writer struct {
	DB      *sql.DB
	Dialect string // "sqlite" | "mariadb"

	// Now is the clock entries are stamped with; nil means time.Now. Tests
	// roll it past New Year to exercise the rollover (§6) rather than wait
	// for it; nothing outside this package sets it.
	Now func() time.Time

	mu sync.Mutex
	// latched is the year covered by the last completed rollover, read
	// atomically so the per-request check on the hot path costs a load
	// (§6.1's "in-process year latch"); 0 = never run. Stored separately from
	// mu because EnsureYear must not serialise readers behind a slow ALTER.
	latched atomic.Int64
	ensured map[string]bool // physical table names already known to exist
}

// NewWriter returns a writer for the store's dialect.
func NewWriter(db *sql.DB, dialect string) *Writer {
	return &Writer{DB: db, Dialect: dialect, ensured: map[string]bool{}}
}

// now is the single clock of this package: an entry always lands in the year
// its own created_at reads back from (§6.2), so routing and stamping must not
// read two different times.
func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

// covers reports whether the rollover for a year has completed in this process.
func (w *Writer) covers(year int) bool { return int(w.latched.Load()) == year }

const datetimeLayout = "2006-01-02 15:04:05"

// InsertTx writes the entry inside the caller's transaction (REQ-AUD-003).
//
// It performs no rollover work on MariaDB, on purpose: that rollover is
// `ALTER TABLE … ADD PARTITION`, and DDL commits implicitly — issued here it
// would end the caller's transaction and break the very guarantee this method
// exists to give (REQ-AUD-003). Provisioning a year therefore happens before any
// transaction opens: at startup, and on the request path in
// httpapi.rolloverGuard (§6.1). SQLite is exempt from that rule because its DDL
// is transactional — creating the year object inside the caller's transaction is
// what §6.2 prescribes, and a rollback takes it back with the operation.
func (w *Writer) InsertTx(ctx context.Context, tx *sql.Tx, e Entry) error {
	now := w.now()
	table, args, err := w.renderAt(e, now)
	if err != nil {
		return err
	}
	if err := w.writeRow(ctx, tx, "audit_events", table, now.Year(), insertSQL(table), args); err != nil {
		return w.writeError(err, now.Year())
	}
	return nil
}

// writeRow inserts one row into a year object, creating the object first on
// SQLite (§6.2). ex is the caller's transaction when there is one, so the DDL
// and the row commit or roll back together.
func (w *Writer) writeRow(ctx context.Context, ex execer, base, table string, year int, stmt string, args []any) error {
	if w.Dialect != "sqlite" {
		_, err := ex.ExecContext(ctx, stmt, args...)
		return err
	}
	if err := w.ensureYearObject(ctx, ex, base, year); err != nil {
		return err
	}
	_, err := ex.ExecContext(ctx, stmt, args...)
	if err == nil || !isMissingObject(err) {
		return err
	}
	// The cached belief that this year's objects exist has gone stale — an
	// operation rolled back after it created them took the table and the view
	// back, and forget is the only way to notice. Re-run the idempotent DDL
	// once rather than fail every write of this year until the process restarts.
	w.forget(table)
	if err := w.ensureYearObject(ctx, ex, base, year); err != nil {
		return err
	}
	_, err = ex.ExecContext(ctx, stmt, args...)
	return err
}

// Insert writes a standalone entry (no surrounding operation). Used for
// security events such as login_failure and admin_rejected.
func (w *Writer) Insert(ctx context.Context, e Entry) error {
	now := w.now()
	table, args, err := w.renderAt(e, now)
	if err != nil {
		return err
	}
	// No transaction is open on this path, so the rollover check may run its
	// DDL here — which is what provisions a new year for every caller that is
	// not an HTTP request (§6.1's "first audit write after a calendar-year
	// change"). The HTTP path has already done it in the guard; for a latch hit
	// this test is an atomic load.
	if !w.covers(now.Year()) {
		if err := w.EnsureYear(ctx); err != nil {
			return err
		}
	}
	if err := w.writeRow(ctx, w.DB, "audit_events", table, now.Year(), insertSQL(table), args); err != nil {
		return w.writeError(err, now.Year())
	}
	return nil
}

// writeError annotates an INSERT that failed with the one cause a reader of the
// log cannot infer from the driver's message: on MariaDB an entry whose year has
// no partition is rejected (ER_NO_PARTITION_FOR_GIVEN_VALUE — there is no
// MAXVALUE guard, deliberately, §6.1), and the reason is that this process never
// ran the rollover for that year. Only claimed when the latch says so, so an
// unrelated failure keeps its own error.
func (w *Writer) writeError(err error, year int) error {
	if w.Dialect == "mariadb" && !w.covers(year) {
		return fmt.Errorf(
			"audit: entry for %d rejected and no rollover has run for it in this process — "+
				"the year's partition is missing (Audit_Logging_Design.md §6.1): %w", year, err)
	}
	return err
}

func (w *Writer) renderAt(e Entry, now time.Time) (table string, args []any, err error) {
	year := now.Year()
	if w.Dialect == "sqlite" {
		table = physical("audit_events", year)
	} else {
		table = "audit_events"
	}
	details := any(nil)
	if e.Details != nil {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return "", nil, fmt.Errorf("audit details: %w", err)
		}
		details = string(b)
	}
	return table, []any{
		e.EventType, e.Source,
		nullInt64(e.UserID), nullString(e.Email), nullString(e.Token),
		nullInt64(e.ProjectID), nullInt(e.ArmNum), nullString(e.Role),
		nullString(e.TargetRecord), details, now.Format(datetimeLayout),
	}, nil
}

func insertSQL(table string) string {
	return `INSERT INTO ` + table + ` (event_type, source, user_id, email, token,
		project_id, arm_num, role, target_record, details, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`
}

// --- record views (§4) ---

// RecordView is one audit_record_views row: who pulled which records through
// content=record&action=export. It carries no values (REQ-AUD-014).
type RecordView struct {
	UserID      int64 // 0 = unset (survey-link pulls have no account)
	Email       string
	Token       string // NOT NULL — the only actor a record pull has
	ProjectID   int64
	RecordIDs   []string // returned records, after DAG filtering
	Instruments []string // instruments present in the returned rows
}

// InsertViewTx writes a record-view row inside the caller's transaction.
func (w *Writer) InsertViewTx(ctx context.Context, tx *sql.Tx, v RecordView) error {
	now := w.now()
	year := now.Year()
	table := "audit_record_views"
	if w.Dialect == "sqlite" {
		table = physical("audit_record_views", year)
	}
	rids, err := json.Marshal(v.RecordIDs)
	if err != nil {
		return fmt.Errorf("record view ids: %w", err)
	}
	instrs := any(nil)
	if v.Instruments != nil {
		b, err := json.Marshal(v.Instruments)
		if err != nil {
			return fmt.Errorf("record view instruments: %w", err)
		}
		instrs = string(b)
	}
	stmt := `INSERT INTO ` + table + ` (user_id, email, token, project_id, record_ids, instruments, created_at)
		 VALUES (?,?,?,?,?,?,?)`
	if err := w.writeRow(ctx, tx, "audit_record_views", table, year, stmt, []any{
		nullInt64(v.UserID), nullString(v.Email), v.Token, v.ProjectID,
		string(rids), instrs, now.Format(datetimeLayout)}); err != nil {
		return w.writeError(err, year)
	}
	return nil
}

// --- year objects (§6) ---

func physical(base string, year int) string { return base + "_" + strconv.Itoa(year) }

// EnsureYear runs the rollover check when the process has not yet covered the
// current UTC year (idempotent; cheap on the hot path — an in-memory latch, no
// scheduler). §6.1 fixes the two triggers: process startup, and before the first
// audit write of a new calendar year. "Before" is load-bearing on MariaDB, where
// the check is DDL and so implicitly commits whatever transaction it lands in:
// the request guard (httpapi.rolloverGuard) calls this ahead of any handler
// transaction, and Writer.InsertTx never calls it for exactly that reason.
func (w *Writer) EnsureYear(ctx context.Context) error {
	year := w.now().Year()
	if w.covers(year) {
		return nil // hot path: one atomic load per request
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.covers(year) {
		return nil // another caller finished this rollover first
	}
	switch w.Dialect {
	case "sqlite":
		if err := w.rolloverSQLite(ctx, year); err != nil {
			return err
		}
	case "mariadb":
		if err := w.rolloverMariaDB(ctx, year); err != nil {
			return err
		}
	default:
		return fmt.Errorf("audit: unknown dialect %q", w.Dialect)
	}
	w.latched.Store(int64(year))
	return nil
}

const (
	eventsCols = `id INTEGER PRIMARY KEY,
		event_type TEXT NOT NULL, source TEXT NOT NULL, user_id INTEGER, email TEXT,
		token TEXT, project_id INTEGER, arm_num INTEGER, role TEXT,
		target_record TEXT, details TEXT, created_at TEXT NOT NULL`
	viewsCols = `id INTEGER PRIMARY KEY, user_id INTEGER, email TEXT, token TEXT NOT NULL,
		project_id INTEGER NOT NULL, record_ids TEXT NOT NULL, instruments TEXT, created_at TEXT NOT NULL`
)

// ensureYearObject creates a missing SQLite year object and re-points the
// stable-name view at all year tables — the two halves of §6.2's check, which
// reads "create audit_events_<year>, then DROP VIEW audit_events and recreate it
// as UNION ALL over all audit_events_* tables". The second half is not optional:
// reads only ever name the stable table, so a year created without the rebuild
// would take writes that no read could see.
//
// It runs on ex — the caller's transaction when there is one — because SQLite DDL
// is transactional: an operation that rolls back leaves no half-built year
// behind, and no other connection sees the new view before the commit does.
func (w *Writer) ensureYearObject(ctx context.Context, ex execer, base string, year int) error {
	table := physical(base, year)
	w.mu.Lock()
	known := w.ensured[table]
	w.mu.Unlock()
	if known {
		return nil
	}
	if err := w.retirePlainTable(ctx, ex, base, year); err != nil {
		return err
	}
	if err := w.createObjectsOfKind(ctx, ex, base, table); err != nil {
		return err
	}
	if err := w.rebuildView(ctx, ex, base); err != nil {
		return err
	}
	w.mu.Lock()
	w.ensured[table] = true
	w.mu.Unlock()
	return nil
}

// retirePlainTable moves the migration-created unpartitioned table out of the
// stable name's way (§6.2): it becomes this year's physical table, so its rows
// keep their created_at and stay readable through the view that replaces the
// name. Renaming is metadata-only and transactional in SQLite, which is what
// makes it safe to do here — inside a caller's transaction, an interrupted
// rollover leaves nothing half-moved. When the year object already exists, the
// plain table can only be the empty residue of an interrupted rollover, so it is
// dropped rather than merged (its rows would have had nowhere to come from).
func (w *Writer) retirePlainTable(ctx context.Context, ex execer, base string, year int) error {
	var kind string
	err := ex.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name = ?`, base).Scan(&kind)
	if err == sql.ErrNoRows {
		return nil // already a view, or nothing created yet
	}
	if err != nil {
		return err
	}
	if kind != "table" {
		return nil
	}
	target := physical(base, year)
	var exists int
	if err := ex.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, target).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		_, err := ex.ExecContext(ctx, `DROP TABLE `+base)
		return err
	}
	_, err = ex.ExecContext(ctx, `ALTER TABLE `+base+` RENAME TO `+target)
	return err
}

// forget drops the cached belief that a physical year object exists, so the next
// write re-runs its (idempotent) creation. Only an insert that failed because the
// object is gone can justify that: w.ensured cannot see a rollback, because it is
// written before the surrounding transaction decides.
func (w *Writer) forget(table string) {
	w.mu.Lock()
	delete(w.ensured, table)
	w.mu.Unlock()
}

// isMissingObject reports the SQLite error for an absent table or view — the
// signature of a stale w.ensured entry.
func isMissingObject(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such view")
}

// createObjectsOfKind creates one year object of the kind the base name names.
func (w *Writer) createObjectsOfKind(ctx context.Context, ex execer, base, table string) error {
	if base == "audit_events" {
		return w.createEventsObjects(ctx, ex, table)
	}
	return w.createViewObjects(ctx, ex, table)
}

// createEventsObjects and createViewObjects run the year-object DDL without
// touching w.mu (the mutex is not reentrant; EnsureYear holds it across the
// rollover). All statements are IF NOT EXISTS, so concurrent creation is a
// no-op for the loser — including two API processes rolling over at the same
// second on New Year's Eve.

func (w *Writer) createEventsObjects(ctx context.Context, ex execer, table string) error {
	if _, err := ex.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (`+eventsCols+`)`); err != nil {
		return err
	}
	for _, idx := range []struct{ name, cols string }{
		{"project", "project_id, created_at"},
		{"user", "user_id, created_at"},
		{"user_project", "user_id, project_id, created_at"}, // the overview's per-project recency (REQ-UI-071)
		{"type", "project_id, event_type, created_at"},
		{"record", "project_id, target_record, created_at"},
	} {
		if _, err := ex.ExecContext(ctx,
			`CREATE INDEX IF NOT EXISTS idx_`+table+`_`+idx.name+` ON `+table+` (`+idx.cols+`)`); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) createViewObjects(ctx context.Context, ex execer, table string) error {
	if _, err := ex.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (`+viewsCols+`)`); err != nil {
		return err
	}
	if _, err := ex.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_`+table+`_project ON `+table+` (project_id, created_at)`); err != nil {
		return err
	}
	return nil
}

// execer is what the writer needs from a statement runner: *sql.DB and *sql.Tx
// both satisfy it, so every helper here works inside or outside a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rolloverSQLite migrates the plain migration-created tables to per-year
// tables behind a UNION ALL view (§6.2), and adds the current year's objects.
// EnsureYear calls this with w.mu held — the mutex is not reentrant, so nothing
// below may take it; that also means the year objects are left out of w.ensured,
// and the first write of the year re-runs the idempotent DDL once. Cheap, and
// cheaper still than a lock discipline that breaks at 00:00.
func (w *Writer) rolloverSQLite(ctx context.Context, year int) error {
	for _, base := range []string{"audit_events", "audit_record_views"} {
		if err := w.retirePlainTable(ctx, w.DB, base, year); err != nil {
			return err
		}
		if err := w.createObjectsOfKind(ctx, w.DB, base, physical(base, year)); err != nil {
			return err
		}
		if err := w.rebuildView(ctx, w.DB, base); err != nil {
			return err
		}
	}
	return nil
}

// rebuildView recreates the stable-name view as UNION ALL over all existing
// per-year tables, in year order. It runs on ex — inside the caller's
// transaction when a write triggered it, so readers switch from the old view to
// the new one at exactly that commit (§6.2).
func (w *Writer) rebuildView(ctx context.Context, ex execer, base string) error {
	rows, err := ex.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE ? ORDER BY name`,
		base+`_%`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := ex.ExecContext(ctx, `DROP VIEW IF EXISTS `+base); err != nil {
		return err
	}
	var union strings.Builder
	for i, t := range tables {
		if i > 0 {
			union.WriteString(" UNION ALL ")
		}
		union.WriteString(`SELECT * FROM ` + t)
	}
	_, err = ex.ExecContext(ctx, `CREATE VIEW `+base+` AS `+union.String())
	return err
}

// rolloverMariaDB converts the audit tables to yearly RANGE partitions when
// they are not yet partitioned, then ensures the current and next year's
// partitions exist (§6.1; no MAXVALUE guard).
func (w *Writer) rolloverMariaDB(ctx context.Context, year int) error {
	for _, base := range []string{"audit_events", "audit_record_views"} {
		var partCount int
		if err := w.DB.QueryRowContext(ctx,
			`SELECT count(*) FROM information_schema.PARTITIONS
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND PARTITION_NAME IS NOT NULL`,
			base).Scan(&partCount); err != nil {
			return err
		}
		if partCount == 0 {
			if err := w.partitionMariaDB(ctx, base, year); err != nil {
				return err
			}
			continue
		}
		// Partitioned: add current/next year when missing.
		for _, y := range []int{year, year + 1} {
			have, err := w.hasPartition(ctx, base, y)
			if err != nil {
				return err
			}
			if !have {
				if err := w.addPartition(ctx, base, y); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// hasPartition reports whether a yearly partition exists (§6.1).
func (w *Writer) hasPartition(ctx context.Context, base string, year int) (bool, error) {
	var n int
	if err := w.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.PARTITIONS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND PARTITION_NAME = ?`,
		base, partitionName(year)).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// addPartition adds one yearly partition. A failure is forgiven when the
// partition exists by then: several API processes all serving their first
// request of the new year run this ALTER concurrently, and one of them losing
// the race has the outcome it wanted anyway.
func (w *Writer) addPartition(ctx context.Context, base string, year int) error {
	_, err := w.DB.ExecContext(ctx,
		`ALTER TABLE `+base+` ADD PARTITION (PARTITION `+partitionName(year)+
			` VALUES LESS THAN (`+strconv.Itoa(year+1)+`))`)
	if err == nil {
		return nil
	}
	if have, herr := w.hasPartition(ctx, base, year); herr == nil && have {
		return nil
	}
	return err
}

// partitionMariaDB re-clusters the table into yearly partitions covering all
// stored years plus current and next. The audit tables carry no foreign keys
// (a partitioned InnoDB table cannot hold them), matching §6.1.
func (w *Writer) partitionMariaDB(ctx context.Context, base string, year int) error {
	years := map[int]bool{year: true, year + 1: true}
	rows, err := w.DB.QueryContext(ctx,
		`SELECT DISTINCT YEAR(created_at) FROM `+base)
	if err != nil {
		return err
	}
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			rows.Close()
			return err
		}
		years[y] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	list := sortedKeys(years)
	var parts strings.Builder
	for i, y := range list {
		if i > 0 {
			parts.WriteString(", ")
		}
		parts.WriteString(`PARTITION ` + partitionName(y) + ` VALUES LESS THAN (` + strconv.Itoa(y+1) + `)`)
	}
	// Drop the audit user FK first (partitioned tables cannot carry FKs); it
	// is absent on fresh installs created after migration 0004.
	for _, fk := range []string{"fk_audit_user", "fk_views_user"} {
		w.DB.ExecContext(ctx, `ALTER TABLE `+base+` DROP FOREIGN KEY `+fk) // ignore errors when absent
	}
	_, err = w.DB.ExecContext(ctx,
		`ALTER TABLE `+base+` PARTITION BY RANGE (YEAR(created_at)) (`+parts.String()+`)`)
	return err
}

func partitionName(y int) string { return "p" + strconv.Itoa(y) }

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// --- null helpers (0 / "" mean unset) ---

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
