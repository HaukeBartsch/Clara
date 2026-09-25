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
| REQ-UI-007 | Login is the PHP-side OAuth2/LDAP flow (REQ-AUTH-001…003) **or the email+password form, which tries the local (table-based) path first and falls back to LDAP** (GD-18, REQ-AUTH-050/051); the login page MUST show the provider buttons only for configured providers and the email+password form always (it is the only path on a first install without an IdP); on session inactivity timeout or logout the user MUST be returned to the login page (REQ-AUTH-015); logout MUST call the API's logout endpoint before destroying the session (REQ-API-045). |
| REQ-UI-008 | The UI MUST be multilingual with English as the default and fallback (GD-12): every UI string — including JavaScript-originated messages — MUST be translated via the mapping tables (REQ-DB-031) or fall back to English, never a blank or a raw key; Norwegian Bokmål (`nb`) and Nynorsk (`nn`) are the first target languages; translations MUST be applied server-side at render time (no i18n JavaScript library, consistent with REQ-TECH-001); responsive Bootstrap layout. |
| REQ-UI-032 | **Layout, style, and client-side data binding (reference application).** The web application MUST adopt the layout and visual style of the historic FIONA reference in `assets/table_based_authentication_plus_user_management/` (master spec, "Details"): the left **sidebar + content-panel** shell, the Bootstrap grid, **`table-sm`** condensed tables, and a **responsive** layout that adjusts to tablet and phone widths. It MUST also adopt the reference's *interfacing style* for the presentation layer (REQ-TECH-025): the PHP layer renders each page's shell, structure, and static content, and the client populates **data regions** — list rows, table bodies, `<select>` options — by pulling JSON from PHP endpoints and writing it into DOM targets; the client performs **no page switching** (no SPA; REQ-UI-001, REQ-TECH-001). The reference is a style and interaction model, **not** a technology one: it is realized with **Bootstrap 5.3.x** (not the reference's Bootstrap 2.x) and **vanilla ES2020** (not jQuery). Where the reference diverges from a fixed convention it is **not** adopted: client-populated content MUST use safe DOM APIs (`textContent`/`createElement`) for all content except the stored free-text allowlist HTML (REQ-UI-004, REQ-TECH-020); UI strings remain translated server-side (REQ-UI-008); mutations remain CSRF-protected `POST`s to PHP routes (REQ-UI-005); and the browser talks only to PHP routes, never `/api/v1/*` (REQ-UI-002). The reference's `AC.php` is the model for the PHP session-establishment flow (GD-1, `Authentication_Authorization_Design.md` §3), not for the authentication mechanism. |

## 3. Dashboard

| ID | Requirement |
|---|---|
| REQ-UI-009 | The start page after login MUST list the projects visible to the user with quick statistics (record and field counts) as per `GET /api/v1/projects` (REQ-API-049); the administration entry point MUST be shown to `is_admin` users only (REQ-UI-003). |
| REQ-UI-010 | For a member with one or more data access groups, the dashboard MUST show the currently active group and offer a switch to any of the member's assigned groups (self-service, REQ-API-090, REQ-AUTH-046); the switch MUST take effect on the next data page load. |

## 4. Administration Interface (`is_admin` only)

| ID | Requirement |
|---|---|
| REQ-UI-011 | **User accounts.** A list of accounts (email, display name, enabled, `is_admin`, authentication source — incl. `local`, **last login**, **validity end**, and the derived status active / disabled / expired / auto-disabled — GD-19, REQ-AUTH-052/053) and actions to create an account (email + display name, optional validity in days with `0` = indefinite, optional local password), set/extend the validity, set or reset the local password, re-enable a disabled (incl. auto-disabled) one — which MUST restart the inactivity clock — and disable one (REQ-API-046/047/048). Auto-disabled accounts MUST be visually distinguishable with the inactivity reason (REQ-AUTH-053). |
| REQ-UI-012 | **Projects.** A project creation form covering the simplified creation fields of REQ-DB-006 (name, organization = main supporting institution, PI name/email, data manager, REK/IRB number, REK start/end dates, start/end dates, participant naming pattern — GD-17; the option flags, end provision, end-user-contract confirmation, and initial-events list are **absent**) and a project edit form for the same metadata (REQ-API-050/052). Events are created in the project's Setup page (REQ-UI-018). |
| REQ-UI-013 | **Assignments.** Per project: the member list with role, data access groups, and token state; assign a member choosing a role from the project's actual roles — which MAY be empty — plus "no role" (full permissions, REQ-AUTH-020/022); issue and rotate tokens, displaying the new token exactly once with an explicit copy affordance (REQ-API-055); set a member's data access group assignments and active group (REQ-API-089, REQ-AUTH-044). |
| REQ-UI-014 | **Role editor.** Create a role with a name and per-arm permissions: for each arm a data access level and an export level, plus the `project_admin` checkbox (GD-2, REQ-API-057); the example presets MAY be offered as starting points only (REQ-AUTH-020). |
| REQ-UI-015 | **Data access groups.** Create groups (name, unique per project) and delete them, with a warning that deletion is rejected while records are still assigned (REQ-API-087/088). |
| REQ-UI-016 | **Audit view.** A paginated, read-only table of audit entries with a project filter and the acting-user/target fields (REQ-API-077/078, REQ-AUD-019); a non-administrator sees only entries of their own projects; values MUST be escaped (REQ-UI-004). |

## 5. Project Workspace

| ID | Requirement |
|---|---|
| REQ-UI-017 | The project screen MUST show the project summary (record count, instrument count, field count — REQ-API-051) and the actions **Setup**, **Design**, **Record status**, and **Export**, each present only when allowed: Setup and Design require `project_admin` (REQ-UI-003), Record status requires data access ≥ `read_only`, Export requires a non-`export_none` level for the arm (REQ-API-075). |
| REQ-UI-018 | **Setup page.** Manage arms, events (label, **timepoint `period` — optional, blank = no timepoint** — safe region; displayed in the canonical per-arm order of GD-15 with **reorder controls for no-timepoint events** via the events-order endpoint, REQ-API-103), instruments (including the survey flag and the branching logic expression, REQ-DB-011), and the instrument-by-event mapping as per-arm checkboxes (REQ-API-058…073, REQ-API-103); an arm with events is hidden everywhere (REQ-AUTH-027) once removed. |
| REQ-UI-019 | **Record status dashboard.** A table of records × events with the instruments per arm in order and a filled/empty indicator per (record, event, instrument) — "any field has a value" — without revealing field values (REQ-API-074, plan §6); rows MUST be restricted to records visible under the data access group rule (REQ-AUTH-045). |
| REQ-UI-020 | **Export action.** Offer the project export in the acting user's export level (CSV or JSON) per arm, with a visible indicator of the sensitivity level being applied (REQ-API-075); the download MUST stream for large exports (REQ-TECH-011). |
| REQ-UI-033 | **Mode.** The project home MUST show the current mode as a badge (development / production / analysis — GD-20), visible to every member. For `project_admin` it MUST offer a mode control presenting only the allowed transitions (REQ-API-105): development → production opens a confirmation that asks whether previously stored data should be **kept or deleted**, naming the deletion consequence explicitly; production → development and production ↔ analysis confirm with "all data is kept". Attempting to leave production while a staging set is open shows the commit/discard prompt (409 surfaced per the error mapping). Non-admins see the badge only. |
| REQ-UI-034 | **Staged setup (production).** When the project is in production mode, the Setup and Designer pages MUST present the staging controls (GD-20, REQ-API-106/108): **Start staging**; while a set is open a persistent banner ("changes are staged — not yet active") with **Commit staged changes** and **Discard**; the commit dialog lists the staged changes split into non-breaking and breaking (each breaking change with its reason) and requires an explicit acknowledgement before committing. In development mode these controls are absent — setup edits apply directly. |

## 6. Instrument Designer (`project_admin`)

| ID | Requirement |
|---|---|
| REQ-UI-021 | Select an instrument and manage its fields: add, edit, remove, reorder (REQ-API-064…071) with the full attribute set (name, label, type including `calculated`, section header, choices, note, validation type/min/max — the type selector lists the built-in structured types plus every entry of the validation-type registry (REQ-VAL-042/043) — required, personal-information flag, direct-identifier flag (offered on any field, preset for `email`/`MRN`/phone types, clearing it warned — REQ-EXP-020/021), matrix group, branching logic expression — REQ-DB-013); the branching logic editor MUST offer `[event][field]` reference assistance, value comparisons, the usual functions, AND/OR, and parentheses (GD-13, REQ-VAL-029), and invalid expressions MUST be rejected with the reason shown (REQ-VAL-029); a warning MUST be shown for field names longer than 26 characters (REQ-VAL-013). |
| REQ-UI-022 | **Calculated field editor.** An expression editor for the calculation expression with references `[event][field]`, numeric constants, `+ - * /`, and parentheses (REQ-VAL-033); invalid expressions (nonexistent/inactive fields, cycles) MUST be rejected by the API and the reason shown to the designer (REQ-VAL-034/035). |
| REQ-UI-023 | **Test a calculation.** A record picker plus a run action invoking the dry-run test (REQ-API-096); the result MUST be displayed and every evaluation problem (missing/empty reference, non-numeric operand, division by zero) MUST be flagged visibly (REQ-VAL-038); the test MUST NOT change stored values. |
| REQ-UI-024 | **Survey flag.** Mark an instrument as a survey (GD-9); only survey-marked instruments may then be filled via a public link (REQ-AUTH-038), and the record view offers the link actions (REQ-UI-028). |

## 7. Data Entry and Record View

| ID | Requirement |
|---|---|
| REQ-UI-025 | The data entry form MUST render the instrument's fields for the selected (record, event) — text inputs, dropdowns, radio groups, matrix rows expanded (REQ-DB-014) — and submit via the data API import path (REQ-UI-031). Client-side real-time validation is advisory only; the API is authoritative (REQ-VAL-002). Required fields MUST be presented as such and checked before submission (REQ-VAL-028); field and instrument branching logic MUST show or hide fields and whole instruments while the expression evaluates to true (1), and the display state MUST update when a referenced field's value changes (GD-13, REQ-VAL-029/040); Calculated fields MUST be rendered read-only (REQ-VAL-036). |
| REQ-UI-026 | **Per-field change history.** The data entry form MUST show, per field, who changed it, when, and old → new values, from the record history endpoint (REQ-API-079/081); values MUST be escaped (REQ-UI-004); the view MUST be read-only with respect to the audit trail (REQ-AUD-002). |
| REQ-UI-027 | **Record actions.** Delete the record or scoped values with an explicit confirmation (GD-3, REQ-API-036); show the record's current data access group and offer assign/change group to `project_admin` users (REQ-API-091); all actions gated per REQ-UI-003. |
| REQ-UI-028 | **Survey page.** For a (record, survey instrument) pair the record view MUST offer "copy link" (REQ-API-082) and "revoke link" (REQ-API-085). The public survey page is a standalone PHP-served route — no login, outside the session (GD-1, REQ-API-084) — showing only that instrument's fields for that record with a submit action (GD-9); the respondent may re-open the link to edit their responses (REQ-AUTH-042); submissions pass the same validation and audit rules as any import (REQ-AUTH-041, REQ-VAL-001). |
| REQ-UI-031 | **Submission policy (GD-14).** Fields may have no value; an instrument that is partially filled and saved stores only the entered values. The data entry form (and the survey page) MUST submit **only** (a) the fields that carry a value in the form, and (b) the fields that **had a stored value that the user removed** (the form tracks each field's original value and sends an explicit empty string for those); it MUST NOT submit empty values for fields that had no value and were left empty — an empty value reaching the API clears the stored value (REQ-VAL-024), so unsent fields keep their previous values. |
| REQ-UI-035 | **Analysis mode (GD-20).** While the project is in analysis mode, data entry is disabled: the data-entry form MUST NOT offer the submit control (values render read-only), the record-status dashboard's "new participant" affordance is absent, and the public survey page shows a closed state (submissions are rejected by the API, REQ-API-109). Viewing and export controls are unaffected. |

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
| DEV-UI-3 | Data-entry submission policy: only entered values and explicitly cleared fields are submitted; untouched empty fields are never sent | Owner decision (2026-09-22, GD-14; "This is a user interface decision"): a partially filled instrument stores only entered values; an API empty value is an intentional clear (REQ-VAL-024), so the UI must not send empties for fields the user did not remove (REQ-UI-031). |
| DEV-UI-4 | Events table shows the canonical order (timepoint events by timepoint) and offers reorder controls for no-timepoint events | Owner decision (2026-09-22, GD-15; master spec "allow changing of event order"): timepoint events sort by their timepoint, non-timepoint events are user-reorderable (REQ-UI-018, REQ-API-103). |
| DEV-UI-5 | User overview extended: last login, validity end, derived status (incl. auto-disabled), validity/password actions | Owner decisions (2026-09-22, GD-18/GD-19; master spec "Details"): table-based accounts, validity days (`0` = indefinite), 180-day inactivity auto-disable with admin re-enable and display (REQ-UI-011). |
| DEV-UI-6 | Project creation form reduced to the simplified field set (GD-17) | Owner decision (2026-09-22; master spec "Details"): options/end provision/event names no longer project metadata — keep PI, REK, main supporting institution (REQ-UI-012, REQ-DB-006). |
| DEV-UI-7 | Layout, style, and client-side data binding adopt the historic FIONA reference app | Owner decision (master spec "Details", `assets/table_based_authentication_plus_user_management/`): use the reference's layout/style and "pull JSON → populate rendering targets" interfacing, realized in Bootstrap 5.3.x + vanilla ES2020 and subordinate to the fixed conventions (no framework/JQuery, server-side UI strings, safe rendering, CSRF, PHP-only browser boundary) (REQ-UI-032, REQ-TECH-025) |
