import { expect, test, type Page } from "@playwright/test"

// M4 exit criterion (Plan/Web_Implementation.md §6): an instrument designed from scratch —
// choices, validation, branching and a calculated field tested against a record — and, in
// production, a staging commit that blocks until its breaking changes are acknowledged
// (REQ-UI-034); plus the analysis-mode acknowledgement of a breaking edit (REQ-UI-037). Driven
// as the bootstrap administrator of `e2e/dev-stack.sh`; every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only
// The data API of the same stack. The browser never talks to it (REQ-UI-002); the spec does,
// once, to store a record the calculation can be tested against — data entry arrives with M5.
const API_URL = process.env.CLARA_API_URL ?? "http://127.0.0.1:8085"

async function signInAsAdmin(page: Page): Promise<void> {
  await page.goto("/login")
  await page.getByRole("radio", { name: "Local", exact: true }).check()
  await page.fill("#login-email", ADMIN_EMAIL)
  await page.fill("#login-password", ADMIN_PASSWORD)
  await page.locator('form[action="/login?action=credentials"] button[type="submit"]').click()
  await expect(page.locator('header a[href="/admin"]')).toBeVisible()
}

async function confirmDialog(page: Page): Promise<void> {
  const dialog = page.locator("#clara-confirm")
  await expect(dialog).toBeVisible()
  await dialog.locator("[data-clara-confirm-ok]").click()
}

const success = (page: Page) => page.locator(".alert-success").last()

async function openField(page: Page, name: string): Promise<void> {
  await page.locator(`table.clara-fields tr[data-field="${name}"] a`, { hasText: "Edit" }).click()
  await expect(page.locator("#field-form")).toBeVisible()
}

async function addField(page: Page, fill: () => Promise<void>, name: string): Promise<void> {
  await page.getByRole("link", { name: "Add field" }).click()
  await fill()
  await page.locator("#field-form button[type=submit]").click()
  await expect(success(page)).toContainText(`Field "${name}" added.`)
}

