# Technology Stack — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Technology_Stack_Requirements.md`
**Date:** 2026-09-21

## 1. Purpose

Fixes the concrete versions, the production dependency set, and the repository layout that the stack constraints (REQ-TECH-001…028) mandate. This document is normative for toolchain selection; any deviation is a requirements-level change.

## 2. Pinned Versions (normative)

| Component | Version | Notes |
|---|---|---|
| Go | 1.26.x (pinned in CI; `go.mod` declares `go 1.26`) | built with `CGO_ENABLED=0` → single static binary (REQ-TECH-013) |
| PHP | 8.4 standard distribution | extensions: `pdo_sqlite`, `pdo_mysql`, `ldap`, `curl`, `openssl`, `mbstring`; Composer dependencies only from the critical-function allowlist (§3, GD-25) — otherwise the standard distribution (REQ-TECH-023/026) |
| SQLite | ≥ 3.45 (OS/toolchain-provided) | development and tests (REQ-TECH-005) |
| MariaDB | 11.x LTS (≥ 11.4) | production (REQ-TECH-005) |
| Bootstrap | 5.3.x | vendored under `web/assets/vendor/bootstrap/` (CSS + bundle JS only); no CDN (ASM-TECH-2) |
| Bootstrap themes | Bootswatch 5.3-compatible (`darkly`, `yeti`) | full-replacement stylesheets vendored under `web/assets/vendor/bootstrap/themes/<name>/bootstrap.min.css`, derived from the copies in `assets/` (`darkly_theme_bootstrap.min.css`, `yeti_theme_bootstrap.min.css`); remote references inside a theme (e.g. the Google Fonts `@import`) are stripped at derivation — themes render offline; selection per REQ-TECH-027/GD-26 (`UI_THEME` default, REQ-CFG-031; per-user override, REQ-UI-040/041) |
| JavaScript | vanilla ES2020 | no framework, no build step (REQ-TECH-001) |
| Web server | nginx + PHP-FPM | reverse proxy and TLS termination point (REQ-TECH-015, ASM-TECH-1) |
| Node.js | 20 LTS (≥ 20.x) | **development and CI only** — runtime for the Playwright browser tests (§6, REQ-TECH-028); never a production dependency, nothing from it is served. Playwright ≥ 1.62 requires Node ≥ 20 |
| Playwright | `@playwright/test` 1.63.x, pinned in `e2e/package.json` + `e2e/package-lock.json`; Chromium | browser tests for essential web components (REQ-TECH-028); dev/CI tooling that lives outside the deployed tree — no page or template references it, so no production dependency (REQ-TECH-023/026), no CDN (ASM-TECH-2) and no build step (REQ-TECH-001) |

## 3. Go Production Dependencies (normative allowlist)

REQ-TECH-023 permits database drivers, the OpenAPI/Swagger UI static bundle, and at most two other production libraries. The complete set is:

| Module | Role |
|---|---|
| `modernc.org/sqlite` | SQLite driver — pure Go, so the static-binary rule (REQ-TECH-013) holds without cgo |
| `github.com/go-sql-driver/mysql` | MariaDB driver |
| `github.com/microcosm-cc/bluemonday` | HTML sanitizer for the free-form content policy (`Data_Validation_Design.md` §5.2) |
| `golang.org/x/net` (package `html` only) | tolerant HTML parse/serialize for the same policy |

Everything else is standard library: `net/http` (both surfaces, OpenAPI serving), `database/sql`, `encoding/csv` (streaming export, custom delimiter, REQ-TECH-011/REQ-API-029), `encoding/json`, `crypto/rand` (UUIDs, link tokens), `crypto/subtle` (constant-time service-token comparison, REQ-AUTH-012), `log/slog` (structured logging, REQ-TECH-016), and a hand-written recursive-descent parser for the two expression languages (`Data_Validation_Design.md` §6/§7) — no parser library.

The OpenAPI document is a hand-maintained `openapi/openapi.json` (OpenAPI 3.1) next to the code; the interactive UI is the vendored `swagger-ui-dist` static bundle served by the API. No swaggo/code-generation tooling (REQ-TECH-004, REQ-API-002).

