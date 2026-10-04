# Web Application — Implementation Plan

**Type:** sequencing and milestones (not a normative area document — it introduces no `REQ-*` IDs and changes no design; every obligation below cites an existing ID)
**Builds:** `Design/User_Interface_Design.md` (normative for routes, pages, components), `Design/Technology_Stack_Design.md` §4/§5 (tree, stack, proxy), `Design/Authentication_Authorization_Design.md` §2–§3 (login sequences, session schema)
**Date:** 2026-09-30

## 1. Scope

`web/` is the PHP 8.3-or-8.4 rendering layer: page rendering, PHP-native session ownership, the OAuth2/LDAP/local login flow, and a thin proxy to the Go administration API. It is the system's only browser-facing component besides the public survey route.

Out of scope here: Go API work beyond §3 below; the production nginx file (open item in `Technology_Stack_Design.md` §9, owned by operations); the quality of the `nb`/`nn` translation content (the mechanism is in scope, the translations are a language task).

Binding limits, restated because they constrain every file written: no SQL and no DB driver (`REQ-TECH-006`, success criterion 6); the browser never sees `/api/v1/*` (`REQ-UI-002`); no build step, no framework, no jQuery (`REQ-UI-001`); no CDN at runtime (`ASM-TECH-2`); UI strings resolved server-side (`REQ-UI-008`).

## 2. Starting point (verified against the tree on 2026-09-30)

- **The Go API is complete.** All 77 administration routes of `API_Endpoints_Design.md` §4 are registered and implemented in `api/internal/admin/*.go` (no stubs, no 501s), plus the data API at `/api/` (`internal/dataapi`) covering `project`, `metadata`, `event`, `formEventMapping`, `exportFieldNames`, `generateNextRecordName`, and `record` with `export`/`import`/`delete`. Migrations `0001`–`0010` exist for both dialects.
- **`web/` does not exist.** `.gitignore` already reserves `web/sessions/`; `assets/` holds the two Bootswatch theme files to vendor, the logo, and the FIONA reference app.
- **No OAuth2/LDAP flow exists in Go** — `internal/config` only parses provider/server definitions. The redirect, the bind, and the parallel named-source race are PHP's job (`Authentication_Authorization_Design.md` §2.9), finalizing through `POST /api/v1/auth/login`.
- **i18n is half-wired.** Languages `en`/`nb`/`nn` are seeded and `GET /api/v1/i18n/bundle` serves a language's overrides, but no strings exist: the English catalog is the web layer's to author (`REQ-DB-031`, `API_Endpoints_Design.md` §4.19 — "English is the source of truth and lives in the PHP layer").
- **FIONA contributes an interfacing style, not markup.** The reference app has no sidebar anywhere, exactly one `<table>` (`User/projects.php:119`, hand-styled), mixed Bootstrap 2.3.1/4.4.1 on the same pages, and three jQuery versions. Adopt from it: per-page access-control include, fetch-and-populate of rendering targets, audit-on-failure, deny-by-default accessors, write-temp-then-rename. Do not port its HTML, and do not repeat its defects (client-side md5 as the wire encoding, no CSRF token, `innerHTML` with server data, GET mutations, session-fixation-prone login without `session_regenerate_id`).

## 3. Prerequisites — five decisions, all closed

These shape or block the work downstream. All five are closed (P0-a and P0-b on 2026-09-30, P0-c and P0-d the same day, P0-e on 2026-10-01) — **nothing blocks M0**.

### P0-a · The API exposed no effective-permissions read — **closed 2026-09-30**

`REQ-UI-003` requires gating at render time with the control *absent from the DOM*. PHP cannot compute it: `X-Internal-User-Id` is authoritative and "the PHP layer adds none" (`API_Endpoints_Design.md` §4.1), yet nothing carried the acting user's per-arm levels — so every project-scoped control for a non-admin member (submit, delete, export card, completion dropdown, survey actions) could only be rendered and fail with 403.

**Resolved as option A.** `GET /api/v1/projects/{id}`, and the `PUT` echo of it, now return:

```json
"permissions": { "project_admin": false,
                 "arms":   [ { "arm_num": 1, "data_access_level": "view_edit", "export_level": "export_de_identified",
                               "delete_values": false, "edit_surveys": false } ],
                 "grants": [ { "unique_event_name": "baseline_arm_1", "instrument": "intake",
                               "data_access_level": "read_only", "export_level": "export_none",
                               "delete_values": false, "edit_surveys": false } ] }
```

