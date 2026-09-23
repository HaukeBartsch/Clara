# Audit Logging — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Audit_Logging_Requirements.md`
**Date:** 2026-09-21

## 1. Purpose

The normative event catalog (event codes, `details` payload schemas), the record-view entry rules, the write mechanics, and the partitioning/rollover mechanics for the two audit tables defined in `Database_Schema_Design.md` §7. The requirements document fixes the taxonomy and minimum entry content (REQ-AUD-016, ASM-AUD-3); this document fixes the codes and schemas.

## 2. Conventions

- **Event codes** (`event_type`) are stable, lowercase, `snake_case` strings; the audit UI and any downstream consumer may branch on them. Adding a code is non-breaking; changing or removing one is a breaking change.
- **Fixed columns** (per `Database_Schema_Design.md` §7) carry the fields common to every entry: `source` (`api` / `ui` / `system`), `user_id`, `email`, `token` (data-API calls and survey-link calls only — REQ-AUD-018), `project_id`, `arm_num`, `role`, `details` (JSON, §3), `created_at` (UTC, REQ-AUD-005), and `target_record` (§6.4, record-scoped events).
- **`source`** (REQ-AUD-017): `api` = direct data-API call (external callers such as Fiona, or a survey link); `ui` = administration-API call initiated by the PHP layer; `system` = system-driven write (calculated-field recomputation), with the triggering user still recorded in `user_id`.
- **Values in payloads** are the stored values (sanitized, canonical forms per `Data_Validation_Design.md`), never raw request bytes. The trail is a business record and MAY contain tokens and values as defined here (REQ-AUD-007); application logs MUST NOT (REQ-API-005, REQ-TECH-016).
- Rejected calls (400/401/403) and validation-failed imports produce **no** data-change, structure, export, or record-view entries (REQ-AUD-004); security-relevant rejections are logged as `admin_rejected` / `login_failure` (§3.1).

## 3. Event Catalog (normative)

### 3.1 Authentication and administration boundary (REQ-AUD-008)

| Code | When | `details` payload |
|---|---|---|
| `login_success` | every successful login | `{"source":"oauth2 or ldap","provider":"<issuer> or <LDAP server n>","display_name":"…"}` |
| `login_failure` | every failed login attempt | `{"source":"oauth2 or ldap","email":"…","reason":"provider_unavailable or state_mismatch or bad_credentials or account_not_found or account_disabled or rate_limited"}` |
| `logout` | explicit logout (REQ-AUTH-015) | `{}` |
| `admin_rejected` | rejected administration-API call (missing/invalid service token, unknown or disabled user — REQ-AUTH-011…013) | `{"path":"POST /api/v1/…","reason":"service_token_invalid or user_unknown or user_disabled"}` |
| `account_auto_disabled` | inactivity auto-disable of the account active rule (GD-19, REQ-AUTH-053, REQ-AUD-024) — `enabled` set to `0` and the inactivity clock reset, same transaction as this entry; `source=system` | `{"email":"…","last_login_at":"…","inactivity_limit_days":180}` |

No IdP tokens, LDAP passwords, or service-token values ever appear in payloads (REQ-AUD-007, REQ-API-005).

### 3.2 Data change events (REQ-AUD-009)

| Code | When | `details` payload |
|---|---|---|
| `record_created` | import creating a new record (REQ-API-035) | data-change shape below, `"action":"create"` |
| `record_updated` | import changing an existing record | shape below, `"action":"update"` |
| `record_deleted` | `content=record&action=delete` (REQ-API-036) | shape below, `"action":"delete"` |
| `calculated_recomputed` | every system-driven recomputation (REQ-AUD-023, REQ-VAL-038) | `{"record_id":"…","field":"total","old":"9","new":"11","trigger_field":"a","trigger_event":"e1_arm_1"}` |

Data-change shape (create / update / delete):

```json
{
  "action": "create | update | delete",
  "record_id": "8DISC042",
  "instrument": "intake",
  "event": "e1_arm_1",
  "fields": [
    { "field": "age", "old": null, "new": "42" },
    { "field": "mrn", "old": "00000000001", "new": null }
  ]
}
```

Rules: `old` is `null` for `create`; for `delete`, `new` is `null` and the `old` values are the deleted values (REQ-AUD-009). One entry per (record, instrument, event) group of the operation. The acting user, token, and arm are in the fixed columns; `target_record` is set (§6.4).

### 3.3 Project structure events (REQ-AUD-010)

