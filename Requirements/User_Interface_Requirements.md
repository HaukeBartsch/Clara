# User Interface — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)
**Source plan:** `Plan/User_Interface.md`, `Endpoints.md` (master spec)
**Status:** Approved for development handoff
**Date:** 2026-09-19

## 1. Purpose

Defines the web user interface requirements: the views, the permission gating of every page/section/action, and the UI obligations parked in the other area documents (record history, data access groups, calculated-field test, survey page, escaping in value views). `Design/User_Interface_Design.md` contains page layouts, component details, and interaction specifications.

## 2. Common Requirements (all views)

| ID | Requirement |
|---|---|
| REQ-UI-001 | The UI MUST be plain HTML and vanilla JavaScript with Bootstrap as the only UI library; no frontend framework and no build step (REQ-TECH-001). Pages are rendered by the PHP layer, which delegates all data access to the API (REQ-TECH-006, master spec). |
| REQ-UI-002 | The browser MUST talk only to PHP routes; it MUST NOT call `/api/v1/*` directly — PHP invokes the API server-side with the service token and acting user id (GD-1, REQ-AUTH-010, REQ-TECH-006). |
| REQ-UI-003 | Every page, section, and action MUST be present in the UI only when the acting user's effective permissions allow it (REQ-AUTH-027); "hidden" means absent from the DOM, not merely disabled. |
| REQ-UI-004 | All user-supplied content MUST be rendered safely: free-form text values are stored sanitized and rendered as allowlist HTML (REQ-VAL-030/031); **all other** content (choice values, labels, record names, user names, audit fields) MUST be escaped server-side (REQ-TECH-020); the Content-Security-Policy header MUST be sent (REQ-TECH-020). |
| REQ-UI-005 | All state-changing browser requests MUST carry the per-session CSRF token (REQ-AUTH-037). |
| REQ-UI-006 | A user who is neither an administrator nor a member of any project MUST be shown an information page explaining how access is granted (REQ-AUTH-028), not an empty dashboard. |
| REQ-UI-007 | Login is the PHP-side OAuth2/LDAP flow (REQ-AUTH-001…003); on session inactivity timeout or logout the user MUST be returned to the login page (REQ-AUTH-015); logout MUST call the API's logout endpoint before destroying the session (REQ-API-045). |
| REQ-UI-008 | The UI MUST be multilingual with English as the default and fallback (GD-12): every UI string — including JavaScript-originated messages — MUST be translated via the mapping tables (REQ-DB-031) or fall back to English, never a blank or a raw key; Norwegian Bokmål (`nb`) and Nynorsk (`nn`) are the first target languages; translations MUST be applied server-side at render time (no i18n JavaScript library, consistent with REQ-TECH-001); responsive Bootstrap layout. |

## 3. Dashboard

| ID | Requirement |
|---|---|
| REQ-UI-009 | The start page after login MUST list the projects visible to the user with quick statistics (record and field counts) as per `GET /api/v1/projects` (REQ-API-049); the administration entry point MUST be shown to `is_admin` users only (REQ-UI-003). |
| REQ-UI-010 | For a member with one or more data access groups, the dashboard MUST show the currently active group and offer a switch to any of the member's assigned groups (self-service, REQ-API-090, REQ-AUTH-046); the switch MUST take effect on the next data page load. |

## 4. Administration Interface (`is_admin` only)

