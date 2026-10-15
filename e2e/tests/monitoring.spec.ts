import { expect, test, type Page } from "@playwright/test"

// The Control Panel's Monitoring section (REQ-UI-064): the left panel links it between Settings
// and Configuration Check, the first paint renders from the API's metrics and blocked-ips reads,
// and monitoring.js then polls this section's data region once per second while the page is
// visible — filling the stat cards, drawing the eight d3 small multiples, and re-rendering the
// blocked-IP table. Driven as the bootstrap administrator of `e2e/dev-stack.sh`.

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

test.describe("Monitoring in the Control Panel (REQ-UI-064)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("links Monitoring between Settings and Configuration Check", async ({ page }) => {
    await signInAsAdmin(page)
    await page.locator('header a[href="/admin"]').click()
    const nav = page.locator("#clara-nav")
    const labels = await nav.getByRole("link").allInnerTexts()
    expect(labels.indexOf("Monitoring")).toBeGreaterThan(-1)
    expect(labels.indexOf("Monitoring")).toBe(labels.indexOf("Settings") + 1)
    expect(labels.indexOf("Configuration Check")).toBe(labels.indexOf("Monitoring") + 1)
  })

  test("renders the first paint and then polls: cards fill, charts draw, table shows its state", async ({ page }) => {
    const problems: string[] = []
    page.on("console", (message) => {
      if (message.type() === "error") problems.push(message.text())
    })

    await signInAsAdmin(page)
    await page.goto("/admin?section=monitoring")
    await expect(page.getByRole("heading", { level: 1, name: "Monitoring" })).toBeVisible()

    // First paint (server-rendered): the gauge cards answer from one snapshot, rates are em-dashed.
    await expect(page.locator('[data-clara-mon-stat="goroutines"]')).not.toBeEmpty()
    await expect(page.getByRole("heading", { level: 2, name: "Blocked IP addresses" })).toBeVisible()

    // The eight small multiples exist empty; the first tick draws a path into each.
    await expect(page.locator('[data-clara-mon-chart="rps"] svg path.clara-mon-line')).toHaveAttribute("d", /^M/)
    await expect(page.locator('[data-clara-mon-chart="throughput"] svg path.clara-mon-line')).toHaveAttribute("d", /^M/)

    // Polling took over: the freshness line names an update, and the two-sample rates appeared.
    await expect(page.locator("[data-clara-mon-updated]")).toContainText("Updated")
    await expect(page.locator('[data-clara-mon-stat="rps"]')).toHaveText(/^\d+\.\d$/)

    // The blocked table: nothing on this stack is blocked, and the settings line says whether
    // limiting is on at all — "nothing blocked" stays distinguishable from "limiter off".
    await expect(page.locator("[data-clara-mon-blocked-body]")).toContainText(/No addresses are blocked\.|\d+\.\d+\.\d+\.\d+/)
    await expect(page.locator("[data-clara-mon-rate-settings]")).toContainText(/Rate limiting is (on|off)/)

    // The failure line stays hidden while the API answers.
    await expect(page.locator("[data-clara-mon-failed]")).toBeHidden()

    expect(problems).toEqual([])
  })
})