| Code | When | `details` payload |
|---|---|---|
| `project_created` | `POST /api/v1/projects` (REQ-API-050) | `{"project_name":"…"}` |
| `project_updated` | `PUT /api/v1/projects/{id}` (REQ-API-052) | `{"changes":{"pi_name":{"old":"…","new":"…"},"…":"…"}}` |
| `arm_created` | REQ-API-059 | `{"arm_num":2,"name":"…"}` |
| `arm_deleted` | REQ-API-060 | `{"arm_num":2,"name":"…"}` |
| `event_created` | REQ-API-062 | `{"event_name":"…","unique_event_name":"…","arm_num":1}` |
| `event_updated` | REQ-API-063 | `{"event_id":7,"changes":{"label":{"old":"…","new":"…"},"period_days":{"old":0,"new":14}}}` |
| `event_reordered` | `PUT …/events/order` (REQ-API-103) | `{"arm_num":1,"order":[9,4,7]}` |
| `instrument_created` | REQ-API-065 | `{"name":"intake","position":1}` |
| `instrument_updated` | REQ-API-101 | `{"name":"…","changes":{"is_survey":{"old":0,"new":1}}}` |
| `instrument_reordered` | REQ-API-066 | `{"order":["intake","follow_up"]}` |
| `field_created` | REQ-API-068 | `{"instrument":"intake","field":"age","type":"text"}` |
| `field_updated` | REQ-API-069 | `{"instrument":"…","field":"…","changes":{"validation_max":{"old":null,"new":"120"}}}` |
| `field_deleted` | REQ-API-070 | `{"instrument":"…","field":"…","values_removed":1234}` (DEV-API-5) |
| `field_reordered` | REQ-API-071 | `{"instrument":"…","order":["a","b","c"]}` |
| `mapping_updated` | REQ-API-073 | `{"arm_num":1,"pairs":{"intake":{"e1_arm_1":1,"e2_arm_1":0}}}` |
| `project_ended` | the one-shot end-of-project provision action — `POST /api/v1/projects/{id}/end-provision` (BR-009, `API_Endpoints_Design.md` §4.20; rules `Data_Export_Anonymization_Design.md` §7.2) | `{"provision":"delete or anonymize","records_affected":42,"values_affected":1287}` |

Field renames are `field_updated` with `"changes":{"name":{"old":"…","new":"…"}}` (REQ-VAL-014, DEV-VAL-4); the stored values are renamed in the same transaction (`Data_Validation_Design.md` §9).

### 3.4 Administration events (REQ-AUD-012)

| Code | When | `details` payload |
|---|---|---|
| `user_created` | REQ-API-047 | `{"email":"…","display_name":"…","re_enabled":false}` — `re_enabled:true` when a disabled account is re-enabled |
| `user_updated` | REQ-API-048 | `{"email":"…","enabled":0}` |
| `membership_changed` | REQ-API-054 | `{"member_email":"…","action":"add or role_change or remove","role":"data-entry or null"}` |
| `token_issued` | first issuance via REQ-API-054 (REQ-AUTH-030) | `{"member_email":"…"}` — the token value is never in `details` (REQ-API-005) |
| `token_rotated` | REQ-API-055 | `{"member_email":"…"}` |
| `token_revoked` | member removal or explicit revocation (REQ-AUTH-030) | `{"member_email":"…","reason":"member_removed or explicit"}` |
| `role_created` | REQ-API-057 | `{"name":"data-manager","project_admin":1,"arms":{"1":{"data":"delete","export":"export_full"},"2":{"data":"view_edit","export":"export_none"}}}` |
| `i18n_updated` | REQ-API-100 | `{"language":"nb","key":"ui.dashboard.title","action":"set or removed"}` |

### 3.5 Export events (REQ-AUD-011, BR-008)

| Code | When | `details` payload |
|---|---|---|
| `export` | every data export on both surfaces — data API (`content=record&action=export`) and administration API (`GET /api/v1/projects/{id}/export`) | `{"surface":"data_api or ui","sensitivity":"full or no_identifiers or de_identified","filters":{"records":["…"],"fields":["…"],"forms":["…"],"events":["…"],"filter_logic":"[age]=\"42\""}}` |

Rules: omitted filters are empty arrays; `sensitivity` is the level applied per REQ-API-026/075 (for multi-arm exports, the least restrictive level applied); the record-view row for data-API exports is written **in addition** to this entry (REQ-AUD-013, §4).

### 3.6 Survey events (REQ-AUD-021)

| Code | When | `details` payload |
|---|---|---|
| `survey_submitted` | every survey-link submission, success **and** failure (REQ-AUD-021) | `{"status":"success or failure","reason":null or "…","record_id":"…","instrument":"…","fields":[{"field":"…","old":"…","new":"…"}]}` — the link token is in the fixed `token` column (REQ-AUTH-041), never in `details` |
| `survey_link_issued` | `GET …/survey-link` (REQ-API-082) | `{"record_id":"…","instrument":"…"}` |
| `survey_link_revoked` | `DELETE …/survey-link` (REQ-API-085) | `{"record_id":"…","instrument":"…"}` |

