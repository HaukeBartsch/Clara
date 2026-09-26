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
	"time"
)

// Event catalog (§3, normative). Adding a code is non-breaking; changing or
// removing one is breaking.
const (
	// §3.1 authentication and administration boundary
	LoginSuccess         = "login_success"
	LoginFailure         = "login_failure"
	Logout               = "logout"
	AdminRejected        = "admin_rejected"
	AccountAutoDisabled  = "account_auto_disabled"

	// §3.2 data change events
	RecordCreated          = "record_created"
	RecordUpdated          = "record_updated"
	RecordDeleted          = "record_deleted"
	CalculatedRecomputed   = "calculated_recomputed"

	// §3.3 project structure events
	ProjectCreated      = "project_created"
	ProjectUpdated      = "project_updated"
	ArmCreated          = "arm_created"
	ArmDeleted          = "arm_deleted"
	EventCreated        = "event_created"
	EventUpdated        = "event_updated"
	EventReordered      = "event_reordered"
	InstrumentCreated   = "instrument_created"
	InstrumentUpdated   = "instrument_updated"
	InstrumentReordered = "instrument_reordered"
	FieldCreated        = "field_created"
	FieldUpdated        = "field_updated"
	FieldDeleted        = "field_deleted"
	FieldReordered      = "field_reordered"
	MappingUpdated      = "mapping_updated"
	ProjectEnded        = "project_ended"

	// §3.4 administration events
	UserCreated         = "user_created"
	UserUpdated         = "user_updated"
	MembershipChanged   = "membership_changed"
	TokenIssued         = "token_issued"
	TokenRotated        = "token_rotated"
	TokenRevoked        = "token_revoked"
	RoleCreated         = "role_created"
	I18nUpdated         = "i18n_updated"

	// §3.5 export events
	Export = "export"

	// §3.6 survey events
	SurveySubmitted    = "survey_submitted"
	SurveyLinkIssued   = "survey_link_issued"
	SurveyLinkRevoked  = "survey_link_revoked"

	// §3.7 data access group events
	DagCreated             = "dag_created"
	DagDeleted             = "dag_deleted"
	DagMembershipChanged   = "dag_membership_changed"
	DagActiveSwitched      = "dag_active_switched"
	DagRecordAssigned      = "dag_record_assigned"

	// §3.8 project mode and staging events
	ProjectModeChanged  = "project_mode_changed"
	StagingStarted      = "staging_started"
	StagingCommitted    = "staging_committed"
	StagingDiscarded    = "staging_discarded"
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

	mu       sync.Mutex
	latched  int              // year covered by the last rollover
	ensured  map[string]bool  // physical table names already known to exist
}

// NewWriter returns a writer for the store's dialect.
func NewWriter(db *sql.DB, dialect string) *Writer {
	return &Writer{DB: db, Dialect: dialect, ensured: map[string]bool{}}
}

const datetimeLayout = "2006-01-02 15:04:05"

// InsertTx writes the entry inside the caller's transaction (REQ-AUD-003).
func (w *Writer) InsertTx(ctx context.Context, tx *sql.Tx, e Entry) error {
	table, args, err := w.render(e)
	if err != nil {
		return err
	}
	if w.Dialect == "sqlite" {
		if err := w.ensureTableTx(ctx, tx, table); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, insertSQL(table), args...)
	return err
}

// Insert writes a standalone entry (no surrounding operation). Used for
// security events such as login_failure and admin_rejected.
func (w *Writer) Insert(ctx context.Context, e Entry) error {
	table, args, err := w.render(e)
	if err != nil {
		return err
	}
	if w.Dialect == "sqlite" {
		if err := w.ensureTableTx(ctx, w.DB, table); err != nil {
			return err
		}
	}
	_, err = w.DB.ExecContext(ctx, insertSQL(table), args...)
	return err
}

