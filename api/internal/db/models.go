// Package db is the single writer to the store: dialect-aware connection,
// migrations, and the repositories for every table. No other component
// opens a database connection (charter §2.2, REQ-TECH-006).
//
// Temporal portability (GD-6, REQ-DB-002): DATE and DATETIME columns are
// declared per the logical type map (SQLite TEXT, MariaDB DATE/DATETIME) but
// are always scanned through the timeString/dateString normalizers below so
// the same repository code runs against both drivers.
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Projects (REQ-DB-006, GD-17 simplified).
type Project struct {
	ID               int64
	ProjectName      string
	Organization     string
	PIName           string
	PIEmail          string
	DMName           sql.NullString
	DMEmail          sql.NullString
	RekNumber        sql.NullString
	RekStartDate     sql.NullString // DATE "YYYY-MM-DD"
	RekEndDate       sql.NullString
	StartDate        sql.NullString
	EndDate          sql.NullString
	ParticipantNames string
	CreationTime     string // DATETIME UTC "YYYY-MM-DD HH:MM:SS"
}

// Users (REQ-DB-008, GD-18/GD-19).
type User struct {
	ID           int64
	Email        string
	DisplayName  string
	Enabled      bool
	AuthSource   string // oauth2 | ldap | local
	PasswordHash sql.NullString
	ValidUntil   sql.NullString // DATE; NULL = indefinite
	LastLoginAt  sql.NullString // DATETIME UTC
	IsAdmin      bool
	UILanguage   string
	CreatedAt    string
}

// Role (REQ-DB-009, GD-2).
type Role struct {
	ID           int64
	ProjectID    int64
	RoleName     string
	ProjectAdmin bool
}

// RoleArm is one per-arm level of a role (REQ-DB-009).
type RoleArm struct {
	ID              int64
	RoleID          int64
	ArmNum          int
	DataAccessLevel string // no_access | read_only | view_edit | delete | edit_survey_responses
	ExportLevel     string // export_none | export_de_identified | export_no_identifiers | export_full
}

// Assignment links a user to a project with a role and a token (REQ-DB-010, GD-5).
type Assignment struct {
	ID        int64
	UserID    int64
	ProjectID int64
	RoleID    sql.NullInt64 // NULL = full permissions
	Token     string
	CreatedAt string
}

// Arm (REQ-DB-011).
type Arm struct {
	ID        int64
	ProjectID int64
	ArmNum    int
	Name      sql.NullString
	Position  int
}

// Event (REQ-DB-011, GD-15).
type Event struct {
	ID              int64
	ProjectID       int64
	ArmID           int64
	EventName       string
	UniqueEventName string
	Period          sql.NullInt64 // timepoint days; NULL = no timepoint
	SafeRegionStart sql.NullInt64
	SafeRegionEnd   sql.NullInt64
	Position        int
}

// Instrument (REQ-DB-011, GD-9/GD-13).
type Instrument struct {
	ID             int64
	ProjectID      int64
	Name           string
	Position       int
	IsSurvey       bool
	BranchingLogic sql.NullString
}

// Field (REQ-DB-013/014).
type Field struct {
	ID                  int64
	ProjectID           int64
	InstrumentID        int64
	FieldName           string
	FieldLabel          sql.NullString
	FieldType           string // text | dropdown | radio | matrix | description | header | calculated
	SectionHeader       sql.NullString
	Choices             sql.NullString
	FieldNote           sql.NullString
	ValidationType      sql.NullString
	ValidationFormat    sql.NullString
	ValidationMin       sql.NullString
	ValidationMax       sql.NullString
	Required            bool
	BranchingLogic      sql.NullString
	Calculation         sql.NullString
	MatrixGroup         sql.NullString
	PersonalInformation bool
	ExportApproved      bool
	Position            int
}

// DataValue is one EAV row (REQ-DB-015…020).
type DataValue struct {
	ProjectID               int64
	RecordID                string
	UniqueEventName         string
	RepeatingInstrument     string
	RepeatingInstanceNumber int
	FieldName               string
	Value                   string
}