*Amended 2026-10-03:* the block gained the two rights and the `grants[]` list when permissions moved from arm to (instrument, event) pair (REQ-AUTH-069); the shape above is the current one.

Specified as **REQ-API-126** (`Requirements/API_Endpoints_Requirement.md` §4.4; rationale recorded as DEV-API-18), documented in `Design/API_Endpoints_Design.md` §4.5, implemented in `api/internal/admin/projects.go` over the same `authz.Effective` result the boundary applies, covered by `TestProjectDetailCarriesEffectivePermissions`. What the web layer relies on:

- one entry per project arm, always, plus one `grants[]` entry per mapped (instrument, event) pair of a visible arm — an ungranted arm reports `no_access` / `export_none` and an ungranted pair reports its arm's default, so a level is never inferred from a missing row (`REQ-AUTH-019/069`). The list is the same cardinality as the record status dashboard it gates, and arrives in arm-then-event order so PHP walks it without sorting;
- `is_admin` and role-less members see `view_edit` + both rights / `export_full` on every arm and pair plus `project_admin: true` (`REQ-AUTH-023/022`) — no special-casing in PHP;
- the block is disclosed only past the same `read_only` + visibility gate as the rest of the read; a rejected read carries none at all (`REQ-API-007`);
- it authorizes nothing — every endpoint re-checks at call time (`REQ-AUTH-033`).

Read it once per project page into `Permissions.php` (§5). Option B (own route) cost an extra round trip on every project page; option C (derive levels in PHP) would duplicate authorization outside the API — both rejected per DEV-API-18.

### P0-b · Survey route path — **closed 2026-09-30**

The API issues survey URLs as `WEB_PUBLIC_URL + "/s/" + token` (`api/internal/admin/survey.go:199`, matching the example in `API_Endpoints_Design.md` §4.17) while `User_Interface_Design.md` fixed the PHP route as `/survey/{link}`. **`/s/{link}` is the route**; the UI design doc (§2.1, §8.8) and `Authentication_Authorization_Design.md` §1.1 now say so, so the value *Copy link* hands over and the route that serves it cannot drift apart. The neighbouring set-password link (`/set-password?token=…`, `passwords.go:56`) already matched.

### P0-c · Where the client-facing JSON proxies live — **closed 2026-09-30**

`User_Interface_Design.md` §2.1 states "there are no other browser-reachable routes", while §3.7 requires the client to fetch JSON from PHP endpoints that proxy the API (`REQ-UI-032`, `REQ-TECH-025`). Both hold only if the proxies reuse the page routes.

**Resolved: the same route, content-negotiated — implemented once in the router, not per page.** A request with `Accept: application/json` (which the client always sends) receives that route's data region as JSON; anything else renders the page. Mutations stay `POST`s to the page route with `?action=` (§2.1). Specified as **REQ-UI-044** (`Requirements/User_Interface_Requirements.md` §2, rationale DEV-UI-11) and recorded in `Design/User_Interface_Design.md` §2.1/§3.7, so the "closed route set" claim stays literally true. What that binds M0 to:

- the decision is taken in `Router.php`, **after** the session guard and permission gate and **before** the controller runs — one dispatch path, so a data request can never skip a check the page render passes (REQ-UI-003);
- a controller declares the data regions its route can serve; adding a page adds no routing code, and no controller or template grows an `if wants_json()` branch of its own;
- a failed data request answers with the API's status and stable `error` code (`API_Endpoints_Design.md` §4.2) so the client renders the translated line of `User_Interface_Design.md` §3.4 rather than an HTML page inside `response.json()`;
- `/s/{link}` reaches its data through the same dispatch under its own public guard (`User_Interface_Design.md` §8.8) — the negotiation itself has no special case for it.

### P0-d · One JavaScript file is too tight a constraint for the intended surface — **closed 2026-09-30**

§1 of `User_Interface_Design.md` fixed `web/assets/app.js` as "the only JavaScript file of the application". That file would have had to carry the branching evaluator (`Data_Validation_Design.md` §7.3 semantics), advisory validation, data-region binding, Tabulator wiring, the expression editors, the submission-policy diff (GD-14) and every page's behaviour.

