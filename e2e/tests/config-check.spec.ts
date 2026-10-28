import { expect, test, type Page } from "@playwright/test"

// The Control Panel's Configuration Check section (REQ-UI-061): the left panel links it between
// Settings and API documentation, the server-rated groups render from the PHP probe and the
// API's facts (REQ-API-159), and the browser probes fill their rows from what the web server
// answers. Driven as the bootstrap administrator of `e2e/dev-stack.sh`, which runs development
// mode on plain HTTP — the expected statuses below are that stack's.

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

test.describe("Configuration Check in the Control Panel (REQ-UI-061)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("rates the installation and runs the browser probes", async ({ page }) => {
    const problems: string[] = []
    page.on("console", (message) => {
      // The probes expect 404s; the browser logs those as failed resources, which is the point.
      if (message.type() === "error" && !message.text().includes("404")) problems.push(message.text())
    })

    await signInAsAdmin(page)
    await page.locator('header a[href="/admin"]').click()
    const nav = page.locator("#clara-nav")
    const labels = await nav.getByRole("link").allInnerTexts()
    // REQ-UI-064 inserted Monitoring between Settings and Configuration Check (REQ-UI-047); the
    // Assistant (DEV-AI-1) and Authentication Test (REQ-AUTH-085) sit between those two, and
    // Configuration Check stays directly before API documentation (REQ-UI-061).
    expect(labels.indexOf("Monitoring")).toBe(labels.indexOf("Settings") + 1)
    expect(labels.indexOf("Configuration Check")).toBeGreaterThan(labels.indexOf("Monitoring"))
    expect(labels.indexOf("API documentation")).toBe(labels.indexOf("Configuration Check") + 1)

    await nav.getByRole("link", { name: "Configuration Check" }).click()
    await expect(page).toHaveURL(/\/admin\?section=check$/)
    await expect(page.getByRole("heading", { level: 1, name: "Configuration Check" })).toBeVisible()

    // Server-rated rows: the API answered, and the stack's development facts are rated as such.
    await expect(page.locator('tr[data-clara-check="api.reachable"]')).toHaveAttribute("data-status", "ok")
    await expect(page.locator('tr[data-clara-check="web.app_env"]')).toHaveAttribute("data-status", "warn")
    await expect(page.locator('tr[data-clara-check="web.https"]')).toHaveAttribute("data-status", "info")
    await expect(page.locator('tr[data-clara-check="php.extensions"]')).toHaveAttribute("data-status", "ok")
    await expect(page.locator('tr[data-clara-check="db.schema"]')).toContainText(/Version \d+\./)

    // The browser probes: php -S answers 404 for the internal paths and revalidates CLARA's assets.
    await expect(page.locator('tr[data-clara-probe="browser.admin_api"]')).toHaveAttribute("data-status", "ok")
    await expect(page.locator('tr[data-clara-probe="browser.docs"]')).toHaveAttribute("data-status", "ok")
    await expect(page.locator('tr[data-clara-probe="browser.assets"]')).toHaveAttribute("data-status", "ok")
    await expect(page.locator('tr[data-clara-probe="browser.hsts"]')).toHaveAttribute("data-status", "info")
    await expect(page.locator('tr[data-clara-probe="browser.docs"]')).toContainText("/docs, /openapi.json and /healthz answer 404")

    await expect(page.getByRole("heading", { level: 2, name: "Check by hand" })).toBeVisible()
    expect(problems).toEqual([])
  })
})
