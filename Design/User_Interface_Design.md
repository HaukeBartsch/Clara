# User Interface — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/User_Interface_Requirements.md`
**Date:** 2026-09-22

## 1. Purpose and Conventions

Fixes the *shape* of the web UI: the PHP route set, the page inventory, the layout of every page, the components on each page, and the client-side interaction behavior. The requirements document fixes *what* (which views exist and what is gated); this document fixes how they are rendered and how the user operates them.

Not fixed here, and fixed by the documents referenced:

- Data contracts (every request/response shape): `API_Endpoints_Design.md`.
- Value rules, expression grammars, and evaluation semantics: `Data_Validation_Design.md`.
- Session, trust model, login/logout sequences, CSRF: `Authentication_Authorization_Design.md`.
- Persistence (data dictionary, survey links, DAG, i18n tables): `Database_Schema_Design.md`.
- Stack, repository layout, Bootstrap version, CSP: `Technology_Stack_Design.md`.

Common conventions (REQ-UI-001…008), binding on every page:

- **Stack** (REQ-UI-001, REQ-TECH-001): plain HTML + vanilla ES2020 + vendored Bootstrap 5.3.x (CSS + bundle JS only); no frontend framework, no build step. Pages are rendered by the PHP layer, which delegates **all** data access to the API (REQ-TECH-006). The only JavaScript file of the application is `web/assets/app.js` (`Technology_Stack_Design.md` §4).
- **Browser boundary** (REQ-UI-002, GD-1): the browser talks only to the PHP routes of §2 and never to `/api/v1/*` or the data API directly.
- **Permission gating** (REQ-UI-003, REQ-AUTH-027): a page, section, or action is present **only** when the acting user's effective permissions allow it — "hidden" means absent from the DOM, not merely disabled. Gating is computed server-side at render time; the API re-checks on every call (REQ-AUTH-033), so client-side gating is presentation, never enforcement.
- **Escaping** (REQ-UI-004): stored free-text values are the only content rendered as HTML — and only because they were sanitized to the allowlist at store (`Data_Validation_Design.md` §5.2). **All other** content (choice values, field labels, record names, user names, audit fields, translated strings) is escaped server-side (REQ-TECH-020). The `Content-Security-Policy` header is sent on every response (REQ-TECH-020).
- **CSRF** (REQ-UI-005, REQ-AUTH-037): every state-changing browser request is a `POST` (form or fetch) carrying the per-session `csrf_token` — hidden `csrf_token` form field for forms, `X-CSRF-Token` header for fetch calls.
- **Language** (REQ-UI-008, GD-12): English is the default and fallback; `nb` and `nn` are the first targets. Translations are applied **server-side at render time** — no i18n JavaScript library (REQ-TECH-001); §9 covers how JavaScript-originated messages are translated.
- **Time** (GD-7, GD-16): **system** timestamps (audit, history, account fields) are displayed as UTC, in the `YYYY-MM-DD HH:MM:SS` form the API returns. Clinical date/date-time **values** are displayed **exactly as stored** — canonical form **including the collection timezone offset** (e.g. `2026-03-01+01:00`, `2026-03-01 09:30+01:00`); the UI performs no locale reformatting of stored data and no timezone conversion (resolves ASM-UI-1). When the user enters a date/date-time in the data-entry form, the form submits it as typed (the field's `validation_format`) and the browser's timezone is sent as the collection zone (GD-16, REQ-VAL-041 — the PHP layer resolves the browser zone to the offset and passes `tz`).
- **Layout** (master spec, "User interface details"): a **sidebar + content-panel** shell — the navigation lives in a left sidebar; selecting a function (e.g. *Setup*) renders the corresponding page in the right-hand content panel (§2.4). This is a shared multi-page layout, not a single-page app: each route of §2.1 renders the same sidebar and swaps the panel content (consistent with REQ-UI-001 — no framework). The shell, visual style, and interaction style follow the historic FIONA reference in `assets/table_based_authentication_plus_user_management/` (master spec, "Details"; REQ-UI-032, REQ-TECH-025): sidebar + panel, `table-sm` tables, responsive layout, and a client that populates data regions from JSON — realized in Bootstrap 5.3.x and vanilla ES2020.

## 2. Application Shell and Routes

### 2.1 Route set (normative — the complete set of browser-reachable routes)

| Route | Page | Gating (server-side at render) | API calls (PHP, server-side) |
|---|---|---|---|
| `GET /login` | login incl. source-name picker and the two-factor step (§2.2) | public (pre-auth `tfa_pending` state for the challenge, GD-21) | — (PHP named-source selection + OAuth2/LDAP/local flow; `Authentication_Authorization_Design.md` §2.9/2.1/2.2/2.7) |
| `GET /auth/callback` | OAuth2 redirect | public | `POST /api/v1/auth/login` on success |
| `POST /logout` | — (redirect to `/login`) | any authenticated | `POST /api/v1/auth/logout` **before** session destruction (REQ-UI-007, `Authentication_Authorization_Design.md` §2.4) |
| `GET /` | dashboard (§4) | any authenticated | `GET /api/v1/projects` |
| `POST /lang` | language switch (redirect back) | any authenticated | `PUT /api/v1/users/me/ui-language` |
| `POST /theme` | theme switch (redirect back, §3.8) | any authenticated | `PUT /api/v1/users/me/ui-theme` (GD-26) |
| `GET /account/two-factor` | two-factor settings (§2.5) | any authenticated | `GET /api/v1/users/me/tfa`, `POST /api/v1/users/me/tfa/*` (GD-21, REQ-UI-039) |
| `GET\|POST /account/password` | change own password (§2.6) | any authenticated | `PUT /api/v1/users/me/password` (GD-23, REQ-AUTH-061) |
| `GET\|POST /password-reset` | forgot-password request (§2.6) | public — no session | `POST /api/v1/auth/password-reset/request` (GD-23, REQ-AUTH-062) |
| `GET\|POST /set-password?token=…` | set password via invite/reset token (§2.6) | public — valid token, no session | `POST /api/v1/auth/invite/complete`, `POST /api/v1/auth/password-reset/complete` (GD-22/GD-23, Sequence H) |
| `GET /admin/users` | user accounts (§5.1) | `is_admin` | `GET/POST /api/v1/users`, `PUT /api/v1/users/{id}` |
| `GET /admin/projects` | project create/edit (§5.2) | `is_admin` | `POST /api/v1/projects`, `GET/PUT /api/v1/projects/{id}` |
| `GET /admin/audit` | audit view (§5.6) | `is_admin` or member of a project (REQ-API-078) | `GET /api/v1/audit` |
| `GET /admin/i18n` | translation management (§5.7) | `is_admin` | `GET /api/v1/i18n/strings?language=`, `PUT /api/v1/i18n/strings` |
| `GET /admin/settings` | system settings (§5.8) | `is_admin` | `GET/PUT /api/v1/settings` |
| `GET /projects/{id}` | project workspace (§6.1) | project visibility (REQ-API-007) | `GET /api/v1/projects/{id}` |
| `GET /projects/{id}/setup` | setup page (§6.2) | `project_admin` | arms / events / instruments / mapping endpoints |
| `GET /projects/{id}/design` | instrument designer (§7.1) | `project_admin` | instruments / fields endpoints |
| `GET /projects/{id}/design/instruments/{iid}` | field editor (§7.2) | `project_admin` | `GET/POST …/instruments/{iid}/fields`, `PUT/DELETE …/fields/{fid}`, `PUT …/fields/order` |
| `GET /projects/{id}/record-status` | record status dashboard (§6.3) | data access ≥ `read_only` | `GET …/record-status` |
| `GET /projects/{id}/members` | members and tokens (§5.3) | `is_admin` | `GET …/users`, `PUT …/users/{uid}`, `GET …/users/{uid}/token` (self), `PUT …/users/{uid}/data-access-groups` |
| `GET /projects/{id}/roles` | role editor (§5.4) | `is_admin` | `GET/POST …/roles` |
| `GET /projects/{id}/groups` | data access groups (§5.5) | read: data access ≥ `read_only`; create/delete: `project_admin` | `GET/POST …/data-access-groups`, `DELETE …/data-access-groups/{gid}` |
| `GET /projects/{id}/records/{record}` | data entry / record view (§8) | data access ≥ `read_only` (per-action levels per §8) | `GET …/instruments/{iid}/fields`, `GET …/records/{record}/history`, token fetch + data-API import/delete (`§8.6`) |
| `GET /projects/{id}/export` | export (streams the response) | a non-`export_none` level per arm (REQ-UI-020) | `GET …/export` (streamed, REQ-TECH-011) |
| `GET /survey/{link}` | public survey page (§8.8) | none — no session, outside the login (GD-9, DEV-UI-1) | data API `content=metadata` + `content=record&action=import` with the link token (REQ-API-083) |

There are no other browser-reachable routes. State-changing browser requests are `POST`s to the same routes with a `?action=<name>` parameter (or a dedicated `POST` route where noted); the table lists the read route each page is bound to. The browser never sees `/api/v1/*` (REQ-UI-002).

### 2.2 Login page (`GET /login`, REQ-UI-007)

- **Source-name picker** (REQ-UI-042, master spec "Authentication order"): the page leads with the distinct set of configured authentication-source names ("Hospital 1", "Hospital 2", … — from `OAUTH2_N_NAMES`, `LDAP_SERVER_N_NAMES`, `LOCAL_LOGIN_NAMES`, REQ-CFG-032) as a radio/button group; the selection is stored in the session (`auth_source_name`, `Authentication_Authorization_Design.md` §3) and everything below re-renders for it. With exactly one distinct name the picker is skipped; changing the selection clears any entered credentials before re-render (REQ-UI-042).
- **What renders under the selected name** (`Authentication_Authorization_Design.md` §2.9):
  - a single OAuth2 source and no credential sources → PHP initiates that provider's Sequence A redirect directly (REQ-AUTH-066);
  - several OAuth2 sources → one button per provider — "Sign in with `<provider name>`" — each initiating its own Sequence A;
  - local and/or LDAP sources → the **email + password form** (on a first installation without an IdP it is the **only** path, GD-18, REQ-AUTH-051). Submitting dispatches the credential race for the selected name — the local verify of Sequence F and the LDAP binds of Sequence B in parallel, first "login ok" wins (`Authentication_Authorization_Design.md` §2.9) — there is no sequential fallback to present or to wait through. The password travels only browser→PHP (TLS) and PHP→the sources under the selected name; it is never stored in plaintext and never logged (REQ-AUTH-036, REQ-AUTH-063).
- **"Forgot password?"** — a translated link beside the form to `/password-reset` (§2.6, GD-23, REQ-AUTH-062). Meaningful only for local accounts; the request page answers generically so the link discloses nothing.
- Failure presentation: a single translated error line per the API reason — provider unavailable / state mismatch / **bad password** / bad credentials (LDAP) / **account disabled** (incl. "disabled after inactivity — an administrator must re-enable") / **account expired** ("validity period ended — an administrator must extend it") — no internals. Rate-limit rejection (429) shows the lockout duration (REQ-AUTH-035).
- **Two-factor step** (GD-21, REQ-UI-038): on 401 `mfa_required` the page switches to a second panel — one code field with method-specific help ("Enter the code from your authenticator app" / "We sent a code to your email address" + a resend action, rate-limited) and an "use a recovery code" toggle. PHP holds the first-factor success in the pre-auth `tfa_pending` session (`Authentication_Authorization_Design.md` §2.7); no other page is reachable while it stands. 401 `bad_mfa_code` re-shows the panel with the failure line; on success the response completes the login exactly as without a second factor.
- **Mandated enrollment** (GD-21, REQ-AUTH-059): on 401 `tfa_enrollment_required` (`AUTH_REQUIRE_2FA` on, account has no method) the page shows the enrollment wizard of §2.5; completing it resumes the login.
- Session inactivity timeout: any request with an expired session redirects to `/login` (REQ-UI-007, REQ-AUTH-015).

### 2.3 No-access page

A user who is neither an administrator nor a member of any project MUST be shown an information page, not an empty dashboard (REQ-UI-006, REQ-AUTH-028). Presentation:

- Title: "No projects yet" (translated); a short explanation that access is granted when an administrator adds them to a project (or grants the administrator flag).
- For `is_admin` users with no projects: the same page shows the administration entry points (create project — §5.2, manage users — §5.1) instead of the explanation.
- The sidebar (§2.4) is rendered with only the sections the user may use; the page is the right-hand panel.

### 2.4 Application layout — sidebar + content panel (master spec, "User interface details")

All authenticated pages (and the public survey page, §8.8, which renders a reduced shell) use a two-pane Bootstrap layout:

```
┌────────────────────────────────────────────────────────────┐
│  brand bar (project name when in a project context)        │
├──────────────┬─────────────────────────────────────────────┤
│  sidebar     │                                             │
│  (nav)       │              content panel                  │
│              │         (the page of §2.1, rendered         │
│  …           │          right-hand; the page's own         │
│              │          headings, forms, tables)           │
├──────────────┴─────────────────────────────────────────────┤
│  footer (language selector, user name, Sign out)           │
└────────────────────────────────────────────────────────────┘
```

- **Selecting a sidebar item loads the corresponding page in the content panel** (master spec). Mechanically this is plain navigation — a `GET` to the route of §2.1 — rendered into the same shell; there is no client-side page switching and no SPA (REQ-UI-001). The active item is marked `active` in the sidebar.
- **Sidebar sections** (each section absent from the DOM unless allowed — REQ-UI-003):
  1. **Projects** (any authenticated user with ≥ 1 visible project): a link to the dashboard (`/`) and one entry per visible project (name + organization) to its home page (`/projects/{id}`).
  2. **Administration** (`is_admin` only): Users (`/admin/users`), Projects (`/admin/projects`), Audit log (`/admin/audit`), Translations (`/admin/i18n`).
  3. **Project context** — shown only while the user is on a page of a specific project (the brand bar shows that project's name): Setup, Design, Record status, Export (gated per §6.1), and — for `is_admin` — Members, Roles; — for `project_admin` or better — Groups. Selecting one of these loads that page in the panel (the master spec's "selecting different functions (like setup) should load the corresponding page in the right hand panel").
  4. **Account**: the language selector (§9) and the theme selector (§3.8, GD-26), a "Two-factor authentication" link (`GET /account/two-factor`, §2.5 — GD-21), a "Password" link (`GET /account/password`, §2.6 — shown only when the account has a local credential, GD-23), and a "Sign out" action (`POST /logout`, CSRF token, REQ-UI-007).
- **Responsive collapse** (REQ-UI-008): below the Bootstrap `lg` breakpoint the sidebar collapses to the standard off-canvas/overlay pattern (Bootstrap navbar + offcanvas); no mobile-specific optimization beyond Bootstrap defaults (ASM-UI-1).
- The login page (§2.2), the public password pages (`/password-reset`, `/set-password`, §2.6), and the public survey page (§8.8) render **without** the sidebar (no session, no navigation) — each is a single centered form panel (DEV-UI-1 pattern).

### 2.5 Two-factor settings (`GET /account/two-factor`, REQ-UI-039)

Self-service page in the shell, reachable by every signed-in user (also rendered standalone inside the mandated-enrollment flow of §2.2 before a session exists). Data: `GET /api/v1/users/me/tfa`.

- **Current method** badge: off / authenticator app (`totp`) / email (`email`), with enrollment date.
- **Enable TOTP**: button → the API's returned secret rendered as a QR code (client-side from `otpauth_uri`, no external service) plus the manual-entry key with a copy affordance; one confirmation-code field; on success the **recovery codes** appear exactly once in a monospace block with a copy button and an explicit "I saved them" acknowledgement (`POST …/totp/enroll`, `POST …/totp/confirm`).
- **Enable email**: "Send code" → six-digit confirmation field (+ resend, rate-limited); the option is absent when SMTP is not configured — `POST …/email/start` 409 maps to a hidden control server-side (REQ-AUTH-057). Recovery codes shown once as above.
- **Disable**: asks for a current code or recovery code before `POST …/tfa/disable`; explains that the mandate re-prompts enrollment at the next login when on.
- The secret and codes never re-render after their one-time display; reload shows status only (REQ-AUTH-056). All actions CSRF-protected (REQ-UI-005); strings translated server-side (REQ-UI-008).

### 2.6 Password change, reset, and invite completion (GD-22/GD-23)

**Change own password (`GET /account/password`, authenticated, in the shell).** Shown in the Account sidebar only when the account has a local credential (GD-23). One form: current password, new password + repeat; submits `PUT /api/v1/users/me/password` (CSRF, REQ-UI-005). A wrong current password shows the generic bad-password line (`API_Endpoints_Design.md` §4.2); success shows a confirmation and clears the fields. The account's **email address is displayed read-only** — identity changes go through an administrator (REQ-AUTH-061). Existing sessions stay valid (DEV-AUTH-13).

**Forgot password (`GET /password-reset`, public, standalone panel).** One email field; submits `POST /api/v1/auth/password-reset/request`; the page always shows the same translated line — "If that address has an account with a local password, a reset link has been sent" — never revealing whether it did (REQ-AUTH-062). Rate-limit rejection (429) shows a retry-soon message.

**Set password (`GET /set-password?token=…`, public, standalone panel).** Reached from the invite or reset email. Renders two new-password fields (+ repeat, client-side match check plus authoritative server check); on submit PHP routes by purpose — `POST /api/v1/auth/invite/complete` or `POST /api/v1/auth/password-reset/complete`. Any token failure (unknown, expired, consumed) shows one generic translated line with a link back to `/login` and, for the reset case, an offer to request a new link (`invalid_setup_token`, `API_Endpoints_Design.md` §4.2). Success: invite → "Password set — sign in" link to `/login`; reset → same. The token never re-renders into the page beyond the hidden form field carrying it; no session is established by this page (Sequence H, `Authentication_Authorization_Design.md` §2.8).

## 3. Common Components and Rendering Rules

These rules apply on every page of §2.1 and are the single implementation of the REQ-UI-001…008 conventions.

### 3.1 Permission gating (REQ-UI-003, REQ-AUTH-027)

- A single PHP helper resolves, per request, the acting user's effective permissions for the target project (via the API — the API re-checks on every call, REQ-AUTH-033) and the template renders conditionally. "Hidden" = **absent from the DOM** — no `display:none`, no disabled control.
- Consequence table (normative): a control whose permission is missing MUST NOT be emitted — not as a disabled button, not as a link to a 403 page. This holds for sidebar entries (§2.4), page action buttons, and per-row actions (e.g. the per-member token rotation, §5.3).

### 3.2 Escaping and free-text (REQ-UI-004, REQ-TECH-020)

- Server-side escaping for **all** content: choice values, field labels, record names, user names, audit payloads, translated strings — every interpolation into HTML.
- **Exception** (the only one): stored free-text values render as HTML — they are allowlist HTML because the store sanitized them (`Data_Validation_Design.md` §5.2). They appear in: data-entry form values, per-field history (REQ-UI-026), record view, audit details.
- The `Content-Security-Policy` header is sent on **every** response (REQ-TECH-020) and is the backstop, not a substitute, for escaping.

### 3.3 State-changing requests (REQ-UI-005, REQ-AUTH-037)

- Every mutation is a browser `POST` (form submit or `fetch`) carrying the per-session `csrf_token`: hidden `csrf_token` field for forms, `X-CSRF-Token` header for `fetch`. No `GET` performs a write.
- The PHP route validates the CSRF token first; on failure it renders the page again with an error alert and no API call is made.

### 3.4 API errors → user messages (REQ-API-006, REQ-API-007)

The PHP layer maps the stable `error` code of `API_Endpoints_Design.md` §4.2 to a **translated** user message; the API's `message` is shown only when it is a design-time reason the requirements mandate displaying (validation reasons, REQ-VAL-029). Mapping:

| API `error` / status | User presentation |
|---|---|
| `invalid_request` (400) | "The form contains invalid values" + the API `message` when it names the offending attribute |
| `validation_error` (400) | the `message` verbatim (design-time expression/dictionary reason — REQ-VAL-029, REQ-UI-021/022) |
| `forbidden` (403) | "You do not have permission for this action" — uniform; never "project not found" (REQ-API-007) |
| `conflict` (409) | the `message` (duplicate name, still-in-use resource, §5.5 group deletion) |
| `not_found` (404) | "The object does not exist or you do not have access" (uniform with 403 for protected resources) |
| `account_not_found` / `bad_password` / `account_disabled` / `account_expired` (login) | the login-page failure line of §2.2 (reason-specific text; auto-disabled and expired accounts point the user to an administrator) |
| `mfa_required` (login, GD-21) | not an error — switches the login page to the two-factor panel (§2.2) |
| `bad_mfa_code` (login) | "That code is wrong or has expired" on the two-factor panel; resend stays available until the lockout line replaces it (REQ-AUTH-058) |
| `tfa_enrollment_required` (login) | not an error — opens the enrollment wizard (§2.5, §2.2) |
| `service_token_invalid` / `internal` (500) | a generic failure line + the operator is notified by the application log — never internals (REQ-API-006) |
| data-API `import_record_id = 0` | the per-field validation detail from `import_form_name` (`Data_Validation_Design.md` §3 codes), §8.6 |

Success confirmations (created/updated/removed) are one translated alert line at the top of the panel; they name the object (e.g. "Event `follow_up` updated") — never identifiers of other users beyond what the page already shows.

### 3.5 Confirmations and destructive actions (GD-3, REQ-UI-015/027)

- Destructive or hard-to-reverse actions open a Bootstrap **modal** stating the consequence, requiring an explicit confirm click (which carries the CSRF token): delete record/scoped values (§8.7), remove member (§5.3), delete group (§5.5), delete field with stored values (§7.2), revoke survey link (§8.7), delete arm/event where supported.
- Token display (add/rotate, §5.3): the new token appears **exactly once** in a read-only `<input readonly>` with a **copy** button and a warning line that it will not be shown again (REQ-API-055, REQ-UI-013). The admin's copy is the only copy — the member later self-serves their own copy per REQ-API-102 (§8.6).

### 3.6 Lists, pagination, empty states

- Paginated endpoints (audit, record history) render a Bootstrap table + "Next/Prev" controls carrying the opaque `cursor` and the active `limit` (`API_Endpoints_Design.md` §1); the URL query string carries `cursor`/filters so a page is reloadable (GET-only).
- Every list has a translated empty state that says what is empty and, when actionable, the one action that fills it (e.g. roles: "No roles yet — members without a role have full permissions (REQ-AUTH-022)").

### 3.7 Client-side data binding (REQ-UI-032, REQ-TECH-025)

The reference application's interfacing style (master spec, "Details"; `assets/table_based_authentication_plus_user_management/` — `/php/*.php` JSON endpoints, `js/all.js` fetch-and-populate) is adopted for the presentation layer, in vanilla ES2020:

- **Server renders the shell and static content.** Each route of §2.1 returns the page shell — sidebar, headings, forms, empty data containers — fully rendered, permission-gated (REQ-UI-003), and with UI strings translated (REQ-UI-008).
- **Client binds data regions.** `app.js` fetches JSON from the PHP endpoints (which proxy the API — REQ-TECH-006, GD-1) and populates data regions: list rows, table bodies, and `<select>` options. It writes with **safe DOM APIs** — `createElement`/`textContent` for all content except the stored free-text allowlist HTML (the §3.2 exception); `innerHTML` with user data is forbidden (REQ-UI-004, REQ-TECH-020).
- **No page switching.** Data binding fills targets within the current page; navigation is still a full `GET` to a §2.1 route (no SPA — REQ-UI-001, REQ-TECH-001).
- **Mutations are unchanged.** Every state-changing request is still a CSRF-protected `POST` to a PHP route (REQ-UI-005, §3.3); after a successful mutation the client re-fetches the affected data region rather than mutating the DOM by hand.
- **Subordinate to the fixed conventions.** Where the reference diverges — its Bootstrap 2.x, jQuery, JSON-file store, and md5-in-transport password — it is **not** adopted (REQ-TECH-001, REQ-TECH-020/024, REQ-AUTH-036).

### 3.8 Theme rendering and selection (GD-26, REQ-UI-040/041)

- **One stylesheet per page.** The PHP shell links exactly one Bootstrap stylesheet in the `<head>`: the standard `vendor/bootstrap/bootstrap.min.css`, or the selected theme's `vendor/bootstrap/themes/<name>/bootstrap.min.css` (vendored per `Technology_Stack_Design.md` §2/§4; themes are full replacements — never both, never layered). All other assets (bundle JS, Tabulator, Geist) are theme-independent and unchanged.
- **Resolution at render time.** The effective theme is the acting user's override (`users.ui_theme`, from the session's user object) when set, else the installation default `UI_THEME` (REQ-CFG-031). Only installed theme identifiers resolve; the value never comes from the request, so no arbitrary path lands in the `href` (REQ-TECH-027). The login page, the public password pages (§2.6), and the survey page (§8.8) have no user context and render with the installation default.
- **Theme selector** (sidebar Account section, §2.4; REQ-UI-041): a `<select>` beside the language selector listing the installed themes (`Standard`, `Darkly`, `Yeti` — translated display names) plus **"Default"** (= follow the installation theme, i.e. clear the personal override). Choosing one is a CSRF-protected `POST /theme` (form submit, redirect back like `POST /lang`) → `PUT /api/v1/users/me/ui-theme` (REQ-API-122); the new stylesheet applies **from the next page load** — the current page keeps its already-rendered theme.
- **Markup is theme-neutral.** No page structure or component changes with the theme (REQ-TECH-027): `table-sm`, the offcanvas collapse (§2.4), modals, and badges render from the same markup under every installed theme. The CSP of §3.2 already allows only same-origin styles — a theme introduces no new source.

