import { expect, test, type Page } from "@playwright/test"

// Record naming mode (REQ-UI-052) end to end: the Setup block flips the project between
// auto-name and type-record-ID, and the Record Status Dashboard swaps its new-participant
// control accordingly — never both. In auto mode one click takes the next number from the
// data API and opens the record on it (REQ-API-151), counting 1, 2 for a fresh project.
// Driven as the bootstrap administrator of `e2e/dev-stack.sh`; every name is unique per run.

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

test.describe("record naming mode (Setup ↔ dashboard, REQ-UI-052)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("the Setup block flips the mode and the dashboard swaps its new-participant control", async ({ page }) => {
    test.setTimeout(120_000)
    const projectName = `Naming ${Date.now()}`

    await signInAsAdmin(page)

    // --- a fresh project: the mode defaults to type record ID (§6.2) ---
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "NAM[0-9][0-9][0-9]")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)
    const projectUrl = page.url().replace(/\/setup$/, "")

    // Map the default instrument to the baseline event so the record form has a pair.
    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    const pair = matrix.locator('input[data-pair="instrument|baseline_arm_1"]')
    if (!(await pair.isChecked())) {
      await pair.check()
      await matrix.locator("button[type=submit]").click()
      await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")
    }

    const namingForm = page.locator('form[action*="action=save_naming"]')
    await expect(namingForm).toBeVisible()
    await expect(namingForm.locator("#naming-manual")).toBeChecked()

    // --- manual mode: the dashboard offers the free-text field, not the auto button ---
    await page.goto(`${projectUrl}/record-status`)
    await expect(page.locator('form[action*="action=new_record"] input[name="record_id"]')).toBeVisible()
    await expect(page.getByRole("button", { name: "Add participant" })).toHaveCount(0)

    // --- the acting user needs a project assignment for the data API (§8.6, REQ-API-102) ---
    await page.goto(`${projectUrl}/members`)
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()

    // --- flip to auto on Setup ---
    await page.goto(`${projectUrl}/setup`)
    const naming = page.locator('form[action*="action=save_naming"]')
    await naming.locator("#naming-auto").check()
    await naming.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Record naming mode saved.")
    await expect(naming.locator("#naming-auto")).toBeChecked()

    // --- auto mode: one click adds a numbered participant, 1 then 2 (REQ-API-151) ---
    await page.goto(`${projectUrl}/record-status`)
    await expect(page.locator('input[name="record_id"]')).toHaveCount(0)
    const add = page.getByRole("button", { name: "Add participant" })
    await expect(add).toBeVisible()

    // The record view opens on the number straight away (REQ-API-151).
    await add.click()
    await expect(page).toHaveURL(/\/records\/1(\?|$)/)

    // A participant exists only once its first value is stored (REQ-API-033): design a pair
    // of fields, save one on record 1 — and only then does the counter advance to 2.
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
    // The first field is the record identifier (GD-8), so a second one carries the value.
    await page.getByRole("link", { name: "Add field" }).click()
    await page.fill("#field-name", "record_id")
    await page.locator("#field-form button[type=submit]").click()
    await expect(success(page)).toContainText('Field "record_id" added.')
    await page.getByRole("link", { name: "Add field" }).click()
    await page.fill("#field-name", "note")
    await page.locator("#field-form button[type=submit]").click()
    await expect(success(page)).toContainText('Field "note" added.')

    await page.goto(`${projectUrl}/records/1`)
    // In auto mode the identifier is plain text holding the record's number (REQ-UI-052).
    expect(await page.locator("#f-record_id").evaluate((el) => el.tagName.toLowerCase())).toBe("div")
    await expect(page.locator("#f-record_id")).toHaveText("1")
    await page.fill("#f-note", "first")
    await page.locator("form[data-clara-record-form] [data-clara-save]").click()
    await expect(success(page)).toContainText("saved.")

    await page.goto(`${projectUrl}/record-status`)
    await page.getByRole("button", { name: "Add participant" }).click()
    await expect(page).toHaveURL(/\/records\/2(\?|$)/)
  })
})