### 3.7 Data access group events (REQ-AUD-022)

| Code | When | `details` payload |
|---|---|---|
| `dag_created` | REQ-API-087 | `{"name":"…"}` |
| `dag_deleted` | REQ-API-088 (rejected 409 while records are assigned → no entry, REQ-AUD-004) | `{"name":"…","record_count":0}` |
| `dag_membership_changed` | REQ-API-089 | `{"member_email":"…","groups":[3,7],"active_group_id":3}` |
| `dag_active_switched` | REQ-API-090 (self-service, REQ-AUTH-046) | `{"member_email":"…","group_id":7}` |
| `dag_record_assigned` | REQ-API-091 | `{"record_id":"…","group_id":3 or null}` — `null` = unassigned; `target_record` is set |

## 4. Record View Entries — `audit_record_views`

One row per invocation of `content=record&action=export` — regardless of initiator (external caller or the PHP layer, REQ-AUD-013, ASM-API-3). Rejected invocations (invalid token, `export_none`, no visibility) write no row (REQ-AUD-004).

| Column | Content |
|---|---|
| `token` | the project token or link token presented (NOT NULL — the only actor a record pull has) |
| `user_id` / `email` | resolved from the token (`null` for survey-link pulls, where the respondent has no account) |
| `project_id` | the project of the pull |
| `record_ids` | JSON array of the record ids **returned** by the call (after data-access-group filtering, REQ-AUTH-045); no filters supplied → all records returned |
| `instruments` | JSON array of the instrument names present in the returned rows |
| `created_at` | UTC |