## 4. Dashboard (`GET /`)

The start page after login (REQ-UI-009). Data: `GET /api/v1/projects` (visible projects only — REQ-API-049, REQ-API-007).

```
┌───────────────────────────────────────────────────────────────────────────┐
│  <user display name>                                                      │
│  Projects (n)                          [Administration] (adm)             │
├───────────────────────────────────────────────────────────────────────────┤
│  8DISC                 NAT EU      42 records · 5 instr. · 128 fields  →  │
│  EMIT-23               OTHER       128 records · 1 instr. · 146 fields →  │
└───────────────────────────────────────────────────────────────────────────┘
```

- One row/card per visible project: name, organization, and the quick statistics exactly as returned (`record_count`, `instrument_count`, `field_count` — REQ-UI-009, REQ-API-049). The row links to the project home (`/projects/{id}`, §6.1).
- **[Administration]** entry point is present only for `is_admin` (REQ-UI-003/009) and links to the §5 pages.
- **Data access groups** (REQ-UI-010): for a member with one or more assigned groups, the dashboard shows the **currently active group** (per project, where the member is assigned to ≥ 1 group) and a switch control listing the member's assigned groups. Switching `POST`s to the PHP route → `PUT /api/v1/projects/{id}/active-data-access-group` (REQ-API-090); the switch takes effect on the **next** data page load (the current page's data was already scoped). A member without any assignment sees no group control — they see all records of the project (GD-10).
- No projects and not `is_admin` → the no-access page (§2.3).

