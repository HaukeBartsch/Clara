import { expect, test, type Page } from "@playwright/test"

// Repeating instruments end to end (REQ-UI-054, REQ-UI-055, REQ-API-154/155): a second instrument
// is switched to repeating at baseline in the Design page's dropdown, filled out once, then a
// second time through the dashboard's "+"; the dashboard shows a dot per instance, the data API
// exports one REDCap row per instance, and switching repeating off hides the second instance
// until it is switched on again. Driven as the bootstrap administrator of `e2e/dev-stack.sh`;
// every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only
const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

const success = (page: Page) => page.locator(".alert-success").last()

async function addField(page: Page, name: string, label: string): Promise<void> {
  await page.getByRole("link", { name: "Add field" }).click()
  await page.fill("#field-name", name)
  await page.fill("#field-label", label)
  await page.locator("#field-form button[type=submit]").click()
  await expect(success(page)).toContainText(`Field "${name}" added.`)
}

// The visits cell of one record on the dashboard.
const visitsCell = (page: Page, record: string) => {
  const table = page.locator("table.clara-record-status")
  return table.locator(`tr[data-record="${record}"] td`).nth(1) // th record | td instrument | td visits
}

async function setRepeating(page: Page, on: boolean): Promise<void> {
  await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
  const control = page.locator('[data-clara-repeating="visits"]')
  await control.locator("button.dropdown-toggle").click()
  const toggle = control.getByRole("switch", { name: /baseline/ })
  await (on ? toggle.check() : toggle.uncheck())
  // A switch saves at once (design.js) and the page comes back with the saved state.
  await expect(success(page)).toContainText(on ? "visits now repeats at 1 event(s)." : "visits no longer repeats.")
  await expect(page.locator('[data-clara-repeating="visits"] button.dropdown-toggle')).toHaveText(on ? "⟳ baseline" : "No")
}

test.describe("repeating instruments (REQ-UI-054, REQ-UI-055)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("switch an instrument to repeating, fill it out twice through +, export one row per instance", async ({ page, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `Repeating ${tag}`

    await signInAsAdmin(page)

    // --- a project: the first instrument plus "visits", both at baseline (§6.2) ---
    await page.locator('header a[href="/admin"]').click()
    // The Control Panel opens on Projects (REQ-UI-047); clicking its entry again would
    // reload the page under the next step.
    await expect(page.locator('#clara-nav a[href="/admin?section=projects"]')).toHaveAttribute("aria-current", "page")
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "OUS")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)

    // The instrument is made on the Design page (§7.1, REQ-UI-067); Setup maps it to an event.
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.fill("#instrument-name", "visits")
    await page.locator('form[action*="action=add_instrument"] button[type="submit"]').click()
    await expect(success(page)).toContainText('Instrument "visits" added.')

    await page.locator("#clara-nav").getByRole("link", { name: "Setup" }).click()
    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    for (const pair of ["instrument|baseline_arm_1", "visits|baseline_arm_1"]) {
      const box = matrix.locator(`input[data-pair="${pair}"]`)
      if (!(await box.isChecked())) await box.check()
    }
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")

    // --- fields: the record identifier, and one field on visits ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    const instruments = page.locator("table.clara-design-instruments")
    await instruments.getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, "record_id", "Record ID")
    await page.getByRole("link", { name: "← All instruments" }).click()
    await instruments.getByRole("link", { name: "visits", exact: true }).click()
    await addField(page, "visit_note", "Visit note")

    // --- the Design page's dropdown: the first instrument cannot repeat, visits can ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    const first = page.locator('[data-clara-repeating="instrument"]')
    await first.locator("button.dropdown-toggle").click()
    await expect(first.locator(".dropdown-menu")).toContainText("The first instrument holds the record ID and cannot repeat.")
    await first.locator("button.dropdown-toggle").click()
    await setRepeating(page, true)

    // --- a token for data entry (§8.6, REQ-API-102) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    // The reveal arrives masked; Show (audited, REQ-AUD-033) puts the value on the page.
    await page.locator("[data-clara-token-show]").click()
    await expect(page.locator("[data-clara-token-hide]")).toBeVisible()
    const token = await page.locator("#clara-new-token").inputValue()

    // --- the record and its first visit ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await expect(page.locator('th[data-instrument="visits"]')).toHaveAttribute("data-repeating", "1")
    await page.fill("#new-record-id", "RP001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/RP001/)
    const form = page.locator("form[data-clara-record-form]")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("RP001")

    await page.locator('.clara-instrument-nav [data-clara-instrument="visits"] a.nav-link').click()
    await expect(page.locator(".clara-instance-nav")).toBeVisible()
    await form.getByLabel("Visit note").fill("first visit")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("visits")

    // --- the dashboard: one dot, then "+" opens instance 2 ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    const cell = visitsCell(page, "RP001")
    await expect(cell.locator(".clara-state")).toHaveCount(1)
    await expect(cell.locator(".clara-state-some_data")).toHaveCount(1)
    await cell.locator("a.clara-instance-add").click()
    await expect(page).toHaveURL(/instrument=visits&instance=2/)
    await expect(page.locator('.clara-instance-nav [data-clara-instance="2"] a')).toHaveAttribute("aria-current", "page")
    await expect(form.getByLabel("Visit note")).toHaveValue("")
    await form.getByLabel("Visit note").fill("second visit")
    await form.locator("[data-clara-save]").click()
    await expect(page).toHaveURL(/instance=2/)
    await expect(form.getByLabel("Visit note")).toHaveValue("second visit")

    // Instance 1 kept its own value.
    await page.locator('.clara-instance-nav [data-clara-instance="1"] a').click()
    await expect(form.getByLabel("Visit note")).toHaveValue("first visit")

    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await expect(visitsCell(page, "RP001").locator(".clara-state")).toHaveCount(2)
    await expect(visitsCell(page, "RP001").locator("a.clara-instance-add")).toHaveAttribute("data-instance", "3")

    // --- the data API exports one REDCap row per instance (REQ-API-155) ---
    const exported = await request.post(`${API_URL}/api/`, {
      form: { token, content: "record", format: "json", returnFormat: "json", "records[0]": "RP001" },
    })
    expect(exported.ok()).toBeTruthy()
    const rows = (await exported.json()) as Record<string, string | number>[]
    const instances = rows.filter((r) => r.redcap_repeat_instrument === "visits")
    // The instance number is a JSON number, as REDCap writes it.
    expect(instances.map((r) => [r.redcap_repeat_instance, r.visit_note])).toEqual([[1, "first visit"], [2, "second visit"]])

    // --- off hides the second instance, on brings it back (REQ-DB-048) ---
    await setRepeating(page, false)
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await expect(page.locator('th[data-instrument="visits"]')).not.toHaveAttribute("data-repeating", "1")
    await expect(visitsCell(page, "RP001").locator(".clara-state")).toHaveCount(1)
    await setRepeating(page, true)
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await expect(visitsCell(page, "RP001").locator(".clara-state")).toHaveCount(2)
  })
})