The row records **no values** — only who, when, which records, which instruments, which token (REQ-AUD-014). Reads that do not return record values (`project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, record status) write no row (REQ-AUD-015, ASM-AUD-2).

## 5. Write Mechanics

- **Same transaction** (REQ-AUD-003, DEV-AUD-1): the audit `INSERT` is issued before `COMMIT` of the operation it describes; a rollback of the operation rolls back the entry, and a failure to write the entry fails the operation. No data change commits without its audit entry.
- **Attribution** (REQ-AUD-017/018): data-API entries carry the token in the fixed column, `source=api`; administration entries carry the acting user (from `X-Internal-User-Id`, authoritative per REQ-AUTH-013), `source=ui`; system-driven entries (calculated recomputation) carry the triggering user, `source=system`, and no token.
- **Timestamps** (REQ-AUD-005): server UTC at write time; MariaDB `DATETIME`, SQLite `TEXT` `YYYY-MM-DD HH:MM:SS` (`Database_Schema_Design.md` §2).
- **Append-only** (REQ-AUD-002): the API exposes no UPDATE/DELETE on either table; on MariaDB the application account holds `INSERT, SELECT` only (REQ-DB-024).
- **Values** in `details` are the stored (validated, sanitized) values, not request bytes (REQ-AUD-007, §2).

## 6. Partitioning and Rollover (REQ-AUD-006, REQ-DB-022)

The application reads and writes **only** the stable names `audit_events` / `audit_record_views`; the physical objects are per dialect.

### 6.1 MariaDB — yearly partitions (normative DDL)

```sql
CREATE TABLE IF NOT EXISTS audit_events (
    id            INTEGER PRIMARY KEY AUTO_INCREMENT,
    event_type    VARCHAR(64) NOT NULL,
    source        VARCHAR(8)  NOT NULL,
    user_id       INTEGER,
    email         VARCHAR(254),
    token         CHAR(36),
    project_id    INTEGER,
    arm_num       INTEGER,
    role          VARCHAR(255),
    target_record VARCHAR(255),
    details       TEXT,
    created_at    DATETIME NOT NULL,
    PARTITION BY RANGE (YEAR(created_at)) (
        PARTITION p2026 VALUES LESS THAN (2027),
        PARTITION p2027 VALUES LESS THAN (2028)
    )
);
```

Same shape for `audit_record_views` (columns per `Database_Schema_Design.md` §7). **No `MAXVALUE` partition** — a `MAXVALUE` guard would block later `ADD PARTITION`.

**Rollover check** (idempotent; runs at process startup and on the first audit write after a calendar-year change — an in-process year latch, no scheduler): inspect `information_schema.PARTITIONS`; if the current year's or the next year's partition is missing, `ALTER TABLE audit_events ADD PARTITION (PARTITION pYYYY VALUES LESS THAN (YYYY+1))` — likewise for `audit_record_views` — before the write proceeds.

### 6.2 SQLite — per-year tables behind stable views (normative)

```sql
CREATE TABLE IF NOT EXISTS audit_events_2026 (
    id INTEGER PRIMARY KEY,
    event_type TEXT NOT NULL,
    source TEXT NOT NULL,
    user_id INTEGER,
    email TEXT,
    token TEXT,
    project_id INTEGER,
    arm_num INTEGER,
    role TEXT,
    target_record TEXT,
    details TEXT,
    created_at TEXT NOT NULL
);
CREATE VIEW IF NOT EXISTS audit_events AS SELECT * FROM audit_events_2026;
```

**Rollover check** (same trigger as §6.1): if `audit_events_<current year>` does not exist, create it with the same shape, then `DROP VIEW audit_events` and recreate it as `UNION ALL` over all `audit_events_*` tables in year order (same for `audit_record_views`). The audit tables carry no foreign keys — they are append-only business records — so view recreation has no integrity side effects.

### 6.3 Rollover is maintenance, not migration

Year objects are created by the startup/first-write check with the shape of the current migration; `schema_version` (REQ-DB-003) tracks shape migrations only. Rollover requires no application change and no downtime (REQ-AUD-006). Retention is indefinite — no expiry logic anywhere (ASM-AUD-1); dropping old years is an operational action outside the application.

### 6.4 DDL supplement — `target_record` column and indexes

To serve the record-scoped read patterns (REQ-AUD-020, REQ-API-079/080) at the reference scale (REQ-DB-025), `audit_events` carries one additional column — a normative supplement to `Database_Schema_Design.md` §7, applied by migration `004_audit_record_index.up.sql`:

```sql
ALTER TABLE audit_events ADD COLUMN target_record VARCHAR(255);   -- SQLite: TEXT
CREATE INDEX IF NOT EXISTS idx_audit_events_type   ON audit_events (project_id, event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_record ON audit_events (project_id, target_record, created_at);
```

`target_record` is set for record-scoped events — `record_created`, `record_updated`, `record_deleted`, `calculated_recomputed`, `survey_submitted`, `dag_record_assigned` — and `NULL` otherwise. The pre-existing `idx_audit_events_project (project_id, created_at)` and `idx_audit_events_user (user_id, created_at)` (`Database_Schema_Design.md` §7) cover the time-range, project, and user patterns; the two indexes above cover event type and record (REQ-AUD-020, DEV-AUD-2). `audit_record_views` keeps `idx_audit_views_project`.

## 7. Read Access

Reads go through the two endpoints only (REQ-AUD-019; full contracts in `API_Endpoints_Design.md`):

- `GET /api/v1/audit` — both tables selected by `type=events|views`, reverse-chronological, `limit`/`cursor` pagination, filters: `project` (mandatory for non-admins, optional for `is_admin`), `user`, `event_type`, `from`, `to` (UTC). A non-admin sees only entries of projects they are a member of (REQ-API-078, ASM-API-2).
- `GET /api/v1/projects/{id}/records/{record}/history` — chronological, per record; filters `instrument`, `event`, `field`; `limit`/`cursor`. Served by `idx_audit_events_record` (§6.4); the `field` filter matches `details.fields[].field` within the record-scoped page (REQ-API-080). History is transparent across the yearly rollover because queries always use the stable name (REQ-AUD-006).

Both endpoints are read-only; no endpoint exists that writes, updates, or deletes audit data (REQ-DB-024, REQ-API-077).

## 8. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| `details` JSON shapes per audit event type (`Database_Schema_Design.md` §12) | complete catalog with payload schemas, §3 |
| ASM-AUD-1 (retention) | no expiry; indefinite retention on yearly objects; dropping years is an operational action (§6.3) |
| ASM-AUD-2 (UI-mediated reads) | a record-view row is written when the PHP layer initiates `content=record&action=export`; record status and structure reads are not record views (§4) |
| ASM-AUD-3 (event codes and payload schemas) | §3 is the normative catalog |
| record-scoped read index (REQ-AUD-020, DEV-AUD-2) | `target_record` column + `idx_audit_events_record` (§6.4) |

## 9. Open Items

| Item | Owner |
|---|---|
| ~~`limit`/`cursor` encoding for `/api/v1/audit` and record history~~ | **RESOLVED** — the pagination convention of `API_Endpoints_Design.md` §1 (opaque `cursor` encoding the last-seen `(created_at, id)`, `limit` default 50 / max 200, `next_cursor` `null` when exhausted) binds for both endpoints (§7) |
