import { expect, test, type Page } from "@playwright/test"

// The Clara assistant section and its MCP endpoint (AI_Assistant area, Plan D4): the page renders
// for an administrator, the protocol answers on this application's own route, and — the rule that
// matters most — a write tool is refused **server-side** while the session is read-only
// (REQ-AI-006), independent of anything the browser shows. The model itself is not exercised:
// CI browsers have no WebGPU, and the engine's states are asserted as far as they reach here.

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
    // The field states its own key contract, so it has to be on the page with it (assistant.js).
    await expect(page.locator("#clara-as-input-hint")).toBeVisible()
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
