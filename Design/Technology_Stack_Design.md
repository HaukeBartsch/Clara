# Technology Stack — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Technology_Stack_Requirements.md`
**Date:** 2026-09-21

## 1. Purpose

Fixes the concrete versions, the production dependency set, and the repository layout that the stack constraints (REQ-TECH-001…028) mandate. This document is normative for toolchain selection; any deviation is a requirements-level change.

> **Key constraints** — a summary for quick orientation; the cited IDs and sections are normative.
>
> - **This document is normative for toolchain selection**; any deviation is a requirements-level change (§1).
> - **Production dependencies come only from the §3 allowlist**, each pinned with an integrity hash; frontend libraries are vendored under `web/assets/vendor/`, never loaded from a CDN.
> - **The layering rules of §4 hold everywhere**: PHP contains no SQL, the Go API contains no session logic, and neither imports the other.

## 2. Pinned Versions (normative)

| Component | Version | Notes |
|---|---|---|
| Go | 1.26.x (pinned in CI; `go.mod` declares `go 1.26`) | built with `CGO_ENABLED=0` → single static binary (REQ-TECH-013) |
| PHP | 8.3 or 8.4 standard distribution — either line is acceptable (owner decision 2026-09-30) | extensions: `pdo_sqlite`, `pdo_mysql`, `ldap`, `curl`, `openssl`, `mbstring`; Composer dependencies only from the critical-function allowlist (§3, GD-25) — otherwise the standard distribution (REQ-TECH-023/026). Because both lines must run, application code stays inside the feature set common to 8.3 and 8.4 (no 8.4-only syntax), mirroring the portability rule of REQ-TECH-007; CI and each deployment pin one exact version |
| SQLite | ≥ 3.45 (OS/toolchain-provided) | development and tests (REQ-TECH-005) |
| MariaDB | 11.x LTS (≥ 11.4) | production (REQ-TECH-005) |
| Bootstrap | 5.3.x | vendored under `web/assets/vendor/bootstrap/` (CSS + bundle JS only); no CDN (ASM-TECH-2) |
| Bootstrap themes | Bootswatch 5.3-compatible (`darkly`, `yeti`) | full-replacement stylesheets vendored under `web/assets/vendor/bootstrap/themes/<name>/bootstrap.min.css`, derived from the copies in `assets/` (`darkly_theme_bootstrap.min.css`, `yeti_theme_bootstrap.min.css`); remote references inside a theme (e.g. the Google Fonts `@import`) are stripped at derivation — themes render offline; selection per REQ-TECH-027/GD-26 (`UI_THEME` default, REQ-CFG-031; per-user override, REQ-UI-040/041) |
| JavaScript | vanilla ES2020 | no framework, no build step (REQ-TECH-001) |
| Web server | nginx + PHP-FPM (reference); Apache httpd 2.4 + PHP-FPM (approved alternative) | reverse proxy and TLS termination point — one of the two per host, never both in front of the same application (REQ-TECH-015, ASM-TECH-1). Reference configurations: `deploy/nginx/clara.conf`, `deploy/apache2/clara.conf` |
| Node.js | 20 LTS (≥ 20.x) | **development and CI only** — runtime for the Playwright browser tests (§6, REQ-TECH-028); never a production dependency, nothing from it is served. Playwright ≥ 1.62 requires Node ≥ 20 |
| Playwright | `@playwright/test` 1.63.x, pinned in `e2e/package.json` + `e2e/package-lock.json`; Chromium | browser tests for essential web components (REQ-TECH-028); dev/CI tooling that lives outside the deployed tree — no page or template references it, so no production dependency (REQ-TECH-023/026), no CDN (ASM-TECH-2) and no build step (REQ-TECH-001) |

## 3. Go Production Dependencies (normative allowlist)

REQ-TECH-023 permits database drivers, the OpenAPI/Swagger UI static bundle, and at most two other production libraries. The complete set is:

| Module | Role |
|---|------|
| `modernc.org/sqlite` | SQLite driver — pure Go, so the static-binary rule (REQ-TECH-013) holds without cgo |
| `github.com/go-sql-driver/mysql` | MariaDB driver |
| `github.com/microcosm-cc/bluemonday` | HTML sanitizer for the free-form content policy (`Data_Validation_Design.md` §5.2) |
| `golang.org/x/net` (package `html` only) | tolerant HTML parse/serialize for the same policy |

Everything else is standard library: `net/http` (both surfaces, OpenAPI serving), `database/sql`, `encoding/csv` (streaming export, custom delimiter, REQ-TECH-011/REQ-API-029), `encoding/json`, `crypto/rand` (UUIDs, link tokens), `crypto/subtle` (constant-time service-token comparison, REQ-AUTH-012), `log/slog` (structured logging, REQ-TECH-016), and a hand-written recursive-descent parser for the two expression languages (`Data_Validation_Design.md` §6/§7) — no parser library.

The OpenAPI document is a hand-maintained `openapi/openapi.json` (OpenAPI 3.1) next to the code; the interactive UI is the vendored `swagger-ui-dist` static bundle served by the API. No swaggo/code-generation tooling (REQ-TECH-004, REQ-API-002).

**Critical-function library exceptions (GD-25, REQ-TECH-026).** For authentication protocol clients (OAuth2, LDAP), two-factor and token cryptography adjacent code, session handling, and i18n, additional vetted libraries MAY be admitted in Go, PHP, and JavaScript. Admission is by amendment to this section: one row per library with **component, module/package, pinned version + integrity hash, role, and the date of its security review**; re-review is required on every version bump, and each amendment needs owner approval. Admitted PHP code ships as a committed `vendor/` tree plus `composer.lock`; admitted JavaScript ships as vendored ES modules under `web/assets/vendor/` with no build step and no CDN (REQ-TECH-001, ASM-TECH-2). UI strings stay translated server-side (REQ-UI-008) — an admitted JS i18n library covers client-side formatting only.

