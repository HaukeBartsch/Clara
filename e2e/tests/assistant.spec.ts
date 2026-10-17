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

  test("an anonymous caller never reaches the endpoint", async ({ request }) => {
    const response = await request.post("/mcp", {
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      data: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list", params: {} }),
    })
    expect(response.status()).toBe(403)
  })
})