## 5. Administration Interface

The pages of §5.1/§5.2/§5.6/§5.7 are global (`is_admin`); §5.3/§5.4/§5.5 are project-scoped and are also linked from the project home sidebar (§2.4, §6.1 — master spec: the project home offers "administer users in the project").

### 5.1 User accounts (`GET /admin/users`, `is_admin`, REQ-UI-011)

Data: `GET /api/v1/users` (the full user object, `API_Endpoints_Design.md` §4.3/§4.4). Table columns: `email`, `display_name`, `enabled` (badge), `is_admin` (badge), `auth_source` (`oauth2`/`ldap`/`local`), **`last_login_at`** (UTC; "never" when `null`), **`valid_until`** ("indefinite" when `null`), the derived **status** badge — `active` / `disabled` / `expired` / `auto_disabled` (GD-19, REQ-AUTH-052/053) — and the **two-factor method** badge `off` / `totp` / `email` (GD-21, REQ-API-116). An `auto_disabled` row is visually distinct and shows the inactivity reason ("no login for more than `AUTH_INACTIVITY_LIMIT_DAYS` days — re-enabled by an administrator").

| Action | Control | API | Notes |
|---|---|---|---|
| create account | form: `email` + `display_name` + **validity in days** (`0` = indefinite) + optional **local password** (repeat field; the value is shown once as masked text, never echoed back) | `POST /api/v1/users` (`valid_days`, `password`) | a disabled account with the same email is **re-enabled** by the same call (REQ-API-047) — the form therefore doubles as the re-enable action; `valid_days 0` → `valid_until = null` (REQ-AUTH-052); when created **without** a password the row offers "Send invite" as the next action (GD-22, REQ-AUTH-060) |
| disable / re-enable | row button, confirmation modal on disable (naming the consequence for that user's tokens) | `PUT /api/v1/users/{id}` with `enabled` | idempotent; disabling takes effect at call time for that user's tokens (REQ-API-048); **re-enabling an auto-disabled account resets the inactivity clock** (REQ-AUTH-053) |
| extend validity | row action (number input, days; `0` = indefinite) | `PUT /api/v1/users/{id}` with `valid_days` | re-sets `valid_until`; restores access for expired accounts (REQ-AUTH-052) |
| set / reset local password | row action (password + repeat; empty = clear the local password) | `PUT /api/v1/users/{id}` with `password` | stored only as a bcrypt hash; never shown, never logged (REQ-AUTH-050, REQ-AUTH-036); `auth_source` then reports `local` after the next login (REQ-AUTH-005) |
| send invite | row action "Send invite" (accounts on the local path; confirmation modal naming the target address) | `POST /api/v1/users/{id}/invite` | emails a single-use set-password link so the user chooses their own first password (GD-22, REQ-AUTH-060); re-sending invalidates the previous link; hidden when SMTP is not configured (`smtp_not_configured`, REQ-CFG-028) — direct "set / reset local password" is then the only path; audit `user_invited` |
| reset two-factor | row action with confirmation ("the user will lose their enrolled method and recovery codes") | `POST /api/v1/users/{id}/tfa/reset` | clears method, secret, codes (GD-21, REQ-AUTH-059); audit `tfa_reset`; under `AUTH_REQUIRE_2FA` the user re-enrolls at next login |

The `is_admin` flag is set by the bootstrap mechanism (GD-4, REQ-AUTH-007) — phase 1 offers no UI toggle for it (out of REQ-UI-011's scope).

### 5.2 Projects — create and edit (`GET /admin/projects`, `is_admin`, REQ-UI-012)

**Create** — a single form covering the simplified creation fields of REQ-DB-006 (GD-17), submitting `POST /api/v1/projects`:

- identity: `project_name` (unique — a 409 shows the conflict message, §3.4), `organization` (select of the REQ-DB-006 enumeration — **the main supporting institution**), `pi_name`, `pi_email`, `dm_name`, `dm_email`;
- ethics and time: `rek_number`, `rek_start_date`, `rek_end_date`, `start_date`, `end_date`;
- naming: `participant_names` (naming pattern — `8DISC[0-9][0-9][0-9]` or `0001_01`, REQ-DB-007).

The option flags, the end-user-contract confirmation, the end provision, and the initial-events list are **absent from the form** (GD-17, REQ-DB-032): the owner MAY record them later as data in an ordinary instrument (e.g. `DataTransferProjects`), and events are added in the project's Setup page (§6.2 B). Creation makes the project with **arm 1 only** (`API_Endpoints_Design.md` §4.5); the page then offers a link straight into the new project's setup to add its events.

On 201 the page offers a link into the new project's setup (§6.2).

**Edit** (`/admin/projects?edit={id}` → `PUT /api/v1/projects/{id}`): the same form pre-filled from `GET /api/v1/projects/{id}`; any subset may be changed (idempotent, REQ-API-042); metadata changes are audit-logged with old/new (REQ-API-052).

### 5.3 Members and tokens (`GET /projects/{id}/members`, `is_admin`, REQ-UI-013)

Data: `GET /api/v1/projects/{id}/users` (+ `GET …/data-access-groups` for the assignment controls). One row per member: `email`, `display_name`, `role` (or "no role (full permissions)" — REQ-AUTH-022), `token_present`, `enabled`, data-access-group assignments with the active one marked.

| Action | Control | API | Notes |
|---|---|---|---|
| add member | form: user (email or id) + role select (the project's actual roles, possibly empty, plus **"no role"**) | `PUT …/users/{uid}` `{ "role": … }` | → 201 **with the new token** — shown once, read-only input + copy + warning (§3.5) |
| change role | row select + apply | `PUT …/users/{uid}` `{ "role": … or null }` | "no role" = full permissions (REQ-AUTH-022) |
| rotate token | row button, confirmation | `PUT …/users/{uid}` `{ "rotate_token": true }` | previous token invalid immediately (REQ-AUTH-030); new token shown once |
| remove member | row button, confirmation | `PUT …/users/{uid}` `{ "remove": true }` | token invalid immediately (REQ-API-055) |
| set group assignments | row checkboxes (assigned groups) + active-radio | `PUT …/users/{uid}/data-access-groups` | exactly one active when non-empty (§3.4 on 400) |

Token values never appear in the table itself (`token_present` only, REQ-API-005) and never in logs.

### 5.4 Role editor (`GET /projects/{id}/roles`, `is_admin`, REQ-UI-014)

Data: `GET /api/v1/projects/{id}/roles`. A list of the project's roles (name, `project_admin`, per-arm levels) plus a **create** form:

- `name` — unique within the project (409 on duplicate, §3.4); roles are not limited to presets (REQ-AUTH-020).
- `project_admin` — checkbox (GD-2).
- **Per arm** (one block per existing arm): a **data access level** select (`no_access | read_only | view_edit | delete | edit_survey_responses`) and an **export level** select (`export_none | export_de_identified | export_no_identifiers | export_full`); an arm left absent submits `no_access`/`export_none` (no implicit access, REQ-AUTH-019).
- The three example presets (`data-manager`, `data-entry`, `controller`) MAY be offered as one-click **starting points** that pre-fill the form — they are never enforced (REQ-AUTH-020).

Submit → `POST …/roles` (201) with the role object of `API_Endpoints_Design.md` §4.7.

### 5.5 Data access groups (`GET /projects/{id}/groups`, REQ-UI-015)

Data: `GET …/data-access-groups` (read: data access ≥ `read_only`; mutations: `project_admin`).

- **List**: `id`, `name`.
- **Create**: name form (unique per project — 409 `conflict` on duplicate, REQ-AUTH-043) → `POST …/data-access-groups`.
- **Delete**: confirmation modal **with the warning that deletion is rejected while records are still assigned** (REQ-API-088; the 409 message is shown per §3.4) → `DELETE …/data-access-groups/{gid}`. Deleting removes the group and its member assignments (REQ-API-088).
- The member-assignment and record-assignment actions live in §5.3 (members) and §8.8 (record view) respectively.

### 5.6 Audit view (`GET /admin/audit`, REQ-UI-016)

Data: `GET /api/v1/audit` — read-only (REQ-API-077). Presentation: a filter bar + paginated table (§3.6) + an expandable detail cell.

- **Filters** (query parameters, §3.6): `type` (`events`|`views`), `project` (**required** for non-admins — REQ-API-078; a select pre-populated with the acting user's projects, optional/all for `is_admin`), `user`, `event_type` (select from the catalog of `Audit_Logging_Design.md` §3), `from`/`to` (UTC date inputs).
- **Table columns**: `created_at` (UTC), `source` (`api`/`ui`/`system`), project, acting user (display name/email), `event_type` (the stable code), `target_record` where set, and a truncated `details` preview.
- **Detail**: the full `details` payload rendered as a read-only key/value list — **every value escaped** (§3.2; REQ-UI-004). Token values may appear (the trail is the sole sanctioned carrier, REQ-AUD-007) — they are displayed escaped and never copied into the page's other elements.
- Rows are reverse-chronological (REQ-API-077); there is **no** write control anywhere on this page (REQ-DB-024).

### 5.7 Translation management (`GET /admin/i18n`, `is_admin`, REQ-UI-030)

Data: `GET /api/v1/i18n/strings?language=<code>` (after `GET /api/v1/i18n/languages` for the language select).

- **Language select**: the enabled languages (code + display name).
- **Key table**: one row per translation key — `key`, current `text`, and a **missing** badge (`missing = true` → the key falls back to English at render, REQ-DB-031, REQ-UI-008). Missing keys are visually distinct (the requirement's "MUST be visible in the list").
- **Editor**: inline `text` editing per row (or a side form); empty `text` **removes** the translation (fallback to English). Save → `PUT /api/v1/i18n/strings` with `{ "language", "entries": [ { "key", "text" } ] }` (upsert; REQ-API-100).
- Translated strings are escaped on render (§3.2, REQ-UI-004).

### 5.8 System settings (`GET /admin/settings`, `is_admin`)

Data: `GET /api/v1/settings`; save → `PUT /api/v1/settings` (CSRF, §3.3; contract `API_Endpoints_Design.md` §4.22).

- **Rate limiting (REQ-UI-037)**: an enabled/disabled switch (`rate_limit_enabled`), a requests-per-minute number input per source IP (`rate_limit_rpm`, ≥ 1, default 600) and a blockout-minutes number input (`rate_limit_block_minutes`, 1–1440, default 10), with helper text naming the scope ("limits every caller IP on both the data API and the administration surface; an IP over its per-minute budget is refused for this many minutes; applies immediately after saving"). Invalid values are rejected by the API (400) and surfaced per §3.4; applied changes are audit-logged (`settings_updated`, REQ-AUD-027).

## 6. Project Workspace

### 6.1 Project home (`GET /projects/{id}`, REQ-UI-017)

Data: `GET /api/v1/projects/{id}` (full metadata + structure). Gating: project visibility (REQ-API-007).

- **Summary** (REQ-UI-017): record count, instrument count, field count, plus the project metadata block (name, organization, PI, REK number, dates — read-only display; editing goes to §5.2 for `project_admin`).
- **Action cards** — each present **only** when allowed (REQ-UI-003), and mirrored in the sidebar's project context (§2.4). This realizes the master spec's project-home option list (setup the project, design all instruments, create arms and events, assign instruments to arms and events, administer users, export):

| Card | Target | Required permission | Master-spec option |
|---|---|---|---|
| Setup | §6.2 | `project_admin` | i) setup the project; iii) create arms and events; iv) assign instruments to arms and events |
| Design | §7.1 | `project_admin` | ii) design all instruments |
| Record status | §6.3 | data access ≥ `read_only` | (participant overview) |
| Export | §6.4 | a non-`export_none` level on the arm (REQ-API-075) | vi) export |
| Members · Roles | §5.3 · §5.4 | `is_admin` | v) administer users in the project (match roles to permissions) |
| Groups | §5.5 | data access ≥ `read_only` (mutate: `project_admin`) | (record scope) |
| End provision | §6.5 | `is_admin` | (end of project — BR-009) |
| Mode | §6.6 | `is_admin` (the mode badge is visible to every member) | project modes (GD-20) |