**Resolved: several scripts as needed — one shared runtime plus one module per view / logical section / application.** `web/assets/app.js` is the shared runtime (fetch helper carrying `Accept` + `X-CSRF-Token`, data-region binders, the `data-i18n` string block, Tabulator defaults); `web/assets/js/<section>.js` carries one surface each — `record.js`, `record-status.js`, `setup.js`, `design.js`, `members.js`, `admin-users.js`, `survey.js`, … — and a page emits `<script type="module">` tags only for what it uses. The branching evaluator is one module imported by both the record form and the survey page, which is the case a single entry point could not express as clearly. Specified as **REQ-UI-045** (rationale DEV-UI-12); `Design/User_Interface_Design.md` §1/§3.7/§8.4 and `Technology_Stack_Design.md` §4 amended to match.

The limits that keep this inside the existing stack facts, and that review should enforce: `<script type="module">` with **relative** specifiers (no import map, no bundler — `REQ-TECH-001`/`REQ-UI-001`), same-origin only (`ASM-TECH-2`, CSP `script-src 'self'`), UI strings still injected server-side (`REQ-UI-008`), and vendored libraries untouched under `assets/vendor/`.

### P0-e · A two-factor challenge could not complete a local login — **closed 2026-10-01**

`POST /api/v1/auth/login` verifies the bcrypt hash on **every** local call (Sequence C step 0, `REQ-AUTH-050`) and the API keeps no challenge state (GD-1), so the code submission §2.7 describes had to carry a first-factor proof — while `User_Interface_Design.md` §2.2 gives that panel one code field, and `REQ-AUTH-036` forbids persisting a password anywhere a session file could hold it.

**Resolved as a signed first-factor handle** (`REQ-API-131`, rationale DEV-API-19): `POST /api/v1/auth/verify-password` — which has just verified that hash and does nothing else — returns a 5-minute HMAC statement to that effect, and login accepts it in place of `password`. It is issued only for an account a second factor still guards, so it can never finish a login on its own; it is bound to one user id, carries no session reference, stays inside the internal boundary, and never reaches a log or audit row. Rejected alternatives: re-asking for the password on the panel (contradicts §2.2 and charges the user for a property of the API), and a browser-held credential re-submitted by script (a 2FA login would need JavaScript, and a reload loses it). Implemented in `api/internal/admin/firstfactor.go` over the same service secret, covered by `TestFirstFactorCompletesAChallengedLogin`, `TestFirstFactorCannotReplaceAPasswordForAnUnguardedAccount` and `TestFirstFactorSignatureAndExpiryAreEnforced`.

What PHP does with it: store the handle in `tfa_pending` (§3) beside the email, present one code field, re-invoke login with `{email, source, first_factor, mfa_code}`, and treat 401 `first_factor_expired` as "start over" rather than as a failure line — it is not counted toward the lockout (`REQ-AUTH-035`).

## 4. Target tree

Per `Technology_Stack_Design.md` §4, expanded:

```
web/
├── public/index.php              # front controller: config → session → router → response
├── app/
│   ├── Config.php                # env load + startup validation (REQ-CFG-005)
│   ├── Logger.php                # structured, secrets/value-free (REQ-API-005 hygiene applies here too)
│   ├── Router.php                # route table of §2.1; the one page-vs-JSON dispatch on Accept (REQ-UI-044) + `?action=` mutations
│   ├── ApiClient.php             # curl: service token, X-Internal-User-Id, X-Real-IP; error envelope → ApiException
│   ├── Session.php               # SESSION_DIR, cookie flags, regenerate, idle timeout (§3 schema)
│   ├── Auth.php                  # require_login(); local/LDAP/OAuth2 flows; tfa_pending gate
│   ├── Csrf.php                  # issue/validate (REQ-UI-005)
│   ├── Permissions.php           # gating predicates over P0-a's payload — the single place §3.1 lives
│   ├── Navigation.php            # the two left panels + the default-section rule (§2.4, REQ-UI-046/047)
│   ├── View.php                  # layout + template render, e() escaping, CSP header (REQ-TECH-020)
│   ├── I18n.php                  # English catalog + bundle overlay, t(), JS-string injection (§9)
│   ├── Messages.php              # API error code → translated line (§3.4 table)
│   ├── DataApi.php               # data-API form encoding: import rows data[i][key], delete, export passthrough
│   └── i18n/en.php               # English source of truth (REQ-DB-031)
├── app/Controllers/…             # one class per page group of §2.1 (Login, Dashboard, ControlPanel, Project, Setup, Record, …)
├── views/
│   ├── layout/shell.php          # header + content panel + footer; renders a left panel only when the page supplies one (§2.4)
│   ├── layout/standalone.php     # login, password pages (§2.4 — no navigation)
│   ├── layout/survey.php         # public survey shell (§8.8)
│   ├── partials/nav-panel.php    # the left panel of a section-bearing page — entries computed by app/Navigation.php (§2.4, REQ-UI-046/047)
│   └── <page>.php …              # Bootstrap 5.3, table-sm, escaped by default
├── assets/
│   ├── app.js                    # shared runtime: fetch helper (Accept + X-CSRF-Token), data-region binders, i18n block reader, Tabulator defaults
│   ├── js/                       # one module per view/logical section — record.js, setup.js, design.js, admin-*.js, survey.js (REQ-UI-045)
│   ├── app.css                   # the only hand-written CSS (keep CSP at style-src 'self')
│   └── vendor/
│       ├── bootstrap/            # 5.3.x css + bundle js, local
│       │   └── themes/{darkly,yeti}/bootstrap.min.css   # derived from assets/, remote @imports stripped
│       ├── tabulator/            # vendored per AGENTS.md
│       └── fonts/geist/          # served locally
└── sessions/                     # SESSION_DIR (gitignored)
```

