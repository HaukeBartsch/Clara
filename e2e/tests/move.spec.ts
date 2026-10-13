import { expect, test, type Page } from "@playwright/test"

// Moving a field end to end (REQ-UI-056, REQ-API-157): a field that already holds a value is
// moved out of the first instrument into a second one through the field list's Move dialog —
// the dialog offers this instrument's fields first and the target's once it is chosen — and the
// value shows in the target's form afterwards. Moving the record identifier away is refused
// while records exist (GD-8). Driven as the bootstrap administrator of `e2e/dev-stack.sh`;
// every name is unique per run.

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

const success = (page: Page) => page.locator(".alert-success").last()

async function addField(page: Page, name: string, label: string): Promise<void> {
  await page.getByRole("link", { name: "Add field" }).click()
  await page.fill("#field-name", name)
  await page.fill("#field-label", label)
  await page.locator("#field-form button[type=submit]").click()
  await expect(success(page)).toContainText(`Field "${name}" added.`)
}

const fieldOrder = (page: Page) => page.locator("table.clara-fields tbody tr[data-field]")

test.describe("moving a field (REQ-UI-056)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("move a field with a value into another instrument; the record identifier stays", async ({ page }) => {
    test.setTimeout(120_000)
    const projectName = `Move ${Date.now()}`

    await signInAsAdmin(page)

    // --- a project: the first instrument plus "visits", both at baseline (§6.2) ---
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "MV[0-9][0-9][0-9]")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)

    await page.fill("#instrument-name", "visits")
    await page.locator('form[action*="action=add_instrument"] button[type="submit"]').click()
    await expect(success(page)).toContainText('Instrument "visits" added.')
    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    for (const pair of ["instrument|baseline_arm_1", "visits|baseline_arm_1"]) {
      const box = matrix.locator(`input[data-pair="${pair}"]`)
      if (!(await box.isChecked())) await box.check()
    }
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")

    // --- fields: record_id, age and weight on the first instrument, a note on visits ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    const instruments = page.locator("table.clara-design-instruments")
    await instruments.getByRole("link", { name: "visits", exact: true }).click()
    await addField(page, "visit_note", "Visit note")
    await page.getByRole("link", { name: "← All instruments" }).click()
    await instruments.getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, "record_id", "Record ID")
    await addField(page, "age", "Age")
    await addField(page, "weight", "Weight")
    const instrumentEditor = page.url()

    // --- membership for data entry (§8.6, REQ-API-102), then a record with an age ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    await expect(page.locator("#clara-new-token")).toBeVisible()
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await page.fill("#new-record-id", "MV001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/MV001/)
    const form = page.locator("form[data-clara-record-form]")
    await form.getByLabel("Age").fill("41")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("MV001")

    // --- the dialog: this instrument chosen, the field's own place preselected ---
    await page.goto(instrumentEditor)
    // The buttons render disabled; design.js enables them once the dialog is wired.
    await expect(page.getByRole("button", { name: "Move field age", exact: true })).toBeEnabled()
    await page.getByRole("button", { name: "Move field age", exact: true }).click()
    const dialog = page.locator("#clara-move")
    await expect(dialog).toBeVisible()
    await expect(dialog.locator("#clara-move-title")).toHaveText("Move field age")
    await expect(dialog.locator("#move-instrument option:checked")).toHaveText("instrument")
    await expect(dialog.locator("#move-before option:checked")).toHaveText("weight — Weight")
    await expect(dialog.locator("#move-before option")).toHaveText(["record_id — Record ID", "weight — Weight", "(end of the instrument)"])

    // Another instrument: its fields from the page's data region, the end preselected.
    await dialog.locator("#move-instrument").selectOption({ label: "visits" })
    await expect(dialog.locator("#move-before option")).toHaveText(["visit_note — Visit note", "(end of the instrument)"])
    await expect(dialog.locator("#move-before option:checked")).toHaveText("(end of the instrument)")
    // Every switch re-populates "Move before": back to this instrument, then out again.
    await dialog.locator("#move-instrument").selectOption({ label: "instrument" })
    await expect(dialog.locator("#move-before option")).toHaveText(["record_id — Record ID", "weight — Weight", "(end of the instrument)"])
    await expect(dialog.locator("#move-before option:checked")).toHaveText("weight — Weight")
    await dialog.locator("#move-instrument").selectOption({ label: "visits" })
    await expect(dialog.locator("#move-before option")).toHaveText(["visit_note — Visit note", "(end of the instrument)"])
    await dialog.locator("#move-before").selectOption({ label: "visit_note — Visit note" })
    await dialog.getByRole("button", { name: "Move", exact: true }).click()

    // The target instrument's editor, the field in front of visit_note.
    await expect(success(page)).toContainText('Field "age" moved.')
    await expect(page.locator("h1")).toContainText("visits")
    await expect(fieldOrder(page)).toHaveCount(2)
    expect(await fieldOrder(page).evaluateAll((rows) => rows.map((r) => r.getAttribute("data-field")))).toEqual(["age", "visit_note"])

    // --- the value moved with the field: it shows in the visits form ---
    await page.goto(page.url().replace(/\/design\/instruments\/\d+.*$/, "/records/MV001"))
    const age = form.getByRole("textbox", { name: "Age", exact: true })
    await expect(age).toHaveCount(0)
    await page.locator('.clara-instrument-nav [data-clara-instrument="visits"] a.nav-link').click()
    await expect(age).toHaveValue("41")

    // --- the record identifier stays where it is while records exist (GD-8) ---
    await page.goto(instrumentEditor)
    expect(await fieldOrder(page).evaluateAll((rows) => rows.map((r) => r.getAttribute("data-field")))).toEqual(["record_id", "weight"])
    await page.getByRole("button", { name: "Move field record_id", exact: true }).click()
    await dialog.locator("#move-instrument").selectOption({ label: "visits" })
    await expect(dialog.locator("#move-before option:checked")).toHaveText("(end of the instrument)")
    await dialog.getByRole("button", { name: "Move", exact: true }).click()
    await expect(page.locator(".alert-danger").last()).toContainText("record identifier")
    expect(await fieldOrder(page).evaluateAll((rows) => rows.map((r) => r.getAttribute("data-field")))).toEqual(["record_id", "weight"])
  })
})