### 6.2 Setup page (`GET /projects/{id}/setup`, `project_admin`, REQ-UI-018)

One page, four blocks. **Arm-dependent blocks are presented as tabs, one per arm, with arm 1 (`arm_1`) displayed by default** (master spec: "as arms will be used rarely, all arm dependent sections can be hidden using a tab-interface; the information of the first arm (arm_1) should be displayed by default"). Phase 1 starts with a single arm (REQ-DB-011), so the tab strip initially shows one tab; adding an arm adds a tab.

**Block A — Arms** (all arms; not tabbed): a list of arms (`arm_num`, `name`); **Add arm** form (`name`) → `POST …/arms`; **Remove** (confirmation) → `DELETE /api/v1/arms/{id}` — rejected with the 409 message while the arm still has events or data (§3.4, DEV-API-6).

**Block B — Events** (per arm, tabbed): a table of the arm's events — `event_name`, `unique_event_name` (derived `<label>_arm_<n>`, REQ-DB-011), `period` (the **timepoint**, days after baseline — blank when the event has **no timepoint**, GD-15), `safe_region_start`/`safe_region_end` (± days), `position`. **Add event** form (label, optional timepoint, safe region) → `POST …/events` (duplicate label in the arm → 409; a blank timepoint stores `period = null`). **Edit** row → `PUT /api/v1/events/{id}` (label change re-derives `unique_event_name`; collision → 409; clearing the timepoint makes the event user-orderable).

