# AI Assistant — Requirements Analysis

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Source:** `VISION_AND_REQUIREMENTS.md` ("Later: alternative admin WebLLM interface"); delivery plan `Plan/AI_Assistant.md`\
**Status:** Approved for development handoff\
**Date:** 2026-10-16

## 1. Purpose

An alternative, conversation-driven administration surface — branded **Clara** — for installation
administrators. A tool-capable language model runs entirely in the administrator's browser (WebLLM
on WebGPU) and reaches this application's API through an MCP server hosted by the PHP layer, in a
read-only mode or an explicitly enabled read-write mode. The use cases fixed by the master spec are:
project information, bulk record creation from a list or spreadsheet paste, longitudinal-study
design suggestions applied to Setup, statistics about stored data (marginal statistics and t-tests;
mixed models deferred per DEV-AI-3), and questions answered across projects.

This area adds no model server, no external inference service and no additional runtime: the model
is an asset this application serves itself, and every statistic is computed by the existing PHP and
Go layers from data the API already exposes.

## 2. Surface and identity

| ID | Requirement |
|---|---|
| REQ-AI-001 | The assistant lives in the Control Panel as one further section (`/admin?section=assistant`, DEV-AI-1), reachable by `is_admin` only — the same guard every other Control Panel section uses (REQ-UI-047). It is not a separate login surface: it acts as the signed-in administrator, and no second identity or credential exists. |
| REQ-AI-002 | Model inference happens entirely in the browser via WebLLM/WebGPU (the master spec's "webllm based interface"). No prompt, completion, tool argument or record value is ever sent to a model outside the administrator's browser; the server runs no inference and proxies none. |
| REQ-AI-003 | Model artifacts — weights, compiled library, chat config — are served from this application's own origin under `web/assets/models/`, fetched at installation time (ASM-AI-2). No runtime request leaves the browser for model or library bytes: the assistant works with no internet access exactly like every other page (REQ-TECH-001, ASM-TECH-2, REQ-TECH-027). |

## 3. MCP integration

| ID | Requirement |
|---|---|
| REQ-AI-004 | Tools are exposed over the Model Context Protocol: the PHP layer serves a same-origin MCP endpoint (`POST /mcp`, JSON-RPC 2.0 — `initialize`, `tools/list`, `tools/call`) on the administrator's session with the route's CSRF token, and the browser client speaks exactly that protocol. The endpoint proxies every tool through `Clara\ApiClient`/`Clara\DataApi` under the acting user's identity; the browser never calls `/api/v1/*` or the data API directly (REQ-UI-002, GD-1). |
| REQ-AI-005 | The endpoint answers only simple JSON request/response — no server-initiated streams, sampling or elicitation. A model-generated call is executed exactly once per `tools/call`, and its result is returned complete. |

## 4. Read-only and read-write modes

| ID | Requirement |
|---|---|
| REQ-AI-006 | The assistant starts **read-only**. The administrator may switch the current session to read-write explicitly; switching back is one click. Every tool declares its mode server-side, and a write-mode tool called while the session is read-only is refused there (REQ-AUTH-026: the decision lives where it can be enforced) — hiding or listing tools never *is* the enforcement. |
| REQ-AI-007 | Every write executes only after an interactive confirmation that shows the concrete payload (project, records/rows or objects to create or change); cancelling performs nothing. The confirmation supplements REQ-AI-006 and replaces neither it nor the API's own permission checks. |

## 5. Toolset

| ID | Requirement |
|---|---|
| REQ-AI-008 | Read tools cover the master spec's questions: `list_projects`, `get_project_setup` (arms, events, instruments, mapping), `get_record_status`, `export_records` (bounded rows), `marginal_stats(project, field)` and `ttest(project, value_field, group_field)` (Welch). Cross-project questions are answered by composing these calls. |
| REQ-AI-009 | Write tools cover the master spec's actions: `create_records` (bulk rows, data-API import semantics of §8.6), `add_event`, `add_instrument`, `set_instrument_survey`, `set_instrument_repeatable`, and `assign_instruments_to_events`. Each maps 1:1 onto an existing administration/data API call — the assistant adds no new write path to the API. |

## 6. Statistics and presentation

| ID | Requirement |
|---|---|
| REQ-AI-010 | Statistics are computed server-side in PHP from data-API exports — marginal frequencies, mean/standard deviation/histogram for numeric fields, Welch's t-test with two-sided p-value. No external stats runtime (R, Python) is introduced; results travel as JSON values and small row sets, bounded so a tool result cannot flood the model context. |
| REQ-AI-011 | Tool results are presented as text and Bootstrap `table-sm` tables plus d3.js charts (the already-admitted library, REQ-UI-064's precedent) rendered in the browser from the structured result; no server-rendered images. The vision's ggplot2/matplotlib graphics stay deferred with DEV-AI-3. |
| REQ-AI-012 | Model output streams token by token into the chat, and every tool call shows as an execution line (name, running, succeeded/failed) so the administrator sees what the model did. A browser without WebGPU, or an installation without fetched model artifacts, renders an explanatory state instead of a broken page (REQ-UI-003). |
| REQ-AI-013 | The assistant's UI strings are translated server-side and injected like every other page's (REQ-UI-008); the page renders in the application shell with the Control Panel's left panel (REQ-UI-046). |

## 7. Assumptions

| ID | Assumption |
|---|---|
| ASM-AI-1 | Administrators' workstations have WebGPU-capable browsers and enough VRAM for the installed quantized tool-calling model (a q4-class 8B model needs roughly 4–5 GB); the page degrades to a message, not a crash, when they do not. |
| ASM-AI-2 | Model artifacts are placed under `web/assets/models/` at installation time by `tools/fetch_assistant_model.sh` (network available then); production hosts serve them without ever fetching them. |

## 8. Deviations

| ID | Deviation | Source |
|---|---|---|
| DEV-AI-1 | The Control Panel's section list (REQ-UI-047) gains **Assistant** between Monitoring and Configuration Check. Owner instruction of 2026-10-16; the closed-list rule is amended by this entry. | REQ-UI-047 |
| DEV-AI-2 | The MCP client and server are hand-written minimal implementations (browser ES module; PHP JSON-RPC over the existing router) instead of an SDK: the npm SDK needs a build step forbidden by REQ-TECH-001, and no PHP library is admissible outside the critical-function allowlist (REQ-TECH-026). Only the subset REQ-AI-004/005 names is implemented; conformance is checked against the protocol shapes. | REQ-TECH-001, REQ-TECH-026 |
| DEV-AI-3 | Linear mixed effects models and ggplot2/matplotlib graphics from the master spec's use-case list are deferred to a follow-up requirement set: they need a stats runtime this area deliberately does not add (owner decision 2026-10-16 — "leave out the stats runtime for now; display stats values using text/tables and d3.js graphics"). Marginal statistics and t-tests cover the near-term analysis questions. | VISION §"Later: alternative admin WebLLM interface" |
