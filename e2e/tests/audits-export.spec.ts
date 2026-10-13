import { expect, test, type Page } from "@playwright/test"
import { readFileSync } from "node:fs"

// The Audits page's Export control (§5.6, REQ-UI-057): clicking it downloads the filtered
// trail as a zip-compressed CSV streamed through the PHP route (REQ-AUD-031), and an export
// estimated above 1 GiB is confirmed in a modal naming its size first (REQ-AUD-032). The
// estimate paths are route-mocked — the threshold behaviour must hold whatever the dev
// stack's trail currently weighs; the unmocked test proves the real pipeline end to end.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

const GIB = 1073741824
const ESTIMATE_ROUTE = /\/admin\?.*estimate=1/

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

async function openAudits(page: Page): Promise<void> {
  await signInAsAdmin(page)
  await page.goto("/admin?section=audits")
  await expect(page.locator("form.clara-audit-filters")).toBeVisible()
}

function mockEstimate(page: Page, body: unknown, status = 200): void {
  void page.route(ESTIMATE_ROUTE, (route) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) }))
}

test.describe("audit log export (§5.6, REQ-UI-057)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("exports the filtered trail as a zip-compressed CSV", async ({ page }) => {
    await openAudits(page)

    // The signing-in itself wrote audit entries, so the export has rows on any stack.
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator('form.clara-audit-filters button[name="download"]').click(),
    ])
    expect(download.suggestedFilename()).toMatch(/^audit_events_\d{4}-\d{2}-\d{2}_\d{4}\.zip$/)
    const path = await download.path()
    expect(path).toBeTruthy()
    const bytes = readFileSync(path as string)
    expect(bytes.length).toBeGreaterThan(30)
    expect(String.fromCharCode(bytes[0], bytes[1])).toBe("PK") // a real ZIP container
  })

  test("an export estimated above 1 GiB asks first, and downloads on confirm (REQ-AUD-032)", async ({ page }) => {
    await openAudits(page)
    mockEstimate(page, { rows: 9_000_000, estimated_bytes: Math.round(2.5 * GIB) })

    await page.locator('form.clara-audit-filters button[name="download"]').click()
    const dialog = page.locator("#clara-audit-export-warning")
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText("2.5 GiB")

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      dialog.getByRole("button", { name: "Export anyway" }).click(),
    ])
    expect(download.suggestedFilename()).toMatch(/^audit_events_.*\.zip$/)
  })

  test("cancelling the warning starts no download", async ({ page }) => {
    await openAudits(page)
    mockEstimate(page, { rows: 9_000_000, estimated_bytes: 2 * GIB })

    await page.locator('form.clara-audit-filters button[name="download"]').click()
    const dialog = page.locator("#clara-audit-export-warning")
    await expect(dialog).toBeVisible()
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(dialog).toBeHidden()

    // And the page never navigated away — nothing was submitted.
    expect(page.url()).toContain("section=audits")
  })

  test("below the threshold the export starts without a warning", async ({ page }) => {
    await openAudits(page)
    mockEstimate(page, { rows: 120, estimated_bytes: 48_576 })

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator('form.clara-audit-filters button[name="download"]').click(),
    ])
    expect(download.suggestedFilename()).toMatch(/^audit_events_.*\.zip$/)
    await expect(page.locator("#clara-audit-export-warning")).toHaveCount(0)
  })

  test("a failing estimate never blocks the export", async ({ page }) => {
    await openAudits(page)
    mockEstimate(page, { error: "internal" }, 500)

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator('form.clara-audit-filters button[name="download"]').click(),
    ])
    expect(download.suggestedFilename()).toMatch(/^audit_events_.*\.zip$/)
  })

  test("the filters ride along: a views export carries type=views", async ({ page }) => {
    await openAudits(page)
    await page.selectOption("#audit-type", "views")
    await page.locator('form.clara-audit-filters button[type="submit"]:not([name])').click()
    await expect(page).toHaveURL(/type=views/)

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator('form.clara-audit-filters button[name="download"]').click(),
    ])
    expect(download.suggestedFilename()).toMatch(/^audit_views_.*\.zip$/)
  })
})
