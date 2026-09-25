# AGENTS.md

Rules for LLM-assisted development on CLARA — "Clinical Logbook for Automated Research Assistance", a research electronic data capture (EDC) system for clinical studies that replaces the REDCap API. This file lives at the repository root; add folder-level `AGENTS.md` files in folders that benefit from narrower rules, keeping them consistent with this one.

## Source of truth and documentation workflow

- [VISION_AND_REQUIREMENTS.md](VISION_AND_REQUIREMENTS.md) is the origin document — vision, requirements, and binding decisions. All other documentation derives from it; when documents conflict, it wins.
- Software development documents live in `Requirements/*.md`, `Plan/*.md`, and `Design/*.md`. Each functional area has one document per folder, named after the area (e.g. `Requirements/Data_Validation_Requirements.md` → `Plan/Data_Validation.md` → `Design/Data_Validation_Design.md`).
- Functional areas: API_Endpoints, Audit_Logging, Authentication_Authorization, Data_Export_Anonymization, Data_Validation, Database_Schema, Project_Charter, System_Configuration, Technology_Stack, User_Interface.
- Requirements carry stable IDs (`REQ-<AREA>-nnn`, assumptions `ASM-…`). Reference existing IDs when writing or changing behavior; introduce new ones in the Requirements document first, then reflect them in Plan and Design.
- Design documents are normative for their area (e.g. `Design/Technology_Stack_Design.md` fixes toolchain selection). A deviation from a design document is a requirements-level change, not an implementation detail.

## Repository layout

- `api/` — Go API (module `csms/api`). Current packages under `api/internal/`: `config`, `db`, `migrations`, `dataapi`, `validate`.
- `Design/Technology_Stack_Design.md` §4 defines the normative target layout (`cmd/server`, `httpapi`, `redcap`, `admin`, `authz`, `validation`, `audit`, …). Where the current tree differs, follow the design document and place new code accordingly.
- `web/` — PHP web application (planned; not yet present in the repository). All data access from PHP goes exclusively through the API.
- `assets/` — example data dictionaries and the historic FIONA user-management application (`table_based_authentication_plus_user_management/`); `AC.php` there is the authentication control script every FIONA page used to establish a session. When no requirement says otherwise, follow that example layout for interfacing PHP with the web application: pull JSON from the backend, populate rendering targets on the client.

## Stack facts (normative — do not re-decide)

Pinned versions and the production dependency allowlist are fixed in `Design/Technology_Stack_Design.md`. In short:

- Go API built as a single static binary (`CGO_ENABLED=0`, pure-Go SQLite driver); standard library preferred over libraries.
- SQLite for development and tests, MariaDB 11.x in production; SQL targets features common to both, with documented exceptions.
- PHP 8.4 rendering layer with no Composer production dependencies; nginx + PHP-FPM in production.
- Frontend is vanilla ES2020 JavaScript with Bootstrap 5.3 — vendored locally, never loaded from a CDN at runtime (the application must work without internet access). Download library assets once into local directories.
- No webpack or similar technology that requires a build step for the website frontend.
- Configuration comes from environment variables; `.env.example` documents the full set.

## Dev environment tips

- Run Go tooling from `api/`: `go test ./...`, `go vet ./...`.
- For golang place `_test.go` files next to the implementation files.
- Database migrations live in `api/internal/migrations/{sqlite,mariadb}/` as numbered files (`0001_schema.sql`, …); add a file for both dialects when a change is dialect-specific.
- Code style for JavaScript is double quotes, no semicolons, use functional patterns where possible.
- Bootstrap: use condensed tables (`table-sm` class) for all tables; keep tables responsive on tablets and phones.
- For performant table rendering on the website use Tabulator (https://unpkg.com/tabulator-tables — vendored locally per the rule above).
- The web interface font is Geist (https://fontsource.org/fonts/geist/use), served from a local copy.