func (w *Writer) render(e Entry) (table string, args []any, err error) {
	now := time.Now().UTC()
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
	now := time.Now().UTC()
	table := "audit_record_views"
	if w.Dialect == "sqlite" {
		table = physical("audit_record_views", now.Year())
		if err := w.ensureViewTableTx(ctx, tx, table); err != nil {
			return err
		}
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
	_, err = tx.ExecContext(ctx,
		`INSERT INTO `+table+` (user_id, email, token, project_id, record_ids, instruments, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		nullInt64(v.UserID), nullString(v.Email), v.Token, v.ProjectID,
		string(rids), instrs, now.Format(datetimeLayout))
	return err
}

// --- year objects (§6) ---

func physical(base string, year int) string { return base + "_" + strconv.Itoa(year) }

// EnsureYear runs the rollover check when the process has not yet covered the
// current UTC year (idempotent; cheap on the hot path — an in-memory latch,
// no scheduler). Call it at process startup and before any audit write batch;
// never inside another operation's transaction on MariaDB (DDL commits).
func (w *Writer) EnsureYear(ctx context.Context) error {
	year := time.Now().UTC().Year()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.latched == year {
		return nil
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
	w.latched = year
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

// ensureTableTx creates a missing SQLite year object (transactional DDL).
func (w *Writer) ensureTableTx(ctx context.Context, ex execer, table string) error {
	w.mu.Lock()
	known := w.ensured[table]
	w.mu.Unlock()
	if known {
		return nil
	}
	if _, err := ex.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (`+eventsCols+`)`); err != nil {
		return err
	}
	for _, idx := range []struct{ name, cols string }{
		{"project", "project_id, created_at"},
		{"user", "user_id, created_at"},
		{"type", "project_id, event_type, created_at"},
		{"record", "project_id, target_record, created_at"},
	} {
		if _, err := ex.ExecContext(ctx,
			`CREATE INDEX IF NOT EXISTS idx_`+table+`_`+idx.name+` ON `+table+` (`+idx.cols+`)`); err != nil {
			return err
		}
	}
	w.mu.Lock()
	w.ensured[table] = true
	w.mu.Unlock()
	return nil
}

func (w *Writer) ensureViewTableTx(ctx context.Context, ex execer, table string) error {
	w.mu.Lock()
	known := w.ensured[table]
	w.mu.Unlock()
	if known {
		return nil
	}
	if _, err := ex.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (`+viewsCols+`)`); err != nil {
		return err
	}
	if _, err := ex.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_`+table+`_project ON `+table+` (project_id, created_at)`); err != nil {
		return err
	}
	w.mu.Lock()
	w.ensured[table] = true
	w.mu.Unlock()
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// rolloverSQLite migrates the plain migration-created tables to per-year
// tables behind a UNION ALL view (§6.2), and adds the current year's objects.
func (w *Writer) rolloverSQLite(ctx context.Context, year int) error {
	for _, base := range []string{"audit_events", "audit_record_views"} {
		var plain int
		if err := w.DB.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, base).Scan(&plain); err != nil {
			return err
		}
		if plain > 0 {
			target := physical(base, year)
			var targetExists int
			if err := w.DB.QueryRowContext(ctx,
				`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, target).Scan(&targetExists); err != nil {
				return err
			}
			if targetExists == 0 {
				if _, err := w.DB.ExecContext(ctx,
					`ALTER TABLE `+base+` RENAME TO `+target); err != nil {
					return err
				}
			} else {
				// The plain table is empty residue; drop it.
				if _, err := w.DB.ExecContext(ctx, `DROP TABLE `+base); err != nil {
					return err
				}
			}
		}
		if err := w.ensureTableTxByKind(ctx, base, year); err != nil {
			return err
		}
		if err := w.rebuildView(ctx, base); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) ensureTableTxByKind(ctx context.Context, base string, year int) error {
	table := physical(base, year)
	if base == "audit_events" {
		return w.ensureTableTx(ctx, w.DB, table)
	}
	return w.ensureViewTableTx(ctx, w.DB, table)
}

// rebuildView recreates the stable-name view as UNION ALL over all existing
// per-year tables, in year order.
func (w *Writer) rebuildView(ctx context.Context, base string) error {
	rows, err := w.DB.QueryContext(ctx,
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
	if _, err := w.DB.ExecContext(ctx, `DROP VIEW IF EXISTS `+base); err != nil {
		return err
	}
	var union strings.Builder
	for i, t := range tables {
		if i > 0 {
			union.WriteString(" UNION ALL ")
		}
		union.WriteString(`SELECT * FROM ` + t)
	}
	_, err = w.DB.ExecContext(ctx, `CREATE VIEW `+base+` AS `+union.String())
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
			var have int
			if err := w.DB.QueryRowContext(ctx,
				`SELECT count(*) FROM information_schema.PARTITIONS
				 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND PARTITION_NAME = ?`,
				base, partitionName(y)).Scan(&have); err != nil {
				return err
			}
			if have == 0 {
				if _, err := w.DB.ExecContext(ctx,
					`ALTER TABLE `+base+` ADD PARTITION (PARTITION `+partitionName(y)+
						` VALUES LESS THAN (`+strconv.Itoa(y+1)+`))`); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