// RecordEntity tracks a record's identity and DAG assignment (REQ-DB-029, GD-10).
type RecordEntity struct {
	ProjectID  int64
	RecordID   string
	DagGroupID sql.NullInt64
	CreatedBy  sql.NullInt64
	CreatedAt  string
}

// AnonOffset persists a record's deterministic date-shift (REQ-DB-023).
type AnonOffset struct {
	ProjectID  int64
	RecordID   string
	OffsetDays int
}

// SurveyLink is a stable public fill token (REQ-DB-027, GD-9).
type SurveyLink struct {
	ID           int64
	ProjectID    int64
	RecordID     string
	InstrumentID int64
	Token        string
	Revoked      bool
	CreatedBy    sql.NullInt64
	CreatedAt    string
}

// DagGroup (REQ-DB-028, GD-10).
type DagGroup struct {
	ID        int64
	ProjectID int64
	Name      string
	CreatedAt string
}

// DagMembership links a project membership to a data access group
// (REQ-DB-028, REQ-AUTH-044); exactly one is active per assignment when any
// exist.
type DagMembership struct {
	ID           int64
	AssignmentID int64
	GroupID      int64
	IsActive     bool
}

// CalculatedDependency links a calculated field to the (event, field) pair it
// reads, maintained by the API when the expression changes (GD-11, REQ-VAL-037).
type CalculatedDependency struct {
	ProjectID          int64
	CalculatedFieldID  int64
	RefUniqueEventName string
	RefFieldName       string
}

// Language and i18n string (REQ-DB-031, GD-12).
type Language struct {
	ID          int64
	Code        string
	DisplayName string
	Enabled     bool
}

type I18nString struct {
	ID         int64
	LanguageID int64
	Key        string
	Text       string
}

// AuditEvent is one append-only log entry (REQ-DB-021/024). `details` carries
// the per-event JSON payload from the audit catalog; the fixed columns carry
// the fields common to every entry.
type AuditEvent struct {
	ID           int64
	EventType    string // stable snake_case code (audit design §3)
	Source       string // api | ui | system
	UserID       sql.NullInt64
	Email        sql.NullString
	Token        sql.NullString // data-API and survey-link calls only (REQ-AUD-018)
	ProjectID    sql.NullInt64
	ArmNum       sql.NullInt64
	Role         sql.NullString
	TargetRecord sql.NullString // record-scoped events only
	Details      sql.NullString // JSON, per-event shape
	CreatedAt    string         // DATETIME UTC
}

// AuditRecordView is one API record-pull entry (REQ-DB-021).
type AuditRecordView struct {
	ID          int64
	UserID      sql.NullInt64
	Email       sql.NullString
	Token       string
	ProjectID   int64
	RecordIDs   string // JSON array of record ids
	Instruments sql.NullString // JSON array
	CreatedAt   string
}

// --- temporal scan helpers (portable across sqlite and mariadb drivers) ---

const (
	dateLayout     = "2006-01-02"
	datetimeLayout = "2006-01-02 15:04:05"
)

// dateString normalizes a scanned DATE value (time.Time, string, or []byte)
// to "YYYY-MM-DD". ok is false for NULL/empty.
func dateString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case time.Time:
		return t.UTC().Format(dateLayout), true
	case string:
		if t == "" {
			return "", false
		}
		return t, true
	case []byte:
		if len(t) == 0 {
			return "", false
		}
		return string(t), true
	default:
		return fmt.Sprintf("%v", t), true
	}
}

// datetimeString normalizes a scanned DATETIME value to UTC "YYYY-MM-DD HH:MM:SS".
func datetimeString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case time.Time:
		return t.UTC().Format(datetimeLayout), true
	case string:
		if t == "" {
			return "", false
		}
		return t, true
	case []byte:
		if len(t) == 0 {
			return "", false
		}
		return string(t), true
	default:
		return fmt.Sprintf("%v", t), true
	}
}
