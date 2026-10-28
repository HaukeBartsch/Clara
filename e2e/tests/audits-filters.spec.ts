import { expect, test, type Page } from "@playwright/test"

// The Audits page's two logs and their filters (§5.6, REQ-UI-066, REQ-AUD-035): the Event
// filter belongs to the events log alone — the API refuses it with any other log (REQ-API-077) —
// so switching the Log select clears it rather than leaving a stale choice to answer with an
// empty table beside its error line. `record_read` is listed there as the event a browser open
// of a record writes, while the Record views log stays the data API's pull trail (REQ-AUD-013).

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only

async function openAudits(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
  await page.goto("/admin?section=audits")
  await expect(page.locator("form.clara-audit-filters")).toBeVisible()
}

test.describe("audit log filters (§5.6, REQ-UI-066)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("offers the browser's record reads as an event type (REQ-AUD-035)", async ({ page }) => {
    await openAudits(page)

    await expect(page.locator('#audit-event option[value="record_read"]')).toHaveCount(1)
  })

  test("switching the log clears the Event filter, so no stale choice empties the table", async ({ page }) => {
    await openAudits(page)
    const log = page.locator("#audit-type")
    const eventType = page.locator("#audit-event")

    await eventType.selectOption("login_success")
    expect(await eventType.inputValue()).toBe("login_success")

    await log.selectOption("views")
    expect(await eventType.inputValue()).toBe("", "the events-only filter must not survive the switch")

    // What the form submits is what the API accepts: the views log carries no event type. A GET
    // form names every field, so the cleared select still sends an empty `event_type=`, which
    // the controller leaves out of the API query. The form's other submit button is Export CSV
    // (`name="download"`, §5.6, REQ-UI-058).
    await page.locator('form.clara-audit-filters button[type="submit"]:not([name="download"])').click()
    await expect(page).toHaveURL(/section=audits/)
    expect(new URL(page.url()).searchParams.get("event_type") ?? "").toBe("")
    await expect(page.locator(".clara-audits")).toHaveCount(1)
    await expect(page.getByRole("alert")).toHaveCount(0)
  })
})