**Critical-function library exceptions (GD-25, REQ-TECH-026).** For authentication protocol clients (OAuth2, LDAP), two-factor and token cryptography adjacent code, session handling, and i18n, additional vetted libraries MAY be admitted in Go, PHP, and JavaScript. Admission is by amendment to this section: one row per library with **component, module/package, pinned version + integrity hash, role, and the date of its security review**; re-review is required on every version bump, and each amendment needs owner approval. Admitted PHP code ships as a committed `vendor/` tree plus `composer.lock`; admitted JavaScript ships as vendored ES modules under `web/assets/vendor/` with no build step and no CDN (REQ-TECH-001, ASM-TECH-2). UI strings stay translated server-side (REQ-UI-008) — an admitted JS i18n library covers client-side formatting only.

| Component | Module / package | Pinned version + integrity hash | Role | Security review |
|---|---|---|---|---|
| Go | `golang.org/x/crypto` (package `bcrypt` only) | `v0.57.0`, `h1:3ZVCjf8Ggz7zneR/EHRVx68Ctf+2pmIMP2UFhh9cC6M=` (per `api/go.sum`) | bcrypt hashing/verification for table-based authentication (GD-18, REQ-AUTH-050; `Authentication_Authorization_Design.md` §7) — cost ≥ 10, constant-time verify | 2026-09-28 |

Additional candidates (e.g. a vetted OAuth2 client or an i18n formatter for PHP/JS) are admitted by adding a row here — the stack decision itself is settled.

## 4. Repository Layout (normative)

```
redcap-replacement/
├── api/                            # Go API (single process)
│   ├── cmd/server/main.go          # entry point: config → migrations → routes
│   ├── internal/
│   │   ├── config/                 # env parsing + startup validation (REQ-CFG-004)
│   │   ├── httpapi/                # routing, middleware (service token, rate limit), docs
│   │   ├── redcap/                 # /api/ protocol: content handlers, import/export
│   │   ├── admin/                  # /api/v1/ handlers (one file per area)
│   │   ├── authz/                  # token lookup, per-arm levels, DAG visibility (ASM-AUTH-5)
│   │   ├── validation/             # pipeline, grammars, content policy, expressions
│   │   ├── audit/                  # event catalog, same-transaction writer
│   │   └── db/                     # dialect switch, repositories, year-rollover checks
│   ├── migrations/                 # NNN_name.up.sql (REQ-DB-003)
│   ├── openapi/openapi.json
│   ├── docs/                       # vendored swagger-ui-dist
│   └── go.mod / go.sum
├── web/                            # PHP web application
│   ├── public/index.php            # front controller (routes → app/)
│   ├── app/                        # page controllers (each route also serves its content-negotiated JSON — REQ-UI-044), session, CSRF, OAuth2/LDAP flow, API client
│   ├── views/                      # PHP templates (Bootstrap, escaped output)
│   └── assets/
│       ├── app.js                  # shared ES-module runtime (REQ-UI-045): fetch helper (Accept: application/json, X-CSRF-Token), data-region binding, JS-string block reader, Tabulator defaults
│       ├── js/                     # one module per view/logical section — record.js, record-status.js, setup.js, design.js, admin-*.js, survey.js — loaded with <script type="module"> by relative path; no bundler (REQ-UI-045, REQ-TECH-001)
│       └── vendor/bootstrap/       # vendored Bootstrap 5.3.x
│           └── themes/             # darkly/, yeti/ — full-replacement bootstrap.min.css per theme, remote @imports stripped (GD-26, REQ-TECH-027)
├── e2e/                            # Playwright browser tests (REQ-TECH-028) — package.json + package-lock.json + playwright.config.ts + specs; dev/CI only, never served and never inside web/ (node_modules/, test-results/, playwright-report/ are git-ignored)
├── .env.example                    # complete variable inventory (System_Configuration_Design.md §5)
├── .gitignore                      # .env, SQLite files (REQ-CFG-001)
└── ci/run.sh                       # go vet + unit + integration (SQLite) + Fiona fixtures + php -l + Playwright (§6, REQ-TECH-028)
```

Rules: the PHP layer contains **no** SQL and no direct data access (REQ-TECH-006); the Go API contains **no** session logic (GD-1); neither tree may import the other. The web app adopts the reference application's style and interfacing (REQ-TECH-025, REQ-UI-032, master spec "Details"; `assets/table_based_authentication_plus_user_management/`): PHP renders the shell and serves JSON that proxies the API **from the page routes themselves**, selected on `Accept: application/json` so no proxy route is added to the UI's route set (REQ-UI-044), and the vanilla ES2020 client — ES modules, one per view/logical section behind a shared runtime (REQ-UI-045) — fetches that JSON and populates data regions (lists, tables, `<select>` options) without page switching; the reference's `AC.php` is the model for the PHP session flow (`Authentication_Authorization_Design.md` §3).

