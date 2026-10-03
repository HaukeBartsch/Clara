import { expect, test, type Page } from "@playwright/test"

// M5 exit criterion (Plan/Web_Implementation.md §6) — charter success criterion 2: a data-entry
// user creates, reads and updates values through the UI; a partially filled instrument stores
// only the entered values and clears exactly what the user cleared (REQ-UI-031, GD-14). On the
// way it drives what REQ-TECH-028 names for the record form: client-side (advisory) validation
// and branching feedback, and the client half of the page/data-region split (REQ-UI-044) — the
// dashboard rows and the per-field history are both data regions of their page's route.
// Driven as the bootstrap administrator of `e2e/dev-stack.sh`; every name is unique per run.

const ADMIN_EMAIL = "admin@example.org"
const ADMIN_PASSWORD = "clara-dev-481516-pw" // dev-stack.sh: "${stack_seed}-pw", development only
// The data API of the same stack, read once at the end to prove what was stored — the browser
// itself never talks to it (REQ-UI-002).
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

test.describe("data entry and the record view (M5)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("create a participant, enter, read back and update values; partial saves store only what changed", async ({ page, request }) => {
    test.setTimeout(120_000)
    const tag = Date.now()
    const projectName = `M5 ${tag}`

    await signInAsAdmin(page)

    // --- a project with one instrument, mapped to baseline (§6.2) ---
    await page.locator('header a[href="/admin"]').click()
    await page.locator('#clara-nav a[href="/admin?section=projects"]').click()
    await page.locator("details.clara-create summary").click()
    await page.fill("#project-project_name", projectName)
    await page.selectOption("#project-organization", "HBE")
    await page.fill("#project-pi_name", "Principal Investigator")
    await page.fill("#project-pi_email", "pi@example.org")
    await page.fill("#project-participant_names", "M5[0-9][0-9][0-9]")
    await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
    await page.getByRole("link", { name: `Open ${projectName}` }).click()
    await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)
    const projectUrl = page.url().replace(/\/setup$/, "")

    const matrix = page.locator('form.clara-mapping[data-arm="1"]')
    const pair = matrix.locator('input[data-pair="instrument|baseline_arm_1"]')
    if (!(await pair.isChecked())) {
      await pair.check()
      await matrix.locator("button[type=submit]").click()
      await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")
    }

    // --- the instrument (§7.2): identifier, a required integer, a radio, a branched dropdown, free text ---
    await page.locator("#clara-nav").getByRole("link", { name: "Design" }).click()
    await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
    await addField(page, async () => page.fill("#field-label", "Record ID"), "record_id")
    await addField(page, async () => {
      await page.fill("#field-label", "Age")
      await page.selectOption("#field-validation", "integer")
      await page.fill("#field-min", "0")
      await page.fill("#field-max", "120")
      await page.check("#field-required")
    }, "age")
    await addField(page, async () => {
      await page.fill("#field-label", "Sex")
      await page.selectOption("#field-type", "radio")
      const codes = page.locator('input[name="choice_code[]"]')
      const labels = page.locator('input[name="choice_label[]"]')
      await codes.nth(0).fill("1")
      await labels.nth(0).fill("Male")
      await codes.nth(1).fill("2")
      await labels.nth(1).fill("Female")
    }, "sex")
    await addField(page, async () => {
      await page.fill("#field-label", "Pregnant")
      await page.selectOption("#field-type", "dropdown")
      const codes = page.locator('input[name="choice_code[]"]')
      const labels = page.locator('input[name="choice_label[]"]')
      await codes.nth(0).fill("0")
      await labels.nth(0).fill("No")
      await codes.nth(1).fill("1")
      await labels.nth(1).fill("Yes")
      await page.fill("#field-branching", '[baseline_arm_1][sex] = "2"')
    }, "preg")
    await addField(page, async () => page.fill("#field-label", "Notes"), "notes")
    await addField(page, async () => {
      await page.fill("#field-label", "Double age")
      await page.selectOption("#field-type", "calculated")
      await page.fill("#field-calculation", "[baseline_arm_1][age] * 2")
    }, "dbl")

    // --- the acting user needs a project token for data entry (§8.6, REQ-API-102) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Members" }).click()
    const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
    await page.locator("#member-user").selectOption(adminOption ?? "")
    await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
    const token = await page.locator("#clara-new-token").inputValue()

    // --- Record Status Dashboard: rows are the route's data region (REQ-UI-044) ---
    await page.locator("#clara-nav").getByRole("link", { name: "Record Status Dashboard" }).click()
    await expect(page.locator("tbody[data-region='records']")).toContainText("No records yet.")
    await page.getByRole("button", { name: "Auto-name" }).click()
    await expect(page.locator("#new-record-id")).toHaveValue("M5001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/M5001/)
    await expect(page.locator(".clara-record-new")).toBeVisible()

    const form = page.locator("form[data-clara-record-form]")
    const field = (name: string) => form.locator(`[data-clara-field="${name}"]`)

    // Advisory validation (REQ-VAL-002): a hint, never a block.
    await page.fill("#f-age", "abc")
    await expect(field("age").locator("[data-clara-hint]")).toHaveText("Expected a whole number.")
    await page.fill("#f-age", "130")
    await expect(field("age").locator("[data-clara-hint]")).toContainText("maximum of 120")
    await page.fill("#f-age", "")

    // Branching (§8.4): Pregnant shows only while sex = Female.
    await expect(field("preg")).toBeHidden()
    await form.getByLabel("Female", { exact: true }).check()
    await expect(field("preg")).toBeVisible()
    await form.getByLabel("Male", { exact: true }).check()
    await expect(field("preg")).toBeHidden()
    await form.getByLabel("Female", { exact: true }).check()

    // Required fields are checked before submission, and a partial form may still be saved
    // (REQ-VAL-028, GD-14): the first click is held with the list, the second saves.
    await page.selectOption("#f-preg", "1")
    await page.fill("#f-notes", "first visit")
    await form.locator("[data-clara-save]").click()
    await expect(form.locator("[data-clara-required-warning]")).toContainText("age")
    await page.fill("#f-age", "42")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("Record M5001 created")

    // --- read back: the form opens with the stored values (§8.3) ---
    await page.reload()
    await expect(page.locator("#f-age")).toHaveValue("42")
    await expect(form.getByLabel("Female", { exact: true })).toBeChecked()
    await expect(page.locator("#f-preg")).toHaveValue("1")
    await expect(page.locator("#f-notes")).toHaveValue("first visit")
    // The calculated result comes back through the history like any value (REQ-VAL-036).
    await expect(page.locator("output#f-dbl")).toHaveText("84")

    // The dashboard derives "some data" (amber) from the stored values (§6.3), and its
    // cell opens this very form.
    await page.getByRole("link", { name: "← Record Status Dashboard" }).click()
    const someData = page.locator("tbody[data-region='records'] tr[data-record='M5001'] td[data-state]")
    await expect(someData).toHaveAttribute("data-state", "some_data")
    await someData.locator("a").click()
    await expect(page).toHaveURL(/\/records\/M5001\?event=baseline_arm_1&instrument=instrument$/)

    // --- update: change one value, clear one, leave the rest untouched ---
    await page.fill("#f-age", "43")
    await page.fill("#f-notes", "")
    await page.selectOption("#record-completion", "finished")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("Record M5001 — instrument at baseline_arm_1 saved.")
    await expect(page.locator("#f-age")).toHaveValue("43")
    await expect(page.locator("#f-notes")).toHaveValue("")
    await expect(page.locator("output#f-dbl")).toHaveText("86")
    await expect(page.locator("#record-completion")).toHaveValue("finished")

    // Per-field history (§8.3, REQ-UI-026): the route's `history` region in a read-only modal.
    await field("age").locator("[data-clara-history]").click()
    const history = page.locator("#clara-history")
    await expect(history).toBeVisible()
    await expect(history.locator("tbody tr")).toHaveCount(2)
    await expect(history.locator("tbody tr").first()).toContainText("42 → 43")
    await history.getByRole("button", { name: "Close" }).click()

    // --- the dashboard shows the user's completion assignment as green (§6.3) ---
    await page.getByRole("link", { name: "← Record Status Dashboard" }).click()
    const cell = page.locator("tbody[data-region='records'] tr[data-record='M5001'] td[data-state]")
    await expect(cell).toHaveAttribute("data-state", "finished")

    // --- what is stored: the cleared value is gone, the untouched ones are intact (GD-14) ---
    const exported = await request.post(`${API_URL}/api/`, {
      form: { token, content: "record", action: "export", format: "json", type: "flat", "records[0]": "M5001" },
    })
    expect(exported.ok()).toBeTruthy()
    const rows = (await exported.json()) as Array<Record<string, string>>
    const row = rows.find((r) => r.redcap_event_name === "baseline_arm_1") ?? {}
    expect(row.age).toBe("43")
    expect(row.sex).toBe("2")
    expect(row.preg).toBe("1")
    expect(row.notes).toBe("")

    // --- a scoped delete keeps the participant (§8.7) ---
    await page.goto(`${projectUrl}/records/M5001`)
    await page.locator('.clara-record-delete button[data-scope="instrument"]').click()
    await page.locator("#clara-confirm [data-clara-confirm-ok]").click()
    await expect(success(page)).toContainText("were deleted from record M5001")
    await expect(page.locator("#f-age")).toHaveValue("")
    await expect(page.locator("#f-record_id")).toHaveValue("M5001")
  })
})
