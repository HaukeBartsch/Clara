# Audit Logging Plan

This document outlines the strategy for tracking user activity and data changes within the clinical study management system.

## Logged Events
- Authentication: Login attempts (success/failure) and logouts; each login event names the authentication source and the user-selected source name (master spec "Authentication order", REQ-AUTH-067) — a parallel credential race under one name yields a single failure event with per-source outcomes in its details (REQ-AUD-008). Two-factor enrollment, self-service disable, administrator reset, and failed second-factor attempts (GD-21, REQ-AUD-028). Secrets and codes are never in the trail.
- Data Changes: Creation, modification, and deletion of records.
- Transfers: Every executed cross-project transfer records a `transfer_executed` entry in the **source** project — acting user, source record and field, target project and record, whether the record was created, and which fields were copied or skipped — carrying no values; the writes to the **target** project appear there as the standard data-change entries with a reference to the source project and record. All entries ride the operation's transaction, so a failed transfer records nothing that did not happen (REQ-AUD-034).
- Record Views: All views of records, i.e. every record a user pulls through the API.
- Record Reads: Every time the web interface shows a record — one `record_read` event per page view with the record and the (event, instrument, instance) displayed, never values; written before the form reaches the browser, so an unrecorded read is never shown (REQ-AUD-035). It lives in audit_events, not in audit_record_views: a browser read has no token. The two trails together answer who has seen a record — pulls on one, openings on the other.
- Project Structure: Changes to instruments, fields, or project settings.
- Token Views: Every Show of a just-issued project token on the Members page, as its own entry beside the add or rotation — never the value (REQ-AUD-033).
- Project Modes: Mode changes (with the keep-or-delete decision for development to production) and the staging lifecycle - start, commit (including the breaking changes acknowledged), discard, and the acknowledgement carried by a structure change applied in analysis mode.
- Exports: Full or anonymized data exports.
- Audit Trail Export: A downloadable zip-compressed CSV copy of the trail itself from the Audits page, reserved to administrators, honoring the read filters and covering every matching entry; each export is itself an audited event, and exports estimated above 1 GiB are confirmed first (REQ-AUD-031/032).

## Log Details
Each log entry will include:
- Timestamp: Exact time of the event.
- User ID: Who performed the action.
- Action: Type of event (e.g., "Update Record").
- Details: JSON object containing the old and new values (for data changes).

## Storage
- Two audit tables in the database:
    - audit_events: all events that change, create, or delete records, plus project structure changes, exports, and authentication events.
    - audit_record_views: exclusively for all views of records - every record a user pulls through the API (who, when, which records/instruments, which token). API pulls only: a record opened in the web interface has no token and is logged to audit_events as `record_read` instead (REQ-AUD-035, DEV-AUD-6).
- Both tables are immutable and can only be appended to.

## Table Partitioning
- Yearly Rollover: The log tables are partitioned or rolled over annually (e.g., audit_logs_2026, audit_logs_2027) to maintain performance and manageability. MariaDB uses table partitions in production; the SQLite development environment uses per-year tables.