**Ordering (GD-15, REQ-DB-011):** the table is displayed in the **canonical per-arm order** — timepoint events first, sorted by `period` ascending (ties in position order); then no-timepoint events in position order. **Reorder controls** (up/down arrows) are offered on the **no-timepoint events** (and on ties among timepoint events): they submit `PUT /api/v1/projects/{id}/events/order` with the **full** ordered id list of the arm (`API_Endpoints_Design.md` §4.9, REQ-API-103); timepoint events without ties are not reorderable — their place is fixed by their timepoint (the owner changes that by editing `period`).

**Block C — Instruments** (all arms; not tabbed): the instrument list in position order (`name`, `position`, `field_count`, `is_survey`, `branching_logic`). **Add** (`name`, unique per project) → `POST …/instruments`. **Reorder** (up/down or drag) → `PUT …/instruments/order` with the **full** ordered id list; the GD-8 record-identifier invariant MUST hold after the change — a violation returns 400 and is shown per §3.4 (`API_Endpoints_Design.md` §4.10). **Edit** (survey flag, branching logic) → `PUT …/instruments/{iid}` (invalid branching → 400 `validation_error` with the reason shown, REQ-VAL-029).

**Block D — Instrument × event mapping** (per arm, tabbed; REQ-UI-018, REQ-API-072/073): a **checkbox matrix with instruments as rows and events as columns** (master spec orientation), one matrix per arm tab; each cell = that instrument mapped to that event. An instrument may be assigned to none, one, or several events; it is active for data entry once mapped to ≥ 1 event (REQ-DB-012). **Apply** submits the full matrix of the tabbed arm → `PUT …/instrument-event-mapping` (idempotent; unknown instrument/event name → 400). This is the "design of a project's arm" (master spec, data model).

### 6.3 Record status dashboard (`GET /projects/{id}/record-status`, REQ-UI-019)

Data: `GET …/record-status` (data access ≥ `read_only` + record visibility, REQ-API-074/REQ-AUTH-045). This is the **participant overview** (master spec): it lists all participants (records) and is where a new participant is created.

- **New participant** (master spec: "create a new participant (enter record_id string)"): a form at the top of the dashboard with a `record_id` input. Two paths — (a) the user types the record_id, or (b) an **"auto-name"** button calls the data API `generateNextRecordName` (≥ `view_edit` on the target arm, REQ-API-023) via PHP and fills the field. Creating the participant opens the data-entry view (§8) for that (record, first event, first active instrument); the record is persisted on first import (REQ-API-033) and takes the member's active data-access group or none (REQ-API-093). Present only when the member has data access ≥ `view_edit` on some arm (REQ-UI-003).
- **The table**: rows = records (visible under the DAG rule, REQ-AUTH-045); columns = events, grouped per arm (arms tabbed as in §6.2); within each event, the arm's instruments **in order** (REQ-API-074). Each (record, event, instrument) cell shows a **color-code** (the "small graphic" of the master spec):

| State | Color | Meaning | Source |
|---|---|---|---|
| no data | grey | no field of the (record, event, instrument) has a value | derived (any field has a value = false) |
| some data | amber | at least one field has a value | derived (any field has a value = true) |
| finished | green | data entry for this instrument is complete | **user-assigned** (§8.5) |

> **Backend (Open Item 1, §11 — resolved).** The three states are in the baseline: `record-status` returns `no_data` / `some_data` / `finished` per (record, event, instrument) (REQ-API-074), where `finished` is read from `instrument_completion` (REQ-DB-036, `Database_Schema_Design.md` §6) and the other two are derived from whether any field holds a value — so grey/amber can never disagree with the stored data, and green otherwise only ever comes from the user's own assignment. Setting it is REQ-API-110 (§8.5); a survey-marked instrument shows **green automatically** — `record-status` reports it as `finished` without any stored assignment (GD-9; master spec "Instrument level completion info").

- **Navigation**: a record row (or a specific instrument cell) links to the data-entry / record view for that (record, event, instrument) — §8.
- The response MUST NOT contain field values (REQ-API-074); the dashboard reveals only the completion state.

### 6.4 Export action (`GET /projects/{id}/export`, REQ-UI-020)

- A control offering **CSV** or **JSON** (`format=csv|json`, default `csv`) — the two export formats of the master spec (REQ-API-075).
- **Arm selector** (REQ-UI-020): one checkbox per arm the member may export, all checked by default; the selection is sent as repeated `arm=` parameters. This is how a member with different levels on different arms obtains the higher sensitivity for a single arm — deselecting the weaker arm raises the applied level for that download (§4.3 of `Data_Export_Anonymization_Design.md`; REQ-EXP-003). A project with one arm hides the selector.
- **Values / headers** (REQ-EXP-010): a raw-vs-labels switch (`rawOrLabel`) and a header switch (`rawOrLabelHeaders`), mirroring the data API's axes — these are available on this surface too, not only on the data API. For CSV, an optional `csvDelimiter` (REQ-EXP-013).
- **Sensitivity indicator**: a visible badge stating the level being applied for the selected arm(s) — `export_full` / `export_no_identifiers` / `export_de_identified` — so the user knows exactly what they are downloading (REQ-UI-020). For a multi-arm export the **lowest (most protective)** level among the selected arms is the one shown and the one applied (REQ-EXP-003, D-4) — never the highest any single arm would allow. The badge recomputes when the arm selection changes, so unchecking an arm visibly raises the stated level before the download starts.
- The download **streams** (REQ-TECH-011); the PHP route proxies the streamed response, so large exports do not buffer.
- Gating: present only when the member holds a non-`export_none` level on the arm (REQ-UI-017/020, REQ-API-075); `export_none` → the card is absent (REQ-UI-003).
- Every call is audit-logged with `surface:"ui"`, the project, and the sensitivity level (REQ-API-076, `Audit_Logging_Design.md` §3.5).

### 6.5 End-of-Project Provision (project home card, `is_admin`, BR-009)

Present on the project home (§6.1) **only** for `is_admin` (REQ-UI-003; the action is `is_admin`-gated at the API, `API_Endpoints_Design.md` §4.20). The operator's cue is the REK end date in the §6.1 summary (`rek_end_date`, `Data_Export_Anonymization_Design.md` §7.1) — nothing runs automatically (no scheduler, `Project_Charter_Design.md` §5).

- **Action**: `POST` to the project route (CSRF, §3.3) → `POST /api/v1/projects/{id}/end-provision` (contract `API_Endpoints_Design.md` §4.20; rules `Data_Export_Anonymization_Design.md` §7).
- **Choice** — a radio pair in the confirmation modal:
  - `delete` — removes the project's stored record data (values, record entities, survey links, anonymization offsets); project metadata, structure, roles, and the audit trail remain (`Data_Export_Anonymization_Design.md` §7.3);
  - `anonymize` — applies the de-identified pipeline **in place**; every later read and export of the project returns the anonymized form — irreversible (§7.4).