No Composer dependency in phase 1: OAuth2 authorization-code + PKCE with `curl` + `openssl`, LDAP through `ext-ldap`. Anything else needs a row added to `Technology_Stack_Design.md` §3 first (`GD-25`, `REQ-TECH-026`).

## 5. Foundation layer (built once, in M0–M1)

| Component | Contract worth stating up front |
|---|------|
| `ApiClient` | Adds `X-Internal-Service-Token` + `X-Internal-User-Id` on every call; forwards the caller's address as `X-Real-IP` (`Technology_Stack_Design.md` §5, `REQ-API-125`). **Admin writes reject unknown attributes with 400** (`admin.go:435`) — send exactly the whitelisted names, per area. Data-API calls behave oppositely: unknown parameters are dropped. Maps `{error,message,status}` to an exception carrying the stable code for `Messages.php` |
| `Session` | Keys and lifetimes exactly as `Authentication_Authorization_Design.md` §3; `session_regenerate_id(true)` on login and on TFA promotion; identity only — permissions never cached, project tokens excepted per the §8.6 cache rule |
| `Auth` | `require_login()` as the FIONA `AC.php` pattern: first statement of every controller; expired session → redirect `/login`. `tfa_pending` is pre-authentication and must be rejected by every page guard |
| `Permissions` | Reads the `permissions` block of the project detail read (REQ-API-126) once per request, indexed by pair; exposes `canView($event,$instrument)/canEdit($event,$instrument)/canDeleteValues($event,$instrument)/canEditSurveys($event,$instrument)/exportLevel($event,$instrument)/isProjectAdmin`, plus arm-level forms of the first two for project-wide controls. A predicate with no pair argument means "any pair" and must never stand in for a specific one when the template knows it (REQ-AUTH-069). Templates call only this. Hidden means not emitted — no `disabled`, no 403 link (`REQ-UI-003`) |
| `View` / escaping | Escape every interpolation; the single exception is stored free-text allowlist HTML (§3.2). CSP on every response, `style-src 'self'` achievable if all styling goes through `app.css`/Bootstrap classes rather than inline `style=` attributes |
| `I18n` | English array in code, DB overlay from `GET /api/v1/i18n/bundle`; a key that resolves to nothing renders English, never blank or raw (`REQ-UI-008`). JS-visible strings injected as one `<script type="application/json" data-i18n>` block (§9) — no data values in it. Cache the bundle per session and invalidate on `POST /lang` |
| `Router` | The §2.1 route set, the `?action=` mutation convention, and **the one** page-vs-JSON dispatch (`REQ-UI-044`): after the guard and permission gate, before the controller, `Accept: application/json` selects the route's declared data region instead of its template. Controllers never test the request type themselves; a route with no declared region answers 406, not HTML |
| `assets/app.js` + `assets/js/` | Shared runtime (`app.js`) plus one ES module per view/logical section, loaded by relative path with `<script type="module">` — unbundled, same-origin, page loads only what it uses (`REQ-UI-045`). The layout emits the `src` list; a module never carries a UI string (the `data-i18n` block of `User_Interface_Design.md` §9 is the only source) and never touches `/api/v1/*` |

