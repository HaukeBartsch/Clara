# Technology Stack — Design

**Project:** Clinical Study Management System (REDCap API Replacement)
**Implements:** `Requirements/Technology_Stack_Requirements.md`
**Date:** 2026-09-21

## 1. Purpose

Fixes the concrete versions, the production dependency set, and the repository layout that the stack constraints (REQ-TECH-001…024) mandate. This document is normative for toolchain selection; any deviation is a requirements-level change.

## 2. Pinned Versions (normative)

| Component | Version | Notes |
|---|---|---|
| Go | 1.26.x (pinned in CI; `go.mod` declares `go 1.26`) | built with `CGO_ENABLED=0` → single static binary (REQ-TECH-013) |
| PHP | 8.4 standard distribution | extensions: `pdo_sqlite`, `pdo_mysql`, `ldap`, `curl`, `openssl`, `mbstring`; no Composer production dependencies (REQ-TECH-023) |
| SQLite | ≥ 3.45 (OS/toolchain-provided) | development and tests (REQ-TECH-005) |
| MariaDB | 11.x LTS (≥ 11.4) | production (REQ-TECH-005) |
| Bootstrap | 5.3.x | vendored under `web/assets/vendor/bootstrap/` (CSS + bundle JS only); no CDN (ASM-TECH-2) |
| JavaScript | vanilla ES2020 | no framework, no build step (REQ-TECH-001) |
| Web server | nginx + PHP-FPM | reverse proxy and TLS termination point (REQ-TECH-015, ASM-TECH-1) |

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
│   ├── app/                        # page controllers, session, CSRF, OAuth2/LDAP flow, API client
│   ├── views/                      # PHP templates (Bootstrap, escaped output)
│   └── assets/
│       ├── app.js                  # vanilla ES2020: branching evaluator, form behavior
│       └── vendor/bootstrap/       # vendored Bootstrap 5.3.x
├── .env.example                    # complete variable inventory (System_Configuration_Design.md §5)
├── .gitignore                      # .env, SQLite files (REQ-CFG-001)
└── ci/run.sh                       # go vet + unit + integration (SQLite) + Fiona fixtures
```

Rules: the PHP layer contains **no** SQL and no direct data access (REQ-TECH-006); the Go API contains **no** session logic (GD-1); neither tree may import the other.

## 5. Build and Deployment

- **Go:** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o api-server ./cmd/server` → one static binary, no runtime language dependency (REQ-TECH-013). The API applies migrations at startup (REQ-DB-003) and fails fast on missing configuration or unreachable database (REQ-TECH-014, REQ-CFG-004).
- **PHP:** static deployment; PHP-FPM pool behind nginx. No build step.
- **Reverse proxy (normative routing rules):** TLS termination; `POST /api/` (data API) and the PHP routes are publicly routable; `/api/v1/*` and the documentation endpoints are NOT routable from the public network (REQ-TECH-018, REQ-AUTH-014); `X-Internal-Service-Token` and `X-Internal-User-Id` are stripped from every externally-originated request (`nginx: proxy_set_header X-Internal-Service-Token ""; proxy_set_header X-Internal-User-Id "";`).
- **Placement:** API and PHP on the same host or trusted internal segment (ASM-TECH-3); inter-component traffic loopback or TLS (REQ-AUTH-034).

## 6. Testing (REQ-TECH-021/022, REQ-TECH-009)

- **Go:** unit tests per package; integration tests boot the real HTTP stack against a temporary SQLite file with `APP_ENV=development`; CI runs them with no external services (OAuth2/LDAP replaced by test doubles in the PHP flow; the API's login endpoint is exercised with fixture emails).
- **REDCap compatibility:** every Fiona call example in `Endpoints.md` is encoded as a table-driven fixture (form-encoded request → expected status + body) run against a seeded project — the charter success criterion 1 as an executable regression test (REQ-TECH-022, REQ-API-037).
- **PHP:** no framework; smoke tests use a minimal stdlib test harness for route dispatch, CSRF validation, and the session flow. The normative coverage lives in the Go API (REQ-TECH-021).

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