## 5. Build and Deployment

- **Go:** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o api-server ./cmd/server` → one static binary, no runtime language dependency (REQ-TECH-013). The API applies migrations at startup (REQ-DB-003) and fails fast on missing configuration or unreachable database (REQ-TECH-014, REQ-CFG-004).
- **PHP:** static deployment; PHP-FPM pool behind nginx. No build step.
- **Reverse proxy (normative routing rules):** TLS termination; `POST /api/` (data API) and the PHP routes are publicly routable; `/api/v1/*` and the documentation endpoints are NOT routable from the public network (REQ-TECH-018, REQ-AUTH-014); `X-Internal-Service-Token` and `X-Internal-User-Id` are stripped from every externally-originated request (`nginx: proxy_set_header X-Internal-Service-Token ""; proxy_set_header X-Internal-User-Id "";`). The proxy sets `X-Real-IP $remote_addr` on every routed request (overwriting any client-supplied value; no forwarded-for chains), so the API's rate limiter sees a distinct address per caller (REQ-API-125); PHP forwards that address in `X-Real-IP` on its server-side API calls (`API_Endpoints_Design.md` §3.9).
- **Placement:** API and PHP on the same host or trusted internal segment (ASM-TECH-3); inter-component traffic loopback or TLS (REQ-AUTH-034).

## 6. Testing (REQ-TECH-021/022, REQ-TECH-009)

- **Go:** unit tests per package; integration tests boot the real HTTP stack against a temporary SQLite file with `APP_ENV=development`; CI runs them with no external services (OAuth2/LDAP replaced by test doubles in the PHP flow; the API's login endpoint is exercised with fixture emails).
- **REDCap compatibility:** every Fiona call example in `VISION_AND_REQUIREMENTS.md` is encoded as a table-driven fixture (form-encoded request → expected status + body) run against a seeded project — the charter success criterion 1 as an executable regression test (REQ-TECH-022, REQ-API-037).
- **PHP:** no framework; smoke tests use a minimal stdlib test harness for route dispatch, CSRF validation, and the session flow. The normative coverage lives in the Go API (REQ-TECH-021).
- **Browser (`e2e/`, REQ-TECH-028):** Playwright (`@playwright/test` 1.63.x, Chromium, Node 20 LTS) drives the running PHP application against the real Go API on a temporary SQLite file with LDAP/OAuth2 test doubles (REQ-TECH-009), covering the essential interactive components: login and session flow, a record entry/edit form including client-side validation and branching feedback, a Tabulator-backed data table, and the client half of the page/data-region split (REQ-UI-032, REQ-UI-044, REQ-TECH-025). Assertions target rendered markers and behaviour, not pixels. `e2e/` is development and CI tooling: it is never served, no shipped page or template references it (so it adds no production dependency under REQ-TECH-023/026 and no runtime CDN reference under ASM-TECH-2), and it introduces no build step for frontend code (REQ-TECH-001). It complements the PHP harness above — the normative coverage stays in the Go API (REQ-TECH-021).

## 7. Reference Hardware (REQ-TECH-010/011)

The performance targets are measured on: 4 vCPU, 8 GB RAM, SSD, MariaDB on the same host, Go API and PHP-FPM on the same host, project size 1,000 records × 200 fields × 10 events. Against this: admin API reads < 300 ms p95 (REQ-TECH-010); full flat CSV export < 10 s and streamed (REQ-TECH-011).

## 8. Resolved Deferred Items

| Deferred in | Resolution here |
|---|---|
| "reference hardware (see design document)" (REQ-TECH-010) | defined in §7 |
| static-binary mechanism (REQ-TECH-013) | pure-Go SQLite driver + `CGO_ENABLED=0` (§3/§5) |
| Swagger UI hosting (REQ-TECH-004) | hand-maintained OpenAPI 3.1 + vendored `swagger-ui-dist`, no generator (§3) |

## 9. Open Items

| Item | Owner |
|---|---|
| exact nginx configuration file for the deployment target | operations (rules fixed in §5) |
| Node.js 20 LTS on developer workstations and CI images — Playwright ≥ 1.62 refuses to run on the Node 18.19.1 currently installed, so `e2e/` tests cannot execute until it is upgraded (§2, REQ-TECH-028) | operations / each developer |