- **Confirmation modal** (§3.5 — destructive and hard-to-reverse): states the chosen provision and the one-shot consequence, requires an explicit confirm click.
- **Result**: on 200, a success line naming the provision and the affected records/values; on 409 (already executed) the conflict line per §3.4.

### 6.6 Project mode (GD-20, REQ-UI-033)

The project home (§6.1) shows the current mode as a **badge** next to the project name — `development` / `production` / `analysis` — from `GET …/mode` (visible to every member; the sidebar brand bar carries it in the project context, §2.4). Data pages state the active design they serve when a staging set is open (§6.7).

The **Mode card** (`is_admin` only — a project's own `project_admin` gets no mode control; REQ-UI-003) offers `PUT /api/v1/projects/{id}/mode` through `?action=` on the project route (CSRF, §3.3), presenting **only the allowed transitions**:

| Transition | Confirmation modal |
|---|---|
| development → production | asks whether previously stored data should be **kept or deleted** (radio pair); choosing delete states the consequence in full — every record value is removed, metadata/structure/memberships/audit remain (same scope as §6.5 `delete`) — and requires a second explicit confirm click (§3.5) |
| production → development | "all data is kept" |
| production → analysis | "all data is kept", plus the warning that data entry stops for everyone from then on (§8.1) |
| analysis → production | "all data is kept"; setup goes back behind staging (§6.7) |
| analysis → development | "all data is kept" — offered so a project can leave analysis without re-entering production (GD-20, 2026-09-27) |

**No transition is offered while a staging set is open**: the Mode card points at the staging banner instead (§6.7), and a stale page that posts anyway gets the 409 (§3.4).

`development → analysis` is never offered — analysis is entered only from production (`API_Endpoints_Design.md` §4.21). On success the badge updates and a translated line names the new mode; on 409 the conflict reason is shown per §3.4.

### 6.7 Staged setup changes in production (GD-20, REQ-UI-034)

When `mode = production`, the Setup (§6.2) and Designer (§7) pages carry the staging controls; in development they are absent and edits apply directly.

- **Start staging** button → `POST …/staging`. Once a set is open, every setup/design page shows a persistent banner: **"Staging open — your changes are staged, data collection continues on the active design"** with **Commit staged changes** and **Discard** (§3.5 confirmations for both).
- The pages then read and write the **staged** design (the API serves it to `project_admin`, `API_Endpoints_Design.md` §4.21); response shapes are unchanged, so every §6.2/§7 control works identically on the staged state. A "view active design" toggle links the plain reads for comparison.
- **Commit** opens a dialog fed by `GET …/staging`: the staged changes listed in two groups — **non-breaking** and **breaking**, each breaking entry with its reason (classification normative in `API_Endpoints_Design.md` §4.21). Committing is disabled until an explicit acknowledgement checkbox ("I understand these changes make existing data inaccessible") is ticked, then `POST …/staging/commit` with `{ "acknowledge_breaking": true }`; a commit without breaking changes posts directly. On success the banner closes and a line names the applied counts; on 409 (breaking not acknowledged) the dialog stays open with the list refreshed.
- **Discard** → `POST …/staging/discard` (confirmation naming the number of staged changes thrown away); the pages revert to the active design.

### 6.8 Setup changes in analysis mode (GD-20, REQ-UI-037)

Analysis mode keeps the Setup (§6.2) and Designer (§7) controls exactly as development has them — no banner, no staging buttons, edits save directly — and adds one interception (REQ-API-111):

- A save that the API rejects with 409 `conflict` naming a breaking change reopens the edit with a confirmation dialog above it: the change in plain words, the consequence from the API's reason string ("deleting a field removes its stored values"), and a **Delete anyway** / **Cancel** pair (§3.5).
- Confirming resubmits the identical request with `acknowledge_breaking: true`; success states that the change is live now, since nothing stages it and there is no commit to undo it from.
- Non-breaking saves are unaffected — no dialog, no extra click, so the guard costs nothing on the edits that dominate designer work.

The dialog text is the same string set the production commit dialog uses (§6.7), which keeps one classification vocabulary in the UI for both paths.

## 7. Instrument Designer (`project_admin`)

### 7.1 Instrument selection (`GET /projects/{id}/design`, REQ-UI-021)

The designer lists the project's instruments in position order (`name`, `field_count`, `is_survey`, `branching_logic`); selecting one opens the field editor for it (§7.2, `GET /projects/{id}/design/instruments/{iid}`). Creation, reordering, and the survey/branching attributes are managed in §6.2 blocks C (structure) — the designer is where the **fields** of an instrument are edited (REQ-UI-021: "select an instrument and manage its fields").

### 7.2 Field editor (`GET /projects/{id}/design/instruments/{iid}`, REQ-UI-021)

Data: `GET …/instruments/{iid}/fields` (fields in position order, all data-dictionary attributes — REQ-DB-013). Presentation: an ordered field table (name, label, type, required, position, section header) plus, per field, an **edit form** with the full attribute set:

| Attribute | Control | Notes |
|---|---|---|
| `field_name` | text | `^[a-z0-9_]+$`, unique in the project (409 on duplicate); **a warning — not an error — when > 26 characters** (REQ-VAL-013, REQ-UI-021); reserved names `redcap_event_name`/`redcap_repeat_instrument`/`redcap_repeat_instance` rejected (`Data_Validation_Design.md` §9) |
| `field_label` | text | the question/description |
| `field_type` | select: `text` / `dropdown` / `radio` / `matrix` / `description` / `header` / `calculated` | type-specific sub-controls below |
| `section_header` | text | group heading in the form |
| `choices` | list editor: `code`/`label` rows | for `dropdown`/`radio`/`matrix` — the `code$label##…` encoding (REQ-VAL-022); codes numeric and unique |
| `field_note` | text | help text |
| `validation_type` | select: empty / built-in `integer` / `floating point` / `date` / `datetime` / every entry of the validation-type registry (`email`, `MRN`, `international phone`, `national phone`, … — listed from `GET /api/v1/validationTypes`, REQ-API-104) | + `validation_format` for date/datetime (§4.1 of `Data_Validation_Design.md`); `validation_min`/`max` for integer/float only (REQ-VAL-015/016/020); unknown names rejected at design time (REQ-VAL-010/042) |
| `required` | checkbox | REQ-VAL-028 |
| `personal_information` | checkbox | feeds anonymized export (REQ-DB-013) |
| `direct_identifier` | checkbox on any field; preset when the validation type is `email`/`MRN`/phone; clearing such a preset requires an explicit action and shows a warning | removed at de-identified export (REQ-EXP-020/021, DEV-EXP-5) |
| `matrix_group` | text | for `matrix` rows (REQ-DB-014) |
| `branching_logic` | expression editor (§7.3) | display-only logic (GD-13) |
| `calculation` | expression editor (§7.3) | only when `field_type = calculated` |

**Actions**: **Add** (append at end, `POST …/fields`), **Edit** (`PUT …/fields/{fid}` — idempotent; rename renames stored values in-transaction, REQ-VAL-014), **Remove** (confirmation stating stored values are removed in-transaction, DEV-API-5; a field referenced by an active expression → 409, §3.4), **Reorder** (up/down) → `PUT …/fields/order` with the full ordered id list; the GD-8 record-identifier invariant MUST hold after reordering (400 on violation, §3.4).

Every design-time rejection (ill-formed expression, unknown/inactive reference, cycle, malformed name) is shown with the API's `validation_error` reason (§3.4, REQ-VAL-029, REQ-UI-021/022).

### 7.3 Expression editors — branching logic and calculations (REQ-UI-021/022)

Both editors are the same component, configured per grammar:

- **Branching logic** (GD-13, `Data_Validation_Design.md` §7.1): references `[event][field]`, string/numeric constants, comparisons `= != < > <= >=`, functions `text_contains` / `is_blank` / `is_not_blank`, logical `&&` / `||`, parentheses.
- **Calculation** (GD-11, `Data_Validation_Design.md` §6.1): references `[event][field]`, numeric constants, `+ - * /`, parentheses.

Editor assistance (REQ-UI-021/022, "reference assistance"): a reference-picker that offers the project's **active** `[event][field]` references (from the data dictionary + mapping) for insertion, plus quick-insert buttons for the operators/functions of the active grammar. The editor is syntax-aware (highlighting references, constants, operators) but performs **no** validation — validation is the API's (design-time, REQ-VAL-029/034/035), and its rejection reason is shown inline (§3.4). Cycles in a calculation are rejected by the API (REQ-VAL-035).

### 7.4 Test a calculation (`REQ-UI-023`)

A panel on the calculated-field editor: a **record picker** (visible records, REQ-AUTH-045) + an optional **draft expression** field + a **Test** button → `POST …/records/{record}/fields/{fid}/test` (optionally `{ "expression": "…" }`; omitted = the field's stored expression, `API_Endpoints_Design.md` §4.11).

Presentation of the result: the computed **value**, and — **every evaluation problem flagged visibly** (REQ-VAL-038) — one line per problem: the offending operand + the `problem` code (`missing_value` / `non_numeric_operand` / `division_by_zero`). The test is a dry run: it MUST NOT store or modify anything (REQ-API-096).

### 7.5 Survey flag (REQ-UI-024)

Marking an instrument as a survey is the `is_survey` attribute in §6.2 block C / §7.2 (`PUT …/instruments/{iid}`). Consequences surfaced in the UI:

- Only survey-marked instruments can be filled via a public link (REQ-AUTH-038); the record view (§8.7) then offers the copy/revoke link actions for it.
- On survey instruments the trailing completion field (§8.5) shows the completed state read-only — filled in automatically, no dropdown, no assignment (GD-9; master spec "Instrument level completion info").

## 8. Data Entry and Record View (`GET /projects/{id}/records/{record}`, REQ-UI-025…028)

### 8.1 Navigation and selection

Reached from the record status dashboard (§6.3): selecting a participant (record) opens this view; the user then selects the **event** and **instrument** (tabs/selects — the instrument must be mapped to the event, REQ-DB-012) to render the form. The unit of the form is a **(record, event, instrument)** triple (the "instrument display of a record", REQ-API-081).

Gating: the view requires data access ≥ `read_only` on the record's arm + record visibility (REQ-AUTH-045); **changing values requires ≥ `view_edit`** — a `read_only` member sees the values but the submit control is absent (REQ-UI-003, REQ-API-033). In an **analysis-mode** project the submit control is absent for everyone including `project_admin` — data entry is disabled (GD-20, REQ-UI-035); the form renders read-only with a translated "analysis mode — data entry is closed" notice, and the record-status dashboard's new-participant affordance (§6.3) is likewise absent.

### 8.2 Form rendering (REQ-UI-025)

The instrument's fields for the selected (record, event) render in position order. Field-type → control mapping:

| `field_type` | Control | Notes |
|---|---|---|
| `text` | `<input type=text>` (or `<textarea>` for free-text) | free-text values render as their allowlist HTML on display (§3.2); validation type drives input hints (built-in integer/float/date/datetime; registry types — email, phone, … — drive the advisory `pattern` from the client-side copy of the registry, never trusted server-side, REQ-VAL-002) |
| `dropdown` | `<select>` | choices as `code`/`label` (stored value = code, REQ-VAL-022) |
| `radio` | radio group | one input per choice |
| `matrix` | a row per matrix sub-field (rows expanded, REQ-DB-014), sharing the group's choices/validation | the matrix header states the coding once (master spec) |
| `description` | static text (no input) | does not accept values (REQ-VAL `NON_VALUE_FIELD`) |
| `header` | section heading (no input) | |
| `calculated` | **read-only** value display (REQ-VAL-036) | never an input; `CALCULATED_READONLY` if submitted (REQ-API-095) |

**Required fields** are marked (label suffix + control styling) and checked before submission (REQ-VAL-028, REQ-UI-025). **Branching logic** (field and instrument level) shows/hides fields and whole instruments while the expression evaluates to 1 — evaluated client-side per §8.4, re-evaluated when a referenced value changes (GD-13, REQ-VAL-040). A hidden instrument hides all its fields (REQ-VAL-040).

### 8.3 Current values and per-field history (REQ-UI-026)

Both come from the **record history endpoint** `GET …/records/{record}/history?instrument=&event=` (data access ≥ `read_only`; chronological, paginated, `API_Endpoints_Design.md` §4.16):

- **Prefill**: each field's **current value** is the most recent non-null value for that field across the (instrument, event)-filtered history (a `create`/`update` entry's `new`; a trailing `delete` → empty). The form opens with existing values filled into all fields (master spec: "existing values filled into all fields").
- **Per-field history button + table** (REQ-UI-026, REQ-API-081; master spec "Record history by field"): next to each field's description and value sits a small **history button**; clicking it opens a table of that field's previous values — **who** entered or changed each one (user account display name), **when** (UTC), and the **old → new** values; for deletions the deleted value. It fetches `GET …/records/{record}/history?instrument=&event=&field=` (the field filter of REQ-API-080) and pages with `limit`/`cursor`. The table is a read-only overlay (Bootstrap modal or popover, §3 per the destructive-action pattern's shell only): no control writes to it — the view stays read-only with respect to the audit trail (REQ-AUD-002). Values escaped per §3.2.
- The UI pages through the filtered history with `limit`/`cursor` (§3.6) to derive the current values and the per-field lists. *Efficiency note (Open Item, §11): a most-recent-first read order would serve this better; the endpoint is chronological.*

### 8.4 Client-side validation and branching (advisory, REQ-VAL-002)

- **Advisory validation**: as the user types, `app.js` validates the value against the field's type/choices/min/max (`Data_Validation_Design.md` §4) and marks required empties (REQ-VAL-028). A client pass is **not** a guarantee — the API is authoritative and re-validates every value before storage (REQ-VAL-001/002); a server rejection is shown per §3.4/§8.6.
- **Branching evaluator**: the same vanilla-JS evaluator implements the normative semantics of `Data_Validation_Design.md` §7.3 (operand resolution, choice-label→code, comparison kinds, truthiness, functions, `&&`/`||`) and re-runs on any referenced field's change (§7.4). It is **display-only**: it never blocks or alters the submitted values (DEV-VAL-5) — the API stores what is sent.

### 8.5 Completion state per instrument (master spec)

At the **end of each instrument's form — always displayed as the last field** (master spec "Instrument level completion info") — a **dropdown** lets the user set that (record, event, instrument)'s completion state: *no data / some data / finished*. Selecting "finished" is the user's completion assignment that drives the green color-code in §6.3. The dropdown is present when the member has data access ≥ `view_edit` on the arm. On a **survey-marked instrument** the same trailing field instead shows the completed state **read-only** — filled in automatically (green, §6.3), no control and no assignment (§7.5, GD-9).

The control maps onto one call — `PUT …/records/{record}/events/{event}/instruments/{iid}/completion` (REQ-API-110, `API_Endpoints_Design.md` §4.13): **finished** → `{ "state": "finished" }`; **no data** or **some data** → `{ "state": "unfinished" }`, after which the dashboard re-derives which of those two to show from the stored values (REQ-API-074). The two non-finished choices are therefore one action, not two — the UI never asserts a filled/empty state the data contradicts, and the dropdown's current selection for an unfinished instrument reflects the derived state rather than a stored one.

> **Backend (Open Item 1, §11 — resolved):** the assignment persists in `instrument_completion` (REQ-DB-036) and is returned by `record-status` as the third state; each set or clear is audit-logged (REQ-AUD-026).

### 8.6 Submitting values — the data-API import path (ASM-API-3, REQ-API-031/035)

The form `POST`s to the PHP route (CSRF token, §3.3). PHP then, server-side:

1. **Obtain the member's project token.** From the session cache (`proj_token_{id}`) if present; otherwise call `GET /api/v1/projects/{id}/users/{uid}/token` (**self-service fetch, REQ-API-102** — `{uid}` is the acting member) and cache the result in the session for that (user, project). This is the only sanctioned source: the session stores it as a **cache** (invalidated per step 3), never as an authorization input (permissions stay API-derived, REQ-AUTH-033 — this refines, not overrides, `Authentication_Authorization_Design.md` §3).
2. **Present it to the data API**: `content=record&action=import` with the form's values as `data[]` entries for the (record, instrument, event) (`API_Endpoints_Design.md` §3.7) — **submission policy (GD-14, REQ-UI-031):** the `data[]` payload contains exactly (a) the fields that **carry a value** in the form, and (b) the fields that **had a stored value that the user removed** (the client tracks each field's prefill value of §8.3 and sends an explicit empty string for those). Fields that had no value and were left empty are **not sent at all** — an empty value reaching the API clears the stored value (REQ-VAL-024), so unsent fields keep their previous values and a partially filled instrument stores only what was entered. For date/date-time fields the call carries the browser's timezone as the collection zone (the `tz` parameter — GD-16, REQ-VAL-041, §1). The API validates every value (REQ-VAL-001), stores all-or-nothing per record (REQ-API-035), and recomputes calculated fields in the same transaction (REQ-API-095). A new record is assigned to the member's active data-access group or none (REQ-API-093).
3. **Stale-token handling**: if the data API answers **401 `Invalid token`** (the token was rotated/revoked since the cache, REQ-AUTH-030), PHP discards the cached token, re-fetches per step 1, and **retries once**. A **403** is a permission result — it is surfaced per §3.4 and NOT retried.
4. **Render the result** (`API_Endpoints_Design.md` §3.7.2): per record — `import_record_id` `1` (added) / `2` (updated) → a success line; `0` → the per-field validation detail from `import_form_name` (rule codes + messages, `Data_Validation_Design.md` §3) listed per field, with the offending inputs highlighted. The completion dropdown (§8.5) submits alongside, as part of the same PHP action.

All successful imports are audit-logged with user, token, record, and changed fields (REQ-API-035, `Audit_Logging_Design.md` §3.2) — the UI shows no audit data for this (the per-field history panel, §8.3, is the read side).

### 8.7 Record actions (REQ-UI-027)

Present per the member's levels (REQ-UI-003):

- **Delete** (data access ≥ `delete` on the arm): delete the whole record or scoped values (by instrument/event), behind an explicit confirmation naming the scope (GD-3, REQ-API-036). PHP presents the member's token to the data API `content=record&action=delete` (same acquisition/stale handling as §8.6). Deletions are audit-logged **with the deleted values** (REQ-AUD-009).
- **Data access group** (record visibility, REQ-AUTH-045): show the record's current group; for `project_admin` — an assign/change control (group select incl. "none") → `PUT …/records/{record}/data-access-group` (REQ-API-091). Members who are not `project_admin` see the group read-only.
- **Survey link** (survey-marked instruments, data access ≥ `view_edit` on the arm): **Copy link** → `GET …/instruments/{iid}/survey-link` — the stable URL in a read-only input + copy button (issued once per (record, instrument) until revoked, `Database_Schema_Design.md` §8); **Revoke link** → confirmation → `DELETE …/survey-link` (effective immediately, REQ-AUTH-040). Absent for non-survey instruments (REQ-AUTH-038).

### 8.8 Public survey page (`GET /survey/{link}`, GD-9, REQ-UI-028)

A standalone PHP route — **no login, outside the session** (GD-1, REQ-API-084, DEV-UI-1), rendered without the sidebar (§2.4). The link is `WEB_PUBLIC_URL + /survey/<link token>`.

- **Render**: PHP calls the **data API** `content=metadata` with the link token — the API accepts a link token only for the calls that render and fill its (record, instrument) (REQ-API-083, `API_Endpoints_Design.md` §3.10); the page shows **only** that instrument's fields for that record (values prefilled from the respondent's prior submission, if any).
- **Submit**: PHP → data API `content=record&action=import` with the link token, applying the same **submission policy** as the data-entry form (GD-14, REQ-UI-031 — entered values plus explicitly cleared fields only) and sending the respondent's browser timezone as the collection zone (GD-16). Submissions pass the **same validation and audit rules as any import** (REQ-AUTH-041, REQ-VAL-001; audit `survey_submitted` — success and failure, `Audit_Logging_Design.md` §3.6).
- **Re-open to edit**: the respondent may re-open the link and change their responses (REQ-AUTH-042) — the page is idempotent for them; the API upserts (REQ-DB-016).
- **Branching**: the same client-side evaluator (§8.4) applies — fields/instruments show or hide per their logic (GD-13, REQ-VAL-040).
- **Revoked link**: the API rejects every call (403, REQ-AUTH-040) — the page shows a single translated "this link is no longer valid" state, no retry.
- **Analysis mode**: while the project is in analysis mode the import is rejected (`Project in analysis mode`, REQ-API-109) — the page renders read-only with a translated "this survey no longer accepts responses" closed state (GD-20).
- The browser **never** calls `/api/v1/*` from this page (REQ-UI-002, REQ-API-084); the CSRF/session rules of §3.3 do not apply (no session) — the link token is the sole credential, and it is scoped to this one (record, instrument) by the API (REQ-AUTH-039).

