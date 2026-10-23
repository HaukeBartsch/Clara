import { expect, test, type Page } from "@playwright/test"

// The transfer field type end to end (REQ-DB-050, REQ-UI-065, REQ-API-165): designed as raw
// copy instructions in the field editor, executed as a confirmed button in data entry, and
// read back in the other project through its own form. A second click updates the target
// record instead of creating another one; an unknown target project is refused already at
// design time. The deep authorization matrix (read-only members, pair-level grants, DAGs) is
// covered by the Go tests (`api/internal/admin/transfer_test.go`) — this spec drives what a
// person sees and clicks, as the bootstrap administrator of `e2e/dev-stack.sh`.

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
const danger = (page: Page) => page.locator(".alert-danger").last()

/** Creates a project through the Control Panel and returns its id from the setup URL. */
async function createProject(page: Page, name: string): Promise<number> {
  await page.locator('header a[href="/admin"]').click()
  await page.locator("details.clara-create summary").click()
  await page.fill("#project-project_name", name)
  await page.selectOption("#project-organization", "OUS")
  await page.fill("#project-pi_name", "Principal Investigator")
  await page.fill("#project-pi_email", "pi@example.org")
  await page.locator('form[action*="action=create_project"] button[type="submit"]').click()
  await page.getByRole("link", { name: `Open ${name}` }).click()
  await expect(page).toHaveURL(/\/projects\/\d+\/setup$/)
  return Number(new URL(page.url()).pathname.match(/\/projects\/(\d+)\//)?.[1] ?? "0")
}

/** Maps the default instrument to baseline_arm_1 unless it is mapped already. */
async function mapToBaseline(page: Page): Promise<void> {
  const matrix = page.locator('form.clara-mapping[data-arm="1"]')
  const pair = matrix.locator('input[data-pair="instrument|baseline_arm_1"]')
  if (!(await pair.isChecked())) {
    await pair.check()
    await matrix.locator("button[type=submit]").click()
    await expect(success(page)).toContainText("Instrument assignment of arm 1 saved.")
  }
}

/** Adds the administrator as a plain member (data entry needs it, §8.6 — like fieldtypes.spec). */
async function joinProject(page: Page, projectId: number): Promise<void> {
  await page.goto(`/projects/${projectId}/members`)
  const adminOption = await page.locator("#member-user option", { hasText: ADMIN_EMAIL }).getAttribute("value")
  await page.locator("#member-user").selectOption(adminOption ?? "")
  await page.locator('form[action*="action=add_member"] button[type="submit"]').click()
}

async function addField(page: Page, projectId: number, fill: () => Promise<void>, name: string): Promise<void> {
  await page.goto(`/projects/${projectId}/design`)
  await page.locator("table.clara-design-instruments").getByRole("link", { name: "instrument", exact: true }).click()
  await page.getByRole("link", { name: "Add field" }).click()
  await page.fill("#field-name", name)
  await fill()
  await page.locator("#field-form button[type=submit]").click()
}

test.describe("transfer fields (REQ-UI-065, REQ-API-165)", () => {
  test.skip(!process.env.CLARA_WEB_URL, "CLARA_WEB_URL not set — no running web/ application to drive")

  test("design a transfer field, run it twice, and read the target record in its own project", async ({ page }) => {
    test.setTimeout(180_000)
    const tag = Date.now()
    const tgtName = `Xfer Tgt ${tag}`
    const srcName = `Xfer Src ${tag}`

    await signInAsAdmin(page)

    // --- the target: identifier `record_id`, one value field, baseline event (§6.2) ---
    const tgtId = await createProject(page, tgtName)
    await mapToBaseline(page)
    await joinProject(page, tgtId)
    for (const [name, label] of [["record_id", "Record ID"], ["age_in", "Age in target"]] as const) {
      await addField(page, tgtId, async () => page.fill("#field-label", label), name)
      await expect(success(page)).toContainText(`Field "${name}" added.`)
    }

    // --- the source: identifier `record_name`, a plain `age`, and the transfer field ---
    const srcId = await createProject(page, srcName)
    await mapToBaseline(page)
    await joinProject(page, srcId)
    for (const [name, label] of [["record_name", "Record name"], ["age", "Age"]] as const) {
      await addField(page, srcId, async () => page.fill("#field-label", label), name)
      await expect(success(page)).toContainText(`Field "${name}" added.`)
    }

    // The transfer editor: raw instructions instead of the choice rows (REQ-DB-050).
    const config = `create_record [${tgtName}][record_id] record_name # the new record's name\ncopy age [${tgtName}][baseline][age_in]`
    await addField(page, srcId, async () => {
      await page.fill("#field-label", "Handoff")
      await page.selectOption("#field-type", "transfer")
      await expect(page.locator("fieldset[data-clara-for-types]")).toBeHidden()
      await expect(page.locator("#field-validation")).toBeHidden()
      await expect(page.locator("#field-transfer")).toBeVisible()
      await page.fill("#field-transfer", config)
    }, "xfer")
    await expect(success(page)).toContainText('Field "xfer" added.')

    // Design time already knows both projects: an unknown target names itself (REQ-DB-050).
    await addField(page, srcId, async () => {
      await page.fill("#field-label", "Ghost handoff")
      await page.selectOption("#field-type", "transfer")
      await page.fill("#field-transfer", "create_record [Ghost Study 42][record_id] record_name")
    }, "xfer_ghost")
    await expect(danger(page)).toContainText(`no project named 'Ghost Study 42'`)

    // --- data entry: no button before the record exists, one after (§8.3) ---
    await page.goto(`/projects/${srcId}/record-status`)
    await page.fill("#new-record-id", "SR001")
    await page.getByRole("button", { name: "Create" }).click()
    await expect(page).toHaveURL(/\/records\/SR001/)
    await expect(page.locator(".clara-transfer")).toHaveCount(0)

    const form = page.locator("form[data-clara-record-form]")
    await page.fill("#f-age", "42")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("Record SR001 created")

    // The button renders where the availability probe said yes (REQ-API-166), and confirms
    // its consequence before running (§3.5).
    const transferButton = page.locator(".clara-transfer button", { hasText: "Transfer" })
    await expect(transferButton).toBeVisible()
    await transferButton.click()
    await expect(page.locator("#clara-confirm")).toBeVisible()
    await page.locator("[data-clara-confirm-ok]").click()
    await expect(success(page)).toContainText(`copied to record SR001 in project ${tgtName}`)

    // The target holds the values, read through its own form.
    await page.goto(`/projects/${tgtId}/records/SR001`)
    await expect(page.locator("#f-age_in")).toHaveValue("42")

    // --- a second run updates: same record, new value, no duplicate ---
    await page.goto(`/projects/${srcId}/records/SR001`)
    await page.fill("#f-age", "43")
    await form.locator("[data-clara-save]").click()
    await expect(success(page)).toContainText("saved")
    await transferButton.click()
    await page.locator("[data-clara-confirm-ok]").click()
    await expect(success(page)).toContainText(`copied to record SR001 in project ${tgtName}`)

    await page.goto(`/projects/${tgtId}/records/SR001`)
    await expect(page.locator("#f-age_in")).toHaveValue("43")
  })
})
