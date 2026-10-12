import { expect, test, type Page } from "@playwright/test"

// The yesno and notes field types end to end (REQ-DB-043/044, REQ-UI-025): designed without a
// choice editor, entered as a Yes/No radio pair — branching reading the answer by its label —
// and as a multi-line text area, read back through the form, and exported (1/0 raw, Yes/No as
// label, the note's line breaks intact). Driven as the bootstrap administrator of
// `e2e/dev-stack.sh`; every name is unique per run.

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

test.describe("yes/no and notes fields (REQ-DB-043/044)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("design a yes/no and a notes field, enter and change them, read them back and export them", async ({ page, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `Field types ${tag}`

    await signInAsAdmin(page)

    // --- a project with one instrument, mapped to baseline (§6.2) ---
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "YN[0-9][0-9][0-9]")
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

    // --- the instrument: identifier, a yes/no, a notes field branched on the answer ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, async () => page.fill("#field-label", "Record ID"), "record_id")
    await addField(page, async () => {
      await page.fill("#field-label", "Do you smoke?")
      await page.selectOption("#field-type", "yesno")
      // The coding is fixed: no choice editor, no validation type (REQ-DB-043).
      await expect(page.locator("fieldset[data-clara-for-types]")).toBeHidden()
      await expect(page.locator("#field-validation")).toBeHidden()
    }, "smoker")
    await expect(page.locator('table.clara-fields tr[data-field="smoker"]')).toContainText("yesno")
    await addField(page, async () => {
      await page.fill("#field-label", "Smoking history")
      await page.selectOption("#field-type", "notes")
      await expect(page.locator("#field-validation")).toBeHidden()
      await page.fill("#field-branching", '[baseline_arm_1][smoker] = "Yes"')
    }, "history")

    // --- the acting user needs a project token for data entry (§8.6, REQ-API-102) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    const token = await page.locator("#clara-new-token").inputValue()

    // --- data entry: a Yes/No pair and a text area ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await page.getByRole("button", { name: "Auto-name" }).click()
    await expect(page.locator("#new-record-id")).toHaveValue("YN001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/YN001/)

    const form = page.locator("form[data-clara-record-form]")
    const smoker = form.locator('[data-clara-field="smoker"]')
    const answer = (label: string) => smoker.getByLabel(label, { exact: true })
    const notes = form.locator('[data-clara-field="history"]')
    await expect(smoker.locator("input[type=radio]")).toHaveCount(2)

    // Branching resolves the label "Yes" to its code 1 (§7.3).
    await expect(notes).toBeHidden()
    await answer("No").check()
    await expect(notes).toBeHidden()
    await answer("Yes").check()
    await expect(notes).toBeVisible()
    await expect(notes.locator("textarea")).toHaveCount(1)
    await page.fill("#f-history", "Started at 18.\nStopped in 2020.")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("Record YN001 created")

    // --- read back, then change the answer to No: 0 is stored, not a blank ---
    await page.reload()
    await expect(answer("Yes")).toBeChecked()
    await expect(page.locator("#f-history")).toHaveValue("Started at 18.\nStopped in 2020.")
    await answer("No").check()
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("saved")
    await expect(answer("No")).toBeChecked()

    // --- export: 1/0 raw, Yes/No as label, the note as typed ---
    const exported = async (extra: Record<string, string>) => {
      const reply = await request.post(`${API_URL}/api/`, {
        form: { token, content: "record", format: "json", "records[0]": "YN001", ...extra },
      })
      expect(reply.ok()).toBeTruthy()
      return ((await reply.json()) as Array<Record<string, string>>)[0] ?? {}
    }
    const raw = await exported({})
    expect(raw.smoker).toBe("0")
    expect(raw.history).toBe("Started at 18.\nStopped in 2020.")
    const labels = await exported({ rawOrLabel: "label" })
    expect(labels.smoker).toBe("No")
  })
})
