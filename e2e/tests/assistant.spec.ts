import { expect, test, type Page } from "@playwright/test"
import { readFileSync } from "node:fs"
import path from "node:path"
import url from "node:url"

// The Clara assistant section and its MCP endpoint (AI_Assistant area, Plan D4): the page renders
// for an administrator, the protocol answers on this application's own route, and — the rule that
// matters most — a write tool is refused **server-side** while the session is read-only
// (REQ-AI-006), independent of anything the browser shows. The model itself is not exercised:
// CI browsers have no WebGPU, and the engine's states are asserted as far as they reach here.

const here = path.dirname(url.fileURLToPath(import.meta.url))

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

/** One JSON-RPC call from the signed-in page context (session cookie + CSRF meta, like mcp-client.js). */
async function mcp(page: Page, method: string, params: Record<string, unknown>): Promise<Record<string, never>> {
  return page.evaluate(async ({ method, params }) => {
    const token = document.querySelector("meta[name=csrf-token]")?.getAttribute("content") || ""
    const response = await fetch("/mcp", {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": token },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    })

    return (await response.json()) as Record<string, never>
  }, { method, params })
}

/**
 * Replaces the vendored engine with one that answers the way web-llm's Hermes path does, and
 * records every request it receives in `window.__engineCalls`. Two scenarios share it: an ordinary
 * tool round followed by a prose answer, and — when the question says "repeat yourself" — a model
 * that ignores the result it was handed and asks for the same call again.
 */
async function stubHermesEngine(page: Page): Promise<void> {
  await page.addInitScript(() => {
    ;(window as unknown as Record<string, unknown>).__engineCalls = []
  })
  await page.route("**/vendor/webllm/web-llm.mjs", (route) =>
    route.fulfill({
      contentType: "text/javascript",
      body: `
          const calls = []
          window.__engineCalls = calls
          const delta = (part, reason) => ({ choices: [{ delta: part, finish_reason: reason || null, index: 0 }] })
          const askForProjects = () => [
            delta({ role: "assistant", tool_calls: [{ index: 0, function: { name: "list_projects", arguments: "{}" } }] }),
            delta({}, "tool_calls"),
          ]
          export async function CreateMLCEngine(modelId, options) {
            if (options && options.initProgressCallback) { options.initProgressCallback({ progress: 1 }) }
            return {
              chat: {
                completions: {
                  create(request) {
                    calls.push(JSON.parse(JSON.stringify({ messages: request.messages, tools: request.tools })))
                    const askedForTools = request.tools !== undefined
                    const ignoresItsResult = JSON.stringify(request.messages).includes("repeat yourself")
                    let chunks
                    if (calls.length === 1 || (ignoresItsResult && askedForTools)) {
                      chunks = askForProjects()
                    } else if (calls.length === 2 && askedForTools) {
                      chunks = [delta({}, "stop")]
                    } else {
                      chunks = [delta({ content: "One project, 42 records." }), delta({}, "stop")]
                    }
                    return (async function* () { for (const chunk of chunks) { yield chunk } })()
                  },
                },
              },
            }
          }
        `,
    }),
  )
  // WebGPU is missing in CI browsers, so the probe is answered too: what is under test is the
  // conversation loop, and the probe would otherwise stop before the engine is ever created.
  await page.addInitScript(() => {
    Object.defineProperty(window.navigator, "gpu", { value: {}, configurable: true })
  })
}

/** The requests the stubbed engine received, newest last. */
async function engineCalls(page: Page): Promise<Array<{ messages: Array<{ role: string; content: string | null }>; tools: unknown }>> {
  return (await page.evaluate(() => (window as unknown as Record<string, unknown>).__engineCalls)) as Array<
    { messages: Array<{ role: string; content: string | null }>; tools: unknown }
  >
}

test.describe("clara assistant (AI_Assistant)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the section renders inside the Control Panel", async ({ page }) => {
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    await expect(page.locator("#clara-nav a[href='/admin?section=assistant']")).toBeVisible()
    await expect(page.getByRole("heading", { name: "Assistant" })).toBeVisible()
    // Read-only is the default state the page shows (REQ-AI-006).
    await expect(page.getByText("Read-only", { exact: true })).toBeVisible()
    await expect(page.locator("[data-clara-as-form]")).toBeVisible()
    await expect(page.getByRole("button", { name: "Send", exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "New chat", exact: true })).toBeVisible()
    // The field states its own key contract, so it has to be on the page with it (assistant.js).
    await expect(page.locator("#clara-as-input-hint")).toBeVisible()
  })

  test("every string the client asks for is on the page", async ({ page }) => {
    // `t()` answers an unknown key with the key itself, so a message whose name was left out of
    // ControlPanelController's jsKeys list renders as `admin.assistant.error.…`. The sentence that
    // explains a CSP-blocked WebAssembly compile spent its first release doing exactly that: the
    // text was in en.php, the list had never heard of it. Both halves are checked from here — the
    // file the client reads, and the block this page injects (View.php's `script[data-i18n]`).
    const client = readFileSync(path.join(here, "..", "..", "web", "assets", "js", "assistant.js"), "utf8")
    const asked = [...new Set(client.match(/admin\.assistant\.[a-z0-9_.]*/g) ?? [])]
    expect(asked.length).toBeGreaterThan(10) // the scrape found this client's strings at all

    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")
    const injected = Object.keys(
      await page.locator("script[data-i18n]").evaluate((node) => JSON.parse(node.textContent || "{}") as Record<string, string>),
    )

    // A key ending in `.` is completed at runtime (`"admin.assistant.who." + role`), so it counts as
    // answered when any injected key hangs off it.
    const missing = asked
      .map((key) => key.replace(/\.$/, ""))
      .filter((key) => !injected.includes(key) && !injected.some((name) => name.startsWith(key + ".")))
    expect(missing).toEqual([])
  })

  test("Enter sends the message; Shift+Enter writes a line break instead", async ({ page }) => {
    // The engine needs WebGPU, which these browsers do not have, so the field is enabled by hand:
    // what is under test is this page's keyboard contract — which key starts a turn — and not the
    // model. The turn itself fails at the engine, so only the bubble and the field are inspected.
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    const input = page.locator("[data-clara-as-input]")
    await expect(input).toBeVisible()
    await input.evaluate((node) => {
      (node as HTMLTextAreaElement).disabled = false
    })

    await input.click()
    await input.pressSequentially("first line")
    await input.press("Shift+Enter")
    await input.pressSequentially("second line")

    // Shift+Enter is a newline in the field and nothing else — no message leaves it.
    await expect(input).toHaveValue("first line\nsecond line")
    await expect(page.locator(".clara-as-msg-user")).toHaveCount(0)

    // Enter sends: the whole text becomes one user bubble, and the field clears for the next turn.
    // textContent, not innerText — the rendered paragraph collapses the line break to a space,
    // while what matters here is that the newline travelled into the message intact.
    await input.press("Enter")
    await expect(page.locator(".clara-as-msg-user")).toHaveCount(1)
    expect(await page.locator(".clara-as-msg-user p").textContent()).toBe("first line\nsecond line")
    await expect(input).toHaveValue("")
  })

  test("a turn that fails says what failed, not only that something did", async ({ page }) => {
    // No engine in these browsers, so every send dies at the first stream — the state the report
    // exists for, reached without a GPU. What is asserted is this page's own context (the step, the
    // round, whether an engine was there at all), since the engine's message differs per browser;
    // REQ-AI-012 asks for an actionable state, and the bare "could not answer" sentence gave an
    // administrator nothing to act on and nothing to paste anywhere.
    await signInAsAdmin(page)
    // The config answers 404 so startup stops at "no model installed": a browser here that does
    // have WebGPU would otherwise start fetching gigabytes of weights in the background, fail at
    // some point during this test, and report *that* failure into the same chat log.
    await page.route("**/assets/models/app-config.json", (route) => route.fulfill({ status: 404 }))
    await page.goto("/admin?section=assistant")

    const input = page.locator("[data-clara-as-input]")
    await input.evaluate((node) => {
      (node as HTMLTextAreaElement).disabled = false
    })
    await input.fill("hello")
    await input.press("Enter")

    const status = page.locator("[data-clara-as-status]")
    await expect(status).toContainText("The model could not answer:")
    // The translated sentence resolved from the injected block, so the key is on the page's list.
    await expect(status).not.toContainText("admin.assistant.error")

    const details = page.locator("[data-clara-as-diagnostics]")
    await expect(details).toHaveCount(1)
    await details.locator("summary").click()
    await expect(details).toContainText("Technical details")
    await expect(details).toContainText("stage=stream")
    await expect(details).toContainText("round=1/")
    await expect(details).toContainText("engine=absent")
    // Counts, never content: the report must not become a place record values collect.
    await expect(details).toContainText("messages=2")
  })

  test("the tool loop speaks the shape the Hermes engine answers in", async ({ page }) => {
    // The engine is replaced, not skipped: web-llm's Hermes path has three rules this client exists
    // to satisfy — a request carrying `system` throws CustomSystemPromptError, a request carrying
    // `tools` answers only with a JSON array of calls (never prose), and those calls carry no id.
    // None of that is observable without a GPU, which CI browsers do not have, so the vendored
    // module is swapped for one that replies exactly as the engine does and records what it received.
    await stubHermesEngine(page)
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    const input = page.locator("[data-clara-as-input]")
    await expect(input).toBeEnabled({ timeout: 15000 })
    await input.fill("how many projects are there?")
    await input.press("Enter")

    // The tool call the engine reported ran for real, through this application's own endpoint.
    await expect(page.locator(".clara-as-tool").first()).toContainText("list_projects")
    await expect(page.locator(".clara-as-tool").first()).toContainText("done")
    await expect(page.locator(".clara-as-msg-assistant p").last()).toHaveText("One project, 42 records.")

    const calls = (await page.evaluate(() => (window as unknown as Record<string, unknown>).__engineCalls)) as Array<{
      messages: Array<{ role: string; content: string | null }>
      tools: unknown
    }>
    expect(calls.length).toBeGreaterThanOrEqual(3)
    // 1. No request ever carries a system message; its text leads the first user turn instead.
    for (const call of calls) {
      expect(call.messages.map((message) => message.role)).not.toContain("system")
    }
    expect(calls[0].messages[0].role).toBe("user")
    expect(calls[0].messages[0].content ?? "").toContain("You are Clara")
    // 2. The prose answer comes from a request without tools; the call rounds carry them.
    expect(calls[0].tools).toBeTruthy()
    expect(calls[calls.length - 1].tools).toBeUndefined()
    // 3. The engine's id-less call is named, and its tool result answers that name.
    const toolRound = calls[2].messages.find((message) => message.role === "tool")
    expect(toolRound).toBeTruthy()
    //    …and the result is wrapped the way Hermes-3 was trained to read one. MLC's template for this
    //    model renders a `tool` turn as role plus bare content, with no < tool_response> markers.
    expect(toolRound?.content ?? "").toContain("< tool_response>")

    // And the answer stays in the conversation: a follow-up sees the reply it follows up.
    await input.fill("and its records?")
    await input.press("Enter")
    const followUp = (await page.evaluate(() => (window as unknown as Record<string, unknown>).__engineCalls)) as Array<{
      messages: Array<{ role: string; content: string | null }>
    }>
    const seen = JSON.stringify(followUp[followUp.length - 1].messages)
    expect(seen).toContain("One project, 42 records.")
  })

  test("a model that ignores the result it was handed is stopped, not indulged", async ({ page }) => {
    // Repeating an identical call cannot improve the answer, and up to MAX_TOOL_ROUNDS of the same
    // query is the worst thing this page can do to its own API while looking like it is thinking.
    await stubHermesEngine(page)
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    const input = page.locator("[data-clara-as-input]")
    await expect(input).toBeEnabled({ timeout: 15000 })
    await input.fill("repeat yourself please")
    await input.press("Enter")

    await expect(page.locator("[data-clara-as-status]")).toContainText("asked to run list_projects a second time")
    // One execution, then the duplicate named and refused: two lines, only one of them "done".
    const toolLines = page.locator(".clara-as-tool")
    await expect(toolLines).toHaveCount(2)
    await expect(toolLines.nth(0)).toContainText("done")
    await expect(toolLines.nth(1)).toContainText("repeated")
    expect((await engineCalls(page)).length).toBe(2)
  })

  test("the MCP endpoint speaks the protocol on this origin only", async ({ page }) => {
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    const init = await mcp(page, "initialize", { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "spec", version: "0" } })
    expect(init.result).toBeTruthy()
    expect((init.result as Record<string, string>).serverInfo.name).toBe("clara-assistant")

    const tools = await mcp(page, "tools/list", {})
    const names = ((tools.result as Record<string, Array<Record<string, string>>>).tools).map((tool) => tool.name)
    for (const expected of ["list_projects", "marginal_stats", "ttest", "create_records", "add_event"]) {
      expect(names).toContain(expected)
    }
    // The mode gate is not visible in the protocol shape — it lives on the server.
    for (const tool of (tools.result as Record<string, Array<Record<string, unknown>>>).tools) {
      expect(tool.x_mode).toBeUndefined()
    }
  })

  test("write tools are refused while the session is read-only, and allowed after enabling", async ({ page }) => {
    await signInAsAdmin(page)
    await page.goto("/admin?section=assistant")

    const call = () => mcp(page, "tools/call", { name: "create_records", arguments: { project_id: 1, records: [{ record_id: "spec-row" }] } })

    // Read-only (the default): the dispatcher refuses before anything reaches the API (REQ-AI-006).
    const refused = await call()
    const refusedResult = refused.result as { isError: boolean; content: Array<{ text: string }> }
    expect(refusedResult.isError).toBe(true)
    expect(refusedResult.content[0].text).toContain("read_only")

    // Switch the session to read-write through the section's own form (REQ-AI-006), then back.
    await page.locator('form[action="/admin?action=assistant_mode"] button[type="submit"]').click()
    await expect(page.getByText("Read & write", { exact: true })).toBeVisible()

    const after = await call()
    const afterResult = after.result as { isError?: boolean; content: Array<{ text: string }> }
    // Whatever the API now says (permissions, unknown project), it is no longer the mode refusal.
    expect(afterResult.content[0].text ?? "").not.toContain("read_only")

    await page.locator('form[action="/admin?action=assistant_mode"] button[type="submit"]').click()
    await expect(page.getByText("Read-only", { exact: true })).toBeVisible()
  })

  test("the assistant page permits compiling WebAssembly", async ({ page }) => {
    // The engine compiles the model library it fetched from this origin, and CSP judges that
    // against script-src: with a bare 'self' every artifact is fine on disk and the model still
    // never loads (AI_Assistant_Design.md §5). Asserted here because a stripped source stays
    // invisible until someone waits on a multi-gigabyte install to notice it.
    await signInAsAdmin(page)
    const response = await page.goto("/admin?section=assistant")
    const csp = response?.headers()["content-security-policy"] ?? ""

    expect(csp).toContain("script-src 'self' 'wasm-unsafe-eval'")
    expect(csp).not.toContain("'unsafe-eval'")
  })

  test("an anonymous caller never reaches the endpoint", async ({ request }) => {
    const response = await request.post("/mcp", {
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      data: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list", params: {} }),
    })
    expect(response.status()).toBe(403)
  })
})