## 9. Multilingual (GD-12, REQ-UI-008/029/030)

- **Language selector** (footer/sidebar, §2.4): a `<select>` of the enabled languages (`GET /api/v1/i18n/languages` — code + display name; any authenticated user, REQ-API-097). Choosing one `POST`s to the PHP route → `PUT /api/v1/users/me/ui-language` (REQ-API-098); the setting persists across sessions (REQ-DB-008) and applies **from the next page load** (the current page is already rendered). Users without a setting get English (the default, REQ-API-098).
- **Server-side resolution** (REQ-UI-008, REQ-TECH-001): every UI string — page text, labels, buttons, alerts, the §3.4 error messages — is resolved at render time via the mapping tables (REQ-DB-031) or **falls back to English**; a blank or a raw key is never rendered (REQ-UI-008). Translated strings are escaped on render (§3.2).
- **JavaScript-originated messages** (REQ-UI-008: "including JavaScript-originated messages"): since no i18n library is allowed, the server injects the page's small set of JS-visible strings as a single JSON object in a `<script type="application/json" data-i18n>` element (values escaped/JSON-encoded server-side); `app.js` reads it and uses those strings for toasts, confirmation texts, and the advisory-validation hints of §8.4. The JSON contains **no** data values — only UI copy.
- **Translation management**: §5.7 (add/change/clear per language, missing-key visibility, empty text = removal → English fallback).
- The language setting applies to **UI strings only** — never to stored data, field labels, or choice values (GD-12, REQ-UI-008).