## 6. Milestones

Each milestone is a vertical slice that runs against the real API (SQLite, `APP_ENV=development`) and leaves the app deployable.

| # | Milestone | Routes / surfaces | Size | Depends on |
|---|---|---|---|---|
| M0 | Skeleton + assets | front controller, router, config validation, View/layout, CSP, vendored Bootstrap 5.3 + themes + Tabulator + Geist | M | — |
| M1 | Authentication and shell | `/login` (source picker, local/LDAP race, OAuth2 redirect), `/auth/callback`, TFA panels, `POST /logout`, `/lang`, `/theme`, no-access page | **XL** | M0 |
| M2 | Dashboard, project home, self-service | `/`, `/projects/{id}`, `/account/two-factor`, `/account/password`, `/password-reset`, `/set-password` | L | M1 |
| M3 | Administration surface | `/admin` (Control Panel: `?section=users\|projects\|audits\|translations\|settings`, REQ-UI-047), `/projects/{id}/members`, `/roles`, `/groups` | L | M2 |
| M4 | Setup and designer | `/projects/{id}/setup` (blocks A–D, arm tabs), `/design`, `/design/instruments/{iid}`, expression editors + test panel, staging banner/commit/discard, analysis-mode acknowledge, mode card | **XL** | M3 |
| M5 | Data entry and record view | `/projects/{id}/record-status` (colour grid, new participant, auto-name), `/projects/{id}/records/{record}` (form render, prefill from history, per-field history, completion control, branching evaluator, submit via data-API import), record actions (delete scoped, DAG assign, survey link) | **XL** | M4 |
| M6 | Export, public survey, hardening | `/projects/{id}/export` (streamed), `/s/{link}`, responsive pass, error-mapping completeness, `nb`/`nn` catalog population, PHP smoke tests + Playwright browser tests (`REQ-TECH-028`) in CI | M | M5 |

Ordering notes: M3 precedes M4 because members/roles must exist before a project can be designed and permissioned by a non-admin. M6's export slice is small and may be pulled forward into M3 if a demonstration of Fiona-parity download is wanted early. M4 and M5 are the two heaviest; nothing else can proceed in parallel with them beyond i18n catalog authoring, which is continuous work from M1 onward.

### Exit criteria per milestone (condensed acceptance)

- **M0** — `/` renders the shell for a stub user with one stylesheet, all assets load with the network disabled, CSP present on every response, `php -S` serves it without a build step. The two closed decisions are demonstrated, not assumed: one route answers in both shapes (HTML, and its data region for `Accept: application/json`) from the router alone (`REQ-UI-044`), and that page loads its own section module through the shared runtime (`REQ-UI-045`).
- **M1** — bootstrap admin logs in with `ADMIN_BOOTSTRAP_EMAIL`/`_PASSWORD`; session-fixation defense verified; expired session redirects to `/login`; TOTP challenge and mandated enrollment reachable against the real API; logout calls `POST /api/v1/auth/logout` *before* destroying the session (`REQ-UI-007`); a user with no projects sees the information page, not an empty dashboard (`REQ-UI-006`).
- **M2** — dashboard lists visible projects with the API's counts; TFA enroll/confirm/recovery-codes-once/disable works end to end; invite → `/set-password?token=…` → login round-trips.
- **M3** — charter success criterion 3: a full project lifecycle (create project, add role, add member, show token once, rotate, remove) entirely through the UI.
- **M4** — an instrument can be designed from scratch including choices, validation, branching and a calculated field tested against a record; production-mode staging commit blocks until breaking changes are acknowledged (`REQ-UI-034`).
- **M5** — charter success criterion 2: a data-entry user creates, reads and updates values through the UI, with audit rows in the right table; a partially filled instrument stores only entered values and clears exactly what the user cleared (`REQ-UI-031`); a `read_only` member sees no submit control.
- **M6** — survey link issued from the record view opens without a session and submits; export streams (no buffering at 1k×200×10 on the reference hardware) and states the applied sensitivity level.

## 7. Cross-cutting rules to enforce during review

These are the ones most likely to be got wrong, gathered from the code-level facts:

