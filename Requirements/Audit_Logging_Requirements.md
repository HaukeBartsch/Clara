# Audit Logging — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/Audit_Logging.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-19

## 1. Purpose

Defines the requirements for the system's audit trail: the two append-only audit tables, the complete event taxonomy, the minimum content of entries, the boundary between the audit trail and application logging, and the authorized read access. `Design/Audit_Logging_Design.md` contains the normative event catalog (event codes, payload schemas) and the partitioning/rollover mechanics.

## 2. Audit Store

| ID | Requirement |
|---|---|
| REQ-AUD-001 | The system MUST maintain two audit tables: `audit_events` (change/create/delete actions, project structure changes, exports, authentication, administration) and `audit_record_views` (record pulls via the data API only) (BR-007, REQ-DB-021). |
| REQ-AUD-002 | Both tables MUST be append-only: the application MUST expose no operation that updates or deletes audit entries; on MariaDB, immutability MUST additionally be enforced at the database privilege level (REQ-DB-024). |
| REQ-AUD-003 | An audit entry MUST commit in the same transaction as the operation it describes: an import, deletion, or structure change whose audit entry cannot be written MUST roll back (DEV-AUD-1); no data change MAY commit without its audit entry. |
| REQ-AUD-004 | Audit entries describe operations that completed: rejected calls (401/403/400) and validation-failed imports MUST NOT generate data-change or record-view entries (REQ-API-035 scopes audit to successful imports); security-relevant rejections remain audit-logged as authentication/administration events (REQ-AUTH-013). |
| REQ-AUD-005 | Every entry MUST carry a timestamp in UTC (GD-7). |
| REQ-AUD-006 | The tables MUST support yearly rollover without application changes: MariaDB partitioned by year; SQLite per-year tables behind stable view names; the application MUST read and write through the stable names only (REQ-DB-022, plan \"Table Partitioning\"). |
| REQ-AUD-007 | The audit trail is a business record, not application logging: it MAY record the token and old/new values as defined in §3–§5; application logs MUST NOT contain tokens, the service secret, or record values (REQ-API-005, REQ-TECH-016). |

## 3. Event Taxonomy — `audit_events`

| ID | Requirement |
|---|---|
| REQ-AUD-008 | **Authentication events.** Every login attempt (success and failure, with the authentication source `oauth2`\|`ldap`), every logout, and every rejected administration API call MUST be recorded (REQ-AUTH-005/008/013/035, REQ-API-044/045). |
| REQ-AUD-009 | **Data change events.** Every record create, update, and delete MUST be recorded with the acting user, the token used, the record, and the changed fields; updates MUST include the old and new values, and deletions MUST include the deleted values, as a JSON details payload (plan \"Log Details\", GD-3, REQ-API-035/036). |
| REQ-AUD-010 | **Project structure events.** Every change to arms, events, instruments, fields (including renames, reordering, and deletion with cascaded values), the instrument-event mapping, and project metadata MUST be recorded with the acting user, the target project, and old/new values where applicable (plan \"Logged Events\", REQ-API-043, REQ-VAL-014). |
| REQ-AUD-011 | **Export events.** Every data export — data API (`content=record&action=export`) or administration API (`GET /api/v1/projects/{id}/export`) — MUST be recorded with the acting user/token, the project, the sensitivity level (full / anonymized / non-sensitive), and the filters supplied (REQ-API-076, BR-008). |
| REQ-AUD-012 | **Administration events.** Every user account change (create, re-enable, disable), every membership/role change, and every token issuance, rotation, and revocation MUST be recorded with the acting administrator, the target user, and the target project (REQ-AUTH-030, REQ-API-043/054/055). |

## 4. Record View Log — `audit_record_views`

| ID | Requirement |
|---|---|
| REQ-AUD-013 | Every invocation of `content=record&action=export` MUST be recorded — regardless of initiator (external caller such as Fiona, or the PHP layer) — with: when (UTC), who (user), which records, which instruments, and which token (plan \"Storage\", REQ-API-030, REQ-DB-021). |
| REQ-AUD-014 | The record view log MUST NOT record the record values themselves, only the records and instruments accessed (plan: \"who, when, which records/instruments, which token\"). |
| REQ-AUD-015 | Reads that do not return record values (`project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, record status) MUST NOT be written to `audit_record_views` (REQ-DB-021: record pulls via the API only). |

## 5. Entry Content

| ID | Requirement |
|---|---|
| REQ-AUD-016 | Every `audit_events` entry MUST contain at minimum: timestamp (UTC), acting user id, action (stable event code), target project (where project-scoped), and a JSON details payload (plan \"Log Details\"); the design document defines the full event code catalog and payload schemas (ASM-AUD-3). |
| REQ-AUD-017 | Every entry MUST identify the origin of the call (data API vs. administration API/UI) so the trail distinguishes direct token calls from UI-mediated actions (REQ-DB-021 `source`, DEV-DB-4). |
| REQ-AUD-018 | Data API entries MUST record the token used (plan: \"which token\"); administration API entries MUST record the acting user (REQ-API-041/043). |

## 6. Read Access

| ID | Requirement |
|---|---|
| REQ-AUD-019 | The audit trail MUST be readable only through `GET /api/v1/audit` (read-only, paginated; REQ-API-077); a non-admin acting user MUST see only entries for projects they are a member of, and an `is_admin` user MAY query all projects (REQ-API-078). |
| REQ-AUD-020 | The audit tables MUST be indexed to support the read access patterns: time range, project, user, and event type (REQ-API-077 pagination at the reference scale of REQ-DB-025; DEV-AUD-2). |

## 7. Assumptions

| ID | Assumption |
|---|---|
| ASM-AUD-1 | Retention beyond the yearly rollover is an operational decision; the schema supports indefinite retention (yearly partitions/tables, REQ-DB-022) and defines no expiry. |
| ASM-AUD-2 | UI-mediated record reads are logged when they pass through the data API export action (the PHP layer initiates them, ASM-API-3); the record status dashboard and structure reads are not record views (REQ-AUD-015). |
| ASM-AUD-3 | The exact event codes and payload schemas are normative in the design document; this document fixes the taxonomy and the minimum entry content. |

## 8. Deviations from Plan

| ID | Deviation | Rationale |
|---|---|---|
| DEV-AUD-1 | The audit entry commits atomically (same transaction) with the audited operation | The plan mandates logging but is silent on transactional coupling; atomicity guarantees no data change ever commits without its audit entry (REQ-AUD-003). |
| DEV-AUD-2 | Explicit indices for the audit read patterns (time range, project, user, event type) | The plan is silent on audit indexing; required for the paginated read API (REQ-API-077) to stay responsive at the reference scale (REQ-DB-025). |
