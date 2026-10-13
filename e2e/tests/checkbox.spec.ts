import { expect, test, type Page } from "@playwright/test"

// The checkbox field type end to end (REQ-DB-042, REQ-EXP-053, REQ-UI-025): designed with its
// choices, entered as one box per choice — branching and the required check reading the boxes —
// read back and changed through the form, shown in the per-field history, counted as data on the
// dashboard, and exported one column per choice (1/0 raw, the choice's text as label). Driven as
// the bootstrap administrator of `e2e/dev-stack.sh`; every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only
// The data API of the same stack, read at the end to prove what was stored and how it exports —
// the browser itself never talks to it (REQ-UI-002).
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

async function addField(page: Page, fill: () => Promise<void>, name: string): Promise<void> {
  await page.getByRole("link", { name: "Add field" }).click()
  await page.fill("#field-name", name)
  await fill()
  await page.locator("#field-form button[type=submit]").click()
  await expect(success(page)).toContainText(`Field "${name}" added.`)
}

test.describe("checkbox fields (REQ-DB-042)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("design a checkbox, enter and change its boxes, read them back and export one column per choice", async ({ page, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `Checkbox ${tag}`

    await signInAsAdmin(page)

    // --- a project with one instrument, mapped to baseline (§6.2) ---
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

    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    const pair = matrix.locator('input[data-pair="instrument|baseline_arm_1"]')
    if (!(await pair.isChecked())) {
      await pair.check()
      await matrix.locator("button[type=submit]").click()
      await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")
    }

    // --- the instrument: identifier, a required checkbox, a field branched on one box ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, async () => page.fill("#field-label", "Record ID"), "record_id")
    await addField(page, async () => {
      await page.fill("#field-label", "Habits")
      await page.selectOption("#field-type", "checkbox")
      await expect(page.locator("fieldset[data-clara-for-types]")).toBeVisible()
      const codes = page.locator('input[name="choice_code[]"]')
      const labels = page.locator('input[name="choice_label[]"]')
      await codes.nth(0).fill("1")
      await labels.nth(0).fill("Smoking")
      await codes.nth(1).fill("2")
      await labels.nth(1).fill("Drinking")
      await page.locator("[data-clara-add-choice]").click()
      await codes.nth(2).fill("3")
      await labels.nth(2).fill("Sports")
      await page.check("#field-required")
    }, "habits")
    await expect(page.locator('table.clara-fields tr[data-field="habits"]')).toContainText("checkbox")
    await addField(page, async () => {
      await page.fill("#field-label", "Cigarettes per day")
      await page.fill("#field-branching", "[baseline_arm_1][habits___1]")
    }, "cigs")

    // --- the acting user needs a project token for data entry (§8.6, REQ-API-102) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    // The reveal arrives masked; Show (audited, REQ-AUD-033) puts the value on the page.
    await page.locator("[data-clara-token-show]").click()
    await expect(page.locator("[data-clara-token-hide]")).toBeVisible()
    const token = await page.locator("#clara-new-token").inputValue()

    // --- data entry: one box per choice ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    // Manual naming mode (the project default, REQ-UI-052): the record ID is typed in.
    await page.fill("#new-record-id", "CB001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/CB001/)

    const form = page.locator("form[data-clara-record-form]")
    const habits = form.locator('[data-clara-field="habits"]')
    const box = (label: string) => habits.getByLabel(label, { exact: true })
    await expect(habits.locator("input[type=checkbox]")).toHaveCount(3)

    // Branching reads a box: 1 while checked, nothing otherwise (§8.4).
    await expect(form.locator('[data-clara-field="cigs"]')).toBeHidden()
    await box("Smoking").check()
    await expect(form.locator('[data-clara-field="cigs"]')).toBeVisible()
    await box("Smoking").uncheck()
    await expect(form.locator('[data-clara-field="cigs"]')).toBeHidden()

    // A required checkbox is empty while no box is checked (REQ-VAL-028).
    await form.locator("[data-clara-save]").click()
    await expect(form.locator("[data-clara-required-warning]")).toContainText("habits")
    await box("Smoking").check()
    await box("Sports").check()
    await page.fill("#f-cigs", "5")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("Record CB001 created")

    // --- read back, then change: uncheck one stored box, check another ---
    await page.reload()
    await expect(box("Smoking")).toBeChecked()
    await expect(box("Drinking")).not.toBeChecked()
    await expect(box("Sports")).toBeChecked()
    await box("Sports").uncheck()
    await box("Drinking").check()
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("saved")
    await expect(box("Sports")).not.toBeChecked()
    await expect(box("Drinking")).toBeChecked()

    // The checkbox's history is that of its boxes, each change naming its box (REQ-UI-026).
    await habits.locator("[data-clara-history]").click()
    const history = page.locator("#clara-history")
    await expect(history).toBeVisible()
    await expect(history.locator("tbody")).toContainText("habits___3")
    await expect(history.locator("tbody")).toContainText("habits___2")
    await history.getByRole("button", { name: "Close" }).click()

    // A checked box is data of its instrument on the dashboard (§6.3).
    await page.getByRole("link", { name: "← Record Status Dashboard" }).click()
    await expect(page.locator("tbody[data-region='records'] tr[data-record='CB001'] td[data-state]"))
      .toHaveAttribute("data-state", "some_data")

    // --- export: one column per choice (REQ-EXP-053) ---
    const exported = async (extra: Record<string, string>) => {
      const answer = await request.post(`${API_URL}/api/`, {
        form: { token, content: "record", format: "json", "records[0]": "CB001", ...extra },
      })
      expect(answer.ok()).toBeTruthy()
      return ((await answer.json()) as Array<Record<string, string>>)[0] ?? {}
    }
    const raw = await exported({})
    expect(raw).not.toHaveProperty("habits")
    expect([raw.habits___1, raw.habits___2, raw.habits___3]).toEqual(["1", "1", "0"])
    // Label mode follows REDCap: Checked/Unchecked, the choice text with exportCheckboxLabel=true.
    const labels = await exported({ rawOrLabel: "label" })
    expect([labels.habits___1, labels.habits___2, labels.habits___3]).toEqual(["Checked", "Checked", "Unchecked"])
    const choiceLabels = await exported({ rawOrLabel: "label", exportCheckboxLabel: "true" })
    expect([choiceLabels.habits___1, choiceLabels.habits___2, choiceLabels.habits___3]).toEqual(["Smoking", "Drinking", ""])
  })
})