| ID | Requirement |
|---|---|
| REQ-UI-011 | **User accounts.** A list of accounts (id, email, display name, enabled, `is_admin`, authentication source) and actions to create an account (email + display name), re-enable a disabled one, and disable one (REQ-API-046/047/048). |
| REQ-UI-012 | **Projects.** A project creation form covering all master-spec attributes (name, organization, PI name/email, data manager, REK/IRB number, start/end dates, end provision, options, participant naming pattern, initial events — REQ-DB-006) and a project edit form for metadata (REQ-API-050/052). |
| REQ-UI-013 | **Assignments.** Per project: the member list with role, data access groups, and token state; assign a member choosing a role from the project's actual roles — which MAY be empty — plus "no role" (full permissions, REQ-AUTH-020/022); issue and rotate tokens, displaying the new token exactly once with an explicit copy affordance (REQ-API-055); set a member's data access group assignments and active group (REQ-API-089, REQ-AUTH-044). |
| REQ-UI-014 | **Role editor.** Create a role with a name and per-arm permissions: for each arm a data access level and an export level, plus the `project_admin` checkbox (GD-2, REQ-API-057); the example presets MAY be offered as starting points only (REQ-AUTH-020). |
| REQ-UI-015 | **Data access groups.** Create groups (name, unique per project) and delete them, with a warning that deletion is rejected while records are still assigned (REQ-API-087/088). |
| REQ-UI-016 | **Audit view.** A paginated, read-only table of audit entries with a project filter and the acting-user/target fields (REQ-API-077/078, REQ-AUD-019); a non-administrator sees only entries of their own projects; values MUST be escaped (REQ-UI-004). |

## 5. Project Workspace

| ID | Requirement |
|---|---|
| REQ-UI-017 | The project screen MUST show the project summary (record count, instrument count, field count — REQ-API-051) and the actions **Setup**, **Design**, **Record status**, and **Export**, each present only when allowed: Setup and Design require `project_admin` (REQ-UI-003), Record status requires data access ≥ `read_only`, Export requires a non-`export_none` level for the arm (REQ-API-075). |
| REQ-UI-018 | **Setup page.** Manage arms, events (label, period, safe region/position), instruments (including the survey flag and the branching logic expression, REQ-DB-011), and the instrument-by-event mapping as per-arm checkboxes (REQ-API-058…073); an arm with events is hidden everywhere (REQ-AUTH-027) once removed. |
| REQ-UI-019 | **Record status dashboard.** A table of records × events with the instruments per arm in order and a filled/empty indicator per (record, event, instrument) — "any field has a value" — without revealing field values (REQ-API-074, plan §6); rows MUST be restricted to records visible under the data access group rule (REQ-AUTH-045). |
| REQ-UI-020 | **Export action.** Offer the project export in the acting user's export level (CSV or JSON) per arm, with a visible indicator of the sensitivity level being applied (REQ-API-075); the download MUST stream for large exports (REQ-TECH-011). |

## 6. Instrument Designer (`project_admin`)

| ID | Requirement |
|---|---|
| REQ-UI-021 | Select an instrument and manage its fields: add, edit, remove, reorder (REQ-API-064…071) with the full attribute set (name, label, type including `calculated`, section header, choices, note, validation type/min/max, required, personal-information flag, matrix group, branching logic expression — REQ-DB-013); the branching logic editor MUST offer `[event][field]` reference assistance, value comparisons, the usual functions, AND/OR, and parentheses (GD-13, REQ-VAL-029), and invalid expressions MUST be rejected with the reason shown (REQ-VAL-029); a warning MUST be shown for field names longer than 26 characters (REQ-VAL-013). |
| REQ-UI-022 | **Calculated field editor.** An expression editor for the calculation expression with references `[event][field]`, numeric constants, `+ - * /`, and parentheses (REQ-VAL-033); invalid expressions (nonexistent/inactive fields, cycles) MUST be rejected by the API and the reason shown to the designer (REQ-VAL-034/035). |
| REQ-UI-023 | **Test a calculation.** A record picker plus a run action invoking the dry-run test (REQ-API-096); the result MUST be displayed and every evaluation problem (missing/empty reference, non-numeric operand, division by zero) MUST be flagged visibly (REQ-VAL-038); the test MUST NOT change stored values. |
| REQ-UI-024 | **Survey flag.** Mark an instrument as a survey (GD-9); only survey-marked instruments may then be filled via a public link (REQ-AUTH-038), and the record view offers the link actions (REQ-UI-028). |

