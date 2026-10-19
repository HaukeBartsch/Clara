# AI Assistant — Design

**Project:** Clinical Study Management System (REDCap API Replacement)\
**Implements:** `Requirements/AI_Assistant_Requirements.md`\
**Date:** 2026-10-16

## 1. Shape

One page, one endpoint, two hand-written MCP halves. The browser holds the model; the PHP layer
holds the tools and every permission decision. No Go changes, no migrations, no new runtime.

```
browser (is_admin session)                      server
┌─────────────────────────────┐      JSON-RPC   ┌──────────────────────┐      ┌────────────┐
│ assistant.js  ── WebLLM     │ ── POST /mcp ─→ │ Mcp\Server → Tools   │ ───→ │ ApiClient  │→ Go API
│ mcp-client.js ── d3 charts  │ ←── result ──── │   ├ Stats (PHP)      │      │ DataApi    │
│ confirm dialog (writes)     │                 │   └ mode gate        │      └────────────┘
└─────────────────────────────┘                 └──────────────────────┘
```

## 2. MCP endpoint (`web/app/Mcp/Server.php`)

`POST /mcp`, route row `'guard' => 'admin'`, `'region' => 'mcp'`: the router's CSRF check runs
before the controller (REQ-UI-005), and the Accept split delivers the request to
`AssistantController::data()`, which hands the raw body to `Mcp\Server::handle()`.

* Body read from `php://input`, capped at 256 KiB; malformed JSON answers JSON-RPC `-32701`/`-32600`.
* Methods: `initialize` (answers protocolVersion `2025-03-26`, `serverInfo{name:"clara"}`,
  `tools:{}` capability), `notifications/initialized` (no response content), `ping`,
  `tools/list`, `tools/call` (REQ-AI-004, REQ-AI-005). Unknown methods answer `-32601`.
* A tool result is `{content:[{type:"text",…}], structuredContent:{…}, isError:bool}` — the text
  half is what the model reads; `structuredContent` carries the display hints of §6. Tool failures
  are results with `isError:true`, never JSON-RPC errors, so the model can see and re-plan (REQ-AI-012).
* The endpoint keeps no state: each call is one complete request/response (REQ-AI-005); the chat
  history lives in the browser only.

## 3. Tool registry (`web/app/Mcp/Tools.php`)

Each tool: `name`, `description` (written to steer a small tool-calling model), `inputSchema`
(JSON Schema draft-07 subset), `mode` ∈ read|write. The dispatcher checks the session's write flag
before executing any `write` tool (REQ-AI-006) and returns an `isError` result naming the mode —
the UI hides nothing to achieve this; the check is the rule.

| Tool | Mode | Upstream call |
|---|---|---|
| `list_projects` | read | `GET /api/v1/projects` → id, name, organization, record/instrument/field counts |
| `get_project_setup` | read | project detail + `/arms`, `/instruments`, `/instrument-event-mapping` |
| `get_record_status` | read | `GET …/record-status` (rows capped) |
| `export_records` | read | data-API `content=record` via `DataApi::forMember`, `fields[]`/`events[]`, rows capped at 200 with a `truncated` flag |
| `marginal_stats` | read | export one field (≤ 50 000 rows) → §4 stats |
| `ttest` | read | export two fields → §4 Welch |
| `create_records` | write | data-API `content=record&action=import&data[…]` (§8.6 semantics; rows ≤ 500) |
| `add_event` | write | `POST …/events` `{arm_num, event_name}` |
| `add_instrument` | write | `POST …/instruments` `{name}` |
| `set_instrument_survey` | write | `PUT …/instruments/{iid}` `{is_survey}` (whitelisted attributes only) |
| `set_instrument_repeatable` | write | `PUT …/instruments/{iid}/repeating` events list |
| `assign_instruments_to_events` | write | `PUT …/instrument-event-mapping` |

Every upstream `ApiException` becomes an `isError` result carrying the API's stable code — never a
stack detail (REQ-CFG-005). Reads and writes are audited by the API itself, as on every other page
(REQ-AUD); the assistant adds no separate audit channel.

## 4. Statistics (`web/app/Mcp/Stats.php`, REQ-AI-010)

Pure PHP over exported rows — missing values skipped, numerics parsed leniently:

* **marginal**: value → count + share for categorical fields; for numeric fields additionally n,
  mean, SD, min, max and up to 20 histogram bins (equal width). Bins are what the d3 chart draws.
* **Welch t-test**: two groups from a grouping field (exactly two distinct group values required),
  t, df (Welch–Satterthwaite), two-sided p via the regularized incomplete beta function
  (`betacf` continued fraction, Numerical Recipes form — stdlib math only).