| Component | Module / package | Pinned version + integrity hash | Role | Security review |
|---|---|---|---|---|
| Go | `golang.org/x/crypto` (package `argon2` only, with its in-module dependency `blake2b`; on amd64 it also links `golang.org/x/sys/cpu`, a module already present through `modernc.org/sqlite`) | `v0.57.0`, `h1:3ZVCjf8Ggz7zneR/EHRVx68Ctf+2pmIMP2UFhh9cC6M=` (per `api/go.sum`) | argon2id password hashing/verification for table-based authentication (GD-18, REQ-AUTH-050, DEV-AUTH-16; `Authentication_Authorization_Design.md` §7) — PHC strings, RFC 9106 parameters (64 MiB, t = 3, p = 4), constant-time verify; replaced package `bcrypt` on 2026-10-11 | 2026-10-11 |
| Go | `github.com/coreos/go-oidc/v3` (package `oidc`) | `v3.21.0`, `h1:wZo4Q9Pum8dYEj0eMUPrqR+kvuGkeUplbLpNCkBqoWM=` (per `api/go.sum`) | Sequence J ID-token verification (`api/internal/admin/assertion.go`, REQ-AUTH-076/077/082) — discovery validation, JWKS fetch with cache and refresh on an unknown `kid`, and signature plus `iss`/`aud`/`exp` checks against a **pinned** algorithm. Admitted over hand-written JOSE because this is the one parse in the system where a mistake opens a session. What it does *not* do, and CLARA code beside it therefore does: nonce comparison, the bounded clock skew, `email_verified` type strictness, the acr/amr assurance gate, and the `OAUTH2_JWKS_TTL` reuse bound (the library refreshes only on an unknown `kid`, never on a timer). Discovery runs once per issuer and feeds both halves of Sequence A — the API's authorization request is built from the endpoints this call resolves, and the same runtime holds the verifier (`api/internal/admin/assertion.go`, `providerRuntime`). The token exchange itself is `golang.org/x/oauth2`'s row below: this library verifies a proof, it does not perform the exchange | 2026-10-27 (owner approval; re-review on every version bump) |
| Go | `github.com/go-jose/go-jose/v4` | `v4.1.4`, `h1:moDMcTHmvE6Groj34emNPLs/qtYXRVcd6S7NHbHz3kA=` (per `api/go.sum`) | the JOSE layer go-oidc verifies ID-token signatures through — pulled in by it from `api/internal/admin`, imported by no CLARA code of its own, and admitted alongside the row above rather than in its own right | 2026-10-27 |
| Go | `golang.org/x/oauth2` | `v0.36.0`, `h1:peZ/1z27fi9hUOFCAZaHyrpWG5lwe0RJEEEeH0ThlIs=` (per `api/go.sum`) | the OAuth2 client side of Sequence A in Go (`api/internal/admin/oauthflow.go`, `assertion.go`; REQ-AUTH-086): `AuthCodeURL` builds the authorization-code request with a PKCE S256 challenge and the `nonce`, `Exchange` redeems the callback code with `VerifierOption` and the configured client credentials, and the ID token is read out of that response for Sequence J. Because the exchange moved here from PHP, `OAUTH2_N_CLIENT_SECRET` is now a Go-only secret — `web/app/Config.php` no longer reads it (REQ-CFG-021). Admitted as go-oidc's dependency on 2026-10-27; this row restates its role for the flow, and **owner approval of the widened use is pending** (REQ-TECH-026) | 2026-10-27; re-review on every version bump |
| JavaScript | `tabulator-tables` (vendored ESM + `tabulator_bootstrap5` CSS under `web/assets/vendor/tabulator/`) | `6.6.1`, JS `sha256:4d133538a58b5d4c8c344f5b02dc618cfc31adf01eed86b4632b5233d811948c`, CSS `sha256:f1fe11a28e4f1a1ecd690b3ed32d8665534a7970b1c6a37d97a6dccc5537c437` | performant client-rendered data tables (AGENTS.md "use Tabulator"; shared defaults in `app.js`, §3.7; first surface `User_Interface_Design.md` §5.7) — vendored per REQ-TECH-001/ASM-TECH-2, MIT licence shipped alongside | 2026-10-11 |
| JavaScript | `swagger-ui-dist` bundle (`swagger-ui-bundle.js` + `swagger-ui.css` under `web/assets/vendor/swagger-ui/`) — byte-identical copies of the bundle the API embeds under `api/docs/swaggerui/` (§3 above, REQ-TECH-023), not a second library | JS `sha256:62df541529080464a7660adc793eab7128c6193ce3be24ddc1e0e0a4a63edc2f`, CSS `sha256:1ac324f7dcd27e4b9386b4bd6421271ec147e922a22c05ba24b11515e9aa6321` | renders the OpenAPI document in the Control Panel's API documentation section, read-only (`User_Interface_Design.md` §5.9, REQ-UI-057); loaded only by that section | 2026-10-13 |
| JavaScript | `d3` (vendored UMD bundle `d3.v7.min.js` under `web/assets/vendor/d3/`) | `7.9.0`, JS `sha256:f2094bbf6141b359722c4fe454eb6c4b0f0e42cc10cc7af921fc158fceb86539` | the D3 core for the relevance Sankey of the API documentation section (`User_Interface_Design.md` §5.9, REQ-UI-060) — classic script attaching `d3` to the global object (the Swagger bundle's precedent), loaded only by that section; MIT licence shipped alongside | 2026-10-13 |
| JavaScript | `d3-sankey` (vendored UMD bundle `d3-sankey.min.js` under `web/assets/vendor/d3/`) | `0.12.3`, JS `sha256:8286db5d6aa049cc6e8a546708943b79dfb4daaefb0ccf42af674ec0ee4c86be` | the Sankey layout for `d3` above — its UMD wrapper extends the global `d3` with `sankey`, so it loads after it and nowhere else (REQ-UI-060); MIT licence shipped alongside | 2026-10-13 |
| JavaScript | `@mlc-ai/web-llm` (vendored prebuilt ESM bundle `web-llm.mjs` under `web/assets/vendor/webllm/`) | `0.2.86`, JS `sha256:ae2642e73a4a9896c21efb3a067c4666446f8b49cbe2012f4b576f5d83d5f206` | the in-browser inference engine of the Clara assistant — loads and runs a locally hosted tool-calling model on WebGPU (`AI_Assistant_Design.md`, REQ-AI-002/003); loaded only by the assistant section, no CDN at runtime (ASM-TECH-2). Model weights are assets, not a library: git-ignored under `web/assets/models/`, fetched at install by `tools/fetch_assistant_model.sh` (ASM-AI-2) | 2026-10-16 (owner instruction of the same day; re-review on every version bump) |

A vetted OAuth2/OIDC client is no longer among the candidates: it is admitted above. The three Go rows were taken on 2026-10-27 for Sequence J (REQ-AUTH-076…082, DEV-AUTH-18), and with REQ-AUTH-086 they now cover Sequence A's protocol half as well — the Go API resolves discovery, builds the PKCE authorization request, redeems the callback code and verifies the ID token, while PHP keeps only the single-use browser transaction and its own session (§2.1). The split that decision accepts is deliberate: the browser-facing half stays in PHP because `state` lives in the PHP session, and the API stays stateless by taking the verifier and nonce back from PHP on the login call rather than storing them (REQ-AUTH-079). Remaining candidates (e.g. an i18n formatter for PHP/JS) are admitted by adding a row here — the stack decision itself is settled.

## 4. Repository Layout (normative)

```
redcap-replacement/
├── api/                            # Go API (single process, module csms/api)
│   ├── cmd/server/main.go          # entry point: config → migrations → routes
│   ├── internal/
│   │   ├── config/                 # env parsing + startup validation (REQ-CFG-004)
│   │   ├── httpapi/                # routing, middleware (service token, acting user, rate limit), health, docs mounts
│   │   ├── dataapi/                # POST /api/ REDCap protocol: content handlers, record import/export/delete, survey links, transfer, IP rate limiter
│   │   ├── admin/                  # /api/v1/ handlers (one file per area, incl. ODM interchange)
│   │   ├── authz/                  # token lookup, per-arm levels, DAG visibility (ASM-AUTH-5)
│   │   ├── validate/               # DB-free validation core: type validators + regex registry, choice codes, content policy, expression grammars
│   │   ├── audit/                  # event catalog, same-transaction writer
│   │   ├── db/                     # single writer to the store: dialect switch, migration runner, repositories, year-rollover checks
│   │   ├── migrations/             # embedded SQL per dialect: sqlite/ and mariadb/, NNNN_name.sql (REQ-DB-003)
│   │   ├── metrics/                # in-memory performance counters + snapshot (REQ-API-163), stdlib only
│   │   ├── mailer/                 # net/smtp sender: 2FA codes, invite/reset links (REQ-AUTH-057/060/062)
│   │   ├── passhash/               # argon2id password hashing, PHC strings (GD-18, REQ-AUTH-050)
│   │   ├── totp/                   # RFC 6238 TOTP + recovery codes (REQ-AUTH-054/059), stdlib only
│   │   └── testdb/                 # test support only: fixtures on SQLite or MariaDB; never imported by the server
│   ├── openapi/                    # openapi.json (hand-maintained) + openapi.go embedding it
│   ├── docs/                       # vendored swagger-ui-dist, embedded by docs.go
│   └── go.mod / go.sum
├── web/                            # PHP web application
│   ├── public/index.php            # front controller (routes → app/)
│   ├── app/                        # page controllers (each route also serves its content-negotiated JSON — REQ-UI-044), session, CSRF, OAuth2/LDAP flow, API client
│   │   ├── Controllers/            # one controller per page or route group (LoginController.php, RecordStatusController.php, ControlPanelController.php, …)
│   │   ├── Mcp/                    # in-browser assistant's tool server (AI_Assistant_Design.md)
│   │   ├── i18n/                   # English source strings (en.php); translations live in the database (GD-12)
│   │   └── data/                   # relevance_sankey.php, generated by tools/build_relevance_sankey.php for the API documentation section (REQ-UI-057); the only tracked file here
│   ├── views/                      # PHP templates (Bootstrap, escaped output)
│   ├── sessions/                   # development SESSION_DIR (REQ-CFG-017); git-ignored
│   └── assets/
│       ├── app.js                  # shared ES-module runtime (REQ-UI-045): fetch helper (Accept: application/json, X-CSRF-Token), data-region binding, JS-string block reader, Tabulator defaults
│       ├── training/               # Training page plans, one directory each: plan.md (+ plan.<lang>.md) and pictures (User_Interface_Design.md §2.7, REQ-UI-059)
│       ├── js/                     # one module per view/logical section — record.js, record-status.js, design.js, roles.js, admin.js, monitoring.js, survey.js, … — loaded with <script type="module"> by relative path; no bundler (REQ-UI-045, REQ-TECH-001)
│       ├── models/                 # in-browser assistant model files, downloaded by tools/fetch_assistant_model.sh; git-ignored (AI_Assistant_Design.md)
│       └── vendor/                 # vendored frontend libraries, never loaded from a CDN; versions in vendor/README.md
│           ├── bootstrap/          # Bootstrap 5.3.x
│           │   └── themes/         # darkly/, yeti/ — full-replacement bootstrap.min.css per theme, remote @imports stripped (GD-26, REQ-TECH-027)
│           ├── tabulator/          # table rendering (AGENTS.md)
│           ├── fonts/              # Geist (interface font) and League Gothic
│           ├── swagger-ui/         # Control Panel API documentation section (REQ-UI-057)
│           ├── d3/                 # d3 + d3-sankey: Monitoring charts (REQ-UI-064), assistant charts, API documentation relevance diagram (REQ-UI-057)
│           └── webllm/             # in-browser assistant runtime (§3, AI_Assistant_Design.md)
├── tests/php/                      # PHP test suite: run.php runs every *_test.php; shared helpers in support.php (§6)
├── e2e/                            # Playwright browser tests (REQ-TECH-028) — package.json + package-lock.json + playwright.config.ts + specs; dev/CI only, never served and never inside web/ (node_modules/, test-results/, playwright-report/ are git-ignored)
├── stress/                         # API load tests (Python 3 standard library) and their launchers; dev only, never deployed
├── tools/                          # developer scripts: translation seeds (i18n/), ODM conformance check + CDISC schemas (odm_schema/), assistant model download, relevance diagram build
├── deploy/                         # reverse-proxy reference configurations (§5): nginx/clara.conf and apache2/clara.conf, one rule set in two spellings; installed to /etc/<server>/… by operations, never served
├── ci/                             # run.sh: go vet + unit + integration (SQLite) + Fiona fixtures + php -l + Playwright (§6, REQ-TECH-028); mariadb.compose.yml; semgrep_baseline.php
├── Requirements/, Design/, Plan/   # the documentation chain (AGENTS.md); docs/ holds the user-facing introduction and analysis notes
├── assets/                         # example data dictionaries and the historic FIONA reference application; never deployed
├── .env.example                    # complete variable inventory (System_Configuration_Design.md §5)
└── .gitignore                      # .env, SQLite files (REQ-CFG-001)
```

> **Layering rules.** The PHP layer contains **no** SQL and no direct data access (REQ-TECH-006); the Go API contains **no** session logic (GD-1); neither tree may import the other.

The web app adopts the reference application's style and interfacing (REQ-TECH-025, REQ-UI-032, master spec "Details"; `assets/table_based_authentication_plus_user_management/`): PHP renders the shell and serves JSON that proxies the API **from the page routes themselves**, selected on `Accept: application/json` so no proxy route is added to the UI's route set (REQ-UI-044), and the vanilla ES2020 client — ES modules, one per view/logical section behind a shared runtime (REQ-UI-045) — fetches that JSON and populates data regions (lists, tables, `<select>` options) without page switching; the reference's `AC.php` is the model for the PHP session flow (`Authentication_Authorization_Design.md` §3).

## 5. Build and Deployment

- **Go:** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o api-server ./cmd/server` → one static binary, no runtime language dependency (REQ-TECH-013). The API applies migrations at startup (REQ-DB-003) and fails fast on missing configuration or unreachable database (REQ-TECH-014, REQ-CFG-004).
- **PHP:** static deployment; PHP-FPM pool behind the reverse proxy (nginx, or Apache httpd as the approved alternative). No build step.
- **Reverse proxy (normative routing rules):** TLS termination; `POST /api/` (data API) and the PHP routes are publicly routable; `/api/v1/*` and the documentation endpoints are NOT routable from the public network (REQ-TECH-018, REQ-AUTH-014); `X-Internal-Service-Token`, `X-Internal-User-Id` and `X-Internal-Session-Epoch` are stripped from every externally-originated request (`nginx: proxy_set_header X-Internal-Service-Token ""; proxy_set_header X-Internal-User-Id ""; proxy_set_header X-Internal-Session-Epoch "";`). The proxy sets `X-Real-IP $remote_addr` on every routed request (overwriting any client-supplied value; no forwarded-for chains), so the API's rate limiter sees a distinct address per caller (REQ-API-125); PHP derives the address it forwards on its server-side API calls by that same rule — `REMOTE_ADDR`, unless that peer is listed in `TRUSTED_PROXY_CIDRS` (`System_Configuration_Design.md` §3.9, `API_Endpoints_Design.md` §3.9) — so the proxy's overwrite is defence in depth rather than the guarantee, and a browser never names itself (finding F9). **Reference configurations:** `deploy/nginx/clara.conf` and `deploy/apache2/clara.conf` ship these rules for the two approved servers — one rule set in two spellings, so a change to either lands in both. Each file's header maps every directive group back to the rule and REQ ID behind it, and `e2e/tests/deployment.spec.ts` checks that the two still agree with each other and with the guide, and (against a deployed front door) that the rules hold on the wire.
- **Placement:** API and PHP on the same host or trusted internal segment (ASM-TECH-3); inter-component traffic loopback or TLS (REQ-AUTH-034).

## 6. Testing (REQ-TECH-021/022, REQ-TECH-009)

- **Go:** unit tests per package; integration tests boot the real HTTP stack against a temporary SQLite file with `APP_ENV=development`; CI runs them with no external services (OAuth2/LDAP replaced by test doubles in the PHP flow; the API's login endpoint is exercised with fixture emails).
- **REDCap compatibility:** every Fiona call example in `VISION_AND_REQUIREMENTS.md` is encoded as a table-driven fixture (form-encoded request → expected status + body) run against a seeded project — the charter success criterion 1 as an executable regression test (REQ-TECH-022, REQ-API-037).
- **PHP:** no framework; smoke tests use a minimal stdlib test harness for route dispatch, CSRF validation, and the session flow. The normative coverage lives in the Go API (REQ-TECH-021). The harness (`tests/php/`, `php tests/php/run.php`) runs with **no HTTP**: a fake transport answers in place of the API (`web/app/Transport.php`), so it covers router dispatch — including both shapes of one route, HTML and `Accept: application/json`, and that a data response passes the same gate as its page (REQ-UI-044) — CSRF accept/reject, the session lifecycle (idle and absolute bounds, rotation, grace-window follow, replay, epoch adoption — the id swaps against a real session store in a child process), `Messages.php` code→string mapping, `Permissions.php` gating decisions against fixture payloads, i18n fallback, the pure data-entry rules of `web/app/DataEntry.php`, and the parallel LDAP race through its launcher seam. The data-API client (`DataApi.php`) takes its wire examples from `api/internal/dataapi/fiona_test.go` rather than re-deriving them from REDCap documentation.
- **Browser (`e2e/`, REQ-TECH-028):** Playwright (`@playwright/test` 1.63.x, Chromium, Node 20 LTS) drives the running PHP application against the real Go API on a temporary SQLite file with LDAP/OAuth2 test doubles (REQ-TECH-009), covering the essential interactive components: login and session flow, a record entry/edit form including client-side validation and branching feedback, a Tabulator-backed data table, and the client half of the page/data-region split (REQ-UI-032, REQ-UI-044, REQ-TECH-025). Assertions target rendered markers and behaviour, not pixels. `e2e/` is development and CI tooling: it is never served, no shipped page or template references it (so it adds no production dependency under REQ-TECH-023/026 and no runtime CDN reference under ASM-TECH-2), and it introduces no build step for frontend code (REQ-TECH-001). It complements the PHP harness above — the normative coverage stays in the Go API (REQ-TECH-021).
  - *Stack and setup:* specs live in `e2e/`, never under `web/`, so nothing test-related is served; `node_modules/`, `test-results/` and `playwright-report/` are git-ignored. First-time setup is `cd e2e && npm ci && npx playwright install chromium`; `e2e/dev-stack.sh` starts the API on a throwaway SQLite file and `web/public` under `php -S` with the source names the login spec asserts against, and `CLARA_WEB_URL=http://127.0.0.1:8090 npx playwright test` drives it. Without `CLARA_WEB_URL` every spec skips itself — skipped, not red — so a machine with no running stack stays usable.
  - *Integration tier:* the same stack scripted along the two charter paths — `e2e/tests/admin.spec.ts` (success criterion 3: project, role, member, token shown once, rotate, remove) and `e2e/tests/record.spec.ts` (criterion 2: create, read and update values with audit rows) — asserting status codes and rendered markers rather than pixel output.
  - *Deployment:* `e2e/tests/deployment.spec.ts` holds a deployed front door (`CLARA_DEPLOY_URL`, `CLARA_PROXY_KIND=nginx|apache`) to the routing rules of §5 and checks that both files under `deploy/` and INSTALL.md still agree.
- **CI gate (`ci/run.sh`):** one script runs every layer in the order §4 lists them and installs nothing — tooling that is absent is a failure with a remedy, never a green tick: `go vet`; Go unit and integration tests; the Fiona fixtures (REQ-API-037); ODM dialect conformance against the vendored CDISC schemas (REQ-ODM-055 — the one advisory step); `php -l` over `web/` and `tests/php/`; the no-external-reference check over `web/` (ASM-TECH-2); semgrep over `api/` and `web/`, failing on any finding not recorded in `.semgrep/baseline.txt` with its triage in `docs/Static_Analysis_Triage.md`; freshness of the relevance Sankey data (REQ-UI-060); the PHP harness; the Playwright specs. Inside the gate a skipped browser spec counts as uncovered rather than green, and a run in which no spec ran fails (REQ-TECH-028).

## 7. Reference Hardware (REQ-TECH-010/011)

The performance targets are measured on: 4 vCPU, 8 GB RAM, SSD, MariaDB on the same host, Go API and PHP-FPM on the same host, project size 1,000 records × 200 fields × 10 events. Against this: admin API reads < 300 ms p95 (REQ-TECH-010); full flat CSV export < 10 s and streamed (REQ-TECH-011).

## 8. Resolved Deferred Items

| Deferred in | Resolution here |
|---|------|
| "reference hardware (see design document)" (REQ-TECH-010) | defined in §7 |
| static-binary mechanism (REQ-TECH-013) | pure-Go SQLite driver + `CGO_ENABLED=0` (§3/§5) |
| Swagger UI hosting (REQ-TECH-004) | hand-maintained OpenAPI 3.1 + vendored `swagger-ui-dist`, no generator (§3) |

## 9. Open Items

| Item | Owner |
|---|------|
| exact reverse-proxy configuration file for the deployment target | operations — closed for the rule set, which ships as `deploy/nginx/clara.conf` and `deploy/apache2/clara.conf` (§5); what remains per host is `server_name`, the certificate paths, and finding F8's separate-listener question |
| Node.js 20 LTS on developer workstations and CI images — Playwright ≥ 1.62 refuses to run on the Node 18.19.1 currently installed, so `e2e/` tests cannot execute until it is upgraded (§2, REQ-TECH-028) | operations / each developer |
