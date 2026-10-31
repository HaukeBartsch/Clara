# AGENTS.md

Rules for LLM-assisted development on CLARA — "Clinical Logbook for Automated Research Assistance", a research electronic data capture (EDC) system for clinical studies that replaces the REDCap API. This file lives at the repository root; add folder-level `AGENTS.md` files in folders that benefit from narrower rules, keeping them consistent with this one.

## Source of truth and documentation workflow

- [VISION_AND_REQUIREMENTS.md](VISION_AND_REQUIREMENTS.md) is the origin document — vision, requirements, and binding decisions. All other documentation derives from it; when documents conflict, it wins.
- Software development documents live in `Requirements/*.md` and `Design/*.md`. Each functional area has one document per folder, named after the area (e.g. `Requirements/Data_Validation_Requirements.md` → `Design/Data_Validation_Design.md`). The chain is `VISION_AND_REQUIREMENTS.md` → Requirements → Design; every normative statement lives in one of those three.
- `Plan/*.md` holds delivery plans, named after the delivery rather than an area (`Plan/Web_Implementation.md`, `Plan/AI_Assistant.md`): the order of work, milestones, and exit criteria of one delivery. A plan sits outside the chain — it introduces no requirement IDs and no behavior, and a rule that must outlast the delivery moves into the Design document of its area. Per-area plans existed until 2026-10-28; the "plan" citations still found in Requirements and Design quote them as of commit `2b0c371`.
- Functional areas: AI_Assistant, API_Endpoints, Audit_Logging, Authentication_Authorization, Data_Export_Anonymization, Data_Validation, Database_Schema, ODM_Interchange, Project_Charter, System_Configuration, Technology_Stack, User_Interface.
- Reference codes (`REQ-…`, `ASM-…`, `DEV-…`, `GD-n`, `BR-nnn`, `D-n`, `F<n>`, Sequence A…J) and domain terms are defined in the glossary, `Requirements/Project_Charter_Requirements.md` §9.
- Requirements carry stable IDs (`REQ-<AREA>-nnn`, assumptions `ASM-…`). Reference existing IDs when writing or changing behavior; introduce new ones in the Requirements document first, then reflect them in Design.
- Design documents are normative for their area (e.g. `Design/Technology_Stack_Design.md` fixes toolchain selection). A deviation from a design document is a requirements-level change, not an implementation detail.

## Repository layout

- `api/` — Go API (module `csms/api`). Packages under `api/internal/`: `httpapi` (routing, middleware), `dataapi` (REDCap-compatible `POST /api/`), `admin` (`/api/v1/`), `authz`, `validate`, `audit`, `db`, `migrations`, `config`, `metrics`, `mailer`, `passhash`, `totp`, and the test-only `testdb`.
- `Design/Technology_Stack_Design.md` §4 defines the normative layout, and the tree matches it. Place new code according to that document.
- `web/` — PHP web application: `app/` holds the framework-free application code (Router, Auth, the named-source credential race — `LdapRace`, `SubprocessLdap*` — I18n, controllers under `app/Controllers/`), `views/` the rendering templates, `public/` the entry point and `assets/` vendored frontend libraries. All data access from PHP goes exclusively through the API; PHP tests live in `tests/php/`.
- `deploy/` — reverse-proxy reference configurations for production (`nginx/clara.conf`, `apache2/clara.conf`). They implement the normative routing rules of `Design/Technology_Stack_Design.md` §5; treat them as production code: a change to one lands in both, INSTALL.md §11 documents the same rules, and `e2e/tests/deployment.spec.ts` checks that all three still agree.
- `assets/` — example data dictionaries and the historic FIONA user-management application (`table_based_authentication_plus_user_management/`); `AC.php` there is the authentication control script every FIONA page used to establish a session. When no requirement says otherwise, follow that example layout for interfacing PHP with the web application: pull JSON from the backend, populate rendering targets on the client.

## Stack facts (normative — do not re-decide)

Pinned versions and the production dependency allowlist are fixed in `Design/Technology_Stack_Design.md`. In short:

- Go API built as a single static binary (`CGO_ENABLED=0`, pure-Go SQLite driver); standard library preferred over libraries.
- SQLite for development and tests, MariaDB 11.x in production; SQL targets features common to both, with documented exceptions.
- PHP 8.3 or 8.4 rendering layer (either is acceptable — keep code inside the feature set common to both); Composer dependencies only from the critical-function allowlist (`Design/Technology_Stack_Design.md` §3, REQ-TECH-026 — authentication, session handling, i18n), otherwise the standard distribution; nginx + PHP-FPM in production, with Apache httpd 2.4 as an approved alternative implementing the same routing rules (`deploy/` carries both reference files).
- Frontend is vanilla ES2020 JavaScript with Bootstrap 5.3 — vendored locally, never loaded from a CDN at runtime (the application must work without internet access). Download library assets once into local directories.
- No webpack or similar technology that requires a build step for the website frontend.
- Configuration comes from environment variables; `.env.example` documents the full set.

## Dev environment tips

- Run Go tooling from `api/`: `go test ./...`, `go vet ./...`.
- Run PHP tests from the repository root: `php tests/php/run.php` (runs the whole suite; tests live in `tests/php/` with shared helpers in `tests/php/support.php`).
- For golang place `_test.go` files next to the implementation files.
- Database migrations live in `api/internal/migrations/{sqlite,mariadb}/` as numbered files (`0001_schema.sql`, …); add a file for both dialects when a change is dialect-specific.
- Code style for JavaScript is double quotes, no semicolons, use functional patterns where possible.
- Bootstrap: use condensed tables (`table-sm` class) for all tables; keep tables responsive on tablets and phones.
- For performant table rendering on the website use Tabulator (https://unpkg.com/tabulator-tables — vendored locally per the rule above).
- The web interface font is Geist (https://fontsource.org/fonts/geist/use), served from a local copy.