1. **Attribute whitelists.** Admin writes 400 on unknown attributes; each form's payload must match the endpoint's accepted set exactly (`REQ-API-042` idempotency assumed, not verified per field).
2. **Never call `GET …/instruments/{iid}/fields/order`.** It routes to the reorder handler and needs a body (`structure.go:42,45`). Field order comes from `GET …/fields`.
3. **Negative provisional ids.** While a staging set is open, objects created in it carry negative ids and structure reads return the staged snapshot (`admin.go:156-165`) — forms and links must tolerate them.
4. **Project creation already seeds arm 1 and a `baseline` event** (`projects.go`, HEAD). Do not create them from the UI; offer the link into setup instead (§5.2).
5. **Roles are editable, not deletable** — `PUT /api/v1/projects/{id}/roles/{rid}` (REQ-API-143) replaces a role's name, arm defaults and pair grants in one call, so the roles screen is one form in create or edit mode (`User_Interface_Design.md` §5.4). There is still no role delete and no user delete (disable only), and the UI offers neither. Deleting a role would leave its members' `role_id` null, which reads as *full permissions* (`REQ-AUTH-022`) — a deletion endpoint must not be added without first closing that fail-open path (master-spec finding F20).
6. **Login's 2FA responses are non-standard 401s** (`mfa_required` with `method`, `tfa_enrollment_required` with `user_id`) — branch on `error`, not on status alone; the enrollment wizard runs pre-session using that id as `X-Internal-User-Id`. A local challenge resumes with the P0-e handle and never with the password, and 401 `first_factor_expired` clears the pending state back to the credential form rather than showing a failure line.
7. **Token cache discipline (§8.6).** Fetch the member's project token via `GET …/users/{uid}/token`, cache per (user, project) in the session, and on data-API **401 `Invalid token`** discard + refetch + retry exactly once; a 403 is a permission result and is never retried.
8. **Submission policy (GD-14).** Send only fields carrying a value plus fields whose stored value the user removed (tracked client-side against the §8.3 prefill). An empty value that reaches the API clears the row (`REQ-VAL-024`).
9. **Time handling.** Display system timestamps as returned (UTC `YYYY-MM-DD HH:MM:SS`); display clinical dates exactly as stored, offset included; send the browser zone as `tz` on import (`GD-16`). No locale reformatting anywhere.
10. **Rate limiting and lockout.** Forward `X-Real-IP`; surface 429 with the retry/lockout duration on login and reset (`REQ-AUTH-035`).
11. **Log hygiene.** Tokens, the service secret and record values never in logs at any level (`REQ-API-005`); audit payloads displayed escaped, never copied into other page elements (§5.6).
12. **Status codes.** `DELETE` returns 204, creates return 201 (member add included — that response carries the one-time token), most mutations 200. Do not branch on 200 uniformly.
13. **Bounded API calls per render.** `REQ-TECH-010` targets < 300 ms p95: no N+1 fan-out per table row (the FIONA `admin.php` refetch-three-endpoints-then-fan-out-per-row pattern is the anti-pattern to avoid); one bundle fetch for i18n; cursor and filters live in the query string so a page reloads (§3.6).
14. **Prefill cost.** Current values come from paging the record-history endpoint with `order=newest` (`REQ-API-137`, closed 2026-10-02): walk most-recent-first and stop per field at the first entry seen, so a form open costs pages over recent changes, not the whole history. Still measure on M5 with seeded data — if it hurts even backwards, that is another API addition, not a UI workaround.
15. **The permissions block is disclosure, not a credential.** Fetch `GET /api/v1/projects/{id}` once per project page and hand the levels to templates through `Permissions.php` — never re-request it per widget, and never carry levels into the session (they go stale; `REQ-AUTH-033`). It answers rendering only: whatever is shown still has to survive the API's own check.
16. **Content negotiation belongs to the router.** One dispatch in `Router.php` decides page vs data region from `Accept` (`REQ-UI-044`); a controller or template that inspects the header, re-implements the split, or reaches for its own `/json` route is a defect — as is a data region whose payload discloses more than the same user's rendered page would.
17. **One module per section, no bundler.** Section behaviour goes in `web/assets/js/<section>.js`, loaded by the pages that need it through the shared runtime (`REQ-UI-045`); shared code is imported, not duplicated — and no fix for the file count may introduce a build step (`REQ-TECH-001`).

## 8. Testing and CI

