# CLARA — Clinical Logbook for Automated Research Assistance

A research electronic data capture (EDC) system for clinical studies: a web-based platform to collect project data, with a Go + OpenAPI backend and a PHP web application.

## Start here

[VISION_AND_REQUIREMENTS.md](VISION_AND_REQUIREMENTS.md) is the **source of truth** for this project. It contains the product vision, the data model, the API contract used by Fiona, UI behavior, project modes, and open decisions. All other documentation derives from it.

## Documentation map

| Document | Role |
|---|---|
| [VISION_AND_REQUIREMENTS.md](VISION_AND_REQUIREMENTS.md) | Origin document — vision, requirements, and binding decisions |
| Requirements documents | Derived, detailed functional requirements (one area per document) |
| Plan documents | Implementation sequencing and milestones |
| Design documents | Architecture and technical design derived from the above |

`AGENTS.md` files (where present) outline rules for LLM-assisted development based on this structure.

## Key concepts at a glance

- **Projects** — secured by tokens mapped to user accounts; ethics approval (REK), PI, roles
- **Arms, events, instruments, fields** — the project structure; instruments are defined by a data dictionary
- **Authentication** — OAuth2 with fallback to up to three LDAP servers; table-based admin bootstrap
- **Stack** — Go API (OpenAPI/Swagger), PHP rendering layer, MariaDB in production / SQLite in development, Bootstrap 5 frontend without a build step


## Create documentation (PDF)

```
pandoc *.md */*.md -o /tmp/result.pdf --pdf-engine=xelatex -V mainfont="Lucida Grande" -V geometry:margin=0.5in
```