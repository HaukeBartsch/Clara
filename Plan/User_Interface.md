# User Interface Plan

This document outlines the key views and layout for the clinical study management system, built using JavaScript, HTML, and Bootstrap, and served by the PHP web application. The interface exclusively uses the API to create projects, edit the setup of a project, and to store values into fields for projects.

## Layout (three shells — REQ-UI-046, DEV-UI-14)

A left panel appears only where it switches between sections. The **project overview** (§1) is a single panel between header and footer; the **project page** (§3) and the **Control Panel** (§2) each carry their own left panel with the right-hand panel showing the selected entry. One header (brand, project breadcrumb, and the `is_admin` **Control Panel** button) and one footer (language, theme, two-factor, password, sign out) are shared by all three; selecting a panel entry is plain navigation to that route (`Design/User_Interface_Design.md` §2.4).

## Key Views

### 1. Project overview
- Purpose: Start page after login; overview of accessible projects — information, not navigation, so it has **no left sidebar** (REQ-UI-009/046).
- Components: A table with one row per project the user has access to (administrator group or project membership) and quick stats in columns (record, instrument, field counts per project). Selecting a project's **name** opens that project's page (§3), which lands on its first available section. The header shows the **Control Panel** button for `is_admin` users only.
- Not a requirement today: "recent activity" and "notifications". An earlier draft of this plan listed them as dashboard components; nothing in the requirements or design asks for them and no API supplies them, so they are out rather than pending. Re-introducing either needs a `REQ-UI-*` entry first (with an endpoint behind it), not an implementation shortcut.

### 2. Control Panel (`is_admin`)
- Purpose: Administration for authenticated and authorized (administrator) users, on **one page** reached from the header button — all installation-administration settings live here and nowhere else (REQ-UI-047).
- Components: a left panel with the sections **Users**, **Projects**, **Audits**, **Translations**, **Settings**; the right-hand panel shows the section selected there. Project-scoped administration (assignments, roles, data access groups) belongs to the project page (§3), not to this page.
    - User accounts: Create (enable) and manage normal user accounts; show each account's two-factor method and offer an administrator reset for lost devices (GD-21, REQ-UI-011/039).

#### Login and two-factor (REQ-UI-007/038/039/042)
- The login page leads with the **authentication-source name picker** ("Hospital 1", "Hospital 2", …; skipped when only one name is configured; nothing is preselected and nothing shows below it until a name is chosen — choosing applies at once, so the page requires JavaScript, DEV-UI-13). Under the selected name it shows the provider buttons and/or the email+password form for that name's sources — a credential submit races them in parallel, first "login ok" wins (master spec "Authentication order", REQ-AUTH-063…067). When the account has a second factor configured, a follow-up step asks for the code (authenticator app or delivered by email, with resend and recovery-code entry). No session content is reachable between the steps. With the installation-wide mandate on (`AUTH_REQUIRE_2FA`), accounts without a method are taken through enrollment before login completes.
- A self-service account page lets each user enable TOTP (QR + manual key, confirm with one code, recovery codes shown once) or email, and disable it with a current code.
    - Projects: Create new projects (name, organization = main supporting institution, PI name and email, data manager, REK/IRB number, REK start/end dates, start/end dates, participant naming pattern) and edit project metadata. The option flags, the end provision, the end-user-contract confirmation, and an initial-events list are **absent from the form** (GD-17, REQ-UI-012); creation makes the project with arm 1 only, and events are added afterwards in the project's Setup page.
    - Assignment: Assign users to projects given a role (data-manager, data-entry, controller, or a custom role). Role-less members have full permissions.
    - Roles: Create additional roles with a mixture of permissions.

### 3. Project Workspace
- Purpose: Main hub for a specific study, shown when the user selects a project on the project overview. A left panel lists the project's functions and the right-hand panel shows the selected one (REQ-UI-017/046).
- Components:
    - Left panel entries, each present only when permitted: **Overview**, **Setup**, **Design**, **Record Status Dashboard**, **Export**, and for administrators **Members**, **Roles**, **Groups**. Entering the project opens the first available entry after Overview — Setup for a member who may change the setup, otherwise Record Status Dashboard for a member with data access, otherwise Overview.
    - Overview: Number of records, instruments, and fields in the project; the read-only project metadata; the current project mode (development | production | analysis) as a badge. Installation admin users (is_admin) can change the mode; a project's own admin sees the badge only: development to production asks whether previously stored data is kept or deleted; other transitions confirm that all data is kept. The control is offered only while no staging set is open.
    - Staging (production mode): Setup and Design pages show staging controls - start staging, a persistent "changes are staged" banner with commit/discard, and a commit dialog that lists breaking changes (each with its reason) and requires acknowledgement. In development mode setup edits apply directly; in analysis mode they do too, but a breaking change shows the same warning dialog and saves only on confirmation (REQ-UI-037).
    - Sections (presented in the left panel only if the user's role/permission allows them):
        - Setup (permission "project_admin"): Add/remove arms, instruments, events, and mappings between them.
        - Design: Create a new instrument, edit fields in an existing instrument.
        - Record status dashboard: Show the table of records and instruments by event.
        - Export: Export project data as CSV or JSON at the acting user's export level, with an arm selector and a visible sensitivity badge (REQ-UI-020). The applied level for a multi-arm export is the **lowest (most protective)** of the selected arms; deselecting an arm raises it for that download (REQ-EXP-003).

### 4. Instrument Designer
- Purpose: Edit an instrument's data dictionary.
- Components: Select an instrument; edit, add, and change its fields; reorder the fields; field properties (name with a warning after 26 characters, label, type, choices, validation, required, personal-information flag).
- The instrument list of a project can be reordered; the record field of the first instrument automatically becomes the record_id field for the project.
- A mapping page shows the instrument-by-event mapping (table with checkboxes) for each arm. A single arm is assumed to start with.

### 5. Data Entry Form
- Purpose: Collecting clinical data. In analysis-mode projects the form is read-only (no submit control); the survey page shows a closed state.
- Components:
    - Dynamic rendering of fields (Text, Dropdowns, Radio buttons, Matrix).
    - Real-time validation feedback (client-side JavaScript + HTML5; authoritative validation in the API).
    - A completion control as the always-last field of each instrument's form: set or clear this record/event/instrument's "finished" state, which drives the green marker on the status dashboard (REQ-UI-036). On survey instruments the same trailing field shows the completed state read-only, filled in automatically (GD-9).

### 6. Record Status Dashboard
- Purpose: Overview of data completion for a project.
- Components: Lists all record_ids in a project with their instruments sorted by event and ordered based on the order of instruments in each arm. Each instrument is rendered with a small graphic in one of **three** states — grey (no field has a value), amber (some value present), green (the user marked the instrument finished at the end of its form) (REQ-UI-019/036, REQ-API-074/110). Grey and amber are derived from the stored values; only green is stored (or automatic on survey instruments), so the indicator can never claim completion the data contradicts (REQ-API-074, GD-9).

## UI Framework
- Framework: Bootstrap
- Themes: The page renders with the standard Bootstrap stylesheet or one installed theme file (`darkly`, `yeti`); the installation default comes from configuration (`UI_THEME`), each user may override it beside the language selector, and "Default" returns to the installation theme (GD-26).
- Description: Used for responsive layout and pre-built components (forms, buttons, modals) on plain JavaScript and HTML.
