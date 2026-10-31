# AI Assistant — Requirements Analysis

**Project:** CLARA — Clinical Logbook for Automated Research Assistance (REDCap API Replacement)\
**Source:** `VISION_AND_REQUIREMENTS.md` ("Later: alternative admin WebLLM interface"); delivery plan `Plan/AI_Assistant.md`\
**Status:** Approved for development handoff\
**Created:** 2026-10-16

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

> **Key constraints** — a summary for quick orientation; the cited IDs and sections are normative.
>
> - **Inference stays in the browser.** No prompt, completion, tool argument, or record value is sent to a model outside the administrator's browser; the server runs no inference and proxies none (REQ-AI-002).
> - **No runtime downloads.** Model artifacts are served from `web/assets/models/` on the application's own origin, so the assistant works without internet access (REQ-AI-003).
> - **Read-only until switched.** A write tool is refused server-side while the session is read-only, and every write runs only after a confirmation that shows its payload (REQ-AI-006, REQ-AI-007).
> - **No new permissions.** The assistant acts as the signed-in `is_admin` user, and every tool maps onto an existing API call that keeps its own checks (REQ-AI-001, REQ-AI-009).

## 2. Surface and identity

| ID | Requirement |
|---|---|
| REQ-AI-001 | The assistant MUST live in the Control Panel as one further section (`/admin?section=assistant`, DEV-AI-1), reachable by `is_admin` only — the same guard every other Control Panel section uses (REQ-UI-047). It MUST NOT be a separate login surface: it acts as the signed-in administrator, and no second identity or credential exists. |
| REQ-AI-002 | Model inference MUST happen entirely in the browser via WebLLM/WebGPU (the master spec's "webllm based interface"). A prompt, completion, tool argument or record value MUST NOT be sent to a model outside the administrator's browser; the server MUST NOT run or proxy inference. |
| REQ-AI-003 | Model artifacts — weights, compiled library, chat config — MUST be served from this application's own origin under `web/assets/models/`, fetched at installation time (ASM-AI-2). At runtime the browser MUST NOT request model or library bytes from any other origin: the assistant works with no internet access exactly like every other page (REQ-TECH-001, ASM-TECH-2, REQ-TECH-027). |

## 3. MCP integration

| ID | Requirement |
|---|---|
| REQ-AI-004 | Tools MUST be exposed over the Model Context Protocol: the PHP layer serves a same-origin MCP endpoint (`POST /mcp`, JSON-RPC 2.0 — `initialize`, `tools/list`, `tools/call`) on the administrator's session with the route's CSRF token, and the browser client speaks exactly that protocol. The endpoint MUST proxy every tool through `Clara\ApiClient`/`Clara\DataApi` under the acting user's identity; the browser MUST NOT call `/api/v1/*` or the data API directly (REQ-UI-002, GD-1). |
| REQ-AI-005 | The endpoint MUST answer only simple JSON request/response — no server-initiated streams, sampling or elicitation. A model-generated call MUST be executed exactly once per `tools/call`, and its result MUST be returned complete. |

## 4. Read-only and read-write modes

| ID | Requirement |
|---|---|
| REQ-AI-006 | The assistant MUST start **read-only**. The administrator MAY switch the current session to read-write explicitly; switching back MUST take one click. Every tool MUST declare its mode server-side, and a write-mode tool called while the session is read-only MUST be refused there (REQ-AUTH-026: the decision lives where it can be enforced) — hiding or listing tools never *is* the enforcement. |
| REQ-AI-007 | Every write MUST execute only after an interactive confirmation that shows the concrete payload (project, records/rows or objects to create or change); cancelling MUST perform nothing. The confirmation supplements REQ-AI-006 and replaces neither it nor the API's own permission checks. |

## 5. Toolset

| ID | Requirement |
|---|---|
| REQ-AI-008 | Read tools MUST cover the master spec's questions: `list_projects`, `get_project_setup` (arms, events, instruments, mapping), `get_record_status`, `export_records` (bounded rows), `marginal_stats(project, field)` and `ttest(project, value_field, group_field)` (Welch). Cross-project questions are answered by composing these calls. |
| REQ-AI-009 | Write tools MUST cover the master spec's actions: `create_records` (bulk rows, data-API import semantics of `User_Interface_Design.md` §8.6), `add_event`, `add_instrument`, `set_instrument_survey`, `set_instrument_repeatable`, and `assign_instruments_to_events`. Each MUST map 1:1 onto an existing administration/data API call — the assistant adds no new write path to the API. |

## 6. Statistics and presentation

| ID | Requirement |
|---|---|
| REQ-AI-010 | Statistics MUST be computed server-side in PHP from data-API exports — marginal frequencies, mean/standard deviation/histogram for numeric fields, Welch's t-test with two-sided p-value. No external stats runtime (R, Python) is introduced; results MUST travel as JSON values and row sets bounded so a tool result cannot flood the model context. |
| REQ-AI-011 | Tool results MUST be presented as text and Bootstrap `table-sm` tables plus d3.js charts (the already-admitted library, REQ-UI-064's precedent) rendered in the browser from the structured result; the server MUST NOT render images. The master spec's ggplot2/matplotlib graphics stay deferred with DEV-AI-3. |
| REQ-AI-012 | Model output MUST stream token by token into the chat, and every tool call MUST show as an execution line (name, running, succeeded/failed) so the administrator sees what the model did. A browser without WebGPU, or an installation without fetched model artifacts, MUST render an explanatory state instead of a broken page (REQ-UI-003). |
| REQ-AI-013 | The assistant's UI strings MUST be translated server-side and injected like every other page's (REQ-UI-008); the page MUST render in the application shell with the Control Panel's left panel (REQ-UI-046). |

## 7. Assumptions

| ID | Assumption |
|---|---|
| ASM-AI-1 | Administrators' workstations have WebGPU-capable browsers and enough VRAM for the installed quantized tool-calling model (a q4-class 8B model needs roughly 4–5 GB); the page degrades to a message, not a crash, when they do not. |
| ASM-AI-2 | Model artifacts are placed under `web/assets/models/` at installation time by `tools/fetch_assistant_model.sh` (network available then); production hosts serve them without ever fetching them. |

## 8. Decision Records

Each record states a decision this area made where the master spec, another requirement, or a common default points elsewhere, and why. The IDs are stable anchors cited by the requirements above.

| ID | Decision | Rationale |
|---|---|---|
| DEV-AI-1 | The Control Panel's section list (REQ-UI-047) includes **Assistant**, between Monitoring and Configuration Check. | Owner instruction. REQ-UI-047 fixes a closed list of sections; this record is the amendment that admits the new one. |
| DEV-AI-2 | The MCP client and server are hand-written minimal implementations — a browser ES module and a PHP JSON-RPC endpoint on the existing router — implementing only the subset REQ-AI-004/005 names. | The npm MCP SDK needs a build step (forbidden, REQ-TECH-001), and no PHP MCP library is admissible outside the critical-function allowlist (REQ-TECH-026). The cost is that conformance is checked against the protocol shapes rather than inherited from a reference implementation. |
| DEV-AI-3 | Linear mixed effects models and ggplot2/matplotlib graphics from the master spec's use-case list ("Later: alternative admin WebLLM interface") are out of scope for this area; marginal statistics and t-tests cover the analysis questions, shown as text, tables and d3.js graphics. | Both need a statistics runtime this area deliberately does not add (owner decision: "leave out the stats runtime for now; display stats values using text/tables and d3.js graphics"). They belong to a follow-up requirement set. |