## 10. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| page layouts, component details, interaction specifications (`User_Interface_Requirements.md` §1) | this document — routes §2.1, layout §2.4, pages §4–§8 |
| locale-specific date/number formatting (ASM-UI-1) | displayed as stored — system timestamps in UTC `YYYY-MM-DD HH:MM:SS`; clinical date/date-time values **with their collection offset** (GD-16); no locale reformatting, no conversion (§1) |
| token source for UI data entry (ASM-API-3; conflict with `Authentication_Authorization_Design.md` §3 "never in the session") | the self-service fetch `GET …/users/{uid}/token` (REQ-API-102) + a **session cache** invalidated on 401/rotate (§8.6); the session stores it as a cache, never as an authorization input (owner decision, 2026-09-22, master spec "Details") |
| where the data-entry form reads current values (not specified in the baseline) | the record-history endpoint (data-access gated per REQ-AUTH-018 `read_only` = "read the arm's record values"; untransformed values suitable for round-trip entry — unlike the export-gated surfaces, REQ-API-026) (§8.3) |
| sidebar + panel layout, arm tabs (arm_1 default), mapping orientation (instruments × events) (master spec "User interface details") | §2.4, §6.2 (tabbed arm blocks; D = rows × columns checkbox matrix) |
| participant overview + new-participant creation (master spec) | §6.3 (list + record_id/auto-name creation via REQ-API-023/033) |
| project-home option list incl. "administer users in the project" (master spec) | §6.1 action cards + sidebar project context (§2.4); §5.3/§5.4 are project-scoped routes |

## 11. Open Items and Backend Dependencies

Items 2, 4–8 were owner-level baseline changes; the owner decisions of 2026-09-22 (master spec "Details") have now been taken into the requirements baseline, and each resolution is recorded in the Status column with the baseline references that now bind. Item 1 was an additive backend dependency and has since been specified into the baseline (REQ-API-110, REQ-DB-036, REQ-AUD-026, REQ-UI-036). Only item 3 remains open.

| # | Item (master spec source) | Status |
|---|---|---|
| 1 | 3-state completion: no data / some data / **finished**, user-assigned per non-survey instrument (§6.3, §8.5) | **RESOLVED** — `instrument_completion` stores the finished assignment per (record, event, instrument) (REQ-DB-036, DEV-DB-10, `Database_Schema_Design.md` §6); `PUT …/records/{record}/events/{event}/instruments/{iid}/completion` sets or clears it (REQ-API-110, `API_Endpoints_Design.md` §4.13); `record-status` returns `no_data`/`some_data`/`finished` (REQ-API-074, DEV-API-11); audit `instrument_completed`/`instrument_uncompleted` (REQ-AUD-026). Grey/amber stay derived so the badge cannot contradict the stored values (DEV-UI-8); the §6.3/§8.5 contracts bind |
| 2 | "allow changing of event order" (§6.2 B) | **RESOLVED (GD-15)** — `PUT …/projects/{id}/events/order` (REQ-API-103); `period` nullable; canonical per-arm order (REQ-DB-011, `API_Endpoints_Design.md` §4.9); the §6.2 B contract binds |
| 3 | record-history read order (§8.3) | **OPEN** — a most-recent-first (or tail) read mode would serve the form's current-value derivation; the endpoint is chronological (`API_Endpoints_Design.md` §4.16 — efficiency note, not a blocker) |
| 4 | **time zones** — browser tz, per-project collection tz (master spec "Details") | **RESOLVED (GD-16)** — the canonical date/date-time form carries the collection offset (REQ-VAL-041, `Data_Validation_Design.md` §4.1); zone from browser `tz` (UI) / `tz` parameter (API) else `APP_TIMEZONE` (REQ-CFG-026); GD-7 scoped to system timestamps; the §1/§8.6 contracts bind |
| 5 | **simplify `projects`** — drop options / `end_provision` / `event_names`; keep PI + REK + institution; some into a `DataTransferProjects` instrument (master spec "Details") | **RESOLVED (GD-17)** — simplified table (REQ-DB-006, `Database_Schema_Design.md` §4); removed attributes are ordinary instrument data (REQ-DB-032); creation body and form reduced (REQ-API-050, REQ-UI-012, §5.2); BR-009 re-sourced |
| 6 | **table-based authentication** — local password account; bootstrap admin configures the system in the UI (master spec "Details") | **RESOLVED (GD-18)** — `users.password_hash` (bcrypt) + `auth_source = local` (REQ-DB-008); Sequence F, raced with LDAP under the selected source name (REQ-AUTH-050/051/065, `Authentication_Authorization_Design.md` §2.6/§2.9); `ADMIN_BOOTSTRAP_PASSWORD` startup rule (REQ-CFG-025, `System_Configuration_Design.md` §4.1); REQ-AUTH-036 revised; the §2.2 contract binds. (The authentication/authorization setup itself uses the existing administration UI — §5 — since provider configuration remains environment-based, REQ-CFG-001) |
| 7 | **account validity (days; 0 = indefinite)** (master spec "Details") | **RESOLVED (GD-19)** — `users.valid_until` (REQ-DB-008); `valid_days` on the user API (REQ-API-047/048); `account_expired` rejection (REQ-AUTH-052); the §5.1 form contract binds |
| 8 | **auto-disable after N days (180) inactivity** + admin re-enable + display on the user overview (master spec "Details") | **RESOLVED (GD-19)** — `users.last_login_at` (REQ-DB-008); auto-disable rule + `account_auto_disabled` audit (REQ-AUTH-053, `Authentication_Authorization_Design.md` §4.4); `AUTH_INACTIVITY_LIMIT_DAYS` default 180, `0` = off (REQ-CFG-024, `System_Configuration_Design.md` §3.10); admin re-enable resets the inactivity clock; the §5.1 contract binds |

Item 1 is no longer a dependency: the storage, endpoint, audit event, and three-state `record-status` response are in the baseline, so the §6.3 and §8.5 contracts bind as written. The remaining item (3) is additive and non-blocking — an efficiency suggestion for an endpoint that already works correctly, needing no requirements decision to implement the UI as specified here.
