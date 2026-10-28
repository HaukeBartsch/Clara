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
  * No `system` message reaches the engine: Hermes function calling adds its own tool-usage prompt
    and throws `CustomSystemPromptError` on a request that carries one, so Clara's instructions are
    folded into the first user turn (`engineMessages()`) while the stored history keeps its `system`
    entry. Tool calls the engine parses out of a JSON array have no id, and each tool result names
    the call it answers, so the client mints one per call.
  * Loop: user message → streamed tokens appended to the chat (REQ-AI-012) → tool calls executed →
    results fed back → final text. A `write` tool pauses for the confirmation dialog showing the
    formatted arguments before `callTool` (REQ-AI-007); cancel feeds a "declined" result back to the model.
  * **A tool result is wrapped for the model, not just for the protocol.** MLC's compiled template for
    this model (`conv_template.name` `hermes3_llama-3_1`) renders a `tool` turn as its role and its
    content — `roles.tool` plus `role_templates.tool` of `{tool_message}` — and the bundle adds nothing
    of its own (web-llm v0.2.86 contains no `tool_response` string at all). Hermes-3 was trained to
    read a result inside `< tool_response>` markers, so `toolMessage()` puts them there itself: an
    unwrapped payload reads as prose arriving from nowhere, which is what made the model ask again for
    the call it had already been answered. The markers are model-facing formatting, like the system
    prompt; the MCP result on the wire is unchanged.
  * **A repeated call stops the turn.** The same tool with the same arguments twice in one turn means
    the model did not take in the first answer, and a second identical query will not change that — so
    instead of running it up to `MAX_TOOL_ROUNDS` times against its own API, the client marks the line
    `repeated`, says so (`admin.assistant.error.repeat_stopped`) and ends the turn. The refused call is
    still answered in the history: its `tool_calls` entry stays there, and the next question is built on
    these messages, so an unanswered call would follow the user around.
  * The final text is asked for **without** `tools`. While a request carries them, this bundle
    constrains every reply to a JSON array of calls — an empty array means "no call needed" and is
    not followed by prose, and a reply that is not such an array throws
    `ToolCallOutputParseError` — so a tools-enabled request can never phrase the answer. The closing
    turn's text is pushed into the history like any other message, which is also what lets a follow-up
    question see the reply it follows up.
  * Rendering: assistant/user bubbles as `textContent`; `structuredContent.display` of `"table"` →
    `table-sm`, `"bar_chart"`/`"histogram"` → d3 chart in an inline SVG (REQ-AI-011). No innerHTML
    with model or server data (REQ-UI-004).
  * Failure reporting: a failed turn reaches three channels, because the generic sentence alone left
    nothing to act on. `console.error` keeps the Error itself (engine subclasses carry their diagnosis
    in the name, e.g. `UnsupportedModelIdError`); the status line carries one translated clause
    naming it (`admin.assistant.error.turn_detail`); and the chat gets a collapsed details block with
    where the loop stood — stage, round, tool — beside what is loaded: model id, engine, tool count,
    the configured context window and how many characters the last request carried (§7), WebGPU
    adapter, user agent. Those two sizes are what separate a conversation that outgrew its window —
    `ContextWindowSizeExceededError`, thrown mid-turn after the tool work was already done — from a
    model that simply misbehaves. It holds counts and identifiers only, never conversation text: record
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
  compiled model (default `Hermes-3-Llama-3.1-8B-q4f32_1-MLC`) plus its `mlc-chat-config.json`,
  `ndarray-cache.json`, the tokenizer files that config lists, the weight shards the manifest lists,
  the `tensor-cache.json` manifest the engine loads those shards through (named by the bundle, not
  by any file in the directory) and the compiled WebGPU library, then writes `app-config.json` whose
  URLs all point into this directory (REQ-AI-003). The script keeps any file already present with
  the size it promises, so a run interrupted halfway is finished by running it again; `--force`
  fetches everything anew. Absent or incomplete artifacts are a supported state (§5).
