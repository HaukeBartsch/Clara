Audit Logging Plan

This document outlines the strategy for tracking user activity and data changes within the clinical study management system.

Logged Events
- Authentication: Login attempts (success/failure) and logouts.
- Data Changes: Creation, modification, and deletion of records.
- Record Views: All views of records, i.e. every record a user pulls through the API.
- Project Structure: Changes to instruments, fields, or project settings.
- Project Modes: Mode changes (with the keep-or-delete decision for development to production) and the staging lifecycle - start, commit (including the breaking changes acknowledged), discard.
- Exports: Full or anonymized data exports.

Log Details
Each log entry will include:
- Timestamp: Exact time of the event.
- User ID: Who performed the action.
- Action: Type of event (e.g., "Update Record").
- Details: JSON object containing the old and new values (for data changes).

Storage
- Two audit tables in the database:
    - audit_events: all events that change, create, or delete records, plus project structure changes, exports, and authentication events.
    - audit_record_views: exclusively for all views of records - every record a user pulls through the API (who, when, which records/instruments, which token).
- Both tables are immutable and can only be appended to.

Table Partitioning
- Yearly Rollover: The log tables are partitioned or rolled over annually (e.g., audit_logs_2026, audit_logs_2027) to maintain performance and manageability. MariaDB uses table partitions in production; the SQLite development environment uses per-year tables.