Results are plain JSON numbers/strings; tool text summaries round to four significant digits.

## 5. Browser client (`web/assets/js/`)

* **`mcp-client.js`** — `listTools()` / `callTool(name, args)` over `fetch(POST /mcp)` with
  `Accept: application/json`, `Content-Type: application/json`, `X-CSRF-Token` from the shell's meta
  (REQ-UI-005); a JSON-RPC error or non-2xx throws with the server's code. ~100 lines, DEV-AI-2.
* **`assistant.js`** — on ready: probe WebGPU (`navigator.gpu`), fetch `/assets/models/app-config.json`;
  missing either → render the translated explanation and stop (REQ-AI-012). Otherwise
  `CreateWebEngine` from the vendored `../vendor/webllm/…` ESM bundle with the local app config,
  preload the configured model, then fetch `tools/list` and keep them as engine tools.
  * Schema adapter: MCP `{name, description, inputSchema}` → engine function schema
    `{name, description, parameters: inputSchema}`.
  * Loop: user message → streamed tokens appended to the chat (REQ-AI-012) → tool calls executed →
    results fed back → final text. A `write` tool pauses for the confirmation dialog showing the
    formatted arguments before `callTool` (REQ-AI-007); cancel feeds a "declined" result back to the model.
  * Rendering: assistant/user bubbles as `textContent`; `structuredContent.display` of `"table"` →
    `table-sm`, `"bar_chart"`/`"histogram"` → d3 chart in an inline SVG (REQ-AI-011). No innerHTML
    with model or server data (REQ-UI-004).
  * Failure reporting: a failed turn reaches three channels, because the generic sentence alone left
    nothing to act on. `console.error` keeps the Error itself (engine subclasses carry their diagnosis
    in the name, e.g. `UnsupportedModelIdError`); the status line carries one translated clause
    naming it (`admin.assistant.error.turn_detail`); and the chat gets a collapsed details block with
    where the loop stood — stage, round, tool — beside what is loaded: model id, engine, tool count,
    WebGPU adapter, user agent. It holds counts and identifiers only, never conversation text: record
    values are data the model reports on, not something to paste into a bug report (REQ-AI-002).
* **Policy needed to compile the model**: `Response::CSP` carries `'wasm-unsafe-eval'` in `script-src`.
  Fetching `model_lib` and the shards is `connect-src 'self'`, but *compiling* WebAssembly is judged
  against `script-src`, so a bare `'self'` policy fails inside the engine with "Refused to create a
  WebAssembly object" while every artifact on disk is fine — the model then never loads. The client
  tells the two apart: a refusal names the header (`admin.assistant.error.wasm_blocked`), any other
  engine failure names its message and the fetch script. See `User_Interface_Design.md` §3.2 for why
  this is the policy's only relaxation.

## 6. Section page and mode toggle

* `Navigation::adminSections()` gains `['key' => 'assistant', …]` after `monitoring` (DEV-AI-1);
  `ControlPanelController::renderSection()` dispatches `assistant` to a page rendering
  `views/admin/assistant.php` — chat pane, model status card, write-mode switch, script extras
  `[d3.v7.min.js, assistant.js]`.
* The toggle is a form POST `/admin?action=assistant_mode` (`on|off`) setting the session flag; the
  button state and a persistent read-only notice render from it (REQ-AI-006). `Session::assistantWriteMode()`
  / `setAssistantWriteMode()` hold the flag; it resets with the session.
* The MCP client reads the current mode only as a display default — the server never trusts it (§3).

## 7. Artifacts

* `web/assets/vendor/webllm/` — the pinned `@mlc-ai/web-llm` ESM bundle + licence (admitted row,
  `Technology_Stack_Design.md` §3). Served like every vendor asset.
* `web/assets/models/` — git-ignored; `tools/fetch_assistant_model.sh` downloads a tool-capable
  compiled model (default a Qwen3-class q4 artifact) plus its `mlc-chat-config.json`,
  `ndarray-cache.json`, the tokenizer files that config lists, the weight shards the manifest lists,
  the `tensor-cache.json` manifest the engine loads those shards through (named by the bundle, not
  by any file in the directory) and the compiled WebGPU library, then writes `app-config.json` whose
  URLs all point into this directory (REQ-AI-003). The script keeps any file already present with
  the size it promises, so a run interrupted halfway is finished by running it again; `--force`
  fetches everything anew. Absent or incomplete artifacts are a supported state (§5).

## 8. Tests

Playwright per `Plan/AI_Assistant.md` D4; stats helpers verified against hand-computed values,
including the p-value against a known reference (e.g. t = 2.1, df = 18 → p ≈ 0.0489).