## 7. Data Entry and Record View

| ID | Requirement |
|---|---|
| REQ-UI-025 | The data entry form MUST render the instrument's fields for the selected (record, event) — text inputs, dropdowns, radio groups, matrix rows expanded (REQ-DB-014) — and submit via the data API import path. Client-side real-time validation is advisory only; the API is authoritative (REQ-VAL-002). Required fields MUST be presented as such and checked before submission (REQ-VAL-028); field and instrument branching logic MUST show or hide fields and whole instruments while the expression evaluates to true (1), and the display state MUST update when a referenced field's value changes (GD-13, REQ-VAL-029/040); Calculated fields MUST be rendered read-only (REQ-VAL-036). |
| REQ-UI-026 | **Per-field change history.** The data entry form MUST show, per field, who changed it, when, and old → new values, from the record history endpoint (REQ-API-079/081); values MUST be escaped (REQ-UI-004); the view MUST be read-only with respect to the audit trail (REQ-AUD-002). |
| REQ-UI-027 | **Record actions.** Delete the record or scoped values with an explicit confirmation (GD-3, REQ-API-036); show the record's current data access group and offer assign/change group to `project_admin` users (REQ-API-091); all actions gated per REQ-UI-003. |
| REQ-UI-028 | **Survey page.** For a (record, survey instrument) pair the record view MUST offer "copy link" (REQ-API-082) and "revoke link" (REQ-API-085). The public survey page is a standalone PHP-served route — no login, outside the session (GD-1, REQ-API-084) — showing only that instrument's fields for that record with a submit action (GD-9); the respondent may re-open the link to edit their responses (REQ-AUTH-042); submissions pass the same validation and audit rules as any import (REQ-AUTH-041, REQ-VAL-001). |

## 8. Multilingual (GD-12)

| ID | Requirement |
|---|---|
| REQ-UI-029 | The UI MUST offer a language selector listing the enabled languages (REQ-API-097); the choice MUST be persisted to the acting user's setting (REQ-API-098), apply from the next page load, and default to English for users without a setting. |
| REQ-UI-030 | **Translation management (administration).** Per language: a list of translation keys with translated/missing status (REQ-API-099) and an editor to add, change, or clear translations (REQ-API-100); missing translations MUST be visible in the list and MUST fall back to English at render time (REQ-UI-008); translated strings MUST be escaped on render (REQ-UI-004). |

## 9. Assumptions

| ID | Assumption |
|---|---|
| ASM-UI-1 | Desktop-first responsive layout, no mobile-specific optimization beyond Bootstrap defaults. Locale-specific date/number formatting is a design-document concern; the language setting applies to UI strings only — never to stored data, field labels, or choice values (GD-5, GD-12). |
| ASM-UI-2 | All UI obligations parked in the other area documents (record history REQ-API-081, DAG switcher/record reassignment REQ-API-094, calculated-field test REQ-API-096, required/branching presentation REQ-VAL-028/029, escaping in value views REQ-VAL-031) are consolidated here and are normative via their cross-references. |
| ASM-UI-3 | The role editor presents the per-arm level pickers (data access level + export level per arm, plus `project_admin`); "no role" is an explicit choice meaning full permissions (REQ-AUTH-022). |

## 10. Deviations

| ID | Deviation | Source |
|---|---|---|
| DEV-UI-1 | The public survey page is a PHP route outside the session, while all other UI pages are session-based | Consistent with GD-1 (PHP owns the session) and GD-9 (respondent has no login); the browser still never calls `/api/v1/*` (REQ-UI-002). |
| DEV-UI-2 | Multilingual UI (English default), superseding the master spec's "UI language: English" | Owner decision (2026-09-19, GD-12): English remains the default and the fallback; Bokmål and Nynorsk are first targets; the mapping tables (REQ-DB-031) admit further languages without schema changes. |
