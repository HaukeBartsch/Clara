package dataapi

// The change window of the record export (REQ-API-181, API_Endpoints_Design.md
// §3.6.1): REDCap's dateRangeBegin / dateRangeEnd keep the records with a data
// change in [begin, end), read from the audit trail.

import (
	"time"

	"csms/api/internal/audit"
)

// recordChangeEvents are the audit events that change a record's values or
// completion states — the record history's data-change codes (REQ-API-079)
// plus the completion events (REQ-AUD-026), which REDCap logs as data too, since
// its form status is a field. Every one is written in the transaction of the
// change it records (GD-3), with target_record set.
var recordChangeEvents = []string{
	audit.RecordCreated, audit.RecordUpdated, audit.RecordDeleted, audit.CalculatedRecomputed,
	audit.InstrumentCompleted, audit.InstrumentUnverified, audit.InstrumentUncompleted,
}

// changeWindowLayouts are the accepted spellings of a bound: REDCap's
// `YYYY-MM-DD HH:MM:SS`, and the same without seconds or without a time, the
// missing parts zero.
var changeWindowLayouts = []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}

// auditTimeLayout is the form every audit timestamp is stored in (UTC, GD-7).
const auditTimeLayout = "2006-01-02 15:04:05"

// appLocation is the deployment timezone APP_TIMEZONE (REQ-CFG-026), resolved
// the way collectionOffsetFor resolves it for imported dates; UTC when it does
// not resolve (configuration loading refuses such a value already).
func (h *Handler) appLocation() *time.Location {
	if loc, err := time.LoadLocation(h.Cfg.AppTimezone); err == nil {
		return loc
	}
	return time.UTC
}

// changeWindowBound reads one bound of the change window in loc — the
// deployment timezone, standing in for the REDCap server's local time — and
// returns it as a UTC audit timestamp. An empty value is no bound ("", true); a
// value in no accepted spelling, or naming no calendar date, is ("", false).
func changeWindowBound(v string, loc *time.Location) (string, bool) {
	if v == "" {
		return "", true
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range changeWindowLayouts {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t.UTC().Format(auditTimeLayout), true
		}
	}
	return "", false
}
