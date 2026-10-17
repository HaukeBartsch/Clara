# AI Assistant Plan

Delivers `Requirements/AI_Assistant_Requirements.md`: a Control Panel section where an in-browser
WebLLM model drives administration work through a same-origin MCP endpoint. Three deliverables land
together; nothing here touches the Go API or the database schema.

## D1 — MCP endpoint and tools (PHP)

`web/app/Mcp/Server.php` implements the JSON-RPC subset REQ-AI-004 names (`initialize`,
`notifications/initialized`, `tools/list`, `tools/call`) as a plain request/response handler
(REQ-AI-005), mounted at `POST /mcp` through the existing router with the `admin` guard and the
route CSRF check (DEV-AI-2). `web/app/Mcp/Tools.php` holds the tool registry — name, description,
JSON-Schema input, mode (`read` | `write`) — and executes each call against the same API calls the
page controllers already make: `GET /api/v1/projects`, project setup reads, `record-status`,
data-API export/import via `DataApi::forMember`, Setup writes for events/instruments/mapping.
Mode enforcement is one check in the dispatcher (REQ-AI-006).

`web/app/Mcp/Stats.php` computes marginal statistics and Welch's t-test from exported rows
(REQ-AI-010); every tool result is bounded (row caps, value truncation) before it reaches the model.

## D2 — Assistant section (PHP view + JS client)

A new `Navigation::adminSections()` entry renders `views/admin/assistant.php` inside the Control
Panel shell (REQ-AI-013), with a session-scoped read-write toggle posted to `/admin?action=assistant_mode`
(REQ-AI-006). `web/assets/js/mcp-client.js` is the browser half of DEV-AI-2 — fetch-based JSON-RPC
carrying `X-CSRF-Token`. `web/assets/js/assistant.js` preloads the tool-capable model from local
artifacts (REQ-AI-003), streams tokens, adapts MCP tool definitions to the engine's function schema,
shows execution lines and confirmation dialogs for writes (REQ-AI-012, REQ-AI-007), and renders
structured results as `table-sm` tables and d3 charts (REQ-AI-011).

## D3 — Artifacts and installation

`@mlc-ai/web-llm` is vendored under `web/assets/vendor/webllm/` (one admitted row in
`Design/Technology_Stack_Design.md` §3); model weights are fetched at install time by
`tools/fetch_assistant_model.sh` into git-ignored `web/assets/models/`, which also carries the
rewritten app-config the engine loads (REQ-AI-003, ASM-AI-2). INSTALL.md gains the fetch step.

## D4 — Tests

Playwright (`e2e/tests/assistant.spec.ts`, REQ-TECH-028): the section renders for an admin with a
"model not installed / WebGPU unavailable" state; the MCP endpoint is exercised from the page's own
session — `tools/list` shape, a read tool round-trip, and the server-side refusal of a write tool in
read-only mode (REQ-AI-006). Stats helpers are checked against hand-computed values.