* **Which models can be used at all.** The bundle gates `ChatCompletionRequest.tools` on model ids it
  hard-codes (`functionCallingModelIds`): the compiled Hermes-2-Pro and Hermes-3 models, and nothing
  else — any other id throws `UnsupportedModelIdError` before inference starts. The assistant sends
  its tools on every turn, so a model outside that list loads, reports "Model ready" and then fails
  every question; that is why the fetch script's case list holds only those ids (default
  `Hermes-3-Llama-3.1-8B-q4f32_1-MLC` at ~5.8 GB, or `Hermes-3-Llama-3.2-3B-q4f16_1-MLC` at ~2.3 GB
  for a small GPU). Widening it means grammar-constrained output (`response_format` of type
  `structural_tag`, which the bundle accepts for any model) driven from this client rather than from
  the dependency — a deliberate redesign, not an implementation detail.
* **The context-window override, and what it costs.** The model's own `mlc-chat-config.json` declares
  131072 tokens and the KV cache is allocated up front for whatever the loaded config says, so
  `app-config.json` has to override it — without one the engine asks the GPU for its whole window
  before generating a token. The fetch script computes the number rather than hardcoding it:
  `CONTEXT_WINDOW_SIZE=<tokens>` when given (clamped to what the model declares), otherwise the window
  the model was **trained** at — `model_config.rope_scaling.original_max_position_embeddings`, 8192 for
  every Llama-3.1-based id here, falling back to the bundle's conservative 4096 where a config has no
  rope scaling. Trained-at is the ceiling worth having: past it the model runs on extrapolated
  positions, an unasked-for quality gamble, while below it a tool round can stop fitting — twelve tool
  schemas are ~4.6 kB of JSON folded into every prompt, plus whatever the last call returned, and a
  round that no longer fits is `ContextWindowSizeExceededError`, not a truncation.
  The price is arithmetic: `2 × layers × kv_heads × head_dim` bytes per token (K and V, float16) for
  the whole window — 128 KiB/token for Hermes-3-Llama-3.1-8B, so 512 MB at 4096, 1 GB at this default,
  and 16 GB at its declared maximum, which is a load that dies on any ordinary card.
* **`vram_required_MB` is a stated figure, not a gate.** The script sizes it as weights + cache +
  ~950 MB of logits and buffers, which reproduces what the bundle records for
  Hermes-3-Llama-3.1-8B-q4f32_1 at its own 4096 window (5771 against 5779) — but web-llm v0.2.86 never
  reads the field, so nothing refuses an over-large install: it dies mid-load as `DeviceLostError`,
  whose message advises "a smaller context length". The number is there so an administrator can tell
  that failure apart from a broken artifact, and so the install says what it needs before it runs.
* `tools/assistant_model_hashes.txt` — one `<sha256>  <MODEL_ID>/<file>` line per artifact, the integrity
  control behind `web/assets/models/`. Size checks prove only that a file is the length its own manifest claims;
  the compiled `.wasm` pulled into `web/assets/models/` runs as browser code under this application's origin
  with the assistant's API access, so without a pinned digest the install trusts whatever `huggingface.co`
  and `raw.githubusercontent.com` answer on the day the script runs. `--pin` writes the manifest once, from
  a network somebody trusts; reviewing it (against upstream release notes, or a second fetch over another
  path) and committing it is the human step — nothing here can distinguish a genuine artifact from one
  substituted before anyone looked. Every later run verifies against it and installs nothing unmatched, an
  absent manifest being a refusal rather than a fallback to size checks, and `--verify` re-checks an existing
  install with no network at all (§3 review rule, REQ-TECH-021's standard for vendor assets). The same digests
  go into `app-config.json` under the bundle's own `integrity` key (`.config`, `.model_lib`, `.tokenizer`), so
  the check repeats in the browser on every load and a directory modified after install fails with an
  `IntegrityError` instead of executing. Keys are per `MODEL_ID`, and pinning one model keeps another model's
  entries in the same file.

## 8. Tests

Playwright (`e2e/tests/assistant.spec.ts`, REQ-TECH-028): the section renders for an admin with a
"model not installed / WebGPU unavailable" state; the MCP endpoint is exercised from the page's own
session — `tools/list` shape, a read tool round-trip, and the server-side refusal of a write tool in
read-only mode (REQ-AI-006). Stats helpers are verified against hand-computed values,
including the p-value against a known reference (e.g. t = 2.1, df = 18 → p ≈ 0.0489).