test.describe("setup and designer (M4)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("design an instrument from scratch, test a calculation, stage and acknowledge breaking changes", async ({ page, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `M4 ${tag}`

    await signInAsAdmin(page)

    // --- a project; entering it lands a project administrator on Setup (REQ-UI-017) ---
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "M4[0-9][0-9][0-9]")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)
    const projectUrl = page.url().replace(/\/setup$/, "")

    // --- Setup: an event with a timepoint, an instrument, the mapping (§6.2) ---
    await expect(page.locator('table.clara-events tr[data-event="baseline_arm_1"]')).toBeVisible()
    await page.fill("#event-name-1", "follow_up")
    await page.fill("#event-period-1", "30")
    await page.locator("form.clara-add-event button[type=submit]").click()
    await expect(page.locator('table.clara-events tr[data-event="follow_up_arm_1"]')).toContainText("30")

    await page.fill("#instrument-name", "vitals")
    await page.locator('form[action$="action=add_instrument"] button[type=submit]').click()
    await expect(page.locator('table.clara-instruments tr[data-instrument="vitals"]')).toBeVisible()

    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    await matrix.locator('input[data-pair="instrument|baseline_arm_1"]').check()
    await matrix.locator('input[data-pair="vitals|baseline_arm_1"]').check()
    await matrix.locator('input[data-pair="vitals|follow_up_arm_1"]').check()
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")
    await expect(page.locator('form.clara-mapping[data-arm="1"] input[data-pair="vitals|follow_up_arm_1"]')).toBeChecked()

    // --- Design: the record identifier first, then the vitals instrument (§7.2) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, async () => {
      await page.fill("#field-name", "record_id")
      await page.fill("#field-label", "Record ID")
    }, "record_id")

    await page.getByRole("link", { name: "← All instruments" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "vitals" }).click()

    // A long name is warned about, not refused (REQ-VAL-013).
    await page.getByRole("link", { name: "Add field" }).click()
    await page.fill("#field-name", "a_field_name_longer_than_26_chars")
    await expect(page.locator("[data-clara-name-warning]")).toBeVisible()
    await page.locator("#field-form").getByRole("link", { name: "Cancel" }).click()

    await addField(page, async () => {
      await page.fill("#field-name", "weight")
      await page.fill("#field-label", "Weight (kg)")
      await page.selectOption("#field-validation", "floating point")
      await expect(page.locator("#field-min")).toBeVisible()
      await page.fill("#field-min", "1")
      await page.check("#field-required")
    }, "weight")
    await addField(page, async () => {
      await page.fill("#field-name", "height")
      await page.fill("#field-label", "Height (m)")
      await page.selectOption("#field-validation", "floating point")
    }, "height")

    // Choices: code/label rows, encoded code$label##… for the API (REQ-VAL-022).
    await addField(page, async () => {
      await page.fill("#field-name", "sex")
      await page.selectOption("#field-type", "radio")
      const codes = page.locator('input[name="choice_code[]"]')
      const labels = page.locator('input[name="choice_label[]"]')
      await codes.nth(0).fill("1")
      await labels.nth(0).fill("Male")
      await codes.nth(1).fill("2")
      await labels.nth(1).fill("Female")
    }, "sex")
    await openField(page, "sex")
    await expect(page.locator('input[name="choice_label[]"]').nth(1)).toHaveValue("Female")

    // Branching: an unknown reference is rejected with the API's reason, and the draft survives.
    await page.getByRole("link", { name: "Add field" }).click()
    await page.fill("#field-name", "pregnant")
    await page.fill("#field-branching", '[baseline_arm_1][nope] = "2"')
    await page.locator("#field-form button[type=submit]").click()
    await expect(page.locator(".alert-danger")).toContainText("nope")
    await expect(page.locator("#field-name")).toHaveValue("pregnant")

    // …then written with the reference picker (the page route's data region, REQ-UI-044).
    await page.fill("#field-branching", "")
    await page.locator("#field-branching-refs").focus()
    await expect(page.locator('#field-branching-refs option[value="[baseline_arm_1][sex]"]')).toHaveCount(1)
    await page.selectOption("#field-branching-refs", "[baseline_arm_1][sex]")
    await page.locator('#field-form [data-clara-expression="branching"] [data-clara-insert="="]').click()
    await page.locator("#field-branching").press("End")
    await page.locator("#field-branching").pressSequentially(' "2"')
    await expect(page.locator("#field-branching")).toHaveValue('[baseline_arm_1][sex]= "2"')
    await expect(page.locator('[data-clara-expression="branching"] .clara-tok-ref').first()).toHaveText("[baseline_arm_1][sex]")
    await page.locator("#field-form button[type=submit]").click()
    await expect(success(page)).toContainText('Field "pregnant" added.')

    await addField(page, async () => {
      await page.fill("#field-name", "bmi")
      await page.selectOption("#field-type", "calculated")
      await expect(page.locator("#field-calculation")).toBeVisible()
      await page.fill("#field-calculation", "[baseline_arm_1][weight] / ([baseline_arm_1][height] * [baseline_arm_1][height])")
    }, "bmi")

    // --- a record to test against, stored through the data API with the admin's own token ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    const token = await page.locator("#clara-new-token").inputValue()
    const imported = await request.post(`${API_URL}/api/`, {
      form: {
        token, content: "record", action: "import", format: "json",
        "data[0][record_id]": "M4001", "data[0][form_name]": "instrument", "data[0][event_name]": "baseline_arm_1",
        "data[1][record_id]": "M4001", "data[1][form_name]": "vitals", "data[1][event_name]": "baseline_arm_1",
        "data[1][weight]": "80", "data[1][height]": "2", "data[1][sex]": "2", "data[1][pregnant]": "no",
      },
    })
    expect(imported.ok()).toBeTruthy()
    expect(JSON.stringify(await imported.json())).not.toContain('"import_record_id":0')

    // --- test the calculation: the stored expression, then a draft that divides by zero (§7.4) ---
    await page.goto(`${projectUrl}/design`)
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "vitals" }).click()
    await openField(page, "bmi")
    const testPanel = page.locator(".clara-calc-test")
    await testPanel.locator("#calc-record").selectOption("M4001")
    await testPanel.getByRole("button", { name: "Test" }).click()
    await expect(page.locator("[data-clara-calc-value]")).toHaveText("20")

    await page.fill("#calc-draft", "[baseline_arm_1][weight] / 0")
    await page.locator(".clara-calc-test").getByRole("button", { name: "Test" }).click()
    await expect(page.locator(".clara-calc-problems")).toContainText("division by zero")

    // --- production: edits need a staging set; commit blocks until breaking changes are acknowledged ---
    await page.locator("#clara-nav").getByRole("link", { name: "Overview" }).click()
    await page.getByRole("button", { name: "Move to production — keep stored data" }).click()
    await confirmDialog(page)
    await expect(success(page)).toContainText("The project is now in Production mode.")

    await page.goto(`${projectUrl}/design`)
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "vitals" }).click()
    await expect(page.locator(".clara-staging-closed")).toBeVisible()
    await expect(page.getByRole("link", { name: "Add field" })).toHaveCount(0)
    await page.getByRole("button", { name: "Start staging" }).click()
    await expect(page.locator(".clara-staging-banner")).toBeVisible()

    // Deleting a field that holds a value is a breaking change (API §4.21).
    await page.locator('table.clara-fields tr[data-field="pregnant"] form[action*="action=delete_field"] button').click()
    await confirmDialog(page)
    await expect(success(page)).toContainText('Field "pregnant" removed.')

    await page.locator(".clara-staging-banner").getByRole("button", { name: "Commit staged changes" }).click()
    const commit = page.locator("#clara-commit")
    await expect(commit.locator(".clara-staging-breaking")).toContainText("pregnant")
    await commit.locator("button[type=submit]").click()
    // The acknowledgement is required — the dialog stays and nothing is committed.
    await expect(commit).toBeVisible()
    await expect(page.locator(".clara-staging-banner")).toBeVisible()
    await commit.locator("#staging-ack").check()
    await commit.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Staged changes committed")
    await expect(page.locator(".clara-staging-banner")).toHaveCount(0)
    await expect(page.locator('table.clara-fields tr[data-field="pregnant"]')).toHaveCount(0)

    // --- analysis: a breaking edit asks first, and applies on "Delete anyway" (REQ-UI-037) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Overview" }).click()
    await page.getByRole("button", { name: "Change to Analysis" }).click()
    await confirmDialog(page)
    await expect(success(page)).toContainText("The project is now in Analysis mode.")

    await page.goto(`${projectUrl}/design`)
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "vitals" }).click()
    await page.locator('table.clara-fields tr[data-field="sex"] form[action*="action=delete_field"] button').click()
    await confirmDialog(page)
    const question = page.locator("#clara-breaking")
    await expect(question).toBeVisible()
    await expect(question.locator(".clara-breaking-reason")).toContainText("sex")
    await question.getByRole("button", { name: "Delete anyway" }).click()
    await expect(page.locator(".alert-success")).toContainText(["The change is live now."])
    await expect(page.locator('table.clara-fields tr[data-field="sex"]')).toHaveCount(0)
  })
})