`ci/run.sh` has covered all four layers since 2026-10-03, in the order `Technology_Stack_Design.md` §4 names them — Go vet and tests, the Fiona fixtures, `php -l` plus the `ASM-TECH-2` external-reference check, this harness, then the browser specs (it was Go-only before, which left success criteria 2 and 3 uncovered by the gate). Per `Technology_Stack_Design.md` §6 the normative coverage stays in Go, PHP gets a minimal stdlib harness, and the essential client-side components get Playwright browser tests (`REQ-TECH-028`, `e2e/`). The gate counts a skipped spec as uncovered rather than green, because `REQ-TECH-028` asks for these in CI; outside it the specs still skip themselves when no stack is running:

- **Static:** `php -l` over `web/`; a grep asserting no external URL in any template or asset reference (`ASM-TECH-2`).
- **Unit (no HTTP):** router dispatch — including both shapes of one route, HTML and `Accept: application/json`, and that a data response passes the same gate as its page (`REQ-UI-044`), CSRF accept/reject, session idle timeout, `Messages.php` code→string mapping, `Permissions.php` gating decisions against fixture payloads, i18n fallback.
- **Integration:** boot the real API on a temp SQLite file, serve `web/public` with `php -S`, and script the two charter paths — login → create project → design instrument → enter data → export (criteria 2/3) — asserting status codes and rendered markers rather than pixel output.
- **Browser (`e2e/`, `REQ-TECH-028`):** Playwright (`@playwright/test` 1.63.x, Chromium on Node 20 LTS) drives that same stack through a real browser for what the server-side harness cannot reach — login/session flow, record entry and editing including client-side validation and branching feedback, a Tabulator-backed data table, and the client half of the page/JSON split (`REQ-UI-044`). Specs live in `e2e/`, never under `web/`, so nothing test-related is served and no build step is introduced (`REQ-TECH-001`); `node_modules/`, `test-results/` and `playwright-report/` are git-ignored. Running since 2026-10-02 (the Node ≥ 20 upgrade landed; first-time setup is `cd e2e && npm ci && npx playwright install chromium`): `e2e/dev-stack.sh` starts the API on a throwaway SQLite file and `web/public` under `php -S` with the source names the login spec asserts against, and `CLARA_WEB_URL=http://127.0.0.1:8090 npx playwright test` drives it. Specs stay skipped, not red, when no `CLARA_WEB_URL` is set.
- Reuse `api/internal/dataapi/fiona_test.go` as the authoritative wire examples for the data-API client (`DataApi.php`) instead of re-deriving them from REDCap docs.

## 9. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Nothing ties the PHP survey route to the URL the API builds | an issued link 404s in production while every test passes | `/s/{link}` is now fixed on both sides — keep it that way: never re-spell the path in PHP configuration, and add a web-layer test asserting the URL returned by `GET …/survey-link` is routable |
| M5's history-based prefill on long histories | slow form open, p95 breach | `order=newest` (`REQ-API-137`) already removes the whole-history walk — still measure with seeded data; if it hurts even backwards, that is another API addition, never caching in PHP |
| Section modules drift into per-widget scripts and per-widget fetches, and pages accumulate module `src` tags with no bundler to fold them | p95 breach on exactly the heavy pages (§7 rule 13), and a client that is hard to attribute | one module per view/logical section, one data region fetched per page render (`REQ-UI-044/045`); count API calls and `src` tags per page in M4/M5 review — the fix is a coarser region, never a build step (`REQ-TECH-001`) |
| Translation catalog treated as an afterthought | raw keys or English-only UI at hand-off against `REQ-UI-008` | author the English catalog incrementally per page from M1; `nb`/`nn` in M6 with the §5.7 missing-key view as the worklist |
| Free-text allowlist rendering treated as licence for `innerHTML` | XSS through stored values (`REQ-UI-004`) | one render helper pair — `e()` and `html_allowed()` — and a review rule that `innerHTML` appears only in the latter |

## 10. Suggested first session of work

M0: front controller, router carrying the one `Accept`-based page/JSON dispatch (`REQ-UI-044`), config validation, layout shell with header/footer and the optional left panel (§2.4), escaping/CSP/CSRF plumbing, the shared `app.js` runtime plus the first section module and the way a page declares its scripts (`REQ-UI-045`), and the vendored assets (Bootstrap 5.3 + derived `darkly`/`yeti` with remote `@import`s stripped + Tabulator + Geist) — enough that M1's login work lands into a real, styled shell instead of a bare page. Both P0-c and P0-d are closed, so M0 builds them rather than choosing between them.
